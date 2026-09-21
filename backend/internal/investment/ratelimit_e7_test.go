package investment_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/config"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// QA da E7 — critério 12, a última das quatro formas de abuso: "rajada de
// `detect` → 429 pela classe de escrita pesada".
//
// A regra do balde já era testada em `platform/config` (valores) e em
// `cmd/api/routes_test.go` (a rota tem DOIS middlewares). O que faltava é o
// que só a CADEIA montada prova: que a chamada recusada **não chega ao
// serviço**. Um limitador ligado depois do handler, ou com a chave errada,
// passa nos dois testes que já existiam e mesmo assim deixa a 4ª rajada
// escrever.
//
// A cadeia aqui é a mesma de `cmd/api/routes.go` e de `main.go`:
// `RateLimit(newLimiter(rl.InvestmentDetect), HouseholdKey(hash))` em volta do
// handler, com a identidade já no contexto — que é o que o `RequireAuth` faz
// antes, e é de onde o limitador tira a casa.

// limitadorDeProducao reproduz o `newLimiter` de cmd/api/main.go: cota da
// janela pela regra e ESTOURO reduzido quando a regra o declara.
//
// O estouro é o ponto desta rota: com o padrão (estouro = cota) uma casa
// dispararia 60 execuções simultâneas dentro da cota, cada uma varrendo até
// 10.000 linhas e segurando uma conexão do pool numa transação aberta — o
// achado A2 da revisão anterior. Com 3, prévia e confirmação continuam
// cabendo, e a rajada vira fila.
func limitadorDeProducao(r config.Rule, idleTTL time.Duration, agora *time.Time) *httpserver.Limiter {
	l := httpserver.NewLimiter(r.Requests, r.Window, idleTTL)
	if r.Burst > 0 {
		l = l.WithBurst(r.Burst)
	}
	return l.WithClock(func() time.Time { return *agora })
}

// cadeiaDoDetect devolve o handler embrulhado no limitador por casa e o
// relógio que o limitador enxerga.
func cadeiaDoDetect(t *testing.T, h *httpCenario) (http.Handler, *time.Time, config.Rule) {
	t.Helper()

	rl := config.DefaultRateLimits()
	regra := rl.InvestmentDetect
	require.Equal(t, 60, regra.Requests, "spec 0006 §3.3.5: a classe de escrita pesada é 60/h por casa")
	require.Equal(t, time.Hour, regra.Window)
	require.Equal(t, 3, regra.Burst, "estouro reduzido é o que impede a rajada de esgotar o pool (A2)")

	agora := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	limitador := limitadorDeProducao(regra, rl.IdleTTL, &agora)
	// A chave por casa é um HMAC em produção; aqui basta ser injetiva e não
	// ecoar o id em claro.
	hash := func(householdID string) string { return "h:" + strings.ToUpper(householdID) }
	cadeia := httpserver.RateLimit(limitador, httpserver.HouseholdKey(hash))(http.HandlerFunc(h.handler.Detect))
	return cadeia, &agora, regra
}

func (h *httpCenario) postNaCadeia(t *testing.T, cadeia http.Handler, casa, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/investments/detect", strings.NewReader(corpo))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID: "user-1", HouseholdID: casa, Role: "owner", SessionID: "sess-1",
	}))
	rec := httptest.NewRecorder()
	cadeia.ServeHTTP(rec, r)
	return rec
}

// A quarta chamada da rajada é 429 — e ela é justamente a que GRAVARIA.
func TestRajadaDeDetectEh429ENaoChegaAoServico(t *testing.T) {
	t.Parallel()

	h := montarHTTP(t)
	h.categorias.juntar(casaA, "cat-cdb", "Renda fixa", category.KindInvestment, "cdb")
	h.contas.juntar(casaA, "acc-1", "Nubank")
	h.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 5, "2026-09", ""))

	// A vizinha tem a MESMA palavra na categoria dela e uma linha igual: o
	// balde é por casa, e é isso que precisa ficar provado.
	h.categorias.juntar(casaB, "cat-b-cdb", "Renda fixa B", category.KindInvestment, "cdb")
	h.contas.juntar(casaB, "acc-1", "Itaú")
	h.ledger.juntar(lanc(casaB, uuidDe(2), transaction.KindExpense, "CDB 15 DIAS", 999900, 5, "2026-09", ""))

	cadeia, relogio, regra := cadeiaDoDetect(t, h)
	previa := `{"month":"2026-09","dryRun":true}`
	real := `{"month":"2026-09","dryRun":false}`

	// O estouro inteiro em prévias: todas passam, nenhuma grava.
	for i := 1; i <= regra.Burst; i++ {
		rec := h.postNaCadeia(t, cadeia, casaA, previa)
		require.Equal(t, http.StatusOK, rec.Code, "chamada %d: %s", i, rec.Body.String())
	}
	require.Nil(t, h.ledger.por(uuidDe(1)).CategoryID)
	require.Empty(t, h.auditoria.entradas)

	// A seguinte, no MESMO instante, é 429.
	rec := h.postNaCadeia(t, cadeia, casaA, real)
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), httpserver.CodeRateLimited)

	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, retry, 1)
	assert.LessOrEqual(t, retry, 60, "60/h é um token por minuto: o próximo cabe em até 60 s")

	// A recusa não conta nada sobre a casa nem sobre a rota (S5/S8).
	assert.NotContains(t, rec.Body.String(), casaA)
	assert.NotContains(t, rec.Body.String(), "investments")
	assert.NotContains(t, rec.Body.String(), "2026-09")

	// E, sobretudo: o 429 não chegou ao serviço.
	assert.Nil(t, h.ledger.por(uuidDe(1)).CategoryID, "o 429 não pode ter escrito")
	assert.Zero(t, h.ledger.chamadasSetNull, "o 429 não pode ter chamado o repositório")
	assert.Zero(t, h.tx.chamadas, "o 429 não abre transação")
	assert.Empty(t, h.auditoria.entradas, "o 429 não audita")
	assert.NotContains(t, h.logs.String(), "investimentos detectados", "o handler não rodou")

	// Repetir dá 429 de novo: o 429 não gasta token nem libera.
	assert.Equal(t, http.StatusTooManyRequests, h.postNaCadeia(t, cadeia, casaA, real).Code)

	// A OUTRA casa tem balde próprio: passa e grava só o dela.
	recDela := h.postNaCadeia(t, cadeia, casaB, real)
	require.Equal(t, http.StatusOK, recDela.Code, recDela.Body.String())
	require.NotNil(t, h.ledger.por(uuidDe(2)).CategoryID)
	assert.Equal(t, "cat-b-cdb", *h.ledger.por(uuidDe(2)).CategoryID)
	assert.Nil(t, h.ledger.por(uuidDe(1)).CategoryID, "a execução da vizinha não marcou a minha linha")

	// Passado o Retry-After, um token voltou: a execução real passa e grava.
	*relogio = relogio.Add(time.Duration(retry) * time.Second)
	recDepois := h.postNaCadeia(t, cadeia, casaA, real)
	require.Equal(t, http.StatusOK, recDepois.Code, recDepois.Body.String())
	require.NotNil(t, h.ledger.por(uuidDe(1)).CategoryID)
	assert.Equal(t, "cat-cdb", *h.ledger.por(uuidDe(1)).CategoryID)

	// E a seguinte, no mesmo instante, volta a ser 429.
	assert.Equal(t, http.StatusTooManyRequests, h.postNaCadeia(t, cadeia, casaA, previa).Code)
}
