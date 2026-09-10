package config_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/platform/config"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Placeholders longos o bastante para passar no piso de 32 bytes. Não são
// segredos reais — são valores de teste (docs/SEGURANCA.md §7).
const (
	testJWTSecret = "test-jwt-secret-0123456789abcdefghij"
	testOTPPepper = "test-otp-pepper-0123456789abcdefghij"
)

func baseEnv() map[string]string {
	return map[string]string{
		"APP_ENV":    config.EnvDevelopment,
		"JWT_SECRET": testJWTSecret,
		"OTP_PEPPER": testOTPPepper,
	}
}

func prodEnv() map[string]string {
	return map[string]string{
		"APP_ENV":       config.EnvProduction,
		"JWT_SECRET":    testJWTSecret,
		"OTP_PEPPER":    testOTPPepper,
		"MAILER":        config.MailerSMTP,
		"SMTP_HOST":     "smtp.exemplo.com",
		"SMTP_PORT":     "587",
		"SMTP_TLS":      "true",
		"MAIL_FROM":     "nao-responda@exemplo.com",
		"COOKIE_SECURE": "true",
		"CORS_ORIGIN":   "https://app.exemplo.com",
		"DB_DRIVER":     config.DriverPostgres,
		"DB_DSN":        "postgres://u:p@localhost:5432/hf",
	}
}

func TestLoadPadroesDeDesenvolvimento(t *testing.T) {
	t.Parallel()

	cfg, err := config.LoadFrom(baseEnv())
	require.NoError(t, err)

	assert.Equal(t, config.EnvDevelopment, cfg.AppEnv)
	assert.Equal(t, ":8080", cfg.HTTP.Addr)
	assert.Equal(t, config.DriverSQLite, cfg.DB.Driver)
	assert.Equal(t, config.MailerConsole, cfg.Mail.Mailer)
	assert.Equal(t, 15*time.Minute, cfg.JWT.AccessTTL)
	assert.Equal(t, 14*24*time.Hour, cfg.JWT.RefreshTTL)
	assert.Equal(t, 15*time.Minute, cfg.OTP.TTL)
	assert.Equal(t, 5, cfg.OTP.MaxAttempts)
	assert.Equal(t, 300*time.Millisecond, cfg.Auth.MinResponseTime)
	assert.Equal(t, uint32(65536), cfg.Argon2.MemoryKiB)
	assert.Equal(t, config.DefaultRateLimits(), cfg.RateLimits)
	assert.False(t, cfg.IsProduction())
}

func TestLoadProducaoValida(t *testing.T) {
	t.Parallel()

	cfg, err := config.LoadFrom(prodEnv())
	require.NoError(t, err)
	assert.True(t, cfg.IsProduction())
	assert.Equal(t, []string{"https://app.exemplo.com"}, cfg.HTTP.CORSOrigins)
}

// Critério de aceite 23 da spec 0001.
func TestLoadFalhaNoBoot(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome    string
		muda    func(map[string]string)
		base    func() map[string]string
		esperar string
	}{
		{
			nome:    "producao com mailer de console",
			base:    prodEnv,
			muda:    func(e map[string]string) { e["MAILER"] = config.MailerConsole },
			esperar: "MAILER precisa ser smtp em produção",
		},
		{
			nome:    "producao sem cookie seguro",
			base:    prodEnv,
			muda:    func(e map[string]string) { e["COOKIE_SECURE"] = "false" },
			esperar: "COOKIE_SECURE precisa ser true",
		},
		{
			nome:    "producao com CORS vazio",
			base:    prodEnv,
			muda:    func(e map[string]string) { e["CORS_ORIGIN"] = "" },
			esperar: "CORS_ORIGIN é obrigatório em produção",
		},
		{
			nome:    "producao com CORS curinga",
			base:    prodEnv,
			muda:    func(e map[string]string) { e["CORS_ORIGIN"] = "*" },
			esperar: "não pode conter curinga",
		},
		{
			nome:    "producao com sqlite",
			base:    prodEnv,
			muda:    func(e map[string]string) { e["DB_DRIVER"] = config.DriverSQLite },
			esperar: "sqlite não é aceito em produção",
		},
		{
			nome:    "producao sem TLS no SMTP",
			base:    prodEnv,
			muda:    func(e map[string]string) { e["SMTP_TLS"] = "false" },
			esperar: "SMTP_TLS não pode ser desligado",
		},
		{
			nome:    "producao sem host SMTP",
			base:    prodEnv,
			muda:    func(e map[string]string) { delete(e, "SMTP_HOST") },
			esperar: "SMTP_HOST",
		},
		{
			nome:    "jwt secret curto",
			base:    baseEnv,
			muda:    func(e map[string]string) { e["JWT_SECRET"] = "curto" },
			esperar: "JWT_SECRET precisa de pelo menos 32 bytes",
		},
		{
			nome:    "otp pepper curto",
			base:    baseEnv,
			muda:    func(e map[string]string) { e["OTP_PEPPER"] = "curto" },
			esperar: "OTP_PEPPER precisa de pelo menos 32 bytes",
		},
		{
			nome:    "segredos ausentes",
			base:    func() map[string]string { return map[string]string{} },
			muda:    func(map[string]string) {},
			esperar: "JWT_SECRET",
		},
		{
			nome:    "jwt secret igual ao pepper",
			base:    baseEnv,
			muda:    func(e map[string]string) { e["OTP_PEPPER"] = testJWTSecret },
			esperar: "segredos diferentes",
		},
		{
			nome:    "driver desconhecido",
			base:    baseEnv,
			muda:    func(e map[string]string) { e["DB_DRIVER"] = "oracle" },
			esperar: "DB_DRIVER deve ser",
		},
		{
			nome:    "access ttl longo demais",
			base:    baseEnv,
			muda:    func(e map[string]string) { e["ACCESS_TOKEN_TTL"] = "2h" },
			esperar: "ACCESS_TOKEN_TTL",
		},
		{
			nome:    "otp ttl longo demais",
			base:    baseEnv,
			muda:    func(e map[string]string) { e["OTP_TTL"] = "2h" },
			esperar: "OTP_TTL",
		},
		{
			nome:    "otp com tentativas demais",
			base:    baseEnv,
			muda:    func(e map[string]string) { e["OTP_MAX_ATTEMPTS"] = "50" },
			esperar: "OTP_MAX_ATTEMPTS",
		},
		{
			nome:    "argon2 fraco",
			base:    baseEnv,
			muda:    func(e map[string]string) { e["ARGON2_MEMORY_KIB"] = "1024" },
			esperar: "ARGON2_MEMORY_KIB",
		},
		{
			nome:    "cookie domain com __Host-",
			base:    baseEnv,
			muda:    func(e map[string]string) { e["COOKIE_SECURE"] = "true"; e["COOKIE_DOMAIN"] = "exemplo.com" },
			esperar: "COOKIE_DOMAIN precisa ficar vazio",
		},
		{
			nome:    "origem CORS relativa",
			base:    baseEnv,
			muda:    func(e map[string]string) { e["CORS_ORIGIN"] = "app.exemplo.com" },
			esperar: "origens absolutas",
		},
		{
			nome:    "proxy count negativo",
			base:    baseEnv,
			muda:    func(e map[string]string) { e["TRUSTED_PROXY_COUNT"] = "-1" },
			esperar: "TRUSTED_PROXY_COUNT",
		},
		{
			nome:    "app env desconhecido",
			base:    baseEnv,
			muda:    func(e map[string]string) { e["APP_ENV"] = "staging" },
			esperar: "APP_ENV deve ser",
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			e := c.base()
			c.muda(e)
			_, err := config.LoadFrom(e)
			require.Error(t, err)
			assert.ErrorIs(t, err, config.ErrInvalid)
			assert.Contains(t, err.Error(), c.esperar)
		})
	}
}

func TestSecretNuncaVazaEmFormatacao(t *testing.T) {
	t.Parallel()

	s := config.Secret("valor-super-secreto")
	assert.Equal(t, "[REDACTED]", s.String())
	assert.NotContains(t, fmt.Sprintf("%v %s %#v %q", s, s, s, s), "valor-super-secreto")
	assert.Equal(t, "valor-super-secreto", s.Reveal())

	j, err := s.MarshalJSON()
	require.NoError(t, err)
	assert.NotContains(t, string(j), "valor-super-secreto")
}

func TestLogValueDaConfigNaoTemSegredo(t *testing.T) {
	t.Parallel()

	cfg, err := config.LoadFrom(prodEnv())
	require.NoError(t, err)

	rendered := fmt.Sprintf("%+v", cfg.LogValue())
	for _, sensivel := range []string{testJWTSecret, testOTPPepper, "postgres://u:p@localhost:5432/hf"} {
		assert.False(t, strings.Contains(rendered, sensivel), "LogValue vazou %q", sensivel)
	}
}

func TestOrigensSaoNormalizadas(t *testing.T) {
	t.Parallel()

	e := baseEnv()
	e["CORS_ORIGIN"] = " https://A.Exemplo.com/ , ,https://b.exemplo.com "
	cfg, err := config.LoadFrom(e)
	require.NoError(t, err)
	assert.Equal(t, []string{"https://a.exemplo.com", "https://b.exemplo.com"}, cfg.HTTP.CORSOrigins)
}

// ---------------------------------------------------------------------------
// Invariante do achado C — TTL do limitador nunca menor que a janela da regra
// ---------------------------------------------------------------------------

// Um teto de taxa só vale se o estado que o sustenta viver pelo menos o tempo
// que ele leva para se recompor. Quando IdleTTL ficava abaixo da janela, a
// varredura do limitador apagava o balde e ele renascia cheio — a cota de
// "1 por 24 h" virava "1 por hora".
//
// httpserver.NewLimiter já eleva o TTL efetivo para max(idleTTL, window), o
// que protege qualquer regra. Este teste é a segunda barreira: ele afirma o
// invariante sobre os PADRÕES, para que ninguém introduza uma regra nova de
// janela longa acreditando que o IdleTTL configurado dá conta.
func TestNenhumaRegraDePadraoTemJanelaMaiorQueOIdleTTL(t *testing.T) {
	t.Parallel()

	rl := config.DefaultRateLimits()
	regras := map[string]config.Rule{
		"Global":                        rl.Global,
		"Login":                         rl.Login,
		"Register":                      rl.Register,
		"ResendCode":                    rl.ResendCode,
		"ForgotPassword":                rl.ForgotPassword,
		"VerifyEmail":                   rl.VerifyEmail,
		"ResetPassword":                 rl.ResetPassword,
		"Refresh":                       rl.Refresh,
		"Health":                        rl.Health,
		"LoginPerAccount":               rl.LoginPerAccount,
		"ForgotPasswordPerAccount":      rl.ForgotPasswordPerAccount,
		"RegisterMailPerAccount":        rl.RegisterMailPerAccount,
		"AccountExistsNoticePerAccount": rl.AccountExistsNoticePerAccount,
	}

	for nome, r := range regras {
		require.Positive(t, r.Requests, "regra %s sem teto", nome)
		require.Positive(t, r.Window, "regra %s sem janela", nome)

		efetivo := httpserver.NewLimiter(r.Requests, r.Window, rl.IdleTTL).IdleTTL()
		assert.GreaterOrEqual(t, efetivo, r.Window,
			"regra %s: o balde é varrido antes de a janela fechar, e renasce cheio", nome)
	}
}

// ---------------------------------------------------------------------------
// Invariante da negação de cadastro por procuração
// ---------------------------------------------------------------------------

// A cota de MENSAGENS por endereço tem de ficar ACIMA do teto de cadastros
// por IP — sempre.
//
// A cota por endereço governa o envio e não pode recusar a requisição (o
// e-mail vem do corpo, sem prova de posse), então um terceiro consegue
// queimá-la só registrando. Enquanto ela valia 3/h contra um teto de 5
// cadastros/h por IP, um ÚNICO IP zerava o balde do endereço da vítima em
// ~3,7 cadastros e o dono não recebia mais nenhum código: negação de cadastro
// por procuração, de graça.
//
// Com a cota maior que o teto por IP, sobra folga para o dono e o ataque
// passa a exigir pool de IPs. É uma relação ENTRE duas regras, que nenhum
// ajuste isolado enxerga — o mesmo tipo de armadilha do IdleTTL acima.
func TestCotaPorEnderecoFicaAcimaDoTetoPorIP(t *testing.T) {
	t.Parallel()

	rl := config.DefaultRateLimits()

	// PISO, não garantia. Este teste afirmava, até 09/09/2026, que a folga
	// de 5 slots protegia o dono contra um único IP. Era FALSO: a reemissão
	// do /auth/login não verificado gasta o mesmo balde e é limitada por
	// LoginPerAccount (5/15min = 20/h, POR CONTA), então um IP sozinho ainda
	// drena os 10 slots. Refutado com PoC. Um teste verde certificando
	// proteção ausente é pior que teste nenhum, então a asserção de "folga
	// >= 5" foi REMOVIDA em vez de afrouxada.
	//
	// O que continua valendo e vale travar: a cota de mensagens por endereço
	// não pode cair a <= o teto de register por IP, senão o ataque volta na
	// sua forma mais barata (só register, sem precisar nem de conta).
	assert.Greater(t, rl.RegisterMailPerAccount.Requests, rl.Register.Requests,
		"cota de mensagens por endereço <= teto de register por IP: a negação de cadastro por procuração volta na forma mais barata")
	assert.LessOrEqual(t, rl.Register.Window, rl.RegisterMailPerAccount.Window,
		"comparar os tetos só faz sentido se a janela por IP não for mais longa que a do endereço")
}
