package transaction

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// categoriaCombina DELEGA para category.AceitaLancamento, a única fonte da
// verdade do pareamento (ADR-029b). Estes testes travam a delegação — e não
// uma segunda cópia da regra —, porque uma cópia que envelhece é exatamente o
// que a alínea (b) existe para impedir.

func TestCategoriaCombinaDelegaParaACategoria(t *testing.T) {
	t.Parallel()

	// Toda combinação de tipo de lançamento × natureza de categoria responde
	// EXATAMENTE o que category.AceitaLancamento responde. Se um dia alguém
	// reescrever a regra aqui, este teste cai.
	lancamentos := []string{KindIncome, KindExpense, KindTransferIn, KindTransferOut, "", "lixo"}
	naturezas := []string{
		category.KindIncome, category.KindExpense,
		category.KindInvestment, category.KindRedemption, "", "lixo",
	}

	for _, k := range lancamentos {
		for _, n := range naturezas {
			assert.Equal(t, category.AceitaLancamento(k, n), categoriaCombina(k, n),
				"lançamento %q × categoria %q", k, n)
		}
	}
}

// Aceite 2 da spec 0006, do lado do serviço de lançamentos.
func TestPareamentoAceitaAporteEmDespesaEResgateEmReceita(t *testing.T) {
	t.Parallel()

	assert.True(t, categoriaCombina(KindExpense, category.KindExpense))
	assert.True(t, categoriaCombina(KindExpense, category.KindInvestment), "aporte é dinheiro que SAI")
	assert.True(t, categoriaCombina(KindIncome, category.KindIncome))
	assert.True(t, categoriaCombina(KindIncome, category.KindRedemption), "resgate é dinheiro que ENTRA")

	// Lado errado do dinheiro: recusado nos dois sentidos.
	assert.False(t, categoriaCombina(KindExpense, category.KindRedemption))
	assert.False(t, categoriaCombina(KindExpense, category.KindIncome))
	assert.False(t, categoriaCombina(KindIncome, category.KindInvestment))
	assert.False(t, categoriaCombina(KindIncome, category.KindExpense))

	// Transferência não aceita natureza nenhuma, nem as novas (ADR-016).
	for _, perna := range []string{KindTransferIn, KindTransferOut} {
		for _, n := range []string{
			category.KindIncome, category.KindExpense,
			category.KindInvestment, category.KindRedemption,
		} {
			assert.False(t, categoriaCombina(perna, n), "%s × %s", perna, n)
		}
	}
}

// As strings de `kind` de LANÇAMENTO e as duas naturezas homônimas de
// CATEGORIA precisam continuar iguais: category.AceitaLancamento compara o
// primeiro argumento com literais próprios, porque `category` não pode
// importar `transaction` (é `transaction` quem importa `category`). Se um dos
// dois lados renomear a string, o pareamento passaria a recusar tudo — em
// silêncio. Este teste é a trava.
func TestVocabularioDeKindContinuaAlinhadoEntreOsPacotes(t *testing.T) {
	t.Parallel()

	require.Equal(t, category.KindIncome, KindIncome)
	require.Equal(t, category.KindExpense, KindExpense)
	require.NotEqual(t, category.KindInvestment, KindTransferIn)
	require.NotEqual(t, category.KindRedemption, KindTransferOut)

	// E `investment`/`redemption` NÃO são tipos de lançamento: o conjunto de
	// kinds de lançamento continua fechado nos quatro de sempre (ADR-029a).
	assert.False(t, ValidKind(category.KindInvestment), "aporte não é tipo de lançamento")
	assert.False(t, ValidKind(category.KindRedemption), "resgate não é tipo de lançamento")
}
