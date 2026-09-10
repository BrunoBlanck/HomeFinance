package auth

import (
	"bufio"
	_ "embed"
	"strings"
	"sync"
	"unicode/utf8"
)

// Limites da política de senha.
const (
	MinPasswordLen = 12
	MaxPasswordLen = 256
)

//go:embed data/common-passwords.txt
var commonPasswordsRaw string

var (
	commonOnce      sync.Once
	commonPasswords map[string]struct{}
)

func loadCommonPasswords() map[string]struct{} {
	commonOnce.Do(func() {
		commonPasswords = make(map[string]struct{}, 512)
		sc := bufio.NewScanner(strings.NewReader(commonPasswordsRaw))
		for sc.Scan() {
			line := strings.ToLower(strings.TrimSpace(sc.Text()))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			commonPasswords[line] = struct{}{}
		}
	})
	return commonPasswords
}

// PasswordPolicy valida a senha antes de qualquer hash.
//
// D20 da spec 0001 — a denylist NÃO é enfeite: sem ela, "123456789012" e
// "senha123456" passam folgado numa política de 12 caracteres, e são
// exatamente as primeiras tentativas de qualquer ataque de dicionário.
type PasswordPolicy struct{}

// NewPasswordPolicy monta a política.
func NewPasswordPolicy() *PasswordPolicy { return &PasswordPolicy{} }

// Validate confere a senha contra a política.
//
// Devolve PasswordProblem (que casa com ErrWeakPassword em errors.Is) com um
// motivo em pt-BR pronto para o campo "fields" da resposta de validação.
// O motivo NUNCA contém a senha nem parte dela.
func (p *PasswordPolicy) Validate(password, email, name string) error {
	length := utf8.RuneCountInString(password)
	switch {
	case length < MinPasswordLen:
		return PasswordProblem{Reason: "use pelo menos 12 caracteres"}
	case length > MaxPasswordLen:
		return PasswordProblem{Reason: "use no máximo 256 caracteres"}
	}
	if strings.ContainsAny(password, "\x00") {
		return PasswordProblem{Reason: "caractere não permitido"}
	}
	if strings.TrimSpace(password) == "" {
		return PasswordProblem{Reason: "a senha não pode ser só espaços"}
	}

	lower := strings.ToLower(password)

	if _, banned := loadCommonPasswords()[lower]; banned {
		return PasswordProblem{Reason: "esta senha é muito comum"}
	}

	if email != "" {
		e := strings.ToLower(strings.TrimSpace(email))
		local := e
		if at := strings.Index(e, "@"); at > 0 {
			local = e[:at]
		}
		if lower == e || (len(local) >= 4 && strings.Contains(lower, local)) {
			return PasswordProblem{Reason: "a senha não pode conter seu e-mail"}
		}
	}

	if name != "" {
		n := strings.ToLower(strings.TrimSpace(name))
		if n != "" && lower == n {
			return PasswordProblem{Reason: "a senha não pode ser igual ao seu nome"}
		}
		for _, part := range strings.Fields(n) {
			if utf8.RuneCountInString(part) >= 4 && strings.Contains(lower, part) {
				return PasswordProblem{Reason: "a senha não pode conter seu nome"}
			}
		}
	}

	if isSingleRepeatedRune(password) {
		return PasswordProblem{Reason: "a senha não pode ser um caractere repetido"}
	}

	return nil
}

func isSingleRepeatedRune(s string) bool {
	var first rune
	for i, r := range s {
		if i == 0 {
			first = r
			continue
		}
		if r != first {
			return false
		}
	}
	return s != ""
}
