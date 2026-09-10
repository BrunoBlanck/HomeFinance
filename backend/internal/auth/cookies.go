package auth

import (
	"net/http"
	"time"
)

// Nomes-base dos cookies de sessão.
const (
	AccessCookieBaseName  = "hf_access"
	RefreshCookieBaseName = "hf_refresh"
	hostPrefix            = "__Host-"
)

// CookieJar escreve e limpa os cookies de sessão (ADR-013 / D11).
//
// Quando Secure está ligado, os cookies ganham o prefixo __Host-, que o
// navegador só aceita com Secure, Path=/ e SEM atributo Domain. Isso impede
// que um subdomínio comprometido (ou um MITM em http://) sobrescreva o cookie
// de sessão — o ataque clássico de fixação de sessão.
//
// O custo aceito é o Path=/ obrigatório: não dá para restringir o refresh a
// /api/v1/auth. Em troca, SameSite=Strict garante que o cookie não sai em
// nenhuma navegação cross-site.
type CookieJar struct {
	secure     bool
	domain     string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

// CookieOptions configura o jar.
type CookieOptions struct {
	Secure     bool
	Domain     string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

// NewCookieJar monta o jar. Com Secure, o Domain é ignorado — o prefixo
// __Host- proíbe esse atributo, e a config já falha no boot se os dois forem
// declarados juntos.
func NewCookieJar(opts CookieOptions) *CookieJar {
	domain := opts.Domain
	if opts.Secure {
		domain = ""
	}
	return &CookieJar{
		secure:     opts.Secure,
		domain:     domain,
		accessTTL:  opts.AccessTTL,
		refreshTTL: opts.RefreshTTL,
	}
}

// AccessCookieName devolve o nome efetivo do cookie de access.
func (j *CookieJar) AccessCookieName() string { return j.name(AccessCookieBaseName) }

// RefreshCookieName devolve o nome efetivo do cookie de refresh.
func (j *CookieJar) RefreshCookieName() string { return j.name(RefreshCookieBaseName) }

func (j *CookieJar) name(base string) string {
	if j.secure {
		return hostPrefix + base
	}
	return base
}

// SetSessionCookies grava os dois cookies da sessão.
func (j *CookieJar) SetSessionCookies(w http.ResponseWriter, accessToken, refreshToken string) {
	http.SetCookie(w, j.cookie(j.AccessCookieName(), accessToken, j.accessTTL))
	http.SetCookie(w, j.cookie(j.RefreshCookieName(), refreshToken, j.refreshTTL))
}

// ClearSessionCookies apaga os dois cookies.
//
// Usa MaxAge negativo E Expires no passado: alguns navegadores antigos só
// honram um dos dois, e um cookie de sessão que sobrevive ao logout é uma
// falha de segurança, não um detalhe de compatibilidade.
func (j *CookieJar) ClearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, j.expired(j.AccessCookieName()))
	http.SetCookie(w, j.expired(j.RefreshCookieName()))
}

// ReadAccessToken lê o cookie de access.
func (j *CookieJar) ReadAccessToken(r *http.Request) string {
	return readCookie(r, j.AccessCookieName())
}

// ReadRefreshToken lê o cookie de refresh.
func (j *CookieJar) ReadRefreshToken(r *http.Request) string {
	return readCookie(r, j.RefreshCookieName())
}

func readCookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

// cookie monta um cookie de sessão.
//
// #nosec G124 -- HttpOnly e SameSite=Strict são SEMPRE aplicados. Secure vem
// de COOKIE_SECURE, que só pode ser false em desenvolvimento: a validação de
// configuração recusa o boot com COOKIE_SECURE=false em produção (D8).
func (j *CookieJar) cookie(name, value string, ttl time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Domain:   j.domain,
		MaxAge:   int(ttl.Seconds()),
		Secure:   j.secure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

// expired monta o cookie de remoção.
//
// #nosec G124 -- mesmos atributos do cookie de sessão; o valor é vazio e o
// Max-Age é negativo, então não há nada a proteger neste cookie.
func (j *CookieJar) expired(name string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		Domain:   j.domain,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
		Secure:   j.secure,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}
