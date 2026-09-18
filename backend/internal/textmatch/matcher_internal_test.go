package textmatch

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Geradores determinísticos (semente fixa: a falha de hoje reproduz amanhã).
// ---------------------------------------------------------------------------

const alfabeto = "abcdefghijklmnopqrstuvwxyz"

var (
	consoantes = []rune("bcdfglmnprstvz")
	vogais     = []rune("aeiou")
)

func novoRNG(t testing.TB) *rand.Rand {
	const semente1, semente2 = 2026, 917
	t.Logf("rand.NewPCG(%d, %d)", semente1, semente2)
	return rand.New(rand.NewPCG(semente1, semente2))
}

// palavraAleatoria: letras uniformes de a–z, entre minLen e maxLen runas.
func palavraAleatoria(rng *rand.Rand, minLen, maxLen int) []rune {
	n := minLen + rng.IntN(maxLen-minLen+1)
	w := make([]rune, n)
	for i := range w {
		w[i] = rune(alfabeto[rng.IntN(len(alfabeto))])
	}
	return w
}

// palavraSilabica gera algo parecido com português (consoante+vogal), que é
// o que produz trigramas repetidos entre palavras — o cenário realista para
// o prefiltro e para a carga.
func palavraSilabica(rng *rand.Rand, minSil, maxSil int) string {
	n := minSil + rng.IntN(maxSil-minSil+1)
	var b strings.Builder
	for range n {
		b.WriteRune(consoantes[rng.IntN(len(consoantes))])
		b.WriteRune(vogais[rng.IntN(len(vogais))])
		// Às vezes uma consoante extra fecha a sílaba ("mer", "cas").
		if rng.IntN(4) == 0 {
			b.WriteRune(consoantes[rng.IntN(len(consoantes))])
		}
	}
	return b.String()
}

// umaEdicao aplica exatamente UMA operação OSA a `a` e garante que o
// resultado é diferente de `a`.
func umaEdicao(rng *rand.Rand, a []rune) []rune {
	for {
		b := append([]rune(nil), a...)
		switch rng.IntN(4) {
		case 0: // substituição por letra diferente
			i := rng.IntN(len(b))
			for {
				r := rune(alfabeto[rng.IntN(len(alfabeto))])
				if r != b[i] {
					b[i] = r
					break
				}
			}
		case 1: // inserção
			i := rng.IntN(len(b) + 1)
			r := rune(alfabeto[rng.IntN(len(alfabeto))])
			b = append(b[:i], append([]rune{r}, b[i:]...)...)
		case 2: // remoção
			i := rng.IntN(len(b))
			b = append(b[:i], b[i+1:]...)
		case 3: // transposição de vizinhas diferentes
			i := rng.IntN(len(b) - 1)
			if b[i] == b[i+1] {
				continue
			}
			b[i], b[i+1] = b[i+1], b[i]
		}
		if !runesEqual(a, b) {
			return b
		}
	}
}

// osaDistance é a DP completa de referência (Damerau-Levenshtein restrita),
// usada só para conferir o atalho de EditDistanceIsOne.
func osaDistance(a, b []rune) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			custo := 1
			if a[i-1] == b[j-1] {
				custo = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+custo)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(a)][len(b)]
}

func compartilhamTrigrama(a, b []rune) bool {
	ta := paddedTrigrams(a)
	for _, tri := range paddedTrigrams(b) {
		for _, x := range ta {
			if x == tri {
				return true
			}
		}
	}
	return false
}

// cenarioDeCarga monta um Matcher com nKeywords palavras-chave (10% com duas
// palavras) distribuídas entre nOwners donos, e nDistinct descrições que
// exercitam as três regras: 30% contêm uma palavra-chave inteira, 20% uma
// com um erro de digitação, 20% uma com prefixo/sufixo, 30% só ruído.
func cenarioDeCarga(tb testing.TB, rng *rand.Rand, nKeywords, nOwners, nDistinct int) (*Matcher, []string) {
	tb.Helper()

	vistas := make(map[string]struct{}, nKeywords)
	kws := make([]Keyword, 0, nKeywords)
	palavras := make([]string, 0, nKeywords)
	for len(kws) < nKeywords {
		p := palavraSilabica(rng, 2, 5)
		if rng.IntN(10) == 0 {
			p += " " + palavraSilabica(rng, 2, 3)
		}
		if _, dup := vistas[p]; dup {
			continue
		}
		vistas[p] = struct{}{}
		kws = append(kws, Keyword{OwnerID: fmt.Sprintf("owner-%03d", len(kws)%nOwners), Keyword: p, Norm: p})
		palavras = append(palavras, p)
	}
	m, err := NewMatcher(kws)
	require.NoError(tb, err)

	descs := make([]string, nDistinct)
	for i := range descs {
		n := 2 + rng.IntN(3)
		partes := make([]string, 0, n+1)
		for range n {
			base := palavras[rng.IntN(len(palavras))]
			switch rng.IntN(10) {
			case 0, 1, 2:
				partes = append(partes, base)
			case 3, 4:
				partes = append(partes, string(umaEdicao(rng, []rune(strings.Fields(base)[0]))))
			case 5, 6:
				partes = append(partes, palavraSilabica(rng, 1, 1)+strings.Fields(base)[0])
			default:
				partes = append(partes, palavraSilabica(rng, 2, 4))
			}
		}
		if rng.IntN(3) == 0 {
			partes = append(partes, "de", fmt.Sprintf("%d", rng.IntN(100000)))
		}
		descs[i] = strings.Join(partes, " ")
	}
	return m, descs
}

// ---------------------------------------------------------------------------
// Helpers do orçamento de trabalho
// ---------------------------------------------------------------------------

// rankOK e bestOK conferem que a chamada NÃO estourou o orçamento de trabalho
// e devolvem o valor.
//
// Não recebem testing.TB porque Go não deixa misturar um argumento extra com
// o par (valor, erro) de outra chamada — e, aqui, isso é uma vantagem: eles
// são chamados também de goroutines que não são a do teste (o teste de
// concorrência), onde require.FailNow seria uso indevido de testing. Estourar
// o orçamento num cenário destes é defeito do teste, não caso de borda: o
// pânico falha alto, com pilha, em qualquer goroutine.
func rankOK(rank []Match, err error) []Match {
	if err != nil {
		panic("orçamento de trabalho estourado num cenário que não devia estourar: " + err.Error())
	}
	return rank
}

func bestOK(r Result, err error) Result {
	if err != nil {
		panic("orçamento de trabalho estourado num cenário que não devia estourar: " + err.Error())
	}
	return r
}

// ---------------------------------------------------------------------------
// Propriedades
// ---------------------------------------------------------------------------

// A invariante que sustenta o prefiltro: TODO par que as regras 2 ou 3
// aceitariam compartilha ao menos um trigrama da forma preenchida. Se isto
// falhar, o Matcher devolveria menos do que a força bruta — e a tabela da
// spec deixaria de valer para a versão indexada.
func TestPrefiltroNuncaExcluiParValido(t *testing.T) {
	t.Parallel()
	rng := novoRNG(t)

	t.Run("OSA = 1 em palavras de 6 a 12 runas", func(t *testing.T) {
		for i := range 10_000 {
			// Remoção numa palavra de 6 deixa 5 — abaixo do mínimo da regra 3;
			// começa em 7 para o par ficar sempre dentro da regra.
			a := palavraAleatoria(rng, 7, 12)
			b := umaEdicao(rng, a)
			require.Truef(t, EditDistanceIsOne(a, b), "#%d: %q ~ %q deveria estar a distância 1", i, string(a), string(b))
			require.Truef(t, compartilhamTrigrama(a, b), "#%d: %q ~ %q (OSA=1) sem trigrama comum", i, string(a), string(b))
			require.Greaterf(t, ScoreWord(a, b), 0, "#%d: %q ~ %q deveria pontuar", i, string(a), string(b))
			require.Equal(t, ScoreWord(a, b), ScoreWord(b, a), "ScoreWord é simétrica")
		}
	})

	t.Run("substring comum ≥ 5", func(t *testing.T) {
		for i := range 10_000 {
			core := palavraAleatoria(rng, 5, 8)
			a := append(append(palavraAleatoria(rng, 0, 4), core...), palavraAleatoria(rng, 0, 4)...)
			b := append(append(palavraAleatoria(rng, 0, 4), core...), palavraAleatoria(rng, 0, 4)...)
			require.GreaterOrEqualf(t, LongestCommonSubstring(a, b), MinFuzzyRunes, "#%d: %q ~ %q", i, string(a), string(b))
			require.Truef(t, compartilhamTrigrama(a, b), "#%d: %q ~ %q (LCS ≥ 5) sem trigrama comum", i, string(a), string(b))
			require.Greaterf(t, ScoreWord(a, b), 0, "#%d: %q ~ %q deveria pontuar", i, string(a), string(b))
			require.Equal(t, ScoreWord(a, b), ScoreWord(b, a), "ScoreWord é simétrica")
		}
	})

	t.Run("o exemplo do plano: transposição central em 6 runas", func(t *testing.T) {
		a, b := []rune("abcdef"), []rune("abdcef")
		assert.True(t, EditDistanceIsOne(a, b))
		assert.True(t, compartilhamTrigrama(a, b))
		assert.Equal(t, 83, ScoreWord(a, b), "100 − 100/6 = 83,3 → 83")
	})

	// A prova de que índice + prefiltro + memo não mudam NADA: o ranking
	// indexado é igual ao ranking por força bruta sobre ScoreKeyword.
	t.Run("Rank com prefiltro == força bruta", func(t *testing.T) {
		m, descs := cenarioDeCarga(t, rng, 300, 60, 2_000)
		for i, d := range descs {
			esperado := m.rankBruteForce(d)
			require.Equalf(t, esperado, rankOK(m.compute(d)), "#%d: descrição %q", i, d)
			require.Equalf(t, esperado, rankOK(m.Rank(d)), "#%d (memo): descrição %q", i, d)
		}
	})
}

// O atalho de EditDistanceIsOne precisa concordar com a DP completa em
// pares aleatórios sobre um alfabeto pequeno (que produz muitos pares
// próximos), inclusive nos casos degenerados: vazios, iguais, repetições.
func TestEditDistanceIsOneBateComDPDeReferencia(t *testing.T) {
	t.Parallel()
	rng := novoRNG(t)

	const alfabetoCurto = "abc"
	gera := func() []rune {
		n := rng.IntN(8)
		w := make([]rune, n)
		for i := range w {
			w[i] = rune(alfabetoCurto[rng.IntN(len(alfabetoCurto))])
		}
		return w
	}
	for i := range 20_000 {
		a, b := gera(), gera()
		esperado := osaDistance(a, b) == 1
		require.Equalf(t, esperado, EditDistanceIsOne(a, b), "#%d: %q ~ %q (OSA=%d)", i, string(a), string(b), osaDistance(a, b))
		require.Equal(t, EditDistanceIsOne(a, b), EditDistanceIsOne(b, a), "simétrica")
	}

	assert.False(t, EditDistanceIsOne(nil, nil))
	assert.True(t, EditDistanceIsOne([]rune("a"), nil))
	assert.False(t, EditDistanceIsOne([]rune("ab"), nil))
	assert.True(t, EditDistanceIsOne([]rune("aab"), []rune("ab")))
	assert.False(t, EditDistanceIsOne([]rune("aa"), []rune("aa")), "transposição de iguais é distância 0")
}

func TestLongestCommonSubstring(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 0, LongestCommonSubstring(nil, []rune("abc")))
	assert.Equal(t, 0, LongestCommonSubstring([]rune("abc"), []rune("xyz")))
	assert.Equal(t, 3, LongestCommonSubstring([]rune("abc"), []rune("abc")))
	assert.Equal(t, 7, LongestCommonSubstring([]rune("supermercado"), []rune("mercado")))
	assert.Equal(t, 7, LongestCommonSubstring([]rune("mercado"), []rune("supermercado")), "simétrica")
	// Substring, não subsequência: "a_c_e" contra "ace" é 1, não 3.
	assert.Equal(t, 1, LongestCommonSubstring([]rune("axcxe"), []rune("ace")))
	// Palavras acima do buffer de pilha (64) ainda funcionam.
	longa := []rune(strings.Repeat("ab", 50))
	assert.Equal(t, 100, LongestCommonSubstring(longa, longa))
	assert.Equal(t, 5, LongestCommonSubstring(longa, []rune("babab")))
}

// A PODA LITERAL DO PLANO ("pule quando LM > 3·Lm") continua proibida: com
// LM=22, Lm=7 e C=7 a conta dá 79,545…, que roundDiv leva a 80 — exatamente
// MinScore. Descartar esse par viraria um `matched` em `below_threshold`.
//
// A poda que EXISTE (MaxScoreForLengths) é outra coisa: ela calcula o teto do
// par com a mesma fórmula e o mesmo roundDiv inteiro, e só descarta quando
// esse teto arredondado fica ABAIXO de MinScore. Este teste é a regressão do
// par do limite — ele não pode ser podado.
func TestPodaPorComprimentoNaoDescartaOParDoLimite(t *testing.T) {
	t.Parallel()

	longa := strings.Repeat("x", 15) + "mercado" // 22 runas: 22 > 3·7
	require.Equal(t, 80, MaxScoreForLengths(22, 7), "o teto do par do limite é exatamente MinScore")
	require.Equal(t, 80, ScoreWord([]rune(longa), []rune("mercado")))

	m, err := NewMatcher([]Keyword{{OwnerID: "A", Keyword: "mercado"}})
	require.NoError(t, err)
	r := bestOK(m.Best(longa))
	require.True(t, r.Matched(), "80 é ≥ MinScore: precisa sugerir")
	assert.Equal(t, 80, r.Match.Score)
	assert.Equal(t, m.rankBruteForce(longa), rankOK(m.compute(longa)))
}

// PROVA DE INOCUIDADE DA PODA, por força bruta sobre TODO par de comprimentos
// que o produto admite: palavra-chave de 1..MaxKeywordRunes runas contra token
// de descrição de 1..MaxDescriptionRunes runas.
//
// Para cada par (lM, lm): se MaxScoreForLengths o poda, então NENHUM C
// possível — de MinFuzzyRunes até lm — alcança MinScore pela regra 2, e a
// regra 3 também não pode valer (ela exige |lM − lm| ≤ 1). Se não o poda, o
// teto declarado é de fato o máximo alcançável.
func TestPodaPorComprimentoEhInocua(t *testing.T) {
	t.Parallel()

	// 140 runas é MaxDescriptionLen de internal/transaction: é o maior token
	// que uma descrição pode produzir.
	const maxDescricao = 140

	podados, livres := 0, 0
	for lM := 1; lM <= maxDescricao; lM++ {
		for lm := 1; lm <= MaxKeywordRunes && lm <= lM; lm++ {
			teto := MaxScoreForLengths(lM, lm)

			// O MAIOR score que a regra 2 alcança, por força bruta sobre C.
			melhorRegra2 := 0
			for c := MinFuzzyRunes; c <= lm; c++ {
				num := 100*lM*lm - 30*(lM-c)*lm - 40*(lm-c)*lM
				if s := roundDiv(num, lM*lm); s > melhorRegra2 {
					melhorRegra2 = s
				}
			}
			// A regra 3 só existe com |lM − lm| ≤ 1 e lm ≥ MinTypoRunes.
			regra3 := 0
			if lM-lm <= 1 && lm >= MinTypoRunes {
				regra3 = roundDiv(100*lM-100, lM)
			}
			// A regra 1 exige palavras iguais — mesmo comprimento.
			regra1 := 0
			if lM == lm {
				regra1 = 100
			}
			alcancavel := max(melhorRegra2, max(regra3, regra1))

			if teto < MinScore {
				podados++
				require.Lessf(t, alcancavel, MinScore,
					"par podado (lM=%d, lm=%d) alcançava %d — a poda MUDARIA o resultado", lM, lm, alcancavel)
				continue
			}
			livres++
			require.GreaterOrEqualf(t, teto, melhorRegra2,
				"o teto declarado (lM=%d, lm=%d) é menor que o máximo real da regra 2", lM, lm)
		}
	}
	t.Logf("poda por comprimento: %d pares (lM, lm) podados, %d livres", podados, livres)
	require.Positive(t, podados, "o cenário precisa exercitar a poda de verdade")
}

func TestRoundDiv(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 88, roundDiv(7350, 84), "87,5 sobe")
	assert.Equal(t, 93, roundDiv(9990, 108), "92,5 sobe")
	assert.Equal(t, 82, roundDiv(5760, 70), "82,29 desce")
	assert.Equal(t, 86, roundDiv(600, 7), "85,71 sobe")
	assert.Equal(t, 0, roundDiv(0, 5))
	assert.Equal(t, 100, roundDiv(100, 1))
	// Limite superior da regra 3: L = 40 dá 97,5 → 98 (meio-para-cima).
	assert.Equal(t, 98, roundDiv(100*40-100, 40))
}

// ---------------------------------------------------------------------------
// Memo e concorrência
// ---------------------------------------------------------------------------

func TestMemoizacaoDevolveOMesmoObjeto(t *testing.T) {
	t.Parallel()

	m, err := NewMatcher([]Keyword{
		{OwnerID: "A", Keyword: "supermercado"},
		{OwnerID: "B", Keyword: "mercado"},
	})
	require.NoError(t, err)

	const desc = "mercado do seu jose"
	primeiro := rankOK(m.Rank(desc))
	segundo := rankOK(m.Rank(desc))
	require.NotEmpty(t, primeiro)
	assert.Same(t, &primeiro[0], &segundo[0], "a segunda chamada devolve o slice memorizado")

	// Best com exclusão lê a memo sem alterá-la.
	r := bestOK(m.Best(desc, "B"))
	require.True(t, r.Matched())
	assert.Equal(t, "A", r.Match.OwnerID)
	terceiro := rankOK(m.Rank(desc))
	assert.Same(t, &primeiro[0], &terceiro[0])
	assert.Equal(t, []Match{
		{OwnerID: "B", Keyword: "mercado", Score: 100},
		{OwnerID: "A", Keyword: "supermercado", Score: 88},
	}, terceiro, "a exclusão não mexe no ranking guardado")

	m.mu.RLock()
	assert.Len(t, m.memo, 1)
	m.mu.RUnlock()
}

func TestMemoRespeitaOTeto(t *testing.T) {
	t.Parallel()

	m, err := NewMatcher([]Keyword{{OwnerID: "A", Keyword: "padaria"}})
	require.NoError(t, err)
	// Enche a memo artificialmente até o teto; a próxima descrição nova não
	// entra, mas continua sendo calculada certo.
	m.mu.Lock()
	for i := range maxMemoEntries {
		m.memo[fmt.Sprintf("x%d", i)] = nil
	}
	m.mu.Unlock()

	assert.True(t, bestOK(m.Best("padaria central")).Matched())
	m.mu.RLock()
	defer m.mu.RUnlock()
	assert.Len(t, m.memo, maxMemoEntries)
	_, guardou := m.memo["padaria central"]
	assert.False(t, guardou)
}

// Sob -race: várias goroutines lendo e preenchendo a memo ao mesmo tempo.
func TestMatcherEhSeguroParaUsoConcorrente(t *testing.T) {
	t.Parallel()
	rng := novoRNG(t)
	m, descs := cenarioDeCarga(t, rng, 200, 40, 300)

	esperado := make([][]Match, len(descs))
	for i, d := range descs {
		esperado[i] = m.rankBruteForce(d)
	}

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := range descs {
				j := (i + offset*37) % len(descs)
				assert.Equal(t, esperado[j], rankOK(m.Rank(descs[j])))
				_ = bestOK(m.Best(descs[j], "owner-000"))
			}
		}(g)
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

// BenchmarkMatcher mede o cálculo SEM memo (o caminho caro): 1.000
// palavras-chave, descrições distintas a cada iteração.
func BenchmarkMatcher(b *testing.B) {
	rng := rand.New(rand.NewPCG(1, 1))
	m, descs := cenarioDeCarga(b, rng, 1_000, 200, 2_500)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rankOK(m.compute(descs[i%len(descs)]))
	}
}

func BenchmarkMatcherMemoizado(b *testing.B) {
	rng := rand.New(rand.NewPCG(1, 1))
	m, descs := cenarioDeCarga(b, rng, 1_000, 200, 2_500)
	for _, d := range descs {
		rankOK(m.Rank(d))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bestOK(m.Best(descs[i%len(descs)], "owner-000"))
	}
}

func BenchmarkForcaBruta(b *testing.B) {
	rng := rand.New(rand.NewPCG(1, 1))
	m, descs := cenarioDeCarga(b, rng, 1_000, 200, 2_500)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.rankBruteForce(descs[i%len(descs)])
	}
}

func BenchmarkScoreWord(b *testing.B) {
	w, k := []rune("mercadinho"), []rune("supermercado")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ScoreWord(w, k)
	}
}
