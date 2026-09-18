package gormstore_test

import (
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Este arquivo cobre as consultas do schema v4 (spec 0005) em transactions:
// auto-categorização (ADR-026h), GET /transfers e a ação `link` (ADR-026f).
// Todas rodam contra banco real porque o que importa provar é o WHERE.

// --- auto-categorização -------------------------------------------------------

func TestListUncategorizedSoDevolveReceitaEDespesaVivaSemCategoriaDaCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		outraConta := s.makeAccount(t, ctx, minha.ID, "Outra")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")
		cat := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		grupo := s.nextID("grp")

		semCategoria := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Description: "Supermercado Extra", OccurredOn: civil.MustNew(2026, 8, 10),
		})
		receitaSem := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindIncome, Description: "Salário", OccurredOn: civil.MustNew(2026, 8, 5),
		})
		// Não entram: com categoria, transferência, excluída, outro mês, outra casa.
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Description: "Já categorizada", OccurredOn: civil.MustNew(2026, 8, 11), CategoryID: &cat.ID,
		})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindTransferOut, OccurredOn: civil.MustNew(2026, 8, 12), TransferGroupID: &grupo,
		})
		s.makeTransaction(t, ctx, minha.ID, outraConta.ID, txSpec{
			Kind: transaction.KindTransferIn, OccurredOn: civil.MustNew(2026, 8, 12), TransferGroupID: &grupo,
		})
		excluida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Description: "Excluída", OccurredOn: civil.MustNew(2026, 8, 13),
		})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Description: "Outro mês", OccurredOn: civil.MustNew(2026, 9, 1),
		})
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Description: "Da vizinha", OccurredOn: civil.MustNew(2026, 8, 10),
		})

		linhas, err := s.transactions.ListUncategorized(ctx, minha.ID, "2026-08", 100)
		require.NoError(t, err)
		require.Len(t, linhas, 2)
		// Ordem constante: (occurred_on, id) ascendente.
		assert.Equal(t, receitaSem.ID, linhas[0].ID)
		assert.Equal(t, transaction.KindIncome, linhas[0].Kind)
		assert.Equal(t, semCategoria.ID, linhas[1].ID)
		assert.Equal(t, "Supermercado Extra", linhas[1].Description)
		assert.Equal(t, "supermercado extra", linhas[1].DescriptionNorm)

		// O limite corta; é como o serviço descobre que passou do teto.
		umaSo, err := s.transactions.ListUncategorized(ctx, minha.ID, "2026-08", 1)
		require.NoError(t, err)
		assert.Len(t, umaSo, 1)

		// Casa vizinha: nada meu aparece.
		daVizinha, err := s.transactions.ListUncategorized(ctx, alheia.ID, "2026-08", 100)
		require.NoError(t, err)
		require.Len(t, daVizinha, 1)
		assert.Equal(t, "Da vizinha", daVizinha[0].Description)

		// Mês vazio e limite inválido são erro, nunca varredura.
		_, err = s.transactions.ListUncategorized(ctx, minha.ID, "", 100)
		assert.Error(t, err)
		_, err = s.transactions.ListUncategorized(ctx, minha.ID, "2026-08", 0)
		assert.Error(t, err)
	})
}

// A regra inteira da spec 0005 §4.3 em um WHERE: linha com categoria não é
// tocada, aconteça o que acontecer na camada de cima.
func TestSetCategoryWhereNullNaoTocaLinhaComCategoria(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		outraConta := s.makeAccount(t, ctx, minha.ID, "Outra")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")
		mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		lazer := s.makeCategory(t, ctx, minha.ID, "Lazer", category.KindExpense, nil)
		grupo := s.nextID("grp")

		vazia := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Description: "Sem categoria"})
		escolhida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Description: "Escolhida", CategoryID: &lazer.ID})
		saida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Kind: transaction.KindTransferOut, TransferGroupID: &grupo})
		s.makeTransaction(t, ctx, minha.ID, outraConta.ID, txSpec{Kind: transaction.KindTransferIn, TransferGroupID: &grupo})
		excluida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Description: "Excluída"})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		daVizinha := s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{Description: "Da vizinha"})

		depois := now()
		afetadas, err := s.transactions.SetCategoryWhereNull(ctx, minha.ID,
			[]string{vazia.ID, escolhida.ID, saida.ID, excluida.ID, daVizinha.ID, vazia.ID, ""},
			mercado.ID, depois)
		require.NoError(t, err)
		assert.EqualValues(t, 1, afetadas, "só a linha viva, sem categoria, de receita/despesa e da casa")

		lida, err := s.transactions.ByID(ctx, minha.ID, vazia.ID)
		require.NoError(t, err)
		require.NotNil(t, lida.CategoryID)
		assert.Equal(t, mercado.ID, *lida.CategoryID)
		assert.Equal(t, "Sem categoria", lida.Description, "o SET tem duas colunas: nada mais mudou")

		lida, err = s.transactions.ByID(ctx, minha.ID, escolhida.ID)
		require.NoError(t, err)
		assert.Equal(t, lazer.ID, *lida.CategoryID, "categoria escolhida pela pessoa é intocável")

		lida, err = s.transactions.ByID(ctx, minha.ID, saida.ID)
		require.NoError(t, err)
		assert.Nil(t, lida.CategoryID, "transferência não recebe categoria")

		lida, err = s.transactions.ByIDIncludingDeleted(ctx, minha.ID, excluida.ID)
		require.NoError(t, err)
		assert.Nil(t, lida.CategoryID, "linha excluída não é tocada")

		lida, err = s.transactions.ByID(ctx, alheia.ID, daVizinha.ID)
		require.NoError(t, err)
		assert.Nil(t, lida.CategoryID, "a categoria da minha casa nunca chega à linha da vizinha")

		// Idempotente por construção: a segunda passada não acha nada vazio.
		afetadas, err = s.transactions.SetCategoryWhereNull(ctx, minha.ID, []string{vazia.ID}, mercado.ID, now())
		require.NoError(t, err)
		assert.Zero(t, afetadas)

		// Lista vazia é zero sem SQL; categoria vazia é erro.
		afetadas, err = s.transactions.SetCategoryWhereNull(ctx, minha.ID, nil, mercado.ID, now())
		require.NoError(t, err)
		assert.Zero(t, afetadas)
		_, err = s.transactions.SetCategoryWhereNull(ctx, minha.ID, []string{vazia.ID}, "", now())
		assert.Error(t, err)
	})
}

// PATCH /transactions/{id} (spec 0005 §11): o WHERE de UpdateCategory
// reconfere casa, linha viva, id e kind categorizável — e substitui categoria
// já escolhida, que é a diferença dele para SetCategoryWhereNull.
func TestUpdateCategoryTrocaSoACategoriaDaLinhaCertaDaCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		outraConta := s.makeAccount(t, ctx, minha.ID, "Outra")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")
		mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		lazer := s.makeCategory(t, ctx, minha.ID, "Lazer", category.KindExpense, nil)
		grupo := s.nextID("grp")

		vazia := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Description: "Sem categoria", AmountCents: 150_07})
		escolhida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Description: "Escolhida", CategoryID: &lazer.ID})
		vizinhaDeMesa := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Description: "Não é o alvo"})
		saida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Kind: transaction.KindTransferOut, TransferGroupID: &grupo})
		entrada := s.makeTransaction(t, ctx, minha.ID, outraConta.ID, txSpec{Kind: transaction.KindTransferIn, TransferGroupID: &grupo})
		excluida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{Description: "Excluída"})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		daVizinha := s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{Description: "Da vizinha"})

		// Linha sem categoria ganha a categoria; o SET tem duas colunas.
		depois := now().Add(time.Hour)
		require.NoError(t, s.transactions.UpdateCategory(ctx, minha.ID, vazia.ID, mercado.ID, depois))
		lida, err := s.transactions.ByID(ctx, minha.ID, vazia.ID)
		require.NoError(t, err)
		require.NotNil(t, lida.CategoryID)
		assert.Equal(t, mercado.ID, *lida.CategoryID)
		assert.Equal(t, "Sem categoria", lida.Description)
		assert.EqualValues(t, 150_07, lida.AmountCents)
		assert.Equal(t, vazia.OccurredOn, lida.OccurredOn)
		assert.Equal(t, vazia.AccountID, lida.AccountID)
		assert.Equal(t, vazia.CompetenceMonth, lida.CompetenceMonth)
		assert.True(t, depois.Equal(lida.UpdatedAt), "updated_at acompanha a escrita")

		// Categoria já escolhida É substituída (é a pessoa recategorizando).
		require.NoError(t, s.transactions.UpdateCategory(ctx, minha.ID, escolhida.ID, mercado.ID, depois))
		lida, err = s.transactions.ByID(ctx, minha.ID, escolhida.ID)
		require.NoError(t, err)
		assert.Equal(t, mercado.ID, *lida.CategoryID)

		// Só a linha apontada: a de ao lado continua sem categoria.
		lida, err = s.transactions.ByID(ctx, minha.ID, vizinhaDeMesa.ID)
		require.NoError(t, err)
		assert.Nil(t, lida.CategoryID)

		// Zero linhas afetadas é ErrNotFound: transferência (as duas pernas),
		// excluída, inexistente, de outra casa — e casa alheia tentando a
		// MINHA linha.
		for nome, caso := range map[string]struct{ casa, id string }{
			"perna de saída":            {minha.ID, saida.ID},
			"perna de entrada":          {minha.ID, entrada.ID},
			"excluída":                  {minha.ID, excluida.ID},
			"inexistente":               {minha.ID, s.nextID("t")},
			"da vizinha, pela minha":    {minha.ID, daVizinha.ID},
			"minha, pela casa vizinha":  {alheia.ID, vazia.ID},
			"minha, com categoria dela": {alheia.ID, escolhida.ID},
		} {
			err := s.transactions.UpdateCategory(ctx, caso.casa, caso.id, mercado.ID, now())
			assert.ErrorIs(t, err, transaction.ErrNotFound, nome)
		}
		lida, err = s.transactions.ByID(ctx, minha.ID, saida.ID)
		require.NoError(t, err)
		assert.Nil(t, lida.CategoryID, "transferência não recebe categoria")
		lida, err = s.transactions.ByIDIncludingDeleted(ctx, minha.ID, excluida.ID)
		require.NoError(t, err)
		assert.Nil(t, lida.CategoryID, "linha excluída não é tocada")
		lida, err = s.transactions.ByID(ctx, alheia.ID, daVizinha.ID)
		require.NoError(t, err)
		assert.Nil(t, lida.CategoryID, "a categoria da minha casa nunca chega à linha da vizinha")

		// Argumento vazio é erro antes de qualquer SQL.
		assert.Error(t, s.transactions.UpdateCategory(ctx, "", vazia.ID, mercado.ID, now()))
		assert.Error(t, s.transactions.UpdateCategory(ctx, minha.ID, "", mercado.ID, now()))
		assert.Error(t, s.transactions.UpdateCategory(ctx, minha.ID, vazia.ID, "", now()))
	})
}

// --- GET /transfers -----------------------------------------------------------

// par cria as duas pernas de uma transferência de `de` para `para` e devolve o
// grupo.
func (s *store) par(t *testing.T, householdID, de, para string, cents int64, on civil.Date) (grupo string, saida, entrada *transaction.Transaction) {
	t.Helper()
	ctx := t.Context()
	grupo = s.nextID("grp")
	saida = s.makeTransaction(t, ctx, householdID, de, txSpec{
		Kind: transaction.KindTransferOut, AmountCents: cents, OccurredOn: on, TransferGroupID: &grupo,
	})
	entrada = s.makeTransaction(t, ctx, householdID, para, txSpec{
		Kind: transaction.KindTransferIn, AmountCents: cents, OccurredOn: on, TransferGroupID: &grupo,
	})
	return grupo, saida, entrada
}

func TestListTransferLegsFiltraPorContaContraparteECursor(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "A")
		b := s.makeAccount(t, ctx, minha.ID, "B")
		c := s.makeAccount(t, ctx, minha.ID, "C")
		alheiaA := s.makeAccount(t, ctx, alheia.ID, "A")
		alheiaB := s.makeAccount(t, ctx, alheia.ID, "B")

		gAB, saidaAB, entradaAB := s.par(t, minha.ID, a.ID, b.ID, 100_00, civil.MustNew(2026, 8, 5))
		gBA, saidaBA, _ := s.par(t, minha.ID, b.ID, a.ID, 30_00, civil.MustNew(2026, 8, 10))
		gAC, saidaAC, _ := s.par(t, minha.ID, a.ID, c.ID, 50_00, civil.MustNew(2026, 8, 10))
		// Outro mês e outra casa: fora.
		s.par(t, minha.ID, a.ID, b.ID, 1_00, civil.MustNew(2026, 9, 1))
		s.par(t, alheia.ID, alheiaA.ID, alheiaB.ID, 999_00, civil.MustNew(2026, 8, 5))
		// Uma despesa comum no mês: nunca é perna.
		s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{OccurredOn: civil.MustNew(2026, 8, 6)})

		// Sem conta: UMA perna por par (a de saída), mais recente primeiro.
		pernas, err := s.transactions.ListTransferLegs(ctx, minha.ID, transaction.TransferFilter{CompetenceMonth: "2026-08"})
		require.NoError(t, err)
		require.Len(t, pernas, 3)
		for _, p := range pernas {
			assert.Equal(t, transaction.KindTransferOut, p.Kind)
		}
		// 10/08 antes de 05/08; entre os dois de 10/08, id maior primeiro.
		assert.Equal(t, civil.MustNew(2026, 8, 10), pernas[0].OccurredOn)
		assert.Equal(t, civil.MustNew(2026, 8, 10), pernas[1].OccurredOn)
		assert.Equal(t, saidaAB.ID, pernas[2].ID)
		assert.Greater(t, pernas[0].ID, pernas[1].ID)

		// Com conta A: as pernas de A, de saída E de entrada (A→B, B→A, A→C).
		deA, err := s.transactions.ListTransferLegs(ctx, minha.ID, transaction.TransferFilter{CompetenceMonth: "2026-08", AccountID: a.ID})
		require.NoError(t, err)
		require.Len(t, deA, 3)
		idsDeA := []string{deA[0].ID, deA[1].ID, deA[2].ID}
		assert.Contains(t, idsDeA, saidaAB.ID)
		assert.Contains(t, idsDeA, saidaAC.ID)
		assert.NotContains(t, idsDeA, saidaBA.ID, "a perna de B→A que está em A é a de ENTRADA")
		_ = gBA
		_ = gAC

		// Com conta A e contraparte B: só o par A–B (nos dois sentidos).
		entreAB, err := s.transactions.ListTransferLegs(ctx, minha.ID, transaction.TransferFilter{
			CompetenceMonth: "2026-08", AccountID: a.ID, CounterpartAccountID: b.ID,
		})
		require.NoError(t, err)
		require.Len(t, entreAB, 2)
		grupos := []string{*entreAB[0].TransferGroupID, *entreAB[1].TransferGroupID}
		assert.ElementsMatch(t, []string{gAB, gBA}, grupos)
		for _, p := range entreAB {
			assert.Equal(t, a.ID, p.AccountID, "as pernas devolvidas são as da conta A")
		}
		_ = entradaAB

		// Cursor: a página seguinte à primeira linha continua de onde parou.
		primeira, err := s.transactions.ListTransferLegs(ctx, minha.ID, transaction.TransferFilter{CompetenceMonth: "2026-08", Limit: 1})
		require.NoError(t, err)
		require.Len(t, primeira, 1)
		segunda, err := s.transactions.ListTransferLegs(ctx, minha.ID, transaction.TransferFilter{
			CompetenceMonth: "2026-08", Limit: 10,
			Cursor: &transaction.Cursor{OccurredOn: primeira[0].OccurredOn, ID: primeira[0].ID},
		})
		require.NoError(t, err)
		require.Len(t, segunda, 2)
		assert.NotEqual(t, primeira[0].ID, segunda[0].ID)

		// Conta alheia com a MINHA casa: nada — o id existe, mas não é meu.
		nada, err := s.transactions.ListTransferLegs(ctx, minha.ID, transaction.TransferFilter{CompetenceMonth: "2026-08", AccountID: alheiaA.ID})
		require.NoError(t, err)
		assert.Empty(t, nada)

		// Mês ausente e contraparte sem conta são erro.
		_, err = s.transactions.ListTransferLegs(ctx, minha.ID, transaction.TransferFilter{})
		assert.Error(t, err)
		_, err = s.transactions.ListTransferLegs(ctx, minha.ID, transaction.TransferFilter{CompetenceMonth: "2026-08", CounterpartAccountID: b.ID})
		assert.Error(t, err)
	})
}

func TestByTransferGroupsETransferLegsOfMonthSoVeemPernasVivasDaCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "A")
		b := s.makeAccount(t, ctx, minha.ID, "B")
		alheiaA := s.makeAccount(t, ctx, alheia.ID, "A")
		alheiaB := s.makeAccount(t, ctx, alheia.ID, "B")

		gAB, saidaAB, entradaAB := s.par(t, minha.ID, a.ID, b.ID, 100_00, civil.MustNew(2026, 8, 5))
		gBA, _, entradaBA := s.par(t, minha.ID, b.ID, a.ID, 30_00, civil.MustNew(2026, 8, 10))
		gAlheio, _, _ := s.par(t, alheia.ID, alheiaA.ID, alheiaB.ID, 999_00, civil.MustNew(2026, 8, 5))
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, entradaBA.ID, now()))

		pernas, err := s.transactions.ByTransferGroups(ctx, minha.ID, []string{gAB, gBA, gAlheio, gAB, ""})
		require.NoError(t, err)
		// gAB inteiro (2), gBA só a saída viva (1), gAlheio nada.
		require.Len(t, pernas, 3)
		ids := []string{pernas[0].ID, pernas[1].ID, pernas[2].ID}
		assert.Contains(t, ids, saidaAB.ID)
		assert.Contains(t, ids, entradaAB.ID)
		assert.NotContains(t, ids, entradaBA.ID, "perna excluída não é par de ninguém")

		resumo, err := s.transactions.TransferLegsOfMonth(ctx, minha.ID, "2026-08", 100)
		require.NoError(t, err)
		require.Len(t, resumo, 3)
		var soma int64
		for _, r := range resumo {
			assert.NotEqual(t, gAlheio, r.TransferGroupID)
			soma += r.AmountCents
		}
		assert.EqualValues(t, 100_00+100_00+30_00, soma)

		// O limite corta (é assim que o serviço descobre o teto).
		cortado, err := s.transactions.TransferLegsOfMonth(ctx, minha.ID, "2026-08", 2)
		require.NoError(t, err)
		assert.Len(t, cortado, 2)

		vazio, err := s.transactions.ByTransferGroups(ctx, minha.ID, nil)
		require.NoError(t, err)
		assert.Empty(t, vazio)
	})
}

func TestSumByAccountUntilRespeitaADataEACasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "A")
		b := s.makeAccount(t, ctx, minha.ID, "B")
		alheiaA := s.makeAccount(t, ctx, alheia.ID, "A")

		s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{Kind: transaction.KindIncome, AmountCents: 1_000_00, OccurredOn: civil.MustNew(2026, 8, 1)})
		s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{Kind: transaction.KindExpense, AmountCents: 100_00, OccurredOn: civil.MustNew(2026, 8, 31)})
		s.par(t, minha.ID, a.ID, b.ID, 200_00, civil.MustNew(2026, 8, 15))
		// Depois do fim do mês: não entra.
		s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{Kind: transaction.KindExpense, AmountCents: 999_00, OccurredOn: civil.MustNew(2026, 9, 1)})
		s.makeTransaction(t, ctx, alheia.ID, alheiaA.ID, txSpec{Kind: transaction.KindIncome, AmountCents: 5_00, OccurredOn: civil.MustNew(2026, 8, 1)})

		somas, err := s.transactions.SumByAccountUntil(ctx, minha.ID, civil.MustNew(2026, 8, 31))
		require.NoError(t, err)
		assert.EqualValues(t, 1_000_00-100_00-200_00, somas[a.ID])
		assert.EqualValues(t, 200_00, somas[b.ID])
		assert.NotContains(t, somas, alheiaA.ID)

		// Até o dia 14: a transferência do dia 15 ainda não aconteceu.
		somas, err = s.transactions.SumByAccountUntil(ctx, minha.ID, civil.MustNew(2026, 8, 14))
		require.NoError(t, err)
		assert.EqualValues(t, 1_000_00, somas[a.ID])
		assert.NotContains(t, somas, b.ID)

		// Coerência com SumByAccount quando a data cobre tudo.
		tudo, err := s.transactions.SumByAccount(ctx, minha.ID)
		require.NoError(t, err)
		ateSempre, err := s.transactions.SumByAccountUntil(ctx, minha.ID, civil.MustNew(2099, 12, 31))
		require.NoError(t, err)
		assert.Equal(t, tudo, ateSempre)

		_, err = s.transactions.SumByAccountUntil(ctx, minha.ID, civil.Date{})
		assert.Error(t, err, "data zero é erro, não 'sem limite'")
	})
}

// --- ação `link` ------------------------------------------------------------

func TestTransferLegsForLinkingDescartaPernaSemContraparteViva(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "A")
		b := s.makeAccount(t, ctx, minha.ID, "B")
		c := s.makeAccount(t, ctx, minha.ID, "C")
		alheiaA := s.makeAccount(t, ctx, alheia.ID, "A")
		alheiaB := s.makeAccount(t, ctx, alheia.ID, "B")

		// Par completo A→B dentro da janela: candidato.
		gAB, saidaAB, _ := s.par(t, minha.ID, a.ID, b.ID, 100_00, civil.MustNew(2026, 8, 10))
		// Par C→A: a perna em A é de ENTRADA, também candidata, contraparte C.
		gCA, _, entradaCA := s.par(t, minha.ID, c.ID, a.ID, 40_00, civil.MustNew(2026, 8, 12))
		// Contraparte EXCLUÍDA: a perna em A fica órfã e é descartada.
		_, saidaOrfa, entradaOrfa := s.par(t, minha.ID, a.ID, b.ID, 70_00, civil.MustNew(2026, 8, 11))
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, entradaOrfa.ID, now()))
		// Perna em A excluída: não é candidata.
		_, saidaExcluida, _ := s.par(t, minha.ID, a.ID, b.ID, 60_00, civil.MustNew(2026, 8, 11))
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, saidaExcluida.ID, now()))
		// Fora da janela (±3 dias em torno de 10–12/08): 01/08 e 20/08.
		s.par(t, minha.ID, a.ID, b.ID, 1_00, civil.MustNew(2026, 8, 1))
		s.par(t, minha.ID, a.ID, b.ID, 2_00, civil.MustNew(2026, 8, 20))
		// Na borda da folga (07/08 = 10 − 3): entra.
		gBorda, saidaBorda, _ := s.par(t, minha.ID, a.ID, c.ID, 3_00, civil.MustNew(2026, 8, 7))
		// Outra casa: jamais.
		s.par(t, alheia.ID, alheiaA.ID, alheiaB.ID, 100_00, civil.MustNew(2026, 8, 10))
		// Despesa comum de A no período: não é perna.
		s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{OccurredOn: civil.MustNew(2026, 8, 10)})

		pernas, err := s.transactions.TransferLegsForLinking(ctx, minha.ID, a.ID, civil.MustNew(2026, 8, 10), civil.MustNew(2026, 8, 12))
		require.NoError(t, err)
		require.Len(t, pernas, 3)

		porID := map[string]transaction.TransferLeg{}
		for _, p := range pernas {
			porID[p.ID] = p
		}
		require.Contains(t, porID, saidaAB.ID)
		assert.Equal(t, transaction.KindTransferOut, porID[saidaAB.ID].Kind)
		assert.Equal(t, b.ID, porID[saidaAB.ID].CounterpartAccountID)
		assert.Equal(t, gAB, porID[saidaAB.ID].TransferGroupID)
		assert.EqualValues(t, 100_00, porID[saidaAB.ID].AmountCents)
		assert.Equal(t, civil.MustNew(2026, 8, 10), porID[saidaAB.ID].OccurredOn)

		require.Contains(t, porID, entradaCA.ID)
		assert.Equal(t, transaction.KindTransferIn, porID[entradaCA.ID].Kind)
		assert.Equal(t, c.ID, porID[entradaCA.ID].CounterpartAccountID)
		assert.Equal(t, gCA, porID[entradaCA.ID].TransferGroupID)

		require.Contains(t, porID, saidaBorda.ID)
		assert.Equal(t, gBorda, porID[saidaBorda.ID].TransferGroupID)

		assert.NotContains(t, porID, saidaOrfa.ID, "perna sem contraparte viva não é par de ninguém")
		assert.NotContains(t, porID, saidaExcluida.ID)

		// Ordem constante: (occurred_on, id) ascendente.
		assert.Equal(t, saidaBorda.ID, pernas[0].ID)
		assert.Equal(t, saidaAB.ID, pernas[1].ID)
		assert.Equal(t, entradaCA.ID, pernas[2].ID)

		// Conta alheia com a minha casa: nada. Intervalo inválido: erro.
		nada, err := s.transactions.TransferLegsForLinking(ctx, minha.ID, alheiaA.ID, civil.MustNew(2026, 8, 10), civil.MustNew(2026, 8, 12))
		require.NoError(t, err)
		assert.Empty(t, nada)
		_, err = s.transactions.TransferLegsForLinking(ctx, minha.ID, a.ID, civil.MustNew(2026, 8, 12), civil.MustNew(2026, 8, 10))
		assert.Error(t, err)
		_, err = s.transactions.TransferLegsForLinking(ctx, minha.ID, "", civil.MustNew(2026, 8, 10), civil.MustNew(2026, 8, 12))
		assert.Error(t, err)
	})
}

func TestLinkImportSoAlcancaAPernaVivaDaContaDoLote(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "A")
		b := s.makeAccount(t, ctx, minha.ID, "B")

		_, saida, entrada := s.par(t, minha.ID, a.ID, b.ID, 100_00, civil.MustNew(2026, 8, 10))
		lote := s.nextID("ib")
		ext := "NU-123"
		campos := transaction.LinkFields{
			AccountID: b.ID, ExternalID: &ext, DedupKey: s.nextID("dk"), DedupOrdinal: 1,
			ImportBatchID: lote, UpdatedAt: now(),
		}

		// account_id errado (o lote é de B, a perna pedida está em A): ZERO
		// linhas afetadas, ErrNotFound — e nada mudou na perna de A.
		err := s.transactions.LinkImport(ctx, minha.ID, saida.ID, campos)
		require.ErrorIs(t, err, transaction.ErrNotFound)
		intacta, err := s.transactions.ByID(ctx, minha.ID, saida.ID)
		require.NoError(t, err)
		assert.Nil(t, intacta.ImportBatchID)
		assert.Nil(t, intacta.ExternalID)

		// Casa errada: idem.
		err = s.transactions.LinkImport(ctx, alheia.ID, entrada.ID, campos)
		require.ErrorIs(t, err, transaction.ErrNotFound)

		// A perna certa (em B): grava só as cinco colunas.
		require.NoError(t, s.transactions.LinkImport(ctx, minha.ID, entrada.ID, campos))
		vinculada, err := s.transactions.ByID(ctx, minha.ID, entrada.ID)
		require.NoError(t, err)
		require.NotNil(t, vinculada.ImportBatchID)
		assert.Equal(t, lote, *vinculada.ImportBatchID)
		require.NotNil(t, vinculada.ExternalID)
		assert.Equal(t, "NU-123", *vinculada.ExternalID)
		assert.Equal(t, campos.DedupKey, vinculada.DedupKey)
		assert.Equal(t, 1, vinculada.DedupOrdinal)
		assert.EqualValues(t, 100_00, vinculada.AmountCents, "valor intacto")
		assert.Equal(t, b.ID, vinculada.AccountID, "conta intacta")
		assert.Equal(t, transaction.KindTransferIn, vinculada.Kind, "kind intacto")
		assert.Equal(t, entrada.TransferGroupID, vinculada.TransferGroupID, "grupo intacto")

		// Chave já ocupada por OUTRO lançamento: o índice único arbitra.
		ocupada := s.makeTransaction(t, ctx, minha.ID, b.ID, txSpec{DedupKey: s.nextID("dk")})
		colide := campos
		colide.DedupKey = ocupada.DedupKey
		colide.ImportBatchID = s.nextID("ib")
		err = s.transactions.LinkImport(ctx, minha.ID, entrada.ID, colide)
		require.ErrorIs(t, err, transaction.ErrDuplicateDedup)
		assert.NotContains(t, err.Error(), ocupada.DedupKey, "o erro nativo não é embrulhado: ecoaria a chave")

		// Perna excluída entre a análise e o confirm: zero linhas.
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, entrada.ID, now()))
		campos.ImportBatchID = s.nextID("ib")
		err = s.transactions.LinkImport(ctx, minha.ID, entrada.ID, campos)
		require.ErrorIs(t, err, transaction.ErrNotFound)

		// Campos incompletos são recusados antes de qualquer SQL.
		semChave := campos
		semChave.DedupKey = ""
		require.ErrorIs(t, s.transactions.LinkImport(ctx, minha.ID, entrada.ID, semChave), transaction.ErrIncomplete)
		semLote := campos
		semLote.ImportBatchID = ""
		assert.Error(t, s.transactions.LinkImport(ctx, minha.ID, entrada.ID, semLote))
	})
}

func TestByIDIncludingDeletedEOccurredOnByIDsEnxergamAExcluidaMasNuncaAVizinha(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		viva := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{OccurredOn: civil.MustNew(2026, 8, 3)})
		excluida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{OccurredOn: civil.MustNew(2026, 8, 4)})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		daVizinha := s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{OccurredOn: civil.MustNew(2026, 8, 5)})

		// ByID esconde a excluída; ByIDIncludingDeleted a mostra, COM DeletedAt.
		_, err := s.transactions.ByID(ctx, minha.ID, excluida.ID)
		require.ErrorIs(t, err, transaction.ErrNotFound)
		lida, err := s.transactions.ByIDIncludingDeleted(ctx, minha.ID, excluida.ID)
		require.NoError(t, err)
		assert.NotNil(t, lida.DeletedAt)
		lida, err = s.transactions.ByIDIncludingDeleted(ctx, minha.ID, viva.ID)
		require.NoError(t, err)
		assert.Nil(t, lida.DeletedAt)

		// Outra casa e inexistente: o MESMO ErrNotFound.
		_, err = s.transactions.ByIDIncludingDeleted(ctx, minha.ID, daVizinha.ID)
		require.ErrorIs(t, err, transaction.ErrNotFound)
		_, err = s.transactions.ByIDIncludingDeleted(ctx, minha.ID, "nao-existe")
		require.ErrorIs(t, err, transaction.ErrNotFound)
		_, err = s.transactions.ByIDIncludingDeleted(ctx, minha.ID, "")
		require.ErrorIs(t, err, transaction.ErrNotFound)

		datas, err := s.transactions.OccurredOnByIDs(ctx, minha.ID, []string{viva.ID, excluida.ID, daVizinha.ID, "nao-existe", "", viva.ID})
		require.NoError(t, err)
		assert.Equal(t, map[string]civil.Date{
			viva.ID:     civil.MustNew(2026, 8, 3),
			excluida.ID: civil.MustNew(2026, 8, 4),
		}, datas)

		vazio, err := s.transactions.OccurredOnByIDs(ctx, minha.ID, nil)
		require.NoError(t, err)
		assert.Empty(t, vazio)
	})
}
