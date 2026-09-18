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

// Fuso e moeda padrão da casa (ADR-019).
const (
	// DefaultTimezone é o fuso de uma casa recém-criada. Ele é REGRA DE
	// NEGÓCIO, não formatação: é ele que decide se uma conta está atrasada.
	DefaultTimezone = "America/Sao_Paulo"

	// DefaultCurrency é fixo no v1. A coluna existe para que multimoeda não
	// exija mudança destrutiva depois — o AutoMigrate não renomeia (ADR-008).
	DefaultCurrency = "BRL"

	// MaxTimezoneLen acompanha o maior nome da base IANA com folga.
	MaxTimezoneLen = 64
)

// ErrInvalidTimezone — fuso que não existe na base de dados de fusos.
var ErrInvalidTimezone = errors.New("fuso horário inválido")

// Household é a casa.
type Household struct {
	ID        string
	Name      string
	Timezone  string
	Currency  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Location resolve o fuso da casa.
//
// Casa sem fuso gravado (linha criada antes do ADR-019, que o AutoMigrate
// preenche com o default) e fuso desconhecido caem no padrão em vez de
// derrubar a requisição: "atrasado" calculado no fuso errado é um bug de um
// dia, e um erro 500 aqui derrubaria a tela inteira.
func (h Household) Location() *time.Location {
	name := strings.TrimSpace(h.Timezone)
	if name == "" {
		name = DefaultTimezone
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc, err = time.LoadLocation(DefaultTimezone)
		if err != nil {
			return time.UTC
		}
	}
	return loc
}

// ValidateTimezone confirma que o nome existe na base IANA. A validação é por
// LoadLocation, e não por lista própria: manter uma lista aqui seria garantir
// que ela envelhece.
func ValidateTimezone(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > MaxTimezoneLen {
		return ErrInvalidTimezone
	}
	if _, err := time.LoadLocation(trimmed); err != nil {
		return ErrInvalidTimezone
	}
	return nil
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
//
// Timezone e Currency entram aqui porque o cliente PRECISA deles para decidir
// o que mostrar: o mês corrente do seletor é o mês de hoje NO FUSO DA CASA, e
// calculá-lo no fuso do navegador daria o mês errado para quem viaja ou para
// quem abre o app perto da virada (ADR-019). Não são segredo nem dado pessoal
// — são a configuração que a própria pessoa escolheu.
type Summary struct {
	ID       string
	Name     string
	Role     string
	Timezone string
	Currency string
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
