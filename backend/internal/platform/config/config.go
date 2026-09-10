// Package config carrega toda a configuração a partir de variáveis de
// ambiente, com fail-fast no boot.
//
// Este é o ÚNICO pacote do projeto que lê variáveis de ambiente
// (docs/ARQUITETURA.md). Segredo nenhum tem valor padrão: se faltar, a
// aplicação não sobe (docs/SEGURANCA.md §7).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Ambientes reconhecidos.
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvProduction  = "production"
)

// Mailers reconhecidos.
const (
	MailerConsole = "console"
	MailerSMTP    = "smtp"
)

// Drivers de banco reconhecidos (ADR-008).
const (
	DriverSQLite    = "sqlite"
	DriverPostgres  = "postgres"
	DriverMySQL     = "mysql"
	DriverSQLServer = "sqlserver"
)

// MaxRequestBodyBytes limita o corpo de qualquer requisição (docs/SEGURANCA.md
// §3). Não é configurável de propósito: afrouxar isso por env seria um botão
// de negação de serviço.
const MaxRequestBodyBytes int64 = 1 << 20 // 1 MiB

// ErrInvalid é a raiz de todo erro de configuração.
var ErrInvalid = errors.New("configuração inválida")

// Secret é uma string que nunca se revela por acidente: implementa Stringer,
// GoStringer e slog.LogValuer devolvendo um marcador. Para usar o valor de
// verdade é preciso chamar Reveal() explicitamente.
type Secret string

// String implementa fmt.Stringer.
func (s Secret) String() string { return "[REDACTED]" }

// GoString implementa fmt.GoStringer (formato %#v).
func (s Secret) GoString() string { return "\"[REDACTED]\"" }

// LogValue implementa slog.LogValuer.
func (s Secret) LogValue() slog.Value { return slog.StringValue("[REDACTED]") }

// MarshalJSON garante que o segredo não vaze em serialização.
func (s Secret) MarshalJSON() ([]byte, error) { return []byte("\"[REDACTED]\""), nil }

// Reveal devolve o valor em claro. Só deve ser chamado no ponto de uso.
func (s Secret) Reveal() string { return string(s) }

// Len devolve o tamanho em bytes do segredo, para validação sem exposição.
func (s Secret) Len() int { return len(s) }

// HTTPConfig agrupa o servidor HTTP.
type HTTPConfig struct {
	Addr              string        `env:"HTTP_ADDR" envDefault:":8080"`
	ReadHeaderTimeout time.Duration `env:"HTTP_READ_HEADER_TIMEOUT" envDefault:"5s"`
	ReadTimeout       time.Duration `env:"HTTP_READ_TIMEOUT" envDefault:"15s"`
	WriteTimeout      time.Duration `env:"HTTP_WRITE_TIMEOUT" envDefault:"30s"`
	IdleTimeout       time.Duration `env:"HTTP_IDLE_TIMEOUT" envDefault:"60s"`
	ShutdownTimeout   time.Duration `env:"HTTP_SHUTDOWN_TIMEOUT" envDefault:"15s"`
	CORSOrigins       []string      `env:"CORS_ORIGIN" envSeparator:","`
	TrustedProxyCount int           `env:"TRUSTED_PROXY_COUNT" envDefault:"0"`
}

// LogConfig agrupa o logger estruturado.
type LogConfig struct {
	Level  string `env:"LOG_LEVEL" envDefault:"info"`
	Format string `env:"LOG_FORMAT" envDefault:"json"`
}

// DBConfig agrupa a conexão com o banco (ADR-008).
type DBConfig struct {
	Driver          string        `env:"DB_DRIVER" envDefault:"sqlite"`
	DSN             Secret        `env:"DB_DSN" envDefault:"homefinance.db"`
	MaxOpenConns    int           `env:"DB_MAX_OPEN_CONNS" envDefault:"25"`
	MaxIdleConns    int           `env:"DB_MAX_IDLE_CONNS" envDefault:"5"`
	ConnMaxLifetime time.Duration `env:"DB_CONN_MAX_LIFETIME" envDefault:"30m"`
	AutoMigrate     bool          `env:"DB_AUTOMIGRATE" envDefault:"true"`
}

// JWTConfig agrupa o access token.
type JWTConfig struct {
	Secret     Secret        `env:"JWT_SECRET"`
	Issuer     string        `env:"JWT_ISSUER" envDefault:"homefinance"`
	Audience   string        `env:"JWT_AUDIENCE" envDefault:"homefinance-api"`
	AccessTTL  time.Duration `env:"ACCESS_TOKEN_TTL" envDefault:"15m"`
	RefreshTTL time.Duration `env:"REFRESH_TOKEN_TTL" envDefault:"336h"`
}

// OTPConfig agrupa os códigos de 6 dígitos (docs/SEGURANCA.md §1.1).
type OTPConfig struct {
	Pepper         Secret        `env:"OTP_PEPPER"`
	TTL            time.Duration `env:"OTP_TTL" envDefault:"15m"`
	MaxAttempts    int           `env:"OTP_MAX_ATTEMPTS" envDefault:"5"`
	ResendInterval time.Duration `env:"OTP_RESEND_INTERVAL" envDefault:"60s"`
}

// Argon2Config agrupa o custo do hash de senha (docs/SEGURANCA.md §1).
type Argon2Config struct {
	MemoryKiB     uint32 `env:"ARGON2_MEMORY_KIB" envDefault:"65536"`
	Iterations    uint32 `env:"ARGON2_ITERATIONS" envDefault:"3"`
	Parallelism   uint8  `env:"ARGON2_PARALLELISM" envDefault:"2"`
	MaxConcurrent int    `env:"ARGON2_MAX_CONCURRENT" envDefault:"4"`
}

// AuthConfig agrupa o comportamento dos endpoints sensíveis.
type AuthConfig struct {
	MinResponseTime time.Duration `env:"AUTH_MIN_RESPONSE_TIME" envDefault:"300ms"`
}

// CookieConfig agrupa os atributos dos cookies de sessão (ADR-013).
type CookieConfig struct {
	Secure bool   `env:"COOKIE_SECURE" envDefault:"false"`
	Domain string `env:"COOKIE_DOMAIN"`
}

// MailConfig agrupa o envio de e-mail (ADR-009).
type MailConfig struct {
	Mailer    string `env:"MAILER" envDefault:"console"`
	From      string `env:"MAIL_FROM" envDefault:"nao-responda@homefinance.local"`
	FromName  string `env:"MAIL_FROM_NAME" envDefault:"HomeFinance"`
	QueueSize int    `env:"MAIL_QUEUE_SIZE" envDefault:"256"`
}

// SMTPConfig agrupa o servidor SMTP de produção.
type SMTPConfig struct {
	Host     string `env:"SMTP_HOST"`
	Port     int    `env:"SMTP_PORT" envDefault:"587"`
	Username string `env:"SMTP_USERNAME"`
	Password Secret `env:"SMTP_PASSWORD"`
	TLS      bool   `env:"SMTP_TLS" envDefault:"true"`
}

// Config é a configuração completa da aplicação.
type Config struct {
	AppEnv string `env:"APP_ENV" envDefault:"development"`

	HTTP   HTTPConfig
	Log    LogConfig
	DB     DBConfig
	JWT    JWTConfig
	OTP    OTPConfig
	Argon2 Argon2Config
	Auth   AuthConfig
	Cookie CookieConfig
	Mail   MailConfig
	SMTP   SMTPConfig

	// RateLimits não vem de ambiente de propósito: são limites de segurança,
	// não sintonia operacional. Afrouxá-los exige mudança de código revisada
	// (a §7 da spec 0001 fixa os padrões).
	RateLimits RateLimits
}

// IsProduction informa se a aplicação está em produção.
func (c Config) IsProduction() bool { return c.AppEnv == EnvProduction }

// LogValue implementa slog.LogValuer: mesmo que alguém logue a configuração
// inteira por engano, nenhum segredo sai (docs/SEGURANCA.md §4).
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("app_env", c.AppEnv),
		slog.String("http_addr", c.HTTP.Addr),
		slog.String("db_driver", c.DB.Driver),
		slog.String("mailer", c.Mail.Mailer),
		// A chave evita a palavra "cookie" de propósito: o redator de log
		// substitui qualquer atributo cujo nome a contenha, e aqui o valor é
		// só um booleano de diagnóstico.
		slog.Bool("https_only", c.Cookie.Secure),
		slog.Bool("automigrate", c.DB.AutoMigrate),
	)
}

var secretParsers = map[reflect.Type]env.ParserFunc{
	reflect.TypeOf(Secret("")): func(v string) (any, error) { return Secret(v), nil },
}

// Load lê o ambiente do processo e valida tudo. Devolve erro (nunca panic)
// para que cmd/api decida como abortar.
func Load() (Config, error) { return LoadFrom(nil) }

// LoadFrom lê a configuração de um mapa explícito (nil = ambiente do
// processo). Existe para tornar as validações testáveis sem os.Setenv global.
func LoadFrom(environ map[string]string) (Config, error) {
	var cfg Config
	opts := env.Options{FuncMap: secretParsers}
	if environ != nil {
		opts.Environment = environ
	}
	if err := env.ParseWithOptions(&cfg, opts); err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	cfg.AppEnv = strings.ToLower(strings.TrimSpace(cfg.AppEnv))
	cfg.DB.Driver = strings.ToLower(strings.TrimSpace(cfg.DB.Driver))
	cfg.Mail.Mailer = strings.ToLower(strings.TrimSpace(cfg.Mail.Mailer))
	cfg.Log.Level = strings.ToLower(strings.TrimSpace(cfg.Log.Level))
	cfg.Log.Format = strings.ToLower(strings.TrimSpace(cfg.Log.Format))
	cfg.HTTP.CORSOrigins = normalizeOrigins(cfg.HTTP.CORSOrigins)
	cfg.RateLimits = DefaultRateLimits()

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func normalizeOrigins(in []string) []string {
	out := make([]string, 0, len(in))
	for _, o := range in {
		o = strings.TrimSpace(o)
		o = strings.TrimSuffix(o, "/")
		if o == "" {
			continue
		}
		out = append(out, strings.ToLower(o))
	}
	return out
}

// minSecretBytes é o piso de 256 bits exigido por docs/SEGURANCA.md §1.
const minSecretBytes = 32

func (c Config) validate() error {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	switch c.AppEnv {
	case EnvDevelopment, EnvTest, EnvProduction:
	default:
		add("APP_ENV deve ser development, test ou production")
	}

	// Segredos: piso de 32 bytes em QUALQUER ambiente (D8).
	if c.JWT.Secret.Len() < minSecretBytes {
		add("JWT_SECRET precisa de pelo menos %d bytes", minSecretBytes)
	}
	if c.OTP.Pepper.Len() < minSecretBytes {
		add("OTP_PEPPER precisa de pelo menos %d bytes", minSecretBytes)
	}
	if c.JWT.Secret.Len() >= minSecretBytes && c.JWT.Secret == c.OTP.Pepper {
		add("JWT_SECRET e OTP_PEPPER precisam ser segredos diferentes")
	}

	switch c.DB.Driver {
	case DriverSQLite, DriverPostgres, DriverMySQL, DriverSQLServer:
	default:
		add("DB_DRIVER deve ser sqlite, postgres, mysql ou sqlserver")
	}
	if c.DB.DSN.Len() == 0 {
		add("DB_DSN é obrigatório")
	}
	if c.DB.MaxOpenConns < 1 {
		add("DB_MAX_OPEN_CONNS deve ser >= 1")
	}
	if c.DB.MaxIdleConns < 0 || c.DB.MaxIdleConns > c.DB.MaxOpenConns {
		add("DB_MAX_IDLE_CONNS deve estar entre 0 e DB_MAX_OPEN_CONNS")
	}

	switch c.Mail.Mailer {
	case MailerConsole, MailerSMTP:
	default:
		add("MAILER deve ser console ou smtp")
	}
	if c.Mail.QueueSize < 1 {
		add("MAIL_QUEUE_SIZE deve ser >= 1")
	}
	if c.Mail.From == "" {
		add("MAIL_FROM é obrigatório")
	}

	switch c.Log.Format {
	case "json", "text":
	default:
		add("LOG_FORMAT deve ser json ou text")
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		add("LOG_LEVEL deve ser debug, info, warn ou error")
	}

	if c.JWT.AccessTTL < time.Minute || c.JWT.AccessTTL > 15*time.Minute {
		add("ACCESS_TOKEN_TTL deve ficar entre 1m e 15m (docs/SEGURANCA.md §1)")
	}
	if c.JWT.RefreshTTL <= c.JWT.AccessTTL {
		add("REFRESH_TOKEN_TTL deve ser maior que ACCESS_TOKEN_TTL")
	}

	if c.OTP.TTL < 5*time.Minute || c.OTP.TTL > 15*time.Minute {
		add("OTP_TTL deve ficar entre 5m e 15m (docs/SEGURANCA.md §1.1)")
	}
	if c.OTP.MaxAttempts < 1 || c.OTP.MaxAttempts > 5 {
		add("OTP_MAX_ATTEMPTS deve ficar entre 1 e 5 (docs/SEGURANCA.md §1.1)")
	}
	if c.OTP.ResendInterval < 30*time.Second {
		add("OTP_RESEND_INTERVAL deve ser >= 30s")
	}

	if c.Argon2.MemoryKiB < 65536 {
		add("ARGON2_MEMORY_KIB deve ser >= 65536 (64 MiB)")
	}
	if c.Argon2.Iterations < 3 {
		add("ARGON2_ITERATIONS deve ser >= 3")
	}
	if c.Argon2.Parallelism < 2 {
		add("ARGON2_PARALLELISM deve ser >= 2")
	}
	if c.Argon2.MaxConcurrent < 1 {
		add("ARGON2_MAX_CONCURRENT deve ser >= 1")
	}

	if c.Auth.MinResponseTime < 0 || c.Auth.MinResponseTime > 5*time.Second {
		add("AUTH_MIN_RESPONSE_TIME deve ficar entre 0 e 5s")
	}

	if c.HTTP.TrustedProxyCount < 0 {
		add("TRUSTED_PROXY_COUNT não pode ser negativo")
	}
	for _, o := range c.HTTP.CORSOrigins {
		if o == "*" {
			add("CORS_ORIGIN não pode conter curinga — cookies de sessão exigem origem exata")
			continue
		}
		if !strings.HasPrefix(o, "http://") && !strings.HasPrefix(o, "https://") {
			add("CORS_ORIGIN precisa de origens absolutas (http:// ou https://): %q", o)
		}
	}

	// __Host- proíbe o atributo Domain (D11 / ADR-013).
	if c.Cookie.Secure && c.Cookie.Domain != "" {
		add("COOKIE_DOMAIN precisa ficar vazio quando COOKIE_SECURE=true (prefixo __Host-)")
	}

	if c.Mail.Mailer == MailerSMTP {
		if c.SMTP.Host == "" {
			add("SMTP_HOST é obrigatório quando MAILER=smtp")
		}
		if c.SMTP.Port < 1 || c.SMTP.Port > 65535 {
			add("SMTP_PORT inválido")
		}
	}

	// Trava de produção (D8).
	if c.AppEnv == EnvProduction {
		if c.Mail.Mailer != MailerSMTP {
			add("MAILER precisa ser smtp em produção — o mailer de console imprime o código")
		}
		if c.SMTP.Host == "" || c.SMTP.Port == 0 || c.Mail.From == "" {
			add("SMTP_HOST, SMTP_PORT e MAIL_FROM são obrigatórios em produção")
		}
		if !c.SMTP.TLS {
			add("SMTP_TLS não pode ser desligado em produção")
		}
		if !c.Cookie.Secure {
			add("COOKIE_SECURE precisa ser true em produção")
		}
		if len(c.HTTP.CORSOrigins) == 0 {
			add("CORS_ORIGIN é obrigatório em produção")
		}
		if c.DB.Driver == DriverSQLite {
			add("DB_DRIVER=sqlite não é aceito em produção")
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalid, strings.Join(problems, "; "))
	}
	return nil
}
