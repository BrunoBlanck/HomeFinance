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

	"github.com/brunorblanck/homefinance/backend/internal/aiimport"
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

// caminhos das exceções, escritos UMA vez para o teste e para a varredura de
// "toda outra rota continua em 1 MiB" não divergirem.
const (
	caminhoImports         = APIBasePath + "/imports"
	caminhoAiImportPreview = APIBasePath + "/ai/keyword-import/preview"
	caminhoAiImportConfirm = APIBasePath + "/ai/keyword-import/confirm"
)

// tabelaDeExcecoes é a MESMA tabela montada em main.go. A prova de que é a
// mesma não é a leitura do revisor: é o TestTabelaDeExcecoesDoMainEhEstaMesma
// abaixo, que confere a chamada real no fonte.
//
// São TRÊS exceções, e elas vão em direções opostas: POST /imports SOBE o teto
// para 8 MiB (recebe arquivo); as duas rotas do import de IA o BAIXAM para 128
// KiB, porque o corpo delas é JSON colado de uma IA — entrada hostil que o
// servidor parseia e casa contra o banco (spec 0010 §4.2).
func tabelaDeExcecoes() map[string]int64 {
	return map[string]int64{
		caminhoImports:         importer.MaxUploadBytes,
		caminhoAiImportPreview: aiimport.MaxPayloadBytes,
		caminhoAiImportConfirm: aiimport.MaxPayloadBytes,
	}
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
	assert.EqualValues(t, 128<<10, aiimport.MaxPayloadBytes, "o import de IA é 128 KiB (spec 0010 §4.2)")

	// A direção de cada exceção é contrato, não acaso: uma sobe, duas descem.
	// Trocar o sinal de qualquer uma é decisão de segurança, e quebra aqui.
	// (As conversões existem porque MaxUploadBytes é `int` e os outros dois são
	// `int64`; comparar tipos diferentes com testify falha por tipo, não por
	// valor, e esconderia o que o teste quer afirmar.)
	assert.Greater(t, int64(importer.MaxUploadBytes), config.MaxRequestBodyBytes,
		"POST /imports é a exceção que SOBE — ele recebe arquivo")
	assert.Less(t, aiimport.MaxPayloadBytes, config.MaxRequestBodyBytes,
		"o import de IA é a exceção que DESCE — o corpo é JSON hostil, e 1 MiB dele é trabalho de graça")
}

func TestPostImportsAceitaOitoMebibytesReais(t *testing.T) {
	t.Parallel()

	assert.Equal(t, http.StatusOK,
		statusComCorpoDe(t, caminhoImports, importer.MaxUploadBytes),
		"a rota de importação precisa aceitar 8 MiB inteiros")
	assert.Equal(t, http.StatusRequestEntityTooLarge,
		statusComCorpoDe(t, caminhoImports, importer.MaxUploadBytes+1),
		"e recusar UM byte acima")
}

// As duas rotas do import de IA aceitam 128 KiB inteiros e recusam UM byte
// acima — com 413, que é o que o MaxBytesReader produz, e não 400 (achado A4
// da emenda §10 da spec 0010).
func TestImportDeIAAceitaCentoEVinteEOitoKibibytesReais(t *testing.T) {
	t.Parallel()

	for _, caminho := range []string{caminhoAiImportPreview, caminhoAiImportConfirm} {
		t.Run(caminho, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, http.StatusOK,
				statusComCorpoDe(t, caminho, aiimport.MaxPayloadBytes),
				"%s precisa aceitar 128 KiB inteiros", caminho)
			assert.Equal(t, http.StatusRequestEntityTooLarge,
				statusComCorpoDe(t, caminho, aiimport.MaxPayloadBytes+1),
				"%s precisa recusar UM byte acima, com 413", caminho)

			// E o principal: o teto BAIXO é real. Um corpo que passaria no
			// teto global de 1 MiB é recusado aqui.
			assert.Equal(t, http.StatusRequestEntityTooLarge,
				statusComCorpoDe(t, caminho, config.MaxRequestBodyBytes),
				"%s NÃO pode herdar o teto global de 1 MiB", caminho)
		})
	}
}

// TestTodaOutraRotaContinuaNoTetoDeUmMebibyte é o teste que a §6.4 exige com
// todas as letras, agora com os números reais e com as variações de caminho que
// um teto por PREFIXO deixaria passar.
func TestTodaOutraRotaContinuaNoTetoDeUmMebibyte(t *testing.T) {
	t.Parallel()

	// Toda rota real da tabela, exceto as exceções declaradas.
	excecoes := tabelaDeExcecoes()
	caminhos := map[string]bool{}
	for _, rt := range buildRoutes(routeDeps{}) {
		caminho := APIBasePath + rt.Pattern
		if _, excecao := excecoes[caminho]; excecao {
			continue
		}
		// Padrões com {id} viram um caminho concreto: é o que o cliente manda.
		caminhos[semPlaceholders(caminho)] = true
	}
	require.NotEmpty(t, caminhos)

	// E as variações perigosas, que são o motivo de a comparação ser EXATA.
	// Elas valem para as TRÊS exceções, e não só para a que sobe o teto: um
	// teto por prefixo em `/ai/keyword-import` faria `/ai/keyword-importXYZ`
	// herdar 128 KiB, e — pior — um prefixo em `/ai/` cortaria o export.
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
		APIBasePath + "/ai/keyword-import/preview/",
		APIBasePath + "/ai/keyword-import/previewXYZ",
		APIBasePath + "/ai/keyword-import/confirm/",
		APIBasePath + "/ai/keyword-import/confirmXYZ",
		APIBasePath + "/ai/keyword-import",
		APIBasePath + "/ai/keyword-import/",
		APIBasePath + "/AI/keyword-import/preview",
		APIBasePath + "//ai/keyword-import/preview",
		APIBasePath + "/ai/export-prompt",
		APIBasePath + "/ai",
		"/ai/keyword-import/preview",
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
// de EXATAMENTE três entradas, todas com chave `APIBasePath + "<caminho
// literal>"` e valor vindo de uma constante nomeada.
//
// O dia em que alguém acrescentar uma quarta exceção — ou trocar uma chave por
// um prefixo, ou um valor por um número solto —, este teste fica vermelho antes
// de o código chegar à revisão. A lista abaixo é a AUTORIZAÇÃO: entrada que não
// está aqui não passa, e acrescentá-la é um ato deliberado, num teste que o
// revisor lê.
func TestTabelaDeExcecoesDoMainEhEstaMesma(t *testing.T) {
	t.Parallel()

	// caminho literal (sem APIBasePath) -> nome da constante do teto.
	autorizadas := map[string]string{
		`"/imports"`:                   "MaxUploadBytes",
		`"/ai/keyword-import/preview"`: "MaxPayloadBytes",
		`"/ai/keyword-import/confirm"`: "MaxPayloadBytes",
	}

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

	// 2º argumento: o mapa de exceções, com exatamente as entradas autorizadas.
	mapa, ok := chamada.Args[1].(*ast.CompositeLit)
	require.True(t, ok, "as exceções são um literal de mapa, revisável numa olhada")
	require.Len(t, mapa.Elts, len(autorizadas),
		"há EXATAMENTE %d exceções declaradas; uma a mais precisa de revisão", len(autorizadas))

	vistas := map[string]bool{}
	for _, elemento := range mapa.Elts {
		par, ok := elemento.(*ast.KeyValueExpr)
		require.True(t, ok)

		// A chave é `APIBasePath + "<caminho>"` — caminho EXATO, sem curinga.
		soma, ok := par.Key.(*ast.BinaryExpr)
		require.True(t, ok, "a chave é APIBasePath + o caminho")
		base, ok := soma.X.(*ast.Ident)
		require.True(t, ok)
		assert.Equal(t, "APIBasePath", base.Name)
		sufixo, ok := soma.Y.(*ast.BasicLit)
		require.True(t, ok)

		constante, autorizada := autorizadas[sufixo.Value]
		require.Truef(t, autorizada,
			"exceção de teto para %s não está autorizada neste teste — uma rota com teto próprio é decisão de segurança",
			sufixo.Value)
		assert.Falsef(t, vistas[sufixo.Value], "caminho %s declarado duas vezes", sufixo.Value)
		vistas[sufixo.Value] = true

		// O valor vem de uma constante NOMEADA, nunca de um número mágico.
		valor, ok := par.Value.(*ast.SelectorExpr)
		require.True(t, ok, "o valor vem de uma constante de pacote, nunca de um número mágico")
		assert.Equal(t, constante, valor.Sel.Name, "teto errado para %s", sufixo.Value)
	}
	assert.Len(t, vistas, len(autorizadas), "alguma exceção autorizada sumiu da tabela do main.go")
}
