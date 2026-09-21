package gormstore_test

import (
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rodada de QA da tarefa T5 da E2d: a PARTIÇÃO do filtro de tipo conferida
// contra o banco REAL, sobre cenários sorteados e percorrida página a página.
//
// O `transaction_e2d_test.go` prova a partição num cenário fixo de oito
// linhas. Um cenário fixo não pega o que muda com a forma dos dados: a linha
// sem categoria que cai no dia em que também há aporte, a página que termina
// exatamente na perna de transferência, o mês em que um dos quatro grupos é
// vazio. É para isso que serve o sorteio.
//
// O que a propriedade trava, e que nenhum exemplo trava sozinho:
//
//	união dos quatro grupos == "Tudo", e interseção dois a dois == vazio
//
// Perder o `category_id IS NULL OR` derruba a igualdade da esquerda; tratar
// `investment` como um `kind` derruba a da direita; e perder o parêntese que o
// GORM põe em volta do `OR` derruba as duas de uma vez, porque a consulta passa
// a atravessar o `household_id`.

// paginarTudo percorre o recorte inteiro com `limit`, pelo cursor, e devolve
// os ids na ordem em que o banco os entregou.
//
// A guarda de voltas existe para que um cursor que não avança falhe como
// TESTE, e não como suíte travada.
func paginarTudo(t *testing.T, s *store, casa, grupo string, marcadas []string, limite int) []string {
	t.Helper()

	var vistos []string
	var cursor *transaction.Cursor
	for voltas := 0; ; voltas++ {
		require.Less(t, voltas, 500, "o cursor não avançou no grupo %q", grupo)
		rows, err := s.transactions.List(t.Context(), casa, transaction.ListFilter{
			CompetenceMonth:       mesDoCenario,
			KindGroup:             grupo,
			InvestmentCategoryIDs: marcadas,
			Cursor:                cursor,
			Limit:                 limite,
		})
		require.NoError(t, err)
		if len(rows) == 0 {
			return vistos
		}
		for i := range rows {
			// A casa é conferida em TODA linha de TODA página: o vazamento do
			// `OR` sem parêntese aparece aqui, e não na contagem.
			require.Equal(t, casa, rows[i].HouseholdID,
				"BOLA: o grupo %q devolveu linha da casa %s", grupo, rows[i].HouseholdID)
		}
		vistos = append(vistos, idsDe(rows)...)
		ultima := rows[len(rows)-1]
		cursor = &transaction.Cursor{OccurredOn: ultima.OccurredOn, ID: ultima.ID}
	}
}

// TestQAParticaoDoFiltroDeTipoContraOBancoReal é a propriedade da §12.5.1 e
// da §12.5.2 medida no SQL de verdade, e não na aritmética do dublê.
func TestQAParticaoDoFiltroDeTipoContraOBancoReal(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta Corrente")
		outra := s.makeAccount(t, ctx, minha.ID, "Poupanca")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		salario := s.makeCategory(t, ctx, minha.ID, "Salario", category.KindIncome, nil)
		cdb := s.makeCategory(t, ctx, minha.ID, "CDB", category.KindInvestment, nil)
		resgateCat := s.makeCategory(t, ctx, minha.ID, "Resgate CDB", category.KindRedemption, nil)
		marcadas := []string{cdb.ID, resgateCat.ID}

		// 120 linhas sorteadas entre as sete situações que a partição separa,
		// mais linhas da casa vizinha com a MESMA categoria marcada — para o
		// vazamento ter o que vazar.
		r := rand.New(rand.NewPCG(20260918, 0x5eed))
		naMinhaCasa := map[string]bool{}
		for i := range 120 {
			dia := civil.MustNew(2026, 9, 1+r.IntN(28))
			valor := int64(1 + r.IntN(500_000))
			var spec txSpec
			switch r.IntN(7) {
			case 0:
				spec = txSpec{Kind: transaction.KindIncome, AmountCents: valor, OccurredOn: dia, CategoryID: &salario.ID}
			case 1:
				spec = txSpec{Kind: transaction.KindIncome, AmountCents: valor, OccurredOn: dia}
			case 2:
				spec = txSpec{Kind: transaction.KindExpense, AmountCents: valor, OccurredOn: dia, CategoryID: &mercado.ID}
			case 3:
				// A armadilha do `NULL NOT IN`, sorteada muitas vezes.
				spec = txSpec{Kind: transaction.KindExpense, AmountCents: valor, OccurredOn: dia}
			case 4:
				spec = txSpec{Kind: transaction.KindExpense, AmountCents: valor, OccurredOn: dia, CategoryID: &cdb.ID}
			case 5:
				spec = txSpec{Kind: transaction.KindIncome, AmountCents: valor, OccurredOn: dia, CategoryID: &resgateCat.ID}
			default:
				// O par de transferência inteiro, nas duas contas.
				grupo := s.nextID("g")
				saida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
					Kind: transaction.KindTransferOut, AmountCents: valor, OccurredOn: dia, TransferGroupID: &grupo,
				})
				entrada := s.makeTransaction(t, ctx, minha.ID, outra.ID, txSpec{
					Kind: transaction.KindTransferIn, AmountCents: valor, OccurredOn: dia, TransferGroupID: &grupo,
				})
				naMinhaCasa[saida.ID] = true
				naMinhaCasa[entrada.ID] = true
				continue
			}
			naMinhaCasa[s.makeTransaction(t, ctx, minha.ID, conta.ID, spec).ID] = true

			if i%7 == 0 {
				// A vizinha, no MESMO mês e com a MESMA categoria marcada.
				s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
					Kind: transaction.KindExpense, AmountCents: valor, OccurredOn: dia, CategoryID: &cdb.ID,
				})
			}
		}

		// Limites de página diferentes: 1 é o tamanho em que todo defeito de
		// cursor aparece; 7 quebra as páginas em lugares arbitrários; o
		// default fecha em uma ou duas páginas.
		for _, limite := range []int{1, 7, 0} {
			t.Run(fmt.Sprintf("limite-%d", limite), func(t *testing.T) {
				tudo := paginarTudo(t, s, minha.ID, "", nil, limite)
				assert.Len(t, tudo, len(naMinhaCasa), "\"Tudo\" tem de trazer a casa inteira")

				dono := map[string]string{}
				var soma int
				for _, grupo := range []string{
					transaction.KindGroupIncome, transaction.KindGroupExpense,
					transaction.KindGroupTransfer, transaction.KindGroupInvestment,
				} {
					ids := paginarTudo(t, s, minha.ID, grupo, marcadas, limite)
					soma += len(ids)
					for _, id := range ids {
						if antes, jaVisto := dono[id]; jaVisto {
							t.Fatalf("dupla contagem: %s está em %q e em %q", id, antes, grupo)
						}
						dono[id] = grupo
					}
				}

				assert.Equal(t, len(tudo), soma,
					"a soma dos quatro grupos tem de ser exatamente o \"Tudo\"")
				faltando := make([]string, 0)
				for _, id := range tudo {
					if _, ok := dono[id]; !ok {
						faltando = append(faltando, id)
					}
				}
				assert.Empty(t, faltando,
					"linha que não caiu em grupo nenhum — é assim que a despesa sem categoria some")
			})
		}
	})
}
