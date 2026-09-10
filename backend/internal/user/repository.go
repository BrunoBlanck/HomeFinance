package user

import (
	"context"
	"time"
)

// Repository persiste usuários. Implementação em
// internal/platform/storage/gormstore (ADR-008).
//
// Todos os métodos de escrita são pontuais (colunas nomeadas) em vez de um
// Save(entidade) genérico: assim nenhuma gravação sobrescreve por acidente o
// hash de senha ou o email_verified_at de outro fluxo.
type Repository interface {
	Create(ctx context.Context, u *User) error
	ByID(ctx context.Context, id string) (*User, error)
	// ByEmail busca pelo e-mail JÁ NORMALIZADO.
	ByEmail(ctx context.Context, email string) (*User, error)

	// ActivatePending grava, NUMA ÚNICA linha de UPDATE, o nome, o hash de
	// senha e o instante da verificação de uma conta ainda pendente.
	//
	// As três colunas andam juntas de propósito: no modelo de tentativas de
	// cadastro (auth.RegistrationAttempt), quem confirma o e-mail ativa as
	// credenciais DA TENTATIVA que recebeu o código. Separar "gravar
	// credenciais" de "marcar verificado" abriria uma janela em que a conta
	// está verificada com a senha de outra pessoa.
	//
	// A implementação exige email_verified_at IS NULL na cláusula: conta já
	// ativa NUNCA é tocada por este caminho — seria tomada de conta direta.
	// Devolve false quando nenhuma linha foi afetada (já verificada).
	ActivatePending(ctx context.Context, id, name, passwordHash string, at time.Time) (bool, error)
	// UpdatePassword troca a senha de qualquer conta.
	UpdatePassword(ctx context.Context, id, passwordHash string, at time.Time) error
}
