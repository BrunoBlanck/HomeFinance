package household

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/id"
)

// Seeder popula uma casa recém-criada.
//
// Interface declarada aqui, no consumidor: é o que permite este pacote semear
// categorias sem depender do pacote de categorias. Quem implementa é
// category.Service, ligado em cmd/api/main.go.
type Seeder interface {
	// SeedDefaults roda DENTRO da transação que cria a casa. Não deve abrir
	// transação própria, e precisa ser idempotente — EnsureDefault também roda
	// como auto-reparo no login.
	SeedDefaults(ctx context.Context, householdID string) error
}

// Service concentra a regra de negócio das casas.
type Service struct {
	households  Repository
	memberships MembershipRepository
	seeders     []Seeder
	ids         id.Generator
	clock       func() time.Time
}

// Option configura o Service.
type Option func(*Service)

// WithIDs injeta o gerador de IDs (teste).
func WithIDs(g id.Generator) Option { return func(s *Service) { s.ids = g } }

// WithClock injeta o relógio (teste).
func WithClock(c func() time.Time) Option { return func(s *Service) { s.clock = c } }

// WithSeeders registra quem popula a casa nova. Na E1 é a semente de
// categorias (D5); entregas futuras podem somar as suas.
func WithSeeders(seeders ...Seeder) Option {
	return func(s *Service) { s.seeders = append(s.seeders, seeders...) }
}

// NewService monta o serviço.
func NewService(households Repository, memberships MembershipRepository, opts ...Option) *Service {
	s := &Service{
		households:  households,
		memberships: memberships,
		ids:         id.New,
		clock:       func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// EnsureDefault garante o invariante "todo usuário verificado tem ao menos
// uma casa" (ADR-012 / D2 da spec 0001).
//
// É IDEMPOTENTE: se o usuário já tem vínculo, devolve a casa ativa sem
// escrever nada. Roda na verificação do e-mail e também no login, como
// auto-reparo — a claim hid do access token depende disso.
//
// Chamada dentro da transação do caller: sem FK física (ADR-013), casa sem
// vínculo é um estado que o banco NÃO barra, então casa e vínculo precisam
// nascer atomicamente.
func (s *Service) EnsureDefault(ctx context.Context, userID, userName string) (Summary, error) {
	if userID == "" {
		return Summary{}, fmt.Errorf("garantindo casa padrão: %w", ErrNotFound)
	}

	active, err := s.Active(ctx, userID)
	switch {
	case err == nil:
		return active, nil
	case !errors.Is(err, ErrNotFound):
		return Summary{}, err
	}

	now := s.clock()
	h := &Household{
		ID:   s.ids(),
		Name: DefaultName(userName),
		// Fuso e moeda nascem no padrão (ADR-019). O fuso é editável pela
		// casa; a moeda é BRL fixo no v1 e não é exposta para escrita.
		Timezone:  DefaultTimezone,
		Currency:  DefaultCurrency,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.households.Create(ctx, h); err != nil {
		return Summary{}, fmt.Errorf("criando casa padrão: %w", err)
	}

	m := &Membership{
		ID:          s.ids(),
		HouseholdID: h.ID,
		UserID:      userID,
		Role:        RoleOwner,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.memberships.Create(ctx, m); err != nil {
		return Summary{}, fmt.Errorf("criando vínculo de dono: %w", err)
	}

	// A semente roda na MESMA transação (S10 do PLANOS.md): sem FK física
	// (ADR-013), uma casa que nasce com metade das categorias é um estado que
	// o banco não barra, e que ninguém percebe até faltar uma categoria.
	for _, seeder := range s.seeders {
		if err := seeder.SeedDefaults(ctx, h.ID); err != nil {
			return Summary{}, fmt.Errorf("semeando casa nova: %w", err)
		}
	}

	return Summary{ID: h.ID, Name: h.Name, Role: m.Role, Timezone: h.Timezone, Currency: h.Currency}, nil
}

// Active devolve a casa ativa do usuário: a do vínculo MAIS ANTIGO (D18).
// Determinístico e sem coluna extra. Devolve ErrNotFound quando o usuário
// não tem nenhum vínculo.
func (s *Service) Active(ctx context.Context, userID string) (Summary, error) {
	list, err := s.ListForUser(ctx, userID)
	if err != nil {
		return Summary{}, err
	}
	if len(list) == 0 {
		return Summary{}, ErrNotFound
	}
	return list[0], nil
}

// ListForUser devolve todas as casas do usuário, da mais antiga para a mais
// nova.
func (s *Service) ListForUser(ctx context.Context, userID string) ([]Summary, error) {
	memberships, err := s.memberships.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("listando vínculos: %w", err)
	}
	if len(memberships) == 0 {
		return nil, nil
	}

	ids := make([]string, 0, len(memberships))
	for _, m := range memberships {
		ids = append(ids, m.HouseholdID)
	}

	households, err := s.households.ByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("carregando casas: %w", err)
	}
	byID := make(map[string]Household, len(households))
	for _, h := range households {
		byID[h.ID] = h
	}

	out := make([]Summary, 0, len(memberships))
	for _, m := range memberships {
		h, ok := byID[m.HouseholdID]
		if !ok {
			// Vínculo apontando para casa inexistente: sem FK física isso é
			// possível em caso de bug. Ignoramos em vez de devolver uma casa
			// fantasma para o cliente.
			continue
		}
		out = append(out, Summary{ID: h.ID, Name: h.Name, Role: m.Role, Timezone: h.Timezone, Currency: h.Currency})
	}
	return out, nil
}

// MembershipOf confirma que o usuário É MEMBRO da casa informada e devolve o
// resumo.
//
// É a revalidação exigida pelo risco 2 da §9 da spec 0001: um access token
// válido cujo hid aponta para uma casa da qual o usuário já não é membro
// não pode continuar valendo.
func (s *Service) MembershipOf(ctx context.Context, userID, householdID string) (Summary, error) {
	if userID == "" || householdID == "" {
		return Summary{}, ErrNotFound
	}
	m, err := s.memberships.ByUserAndHousehold(ctx, userID, householdID)
	if err != nil {
		return Summary{}, err
	}
	h, err := s.households.ByID(ctx, m.HouseholdID)
	if err != nil {
		return Summary{}, err
	}
	return Summary{ID: h.ID, Name: h.Name, Role: m.Role, Timezone: h.Timezone, Currency: h.Currency}, nil
}

// Today devolve o dia de hoje NO FUSO DA CASA.
//
// Existe como método de serviço, e não como helper solto, porque é a ÚNICA
// porta pela qual o resto do sistema pergunta "que dia é hoje?" (ADR-019 e
// risco R6 do PLANOS.md). Espalhar `time.Now()` pelos domínios é como o bug
// de "a conta venceu um dia antes" entra: às 21h em São Paulo já é o dia
// seguinte em UTC.
func (s *Service) Today(ctx context.Context, householdID string) (civil.Date, error) {
	h, err := s.households.ByID(ctx, householdID)
	if err != nil {
		return civil.Date{}, err
	}
	return civil.FromTime(s.clock(), h.Location()), nil
}
