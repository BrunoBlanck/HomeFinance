// Command api sobe a API HTTP do HomeFinance.
//
// Este arquivo é o ÚNICO ponto de composição do sistema: config -> logger ->
// banco -> AutoMigrate -> repositórios -> serviços -> handlers -> servidor.
// Injeção de dependência é manual, por construtor (ADR-004): sem framework de
// DI, sem variável global, sem init() escondido.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/aiimport"
	"github.com/brunorblanck/homefinance/backend/internal/aiprompt"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/dashboard"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/c6"
	"github.com/brunorblanck/homefinance/backend/internal/importer/inter"
	"github.com/brunorblanck/homefinance/backend/internal/importer/nubank"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/platform/config"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/mailer"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/brunorblanck/homefinance/backend/internal/user"
)

func main() {
	if err := run(); err != nil {
		// Antes do logger existir só há o stderr. A mensagem de erro de
		// configuração nunca contém o valor dos segredos (config.Secret).
		fmt.Fprintf(os.Stderr, "falha ao iniciar: %v\n", err)
		os.Exit(1)
	}
}

// newParserRegistry monta a lista COMPLETA de leiautes de importação.
//
// É a única lista: o serviço real usa esta função, e o teste de fixtures
// (parsers_test.go) usa a MESMA, para que um leiaute novo não possa entrar aqui
// sem passar pela prova de que cada fixture anonimizada casa com exatamente um
// parser. Os cinco leiautes de hoje (extrato e fatura do Nubank, extrato e
// fatura do C6, extrato do Inter) entram por construtor; cada um DECLARA a sua
// convenção de sinal e o cabeçalho reconhecido, e a detecção escolhe pelo
// cabeçalho, nunca pela instituição marcada na conta (spec 0004 §7).
func newParserRegistry() (*importer.Registry, error) {
	return importer.NewRegistry(
		nubank.NewChecking(), nubank.NewCard(),
		c6.NewChecking(), c6.NewCard(),
		inter.NewChecking(),
	)
}

func run() error {
	// Contexto cancelado no primeiro SIGINT/SIGTERM; o segundo sinal deixa o
	// runtime matar o processo, como manda o padrão.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	lg := logging.New(os.Stdout, logging.Options{
		Level:     cfg.Log.Level,
		Format:    cfg.Log.Format,
		AddSource: !cfg.IsProduction(),
	})
	slog.SetDefault(lg)
	lg.InfoContext(ctx, "iniciando homefinance", slog.Any("config", cfg))

	// Um servidor com os tetos de abuso afrouxados nunca sobe em silêncio. A
	// config já recusa este perfil em produção (config.validate); o aviso
	// existe para o caso do "ambiente de homologação que virou produção sem
	// ninguém trocar o APP_ENV" — que é como esse tipo de coisa vaza.
	if cfg.UsesLooseRateLimits() {
		lg.WarnContext(ctx, "perfil de limites de abuso NÃO-PADRÃO ativo — use apenas em teste automatizado ou na máquina de desenvolvimento, NUNCA em produção",
			slog.String("rate_limits_profile", cfg.RateLimitProfile),
			slog.String("app_env", cfg.AppEnv))
	}

	// --- Banco -------------------------------------------------------------
	db, err := storage.Open(ctx, storage.Options{
		Driver:          cfg.DB.Driver,
		DSN:             cfg.DB.DSN.Reveal(),
		MaxOpenConns:    cfg.DB.MaxOpenConns,
		MaxIdleConns:    cfg.DB.MaxIdleConns,
		ConnMaxLifetime: cfg.DB.ConnMaxLifetime,
		Production:      cfg.IsProduction(),
	}, lg)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			lg.Error("falha ao fechar banco", slog.String("reason", err.Error()))
		}
	}()

	if cfg.DB.AutoMigrate {
		if err := storage.Migrate(ctx, db, lg, gormstore.Models()...); err != nil {
			return err
		}
	}

	// --- Repositórios ------------------------------------------------------
	userRepo := gormstore.NewUserRepository(db)
	householdRepo := gormstore.NewHouseholdRepository(db)
	membershipRepo := gormstore.NewMembershipRepository(db)
	refreshRepo := gormstore.NewRefreshTokenRepository(db)
	codeRepo := gormstore.NewVerificationCodeRepository(db)
	attemptRepo := gormstore.NewRegistrationAttemptRepository(db)
	auditRepo := gormstore.NewAuditRepository(db)
	accountRepo := gormstore.NewAccountRepository(db)
	categoryRepo := gormstore.NewCategoryRepository(db)
	txRepo := gormstore.NewTransactionRepository(db)
	stmtRepo := gormstore.NewCardStatementRepository(db)
	importRepo := gormstore.NewImportRepository(db)
	uow := gormstore.NewUnitOfWork(db)

	// --- Mailer (assíncrono) ----------------------------------------------
	sender, err := buildMailer(cfg)
	if err != nil {
		return err
	}
	lg.InfoContext(ctx, "mailer selecionado", slog.String("mailer", sender.Name()))

	mailQueue := mailer.NewQueue(sender, lg, mailer.QueueOptions{
		Size:    cfg.Mail.QueueSize,
		Workers: 2,
	})
	notifier := mailer.NewNotifier(mailQueue, cfg.Mail.From, cfg.Mail.FromName)

	// --- Serviços de domínio ----------------------------------------------
	auditSvc := audit.NewService(auditRepo, lg)

	// A semente de categorias (D5) roda dentro da transação que cria a casa,
	// então o serviço de categoria é construído ANTES do de casa e entra nele
	// como Seeder. Sem FK física (ADR-013), casa e semente precisam nascer
	// atomicamente — S10 do PLANOS.md.
	//
	// O UsageChecker de lançamentos é UM tipo que responde às duas perguntas
	// ("esta conta já foi usada?" e "esta categoria já foi usada?"), porque a
	// resposta vem da mesma tabela. Registrá-lo aqui é o que paga a dívida
	// declarada na spec 0003 §1: a partir desta entrega, DELETE /accounts/{id}
	// e DELETE /categories/{id} com lançamento associado respondem 422
	// RESOURCE_IN_USE em vez de deixar dinheiro pendurado numa conta que sumiu.
	// A saída para o usuário é ARQUIVAR, e é isso que a mensagem do 422 diz.
	usoEmLancamentos := transaction.NewUsageChecker(txRepo)

	categorySvc := category.NewService(categoryRepo, uow,
		category.WithAudit(categoryAuditBridge{svc: auditSvc}),
		category.WithUsageCheckers(usoEmLancamentos),
		// A escrita ADITIVA de palavras-chave do import de IA (spec 0010
		// §10.4, achado A2): o mesmo repositório, por uma interface à parte.
		category.WithKeywordAppender(categoryRepo),
	)
	householdSvc := household.NewService(householdRepo, membershipRepo,
		household.WithSeeders(categorySvc),
	)
	// WithBalances fecha o ADR-017 na prática: a partir daqui balanceCents é o
	// saldo de abertura MAIS a soma com sinal dos lançamentos, e nunca coluna.
	accountSvc := account.NewService(accountRepo, householdSvc, uow,
		account.WithAudit(auditBridge{svc: auditSvc}),
		account.WithBalances(txRepo),
		account.WithUsageCheckers(usoEmLancamentos),
		account.WithKeywordAppender(accountRepo),
	)
	userSvc := user.NewService(userRepo, householdSvc)

	// O classificador (spec 0005, ADR-026) é UM loader compartilhado por
	// lançamentos e importação: os dois precisam da mesma seleção de
	// palavras-chave (só dona ativa, natureza da linha, nunca a conta do lote),
	// e é ele quem lê categoria e conta pelos repositórios — sempre com o
	// household_id do token.
	classifier := classify.NewLoader(categoryRepo, accountRepo)

	// Lançamentos, faturas e importação. A ordem é a das dependências:
	// lançamento conhece conta, categoria e fatura; a fatura DERIVA os números
	// dele (StatementTotals); a importação escreve pelos dois SERVIÇOS, nunca
	// pelos repositórios — é no serviço que moram a conferência de casa dentro
	// da transação, a competência da fatura e o ordinal de deduplicação.
	transactionSvc := transaction.NewService(txRepo, accountRepo, categoryRepo, stmtRepo, uow, classifier,
		transaction.WithAudit(transactionAuditBridge{svc: auditSvc}),
	)
	cardStatementSvc := cardstatement.NewService(stmtRepo, accountRepo, householdSvc,
		transaction.NewStatementTotals(txRepo), uow,
		cardstatement.WithAudit(cardStatementAuditBridge{svc: auditSvc}),
	)

	// O REGISTRO de parsers é montado uma vez (newParserRegistry) e é imutável
	// depois: é ele que decide qual leiaute lê qual arquivo, e essa lista não
	// pode depender da ordem de import dos pacotes.
	parserRegistry, err := newParserRegistry()
	if err != nil {
		return err
	}
	importSvc := importer.NewService(importRepo, parserRegistry, accountRepo, txRepo,
		transactionSvc, cardStatementSvc, uow, classifier,
		importer.WithAudit(importAuditBridge{svc: auditSvc}),
	)

	// Relatórios (ADR-027): leitura pura sobre os repositórios de lançamento
	// (agregação no banco), de categoria (a árvore, dobrada em Go) e de conta
	// (o recorte crédito/débito, ADR-032 — resolvido em Go sobre as contas da
	// casa, sem id de conta no SQL). Sem UnitOfWork e sem auditoria — não há
	// escrita.
	reportSvc := report.NewService(txRepo, categoryRepo, accountRepo, lg)

	// Investimentos (ADR-029): como o relatório, é CONSUMIDOR de lançamento,
	// categoria e conta — mas, diferente dele, ESCREVE: o `detect` marca em
	// lote, então leva o mesmo UnitOfWork e a mesma auditoria das outras
	// escritas financeiras. O classificador é o MESMO loader compartilhado com
	// a importação e o auto-categorize: a sugestão de investimento nasce da
	// mesma seleção de palavras-chave, e não de uma segunda que pudesse
	// divergir.
	investmentSvc := investment.NewService(txRepo, categoryRepo, accountRepo, classifier, uow, lg,
		investment.WithAudit(investmentAuditBridge{svc: auditSvc}),
	)

	// Painel (ADR-031): o terceiro consumidor de leitura agregada, depois do
	// relatório e do investimento. Consome os MESMOS três repositórios — e é
	// por isso que ele não é escrita: o resumo do mês não desfaz nem rastreia
	// nada, então não leva UnitOfWork nem auditoria.
	dashboardSvc := dashboard.NewService(txRepo, categoryRepo, accountRepo, lg)

	// Menu IA — exportação (spec 0010, E9a). LEITURA PURA: o construtor não
	// recebe UnitOfWork nem Auditor, e é isso que torna "o export não escreve"
	// verificável pelo compilador (§10.3 da spec 0010).
	aiPromptSvc := aiprompt.NewService(txRepo, categoryRepo, accountRepo, lg)

	// Menu IA — importação (spec 0010, E9b). É o irmão que ESCREVE, e
	// escreve pelos SERVIÇOS: categoria nasce por categorySvc.Create (o mesmo
	// caminho do POST /categories — teto de 200, pai na casa do token,
	// herança da natureza, auditoria) e palavra entra por AppendKeywords dos
	// dois serviços, a única extração que a §10.4 autorizou — aditiva. Os repositórios
	// entram só como leitura, para o índice em memória; o razão, para medir o
	// impacto. Mesmo UnitOfWork: o confirm é UMA transação, e o Create
	// reentra nela.
	aiImportSvc := aiimport.NewService(categoryRepo, categorySvc, accountRepo, accountSvc,
		txRepo, uow, lg,
		aiimport.WithAudit(aiImportAuditBridge{svc: auditSvc}),
	)

	hasher, err := auth.NewPasswordHasher(auth.Argon2Params{
		MemoryKiB:     cfg.Argon2.MemoryKiB,
		Iterations:    cfg.Argon2.Iterations,
		Parallelism:   cfg.Argon2.Parallelism,
		MaxConcurrent: cfg.Argon2.MaxConcurrent,
	})
	if err != nil {
		return err
	}

	tokenSvc, err := auth.NewTokenService(auth.TokenOptions{
		Secret:     cfg.JWT.Secret.Reveal(),
		Issuer:     cfg.JWT.Issuer,
		Audience:   cfg.JWT.Audience,
		AccessTTL:  cfg.JWT.AccessTTL,
		RefreshTTL: cfg.JWT.RefreshTTL,
	})
	if err != nil {
		return err
	}

	otpSvc, err := auth.NewOTP(cfg.OTP.Pepper.Reveal())
	if err != nil {
		return err
	}

	cookieJar := auth.NewCookieJar(auth.CookieOptions{
		Secure:     cfg.Cookie.Secure,
		Domain:     cfg.Cookie.Domain,
		AccessTTL:  cfg.JWT.AccessTTL,
		RefreshTTL: cfg.JWT.RefreshTTL,
	})

	authSvc, err := auth.NewService(auth.Deps{
		Users:      userRepo,
		Households: householdSvc,
		Tokens:     refreshRepo,
		Codes:      codeRepo,
		Attempts:   attemptRepo,
		Audit:      auditSvc,
		UoW:        uow,
		Hasher:     hasher,
		Policy:     auth.NewPasswordPolicy(),
		TokenSvc:   tokenSvc,
		OTP:        otpSvc,
		Mailer:     notifier,
		Cookies:    cookieJar,
		View:       userSvc,
		Logger:     lg,
		MailLimits: auth.MailLimiters{
			// Tetos por ENDEREÇO que governam o envio, não a requisição
			// (ver auth.MailLimiters e auth.AccountLimiters).
			VerificationMail:    newAccountLimiter(cfg.RateLimits.RegisterMailPerAccount, cfg.RateLimits.IdleTTL),
			AccountExistsNotice: newAccountLimiter(cfg.RateLimits.AccountExistsNoticePerAccount, cfg.RateLimits.IdleTTL),
		},
	}, auth.ServiceOptions{
		OTPTTL:            cfg.OTP.TTL,
		OTPMaxAttempts:    cfg.OTP.MaxAttempts,
		OTPResendInterval: cfg.OTP.ResendInterval,
		RefreshRetention:  cfg.JWT.RefreshTTL * 2,
	})
	if err != nil {
		return err
	}

	// --- Limitadores -------------------------------------------------------
	rl := cfg.RateLimits
	newLimiter := func(r config.Rule) *httpserver.Limiter {
		l := httpserver.NewLimiter(r.Requests, r.Window, rl.IdleTTL)
		if r.Burst > 0 {
			// Estouro menor que a cota: só a rota cara pede isso (A2).
			l = l.WithBurst(r.Burst)
		}
		return l
	}
	perRoute := routeLimiters{
		login:          newLimiter(rl.Login),
		register:       newLimiter(rl.Register),
		resendCode:     newLimiter(rl.ResendCode),
		forgotPassword: newLimiter(rl.ForgotPassword),
		verifyEmail:    newLimiter(rl.VerifyEmail),
		resetPassword:  newLimiter(rl.ResetPassword),
		refresh:        newLimiter(rl.Refresh),
		health:         newLimiter(rl.Health),

		importUpload:   newLimiter(rl.ImportUpload),
		importConfirm:  newLimiter(rl.ImportConfirm),
		importUploadIP: newLimiter(rl.ImportUploadIP),

		// Por casa, com a mesma chave HMAC do confirm (spec 0005 §10.8).
		autoCategorize: newLimiter(rl.AutoCategorize),
		// Por casa, balde separado do auto-categorize (spec 0005 §13.1.8).
		transferDetect: newLimiter(rl.TransferDetect),
		// Por casa, balde separado dos dois acima (spec 0006 §3.3.5).
		investmentDetect: newLimiter(rl.InvestmentDetect),
		// Por casa, balde próprio do menu IA (spec 0010 §8.6).
		aiExport: newLimiter(rl.AiExport),
		// Por casa, dois baldes separados para o import de IA: a prévia roda o
		// matcher, o confirm segura conexão do pool numa transação (§8.6).
		aiImportPreview: newLimiter(rl.AiImportPreview),
		aiImportConfirm: newLimiter(rl.AiImportConfirm),
		// Por casa, balde próprio do atalho de categoria (emenda §11).
		transactionUpdate: newLimiter(rl.TransactionUpdate),
	}
	globalLimiter := newLimiter(rl.Global)

	// --- Handlers ----------------------------------------------------------
	authHandler := auth.NewHandler(authSvc, lg, auth.HandlerOptions{
		MinResponseTime:   cfg.Auth.MinResponseTime,
		TrustedProxyCount: cfg.HTTP.TrustedProxyCount,
		Limiters: auth.AccountLimiters{
			Login:          newLimiter(rl.LoginPerAccount),
			ForgotPassword: newLimiter(rl.ForgotPasswordPerAccount),
		},
	})
	userHandler := user.NewHandler(userSvc, cookieJar, lg)
	accountHandler := account.NewHandler(accountSvc, lg, cfg.HTTP.TrustedProxyCount)
	categoryHandler := category.NewHandler(categorySvc, lg, cfg.HTTP.TrustedProxyCount)
	transactionHandler := transaction.NewHandler(transactionSvc, lg, cfg.HTTP.TrustedProxyCount)
	cardStatementHandler := cardstatement.NewHandler(cardStatementSvc,
		transaction.NewStatementLines(transactionSvc), lg, cfg.HTTP.TrustedProxyCount)
	importHandler := importer.NewHandler(importSvc, lg, cfg.HTTP.TrustedProxyCount)
	reportHandler := report.NewHandler(reportSvc, lg)
	investmentHandler := investment.NewHandler(investmentSvc, lg, cfg.HTTP.TrustedProxyCount)
	// Sem trustedProxyCount: o painel não audita e não tem limitador próprio,
	// então não lê o IP do cliente (ADR-031e) — igual ao do relatório.
	dashboardHandler := dashboard.NewHandler(dashboardSvc, lg)
	// Sem trustedProxyCount pelo mesmo motivo do painel: esta rota não audita,
	// então não lê o IP do cliente.
	aiPromptHandler := aiprompt.NewHandler(aiPromptSvc, lg)
	// COM trustedProxyCount: o confirm audita, e a auditoria leva o IP.
	aiImportHandler := aiimport.NewHandler(aiImportSvc, lg, cfg.HTTP.TrustedProxyCount)

	// --- Rotas e cadeia de middlewares ------------------------------------
	routes := buildRoutes(routeDeps{
		auth:          authHandler,
		user:          userHandler,
		account:       accountHandler,
		category:      categoryHandler,
		transaction:   transactionHandler,
		cardStatement: cardStatementHandler,
		importer:      importHandler,
		report:        reportHandler,
		investment:    investmentHandler,
		dashboard:     dashboardHandler,
		aiPrompt:      aiPromptHandler,
		aiImport:      aiImportHandler,
		ready:         httpserver.Ready(db, lg),
		limiters:      perRoute,
		requireAuth:   httpserver.RequireAuth(authHandler),
		// A chave do limitador por casa é o HMAC do household_id com o pepper
		// da aplicação: o id em claro nunca entra no mapa do limitador, igual
		// ao e-mail hoje (§7 da spec 0001).
		householdKey: func(householdID string) string {
			return otpSvc.AccountKey("ratelimit-household", householdID)
		},
		trustedProxyCount: cfg.HTTP.TrustedProxyCount,
	})

	allowlist := httpserver.NewOriginAllowlist(cfg.HTTP.CORSOrigins)

	// Ordem: do mais externo para o mais interno. Recover primeiro para
	// cobrir tudo; rate limit antes do handler para o trabalho caro só
	// acontecer depois de passar pelo funil.
	handler := httpserver.Chain(
		httpserver.ErrorShim(newMux(routes)),
		httpserver.Recover(lg),
		httpserver.RequestID(),
		httpserver.AccessLog(lg, cfg.HTTP.TrustedProxyCount),
		httpserver.SecurityHeaders(cfg.Cookie.Secure),
		httpserver.CORS(allowlist),
		httpserver.CSRFGuard(allowlist),
		// O teto de corpo continua 1 MiB para TODAS as rotas, com exceções
		// declaradas por caminho EXATO — sem prefixo e sem curinga. Subir o
		// teto global abriria 8 MiB nas rotas de autenticação, e embrulhar de
		// novo dentro da rota não funcionaria: quem corta é o MaxBytesReader
		// mais interno (spec 0004 §6.4).
		//
		// POST /imports SOBE o teto (recebe arquivo). As duas rotas do import
		// de IA o BAIXAM, e a direção é o ponto: o corpo delas é JSON colado
		// de uma IA — entrada hostil que o servidor vai parsear, validar campo
		// a campo e casar contra o banco. 1 MiB disso é trabalho de graça para
		// quem manda, e nenhum payload legítimo do formato chega perto de 128
		// KiB (spec 0010 §4.2, regra 1).
		httpserver.MaxBytesByPath(config.MaxRequestBodyBytes, map[string]int64{
			APIBasePath + "/imports":                   importer.MaxUploadBytes,
			APIBasePath + "/ai/keyword-import/preview": aiimport.MaxPayloadBytes,
			APIBasePath + "/ai/keyword-import/confirm": aiimport.MaxPayloadBytes,
		}),
		httpserver.RateLimit(globalLimiter, httpserver.IPKey(cfg.HTTP.TrustedProxyCount)),
		// ÚLTIMO da lista = MAIS INTERNO, envolvendo o ErrorShim(newMux(...)).
		// A posição é parte da decisão, não acaso:
		//  (1) a resposta sai por DENTRO de SecurityHeaders/CORS, que são
		//      externos, então o navegador consegue ler o 400 em vez de tomar
		//      um erro de CORS opaco;
		//  (2) fica DEPOIS do CSRFGuard — origem estranha continua barrada
		//      antes de qualquer validação de entrada;
		//  (3) fica DEPOIS do RateLimit — enxurrada de query malformada
		//      continua gastando balde; checagem barata não vira rota grátis.
		httpserver.WellFormedQuery(),
	)

	// --- Janitor -----------------------------------------------------------
	jan := newJanitor(authSvc, auditSvc, importSvc, lg, janitorOptions{})
	jan.Start(ctx)

	// --- Servidor ----------------------------------------------------------
	srv := httpserver.NewServer(httpserver.ServerOptions{
		Addr:              cfg.HTTP.Addr,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
		ReadTimeout:       cfg.HTTP.ReadTimeout,
		WriteTimeout:      cfg.HTTP.WriteTimeout,
		IdleTimeout:       cfg.HTTP.IdleTimeout,
		ShutdownTimeout:   cfg.HTTP.ShutdownTimeout,
	}, handler, lg)

	runErr := srv.Run(ctx)

	// Desligamento: o janitor para, e a fila de e-mail DRENA — mensagens já
	// aceitas precisam sair (D7).
	jan.Stop()

	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.HTTP.ShutdownTimeout)
	defer cancel()
	if err := mailQueue.Close(drainCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		lg.Error("falha ao drenar fila de e-mail", slog.String("reason", err.Error()))
	}

	enqueued, sent, failed, dropped := mailQueue.Stats()
	lg.Info("fila de e-mail encerrada",
		slog.Int64("enfileirados", enqueued),
		slog.Int64("enviados", sent),
		slog.Int64("falhas", failed),
		slog.Int64("descartados", dropped),
	)

	if runErr != nil {
		return runErr
	}
	lg.Info("encerrado com sucesso")
	return nil
}

// newAccountLimiter monta um limitador por conta para as cotas de ENVIO do
// serviço de auth. Fica aqui, e não junto dos limitadores de rota, porque é
// construído antes deles (o serviço nasce primeiro).
func newAccountLimiter(r config.Rule, idleTTL time.Duration) *httpserver.Limiter {
	return httpserver.NewLimiter(r.Requests, r.Window, idleTTL)
}

// buildMailer escolhe a implementação por MAILER.
//
// D8: a config já falha no boot se APP_ENV=production com MAILER=console.
// A guarda repetida aqui é proposital — defesa em profundidade contra uma
// futura mudança que afrouxe a validação.
func buildMailer(cfg config.Config) (mailer.Mailer, error) {
	switch cfg.Mail.Mailer {
	case config.MailerSMTP:
		return mailer.NewSMTP(mailer.SMTPOptions{
			Host:     cfg.SMTP.Host,
			Port:     cfg.SMTP.Port,
			Username: cfg.SMTP.Username,
			Password: cfg.SMTP.Password.Reveal(),
			TLS:      cfg.SMTP.TLS,
			Timeout:  15 * time.Second,
		})
	case config.MailerConsole:
		if cfg.IsProduction() {
			return nil, errors.New("mailer de console não pode ser usado em produção")
		}
		// Escreve no stdout, NÃO no slog: o corpo contém o código de 6
		// dígitos e o log estruturado precisa continuar limpo.
		return mailer.NewConsole(os.Stdout), nil
	default:
		return nil, fmt.Errorf("MAILER desconhecido: %q", cfg.Mail.Mailer)
	}
}

// garante em tempo de compilação que o handler de auth serve de autenticador
// para o middleware, sem o pacote httpserver importar auth.
var _ httpserver.Authenticator = (*auth.Handler)(nil)
