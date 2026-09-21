package dashboard

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// msgMonthRepetido é a redação do 400 da chave `month` REPETIDA. Mesma forma
// da de `kindGroup` em GET /transactions ("Informe o tipo uma única vez.",
// spec 0004 §12.5.5): aponta a AÇÃO e só isso — não ecoa nenhum dos valores
// recebidos nem diz quantas ocorrências chegaram, porque contar para o cliente
// o que ele mandou é devolver a entrada dele pela porta do erro.
//
// Não é a redação do mês MALFORMADO ("Informe o mês no formato AAAA-MM."):
// quem mandou dois meses válidos precisa da ação certa, e não de uma instrução
// sobre um formato que já estava certo.
const msgMonthRepetido = "Informe o mês uma única vez."

// Handler expõe a rota do painel.
type Handler struct {
	svc *Service
	lg  *slog.Logger
}

// NewHandler monta o handler.
//
// Sem trustedProxyCount, como o do relatório e pelo mesmo motivo: o painel não
// audita e não tem limitador próprio (ADR-031e), então o IP do cliente não é
// lido aqui — e o que não é lido não pode ser lido errado atrás de um proxy.
func NewHandler(svc *Service, lg *slog.Logger) *Handler {
	if lg == nil {
		lg = slog.Default()
	}
	return &Handler{svc: svc, lg: lg}
}

// Summary responde GET /api/v1/dashboard (spec 0008, ADR-031).
//
// UM parâmetro é lido: `month`. Qualquer outro — `householdId=`, `accountId=`,
// `categoryId=` — é ignorado sem efeito, porque a casa vem do TOKEN e o painel
// não filtra por mais nada (docs/SEGURANCA.md §2). Nada é normalizado:
// " 2026-09" e "2026-9" são recusados como vieram, para o cliente não aprender
// nada sobre o servidor pela recusa. `Cache-Control: no-store` é global
// (httpserver.SecurityHeaders).
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	// `month` é lido por httpserver.SoleQueryValue, e não por `Query().Get`.
	// O helper veio da E2d (spec 0004 §12.5.5, `kindGroup`), e o doc-comment
	// dele marca como gatilho de adoção "a próxima entrega que tocar a borda
	// HTTP de qualquer rota de leitura" — esta. `Get` devolve o PRIMEIRO valor
	// e descarta o resto em silêncio, então `?month=2026-09&month=2026-13`
	// respondia 200 com um mês que proxy, WAF e log podiam ler como outro
	// (HTTP Parameter Pollution). Chave repetida é URL ambígua: 400, sem
	// olhar se os valores concordam e sem consultar nada.
	//
	// A recusa NÃO é do mês malformado — essa continua no serviço, por
	// ParseMonth, e ausente continua chegando lá como "" (400 pela mesma
	// redação de sempre).
	month, err := httpserver.SoleQueryValue(r.URL.Query(), "month")
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{"month": msgMonthRepetido})
		return
	}

	view, err := h.svc.Summary(r.Context(), ator, SummaryInput{Month: month})
	if err != nil {
		h.fail(w, r, err, "resumo do painel")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// ator monta quem está pedindo: casa e usuário saem do contexto publicado pelo
// RequireAuth (ou seja, do token assinado). O household NUNCA vem de corpo,
// query ou path (docs/SEGURANCA.md §2).
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
// mensagem genérica e o detalhe apenas no log (docs/SEGURANCA.md §4). O log do
// 500 leva request_id, operação e a razão — NUNCA centavos, nome de conta ou
// de categoria, e nunca a consulta.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, contexto string) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeUnauthenticated, httpserver.MsgUnauthenticated)

	case errors.Is(err, transaction.ErrInvalidMonth):
		// A MESMA redação de GET /reports/by-category e de GET /investments:
		// três telas, um vocabulário, e nenhuma delas diz por que a string
		// exata não serviu.
		httpserver.WriteValidationError(w, map[string]string{
			"month": "Informe o mês no formato AAAA-MM.",
		})

	default:
		// transaction.ErrTooManyCategories cai AQUI de propósito (ADR-029 j.2),
		// e o repositório o devolve EMBRULHADO — por isso a comparação de
		// erros deste arquivo é sempre `errors.Is`, nunca `==`. O nome convida
		// ao engano: ele conta CATEGORIAS da própria casa, que já têm teto
		// próprio (200), então passar disso significa que a taxonomia foi
		// violada e o servidor não sabe mais o que é a taxonomia da casa.
		// Qualquer 4xx apontaria um campo que a pessoa não tem como corrigir
		// naquele pedido. 500 genérico, razão só no log.
		//
		// errTooManyRows e errTotalsOutOfRange caem aqui pelo mesmo caminho e
		// pelo mesmo motivo: falha FECHADA, com as contagens no log e nenhum
		// número inventado na resposta.
		h.lg.ErrorContext(r.Context(), "falha no painel",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("operacao", contexto),
			slog.String("reason", err.Error()),
		)
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
	}
}
