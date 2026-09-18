package report_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Critério 18 da E7, a metade que faltava: o aporte de R$ 2.000 tem de
// **aparecer em `GET /investments`** com o MESMO valor que saiu do relatório e
// de `expenseCents`.
//
// `TestAporteSomeDoRelatorioEDaDespesaSemMexerNoSaldo` já provava as quatro
// primeiras respostas (relatório, `expenseCents`, `investedCents`, saldo).
// Faltava a quinta, e ela é a que fecha o círculo: o número que a tela de
// investimentos mostra nasce de uma consulta PRÓPRIA (SumInvestmentsByMonth,
// com `GROUP BY competence_month, kind`), e não da mesma que produz o
// `summary` (uma projeção condicional dentro do `GROUP BY kind`). São dois
// SQL diferentes sobre a mesma linha — exatamente o par que pode divergir sem
// que ninguém perceba, e que nenhum dublê revelaria.
//
// Contra SQLite de verdade, pelo mesmo motivo do arquivo vizinho.
func TestAporteAparecePorInteiroNaTelaDeInvestimentos(t *testing.T) {
	t.Parallel()

	p := novaPilhaCruzada(t)
	ctx := t.Context()
	atorTx := transaction.Actor{HouseholdID: casaCruzada, UserID: donoDaCasa, IP: "203.0.113.7"}
	atorRel := report.Actor{HouseholdID: casaCruzada, UserID: donoDaCasa}
	atorInv := investment.Actor{HouseholdID: casaCruzada, UserID: donoDaCasa, IP: "203.0.113.7"}

	mercado := p.criarCategoria(t, "00000000-0000-7000-c000-000000000011", "Mercado", category.KindExpense)
	aportes := p.criarCategoria(t, "00000000-0000-7000-c000-000000000012", "Investimentos", category.KindExpense)

	p.lancar(t, mercado.ID, 300_00, "Mercado do bairro", 6)
	idDoAporte := p.lancar(t, aportes.ID, 2_000_00, "CDB 15 DIAS", 10)

	// --- ANTES: a tela de investimentos não conhece a linha ----------------
	//
	// A casa ainda não tem NENHUMA categoria de natureza de investimento, e é
	// o estado de toda casa no dia da entrega: zeros, série de 12 e `items`
	// vazio, sem que uma consulta de lançamento sequer aconteça (ADR-029f).
	antes, err := p.investimentos.Overview(ctx, atorInv, investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.Equal(t, investment.TotalsView{}, antes.Monthly)
	assert.Equal(t, investment.TotalsView{}, antes.YearToDate)
	assert.Len(t, antes.Series, investment.SeriesMonths)
	assert.Empty(t, antes.Items)

	// --- A marcação: a natureza da categoria vira `investment` -------------
	marcada := aportes
	marcada.Kind = category.KindInvestment
	marcada.UpdatedAt = p.momento
	require.NoError(t, p.categorias.Update(ctx, &marcada))

	// --- DEPOIS: as três leituras precisam contar a MESMA história ---------
	relDepois, err := p.relatorios.ByCategory(ctx, atorRel, report.ByCategoryInput{Month: "2026-09", Kind: "expense"})
	require.NoError(t, err)
	listaDepois, err := p.lancamentos.List(ctx, atorTx, transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)
	depois, err := p.investimentos.Overview(ctx, atorInv, investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)

	// O número da tela de investimentos é, centavo a centavo, o que saiu do
	// resumo de lançamentos — e as duas consultas são diferentes.
	assert.Equal(t, int64(2_000_00), depois.Monthly.ContributionsCents)
	assert.Equal(t, int64(1), depois.Monthly.ContributionCount)
	assert.Equal(t, listaDepois.Summary.InvestedCents, depois.Monthly.ContributionsCents,
		"investedCents do summary e o aporte do mês saem de consultas diferentes: elas não podem divergir")

	// Resgate é zero: o `flow` vem do `kind` do LANÇAMENTO, e a linha é
	// despesa. Se ele viesse da natureza da categoria, esta asserção cairia.
	assert.Equal(t, int64(0), depois.Monthly.RedemptionsCents)
	assert.Equal(t, int64(0), depois.Monthly.RedemptionCount)
	assert.Equal(t, listaDepois.Summary.RedeemedCents, depois.Monthly.RedemptionsCents)

	// O ano-até-o-mês repete o mês, porque só há setembro.
	assert.Equal(t, depois.Monthly, depois.YearToDate)

	// A série tem 12 itens e o ÚLTIMO é o mês pedido, com o mesmo valor.
	require.Len(t, depois.Series, investment.SeriesMonths)
	ultimo := depois.Series[investment.SeriesMonths-1]
	assert.Equal(t, "2026-09", ultimo.Month)
	assert.Equal(t, int64(2_000_00), ultimo.ContributionsCents)
	assert.Equal(t, int64(0), ultimo.RedemptionsCents)
	// Os outros 11 estão zerados e presentes: buraco na série é contrato
	// quebrado, não "mês sem movimento".
	var somaDosOutros int64
	for _, ponto := range depois.Series[:investment.SeriesMonths-1] {
		somaDosOutros += ponto.ContributionsCents + ponto.RedemptionsCents
		assert.NotEmpty(t, ponto.Month)
	}
	assert.Equal(t, int64(0), somaDosOutros)

	// E a LINHA aparece na lista da tela, com o rótulo de aporte, a conta e a
	// categoria — é ela que sustenta a mitigação do ADR-029(i): o gasto
	// marcado como investimento não desaparece, ele muda de tela.
	require.Len(t, depois.Items, 1)
	item := depois.Items[0]
	assert.Equal(t, idDoAporte, item.ID)
	assert.Equal(t, investment.FlowContribution, item.Flow, "despesa marcada é APORTE (o dinheiro saiu)")
	assert.Equal(t, int64(2_000_00), item.AmountCents)
	assert.Equal(t, "CDB 15 DIAS", item.Description)
	assert.Equal(t, marcada.ID, item.CategoryID)
	assert.Equal(t, "Investimentos", item.CategoryName)
	assert.Equal(t, contaCruzada, item.AccountID)
	assert.Equal(t, "Conta corrente", item.AccountName, "a lista rotula a conta")
	assert.Nil(t, depois.NextCursor)

	// O fecho aritmético entre as TRÊS leituras, numa linha: o que o relatório
	// perdeu é o que o resumo marcou, e é o que a tela de investimentos soma.
	assert.Equal(t, int64(300_00), relDepois.TotalCents)
	assert.Equal(t,
		listaDepois.Summary.ExpenseCents+depois.Monthly.ContributionsCents,
		relDepois.TotalCents+depois.Monthly.ContributionsCents,
		"relatório e resumo continuam falando do mesmo mês")

	// E a linha do Mercado, que não é aporte, NÃO aparece na tela de
	// investimentos — a tela mostra o que foi marcado, e só.
	assert.Equal(t, int64(300_00), listaDepois.Summary.ExpenseCents)
}
