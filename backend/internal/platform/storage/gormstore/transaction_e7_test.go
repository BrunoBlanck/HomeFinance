package gormstore_test

import (
	"fmt"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Este arquivo cobre as consultas da spec 0006 (ADR-029): investimentos e
// resgates marcados pela NATUREZA DA CATEGORIA, sem tabela, coluna ou índice
// novo. Todas rodam contra banco real, porque o que importa provar é o WHERE —
// e, em três delas, provar que NENHUM comando foi emitido.

// --- Summary ------------------------------------------------------------------

// O aporte sai de expenseCents e entra em investedCents; o resgate faz o mesmo
// do lado da receita. Contagem e "sem categoria" não mudam, e o saldo da conta
// não é assunto desta consulta (ADR-029e).
func TestSummaryTiraOMarcadoDeReceitaEDespesaSemMudarContagem(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		cdb := s.makeCategory(t, ctx, minha.ID, "CDB", category.KindInvestment, nil)
		resgate := s.makeCategory(t, ctx, minha.ID, "Resgate CDB", category.KindRedemption, nil)
		salario := s.makeCategory(t, ctx, minha.ID, "Salário", category.KindIncome, nil)

		mes := "2026-09"
		emSetembro := civil.MustNew(2026, 9, 10)

		// Despesas: comum, marcada e sem categoria.
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 150_00, OccurredOn: emSetembro, CategoryID: &mercado.ID,
		})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 2_000_00, OccurredOn: emSetembro, CategoryID: &cdb.ID,
			Description: "CDB 15 DIAS",
		})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 30_00, OccurredOn: emSetembro,
		})
		// Receitas: comum e marcada (resgate).
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindIncome, AmountCents: 5_000_00, OccurredOn: emSetembro, CategoryID: &salario.ID,
		})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindIncome, AmountCents: 850_00, OccurredOn: emSetembro, CategoryID: &resgate.ID,
		})
		// Da vizinha, mesmo mês e MESMA categoria marcada: nunca entra.
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 999_00, OccurredOn: emSetembro, CategoryID: &cdb.ID,
		})

		marcadas := []string{cdb.ID, resgate.ID}

		resumo, err := s.transactions.Summary(ctx, minha.ID, transaction.SummaryFilter{
			CompetenceMonth: mes, InvestmentCategoryIDs: marcadas,
		})
		require.NoError(t, err)

		assert.EqualValues(t, 180_00, resumo.ExpenseCents, "despesa é 150 + 30: o aporte de 2.000 saiu")
		assert.EqualValues(t, 2_000_00, resumo.InvestedCents)
		assert.EqualValues(t, 5_000_00, resumo.IncomeCents, "receita é só o salário: o resgate saiu")
		assert.EqualValues(t, 850_00, resumo.RedeemedCents)
		assert.EqualValues(t, 5_000_00-180_00, resumo.NetCents)
		assert.EqualValues(t, 5, resumo.Count, "a lista continua mostrando os 5 lançamentos")
		assert.EqualValues(t, 1, resumo.Uncategorized, "marcado tem categoria: nunca foi pendência")

		// A mesma janela SEM o conjunto: os números voltam a ser os de antes do
		// E7. É a prova de que a marcação é do conjunto, não do dado.
		antes, err := s.transactions.Summary(ctx, minha.ID, transaction.SummaryFilter{CompetenceMonth: mes})
		require.NoError(t, err)
		assert.EqualValues(t, 2_180_00, antes.ExpenseCents)
		assert.EqualValues(t, 5_850_00, antes.IncomeCents)
		assert.Zero(t, antes.InvestedCents)
		assert.Zero(t, antes.RedeemedCents)
		assert.EqualValues(t, resumo.Count, antes.Count)
		assert.EqualValues(t, resumo.Uncategorized, antes.Uncategorized)
	})
}

// A despesa marcada NUNCA pode deixar expenseCents negativo — é o defeito que
// duas consultas com subtração produziriam sob escrita concorrente. Aqui o mês
// inteiro é aporte: a despesa comum fica em ZERO, nunca abaixo.
func TestSummaryComOMesInteiroMarcadoZeraDespesaEmVezDeNegativar(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		cdb := s.makeCategory(t, ctx, minha.ID, "CDB", category.KindInvestment, nil)

		for i := range 3 {
			s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
				Kind: transaction.KindExpense, AmountCents: int64(100_00 * (i + 1)),
				OccurredOn: civil.MustNew(2026, 9, 10), CategoryID: &cdb.ID,
			})
		}

		resumo, err := s.transactions.Summary(ctx, minha.ID, transaction.SummaryFilter{
			CompetenceMonth: "2026-09", InvestmentCategoryIDs: []string{cdb.ID},
		})
		require.NoError(t, err)
		assert.Zero(t, resumo.ExpenseCents)
		assert.EqualValues(t, 600_00, resumo.InvestedCents)
		assert.Zero(t, resumo.NetCents)
	})
}

// Critério 6, primeira metade: casa SEM categoria de investimento emite
// exatamente o SQL de antes do E7 — sem CASE, sem IN, com os mesmos dois
// parâmetros. E as três formas de "vazio" (nil, slice vazio e slice só com
// string vazia) produzem a MESMA string, byte a byte.
func TestSummarySemCategoriaDeInvestimentoEmiteOSQLDeSempre(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 100_00, OccurredOn: civil.MustNew(2026, 9, 10),
		})

		spy := s.espiarSQL(t)
		janela := transaction.SummaryFilter{CompetenceMonth: "2026-09"}

		emitido := func(t *testing.T, ids []string) comandoSQL {
			t.Helper()
			f := janela
			f.InvestmentCategoryIDs = ids
			spy.ligar()
			_, err := s.transactions.Summary(ctx, minha.ID, f)
			require.NoError(t, err)
			comandos := spy.comandosEmitidos()
			require.Len(t, comandos, 1, "o resumo é UMA consulta, sempre")
			return comandos[0]
		}

		semNada := emitido(t, nil)
		vazio := emitido(t, []string{})
		soVazias := emitido(t, []string{"", ""})

		assert.Equal(t, semNada.SQL, vazio.SQL)
		assert.Equal(t, semNada.SQL, soVazias.SQL,
			"lista só com string vazia é lista vazia: o CASE não pode entrar")
		assert.NotContains(t, semNada.SQL, "category_id IN", "IN () nunca é emitido (ADR-029f)")
		assert.NotContains(t, semNada.SQL, "marked_total")
		assert.NotContains(t, semNada.SQL, "marked_cnt")
		assert.Contains(t, semNada.SQL, "SUM(CASE WHEN category_id IS NULL THEN 1 ELSE 0 END) AS uncategorized",
			"a projeção de sempre continua inteira")
		assert.Equal(t, 2, semNada.Parametros, "casa + mês, como antes do E7")

		// Com o conjunto de verdade, a coluna condicional aparece — e é UMA
		// consulta só, com um parâmetro a mais por categoria.
		cdb := s.makeCategory(t, ctx, minha.ID, "CDB", category.KindInvestment, nil)
		resgate := s.makeCategory(t, ctx, minha.ID, "Resgate", category.KindRedemption, nil)
		comIDs := emitido(t, []string{cdb.ID, resgate.ID})
		assert.Contains(t, comIDs.SQL, "marked_total")
		assert.Contains(t, comIDs.SQL, "marked_cnt",
			"a contagem marcada entrou com o filtro de tipo (E2d): é dela que sai a "+
				"contagem dos cinco recortes, por aritmética sobre a mesma linha")
		assert.Equal(t, 6, comIDs.Parametros,
			"2 categorias em marked_total + 2 em marked_cnt + casa + mês")
	})
}

// Teto de parâmetros: o conjunto de categorias não é fatiado (fatiar uma
// agregação obrigaria a somar fatias de dinheiro), então acima do teto é ERRO
// alto — nunca um comando que estoura dentro do driver em dois dialetos só.
func TestConsultasDeInvestimentoRecusamCategoriasDemais(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)

		demais := make([]string, 0, 201)
		for i := range 201 {
			demais = append(demais, fmt.Sprintf("c-%03d", i))
		}

		_, err := s.transactions.Summary(ctx, minha.ID, transaction.SummaryFilter{
			CompetenceMonth: "2026-09", InvestmentCategoryIDs: demais,
		})
		require.ErrorIs(t, err, transaction.ErrTooManyCategories)

		_, err = s.transactions.SumInvestmentsByMonth(ctx, minha.ID, demais, "2026-01", "2026-09")
		require.ErrorIs(t, err, transaction.ErrTooManyCategories)

		_, err = s.transactions.ListByCategories(ctx, minha.ID, transaction.CategoryListFilter{
			CompetenceMonth: "2026-09", CategoryIDs: demais,
		})
		require.ErrorIs(t, err, transaction.ErrTooManyCategories)

		_, err = s.transactions.SetCategoryWhereCurrentIn(ctx, minha.ID, []string{"t-1"}, demais, "c-nova", now())
		require.ErrorIs(t, err, transaction.ErrTooManyCategories)
	})
}

// --- SumInvestmentsByMonth ----------------------------------------------------

// Critério 6, segunda metade, e a razão de ser do ADR-029f: com a lista vazia
// NENHUM comando é emitido. Um teste que só conferisse o resultado vazio
// passaria igual se a consulta tivesse rodado — que é justamente o caso em que
// `IN ()` iria para o banco.
func TestConsultasPorCategoriaComListaVaziaNaoTocamOBanco(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 100_00, OccurredOn: civil.MustNew(2026, 9, 10),
		})

		spy := s.espiarSQL(t)

		spy.ligar()
		_, err := s.transactions.SumInvestmentsByMonth(ctx, minha.ID, nil, "2026-01", "2026-09")
		require.ErrorIs(t, err, transaction.ErrEmptyCategoryFilter)
		assert.Empty(t, spy.comandosEmitidos(), "soma com conjunto vazio não vai ao banco")

		spy.ligar()
		_, err = s.transactions.ListByCategories(ctx, minha.ID, transaction.CategoryListFilter{
			CompetenceMonth: "2026-09", CategoryIDs: []string{},
		})
		require.ErrorIs(t, err, transaction.ErrEmptyCategoryFilter)
		assert.Empty(t, spy.comandosEmitidos(), "lista com conjunto vazio não vai ao banco")

		// A armadilha: lista só com string vazia. dedupeStrings a esvazia, e o
		// curto-circuito tem de valer igual.
		spy.ligar()
		_, err = s.transactions.ListByCategories(ctx, minha.ID, transaction.CategoryListFilter{
			CompetenceMonth: "2026-09", CategoryIDs: []string{"", ""},
		})
		require.ErrorIs(t, err, transaction.ErrEmptyCategoryFilter)
		assert.Empty(t, spy.comandosEmitidos())

		// E a escrita: allowlist vazia não vira "qualquer categoria serve".
		spy.ligar()
		afetadas, err := s.transactions.SetCategoryWhereCurrentIn(ctx, minha.ID, []string{"t-1"}, nil, "c-nova", now())
		require.ErrorIs(t, err, transaction.ErrEmptyCategoryFilter)
		assert.Zero(t, afetadas)
		assert.Empty(t, spy.comandosEmitidos(), "nenhum UPDATE sem allowlist")
	})
}

// Uma consulta serve o mês, o ano até o mês e a série de 12 — e ela devolve no
// máximo 24 linhas. O teste confere o número de comandos, o conteúdo e tudo
// que NÃO pode entrar: outra casa, excluída, transferência, outra categoria e
// mês fora da janela.
func TestSumInvestmentsByMonthResolveOsTresNumerosEmUmaConsulta(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		outraConta := s.makeAccount(t, ctx, minha.ID, "Outra")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		cdb := s.makeCategory(t, ctx, minha.ID, "CDB", category.KindInvestment, nil)
		resgate := s.makeCategory(t, ctx, minha.ID, "Resgate", category.KindRedemption, nil)
		mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		grupo := s.nextID("grp")

		// Julho: um aporte. Setembro: dois aportes e um resgate.
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 500_00, OccurredOn: civil.MustNew(2026, 7, 5), CategoryID: &cdb.ID,
		})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 2_000_00, OccurredOn: civil.MustNew(2026, 9, 5), CategoryID: &cdb.ID,
		})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 300_00, OccurredOn: civil.MustNew(2026, 9, 20), CategoryID: &cdb.ID,
		})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindIncome, AmountCents: 850_00, OccurredOn: civil.MustNew(2026, 9, 25), CategoryID: &resgate.ID,
		})

		// Nenhum destes entra: categoria comum, excluído, transferência (que
		// não tem categoria), fora da janela e de outra casa.
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 77_00, OccurredOn: civil.MustNew(2026, 9, 6), CategoryID: &mercado.ID,
		})
		excluido := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 999_00, OccurredOn: civil.MustNew(2026, 9, 7), CategoryID: &cdb.ID,
		})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluido.ID, now()))
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindTransferOut, AmountCents: 400_00, OccurredOn: civil.MustNew(2026, 9, 8), TransferGroupID: &grupo,
		})
		s.makeTransaction(t, ctx, minha.ID, outraConta.ID, txSpec{
			Kind: transaction.KindTransferIn, AmountCents: 400_00, OccurredOn: civil.MustNew(2026, 9, 8), TransferGroupID: &grupo,
		})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 111_00, OccurredOn: civil.MustNew(2025, 1, 4), CategoryID: &cdb.ID,
		})
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 4_000_00, OccurredOn: civil.MustNew(2026, 9, 9), CategoryID: &cdb.ID,
		})

		spy := s.espiarSQL(t)
		spy.ligar()
		// A janela pedida é a MAIOR das três: os 12 meses até setembro/2026.
		totais, err := s.transactions.SumInvestmentsByMonth(ctx, minha.ID,
			[]string{cdb.ID, resgate.ID}, "2025-10", "2026-09")
		require.NoError(t, err)
		require.Len(t, spy.comandosEmitidos(), 1, "os três números saem de UMA consulta")

		require.Len(t, totais, 2, "só os meses COM movimento voltam")
		assert.Equal(t, "2026-07", totais[0].Month, "em ordem cronológica")
		assert.EqualValues(t, 500_00, totais[0].ContributionsCents)
		assert.EqualValues(t, 1, totais[0].ContributionCount)
		assert.Zero(t, totais[0].RedemptionsCents)

		assert.Equal(t, "2026-09", totais[1].Month)
		assert.EqualValues(t, 2_300_00, totais[1].ContributionsCents, "2.000 + 300; o excluído e o da vizinha ficam de fora")
		assert.EqualValues(t, 2, totais[1].ContributionCount)
		assert.EqualValues(t, 850_00, totais[1].RedemptionsCents, "receita marcada é RESGATE, nunca aporte")
		assert.EqualValues(t, 1, totais[1].RedemptionCount)
	})
}

// Janela invertida ou vazia é ERRO, e não zeros: zeros em silêncio diriam
// "você não investiu nada" para quem investiu.
func TestSumInvestmentsByMonthRecusaJanelaInvalida(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		cdb := s.makeCategory(t, ctx, minha.ID, "CDB", category.KindInvestment, nil)

		for _, caso := range []struct{ de, ate string }{
			{"", "2026-09"},
			{"2026-09", ""},
			{"2026-09", "2026-08"},
		} {
			_, err := s.transactions.SumInvestmentsByMonth(ctx, minha.ID, []string{cdb.ID}, caso.de, caso.ate)
			require.Error(t, err, "janela %q..%q tem de ser recusada", caso.de, caso.ate)
		}
	})
}

// --- ListByCategories ---------------------------------------------------------

// A lista do mês: só o marcado, na ordem de GET /transactions, com o MESMO
// cursor — e sem a vizinha, sem excluído e sem perna de transferência.
func TestListByCategoriesUsaAOrdemEOCursorDeSempre(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		cdb := s.makeCategory(t, ctx, minha.ID, "CDB", category.KindInvestment, nil)
		mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)

		primeiro := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 100_00, OccurredOn: civil.MustNew(2026, 9, 3), CategoryID: &cdb.ID,
		})
		segundo := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 200_00, OccurredOn: civil.MustNew(2026, 9, 10), CategoryID: &cdb.ID,
		})
		terceiro := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 300_00, OccurredOn: civil.MustNew(2026, 9, 20), CategoryID: &cdb.ID,
		})
		// Fora: categoria comum, excluído, outro mês e a vizinha.
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 44_00, OccurredOn: civil.MustNew(2026, 9, 15), CategoryID: &mercado.ID,
		})
		excluido := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 55_00, OccurredOn: civil.MustNew(2026, 9, 16), CategoryID: &cdb.ID,
		})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluido.ID, now()))
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 66_00, OccurredOn: civil.MustNew(2026, 8, 16), CategoryID: &cdb.ID,
		})
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 77_00, OccurredOn: civil.MustNew(2026, 9, 17), CategoryID: &cdb.ID,
		})

		// Página 1: pede 2+1 para saber que há próxima, como a listagem faz.
		pagina, err := s.transactions.ListByCategories(ctx, minha.ID, transaction.CategoryListFilter{
			CompetenceMonth: "2026-09", CategoryIDs: []string{cdb.ID}, Limit: 3,
		})
		require.NoError(t, err)
		require.Len(t, pagina, 3)
		assert.Equal(t, terceiro.ID, pagina[0].ID, "do mais recente para o mais antigo")
		assert.Equal(t, segundo.ID, pagina[1].ID)
		assert.Equal(t, primeiro.ID, pagina[2].ID)

		// Página 2, a partir do cursor da segunda linha: só sobra a primeira.
		seguinte, err := s.transactions.ListByCategories(ctx, minha.ID, transaction.CategoryListFilter{
			CompetenceMonth: "2026-09", CategoryIDs: []string{cdb.ID}, Limit: 10,
			Cursor: &transaction.Cursor{OccurredOn: segundo.OccurredOn, ID: segundo.ID},
		})
		require.NoError(t, err)
		require.Len(t, seguinte, 1)
		assert.Equal(t, primeiro.ID, seguinte[0].ID)
	})
}

// --- ListIncomeExpenseOfMonth -------------------------------------------------

// A projeção do `detect`: receita e despesa vivas do mês, COM e SEM categoria,
// e o teto + 1 que permite decidir o 422 sem ler o mês inteiro.
func TestListIncomeExpenseOfMonthTrazComESemCategoriaAteOTeto(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		outraConta := s.makeAccount(t, ctx, minha.ID, "Outra")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")
		mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		grupo := s.nextID("grp")

		semCategoria := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, Description: "CDB 15 DIAS", OccurredOn: civil.MustNew(2026, 9, 3),
		})
		comCategoria := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, Description: "Tesouro Selic", OccurredOn: civil.MustNew(2026, 9, 4),
			CategoryID: &mercado.ID,
		})
		// Fora: transferência, excluída, outro mês e a vizinha.
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindTransferOut, OccurredOn: civil.MustNew(2026, 9, 5), TransferGroupID: &grupo,
		})
		s.makeTransaction(t, ctx, minha.ID, outraConta.ID, txSpec{
			Kind: transaction.KindTransferIn, OccurredOn: civil.MustNew(2026, 9, 5), TransferGroupID: &grupo,
		})
		excluida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, OccurredOn: civil.MustNew(2026, 9, 6),
		})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, OccurredOn: civil.MustNew(2026, 8, 6),
		})
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindExpense, OccurredOn: civil.MustNew(2026, 9, 7),
		})

		linhas, err := s.transactions.ListIncomeExpenseOfMonth(ctx, minha.ID, "2026-09", 100)
		require.NoError(t, err)
		require.Len(t, linhas, 2)

		assert.Equal(t, semCategoria.ID, linhas[0].ID, "ordem (occurred_on, id) crescente")
		assert.Equal(t, "CDB 15 DIAS", linhas[0].Description)
		assert.Equal(t, "cdb 15 dias", linhas[0].DescriptionNorm)
		assert.Nil(t, linhas[0].CategoryID, "sem categoria é NULO, não vazio")

		assert.Equal(t, comCategoria.ID, linhas[1].ID)
		require.NotNil(t, linhas[1].CategoryID)
		assert.Equal(t, mercado.ID, *linhas[1].CategoryID)

		// O teto + 1: com limite 1 volta uma linha, e é assim que o serviço
		// descobre que ultrapassou sem ler o mês inteiro.
		uma, err := s.transactions.ListIncomeExpenseOfMonth(ctx, minha.ID, "2026-09", 1)
		require.NoError(t, err)
		assert.Len(t, uma, 1)

		_, err = s.transactions.ListIncomeExpenseOfMonth(ctx, minha.ID, "", 10)
		require.Error(t, err, "mês vazio é erro, nunca a casa inteira")
		_, err = s.transactions.ListIncomeExpenseOfMonth(ctx, minha.ID, "2026-09", 0)
		require.Error(t, err, "limite não positivo é erro")
	})
}

// --- SetCategoryWhereCurrentIn ------------------------------------------------

// A regra que o revisor de segurança vem conferir: a troca em massa NUNCA
// desfaz uma marcação de investimento, nem toca linha sem categoria, nem
// alcança a casa vizinha — e tudo isso mora no WHERE, não no Go.
func TestSetCategoryWhereCurrentInNuncaDesfazMarcacaoDeInvestimento(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		outros := s.makeCategory(t, ctx, minha.ID, "Outros", category.KindExpense, nil)
		cdb := s.makeCategory(t, ctx, minha.ID, "CDB", category.KindInvestment, nil)
		tesouro := s.makeCategory(t, ctx, minha.ID, "Tesouro", category.KindInvestment, nil)

		// A allowlist é o conjunto das categorias de natureza income/expense.
		permitidas := []string{mercado.ID, outros.ID}

		comum := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, OccurredOn: civil.MustNew(2026, 9, 3), CategoryID: &outros.ID,
		})
		jaMarcada := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, OccurredOn: civil.MustNew(2026, 9, 4), CategoryID: &tesouro.ID,
		})
		semCategoria := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, OccurredOn: civil.MustNew(2026, 9, 5),
		})
		excluida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, OccurredOn: civil.MustNew(2026, 9, 6), CategoryID: &outros.ID,
		})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		daVizinha := s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindExpense, OccurredOn: civil.MustNew(2026, 9, 7), CategoryID: &outros.ID,
		})

		alvos := []string{comum.ID, jaMarcada.ID, semCategoria.ID, excluida.ID, daVizinha.ID}
		afetadas, err := s.transactions.SetCategoryWhereCurrentIn(ctx, minha.ID, alvos, permitidas, cdb.ID, now())
		require.NoError(t, err)
		assert.EqualValues(t, 1, afetadas, "só a linha de categoria COMUM e VIVA da casa é trocada")

		conferir := func(id string, esperada *string) {
			t.Helper()
			lido, err := s.transactions.ByIDIncludingDeleted(ctx, minha.ID, id)
			require.NoError(t, err)
			if esperada == nil {
				assert.Nil(t, lido.CategoryID)
				return
			}
			require.NotNil(t, lido.CategoryID)
			assert.Equal(t, *esperada, *lido.CategoryID)
		}
		conferir(comum.ID, &cdb.ID)
		conferir(jaMarcada.ID, &tesouro.ID)
		conferir(semCategoria.ID, nil)
		conferir(excluida.ID, &outros.ID)

		daVizinhaLida, err := s.transactions.ByID(ctx, alheia.ID, daVizinha.ID)
		require.NoError(t, err)
		require.NotNil(t, daVizinhaLida.CategoryID)
		assert.Equal(t, outros.ID, *daVizinhaLida.CategoryID, "a casa vizinha não é alcançada")

		// Segunda execução com os mesmos alvos: a linha já está na categoria de
		// investimento, que não está na allowlist — zero linhas afetadas.
		afetadas, err = s.transactions.SetCategoryWhereCurrentIn(ctx, minha.ID, alvos, permitidas, cdb.ID, now())
		require.NoError(t, err)
		assert.Zero(t, afetadas, "a troca não se repete nem se desfaz")
	})
}

// Orçamento de parâmetros do primeiro comando do projeto com DOIS IN (...):
// 200 da allowlist + uma fatia de 200 alvos + os kinds e a casa ≈ 405, abaixo
// do piso histórico de 999 do SQLite e muito abaixo dos 2100 do SQL Server.
func TestSetCategoryWhereCurrentInCabeNoOrcamentoDeParametros(t *testing.T) {
	t.Parallel()

	const (
		pisoSQLite = 999
		tetoMSSQL  = 2100
	)

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)

		// Os ids não precisam existir: o que está sob teste é o comando
		// emitido, não o que ele encontra. 400 alvos forçam DUAS fatias.
		permitidas := make([]string, 0, 200)
		for i := range 200 {
			permitidas = append(permitidas, fmt.Sprintf("c-perm-%03d", i))
		}
		alvos := make([]string, 0, 400)
		for i := range 400 {
			alvos = append(alvos, fmt.Sprintf("t-alvo-%03d", i))
		}

		spy := s.espiarSQL(t)
		spy.ligar()
		afetadas, err := s.transactions.SetCategoryWhereCurrentIn(ctx, minha.ID, alvos, permitidas, "c-nova", now())
		require.NoError(t, err)
		assert.Zero(t, afetadas)

		comandos := spy.comandosEmitidos()
		require.Len(t, comandos, 2, "400 alvos vão em duas fatias de 200")
		for i, cmd := range comandos {
			assert.LessOrEqualf(t, cmd.Parametros, pisoSQLite,
				"comando %d com %d parâmetros estoura o piso histórico do SQLite", i, cmd.Parametros)
			assert.LessOrEqualf(t, cmd.Parametros, tetoMSSQL,
				"comando %d com %d parâmetros estoura o teto do SQL Server", i, cmd.Parametros)
		}
		assert.Equal(t, 405, comandos[0].Parametros,
			"2 do SET + casa + 200 alvos + 2 kinds + 200 da allowlist")
	})
}
