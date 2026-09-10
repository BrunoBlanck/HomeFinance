package storage_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func openSQLite(t *testing.T, path string) *storage.DB {
	t.Helper()

	db, err := storage.Open(t.Context(), storage.Options{
		Driver:       storage.DriverSQLite,
		DSN:          path,
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}, logging.Discard())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// Critério de aceite 4 da spec 0001: AutoMigrate cria as 6 tabelas a partir
// de banco VAZIO e roda de novo sobre banco POVOADO sem erro e sem perda.
func TestMigrateEmBancoVazioEDepoisPovoado(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "migrate.db")
	ctx := t.Context()

	db := openSQLite(t, path)
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))

	users := gormstore.NewUserRepository(db)
	households := gormstore.NewHouseholdRepository(db)
	memberships := gormstore.NewMembershipRepository(db)

	// As 6 tabelas existem e aceitam escrita.
	u := &user.User{
		ID: "u-1", Email: "bruno@exemplo.test", PasswordHash: "hash", Name: "Bruno",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, users.Create(ctx, u))
	require.NoError(t, households.Create(ctx, &household.Household{
		ID: "h-1", Name: "Casa de Bruno", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}))
	require.NoError(t, memberships.Create(ctx, &household.Membership{
		ID: "m-1", HouseholdID: "h-1", UserID: "u-1", Role: household.RoleOwner,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}))

	// Segunda passada sobre banco POVOADO: idempotente e não destrutiva.
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))

	depois, err := users.ByID(ctx, "u-1")
	require.NoError(t, err)
	assert.Equal(t, "bruno@exemplo.test", depois.Email)

	m, err := memberships.ByUserAndHousehold(ctx, "u-1", "h-1")
	require.NoError(t, err)
	assert.Equal(t, household.RoleOwner, m.Role)
}

// Duas das tabelas nasceram de achados de segurança, e a lista existe para
// que nenhuma delas suma sem alguém perceber:
//
//	refresh_families        — achado ALTA-1: estado de revogação por FAMÍLIA,
//	                          sem o qual o sucessor de uma rotação escapava da
//	                          revogação disparada por reúso;
//	registration_attempts   — achado de PRE-HIJACKING: amarra o código de 6
//	                          dígitos à sessão de cadastro que o pediu, via
//	                          registrationToken.
func TestMigrateCriaAsOitoTabelas(t *testing.T) {
	t.Parallel()

	db := openSQLite(t, filepath.Join(t.TempDir(), "tabelas.db"))
	require.NoError(t, storage.Migrate(t.Context(), db, nil, gormstore.Models()...))

	nomes := gormstore.TableNames()
	require.Len(t, nomes, 8)
	assert.ElementsMatch(t, []string{
		"users", "households", "memberships",
		"refresh_families", "refresh_tokens", "verification_codes",
		"registration_attempts", "audit_log",
	}, nomes)
	assert.Len(t, gormstore.Models(), 8, "modelo esquecido em Models() é tabela que não existe em produção")
}

func TestMigrateSemModelosNaoFaltaNada(t *testing.T) {
	t.Parallel()

	db := openSQLite(t, filepath.Join(t.TempDir(), "vazio.db"))
	assert.NoError(t, storage.Migrate(t.Context(), db, nil))
}

func TestMigrateSemBancoDevolveErro(t *testing.T) {
	t.Parallel()

	assert.Error(t, storage.Migrate(t.Context(), nil, nil, gormstore.Models()...))
}

func TestOpenRecusaDriverDesconhecido(t *testing.T) {
	t.Parallel()

	_, err := storage.Open(t.Context(), storage.Options{Driver: "oracle", DSN: "x"}, logging.Discard())
	assert.ErrorIs(t, err, storage.ErrUnsupportedDriver)
}

// Risco 6 da §9: a DSN carrega a senha do banco. Nenhum erro que sobe desta
// camada pode ecoá-la — é o que permite o readiness logar o motivo da falha.
func TestErroDeConexaoNaoVazaDSN(t *testing.T) {
	t.Parallel()

	const dsn = "postgres://usuario:SenhaSuperSecreta@127.0.0.1:1/base?sslmode=disable"

	_, err := storage.Open(t.Context(), storage.Options{
		Driver:       storage.DriverPostgres,
		DSN:          dsn,
		MaxOpenConns: 1,
	}, logging.Discard())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SenhaSuperSecreta")
	assert.NotContains(t, err.Error(), dsn)
}
