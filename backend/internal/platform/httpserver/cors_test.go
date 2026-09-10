package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		httpserver.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
}

// Critério de aceite 26 da spec 0001.
func TestCORSSoEcoaOrigemDaAllowlist(t *testing.T) {
	t.Parallel()

	allow := httpserver.NewOriginAllowlist([]string{"https://app.exemplo.com"})
	h := httpserver.CORS(allow)(okHandler())

	t.Run("origem permitida", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/t", nil)
		r.Header.Set("Origin", "https://app.exemplo.com")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		assert.Equal(t, "https://app.exemplo.com", rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", rec.Header().Get("Access-Control-Allow-Credentials"))
		assert.Contains(t, rec.Header().Values("Vary"), "Origin")
	})

	t.Run("origem estranha nao recebe cabecalho", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/t", nil)
		r.Header.Set("Origin", "https://malicioso.example")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
		assert.Equal(t, http.StatusOK, rec.Code, "a requisição em si não é bloqueada; o navegador é quem barra a leitura")
	})
}

func TestCORSNuncaRefleteCuringa(t *testing.T) {
	t.Parallel()

	// Mesmo que alguém configure "*", a allowlist descarta.
	allow := httpserver.NewOriginAllowlist([]string{"*"})
	assert.True(t, allow.Empty())

	h := httpserver.CORS(allow)(okHandler())
	r := httptest.NewRequest(http.MethodGet, "/t", nil)
	r.Header.Set("Origin", "https://qualquer.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSPreflight(t *testing.T) {
	t.Parallel()

	allow := httpserver.NewOriginAllowlist([]string{"https://app.exemplo.com"})
	h := httpserver.CORS(allow)(okHandler())

	t.Run("permitido", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodOptions, "/t", nil)
		r.Header.Set("Origin", "https://app.exemplo.com")
		r.Header.Set("Access-Control-Request-Method", http.MethodPost)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		assert.Equal(t, http.StatusNoContent, rec.Code)
		assert.Contains(t, rec.Header().Get("Access-Control-Allow-Methods"), "POST")
		assert.Equal(t, "https://app.exemplo.com", rec.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("origem desconhecida", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodOptions, "/t", nil)
		r.Header.Set("Origin", "https://malicioso.example")
		r.Header.Set("Access-Control-Request-Method", http.MethodPost)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Contains(t, rec.Body.String(), httpserver.CodeForbidden)
	})
}

func TestAllowlistNormalizaOrigem(t *testing.T) {
	t.Parallel()

	allow := httpserver.NewOriginAllowlist([]string{" HTTPS://App.Exemplo.com/ "})
	assert.True(t, allow.Allows("https://app.exemplo.com"))
	assert.True(t, allow.Allows("https://APP.exemplo.com"))
	assert.False(t, allow.Allows("http://app.exemplo.com"), "esquema faz parte da origem")
	assert.False(t, allow.Allows("https://app.exemplo.com.malicioso.example"))
	assert.False(t, allow.Allows(""))
}
