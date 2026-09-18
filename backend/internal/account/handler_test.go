package account_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Os testes de handler existem para provar o CONTRATO: status, código de erro,
// campos em `fields`, e — o mais importante — que id de outra casa responde
// 404 em TODAS as rotas com {id}.

type ambiente struct {
	handler *account.Handler
	repo    *repoFake
	svc     *account.Service
}

func novoAmbiente(t *testing.T, opts ...account.Option) *ambiente {
	t.Helper()
	repo := novoRepo()
	svc := novoServico(t, repo, opts...)
	return &ambiente{handler: account.NewHandler(svc, logging.Discard(), 0), repo: repo, svc: svc}
}

// chamar monta a requisição já com a identidade no contexto — que é o que o
// RequireAuth faz em produção. A casa NUNCA vem da URL nem do corpo.
func (a *ambiente) chamar(t *testing.T, casa, metodo, alvo, corpo string, h http.HandlerFunc, pathID string) *httptest.ResponseRecorder {
	t.Helper()

	var body *strings.Reader
	if corpo == "" {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(corpo)
	}
	r := httptest.NewRequest(metodo, alvo, body)
	if corpo != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if pathID != "" {
		r.SetPathValue("id", pathID)
	}
	r = r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID:      "user-1",
		HouseholdID: casa,
		Role:        "owner",
		SessionID:   "sess-1",
	}))

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

const corpoValido = `{"name":"Conta Corrente","kind":"checking","openingBalanceCents":15000,"openingDate":"2026-09-01"}`

func TestCriarDevolve201ComORecurso(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpoValido, amb.handler.Create, "")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var v account.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.NotEmpty(t, v.ID)
	assert.Equal(t, "Conta Corrente", v.Name)
	assert.EqualValues(t, 15000, v.BalanceCents)
	assert.Equal(t, "2026-09-01", v.OpeningDate.String())
}

// S2 — mass assignment. Estes campos são DERIVADOS no servidor, e o decoder
// roda com DisallowUnknownFields: mandá-los não é ignorado em silêncio, é 400.
// Silêncio seria pior do que o erro: quem tentou continuaria achando que
// conseguiu.
func TestCampoDerivadoNoCorpoEhRecusado(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	proibidos := map[string]string{
		"householdId":  `{"name":"X","kind":"cash","openingBalanceCents":0,"openingDate":"2026-01-01","householdId":"casa-2"}`,
		"id":           `{"name":"X","kind":"cash","openingBalanceCents":0,"openingDate":"2026-01-01","id":"forjado"}`,
		"balanceCents": `{"name":"X","kind":"cash","openingBalanceCents":0,"openingDate":"2026-01-01","balanceCents":999999}`,
		"archivedAt":   `{"name":"X","kind":"cash","openingBalanceCents":0,"openingDate":"2026-01-01","archivedAt":null}`,
		"nameNorm":     `{"name":"X","kind":"cash","openingBalanceCents":0,"openingDate":"2026-01-01","nameNorm":"y"}`,
		"createdAt":    `{"name":"X","kind":"cash","openingBalanceCents":0,"openingDate":"2026-01-01","createdAt":"2020-01-01T00:00:00Z"}`,
	}
	for campo, corpo := range proibidos {
		rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpo, amb.handler.Create, "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, "campo %q deveria ser recusado", campo)
	}
	assert.Zero(t, amb.repo.criadas, "nenhuma delas pode ter criado conta")
}

// O teste que o critério de aceite 3 da spec 0003 exige, em TODAS as rotas com
// {id}. Um 403 aqui seria uma regressão de segurança: ele confirmaria que o
// recurso existe em alguma casa.
func TestRecursoDeOutraCasaResponde404EmTodasAsRotas(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	// Cria de verdade na outra casa, para que o id EXISTA.
	daOutra, err := amb.svc.Create(t.Context(), ator(outraCasa), entradaValida())
	require.NoError(t, err)

	rotas := []struct {
		nome    string
		metodo  string
		corpo   string
		handler http.HandlerFunc
	}{
		{"GET", http.MethodGet, "", amb.handler.Get},
		{"PATCH", http.MethodPatch, `{"name":"Sequestrada"}`, amb.handler.Update},
		{"DELETE", http.MethodDelete, "", amb.handler.Delete},
		{"archive", http.MethodPost, "", amb.handler.Archive},
		{"unarchive", http.MethodPost, "", amb.handler.Unarchive},
	}
	for _, rota := range rotas {
		rec := amb.chamar(t, minhaCasa, rota.metodo, "/accounts/"+daOutra.ID, rota.corpo, rota.handler, daOutra.ID)
		require.Equal(t, http.StatusNotFound, rec.Code, "rota %s", rota.nome)

		codigo, _ := corpoDeErro(t, rec)
		assert.Equal(t, "NOT_FOUND", codigo, "rota %s", rota.nome)
		assert.NotContains(t, rec.Body.String(), "Conta Corrente",
			"a resposta não pode devolver nada do recurso alheio")
	}

	// E a conta continua intacta na casa dela.
	intacta, err := amb.svc.Get(t.Context(), ator(outraCasa), daOutra.ID)
	require.NoError(t, err)
	assert.Equal(t, "Conta Corrente", intacta.Name)
	assert.Nil(t, intacta.ArchivedAt)
}

// Id inexistente e id malformado precisam responder IGUAL ao id de outra casa.
// Se respondessem diferente, a diferença seria o oráculo.
func TestIdInexistenteOuMalformadoRespondeIgualAoDeOutraCasa(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	daOutra, err := amb.svc.Create(t.Context(), ator(outraCasa), entradaValida())
	require.NoError(t, err)

	referencia := amb.chamar(t, minhaCasa, http.MethodGet, "/accounts/x", "", amb.handler.Get, daOutra.ID)

	for _, id := range []string{
		"nao-existe",
		"",
		"../../etc/passwd",
		"' OR 1=1--",
		strings.Repeat("a", 500),
		"00000000-0000-0000-0000-000000000000",
	} {
		rec := amb.chamar(t, minhaCasa, http.MethodGet, "/accounts/x", "", amb.handler.Get, id)
		assert.Equal(t, referencia.Code, rec.Code, "id %q", id)
		assert.JSONEq(t, referencia.Body.String(), rec.Body.String(), "id %q", id)
	}
}

func TestValidacaoDevolve400ComOCampoCulpado(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	casos := []struct {
		nome       string
		corpo      string
		campo      string
		statusCode int
	}{
		{"nome vazio", `{"name":"  ","kind":"cash","openingBalanceCents":0,"openingDate":"2026-01-01"}`, "name", http.StatusBadRequest},
		{"nome gigante", fmt.Sprintf(`{"name":%q,"kind":"cash","openingBalanceCents":0,"openingDate":"2026-01-01"}`, strings.Repeat("a", 1000)), "name", http.StatusBadRequest},
		{"tipo inválido", `{"name":"X","kind":"cripto","openingBalanceCents":0,"openingDate":"2026-01-01"}`, "kind", http.StatusBadRequest},
		{"valor absurdo", `{"name":"X","kind":"cash","openingBalanceCents":9999999999999,"openingDate":"2026-01-01"}`, "openingBalanceCents", http.StatusBadRequest},
		{"data impossível", `{"name":"X","kind":"cash","openingBalanceCents":0,"openingDate":"2026-02-30"}`, "openingDate", http.StatusBadRequest},
		{"data zerada", `{"name":"X","kind":"cash","openingBalanceCents":0,"openingDate":"0000-00-00"}`, "openingDate", http.StatusBadRequest},
		{"data no futuro distante", `{"name":"X","kind":"cash","openingBalanceCents":0,"openingDate":"2099-01-01"}`, "openingDate", http.StatusBadRequest},
	}
	for _, caso := range casos {
		rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", caso.corpo, amb.handler.Create, "")
		require.Equal(t, caso.statusCode, rec.Code, "caso %q: %s", caso.nome, rec.Body.String())

		codigo, campos := corpoDeErro(t, rec)
		assert.Equal(t, "VALIDATION_FAILED", codigo, "caso %q", caso.nome)
		assert.Contains(t, campos, caso.campo, "caso %q", caso.nome)
	}
}

// "0000-00-00" e "2026-02-30" precisam morrer na desserialização, ANTES de
// virarem uma data zero silenciosa — o MySQL recusa data zero com erro 1292, e
// descobrir isso só em produção seria caro.
func TestDataMalformadaMorreNaBorda(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	for _, data := range []string{"0000-00-00", "2026-02-30", "2026-13-01", "12/09/2026", "2026-9-1", "hoje", ""} {
		corpo := fmt.Sprintf(`{"name":"X","kind":"cash","openingBalanceCents":0,"openingDate":%q}`, data)
		rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpo, amb.handler.Create, "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, "data %q", data)
	}
	assert.Zero(t, amb.repo.criadas)
}

func TestNomeDuplicadoDevolve422(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpoValido, amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code)

	rec = amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpoValido, amb.handler.Create, "")
	// 422, e não 400: o corpo está correto, quem recusa é a regra de negócio.
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, "VALIDATION_FAILED", codigo)
	assert.Contains(t, campos, "name")
}

func TestExclusaoDeContaEmUsoDevolve422ComCodigoProprio(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t, account.WithUsageCheckers(usoFake{emUso: true}))
	criada, err := amb.svc.Create(t.Context(), ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	rec := amb.chamar(t, minhaCasa, http.MethodDelete, "/accounts/"+criada.ID, "", amb.handler.Delete, criada.ID)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	// Código PRÓPRIO: é o que deixa a tela oferecer "arquivar" em vez de
	// mandar corrigir um formulário que está certo (D4 da spec 0003).
	codigo, _ := corpoDeErro(t, rec)
	assert.Equal(t, "RESOURCE_IN_USE", codigo)
}

func TestExclusaoBemSucedidaDevolve204SemCorpo(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	criada, err := amb.svc.Create(t.Context(), ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	rec := amb.chamar(t, minhaCasa, http.MethodDelete, "/accounts/"+criada.ID, "", amb.handler.Delete, criada.ID)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, rec.Body.String())
}

func TestListagemRecusaFiltroBooleanoInvalido(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	// Aceitos.
	for _, valor := range []string{"", "true", "false", "1", "0"} {
		alvo := "/accounts"
		if valor != "" {
			alvo += "?includeArchived=" + valor
		}
		rec := amb.chamar(t, minhaCasa, http.MethodGet, alvo, "", amb.handler.List, "")
		assert.Equal(t, http.StatusOK, rec.Code, "valor %q", valor)
	}

	// Recusados — tratar "sim" como false faria a tela mostrar um conjunto
	// diferente do pedido, sem ninguém notar.
	for _, valor := range []string{"sim", "yes", "2", "verdadeiro"} {
		rec := amb.chamar(t, minhaCasa, http.MethodGet, "/accounts?includeArchived="+valor, "", amb.handler.List, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, "valor %q", valor)
		_, campos := corpoDeErro(t, rec)
		assert.Contains(t, campos, "includeArchived")
	}
}

func TestSemIdentidadeNaoPassa(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	// Sem o contexto do RequireAuth. Em produção o middleware barra antes,
	// mas o handler não pode confiar nisso: defesa em profundidade.
	r := httptest.NewRequest(http.MethodGet, "/accounts", nil)
	rec := httptest.NewRecorder()
	amb.handler.List(rec, r)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	// Identidade sem casa também não passa: sem household_id não existe
	// escopo, e uma consulta sem escopo é um vazamento.
	r = httptest.NewRequest(http.MethodGet, "/accounts", nil)
	r = r.WithContext(session.NewContext(r.Context(), session.Identity{UserID: "u", SessionID: "s"}))
	rec = httptest.NewRecorder()
	amb.handler.List(rec, r)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCorpoSemContentTypeJSONEhRecusado(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	r := httptest.NewRequest(http.MethodPost, "/accounts", strings.NewReader(corpoValido))
	r = r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID: "u", HouseholdID: minhaCasa, Role: "owner", SessionID: "s",
	}))
	rec := httptest.NewRecorder()
	amb.handler.Create(rec, r)

	assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
}

func TestCorpoComMaisDeUmObjetoJSONEhRecusado(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpoValido+corpoValido, amb.handler.Create, "")
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Zero(t, amb.repo.criadas)
}

func TestListagemTrazTotalEItens(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	_, err := amb.svc.Create(t.Context(), ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	rec := amb.chamar(t, minhaCasa, http.MethodGet, "/accounts", "", amb.handler.List, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var lista account.ListView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &lista))
	require.Len(t, lista.Items, 1)
	assert.EqualValues(t, 15000, lista.TotalBalanceCents)
}
