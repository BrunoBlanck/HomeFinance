package investment_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Os testes deste arquivo provam o CONTRATO das duas rotas: status, código,
// campos em `fields`, forma exata do JSON (nulos, listas vazias, enum) e —
// tão importante quanto — o que NÃO aparece no log.

type httpCenario struct {
	*cenario
	handler *investment.Handler
	logs    *bytes.Buffer
}

func montarHTTP(t *testing.T) *httpCenario {
	t.Helper()
	logs := &bytes.Buffer{}
	lg := logging.New(logs, logging.Options{Level: "debug", Format: "json"})

	// Serviço e handler compartilham o logger: o aviso agregado do serviço e o
	// erro da borda caem no mesmo buffer, e o teste consegue afirmar o que
	// NÃO está lá.
	c := montarCom(lg)
	h := investment.NewHandler(c.svc, lg, 0)
	return &httpCenario{cenario: c, handler: h, logs: logs}
}

// comSessao devolve a requisição já com a identidade no contexto — que é o que
// o RequireAuth faz em produção. A casa NUNCA vem da URL nem do corpo.
func comSessao(r *http.Request, casa string) *http.Request {
	if casa == "" {
		return r
	}
	return r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID: "user-1", HouseholdID: casa, Role: "owner", SessionID: "sess-1",
	}))
}

func (h *httpCenario) get(t *testing.T, casa, alvo string) *httptest.ResponseRecorder {
	t.Helper()
	r := comSessao(httptest.NewRequest(http.MethodGet, alvo, nil), casa)
	rec := httptest.NewRecorder()
	h.handler.Overview(rec, r)
	return rec
}

func (h *httpCenario) post(t *testing.T, casa, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	r := comSessao(httptest.NewRequest(http.MethodPost, "/api/v1/investments/detect", strings.NewReader(corpo)), casa)
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handler.Detect(rec, r)
	return rec
}

func erroDoCorpo(t *testing.T, rec *httptest.ResponseRecorder) (codigo string, campos map[string]string) {
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

func TestSemSessaoAsDuasRotasSao401(t *testing.T) {
	t.Parallel()

	h := montarHTTP(t)

	rec := h.get(t, "", "/api/v1/investments?month=2026-09")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Zero(t, h.categorias.chamadas, "consultou alguma coisa sem sessão")

	rec = h.post(t, "", `{"month":"2026-09","dryRun":true}`)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Zero(t, h.ledger.chamadasListDoMes)
}

// Aceite 12: mês malformado é 400 em `fields.month`; `limit` acima do teto é
// 400 em `fields.limit`; cursor adulterado é 400 SEM detalhe.
func TestAbusoNaQueryDoOverview(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome  string
		alvo  string
		campo string
	}{
		{"mês ausente", "/api/v1/investments", "month"},
		{"mês malformado", "/api/v1/investments?month=2026-13", "month"},
		{"mês com espaço", "/api/v1/investments?month=%202026-01", "month"},
		{"limit acima do teto", "/api/v1/investments?month=2026-09&limit=101", "limit"},
		{"limit zero", "/api/v1/investments?month=2026-09&limit=0", "limit"},
		{"limit negativo", "/api/v1/investments?month=2026-09&limit=-1", "limit"},
		{"limit não numérico", "/api/v1/investments?month=2026-09&limit=abc", "limit"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			h := montarHTTP(t)
			rec := h.get(t, casaA, c.alvo)
			require.Equal(t, http.StatusBadRequest, rec.Code, "corpo: %s", rec.Body.String())
			codigo, campos := erroDoCorpo(t, rec)
			assert.Equal(t, "VALIDATION_FAILED", codigo)
			assert.Contains(t, campos, c.campo)
		})
	}

	t.Run("cursor adulterado não explica o porquê", func(t *testing.T) {
		t.Parallel()
		h := montarHTTP(t)
		h.categorias.juntar(casaA, "cat-inv", "Renda fixa", category.KindInvestment)
		rec := h.get(t, casaA, "/api/v1/investments?month=2026-09&cursor=..zzz")
		require.Equal(t, http.StatusBadRequest, rec.Code)
		codigo, campos := erroDoCorpo(t, rec)
		assert.Equal(t, "VALIDATION_FAILED", codigo)
		assert.Empty(t, campos, "o cursor é opaco: explicar a recusa é ensinar a forjá-lo")
	})
}

// Parâmetro desconhecido na query é IGNORADO sem efeito — em especial
// `householdId`, que nunca substitui a casa do token.
func TestParametroDesconhecidoNaoTemEfeito(t *testing.T) {
	t.Parallel()

	h := montarHTTP(t)
	h.categorias.juntar(casaA, "cat-inv", "Renda fixa", category.KindInvestment)
	h.categorias.juntar(casaB, "cat-b", "Renda fixa B", category.KindInvestment)
	h.contas.juntar(casaA, "acc-1", "Nubank")
	h.contas.juntar(casaB, "acc-1", "Itaú")
	h.ledger.juntar(lanc(casaB, uuidDe(1), transaction.KindExpense, "CDB", 999999, 1, "2026-09", "cat-b"))

	rec := h.get(t, casaA, "/api/v1/investments?month=2026-09&householdId="+casaB+"&pageSize=200")
	require.Equal(t, http.StatusOK, rec.Code)

	var view investment.OverviewView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	assert.Empty(t, view.Items, "householdId da query mudou a casa consultada")
	assert.Equal(t, casaA, h.ledger.ultimaCasaList)
}

// A forma do JSON é o contrato: `series` com 12 itens, `items` como `[]` e
// `nextCursor` presente e nulo.
func TestFormaDoJSONDoOverview(t *testing.T) {
	t.Parallel()

	h := montarHTTP(t)
	rec := h.get(t, casaA, "/api/v1/investments?month=2026-09")
	require.Equal(t, http.StatusOK, rec.Code)

	var bruto map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &bruto))
	for _, campo := range []string{"month", "monthly", "yearToDate", "series", "items", "nextCursor"} {
		assert.Contains(t, bruto, campo, "campo obrigatório do contrato ausente")
	}
	assert.JSONEq(t, `null`, string(bruto["nextCursor"]))
	assert.JSONEq(t, `[]`, string(bruto["items"]), "items tem de ser [] e nunca null")

	var serie []investment.SeriesPointView
	require.NoError(t, json.Unmarshal(bruto["series"], &serie))
	assert.Len(t, serie, 12)
}

// `dryRun` ausente é 400 em `fields.dryRun`: o zero value de bool seria
// "gravar", e gravar por campo esquecido é o que não pode acontecer.
func TestDryRunAusenteEh400(t *testing.T) {
	t.Parallel()

	h := montarHTTP(t)
	rec := h.post(t, casaA, `{"month":"2026-09"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	codigo, campos := erroDoCorpo(t, rec)
	assert.Equal(t, "VALIDATION_FAILED", codigo)
	assert.Contains(t, campos, "dryRun")
	assert.Zero(t, h.tx.chamadas)
}

// Campo fora do contrato é 400 — não é ignorado (S2, mass assignment). Inclui
// a caixa errada do nome e o campo que "parece" aceitável.
func TestCorpoForaDoContratoEh400(t *testing.T) {
	t.Parallel()

	corpos := []string{
		`{"month":"2026-09","dryRun":true,"householdId":"outra-casa"}`,
		`{"month":"2026-09","DryRun":true}`,
		`{"month":"2026-09","dryRun":true,"ids":["x"]}`,
		`{"month":"2026-09","dryRun":true,"overwrite":true}`,
		`{"month":"2026-09","dryRun":"true"}`,
	}
	for _, corpo := range corpos {
		t.Run(corpo, func(t *testing.T) {
			t.Parallel()
			h := montarHTTP(t)
			rec := h.post(t, casaA, corpo)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "corpo: %s", rec.Body.String())
			assert.Zero(t, h.tx.chamadas)
		})
	}
}

// `overwriteCategorized` ausente é FALSE na borda, e a tela não precisa
// mandá-lo para ficar segura.
func TestOverwriteAusenteNoCorpoNaoTroca(t *testing.T) {
	t.Parallel()

	h := montarHTTP(t)
	h.categorias.juntar(casaA, "cat-mercado", "Mercado", category.KindExpense, "supermercado")
	h.categorias.juntar(casaA, "cat-cdb", "Renda fixa", category.KindInvestment, "cdb")
	h.contas.juntar(casaA, "acc-1", "Nubank")
	h.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))

	rec := h.post(t, casaA, `{"month":"2026-09","dryRun":false}`)
	require.Equal(t, http.StatusOK, rec.Code)

	var view investment.DetectView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	assert.Zero(t, view.Marked)
	assert.EqualValues(t, 1, view.AlreadyCategorized)
	assert.Equal(t, "cat-mercado", *h.ledger.por(uuidDe(1)).CategoryID)
}

// O log da execução real leva contagens e a flag — NUNCA descrição, valor,
// palavra-chave ou id de lançamento (S8).
func TestLogDaExecucaoRealNaoVazaDadoDaCasa(t *testing.T) {
	t.Parallel()

	h := montarHTTP(t)
	h.categorias.juntar(casaA, "cat-cdb", "Renda fixa", category.KindInvestment, "cdb")
	h.contas.juntar(casaA, "acc-1", "Nubank")
	h.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	rec := h.post(t, casaA, `{"month":"2026-09","dryRun":false}`)
	require.Equal(t, http.StatusOK, rec.Code)

	log := h.logs.String()
	assert.Contains(t, log, `"marked":1`)
	assert.Contains(t, log, `"month":"2026-09"`)
	assert.Contains(t, log, `"overwrite_categorized":false`)
	assert.NotContains(t, log, "CDB 15 DIAS", "descrição no log")
	assert.NotContains(t, log, "200000", "centavos no log")
	assert.NotContains(t, strings.ToLower(log), `"cdb"`, "palavra-chave no log")
	assert.NotContains(t, log, uuidDe(1), "id de lançamento no log")
}

// A prévia não loga execução: não escreveu nada.
func TestPreviaNaoEmiteLogDeExecucao(t *testing.T) {
	t.Parallel()

	h := montarHTTP(t)
	h.categorias.juntar(casaA, "cat-cdb", "Renda fixa", category.KindInvestment, "cdb")
	h.contas.juntar(casaA, "acc-1", "Nubank")
	h.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	rec := h.post(t, casaA, `{"month":"2026-09","dryRun":true}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, h.logs.String(), "investimentos detectados")
}

// Mês grande demais é 422 VALIDATION_FAILED em `fields.month` — e não um
// código inventado: KIND_LOCKED e CATEGORY_KIND_MISMATCH não existem para esta
// rota.
func TestMesGrandeDemaisEh422EmMonth(t *testing.T) {
	t.Parallel()

	h := montarHTTP(t)
	h.categorias.juntar(casaA, "cat-cdb", "Renda fixa", category.KindInvestment, "cdb")
	h.contas.juntar(casaA, "acc-1", "Nubank")
	for i := range investment.MaxCandidates + 1 {
		h.ledger.juntar(lanc(casaA, uuidDe(i+1), transaction.KindExpense, "LINHA", 100, 1, "2026-09", ""))
	}

	rec := h.post(t, casaA, `{"month":"2026-09","dryRun":false}`)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	codigo, campos := erroDoCorpo(t, rec)
	assert.Equal(t, "VALIDATION_FAILED", codigo)
	assert.Contains(t, campos, "month")
	assert.NotContains(t, campos["month"], "10000", "o teto está no contrato; a contagem do mês é dado da casa")
	assert.Zero(t, h.tx.chamadas)
}

// ErrTooManyCategories é 500 GENÉRICO, nunca 4xx (ADR-029 j.2): ele conta
// categorias da própria casa, que já têm teto próprio — a pessoa não tem como
// corrigir um campo do pedido.
func TestTetoDeCategoriasEh500Generico(t *testing.T) {
	t.Parallel()

	h := montarHTTP(t)
	for i := range maxIDsNoFiltro + 1 {
		h.categorias.juntar(casaA, "cat-"+uuidDe(i), "Inv", category.KindInvestment)
	}

	rec := h.get(t, casaA, "/api/v1/investments?month=2026-09")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	codigo, campos := erroDoCorpo(t, rec)
	assert.Equal(t, "INTERNAL_ERROR", codigo)
	assert.Empty(t, campos)
	assert.NotContains(t, rec.Body.String(), "categorias", "a mensagem ao cliente é genérica")
	assert.Contains(t, h.logs.String(), "falha em investimentos", "o detalhe tem de ir para o log")
}
