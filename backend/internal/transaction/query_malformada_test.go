package transaction_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Critério 5 da correção de query malformada: em GET /transactions,
// `?kindGroup=expense;` respondia 200 LISTANDO TUDO.
//
// O caminho do defeito: `url.ParseQuery` pula o par com `;` cru, `kindGroup`
// chega VAZIO à borda, e vazio é o valor legítimo de "Tudo" (spec 0004 §12) —
// então o recorte evapora e a tela mostra receitas, transferências e
// investimentos sob o rótulo "Despesas". A guarda da cadeia transforma isso em
// 400 antes do mux.

func (a *httpAmbiente) pelaCadeiaComQuery(t *testing.T, casa, caminho, query string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, caminho, nil)
	// RawQuery direto: a query chega exatamente como o cliente a escreveu.
	r.URL.RawQuery = query
	if casa != "" {
		r = comIdentidade(r, casa)
	}
	rec := httptest.NewRecorder()
	httpserver.WellFormedQuery()(h).ServeHTTP(rec, r)
	return rec
}

func TestQueryMalformadaEmTransactionsEh400EmVezDeListarTudo(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"; no fim do kindGroup":      "month=2026-09&kindGroup=expense;",
		"; dentro do kindGroup":      "month=2026-09&kindGroup=expense;income",
		"escape percentual inválido": "month=2026-09&kindGroup=exp%zzense",
		"; no accountId":             "month=2026-09&accountId=" + idContaCanonica + ";",
	}
	for nome, query := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			amb := novoHTTPAmbiente(t)
			amb.conta(minhaCasa, idContaCanonica, "Conta", account.KindChecking)
			amb.repo.semear(transaction.Transaction{
				HouseholdID: minhaCasa, AccountID: idContaCanonica, Kind: transaction.KindIncome,
				AmountCents: 900_00, Description: "Salário", OccurredOn: civil.MustNew(2026, 9, 5),
				CompetenceMonth: "2026-09",
			})

			rec := amb.pelaCadeiaComQuery(t, minhaCasa, "/transactions", query, amb.handler.List)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

			corpo := rec.Body.String()
			assert.NotContains(t, corpo, ";", "o separador recebido não volta na resposta")
			assert.NotContains(t, corpo, "%zz")
			assert.NotContains(t, corpo, "expense")
			assert.NotContains(t, corpo, "Salário", "e nada do dado da casa vaza por um erro de forma")

			var env httpserver.ErrorEnvelope
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
			assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
			assert.Nil(t, env.Error.Fields)

			assert.NotContains(t, amb.logs.String(), ";", "a query nunca entra em log")
			assert.NotContains(t, amb.logs.String(), "kindGroup")
		})
	}
}

// E a query bem formada continua atravessando: o recorte legítimo responde o
// que sempre respondeu.
func TestQueryBemFormadaEmTransactionsAtravessaAGuarda(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, idContaCanonica, "Conta", account.KindChecking)
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: idContaCanonica, Kind: transaction.KindIncome,
		AmountCents: 900_00, Description: "Salário", OccurredOn: civil.MustNew(2026, 9, 5),
		CompetenceMonth: "2026-09",
	})

	comGuarda := amb.pelaCadeiaComQuery(t, minhaCasa, "/transactions", "month=2026-09&kindGroup=income", amb.handler.List)
	require.Equal(t, http.StatusOK, comGuarda.Code, comGuarda.Body.String())

	semGuarda := amb.chamar(t, minhaCasa, http.MethodGet, "/transactions?month=2026-09&kindGroup=income", "", amb.handler.List)
	assert.Equal(t, semGuarda.Code, comGuarda.Code)
	assert.JSONEq(t, semGuarda.Body.String(), comGuarda.Body.String())
}
