package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A FIAÇÃO da guarda de query malformada, medida com a cadeia real.
//
// O middleware em si é medido em internal/platform/httpserver; as rotas, em
// internal/report, internal/transaction e internal/account. O que falta — e é
// o que mora aqui — é a POSIÇÃO: WellFormedQuery tem de ser o ÚLTIMO da lista
// de Chain, ou seja, o MAIS INTERNO. A posição não é estética:
//
//	(1) mais interno que SecurityHeaders/CORS → o 400 sai com os cabeçalhos, e
//	    o navegador consegue LER a resposta em vez de tomar um erro de CORS
//	    opaco;
//	(2) depois do CSRFGuard → origem estranha continua barrada ANTES de
//	    qualquer validação de entrada;
//	(3) depois do RateLimit → enxurrada de query malformada continua gastando
//	    balde; checagem barata não pode virar rota grátis.
//
// Sem esta metade, o middleware poderia estar perfeito e a chamada em main.go
// colocá-lo na frente de tudo — o que transformaria a rota em um teste de
// query DE GRAÇA, sem balde e sem CSRF.

// cadeiaDeTeste monta a MESMA ordem de main.go, com o que estes testes medem.
// O RateLimit fica com um balde minúsculo para o estouro ser observável.
func cadeiaDeTeste(t *testing.T, alvo http.Handler, requisicoes int) http.Handler {
	t.Helper()

	allowlist := httpserver.NewOriginAllowlist([]string{"https://app.exemplo.test"})
	limitador := httpserver.NewLimiter(requisicoes, time.Minute, time.Minute)

	return httpserver.Chain(
		alvo,
		httpserver.Recover(logging.Discard()),
		httpserver.RequestID(),
		httpserver.AccessLog(logging.Discard(), 0),
		httpserver.SecurityHeaders(true),
		httpserver.CORS(allowlist),
		httpserver.CSRFGuard(allowlist),
		httpserver.RateLimit(limitador, httpserver.IPKey(0)),
		httpserver.WellFormedQuery(),
	)
}

func requisicaoComQuery(metodo, caminho, query string) *http.Request {
	r := httptest.NewRequest(metodo, caminho, nil)
	r.URL.RawQuery = query
	r.Header.Set("Origin", "https://app.exemplo.test")
	return r
}

// Critério 10: o 400 sai com X-Request-Id e com os cabeçalhos de segurança e
// de CORS — porque a guarda é INTERNA a eles.
func TestQueryMalformadaRespondeComOsCabecalhosDaCadeia(t *testing.T) {
	t.Parallel()

	cadeia := cadeiaDeTeste(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), 100)

	rec := httptest.NewRecorder()
	cadeia.ServeHTTP(rec, requisicaoComQuery(http.MethodGet, "/api/v1/transactions", "month=2026-09&kindGroup=expense;"))

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("X-Request-Id"), "o 400 é correlacionável")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	assert.Equal(t, "https://app.exemplo.test", rec.Header().Get("Access-Control-Allow-Origin"),
		"sem este cabeçalho o navegador não conseguiria LER o 400")

	var env httpserver.ErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
	assert.Nil(t, env.Error.Fields)
}

// Critério 9: a requisição malformada CONSOME o balde global por IP, porque a
// guarda está DEPOIS do RateLimit. Uma checagem barata na frente do limitador
// seria uma rota grátis para varrer a API.
func TestQueryMalformadaConsomeOBaldeGlobalDeIP(t *testing.T) {
	t.Parallel()

	var chegaramAoHandler int
	cadeia := cadeiaDeTeste(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chegaramAoHandler++
		w.WriteHeader(http.StatusOK)
	}), 2)

	// Duas requisições malformadas: 400 nas duas, e o balde de 2 esgotado.
	for range 2 {
		rec := httptest.NewRecorder()
		cadeia.ServeHTTP(rec, requisicaoComQuery(http.MethodGet, "/api/v1/accounts", "includeArchived=true;"))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}

	// A terceira, BEM FORMADA, já não passa: o balde foi gasto pelas duas
	// anteriores. Se a guarda estivesse antes do limitador, esta responderia
	// 200 e a API teria uma porta de fora do funil.
	rec := httptest.NewRecorder()
	cadeia.ServeHTTP(rec, requisicaoComQuery(http.MethodGet, "/api/v1/accounts", "includeArchived=true"))
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	assert.NotEmpty(t, rec.Header().Get("Retry-After"))
	assert.Zero(t, chegaramAoHandler, "nenhuma das três chegou ao handler")
}

// Critério 9 (a outra metade): a guarda está DEPOIS do CSRFGuard — origem
// estranha continua barrada antes de qualquer validação de entrada, mesmo
// quando a query também está malformada.
func TestOrigemEstranhaComQueryMalformadaEhBarradaPeloCSRFPrimeiro(t *testing.T) {
	t.Parallel()

	cadeia := cadeiaDeTeste(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), 100)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/transactions", nil)
	r.URL.RawQuery = "kindGroup=expense;"
	r.Header.Set("Origin", "https://atacante.test")

	rec := httptest.NewRecorder()
	cadeia.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
}

// Critério 8: sem query e com query válida, nada muda — inclusive em /health,
// que é a sonda de liveness do orquestrador e não pode ganhar um modo de falha
// novo.
func TestSemQueryEComQueryValidaACadeiaNaoMuda(t *testing.T) {
	t.Parallel()

	cadeia := cadeiaDeTeste(t, httpserver.Health(), 100)

	for nome, query := range map[string]string{
		"sem query":    "",
		"query válida": "probe=liveness",
	} {
		t.Run(nome, func(t *testing.T) {
			rec := httptest.NewRecorder()
			cadeia.ServeHTTP(rec, requisicaoComQuery(http.MethodGet, "/health", query))
			assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	}

	// E /health com query malformada também é 400 — a guarda é global de
	// propósito: uma exceção por caminho seria uma lista para manter, e
	// "malformada" não é mais aceitável numa rota do que noutra.
	rec := httptest.NewRecorder()
	cadeia.ServeHTTP(rec, requisicaoComQuery(http.MethodGet, "/health", "probe=liveness;"))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// TestWellFormedQueryEhOUltimoDaCadeiaEmMain é a trava de FIAÇÃO.
//
// Os testes acima medem uma cadeia montada AQUI. Isso só vale se a cadeia de
// main.go tiver a mesma ordem — e main.go a monta dentro de run(), que não é
// chamável de teste sem subir servidor, banco e mailer. A saída é conferir a
// chamada real na árvore sintática, no mesmo molde de
// TestTabelaDeExcecoesDoMainEhEstaMesma.
func TestWellFormedQueryEhOUltimoDaCadeiaEmMain(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	arquivo, err := parser.ParseFile(fset, "main.go", nil, parser.SkipObjectResolution)
	require.NoError(t, err)

	var chains []*ast.CallExpr
	var guardas int
	ast.Inspect(arquivo, func(n ast.Node) bool {
		chamada, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := chamada.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "Chain":
			chains = append(chains, chamada)
		case "WellFormedQuery":
			guardas++
		}
		return true
	})

	require.Len(t, chains, 1, "a cadeia global é montada UMA vez")
	require.Equal(t, 1, guardas, "a guarda é instalada UMA vez, na cadeia global")

	args := chains[0].Args
	require.Greater(t, len(args), 1)

	// O ÚLTIMO argumento de Chain é o middleware MAIS INTERNO (ver Chain: ele
	// compõe de trás para frente). É essa posição que o teste tranca.
	ultimo, ok := args[len(args)-1].(*ast.CallExpr)
	require.True(t, ok, "o último argumento de Chain é uma chamada de middleware")
	sel, ok := ultimo.Fun.(*ast.SelectorExpr)
	require.True(t, ok)
	assert.Equal(t, "WellFormedQuery", sel.Sel.Name,
		"WellFormedQuery é o MAIS INTERNO: dentro de SecurityHeaders/CORS, depois do CSRFGuard e depois do RateLimit")

	// E o RateLimit vem imediatamente antes dele — é o vizinho cuja ordem
	// importa mais: invertê-los daria uma rota fora do funil.
	penultimo, ok := args[len(args)-2].(*ast.CallExpr)
	require.True(t, ok)
	selPenultimo, ok := penultimo.Fun.(*ast.SelectorExpr)
	require.True(t, ok)
	assert.Equal(t, "RateLimit", selPenultimo.Sel.Name)
}
