package cardstatement

import "context"

// ListFilter descreve a janela da listagem. Campo vazio = sem filtro.
type ListFilter struct {
	AccountID       string
	CompetenceMonth string
	// Limit zero usa o default do repositório; valores absurdos são reduzidos
	// ao teto.
	Limit int
}

// Repository persiste faturas. Implementação em
// internal/platform/storage/gormstore (ADR-008).
//
// Como em todo repositório do projeto, householdID entra no WHERE e nunca no
// SET (docs/SEGURANCA.md §2).
type Repository interface {
	// Upsert é IDEMPOTENTE por (household_id, account_id,
	// competence_month): importar o mesmo PDF duas vezes não cria duas
	// faturas de fevereiro.
	//
	// É SELECT + INSERT/UPDATE dentro da transação em curso, e não um upsert
	// de dialeto (armadilha P8): ON CONFLICT, ON DUPLICATE KEY e MERGE têm
	// três sintaxes e um comportamento diferente em cada banco. **Chame
	// sempre dentro de um UnitOfWork** — fora dele, duas requisições
	// simultâneas podem colidir, e a colisão volta como ErrDuplicate.
	//
	// Ao encontrar uma fatura já existente, Upsert preenche s.ID com o id
	// dela — é assim que quem chama liga os lançamentos à fatura certa. Uma
	// fatura excluída logicamente é RESTAURADA em vez de duplicada: a chave
	// única do banco não distingue excluído, então ignorá-la deixaria aquela
	// competência bloqueada para sempre.
	Upsert(ctx context.Context, s *Statement) error

	// ByID devolve a fatura da casa; de outra casa é ErrNotFound.
	ByID(ctx context.Context, householdID, id string) (*Statement, error)

	// List devolve as faturas da casa, da competência mais recente para a
	// mais antiga.
	List(ctx context.Context, householdID string, f ListFilter) ([]Statement, error)
}
