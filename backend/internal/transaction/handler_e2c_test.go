package transaction_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Os testes de handler provam o CONTRATO de POST /transactions/auto-categorize
// e GET /transfers (spec 0005, plano E2c §1.2 e §9): status, código, campos em
// `fields`, corpo do 404 idêntico entre conta alheia e inexistente, cursor
// forjado sem detalhe, e — o mais importante — nada sensível no log.

// httpAmbiente liga o handler ao serviço com os mesmos dublês dos testes de
// serviço e captura o log.
type httpAmbiente struct {
	*ambiente
	handler *transaction.Handler
	logs    *bytes.Buffer
}

func novoHTTPAmbiente(t *testing.T, opts ...transaction.Option) *httpAmbiente {
	t.Helper()
	amb := novoAmbiente(t, opts...)
	logs := &bytes.Buffer{}
	lg := logging.New(logs, logging.Options{Level: "debug", Format: "json"})
	return &httpAmbiente{
		ambiente: amb,
		handler:  transaction.NewHandler(amb.svc, lg, 0),
		logs:     logs,
	}
}

// comIdentidade publica no contexto da requisição a identidade que o
// RequireAuth publica em produção — ou seja, a que veio do TOKEN ASSINADO. A
// casa NUNCA vem da URL nem do corpo.
//
// Está numa função porque mais de um construtor de requisição precisa dela, e
// duas cópias divergiriam — uma delas passando a aceitar a casa de outro lugar
// é exatamente o defeito que estes testes existem para pegar.
func comIdentidade(r *http.Request, casa string) *http.Request {
	return r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID: usuario, HouseholdID: casa, Role: "owner", SessionID: "sess-1",
	}))
}

// chamar monta a requisição já com a identidade no contexto.
func (a *httpAmbiente) chamar(t *testing.T, casa, metodo, alvo, corpo string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(metodo, alvo, strings.NewReader(corpo))
	if corpo != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if casa != "" {
		r = comIdentidade(r, casa)
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

func corpoDeErro(t *testing.T, rec *httptest.ResponseRecorder) (codigo string, campos map[string]string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Fields  map[string]string `json:"fields"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), "corpo: %s", rec.Body.String())
	return env.Error.Code, env.Error.Fields
}

// --- POST /transactions/auto-categorize -----------------------------------

func TestAutoCategorizeHandlerExigeDryRunExplicito(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transactions/auto-categorize", `{"month":"2026-09"}`, amb.handler.AutoCategorize)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeValidationFailed, codigo)
	assert.Contains(t, campos, "dryRun")
	assert.Empty(t, amb.auditor.registros, "dryRun ausente NUNCA grava")
}

func TestAutoCategorizeHandlerValidaOCorpo(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	casos := []struct {
		nome   string
		corpo  string
		status int
		campo  string
	}{
		{"mês inválido", `{"month":"2026-13","dryRun":true}`, http.StatusBadRequest, "month"},
		{"mês ausente", `{"dryRun":false}`, http.StatusBadRequest, "month"},
		{"dryRun com tipo errado", `{"month":"2026-09","dryRun":"sim"}`, http.StatusBadRequest, "dryRun"},
		{"campo desconhecido", `{"month":"2026-09","dryRun":true,"householdId":"x"}`, http.StatusBadRequest, ""},
		{"corpo vazio", ``, http.StatusUnsupportedMediaType, ""},
		{"dois objetos", `{"month":"2026-09","dryRun":true}{}`, http.StatusBadRequest, ""},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transactions/auto-categorize", c.corpo, amb.handler.AutoCategorize)
			require.Equal(t, c.status, rec.Code, rec.Body.String())
			if c.campo != "" {
				_, campos := corpoDeErro(t, rec)
				assert.Contains(t, campos, c.campo)
			}
		})
	}
	assert.Empty(t, amb.auditor.registros)
}

func TestAutoCategorizeHandlerSemSessaoResponde401(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	rec := amb.chamar(t, "", http.MethodPost, "/transactions/auto-categorize", `{"month":"2026-09","dryRun":false}`, amb.handler.AutoCategorize)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAutoCategorizeHandlerPreviaEGravacaoSeguemOContrato(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-1", "Mercado", category.KindExpense)
	amb.palavraDeCategoria(minhaCasa, "cat-1", "supermercado")
	casou := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado Segredo 42", 150_07, 3)
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria Sigilosa", 12_34, 4)

	previa := amb.chamar(t, minhaCasa, http.MethodPost, "/transactions/auto-categorize", `{"month":"2026-09","dryRun":true}`, amb.handler.AutoCategorize)
	require.Equal(t, http.StatusOK, previa.Code, previa.Body.String())
	var corpoPrevia struct {
		Month          string            `json:"month"`
		Categorized    int               `json:"categorized"`
		Unmatched      int               `json:"unmatched"`
		Items          []json.RawMessage `json:"items"`
		UnmatchedItems []json.RawMessage `json:"unmatchedItems"`
	}
	require.NoError(t, json.Unmarshal(previa.Body.Bytes(), &corpoPrevia))
	assert.Equal(t, "2026-09", corpoPrevia.Month)
	assert.Equal(t, 1, corpoPrevia.Categorized)
	assert.Equal(t, 1, corpoPrevia.Unmatched)
	require.Len(t, corpoPrevia.Items, 1)
	require.Len(t, corpoPrevia.UnmatchedItems, 1)
	assert.Nil(t, amb.repo.linhas[casou.ID].CategoryID, "a prévia não gravou")

	real := amb.chamar(t, minhaCasa, http.MethodPost, "/transactions/auto-categorize", `{"month":"2026-09","dryRun":false}`, amb.handler.AutoCategorize)
	require.Equal(t, http.StatusOK, real.Code, real.Body.String())
	// Listas VAZIAS (`[]`), nunca nulas.
	assert.Contains(t, real.Body.String(), `"items":[]`)
	assert.Contains(t, real.Body.String(), `"unmatchedItems":[]`)
	require.NotNil(t, amb.repo.linhas[casou.ID].CategoryID)
	assert.Equal(t, "cat-1", *amb.repo.linhas[casou.ID].CategoryID)

	// S8: o log da execução real tem request_id e contagens — nunca a
	// descrição, a palavra-chave, o valor ou o id do lançamento.
	log := amb.logs.String()
	assert.Contains(t, log, `"categorized":1`)
	assert.Contains(t, log, `"unmatched":1`)
	for _, proibido := range []string{"Segredo", "Sigilosa", "supermercado", "15007", "1234", casou.ID} {
		assert.NotContains(t, log, proibido, "vazou no log: %q", proibido)
	}
}

func TestAutoCategorizeHandlerRecusaMesAcimaDoTetoCom422(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	for i := 0; i <= transaction.MaxAutoCategorizeRows; i++ {
		amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "x", 1, 1)
	}
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transactions/auto-categorize", `{"month":"2026-09","dryRun":true}`, amb.handler.AutoCategorize)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeValidationFailed, codigo)
	assert.Contains(t, campos, "month")
	assert.NotContains(t, campos["month"], "10001", "a contagem do mês é dado da casa")
}

// --- GET /transfers -------------------------------------------------------

func TestListTransfersHandlerRespondeConformeOContrato(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "Nubank Secreto", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "C6", account.KindChecking)
	amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 250_00, 5, "g-1")

	rec := amb.chamar(t, minhaCasa, http.MethodGet, "/transfers?month=2026-09&limit=10", "", amb.handler.ListTransfers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var corpo struct {
		Items      []map[string]any `json:"items"`
		Pairs      []map[string]any `json:"pairs"`
		Balances   []map[string]any `json:"balances"`
		NextCursor *string          `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
	require.Len(t, corpo.Items, 1)
	require.Len(t, corpo.Pairs, 1)
	require.Len(t, corpo.Balances, 2)
	assert.Nil(t, corpo.NextCursor)
	assert.Contains(t, rec.Body.String(), `"nextCursor":null`, "nextCursor está SEMPRE presente")
	assert.Equal(t, "2026-09-05", corpo.Items[0]["occurredOn"])
	assert.EqualValues(t, 250_00, corpo.Items[0]["amountCents"])

	// Mês vazio: os três arrays vêm vazios, nunca nulos.
	vazio := amb.chamar(t, minhaCasa, http.MethodGet, "/transfers?month=2026-01", "", amb.handler.ListTransfers)
	require.Equal(t, http.StatusOK, vazio.Code)
	assert.Contains(t, vazio.Body.String(), `"items":[]`)
	assert.Contains(t, vazio.Body.String(), `"pairs":[]`)
	assert.Contains(t, vazio.Body.String(), `"balances":[]`)

	// Nada da resposta vai para o log de uma leitura bem-sucedida.
	assert.NotContains(t, amb.logs.String(), "Secreto")
	assert.NotContains(t, amb.logs.String(), "25000")
}

func TestListTransfersHandlerContaAlheiaE404IgualAoInexistente(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "A", account.KindChecking)
	amb.conta(outraCasa, "acc-x", "X", account.KindChecking)

	alheia := amb.chamar(t, minhaCasa, http.MethodGet, "/transfers?month=2026-09&accountId=acc-x", "", amb.handler.ListTransfers)
	inexistente := amb.chamar(t, minhaCasa, http.MethodGet, "/transfers?month=2026-09&accountId=00000000-0000-7000-8000-999999999999", "", amb.handler.ListTransfers)
	require.Equal(t, http.StatusNotFound, alheia.Code, alheia.Body.String())
	require.Equal(t, http.StatusNotFound, inexistente.Code)
	assert.Equal(t, inexistente.Body.String(), alheia.Body.String(), "corpo byte a byte igual (S1)")
	assert.Equal(t, inexistente.Header(), alheia.Header())

	contraparte := amb.chamar(t, minhaCasa, http.MethodGet, "/transfers?month=2026-09&accountId=acc-a&counterpartAccountId=acc-x", "", amb.handler.ListTransfers)
	require.Equal(t, http.StatusNotFound, contraparte.Code)
	assert.Equal(t, inexistente.Body.String(), contraparte.Body.String())
}

func TestListTransfersHandlerValidaQuery(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "A", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "B", account.KindChecking)

	casos := []struct {
		nome   string
		query  string
		status int
		campo  string
	}{
		{"sem mês", "", http.StatusBadRequest, "month"},
		{"mês inválido", "month=2026-13", http.StatusBadRequest, "month"},
		{"contraparte sem conta", "month=2026-09&counterpartAccountId=acc-b", http.StatusBadRequest, "counterpartAccountId"},
		{"conta igual à contraparte", "month=2026-09&accountId=acc-a&counterpartAccountId=acc-a", http.StatusBadRequest, "counterpartAccountId"},
		{"limit acima do teto", "month=2026-09&limit=101", http.StatusBadRequest, "limit"},
		{"limit zero", "month=2026-09&limit=0", http.StatusBadRequest, "limit"},
		{"limit não numérico", "month=2026-09&limit=abc", http.StatusBadRequest, "limit"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			rec := amb.chamar(t, minhaCasa, http.MethodGet, "/transfers?"+c.query, "", amb.handler.ListTransfers)
			require.Equal(t, c.status, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			assert.Contains(t, campos, c.campo)
		})
	}

	t.Run("cursor forjado é 400 SEM detalhe", func(t *testing.T) {
		forjado := base64.RawURLEncoding.EncodeToString([]byte("occ=2026-09-01|id=' OR 1=1"))
		rec := amb.chamar(t, minhaCasa, http.MethodGet, "/transfers?month=2026-09&cursor="+forjado, "", amb.handler.ListTransfers)
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		codigo, campos := corpoDeErro(t, rec)
		assert.Equal(t, httpserver.CodeValidationFailed, codigo)
		assert.Empty(t, campos, "explicar por que o cursor não serve é ensinar a forjá-lo (S5)")
		assert.NotContains(t, rec.Body.String(), "cursor")
	})

	t.Run("sem sessão", func(t *testing.T) {
		rec := amb.chamar(t, "", http.MethodGet, "/transfers?month=2026-09", "", amb.handler.ListTransfers)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

// --- PATCH /transactions/{id} (emenda §11) --------------------------------

// patchCategoria chama o handler com o id no path, como o ServeMux faz.
func (a *httpAmbiente) patchCategoria(t *testing.T, casa, id, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPatch, "/transactions/"+id, strings.NewReader(corpo))
	if corpo != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.SetPathValue("id", id)
	if casa != "" {
		r = r.WithContext(session.NewContext(r.Context(), session.Identity{
			UserID: usuario, HouseholdID: casa, Role: "owner", SessionID: "sess-1",
		}))
	}
	rec := httptest.NewRecorder()
	a.handler.UpdateCategory(rec, r)
	return rec
}

func TestUpdateCategoryHandlerRespondeOLancamentoAtualizado(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta Sigilosa", account.KindChecking)
	amb.categoria(minhaCasa, catMercado, "Mercado", category.KindExpense)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado Segredo 42", 150_07, 3)
	outro := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria Sigilosa", 12_34, 4)

	rec := amb.patchCategoria(t, minhaCasa, alvo.ID, `{"categoryId":"`+catMercado+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var corpo struct {
		ID           string  `json:"id"`
		CategoryID   *string `json:"categoryId"`
		CategoryName *string `json:"categoryName"`
		AmountCents  int64   `json:"amountCents"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
	assert.Equal(t, alvo.ID, corpo.ID)
	require.NotNil(t, corpo.CategoryID)
	assert.Equal(t, catMercado, *corpo.CategoryID)
	require.NotNil(t, corpo.CategoryName)
	assert.Equal(t, "Mercado", *corpo.CategoryName)
	assert.EqualValues(t, 150_07, corpo.AmountCents)

	// Critério (b): nenhum outro lançamento mudou.
	assert.Nil(t, amb.repo.linhas[outro.ID].CategoryID)

	// Auditoria com o id — e S8: o log de um PATCH bem-sucedido não tem
	// descrição, valor, nome de conta nem categoria.
	require.Len(t, amb.auditor.registros, 1)
	assert.Equal(t, alvo.ID, amb.auditor.registros[0].EntityID)
	log := amb.logs.String()
	for _, proibido := range []string{"Segredo", "Sigilosa", "15007", "1234", "Mercado", catMercado} {
		assert.NotContains(t, log, proibido, "vazou no log: %q", proibido)
	}
}

func TestUpdateCategoryHandlerValidaOCorpo(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, catMercado, "Mercado", category.KindExpense)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)

	casos := []struct {
		nome   string
		corpo  string
		status int
		campo  string
	}{
		{"categoryId ausente", `{}`, http.StatusBadRequest, "categoryId"},
		{"categoryId vazio", `{"categoryId":""}`, http.StatusBadRequest, "categoryId"},
		{"categoryId nulo", `{"categoryId":null}`, http.StatusBadRequest, "categoryId"},
		{"categoryId fora da forma de UUID", `{"categoryId":"cat-1"}`, http.StatusBadRequest, "categoryId"},
		{"categoryId com tipo errado", `{"categoryId":1}`, http.StatusBadRequest, "categoryId"},
		// Critério (a): campo além de categoryId é 400 — o PATCH completo é
		// da E2b, e mass assignment não passa (S2).
		{"campo desconhecido: amountCents", `{"categoryId":"` + catMercado + `","amountCents":1}`, http.StatusBadRequest, ""},
		{"campo desconhecido: description", `{"categoryId":"` + catMercado + `","description":"x"}`, http.StatusBadRequest, ""},
		{"campo desconhecido: householdId", `{"categoryId":"` + catMercado + `","householdId":"x"}`, http.StatusBadRequest, ""},
		{"campo desconhecido: accountId", `{"categoryId":"` + catMercado + `","accountId":"x"}`, http.StatusBadRequest, ""},
		{"JSON malformado", `{"categoryId":`, http.StatusBadRequest, ""},
		{"dois objetos", `{"categoryId":"` + catMercado + `"}{}`, http.StatusBadRequest, ""},
		{"corpo vazio (sem content-type)", ``, http.StatusUnsupportedMediaType, ""},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			rec := amb.patchCategoria(t, minhaCasa, alvo.ID, c.corpo)
			require.Equal(t, c.status, rec.Code, rec.Body.String())
			if c.campo != "" {
				codigo, campos := corpoDeErro(t, rec)
				assert.Equal(t, httpserver.CodeValidationFailed, codigo)
				assert.Contains(t, campos, c.campo)
			}
		})
	}
	assert.Nil(t, amb.repo.linhas[alvo.ID].CategoryID, "entrada recusada NUNCA grava")
	assert.Empty(t, amb.auditor.registros)
}

// Critério (a): 404 de lançamento alheio é BYTE A BYTE igual ao inexistente,
// e o mesmo vale para categoria alheia — nada é gravado em nenhum dos casos.
func TestUpdateCategoryHandler404AlheioIgualAoInexistente(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(outraCasa, "acc-x", "Alheia", account.KindChecking)
	amb.categoria(minhaCasa, catMercado, "Mercado", category.KindExpense)
	amb.categoria(outraCasa, catDaVizinha, "Da vizinha", category.KindExpense)
	minha := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Minha", 10_00, 3)
	daVizinha := amb.lancamento(outraCasa, "acc-x", transaction.KindExpense, "Da vizinha", 99_00, 3)

	corpo := `{"categoryId":"` + catMercado + `"}`
	inexistente := amb.patchCategoria(t, minhaCasa, "00000000-0000-7000-8000-000000000999", corpo)
	alheio := amb.patchCategoria(t, minhaCasa, daVizinha.ID, corpo)
	require.Equal(t, http.StatusNotFound, inexistente.Code, inexistente.Body.String())
	require.Equal(t, http.StatusNotFound, alheio.Code, alheio.Body.String())
	assert.Equal(t, inexistente.Body.String(), alheio.Body.String(), "corpo byte a byte igual (S1)")
	assert.Equal(t, inexistente.Header(), alheio.Header())

	categoriaAlheia := amb.patchCategoria(t, minhaCasa, minha.ID, `{"categoryId":"`+catDaVizinha+`"}`)
	categoriaFantasma := amb.patchCategoria(t, minhaCasa, minha.ID, `{"categoryId":"`+catFantasma+`"}`)
	require.Equal(t, http.StatusNotFound, categoriaAlheia.Code, categoriaAlheia.Body.String())
	require.Equal(t, http.StatusNotFound, categoriaFantasma.Code)
	assert.Equal(t, inexistente.Body.String(), categoriaAlheia.Body.String())
	assert.Equal(t, inexistente.Body.String(), categoriaFantasma.Body.String())

	assert.Nil(t, amb.repo.linhas[minha.ID].CategoryID, "categoria alheia não gravou")
	assert.Nil(t, amb.repo.linhas[daVizinha.ID].CategoryID, "o lançamento da vizinha continua como estava")
	assert.Empty(t, amb.auditor.registros)
	assert.NotContains(t, amb.logs.String(), "Da vizinha")
}

// Critério (a): os três 422 da emenda, com o campo certo e nada gravado.
func TestUpdateCategoryHandlerRegrasDeNegocioRespondem422(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-2", "Poupança", account.KindChecking)
	amb.categoria(minhaCasa, catMercado, "Mercado", category.KindExpense)
	amb.categoria(minhaCasa, catSalario, "Salário", category.KindIncome)
	arquivada := amb.categoria(minhaCasa, catArquivada, "Antiga", category.KindExpense)
	arquivada.ArchivedAt = ptr(agora)
	amb.categorias.add(arquivada)
	despesa := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)
	receita := amb.lancamento(minhaCasa, "acc-1", transaction.KindIncome, "Salário", 5_000_00, 5)
	saida, entrada := amb.parDeTransferencia(minhaCasa, "acc-1", "acc-2", 250_00, 6, "g-1")

	casos := []struct {
		nome      string
		id        string
		categoria string
		campo     string
	}{
		{"perna de saída", saida.ID, catMercado, "id"},
		{"perna de entrada", entrada.ID, catMercado, "id"},
		{"receita com categoria de despesa", receita.ID, catMercado, "categoryId"},
		{"despesa com categoria de receita", despesa.ID, catSalario, "categoryId"},
		{"categoria arquivada", despesa.ID, catArquivada, "categoryId"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			rec := amb.patchCategoria(t, minhaCasa, c.id, `{"categoryId":"`+c.categoria+`"}`)
			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			assert.Contains(t, campos, c.campo)
		})
	}
	for id, linha := range amb.repo.linhas {
		assert.Nil(t, linha.CategoryID, "linha %s foi tocada", id)
	}
	assert.Empty(t, amb.auditor.registros)
}

func TestUpdateCategoryHandlerSemSessaoResponde401(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, catMercado, "Mercado", category.KindExpense)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)

	rec := amb.patchCategoria(t, "", alvo.ID, `{"categoryId":"`+catMercado+`"}`)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Nil(t, amb.repo.linhas[alvo.ID].CategoryID)
}

// --- contrato: campos `required` e nenhum fora do schema ------------------

// schemasDoContrato lê required e properties de components.schemas.
func schemasDoContrato(t *testing.T) map[string]struct {
	Required   []string             `yaml:"required"`
	Properties map[string]yaml.Node `yaml:"properties"`
} {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi.yaml")
	require.NoError(t, err)
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Required   []string             `yaml:"required"`
				Properties map[string]yaml.Node `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	return doc.Components.Schemas
}

func chavesDoObjeto(t *testing.T, bruto []byte) []string {
	t.Helper()
	var objeto map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(bruto, &objeto), "corpo: %s", string(bruto))
	out := make([]string, 0, len(objeto))
	for chave := range objeto {
		out = append(out, chave)
	}
	sort.Strings(out)
	return out
}

// conferirContrato exige TODOS os `required` do schema e NENHUM campo fora de
// `properties` (additionalProperties: false) — o mesmo cuidado de
// account.TestRespostaDeContaTemTodosOsCamposRequiredDoContrato, com a lista
// lida da spec para o teste envelhecer junto com ela.
func conferirContrato(t *testing.T, schemas map[string]struct {
	Required   []string             `yaml:"required"`
	Properties map[string]yaml.Node `yaml:"properties"`
}, nome string, bruto []byte) {
	t.Helper()
	esquema, ok := schemas[nome]
	require.True(t, ok, "o schema %s precisa existir na spec", nome)
	require.NotEmpty(t, esquema.Required, nome)
	chaves := chavesDoObjeto(t, bruto)
	for _, campo := range esquema.Required {
		assert.Contains(t, chaves, campo, "%s: campo `required` ausente na resposta", nome)
	}
	for _, chave := range chaves {
		assert.Contains(t, esquema.Properties, chave, "%s: a resposta publica um campo que o contrato não declara", nome)
	}
}

func TestRespostaDeTransferListTemTodosOsCamposRequiredDoContrato(t *testing.T) {
	t.Parallel()

	schemas := schemasDoContrato(t)
	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "A", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "B", account.KindChecking)
	amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 250_00, 5, "g-1")

	rec := amb.chamar(t, minhaCasa, http.MethodGet, "/transfers?month=2026-09&accountId=acc-a&counterpartAccountId=acc-b", "", amb.handler.ListTransfers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	conferirContrato(t, schemas, "TransferList", rec.Body.Bytes())

	var corpo struct {
		Items    []json.RawMessage `json:"items"`
		Pairs    []json.RawMessage `json:"pairs"`
		Balances []json.RawMessage `json:"balances"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
	require.Len(t, corpo.Items, 1)
	require.Len(t, corpo.Pairs, 1)
	require.Len(t, corpo.Balances, 2)
	conferirContrato(t, schemas, "TransferItem", corpo.Items[0])
	conferirContrato(t, schemas, "TransferPair", corpo.Pairs[0])
	conferirContrato(t, schemas, "TransferBalance", corpo.Balances[0])
}

func TestRespostaDeAutoCategorizeResultTemTodosOsCamposRequiredDoContrato(t *testing.T) {
	t.Parallel()

	schemas := schemasDoContrato(t)
	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-1", "Mercado", category.KindExpense)
	amb.palavraDeCategoria(minhaCasa, "cat-1", "supermercado")
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 12_00, 4)

	previa := amb.chamar(t, minhaCasa, http.MethodPost, "/transactions/auto-categorize", `{"month":"2026-09","dryRun":true}`, amb.handler.AutoCategorize)
	require.Equal(t, http.StatusOK, previa.Code, previa.Body.String())
	conferirContrato(t, schemas, "AutoCategorizeResult", previa.Body.Bytes())

	var corpo struct {
		Items          []json.RawMessage `json:"items"`
		UnmatchedItems []json.RawMessage `json:"unmatchedItems"`
	}
	require.NoError(t, json.Unmarshal(previa.Body.Bytes(), &corpo))
	require.Len(t, corpo.Items, 1)
	require.Len(t, corpo.UnmatchedItems, 1)
	conferirContrato(t, schemas, "AutoCategorizeItem", corpo.Items[0])
	conferirContrato(t, schemas, "AutoCategorizeUnmatchedItem", corpo.UnmatchedItems[0])

	real := amb.chamar(t, minhaCasa, http.MethodPost, "/transactions/auto-categorize", `{"month":"2026-09","dryRun":false}`, amb.handler.AutoCategorize)
	require.Equal(t, http.StatusOK, real.Code, real.Body.String())
	conferirContrato(t, schemas, "AutoCategorizeResult", real.Body.Bytes())
}

// A resposta do PATCH é o schema Transaction do contrato — o MESMO de GET —,
// com todos os `required` e nenhum campo a mais (dedupKey, externalId,
// householdId e descriptionNorm continuam fora).
func TestRespostaDeUpdateCategoryTemTodosOsCamposRequiredDoContrato(t *testing.T) {
	t.Parallel()

	schemas := schemasDoContrato(t)
	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, catMercado, "Mercado", category.KindExpense)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)

	rec := amb.patchCategoria(t, minhaCasa, alvo.ID, `{"categoryId":"`+catMercado+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	conferirContrato(t, schemas, "Transaction", rec.Body.Bytes())

	// A resposta do PATCH é idêntica à de GET logo depois.
	get := amb.chamar(t, minhaCasa, http.MethodGet, "/transactions/"+alvo.ID, "", func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("id", alvo.ID)
		amb.handler.Get(w, r)
	})
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	assert.JSONEq(t, get.Body.String(), rec.Body.String())
}

// O corpo do PATCH aceita EXATAMENTE os campos do contrato: o schema declara
// só categoryId, e qualquer outro nome é 400 (DisallowUnknownFields).
func TestUpdateTransactionCategoryRequestAceitaExatamenteOsCamposDoContrato(t *testing.T) {
	t.Parallel()

	schemas := schemasDoContrato(t)
	esquema, ok := schemas["UpdateTransactionCategoryRequest"]
	require.True(t, ok, "o schema precisa existir na spec")
	require.ElementsMatch(t, []string{"categoryId"}, esquema.Required)
	require.Len(t, esquema.Properties, 1, "a emenda §11 aceita um campo só")

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, catMercado, "Mercado", category.KindExpense)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)
	for campo := range esquema.Properties {
		corpo := fmt.Sprintf(`{"categoryId":%q,%q:1}`, catMercado, campo+"X")
		rec := amb.patchCategoria(t, minhaCasa, alvo.ID, corpo)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "campo fora do contrato precisa ser 400")
	}
	assert.Nil(t, amb.repo.linhas[alvo.ID].CategoryID)
}

// O corpo da requisição do auto-categorize também segue o contrato: os
// campos aceitos são exatamente os declarados (DisallowUnknownFields).
func TestAutoCategorizeRequestAceitaExatamenteOsCamposDoContrato(t *testing.T) {
	t.Parallel()

	schemas := schemasDoContrato(t)
	esquema := schemas["AutoCategorizeRequest"]
	require.ElementsMatch(t, []string{"month", "dryRun"}, esquema.Required)
	amb := novoHTTPAmbiente(t)
	for campo := range esquema.Properties {
		corpo := fmt.Sprintf(`{"month":"2026-09","dryRun":true,%q:1}`, campo+"X")
		rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transactions/auto-categorize", corpo, amb.handler.AutoCategorize)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "campo fora do contrato precisa ser 400")
	}
}
