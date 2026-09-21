package transaction_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
)

// O sinal do lançamento vem do kind, porque no banco amount_cents é SEMPRE
// positivo. Esta função é o único lugar onde essa regra vive — se cada
// chamador repetisse o switch, um deles somaria despesa como receita, e o erro
// apareceria como saldo errado, sem mensagem nenhuma.
func TestSignedAmountCentsVemDoKind(t *testing.T) {
	t.Parallel()

	casos := []struct {
		kind     string
		esperado int64
	}{
		{transaction.KindIncome, 1_000},
		{transaction.KindTransferIn, 1_000},
		{transaction.KindExpense, -1_000},
		{transaction.KindTransferOut, -1_000},
		{"kind_que_nao_existe", 0},
	}

	for _, caso := range casos {
		tx := transaction.Transaction{Kind: caso.kind, AmountCents: 1_000}
		assert.Equal(t, caso.esperado, tx.SignedAmountCents(), "kind %q", caso.kind)
	}
}

// Transferência aparece no extrato, mas não é receita nem despesa (ADR-016):
// quem soma o resultado do mês precisa conseguir reconhecê-la.
func TestIsTransferReconheceAsDuasPernas(t *testing.T) {
	t.Parallel()

	assert.True(t, transaction.Transaction{Kind: transaction.KindTransferOut}.IsTransfer())
	assert.True(t, transaction.Transaction{Kind: transaction.KindTransferIn}.IsTransfer())
	assert.False(t, transaction.Transaction{Kind: transaction.KindExpense}.IsTransfer())
	assert.False(t, transaction.Transaction{Kind: transaction.KindIncome}.IsTransfer())
}
