package config_test

import (
	"fmt"
	"reflect"
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
		"ImportUpload":                  rl.ImportUpload,
		"ImportUploadIP":                rl.ImportUploadIP,
		"ImportConfirm":                 rl.ImportConfirm,
		"AutoCategorize":                rl.AutoCategorize,
		"TransferDetect":                rl.TransferDetect,
		"TransactionUpdate":             rl.TransactionUpdate,
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

// ---------------------------------------------------------------------------
// Auto-categorização (spec 0005, emenda §10.8)
// ---------------------------------------------------------------------------

// O balde de POST /transactions/auto-categorize é PRÓPRIO e por casa: 60/h,
// o dobro do confirm da importação, porque cada uso legítimo gasta duas
// chamadas (prévia com dryRun e confirmação). Travar o número aqui evita que
// alguém o "afrouxe" sem passar por revisão — é limite de segurança, não
// sintonia operacional, e por isso não vem de ambiente.
func TestAutoCategorizeTemBaldeProprioDe60PorHora(t *testing.T) {
	t.Parallel()

	rl := config.DefaultRateLimits()

	// Estouro 3 desde 18/09/2026 (achado A2): a rota varre até 10.000
	// lançamentos e os pontua contra até 4.000 palavras-chave, e sem Burst o
	// estouro seria a cota inteira.
	assert.Equal(t, config.Rule{Requests: 60, Window: time.Hour, Burst: 3}, rl.AutoCategorize)
	assert.Less(t, rl.AutoCategorize.Burst, rl.AutoCategorize.Requests,
		"rota cara não pode gastar a cota inteira de uma vez")

	// Dois usos (prévia + confirmação) por uso do confirm: os dois tetos
	// precisam representar a MESMA quantidade de operações por hora.
	assert.Equal(t, 2*rl.ImportConfirm.Requests, rl.AutoCategorize.Requests,
		"auto-categorize gasta duas chamadas por uso; o teto deve ser o dobro do confirm")
	assert.Equal(t, rl.ImportConfirm.Window, rl.AutoCategorize.Window)
}

// O balde de POST /transfers/detect (spec 0005 §13.1.8, ADR-028f) é PRÓPRIO
// e por casa: 60/h, como o auto-categorize e pelo mesmo motivo — cada uso
// legítimo gasta prévia + confirmação —, mas em balde SEPARADO, para
// consertar o mês de um jeito não trancar o outro. Travado aqui por ser
// limite de segurança, não sintonia operacional.
func TestTransferDetectTemBaldeProprioDe60PorHora(t *testing.T) {
	t.Parallel()

	rl := config.DefaultRateLimits()

	assert.Equal(t, config.Rule{Requests: 60, Window: time.Hour, Burst: 3}, rl.TransferDetect)
	assert.Equal(t, rl.AutoCategorize.Requests, rl.TransferDetect.Requests,
		"as duas ações de reprocessamento em massa têm a mesma cota, em baldes separados")
	assert.Equal(t, rl.AutoCategorize.Window, rl.TransferDetect.Window)
	assert.Equal(t, 2*rl.ImportConfirm.Requests, rl.TransferDetect.Requests,
		"detect gasta duas chamadas por uso; o teto deve ser o dobro do confirm")

	// O ESTOURO é o que impede uma casa de ocupar o pool de conexões inteiro
	// com execuções simultâneas, dentro da cota (achado A2 da revisão de
	// segurança). Travado aqui: é limite de segurança, não sintonia.
	assert.Equal(t, 3, rl.TransferDetect.Burst)
	assert.Less(t, rl.TransferDetect.Burst, rl.TransferDetect.Requests,
		"rota cara não pode gastar a cota inteira de uma vez")
	assert.Equal(t, rl.AutoCategorize.Burst, rl.TransferDetect.Burst,
		"as duas rotas de escrita em massa têm o mesmo estouro")
	assert.Zero(t, rl.ImportConfirm.Burst, "as demais regras mantêm o estouro igual à cota")
}

// ---------------------------------------------------------------------------
// Atalho de categoria — PATCH /transactions/{id} (emenda §11)
// ---------------------------------------------------------------------------

// O balde de PATCH /transactions/{id} é PRÓPRIO e por casa: 120/h (decisão do
// usuário, 17/09/2026). Ele não é escrita em massa — grava UMA linha por
// chamada —, mas escreve e AUDITA a cada chamada, e antes disso só o balde
// global por IP o cobria. O teto é generoso porque categorizar a fatura
// recém-importada é uma rajada legítima, e FINITO porque a rota escreve.
func TestTransactionUpdateTemBaldeProprioDe120PorHora(t *testing.T) {
	t.Parallel()

	rl := config.DefaultRateLimits()

	assert.Equal(t, config.Rule{Requests: 120, Window: time.Hour}, rl.TransactionUpdate)
	assert.Zero(t, rl.TransactionUpdate.Burst,
		"escrita de uma linha não precisa do estouro reduzido da rota cara")
	assert.Greater(t, rl.TransactionUpdate.Requests, rl.AutoCategorize.Requests,
		"o atalho de uma linha é mais barato que a categorização em massa; o teto acompanha")
}

// ---------------------------------------------------------------------------
// Perfil de limites por ambiente (decisão do usuário, 17/09/2026)
// ---------------------------------------------------------------------------

// Sem o interruptor, o ambiente NÃO muda nada.
//
// Este teste substitui o antigo TestRateLimitsIgnoramAmbiente, que travava a
// regra "RateLimits não vem de ambiente, ponto". A regra agora é mais estreita
// e continua valendo no que importa: NÃO existe override por regra. Uma
// variável solta por limite (`RATE_LIMIT_IMPORT_UPLOAD=9999`) é justamente o
// caminho pelo qual um teto frouxo vaza para produção sem aparecer em revisão
// — então nenhuma delas é lida, hoje nem depois.
func TestSemOInterruptorOAmbienteNaoMudaNenhumLimite(t *testing.T) {
	t.Parallel()

	env := baseEnv()
	env["RATE_LIMIT_AUTO_CATEGORIZE"] = "1000"
	env["RATE_LIMIT_TRANSFER_DETECT"] = "1000"
	env["RATE_LIMIT_IMPORT_CONFIRM"] = "1000"
	env["RATE_LIMIT_IMPORT_UPLOAD"] = "9999"
	env["RATE_LIMIT_REGISTER"] = "9999"
	env["RATE_LIMIT_TRANSACTION_UPDATE"] = "9999"
	env["RATE_LIMIT_GLOBAL"] = "9999"
	cfg, err := config.LoadFrom(env)
	require.NoError(t, err)

	assert.Equal(t, config.RateLimitProfileDefault, cfg.RateLimitProfile)
	assert.Equal(t, config.DefaultRateLimits(), cfg.RateLimits)
	assert.Equal(t, 60, cfg.RateLimits.AutoCategorize.Requests)
	assert.Equal(t, 60, cfg.RateLimits.TransferDetect.Requests)
	assert.Equal(t, 10, cfg.RateLimits.ImportUpload.Requests)
	assert.Equal(t, 5, cfg.RateLimits.Register.Requests)
	assert.Equal(t, 120, cfg.RateLimits.TransactionUpdate.Requests)
	assert.False(t, cfg.UsesLooseRateLimits())
}

// O default — nenhuma variável de perfil no ambiente — é EXATAMENTE
// DefaultRateLimits(). A comparação é do conjunto inteiro de propósito: um
// campo novo que alguém esquecer de repetir no perfil de teste não pode
// escorregar para cá.
func TestSemVariavelDePerfilOsLimitesSaoOsPadroes(t *testing.T) {
	t.Parallel()

	for nome, ambiente := range map[string]map[string]string{
		"development": baseEnv(),
		"production":  prodEnv(),
	} {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			cfg, err := config.LoadFrom(ambiente)
			require.NoError(t, err)
			assert.Equal(t, config.RateLimitProfileDefault, cfg.RateLimitProfile)
			assert.Equal(t, config.DefaultRateLimits(), cfg.RateLimits)
			assert.False(t, cfg.UsesLooseRateLimits())
		})
	}

	// RATE_LIMITS_PROFILE=default explícito é o mesmo que não ter nada.
	env := baseEnv()
	env["RATE_LIMITS_PROFILE"] = config.RateLimitProfileDefault
	cfg, err := config.LoadFrom(env)
	require.NoError(t, err)
	assert.Equal(t, config.DefaultRateLimits(), cfg.RateLimits)
}

// O interruptor troca o CONJUNTO inteiro — e SÓ com APP_ENV=test.
func TestPerfilDeTesteTrocaOConjuntoInteiroSomenteComAppEnvTest(t *testing.T) {
	t.Parallel()

	env := baseEnv()
	env["APP_ENV"] = config.EnvTest
	env["RATE_LIMITS_PROFILE"] = config.RateLimitProfileTest

	cfg, err := config.LoadFrom(env)
	require.NoError(t, err)
	assert.True(t, cfg.UsesLooseRateLimits())
	assert.Equal(t, config.ProfileRateLimits(config.RateLimitProfileTest), cfg.RateLimits)
	assert.NotEqual(t, config.DefaultRateLimits(), cfg.RateLimits)

	// Os dois tetos que motivaram a decisão (5 cadastros/h por IP e 10
	// importações/h por casa) precisam caber numa suíte inteira.
	assert.Greater(t, cfg.RateLimits.Register.Requests, 100)
	assert.Greater(t, cfg.RateLimits.ImportUpload.Requests, 100)
}

// SALVAGUARDA 1b (achado B2): o perfil frouxo exige APP_ENV=test. Não basta
// "não ser produção" — development é o rótulo que uma instância exposta de
// verdade costuma carregar, e ele não pode destravar TODOS os limites de
// abuso.
func TestPerfilDeTesteExigeAppEnvTest(t *testing.T) {
	t.Parallel()

	for _, ambiente := range []string{config.EnvDevelopment, config.EnvProduction} {
		t.Run(ambiente, func(t *testing.T) {
			t.Parallel()
			env := baseEnv()
			if ambiente == config.EnvProduction {
				env = prodEnv()
			}
			env["APP_ENV"] = ambiente
			env["RATE_LIMITS_PROFILE"] = config.RateLimitProfileTest

			_, err := config.LoadFrom(env)
			require.Error(t, err, "APP_ENV=%s aceitou o perfil frouxo", ambiente)
			assert.ErrorIs(t, err, config.ErrInvalid)
			assert.Contains(t, err.Error(), "RATE_LIMITS_PROFILE")
			assert.Contains(t, err.Error(), "APP_ENV")
		})
	}
}

// SALVAGUARDA 1: produção RECUSA o perfil de teste no boot, com motivo claro.
func TestPerfilDeTesteEhRecusadoEmProducao(t *testing.T) {
	t.Parallel()

	env := prodEnv()
	env["RATE_LIMITS_PROFILE"] = config.RateLimitProfileTest

	_, err := config.LoadFrom(env)
	require.Error(t, err)
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.Contains(t, err.Error(), "RATE_LIMITS_PROFILE")

	// Caixa e espaços não driblam a trava: o valor é normalizado antes de ser
	// comparado.
	for _, variante := range []string{"TEST", " test ", "Test", "\ttest\n"} {
		t.Run(variante, func(t *testing.T) {
			t.Parallel()
			e := prodEnv()
			e["RATE_LIMITS_PROFILE"] = variante
			_, err := config.LoadFrom(e)
			require.Error(t, err, "variante %q passou pela trava", variante)
		})
	}
}

// SALVAGUARDA 2: perfil desconhecido é FALHA, não "cai no padrão em silêncio".
// Quem digitou "testing" quis afrouxar e precisa descobrir que não afrouxou —
// em qualquer ambiente.
func TestPerfilDesconhecidoFalhaNoBoot(t *testing.T) {
	t.Parallel()

	for _, valor := range []string{"testing", "loose", "prod", "e2e", "default "} {
		t.Run(valor, func(t *testing.T) {
			t.Parallel()
			env := baseEnv()
			env["RATE_LIMITS_PROFILE"] = valor
			_, err := config.LoadFrom(env)
			if strings.TrimSpace(valor) == config.RateLimitProfileDefault {
				require.NoError(t, err, "o espaço à direita é normalizado, não é valor novo")
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, config.ErrInvalid)
			assert.Contains(t, err.Error(), "RATE_LIMITS_PROFILE")
		})
	}
}

// SALVAGUARDA 3: o perfil frouxo mantém a FORMA das regras. Frouxo não é
// "sem limite", e não é lugar de perder um invariante que a revisão de
// segurança pagou caro para estabelecer.
func TestPerfilDeTesteMantemAFormaDasRegras(t *testing.T) {
	t.Parallel()

	rl := config.ProfileRateLimits(config.RateLimitProfileTest)
	padrao := config.DefaultRateLimits()

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
		"ImportUpload":                  rl.ImportUpload,
		"ImportUploadIP":                rl.ImportUploadIP,
		"ImportConfirm":                 rl.ImportConfirm,
		"AutoCategorize":                rl.AutoCategorize,
		"TransferDetect":                rl.TransferDetect,
		"TransactionUpdate":             rl.TransactionUpdate,
	}
	padroes := map[string]config.Rule{
		"Global":                        padrao.Global,
		"Login":                         padrao.Login,
		"Register":                      padrao.Register,
		"ResendCode":                    padrao.ResendCode,
		"ForgotPassword":                padrao.ForgotPassword,
		"VerifyEmail":                   padrao.VerifyEmail,
		"ResetPassword":                 padrao.ResetPassword,
		"Refresh":                       padrao.Refresh,
		"Health":                        padrao.Health,
		"LoginPerAccount":               padrao.LoginPerAccount,
		"ForgotPasswordPerAccount":      padrao.ForgotPasswordPerAccount,
		"RegisterMailPerAccount":        padrao.RegisterMailPerAccount,
		"AccountExistsNoticePerAccount": padrao.AccountExistsNoticePerAccount,
		"ImportUpload":                  padrao.ImportUpload,
		"ImportUploadIP":                padrao.ImportUploadIP,
		"ImportConfirm":                 padrao.ImportConfirm,
		"AutoCategorize":                padrao.AutoCategorize,
		"TransferDetect":                padrao.TransferDetect,
		"TransactionUpdate":             padrao.TransactionUpdate,
	}
	require.Len(t, regras, len(padroes),
		"regra nova sem lugar no perfil de teste — ou ela não foi elevada, ou este teste ficou para trás")

	for nome, r := range regras {
		// Toda regra continua existindo, finita e com janela.
		require.Positive(t, r.Requests, "regra %s sem teto no perfil de teste", nome)
		require.Positive(t, r.Window, "regra %s sem janela no perfil de teste", nome)

		// Mesma janela do padrão: o perfil eleva o TETO, não estica o tempo.
		assert.Equal(t, padroes[nome].Window, r.Window,
			"regra %s: o perfil de teste mudou a janela, não só o teto", nome)
		assert.GreaterOrEqual(t, r.Requests, padroes[nome].Requests,
			"regra %s: o perfil de teste não pode APERTAR um limite", nome)

		// O mesmo invariante do achado C: o balde não é varrido antes de a
		// janela fechar.
		efetivo := httpserver.NewLimiter(r.Requests, r.Window, rl.IdleTTL).IdleTTL()
		assert.GreaterOrEqual(t, efetivo, r.Window,
			"regra %s: o balde é varrido antes de a janela fechar, e renasce cheio", nome)
	}

	// A relação ENTRE regras que sustenta a negação de cadastro por procuração
	// vale no perfil de teste também.
	assert.Greater(t, rl.RegisterMailPerAccount.Requests, rl.Register.Requests,
		"cota de mensagens por endereço <= teto de register por IP, mesmo no perfil de teste")
	assert.LessOrEqual(t, rl.Register.Window, rl.RegisterMailPerAccount.Window)

	// As rotas caras continuam com estouro menor que a cota (achado A2). A
	// conferência completa, perfil a perfil, está em
	// TestRotasDeEscritaEmMassaTemEstouroMenorQueACota.
	assert.Positive(t, rl.TransferDetect.Burst)
	assert.Less(t, rl.TransferDetect.Burst, rl.TransferDetect.Requests,
		"rota cara não pode gastar a cota inteira de uma vez, nem no perfil de teste")
	assert.Positive(t, rl.AutoCategorize.Burst)
	assert.Less(t, rl.AutoCategorize.Burst, rl.AutoCategorize.Requests,
		"rota cara não pode gastar a cota inteira de uma vez, nem no perfil de teste")
}

// classeDeRegra diz o que o teste exige de UMA regra, e por quê. O `porque`
// não é decoração: uma exceção nominal sem motivo escrito é a forma como um
// invariante de segurança morre — alguém acrescenta um nome à lista para o
// teste ficar verde, e ninguém descobre depois que aquilo era o defeito.
type classeDeRegra struct {
	emMassa bool
	porque  string
}

// regrasPorClasse classifica CADA regra de config.RateLimits.
//
// EM MASSA (exige 0 < Burst < Requests): UMA requisição processa o mês inteiro
// — milhares de linhas — e, na execução real, escreve em lote. Para essas, o
// estouro padrão do limitador (igual à cota) é um problema de DISPONIBILIDADE
// e não de cota: 60 execuções simultâneas de uma casa só esgotam o pool de 25
// conexões sem passar de limite nenhum. Foi o achado A2 da revisão de
// segurança, e ele nasceu exatamente do modo de falha que este teste cobre —
// a rota irmã (AutoCategorize) ficou sem Burst porque a correção anterior foi
// aplicada a uma só (TransferDetect).
//
// O mapa é varrido por REFLEXÃO contra a struct: regra nova sem entrada aqui
// FALHA o teste, e quem a criou decide a classe antes de seguir. Entradas para
// regras que ainda não existem são permitidas (pré-classificação de balde que
// outra frente está acrescentando); o que não é permitido é o contrário.
var regrasPorClasse = map[string]classeDeRegra{
	// --- por IP / por conta: autenticação e e-mail --------------------------
	// Nenhuma delas varre nada: são baratas por requisição, e o estouro igual
	// à cota É o comportamento desejado (quem tem 10/min pode gastar os 10 de
	// uma vez e esperar).
	"Global":                        {false, "teto geral por IP; requisição barata"},
	"Login":                         {false, "uma verificação de senha por chamada"},
	"Register":                      {false, "uma linha de usuário por chamada"},
	"ResendCode":                    {false, "um e-mail por chamada"},
	"ForgotPassword":                {false, "um e-mail por chamada"},
	"VerifyEmail":                   {false, "uma comparação de OTP por chamada"},
	"ResetPassword":                 {false, "uma troca de senha por chamada"},
	"Refresh":                       {false, "uma rotação de refresh por chamada"},
	"Health":                        {false, "leitura"},
	"LoginPerAccount":               {false, "mesma chamada do Login, chaveada por conta"},
	"ForgotPasswordPerAccount":      {false, "mesma chamada do ForgotPassword"},
	"RegisterMailPerAccount":        {false, "governa ENVIO de e-mail, não trabalho de CPU"},
	"AccountExistsNoticePerAccount": {false, "um aviso de conteúdo fixo"},

	// --- importação --------------------------------------------------------
	// Caras de verdade, mas SEM Burst nesta entrega: pôr um estouro pequeno
	// nelas é decisão de produto que ninguém tomou, e a revisão da E2c não a
	// pediu. Ficam como `false` com o motivo escrito, e não escondidas: o
	// backlog está no relatório da E2c.
	"ImportUpload":   {false, "cara, mas sem Burst nesta entrega — decisão de produto pendente (backlog E2c)"},
	"ImportUploadIP": {false, "espelho por IP da ImportUpload"},
	"ImportConfirm":  {false, "cara, mas sem Burst nesta entrega — decisão de produto pendente (backlog E2c)"},

	// --- escrita em massa por casa -----------------------------------------
	"AutoCategorize": {true, "varre até 10.000 lançamentos e escreve em lote"},
	"TransferDetect": {true, "varre até 10.000 candidatas e 20.000 espelhos e escreve em lote"},
	// Uma execução de POST /investments/detect percorre TODA receita e despesa
	// viva do mês (teto de 10.000) e, na confirmação, aplica os UPDATE e a
	// auditoria DENTRO de uma transação — cada requisição em voo segura uma
	// conexão do pool até terminar. Por isso o estouro precisa ser bem menor
	// que a cota: estouro IGUAL à cota deixaria UMA casa ocupar o pool inteiro
	// sem passar de limite nenhum, que é exatamente o achado A2. A cota de
	// 60/h é o dobro do confirm da importação (30/h) porque cada uso legítimo
	// gasta DUAS chamadas: a prévia (dryRun: true) e a confirmação.
	//
	// Burst 3 em produção; o perfil de teste sobe para 30, senão a prévia
	// seguida da confirmação daria 429 intermitente no E2E — a mesma razão da
	// AutoCategorize. O invariante 0 < Burst < Requests vale nos dois perfis.
	//
	// O balde é da entrega E7 (Investimentos), escrita em paralelo a esta; a
	// entrada nasceu antes do campo de propósito, para o invariante valer no
	// minuto em que ele aparecesse em vez de depender de alguém lembrar de
	// voltar aqui. O texto acima é da frente que o construiu.
	"InvestmentDetect": {true, "varre o mês inteiro e segura conexão do pool dentro da transação; cota dobrada porque cada uso gasta prévia + confirmação"},

	// --- escrita avulsa ----------------------------------------------------
	// NÃO é escrita em massa: grava UMA linha e audita UMA vez por chamada. E
	// o estouro dela É a cota útil — categorizar a fatura recém-importada é
	// uma rajada legítima de cliques, e um balde de 3 quebraria exatamente
	// esse uso. `false` de propósito, não por esquecimento.
	"TransactionUpdate": {false, "grava UMA linha por chamada; o estouro é a cota útil da rajada de cliques"},
}

// INVARIANTE (achado A2), varrido por REFLEXÃO sobre TODAS as regras de
// config.RateLimits, nos DOIS perfis:
//
//  1. propriedade universal, sem nome nenhum: toda regra tem cota e janela
//     positivas, e um Burst declarado é sempre MENOR que a cota — um estouro
//     maior ou igual à cota não limita nada e só engana quem lê;
//  2. rota de escrita em massa tem Burst POSITIVO.
//
// A reflexão é o que faz o teste durar: o balde novo que alguém acrescentar
// cai aqui antes de chegar em produção sem estouro.
func TestRotasDeEscritaEmMassaTemEstouroMenorQueACota(t *testing.T) {
	t.Parallel()

	perfis := map[string]config.RateLimits{
		"default": config.DefaultRateLimits(),
		"test":    config.ProfileRateLimits(config.RateLimitProfileTest),
	}

	for nomePerfil, rl := range perfis {
		t.Run(nomePerfil, func(t *testing.T) {
			t.Parallel()

			v := reflect.ValueOf(rl)
			tipoRegra := reflect.TypeOf(config.Rule{})
			emMassaVistas := 0

			for i := range v.NumField() {
				campo := v.Type().Field(i)
				if campo.Type != tipoRegra {
					continue // IdleTTL e o que mais vier que não seja Rule
				}
				regra := v.Field(i).Interface().(config.Rule)

				// (1) Propriedade universal — vale para toda regra, sem exceção.
				assert.Positivef(t, regra.Requests, "regra %s sem cota", campo.Name)
				assert.Positivef(t, regra.Window, "regra %s sem janela", campo.Name)
				assert.GreaterOrEqualf(t, regra.Burst, 0, "regra %s com Burst negativo", campo.Name)
				if regra.Burst > 0 {
					assert.Lessf(t, regra.Burst, regra.Requests,
						"regra %s: Burst >= Requests não limita nada — ou tire o Burst, ou baixe-o", campo.Name)
				}

				// (2) Classificação: toda regra precisa de uma, com motivo.
				classe, classificada := regrasPorClasse[campo.Name]
				require.Truef(t, classificada,
					"regra %s não está em regrasPorClasse (config_test.go): diga se ela é escrita em massa e POR QUÊ "+
						"— rota que varre o mês inteiro precisa de Burst (achado A2)", campo.Name)
				require.NotEmptyf(t, classe.porque, "regra %s classificada sem motivo escrito", campo.Name)
				if !classe.emMassa {
					continue
				}
				emMassaVistas++
				assert.Positivef(t, regra.Burst,
					"%s é escrita em massa (%s) e está SEM Burst: o estouro vira a cota inteira (%d de uma vez)",
					campo.Name, classe.porque, regra.Requests)
			}

			require.Positive(t, emMassaVistas, "nenhuma rota de escrita em massa foi encontrada — a reflexão quebrou?")
		})
	}
}
