package aiimport_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/aiimport"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/require"
)

// Harness de integração do import de IA.
//
// Os testes de serviço rodam contra SQLite REAL, e não contra dublês, porque
// as garantias desta fatia moram no BANCO e nos SERVIÇOS reais: a transação
// única (grupos → folhas → palavras), o índice único das palavras, o teto de
// 200 do category.Service.Create, o `category.created` que ele grava, o
// `podeReceberPalavras` que AppendKeywords reconfere. Um dublê responderia "ok"
// a tudo isso e o teste passaria enquanto a garantia não existisse.
//
// O pacote é `aiimport_test` (teste externo), o que lhe permite importar
// gormstore.

const (
	mesInicial = "2026-07"
	mesFinal   = "2026-09"
)

type ambiente struct {
	db *storage.DB

	categorias *gormstore.CategoryRepository
	contas     *gormstore.AccountRepository
	razao      *gormstore.TransactionRepository
	uow        *gormstore.UnitOfWork

	categoriaSvc *category.Service
	contaSvc     *account.Service
	lancamentos  *transaction.Service

	svc     *aiimport.Service
	handler *aiimport.Handler

	auditoria *auditoriaFake
	logs      *bytes.Buffer
	momento   time.Time

	casa, alheia *household.Household
	usuario      string
	seq          int
}

// eventoAuditoria é uma entrada registrada — sem palavra-chave nenhuma, que
// é justamente o que o teste confere.
type eventoAuditoria struct {
	Action, Entity, EntityID, UserID, HouseholdID, IP string
}

type auditoriaFake struct {
	mu       sync.Mutex
	entradas []eventoAuditoria
}

func (a *auditoriaFake) registrar(e eventoAuditoria) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entradas = append(a.entradas, e)
}

func (a *auditoriaFake) Record(_ context.Context, p aiimport.AuditParams) error {
	a.registrar(eventoAuditoria(p))
	return nil
}

func (a *auditoriaFake) acoes() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.entradas))
	for _, e := range a.entradas {
		out = append(out, e.Action)
	}
	sort.Strings(out)
	return out
}

func (a *auditoriaFake) zerar() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.entradas = nil
}

type auditoriaCategoria struct{ alvo *auditoriaFake }

func (a auditoriaCategoria) Record(_ context.Context, p category.AuditParams) error {
	a.alvo.registrar(eventoAuditoria(p))
	return nil
}

type auditoriaConta struct{ alvo *auditoriaFake }

func (a auditoriaConta) Record(_ context.Context, p account.AuditParams) error {
	a.alvo.registrar(eventoAuditoria(p))
	return nil
}

// escritorDeCategoria embute o serviço REAL de categoria e permite forçar
// uma falha na N-ésima criação — é o que prova que a transação é uma só
// (critério 22): a primeira categoria já foi gravada quando a segunda falha,
// e o banco tem de voltar ao que era.
type escritorDeCategoria struct {
	*category.Service
	mu        sync.Mutex
	criacoes  int
	falharNa  int
	erroForca error

	// entradas guarda CADA CreateInput recebido, na ordem: é a prova de que
	// o import cria a folha com Keywords nil (spec 0010 §10.4 — todas as
	// palavras entram por SetKeywords, um caminho só).
	entradas []category.CreateInput
}

func (e *escritorDeCategoria) Create(ctx context.Context, ator category.Actor, in category.CreateInput) (category.View, error) {
	e.mu.Lock()
	e.criacoes++
	n := e.criacoes
	e.entradas = append(e.entradas, in)
	e.mu.Unlock()
	if e.falharNa > 0 && n == e.falharNa {
		return category.View{}, e.erroForca
	}
	return e.Service.Create(ctx, ator, in)
}

// novoAmbiente monta a pilha REAL: os mesmos repositórios, o mesmo
// UnitOfWork e os mesmos serviços que cmd/api liga.
func novoAmbiente(t *testing.T, opts ...aiimport.Option) *ambiente {
	t.Helper()
	return novoAmbienteComEscritor(t, nil, opts...)
}

func novoAmbienteComEscritor(t *testing.T, escritor *escritorDeCategoria, opts ...aiimport.Option) *ambiente {
	t.Helper()

	ctx := t.Context()
	db, err := storage.Open(ctx, storage.Options{
		Driver: storage.DriverSQLite,
		DSN:    filepath.Join(t.TempDir(), "aiimport-test.db"),
		// SQLite em arquivo e UMA conexão: serializa e evita "database is
		// locked" na transação do confirm, que reentra pelo Create.
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}, logging.Discard())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))

	a := &ambiente{
		db:         db,
		categorias: gormstore.NewCategoryRepository(db),
		contas:     gormstore.NewAccountRepository(db),
		razao:      gormstore.NewTransactionRepository(db),
		uow:        gormstore.NewUnitOfWork(db),
		auditoria:  &auditoriaFake{},
		logs:       &bytes.Buffer{},
		momento:    time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC),
	}
	lg := logging.New(a.logs, logging.Options{Level: "debug", Format: "json"})
	relogio := func() time.Time { return a.momento }

	casas := gormstore.NewHouseholdRepository(db)
	vinculos := gormstore.NewMembershipRepository(db)
	usuarios := gormstore.NewUserRepository(db)
	faturas := gormstore.NewCardStatementRepository(db)
	householdSvc := household.NewService(casas, vinculos)

	a.categoriaSvc = category.NewService(a.categorias, a.uow,
		category.WithAudit(auditoriaCategoria{alvo: a.auditoria}),
		category.WithClock(relogio),
		category.WithKeywordAppender(a.categorias),
	)
	a.contaSvc = account.NewService(a.contas, householdSvc, a.uow,
		account.WithAudit(auditoriaConta{alvo: a.auditoria}),
		account.WithBalances(a.razao),
		account.WithClock(relogio),
		account.WithKeywordAppender(a.contas),
	)
	a.lancamentos = transaction.NewService(a.razao, a.contas, a.categorias, faturas, a.uow,
		classify.NewLoader(a.categorias, a.contas),
		transaction.WithClock(relogio),
	)

	var escritorCat aiimport.CategoryWriter = a.categoriaSvc
	if escritor != nil {
		escritor.Service = a.categoriaSvc
		escritorCat = escritor
	}
	a.svc = aiimport.NewService(a.categorias, escritorCat, a.contas, a.contaSvc, a.razao, a.uow, lg,
		append([]aiimport.Option{aiimport.WithAudit(a.auditoria)}, opts...)...)
	a.handler = aiimport.NewHandler(a.svc, lg, 0)

	a.usuario = a.criarUsuario(t, usuarios)
	outro := a.criarUsuario(t, usuarios)
	a.casa = a.criarCasa(t, casas, vinculos, a.usuario)
	a.alheia = a.criarCasa(t, casas, vinculos, outro)
	return a
}

func (a *ambiente) proximoID(prefixo string) string {
	a.seq++
	return fmt.Sprintf("00000000-0000-7000-%s00-%012d", prefixo, a.seq)
}

func (a *ambiente) criarUsuario(t *testing.T, repo *gormstore.UserRepository) string {
	t.Helper()
	id := a.proximoID("80")
	require.NoError(t, repo.Create(t.Context(), &user.User{
		ID: id, Email: fmt.Sprintf("pessoa%d@exemplo.test", a.seq), Name: "Pessoa de Teste",
		PasswordHash:    "$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0c2E$aGFzaGhhc2hoYXNoaGFzaGhhc2hoYXNoaGFzaA",
		EmailVerifiedAt: &a.momento, CreatedAt: a.momento, UpdatedAt: a.momento,
	}))
	return id
}

func (a *ambiente) criarCasa(t *testing.T, casas *gormstore.HouseholdRepository, vinculos *gormstore.MembershipRepository, userID string) *household.Household {
	t.Helper()
	h := &household.Household{
		ID: a.proximoID("90"), Name: "Casa de Teste",
		Timezone: household.DefaultTimezone, Currency: household.DefaultCurrency,
		CreatedAt: a.momento, UpdatedAt: a.momento,
	}
	require.NoError(t, casas.Create(t.Context(), h))
	require.NoError(t, vinculos.Create(t.Context(), &household.Membership{
		ID: a.proximoID("a0"), HouseholdID: h.ID, UserID: userID, Role: session.RoleOwner,
		CreatedAt: a.momento, UpdatedAt: a.momento,
	}))
	return h
}

func (a *ambiente) ator() aiimport.Actor {
	return aiimport.Actor{HouseholdID: a.casa.ID, UserID: a.usuario, IP: "203.0.113.7"}
}

func (a *ambiente) atorCategoria() category.Actor {
	return category.Actor{HouseholdID: a.casa.ID, UserID: a.usuario, IP: "203.0.113.7"}
}

func (a *ambiente) atorConta() account.Actor {
	return account.Actor{HouseholdID: a.casa.ID, UserID: a.usuario, IP: "203.0.113.7"}
}

// --- montagem de dado -----------------------------------------------------------

// grupo cria um grupo pela porta da frente (o serviço), com palavras.
func (a *ambiente) grupo(t *testing.T, casa, nome, kind string, kws ...string) category.View {
	t.Helper()
	v, err := a.categoriaSvc.Create(t.Context(), category.Actor{HouseholdID: casa, UserID: a.usuario},
		category.CreateInput{Name: nome, Kind: kind, Keywords: kws})
	require.NoError(t, err)
	return v
}

// folha cria uma subcategoria pela porta da frente.
func (a *ambiente) folha(t *testing.T, casa, pai, nome string, kws ...string) category.View {
	t.Helper()
	v, err := a.categoriaSvc.Create(t.Context(), category.Actor{HouseholdID: casa, UserID: a.usuario},
		category.CreateInput{Name: nome, ParentID: &pai, Keywords: kws})
	require.NoError(t, err)
	return v
}

func (a *ambiente) arquivar(t *testing.T, casa, id string) {
	t.Helper()
	_, err := a.categoriaSvc.Archive(t.Context(), category.Actor{HouseholdID: casa, UserID: a.usuario}, id)
	require.NoError(t, err)
}

// conta grava uma conta direto no repositório (conta não nasce pelo import).
func (a *ambiente) conta(t *testing.T, casa, nome string, kws ...string) string {
	t.Helper()
	nomeOK, norm, err := account.NormalizeName(nome)
	require.NoError(t, err)
	c := &account.Account{
		ID: a.proximoID("b0"), HouseholdID: casa, Name: nomeOK, NameNorm: norm,
		Kind: account.KindChecking, Institution: account.InstitutionOther,
		OpeningDate: civil.MustNew(2026, 1, 1), CreatedAt: a.momento, UpdatedAt: a.momento,
	}
	require.NoError(t, a.contas.Create(t.Context(), c))
	if len(kws) > 0 {
		_, err := a.contaSvc.Update(t.Context(), account.Actor{HouseholdID: casa, UserID: a.usuario}, c.ID,
			account.UpdateInput{Keywords: &kws})
		require.NoError(t, err)
	}
	return c.ID
}

// lancar grava UM lançamento pela porta da frente, na competência do mês
// informado.
func (a *ambiente) lancar(t *testing.T, casa, kind, contaID, descricao string, mes string) {
	t.Helper()
	a.seq++
	var ano, m int
	_, err := fmt.Sscanf(mes, "%d-%d", &ano, &m)
	require.NoError(t, err)
	_, err = a.lancamentos.CreateBatch(t.Context(),
		transaction.Actor{HouseholdID: casa, UserID: a.usuario, IP: "203.0.113.7"},
		transaction.CreateBatchInput{
			Source: transaction.SourceManual,
			Rows: []transaction.NewTransaction{{
				Kind: kind, AccountID: contaID, AmountCents: 1000 + int64(a.seq),
				Description: descricao, OccurredOn: civil.MustNew(ano, m, 10),
				DedupKey: chaveDedup(fmt.Sprintf("%s|%s|%d", casa, descricao, a.seq)),
			}},
		})
	require.NoError(t, err)
}

func chaveDedup(semente string) string {
	soma := sha256.Sum256([]byte(semente))
	return hex.EncodeToString(soma[:])
}

// --- leitura do banco (a prova é relendo) ------------------------------------------

// retrato é o estado das categorias e palavras da casa, comparável por
// igualdade: é com ele que "nada foi gravado" e "banco inalterado" são
// provados.
type retrato struct {
	categorias []string // "id|nome|kind|pai|arquivada"
	palavras   []string // "dona|palavra|norm|posicao"
	deConta    []string
}

func (a *ambiente) retrato(t *testing.T, casa string) retrato {
	t.Helper()
	ctx := t.Context()
	var r retrato
	cats, err := a.categorias.List(ctx, casa, true)
	require.NoError(t, err)
	for _, c := range cats {
		pai := ""
		if c.ParentID != nil {
			pai = *c.ParentID
		}
		r.categorias = append(r.categorias, fmt.Sprintf("%s|%s|%s|%s|%t", c.ID, c.Name, c.Kind, pai, c.ArchivedAt != nil))
	}
	kws, err := a.categorias.ListKeywords(ctx, casa)
	require.NoError(t, err)
	for _, k := range kws {
		r.palavras = append(r.palavras, fmt.Sprintf("%s|%s|%s|%d", k.CategoryID, k.Keyword, k.Norm, k.Position))
	}
	akws, err := a.contas.ListKeywords(ctx, casa)
	require.NoError(t, err)
	for _, k := range akws {
		r.deConta = append(r.deConta, fmt.Sprintf("%s|%s|%s|%d", k.AccountID, k.Keyword, k.Norm, k.Position))
	}
	sort.Strings(r.categorias)
	sort.Strings(r.palavras)
	sort.Strings(r.deConta)
	return r
}

// palavrasDe devolve as palavras de uma categoria, na ordem de cadastro.
func (a *ambiente) palavrasDe(t *testing.T, casa, categoriaID string) []string {
	t.Helper()
	v, err := a.categoriaSvc.Get(t.Context(), category.Actor{HouseholdID: casa, UserID: a.usuario}, categoriaID)
	require.NoError(t, err)
	return v.Keywords
}

func (a *ambiente) palavrasDeConta(t *testing.T, casa, contaID string) []string {
	t.Helper()
	v, err := a.contaSvc.Get(t.Context(), account.Actor{HouseholdID: casa, UserID: a.usuario}, contaID)
	require.NoError(t, err)
	return v.Keywords
}

// categoriaPorCaminho acha a categoria ATIVA pelo caminho normalizado, ou
// devolve nil.
func (a *ambiente) categoriaPorCaminho(t *testing.T, casa, caminho string) *category.Category {
	t.Helper()
	cats, err := a.categorias.List(t.Context(), casa, true)
	require.NoError(t, err)
	porID := map[string]category.Category{}
	for _, c := range cats {
		porID[c.ID] = c
	}
	for _, c := range cats {
		if c.ArchivedAt != nil {
			continue
		}
		p := c.NameNorm
		if c.ParentID != nil {
			p = porID[*c.ParentID].NameNorm + " > " + c.NameNorm
		}
		if p == textnorm.Normalize(caminho) {
			copia := c
			return &copia
		}
	}
	return nil
}

// --- payload --------------------------------------------------------------------------

func versao() *int64 { v := aiimport.FormatVersion; return &v }

func entrada(id, caminho string, add ...string) aiimport.CategoryKeywordEntry {
	return aiimport.CategoryKeywordEntry{CategoryID: id, CategoryPath: caminho, Add: add}
}

func deConta(id, nome string, add ...string) aiimport.AccountKeywordEntry {
	return aiimport.AccountKeywordEntry{AccountID: id, AccountName: nome, Add: add}
}

func nova(grupo, nome string, kind *string, add ...string) aiimport.NewCategoryEntry {
	return aiimport.NewCategoryEntry{Group: grupo, Name: nome, Kind: kind, Add: add}
}

func ptr(s string) *string { return &s }

func input(pl aiimport.Payload, pular ...string) aiimport.Input {
	pl.Version = versao()
	return aiimport.Input{FromMonth: mesInicial, ToMonth: mesFinal, SkipNewCategories: pular, Payload: pl}
}

func motivos(rej []aiimport.RejectedKeyword) map[string]string {
	out := map[string]string{}
	for _, r := range rej {
		out[r.Keyword] = r.Reason
	}
	return out
}
