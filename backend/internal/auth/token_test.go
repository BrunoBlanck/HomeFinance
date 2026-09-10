package auth_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Placeholders de teste, não são segredos reais.
const (
	segredoDeTeste = "segredo-de-teste-0123456789abcdefghij"
	outroSegredo   = "outro-segredo-de-teste-0123456789abcd"
)

func newTokens(t *testing.T, clock auth.Clock) *auth.TokenService {
	t.Helper()
	svc, err := auth.NewTokenService(auth.TokenOptions{
		Secret:     segredoDeTeste,
		Issuer:     "homefinance",
		Audience:   "homefinance-api",
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 14 * 24 * time.Hour,
		Clock:      clock,
	})
	require.NoError(t, err)
	return svc
}

func identidade() session.Identity {
	return session.Identity{UserID: "u-1", HouseholdID: "h-1", Role: session.RoleOwner, SessionID: "fam-1"}
}

func TestNewTokenServiceValidaEntrada(t *testing.T) {
	t.Parallel()

	_, err := auth.NewTokenService(auth.TokenOptions{Secret: "curto", Issuer: "x", Audience: "y"})
	assert.Error(t, err, "segredo abaixo de 256 bits é recusado")

	_, err = auth.NewTokenService(auth.TokenOptions{Secret: segredoDeTeste, Audience: "y"})
	assert.Error(t, err, "emissor é obrigatório")

	_, err = auth.NewTokenService(auth.TokenOptions{Secret: segredoDeTeste, Issuer: "x"})
	assert.Error(t, err, "audiência é obrigatória")
}

func TestAccessTokenIdaEVolta(t *testing.T) {
	t.Parallel()

	svc := newTokens(t, nil)
	raw, exp, err := svc.IssueAccessToken(identidade())
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().UTC().Add(15*time.Minute), exp, time.Minute)

	got, err := svc.ParseAccessToken(raw)
	require.NoError(t, err)
	assert.Equal(t, identidade(), got)
}

func TestIssueAccessTokenExigeIdentidadeCompleta(t *testing.T) {
	t.Parallel()

	svc := newTokens(t, nil)
	_, _, err := svc.IssueAccessToken(session.Identity{UserID: "u-1"})
	assert.Error(t, err)
}

// Critério de aceite 18 e risco 1 da §9.
func TestParseAccessTokenRejeitaTokenAdulterado(t *testing.T) {
	t.Parallel()

	agora := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return agora }
	svc := newTokens(t, clock)

	valido, _, err := svc.IssueAccessToken(identidade())
	require.NoError(t, err)

	t.Run("alg none", func(t *testing.T) {
		tok := jwt.NewWithClaims(jwt.SigningMethodNone, auth.Claims{
			HouseholdID: "h-1", Role: "owner", SessionID: "fam-1",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer: "homefinance", Subject: "u-1",
				Audience:  jwt.ClaimStrings{"homefinance-api"},
				ExpiresAt: jwt.NewNumericDate(agora.Add(time.Hour)),
			},
		})
		raw, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)

		_, err = svc.ParseAccessToken(raw)
		assert.ErrorIs(t, err, auth.ErrInvalidSession, "alg=none precisa ser recusado")
	})

	t.Run("assinado com outra chave", func(t *testing.T) {
		outro, err := auth.NewTokenService(auth.TokenOptions{
			Secret: outroSegredo, Issuer: "homefinance", Audience: "homefinance-api",
			AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour, Clock: clock,
		})
		require.NoError(t, err)

		raw, _, err := outro.IssueAccessToken(identidade())
		require.NoError(t, err)

		_, err = svc.ParseAccessToken(raw)
		assert.ErrorIs(t, err, auth.ErrInvalidSession)
	})

	t.Run("emissor errado", func(t *testing.T) {
		outro, err := auth.NewTokenService(auth.TokenOptions{
			Secret: segredoDeTeste, Issuer: "outro-emissor", Audience: "homefinance-api",
			AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour, Clock: clock,
		})
		require.NoError(t, err)
		raw, _, err := outro.IssueAccessToken(identidade())
		require.NoError(t, err)

		_, err = svc.ParseAccessToken(raw)
		assert.ErrorIs(t, err, auth.ErrInvalidSession)
	})

	t.Run("audiencia errada", func(t *testing.T) {
		outro, err := auth.NewTokenService(auth.TokenOptions{
			Secret: segredoDeTeste, Issuer: "homefinance", Audience: "outra-api",
			AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour, Clock: clock,
		})
		require.NoError(t, err)
		raw, _, err := outro.IssueAccessToken(identidade())
		require.NoError(t, err)

		_, err = svc.ParseAccessToken(raw)
		assert.ErrorIs(t, err, auth.ErrInvalidSession)
	})

	t.Run("expirado", func(t *testing.T) {
		futuro := func() time.Time { return agora.Add(time.Hour) }
		svcFuturo := newTokens(t, futuro)
		_, err := svcFuturo.ParseAccessToken(valido)
		assert.ErrorIs(t, err, auth.ErrInvalidSession)
	})

	t.Run("claim adulterada", func(t *testing.T) {
		partes := strings.Split(valido, ".")
		require.Len(t, partes, 3)

		payload, err := base64.RawURLEncoding.DecodeString(partes[1])
		require.NoError(t, err)
		adulterado := strings.Replace(string(payload), `"hid":"h-1"`, `"hid":"h-9"`, 1)
		require.NotEqual(t, string(payload), adulterado, "o payload precisa ter mudado")

		forjado := partes[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(adulterado)) + "." + partes[2]
		_, err = svcFuturo(t).ParseAccessToken(forjado)
		assert.ErrorIs(t, err, auth.ErrInvalidSession, "assinatura não confere depois da adulteração")
	})

	t.Run("vazio e lixo", func(t *testing.T) {
		for _, raw := range []string{"", "abc", "a.b.c", valido + "x"} {
			_, err := svc.ParseAccessToken(raw)
			assert.ErrorIs(t, err, auth.ErrInvalidSession)
		}
	})

	t.Run("papel desconhecido", func(t *testing.T) {
		ident := identidade()
		ident.Role = "superadmin"
		raw, _, err := svc.IssueAccessToken(ident)
		require.NoError(t, err)

		_, err = svc.ParseAccessToken(raw)
		assert.ErrorIs(t, err, auth.ErrInvalidSession, "papel fora da allowlist não autentica")
	})
}

// helper: mesmo serviço, relógio no instante da emissão.
func svcFuturo(t *testing.T) *auth.TokenService {
	t.Helper()
	agora := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	return newTokens(t, func() time.Time { return agora })
}

// docs/SEGURANCA.md §1: refresh é opaco, com pelo menos 128 bits, guardado só
// como hash.
func TestRefreshTokenEhOpacoEForte(t *testing.T) {
	t.Parallel()

	svc := newTokens(t, nil)

	vistos := map[string]struct{}{}
	for range 200 {
		plain, hash, err := svc.NewRefreshToken()
		require.NoError(t, err)

		raw, err := base64.RawURLEncoding.DecodeString(plain)
		require.NoError(t, err)
		assert.Len(t, raw, auth.RefreshTokenBytes)
		assert.GreaterOrEqual(t, len(raw)*8, 128)

		// Não é JWT: não tem ponto nem estrutura.
		assert.NotContains(t, plain, ".")

		assert.Equal(t, auth.HashRefreshToken(plain), hash)
		assert.Len(t, hash, 64)
		assert.NotContains(t, hash, plain)

		_, repetido := vistos[plain]
		assert.False(t, repetido, "token repetido")
		vistos[plain] = struct{}{}
	}
}

func TestHashRefreshTokenEhDeterministico(t *testing.T) {
	t.Parallel()

	a := auth.HashRefreshToken("token-qualquer")
	b := auth.HashRefreshToken("token-qualquer")
	assert.Equal(t, a, b)
	assert.NotEqual(t, a, auth.HashRefreshToken("token-diferente"))
}
