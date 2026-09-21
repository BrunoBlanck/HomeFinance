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
//
// O valor de cada chave dentro de um caminho fica como yaml.Node cru porque
// nem toda chave ali é uma operação: o OpenAPI permite `parameters`,
// `summary`, `description` e `servers` no nível do CAMINHO, e `parameters` é
// uma SEQUÊNCIA. Modelar tudo como objeto de operação fazia o unmarshal
// quebrar no primeiro caminho que declarasse um parâmetro compartilhado — que
// é exatamente o que os caminhos com `{id}` fazem, para não repetir a mesma
// definição em quatro verbos.
type specDocument struct {
	OpenAPI string `yaml:"openapi"`
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Paths map[string]map[string]yaml.Node `yaml:"paths"`
}

// métodos HTTP que contam como operação. É a mesma lista usada para montar o
// conjunto comparado com a tabela de rotas.
func ehMetodoHTTP(chave string) bool {
	switch strings.ToLower(chave) {
	case "get", "post", "put", "patch", "delete", "head", "options", "trace":
		return true
	default:
		return false
	}
}

// operationIDDe extrai o operationId de um nó de operação.
func operationIDDe(t *testing.T, node yaml.Node) string {
	t.Helper()
	var op struct {
		OperationID string `yaml:"operationId"`
	}
	require.NoError(t, node.Decode(&op))
	return op.OperationID
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
			if ehMetodoHTTP(method) {
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

// operacoesComHandlerPendente são as operações que JÁ existem no contrato e
// ainda NÃO têm handler na tabela de rotas.
//
// Ela existe porque o projeto é spec-first (ADR-006): o `openapi.yaml` é
// atualizado ANTES do handler, e a entrega E9 foi fatiada de propósito — o
// contrato e a configuração vieram primeiro, o handler vem na fatia seguinte.
// Sem esta lista, a comparação de conjuntos abaixo tornaria o "spec ANTES do
// handler" impossível em duas entregas, e o jeito de ficar verde seria escrever
// a spec DEPOIS — exatamente o contrário da regra.
//
// A lista é apertada nas DUAS direções, e é isso que a impede de virar um
// esconderijo (ver TestOperacoesPendentesSaoExatamenteAsDeclaradas):
//
//   - toda operação daqui precisa EXISTIR na spec — entrada órfã quebra;
//   - nenhuma operação daqui pode ter handler — no dia em que a fatia seguinte
//     registrar a rota, o teste fica VERMELHO e obriga a apagar a linha.
//
// Enquanto uma linha estiver aqui, a rota responde 404 em produção. É o preço
// declarado de publicar contrato antes de implementação, e ele tem de ser
// pequeno e temporário.
var operacoesComHandlerPendente = []string{
	// Vazia desde a E9b (21/09/2026): `GET /ai/export-prompt` saiu na E9a e
	// as duas rotas de import saíram quando `internal/aiimport` registrou os
	// handlers. A lista fica, vazia, como o MECANISMO que a próxima entrega
	// spec-first vai usar — e o teste abaixo continua conferindo que ela não
	// esconde nada.
}

// semPendentes remove da lista de operações da spec as que ainda não têm
// handler declarado.
func semPendentes(operacoes []string) []string {
	pendente := map[string]bool{}
	for _, op := range operacoesComHandlerPendente {
		pendente[op] = true
	}
	out := make([]string, 0, len(operacoes))
	for _, op := range operacoes {
		if !pendente[op] {
			out = append(out, op)
		}
	}
	return out
}

// Critério de aceite 8 da spec 0001 e ADR-011: enquanto o oapi-codegen não
// entra, este teste é o que impede código e contrato de divergirem em
// silêncio.
func TestRotasBatemComOpenAPI(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t)
	assert.True(t, strings.HasPrefix(doc.OpenAPI, "3.1"), "o projeto usa OpenAPI 3.1 (ADR-006)")

	assert.Equal(t, semPendentes(specOperations(t, doc)), routeOperations(),
		"tabela de rotas e api/openapi.yaml divergiram — atualize a spec ANTES do handler")
}

// A lista de pendências não pode crescer sozinha nem sobreviver ao handler.
//
// É o teste que transforma `operacoesComHandlerPendente` de brecha em
// declaração: ele confere que cada linha existe MESMO na spec (nada de entrada
// órfã segurando um conjunto que já bate) e que NENHUMA delas já tem handler
// (foi o que aconteceu quando a E9a registrou `GET /ai/export-prompt`: o teste
// ficou vermelho e cobrou a remoção da linha).
func TestOperacoesPendentesSaoExatamenteAsDeclaradas(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t)

	naSpec := map[string]bool{}
	for _, op := range specOperations(t, doc) {
		naSpec[op] = true
	}
	naTabela := map[string]bool{}
	for _, op := range routeOperations() {
		naTabela[op] = true
	}

	for _, op := range operacoesComHandlerPendente {
		assert.Truef(t, naSpec[op],
			"%s está na lista de pendentes mas NÃO está na spec — entrada órfã esconde divergência", op)
		assert.Falsef(t, naTabela[op],
			"%s já tem handler: apague a linha de operacoesComHandlerPendente", op)
	}

	// Os números da entrega, escritos para que somar uma rota à spec sem
	// somá-la à tabela (ou o contrário) exija passar por aqui.
	assert.Len(t, naSpec, 46, "a API tem 46 operações declaradas (43 + as 3 do menu IA)")
	assert.Len(t, naTabela, 46, "a tabela de rotas tem 46 handlers (43 + as 3 do menu IA, completas na E9b)")
	assert.Empty(t, operacoesComHandlerPendente, "nenhuma operação pendente depois da E9b")
}

// Spec 0010 (E9): as três rotas do menu IA existem no CONTRATO, com o método e
// o caminho exatos que a tela vai chamar.
//
// A comparação de conjuntos de TestRotasBatemComOpenAPI não cobre isto
// enquanto elas estiverem em `operacoesComHandlerPendente` — então este teste é
// quem sustenta o contrato nesta fatia. Ele também trava o que a §10.3 decidiu:
// o export é uma rota `GET` (leitura pura, sem corpo e sem escrita), e as duas
// de import são `POST` no MESMO prefixo, porque compartilham corpo e teto.
func TestRotasDaSpec0010ExistemNoContrato(t *testing.T) {
	t.Parallel()

	naSpec := specOperations(t, loadSpec(t))

	for _, op := range []string{
		"GET /api/v1/ai/export-prompt",
		"POST /api/v1/ai/keyword-import/preview",
		"POST /api/v1/ai/keyword-import/confirm",
	} {
		assert.Contains(t, naSpec, op, "falta na spec")
	}

	// A exportação NÃO pode ser POST: ela não escreve nada, e um POST aqui
	// convidaria alguém a mandar filtro no corpo — que é como a janela de
	// trabalho deixaria de ser conferível na URL (§10.3).
	assert.NotContains(t, naSpec, "POST /api/v1/ai/export-prompt")
	// E não existe rota nova de reprocessamento: a tela orquestra
	// /transfers/detect e /transactions/auto-categorize, que já existem (§10.5).
	assert.NotContains(t, naSpec, "POST /api/v1/ai/reprocess")
	assert.Contains(t, naSpec, "POST /api/v1/transfers/detect")
	assert.Contains(t, naSpec, "POST /api/v1/transactions/auto-categorize")
}

// Spec 0005 (E2c): as duas rotas novas precisam existir NOS DOIS LADOS — na
// spec e na tabela — e com a proteção certa. A comparação de conjuntos acima
// já pega a ausência em um dos lados; este teste é explícito para que remover
// as duas de ambos ao mesmo tempo (o jeito de "fazer o teste passar") também
// quebre.
//
// A contagem de middlewares é o que se consegue afirmar com routeDeps{}
// zerado: requireAuth é nil e o limitador é nil, mas o middleware de rate
// limit é montado do mesmo jeito, então o TAMANHO da cadeia é observável.
func TestRotasDaSpec0005ExistemEEstaoProtegidas(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t)
	naSpec := specOperations(t, doc)

	porPadrao := map[string]Route{}
	for _, r := range buildRoutes(routeDeps{}) {
		porPadrao[r.FullPattern()] = r
	}

	casos := []struct {
		padrao      string
		middlewares int
	}{
		// requireAuth + rate limit por casa (escrita em massa).
		{"POST /api/v1/transactions/auto-categorize", 2},
		// requireAuth.
		{"GET /api/v1/transfers", 1},
		// ADR-028: escrita em massa com balde próprio por casa — requireAuth
		// + rate limit, como o auto-categorize.
		{"POST /api/v1/transfers/detect", 2},
		// Emenda §11: escrita avulsa de UMA linha, mas com balde PRÓPRIO por
		// casa desde 17/09/2026 — requireAuth + rate limit. Ela escreve e
		// AUDITA a cada chamada, e o balde global (100/min por IP) não é teto
		// por casa. A 121ª chamada na hora é 429 e não escreve: a prova está
		// em internal/transaction/updatecategory_abuso_test.go.
		{"PATCH /api/v1/transactions/{id}", 2},
		// ADR-027: relatório é leitura pura sob requireAuth e o limitador
		// global — SEM balde próprio, como GET /transactions e GET /transfers.
		{"GET /api/v1/reports/by-category", 1},
	}
	for _, c := range casos {
		t.Run(c.padrao, func(t *testing.T) {
			assert.Contains(t, naSpec, c.padrao, "falta na spec")
			rt, ok := porPadrao[c.padrao]
			require.True(t, ok, "falta na tabela de rotas")
			assert.Len(t, rt.Middlewares, c.middlewares)
		})
	}
}

// Spec 0010 (E9a): a exportação do prompt existe NOS DOIS LADOS e está
// protegida por requireAuth MAIS o balde por casa.
//
// A contagem de middlewares é o que se consegue afirmar com routeDeps{}
// zerado, e ela é o ponto: a rota é leitura pura, mas NÃO é da classe do
// painel (um middleware só). Cair para 1 aqui seria tirar o teto por casa de
// uma agregação de 3 meses sobre coluna sem índice — em silêncio.
func TestRotaDeExportacaoDaIAExisteEEstaProtegida(t *testing.T) {
	t.Parallel()

	const padrao = "GET /api/v1/ai/export-prompt"

	assert.Contains(t, specOperations(t, loadSpec(t)), padrao, "falta na spec")

	porPadrao := map[string]Route{}
	for _, r := range buildRoutes(routeDeps{}) {
		porPadrao[r.FullPattern()] = r
	}
	rt, ok := porPadrao[padrao]
	require.True(t, ok, "falta na tabela de rotas")
	assert.Len(t, rt.Middlewares, 2, "requireAuth + rate limit por casa (config.AiExport)")
}

// Spec 0010 (E9b): as duas rotas do import de IA existem NOS DOIS LADOS e
// estão protegidas por requireAuth MAIS o balde por casa de cada uma
// (config.AiImportPreview e config.AiImportConfirm). Cair para 1 aqui seria
// tirar o teto por casa de uma rota que roda o matcher (a prévia) ou que
// segura conexão do pool numa transação (o confirm) — em silêncio.
func TestRotasDeImportDaIAExistemEEstaoProtegidas(t *testing.T) {
	t.Parallel()

	naSpec := specOperations(t, loadSpec(t))
	porPadrao := map[string]Route{}
	for _, r := range buildRoutes(routeDeps{}) {
		porPadrao[r.FullPattern()] = r
	}

	for _, padrao := range []string{
		"POST /api/v1/ai/keyword-import/preview",
		"POST /api/v1/ai/keyword-import/confirm",
	} {
		t.Run(padrao, func(t *testing.T) {
			assert.Contains(t, naSpec, padrao, "falta na spec")
			rt, ok := porPadrao[padrao]
			require.True(t, ok, "falta na tabela de rotas")
			assert.Len(t, rt.Middlewares, 2, "requireAuth + rate limit por casa")
		})
	}
}

func TestTodaRotaTemOperationIdUnico(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t)
	seen := map[string]string{}
	for path, ops := range doc.Paths {
		for method, node := range ops {
			if !ehMetodoHTTP(method) {
				continue // parameters, summary e afins no nível do caminho
			}
			operationID := operationIDDe(t, node)
			require.NotEmpty(t, operationID, "%s %s sem operationId", method, path)
			if before, dup := seen[operationID]; dup {
				t.Fatalf("operationId %q repetido em %s e %s %s", operationID, before, method, path)
			}
			seen[operationID] = strings.ToUpper(method) + " " + path
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

// Spec 0006 (E7): as duas rotas de investimento precisam existir NOS DOIS
// LADOS — na spec e na tabela — e com a proteção certa. A comparação de
// conjuntos de TestRotasBatemComOpenAPI já pega a ausência em um dos lados;
// este teste é explícito para que remover as duas de ambos ao mesmo tempo (o
// jeito de "fazer o teste passar") também quebre.
func TestRotasDaSpec0006ExistemEEstaoProtegidas(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t)
	naSpec := specOperations(t, doc)

	porPadrao := map[string]Route{}
	for _, r := range buildRoutes(routeDeps{}) {
		porPadrao[r.FullPattern()] = r
	}

	casos := []struct {
		padrao      string
		middlewares int
	}{
		// ADR-029: leitura pura sob requireAuth e o limitador GLOBAL — sem
		// balde próprio, como GET /transactions e GET /reports/by-category.
		{"GET /api/v1/investments", 1},
		// Escrita em massa com balde PRÓPRIO por casa — requireAuth + rate
		// limit, como o auto-categorize e o transfers/detect.
		{"POST /api/v1/investments/detect", 2},
	}
	for _, c := range casos {
		t.Run(c.padrao, func(t *testing.T) {
			assert.Contains(t, naSpec, c.padrao, "falta na spec")
			rt, ok := porPadrao[c.padrao]
			require.True(t, ok, "falta na tabela de rotas")
			assert.Len(t, rt.Middlewares, c.middlewares)
		})
	}
}

// A rota LITERAL vem antes de qualquer padrão com {id} na tabela.
//
// O ServeMux escolhe o padrão mais específico de qualquer jeito (Go 1.22+), e
// é por isso que o teste olha a ORDEM DA TABELA e não o roteamento: a tabela é
// lida por gente, e a linha literal depois de um curinga é o tipo de coisa que
// alguém "corrige" movendo o curinga para cima.
func TestRotaLiteralDeInvestimentoVemAntesDeCuringa(t *testing.T) {
	t.Parallel()

	var posicaoLiteral, posicaoCuringa = -1, -1
	for i, r := range buildRoutes(routeDeps{}) {
		if !strings.HasPrefix(r.Pattern, "/investments") {
			continue
		}
		if strings.Contains(r.Pattern, "{") {
			if posicaoCuringa == -1 {
				posicaoCuringa = i
			}
			continue
		}
		if r.Pattern == "/investments/detect" {
			posicaoLiteral = i
		}
	}
	require.NotEqual(t, -1, posicaoLiteral, "POST /investments/detect sumiu da tabela")
	if posicaoCuringa != -1 {
		assert.Less(t, posicaoLiteral, posicaoCuringa,
			"a rota literal tem de vir ANTES de qualquer /investments/{id}")
	}
}

// Spec 0008 (E4, 1ª fatia): a rota do painel existe NOS DOIS LADOS — na spec e
// na tabela — e está sob requireAuth e o limitador GLOBAL por IP.
//
// A contagem de middlewares é o ponto do teste, e ela é 1 de propósito
// (ADR-031e): esta é a rota da HOME, a mais chamada do app, e um balde por
// casa transformaria abrir o app e trocar de mês em 429 na primeira tela. Se
// alguém acrescentar um limitador próprio, este teste quebra — e quebrar é o
// comportamento certo: a decisão está num ADR e mudá-la exige justificar.
func TestRotasDaSpec0008ExistemEEstaoProtegidas(t *testing.T) {
	t.Parallel()

	doc := loadSpec(t)
	naSpec := specOperations(t, doc)

	porPadrao := map[string]Route{}
	for _, r := range buildRoutes(routeDeps{}) {
		porPadrao[r.FullPattern()] = r
	}

	const padrao = "GET /api/v1/dashboard"
	assert.Contains(t, naSpec, padrao, "falta na spec")

	rt, ok := porPadrao[padrao]
	require.True(t, ok, "falta na tabela de rotas")
	assert.Len(t, rt.Middlewares, 1,
		"o painel fica sob requireAuth e o limitador GLOBAL — UM middleware, sem balde próprio (ADR-031e)")
}
