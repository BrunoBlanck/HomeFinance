package cardstatement

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
)

// MaxAPIPageSize é o teto de `limit` das linhas da fatura, igual ao de
// GET /transactions: um só cursor e um só teto no app inteiro.
const MaxAPIPageSize = 100

// StatementLines é o que a página de detalhe precisa do domínio de lançamentos.
//
// Interface no consumidor, implementada por transaction.StatementLines. O
// retorno é []any porque este pacote não conhece — e não deve conhecer — o DTO
// de lançamento: ele só o repassa para o JSON. Espelhar aqui a struct de vinte
// campos do schema Transaction colocaria o mesmo contrato em dois lugares, e
// eles divergiriam na primeira coluna nova.
type StatementLines interface {
	ByStatement(ctx context.Context, householdID, statementID, cursor string, limit int) ([]any, *string, error)
}

// DetailView é o schema CardStatementDetail: a fatura e as linhas dela.
type DetailView struct {
	Statement View  `json:"statement"`
	Items     []any `json:"items"`

	// NextCursor é nulo quando não há mais página, e está SEMPRE presente para
	// a tela não precisar distinguir "ausente" de "acabou".
	NextCursor *string `json:"nextCursor"`
}

// Handler expõe os endpoints de fatura. Somente LEITURA: no v1 a fatura nasce e
// morre com a importação (§5.2 da spec 0004), e superfície que não existe não
// precisa ser revisada.
type Handler struct {
	svc            *Service
	lines          StatementLines
	lg             *slog.Logger
	trustedProxies int
}

// NewHandler monta o handler.
func NewHandler(svc *Service, lines StatementLines, lg *slog.Logger, trustedProxyCount int) *Handler {
	return &Handler{svc: svc, lines: lines, lg: lg, trustedProxies: trustedProxyCount}
}

// List responde GET /api/v1/card-statements.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	view, err := h.svc.List(r.Context(), ator, ListInput{
		AccountID:       q.Get("accountId"),
		CompetenceMonth: q.Get("month"),
	})
	if err != nil {
		h.fail(w, r, err, "listando faturas")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Get responde GET /api/v1/card-statements/{id}, com os lançamentos da fatura.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	limite, err := pageLimit(r.URL.Query().Get("limit"))
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{
			"limit": "Informe um número inteiro de 1 a " + strconv.Itoa(MaxAPIPageSize) + ".",
		})
		return
	}

	statementID := r.PathValue("id")
	fatura, err := h.svc.ByID(r.Context(), ator, statementID)
	if err != nil {
		h.fail(w, r, err, "buscando fatura")
		return
	}

	detalhe := DetailView{Statement: fatura, Items: []any{}}
	if h.lines != nil {
		itens, proximo, err := h.lines.ByStatement(r.Context(), ator.HouseholdID, statementID,
			r.URL.Query().Get("cursor"), limite)
		if err != nil {
			h.fail(w, r, err, "listando linhas da fatura")
			return
		}
		detalhe.Items, detalhe.NextCursor = itens, proximo
	}
	httpserver.WriteJSON(w, http.StatusOK, detalhe)
}

// pageLimit lê e valida `limit`. Ausente devolve zero (tamanho padrão);
// presente e fora da faixa é ERRO, nunca truncado em silêncio.
func pageLimit(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if n < 1 || n > MaxAPIPageSize {
		return 0, errors.New("limit fora da faixa")
	}
	return n, nil
}

// ator monta quem está agindo — casa e usuário do token, IP da borda.
func (h *Handler) ator(w http.ResponseWriter, r *http.Request) (Actor, bool) {
	ident, ok := session.FromContext(r.Context())
	if !ok || ident.HouseholdID == "" {
		httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeUnauthenticated, httpserver.MsgUnauthenticated)
		return Actor{}, false
	}
	return Actor{
		HouseholdID: ident.HouseholdID,
		UserID:      ident.UserID,
		IP:          httpserver.ClientIP(r, h.trustedProxies),
	}, true
}

// fail traduz erro de domínio para HTTP.
//
// O erro de lançamento (cursor inválido, por exemplo) chega aqui pela listagem
// das linhas; ele é tratado pelo texto genérico, sem detalhe, porque quem
// explica o cursor é GET /transactions.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, contexto string) {
	switch {
	case errors.Is(err, ErrNotFound):
		httpserver.WriteError(w, http.StatusNotFound, httpserver.CodeNotFound, httpserver.MsgNotFound)

	case errors.Is(err, ErrInvalidMonth):
		httpserver.WriteValidationError(w, map[string]string{
			"month": "Informe o mês no formato AAAA-MM.",
		})

	case errors.Is(err, ErrNotCreditCard):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"accountId": "Fatura só existe em conta de cartão de crédito."})

	case errors.Is(err, ErrAccountArchived):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"accountId": "Esta conta está arquivada. Desarquive-a para usá-la."})

	case IsValidationError(err):
		httpserver.WriteError(w, http.StatusBadRequest, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed)

	default:
		// Falha de verdade: o cliente recebe genérico, e o detalhe fica no log
		// — sem valor em centavos e sem descrição (S8).
		h.lg.ErrorContext(r.Context(), "falha em fatura",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("operacao", contexto),
			slog.String("reason", err.Error()),
		)
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
	}
}
