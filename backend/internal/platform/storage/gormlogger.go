package storage

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// GormLoggerOptions configura a ponte GORM -> slog.
type GormLoggerOptions struct {
	// SlowThreshold: acima disso a consulta vira aviso. 0 usa 200ms.
	SlowThreshold time.Duration
	// Silent desliga tudo menos erro. Ligado em produção.
	Silent bool
}

// gormLogger implementa logger.Interface do GORM.
//
// Ponto de segurança deste arquivo (risco 6 da §9 da spec 0001): o logger
// padrão do GORM imprime o SQL COM OS VALORES INTERPOLADOS. Numa API de
// autenticação isso significa hash de senha, hash de código OTP e hash de
// refresh token em disco. Por isso nunca chamamos a função que produz o SQL:
// registramos apenas duração, linhas afetadas e a classe do erro.
type gormLogger struct {
	lg            *slog.Logger
	level         logger.LogLevel
	slowThreshold time.Duration
	silent        bool
}

// NewGormLogger monta a ponte.
func NewGormLogger(lg *slog.Logger, opts GormLoggerOptions) logger.Interface {
	if opts.SlowThreshold <= 0 {
		opts.SlowThreshold = 200 * time.Millisecond
	}
	level := logger.Warn
	if opts.Silent {
		level = logger.Error
	}
	return &gormLogger{lg: lg, level: level, slowThreshold: opts.SlowThreshold, silent: opts.Silent}
}

// LogMode devolve uma cópia com outro nível.
func (l *gormLogger) LogMode(level logger.LogLevel) logger.Interface {
	clone := *l
	clone.level = level
	return &clone
}

func (l *gormLogger) Info(ctx context.Context, msg string, data ...any) {
	if l.level >= logger.Info {
		l.lg.InfoContext(ctx, "gorm", slog.String("msg", fmt.Sprintf(msg, data...)))
	}
}

func (l *gormLogger) Warn(ctx context.Context, msg string, data ...any) {
	if l.level >= logger.Warn {
		l.lg.WarnContext(ctx, "gorm", slog.String("msg", fmt.Sprintf(msg, data...)))
	}
}

func (l *gormLogger) Error(ctx context.Context, msg string, data ...any) {
	if l.level >= logger.Error {
		l.lg.ErrorContext(ctx, "gorm", slog.String("msg", fmt.Sprintf(msg, data...)))
	}
}

// Trace é chamado ao fim de cada consulta.
//
// fc() devolveria o SQL com os valores embutidos — NUNCA é chamado aqui.
func (l *gormLogger) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	if l.level <= logger.Silent {
		return
	}
	elapsed := time.Since(begin)

	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound) && l.level >= logger.Error:
		attrs := []slog.Attr{
			slog.Int64("elapsed_ms", elapsed.Milliseconds()),
			slog.String("kind", classifyDBError(err)),
		}
		if !l.silent {
			// Fora de produção o texto do erro é essencial para depurar. Em
			// produção ele fica de fora: mensagens de violação de unicidade
			// costumam ecoar o valor da coluna (e-mail, por exemplo).
			attrs = append(attrs, slog.String("detail", err.Error()))
		}
		l.lg.LogAttrs(ctx, slog.LevelError, "consulta falhou", attrs...)

	case elapsed > l.slowThreshold && l.level >= logger.Warn:
		rows := int64(-1)
		if fc != nil {
			_, rows = fc()
		}
		l.lg.LogAttrs(ctx, slog.LevelWarn, "consulta lenta",
			slog.Int64("elapsed_ms", elapsed.Milliseconds()),
			slog.Int64("rows", rows),
		)
	}
}

func classifyDBError(err error) string {
	switch {
	case errors.Is(err, gorm.ErrDuplicatedKey):
		return "duplicated_key"
	case errors.Is(err, gorm.ErrForeignKeyViolated):
		return "foreign_key"
	case errors.Is(err, gorm.ErrInvalidTransaction):
		return "invalid_transaction"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return "other"
	}
}
