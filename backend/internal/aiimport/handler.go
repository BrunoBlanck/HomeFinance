package aiimport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"unicode/utf8"

	"github.com/brunorblanck/homefinance/backend/internal/aiprompt"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Redações do 400/422. Constantes para que dois caminhos de erro produzam
// respostas byte a byte idênticas, e NENHUMA delas ecoa o que foi colado: o
// JSON veio de uma IA e passou por qualquer lugar; devolvê-lo pela porta do
// erro é renderizar texto de terceiro dentro do app.
const (
	// As quatro redações da janela são as MESMAS do export (aiprompt): uma
	// janela, um vocabulário.
	msgMesInvalido     = "Informe o mês no formato AAAA-MM."
	msgJanelaInvertida = "O mês final não pode ser anterior ao inicial."

	msgPayloadAusente = "Cole o JSON que a IA devolveu."
	msgVersaoInvalida = "Este JSON não está no formato de import do HomeFinance (versão 1)."
	msgListasVazias   = "O JSON não traz categoria nova nem palavra-chave para importar."
	msgListaLonga     = "Esta lista passa do limite de entradas."
	msgPalavrasDemais = "Esta entrada passa do limite de palavras-chave."
	msgNotasLongas    = "O campo de observações é longo demais."
	msgSkipLongo      = "Lista de categorias desmarcadas longa demais."

	msgDescricoesDemais = "Este período tem descrições demais para medir o impacto. Tente um período menor."
	msgMedicaoCara      = "Não consegui medir o impacto neste período com estas palavras-chave. Tente um período menor."
)

// msgJanelaLonga nasce do teto do domínio, e não de um número digitado aqui.
var msgJanelaLonga = fmt.Sprintf("A janela é de no máximo %d meses de competência.",
	transaction.MaxCompetenceMonthsInWindow)

// errNotesTooLong — `notes` acima de MaxNotesRunes. Sentinela própria para o
// handler apontar `fields.payload.notes` em vez do 400 genérico do
// decodificador.
var errNotesTooLong = errors.New("notes longo demais")

// Handler expõe as duas rotas do import de IA.
type Handler struct {
	svc            *Service
	lg             *slog.Logger
	trustedProxies int
}

// NewHandler monta o handler. COM trustedProxyCount, ao contrário do export:
// o confirm audita, e a auditoria leva o IP do cliente (lido atrás dos proxies
// confiáveis, como nas outras rotas que escrevem).
func NewHandler(svc *Service, lg *slog.Logger, trustedProxyCount int) *Handler {
	if lg == nil {
		lg = slog.Default()
	}
	return &Handler{svc: svc, lg: lg, trustedProxies: trustedProxyCount}
}

// --- corpo -------------------------------------------------------------------------

// envelopeRequest é o corpo das DUAS rotas (schema KeywordImportEnvelope),
// decodificado por httpserver.DecodeJSON: nome de campo na caixa exata, campo
// desconhecido é 400, um valor por corpo, teto de tokens.
//
// `payload` é ponteiro para distinguir "ausente" (400 em `fields.payload`) de
// "veio com forma errada" (400 apontado pelo decodificador).
type envelopeRequest struct {
	Payload           *payloadRequest `json:"payload"`
	FromMonth         string          `json:"fromMonth"`
	ToMonth           string          `json:"toMonth"`
	SkipNewCategories []string        `json:"skipNewCategories"`
}

// payloadRequest é o JSON da IA (schema KeywordImportPayload). Conjunto
// FECHADO de campos: `additionalProperties: false` é feito valer pelo
// DisallowUnknownFields, e é o que torna 400 qualquer campo capaz de
// renomear, mover, arquivar ou excluir — porque ele não existe aqui.
type payloadRequest struct {
	Version          *int64                   `json:"homefinanceKeywordImport"`
	NewCategories    []newCategoryRequest     `json:"newCategories"`
	CategoryKeywords []categoryKeywordRequest `json:"categoryKeywords"`
	AccountKeywords  []accountKeywordRequest  `json:"accountKeywords"`

	// Notes é DESCARTADO no ato da decodificação: o tipo não tem campo onde
	// guardar o texto. Ver discardedNotes.
	Notes discardedNotes `json:"notes"`
}

type newCategoryRequest struct {
	Group string   `json:"group"`
	Name  string   `json:"name"`
	Kind  *string  `json:"kind"`
	Add   []string `json:"add"`
}

type categoryKeywordRequest struct {
	CategoryID   string   `json:"categoryId"`
	CategoryPath string   `json:"categoryPath"`
	Add          []string `json:"add"`
}

type accountKeywordRequest struct {
	AccountID   string   `json:"accountId"`
	AccountName string   `json:"accountName"`
	Add         []string `json:"add"`
}

// discardedNotes é o `notes` do payload: aceito para a IA ter onde despejar a
// explicação que sempre quer dar, e DESCARTADO — nunca gravado, nunca
// logado, nunca devolvido (spec 0010 §4.1, ADR-036 (c)).
//
// O tipo não tem campo nenhum, e é isso que fecha a superfície: o texto
// existe só dentro de UnmarshalJSON, onde a forma é conferida (tem de ser
// string ou null, e caber no teto), e morre no retorno. Não há como um
// caminho futuro "aproveitar" o conteúdo, porque ele não foi guardado.
type discardedNotes struct{}

func (*discardedNotes) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	var texto string
	if err := json.Unmarshal(b, &texto); err != nil {
		// O erro de tipo vira 400 na borda, com o campo apontado; o valor não
		// entra na mensagem.
		return err
	}
	if utf8.RuneCountInString(texto) > MaxNotesRunes {
		return errNotesTooLong
	}
	return nil
}

// --- rotas -------------------------------------------------------------------------

// Preview responde POST /api/v1/ai/keyword-import/preview.
func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) {
	h.atender(w, r, "prévia do import de IA", h.svc.Preview)
}

// Confirm responde POST /api/v1/ai/keyword-import/confirm.
func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	h.atender(w, r, "confirmação do import de IA", h.svc.Confirm)
}

// atender é o caminho comum das duas rotas: identidade do TOKEN, corpo
// decodificado com as defesas da borda, e a tradução de erro. As duas rotas
// leem o MESMO corpo — é o que torna literal "o confirm revalida tudo".
func (h *Handler) atender(w http.ResponseWriter, r *http.Request, contexto string,
	op func(context.Context, Actor, Input) (Report, error),
) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	body, err := httpserver.DecodeJSON[envelopeRequest](w, r)
	if err != nil {
		if errors.Is(err, errNotesTooLong) {
			httpserver.WriteValidationError(w, map[string]string{"payload.notes": msgNotasLongas})
			return
		}
		httpserver.WriteDecodeError(w, err)
		return
	}
	if body.Payload == nil {
		httpserver.WriteValidationError(w, map[string]string{"payload": msgPayloadAusente})
		return
	}

	view, err := op(r.Context(), ator, paraDominio(body))
	if err != nil {
		h.fail(w, r, err, contexto)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// paraDominio converte o corpo no Input do serviço. É cópia campo a campo de
// propósito: o serviço não conhece o tipo da borda, e `notes` não tem para
// onde ir.
func paraDominio(body envelopeRequest) Input {
	in := Input{
		FromMonth:         body.FromMonth,
		ToMonth:           body.ToMonth,
		SkipNewCategories: body.SkipNewCategories,
		Payload: Payload{
			Version:          body.Payload.Version,
			NewCategories:    make([]NewCategoryEntry, 0, len(body.Payload.NewCategories)),
			CategoryKeywords: make([]CategoryKeywordEntry, 0, len(body.Payload.CategoryKeywords)),
			AccountKeywords:  make([]AccountKeywordEntry, 0, len(body.Payload.AccountKeywords)),
		},
	}
	for _, e := range body.Payload.NewCategories {
		in.Payload.NewCategories = append(in.Payload.NewCategories, NewCategoryEntry{
			Group: e.Group, Name: e.Name, Kind: e.Kind, Add: e.Add,
		})
	}
	for _, e := range body.Payload.CategoryKeywords {
		in.Payload.CategoryKeywords = append(in.Payload.CategoryKeywords, CategoryKeywordEntry{
			CategoryID: e.CategoryID, CategoryPath: e.CategoryPath, Add: e.Add,
		})
	}
	for _, e := range body.Payload.AccountKeywords {
		in.Payload.AccountKeywords = append(in.Payload.AccountKeywords, AccountKeywordEntry{
			AccountID: e.AccountID, AccountName: e.AccountName, Add: e.Add,
		})
	}
	return in
}

// ator monta quem está agindo: casa e usuário do TOKEN (publicado pelo
// RequireAuth), IP da borda. O household NUNCA vem do corpo.
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
// Nenhum código de erro novo: forma é 400 `VALIDATION_FAILED` com o campo; a
// janela grande demais para medir é 422 com o MESMO código (a ação da tela é
// a de qualquer entrada inválida); o estado que mudou na confirmação é 409
// `CONFLICT`, sem campos. O resto é 500 genérico, com o detalhe só no log —
// e o log leva request_id, operação e razão, NUNCA palavra-chave, nome de
// categoria ou trecho do payload.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, contexto string) {
	var payloadErr *PayloadError
	switch {
	case errors.Is(err, ErrUnauthenticated):
		httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeUnauthenticated, httpserver.MsgUnauthenticated)

	case errors.Is(err, aiprompt.ErrInvalidFromMonth):
		httpserver.WriteValidationError(w, map[string]string{"fromMonth": msgMesInvalido})

	case errors.Is(err, aiprompt.ErrInvalidToMonth):
		httpserver.WriteValidationError(w, map[string]string{"toMonth": msgMesInvalido})

	case errors.Is(err, aiprompt.ErrWindowInverted):
		httpserver.WriteValidationError(w, map[string]string{"toMonth": msgJanelaInvertida})

	case errors.Is(err, aiprompt.ErrWindowTooLong):
		httpserver.WriteValidationError(w, map[string]string{"toMonth": msgJanelaLonga})

	case errors.As(err, &payloadErr):
		httpserver.WriteValidationError(w, map[string]string{payloadErr.Field: mensagemDeForma(payloadErr.Field)})

	case errors.Is(err, ErrConflict):
		// 409 sem campos (ADR-028d): não é falha do servidor, não vai para o
		// log de erro, e citar o que mudou seria vazar o estado de outra
		// requisição. A transação inteira foi desfeita.
		httpserver.WriteConflict(w, httpserver.CodeConflict, httpserver.MsgConflict, nil)

	case errors.Is(err, transaction.ErrTooManyDescriptionGroups):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"toMonth": msgDescricoesDemais})

	case errors.Is(err, textmatch.ErrWorkBudgetExceeded):
		// O orçamento de trabalho da medição estourou: 422, como todo outro
		// teto que chega ao matcher — nunca uma medição parcial.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"toMonth": msgMedicaoCara})

	default:
		if errors.Is(r.Context().Err(), context.Canceled) {
			// O cliente foi embora no meio: comportamento normal, INFO.
			h.lg.InfoContext(r.Context(), "import de IA: cliente desistiu no meio",
				slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
				slog.String("operacao", contexto),
				slog.String("reason", err.Error()),
			)
			httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
			return
		}
		h.lg.ErrorContext(r.Context(), "falha no import de IA",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("operacao", contexto),
			slog.String("reason", err.Error()),
		)
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
	}
}

// mensagemDeForma escolhe a redação pelo CAMPO apontado — o conteúdo do
// payload nunca entra.
func mensagemDeForma(campo string) string {
	switch {
	case campo == "payload":
		return msgListasVazias
	case campo == "payload.homefinanceKeywordImport":
		return msgVersaoInvalida
	case campo == "skipNewCategories":
		return msgSkipLongo
	case len(campo) > 4 && campo[len(campo)-4:] == ".add":
		return msgPalavrasDemais
	default:
		return msgListaLonga
	}
}
