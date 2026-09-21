package investment_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Critério 6 da §7 da spec 0006, na forma que importa: o resultado vazio é
// fácil, e um teste que só o olhasse passaria mesmo se a consulta tivesse ido
// ao banco com `IN ()` — que é justamente o defeito. O que se afirma aqui é a
// CONTAGEM DE CHAMADAS.
func TestCasaSemCategoriaDeInvestimentoNaoConsultaLancamentoNenhum(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-mercado", "Mercado", category.KindExpense, "mercado")
	// Lançamento existe e não pode aparecer: não há categoria marcada.
	c.ledger.juntar(lanc(casaA, "tx-1", transaction.KindExpense, "CDB 15 DIAS", 200000, 5, "2026-09", "cat-mercado"))

	view, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Zero(t, c.ledger.chamadasSum, "somou investimentos sem ter categoria marcada — `IN ()` iria ao banco")
	assert.Zero(t, c.ledger.chamadasList, "listou lançamentos sem ter categoria marcada")

	assert.Equal(t, "2026-09", view.Month)
	assert.Equal(t, investment.TotalsView{}, view.Monthly)
	assert.Equal(t, investment.TotalsView{}, view.YearToDate)
	assert.Empty(t, view.Items)
	assert.NotNil(t, view.Items, "items tem de ser [] e nunca null")
	assert.Nil(t, view.NextCursor)

	// A série é promessa de contrato, não consequência de ter havido
	// movimento: 12 itens, crescente, terminando no mês pedido.
	require.Len(t, view.Series, 12)
	assert.Equal(t, "2025-10", view.Series[0].Month)
	assert.Equal(t, "2026-09", view.Series[11].Month)
	for _, p := range view.Series {
		assert.Zero(t, p.ContributionsCents)
		assert.Zero(t, p.RedemptionsCents)
	}
}

// UMA consulta resolve os três números: a janela do ano está contida na dos 12
// meses. O teste afere a contagem, os argumentos e os três resultados.
func TestOsTresNumerosSaemDeUmaConsultaSo(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-inv", "Renda fixa", category.KindInvestment)
	c.categorias.juntar(casaA, "cat-res", "Resgates", category.KindRedemption)
	c.contas.juntar(casaA, "acc-1", "Nubank")

	// Dezembro do ano anterior: entra na série, FICA DE FORA do ano-até-o-mês.
	c.ledger.juntar(lanc(casaA, "tx-dez", transaction.KindExpense, "CDB", 50000, 10, "2025-12", "cat-inv"))
	// Março: entra na série e no ano.
	c.ledger.juntar(lanc(casaA, "tx-mar", transaction.KindExpense, "CDB", 100000, 10, "2026-03", "cat-inv"))
	// Setembro: entra nos três.
	c.ledger.juntar(lanc(casaA, "tx-set-a", transaction.KindExpense, "CDB", 200000, 5, "2026-09", "cat-inv"))
	c.ledger.juntar(lanc(casaA, "tx-set-b", transaction.KindIncome, "RESGATE", 85000, 6, "2026-09", "cat-res"))

	view, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Equal(t, 1, c.ledger.chamadasSum, "a agregação tem de ser UMA consulta — nem 12, nem 2")
	assert.Equal(t, "2025-10", c.ledger.ultimoFrom)
	assert.Equal(t, "2026-09", c.ledger.ultimoTo)
	assert.Equal(t, []string{"cat-inv", "cat-res"}, c.ledger.ultimasCategorias)

	assert.Equal(t, investment.TotalsView{
		ContributionsCents: 200000, ContributionCount: 1,
		RedemptionsCents: 85000, RedemptionCount: 1,
	}, view.Monthly)

	// Ano civil da casa: 2026-01..2026-09. Dezembro/2025 está na série e NÃO
	// no ano — é exatamente a diferença entre as duas perguntas.
	assert.Equal(t, investment.TotalsView{
		ContributionsCents: 300000, ContributionCount: 2,
		RedemptionsCents: 85000, RedemptionCount: 1,
	}, view.YearToDate)

	porMes := map[string]investment.SeriesPointView{}
	for _, p := range view.Series {
		porMes[p.Month] = p
	}
	assert.Equal(t, int64(50000), porMes["2025-12"].ContributionsCents)
	assert.Equal(t, int64(100000), porMes["2026-03"].ContributionsCents)
	assert.Equal(t, int64(200000), porMes["2026-09"].ContributionsCents)
	assert.Equal(t, int64(85000), porMes["2026-09"].RedemptionsCents)
	assert.Zero(t, porMes["2026-05"].ContributionsCents, "mês sem movimento vem zerado, nunca ausente")
}

// O FLUXO vem do kind do LANÇAMENTO, nunca da natureza da categoria
// (ADR-029d). O caso anômalo — despesa numa categoria de RESGATE — prova a
// regra: ela continua sendo `contribution`.
func TestFluxoVemDoKindDoLancamentoENaoDaNaturezaDaCategoria(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-res", "Resgates", category.KindRedemption)
	c.contas.juntar(casaA, "acc-1", "Nubank")
	c.ledger.juntar(lanc(casaA, "tx-1", transaction.KindExpense, "APORTE ERRADO", 10000, 5, "2026-09", "cat-res"))

	view, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)

	require.Len(t, view.Items, 1)
	assert.Equal(t, investment.FlowContribution, view.Items[0].Flow,
		"despesa é aporte mesmo numa categoria de resgate: o fluxo é do lançamento")
	assert.Equal(t, int64(10000), view.Monthly.ContributionsCents)
	assert.Zero(t, view.Monthly.RedemptionsCents)
}

// Categoria ARQUIVADA continua marcando o passado: arquivar não desfaz nada
// (PLANOS.md §4.4).
func TestCategoriaArquivadaContinuaContando(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-inv", "Tesouro antigo", category.KindInvestment)
	c.categorias.arquivar(casaA, "cat-inv")
	c.contas.juntar(casaA, "acc-1", "Nubank")
	c.ledger.juntar(lanc(casaA, "tx-1", transaction.KindExpense, "TESOURO", 70000, 5, "2026-09", "cat-inv"))

	view, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Contains(t, c.ledger.ultimasCategorias, "cat-inv")
	assert.Equal(t, int64(70000), view.Monthly.ContributionsCents)
	require.Len(t, view.Items, 1)
	assert.Equal(t, "Tesouro antigo", view.Items[0].CategoryName)
}

// Duas casas, o MESMO mês e ids de categoria iguais no espírito: nenhuma
// enxerga a outra, e a prova é o household_id que chegou ao repositório — não
// só o resultado.
func TestOverviewNaoAtravessaCasas(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-a", "Renda fixa", category.KindInvestment)
	c.categorias.juntar(casaB, "cat-b", "Renda fixa", category.KindInvestment)
	c.contas.juntar(casaA, "acc-1", "Nubank")
	c.contas.juntar(casaB, "acc-1", "Itaú")
	c.ledger.juntar(lanc(casaA, "tx-a", transaction.KindExpense, "CDB", 100000, 5, "2026-09", "cat-a"))
	c.ledger.juntar(lanc(casaB, "tx-b", transaction.KindExpense, "CDB", 999999, 5, "2026-09", "cat-b"))

	view, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Equal(t, casaA, c.ledger.ultimaCasaSum)
	assert.Equal(t, casaA, c.ledger.ultimaCasaList)
	assert.Equal(t, []string{"cat-a"}, c.ledger.ultimasCategorias,
		"o filtro de categorias levou id de outra casa")
	assert.Equal(t, int64(100000), view.Monthly.ContributionsCents)
	require.Len(t, view.Items, 1)
	assert.Equal(t, "tx-a", view.Items[0].ID)
}

// Ator sem casa nunca chega ao banco.
func TestOverviewSemCasaNaoConsultaNada(t *testing.T) {
	t.Parallel()

	c := montar()
	_, err := c.svc.Overview(context.Background(), investment.Actor{}, investment.OverviewInput{Month: "2026-09"})
	require.ErrorIs(t, err, investment.ErrUnauthenticated)
	assert.Zero(t, c.categorias.chamadas)
	assert.Zero(t, c.ledger.chamadasSum)
	assert.Zero(t, c.ledger.chamadasList)
}

func TestMesEhValidadoAntesDeQualquerConsulta(t *testing.T) {
	t.Parallel()

	for _, mes := range []string{"", "2026-13", "2026-1", "2026-01-01", " 2026-01", "2026/01"} {
		t.Run(mes, func(t *testing.T) {
			t.Parallel()
			c := montar()
			_, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: mes})
			require.ErrorIs(t, err, transaction.ErrInvalidMonth)
			assert.Zero(t, c.categorias.chamadas)
			assert.Zero(t, c.ledger.chamadasSum)
		})
	}
}

func TestCursorAdulteradoEhRecusadoAntesDoBanco(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-inv", "Renda fixa", category.KindInvestment)

	_, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{
		Month:  "2026-09",
		Cursor: "nao-e-um-cursor",
	})
	require.ErrorIs(t, err, transaction.ErrInvalidCursor)
	assert.Zero(t, c.ledger.chamadasSum)
	assert.Zero(t, c.ledger.chamadasList)
}

// A página pede limite+1 e devolve o cursor da última linha mantida — o MESMO
// formato de GET /transactions.
func TestPaginacaoUsaOCursorDeTransactions(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-inv", "Renda fixa", category.KindInvestment)
	c.contas.juntar(casaA, "acc-1", "Nubank")
	for _, dia := range []int{1, 2, 3, 4, 5} {
		c.ledger.juntar(lanc(casaA, uuidDe(dia), transaction.KindExpense, "CDB", 1000, dia, "2026-09", "cat-inv"))
	}

	primeira, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-09", Limit: 2})
	require.NoError(t, err)
	require.Len(t, primeira.Items, 2)
	require.NotNil(t, primeira.NextCursor)

	// A ordem é decrescente: dias 5 e 4 na primeira página.
	assert.Equal(t, 5, primeira.Items[0].OccurredOn.Day())
	assert.Equal(t, 4, primeira.Items[1].OccurredOn.Day())

	cursor, err := transaction.ParseCursor(*primeira.NextCursor)
	require.NoError(t, err)
	assert.Equal(t, 4, cursor.OccurredOn.Day())

	segunda, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{
		Month: "2026-09", Limit: 2, Cursor: *primeira.NextCursor,
	})
	require.NoError(t, err)
	require.Len(t, segunda.Items, 2)
	assert.Equal(t, 3, segunda.Items[0].OccurredOn.Day())

	ultima, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{
		Month: "2026-09", Limit: 2, Cursor: *segunda.NextCursor,
	})
	require.NoError(t, err)
	require.Len(t, ultima.Items, 1)
	assert.Nil(t, ultima.NextCursor, "última página não devolve cursor")
}

// ErrTooManyCategories do repositório NÃO vira 4xx: ele conta categorias da
// própria casa, que já têm teto próprio (ADR-029 j.2). O serviço o deixa subir
// embrulhado, e é o handler que o transforma em 500 — aqui basta afirmar que
// ele não vira nenhum dos erros de entrada.
func TestTetoDeCategoriasSobeComoFalhaInterna(t *testing.T) {
	t.Parallel()

	c := montar()
	for i := range maxIDsNoFiltro + 1 {
		c.categorias.juntar(casaA, "cat-"+string(rune('A'+i%26))+string(rune('a'+i/26)), "Inv", category.KindInvestment)
	}

	_, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-09"})
	require.Error(t, err)
	assert.ErrorIs(t, err, transaction.ErrTooManyCategories)
	assert.NotErrorIs(t, err, transaction.ErrInvalidMonth)
	assert.NotErrorIs(t, err, investment.ErrTooManyCandidates)
}

// ADR-029 j.1: a desigualdade é VERIFICADA, não confiada. Total negativo vindo
// do banco falha FECHADO — nenhum número inventado na tela.
func TestTotalNegativoDoBancoFalhaFechado(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-inv", "Renda fixa", category.KindInvestment)
	c.ledger.linhasDoSumOv = []transaction.InvestmentMonthTotals{
		{Month: "2026-09", ContributionsCents: -1, ContributionCount: 1},
	}

	_, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-09"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, transaction.ErrInvalidMonth, "não é erro de entrada: é banco em estado impossível")
}

func TestFalhaDoBancoNaoViraRespostaParcial(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-inv", "Renda fixa", category.KindInvestment)
	c.ledger.erroSum = errFalhaDoBanco

	view, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-09"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, errFalhaDoBanco))
	assert.Equal(t, investment.OverviewView{}, view, "nenhum número parcial sai daqui")
}

// A taxonomia é lida com arquivadas INCLUÍDAS: é isso que faz a marcação do
// passado sobreviver ao arquivamento e a allowlist alcançar a categoria de
// despesa arquivada que a linha tem hoje.
func TestTaxonomiaEhLidaComArquivadas(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-inv", "Renda fixa", category.KindInvestment)

	_, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.True(t, c.categorias.ultimaSet, "a taxonomia foi lida sem as arquivadas")
}

// Janeiro é a fronteira que a aritmética de mês erra: a série recua para o ano
// anterior e o ano-até-o-mês tem UM mês só. Os dois números são diferentes
// aqui, e é isso que prova que não são o mesmo cálculo com outro nome.
func TestJaneiroSeparaAJanelaDoAnoDaJanelaDaSerie(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-inv", "Renda fixa", category.KindInvestment)
	c.contas.juntar(casaA, "acc-1", "Nubank")
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB", 30000, 10, "2025-11", "cat-inv"))
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindExpense, "CDB", 70000, 10, "2026-01", "cat-inv"))

	view, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-01"})
	require.NoError(t, err)

	assert.Equal(t, "2025-02", c.ledger.ultimoFrom)
	assert.Equal(t, "2026-01", c.ledger.ultimoTo)
	require.Len(t, view.Series, 12)
	assert.Equal(t, "2025-02", view.Series[0].Month)
	assert.Equal(t, "2026-01", view.Series[11].Month)

	assert.Equal(t, int64(70000), view.Monthly.ContributionsCents)
	assert.Equal(t, int64(70000), view.YearToDate.ContributionsCents,
		"o ano-até-o-mês de janeiro é só janeiro: novembro de 2025 está na série e não no ano")
	assert.Equal(t, int64(30000), view.Series[9].ContributionsCents, "2025-11 é o décimo ponto da série")
}

// Duas idas ao banco de lançamentos no caminho normal, e não mais: a agregação
// e a página. A contagem é o teste — "parece rápido" não é.
func TestOverviewFazDuasIdasAoBancoDeLancamentos(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-inv", "Renda fixa", category.KindInvestment)
	c.contas.juntar(casaA, "acc-1", "Nubank")
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB", 1000, 1, "2026-09", "cat-inv"))

	_, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Equal(t, 1, c.ledger.chamadasSum)
	assert.Equal(t, 1, c.ledger.chamadasList)
	assert.Equal(t, 1, c.categorias.chamadas, "a taxonomia é lida UMA vez")
	assert.Equal(t, 1, c.contas.chamadas, "os nomes de conta saem de UMA leitura, não de uma por linha")
}

// Mês sem lançamento marcado (mas com categoria cadastrada) consulta o banco e
// devolve zeros com a lista vazia — sem carregar nomes de conta à toa.
func TestMesSemMovimentoNaoCarregaNomesDeConta(t *testing.T) {
	t.Parallel()

	c := montar()
	c.categorias.juntar(casaA, "cat-inv", "Renda fixa", category.KindInvestment)
	c.contas.juntar(casaA, "acc-1", "Nubank")

	view, err := c.svc.Overview(context.Background(), ator(casaA), investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Equal(t, 1, c.ledger.chamadasSum)
	assert.Equal(t, 1, c.ledger.chamadasList)
	assert.Zero(t, c.contas.chamadas)
	assert.Empty(t, view.Items)
	assert.NotNil(t, view.Items)
}
