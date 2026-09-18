package main

import (
	"bytes"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/platform/config"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// O teto de corpo POR ROTA, medido com os VALORES REAIS (spec 0004 §6.4).
//
// O teste do middleware (httpserver/maxbytes_test.go) usa tetos proporcionais
// para não mover 8 MiB por caso. Ele prova a MECÂNICA. O que falta — e é o que
// mora aqui — é provar os NÚMEROS e a FIAÇÃO: que a exceção é 8 MiB, que ela
// vale só em `/api/v1/imports`, e que a tabela montada em main.go é essa e não
// outra.
//
// Sem a segunda metade, o middleware poderia estar perfeito e a chamada em
// main.go abrir 8 MiB em `/api/v1/` inteiro sem nenhum teste ficar vermelho.

// tabelaDeExcecoes é a MESMA tabela montada em main.go. A prova de que é a
// mesma não é a leitura do revisor: é o TestTabelaDeExcecoesDoMainEhEstaMesma
// abaixo, que confere a chamada real no fonte.
func tabelaDeExcecoes() map[string]int64 {
	return map[string]int64{APIBasePath + "/imports": importer.MaxUploadBytes}
}

func handlerQueLeTudo() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		var teto *http.MaxBytesError
		if errors.As(err, &teto) {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

func statusComCorpoDe(t *testing.T, caminho string, tamanho int64) int {
	t.Helper()

	mw := httpserver.MaxBytesByPath(config.MaxRequestBodyBytes, tabelaDeExcecoes())
	r := httptest.NewRequest(http.MethodPost, caminho, bytes.NewReader(make([]byte, tamanho)))
	rec := httptest.NewRecorder()
	mw(handlerQueLeTudo()).ServeHTTP(rec, r)
	return rec.Code
}

// TestOsNumerosSaoOsDaSpec trava os dois valores. Eles são contrato: a §5.6 diz
// 8 MiB para o upload e 1 MiB para o resto, e a tela de importação diz ao
// usuário "o arquivo passa do limite de 8 MB".
func TestOsNumerosSaoOsDaSpec(t *testing.T) {
	t.Parallel()

	assert.EqualValues(t, 8<<20, importer.MaxUploadBytes, "o upload é 8 MiB (spec 0004 §5.6)")
	assert.EqualValues(t, 1<<20, config.MaxRequestBodyBytes, "o teto global continua 1 MiB")
}

func TestPostImportsAceitaOitoMebibytesReais(t *testing.T) {
	t.Parallel()

	assert.Equal(t, http.StatusOK,
		statusComCorpoDe(t, APIBasePath+"/imports", importer.MaxUploadBytes),
		"a rota de importação precisa aceitar 8 MiB inteiros")
	assert.Equal(t, http.StatusRequestEntityTooLarge,
		statusComCorpoDe(t, APIBasePath+"/imports", importer.MaxUploadBytes+1),
		"e recusar UM byte acima")
}

// TestTodaOutraRotaContinuaNoTetoDeUmMebibyte é o teste que a §6.4 exige com
// todas as letras, agora com os números reais e com as variações de caminho que
// um teto por PREFIXO deixaria passar.
func TestTodaOutraRotaContinuaNoTetoDeUmMebibyte(t *testing.T) {
	t.Parallel()

	// Toda rota real da tabela, exceto a exceção declarada.
	caminhos := map[string]bool{}
	for _, rt := range buildRoutes(routeDeps{}) {
		caminho := APIBasePath + rt.Pattern
		if caminho == APIBasePath+"/imports" {
			continue
		}
		// Padrões com {id} viram um caminho concreto: é o que o cliente manda.
		caminhos[semPlaceholders(caminho)] = true
	}
	require.NotEmpty(t, caminhos)

	// E as variações perigosas, que são o motivo de a comparação ser EXATA.
	for _, variacao := range []string{
		APIBasePath + "/imports/",
		APIBasePath + "/importsXYZ",
		APIBasePath + "/imports2",
		APIBasePath + "/IMPORTS",
		APIBasePath + "/Imports",
		APIBasePath + "//imports",
		APIBasePath + "/imports/../imports",
		APIBasePath + "/imports%2F",
		"/imports",
		APIBasePath + "/imports/abc",
		APIBasePath + "/imports/abc/confirm",
	} {
		caminhos[variacao] = true
	}

	for caminho := range caminhos {
		t.Run(caminho, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, http.StatusOK,
				statusComCorpoDe(t, caminho, config.MaxRequestBodyBytes),
				"%s aceita o teto padrão", caminho)
			assert.Equal(t, http.StatusRequestEntityTooLarge,
				statusComCorpoDe(t, caminho, config.MaxRequestBodyBytes+1),
				"%s NÃO pode herdar os 8 MiB da importação", caminho)
		})
	}
}

// semPlaceholders troca "{id}" por um segmento concreto.
func semPlaceholders(padrao string) string {
	saida := padrao
	for {
		abre := indexOf(saida, '{')
		if abre < 0 {
			return saida
		}
		fecha := indexOf(saida[abre:], '}')
		if fecha < 0 {
			return saida
		}
		saida = saida[:abre] + "00000000-0000-7000-8000-000000000001" + saida[abre+fecha+1:]
	}
}

func indexOf(s string, c byte) int {
	for i := range len(s) {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// TestTabelaDeExcecoesDoMainEhEstaMesma é a trava de FIAÇÃO.
//
// Os testes acima medem o comportamento de uma tabela montada AQUI. Isso só
// vale alguma coisa se a tabela de main.go for a mesma — e main.go monta a
// cadeia dentro de run(), que não é chamável de teste sem subir servidor,
// banco e mailer.
//
// A saída é conferir a chamada real na árvore sintática: `MaxBytesByPath`
// precisa aparecer UMA vez, com o teto global como primeiro argumento e um mapa
// de EXATAMENTE uma entrada, cuja chave é `APIBasePath + "/imports"` e cujo
// valor é `importer.MaxUploadBytes`.
//
// O dia em que alguém acrescentar uma segunda exceção — ou trocar a chave por
// um prefixo —, este teste fica vermelho antes de o código chegar à revisão.
func TestTabelaDeExcecoesDoMainEhEstaMesma(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	arquivo, err := parser.ParseFile(fset, "main.go", nil, parser.SkipObjectResolution)
	require.NoError(t, err)

	var chamadas []*ast.CallExpr
	ast.Inspect(arquivo, func(n ast.Node) bool {
		chamada, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := chamada.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "MaxBytesByPath" {
			chamadas = append(chamadas, chamada)
		}
		return true
	})

	require.Len(t, chamadas, 1, "MaxBytesByPath é chamado UMA vez, na cadeia global")
	chamada := chamadas[0]
	require.Len(t, chamada.Args, 2)

	// 1º argumento: o teto global, pelo nome da constante.
	padrao, ok := chamada.Args[0].(*ast.SelectorExpr)
	require.True(t, ok, "o teto padrão vem de uma constante, nunca de um literal solto")
	assert.Equal(t, "MaxRequestBodyBytes", padrao.Sel.Name)

	// 2º argumento: o mapa de exceções, com exatamente uma entrada.
	mapa, ok := chamada.Args[1].(*ast.CompositeLit)
	require.True(t, ok, "as exceções são um literal de mapa, revisável numa olhada")
	require.Len(t, mapa.Elts, 1, "há EXATAMENTE uma exceção declarada; uma segunda precisa de revisão")

	par, ok := mapa.Elts[0].(*ast.KeyValueExpr)
	require.True(t, ok)

	// A chave é `APIBasePath + "/imports"` — caminho EXATO, sem curinga.
	soma, ok := par.Key.(*ast.BinaryExpr)
	require.True(t, ok, "a chave é APIBasePath + o caminho")
	base, ok := soma.X.(*ast.Ident)
	require.True(t, ok)
	assert.Equal(t, "APIBasePath", base.Name)
	sufixo, ok := soma.Y.(*ast.BasicLit)
	require.True(t, ok)
	assert.Equal(t, `"/imports"`, sufixo.Value)

	// O valor é o teto do upload, pelo nome.
	valor, ok := par.Value.(*ast.SelectorExpr)
	require.True(t, ok, "o valor vem de importer.MaxUploadBytes, nunca de um número mágico")
	assert.Equal(t, "MaxUploadBytes", valor.Sel.Name)
}
