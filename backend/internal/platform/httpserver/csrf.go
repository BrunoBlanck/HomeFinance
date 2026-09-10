package httpserver

import (
	"net/http"
	"strings"
)

// Valores de Sec-Fetch-Site que o navegador envia.
const (
	fetchSiteSameOrigin = "same-origin"
	fetchSiteNone       = "none"
)

func isSafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

// CSRFGuard bloqueia requisições que mudam estado vindas de outra origem
// (ADR-013 / D10).
//
// A defesa é em duas camadas: os cookies são SameSite=Strict (o navegador
// nem manda a credencial num pedido cross-site) e aqui conferimos
// Sec-Fetch-Site / Origin contra a allowlist. Não usamos token
// double-submit: o backend não serve formulário HTML, então não há de onde
// embutir o token, e um token a mais seria só superfície nova.
//
// Regras:
//   - método seguro (GET/HEAD/OPTIONS): passa;
//   - Sec-Fetch-Site same-origin ou none: passa;
//   - Sec-Fetch-Site cross-site/same-site: exige Origin na allowlist;
//   - sem Sec-Fetch-Site e com Origin: exige allowlist ou a própria origem;
//   - sem Sec-Fetch-Site e sem Origin: passa (cliente não-navegador, sem
//     cookie ambiente — CSRF não se aplica).
func CSRFGuard(allowlist *OriginAllowlist) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
			origin := r.Header.Get("Origin")

			switch {
			case fetchSite == fetchSiteSameOrigin || fetchSite == fetchSiteNone:
				next.ServeHTTP(w, r)
				return
			case fetchSite != "":
				// Navegador declarou origem cruzada: só passa com Origin
				// explicitamente permitida.
				if origin != "" && allowlist.Allows(origin) {
					next.ServeHTTP(w, r)
					return
				}
				WriteError(w, http.StatusForbidden, CodeForbidden, MsgForbidden)
				return
			case origin == "":
				next.ServeHTTP(w, r)
				return
			case allowlist.Allows(origin) || isSameOrigin(origin, r):
				next.ServeHTTP(w, r)
				return
			default:
				WriteError(w, http.StatusForbidden, CodeForbidden, MsgForbidden)
				return
			}
		})
	}
}

// isSameOrigin compara a Origin declarada com o host da própria requisição.
// Aceitamos http e https do mesmo host: quem controla os dois esquemas do
// mesmo domínio já derrotou defesas bem maiores que esta.
func isSameOrigin(origin string, r *http.Request) bool {
	host := strings.ToLower(strings.TrimSpace(r.Host))
	if host == "" {
		return false
	}
	o := normalizeOrigin(origin)
	return o == "http://"+host || o == "https://"+host
}
