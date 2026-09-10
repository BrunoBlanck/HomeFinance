package gormstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ADR-013(c): sem FK física, casa sem vínculo NÃO é barrada pelo banco. A
// transação é a única coisa que impede esse estado — por isso este teste.
func TestUnitOfWorkFazRollbackDeEscritaMultiTabela(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casaID := s.nextID("h")
		falha := errors.New("falha proposital no meio da transação")

		err := s.uow.Do(ctx, func(ctx context.Context) error {
			if err := s.households.Create(ctx, &household.Household{
				ID: casaID, Name: "Casa órfã", CreatedAt: now(), UpdatedAt: now(),
			}); err != nil {
				return err
			}
			return falha
		})
		require.ErrorIs(t, err, falha)

		_, err = s.households.ByID(ctx, casaID)
		assert.ErrorIs(t, err, household.ErrNotFound, "a casa não pode sobreviver ao rollback")
	})
}

func TestUnitOfWorkCommitaTudoJunto(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()

		userID := s.nextID("u")
		casaID := s.nextID("h")
		email := s.nextEmail()

		err := s.uow.Do(ctx, func(ctx context.Context) error {
			if err := s.users.Create(ctx, &user.User{
				ID: userID, Email: email, PasswordHash: "hash", Name: "Bruno",
				CreatedAt: now(), UpdatedAt: now(),
			}); err != nil {
				return err
			}
			if err := s.households.Create(ctx, &household.Household{
				ID: casaID, Name: "Casa de Bruno", CreatedAt: now(), UpdatedAt: now(),
			}); err != nil {
				return err
			}
			if err := s.memberships.Create(ctx, &household.Membership{
				ID: s.nextID("m"), HouseholdID: casaID, UserID: userID,
				Role: household.RoleOwner, CreatedAt: now(), UpdatedAt: now(),
			}); err != nil {
				return err
			}
			return s.audit.Create(ctx, &audit.Entry{
				ID: s.nextID("a"), Action: audit.ActionEmailVerified,
				Entity: audit.EntityUser, IP: "203.0.113.1", CreatedAt: now(),
			})
		})
		require.NoError(t, err)

		_, err = s.users.ByID(ctx, userID)
		assert.NoError(t, err)
		_, err = s.households.ByID(ctx, casaID)
		assert.NoError(t, err)
		m, err := s.memberships.ByUserAndHousehold(ctx, userID, casaID)
		require.NoError(t, err)
		assert.Equal(t, household.RoleOwner, m.Role)
	})
}

func TestUnitOfWorkEhReentrante(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casaID := s.nextID("h")

		err := s.uow.Do(ctx, func(ctx context.Context) error {
			return s.uow.Do(ctx, func(ctx context.Context) error {
				return s.households.Create(ctx, &household.Household{
					ID: casaID, Name: "Casa aninhada", CreatedAt: now(), UpdatedAt: now(),
				})
			})
		})
		require.NoError(t, err)

		_, err = s.households.ByID(ctx, casaID)
		assert.NoError(t, err)
	})
}

func TestAuditRepository(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)
		acao := audit.ActionRefreshReuseDetected

		require.NoError(t, s.audit.Create(ctx, &audit.Entry{
			ID:        s.nextID("a"),
			UserID:    &u.ID,
			Action:    acao,
			Entity:    audit.EntitySession,
			IP:        "2001:db8::1",
			CreatedAt: now(),
		}))

		lista, err := s.audit.ListByAction(ctx, acao, 10)
		require.NoError(t, err)
		require.NotEmpty(t, lista)
		assert.Equal(t, acao, lista[0].Action)
		assert.Equal(t, "2001:db8::1", lista[0].IP, "a coluna precisa comportar IPv6")

		n, err := s.audit.DeleteOlderThan(ctx, now().Add(time.Hour))
		require.NoError(t, err)
		assert.GreaterOrEqual(t, n, int64(1))
	})
}
