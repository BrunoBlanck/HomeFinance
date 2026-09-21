package classify_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// Testes do ADR-029(g): o matcher passa a ser organizado por LADO DO DINHEIRO
// — despesa concorre contra `expense` + `investment`, receita contra `income` +
// `redemption` —, em DOIS matchers e UMA passagem.
//
// Os ARGUMENTOS de SuggestCategory não mudaram com o ADR-029, e é isso que faz
// a importação e o auto-categorize ganharem as naturezas novas de graça. (O
// retorno ganhou um erro depois, pelo orçamento de trabalho do textmatch —
// assunto de outra entrega, tratado aqui pelo helper `sugerir`.)

// fixturaComInvestimento monta uma casa com as QUATRO naturezas, cada uma com
// palavra própria e sem colisão entre elas.
func fixturaComInvestimento() *fixtura {
	reg := &registro{}
	categorias := &categoriasFake{reg: reg, linhas: []category.Category{
		grupo("cat-mercado", casaA, category.KindExpense, "Mercado"),
		grupo("cat-salario", casaA, category.KindIncome, "Salário"),
		grupo("cat-cdb", casaA, category.KindInvestment, "Renda fixa"),
		grupo("cat-resgate", casaA, category.KindRedemption, "Resgates"),
		// Casa B tem as MESMAS palavras, outras donas: o isolamento não passa
		// por acaso.
		grupo("cat-b-cdb", casaB, category.KindInvestment, "Renda fixa B"),
	}, palavras: []category.Keyword{
		palavraDeCategoria(casaA, "cat-mercado", "supermercado"),
		palavraDeCategoria(casaA, "cat-salario", "salario"),
		palavraDeCategoria(casaA, "cat-cdb", "cdb"),
		palavraDeCategoria(casaA, "cat-resgate", "resgate"),
		palavraDeCategoria(casaB, "cat-b-cdb", "cdb"),
	}}
	contas := &contasFake{reg: reg, linhas: []account.Account{
		conta("conta-nubank", casaA, "Nubank"),
	}, palavras: []account.Keyword{
		palavraDeConta(casaA, "conta-nubank", "nubank"),
	}}
	return &fixtura{categorias: categorias, contas: contas, reg: reg}
}

func norm(s string) string { return textnorm.Normalize(s) }

// sugerir chama SuggestCategory e exige que o orçamento de trabalho do
// textmatch NÃO tenha estourado (ErrWorkBudgetExceeded). O assunto destes
// testes é o LADO DO DINHEIRO, nunca o teto de trabalho — e um erro engolido
// aqui viraria um "sem sugestão" que passaria por resultado legítimo.
func sugerir(t *testing.T, set *classify.Set, kind, descricao string) textmatch.Result {
	t.Helper()
	res, err := set.SuggestCategory(kind, descricao)
	require.NoError(t, err)
	return res
}

// Aceite 5, primeira metade: despesa cuja descrição bate com palavra-chave de
// categoria de INVESTIMENTO recebe a sugestão dela — sem uma linha de código
// nova em quem chama, porque a pergunta continua sendo a mesma.
func TestDespesaRecebeSugestaoDeCategoriaDeInvestimento(t *testing.T) {
	t.Parallel()

	set := fixturaComInvestimento().carregar(t, casaA)

	res := sugerir(t, set, "expense", norm("CDB 15 DIAS"))
	require.Equal(t, textmatch.ReasonMatched, res.Reason, "%+v", res)
	assert.Equal(t, "cat-cdb", res.Match.OwnerID)
}

// Aceite 5, segunda metade — o que de fato importa: despesa NUNCA recebe
// sugestão de categoria de RESGATE, e receita nunca de aporte. É por essa
// porta que um lançamento ganharia a natureza do lado errado do caixa.
func TestLadoErradoDoDinheiroNuncaEhSugerido(t *testing.T) {
	t.Parallel()

	set := fixturaComInvestimento().carregar(t, casaA)

	casos := []struct {
		nome, kind, descricao string
	}{
		{"despesa não recebe resgate", "expense", "RESGATE TESOURO"},
		{"despesa não recebe receita", "expense", "SALARIO MENSAL"},
		{"receita não recebe aporte", "income", "CDB 15 DIAS"},
		{"receita não recebe despesa", "income", "SUPERMERCADO BOM"},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			res := sugerir(t, set, c.kind, norm(c.descricao))
			assert.Equal(t, textmatch.ReasonBelowThreshold, res.Reason, "%+v", res)
			assert.Empty(t, res.Match.OwnerID, "nenhum dono sai de um resultado sem correspondência")
		})
	}
}

func TestReceitaRecebeSugestaoDeCategoriaDeResgate(t *testing.T) {
	t.Parallel()

	set := fixturaComInvestimento().carregar(t, casaA)

	res := sugerir(t, set, "income", norm("RESGATE TESOURO SELIC"))
	require.Equal(t, textmatch.ReasonMatched, res.Reason, "%+v", res)
	assert.Equal(t, "cat-resgate", res.Match.OwnerID)
}

// UMA passagem, não duas: uma descrição que bate igual numa categoria de
// despesa e numa de investimento é AMBÍGUA e não recebe sugestão nenhuma
// (ADR-026a). Com dois passes — um por natureza — haveria dois vencedores e
// alguém teria de inventar um critério entre eles.
func TestEmpateEntreDespesaEAporteEhAmbiguoENaoDoisVencedores(t *testing.T) {
	t.Parallel()

	reg := &registro{}
	categorias := &categoriasFake{reg: reg, linhas: []category.Category{
		grupo("cat-despesa", casaA, category.KindExpense, "Aplicações"),
		grupo("cat-aporte", casaA, category.KindInvestment, "Renda fixa"),
	}, palavras: []category.Keyword{
		// A MESMA palavra em duas donas do mesmo lado do dinheiro. O índice
		// único do banco barra isso no cadastro (KEYWORD_TAKEN); aqui o dado
		// é montado à mão justamente para provar que, se chegasse, o
		// classificador responde AMBÍGUO em vez de escolher.
		palavraDeCategoria(casaA, "cat-despesa", "tesouro"),
		palavraDeCategoria(casaA, "cat-aporte", "tesouro"),
	}}
	contas := &contasFake{reg: reg}
	f := &fixtura{categorias: categorias, contas: contas, reg: reg}

	set := f.carregar(t, casaA)

	res := sugerir(t, set, "expense", norm("TESOURO DIRETO"))
	assert.Equal(t, textmatch.ReasonAmbiguous, res.Reason,
		"empate entre naturezas do mesmo lado é ambíguo, não dois vencedores: %+v", res)
	assert.Empty(t, res.Match.OwnerID)
}

// A pontuação e o desempate valem entre as naturezas do mesmo lado, como se
// fossem uma só: a melhor pontuação vence, venha de `expense` ou de
// `investment`.
func TestDesempatePorPontuacaoAtravessaAsNaturezasDoMesmoLado(t *testing.T) {
	t.Parallel()

	reg := &registro{}
	categorias := &categoriasFake{reg: reg, linhas: []category.Category{
		grupo("cat-despesa", casaA, category.KindExpense, "Serviços"),
		grupo("cat-aporte", casaA, category.KindInvestment, "Renda fixa"),
	}, palavras: []category.Keyword{
		palavraDeCategoria(casaA, "cat-despesa", "tesouraria"),
		palavraDeCategoria(casaA, "cat-aporte", "tesouro direto"),
	}}
	f := &fixtura{categorias: categorias, contas: &contasFake{reg: reg}, reg: reg}

	set := f.carregar(t, casaA)

	res := sugerir(t, set, "expense", norm("TESOURO DIRETO CDB"))
	require.Equal(t, textmatch.ReasonMatched, res.Reason, "%+v", res)
	assert.Equal(t, "cat-aporte", res.Match.OwnerID,
		"a melhor pontuação vence, independentemente da natureza dentro do lado")
}

// A assinatura recebe o `kind` do LANÇAMENTO. Passar uma NATUREZA de categoria
// (`investment`, `redemption`) ou uma perna de transferência não casa com lado
// nenhum e responde sem sugestão — o desfecho seguro.
func TestSuggestCategoryRecusaKindQueNaoEhDeLancamento(t *testing.T) {
	t.Parallel()

	set := fixturaComInvestimento().carregar(t, casaA)

	for _, kind := range []string{
		"investment", "redemption", "transfer_in", "transfer_out", "", " ", "Expense", "EXPENSE",
	} {
		res := sugerir(t, set, kind, norm("CDB 15 DIAS"))
		assert.Equal(t, textmatch.ReasonBelowThreshold, res.Reason, "kind %q: %+v", kind, res)
		assert.Empty(t, res.Match.OwnerID, "kind %q", kind)
	}
}

// Isolamento: a categoria de investimento da casa B nunca chega ao Set da casa
// A, mesmo com as duas tendo a mesma palavra.
func TestCategoriaDeInvestimentoDeOutraCasaNuncaEhSugerida(t *testing.T) {
	t.Parallel()

	f := fixturaComInvestimento()
	set := f.carregar(t, casaA)

	res := sugerir(t, set, "expense", norm("CDB 15 DIAS"))
	require.Equal(t, textmatch.ReasonMatched, res.Reason)
	assert.Equal(t, "cat-cdb", res.Match.OwnerID, "a dona é sempre da casa carregada")

	for _, c := range f.reg.todas() {
		assert.Equal(t, casaA, c.Casa, "toda consulta leva o householdID recebido, tal qual")
	}
}

// AssignableKind devolve as naturezas novas: é por ele que o confirm da
// importação confere se a sugestão ainda vale (spec 0005 §13).
func TestAssignableKindConheceAsNaturezasNovas(t *testing.T) {
	t.Parallel()

	set := fixturaComInvestimento().carregar(t, casaA)

	kind, ok := set.AssignableKind("cat-cdb")
	require.True(t, ok)
	assert.Equal(t, category.KindInvestment, kind)

	kind, ok = set.AssignableKind("cat-resgate")
	require.True(t, ok)
	assert.Equal(t, category.KindRedemption, kind)

	// Casa alheia: nunca entra no Set, então responde false pelo mesmo caminho
	// de uma categoria arquivada.
	_, ok = set.AssignableKind("cat-b-cdb")
	assert.False(t, ok)
}

// Categoria de investimento ARQUIVADA sai do matcher, como qualquer outra: o
// Set nunca a vê (arquivar não desfaz o passado, mas tira do futuro).
func TestCategoriaDeInvestimentoArquivadaSaiDoMatcher(t *testing.T) {
	t.Parallel()

	reg := &registro{}
	categorias := &categoriasFake{reg: reg, linhas: []category.Category{
		arquivada(grupo("cat-cdb", casaA, category.KindInvestment, "Renda fixa")),
	}, palavras: []category.Keyword{
		palavraDeCategoria(casaA, "cat-cdb", "cdb"),
	}}
	f := &fixtura{categorias: categorias, contas: &contasFake{reg: reg}, reg: reg}

	set := f.carregar(t, casaA)

	res := sugerir(t, set, "expense", norm("CDB 15 DIAS"))
	assert.Equal(t, textmatch.ReasonBelowThreshold, res.Reason, "%+v", res)
	_, ok := set.AssignableKind("cat-cdb")
	assert.False(t, ok)
}

// Grupo de investimento COM subcategoria ativa não recebe lançamento, então a
// palavra dele fica de fora do matcher — a regra da spec 0005 §12 vale igual
// para as naturezas novas.
func TestGrupoDeInvestimentoComFilhaAtivaFicaForaDoMatcher(t *testing.T) {
	t.Parallel()

	reg := &registro{}
	categorias := &categoriasFake{reg: reg, linhas: []category.Category{
		grupo("cat-carteira", casaA, category.KindInvestment, "Carteira"),
		folha("cat-cdb", casaA, category.KindInvestment, "CDB", "cat-carteira"),
	}, palavras: []category.Keyword{
		palavraDeCategoria(casaA, "cat-carteira", "aplicacao"),
		palavraDeCategoria(casaA, "cat-cdb", "cdb"),
	}}
	f := &fixtura{categorias: categorias, contas: &contasFake{reg: reg}, reg: reg}

	set := f.carregar(t, casaA)

	res := sugerir(t, set, "expense", norm("APLICACAO AUTOMATICA"))
	assert.Equal(t, textmatch.ReasonBelowThreshold, res.Reason,
		"a palavra do grupo com filha ativa nunca entra no matcher: %+v", res)

	res = sugerir(t, set, "expense", norm("CDB 15 DIAS"))
	require.Equal(t, textmatch.ReasonMatched, res.Reason, "%+v", res)
	assert.Equal(t, "cat-cdb", res.Match.OwnerID, "a folha, sim")
}

// O Load continua fazendo exatamente QUATRO consultas com as naturezas novas —
// dois matchers por lado, não quatro por natureza.
func TestLoadComNaturezasNovasContinuaEmQuatroConsultas(t *testing.T) {
	t.Parallel()

	f := fixturaComInvestimento()
	_, err := classify.NewLoader(f.categorias, f.contas).Load(context.Background(), casaA)
	require.NoError(t, err)
	assert.Len(t, f.reg.todas(), 4, "uma consulta a mais é uma consulta por linha esperando para acontecer")
}
