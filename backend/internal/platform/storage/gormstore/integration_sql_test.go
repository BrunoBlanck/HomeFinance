//go:build integration

// Suíte de portabilidade multi-dialeto (ADR-008).
//
// Não roda por padrão. Para executar:
//
//	TEST_POSTGRES_DSN=postgres://... \
//	TEST_MYSQL_DSN=user:senha@tcp(host:3306)/base?parseTime=true \
//	TEST_SQLSERVER_DSN=sqlserver://... \
//	go test -tags=integration -race ./internal/platform/storage/gormstore/...
//
// A suíte testcontainers completa é da Fase 6 (fora do escopo da spec 0001);
// este arquivo garante que o caminho existe e é exercitável hoje.
package gormstore_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dialect struct {
	name   string
	driver string
	env    string
}

func integrationDialects() []dialect {
	return []dialect{
		{name: "postgres", driver: storage.DriverPostgres, env: "TEST_POSTGRES_DSN"},
		{name: "mysql", driver: storage.DriverMySQL, env: "TEST_MYSQL_DSN"},
		{name: "sqlserver", driver: storage.DriverSQLServer, env: "TEST_SQLSERVER_DSN"},
	}
}

// O AutoMigrate precisa rodar sem exceção nos quatro dialetos (ADR-013(c)),
// e o ciclo completo de credenciais precisa se comportar igual em todos.
func TestPortabilidadeEntreDialetos(t *testing.T) {
	for _, d := range integrationDialects() {
		dsn := os.Getenv(d.env)
		if dsn == "" {
			t.Run(d.name, func(t *testing.T) { t.Skipf("%s não definido", d.env) })
			continue
		}

		t.Run(d.name, func(t *testing.T) {
			ctx := t.Context()

			db, err := storage.Open(ctx, storage.Options{
				Driver: d.driver, DSN: dsn, MaxOpenConns: 4, MaxIdleConns: 2,
			}, logging.Discard())
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))
			// Segunda passada sobre banco já povoado.
			require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))

			users := gormstore.NewUserRepository(db)
			households := gormstore.NewHouseholdRepository(db)
			memberships := gormstore.NewMembershipRepository(db)
			refresh := gormstore.NewRefreshTokenRepository(db)
			codes := gormstore.NewVerificationCodeRepository(db)
			uow := gormstore.NewUnitOfWork(db)

			sufixo := time.Now().UnixNano()
			userID := idf("u", sufixo)
			casaID := idf("h", sufixo)
			email := emailf(sufixo)

			require.NoError(t, uow.Do(ctx, func(ctx context.Context) error {
				if err := users.Create(ctx, &user.User{
					ID: userID, Email: email, PasswordHash: "hash", Name: "Bruno",
					CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
				}); err != nil {
					return err
				}
				if err := households.Create(ctx, &household.Household{
					ID: casaID, Name: "Casa de Bruno", CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
				}); err != nil {
					return err
				}
				return memberships.Create(ctx, &household.Membership{
					ID: idf("m", sufixo), HouseholdID: casaID, UserID: userID,
					Role: household.RoleOwner, CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
				})
			}))

			// Rotação atômica precisa funcionar igual em todo dialeto.
			tokenID := idf("rt", sufixo)
			require.NoError(t, refresh.Create(ctx, &auth.RefreshToken{
				ID: tokenID, UserID: userID, FamilyID: idf("fam", sufixo),
				TokenHash: idf("hash", sufixo), ExpiresAt: nowUTC().Add(time.Hour),
				CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
			}))

			ok, err := refresh.Rotate(ctx, tokenID, idf("suc", sufixo), nowUTC())
			require.NoError(t, err)
			assert.True(t, ok)
			ok, err = refresh.Rotate(ctx, tokenID, idf("suc2", sufixo), nowUTC())
			require.NoError(t, err)
			assert.False(t, ok, "reúso precisa ser detectado em todos os dialetos")

			// Incremento atômico de tentativas.
			codeID := idf("vc", sufixo)
			require.NoError(t, codes.Create(ctx, &auth.VerificationCode{
				ID: codeID, Email: email, Purpose: auth.PurposeEmailVerification,
				CodeHash: idf("ch", sufixo), ExpiresAt: nowUTC().Add(15 * time.Minute),
				CreatedAt: nowUTC(), UpdatedAt: nowUTC(),
			}))
			n, contou, err := codes.IncrementAttempts(ctx, codeID)
			require.NoError(t, err)
			require.True(t, contou)
			assert.Equal(t, 1, n)

			// registration_attempts SEM envio — o caso que o MySQL recusava.
			attempts := gormstore.NewRegistrationAttemptRepository(db)
			checarTentativaSemEnvio(t, ctx, attempts, sufixo)
		})
	}
}

// checarTentativaSemEnvio prova, NO BANCO DE VERDADE, que uma tentativa cujo
// código nunca foi emitido (a) é GRAVADA e (b) não é validável.
//
// É o teste que faltava quando a sentinela de "nunca emitido" era o zero de
// time.Time. Naquele desenho, este Create falhava só no MySQL: o driver
// serializa o zero como '0000-00-00 00:00:00' e o sql_mode padrão do MySQL 8
// (STRICT_TRANS_TABLES + NO_ZERO_DATE) responde erro 1292. O efeito era
// duplo e grave — o invariante do ADR-014 ("a tentativa é gravada MESMO
// quando o cooldown ou a cota seguram a mensagem") caía, e o
// POST /auth/register passava a responder 500 nos caminhos "e-mail livre" e
// "e-mail pendente" enquanto "e-mail já verificado" seguia com 202, que é um
// oráculo de enumeração pronto (grupo A da §3.12).
//
// Agora a coluna é NULA nesse estado, e NULL é o único valor que os quatro
// dialetos representam igual.
func checarTentativaSemEnvio(t *testing.T, ctx context.Context, attempts *gormstore.RegistrationAttemptRepository, sufixo int64) {
	t.Helper()

	email := emailf(sufixo + 1)
	tokenClaro := idf("tok", sufixo)
	tokenHash := auth.HashRegistrationToken(tokenClaro)

	att := &auth.RegistrationAttempt{
		ID:           idf("ra", sufixo),
		Email:        email,
		UserID:       idf("u", sufixo),
		Name:         "Bruno",
		PasswordHash: "hash",
		TokenHash:    tokenHash,
		CodeHash:     idf("ch", sufixo),
		// CodeIssuedAt fica NULO: nenhuma mensagem saiu.
		ExpiresAt: nowUTC().Add(15 * time.Minute),
		CreatedAt: nowUTC(),
		UpdatedAt: nowUTC(),
	}
	require.NoError(t, attempts.Create(ctx, att),
		"a tentativa sem envio precisa ser GRAVADA em todo dialeto (ADR-014)")

	// (a) Ela existe e voltou com o estado honesto: nulo.
	guardada, err := attempts.ByToken(ctx, email, tokenHash)
	require.NoError(t, err)
	assert.Nil(t, guardada.CodeIssuedAt, "sem envio, a coluna precisa voltar NULA")
	assert.False(t, guardada.CodeIssued())

	// (b) E não é validável: o código que ninguém recebeu não existe para a
	// validação.
	_, err = attempts.LiveByToken(ctx, email, tokenHash, nowUTC())
	assert.ErrorIs(t, err, auth.ErrNotFound,
		"código nunca enviado não pode ser encontrado para validação")

	// Não conta como emissão (cooldown) nem como tentativa viva (BAIXA-1).
	_, ok, err := attempts.LastCodeIssuedAt(ctx, email)
	require.NoError(t, err)
	assert.False(t, ok, "tentativa sem envio não empurra o cooldown do endereço")

	vivas, err := attempts.LiveByEmail(ctx, email, nowUTC(), 2)
	require.NoError(t, err)
	assert.Empty(t, vivas, "tentativa sem código emitido não é utilizável")

	// Emitida de verdade, a MESMA tentativa passa a valer — é a recuperação
	// de quem ficou sem mensagem pelo cooldown ou pela cota.
	emissao := nowUTC().Add(time.Minute)
	rodou, err := attempts.RotateCode(ctx, att.ID, idf("ch2", sufixo), emissao, emissao.Add(15*time.Minute))
	require.NoError(t, err)
	require.True(t, rodou)

	viva, err := attempts.LiveByToken(ctx, email, tokenHash, emissao)
	require.NoError(t, err)
	assert.Equal(t, att.ID, viva.ID)
	require.NotNil(t, viva.CodeIssuedAt)
	assert.True(t, viva.CodeIssued())
}

func nowUTC() time.Time { return time.Now().UTC().Truncate(time.Second) }

func idf(prefix string, sufixo int64) string {
	return prefix + "-int-" + time.Unix(0, sufixo).Format("20060102150405.000000000")
}

func emailf(sufixo int64) string {
	return "integracao" + time.Unix(0, sufixo).Format("20060102150405000000000") + "@exemplo.test"
}
