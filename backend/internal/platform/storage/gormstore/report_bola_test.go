package gormstore_test

import (
	"bytes"
	"encoding/json"
	"testing"

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
	svc := report.NewService(s.transactions, s.categories, logging.New(logs, logging.Options{Level: "debug", Format: "json"}))
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
