package httpserver

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/id"
)

// Middleware é a assinatura padrão de decorador HTTP (ADR-001).
type Middleware func(http.Handler) http.Handler

// Chain compõe middlewares. O primeiro da lista é o mais externo, ou seja,
// o primeiro a ver a requisição e o último a ver a resposta.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		if mws[i] == nil {
			continue
		}
		h = mws[i](h)
	}
	return h
}

type requestIDKey struct{}

// RequestIDFromContext devolve o identificador da requisição atual.
func RequestIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey{}).(string)
	return v
}

// RequestID gera um identificador por requisição e o publica no contexto e no
// cabeçalho de resposta.
//
// O identificador é SEMPRE gerado pelo servidor. Aceitar um X-Request-Id do
// cliente permitiria envenenar o log (injeção de conteúdo e colisão
// proposital de correlação).
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rid := id.New()
			w.Header().Set("X-Request-Id", rid)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, rid)))
		})
	}
}

// statusRecorder guarda o status e o tamanho para o log de acesso.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int64
	wrote   bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wrote {
		return
	}
	s.wrote = true
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wrote {
		s.WriteHeader(http.StatusOK)
	}
	n, err := s.ResponseWriter.Write(b)
	s.written += int64(n)
	return n, err
}

// Unwrap permite que http.ResponseController alcance o writer original.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Recover é a ÚLTIMA barreira contra panic (docs/SEGURANCA.md e AGENTS.md:
// panic é proibido no caminho de request; este middleware existe para que um
// bug não derrube o processo inteiro, não como estratégia de erro).
//
// O stack trace vai só para o log; o cliente recebe 500 genérico.
func Recover(lg *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			defer func() {
				if rv := recover(); rv != nil {
					if rv == http.ErrAbortHandler { //nolint:errorlint // valor sentinela por igualdade, como manda a stdlib
						panic(rv)
					}
					lg.ErrorContext(r.Context(), "panic no caminho de request",
						slog.String("request_id", RequestIDFromContext(r.Context())),
						slog.String("method", r.Method),
						slog.String("path", r.URL.Path),
						slog.Any("panic", rv),
						slog.String("stack", string(debug.Stack())),
					)
					if !rec.wrote {
						WriteError(w, http.StatusInternalServerError, CodeInternalError, MsgInternalError)
					}
				}
			}()
			next.ServeHTTP(rec, r)
		})
	}
}

// AccessLog registra uma linha por requisição.
//
// Não loga query string nem corpo: dado sensível não pode entrar em query
// (docs/SEGURANCA.md §6) e corpo completo é proibido em log (§4).
func AccessLog(lg *slog.Logger, trustedProxyCount int) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			level := slog.LevelInfo
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case rec.status >= 400:
				level = slog.LevelWarn
			}

			lg.LogAttrs(r.Context(), level, "http",
				slog.String("request_id", RequestIDFromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("bytes", rec.written),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("ip", ClientIP(r, trustedProxyCount)),
			)
		})
	}
}

// MaxBytes limita o corpo da requisição (docs/SEGURANCA.md §3).
//
// http.MaxBytesReader também fecha a conexão quando o cliente insiste em
// mandar mais, então serve como defesa contra corpo infinito.
func MaxBytes(limit int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}
