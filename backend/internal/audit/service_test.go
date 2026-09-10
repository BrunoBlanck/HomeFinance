package audit_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/id"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeRepo struct {
	entries []audit.Entry
	err     error
	deleted int64
}

func (f *fakeRepo) Create(_ context.Context, e *audit.Entry) error {
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, *e)
	return nil
}

func (f *fakeRepo) DeleteOlderThan(context.Context, time.Time) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.deleted, nil
}

func TestRecordPreencheOsCampos(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{}
	instante := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	svc := audit.NewService(repo, logging.Discard(),
		audit.WithIDs(id.Fixed("a-1")),
		audit.WithClock(func() time.Time { return instante }),
	)

	require.NoError(t, svc.Record(t.Context(), audit.Params{
		Action:      audit.ActionLogin,
		Entity:      audit.EntitySession,
		EntityID:    "s-1",
		UserID:      "u-1",
		HouseholdID: "h-1",
		IP:          "203.0.113.7",
	}))

	require.Len(t, repo.entries, 1)
	e := repo.entries[0]
	assert.Equal(t, "a-1", e.ID)
	assert.Equal(t, audit.ActionLogin, e.Action)
	assert.Equal(t, instante, e.CreatedAt)
	require.NotNil(t, e.UserID)
	assert.Equal(t, "u-1", *e.UserID)
	require.NotNil(t, e.HouseholdID)
	assert.Equal(t, "h-1", *e.HouseholdID)
	require.NotNil(t, e.EntityID)
	assert.Equal(t, "s-1", *e.EntityID)
}

func TestRecordDeixaPonteirosNulosQuandoVazio(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{}
	svc := audit.NewService(repo, logging.Discard(), audit.WithIDs(id.Fixed("a-1")))

	require.NoError(t, svc.Record(t.Context(), audit.Params{
		Action: audit.ActionLoginFailed,
		Entity: audit.EntityUser,
		IP:     "203.0.113.7",
	}))

	e := repo.entries[0]
	assert.Nil(t, e.UserID, "login com e-mail inexistente não tem usuário")
	assert.Nil(t, e.HouseholdID)
	assert.Nil(t, e.EntityID)
}

// Auditoria indisponível não pode virar negação de serviço de autenticação.
func TestTryRecordEngoleFalhaELoga(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	lg := logging.New(&buf, logging.Options{Level: "debug", Format: "json"})

	repo := &fakeRepo{err: errors.New("tabela indisponível")}
	svc := audit.NewService(repo, lg)

	assert.NotPanics(t, func() {
		svc.TryRecord(t.Context(), audit.Params{Action: audit.ActionLogin, Entity: audit.EntitySession, IP: "1.2.3.4"})
	})
	assert.Contains(t, buf.String(), "falha ao gravar auditoria")
}

func TestPurgeOlderThan(t *testing.T) {
	t.Parallel()

	repo := &fakeRepo{deleted: 7}
	svc := audit.NewService(repo, logging.Discard())

	n, err := svc.PurgeOlderThan(t.Context(), time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(7), n)
}

// Critério de aceite 19 depende do valor exato desta constante.
func TestNomeDaAcaoDeReuso(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "auth.refresh_reuse_detected", audit.ActionRefreshReuseDetected)
}
