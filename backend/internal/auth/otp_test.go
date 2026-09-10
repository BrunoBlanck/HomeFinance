package auth_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Placeholder de teste, não é segredo real.
const pepperDeTeste = "pepper-de-teste-0123456789abcdefghij"

func newOTP(t *testing.T) *auth.OTP {
	t.Helper()
	o, err := auth.NewOTP(pepperDeTeste)
	require.NoError(t, err)
	return o
}

func TestNewOTPExigePepperForte(t *testing.T) {
	t.Parallel()

	_, err := auth.NewOTP("curto")
	assert.Error(t, err)

	_, err = auth.NewOTP(strings.Repeat("x", 32))
	assert.NoError(t, err)
}

// docs/SEGURANCA.md §1.1: sempre 6 caracteres, e zeros à esquerda contam.
func TestGenerateCodeTemSempreSeisDigitos(t *testing.T) {
	t.Parallel()

	o := newOTP(t)
	re := regexp.MustCompile(`^[0-9]{6}$`)

	for range 2000 {
		code, err := o.GenerateCode()
		require.NoError(t, err)
		require.True(t, re.MatchString(code), "código fora do formato: %q", code)
		require.Len(t, code, 6)
	}
}

// O ponto do "sem viés de módulo": a distribuição precisa cobrir o espaço
// inteiro de forma uniforme. Com "bytes % 1000000" os valores baixos
// apareceriam mais.
func TestGenerateCodeEhUniforme(t *testing.T) {
	t.Parallel()

	o := newOTP(t)
	const amostras = 30000
	const baldes = 10

	contagem := make([]int, baldes)
	vistos := map[string]int{}

	for range amostras {
		code, err := o.GenerateCode()
		require.NoError(t, err)
		contagem[code[0]-'0']++
		vistos[code]++
	}

	esperado := amostras / baldes
	for digito, n := range contagem {
		// Tolerância folgada: o objetivo é pegar viés grosseiro, não medir
		// qui-quadrado.
		assert.InDelta(t, esperado, n, float64(esperado)*0.15,
			"primeiro dígito %d aparece com frequência enviesada", digito)
	}

	// Zeros à esquerda TÊM de acontecer.
	assert.Positive(t, contagem[0], "o código 0xxxxx precisa ser possível")

	// Nenhum valor pode dominar.
	for code, n := range vistos {
		assert.Less(t, n, 30, "código %q repetiu demais", code)
	}
}

func TestHashNaoRevelaOCodigo(t *testing.T) {
	t.Parallel()

	o := newOTP(t)
	const code = "042317"
	h := o.Hash(auth.PurposeEmailVerification, "bruno@exemplo.com", code)

	assert.Len(t, h, 64, "SHA-256 em hex")
	assert.NotContains(t, h, code)
	assert.Regexp(t, `^[0-9a-f]{64}$`, h)
}

// docs/SEGURANCA.md §1.1: o propósito faz parte do que é validado — um código
// de verificação de e-mail NUNCA pode trocar senha (critério de aceite 21).
func TestHashDependeDePropositoEEmail(t *testing.T) {
	t.Parallel()

	o := newOTP(t)
	const code = "123456"

	base := o.Hash(auth.PurposeEmailVerification, "bruno@exemplo.com", code)
	outroProposito := o.Hash(auth.PurposePasswordReset, "bruno@exemplo.com", code)
	outroEmail := o.Hash(auth.PurposeEmailVerification, "outro@exemplo.com", code)
	outroCodigo := o.Hash(auth.PurposeEmailVerification, "bruno@exemplo.com", "654321")

	assert.NotEqual(t, base, outroProposito)
	assert.NotEqual(t, base, outroEmail)
	assert.NotEqual(t, base, outroCodigo)

	assert.False(t, o.Verify(auth.PurposePasswordReset, "bruno@exemplo.com", code, base))
	assert.False(t, o.Verify(auth.PurposeEmailVerification, "outro@exemplo.com", code, base))
	assert.True(t, o.Verify(auth.PurposeEmailVerification, "bruno@exemplo.com", code, base))
}

// O separador 0x00 impede colisão entre concatenações diferentes:
// ("ab","c") e ("a","bc") não podem gerar o mesmo HMAC.
func TestHashNaoColideEntreConcatenacoes(t *testing.T) {
	t.Parallel()

	o := newOTP(t)
	a := o.Hash("ab", "c@x.com", "123456")
	b := o.Hash("a", "bc@x.com", "123456")
	assert.NotEqual(t, a, b)
}

func TestHashEhInsensivelACaixaDoEmail(t *testing.T) {
	t.Parallel()

	o := newOTP(t)
	a := o.Hash(auth.PurposeEmailVerification, "Bruno@Exemplo.com", "123456")
	b := o.Hash(auth.PurposeEmailVerification, "bruno@exemplo.com", "123456")
	assert.Equal(t, a, b)
}

func TestPepperDiferenteMudaOHash(t *testing.T) {
	t.Parallel()

	a := newOTP(t)
	outro, err := auth.NewOTP("outro-pepper-de-teste-0123456789abcd")
	require.NoError(t, err)

	assert.NotEqual(t,
		a.Hash(auth.PurposeEmailVerification, "bruno@exemplo.com", "123456"),
		outro.Hash(auth.PurposeEmailVerification, "bruno@exemplo.com", "123456"),
		"sem pepper próprio, um vazamento do banco permitiria força bruta offline",
	)
}

func TestVerifyRejeitaFormatoInvalido(t *testing.T) {
	t.Parallel()

	o := newOTP(t)
	stored := o.Hash(auth.PurposeEmailVerification, "bruno@exemplo.com", "123456")

	assert.False(t, o.Verify(auth.PurposeEmailVerification, "bruno@exemplo.com", "12345", stored))
	assert.False(t, o.Verify(auth.PurposeEmailVerification, "bruno@exemplo.com", "1234567", stored))
	assert.False(t, o.Verify(auth.PurposeEmailVerification, "bruno@exemplo.com", "12345a", stored))
	assert.False(t, o.Verify(auth.PurposeEmailVerification, "bruno@exemplo.com", "", stored))
	assert.False(t, o.Verify(auth.PurposeEmailVerification, "bruno@exemplo.com", "123456", ""))
}

func TestValidCodeFormat(t *testing.T) {
	t.Parallel()

	validos := []string{"000000", "123456", "999999", "000123"}
	for _, c := range validos {
		assert.True(t, auth.ValidCodeFormat(c), "%q deveria ser válido", c)
	}
	invalidos := []string{"", "12345", "1234567", "12345 ", "12345a", "-12345", "١٢٣٤٥٦"}
	for _, c := range invalidos {
		assert.False(t, auth.ValidCodeFormat(c), "%q deveria ser inválido", c)
	}
}

// §7 da spec 0001: o e-mail em claro nunca entra no mapa do limitador.
func TestAccountKeyNaoContemEmail(t *testing.T) {
	t.Parallel()

	o := newOTP(t)
	k := o.AccountKey("login", "Bruno@Exemplo.com")

	assert.NotContains(t, k, "bruno")
	assert.NotContains(t, k, "exemplo")
	assert.Regexp(t, `^[0-9a-f]{64}$`, k)

	assert.Equal(t, k, o.AccountKey("login", " bruno@exemplo.com "), "normaliza caixa e espaços")
	assert.NotEqual(t, k, o.AccountKey("forgot_password", "bruno@exemplo.com"), "escopos são independentes")
	assert.NotEqual(t, k, o.AccountKey("login", "outro@exemplo.com"))
}

func TestValidPurpose(t *testing.T) {
	t.Parallel()

	assert.True(t, auth.ValidPurpose(auth.PurposeEmailVerification))
	assert.True(t, auth.ValidPurpose(auth.PurposePasswordReset))
	assert.False(t, auth.ValidPurpose("outra_coisa"))
	assert.False(t, auth.ValidPurpose(""))
}
