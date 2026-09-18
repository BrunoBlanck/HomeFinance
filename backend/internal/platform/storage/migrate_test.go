package storage_test

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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
func TestMigrateCriaTodasAsTabelasEsperadas(t *testing.T) {
	t.Parallel()

	db := openSQLite(t, filepath.Join(t.TempDir(), "tabelas.db"))
	require.NoError(t, storage.Migrate(t.Context(), db, nil, gormstore.Models()...))

	// A lista é escrita à mão de propósito: comparar TableNames() consigo
	// mesmo não provaria nada. Tabela nova é uma decisão, e ela passa por
	// aqui — inclusive para que as duas nascidas de achado de segurança
	// (acima) não sumam sem ninguém notar.
	//
	// Não há contagem numérica: um `Len(nomes, 8)` só diz que o número mudou,
	// e mandava corrigir o número em vez de olhar a lista.
	esperadas := []string{
		// spec 0001 — fundação e autenticação
		"users", "households", "memberships",
		"refresh_families", "refresh_tokens", "verification_codes",
		"registration_attempts", "audit_log",
		// spec 0003 — schema v2, contas e categorias
		"accounts", "categories",
		// spec 0004 — schema v3, lançamentos e importação
		"transactions", "card_statements", "import_batches", "import_rows",
		// spec 0005 — schema v4, palavras-chave
		"category_keywords", "account_keywords",
	}
	assert.ElementsMatch(t, esperadas, gormstore.TableNames())
	assert.Len(t, gormstore.Models(), len(esperadas),
		"modelo esquecido em Models() é tabela que não existe em produção")
}

// Critério de aceite 6 da spec 0003: o AutoMigrate leva um banco que já está
// no schema v1, COM DADO, para o v2 — sem perder linha e sem exigir passo
// manual.
//
// Este é o teste que o ADR-008 pede: ele registra que a evolução por
// AutoMigrate foi VERIFICADA partindo de banco povoado, e não só de banco
// vazio, que é o caso fácil.
func TestMigrateLevaBancoV1PovoadoParaV2(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "v1-para-v2.db")
	ctx := t.Context()
	db := openSQLite(t, path)

	// 1) Sobe SÓ o schema v1 e povoa.
	require.NoError(t, storage.Migrate(ctx, db, nil, modelosV1()...))

	users := gormstore.NewUserRepository(db)
	households := gormstore.NewHouseholdRepository(db)
	require.NoError(t, users.Create(ctx, &user.User{
		ID: "u-v1", Email: "antigo@exemplo.test", PasswordHash: "hash", Name: "Antigo",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}))
	require.NoError(t, households.Create(ctx, &household.Household{
		ID: "h-v1", Name: "Casa Antiga", Timezone: household.DefaultTimezone,
		Currency:  household.DefaultCurrency,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}))

	// 2) Migra para o v2 (accounts, categories e as colunas novas da casa).
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))

	// 3) O dado do v1 continua lá, inteiro.
	sobreviveu, err := users.ByID(ctx, "u-v1")
	require.NoError(t, err)
	assert.Equal(t, "antigo@exemplo.test", sobreviveu.Email)

	casa, err := households.ByID(ctx, "h-v1")
	require.NoError(t, err)
	assert.Equal(t, "Casa Antiga", casa.Name)
	// As colunas novas nasceram com o default declarado no modelo. Sem esse
	// default, a casa antiga ficaria com fuso vazio e passaria a calcular
	// "hoje" — e portanto "atrasado" — no lugar errado.
	assert.Equal(t, household.DefaultTimezone, casa.Timezone)
	assert.Equal(t, household.DefaultCurrency, casa.Currency)

	// 4) As tabelas novas existem e aceitam escrita.
	accounts := gormstore.NewAccountRepository(db)
	require.NoError(t, accounts.Create(ctx, &account.Account{
		ID: "a-1", HouseholdID: "h-v1", Name: "Carteira", NameNorm: "carteira",
		Kind: account.KindCash, OpeningBalanceCents: 500, OpeningDate: civil.MustNew(2026, 1, 1),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}))
	lidas, err := accounts.List(ctx, "h-v1", false)
	require.NoError(t, err)
	require.Len(t, lidas, 1)
	assert.Equal(t, "2026-01-01", lidas[0].OpeningDate.String())
}

// modelosV1 reproduz o schema anterior à spec 0003 — sem accounts nem
// categories. É o ponto de partida do teste de evolução acima.
//
// A lista é EXPLÍCITA, e não mais um "corte o fim de Models()": o corte por
// índice funcionou enquanto havia dois modelos novos, e silenciosamente passou
// a reconstruir um v1 errado quando o schema v3 acrescentou mais quatro. Uma
// lista escrita à mão quebra o build quando alguém remove um modelo, que é
// exatamente quando se quer ser avisado.
func modelosV1() []any {
	return []any{
		&gormstore.User{},
		&gormstore.Household{},
		&gormstore.Membership{},
		&gormstore.RefreshFamily{},
		&gormstore.RefreshToken{},
		&gormstore.VerificationCode{},
		&gormstore.RegistrationAttempt{},
		&gormstore.AuditLog{},
	}
}

// accountV2 é a tabela accounts COMO ELA ERA no schema v2: sem institution,
// sem statement_closing_day e sem statement_due_day.
//
// Existe porque migrar a partir das structs ATUAIS não prova nada sobre as
// colunas novas — a tabela já nasceria com elas. Para provar que o AutoMigrate
// acrescenta coluna a uma tabela que já tem dado, é preciso criar a tabela
// antiga de verdade.
type accountV2 struct {
	ID                  string `gorm:"type:varchar(36);primaryKey"`
	HouseholdID         string `gorm:"type:varchar(36);not null"`
	Name                string `gorm:"type:varchar(80);not null"`
	NameNorm            string `gorm:"type:varchar(80);not null"`
	Kind                string `gorm:"type:varchar(20);not null"`
	OpeningBalanceCents int64  `gorm:"not null"`
	OpeningDate         string `gorm:"type:varchar(10);not null"`
	ArchivedAt          *time.Time
	CreatedAt           time.Time `gorm:"not null"`
	UpdatedAt           time.Time `gorm:"not null"`
	DeletedAt           *time.Time
}

func (accountV2) TableName() string { return "accounts" }

// modelosV2 é o schema da entrega E1 (spec 0003): o v1 mais accounts (na forma
// antiga) e categories.
func modelosV2() []any {
	return append(modelosV1(), &accountV2{}, &gormstore.Category{})
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

// Critério de aceite 6 da spec 0003, aplicado agora ao schema v3: o
// AutoMigrate leva um banco que já está no v2, COM DADO, para o v3 — sem
// perder linha e sem exigir passo manual.
//
// O caso fácil (banco vazio) não prova nada sobre produção. O que este teste
// prova é o caso que dá medo: tabela com dado ganhando coluna NOT NULL nova.
func TestMigrateLevaBancoV2PovoadoParaV3(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "v2-para-v3.db")
	ctx := t.Context()
	db := openSQLite(t, path)

	// 1) Sobe o schema v2 (accounts SEM as colunas novas) e povoa.
	require.NoError(t, storage.Migrate(ctx, db, nil, modelosV2()...))

	users := gormstore.NewUserRepository(db)
	households := gormstore.NewHouseholdRepository(db)
	categories := gormstore.NewCategoryRepository(db)

	require.NoError(t, users.Create(ctx, &user.User{
		ID: "u-v2", Email: "antigo-v2@exemplo.test", PasswordHash: "hash", Name: "Antigo",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}))
	require.NoError(t, households.Create(ctx, &household.Household{
		ID: "h-v2", Name: "Casa do v2", Timezone: household.DefaultTimezone,
		Currency:  household.DefaultCurrency,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}))
	require.NoError(t, categories.Create(ctx, &category.Category{
		ID: "c-v2", HouseholdID: "h-v2", Name: "Moradia", NameNorm: "moradia",
		Kind: "expense", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}))
	// A conta é criada pela struct ANTIGA: é isso que faz a tabela existir sem
	// as colunas do v3 no momento da migração.
	require.NoError(t, db.Gorm().Create(&accountV2{
		ID: "a-v2", HouseholdID: "h-v2", Name: "Cartão", NameNorm: "cartao",
		Kind: "credit_card", OpeningBalanceCents: -25_000, OpeningDate: "2026-01-01",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}).Error)

	// 2) Migra para o v3 — e roda duas vezes, porque o boot da API roda o
	// AutoMigrate em toda subida.
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))

	// 3) Nada do v2 se perdeu.
	u, err := users.ByID(ctx, "u-v2")
	require.NoError(t, err)
	assert.Equal(t, "antigo-v2@exemplo.test", u.Email)

	casa, err := households.ByID(ctx, "h-v2")
	require.NoError(t, err)
	assert.Equal(t, "Casa do v2", casa.Name)

	cats, err := categories.List(ctx, "h-v2", true)
	require.NoError(t, err)
	require.Len(t, cats, 1)
	assert.Equal(t, "Moradia", cats[0].Name)

	accounts := gormstore.NewAccountRepository(db)
	conta, err := accounts.ByID(ctx, "h-v2", "a-v2")
	require.NoError(t, err)
	assert.Equal(t, "Cartão", conta.Name)
	assert.EqualValues(t, -25_000, conta.OpeningBalanceCents, "saldo de abertura intacto")
	assert.Equal(t, "2026-01-01", conta.OpeningDate.String())
	// A coluna NOT NULL nova nasceu preenchida pelo DEFAULT declarado no
	// modelo. Sem esse default, a conta antiga ficaria com string vazia e a
	// importação não acharia leiaute nenhum sem dizer por quê.
	assert.Equal(t, account.InstitutionOther, conta.Institution)
	assert.Nil(t, conta.StatementClosingDay, "dia de fechamento continua sem valor, não zero")
	assert.Nil(t, conta.StatementDueDay)

	// 4) As tabelas novas existem e aceitam escrita — inclusive amarrando
	// lançamento, fatura e lote, que é o caminho da importação.
	statements := gormstore.NewCardStatementRepository(db)
	fatura := &cardstatement.Statement{
		ID: "cs-v3", HouseholdID: "h-v2", AccountID: "a-v2", CompetenceMonth: "2026-02",
		ClosingDate: civil.MustNew(2026, 2, 2), DueDate: civil.MustNew(2026, 2, 10),
		Source: cardstatement.SourceImport, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, statements.Upsert(ctx, fatura))

	lotes := gormstore.NewImportRepository(db)
	lote := &importer.Batch{
		ID: "ib-v3", HouseholdID: "h-v2", AccountID: "a-v2", CreatedBy: "u-v2",
		Institution: "nubank", DocKind: "card_statement", FormatID: "nubank_card_statement_v1",
		FileName: "fatura.csv", ContentSHA256: "abc123", RowCount: 1,
		MinDate: civil.MustNew(2026, 1, 5), MaxDate: civil.MustNew(2026, 1, 30),
		Status: importer.BatchStatusPending, ExpiresAt: time.Now().UTC().Add(time.Hour),
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	require.NoError(t, lotes.CreateBatch(ctx, lote))
	require.NoError(t, lotes.CreateRows(ctx, "h-v2", []importer.Row{{
		ID: "ir-v3", HouseholdID: "h-v2", BatchID: "ib-v3", Seq: 1, LineNo: 5,
		Kind: transaction.KindExpense, OccurredOn: civil.MustNew(2026, 1, 20),
		AmountCents: 4_990, Description: "Assinatura", DescriptionNorm: "assinatura",
		DedupKey: "chave-1", Status: "importar", CreatedAt: time.Now().UTC(),
	}}))

	transactions := gormstore.NewTransactionRepository(db)
	require.NoError(t, transactions.CreateBatch(ctx, "h-v2", []transaction.Transaction{{
		ID: "t-v3", HouseholdID: "h-v2", Kind: transaction.KindExpense, AccountID: "a-v2",
		CategoryID: &cats[0].ID, AmountCents: 4_990, Description: "Assinatura",
		DescriptionNorm: "assinatura", OccurredOn: civil.MustNew(2026, 1, 20),
		CompetenceMonth: "2026-02", StatementID: &fatura.ID, Source: transaction.SourceImport,
		ImportBatchID: &lote.ID, DedupKey: "chave-1", DedupOrdinal: 1, CreatedBy: "u-v2",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}))

	resumo, err := transactions.Summary(ctx, "h-v2", transaction.SummaryFilter{CompetenceMonth: "2026-02"})
	require.NoError(t, err)
	assert.EqualValues(t, 4_990, resumo.ExpenseCents)
	assert.EqualValues(t, 1, resumo.Count)
}

// Os índices do schema v3 são a diferença entre "o extrato abre" e "o extrato
// abre em 300 ms com 5 mil lançamentos" — e, no caso do único, entre "importar
// duas vezes duplica" e "o banco recusa".
//
// O teste confere que o AutoMigrate os criou DE FATO, pelo Migrator (que
// pergunta ao banco, e não à struct). Um erro de digitação na tag de índice
// não falha o AutoMigrate: ele simplesmente cria um índice a menos, em
// silêncio, e ninguém percebe até a tabela crescer.
func TestMigrateCriaOsIndicesDoSchemaV3(t *testing.T) {
	t.Parallel()

	db := openSQLite(t, filepath.Join(t.TempDir(), "indices-v3.db"))
	require.NoError(t, storage.Migrate(t.Context(), db, nil, gormstore.Models()...))

	migrator := db.Gorm().Migrator()

	for _, nome := range []string{
		"ux_transactions_dedup",
		"ix_transactions_occurred",
		"ix_transactions_account_occurred",
		"ix_transactions_competence",
		"ix_transactions_statement",
		"ix_transactions_group",
		"ix_transactions_batch",
		"ix_transactions_deleted_at",
		"ix_transactions_category",
	} {
		assert.True(t, migrator.HasIndex(&gormstore.Transaction{}, nome), "falta o índice %s", nome)
	}

	assert.True(t, migrator.HasIndex(&gormstore.CardStatement{}, "ux_card_statements"),
		"falta o índice ux_card_statements")

	// A ausência é tão deliberada quanto a presença: um índice comum com as
	// mesmas colunas do único, na mesma ordem, é só custo de escrita — e o
	// AutoMigrate cria índice mas nunca remove (ADR-008), então criá-lo por
	// engano custaria um passo manual de migração para desfazer.
	assert.False(t, migrator.HasIndex(&gormstore.CardStatement{}, "ix_card_statements_account"),
		"ix_card_statements_account é redundante com ux_card_statements e não deve existir")

	for _, nome := range []string{
		"ix_import_batches_household",
		"ix_import_batches_content",
		"ix_import_batches_expires",
	} {
		assert.True(t, migrator.HasIndex(&gormstore.ImportBatch{}, nome), "falta o índice %s", nome)
	}

	for _, nome := range []string{"ux_import_rows_seq", "ix_import_rows_batch"} {
		assert.True(t, migrator.HasIndex(&gormstore.ImportRow{}, nome), "falta o índice %s", nome)
	}

	// As colunas novas de accounts também precisam existir — elas são o que a
	// importação usa para achar o leiaute e as datas da fatura.
	for _, coluna := range []string{"institution", "statement_closing_day", "statement_due_day"} {
		assert.True(t, migrator.HasColumn(&gormstore.Account{}, coluna), "falta a coluna accounts.%s", coluna)
	}
}

// importBatchV3 é a tabela import_batches COMO ELA ERA no schema v3: sem
// linked_count e sem transfer_pairs_count.
//
// Existe pelo mesmo motivo de accountV2: migrar a partir das structs ATUAIS não
// prova nada sobre as colunas novas — a tabela já nasceria com elas. Para
// provar que o AutoMigrate acrescenta coluna NOT NULL a uma tabela que já tem
// dado, é preciso criar a tabela antiga de verdade e povoá-la.
type importBatchV3 struct {
	ID          string `gorm:"type:varchar(36);primaryKey"`
	HouseholdID string `gorm:"type:varchar(36);not null;index:ix_import_batches_household,priority:1;index:ix_import_batches_content,priority:1"`
	AccountID   string `gorm:"type:varchar(36);not null"`
	CreatedBy   string `gorm:"type:varchar(36);not null"`

	Institution   string `gorm:"type:varchar(20);not null"`
	DocKind       string `gorm:"type:varchar(16);not null"`
	FormatID      string `gorm:"type:varchar(40);not null"`
	FileName      string `gorm:"type:varchar(200);not null"`
	ContentSHA256 string `gorm:"type:varchar(64);not null;index:ix_import_batches_content,priority:2"`
	Encoding      string `gorm:"type:varchar(16);not null;default:'utf-8'"`

	RowCount      int `gorm:"type:int;not null;default:0"`
	ImportedCount int `gorm:"type:int;not null;default:0"`
	SkippedCount  int `gorm:"type:int;not null;default:0"`
	BlockedCount  int `gorm:"type:int;not null;default:0"`
	RestoredCount int `gorm:"type:int;not null;default:0"`
	RejectedCount int `gorm:"type:int;not null;default:0"`

	MinDate string `gorm:"type:varchar(10);not null"`
	MaxDate string `gorm:"type:varchar(10);not null"`

	SuggestedCompetenceMonth *string `gorm:"type:varchar(7)"`
	SuggestedClosingDate     *string `gorm:"type:varchar(10)"`
	SuggestedDueDate         *string `gorm:"type:varchar(10)"`

	Status      string    `gorm:"type:varchar(12);not null"`
	ExpiresAt   time.Time `gorm:"not null;index:ix_import_batches_expires"`
	CommittedAt *time.Time
	CreatedAt   time.Time `gorm:"not null;index:ix_import_batches_household,priority:2"`
	UpdatedAt   time.Time `gorm:"not null"`
}

func (importBatchV3) TableName() string { return "import_batches" }

// importRowV3 é a tabela import_rows COMO ELA ERA no schema v3: sem as quatro
// colunas de sugestão da análise.
type importRowV3 struct {
	ID          string `gorm:"type:varchar(36);primaryKey"`
	HouseholdID string `gorm:"type:varchar(36);not null;index:ix_import_rows_batch,priority:1"`
	BatchID     string `gorm:"type:varchar(36);not null;uniqueIndex:ux_import_rows_seq,priority:1;index:ix_import_rows_batch,priority:2"`
	Seq         int    `gorm:"type:int;not null;uniqueIndex:ux_import_rows_seq,priority:2;index:ix_import_rows_batch,priority:3"`
	LineNo      int    `gorm:"type:int;not null"`

	Kind            string  `gorm:"type:varchar(12);not null"`
	OccurredOn      string  `gorm:"type:varchar(10);not null"`
	AmountCents     int64   `gorm:"not null"`
	Description     string  `gorm:"type:varchar(140);not null"`
	DescriptionNorm string  `gorm:"type:varchar(140);not null"`
	ExternalID      *string `gorm:"type:varchar(64)"`
	DedupKey        string  `gorm:"type:varchar(64);not null"`

	Status             string    `gorm:"type:varchar(24);not null"`
	RejectReason       *string   `gorm:"type:varchar(32)"`
	MatchTransactionID *string   `gorm:"type:varchar(36)"`
	CreatedAt          time.Time `gorm:"not null"`
}

func (importRowV3) TableName() string { return "import_rows" }

// modelosV3 é o schema da entrega E2 (spec 0004): o v1 mais as tabelas do v2 e
// do v3, com import_batches e import_rows na forma ANTIGA e SEM as duas tabelas
// de palavras-chave. Lista explícita, pelo motivo registrado em modelosV1.
func modelosV3() []any {
	return append(modelosV1(),
		&gormstore.Account{},
		&gormstore.Category{},
		&gormstore.Transaction{},
		&gormstore.CardStatement{},
		&importBatchV3{},
		&importRowV3{},
	)
}

// Critério de aceite 6 da spec 0003, aplicado ao schema v4 (spec 0005): o
// AutoMigrate leva um banco que já está no v3, COM DADO, para o v4 — sem
// perder linha, sem exigir passo manual e rodando DUAS vezes (o boot da API
// roda o AutoMigrate em toda subida).
//
// O que este teste prova, além do caso fácil de banco vazio:
//
//	(a) o dado do v3 sobrevive inteiro;
//	(b) as seis colunas novas existem, as anuláveis nascem NULAS na linha
//	    antiga e as NOT NULL nascem com o DEFAULT de coluna (0), não com NULL;
//	(c) as duas tabelas novas e os quatro índices existem DE FATO, pelo
//	    Migrator (que pergunta ao banco, e não à struct);
//	(d) o índice único (household_id, keyword_norm) recusa a mesma norm em
//	    OUTRA categoria da mesma casa, aceita em conta da mesma casa
//	    (conjuntos independentes) e aceita em outra casa.
func TestMigrateLevaBancoV3PovoadoParaV4(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "v3-para-v4.db")
	ctx := t.Context()
	db := openSQLite(t, path)
	agora := time.Now().UTC().Truncate(time.Second)

	// 1) Sobe o schema v3 (lotes e linhas SEM as colunas novas) e povoa.
	require.NoError(t, storage.Migrate(ctx, db, nil, modelosV3()...))

	users := gormstore.NewUserRepository(db)
	households := gormstore.NewHouseholdRepository(db)
	categories := gormstore.NewCategoryRepository(db)
	accounts := gormstore.NewAccountRepository(db)
	transactions := gormstore.NewTransactionRepository(db)

	require.NoError(t, users.Create(ctx, &user.User{
		ID: "u-v3", Email: "antigo-v3@exemplo.test", PasswordHash: "hash", Name: "Antigo",
		CreatedAt: agora, UpdatedAt: agora,
	}))
	require.NoError(t, households.Create(ctx, &household.Household{
		ID: "h-v3", Name: "Casa do v3", Timezone: household.DefaultTimezone,
		Currency: household.DefaultCurrency, CreatedAt: agora, UpdatedAt: agora,
	}))
	require.NoError(t, categories.Create(ctx, &category.Category{
		ID: "c-v3", HouseholdID: "h-v3", Name: "Mercado", NameNorm: "mercado",
		Kind: category.KindExpense, CreatedAt: agora, UpdatedAt: agora,
	}))
	require.NoError(t, accounts.Create(ctx, &account.Account{
		ID: "a-v3", HouseholdID: "h-v3", Name: "Conta Corrente", NameNorm: "conta corrente",
		Kind: account.KindChecking, OpeningBalanceCents: 100_00, OpeningDate: civil.MustNew(2026, 1, 1),
		Institution: account.InstitutionOther, CreatedAt: agora, UpdatedAt: agora,
	}))
	catID := "c-v3"
	require.NoError(t, transactions.CreateBatch(ctx, "h-v3", []transaction.Transaction{{
		ID: "t-v3", HouseholdID: "h-v3", Kind: transaction.KindExpense, AccountID: "a-v3",
		CategoryID: &catID, AmountCents: 57_90, Description: "Supermercado Extra",
		DescriptionNorm: "supermercado extra", OccurredOn: civil.MustNew(2026, 8, 14),
		CompetenceMonth: "2026-08", Source: transaction.SourceManual,
		DedupKey: "chave-v3", DedupOrdinal: 1, CreatedBy: "u-v3", CreatedAt: agora, UpdatedAt: agora,
	}}))
	// Lote e linha são criados pelas structs ANTIGAS: é isso que faz as tabelas
	// existirem sem as colunas do v4 no momento da migração.
	require.NoError(t, db.Gorm().Create(&importBatchV3{
		ID: "ib-v3", HouseholdID: "h-v3", AccountID: "a-v3", CreatedBy: "u-v3",
		Institution: "nubank", DocKind: "bank_statement", FormatID: "nubank_bank_statement_v1",
		FileName: "extrato.csv", ContentSHA256: "abc123", Encoding: "utf-8", RowCount: 1,
		MinDate: "2026-08-01", MaxDate: "2026-08-31", Status: importer.BatchStatusPending,
		ExpiresAt: agora.Add(time.Hour), CreatedAt: agora, UpdatedAt: agora,
	}).Error)
	require.NoError(t, db.Gorm().Create(&importRowV3{
		ID: "ir-v3", HouseholdID: "h-v3", BatchID: "ib-v3", Seq: 1, LineNo: 2,
		Kind: transaction.KindExpense, OccurredOn: "2026-08-14", AmountCents: 57_90,
		Description: "Supermercado Extra", DescriptionNorm: "supermercado extra",
		DedupKey: "chave-v3", Status: "novo", CreatedAt: agora,
	}).Error)

	migrator := db.Gorm().Migrator()
	require.False(t, migrator.HasColumn(&gormstore.ImportRow{}, "suggested_category_id"),
		"o cenário de partida precisa ser o v3 de verdade, sem as colunas novas")
	require.False(t, migrator.HasTable("category_keywords"))

	// 2) Migra para o v4 — DUAS vezes, como o boot faz.
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))

	// 3) (a) Nada do v3 se perdeu.
	u, err := users.ByID(ctx, "u-v3")
	require.NoError(t, err)
	assert.Equal(t, "antigo-v3@exemplo.test", u.Email)

	casa, err := households.ByID(ctx, "h-v3")
	require.NoError(t, err)
	assert.Equal(t, "Casa do v3", casa.Name)

	cat, err := categories.ByID(ctx, "h-v3", "c-v3")
	require.NoError(t, err)
	assert.Equal(t, "Mercado", cat.Name)

	conta, err := accounts.ByID(ctx, "h-v3", "a-v3")
	require.NoError(t, err)
	assert.Equal(t, "Conta Corrente", conta.Name)
	assert.EqualValues(t, 100_00, conta.OpeningBalanceCents)

	lancamento, err := transactions.ByID(ctx, "h-v3", "t-v3")
	require.NoError(t, err)
	assert.EqualValues(t, 57_90, lancamento.AmountCents)
	require.NotNil(t, lancamento.CategoryID)
	assert.Equal(t, "c-v3", *lancamento.CategoryID)

	// 4) (b) As seis colunas novas existem — pelo Migrator, que pergunta ao
	// banco — e a linha antiga, lida pelo repositório ATUAL, tem os quatro
	// palpites NULOS e os dois contadores em ZERO (default de coluna, não NULL
	// numa coluna NOT NULL).
	for _, coluna := range []string{
		"suggested_category_id", "match_score", "matched_keyword", "suggested_counterpart_account_id",
	} {
		assert.True(t, migrator.HasColumn(&gormstore.ImportRow{}, coluna), "falta a coluna import_rows.%s", coluna)
	}
	for _, coluna := range []string{"linked_count", "transfer_pairs_count"} {
		assert.True(t, migrator.HasColumn(&gormstore.ImportBatch{}, coluna), "falta a coluna import_batches.%s", coluna)
	}

	lotes := gormstore.NewImportRepository(db)
	loteAntigo, err := lotes.BatchByID(ctx, "h-v3", "ib-v3")
	require.NoError(t, err)
	assert.Equal(t, importer.BatchStatusPending, loteAntigo.Status)
	assert.Zero(t, loteAntigo.LinkedCount, "contador novo nasce em zero na linha antiga")
	assert.Zero(t, loteAntigo.TransferPairsCount)
	assert.Equal(t, 1, loteAntigo.RowCount, "contador antigo intacto")

	linhasAntigas, err := lotes.ListRows(ctx, "h-v3", "ib-v3", 0, 10)
	require.NoError(t, err)
	require.Len(t, linhasAntigas, 1)
	antiga := linhasAntigas[0]
	assert.Equal(t, "Supermercado Extra", antiga.Description)
	assert.Nil(t, antiga.SuggestedCategoryID, "palpite nasce NULO, não vazio")
	assert.Nil(t, antiga.MatchScore)
	assert.Nil(t, antiga.MatchedKeyword)
	assert.Nil(t, antiga.SuggestedCounterpartAccountID)

	// A tabela migrada aceita a escrita NOVA, com os palpites preenchidos.
	pontuacao := 88
	palavra := "supermercado"
	require.NoError(t, lotes.CreateRows(ctx, "h-v3", []importer.Row{{
		ID: "ir-v4", HouseholdID: "h-v3", BatchID: "ib-v3", Seq: 2, LineNo: 3,
		Kind: transaction.KindExpense, OccurredOn: civil.MustNew(2026, 8, 15), AmountCents: 12_00,
		Description: "Mercado do Bairro", DescriptionNorm: "mercado do bairro",
		DedupKey: "chave-v4", Status: "novo", CreatedAt: agora,
		SuggestedCategoryID: &catID, MatchScore: &pontuacao, MatchedKeyword: &palavra,
	}}))
	linhas, err := lotes.ListRows(ctx, "h-v3", "ib-v3", 1, 10)
	require.NoError(t, err)
	require.Len(t, linhas, 1)
	require.NotNil(t, linhas[0].SuggestedCategoryID)
	assert.Equal(t, "c-v3", *linhas[0].SuggestedCategoryID)
	require.NotNil(t, linhas[0].MatchScore)
	assert.Equal(t, 88, *linhas[0].MatchScore)
	require.NotNil(t, linhas[0].MatchedKeyword)
	assert.Equal(t, "supermercado", *linhas[0].MatchedKeyword)

	// Os contadores novos entram no MESMO UPDATE condicional dos antigos.
	afetadas, err := lotes.UpdateBatchStatus(ctx, "h-v3", "ib-v3",
		importer.BatchStatusPending, importer.BatchStatusCommitted, agora,
		&importer.BatchOutcome{ImportedCount: 1, LinkedCount: 1, TransferPairsCount: 2})
	require.NoError(t, err)
	assert.EqualValues(t, 1, afetadas)
	confirmado, err := lotes.BatchByID(ctx, "h-v3", "ib-v3")
	require.NoError(t, err)
	assert.Equal(t, 1, confirmado.LinkedCount)
	assert.Equal(t, 2, confirmado.TransferPairsCount)

	// 5) (c) As duas tabelas e os quatro índices existem DE FATO. Um erro de
	// digitação na tag não falha o AutoMigrate: ele só cria um índice a menos,
	// em silêncio — e o único é o que sustenta o 409.
	assert.True(t, migrator.HasTable("category_keywords"))
	assert.True(t, migrator.HasTable("account_keywords"))
	for _, nome := range []string{"ux_category_keywords_norm", "ix_category_keywords_cat"} {
		assert.True(t, migrator.HasIndex(&gormstore.CategoryKeyword{}, nome), "falta o índice %s", nome)
	}
	for _, nome := range []string{"ux_account_keywords_norm", "ix_account_keywords_acc"} {
		assert.True(t, migrator.HasIndex(&gormstore.AccountKeyword{}, nome), "falta o índice %s", nome)
	}

	// 6) (d) O índice único funciona como o ADR-026(d) promete.
	require.NoError(t, households.Create(ctx, &household.Household{
		ID: "h-outra", Name: "Casa Vizinha", Timezone: household.DefaultTimezone,
		Currency: household.DefaultCurrency, CreatedAt: agora, UpdatedAt: agora,
	}))
	require.NoError(t, categories.Create(ctx, &category.Category{
		ID: "c-v4-2", HouseholdID: "h-v3", Name: "Farmácia", NameNorm: "farmacia",
		Kind: category.KindExpense, CreatedAt: agora, UpdatedAt: agora,
	}))

	require.NoError(t, categories.ReplaceKeywords(ctx, "h-v3", "c-v3", []category.Keyword{
		{ID: "kw-1", Keyword: "Supermercado", Norm: "supermercado", Position: 0, CreatedAt: agora},
	}))

	// A MESMA norm em OUTRA categoria da mesma casa: o índice recusa, e o
	// repositório traduz para o erro de domínio — sem ecoar a palavra.
	err = categories.ReplaceKeywords(ctx, "h-v3", "c-v4-2", []category.Keyword{
		{ID: "kw-2", Keyword: "SUPERMERCADO", Norm: "supermercado", Position: 0, CreatedAt: agora},
	})
	require.ErrorIs(t, err, category.ErrKeywordTaken)
	assert.NotContains(t, err.Error(), "supermercado", "a mensagem do erro não pode carregar a palavra")

	// A mesma norm em CONTA da mesma casa: conjuntos independentes, aceita.
	require.NoError(t, accounts.ReplaceKeywords(ctx, "h-v3", "a-v3", []account.Keyword{
		{ID: "akw-1", Keyword: "supermercado", Norm: "supermercado", Position: 0, CreatedAt: agora},
	}))

	// A mesma norm em OUTRA casa: aceita — a unicidade é por casa.
	require.NoError(t, categories.Create(ctx, &category.Category{
		ID: "c-outra", HouseholdID: "h-outra", Name: "Mercado", NameNorm: "mercado",
		Kind: category.KindExpense, CreatedAt: agora, UpdatedAt: agora,
	}))
	require.NoError(t, categories.ReplaceKeywords(ctx, "h-outra", "c-outra", []category.Keyword{
		{ID: "kw-3", Keyword: "supermercado", Norm: "supermercado", Position: 0, CreatedAt: agora},
	}))

	// E a leitura respeita a casa: a vizinha não enxerga a palavra da outra.
	daCasa, err := categories.ListKeywords(ctx, "h-v3")
	require.NoError(t, err)
	require.Len(t, daCasa, 1)
	assert.Equal(t, "c-v3", daCasa[0].CategoryID)
	daVizinha, err := categories.ListKeywords(ctx, "h-outra")
	require.NoError(t, err)
	require.Len(t, daVizinha, 1)
	assert.Equal(t, "c-outra", daVizinha[0].CategoryID)
}

// --- schema v4 continua v4: as naturezas de investimento (spec 0006) ---------

// ddlSpy grava os comandos que o AutoMigrate EXECUTA.
//
// O callback é o de `Raw`, e a escolha é o que dá sentido ao teste: o migrator
// PERGUNTA ao banco por `Raw(...).Row()`/`.Rows()` (existe a tabela? quais as
// colunas? quais os índices?), que passam pelo callback de Row, e só MANDA
// mudar por `Exec(...)`, que passa por este. Então tudo que aparecer aqui é
// alteração de schema — e o que este teste afirma é que, no E7, não aparece
// nada.
type ddlSpy struct {
	mu       sync.Mutex
	comandos []string
}

func (s *ddlSpy) registrar(db *gorm.DB) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.comandos = append(s.comandos, db.Statement.SQL.String())
}

func (s *ddlSpy) emitidos() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.comandos))
	copy(out, s.comandos)
	return out
}

func espiarDDL(t *testing.T, db *storage.DB) *ddlSpy {
	t.Helper()

	spy := &ddlSpy{}
	g := db.Gorm()
	require.NoError(t, g.Callback().Raw().After("gorm:raw").Register("ddl:spy", spy.registrar))
	t.Cleanup(func() { _ = g.Callback().Raw().Remove("ddl:spy") })
	return spy
}

// Critério 19 do plano da E7 (spec 0006, ADR-029a): as naturezas `investment`
// e `redemption` NÃO mudam o schema.
//
// O teste parte de um banco **v4 povoado** — o banco de quem já usa o app — e
// prova três coisas que só juntas sustentam a afirmação:
//
//	(a) o AutoMigrate da subida seguinte não emite NENHUM comando de alteração
//	    de schema. Não é "não deu erro": é a lista de DDL executada, vazia;
//	(b) o dado do v4 continua legível pelos repositórios ATUAIS;
//	(c) categoria de natureza `investment`/`redemption` grava e lê inteira —
//	    as duas palavras têm exatamente 10 caracteres e cabem, sem sobra
//	    nenhuma, no `varchar(10)` que a coluna já tinha.
//
// O (c) é o que torna o (a) verdadeiro e não apenas vazio: um valor que não
// coubesse na coluna não apareceria como falha de migração, e sim como dado
// truncado em silêncio no MySQL e erro de escrita no PostgreSQL.
func TestMigrateMantemOSchemaV4ComAsNaturezasDeInvestimento(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "v4-com-investimentos.db")
	ctx := t.Context()
	db := openSQLite(t, path)
	agora := time.Now().UTC().Truncate(time.Second)

	// 1) Sobe o v4 e povoa com o que existia ANTES do E7: categorias de
	// receita e despesa, e um lançamento pendurado numa delas.
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))

	users := gormstore.NewUserRepository(db)
	households := gormstore.NewHouseholdRepository(db)
	categories := gormstore.NewCategoryRepository(db)
	accounts := gormstore.NewAccountRepository(db)
	transactions := gormstore.NewTransactionRepository(db)

	require.NoError(t, users.Create(ctx, &user.User{
		ID: "u-v4", Email: "antigo-v4@exemplo.test", PasswordHash: "hash", Name: "Antigo",
		CreatedAt: agora, UpdatedAt: agora,
	}))
	require.NoError(t, households.Create(ctx, &household.Household{
		ID: "h-v4", Name: "Casa do v4", Timezone: household.DefaultTimezone,
		Currency: household.DefaultCurrency, CreatedAt: agora, UpdatedAt: agora,
	}))
	require.NoError(t, accounts.Create(ctx, &account.Account{
		ID: "a-v4", HouseholdID: "h-v4", Name: "Conta Corrente", NameNorm: "conta corrente",
		Kind: account.KindChecking, OpeningBalanceCents: 100_00, OpeningDate: civil.MustNew(2026, 1, 1),
		Institution: account.InstitutionOther, CreatedAt: agora, UpdatedAt: agora,
	}))
	require.NoError(t, categories.Create(ctx, &category.Category{
		ID: "c-v4", HouseholdID: "h-v4", Name: "Investimentos", NameNorm: "investimentos",
		Kind: category.KindExpense, CreatedAt: agora, UpdatedAt: agora,
	}))
	catV4 := "c-v4"
	require.NoError(t, transactions.CreateBatch(ctx, "h-v4", []transaction.Transaction{{
		ID: "t-v4", HouseholdID: "h-v4", Kind: transaction.KindExpense, AccountID: "a-v4",
		CategoryID: &catV4, AmountCents: 2_000_00, Description: "CDB 15 DIAS",
		DescriptionNorm: "cdb 15 dias", OccurredOn: civil.MustNew(2026, 9, 5),
		CompetenceMonth: "2026-09", Source: transaction.SourceManual,
		DedupKey: "chave-v4", DedupOrdinal: 1, CreatedBy: "u-v4", CreatedAt: agora, UpdatedAt: agora,
	}}))
	require.NoError(t, categories.ReplaceKeywords(ctx, "h-v4", "c-v4", []category.Keyword{
		{ID: "kw-v4", Keyword: "CDB", Norm: "cdb", Position: 0, CreatedAt: agora},
	}))

	// 2) (a) A subida seguinte, já com o código do E7: nenhum DDL. O
	// AutoMigrate roda DUAS vezes porque o boot roda em toda subida — se a
	// segunda emitisse algo, seria uma alteração acontecendo a cada restart.
	spy := espiarDDL(t, db)
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))
	assert.Empty(t, spy.emitidos(),
		"o E7 não muda o schema: o AutoMigrate não pode emitir CREATE, ALTER nem DROP")

	// 3) (b) O dado do v4 continua inteiro, lido pelos repositórios atuais.
	conta, err := accounts.ByID(ctx, "h-v4", "a-v4")
	require.NoError(t, err)
	assert.Equal(t, "Conta Corrente", conta.Name)

	lancamento, err := transactions.ByID(ctx, "h-v4", "t-v4")
	require.NoError(t, err)
	assert.EqualValues(t, 2_000_00, lancamento.AmountCents)
	require.NotNil(t, lancamento.CategoryID)
	assert.Equal(t, "c-v4", *lancamento.CategoryID)

	palavras, err := categories.ListKeywords(ctx, "h-v4")
	require.NoError(t, err)
	require.Len(t, palavras, 1)
	assert.Equal(t, "cdb", palavras[0].Norm)

	// 4) (c) As duas naturezas novas cabem em varchar(10) — SEM sobra — e
	// sobrevivem à ida e à volta do banco.
	for _, kind := range []string{category.KindInvestment, category.KindRedemption} {
		assert.LessOrEqual(t, len(kind), 10,
			"a natureza %q não cabe em categories.kind varchar(10): alargar a coluna é MIGRAÇÃO", kind)
	}

	require.NoError(t, categories.Create(ctx, &category.Category{
		ID: "c-inv", HouseholdID: "h-v4", Name: "Aportes", NameNorm: "aportes",
		Kind: category.KindInvestment, CreatedAt: agora, UpdatedAt: agora,
	}))
	require.NoError(t, categories.Create(ctx, &category.Category{
		ID: "c-res", HouseholdID: "h-v4", Name: "Resgates", NameNorm: "resgates",
		Kind: category.KindRedemption, CreatedAt: agora, UpdatedAt: agora,
	}))

	aportes, err := categories.ByID(ctx, "h-v4", "c-inv")
	require.NoError(t, err)
	assert.Equal(t, category.KindInvestment, aportes.Kind, "a natureza não pode voltar truncada")
	resgates, err := categories.ByID(ctx, "h-v4", "c-res")
	require.NoError(t, err)
	assert.Equal(t, category.KindRedemption, resgates.Kind)

	// 5) E o lançamento do v4 passa a ser lido como aporte pela consulta nova,
	// sem nada ter sido migrado: basta a categoria dele mudar de natureza.
	require.NoError(t, transactions.UpdateCategory(ctx, "h-v4", "t-v4", "c-inv", agora))

	totais, err := transactions.SumInvestmentsByMonth(ctx, "h-v4", []string{"c-inv", "c-res"}, "2025-10", "2026-09")
	require.NoError(t, err)
	require.Len(t, totais, 1)
	assert.Equal(t, "2026-09", totais[0].Month)
	assert.EqualValues(t, 2_000_00, totais[0].ContributionsCents)
	assert.EqualValues(t, 1, totais[0].ContributionCount)

	resumo, err := transactions.Summary(ctx, "h-v4", transaction.SummaryFilter{
		CompetenceMonth: "2026-09", InvestmentCategoryIDs: []string{"c-inv", "c-res"},
	})
	require.NoError(t, err)
	assert.Zero(t, resumo.ExpenseCents, "o aporte saiu da despesa")
	assert.EqualValues(t, 2_000_00, resumo.InvestedCents)
	assert.EqualValues(t, 1, resumo.Count, "e continua aparecendo na lista do mês")
}
