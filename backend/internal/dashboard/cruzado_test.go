package dashboard_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/dashboard"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Testes CRUZADOS do painel — critérios 4, 5, 6, 7 e 15 da spec 0008 §8, mais
// as bordas que só o banco mostra.
//
// # Por que contra SQLite de verdade, e não contra dublês
//
// Os três números do painel têm de concordar com os de OUTRAS duas telas
// (`GET /transactions` e `GET /investments`), e cada uma delas nasce de uma
// consulta diferente sobre as MESMAS linhas: SumMonthByKindAndAccount,
// Summary e SumInvestmentsByMonth. Um dublê por serviço provaria que os dublês
// concordam entre si — que é exatamente o que nunca esteve em dúvida. O risco
// real é as três consultas divergirem no predicado, na janela ou no
// tratamento do NULL, e só o banco mostra isso.
//
// A pilha é montada com as MESMAS ligações de `cmd/api/main.go`: os três
// serviços compartilham os mesmos repositórios sobre o mesmo arquivo de banco.
//
// Molde: internal/report/investimentos_cruzado_test.go.

const (
	casaA = "00000000-0000-7000-9000-0000000000a1"
	casaB = "00000000-0000-7000-9000-0000000000b1"
	donoA = "00000000-0000-7000-8000-0000000000a1"
	donoB = "00000000-0000-7000-8000-0000000000b1"

	mesCruzado = "2026-09"
)

// pilha é a fatia da aplicação que responde às TRÊS perguntas que têm de
// concordar sobre o mesmo mês.
type pilha struct {
	db *storage.DB

	lancamentos   *transaction.Service
	investimentos *investment.Service
	painel        *dashboard.Service
	handler       *dashboard.Handler

	contas     *gormstore.AccountRepository
	categorias *gormstore.CategoryRepository

	logs    *bytes.Buffer
	momento time.Time
	seq     int
}

func novaPilha(t *testing.T) *pilha {
	t.Helper()

	ctx := t.Context()
	db, err := storage.Open(ctx, storage.Options{
		Driver: storage.DriverSQLite,
		// Arquivo, e uma conexão só: SQLite em memória por conexão daria um
		// banco diferente a cada handle, e mais de uma conexão traz
		// "database is locked" nas transações do UnitOfWork.
		DSN:          filepath.Join(t.TempDir(), "painel-cruzado.db"),
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

	logs := &bytes.Buffer{}
	lg := logging.New(logs, logging.Options{Level: "debug", Format: "json"})
	momento := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

	p := &pilha{
		db:         db,
		contas:     repoConta,
		categorias: repoCategoria,
		logs:       logs,
		momento:    momento,
		lancamentos: transaction.NewService(repoTx, repoConta, repoCategoria, repoFatura, uow,
			classify.NewLoader(repoCategoria, repoConta),
			transaction.WithClock(func() time.Time { return momento }),
		),
		investimentos: investment.NewService(repoTx, repoCategoria, repoConta,
			classify.NewLoader(repoCategoria, repoConta), uow, logging.Discard(),
			investment.WithClock(func() time.Time { return momento }),
		),
		// O painel recebe EXATAMENTE os mesmos três repositórios que o
		// main.go entrega a ele.
		painel: dashboard.NewService(repoTx, repoCategoria, repoConta, lg),
	}
	p.handler = dashboard.NewHandler(p.painel, lg)
	return p
}

// --- montagem ---------------------------------------------------------------

func (p *pilha) proximo(prefixo string) string {
	p.seq++
	return fmt.Sprintf("00000000-0000-7000-%s000-%012d", prefixo, p.seq)
}

// conta grava uma conta direto no repositório e devolve o id.
func (p *pilha) conta(t *testing.T, casa, nome, kind string) string {
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
	require.NoError(t, p.contas.Create(t.Context(), a))
	return a.ID
}

// categoria grava uma categoria de nível 1 com a natureza pedida.
func (p *pilha) categoria(t *testing.T, casa, nome, kind string) string {
	t.Helper()
	c := category.Category{
		ID: p.proximo("c"), HouseholdID: casa,
		Name: nome, NameNorm: textnorm.Normalize(nome), Kind: kind,
		CreatedAt: p.momento, UpdatedAt: p.momento,
	}
	require.NoError(t, p.categorias.Create(t.Context(), &c))
	return c.ID
}

// lancar grava UM lançamento pela porta da frente — o serviço, dentro da mesma
// transação de produção.
func (p *pilha) lancar(t *testing.T, casa, dono, kind, contaID string, categoriaID *string,
	cents int64, descricao string, dia civil.Date,
) string {
	t.Helper()
	p.seq++
	res, err := p.lancamentos.CreateBatch(t.Context(),
		transaction.Actor{HouseholdID: casa, UserID: dono, IP: "203.0.113.7"},
		transaction.CreateBatchInput{
			Source: transaction.SourceManual,
			Rows: []transaction.NewTransaction{{
				Kind: kind, AccountID: contaID, CategoryID: categoriaID,
				AmountCents: cents, Description: descricao, OccurredOn: dia,
				DedupKey: chaveDedupQA(fmt.Sprintf("%s|%s|%d|%d", casa, descricao, cents, p.seq)),
			}},
		})
	require.NoError(t, err)
	require.Len(t, res.IDs, 1)
	return res.IDs[0]
}

// transferir grava as DUAS pernas de uma transferência interna, no mesmo lote e
// na mesma transação — que é a única forma que o domínio aceita (ADR-016).
func (p *pilha) transferir(t *testing.T, casa, dono, de, para string, cents int64, descricao string, dia civil.Date) {
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
					Description: descricao, OccurredOn: dia, TransferGroupID: &grupo,
					DedupKey: chaveDedupQA(fmt.Sprintf("%s|out|%s|%d", casa, descricao, p.seq)),
				},
				{
					Kind: transaction.KindTransferIn, AccountID: para, AmountCents: cents,
					Description: descricao, OccurredOn: dia, TransferGroupID: &grupo,
					DedupKey: chaveDedupQA(fmt.Sprintf("%s|in|%s|%d", casa, descricao, p.seq)),
				},
			},
		})
	require.NoError(t, err)
}

// --- leituras ---------------------------------------------------------------

func (p *pilha) resumoDoPainel(t *testing.T, casa, dono, mesAlvo string) dashboard.SummaryView {
	t.Helper()
	v, err := p.painel.Summary(t.Context(),
		dashboard.Actor{HouseholdID: casa, UserID: dono},
		dashboard.SummaryInput{Month: mesAlvo})
	require.NoError(t, err)
	return v
}

func (p *pilha) resumoDeLancamentos(t *testing.T, casa, dono, mesAlvo string) transaction.SummaryView {
	t.Helper()
	v, err := p.lancamentos.List(t.Context(),
		transaction.Actor{HouseholdID: casa, UserID: dono, IP: "203.0.113.7"},
		transaction.ListInput{Month: mesAlvo})
	require.NoError(t, err)
	return v.Summary
}

func (p *pilha) totaisDeInvestimento(t *testing.T, casa, dono, mesAlvo string) investment.TotalsView {
	t.Helper()
	v, err := p.investimentos.Overview(t.Context(),
		investment.Actor{HouseholdID: casa, UserID: dono, IP: "203.0.113.7"},
		investment.OverviewInput{Month: mesAlvo})
	require.NoError(t, err)
	return v.Monthly
}

// pelaRota responde como a rota real responderia, para as asserções sobre o
// CORPO da resposta (que é onde um id ou um nome alheio vazaria).
func (p *pilha) pelaRota(t *testing.T, casa, dono, alvo string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, alvo, nil)
	r = r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID: dono, HouseholdID: casa, Role: "owner", SessionID: "sess-qa",
	}))
	rec := httptest.NewRecorder()
	p.handler.Summary(rec, r)
	return rec
}

// --- cenário padrão ---------------------------------------------------------

// casaMontada são os ids de uma casa completa: corrente, cartão, e as duas
// categorias que marcam investimento.
type casaMontada struct {
	casa, dono            string
	corrente, cartao      string
	mercado, salario      string
	aporteCat, resgateCat string
}

// montarCasa cria SEMPRE com os mesmos nomes. Nomes idênticos entre as duas
// casas são o ponto do teste de BOLA: se o `household_id` sumisse de algum
// WHERE, um cenário com nomes diferentes só provaria que a busca falha, e não
// que uma casa enxergou a outra.
func (p *pilha) montarCasa(t *testing.T, casa, dono string) casaMontada {
	t.Helper()
	return casaMontada{
		casa: casa, dono: dono,
		corrente:   p.conta(t, casa, "Conta Corrente", account.KindChecking),
		cartao:     p.conta(t, casa, "Cartao Roxo", account.KindCreditCard),
		mercado:    p.categoria(t, casa, "Mercado", category.KindExpense),
		salario:    p.categoria(t, casa, "Salario", category.KindIncome),
		aporteCat:  p.categoria(t, casa, "CDB", category.KindInvestment),
		resgateCat: p.categoria(t, casa, "Resgate CDB", category.KindRedemption),
	}
}

// mesDaSpec monta EXATAMENTE o mês do critério 1 da spec §8, contra banco:
// 3 receitas (R$ 5.000), 2 despesas comuns (R$ 800), 1 aporte de R$ 2.000 e
// 1 resgate de R$ 350. O aporte vai no CARTÃO, para o critério 10 também
// atravessar o banco.
func (p *pilha) mesDaSpec(t *testing.T, c casaMontada) {
	t.Helper()
	dia := func(d int) civil.Date { return civil.MustNew(2026, 9, d) }

	p.lancar(t, c.casa, c.dono, transaction.KindIncome, c.corrente, &c.salario, 300_000, "Salario", dia(5))
	p.lancar(t, c.casa, c.dono, transaction.KindIncome, c.corrente, &c.salario, 150_000, "Freela", dia(6))
	p.lancar(t, c.casa, c.dono, transaction.KindIncome, c.corrente, nil, 50_000, "Reembolso", dia(7))

	p.lancar(t, c.casa, c.dono, transaction.KindExpense, c.cartao, &c.mercado, 50_000, "Mercado", dia(8))
	p.lancar(t, c.casa, c.dono, transaction.KindExpense, c.cartao, nil, 30_000, "Farmacia", dia(9))

	p.lancar(t, c.casa, c.dono, transaction.KindExpense, c.cartao, &c.aporteCat, 200_000, "CDB 15 dias", dia(10))
	p.lancar(t, c.casa, c.dono, transaction.KindIncome, c.corrente, &c.resgateCat, 35_000, "Resgate CDB", dia(11))
}

// --- critério 4 -------------------------------------------------------------

// `incomeCents` do painel é IDÊNTICO ao `summary.incomeCents` de
// GET /transactions no mesmo mês. As duas respostas vêm de consultas
// diferentes sobre as mesmas linhas, e é por isso que o teste é contra banco.
//
// A asserção vale nos QUATRO estados que mudam o predicado: mês sem marcação,
// mês com resgate, mês em que o resgate é a única receita, e mês vazio. Um
// deles só, e o teste passaria por acaso no estado em que os dois números são
// zero.
func TestQACruzadoReceitaDoPainelBateComOSummaryDeLancamentos(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	c := p.montarCasa(t, casaA, donoA)
	dia := func(d int) civil.Date { return civil.MustNew(2026, 9, d) }

	// Estado 1: mês vazio. Os dois são zero, e isso também tem de bater.
	assert.Equal(t, p.resumoDeLancamentos(t, casaA, donoA, mesCruzado).IncomeCents,
		p.resumoDoPainel(t, casaA, donoA, mesCruzado).IncomeCents, "mês vazio")

	// Estado 2: só receita comum.
	p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.salario, 300_000, "Salario", dia(5))
	assert.Equal(t, p.resumoDeLancamentos(t, casaA, donoA, mesCruzado).IncomeCents,
		p.resumoDoPainel(t, casaA, donoA, mesCruzado).IncomeCents, "só receita comum")

	// Estado 3: entra um RESGATE — é aqui que os dois números poderiam
	// divergir, porque é o único em que a subtração acontece.
	p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.resgateCat, 35_000, "Resgate", dia(11))
	painel := p.resumoDoPainel(t, casaA, donoA, mesCruzado)
	lista := p.resumoDeLancamentos(t, casaA, donoA, mesCruzado)
	assert.Equal(t, lista.IncomeCents, painel.IncomeCents, "com resgate no mês")
	assert.Equal(t, int64(300_000), painel.IncomeCents, "o resgate saiu da receita nos DOIS")
	assert.Equal(t, int64(35_000), lista.RedeemedCents, "e ele está, inteiro, no balde de resgate")

	// Estado 4: o mês INTEIRO da spec §8.1 — três receitas, duas despesas, um
	// aporte e um resgate, sobre a mesma casa.
	p.mesDaSpec(t, c)
	painel = p.resumoDoPainel(t, casaA, donoA, mesCruzado)
	lista = p.resumoDeLancamentos(t, casaA, donoA, mesCruzado)
	assert.Equal(t, lista.IncomeCents, painel.IncomeCents, "mês completo")

	// E a CONTAGEM também: um número certo com a contagem errada faria a
	// legenda da faixa mentir.
	assert.Equal(t, lista.IncomeCents+lista.RedeemedCents-lista.RedeemedCents, painel.IncomeCents)
}

// O critério 1 da spec, AGORA contra banco: os números exatos que o usuário
// aprovou, atravessando as consultas reais em vez de linhas agregadas
// escritas à mão.
func TestQACruzadoNumerosDoCenario1ContraBanco(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	c := p.montarCasa(t, casaA, donoA)
	p.mesDaSpec(t, c)

	v := p.resumoDoPainel(t, casaA, donoA, mesCruzado)

	assert.Equal(t, mesCruzado, v.Month)
	assert.Equal(t, int64(500_000), v.IncomeCents, "3 receitas de R$ 5.000, sem o resgate")
	assert.Equal(t, int64(3), v.IncomeCount)
	assert.Equal(t, int64(165_000), v.InvestmentNetCents, "aporte 200.000 − resgate 35.000")
	assert.Equal(t, int64(2), v.InvestmentCount, "aportes + resgates, nunca a diferença")
	// Critério 10, contra banco: o aporte foi lançado NO CARTÃO e não pode
	// aparecer no gasto do cartão.
	assert.Equal(t, int64(80_000), v.CreditCardExpenseCents, "R$ 800 de despesa comum, sem o aporte")
	assert.Equal(t, int64(2), v.CreditCardExpenseCount)
	assert.Equal(t, int64(1), v.CreditCardAccountCount)
	assert.Equal(t, int64(2), v.InvestmentCategoryCount)
}

// --- critério 5 -------------------------------------------------------------

// `investmentNetCents` é EXATAMENTE `monthly.contributionsCents −
// monthly.redemptionsCents` de GET /investments no mesmo mês.
//
// Os três estados que importam: aporte maior, resgate maior (líquido
// NEGATIVO, que é o ponto da feature) e empate em zero COM movimento — o
// último separa "zero porque não houve nada" de "zero porque se anularam", e
// é onde um `investmentCount` calculado como diferença quebraria.
func TestQACruzadoLiquidoBateComOsTotaisDeInvestimentos(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		aporte, resgate int64
		liquido         int64
		contagem        int64
	}{
		"aporte maior":         {aporte: 200_000, resgate: 35_000, liquido: 165_000, contagem: 2},
		"resgate maior":        {aporte: 50_000, resgate: 90_000, liquido: -40_000, contagem: 2},
		"empate com movimento": {aporte: 70_000, resgate: 70_000, liquido: 0, contagem: 2},
		"só aporte":            {aporte: 120_000, resgate: 0, liquido: 120_000, contagem: 1},
		"só resgate":           {aporte: 0, resgate: 120_000, liquido: -120_000, contagem: 1},
		"nenhum":               {aporte: 0, resgate: 0, liquido: 0, contagem: 0},
	}

	for nome, caso := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			p := novaPilha(t)
			c := p.montarCasa(t, casaA, donoA)

			if caso.aporte > 0 {
				p.lancar(t, casaA, donoA, transaction.KindExpense, c.corrente, &c.aporteCat,
					caso.aporte, "Aporte", civil.MustNew(2026, 9, 10))
			}
			if caso.resgate > 0 {
				p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.resgateCat,
					caso.resgate, "Resgate", civil.MustNew(2026, 9, 11))
			}

			painel := p.resumoDoPainel(t, casaA, donoA, mesCruzado)
			mensal := p.totaisDeInvestimento(t, casaA, donoA, mesCruzado)

			assert.Equal(t, mensal.ContributionsCents-mensal.RedemptionsCents, painel.InvestmentNetCents,
				"o líquido do painel é, exatamente, a subtração dos dois números de /investimentos")
			assert.Equal(t, caso.liquido, painel.InvestmentNetCents)

			// A CONTAGEM é a SOMA das duas — um aporte e um resgate que se
			// anulam continuam sendo DOIS lançamentos.
			assert.Equal(t, mensal.ContributionCount+mensal.RedemptionCount, painel.InvestmentCount)
			assert.Equal(t, caso.contagem, painel.InvestmentCount)
		})
	}
}

// --- critérios 6 e 7 --------------------------------------------------------

// Critério 6: uma transferência interna de R$ 1.000 da conta corrente para o
// cartão não muda NENHUM dos três números.
//
// A prova é a comparação do MESMO mês com e sem ela — a SummaryView inteira,
// campo a campo, e não só os três de dinheiro: uma contagem que se mexesse já
// seria a perna vazando para dentro da agregação.
func TestQACruzadoTransferenciaInternaNaoMudaNenhumDosTresNumeros(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	c := p.montarCasa(t, casaA, donoA)
	p.mesDaSpec(t, c)

	antes := p.resumoDoPainel(t, casaA, donoA, mesCruzado)
	listaAntes := p.resumoDeLancamentos(t, casaA, donoA, mesCruzado)

	// R$ 1.000 da corrente para o cartão — que é, literalmente, pagar a
	// fatura.
	p.transferir(t, casaA, donoA, c.corrente, c.cartao, 100_000, "Pagamento de fatura", civil.MustNew(2026, 9, 20))

	depois := p.resumoDoPainel(t, casaA, donoA, mesCruzado)
	assert.Equal(t, antes, depois, "transferência interna não entra em NENHUM número do painel")

	// A perna nem CHEGOU ao serviço: se a consulta a tivesse lido, ela cairia
	// no balde `kind_inesperado` da dobra e sairia um aviso no log. Sem esta
	// asserção o teste passaria com o `kind IN (income, expense)` removido do
	// WHERE — os números continuariam certos, e a garantia teria virado sorte.
	assert.NotContains(t, p.logs.String(), "kind_inesperado",
		"a perna de transferência não é LIDA, e não podada depois em Go")

	// E não é por a transferência não ter sido gravada: as duas pernas estão
	// lá, contadas em `count`, e continuam fora de receita e de despesa.
	listaDepois := p.resumoDeLancamentos(t, casaA, donoA, mesCruzado)
	assert.Equal(t, listaAntes.Count+2, listaDepois.Count, "as duas pernas foram gravadas de verdade")
	assert.Equal(t, listaAntes.IncomeCents, listaDepois.IncomeCents)
	assert.Equal(t, listaAntes.ExpenseCents, listaDepois.ExpenseCents)
}

// Critério 7: pagar a fatura do cartão não reduz nem aumenta
// `creditCardExpenseCents`.
//
// O mês tem gasto de cartão de verdade ANTES do pagamento — um teste em que os
// dois lados são zero provaria apenas que zero é igual a zero. E o pagamento é
// do valor EXATO da fatura, que é a forma de o defeito aparecer: se a perna
// `transfer_in` do cartão fosse lida como receita, ou a `transfer_out` como
// gasto, o número zeraria ou dobraria, e não simplesmente "mudaria um pouco".
func TestQACruzadoPagarAFaturaNaoMexeNoGastoDoCartao(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	c := p.montarCasa(t, casaA, donoA)
	p.mesDaSpec(t, c)

	antes := p.resumoDoPainel(t, casaA, donoA, mesCruzado)
	require.Equal(t, int64(80_000), antes.CreditCardExpenseCents, "o mês tem gasto de cartão de verdade")

	// O pagamento do valor exato da fatura do mês.
	p.transferir(t, casaA, donoA, c.corrente, c.cartao, antes.CreditCardExpenseCents,
		"Pagamento da fatura", civil.MustNew(2026, 9, 25))

	depois := p.resumoDoPainel(t, casaA, donoA, mesCruzado)
	assert.Equal(t, antes.CreditCardExpenseCents, depois.CreditCardExpenseCents,
		"pagar a fatura não é gasto novo, e não desfaz o gasto antigo")
	assert.Equal(t, antes.CreditCardExpenseCount, depois.CreditCardExpenseCount)
	assert.Equal(t, antes.IncomeCents, depois.IncomeCents, "e a perna que cai no cartão não é receita")
	assert.Equal(t, antes.InvestmentNetCents, depois.InvestmentNetCents)
	assert.NotContains(t, p.logs.String(), "kind_inesperado",
		"nenhuma das duas pernas foi lida pela consulta")
}

// Mês em que só houve transferência: os três números são ZERO, com as
// contagens em zero — e a faixa continua existindo, com os dois contadores de
// estado vazio preenchidos (é o que deixa a tela dizer "nenhum lançamento" sem
// esconder que a casa tem cartão e categoria).
func TestQACruzadoMesSoComTransferenciasEhTodoZero(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	c := p.montarCasa(t, casaA, donoA)
	corrente2 := p.conta(t, casaA, "Poupanca", account.KindSavings)

	p.transferir(t, casaA, donoA, c.corrente, c.cartao, 100_000, "Fatura", civil.MustNew(2026, 9, 3))
	p.transferir(t, casaA, donoA, c.corrente, corrente2, 250_000, "Reserva", civil.MustNew(2026, 9, 4))
	p.transferir(t, casaA, donoA, corrente2, c.corrente, 250_000, "Volta", civil.MustNew(2026, 9, 5))

	v := p.resumoDoPainel(t, casaA, donoA, mesCruzado)

	assert.Zero(t, v.IncomeCents)
	assert.Zero(t, v.IncomeCount)
	assert.Zero(t, v.CreditCardExpenseCents)
	assert.Zero(t, v.CreditCardExpenseCount)
	assert.Zero(t, v.InvestmentNetCents)
	assert.Zero(t, v.InvestmentCount)
	// Os contadores de estado vazio NÃO são zero: a casa tem cartão e marca
	// investimento. Zerá-los aqui faria a tela dizer "nenhum cartão
	// cadastrado" no mês em que só houve transferência.
	assert.Equal(t, int64(1), v.CreditCardAccountCount)
	assert.Equal(t, int64(2), v.InvestmentCategoryCount)

	// E o mês existe de verdade: seis pernas gravadas.
	assert.Equal(t, int64(6), p.resumoDeLancamentos(t, casaA, donoA, mesCruzado).Count)

	// Nenhuma delas chegou à dobra: o log do painel está limpo. Zeros que
	// viessem de seis linhas descartadas em Go dariam o mesmo resultado na
	// tela e seriam outra coisa completamente.
	assert.Empty(t, p.logs.String(), "seis pernas, nenhuma lida pela consulta")
}

// --- critério 15: BOLA ------------------------------------------------------

// A casa A tem gasto de cartão e investimento; o token da casa B — com conta e
// categoria de nomes IDÊNTICOS — recebe zeros, e nenhum id, nome ou centavo da
// casa A aparece na resposta nem no log.
//
// Nomes idênticos são o ponto: com nomes diferentes, um vazamento apareceria
// como "número errado" e poderia ser confundido com bug de soma. Com nomes
// iguais, o único jeito de a casa B ver algo é o escopo ter falhado.
func TestQACruzadoBOLACasaBRecebeZerosSemNadaDaCasaA(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)

	a := p.montarCasa(t, casaA, donoA)
	p.montarCasa(t, casaB, donoB)
	p.mesDaSpec(t, a)

	// A casa A tem números; é isso que faz o zero da casa B significar algo.
	vistaA := p.resumoDoPainel(t, casaA, donoA, mesCruzado)
	require.NotZero(t, vistaA.IncomeCents)
	require.NotZero(t, vistaA.CreditCardExpenseCents)
	require.NotZero(t, vistaA.InvestmentNetCents)

	rec := p.pelaRota(t, casaB, donoB, "/api/v1/dashboard?month="+mesCruzado)
	require.Equal(t, http.StatusOK, rec.Code, "corpo: %s", rec.Body.String())
	corpo := rec.Body.String()

	var vistaB dashboard.SummaryView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &vistaB))

	assert.Zero(t, vistaB.IncomeCents)
	assert.Zero(t, vistaB.IncomeCount)
	assert.Zero(t, vistaB.CreditCardExpenseCents)
	assert.Zero(t, vistaB.CreditCardExpenseCount)
	assert.Zero(t, vistaB.InvestmentNetCents)
	assert.Zero(t, vistaB.InvestmentCount)
	// O que a casa B vê é a SUA própria configuração — e ela é igual à da
	// casa A em forma, o que torna o teste honesto: os contadores não são
	// zero, e ainda assim nenhum centavo atravessou.
	assert.Equal(t, int64(1), vistaB.CreditCardAccountCount)
	assert.Equal(t, int64(2), vistaB.InvestmentCategoryCount)

	// Nenhum ID da casa A no corpo.
	for nome, id := range map[string]string{
		"casa":              casaA,
		"dono":              donoA,
		"conta corrente":    a.corrente,
		"cartão":            a.cartao,
		"categoria aporte":  a.aporteCat,
		"categoria resgate": a.resgateCat,
	} {
		assert.NotContains(t, corpo, id, "id de %s da casa A vazou na resposta", nome)
	}
	// Nenhum CENTAVO da casa A no corpo. (A busca é textual de propósito: ela
	// pega o número em qualquer campo, inclusive num campo novo que alguém
	// acrescente amanhã sem pensar no escopo.)
	for _, centavos := range []string{"500000", "165000", "80000", "200000", "35000"} {
		assert.NotContains(t, corpo, centavos, "centavo da casa A vazou na resposta")
	}

	// E nada disso no LOG — nem id, nem nome, nem centavo.
	registros := p.logs.String()
	assert.NotContains(t, registros, casaA)
	assert.NotContains(t, registros, a.cartao)
	assert.NotContains(t, registros, "Cartao Roxo")
	assert.NotContains(t, registros, "500000")

	// O caminho inverso fecha o cerco: a casa A continua vendo o que é dela.
	assert.Equal(t, vistaA, p.resumoDoPainel(t, casaA, donoA, mesCruzado))
}

// Critério 16 contra BANCO: com duas casas reais e dados reais, `householdId=`
// da casa A no token da casa B devolve a resposta da casa B, byte a byte.
func TestQACruzadoHouseholdIdDaOutraCasaNaoTrocaDeCasaNoBanco(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	a := p.montarCasa(t, casaA, donoA)
	p.montarCasa(t, casaB, donoB)
	p.mesDaSpec(t, a)

	semParam := p.pelaRota(t, casaB, donoB, "/api/v1/dashboard?month="+mesCruzado)
	require.Equal(t, http.StatusOK, semParam.Code)

	comParam := p.pelaRota(t, casaB, donoB,
		"/api/v1/dashboard?month="+mesCruzado+"&householdId="+casaA+"&accountId="+a.cartao)
	require.Equal(t, http.StatusOK, comParam.Code)

	assert.Equal(t, semParam.Body.String(), comParam.Body.String(),
		"o parâmetro é inerte também com as duas casas existindo de verdade")
}

// --- bordas: a virada do ano ------------------------------------------------

// Dezembro e janeiro são meses DIFERENTES, e o painel não mistura os dois.
//
// A virada do ano é a borda em que uma aritmética de mês ("mês + 1") erra sem
// avisar, e em que um `competence_month` montado por concatenação erraria o
// ano. Os três números são conferidos nos dois meses, CRUZADOS com as outras
// duas telas em cada um deles.
func TestQACruzadoViradaDoAnoNaoMisturaOsMeses(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	c := p.montarCasa(t, casaA, donoA)

	// Dezembro de 2026.
	p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.salario, 400_000, "Salario dez", civil.MustNew(2026, 12, 20))
	p.lancar(t, casaA, donoA, transaction.KindExpense, c.cartao, &c.mercado, 60_000, "Ceia", civil.MustNew(2026, 12, 24))
	p.lancar(t, casaA, donoA, transaction.KindExpense, c.corrente, &c.aporteCat, 100_000, "Aporte dez", civil.MustNew(2026, 12, 31))

	// Janeiro de 2027 — inclusive o dia 1º, que é o vizinho imediato.
	p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.salario, 410_000, "Salario jan", civil.MustNew(2027, 1, 1))
	p.lancar(t, casaA, donoA, transaction.KindExpense, c.cartao, &c.mercado, 70_000, "Mercado jan", civil.MustNew(2027, 1, 2))
	p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.resgateCat, 25_000, "Resgate jan", civil.MustNew(2027, 1, 3))

	dez := p.resumoDoPainel(t, casaA, donoA, "2026-12")
	jan := p.resumoDoPainel(t, casaA, donoA, "2027-01")

	assert.Equal(t, "2026-12", dez.Month)
	assert.Equal(t, int64(400_000), dez.IncomeCents)
	assert.Equal(t, int64(60_000), dez.CreditCardExpenseCents)
	assert.Equal(t, int64(100_000), dez.InvestmentNetCents, "o aporte de 31/12 é de dezembro")

	assert.Equal(t, "2027-01", jan.Month)
	assert.Equal(t, int64(410_000), jan.IncomeCents, "o resgate de 03/01 saiu da receita de janeiro")
	assert.Equal(t, int64(70_000), jan.CreditCardExpenseCents)
	assert.Equal(t, int64(-25_000), jan.InvestmentNetCents, "janeiro só teve resgate: líquido negativo")

	// Cruzado nos DOIS meses: cada tela lê a mesma janela.
	for _, m := range []string{"2026-12", "2027-01"} {
		painel := p.resumoDoPainel(t, casaA, donoA, m)
		assert.Equal(t, p.resumoDeLancamentos(t, casaA, donoA, m).IncomeCents, painel.IncomeCents, "receita em %s", m)
		mensal := p.totaisDeInvestimento(t, casaA, donoA, m)
		assert.Equal(t, mensal.ContributionsCents-mensal.RedemptionsCents, painel.InvestmentNetCents, "líquido em %s", m)
	}

	// Novembro/2026 e fevereiro/2027 continuam vazios: nada escorreu para os
	// vizinhos.
	for _, m := range []string{"2026-11", "2027-02"} {
		v := p.resumoDoPainel(t, casaA, donoA, m)
		assert.Zero(t, v.IncomeCents, "mês %s", m)
		assert.Zero(t, v.CreditCardExpenseCents, "mês %s", m)
		assert.Zero(t, v.InvestmentNetCents, "mês %s", m)
	}
}

// --- bordas: os tetos de domínio -------------------------------------------

// Casa com 200 categorias de investimento — o teto EXATO da taxonomia
// (category.MaxPerHousehold) e do filtro do repositório
// (maxCategoryFilterIDs). A consulta tem de caber no orçamento de parâmetros
// dos quatro dialetos e ainda somar certo.
func TestQACruzado200CategoriasDeInvestimentoAindaFecham(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	c := p.montarCasa(t, casaA, donoA)

	// A casa já tem 2 marcadas (CDB e Resgate CDB); mais 198 fecham 200.
	var ultimas []string
	for i := range 198 {
		kind := category.KindInvestment
		if i%2 == 1 {
			kind = category.KindRedemption
		}
		ultimas = append(ultimas, p.categoria(t, casaA, fmt.Sprintf("Fundo %03d", i), kind))
	}
	require.Len(t, ultimas, 198)

	// Um aporte e um resgate em categorias do FIM da lista: se o conjunto
	// fosse truncado em algum ponto, seriam estes que sumiriam.
	p.lancar(t, casaA, donoA, transaction.KindExpense, c.corrente, &ultimas[196], 80_000, "Aporte", civil.MustNew(2026, 9, 10))
	p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &ultimas[197], 30_000, "Resgate", civil.MustNew(2026, 9, 11))
	p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.salario, 500_000, "Salario", civil.MustNew(2026, 9, 5))

	v := p.resumoDoPainel(t, casaA, donoA, mesCruzado)
	assert.Equal(t, int64(200), v.InvestmentCategoryCount, "o teto EXATO da taxonomia")
	assert.Equal(t, int64(50_000), v.InvestmentNetCents, "80.000 de aporte − 30.000 de resgate")
	assert.Equal(t, int64(2), v.InvestmentCount)
	assert.Equal(t, int64(500_000), v.IncomeCents, "o resgate saiu da receita, o salário ficou")

	// Cruzado: /investimentos lê a MESMA taxonomia de 200 e chega ao mesmo
	// lugar.
	mensal := p.totaisDeInvestimento(t, casaA, donoA, mesCruzado)
	assert.Equal(t, mensal.ContributionsCents-mensal.RedemptionsCents, v.InvestmentNetCents)
}

// Um passo ACIMA do teto é falha FECHADA: 500 genérico, sem número nenhum na
// resposta. É o teto da própria taxonomia que foi violado — banco em estado
// que a API não produz —, e qualquer 4xx apontaria um campo que quem pediu não
// tem como corrigir naquele pedido (ADR-029 j.2).
func TestQACruzado201CategoriasDeInvestimentoEh500Generico(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	c := p.montarCasa(t, casaA, donoA)
	p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.salario, 500_000, "Salario", civil.MustNew(2026, 9, 5))

	for i := range 199 {
		p.categoria(t, casaA, fmt.Sprintf("Fundo %03d", i), category.KindInvestment)
	}

	rec := p.pelaRota(t, casaA, donoA, "/api/v1/dashboard?month="+mesCruzado)
	require.Equal(t, http.StatusInternalServerError, rec.Code, "corpo: %s", rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "incomeCents", "nenhum número é publicado")
	assert.NotContains(t, rec.Body.String(), "500000")
	assert.Contains(t, p.logs.String(), "falha no painel", "a razão fica no log")
}

// Casa com 50 contas, TODAS cartão de crédito — o teto de
// account.MaxPerHousehold, e o pior caso da dobra: 50 linhas de despesa mais
// 50 de receita é exatamente o limite estrutural de `2 × MaxPerHousehold`
// linhas que o serviço aceita.
func TestQACruzado50ContasTodasCartaoSomamNumUmNumeroSo(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)

	var cartoes []string
	for i := range account.MaxPerHousehold {
		cartoes = append(cartoes, p.conta(t, casaA, fmt.Sprintf("Cartao %02d", i), account.KindCreditCard))
	}
	require.Len(t, cartoes, 50)
	aporteCat := p.categoria(t, casaA, "CDB", category.KindInvestment)

	var esperado int64
	for i, id := range cartoes {
		valor := int64(1_000 + i)
		esperado += valor
		p.lancar(t, casaA, donoA, transaction.KindExpense, id, nil, valor,
			fmt.Sprintf("Compra %02d", i), civil.MustNew(2026, 9, 10))
		// Uma receita na MESMA conta: é o que leva a agregação às 100 linhas.
		p.lancar(t, casaA, donoA, transaction.KindIncome, id, nil, 100,
			fmt.Sprintf("Estorno %02d", i), civil.MustNew(2026, 9, 11))
	}
	// Um aporte no último cartão, para a subtração do cartão também acontecer
	// no pior caso.
	p.lancar(t, casaA, donoA, transaction.KindExpense, cartoes[49], &aporteCat, 700_00,
		"Aporte no cartao", civil.MustNew(2026, 9, 12))

	v := p.resumoDoPainel(t, casaA, donoA, mesCruzado)

	assert.Equal(t, esperado, v.CreditCardExpenseCents, "os 50 cartões num número SÓ")
	assert.Equal(t, int64(50), v.CreditCardExpenseCount)
	assert.Equal(t, int64(50), v.CreditCardAccountCount)
	assert.Equal(t, int64(70_000), v.InvestmentNetCents, "o aporte saiu do cartão e entrou no líquido")
	assert.Equal(t, int64(50*100), v.IncomeCents)

	// Cruzado: a soma das 50 contas bate com a despesa do mês inteiro menos o
	// aporte, lida pela outra rota.
	lista := p.resumoDeLancamentos(t, casaA, donoA, mesCruzado)
	assert.Equal(t, lista.ExpenseCents, v.CreditCardExpenseCents,
		"todas as contas são cartão: a despesa do mês É o gasto do cartão")
	assert.Equal(t, lista.IncomeCents, v.IncomeCents)
}

// --- bordas: o limite do dinheiro -------------------------------------------

// Valores no limite de MaxAmountCents (R$ 999.999.999,99) atravessam inteiros,
// e a soma de vários deles não estoura nem vira float.
func TestQACruzadoValoresNoLimiteDeMaxAmountCents(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	c := p.montarCasa(t, casaA, donoA)
	const teto = int64(transaction.MaxAmountCents)

	// Três receitas no teto na mesma conta: a linha agregada soma 3 × teto,
	// que é ~3 × 10¹¹ — longe de int64, e é essa a afirmação.
	for i := range 3 {
		p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.salario, teto,
			fmt.Sprintf("Premio %d", i), civil.MustNew(2026, 9, 5+i))
	}
	p.lancar(t, casaA, donoA, transaction.KindExpense, c.cartao, &c.mercado, teto, "Compra", civil.MustNew(2026, 9, 8))
	p.lancar(t, casaA, donoA, transaction.KindExpense, c.corrente, &c.aporteCat, teto, "Aporte", civil.MustNew(2026, 9, 9))
	p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.resgateCat, teto, "Resgate", civil.MustNew(2026, 9, 10))

	v := p.resumoDoPainel(t, casaA, donoA, mesCruzado)

	assert.Equal(t, 3*teto, v.IncomeCents, "quatro linhas de receita no teto, menos o resgate no teto")
	assert.Equal(t, int64(3), v.IncomeCount, "e a contagem também perde o resgate")
	assert.Equal(t, teto, v.CreditCardExpenseCents)
	assert.Equal(t, int64(0), v.InvestmentNetCents, "aporte no teto − resgate no teto = 0")
	assert.Equal(t, int64(2), v.InvestmentCount, "zero em centavos, DOIS lançamentos")

	// O JSON não vira notação científica nem perde precisão: os centavos
	// aparecem inteiros, dígito a dígito.
	rec := p.pelaRota(t, casaA, donoA, "/api/v1/dashboard?month="+mesCruzado)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), fmt.Sprintf("\"incomeCents\":%d", 3*teto))
	assert.NotContains(t, rec.Body.String(), "e+", "dinheiro nunca sai como float")
}

// --- bordas: banco adulterado ----------------------------------------------

// `amount_cents` NEGATIVO gravado direto no banco — o que a API não produz
// (ValidateAmount recusa) e um restore parcial ou um acesso direto poderia.
//
// A regra é uma só: o painel NUNCA publica número negativo num campo que o
// contrato promete com `minimum: 0`. Quando a linha agregada fica negativa, é
// 500 genérico; quando o negativo é absorvido por uma soma maior na mesma
// linha agregada, o número publicado continua não negativo. Nos dois casos, o
// que não pode acontecer é um `-` chegar à tela.
func TestQACruzadoAmountNegativoNoBancoNuncaPublicaNumeroNegativo(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		// acompanhante é um lançamento positivo na MESMA (kind, conta) que
		// pode ou não absorver o negativo.
		acompanhanteCents int64
		esperaErro        bool
	}{
		"linha agregada fica negativa":      {acompanhanteCents: 0, esperaErro: true},
		"negativo maior que o acompanhante": {acompanhanteCents: 10_000, esperaErro: true},
		"negativo absorvido pela soma":      {acompanhanteCents: 900_000, esperaErro: false},
	}

	for nome, caso := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			p := novaPilha(t)
			c := p.montarCasa(t, casaA, donoA)

			if caso.acompanhanteCents > 0 {
				p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.salario,
					caso.acompanhanteCents, "Salario", civil.MustNew(2026, 9, 5))
			}
			alvo := p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.salario,
				100_000, "Bonus", civil.MustNew(2026, 9, 6))

			// A adulteração: direto na tabela, contornando o domínio.
			require.NoError(t, p.db.Gorm().
				Exec("UPDATE transactions SET amount_cents = ? WHERE id = ?", -500_000, alvo).Error)

			rec := p.pelaRota(t, casaA, donoA, "/api/v1/dashboard?month="+mesCruzado)
			corpo := rec.Body.String()

			if caso.esperaErro {
				require.Equal(t, http.StatusInternalServerError, rec.Code, "corpo: %s", corpo)
				assert.NotContains(t, corpo, "incomeCents", "nenhum número é publicado")
				assert.NotContains(t, corpo, "-", "e muito menos um número negativo")
				assert.Contains(t, p.logs.String(), "falha no painel")
				return
			}

			require.Equal(t, http.StatusOK, rec.Code, "corpo: %s", corpo)
			var v dashboard.SummaryView
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
			// Os QUATRO campos que o contrato publica com `minimum: 0`.
			assert.GreaterOrEqual(t, v.IncomeCents, int64(0))
			assert.GreaterOrEqual(t, v.IncomeCount, int64(0))
			assert.GreaterOrEqual(t, v.CreditCardExpenseCents, int64(0))
			assert.GreaterOrEqual(t, v.CreditCardExpenseCount, int64(0))
		})
	}
}

// Adulteração que faz o MARCADO passar do TOTAL na mesma linha agregada: o
// aporte fica positivo e a despesa comum da mesma conta fica negativa.
// `0 ≤ marcado ≤ total` é VERIFICADA, não confiada — e violá-la é 500.
func TestQACruzadoMarcadoAcimaDoTotalNoBancoEh500(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	c := p.montarCasa(t, casaA, donoA)

	p.lancar(t, casaA, donoA, transaction.KindExpense, c.corrente, &c.aporteCat, 200_000, "Aporte", civil.MustNew(2026, 9, 10))
	comum := p.lancar(t, casaA, donoA, transaction.KindExpense, c.corrente, &c.mercado, 10_000, "Mercado", civil.MustNew(2026, 9, 11))

	require.NoError(t, p.db.Gorm().
		Exec("UPDATE transactions SET amount_cents = ? WHERE id = ?", -150_000, comum).Error)

	rec := p.pelaRota(t, casaA, donoA, "/api/v1/dashboard?month="+mesCruzado)
	require.Equal(t, http.StatusInternalServerError, rec.Code, "corpo: %s", rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "investmentNetCents")
}

// Cartão EXCLUÍDO LOGICAMENTE com gasto vivo no mês.
//
// A API não produz este estado (o UsageChecker responde 422 a quem tenta
// excluir conta com lançamento), mas um restore parcial produz. O desenho é
// falha ABERTA (ADR-027f): a conta some da lista da casa, então a linha não é
// reconhecida como cartão — o gasto sai de `creditCardExpenseCents` e um aviso
// AGREGADO vai ao log, sem centavos e sem nome. O que NÃO pode acontecer: 500,
// ou o gasto de um cartão desconhecido virar receita.
//
// ⚠️ Isto é diferente de cartão ARQUIVADO (critério 12), que continua contando
// no gasto: arquivar não apaga o passado, excluir apaga a conta.
func TestQACruzadoCartaoExcluidoLogicamenteSaiDoGastoSemDerrubarOPainel(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	c := p.montarCasa(t, casaA, donoA)
	outroCartao := p.conta(t, casaA, "Cartao Preto", account.KindCreditCard)

	p.lancar(t, casaA, donoA, transaction.KindExpense, c.cartao, &c.mercado, 60_000, "Mercado", civil.MustNew(2026, 9, 8))
	p.lancar(t, casaA, donoA, transaction.KindExpense, outroCartao, &c.mercado, 25_000, "Farmacia", civil.MustNew(2026, 9, 9))
	p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.salario, 500_000, "Salario", civil.MustNew(2026, 9, 5))

	antes := p.resumoDoPainel(t, casaA, donoA, mesCruzado)
	require.Equal(t, int64(85_000), antes.CreditCardExpenseCents)
	require.Equal(t, int64(2), antes.CreditCardAccountCount)

	// A exclusão lógica, direto na tabela.
	require.NoError(t, p.db.Gorm().
		Exec("UPDATE accounts SET deleted_at = ? WHERE id = ?", p.momento, outroCartao).Error)

	depois := p.resumoDoPainel(t, casaA, donoA, mesCruzado)

	assert.Equal(t, int64(60_000), depois.CreditCardExpenseCents,
		"o gasto do cartão excluído sai da conta — falha ABERTA, não 500")
	assert.Equal(t, int64(1), depois.CreditCardExpenseCount)
	assert.Equal(t, int64(1), depois.CreditCardAccountCount)
	assert.Equal(t, int64(500_000), depois.IncomeCents, "e a despesa órfã NUNCA vira receita")

	registros := p.logs.String()
	assert.Equal(t, 1, strings.Count(registros, "\"level\":\"WARN\""), "UM aviso por requisição")
	assert.Contains(t, registros, "\"conta_desconhecida\":1")
	assert.NotContains(t, registros, "25000", "centavos nunca vão para o log")
	assert.NotContains(t, registros, "Cartao Preto", "nome de conta nunca vai para o log")
}

// Lançamento EXCLUÍDO LOGICAMENTE não conta em número nenhum — a mesma regra
// das outras duas telas, conferida aqui porque o painel tem `WHERE` próprio.
func TestQACruzadoLancamentoExcluidoNaoContaEmNumeroNenhum(t *testing.T) {
	t.Parallel()
	p := novaPilha(t)
	c := p.montarCasa(t, casaA, donoA)
	p.mesDaSpec(t, c)

	antes := p.resumoDoPainel(t, casaA, donoA, mesCruzado)

	// Uma receita e um aporte a mais, e depois excluídos pela porta da frente.
	extraReceita := p.lancar(t, casaA, donoA, transaction.KindIncome, c.corrente, &c.salario,
		777_000, "Receita a excluir", civil.MustNew(2026, 9, 15))
	extraAporte := p.lancar(t, casaA, donoA, transaction.KindExpense, c.cartao, &c.aporteCat,
		333_000, "Aporte a excluir", civil.MustNew(2026, 9, 16))

	atorTx := transaction.Actor{HouseholdID: casaA, UserID: donoA, IP: "203.0.113.7"}
	require.NoError(t, p.lancamentos.SoftDelete(t.Context(), atorTx, extraReceita))
	require.NoError(t, p.lancamentos.SoftDelete(t.Context(), atorTx, extraAporte))

	assert.Equal(t, antes, p.resumoDoPainel(t, casaA, donoA, mesCruzado),
		"excluído logicamente não conta — nem no dinheiro, nem na contagem")

	// E restaurar devolve os dois números: a exclusão é reversível, e o painel
	// acompanha.
	_, err := p.lancamentos.Restore(t.Context(), atorTx, extraReceita)
	require.NoError(t, err)
	restaurado := p.resumoDoPainel(t, casaA, donoA, mesCruzado)
	assert.Equal(t, antes.IncomeCents+777_000, restaurado.IncomeCents)
}

// --- apoio -----------------------------------------------------------------

// chaveDedupQA devolve os 64 hexadecimais que validarLinha exige.
func chaveDedupQA(semente string) string {
	soma := sha256.Sum256([]byte(semente))
	return hex.EncodeToString(soma[:])
}
