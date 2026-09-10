package user_test

import (
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeEmailAceitaEDeixaCanonico(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"bruno@exemplo.com":       "bruno@exemplo.com",
		"  Bruno@Exemplo.COM  ":   "bruno@exemplo.com",
		"BRUNO.BLANCK@SUB.EX.COM": "bruno.blanck@sub.ex.com",
		"a+tag@exemplo.com.br":    "a+tag@exemplo.com.br",
		"x_y-z@exemplo.io":        "x_y-z@exemplo.io",
	}
	for entrada, esperado := range casos {
		got, err := user.NormalizeEmail(entrada)
		require.NoError(t, err, "entrada %q", entrada)
		assert.Equal(t, esperado, got)
	}
}

func TestNormalizeEmailRejeita(t *testing.T) {
	t.Parallel()

	invalidos := []string{
		"",
		"   ",
		"semarroba.com",
		"@exemplo.com",
		"bruno@",
		"bruno@@exemplo.com",
		"bruno@exemplo",
		"bruno@.com",
		"bruno@exemplo..com",
		"bruno@exemplo.com.",
		".bruno@exemplo.com",
		"bruno.@exemplo.com",
		"bru..no@exemplo.com",
		"Bruno Blanck <bruno@exemplo.com>",
		"bruno@exemplo.com, outro@exemplo.com",
		"bruno<script>@exemplo.com",
		strings.Repeat("a", 250) + "@exemplo.com",
		strings.Repeat("a", 65) + "@exemplo.com",
	}
	for _, entrada := range invalidos {
		_, err := user.NormalizeEmail(entrada)
		assert.ErrorIs(t, err, user.ErrInvalidEmail, "deveria rejeitar %q", entrada)
	}
}

// Risco 12 da §9: CR/LF no e-mail viraria injeção de cabeçalho SMTP.
func TestNormalizeEmailBloqueiaInjecaoDeCabecalho(t *testing.T) {
	t.Parallel()

	ataques := []string{
		"bruno@exemplo.com\r\nBcc: vitima@alvo.com",
		"bruno@exemplo.com\nBcc: vitima@alvo.com",
		"bruno@exemplo.com\r",
		"bruno@exemplo.com\x00",
	}
	for _, entrada := range ataques {
		_, err := user.NormalizeEmail(entrada)
		assert.ErrorIs(t, err, user.ErrInvalidEmail, "deveria bloquear %q", entrada)
	}
}

func TestNormalizeName(t *testing.T) {
	t.Parallel()

	got, err := user.NormalizeName("  Bruno   Blanck  ")
	require.NoError(t, err)
	assert.Equal(t, "Bruno Blanck", got)

	got, err = user.NormalizeName("José da Silva")
	require.NoError(t, err)
	assert.Equal(t, "José da Silva", got)

	for _, invalido := range []string{"", "   ", "\t\n", "Bruno\r\nX", "Bruno\x00", strings.Repeat("a", 121)} {
		_, err := user.NormalizeName(invalido)
		assert.ErrorIs(t, err, user.ErrInvalidName, "deveria rejeitar %q", invalido)
	}
}
