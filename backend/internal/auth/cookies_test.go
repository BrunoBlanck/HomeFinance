package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newJar(secure bool, domain string) *auth.CookieJar {
	return auth.NewCookieJar(auth.CookieOptions{
		Secure:     secure,
		Domain:     domain,
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 14 * 24 * time.Hour,
	})
}

// Critério de aceite 17 e ADR-013/D11.
func TestCookiesSegurosUsamPrefixoHost(t *testing.T) {
	t.Parallel()

	jar := newJar(true, "")
	rec := httptest.NewRecorder()
	jar.SetSessionCookies(rec, "jwt-access", "opaco-refresh")

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 2)

	byName := map[string]*http.Cookie{}
	for _, c := range cookies {
		byName[c.Name] = c
	}

	access := byName["__Host-hf_access"]
	refresh := byName["__Host-hf_refresh"]
	require.NotNil(t, access, "cookie de access precisa do prefixo __Host-")
	require.NotNil(t, refresh)

	for _, c := range []*http.Cookie{access, refresh} {
		assert.True(t, c.HttpOnly, "%s precisa ser HttpOnly", c.Name)
		assert.True(t, c.Secure, "%s precisa ser Secure", c.Name)
		assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
		// __Host- exige Path=/ e PROÍBE Domain.
		assert.Equal(t, "/", c.Path)
		assert.Empty(t, c.Domain)
	}

	assert.Equal(t, "jwt-access", access.Value)
	assert.Equal(t, "opaco-refresh", refresh.Value)
	assert.Equal(t, int((15 * time.Minute).Seconds()), access.MaxAge)
	assert.Equal(t, int((14 * 24 * time.Hour).Seconds()), refresh.MaxAge)
}

func TestCookiesInsegurosNaoUsamPrefixo(t *testing.T) {
	t.Parallel()

	jar := newJar(false, "")
	assert.Equal(t, "hf_access", jar.AccessCookieName())
	assert.Equal(t, "hf_refresh", jar.RefreshCookieName())

	rec := httptest.NewRecorder()
	jar.SetSessionCookies(rec, "a", "r")
	for _, c := range rec.Result().Cookies() {
		assert.False(t, c.Secure)
		assert.True(t, c.HttpOnly, "HttpOnly vale mesmo em desenvolvimento")
		assert.Equal(t, http.SameSiteStrictMode, c.SameSite)
	}
}

// __Host- proíbe o atributo Domain; o jar ignora o valor configurado quando
// Secure está ligado (a config já falha no boot nessa combinação).
func TestJarIgnoraDominioQuandoSeguro(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	newJar(true, "exemplo.com").SetSessionCookies(rec, "a", "r")

	for _, c := range rec.Result().Cookies() {
		assert.Empty(t, c.Domain)
	}
}

func TestClearSessionCookies(t *testing.T) {
	t.Parallel()

	jar := newJar(true, "")
	rec := httptest.NewRecorder()
	jar.ClearSessionCookies(rec)

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 2)
	for _, c := range cookies {
		assert.Empty(t, c.Value)
		assert.Negative(t, c.MaxAge, "MaxAge negativo apaga o cookie")
		assert.True(t, c.Expires.Before(time.Now()), "Expires no passado cobre navegador que ignora MaxAge")
		assert.True(t, c.HttpOnly)
		assert.Equal(t, "/", c.Path)
	}
}

func TestLeituraDeCookies(t *testing.T) {
	t.Parallel()

	jar := newJar(true, "")
	r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	r.AddCookie(&http.Cookie{Name: "__Host-hf_access", Value: "valor-access"})
	r.AddCookie(&http.Cookie{Name: "__Host-hf_refresh", Value: "valor-refresh"})

	assert.Equal(t, "valor-access", jar.ReadAccessToken(r))
	assert.Equal(t, "valor-refresh", jar.ReadRefreshToken(r))

	vazio := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", nil)
	assert.Empty(t, jar.ReadAccessToken(vazio))
	assert.Empty(t, jar.ReadRefreshToken(vazio))

	// Jar inseguro não enxerga cookie com prefixo, e vice-versa.
	assert.Empty(t, newJar(false, "").ReadAccessToken(r))
}
