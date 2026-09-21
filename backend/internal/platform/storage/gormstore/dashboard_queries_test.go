package gormstore_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/dashboard"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Consulta agregada do painel (spec 0008, ADR-031b): `GROUP BY kind,
// account_id` sobre a competência, com as duas colunas condicionais entrando
// só quando a casa marca investimento.
//
// O que estes testes guardam, e que um teste de RESULTADO não alcançaria:
//
//   - a forma do comando EMITIDO (4 colunas × 6 colunas), porque é ela que
//     decide se `IN ()` vai ao banco — erro de sintaxe em três dialetos e
//     `1=0` no quarto (ADR-029f);
//   - o ORÇAMENTO de parâmetros medido, que é o motivo nº 2 de a conta estar na
//     CHAVE e não na projeção (a forma rejeitada dava 1002, acima do piso
//     histórico de 999 do SQLite);
//   - a despesa de cartão SEM categoria aparecendo no total bruto, que é o
//     motivo nº 1 (a forma rejeitada a perderia em silêncio, justo no mês em
//     que se acabou de importar a fatura).

const mesDoPainel = "2026-09"

// cenarioDoPainel é o mês montado uma vez e lido por vários testes: os números
// estão todos aqui para quem lê não precisar garimpá-los no meio das
// asserções.
type cenarioDoPainel struct {
	casa     string
	alheia   string
	corrente string
	cartao   string

	// marcadas são os ids das categorias de natureza investment/redemption da
	// casa — o MESMO conjunto que o serviço passa, vindo de uma leitura da
	// taxonomia com as arquivadas incluídas.
	marcadas []string

	// contasDaAlheia serve à asserção de isolamento: nenhuma delas pode
	// aparecer na agregação da casa de cá.
	contasDaAlheia []string
}

// contaDeCartao cria uma conta de tipo credit_card direto no repositório.
//
// O tipo da conta NÃO entra nesta consulta — a conta é chave de agrupamento, e
// a interseção com o conjunto de cartões é feita em Go pelo serviço, sobre a
// lista de contas da própria casa (é por isso que id de conta nunca vai ao
// SQL). O tipo está aqui para o cenário descrever o caso real: "a despesa do
// cartão sem categoria".
func contaDeCartao(t *testing.T, ctx context.Context, s *store, householdID, nome string) *account.Account {
	t.Helper()

	nomeOK, norm, err := account.NormalizeName(nome)
	require.NoError(t, err)

	a := &account.Account{
		ID:                  s.nextID("a"),
		HouseholdID:         householdID,
		Name:                nomeOK,
		NameNorm:            norm,
		Kind:                account.KindCreditCard,
		OpeningBalanceCents: 0,
		OpeningDate:         civil.MustNew(2026, 1, 1),
		CreatedAt:           now(),
		UpdatedAt:           now(),
	}
	require.NoError(t, s.accounts.Create(ctx, a))
	return a
}

// montarCenarioDoPainel monta um mês com TODOS os casos que a consulta precisa
// distinguir, e uma casa vizinha com dados EQUIVALENTES — mesmos valores,
// mesmo mês, mesmos nomes. Dado equivalente é o que faz o teste de isolamento
// valer: se o `household_id` sumisse do WHERE, os totais DOBRARIAM, e um
// cenário com valores diferentes só provaria que a busca falha.
//
// Mês 2026-09 da casa de cá:
//
//	(income,  corrente) 500.000 sem categoria + 100.000 marcado (resgate)
//	(expense, corrente)  20.000 em "Mercado"
//	(expense, cartão)    30.000 SEM categoria + 50.000 marcado (aporte) + 15.000 "Mercado"
//	transferência corrente → cartão de 400.000 (as DUAS pernas)
//	ruído: 999.900 de outro mês, 777.700 excluído logicamente
func montarCenarioDoPainel(t *testing.T, s *store) cenarioDoPainel {
	t.Helper()

	ctx := t.Context()
	minha, alheia := s.duasCasas(t, ctx)
	corrente := s.makeAccount(t, ctx, minha.ID, "Conta Corrente")
	cartao := contaDeCartao(t, ctx, s, minha.ID, "Cartão Roxo")

	cdb := s.makeCategory(t, ctx, minha.ID, "CDB", category.KindInvestment, nil)
	resgate := s.makeCategory(t, ctx, minha.ID, "Resgate CDB", category.KindRedemption, nil)
	mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)

	dia := civil.MustNew(2026, 9, 10)

	// --- receita ------------------------------------------------------------
	s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
		Kind: transaction.KindIncome, AmountCents: 500_000, OccurredOn: dia, Description: "Salário",
	})
	s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
		Kind: transaction.KindIncome, AmountCents: 100_000, OccurredOn: dia,
		CategoryID: &resgate.ID, Description: "Resgate",
	})

	// --- despesa na conta corrente -----------------------------------------
	s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
		Kind: transaction.KindExpense, AmountCents: 20_000, OccurredOn: dia, CategoryID: &mercado.ID,
	})

	// --- despesa no cartão, incluindo a SEM categoria -----------------------
	s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
		Kind: transaction.KindExpense, AmountCents: 30_000, OccurredOn: dia,
		Description: "Compra importada da fatura", // sem categoria: o estado normal pós-importação
	})
	s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
		Kind: transaction.KindExpense, AmountCents: 50_000, OccurredOn: dia, CategoryID: &cdb.ID,
		Description: "Aporte no cartão",
	})
	s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
		Kind: transaction.KindExpense, AmountCents: 15_000, OccurredOn: dia, CategoryID: &mercado.ID,
	})

	// --- transferência interna: as DUAS pernas ------------------------------
	grupo := s.nextID("tg")
	s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
		Kind: transaction.KindTransferOut, AmountCents: 400_000, OccurredOn: dia,
		TransferGroupID: &grupo, Description: "Pagamento da fatura",
	})
	s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
		Kind: transaction.KindTransferIn, AmountCents: 400_000, OccurredOn: dia,
		TransferGroupID: &grupo, Description: "Pagamento recebido",
	})

	// --- ruído que o WHERE precisa podar ------------------------------------
	s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
		Kind: transaction.KindIncome, AmountCents: 999_900, OccurredOn: civil.MustNew(2026, 10, 5),
	})
	excluida := s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
		Kind: transaction.KindExpense, AmountCents: 777_700, OccurredOn: dia,
	})
	require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))

	// --- a casa vizinha, com dados EQUIVALENTES -----------------------------
	correnteAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta Corrente")
	cartaoAlheio := contaDeCartao(t, ctx, s, alheia.ID, "Cartão Roxo")
	cdbAlheia := s.makeCategory(t, ctx, alheia.ID, "CDB", category.KindInvestment, nil)
	s.makeTransaction(t, ctx, alheia.ID, correnteAlheia.ID, txSpec{
		Kind: transaction.KindIncome, AmountCents: 500_000, OccurredOn: dia, Description: "Salário",
	})
	s.makeTransaction(t, ctx, alheia.ID, cartaoAlheio.ID, txSpec{
		Kind: transaction.KindExpense, AmountCents: 30_000, OccurredOn: dia,
	})
	s.makeTransaction(t, ctx, alheia.ID, cartaoAlheio.ID, txSpec{
		Kind: transaction.KindExpense, AmountCents: 50_000, OccurredOn: dia, CategoryID: &cdbAlheia.ID,
	})

	return cenarioDoPainel{
		casa:           minha.ID,
		alheia:         alheia.ID,
		corrente:       corrente.ID,
		cartao:         cartao.ID,
		marcadas:       []string{cdb.ID, resgate.ID},
		contasDaAlheia: []string{correnteAlheia.ID, cartaoAlheio.ID},
	}
}

// linhaDoPainel acha a linha (kind, conta) e falha se ela não existir — a
// ausência de uma linha é o defeito mais silencioso desta consulta.
func linhaDoPainel(t *testing.T, rows []dashboard.KindAccountTotals, kind, accountID string) dashboard.KindAccountTotals {
	t.Helper()
	for _, r := range rows {
		if r.Kind == kind && r.AccountID == accountID {
			return r
		}
	}
	require.Failf(t, "linha ausente", "não há linha (%s, %s) em %+v", kind, accountID, rows)
	return dashboard.KindAccountTotals{}
}

// grupoDoComando devolve o trecho do SQL a partir do GROUP BY. É o que
// distingue "a conta está na CHAVE" de "a conta está só na projeção" — e é a
// diferença entre a forma escolhida e a rejeitada.
func grupoDoComando(t *testing.T, sql string) string {
	t.Helper()
	i := strings.Index(strings.ToUpper(sql), "GROUP BY")
	require.GreaterOrEqual(t, i, 0, "a agregação do painel tem GROUP BY: %s", sql)
	return sql[i:]
}

// Forma do comando EMITIDO: 4 colunas sem categoria marcada, 6 com. E as três
// formas de "vazio" (nil, slice vazio e slice só com string vazia) produzem a
// MESMA string, byte a byte — nenhuma delas emite `IN ()`.
//
// Conferido por comando emitido, e não por resultado: uma casa sem categoria
// de investimento devolve `marked = 0` de qualquer jeito, então um teste de
// resultado passaria igual se o `IN ()` tivesse ido ao banco.
func TestPainelEmiteAFormaDeQuatroColunasSemCategoriaMarcada(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDoPainel(t, s)
		spy := s.espiarSQL(t)

		emitido := func(t *testing.T, ids []string) comandoSQL {
			t.Helper()
			spy.ligar()
			_, err := s.transactions.SumMonthByKindAndAccount(ctx, c.casa, mesDoPainel, ids)
			require.NoError(t, err)
			cmds := spy.comandosEmitidos()
			require.Len(t, cmds, 1, "o resumo do painel é UMA consulta, sempre")
			return cmds[0]
		}

		semNada := emitido(t, nil)
		vazio := emitido(t, []string{})
		soVazias := emitido(t, []string{"", ""})

		assert.Equal(t, semNada.SQL, vazio.SQL)
		assert.Equal(t, semNada.SQL, soVazias.SQL,
			"lista só com string vazia é lista vazia: as colunas condicionais não podem entrar")
		assert.NotContains(t, semNada.SQL, "category_id IN", "IN () nunca é emitido (ADR-029f)")
		assert.NotContains(t, semNada.SQL, "NOT IN", "o painel não tem NOT IN: os marcados saem por CASE na projeção")
		assert.NotContains(t, semNada.SQL, "marked_total")
		assert.NotContains(t, semNada.SQL, "marked_cnt")
		assert.Contains(t, semNada.SQL, "kind AS kind, account_id AS account_id, COUNT(*) AS cnt",
			"a forma de 4 colunas é a projeção inteira, e a conta está nela")
		assert.NotContains(t, strings.ToUpper(semNada.SQL), "ORDER BY",
			"sem ORDER BY: a dobra é em Go, e collation é a última diferença entre dialetos")
		assert.Equal(t, 4, semNada.Parametros, "casa + mês + 2 kinds")

		// A conta está na CHAVE, e não só na projeção: é o que segura o
		// orçamento de parâmetros e o que faz a despesa sem categoria
		// sobreviver (ADR-031b).
		grupo := grupoDoComando(t, semNada.SQL)
		assert.Contains(t, grupo, "kind")
		assert.Contains(t, grupo, "account_id",
			"a conta vai na chave de agrupamento, nunca como coluna condicional")

		// --- com o conjunto de verdade: a forma de 6 colunas -----------------
		comIDs := emitido(t, c.marcadas)
		assert.Contains(t, comIDs.SQL, "marked_total")
		assert.Contains(t, comIDs.SQL, "marked_cnt",
			"dinheiro marcado e contagem marcada saem da MESMA varredura")
		assert.Contains(t, comIDs.SQL, "kind AS kind, account_id AS account_id, COUNT(*) AS cnt",
			"a forma de 6 colunas é a de 4 mais duas — a constante é a mesma")
		assert.Equal(t, 8, comIDs.Parametros,
			"2 categorias em marked_total + 2 em marked_cnt + casa + mês + 2 kinds")
		assert.Equal(t, grupoDoComando(t, semNada.SQL), grupoDoComando(t, comIDs.SQL),
			"as colunas condicionais mudam a projeção, nunca a chave de agrupamento")
	})
}

// Orçamento de parâmetros MEDIDO no pior caso (molde:
// TestSetCategoryWhereCurrentInCabeNoOrcamentoDeParametros).
//
// É o motivo nº 2 de a conta estar na CHAVE: com ela como terceira coluna
// condicional, o pior caso seria `2 + 4×200 + 4×50 = 1002` parâmetros —
// ACIMA do piso histórico de 999 do SQLite. Funcionaria em PostgreSQL e MySQL
// e estouraria dentro do driver nos outros dois.
func TestPainelCabeNoOrcamentoDeParametros(t *testing.T) {
	t.Parallel()

	const (
		pisoSQLite = 999
		tetoMSSQL  = 2100
		// formaRejeitada é o que a conta na projeção custaria: casa + mês +
		// (marked_total + marked_cnt) × 200 categorias × 2 (a coluna de
		// interseção cartão ∧ marcado) + (cartão + interseção) × 50 contas.
		formaRejeitada = 2 + 4*200 + 4*account.MaxPerHousehold
	)
	require.Greater(t, formaRejeitada, pisoSQLite,
		"a aritmética que rejeitou a conta na projeção: %d parâmetros não cabem em %d",
		formaRejeitada, pisoSQLite)

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDoPainel(t, s)

		// Os ids não precisam existir: o que está sob teste é o COMANDO
		// emitido, não o que ele encontra. 200 é o teto real (o mesmo
		// category.MaxPerHousehold).
		marcadas := make([]string, 0, 200)
		for i := range 200 {
			marcadas = append(marcadas, fmt.Sprintf("c-marcada-%03d", i))
		}

		spy := s.espiarSQL(t)
		spy.ligar()
		_, err := s.transactions.SumMonthByKindAndAccount(ctx, c.casa, mesDoPainel, marcadas)
		require.NoError(t, err)

		cmds := spy.comandosEmitidos()
		require.Len(t, cmds, 1, "o resumo do painel é UMA consulta, sempre")
		pior := cmds[0]
		t.Logf("PIOR CASO do painel: %d parâmetros (forma rejeitada custaria %d)", pior.Parametros, formaRejeitada)

		assert.LessOrEqual(t, pior.Parametros, pisoSQLite,
			"o pior caso tem de caber no piso histórico de 999 do SQLite")
		assert.LessOrEqual(t, pior.Parametros, tetoMSSQL,
			"o pior caso tem de caber nos 2100 do SQL Server")
		assert.Equal(t, 404, pior.Parametros,
			"200 de marked_total + 200 de marked_cnt + casa + mês + 2 kinds — e NENHUM id de conta")
	})
}

// O critério que a forma rejeitada perderia: `category_id` NULL cai no `ELSE`
// do CASE, então a despesa de cartão SEM categoria fica FORA do marcado e
// DENTRO do total bruto — que é de onde sai o gasto do cartão na tela.
//
// `NULL NOT IN (…)` é NULL nos quatro dialetos (lógica de três valores do
// SQL-92), e o WHERE só deixa passar o VERDADEIRO: por isso excluir os
// marcados do cartão por `NOT IN` apagaria esta linha em silêncio, no mês em
// que se acabou de importar a fatura. A prova de dialeto do `NOT IN` já existe
// (TestNullNotInNaoPassaNoWhereNesteDialeto); aqui a prova é a positiva — o
// dinheiro APARECE.
func TestPainelContaDespesaDeCartaoSemCategoriaNoTotalBruto(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDoPainel(t, s)

		rows, err := s.transactions.SumMonthByKindAndAccount(ctx, c.casa, mesDoPainel, c.marcadas)
		require.NoError(t, err)

		cartao := linhaDoPainel(t, rows, transaction.KindExpense, c.cartao)
		assert.EqualValues(t, 95_000, cartao.TotalCents,
			"30.000 sem categoria + 50.000 do aporte + 15.000 de mercado: o bruto tem TUDO")
		assert.EqualValues(t, 3, cartao.Count,
			"a linha sem categoria também é contada — COUNT(*) não sabe de categoria")
		assert.EqualValues(t, 50_000, cartao.MarkedTotalCents,
			"só o aporte é marcado: a linha sem categoria caiu no ELSE, como se quer")
		assert.EqualValues(t, 1, cartao.MarkedCount)

		// O gasto do cartão que o serviço publica é `total − marcado`, e é por
		// a linha sem categoria estar no total que ele sai certo.
		assert.EqualValues(t, 45_000, cartao.TotalCents-cartao.MarkedTotalCents,
			"30.000 + 15.000 — se a despesa sem categoria sumisse, o cartão apareceria 30.000 menor")

		// Receita e conta corrente, no mesmo comando.
		receita := linhaDoPainel(t, rows, transaction.KindIncome, c.corrente)
		assert.EqualValues(t, 600_000, receita.TotalCents)
		assert.EqualValues(t, 2, receita.Count)
		assert.EqualValues(t, 100_000, receita.MarkedTotalCents, "o resgate é receita MARCADA")
		assert.EqualValues(t, 1, receita.MarkedCount)

		corrente := linhaDoPainel(t, rows, transaction.KindExpense, c.corrente)
		assert.EqualValues(t, 20_000, corrente.TotalCents)
		assert.EqualValues(t, 1, corrente.Count)
		assert.Zero(t, corrente.MarkedTotalCents)
		assert.Zero(t, corrente.MarkedCount)

		// Ruído podado: outro mês e o excluído logicamente não entram em
		// nenhuma linha.
		var total int64
		for _, r := range rows {
			total += r.TotalCents
		}
		assert.EqualValues(t, 715_000, total,
			"600.000 + 20.000 + 95.000 — sem o mês seguinte, sem o excluído e sem transferência")
	})
}

// Critérios 6 e 7 da spec, no nível da CONSULTA: perna de transferência não é
// LIDA — não é podada depois, em Go.
//
// É `kind IN (income, expense)` (categorizableKinds) que torna isso
// estrutural: pagar a fatura do cartão cria um `transfer_in` DENTRO do cartão,
// e se ele fosse lido apareceria como dinheiro na conta agrupada — a fatura
// paga viraria receita, ou o gasto do cartão mudaria por causa de um
// pagamento. A asserção é por AUSÊNCIA de linha, que é mais forte do que
// conferir um total: um total certo por acaso não prova que o kind foi podado.
func TestPainelNaoLeTransferencia(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDoPainel(t, s)

		rows, err := s.transactions.SumMonthByKindAndAccount(ctx, c.casa, mesDoPainel, c.marcadas)
		require.NoError(t, err)

		for _, r := range rows {
			assert.NotEqual(t, transaction.KindTransferOut, r.Kind, "perna de saída não é lida (ADR-016)")
			assert.NotEqual(t, transaction.KindTransferIn, r.Kind, "perna de entrada não é lida (ADR-016)")
			assert.Containsf(t, []string{transaction.KindIncome, transaction.KindExpense}, r.Kind,
				"kind inesperado na agregação: %q", r.Kind)
		}
		require.Len(t, rows, 3, "(income, corrente), (expense, corrente) e (expense, cartão) — mais nada")

		// As duas pernas somam 800.000: se qualquer uma tivesse entrado, o
		// total do mês não seria este.
		var total int64
		for _, r := range rows {
			total += r.TotalCents
		}
		assert.EqualValues(t, 715_000, total, "as duas pernas de 400.000 ficaram de fora, as duas")
	})
}

// Isolamento por casa: duas casas com dados EQUIVALENTES, e a consulta de uma
// nunca vê a outra — nem no dinheiro, nem nos ids de conta que ela devolve.
//
// Dado equivalente é o ponto: se `household_id` saísse do WHERE, os totais
// DOBRARIAM em vez de simplesmente errarem, e um id de conta da vizinha
// chegaria ao serviço, que o interseccionaria com a lista de cartões da casa
// de cá (BOLA — docs/SEGURANCA.md §2).
func TestPainelNaoVeAOutraCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDoPainel(t, s)

		minhas, err := s.transactions.SumMonthByKindAndAccount(ctx, c.casa, mesDoPainel, c.marcadas)
		require.NoError(t, err)

		proibidas := map[string]struct{}{}
		for _, id := range c.contasDaAlheia {
			proibidas[id] = struct{}{}
		}
		var total int64
		for _, r := range minhas {
			_, alheia := proibidas[r.AccountID]
			assert.Falsef(t, alheia, "conta da outra casa apareceu na agregação: %s", r.AccountID)
			total += r.TotalCents
		}
		assert.EqualValues(t, 715_000, total,
			"os 580.000 equivalentes da vizinha não entram — se entrassem, o total subiria")

		// E o caminho inverso: a vizinha vê só o dela.
		dela, err := s.transactions.SumMonthByKindAndAccount(ctx, c.alheia, mesDoPainel, c.marcadas)
		require.NoError(t, err)
		var totalDela int64
		for _, r := range dela {
			assert.NotEqual(t, c.corrente, r.AccountID)
			assert.NotEqual(t, c.cartao, r.AccountID)
			totalDela += r.TotalCents
		}
		assert.EqualValues(t, 580_000, totalDela, "500.000 + 30.000 + 50.000, só o da vizinha")

		// As categorias marcadas passadas são as da casa de CÁ: nenhuma delas
		// marca lançamento da vizinha, então o marcado dela é zero. É a prova
		// de que o `IN (…)` não atravessa a fronteira da casa.
		for _, r := range dela {
			assert.Zerof(t, r.MarkedTotalCents, "categoria da outra casa não marca linha desta: %+v", r)
			assert.Zerof(t, r.MarkedCount, "%+v", r)
		}
	})
}

// Guardas ANTES de qualquer SQL: casa vazia e mês vazio são ERRO, e NENHUM
// comando é emitido.
//
// Mês vazio devolveria um mês inteiramente zerado em silêncio — a tela diria
// "você não gastou nada" —, e casa vazia é a porta do BOLA. Conferido pela
// contagem de comandos, e não pelo resultado: uma consulta que rodasse e
// voltasse vazia daria o mesmo `len(rows) == 0`.
func TestPainelRecusaCasaOuMesVazioSemTocarOBanco(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDoPainel(t, s)
		spy := s.espiarSQL(t)

		casos := []struct {
			nome string
			casa string
			mes  string
		}{
			{"casa vazia", "", mesDoPainel},
			{"mês vazio", c.casa, ""},
			{"os dois vazios", "", ""},
		}
		for _, caso := range casos {
			t.Run(caso.nome, func(t *testing.T) {
				spy.ligar()
				rows, err := s.transactions.SumMonthByKindAndAccount(ctx, caso.casa, caso.mes, c.marcadas)
				require.Error(t, err)
				assert.Nil(t, rows)
				assert.Empty(t, spy.comandosEmitidos(),
					"a guarda vem ANTES do SQL: nenhum comando pode ter ido ao banco")
			})
		}
	})
}

// Acima do teto da taxonomia é transaction.ErrTooManyCategories — falha ALTA,
// sem emitir comando — e não um `IN (...)` com 201 ids que estouraria dentro
// do driver em dois dialetos só.
//
// O conjunto NÃO é fatiado: fatiar uma agregação obrigaria a somar fatias de
// dinheiro, e é assim que se passa a ter dois números para o mesmo mês.
func TestPainelRecusaCategoriasDemaisSemTocarOBanco(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDoPainel(t, s)

		demais := make([]string, 0, 201)
		for i := range 201 {
			demais = append(demais, fmt.Sprintf("c-marcada-%03d", i))
		}

		spy := s.espiarSQL(t)
		spy.ligar()
		rows, err := s.transactions.SumMonthByKindAndAccount(ctx, c.casa, mesDoPainel, demais)
		require.ErrorIs(t, err, transaction.ErrTooManyCategories)
		assert.Nil(t, rows)
		assert.Empty(t, spy.comandosEmitidos(), "a recusa acontece antes de qualquer SQL")

		// Exatamente 200 passa — e o teto é conferido DEPOIS do dedupe: 201
		// ids com uma repetição são 200 ids, e a consulta roda.
		comRepetido := make([]string, 0, 201)
		comRepetido = append(comRepetido, demais[:200]...)
		comRepetido = append(comRepetido, demais[0])
		spy.ligar()
		_, err = s.transactions.SumMonthByKindAndAccount(ctx, c.casa, mesDoPainel, comRepetido)
		require.NoError(t, err)
		cmds := spy.comandosEmitidos()
		require.Len(t, cmds, 1)
		assert.Equal(t, 404, cmds[0].Parametros, "o id repetido não vira parâmetro a mais")
	})
}
