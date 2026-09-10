package user

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
)

// SessionClearer limpa os cookies de sessão. Implementado pelo cookie jar do
// pacote auth; aqui é interface para não inverter a dependência
// (user não pode importar auth).
type SessionClearer interface {
	ClearSessionCookies(w http.ResponseWriter)
}

// Handler expõe os endpoints de usuário.
type Handler struct {
	svc    *Service
	cookie SessionClearer
	lg     *slog.Logger
}

// NewHandler monta o handler.
func NewHandler(svc *Service, cookie SessionClearer, lg *slog.Logger) *Handler {
	return &Handler{svc: svc, cookie: cookie, lg: lg}
}

// Me responde GET /api/v1/me (§3.11 da spec 0001).
//
// A identidade vem SÓ do contexto publicado pelo RequireAuth, ou seja, do
// token assinado — nunca de corpo, query ou path (docs/SEGURANCA.md §2).
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	ident, ok := session.FromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeUnauthenticated, httpserver.MsgUnauthenticated)
		return
	}

	view, err := h.svc.Me(r.Context(), ident)
	switch {
	case err == nil:
		httpserver.WriteJSON(w, http.StatusOK, view)

	case errors.Is(err, household.ErrNotFound), errors.Is(err, ErrNotFound):
		// Token válido, realidade mudou: o vínculo (ou o próprio usuário)
		// não existe mais. A sessão inteira perdeu o sentido, então
		// derrubamos os cookies — diferente de um access apenas expirado
		// (critério de aceite 24 da spec 0001).
		if h.cookie != nil {
			h.cookie.ClearSessionCookies(w)
		}
		httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeUnauthenticated, httpserver.MsgUnauthenticated)

	default:
		h.lg.ErrorContext(r.Context(), "falha ao montar /me",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("user_id", ident.UserID),
			slog.String("reason", err.Error()),
		)
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
	}
}
