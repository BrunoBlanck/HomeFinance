package importer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/id"
	"github.com/brunorblanck/homefinance/backend/internal/importer/archive"
	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Tetos da borda (§5.3 da spec 0004).
const (
	// MaxHistoryBatches é o tamanho do histórico de GET /imports.
	MaxHistoryBatches = 50

	// MaxPreviewPage e DefaultPreviewPage são o teto e o padrão de `limit` na
	// revisão. Acima do teto é 400 — nunca truncado em silêncio.
	MaxPreviewPage     = 200
	DefaultPreviewPage = 100
)

// Handler expõe os endpoints de importação.
type Handler struct {
	svc            *Service
	lg             *slog.Logger
	trustedProxies int
}

// NewHandler monta o handler.
func NewHandler(svc *Service, lg *slog.Logger, trustedProxyCount int) *Handler {
	return &Handler{svc: svc, lg: lg, trustedProxies: trustedProxyCount}
}

// Create responde POST /api/v1/imports — a fase 1.
//
// ESTA ROTA NÃO CRIA LANÇAMENTO NENHUM. Ela extrai, detecta o formato,
// parseia, deduplica e grava um rascunho com prazo de 24 h. Só o confirm grava
// em transactions (ADR-024c).
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	form, err := ReadUploadForm(r)
	if err != nil {
		h.fail(w, r, err, "lendo o formulário de importação")
		return
	}
	// A senha morre no fim desta função, aconteça o que acontecer. Analyze
	// também a zera; zerar duas vezes não custa nada, e é o preço de nenhum
	// caminho de erro deixá-la viva na memória do processo.
	defer form.Zero()

	view, err := h.svc.Analyze(r.Context(), ator, AnalyzeInput{
		AccountID: form.AccountID,
		FileName:  form.FileName,
		Content:   form.Content,
		Password:  form.Password,
		FormatID:  form.FormatID,
	})
	if err != nil {
		h.fail(w, r, err, "analisando arquivo importado")
		return
	}
	httpserver.WriteJSON(w, http.StatusCreated, view)
}

// List responde GET /api/v1/imports.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}
	view, err := h.svc.ListBatches(r.Context(), ator, MaxHistoryBatches)
	if err != nil {
		h.fail(w, r, err, "listando lotes de importação")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Get responde GET /api/v1/imports/{id} — a revisão paginada.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	limite, err := inteiroNaFaixa(q.Get("limit"), 1, MaxPreviewPage, DefaultPreviewPage)
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{
			"limit": "Informe um número inteiro de 1 a " + strconv.Itoa(MaxPreviewPage) + ".",
		})
		return
	}
	// O cursor da revisão é o próprio `seq`, que já é único e estável dentro do
	// lote — e o lote já é escopado pela casa do token. Valor fora da faixa é
	// 400, nunca "começo do início".
	cursor, err := inteiroNaFaixa(q.Get("cursor"), 1, dedup.MaxFileRows, 0)
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{
			"cursor": "Cursor inválido.",
		})
		return
	}

	view, err := h.svc.Preview(r.Context(), ator, r.PathValue("id"), cursor, limite)
	if err != nil {
		h.fail(w, r, err, "abrindo lote de importação")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// Delete responde DELETE /api/v1/imports/{id}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}
	if err := h.svc.Discard(r.Context(), ator, r.PathValue("id")); err != nil {
		h.fail(w, r, err, "descartando lote de importação")
		return
	}
	httpserver.WriteNoContent(w)
}

// confirmRequest é o corpo de POST /imports/{id}/confirm.
//
// DTO explícito, decodificado com DisallowUnknownFields (S2): `householdId`,
// `dedupKey`, `dedupOrdinal`, `amountCents`, `occurredOn`, `description` e
// `kind` não existem aqui, e mandá-los é 400 — não "ignorado". Valor, data,
// descrição e tipo vêm do staging, sempre.
type confirmRequest struct {
	// Decisions é ponteiro para distinguir "mandei lista vazia" (legítimo:
	// aceito todos os defaults) de "esqueci o campo" — que o contrato declara
	// obrigatório.
	Decisions *[]decisionRequest `json:"decisions"`
	Statement *statementRequest  `json:"statement"`

	// DefaultCategoryID é aplicado a toda linha importada sem categoria
	// própria. Nulo deixa as linhas SEM categoria, que é o estado normal de um
	// lançamento importado (D3).
	DefaultCategoryID *string `json:"defaultCategoryId"`
}

// decisionRequest é UMA decisão do corpo. Só estes cinco campos existem
// (ImportDecision é `additionalProperties: false`): em particular NÃO existe
// `matchTransactionId` — a perna do `link` é a que a análise gravou no
// staging, e mandá-la aqui é 400 pelo DisallowUnknownFields.
type decisionRequest struct {
	RowID  string `json:"rowId"`
	Action string `json:"action"`

	// CategoryID é TRI-ESTADO (spec 0005 §4.2.3): ausente usa a sugestão da
	// análise; nulo explícito grava SEM categoria; valor usa este.
	CategoryID optionalString `json:"categoryId"`

	CounterpartAccountID string `json:"counterpartAccountId"`
	StatementID          string `json:"statementId"`
}

// optionalString distingue os TRÊS estados de um campo anulável: ausente, nulo
// e com valor (mesmo desenho de account.optionalDay).
//
// O `encoding/json` só chama UnmarshalJSON quando a chave EXISTE no corpo — é
// daí que sai o "veio no corpo". Sem isso, `*string` faria "use a sugestão" e
// "quero sem categoria" chegarem ao serviço como o mesmo nil, e limpar a
// sugestão de uma linha seria impossível pela API.
type optionalString struct {
	presente bool
	valor    *string
}

func (o *optionalString) UnmarshalJSON(b []byte) error {
	o.presente = true
	if string(b) == "null" {
		o.valor = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		// O erro de tipo vira 400 na borda, com a mensagem genérica do
		// contrato: o cliente recebe o endereço do problema, nunca a
		// estrutura interna.
		return err
	}
	o.valor = &s
	return nil
}

// paraDominio converte a forma da borda no tri-estado do domínio.
func (o optionalString) paraDominio() OptionalCategory {
	return OptionalCategory{Set: o.presente, ID: o.valor}
}

type statementRequest struct {
	CompetenceMonth string     `json:"competenceMonth"`
	ClosingDate     civil.Date `json:"closingDate"`
	DueDate         civil.Date `json:"dueDate"`
}

// Confirm responde POST /api/v1/imports/{id}/confirm — a fase 2.
func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	ator, ok := h.ator(w, r)
	if !ok {
		return
	}

	body, err := httpserver.DecodeJSON[confirmRequest](w, r)
	if err != nil {
		httpserver.WriteDecodeError(w, err)
		return
	}
	if body.Decisions == nil {
		httpserver.WriteValidationError(w, map[string]string{
			"decisions": "Informe a lista de decisões (vazia aceita todos os padrões).",
		})
		return
	}

	in := ConfirmInput{
		Decisions:         make([]Decision, 0, len(*body.Decisions)),
		DefaultCategoryID: body.DefaultCategoryID,
	}
	for _, d := range *body.Decisions {
		// `counterpartAccountId` é o ÚNICO id de conta que este corpo carrega,
		// e a FORMA dele é conferida aqui, na borda, antes de virar
		// `WHERE id = ?`. Vazio continua legítimo (a análise pode ter sugerido
		// a contraparte); presente e fora da forma canônica é 400 no campo,
		// com a mensagem da AÇÃO e sem eco nenhum do valor recusado (S8).
		//
		// O que isto fecha: o id com espaço à direita, que o MSSQL casa por
		// padding ANSI e que seria GRAVADO com o espaço na perna da
		// transferência — a partir daí a conta some de todo mapa em Go (o
		// recorte crédito/débito do relatório, o painel, o nome na listagem).
		// A caixa trocada não é fechada aqui, porque é forma canônica válida:
		// quem a fecha é a canonização de `canonizarContas`, no confirm.
		if d.CounterpartAccountID != "" && !id.IsCanonical(d.CounterpartAccountID) {
			httpserver.WriteValidationError(w, map[string]string{
				"counterpartAccountId": "Escolha a conta da outra perna.",
			})
			return
		}
		in.Decisions = append(in.Decisions, Decision{
			RowID:                d.RowID,
			Action:               d.Action,
			CategoryID:           d.CategoryID.paraDominio(),
			CounterpartAccountID: d.CounterpartAccountID,
			StatementID:          d.StatementID,
		})
	}
	if body.Statement != nil {
		in.Statement = &StatementConfirmation{
			CompetenceMonth: body.Statement.CompetenceMonth,
			ClosingDate:     body.Statement.ClosingDate,
			DueDate:         body.Statement.DueDate,
		}
	}

	view, err := h.svc.Confirm(r.Context(), ator, r.PathValue("id"), in)
	if err != nil {
		h.fail(w, r, err, "confirmando lote de importação")
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, view)
}

// inteiroNaFaixa lê um parâmetro inteiro da query.
//
// Ausente devolve o padrão. Presente e fora da faixa é ERRO, nunca reduzido em
// silêncio: um `limit=10000` aceito e cortado entregaria menos do que o cliente
// pediu sem dizer nada, e a tela concluiria que acabou a lista (S4).
func inteiroNaFaixa(raw string, minimo, maximo, padrao int) (int, error) {
	if raw == "" {
		return padrao, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	if n < minimo || n > maximo {
		return 0, errors.New("fora da faixa")
	}
	return n, nil
}

// ator monta quem está agindo — casa e usuário do TOKEN, IP da borda.
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
// ⚠️ Nenhum ramo ecoa conteúdo do arquivo, nome de terceiro ou — jamais — a
// senha do ZIP. O detalhe técnico vai só para o log, e o log não recebe valor
// em centavos nem descrição (S8).
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, contexto string) {
	// 1) Recurso de outra casa ou inexistente: MESMA resposta, byte a byte.
	if ehNaoEncontrado(err) {
		httpserver.WriteError(w, http.StatusNotFound, httpserver.CodeNotFound, httpserver.MsgNotFound)
		return
	}

	// 2) Os seis códigos próprios da importação, cada um mapeando para uma
	//    ação diferente da tela (§5.4).
	switch {
	case errors.Is(err, archive.ErrPasswordRequired):
		httpserver.WriteUnprocessable(w, httpserver.CodeImportPasswordRequired, httpserver.MsgImportFileLocked, nil)
		return

	case errors.Is(err, archive.ErrPasswordInvalid):
		httpserver.WriteUnprocessable(w, httpserver.CodeImportPasswordInvalid, httpserver.MsgImportFileUnlockFailed, nil)
		return

	case errors.Is(err, ErrFormatAmbiguous):
		// Os candidatos vão em fields.format para a tela montar o seletor. São
		// ids de parser do REGISTRO — vocabulário do servidor, nunca conteúdo
		// do arquivo.
		campos := map[string]string{}
		var ambiguo *AmbiguousFormatError
		if errors.As(err, &ambiguo) {
			campos["format"] = strings.Join(ambiguo.Candidates, ",")
		}
		httpserver.WriteUnprocessable(w, httpserver.CodeImportFormatAmbiguous, httpserver.MsgImportFormatAmbiguous, campos)
		return

	case errors.Is(err, ErrFormatNotAllowed):
		// Formato fora da allowlist do registro é entrada inválida do cliente,
		// e não "não sei ler este arquivo": 400, com o campo.
		httpserver.WriteValidationError(w, map[string]string{"format": "Formato não reconhecido."})
		return

	case errors.Is(err, ErrFormatUnknown):
		httpserver.WriteUnprocessable(w, httpserver.CodeImportFormatUnknown, httpserver.MsgImportFormatUnknown, nil)
		return

	case errors.Is(err, ErrTargetMismatch):
		// O código é o MESMO (a ação da tela continua "troque a conta"), mas o
		// `fields` carrega agora o MOTIVO estruturado, para o front guiar em vez
		// de travar. Só enums fechados vindos do arquivo do próprio usuário —
		// nunca PII. Ver targetMismatchError.
		httpserver.WriteUnprocessable(w, httpserver.CodeImportTargetMismatch,
			httpserver.MsgImportTargetMismatch, targetMismatchFields(err))
		return
	}

	// 3) Limites: um código só para todos, com `fields` dizendo QUAL estourou.
	if campo, mensagem, ok := limiteDoArquivo(err); ok {
		httpserver.WriteUnprocessable(w, httpserver.CodeImportFileRejected, httpserver.MsgImportFileRejected,
			map[string]string{campo: mensagem})
		return
	}

	// 4) Regra de negócio com campo: 422 VALIDATION_FAILED, o padrão já
	//    existente do projeto.
	if campo, mensagem, ok := regraDeNegocio(err); ok {
		httpserver.WriteUnprocessable(w, httpserver.CodeValidationFailed, httpserver.MsgValidationFailed,
			map[string]string{campo: mensagem})
		return
	}

	// 5) Forma do pedido: 400.
	if campo, mensagem, ok := formaDoPedido(err); ok {
		httpserver.WriteValidationError(w, map[string]string{campo: mensagem})
		return
	}

	switch {
	case errors.Is(err, ErrNotMultipart):
		httpserver.WriteError(w, http.StatusUnsupportedMediaType,
			httpserver.CodeUnsupportedMediaType, httpserver.MsgUnsupportedMediaType)
		return

	case errors.Is(err, ErrPayloadTooLarge):
		httpserver.WriteError(w, http.StatusRequestEntityTooLarge,
			httpserver.CodePayloadTooLarge, httpserver.MsgPayloadTooLarge)
		return

	case errors.Is(err, ErrMalformedForm), errors.Is(err, ErrTooManyParts):
		httpserver.WriteError(w, http.StatusBadRequest,
			httpserver.CodeValidationFailed, httpserver.MsgValidationFailed)
		return
	}

	// 6) O cliente foi embora no meio (aba fechada, app encerrado, rede caída).
	//
	//    Quem decide isto é o CONTEXTO DA REQUISIÇÃO, e jamais o erro. Com o
	//    embrulho do gormstore (platform/storage/ctxerr.go), qualquer falha de
	//    driver sob contexto morto casa `errors.Is(err, context.Canceled)`, e
	//    perguntar ao erro rebaixaria para INFO um defeito de servidor. O
	//    contexto da requisição, esse, o net/http só cancela quando a conexão
	//    cai de verdade — requisição viva nunca entra aqui, e um erro de
	//    servidor continua indo para ERROR logo abaixo.
	//
	//    INFO e não ERROR porque é comportamento NORMAL de cliente: uma linha
	//    de ERROR por aba fechada polui exatamente o log que existe para
	//    mostrar sinal de segurança. A falha continua registrada, com o mesmo
	//    `reason` do ramo de 500 — nada some do log, só muda de nível.
	//
	//    A resposta continua sendo o 500 genérico do enum fechado do contrato:
	//    ninguém a lê (a conexão já foi), e inventar um status fora do enum
	//    publicado seria pior do que escrever um que não chega a lugar nenhum.
	if errors.Is(r.Context().Err(), context.Canceled) {
		h.lg.InfoContext(r.Context(), "importação: cliente desistiu no meio",
			slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
			slog.String("operacao", contexto),
			slog.String("reason", err.Error()),
		)
		httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
		return
	}

	// 7) O resto é falha do SERVIDOR, inclusive erro de banco ocorrido com o
	//    contexto já morto: 500 genérico e uma linha de ERROR. Nenhum ramo
	//    acima pode roubar este log — foi o que aconteceu enquanto o prazo era
	//    deduzido do contexto, e é a razão do desenho de ErrAnalyzeTimeout.
	h.lg.ErrorContext(r.Context(), "falha em importação",
		slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
		slog.String("operacao", contexto),
		slog.String("reason", err.Error()),
	)
	httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
}

// targetMismatchFields extrai o motivo estruturado do erro de incompatibilidade
// para o `fields` da resposta.
//
// Devolve só as chaves com valor: `reason` sempre; `detectedInstitution`,
// `detectedDocKind` e `accountInstitution` quando fazem sentido (o erro só
// preenche enums reconhecidos, e accountInstitution só no wrong_institution).
// São todos valores fechados vindos do arquivo do próprio usuário — nunca PII,
// nunca nome de arquivo, nunca linha do documento.
//
// Um ErrTargetMismatch cru (sem enriquecimento, ex.: erro futuro que só embrulhe
// a sentinela) ainda responde o MESMO código, apenas sem os campos de guia — por
// isso o fallback devolve nil em vez de entrar em pânico.
func targetMismatchFields(err error) map[string]string {
	var tm *targetMismatchError
	if !errors.As(err, &tm) {
		return nil
	}
	campos := map[string]string{"reason": tm.Reason}
	if tm.DetectedInstitution != "" {
		campos["detectedInstitution"] = tm.DetectedInstitution
	}
	if tm.DetectedDocKind != "" {
		campos["detectedDocKind"] = tm.DetectedDocKind
	}
	if tm.AccountInstitution != "" {
		campos["accountInstitution"] = tm.AccountInstitution
	}
	return campos
}

// ehNaoEncontrado junta TODOS os caminhos que precisam responder 404 com o
// mesmo corpo.
//
// Lote de outra casa, conta de outra casa, categoria de outra casa, fatura de
// outra casa e conta da outra perna de outra casa caem todos aqui — é a defesa
// contra BOLA na borda, e a razão de a resposta ser idêntica à de id
// inexistente (S1). Distinguir confirmaria a existência do recurso alheio.
func ehNaoEncontrado(err error) bool {
	return errors.Is(err, ErrBatchNotFound) ||
		errors.Is(err, ErrAccountNotFound) ||
		errors.Is(err, transaction.ErrNotFound) ||
		errors.Is(err, cardstatement.ErrNotFound)
}

// limiteDoArquivo classifica os erros de LIMITE, que compartilham o código
// IMPORT_FILE_REJECTED.
//
// Um código só para todos, com `fields` dizendo qual limite estourou: seis
// códigos existem porque cada um leva a uma AÇÃO diferente da tela, e todos
// estes levam à mesma — "escolha outro arquivo, ou importe por período menor".
func limiteDoArquivo(err error) (campo, mensagem string, ok bool) {
	switch {
	// --- contêiner --------------------------------------------------------
	case errors.Is(err, archive.ErrTooLarge), errors.Is(err, csvtext.ErrTooLarge):
		return PartFile, "O arquivo passa do limite de 8 MB.", true
	case errors.Is(err, archive.ErrCompressionBomb):
		return PartFile, "A compressão deste arquivo não parece legítima.", true
	case errors.Is(err, archive.ErrNotSingleEntry), errors.Is(err, archive.ErrEntryNotCSV):
		return PartFile, "O ZIP precisa conter exatamente um arquivo .csv.", true
	case errors.Is(err, archive.ErrUnsafeName):
		return PartFile, "O nome do arquivo dentro do ZIP não é aceitável.", true
	case errors.Is(err, archive.ErrNestedArchive):
		return PartFile, "Não aceito arquivo compactado dentro de arquivo compactado.", true
	case errors.Is(err, archive.ErrEncryptionUnsupported):
		return PartFile, "Este tipo de proteção de ZIP ainda não é suportado.", true
	case errors.Is(err, archive.ErrInvalidArchive):
		return PartFile, "Não consegui ler este arquivo ZIP.", true

	// --- texto ------------------------------------------------------------
	case errors.Is(err, csvtext.ErrEmpty), errors.Is(err, ErrNoRows):
		return PartFile, "O arquivo não tem linhas de dados.", true
	case errors.Is(err, csvtext.ErrBinaryContent):
		return PartFile, "O arquivo não parece um CSV de texto.", true
	case errors.Is(err, csvtext.ErrUnsupportedEncoding):
		return PartFile, "A codificação deste arquivo não é suportada.", true
	case errors.Is(err, csvtext.ErrNoSeparator), errors.Is(err, csvtext.ErrMalformed),
		errors.Is(err, csvtext.ErrDuplicateHeader):
		return PartFile, "Não consegui interpretar as colunas deste arquivo.", true
	case errors.Is(err, csvtext.ErrLineTooLong):
		return PartFile, "Há uma linha longa demais neste arquivo.", true
	case errors.Is(err, csvtext.ErrTooManyColumns):
		return PartFile, "Há colunas demais neste arquivo.", true

	// --- volume -----------------------------------------------------------
	case errors.Is(err, ErrTooManyRows), errors.Is(err, csvtext.ErrTooManyRows),
		errors.Is(err, dedup.ErrTooManyRows):
		return "rows", "O arquivo tem linhas demais. Importe por período menor.", true
	case errors.Is(err, ErrClassifyTooCostly):
		// Achado A1: não é o número de linhas que estourou, é o produto
		// palavras-chave × descrições. A orientação útil é a mesma (arquivo
		// menor) e o campo continua sendo `rows` — nenhuma palavra-chave,
		// contagem ou descrição aparece na mensagem.
		return "rows", "Não consegui classificar este arquivo com as palavras-chave da casa. Importe por período menor.", true
	case errors.Is(err, ErrTooManyRejected):
		return "rows", "Muitas linhas deste arquivo não puderam ser lidas. Confira se é o arquivo certo.", true
	case errors.Is(err, ErrDateSpanTooWide):
		return "window", "O período coberto pelo arquivo é longo demais. Importe por período menor.", true
	case errors.Is(err, dedup.ErrWindowTooLarge):
		return "window", "Há lançamentos demais no período deste arquivo. Importe por período menor.", true

	// --- prazo ------------------------------------------------------------
	case errors.Is(err, ErrAnalyzeTimeout):
		// A análise tem 15 s e não os cumpriu: é limite de trabalho, não falha
		// do servidor, e a orientação é a mesma — arquivo menor.
		//
		// ⚠️ A condição é a SENTINELA DO DOMÍNIO, e nunca
		// `context.DeadlineExceeded`. O gormstore soma o motivo do contexto ao
		// erro do driver (platform/storage/ctxerr.go), então, com o contexto
		// morto, QUALQUER falha de banco casa o erro de contexto: a versão
		// antiga desta linha respondia "o seu arquivo demorou demais" para
		// falhas do SERVIDOR e, de quebra, as fazia sumir do log de ERROR —
		// que é o único registro delas. Quem sabe que o prazo venceu é quem o
		// impôs (conferirPrazo, em analyze.go), e é de lá que a sentinela vem.
		return PartFile, "A análise deste arquivo demorou demais. Importe por período menor.", true

	default:
		return "", "", false
	}
}

// regraDeNegocio classifica o que é 422 VALIDATION_FAILED com campo.
func regraDeNegocio(err error) (campo, mensagem string, ok bool) {
	switch {
	case errors.Is(err, ErrCounterpartArchived):
		// A conta arquivada é a da OUTRA perna do `transfer` — a única das
		// duas que está no corpo do confirm (spec 0005 §12). Antes do caso
		// geral de propósito: `accountId` mandaria desarquivar a conta errada.
		return "counterpartAccountId", "A conta da outra perna está arquivada. Desarquive-a para usá-la.", true

	case errors.Is(err, ErrAccountArchived), errors.Is(err, transaction.ErrAccountArchived),
		errors.Is(err, cardstatement.ErrAccountArchived):
		return PartAccountID, "Esta conta está arquivada. Desarquive-a para usá-la.", true

	case errors.Is(err, cardstatement.ErrNotCreditCard):
		return PartAccountID, "Fatura só existe em conta de cartão de crédito.", true

	case errors.Is(err, transaction.ErrCategoryArchived):
		return "categoryId", "Esta categoria está arquivada. Desarquive-a para usá-la.", true

	case errors.Is(err, transaction.ErrCategoryKindMismatch):
		return "categoryId", "A natureza da categoria não combina com o lançamento.", true

	case errors.Is(err, transaction.ErrCategoryIsParentGroup):
		// §13 da spec 0005. Mesmo campo dos outros erros de categoria: a
		// categoria recusada é a da decisão — ou a do `defaultCategoryId`,
		// que chega ao lote pelo mesmo caminho.
		return "categoryId", "Este grupo tem subcategorias. Escolha uma subcategoria.", true

	case errors.Is(err, cardstatement.ErrInvalidDates), errors.Is(err, cardstatement.ErrCompetenceMismatch),
		errors.Is(err, cardstatement.ErrInvalidMonth):
		return "statement", "Confira a competência, o fechamento e o vencimento da fatura.", true

	case errors.Is(err, transaction.ErrStatementMismatch):
		return "statementId", "Esta fatura não pertence à conta informada.", true

	default:
		return "", "", false
	}
}

// formaDoPedido classifica o que é 400: o cliente montou o pedido errado.
func formaDoPedido(err error) (campo, mensagem string, ok bool) {
	switch {
	case errors.Is(err, ErrStatementRequired):
		return "statement", "Confirme a competência, o fechamento e o vencimento da fatura.", true
	case errors.Is(err, ErrStatementNotAllowed):
		return "statement", "Este lote não é de fatura.", true
	case errors.Is(err, ErrTooManyDecisions):
		return "decisions", "Decisões demais no mesmo pedido.", true
	case errors.Is(err, ErrDuplicateDecision):
		return "decisions", "A mesma linha foi citada mais de uma vez.", true
	case errors.Is(err, ErrRowNotInBatch):
		return "decisions", "Há uma decisão para uma linha que não é deste lote.", true
	case errors.Is(err, ErrActionNotAllowed):
		return "decisions", "Esta ação não está disponível para a linha escolhida.", true
	case errors.Is(err, ErrCounterpartRequired):
		return "counterpartAccountId", "Escolha a conta da outra perna da transferência.", true
	case errors.Is(err, ErrCounterpartNotAllowed), errors.Is(err, ErrSameAccountTransfer):
		return "counterpartAccountId", "Escolha uma conta diferente da conta do arquivo.", true
	case errors.Is(err, ErrStatementOnlyOnTransfer):
		return "statementId", "A fatura só pode ser informada ao registrar uma transferência.", true
	case errors.Is(err, ErrCategoryOnlyOnImport):
		return "categoryId", "A categoria só pode ser informada ao importar a linha.", true
	case errors.Is(err, transaction.ErrBrokenTransfer):
		return "counterpartAccountId", "Não consegui montar o par da transferência.", true
	default:
		var campoDoForm *FormFieldError
		if errors.As(err, &campoDoForm) {
			return campoDoForm.Field, campoDoForm.Msg, true
		}
		return "", "", false
	}
}
