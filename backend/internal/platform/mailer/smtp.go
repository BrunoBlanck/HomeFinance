package mailer

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/id"
)

// SMTPOptions configura o mailer de produção.
type SMTPOptions struct {
	Host     string
	Port     int
	Username string
	Password string
	// TLS exige canal cifrado. Nunca desligue em produção (a config já
	// impede).
	TLS     bool
	Timeout time.Duration
}

// SMTP entrega por net/smtp.
//
// Sem biblioteca de terceiros: a stdlib cobre STARTTLS, TLS implícito e
// autenticação PLAIN, e cada dependência a menos no caminho de credencial é
// uma superfície a menos (§6 da spec 0001).
type SMTP struct {
	opts SMTPOptions
}

// NewSMTP monta o mailer de produção.
func NewSMTP(opts SMTPOptions) (*SMTP, error) {
	if opts.Host == "" {
		return nil, errors.New("SMTP_HOST é obrigatório")
	}
	if opts.Port < 1 || opts.Port > 65535 {
		return nil, errors.New("SMTP_PORT inválido")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 15 * time.Second
	}
	return &SMTP{opts: opts}, nil
}

// Name identifica a implementação.
func (s *SMTP) Name() string { return "smtp" }

// Send entrega a mensagem.
func (s *SMTP) Send(ctx context.Context, msg Message) error {
	from, err := msg.Sender()
	if err != nil {
		return err
	}
	to, err := msg.Recipient()
	if err != nil {
		return err
	}
	raw, err := msg.Build(time.Now().UTC(), id.New())
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(s.opts.Host, strconv.Itoa(s.opts.Port))
	tlsCfg := &tls.Config{
		ServerName: s.opts.Host,
		MinVersion: tls.VersionTLS12,
	}

	conn, err := s.dial(ctx, addr, tlsCfg)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	client, err := smtp.NewClient(conn, s.opts.Host)
	if err != nil {
		return fmt.Errorf("abrindo sessão smtp: %w", err)
	}
	defer func() { _ = client.Close() }()

	// STARTTLS quando a porta não é a de TLS implícito.
	if s.opts.TLS && s.opts.Port != 465 {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return errors.New("servidor smtp não oferece STARTTLS e SMTP_TLS está ligado")
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("negociando starttls: %w", err)
		}
	}

	if s.opts.Username != "" {
		// Autenticação só sobre canal cifrado: mandar credencial em claro é
		// pior do que não autenticar.
		if state, ok := client.TLSConnectionState(); !ok || !state.HandshakeComplete {
			return errors.New("recusando autenticar smtp sem TLS")
		}
		auth := smtp.PlainAuth("", s.opts.Username, s.opts.Password, s.opts.Host)
		if err := client.Auth(auth); err != nil {
			// A mensagem do servidor pode ecoar a credencial; não a
			// propagamos.
			return errors.New("autenticação smtp recusada")
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("comando MAIL: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("comando RCPT: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("comando DATA: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		_ = w.Close()
		return fmt.Errorf("escrevendo corpo: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finalizando corpo: %w", err)
	}
	return client.Quit()
}

func (s *SMTP) dial(ctx context.Context, addr string, tlsCfg *tls.Config) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: s.opts.Timeout}

	// Porta 465: TLS implícito desde o primeiro byte.
	if s.opts.TLS && s.opts.Port == 465 {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("conectando smtps: %w", err)
		}
		return conn, nil
	}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("conectando smtp: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(s.opts.Timeout))
	}
	return conn, nil
}
