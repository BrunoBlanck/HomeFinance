package report_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/dashboard"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Teste CRUZADO do recorte `credit` (ADR-032): o total de
// `GET /reports/by-category?kind=expense&accountGroup=credit` tem de ser
// IDÊNTICO a `creditCardExpenseCents` de `GET /dashboard` no mesmo mês — e a
// contagem, a `creditCardExpenseCount`.
//
// # Por que contra SQLite de verdade, e não contra dublês
//
// Os dois números nascem de consultas DIFERENTES sobre as MESMAS linhas
// (SumByCategoryAndAccount e SumMonthByKindAndAccount) e de dois serviços que
// decidem separadamente o que é cartão e o que é aporte. Dublês provariam que
// os dublês concordam, que é o que nunca esteve em dúvida; o risco real é as
// duas leituras divergirem no conjunto de cartões (arquivado conta?), no
// descarte do aporte, na transferência de pagamento de fatura ou no
// tratamento do NULL de `category_id` — e só o banco mostra isso.
//
// A pilha é montada com as MESMAS ligações de `cmd/api/main.go`.
//
// Molde: internal/dashboard/cruzado_test.go e
// internal/report/investimentos_cruzado_test.go.

const (
	casaCredito  = "00000000-0000-7000-9000-0000000000c1"
	donoCredito  = "00000000-0000-7000-8000-0000000000c1"
	casaVizinha  = "00000000-0000-7000-9000-0000000000c2"
	donoVizinho  = "00000000-0000-7000-8000-0000000000c2"
	mesDoCredito = "2026-09"
)

// pilhaCredito é a fatia da aplicação que responde às DUAS perguntas que têm
// de concordar sobre o gasto do cartão no mês.
type pilhaCredito struct {
	relatorios  *report.Service
	painel      *dashboard.Service
	lancamentos *transaction.Service

	contas     *gormstore.AccountRepository
	categorias *gormstore.CategoryRepository

	momento time.Time
	seq     int
}

func novaPilhaCredito(t *testing.T) *pilhaCredito {
	t.Helper()

	ctx := t.Context()
	db, err := storage.Open(ctx, storage.Options{
		Driver: storage.DriverSQLite,
		// Arquivo, e uma conexão só: SQLite em memória por conexão daria um
		// banco diferente a cada handle, e mais de uma conexão traz
		// "database is locked" nas transações do UnitOfWork.
		DSN:          filepath.Join(t.TempDir(), "credito-cruzado.db"),
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
	momento := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	return &pilhaCredito{
		contas:     repoConta,
		categorias: repoCategoria,
		momento:    momento,
		lancamentos: transaction.NewService(repoTx, repoConta, repoCategoria, repoFatura, uow,
			classify.NewLoader(repoCategoria, repoConta),
			transaction.WithClock(func() time.Time { return momento }),
		),
		// Os dois serviços recebem EXATAMENTE os mesmos repositórios que o
		// main.go entrega a eles.
		relatorios: report.NewService(repoTx, repoCategoria, repoConta, logging.Discard()),
		painel:     dashboard.NewService(repoTx, repoCategoria, repoConta, logging.Discard()),
	}
}

func (p *pilhaCredito) proximo(prefixo string) string {
	p.seq++
	return fmt.Sprintf("00000000-0000-7000-%s000-%012d", prefixo, p.seq)
}

func (p *pilhaCredito) conta(t *testing.T, casa, nome, kind string, arquivada bool) string {
	t.Helper()
	nomeOK, norm, err := account.NormalizeName(nome)
	require.NoError(t, err)
	a := &account.Account{
		ID: p.proximo("b"), HouseholdID: casa,
		Name: nomeOK, NameNorm: norm, Kind: kind,
		Institution:         account.InstitutionOther,
		OpeningBalanceCents: 0, OpeningDate: civil.MustNew(2026, 1, 1),
		CreatedAt: p.momento, UpdatedAt: p.momento,
	}
	if arquivada {
		at := p.momento
		a.ArchivedAt = &at
	}
	require.NoError(t, p.contas.Create(t.Context(), a))
	return a.ID
}

// arquivar arquiva a conta DEPOIS de ela já ter lançamentos — que é a ordem
// da vida real e a única que o domínio aceita (lançar em conta arquivada é
// recusado pelo serviço).
func (p *pilhaCredito) arquivar(t *testing.T, casa, contaID string) {
	t.Helper()
	a, err := p.contas.ByID(t.Context(), casa, contaID)
	require.NoError(t, err)
	at := p.momento
	a.ArchivedAt = &at
	a.UpdatedAt = at
	require.NoError(t, p.contas.Update(t.Context(), a))
}

func (p *pilhaCredito) categoria(t *testing.T, casa, nome, kind string, parentID *string) string {
	t.Helper()
	c := category.Category{
		ID: p.proximo("c"), HouseholdID: casa, ParentID: parentID,
		Name: nome, NameNorm: textnorm.Normalize(nome), Kind: kind,
		CreatedAt: p.momento, UpdatedAt: p.momento,
	}
	require.NoError(t, p.categorias.Create(t.Context(), &c))
	return c.ID
}

// lancar grava UM lançamento pela porta da frente — o serviço, dentro da
// mesma transação de produção.
func (p *pilhaCredito) lancar(t *testing.T, casa, dono, kind, contaID string, categoriaID *string,
	cents int64, descricao string, dia int,
) {
	t.Helper()
	p.seq++
	_, err := p.lancamentos.CreateBatch(t.Context(),
		transaction.Actor{HouseholdID: casa, UserID: dono, IP: "203.0.113.7"},
		transaction.CreateBatchInput{
			Source: transaction.SourceManual,
			Rows: []transaction.NewTransaction{{
				Kind: kind, AccountID: contaID, CategoryID: categoriaID,
				AmountCents: cents, Description: descricao, OccurredOn: civil.MustNew(2026, 9, dia),
				DedupKey: chaveDedup(fmt.Sprintf("%s|%s|%d|%d", casa, descricao, cents, p.seq)),
			}},
		})
	require.NoError(t, err)
}

// transferir grava as DUAS pernas no mesmo lote e na mesma transação — a
// única forma que o domínio aceita (ADR-016).
func (p *pilhaCredito) transferir(t *testing.T, casa, dono, de, para string, cents int64, descricao string, dia int) {
	t.Helper()
	p.seq++
	grupo := fmt.Sprintf("grupo-%d", p.seq)
	_, err := p.lancamentos.CreateBatch(t.Context(),
		transaction.Actor{HouseholdID: casa, UserID: dono, IP: "203.0.113.7"},
		transaction.CreateBatchInput{
			Source: transaction.SourceManual,
			Rows: []transaction.NewTransaction{
				{
					Kind: transaction.KindTransferOut, AccountID: de, AmountCents: cents,
					Description: descricao, OccurredOn: civil.MustNew(2026, 9, dia), TransferGroupID: &grupo,
					DedupKey: chaveDedup(fmt.Sprintf("%s|out|%s|%d", casa, descricao, p.seq)),
				},
				{
					Kind: transaction.KindTransferIn, AccountID: para, AmountCents: cents,
					Description: descricao, OccurredOn: civil.MustNew(2026, 9, dia), TransferGroupID: &grupo,
					DedupKey: chaveDedup(fmt.Sprintf("%s|in|%s|%d", casa, descricao, p.seq)),
				},
			},
		})
	require.NoError(t, err)
}

func (p *pilhaCredito) relatorio(t *testing.T, casa, dono, kind, grupo string) report.CategoryReportView {
	t.Helper()
	v, err := p.relatorios.ByCategory(t.Context(),
		report.Actor{HouseholdID: casa, UserID: dono},
		report.ByCategoryInput{Month: mesDoCredito, Kind: kind, AccountGroup: grupo})
	require.NoError(t, err)
	return v
}

func (p *pilhaCredito) resumo(t *testing.T, casa, dono string) dashboard.SummaryView {
	t.Helper()
	v, err := p.painel.Summary(t.Context(),
		dashboard.Actor{HouseholdID: casa, UserID: dono},
		dashboard.SummaryInput{Month: mesDoCredito})
	require.NoError(t, err)
	return v
}

// O cenário completo: cartão vivo, cartão ARQUIVADO, conta corrente e
// dinheiro; despesa com e sem categoria em cada um; aporte lançado NO CARTÃO;
// `transfer_in` de pagamento de fatura caindo no cartão. Nos dois números, e
// nas duas contagens.
func TestQACruzadoRelatorioCreditBateComOGastoDoCartaoDoPainel(t *testing.T) {
	t.Parallel()
	p := novaPilhaCredito(t)

	cartao := p.conta(t, casaCredito, "Cartao Roxo", account.KindCreditCard, false)
	// Nasce VIVO, recebe o lançamento e só então é arquivado — a ordem da
	// vida real.
	cartaoArq := p.conta(t, casaCredito, "Cartao Antigo", account.KindCreditCard, false)
	corrente := p.conta(t, casaCredito, "Conta Corrente", account.KindChecking, false)
	dinheiro := p.conta(t, casaCredito, "Dinheiro", account.KindCash, false)

	// Mercado é GRUPO com filha (Padaria), e grupo com subcategoria não
	// recebe lançamento direto (regra do domínio) — o direto fica em Lazer,
	// que é grupo-folha. Assim o recorte é exercitado nos dois níveis.
	mercado := p.categoria(t, casaCredito, "Mercado", category.KindExpense, nil)
	padaria := p.categoria(t, casaCredito, "Padaria", category.KindExpense, &mercado)
	lazer := p.categoria(t, casaCredito, "Lazer", category.KindExpense, nil)
	salario := p.categoria(t, casaCredito, "Salario", category.KindIncome, nil)
	cdb := p.categoria(t, casaCredito, "CDB", category.KindInvestment, nil)

	// Despesas no cartão vivo — com categoria de nível 2, com categoria de
	// nível 1 e SEM categoria (o estado normal logo depois de importar a
	// fatura).
	p.lancar(t, casaCredito, donoCredito, transaction.KindExpense, cartao, &padaria, 50_000, "Padaria", 8)
	p.lancar(t, casaCredito, donoCredito, transaction.KindExpense, cartao, &lazer, 7_500, "Cinema", 8)
	p.lancar(t, casaCredito, donoCredito, transaction.KindExpense, cartao, nil, 30_000, "Farmacia", 9)
	// Despesa no cartão ARQUIVADO: continua sendo gasto de cartão daquele mês.
	p.lancar(t, casaCredito, donoCredito, transaction.KindExpense, cartaoArq, &padaria, 12_000, "Feira antiga", 7)
	// Despesas fora do cartão.
	p.lancar(t, casaCredito, donoCredito, transaction.KindExpense, corrente, &padaria, 90_000, "Atacado", 10)
	p.lancar(t, casaCredito, donoCredito, transaction.KindExpense, dinheiro, nil, 2_500, "Pao", 11)
	// Aporte NO CARTÃO: sai dos dois números (ADR-029e).
	p.lancar(t, casaCredito, donoCredito, transaction.KindExpense, cartao, &cdb, 200_000, "CDB 15 dias", 12)
	// Receita e pagamento de fatura: nem um nem outro é gasto de cartão.
	p.lancar(t, casaCredito, donoCredito, transaction.KindIncome, corrente, &salario, 500_000, "Salario", 5)
	p.transferir(t, casaCredito, donoCredito, corrente, cartao, 99_500, "Pagamento de fatura", 20)

	// Só AGORA o cartão antigo é arquivado: arquivar não apaga o passado.
	p.arquivar(t, casaCredito, cartaoArq)

	credito := p.relatorio(t, casaCredito, donoCredito, transaction.KindExpense, report.AccountGroupCredit)
	debito := p.relatorio(t, casaCredito, donoCredito, transaction.KindExpense, report.AccountGroupDebit)
	todas := p.relatorio(t, casaCredito, donoCredito, transaction.KindExpense, "")
	painel := p.resumo(t, casaCredito, donoCredito)

	// A asserção central: os DOIS números, e as DUAS contagens.
	assert.Equal(t, painel.CreditCardExpenseCents, credito.TotalCents,
		"o recorte credit do relatório É o gasto do cartão do painel")
	assert.Equal(t, painel.CreditCardExpenseCount, credito.Count)
	assert.Equal(t, int64(99_500), credito.TotalCents, "50.000 + 7.500 + 30.000 + 12.000, sem o aporte")
	assert.Equal(t, int64(4), credito.Count)

	// E a partição continua fechando sobre as mesmas linhas.
	assert.Equal(t, todas.TotalCents, credito.TotalCents+debito.TotalCents)
	assert.Equal(t, todas.Count, credito.Count+debito.Count)
	assert.Equal(t, int64(92_500), debito.TotalCents, "corrente 90.000 + dinheiro 2.500")

	// A despesa de cartão SEM categoria está no balde do recorte, e não sumiu
	// — é o defeito que a forma rejeitada do ADR-031b produziria.
	var balde *report.CategoryReportGroupView
	for i := range credito.Items {
		if credito.Items[i].CategoryID == nil {
			balde = &credito.Items[i]
		}
	}
	require.NotNil(t, balde, "o balde existe no recorte credit")
	assert.Equal(t, int64(30_000), balde.TotalCents)
	assert.Equal(t, int64(1), balde.Count)

	// O cartão arquivado está dentro do recorte, somado à filha do cartão
	// vivo — e a dobra folha → grupo acontece DEPOIS do recorte, então o
	// grupo Mercado do recorte credit é só a parte de cartão.
	itens := map[string]report.CategoryReportGroupView{}
	for _, it := range credito.Items {
		if it.CategoryID != nil {
			itens[*it.CategoryID] = it
		}
	}
	assert.Equal(t, int64(62_000), itens[mercado].TotalCents, "cartão vivo 50.000 + cartão arquivado 12.000")
	assert.Equal(t, int64(0), itens[mercado].DirectCents, "grupo com filha não recebe lançamento direto")
	require.Len(t, itens[mercado].Children, 1)
	assert.Equal(t, int64(62_000), itens[mercado].Children[0].TotalCents)
	assert.Equal(t, int64(2), itens[mercado].Children[0].Count)
	assert.Equal(t, int64(7_500), itens[lazer].DirectCents, "o grupo-folha, no cartão")

	// O aporte não aparece em nenhum dos três, e o painel o vê no líquido.
	for nome, v := range map[string]report.CategoryReportView{"credit": credito, "debit": debito, "todas": todas} {
		for _, it := range v.Items {
			if it.CategoryID != nil {
				assert.NotEqual(t, cdb, *it.CategoryID, "o aporte aparece em %s", nome)
			}
		}
	}
	assert.Equal(t, int64(200_000), painel.InvestmentNetCents)
}

// Os dois números batem também nos estados de BORDA em que um teste de um
// cenário só passaria por acaso: mês vazio, casa sem cartão, mês em que o
// cartão só tem aporte, e mês em que o cartão só tem pagamento de fatura.
func TestQACruzadoCreditBateComOPainelNasBordas(t *testing.T) {
	t.Parallel()

	casos := map[string]func(t *testing.T, p *pilhaCredito, cartao, corrente string, cdb, mercado string){
		"mês vazio": func(*testing.T, *pilhaCredito, string, string, string, string) {},
		"sem gasto no cartão": func(t *testing.T, p *pilhaCredito, _, corrente, _, mercado string) {
			p.lancar(t, casaCredito, donoCredito, transaction.KindExpense, corrente, &mercado, 10_000, "Atacado", 8)
		},
		"cartão só com aporte": func(t *testing.T, p *pilhaCredito, cartao, _, cdb, _ string) {
			p.lancar(t, casaCredito, donoCredito, transaction.KindExpense, cartao, &cdb, 150_000, "CDB", 9)
		},
		"cartão só com pagamento de fatura": func(t *testing.T, p *pilhaCredito, cartao, corrente, _, _ string) {
			p.transferir(t, casaCredito, donoCredito, corrente, cartao, 80_000, "Fatura", 20)
		},
		"cartão com gasto de valor zero": func(t *testing.T, p *pilhaCredito, cartao, _, _, mercado string) {
			p.lancar(t, casaCredito, donoCredito, transaction.KindExpense, cartao, &mercado, 1, "Ajuste", 9)
		},
	}

	for nome, montar := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			p := novaPilhaCredito(t)
			cartao := p.conta(t, casaCredito, "Cartao Roxo", account.KindCreditCard, false)
			corrente := p.conta(t, casaCredito, "Conta Corrente", account.KindChecking, false)
			mercado := p.categoria(t, casaCredito, "Mercado", category.KindExpense, nil)
			cdb := p.categoria(t, casaCredito, "CDB", category.KindInvestment, nil)
			montar(t, p, cartao, corrente, cdb, mercado)

			credito := p.relatorio(t, casaCredito, donoCredito, transaction.KindExpense, report.AccountGroupCredit)
			painel := p.resumo(t, casaCredito, donoCredito)

			assert.Equal(t, painel.CreditCardExpenseCents, credito.TotalCents, "centavos em %q", nome)
			assert.Equal(t, painel.CreditCardExpenseCount, credito.Count, "contagem em %q", nome)
			require.NotNil(t, credito.AccountGroup)
			assert.Equal(t, report.AccountGroupCredit, *credito.AccountGroup)
			assert.NotNil(t, credito.Items, "items nunca é null")
		})
	}
}

// BOLA no cruzado: a casa vizinha, com cartão de MESMO NOME e gasto no mesmo
// mês, recebe zeros nos dois números — e a casa de quem tem os dados continua
// vendo exatamente o que é dela.
func TestQACruzadoCreditNaoVazaParaACasaVizinha(t *testing.T) {
	t.Parallel()
	p := novaPilhaCredito(t)

	cartao := p.conta(t, casaCredito, "Cartao Roxo", account.KindCreditCard, false)
	mercado := p.categoria(t, casaCredito, "Mercado", category.KindExpense, nil)
	p.lancar(t, casaCredito, donoCredito, transaction.KindExpense, cartao, &mercado, 123_456, "Mercado", 8)

	// A vizinha tem um cartão de mesmo nome, e nada gasto nele.
	p.conta(t, casaVizinha, "Cartao Roxo", account.KindCreditCard, false)
	p.categoria(t, casaVizinha, "Mercado", category.KindExpense, nil)

	minha := p.relatorio(t, casaCredito, donoCredito, transaction.KindExpense, report.AccountGroupCredit)
	assert.Equal(t, int64(123_456), minha.TotalCents)

	vizinha := p.relatorio(t, casaVizinha, donoVizinho, transaction.KindExpense, report.AccountGroupCredit)
	assert.Equal(t, int64(0), vizinha.TotalCents)
	assert.Empty(t, vizinha.Items)
	assert.Equal(t, p.resumo(t, casaVizinha, donoVizinho).CreditCardExpenseCents, vizinha.TotalCents)

	// E a minha continua igual depois de a vizinha ter lido.
	assert.Equal(t, minha, p.relatorio(t, casaCredito, donoCredito, transaction.KindExpense, report.AccountGroupCredit))
}
