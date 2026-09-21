package textmatch_test

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lista é FECHADA: este teste é o que impede alguém de "só acrescentar
// uma" sem passar pela spec. A string abaixo é copiada literalmente da §3.
// rankOK e bestOK conferem que a chamada NÃO estourou o orçamento de trabalho
// (textmatch.MaxMatchWork) e devolvem o valor. Ver o comentário gêmeo em
// matcher_internal_test.go para o porquê de não receberem testing.TB.
func rankOK(rank []textmatch.Match, err error) []textmatch.Match {
	if err != nil {
		panic("orçamento de trabalho estourado num cenário que não devia estourar: " + err.Error())
	}
	return rank
}

func bestOK(r textmatch.Result, err error) textmatch.Result {
	if err != nil {
		panic("orçamento de trabalho estourado num cenário que não devia estourar: " + err.Error())
	}
	return r
}

func TestStopwordsSaoExatamenteAsDaSpec(t *testing.T) {
	t.Parallel()

	const daSpec = "de do da dos das e o a os as em no na nos nas um uma por para com sem seu sua ltda me sa eireli epp"

	esperadas := map[string]struct{}{}
	for _, p := range strings.Fields(daSpec) {
		esperadas[p] = struct{}{}
	}
	assert.Len(t, esperadas, 28, "a spec lista 28 palavras vazias")
	assert.Equal(t, esperadas, textmatch.Stopwords)
}

func TestTokenize(t *testing.T) {
	t.Parallel()

	casos := []struct {
		entrada  string
		esperado []string
	}{
		{"mercado do seu jose", []string{"mercado", "jose"}},
		{textnorm.Normalize("C&A MODAS LTDA"), []string{"modas"}},
		{"99 tecnologia ltda", []string{"99", "tecnologia"}},
		// "com" é palavra vazia da lista fechada ("compra com cartão"): cai.
		{"netflix.com", []string{"netflix"}},
		{"amazon.com.br", []string{"amazon", "br"}},
		{"pix enviado p/ joao", []string{"pix", "enviado", "joao"}},
		{"uber   eats", []string{"uber", "eats"}},
		{"compra no debito - padaria sao joao me", []string{"compra", "debito", "padaria", "sao", "joao"}},
		{"x-y", nil},
		{"de do da", nil},
		{"", nil},
		{"   ", nil},
		{"---", nil},
		{"a1 b2", []string{"a1", "b2"}},
	}
	for _, c := range casos {
		assert.Equal(t, c.esperado, textmatch.Tokenize(c.entrada), "entrada %q", c.entrada)
	}
}

func TestValidateKeyword(t *testing.T) {
	t.Parallel()

	t.Run("aceita e devolve as duas formas", func(t *testing.T) {
		t.Parallel()
		casos := []struct {
			raw, keyword, norm string
		}{
			{"Padaria São João", "Padaria São João", "padaria sao joao"},
			{"  Padaria   Central  ", "Padaria Central", "padaria central"},
			{"Pão d'Água", "Pão d'Água", "pao d'agua"},
			{"C6 Bank", "C6 Bank", "c6 bank"},
			{"99", "99", "99"},
			{"pix", "pix", "pix"},
			{"netflix.com", "netflix.com", "netflix.com"},
			{"Drogaria S/A", "Drogaria S/A", "drogaria s/a"},
			{"Mercado & Cia", "Mercado & Cia", "mercado & cia"},
			{"Super-Mercado", "Super-Mercado", "super-mercado"},
			{strings.Repeat("a", 40), strings.Repeat("a", 40), strings.Repeat("a", 40)},
			// "é" digitado como "e" + acento combinante (NFD) vira a letra composta.
			{"café", "café", "cafe"},
		}
		for _, c := range casos {
			keyword, norm, err := textmatch.ValidateKeyword(c.raw)
			require.NoError(t, err, "raw %q", c.raw)
			assert.Equal(t, c.keyword, keyword, "exibível de %q", c.raw)
			assert.Equal(t, c.norm, norm, "norm de %q", c.raw)
			assert.Equal(t, textnorm.Normalize(keyword), norm, "norm precisa ser Normalize(exibível)")
		}
	})

	t.Run("recusa sem ecoar a palavra", func(t *testing.T) {
		t.Parallel()
		casos := []struct {
			nome, raw, razao string
		}{
			{"só palavra vazia", "de", "no useful word"},
			{"só letras soltas", "c & a", "no useful word"},
			{"só ltda", "ltda", "no useful word"},
			{"41 runas", strings.Repeat("a", 41), "longer than 40"},
			{"41 runas com acento", "São" + strings.Repeat("o", 38), "longer than 40"},
			{"uma runa", "a", "shorter than 2"},
			{"vazio", "", "shorter than 2"},
			{"só espaço", " \t\n ", "shorter than 2"},
			{"tag html", "<script>", "outside the allowed set"},
			{"vírgula", "padaria, x", "outside the allowed set"},
			{"parêntese", "padaria (centro)", "outside the allowed set"},
			{"aspas duplas", `"padaria"`, "outside the allowed set"},
			{"controle", "pada\x00ria", "outside the allowed set"},
			{"emoji", "padaria 🍞", "outside the allowed set"},
			{"norm curta demais", "́́", "outside the allowed set"},
		}
		for _, c := range casos {
			t.Run(c.nome, func(t *testing.T) {
				t.Parallel()
				keyword, norm, err := textmatch.ValidateKeyword(c.raw)
				require.Error(t, err)
				assert.True(t, errors.Is(err, textmatch.ErrInvalidKeyword), "erro precisa embrulhar ErrInvalidKeyword: %v", err)
				assert.Contains(t, err.Error(), c.razao)
				assert.Empty(t, keyword)
				assert.Empty(t, norm)

				// A mensagem pode ir para um log: nunca carrega a entrada.
				if utf8.RuneCountInString(strings.TrimSpace(c.raw)) >= 3 {
					assert.NotContains(t, err.Error(), strings.TrimSpace(c.raw))
				}
			})
		}
		_, _, err := textmatch.ValidateKeyword("<script>alert(1)</script>")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "script")
	})

	t.Run("limite de 40 conta runas, não bytes", func(t *testing.T) {
		t.Parallel()
		quarenta := strings.Repeat("ã", 40) // 80 bytes
		keyword, norm, err := textmatch.ValidateKeyword(quarenta)
		require.NoError(t, err)
		assert.Equal(t, quarenta, keyword)
		assert.Equal(t, strings.Repeat("a", 40), norm)
	})
}

func TestNewMatcherRecusaPalavraSemTokenSemEcoar(t *testing.T) {
	t.Parallel()

	_, err := textmatch.NewMatcher([]textmatch.Keyword{
		{OwnerID: "A", Keyword: "padaria"},
		{OwnerID: "B", Keyword: "SEGREDO-DE", Norm: "de"},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, textmatch.ErrInvalidKeyword))
	assert.Contains(t, err.Error(), "#1")
	assert.NotContains(t, err.Error(), "SEGREDO")
	assert.NotContains(t, err.Error(), "B")

	_, err = textmatch.NewMatcher([]textmatch.Keyword{{OwnerID: "", Keyword: "padaria"}})
	require.Error(t, err)
	assert.True(t, errors.Is(err, textmatch.ErrInvalidKeyword))
}

func TestMatcherVazioENulo(t *testing.T) {
	t.Parallel()

	m, err := textmatch.NewMatcher(nil)
	require.NoError(t, err)
	assert.Nil(t, rankOK(m.Rank("padaria central")))
	assert.Equal(t, textmatch.Result{Reason: textmatch.ReasonBelowThreshold}, bestOK(m.Best("padaria central")))

	// Matcher nulo não pode derrubar uma requisição.
	var nulo *textmatch.Matcher
	assert.Nil(t, rankOK(nulo.Rank("padaria")))
	assert.Equal(t, textmatch.Result{Reason: textmatch.ReasonBelowThreshold}, bestOK(nulo.Best("padaria")))

	m, err = textmatch.NewMatcher([]textmatch.Keyword{{OwnerID: "A", Keyword: "padaria"}})
	require.NoError(t, err)
	assert.Nil(t, rankOK(m.Rank("")), "descrição vazia")
	assert.Nil(t, rankOK(m.Rank("de do da")), "só palavras vazias")
}

func TestMatcherUsaNormInformadaOuDeriva(t *testing.T) {
	t.Parallel()

	// Com Norm vazia, deriva de Keyword (acento e caixa caem).
	m, err := textmatch.NewMatcher([]textmatch.Keyword{{OwnerID: "A", Keyword: "Padaria São João"}})
	require.NoError(t, err)
	r := bestOK(m.Best("compra padaria sao joao"))
	require.True(t, r.Matched())
	assert.Equal(t, "Padaria São João", r.Match.Keyword, "devolve a forma exibível, não a norm")
	assert.Equal(t, 100, r.Match.Score)

	// Com Norm informada, é ela que manda (é a coluna indexada do banco).
	m, err = textmatch.NewMatcher([]textmatch.Keyword{{OwnerID: "A", Keyword: "Padaria", Norm: "padaria"}})
	require.NoError(t, err)
	assert.True(t, bestOK(m.Best("padaria central")).Matched())
}

func TestRankOrdenaPorPontuacaoDonoEPalavra(t *testing.T) {
	t.Parallel()

	m, err := textmatch.NewMatcher([]textmatch.Keyword{
		{OwnerID: "C", Keyword: "mercado"},
		{OwnerID: "A", Keyword: "supermercado"},
		{OwnerID: "B", Keyword: "mercado"},
		{OwnerID: "B", Keyword: "atacado"},
	})
	require.NoError(t, err)

	assert.Equal(t, []textmatch.Match{
		{OwnerID: "B", Keyword: "mercado", Score: 100},
		{OwnerID: "C", Keyword: "mercado", Score: 100},
		{OwnerID: "A", Keyword: "supermercado", Score: 88},
	}, rankOK(m.Rank("mercado do seu jose")))
}

// Dentro do mesmo dono, em empate, decide a palavra exibível menor — para o
// resultado não depender da ordem em que as palavras foram cadastradas.
func TestRankDesempataDentroDoDonoPelaPalavra(t *testing.T) {
	t.Parallel()

	m, err := textmatch.NewMatcher([]textmatch.Keyword{
		{OwnerID: "A", Keyword: "padaria"},
		{OwnerID: "A", Keyword: "central"},
	})
	require.NoError(t, err)
	assert.Equal(t, []textmatch.Match{{OwnerID: "A", Keyword: "central", Score: 100}}, rankOK(m.Rank("padaria central")))

	invertido, err := textmatch.NewMatcher([]textmatch.Keyword{
		{OwnerID: "A", Keyword: "central"},
		{OwnerID: "A", Keyword: "padaria"},
	})
	require.NoError(t, err)
	assert.Equal(t, rankOK(m.Rank("padaria central")), rankOK(invertido.Rank("padaria central")))
}

func TestBestExcluiAntesDeEscolher(t *testing.T) {
	t.Parallel()

	m, err := textmatch.NewMatcher([]textmatch.Keyword{
		{OwnerID: "A", Keyword: "nubank"},
		{OwnerID: "B", Keyword: "nubank"},
		{OwnerID: "C", Keyword: "nubanco"}, // regra 2: C=5 (nuban), LM=7, Lm=6 → 3560/42 → 85
	})
	require.NoError(t, err)

	// Sem exclusão: A e B empatam em 100.
	assert.Equal(t, textmatch.ReasonAmbiguous, bestOK(m.Best("pix nubank")).Reason)

	// Excluindo A (a conta do lote), B vence com 100 — não é mais empate.
	r := bestOK(m.Best("pix nubank", "A"))
	require.True(t, r.Matched())
	assert.Equal(t, textmatch.Match{OwnerID: "B", Keyword: "nubank", Score: 100}, r.Match)

	// Excluindo A e B, sobra C com 85.
	r = bestOK(m.Best("pix nubank", "A", "B"))
	require.True(t, r.Matched())
	assert.Equal(t, textmatch.Match{OwnerID: "C", Keyword: "nubanco", Score: 85}, r.Match)

	// Excluindo todos, não há sugestão.
	assert.Equal(t, textmatch.ReasonBelowThreshold, bestOK(m.Best("pix nubank", "A", "B", "C")).Reason)

	// Excluir um dono que não está no ranking não muda nada.
	assert.Equal(t, textmatch.ReasonAmbiguous, bestOK(m.Best("pix nubank", "Z")).Reason)
}

func TestBestEmpateAbaixoDoLimiarEBelowThreshold(t *testing.T) {
	t.Parallel()

	m, err := textmatch.NewMatcher([]textmatch.Keyword{
		{OwnerID: "A", Keyword: "drogaria"},
		{OwnerID: "B", Keyword: "drogaria"},
	})
	require.NoError(t, err)
	// drogasil ~ drogaria = 74 para os dois: empate, mas abaixo de 80.
	assert.Equal(t, textmatch.ReasonBelowThreshold, bestOK(m.Best("drogasil")).Reason)
}

// Palavra-chave com mais de uma palavra: casa inteira (regra 1) ou com todas
// as suas palavras casando individualmente (a menor pontuação manda).
func TestPalavraChaveComposta(t *testing.T) {
	t.Parallel()

	m, err := textmatch.NewMatcher([]textmatch.Keyword{{OwnerID: "A", Keyword: "uber eats"}})
	require.NoError(t, err)

	assert.Equal(t, 100, bestOK(m.Best("uber eats sao paulo")).Match.Score, "frase consecutiva")
	assert.Equal(t, 100, bestOK(m.Best("eats uber")).Match.Score, "as duas palavras, fora de ordem, casam individualmente")
	assert.Equal(t, textmatch.ReasonBelowThreshold, bestOK(m.Best("uber viagem")).Reason, "falta eats: uber sozinho não basta")
	assert.Equal(t, textmatch.ReasonBelowThreshold, bestOK(m.Best("eats")).Reason)

	// Uma das palavras casa por aproximação: a menor pontuação decide.
	m, err = textmatch.NewMatcher([]textmatch.Keyword{{OwnerID: "A", Keyword: "supermercado central"}})
	require.NoError(t, err)
	r := bestOK(m.Best("central mercado"))
	require.True(t, r.Matched())
	assert.Equal(t, 88, r.Match.Score, "central=100, mercado~supermercado=88 → 88")
}

// As palavras vazias caem dos DOIS lados: "mercado do seu jose" com a
// palavra-chave "mercado do jose" casa inteira.
func TestStopwordsCaemDosDoisLados(t *testing.T) {
	t.Parallel()

	m, err := textmatch.NewMatcher([]textmatch.Keyword{{OwnerID: "A", Keyword: "mercado do jose"}})
	require.NoError(t, err)
	r := bestOK(m.Best("mercado do seu jose"))
	require.True(t, r.Matched())
	assert.Equal(t, 100, r.Match.Score)
}
