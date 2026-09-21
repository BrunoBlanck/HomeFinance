package report

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

// somaSegura é a única guarda entre "a soma do mês não cabe em int64" e um
// total inventado na resposta. O contrato é estreito de propósito: só soma
// valores NÃO NEGATIVOS, e qualquer outra coisa é recusada — inclusive o caso
// que a versão anterior errava, `a < 0`, em que `math.MaxInt64-a` transborda e
// a função passaria a recusar somas perfeitamente válidas.
func TestSomaSegura(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome   string
		a, b   int64
		quer   int64
		querOK bool
	}{
		{"zeros", 0, 0, 0, true},
		{"soma comum", 1_000, 2_345, 3_345, true},
		{"b zero no limite", math.MaxInt64, 0, math.MaxInt64, true},
		{"a zero no limite", 0, math.MaxInt64, math.MaxInt64, true},
		{"encosta no limite", math.MaxInt64 - 1, 1, math.MaxInt64, true},
		{"passa do limite por um", math.MaxInt64, 1, 0, false},
		{"passa do limite por muito", math.MaxInt64 - 10, 11, 0, false},
		{"dois máximos", math.MaxInt64, math.MaxInt64, 0, false},
		{"a negativo", -1, 5, 0, false},
		{"b negativo", 5, -1, 0, false},
		{"os dois negativos", -5, -5, 0, false},
		{"a no mínimo", math.MinInt64, 0, 0, false},
		{"b no mínimo", 0, math.MinInt64, 0, false},
		// O caso que denuncia o bug antigo: com a = -1 o teste
		// `b > math.MaxInt64-a` era sempre verdadeiro, então até b = 1 (soma
		// 0, trivialmente representável) era recusado. Recusar continua sendo
		// o certo — mas por ser entrada inválida, não por "não coube".
		{"negativo cuja soma caberia", -1, 1, 0, false},
		{"negativo que cancelaria o máximo", math.MinInt64, math.MaxInt64, 0, false},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			got, ok := somaSegura(c.a, c.b)
			assert.Equal(t, c.querOK, ok)
			assert.Equal(t, c.quer, got)
		})
	}
}

// Recusa é sempre com zero: nenhum chamador pode cair na tentação de usar o
// valor devolvido quando ok é falso.
func TestSomaSeguraRecusaDevolveZero(t *testing.T) {
	t.Parallel()
	for _, par := range [][2]int64{{math.MaxInt64, 1}, {-1, 1}, {1, -1}, {math.MinInt64, math.MinInt64}} {
		got, ok := somaSegura(par[0], par[1])
		assert.False(t, ok)
		assert.Zero(t, got)
	}
}
