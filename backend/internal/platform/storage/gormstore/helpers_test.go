package gormstore_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/require"
)

// backend descreve um banco onde a suíte de repositórios roda.
type backend struct {
	name   string
	driver string
	dsn    func(t *testing.T) string
}

var pgSchemaCounter atomic.Uint64

// backends devolve os bancos a exercitar.
//
// Critério de aceite 5 da spec 0001: SQLite sempre; PostgreSQL também quando
// TEST_POSTGRES_DSN estiver definido. É a prova mais barata de que o SQL
// gerado é portátil (ADR-008) sem trazer testcontainers agora.
func backends() []backend {
	out := []backend{{
		name:   "sqlite",
		driver: storage.DriverSQLite,
		dsn: func(t *testing.T) string {
			return filepath.Join(t.TempDir(), "homefinance-test.db")
		},
	}}

	if dsn := os.Getenv("TEST_POSTGRES_DSN"); dsn != "" {
		out = append(out, backend{
			name:   "postgres",
			driver: storage.DriverPostgres,
			dsn:    func(*testing.T) string { return dsn },
		})
	}
	return out
}

// store reúne tudo o que um teste de repositório precisa.
type store struct {
	db           *storage.DB
	users        *gormstore.UserRepository
	households   *gormstore.HouseholdRepository
	memberships  *gormstore.MembershipRepository
	refresh      *gormstore.RefreshTokenRepository
	codes        *gormstore.VerificationCodeRepository
	attempts     *gormstore.RegistrationAttemptRepository
	audit        *gormstore.AuditRepository
	uow          *gormstore.UnitOfWork
	seq          atomic.Uint64
	backendName  string
	backendIsPG  bool
	tablesSuffix string
}

func newStore(t *testing.T, b backend) *store {
	t.Helper()

	ctx := t.Context()
	db, err := storage.Open(ctx, storage.Options{
		Driver:       b.driver,
		DSN:          b.dsn(t),
		MaxOpenConns: 1, // SQLite em arquivo: serializa e evita "database is locked"
		MaxIdleConns: 1,
	}, logging.Discard())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))

	s := &store{
		db:          db,
		users:       gormstore.NewUserRepository(db),
		households:  gormstore.NewHouseholdRepository(db),
		memberships: gormstore.NewMembershipRepository(db),
		refresh:     gormstore.NewRefreshTokenRepository(db),
		codes:       gormstore.NewVerificationCodeRepository(db),
		attempts:    gormstore.NewRegistrationAttemptRepository(db),
		audit:       gormstore.NewAuditRepository(db),
		uow:         gormstore.NewUnitOfWork(db),
		backendName: b.name,
		backendIsPG: b.driver == storage.DriverPostgres,
	}

	if s.backendIsPG {
		// Postgres é compartilhado entre execuções: isolamos por prefixo de
		// e-mail/ID em vez de recriar o schema.
		s.tablesSuffix = fmt.Sprintf("%d-%d", time.Now().UnixNano(), pgSchemaCounter.Add(1))
	}
	return s
}

// eachBackend roda o corpo em cada banco disponível.
func eachBackend(t *testing.T, fn func(t *testing.T, s *store)) {
	t.Helper()
	for _, b := range backends() {
		t.Run(b.name, func(t *testing.T) {
			fn(t, newStore(t, b))
		})
	}
}

func (s *store) nextID(prefix string) string {
	return fmt.Sprintf("%s-%s-%06d", prefix, s.tablesSuffix, s.seq.Add(1))
}

func (s *store) nextEmail() string {
	return fmt.Sprintf("pessoa%s@exemplo.test", s.nextID(""))
}

func now() time.Time { return time.Now().UTC().Truncate(time.Second) }

func (s *store) makeUser(t *testing.T, ctx context.Context, verified bool) *user.User {
	t.Helper()

	u := &user.User{
		ID:           s.nextID("u"),
		Email:        s.nextEmail(),
		PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA",
		Name:         "Bruno Blanck",
		CreatedAt:    now(),
		UpdatedAt:    now(),
	}
	if verified {
		at := now()
		u.EmailVerifiedAt = &at
	}
	require.NoError(t, s.users.Create(ctx, u))
	return u
}

func (s *store) makeHousehold(t *testing.T, ctx context.Context, userID, role string) *household.Household {
	t.Helper()

	h := &household.Household{ID: s.nextID("h"), Name: "Casa de Bruno", CreatedAt: now(), UpdatedAt: now()}
	require.NoError(t, s.households.Create(ctx, h))
	require.NoError(t, s.memberships.Create(ctx, &household.Membership{
		ID:          s.nextID("m"),
		HouseholdID: h.ID,
		UserID:      userID,
		Role:        role,
		CreatedAt:   now(),
		UpdatedAt:   now(),
	}))
	return h
}
