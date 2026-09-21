package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/session"
)

// Households é o que o serviço de usuário precisa saber sobre casas.
// Interface (e não o *household.Service concreto) para manter o teste sem
// banco e a fronteira explícita.
type Households interface {
	ListForUser(ctx context.Context, userID string) ([]household.Summary, error)
	MembershipOf(ctx context.Context, userID, householdID string) (household.Summary, error)
}

// Service concentra a regra de negócio de usuário.
type Service struct {
	repo       Repository
	households Households
}

// NewService monta o serviço.
func NewService(repo Repository, households Households) *Service {
	return &Service{repo: repo, households: households}
}

// HouseholdView é a casa como aparece no corpo de /me.
type HouseholdView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
	// O fuso é o que o cliente usa para saber em que MÊS ele está (ADR-019);
	// a moeda é BRL fixa no v1, e existe para a formatação não ter símbolo
	// escrito na mão em nenhuma tela.
	Timezone string `json:"timezone"`
	Currency string `json:"currency"`
}

// UserView é o usuário como aparece no corpo de /me. É um DTO próprio: a
// entidade User nunca é serializada, para que um campo novo (outro hash,
// outro segredo) não vaze por acidente.
type UserView struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Email           string  `json:"email"`
	EmailVerifiedAt *string `json:"emailVerifiedAt"`
	CreatedAt       string  `json:"createdAt"`
}

// MeView é o corpo completo de GET /me, reaproveitado por login, verificação
// e refresh (§3.11 da spec 0001).
type MeView struct {
	User       UserView        `json:"user"`
	Household  HouseholdView   `json:"household"`
	Households []HouseholdView `json:"households"`
}

// Me monta a visão da sessão atual.
//
// Revalida a membership do hid do token contra o banco (risco 2 da §9 da
// spec 0001): um access token continua criptograficamente válido mesmo
// depois de o vínculo ser removido, e sem esta checagem ele daria acesso à
// casa de outra pessoa.
func (s *Service) Me(ctx context.Context, ident session.Identity) (*MeView, error) {
	u, err := s.repo.ByID(ctx, ident.UserID)
	if err != nil {
		return nil, fmt.Errorf("carregando usuário: %w", err)
	}

	active, err := s.households.MembershipOf(ctx, ident.UserID, ident.HouseholdID)
	if err != nil {
		if errors.Is(err, household.ErrNotFound) {
			return nil, household.ErrNotFound
		}
		return nil, fmt.Errorf("revalidando vínculo: %w", err)
	}

	all, err := s.households.ListForUser(ctx, ident.UserID)
	if err != nil {
		return nil, fmt.Errorf("listando casas: %w", err)
	}

	views := make([]HouseholdView, 0, len(all))
	for _, h := range all {
		views = append(views, HouseholdView{ID: h.ID, Name: h.Name, Role: h.Role, Timezone: h.Timezone, Currency: h.Currency})
	}

	return &MeView{
		User: UserView{
			ID:              u.ID,
			Name:            u.Name,
			Email:           u.Email,
			EmailVerifiedAt: formatTimePtr(u.EmailVerifiedAt),
			CreatedAt:       formatTime(u.CreatedAt),
		},
		Household:  HouseholdView{ID: active.ID, Name: active.Name, Role: active.Role, Timezone: active.Timezone, Currency: active.Currency},
		Households: views,
	}, nil
}

// formatTime devolve ISO 8601 UTC com precisão de segundos, o formato do
// contrato ("2026-09-09T14:03:11Z"). Fixar o formato evita que a precisão do
// banco (segundos no MySQL, nanos no Postgres) mude a resposta da API.
func formatTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(time.RFC3339)
}

func formatTimePtr(t *time.Time) *string {
	if t == nil || t.IsZero() {
		return nil
	}
	v := formatTime(*t)
	return &v
}
