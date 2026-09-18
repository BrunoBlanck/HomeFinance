package gormstore_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Este arquivo cobre as duas consultas que sustentam ADR-016 (o par de
// transferência) e ADR-023d (os números derivados da fatura). As duas usam
// índices que já existiam no schema e que, até aqui, nenhuma consulta
// exercitava: ix_transactions_group e ix_transactions_statement.

// ADR-016: o par mora em DUAS contas. Sem esta consulta não há como cumprir
// "excluir uma perna exclui a outra" — ByID e List só alcançam uma conta por
// vez, e a outra perna ficaria para trás.
func TestByTransferGroupDevolveAsDuasPernasInclusiveAExcluida(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)

		corrente := s.makeAccount(t, ctx, minha.ID, "Conta Corrente")
		cartao := s.makeAccount(t, ctx, minha.ID, "Cartão")
		grupo := s.nextID("grp")

		saida := s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
			Kind: transaction.KindTransferOut, AmountCents: 500_00,
			OccurredOn: civil.MustNew(2026, 2, 13), TransferGroupID: &grupo,
		})
		entrada := s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindTransferIn, AmountCents: 500_00,
			OccurredOn: civil.MustNew(2026, 2, 13), TransferGroupID: &grupo,
		})
		// Uma despesa qualquer, sem grupo: não pode aparecer.
		s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 10_00,
			OccurredOn: civil.MustNew(2026, 2, 13),
		})

		pernas, err := s.transactions.ByTransferGroup(ctx, minha.ID, grupo)
		require.NoError(t, err)
		require.Len(t, pernas, 2)

		ids := []string{pernas[0].ID, pernas[1].ID}
		assert.Contains(t, ids, saida.ID)
		assert.Contains(t, ids, entrada.ID)

		// Depois de excluída, a perna CONTINUA aparecendo: é isso que permite
		// a restauração encontrá-la (ADR-025f). ByID a esconderia.
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, entrada.ID, now()))
		pernas, err = s.transactions.ByTransferGroup(ctx, minha.ID, grupo)
		require.NoError(t, err)
		require.Len(t, pernas, 2, "a perna excluída precisa continuar visível para poder voltar")

		excluidas := 0
		for _, p := range pernas {
			if p.DeletedAt != nil {
				excluidas++
			}
		}
		assert.Equal(t, 1, excluidas)
	})
}

// BOLA na consulta do par: conhecer o transfer_group_id da vizinha não pode
// devolver os lançamentos dela — e ele é adivinhável na mesma medida em que
// qualquer id é.
func TestByTransferGroupNaoAtravessaAFronteiraDaCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta da Vizinha")
		outraAlheia := s.makeAccount(t, ctx, alheia.ID, "Poupança da Vizinha")
		grupo := s.nextID("grp")

		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindTransferOut, AmountCents: 100_00,
			OccurredOn: civil.MustNew(2026, 2, 13), TransferGroupID: &grupo,
		})
		s.makeTransaction(t, ctx, alheia.ID, outraAlheia.ID, txSpec{
			Kind: transaction.KindTransferIn, AmountCents: 100_00,
			OccurredOn: civil.MustNew(2026, 2, 13), TransferGroupID: &grupo,
		})

		pernas, err := s.transactions.ByTransferGroup(ctx, minha.ID, grupo)
		require.NoError(t, err)
		assert.Empty(t, pernas)

		// Grupo vazio não pode virar "todas as linhas": seria uma varredura da
		// casa inteira devolvida como se fosse um par.
		pernas, err = s.transactions.ByTransferGroup(ctx, alheia.ID, "")
		require.NoError(t, err)
		assert.Empty(t, pernas)
	})
}

// ADR-023(d): total, pago e status são DERIVADOS. Este teste prova a aritmética
// contra o SQL de verdade — inclusive o crédito na fatura, que ABATE o total
// (D4 da spec 0004), e o pagamento, que é uma transferência.
func TestSumByStatementDerivaTotalEPago(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)

		cartao := s.makeAccount(t, ctx, minha.ID, "Cartão")
		fatura := s.makeStatement(t, ctx, minha.ID, cartao.ID, "2026-02")
		outraFatura := s.makeStatement(t, ctx, minha.ID, cartao.ID, "2026-03")

		// Duas compras, um crédito (estorno) e um pagamento parcial.
		s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 300_00,
			OccurredOn: civil.MustNew(2026, 1, 28), CompetenceMonth: "2026-02", StatementID: &fatura.ID,
		})
		s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 200_00,
			OccurredOn: civil.MustNew(2026, 1, 30), CompetenceMonth: "2026-02", StatementID: &fatura.ID,
		})
		s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindIncome, AmountCents: 50_00,
			OccurredOn: civil.MustNew(2026, 2, 1), CompetenceMonth: "2026-02", StatementID: &fatura.ID,
		})
		s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindTransferIn, AmountCents: 100_00,
			OccurredOn: civil.MustNew(2026, 2, 10), CompetenceMonth: "2026-02", StatementID: &fatura.ID,
		})
		// Linha excluída: não cobra nada. Somá-la faria a fatura pedir dinheiro
		// que ninguém deve.
		excluida := s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 999_00,
			OccurredOn: civil.MustNew(2026, 1, 29), CompetenceMonth: "2026-02", StatementID: &fatura.ID,
		})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		// Linha do mesmo cartão e do mesmo mês, mas FORA da fatura.
		s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 77_00,
			OccurredOn: civil.MustNew(2026, 2, 20), CompetenceMonth: "2026-02",
		})

		somas, err := s.transactions.SumByStatement(ctx, minha.ID, []string{fatura.ID, outraFatura.ID})
		require.NoError(t, err)

		assert.EqualValues(t, 450_00, somas[fatura.ID].TotalCents, "300 + 200 - 50 de crédito")
		assert.EqualValues(t, 100_00, somas[fatura.ID].PaidCents)
		assert.EqualValues(t, 4, somas[fatura.ID].LineCount)

		// Fatura sem linha não aparece no mapa, e o zero-value é o resultado
		// certo para ela: zero a cobrar, zero pago.
		assert.EqualValues(t, 0, somas[outraFatura.ID].TotalCents)

		vazio, err := s.transactions.SumByStatement(ctx, minha.ID, nil)
		require.NoError(t, err)
		assert.Empty(t, vazio)
	})
}

// Isolamento na consulta AGREGADA — a pior forma de vazamento, porque vaza
// DINHEIRO: o total da fatura passaria a incluir a compra da vizinha.
func TestSumByStatementNaoSomaLinhaDeOutraCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		cartaoAlheio := s.makeAccount(t, ctx, alheia.ID, "Cartão da Vizinha")
		faturaAlheia := s.makeStatement(t, ctx, alheia.ID, cartaoAlheio.ID, "2026-02")
		s.makeTransaction(t, ctx, alheia.ID, cartaoAlheio.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 900_00,
			OccurredOn: civil.MustNew(2026, 1, 28), CompetenceMonth: "2026-02", StatementID: &faturaAlheia.ID,
		})

		somas, err := s.transactions.SumByStatement(ctx, minha.ID, []string{faturaAlheia.ID})
		require.NoError(t, err)
		assert.Empty(t, somas, "id de fatura alheia não soma nada na minha casa")
	})
}

// O detalhe da fatura lista as linhas DELA — não as do mês inteiro daquele
// cartão. A diferença aparece exatamente na borda que mais confunde o usuário:
// a compra feita depois do fechamento, que é do mesmo mês e da fatura seguinte.
func TestListFiltraPelaFatura(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		cartao := s.makeAccount(t, ctx, minha.ID, "Cartão")
		fatura := s.makeStatement(t, ctx, minha.ID, cartao.ID, "2026-02")

		naFatura := s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 300_00,
			OccurredOn: civil.MustNew(2026, 1, 28), CompetenceMonth: "2026-02", StatementID: &fatura.ID,
		})
		s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 40_00,
			OccurredOn: civil.MustNew(2026, 2, 20), CompetenceMonth: "2026-02",
		})

		linhas, err := s.transactions.List(ctx, minha.ID, transaction.ListFilter{StatementID: fatura.ID})
		require.NoError(t, err)
		require.Len(t, linhas, 1)
		assert.Equal(t, naFatura.ID, linhas[0].ID)

		// E a fatura da vizinha não lista nada aqui.
		linhas, err = s.transactions.List(ctx, alheia.ID, transaction.ListFilter{StatementID: fatura.ID})
		require.NoError(t, err)
		assert.Empty(t, linhas)
	})
}
