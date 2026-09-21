package transaction_test

import (
	"encoding/base64"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCursorVaiEVoltaIntacto(t *testing.T) {
	t.Parallel()

	original := transaction.Cursor{
		OccurredOn: civil.MustNew(2026, 8, 4),
		ID:         "0190f3a1-2b3c-7d4e-8f90-112233445566",
	}
	texto := transaction.EncodeCursor(original)
	require.NotEmpty(t, texto)
	assert.NotContains(t, texto, "=", "sem padding: o cursor viaja em query string")

	volta, err := transaction.ParseCursor(texto)
	require.NoError(t, err)
	assert.Equal(t, original, volta)
}

func TestCursorVazioEhAPrimeiraPaginaENaoUmErro(t *testing.T) {
	t.Parallel()

	c, err := transaction.ParseCursor("")
	require.NoError(t, err)
	assert.True(t, c.OccurredOn.IsZero())
	assert.Empty(t, transaction.EncodeCursor(transaction.Cursor{}))
}

// S5 do PLANOS.md: cursor forjado é 400 SEM detalhe. Aqui provamos a recusa; a
// ausência de detalhe é do handler, que traduz ErrInvalidCursor numa mensagem
// genérica.
func TestCursorForjadoEhRecusado(t *testing.T) {
	t.Parallel()

	b64 := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

	casos := map[string]string{
		"não é base64":               "isto!nao@e#base64",
		"base64 de lixo":             b64("qualquer coisa"),
		"sem o campo de data":        b64("id=0190f3a1-2b3c-7d4e-8f90-112233445566"),
		"data inválida":              b64("occ=2026-13-45|id=0190f3a1-2b3c-7d4e-8f90-112233445566"),
		"data sem zero à esquerda":   b64("occ=2026-8-4|id=0190f3a1-2b3c-7d4e-8f90-112233445566"),
		"id que não é uuid":          b64("occ=2026-08-04|id=1 OR 1=1"),
		"id com aspas":               b64(`occ=2026-08-04|id="; DROP TABLE transactions--`),
		"id com tamanho de uuid":     b64("occ=2026-08-04|id=zzzzzzzz-zzzz-zzzz-zzzz-zzzzzzzzzzzz"),
		"separador repetido":         b64("occ=2026-08-04|id=0190f3a1-2b3c-7d4e-8f90-112233445566|id=outro"),
		"com padding (não é RawURL)": base64.URLEncoding.EncodeToString([]byte("occ=2026-08-04|id=0190f3a1-2b3c-7d4e-8f90-11223344556")),
	}

	for nome, forjado := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			_, err := transaction.ParseCursor(forjado)
			require.ErrorIs(t, err, transaction.ErrInvalidCursor)
		})
	}
}

func TestCursorGiganteEhRecusadoAntesDeDecodificar(t *testing.T) {
	t.Parallel()

	gigante := make([]byte, 100_000)
	for i := range gigante {
		gigante[i] = 'A'
	}
	_, err := transaction.ParseCursor(string(gigante))
	require.ErrorIs(t, err, transaction.ErrInvalidCursor)
}
