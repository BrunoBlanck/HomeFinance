package dashboard_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/dashboard"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Os testes de serviço provam a DOBRA: os três números do mês saem das linhas
// agregadas sem que nenhuma subtração aconteça em cima de uma desigualdade não
// verificada, e sem que nenhum deles seja calculado duas vezes.
//
// Cenários 1, 2 e 3 da spec 0008 §8 estão aqui, com dublês. Os cruzados
// (critérios 4 a 7, 15 e 17), o abuso de handler (13, 14, 16) e o volume ficam
// com o `qa-testes`.

const (
	minhaCasa = "casa-1"
	outraCasa = "casa-2"
	usuario   = "11111111-1111-7111-8111-111111111111"
	mes       = "2026-09"

	contaCorrente = "conta-corrente"
	cartao        = "conta-cartao"
	cartaoVelho   = "conta-cartao-arquivado"
)

var arquivadaEm = time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC)

func ator(casa string) dashboard.Actor {
	return dashboard.Actor{HouseholdID: casa, UserID: usuario}
}

// --- dublês ---------------------------------------------------------------

type chamadaLedger struct {
	casa, mes string
	marcadas  []string
}

// ledgerFake devolve as linhas programadas e registra COM QUE ARGUMENTOS foi
// chamado — é assim que o teste prova que a casa é a do token e que o conjunto
// de categorias que vira `IN (?)` saiu da lista da própria casa.
type ledgerFake struct {
	rows     []dashboard.KindAccountTotals
	err      error
	chamadas []chamadaLedger
}

func (l *ledgerFake) SumMonthByKindAndAccount(_ context.Context, householdID, competenceMonth string,
	markedCategoryIDs []string,
) ([]dashboard.KindAccountTotals, error) {
	l.chamadas = append(l.chamadas, chamadaLedger{householdID, competenceMonth, markedCategoryIDs})
	if l.err != nil {
		return nil, l.err
	}
	return l.rows, nil
}

type chamadaLista struct {
	casa            string
	includeArchived bool
}

// categoriasFake filtra por casa como o repositório real: categoria de outra
// casa NÃO é devolvida.
type categoriasFake struct {
	cats     []category.Category
	err      error
	chamadas []chamadaLista
}

func (c *categoriasFake) List(_ context.Context, householdID string, includeArchived bool) ([]category.Category, error) {
	c.chamadas = append(c.chamadas, chamadaLista{householdID, includeArchived})
	if c.err != nil {
		return nil, c.err
	}
	var out []category.Category
	for _, cat := range c.cats {
		if cat.HouseholdID != householdID {
			continue
		}
		if !includeArchived && cat.ArchivedAt != nil {
			continue
		}
		out = append(out, cat)
	}
	return out, nil
}

// contasFake filtra por casa e por arquivamento como o repositório real.
type contasFake struct {
	accs     []account.Account
	err      error
	chamadas []chamadaLista
}

func (c *contasFake) List(_ context.Context, householdID string, includeArchived bool) ([]account.Account, error) {
	c.chamadas = append(c.chamadas, chamadaLista{householdID, includeArchived})
	if c.err != nil {
		return nil, c.err
	}
	var out []account.Account
	for _, a := range c.accs {
		if a.HouseholdID != householdID {
			continue
		}
		if !includeArchived && a.ArchivedAt != nil {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

type ambiente struct {
	ledger *ledgerFake
	cats   *categoriasFake
	contas *contasFake
	logs   *bytes.Buffer
	svc    *dashboard.Service
}

func novoAmbiente(t *testing.T) *ambiente {
	t.Helper()
	logs := &bytes.Buffer{}
	lg := logging.New(logs, logging.Options{Level: "debug", Format: "json"})
	a := &ambiente{
		ledger: &ledgerFake{},
		cats:   &categoriasFake{},
		contas: &contasFake{},
		logs:   logs,
	}
	a.svc = dashboard.NewService(a.ledger, a.cats, a.contas, lg)
	return a
}

func (a *ambiente) categoria(casa, id, kind string, arquivada bool) {
	c := category.Category{ID: id, HouseholdID: casa, Name: id, Kind: kind}
	if arquivada {
		at := arquivadaEm
		c.ArchivedAt = &at
	}
	a.cats.cats = append(a.cats.cats, c)
}

func (a *ambiente) conta(casa, id, kind string, arquivada bool) {
	acc := account.Account{ID: id, HouseholdID: casa, Name: id, Kind: kind}
	if arquivada {
		at := arquivadaEm
		acc.ArchivedAt = &at
	}
	a.contas.accs = append(a.contas.accs, acc)
}

// casaComum monta a casa do cenário 1: uma conta corrente, um cartão vivo e
// as duas categorias que marcam investimento.
func (a *ambiente) casaComum() {
	a.conta(minhaCasa, contaCorrente, account.KindChecking, false)
	a.conta(minhaCasa, cartao, account.KindCreditCard, false)
	a.categoria(minhaCasa, "cat-aporte", category.KindInvestment, false)
	a.categoria(minhaCasa, "cat-resgate", category.KindRedemption, false)
}

// --- cenários da spec §8 ---------------------------------------------------

// Cenário 1: 3 receitas (R$ 5.000), 2 despesas comuns (R$ 800), 1 aporte de
// R$ 2.000 e 1 resgate de R$ 350.
//
// O resgate chega ao serviço DENTRO da linha de receita (é um `income`
// marcado), e é a subtração no servidor que o tira da receita: o mesmo
// dinheiro não pode aparecer duas vezes na mesma faixa.
func TestCenario1NumerosDoMes(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.rows = []dashboard.KindAccountTotals{
		// 3 receitas de 500000 no total + 1 resgate de 35000.
		{Kind: transaction.KindIncome, AccountID: contaCorrente, Count: 4, TotalCents: 535_000,
			MarkedCount: 1, MarkedTotalCents: 35_000},
		// 2 despesas comuns de 80000 no total + 1 aporte de 200000.
		{Kind: transaction.KindExpense, AccountID: contaCorrente, Count: 3, TotalCents: 280_000,
			MarkedCount: 1, MarkedTotalCents: 200_000},
	}

	view, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
	require.NoError(t, err)

	assert.Equal(t, mes, view.Month)
	assert.Equal(t, int64(500_000), view.IncomeCents, "receita sem os resgates")
	assert.Equal(t, int64(3), view.IncomeCount)
	assert.Equal(t, int64(165_000), view.InvestmentNetCents, "200000 de aporte menos 35000 de resgate")
	assert.Equal(t, int64(2), view.InvestmentCount, "aportes + resgates, nunca a diferença")
	assert.Zero(t, view.CreditCardExpenseCents, "nada foi gasto no cartão")
	assert.Zero(t, view.CreditCardExpenseCount)
	assert.Equal(t, int64(1), view.CreditCardAccountCount)
	assert.Equal(t, int64(2), view.InvestmentCategoryCount)

	// O conjunto que virou `IN (?)` é o da PRÓPRIA casa, com as arquivadas
	// incluídas, e a consulta foi UMA só.
	require.Len(t, a.ledger.chamadas, 1)
	assert.Equal(t, minhaCasa, a.ledger.chamadas[0].casa)
	assert.Equal(t, mes, a.ledger.chamadas[0].mes)
	assert.Equal(t, []string{"cat-aporte", "cat-resgate"}, a.ledger.chamadas[0].marcadas)
	require.Len(t, a.cats.chamadas, 1)
	assert.True(t, a.cats.chamadas[0].includeArchived, "arquivar não desfaz a marcação do passado")
	require.Len(t, a.contas.chamadas, 1)
	assert.True(t, a.contas.chamadas[0].includeArchived, "gasto de cartão arquivado continua contando")
}

// Cenário 2: o resgate supera o aporte — o líquido é NEGATIVO, e isso é
// resposta, não erro. É a única subtração do painel que pode dar negativo.
func TestCenario2LiquidoNegativo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.rows = []dashboard.KindAccountTotals{
		{Kind: transaction.KindIncome, AccountID: contaCorrente, Count: 1, TotalCents: 90_000,
			MarkedCount: 1, MarkedTotalCents: 90_000},
		{Kind: transaction.KindExpense, AccountID: contaCorrente, Count: 1, TotalCents: 50_000,
			MarkedCount: 1, MarkedTotalCents: 50_000},
	}

	view, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
	require.NoError(t, err)

	assert.Equal(t, int64(-40_000), view.InvestmentNetCents, "aporte 50000 − resgate 90000")
	assert.Equal(t, int64(2), view.InvestmentCount)
	// A receita do mês era SÓ o resgate: tirá-lo zera a faixa "Entrou", e o
	// zero aqui é a resposta certa — não pode virar negativo.
	assert.Zero(t, view.IncomeCents)
	assert.Zero(t, view.IncomeCount)
}

// Cenário 3: mês sem nenhum investimento — zeros, nunca ausentes e nunca
// nulos. E, sem categoria marcada, o conjunto que vai ao repositório é VAZIO
// (o `IN ()` não é emitido lá dentro; aqui se prova que ele chega vazio).
func TestCenario3MesSemInvestimento(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.conta(minhaCasa, contaCorrente, account.KindChecking, false)
	a.conta(minhaCasa, cartao, account.KindCreditCard, false)
	a.categoria(minhaCasa, "cat-mercado", category.KindExpense, false)
	a.ledger.rows = []dashboard.KindAccountTotals{
		{Kind: transaction.KindIncome, AccountID: contaCorrente, Count: 1, TotalCents: 300_000},
		{Kind: transaction.KindExpense, AccountID: cartao, Count: 2, TotalCents: 45_000},
	}

	view, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
	require.NoError(t, err)

	assert.Zero(t, view.InvestmentNetCents)
	assert.Zero(t, view.InvestmentCount)
	assert.Zero(t, view.InvestmentCategoryCount)
	assert.Equal(t, int64(300_000), view.IncomeCents)
	assert.Equal(t, int64(45_000), view.CreditCardExpenseCents)
	assert.Equal(t, int64(2), view.CreditCardExpenseCount)

	require.Len(t, a.ledger.chamadas, 1)
	assert.Empty(t, a.ledger.chamadas[0].marcadas, "sem categoria marcada, o conjunto vai VAZIO")

	// Nenhum campo vira `null` ou some: o contrato os declara todos
	// obrigatórios, e um número ausente obrigaria a tela a inventar.
	bruto, err := json.Marshal(view)
	require.NoError(t, err)
	assert.NotContains(t, string(bruto), "null")
}

// --- cartão ----------------------------------------------------------------

// Critério 12: cartão ARQUIVADO com gasto no mês — o gasto conta (arquivar não
// apaga o passado), o contador de cartões não o conta (ele responde "você tem
// um cartão cadastrado?"). São critérios diferentes DE PROPÓSITO.
//
// Critérios 8 e 9 vêm de brinde na mesma tabela: dois cartões viram UM número,
// e a despesa em `checking` fica de fora dele.
func TestCartaoArquivadoContaNoGastoMasNaoNoContador(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.conta(minhaCasa, contaCorrente, account.KindChecking, false)
	a.conta(minhaCasa, cartao, account.KindCreditCard, false)
	a.conta(minhaCasa, cartaoVelho, account.KindCreditCard, true)
	a.ledger.rows = []dashboard.KindAccountTotals{
		{Kind: transaction.KindExpense, AccountID: cartao, Count: 3, TotalCents: 30_000},
		{Kind: transaction.KindExpense, AccountID: cartaoVelho, Count: 1, TotalCents: 12_000},
		{Kind: transaction.KindExpense, AccountID: contaCorrente, Count: 5, TotalCents: 99_000},
	}

	view, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
	require.NoError(t, err)

	assert.Equal(t, int64(42_000), view.CreditCardExpenseCents, "os dois cartões num número só")
	assert.Equal(t, int64(4), view.CreditCardExpenseCount)
	assert.Equal(t, int64(1), view.CreditCardAccountCount, "o arquivado não conta aqui")
}

// Critério 10: aporte lançado NO CARTÃO entra no líquido e sai do gasto do
// cartão — o mesmo dinheiro não é contado duas vezes na mesma faixa.
func TestAporteNoCartaoSaiDoGastoDoCartao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.rows = []dashboard.KindAccountTotals{
		{Kind: transaction.KindExpense, AccountID: cartao, Count: 4, TotalCents: 100_000,
			MarkedCount: 1, MarkedTotalCents: 70_000},
	}

	view, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
	require.NoError(t, err)

	assert.Equal(t, int64(30_000), view.CreditCardExpenseCents)
	assert.Equal(t, int64(3), view.CreditCardExpenseCount)
	assert.Equal(t, int64(70_000), view.InvestmentNetCents)
	assert.Equal(t, int64(1), view.InvestmentCount)
}

// Critério 11: casa sem nenhum cartão — zero no gasto E zero no contador, que
// é o par que deixa a tela mostrar texto em vez de `R$ 0,00`.
func TestCasaSemCartaoTemOsDoisZeros(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.conta(minhaCasa, contaCorrente, account.KindChecking, false)
	a.ledger.rows = []dashboard.KindAccountTotals{
		{Kind: transaction.KindExpense, AccountID: contaCorrente, Count: 2, TotalCents: 80_000},
	}

	view, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
	require.NoError(t, err)

	assert.Zero(t, view.CreditCardExpenseCents)
	assert.Zero(t, view.CreditCardExpenseCount)
	assert.Zero(t, view.CreditCardAccountCount)
}

// --- escopo e guardas ------------------------------------------------------

// A casa vem do TOKEN: conta e categoria de OUTRA casa não entram em conjunto
// nenhum. Aqui a casa B tem nomes idênticos aos da casa A, e mesmo assim não
// vê nada dela.
func TestCasaDoTokenNaoEnxergaConfiguracaoDaOutra(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.conta(outraCasa, "conta-cartao-b", account.KindCreditCard, false)
	a.categoria(outraCasa, "cat-aporte-b", category.KindInvestment, false)

	_, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
	require.NoError(t, err)

	require.Len(t, a.ledger.chamadas, 1)
	assert.Equal(t, []string{"cat-aporte", "cat-resgate"}, a.ledger.chamadas[0].marcadas,
		"nenhum id da outra casa pode chegar ao IN (?)")
}

// Casa vazia é ErrUnauthenticated ANTES de qualquer consulta — defesa em
// profundidade, mesmo com o handler já barrando.
func TestSemCasaNaoConsultaNada(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	_, err := a.svc.Summary(context.Background(), ator(""), dashboard.SummaryInput{Month: mes})
	require.ErrorIs(t, err, dashboard.ErrUnauthenticated)
	assert.Empty(t, a.ledger.chamadas)
	assert.Empty(t, a.cats.chamadas)
	assert.Empty(t, a.contas.chamadas)
}

// Mês malformado é recusado ANTES do banco, e nada é normalizado.
func TestMesInvalidoNaoConsultaNada(t *testing.T) {
	t.Parallel()

	for nome, valor := range map[string]string{
		"ausente":         "",
		"mês 13":          "2026-13",
		"sem zero":        "2026-9",
		"espaço à frente": " 2026-09",
		"com dia":         "2026-09-01",
	} {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			a := novoAmbiente(t)
			a.casaComum()

			_, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: valor})
			require.ErrorIs(t, err, transaction.ErrInvalidMonth)
			assert.Empty(t, a.ledger.chamadas)
			assert.Empty(t, a.cats.chamadas)
			assert.Empty(t, a.contas.chamadas)
		})
	}
}

// --- invariantes -----------------------------------------------------------

// Desigualdade violada (`marcado > total`, em centavos ou em contagem) é falha
// FECHADA: erro, view ZERADA, nenhum número inventado e nenhum clamp
// silencioso.
func TestMarcadoMaiorQueTotalFalhaFechado(t *testing.T) {
	t.Parallel()

	casos := map[string]dashboard.KindAccountTotals{
		"centavos marcados acima do total": {
			Kind: transaction.KindIncome, AccountID: contaCorrente,
			Count: 2, TotalCents: 10_000, MarkedCount: 1, MarkedTotalCents: 90_000,
		},
		"contagem marcada acima da total": {
			Kind: transaction.KindExpense, AccountID: contaCorrente,
			Count: 1, TotalCents: 10_000, MarkedCount: 7, MarkedTotalCents: 1_000,
		},
		"total negativo": {
			Kind: transaction.KindIncome, AccountID: contaCorrente,
			Count: 1, TotalCents: -1,
		},
		"marcado negativo": {
			Kind: transaction.KindExpense, AccountID: contaCorrente,
			Count: 1, TotalCents: 10_000, MarkedTotalCents: -1,
		},
	}
	for nome, linha := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			a := novoAmbiente(t)
			a.casaComum()
			a.ledger.rows = []dashboard.KindAccountTotals{linha}

			view, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
			require.Error(t, err)
			assert.Equal(t, dashboard.SummaryView{}, view, "nenhum número inventado")
			// Não é 4xx: não é entrada do usuário, é banco em estado que a
			// aplicação não produz.
			assert.NotErrorIs(t, err, transaction.ErrInvalidMonth)
			assert.NotErrorIs(t, err, dashboard.ErrUnauthenticated)
		})
	}
}

// Linhas demais também falha FECHADO, e a contagem vai para a mensagem que o
// handler registra no log.
func TestLinhasDemaisFalhaFechado(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	for i := 0; i <= 2*account.MaxPerHousehold; i++ {
		a.ledger.rows = append(a.ledger.rows, dashboard.KindAccountTotals{
			Kind: transaction.KindIncome, AccountID: contaCorrente, Count: 1, TotalCents: 1,
		})
	}

	view, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
	require.Error(t, err)
	assert.Equal(t, dashboard.SummaryView{}, view)
}

// O erro de teto de categorias sobe EMBRULHADO do repositório — quem o
// traduzir tem de usar errors.Is, nunca `==`. O serviço não pode engoli-lo nem
// transformá-lo em zeros.
func TestErroDoRepositorioSobeEmbrulhado(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	// Exatamente como o gormstore o devolve: EMBRULHADO.
	a.ledger.err = fmt.Errorf("resumindo o mês do painel: %w", transaction.ErrTooManyCategories)

	_, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
	require.ErrorIs(t, err, transaction.ErrTooManyCategories)
}

// --- anomalia de dado ------------------------------------------------------

// Conta fora da lista da casa falha ABERTA: o dinheiro continua na receita,
// cartão ela não é, e o aviso é UM só por requisição — com contagem e amostra
// de ids, nunca centavos e nunca nome.
func TestContaForaDaListaFalhaAbertaComUmAvisoSo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.rows = []dashboard.KindAccountTotals{
		{Kind: transaction.KindIncome, AccountID: "conta-orfa-1", Count: 1, TotalCents: 70_000},
		{Kind: transaction.KindExpense, AccountID: "conta-orfa-2", Count: 1, TotalCents: 25_000},
		{Kind: transaction.KindIncome, AccountID: contaCorrente, Count: 1, TotalCents: 30_000},
	}

	view, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
	require.NoError(t, err)

	assert.Equal(t, int64(100_000), view.IncomeCents, "o dinheiro da linha órfã não some")
	assert.Zero(t, view.CreditCardExpenseCents, "conta órfã não vira cartão")

	logs := a.logs.String()
	assert.Equal(t, 1, strings.Count(logs, "\"level\":\"WARN\""), "UM aviso por requisição")
	assert.Contains(t, logs, "\"conta_desconhecida\":2")
	assert.Contains(t, logs, "conta-orfa-1")
	assert.NotContains(t, logs, "70000", "centavos nunca vão para o log")
}

// Kind fora de income/expense não entra em número nenhum: se uma perna de
// transferência vazasse da consulta, somá-la ao cartão faria PAGAR a fatura
// parecer gasto novo.
func TestKindInesperadoNaoEntraEmNumeroNenhum(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.rows = []dashboard.KindAccountTotals{
		{Kind: transaction.KindTransferIn, AccountID: cartao, Count: 1, TotalCents: 100_000},
		{Kind: transaction.KindIncome, AccountID: contaCorrente, Count: 1, TotalCents: 30_000},
	}

	view, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
	require.NoError(t, err)

	assert.Equal(t, int64(30_000), view.IncomeCents)
	assert.Zero(t, view.CreditCardExpenseCents)
	assert.Contains(t, a.logs.String(), "\"kind_inesperado\":1")
}

// Falha ao carregar a taxonomia ou as contas NÃO vira mês zerado: o painel
// prefere 500 a dizer "você não gastou nada".
func TestFalhaAoCarregarListasNaoViraMesZerado(t *testing.T) {
	t.Parallel()

	t.Run("categorias", func(t *testing.T) {
		t.Parallel()
		a := novoAmbiente(t)
		a.casaComum()
		a.cats.err = errors.New("banco fora")

		_, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
		require.Error(t, err)
		assert.Empty(t, a.ledger.chamadas, "nada é consultado sem a taxonomia")
	})

	t.Run("contas", func(t *testing.T) {
		t.Parallel()
		a := novoAmbiente(t)
		a.casaComum()
		a.contas.err = errors.New("banco fora")

		_, err := a.svc.Summary(context.Background(), ator(minhaCasa), dashboard.SummaryInput{Month: mes})
		require.Error(t, err)
		assert.Empty(t, a.ledger.chamadas, "nada é consultado sem as contas")
	})
}
