package transaction_test

import (
	"bytes"
	"context"
	netHTTP "net/http"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// TOCTOU: categoria excluída ENTRE o plano e o UPDATE (achado A4)
// ---------------------------------------------------------------------------

// txComInterferencia roda `antes` UMA vez, no instante em que a transação
// abre, e só então executa o corpo. É a janela do achado A4 encenada: desde a
// correção do A2 o cálculo do plano acontece FORA da transação, então tudo o
// que outro membro da casa fizer nesse intervalo chega ao corpo já feito.
//
// `desfeito` guarda se o corpo devolveu erro — é como o teste prova que a
// transação seria desfeita sem depender de um banco de verdade.
type txComInterferencia struct {
	antes    func()
	feito    bool
	desfeito bool
}

func (t *txComInterferencia) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if !t.feito {
		t.feito = true
		t.antes()
	}
	err := fn(ctx)
	t.desfeito = err != nil
	return err
}

// ambienteComInterferencia é o novoAmbiente com um Transactor que deixa outro
// membro da casa agir entre o plano e a escrita.
func ambienteComInterferencia(t *testing.T, antes func(*ambiente)) (*ambiente, *txComInterferencia) {
	t.Helper()

	repo := novoRepo()
	contas := novasContas()
	categorias := novasCategorias()
	faturas := novasFaturas()
	auditor := &auditorFake{}
	amb := &ambiente{repo: repo, contas: contas, categorias: categorias, faturas: faturas, auditor: auditor}

	tx := &txComInterferencia{antes: func() { antes(amb) }}
	amb.svc = transaction.NewService(repo, contas, categorias, faturas, tx,
		classify.NewLoader(categorias, contas),
		transaction.WithIDs(repo.proximoID),
		transaction.WithClock(func() time.Time { return agora }),
		transaction.WithAudit(auditor),
	)
	return amb, tx
}

// excluirCategoria encena o `DELETE /categories/{id}` de outro membro: a
// categoria ganha deleted_at (exclusão LÓGICA, ADR-013) e as palavras-chave
// dela saem FISICAMENTE, na mesma transação, como category.Delete faz.
//
// O deleted_at é gravado, e não a linha removida do dublê, de propósito: é
// assim que o banco fica, e é o filtro do repositório que tem de recusá-la.
func (a *ambiente) excluirCategoria(id string) {
	c := a.categorias.linhas[id]
	c.DeletedAt = ptr(agora)
	a.categorias.add(c)

	restantes := a.categorias.palavras[:0]
	for _, kw := range a.categorias.palavras {
		if kw.CategoryID != id {
			restantes = append(restantes, kw)
		}
	}
	a.categorias.palavras = restantes
}

// cenarioDuasCategorias monta duas despesas que casam com DUAS categorias
// diferentes. São duas de propósito: é o que prova que a operação INTEIRA
// morre — inclusive a categoria que continua viva —, e não só a que sumiu.
func cenarioDuasCategorias(t *testing.T, amb *ambiente) map[string]transaction.Transaction {
	t.Helper()
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	amb.categoria(minhaCasa, "cat-transporte", "Transporte", category.KindExpense)
	amb.palavraDeCategoria(minhaCasa, "cat-mercado", "supermercado")
	amb.palavraDeCategoria(minhaCasa, "cat-transporte", "uber")

	return map[string]transaction.Transaction{
		"mercado":    amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "SUPERMERCADO EXTRA 123", 150_00, 3),
		"uber":       amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Uber *Trip", 25_00, 4),
		"semPalavra": amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria da esquina", 12_00, 9),
	}
}

// O DEFEITO, encenado: o plano decide que cat-mercado recebe a linha do
// supermercado; outro membro exclui cat-mercado (e o `inUse` dele não encontra
// uso nenhum, porque nada foi gravado ainda); o UPDATE roda em seguida.
//
// Sem a reconferência, a linha ficaria apontando para uma categoria EXCLUÍDA —
// exatamente o estado que o ErrInUse existe para impedir. Com ela, a operação
// INTEIRA falha com 409, nada é gravado e nada é auditado.
func TestAutoCategorizeNaoGravaCategoriaExcluidaNoMeio(t *testing.T) {
	t.Parallel()

	amb, tx := ambienteComInterferencia(t, func(a *ambiente) { a.excluirCategoria("cat-mercado") })
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

	// Transação desfeita: NADA gravado — nem a categoria que sumiu, nem a que
	// continuou viva — e NADA auditado.
	assert.True(t, tx.desfeito, "o corpo da transação tinha de devolver erro")
	assert.Equal(t, antes, clonarLinhas(amb.repo), "gravou alguma coisa")
	assert.Nil(t, amb.repo.linhas[linhas["mercado"].ID].CategoryID, "gravou uma categoria excluída")
	assert.Nil(t, amb.repo.linhas[linhas["uber"].ID].CategoryID, "gravou o resto do lote")
	assert.Empty(t, amb.auditor.registros, "auditou uma escrita que não aconteceu")
}

// A mesma janela fecha para a categoria que virou GRUPO com subcategoria
// ativa: ela deixou de poder receber lançamento (spec 0005 §13), e gravar nela
// seria pendurar lançamento onde a tela não deixa escolher.
func TestAutoCategorizeRecusaGrupoQueGanhouFilhaNoMeio(t *testing.T) {
	t.Parallel()

	amb, _ := ambienteComInterferencia(t, func(a *ambiente) {
		pai := "cat-mercado"
		a.categorias.add(category.Category{
			ID: "cat-feira", HouseholdID: minhaCasa, Name: "Feira",
			Kind: category.KindExpense, ParentID: &pai,
		})
	})
	linhas := cenarioDuasCategorias(t, amb)

	_, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: false})
	require.ErrorIs(t, err, transaction.ErrCategoryChanged)
	assert.Nil(t, amb.repo.linhas[linhas["mercado"].ID].CategoryID)
	assert.Empty(t, amb.auditor.registros)
}

// CATEGORIA ARQUIVADA NA JANELA TAMBÉM DERRUBA: 409, igual à excluída, à que
// trocou de natureza e ao grupo que ganhou filha.
//
// ATENÇÃO — ESTA RESPOSTA JÁ FOI O CONTRÁRIO, e a inversão é deliberada. A
// versão anterior deste teste exigia que a gravação SEGUISSE, apoiada em
// "arquivar é benigno, não desfaz marcação". A premissa está certa e a
// conclusão estava errada, porque ela troca duas coisas diferentes:
//
//   - MARCAÇÃO EXISTENTE sobrevive ao arquivamento. O lançamento que já aponta
//     para a categoria continua apontando, e ela continua contando nos
//     relatórios (PLANOS.md §4.4);
//   - ATRIBUIÇÃO NOVA, não. As outras três portas do produto recusam categoria
//     arquivada para marcar uma linha: transaction.ErrCategoryArchived no
//     PATCH /transactions/{id}, no CreateBatch do lote e no confirm da
//     importação.
//
// O auto-categorize faz ATRIBUIÇÃO NOVA. Gravar aqui faria desta a ÚNICA porta
// do produto marcando onde as outras três recusam — que é exatamente a forma do
// achado A9, num qualificador diferente. POST /investments/detect chegou à
// mesma conclusão e recusa pelo mesmo campo (investment/detect.go).
//
// O caso só existe pela JANELA: classify.Load só carrega categorias ATIVAS,
// então uma arquivada nunca entra no plano — ela só pode ter sido arquivada
// DURANTE o cálculo, que é a corrida que esta reconferência fecha. Por isso
// recusar aqui não tira nada de ninguém em uso normal.
//
// Quem for "consertar" isto de volta achando que é aperto demais: a pergunta a
// responder antes é por que esta rota deveria marcar o que o PATCH de uma
// única linha recusa.
func TestAutoCategorizeRecusaCategoriaArquivadaNoMeio(t *testing.T) {
	t.Parallel()

	amb, tx := ambienteComInterferencia(t, func(a *ambiente) {
		c := a.categorias.linhas["cat-mercado"]
		c.ArchivedAt = ptr(agora)
		a.categorias.add(c)
	})
	linhas := cenarioDuasCategorias(t, amb)
	antes := clonarLinhas(amb.repo)

	previa, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)
	require.EqualValues(t, 2, previa.Categorized, "o cenário precisa ter o que gravar")

	_, err = amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: false})
	require.ErrorIs(t, err, transaction.ErrCategoryChanged)

	assert.True(t, tx.desfeito, "o corpo da transação tinha de devolver erro")
	assert.Equal(t, antes, clonarLinhas(amb.repo), "gravou alguma coisa")
	assert.Nil(t, amb.repo.linhas[linhas["mercado"].ID].CategoryID,
		"atribuiu uma categoria ARQUIVADA a uma linha que não tinha categoria")
	assert.Nil(t, amb.repo.linhas[linhas["uber"].ID].CategoryID, "gravou o resto do lote")
	assert.Empty(t, amb.auditor.registros, "auditou uma escrita que não aconteceu")
}

// O que arquivar NÃO pode causar: derrubar a execução por causa de uma
// categoria que arquivou FORA do lote.
//
// A recusa acima é sobre o DESTINO do plano. Uma categoria arquivada que não
// recebe nenhuma linha não entra em `categorias`, não é relida e não tem
// opinião sobre esta execução — se entrasse, a rota ficaria refém de qualquer
// arrumação de taxonomia acontecendo em paralelo.
func TestAutoCategorizeIgnoraArquivamentoForaDoLote(t *testing.T) {
	t.Parallel()

	amb, _ := ambienteComInterferencia(t, func(a *ambiente) {
		c := a.categorias.linhas["cat-fora"]
		c.ArchivedAt = ptr(agora)
		a.categorias.add(c)
	})
	linhas := cenarioDuasCategorias(t, amb)
	amb.categoria(minhaCasa, "cat-fora", "Lazer", category.KindExpense)

	view, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: false})
	require.NoError(t, err, "categoria fora do lote não tem voto nesta execução")

	assert.EqualValues(t, 2, view.Categorized)
	require.NotNil(t, amb.repo.linhas[linhas["mercado"].ID].CategoryID)
	assert.Equal(t, "cat-mercado", *amb.repo.linhas[linhas["mercado"].ID].CategoryID)
}

// Sem interferência nenhuma, a reconferência não muda NADA: ela é uma consulta
// a mais, não um filtro a mais. Este é o teste que impede a correção do A4 de
// virar "o auto-categorize parou de categorizar".
func TestAutoCategorizeReconferenciaNaoMudaOCasoNormal(t *testing.T) {
	t.Parallel()

	amb, _ := ambienteComInterferencia(t, func(*ambiente) {})
	linhas := cenarioDuasCategorias(t, amb)

	view, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: false})
	require.NoError(t, err)

	assert.EqualValues(t, 2, view.Categorized)
	assert.EqualValues(t, 1, view.Unmatched, "só a padaria")
	require.NotNil(t, amb.repo.linhas[linhas["mercado"].ID].CategoryID)
	assert.Equal(t, "cat-mercado", *amb.repo.linhas[linhas["mercado"].ID].CategoryID)
}

// A reconferência é UMA consulta por EXECUÇÃO, nunca uma por linha nem uma por
// categoria — e a prévia não faz nenhuma, porque prévia não escreve.
func TestAutoCategorizeReconfereComUmaConsultaSo(t *testing.T) {
	t.Parallel()

	amb, _ := ambienteComInterferencia(t, func(*ambiente) {})
	cenarioDuasCategorias(t, amb)

	_, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)
	require.Zero(t, amb.categorias.atribuiveisPedidas, "a prévia não escreve, então não reconfere")

	_, err = amb.svc.AutoCategorize(t.Context(), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: false})
	require.NoError(t, err)
	assert.Equal(t, 1, amb.categorias.atribuiveisPedidas,
		"a execução real reconfere UMA vez, com as DUAS categorias do plano na mesma consulta")
}

// Na borda: 409 CONFLICT sem campos, a mesma forma de
// ErrTransferConversionConflict (ADR-028d) e de
// investment.ErrDestinationChanged. Nada no log de erro — não foi o servidor
// que falhou — e nada de dado da casa.
//
// O contrato já declara este 409: `api/openapi.yaml`, em
// POST /transactions/auto-categorize, publica 200, 400, 401, 409, 413, 415,
// 422, 429 e 500.
func TestAutoCategorizeHandlerCategoriaExcluidaNoMeioResponde409(t *testing.T) {
	t.Parallel()

	amb, _ := ambienteComInterferencia(t, func(a *ambiente) { a.excluirCategoria("cat-mercado") })
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
