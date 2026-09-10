// Package storage abre e configura a conexão com o banco.
//
// Fronteira do ADR-008: *gorm.DB existe SOMENTE dentro deste pacote e do
// subpacote gormstore. Nenhum service, handler ou domínio enxerga GORM.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlserver"
	"gorm.io/gorm"
)

// Drivers suportados (ADR-008).
const (
	DriverSQLite    = "sqlite"
	DriverPostgres  = "postgres"
	DriverMySQL     = "mysql"
	DriverSQLServer = "sqlserver"
)

// ErrUnsupportedDriver — DB_DRIVER desconhecido.
var ErrUnsupportedDriver = errors.New("driver de banco não suportado")

// Options descreve a conexão.
type Options struct {
	Driver          string
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	// SlowThreshold define a partir de quanto tempo uma consulta vira aviso.
	SlowThreshold time.Duration
	// Production silencia o logger do GORM e endurece a redação.
	Production bool
}

// DB é o handle opaco de banco entregue ao resto da aplicação.
//
// O acesso ao *gorm.DB é deliberadamente restrito: só o gormstore chama
// Gorm(). Assim o grep de "gorm.DB" em internal/ continua devolvendo apenas
// caminhos sob internal/platform/storage/ (critério de aceite 7).
type DB struct {
	gdb *gorm.DB
	sql *sql.DB
	dsn string
}

// Open conecta, aplica o pool e valida a conexão com um ping.
func Open(ctx context.Context, opts Options, lg *slog.Logger) (*DB, error) {
	dialector, err := dialectorFor(opts.Driver, opts.DSN)
	if err != nil {
		return nil, err
	}

	gcfg := &gorm.Config{
		Logger: NewGormLogger(lg, GormLoggerOptions{
			SlowThreshold: opts.SlowThreshold,
			Silent:        opts.Production,
		}),
		// Datas sempre em UTC: o banco não decide fuso por nós.
		NowFunc: func() time.Time { return time.Now().UTC() },
		// ADR-013(c): sem chave estrangeira física. A integridade é
		// garantida em Go dentro de transação (UnitOfWork).
		DisableForeignKeyConstraintWhenMigrating: true,
		// Traduz violação de unicidade para gorm.ErrDuplicatedKey em todos
		// os dialetos, o que evita comparar mensagem de erro por string.
		TranslateError: true,
	}

	gdb, err := gorm.Open(dialector, gcfg)
	if err != nil {
		return nil, fmt.Errorf("abrindo banco (%s): %w", opts.Driver, scrub(err, opts.DSN))
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("obtendo pool: %w", scrub(err, opts.DSN))
	}

	if opts.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(opts.MaxOpenConns)
	}
	if opts.MaxIdleConns >= 0 {
		sqlDB.SetMaxIdleConns(opts.MaxIdleConns)
	}
	if opts.ConnMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(opts.ConnMaxLifetime)
	}

	db := &DB{gdb: gdb, sql: sqlDB, dsn: opts.DSN}
	if err := db.Ping(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// New embrulha um *gorm.DB já aberto. Existe para os testes de repositório,
// que sobem SQLite em arquivo temporário.
func New(gdb *gorm.DB) (*DB, error) {
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("obtendo pool: %w", err)
	}
	return &DB{gdb: gdb, sql: sqlDB}, nil
}

// Gorm devolve o handle do GORM.
//
// USO RESTRITO a internal/platform/storage/gormstore. Chamar isto de um
// service ou handler quebra o ADR-008 e a revisão de segurança.
func (d *DB) Gorm() *gorm.DB { return d.gdb }

// Ping valida a conexão. O erro devolvido já vem com a DSN removida, para
// que o readiness possa logar o motivo sem vazar a senha do banco
// (risco 6 da §9 da spec 0001).
func (d *DB) Ping(ctx context.Context) error {
	if d == nil || d.sql == nil {
		return errors.New("banco não inicializado")
	}
	if err := d.sql.PingContext(ctx); err != nil {
		return fmt.Errorf("ping no banco: %w", scrub(err, d.dsn))
	}
	return nil
}

// Close fecha o pool.
func (d *DB) Close() error {
	if d == nil || d.sql == nil {
		return nil
	}
	if err := d.sql.Close(); err != nil {
		return fmt.Errorf("fechando banco: %w", scrub(err, d.dsn))
	}
	return nil
}

func dialectorFor(driver, dsn string) (gorm.Dialector, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case DriverSQLite:
		// glebarez/sqlite é puro Go (modernc.org/sqlite): sem CGo, o build
		// continua reproduzível e cross-compilável (ADR-008).
		return sqlite.Open(dsn), nil
	case DriverPostgres:
		return postgres.Open(dsn), nil
	case DriverMySQL:
		return mysql.Open(dsn), nil
	case DriverSQLServer:
		return sqlserver.Open(dsn), nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDriver, driver)
	}
}

// scrub remove a DSN (e a senha dentro dela) da mensagem de erro.
//
// Drivers de banco costumam ecoar a string de conexão inteira no erro. Sem
// isto, a senha do banco iria parar no log na primeira falha de conexão.
func scrub(err error, dsn string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, secret := range dsnSecrets(dsn) {
		msg = strings.ReplaceAll(msg, secret, "[REDACTED]")
	}
	if msg == err.Error() {
		return err
	}
	return errors.New(msg)
}

// dsnSecrets devolve os pedaços da DSN que não podem aparecer em log: a DSN
// inteira e, quando dá para identificar, só a senha.
func dsnSecrets(dsn string) []string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil
	}
	out := []string{dsn}

	// postgres://user:senha@host/db  e  mysql: user:senha@tcp(host)/db
	if at := strings.LastIndex(dsn, "@"); at > 0 {
		creds := dsn[:at]
		if scheme := strings.Index(creds, "://"); scheme >= 0 {
			creds = creds[scheme+3:]
		}
		if colon := strings.Index(creds, ":"); colon >= 0 && colon+1 < len(creds) {
			if pwd := creds[colon+1:]; pwd != "" {
				out = append(out, pwd)
			}
		}
	}

	// sqlserver / chave=valor
	for _, key := range []string{"password=", "Password=", "PASSWORD=", "pwd="} {
		if i := strings.Index(dsn, key); i >= 0 {
			rest := dsn[i+len(key):]
			if end := strings.IndexAny(rest, ";& "); end >= 0 {
				rest = rest[:end]
			}
			if rest != "" {
				out = append(out, rest)
			}
		}
	}
	return out
}

// IsDuplicate informa se o erro é violação de unicidade, em qualquer
// dialeto (depende de gorm.Config.TranslateError).
func IsDuplicate(err error) bool { return errors.Is(err, gorm.ErrDuplicatedKey) }
