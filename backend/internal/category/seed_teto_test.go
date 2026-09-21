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
// A justificativa original dizia "porque ela roda no auto-reparo do LOGIN".
// Isso está ERRADO e foi corrigido no ADR-033: `household.EnsureDefault`
// devolve cedo quando o usuário já tem casa, então a semente roda UMA vez por
// casa, na criação. A trava continua valendo, e por um motivo que não depende
// daquela frase: a semente cresceu de 14 para 56 categorias (ADR-033), e uma
// função que popula sem olhar o teto empurraria a casa para além dos 200 que o
// resto do código assume. A partir daí `/investments/detect` com
// `overwriteCategorized` responde 500 PERMANENTE
// (transaction.ErrTooManyCategories) — sem nenhuma ação de autoatendimento,
// porque a pessoa não tem como saber que precisa excluir uma categoria.
//
// E parar em SILÊNCIO, sem falhar: um erro aqui derrubaria a criação da casa
// inteira por causa de uma categoria sugerida.

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

	// Casa cheia: nem falha, nem estoura.
	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa),
		"a semente roda dentro da criação da casa: falhar aqui derrubaria o cadastro por causa de uma categoria sugerida")

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

// Casa nova continua recebendo a semente inteira: o teto é 200 e a semente são
// 56 categorias — o conserto do A3 não pode custar a razão de a semente existir.
func TestCasaNovaContinuaRecebendoASementeInteira(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	total, err := repo.CountAll(ctx, minhaCasa)
	require.NoError(t, err)
	assert.EqualValues(t, category.DefaultCategoryCount(), total)
}
