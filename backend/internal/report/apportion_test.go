package report

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Teste interno (package report) de propósito: apportion não é exportado e
// não deve ser — é detalhe do ADR-027c, não API do pacote.

func soma(xs []int64) int64 {
	var s int64
	for _, x := range xs {
		s += x
	}
	return s
}

// invariantes confere o que TODA repartição precisa cumprir, seja qual for a
// entrada: soma exata, nada negativo, peso zero sem fatia, ordem preservada.
func invariantes(t *testing.T, weights []int64, total int64, out []int64) {
	t.Helper()
	require.Len(t, out, len(weights))

	var somaPesos int64
	for _, w := range weights {
		if w > 0 {
			somaPesos += w
		}
	}
	if somaPesos > 0 && total > 0 {
		assert.Equal(t, total, soma(out), "Σ partes == total")
	} else {
		assert.Equal(t, int64(0), soma(out), "sem peso ou sem total: tudo zero")
	}
	for i := range out {
		assert.GreaterOrEqual(t, out[i], int64(0), "parte %d negativa", i)
		if weights[i] <= 0 {
			assert.Equal(t, int64(0), out[i], "peso zero recebeu fatia em %d", i)
		}
	}
	for i := range weights {
		for j := range weights {
			if weights[i] > weights[j] {
				assert.GreaterOrEqual(t, out[i], out[j], "ordem invertida entre %d (peso %d) e %d (peso %d)", i, weights[i], j, weights[j])
			}
		}
	}
}

func TestApportionCasosDeTabela(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome     string
		weights  []int64
		total    int64
		esperado []int64
	}{
		{"vazio", nil, 10000, []int64{}},
		{"um só", []int64{5}, 10000, []int64{10000}},
		{"total zero", []int64{1, 2, 3}, 0, []int64{0, 0, 0}},
		{"total negativo", []int64{1, 2, 3}, -5, []int64{0, 0, 0}},
		{"Σw zero", []int64{0, 0, 0}, 10000, []int64{0, 0, 0}},
		{"peso negativo é zero", []int64{-7, 3}, 10000, []int64{0, 10000}},
		// 1/3 cada: 3333 + sobra 1 → maior resto empata; maior peso empata;
		// menor índice ganha.
		{"terços", []int64{1, 1, 1}, 10000, []int64{3334, 3333, 3333}},
		// 2/3 e 1/3: 6666,67 e 3333,33 → sobra 1 vai para o maior resto (.67).
		{"dois terços", []int64{2, 1}, 10000, []int64{6667, 3333}},
		// Peso zero no meio nunca recebe sobra, mesmo com sobra disponível.
		{"zero no meio", []int64{1, 0, 1, 1}, 10000, []int64{3334, 0, 3333, 3333}},
		// Metades exatas: sem sobra.
		{"metades", []int64{50, 50}, 10000, []int64{5000, 5000}},
		// Empate de resto com pesos diferentes: 1/4·10 = 2,5 e 3/4·10 = 7,5 —
		// o maior peso leva a sobra.
		{"empate de resto, maior peso ganha", []int64{1, 3}, 10, []int64{2, 8}},
		// Sete iguais em 100: 14 cada (98) + 2 de sobra aos dois primeiros.
		{"sete iguais", []int64{1, 1, 1, 1, 1, 1, 1}, 100, []int64{15, 15, 14, 14, 14, 14, 14}},
		// Total pequeno com muitas partes: só as maiores fatias recebem.
		{"total menor que n", []int64{5, 4, 3, 2, 1}, 2, []int64{1, 1, 0, 0, 0}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			out := apportion(c.weights, c.total)
			assert.Equal(t, c.esperado, out)
			invariantes(t, c.weights, c.total, out)
		})
	}
}

// Valores perto de MaxAmountCents × 200 categorias: centavos × 10000 passa
// de int64 (≈ 2·10^17 × 10^4 = 2·10^21 > 9,2·10^18) — é exatamente o caso
// em que a multiplicação em 64 bits estouraria. Aqui não estoura nem entra em
// pânico, e a soma fecha.
func TestApportionPertoDoTetoNaoEstoura(t *testing.T) {
	t.Parallel()

	const n = 200
	weights := make([]int64, n)
	for i := range weights {
		weights[i] = transaction.MaxAmountCents * 200 // R$ 200 bilhões por parte
	}
	weights[0] = transaction.MaxAmountCents*200 + 1 // desempata a sobra

	out := apportion(weights, BasisPointsTotal)
	invariantes(t, weights, BasisPointsTotal, out)
	assert.Equal(t, int64(BasisPointsTotal), soma(out))
}

// Pesos que somam mais do que uint64 comporta: sem panic, tudo zero. Em
// produção o serviço recusa antes (errTotalOverflow); aqui é o invariante
// "sem panic" independente do chamador.
func TestApportionSomaDePesosEstouradaDevolveZeros(t *testing.T) {
	t.Parallel()

	weights := []int64{math.MaxInt64, math.MaxInt64, math.MaxInt64}
	assert.NotPanics(t, func() {
		out := apportion(weights, BasisPointsTotal)
		assert.Equal(t, []int64{0, 0, 0}, out)
	})
}

// Um peso só igual a MaxInt64 é o caso extremo de hi:lo = w × 10000 com
// hi ≠ 0 e Σw = w — o quociente é exatamente `total`.
func TestApportionPesoMaximo(t *testing.T) {
	t.Parallel()

	out := apportion([]int64{math.MaxInt64, 1}, BasisPointsTotal)
	assert.Equal(t, int64(BasisPointsTotal), soma(out))
	assert.Equal(t, int64(BasisPointsTotal), out[0])
	assert.Equal(t, int64(0), out[1])
}

// Propriedade (critério 9): pesos aleatórios com semente fixa — soma exata,
// nada negativo, ordem preservada — e os DOIS níveis fecham: a fatia de cada
// grupo repartida entre as suas partes soma a fatia do grupo.
func TestApportionPropriedades(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(2026, 917))
	for iter := 0; iter < 2000; iter++ {
		n := 1 + rng.IntN(60)
		weights := make([]int64, n)
		for i := range weights {
			switch rng.IntN(6) {
			case 0:
				weights[i] = 0
			case 1:
				weights[i] = int64(rng.IntN(100))
			case 2:
				weights[i] = rng.Int64N(transaction.MaxAmountCents + 1)
			default:
				weights[i] = rng.Int64N(10_000_000)
			}
		}
		total := int64(BasisPointsTotal)
		if rng.IntN(4) == 0 {
			total = rng.Int64N(BasisPointsTotal + 1)
		}

		nivel1 := apportion(weights, total)
		invariantes(t, weights, total, nivel1)
		if t.Failed() {
			t.Fatalf("iteração %d: weights=%v total=%d out=%v", iter, weights, total, nivel1)
		}

		// Segundo nível: cada parte vira um grupo com 1..5 filhas + direto.
		for g := range weights {
			m := 1 + rng.IntN(5)
			partes := make([]int64, m+1)
			var resto int64 = weights[g]
			for i := 0; i < m; i++ {
				if resto > 0 {
					partes[i] = rng.Int64N(resto + 1)
					resto -= partes[i]
				}
			}
			partes[m] = resto // o "direto" fecha o peso do grupo
			nivel2 := apportion(partes, nivel1[g])
			invariantes(t, partes, nivel1[g], nivel2)
			if nivel1[g] > 0 && soma(partes) > 0 {
				assert.Equal(t, nivel1[g], soma(nivel2), "iteração %d grupo %d: nível 2 não fecha", iter, g)
			}
			if t.Failed() {
				t.Fatalf("iteração %d grupo %d: partes=%v share=%d out=%v", iter, g, partes, nivel1[g], nivel2)
			}
		}
	}
}

// Determinismo: a mesma entrada produz sempre a mesma saída (a ordenação da
// sobra é estável e não depende de map).
func TestApportionDeterministico(t *testing.T) {
	t.Parallel()

	weights := []int64{7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7}
	primeiro := apportion(weights, 100)
	for i := 0; i < 50; i++ {
		assert.Equal(t, primeiro, apportion(weights, 100))
	}
	// 100/11 = 9,09 → 9 cada (99) + 1 de sobra ao primeiro índice.
	assert.Equal(t, []int64{10, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9}, primeiro)
}
