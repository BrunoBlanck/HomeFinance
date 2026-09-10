package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
