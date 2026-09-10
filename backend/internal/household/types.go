// Package household modela a casa — a unidade de isolamento de TODO dado do
// projeto (docs/SEGURANCA.md §2).
//
// O household_id de qualquer operação vem do token de sessão. Este pacote
// nunca aceita household_id vindo do cliente.
package household

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// Papéis de membro.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// Limites de coluna (ver §4 da spec 0001).
const (
	MaxNameLen = 120
)

// Erros de domínio. O handler os traduz para HTTP; nenhum deles carrega
// detalhe interno.
var (
	// ErrNotFound — casa ou vínculo inexistente.
	ErrNotFound = errors.New("casa não encontrada")
	// ErrInvalidRole — papel fora do conjunto conhecido.
	ErrInvalidRole = errors.New("papel inválido")
)

// Household é a casa.
type Household struct {
	ID        string
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Membership é o vínculo entre usuário e casa, com papel.
type Membership struct {
	ID          string
	HouseholdID string
	UserID      string
	Role        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Summary é a visão de uma casa do ponto de vista de um usuário: é o que
// aparece no corpo de /me. DTO próprio de propósito — nunca serializamos a
// entidade do banco (docs/SEGURANCA.md §4).
type Summary struct {
	ID   string
	Name string
	Role string
}

// ValidRole informa se o papel é conhecido.
func ValidRole(role string) bool {
	return role == RoleOwner || role == RoleMember
}

// DefaultName monta o nome da casa criada na verificação do e-mail (D1 da
// spec 0001): "Casa de {primeiro nome}", truncado em MaxNameLen; nome vazio
// vira "Minha casa".
func DefaultName(userName string) string {
	first := strings.TrimSpace(userName)
	if first == "" {
		return "Minha casa"
	}
	if idx := strings.IndexFunc(first, func(r rune) bool { return r == ' ' || r == '\t' }); idx > 0 {
		first = first[:idx]
	}
	name := "Casa de " + first
	return truncateRunes(name, MaxNameLen)
}

func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i]
		}
		count++
	}
	return s
}
