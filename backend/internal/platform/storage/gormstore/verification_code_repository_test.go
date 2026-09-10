package gormstore_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func (s *store) makeCode(t *testing.T, ctx context.Context, email, purpose string, expires time.Time) *auth.VerificationCode {
	t.Helper()

	c := &auth.VerificationCode{
		ID:        s.nextID("vc"),
		Email:     email,
		Purpose:   purpose,
		CodeHash:  s.nextID("codehash"),
		ExpiresAt: expires,
		CreatedAt: now(),
		UpdatedAt: now(),
	}
	require.NoError(t, s.codes.Create(ctx, c))
	return c
}

func TestVerificationCodeAtivoPorEmailEProposito(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()

		vivo := s.makeCode(t, ctx, email, auth.PurposeEmailVerification, now().Add(15*time.Minute))
		s.makeCode(t, ctx, email, auth.PurposePasswordReset, now().Add(15*time.Minute))

		achado, err := s.codes.ActiveByEmailPurpose(ctx, email, auth.PurposeEmailVerification, now())
		require.NoError(t, err)
		assert.Equal(t, vivo.ID, achado.ID)

		// Propósito diferente não enxerga o código do outro fluxo
		// (docs/SEGURANCA.md §1.1).
		outro, err := s.codes.ActiveByEmailPurpose(ctx, email, auth.PurposePasswordReset, now())
		require.NoError(t, err)
		assert.NotEqual(t, vivo.ID, outro.ID)

		// E-mail sem código: mesma consulta, mesmo erro que e-mail
		// inexistente (grupo D da §3.12).
		_, err = s.codes.ActiveByEmailPurpose(ctx, "ninguem@exemplo.test", auth.PurposeEmailVerification, now())
		assert.ErrorIs(t, err, auth.ErrNotFound)
	})
}

func TestVerificationCodeExpiradoNaoEhAtivo(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()
		s.makeCode(t, ctx, email, auth.PurposeEmailVerification, now().Add(-time.Minute))

		_, err := s.codes.ActiveByEmailPurpose(ctx, email, auth.PurposeEmailVerification, now())
		assert.ErrorIs(t, err, auth.ErrNotFound)
	})
}

// docs/SEGURANCA.md §1.1: emitir código novo invalida todos os anteriores.
func TestVerificationCodeConsumeAllActive(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()

		s.makeCode(t, ctx, email, auth.PurposeEmailVerification, now().Add(15*time.Minute))
		s.makeCode(t, ctx, email, auth.PurposeEmailVerification, now().Add(15*time.Minute))
		sobrevivente := s.makeCode(t, ctx, email, auth.PurposePasswordReset, now().Add(15*time.Minute))

		n, err := s.codes.ConsumeAllActive(ctx, email, auth.PurposeEmailVerification, now())
		require.NoError(t, err)
		assert.Equal(t, int64(2), n)

		_, err = s.codes.ActiveByEmailPurpose(ctx, email, auth.PurposeEmailVerification, now())
		assert.ErrorIs(t, err, auth.ErrNotFound)

		vivo, err := s.codes.ActiveByEmailPurpose(ctx, email, auth.PurposePasswordReset, now())
		require.NoError(t, err)
		assert.Equal(t, sobrevivente.ID, vivo.ID)
	})
}

// Risco 3 da §9: sem incremento ATÔMICO no banco, N tentativas simultâneas
// contam como uma só e o limite de 5 vira decoração.
func TestVerificationCodeIncrementoDeTentativasEhAtomico(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()
		c := s.makeCode(t, ctx, email, auth.PurposeEmailVerification, now().Add(15*time.Minute))

		const n = 10
		var wg sync.WaitGroup
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _, _ = s.codes.IncrementAttempts(ctx, c.ID)
			}()
		}
		wg.Wait()

		atual, contou, err := s.codes.IncrementAttempts(ctx, c.ID)
		require.NoError(t, err)
		require.True(t, contou)
		assert.Equal(t, n+1, atual, "cada tentativa concorrente precisa contar")
	})
}

func TestVerificationCodeConsumoEhUnico(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()
		c := s.makeCode(t, ctx, email, auth.PurposeEmailVerification, now().Add(15*time.Minute))

		primeiro, err := s.codes.Consume(ctx, c.ID, now())
		require.NoError(t, err)
		assert.True(t, primeiro)

		segundo, err := s.codes.Consume(ctx, c.ID, now())
		require.NoError(t, err)
		assert.False(t, segundo, "uso único: só a primeira validação consome")

		// Depois de consumido, o contador não avança mais.
		_, contou, err := s.codes.IncrementAttempts(ctx, c.ID)
		require.NoError(t, err)
		assert.False(t, contou)
	})
}

func TestVerificationCodeLastIssuedAt(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()

		_, ok, err := s.codes.LastIssuedAt(ctx, email, auth.PurposeEmailVerification)
		require.NoError(t, err)
		assert.False(t, ok)

		s.makeCode(t, ctx, email, auth.PurposeEmailVerification, now().Add(15*time.Minute))

		quando, ok, err := s.codes.LastIssuedAt(ctx, email, auth.PurposeEmailVerification)
		require.NoError(t, err)
		require.True(t, ok)
		assert.WithinDuration(t, now(), quando, 5*time.Second)
	})
}

func TestVerificationCodeExpurgo(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		email := s.nextEmail()

		s.makeCode(t, ctx, email, auth.PurposeEmailVerification, now().Add(-time.Hour))
		vivo := s.makeCode(t, ctx, email, auth.PurposePasswordReset, now().Add(time.Hour))

		n, err := s.codes.DeleteExpired(ctx, now())
		require.NoError(t, err)
		assert.GreaterOrEqual(t, n, int64(1))

		restante, err := s.codes.ActiveByEmailPurpose(ctx, email, auth.PurposePasswordReset, now())
		require.NoError(t, err)
		assert.Equal(t, vivo.ID, restante.ID)
	})
}
