package gormstore_test

import (
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHouseholdRepository(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)
		h := s.makeHousehold(t, ctx, u.ID, household.RoleOwner)

		encontrada, err := s.households.ByID(ctx, h.ID)
		require.NoError(t, err)
		assert.Equal(t, "Casa de Bruno", encontrada.Name)

		_, err = s.households.ByID(ctx, "nao-existe")
		assert.ErrorIs(t, err, household.ErrNotFound)

		varias, err := s.households.ByIDs(ctx, []string{h.ID, "nao-existe"})
		require.NoError(t, err)
		require.Len(t, varias, 1)
		assert.Equal(t, h.ID, varias[0].ID)

		vazio, err := s.households.ByIDs(ctx, nil)
		require.NoError(t, err)
		assert.Empty(t, vazio)
	})
}

func TestMembershipRepositoryListaEmOrdemDeCriacao(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)

		base := now()
		for i, nome := range []string{"primeira", "segunda", "terceira"} {
			h := &household.Household{ID: s.nextID("h"), Name: nome, CreatedAt: base, UpdatedAt: base}
			require.NoError(t, s.households.Create(ctx, h))
			require.NoError(t, s.memberships.Create(ctx, &household.Membership{
				ID:          s.nextID("m"),
				HouseholdID: h.ID,
				UserID:      u.ID,
				Role:        household.RoleMember,
				CreatedAt:   base.Add(time.Duration(i) * time.Minute),
				UpdatedAt:   base,
			}))
		}

		lista, err := s.memberships.ListByUser(ctx, u.ID)
		require.NoError(t, err)
		require.Len(t, lista, 3)

		// D18: a casa ativa é a do vínculo mais antigo — a ordem precisa ser
		// determinística.
		for i := 1; i < len(lista); i++ {
			assert.False(t, lista[i].CreatedAt.Before(lista[i-1].CreatedAt))
		}

		n, err := s.memberships.CountByUser(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(3), n)
	})
}

// docs/SEGURANCA.md §2: a consulta filtra pelos DOIS lados. Buscar por um e
// conferir o outro depois é proibido.
func TestMembershipRepositoryFiltraPorUsuarioECasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()

		dono := s.makeUser(t, ctx, true)
		estranho := s.makeUser(t, ctx, true)
		casa := s.makeHousehold(t, ctx, dono.ID, household.RoleOwner)
		outraCasa := s.makeHousehold(t, ctx, estranho.ID, household.RoleOwner)

		m, err := s.memberships.ByUserAndHousehold(ctx, dono.ID, casa.ID)
		require.NoError(t, err)
		assert.Equal(t, household.RoleOwner, m.Role)

		// Usuário existente + casa existente, mas sem vínculo entre eles.
		_, err = s.memberships.ByUserAndHousehold(ctx, dono.ID, outraCasa.ID)
		assert.ErrorIs(t, err, household.ErrNotFound)

		_, err = s.memberships.ByUserAndHousehold(ctx, estranho.ID, casa.ID)
		assert.ErrorIs(t, err, household.ErrNotFound)
	})
}

func TestMembershipRepositoryVinculoEhUnicoPorCasaEUsuario(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)
		h := s.makeHousehold(t, ctx, u.ID, household.RoleOwner)

		err := s.memberships.Create(ctx, &household.Membership{
			ID:          s.nextID("m"),
			HouseholdID: h.ID,
			UserID:      u.ID,
			Role:        household.RoleMember,
			CreatedAt:   now(),
			UpdatedAt:   now(),
		})
		assert.Error(t, err, "o índice único ux_memberships_household_user precisa barrar o duplicado")
	})
}
