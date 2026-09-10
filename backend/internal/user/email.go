package user

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// NormalizeEmail valida e normaliza o e-mail na borda.
//
// Normalizar é requisito de SEGURANÇA, não estética: sem uma forma canônica,
// "Bruno@X.com" e "bruno@x.com" viram duas contas, o rate limit por conta
// deixa de funcionar e a busca do código OTP por e-mail passa a depender de
// como o cliente digitou.
//
// O que fazemos: recortar espaços, rejeitar CR/LF (injeção de cabeçalho SMTP
// — risco 12 da §9 da spec 0001), exigir exatamente um "@", baixar a caixa do
// DOMÍNIO (a parte local é, pela RFC 5321, sensível a caixa; baixamos ela
// também porque nenhum provedor sério diferencia, e a alternativa abre
// duplicidade de contas), e conferir tamanho e forma com net/mail.
func NormalizeEmail(raw string) (string, error) {
	// A checagem de controle roda no valor BRUTO. Se rodasse depois do
	// TrimSpace, "bruno@exemplo.com\r" passaria batido — e é exatamente esse
	// byte que abre injeção de cabeçalho no SMTP.
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", fmt.Errorf("%w: caractere de controle", ErrInvalidEmail)
	}
	e := strings.TrimSpace(raw)
	if e == "" {
		return "", fmt.Errorf("%w: vazio", ErrInvalidEmail)
	}
	if len(e) > MaxEmailLen {
		return "", fmt.Errorf("%w: acima de %d bytes", ErrInvalidEmail, MaxEmailLen)
	}
	if strings.ContainsAny(e, " \t<>\"(),;:[]\\") {
		return "", fmt.Errorf("%w: caractere não permitido", ErrInvalidEmail)
	}

	at := strings.LastIndex(e, "@")
	if at <= 0 || at == len(e)-1 || strings.Count(e, "@") != 1 {
		return "", fmt.Errorf("%w: forma inválida", ErrInvalidEmail)
	}

	local, domain := e[:at], strings.ToLower(e[at+1:])
	local = strings.ToLower(local)

	if len(local) > 64 {
		return "", fmt.Errorf("%w: parte local acima de 64 bytes", ErrInvalidEmail)
	}
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return "", fmt.Errorf("%w: domínio inválido", ErrInvalidEmail)
	}
	if strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") {
		return "", fmt.Errorf("%w: parte local inválida", ErrInvalidEmail)
	}

	normalized := local + "@" + domain

	// net/mail é a checagem final de forma. Rejeitamos qualquer coisa que ele
	// interprete diferente do que escrevemos (nome de exibição, comentário…).
	addr, err := mail.ParseAddress(normalized)
	if err != nil || addr.Address != normalized || addr.Name != "" {
		return "", fmt.Errorf("%w: forma inválida", ErrInvalidEmail)
	}

	return normalized, nil
}

// NormalizeName valida e normaliza o nome exibido.
func NormalizeName(raw string) (string, error) {
	// Mesma razão do e-mail: o nome vira o display name do cabeçalho "To:".
	// strings.Fields transformaria um CRLF em espaço e esconderia a tentativa
	// de injeção, então a checagem vem ANTES da normalização.
	if strings.ContainsAny(raw, "\r\n\x00") {
		return "", fmt.Errorf("%w: caractere de controle", ErrInvalidName)
	}
	n := strings.Join(strings.Fields(strings.TrimSpace(raw)), " ")
	if n == "" {
		return "", fmt.Errorf("%w: vazio", ErrInvalidName)
	}
	if !utf8.ValidString(n) {
		return "", fmt.Errorf("%w: codificação inválida", ErrInvalidName)
	}
	if utf8.RuneCountInString(n) > MaxNameLen {
		return "", fmt.Errorf("%w: acima de %d caracteres", ErrInvalidName, MaxNameLen)
	}
	return n, nil
}
