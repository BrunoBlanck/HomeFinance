package report

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Redações do 400 de chave REPETIDA na query — uma por parâmetro, no molde
// de `kindGroup` em GET /transactions ("Informe o tipo uma única vez.", spec
// 0004 §12.5.5) e de `month` em GET /dashboard: cada uma aponta a AÇÃO e só
// isso — não ecoa nenhum dos valores recebidos nem diz quantas ocorrências
// chegaram, porque contar para o cliente o que ele mandou é devolver a
// entrada dele pela porta do erro.
//
// Não são as redações do valor MALFORMADO ("Informe o mês no formato
// AAAA-MM.", "Informe expense ou income.", "Informe credit ou debit."): quem
// mandou dois valores VÁLIDOS precisa da ação certa, e não de uma instrução
// sobre um formato que já estava certo.
const (
	msgMonthRepetido        = "Informe o mês uma única vez."
	msgKindRepetido         = "Informe a natureza uma única vez."
	msgAccountGroupRepetido = "Informe o grupo de contas uma única vez."
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

// ByCategory responde GET /api/v1/reports/by-category (ADR-027, ADR-032).
//
// Só três parâmetros são lidos: `month`, `kind` e `accountGroup`. Qualquer
// outro (`householdId=`, `accountId=`, `categoryId=`, …) é ignorado sem
// efeito — a casa vem do token, o relatório não filtra por categoria e o
// recorte de contas nunca aceita id de conta: `credit`/`debit` são resolvidos
// no servidor sobre as contas da casa do token. Nada é normalizado:
// " 2026-09", "EXPENSE" e "CREDIT" são recusados como vieram, para o cliente
// não aprender nada sobre o servidor pela recusa. `Cache-Control: no-store` é
// global (httpserver.SecurityHeaders).
func (h *Handler) ByCategory(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	// Os três são lidos por httpserver.SoleQueryValue, e não por
	// `Query().Get`. `Get` devolve o PRIMEIRO valor e descarta o resto em
	// silêncio, então `?kind=expense&kind=income` respondia 200 com uma
	// natureza que proxy, WAF e log podiam ler como outra (HTTP Parameter
	// Pollution). Chave repetida é URL ambígua: 400 no campo repetido, sem
	// olhar se os valores concordam e sem consultar nada — inclusive quando
	// a segunda ocorrência é vazia, que é a pior das três (quem lê a última
	// recebe "sem recorte" e a tela mostra tudo sob o rótulo do recorte).
	//
	// Uma falha por resposta, na ordem month → kind → accountGroup. A recusa
	// NÃO é do valor malformado — essa continua no serviço, e ausente
	// continua chegando lá como "" (mês obrigatório; natureza `expense`;
	// recorte "todas as contas").
	q := r.URL.Query()
	month, err := httpserver.SoleQueryValue(q, "month")
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{"month": msgMonthRepetido})
		return
	}
	kind, err := httpserver.SoleQueryValue(q, "kind")
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{"kind": msgKindRepetido})
		return
	}
	accountGroup, err := httpserver.SoleQueryValue(q, "accountGroup")
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{"accountGroup": msgAccountGroupRepetido})
		return
	}

	view, err := h.svc.ByCategory(r.Context(), ator, ByCategoryInput{
		Month:        month,
		Kind:         kind,
		AccountGroup: accountGroup,
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

	// A MESMA mensagem para todo valor recusado ("CREDIT", "credit_card",
	// "'; DROP TABLE…"): a recusa é a lista dos dois valores aceitos, nunca o
	// que veio.
	case errors.Is(err, ErrInvalidAccountGroup):
		httpserver.WriteValidationError(w, map[string]string{
			"accountGroup": "Informe credit ou debit.",
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
