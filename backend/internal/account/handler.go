package account

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
)

// Mensagens dos 422 dos dias de fatura. Constantes porque as duas aparecem em
// dois campos cada uma, e mensagem repetida à mão é mensagem que um dia diverge.
const (
	MsgDiaSoEmCartao = "Só há fechamento e vencimento em conta de cartão de crédito."
	MsgDiaDoMes      = "Informe um dia do mês entre 1 e 31."
)

// Mensagens dos 400 de palavra-chave. Genéricas de propósito: nenhuma ecoa a
// palavra recusada — a resposta é do cliente, mas a mesma string não pode ter
// dois destinos, e o formato exato está no contrato.
const (
	MsgPalavraChaveInvalida = "Palavra-chave inválida: use de 2 a 40 caracteres (letras, números, espaço, & . - / ') com ao menos uma palavra útil."
	MsgPalavraChaveRepetida = "Palavra-chave repetida na lista."
	MsgPalavrasChaveDemais  = "Use no máximo 20 palavras-chave."
)

// Handler expõe os endpoints de conta.
type Handler struct {
	svc            *Service
	lg             *slog.Logger
	trustedProxies int
}

// NewHandler monta o handler.
//
// trustedProxyCount é a mesma configuração do resto da borda: sem ela, o IP
// registrado na auditoria seria o do balanceador, e o rastro perderia o valor.
func NewHandler(svc *Service, lg *slog.Logger, trustedProxyCount int) *Handler {
	return &Handler{svc: svc, lg: lg, trustedProxies: trustedProxyCount}
}

// createRequest é o corpo de POST /accounts.
//
// DTO explícito por endpoint (S2 do PLANOS.md). Repare no que NÃO existe aqui:
// householdId, id, createdAt, archivedAt, nameNorm. Como o decoder roda com
// DisallowUnknownFields, mandar qualquer um deles não é ignorado em silêncio —
// é 400. Mass assignment deixa de ser possível por construção, não por
// disciplina.
type createRequest struct {
	Name string `json:"name"`
	Kind string `json:"kind"`

	// Institution ausente vira `other` no serviço (é o que o contrato diz).
	// Ela precisa existir aqui: sem este campo não há caminho nenhum para
	// gravar a instituição, toda conta fica em `other`, e a trava de
	// consistência da importação — que compara o emissor DETECTADO com o
	// declarado na conta — nunca dispara.
	Institution string `json:"institution"`

	StatementClosingDay *int `json:"statementClosingDay"`
	StatementDueDay     *int `json:"statementDueDay"`

	OpeningBalanceCents int64      `json:"openingBalanceCents"`
	OpeningDate         civil.Date `json:"openingDate"`

	// Keywords ausente = lista vazia (contrato: "ausente = []").
	Keywords optionalKeywords `json:"keywords"`
}

// updateRequest é o corpo de PATCH /accounts/{id}. Ponteiro nulo = não mexer.
type updateRequest struct {
	Name        *string `json:"name"`
	Kind        *string `json:"kind"`
	Institution *string `json:"institution"`

	// Os dois dias são tri-estado (ver optionalDay): o contrato promete que
	// `null` explícito volta a "não configurado" e que o campo ausente não
	// mexe em nada.
	StatementClosingDay optionalDay `json:"statementClosingDay"`
	StatementDueDay     optionalDay `json:"statementDueDay"`

	OpeningBalanceCents *int64      `json:"openingBalanceCents"`
	OpeningDate         *civil.Date `json:"openingDate"`

	// Keywords é tri-estado (ver optionalKeywords): ausente não mexe,
	// presente substitui a lista inteira, `[]` limpa.
	Keywords optionalKeywords `json:"keywords"`
}

// optionalKeywords distingue "o campo não veio" de "veio uma lista" — o
// tri-estado do PATCH, pelo mesmo mecanismo de optionalDay: o encoding/json
// só chama UnmarshalJSON quando a chave EXISTE no corpo.
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

// optionalDay distingue os TRÊS estados de um campo anulável de PATCH: ausente,
// nulo e com valor.
//
// O `encoding/json` só chama UnmarshalJSON quando a chave EXISTE no corpo — é
// daí que sai o "veio no corpo". Sem isso, `*int` faria "não mexi" e "quero
// limpar" chegarem ao serviço como o mesmo nil, e limpar o dia de vencimento
// de um cartão seria impossível pela API.
type optionalDay struct {
	presente bool
	valor    *int
}

func (o *optionalDay) UnmarshalJSON(b []byte) error {
	o.presente = true
	if string(b) == "null" {
		o.valor = nil
		return nil
	}
	var dia int
	if err := json.Unmarshal(b, &dia); err != nil {
		// O erro de tipo vira 400 na borda, com a mensagem genérica do
		// contrato: o cliente recebe o endereço do problema, nunca a estrutura
		// interna.
		return err
	}
	o.valor = &dia
	return nil
}

// paraDominio converte a forma da borda no tri-estado do domínio.
func (o optionalDay) paraDominio() OptionalDay {
	return OptionalDay{Set: o.presente, Day: o.valor}
}

// List responde GET /api/v1/accounts.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	includeArchived, err := boolQuery(r, "includeArchived")
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{
			"includeArchived": "Use true ou false.",
		})
		return
	}

	view, err := h.svc.List(r.Context(), ator, includeArchived)
	if err != nil {
		h.fail(w, r, err, "listando contas")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Create responde POST /api/v1/accounts.
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
		Name:                body.Name,
		Kind:                body.Kind,
		Institution:         body.Institution,
		StatementClosingDay: body.StatementClosingDay,
		StatementDueDay:     body.StatementDueDay,
		OpeningBalanceCents: body.OpeningBalanceCents,
		OpeningDate:         body.OpeningDate,
		Keywords:            body.Keywords.paraCriacao(),
	})
	if err != nil {
		h.fail(w, r, err, "criando conta")
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, view)
}

// Get responde GET /api/v1/accounts/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}
	view, err := h.svc.Get(r.Context(), ator, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err, "buscando conta")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Update responde PATCH /api/v1/accounts/{id}.
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
		Name:                body.Name,
		Kind:                body.Kind,
		Institution:         body.Institution,
		StatementClosingDay: body.StatementClosingDay.paraDominio(),
		StatementDueDay:     body.StatementDueDay.paraDominio(),
		OpeningBalanceCents: body.OpeningBalanceCents,
		OpeningDate:         body.OpeningDate,
		Keywords:            body.Keywords.paraEdicao(),
	})
	if err != nil {
		h.fail(w, r, err, "atualizando conta")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Archive responde POST /api/v1/accounts/{id}/archive.
func (h *Handler) Archive(w http.ResponseWriter, r *http.Request) {
	h.mudarArquivamento(w, r, true)
}

// Unarchive responde POST /api/v1/accounts/{id}/unarchive.
func (h *Handler) Unarchive(w http.ResponseWriter, r *http.Request) {
	h.mudarArquivamento(w, r, false)
}

func (h *Handler) mudarArquivamento(w http.ResponseWriter, r *http.Request, arquivar bool) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	acao, contexto := h.svc.Unarchive, "desarquivando conta"
	if arquivar {
		acao, contexto = h.svc.Archive, "arquivando conta"
	}

	view, err := acao(r.Context(), ator, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err, contexto)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Delete responde DELETE /api/v1/accounts/{id}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), ator, r.PathValue("id")); err != nil {
		h.fail(w, r, err, "excluindo conta")
		return
	}
	httpserver.WriteNoContent(w)
}

// ator monta quem está agindo: casa e usuário saem do contexto publicado pelo
// RequireAuth (ou seja, do token assinado), e o IP sai da borda HTTP. O
// household NUNCA vem de corpo, query ou path (docs/SEGURANCA.md §2).
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

// boolQuery lê um parâmetro booleano da query.
//
// Ausente = false. Presente com valor que não seja booleano é ERRO, não
// "false": aceitar `?includeArchived=sim` silenciosamente faria a tela mostrar
// um conjunto diferente do pedido, sem ninguém notar (S4).
func boolQuery(r *http.Request, name string) (bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return false, nil
	}
	return strconv.ParseBool(raw)
}

// fail traduz erro de domínio para HTTP.
//
// Só os erros conhecidos viram resposta específica; qualquer outro é 500 com
// mensagem genérica e o detalhe apenas no log (docs/SEGURANCA.md §4). O log
// registra o id da conta, nunca o valor em centavos (§4.7 do PLANOS.md: log é
// superfície de vazamento, e dinheiro não entra nele).
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, contexto string) {
	var itemInvalido *KeywordValidationError
	var palavraTomada *KeywordTakenError

	switch {
	case errors.Is(err, ErrNotFound):
		// 404, e não 403: 403 confirmaria que o recurso existe em outra casa.
		httpserver.WriteError(w, http.StatusNotFound, httpserver.CodeNotFound, httpserver.MsgNotFound)

	case errors.Is(err, ErrNameTaken):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"name": "Já existe uma conta com este nome."})

	case errors.Is(err, ErrTooMany):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"limit": "Você atingiu o limite de contas desta casa."})

	case errors.Is(err, ErrInUse):
		httpserver.WriteUnprocessable(w, httpserver.CodeResourceInUse, httpserver.MsgResourceInUse, nil)

	case errors.Is(err, ErrInvalidName):
		httpserver.WriteValidationError(w, map[string]string{"name": "Informe um nome de até 80 caracteres."})

	case errors.Is(err, ErrInvalidKind):
		httpserver.WriteValidationError(w, map[string]string{"kind": "Tipo de conta inválido."})

	// Os três abaixo são 422, e não 400: o corpo está bem formado e dentro do
	// contrato — o que foi recusado é a REGRA (a allowlist de instituições, a
	// faixa do dia, e o fato de dia de fatura só existir em cartão). O mapa de
	// campos vai junto para o formulário destacar o campo certo.
	case errors.Is(err, ErrInvalidInstitution):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{"institution": "Instituição inválida."})

	case errors.Is(err, ErrStatementDayNotAllowed):
		// Os DOIS campos entram no mapa porque a regra é sobre o par: qual dos
		// dois veio preenchido é detalhe, e a correção (limpar os dois, ou
		// mudar o tipo da conta) é a mesma.
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{
				"statementClosingDay": MsgDiaSoEmCartao,
				"statementDueDay":     MsgDiaSoEmCartao,
			})

	case errors.Is(err, ErrInvalidStatementDay):
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{
				"statementClosingDay": MsgDiaDoMes,
				"statementDueDay":     MsgDiaDoMes,
			})

	case errors.Is(err, ErrInvalidAmount):
		httpserver.WriteValidationError(w, map[string]string{"openingBalanceCents": "Valor fora da faixa permitida."})

	case errors.Is(err, ErrInvalidDate):
		httpserver.WriteValidationError(w, map[string]string{"openingDate": "Informe uma data válida no formato AAAA-MM-DD."})

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

	case errors.As(err, &palavraTomada):
		// 409 KEYWORD_TAKEN. `fields.keyword` é obrigatório no contrato e é
		// a palavra que o próprio cliente enviou; `ownerId` só entra quando a
		// dona é conhecida — ela é SEMPRE uma conta desta casa (a consulta
		// filtra por household_id), e fica de fora na corrida tripla em que
		// o serviço não consegue identificá-la (escreverComPalavras). Um
		// `ownerId` vazio violaria o formato uuid do contrato.
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
		h.lg.ErrorContext(r.Context(), "falha em conta",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("operacao", contexto),
			slog.String("reason", err.Error()),
		)
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
	}
}
