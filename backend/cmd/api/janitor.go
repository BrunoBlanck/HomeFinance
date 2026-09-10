package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/auth"
)

// janitorOptions configura o expurgo periódico.
type janitorOptions struct {
	// Interval entre passadas. 0 = 1 hora.
	Interval time.Duration
	// AuditRetention é por quanto tempo o rastro de auditoria é mantido.
	// 0 = 180 dias.
	AuditRetention time.Duration
	// Timeout de cada passada. 0 = 1 minuto.
	Timeout time.Duration
}

// janitor expurga códigos e refresh tokens vencidos.
//
// Sem isto, verification_codes e refresh_tokens crescem para sempre: cada
// registro, reenvio e login deixa linha. Além do custo, guardar hash de
// credencial além do necessário é aumentar a janela de um vazamento de banco.
type janitor struct {
	auth  *auth.Service
	audit *audit.Service
	lg    *slog.Logger
	opts  janitorOptions

	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

func newJanitor(a *auth.Service, ad *audit.Service, lg *slog.Logger, opts janitorOptions) *janitor {
	if opts.Interval <= 0 {
		opts.Interval = time.Hour
	}
	if opts.AuditRetention <= 0 {
		opts.AuditRetention = 180 * 24 * time.Hour
	}
	if opts.Timeout <= 0 {
		opts.Timeout = time.Minute
	}
	return &janitor{auth: a, audit: ad, lg: lg, opts: opts}
}

// Start sobe o laço em segundo plano.
func (j *janitor) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	j.cancel = cancel

	j.wg.Add(1)
	go func() {
		defer j.wg.Done()

		ticker := time.NewTicker(j.opts.Interval)
		defer ticker.Stop()

		// Uma passada logo no boot: reinícios frequentes não podem adiar o
		// expurgo indefinidamente.
		j.sweep(ctx)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				j.sweep(ctx)
			}
		}
	}()
}

// Stop encerra o laço e espera a passada em andamento.
func (j *janitor) Stop() {
	j.once.Do(func() {
		if j.cancel != nil {
			j.cancel()
		}
		j.wg.Wait()
	})
}

func (j *janitor) sweep(parent context.Context) {
	// Recover local: um erro aqui não pode derrubar o processo do servidor.
	defer func() {
		if rv := recover(); rv != nil {
			j.lg.Error("panic no janitor", slog.Any("panic", rv))
		}
	}()

	ctx, cancel := context.WithTimeout(parent, j.opts.Timeout)
	defer cancel()

	codes, tokens, err := j.auth.PurgeExpired(ctx)
	if err != nil {
		j.lg.ErrorContext(ctx, "falha no expurgo de credenciais", slog.String("reason", err.Error()))
	}

	entries, err := j.audit.PurgeOlderThan(ctx, time.Now().UTC().Add(-j.opts.AuditRetention))
	if err != nil {
		j.lg.ErrorContext(ctx, "falha no expurgo de auditoria", slog.String("reason", err.Error()))
	}

	if codes+tokens+entries > 0 {
		j.lg.InfoContext(ctx, "expurgo concluído",
			slog.Int64("codigos", codes),
			slog.Int64("refresh_tokens", tokens),
			slog.Int64("auditoria", entries),
		)
	}
}
