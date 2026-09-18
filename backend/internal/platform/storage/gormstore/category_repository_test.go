package gormstore_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCategoriaDeOutraCasaNaoApareceEmNenhumaConsulta(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		grupoAlheio := s.makeCategory(t, ctx, alheia.ID, "Moradia", category.KindExpense, nil)
		s.makeCategory(t, ctx, alheia.ID, "Energia", category.KindExpense, &grupoAlheio.ID)
		s.makeCategory(t, ctx, minha.ID, "Alimentação", category.KindExpense, nil)

		_, err := s.categories.ByID(ctx, minha.ID, grupoAlheio.ID)
		require.ErrorIs(t, err, category.ErrNotFound)

		lista, err := s.categories.List(ctx, minha.ID, true)
		require.NoError(t, err)
		require.Len(t, lista, 1)
		assert.Equal(t, "Alimentação", lista[0].Name)

		// Filhas de um grupo alheio não vazam nem pedindo pelo id do pai.
		filhas, err := s.categories.Children(ctx, minha.ID, grupoAlheio.ID)
		require.NoError(t, err)
		assert.Empty(t, filhas)

		total, err := s.categories.CountAll(ctx, minha.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)

		taken, err := s.categories.NameTaken(ctx, minha.ID, nil, grupoAlheio.NameNorm, "")
		require.NoError(t, err)
		assert.False(t, taken)
	})
}

// O bug que este teste impede: `WHERE parent_id = ?` com nil nunca é
// verdadeiro em SQL (`= NULL` não casa com nada), então a unicidade dos GRUPOS
// simplesmente não valeria — e o código "pareceria" certo na revisão.
func TestUnicidadeDeGrupoUsaParentIdIsNull(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		grupo := s.makeCategory(t, ctx, minha.ID, "Moradia", category.KindExpense, nil)

		// Grupo com o mesmo nome: tomado.
		taken, err := s.categories.NameTaken(ctx, minha.ID, nil, grupo.NameNorm, "")
		require.NoError(t, err)
		assert.True(t, taken, "já existe um grupo chamado Moradia")

		// A MESMA palavra como FILHA de outro grupo: livre, porque a
		// unicidade é entre irmãos.
		outro := s.makeCategory(t, ctx, minha.ID, "Transporte", category.KindExpense, nil)
		taken, err = s.categories.NameTaken(ctx, minha.ID, &outro.ID, grupo.NameNorm, "")
		require.NoError(t, err)
		assert.False(t, taken, "filha pode se chamar como um grupo")
	})
}

func TestUnicidadeEntreIrmaosDeGruposDiferentes(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)

		moradia := s.makeCategory(t, ctx, minha.ID, "Moradia", category.KindExpense, nil)
		lazer := s.makeCategory(t, ctx, minha.ID, "Lazer", category.KindExpense, nil)
		energia := s.makeCategory(t, ctx, minha.ID, "Energia", category.KindExpense, &moradia.ID)

		// "Energia" já existe dentro de Moradia...
		taken, err := s.categories.NameTaken(ctx, minha.ID, &moradia.ID, energia.NameNorm, "")
		require.NoError(t, err)
		assert.True(t, taken)

		// ...mas está livre dentro de Lazer.
		taken, err = s.categories.NameTaken(ctx, minha.ID, &lazer.ID, energia.NameNorm, "")
		require.NoError(t, err)
		assert.False(t, taken)
	})
}

func TestChildrenTrazArquivadasTambem(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		grupo := s.makeCategory(t, ctx, minha.ID, "Moradia", category.KindExpense, nil)
		filha := s.makeCategory(t, ctx, minha.ID, "Energia", category.KindExpense, &grupo.ID)

		quando := now()
		filha.ArchivedAt = &quando
		require.NoError(t, s.categories.Update(ctx, filha))

		// Quem chama Children é a regra de exclusão: ela precisa enxergar a
		// filha arquivada, senão o grupo seria excluído deixando a filha órfã
		// — e sem FK física (ADR-013) o banco não barraria isso.
		filhas, err := s.categories.Children(ctx, minha.ID, grupo.ID)
		require.NoError(t, err)
		require.Len(t, filhas, 1)
		assert.NotNil(t, filhas[0].ArchivedAt)
	})
}

func TestUpdateNaoMoveCategoriaDeGrupo(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		moradia := s.makeCategory(t, ctx, minha.ID, "Moradia", category.KindExpense, nil)
		lazer := s.makeCategory(t, ctx, minha.ID, "Lazer", category.KindExpense, nil)
		filha := s.makeCategory(t, ctx, minha.ID, "Energia", category.KindExpense, &moradia.ID)

		// parent_id fica fora do SET de propósito (invariante 6 da spec 0003):
		// mesmo que um bug de serviço mande o pai trocado, o banco não muda.
		filha.ParentID = &lazer.ID
		filha.Name = "Energia elétrica"
		require.NoError(t, s.categories.Update(ctx, filha))

		recarregada, err := s.categories.ByID(ctx, minha.ID, filha.ID)
		require.NoError(t, err)
		assert.Equal(t, "Energia elétrica", recarregada.Name, "o nome muda")
		require.NotNil(t, recarregada.ParentID)
		assert.Equal(t, moradia.ID, *recarregada.ParentID, "o grupo NÃO muda")
	})
}

func TestCategoriaExcluidaSomeMasLiberaONome(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		c := s.makeCategory(t, ctx, minha.ID, "Assinaturas", category.KindExpense, nil)

		require.NoError(t, s.categories.SoftDelete(ctx, minha.ID, c.ID, now()))

		_, err := s.categories.ByID(ctx, minha.ID, c.ID)
		assert.ErrorIs(t, err, category.ErrNotFound)

		lista, err := s.categories.List(ctx, minha.ID, true)
		require.NoError(t, err)
		assert.Empty(t, lista)

		taken, err := s.categories.NameTaken(ctx, minha.ID, nil, c.NameNorm, "")
		require.NoError(t, err)
		assert.False(t, taken)
	})
}

func TestEscritaEmCategoriaDeOutraCasaNaoAcontece(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		daAlheia := s.makeCategory(t, ctx, alheia.ID, "Moradia", category.KindExpense, nil)

		forjada := *daAlheia
		forjada.HouseholdID = minha.ID
		forjada.Name = "Sequestrada"
		require.ErrorIs(t, s.categories.Update(ctx, &forjada), category.ErrNotFound)
		require.ErrorIs(t, s.categories.SoftDelete(ctx, minha.ID, daAlheia.ID, now()), category.ErrNotFound)

		intacta, err := s.categories.ByID(ctx, alheia.ID, daAlheia.ID)
		require.NoError(t, err)
		assert.Equal(t, "Moradia", intacta.Name)
	})
}
