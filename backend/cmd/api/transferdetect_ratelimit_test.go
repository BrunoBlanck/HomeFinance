package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/platform/config"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Abuso do POST /transfers/detect (QA, spec 0005 §13.5.11 e ADR-028f).
//
// O teste de rotas existente só conta os middlewares da cadeia — dois — e
// isso não distingue "o limitador certo, com a chave certa" de "o balde do
// auto-categorize colado por engano". Aqui a cadeia REAL é montada e
// exercida: a 61ª chamada da hora vira 429 antes de o handler existir, casas
// diferentes têm baldes diferentes, e gastar o balde do auto-categorize não
// tranca o reprocessamento.

// rotaDe devolve a rota da tabela pelo padrão completo.
func rotaDe(t *testing.T, rotas []Route, padrao string) Route {
	t.Helper()
	for _, r := range rotas {
		if r.FullPattern() == padrao {
			return r
		}
	}
	t.Fatalf("rota %q não está na tabela", padrao)
	return Route{}
}

// cadeia monta o handler final da rota: os middlewares na ordem da tabela em
// volta de um contador, que faz as vezes do handler de verdade.
func cadeia(r Route, chamadas *int) http.Handler {
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*chamadas++
		w.WriteHeader(http.StatusOK)
	})
	for i := len(r.Middlewares) - 1; i >= 0; i-- {
		if r.Middlewares[i] == nil {
			continue
		}
		h = r.Middlewares[i](h)
	}
	return h
}

// pedidoDaCasa é um POST autenticado como a casa informada. requireAuth fica
// zerado na tabela de teste, então a identidade entra direto no contexto — é
// exatamente o que o RequireAuth publicaria.
func pedidoDaCasa(casa string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transfers/detect", http.NoBody)
	return r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID: "u-1", HouseholdID: casa, Role: session.RoleOwner, SessionID: "s-1",
	}))
}

// depsComLimitadoresReais monta a tabela com os limitadores de produção
// (60/h por casa) e uma householdKey que não devolve o id em claro.
func depsComLimitadoresReais() routeDeps {
	rl := config.DefaultRateLimits()
	novo := func(r config.Rule) *httpserver.Limiter {
		return httpserver.NewLimiter(r.Requests, r.Window, 2*r.Window)
	}
	return routeDeps{
		limiters: routeLimiters{
			transferDetect: novo(rl.TransferDetect),
			autoCategorize: novo(rl.AutoCategorize),
		},
		householdKey: func(id string) string { return "hash:" + id },
	}
}

// Critério 11: a 61ª chamada da hora responde 429 e o handler nem roda —
// nenhuma escrita, nenhuma auditoria, nenhuma leitura do banco.
func TestTransferDetectA61aChamadaDaHoraResponde429SemChegarNoHandler(t *testing.T) {
	t.Parallel()

	deps := depsComLimitadoresReais()
	rota := rotaDe(t, buildRoutes(deps), "POST /api/v1/transfers/detect")
	var chamadas int
	h := cadeia(rota, &chamadas)

	const cota = 60
	require.Equal(t, cota, config.DefaultRateLimits().TransferDetect.Requests)

	for i := 1; i <= cota; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, pedidoDaCasa("casa-1"))
		require.Equal(t, http.StatusOK, rec.Code, "chamada %d da cota", i)
	}
	assert.Equal(t, cota, chamadas)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, pedidoDaCasa("casa-1"))
	assert.Equal(t, http.StatusTooManyRequests, rec.Code, "a 61ª tem de ser recusada")
	assert.Equal(t, cota, chamadas, "o handler não pode ser alcançado depois do teto")
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))

	// A mensagem do 429 não pode carregar dado da casa.
	corpo := rec.Body.String()
	assert.NotContains(t, corpo, "casa-1")
	assert.NotContains(t, corpo, "hash:")

	// O balde é POR CASA: a vizinha continua com a cota inteira.
	outra := httptest.NewRecorder()
	h.ServeHTTP(outra, pedidoDaCasa("casa-2"))
	assert.Equal(t, http.StatusOK, outra.Code, "o teto de uma casa não pode trancar a outra")
}

// §13.1.8: balde SEPARADO do auto-categorize — gastar um não tranca o outro.
func TestTransferDetectTemBaldeSeparadoDoAutoCategorize(t *testing.T) {
	t.Parallel()

	deps := depsComLimitadoresReais()
	rotas := buildRoutes(deps)

	var chamadasAuto int
	auto := cadeia(rotaDe(t, rotas, "POST /api/v1/transactions/auto-categorize"), &chamadasAuto)
	var chamadasDetect int
	detect := cadeia(rotaDe(t, rotas, "POST /api/v1/transfers/detect"), &chamadasDetect)

	cota := config.DefaultRateLimits().AutoCategorize.Requests
	for i := 0; i < cota+1; i++ {
		rec := httptest.NewRecorder()
		r := pedidoDaCasa("casa-1")
		r.URL.Path = "/api/v1/transactions/auto-categorize"
		auto.ServeHTTP(rec, r)
	}
	assert.Equal(t, cota, chamadasAuto, "o auto-categorize gastou a cota dele")

	rec := httptest.NewRecorder()
	detect.ServeHTTP(rec, pedidoDaCasa("casa-1"))
	assert.Equal(t, http.StatusOK, rec.Code,
		"a cota do auto-categorize não pode consumir a do reprocessamento")
	assert.Equal(t, 1, chamadasDetect)
}

// Requisição sem identidade cai numa chave única e constante — nunca numa
// chave vazia que compartilhasse balde com a casa de alguém.
func TestTransferDetectSemIdentidadeCaiNumBaldeUnico(t *testing.T) {
	t.Parallel()

	deps := depsComLimitadoresReais()
	rota := rotaDe(t, buildRoutes(deps), "POST /api/v1/transfers/detect")
	var chamadas int
	h := cadeia(rota, &chamadas)

	cota := config.DefaultRateLimits().TransferDetect.Requests
	for i := 0; i < cota+1; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/transfers/detect", http.NoBody))
		if i == cota {
			assert.Equal(t, http.StatusTooManyRequests, rec.Code)
		}
	}

	// Estourar o balde "sem casa" não afeta uma casa de verdade.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, pedidoDaCasa("casa-1"))
	assert.Equal(t, http.StatusOK, rec.Code)
}

// A janela é de UMA hora, e não de um minuto: o balde não pode reabrir sozinho
// em segundos.
func TestTransferDetectUsaAJanelaDeUmaHora(t *testing.T) {
	t.Parallel()
	assert.Equal(t, time.Hour, config.DefaultRateLimits().TransferDetect.Window)
}
