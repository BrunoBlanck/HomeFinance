package audit

import (
	"context"
	"log/slog"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/id"
)

// Service grava entradas de auditoria.
type Service struct {
	repo  Repository
	ids   id.Generator
	clock func() time.Time
	lg    *slog.Logger
}

// Option configura o Service.
type Option func(*Service)

// WithIDs injeta o gerador de IDs (teste).
func WithIDs(g id.Generator) Option { return func(s *Service) { s.ids = g } }

// WithClock injeta o relógio (teste).
func WithClock(c func() time.Time) Option { return func(s *Service) { s.clock = c } }

// NewService monta o serviço de auditoria.
func NewService(repo Repository, lg *slog.Logger, opts ...Option) *Service {
	s := &Service{repo: repo, ids: id.New, clock: func() time.Time { return time.Now().UTC() }, lg: lg}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Params descreve o evento a auditar.
type Params struct {
	Action      string
	Entity      string
	EntityID    string
	UserID      string
	HouseholdID string
	IP          string
}

// Record grava a entrada e devolve o erro.
//
// Use dentro de uma transação quando o evento faz parte de uma escrita
// multi-tabela (registro, verificação, reset): aí a auditoria tem de cair
// junto se a operação cair.
func (s *Service) Record(ctx context.Context, p Params) error {
	e := &Entry{
		ID:        s.ids(),
		Action:    p.Action,
		Entity:    p.Entity,
		IP:        p.IP,
		CreatedAt: s.clock(),
	}
	if p.EntityID != "" {
		v := p.EntityID
		e.EntityID = &v
	}
	if p.UserID != "" {
		v := p.UserID
		e.UserID = &v
	}
	if p.HouseholdID != "" {
		v := p.HouseholdID
		e.HouseholdID = &v
	}
	return s.repo.Create(ctx, e)
}

// TryRecord grava a entrada e apenas loga em caso de falha.
//
// Existe para os eventos que NÃO podem derrubar a requisição do usuário
// (login bem-sucedido, refresh). Um banco de auditoria indisponível não pode
// virar negação de serviço de autenticação.
func (s *Service) TryRecord(ctx context.Context, p Params) {
	if err := s.Record(ctx, p); err != nil && s.lg != nil {
		s.lg.WarnContext(ctx, "falha ao gravar auditoria",
			slog.String("action", p.Action),
			slog.String("reason", err.Error()),
		)
	}
}

// PurgeOlderThan expurga entradas antigas (janitor).
func (s *Service) PurgeOlderThan(ctx context.Context, before time.Time) (int64, error) {
	return s.repo.DeleteOlderThan(ctx, before)
}
