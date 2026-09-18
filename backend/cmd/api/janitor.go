package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/auth"
)

// importPurger é a varredura de importação que o janitor executa.
//
// Interface no consumidor, implementada por importer.Service. Ela existe para
// o teste do janitor poder injetar um relógio e um contador sem subir banco —
// e para deixar explícito que o janitor não sabe NADA sobre staging: ele só
// chama a varredura no intervalo certo e registra o resultado.
type importPurger interface {
	PurgeExpired(ctx context.Context) (expirados, linhas, lotes int64, err error)
}

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

// janitor expurga códigos e refresh tokens vencidos, rastro de auditoria
// antigo e o staging de importação.
//
// Sem isto, verification_codes e refresh_tokens crescem para sempre: cada
// registro, reenvio e login deixa linha. Além do custo, guardar hash de
// credencial além do necessário é aumentar a janela de um vazamento de banco.
//
// O staging da importação tem o MESMO raciocínio, e mais urgência: import_rows
// guarda descrição de lançamento — nome de contraparte — de um rascunho que
// ninguém confirmou. Lote pendente vencido vira `expired` e perde as linhas; o
// lote terminal sobrevive 180 dias como histórico, alinhado à retenção de
// auditoria (spec 0004 §3.6).
type janitor struct {
	auth    *auth.Service
	audit   *audit.Service
	imports importPurger
	lg      *slog.Logger
	opts    janitorOptions

	cancel context.CancelFunc
	wg     sync.WaitGroup
	once   sync.Once
}

func newJanitor(a *auth.Service, ad *audit.Service, imp importPurger, lg *slog.Logger, opts janitorOptions) *janitor {
	if opts.Interval <= 0 {
		opts.Interval = time.Hour
	}
	if opts.AuditRetention <= 0 {
		opts.AuditRetention = 180 * 24 * time.Hour
	}
	if opts.Timeout <= 0 {
		opts.Timeout = time.Minute
	}
	return &janitor{auth: a, audit: ad, imports: imp, lg: lg, opts: opts}
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

	// Cada expurgo é INDEPENDENTE, e cada um tem a sua guarda de "não ligado":
	// deixar de limpar credencial porque o staging falhou — ou porque uma
	// dependência não foi montada — seria trocar um problema por dois.
	var codes, tokens int64
	var err error
	if j.auth != nil {
		codes, tokens, err = j.auth.PurgeExpired(ctx)
		if err != nil {
			j.lg.ErrorContext(ctx, "falha no expurgo de credenciais", slog.String("reason", err.Error()))
		}
	}

	var entries int64
	if j.audit != nil {
		entries, err = j.audit.PurgeOlderThan(ctx, time.Now().UTC().Add(-j.opts.AuditRetention))
		if err != nil {
			j.lg.ErrorContext(ctx, "falha no expurgo de auditoria", slog.String("reason", err.Error()))
		}
	}

	var lotesExpirados, linhasDeStaging, lotesAntigos int64
	if j.imports != nil {
		lotesExpirados, linhasDeStaging, lotesAntigos, err = j.imports.PurgeExpired(ctx)
		if err != nil {
			j.lg.ErrorContext(ctx, "falha no expurgo de importação", slog.String("reason", err.Error()))
		}
	}

	total := codes + tokens + entries + lotesExpirados + linhasDeStaging + lotesAntigos
	if total > 0 {
		j.lg.InfoContext(ctx, "expurgo concluído",
			slog.Int64("codigos", codes),
			slog.Int64("refresh_tokens", tokens),
			slog.Int64("auditoria", entries),
			slog.Int64("lotes_expirados", lotesExpirados),
			slog.Int64("linhas_de_importacao", linhasDeStaging),
			slog.Int64("lotes_apagados", lotesAntigos),
		)
	}
}
