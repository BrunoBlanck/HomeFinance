package account

import (
	"context"
	"errors"
	"time"
)

// ErrKeywordTaken — a palavra-chave já pertence a OUTRA conta desta casa
// (spec 0005 §4.1, ADR-026d).
//
// Tem duas origens: a pré-checagem do serviço via KeywordOwners (que sabe
// citar a dona) e o índice único do banco, quando duas edições disputam a
// mesma palavra ao mesmo tempo (ReplaceKeywords, sem dona). A mensagem NÃO
// contém a palavra: o erro passa pelo log do handler.
var ErrKeywordTaken = errors.New("palavra-chave já usada por outra conta desta casa")

// MaxKeywordsPerOwner é o teto de palavras-chave por conta (spec 0005 §4.1).
const MaxKeywordsPerOwner = 20

// Keyword é UMA palavra-chave de conta (spec 0005, ADR-026d). Conjunto
// INDEPENDENTE do de categoria: a mesma palavra pode estar numa conta e numa
// categoria da mesma casa.
//
// Keyword é a forma EXIBÍVEL; Norm é textnorm.Normalize(Keyword), que o índice
// único (household_id, keyword_norm) compara.
type Keyword struct {
	ID          string
	HouseholdID string
	AccountID   string
	Keyword     string
	Norm        string
	// Position é a ordem de cadastro (0..MaxKeywordsPerOwner-1).
	Position  int
	CreatedAt time.Time
}

// Repository persiste contas. Implementação em
// internal/platform/storage/gormstore (ADR-008).
//
// Repare que TODO método recebe householdID. Isso não é redundância: é a
// defesa contra BOLA aplicada na camada mais baixa (docs/SEGURANCA.md §2). A
// alternativa comum — `ByID(id)` e o service conferir o dono depois — deixa a
// porta aberta para o dia em que alguém esquecer a conferência, e o esquecimento
// não aparece em nenhum teste que não seja o de isolamento. Aqui, uma conta de
// outra casa simplesmente não é retornável.
type Repository interface {
	Create(ctx context.Context, a *Account) error

	// ByID devolve a conta da casa informada. Conta de outra casa, conta
	// inexistente e conta excluída são o MESMO resultado: ErrNotFound.
	ByID(ctx context.Context, householdID, id string) (*Account, error)

	// List devolve as contas da casa ordenadas por nome normalizado, o que dá
	// uma ordem estável e alfabética que independe de acento e caixa.
	// Arquivadas só entram com includeArchived.
	List(ctx context.Context, householdID string, includeArchived bool) ([]Account, error)

	// Update grava nome, tipo, saldo de abertura, data e arquivamento. O
	// household_id entra no WHERE, nunca no SET.
	Update(ctx context.Context, a *Account) error

	// SoftDelete marca a exclusão lógica. Devolve ErrNotFound se a conta não
	// for da casa ou já estiver excluída.
	SoftDelete(ctx context.Context, householdID, id string, at time.Time) error

	// CountAll conta as contas NÃO EXCLUÍDAS da casa (arquivada conta: ela
	// ainda existe e pode voltar).
	CountAll(ctx context.Context, householdID string) (int64, error)

	// NameTaken informa se já existe conta ATIVA (não arquivada, não
	// excluída) com este nome normalizado na casa. exceptID permite que a
	// própria conta se renomeie para o mesmo nome sem colidir consigo mesma.
	NameTaken(ctx context.Context, householdID, nameNorm, exceptID string) (bool, error)

	// --- palavras-chave (schema v4, spec 0005, ADR-026d) ---------------------

	// ListKeywords devolve TODAS as palavras-chave da casa, em UMA consulta,
	// ordenadas por (account_id, position, id). Teto natural 20 × 50 = 1.000
	// linhas; quem descarta a palavra de conta arquivada/excluída é o
	// classificador (internal/classify), em Go.
	ListKeywords(ctx context.Context, householdID string) ([]Keyword, error)

	// KeywordOwners devolve, para cada norm informada que já existe na casa,
	// a conta dona: norm -> accountID. Inclui a PRÓPRIA conta em edição —
	// quem chama ignora ownerID == accountID. IN fatiado; outra casa nunca
	// volta.
	KeywordOwners(ctx context.Context, householdID string, norms []string) (map[string]string, error)

	// ReplaceKeywords SUBSTITUI a lista da conta na transação em curso: apaga
	// as atuais e grava as novas. Cada Keyword vem com ID, Keyword e Norm
	// preenchidos (ids nascem no serviço); HouseholdID/AccountID preenchidos
	// têm de bater com os argumentos. Violação do índice único volta como
	// ErrKeywordTaken.
	ReplaceKeywords(ctx context.Context, householdID, accountID string, kws []Keyword) error

	// DeleteKeywords apaga fisicamente as palavras-chave da conta, na mesma
	// transação da exclusão da conta (ADR-013).
	DeleteKeywords(ctx context.Context, householdID, accountID string) error
}

// UsageChecker informa se a conta tem dado dependente que impeça a exclusão.
//
// É uma interface, e não uma consulta direta, porque quem sabe responder muda
// a cada entrega: na E2 é o repositório de lançamentos, na E3 o de contas
// fixas. O serviço de conta não precisa conhecer nenhum dos dois — ele pergunta
// a quem foi registrado.
//
// **Estado na E2:** o verificador de lançamentos está registrado
// (transaction.NewUsageChecker), e com ele DELETE /accounts/{id} numa conta com
// lançamento responde 422 RESOURCE_IN_USE — a dívida que a spec 0003 §1 deixou
// declarada. O critério é "já foi usada alguma vez", contando o lançamento
// excluído logicamente; o porquê está em transaction.UsageChecker, e a saída
// para quem quer se livrar da conta é ARQUIVAR.
type UsageChecker interface {
	// AccountInUse responde se há dado dependente. Erro aqui é falha de
	// infraestrutura, e o serviço NÃO o interpreta como "não está em uso" —
	// na dúvida, a exclusão é recusada.
	AccountInUse(ctx context.Context, householdID, accountID string) (bool, error)
}
