package httpserver_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type payload struct {
	Nome  string `json:"nome"`
	Idade int    `json:"idade"`
}

func postJSON(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestDecodeJSONFeliz(t *testing.T) {
	t.Parallel()

	got, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), postJSON(`{"nome":"bruno","idade":40}`))
	require.NoError(t, err)
	assert.Equal(t, payload{Nome: "bruno", Idade: 40}, got)
}

// Critério de aceite 25 da spec 0001.
func TestDecodeJSONRejeitaCampoDesconhecido(t *testing.T) {
	t.Parallel()

	_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), postJSON(`{"nome":"b","admin":true}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, httpserver.ErrMalformedBody)

	rec := httptest.NewRecorder()
	httpserver.WriteDecodeError(rec, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDecodeJSONRejeitaContentTypeErrado(t *testing.T) {
	t.Parallel()

	casos := []string{"", "text/plain", "application/x-www-form-urlencoded", "lixo/(("}
	for _, ct := range casos {
		r := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader(`{}`))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), r)
		require.Error(t, err, "content-type %q deveria ser rejeitado", ct)
		assert.ErrorIs(t, err, httpserver.ErrUnsupportedMediaType)

		rec := httptest.NewRecorder()
		httpserver.WriteDecodeError(rec, err)
		assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
	}
}

func TestDecodeJSONAceitaCharsetNoContentType(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader(`{"nome":"x"}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), r)
	assert.NoError(t, err)
}

func TestDecodeJSONRejeitaCorpoVazio(t *testing.T) {
	t.Parallel()

	_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), postJSON(``))
	require.Error(t, err)
	assert.ErrorIs(t, err, httpserver.ErrMalformedBody)
}

func TestDecodeJSONRejeitaMaisDeUmValor(t *testing.T) {
	t.Parallel()

	_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), postJSON(`{"nome":"a"}{"nome":"b"}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, httpserver.ErrMalformedBody)
}

// Critério de aceite 25: corpo acima do limite vira 413.
func TestDecodeJSONRespeitaMaxBytes(t *testing.T) {
	t.Parallel()

	grande := `{"nome":"` + strings.Repeat("a", 5000) + `"}`
	var capturado error
	h := httpserver.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, capturado = httpserver.DecodeJSON[payload](w, r)
			httpserver.WriteDecodeError(w, capturado)
		}),
		httpserver.MaxBytes(512),
	)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, postJSON(grande))

	require.Error(t, capturado)
	assert.ErrorIs(t, capturado, httpserver.ErrPayloadTooLarge)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Contains(t, rec.Body.String(), httpserver.CodePayloadTooLarge)
}

func TestDecodeJSONNaoEcoaDetalheInterno(t *testing.T) {
	t.Parallel()

	_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), postJSON(`{"idade":"texto"}`))
	require.Error(t, err)

	rec := httptest.NewRecorder()
	httpserver.WriteDecodeError(rec, err)
	body, _ := io.ReadAll(rec.Body)
	assert.NotContains(t, string(body), "json:")
	assert.NotContains(t, string(body), "payload")
	assert.Contains(t, string(body), httpserver.MsgValidationFailed)
}
