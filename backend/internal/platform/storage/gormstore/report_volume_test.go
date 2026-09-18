package gormstore_test

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Volume: o relatório de um mês pesado — 200 categorias (o teto da taxonomia)
// e 10 mil lançamentos vivos — tem de sair em UMA consulta, com a saída
// limitada pela taxonomia e não pelo volume. É o teste que pega um N+1 antes
// do usuário: nada aqui pode crescer com o número de lançamentos.

// contadorDeConsultas conta as idas ao banco. `Scan` passa pelos callbacks de
// Query; Raw e Row entram junto para que nenhuma consulta escape da contagem.
type contadorDeConsultas struct{ n atomic.Int64 }

func instrumentarConsultas(t *testing.T, s *store) *contadorDeConsultas {
	t.Helper()
	c := &contadorDeConsultas{}
	contar := func(*gorm.DB) { c.n.Add(1) }
	g := s.db.Gorm()
	require.NoError(t, g.Callback().Query().After("gorm:query").Register("qa:contar_query", contar))
	require.NoError(t, g.Callback().Raw().After("gorm:raw").Register("qa:contar_raw", contar))
	require.NoError(t, g.Callback().Row().After("gorm:row").Register("qa:contar_row", contar))
	t.Cleanup(func() {
		_ = g.Callback().Query().Remove("qa:contar_query")
		_ = g.Callback().Raw().Remove("qa:contar_raw")
		_ = g.Callback().Row().Remove("qa:contar_row")
	})
	return c
}

func TestSumByCategoryEmVolumeEhUmaConsultaSo(t *testing.T) {
	if testing.Short() {
		t.Skip("volume: pulado em -short")
	}
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		// --- 200 categorias: 50 grupos com 3 filhas cada --------------------
		const nGrupos = 50
		ids := make([]string, 0, category.MaxPerHousehold)
		for g := range nGrupos {
			pai := s.makeCategory(t, ctx, minha.ID, fmt.Sprintf("Grupo %02d", g), category.KindExpense, nil)
			ids = append(ids, pai.ID)
			for f := range 3 {
				filha := s.makeCategory(t, ctx, minha.ID, fmt.Sprintf("Filha %02d %d", g, f), category.KindExpense, &pai.ID)
				ids = append(ids, filha.ID)
			}
		}
		require.Len(t, ids, category.MaxPerHousehold, "a casa está no teto da taxonomia")

		// --- 10 mil lançamentos vivos no mês --------------------------------
		//
		// Semear 10 000 linhas custa ~1,5 s em SQLite porque vai em lotes; a
		// consulta em si sai em dezenas de MILISSEGUNDOS. É a diferença que o
		// teste existe para guardar: o que não pode crescer com o volume é a
		// AGREGAÇÃO, e é por isso que a contagem de consultas é assertada.
		const nVivos = 10_000
		set := civil.MustNew(2026, 9, 10)
		esperadoPorCategoria := map[string]int64{}
		var esperadoTotal int64
		lote := make([]transaction.Transaction, 0, 500)
		enviar := func() {
			if len(lote) == 0 {
				return
			}
			require.NoError(t, s.transactions.CreateBatch(ctx, minha.ID, lote))
			lote = lote[:0]
		}
		novo := func(casa, contaID string, categoriaID *string, kind string, cents int64, mes string, deletada bool) {
			tx := transaction.Transaction{
				ID:              s.nextID("t"),
				HouseholdID:     casa,
				Kind:            kind,
				AccountID:       contaID,
				CategoryID:      categoriaID,
				AmountCents:     cents,
				Description:     "Compra",
				DescriptionNorm: textnorm.Normalize("Compra"),
				OccurredOn:      set,
				CompetenceMonth: mes,
				Source:          transaction.SourceManual,
				DedupKey:        s.nextID("dk"),
				DedupOrdinal:    1,
				CreatedBy:       s.nextID("u"),
				CreatedAt:       now(),
				UpdatedAt:       now(),
			}
			if casa == minha.ID {
				lote = append(lote, tx)
				if len(lote) == cap(lote) {
					enviar()
				}
				return
			}
			require.NoError(t, s.transactions.CreateBatch(ctx, casa, []transaction.Transaction{tx}))
		}

		for i := range nVivos {
			cents := int64(1 + i%997)
			var cat *string
			if i%25 != 0 { // 4 % sem categoria, no balde
				id := ids[i%len(ids)]
				cat = &id
				esperadoPorCategoria[id] += cents
			} else {
				esperadoPorCategoria[""] += cents
			}
			esperadoTotal += cents
			novo(minha.ID, conta.ID, cat, transaction.KindExpense, cents, "2026-09", false)
		}
		enviar()

		// --- ruído que o WHERE precisa podar --------------------------------
		for i := range 60 {
			id := ids[i%len(ids)]
			novo(minha.ID, conta.ID, &id, transaction.KindExpense, 1_000_000, "2026-10", false) // outro mês
			novo(minha.ID, conta.ID, &id, transaction.KindIncome, 2_000_000, "2026-09", false)  // outra natureza
			novo(alheia.ID, contaAlheia.ID, nil, transaction.KindExpense, 3_000_000, "2026-09", false)
		}
		enviar()
		// Excluídas: entram vivas e saem por SoftDelete.
		excluidas := make([]string, 0, 20)
		for range 20 {
			id := s.nextID("t")
			excluidas = append(excluidas, id)
			lote = append(lote, transaction.Transaction{
				ID: id, HouseholdID: minha.ID, Kind: transaction.KindExpense, AccountID: conta.ID,
				CategoryID: &ids[0], AmountCents: 9_000_000, Description: "Some", DescriptionNorm: "some",
				OccurredOn: set, CompetenceMonth: "2026-09", Source: transaction.SourceManual,
				DedupKey: s.nextID("dk"), DedupOrdinal: 1, CreatedBy: s.nextID("u"),
				CreatedAt: now(), UpdatedAt: now(),
			})
		}
		enviar()
		for _, id := range excluidas {
			require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, id, now()))
		}

		// --- a medição -------------------------------------------------------
		contador := instrumentarConsultas(t, s)
		inicio := time.Now()
		rows, err := s.transactions.SumByCategory(ctx, minha.ID, "2026-09", transaction.KindExpense)
		decorrido := time.Since(inicio)
		require.NoError(t, err)

		assert.Equal(t, int64(1), contador.n.Load(),
			"a agregação é UMA consulta — qualquer número maior é N+1 esperando o mês cheio")
		assert.LessOrEqual(t, len(rows), category.MaxPerHousehold+1,
			"a saída é limitada pela taxonomia, nunca pelo volume de lançamentos")

		var total, count int64
		for _, r := range rows {
			chave := ""
			if r.CategoryID != nil {
				chave = *r.CategoryID
			}
			total += r.TotalCents
			count += r.Count
			assert.Equal(t, esperadoPorCategoria[chave], r.TotalCents, "categoria %q", chave)
		}
		assert.Equal(t, esperadoTotal, total, "sem o ruído de outro mês, outra natureza, outra casa e excluídos")
		assert.Equal(t, int64(nVivos), count)

		t.Logf("%s: %d lançamentos vivos, %d categorias → %d linhas, %d consulta(s), %s",
			s.backendName, nVivos, len(ids), len(rows), contador.n.Load(), decorrido)
		assert.Less(t, decorrido, 5*time.Second, "a agregação indexada não pode levar segundos")
	})
}
