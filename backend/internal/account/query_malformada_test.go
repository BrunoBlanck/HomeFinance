package account_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Critério 6 da correção de query malformada: em GET /accounts,
// `?includeArchived=true;` respondia 200 com a lista SEM as arquivadas — e a
// tela afirmando o contrário.
//
// O caminho do defeito: `url.ParseQuery` pula o par com `;` cru, `boolQuery`
// recebe a chave AUSENTE, ausente é `false` por contrato, e o resultado é uma
// lista que contradiz o que o cliente pediu sem erro nenhum. É o caso mais
// enganoso da classe, porque a resposta é 200 e parece certa.

func (a *ambiente) pelaCadeiaComQuery(t *testing.T, casa, caminho, query string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodGet, caminho, nil)
	// RawQuery direto: a query chega exatamente como o cliente a escreveu.
	r.URL.RawQuery = query
	r = r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID: "user-1", HouseholdID: casa, Role: "owner", SessionID: "sess-1",
	}))
	rec := httptest.NewRecorder()
	httpserver.WellFormedQuery()(h).ServeHTTP(rec, r)
	return rec
}

func TestQueryMalformadaEmAccountsEh400EmVezDeMentirSobreOFiltro(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"; no fim do booleano":       "includeArchived=true;",
		"escape percentual inválido": "includeArchived=tr%zzue",
		"percent solto":              "includeArchived=true%",
	}
	for nome, query := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			amb := novoAmbiente(t)
			rec := amb.pelaCadeiaComQuery(t, minhaCasa, "/accounts", query, amb.handler.List)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

			corpo := rec.Body.String()
			assert.NotContains(t, corpo, ";")
			assert.NotContains(t, corpo, "%zz")
			assert.NotContains(t, corpo, "includeArchived")

			var env httpserver.ErrorEnvelope
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
			assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
			assert.Nil(t, env.Error.Fields)
		})
	}
}

// E o filtro bem formado continua valendo, inclusive trazendo a arquivada.
func TestQueryBemFormadaEmAccountsAtravessaAGuarda(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	rec := amb.pelaCadeiaComQuery(t, minhaCasa, "/accounts", "includeArchived=true", amb.handler.List)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	semGuarda := amb.chamar(t, minhaCasa, http.MethodGet, "/accounts?includeArchived=true", "",
		amb.handler.List, "")
	assert.Equal(t, semGuarda.Code, rec.Code)
	assert.JSONEq(t, semGuarda.Body.String(), rec.Body.String())

	// E o valor NÃO booleano continua sendo 400 no campo — a guarda não roubou
	// a recusa da borda (account.boolQuery).
	invalido := amb.pelaCadeiaComQuery(t, minhaCasa, "/accounts", "includeArchived=sim", amb.handler.List)
	require.Equal(t, http.StatusBadRequest, invalido.Code)
	var env httpserver.ErrorEnvelope
	require.NoError(t, json.Unmarshal(invalido.Body.Bytes(), &env))
	assert.Contains(t, env.Error.Fields, "includeArchived")
}
