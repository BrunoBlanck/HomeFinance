package httpserver_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChainRespeitaAOrdem(t *testing.T) {
	t.Parallel()

	var ordem []string
	mk := func(nome string) httpserver.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ordem = append(ordem, nome)
				next.ServeHTTP(w, r)
			})
		}
	}
	h := httpserver.Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		ordem = append(ordem, "handler")
	}), mk("externo"), nil, mk("interno"))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/t", nil))
	assert.Equal(t, []string{"externo", "interno", "handler"}, ordem)
}

func TestRequestIDEhSempreDoServidor(t *testing.T) {
	t.Parallel()

	var visto string
	h := httpserver.RequestID()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		visto = httpserver.RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/t", nil)
	r.Header.Set("X-Request-Id", "id-forjado-pelo-cliente")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	assert.NotEmpty(t, visto)
	assert.NotEqual(t, "id-forjado-pelo-cliente", visto, "o id do cliente não pode envenenar o log")
	assert.Equal(t, visto, rec.Header().Get("X-Request-Id"))
}

func TestRecoverNaoVazaStackParaOCliente(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := logging.New(&buf, logging.Options{Level: "debug", Format: "json"})
	h := httpserver.Chain(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			panic("segredo-do-panic")
		}),
		httpserver.Recover(lg),
	)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/t", nil))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), httpserver.CodeInternalError)
	assert.NotContains(t, rec.Body.String(), "segredo-do-panic")
	assert.NotContains(t, rec.Body.String(), "goroutine")
	// O detalhe existe, mas só no log.
	assert.Contains(t, buf.String(), "panic no caminho de request")
}

func TestRecoverNaoReescreveRespostaJaEnviada(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := logging.New(&buf, logging.Options{Level: "debug", Format: "json"})
	h := httpserver.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			httpserver.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
			panic("depois da resposta")
		}),
		httpserver.Recover(lg),
	)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/t", nil))
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAccessLogNaoLogaQueryStringNemCorpo(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := logging.New(&buf, logging.Options{Level: "debug", Format: "json"})
	h := httpserver.Chain(okHandler(), httpserver.AccessLog(lg, 0))

	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login?token=segredo-na-url", strings.NewReader("corpo-secreto"))
	r.RemoteAddr = "203.0.113.4:9"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	out := buf.String()
	assert.Contains(t, out, "/api/v1/auth/login")
	assert.Contains(t, out, "203.0.113.4")
	assert.NotContains(t, out, "segredo-na-url")
	assert.NotContains(t, out, "corpo-secreto")
}

func TestSecurityHeaders(t *testing.T) {
	t.Parallel()

	t.Run("sem hsts", func(t *testing.T) {
		h := httpserver.SecurityHeaders(false)(okHandler())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/t", nil))

		assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
		assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
		assert.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
		assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "default-src 'none'")
		assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")
		assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		assert.Empty(t, rec.Header().Get("Strict-Transport-Security"))
	})

	t.Run("com hsts", func(t *testing.T) {
		h := httpserver.SecurityHeaders(true)(okHandler())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/t", nil))
		assert.Contains(t, rec.Header().Get("Strict-Transport-Security"), "max-age=31536000")
	})
}

type autenticadorFake struct {
	ident session.Identity
	err   error
}

func (a autenticadorFake) Authenticate(*http.Request) (session.Identity, error) {
	return a.ident, a.err
}

func TestRequireAuth(t *testing.T) {
	t.Parallel()

	valida := session.Identity{UserID: "u1", HouseholdID: "h1", Role: session.RoleOwner, SessionID: "s1"}

	t.Run("identidade valida chega ao handler", func(t *testing.T) {
		var visto session.Identity
		h := httpserver.RequireAuth(autenticadorFake{ident: valida})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			visto, _ = session.FromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, valida, visto)
	})

	t.Run("erro vira 401 sem limpar cookies", func(t *testing.T) {
		h := httpserver.RequireAuth(autenticadorFake{err: errors.New("token ruim")})(okHandler())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me", nil))

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
		assert.Contains(t, rec.Body.String(), httpserver.CodeUnauthenticated)
		assert.Empty(t, rec.Header().Values("Set-Cookie"), "access expirado não pode derrubar o cookie de refresh")
	})

	t.Run("identidade incompleta vira 401", func(t *testing.T) {
		h := httpserver.RequireAuth(autenticadorFake{ident: session.Identity{UserID: "u1"}})(okHandler())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me", nil))
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("autenticador ausente vira 401", func(t *testing.T) {
		h := httpserver.RequireAuth(nil)(okHandler())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me", nil))
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

// Critério de aceite 9 da spec 0001: 404 e 405 do ServeMux também saem no
// formato único.
func TestErrorShimPadronizaErrosDoServeMux(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/health", okHandler())
	h := httpserver.ErrorShim(mux)

	t.Run("404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/inexistente", nil))

		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"error":{"code":"NOT_FOUND","message":"Recurso não encontrado."}}`, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "404 page not found")
	})

	t.Run("405", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/health", nil))

		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"error":{"code":"METHOD_NOT_ALLOWED","message":"Método não permitido."}}`, rec.Body.String())
	})

	t.Run("resposta normal passa intacta", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
	})

	t.Run("404 de handler nosso passa intacto", func(t *testing.T) {
		inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			httpserver.WriteError(w, http.StatusNotFound, httpserver.CodeNotFound, httpserver.MsgNotFound)
		})
		rec := httptest.NewRecorder()
		httpserver.ErrorShim(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.JSONEq(t, `{"error":{"code":"NOT_FOUND","message":"Recurso não encontrado."}}`, rec.Body.String())
	})
}

type pingerFake struct{ err error }

func (p pingerFake) Ping(context.Context) error { return p.err }

func TestHealthNaoTocaNoBanco(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpserver.Health()(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
	// D17: nada de versão, driver ou detalhe de ambiente.
	body := rec.Body.String()
	for _, proibido := range []string{"version", "driver", "sqlite", "postgres", "go1."} {
		assert.NotContains(t, strings.ToLower(body), proibido)
	}
}

func TestReady(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := logging.New(&buf, logging.Options{Level: "debug", Format: "json"})

	t.Run("sem dependencia responde 503", func(t *testing.T) {
		rec := httptest.NewRecorder()
		httpserver.Ready(nil, lg)(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Contains(t, rec.Body.String(), httpserver.CodeServiceUnavailable)
	})

	t.Run("ping ok responde 200", func(t *testing.T) {
		rec := httptest.NewRecorder()
		httpserver.Ready(pingerFake{}, lg)(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
	})

	t.Run("ping com erro responde 503 sem detalhe", func(t *testing.T) {
		rec := httptest.NewRecorder()
		falha := errors.New("dial tcp 10.0.0.5:5432: connection refused")
		httpserver.Ready(pingerFake{err: falha}, lg)(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))

		assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
		assert.JSONEq(t, `{"error":{"code":"SERVICE_UNAVAILABLE","message":"Serviço indisponível."}}`, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), "10.0.0.5")
		assert.NotContains(t, rec.Body.String(), "connection refused")
	})
}
