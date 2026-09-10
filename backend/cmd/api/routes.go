package main

import (
	"net/http"

	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/user"
)

// APIBasePath é o prefixo de todas as rotas (§3 da spec 0001).
const APIBasePath = "/api/v1"

// Route é uma linha da tabela de rotas.
//
// A tabela é a FONTE ÚNICA das rotas do serviço: o teste de aderência
// (routes_test.go) compara este conjunto com api/openapi.yaml e quebra o
// build se divergirem (ADR-011).
type Route struct {
	Method      string
	Pattern     string // relativo a APIBasePath, ex.: "/auth/login"
	Handler     http.HandlerFunc
	Middlewares []httpserver.Middleware
}

// FullPattern devolve o padrão do net/http.ServeMux (Go 1.22+).
func (r Route) FullPattern() string { return r.Method + " " + APIBasePath + r.Pattern }

// routeLimiters agrupa os limitadores por IP de cada rota.
// É um tipo VALOR com campos ponteiro: o zero-value é utilizável (limitador
// nil = sem limite), o que permite ao teste montar a tabela sem dependências.
type routeLimiters struct {
	login          *httpserver.Limiter
	register       *httpserver.Limiter
	resendCode     *httpserver.Limiter
	forgotPassword *httpserver.Limiter
	verifyEmail    *httpserver.Limiter
	resetPassword  *httpserver.Limiter
	refresh        *httpserver.Limiter
	health         *httpserver.Limiter
}

// routeDeps são as dependências para montar a tabela.
type routeDeps struct {
	auth              *auth.Handler
	user              *user.Handler
	ready             http.HandlerFunc
	limiters          routeLimiters
	requireAuth       httpserver.Middleware
	trustedProxyCount int
}

// buildRoutes devolve a tabela completa.
//
// Precisa continuar funcionando com routeDeps{} zerado: o routes_test só
// compara (método, padrão) e não deve precisar de banco, config nem mailer.
func buildRoutes(d routeDeps) []Route {
	ip := httpserver.IPKey(d.trustedProxyCount)
	limit := func(l *httpserver.Limiter) []httpserver.Middleware {
		return []httpserver.Middleware{httpserver.RateLimit(l, ip)}
	}

	// Sem dependência de banco, o readiness responde 503 — que é a resposta
	// correta, e mantém a tabela completa para o teste de aderência.
	ready := d.ready
	if ready == nil {
		ready = httpserver.Ready(nil, nil)
	}

	return []Route{
		// D17: /health não toca no banco e não é limitado — é a sonda de
		// liveness do orquestrador. /health/ready faz ping e é limitado.
		{
			Method:  http.MethodGet,
			Pattern: "/health",
			Handler: httpserver.Health(),
		},
		{
			Method:      http.MethodGet,
			Pattern:     "/health/ready",
			Handler:     ready,
			Middlewares: limit(d.limiters.health),
		},

		{
			Method:      http.MethodPost,
			Pattern:     "/auth/register",
			Handler:     d.auth.Register,
			Middlewares: limit(d.limiters.register),
		},
		{
			Method:      http.MethodPost,
			Pattern:     "/auth/verify-email",
			Handler:     d.auth.VerifyEmail,
			Middlewares: limit(d.limiters.verifyEmail),
		},
		{
			Method:      http.MethodPost,
			Pattern:     "/auth/resend-code",
			Handler:     d.auth.ResendCode,
			Middlewares: limit(d.limiters.resendCode),
		},
		{
			Method:      http.MethodPost,
			Pattern:     "/auth/login",
			Handler:     d.auth.Login,
			Middlewares: limit(d.limiters.login),
		},
		{
			Method:      http.MethodPost,
			Pattern:     "/auth/refresh",
			Handler:     d.auth.Refresh,
			Middlewares: limit(d.limiters.refresh),
		},
		{
			Method:  http.MethodPost,
			Pattern: "/auth/logout",
			Handler: d.auth.Logout,
		},
		{
			Method:      http.MethodPost,
			Pattern:     "/auth/forgot-password",
			Handler:     d.auth.ForgotPassword,
			Middlewares: limit(d.limiters.forgotPassword),
		},
		{
			Method:      http.MethodPost,
			Pattern:     "/auth/reset-password",
			Handler:     d.auth.ResetPassword,
			Middlewares: limit(d.limiters.resetPassword),
		},

		{
			Method:      http.MethodGet,
			Pattern:     "/me",
			Handler:     d.user.Me,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
	}
}

// newMux registra a tabela num ServeMux nativo (ADR-001).
func newMux(routes []Route) *http.ServeMux {
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.Handle(rt.FullPattern(), httpserver.Chain(rt.Handler, rt.Middlewares...))
	}
	return mux
}
