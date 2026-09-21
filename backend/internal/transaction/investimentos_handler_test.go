package transaction_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-029(j.2): ErrTooManyCategories mapeia para 500 GENÉRICO, nunca 4xx. O
// nome convida ao engano por analogia com ErrTooManyUncategorized (422 em
// `fields.month`), mas aquele conta LANÇAMENTOS — que a pessoa pode dividir em
// dois meses — e este conta CATEGORIAS DA PRÓPRIA CASA, que já têm teto
// próprio. Passar de 200 significa taxonomia violada: falha FECHADA.
func TestListHandlerComTaxonomiaEstouradaEh500Generico(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	for i := 0; i <= category.MaxPerHousehold; i++ {
		amb.categoria(minhaCasa, fmt.Sprintf("cat-%03d", i), "Aporte", category.KindInvestment)
	}

	rec := amb.chamar(t, minhaCasa, http.MethodGet, "/transactions?month=2026-09", "", amb.handler.List)
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeInternalError, codigo)
	assert.Empty(t, campos, "um 422 apontaria um campo que a pessoa não tem como corrigir neste pedido")

	// O log leva a CONTAGEM, e nada mais: nem os ids das categorias, nem
	// centavos, nem a descrição de lançamento nenhum (S8).
	linhas := amb.logs.String()
	assert.Contains(t, linhas, "categorias marcadas")
	assert.NotContains(t, linhas, "cat-001")
}

// ADR-029(j.1): resumo com parcela impossível também é 500 genérico, e o log
// leva só contagens — clampar em silêncio esconderia a corrupção e ainda
// publicaria um total errado.
func TestListHandlerComResumoImpossivelEh500EComContagensNoLog(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.repo.resumoForcado = &transaction.Summary{
		IncomeCents: 10, ExpenseCents: -1, InvestedCents: 2_000_00, Count: 7, Uncategorized: 2,
	}

	rec := amb.chamar(t, minhaCasa, http.MethodGet, "/transactions?month=2026-09", "", amb.handler.List)
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeInternalError, codigo)
	assert.Empty(t, campos)

	linhas := amb.logs.String()
	assert.Contains(t, linhas, "lancamentos=7")
	assert.Contains(t, linhas, "sem_categoria=2")
	// Nenhum valor em centavos vaza pelo log — o que interessa ao diagnóstico
	// é quantas linhas havia, nunca quanto dinheiro era.
	assert.NotContains(t, linhas, "200000")
	assert.NotContains(t, linhas, "2000,00")
}

// E o caminho feliz pelo HTTP: os dois campos novos aparecem no JSON, com os
// marcados fora de income/expense/net.
func TestListHandlerPublicaOsDoisCamposNovos(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	cdb := amb.categoria(minhaCasa, "cat-cdb", "CDB", category.KindInvestment)
	mercado := amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 2_000_00, Description: "CDB 15 DIAS", CategoryID: ptr(cdb.ID),
		OccurredOn: civil.MustNew(2026, 9, 10), CompetenceMonth: "2026-09",
	})
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 300_00, Description: "Mercado", CategoryID: ptr(mercado.ID),
		OccurredOn: civil.MustNew(2026, 9, 11), CompetenceMonth: "2026-09",
	})

	rec := amb.chamar(t, minhaCasa, http.MethodGet, "/transactions?month=2026-09", "", amb.handler.List)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var corpo struct {
		Items   []map[string]any `json:"items"`
		Summary map[string]any   `json:"summary"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
	assert.Equal(t, float64(300_00), corpo.Summary["expenseCents"])
	assert.Equal(t, float64(2_000_00), corpo.Summary["investedCents"])
	assert.Equal(t, float64(0), corpo.Summary["redeemedCents"])
	assert.Len(t, corpo.Items, 2, "o aporte continua na lista: ele existe e saiu da conta")

	// Nem a descrição do aporte nem o valor entram em log num caminho feliz.
	assert.False(t, strings.Contains(amb.logs.String(), "CDB 15 DIAS"))
}
