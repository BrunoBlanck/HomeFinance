// Package session carrega a identidade autenticada pelo context.Context.
//
// É um pacote-FOLHA: não importa nenhum outro pacote do projeto (D14). Isso
// quebra o ciclo auth -> user -> auth e permite que o middleware de
// autenticação da borda publique a identidade sem conhecer o domínio auth.
package session

import "context"

// Identity é o que o servidor sabe sobre quem está fazendo a requisição.
// Todo campo vem do access token assinado — NUNCA do corpo, query ou path
// (docs/SEGURANCA.md §2: BOLA é o risco nº 1).
type Identity struct {
	UserID      string
	HouseholdID string
	Role        string
	SessionID   string
}

// Papéis reconhecidos.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// Valid informa se a identidade tem os campos mínimos para autorizar algo.
func (i Identity) Valid() bool {
	return i.UserID != "" && i.HouseholdID != "" && i.Role != "" && i.SessionID != ""
}

type contextKey struct{}

// NewContext devolve um contexto derivado carregando a identidade.
func NewContext(ctx context.Context, ident Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, ident)
}

// FromContext extrai a identidade publicada pelo middleware de autenticação.
// O segundo retorno é falso quando a requisição não está autenticada.
func FromContext(ctx context.Context) (Identity, bool) {
	ident, ok := ctx.Value(contextKey{}).(Identity)
	if !ok || !ident.Valid() {
		return Identity{}, false
	}
	return ident, true
}
