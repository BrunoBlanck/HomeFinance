package mailer

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// sendTimeout limita cada tentativa de entrega.
const sendTimeout = 30 * time.Second

// Queue entrega e-mail de forma ASSÍNCRONA.
//
// D7 da spec 0001 e risco 4 da §9: mandar SMTP dentro do request seria o
// maior vazamento de tempo do fluxo "esqueci a senha" — "e-mail existe" gasta
// a latência do servidor de e-mail, "não existe" responde na hora, e o
// atacante enumera a base com um cronômetro. Enfileirar remove essa variância
// por completo: o handler devolve sempre no mesmo tempo, aconteça o que
// acontecer com o SMTP.
//
// Fila cheia => a mensagem é DESCARTADA com log, nunca bloqueia o request
// (bloquear reintroduziria exatamente a variância que queremos eliminar).
type Queue struct {
	ch      chan Message
	mailer  Mailer
	lg      *slog.Logger
	wg      sync.WaitGroup
	closing atomic.Bool

	enqueued atomic.Int64
	dropped  atomic.Int64
	failed   atomic.Int64
	sent     atomic.Int64
}

// QueueOptions configura a fila.
type QueueOptions struct {
	Size    int
	Workers int
}

// NewQueue cria a fila e sobe os workers.
func NewQueue(m Mailer, lg *slog.Logger, opts QueueOptions) *Queue {
	if opts.Size < 1 {
		opts.Size = 128
	}
	if opts.Workers < 1 {
		opts.Workers = 2
	}
	q := &Queue{
		ch:     make(chan Message, opts.Size),
		mailer: m,
		lg:     lg,
	}
	for range opts.Workers {
		q.wg.Add(1)
		go q.worker()
	}
	return q
}

func (q *Queue) worker() {
	defer q.wg.Done()
	for msg := range q.ch {
		q.deliver(msg)
	}
}

func (q *Queue) deliver(msg Message) {
	// Recover local: um panic dentro do envio não pode matar o worker e
	// deixar a fila entupida para sempre.
	defer func() {
		if rv := recover(); rv != nil {
			q.failed.Add(1)
			q.lg.Error("panic ao enviar e-mail", slog.Any("panic", rv))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	if err := q.mailer.Send(ctx, msg); err != nil {
		q.failed.Add(1)
		// O erro NÃO carrega o corpo (que contém o código): logamos só o
		// motivo e nenhum dado da mensagem além de que houve falha.
		q.lg.Error("falha ao enviar e-mail",
			slog.String("mailer", q.mailer.Name()),
			slog.String("reason", err.Error()),
		)
		return
	}
	q.sent.Add(1)
}

// Enqueue entrega a mensagem para os workers. NUNCA bloqueia.
func (q *Queue) Enqueue(msg Message) {
	if q.closing.Load() {
		q.dropped.Add(1)
		return
	}
	select {
	case q.ch <- msg:
		q.enqueued.Add(1)
	default:
		q.dropped.Add(1)
		// Nada da mensagem entra no log: destinatário é dado pessoal e o
		// corpo tem o código.
		q.lg.Warn("fila de e-mail cheia; mensagem descartada",
			slog.Int64("dropped_total", q.dropped.Load()),
		)
	}
}

// Stats devolve os contadores (observabilidade e teste).
func (q *Queue) Stats() (enqueued, sent, failed, dropped int64) {
	return q.enqueued.Load(), q.sent.Load(), q.failed.Load(), q.dropped.Load()
}

// Close para de aceitar mensagens e DRENA o que já está na fila, respeitando
// o contexto. Chamado no desligamento gracioso.
func (q *Queue) Close(ctx context.Context) error {
	if q.closing.Swap(true) {
		return nil
	}
	close(q.ch)

	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		q.lg.Warn("fila de e-mail não drenou a tempo",
			slog.Int64("pendentes", int64(len(q.ch))),
		)
		return ctx.Err()
	}
}
