package gormstore_test

import (
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserRepositoryCRUD(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, false)

		porID, err := s.users.ByID(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, u.Email, porID.Email)
		assert.False(t, porID.Verified())

		porEmail, err := s.users.ByEmail(ctx, u.Email)
		require.NoError(t, err)
		assert.Equal(t, u.ID, porEmail.ID)

		_, err = s.users.ByID(ctx, "nao-existe")
		assert.ErrorIs(t, err, user.ErrNotFound)

		_, err = s.users.ByEmail(ctx, "ninguem@exemplo.test")
		assert.ErrorIs(t, err, user.ErrNotFound)
	})
}

func TestUserRepositoryEmailEhUnico(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, false)

		err := s.users.Create(ctx, &user.User{
			ID:           s.nextID("u"),
			Email:        u.Email,
			PasswordHash: u.PasswordHash,
			Name:         "Outro",
			CreatedAt:    now(),
			UpdatedAt:    now(),
		})
		assert.ErrorIs(t, err, user.ErrEmailTaken)
	})
}

// ActivatePending é o UPDATE que a confirmação de e-mail dispara: grava as
// credenciais DA TENTATIVA que recebeu o código e a data de verificação de
// uma vez só.
func TestUserRepositoryActivatePending(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, false)

		instante := now().Add(time.Minute)
		ok, err := s.users.ActivatePending(ctx, u.ID, "Novo Nome", "hash-novo", instante)
		require.NoError(t, err)
		require.True(t, ok)

		depois, err := s.users.ByID(ctx, u.ID)
		require.NoError(t, err)
		require.True(t, depois.Verified())
		assert.Equal(t, "Novo Nome", depois.Name)
		assert.Equal(t, "hash-novo", depois.PasswordHash)
		assert.WithinDuration(t, instante, *depois.EmailVerifiedAt, time.Second)

		// Idempotente: a segunda chamada não muda nada, nem a data original.
		ok, err = s.users.ActivatePending(ctx, u.ID, "Outro", "outro-hash", instante.Add(time.Hour))
		require.NoError(t, err)
		assert.False(t, ok)

		outra, err := s.users.ByID(ctx, u.ID)
		require.NoError(t, err)
		assert.WithinDuration(t, instante, *outra.EmailVerifiedAt, time.Second)
		assert.Equal(t, "Novo Nome", outra.Name)
	})
}

// Defesa central: ativar credenciais só vale para conta PENDENTE. Se valesse
// para conta verificada, um "registro" repetido trocaria a senha da vítima —
// tomada de conta direta.
func TestUserRepositoryActivatePendingNaoTocaContaVerificada(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()

		verificado := s.makeUser(t, ctx, true)
		hashOriginal := verificado.PasswordHash

		ok, err := s.users.ActivatePending(ctx, verificado.ID, "Invasor", "hash-do-invasor", now())
		require.NoError(t, err)
		assert.False(t, ok, "conta verificada não pode ser sobrescrita pelo cadastro")

		intacto, err := s.users.ByID(ctx, verificado.ID)
		require.NoError(t, err)
		assert.Equal(t, hashOriginal, intacto.PasswordHash)
		assert.NotEqual(t, "Invasor", intacto.Name)
	})
}

func TestUserRepositoryUpdatePassword(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)

		require.NoError(t, s.users.UpdatePassword(ctx, u.ID, "hash-redefinido", now()))
		depois, err := s.users.ByID(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, "hash-redefinido", depois.PasswordHash)

		err = s.users.UpdatePassword(ctx, "nao-existe", "x", now())
		assert.ErrorIs(t, err, user.ErrNotFound)
	})
}
