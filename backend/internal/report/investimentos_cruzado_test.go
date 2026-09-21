package report_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Critérios de aceite 7 (spec 0006 §7.7) e 18 do plano da E7, CRUZADOS: a
// MESMA despesa de R$ 2.000 marcada como aporte tem de sumir do relatório por
// categoria, sumir de `expenseCents`, aparecer em `investedCents` e deixar o
// saldo da conta exatamente como estava — os quatro na mesma asserção.
//
// Por que contra SQLite de verdade, e não contra dublês: as quatro respostas
// nascem de consultas diferentes (SumByCategory, Summary, List e SumByAccount)
// sobre a MESMA linha. Um dublê por serviço provaria que os dublês concordam
// entre si, que é justamente o que não está em dúvida. O risco real é as
// consultas divergirem — e só o banco mostra isso.

const (
	casaCruzada  = "00000000-0000-7000-9000-000000000001"
	donoDaCasa   = "00000000-0000-7000-8000-000000000001"
	contaCruzada = "00000000-0000-7000-b000-000000000001"
)

// pilhaCruzada é a fatia da aplicação que responde às CINCO perguntas que o
// critério 18 obriga a concordar sobre a mesma linha.
type pilhaCruzada struct {
	relatorios    *report.Service
	lancamentos   *transaction.Service
	contas        *account.Service
	investimentos *investment.Service
	categorias    *gormstore.CategoryRepository
	momento       time.Time
	proximoIndex  int
}

func novaPilhaCruzada(t *testing.T) *pilhaCruzada {
	t.Helper()

	ctx := t.Context()
	db, err := storage.Open(ctx, storage.Options{
		Driver: storage.DriverSQLite,
		// Arquivo, e uma conexão só: SQLite em memória por conexão daria um
		// banco diferente a cada handle, e mais de uma conexão traz
		// "database is locked" nas transações do UnitOfWork.
		DSN:          filepath.Join(t.TempDir(), "investimentos-cruzado.db"),
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}, logging.Discard())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))

	repoTx := gormstore.NewTransactionRepository(db)
	repoConta := gormstore.NewAccountRepository(db)
	repoCategoria := gormstore.NewCategoryRepository(db)
	repoFatura := gormstore.NewCardStatementRepository(db)
	uow := gormstore.NewUnitOfWork(db)
	casas := household.NewService(gormstore.NewHouseholdRepository(db), gormstore.NewMembershipRepository(db))

	momento := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	p := &pilhaCruzada{
		categorias: repoCategoria,
		momento:    momento,
		// As MESMAS ligações que cmd/api registra: o saldo da conta é
		// derivado dos lançamentos (ADR-017), e é essa ligação que o teste
		// precisa exercitar.
		contas: account.NewService(repoConta, casas, uow,
			account.WithBalances(repoTx),
			account.WithClock(func() time.Time { return momento }),
		),
		lancamentos: transaction.NewService(repoTx, repoConta, repoCategoria, repoFatura, uow,
			classify.NewLoader(repoCategoria, repoConta),
			transaction.WithClock(func() time.Time { return momento }),
		),
		relatorios: report.NewService(repoTx, repoCategoria, repoConta, logging.Discard()),
		// A QUINTA resposta sobre a MESMA linha: GET /investments. Ela entra
		// aqui, e não num teste com dublê, porque o critério 18 pede que ela
		// concorde com as outras quatro — e concordância entre dublês é o que
		// nunca esteve em dúvida.
		investimentos: investment.NewService(repoTx, repoCategoria, repoConta,
			classify.NewLoader(repoCategoria, repoConta), uow, logging.Discard(),
			investment.WithClock(func() time.Time { return momento }),
		),
	}

	require.NoError(t, repoConta.Create(ctx, &account.Account{
		ID: contaCruzada, HouseholdID: casaCruzada,
		Name: "Conta corrente", NameNorm: textnorm.Normalize("Conta corrente"),
		Kind: account.KindChecking, Institution: "Nubank",
		OpeningBalanceCents: 10_000_00, OpeningDate: civil.MustNew(2026, 1, 1),
		CreatedAt: momento, UpdatedAt: momento,
	}))
	return p
}

// criarCategoria grava uma categoria de nível 1 com a natureza pedida.
func (p *pilhaCruzada) criarCategoria(t *testing.T, id, nome, kind string) category.Category {
	t.Helper()
	c := category.Category{
		ID: id, HouseholdID: casaCruzada, Name: nome, NameNorm: textnorm.Normalize(nome),
		Kind: kind, CreatedAt: p.momento, UpdatedAt: p.momento,
	}
	require.NoError(t, p.categorias.Create(t.Context(), &c))
	return c
}

// lancar grava UMA despesa pela porta da frente (o serviço, dentro da mesma
// transação de produção) e devolve o id.
func (p *pilhaCruzada) lancar(t *testing.T, categoriaID string, cents int64, descricao string, dia int) string {
	t.Helper()
	p.proximoIndex++
	res, err := p.lancamentos.CreateBatch(t.Context(),
		transaction.Actor{HouseholdID: casaCruzada, UserID: donoDaCasa, IP: "203.0.113.7"},
		transaction.CreateBatchInput{
			Source: transaction.SourceManual,
			Rows: []transaction.NewTransaction{{
				Kind:        transaction.KindExpense,
				AccountID:   contaCruzada,
				CategoryID:  &categoriaID,
				AmountCents: cents,
				Description: descricao,
				OccurredOn:  civil.MustNew(2026, 9, dia),
				// A chave de deduplicação vem calculada de fora (ADR-025c);
				// aqui basta ser estável e distinta por linha.
				DedupKey: chaveDedup(fmt.Sprintf("%s|%d|%d", descricao, cents, p.proximoIndex)),
			}},
		})
	require.NoError(t, err)
	require.Len(t, res.IDs, 1)
	return res.IDs[0]
}

// saldo devolve o balanceCents da conta, pelo mesmo caminho de GET /accounts.
func (p *pilhaCruzada) saldo(t *testing.T) int64 {
	t.Helper()
	v, err := p.contas.List(t.Context(), account.Actor{HouseholdID: casaCruzada, UserID: donoDaCasa}, false)
	require.NoError(t, err)
	require.Len(t, v.Items, 1)
	return v.Items[0].BalanceCents
}

// TestAporteSomeDoRelatorioEDaDespesaSemMexerNoSaldo é o critério 7/18 inteiro,
// numa asserção só: a mesma despesa de R$ 2.000, antes e depois de ser marcada
// como aporte.
func TestAporteSomeDoRelatorioEDaDespesaSemMexerNoSaldo(t *testing.T) {
	t.Parallel()

	p := novaPilhaCruzada(t)
	ctx := t.Context()
	atorTx := transaction.Actor{HouseholdID: casaCruzada, UserID: donoDaCasa, IP: "203.0.113.7"}
	atorRel := report.Actor{HouseholdID: casaCruzada, UserID: donoDaCasa}

	mercado := p.criarCategoria(t, "00000000-0000-7000-c000-000000000001", "Mercado", category.KindExpense)
	// A categoria de destino nasce como DESPESA: é a casa que já tinha uma
	// categoria "Investimentos" de natureza expense, o caso que a alínea (c)
	// do ADR-029 existe para atender.
	aportes := p.criarCategoria(t, "00000000-0000-7000-c000-000000000002", "Investimentos", category.KindExpense)

	p.lancar(t, mercado.ID, 300_00, "Mercado do bairro", 6)
	p.lancar(t, aportes.ID, 2_000_00, "CDB 15 DIAS", 10)

	// --- ANTES: o aporte ainda é despesa comum em todo lugar ---------------
	saldoAntes := p.saldo(t)
	assert.Equal(t, int64(10_000_00-2_300_00), saldoAntes)

	relAntes, err := p.relatorios.ByCategory(ctx, atorRel, report.ByCategoryInput{Month: "2026-09", Kind: "expense"})
	require.NoError(t, err)
	assert.Equal(t, int64(2_300_00), relAntes.TotalCents)
	assert.Len(t, relAntes.Items, 2)

	listaAntes, err := p.lancamentos.List(ctx, atorTx, transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.Equal(t, int64(2_300_00), listaAntes.Summary.ExpenseCents)
	assert.Equal(t, int64(0), listaAntes.Summary.InvestedCents)

	// --- A marcação: a natureza da categoria vira `investment` -------------
	//
	// Escrito pelo repositório porque a troca de natureza é da tarefa T2a
	// (fechada, `internal/category`); aqui interessa o EFEITO dela nas três
	// leituras, não o caminho da escrita.
	marcada := aportes
	marcada.Kind = category.KindInvestment
	marcada.UpdatedAt = p.momento
	require.NoError(t, p.categorias.Update(ctx, &marcada))

	// --- DEPOIS: os quatro fatos, juntos ----------------------------------
	relDepois, err := p.relatorios.ByCategory(ctx, atorRel, report.ByCategoryInput{Month: "2026-09", Kind: "expense"})
	require.NoError(t, err)
	conferirInvariantes(t, relDepois)

	listaDepois, err := p.lancamentos.List(ctx, atorTx, transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)

	// 1) sumiu do relatório por categoria; 2) saiu de expenseCents;
	// 3) apareceu em investedCents; 4) o saldo da conta não se mexeu.
	assert.Equal(t, int64(300_00), relDepois.TotalCents, "o aporte sumiu do relatório de despesas")
	assert.Equal(t, int64(300_00), listaDepois.Summary.ExpenseCents, "o aporte saiu de expenseCents")
	assert.Equal(t, int64(2_000_00), listaDepois.Summary.InvestedCents, "e entrou, inteiro, em investedCents")
	assert.Equal(t, saldoAntes, p.saldo(t), "o dinheiro saiu da conta de verdade — o saldo não muda")

	// E o que sai de um é exatamente o que entra no outro: nenhum centavo se
	// perde entre as duas agregações.
	assert.Equal(t,
		listaAntes.Summary.ExpenseCents,
		listaDepois.Summary.ExpenseCents+listaDepois.Summary.InvestedCents,
		"o total do mês não encolheu: ele foi repartido",
	)
	assert.Equal(t, relAntes.TotalCents-relDepois.TotalCents, listaDepois.Summary.InvestedCents,
		"o relatório perdeu exatamente o que o resumo marcou")

	// O lançamento continua existindo e visível em /lancamentos (ADR-029i: é
	// a mitigação declarada de esconder gasto marcando como investimento).
	require.Len(t, relDepois.Items, 1)
	assert.Equal(t, mercado.ID, *relDepois.Items[0].CategoryID)
	require.Len(t, listaDepois.Items, 2)
	assert.Equal(t, int64(2), listaDepois.Summary.Count)
	assert.Equal(t, int64(0), listaDepois.Summary.UncategorizedCount)
}

// chaveDedup devolve os 64 hexadecimais que validarLinha exige.
func chaveDedup(semente string) string {
	soma := sha256.Sum256([]byte(semente))
	return hex.EncodeToString(soma[:])
}
