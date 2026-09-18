package transaction_test

import (
	"fmt"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Critério 10 da spec 0005 (§8): cada par UMA vez; `pairs` fecha com a soma
// de `items` do mês inteiro (não da página); netCents == aToB − bToA;
// balanceAtMonthEndCents igual ao balanceCents de GET /accounts no mês
// corrente; accountId de outra casa é 404 igual ao inexistente.

// cenarioDeTransferencias monta três contas e um mês com transferências nos
// dois sentidos entre A e B, uma entre A e C, uma despesa comum, uma
// transferência de outro mês, um par excluído e a vizinha com o par dela.
func cenarioDeTransferencias(t *testing.T) *ambiente {
	t.Helper()
	amb := novoAmbiente(t)
	a := amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	a.OpeningBalanceCents = 1_000_00
	amb.contas.add(a)
	b := amb.conta(minhaCasa, "acc-b", "C6", account.KindChecking)
	b.OpeningBalanceCents = 500_00
	amb.contas.add(b)
	amb.conta(minhaCasa, "acc-c", "Poupança", account.KindSavings)

	amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 300_00, 3, "g-ab-1")
	amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 200_00, 10, "g-ab-2")
	amb.parDeTransferencia(minhaCasa, "acc-b", "acc-a", 50_00, 15, "g-ba-1")
	amb.parDeTransferencia(minhaCasa, "acc-a", "acc-c", 1_000_00, 20, "g-ac-1")

	// Despesa comum no mês: não é transferência, não entra em lugar nenhum
	// de /transfers — mas entra no saldo.
	amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "Padaria", 30_00, 4)
	// Transferência de outro mês: fora da janela, mas dentro do saldo? Não —
	// é de outubro, e o saldo é até o fim de setembro.
	saidaOut, entradaOut := amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 999_00, 1, "g-out")
	for _, id := range []string{saidaOut.ID, entradaOut.ID} {
		l := amb.repo.linhas[id]
		l.OccurredOn = civil.MustNew(2026, 10, 1)
		l.CompetenceMonth = "2026-10"
		amb.repo.linhas[id] = l
	}
	// Par excluído (as duas pernas): não aparece em nada.
	sx, ex := amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 777_00, 12, "g-excluido")
	require.NoError(t, amb.svc.SoftDelete(t.Context(), ator(minhaCasa), sx.ID))
	assert.NotNil(t, amb.repo.linhas[ex.ID].DeletedAt)

	// A vizinha.
	amb.conta(outraCasa, "acc-x", "X", account.KindChecking)
	amb.conta(outraCasa, "acc-y", "Y", account.KindChecking)
	amb.parDeTransferencia(outraCasa, "acc-x", "acc-y", 123_00, 5, "g-alheio")
	return amb
}

func TestListTransfersTrazCadaParUmaVezComTotaisDoMesInteiro(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTransferencias(t)
	view, err := amb.svc.ListTransfers(t.Context(), ator(minhaCasa), transaction.TransferListInput{Month: "2026-09"})
	require.NoError(t, err)

	require.Len(t, view.Items, 4, "quatro pares vivos em setembro, cada um UMA vez")
	assert.Nil(t, view.NextCursor)
	vistos := map[string]bool{}
	for _, it := range view.Items {
		assert.False(t, vistos[it.GroupID], "par repetido: %s", it.GroupID)
		vistos[it.GroupID] = true
		assert.Equal(t, "2026-09", it.CompetenceMonth)
		assert.Equal(t, transaction.SourceImport, it.Source)
		assert.NotEmpty(t, it.FromAccountName)
		assert.NotEmpty(t, it.ToAccountName)
	}
	// Ordem decrescente por data; o primeiro é o de 20/09 (A → C).
	assert.Equal(t, "g-ac-1", view.Items[0].GroupID)
	assert.Equal(t, "acc-a", view.Items[0].FromAccountID)
	assert.Equal(t, "Nubank", view.Items[0].FromAccountName)
	assert.Equal(t, "acc-c", view.Items[0].ToAccountID)
	assert.Equal(t, "Poupança", view.Items[0].ToAccountName)
	assert.EqualValues(t, 1_000_00, view.Items[0].AmountCents)
	// O de 15/09 é B → A: from é a perna de SAÍDA.
	assert.Equal(t, "g-ba-1", view.Items[1].GroupID)
	assert.Equal(t, "acc-b", view.Items[1].FromAccountID)
	assert.Equal(t, "acc-a", view.Items[1].ToAccountID)

	require.Len(t, view.Pairs, 2, "A–B e A–C")
	ab, ac := view.Pairs[0], view.Pairs[1]
	assert.Equal(t, "acc-a", ab.AccountAID)
	assert.Equal(t, "Nubank", ab.AccountAName)
	assert.Equal(t, "acc-b", ab.AccountBID)
	assert.Equal(t, "C6", ab.AccountBName)
	assert.EqualValues(t, 500_00, ab.AToBCents)
	assert.EqualValues(t, 50_00, ab.BToACents)
	assert.EqualValues(t, 450_00, ab.NetCents)
	assert.EqualValues(t, 3, ab.Count)
	assert.Equal(t, "acc-c", ac.AccountBID)
	assert.EqualValues(t, 1_000_00, ac.AToBCents)
	assert.EqualValues(t, 0, ac.BToACents)
	assert.EqualValues(t, 1, ac.Count)

	// pairs fecha com a soma dos items.
	conferirParesContraItens(t, view.Items, view.Pairs)

	// Saldos: sem filtro, todas as contas que aparecem em pairs, em ordem de id.
	require.Len(t, view.Balances, 3)
	assert.Equal(t, []string{"acc-a", "acc-b", "acc-c"}, []string{view.Balances[0].AccountID, view.Balances[1].AccountID, view.Balances[2].AccountID})
	assert.Equal(t, "Nubank", view.Balances[0].AccountName)
	// A: 1000 − 300 − 200 + 50 − 1000 − 30 (padaria) = −480; outubro e o
	// par excluído ficam fora.
	assert.EqualValues(t, -480_00, view.Balances[0].BalanceAtMonthEndCents)
	// B: 500 + 300 + 200 − 50 = 950.
	assert.EqualValues(t, 950_00, view.Balances[1].BalanceAtMonthEndCents)
	// C: 0 + 1000.
	assert.EqualValues(t, 1_000_00, view.Balances[2].BalanceAtMonthEndCents)
}

// conferirParesContraItens soma os items por par e compara com pairs.
func conferirParesContraItens(t *testing.T, itens []transaction.TransferItemView, pares []transaction.TransferPairView) {
	t.Helper()
	type soma struct{ aToB, bToA, count int64 }
	somas := map[string]*soma{}
	for _, it := range itens {
		a, b := it.FromAccountID, it.ToAccountID
		if b < a {
			a, b = b, a
		}
		k := a + "|" + b
		if somas[k] == nil {
			somas[k] = &soma{}
		}
		if it.FromAccountID == a {
			somas[k].aToB += it.AmountCents
		} else {
			somas[k].bToA += it.AmountCents
		}
		somas[k].count++
	}
	require.Len(t, pares, len(somas))
	for _, p := range pares {
		s := somas[p.AccountAID+"|"+p.AccountBID]
		require.NotNil(t, s, "par %s–%s sem item", p.AccountAID, p.AccountBID)
		assert.EqualValues(t, s.aToB, p.AToBCents)
		assert.EqualValues(t, s.bToA, p.BToACents)
		assert.EqualValues(t, s.count, p.Count)
		assert.Equal(t, p.AToBCents-p.BToACents, p.NetCents, "netCents == aToB − bToA")
	}
}

// O saldo no fim do mês CORRENTE (sem lançamento datado depois dele) é o
// mesmo balanceCents que GET /accounts calcula: abertura + SumByAccount.
func TestListTransfersSaldoNoFimDoMesBateComOSaldoDaConta(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTransferencias(t)
	// Outubro é o último mês com lançamento: ali o saldo de fim de mês é o
	// saldo corrente de /accounts.
	view, err := amb.svc.ListTransfers(t.Context(), ator(minhaCasa), transaction.TransferListInput{Month: "2026-10"})
	require.NoError(t, err)
	require.Len(t, view.Items, 1)
	require.Len(t, view.Balances, 2)

	somas, err := amb.repo.SumByAccount(t.Context(), minhaCasa)
	require.NoError(t, err)
	for _, b := range view.Balances {
		conta, err := amb.contas.ByID(t.Context(), minhaCasa, b.AccountID)
		require.NoError(t, err)
		assert.Equal(t, conta.OpeningBalanceCents+somas[b.AccountID], b.BalanceAtMonthEndCents, "conta %s", b.AccountID)
	}
}

func TestListTransfersFiltraPorContaEContraparte(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTransferencias(t)

	t.Run("só accountId traz tudo que toca a conta", func(t *testing.T) {
		view, err := amb.svc.ListTransfers(t.Context(), ator(minhaCasa), transaction.TransferListInput{Month: "2026-09", AccountID: "acc-b"})
		require.NoError(t, err)
		require.Len(t, view.Items, 3, "os três pares A–B, nos dois sentidos")
		for _, it := range view.Items {
			assert.True(t, it.FromAccountID == "acc-b" || it.ToAccountID == "acc-b")
		}
		require.Len(t, view.Pairs, 1)
		assert.Equal(t, "acc-a", view.Pairs[0].AccountAID)
		assert.Equal(t, "acc-b", view.Pairs[0].AccountBID)
		conferirParesContraItens(t, view.Items, view.Pairs)
		require.Len(t, view.Balances, 1, "com filtro, só a conta do filtro")
		assert.Equal(t, "acc-b", view.Balances[0].AccountID)
		assert.EqualValues(t, 950_00, view.Balances[0].BalanceAtMonthEndCents)
	})

	t.Run("accountId e counterpartAccountId trazem só o par", func(t *testing.T) {
		view, err := amb.svc.ListTransfers(t.Context(), ator(minhaCasa), transaction.TransferListInput{
			Month: "2026-09", AccountID: "acc-c", CounterpartAccountID: "acc-a",
		})
		require.NoError(t, err)
		require.Len(t, view.Items, 1)
		assert.Equal(t, "g-ac-1", view.Items[0].GroupID)
		require.Len(t, view.Pairs, 1)
		assert.Equal(t, "acc-a", view.Pairs[0].AccountAID, "A é sempre o menor id, independente da ordem do filtro")
		assert.Equal(t, "acc-c", view.Pairs[0].AccountBID)
		require.Len(t, view.Balances, 2)
		assert.Equal(t, "acc-a", view.Balances[0].AccountID)
		assert.Equal(t, "acc-c", view.Balances[1].AccountID)
	})

	t.Run("par sem transferência no mês", func(t *testing.T) {
		view, err := amb.svc.ListTransfers(t.Context(), ator(minhaCasa), transaction.TransferListInput{
			Month: "2026-09", AccountID: "acc-b", CounterpartAccountID: "acc-c",
		})
		require.NoError(t, err)
		assert.NotNil(t, view.Items)
		assert.Empty(t, view.Items)
		assert.NotNil(t, view.Pairs)
		assert.Empty(t, view.Pairs)
		assert.Len(t, view.Balances, 2, "os saldos das duas contas do filtro vêm mesmo sem par")
		assert.Nil(t, view.NextCursor)
	})
}

// pairs é do MÊS INTEIRO, não da página: com limit 1, a primeira página tem
// um item e os totais continuam os de todo o mês.
func TestListTransfersPaginaSemRepetirEOsTotaisSaoDoMesInteiro(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTransferencias(t)
	var todos []transaction.TransferItemView
	var primeirosPares []transaction.TransferPairView
	cursor := ""
	for pagina := 0; pagina < 10; pagina++ {
		view, err := amb.svc.ListTransfers(t.Context(), ator(minhaCasa), transaction.TransferListInput{Month: "2026-09", Limit: 1, Cursor: cursor})
		require.NoError(t, err)
		require.Len(t, view.Items, 1)
		if pagina == 0 {
			primeirosPares = view.Pairs
		}
		assert.Equal(t, primeirosPares, view.Pairs, "os totais não mudam de página para página")
		todos = append(todos, view.Items...)
		if view.NextCursor == nil {
			break
		}
		cursor = *view.NextCursor
	}
	require.Len(t, todos, 4)
	conferirParesContraItens(t, todos, primeirosPares)
}

func TestListTransfersValidaOsFiltros(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTransferencias(t)
	chamar := func(in transaction.TransferListInput) error {
		_, err := amb.svc.ListTransfers(t.Context(), ator(minhaCasa), in)
		return err
	}

	require.ErrorIs(t, chamar(transaction.TransferListInput{Month: "2026-13"}), transaction.ErrInvalidMonth)
	require.ErrorIs(t, chamar(transaction.TransferListInput{Month: ""}), transaction.ErrInvalidMonth)
	require.ErrorIs(t, chamar(transaction.TransferListInput{Month: "2026-09", CounterpartAccountID: "acc-b"}), transaction.ErrCounterpartNeedsAccount)
	require.ErrorIs(t, chamar(transaction.TransferListInput{Month: "2026-09", AccountID: "acc-a", CounterpartAccountID: "acc-a"}), transaction.ErrSameAccountFilter)
	require.ErrorIs(t, chamar(transaction.TransferListInput{Month: "2026-09", Cursor: "nao-e-base64-valido!!"}), transaction.ErrInvalidCursor)

	// S1: conta da vizinha e conta inexistente são o MESMO erro.
	errAlheia := chamar(transaction.TransferListInput{Month: "2026-09", AccountID: "acc-x"})
	require.ErrorIs(t, errAlheia, transaction.ErrNotFound)
	errInexistente := chamar(transaction.TransferListInput{Month: "2026-09", AccountID: "00000000-0000-7000-8000-999999999999"})
	require.ErrorIs(t, errInexistente, transaction.ErrNotFound)
	assert.Equal(t, errAlheia.Error(), errInexistente.Error())
	require.ErrorIs(t, chamar(transaction.TransferListInput{Month: "2026-09", AccountID: "acc-a", CounterpartAccountID: "acc-x"}), transaction.ErrNotFound)

	_, err := amb.svc.ListTransfers(t.Context(), transaction.Actor{UserID: usuario}, transaction.TransferListInput{Month: "2026-09"})
	require.ErrorIs(t, err, transaction.ErrNotFound, "sem casa no token")
}

func TestListTransfersNaoVeATransferenciaDaVizinha(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTransferencias(t)
	view, err := amb.svc.ListTransfers(t.Context(), ator(outraCasa), transaction.TransferListInput{Month: "2026-09"})
	require.NoError(t, err)
	require.Len(t, view.Items, 1)
	assert.Equal(t, "g-alheio", view.Items[0].GroupID)
	require.Len(t, view.Pairs, 1)
	assert.EqualValues(t, 123_00, view.Pairs[0].AToBCents)
	require.Len(t, view.Balances, 2)
}

// Defesa: grupo que não tem exatamente duas pernas vivas (uma de saída e uma
// de entrada, em contas diferentes) é descartado da lista e dos totais.
func TestListTransfersDescartaGrupoQueNaoEUmParCoerente(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "A", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "B", account.KindChecking)
	amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 100_00, 1, "g-bom")

	// Meia transferência: a perna de entrada foi excluída "por fora" (o
	// serviço nunca faz isso; o teste simula o dado inconsistente).
	_, entrada := amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 200_00, 2, "g-meio")
	l := amb.repo.linhas[entrada.ID]
	l.DeletedAt = ptr(agora)
	amb.repo.linhas[entrada.ID] = l
	// Duas pernas de saída no mesmo grupo.
	grupo := "g-duas-saidas"
	for _, conta := range []string{"acc-a", "acc-b"} {
		amb.repo.semear(transaction.Transaction{
			HouseholdID: minhaCasa, AccountID: conta, Kind: transaction.KindTransferOut, AmountCents: 300_00,
			Description: "x", OccurredOn: civil.MustNew(2026, 9, 3), TransferGroupID: &grupo,
		})
	}

	view, err := amb.svc.ListTransfers(t.Context(), ator(minhaCasa), transaction.TransferListInput{Month: "2026-09"})
	require.NoError(t, err)
	require.Len(t, view.Items, 1)
	assert.Equal(t, "g-bom", view.Items[0].GroupID)
	require.Len(t, view.Pairs, 1)
	assert.EqualValues(t, 100_00, view.Pairs[0].AToBCents)
	assert.EqualValues(t, 1, view.Pairs[0].Count)
}

func TestListTransfersRecusaMesAcimaDoTetoDePernas(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "A", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "B", account.KindChecking)
	for i := 0; i <= transaction.MaxTransferLegsPerMonth/2; i++ {
		amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 1, 1, fmt.Sprintf("g-%d", i))
	}

	_, err := amb.svc.ListTransfers(t.Context(), ator(minhaCasa), transaction.TransferListInput{Month: "2026-09"})
	require.ErrorIs(t, err, transaction.ErrTooManyTransfers)
	assert.True(t, transaction.IsValidationError(err))
}
