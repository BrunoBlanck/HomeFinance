package httpserver

import "net/http"

// SecurityHeaders instala os cabeçalhos de defesa em TODA resposta
// (docs/SEGURANCA.md §5).
//
// A API só devolve JSON e nunca é embutida em página: por isso a CSP pode ser
// a mais restritiva possível (default-src 'none') e o frame-ancestors 'none'
// fecha clickjacking mesmo em navegador que ignore X-Frame-Options.
//
// hsts liga o Strict-Transport-Security; só deve ser true quando o serviço
// realmente está atrás de TLS, senão o navegador trava o domínio em https
// durante o desenvolvimento.
func SecurityHeaders(hsts bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'; sandbox")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()")
			// Dado financeiro e resposta autenticada nunca podem ficar em
			// cache compartilhado nem no disco do navegador.
			h.Set("Cache-Control", "no-store")
			h.Set("Pragma", "no-cache")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}
