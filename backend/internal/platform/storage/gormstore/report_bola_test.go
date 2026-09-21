package gormstore_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BOLA e dado adulterado com os repositórios REAIS e o serviço real montado em
// cima deles. Os testes de `internal/report` usam dublês — que provam a lógica,
// mas não provam que o repositório filtra a casa. Aqui a única coisa que separa
// a casa A da casa B é o `household_id` que o serviço recebe, que em produção
// vem do token.

// relatorioDaCasa monta o serviço real sobre os repositórios do store e
// devolve a resposta e o log produzido.
func relatorioDaCasa(t *testing.T, s *store, casa, mes, kind string) (report.CategoryReportView, string) {
	t.Helper()
	logs := &bytes.Buffer{}
	svc := report.NewService(s.transactions, s.categories, s.accounts, logging.New(logs, logging.Options{Level: "debug", Format: "json"}))
	v, err := svc.ByCategory(t.Context(), report.Actor{HouseholdID: casa, UserID: "u"}, report.ByCategoryInput{Month: mes, Kind: kind})
	require.NoError(t, err)
	return v, logs.String()
}

// Critério 12, ponta a ponta no banco: duas casas povoadas no MESMO mês com os
// MESMOS nomes de categoria. O relatório de A não contém valor, id nem nome de
// B — em nenhuma direção.
func TestRelatorioNaoVazaEntreCasasComNomesIguais(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casaA, casaB := s.duasCasas(t, ctx)
		contaA := s.makeAccount(t, ctx, casaA.ID, "Conta")
		contaB := s.makeAccount(t, ctx, casaB.ID, "Conta")
		set := civil.MustNew(2026, 9, 10)

		// Nomes idênticos nas duas casas — se o filtro fosse por NOME, e não
		// por id e casa, este teste passaria mesmo com o vazamento.
		mercadoA := s.makeCategory(t, ctx, casaA.ID, "Mercado", category.KindExpense, nil)
		padariaA := s.makeCategory(t, ctx, casaA.ID, "Padaria", category.KindExpense, &mercadoA.ID)
		mercadoB := s.makeCategory(t, ctx, casaB.ID, "Mercado", category.KindExpense, nil)
		padariaB := s.makeCategory(t, ctx, casaB.ID, "Padaria", category.KindExpense, &mercadoB.ID)

		s.makeTransaction(t, ctx, casaA.ID, contaA.ID, txSpec{AmountCents: 1_000, OccurredOn: set, CategoryID: &mercadoA.ID})
		s.makeTransaction(t, ctx, casaA.ID, contaA.ID, txSpec{AmountCents: 500, OccurredOn: set, CategoryID: &padariaA.ID})
		// Valores marcantes na casa B: se um deles aparecer em A, o teste vê.
		s.makeTransaction(t, ctx, casaB.ID, contaB.ID, txSpec{AmountCents: 987_654, OccurredOn: set, CategoryID: &mercadoB.ID})
		s.makeTransaction(t, ctx, casaB.ID, contaB.ID, txSpec{AmountCents: 123_456, OccurredOn: set, CategoryID: &padariaB.ID})

		vA, _ := relatorioDaCasa(t, s, casaA.ID, "2026-09", transaction.KindExpense)
		vB, _ := relatorioDaCasa(t, s, casaB.ID, "2026-09", transaction.KindExpense)

		assert.Equal(t, int64(1_500), vA.TotalCents)
		assert.Equal(t, int64(2), vA.Count)
		assert.Equal(t, int64(1_111_110), vB.TotalCents)

		brutoA, err := json.Marshal(vA)
		require.NoError(t, err)
		for _, proibido := range []string{mercadoB.ID, padariaB.ID, "987654", "123456", "1111110"} {
			assert.NotContains(t, string(brutoA), proibido, "o relatório de A carrega algo de B")
		}
		brutoB, err := json.Marshal(vB)
		require.NoError(t, err)
		for _, proibido := range []string{mercadoA.ID, padariaA.ID} {
			assert.NotContains(t, string(brutoB), proibido, "o relatório de B carrega algo de A")
		}

		// A casa de outra pessoa não muda NADA no meu relatório: o mesmo mês,
		// pedido de novo, dá o mesmo número.
		vA2, _ := relatorioDaCasa(t, s, casaA.ID, "2026-09", transaction.KindExpense)
		assert.Equal(t, vA, vA2)
	})
}

// O ponto que o revisor de segurança levanta: o banco foi ADULTERADO e um
// lançamento da casa A aponta para a categoria da casa B. A decisão do plano é
// tolerar (o dinheiro não some do relatório); o que este teste fixa é o PREÇO
// dessa tolerância — que tem de ser zero em sigilo.
func TestRelatorioComCategoriaAdulteradaNaoVazaNomeNemId(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casaA, casaB := s.duasCasas(t, ctx)
		contaA := s.makeAccount(t, ctx, casaA.ID, "Conta")
		set := civil.MustNew(2026, 9, 10)

		minha := s.makeCategory(t, ctx, casaA.ID, "Mercado", category.KindExpense, nil)
		grupoB := s.makeCategory(t, ctx, casaB.ID, "Divorcio", category.KindExpense, nil)
		filhaB := s.makeCategory(t, ctx, casaB.ID, "Advogado", category.KindExpense, &grupoB.ID)

		s.makeTransaction(t, ctx, casaA.ID, contaA.ID, txSpec{AmountCents: 50_000, OccurredOn: set, CategoryID: &minha.ID})
		normal := s.makeTransaction(t, ctx, casaA.ID, contaA.ID, txSpec{AmountCents: 30_000, OccurredOn: set})
		outro := s.makeTransaction(t, ctx, casaA.ID, contaA.ID, txSpec{AmountCents: 20_000, OccurredOn: set})

		// A adulteração: escrita DIRETA na tabela, o que a API nunca faria (o
		// serviço de lançamento valida a categoria contra a casa). É o cenário
		// "atacante com escrita no banco".
		require.NoError(t, s.db.Gorm().WithContext(ctx).Table("transactions").
			Where("id = ?", normal.ID).Update("category_id", grupoB.ID).Error)
		require.NoError(t, s.db.Gorm().WithContext(ctx).Table("transactions").
			Where("id = ?", outro.ID).Update("category_id", filhaB.ID).Error)

		v, log := relatorioDaCasa(t, s, casaA.ID, "2026-09", transaction.KindExpense)

		// 1. O dinheiro continua no relatório, no balde explícito.
		assert.Equal(t, int64(100_000), v.TotalCents, "o total não muda: nada sumiu")
		assert.Equal(t, int64(3), v.Count)
		require.Len(t, v.Items, 2)
		// Empate de valor (50 000 x 50 000): a contagem desempata, e o balde
		// tem duas linhas contra uma — por isso ele vem PRIMEIRO. A busca é
		// pelo balde, não pela posição, para o teste continuar contando a
		// mesma história se a regra de desempate mudar.
		balde, grupo := v.Items[0], v.Items[1]
		if balde.CategoryID != nil {
			balde, grupo = grupo, balde
		}
		require.Nil(t, balde.CategoryID, "as duas linhas adulteradas caem no balde")
		require.NotNil(t, grupo.CategoryID)
		assert.Equal(t, minha.ID, *grupo.CategoryID)
		assert.Equal(t, int64(50_000), grupo.TotalCents)
		assert.Equal(t, int64(50_000), balde.TotalCents)
		assert.Equal(t, int64(2), balde.Count)
		assert.Equal(t, int64(5_000), balde.ShareBp)

		// 2. Nem o id nem o nome da casa B saem na resposta.
		bruto, err := json.Marshal(v)
		require.NoError(t, err)
		for _, proibido := range []string{grupoB.ID, filhaB.ID, "Divorcio", "Advogado"} {
			assert.NotContains(t, string(bruto), proibido)
		}

		// 3. O aviso existe (é anomalia de dado, e some em silêncio seria pior)
		// e leva SÓ o id — nunca o nome, nunca os centavos.
		assert.Contains(t, log, `"level":"WARN"`)
		assert.Contains(t, log, grupoB.ID)
		assert.Contains(t, log, filhaB.ID)
		assert.NotContains(t, log, "Divorcio")
		assert.NotContains(t, log, "Advogado")
		assert.NotContains(t, log, "30000")
		assert.NotContains(t, log, "20000")

		// 4. E a casa B não enxerga o dinheiro de A por causa da adulteração:
		// o lançamento continua sendo da casa A.
		vB, _ := relatorioDaCasa(t, s, casaB.ID, "2026-09", transaction.KindExpense)
		assert.Equal(t, int64(0), vB.TotalCents, "adulterar o category_id não move o lançamento de casa")
		assert.Empty(t, vB.Items)
	})
}

// Filha cujo PAI é de outra casa (a outra metade da adulteração): ela é
// promovida a grupo próprio — o nome dela é da casa, então aparece; o do pai
// alheio, não.
func TestRelatorioComPaiDeOutraCasaPromoveAFilha(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casaA, casaB := s.duasCasas(t, ctx)
		contaA := s.makeAccount(t, ctx, casaA.ID, "Conta")
		set := civil.MustNew(2026, 9, 10)

		paiB := s.makeCategory(t, ctx, casaB.ID, "Segredo", category.KindExpense, nil)
		grupoA := s.makeCategory(t, ctx, casaA.ID, "Alimentacao", category.KindExpense, nil)
		filhaA := s.makeCategory(t, ctx, casaA.ID, "Padaria", category.KindExpense, &grupoA.ID)

		// Adulteração: a filha de A passa a apontar para o grupo de B.
		require.NoError(t, s.db.Gorm().WithContext(ctx).Table("categories").
			Where("id = ?", filhaA.ID).Update("parent_id", paiB.ID).Error)

		s.makeTransaction(t, ctx, casaA.ID, contaA.ID, txSpec{AmountCents: 1_234, OccurredOn: set, CategoryID: &filhaA.ID})

		v, log := relatorioDaCasa(t, s, casaA.ID, "2026-09", transaction.KindExpense)

		require.Len(t, v.Items, 1)
		require.NotNil(t, v.Items[0].CategoryID)
		assert.Equal(t, filhaA.ID, *v.Items[0].CategoryID, "a filha vira grupo, e o dinheiro fica")
		assert.Equal(t, int64(1_234), v.Items[0].TotalCents)
		assert.Equal(t, int64(report.BasisPointsTotal), v.Items[0].ShareBp)

		bruto, err := json.Marshal(v)
		require.NoError(t, err)
		assert.NotContains(t, string(bruto), paiB.ID)
		assert.NotContains(t, string(bruto), "Segredo")

		assert.Contains(t, log, `"level":"WARN"`)
		assert.Contains(t, log, filhaA.ID, "o aviso leva o id da filha, que é da própria casa")
		assert.NotContains(t, log, "Segredo")
	})
}

// BOLA do RECORTE (ADR-032): duas casas com cartões de MESMO NOME e despesas
// no mesmo mês. O `credit` de uma nunca contém conta nem categoria da outra —
// e o conjunto de cartões de cada uma sai da lista da PRÓPRIA casa.
//
// Nomes idênticos são o ponto: se o recorte casasse por nome, ou se o
// household_id sumisse do WHERE das contas, um cenário com nomes diferentes
// só mostraria "número errado"; com nomes iguais, a única forma de A ver algo
// de B é o escopo ter falhado.
func TestRelatorioRecorteNaoVazaEntreCasasComCartoesDeMesmoNome(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casaA, casaB := s.duasCasas(t, ctx)
		set := civil.MustNew(2026, 9, 10)

		cartaoA := s.makeAccountKind(t, ctx, casaA.ID, "Cartao Roxo", account.KindCreditCard, false)
		correnteA := s.makeAccountKind(t, ctx, casaA.ID, "Conta Corrente", account.KindChecking, false)
		cartaoB := s.makeAccountKind(t, ctx, casaB.ID, "Cartao Roxo", account.KindCreditCard, false)
		correnteB := s.makeAccountKind(t, ctx, casaB.ID, "Conta Corrente", account.KindChecking, false)

		mercadoA := s.makeCategory(t, ctx, casaA.ID, "Mercado", category.KindExpense, nil)
		mercadoB := s.makeCategory(t, ctx, casaB.ID, "Mercado", category.KindExpense, nil)

		s.makeTransaction(t, ctx, casaA.ID, cartaoA.ID, txSpec{AmountCents: 1_000, OccurredOn: set, CategoryID: &mercadoA.ID})
		s.makeTransaction(t, ctx, casaA.ID, correnteA.ID, txSpec{AmountCents: 2_000, OccurredOn: set, CategoryID: &mercadoA.ID})
		// Valores marcantes na casa B: se um deles aparecer em A, o teste vê.
		s.makeTransaction(t, ctx, casaB.ID, cartaoB.ID, txSpec{AmountCents: 987_654, OccurredOn: set, CategoryID: &mercadoB.ID})
		s.makeTransaction(t, ctx, casaB.ID, correnteB.ID, txSpec{AmountCents: 123_456, OccurredOn: set, CategoryID: &mercadoB.ID})

		pedir := func(casa, grupo string) (report.CategoryReportView, string) {
			logs := &bytes.Buffer{}
			svc := report.NewService(s.transactions, s.categories, s.accounts,
				logging.New(logs, logging.Options{Level: "debug", Format: "json"}))
			v, err := svc.ByCategory(t.Context(), report.Actor{HouseholdID: casa, UserID: "u"},
				report.ByCategoryInput{Month: "2026-09", Kind: transaction.KindExpense, AccountGroup: grupo})
			require.NoError(t, err)
			return v, logs.String()
		}

		creditoA, logA := pedir(casaA.ID, report.AccountGroupCredit)
		debitoA, _ := pedir(casaA.ID, report.AccountGroupDebit)
		creditoB, _ := pedir(casaB.ID, report.AccountGroupCredit)
		debitoB, _ := pedir(casaB.ID, report.AccountGroupDebit)

		assert.Equal(t, int64(1_000), creditoA.TotalCents, "só o cartão da casa A")
		assert.Equal(t, int64(2_000), debitoA.TotalCents)
		assert.Equal(t, int64(987_654), creditoB.TotalCents, "só o cartão da casa B")
		assert.Equal(t, int64(123_456), debitoB.TotalCents)

		// Nada de B na resposta de A (e vice-versa), em nenhum dos recortes.
		for nome, v := range map[string]report.CategoryReportView{"credit": creditoA, "debit": debitoA} {
			bruto, err := json.Marshal(v)
			require.NoError(t, err)
			for _, proibido := range []string{cartaoB.ID, correnteB.ID, mercadoB.ID, casaB.ID, "987654", "123456"} {
				assert.NotContains(t, string(bruto), proibido, "o %s de A carrega algo de B", nome)
			}
		}
		for nome, v := range map[string]report.CategoryReportView{"credit": creditoB, "debit": debitoB} {
			bruto, err := json.Marshal(v)
			require.NoError(t, err)
			for _, proibido := range []string{cartaoA.ID, correnteA.ID, mercadoA.ID, casaA.ID} {
				assert.NotContains(t, string(bruto), proibido, "o %s de B carrega algo de A", nome)
			}
		}

		// Nenhum aviso: todas as contas das duas casas existem nas suas casas.
		assert.NotContains(t, logA, `"level":"WARN"`)

		// E a soma dos dois recortes é o mês inteiro, em cada casa.
		todasA, _ := pedir(casaA.ID, "")
		assert.Equal(t, todasA.TotalCents, creditoA.TotalCents+debitoA.TotalCents)
		todasB, _ := pedir(casaB.ID, "")
		assert.Equal(t, todasB.TotalCents, creditoB.TotalCents+debitoB.TotalCents)
	})
}

// A outra metade da adulteração: um lançamento da casa A aponta para o CARTÃO
// da casa B. O recorte não pode "adotar" o cartão alheio — a conta não está na
// lista da casa A, então a linha não é cartão: ela cai em `debit` (falha
// ABERTA, o dinheiro não some) com um aviso que leva só o id.
func TestRelatorioRecorteComContaDeOutraCasaNaoAdotaOCartao(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casaA, casaB := s.duasCasas(t, ctx)
		set := civil.MustNew(2026, 9, 10)

		cartaoA := s.makeAccountKind(t, ctx, casaA.ID, "Cartao Meu", account.KindCreditCard, false)
		cartaoB := s.makeAccountKind(t, ctx, casaB.ID, "Cartao Secreto", account.KindCreditCard, false)
		mercadoA := s.makeCategory(t, ctx, casaA.ID, "Mercado", category.KindExpense, nil)

		s.makeTransaction(t, ctx, casaA.ID, cartaoA.ID, txSpec{AmountCents: 1_000, OccurredOn: set, CategoryID: &mercadoA.ID})
		adulterada := s.makeTransaction(t, ctx, casaA.ID, cartaoA.ID, txSpec{AmountCents: 30_000, OccurredOn: set, CategoryID: &mercadoA.ID})
		// Escrita DIRETA na tabela: a API nunca aceitaria conta de outra casa.
		require.NoError(t, s.db.Gorm().WithContext(ctx).Table("transactions").
			Where("id = ?", adulterada.ID).Update("account_id", cartaoB.ID).Error)

		pedir := func(grupo string) (report.CategoryReportView, string) {
			logs := &bytes.Buffer{}
			svc := report.NewService(s.transactions, s.categories, s.accounts,
				logging.New(logs, logging.Options{Level: "debug", Format: "json"}))
			v, err := svc.ByCategory(t.Context(), report.Actor{HouseholdID: casaA.ID, UserID: "u"},
				report.ByCategoryInput{Month: "2026-09", Kind: transaction.KindExpense, AccountGroup: grupo})
			require.NoError(t, err)
			return v, logs.String()
		}

		credito, logCredito := pedir(report.AccountGroupCredit)
		debito, logDebito := pedir(report.AccountGroupDebit)
		todas, _ := pedir("")

		assert.Equal(t, int64(1_000), credito.TotalCents, "o cartão da casa B não vira cartão da casa A")
		assert.Equal(t, int64(30_000), debito.TotalCents, "falha ABERTA: o dinheiro do lançamento aparece")
		assert.Equal(t, todas.TotalCents, credito.TotalCents+debito.TotalCents)

		for nome, log := range map[string]string{"credit": logCredito, "debit": logDebito} {
			assert.Equal(t, 1, strings.Count(log, `"level":"WARN"`), "%s: UM aviso por requisição", nome)
			assert.Contains(t, log, `"conta_desconhecida":1`, nome)
			assert.Contains(t, log, cartaoB.ID, "%s: o aviso leva o id", nome)
			assert.NotContains(t, log, "Secreto", "%s: nunca o nome da conta", nome)
			assert.NotContains(t, log, "30000", "%s: nunca centavos", nome)
		}

		// E a casa B continua sem ver o dinheiro: adulterar o account_id não
		// move o lançamento de casa.
		logs := &bytes.Buffer{}
		svcB := report.NewService(s.transactions, s.categories, s.accounts,
			logging.New(logs, logging.Options{Level: "debug", Format: "json"}))
		vB, err := svcB.ByCategory(ctx, report.Actor{HouseholdID: casaB.ID, UserID: "v"},
			report.ByCategoryInput{Month: "2026-09", AccountGroup: report.AccountGroupCredit})
		require.NoError(t, err)
		assert.Equal(t, int64(0), vB.TotalCents)
	})
}
