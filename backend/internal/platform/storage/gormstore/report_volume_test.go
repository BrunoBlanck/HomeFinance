package gormstore_test

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Volume: o relatório de um mês pesado — 200 categorias (o teto da taxonomia),
// 10 contas (3 delas cartão) e 10 mil lançamentos vivos — tem de sair em UMA
// consulta, com a saída limitada pela ESTRUTURA (categorias × contas) e não
// pelo volume. É o teste que pega um N+1 antes do usuário: nada aqui pode
// crescer com o número de lançamentos, e o recorte por conta (ADR-032) não
// acrescenta consulta nenhuma à agregação.

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

func TestSumByCategoryAndAccountEmVolumeEhUmaConsultaSo(t *testing.T) {
	if testing.Short() {
		t.Skip("volume: pulado em -short")
	}
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		// --- 10 contas, 3 delas cartão de crédito ---------------------------
		//
		// Com a conta na CHAVE de agrupamento, o número de contas multiplica as
		// LINHAS da saída — e é isso que o teste mede: 10 contas × 200
		// categorias continua sendo UMA consulta, e a saída continua limitada
		// pela estrutura, não pelo volume.
		const nContas = 10
		// Três cartões (0, 3 e 6), uma poupança (9) e seis correntes: o
		// recorte tem de separar um conjunto que não é nem tudo nem nada.
		tipoDaConta := func(c int) string {
			switch {
			case c == 9:
				return account.KindSavings
			case c%3 == 0:
				return account.KindCreditCard
			default:
				return account.KindChecking
			}
		}
		contasIDs := make([]string, 0, nContas)
		cartoes := map[string]bool{}
		for c := range nContas {
			kind := tipoDaConta(c)
			a := s.makeAccountKind(t, ctx, minha.ID, fmt.Sprintf("Conta %02d", c), kind, false)
			contasIDs = append(contasIDs, a.ID)
			if kind == account.KindCreditCard {
				cartoes[a.ID] = true
			}
		}
		require.Len(t, cartoes, 3, "três cartões entre as dez contas")
		conta := contasIDs[0]

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

		esperadoPorChave := map[[2]string]int64{}
		esperadoCartao := int64(0)
		for i := range nVivos {
			cents := int64(1 + i%997)
			// A conta NÃO pode ser função de `i % len(ids)`: seria a mesma
			// conta para a mesma categoria sempre, e a saída teria uma linha
			// por categoria — exatamente o que este teste deveria detectar se
			// alguém removesse a conta da chave. Com `i / len(ids)` cada
			// categoria aparece em TODAS as dez contas.
			contaDaLinha := contasIDs[(i/len(ids))%nContas]
			chave := ""
			var cat *string
			if i%25 != 0 { // 4 % sem categoria, no balde
				id := ids[i%len(ids)]
				cat = &id
				chave = id
			}
			esperadoPorCategoria[chave] += cents
			esperadoPorChave[[2]string{chave, contaDaLinha}] += cents
			if cartoes[contaDaLinha] {
				esperadoCartao += cents
			}
			esperadoTotal += cents
			novo(minha.ID, contaDaLinha, cat, transaction.KindExpense, cents, "2026-09", false)
		}
		enviar()

		// --- ruído que o WHERE precisa podar --------------------------------
		for i := range 60 {
			id := ids[i%len(ids)]
			novo(minha.ID, conta, &id, transaction.KindExpense, 1_000_000, "2026-10", false) // outro mês
			novo(minha.ID, conta, &id, transaction.KindIncome, 2_000_000, "2026-09", false)  // outra natureza
			novo(alheia.ID, contaAlheia.ID, nil, transaction.KindExpense, 3_000_000, "2026-09", false)
		}
		enviar()
		// Excluídas: entram vivas e saem por SoftDelete.
		excluidas := make([]string, 0, 20)
		for range 20 {
			id := s.nextID("t")
			excluidas = append(excluidas, id)
			lote = append(lote, transaction.Transaction{
				ID: id, HouseholdID: minha.ID, Kind: transaction.KindExpense, AccountID: conta,
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
		rows, err := s.transactions.SumByCategoryAndAccount(ctx, minha.ID, "2026-09", transaction.KindExpense)
		decorrido := time.Since(inicio)
		require.NoError(t, err)

		assert.Equal(t, int64(1), contador.n.Load(),
			"a agregação é UMA consulta — qualquer número maior é N+1 esperando o mês cheio")
		assert.LessOrEqual(t, len(rows), (category.MaxPerHousehold+1)*account.MaxPerHousehold,
			"a saída é limitada pela estrutura, nunca pelo volume de lançamentos")
		assert.LessOrEqual(t, len(rows), (category.MaxPerHousehold+1)*nContas,
			"e, na prática, pelas contas que a casa tem")

		var total, count int64
		porCategoriaObtido := map[string]int64{}
		var cartaoObtido int64
		for _, r := range rows {
			chave := ""
			if r.CategoryID != nil {
				chave = *r.CategoryID
			}
			total += r.TotalCents
			count += r.Count
			porCategoriaObtido[chave] += r.TotalCents
			if cartoes[r.AccountID] {
				cartaoObtido += r.TotalCents
			}
			assert.Equal(t, esperadoPorChave[[2]string{chave, r.AccountID}], r.TotalCents,
				"chave (%q, %q)", chave, r.AccountID)
		}
		assert.Equal(t, esperadoPorCategoria, porCategoriaObtido,
			"somadas por categoria, as linhas por conta dão a agregação antiga")
		assert.Equal(t, esperadoCartao, cartaoObtido, "a partição `credit` sai das mesmas linhas")
		assert.Equal(t, esperadoTotal, total, "sem o ruído de outro mês, outra natureza, outra casa e excluídos")
		assert.Equal(t, int64(nVivos), count)

		t.Logf("%s: %d lançamentos vivos, %d categorias, %d contas → %d linhas, %d consulta(s), %s",
			s.backendName, nVivos, len(ids), nContas, len(rows), contador.n.Load(), decorrido)
		assert.Less(t, decorrido, 5*time.Second, "a agregação indexada não pode levar segundos")

		// O relatório completo, pelo SERVIÇO, nos três recortes: cada um é a
		// agregação (1) mais as categorias (1) mais — só nos recortes — as
		// contas (1). Nenhuma consulta cresce com o volume.
		svc := report.NewService(s.transactions, s.categories, s.accounts, logging.Discard())
		ator := report.Actor{HouseholdID: minha.ID, UserID: "u"}
		medir := func(grupo string, esperadas int64) report.CategoryReportView {
			contador.n.Store(0)
			v, err := svc.ByCategory(ctx, ator, report.ByCategoryInput{Month: "2026-09", Kind: "expense", AccountGroup: grupo})
			require.NoError(t, err)
			assert.Equal(t, esperadas, contador.n.Load(), "consultas no recorte %q", grupo)
			return v
		}
		todas := medir("", 2)
		credito := medir(report.AccountGroupCredit, 3)
		debito := medir(report.AccountGroupDebit, 3)

		assert.Equal(t, esperadoTotal, todas.TotalCents)
		assert.Equal(t, esperadoCartao, credito.TotalCents)
		assert.Equal(t, todas.TotalCents, credito.TotalCents+debito.TotalCents, "credit + debit == todas, com 10 mil linhas")
		assert.Equal(t, todas.Count, credito.Count+debito.Count)
	})
}
