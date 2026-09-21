package gormstore_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Recorte por CATEGORIA de GET /transactions (o atalho "Ver lançamentos" do
// relatório). Nenhuma tabela, coluna ou índice novo. O que precisa de prova
// no SQL real é o `category_id IN (…)` — na LISTA e no RESUMO, com o mesmo
// conjunto — e que o conjunto vazio não acrescenta cláusula nenhuma.
func TestFiltroPorCategoriaRecortaListaEResumoComOMesmoConjunto(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta Corrente")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		grupo := s.makeCategory(t, ctx, minha.ID, "Alimentacao", category.KindExpense, nil)
		mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, &grupo.ID)
		transporte := s.makeCategory(t, ctx, minha.ID, "Transporte", category.KindExpense, nil)
		// A vizinha tem uma categoria com o MESMO nome: o escopo por casa é o
		// que impede o id dela de entrar no conjunto — mas, se entrasse, o
		// `household_id` da consulta ainda a deixaria de fora. As duas
		// defesas são medidas aqui.
		alheiaCat := s.makeCategory(t, ctx, alheia.ID, "Alimentacao", category.KindExpense, nil)

		dia := civil.MustNew(2026, 9, 10)
		noGrupo := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 300_00, OccurredOn: dia, CategoryID: &grupo.ID,
		}).ID
		naFilha := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 150_00, OccurredOn: dia, CategoryID: &mercado.ID,
		}).ID
		foraDoGrupo := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 80_00, OccurredOn: dia, CategoryID: &transporte.ID,
		}).ID
		semCategoria := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 20_00, OccurredOn: dia,
		}).ID
		daVizinha := s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 999_00, OccurredOn: dia, CategoryID: &alheiaCat.ID,
		}).ID

		conjunto := []string{grupo.ID, mercado.ID}

		rows, err := s.transactions.List(ctx, minha.ID, transaction.ListFilter{
			CompetenceMonth: mesDoCenario,
			CategoryIDs:     conjunto,
		})
		require.NoError(t, err)
		ids := idsDe(rows)
		assert.ElementsMatch(t, []string{noGrupo, naFilha}, ids)
		assert.NotContains(t, ids, foraDoGrupo)
		assert.NotContains(t, ids, semCategoria, "sem categoria nunca casa com um IN de categorias")
		assert.NotContains(t, ids, daVizinha)

		resumo, err := s.transactions.Summary(ctx, minha.ID, transaction.SummaryFilter{
			CompetenceMonth: mesDoCenario,
			CategoryIDs:     conjunto,
		})
		require.NoError(t, err)
		assert.Equal(t, int64(2), resumo.Count)
		assert.Equal(t, int64(450_00), resumo.ExpenseCents)
		assert.Zero(t, resumo.Uncategorized, "dentro de uma categoria não há pendência")

		// Id alheio DENTRO do conjunto (o serviço nunca o produz, mas o
		// repositório não depende disso): o `household_id` da consulta o
		// deixa de fora, e a lista continua a mesma.
		rows, err = s.transactions.List(ctx, minha.ID, transaction.ListFilter{
			CompetenceMonth: mesDoCenario,
			CategoryIDs:     []string{grupo.ID, mercado.ID, alheiaCat.ID},
		})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{noGrupo, naFilha}, idsDe(rows))

		// Conjunto vazio é "sem filtro": o mês inteiro da casa, e só dela.
		rows, err = s.transactions.List(ctx, minha.ID, transaction.ListFilter{
			CompetenceMonth: mesDoCenario,
		})
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{noGrupo, naFilha, foraDoGrupo, semCategoria}, idsDe(rows))
	})
}
