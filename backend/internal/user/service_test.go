package user_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Duplos de teste
// ---------------------------------------------------------------------------

type fakeUsers struct {
	items map[string]user.User
	err   error
}

func newFakeUsers() *fakeUsers { return &fakeUsers{items: map[string]user.User{}} }

func (f *fakeUsers) Create(_ context.Context, u *user.User) error {
	f.items[u.ID] = *u
	return nil
}

func (f *fakeUsers) ByID(_ context.Context, id string) (*user.User, error) {
	if f.err != nil {
		return nil, f.err
	}
	u, ok := f.items[id]
	if !ok {
		return nil, user.ErrNotFound
	}
	return &u, nil
}

func (f *fakeUsers) ByEmail(_ context.Context, email string) (*user.User, error) {
	for _, u := range f.items {
		if u.Email == email {
			out := u
			return &out, nil
		}
	}
	return nil, user.ErrNotFound
}

func (f *fakeUsers) ActivatePending(context.Context, string, string, string, time.Time) (bool, error) {
	return true, nil
}

func (f *fakeUsers) UpdatePassword(context.Context, string, string, time.Time) error { return nil }

type fakeHouseholds struct {
	list       []household.Summary
	membership map[string]household.Summary
	err        error
}

func (f *fakeHouseholds) ListForUser(context.Context, string) ([]household.Summary, error) {
	return f.list, f.err
}

func (f *fakeHouseholds) MembershipOf(_ context.Context, userID, hid string) (household.Summary, error) {
	if s, ok := f.membership[userID+"|"+hid]; ok {
		return s, nil
	}
	return household.Summary{}, household.ErrNotFound
}

type cookieSpy struct{ chamou bool }

func (c *cookieSpy) ClearSessionCookies(w http.ResponseWriter) {
	c.chamou = true
	http.SetCookie(w, &http.Cookie{Name: "hf_access", Value: "", MaxAge: -1, Path: "/"})
	http.SetCookie(w, &http.Cookie{Name: "hf_refresh", Value: "", MaxAge: -1, Path: "/"})
}

func setup(t *testing.T) (*user.Service, *fakeUsers, *fakeHouseholds) {
	t.Helper()

	users := newFakeUsers()
	verificado := time.Date(2026, 9, 9, 14, 3, 11, 987_000_000, time.UTC)
	users.items["u-1"] = user.User{
		ID:              "u-1",
		Email:           "bruno@exemplo.com",
		PasswordHash:    "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		Name:            "Bruno Blanck",
		EmailVerifiedAt: &verificado,
		CreatedAt:       time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC),
	}

	hh := &fakeHouseholds{
		list:       []household.Summary{{ID: "h-1", Name: "Casa de Bruno", Role: household.RoleOwner}},
		membership: map[string]household.Summary{"u-1|h-1": {ID: "h-1", Name: "Casa de Bruno", Role: household.RoleOwner}},
	}
	return user.NewService(users, hh), users, hh
}

func identity() session.Identity {
	return session.Identity{UserID: "u-1", HouseholdID: "h-1", Role: session.RoleOwner, SessionID: "s-1"}
}

// ---------------------------------------------------------------------------
// Testes
// ---------------------------------------------------------------------------

func TestMeMontaOCorpoDoContrato(t *testing.T) {
	t.Parallel()

	svc, _, _ := setup(t)
	view, err := svc.Me(t.Context(), identity())
	require.NoError(t, err)

	assert.Equal(t, "u-1", view.User.ID)
	assert.Equal(t, "Bruno Blanck", view.User.Name)
	assert.Equal(t, "bruno@exemplo.com", view.User.Email)
	require.NotNil(t, view.User.EmailVerifiedAt)
	// Formato fixo do contrato, sem fração de segundo.
	assert.Equal(t, "2026-09-09T14:03:11Z", *view.User.EmailVerifiedAt)
	assert.Equal(t, "2026-09-01T10:00:00Z", view.User.CreatedAt)
	assert.Equal(t, "h-1", view.Household.ID)
	assert.Equal(t, household.RoleOwner, view.Household.Role)
	require.Len(t, view.Households, 1)
}

// Risco 13 da §9: a entidade nunca é serializada — nenhum campo sensível pode
// escapar pelo DTO.
func TestMeNaoSerializaHashDeSenha(t *testing.T) {
	t.Parallel()

	svc, _, _ := setup(t)
	view, err := svc.Me(t.Context(), identity())
	require.NoError(t, err)

	raw, err := json.Marshal(view)
	require.NoError(t, err)
	corpo := string(raw)

	assert.NotContains(t, corpo, "argon2id")
	assert.NotContains(t, corpo, "passwordHash")
	assert.NotContains(t, corpo, "password_hash")
	assert.NotContains(t, corpo, "PasswordHash")
}

// Risco 2 da §9 e critério de aceite 24.
func TestMeFalhaQuandoOVinculoSumiu(t *testing.T) {
	t.Parallel()

	svc, _, hh := setup(t)
	hh.membership = map[string]household.Summary{}

	_, err := svc.Me(t.Context(), identity())
	assert.ErrorIs(t, err, household.ErrNotFound)
}

func TestMePropagaErroDeRepositorio(t *testing.T) {
	t.Parallel()

	svc, users, _ := setup(t)
	users.err = errors.New("banco fora do ar")

	_, err := svc.Me(t.Context(), identity())
	require.Error(t, err)
	assert.NotErrorIs(t, err, household.ErrNotFound)
}

func TestHandlerMeFeliz(t *testing.T) {
	t.Parallel()

	svc, _, _ := setup(t)
	spy := &cookieSpy{}
	h := user.NewHandler(svc, spy, logging.Discard())

	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	h.Me(rec, r.WithContext(session.NewContext(r.Context(), identity())))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, spy.chamou)

	var corpo map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
	assert.Contains(t, corpo, "user")
	assert.Contains(t, corpo, "household")
	assert.Contains(t, corpo, "households")
}

func TestHandlerMeSemIdentidade(t *testing.T) {
	t.Parallel()

	svc, _, _ := setup(t)
	h := user.NewHandler(svc, &cookieSpy{}, logging.Discard())

	rec := httptest.NewRecorder()
	h.Me(rec, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "UNAUTHENTICATED")
}

// Critério de aceite 24: token válido, vínculo apagado => 401 + cookies
// limpos.
func TestHandlerMeLimpaCookiesQuandoOVinculoSumiu(t *testing.T) {
	t.Parallel()

	svc, _, hh := setup(t)
	hh.membership = map[string]household.Summary{}

	spy := &cookieSpy{}
	h := user.NewHandler(svc, spy, logging.Discard())

	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	h.Me(rec, r.WithContext(session.NewContext(r.Context(), identity())))

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.True(t, spy.chamou, "a sessão perdeu o sentido: os cookies precisam cair")
	assert.Len(t, rec.Result().Cookies(), 2)
}

func TestHandlerMeErroInternoNaoVazaDetalhe(t *testing.T) {
	t.Parallel()

	svc, users, _ := setup(t)
	users.err = errors.New("pq: connection to 10.0.0.5 refused")

	h := user.NewHandler(svc, &cookieSpy{}, logging.Discard())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	rec := httptest.NewRecorder()
	h.Me(rec, r.WithContext(session.NewContext(r.Context(), identity())))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "10.0.0.5")
	assert.NotContains(t, rec.Body.String(), "connection")
}

func TestUserLogValueNaoVazaHash(t *testing.T) {
	t.Parallel()

	u := &user.User{ID: "u-1", Email: "bruno@exemplo.com", PasswordHash: "$argon2id$segredo"}
	v := u.LogValue()
	assert.NotContains(t, v.String(), "argon2id")
	assert.NotContains(t, v.String(), "bruno@exemplo.com")
	assert.Contains(t, v.String(), "u-1")

	var nilUser *user.User
	assert.Equal(t, slog.StringValue("<nil>"), nilUser.LogValue())
}
