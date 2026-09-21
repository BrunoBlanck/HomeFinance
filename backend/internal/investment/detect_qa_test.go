package investment_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// QA adversarial da E7 sobre `overwriteCategorized` — a primeira escrita do
// projeto autorizada a substituir escolha humana, e sem desfazer.
//
// O dev já fechou o caminho do id com natureza ambígua e documentou a corrida
// entre o plano e o UPDATE. O que estes testes atacam é o terceiro eixo, o do
// CICLO DE VIDA da categoria: arquivar e sumir.
//
// Ele importa porque `carregarTaxonomia` lê com `includeArchived = true` — de
// propósito, e por duas razões OPOSTAS ao mesmo tempo. A allowlist precisa
// alcançar a categoria de despesa ARQUIVADA que a linha tem hoje (senão a flag
// não serviria para nada em casa com histórico), e o conjunto de marcadas
// precisa incluir a de investimento arquivada (senão arquivar desfaria a
// marcação do passado, contra o PLANOS.md §4.4). As duas metades saem da MESMA
// leitura, e uma inversão de sinal nela trocaria as duas de lugar sem que
// nenhum teste de caminho feliz percebesse.

// A marcação de investimento numa categoria ARQUIVADA continua protegida: a
// flag não a desfaz, porque a categoria arquivada continua sendo de
// investimento e, portanto, fora da allowlist do WHERE.
//
// É o caso realista de quem arquivou "Tesouro" ao trocar de corretora: os
// aportes do ano passado continuam apontando para ela, e um `detect` com a
// caixa marcada não pode reescrevê-los.
func TestMarcacaoEmCategoriaDeInvestimentoARQUIVADANaoEhDesfeita(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.categorias.juntar(casaA, "cat-tesouro", "Tesouro", category.KindInvestment, "tesouro")
	c.categorias.arquivar(casaA, "cat-tesouro")

	// A linha marcada na categoria arquivada, com descrição que bateria na
	// categoria de investimento ATIVA.
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-tesouro"))
	// Uma linha trocável de verdade, para o UPDATE ser emitido e a allowlist
	// viajar até o repositório — sem ela, "não trocou" não distinguiria
	// "recusou" de "não teve o que fazer".
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindExpense, "CDB 30 DIAS", 100000, 2, "2026-09", "cat-mercado"))

	previa := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})
	for _, it := range previa.AlreadyCategorizedItems {
		assert.NotEqual(t, uuidDe(1), it.ID,
			"a prévia ofereceu trocar uma marcação de investimento arquivada — prometer o que o WHERE recusa")
	}
	assert.EqualValues(t, 1, previa.AlreadyCategorized, "só a linha de categoria COMUM é ofertável")

	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})

	require.Equal(t, 1, c.ledger.chamadasSetCurrent, "o UPDATE precisa ter sido emitido para a allowlist ser observável")
	assert.NotContains(t, c.ledger.ultimaAllowlist, "cat-tesouro",
		"categoria de investimento ARQUIVADA chegou à allowlist: arquivar passaria a desfazer a marcação")
	assert.EqualValues(t, 1, real.Marked)
	assert.Equal(t, "cat-tesouro", *c.ledger.por(uuidDe(1)).CategoryID, "a marcação arquivada foi desfeita")
	assert.Equal(t, "cat-cdb", *c.ledger.por(uuidDe(2)).CategoryID)
}

// A outra metade da mesma leitura, e ela precisa continuar valendo: a
// categoria COMUM arquivada É alcançável pela allowlist.
//
// Sem isto a flag seria inútil justamente para quem mais precisa dela — quem
// tem um ano de lançamentos numa categoria de despesa que já aposentou. O
// teste existe porque a afirmação está escrita no `types.go` e nunca havia sido
// conferida.
func TestCategoriaComumARQUIVADAContinuaTrocavel(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.categorias.arquivar(casaA, "cat-mercado")
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))

	previa := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})
	require.Len(t, previa.AlreadyCategorizedItems, 1)
	assert.Equal(t, "cat-mercado", previa.AlreadyCategorizedItems[0].CurrentCategoryID)
	assert.Equal(t, "Mercado", previa.AlreadyCategorizedItems[0].CurrentCategoryName,
		"o nome da categoria arquivada continua sendo exibível — a leitura é com arquivadas")

	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})
	assert.EqualValues(t, 1, real.Marked)
	assert.Contains(t, c.ledger.ultimaAllowlist, "cat-mercado")
	assert.Equal(t, "cat-cdb", *c.ledger.por(uuidDe(1)).CategoryID)
}

// Linha apontando para uma categoria que a casa NÃO tem — a que sumiu da
// taxonomia entre duas leituras, ou o dado que chegou torto.
//
// Ela não é oferecida na prévia e não é escrita: o `WHERE` recusaria (o id não
// está na allowlist), e a regra do projeto é nunca prometer o que a escrita não
// cumpre. O ponto adversarial é que ela também não pode ser tratada como "sem
// categoria" e cair no `SetCategoryWhereNull` — ali o `category_id IS NULL` do
// WHERE a recusaria do mesmo jeito, e a prévia teria mentido na direção oposta.
func TestLinhaComCategoriaForaDaTaxonomiaNaoEhOfertadaNemEscrita(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-fantasma"))

	previa := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})
	assert.Zero(t, previa.Marked, "linha com categoria não pode ser contada como marcável")
	assert.Zero(t, previa.Unmatched, "e também não é uma linha SEM categoria")
	assert.Zero(t, previa.AlreadyCategorized)
	assert.Empty(t, previa.AlreadyCategorizedItems)

	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})
	assert.Zero(t, real.Marked)
	assert.Zero(t, c.ledger.chamadasSetNull, "não é 'sem categoria': o UPDATE do NULL não pode ser emitido")
	assert.Zero(t, c.ledger.chamadasSetCurrent, "não é trocável: o UPDATE da troca não pode ser emitido")
	assert.Equal(t, "cat-fantasma", *c.ledger.por(uuidDe(1)).CategoryID)
}

// Com a flag ligada e DOIS destinos, cada UPDATE leva a MESMA allowlist — a do
// plano, e não uma releitura por destino.
//
// Uma releitura por destino abriria uma janela a mais por comando: a segunda
// allowlist poderia já refletir uma troca de natureza feita no meio, e dois
// comandos da mesma execução passariam a obedecer a regras diferentes. A prévia
// prometeu UMA coisa; a escrita tem de fazer exatamente aquela.
func TestOsDoisUpdatesDaMesmaExecucaoLevamAAllowlistDoPlano(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	// Duas linhas comuns, com destinos DIFERENTES (aporte e resgate).
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindIncome, "RESGATE DO CDB", 85000, 2, "2026-09", "cat-salario"))

	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})

	assert.EqualValues(t, 2, real.Marked)
	assert.Equal(t, 2, c.ledger.chamadasSetCurrent, "um UPDATE por categoria de destino")
	assert.Equal(t, []string{"cat-mercado", "cat-salario"}, c.ledger.ultimaAllowlist,
		"a allowlist do segundo comando é a MESMA do plano")

	assert.Equal(t, "cat-cdb", *c.ledger.por(uuidDe(1)).CategoryID)
	assert.Equal(t, "cat-resgate", *c.ledger.por(uuidDe(2)).CategoryID, "receita vira RESGATE, nunca aporte")
}

// Falha FECHADA no `detect`: taxonomia estourada é 500 GENÉRICO, nunca 4xx e
// nunca clamp.
//
// O teste que já existia (`TestTetoDeCategoriasEh500Generico`) cobre só o
// `GET /investments`. Esta rota chega ao mesmo erro por outro caminho — a
// ALLOWLIST do `overwriteCategorized`, que é o segundo `IN (...)` do comando —
// e é o caminho que ESCREVE. Um 422 aqui apontaria um campo que a pessoa não
// tem como corrigir naquele pedido (ADR-029 j.2), e um clamp silencioso
// mandaria ao banco uma allowlist recortada: parte das categorias comuns da
// casa ficaria de fora do `WHERE` sem ninguém pedir, e a troca aconteceria
// para umas linhas e não para outras.
//
// A rota emite o comando e deixa o REPOSITÓRIO recusar, em vez de conferir o
// tamanho no serviço: é lá que o teto existe (ele protege o número de
// parâmetros por comando, que é assunto de persistência), e conferir nos dois
// lugares faria a regra ter duas donas.
func TestDetectComTaxonomiaEstouradaEh500GenericoENaoAudita(t *testing.T) {
	t.Parallel()

	h := montarHTTP(t)
	h.contas.juntar(casaA, "acc-1", "Nubank")
	h.categorias.juntar(casaA, "cat-cdb", "Renda fixa", category.KindInvestment, "cdb")
	// Uma categoria comum a mais do que o teto da taxonomia da casa: é o
	// estado do ADR-027(f), "taxonomia estourada".
	for i := range maxIDsNoFiltro + 1 {
		h.categorias.juntar(casaA, "cat-comum-"+uuidDe(i), "Comum", category.KindExpense)
	}
	h.ledger.juntar(lanc(casaA, uuidDe(900), transaction.KindExpense, "CDB 15 DIAS", 200000, 5, "2026-09", "cat-comum-"+uuidDe(1)))

	rec := h.post(t, casaA, `{"month":"2026-09","dryRun":false,"overwriteCategorized":true}`)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	codigo, campos := erroDoCorpo(t, rec)
	assert.Equal(t, "INTERNAL_ERROR", codigo, "o nome do erro convida a um 422; ele não é um")
	assert.Empty(t, campos, "não há campo que a pessoa possa corrigir neste pedido")
	assert.NotContains(t, rec.Body.String(), "categorias", "a mensagem ao cliente é genérica")
	assert.NotContains(t, rec.Body.String(), "cat-cdb")

	// O comando FOI emitido: a recusa é do repositório, não um desvio em Go.
	assert.Equal(t, 1, h.ledger.chamadasSetCurrent)
	// E o rastro não vale: a auditoria mora DENTRO da transação, depois dos
	// UPDATE — em produção o rollback desfaz os dois juntos.
	assert.Empty(t, h.auditoria.entradas, "execução que falhou não audita")
	assert.Contains(t, h.logs.String(), "falha em investimentos", "o detalhe vai só para o log")
	assert.NotContains(t, h.logs.String(), "CDB 15 DIAS", "descrição nunca entra em log (S8)")
}
