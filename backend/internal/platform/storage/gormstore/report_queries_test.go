package gormstore_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Este arquivo cobre a consulta do relatório por categoria (ADR-027b) contra
// banco real, porque o que importa provar é o WHERE: casa, competência,
// natureza e deleted_at IS NULL — e que NULL em category_id agrupa numa linha
// só.

// porCategoria indexa o resultado por id ("" para a linha nula).
func porCategoria(rows []report.CategoryTotal) map[string]report.CategoryTotal {
	out := make(map[string]report.CategoryTotal, len(rows))
	for _, r := range rows {
		chave := ""
		if r.CategoryID != nil {
			chave = *r.CategoryID
		}
		out[chave] = r
	}
	return out
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
		rows, err := s.transactions.SumByCategory(ctx, minha.ID, "2026-09", transaction.KindExpense)
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
		rows, err = s.transactions.SumByCategory(ctx, minha.ID, "2026-09", transaction.KindIncome)
		require.NoError(t, err)
		got = porCategoria(rows)
		require.Len(t, got, 2)
		assert.Equal(t, int64(500_000), got[salario.ID].TotalCents)
		assert.Equal(t, int64(25), got[""].TotalCents)

		// --- casa vizinha: só o dela ----------------------------------------
		rows, err = s.transactions.SumByCategory(ctx, alheia.ID, "2026-09", transaction.KindExpense)
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
		rows, err = s.transactions.SumByCategory(ctx, minha.ID, "2026-11", transaction.KindExpense)
		require.NoError(t, err)
		assert.Empty(t, rows)

		// --- guardas: vazio é erro, nunca varredura ---------------------------
		for _, c := range []struct{ casa, mes, kind string }{
			{"", "2026-09", transaction.KindExpense},
			{minha.ID, "", transaction.KindExpense},
			{minha.ID, "2026-09", ""},
		} {
			_, err := s.transactions.SumByCategory(ctx, c.casa, c.mes, c.kind)
			assert.Error(t, err, "%+v", c)
		}

		// Natureza que não existe no banco (transferência pedida "por
		// engano"): o serviço barra antes, mas o repositório responde vazio,
		// nunca erro nem outra coisa.
		rows, err = s.transactions.SumByCategory(ctx, minha.ID, "2026-09", "bogus")
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

		svc := report.NewService(s.transactions, s.categories, logging.Discard())
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
