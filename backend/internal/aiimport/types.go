package aiimport

import (
	"context"
	"errors"
	"fmt"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Tetos do formato (spec 0010 §4.2, regra 2). Reconferidos AQUI, no serviço,
// porque schema não é validador em runtime: o `maxItems` do contrato descreve
// o formato, e é este código que o faz valer.
const (
	// MaxEntriesPerList é o teto de entradas de CADA uma das três listas do
	// payload (`newCategories`, `categoryKeywords`, `accountKeywords`).
	MaxEntriesPerList = 200

	// MaxKeywordsPerEntry é o teto de palavras do `add` de UMA entrada. É o
	// mesmo teto por dona (category.MaxKeywordsPerOwner): mandar mais do que
	// cabe numa dona é erro de forma, não recusa de linha.
	MaxKeywordsPerEntry = category.MaxKeywordsPerOwner

	// MaxSkipRefs é o teto de `skipNewCategories` — uma por entrada de
	// `newCategories`, no máximo.
	MaxSkipRefs = MaxEntriesPerList

	// MaxNotesRunes é o teto de `notes`. O campo é DESCARTADO no ato — nunca
	// gravado, logado ou devolvido —, mas continua sendo bytes que o servidor
	// parseia, e o contrato o limita a 10.000.
	MaxNotesRunes = 10_000

	// FormatVersion é a única versão do formato que este servidor lê.
	// Ausente ou diferente é 400: um payload de outra versão tem de falhar
	// alto, não ser interpretado pela metade.
	FormatVersion int64 = 1

	// maxRejectedKeywordRunes é o teto da palavra recusada que VOLTA no
	// relatório (a forma que o cliente mandou, truncada). É o mesmo teto do
	// schema `Keyword`, para o campo continuar cabendo onde a tela o espera.
	maxRejectedKeywordRunes = 40
)

// Motivos de PULO e de RECUSA de palavra (schemas KeywordImportSkipReason e
// KeywordImportRejectReason). Conjuntos FECHADOS, em `lower_snake`; o texto
// em português é do frontend.
const (
	SkipAlreadyPresent = "already_present"

	RejectItemNotFound       = "item_not_found"
	RejectItemArchived       = "item_archived"
	RejectNameMismatch       = "name_mismatch"
	RejectGroupHasChildren   = "group_has_children"
	RejectInvalidKeyword     = "invalid_keyword"
	RejectKeywordTaken       = "keyword_taken"
	RejectAmbiguousInPayload = "ambiguous_in_payload"
	RejectLimitExceeded      = "limit_exceeded"
)

// Desfechos de uma entrada de `newCategories` (schema NewCategoryOutcome).
// Conjunto FECHADO. `parent_not_group` não existe de propósito (achado A7 da
// emenda §10 da spec 0010): a categoria nova é chaveada por NOME, e um
// `group` com nome de subcategoria simplesmente não acha grupo.
const (
	OutcomeCreated            = "created"
	OutcomeMergedIntoExisting = "merged_into_existing"
	OutcomeSkippedByUser      = "skipped_by_user"
	OutcomeInvalidName        = "invalid_name"
	OutcomeKindRequired       = "kind_required"
	OutcomeInvalidKind        = "invalid_kind"
	OutcomeKindMismatch       = "kind_mismatch"
	OutcomeNameTakenArchived  = "name_taken_archived"
	OutcomeHouseholdLimit     = "household_limit"
	OutcomeDuplicateInPayload = "duplicate_in_payload"
)

// Tipos de item do relatório (schema KeywordImportItemType).
const (
	ItemTypeCategory = "category"
	ItemTypeAccount  = "account"
)

// Erros de domínio. Nenhum carrega dado da casa nem conteúdo do payload.
var (
	// ErrUnauthenticated — ator sem casa. O handler já barrou antes; a guarda
	// no serviço é defesa em profundidade, para que nenhuma consulta rode com
	// household_id vazio.
	ErrUnauthenticated = errors.New("autenticação necessária")

	// ErrInvalidPayload — o payload não tem a FORMA do contrato: versão
	// ausente ou diferente de 1, as três listas ausentes/vazias, lista acima
	// do teto de entradas, entrada acima do teto de palavras. É 400 na rota,
	// nunca relatório: sem forma não há o que relatar. Sempre embrulhado num
	// *PayloadError, que diz o CAMPO — e nunca o conteúdo.
	ErrInvalidPayload = errors.New("payload do import fora do formato")

	// ErrConflict — o estado da casa mudou entre a leitura e a escrita da
	// MESMA transação do confirm: uma categoria, um grupo ou uma
	// palavra-chave que o índice em memória pré-conferiu foi criada, tomada,
	// ganhou filha ou sumiu por outra requisição. A transação inteira foi
	// desfeita — nada gravado — e a tela pede a prévia de novo (409).
	ErrConflict = errors.New("o estado mudou durante a confirmação do import")

	// errPlanoInconsistente — o serviço chegou a um estado que o próprio
	// plano exclui (categoria pré-conferida que o caminho de criação recusa
	// por forma, natureza ou profundidade). É bug, não corrida: falha
	// FECHADA, 500 genérico, e a transação inteira desfeita.
	errPlanoInconsistente = errors.New("plano do import inconsistente com o caminho de criação")
)

// PayloadError aponta QUAL campo do envelope está fora da forma — é o que
// vira `fields.<campo>` no 400 do contrato. O caminho é o do JSON
// (`payload.categoryKeywords`, `payload.newCategories[3].add`), sem nenhum
// valor recebido: quem mandou a entrada já a tem.
type PayloadError struct {
	Field string
}

func (e *PayloadError) Error() string {
	return fmt.Sprintf("%v: %s", ErrInvalidPayload, e.Field)
}

func (e *PayloadError) Unwrap() error { return ErrInvalidPayload }

// --- Interfaces declaradas no CONSUMIDOR (ADR-027a) ------------------------
//
// Estreitas de propósito, como as de internal/classify: este pacote declara o
// que precisa e nada mais. Os repositórios e serviços que já existem as
// satisfazem sem uma linha nova, e nenhum tipo do ORM atravessa a fronteira.

// CategoryStore é a LEITURA que o import precisa de categoria — o material do
// índice em memória (§10.4 da spec 0010). gormstore.CategoryRepository a
// satisfaz.
type CategoryStore interface {
	// List devolve TODAS as categorias da casa (grupos e folhas). Este
	// pacote chama SEMPRE com includeArchived = true: é a arquivada que
	// responde `name_taken_archived` e `item_archived`, e o teto de 200
	// conta grupos, folhas e arquivadas juntos.
	List(ctx context.Context, householdID string, includeArchived bool) ([]category.Category, error)

	// ListKeywords devolve TODAS as palavras-chave de categoria da casa, em
	// uma consulta — é dela que saem `already_present`, `keyword_taken` (com
	// a dona) e o total por item para `limit_exceeded`.
	ListKeywords(ctx context.Context, householdID string) ([]category.Keyword, error)
}

// CategoryWriter é a ESCRITA de categoria, e ela passa pelo SERVIÇO — nunca
// pelo repositório. category.Service a satisfaz.
//
// Create é o MESMO caminho do POST /categories (§10.4): teto de 200,
// resolução do pai na casa do token, recusa do terceiro nível, herança da
// natureza e auditoria `category.created` vêm de graça. AppendKeywords é a
// única extração que a spec autorizou — e é ADITIVA (só insere as aprovadas;
// achado A2 da revisão de segurança): sem DELETE, nenhuma palavra comitada
// por outra transação some por corrida.
type CategoryWriter interface {
	Create(ctx context.Context, ator category.Actor, in category.CreateInput) (category.View, error)
	AppendKeywords(ctx context.Context, ator category.Actor, categoryID string, kws []category.Keyword, action string) error
}

// AccountStore é a LEITURA que o import precisa de conta.
// gormstore.AccountRepository a satisfaz.
type AccountStore interface {
	// List é chamada SEMPRE com includeArchived = true — a conta arquivada
	// responde `item_archived`, e para isso precisa ser encontrada.
	List(ctx context.Context, householdID string, includeArchived bool) ([]account.Account, error)
	ListKeywords(ctx context.Context, householdID string) ([]account.Keyword, error)
}

// AccountWriter é a ESCRITA de conta — só as palavras-chave. Este import
// NÃO cria, renomeia, arquiva nem exclui conta (spec 0010 §2.2), e a
// interface torna isso verificável: não há Create aqui. account.Service a
// satisfaz.
type AccountWriter interface {
	AppendKeywords(ctx context.Context, ator account.Actor, accountID string, kws []account.Keyword, action string) error
}

// Ledger é o que a medição de impacto precisa de lançamento: UMA leitura
// agregada, a mesma do prompt (aiprompt.Ledger). Sem ByID, sem listagem: o
// import não tem como citar um lançamento porque não tem como obter um.
type Ledger interface {
	GroupByDescription(ctx context.Context, householdID string,
		competenceMonths []string, limit int) ([]transaction.DescriptionGroup, error)
}

// Transactor executa uma função dentro de uma transação. É o UnitOfWork do
// projeto; o confirm grava grupos, folhas e palavras numa transação SÓ.
type Transactor interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Auditor registra o rastro da execução. Interface no consumidor, ponte em
// cmd/api — espelha audit.Params sem importar aquele pacote.
type Auditor interface {
	Record(ctx context.Context, p AuditParams) error
}

// AuditParams é o evento a registrar.
type AuditParams struct {
	Action      string
	Entity      string
	EntityID    string
	UserID      string
	HouseholdID string
	IP          string
}

// --- Entradas --------------------------------------------------------------

// Actor é quem está agindo: casa e usuário vêm do TOKEN (nunca do corpo), o
// IP vem da borda HTTP — o confirm audita, e a auditoria leva o IP.
type Actor struct {
	HouseholdID string
	UserID      string
	IP          string
}

// Input é o envelope das duas rotas (schema KeywordImportEnvelope), já
// decodificado pela borda. Os corpos da prévia e do confirm são IDÊNTICOS de
// propósito: é o que torna literal a promessa "o confirm revalida tudo do
// zero".
type Input struct {
	FromMonth string
	ToMonth   string

	// SkipNewCategories são os `ref` normalizados das categorias novas que a
	// pessoa desmarcou. Ausente na prévia. Um ref que não corresponde a
	// nenhuma entrada é ignorado sem erro.
	SkipNewCategories []string

	Payload Payload
}

// Payload é o JSON da IA (schema KeywordImportPayload), como veio — sem o
// `notes`, que a borda descarta ANTES de chegar aqui e que este tipo não tem
// onde guardar.
type Payload struct {
	// Version é nulo quando `homefinanceKeywordImport` não veio.
	Version *int64

	NewCategories    []NewCategoryEntry
	CategoryKeywords []CategoryKeywordEntry
	AccountKeywords  []AccountKeywordEntry
}

// NewCategoryEntry é uma subcategoria a criar (schema NewCategoryEntry). A
// chave é o par group + name, comparado normalizado.
type NewCategoryEntry struct {
	Group string
	Name  string

	// Kind é nulo quando o campo não veio. A distinção importa: com grupo
	// novo, ausente é `kind_required` e presente-mas-inválido é
	// `invalid_kind`.
	Kind *string

	Add []string
}

// CategoryKeywordEntry são palavras a acrescentar a uma categoria existente
// (schema CategoryKeywordEntry).
type CategoryKeywordEntry struct {
	CategoryID   string
	CategoryPath string
	Add          []string
}

// AccountKeywordEntry são palavras a acrescentar a uma conta existente
// (schema AccountKeywordEntry).
type AccountKeywordEntry struct {
	AccountID   string
	AccountName string
	Add         []string
}

// --- DTO de resposta (docs/SEGURANCA.md §4: nunca a entidade direto) -------

// Report é a resposta das duas rotas (schema KeywordImportReport): na prévia,
// o que ENTRARIA; no confirm, o que DE FATO entrou. A forma é idêntica de
// propósito. A única diferença de conteúdo é `impact`, que só vem na prévia e
// só em item de conta.
type Report struct {
	Totals        Totals            `json:"totals"`
	NewCategories []NewCategoryView `json:"newCategories"`
	Items         []ItemView        `json:"items"`
}

// Totals são os números do topo da tela (schema KeywordImportTotals). Cada
// um é a SOMA das listas do relatório — nunca um contador paralelo que
// pudesse divergir delas.
type Totals struct {
	// CategoriesCreated conta `outcome: created`; `merged_into_existing`
	// NÃO conta — não houve criação.
	CategoriesCreated int `json:"categoriesCreated"`
	Added             int `json:"added"`
	Skipped           int `json:"skipped"`
	Rejected          int `json:"rejected"`

	// PeriodTransactions são os lançamentos vivos `income`/`expense` da
	// janela: o universo da medição de impacto e o denominador da frase
	// "87 de 212" (§10.1 da spec 0010). Vem nas duas rotas — é a janela que o
	// define, não a medição.
	PeriodTransactions int `json:"periodTransactions"`
}

// NewCategoryView é uma entrada de `newCategories` com o seu desfecho (schema
// KeywordImportNewCategory).
type NewCategoryView struct {
	// Ref é o caminho NORMALIZADO `grupo > folha` — a chave estável entre a
	// prévia e o confirm, sem estado no servidor.
	Ref string `json:"ref"`

	// Group e Name são a forma EXIBÍVEL resolvida pelo servidor: o nome da
	// categoria existente quando ela existe, o nome como será gravado quando
	// nasce. Em `invalid_name` é a forma do cliente, neutralizada e truncada.
	Group string `json:"group"`
	Name  string `json:"name"`

	// Kind é a natureza EFETIVA da folha; nula quando a entrada foi recusada
	// ou pulada antes de haver natureza a resolver.
	Kind *string `json:"kind"`

	// GroupIsNew marca o grupo que também nasce nesta operação — e nasce sem
	// palavra-chave nenhuma, sempre.
	GroupIsNew bool `json:"groupIsNew"`

	Outcome string `json:"outcome"`

	// CategoryID é a categoria DESTA CASA que recebe as palavras: a existente
	// em `merged_into_existing`, a recém-nascida em `created` (só no
	// confirm). Nula nos demais casos.
	CategoryID *string `json:"categoryId"`

	Add      []string          `json:"add"`
	Skipped  []SkippedKeyword  `json:"skipped"`
	Rejected []RejectedKeyword `json:"rejected"`
}

// ItemView é o que aconteceu com as palavras de UM item que já existia
// (schema KeywordImportItem).
type ItemView struct {
	Type string `json:"type"`

	// ID é o id COMO VEIO no JSON — o único eco do conteúdo colado —, e só
	// quando tem a forma canônica de uuid; fora dela é vazio.
	ID string `json:"id"`

	// Name é o nome EXIBÍVEL vindo do SERVIDOR (caminho `Grupo > Folha` ou o
	// nome da conta), nunca a string do JSON. Nulo exatamente em
	// `item_not_found`.
	Name *string `json:"name"`

	Added    []string          `json:"added"`
	Skipped  []SkippedKeyword  `json:"skipped"`
	Rejected []RejectedKeyword `json:"rejected"`

	// Impact só existe na prévia e só em item de conta; ausente do JSON nos
	// demais casos (nunca `null`, que o schema não admite).
	Impact *Impact `json:"impact,omitempty"`
}

// SkippedKeyword é uma palavra que já estava lá.
type SkippedKeyword struct {
	Keyword string `json:"keyword"`
	Reason  string `json:"reason"`
}

// RejectedKeyword é uma palavra que não entrou, com o motivo. Sem texto
// livre: o que a tela precisa para "já está em X" é o OwnerID — sempre um
// recurso da casa do token —, presente só em `keyword_taken`.
type RejectedKeyword struct {
	Keyword string `json:"keyword"`
	Reason  string `json:"reason"`
	OwnerID string `json:"ownerId,omitempty"`
}

// Impact é o estrago MEDIDO antes de acontecer (schema KeywordImportImpact).
type Impact struct {
	// TransferCandidates é o total do ITEM: quantos lançamentos vivos
	// income/expense da janela ALGUMA das palavras que entrariam alcança —
	// união, sem contar duas vezes o lançamento alcançado por duas palavras.
	TransferCandidates int `json:"transferCandidates"`

	// ByKeyword é a medição POR PALAVRA: exatamente uma entrada por palavra
	// de `added` do mesmo item, na mesma ordem. É esta lista que a tela
	// mostra — a genérica precisa aparecer sozinha ao lado da legítima. A
	// soma pode passar do total do item. Sempre presente, `[]` quando nada
	// entraria.
	ByKeyword []KeywordImpact `json:"byKeyword"`
}

// KeywordImpact é o impacto de UMA palavra que entraria.
type KeywordImpact struct {
	Keyword            string `json:"keyword"`
	TransferCandidates int    `json:"transferCandidates"`
}
