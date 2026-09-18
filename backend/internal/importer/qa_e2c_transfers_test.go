package importer_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QA da E2c — critério 10 da spec 0005 sobre SQLite REAL, pela API: `pairs`
// fecha com a soma de `items` do mês inteiro mesmo quando há mais de uma
// página (cursor SQL de verdade), e `balanceAtMonthEndCents` é igual ao
// `balanceCents` de GET /accounts no mês corrente — e diverge exatamente pelo
// lançamento datado no mês seguinte.

// contasPelaAPI roda GET /accounts?includeArchived=true e devolve id -> saldo.
func contasPelaAPI(t *testing.T, a *ambiente) map[string]int64 {
	t.Helper()
	h := account.NewHandler(a.contaSvc, logging.Discard(), 0)
	r := requisicao(t, http.MethodGet, "/api/v1/accounts?includeArchived=true", nil, identidade(a.casa.ID, a.usuario.ID))
	rec := httptest.NewRecorder()
	h.List(rec, r)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var view account.ListView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	out := map[string]int64{}
	for _, c := range view.Items {
		out[c.ID] = c.BalanceCents
	}
	return out
}

func TestPairsFechaComASomaDosItensDoMesInteiroEmVariasPaginasNoSQLite(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	ator := a.atorDeLancamento(a.casa.ID, a.usuario.ID)
	contaA := a.conta(t, a.casa.ID, "Nubank", account.KindChecking, "nubank")
	contaB := a.conta(t, a.casa.ID, "Itaú", account.KindChecking, account.InstitutionOther)
	contaC := a.conta(t, a.casa.ID, "Poupança", account.KindSavings, account.InstitutionOther)

	// Sete pares no mês, em três sentidos e dois dias repetidos: com limit=2
	// são quatro páginas, e o cursor precisa desempatar por id no mesmo dia.
	a.parManual(t, ator, contaA.ID, contaB.ID, 300_00, 3)
	a.parManual(t, ator, contaA.ID, contaB.ID, 200_00, 10)
	a.parManual(t, ator, contaA.ID, contaB.ID, 1, 10) // mesmo dia, um centavo
	a.parManual(t, ator, contaB.ID, contaA.ID, 50_00, 15)
	a.parManual(t, ator, contaB.ID, contaA.ID, 50_00, 15) // mesmo dia e valor
	a.parManual(t, ator, contaA.ID, contaC.ID, 1_000_00, 20)
	a.parManual(t, ator, contaC.ID, contaA.ID, 1_000_00, 21) // vai e volta: líquido zero
	// Fora do mês (1º de setembro): não entra em nada.
	_, err := a.txSvc.CreateBatch(t.Context(), ator, transaction.CreateBatchInput{
		Source: transaction.SourceManual,
		Rows: []transaction.NewTransaction{{
			Kind: transaction.KindTransferOut, AccountID: contaA.ID, AmountCents: 5, Description: "setembro",
			OccurredOn: civil.MustNew(2026, 9, 1), TransferGroupID: ptr("g-set"), DedupKey: dedup.PairKey("g-set-out"),
		}, {
			Kind: transaction.KindTransferIn, AccountID: contaB.ID, AmountCents: 5, Description: "setembro",
			OccurredOn: civil.MustNew(2026, 9, 1), TransferGroupID: ptr("g-set"), DedupKey: dedup.PairKey("g-set-in"),
		}},
	})
	require.NoError(t, err)

	var todos []transaction.TransferItemView
	var paresDaPrimeira []transaction.TransferPairView
	var saldosDaPrimeira []transaction.TransferBalanceView
	cursor := ""
	paginas := 0
	for {
		query := "month=2026-08&limit=2"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		view := transfersDecodificadas(t, transfersPelaAPI(t, a, a.casa.ID, a.usuario.ID, query))
		paginas++
		require.LessOrEqual(t, len(view.Items), 2)
		if paginas == 1 {
			paresDaPrimeira, saldosDaPrimeira = view.Pairs, view.Balances
		}
		assert.Equal(t, paresDaPrimeira, view.Pairs, "página %d: pairs é do mês inteiro, não da página", paginas)
		assert.Equal(t, saldosDaPrimeira, view.Balances, "página %d: balances não muda de página", paginas)
		todos = append(todos, view.Items...)
		if view.NextCursor == nil {
			break
		}
		cursor = *view.NextCursor
		require.Less(t, paginas, 10, "cursor em loop")
	}
	assert.Equal(t, 4, paginas, "7 pares de agosto em páginas de 2: 2+2+2+1")
	require.Len(t, todos, 7)

	// Sem repetição entre páginas, e os totais fecham com os itens.
	vistos := map[string]bool{}
	for _, it := range todos {
		assert.False(t, vistos[it.GroupID], "par repetido entre páginas: %s", it.GroupID)
		vistos[it.GroupID] = true
	}
	type soma struct{ aToB, bToA, count int64 }
	somas := map[string]*soma{}
	for _, it := range todos {
		x, y := it.FromAccountID, it.ToAccountID
		if y < x {
			x, y = y, x
		}
		k := x + "|" + y
		if somas[k] == nil {
			somas[k] = &soma{}
		}
		if it.FromAccountID == x {
			somas[k].aToB += it.AmountCents
		} else {
			somas[k].bToA += it.AmountCents
		}
		somas[k].count++
	}
	require.Len(t, paresDaPrimeira, len(somas), "um par por dupla de contas")
	for _, p := range paresDaPrimeira {
		s := somas[p.AccountAID+"|"+p.AccountBID]
		require.NotNil(t, s, "par %s–%s sem item", p.AccountAID, p.AccountBID)
		assert.EqualValues(t, s.aToB, p.AToBCents)
		assert.EqualValues(t, s.bToA, p.BToACents)
		assert.EqualValues(t, s.count, p.Count)
		assert.Equal(t, p.AToBCents-p.BToACents, p.NetCents)
		assert.Less(t, p.AccountAID, p.AccountBID, "A é sempre o menor id")
	}
	// Os números concretos: A–B tem 5 pares, 500,01 num sentido e 100,00 no
	// outro; A–C vai e volta e fecha em zero.
	porPar := map[string]transaction.TransferPairView{}
	for _, p := range paresDaPrimeira {
		porPar[p.AccountAID+"|"+p.AccountBID] = p
	}
	ab := porPar[minMax(contaA.ID, contaB.ID)]
	assert.EqualValues(t, 5, ab.Count)
	assert.EqualValues(t, 600_01, ab.AToBCents+ab.BToACents)
	assert.EqualValues(t, 400_01, absInt64(ab.NetCents))
	ac := porPar[minMax(contaA.ID, contaC.ID)]
	assert.EqualValues(t, 2, ac.Count)
	assert.Zero(t, ac.NetCents, "vai e volta do mesmo valor: líquido zero")
	assert.EqualValues(t, 1_000_00, ac.AToBCents)
	assert.EqualValues(t, 1_000_00, ac.BToACents)
	require.Len(t, saldosDaPrimeira, 3, "as três contas que aparecem em pairs")
}

func minMax(a, b string) string {
	if b < a {
		a, b = b, a
	}
	return a + "|" + b
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func TestBalanceAtMonthEndBateComGetAccountsEDivergeComLancamentoNoMesSeguinte(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	contaB := a.contaItau(t)

	// Uma importação real de agosto: transferência A→B e uma despesa comum.
	extrato := csvExtratoAPI(
		linhaExtrato{5, "-1500.00", "01", "Transferência enviada pelo Pix - Itau Corrente"},
		linhaExtrato{6, "-11.00", "02", "Transferência enviada pelo Pix - Padaria Exemplo"},
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extrato))
	linha := linhaPorStatus(t, revisarPelaAPI(t, a, lote.ID).Items, dedup.StatusInternalTransfer)
	resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"transfer"}]}`, linha.ID)))

	saldosAgosto := contasPelaAPI(t, a)
	require.Equal(t, int64(-1511_00), saldosAgosto[contaA.ID])
	require.Equal(t, int64(1500_00), saldosAgosto[contaB.ID])

	// Agosto é o último mês com lançamento: o saldo no fim de agosto É o
	// saldo corrente de GET /accounts, para as duas contas.
	view := transfersDecodificadas(t, transfersPelaAPI(t, a, a.casa.ID, a.usuario.ID, "month=2026-08&accountId="+contaA.ID+"&counterpartAccountId="+contaB.ID))
	require.Len(t, view.Balances, 2)
	for _, b := range view.Balances {
		assert.Equal(t, saldosAgosto[b.AccountID], b.BalanceAtMonthEndCents, "conta %s", b.AccountName)
	}

	// Um lançamento datado em SETEMBRO em A.
	_, err := a.txSvc.CreateBatch(t.Context(), a.atorDeLancamento(a.casa.ID, a.usuario.ID), transaction.CreateBatchInput{
		Source: transaction.SourceManual,
		Rows: []transaction.NewTransaction{{
			Kind: transaction.KindExpense, AccountID: contaA.ID, AmountCents: 42_00, Description: "Setembro",
			OccurredOn: civil.MustNew(2026, 9, 1),
			DedupKey:   dedup.DerivedKey(contaA.ID, transaction.KindExpense, civil.MustNew(2026, 9, 1), 42_00, "setembro"),
		}},
	})
	require.NoError(t, err)

	saldosSetembro := contasPelaAPI(t, a)
	assert.Equal(t, saldosAgosto[contaA.ID]-42_00, saldosSetembro[contaA.ID], "GET /accounts é o saldo corrente")
	assert.Equal(t, saldosAgosto[contaB.ID], saldosSetembro[contaB.ID])

	// O fim de AGOSTO não muda: diverge do corrente exatamente pelos 42,00.
	agosto := transfersDecodificadas(t, transfersPelaAPI(t, a, a.casa.ID, a.usuario.ID, "month=2026-08&accountId="+contaA.ID))
	require.Len(t, agosto.Balances, 1)
	assert.Equal(t, saldosAgosto[contaA.ID], agosto.Balances[0].BalanceAtMonthEndCents)
	assert.Equal(t, int64(42_00), agosto.Balances[0].BalanceAtMonthEndCents-saldosSetembro[contaA.ID])

	// E o fim de SETEMBRO volta a bater com o corrente — mesmo sem
	// transferência nenhuma em setembro (o filtro por conta traz o saldo).
	setembro := transfersDecodificadas(t, transfersPelaAPI(t, a, a.casa.ID, a.usuario.ID, "month=2026-09&accountId="+contaA.ID))
	assert.Empty(t, setembro.Items)
	assert.Empty(t, setembro.Pairs)
	require.Len(t, setembro.Balances, 1)
	assert.Equal(t, saldosSetembro[contaA.ID], setembro.Balances[0].BalanceAtMonthEndCents)

	// Fevereiro bissexto: o fim do mês é calculado, não "dia 30".
	fevereiro := transfersDecodificadas(t, transfersPelaAPI(t, a, a.casa.ID, a.usuario.ID, "month=2028-02&accountId="+contaA.ID))
	require.Len(t, fevereiro.Balances, 1)
	assert.Equal(t, saldosSetembro[contaA.ID], fevereiro.Balances[0].BalanceAtMonthEndCents, "até 29/02/2028 tudo já aconteceu")
	julho := transfersDecodificadas(t, transfersPelaAPI(t, a, a.casa.ID, a.usuario.ID, "month=2026-07&accountId="+contaA.ID))
	require.Len(t, julho.Balances, 1)
	assert.Zero(t, julho.Balances[0].BalanceAtMonthEndCents, "antes de qualquer lançamento: só a abertura (zero)")
}
