package gormstore_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Volume (critério 17 da spec 0008): o painel de um mês pesado — 10.000
// lançamentos vivos, 200 categorias marcadas e as contas da casa no teto —
// sai em UMA consulta, com a saída limitada pelo DOMÍNIO (≤ 2 ×
// account.MaxPerHousehold linhas) e não pelo volume.
//
// É o teste que pega um N+1 antes do usuário: a home é a rota mais chamada do
// app, e nada aqui pode crescer com o número de lançamentos. O tempo é MEDIDO
// e impresso — a referência da E6a (SumByCategory) no mesmo volume foi
// ~20,8 ms; o número desta consulta vai no log da suíte, não numa asserção
// apertada, porque tempo de máquina de CI não é critério de correção. A
// asserção que vale é a contagem de consultas.
func TestPainelEmVolumeEhUmaConsultaSo(t *testing.T) {
	if testing.Short() {
		t.Skip("volume: pulado em -short")
	}
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		// --- as contas da casa: 10, sendo 3 cartões -------------------------
		//
		// A saída da agregação é uma linha por (kind, conta), então o número de
		// CONTAS é o que a limita — nunca o de lançamentos.
		const nContas = 10
		contas := make([]string, 0, nContas)
		for i := range nContas {
			if i%3 == 0 {
				contas = append(contas, contaDeCartao(t, ctx, s, minha.ID, fmt.Sprintf("Cartão %02d", i)).ID)
				continue
			}
			contas = append(contas, s.makeAccount(t, ctx, minha.ID, fmt.Sprintf("Conta %02d", i)).ID)
		}

		// --- 200 categorias, das quais 40 marcadas como investimento ---------
		const nMarcadas = 40
		todas := make([]string, 0, category.MaxPerHousehold)
		marcadas := make([]string, 0, nMarcadas)
		for i := range category.MaxPerHousehold {
			kind := category.KindExpense
			switch {
			case i < nMarcadas/2:
				kind = category.KindInvestment
			case i < nMarcadas:
				kind = category.KindRedemption
			}
			c := s.makeCategory(t, ctx, minha.ID, fmt.Sprintf("Categoria %03d", i), kind, nil)
			todas = append(todas, c.ID)
			if i < nMarcadas {
				marcadas = append(marcadas, c.ID)
			}
		}
		require.Len(t, marcadas, nMarcadas)

		// --- 10 mil lançamentos vivos no mês --------------------------------
		//
		// Semear custa segundos (vai em lotes de 30, pelo teto de parâmetros);
		// a agregação sai em dezenas de milissegundos. É a diferença que o
		// teste existe para guardar.
		const nVivos = 10_000
		set := civil.MustNew(2026, 9, 10)

		esperado := map[[2]string]*[4]int64{} // (kind, conta) → cnt, total, markedCnt, markedTotal
		anotar := func(kind, conta string, cents int64, marcada bool) {
			chave := [2]string{kind, conta}
			linha, ok := esperado[chave]
			if !ok {
				linha = &[4]int64{}
				esperado[chave] = linha
			}
			linha[0]++
			linha[1] += cents
			if marcada {
				linha[2]++
				linha[3] += cents
			}
		}

		lote := make([]transaction.Transaction, 0, 500)
		enviar := func() {
			if len(lote) == 0 {
				return
			}
			require.NoError(t, s.transactions.CreateBatch(ctx, minha.ID, lote))
			lote = lote[:0]
		}
		novo := func(casa, conta string, categoria *string, kind string, cents int64, mes string, grupo *string) transaction.Transaction {
			tx := transaction.Transaction{
				ID:              s.nextID("t"),
				HouseholdID:     casa,
				Kind:            kind,
				AccountID:       conta,
				CategoryID:      categoria,
				AmountCents:     cents,
				Description:     "Compra",
				DescriptionNorm: textnorm.Normalize("Compra"),
				OccurredOn:      set,
				CompetenceMonth: mes,
				TransferGroupID: grupo,
				Source:          transaction.SourceManual,
				DedupKey:        s.nextID("dk"),
				DedupOrdinal:    1,
				CreatedBy:       s.nextID("u"),
				CreatedAt:       now(),
				UpdatedAt:       now(),
			}
			if casa != minha.ID {
				require.NoError(t, s.transactions.CreateBatch(ctx, casa, []transaction.Transaction{tx}))
				return tx
			}
			lote = append(lote, tx)
			if len(lote) == cap(lote) {
				enviar()
			}
			return tx
		}

		for i := range nVivos {
			cents := int64(1 + i%997)
			conta := contas[i%len(contas)]
			kind := transaction.KindExpense
			if i%4 == 0 {
				kind = transaction.KindIncome
			}

			var categoria *string
			marcada := false
			switch {
			case i%25 == 0: // 4 % sem categoria — o estado pós-importação
			default:
				id := todas[i%len(todas)]
				categoria = &id
				marcada = i%len(todas) < nMarcadas
			}

			anotar(kind, conta, cents, marcada)
			novo(minha.ID, conta, categoria, kind, cents, "2026-09", nil)
		}
		enviar()

		// --- ruído que o WHERE precisa podar --------------------------------
		for i := range 60 {
			conta := contas[i%len(contas)]
			id := todas[i%len(todas)]
			novo(minha.ID, conta, &id, transaction.KindExpense, 1_000_000, "2026-10", nil) // outro mês
			novo(alheia.ID, contaAlheia.ID, nil, transaction.KindExpense, 3_000_000, "2026-09", nil)

			// Transferência interna: as duas pernas, no mês, na casa certa —
			// e nenhuma delas pode ser lida (ADR-016).
			grupo := s.nextID("tg")
			novo(minha.ID, contas[0], nil, transaction.KindTransferOut, 5_000_000, "2026-09", &grupo)
			novo(minha.ID, contas[1], nil, transaction.KindTransferIn, 5_000_000, "2026-09", &grupo)
		}
		enviar()

		// Excluídas logicamente: entram vivas e saem por SoftDelete.
		excluidas := make([]string, 0, 20)
		for i := range 20 {
			tx := novo(minha.ID, contas[i%len(contas)], nil, transaction.KindExpense, 9_000_000, "2026-09", nil)
			excluidas = append(excluidas, tx.ID)
		}
		enviar()
		for _, id := range excluidas {
			require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, id, now()))
		}

		// --- a medição -------------------------------------------------------
		contador := instrumentarConsultas(t, s)
		inicio := time.Now()
		rows, err := s.transactions.SumMonthByKindAndAccount(ctx, minha.ID, "2026-09", marcadas)
		decorrido := time.Since(inicio)
		require.NoError(t, err)

		assert.Equal(t, int64(1), contador.n.Load(),
			"a agregação do painel é UMA consulta — qualquer número maior é N+1 na rota mais chamada do app")
		assert.LessOrEqual(t, len(rows), 2*account.MaxPerHousehold,
			"a saída é limitada pelo domínio (2 kinds × contas da casa), nunca pelo volume")

		// --- conferência linha a linha ---------------------------------------
		require.Len(t, rows, len(esperado), "uma linha por (kind, conta) com movimento no mês")
		var total, cnt, marcado int64
		for _, r := range rows {
			linha, ok := esperado[[2]string{r.Kind, r.AccountID}]
			require.Truef(t, ok, "linha inesperada: %+v", r)
			assert.Equalf(t, linha[0], r.Count, "contagem de (%s, %s)", r.Kind, r.AccountID)
			assert.Equalf(t, linha[1], r.TotalCents, "total de (%s, %s)", r.Kind, r.AccountID)
			assert.Equalf(t, linha[2], r.MarkedCount, "contagem marcada de (%s, %s)", r.Kind, r.AccountID)
			assert.Equalf(t, linha[3], r.MarkedTotalCents, "marcado de (%s, %s)", r.Kind, r.AccountID)
			assert.LessOrEqualf(t, r.MarkedTotalCents, r.TotalCents,
				"0 ≤ marcado ≤ total, a desigualdade que o serviço verifica: %+v", r)
			total += r.TotalCents
			cnt += r.Count
			marcado += r.MarkedTotalCents
		}
		assert.EqualValues(t, nVivos, cnt,
			"sem o outro mês, sem a outra casa, sem os excluídos e sem NENHUMA perna de transferência")

		t.Logf("%s: %d lançamentos vivos, %d contas, %d categorias marcadas → %d linhas, %d consulta(s), %s "+
			"(total %d centavos, marcado %d)",
			s.backendName, nVivos, len(contas), len(marcadas), len(rows), contador.n.Load(), decorrido, total, marcado)
		assert.Less(t, decorrido, 5*time.Second, "a agregação indexada não pode levar segundos")
	})
}
