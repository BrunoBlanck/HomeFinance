package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPoliticaAceitaSenhaBoa(t *testing.T) {
	t.Parallel()

	p := auth.NewPasswordPolicy()
	boas := []string{
		"cavalo-bateria-grampo-correto",
		"Rq7!zx#Lm2Pw",
		"são josé do rio preto 1984",
		strings.Repeat("a1B!", 3),
	}
	for _, s := range boas {
		assert.NoError(t, p.Validate(s, "bruno@exemplo.com", "Bruno Blanck"), "deveria aceitar %q", s)
	}
}

func TestPoliticaExigeTamanho(t *testing.T) {
	t.Parallel()

	p := auth.NewPasswordPolicy()

	err := p.Validate("curta12345", "bruno@exemplo.com", "Bruno")
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrWeakPassword)
	assert.Contains(t, err.Error(), "12 caracteres")

	err = p.Validate(strings.Repeat("x", 257), "bruno@exemplo.com", "Bruno")
	require.Error(t, err)
	assert.ErrorIs(t, err, auth.ErrWeakPassword)
}

// D20: sem denylist, "123456789012" passa numa política de 12 caracteres — e
// é a primeira tentativa de qualquer ataque de dicionário.
func TestPoliticaRecusaSenhaComum(t *testing.T) {
	t.Parallel()

	p := auth.NewPasswordPolicy()
	comuns := []string{
		"123456789012",
		"password1234",
		"senha12345678",
		"PASSWORD1234",
		"qwertyuiopas",
		"minhasenha123",
		"brasil12345678",
	}
	for _, s := range comuns {
		err := p.Validate(s, "bruno@exemplo.com", "Bruno")
		require.Error(t, err, "deveria recusar %q", s)
		assert.ErrorIs(t, err, auth.ErrWeakPassword)
		assert.Contains(t, err.Error(), "comum")
	}
}

func TestPoliticaRecusaSenhaComEmailOuNome(t *testing.T) {
	t.Parallel()

	p := auth.NewPasswordPolicy()

	err := p.Validate("bruno@exemplo.com", "bruno@exemplo.com", "Bruno Blanck")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "e-mail")

	err = p.Validate("xxbrunoblanckxx-2026", "brunoblanck@exemplo.com", "Bruno Blanck")
	require.Error(t, err)

	err = p.Validate("meu-blanck-favorito", "outro@exemplo.com", "Bruno Blanck")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nome")
}

func TestPoliticaRecusaCaractereRepetido(t *testing.T) {
	t.Parallel()

	p := auth.NewPasswordPolicy()
	err := p.Validate(strings.Repeat("z", 20), "bruno@exemplo.com", "Bruno")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repetido")

	err = p.Validate(strings.Repeat(" ", 20), "bruno@exemplo.com", "Bruno")
	require.Error(t, err)
}

func TestPoliticaRecusaNul(t *testing.T) {
	t.Parallel()

	p := auth.NewPasswordPolicy()
	assert.Error(t, p.Validate("senha-com-nul\x00aqui", "bruno@exemplo.com", "Bruno"))
}

// A mensagem devolvida vai para o campo "fields" da resposta: não pode conter
// a senha nem parte dela (docs/SEGURANCA.md §4).
func TestMensagemDaPoliticaNaoEcoaASenha(t *testing.T) {
	t.Parallel()

	p := auth.NewPasswordPolicy()
	const senha = "123456789012"

	err := p.Validate(senha, "bruno@exemplo.com", "Bruno")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), senha)
	assert.NotContains(t, err.Error(), "123456")
}

func TestPasswordProblemCasaComErrWeakPassword(t *testing.T) {
	t.Parallel()

	err := auth.PasswordProblem{Reason: "motivo"}
	assert.True(t, errors.Is(err, auth.ErrWeakPassword))
	assert.Equal(t, "motivo", err.Error())
}

func TestPoliticaSemNomeNemEmail(t *testing.T) {
	t.Parallel()

	// Caminho do reset-password: o nome do titular não é carregado, para não
	// vazar existência de conta.
	p := auth.NewPasswordPolicy()
	assert.NoError(t, p.Validate("uma-senha-bem-decente-2026", "", ""))
}
