package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
)

// D16 da spec 0001: X-Forwarded-For só conta com proxies confiáveis
// declarados. Confiar por padrão anularia todo o rate limit por IP.
func TestClientIP(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome       string
		remoteAddr string
		xff        []string
		proxies    int
		esperado   string
	}{
		{nome: "sem proxy usa RemoteAddr", remoteAddr: "203.0.113.9:4242", esperado: "203.0.113.9"},
		{
			nome:       "sem proxy ignora XFF forjado",
			remoteAddr: "203.0.113.9:4242",
			xff:        []string{"1.2.3.4"},
			proxies:    0,
			esperado:   "203.0.113.9",
		},
		{
			nome:       "um proxy confiavel pega a ultima entrada",
			remoteAddr: "10.0.0.1:1",
			xff:        []string{"1.2.3.4, 198.51.100.7"},
			proxies:    1,
			esperado:   "198.51.100.7",
		},
		{
			nome:       "dois proxies confiaveis pegam a penultima",
			remoteAddr: "10.0.0.1:1",
			xff:        []string{"1.2.3.4, 198.51.100.7, 10.0.0.2"},
			proxies:    2,
			esperado:   "198.51.100.7",
		},
		{
			nome:       "cadeia menor que a contagem cai na primeira entrada",
			remoteAddr: "10.0.0.1:1",
			xff:        []string{"198.51.100.7"},
			proxies:    3,
			esperado:   "198.51.100.7",
		},
		{
			nome:       "XFF invalido cai no RemoteAddr",
			remoteAddr: "203.0.113.9:4242",
			xff:        []string{"nao-e-ip"},
			proxies:    1,
			esperado:   "203.0.113.9",
		},
		{
			nome:       "varios cabecalhos XFF sao concatenados",
			remoteAddr: "10.0.0.1:1",
			xff:        []string{"1.2.3.4", "198.51.100.7"},
			proxies:    1,
			esperado:   "198.51.100.7",
		},
		{nome: "ipv6", remoteAddr: "[2001:db8::1]:443", esperado: "2001:db8::1"},
		{nome: "RemoteAddr sem porta", remoteAddr: "203.0.113.9", esperado: "203.0.113.9"},
		{nome: "RemoteAddr ilegivel", remoteAddr: "pipe", esperado: "unknown"},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, "/t", nil)
			r.RemoteAddr = c.remoteAddr
			for _, v := range c.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			assert.Equal(t, c.esperado, httpserver.ClientIP(r, c.proxies))
		})
	}
}
