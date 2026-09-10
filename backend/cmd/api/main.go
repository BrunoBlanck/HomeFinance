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

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/platform/config"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/mailer"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
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
	householdSvc := household.NewService(householdRepo, membershipRepo)
	userSvc := user.NewService(userRepo, householdSvc)

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
		return httpserver.NewLimiter(r.Requests, r.Window, rl.IdleTTL)
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

	// --- Rotas e cadeia de middlewares ------------------------------------
	routes := buildRoutes(routeDeps{
		auth:              authHandler,
		user:              userHandler,
		ready:             httpserver.Ready(db, lg),
		limiters:          perRoute,
		requireAuth:       httpserver.RequireAuth(authHandler),
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
		httpserver.MaxBytes(config.MaxRequestBodyBytes),
		httpserver.RateLimit(globalLimiter, httpserver.IPKey(cfg.HTTP.TrustedProxyCount)),
	)

	// --- Janitor -----------------------------------------------------------
	jan := newJanitor(authSvc, auditSvc, lg, janitorOptions{})
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
