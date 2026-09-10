package household

import "context"

// Repository persiste casas. Implementação em
// internal/platform/storage/gormstore (ADR-008).
type Repository interface {
	Create(ctx context.Context, h *Household) error
	ByID(ctx context.Context, id string) (*Household, error)
	// ByIDs devolve as casas pedidas, em qualquer ordem. Existe para montar
	// a lista de /me sem JOIN — duas consultas parametrizadas são mais
	// simples de auditar do que um join com Select/Table montados.
	ByIDs(ctx context.Context, ids []string) ([]Household, error)
}

// MembershipRepository persiste os vínculos usuário-casa.
type MembershipRepository interface {
	Create(ctx context.Context, m *Membership) error
	// ListByUser devolve os vínculos do usuário ordenados por criação
	// (mais antigo primeiro) e, em empate, por ID — o que torna a "casa
	// ativa" determinística (D18 da spec 0001).
	ListByUser(ctx context.Context, userID string) ([]Membership, error)
	// ByUserAndHousehold é a checagem de autorização por recurso: filtra
	// pelos dois lados, nunca busca por ID e confere depois
	// (docs/SEGURANCA.md §2).
	ByUserAndHousehold(ctx context.Context, userID, householdID string) (*Membership, error)
	CountByUser(ctx context.Context, userID string) (int64, error)
}
