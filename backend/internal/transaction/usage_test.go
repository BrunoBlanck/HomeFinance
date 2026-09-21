package transaction_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// O verificador é o que paga a dívida da spec 0003 §1. Estes testes fixam o
// CRITÉRIO — "já foi usada alguma vez", contando o lançamento excluído — para
// que uma mudança futura tenha de ser deliberada, e não um efeito colateral de
// alguém achando o 422 chato.
//
// O caminho completo (DELETE de conta e de categoria recusado contra o banco de
// verdade) está em internal/platform/storage/gormstore/saldo_e_uso_test.go.
func TestUsageCheckerContaLancamentoExcluido(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	verificador := transaction.NewUsageChecker(repo)

	cat := "cat-1"
	excluido := agora
	repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 10_00, Description: "Mercado", OccurredOn: civil.MustNew(2026, 9, 10),
		CategoryID: &cat, DeletedAt: &excluido,
	})

	usada, err := verificador.AccountInUse(t.Context(), minhaCasa, "acc-1")
	require.NoError(t, err)
	assert.True(t, usada, "a conta JÁ foi usada; a restauração pode trazer o lançamento de volta")

	usada, err = verificador.CategoryInUse(t.Context(), minhaCasa, cat)
	require.NoError(t, err)
	assert.True(t, usada)
}

func TestUsageCheckerNaoAtravessaAFronteiraDaCasa(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	verificador := transaction.NewUsageChecker(repo)

	cat := "cat-alheia"
	repo.semear(transaction.Transaction{
		HouseholdID: outraCasa, AccountID: "acc-alheia", Kind: transaction.KindExpense,
		AmountCents: 10_00, Description: "Alheio", OccurredOn: civil.MustNew(2026, 9, 10),
		CategoryID: &cat,
	})

	// Se o verificador vazasse entre casas, eu ficaria impedido de excluir uma
	// conta minha por causa de dado que não posso nem ver.
	usada, err := verificador.AccountInUse(t.Context(), minhaCasa, "acc-alheia")
	require.NoError(t, err)
	assert.False(t, usada)

	usada, err = verificador.CategoryInUse(t.Context(), minhaCasa, cat)
	require.NoError(t, err)
	assert.False(t, usada)
}

// O verificador satisfaz as duas interfaces sem que nenhum dos três pacotes
// conheça os outros. Se alguém mudar uma assinatura, isto para de compilar —
// que é mais barato do que descobrir na ligação, em cmd/api.
var (
	_ account.UsageChecker  = transaction.UsageChecker{}
	_ category.UsageChecker = transaction.UsageChecker{}
)
