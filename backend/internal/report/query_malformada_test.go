package report_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Query malformada é 400 na CADEIA — os casos de ponta a ponta, com os
// parâmetros reais de GET /reports/by-category.
//
// O defeito: `r.URL.Query()` descarta o erro de `url.ParseQuery`, e desde o Go
// 1.17 esse parser PULA o par que não consegue ler. `?accountGroup=credit;debit`
// chegava à borda como chave AUSENTE, que o ADR-032 lê como "sem recorte" — a
// resposta virava o mês de TODAS as contas, com 200. A regra escrita do ADR-032
// ("valor fora da allowlist é 400 na borda, nunca 'sem filtro'") não alcançava
// o caso porque a borda recebia a chave ausente.
//
// A mecânica do middleware está em
// internal/platform/httpserver/wellformedquery_test.go; o que se mede aqui é a
// ROTA atrás dele.

// pelaCadeia chama o handler real com a guarda da cadeia na frente — a mesma
// composição de cmd/api/main.go, reduzida ao que este teste mede.
func (a *httpAmbiente) pelaCadeia(t *testing.T, casa, alvo string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, "/api/v1/reports/by-category", nil)
	// RawQuery direto: é assim que a query chega EXATAMENTE como o cliente a
	// escreveu, sem nenhuma reescrita do httptest.
	if i := indexDaQuery(alvo); i >= 0 {
		r.URL.RawQuery = alvo[i+1:]
	}
	if casa != "" {
		r = r.WithContext(session.NewContext(r.Context(), session.Identity{
			UserID: usuario, HouseholdID: casa, Role: "owner", SessionID: "sess-1",
		}))
	}
	rec := httptest.NewRecorder()
	httpserver.WellFormedQuery()(http.HandlerFunc(a.handler.ByCategory)).ServeHTTP(rec, r)
	return rec
}

func indexDaQuery(alvo string) int {
	for i := range len(alvo) {
		if alvo[i] == '?' {
			return i
		}
	}
	return -1
}

// Critérios 1, 2 e 3: as três formas de query malformada que hoje respondiam
// 200 com o recorte EVAPORADO agora param em 400.
func TestQueryMalformadaEmByCategoryEh400(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		// 1: hoje 200 com TODAS as contas.
		"ponto e vírgula cru no valor": "?month=2026-09&accountGroup=credit;debit",
		// 2: hoje 200 com accountGroup=credit — o HPP nem dispara.
		"segunda ocorrência quebrada": "?month=2026-09&accountGroup=credit&accountGroup=debit;",
		// 3: escape percentual inválido e percent solto.
		"escape percentual inválido": "?month=2026-09&accountGroup=cred%zzit",
		"percent solto no fim":       "?month=2026-09&accountGroup=credit%",
		"chave quebrada":             "?month=2026-09&account%Group=credit",
	}
	for nome, query := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			a := novoHTTPAmbiente(t)
			a.conta(minhaCasa, "cartao", "Cartão", account.KindCreditCard, false)
			a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)
			a.ledger.rows = []report.CategoryAccountTotal{
				linhaConta(nil, "cartao", 1_000, 1),
				linhaConta(nil, "corrente", 2_000, 1),
			}

			rec := a.pelaCadeia(t, minhaCasa, query)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

			// Critério 7: o corpo não contém o separador, o escape, o valor
			// recebido nem o texto do erro da stdlib.
			corpo := rec.Body.String()
			assert.NotContains(t, corpo, ";")
			assert.NotContains(t, corpo, "%zz")
			assert.NotContains(t, corpo, "credit")
			assert.NotContains(t, corpo, "invalid")
			assert.NotContains(t, corpo, "semicolon")
			assert.NotContains(t, corpo, "escape")

			var env httpserver.ErrorEnvelope
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
			assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
			assert.Nil(t, env.Error.Fields, "não há campo a apontar: a query inteira é ilegível")

			// Nada foi consultado, e a query não foi para o log.
			assert.Empty(t, a.ledger.chamadas, "a requisição parou antes do serviço")
			assert.NotContains(t, a.logs.String(), ";")
			assert.NotContains(t, a.logs.String(), "accountGroup")
		})
	}
}

// Critério 4: a guarda NÃO rouba o caso legítimo. `%3B` é o ponto e vírgula
// ESCAPADO — query bem formada cujo valor é a string "credit;debit" —, e ela
// segue o caminho normal da allowlist: 400 em `fields.accountGroup` com a
// redação do parâmetro.
//
// É a diferença entre "não consigo ler o que você mandou" (sem campo) e "li e
// não aceito" (com o campo e a instrução).
func TestPontoEVirgulaEscapadoContinuaCaindoNaAllowlistDoParametro(t *testing.T) {
	t.Parallel()

	a := novoHTTPAmbiente(t)
	rec := a.pelaCadeia(t, minhaCasa, "?month=2026-09&accountGroup=credit%3Bdebit")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	var env httpserver.ErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
	require.Contains(t, env.Error.Fields, "accountGroup")
	assert.Equal(t, "Informe credit ou debit.", env.Error.Fields["accountGroup"])
}

// Critério 8: requisição sem query e com query válida seguem idênticas à
// resposta sem a guarda na frente.
func TestQueryValidaOuAusenteAtravessaAGuardaSemMudarNada(t *testing.T) {
	t.Parallel()

	for nome, query := range map[string]string{
		"sem query":     "",
		"query válida":  "?month=2026-09",
		"com o recorte": "?month=2026-09&accountGroup=credit",
	} {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			a := novoHTTPAmbiente(t)
			a.conta(minhaCasa, "cartao", "Cartão", account.KindCreditCard, false)
			a.ledger.rows = []report.CategoryAccountTotal{linhaConta(nil, "cartao", 1_000, 1)}

			comGuarda := a.pelaCadeia(t, minhaCasa, query)
			semGuarda := a.chamar(t, minhaCasa, "/api/v1/reports/by-category"+query)

			assert.Equal(t, semGuarda.Code, comGuarda.Code)
			assert.Equal(t, semGuarda.Body.String(), comGuarda.Body.String())
		})
	}
}
