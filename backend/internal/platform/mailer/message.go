package mailer

import (
	"errors"
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"time"
)

// Erros de montagem de mensagem.
var (
	// ErrHeaderInjection — CR, LF ou NUL num campo de cabeçalho.
	ErrHeaderInjection = errors.New("tentativa de injeção de cabeçalho")
	// ErrInvalidAddress — endereço malformado.
	ErrInvalidAddress = errors.New("endereço de e-mail inválido")
)

// Message é uma mensagem em texto puro.
//
// Só texto puro, de propósito: não há HTML para sanitizar, nem imagem remota
// para rastrear o usuário, nem superfície de renderização no cliente.
type Message struct {
	From     string
	FromName string
	To       string
	ToName   string
	Subject  string
	Body     string
}

// Build monta a mensagem RFC 5322 pronta para o SMTP.
//
// Risco 12 da §9 da spec 0001 (header injection): qualquer CR/LF em To,
// From ou Subject permitiria injetar cabeçalhos — Bcc para terceiros, por
// exemplo. Validamos ANTES de concatenar e recusamos a mensagem inteira;
// nunca "limpamos" silenciosamente.
func (m Message) Build(now time.Time, messageID string) ([]byte, error) {
	if err := ensureNoInjection(m.From, m.FromName, m.To, m.ToName, m.Subject); err != nil {
		return nil, err
	}

	from, err := parseAddress(m.From)
	if err != nil {
		return nil, fmt.Errorf("remetente: %w", err)
	}
	to, err := parseAddress(m.To)
	if err != nil {
		return nil, fmt.Errorf("destinatário: %w", err)
	}

	var b strings.Builder
	b.WriteString("From: " + formatAddress(m.FromName, from) + "\r\n")
	b.WriteString("To: " + formatAddress(m.ToName, to) + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", m.Subject) + "\r\n")
	b.WriteString("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	if messageID != "" {
		b.WriteString("Message-ID: <" + messageID + "@homefinance>\r\n")
	}
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("\r\n")
	b.WriteString(normalizeBody(m.Body))

	return []byte(b.String()), nil
}

// Recipient devolve o endereço validado do destinatário.
func (m Message) Recipient() (string, error) {
	if err := ensureNoInjection(m.To); err != nil {
		return "", err
	}
	return parseAddress(m.To)
}

// Sender devolve o endereço validado do remetente.
func (m Message) Sender() (string, error) {
	if err := ensureNoInjection(m.From); err != nil {
		return "", err
	}
	return parseAddress(m.From)
}

func ensureNoInjection(values ...string) error {
	for _, v := range values {
		if strings.ContainsAny(v, "\r\n\x00") {
			return ErrHeaderInjection
		}
	}
	return nil
}

func parseAddress(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", ErrInvalidAddress
	}
	addr, err := mail.ParseAddress(v)
	if err != nil || addr.Address != v {
		return "", fmt.Errorf("%w: forma inesperada", ErrInvalidAddress)
	}
	return addr.Address, nil
}

// formatAddress monta o par "nome <endereço>" do cabeçalho.
//
// Usa mail.Address.String() em vez de concatenar: o QEncoding devolve ASCII
// imprimível INALTERADO, então um nome contendo `<`, `>`, `@`, `,` ou `"`
// era concatenado cru e forjava o cabeçalho exibido — por exemplo
// `Suporte <seguranca@banco-falso.test>, Equipe` virava dois destinatários
// aparentes. mail.Address cita o display name quando ele tem caractere
// especial, e codifica o não-ASCII.
func formatAddress(name, addr string) string {
	if name == "" {
		return "<" + addr + ">"
	}
	return (&mail.Address{Name: name, Address: addr}).String()
}

// normalizeBody garante CRLF e evita que uma linha "." sozinha encerre os
// dados do SMTP antes da hora.
func normalizeBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if l == "." {
			lines[i] = ".."
		}
	}
	return strings.Join(lines, "\r\n")
}
