package report_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Os testes de handler provam o CONTRATO de GET /reports/by-category: status,
// código, campos em `fields`, forma exata do JSON (nulos e listas vazias) e —
// o mais importante — nada sensível no log.

type httpAmbiente struct {
	*ambiente
	handler *report.Handler
}

func novoHTTPAmbiente(t *testing.T) *httpAmbiente {
	t.Helper()
	amb := novoAmbiente(t)
	lg := logging.New(amb.logs, logging.Options{Level: "debug", Format: "json"})
	return &httpAmbiente{ambiente: amb, handler: report.NewHandler(amb.svc, lg)}
}

// chamar monta a requisição já com a identidade no contexto — que é o que o
// RequireAuth faz em produção. A casa NUNCA vem da URL.
func (a *httpAmbiente) chamar(t *testing.T, casa, alvo string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, alvo, nil)
	if casa != "" {
		r = r.WithContext(session.NewContext(r.Context(), session.Identity{
			UserID: usuario, HouseholdID: casa, Role: "owner", SessionID: "sess-1",
		}))
	}
	rec := httptest.NewRecorder()
	a.handler.ByCategory(rec, r)
	return rec
}

func corpoDeErro(t *testing.T, rec *httptest.ResponseRecorder) (codigo string, campos map[string]string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Fields  map[string]string `json:"fields"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), "corpo: %s", rec.Body.String())
	return env.Error.Code, env.Error.Fields
}

// Critério 11: sem sessão é 401 e nada é consultado.
func TestHandlerSemSessaoEh401(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)

	rec := a.chamar(t, "", "/api/v1/reports/by-category?month=2026-09")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	codigo, _ := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeUnauthenticated, codigo)
	assert.Empty(t, a.ledger.chamadas)
}

// Critério 10: cada forma errada de `month` é 400 em fields.month, com a
// MESMA mensagem — o cliente não aprende nada sobre o servidor pela recusa.
func TestHandlerMesInvalidoEh400(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"ausente":         "/api/v1/reports/by-category",
		"vazio":           "/api/v1/reports/by-category?month=",
		"mês 13":          "/api/v1/reports/by-category?month=2026-13",
		"sem zero":        "/api/v1/reports/by-category?month=2026-1",
		"com dia":         "/api/v1/reports/by-category?month=2026-09-01",
		"espaço à frente": "/api/v1/reports/by-category?month=%202026-09",
		"NUL no fim":      "/api/v1/reports/by-category?month=2026-09%00",
		"barra":           "/api/v1/reports/by-category?month=2026%2F09",
		"repetido":        "/api/v1/reports/by-category?month=2026-13&month=2026-09",
	}
	var mensagens []string
	for nome, alvo := range casos {
		t.Run(nome, func(t *testing.T) {
			a := novoHTTPAmbiente(t)
			rec := a.chamar(t, minhaCasa, alvo)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			assert.Contains(t, campos, "month")
			assert.NotContains(t, campos, "kind")
			assert.Empty(t, a.ledger.chamadas, "mês inválido não chega ao banco")
			mensagens = append(mensagens, rec.Body.String())
		})
	}
	for i := 1; i < len(mensagens); i++ {
		assert.Equal(t, mensagens[0], mensagens[i], "recusas byte a byte iguais")
	}
}

// Critério 10: `kind` fora da allowlist é 400 em fields.kind; ausente ou
// vazio é expense.
func TestHandlerKindInvalidoEh400(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"transfer_out", "transfer_in", "EXPENSE", "expense,income", "'; DROP TABLE transactions;--", "expense ", " income", "expense\x00"} {
		t.Run(kind, func(t *testing.T) {
			a := novoHTTPAmbiente(t)
			q := url.Values{"month": {"2026-09"}, "kind": {kind}}
			rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?"+q.Encode())
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			assert.Contains(t, campos, "kind")
			assert.NotContains(t, campos, "month")
			assert.Empty(t, a.ledger.chamadas, "kind inválido não chega ao banco")
			// A recusa não ecoa o valor enviado.
			assert.NotContains(t, rec.Body.String(), "DROP")
		})
	}

	for nome, alvo := range map[string]string{
		"ausente": "/api/v1/reports/by-category?month=2026-09",
		"vazio":   "/api/v1/reports/by-category?month=2026-09&kind=",
	} {
		t.Run("kind "+nome, func(t *testing.T) {
			a := novoHTTPAmbiente(t)
			rec := a.chamar(t, minhaCasa, alvo)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var v report.CategoryReportView
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
			assert.Equal(t, "expense", v.Kind)
			require.Len(t, a.ledger.chamadas, 1)
			assert.Equal(t, "expense", a.ledger.chamadas[0].kind)
		})
	}

	t.Run("kind income", func(t *testing.T) {
		a := novoHTTPAmbiente(t)
		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09&kind=income")
		require.Equal(t, http.StatusOK, rec.Code)
		require.Len(t, a.ledger.chamadas, 1)
		assert.Equal(t, "income", a.ledger.chamadas[0].kind)
	})
}

// Mês e kind inválidos ao mesmo tempo: o mês é conferido primeiro, e só ele
// aparece — um campo por recusa, sem acumular.
func TestHandlerMesVemAntesDoKind(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=x&kind=y")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	_, campos := corpoDeErro(t, rec)
	assert.Contains(t, campos, "month")
	assert.NotContains(t, campos, "kind")
}

// A forma exata do JSON: `categoryId: null` e `name: null` no balde,
// `children: []` e `items: []` nunca `null`, `archivedAt: null` quando não
// arquivada, e nenhum campo além dos do contrato.
func TestHandlerFormaDoJSON(t *testing.T) {
	t.Parallel()

	t.Run("mês vazio", func(t *testing.T) {
		a := novoHTTPAmbiente(t)
		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"month":"2026-09","kind":"expense","totalCents":0,"count":0,"items":[]}`, rec.Body.String())
	})

	t.Run("com balde e grupo sem filha", func(t *testing.T) {
		a := novoHTTPAmbiente(t)
		g := a.categoria(minhaCasa, "11111111-1111-7111-8111-000000000001", "Casa", category.KindExpense, nil, false)
		a.ledger.rows = []report.CategoryTotal{linha(ptr(g.ID), 3_000, 1), linha(nil, 1_000, 2)}

		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{
			"month":"2026-09","kind":"expense","totalCents":4000,"count":3,
			"items":[
				{"categoryId":"11111111-1111-7111-8111-000000000001","name":"Casa","archivedAt":null,
				 "totalCents":3000,"count":1,"shareBp":7500,
				 "directCents":3000,"directCount":1,"directShareBp":7500,"children":[]},
				{"categoryId":null,"name":null,"archivedAt":null,
				 "totalCents":1000,"count":2,"shareBp":2500,
				 "directCents":1000,"directCount":2,"directShareBp":2500,"children":[]}
			]}`, rec.Body.String())

		// Chaves EXATAS do contrato — nada a mais (docs/SEGURANCA.md §4:
		// DTO dedicado, nunca a entidade).
		var bruto map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &bruto))
		assert.ElementsMatch(t, []string{"month", "kind", "totalCents", "count", "items"}, chaves(bruto))
		item := bruto["items"].([]any)[0].(map[string]any)
		assert.ElementsMatch(t, []string{"categoryId", "name", "archivedAt", "totalCents", "count", "shareBp",
			"directCents", "directCount", "directShareBp", "children"}, chaves(item))
	})

	t.Run("filha arquivada", func(t *testing.T) {
		a := novoHTTPAmbiente(t)
		g := a.categoria(minhaCasa, "g", "Casa", category.KindExpense, nil, false)
		f := a.categoria(minhaCasa, "f", "Luz", category.KindExpense, ptr(g.ID), true)
		a.ledger.rows = []report.CategoryTotal{linha(ptr(f.ID), 3_000, 1)}

		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09")
		require.Equal(t, http.StatusOK, rec.Code)
		var bruto map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &bruto))
		filha := bruto["items"].([]any)[0].(map[string]any)["children"].([]any)[0].(map[string]any)
		assert.ElementsMatch(t, []string{"categoryId", "name", "archivedAt", "totalCents", "count", "shareBp"}, chaves(filha))
		assert.Equal(t, "2026-08-01T10:30:00Z", filha["archivedAt"])
	})
}

func chaves(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Parâmetros desconhecidos são ignorados sem efeito: a casa é a do token,
// aconteça o que acontecer na query.
func TestHandlerIgnoraParametrosDesconhecidos(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)

	q := url.Values{}
	q.Set("month", "2026-09")
	q.Set("householdId", outraCasa)
	q.Set("household_id", outraCasa)
	q.Set("categoryId", "qualquer")
	q.Set("limit", "1")
	q.Set("order", "amount_cents; DROP TABLE transactions")
	rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?"+q.Encode())

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, a.ledger.chamadas, 1)
	assert.Equal(t, minhaCasa, a.ledger.chamadas[0].casa, "a casa é SEMPRE a do token")
	assert.Equal(t, "2026-09", a.ledger.chamadas[0].mes)
}

// Critério 13 e 14: falha de infraestrutura é 500 genérico; o log leva
// request_id, operação e razão — e NUNCA centavos, nomes ou a query.
func TestHandlerErroInternoEhGenericoESemDadoNoLog(t *testing.T) {
	t.Parallel()

	t.Run("ledger falha", func(t *testing.T) {
		a := novoHTTPAmbiente(t)
		a.ledger.err = errors.New("dial tcp: connection refused (senha=abc)")

		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		codigo, campos := corpoDeErro(t, rec)
		assert.Equal(t, httpserver.CodeInternalError, codigo)
		assert.Empty(t, campos)
		assert.Equal(t, `{"error":{"code":"INTERNAL_ERROR","message":"Erro interno."}}`, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "connection refused", "detalhe interno nunca sai na resposta")

		log := a.logs.String()
		assert.Contains(t, log, `"level":"ERROR"`)
		assert.Contains(t, log, `"request_id"`)
		assert.Contains(t, log, `"operacao":"relatório por categoria"`)
		assert.Contains(t, log, `"reason"`)
		assert.Contains(t, log, "somando lançamentos por categoria")
	})

	t.Run("linhas demais", func(t *testing.T) {
		a := novoHTTPAmbiente(t)
		rows := make([]report.CategoryTotal, 0, category.MaxPerHousehold+2)
		for i := 0; i < category.MaxPerHousehold+2; i++ {
			id := strings.Repeat("a", 30) + string(rune('0'+i%10)) + string(rune('0'+(i/10)%10)) + string(rune('0'+(i/100)%10))
			rows = append(rows, linha(ptr(id), 987_654_321, 1))
		}
		a.ledger.rows = rows

		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.Equal(t, `{"error":{"code":"INTERNAL_ERROR","message":"Erro interno."}}`, rec.Body.String())

		log := a.logs.String()
		assert.Contains(t, log, `"level":"ERROR"`)
		assert.Contains(t, log, "202 linhas", "só a contagem")
		assert.NotContains(t, log, "987654321", "nunca centavos")
		assert.NotContains(t, log, strings.Repeat("a", 30), "nem os ids das linhas")
	})
}

// Critério 14 no caminho feliz e no aviso: o log de uma requisição normal não
// tem centavos nem nome de categoria; o aviso de anomalia leva só o id.
func TestHandlerLogSemCentavosNemNomes(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)

	g := a.categoria(minhaCasa, "g-1", "Supermercado Preferido", category.KindExpense, nil, false)
	a.ledger.rows = []report.CategoryTotal{
		linha(ptr(g.ID), 123_456, 7),
		linha(ptr("id-que-nao-e-da-casa"), 654_321, 1),
	}

	rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09")
	require.Equal(t, http.StatusOK, rec.Code)

	log := a.logs.String()
	assert.Contains(t, log, "id-que-nao-e-da-casa", "o aviso leva o id")
	assert.NotContains(t, log, "123456")
	assert.NotContains(t, log, "654321")
	assert.NotContains(t, log, "Supermercado")
	assert.NotContains(t, log, "month=", "a query não vai para o log")
	assert.NotContains(t, log, `"level":"ERROR"`)
}

// Nenhum panic em caminho de requisição, mesmo com dublês devolvendo o pior
// caso: nil rows, categorias nil, ids vazios.
func TestHandlerNaoEntraEmPanico(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	a.ledger.rows = []report.CategoryTotal{linha(ptr(""), 1, 1), linha(nil, 0, 0)}

	assert.NotPanics(t, func() {
		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09")
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})
}

// Logger nulo no handler cai no padrão — sem panic.
func TestNewHandlerAceitaLoggerNulo(t *testing.T) {
	t.Parallel()
	amb := novoAmbiente(t)
	h := report.NewHandler(amb.svc, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/reports/by-category?month=2026-09", nil)
	rec := httptest.NewRecorder()
	assert.NotPanics(t, func() { h.ByCategory(rec, r) })
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
