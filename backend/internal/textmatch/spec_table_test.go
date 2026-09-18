package textmatch_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tabela da §3 da spec 0005 (e §3.3 do plano), linha a linha, com a
// pontuação EXATA. Cada caso é conferido por três caminhos que precisam
// concordar: a função pura ScoreKeyword, o Matcher (Rank, com índice e
// prefiltro) e a escolha final (Best).
//
// Se uma linha falhar, a fórmula da §3 é a autoridade — não a tabela nem o
// código: recalcule à mão antes de mexer em qualquer um dos dois.
func TestTabelaDaSpec(t *testing.T) {
	t.Parallel()

	type par struct {
		w, k string // o par de palavras que a regra 2 compara (para conferir C)
		c    int    // maior substring comum esperada
	}
	casos := []struct {
		nome      string
		descricao string
		keyword   string
		pontuacao int
		regra     string
		par       *par
	}{
		{"exata, token igual", "supermercado extra", "supermercado", 100, "1", nil},
		{"exata, palavra curta", "uber eats", "uber", 100, "1", nil},
		{"exata, ponto separa", "netflix.com", "netflix", 100, "1", nil},
		{"exata, frase consecutiva", "uber eats sao paulo", "uber eats", 100, "1", nil},

		{"aproximação: mercado ⊂ supermercado", "mercado do seu jose", "supermercado", 88, "2", &par{"mercado", "supermercado", 7}},
		{"aproximação: mercadinho ~ mercado", "mercadinho", "mercado", 82, "2", &par{"mercadinho", "mercado", 6}},
		{"aproximação: supermerc ⊂ supermercado", "supermerc", "supermercado", 93, "2", &par{"supermerc", "supermercado", 9}},
		{"aproximação: farmac ⊂ farmacia", "farmac", "farmacia", 93, "2", &par{"farmac", "farmacia", 6}},
		{"aproximação: mercado ⊂ hipermercado", "hipermercado", "mercado", 88, "2", &par{"hipermercado", "mercado", 7}},

		{"erro de digitação: padoria ~ padaria", "padoria", "padaria", 86, "3", &par{"padoria", "padaria", 3}},

		{"falso positivo aceito: posto ⊂ imposto", "imposto de renda", "posto", 91, "2", &par{"imposto", "posto", 5}},
		{"falso positivo aceito: amazon ⊂ amazonas", "amazonas turismo", "amazon", 93, "2", &par{"amazonas", "amazon", 6}},
		{"falso positivo aceito: mercado ⊂ mercadoria", "mercadoria", "mercado", 91, "2", &par{"mercadoria", "mercado", 7}},

		{"não casa: padaria ~ farmacia", "padaria", "farmacia", 0, "—", &par{"padaria", "farmacia", 2}},
		{"não casa: paraiba ~ padaria", "paraiba", "padaria", 0, "—", &par{"paraiba", "padaria", 2}},
		{"não casa: casa tem 4 runas", "casamento", "casa", 0, "—", nil},
		{"abaixo do limiar: drogasil ~ drogaria", "drogasil", "drogaria", 74, "2", &par{"drogasil", "drogaria", 5}},
		{"exata: pix inteiro", "pix enviado", "pix", 100, "1", nil},
		{"não casa: pix não é token inteiro", "pixel store", "pix", 0, "—", nil},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()

			if c.par != nil {
				assert.Equal(t, c.par.c, textmatch.LongestCommonSubstring([]rune(c.par.w), []rune(c.par.k)),
					"maior substring comum de %q e %q", c.par.w, c.par.k)
			}

			// Caminho 1: a função pura.
			desc := textmatch.Tokenize(c.descricao)
			kw := textmatch.Tokenize(c.keyword)
			assert.Equal(t, c.pontuacao, textmatch.ScoreKeyword(desc, kw), "ScoreKeyword (regra %s)", c.regra)

			// Caminho 2: o Matcher com índice e prefiltro.
			m, err := textmatch.NewMatcher([]textmatch.Keyword{{OwnerID: "A", Keyword: c.keyword}})
			require.NoError(t, err)
			rank := rankOK(m.Rank(c.descricao))
			if c.pontuacao == 0 {
				assert.Empty(t, rank, "Rank não deve listar dono com pontuação 0")
			} else {
				require.Len(t, rank, 1)
				assert.Equal(t, textmatch.Match{OwnerID: "A", Keyword: c.keyword, Score: c.pontuacao}, rank[0])
			}

			// Caminho 3: a escolha.
			best := bestOK(m.Best(c.descricao))
			if c.pontuacao >= textmatch.MinScore {
				assert.True(t, best.Matched())
				assert.Equal(t, c.pontuacao, best.Match.Score)
				assert.Equal(t, c.keyword, best.Match.Keyword)
			} else {
				assert.Equal(t, textmatch.ReasonBelowThreshold, best.Reason)
				assert.Equal(t, textmatch.Match{}, best.Match, "Match fica zerado quando não há sugestão")
			}
		})
	}
}

// A regra 3 exige as DUAS palavras com ≥ 6 runas e distância exatamente 1;
// os exemplos da tabela conferem a distância que as pontuações pressupõem.
func TestTabelaDaSpecDistanciaDeEdicao(t *testing.T) {
	t.Parallel()

	assert.True(t, textmatch.EditDistanceIsOne([]rune("padoria"), []rune("padaria")))
	assert.False(t, textmatch.EditDistanceIsOne([]rune("padaria"), []rune("farmacia")))
	assert.False(t, textmatch.EditDistanceIsOne([]rune("paraiba"), []rune("padaria")))
	assert.False(t, textmatch.EditDistanceIsOne([]rune("drogasil"), []rune("drogaria")), "duas substituições")
	assert.False(t, textmatch.EditDistanceIsOne([]rune("padaria"), []rune("padaria")), "iguais é distância 0")
}

// Os casos de ESCOLHA da §3.3: donos diferentes disputando a mesma descrição.
func TestTabelaDaSpecEscolha(t *testing.T) {
	t.Parallel()

	novo := func(t *testing.T, kws ...textmatch.Keyword) *textmatch.Matcher {
		t.Helper()
		m, err := textmatch.NewMatcher(kws)
		require.NoError(t, err)
		return m
	}

	t.Run("empate 100 x 100 entre donos diferentes é ambíguo", func(t *testing.T) {
		t.Parallel()
		m := novo(t,
			textmatch.Keyword{OwnerID: "A", Keyword: "padaria"},
			textmatch.Keyword{OwnerID: "B", Keyword: "central"},
		)
		assert.Equal(t, []textmatch.Match{
			{OwnerID: "A", Keyword: "padaria", Score: 100},
			{OwnerID: "B", Keyword: "central", Score: 100},
		}, rankOK(m.Rank("padaria central")))

		r := bestOK(m.Best("padaria central"))
		assert.Equal(t, textmatch.ReasonAmbiguous, r.Reason)
		assert.False(t, r.Matched())
		assert.Equal(t, textmatch.Match{}, r.Match)
	})

	t.Run("mercadinho: mercado (82) vence supermercado (69)", func(t *testing.T) {
		t.Parallel()
		// supermercado x mercadinho: C=6, LM=12, Lm=10 → 12000 − 1800 − 1920 = 8280 / 120 = 69.
		assert.Equal(t, 69, textmatch.ScoreWord([]rune("mercadinho"), []rune("supermercado")))

		m := novo(t,
			textmatch.Keyword{OwnerID: "A", Keyword: "mercado"},
			textmatch.Keyword{OwnerID: "B", Keyword: "supermercado"},
		)
		assert.Equal(t, []textmatch.Match{
			{OwnerID: "A", Keyword: "mercado", Score: 82},
			{OwnerID: "B", Keyword: "supermercado", Score: 69},
		}, rankOK(m.Rank("mercadinho")))

		r := bestOK(m.Best("mercadinho"))
		require.True(t, r.Matched())
		assert.Equal(t, textmatch.Match{OwnerID: "A", Keyword: "mercado", Score: 82}, r.Match)
	})

	t.Run("posto shell: regra 1 (posto) vence a regra 2 (imposto, 91)", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, 91, textmatch.ScoreWord([]rune("posto"), []rune("imposto")))

		m := novo(t,
			textmatch.Keyword{OwnerID: "A", Keyword: "posto"},
			textmatch.Keyword{OwnerID: "B", Keyword: "imposto"},
		)
		assert.Equal(t, []textmatch.Match{
			{OwnerID: "A", Keyword: "posto", Score: 100},
			{OwnerID: "B", Keyword: "imposto", Score: 91},
		}, rankOK(m.Rank("posto shell")))

		r := bestOK(m.Best("posto shell"))
		require.True(t, r.Matched())
		assert.Equal(t, textmatch.Match{OwnerID: "A", Keyword: "posto", Score: 100}, r.Match)
	})

	t.Run("imposto de renda: cadastrar imposto faz o 100 vencer o 91", func(t *testing.T) {
		t.Parallel()
		m := novo(t,
			textmatch.Keyword{OwnerID: "A", Keyword: "posto"},
			textmatch.Keyword{OwnerID: "B", Keyword: "imposto"},
		)
		assert.Equal(t, []textmatch.Match{
			{OwnerID: "B", Keyword: "imposto", Score: 100},
			{OwnerID: "A", Keyword: "posto", Score: 91},
		}, rankOK(m.Rank("imposto de renda")))

		r := bestOK(m.Best("imposto de renda"))
		require.True(t, r.Matched())
		assert.Equal(t, textmatch.Match{OwnerID: "B", Keyword: "imposto", Score: 100}, r.Match)
	})
}
