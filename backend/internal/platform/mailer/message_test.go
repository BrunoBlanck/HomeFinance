package mailer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/platform/mailer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func msgBase() mailer.Message {
	return mailer.Message{
		From:     "nao-responda@homefinance.test",
		FromName: "HomeFinance",
		To:       "bruno@exemplo.com",
		ToName:   "Bruno Blanck",
		Subject:  "Seu código de confirmação",
		Body:     "Olá,\n\nSeu código é 123456.\n",
	}
}

func TestBuildMontaMensagemValida(t *testing.T) {
	t.Parallel()

	raw, err := msgBase().Build(time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC), "id-1")
	require.NoError(t, err)

	out := string(raw)
	assert.Contains(t, out, "To: ")
	assert.Contains(t, out, "bruno@exemplo.com")
	assert.Contains(t, out, "From: ")
	assert.Contains(t, out, "MIME-Version: 1.0")
	assert.Contains(t, out, "Content-Type: text/plain; charset=UTF-8")
	assert.Contains(t, out, "Auto-Submitted: auto-generated")
	assert.Contains(t, out, "Message-ID: <id-1@homefinance>")

	// Cabeçalho e corpo separados por linha em branco CRLF.
	assert.Contains(t, out, "\r\n\r\n")
	// Todo fim de linha é CRLF.
	assert.NotRegexp(t, "[^\r]\n", out)
}

// Risco 12 da §9 da spec 0001: CR/LF em To, From ou Subject permitiria
// injetar cabeçalhos (Bcc para terceiros, por exemplo).
func TestBuildBloqueiaInjecaoDeCabecalho(t *testing.T) {
	t.Parallel()

	ataques := map[string]func(*mailer.Message){
		"CRLF no destinatário":      func(m *mailer.Message) { m.To = "bruno@exemplo.com\r\nBcc: vitima@alvo.com" },
		"LF no destinatário":        func(m *mailer.Message) { m.To = "bruno@exemplo.com\nBcc: vitima@alvo.com" },
		"CRLF no assunto":           func(m *mailer.Message) { m.Subject = "Oi\r\nBcc: vitima@alvo.com" },
		"CRLF no remetente":         func(m *mailer.Message) { m.From = "x@y.com\r\nBcc: vitima@alvo.com" },
		"CRLF no nome do destino":   func(m *mailer.Message) { m.ToName = "Bruno\r\nBcc: vitima@alvo.com" },
		"CRLF no nome do remetente": func(m *mailer.Message) { m.FromName = "HF\r\nBcc: vitima@alvo.com" },
		"NUL no assunto":            func(m *mailer.Message) { m.Subject = "Oi\x00" },
	}

	for nome, mutar := range ataques {
		t.Run(nome, func(t *testing.T) {
			m := msgBase()
			mutar(&m)

			_, err := m.Build(time.Now(), "id")
			require.Error(t, err, "a mensagem inteira precisa ser recusada")
			assert.ErrorIs(t, err, mailer.ErrHeaderInjection)
		})
	}
}

func TestBuildRejeitaEnderecoInvalido(t *testing.T) {
	t.Parallel()

	m := msgBase()
	m.To = "sem-arroba"
	_, err := m.Build(time.Now(), "id")
	assert.ErrorIs(t, err, mailer.ErrInvalidAddress)

	m = msgBase()
	m.From = ""
	_, err = m.Build(time.Now(), "id")
	assert.ErrorIs(t, err, mailer.ErrInvalidAddress)

	// Forma "Nome <endereço>" no campo de endereço também é recusada: o nome
	// vem em campo próprio.
	m = msgBase()
	m.To = "Bruno <bruno@exemplo.com>"
	_, err = m.Build(time.Now(), "id")
	assert.ErrorIs(t, err, mailer.ErrInvalidAddress)
}

// Uma linha "." sozinha encerraria o DATA do SMTP antes da hora.
func TestBuildEscapaPontoSozinho(t *testing.T) {
	t.Parallel()

	m := msgBase()
	m.Body = "linha 1\n.\nlinha 2"

	raw, err := m.Build(time.Now(), "id")
	require.NoError(t, err)
	assert.Contains(t, string(raw), "\r\n..\r\n")
}

func TestAssuntoComAcentoEhCodificado(t *testing.T) {
	t.Parallel()

	m := msgBase()
	m.Subject = "Confirmação de e-mail"

	raw, err := m.Build(time.Now(), "id")
	require.NoError(t, err)

	linhaAssunto := ""
	for _, l := range strings.Split(string(raw), "\r\n") {
		if strings.HasPrefix(l, "Subject: ") {
			linhaAssunto = l
			break
		}
	}
	require.NotEmpty(t, linhaAssunto)
	assert.Contains(t, linhaAssunto, "=?utf-8?", "acentos precisam de codificação MIME")
}

func TestRecipientESender(t *testing.T) {
	t.Parallel()

	m := msgBase()
	to, err := m.Recipient()
	require.NoError(t, err)
	assert.Equal(t, "bruno@exemplo.com", to)

	from, err := m.Sender()
	require.NoError(t, err)
	assert.Equal(t, "nao-responda@homefinance.test", from)

	m.To = "x@y.com\r\nBcc: z@w.com"
	_, err = m.Recipient()
	assert.ErrorIs(t, err, mailer.ErrHeaderInjection)
}
