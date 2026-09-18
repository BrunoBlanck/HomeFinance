package category_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ambiente struct {
	handler *category.Handler
	repo    *repoFake
	svc     *category.Service
}

func novoAmbiente(t *testing.T, opts ...category.Option) *ambiente {
	t.Helper()
	repo := novoRepo()
	svc := novoServico(t, repo, opts...)
	return &ambiente{handler: category.NewHandler(svc, logging.Discard(), 0), repo: repo, svc: svc}
}

func (a *ambiente) chamar(t *testing.T, casa, metodo, alvo, corpo string, h http.HandlerFunc, pathID string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(metodo, alvo, strings.NewReader(corpo))
	if corpo != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if pathID != "" {
		r.SetPathValue("id", pathID)
	}
	r = r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID: "user-1", HouseholdID: casa, Role: "owner", SessionID: "sess-1",
	}))

	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

func corpoDeErro(t *testing.T, rec *httptest.ResponseRecorder) (string, map[string]string) {
	t.Helper()
	var env struct {
		Error struct {
			Code   string            `json:"code"`
			Fields map[string]string `json:"fields"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), "corpo: %s", rec.Body.String())
	return env.Error.Code, env.Error.Fields
}

func TestCriarGrupoEFolhaPelaAPI(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		`{"name":"Moradia","kind":"expense"}`, amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var grupo category.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &grupo))
	assert.Nil(t, grupo.ParentID)
	assert.NotNil(t, grupo.Children, "children vem como lista vazia, nunca null")

	rec = amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		fmt.Sprintf(`{"name":"Energia","parentId":%q}`, grupo.ID), amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var folha category.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &folha))
	require.NotNil(t, folha.ParentID)
	assert.Equal(t, grupo.ID, *folha.ParentID)
	assert.Equal(t, category.KindExpense, folha.Kind, "herdou do grupo")
}

func TestCampoDerivadoOuProibidoNoCorpoEhRecusado(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	proibidos := map[string]string{
		"householdId": `{"name":"X","kind":"expense","householdId":"casa-2"}`,
		"id":          `{"name":"X","kind":"expense","id":"forjado"}`,
		"nameNorm":    `{"name":"X","kind":"expense","nameNorm":"y"}`,
		"archivedAt":  `{"name":"X","kind":"expense","archivedAt":null}`,
		"children":    `{"name":"X","kind":"expense","children":[]}`,
	}
	for campo, corpo := range proibidos {
		rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories", corpo, amb.handler.Create, "")
		assert.Equal(t, http.StatusBadRequest, rec.Code, "campo %q", campo)
	}
}

// Invariante 6: mover categoria de grupo não existe no v1, e `parentId` nem
// aparece no DTO de edição — então mandá-lo é 400, não um no-op silencioso.
func TestPatchComParentIdEhRecusado(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	grupo := criarGrupo(t, amb.svc, minhaCasa, "Moradia", category.KindExpense)

	rec := amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+grupo.ID,
		`{"name":"Casa","parentId":"outro"}`, amb.handler.Update, grupo.ID)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRecursoDeOutraCasaResponde404EmTodasAsRotas(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	daOutra := criarGrupo(t, amb.svc, outraCasa, "Moradia", category.KindExpense)

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
		rec := amb.chamar(t, minhaCasa, rota.metodo, "/categories/"+daOutra.ID, rota.corpo, rota.handler, daOutra.ID)
		require.Equal(t, http.StatusNotFound, rec.Code, "rota %s", rota.nome)
		codigo, _ := corpoDeErro(t, rec)
		assert.Equal(t, "NOT_FOUND", codigo, "rota %s", rota.nome)
		assert.NotContains(t, rec.Body.String(), "Moradia")
	}
}

// parentId de outra casa é 404 — e não 422 nem 403. Um status diferente aqui
// contaria ao atacante que aquele id existe em alguma casa (S1).
func TestParentIdDeOutraCasaResponde404(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	grupoAlheio := criarGrupo(t, amb.svc, outraCasa, "Moradia", category.KindExpense)

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		fmt.Sprintf(`{"name":"Energia","parentId":%q}`, grupoAlheio.ID), amb.handler.Create, "")
	require.Equal(t, http.StatusNotFound, rec.Code)

	// Mesmo status e mesmo corpo de um id que nunca existiu.
	inexistente := amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		`{"name":"Energia","parentId":"nunca-existiu"}`, amb.handler.Create, "")
	assert.Equal(t, inexistente.Code, rec.Code)
	assert.JSONEq(t, inexistente.Body.String(), rec.Body.String())
}

func TestTerceiroNivelDevolve422ComOCampo(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	grupo := criarGrupo(t, amb.svc, minhaCasa, "Moradia", category.KindExpense)
	folha := criarFolha(t, amb.svc, minhaCasa, "Energia", grupo.ID)

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		fmt.Sprintf(`{"name":"Bandeira","parentId":%q}`, folha.ID), amb.handler.Create, "")
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, "VALIDATION_FAILED", codigo)
	assert.Contains(t, campos, "parentId")
}

// Critério de aceite 2 da spec 0003: a UI precisa distinguir "está em uso" de
// "corrija o formulário" para poder oferecer arquivar.
func TestExcluirGrupoComFilhasDevolveResourceInUse(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	grupo := criarGrupo(t, amb.svc, minhaCasa, "Moradia", category.KindExpense)
	criarFolha(t, amb.svc, minhaCasa, "Energia", grupo.ID)

	rec := amb.chamar(t, minhaCasa, http.MethodDelete, "/categories/"+grupo.ID, "", amb.handler.Delete, grupo.ID)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	codigo, _ := corpoDeErro(t, rec)
	assert.Equal(t, "RESOURCE_IN_USE", codigo)
}

func TestListaDevolveArvoreSeparadaPorNatureza(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	moradia := criarGrupo(t, amb.svc, minhaCasa, "Moradia", category.KindExpense)
	criarFolha(t, amb.svc, minhaCasa, "Energia", moradia.ID)
	criarGrupo(t, amb.svc, minhaCasa, "Salário", category.KindIncome)

	rec := amb.chamar(t, minhaCasa, http.MethodGet, "/categories", "", amb.handler.List, "")
	require.Equal(t, http.StatusOK, rec.Code)

	var arvore category.ListView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &arvore))
	require.Len(t, arvore.Expense, 1)
	require.Len(t, arvore.Income, 1)
	assert.Len(t, arvore.Expense[0].Children, 1)

	// Lista sempre é lista, nunca null — a tela não precisa de um caso a mais.
	assert.NotNil(t, arvore.Income[0].Children)
	assert.Contains(t, rec.Body.String(), `"children":[]`)
}

func TestFiltroDeNaturezaInvalidoDevolve400(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	rec := amb.chamar(t, minhaCasa, http.MethodGet, "/categories?kind=receita", "", amb.handler.List, "")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, "VALIDATION_FAILED", codigo)
	assert.Contains(t, campos, "kind")

	// Os válidos passam.
	for _, kind := range []string{"", "income", "expense"} {
		alvo := "/categories"
		if kind != "" {
			alvo += "?kind=" + kind
		}
		rec := amb.chamar(t, minhaCasa, http.MethodGet, alvo, "", amb.handler.List, "")
		assert.Equal(t, http.StatusOK, rec.Code, "kind %q", kind)
	}
}

func TestNaturezaTravadaDevolve422(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	grupo := criarGrupo(t, amb.svc, minhaCasa, "Moradia", category.KindExpense)
	criarFolha(t, amb.svc, minhaCasa, "Energia", grupo.ID)

	rec := amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+grupo.ID,
		`{"kind":"income"}`, amb.handler.Update, grupo.ID)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	_, campos := corpoDeErro(t, rec)
	assert.Contains(t, campos, "kind")
}

func TestDesarquivarFolhaComGrupoArquivadoDevolve422(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	grupo := criarGrupo(t, amb.svc, minhaCasa, "Moradia", category.KindExpense)
	folha := criarFolha(t, amb.svc, minhaCasa, "Energia", grupo.ID)
	_, err := amb.svc.Archive(t.Context(), ator(minhaCasa), grupo.ID)
	require.NoError(t, err)

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories/"+folha.ID+"/unarchive", "", amb.handler.Unarchive, folha.ID)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	_, campos := corpoDeErro(t, rec)
	assert.Contains(t, campos, "parentId")
}

func TestSemIdentidadeNaoPassa(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	r := httptest.NewRequest(http.MethodGet, "/categories", nil)
	rec := httptest.NewRecorder()
	amb.handler.List(rec, r)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
