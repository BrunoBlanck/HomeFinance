package classify_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// Os dublês reproduzem o CONTRATO dos repositórios (filtram por casa; List sem
// includeArchived esconde arquivada e excluída; ListKeywords devolve a casa
// inteira, dona ativa ou não). O modo `vaza` desliga o filtro de arquivada e
// excluída no List — é como o loader é provado a descartá-las por conta
// própria, e não por confiar na fonte.

const (
	casaA = "casa-a"
	casaB = "casa-b"
)

var (
	agora   = time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	passado = agora.Add(-24 * time.Hour)
)

type chamada struct {
	Fonte, Metodo, Casa string
	IncluiArquivadas    bool
}

type registro struct {
	mu       sync.Mutex
	chamadas []chamada
}

func (r *registro) anotar(c chamada) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chamadas = append(r.chamadas, c)
}

func (r *registro) todas() []chamada {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]chamada(nil), r.chamadas...)
}

type categoriasFake struct {
	linhas   []category.Category
	palavras []category.Keyword
	vaza     bool
	// vazaCasas faz ListKeywords ignorar o filtro por casa — simula uma fonte
	// com o isolamento quebrado, que o loader tem de conter.
	vazaCasas bool
	erros     map[string]error
	reg       *registro
}

func (f *categoriasFake) List(_ context.Context, hh string, includeArchived bool) ([]category.Category, error) {
	f.reg.anotar(chamada{Fonte: "categorias", Metodo: "List", Casa: hh, IncluiArquivadas: includeArchived})
	if err := f.erros["List"]; err != nil {
		return nil, err
	}
	var out []category.Category
	for _, c := range f.linhas {
		if c.HouseholdID != hh {
			continue
		}
		if !f.vaza {
			if c.DeletedAt != nil || (!includeArchived && c.ArchivedAt != nil) {
				continue
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func (f *categoriasFake) ListKeywords(_ context.Context, hh string) ([]category.Keyword, error) {
	f.reg.anotar(chamada{Fonte: "categorias", Metodo: "ListKeywords", Casa: hh})
	if err := f.erros["ListKeywords"]; err != nil {
		return nil, err
	}
	var out []category.Keyword
	for _, k := range f.palavras {
		if f.vazaCasas || k.HouseholdID == hh {
			out = append(out, k)
		}
	}
	return out, nil
}

type contasFake struct {
	linhas    []account.Account
	palavras  []account.Keyword
	vaza      bool
	vazaCasas bool
	erros     map[string]error
	reg       *registro
}

func (f *contasFake) List(_ context.Context, hh string, includeArchived bool) ([]account.Account, error) {
	f.reg.anotar(chamada{Fonte: "contas", Metodo: "List", Casa: hh, IncluiArquivadas: includeArchived})
	if err := f.erros["List"]; err != nil {
		return nil, err
	}
	var out []account.Account
	for _, a := range f.linhas {
		if a.HouseholdID != hh {
			continue
		}
		if !f.vaza {
			if a.DeletedAt != nil || (!includeArchived && a.ArchivedAt != nil) {
				continue
			}
		}
		out = append(out, a)
	}
	return out, nil
}

func (f *contasFake) ListKeywords(_ context.Context, hh string) ([]account.Keyword, error) {
	f.reg.anotar(chamada{Fonte: "contas", Metodo: "ListKeywords", Casa: hh})
	if err := f.erros["ListKeywords"]; err != nil {
		return nil, err
	}
	var out []account.Keyword
	for _, k := range f.palavras {
		if f.vazaCasas || k.HouseholdID == hh {
			out = append(out, k)
		}
	}
	return out, nil
}

// --- construtores da fixture ------------------------------------------------

func grupo(id, casa, kind, nome string) category.Category {
	return category.Category{ID: id, HouseholdID: casa, Kind: kind, Name: nome, NameNorm: textnorm.Normalize(nome)}
}

func folha(id, casa, kind, nome, pai string) category.Category {
	c := grupo(id, casa, kind, nome)
	c.ParentID = &pai
	return c
}

func arquivada(c category.Category) category.Category {
	c.ArchivedAt = &passado
	return c
}

func excluida(c category.Category) category.Category {
	c.DeletedAt = &passado
	return c
}

func palavraDeCategoria(casa, categoriaID, palavra string) category.Keyword {
	return category.Keyword{
		ID: "kw-" + categoriaID + "-" + palavra, HouseholdID: casa, CategoryID: categoriaID,
		Keyword: palavra, Norm: textnorm.Normalize(palavra), CreatedAt: agora,
	}
}

func conta(id, casa, nome string) account.Account {
	return account.Account{ID: id, HouseholdID: casa, Name: nome, NameNorm: textnorm.Normalize(nome)}
}

func contaArquivada(a account.Account) account.Account {
	a.ArchivedAt = &passado
	return a
}

func contaExcluida(a account.Account) account.Account {
	a.DeletedAt = &passado
	return a
}

func palavraDeConta(casa, contaID, palavra string) account.Keyword {
	return account.Keyword{
		ID: "kw-" + contaID + "-" + palavra, HouseholdID: casa, AccountID: contaID,
		Keyword: palavra, Norm: textnorm.Normalize(palavra), CreatedAt: agora,
	}
}

// fixtura é o cenário completo das duas casas. Casa A tem todos os casos de
// borda; casa B tem as MESMAS palavras da casa A (e outras), para o teste de
// isolamento não passar por acaso.
type fixtura struct {
	categorias *categoriasFake
	contas     *contasFake
	reg        *registro
}

func novaFixtura(vaza bool) *fixtura {
	reg := &registro{}
	categorias := &categoriasFake{vaza: vaza, reg: reg, linhas: []category.Category{
		// Despesa: grupo com duas folhas ativas — só as folhas são atribuíveis.
		grupo("cat-alimentacao", casaA, category.KindExpense, "Alimentação"),
		folha("cat-supermercado", casaA, category.KindExpense, "Supermercado", "cat-alimentacao"),
		folha("cat-padaria", casaA, category.KindExpense, "Padaria", "cat-alimentacao"),
		// Despesa: grupo SEM filhas — atribuível ele mesmo.
		grupo("cat-transporte", casaA, category.KindExpense, "Transporte"),
		// Despesa: grupo com uma filha ativa — o grupo fica de fora, a filha entra.
		grupo("cat-lazer", casaA, category.KindExpense, "Lazer"),
		folha("cat-streaming", casaA, category.KindExpense, "Streaming", "cat-lazer"),
		// Despesa: grupo cuja ÚNICA filha está arquivada — sem filha ativa, o grupo entra.
		grupo("cat-saude", casaA, category.KindExpense, "Saúde"),
		arquivada(folha("cat-dentista", casaA, category.KindExpense, "Dentista", "cat-saude")),
		// Despesa: duas folhas para o empate.
		folha("cat-servicos", casaA, category.KindExpense, "Serviços", "cat-alimentacao"),
		// Despesa arquivada e despesa excluída: nunca entram.
		arquivada(folha("cat-viagem", casaA, category.KindExpense, "Viagem", "cat-lazer")),
		excluida(folha("cat-antiga", casaA, category.KindExpense, "Antiga", "cat-lazer")),
		// Receita.
		grupo("cat-renda", casaA, category.KindIncome, "Renda"),
		folha("cat-salario", casaA, category.KindIncome, "Salário", "cat-renda"),
		// Casa B: as mesmas palavras, outras donas.
		grupo("cat-b-mercado", casaB, category.KindExpense, "Mercado B"),
		grupo("cat-b-renda", casaB, category.KindIncome, "Renda B"),
	}, palavras: []category.Keyword{
		palavraDeCategoria(casaA, "cat-supermercado", "supermercado"),
		palavraDeCategoria(casaA, "cat-padaria", "padaria"),
		palavraDeCategoria(casaA, "cat-alimentacao", "restaurante"), // grupo com filhas ativas
		palavraDeCategoria(casaA, "cat-transporte", "uber"),
		palavraDeCategoria(casaA, "cat-lazer", "cinema"), // grupo com filha ativa
		palavraDeCategoria(casaA, "cat-streaming", "netflix"),
		palavraDeCategoria(casaA, "cat-saude", "farmacia"),
		palavraDeCategoria(casaA, "cat-servicos", "central"),
		palavraDeCategoria(casaA, "cat-viagem", "latam"),
		palavraDeCategoria(casaA, "cat-antiga", "extra"),
		palavraDeCategoria(casaA, "cat-salario", "salario"),
		palavraDeCategoria(casaB, "cat-b-mercado", "supermercado"),
		palavraDeCategoria(casaB, "cat-b-mercado", "uber"),
		palavraDeCategoria(casaB, "cat-b-renda", "salario"),
	}}

	contas := &contasFake{vaza: vaza, reg: reg, linhas: []account.Account{
		conta("conta-nubank", casaA, "Nubank"),
		conta("conta-c6", casaA, "C6"),
		conta("conta-inter", casaA, "Inter"),
		conta("conta-caixa", casaA, "Caixa"),
		contaArquivada(conta("conta-bradesco", casaA, "Bradesco")),
		contaExcluida(conta("conta-itau", casaA, "Itaú")),
		conta("conta-b-nubank", casaB, "Nubank B"),
	}, palavras: []account.Keyword{
		palavraDeConta(casaA, "conta-nubank", "nubank"),
		// "nubanc" contra o token "nubank" pontua 88 pela regra 2 (LCS 5,
		// comprimentos 6 e 6): é a SEGUNDA melhor quando a Nubank é a conta do lote.
		palavraDeConta(casaA, "conta-c6", "nubanc"),
		palavraDeConta(casaA, "conta-inter", "inter"),
		palavraDeConta(casaA, "conta-caixa", "ted"),
		palavraDeConta(casaA, "conta-bradesco", "bradesco"),
		palavraDeConta(casaA, "conta-itau", "itau"),
		palavraDeConta(casaB, "conta-b-nubank", "nubank"),
		palavraDeConta(casaB, "conta-b-nubank", "doc"),
	}}
	return &fixtura{categorias: categorias, contas: contas, reg: reg}
}

func (f *fixtura) carregar(t *testing.T, casa string) *classify.Set {
	t.Helper()
	set, err := classify.NewLoader(f.categorias, f.contas).Load(context.Background(), casa)
	require.NoError(t, err)
	require.NotNil(t, set)
	return set
}

// --- testes -----------------------------------------------------------------

// resOK confere que a consulta NÃO estourou o orçamento de trabalho do
// conjunto (textmatch.MaxMatchWork) e devolve o resultado.
//
// Não recebe testing.TB porque Go não deixa misturar um argumento extra com o
// par (valor, erro) devolvido por outra chamada. Estourar o orçamento nestes
// cenários — punhados de palavras-chave — é defeito do teste, e o pânico
// falha alto.
func resOK(r textmatch.Result, err error) textmatch.Result {
	if err != nil {
		panic("orçamento de trabalho estourado num cenário que não devia estourar: " + err.Error())
	}
	return r
}

func TestLoadFazExatamenteQuatroConsultasComACasaRecebida(t *testing.T) {
	f := novaFixtura(false)
	f.carregar(t, casaA)

	chamadas := f.reg.todas()
	require.Len(t, chamadas, 4, "uma consulta a mais é uma consulta por linha esperando para acontecer")
	assert.ElementsMatch(t, []chamada{
		{Fonte: "categorias", Metodo: "List", Casa: casaA, IncluiArquivadas: false},
		{Fonte: "categorias", Metodo: "ListKeywords", Casa: casaA},
		{Fonte: "contas", Metodo: "List", Casa: casaA, IncluiArquivadas: false},
		{Fonte: "contas", Metodo: "ListKeywords", Casa: casaA},
	}, chamadas)
	for _, c := range chamadas {
		assert.Equal(t, casaA, c.Casa, "toda consulta leva o householdID recebido, tal qual")
		assert.False(t, c.IncluiArquivadas, "o loader nunca pede arquivadas")
	}
}

// A tabela roda duas vezes: com a fonte fiel ao repositório e com a fonte que
// VAZA arquivadas e excluídas no List. O resultado tem de ser idêntico — é o
// loader, e não a fonte, quem garante que elas não entram.
func TestSuggestCategory(t *testing.T) {
	casos := []struct {
		nome, kind, descricao string
		reason                textmatch.Reason
		owner, keyword        string
		score                 int
	}{
		{nome: "folha ativa de despesa casa inteira", kind: "expense", descricao: "supermercado extra",
			reason: textmatch.ReasonMatched, owner: "cat-supermercado", keyword: "supermercado", score: 100},
		{nome: "palavra de despesa nunca sugere para receita", kind: "income", descricao: "supermercado extra",
			reason: textmatch.ReasonBelowThreshold},
		{nome: "palavra de receita nunca sugere para despesa", kind: "expense", descricao: "salario mensal",
			reason: textmatch.ReasonBelowThreshold},
		{nome: "folha ativa de receita", kind: "income", descricao: "salario mensal",
			reason: textmatch.ReasonMatched, owner: "cat-salario", keyword: "salario", score: 100},
		{nome: "transferência não tem categoria", kind: "transfer_out", descricao: "supermercado extra",
			reason: textmatch.ReasonBelowThreshold},
		{nome: "kind vazio", kind: "", descricao: "supermercado extra",
			reason: textmatch.ReasonBelowThreshold},
		{nome: "kind inválido", kind: "EXPENSE", descricao: "supermercado extra",
			reason: textmatch.ReasonBelowThreshold},
		{nome: "grupo sem filhas é atribuível", kind: "expense", descricao: "uber viagem",
			reason: textmatch.ReasonMatched, owner: "cat-transporte", keyword: "uber", score: 100},
		{nome: "grupo com filhas ativas fica fora do matcher", kind: "expense", descricao: "restaurante do porto",
			reason: textmatch.ReasonBelowThreshold},
		{nome: "grupo com uma filha ativa fica fora, a filha entra", kind: "expense", descricao: "cinema netflix",
			reason: textmatch.ReasonMatched, owner: "cat-streaming", keyword: "netflix", score: 100},
		{nome: "grupo cuja única filha está arquivada é atribuível", kind: "expense", descricao: "farmacia popular",
			reason: textmatch.ReasonMatched, owner: "cat-saude", keyword: "farmacia", score: 100},
		{nome: "categoria arquivada nunca entra", kind: "expense", descricao: "latam airlines",
			reason: textmatch.ReasonBelowThreshold},
		{nome: "categoria excluída nunca entra", kind: "expense", descricao: "extra hiper",
			reason: textmatch.ReasonBelowThreshold},
		{nome: "empate entre duas folhas é ambíguo", kind: "expense", descricao: "padaria central",
			reason: textmatch.ReasonAmbiguous},
		{nome: "descrição sem nada", kind: "expense", descricao: "",
			reason: textmatch.ReasonBelowThreshold},
	}

	for _, vaza := range []bool{false, true} {
		set := novaFixtura(vaza).carregar(t, casaA)
		for _, c := range casos {
			nome := c.nome
			if vaza {
				nome += " (fonte que vaza)"
			}
			t.Run(nome, func(t *testing.T) {
				r := resOK(set.SuggestCategory(c.kind, c.descricao))
				assert.Equal(t, c.reason, r.Reason)
				assert.Equal(t, c.owner, r.Match.OwnerID)
				assert.Equal(t, c.keyword, r.Match.Keyword)
				assert.Equal(t, c.score, r.Match.Score)
			})
		}
	}
}

func TestSuggestCounterpartExcluiAContaDoLoteAntesDaEscolha(t *testing.T) {
	casos := []struct {
		nome, lote, descricao string
		reason                textmatch.Reason
		owner, keyword        string
		score                 int
	}{
		{nome: "conta do lote é a melhor: vence a segunda, não ambiguous",
			lote: "conta-nubank", descricao: "pix nubank enviado",
			reason: textmatch.ReasonMatched, owner: "conta-c6", keyword: "nubanc", score: 88},
		{nome: "outra conta como lote: a melhor vence",
			lote: "conta-c6", descricao: "pix nubank enviado",
			reason: textmatch.ReasonMatched, owner: "conta-nubank", keyword: "nubank", score: 100},
		{nome: "empate desfeito porque uma das empatadas é a conta do lote",
			lote: "conta-inter", descricao: "ted inter",
			reason: textmatch.ReasonMatched, owner: "conta-caixa", keyword: "ted", score: 100},
		{nome: "empate entre duas contas que não são a do lote é ambíguo",
			lote: "conta-nubank", descricao: "ted inter",
			reason: textmatch.ReasonAmbiguous},
		{nome: "só a conta do lote casa: sem sugestão",
			lote: "conta-inter", descricao: "doc inter",
			reason: textmatch.ReasonBelowThreshold},
		{nome: "conta arquivada nunca é contraparte",
			lote: "conta-nubank", descricao: "ted bradesco", // "ted" é da Caixa: empate seria 100×100 se Bradesco entrasse
			reason: textmatch.ReasonMatched, owner: "conta-caixa", keyword: "ted", score: 100},
		{nome: "conta excluída nunca é contraparte",
			lote: "conta-nubank", descricao: "pix itau",
			reason: textmatch.ReasonBelowThreshold},
		{nome: "conta do lote desconhecida não muda nada",
			lote: "conta-inexistente", descricao: "pix nubank enviado",
			reason: textmatch.ReasonMatched, owner: "conta-nubank", keyword: "nubank", score: 100},
		{nome: "conta do lote vazia não muda nada",
			lote: "", descricao: "pix nubank enviado",
			reason: textmatch.ReasonMatched, owner: "conta-nubank", keyword: "nubank", score: 100},
	}

	for _, vaza := range []bool{false, true} {
		set := novaFixtura(vaza).carregar(t, casaA)
		for _, c := range casos {
			nome := c.nome
			if vaza {
				nome += " (fonte que vaza)"
			}
			t.Run(nome, func(t *testing.T) {
				r := resOK(set.SuggestCounterpart(c.lote, c.descricao))
				assert.Equal(t, c.reason, r.Reason)
				assert.Equal(t, c.owner, r.Match.OwnerID)
				assert.Equal(t, c.keyword, r.Match.Keyword)
				assert.Equal(t, c.score, r.Match.Score)
				assert.NotEqual(t, c.lote, r.Match.OwnerID, "a conta do lote nunca é contraparte de si mesma")
			})
		}
	}
}

// ADR-028(b): MatchAccount escolhe entre TODAS as contas ativas, sem excluir
// nenhuma — a própria conta da linha pode vencer, e é o chamador que decide o
// que isso significa. Empate entre contas diferentes continua ambíguo, e conta
// arquivada ou excluída continua fora, como em SuggestCounterpart.
func TestMatchAccountEscolheEntreTodasAsContasAtivasSemExcluirNenhuma(t *testing.T) {
	casos := []struct {
		nome, descricao string
		reason          textmatch.Reason
		owner, keyword  string
		score           int
	}{
		{nome: "a melhor vence mesmo que fosse a conta do lote",
			descricao: "pix nubank enviado",
			reason:    textmatch.ReasonMatched, owner: "conta-nubank", keyword: "nubank", score: 100},
		{nome: "a segunda melhor NÃO vence: nada é excluído",
			descricao: "pix nubanc",
			reason:    textmatch.ReasonMatched, owner: "conta-c6", keyword: "nubanc", score: 100},
		{nome: "empate entre duas contas é ambíguo, mesmo que uma fosse a própria",
			descricao: "ted inter",
			reason:    textmatch.ReasonAmbiguous},
		{nome: "abaixo do limiar: sem sugestão",
			descricao: "padaria da esquina",
			reason:    textmatch.ReasonBelowThreshold},
		{nome: "conta arquivada nunca participa",
			descricao: "ted bradesco", // se Bradesco entrasse, seria empate 100×100 com "ted" da Caixa
			reason:    textmatch.ReasonMatched, owner: "conta-caixa", keyword: "ted", score: 100},
		{nome: "conta excluída nunca participa",
			descricao: "pix itau",
			reason:    textmatch.ReasonBelowThreshold},
	}

	for _, vaza := range []bool{false, true} {
		set := novaFixtura(vaza).carregar(t, casaA)
		for _, c := range casos {
			nome := c.nome
			if vaza {
				nome += " (fonte que vaza)"
			}
			t.Run(nome, func(t *testing.T) {
				r := resOK(set.MatchAccount(c.descricao))
				assert.Equal(t, c.reason, r.Reason)
				assert.Equal(t, c.owner, r.Match.OwnerID)
				assert.Equal(t, c.keyword, r.Match.Keyword)
				assert.Equal(t, c.score, r.Match.Score)
			})
		}
	}

	// A diferença para SuggestCounterpart, lado a lado: com a Nubank como
	// conta do lote, a análise da importação entrega a SEGUNDA (C6, 88); o
	// reprocessamento entrega a PRÓPRIA Nubank (100) — e é o chamador que lê
	// "própria conta" como marcador de transferência entre contas próprias.
	set := novaFixtura(false).carregar(t, casaA)
	importacao := resOK(set.SuggestCounterpart("conta-nubank", "pix nubank enviado"))
	reprocessamento := resOK(set.MatchAccount("pix nubank enviado"))
	require.True(t, importacao.Matched())
	require.True(t, reprocessamento.Matched())
	assert.Equal(t, "conta-c6", importacao.Match.OwnerID)
	assert.Equal(t, "conta-nubank", reprocessamento.Match.OwnerID)

	// Nil-safe e set vazio, como os demais métodos.
	var nulo *classify.Set
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(nulo.MatchAccount("pix nubank")).Reason)
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK((&classify.Set{}).MatchAccount("pix nubank")).Reason)
}

func TestCasaBNuncaInfluenciaCasaA(t *testing.T) {
	f := novaFixtura(false)
	// Casa A sem palavra nenhuma; casa B com "supermercado", "uber", "salario",
	// "nubank" e "doc". Nada disso pode aparecer para a casa A.
	f.categorias.palavras = []category.Keyword{
		palavraDeCategoria(casaB, "cat-b-mercado", "supermercado"),
		palavraDeCategoria(casaB, "cat-b-mercado", "uber"),
		palavraDeCategoria(casaB, "cat-b-renda", "salario"),
	}
	f.contas.palavras = []account.Keyword{
		palavraDeConta(casaB, "conta-b-nubank", "nubank"),
		palavraDeConta(casaB, "conta-b-nubank", "doc"),
	}

	set := f.carregar(t, casaA)

	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCategory("expense", "supermercado extra")).Reason)
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCategory("expense", "uber viagem")).Reason)
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCategory("income", "salario mensal")).Reason)
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCounterpart("conta-c6", "pix nubank")).Reason)
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCounterpart("conta-c6", "doc recebido")).Reason)
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.MatchAccount("pix nubank")).Reason)
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.MatchAccount("doc recebido")).Reason)

	_, ok := set.CategoryName("cat-b-mercado")
	assert.False(t, ok, "nome de categoria de outra casa não existe para a casa A")

	for _, c := range f.reg.todas() {
		assert.Equal(t, casaA, c.Casa, "as fontes só recebem o householdID passado ao Load")
	}

	// E o inverso: carregar a casa B enxerga as palavras dela — prova que o
	// vazio acima é isolamento, e não fixture quebrada.
	setB := f.carregar(t, casaB)
	rB := resOK(setB.SuggestCategory("expense", "supermercado extra"))
	require.True(t, rB.Matched())
	assert.Equal(t, "cat-b-mercado", rB.Match.OwnerID)
	cB := resOK(setB.SuggestCounterpart("conta-x", "pix nubank"))
	require.True(t, cB.Matched())
	assert.Equal(t, "conta-b-nubank", cB.Match.OwnerID)
}

// Fonte com o isolamento quebrado: ListKeywords devolve palavras de TODAS as
// casas. O loader reconfere o household_id de cada palavra e descarta as
// alheias — mesmo quando a palavra aponta para um id de dona que existe na
// casa pedida (banco adulterado).
func TestLoaderDescartaPalavraDeOutraCasaMesmoComFonteQueVaza(t *testing.T) {
	f := novaFixtura(false)
	f.categorias.vazaCasas = true
	f.contas.vazaCasas = true
	f.categorias.palavras = []category.Keyword{
		palavraDeCategoria(casaB, "cat-b-mercado", "supermercado"),
		// Palavra da casa B apontando para uma categoria ATIVA da casa A.
		palavraDeCategoria(casaB, "cat-supermercado", "padaria"),
	}
	f.contas.palavras = []account.Keyword{
		palavraDeConta(casaB, "conta-b-nubank", "nubank"),
		palavraDeConta(casaB, "conta-c6", "inter"),
	}

	set := f.carregar(t, casaA)
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCategory("expense", "supermercado extra")).Reason)
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCategory("expense", "padaria central")).Reason)
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCounterpart("conta-nubank", "pix nubank")).Reason)
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCounterpart("conta-nubank", "ted inter")).Reason)
}

func TestCategoryName(t *testing.T) {
	set := novaFixtura(true).carregar(t, casaA)

	nome, ok := set.CategoryName("cat-supermercado")
	assert.True(t, ok)
	assert.Equal(t, "Supermercado", nome)

	nome, ok = set.CategoryName("cat-alimentacao")
	assert.True(t, ok, "grupo ativo tem nome, mesmo fora do matcher")
	assert.Equal(t, "Alimentação", nome)

	_, ok = set.CategoryName("cat-viagem")
	assert.False(t, ok, "arquivada não faz parte do conjunto")
	_, ok = set.CategoryName("cat-antiga")
	assert.False(t, ok, "excluída não faz parte do conjunto")
	_, ok = set.CategoryName("cat-b-mercado")
	assert.False(t, ok, "outra casa")
	_, ok = set.CategoryName("")
	assert.False(t, ok)
}

// AssignableKind é a pergunta "esta categoria, por id, ainda pode receber
// lançamento?" — a que o confirm da importação faz para descartar a sugestão
// que venceu entre a análise e a confirmação (spec 0005 §13).
//
// O contraste com CategoryName é o ponto do teste: o grupo COM filhas ativas
// tem nome (está ativo) e mesmo assim NÃO é atribuível. Confundir os dois
// devolveria o modo de falha que a correção fechou.
func TestAssignableKind(t *testing.T) {
	set := novaFixtura(true).carregar(t, casaA)

	casos := []struct {
		id      string
		kind    string
		ok      bool
		porque  string
		temNome bool
	}{
		{id: "cat-supermercado", kind: category.KindExpense, ok: true, porque: "folha ativa", temNome: true},
		{id: "cat-salario", kind: category.KindIncome, ok: true, porque: "folha ativa de receita", temNome: true},
		{id: "cat-transporte", kind: category.KindExpense, ok: true, porque: "grupo SEM filhas recebe lançamento", temNome: true},
		{id: "cat-saude", kind: category.KindExpense, ok: true, porque: "a única filha está arquivada: o grupo volta a ser destino", temNome: true},
		{id: "cat-alimentacao", ok: false, porque: "grupo com filhas ativas", temNome: true},
		{id: "cat-lazer", ok: false, porque: "grupo com uma filha ativa", temNome: true},
		{id: "cat-renda", ok: false, porque: "grupo de receita com filha ativa", temNome: true},
		{id: "cat-viagem", ok: false, porque: "arquivada"},
		{id: "cat-antiga", ok: false, porque: "excluída"},
		{id: "cat-b-mercado", ok: false, porque: "categoria de OUTRA casa"},
		{id: "cat-inexistente", ok: false, porque: "id que não existe"},
		{id: "", ok: false, porque: "id vazio"},
	}
	for _, c := range casos {
		t.Run(c.porque, func(t *testing.T) {
			kind, ok := set.AssignableKind(c.id)
			assert.Equal(t, c.ok, ok, c.porque)
			assert.Equal(t, c.kind, kind)
			if c.temNome {
				_, nomeOk := set.CategoryName(c.id)
				assert.True(t, nomeOk, "ativa: tem nome mesmo quando não é atribuível")
			}
		})
	}
}

// Set nulo e zero value respondem false: sem conjunto carregado não há como
// afirmar que a categoria vale, e o silêncio não pode virar aprovação.
func TestAssignableKindEmSetVazioENulo(t *testing.T) {
	zero := &classify.Set{}
	var nulo *classify.Set

	for nome, set := range map[string]*classify.Set{"zero value": zero, "nulo": nulo} {
		t.Run(nome, func(t *testing.T) {
			kind, ok := set.AssignableKind("cat-supermercado")
			assert.False(t, ok)
			assert.Empty(t, kind)
		})
	}
}

func TestSetVazioENuloRespondemSemSugestao(t *testing.T) {
	reg := &registro{}
	vazio, err := classify.NewLoader(&categoriasFake{reg: reg}, &contasFake{reg: reg}).Load(context.Background(), casaA)
	require.NoError(t, err)

	zero := &classify.Set{}
	var nulo *classify.Set

	for nome, set := range map[string]*classify.Set{"carregado sem palavras": vazio, "zero value": zero, "nulo": nulo} {
		t.Run(nome, func(t *testing.T) {
			assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCategory("expense", "supermercado extra")).Reason)
			assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCategory("income", "salario")).Reason)
			assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCounterpart("conta-x", "pix nubank")).Reason)
			_, ok := set.CategoryName("cat-supermercado")
			assert.False(t, ok)
		})
	}
}

// Casa com categorias e contas mas SEM palavra-chave (o estado inicial de toda
// casa): tudo carrega e nada é sugerido.
func TestCasaSemPalavrasCarregaENaoSugere(t *testing.T) {
	f := novaFixtura(false)
	f.categorias.palavras = nil
	f.contas.palavras = nil
	set := f.carregar(t, casaA)

	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCategory("expense", "supermercado extra")).Reason)
	assert.Equal(t, textmatch.ReasonBelowThreshold, resOK(set.SuggestCounterpart("conta-c6", "pix nubank")).Reason)
	nome, ok := set.CategoryName("cat-supermercado")
	assert.True(t, ok)
	assert.Equal(t, "Supermercado", nome)
}

func TestLoadPropagaErroDeCadaFonte(t *testing.T) {
	sentinela := errors.New("banco fora do ar")
	casos := []struct{ fonte, metodo string }{
		{"categorias", "List"}, {"categorias", "ListKeywords"},
		{"contas", "List"}, {"contas", "ListKeywords"},
	}
	for _, c := range casos {
		t.Run(c.fonte+"."+c.metodo, func(t *testing.T) {
			f := novaFixtura(false)
			switch c.fonte {
			case "categorias":
				f.categorias.erros = map[string]error{c.metodo: sentinela}
			case "contas":
				f.contas.erros = map[string]error{c.metodo: sentinela}
			}
			set, err := classify.NewLoader(f.categorias, f.contas).Load(context.Background(), casaA)
			require.ErrorIs(t, err, sentinela)
			assert.Nil(t, set)
		})
	}
}

// Palavra gravada que não produz token (só stopword) não deveria existir —
// ValidateKeyword barra na borda. Se existir, o Load falha alto, embrulhando
// ErrInvalidKeyword, e a mensagem NÃO carrega a palavra: ela passa pelo log.
func TestLoadFalhaSemEcoarPalavraInvalidaGravada(t *testing.T) {
	const invalida = "ltda"
	casos := []struct {
		nome    string
		preparo func(f *fixtura)
	}{
		{"categoria de despesa", func(f *fixtura) {
			f.categorias.palavras = append(f.categorias.palavras, palavraDeCategoria(casaA, "cat-supermercado", invalida))
		}},
		{"categoria de receita", func(f *fixtura) {
			f.categorias.palavras = append(f.categorias.palavras, palavraDeCategoria(casaA, "cat-salario", invalida))
		}},
		{"conta", func(f *fixtura) {
			f.contas.palavras = append(f.contas.palavras, palavraDeConta(casaA, "conta-nubank", invalida))
		}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			f := novaFixtura(false)
			c.preparo(f)
			set, err := classify.NewLoader(f.categorias, f.contas).Load(context.Background(), casaA)
			require.ErrorIs(t, err, textmatch.ErrInvalidKeyword)
			assert.Nil(t, set)
			assert.False(t, strings.Contains(err.Error(), invalida), "o erro nunca ecoa a palavra: %q", err.Error())
		})
	}

	// Palavra inválida de dona ARQUIVADA nem chega ao matcher: o descarte vem antes.
	t.Run("palavra inválida de dona arquivada é descartada antes do matcher", func(t *testing.T) {
		f := novaFixtura(false)
		f.categorias.palavras = append(f.categorias.palavras, palavraDeCategoria(casaA, "cat-viagem", invalida))
		f.contas.palavras = append(f.contas.palavras, palavraDeConta(casaA, "conta-bradesco", invalida))
		_, err := classify.NewLoader(f.categorias, f.contas).Load(context.Background(), casaA)
		require.NoError(t, err)
	})
}

// O Set é compartilhado entre as goroutines de uma análise; o -race é quem
// confere. O teste só exercita as três consultas em paralelo.
func TestSetEhSeguroParaUsoConcorrente(t *testing.T) {
	set := novaFixtura(false).carregar(t, casaA)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				assert.True(t, resOK(set.SuggestCategory("expense", "supermercado extra")).Matched())
				assert.True(t, resOK(set.SuggestCounterpart("conta-nubank", "pix nubank enviado")).Matched())
				_, ok := set.CategoryName("cat-supermercado")
				assert.True(t, ok)
			}
		}()
	}
	wg.Wait()
}
