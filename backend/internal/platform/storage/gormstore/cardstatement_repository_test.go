package gormstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeStatement grava uma fatura pronta na casa e na conta informadas.
func (s *store) makeStatement(t *testing.T, ctx context.Context, householdID, accountID, competencia string) *cardstatement.Statement {
	t.Helper()

	st := &cardstatement.Statement{
		ID:              s.nextID("cs"),
		HouseholdID:     householdID,
		AccountID:       accountID,
		CompetenceMonth: competencia,
		ClosingDate:     civil.MustNew(2026, 2, 2),
		DueDate:         civil.MustNew(2026, 2, 10),
		Source:          cardstatement.SourceImport,
		CreatedAt:       now(),
		UpdatedAt:       now(),
	}
	require.NoError(t, s.statements.Upsert(ctx, st))
	return st
}

// Isolamento por casa na fatura. A fatura é a ponte entre a compra e o mês em
// que ela aparece: vazar uma fatura da vizinha ligaria os lançamentos dela ao
// meu mês de competência.
func TestFaturaDeOutraCasaNaoApareceEmNenhumaConsulta(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		minhaConta := s.makeAccount(t, ctx, minha.ID, "Cartão")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Cartão da Vizinha")

		daAlheia := s.makeStatement(t, ctx, alheia.ID, contaAlheia.ID, "2026-02")
		s.makeStatement(t, ctx, minha.ID, minhaConta.ID, "2026-02")

		_, err := s.statements.ByID(ctx, minha.ID, daAlheia.ID)
		require.ErrorIs(t, err, cardstatement.ErrNotFound)

		lista, err := s.statements.List(ctx, minha.ID, cardstatement.ListFilter{})
		require.NoError(t, err)
		require.Len(t, lista, 1)
		assert.Equal(t, minhaConta.ID, lista[0].AccountID)

		// Filtrar pela conta da vizinha não devolve nada — nem confirma que
		// aquela conta existe.
		porConta, err := s.statements.List(ctx, minha.ID, cardstatement.ListFilter{AccountID: contaAlheia.ID})
		require.NoError(t, err)
		assert.Empty(t, porConta)
	})
}

// O Upsert é o que impede que importar o mesmo PDF duas vezes crie duas
// faturas de fevereiro. Ele é SELECT + INSERT/UPDATE (P8) e precisa devolver
// SEMPRE o mesmo id, porque é por esse id que os lançamentos se ligam à
// fatura — dois ids seriam duas metades da mesma fatura.
func TestUpsertDeFaturaEhIdempotentePorCompetencia(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		cartao := s.makeAccount(t, ctx, minha.ID, "Cartão")

		primeira := s.makeStatement(t, ctx, minha.ID, cartao.ID, "2026-02")

		segunda := &cardstatement.Statement{
			ID:              s.nextID("cs"), // id novo, que deve ser DESCARTADO
			HouseholdID:     minha.ID,
			AccountID:       cartao.ID,
			CompetenceMonth: "2026-02",
			ClosingDate:     civil.MustNew(2026, 2, 3),
			DueDate:         civil.MustNew(2026, 2, 12),
			Source:          cardstatement.SourceManual,
			CreatedAt:       now(),
			UpdatedAt:       now().Add(time.Hour),
		}
		require.NoError(t, s.statements.Upsert(ctx, segunda))
		assert.Equal(t, primeira.ID, segunda.ID, "o upsert precisa devolver o id da fatura que já existia")

		lista, err := s.statements.List(ctx, minha.ID, cardstatement.ListFilter{})
		require.NoError(t, err)
		require.Len(t, lista, 1, "importar o mesmo período duas vezes não pode criar duas faturas")
		assert.Equal(t, "2026-02-03", lista[0].ClosingDate.String(), "as datas corrigidas valem")
		assert.Equal(t, "2026-02-12", lista[0].DueDate.String())

		// Outra competência na MESMA conta é outra fatura — a chave inclui o mês.
		outra := s.makeStatement(t, ctx, minha.ID, cartao.ID, "2026-03")
		assert.NotEqual(t, primeira.ID, outra.ID)

		todas, err := s.statements.List(ctx, minha.ID, cardstatement.ListFilter{})
		require.NoError(t, err)
		require.Len(t, todas, 2)
		assert.Equal(t, "2026-03", todas[0].CompetenceMonth, "a mais recente vem primeiro")
	})
}

// A chave única do banco NÃO distingue linha excluída logicamente: uma fatura
// excluída continua ocupando aquela competência. Se o Upsert procurasse só
// entre as ativas, ele tentaria inserir, o índice recusaria, e aquele mês
// ficaria bloqueado para sempre — com um 500 na cara do usuário.
func TestUpsertRestauraFaturaExcluidaEmVezDeColidir(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		cartao := s.makeAccount(t, ctx, minha.ID, "Cartão")

		original := s.makeStatement(t, ctx, minha.ID, cartao.ID, "2026-02")

		// Exclusão lógica direta: o repositório não expõe SoftDelete de
		// fatura, e o cenário que precisamos provar é o do banco, não o da API.
		require.NoError(t, s.db.Gorm().
			Model(&gormstore.CardStatement{}).
			Where("id = ?", original.ID).
			Update("deleted_at", now()).Error)

		semExcluida, err := s.statements.List(ctx, minha.ID, cardstatement.ListFilter{})
		require.NoError(t, err)
		require.Empty(t, semExcluida, "fatura excluída não aparece na listagem")

		revivida := &cardstatement.Statement{
			ID: s.nextID("cs"), HouseholdID: minha.ID, AccountID: cartao.ID,
			CompetenceMonth: "2026-02",
			ClosingDate:     civil.MustNew(2026, 2, 2), DueDate: civil.MustNew(2026, 2, 10),
			Source: cardstatement.SourceImport, CreatedAt: now(), UpdatedAt: now(),
		}
		require.NoError(t, s.statements.Upsert(ctx, revivida))
		assert.Equal(t, original.ID, revivida.ID)

		depois, err := s.statements.List(ctx, minha.ID, cardstatement.ListFilter{})
		require.NoError(t, err)
		require.Len(t, depois, 1, "a fatura excluída foi restaurada, não duplicada")
	})
}

// Upsert sem casa, conta ou competência é recusado antes de qualquer SQL: as
// três colunas formam a chave única, e string vazia PASSA no NOT NULL — o que
// criaria uma "fatura do mês vazio" por casa.
func TestUpsertDeFaturaRecusaChaveIncompleta(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		cartao := s.makeAccount(t, ctx, minha.ID, "Cartão")

		assert.Error(t, s.statements.Upsert(ctx, &cardstatement.Statement{
			ID: s.nextID("cs"), HouseholdID: minha.ID, AccountID: cartao.ID,
			CompetenceMonth: "", CreatedAt: now(), UpdatedAt: now(),
		}))
		assert.Error(t, s.statements.Upsert(ctx, &cardstatement.Statement{
			ID: s.nextID("cs"), HouseholdID: "", AccountID: cartao.ID,
			CompetenceMonth: "2026-02", CreatedAt: now(), UpdatedAt: now(),
		}))
	})
}
