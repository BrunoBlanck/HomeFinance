package aiprompt

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Redações do 400. São constantes para que dois caminhos de erro produzam
// respostas byte a byte idênticas (§3.12 da spec 0001), e nenhuma delas ecoa o
// valor recebido: quem mandou a entrada já a tem, e devolvê-la pela porta do
// erro é devolver entrada de terceiro.
const (
	// msgMesInvalido é a MESMA redação do painel, do relatório e da listagem —
	// quatro telas, um vocabulário. Ela cobre ausente, vazio e malformado, e
	// não diz por que a string exata não serviu.
	msgMesInvalido = "Informe o mês no formato AAAA-MM."

	// msgFromRepetido e msgToRepetido apontam a AÇÃO e só isso. Não são a
	// redação do mês malformado: quem mandou dois meses VÁLIDOS precisa da
	// ação certa, e não de uma instrução sobre um formato que já estava certo.
	msgFromRepetido = "Informe o mês inicial uma única vez."
	msgToRepetido   = "Informe o mês final uma única vez."

	// msgJanelaInvertida é uma das duas recusas que sobram depois de os dois
	// meses estarem bem formados (a outra, msgJanelaLonga, é montada logo
	// abaixo a partir do teto do domínio). As duas apontam `toMonth`, como o
	// contrato manda.
	msgJanelaInvertida = "O mês final não pode ser anterior ao inicial."

	// msgDescricoesDemais é o 422: o pedido está correto, o período é que não
	// cabe num prompt só. Sem contagem na mensagem — o teto é do servidor e a
	// contagem do período é dado da casa.
	msgDescricoesDemais = "Este período tem descrições demais para um prompt só. Tente um período menor."
)

// msgJanelaLonga nasce do teto do domínio, e não de um número digitado aqui:
// mudar transaction.MaxCompetenceMonthsInWindow muda a mensagem junto.
var msgJanelaLonga = fmt.Sprintf("A janela é de no máximo %d meses de competência.",
	transaction.MaxCompetenceMonthsInWindow)

// Handler expõe a rota de exportação do menu IA.
type Handler struct {
	svc *Service
	lg  *slog.Logger
}

// NewHandler monta o handler.
//
// Sem trustedProxyCount, como o do painel e o do relatório: esta rota não
// audita, então o IP do cliente não é lido aqui — e o que não é lido não pode
// ser lido errado atrás de um proxy. O limitador desta rota é POR CASA
// (config.AiExport), com a chave publicada pelo RequireAuth, e mora na tabela
// de rotas.
func NewHandler(svc *Service, lg *slog.Logger) *Handler {
	if lg == nil {
		lg = slog.Default()
	}
	return &Handler{svc: svc, lg: lg}
}

// ExportPrompt responde GET /api/v1/ai/export-prompt (spec 0010 §3, E9a).
//
// DOIS parâmetros são lidos, e mais nenhum: `fromMonth` e `toMonth`. Não há
// `householdId`, `accountId` nem `categoryId` — a casa vem do TOKEN e a rota
// não filtra por recurso (docs/SEGURANCA.md §2). Um `?householdId=` na query é
// ignorado sem efeito, porque nada aqui o lê.
//
// Os dois são lidos com httpserver.SoleQueryValue, e não com `Query().Get`.
// `Get` devolve a PRIMEIRA ocorrência e descarta o resto em silêncio, então
// `?fromMonth=2026-07&fromMonth=2026-01` responderia 200 com uma janela que
// proxy, WAF e log podem ler como outra (HTTP Parameter Pollution). Chave
// repetida é URL ambígua: 400, sem olhar se os valores concordam e sem
// consultar nada. Esta rota nasce assim — a adoção parcial do helper é dívida
// conhecida das rotas antigas, e dívida não se herda em rota nova.
//
// `Cache-Control: no-store` é global (httpserver.SecurityHeaders), o que
// importa aqui mais do que na média: a resposta é um texto com as descrições e
// os valores das movimentações da casa.
func (h *Handler) ExportPrompt(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	from, err := httpserver.SoleQueryValue(q, "fromMonth")
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{"fromMonth": msgFromRepetido})
		return
	}
	to, err := httpserver.SoleQueryValue(q, "toMonth")
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{"toMonth": msgToRepetido})
		return
	}

	view, err := h.svc.ExportPrompt(r.Context(), ator, ExportInput{FromMonth: from, ToMonth: to})
	if err != nil {
		h.fail(w, r, err, "prompt de exportação da IA")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// ator monta quem está pedindo: casa e usuário saem do contexto publicado pelo
// RequireAuth — ou seja, do token assinado. O household NUNCA vem de corpo,
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
// NENHUM código de erro novo (D4 da spec 0003): as quatro recusas de janela
// são 400 `VALIDATION_FAILED` com o campo apontado, e o estouro da agregação é
// 422 com o MESMO código — a ação da tela é a mesma de qualquer entrada
// inválida, e só o status distingue "corrija o valor" de "o período não cabe".
// Qualquer outro erro é 500 genérico, com o detalhe apenas no log.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, contexto string) {
	switch {
	case errors.Is(err, ErrUnauthenticated):
		httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeUnauthenticated, httpserver.MsgUnauthenticated)

	case errors.Is(err, ErrInvalidFromMonth):
		httpserver.WriteValidationError(w, map[string]string{"fromMonth": msgMesInvalido})

	case errors.Is(err, ErrInvalidToMonth):
		httpserver.WriteValidationError(w, map[string]string{"toMonth": msgMesInvalido})

	case errors.Is(err, ErrWindowInverted):
		httpserver.WriteValidationError(w, map[string]string{"toMonth": msgJanelaInvertida})

	case errors.Is(err, ErrWindowTooLong):
		httpserver.WriteValidationError(w, map[string]string{"toMonth": msgJanelaLonga})

	case errors.Is(err, transaction.ErrTooManyDescriptionGroups):
		// 422, e não 400: o pedido está bem formado, mas a janela não cabe num
		// prompt só. E nunca uma resposta PARCIAL — o repositório é
		// tudo-ou-nada de propósito, porque um prompt que parece completo e não
		// é faria a IA propor palavra-chave para metade da casa sem ninguém
		// saber. A comparação é `errors.Is` porque o serviço embrulha o erro
		// com contexto.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"toMonth": msgDescricoesDemais})

	default:
		// errAgregacaoInconsistente cai aqui de propósito: é banco em estado
		// que a aplicação não produz, e falha FECHADA — 500 genérico, razão só
		// no log, nenhum número inventado no texto que a pessoa levaria para
		// fora. O log leva request_id, operação e razão — NUNCA descrição,
		// centavos, nome de conta ou de categoria.
		h.lg.ErrorContext(r.Context(), "falha ao montar o prompt do menu IA",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("operacao", contexto),
			slog.String("reason", err.Error()),
		)
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
	}
}
