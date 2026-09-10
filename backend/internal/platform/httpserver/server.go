package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// ServerOptions descreve o servidor HTTP.
type ServerOptions struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
}

// Server embrulha o http.Server com desligamento gracioso.
type Server struct {
	srv             *http.Server
	shutdownTimeout time.Duration
	lg              *slog.Logger
}

// NewServer monta o servidor. Todos os timeouts são obrigatórios na prática:
// sem ReadHeaderTimeout uma conexão lenta segura um handler para sempre
// (Slowloris).
func NewServer(opts ServerOptions, handler http.Handler, lg *slog.Logger) *Server {
	if opts.ReadHeaderTimeout <= 0 {
		opts.ReadHeaderTimeout = 5 * time.Second
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 15 * time.Second
	}
	return &Server{
		srv: &http.Server{
			Addr:              opts.Addr,
			Handler:           handler,
			ReadHeaderTimeout: opts.ReadHeaderTimeout,
			ReadTimeout:       opts.ReadTimeout,
			WriteTimeout:      opts.WriteTimeout,
			IdleTimeout:       opts.IdleTimeout,
			// O log de erro do net/http pode conter dados da conexão;
			// mandamos para o slog já redigido.
			ErrorLog: slog.NewLogLogger(lg.Handler(), slog.LevelWarn),
		},
		shutdownTimeout: opts.ShutdownTimeout,
		lg:              lg,
	}
}

// Addr devolve o endereço efetivo (útil quando a porta é 0 em teste).
func (s *Server) Addr() string { return s.srv.Addr }

// Run sobe o servidor e bloqueia até o contexto ser cancelado, então desliga
// graciosamente. Devolve nil no desligamento limpo.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return fmt.Errorf("abrindo listener em %s: %w", s.srv.Addr, err)
	}
	s.srv.Addr = ln.Addr().String()

	errCh := make(chan error, 1)
	go func() {
		s.lg.Info("servidor http ouvindo", slog.String("addr", ln.Addr().String()))
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("servindo http: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	s.lg.Info("desligando servidor http", slog.Duration("timeout", s.shutdownTimeout))
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdownTimeout)
	defer cancel()

	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		_ = s.srv.Close()
		return fmt.Errorf("desligando http: %w", err)
	}
	return <-errCh
}
