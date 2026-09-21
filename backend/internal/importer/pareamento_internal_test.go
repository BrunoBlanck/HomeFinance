package importer

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
)

// categoriaCombinaComALinha DELEGA para category.AceitaLancamento (ADR-029b).
//
// Esta era a SEGUNDA cópia da regra de pareamento — a esquecida das duas, e a
// porta por onde uma despesa importada ganharia categoria de resgate quando
// `investment` e `redemption` entraram na allowlist. Os testes abaixo travam a
// delegação, não uma terceira cópia.

func TestSugestaoDelegaOPareamentoParaACategoria(t *testing.T) {
	t.Parallel()

	linhas := []string{
		transaction.KindIncome, transaction.KindExpense,
		transaction.KindTransferIn, transaction.KindTransferOut, "", "lixo",
	}
	naturezas := []string{
		category.KindIncome, category.KindExpense,
		category.KindInvestment, category.KindRedemption, "", "lixo",
	}

	for _, k := range linhas {
		for _, n := range naturezas {
			assert.Equal(t, category.AceitaLancamento(k, n), categoriaCombinaComALinha(k, n),
				"linha %q × categoria %q", k, n)
		}
	}
}

// Aceite 5 da spec 0006, do lado da importação: a linha de DESPESA aceita
// sugestão de categoria de aporte e NUNCA de resgate.
func TestSugestaoDeDespesaAceitaAporteENuncaResgate(t *testing.T) {
	t.Parallel()

	assert.True(t, categoriaCombinaComALinha(transaction.KindExpense, category.KindInvestment))
	assert.False(t, categoriaCombinaComALinha(transaction.KindExpense, category.KindRedemption),
		"despesa nunca recebe sugestão de resgate")

	assert.True(t, categoriaCombinaComALinha(transaction.KindIncome, category.KindRedemption))
	assert.False(t, categoriaCombinaComALinha(transaction.KindIncome, category.KindInvestment),
		"receita nunca recebe sugestão de aporte")

	// Perna de transferência não recebe categoria de natureza nenhuma.
	for _, perna := range []string{transaction.KindTransferIn, transaction.KindTransferOut} {
		for _, n := range []string{
			category.KindIncome, category.KindExpense,
			category.KindInvestment, category.KindRedemption,
		} {
			assert.False(t, categoriaCombinaComALinha(perna, n), "%s × %s", perna, n)
		}
	}
}
