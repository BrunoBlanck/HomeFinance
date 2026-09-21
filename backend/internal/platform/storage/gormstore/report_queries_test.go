package gormstore_test

import (
	"context"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Este arquivo cobre a consulta do relatório por categoria (ADR-027b, ADR-032)
// contra banco real, porque o que importa provar é o WHERE: casa,
// competência, natureza e deleted_at IS NULL — que NULL em category_id agrupa
// numa linha só, e que a chave (categoria, conta) somada de volta por
// categoria é a agregação antiga.

// porCategoria DOBRA o resultado por categoria ("" para a linha nula),
// somando as linhas das várias contas — é a agregação antiga (`GROUP BY
// category_id`), reconstruída a partir da nova (ADR-032), e é assim que os
// testes que só se importam com a categoria continuam lendo a mesma coisa.
func porCategoria(rows []report.CategoryAccountTotal) map[string]report.CategoryAccountTotal {
	out := make(map[string]report.CategoryAccountTotal, len(rows))
	for _, r := range rows {
		chave := ""
		if r.CategoryID != nil {
			chave = *r.CategoryID
		}
		acc := out[chave]
		acc.CategoryID = r.CategoryID
		acc.TotalCents += r.TotalCents
		acc.Count += r.Count
		out[chave] = acc
	}
	return out
}

// porCategoriaEConta indexa o resultado pela chave (categoria, conta), com ""
// para a categoria nula — uma linha por chave, que é o que o GROUP BY
// promete.
func porCategoriaEConta(t *testing.T, rows []report.CategoryAccountTotal) map[[2]string]report.CategoryAccountTotal {
	t.Helper()
	out := make(map[[2]string]report.CategoryAccountTotal, len(rows))
	for _, r := range rows {
		cat := ""
		if r.CategoryID != nil {
			cat = *r.CategoryID
		}
		chave := [2]string{cat, r.AccountID}
		_, repetida := out[chave]
		require.False(t, repetida, "GROUP BY devolveu a chave %v duas vezes", chave)
		out[chave] = r
	}
	return out
}

// makeAccountKind insere uma conta do tipo pedido, arquivada ou não. O helper
// makeAccount só cria conta corrente viva, e o recorte precisa dos cinco
// tipos e do arquivamento.
func (s *store) makeAccountKind(t *testing.T, ctx context.Context, householdID, name, kind string, arquivada bool) *account.Account {
	t.Helper()
	nome, norm, err := account.NormalizeName(name)
	require.NoError(t, err)
	a := &account.Account{
		ID: s.nextID("a"), HouseholdID: householdID, Name: nome, NameNorm: norm, Kind: kind,
		OpeningBalanceCents: 0, OpeningDate: civil.MustNew(2026, 1, 1),
		CreatedAt: now(), UpdatedAt: now(),
	}
	if arquivada {
		at := now()
		a.ArchivedAt = &at
	}
	require.NoError(t, s.accounts.Create(ctx, a))
	return a
}

// Critério 12: DUAS casas povoadas no MESMO mês, com categorias e valores
// diferentes — a agregação de uma nunca contém nada da outra, em nenhuma
// direção. Também: excluída sai (5), transferência não entra (4), outro mês e
// outra natureza não entram, e a linha nula agrupa os sem categoria.
func TestSumByCategoryIsolaCasasEFiltraJanela(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		outraConta := s.makeAccount(t, ctx, minha.ID, "Poupança")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		padaria := s.makeCategory(t, ctx, minha.ID, "Padaria", category.KindExpense, &mercado.ID)
		salario := s.makeCategory(t, ctx, minha.ID, "Salário", category.KindIncome, nil)
		mercadoAlheio := s.makeCategory(t, ctx, alheia.ID, "Mercado", category.KindExpense, nil)

		set := civil.MustNew(2026, 9, 10)

		// Minha casa, setembro, despesas.
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 1_000, OccurredOn: set, CategoryID: &mercado.ID})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 2_000, OccurredOn: set, CategoryID: &mercado.ID})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 300, OccurredOn: set, CategoryID: &padaria.ID})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 50, OccurredOn: set})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 70, OccurredOn: set})
		// Competência deslocada (fatura): occurred_on em agosto, competência em setembro — ENTRA.
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 400, OccurredOn: civil.MustNew(2026, 8, 28), CompetenceMonth: "2026-09", CategoryID: &padaria.ID})
		// Caixa em setembro, competência em outubro — NÃO entra.
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 9_000, OccurredOn: set, CompetenceMonth: "2026-10", CategoryID: &mercado.ID})
		// Excluída — NÃO entra.
		excluida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 8_000, OccurredOn: set, CategoryID: &mercado.ID})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		// Receita — não entra em expense.
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Kind: transaction.KindIncome, AmountCents: 500_000, OccurredOn: set, CategoryID: &salario.ID})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Kind: transaction.KindIncome, AmountCents: 25, OccurredOn: set})
		// Transferência — nunca entra (ADR-016).
		grupo := s.nextID("grp")
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Kind: transaction.KindTransferOut, AmountCents: 7_000, OccurredOn: set, TransferGroupID: &grupo})
		s.makeTransaction(t, ctx, minha.ID, outraConta.ID, txSpec{Kind: transaction.KindTransferIn, AmountCents: 7_000, OccurredOn: set, TransferGroupID: &grupo})

		// Casa vizinha, MESMO mês, valores marcantes.
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{AmountCents: 111_111, OccurredOn: set, CategoryID: &mercadoAlheio.ID})
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{AmountCents: 222_222, OccurredOn: set})

		// --- minha casa, expense --------------------------------------------
		rows, err := s.transactions.SumByCategoryAndAccount(ctx, minha.ID, "2026-09", transaction.KindExpense)
		require.NoError(t, err)
		got := porCategoria(rows)
		require.Len(t, got, 3, "mercado, padaria e a linha nula")
		assert.Equal(t, int64(3_000), got[mercado.ID].TotalCents)
		assert.Equal(t, int64(2), got[mercado.ID].Count)
		assert.Equal(t, int64(700), got[padaria.ID].TotalCents)
		assert.Equal(t, int64(2), got[padaria.ID].Count)
		assert.Equal(t, int64(120), got[""].TotalCents, "NULL agrupa numa linha só")
		assert.Equal(t, int64(2), got[""].Count)
		_, vazou := got[mercadoAlheio.ID]
		assert.False(t, vazou, "categoria da vizinha não aparece")
		var somaMinha int64
		for _, r := range rows {
			somaMinha += r.TotalCents
		}
		assert.Equal(t, int64(3_820), somaMinha, "sem 111111, 222222, 9000, 8000, 7000 nem 500000")

		// --- minha casa, income ---------------------------------------------
		rows, err = s.transactions.SumByCategoryAndAccount(ctx, minha.ID, "2026-09", transaction.KindIncome)
		require.NoError(t, err)
		got = porCategoria(rows)
		require.Len(t, got, 2)
		assert.Equal(t, int64(500_000), got[salario.ID].TotalCents)
		assert.Equal(t, int64(25), got[""].TotalCents)

		// --- casa vizinha: só o dela ----------------------------------------
		rows, err = s.transactions.SumByCategoryAndAccount(ctx, alheia.ID, "2026-09", transaction.KindExpense)
		require.NoError(t, err)
		got = porCategoria(rows)
		require.Len(t, got, 2)
		assert.Equal(t, int64(111_111), got[mercadoAlheio.ID].TotalCents)
		assert.Equal(t, int64(222_222), got[""].TotalCents)
		_, vazou = got[mercado.ID]
		assert.False(t, vazou)

		// --- mês em que só há transferência: vazio ----------------------------
		nov := civil.MustNew(2026, 11, 3)
		grupo2 := s.nextID("grp")
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Kind: transaction.KindTransferOut, AmountCents: 100, OccurredOn: nov, TransferGroupID: &grupo2})
		s.makeTransaction(t, ctx, minha.ID, outraConta.ID, txSpec{Kind: transaction.KindTransferIn, AmountCents: 100, OccurredOn: nov, TransferGroupID: &grupo2})
		rows, err = s.transactions.SumByCategoryAndAccount(ctx, minha.ID, "2026-11", transaction.KindExpense)
		require.NoError(t, err)
		assert.Empty(t, rows)

		// --- guardas: vazio é erro, nunca varredura ---------------------------
		for _, c := range []struct{ casa, mes, kind string }{
			{"", "2026-09", transaction.KindExpense},
			{minha.ID, "", transaction.KindExpense},
			{minha.ID, "2026-09", ""},
		} {
			_, err := s.transactions.SumByCategoryAndAccount(ctx, c.casa, c.mes, c.kind)
			assert.Error(t, err, "%+v", c)
		}

		// Natureza que não existe no banco (transferência pedida "por
		// engano"): o serviço barra antes, mas o repositório responde vazio,
		// nunca erro nem outra coisa.
		rows, err = s.transactions.SumByCategoryAndAccount(ctx, minha.ID, "2026-09", "bogus")
		require.NoError(t, err)
		assert.Empty(t, rows)
	})
}

// Critério 3: o relatório e o `summary` de GET /transactions são a MESMA
// janela. totalCents(expense) == ExpenseCents, totalCents(income) ==
// IncomeCents, e balde(expense).count + balde(income).count ==
// Uncategorized — ponta a ponta, com o serviço de relatório sobre os
// repositórios reais.
func TestRelatorioPorCategoriaBateComSummary(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		outraConta := s.makeAccount(t, ctx, minha.ID, "Poupança")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		casa := s.makeCategory(t, ctx, minha.ID, "Casa", category.KindExpense, nil)
		luz := s.makeCategory(t, ctx, minha.ID, "Luz", category.KindExpense, &casa.ID)
		lazer := s.makeCategory(t, ctx, minha.ID, "Lazer", category.KindExpense, nil)
		salario := s.makeCategory(t, ctx, minha.ID, "Salário", category.KindIncome, nil)
		// Arquivada: continua contando, com archivedAt (critério 5).
		antiga := s.makeCategory(t, ctx, minha.ID, "Antiga", category.KindExpense, nil)
		arquivadaEm := now()
		antiga.ArchivedAt = &arquivadaEm
		antiga.UpdatedAt = arquivadaEm
		require.NoError(t, s.categories.Update(ctx, antiga))

		set := civil.MustNew(2026, 9, 15)
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 12_000, OccurredOn: set, CategoryID: &luz.ID})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 3_000, OccurredOn: set, CategoryID: &casa.ID})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 5_000, OccurredOn: set, CategoryID: &lazer.ID})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 1_000, OccurredOn: set, CategoryID: &antiga.ID})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 200, OccurredOn: set})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 300, OccurredOn: set})
		// Valor zero (critério 8) — direto pelo repositório, porque o helper
		// troca 0 pelo default.
		require.NoError(t, s.transactions.CreateBatch(ctx, minha.ID, []transaction.Transaction{{
			ID: s.nextID("t"), HouseholdID: minha.ID, Kind: transaction.KindExpense, AccountID: conta.ID,
			CategoryID: &lazer.ID, AmountCents: 0, Description: "Estorno zerado", DescriptionNorm: "estorno zerado",
			OccurredOn: set, CompetenceMonth: "2026-09", Source: transaction.SourceManual,
			DedupKey: s.nextID("dk"), DedupOrdinal: 1, CreatedBy: "u", CreatedAt: now(), UpdatedAt: now(),
		}}))
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Kind: transaction.KindIncome, AmountCents: 700_000, OccurredOn: set, CategoryID: &salario.ID})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Kind: transaction.KindIncome, AmountCents: 1_500, OccurredOn: set})
		excluida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{AmountCents: 99_999, OccurredOn: set, CategoryID: &casa.ID})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		grupo := s.nextID("grp")
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Kind: transaction.KindTransferOut, AmountCents: 40_000, OccurredOn: set, TransferGroupID: &grupo})
		s.makeTransaction(t, ctx, minha.ID, outraConta.ID, txSpec{Kind: transaction.KindTransferIn, AmountCents: 40_000, OccurredOn: set, TransferGroupID: &grupo})
		// Vizinha no mesmo mês.
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{AmountCents: 555_555, OccurredOn: set})

		svc := report.NewService(s.transactions, s.categories, s.accounts, logging.Discard())
		ator := report.Actor{HouseholdID: minha.ID, UserID: "u"}

		resumo, err := s.transactions.Summary(ctx, minha.ID, transaction.SummaryFilter{CompetenceMonth: "2026-09"})
		require.NoError(t, err)

		despesas, err := svc.ByCategory(ctx, ator, report.ByCategoryInput{Month: "2026-09", Kind: "expense"})
		require.NoError(t, err)
		receitas, err := svc.ByCategory(ctx, ator, report.ByCategoryInput{Month: "2026-09", Kind: "income"})
		require.NoError(t, err)

		assert.Equal(t, resumo.ExpenseCents, despesas.TotalCents, "expense bate com summary")
		assert.Equal(t, int64(21_500), despesas.TotalCents)
		assert.Equal(t, resumo.IncomeCents, receitas.TotalCents, "income bate com summary")
		assert.Equal(t, int64(701_500), receitas.TotalCents)
		// count do relatório exclui transferências; o do summary as inclui.
		assert.Equal(t, resumo.Count-2, despesas.Count+receitas.Count)

		balde := func(v report.CategoryReportView) report.CategoryReportGroupView {
			for _, it := range v.Items {
				if it.CategoryID == nil {
					return it
				}
			}
			t.Fatalf("relatório %s sem balde", v.Kind)
			return report.CategoryReportGroupView{}
		}
		assert.Equal(t, resumo.Uncategorized, balde(despesas).Count+balde(receitas).Count, "sem categoria bate com summary")
		assert.Equal(t, int64(500), balde(despesas).TotalCents)
		assert.Equal(t, int64(1_500), balde(receitas).TotalCents)

		// Árvore: Casa (15.000 = Luz 12.000 + direto 3.000) > Lazer (5.000,
		// 2 lançamentos, um deles zero) > Antiga (1.000, arquivada) > balde (500).
		require.Len(t, despesas.Items, 4)
		casaV := despesas.Items[0]
		require.NotNil(t, casaV.CategoryID)
		assert.Equal(t, casa.ID, *casaV.CategoryID)
		assert.Equal(t, int64(15_000), casaV.TotalCents)
		assert.Equal(t, int64(3_000), casaV.DirectCents)
		require.Len(t, casaV.Children, 1)
		assert.Equal(t, luz.ID, casaV.Children[0].CategoryID)
		assert.Equal(t, int64(12_000), casaV.Children[0].TotalCents)

		lazerV := despesas.Items[1]
		assert.Equal(t, lazer.ID, *lazerV.CategoryID)
		assert.Equal(t, int64(5_000), lazerV.TotalCents)
		assert.Equal(t, int64(2), lazerV.Count, "o lançamento de valor zero conta")
		assert.Empty(t, lazerV.Children)

		antigaV := despesas.Items[2]
		assert.Equal(t, antiga.ID, *antigaV.CategoryID)
		require.NotNil(t, antigaV.ArchivedAt, "arquivada aparece com archivedAt")
		assert.Nil(t, casaV.ArchivedAt)

		assert.Nil(t, despesas.Items[3].CategoryID)

		// Shares fecham em 10000 e por grupo.
		var soma int64
		for _, it := range despesas.Items {
			soma += it.ShareBp
			var filhas int64
			for _, f := range it.Children {
				filhas += f.ShareBp
			}
			assert.Equal(t, it.ShareBp, it.DirectShareBp+filhas)
		}
		assert.Equal(t, int64(10_000), soma)

		// A vizinha, pelo MESMO serviço, só vê o dela.
		daVizinha, err := svc.ByCategory(ctx, report.Actor{HouseholdID: alheia.ID, UserID: "v"}, report.ByCategoryInput{Month: "2026-09"})
		require.NoError(t, err)
		require.Len(t, daVizinha.Items, 1)
		assert.Nil(t, daVizinha.Items[0].CategoryID)
		assert.Equal(t, int64(555_555), daVizinha.TotalCents)
	})
}

// ADR-032, contra banco: a chave (categoria, conta) sobre um mês com cartão
// vivo, cartão ARQUIVADO, conta corrente, dinheiro, poupança e `other`;
// despesa em todas; despesa de cartão SEM categoria; aporte em cartão;
// pagamento de fatura (transfer_out/transfer_in); lançamento excluído; e uma
// casa vizinha com cartão de MESMO nome. Prova: (1) uma linha por chave,
// (2) as linhas somadas por categoria batem com a agregação antiga por
// categoria, (3) o serviço real particiona credit + debit == todas, com o
// cartão arquivado dentro de credit, o aporte fora dos dois e o balde do
// cartão sob credit, (4) nenhuma conta ou categoria da vizinha aparece.
func TestSumByCategoryAndAccountParticionaPorConta(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		cartao := s.makeAccountKind(t, ctx, minha.ID, "Cartao Roxo", account.KindCreditCard, false)
		cartaoArq := s.makeAccountKind(t, ctx, minha.ID, "Cartao Antigo", account.KindCreditCard, true)
		corrente := s.makeAccountKind(t, ctx, minha.ID, "Corrente", account.KindChecking, false)
		dinheiro := s.makeAccountKind(t, ctx, minha.ID, "Dinheiro", account.KindCash, false)
		poupanca := s.makeAccountKind(t, ctx, minha.ID, "Poupanca", account.KindSavings, false)
		outra := s.makeAccountKind(t, ctx, minha.ID, "Outra", account.KindOther, false)
		// A vizinha tem um cartão com o MESMO nome: se o recorte fosse por
		// nome, ou o escopo falhasse, o dinheiro dela apareceria no meu credit.
		cartaoAlheio := s.makeAccountKind(t, ctx, alheia.ID, "Cartao Roxo", account.KindCreditCard, false)

		mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		padaria := s.makeCategory(t, ctx, minha.ID, "Padaria", category.KindExpense, &mercado.ID)
		lazer := s.makeCategory(t, ctx, minha.ID, "Lazer", category.KindExpense, nil)
		cdb := s.makeCategory(t, ctx, minha.ID, "CDB", category.KindInvestment, nil)
		mercadoAlheio := s.makeCategory(t, ctx, alheia.ID, "Mercado", category.KindExpense, nil)

		set := civil.MustNew(2026, 9, 10)
		tx := func(conta string, cat *string, cents int64) {
			s.makeTransaction(t, ctx, minha.ID, conta, txSpec{AmountCents: cents, OccurredOn: set, CategoryID: cat})
		}
		// Despesa em TODAS as contas.
		tx(cartao.ID, &mercado.ID, 1_000)
		tx(cartao.ID, &padaria.ID, 300)
		tx(cartaoArq.ID, &mercado.ID, 2_000)
		tx(corrente.ID, &mercado.ID, 10_000)
		tx(corrente.ID, &lazer.ID, 4_000)
		tx(dinheiro.ID, &lazer.ID, 50)
		tx(poupanca.ID, &padaria.ID, 700)
		tx(outra.ID, nil, 90)
		// Despesa de cartão SEM categoria — o estado normal depois de importar.
		tx(cartao.ID, nil, 500)
		tx(cartao.ID, nil, 600)
		// Aporte NO CARTÃO e na corrente: descartados pelo serviço, mas a
		// CONSULTA os devolve (ela não conhece a natureza da categoria).
		tx(cartao.ID, &cdb.ID, 200_000)
		tx(corrente.ID, &cdb.ID, 50_000)
		// Pagamento de fatura — nunca entra (ADR-016).
		grupo := s.nextID("grp")
		s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{Kind: transaction.KindTransferOut, AmountCents: 1_900, OccurredOn: set, TransferGroupID: &grupo})
		s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{Kind: transaction.KindTransferIn, AmountCents: 1_900, OccurredOn: set, TransferGroupID: &grupo})
		// Excluído — sai.
		excluida := s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{AmountCents: 9_999, OccurredOn: set, CategoryID: &mercado.ID})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		// A vizinha, no mesmo mês, no cartão de mesmo nome.
		s.makeTransaction(t, ctx, alheia.ID, cartaoAlheio.ID, txSpec{AmountCents: 777_777, OccurredOn: set, CategoryID: &mercadoAlheio.ID})
		s.makeTransaction(t, ctx, alheia.ID, cartaoAlheio.ID, txSpec{AmountCents: 888_888, OccurredOn: set})

		// --- (1) a consulta: uma linha por (categoria, conta) ---------------
		rows, err := s.transactions.SumByCategoryAndAccount(ctx, minha.ID, "2026-09", transaction.KindExpense)
		require.NoError(t, err)
		chaves := porCategoriaEConta(t, rows)
		require.Len(t, chaves, 11, "onze chaves (categoria, conta) distintas")
		assert.Equal(t, int64(1_000), chaves[[2]string{mercado.ID, cartao.ID}].TotalCents)
		assert.Equal(t, int64(2_000), chaves[[2]string{mercado.ID, cartaoArq.ID}].TotalCents)
		assert.Equal(t, int64(10_000), chaves[[2]string{mercado.ID, corrente.ID}].TotalCents)
		assert.Equal(t, int64(1_100), chaves[[2]string{"", cartao.ID}].TotalCents, "o balde do cartão é UMA linha")
		assert.Equal(t, int64(2), chaves[[2]string{"", cartao.ID}].Count)
		assert.Equal(t, int64(90), chaves[[2]string{"", outra.ID}].TotalCents, "o balde de outra conta é OUTRA linha")
		assert.Equal(t, int64(200_000), chaves[[2]string{cdb.ID, cartao.ID}].TotalCents, "a consulta devolve o aporte; quem descarta é o serviço")
		for chave := range chaves {
			assert.NotEqual(t, cartaoAlheio.ID, chave[1], "conta da vizinha na minha agregação")
			assert.NotEqual(t, mercadoAlheio.ID, chave[0], "categoria da vizinha na minha agregação")
		}

		// --- (2) somadas por categoria, batem com a agregação antiga --------
		porCat := porCategoria(rows)
		require.Len(t, porCat, 5, "mercado, padaria, lazer, cdb e a nula")
		assert.Equal(t, int64(13_000), porCat[mercado.ID].TotalCents)
		assert.Equal(t, int64(3), porCat[mercado.ID].Count)
		assert.Equal(t, int64(1_000), porCat[padaria.ID].TotalCents)
		assert.Equal(t, int64(4_050), porCat[lazer.ID].TotalCents)
		assert.Equal(t, int64(1_190), porCat[""].TotalCents, "NULL de todas as contas somado")
		assert.Equal(t, int64(3), porCat[""].Count)
		var soma, contagem int64
		for _, r := range rows {
			soma += r.TotalCents
			contagem += r.Count
		}
		assert.Equal(t, int64(269_240), soma, "sem a transferência, o excluído e a vizinha")
		assert.Equal(t, int64(12), contagem)

		// --- (3) o serviço real: credit + debit == todas --------------------
		svc := report.NewService(s.transactions, s.categories, s.accounts, logging.Discard())
		ator := report.Actor{HouseholdID: minha.ID, UserID: "u"}
		pedir := func(grupo string) report.CategoryReportView {
			v, err := svc.ByCategory(ctx, ator, report.ByCategoryInput{Month: "2026-09", Kind: "expense", AccountGroup: grupo})
			require.NoError(t, err)
			return v
		}
		todas, credito, debito := pedir(""), pedir(report.AccountGroupCredit), pedir(report.AccountGroupDebit)

		assert.Nil(t, todas.AccountGroup)
		require.NotNil(t, credito.AccountGroup)
		require.NotNil(t, debito.AccountGroup)
		assert.Equal(t, "credit", *credito.AccountGroup)
		assert.Equal(t, "debit", *debito.AccountGroup)
		assert.Equal(t, int64(19_240), todas.TotalCents, "sem os dois aportes")
		assert.Equal(t, int64(10), todas.Count)
		assert.Equal(t, int64(4_400), credito.TotalCents, "cartão vivo 1.000 + 300 + 500 + 600, cartão arquivado 2.000; sem o aporte")
		assert.Equal(t, int64(5), credito.Count)
		assert.Equal(t, int64(14_840), debito.TotalCents, "corrente, dinheiro, poupança e outra; sem o aporte")
		assert.Equal(t, int64(5), debito.Count)
		assert.Equal(t, todas.TotalCents, credito.TotalCents+debito.TotalCents)
		assert.Equal(t, todas.Count, credito.Count+debito.Count)

		itens := func(v report.CategoryReportView) map[string]report.CategoryReportGroupView {
			out := map[string]report.CategoryReportGroupView{}
			for _, it := range v.Items {
				chave := ""
				if it.CategoryID != nil {
					chave = *it.CategoryID
				}
				out[chave] = it
			}
			return out
		}
		iT, iC, iD := itens(todas), itens(credito), itens(debito)
		for chave, it := range iT {
			assert.Equal(t, it.TotalCents, iC[chave].TotalCents+iD[chave].TotalCents, "item %q", chave)
			assert.Equal(t, it.Count, iC[chave].Count+iD[chave].Count, "item %q", chave)
		}
		_, temAporte := iC[cdb.ID]
		assert.False(t, temAporte, "o aporte no cartão não aparece em credit")
		_, temAporte = iT[cdb.ID]
		assert.False(t, temAporte, "nem em todas")
		assert.Equal(t, int64(1_100), iC[""].TotalCents, "o balde de credit é a despesa de cartão sem categoria")
		assert.Equal(t, int64(3_000), iC[mercado.ID].DirectCents, "mercado em credit: cartão vivo + cartão ARQUIVADO")
		require.Len(t, iC[mercado.ID].Children, 1)
		assert.Equal(t, int64(300), iC[mercado.ID].Children[0].TotalCents, "padaria no cartão")

		// --- (4) a vizinha só vê o dela, nos três recortes ------------------
		atorAlheio := report.Actor{HouseholdID: alheia.ID, UserID: "v"}
		for _, grupo := range []string{"", report.AccountGroupCredit, report.AccountGroupDebit} {
			v, err := svc.ByCategory(ctx, atorAlheio, report.ByCategoryInput{Month: "2026-09", AccountGroup: grupo})
			require.NoError(t, err)
			if grupo == report.AccountGroupDebit {
				assert.Equal(t, int64(0), v.TotalCents, "a vizinha só tem cartão")
				continue
			}
			assert.Equal(t, int64(1_666_665), v.TotalCents, "recorte %q", grupo)
			for _, it := range v.Items {
				if it.CategoryID != nil {
					assert.NotEqual(t, mercado.ID, *it.CategoryID)
				}
			}
		}
	})
}
