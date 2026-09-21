package aiprompt_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/aiprompt"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Os testes deste arquivo provam o SERVIÇO: a janela, a dobra por descrição, o
// corte e o isolamento por casa. O texto em si (as nove seções, a minimização,
// o tamanho do preâmbulo) está em prompt_test.go; o contrato HTTP, em
// handler_test.go.

const (
	minhaCasa = "casa-1"
	outraCasa = "casa-2"
	usuario   = "11111111-1111-7111-8111-111111111111"

	// Os três meses da janela máxima da spec 0010.
	mes1 = "2026-07"
	mes2 = "2026-08"
	mes3 = "2026-09"

	contaCorrente = "conta-corrente"
	contaCartao   = "conta-cartao"
	contaVelha    = "conta-arquivada"

	grupoAlimentacao = "cat-grupo-alimentacao"
	catMercado       = "cat-mercado"
	catAportes       = "cat-aportes"
	catVelha         = "cat-arquivada"
)

var (
	arquivadaEm = time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	geradoEm    = time.Date(2026, 9, 21, 15, 4, 5, 0, time.UTC)
)

func ator(casa string) aiprompt.Actor {
	return aiprompt.Actor{HouseholdID: casa, UserID: usuario}
}

func ptr(s string) *string { return &s }

// --- dublês -----------------------------------------------------------------

// lancamento é uma linha crua do razão. O ledgerFake agrega estas linhas
// EXATAMENTE como o SQL de gormstore.GroupByDescription agrega: por (descrição
// normalizada, kind, conta, categoria), sobre a casa e a janela pedidas.
//
// O dublê faz a agregação de verdade, e não devolve grupos prontos, porque é
// só assim que os critérios 5 e 10 da spec 0010 (agrupar por norma; o total do
// grupo bater com a soma dos lançamentos) viram teste em vez de fixture.
type lancamento struct {
	casa      string
	mes       string
	desc      string
	kind      string
	conta     string
	categoria *string
	cents     int64
}

type chamadaLedger struct {
	casa  string
	meses []string
	limit int
}

type ledgerFake struct {
	linhas   []lancamento
	err      error
	chamadas []chamadaLedger
}

func (l *ledgerFake) GroupByDescription(_ context.Context, householdID string,
	competenceMonths []string, limit int,
) ([]transaction.DescriptionGroup, error) {
	l.chamadas = append(l.chamadas, chamadaLedger{householdID, slices.Clone(competenceMonths), limit})
	if l.err != nil {
		return nil, l.err
	}

	type chave struct {
		norm, kind, conta, categoria string
	}
	grupos := map[chave]*transaction.DescriptionGroup{}
	for _, t := range l.linhas {
		// O repositório filtra por casa e por competência na camada mais
		// baixa; o dublê faz o mesmo, para que uma falha de escopo apareça
		// aqui como dado a mais e não como nada.
		if t.casa != householdID || !slices.Contains(competenceMonths, t.mes) {
			continue
		}
		k := chave{norm: textnorm.Normalize(t.desc), kind: t.kind, conta: t.conta}
		if t.categoria != nil {
			k.categoria = *t.categoria
		}
		g := grupos[k]
		if g == nil {
			g = &transaction.DescriptionGroup{
				DescriptionNorm:   k.norm,
				SampleDescription: t.desc,
				Kind:              t.kind,
				AccountID:         t.conta,
				CategoryID:        t.categoria,
			}
			grupos[k] = g
		}
		if t.desc < g.SampleDescription {
			g.SampleDescription = t.desc
		}
		g.Count++
		g.TotalCents += t.cents
	}

	// Tudo ou nada acima do teto, como o repositório real.
	if len(grupos) > limit {
		return nil, fmt.Errorf("%w: %d grupos", transaction.ErrTooManyDescriptionGroups, len(grupos))
	}

	out := make([]transaction.DescriptionGroup, 0, len(grupos))
	for _, k := range slices.SortedFunc(maps.Keys(grupos), func(a, b chave) int {
		return strings.Compare(fmt.Sprint(a), fmt.Sprint(b))
	}) {
		out = append(out, *grupos[k])
	}
	return out, nil
}

type chamadaLista struct {
	casa            string
	includeArchived bool
}

// categoriasFake filtra por casa e por arquivamento como o repositório real.
type categoriasFake struct {
	cats      []category.Category
	kws       []category.Keyword
	err       error
	listagens []chamadaLista
	palavras  []string
}

func (c *categoriasFake) List(_ context.Context, householdID string, includeArchived bool) ([]category.Category, error) {
	c.listagens = append(c.listagens, chamadaLista{householdID, includeArchived})
	if c.err != nil {
		return nil, c.err
	}
	var out []category.Category
	for _, cat := range c.cats {
		if cat.HouseholdID != householdID {
			continue
		}
		if !includeArchived && cat.ArchivedAt != nil {
			continue
		}
		out = append(out, cat)
	}
	return out, nil
}

func (c *categoriasFake) ListKeywords(_ context.Context, householdID string) ([]category.Keyword, error) {
	c.palavras = append(c.palavras, householdID)
	if c.err != nil {
		return nil, c.err
	}
	var out []category.Keyword
	for _, k := range c.kws {
		if k.HouseholdID == householdID {
			out = append(out, k)
		}
	}
	return out, nil
}

// contasFake filtra por casa e por arquivamento como o repositório real.
type contasFake struct {
	accs      []account.Account
	kws       []account.Keyword
	err       error
	listagens []chamadaLista
	palavras  []string
}

func (c *contasFake) List(_ context.Context, householdID string, includeArchived bool) ([]account.Account, error) {
	c.listagens = append(c.listagens, chamadaLista{householdID, includeArchived})
	if c.err != nil {
		return nil, c.err
	}
	var out []account.Account
	for _, a := range c.accs {
		if a.HouseholdID != householdID {
			continue
		}
		if !includeArchived && a.ArchivedAt != nil {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func (c *contasFake) ListKeywords(_ context.Context, householdID string) ([]account.Keyword, error) {
	c.palavras = append(c.palavras, householdID)
	if c.err != nil {
		return nil, c.err
	}
	var out []account.Keyword
	for _, k := range c.kws {
		if k.HouseholdID == householdID {
			out = append(out, k)
		}
	}
	return out, nil
}

// --- ambiente ---------------------------------------------------------------

type ambiente struct {
	ledger *ledgerFake
	cats   *categoriasFake
	contas *contasFake
	logs   *bytes.Buffer
	svc    *aiprompt.Service
}

func novoAmbiente(t *testing.T) *ambiente {
	t.Helper()
	a := &ambiente{
		ledger: &ledgerFake{},
		cats:   &categoriasFake{},
		contas: &contasFake{},
		logs:   &bytes.Buffer{},
	}
	lg := logging.New(a.logs, logging.Options{Level: "debug", Format: "json"})
	a.svc = aiprompt.NewService(a.ledger, a.cats, a.contas, lg,
		aiprompt.WithClock(func() time.Time { return geradoEm }))
	return a
}

// casaComum monta a casa das duas pontas: contas e categorias ativas, uma
// conta e uma categoria ARQUIVADAS (que não podem aparecer no texto) e as
// palavras-chave de cada uma. O saldo de abertura, a instituição e os dias de
// fatura são preenchidos DE PROPÓSITO: é o que a varredura negativa procura.
func (a *ambiente) casaComum() {
	fechamento, vencimento := 20, 27
	a.contas.accs = []account.Account{
		{
			ID: contaCorrente, HouseholdID: minhaCasa, Name: "Conta Corrente",
			NameNorm: "conta corrente", Kind: account.KindChecking,
			Institution: "banco-inter", OpeningBalanceCents: 123_456,
		},
		{
			ID: contaCartao, HouseholdID: minhaCasa, Name: "Nubank",
			NameNorm: "nubank", Kind: account.KindCreditCard,
			Institution: "nubank", OpeningBalanceCents: 987_654,
			StatementClosingDay: &fechamento, StatementDueDay: &vencimento,
		},
		{
			ID: contaVelha, HouseholdID: minhaCasa, Name: "Poupança Antiga",
			NameNorm: "poupanca antiga", Kind: account.KindSavings,
			Institution: "outra", ArchivedAt: &arquivadaEm,
		},
		// Conta de OUTRA casa, com nome parecido: se o escopo cair, ela
		// aparece no texto — e é isso que o teste de BOLA procura.
		{
			ID: "conta-alheia", HouseholdID: outraCasa, Name: "Conta Da Casa Alheia",
			NameNorm: "conta da casa alheia", Kind: account.KindChecking,
		},
	}
	a.contas.kws = []account.Keyword{
		{ID: "k1", HouseholdID: minhaCasa, AccountID: contaCartao, Keyword: "nu pagamentos", Norm: "nu pagamentos", Position: 1},
		{ID: "k2", HouseholdID: minhaCasa, AccountID: contaCartao, Keyword: "nubank", Norm: "nubank", Position: 0},
		{ID: "k3", HouseholdID: outraCasa, AccountID: "conta-alheia", Keyword: "segredo alheio", Norm: "segredo alheio"},
	}

	a.cats.cats = []category.Category{
		{ID: grupoAlimentacao, HouseholdID: minhaCasa, Name: "Alimentação", NameNorm: "alimentacao", Kind: category.KindExpense},
		{ID: catMercado, HouseholdID: minhaCasa, ParentID: ptr(grupoAlimentacao), Name: "Mercado", NameNorm: "mercado", Kind: category.KindExpense},
		{ID: catAportes, HouseholdID: minhaCasa, Name: "Aportes", NameNorm: "aportes", Kind: category.KindInvestment},
		{ID: catVelha, HouseholdID: minhaCasa, ParentID: ptr(grupoAlimentacao), Name: "Restaurante Antigo",
			NameNorm: "restaurante antigo", Kind: category.KindExpense, ArchivedAt: &arquivadaEm},
		{ID: "cat-alheia", HouseholdID: outraCasa, Name: "Categoria Alheia", NameNorm: "categoria alheia", Kind: category.KindExpense},
	}
	a.cats.kws = []category.Keyword{
		{ID: "ck1", HouseholdID: minhaCasa, CategoryID: catMercado, Keyword: "zaffari", Norm: "zaffari", Position: 0},
		{ID: "ck2", HouseholdID: minhaCasa, CategoryID: catVelha, Keyword: "palavra da arquivada", Norm: "palavra da arquivada", Position: 0},
		{ID: "ck3", HouseholdID: outraCasa, CategoryID: "cat-alheia", Keyword: "palavra alheia", Norm: "palavra alheia", Position: 0},
	}
}

func (a *ambiente) exportar(t *testing.T, casa, de, ate string) aiprompt.PromptView {
	t.Helper()
	view, err := a.svc.ExportPrompt(t.Context(), ator(casa), aiprompt.ExportInput{FromMonth: de, ToMonth: ate})
	require.NoError(t, err)
	return view
}

// linhaDaTabela devolve a linha da seção 8 cuja primeira célula começa com o
// texto pedido. Procurar pela linha, e não por substring solta, é o que impede
// o teste de passar por causa de uma ocorrência em outra seção.
func linhaDaTabela(t *testing.T, prompt, comeca string) string {
	t.Helper()
	for _, l := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(l, "| "+comeca) {
			return l
		}
	}
	t.Fatalf("nenhuma linha de tabela começando com %q", comeca)
	return ""
}

// --- a janela ---------------------------------------------------------------

// Critério 1 e 2 da spec 0010, no serviço: 3 meses passam, 4 não; invertida
// não; malformado não. O handler traduz cada um para o campo certo.
func TestJanelaDeCompetencia(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome    string
		de, ate string
		erro    error
		meses   []string
	}{
		{"um mês só", mes3, mes3, nil, []string{mes3}},
		{"exatamente três", mes1, mes3, nil, []string{mes1, mes2, mes3}},
		{"virada de ano", "2025-12", "2026-01", nil, []string{"2025-12", "2026-01"}},
		// A virada de ano na janela CHEIA é o caso que uma subtração ingênua
		// erra em silêncio: `2026-01` menos 2 daria `2026--1`, e o `IN` traria
		// zero linha sem erro nenhum. A aritmética é de índice de mês, e é aqui
		// que isso fica afirmado nos dois sentidos.
		{"três meses atravessando a virada de ano", "2025-11", "2026-01", nil,
			[]string{"2025-11", "2025-12", "2026-01"}},
		{"quatro meses atravessando a virada de ano", "2025-10", "2026-01", aiprompt.ErrWindowTooLong, nil},
		{"invertida atravessando a virada de ano", "2026-01", "2025-12", aiprompt.ErrWindowInverted, nil},
		{"mês zero", "2026-00", mes3, aiprompt.ErrInvalidFromMonth, nil},
		{"ano zero", "0000-01", "0000-03", aiprompt.ErrInvalidFromMonth, nil},
		{"quatro meses", "2026-06", mes3, aiprompt.ErrWindowTooLong, nil},
		{"invertida", mes3, mes1, aiprompt.ErrWindowInverted, nil},
		{"from ausente", "", mes3, aiprompt.ErrInvalidFromMonth, nil},
		{"to ausente", mes1, "", aiprompt.ErrInvalidToMonth, nil},
		{"from malformado", "2026-7", mes3, aiprompt.ErrInvalidFromMonth, nil},
		{"to malformado", mes1, "2026-13", aiprompt.ErrInvalidToMonth, nil},
		{"from com espaço", " 2026-07", mes3, aiprompt.ErrInvalidFromMonth, nil},
		{"to com dia", mes1, "2026-09-01", aiprompt.ErrInvalidToMonth, nil},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			a := novoAmbiente(t)
			a.casaComum()

			_, err := a.svc.ExportPrompt(t.Context(), ator(minhaCasa),
				aiprompt.ExportInput{FromMonth: c.de, ToMonth: c.ate})

			if c.erro != nil {
				require.ErrorIs(t, err, c.erro)
				assert.Empty(t, a.ledger.chamadas, "nada pode ir ao banco com janela inválida")
				return
			}
			require.NoError(t, err)
			require.Len(t, a.ledger.chamadas, 1, "UMA consulta agregada por requisição")
			assert.Equal(t, c.meses, a.ledger.chamadas[0].meses,
				"a janela vira uma lista FECHADA de meses — é ela que autoriza o IN (?)")
			assert.Equal(t, transaction.MaxDescriptionGroupRows, a.ledger.chamadas[0].limit)
		})
	}
}

// Ator sem casa nunca chega ao banco. O handler já barra antes; esta é a defesa
// em profundidade.
func TestSemCasaNaoConsultaNada(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	_, err := a.svc.ExportPrompt(t.Context(), aiprompt.Actor{UserID: usuario},
		aiprompt.ExportInput{FromMonth: mes1, ToMonth: mes3})

	require.ErrorIs(t, err, aiprompt.ErrUnauthenticated)
	assert.Empty(t, a.ledger.chamadas)
	assert.Empty(t, a.cats.listagens)
	assert.Empty(t, a.contas.listagens)
}

// --- BOLA -------------------------------------------------------------------

// Critério 8 da spec 0010: com DUAS casas povoadas, o prompt de uma nunca traz
// nada da outra — e a casa que vai às QUATRO fontes é sempre a do token.
//
// As descrições das duas casas são IGUAIS de propósito: se o escopo caísse, o
// defeito apareceria como contagem inflada (a forma que passa despercebida numa
// revisão de olho), e não como uma linha estranha.
func TestIsolamentoEntreCasas(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.linhas = []lancamento{
		{casa: minhaCasa, mes: mes2, desc: "Mercado do Seu Jose", kind: transaction.KindExpense, conta: contaCorrente, cents: 1_000},
		{casa: minhaCasa, mes: mes2, desc: "Mercado do Seu Jose", kind: transaction.KindExpense, conta: contaCorrente, cents: 1_000},
		{casa: outraCasa, mes: mes2, desc: "Mercado do Seu Jose", kind: transaction.KindExpense, conta: "conta-alheia", cents: 99_999},
		{casa: outraCasa, mes: mes2, desc: "Padaria Alheia", kind: transaction.KindExpense, conta: "conta-alheia", cents: 77_777},
	}

	view := a.exportar(t, minhaCasa, mes1, mes3)

	assert.Equal(t, 2, view.Stats.Transactions, "só os 2 lançamentos da minha casa")
	assert.Equal(t, 1, view.Stats.Descriptions)
	assert.NotContains(t, view.Prompt, "Padaria Alheia")
	assert.NotContains(t, view.Prompt, "Conta Da Casa Alheia")
	assert.NotContains(t, view.Prompt, "Categoria Alheia")
	assert.NotContains(t, view.Prompt, "palavra alheia")
	assert.NotContains(t, view.Prompt, "segredo alheio")
	assert.NotContains(t, view.Prompt, "99.999")
	assert.NotContains(t, view.Prompt, outraCasa)

	// A casa do TOKEN, e mais nenhuma, chegou às quatro fontes.
	require.Len(t, a.ledger.chamadas, 1)
	assert.Equal(t, minhaCasa, a.ledger.chamadas[0].casa)
	assert.Equal(t, []chamadaLista{{minhaCasa, true}}, a.cats.listagens,
		"categorias com as ARQUIVADAS: o predicado de investimento precisa delas")
	assert.Equal(t, []chamadaLista{{minhaCasa, false}}, a.contas.listagens,
		"contas SEM as arquivadas: a conta entra no prompt só para ser nomeada")
	assert.Equal(t, []string{minhaCasa}, a.cats.palavras)
	assert.Equal(t, []string{minhaCasa}, a.contas.palavras)
}

// --- a dobra ----------------------------------------------------------------

// Critérios 5 e 10: duas grafias da MESMA descrição viram UMA linha, com a soma
// das ocorrências e dos centavos — inclusive quando o SQL as separou por conta
// e por categoria.
func TestDescricoesEquivalentesViramUmaLinhaComOsTotaisSomados(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.linhas = []lancamento{
		{casa: minhaCasa, mes: mes1, desc: "MERCADO DO SEU JOSÉ", kind: transaction.KindExpense, conta: contaCorrente, categoria: ptr(catMercado), cents: 10_000},
		{casa: minhaCasa, mes: mes2, desc: "mercado do seu jose", kind: transaction.KindExpense, conta: contaCartao, categoria: ptr(catMercado), cents: 2_550},
		{casa: minhaCasa, mes: mes3, desc: "Mercado do Seu José", kind: transaction.KindExpense, conta: contaCorrente, cents: 1_45},
	}

	view := a.exportar(t, minhaCasa, mes1, mes3)

	assert.Equal(t, 1, view.Stats.Descriptions, "as três grafias são UMA descrição")
	assert.Equal(t, 3, view.Stats.Transactions)

	linha := linhaDaTabela(t, view.Prompt, "MERCADO")
	assert.Contains(t, linha, "| 3 |", "as ocorrências somam")
	// 10.000 + 2.550 + 145 = 12.695 centavos.
	assert.Contains(t, linha, "R$ 126,95", "o total do grupo bate com a soma dos lançamentos")
	assert.Contains(t, linha, "Conta Corrente, Nubank", "as duas contas em que apareceu")
	assert.Contains(t, linha, "várias",
		"uma ocorrência sem categoria e duas com a mesma categoria DIVERGEM")
}

// A categoria só é nomeada quando TODAS as ocorrências têm a mesma; sem
// nenhuma, é o travessão.
func TestColunaDeCategoria(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.linhas = []lancamento{
		{casa: minhaCasa, mes: mes1, desc: "Zaffari", kind: transaction.KindExpense, conta: contaCorrente, categoria: ptr(catMercado), cents: 100},
		{casa: minhaCasa, mes: mes1, desc: "Zaffari", kind: transaction.KindExpense, conta: contaCartao, categoria: ptr(catMercado), cents: 100},
		{casa: minhaCasa, mes: mes1, desc: "Posto Shell", kind: transaction.KindExpense, conta: contaCorrente, cents: 100},
	}

	view := a.exportar(t, minhaCasa, mes1, mes3)

	assert.Contains(t, linhaDaTabela(t, view.Prompt, "Zaffari"), "Alimentação > Mercado")
	assert.Contains(t, linhaDaTabela(t, view.Prompt, "Posto Shell"), "| — |")
}

// O `kindGroup` sai de transaction.MarcadaComoInvestimento (ADR-031f): a
// despesa cuja categoria é de natureza `investment` é INVESTIMENTO, não
// despesa — e a perna de transferência é transferência, venha de onde vier.
func TestTipoDaLinhaSaiDoPredicadoDeInvestimento(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.linhas = []lancamento{
		{casa: minhaCasa, mes: mes1, desc: "Aporte Tesouro", kind: transaction.KindExpense, conta: contaCorrente, categoria: ptr(catAportes), cents: 50_000},
		{casa: minhaCasa, mes: mes1, desc: "Salario", kind: transaction.KindIncome, conta: contaCorrente, cents: 500_000},
		{casa: minhaCasa, mes: mes1, desc: "Padaria", kind: transaction.KindExpense, conta: contaCorrente, cents: 1_000},
		{casa: minhaCasa, mes: mes1, desc: "Pagamento Fatura", kind: transaction.KindTransferOut, conta: contaCorrente, cents: 30_000},
		{casa: minhaCasa, mes: mes1, desc: "Pagamento Fatura", kind: transaction.KindTransferIn, conta: contaCartao, cents: 30_000},
	}

	view := a.exportar(t, minhaCasa, mes1, mes3)

	assert.Contains(t, linhaDaTabela(t, view.Prompt, "Aporte Tesouro"), "| investimento |")
	assert.Contains(t, linhaDaTabela(t, view.Prompt, "Salario"), "| receita |")
	assert.Contains(t, linhaDaTabela(t, view.Prompt, "Padaria"), "| despesa |")
	assert.Contains(t, linhaDaTabela(t, view.Prompt, "Pagamento Fatura"), "| transferência |",
		"as duas pernas caem na MESMA linha e no MESMO tipo")
}

// A ordem é por ocorrências DECRESCENTE, com desempate estável: o mesmo
// conjunto de linhas gera o mesmo prompt duas vezes.
func TestOrdemPorOcorrenciasEhEstavel(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	for i := range 5 {
		a.ledger.linhas = append(a.ledger.linhas, lancamento{
			casa: minhaCasa, mes: mes1, desc: "Frequente", kind: transaction.KindExpense,
			conta: contaCorrente, cents: int64(100 + i),
		})
	}
	// Duas descrições com a MESMA contagem e o MESMO total: só o desempate por
	// descrição normalizada as separa, e ele precisa ser determinístico.
	a.ledger.linhas = append(a.ledger.linhas,
		lancamento{casa: minhaCasa, mes: mes1, desc: "Bbb Empate", kind: transaction.KindExpense, conta: contaCorrente, cents: 700},
		lancamento{casa: minhaCasa, mes: mes1, desc: "Aaa Empate", kind: transaction.KindExpense, conta: contaCorrente, cents: 700},
	)

	primeiro := a.exportar(t, minhaCasa, mes1, mes3)
	segundo := a.exportar(t, minhaCasa, mes1, mes3)
	// O nonce da cerca de dados MUDA de propósito a cada requisição (ver
	// prompt_cerca_test.go), então a comparação é sobre o texto com ele
	// neutralizado: o que este teste afirma é que a ORDEM e o conteúdo são
	// determinísticos, não que o prompt seja byte a byte imutável.
	assert.Equal(t, semNonce(primeiro.Prompt), semNonce(segundo.Prompt),
		"o mesmo banco gera o mesmo prompt (a menos do nonce da cerca)")
	assert.NotEqual(t, primeiro.Prompt, segundo.Prompt,
		"e o nonce, esse, tem de mudar")

	posFrequente := strings.Index(primeiro.Prompt, "| Frequente |")
	posAaa := strings.Index(primeiro.Prompt, "| Aaa Empate |")
	posBbb := strings.Index(primeiro.Prompt, "| Bbb Empate |")
	require.Positive(t, posFrequente)
	assert.Less(t, posFrequente, posAaa, "5 ocorrências vêm antes de 1")
	assert.Less(t, posAaa, posBbb, "o desempate é por descrição normalizada")
}

// --- o corte ----------------------------------------------------------------

// O teto de descrições do prompt é 500, e o corte é por ocorrências
// decrescentes: fica o que mais aparece. O número está escrito aqui de
// propósito — o prompt é artefato versionado do produto, e mudá-lo tem de
// passar por este teste.
func TestCorteEmQuinhentasDescricoes(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()

	// 502 descrições distintas: as 500 primeiras com 2 ocorrências, as duas
	// últimas com 1 — são elas que têm de cair.
	for i := range 500 {
		desc := fmt.Sprintf("Compra %04d", i)
		a.ledger.linhas = append(a.ledger.linhas,
			lancamento{casa: minhaCasa, mes: mes1, desc: desc, kind: transaction.KindExpense, conta: contaCorrente, cents: 100},
			lancamento{casa: minhaCasa, mes: mes1, desc: desc, kind: transaction.KindExpense, conta: contaCorrente, cents: 100},
		)
	}
	a.ledger.linhas = append(a.ledger.linhas,
		lancamento{casa: minhaCasa, mes: mes1, desc: "Rara Um", kind: transaction.KindExpense, conta: contaCorrente, cents: 100},
		lancamento{casa: minhaCasa, mes: mes1, desc: "Rara Dois", kind: transaction.KindExpense, conta: contaCorrente, cents: 100},
	)

	view := a.exportar(t, minhaCasa, mes1, mes3)

	assert.Equal(t, 500, view.Stats.Descriptions)
	assert.Equal(t, 2, view.Stats.TruncatedDescriptions)
	assert.Equal(t, 1000, view.Stats.Transactions, "conta os lançamentos que FICARAM")
	assert.NotContains(t, view.Prompt, "Rara Um", "o corte é por ocorrências decrescentes")
	assert.NotContains(t, view.Prompt, "Rara Dois")
	// O corte é DECLARADO no texto, e não só em stats.
	assert.Contains(t, view.Prompt, "**2 descrições ficaram de fora**")
}

// Corpus abaixo do teto não declara corte nenhum: nada de aviso onde não houve
// perda.
func TestSemCorteNaoDeclaraNada(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.linhas = []lancamento{
		{casa: minhaCasa, mes: mes1, desc: "Padaria", kind: transaction.KindExpense, conta: contaCorrente, cents: 100},
	}

	view := a.exportar(t, minhaCasa, mes1, mes3)

	assert.Zero(t, view.Stats.TruncatedDescriptions)
	assert.NotContains(t, view.Prompt, "ficaram de fora")
}

// O estouro da AGREGAÇÃO (o teto de memória do repositório) é diferente do
// corte: ele sobe como erro, embrulhado, e nunca vira resposta parcial.
func TestAgregacaoQueEstouraNaoViraRespostaParcial(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.err = fmt.Errorf("agrupando: %w", transaction.ErrTooManyDescriptionGroups)

	view, err := a.svc.ExportPrompt(t.Context(), ator(minhaCasa),
		aiprompt.ExportInput{FromMonth: mes1, ToMonth: mes3})

	require.ErrorIs(t, err, transaction.ErrTooManyDescriptionGroups)
	assert.Empty(t, view.Prompt, "nada parcial sai daqui")
}

// Falha de qualquer fonte é erro embrulhado com contexto, nunca um prompt sem a
// seção correspondente.
func TestFalhaDeFonteNaoProduzPromptIncompleto(t *testing.T) {
	t.Parallel()

	quebrar := map[string]func(*ambiente){
		"categorias": func(a *ambiente) { a.cats.err = errors.New("banco fora") },
		"contas":     func(a *ambiente) { a.contas.err = errors.New("banco fora") },
		"agregacao":  func(a *ambiente) { a.ledger.err = errors.New("banco fora") },
	}
	for nome, quebra := range quebrar {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			a := novoAmbiente(t)
			a.casaComum()
			quebra(a)

			view, err := a.svc.ExportPrompt(t.Context(), ator(minhaCasa),
				aiprompt.ExportInput{FromMonth: mes1, ToMonth: mes3})

			require.Error(t, err)
			assert.Empty(t, view.Prompt)
		})
	}
}

// --- contagens --------------------------------------------------------------

// Critério 9: período SEM movimentação gera prompt válido, com a linha de
// "nenhuma movimentação" — contas e categorias bastam.
func TestPeriodoSemMovimentacaoGeraPromptValido(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()

	view := a.exportar(t, minhaCasa, mes1, mes3)

	assert.Contains(t, view.Prompt, "Nenhuma movimentação nesta janela de competência.")
	assert.Contains(t, view.Prompt, "Conta Corrente", "as contas continuam listadas")
	assert.Contains(t, view.Prompt, "Alimentação > Mercado", "as categorias continuam listadas")
	assert.Equal(t, aiprompt.StatsView{
		Accounts: 2, Categories: 3, Descriptions: 0, Transactions: 0, TruncatedDescriptions: 0,
	}, view.Stats)
	assert.Equal(t, mes1, view.FromMonth)
	assert.Equal(t, mes3, view.ToMonth)
	assert.Equal(t, "2026-09-21T15:04:05Z", view.GeneratedAt)
}

// Casa recém-criada, sem conta e sem categoria: o prompt sai mesmo assim, e
// diz o que falta em vez de mostrar uma tabela vazia.
func TestCasaVaziaGeraPromptValido(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	view := a.exportar(t, minhaCasa, mes3, mes3)

	assert.Contains(t, view.Prompt, "Nenhuma conta ativa cadastrada.")
	assert.Contains(t, view.Prompt, "Nenhuma categoria ativa cadastrada.")
	assert.Contains(t, view.Prompt, "Nenhuma movimentação nesta janela de competência.")
	assert.Equal(t, aiprompt.StatsView{}, view.Stats)
}

// --- divergência de tipo ----------------------------------------------------

// A MESMA descrição vista com `kindGroup` DIFERENTES vira `vários` na coluna de
// tipo — a decisão do dev nesta fatia, e o único caminho da linha
// `tipo := tipoDivergente` de `publicar`.
//
// Os três casos são reais, não sintéticos: o mesmo estabelecimento
// categorizado como investimento num mês e ainda sem categoria noutro; o
// estorno que entra como receita com a descrição da despesa que o gerou; e a
// descrição que já virou perna de transferência num lançamento e não no seu
// gêmeo do mês seguinte. Nos três, `vários` é o texto honesto para "não é uma
// só" — e é informação que a IA precisa ter para não propor palavra-chave como
// se a descrição tivesse um tipo só.
func TestTipoDivergenteViraVarios(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome    string
		prefixo string
		linhas  []lancamento
	}{
		{
			"despesa sem categoria e despesa com categoria de investimento",
			"Aplicacao CDB",
			[]lancamento{
				{casa: minhaCasa, mes: mes1, desc: "Aplicacao CDB", kind: transaction.KindExpense, conta: contaCorrente, cents: 20_000},
				{casa: minhaCasa, mes: mes2, desc: "Aplicacao CDB", kind: transaction.KindExpense, conta: contaCorrente, categoria: ptr(catAportes), cents: 20_000},
			},
		},
		{
			"estorno: a mesma descrição como receita e como despesa",
			"Anuidade Cartao",
			[]lancamento{
				{casa: minhaCasa, mes: mes1, desc: "Anuidade Cartao", kind: transaction.KindExpense, conta: contaCartao, cents: 5_000},
				{casa: minhaCasa, mes: mes1, desc: "Anuidade Cartao", kind: transaction.KindIncome, conta: contaCartao, cents: 5_000},
			},
		},
		{
			"uma ocorrência já convertida em transferência e outra não",
			"Pix Enviado",
			[]lancamento{
				{casa: minhaCasa, mes: mes1, desc: "Pix Enviado", kind: transaction.KindTransferOut, conta: contaCorrente, cents: 7_000},
				{casa: minhaCasa, mes: mes2, desc: "Pix Enviado", kind: transaction.KindExpense, conta: contaCorrente, cents: 7_000},
			},
		},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			a := novoAmbiente(t)
			a.casaComum()
			a.ledger.linhas = c.linhas

			view := a.exportar(t, minhaCasa, mes1, mes3)

			linha := linhaDaTabela(t, view.Prompt, c.prefixo)
			assert.Contains(t, linha, "| vários |",
				"tipos divergentes não podem virar um rótulo escolhido no chute")
			assert.Contains(t, linha, "| 2 |", "a linha continua somando as duas ocorrências")
			assert.Equal(t, 1, view.Stats.Descriptions)
			assert.Equal(t, 2, view.Stats.Transactions)
		})
	}
}

// `kind` fora do conjunto fechado do domínio não vira rótulo inventado nem vaza
// cru para o texto: cai no mesmo `vários`, que é um texto honesto para "não
// sei".
//
// Só banco adulterado produz isso — mas o texto vai para uma IA de terceiro, e
// um rótulo desconhecido ali é instrução acidental.
func TestKindDesconhecidoNaoInventaRotuloNemVazaCru(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.linhas = []lancamento{
		{casa: minhaCasa, mes: mes1, desc: "Kind Estranho", kind: "reembolso_alienigena", conta: contaCorrente, cents: 100},
	}

	view := a.exportar(t, minhaCasa, mes1, mes3)

	assert.Contains(t, linhaDaTabela(t, view.Prompt, "Kind Estranho"), "| vários |")
	assert.NotContains(t, view.Prompt, "reembolso_alienigena", "o kind cru não chega ao texto")
}

// --- arquivadas: a linha fica, o nome some -----------------------------------

// Movimentação em conta ARQUIVADA mantém a LINHA — valor e ocorrências
// continuam somando —, e perde só o NOME da conta, que vira travessão.
//
// É o encontro do critério 6 (arquivada não aparece) com o 10 (o total do grupo
// bate com a soma dos lançamentos): o gasto aconteceu e o número tem de fechar;
// o que não pode aparecer é o nome do que foi arquivado. O preço — a linha
// perde a informação de conta — fica aqui escrito em teste, e não implícito.
func TestContaArquivadaPerdeONomeMasNaoALinha(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.linhas = []lancamento{
		// Só na conta arquivada: a coluna inteira vira travessão.
		{casa: minhaCasa, mes: mes1, desc: "So Na Arquivada", kind: transaction.KindExpense, conta: contaVelha, cents: 3_000},
		// Nas duas: a ativa é nomeada, a arquivada não — e o total soma as duas.
		{casa: minhaCasa, mes: mes1, desc: "Nas Duas Contas", kind: transaction.KindExpense, conta: contaVelha, cents: 1_000},
		{casa: minhaCasa, mes: mes2, desc: "Nas Duas Contas", kind: transaction.KindExpense, conta: contaCorrente, cents: 2_000},
	}

	view := a.exportar(t, minhaCasa, mes1, mes3)

	so := linhaDaTabela(t, view.Prompt, "So Na Arquivada")
	assert.Contains(t, so, "| 1 |")
	assert.Contains(t, so, "R$ 30,00", "o valor da conta arquivada continua somando")
	assert.Contains(t, so, "| — | — |", "sem conta nomeável e sem categoria")

	duas := linhaDaTabela(t, view.Prompt, "Nas Duas Contas")
	assert.Contains(t, duas, "| 2 |")
	assert.Contains(t, duas, "R$ 30,00", "1.000 + 2.000 centavos")
	assert.Contains(t, duas, "| Conta Corrente |", "só a conta ATIVA é nomeada")

	assert.NotContains(t, view.Prompt, "Poupança Antiga", "critério 6")
	assert.Equal(t, 3, view.Stats.Transactions, "a movimentação da arquivada continua contada")
}

// Categoria ARQUIVADA cai no mesmo balde de "sem categoria": sozinha vira
// travessão, e ao lado de uma categoria ativa vira `várias`.
//
// São dois estados diferentes na mesma descrição, e o texto não pode fingir que
// a descrição está toda em `Alimentação > Mercado` quando metade dela está numa
// categoria que a pessoa já tirou de circulação.
func TestCategoriaArquivadaContaComoDivergencia(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.linhas = []lancamento{
		{casa: minhaCasa, mes: mes1, desc: "So Categoria Velha", kind: transaction.KindExpense, conta: contaCorrente, categoria: ptr(catVelha), cents: 1_000},
		{casa: minhaCasa, mes: mes1, desc: "Velha E Ativa", kind: transaction.KindExpense, conta: contaCorrente, categoria: ptr(catMercado), cents: 1_000},
		{casa: minhaCasa, mes: mes2, desc: "Velha E Ativa", kind: transaction.KindExpense, conta: contaCorrente, categoria: ptr(catVelha), cents: 1_000},
	}

	view := a.exportar(t, minhaCasa, mes1, mes3)

	assert.Contains(t, linhaDaTabela(t, view.Prompt, "So Categoria Velha"), "| — |")
	assert.Contains(t, linhaDaTabela(t, view.Prompt, "Velha E Ativa"), "| várias |")
	assert.NotContains(t, view.Prompt, "Restaurante Antigo", "critério 6")
}

// --- BOLA: a camada que os dublês honestos escondem ---------------------------

// Os dublês deste arquivo filtram por casa como os repositórios reais — e por
// isso NÃO exercitam a reconferência que o serviço faz em Go, linha a linha
// (`c.HouseholdID != householdID`, em carregarTaxonomia e carregarContas).
// Sem este teste, apagar aquelas comparações deixaria a suíte inteira verde.
//
// Aqui as três fontes VAZAM de propósito: é a simulação de um repositório com o
// `WHERE household_id` quebrado. O contrato é que o texto continue limpo — nada
// da outra casa é NOMEADO — e que a anomalia vire UM aviso agregado no log, com
// a contagem e nada mais.
func TestReconferenciaDeCasaEmGoDescartaOQueVazou(t *testing.T) {
	t.Parallel()

	logs := &bytes.Buffer{}
	lg := logging.New(logs, logging.Options{Level: "debug", Format: "json"})
	svc := aiprompt.NewService(
		&ledgerVazado{linhas: []transaction.DescriptionGroup{{
			DescriptionNorm: "compra alheia", SampleDescription: "Compra Alheia",
			Kind: transaction.KindExpense, AccountID: "conta-alheia",
			CategoryID: ptr("cat-alheia"), Count: 4, TotalCents: 99_999,
		}}},
		&categoriasVazadas{
			cats: []category.Category{
				{ID: catMercado, HouseholdID: minhaCasa, Name: "Mercado", NameNorm: "mercado", Kind: category.KindExpense},
				{ID: "cat-alheia", HouseholdID: outraCasa, Name: "Categoria Alheia", NameNorm: "categoria alheia", Kind: category.KindExpense},
			},
			kws: []category.Keyword{
				{ID: "ck9", HouseholdID: outraCasa, CategoryID: "cat-alheia", Keyword: "palavra alheia", Norm: "palavra alheia"},
			},
		},
		&contasVazadas{
			accs: []account.Account{
				{ID: contaCorrente, HouseholdID: minhaCasa, Name: "Conta Corrente", NameNorm: "conta corrente", Kind: account.KindChecking},
				{ID: "conta-alheia", HouseholdID: outraCasa, Name: "Conta Da Casa Alheia", NameNorm: "conta da casa alheia", Kind: account.KindChecking},
			},
			kws: []account.Keyword{
				{ID: "k9", HouseholdID: outraCasa, AccountID: "conta-alheia", Keyword: "segredo alheio", Norm: "segredo alheio"},
			},
		},
		lg,
		aiprompt.WithClock(func() time.Time { return geradoEm }),
	)

	view, err := svc.ExportPrompt(t.Context(), ator(minhaCasa),
		aiprompt.ExportInput{FromMonth: mes1, ToMonth: mes3})
	require.NoError(t, err)

	for _, agulha := range []string{
		"Conta Da Casa Alheia", "Categoria Alheia", "palavra alheia", "segredo alheio",
		"conta-alheia", "cat-alheia", outraCasa,
	} {
		assert.NotContainsf(t, view.Prompt, agulha,
			"o que vazou do repositório não pode ser NOMEADO no texto: %s", agulha)
	}
	assert.Equal(t, 1, view.Stats.Accounts, "só a conta da casa do token foi listada")
	assert.Equal(t, 1, view.Stats.Categories)

	// A LINHA agregada sobrevive (o serviço não tem como saber de que casa ela
	// é — quem sabe é o `WHERE`), mas sai sem nomear conta nem categoria.
	assert.Contains(t, linhaDaTabela(t, view.Prompt, "Compra Alheia"), "| — | — |")

	// UM aviso agregado, com a contagem e nada mais.
	assert.Contains(t, logs.String(), "linhas agregadas apontam para categoria fora da taxonomia da casa")
	assert.NotContains(t, logs.String(), "Compra Alheia", "o log não leva descrição")
	assert.NotContains(t, logs.String(), "99999", "nem centavos")
}

// --- dublês que VAZAM (só para o teste acima) --------------------------------

type ledgerVazado struct {
	linhas []transaction.DescriptionGroup
}

func (l *ledgerVazado) GroupByDescription(_ context.Context, _ string, _ []string, _ int) ([]transaction.DescriptionGroup, error) {
	return l.linhas, nil
}

type categoriasVazadas struct {
	cats []category.Category
	kws  []category.Keyword
}

func (c *categoriasVazadas) List(_ context.Context, _ string, _ bool) ([]category.Category, error) {
	return c.cats, nil
}

func (c *categoriasVazadas) ListKeywords(_ context.Context, _ string) ([]category.Keyword, error) {
	return c.kws, nil
}

type contasVazadas struct {
	accs []account.Account
	kws  []account.Keyword
}

func (c *contasVazadas) List(_ context.Context, _ string, _ bool) ([]account.Account, error) {
	return c.accs, nil
}

func (c *contasVazadas) ListKeywords(_ context.Context, _ string) ([]account.Keyword, error) {
	return c.kws, nil
}
