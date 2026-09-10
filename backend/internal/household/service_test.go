package household_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/id"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Repositórios em memória
// ---------------------------------------------------------------------------

type fakeHouseholds struct {
	mu    sync.Mutex
	items map[string]household.Household
	err   error
}

func newFakeHouseholds() *fakeHouseholds {
	return &fakeHouseholds{items: map[string]household.Household{}}
}

func (f *fakeHouseholds) Create(_ context.Context, h *household.Household) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.items[h.ID] = *h
	return nil
}

func (f *fakeHouseholds) ByID(_ context.Context, hid string) (*household.Household, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	h, ok := f.items[hid]
	if !ok {
		return nil, household.ErrNotFound
	}
	return &h, nil
}

func (f *fakeHouseholds) ByIDs(_ context.Context, ids []string) ([]household.Household, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []household.Household
	for _, hid := range ids {
		if h, ok := f.items[hid]; ok {
			out = append(out, h)
		}
	}
	return out, nil
}

type fakeMemberships struct {
	mu    sync.Mutex
	items []household.Membership
	err   error
}

func newFakeMemberships() *fakeMemberships { return &fakeMemberships{} }

func (f *fakeMemberships) Create(_ context.Context, m *household.Membership) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.items = append(f.items, *m)
	return nil
}

func (f *fakeMemberships) ListByUser(_ context.Context, userID string) ([]household.Membership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []household.Membership
	for _, m := range f.items {
		if m.UserID == userID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeMemberships) ByUserAndHousehold(_ context.Context, userID, hid string) (*household.Membership, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range f.items {
		if m.UserID == userID && m.HouseholdID == hid {
			out := m
			return &out, nil
		}
	}
	return nil, household.ErrNotFound
}

func (f *fakeMemberships) CountByUser(_ context.Context, userID string) (int64, error) {
	list, _ := f.ListByUser(context.Background(), userID)
	return int64(len(list)), nil
}

// ---------------------------------------------------------------------------
// Testes
// ---------------------------------------------------------------------------

func newService(t *testing.T) (*household.Service, *fakeHouseholds, *fakeMemberships) {
	t.Helper()
	hs, ms := newFakeHouseholds(), newFakeMemberships()
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	svc := household.NewService(hs, ms,
		household.WithIDs(id.Fixed("h-1", "m-1", "h-2", "m-2")),
		household.WithClock(func() time.Time { return base }),
	)
	return svc, hs, ms
}

// D1 e ADR-012.
func TestDefaultName(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"Bruno Blanck":              "Casa de Bruno",
		"  Bruno  ":                 "Casa de Bruno",
		"Bruno":                     "Casa de Bruno",
		"":                          "Minha casa",
		"   ":                       "Minha casa",
		"Maria da Silva de Souza":   "Casa de Maria",
		strings.Repeat("Ana ", 100): "Casa de Ana",
	}
	for entrada, esperado := range casos {
		assert.Equal(t, esperado, household.DefaultName(entrada), "entrada %q", entrada)
	}

	longo := household.DefaultName(strings.Repeat("x", 500))
	assert.LessOrEqual(t, len([]rune(longo)), household.MaxNameLen)
}

// D2: idempotente e roda também no login como auto-reparo.
func TestEnsureDefaultEhIdempotente(t *testing.T) {
	t.Parallel()

	svc, hs, ms := newService(t)
	ctx := t.Context()

	primeira, err := svc.EnsureDefault(ctx, "u-1", "Bruno Blanck")
	require.NoError(t, err)
	assert.Equal(t, "Casa de Bruno", primeira.Name)
	assert.Equal(t, household.RoleOwner, primeira.Role)

	segunda, err := svc.EnsureDefault(ctx, "u-1", "Bruno Blanck")
	require.NoError(t, err)
	assert.Equal(t, primeira, segunda, "a segunda chamada não pode criar outra casa")

	assert.Len(t, hs.items, 1)
	assert.Len(t, ms.items, 1)
}

func TestEnsureDefaultExigeUsuario(t *testing.T) {
	t.Parallel()

	svc, _, _ := newService(t)
	_, err := svc.EnsureDefault(t.Context(), "", "Bruno")
	assert.ErrorIs(t, err, household.ErrNotFound)
}

func TestEnsureDefaultPropagaFalhaDoVinculo(t *testing.T) {
	t.Parallel()

	svc, _, ms := newService(t)
	ms.err = errors.New("banco fora do ar")

	_, err := svc.EnsureDefault(t.Context(), "u-1", "Bruno")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vínculo")
}

// D18: a casa ativa é a do vínculo mais antigo.
func TestActiveUsaOVinculoMaisAntigo(t *testing.T) {
	t.Parallel()

	svc, hs, ms := newService(t)
	ctx := t.Context()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i, nome := range []string{"Casa nova", "Casa antiga"} {
		hid := "h-" + string(rune('a'+i))
		require.NoError(t, hs.Create(ctx, &household.Household{ID: hid, Name: nome}))
		require.NoError(t, ms.Create(ctx, &household.Membership{
			ID:          "m-" + hid,
			HouseholdID: hid,
			UserID:      "u-1",
			Role:        household.RoleMember,
			CreatedAt:   base.Add(time.Duration(1-i) * time.Hour),
		}))
	}
	// A ordenação é responsabilidade do repositório; aqui simulamos a ordem
	// já correta invertendo a lista.
	ms.items[0], ms.items[1] = ms.items[1], ms.items[0]

	ativa, err := svc.Active(ctx, "u-1")
	require.NoError(t, err)
	assert.Equal(t, "Casa antiga", ativa.Name)
}

func TestActiveSemVinculo(t *testing.T) {
	t.Parallel()

	svc, _, _ := newService(t)
	_, err := svc.Active(t.Context(), "u-sem-casa")
	assert.ErrorIs(t, err, household.ErrNotFound)
}

// Risco 2 da §9: revalidar o vínculo é o que impede um token válido de dar
// acesso à casa de outra pessoa.
func TestMembershipOfFiltraPelosDoisLados(t *testing.T) {
	t.Parallel()

	svc, _, _ := newService(t)
	ctx := t.Context()

	minha, err := svc.EnsureDefault(ctx, "u-1", "Bruno")
	require.NoError(t, err)

	achado, err := svc.MembershipOf(ctx, "u-1", minha.ID)
	require.NoError(t, err)
	assert.Equal(t, minha.ID, achado.ID)

	_, err = svc.MembershipOf(ctx, "u-2", minha.ID)
	assert.ErrorIs(t, err, household.ErrNotFound, "usuário de fora não pode enxergar a casa")

	_, err = svc.MembershipOf(ctx, "u-1", "h-de-outra-pessoa")
	assert.ErrorIs(t, err, household.ErrNotFound)

	_, err = svc.MembershipOf(ctx, "", "")
	assert.ErrorIs(t, err, household.ErrNotFound)
}

// Sem FK física (ADR-013), vínculo apontando para casa inexistente é possível
// em caso de bug. O serviço ignora em vez de devolver casa fantasma.
func TestListForUserIgnoraVinculoOrfao(t *testing.T) {
	t.Parallel()

	svc, _, ms := newService(t)
	ctx := t.Context()

	require.NoError(t, ms.Create(ctx, &household.Membership{
		ID: "m-orfa", HouseholdID: "h-que-sumiu", UserID: "u-1", Role: household.RoleOwner,
	}))

	lista, err := svc.ListForUser(ctx, "u-1")
	require.NoError(t, err)
	assert.Empty(t, lista)
}

func TestValidRole(t *testing.T) {
	t.Parallel()

	assert.True(t, household.ValidRole(household.RoleOwner))
	assert.True(t, household.ValidRole(household.RoleMember))
	assert.False(t, household.ValidRole("admin"))
	assert.False(t, household.ValidRole(""))
}
