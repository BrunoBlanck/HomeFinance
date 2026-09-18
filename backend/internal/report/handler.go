package report

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Handler expõe os relatórios.
type Handler struct {
	svc *Service
	lg  *slog.Logger
}

// NewHandler monta o handler. Sem trustedProxyCount: relatório não audita e
// não tem limitador próprio, então o IP do cliente não é lido aqui.
func NewHandler(svc *Service, lg *slog.Logger) *Handler {
	if lg == nil {
		lg = slog.Default()
	}
	return &Handler{svc: svc, lg: lg}
}

// ByCategory responde GET /api/v1/reports/by-category (ADR-027).
//
// Só dois parâmetros são lidos: `month` e `kind`. Qualquer outro
// (`householdId=`, `categoryId=`, …) é ignorado sem efeito — a casa vem do
// token e o relatório não filtra por categoria. Nada é normalizado: " 2026-09"
// e "EXPENSE" são recusados como vieram, para o cliente não aprender nada
// sobre o servidor pela recusa. `Cache-Control: no-store` é global
// (httpserver.SecurityHeaders).
func (h *Handler) ByCategory(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	view, err := h.svc.ByCategory(r.Context(), ator, ByCategoryInput{
		Month: q.Get("month"),
		Kind:  q.Get("kind"),
	})
	if err != nil {
		h.fail(w, r, err, "relatório por categoria")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// ator monta quem está pedindo: casa e usuário saem do contexto publicado
// pelo RequireAuth (ou seja, do token assinado). O household NUNCA vem de
// corpo, query ou path (docs/SEGURANCA.md §2).
func (h *Handler) ator(w http.ResponseWriter, r *http.Request) (Actor, bool) {
	ident, ok := session.FromContext(r.Context())
	if !ok || ident.HouseholdID == "" {
		httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeUnauthenticated, httpserver.MsgUnauthenticated)
		return Actor{}, false
	}
	return Actor{HouseholdID: ident.HouseholdID, UserID: ident.UserID}, true
}

// fail traduz erro de domínio para HTTP.
//
// Só os erros conhecidos viram resposta específica; qualquer outro é 500 com
// mensagem genérica e o detalhe apenas no log (docs/SEGURANCA.md §4). O log
// do 500 leva request_id, operação e a razão — NUNCA centavos, nome de
// categoria ou a query (S8).
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, contexto string) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeUnauthenticated, httpserver.MsgUnauthenticated)

	case errors.Is(err, transaction.ErrInvalidMonth):
		httpserver.WriteValidationError(w, map[string]string{
			"month": "Informe o mês no formato AAAA-MM.",
		})

	case errors.Is(err, ErrInvalidKind):
		httpserver.WriteValidationError(w, map[string]string{
			"kind": "Informe expense ou income.",
		})

	default:
		h.lg.ErrorContext(r.Context(), "falha em relatório",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("operacao", contexto),
			slog.String("reason", err.Error()),
		)
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
	}
}
