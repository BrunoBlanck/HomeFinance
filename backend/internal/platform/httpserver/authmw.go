package httpserver

import (
	"net/http"

	"github.com/brunorblanck/homefinance/backend/internal/session"
)

// Authenticator transforma uma requisição em identidade autenticada.
//
// É uma interface, e não uma dependência direta do pacote auth, por dois
// motivos: quebra o ciclo (auth/handler.go usa este pacote) e deixa
// explícito que o middleware não sabe NADA sobre como o token é validado.
type Authenticator interface {
	Authenticate(r *http.Request) (session.Identity, error)
}

// RequireAuth exige uma identidade válida e a publica no contexto.
//
// docs/SEGURANCA.md §2 (BOLA, risco nº 1): a identidade — e portanto o
// household_id de toda operação — vem EXCLUSIVAMENTE do token assinado.
// Este middleware nunca lê household_id de corpo, query ou path, e nenhum
// handler protegido tem outra fonte de identidade.
//
// Não limpa cookies em caso de falha: um access token expirado é a situação
// normal, e o cliente precisa do cookie de refresh intacto para renovar.
func RequireAuth(a Authenticator) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if a == nil {
				WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, MsgUnauthenticated)
				return
			}
			ident, err := a.Authenticate(r)
			if err != nil || !ident.Valid() {
				WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, MsgUnauthenticated)
				return
			}
			next.ServeHTTP(w, r.WithContext(session.NewContext(r.Context(), ident)))
		})
	}
}
