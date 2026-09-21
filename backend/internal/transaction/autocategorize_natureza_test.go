package transaction_test

import (
	"bytes"
	netHTTP "net/http"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TOCTOU: a NATUREZA da categoria trocada ENTRE o plano e o UPDATE (achado A9)
// ---------------------------------------------------------------------------
//
// É a metade do achado A4 que ficou aberta. A reconferência original perguntava
// só "a categoria continua viva e atribuível?" — pergunta que descarta o
// `kind`. Mas o `kind` MUDA na janela, e muda por um caminho que o produto
// aceita de propósito:
//
// PATCH /categories/{id} recusa cruzar o lado do dinheiro apenas quando a
// categoria tem filha OU está em uso. Numa categoria de TOPO, sem filhas e sem
// nenhum lançamento, `expense → income` é ACEITO — e "sem nenhum lançamento" é
// exatamente o estado da categoria recém-criada cujo lote esta rota está
// calculando: a pessoa cadastra "Mercado" com a palavra-chave `mercado` e
// dispara o auto-categorize para popular o mês.
//
// O desfecho sem a releitura da natureza era um lote inteiro de DESPESAS
// pendurado numa categoria de RECEITA — estado que todas as outras portas
// recusam (PATCH /transactions/{id} responde 422 ErrCategoryKindMismatch, o
// confirm da importação degrada a sugestão, POST /investments/detect relê o
// `Kind` justamente por isso). Não é BOLA: os ids saem do plano, já filtrado
// pela casa do token. É integridade.

// trocarNatureza encena o `PATCH /categories/{id}` de outro membro (ou da mesma
// pessoa em outra aba) que muda a NATUREZA da categoria.
//
// O dublê grava o novo `kind` na linha, e é só isso que o PATCH faz nesse caso:
// categoria de topo, sem filhas e sem uso não tem mais nada a atualizar. A
// reconferência tem de enxergar a troca RELENDO a linha — se ela perguntasse
// algo que não devolve o `kind`, este teste passaria gravando errado.
func (a *ambiente) trocarNatureza(id, novaNatureza string) {
	c := a.categorias.linhas[id]
	c.Kind = novaNatureza
	a.categorias.add(c)
}

// O DEFEITO, encenado (achado A9): o plano decide que cat-mercado (despesa)
// recebe a linha do supermercado; na janela ela vira RECEITA; o UPDATE roda em
// seguida.
//
// Sem a releitura da natureza, o lote era gravado e a resposta era 200. Com
// ela, a operação INTEIRA falha com 409, nada é gravado e nada é auditado — e
// não "pula a categoria que mudou e segue com o resto", pelo mesmo motivo do
// A4: a pessoa pediu para categorizar EM X, e X deixou de ser X.
func TestAutoCategorizeNaoGravaEmCategoriaQueTrocouDeNaturezaNoMeio(t *testing.T) {
	t.Parallel()

	// A premissa do achado, travada aqui: é o pareamento que recusa este
	// estado, e é ele que a reconferência consulta.
	require.False(t, category.AceitaLancamento(transaction.KindExpense, category.KindIncome),
		"despesa em categoria de receita nunca é pareamento válido (ADR-029b)")

	amb, tx := ambienteComInterferencia(t, func(a *ambiente) {
		a.trocarNatureza("cat-mercado", category.KindIncome)
	})
	linhas := cenarioDuasCategorias(t, amb)
	antes := clonarLinhas(amb.repo)

	// A prévia do MESMO mês precisa achar as duas categorias: sem isso o teste
	// passaria com um plano vazio, sem nunca ter exercitado a corrida.
	previa, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)
	require.EqualValues(t, 2, previa.Categorized, "o cenário precisa ter o que gravar")

	_, err = amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: false})
	require.ErrorIs(t, err, transaction.ErrCategoryChanged)

	// Transação desfeita: NADA gravado — nem na categoria que virou receita,
	// nem na que continuou despesa — e NADA auditado.
	assert.True(t, tx.desfeito, "o corpo da transação tinha de devolver erro")
	assert.Equal(t, antes, clonarLinhas(amb.repo), "gravou alguma coisa")
	assert.Nil(t, amb.repo.linhas[linhas["mercado"].ID].CategoryID,
		"gravou despesa numa categoria de receita")
	assert.Nil(t, amb.repo.linhas[linhas["uber"].ID].CategoryID, "gravou o resto do lote")
	assert.Empty(t, amb.auditor.registros, "auditou uma escrita que não aconteceu")
}

// O mesmo pelo lado da RECEITA: a categoria de salário vira despesa na janela.
//
// Os dois sentidos são testados porque a reconferência pergunta por
// category.AceitaLancamento, cuja ORDEM dos argumentos importa: invertê-los faz
// a função responder false para tudo, e um único sentido testado não separa
// "recusa certa" de "recusa tudo" — quem separa é o par formado com os testes
// do caso normal e da troca dentro do mesmo lado.
func TestAutoCategorizeNaoGravaReceitaEmCategoriaQueVirouDespesa(t *testing.T) {
	t.Parallel()

	amb, _ := ambienteComInterferencia(t, func(a *ambiente) {
		a.trocarNatureza("cat-salario", category.KindExpense)
	})
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-salario", "Salário", category.KindIncome)
	amb.palavraDeCategoria(minhaCasa, "cat-salario", "folha")
	linha := amb.lancamento(minhaCasa, "acc-1", transaction.KindIncome, "Folha de pagamento", 5_000_00, 5)

	previa, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)
	require.EqualValues(t, 1, previa.Categorized, "o cenário precisa ter o que gravar")

	_, err = amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: false})
	require.ErrorIs(t, err, transaction.ErrCategoryChanged)
	assert.Nil(t, amb.repo.linhas[linha.ID].CategoryID, "gravou receita numa categoria de despesa")
	assert.Empty(t, amb.auditor.registros)
}

// TROCAR DE NATUREZA DENTRO DO MESMO LADO DO DINHEIRO NÃO DERRUBA NADA, e a
// reconferência não pode confundir as duas trocas.
//
// `expense → investment` é permitido mesmo com a categoria EM USO (ADR-029c):
// nenhum lançamento muda de sinal, de conta ou de saldo, e todos continuam
// válidos contra AceitaLancamento — despesa aceita `expense` E `investment`. Se
// a reconferência recusasse esta troca, ela estaria inventando uma regra que a
// escrita de um lançamento à mão não tem, e a operação inteira cairia em 409
// por um acontecimento benigno.
//
// Este teste existe pelo mesmo motivo do teste da categoria ARQUIVADA: impedir
// que a correção do A9 vire "o auto-categorize parou de categorizar".
func TestAutoCategorizeGravaQuandoANaturezaMudaDentroDoMesmoLado(t *testing.T) {
	t.Parallel()

	require.True(t, category.AceitaLancamento(transaction.KindExpense, category.KindInvestment),
		"despesa aceita categoria de aporte (ADR-029b)")

	amb, _ := ambienteComInterferencia(t, func(a *ambiente) {
		a.trocarNatureza("cat-mercado", category.KindInvestment)
	})
	linhas := cenarioDuasCategorias(t, amb)

	view, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: false})
	require.NoError(t, err, "troca dentro do mesmo lado não pode derrubar a execução")

	assert.EqualValues(t, 2, view.Categorized)
	require.NotNil(t, amb.repo.linhas[linhas["mercado"].ID].CategoryID)
	assert.Equal(t, "cat-mercado", *amb.repo.linhas[linhas["mercado"].ID].CategoryID)
}

// Na borda: 409 CONFLICT sem campos — a MESMA resposta do A4, para as duas
// metades do mesmo acontecimento não responderem coisas diferentes. Nada no log
// de erro (não foi o servidor que falhou) e nada de dado da casa.
func TestAutoCategorizeHandlerNaturezaTrocadaNoMeioResponde409(t *testing.T) {
	t.Parallel()

	amb, _ := ambienteComInterferencia(t, func(a *ambiente) {
		a.trocarNatureza("cat-mercado", category.KindIncome)
	})
	linhas := cenarioDuasCategorias(t, amb)

	logs := &bytes.Buffer{}
	h := transaction.NewHandler(amb.svc, logging.New(logs, logging.Options{Level: "debug", Format: "json"}), 0)
	borda := &httpAmbiente{ambiente: amb, handler: h, logs: logs}

	rec := borda.chamar(t, minhaCasa, netHTTP.MethodPost, "/transactions/auto-categorize",
		`{"month":"2026-09","dryRun":false}`, h.AutoCategorize)
	require.Equal(t, netHTTP.StatusConflict, rec.Code, rec.Body.String())

	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeConflict, codigo)
	assert.Equal(t, "CONFLICT", codigo)
	assert.Empty(t, campos, "409 CONFLICT não tem campo a corrigir")
	assert.Nil(t, amb.repo.linhas[linhas["mercado"].ID].CategoryID, "nada gravado")
	assert.Empty(t, amb.auditor.registros)

	log := logs.String()
	assert.NotContains(t, log, `"level":"ERROR"`, "conflito não é falha do servidor")
	for _, proibido := range []string{"cat-mercado", "supermercado", "SUPERMERCADO EXTRA 123", linhas["mercado"].ID} {
		assert.NotContains(t, log, proibido, "vazou no log: %q", proibido)
	}
}

// A categoria EXCLUÍDA continua caindo em 409 com a reconferência nova: o A4
// não pode regredir quando o A9 troca a pergunta de "atribuível?" para
// "atribuível E do lado certo?".
//
// É a mesma prova do autocategorize_toctou_test.go, refeita aqui contra a ORDEM
// das checagens: presença no mapa primeiro, natureza depois. Uma categoria
// excluída não tem estado relido nenhum, e perguntar a natureza do zero value
// antes de conferir a presença compararia "" com "" numa reescrita futura.
func TestAutoCategorizeExcluidaContinua409ComAReconferenciaNova(t *testing.T) {
	t.Parallel()

	amb, _ := ambienteComInterferencia(t, func(a *ambiente) { a.excluirCategoria("cat-mercado") })
	linhas := cenarioDuasCategorias(t, amb)

	_, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: false})
	require.ErrorIs(t, err, transaction.ErrCategoryChanged)
	assert.Nil(t, amb.repo.linhas[linhas["mercado"].ID].CategoryID)
	assert.Empty(t, amb.auditor.registros)
}
