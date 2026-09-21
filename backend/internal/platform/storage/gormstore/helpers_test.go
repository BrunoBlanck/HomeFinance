package gormstore_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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
	accounts     *gormstore.AccountRepository
	categories   *gormstore.CategoryRepository
	transactions *gormstore.TransactionRepository
	statements   *gormstore.CardStatementRepository
	imports      *gormstore.ImportRepository
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
		db:           db,
		users:        gormstore.NewUserRepository(db),
		households:   gormstore.NewHouseholdRepository(db),
		memberships:  gormstore.NewMembershipRepository(db),
		refresh:      gormstore.NewRefreshTokenRepository(db),
		codes:        gormstore.NewVerificationCodeRepository(db),
		attempts:     gormstore.NewRegistrationAttemptRepository(db),
		audit:        gormstore.NewAuditRepository(db),
		accounts:     gormstore.NewAccountRepository(db),
		categories:   gormstore.NewCategoryRepository(db),
		transactions: gormstore.NewTransactionRepository(db),
		statements:   gormstore.NewCardStatementRepository(db),
		imports:      gormstore.NewImportRepository(db),
		uow:          gormstore.NewUnitOfWork(db),
		backendName:  b.name,
		backendIsPG:  b.driver == storage.DriverPostgres,
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

	h := &household.Household{
		ID:        s.nextID("h"),
		Name:      "Casa de Bruno",
		Timezone:  household.DefaultTimezone,
		Currency:  household.DefaultCurrency,
		CreatedAt: now(),
		UpdatedAt: now(),
	}
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

// duasCasas monta o cenário que todo teste de isolamento precisa: duas casas
// de donos diferentes, para que "dado da outra casa" seja um dado REAL e não
// um id inventado. Id inexistente também deve dar 404, mas provar isso não
// prova isolamento — só prova que a busca falha.
func (s *store) duasCasas(t *testing.T, ctx context.Context) (minha, alheia *household.Household) {
	t.Helper()
	eu := s.makeUser(t, ctx, true)
	outro := s.makeUser(t, ctx, true)
	return s.makeHousehold(t, ctx, eu.ID, household.RoleOwner),
		s.makeHousehold(t, ctx, outro.ID, household.RoleOwner)
}

// makeAccount insere uma conta pronta na casa informada.
func (s *store) makeAccount(t *testing.T, ctx context.Context, householdID, name string) *account.Account {
	t.Helper()

	nome, norm, err := account.NormalizeName(name)
	require.NoError(t, err)

	a := &account.Account{
		ID:                  s.nextID("a"),
		HouseholdID:         householdID,
		Name:                nome,
		NameNorm:            norm,
		Kind:                account.KindChecking,
		OpeningBalanceCents: 10_000,
		OpeningDate:         civil.MustNew(2026, 1, 1),
		CreatedAt:           now(),
		UpdatedAt:           now(),
	}
	require.NoError(t, s.accounts.Create(ctx, a))
	return a
}

// makeCategory insere uma categoria (grupo se parentID for nil).
func (s *store) makeCategory(t *testing.T, ctx context.Context, householdID, name, kind string, parentID *string) *category.Category {
	t.Helper()

	nome, norm, err := category.NormalizeName(name)
	require.NoError(t, err)

	c := &category.Category{
		ID:          s.nextID("c"),
		HouseholdID: householdID,
		ParentID:    parentID,
		Name:        nome,
		NameNorm:    norm,
		Kind:        kind,
		CreatedAt:   now(),
		UpdatedAt:   now(),
	}
	require.NoError(t, s.categories.Create(ctx, c))
	return c
}

// txSpec descreve o lançamento que o teste quer, com defaults sãos: o teste
// só preenche o que é o assunto DELE, e quem lê não precisa garimpar qual
// campo importa no meio de vinte iguais.
type txSpec struct {
	Kind            string
	AmountCents     int64
	OccurredOn      civil.Date
	CompetenceMonth string
	Description     string
	CategoryID      *string
	DedupKey        string
	DedupOrdinal    int
	StatementID     *string
	ImportBatchID   *string
	TransferGroupID *string
	// ExternalID é a chave natural que o documento trouxe. Anulável: só a
	// linha importada de um banco que numera as transações tem uma.
	ExternalID *string
}

// makeTransaction insere um lançamento pronto na casa e na conta informadas.
func (s *store) makeTransaction(t *testing.T, ctx context.Context, householdID, accountID string, spec txSpec) *transaction.Transaction {
	t.Helper()

	if spec.Kind == "" {
		spec.Kind = transaction.KindExpense
	}
	if spec.AmountCents == 0 {
		spec.AmountCents = 1_000
	}
	if spec.OccurredOn.IsZero() {
		spec.OccurredOn = civil.MustNew(2026, 2, 10)
	}
	if spec.CompetenceMonth == "" {
		spec.CompetenceMonth = spec.OccurredOn.YearMonth()
	}
	if spec.Description == "" {
		spec.Description = "Mercado do Bairro"
	}
	if spec.DedupKey == "" {
		spec.DedupKey = s.nextID("dk")
	}
	if spec.DedupOrdinal == 0 {
		spec.DedupOrdinal = 1
	}

	tx := transaction.Transaction{
		ID:              s.nextID("t"),
		HouseholdID:     householdID,
		Kind:            spec.Kind,
		AccountID:       accountID,
		CategoryID:      spec.CategoryID,
		AmountCents:     spec.AmountCents,
		Description:     spec.Description,
		DescriptionNorm: textnorm.Normalize(spec.Description),
		OccurredOn:      spec.OccurredOn,
		CompetenceMonth: spec.CompetenceMonth,
		TransferGroupID: spec.TransferGroupID,
		StatementID:     spec.StatementID,
		Source:          transaction.SourceManual,
		ImportBatchID:   spec.ImportBatchID,
		ExternalID:      spec.ExternalID,
		DedupKey:        spec.DedupKey,
		DedupOrdinal:    spec.DedupOrdinal,
		CreatedBy:       s.nextID("u"),
		CreatedAt:       now(),
		UpdatedAt:       now(),
	}
	require.NoError(t, s.transactions.CreateBatch(ctx, householdID, []transaction.Transaction{tx}))
	return &tx
}

// --- espião de SQL ----------------------------------------------------------

// comandoSQL é UM comando que o repositório mandou ao banco.
type comandoSQL struct {
	// SQL é o texto EXATO que foi executado, com os `?` ainda no lugar.
	SQL string
	// Parametros é quantos valores viajaram ligados a ele. É o número que o
	// teto de parâmetros por comando limita (2100 no SQL Server, 999
	// historicamente no SQLite), e por isso ele é medido, e não estimado.
	Parametros int
}

// sqlSpy grava os comandos emitidos entre um reset e a asserção.
//
// Existe porque há uma classe de teste que o resultado não alcança: "esta
// consulta NÃO aconteceu". Uma casa sem categoria de investimento tem de
// responder zeros sem ir ao banco (ADR-029f), e um teste que só conferisse o
// zero passaria igual se a consulta tivesse rodado e voltado vazia — que é
// justamente o caso em que `IN ()` seria emitido.
type sqlSpy struct {
	mu         sync.Mutex
	comandos   []comandoSQL
	capturando bool
}

func (s *sqlSpy) registrar(db *gorm.DB) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.capturando {
		return
	}
	s.comandos = append(s.comandos, comandoSQL{
		SQL:        db.Statement.SQL.String(),
		Parametros: len(db.Statement.Vars),
	})
}

// ligar começa (ou recomeça) a captura, descartando o que veio antes — o
// preparo do cenário não interessa, só a chamada sob teste.
func (s *sqlSpy) ligar() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.comandos = nil
	s.capturando = true
}

// comandosEmitidos devolve o que foi capturado até agora.
func (s *sqlSpy) comandosEmitidos() []comandoSQL {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]comandoSQL, len(s.comandos))
	copy(out, s.comandos)
	return out
}

// espiarSQL pendura o espião nos callbacks do GORM e o devolve DESLIGADO.
//
// Os três callbacks são necessários e nenhum é redundante: `Find` passa por
// Query, `Scan` (toda agregação deste repositório) passa por Row, e `Updates`
// passa por Update. Espiar só um deixaria de fora exatamente a consulta que o
// teste quer provar que não aconteceu.
func (s *store) espiarSQL(t *testing.T) *sqlSpy {
	t.Helper()

	spy := &sqlSpy{}
	g := s.db.Gorm()
	require.NoError(t, g.Callback().Query().After("gorm:query").Register("spy:query", spy.registrar))
	require.NoError(t, g.Callback().Row().After("gorm:row").Register("spy:row", spy.registrar))
	require.NoError(t, g.Callback().Update().After("gorm:update").Register("spy:update", spy.registrar))
	t.Cleanup(func() {
		_ = g.Callback().Query().Remove("spy:query")
		_ = g.Callback().Row().Remove("spy:row")
		_ = g.Callback().Update().Remove("spy:update")
	})
	return spy
}
