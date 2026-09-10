package httpserver

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// KeyFunc extrai a chave de limitação de uma requisição.
type KeyFunc func(r *http.Request) string

type bucket struct {
	lim  *rate.Limiter
	last time.Time
}

// Limiter é um limitador de taxa com chave (IP, conta, …) baseado em
// golang.org/x/time/rate.
//
// O mapa é varrido periodicamente: sem isso, uma inundação de IPs distintos
// faria o próprio limitador virar o vetor de exaustão de memória (risco 9 da
// §9 da spec 0001).
type Limiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	limit     rate.Limit
	burst     int
	idleTTL   time.Duration
	lastSweep time.Time

	// now é injetável para teste; nil usa time.Now.
	now func() time.Time
}

// NewLimiter cria um limitador de "requests" requisições por "window", com
// estouro igual a requests. idleTTL é quanto tempo uma chave ociosa
// sobrevive no mapa.
//
// O TTL EFETIVO é max(idleTTL, window), e isso é uma correção de segurança,
// não um detalhe de tuning (achado C da re-revisão).
//
// A varredura apaga o balde ocioso há idleTTL, e um balde apagado RENASCE
// CHEIO. Se o TTL fosse menor que a janela, qualquer regra de janela maior
// seria anulada: com idleTTL=1 h, uma cota de "1 por 24 h" virava "1 por
// hora" — uma requisição a cada 61 min nunca encontrava o balde de pé. O PoC
// da revisão entregou 23 avisos em 24 h com cota configurada de 1/24 h.
//
// Elevar o piso aqui protege toda regra futura: um teto só vale se o estado
// que o sustenta viver pelo menos o tempo que ele leva para se recompor
// (burst/limit = window).
func NewLimiter(requests int, window, idleTTL time.Duration) *Limiter {
	if requests < 1 {
		requests = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	if idleTTL <= 0 {
		idleTTL = time.Hour
	}
	if idleTTL < window {
		idleTTL = window
	}
	return &Limiter{
		buckets: make(map[string]*bucket),
		limit:   rate.Limit(float64(requests) / window.Seconds()),
		burst:   requests,
		idleTTL: idleTTL,
	}
}

// WithClock injeta o relógio (teste).
func (l *Limiter) WithClock(now func() time.Time) *Limiter {
	l.now = now
	return l
}

func (l *Limiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// Allow consome um token da chave. Quando nega, devolve quanto tempo falta
// para a próxima permissão (vira o cabeçalho Retry-After).
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	now := l.clock()

	l.mu.Lock()
	l.sweepLocked(now)
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{lim: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.last = now
	lim := b.lim
	l.mu.Unlock()

	res := lim.ReserveN(now, 1)
	if !res.OK() {
		return false, time.Second
	}
	if delay := res.DelayFrom(now); delay > 0 {
		res.CancelAt(now)
		return false, delay
	}
	return true, 0
}

// IdleTTL devolve o TTL EFETIVO das chaves ociosas, já elevado ao piso da
// janela. Existe para o teste de invariante conseguir afirmar que nenhuma
// regra tem janela maior que o TTL que a sustenta.
func (l *Limiter) IdleTTL() time.Duration {
	if l == nil {
		return 0
	}
	return l.idleTTL
}

// Len devolve quantas chaves estão vivas (teste/observabilidade).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

func (l *Limiter) sweepLocked(now time.Time) {
	if now.Sub(l.lastSweep) < l.idleTTL {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if now.Sub(b.last) >= l.idleTTL {
			delete(l.buckets, k)
		}
	}
}

// WriteRateLimited responde 429 com Retry-After. A mensagem é sempre a mesma,
// independente da rota e da existência da conta (§3.12 da spec 0001).
func WriteRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	WriteError(w, http.StatusTooManyRequests, CodeRateLimited, MsgRateLimited)
}

// RateLimit devolve o middleware que aplica o limitador com a chave dada.
func RateLimit(l *Limiter, key KeyFunc) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if l == nil || key == nil {
				next.ServeHTTP(w, r)
				return
			}
			if ok, retry := l.Allow(key(r)); !ok {
				WriteRateLimited(w, retry)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// IPKey devolve a KeyFunc que limita por IP do cliente.
func IPKey(trustedProxyCount int) KeyFunc {
	return func(r *http.Request) string { return ClientIP(r, trustedProxyCount) }
}
