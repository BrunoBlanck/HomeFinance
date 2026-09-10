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

func (s *store) makeFamily(t *testing.T, ctx context.Context, userID string) *auth.RefreshFamily {
	t.Helper()

	fam := &auth.RefreshFamily{
		ID:        s.nextID("fam"),
		UserID:    userID,
		CreatedAt: now(),
		UpdatedAt: now(),
	}
	require.NoError(t, s.refresh.CreateFamily(ctx, fam))
	return fam
}

func (s *store) makeRefresh(t *testing.T, ctx context.Context, userID, familyID string, expires time.Time) *auth.RefreshToken {
	t.Helper()

	tok := &auth.RefreshToken{
		ID:        s.nextID("rt"),
		UserID:    userID,
		FamilyID:  familyID,
		TokenHash: s.nextID("hash"),
		ExpiresAt: expires,
		CreatedAt: now(),
		UpdatedAt: now(),
	}
	require.NoError(t, s.refresh.Create(ctx, tok))
	return tok
}

func TestRefreshTokenRepositoryBusca(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)
		tok := s.makeRefresh(t, ctx, u.ID, s.nextID("fam"), now().Add(time.Hour))

		encontrado, err := s.refresh.ByHash(ctx, tok.TokenHash)
		require.NoError(t, err)
		assert.Equal(t, tok.ID, encontrado.ID)
		assert.True(t, encontrado.Active(now()))

		_, err = s.refresh.ByHash(ctx, "hash-que-nao-existe")
		assert.ErrorIs(t, err, auth.ErrNotFound)
	})
}

// Risco 5 da §9: a rotação precisa ser atômica. Este teste é a prova de que
// duas rotações do MESMO token não podem ambas ter sucesso.
func TestRefreshTokenRepositoryRotacaoEhAtomica(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)
		fam := s.nextID("fam")
		tok := s.makeRefresh(t, ctx, u.ID, fam, now().Add(time.Hour))

		primeiro, err := s.refresh.Rotate(ctx, tok.ID, "sucessor-1", now())
		require.NoError(t, err)
		assert.True(t, primeiro)

		segundo, err := s.refresh.Rotate(ctx, tok.ID, "sucessor-2", now())
		require.NoError(t, err)
		assert.False(t, segundo, "a segunda rotação do mesmo token é REÚSO")

		depois, err := s.refresh.ByHash(ctx, tok.TokenHash)
		require.NoError(t, err)
		require.NotNil(t, depois.RevokedAt)
		require.NotNil(t, depois.ReplacedBy)
		assert.Equal(t, "sucessor-1", *depois.ReplacedBy)
		assert.False(t, depois.Active(now()))
	})
}

func TestRefreshTokenRepositoryRotacaoConcorrente(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)
		tok := s.makeRefresh(t, ctx, u.ID, s.nextID("fam"), now().Add(time.Hour))

		const n = 8
		var (
			wg       sync.WaitGroup
			mu       sync.Mutex
			sucessos int
		)
		for range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ok, err := s.refresh.Rotate(ctx, tok.ID, s.nextID("suc"), now())
				if err != nil {
					return
				}
				if ok {
					mu.Lock()
					sucessos++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()

		assert.Equal(t, 1, sucessos, "exatamente uma rotação pode vencer")
	})
}

func TestRefreshTokenRepositoryRevogacoes(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)
		famA := s.nextID("fam")
		famB := s.nextID("fam")

		a1 := s.makeRefresh(t, ctx, u.ID, famA, now().Add(time.Hour))
		a2 := s.makeRefresh(t, ctx, u.ID, famA, now().Add(time.Hour))
		b1 := s.makeRefresh(t, ctx, u.ID, famB, now().Add(time.Hour))

		n, err := s.refresh.RevokeFamily(ctx, famA, now())
		require.NoError(t, err)
		assert.Equal(t, int64(2), n)

		for _, tok := range []*auth.RefreshToken{a1, a2} {
			depois, err := s.refresh.ByHash(ctx, tok.TokenHash)
			require.NoError(t, err)
			assert.NotNil(t, depois.RevokedAt)
		}
		intacto, err := s.refresh.ByHash(ctx, b1.TokenHash)
		require.NoError(t, err)
		assert.Nil(t, intacto.RevokedAt, "a outra família não pode ser afetada")

		// Troca de senha derruba TODAS as famílias (docs/SEGURANCA.md §1.1).
		n, err = s.refresh.RevokeAllForUser(ctx, u.ID, now())
		require.NoError(t, err)
		assert.Equal(t, int64(1), n)

		derrubado, err := s.refresh.ByHash(ctx, b1.TokenHash)
		require.NoError(t, err)
		assert.NotNil(t, derrubado.RevokedAt)
	})
}

// A revogação precisa alcançar o elo que NÃO EXISTIA quando ela rodou —
// é o achado ALTA-1. A prova no nível do repositório: revogar a família e
// só DEPOIS inserir um token nela; o token nasce limpo (o UPDATE não tem
// como alcançá-lo), mas o estado da FAMÍLIA continua dizendo "morta".
func TestRefreshTokenRepositoryFamiliaRevogadaAlcancaEloPosterior(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)
		fam := s.makeFamily(t, ctx, u.ID)

		s.makeRefresh(t, ctx, u.ID, fam.ID, now().Add(time.Hour))

		_, err := s.refresh.RevokeFamily(ctx, fam.ID, now())
		require.NoError(t, err)

		// O sucessor de uma rotação em voo: inserido DEPOIS da revogação.
		retardatario := s.makeRefresh(t, ctx, u.ID, fam.ID, now().Add(time.Hour))

		vivo, err := s.refresh.ByHash(ctx, retardatario.TokenHash)
		require.NoError(t, err)
		require.Nil(t, vivo.RevokedAt, "o UPDATE de revogação não alcança linha futura — daí a família")

		depois, err := s.refresh.FamilyByID(ctx, fam.ID)
		require.NoError(t, err)
		require.NotNil(t, depois.RevokedAt, "a família tem de permanecer revogada")
		assert.False(t, depois.Active())
	})
}

func TestRefreshTokenRepositoryRevogacaoDeFamiliaEhIdempotente(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)
		fam := s.makeFamily(t, ctx, u.ID)
		s.makeRefresh(t, ctx, u.ID, fam.ID, now().Add(time.Hour))

		primeira := now()
		_, err := s.refresh.RevokeFamily(ctx, fam.ID, primeira)
		require.NoError(t, err)

		antes, err := s.refresh.FamilyByID(ctx, fam.ID)
		require.NoError(t, err)
		require.NotNil(t, antes.RevokedAt)

		// Uma segunda varredura não pode reescrever a hora da morte: é o
		// dado forense do incidente.
		_, err = s.refresh.RevokeFamily(ctx, fam.ID, primeira.Add(time.Hour))
		require.NoError(t, err)

		depois, err := s.refresh.FamilyByID(ctx, fam.ID)
		require.NoError(t, err)
		require.NotNil(t, depois.RevokedAt)
		assert.WithinDuration(t, *antes.RevokedAt, *depois.RevokedAt, time.Second)
	})
}

// Troca de senha derruba TODAS as famílias, não só os tokens.
func TestRefreshTokenRepositoryRevokeAllForUserDerrubaFamilias(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)
		famA := s.makeFamily(t, ctx, u.ID)
		famB := s.makeFamily(t, ctx, u.ID)

		outro := s.makeUser(t, ctx, true)
		famOutro := s.makeFamily(t, ctx, outro.ID)

		_, err := s.refresh.RevokeAllForUser(ctx, u.ID, now())
		require.NoError(t, err)

		for _, f := range []*auth.RefreshFamily{famA, famB} {
			depois, err := s.refresh.FamilyByID(ctx, f.ID)
			require.NoError(t, err)
			assert.NotNil(t, depois.RevokedAt)
		}

		intacta, err := s.refresh.FamilyByID(ctx, famOutro.ID)
		require.NoError(t, err)
		assert.Nil(t, intacta.RevokedAt, "a sessão de outro usuário não pode cair junto")
	})
}

func TestRefreshTokenRepositoryFamiliaInexistente(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		_, err := s.refresh.FamilyByID(t.Context(), "familia-que-nao-existe")
		assert.ErrorIs(t, err, auth.ErrNotFound)
	})
}

func TestRefreshTokenRepositoryExpurgoDeFamiliasMortas(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)

		comToken := s.makeFamily(t, ctx, u.ID)
		s.makeRefresh(t, ctx, u.ID, comToken.ID, now().Add(24*time.Hour))

		semToken := s.makeFamily(t, ctx, u.ID)

		n, err := s.refresh.DeleteDeadFamilies(ctx, now().Add(time.Hour))
		require.NoError(t, err)
		assert.GreaterOrEqual(t, n, int64(1))

		_, err = s.refresh.FamilyByID(ctx, semToken.ID)
		assert.ErrorIs(t, err, auth.ErrNotFound, "família sem nenhum elo é sessão morta")

		_, err = s.refresh.FamilyByID(ctx, comToken.ID)
		assert.NoError(t, err, "família com token vivo não pode ser expurgada")
	})
}

func TestRefreshTokenRepositoryExpurgo(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		u := s.makeUser(t, ctx, true)

		vivo := s.makeRefresh(t, ctx, u.ID, s.nextID("fam"), now().Add(24*time.Hour))
		expirado := s.makeRefresh(t, ctx, u.ID, s.nextID("fam"), now().Add(-24*time.Hour))

		n, err := s.refresh.DeleteExpired(ctx, now())
		require.NoError(t, err)
		assert.GreaterOrEqual(t, n, int64(1))

		_, err = s.refresh.ByHash(ctx, expirado.TokenHash)
		assert.ErrorIs(t, err, auth.ErrNotFound)

		_, err = s.refresh.ByHash(ctx, vivo.TokenHash)
		assert.NoError(t, err)
	})
}
