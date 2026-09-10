package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/id"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/golang-jwt/jwt/v5"
)

// RefreshTokenBytes é a entropia do token opaco: 256 bits, bem acima dos 128
// exigidos por docs/SEGURANCA.md §1.
const RefreshTokenBytes = 32

// Claims do access token. Além das registradas, carregamos hid (casa ativa),
// role e sid (família da sessão).
type Claims struct {
	HouseholdID string `json:"hid"`
	Role        string `json:"role"`
	SessionID   string `json:"sid"`
	jwt.RegisteredClaims
}

// TokenService emite e valida o access token (JWT HS256) e o refresh opaco.
//
// docs/SEGURANCA.md §1 e risco 1 da §9 da spec 0001: o validador PINA o
// algoritmo, exige emissor, audiência e expiração. Sem WithValidMethods, um
// token com alg "none" — ou assinado com HMAC usando a chave pública, no dia
// em que houver RS256 — passaria.
type TokenService struct {
	secret     []byte
	issuer     string
	audience   string
	accessTTL  time.Duration
	refreshTTL time.Duration
	clock      Clock
	ids        id.Generator
	parser     *jwt.Parser
}

// TokenOptions configura o TokenService.
type TokenOptions struct {
	Secret     string
	Issuer     string
	Audience   string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	Clock      Clock
	IDs        id.Generator
}

// NewTokenService monta o serviço de tokens.
func NewTokenService(opts TokenOptions) (*TokenService, error) {
	if len(opts.Secret) < 32 {
		return nil, fmt.Errorf("segredo do JWT precisa de pelo menos 32 bytes")
	}
	if opts.Issuer == "" || opts.Audience == "" {
		return nil, fmt.Errorf("emissor e audiência do JWT são obrigatórios")
	}
	if opts.Clock == nil {
		opts.Clock = func() time.Time { return time.Now().UTC() }
	}
	if opts.IDs == nil {
		opts.IDs = id.New
	}

	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(opts.Issuer),
		jwt.WithAudience(opts.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(func() time.Time { return opts.Clock() }),
		jwt.WithLeeway(0),
	)

	return &TokenService{
		secret:     []byte(opts.Secret),
		issuer:     opts.Issuer,
		audience:   opts.Audience,
		accessTTL:  opts.AccessTTL,
		refreshTTL: opts.RefreshTTL,
		clock:      opts.Clock,
		ids:        opts.IDs,
		parser:     parser,
	}, nil
}

// AccessTTL devolve a validade do access token.
func (s *TokenService) AccessTTL() time.Duration { return s.accessTTL }

// RefreshTTL devolve a validade do refresh token.
func (s *TokenService) RefreshTTL() time.Duration { return s.refreshTTL }

// IssueAccessToken assina o JWT da identidade.
func (s *TokenService) IssueAccessToken(ident session.Identity) (string, time.Time, error) {
	if !ident.Valid() {
		return "", time.Time{}, fmt.Errorf("identidade incompleta para emitir token")
	}
	now := s.clock()
	exp := now.Add(s.accessTTL)

	claims := Claims{
		HouseholdID: ident.HouseholdID,
		Role:        ident.Role,
		SessionID:   ident.SessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.issuer,
			Subject:   ident.UserID,
			Audience:  jwt.ClaimStrings{s.audience},
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ID:        s.ids(),
		},
	}

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("assinando access token: %w", err)
	}
	return signed, exp, nil
}

// ParseAccessToken valida a assinatura e todas as claims registradas.
func (s *TokenService) ParseAccessToken(raw string) (session.Identity, error) {
	if raw == "" {
		return session.Identity{}, ErrInvalidSession
	}

	var claims Claims
	_, err := s.parser.ParseWithClaims(raw, &claims, func(t *jwt.Token) (any, error) {
		// Redundante com WithValidMethods, e proposital: se alguém remover a
		// opção do parser um dia, esta checagem ainda barra a troca de
		// algoritmo.
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("algoritmo inesperado")
		}
		return s.secret, nil
	})
	if err != nil {
		return session.Identity{}, ErrInvalidSession
	}

	ident := session.Identity{
		UserID:      claims.Subject,
		HouseholdID: claims.HouseholdID,
		Role:        claims.Role,
		SessionID:   claims.SessionID,
	}
	if !ident.Valid() || !validRole(ident.Role) {
		return session.Identity{}, ErrInvalidSession
	}
	return ident, nil
}

func validRole(role string) bool {
	return role == session.RoleOwner || role == session.RoleMember
}

// NewRefreshToken sorteia o token opaco e devolve (valor em claro, hash).
//
// O valor em claro só existe no cookie do cliente e nesta chamada; o banco
// guarda apenas o SHA-256. Um dump da tabela não permite forjar sessão.
// SHA-256 puro basta aqui (D6): com 256 bits de entropia não há espaço de
// busca para força bruta, então um segredo a mais não compraria nada.
func (s *TokenService) NewRefreshToken() (plain, hash string, err error) {
	buf := make([]byte, RefreshTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("sorteando refresh token: %w", err)
	}
	plain = base64.RawURLEncoding.EncodeToString(buf)
	return plain, HashRefreshToken(plain), nil
}

// HashRefreshToken devolve o SHA-256 hex do token apresentado.
func HashRefreshToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
