package importer_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/nubank"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/require"
)

// Harness de integração da importação.
//
// O teste do serviço de importação roda contra SQLite REAL, e não contra
// dublês, por um motivo específico: metade das garantias desta entrega mora no
// BANCO — o índice único (household_id, dedup_key, dedup_ordinal), a transição
// condicional do lote e o upsert idempotente da fatura. Um dublê de repositório
// responderia "ok" a todos os três e o teste passaria enquanto a garantia não
// existisse.
//
// O pacote é `importer_test` (teste externo), o que lhe permite importar
// gormstore — que importa importer. Em teste externo isso não é ciclo.

// ambiente reúne tudo o que um teste de importação precisa.
type ambiente struct {
	db *storage.DB

	repoImport *gormstore.ImportRepository
	repoTx     *gormstore.TransactionRepository
	repoConta  *gormstore.AccountRepository
	repoFatura *gormstore.CardStatementRepository

	// contas é o repositório de contas VISTO PELOS SERVIÇOS (importação,
	// lançamentos e faturas). É o repoConta embutido no dublê de collation
	// frouxa, desligado por padrão.
	contas *contasComColacaoFrouxa

	svc          *importer.Service
	txSvc        *transaction.Service
	stmtSvc      *cardstatement.Service
	contaSvc     *account.Service
	categoriaSvc *category.Service
	// classificador é o loader REAL sobre os repositórios de categoria e
	// conta deste banco — o mesmo que cmd/api liga. habilitarC6 o reutiliza.
	classificador *classify.Loader
	// contadorDeCategorias é o repositório de categorias visto PELO
	// classificador, com as consultas contadas. É o que permite provar que a
	// conferência das sugestões no confirm não vira uma consulta por linha
	// (spec 0005 §13).
	contadorDeCategorias *categoriasContadas
	auditoria            *auditoriaFake
	relogio              *relogioFake

	// casa e alheia são DUAS casas de donos diferentes: "dado da outra casa"
	// precisa ser um dado real, não um id inventado. Id inexistente também
	// responde 404, mas provar isso não prova isolamento.
	casa, alheia *household.Household
	usuario      *user.User
	outroUsuario *user.User

	seq atomic.Uint64
}

// categoriasContadas é o repositório REAL de categorias com as duas consultas
// do classificador contadas.
//
// Ele embute o repositório (e não o substitui) de propósito: o resto da
// interface — Children, ByID, Create — continua indo ao SQLite de verdade, e o
// que o teste mede é só quantas vezes o classificador foi carregado. Contador
// atômico porque o -race reclamaria de um int comum no confirm concorrente.
type categoriasContadas struct {
	*gormstore.CategoryRepository
	listas   atomic.Int64
	palavras atomic.Int64
}

func (c *categoriasContadas) List(ctx context.Context, householdID string, includeArchived bool) ([]category.Category, error) {
	c.listas.Add(1)
	return c.CategoryRepository.List(ctx, householdID, includeArchived)
}

func (c *categoriasContadas) ListKeywords(ctx context.Context, householdID string) ([]category.Keyword, error) {
	c.palavras.Add(1)
	return c.CategoryRepository.ListKeywords(ctx, householdID)
}

// zerar reinicia a contagem — o teste mede UMA operação, não a montagem.
func (c *categoriasContadas) zerar() {
	c.listas.Store(0)
	c.palavras.Store(0)
}

// carregamentos é quantas vezes o conjunto da casa foi montado (List +
// ListKeywords andam em par dentro de classify.Load).
func (c *categoriasContadas) carregamentos() (int64, int64) {
	return c.listas.Load(), c.palavras.Load()
}

// contasComColacaoFrouxa embute o repositório REAL de contas e, com `frouxa`
// ligada, imita o casamento que `WHERE id = ?` faz nos dialetos cuja COLLATION
// não é binária.
//
// Por que ele existe: as colunas de id são `varchar(36)` sem collation
// declarada e nada fixa charset na abertura da conexão, então o `=` do MySQL 8
// usa `utf8mb4_0900_ai_ci` (ignora CAIXA e acento) e o do MSSQL usa `CI_AS`
// com padding ANSI (ignora ESPAÇO À DIREITA). Este harness roda contra SQLite,
// onde o `=` é binário — ali o `ByID` responde 404 e a classe de defeito
// "gravei a string do cliente em vez do id canônico" NÃO REPRODUZ. Como
// testcontainers não está no go.mod e não há Docker nesta máquina, a collation
// frouxa é reproduzida aqui, no dublê, e não num dialeto.
//
// Ele EMBUTE o repositório (não o substitui): todo o resto da interface
// continua indo ao SQLite de verdade, e o único desvio é o casamento do ByID.
// Desligado por padrão — só o teste que mede esta classe o liga.
type contasComColacaoFrouxa struct {
	*gormstore.AccountRepository
	frouxa atomic.Bool
}

func (c *contasComColacaoFrouxa) ByID(ctx context.Context, householdID, id string) (*account.Account, error) {
	encontrada, err := c.AccountRepository.ByID(ctx, householdID, id)
	if err == nil || !c.frouxa.Load() || !errors.Is(err, account.ErrNotFound) {
		return encontrada, err
	}
	// O SQLite não casou; MySQL/MSSQL casariam. Repete a busca pela regra
	// FROUXA — sempre dentro da MESMA casa, porque a collation afrouxa a
	// comparação do id, nunca o escopo por household.
	lista, erroDaLista := c.AccountRepository.List(ctx, householdID, true)
	if erroDaLista != nil {
		return nil, erroDaLista
	}
	alvo := strings.TrimRight(id, " ")
	for i := range lista {
		if strings.EqualFold(alvo, lista[i].ID) {
			copia := lista[i]
			return &copia, nil
		}
	}
	return nil, err
}

// relogioFake é o relógio injetado. Ele existe para o TTL do lote e para o
// janitor poderem ser testados sem esperar 24 horas.
type relogioFake struct{ agora atomic.Pointer[time.Time] }

func novoRelogio(t time.Time) *relogioFake {
	r := &relogioFake{}
	r.agora.Store(&t)
	return r
}

func (r *relogioFake) now() time.Time { return *r.agora.Load() }

func (r *relogioFake) avancar(d time.Duration) {
	t := r.now().Add(d)
	r.agora.Store(&t)
}

// eventoAuditoria é uma entrada registrada, sem valor monetário nenhum — que é
// justamente o que o teste confere (S8).
type eventoAuditoria struct {
	Action, Entity, EntityID, UserID, HouseholdID, IP string
}

type auditoriaFake struct {
	// O mutex existe porque o teste de confirm concorrente escreve daqui de
	// duas goroutines — sem ele o -race acusaria o próprio dublê.
	mu       sync.Mutex
	entradas []eventoAuditoria
}

func (a *auditoriaFake) Record(_ context.Context, p importer.AuditParams) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entradas = append(a.entradas, eventoAuditoria(p))
	return nil
}

func (a *auditoriaFake) acoes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.entradas))
	for _, e := range a.entradas {
		out = append(out, e.Action)
	}
	return out
}

// auditoriaLancamento repassa as entradas AVULSAS de lançamento
// (transaction.deleted e transaction.restored) para o mesmo registro.
type auditoriaLancamento struct{ alvo *auditoriaFake }

func (a auditoriaLancamento) Record(ctx context.Context, p transaction.AuditParams) error {
	return a.alvo.Record(ctx, importer.AuditParams(p))
}

type auditoriaFatura struct{ alvo *auditoriaFake }

func (a auditoriaFatura) Record(ctx context.Context, p cardstatement.AuditParams) error {
	return a.alvo.Record(ctx, importer.AuditParams(p))
}

// novoAmbiente monta o ambiente completo. As opções extras são repassadas ao
// importer.Service: existem para o teste de volume poder afrouxar o
// AnalyzeTimeout, que é um guarda de RELÓGIO de produção e, sob contenção do
// `-race` com o pacote inteiro, transforma carga de máquina em vermelho.
func novoAmbiente(t *testing.T, opts ...importer.Option) *ambiente {
	t.Helper()

	ctx := t.Context()
	db, err := storage.Open(ctx, storage.Options{
		Driver: storage.DriverSQLite,
		DSN:    filepath.Join(t.TempDir(), "importacao-test.db"),
		// SQLite em arquivo: uma conexão serializa e evita "database is
		// locked" nas transações aninhadas do confirm.
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}, logging.Discard())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))

	a := &ambiente{
		db:         db,
		repoImport: gormstore.NewImportRepository(db),
		repoTx:     gormstore.NewTransactionRepository(db),
		repoConta:  gormstore.NewAccountRepository(db),
		repoFatura: gormstore.NewCardStatementRepository(db),
		auditoria:  &auditoriaFake{},
		relogio:    novoRelogio(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)),
	}

	a.contas = &contasComColacaoFrouxa{AccountRepository: a.repoConta}

	uow := gormstore.NewUnitOfWork(db)
	casas := gormstore.NewHouseholdRepository(db)
	vinculos := gormstore.NewMembershipRepository(db)
	usuarios := gormstore.NewUserRepository(db)
	categorias := gormstore.NewCategoryRepository(db)

	householdSvc := household.NewService(casas, vinculos)

	// O MESMO UsageChecker que o cmd/api registra, pelo mesmo motivo: é ele
	// que faz DELETE /accounts e DELETE /categories responderem 422 quando há
	// lançamento associado, e é a ligação real que os testes precisam
	// exercitar — um dublê aqui provaria só que o dublê funciona.
	usoEmLancamentos := transaction.NewUsageChecker(a.repoTx)
	a.categoriaSvc = category.NewService(categorias, uow,
		category.WithUsageCheckers(usoEmLancamentos),
		category.WithClock(a.relogio.now),
	)
	a.contaSvc = account.NewService(a.repoConta, householdSvc, uow,
		account.WithBalances(a.repoTx),
		account.WithUsageCheckers(usoEmLancamentos),
		account.WithClock(a.relogio.now),
	)

	a.contadorDeCategorias = &categoriasContadas{CategoryRepository: categorias}
	a.classificador = classify.NewLoader(a.contadorDeCategorias, a.repoConta)
	a.txSvc = transaction.NewService(a.repoTx, a.contas, categorias, a.repoFatura, uow, a.classificador,
		transaction.WithAudit(auditoriaLancamento{alvo: a.auditoria}),
		transaction.WithClock(a.relogio.now),
	)
	a.stmtSvc = cardstatement.NewService(a.repoFatura, a.contas, householdSvc,
		transaction.NewStatementTotals(a.repoTx), uow,
		cardstatement.WithAudit(auditoriaFatura{alvo: a.auditoria}),
		cardstatement.WithClock(a.relogio.now),
	)

	registro, err := importer.NewRegistry(nubank.NewChecking(), nubank.NewCard())
	require.NoError(t, err)

	a.svc = importer.NewService(a.repoImport, registro, a.contas, a.repoTx,
		a.txSvc, a.stmtSvc, uow, a.classificador,
		append([]importer.Option{
			importer.WithAudit(a.auditoria),
			importer.WithClock(a.relogio.now),
		}, opts...)...,
	)

	a.usuario = a.criarUsuario(t, ctx, usuarios)
	a.outroUsuario = a.criarUsuario(t, ctx, usuarios)
	a.casa = a.criarCasa(t, ctx, casas, vinculos, a.usuario.ID)
	a.alheia = a.criarCasa(t, ctx, casas, vinculos, a.outroUsuario.ID)
	return a
}

func (a *ambiente) proximoID(prefixo string) string {
	return fmt.Sprintf("%s-%012d", prefixo, a.seq.Add(1))
}

func (a *ambiente) criarUsuario(t *testing.T, ctx context.Context, repo *gormstore.UserRepository) *user.User {
	t.Helper()
	agora := a.relogio.now()
	u := &user.User{
		ID:              a.proximoID("00000000-0000-7000-8000"),
		Email:           fmt.Sprintf("pessoa%d@exemplo.test", a.seq.Load()),
		PasswordHash:    "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA",
		Name:            "Pessoa de Teste",
		EmailVerifiedAt: &agora,
		CreatedAt:       agora,
		UpdatedAt:       agora,
	}
	require.NoError(t, repo.Create(ctx, u))
	return u
}

func (a *ambiente) criarCasa(t *testing.T, ctx context.Context, casas *gormstore.HouseholdRepository, vinculos *gormstore.MembershipRepository, userID string) *household.Household {
	t.Helper()
	agora := a.relogio.now()
	h := &household.Household{
		ID:        a.proximoID("00000000-0000-7000-9000"),
		Name:      "Casa de Teste",
		Timezone:  household.DefaultTimezone,
		Currency:  household.DefaultCurrency,
		CreatedAt: agora,
		UpdatedAt: agora,
	}
	require.NoError(t, casas.Create(ctx, h))
	require.NoError(t, vinculos.Create(ctx, &household.Membership{
		ID:          a.proximoID("00000000-0000-7000-a000"),
		HouseholdID: h.ID,
		UserID:      userID,
		Role:        session.RoleOwner,
		CreatedAt:   agora,
		UpdatedAt:   agora,
	}))
	return h
}

// conta cria uma conta na casa, com instituição e tipo escolhidos.
func (a *ambiente) conta(t *testing.T, householdID, nome, kind, instituicao string) *account.Account {
	t.Helper()
	nomeLimpo, norm, err := account.NormalizeName(nome)
	require.NoError(t, err)

	agora := a.relogio.now()
	c := &account.Account{
		ID:                  a.proximoID("00000000-0000-7000-b000"),
		HouseholdID:         householdID,
		Name:                nomeLimpo,
		NameNorm:            norm,
		Kind:                kind,
		Institution:         instituicao,
		OpeningBalanceCents: 0,
		OpeningDate:         civil.MustNew(2026, 1, 1),
		CreatedAt:           agora,
		UpdatedAt:           agora,
	}
	require.NoError(t, a.repoConta.Create(t.Context(), c))
	return c
}

// ator monta o Actor da casa principal.
func (a *ambiente) ator() importer.Actor {
	return importer.Actor{HouseholdID: a.casa.ID, UserID: a.usuario.ID, IP: "203.0.113.7"}
}

// atorAlheio monta o Actor da OUTRA casa — é com ele que todo teste de BOLA
// tenta alcançar o que não é dele.
func (a *ambiente) atorAlheio() importer.Actor {
	return importer.Actor{HouseholdID: a.alheia.ID, UserID: a.outroUsuario.ID, IP: "203.0.113.8"}
}

// fixtureExtrato e fixtureFatura leem os arquivos anonimizados do parser.
//
// Reusar as fixtures do parser (em vez de escrever um CSV próprio aqui) é
// deliberado: são elas que carregam os dois casos que o arquivo real não tinha
// — o par de linhas idênticas com identificadores diferentes no extrato e o par
// COMPLETAMENTE idêntico "Cafe Exemplo" na fatura, que é o que exercita o
// ordinal (spec 0004 §2.3).
func fixtureExtrato(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("nubank", "testdata", "nubank_checking_v1.csv"))
	require.NoError(t, err)
	return b
}

func fixtureFatura(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("nubank", "testdata", "nubank_card_statement_v1.csv"))
	require.NoError(t, err)
	return b
}

// lancamentosDa devolve os lançamentos vivos da casa, ordenados por data.
func (a *ambiente) lancamentosDa(t *testing.T, householdID, month string) []transaction.Transaction {
	t.Helper()
	linhas, err := a.repoTx.List(t.Context(), householdID, transaction.ListFilter{
		CompetenceMonth: month,
		Limit:           transaction.MaxPageSize,
	})
	require.NoError(t, err)
	return linhas
}
