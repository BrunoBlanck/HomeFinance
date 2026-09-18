package importer_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QA da emenda §11 da spec 0005 — o atalho de categorização de `/lancamentos`
// exercitado PELA API REAL, sobre SQLite, com lançamentos que entraram por uma
// importação de verdade.
//
// Os testes de `internal/transaction` já provam a regra contra dublês. O que
// só este nível prova é a FIAÇÃO da sequência que a tela dispara — `PATCH
// /categories/{id}` → `PATCH /transactions/{id}` → `POST
// /transactions/auto-categorize` — atravessando três pacotes, o índice único
// de palavra-chave, a transação do serviço e o `category_id IS NULL` do
// UPDATE em massa. Um dublê responderia "ok" às três e o teste passaria com a
// garantia desligada.

// --- ferramentas ------------------------------------------------------------

func (a *ambiente) handlerCategoria() *category.Handler {
	return category.NewHandler(a.categoriaSvc, logging.Discard(), 0)
}

// patchCategoriaDoLancamento roda PATCH /transactions/{id} pelo handler, com o
// id no path como o ServeMux o entrega.
func (a *ambiente) patchCategoriaDoLancamento(t *testing.T, id, corpoJSON string) *httptest.ResponseRecorder {
	t.Helper()
	return a.patchCategoriaComo(t, a.casa.ID, a.usuario.ID, id, corpoJSON)
}

// patchCategoriaComo é o mesmo PATCH com a identidade escolhida — é com ele
// que a casa vizinha tenta alcançar o que não é dela.
func (a *ambiente) patchCategoriaComo(t *testing.T, casa, usuario, id, corpoJSON string) *httptest.ResponseRecorder {
	t.Helper()
	r := requisicao(t, http.MethodPatch, "/api/v1/transactions/"+url.PathEscape(id), strings.NewReader(corpoJSON),
		identidade(casa, usuario))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	a.handlerLancamento().UpdateCategory(rec, r)
	return rec
}

// patchPalavrasDaCategoria roda PATCH /categories/{id} com o corpo literal —
// é assim que a tela manda a lista inteira (leitura antes de escrita).
func (a *ambiente) patchPalavrasDaCategoria(t *testing.T, id, corpoJSON string) *httptest.ResponseRecorder {
	t.Helper()
	r := requisicao(t, http.MethodPatch, "/api/v1/categories/"+id, strings.NewReader(corpoJSON),
		identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	a.handlerCategoria().Update(rec, r)
	return rec
}

func (a *ambiente) getCategoriaPelaAPI(t *testing.T, id string) category.View {
	t.Helper()
	r := requisicao(t, http.MethodGet, "/api/v1/categories/"+id, nil, identidade(a.casa.ID, a.usuario.ID))
	r.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	a.handlerCategoria().Get(rec, r)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var v category.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	return v
}

// autoCategorizarPelaAPI roda POST /transactions/auto-categorize no mês.
func (a *ambiente) autoCategorizarPelaAPI(t *testing.T, mes string, dryRun bool) *httptest.ResponseRecorder {
	t.Helper()
	corpo := fmt.Sprintf(`{"month":%q,"dryRun":%t}`, mes, dryRun)
	r := requisicao(t, http.MethodPost, "/api/v1/transactions/auto-categorize", strings.NewReader(corpo),
		identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handlerLancamento().AutoCategorize(rec, r)
	return rec
}

// excluirLancamentoPelaAPI roda DELETE /transactions/{id} (exclusão lógica).
func (a *ambiente) excluirLancamentoPelaAPI(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	r := requisicao(t, http.MethodDelete, "/api/v1/transactions/"+id, nil, identidade(a.casa.ID, a.usuario.ID))
	r.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	a.handlerLancamento().Delete(rec, r)
	return rec
}

// lancamentoPorDescricao acha um lançamento vivo da casa pela descrição já
// sanitizada.
func (a *ambiente) lancamentoPorDescricao(t *testing.T, mes, descricao string) transaction.Transaction {
	t.Helper()
	for _, l := range a.lancamentosDa(t, a.casa.ID, mes) {
		if l.Description == descricao {
			return l
		}
	}
	t.Fatalf("lançamento %q não encontrado em %s", descricao, mes)
	return transaction.Transaction{}
}

// csvExtratoEmMes é o `csvExtratoAPI` com o mês escolhido — o cenário da §11
// precisa de um lançamento em OUTRO mês para provar que o reprocessamento não
// sai do mês da tela.
func csvExtratoEmMes(mes int, linhas ...linhaExtrato) []byte {
	var b strings.Builder
	b.WriteString("Data,Valor,Identificador,Descrição\n")
	for _, l := range linhas {
		fmt.Fprintf(&b, "%02d/%02d/2026,%s,11111111-1111-4111-8111-1111111111%s,%s\n",
			l.dia, mes, l.valor, l.idSufixo, l.descricao)
	}
	return []byte(b.String())
}

func (a *ambiente) acoesDeLancamento() []string {
	var out []string
	for _, acao := range a.auditoria.acoes() {
		if strings.HasPrefix(acao, "transaction.") {
			out = append(out, acao)
		}
	}
	return out
}

// --- critério (a) e (b): o PATCH avulso sobre dado importado -----------------

// O caminho feliz sobre uma linha que entrou por importação: só a categoria
// muda, a proveniência e o dinheiro ficam, e nenhuma outra linha é tocada.
func TestPatchDeCategoriaEmLancamentoImportadoTrocaSoACategoria(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	alimentacao := a.categoriaComPalavras(t, "Alimentação", category.KindExpense)

	extrato := csvExtratoAPI(
		linhaExtrato{5, "-45.90", "01", "MERCADO X"},
		linhaExtrato{6, "-12.00", "02", "PADARIA Z"},
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv", extrato))
	resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))

	alvo := a.lancamentoPorDescricao(t, "2026-08", "MERCADO X")
	outro := a.lancamentoPorDescricao(t, "2026-08", "PADARIA Z")
	require.Nil(t, alvo.CategoryID)
	a.auditoria.entradas = nil

	rec := a.patchCategoriaDoLancamento(t, alvo.ID, fmt.Sprintf(`{"categoryId":%q}`, alimentacao.ID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var view transaction.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	require.NotNil(t, view.CategoryID)
	assert.Equal(t, alimentacao.ID, *view.CategoryID)
	require.NotNil(t, view.CategoryName)
	assert.Equal(t, "Alimentação", *view.CategoryName)

	// Nada mais mudou na linha: valor, descrição, data, conta, competência e a
	// PROVENIÊNCIA (source/importBatchId continuam os da importação).
	gravado := a.lancamentoPorDescricao(t, "2026-08", "MERCADO X")
	assert.Equal(t, alvo.AmountCents, gravado.AmountCents)
	assert.Equal(t, alvo.Description, gravado.Description)
	assert.Equal(t, alvo.OccurredOn, gravado.OccurredOn)
	assert.Equal(t, alvo.AccountID, gravado.AccountID)
	assert.Equal(t, alvo.CompetenceMonth, gravado.CompetenceMonth)
	assert.Equal(t, transaction.SourceImport, gravado.Source)
	assert.Equal(t, alvo.ImportBatchID, gravado.ImportBatchID)
	assert.Equal(t, alvo.DedupKey, gravado.DedupKey)

	// Critério (b): "só este" não altera nenhum outro lançamento.
	assert.Nil(t, a.lancamentoPorDescricao(t, "2026-08", "PADARIA Z").CategoryID)
	assert.Equal(t, outro.UpdatedAt, a.lancamentoPorDescricao(t, "2026-08", "PADARIA Z").UpdatedAt)

	// Um evento de auditoria, com o id da linha e nada mais.
	assert.Equal(t, []string{"transaction.updated"}, a.acoesDeLancamento())
	for _, e := range a.auditoria.entradas {
		if e.Action == "transaction.updated" {
			assert.Equal(t, alvo.ID, e.EntityID)
			assert.Equal(t, a.casa.ID, e.HouseholdID)
		}
	}
}

// Mandar a categoria que a linha JÁ tem é 200 sem escrita e sem auditoria — um
// "atualizado" numa linha que não mudou seria um rastro que mente.
func TestPatchComACategoriaQueJaEstaNaoEscreveNemAudita(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	alimentacao := a.categoriaComPalavras(t, "Alimentação", category.KindExpense)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv",
		csvExtratoAPI(linhaExtrato{5, "-45.90", "01", "MERCADO X"})))
	resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	alvo := a.lancamentoPorDescricao(t, "2026-08", "MERCADO X")

	corpo := fmt.Sprintf(`{"categoryId":%q}`, alimentacao.ID)
	require.Equal(t, http.StatusOK, a.patchCategoriaDoLancamento(t, alvo.ID, corpo).Code)
	primeira := a.lancamentoPorDescricao(t, "2026-08", "MERCADO X")
	a.auditoria.entradas = nil

	// O relógio anda: se houvesse escrita, o updated_at mudaria.
	a.relogio.avancar(time.Hour)
	rec := a.patchCategoriaDoLancamento(t, alvo.ID, corpo)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	depois := a.lancamentoPorDescricao(t, "2026-08", "MERCADO X")
	assert.Equal(t, primeira.UpdatedAt, depois.UpdatedAt, "nada foi gravado")
	assert.Equal(t, alimentacao.ID, texto(depois.CategoryID))
	assert.Empty(t, a.acoesDeLancamento(), "sem escrita, sem auditoria")

	// E a resposta continua sendo o lançamento inteiro.
	var view transaction.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	assert.Equal(t, alimentacao.ID, texto(view.CategoryID))
}

// Lançamento EXCLUÍDO logicamente é 404, byte a byte igual ao inexistente — e
// continua excluído e sem categoria por baixo.
func TestPatchEmLancamentoExcluidoEh404IgualAoInexistente(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	alimentacao := a.categoriaComPalavras(t, "Alimentação", category.KindExpense)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv",
		csvExtratoAPI(linhaExtrato{5, "-45.90", "01", "MERCADO X"})))
	resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	alvo := a.lancamentoPorDescricao(t, "2026-08", "MERCADO X")

	require.Equal(t, http.StatusNoContent, a.excluirLancamentoPelaAPI(t, alvo.ID).Code)
	a.auditoria.entradas = nil

	corpo := fmt.Sprintf(`{"categoryId":%q}`, alimentacao.ID)
	excluido := a.patchCategoriaDoLancamento(t, alvo.ID, corpo)
	inexistente := a.patchCategoriaDoLancamento(t, "00000000-0000-7000-8000-000000000999", corpo)
	require.Equal(t, http.StatusNotFound, excluido.Code, excluido.Body.String())
	require.Equal(t, http.StatusNotFound, inexistente.Code)
	assert.Equal(t, inexistente.Body.String(), excluido.Body.String(), "corpo byte a byte igual")
	assert.Equal(t, inexistente.Header(), excluido.Header())

	// A linha excluída não foi tocada, e nada foi auditado.
	morta, err := a.repoTx.ByIDIncludingDeleted(t.Context(), a.casa.ID, alvo.ID)
	require.NoError(t, err)
	assert.Nil(t, morta.CategoryID)
	assert.NotNil(t, morta.DeletedAt)
	assert.Empty(t, a.acoesDeLancamento())
}

// Perna de transferência REAL (criada pelo confirm com `transfer`) é 422 em
// `fields.id` — `transfer_*` nunca tem categoria (ADR-016).
func TestPatchEmPernaDeTransferenciaRealEh422(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	contaB := a.contaItau(t)
	alimentacao := a.categoriaComPalavras(t, "Alimentação", category.KindExpense)

	extrato := csvExtratoAPI(linhaExtrato{5, "-150.00", "01", "Transferência enviada pelo Pix - Itau Corrente"})
	lote := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extrato))
	linha := revisarPelaAPI(t, a, lote.ID).Items[0]
	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}]}`, linha.ID, contaB.ID)))
	require.Equal(t, 1, res.TransfersCreated)
	a.auditoria.entradas = nil

	pernas := a.lancamentosDa(t, a.casa.ID, "2026-08")
	require.Len(t, pernas, 2)
	for _, perna := range pernas {
		rec := a.patchCategoriaDoLancamento(t, perna.ID, fmt.Sprintf(`{"categoryId":%q}`, alimentacao.ID))
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		var env httpserver.ErrorEnvelope
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
		assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
		assert.Contains(t, env.Error.Fields, "id")
	}
	for _, perna := range a.lancamentosDa(t, a.casa.ID, "2026-08") {
		assert.Nil(t, perna.CategoryID, "perna não recebe categoria")
	}
	assert.Empty(t, a.acoesDeLancamento())
}

// --- critério (c): a sequência com palavra, inteira, pela API ---------------

// O cenário da §11.3 como a tela o dispara: (a) palavra na categoria, (b) esta
// linha, (c) reprocessar o mês. O que cada asserção vigia está no comentário —
// o essencial é que (c) NÃO toca o que já tem categoria nem o que é de outro
// mês.
func TestSequenciaComPalavraChaveCategorizaSoOsOutrosSemCategoriaDoMes(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	alimentacao := a.categoriaComPalavras(t, "Alimentação", category.KindExpense)
	lazer := a.categoriaComPalavras(t, "Lazer", category.KindExpense)

	// Agosto: duas linhas "mercado" sem categoria, uma "padaria" sem categoria
	// e uma "mercado" que JÁ tem categoria (Lazer, escolhida no confirm).
	agosto := csvExtratoAPI(
		linhaExtrato{5, "-45.90", "01", "MERCADO X"},
		linhaExtrato{6, "-33.10", "02", "MERCADO Y"},
		linhaExtrato{7, "-12.00", "03", "PADARIA Z"},
		linhaExtrato{8, "-99.00", "04", "MERCADO W"},
	)
	loteAgosto := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "ago.csv", agosto))
	linhaW := linhaComDescricao(t, revisarPelaAPI(t, a, loteAgosto.ID).Items, "MERCADO W")
	resultadoDaResposta(t, confirmarPelaAPI(t, a, loteAgosto.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"import","categoryId":%q}]}`, linhaW.ID, lazer.ID)))

	// Julho: uma linha "mercado" sem categoria — é o controle do mês.
	julho := csvExtratoEmMes(7, linhaExtrato{20, "-20.00", "05", "MERCADO J"})
	loteJulho := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "jul.csv", julho))
	resultadoDaResposta(t, confirmarPelaAPI(t, a, loteJulho.ID, `{"decisions":[]}`))

	alvo := a.lancamentoPorDescricao(t, "2026-08", "MERCADO X")
	require.Nil(t, alvo.CategoryID)
	require.Equal(t, lazer.ID, texto(a.lancamentoPorDescricao(t, "2026-08", "MERCADO W").CategoryID))
	require.Nil(t, a.lancamentoPorDescricao(t, "2026-07", "MERCADO J").CategoryID)

	// (a) a palavra entra na categoria com a lista INTEIRA (leitura antes de
	// escrita — aqui a lista estava vazia).
	recA := a.patchPalavrasDaCategoria(t, alimentacao.ID, `{"keywords":["mercado"]}`)
	require.Equal(t, http.StatusOK, recA.Code, recA.Body.String())
	assert.Equal(t, []string{"mercado"}, a.getCategoriaPelaAPI(t, alimentacao.ID).Keywords)

	// (b) este lançamento.
	recB := a.patchCategoriaDoLancamento(t, alvo.ID, fmt.Sprintf(`{"categoryId":%q}`, alimentacao.ID))
	require.Equal(t, http.StatusOK, recB.Code, recB.Body.String())

	// (c) o mês da tela, gravando.
	recC := a.autoCategorizarPelaAPI(t, "2026-08", false)
	require.Equal(t, http.StatusOK, recC.Code, recC.Body.String())
	var resultado transaction.AutoCategorizeView
	require.NoError(t, json.Unmarshal(recC.Body.Bytes(), &resultado))

	// O número do toast conta só os OUTROS: MERCADO Y. PADARIA Z não casa com
	// nada; MERCADO W já tinha categoria; MERCADO J é de julho; MERCADO X já
	// foi por (b).
	assert.EqualValues(t, 1, resultado.Categorized)
	assert.EqualValues(t, 1, resultado.Unmatched, "PADARIA Z continua sem categoria")

	assert.Equal(t, alimentacao.ID, texto(a.lancamentoPorDescricao(t, "2026-08", "MERCADO X").CategoryID))
	assert.Equal(t, alimentacao.ID, texto(a.lancamentoPorDescricao(t, "2026-08", "MERCADO Y").CategoryID))
	assert.Nil(t, a.lancamentoPorDescricao(t, "2026-08", "PADARIA Z").CategoryID)
	assert.Equal(t, lazer.ID, texto(a.lancamentoPorDescricao(t, "2026-08", "MERCADO W").CategoryID),
		"o já categorizado NUNCA é sobrescrito (§4.3)")
	assert.Nil(t, a.lancamentoPorDescricao(t, "2026-07", "MERCADO J").CategoryID,
		"o reprocessamento não sai do mês da tela")

	// Idempotência: rodar de novo no mesmo mês categoriza 0.
	recDeNovo := a.autoCategorizarPelaAPI(t, "2026-08", false)
	require.Equal(t, http.StatusOK, recDeNovo.Code)
	require.NoError(t, json.Unmarshal(recDeNovo.Body.Bytes(), &resultado))
	assert.EqualValues(t, 0, resultado.Categorized)

	// Auditoria da sequência: um `transaction.updated` (b) e um
	// `transaction.auto_categorized` por execução real de (c) — nada com
	// valor nem descrição (S8: AuditParams não tem campo para isso).
	assert.Equal(t, []string{"transaction.updated", "transaction.auto_categorized", "transaction.auto_categorized"},
		a.acoesDeLancamento())
}

// 409 em (a): o servidor recusa a palavra e NÃO tem efeito colateral nenhum —
// nem na categoria que pediu, nem na dona, nem no lançamento. (Parar a
// sequência é responsabilidade da tela; o que se prova aqui é que não há o que
// desfazer.)
func TestConflitoDePalavraNaoDeixaEfeitoColateralNoServidor(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	// As categorias nascem SEM palavras e as recebem depois da importação: as
	// duas linhas precisam entrar sem categoria (é o estado em que a tela
	// oferece o atalho).
	alimentacao := a.categoriaComPalavras(t, "Alimentação", category.KindExpense)
	padarias := a.categoriaComPalavras(t, "Padarias", category.KindExpense)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv", csvExtratoAPI(
		linhaExtrato{5, "-12.00", "01", "PADARIA Z"},
		linhaExtrato{6, "-45.90", "02", "MERCADO X"},
	)))
	resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	require.Equal(t, http.StatusOK, a.patchPalavrasDaCategoria(t, padarias.ID, `{"keywords":["padaria"]}`).Code)
	require.Equal(t, http.StatusOK, a.patchPalavrasDaCategoria(t, alimentacao.ID, `{"keywords":["mercado"]}`).Code)
	alvo := a.lancamentoPorDescricao(t, "2026-08", "PADARIA Z")
	require.Nil(t, alvo.CategoryID)
	a.auditoria.entradas = nil

	// (a) tentar «padaria» em Alimentação: a palavra é de Padarias.
	rec := a.patchPalavrasDaCategoria(t, alimentacao.ID, `{"keywords":["mercado","padaria"]}`)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	var env httpserver.ErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, "KEYWORD_TAKEN", env.Error.Code)
	assert.Equal(t, "padaria", env.Error.Fields["keyword"])
	assert.Equal(t, padarias.ID, env.Error.Fields["ownerId"], "a tela resolve a dona pelo NOME a partir daqui")

	// Nada mudou em lugar nenhum: a lista de Alimentação continua a de antes,
	// a de Padarias também, e o lançamento segue sem categoria.
	assert.Equal(t, []string{"mercado"}, a.getCategoriaPelaAPI(t, alimentacao.ID).Keywords)
	assert.Equal(t, []string{"padaria"}, a.getCategoriaPelaAPI(t, padarias.ID).Keywords)
	assert.Nil(t, a.lancamentoPorDescricao(t, "2026-08", "PADARIA Z").CategoryID)
	assert.Empty(t, a.acoesDeLancamento())

	// E o mês continua como estava: um `auto-categorize` agora categorizaria
	// pela palavra que JÁ existia (mercado), não pela recusada.
	recC := a.autoCategorizarPelaAPI(t, "2026-08", true)
	require.Equal(t, http.StatusOK, recC.Code, recC.Body.String())
	var previa transaction.AutoCategorizeView
	require.NoError(t, json.Unmarshal(recC.Body.Bytes(), &previa))
	assert.EqualValues(t, 2, previa.Categorized, "PADARIA Z por «padaria» (Padarias) e MERCADO X por «mercado»")
	porID := map[string]string{}
	for _, item := range previa.Items {
		porID[item.ID] = item.CategoryID
	}
	assert.Equal(t, padarias.ID, porID[alvo.ID], "quem sugere PADARIA Z é a dona da palavra, não Alimentação")

	// A prévia não escreveu nada.
	assert.Nil(t, a.lancamentoPorDescricao(t, "2026-08", "PADARIA Z").CategoryID)
	assert.Nil(t, a.lancamentoPorDescricao(t, "2026-08", "MERCADO X").CategoryID)
}

// BOLA sobre dado REAL: a casa vizinha não alcança a minha linha nem com o id
// certo, e a variação de CAIXA do id (que uma collation case-insensitive
// deixaria passar em MySQL/SQL Server) também é 404 — o mesmo corpo do
// inexistente, nada gravado.
func TestPatchDeCategoriaNaoAtravessaCasaNemCaixaDoID(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	alimentacao := a.categoriaComPalavras(t, "Alimentação", category.KindExpense)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv",
		csvExtratoAPI(linhaExtrato{5, "-45.90", "01", "MERCADO X"})))
	resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	alvo := a.lancamentoPorDescricao(t, "2026-08", "MERCADO X")
	a.auditoria.entradas = nil

	corpo := fmt.Sprintf(`{"categoryId":%q}`, alimentacao.ID)
	referencia := a.patchCategoriaDoLancamento(t, "00000000-0000-7000-8000-000000000999", corpo)
	require.Equal(t, http.StatusNotFound, referencia.Code)

	// A vizinha com o id certo da minha linha e a categoria dela.
	daVizinha, err := a.categoriaSvc.Create(t.Context(), a.atorAlheioDeCategoria(), category.CreateInput{
		Name: "Da vizinha", Kind: category.KindExpense,
	})
	require.NoError(t, err)
	comoVizinha := a.patchCategoriaComo(t, a.alheia.ID, a.outroUsuario.ID, alvo.ID,
		fmt.Sprintf(`{"categoryId":%q}`, daVizinha.ID))
	assert.Equal(t, http.StatusNotFound, comoVizinha.Code, comoVizinha.Body.String())
	assert.Equal(t, referencia.Body.String(), comoVizinha.Body.String())

	// Eu, com o MEU id em caixa trocada (o id vem do banco com hexa
	// minúsculo): continua sendo outro id.
	maiusculo := strings.ToUpper(alvo.ID)
	require.NotEqual(t, alvo.ID, maiusculo, "o id precisa ter letras para o caso valer")
	emCaixaTrocada := a.patchCategoriaDoLancamento(t, maiusculo, corpo)
	assert.Equal(t, http.StatusNotFound, emCaixaTrocada.Code, emCaixaTrocada.Body.String())
	assert.Equal(t, referencia.Body.String(), emCaixaTrocada.Body.String())

	// E a categoria também: id da minha categoria em maiúscula é 404.
	comCategoriaEmCaixaTrocada := a.patchCategoriaDoLancamento(t, alvo.ID,
		fmt.Sprintf(`{"categoryId":%q}`, strings.ToUpper(alimentacao.ID)))
	assert.Equal(t, http.StatusNotFound, comCategoriaEmCaixaTrocada.Code)

	assert.Nil(t, a.lancamentoPorDescricao(t, "2026-08", "MERCADO X").CategoryID, "nada gravado")
	assert.Empty(t, a.acoesDeLancamento())
}

// §13 da spec 0005 — achado do QA de 17/09/2026, CORRIGIDO.
//
// A §12 diz que "um grupo com subcategorias ativas não recebe lançamento
// diretamente (o seletor só oferece folhas e grupos sem filhas)". Até a §13 o
// seletor era o único a sustentar isso: nem o `PATCH /transactions/{id}` da
// §11 nem o `POST /imports/{id}/confirm` da E2 conferiam se a categoria é
// atribuível, e um corpo montado à mão pendurava o lançamento no grupo.
//
// Este teste era de CARACTERIZAÇÃO (gravava o 200 de então) e virou o teste da
// regra: 422 em `fields.categoryId` nos TRÊS caminhos de escrita — o PATCH, a
// decisão do confirm e o `defaultCategoryId` —, e os dois casos que continuam
// válidos: grupo sem filhas recebe, e grupo cuja única filha foi ARQUIVADA
// volta a receber (é o que faz esta regra concordar com a da §12, que solta as
// palavras-chave do grupo pelo mesmo critério).
func TestCategoriaDeGrupoComFilhaAtivaEhRecusadaPeloServidor(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	ctx := t.Context()
	conta := a.contaCorrente(t)

	grupo, err := a.categoriaSvc.Create(ctx, a.atorDeCategoria(), category.CreateInput{
		Name: "Alimentação QA", Kind: category.KindExpense,
	})
	require.NoError(t, err)
	filha, err := a.categoriaSvc.Create(ctx, a.atorDeCategoria(), category.CreateInput{
		Name: "Restaurantes", ParentID: &grupo.ID,
	})
	require.NoError(t, err)
	// Grupo SEM filhas: o contraste que prova que a recusa é da filha ativa, e
	// não de "ser grupo".
	semFilhas, err := a.categoriaSvc.Create(ctx, a.atorDeCategoria(), category.CreateInput{
		Name: "Assinaturas QA", Kind: category.KindExpense,
	})
	require.NoError(t, err)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv",
		csvExtratoAPI(linhaExtrato{5, "-45.90", "01", "MERCADO X"})))
	resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	alvo := a.lancamentoPorDescricao(t, "2026-08", "MERCADO X")

	// (1) PATCH com o grupo: 422 no campo da categoria, nada gravado, nada
	// auditado — e a resposta não cita nome nem id de categoria nenhuma.
	rec := a.patchCategoriaDoLancamento(t, alvo.ID, fmt.Sprintf(`{"categoryId":%q}`, grupo.ID))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	var env httpserver.ErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
	assert.Equal(t, "Este grupo tem subcategorias. Escolha uma subcategoria.", env.Error.Fields["categoryId"])
	assert.NotContains(t, rec.Body.String(), "Alimentação")
	assert.NotContains(t, rec.Body.String(), grupo.ID)
	assert.Nil(t, a.lancamentoPorDescricao(t, "2026-08", "MERCADO X").CategoryID, "nada gravado")
	assert.Empty(t, a.acoesDeLancamento(), "recusa não audita escrita")

	// (2) Grupo sem filhas continua sendo destino legítimo.
	ok := a.patchCategoriaDoLancamento(t, alvo.ID, fmt.Sprintf(`{"categoryId":%q}`, semFilhas.ID))
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())
	assert.Equal(t, semFilhas.ID, texto(a.lancamentoPorDescricao(t, "2026-08", "MERCADO X").CategoryID))

	// (3) Decisão do confirm apontando o grupo: 422 e NADA importado — o lote
	// inteiro volta atrás, como em qualquer recusa do confirm.
	lote2 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "b.csv",
		csvExtratoAPI(linhaExtrato{6, "-33.10", "02", "MERCADO Y"})))
	linha := revisarPelaAPI(t, a, lote2.ID).Items[0]
	recDecisao := confirmarPelaAPI(t, a, lote2.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"import","categoryId":%q}]}`, linha.ID, grupo.ID))
	require.Equal(t, http.StatusUnprocessableEntity, recDecisao.Code, recDecisao.Body.String())
	env = httpserver.ErrorEnvelope{}
	require.NoError(t, json.Unmarshal(recDecisao.Body.Bytes(), &env))
	assert.Equal(t, "Este grupo tem subcategorias. Escolha uma subcategoria.", env.Error.Fields["categoryId"])
	assert.NotContains(t, recDecisao.Body.String(), grupo.ID)
	assert.False(t, a.existeLancamento(t, "2026-08", "MERCADO Y"), "nada importado")

	// (4) O mesmo pelo `defaultCategoryId`, que é o outro caminho da categoria
	// no confirm — e o que a tela usa no "aplicar a todas".
	recDefault := confirmarPelaAPI(t, a, lote2.ID, fmt.Sprintf(
		`{"decisions":[],"defaultCategoryId":%q}`, grupo.ID))
	require.Equal(t, http.StatusUnprocessableEntity, recDefault.Code, recDefault.Body.String())
	env = httpserver.ErrorEnvelope{}
	require.NoError(t, json.Unmarshal(recDefault.Body.Bytes(), &env))
	assert.Equal(t, "Este grupo tem subcategorias. Escolha uma subcategoria.", env.Error.Fields["categoryId"])
	assert.False(t, a.existeLancamento(t, "2026-08", "MERCADO Y"), "nada importado")

	// (5) Com a FILHA — o que o seletor oferece — o mesmo lote confirma.
	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote2.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"import","categoryId":%q}]}`, linha.ID, filha.ID)))
	assert.Equal(t, 1, res.Imported)
	assert.Equal(t, filha.ID, texto(a.lancamentoPorDescricao(t, "2026-08", "MERCADO Y").CategoryID))

	// (6) Filha ARQUIVADA não conta: o grupo volta a receber lançamento, pela
	// mesma regra que devolve as palavras-chave a ele na §12.
	_, err = a.categoriaSvc.Archive(ctx, a.atorDeCategoria(), filha.ID)
	require.NoError(t, err)
	depois := a.patchCategoriaDoLancamento(t, alvo.ID, fmt.Sprintf(`{"categoryId":%q}`, grupo.ID))
	require.Equal(t, http.StatusOK, depois.Code, depois.Body.String())
	assert.Equal(t, grupo.ID, texto(a.lancamentoPorDescricao(t, "2026-08", "MERCADO X").CategoryID))
}

// existeLancamento diz se a descrição aparece entre os lançamentos vivos do
// mês — o par de lancamentoPorDescricao para provar que NADA foi gravado.
func (a *ambiente) existeLancamento(t *testing.T, mes, descricao string) bool {
	t.Helper()
	for _, l := range a.lancamentosDa(t, a.casa.ID, mes) {
		if l.Description == descricao {
			return true
		}
	}
	return false
}

// A regra do servidor e a do classificador (internal/classify) concordam: a
// categoria que o `classify` NÃO sugere é exatamente a que a escrita recusa.
//
// O teste faz o caminho inteiro: a palavra «mercado» é gravada no GRUPO
// enquanto ele ainda não tem filha (a §12 permite), a importação SUGERE o
// grupo e o confirm aceita. Aí nasce a filha: a mesma palavra deixa de
// sugerir, e o mesmo corpo que antes passava vira 422. Se um dia as duas
// regras divergirem, é aqui que o desencontro aparece — uma sugestão que a
// escrita recusa seria uma tela que oferece o que o servidor nega.
func TestSugestaoDoClassificadorENaRegraDeEscritaConcordamSobreGrupo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	ctx := t.Context()
	conta := a.contaCorrente(t)

	grupo, err := a.categoriaSvc.Create(ctx, a.atorDeCategoria(), category.CreateInput{
		Name: "Alimentação QA", Kind: category.KindExpense, Keywords: []string{"mercado"},
	})
	require.NoError(t, err)

	// Sem filha: o grupo é sugerido E aceito.
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv",
		csvExtratoAPI(linhaExtrato{5, "-45.90", "01", "MERCADO X"})))
	require.Equal(t, grupo.ID, texto(revisarPelaAPI(t, a, lote.ID).Items[0].SuggestedCategoryID))
	resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	assert.Equal(t, grupo.ID, texto(a.lancamentoPorDescricao(t, "2026-08", "MERCADO X").CategoryID),
		"a sugestão aceita é gravada")

	// Com filha: para de sugerir (classify) e para de aceitar (escrita).
	_, err = a.categoriaSvc.Create(ctx, a.atorDeCategoria(), category.CreateInput{
		Name: "Restaurantes", ParentID: &grupo.ID,
	})
	require.NoError(t, err)

	lote2 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "b.csv",
		csvExtratoAPI(linhaExtrato{6, "-33.10", "02", "MERCADO Y"})))
	linha := revisarPelaAPI(t, a, lote2.ID).Items[0]
	assert.Nil(t, linha.SuggestedCategoryID, "grupo com filha ativa não é sugerido")

	recusa := confirmarPelaAPI(t, a, lote2.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"import","categoryId":%q}]}`, linha.ID, grupo.ID))
	assert.Equal(t, http.StatusUnprocessableEntity, recusa.Code, recusa.Body.String())

	// E o lançamento gravado ANTES, no grupo, continua onde está: a §13 não
	// migra nem altera o que já foi gravado.
	assert.Equal(t, grupo.ID, texto(a.lancamentoPorDescricao(t, "2026-08", "MERCADO X").CategoryID))
}

// --- §12 sobre o banco real -------------------------------------------------

// Grupo com subcategoria ATIVA recusa lista de palavras não vazia com 400 em
// `fields.keywords`; as palavras que ele já tinha continuam no GET (inertes) e
// `[]` continua limpando.
func TestGrupoComFilhaAtivaRecusaPalavrasNoBancoReal(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	ctx := t.Context()

	// O grupo ganha as palavras ANTES da filha — é o caso residual da §12.
	grupo, err := a.categoriaSvc.Create(ctx, a.atorDeCategoria(), category.CreateInput{
		Name: "Alimentação QA", Kind: category.KindExpense, Keywords: []string{"mercado", "padaria"},
	})
	require.NoError(t, err)
	filha, err := a.categoriaSvc.Create(ctx, a.atorDeCategoria(), category.CreateInput{
		Name: "Restaurantes", ParentID: &grupo.ID,
	})
	require.NoError(t, err, "criar a filha de um grupo com palavras não é recusado")

	// Lista não vazia: 400 no campo da LISTA, com a frase da spec.
	rec := a.patchPalavrasDaCategoria(t, grupo.ID, `{"keywords":["feira"]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var env httpserver.ErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
	assert.Equal(t, "Palavras-chave ficam nas subcategorias.", env.Error.Fields["keywords"])
	assert.NotContains(t, rec.Body.String(), "feira", "a resposta não ecoa a palavra")

	// As antigas continuam gravadas e VISÍVEIS — é a contagem que a nota do
	// diálogo usa ("2 palavras-chave sem efeito…").
	assert.Equal(t, []string{"mercado", "padaria"}, a.getCategoriaPelaAPI(t, grupo.ID).Keywords)

	// Inertes de verdade: o grupo com filha ativa não recebe lançamento, então
	// «mercado» nele não sugere nada na importação.
	conta := a.contaCorrente(t)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv",
		csvExtratoAPI(linhaExtrato{5, "-45.90", "01", "MERCADO X"})))
	assert.Nil(t, revisarPelaAPI(t, a, lote.ID).Items[0].SuggestedCategoryID,
		"palavra em grupo com filha ativa não sugere")

	// `[]` continua aceito — é o caminho para mover as palavras à mão...
	require.Equal(t, http.StatusOK, a.patchPalavrasDaCategoria(t, grupo.ID, `{"keywords":[]}`).Code)
	assert.Empty(t, a.getCategoriaPelaAPI(t, grupo.ID).Keywords)

	// ...e a palavra liberada entra na FILHA, onde passa a sugerir.
	require.Equal(t, http.StatusOK, a.patchPalavrasDaCategoria(t, filha.ID, `{"keywords":["mercado"]}`).Code)
	lote2 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a2.csv",
		csvExtratoAPI(linhaExtrato{6, "-45.90", "02", "MERCADO X"})))
	assert.Equal(t, filha.ID, texto(revisarPelaAPI(t, a, lote2.ID).Items[0].SuggestedCategoryID))
}
