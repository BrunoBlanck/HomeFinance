package investment

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A aritmética de mês é o lugar em que um `num--` desprotegido produz o "mês
// zero" e a série de 12 passa a mentir na virada do ano — que é exatamente a
// fronteira que ninguém confere à mão em janeiro.

func TestMenosAtravessaAViradaDoAno(t *testing.T) {
	t.Parallel()

	casos := []struct {
		de     mes
		n      int
		espera string
	}{
		{mes{2026, 9}, 0, "2026-09"},
		{mes{2026, 9}, 1, "2026-08"},
		{mes{2026, 9}, 11, "2025-10"},
		{mes{2026, 1}, 1, "2025-12"},
		{mes{2026, 1}, 11, "2025-02"},
		{mes{2026, 1}, 12, "2025-01"},
		{mes{2026, 1}, 13, "2024-12"},
		{mes{2026, 12}, 11, "2026-01"},
		{mes{2026, 12}, 12, "2025-12"},
		// Recuo de mais de um ano inteiro, onde o resto negativo aparece.
		{mes{2026, 3}, 27, "2023-12"},
	}
	for _, c := range casos {
		t.Run(c.de.String()+"-"+c.espera, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.espera, c.de.menos(c.n).String())
		})
	}
}

func TestMaisEhOInversoDeMenos(t *testing.T) {
	t.Parallel()

	for _, inicio := range []mes{{2026, 1}, {2026, 6}, {2026, 12}, {2025, 2}} {
		for n := range 30 {
			assert.Equal(t, inicio.String(), inicio.menos(n).mais(n).String(),
				"ida e volta de %d meses não fecha", n)
		}
	}
}

// O ano é escrito com QUATRO dígitos sempre: sem o zero à esquerda, "999-12"
// compararia MAIOR que "1000-01" numa ordenação de texto — e a comparação
// lexicográfica é justamente o que substitui as funções de data no SQL.
func TestFormaCanonicaTemQuatroDigitosDeAno(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "0999-12", mes{999, 12}.String())
	assert.Equal(t, "0001-01", mes{1, 1}.String())
	assert.Less(t, mes{999, 12}.String(), mes{1000, 1}.String())
}

// A janela do ANO está sempre contida na dos 12 meses — é isso que permite UMA
// consulta em vez de duas. A propriedade é verificada para os 12 meses
// possíveis, e não só para o caso bonito de setembro.
func TestJanelaDoAnoCabeSempreNaJanelaDaSerie(t *testing.T) {
	t.Parallel()

	for num := 1; num <= 12; num++ {
		alvo := mes{2026, num}
		primeiro, ultimo := janelaDaSerie(alvo)
		inicioDoAno := primeiroMesDoAno(alvo)

		assert.LessOrEqual(t, primeiro.String(), inicioDoAno.String(),
			"a série começa DEPOIS de 1º de janeiro para %s — o ano-até-o-mês não caberia", alvo)
		assert.LessOrEqual(t, inicioDoAno.String(), ultimo.String())
		assert.Equal(t, alvo.String(), ultimo.String())
	}
}

func TestSomaSeguraRecusaNegativoEEstouro(t *testing.T) {
	t.Parallel()

	soma, ok := somaSegura(2, 3)
	require.True(t, ok)
	assert.EqualValues(t, 5, soma)

	_, ok = somaSegura(-1, 3)
	assert.False(t, ok, "negativo é banco em estado que a aplicação não produz: falha fechada")

	_, ok = somaSegura(1, -1)
	assert.False(t, ok)

	_, ok = somaSegura(math.MaxInt64, 1)
	assert.False(t, ok)

	soma, ok = somaSegura(math.MaxInt64, 0)
	require.True(t, ok)
	assert.EqualValues(t, math.MaxInt64, soma)
}
