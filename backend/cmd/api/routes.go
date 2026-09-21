package main

import (
	"net/http"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/aiimport"
	"github.com/brunorblanck/homefinance/backend/internal/aiprompt"
	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/dashboard"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
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

	// A importação tem limitadores próprios porque POST /imports é a rota mais
	// cara do sistema: ela infla, parseia e varre uma janela de deduplicação
	// inteira (§5.6 da spec 0004).
	//
	// Os dois primeiros são por CASA — a chave é o HMAC do household_id, nunca
	// o id em claro —, e o terceiro é o teto por IP da mesma rota.
	importUpload   *httpserver.Limiter
	importConfirm  *httpserver.Limiter
	importUploadIP *httpserver.Limiter

	// autoCategorize é POR CASA (mesma chave HMAC do confirm) e tem balde
	// próprio: cada uso gasta duas chamadas — prévia e confirmação — e dividir
	// o balde com o confirm faria uma importação grande trancar a
	// categorização, ou o contrário (spec 0005, emenda §10.8).
	autoCategorize *httpserver.Limiter

	// transferDetect é POR CASA e em balde separado do autoCategorize (spec
	// 0005 §13.1.8): mesma classe de escrita em massa, mesma cota, e um uso
	// não tranca o outro.
	transferDetect *httpserver.Limiter

	// investmentDetect é POR CASA e em balde separado dos dois acima (spec
	// 0006 §3.3.5): mesma classe de escrita em massa, mesma cota, estouro
	// pequeno — a execução real segura conexão do pool dentro da transação.
	investmentDetect *httpserver.Limiter

	// aiExport é POR CASA e cobre GET /ai/export-prompt (spec 0010 §8.6).
	// Balde próprio, separado dos dois baldes do import de IA: exportar o
	// prompt não pode trancar a conferência do JSON, e a pessoa usa os três
	// na mesma sentada. É LEITURA, mas não é barata — uma chamada agrega até
	// 3 meses por `description_norm`, coluna sem índice próprio.
	aiExport *httpserver.Limiter

	// aiImportPreview e aiImportConfirm são POR CASA e cobrem as duas rotas
	// do import de IA (spec 0010 §8.6), em baldes separados entre si e do
	// export: a pessoa usa os três na mesma sentada, e um não pode trancar o
	// outro. A prévia roda o `internal/textmatch` (e por isso entra na conta
	// de heap de textmatch/matcher.go); o confirm grava numa transação e
	// segura conexão do pool até terminar.
	aiImportPreview *httpserver.Limiter
	aiImportConfirm *httpserver.Limiter

	// transactionUpdate é POR CASA e cobre PATCH /transactions/{id} (emenda
	// §11). Não é escrita em massa — uma linha por chamada —, mas escreve e
	// AUDITA a cada chamada, e até 17/09/2026 só o balde global por IP a
	// cobria. Teto generoso (rajada de categorização é uso legítimo) e finito.
	transactionUpdate *httpserver.Limiter
}

// routeDeps são as dependências para montar a tabela.
type routeDeps struct {
	auth          *auth.Handler
	user          *user.Handler
	account       *account.Handler
	category      *category.Handler
	transaction   *transaction.Handler
	cardStatement *cardstatement.Handler
	importer      *importer.Handler
	report        *report.Handler
	investment    *investment.Handler
	dashboard     *dashboard.Handler
	aiPrompt      *aiprompt.Handler
	aiImport      *aiimport.Handler
	ready         http.HandlerFunc
	limiters      routeLimiters
	requireAuth   httpserver.Middleware

	// householdKey transforma o id da casa na chave do limitador. Em produção
	// é o HMAC do serviço de OTP; zerada (teste), o limitador por casa vira
	// global, que é restritivo demais mas nunca vazante.
	householdKey httpserver.HouseholdKeyFunc

	trustedProxyCount int
}

// buildRoutes devolve a tabela completa.
//
// Precisa continuar funcionando com routeDeps{} zerado: o routes_test só
// compara (método, padrão) e não deve precisar de banco, config nem mailer.
func buildRoutes(d routeDeps) []Route {
	ip := httpserver.IPKey(d.trustedProxyCount)
	porCasa := httpserver.HouseholdKey(d.householdKey)
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

		// --- domínio financeiro: contas e categorias (spec 0003) ----------
		//
		// TODAS sob requireAuth. O household vem do token, nunca do caminho —
		// não existe rota com householdId na URL, e é de propósito: o dia em
		// que existir, alguém vai confiar nele (docs/SEGURANCA.md §2).
		{
			Method:      http.MethodGet,
			Pattern:     "/accounts",
			Handler:     d.account.List,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodPost,
			Pattern:     "/accounts",
			Handler:     d.account.Create,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodGet,
			Pattern:     "/accounts/{id}",
			Handler:     d.account.Get,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodPatch,
			Pattern:     "/accounts/{id}",
			Handler:     d.account.Update,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodDelete,
			Pattern:     "/accounts/{id}",
			Handler:     d.account.Delete,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodPost,
			Pattern:     "/accounts/{id}/archive",
			Handler:     d.account.Archive,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodPost,
			Pattern:     "/accounts/{id}/unarchive",
			Handler:     d.account.Unarchive,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},

		{
			Method:      http.MethodGet,
			Pattern:     "/categories",
			Handler:     d.category.List,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodPost,
			Pattern:     "/categories",
			Handler:     d.category.Create,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodGet,
			Pattern:     "/categories/{id}",
			Handler:     d.category.Get,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodPatch,
			Pattern:     "/categories/{id}",
			Handler:     d.category.Update,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodDelete,
			Pattern:     "/categories/{id}",
			Handler:     d.category.Delete,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodPost,
			Pattern:     "/categories/{id}/archive",
			Handler:     d.category.Archive,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodPost,
			Pattern:     "/categories/{id}/unarchive",
			Handler:     d.category.Unarchive,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},

		// --- lançamentos e faturas (spec 0004) ----------------------------
		//
		// Nesta entrega não há POST nem PATCH de lançamento (E2b) e a fatura é
		// somente leitura: no v1 ela nasce e morre com a importação.
		// Superfície que não existe não precisa ser revisada.
		{
			Method:      http.MethodGet,
			Pattern:     "/transactions",
			Handler:     d.transaction.List,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		// Rota literal ANTES de /transactions/{id}: o ServeMux escolhe o padrão
		// mais específico de qualquer jeito, mas a tabela lida na mesma ordem
		// da spec. Escrita em massa (spec 0005 §4.3) — requireAuth primeiro,
		// porque o limitador por casa lê a identidade que ele publica.
		{
			Method:  http.MethodPost,
			Pattern: "/transactions/auto-categorize",
			Handler: d.transaction.AutoCategorize,
			Middlewares: []httpserver.Middleware{
				d.requireAuth,
				httpserver.RateLimit(d.limiters.autoCategorize, porCasa),
			},
		},
		{
			Method:      http.MethodGet,
			Pattern:     "/transactions/{id}",
			Handler:     d.transaction.Get,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		// Emenda §11 da spec 0005: só `categoryId`. Escrita avulsa numa linha —
		// o PATCH completo (valor, data, descrição, conta) continua na E2b.
		//
		// Desde 17/09/2026 tem balde PRÓPRIO por casa: escreve e audita a cada
		// chamada, e o balde global de 100/min por IP não é teto por casa.
		// requireAuth primeiro, porque o limitador por casa lê a identidade que
		// ele publica no contexto.
		{
			Method:  http.MethodPatch,
			Pattern: "/transactions/{id}",
			Handler: d.transaction.UpdateCategory,
			Middlewares: []httpserver.Middleware{
				d.requireAuth,
				httpserver.RateLimit(d.limiters.transactionUpdate, porCasa),
			},
		},
		{
			Method:      http.MethodDelete,
			Pattern:     "/transactions/{id}",
			Handler:     d.transaction.Delete,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodGet,
			Pattern:     "/card-statements",
			Handler:     d.cardStatement.List,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodGet,
			Pattern:     "/card-statements/{id}",
			Handler:     d.cardStatement.Get,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},

		// --- transferências internas (spec 0005 §4.4 e §13) ---------------
		//
		// Leitura: cada PAR de pernas (ADR-016) vira um item. Escrita: só o
		// reprocessamento (ADR-028), que converte pares já gravados — nunca
		// cria perna. POST/PATCH/DELETE /transfers ficam para a E2b.
		{
			Method:      http.MethodGet,
			Pattern:     "/transfers",
			Handler:     d.transaction.ListTransfers,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		// Rota literal ANTES de qualquer /transfers/{id} futura, como
		// /transactions/auto-categorize. Escrita em massa — requireAuth
		// primeiro, porque o limitador por casa lê a identidade que ele
		// publica; balde próprio, separado do auto-categorize.
		{
			Method:  http.MethodPost,
			Pattern: "/transfers/detect",
			Handler: d.transaction.DetectTransfers,
			Middlewares: []httpserver.Middleware{
				d.requireAuth,
				httpserver.RateLimit(d.limiters.transferDetect, porCasa),
			},
		},

		// --- investimentos e resgates (spec 0006, ADR-029) ----------------
		//
		// Leitura: a tela do mês, sob requireAuth e o limitador GLOBAL — a
		// consulta é uma agregação indexada de ≤ 24 linhas mais uma página,
		// limitada pela taxonomia e pelo cursor, não pelo volume do histórico.
		{
			Method:      http.MethodGet,
			Pattern:     "/investments",
			Handler:     d.investment.Overview,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		// Rota literal ANTES de qualquer /investments/{id} futura, como
		// /transactions/auto-categorize e /transfers/detect. Escrita em massa —
		// requireAuth PRIMEIRO, porque o limitador por casa lê a identidade que
		// ele publica no contexto; balde próprio, separado dos outros dois.
		{
			Method:  http.MethodPost,
			Pattern: "/investments/detect",
			Handler: d.investment.Detect,
			Middlewares: []httpserver.Middleware{
				d.requireAuth,
				httpserver.RateLimit(d.limiters.investmentDetect, porCasa),
			},
		},

		// --- relatórios (ADR-027) -----------------------------------------
		//
		// Leitura pura, sob requireAuth e sob o limitador GLOBAL — sem balde
		// próprio, como GET /transactions e GET /transfers: a consulta é uma
		// agregação indexada limitada pela taxonomia, não pelo volume.
		{
			Method:      http.MethodGet,
			Pattern:     "/reports/by-category",
			Handler:     d.report.ByCategory,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},

		// --- painel: o resumo do mês (spec 0008, ADR-031) ------------------
		//
		// UM middleware, e isso é decisão registrada (ADR-031e): requireAuth e
		// o limitador GLOBAL por IP, sem balde próprio e sem teto por casa.
		// Esta é a rota da HOME, a mais chamada do app — um balde por casa
		// transformaria abrir o app e trocar de mês em 429 na primeira tela.
		// Balde próprio existe aqui para escrita em massa (auto-categorize,
		// transfers/detect, investments/detect) e para a rota cara (imports);
		// esta é leitura pura, com custo limitado pelo domínio (≤ 200
		// categorias, ≤ 50 contas, ≤ 100 linhas agregadas) e sem transação
		// segurando conexão do pool — a mesma classe de GET /investments e
		// GET /reports/by-category.
		{
			Method:      http.MethodGet,
			Pattern:     "/dashboard",
			Handler:     d.dashboard.Summary,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},

		// --- menu IA: exportar o prompt (spec 0010, E9a) -------------------
		//
		// Leitura PURA — não escreve lançamento, categoria nem auditoria —, e
		// mesmo assim com balde POR CASA, ao contrário do painel e do
		// relatório: a agregação por `description_norm` varre até 3 meses numa
		// coluna sem índice próprio, e a resposta é um texto grande montado em
		// memória. É a classe da importação, não a da home.
		//
		// A ordem dos middlewares importa: requireAuth PRIMEIRO, porque é ele
		// que publica no contexto a identidade de que o limitador por casa
		// deriva a chave (HMAC do household_id, nunca o id em claro).
		{
			Method:  http.MethodGet,
			Pattern: "/ai/export-prompt",
			Handler: d.aiPrompt.ExportPrompt,
			Middlewares: []httpserver.Middleware{
				d.requireAuth,
				httpserver.RateLimit(d.limiters.aiExport, porCasa),
			},
		},

		// --- menu IA: importar o JSON da IA (spec 0010, E9b) ----------------
		//
		// As duas rotas leem o MESMO corpo (o envelope da §4.1). A prévia
		// não escreve nada e mede o impacto rodando o matcher; o confirm
		// revalida do zero e grava numa transação só. Cada uma tem balde
		// PRÓPRIO por casa, e requireAuth vem PRIMEIRO — é ele que publica a
		// identidade de que o limitador deriva a chave (HMAC do household_id,
		// nunca o id em claro). O teto de corpo (128 KiB) é da cadeia global,
		// por caminho exato (httpserver.MaxBytesByPath em main.go).
		{
			Method:  http.MethodPost,
			Pattern: "/ai/keyword-import/preview",
			Handler: d.aiImport.Preview,
			Middlewares: []httpserver.Middleware{
				d.requireAuth,
				httpserver.RateLimit(d.limiters.aiImportPreview, porCasa),
			},
		},
		{
			Method:  http.MethodPost,
			Pattern: "/ai/keyword-import/confirm",
			Handler: d.aiImport.Confirm,
			Middlewares: []httpserver.Middleware{
				d.requireAuth,
				httpserver.RateLimit(d.limiters.aiImportConfirm, porCasa),
			},
		},

		// --- importação em duas fases (ADR-024) ---------------------------
		//
		// A ordem dos middlewares importa: requireAuth PRIMEIRO, porque o
		// limitador por casa lê a identidade que ele publica no contexto.
		{
			Method:      http.MethodGet,
			Pattern:     "/imports",
			Handler:     d.importer.List,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:  http.MethodPost,
			Pattern: "/imports",
			Handler: d.importer.Create,
			Middlewares: []httpserver.Middleware{
				d.requireAuth,
				httpserver.RateLimit(d.limiters.importUpload, porCasa),
				httpserver.RateLimit(d.limiters.importUploadIP, ip),
			},
		},
		{
			Method:      http.MethodGet,
			Pattern:     "/imports/{id}",
			Handler:     d.importer.Get,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:      http.MethodDelete,
			Pattern:     "/imports/{id}",
			Handler:     d.importer.Delete,
			Middlewares: []httpserver.Middleware{d.requireAuth},
		},
		{
			Method:  http.MethodPost,
			Pattern: "/imports/{id}/confirm",
			Handler: d.importer.Confirm,
			Middlewares: []httpserver.Middleware{
				d.requireAuth,
				httpserver.RateLimit(d.limiters.importConfirm, porCasa),
			},
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
