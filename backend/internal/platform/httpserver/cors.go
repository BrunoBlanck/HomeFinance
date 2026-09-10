package httpserver

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// OriginAllowlist decide quais origens podem falar com a API.
//
// Nunca existe curinga: a API responde com credenciais (cookies) e
// "Access-Control-Allow-Origin: *" é incompatível com
// "Access-Control-Allow-Credentials: true" (docs/SEGURANCA.md §5).
// A validação de config já rejeita "*" no boot.
type OriginAllowlist struct {
	allowed map[string]struct{}
}

// NewOriginAllowlist normaliza e indexa as origens configuradas.
func NewOriginAllowlist(origins []string) *OriginAllowlist {
	set := make(map[string]struct{}, len(origins))
	for _, o := range origins {
		if n := normalizeOrigin(o); n != "" && n != "*" {
			set[n] = struct{}{}
		}
	}
	return &OriginAllowlist{allowed: set}
}

// Allows informa se a origem está na allowlist.
func (a *OriginAllowlist) Allows(origin string) bool {
	if a == nil || len(a.allowed) == 0 {
		return false
	}
	n := normalizeOrigin(origin)
	if n == "" {
		return false
	}
	_, ok := a.allowed[n]
	return ok
}

// Empty informa se nenhuma origem foi configurada.
func (a *OriginAllowlist) Empty() bool { return a == nil || len(a.allowed) == 0 }

func normalizeOrigin(o string) string {
	o = strings.TrimSpace(o)
	o = strings.TrimSuffix(o, "/")
	return strings.ToLower(o)
}

const corsMaxAge = 10 * time.Minute

// CORS aplica a política de origem cruzada. A origem é sempre ecoada apenas
// quando bate exatamente na allowlist — nunca refletida sem checagem.
func CORS(allowlist *OriginAllowlist) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Vary é obrigatório mesmo quando a origem é negada: sem ele um
			// cache intermediário serviria a resposta de uma origem para outra.
			w.Header().Add("Vary", "Origin")

			if origin != "" && allowlist.Allows(origin) {
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", normalizeOrigin(origin))
				h.Set("Access-Control-Allow-Credentials", "true")
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h := w.Header()
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
				if origin == "" || !allowlist.Allows(origin) {
					// Preflight de origem desconhecida: 403 sem cabeçalho de
					// permissão. O navegador bloqueia a requisição real.
					WriteError(w, http.StatusForbidden, CodeForbidden, MsgForbidden)
					return
				}
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
				h.Set("Access-Control-Allow-Headers", "Content-Type")
				h.Set("Access-Control-Max-Age", strconv.Itoa(int(corsMaxAge.Seconds())))
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
