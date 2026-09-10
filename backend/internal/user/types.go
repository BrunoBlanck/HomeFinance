// Package user modela a pessoa que usa o sistema.
//
// A entidade User carrega o hash da senha, e por isso NUNCA é serializada
// direto para o cliente: todo handler monta um DTO próprio
// (docs/SEGURANCA.md §4 e risco 13 da §9 da spec 0001).
package user

import (
	"errors"
	"log/slog"
	"time"
)

// Limites de coluna (§4 da spec 0001).
const (
	MaxEmailLen = 254
	MaxNameLen  = 120
)

// Erros de domínio.
var (
	// ErrNotFound — usuário inexistente.
	ErrNotFound = errors.New("usuário não encontrado")
	// ErrEmailTaken — violação da unicidade de e-mail.
	ErrEmailTaken = errors.New("e-mail já cadastrado")
	// ErrInvalidEmail — e-mail malformado ou grande demais.
	ErrInvalidEmail = errors.New("e-mail inválido")
	// ErrInvalidName — nome vazio ou grande demais.
	ErrInvalidName = errors.New("nome inválido")
)

// User é a entidade de usuário.
type User struct {
	ID              string
	Email           string
	PasswordHash    string
	Name            string
	EmailVerifiedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Verified informa se o e-mail já foi confirmado. Conta não verificada é
// inutilizável (ADR-009).
func (u *User) Verified() bool {
	return u != nil && u.EmailVerifiedAt != nil && !u.EmailVerifiedAt.IsZero()
}

// LogValue implementa slog.LogValuer: um *User que caia no log por engano
// entrega só id e estado de verificação, nunca e-mail ou hash de senha.
func (u *User) LogValue() slog.Value {
	if u == nil {
		return slog.StringValue("<nil>")
	}
	return slog.GroupValue(
		slog.String("id", u.ID),
		slog.Bool("verified", u.Verified()),
	)
}
