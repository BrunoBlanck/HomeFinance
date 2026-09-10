package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// specDocument é o mínimo do OpenAPI que precisamos ler.
type specDocument struct {
	OpenAPI string `yaml:"openapi"`
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Paths map[string]map[string]struct {
		OperationID string `yaml:"operationId"`
	} `yaml:"paths"`
}

func loadSpec(t *testing.T) specDocument {
	t.Helper()

	raw, err := os.ReadFile("../../api/openapi.yaml")
	require.NoError(t, err, "api/openapi.yaml precisa existir (ADR-006)")

	var doc specDocument
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	require.NotEmpty(t, doc.Paths)
	return doc
}

func specOperations(t *testing.T, doc specDocument) []string {
	t.Helper()

	require.NotEmpty(t, doc.Servers, "a spec precisa declarar o servidor base")
	base := strings.TrimSuffix(doc.Servers[0].URL, "/")

	var out []string
	for path, ops := range doc.Paths {
		for method := range ops {
			switch strings.ToLower(method) {
			case "get", "post", "put", "patch", "delete", "head", "options":
				out = append(out, strings.ToUpper(method)+" "+base+path)
			}
		}
	}
	sort.Strings(out)
	return out
}

func routeOperations() []string {
	routes := buildRoutes(routeDeps{})
	out := make([]string, 0, len(routes))
	for _, r := range routes {
		out = append(out, r.FullPattern())
	}
	sort.Strings(out)
	return out
}

// Critério de aceite 8 da spec 0001 e ADR-011: enquanto o oapi-codegen não
// entra, este teste é o que impede código e contrato de divergirem em
// silêncio.
func TestRotasBatemComOpenAPI(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t)
	assert.True(t, strings.HasPrefix(doc.OpenAPI, "3.1"), "o projeto usa OpenAPI 3.1 (ADR-006)")

	assert.Equal(t, specOperations(t, doc), routeOperations(),
		"tabela de rotas e api/openapi.yaml divergiram — atualize a spec ANTES do handler")
}

func TestTodaRotaTemOperationIdUnico(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t)
	seen := map[string]string{}
	for path, ops := range doc.Paths {
		for method, op := range ops {
			require.NotEmpty(t, op.OperationID, "%s %s sem operationId", method, path)
			if before, dup := seen[op.OperationID]; dup {
				t.Fatalf("operationId %q repetido em %s e %s %s", op.OperationID, before, method, path)
			}
			seen[op.OperationID] = strings.ToUpper(method) + " " + path
		}
	}
}

func TestTodaRotaTemHandler(t *testing.T) {
	t.Parallel()

	// Com dependências zeradas os handlers são method values sobre ponteiro
	// nil — legítimos em Go e suficientes para conferir que a tabela não tem
	// buraco.
	for _, r := range buildRoutes(routeDeps{}) {
		assert.NotNil(t, r.Handler, "rota sem handler: %s", r.FullPattern())
		assert.True(t, strings.HasPrefix(r.Pattern, "/"), "padrão precisa começar com /: %s", r.Pattern)
		assert.NotContains(t, r.Pattern, APIBasePath, "o padrão é relativo à base: %s", r.Pattern)
	}
}

// Critério de aceite 9: 404 e 405 do ServeMux saem no formato único.
func TestMuxDevolveErroPadronizado(t *testing.T) {
	t.Parallel()

	mux := newMux([]Route{{
		Method:  http.MethodGet,
		Pattern: "/health",
		Handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
	}})

	casos := []struct {
		nome    string
		metodo  string
		caminho string
		status  int
		codigo  string
	}{
		{"rota inexistente", http.MethodGet, "/api/v1/nao-existe", http.StatusNotFound, "NOT_FOUND"},
		{"metodo errado", http.MethodDelete, "/api/v1/health", http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED"},
		{"fora da base", http.MethodGet, "/health", http.StatusNotFound, "NOT_FOUND"},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			rec := httptest.NewRecorder()
			httpserver.ErrorShim(mux).ServeHTTP(rec, httptest.NewRequest(c.metodo, c.caminho, nil))

			assert.Equal(t, c.status, rec.Code)
			assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
			assert.Contains(t, rec.Body.String(), c.codigo)
		})
	}
}
