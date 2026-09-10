package mailer_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/mailer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type spyMailer struct {
	mu       sync.Mutex
	recebido []mailer.Message
	err      error
	delay    time.Duration
	panica   bool
}

func (s *spyMailer) Name() string { return "spy" }

func (s *spyMailer) Send(_ context.Context, m mailer.Message) error {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.panica {
		panic("falha proposital no envio")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.recebido = append(s.recebido, m)
	return nil
}

func (s *spyMailer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recebido)
}

// D7: Enqueue nunca bloqueia — é o que remove a variância do SMTP do tempo de
// resposta e sustenta os grupos A, B e C da §3.12.
func TestEnqueueNaoBloqueia(t *testing.T) {
	t.Parallel()

	spy := &spyMailer{delay: 200 * time.Millisecond}
	q := mailer.NewQueue(spy, logging.Discard(), mailer.QueueOptions{Size: 16, Workers: 1})
	t.Cleanup(func() { _ = q.Close(context.Background()) })

	inicio := time.Now()
	for range 8 {
		q.Enqueue(msgBase())
	}
	assert.Less(t, time.Since(inicio), 50*time.Millisecond,
		"o enfileiramento precisa ser instantâneo, aconteça o que acontecer com o SMTP")
}

func TestCloseDrenaAFila(t *testing.T) {
	t.Parallel()

	spy := &spyMailer{}
	q := mailer.NewQueue(spy, logging.Discard(), mailer.QueueOptions{Size: 32, Workers: 2})

	for range 20 {
		q.Enqueue(msgBase())
	}
	require.NoError(t, q.Close(context.Background()))
	assert.Equal(t, 20, spy.count(), "o desligamento precisa entregar o que já foi aceito")

	enfileirados, enviados, falhas, descartados := q.Stats()
	assert.Equal(t, int64(20), enfileirados)
	assert.Equal(t, int64(20), enviados)
	assert.Zero(t, falhas)
	assert.Zero(t, descartados)
}

// Fila cheia descarta com log e NUNCA bloqueia o request.
func TestFilaCheiaDescarta(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := logging.New(&buf, logging.Options{Level: "debug", Format: "json"})

	liberar := make(chan struct{})
	bloqueante := &spyMailer{delay: 0}
	q := mailer.NewQueue(&travaMailer{spy: bloqueante, liberar: liberar}, lg, mailer.QueueOptions{Size: 1, Workers: 1})

	for range 50 {
		q.Enqueue(msgBase())
	}
	close(liberar)

	_, _, _, descartados := q.Stats()
	assert.Positive(t, descartados, "com a fila cheia é preciso descartar")

	// O log do descarte não pode conter destinatário nem corpo.
	logs := buf.String()
	assert.Contains(t, logs, "fila de e-mail cheia")
	assert.NotContains(t, logs, "bruno@exemplo.com")
	assert.NotContains(t, logs, "123456")

	require.NoError(t, q.Close(context.Background()))
}

type travaMailer struct {
	spy     *spyMailer
	liberar chan struct{}
}

func (t *travaMailer) Name() string { return "trava" }

func (t *travaMailer) Send(ctx context.Context, m mailer.Message) error {
	<-t.liberar
	return t.spy.Send(ctx, m)
}

func TestFalhaDeEnvioNaoDerrubaOWorker(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := logging.New(&buf, logging.Options{Level: "debug", Format: "json"})

	spy := &spyMailer{err: errors.New("smtp recusou a conexão")}
	q := mailer.NewQueue(spy, lg, mailer.QueueOptions{Size: 8, Workers: 1})

	for range 5 {
		q.Enqueue(msgBase())
	}
	require.NoError(t, q.Close(context.Background()))

	_, _, falhas, _ := q.Stats()
	assert.Equal(t, int64(5), falhas)

	logs := buf.String()
	assert.Contains(t, logs, "falha ao enviar e-mail")
	// O log da falha não carrega o corpo (que contém o código).
	assert.NotContains(t, logs, "123456")
	assert.NotContains(t, logs, "bruno@exemplo.com")
}

func TestPanicNoEnvioNaoDerrubaOWorker(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := logging.New(&buf, logging.Options{Level: "debug", Format: "json"})

	q := mailer.NewQueue(&spyMailer{panica: true}, lg, mailer.QueueOptions{Size: 4, Workers: 1})
	q.Enqueue(msgBase())
	q.Enqueue(msgBase())

	require.NoError(t, q.Close(context.Background()))
	assert.Contains(t, buf.String(), "panic ao enviar e-mail")
}

func TestEnqueueDepoisDoCloseDescarta(t *testing.T) {
	t.Parallel()

	spy := &spyMailer{}
	q := mailer.NewQueue(spy, logging.Discard(), mailer.QueueOptions{Size: 4, Workers: 1})
	require.NoError(t, q.Close(context.Background()))

	assert.NotPanics(t, func() { q.Enqueue(msgBase()) })
	assert.NoError(t, q.Close(context.Background()), "Close é idempotente")
}

// ADR-009: o mailer de console imprime o código, por isso escreve num writer
// PRÓPRIO — nunca no *slog.Logger da aplicação.
func TestConsoleImprimeNoWriterProprio(t *testing.T) {
	t.Parallel()

	var saida bytes.Buffer
	var logs bytes.Buffer
	lg := logging.New(&logs, logging.Options{Level: "debug", Format: "json"})

	c := mailer.NewConsole(&saida)
	q := mailer.NewQueue(c, lg, mailer.QueueOptions{Size: 4, Workers: 1})
	q.Enqueue(msgBase())
	require.NoError(t, q.Close(context.Background()))

	assert.Equal(t, "console", c.Name())
	assert.Contains(t, saida.String(), "123456", "o código precisa aparecer no console de desenvolvimento")
	assert.Contains(t, saida.String(), "bruno@exemplo.com")

	// E não pode aparecer no log estruturado (critério de aceite 16).
	assert.NotContains(t, logs.String(), "123456")
	assert.NotContains(t, logs.String(), "bruno@exemplo.com")
}

func TestConsoleRecusaInjecaoDeCabecalho(t *testing.T) {
	t.Parallel()

	var saida bytes.Buffer
	c := mailer.NewConsole(&saida)

	m := msgBase()
	m.Subject = "Oi\r\nBcc: vitima@alvo.com"

	err := c.Send(context.Background(), m)
	assert.ErrorIs(t, err, mailer.ErrHeaderInjection,
		"o mailer de dev precisa ter o mesmo contrato do de produção")
	assert.Empty(t, saida.String())
}

func TestNotifierMontaAsTresMensagens(t *testing.T) {
	t.Parallel()

	spy := &spyMailer{}
	q := mailer.NewQueue(spy, logging.Discard(), mailer.QueueOptions{Size: 8, Workers: 1})
	n := mailer.NewNotifier(q, "nao-responda@homefinance.test", "HomeFinance")

	n.EnqueueVerificationCode("bruno@exemplo.com", "042317", 15*time.Minute)
	n.EnqueuePasswordResetCode("bruno@exemplo.com", "Bruno", "998877", 15*time.Minute)
	n.EnqueueAccountExistsNotice("bruno@exemplo.com", "Bruno")

	require.NoError(t, q.Close(context.Background()))
	require.Equal(t, 3, spy.count())

	spy.mu.Lock()
	defer spy.mu.Unlock()

	verificacao := spy.recebido[0]
	assert.Contains(t, verificacao.Body, "042317")
	// Primeiro contato: nenhum nome, nem no cabeçalho nem na saudação
	// (achado ALTA-2 da revisão de segurança).
	assert.Empty(t, verificacao.ToName)
	assert.Contains(t, verificacao.Body, "Olá,\n")
	assert.Contains(t, verificacao.Body, "15 minutos")
	assert.Contains(t, verificacao.Body, "Nunca compartilhe")
	assert.Equal(t, "nao-responda@homefinance.test", verificacao.From)

	reset := spy.recebido[1]
	assert.Contains(t, reset.Body, "998877")
	assert.Contains(t, reset.Body, "sessões abertas são encerradas")

	aviso := spy.recebido[2]
	assert.NotContains(t, aviso.Body, "042317")
	assert.Contains(t, aviso.Body, "já está cadastrado")

	// Nenhuma mensagem é HTML: não há o que sanitizar nem rastreador remoto.
	for _, m := range spy.recebido {
		assert.False(t, strings.Contains(m.Body, "<"), "corpo precisa ser texto puro")
	}
}

func TestNewSMTPValidaOpcoes(t *testing.T) {
	t.Parallel()

	_, err := mailer.NewSMTP(mailer.SMTPOptions{})
	assert.Error(t, err)

	_, err = mailer.NewSMTP(mailer.SMTPOptions{Host: "smtp.exemplo.com", Port: 0})
	assert.Error(t, err)

	s, err := mailer.NewSMTP(mailer.SMTPOptions{Host: "smtp.exemplo.com", Port: 587, TLS: true})
	require.NoError(t, err)
	assert.Equal(t, "smtp", s.Name())
}

func TestSMTPRecusaMensagemComInjecao(t *testing.T) {
	t.Parallel()

	s, err := mailer.NewSMTP(mailer.SMTPOptions{Host: "127.0.0.1", Port: 1, TLS: false, Timeout: time.Second})
	require.NoError(t, err)

	m := msgBase()
	m.To = "bruno@exemplo.com\r\nBcc: vitima@alvo.com"

	// Falha na VALIDAÇÃO, antes de qualquer conexão de rede.
	err = s.Send(context.Background(), m)
	assert.ErrorIs(t, err, mailer.ErrHeaderInjection)
}
