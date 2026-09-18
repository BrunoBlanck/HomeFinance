package category

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
)

// Mensagens dos 400 de palavra-chave. Genéricas de propósito: nenhuma ecoa a
// palavra recusada — a resposta é do cliente, mas a mesma string não pode ter
// dois destinos, e o formato exato está no contrato.
const (
	MsgPalavraChaveInvalida = "Palavra-chave inválida: use de 2 a 40 caracteres (letras, números, espaço, & . - / ') com ao menos uma palavra útil."
	MsgPalavraChaveRepetida = "Palavra-chave repetida na lista."
	MsgPalavrasChaveDemais  = "Use no máximo 20 palavras-chave."
	// MsgPalavrasChaveNoGrupo é o 400 da spec 0005 §12: grupo com subcategoria
	// ativa não recebe palavra-chave.
	MsgPalavrasChaveNoGrupo = "Palavras-chave ficam nas subcategorias."
)

// Handler expõe os endpoints de categoria.
type Handler struct {
	svc            *Service
	lg             *slog.Logger
	trustedProxies int
}

// NewHandler monta o handler.
func NewHandler(svc *Service, lg *slog.Logger, trustedProxyCount int) *Handler {
	return &Handler{svc: svc, lg: lg, trustedProxies: trustedProxyCount}
}

// createRequest é o corpo de POST /categories.
//
// DTO explícito (S2). householdId, nameNorm e timestamps não existem aqui, e o
// decoder recusa campo desconhecido — mandar householdId é 400, não é ignorado.
//
// Kind é opcional porque a FOLHA herda a natureza do grupo: quando parentId
// vem preenchido, o servidor ignora o que o cliente mandou em kind e usa a do
// pai (invariante 4 da spec 0003).
type createRequest struct {
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	ParentID *string `json:"parentId"`

	// Keywords ausente = lista vazia (contrato: "ausente = []").
	Keywords optionalKeywords `json:"keywords"`
}

// updateRequest é o corpo de PATCH /categories/{id}.
//
// parentId NÃO existe aqui: mover categoria de grupo não é uma operação do v1
// (invariante 6). Tentar mandá-lo é 400 pelo DisallowUnknownFields.
type updateRequest struct {
	Name *string `json:"name"`
	Kind *string `json:"kind"`

	// Keywords é tri-estado (ver optionalKeywords): ausente não mexe,
	// presente substitui a lista inteira, `[]` limpa.
	Keywords optionalKeywords `json:"keywords"`
}

// optionalKeywords distingue "o campo não veio" de "veio uma lista" — o
// tri-estado do PATCH, pelo mesmo mecanismo de account.optionalDay: o
// encoding/json só chama UnmarshalJSON quando a chave EXISTE no corpo.
//
// `null` é RECUSADO (400): o contrato declara `type: array`, e aceitar null
// como "não mexe" faria quem tentou limpar a lista ver as palavras antigas
// voltarem — é `[]` que limpa. A recusa vira o 400 genérico de corpo
// malformado, sem detalhe da estrutura interna.
type optionalKeywords struct {
	presente bool
	itens    []string
}

func (o *optionalKeywords) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return errors.New("keywords não aceita null: use [] para limpar")
	}
	var itens []string
	if err := json.Unmarshal(b, &itens); err != nil {
		return err
	}
	o.presente, o.itens = true, itens
	return nil
}

// paraCriacao devolve a lista para o POST: ausente = vazia.
func (o optionalKeywords) paraCriacao() []string {
	if !o.presente {
		return []string{}
	}
	return o.itens
}

// paraEdicao devolve o tri-estado do PATCH: nulo = não mexe.
func (o optionalKeywords) paraEdicao() *[]string {
	if !o.presente {
		return nil
	}
	itens := o.itens
	if itens == nil {
		itens = []string{}
	}
	return &itens
}

// List responde GET /api/v1/categories.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	includeArchived, err := boolQuery(r, "includeArchived")
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{"includeArchived": "Use true ou false."})
		return
	}

	view, err := h.svc.List(r.Context(), ator, ListInput{
		Kind:            r.URL.Query().Get("kind"),
		IncludeArchived: includeArchived,
	})
	if err != nil {
		h.fail(w, r, err, "listando categorias")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Create responde POST /api/v1/categories.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	body, err := httpserver.DecodeJSON[createRequest](w, r)
	if err != nil {
		httpserver.WriteDecodeError(w, err)
		return
	}

	view, err := h.svc.Create(r.Context(), ator, CreateInput{
		Name:     body.Name,
		Kind:     body.Kind,
		ParentID: body.ParentID,
		Keywords: body.Keywords.paraCriacao(),
	})
	if err != nil {
		h.fail(w, r, err, "criando categoria")
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, view)
}

// Get responde GET /api/v1/categories/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}
	view, err := h.svc.Get(r.Context(), ator, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err, "buscando categoria")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Update responde PATCH /api/v1/categories/{id}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	body, err := httpserver.DecodeJSON[updateRequest](w, r)
	if err != nil {
		httpserver.WriteDecodeError(w, err)
		return
	}

	view, err := h.svc.Update(r.Context(), ator, r.PathValue("id"), UpdateInput{
		Name:     body.Name,
		Kind:     body.Kind,
		Keywords: body.Keywords.paraEdicao(),
	})
	if err != nil {
		h.fail(w, r, err, "atualizando categoria")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Archive responde POST /api/v1/categories/{id}/archive.
func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	h.mudarArquivamento(w, r, true)
}

// Unarchive responde POST /api/v1/categories/{id}/unarchive.
func (h *Handler) Unarchive(w http.ResponseWriter, r *http.Request) {
	h.mudarArquivamento(w, r, false)
}

func (h *Handler) mudarArquivamento(w http.ResponseWriter, r *http.Request, arquivar bool) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	acao, contexto := h.svc.Unarchive, "desarquivando categoria"
	if arquivar {
		acao, contexto = h.svc.Archive, "arquivando categoria"
	}

	view, err := acao(r.Context(), ator, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err, contexto)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Delete responde DELETE /api/v1/categories/{id}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), ator, r.PathValue("id")); err != nil {
		h.fail(w, r, err, "excluindo categoria")
		return
	}
	httpserver.WriteNoContent(w)
}

// ator monta quem está agindo: casa e usuário do TOKEN, IP da borda HTTP.
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

func boolQuery(r *http.Request, name string) (bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return false, nil
	}
	return strconv.ParseBool(raw)
}

// fail traduz erro de domínio para HTTP. Detalhe interno só no log.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, contexto string) {
	var itemInvalido *KeywordValidationError
	var palavraTomada *KeywordTakenError

	switch {
	case errors.Is(err, ErrNotFound):
		// Vale também para parentId de outra casa: o pai é buscado na casa do
		// token, então ele "não existe" — 404, nunca 403.
		httpserver.WriteError(w, http.StatusNotFound, httpserver.CodeNotFound, httpserver.MsgNotFound)

	case errors.Is(err, ErrNameTaken):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"name": "Já existe uma categoria com este nome aqui."})

	case errors.Is(err, ErrTooMany):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"limit": "Você atingiu o limite de categorias desta casa."})

	case errors.Is(err, ErrTooDeep):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"parentId": "Categoria só tem dois níveis: escolha um grupo."})

	case errors.Is(err, ErrKindLocked):
		// 422 VALIDATION_FAILED em `fields.kind` — não existe código
		// `KIND_LOCKED` no enum fechado de ErrorCode, e inventar um agora
		// quebraria o contrato para todo cliente já escrito.
		//
		// A mensagem fala do LADO DO DINHEIRO porque a recusa agora é só
		// essa (ADR-029c): trocar entre despesa e aporte, ou entre receita e
		// resgate, passou a ser aceito mesmo com a categoria em uso.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"kind": "Não dá para trocar entre receita e despesa com a categoria em uso. Dentro do mesmo lado (despesa e aporte, receita e resgate) a troca é permitida — e em subcategoria, nunca."})

	case errors.Is(err, ErrParentArchived):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"parentId": "Desarquive o grupo antes de desarquivar esta categoria."})

	case errors.Is(err, ErrInUse):
		httpserver.WriteUnprocessable(w, httpserver.CodeResourceInUse, httpserver.MsgResourceInUse, nil)

	case errors.Is(err, ErrInvalidName):
		httpserver.WriteValidationError(w, map[string]string{"name": "Informe um nome de até 60 caracteres."})

	case errors.Is(err, ErrInvalidKind):
		httpserver.WriteValidationError(w, map[string]string{"kind": "Use income, expense, investment ou redemption."})

	case errors.Is(err, ErrParentImmutable):
		httpserver.WriteValidationError(w, map[string]string{"parentId": "Categoria não muda de grupo."})

	// --- palavras-chave (spec 0005 §4.1) -----------------------------------

	case errors.As(err, &itemInvalido):
		// O campo aponta o ITEM (`keywords[i]`), como o contrato promete; a
		// mensagem é genérica e não ecoa a palavra.
		msg := MsgPalavraChaveInvalida
		if errors.Is(err, ErrDuplicateKeyword) {
			msg = MsgPalavraChaveRepetida
		}
		httpserver.WriteValidationError(w, map[string]string{
			fmt.Sprintf("keywords[%d]", itemInvalido.Index): msg,
		})

	case errors.Is(err, ErrTooManyKeywords):
		httpserver.WriteValidationError(w, map[string]string{"keywords": MsgPalavrasChaveDemais})

	case errors.Is(err, ErrKeywordsOnGroupWithChildren):
		// Spec 0005 §12: o campo é a LISTA (`keywords`), e não um item — a
		// recusa é pelo lugar, não por uma palavra específica.
		httpserver.WriteValidationError(w, map[string]string{"keywords": MsgPalavrasChaveNoGrupo})

	case errors.As(err, &palavraTomada):
		// 409 KEYWORD_TAKEN. `fields.keyword` é obrigatório no contrato e é
		// a palavra que o próprio cliente enviou; `ownerId` só entra quando a
		// dona é conhecida — ela é SEMPRE uma categoria desta casa (a
		// consulta filtra por household_id), e fica de fora na corrida tripla
		// em que o serviço não consegue identificá-la (escreverComPalavras).
		// Um `ownerId` vazio violaria o formato uuid do contrato.
		campos := map[string]string{"keyword": palavraTomada.Keyword}
		if palavraTomada.OwnerID != "" {
			campos["ownerId"] = palavraTomada.OwnerID
		}
		httpserver.WriteConflict(w, httpserver.CodeKeywordTaken, httpserver.MsgKeywordTaken, campos)

	// ErrKeywordTaken CRU não tem case próprio, de propósito: o serviço
	// garante que toda colisão sai tipada, com a palavra (escreverComPalavras
	// re-tenta e completa). Um cru chegando aqui é bug — e cai no 500 abaixo,
	// com log (a mensagem não contém a palavra), em vez de virar um 409 sem
	// `fields`, que o contrato não admite.

	default:
		h.lg.ErrorContext(r.Context(), "falha em categoria",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("operacao", contexto),
			slog.String("reason", err.Error()),
		)
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
	}
}
