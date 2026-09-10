package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type relogio struct {
	mu  sync.Mutex
	now time.Time
}

func (r *relogio) agora() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

func (r *relogio) avanca(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.now.Add(d)
}

func TestLimiterQueimaOEstouroEDevolveRetryAfter(t *testing.T) {
	t.Parallel()

	c := &relogio{now: time.Unix(1_700_000_000, 0)}
	lim := httpserver.NewLimiter(3, time.Minute, time.Hour).WithClock(c.agora)

	for i := range 3 {
		ok, _ := lim.Allow("ip-1")
		assert.True(t, ok, "requisição %d deveria passar", i+1)
	}

	ok, retry := lim.Allow("ip-1")
	assert.False(t, ok)
	assert.Positive(t, retry)

	// Chave diferente tem balde próprio.
	ok, _ = lim.Allow("ip-2")
	assert.True(t, ok)

	// Depois de repor um token, volta a passar.
	c.avanca(21 * time.Second)
	ok, _ = lim.Allow("ip-1")
	assert.True(t, ok)
}

func TestLimiterVarreChavesOciosas(t *testing.T) {
	t.Parallel()

	c := &relogio{now: time.Unix(1_700_000_000, 0)}
	lim := httpserver.NewLimiter(5, time.Minute, 10*time.Minute).WithClock(c.agora)

	for i := range 50 {
		lim.Allow("ip-" + strconv.Itoa(i))
	}
	assert.Equal(t, 50, lim.Len())

	c.avanca(11 * time.Minute)
	lim.Allow("ip-novo")
	assert.Equal(t, 1, lim.Len(), "chaves ociosas precisam sair do mapa")
}

// Critério de aceite 27 da spec 0001.
func TestRateLimitMiddlewareResponde429ComRetryAfter(t *testing.T) {
	t.Parallel()

	lim := httpserver.NewLimiter(1, time.Minute, time.Hour)
	h := httpserver.RateLimit(lim, httpserver.IPKey(0))(okHandler())

	r := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
		req.RemoteAddr = "203.0.113.7:5555"
		return req
	}

	primeiro := httptest.NewRecorder()
	h.ServeHTTP(primeiro, r())
	assert.Equal(t, http.StatusOK, primeiro.Code)

	segundo := httptest.NewRecorder()
	h.ServeHTTP(segundo, r())
	assert.Equal(t, http.StatusTooManyRequests, segundo.Code)
	assert.Contains(t, segundo.Body.String(), httpserver.CodeRateLimited)

	retry, err := strconv.Atoi(segundo.Header().Get("Retry-After"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, retry, 1)

	// A mensagem não pode dizer nada sobre conta, e-mail ou rota.
	assert.NotContains(t, segundo.Body.String(), "conta")
	assert.NotContains(t, segundo.Body.String(), "login")
}

func TestRateLimitSeparaPorIP(t *testing.T) {
	t.Parallel()

	lim := httpserver.NewLimiter(1, time.Minute, time.Hour)
	h := httpserver.RateLimit(lim, httpserver.IPKey(0))(okHandler())

	req1 := httptest.NewRequest(http.MethodGet, "/t", nil)
	req1.RemoteAddr = "203.0.113.1:1"
	req2 := httptest.NewRequest(http.MethodGet, "/t", nil)
	req2.RemoteAddr = "203.0.113.2:1"

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	assert.Equal(t, http.StatusOK, rec1.Code)
	assert.Equal(t, http.StatusOK, rec2.Code)
}

func TestLimiterNilPermite(t *testing.T) {
	t.Parallel()

	var lim *httpserver.Limiter
	ok, _ := lim.Allow("x")
	assert.True(t, ok)
}

func TestLimiterEhSeguroSobConcorrencia(t *testing.T) {
	t.Parallel()

	lim := httpserver.NewLimiter(100, time.Minute, time.Hour)
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lim.Allow("mesma-chave")
		}()
	}
	wg.Wait()
	ok, _ := lim.Allow("mesma-chave")
	assert.False(t, ok, "as 100 permissões já foram consumidas")
}

// ---------------------------------------------------------------------------
// Achado C da re-revisão — a cota de 24 h virava cota de 1 h
// ---------------------------------------------------------------------------

// A varredura apaga o balde ocioso há IdleTTL, e um balde apagado RENASCE
// CHEIO. Com IdleTTL menor que a janela, qualquer regra de janela longa era
// anulada: uma requisição a cada 61 min nunca encontrava o balde de pé, e a
// cota de "1 por 24 h" entregava 23 permissões num dia.
//
// A correção é de RAIZ, em NewLimiter: o TTL efetivo é max(idleTTL, window).
func TestLimiterNaoDeixaAVarreduraAnularJanelaLonga(t *testing.T) {
	t.Parallel()

	c := &relogio{now: time.Unix(1_700_000_000, 0)}
	// Exatamente a configuração de produção do aviso "conta já existe":
	// 1 por 24 h, com IdleTTL de 1 h.
	lim := httpserver.NewLimiter(1, 24*time.Hour, time.Hour).WithClock(c.agora)

	permitidas := 0
	if ok, _ := lim.Allow("conta"); ok {
		permitidas++
	}
	for range 23 {
		c.avanca(61 * time.Minute)
		if ok, _ := lim.Allow("conta"); ok {
			permitidas++
		}
	}

	assert.Equal(t, 1, permitidas,
		"a cota de 24 h precisa valer por 24 h: a varredura ressuscitou o balde")

	// E, passado o dia inteiro, a cota volta.
	c.avanca(25 * time.Hour)
	ok, _ := lim.Allow("conta")
	assert.True(t, ok, "depois da janela a cota tem de se recompor")
}

// O piso do TTL não pode ser confundido com "nunca varrer": chave ociosa
// além do TTL efetivo continua saindo do mapa (risco 9 da §9 da spec 0001).
func TestLimiterAindaVarreComPisoDeTTL(t *testing.T) {
	t.Parallel()

	c := &relogio{now: time.Unix(1_700_000_000, 0)}
	lim := httpserver.NewLimiter(1, 24*time.Hour, time.Hour).WithClock(c.agora)
	assert.Equal(t, 24*time.Hour, lim.IdleTTL(), "o TTL efetivo é max(idleTTL, window)")

	for i := range 30 {
		lim.Allow("ip-" + strconv.Itoa(i))
	}
	assert.Equal(t, 30, lim.Len())

	c.avanca(25 * time.Hour)
	lim.Allow("ip-novo")
	assert.Equal(t, 1, lim.Len(), "chaves ociosas além do TTL efetivo precisam sair do mapa")
}
