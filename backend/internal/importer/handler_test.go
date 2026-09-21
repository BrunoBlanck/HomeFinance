package importer_test

import (
	"bytes"
	"encoding/json"
	"go/parser"
	"go/printer"
	"go/token"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Testes da BORDA da importação (T10).
//
// Eles exercitam o handler direto, com a identidade injetada no contexto como o
// RequireAuth faria. O que interessa aqui é o que a borda promete: os códigos
// HTTP, a allowlist de partes do multipart, o 404 idêntico para recurso alheio
// e o fato de NADA ser escrito em disco.

const senhaDeTeste = "12345678909"

func (a *ambiente) handler(t *testing.T) *importer.Handler {
	t.Helper()
	return importer.NewHandler(a.svc, logging.Discard(), 0)
}

// identidade monta a Identity que o RequireAuth publicaria.
func identidade(householdID, userID string) session.Identity {
	return session.Identity{
		UserID:      userID,
		HouseholdID: householdID,
		Role:        session.RoleOwner,
		SessionID:   "00000000-0000-7000-c000-000000000001",
	}
}

// requisicao monta a requisição já autenticada.
func requisicao(t *testing.T, metodo, alvo string, corpo io.Reader, ident session.Identity) *http.Request {
	t.Helper()
	r := httptest.NewRequest(metodo, alvo, corpo)
	return r.WithContext(session.NewContext(r.Context(), ident))
}

// parte é um campo do multipart a montar.
type parte struct {
	nome     string
	arquivo  string // quando preenchido, a parte vai como arquivo
	conteudo []byte
}

// montarMultipart escreve o corpo e devolve o Content-Type com o boundary.
func montarMultipart(t *testing.T, partes ...parte) (*bytes.Buffer, string) {
	t.Helper()

	corpo := &bytes.Buffer{}
	w := multipart.NewWriter(corpo)
	for _, p := range partes {
		var campo io.Writer
		var err error
		if p.arquivo != "" {
			campo, err = w.CreateFormFile(p.nome, p.arquivo)
		} else {
			campo, err = w.CreateFormField(p.nome)
		}
		require.NoError(t, err)
		_, err = campo.Write(p.conteudo)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return corpo, w.FormDataContentType()
}

// envioValido é o formulário feliz.
func envioValido(t *testing.T, contaID string) (*bytes.Buffer, string) {
	return montarMultipart(t,
		parte{nome: "file", arquivo: "NU_2026-08.csv", conteudo: fixtureExtrato(t)},
		parte{nome: "accountId", conteudo: []byte(contaID)},
	)
}

func TestPostImportsAceitaOEnvioEDevolve201(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	corpo, tipo := envioValido(t, conta.ID)
	r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", tipo)

	rec := httptest.NewRecorder()
	a.handler(t).Create(rec, r)

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var view importer.BatchView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	assert.Equal(t, importer.BatchStatusPending, view.Status)
	assert.Equal(t, 13, view.RowCount)
}

func TestPostImportsExigeMultipart(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	r := requisicao(t, http.MethodPost, "/api/v1/imports",
		strings.NewReader(`{"accountId":"x"}`), identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	a.handler(t).Create(rec, r)

	assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
	assert.Contains(t, rec.Body.String(), "UNSUPPORTED_MEDIA_TYPE")
}

func TestPostImportsRecusaParteForaDaAllowlist(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	casos := []string{
		"householdId", // o clássico: casa vinda do cliente
		"AccountId",   // a allowlist é sensível a caixa de propósito
		"",            // parte sem nome
	}
	for _, nome := range casos {
		t.Run("parte_"+nome, func(t *testing.T) {
			corpo, tipo := montarMultipart(t,
				parte{nome: "file", arquivo: "e.csv", conteudo: fixtureExtrato(t)},
				parte{nome: "accountId", conteudo: []byte(conta.ID)},
				parte{nome: nome, conteudo: []byte("valor")},
			)
			r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
			r.Header.Set("Content-Type", tipo)

			rec := httptest.NewRecorder()
			a.handler(t).Create(rec, r)

			assert.Equal(t, http.StatusBadRequest, rec.Code, "parte desconhecida é 400, nunca ignorada")
			assert.Contains(t, rec.Body.String(), "VALIDATION_FAILED")
		})
	}
}

func TestPostImportsRecusaParteRepetida(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	corpo, tipo := montarMultipart(t,
		parte{nome: "file", arquivo: "e.csv", conteudo: fixtureExtrato(t)},
		parte{nome: "accountId", conteudo: []byte(conta.ID)},
		parte{nome: "accountId", conteudo: []byte("outra-coisa")},
	)
	r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", tipo)

	rec := httptest.NewRecorder()
	a.handler(t).Create(rec, r)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestPostImportsRecusaArquivoAcimaDoTeto(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	gigante := bytes.Repeat([]byte("a,b,c\n"), (importer.MaxUploadBytes/6)+16)
	require.Greater(t, len(gigante), importer.MaxUploadBytes)

	corpo, tipo := montarMultipart(t,
		parte{nome: "file", arquivo: "grande.csv", conteudo: gigante},
		parte{nome: "accountId", conteudo: []byte(conta.ID)},
	)
	r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", tipo)

	rec := httptest.NewRecorder()
	a.handler(t).Create(rec, r)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Contains(t, rec.Body.String(), "PAYLOAD_TOO_LARGE")
}

func TestPostImportsRecusaFormatoDesconhecido(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	corpo, tipo := montarMultipart(t,
		parte{nome: "file", arquivo: "c6.csv", conteudo: []byte("coluna1;coluna2\n1;2\n")},
		parte{nome: "accountId", conteudo: []byte(conta.ID)},
	)
	r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", tipo)

	rec := httptest.NewRecorder()
	a.handler(t).Create(rec, r)

	// É a resposta para arquivo do C6 enquanto os parsers dele não existem.
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "IMPORT_FORMAT_UNKNOWN")
}

func TestPostImportsRecusaFormatoForaDaAllowlist(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	corpo, tipo := montarMultipart(t,
		parte{nome: "file", arquivo: "e.csv", conteudo: fixtureExtrato(t)},
		parte{nome: "accountId", conteudo: []byte(conta.ID)},
		parte{nome: "format", conteudo: []byte("banco.inventado.v9")},
	)
	r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", tipo)

	rec := httptest.NewRecorder()
	a.handler(t).Create(rec, r)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"format"`)
}

func TestPostImportsRecusaFaturaEmContaCorrenteComCodigoProprio(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	corpo, tipo := montarMultipart(t,
		parte{nome: "file", arquivo: "fatura.csv", conteudo: fixtureFatura(t)},
		parte{nome: "accountId", conteudo: []byte(conta.ID)},
	)
	r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", tipo)

	rec := httptest.NewRecorder()
	a.handler(t).Create(rec, r)

	// Código próprio porque a AÇÃO da tela é outra: trocar a conta de destino,
	// não o arquivo.
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	assert.Contains(t, rec.Body.String(), "IMPORT_TARGET_MISMATCH")
}

func TestPostImportsComContaDeOutraCasaResponde404(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	alheia := a.conta(t, a.alheia.ID, "Conta Alheia", account.KindChecking, "nubank")

	corpo, tipo := envioValido(t, alheia.ID)
	r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", tipo)

	rec := httptest.NewRecorder()
	a.handler(t).Create(rec, r)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "NOT_FOUND")
}

// TestSenhaDoZipNaoVazaEmLugarNenhum é o critério de aceite 13: a senha não
// pode aparecer na resposta, no log, na auditoria nem no banco.
func TestSenhaDoZipNaoVazaEmLugarNenhum(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	var log bytes.Buffer
	handler := importer.NewHandler(a.svc, slog.New(slog.NewJSONHandler(&log, nil)), 0)

	corpo, tipo := montarMultipart(t,
		parte{nome: "file", arquivo: "e.csv", conteudo: fixtureExtrato(t)},
		parte{nome: "accountId", conteudo: []byte(conta.ID)},
		// Senha num CSV solto: é ignorada, e é exatamente por isso que serve de
		// sonda — ela percorre o caminho inteiro sem ser usada.
		parte{nome: "password", conteudo: []byte(senhaDeTeste)},
	)
	r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", tipo)

	rec := httptest.NewRecorder()
	handler.Create(rec, r)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	assert.NotContains(t, rec.Body.String(), senhaDeTeste, "a senha não volta na resposta")
	assert.NotContains(t, log.String(), senhaDeTeste, "a senha não entra em log")

	for _, e := range a.auditoria.entradas {
		assert.NotContains(t, e.Action+e.Entity+e.EntityID+e.IP, senhaDeTeste)
	}

	// E não está em coluna nenhuma do lote.
	var view importer.BatchView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	lote, err := a.repoImport.BatchByID(t.Context(), a.casa.ID, view.ID)
	require.NoError(t, err)
	serializado, err := json.Marshal(lote)
	require.NoError(t, err)
	assert.NotContains(t, string(serializado), senhaDeTeste)
}

// TestNadaEEscritoEmDisco observa o diretório temporário do processo durante um
// envio de 5 MiB.
//
// É o teste que prova, por OBSERVAÇÃO, o que o teste de código-fonte prova por
// leitura: ParseMultipartForm e ReadForm gravariam o excedente em disco, e o
// que eles gravariam aqui é extrato bancário (§6.2).
//
// Não é paralelo de propósito: ele mexe em variável de ambiente do processo.
func TestNadaEEscritoEmDisco(t *testing.T) {
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	temporario := t.TempDir()
	// os.TempDir() lê estas variáveis a cada chamada; redirecioná-las é o que
	// torna a observação determinística em vez de uma varredura do /tmp real.
	t.Setenv("TMPDIR", temporario)
	t.Setenv("TMP", temporario)
	t.Setenv("TEMP", temporario)
	require.Equal(t, temporario, filepath.Clean(os.TempDir()))

	// 5 MiB de CSV: bem acima dos 32 MiB? não — acima de qualquer defaultMaxMemory
	// que valha a pena, e o suficiente para o multipart da stdlib decidir
	// transbordar se alguém trocar MultipartReader por ReadForm.
	linha := []byte("04/08/2026,-1.00,11111111-1111-4111-8111-111111111101,Compra Exemplo\n")
	grande := append([]byte("Data,Valor,Identificador,Descrição\n"),
		bytes.Repeat(linha, 5_000)...)

	corpo, tipo := montarMultipart(t,
		parte{nome: "file", arquivo: "grande.csv", conteudo: grande},
		parte{nome: "accountId", conteudo: []byte(conta.ID)},
	)
	r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", tipo)

	rec := httptest.NewRecorder()
	a.handler(t).Create(rec, r)
	require.NotEqual(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	sobraram, err := os.ReadDir(temporario)
	require.NoError(t, err)
	assert.Empty(t, sobraram, "a importação não pode escrever NADA em disco")
}

// TestFonteNaoUsaLeitorQueGravaEmDisco é a trava de código: os três nomes
// proibidos nesta rota não podem aparecer no pacote.
//
// Um teste de observação pega o comportamento de hoje; este pega a REGRA, e
// falha no instante em que alguém "simplificar" a leitura do multipart.
//
// A varredura é sobre o CÓDIGO, não sobre o texto do arquivo: o fonte é
// reimpresso a partir da árvore sintática, sem comentários. Sem isso, o próprio
// comentário que explica a proibição faria o teste falhar — e o jeito de
// "consertar" seria apagar a explicação.
func TestFonteNaoUsaLeitorQueGravaEmDisco(t *testing.T) {
	t.Parallel()

	proibidos := []string{"ParseMultipartForm", "ReadForm", "CreateTemp", "WriteFile", "os.Create"}

	arquivos, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, arquivos)

	for _, caminho := range arquivos {
		if strings.HasSuffix(caminho, "_test.go") {
			continue
		}
		codigo := semComentarios(t, caminho)
		for _, proibido := range proibidos {
			if strings.Contains(codigo, proibido) {
				t.Errorf("%s usa %s, que grava extrato bancário em disco (spec 0004 §6.2)",
					caminho, proibido)
			}
		}
	}
}

// semComentarios devolve o código do arquivo sem nenhum comentário.
func semComentarios(t *testing.T, caminho string) string {
	t.Helper()

	fset := token.NewFileSet()
	arquivo, err := parser.ParseFile(fset, caminho, nil, parser.SkipObjectResolution)
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, printer.Fprint(&buf, fset, arquivo))
	return buf.String()
}

// --- revisão, confirmação e descarte --------------------------------------

func TestGetImportValidaLimitECursor(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	casos := map[string]string{
		"limit alto":   "?limit=10000",
		"limit zero":   "?limit=0",
		"limit texto":  "?limit=abc",
		"cursor texto": "?cursor=abc",
		"cursor zero":  "?cursor=0",
	}
	for nome, query := range casos {
		t.Run(nome, func(t *testing.T) {
			r := requisicao(t, http.MethodGet, "/api/v1/imports/"+lote.ID+query, nil,
				identidade(a.casa.ID, a.usuario.ID))
			r.SetPathValue("id", lote.ID)

			rec := httptest.NewRecorder()
			a.handler(t).Get(rec, r)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "recusar, nunca truncar em silêncio")
		})
	}
}

func TestLoteDeOutraCasaRespondeIgualALoteInexistente(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	const inexistente = "00000000-0000-7000-d000-000000000001"
	h := a.handler(t)

	// O ator é o da OUTRA casa; o lote é real. A resposta precisa ser byte a
	// byte igual à de um id que não existe (S1).
	chamar := func(t *testing.T, metodo, id string, corpo io.Reader, fn func(http.ResponseWriter, *http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		r := requisicao(t, metodo, "/api/v1/imports/"+id, corpo, identidade(a.alheia.ID, a.outroUsuario.ID))
		if corpo != nil {
			r.Header.Set("Content-Type", "application/json")
		}
		r.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		fn(rec, r)
		return rec
	}

	t.Run("get", func(t *testing.T) {
		alheio := chamar(t, http.MethodGet, lote.ID, nil, h.Get)
		fantasma := chamar(t, http.MethodGet, inexistente, nil, h.Get)
		assert.Equal(t, http.StatusNotFound, alheio.Code)
		assert.Equal(t, fantasma.Body.Bytes(), alheio.Body.Bytes())
	})

	t.Run("confirm", func(t *testing.T) {
		alheio := chamar(t, http.MethodPost, lote.ID, strings.NewReader(`{"decisions":[]}`), h.Confirm)
		fantasma := chamar(t, http.MethodPost, inexistente, strings.NewReader(`{"decisions":[]}`), h.Confirm)
		assert.Equal(t, http.StatusNotFound, alheio.Code)
		assert.Equal(t, fantasma.Body.Bytes(), alheio.Body.Bytes())
	})

	t.Run("delete", func(t *testing.T) {
		alheio := chamar(t, http.MethodDelete, lote.ID, nil, h.Delete)
		fantasma := chamar(t, http.MethodDelete, inexistente, nil, h.Delete)
		assert.Equal(t, http.StatusNotFound, alheio.Code)
		assert.Equal(t, fantasma.Body.Bytes(), alheio.Body.Bytes())
	})

	// E o lote continua lá para o dono.
	preview, err := a.svc.Preview(t.Context(), a.ator(), lote.ID, 0, 10)
	require.NoError(t, err)
	assert.Equal(t, importer.BatchStatusPending, preview.Batch.Status)
}

func TestConfirmRecusaCampoDesconhecidoNoCorpo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	corpos := map[string]string{
		"householdId":   `{"decisions":[],"householdId":"outra-casa"}`,
		"amountCents":   `{"decisions":[{"rowId":"x","action":"import","amountCents":1}]}`,
		"dedupKey":      `{"decisions":[{"rowId":"x","action":"import","dedupKey":"abc"}]}`,
		"sem decisions": `{}`,
	}
	for nome, corpo := range corpos {
		t.Run(nome, func(t *testing.T) {
			r := requisicao(t, http.MethodPost, "/api/v1/imports/"+lote.ID+"/confirm",
				strings.NewReader(corpo), identidade(a.casa.ID, a.usuario.ID))
			r.Header.Set("Content-Type", "application/json")
			r.SetPathValue("id", lote.ID)

			rec := httptest.NewRecorder()
			a.handler(t).Confirm(rec, r)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}

	// Nada foi gravado por nenhuma das tentativas.
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
}

func TestConfirmFelizDevolve200EOResultado(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	r := requisicao(t, http.MethodPost, "/api/v1/imports/"+lote.ID+"/confirm",
		strings.NewReader(`{"decisions":[]}`), identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", lote.ID)

	rec := httptest.NewRecorder()
	a.handler(t).Confirm(rec, r)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var res importer.ResultView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, 12, res.Imported)
	assert.Equal(t, 1, res.Skipped)
	assert.NotNil(t, res.BlockedRows, "a lista vem vazia, nunca nula")
}

func TestDeleteImportDevolve204(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	r := requisicao(t, http.MethodDelete, "/api/v1/imports/"+lote.ID, nil,
		identidade(a.casa.ID, a.usuario.ID))
	r.SetPathValue("id", lote.ID)

	rec := httptest.NewRecorder()
	a.handler(t).Delete(rec, r)
	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestRotasDeImportacaoExigemAutenticacao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	h := a.handler(t)

	// Sem identidade no contexto — o que acontece se alguém montar a rota sem
	// o RequireAuth na frente.
	rotas := map[string]func(http.ResponseWriter, *http.Request){
		"list":    h.List,
		"create":  h.Create,
		"get":     h.Get,
		"delete":  h.Delete,
		"confirm": h.Confirm,
	}
	for nome, fn := range rotas {
		t.Run(nome, func(t *testing.T) {
			rec := httptest.NewRecorder()
			fn(rec, httptest.NewRequest(http.MethodGet, "/api/v1/imports", nil))
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
		})
	}
}
