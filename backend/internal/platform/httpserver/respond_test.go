package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestWriteErrorUsaFormatoUnico(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpserver.WriteError(rec, http.StatusUnauthorized, httpserver.CodeInvalidCode, httpserver.MsgInvalidCode)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.JSONEq(t, `{"error":{"code":"INVALID_CODE","message":"Código inválido ou expirado."}}`, rec.Body.String())
	// Sem "fields" fora de VALIDATION_FAILED (§3 da spec 0001).
	assert.NotContains(t, rec.Body.String(), "fields")
}

func TestWriteValidationErrorTemCampos(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpserver.WriteValidationError(rec, map[string]string{"password": "mínimo de 12 caracteres"})

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var env httpserver.ErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
	assert.Equal(t, "mínimo de 12 caracteres", env.Error.Fields["password"])
}

func TestCorpoDeErroEhDeterministico(t *testing.T) {
	t.Parallel()

	// Base do §3.12: dois caminhos diferentes têm de gerar bytes iguais.
	primeiro := httpserver.MarshalErrorEnvelope(httpserver.CodeInvalidCode, httpserver.MsgInvalidCode)
	for range 20 {
		assert.Equal(t, primeiro, httpserver.MarshalErrorEnvelope(httpserver.CodeInvalidCode, httpserver.MsgInvalidCode))
	}
}

func TestCamposDeValidacaoSaoOrdenados(t *testing.T) {
	t.Parallel()

	corpo := func() string {
		rec := httptest.NewRecorder()
		httpserver.WriteValidationError(rec, map[string]string{
			"zeta": "z", "alpha": "a", "meio": "m",
		})
		return rec.Body.String()
	}
	primeiro := corpo()
	for range 20 {
		assert.Equal(t, primeiro, corpo(), "mapa de campos precisa serializar em ordem estável")
	}
}

func TestWriteJSONDefineContentLength(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpserver.WriteJSON(rec, http.StatusOK, map[string]string{"status": "ok"})

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "15", rec.Header().Get("Content-Length"))
	assert.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
}

func TestWriteJSONComValorNaoSerializavelVira500(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpserver.WriteJSON(rec, http.StatusOK, make(chan int))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), httpserver.CodeInternalError)
}

// KEYWORD_TAKEN (spec 0005 §4.1) é 409 com o mesmo envelope dos demais: o
// código decide a ação da tela, `fields.keyword` é a palavra recusada e
// `fields.ownerId` é a categoria/conta DA CASA que já a tem.
func TestWriteConflictEscreve409ComCampos(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpserver.WriteConflict(rec, httpserver.CodeKeywordTaken, httpserver.MsgKeywordTaken,
		map[string]string{"keyword": "padaria", "ownerId": "11111111-1111-4111-8111-111111111111"})

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	assert.Equal(t, strconv.Itoa(rec.Body.Len()), rec.Header().Get("Content-Length"))

	var env httpserver.ErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, "KEYWORD_TAKEN", env.Error.Code)
	assert.Equal(t, "Esta palavra-chave já está em uso nesta casa.", env.Error.Message)
	assert.Equal(t, "padaria", env.Error.Fields["keyword"])
	assert.Equal(t, "11111111-1111-4111-8111-111111111111", env.Error.Fields["ownerId"])
}

// Na corrida rara em que o índice único decide, a dona não é conhecida: o
// mapa vem só com a palavra, e `ownerId` simplesmente não aparece — nunca
// vazio, nunca nulo.
func TestWriteConflictSemDonaOmiteOwnerId(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpserver.WriteConflict(rec, httpserver.CodeKeywordTaken, httpserver.MsgKeywordTaken,
		map[string]string{"keyword": "padaria"})

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.JSONEq(t,
		`{"error":{"code":"KEYWORD_TAKEN","message":"Esta palavra-chave já está em uso nesta casa.","fields":{"keyword":"padaria"}}}`,
		rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "ownerId")
}

// Sem campos, o envelope é idêntico ao de WriteError — `fields` some em vez
// de sair como `null` ou `{}`.
func TestWriteConflictSemCamposOmiteFields(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpserver.WriteConflict(rec, httpserver.CodeKeywordTaken, httpserver.MsgKeywordTaken, nil)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Equal(t, string(httpserver.MarshalErrorEnvelope(httpserver.CodeKeywordTaken, httpserver.MsgKeywordTaken)), rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "fields")
}

// CONFLICT (spec 0005 §13, ADR-028d) é 409 SEM campos: não há o que corrigir
// no formulário, e citar qual lançamento mudou seria vazar o estado de outra
// requisição. A mensagem é genérica e fixa — a tela reage pelo código.
func TestWriteConflictComCodeConflictVaiSemCamposEComMensagemGenerica(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	httpserver.WriteConflict(rec, httpserver.CodeConflict, httpserver.MsgConflict, nil)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.JSONEq(t,
		`{"error":{"code":"CONFLICT","message":"Os dados mudaram enquanto a operação rodava. Confira a prévia de novo."}}`,
		rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "fields")
	assert.Equal(t, string(httpserver.MarshalErrorEnvelope(httpserver.CodeConflict, httpserver.MsgConflict)), rec.Body.String())
}

// O conjunto de códigos é FECHADO (ErrorCode do contrato): todo código que o
// pacote publica precisa estar no enum da spec, e o contrário — um valor do
// enum sem constante aqui — também é divergência.
func TestCodigosDeErroBatemComOEnumDoContrato(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../../api/openapi.yaml")
	require.NoError(t, err)
	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Enum []string `yaml:"enum"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	naSpec := doc.Components.Schemas["ErrorCode"].Enum
	require.NotEmpty(t, naSpec, "o contrato precisa publicar o enum ErrorCode")

	noCodigo := []string{
		httpserver.CodeValidationFailed, httpserver.CodeInvalidCredentials, httpserver.CodeInvalidCode,
		httpserver.CodeEmailNotVerified, httpserver.CodeUnauthenticated, httpserver.CodeInvalidSession,
		httpserver.CodeForbidden, httpserver.CodeNotFound, httpserver.CodeMethodNotAllowed,
		httpserver.CodeRateLimited, httpserver.CodePayloadTooLarge, httpserver.CodeUnsupportedMediaType,
		httpserver.CodeServiceUnavailable, httpserver.CodeInternalError, httpserver.CodeResourceInUse,
		httpserver.CodeKeywordTaken, httpserver.CodeConflict,
		httpserver.CodeImportPasswordRequired, httpserver.CodeImportPasswordInvalid,
		httpserver.CodeImportFormatUnknown, httpserver.CodeImportFormatAmbiguous,
		httpserver.CodeImportTargetMismatch, httpserver.CodeImportFileRejected,
	}
	assert.ElementsMatch(t, naSpec, noCodigo, "ErrorCode do contrato e as constantes do pacote divergiram")
}
