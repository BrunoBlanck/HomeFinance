package gormstore_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Este arquivo prova, com os serviços REAIS ligados aos repositórios REAIS, os
// dois critérios de aceite que a spec 0004 escreveu primeiro:
//
//  1. saldo de conta é abertura + soma dos lançamentos, e o total da lista bate
//     com a soma das linhas exibidas (ADR-017, critério 1);
//  2. excluir conta ou categoria com lançamento é recusado (critério 2) — a
//     dívida que a spec 0003 §1 deixou declarada para esta entrega.
//
// Dublê aqui seria autoengano: os dois critérios são sobre o que o SQL soma e
// sobre o que o SQL encontra.

// servicoDeContas monta o serviço de contas ligado de verdade: fuso da casa
// pelo household, saldo pelos lançamentos, uso pelos lançamentos.
func (s *store) servicoDeContas() *account.Service {
	casas := household.NewService(s.households, s.memberships)
	return account.NewService(s.accounts, casas, s.uow,
		account.WithBalances(s.transactions),
		account.WithUsageCheckers(transaction.NewUsageChecker(s.transactions)),
	)
}

func TestSaldoDaContaEhAberturaMaisOsLancamentos(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		svc := s.servicoDeContas()
		ator := account.Actor{HouseholdID: minha.ID, UserID: s.nextID("u")}

		// makeAccount abre com R$ 100,00.
		corrente := s.makeAccount(t, ctx, minha.ID, "Conta Corrente")
		cartao := s.makeAccount(t, ctx, minha.ID, "Cartão")

		s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
			Kind: transaction.KindIncome, AmountCents: 1_000_00, OccurredOn: civil.MustNew(2026, 2, 5),
		})
		s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 250_00, OccurredOn: civil.MustNew(2026, 2, 6),
		})
		// Transferência: sai da corrente, entra no cartão. A casa não fica nem
		// mais rica nem mais pobre.
		grupo := s.nextID("grp")
		s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
			Kind: transaction.KindTransferOut, AmountCents: 300_00,
			OccurredOn: civil.MustNew(2026, 2, 13), TransferGroupID: &grupo,
		})
		s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindTransferIn, AmountCents: 300_00,
			OccurredOn: civil.MustNew(2026, 2, 13), TransferGroupID: &grupo,
		})
		// Lançamento excluído NÃO entra no saldo.
		excluido := s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 900_00, OccurredOn: civil.MustNew(2026, 2, 7),
		})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluido.ID, now()))
		// E o dinheiro da vizinha não entra em nada.
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta da Vizinha")
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindIncome, AmountCents: 5_000_00, OccurredOn: civil.MustNew(2026, 2, 5),
		})

		view, err := svc.Get(ctx, ator, corrente.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 100_00+1_000_00-250_00-300_00, view.BalanceCents)

		doCartao, err := svc.Get(ctx, ator, cartao.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 100_00+300_00, doCartao.BalanceCents)

		lista, err := svc.List(ctx, ator, false)
		require.NoError(t, err)
		require.Len(t, lista.Items, 2)

		// Critério 1, literal: o total tem de bater com a soma das linhas
		// EXIBIDAS. Somar um conjunto diferente do mostrado é como um painel
		// passa a dizer um número que a tela contradiz.
		var soma int64
		for _, item := range lista.Items {
			soma += item.BalanceCents
		}
		assert.Equal(t, soma, lista.TotalBalanceCents)
		assert.EqualValues(t, 100_00+1_000_00-250_00-300_00+100_00+300_00, lista.TotalBalanceCents)
	})
}

// O saldo respondido por uma ESCRITA é o mesmo respondido pela leitura: sem
// isso, o número mudaria sozinho na tela depois de renomear a conta.
func TestEscritaDeContaRespondeOMesmoSaldoQueALeitura(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		svc := s.servicoDeContas()
		ator := account.Actor{HouseholdID: minha.ID, UserID: s.nextID("u")}

		conta := s.makeAccount(t, ctx, minha.ID, "Conta Corrente")
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 40_00, OccurredOn: civil.MustNew(2026, 2, 6),
		})

		novoNome := "Conta Principal"
		atualizada, err := svc.Update(ctx, ator, conta.ID, account.UpdateInput{Name: &novoNome})
		require.NoError(t, err)

		lida, err := svc.Get(ctx, ator, conta.ID)
		require.NoError(t, err)
		assert.Equal(t, lida.BalanceCents, atualizada.BalanceCents)
		assert.EqualValues(t, 100_00-40_00, atualizada.BalanceCents)

		arquivada, err := svc.Archive(ctx, ator, conta.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 100_00-40_00, arquivada.BalanceCents)
	})
}

// Critério de aceite 2, primeira metade: DELETE /accounts/{id} com lançamento
// associado é recusado. O handler traduz ErrInUse em 422 RESOURCE_IN_USE.
func TestExcluirContaComLancamentoEhRecusado(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		svc := s.servicoDeContas()
		ator := account.Actor{HouseholdID: minha.ID, UserID: s.nextID("u")}

		usada := s.makeAccount(t, ctx, minha.ID, "Conta Usada")
		lancamento := s.makeTransaction(t, ctx, minha.ID, usada.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 10_00, OccurredOn: civil.MustNew(2026, 2, 6),
		})

		require.ErrorIs(t, svc.Delete(ctx, ator, usada.ID), account.ErrInUse)

		// E continua recusando DEPOIS de o lançamento ser excluído: o critério
		// é "já foi usada?", e a restauração (ADR-025f) pode trazer o
		// lançamento de volta — para uma conta que não existisse mais, se a
		// exclusão tivesse passado aqui. A saída para o usuário é ARQUIVAR.
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, lancamento.ID, now()))
		require.ErrorIs(t, svc.Delete(ctx, ator, usada.ID), account.ErrInUse)

		arquivada, err := svc.Archive(ctx, ator, usada.ID)
		require.NoError(t, err, "arquivar é a saída, e ela precisa continuar aberta")
		assert.NotNil(t, arquivada.ArchivedAt)

		// Conta nunca usada continua excluível — a parede é para quem tem
		// histórico, não para todo mundo.
		limpa := s.makeAccount(t, ctx, minha.ID, "Conta Sem Uso")
		require.NoError(t, svc.Delete(ctx, ator, limpa.ID))

		// E o lançamento da vizinha não pode travar a MINHA conta: se o
		// verificador vazasse entre casas, eu não conseguiria excluir uma conta
		// minha por causa de dado que nem posso ver.
		outraLimpa := s.makeAccount(t, ctx, minha.ID, "Outra Sem Uso")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta da Vizinha")
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 10_00, OccurredOn: civil.MustNew(2026, 2, 6),
		})
		require.NoError(t, svc.Delete(ctx, ator, outraLimpa.ID))
	})
}

// Critério de aceite 2, segunda metade: DELETE /categories/{id} com lançamento
// associado é recusado.
func TestExcluirCategoriaComLancamentoEhRecusado(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)

		svc := category.NewService(s.categories, s.uow,
			category.WithUsageCheckers(transaction.NewUsageChecker(s.transactions)),
		)
		ator := category.Actor{HouseholdID: minha.ID, UserID: s.nextID("u")}

		conta := s.makeAccount(t, ctx, minha.ID, "Conta Corrente")
		grupo := s.makeCategory(t, ctx, minha.ID, "Alimentação", category.KindExpense, nil)
		folha := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, &grupo.ID)

		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 10_00,
			OccurredOn: civil.MustNew(2026, 2, 6), CategoryID: &folha.ID,
		})

		require.ErrorIs(t, svc.Delete(ctx, ator, folha.ID), category.ErrInUse)

		// Categoria sem uso nenhum continua excluível.
		semUso := s.makeCategory(t, ctx, minha.ID, "Padaria", category.KindExpense, &grupo.ID)
		require.NoError(t, svc.Delete(ctx, ator, semUso.ID))
	})
}
