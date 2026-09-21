package aiprompt_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/aiprompt"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Os testes de handler provam o CONTRATO de GET /ai/export-prompt: status,
// forma exata do JSON, o campo apontado em cada recusa e o que NÃO sai na
// resposta.

type httpAmbiente struct {
	*ambiente
	handler *aiprompt.Handler
}

func novoHTTPAmbiente(t *testing.T) *httpAmbiente {
	t.Helper()
	amb := novoAmbiente(t)
	lg := logging.New(amb.logs, logging.Options{Level: "debug", Format: "json"})
	return &httpAmbiente{ambiente: amb, handler: aiprompt.NewHandler(amb.svc, lg)}
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
	a.handler.ExportPrompt(rec, r)
	return rec
}

// erroDe extrai code e fields da resposta de erro.
func erroDe(t *testing.T, rec *httptest.ResponseRecorder) (string, map[string]string) {
	t.Helper()
	var corpo struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Fields  map[string]string `json:"fields"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo), "corpo: %s", rec.Body.String())
	return corpo.Error.Code, corpo.Error.Fields
}

// Critério 1: 3 meses passam. A resposta é o schema AiExportPrompt, com os
// CINCO campos — nem um a mais, nem um a menos.
func TestHandlerCaminhoFeliz(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	a.casaComum()
	a.ledger.linhas = []lancamento{
		{casa: minhaCasa, mes: mes2, desc: "Zaffari", kind: transaction.KindExpense,
			conta: contaCorrente, categoria: ptr(catMercado), cents: 15_000},
	}

	rec := a.chamar(t, minhaCasa, "/api/v1/ai/export-prompt?fromMonth="+mes1+"&toMonth="+mes3)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var corpo map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
	assert.ElementsMatch(t, []string{"prompt", "fromMonth", "toMonth", "generatedAt", "stats"},
		chaves(corpo), "campo a mais ou a menos é divergência de contrato")

	assert.Equal(t, mes1, corpo["fromMonth"])
	assert.Equal(t, mes3, corpo["toMonth"])
	assert.Equal(t, "2026-09-21T15:04:05Z", corpo["generatedAt"])

	stats, ok := corpo["stats"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{
		"accounts":              float64(2),
		"categories":            float64(3),
		"descriptions":          float64(1),
		"transactions":          float64(1),
		"truncatedDescriptions": float64(0),
	}, stats)

	prompt, ok := corpo["prompt"].(string)
	require.True(t, ok)
	assert.Contains(t, prompt, "## 1. Papel e contexto")
	assert.Contains(t, prompt, "## 9. Tarefa e formato de saída")
}

// Critério 1 e 2: cada recusa de janela aponta o campo certo, com o MESMO
// código de erro de sempre — nenhum código novo (D4 da spec 0003).
func TestHandlerRecusasDeJanela(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome   string
		query  string
		status int
		campo  string
	}{
		{"janela de 4 meses", "?fromMonth=2026-06&toMonth=2026-09", http.StatusBadRequest, "toMonth"},
		{"invertida", "?fromMonth=2026-09&toMonth=2026-07", http.StatusBadRequest, "toMonth"},
		{"from malformado", "?fromMonth=2026-7&toMonth=2026-09", http.StatusBadRequest, "fromMonth"},
		{"to malformado", "?fromMonth=2026-07&toMonth=setembro", http.StatusBadRequest, "toMonth"},
		{"from ausente", "?toMonth=2026-09", http.StatusBadRequest, "fromMonth"},
		{"to ausente", "?fromMonth=2026-07", http.StatusBadRequest, "toMonth"},
		{"sem parâmetro nenhum", "", http.StatusBadRequest, "fromMonth"},
		{"from vazio", "?fromMonth=&toMonth=2026-09", http.StatusBadRequest, "fromMonth"},
		{"mês 13", "?fromMonth=2026-13&toMonth=2026-13", http.StatusBadRequest, "fromMonth"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			a := novoHTTPAmbiente(t)
			a.casaComum()

			rec := a.chamar(t, minhaCasa, "/api/v1/ai/export-prompt"+c.query)
			require.Equal(t, c.status, rec.Code, rec.Body.String())

			code, fields := erroDe(t, rec)
			assert.Equal(t, "VALIDATION_FAILED", code)
			assert.Contains(t, fields, c.campo)
			assert.Len(t, fields, 1, "uma recusa aponta UM campo")
			assert.Empty(t, a.ledger.chamadas, "recusa de borda não consulta nada")
		})
	}
}

// Critério 2, terceira parte: parâmetro REPETIDO é 400 — esta rota nasce lendo
// os dois com SoleQueryValue, e não pela primeira ocorrência.
//
// Os três casos são os três jeitos de a URL ficar ambígua: valores diferentes,
// valores iguais e a segunda vazia. A recusa é da AMBIGUIDADE, não da
// discordância — por isso os três são 400.
func TestHandlerParametroRepetidoEh400(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome  string
		query string
		campo string
	}{
		{"fromMonth duas vezes, valores diferentes", "?fromMonth=2026-07&fromMonth=2026-01&toMonth=2026-09", "fromMonth"},
		{"fromMonth duas vezes, o MESMO valor", "?fromMonth=2026-07&fromMonth=2026-07&toMonth=2026-09", "fromMonth"},
		{"fromMonth com a segunda vazia", "?fromMonth=2026-07&fromMonth=&toMonth=2026-09", "fromMonth"},
		{"toMonth duas vezes", "?fromMonth=2026-07&toMonth=2026-09&toMonth=2026-08", "toMonth"},
		{"toMonth com a segunda vazia", "?fromMonth=2026-07&toMonth=2026-09&toMonth=", "toMonth"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			a := novoHTTPAmbiente(t)
			a.casaComum()

			rec := a.chamar(t, minhaCasa, "/api/v1/ai/export-prompt"+c.query)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

			code, fields := erroDe(t, rec)
			assert.Equal(t, "VALIDATION_FAILED", code)
			assert.Contains(t, fields, c.campo)
			assert.Empty(t, a.ledger.chamadas)
			// A recusa não ecoa nenhum dos valores recebidos: contar para o
			// cliente o que ele mandou é devolver a entrada dele pela porta do
			// erro.
			assert.NotContains(t, rec.Body.String(), "2026-01")
		})
	}
}

// Sem identidade no contexto é 401, e nada é consultado.
func TestHandlerSemIdentidadeEh401(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	a.casaComum()

	rec := a.chamar(t, "", "/api/v1/ai/export-prompt?fromMonth="+mes1+"&toMonth="+mes3)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	code, _ := erroDe(t, rec)
	assert.Equal(t, "UNAUTHENTICATED", code)
	assert.Empty(t, a.ledger.chamadas)
}

// BOLA: a casa vem do TOKEN. Um `householdId` na query é ignorado sem efeito —
// nada aqui o lê.
func TestHandlerIgnoraHouseholdIdDaQuery(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	a.casaComum()

	rec := a.chamar(t, minhaCasa,
		"/api/v1/ai/export-prompt?fromMonth="+mes1+"&toMonth="+mes3+"&householdId="+outraCasa+"&accountId=x")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, a.ledger.chamadas, 1)
	assert.Equal(t, minhaCasa, a.ledger.chamadas[0].casa)
}

// A agregação que estoura é 422 — o pedido está bem formado, o período é que
// não cabe num prompt só — e NUNCA uma resposta parcial.
func TestHandlerAgregacaoQueEstouraEh422(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	a.casaComum()
	a.ledger.err = fmt.Errorf("agrupando: %w", transaction.ErrTooManyDescriptionGroups)

	rec := a.chamar(t, minhaCasa, "/api/v1/ai/export-prompt?fromMonth="+mes1+"&toMonth="+mes3)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	code, fields := erroDe(t, rec)
	assert.Equal(t, "VALIDATION_FAILED", code, "nenhum código de erro novo")
	assert.Contains(t, fields, "toMonth")
	assert.NotContains(t, rec.Body.String(), `"prompt"`, "nada parcial na resposta: não há campo prompt")
	// A contagem do período é dado da casa e não entra na mensagem.
	assert.NotContains(t, rec.Body.String(), fmt.Sprint(transaction.MaxDescriptionGroupRows))
}

// Falha inesperada é 500 GENÉRICO: mensagem sem detalhe, razão só no log — e o
// log sem descrição, sem centavos e sem nome de conta ou categoria.
func TestHandlerFalhaInesperadaEh500Generico(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	a.casaComum()
	a.ledger.err = fmt.Errorf("pq: relation \"transactions\" does not exist")

	rec := a.chamar(t, minhaCasa, "/api/v1/ai/export-prompt?fromMonth="+mes1+"&toMonth="+mes3)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	code, _ := erroDe(t, rec)
	assert.Equal(t, "INTERNAL_ERROR", code)
	assert.NotContains(t, rec.Body.String(), "transactions", "o detalhe do banco não vai para o cliente")

	logs := a.logs.String()
	assert.Contains(t, logs, "falha ao montar o prompt do menu IA")
	assert.NotContains(t, logs, "Conta Corrente")
	assert.NotContains(t, logs, "Zaffari")
}

func chaves(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
