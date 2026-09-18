package category_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/category"
)

// A semente respeita MaxPerHousehold — achado A3 da segunda revisão de
// segurança.
//
// Ela não ser exceção ao teto importa porque roda no auto-reparo do LOGIN: uma
// casa exatamente em 200 recebia os dois grupos do ADR-029a e ficava com 202. A
// partir daí a taxonomia passa do teto que o resto do código assume, e
// `/investments/detect` com `overwriteCategorized` responde 500 PERMANENTE
// (transaction.ErrTooManyCategories) — sem nenhuma ação de autoatendimento,
// porque a pessoa não tem como saber que precisa excluir uma categoria.

// encherTaxonomia cria n grupos com nomes que NÃO colidem com os da semente,
// para o teste medir o teto e não a idempotência por nome.
func encherTaxonomia(t *testing.T, svc *category.Service, casa string, n int) {
	t.Helper()
	for i := range n {
		criarGrupo(t, svc, casa, fmt.Sprintf("Categoria cheia %03d", i), category.KindExpense)
	}
}

func TestSementeNaoUltrapassaOTetoDaCasa(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	encherTaxonomia(t, svc, minhaCasa, category.MaxPerHousehold)

	// É o auto-reparo do login: nem falha, nem estoura.
	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa),
		"a semente roda no caminho do LOGIN: falhar aqui trancaria a entrada de quem tem a taxonomia cheia")

	total, err := repo.CountAll(ctx, minhaCasa)
	require.NoError(t, err)
	assert.EqualValues(t, category.MaxPerHousehold, total,
		"a semente não pode empurrar a casa para além do teto")
}

// O caso de borda que produz o 500 permanente: a casa entra com uma vaga só, e
// só um grupo da semente cabe.
func TestSementeParaExatamenteNaUltimaVaga(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	encherTaxonomia(t, svc, minhaCasa, category.MaxPerHousehold-1)
	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	total, err := repo.CountAll(ctx, minhaCasa)
	require.NoError(t, err)
	assert.EqualValues(t, category.MaxPerHousehold, total)
}

// Casa nova continua recebendo a semente inteira: o teto é 200 e os grupos são
// catorze — o conserto do A3 não pode custar a razão de a semente existir.
func TestCasaNovaContinuaRecebendoASementeInteira(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	total, err := repo.CountAll(ctx, minhaCasa)
	require.NoError(t, err)
	assert.EqualValues(t, len(category.DefaultGroups()), total)
}
