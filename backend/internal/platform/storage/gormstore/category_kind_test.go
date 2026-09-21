package gormstore_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// As naturezas `investment` e `redemption` têm EXATAMENTE 10 caracteres e
// cabem no `varchar(10)` que já existe (ADR-029a): o schema não muda de versão
// e o AutoMigrate não tem o que fazer.
//
// "Cabem" é uma afirmação sobre o BANCO, não sobre Go — e os quatro dialetos
// tratam o excedente de um varchar de formas diferentes (uns truncam, outros
// recusam). Por isso a prova é ida e volta, em cada backend disponível: grava
// e relê. Uma truncagem silenciosa para "investmen" tiraria a categoria de
// todo switch da allowlist sem erro nenhum.
func TestNaturezasNovasSobrevivemAoRoundtripNoBanco(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casa := s.makeHousehold(t, ctx, s.makeUser(t, ctx, true).ID, "owner")

		for _, kind := range []string{
			category.KindIncome, category.KindExpense,
			category.KindInvestment, category.KindRedemption,
		} {
			require.LessOrEqual(t, len(kind), 10,
				"%q não cabe no varchar(10) de categories.kind — uma quinta natureza mais longa exige migração", kind)

			criada := s.makeCategory(t, ctx, casa.ID, "Grupo "+kind, kind, nil)

			relida, err := s.categories.ByID(ctx, casa.ID, criada.ID)
			require.NoError(t, err)
			assert.Equal(t, kind, relida.Kind, "a natureza volta inteira do banco, sem truncagem")

			// E pela listagem, que é o caminho da árvore e do classificador.
			todas, err := s.categories.List(ctx, casa.ID, false)
			require.NoError(t, err)
			var achou bool
			for _, c := range todas {
				if c.ID == criada.ID {
					achou = true
					assert.Equal(t, kind, c.Kind)
				}
			}
			assert.True(t, achou, "a categoria de natureza %q aparece na listagem", kind)
		}
	})
}

// A troca de natureza é gravada pelo Update, que traz `kind` no Select — é o
// mecanismo da cascata do ADR-029c. Aqui ele é exercitado contra o banco de
// verdade, no grupo E na filha, porque um Select que esquecesse a coluna
// falharia em silêncio (0 linhas alteradas nunca aconteceria: o nome também
// muda... e no caso da cascata, NADA mais muda além do kind).
func TestUpdateGravaApenasATrocaDeNaturezaNoGrupoENaFilha(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casa := s.makeHousehold(t, ctx, s.makeUser(t, ctx, true).ID, "owner")

		grupo := s.makeCategory(t, ctx, casa.ID, "Investimentos", category.KindExpense, nil)
		filha := s.makeCategory(t, ctx, casa.ID, "CDB", category.KindExpense, &grupo.ID)

		grupo.Kind = category.KindInvestment
		require.NoError(t, s.categories.Update(ctx, grupo))
		filha.Kind = category.KindInvestment
		require.NoError(t, s.categories.Update(ctx, filha))

		relidoGrupo, err := s.categories.ByID(ctx, casa.ID, grupo.ID)
		require.NoError(t, err)
		assert.Equal(t, category.KindInvestment, relidoGrupo.Kind)
		assert.Equal(t, "Investimentos", relidoGrupo.Name, "só a natureza mudou")
		assert.Nil(t, relidoGrupo.ParentID)

		relidaFilha, err := s.categories.ByID(ctx, casa.ID, filha.ID)
		require.NoError(t, err)
		assert.Equal(t, category.KindInvestment, relidaFilha.Kind)
		require.NotNil(t, relidaFilha.ParentID)
		assert.Equal(t, grupo.ID, *relidaFilha.ParentID, "a cascata não move a filha de grupo")
	})
}

// BOLA: a troca de natureza não alcança categoria de outra casa. O escopo do
// repositório filtra por household_id na camada mais baixa, e um Update com a
// casa errada não altera linha nenhuma — responde ErrNotFound, nunca 403.
func TestTrocaDeNaturezaNaoAlcancaCategoriaDeOutraCasa(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		daOutra := s.makeCategory(t, ctx, alheia.ID, "Investimentos", category.KindExpense, nil)

		forjada := *daOutra
		forjada.HouseholdID = minha.ID
		forjada.Kind = category.KindInvestment
		require.ErrorIs(t, s.categories.Update(ctx, &forjada), category.ErrNotFound)

		// Nada mudou na casa alheia.
		intacta, err := s.categories.ByID(ctx, alheia.ID, daOutra.ID)
		require.NoError(t, err)
		assert.Equal(t, category.KindExpense, intacta.Kind)

		// E ela continua invisível para a minha casa.
		_, err = s.categories.ByID(ctx, minha.ID, daOutra.ID)
		require.ErrorIs(t, err, category.ErrNotFound)
	})
}
