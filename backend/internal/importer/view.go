package importer

import (
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
)

// Este arquivo é o DTO da importação — a forma EXATA dos schemas ImportBatch,
// ImportRow, ImportPreview e ImportResult do backend/api/openapi.yaml, que são
// todos `additionalProperties: false`. Campo a mais aqui é divergência de
// contrato, não bônus.
//
// A entidade nunca é serializada (docs/SEGURANCA.md §4). Repare no que NÃO sai
// daqui: `householdId`, `dedupKey`, `descriptionNorm` e `createdBy`. A chave de
// deduplicação em especial NUNCA sai do servidor — ela é o mecanismo, e expô-la
// seria dar ao cliente material para tentar fabricar colisões.

// Ações de decisão da revisão (schema ImportDecisionAction).
const (
	// ActionImport grava a linha.
	ActionImport = "import"

	// ActionSkip a ignora.
	ActionSkip = "skip"

	// ActionTransfer é aceita em linha `pagamento_de_fatura` e
	// `transferencia_interna` e cria o PAR de transferência (ADR-016) dentro
	// da mesma transação.
	ActionTransfer = "transfer"

	// ActionLink só é aceita em linha `transferencia_ja_registrada` (spec 0005
	// §4.2.3, ADR-026f): NÃO cria lançamento — grava na perna existente (a
	// apontada por matchTransactionId, calculada pela análise; o corpo não
	// tem esse campo) a identidade de deduplicação e o lote desta linha, para
	// a reimportação cair em `duplicado_exato`.
	ActionLink = "link"
)

// DefaultActionFor devolve o que acontece com a linha se ela NÃO for citada no
// confirm.
//
// ⚠️ Derivado de dedup.Status.DefaultImports(), que é a FONTE ÚNICA da
// taxonomia (§4.6 da spec 0004). Uma segunda tabela de defaults aqui
// divergiria da primeira no dia em que um status novo entrasse — e divergir
// nesta tabela quer dizer importar uma linha que o usuário mandou barrar.
//
// A única exceção nomeada é `transferencia_ja_registrada`: o default é `link`
// (spec 0005 §4.2.2), que não importa nada — não cria movimento, só vincula a
// linha à perna que já existe. Continua verdade que nenhuma linha barrada
// ENTRA sem decisão explícita.
func DefaultActionFor(s dedup.Status) string {
	if s == dedup.StatusTransferAlreadyRegistered {
		return ActionLink
	}
	if s.DefaultImports() {
		return ActionImport
	}
	return ActionSkip
}

// AllowedActionsFor devolve as ações oferecidas para a linha.
//
// ⚠️ Derivado de dedup.Status.Releasable(), pelo mesmo motivo de
// DefaultActionFor. Os dois "vazio" — `duplicado_exato` e `rejeitado` — têm
// motivos diferentes e estão explicados em dedup/status.go: um porque o índice
// único recusaria o INSERT de qualquer jeito, o outro porque não há o que
// inserir.
//
// Decisão com ação fora desta lista é 400, nunca "ignorada em silêncio".
func AllowedActionsFor(s dedup.Status) []string {
	switch {
	case s == dedup.StatusCardPayment:
		// A liberação oferecida é justamente a que resolve o caso: registrar
		// como transferência para a conta do cartão (ADR-016).
		return []string{ActionImport, ActionSkip, ActionTransfer}
	case s == dedup.StatusInternalTransfer:
		// O par é a liberação natural; `import` entra como receita/despesa
		// comum (com categoria), para o caso de a palavra-chave ter errado.
		return []string{ActionTransfer, ActionImport, ActionSkip}
	case s == dedup.StatusTransferAlreadyRegistered:
		// `import` NÃO é oferecido: seria a duplicata que a spec 0004 existe
		// para impedir. `link` vincula à perna existente sem criar movimento.
		return []string{ActionLink, ActionSkip}
	case s.DefaultImports(), s.Releasable():
		return []string{ActionImport, ActionSkip}
	default:
		return []string{}
	}
}

// ValidAction informa se a ação pertence à allowlist do contrato.
func ValidAction(a string) bool {
	switch a {
	case ActionImport, ActionSkip, ActionTransfer, ActionLink:
		return true
	default:
		return false
	}
}

// CountsView é o schema ImportRowCounts: uma entrada para CADA status, inclusive
// as zeradas, para a resposta ter forma estável.
type CountsView struct {
	New                       int `json:"novo"`
	RepeatedInFile            int `json:"repetido_no_arquivo"`
	DuplicateExact            int `json:"duplicado_exato"`
	DuplicateDeleted          int `json:"duplicado_excluido"`
	PossibleDup               int `json:"possivel_duplicado"`
	CardPayment               int `json:"pagamento_de_fatura"`
	InternalTransfer          int `json:"transferencia_interna"`
	TransferAlreadyRegistered int `json:"transferencia_ja_registrada"`
	Rejected                  int `json:"rejeitado"`
}

// countsFrom monta os contadores a partir do mapa status -> quantidade.
func countsFrom(m map[string]int) CountsView {
	return CountsView{
		New:                       m[string(dedup.StatusNew)],
		RepeatedInFile:            m[string(dedup.StatusRepeatedInFile)],
		DuplicateExact:            m[string(dedup.StatusDuplicateExact)],
		DuplicateDeleted:          m[string(dedup.StatusDuplicateDeleted)],
		PossibleDup:               m[string(dedup.StatusPossibleDuplicate)],
		CardPayment:               m[string(dedup.StatusCardPayment)],
		InternalTransfer:          m[string(dedup.StatusInternalTransfer)],
		TransferAlreadyRegistered: m[string(dedup.StatusTransferAlreadyRegistered)],
		Rejected:                  m[string(dedup.StatusRejected)],
	}
}

// OutcomeView é o schema ImportOutcome: o que o confirm fez de fato. Sobrevive
// ao lote como histórico, mesmo depois que as linhas de staging são apagadas.
type OutcomeView struct {
	Imported int `json:"imported"`
	Restored int `json:"restored"`
	Skipped  int `json:"skipped"`
	Blocked  int `json:"blocked"`
	Rejected int `json:"rejected"`

	// Linked conta as linhas vinculadas a uma perna que já existia (`link`).
	// Não criaram lançamento e não entram em Imported.
	Linked int `json:"linked"`
}

// StatementSuggestionView é o schema StatementSuggestion — SUGESTÃO, nunca
// decisão: o usuário confirma no passo de revisão.
type StatementSuggestionView struct {
	CompetenceMonth string     `json:"competenceMonth"`
	ClosingDate     civil.Date `json:"closingDate"`
	DueDate         civil.Date `json:"dueDate"`
}

// BatchView é o schema ImportBatch.
type BatchView struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	AccountID string `json:"accountId"`

	// Institution é a instituição DETECTADA no arquivo, não a da conta. Nunca
	// é "other": um lote só existe quando um parser reconheceu o cabeçalho.
	Institution string `json:"institution"`
	DocKind     string `json:"docKind"`
	FormatID    string `json:"formatId"`
	FileName    string `json:"fileName"`
	Encoding    string `json:"encoding"`

	RowCount int        `json:"rowCount"`
	MinDate  civil.Date `json:"minDate"`
	MaxDate  civil.Date `json:"maxDate"`

	// Counts existe enquanto o lote está pendente; é nulo depois que as linhas
	// de staging são apagadas (confirm ou descarte).
	Counts *CountsView `json:"counts"`

	// Outcome é o contrário: nulo antes do confirm, preenchido depois.
	Outcome *OutcomeView `json:"outcome"`

	StatementSuggestion *StatementSuggestionView `json:"statementSuggestion"`

	// SameContentImportedAt é AVISO, nunca bloqueio (ADR-025d).
	SameContentImportedAt *string `json:"sameContentImportedAt"`

	ExpiresAt   string  `json:"expiresAt"`
	CreatedAt   string  `json:"createdAt"`
	CommittedAt *string `json:"committedAt"`
}

// BatchListView é a resposta de GET /imports.
type BatchListView struct {
	Items []BatchView `json:"items"`
}

// RowView é o schema ImportRow: EXATAMENTE o que será gravado se a decisão for
// `import`. Não é a linha crua do arquivo, que não é guardada (§3.6).
type RowView struct {
	ID     string `json:"id"`
	Seq    int    `json:"seq"`
	LineNo int    `json:"lineNo"`
	Status string `json:"status"`

	DefaultAction  string   `json:"defaultAction"`
	AllowedActions []string `json:"allowedActions"`

	// Os quatro campos de domínio são nulos APENAS em linha `rejeitado`, que
	// por definição não produziu valores canônicos.
	Kind        *string     `json:"kind"`
	OccurredOn  *civil.Date `json:"occurredOn"`
	AmountCents *int64      `json:"amountCents"`
	Description *string     `json:"description"`

	ExternalID         *string `json:"externalId"`
	RejectReason       *string `json:"rejectReason"`
	MatchTransactionID *string `json:"matchTransactionId"`

	// --- spec 0005: sugestões por palavra-chave, SEMPRE presentes (nulas
	// quando não há). Calculadas no servidor; o cliente só devolve
	// `categoryId`/`counterpartAccountId` na decisão, que passam pela mesma
	// validação de casa dentro da transação (S1/S2).

	// SuggestedCategoryID é o default da linha no confirm: decisão ausente
	// grava esta; `categoryId: null` explícito grava sem categoria.
	SuggestedCategoryID *string `json:"suggestedCategoryId"`

	// MatchScore e MatchedKeyword descrevem a categoria quando há
	// SuggestedCategoryID, e a contraparte em linha `transferencia_*`. A tela
	// mostra como TEXTO ("88% · supermercado"); 100 não mostra pontuação.
	MatchScore     *int    `json:"matchScore"`
	MatchedKeyword *string `json:"matchedKeyword"`

	// SuggestedCounterpartAccountID é a conta da outra perna sugerida — nunca
	// a conta do lote, nunca conta arquivada. É o default de
	// `counterpartAccountId` na decisão `transfer`.
	SuggestedCounterpartAccountID *string `json:"suggestedCounterpartAccountId"`

	// MatchOccurredOn é a data do lançamento apontado por MatchTransactionID,
	// quando houver — é o que deixa a tela dizer "já registrada em 05/09".
	// Uma consulta por PÁGINA, nunca uma por linha.
	MatchOccurredOn *civil.Date `json:"matchOccurredOn"`
}

// PreviewView é a resposta de GET /imports/{id}.
type PreviewView struct {
	Batch BatchView `json:"batch"`
	Items []RowView `json:"items"`

	// NextCursor é o `seq` a partir do qual continuar; nulo quando acabou.
	NextCursor *int `json:"nextCursor"`
}

// BlockedRowView é o schema ImportBlockedRow: o motivo vai como CÓDIGO, nunca
// como texto em português.
type BlockedRowView struct {
	RowID  string `json:"rowId"`
	Reason string `json:"reason"`
}

// ResultView é o schema ImportResult — a resposta do confirm.
type ResultView struct {
	ID     string `json:"id"`
	Status string `json:"status"`

	Imported int `json:"imported"`
	Restored int `json:"restored"`
	Skipped  int `json:"skipped"`
	Blocked  int `json:"blocked"`
	Rejected int `json:"rejected"`

	// StatementID é a fatura criada ou REUTILIZADA; nulo em extrato.
	StatementID *string `json:"statementId"`

	// TransfersCreated conta PARES (cada par conta como 1).
	TransfersCreated int `json:"transfersCreated"`

	// Linked conta as linhas vinculadas a perna existente (`link`). Não
	// entram em Imported nem em TransfersCreated.
	Linked int `json:"linked"`

	BlockedRows []BlockedRowView `json:"blockedRows"`
}

// toBatchView monta o DTO do lote.
//
// counts vem de fora porque ele é uma CONSULTA (agregação sobre import_rows) e
// não um campo do lote: exigi-lo aqui faria toda listagem pagar uma agregação
// por linha da lista.
func toBatchView(b *Batch, counts *CountsView, sameContentAt *time.Time) BatchView {
	v := BatchView{
		ID:          b.ID,
		Status:      b.Status,
		AccountID:   b.AccountID,
		Institution: b.Institution,
		DocKind:     b.DocKind,
		FormatID:    b.FormatID,
		FileName:    b.FileName,
		Encoding:    b.Encoding,
		RowCount:    b.RowCount,
		MinDate:     b.MinDate,
		MaxDate:     b.MaxDate,
		Counts:      counts,
		ExpiresAt:   b.ExpiresAt.UTC().Format(time.RFC3339),
		CreatedAt:   b.CreatedAt.UTC().Format(time.RFC3339),
	}

	// Lote terminal publica o resultado; lote pendente ainda não tem um.
	if b.Status != BatchStatusPending {
		v.Outcome = &OutcomeView{
			Imported: b.ImportedCount,
			Restored: b.RestoredCount,
			Skipped:  b.SkippedCount,
			Blocked:  b.BlockedCount,
			Rejected: b.RejectedCount,
			Linked:   b.LinkedCount,
		}
	}

	if b.DocKind == string(DocKindCardStatement) && b.SuggestedCompetenceMonth != nil &&
		b.SuggestedClosingDate != nil && b.SuggestedDueDate != nil {
		v.StatementSuggestion = &StatementSuggestionView{
			CompetenceMonth: *b.SuggestedCompetenceMonth,
			ClosingDate:     *b.SuggestedClosingDate,
			DueDate:         *b.SuggestedDueDate,
		}
	}

	if sameContentAt != nil {
		s := sameContentAt.UTC().Format(time.RFC3339)
		v.SameContentImportedAt = &s
	}
	if b.CommittedAt != nil {
		s := b.CommittedAt.UTC().Format(time.RFC3339)
		v.CommittedAt = &s
	}
	return v
}

// toRowView monta o DTO da linha de staging.
//
// matchOccurredOn é o mapa id -> data dos lançamentos apontados pela PÁGINA
// (OccurredOnByIDs, uma consulta por página): a data só entra quando a linha
// aponta para alguém e o mapa a conhece. Id que o mapa não tem — outra casa,
// que o repositório não devolve — sai nulo, nunca inventado.
func toRowView(r *Row, matchOccurredOn map[string]civil.Date) RowView {
	status := dedup.Status(r.Status)
	v := RowView{
		ID:                 r.ID,
		Seq:                r.Seq,
		LineNo:             r.LineNo,
		Status:             r.Status,
		DefaultAction:      DefaultActionFor(status),
		AllowedActions:     AllowedActionsFor(status),
		ExternalID:         r.ExternalID,
		MatchTransactionID: r.MatchTransactionID,
	}

	if r.MatchTransactionID != nil {
		if d, ok := matchOccurredOn[*r.MatchTransactionID]; ok && !d.IsZero() {
			data := d
			v.MatchOccurredOn = &data
		}
	}

	if status == dedup.StatusRejected {
		// Linha rejeitada não tem valor canônico: os quatro campos de domínio
		// saem nulos, e o que sai é o CÓDIGO do motivo — nunca o conteúdo da
		// linha (§3.6 da spec 0004).
		if r.RejectReason != nil {
			motivo := PublicRejectReason(*r.RejectReason)
			v.RejectReason = &motivo
		}
		return v
	}

	kind := r.Kind
	ocorrido := r.OccurredOn
	valor := r.AmountCents
	descricao := r.Description
	v.Kind = &kind
	v.OccurredOn = &ocorrido
	v.AmountCents = &valor
	v.Description = &descricao

	// As sugestões são copiadas por VALOR: o DTO não compartilha ponteiro com
	// a entidade, e a entidade nunca é serializada (§4 de docs/SEGURANCA.md).
	v.SuggestedCategoryID = copiarTexto(r.SuggestedCategoryID)
	v.MatchScore = copiarInteiro(r.MatchScore)
	v.MatchedKeyword = copiarTexto(r.MatchedKeyword)
	v.SuggestedCounterpartAccountID = copiarTexto(r.SuggestedCounterpartAccountID)
	return v
}

func copiarTexto(p *string) *string {
	if p == nil {
		return nil
	}
	s := *p
	return &s
}

func copiarInteiro(p *int) *int {
	if p == nil {
		return nil
	}
	n := *p
	return &n
}

// PublicRejectReason traduz o código interno do parser para o vocabulário
// PUBLICADO no schema ImportRejectReason.
//
// Os dois conjuntos não são o mesmo, e isso é deliberado: o parser distingue
// mais casos do que a tela precisa (ele guarda "short_row" e
// "missing_external_id" separados, que é o que serve para investigar um arquivo
// novo), enquanto o contrato publica o conjunto pequeno de motivos que a tela
// sabe explicar. O detalhe fino fica em import_rows.reject_reason, para a
// forense; o que sai na API é a allowlist do contrato.
//
// Código desconhecido cai em `invalid_columns` em vez de vazar um valor fora da
// enum: resposta fora do contrato quebra o cliente tipado (ADR-015).
func PublicRejectReason(code string) string {
	switch code {
	case RejectInvalidDate:
		return "invalid_date"
	case RejectInvalidAmount, RejectZeroAmount:
		return "invalid_amount"
	case RejectShortRow:
		return "invalid_columns"
	default:
		return "invalid_columns"
	}
}
