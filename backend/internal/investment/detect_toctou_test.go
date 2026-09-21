package investment_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// A janela TOCTOU entre o plano e a escrita do `detect`.
//
// O plano é calculado FORA da transação — decisão deliberada do achado A2 da
// entrega anterior, para não segurar conexão do pool durante o cálculo. O preço
// é que entre o plano e o UPDATE cabe uma requisição inteira, e o `WHERE` dos
// dois comandos confere a casa, a linha viva, o `kind` e a categoria ATUAL —
// mas nunca a categoria de DESTINO, que é valor do SET.
//
// A corrida é encenada com o gancho `antes` do txFake, que roda DENTRO da
// transação e imediatamente antes da função de escrita: é exatamente a janela
// real, e não sorte de escalonamento.

// ---------------------------------------------------------------------------
// Os QUATRO eixos da reconferência, e quem derruba cada mutante
// ---------------------------------------------------------------------------
//
// `reconferirDestinos` faz quatro perguntas em `if`s SEPARADOS (achado N2: a
// forma fundida `!vivo || st.HasActiveChild` deixava um eixo sustentado por
// acidente de outro). Verificação por mutação desta rodada, apagando um `if` de
// cada vez:
//
//	EIXO 2 ARQUIVADA  → TestDestinoArquivadoEntreOPlanoEAEscritaEhConflito
//	EIXO 3 FILHA ATIVA → TestDestinoQueVirouGrupoComSubcategoriaAtivaTambemEhConflito
//	EIXO 4 NATUREZA   → TestDestinoQueVirouCategoriaComumNaJanelaNaoEhGravado
//	                    (+ 3 outros, em detect_natureza_test.go)
//	EIXO 1 VIVA/CASA  → NENHUM teste. Ver abaixo.
//
// O EIXO 1 não é lacuna a preencher, e sim um eixo guardado por outra coisa: em
// Go o `ok` falso de um mapa vem sempre com o ZERO VALUE, então a excluída (ou
// a de outra casa, que o repositório sequer devolve) chega como
// `category.LiveState{}` de `Kind` vazio e o EIXO 4 já a reprova. Um teste que
// apresentasse um estado POPULADO com `vivo` falso estaria encenando um mapa
// que a linguagem não produz — é justamente o que o `destino_test.go` do pacote
// `category` consegue fazer com o predicado compartilhado, porque lá
// `encontrada` é PARÂMETRO.
//
// Quem guarda o EIXO 1 é o COMPILADOR: apagar aquele `if` deixa `vivo`
// declarado e não usado e o pacote não compila. A única forma de o mutante
// compilar é DESCARTAR `vivo` num identificador em branco — que é exatamente o
// padrão caçado por grep na revisão de segurança, e exatamente o mutante vivo
// que o revisor encontrou na árvore. Guarda que não se pula é mais forte do que
// teste; o que ela exige é que o grep continue existindo.
//
// A cobertura dos dois eixos que aquele `if` guarda é do REPOSITÓRIO:
// gormstore.TestLiveStatesNaoEnxergaOutraCasa (a CASA) e
// gormstore.TestLiveStatesDeixaDeForaAExcluida (a EXCLUSÃO). Quem mexer no
// LiveStates não pode presumir que algum teste daqui de baixo perceberia.

// excluir marca a exclusão LÓGICA da categoria, como faz DELETE /categories/{id}.
func (c *categoriasFake) excluir(householdID, id string) {
	quando := time.Date(2026, 9, 18, 11, 0, 0, 0, time.UTC)
	for i := range c.porCasa[householdID] {
		if c.porCasa[householdID][i].ID == id {
			c.porCasa[householdID][i].DeletedAt = &quando
		}
	}
}

// juntarFilha pendura uma subcategoria ATIVA num grupo existente. É o que torna
// o grupo inelegível para receber lançamento (spec 0005 §12/§13).
func (c *categoriasFake) juntarFilha(householdID, paiID, id, nome, kind string) {
	pai := paiID
	c.porCasa[householdID] = append(c.porCasa[householdID], category.Category{
		ID:          id,
		HouseholdID: householdID,
		ParentID:    &pai,
		Name:        nome,
		NameNorm:    textnorm.Normalize(nome),
		Kind:        kind,
	})
}

// O caso provado pela revisão: entre o plano e o UPDATE, outro morador exclui a
// categoria de destino. A checagem `inUse` da exclusão não encontra nada
// (NADA foi gravado ainda), a categoria some — e sem a reconferência o UPDATE
// penduraria os lançamentos numa categoria EXCLUÍDA.
//
// A decisão desta rota é explícita: a operação INTEIRA falha. Nada é gravado,
// nada é auditado, e a resposta é 409 CONFLICT.
func TestDestinoExcluidoEntreOPlanoEAEscritaFalhaAOperacaoInteira(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindExpense, "CDB 30 DIAS", 100000, 2, "2026-09", ""))

	c.tx.antes = func() {
		// DELETE /categories/cat-cdb passa: nenhum lançamento aponta para ela
		// ainda, porque o UPDATE desta execução ainda não rodou.
		c.categorias.excluir(casaA, "cat-cdb")
	}

	_, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, investment.ErrDestinationChanged)

	assert.Equal(t, 1, c.tx.chamadas, "a transação foi aberta")
	assert.Equal(t, 1, c.categorias.chamadasAtribuiveis, "a reconferência é UMA consulta, não uma por id")
	assert.Zero(t, c.ledger.chamadasSetNull, "nenhum UPDATE pode ter sido emitido")
	assert.Zero(t, c.ledger.chamadasSetCurrent)
	assert.Empty(t, c.auditoria.entradas, "rastro de escrita que não aconteceu seria mentira")

	assert.Nil(t, c.ledger.por(uuidDe(1)).CategoryID, "o lançamento continua sem categoria")
	assert.Nil(t, c.ledger.por(uuidDe(2)).CategoryID)
}

// A mesma corrida do lado do overwriteCategorized: o destino da TROCA também é
// reconferido, e a linha que já tinha categoria comum continua com ela.
func TestDestinoExcluidoTambemBarraATrocaDeCategoria(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))

	c.tx.antes = func() { c.categorias.excluir(casaA, "cat-cdb") }

	_, err := c.svc.Detect(context.Background(), ator(casaA),
		investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})
	require.ErrorIs(t, err, investment.ErrDestinationChanged)

	assert.Zero(t, c.ledger.chamadasSetCurrent)
	assert.Equal(t, "cat-mercado", *c.ledger.por(uuidDe(1)).CategoryID, "a categoria que a pessoa tinha escolhido ficou intacta")
	assert.Empty(t, c.auditoria.entradas)
}

// DESTINO arquivado na janela é CONFLITO — o quinto qualificador, fechado.
//
// ⚠️ A expectativa deste teste foi INVERTIDA, e a inversão é o conserto.
//
// A versão anterior afirmava que arquivar o destino no meio da janela era
// benigno e a escrita seguia. A afirmação misturava duas coisas que o produto
// trata de formas OPOSTAS, e a distinção é sutil o bastante para alguém querer
// reabri-la:
//
//   - MARCAÇÃO EXISTENTE sobrevive ao arquivamento. Categoria arquivada
//     continua contando e o lançamento que já aponta para ela continua
//     apontando (PLANOS.md §4.4). Isso não mudou e continua verdadeiro;
//   - ATRIBUIÇÃO NOVA a categoria arquivada é RECUSADA em todas as outras
//     portas: transaction.ErrCategoryArchived barra no PATCH de uma linha
//     (service.go:552), no lote (service.go:733) e na importação.
//
// O `detect` atribui. Sem esta recusa ele seria a única porta do produto a
// gravar onde as outras três recusam — a mesma forma do defeito que travou a
// outra frente.
//
// O custo é declarado: quem arquivar exatamente durante a janela leva 409. É
// raro, não perde dado, e a ação da tela é pedir a prévia de novo.
func TestDestinoArquivadoEntreOPlanoEAEscritaEhConflito(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	c.tx.antes = func() { c.categorias.arquivar(casaA, "cat-cdb") }

	_, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, investment.ErrDestinationChanged)

	assert.Zero(t, c.ledger.chamadasSetNull, "nenhum UPDATE pode ter sido emitido")
	assert.Nil(t, c.ledger.por(uuidDe(1)).CategoryID)
	assert.Empty(t, c.auditoria.entradas)
}

// A OUTRA metade da distinção, e é ela que impede a correção de virar exagero:
// a categoria de ORIGEM arquivada CONTINUA na allowlist da troca.
//
// A allowlist descreve a categoria que a linha JÁ TEM. A linha presa numa
// categoria de despesa que a casa aposentou é justamente um dos casos que o
// `overwriteCategorized` existe para resolver — tirá-la daqui deixaria esse
// caso sem conserto.
func TestOrigemARQUIVADAContinuaTrocavel(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))

	// "Mercado" foi aposentada pela casa; a linha continua pendurada nela.
	c.categorias.arquivar(casaA, "cat-mercado")

	view, err := c.svc.Detect(context.Background(), ator(casaA),
		investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})
	require.NoError(t, err, "arquivar a ORIGEM não impede a troca: é marcação existente, não atribuição nova")

	assert.Contains(t, c.ledger.ultimaAllowlist, "cat-mercado")
	assert.EqualValues(t, 1, view.Marked)
	assert.Equal(t, "cat-cdb", *c.ledger.por(uuidDe(1)).CategoryID)
}

// A outra metade de "atribuível": o grupo que ganha subcategoria ATIVA entre o
// plano e a escrita deixa de receber lançamento (spec 0005 §12/§13). Sem esta
// recusa, a marcação penduraria o dinheiro no grupo e o relatório passaria a
// ter a mesma quantia em dois níveis.
func TestDestinoQueVirouGrupoComSubcategoriaAtivaTambemEhConflito(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	c.tx.antes = func() {
		c.categorias.juntarFilha(casaA, "cat-cdb", "cat-cdb-b", "CDB do banco B", category.KindInvestment)
	}

	_, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, investment.ErrDestinationChanged)

	assert.Zero(t, c.ledger.chamadasSetNull)
	assert.Nil(t, c.ledger.por(uuidDe(1)).CategoryID)
}

// Filha ARQUIVADA não conta: o grupo cujas filhas foram todas arquivadas volta
// a ser destino legítimo, e é a mesma regra do recusarGrupoComFilhas do domínio
// de lançamentos. As duas precisam concordar.
func TestFilhaArquivadaNaoTornaODestinoInatribuivel(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.categorias.juntarFilha(casaA, "cat-cdb", "cat-cdb-b", "CDB do banco B", category.KindInvestment)
	c.categorias.arquivar(casaA, "cat-cdb-b")
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	view, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, view.Marked)
}

// "Uma consulta só, não uma por id": com DOIS destinos e os DOIS lotes
// (semCategoria e paraTrocar) ocupados, a reconferência é UMA chamada, com os
// destinos dos dois lotes juntos e sem repetição.
func TestReconferenciaEhUmaConsultaComOsDestinosDosDoisLotes(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	// Sem categoria → cat-cdb (aporte) e cat-resgate (resgate).
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindIncome, "RESGATE CDB", 50000, 2, "2026-09", ""))
	// Já categorizado com categoria comum → também vai para cat-cdb, no OUTRO
	// lote. O destino repetido não pode aparecer duas vezes.
	c.ledger.juntar(lanc(casaA, uuidDe(3), transaction.KindExpense, "CDB 30 DIAS", 100000, 3, "2026-09", "cat-mercado"))

	view, err := c.svc.Detect(context.Background(), ator(casaA),
		investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})
	require.NoError(t, err)
	assert.EqualValues(t, 3, view.Marked)

	assert.Equal(t, 1, c.categorias.chamadasAtribuiveis, "UMA consulta, com tudo o que precisa ser relido")

	// Os destinos dos DOIS lotes, sem repetição, MAIS a allowlist — que é
	// relida junto porque ela também pode ter mudado de natureza na janela
	// (achado A1, segunda face). Tudo num pedido só.
	assert.ElementsMatch(t,
		[]string{"cat-cdb", "cat-resgate", "cat-mercado", "cat-salario"},
		c.categorias.ultimosAtribuiveis)
	assert.Len(t, c.categorias.ultimosAtribuiveis, 4,
		"destino repetido entre os lotes não pode ir duas vezes")
}

// dryRun NÃO abre transação e, portanto, não reconfere nada: a prévia não
// escreve, e uma consulta a mais na prévia seria custo sem promessa.
func TestPreviaNaoReconfere(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	_, err := c.svc.Detect(context.Background(), ator(casaA),
		investment.DetectInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)

	assert.Zero(t, c.tx.chamadas)
	assert.Zero(t, c.categorias.chamadasAtribuiveis)
}

// Execução real que não tem nada a gravar não pergunta nada: sem destino, não
// há o que reconferir.
func TestSemDestinoNaoHaReconferencia(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "PADARIA DA ESQUINA", 1500, 1, "2026-09", ""))

	view, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Zero(t, view.Marked)
	assert.Zero(t, c.categorias.chamadasAtribuiveis)
}

// Falha da própria reconferência (banco fora do ar) não pode virar "nenhum
// destino é atribuível" nem, pior, "todos são": ela aborta a escrita com o erro
// embrulhado, que a borda traduz em 500 genérico.
func TestFalhaNaReconferenciaAbortaAEscrita(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))
	c.tx.antes = func() { c.categorias.erroAtribuiveis = errFalhaDoBanco }

	_, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, errFalhaDoBanco)
	assert.NotErrorIs(t, err, investment.ErrDestinationChanged, "falha de infraestrutura não é conflito de estado")

	assert.Zero(t, c.ledger.chamadasSetNull)
	assert.Nil(t, c.ledger.por(uuidDe(1)).CategoryID)
	assert.Empty(t, c.auditoria.entradas)
}

// A borda: 409 CONFLICT sem campos (ADR-028d), com a mensagem genérica, e SEM
// linha de ERROR no log — não foi o servidor que falhou.
func TestDestinoMudadoRespondeConflitoSemLogDeErro(t *testing.T) {
	t.Parallel()

	h := montarHTTP(t)
	h.categorias.juntar(casaA, "cat-cdb", "Renda fixa", category.KindInvestment, "cdb")
	h.contas.juntar(casaA, "acc-1", "Nubank")
	h.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))
	h.tx.antes = func() { h.categorias.excluir(casaA, "cat-cdb") }

	rec := h.post(t, casaA, `{"month":"2026-09","dryRun":false}`)
	assert.Equal(t, http.StatusConflict, rec.Code)

	codigo, campos := erroDoCorpo(t, rec)
	assert.Equal(t, httpserver.CodeConflict, codigo)
	assert.Empty(t, campos, "CONFLICT vai sem campos: não há campo do pedido para corrigir")

	assert.NotContains(t, h.logs.String(), `"level":"ERROR"`, "conflito de estado não é falha do servidor")
	assert.NotContains(t, h.logs.String(), "cat-cdb", "id de categoria é dado da casa e não vai para o log")
}
