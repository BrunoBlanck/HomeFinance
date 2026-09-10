package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
)

// Critério de aceite 26 da spec 0001: POST com Origin estranha vira 403.
func TestCSRFGuard(t *testing.T) {
	t.Parallel()

	allow := httpserver.NewOriginAllowlist([]string{"https://app.exemplo.com"})
	h := httpserver.CSRFGuard(allow)(okHandler())

	casos := []struct {
		nome      string
		metodo    string
		origin    string
		fetchSite string
		host      string
		esperado  int
	}{
		{nome: "GET sempre passa", metodo: http.MethodGet, origin: "https://malicioso.example", esperado: http.StatusOK},
		{nome: "HEAD sempre passa", metodo: http.MethodHead, origin: "https://malicioso.example", esperado: http.StatusOK},
		{nome: "POST same-origin", metodo: http.MethodPost, fetchSite: "same-origin", esperado: http.StatusOK},
		{nome: "POST sec-fetch none", metodo: http.MethodPost, fetchSite: "none", esperado: http.StatusOK},
		{nome: "POST cross-site com origem permitida", metodo: http.MethodPost, fetchSite: "cross-site", origin: "https://app.exemplo.com", esperado: http.StatusOK},
		{nome: "POST cross-site com origem estranha", metodo: http.MethodPost, fetchSite: "cross-site", origin: "https://malicioso.example", esperado: http.StatusForbidden},
		{nome: "POST cross-site sem origem", metodo: http.MethodPost, fetchSite: "cross-site", esperado: http.StatusForbidden},
		{nome: "POST same-site com origem estranha", metodo: http.MethodPost, fetchSite: "same-site", origin: "https://outro.exemplo.com", esperado: http.StatusForbidden},
		{nome: "POST sem sec-fetch e com origem estranha", metodo: http.MethodPost, origin: "https://malicioso.example", esperado: http.StatusForbidden},
		{nome: "POST sem sec-fetch e com origem permitida", metodo: http.MethodPost, origin: "https://app.exemplo.com", esperado: http.StatusOK},
		{nome: "POST sem sec-fetch e com o proprio host", metodo: http.MethodPost, origin: "http://api.local", host: "api.local", esperado: http.StatusOK},
		{nome: "POST sem cabecalho de navegador (cliente nao-browser)", metodo: http.MethodPost, esperado: http.StatusOK},
		{nome: "DELETE cross-site bloqueado", metodo: http.MethodDelete, fetchSite: "cross-site", origin: "https://malicioso.example", esperado: http.StatusForbidden},
		{nome: "PATCH com origem estranha bloqueado", metodo: http.MethodPatch, origin: "https://malicioso.example", esperado: http.StatusForbidden},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(c.metodo, "/t", nil)
			if c.origin != "" {
				r.Header.Set("Origin", c.origin)
			}
			if c.fetchSite != "" {
				r.Header.Set("Sec-Fetch-Site", c.fetchSite)
			}
			if c.host != "" {
				r.Host = c.host
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			assert.Equal(t, c.esperado, rec.Code)
			if c.esperado == http.StatusForbidden {
				assert.Contains(t, rec.Body.String(), httpserver.CodeForbidden)
			}
		})
	}
}
