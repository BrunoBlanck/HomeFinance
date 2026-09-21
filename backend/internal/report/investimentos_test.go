package report_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 0006 §3.5.1 e ADR-029(e)(h): o relatório por categoria DESCARTA os
// lançamentos cuja categoria é de natureza `investment`/`redemption` — dos
// totais e das linhas. O balde "Sem categoria" não muda, e o enum de `kind`
// continua `income|expense`: investimento é tela própria, não uma natureza a
// mais do relatório.

// O caso da spec: o aporte de R$ 2.000 some do relatório de despesas, e o
// total do mês passa a ser só o que sobrou.
func TestByCategoryDescartaAporteDosTotaisEDasLinhas(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	mercado := a.categoria(minhaCasa, "g-mercado", "Mercado", category.KindExpense, nil, false)
	investimentos := a.categoria(minhaCasa, "g-invest", "Investimentos", category.KindInvestment, nil, false)
	cdb := a.categoria(minhaCasa, "f-cdb", "CDB", category.KindInvestment, ptr(investimentos.ID), false)

	a.ledger.rows = []report.CategoryAccountTotal{
		linha(ptr(mercado.ID), 300_00, 3),
		linha(ptr(investimentos.ID), 500_00, 1),
		linha(ptr(cdb.ID), 2_000_00, 1),
	}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	assert.Equal(t, int64(300_00), v.TotalCents, "o aporte não é despesa deste relatório")
	assert.Equal(t, int64(3), v.Count, "a contagem sai junto com o valor")
	require.Len(t, v.Items, 1, "nem o grupo de investimento nem a folha dele viram linha")
	assert.Equal(t, mercado.ID, *v.Items[0].CategoryID)
	assert.Equal(t, int64(report.BasisPointsTotal), v.Items[0].ShareBp,
		"o que sobrou reparte 100%% entre si — o descartado não deixa buraco na fatia")
}

// Resgate sai do relatório de RECEITAS pela mesma regra e pelo mesmo caminho.
func TestByCategoryDescartaResgateDoRelatorioDeReceitas(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	salario := a.categoria(minhaCasa, "g-salario", "Salário", category.KindIncome, nil, false)
	resgates := a.categoria(minhaCasa, "g-resgate", "Resgates", category.KindRedemption, nil, false)

	a.ledger.rows = []report.CategoryAccountTotal{
		linha(ptr(salario.ID), 5_000_00, 1),
		linha(ptr(resgates.ID), 500_00, 1),
	}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09", Kind: "income"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	assert.Equal(t, int64(5_000_00), v.TotalCents)
	require.Len(t, v.Items, 1)
	assert.Equal(t, salario.ID, *v.Items[0].CategoryID)
}

// A folha marcada some SOZINHA, sem levar o grupo junto: o grupo de despesa
// continua com o que é dele. (Estado irregular — a folha herda a natureza do
// grupo, ADR-017b —, mas a regra é do lançamento, e a linha dele é a folha.)
func TestByCategoryDescartaSoALinhaMarcadaEMantemOResto(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	casa := a.categoria(minhaCasa, "g-casa", "Casa", category.KindExpense, nil, false)
	luz := a.categoria(minhaCasa, "f-luz", "Luz", category.KindExpense, ptr(casa.ID), false)
	marcada := a.categoria(minhaCasa, "f-marcada", "Aporte", category.KindInvestment, ptr(casa.ID), false)

	a.ledger.rows = []report.CategoryAccountTotal{
		linha(ptr(casa.ID), 100_00, 1),
		linha(ptr(luz.ID), 200_00, 2),
		linha(ptr(marcada.ID), 2_000_00, 1),
	}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	assert.Equal(t, int64(300_00), v.TotalCents)
	require.Len(t, v.Items, 1)
	require.Len(t, v.Items[0].Children, 1, "a folha marcada não vira filha do grupo")
	assert.Equal(t, luz.ID, v.Items[0].Children[0].CategoryID)
	assert.Equal(t, int64(100_00), v.Items[0].DirectCents, "o direto do grupo não absorve o descartado")
}

// Categoria marcada e ARQUIVADA continua sendo descartada: arquivar não
// desfaz a marcação do passado (PLANOS.md §4.4), e a lista que o relatório
// carrega já pede includeArchived = true.
func TestByCategoryDescartaMarcadaArquivada(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	mercado := a.categoria(minhaCasa, "g-mercado", "Mercado", category.KindExpense, nil, false)
	poupanca := a.categoria(minhaCasa, "g-poupanca", "Poupança", category.KindInvestment, nil, true)

	a.ledger.rows = []report.CategoryAccountTotal{
		linha(ptr(mercado.ID), 300_00, 3),
		linha(ptr(poupanca.ID), 1_000_00, 1),
	}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.Equal(t, int64(300_00), v.TotalCents)
	require.Len(t, v.Items, 1)

	require.Len(t, a.cats.chamadas, 1, "UMA leitura de categorias, a que o pacote já fazia")
	assert.True(t, a.cats.chamadas[0].includeArchived)
}

// O balde "Sem categoria" NÃO muda: a linha nula continua inteira. Descartar
// aporte nunca pode virar pendência de categorização — ele tem categoria.
func TestByCategoryNaoMexeNoBaldeSemCategoria(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	investimentos := a.categoria(minhaCasa, "g-invest", "Investimentos", category.KindInvestment, nil, false)
	a.ledger.rows = []report.CategoryAccountTotal{
		linha(nil, 150_00, 4),
		linha(ptr(investimentos.ID), 2_000_00, 1),
	}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	require.Len(t, v.Items, 1)
	assert.Nil(t, v.Items[0].CategoryID, "o balde continua sendo o balde")
	assert.Equal(t, int64(150_00), v.Items[0].TotalCents)
	assert.Equal(t, int64(4), v.Items[0].Count)
	assert.Equal(t, int64(150_00), v.TotalCents)
}

// Mês em que TUDO é aporte: o relatório responde vazio e bem formado — zeros,
// `items: []` (nunca null) e nenhum share inventado.
func TestByCategoryComTudoMarcadoRespondeVazioBemFormado(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	investimentos := a.categoria(minhaCasa, "g-invest", "Investimentos", category.KindInvestment, nil, false)
	a.ledger.rows = []report.CategoryAccountTotal{linha(ptr(investimentos.ID), 2_000_00, 1)}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	assert.Equal(t, int64(0), v.TotalCents)
	assert.Equal(t, int64(0), v.Count)
	require.NotNil(t, v.Items)
	assert.Empty(t, v.Items)
}

// O enum de `kind` do parâmetro continua fechado em income|expense: pedir o
// relatório "de investimento" é 400, não uma terceira natureza (ADR-029h).
func TestByCategoryNaoAceitaNaturezaDeInvestimentoComoKind(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	for _, kind := range []string{category.KindInvestment, category.KindRedemption} {
		_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09", Kind: kind})
		require.ErrorIs(t, err, report.ErrInvalidKind, "kind=%s", kind)
	}
	assert.Empty(t, a.ledger.chamadas, "a natureza é conferida ANTES de qualquer consulta")
}
