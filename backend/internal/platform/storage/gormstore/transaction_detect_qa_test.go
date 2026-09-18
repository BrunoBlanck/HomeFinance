package gormstore_test

import (
	"context"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Critério 1 da §13.5, a parte financeira, contra banco REAL: converter o par
// não pode mover um centavo de saldo, tem de tirar a receita e a despesa do
// `summary` do mês, e o par tem de passar a aparecer na listagem de
// transferências. É o invariante que o resto dos testes assume e ninguém
// mediu de ponta a ponta — e é o único que, se quebrar, some dinheiro da tela.
func TestQAConversaoNaoMudaSaldoTiraDoSummaryEApareceEmTransferencias(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "Nubank")
		b := s.makeAccount(t, ctx, minha.ID, "C6")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Da vizinha")

		dia := civil.MustNew(2026, 8, 25)
		saida := s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{
			Kind: transaction.KindExpense, Description: "Pix enviado - Bruno",
			OccurredOn: dia, AmountCents: 3_000_00,
		})
		entrada := s.makeTransaction(t, ctx, minha.ID, b.ID, txSpec{
			Kind: transaction.KindIncome, Description: "Pix recebido de Bruno",
			OccurredOn: dia, AmountCents: 3_000_00,
		})
		// Ruído que TEM de continuar contando depois da conversão.
		s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{
			Kind: transaction.KindExpense, Description: "Mercado", OccurredOn: dia, AmountCents: 150_00,
		})
		s.makeTransaction(t, ctx, minha.ID, b.ID, txSpec{
			Kind: transaction.KindIncome, Description: "Salário", OccurredOn: dia, AmountCents: 5_000_00,
		})
		// A vizinha, com o par espelhado idêntico: nada dela pode se mexer.
		daVizinha := s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindExpense, Description: "Pix enviado - Bruno",
			OccurredOn: dia, AmountCents: 3_000_00,
		})

		filtro := transaction.SummaryFilter{CompetenceMonth: "2026-08"}
		saldosAntes, err := s.transactions.SumByAccount(ctx, minha.ID)
		require.NoError(t, err)
		resumoAntes, err := s.transactions.Summary(ctx, minha.ID, filtro)
		require.NoError(t, err)
		saldosVizinhaAntes, err := s.transactions.SumByAccount(ctx, alheia.ID)
		require.NoError(t, err)

		require.EqualValues(t, 5_000_00+3_000_00, resumoAntes.IncomeCents)
		require.EqualValues(t, 150_00+3_000_00, resumoAntes.ExpenseCents)

		pernas, err := s.transactions.TransferLegsOfMonth(ctx, minha.ID, "2026-08", 100)
		require.NoError(t, err)
		require.Empty(t, pernas, "antes da conversão não há transferência nenhuma")

		grupo := s.nextID("grp")
		require.NoError(t, s.transactions.ConvertToTransferPair(ctx, minha.ID, transaction.TransferPairConversion{
			OutID: saida.ID, OutAccountID: a.ID,
			InID: entrada.ID, InAccountID: b.ID,
			TransferGroupID: grupo, UpdatedAt: now(),
		}))

		// 1) SALDO: nem um centavo se move. −X em despesa vira −X em saída;
		// +X em receita vira +X em entrada.
		saldosDepois, err := s.transactions.SumByAccount(ctx, minha.ID)
		require.NoError(t, err)
		assert.Equal(t, saldosAntes, saldosDepois, "a conversão mexeu no saldo de alguma conta")
		assert.EqualValues(t, -150_00-3_000_00, saldosDepois[a.ID])
		assert.EqualValues(t, 5_000_00+3_000_00, saldosDepois[b.ID])

		// 2) SUMMARY: o mês perde exatamente a receita e a despesa do par, e
		// o resultado do mês não muda (dinheiro que só mudou de conta).
		resumoDepois, err := s.transactions.Summary(ctx, minha.ID, filtro)
		require.NoError(t, err)
		assert.EqualValues(t, 5_000_00, resumoDepois.IncomeCents, "a receita do par saiu do mês")
		assert.EqualValues(t, 150_00, resumoDepois.ExpenseCents, "a despesa do par saiu do mês")
		assert.Equal(t, resumoAntes.NetCents, resumoDepois.NetCents,
			"transferência nunca entra em resultado: o líquido do mês não pode mudar")
		assert.Equal(t, resumoAntes.Count, resumoDepois.Count, "a lista continua mostrando as 4 linhas")

		// 3) TRANSFERÊNCIAS: o par passa a ser listado, com as duas pernas no
		// mesmo grupo e sem categoria.
		pernas, err = s.transactions.TransferLegsOfMonth(ctx, minha.ID, "2026-08", 100)
		require.NoError(t, err)
		require.Len(t, pernas, 2, "as duas pernas do par novo")
		for _, p := range pernas {
			assert.Equal(t, grupo, p.TransferGroupID)
		}

		// 4) ISOLAMENTO: a linha da vizinha continua despesa, e o saldo dela
		// não se moveu.
		dela, err := s.transactions.ByID(ctx, alheia.ID, daVizinha.ID)
		require.NoError(t, err)
		assert.Equal(t, transaction.KindExpense, dela.Kind)
		assert.Nil(t, dela.TransferGroupID)
		saldosVizinhaDepois, err := s.transactions.SumByAccount(ctx, alheia.ID)
		require.NoError(t, err)
		assert.Equal(t, saldosVizinhaAntes, saldosVizinhaDepois)

		// 5) O par convertido não pode voltar a ser candidato nem espelho.
		candidatas, err := s.transactions.ListTransferCandidates(ctx, minha.ID, "2026-08", 100)
		require.NoError(t, err)
		for _, c := range candidatas {
			assert.NotEqual(t, saida.ID, c.ID, "perna convertida voltou como candidata")
			assert.NotEqual(t, entrada.ID, c.ID)
		}
		janela, err := s.transactions.IncomeExpenseInWindow(ctx, minha.ID, dia, dia, 100)
		require.NoError(t, err)
		for _, l := range janela {
			assert.NotEqual(t, saida.ID, l.ID, "perna convertida voltou ao pool de espelhos")
			assert.NotEqual(t, entrada.ID, l.ID)
		}
	})
}

// A janela de espelhos é lida pela CASA INTEIRA, sem filtro de conta — é o
// serviço que aplica a allowlist de contas ativas. Este teste fecha a outra
// metade da conferência: a consulta em si nunca devolve linha de outra casa,
// nem com descrição, valor e data idênticos.
func TestQAIncomeExpenseInWindowNuncaDevolveLinhaDeOutraCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		minhaConta := s.makeAccount(t, ctx, minha.ID, "Minha")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Dela")

		dia := civil.MustNew(2026, 8, 25)
		minhaLinha := s.makeTransaction(t, ctx, minha.ID, minhaConta.ID, txSpec{
			Kind: transaction.KindExpense, Description: "Pix enviado - Bruno", OccurredOn: dia, AmountCents: 300_00,
		})
		// Espelho PERFEITO na casa da vizinha: mesmo valor, mesmo dia, mesma
		// descrição, sentido oposto. Se algum WHERE esquecer a casa, ele
		// aparece aqui.
		alheiaLinha := s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindIncome, Description: "Pix recebido de Bruno", OccurredOn: dia, AmountCents: 300_00,
		})

		janela, err := s.transactions.IncomeExpenseInWindow(ctx, minha.ID, dia, dia, 1000)
		require.NoError(t, err)
		require.NotEmpty(t, janela)
		for _, l := range janela {
			assert.NotEqual(t, alheiaLinha.ID, l.ID, "linha da vizinha entrou no pool de espelhos")
			assert.Equal(t, minhaConta.ID, l.AccountID, "conta de fora da casa no pool")
		}

		candidatas, err := s.transactions.ListTransferCandidates(ctx, minha.ID, "2026-08", 1000)
		require.NoError(t, err)
		require.Len(t, candidatas, 1)
		assert.Equal(t, minhaLinha.ID, candidatas[0].ID)

		// E o simétrico: a vizinha também não me vê.
		dela, err := s.transactions.IncomeExpenseInWindow(ctx, alheia.ID, dia, dia, 1000)
		require.NoError(t, err)
		for _, l := range dela {
			assert.NotEqual(t, minhaLinha.ID, l.ID)
		}
	})
}

// Fronteira exata da janela no BANCO: a folga de ±DedupWindowDays é aplicada
// pelo repositório, então 3 dias entra e 4 fica de fora — nos dois sentidos,
// inclusive atravessando a virada de mês e de ano.
func TestQAIncomeExpenseInWindowRespeitaAFronteiraExataDeTresDias(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")

		// Faixa das candidatas: só 31/12/2026.
		alvo := civil.MustNew(2026, 12, 31)
		dentroAntes := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			OccurredOn: civil.MustNew(2026, 12, 28), Description: "3 dias antes",
		})
		foraAntes := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			OccurredOn: civil.MustNew(2026, 12, 27), Description: "4 dias antes",
		})
		dentroDepois := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			OccurredOn: civil.MustNew(2027, 1, 3), Description: "3 dias depois, outro ano",
		})
		foraDepois := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			OccurredOn: civil.MustNew(2027, 1, 4), Description: "4 dias depois, outro ano",
		})

		janela, err := s.transactions.IncomeExpenseInWindow(ctx, minha.ID, alvo, alvo, 1000)
		require.NoError(t, err)
		ids := map[string]bool{}
		for _, l := range janela {
			ids[l.ID] = true
		}
		assert.True(t, ids[dentroAntes.ID], "3 dias antes tem de entrar")
		assert.True(t, ids[dentroDepois.ID], "3 dias depois, virando o ano, tem de entrar")
		assert.False(t, ids[foraAntes.ID], "4 dias antes não pode entrar")
		assert.False(t, ids[foraDepois.ID], "4 dias depois não pode entrar")
	})
}

// A conta de cada perna entra no WHERE do UPDATE (defesa em profundidade sobre
// o ADR-028d): mandar a perna certa com a conta ERRADA — inclusive a conta de
// outra casa — não pode converter nada, e não pode deixar meia transferência
// gravada.
func TestQAConvertToTransferPairExigeAContaCertaDeCadaPerna(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "A")
		b := s.makeAccount(t, ctx, minha.ID, "B")
		c := s.makeAccount(t, ctx, minha.ID, "C")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Dela")

		dia := civil.MustNew(2026, 8, 25)
		saida := s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{
			Kind: transaction.KindExpense, OccurredOn: dia, AmountCents: 300_00,
		})
		entrada := s.makeTransaction(t, ctx, minha.ID, b.ID, txSpec{
			Kind: transaction.KindIncome, OccurredOn: dia, AmountCents: 300_00,
		})

		recusas := map[string]transaction.TransferPairConversion{
			"conta da saída trocada por outra da casa": {
				OutID: saida.ID, OutAccountID: c.ID, InID: entrada.ID, InAccountID: b.ID,
			},
			"conta da entrada trocada por outra da casa": {
				OutID: saida.ID, OutAccountID: a.ID, InID: entrada.ID, InAccountID: c.ID,
			},
			"conta de OUTRA casa na saída": {
				OutID: saida.ID, OutAccountID: contaAlheia.ID, InID: entrada.ID, InAccountID: b.ID,
			},
			"contas invertidas entre as pernas": {
				OutID: saida.ID, OutAccountID: b.ID, InID: entrada.ID, InAccountID: a.ID,
			},
			"conta vazia": {
				OutID: saida.ID, OutAccountID: "", InID: entrada.ID, InAccountID: b.ID,
			},
		}

		for nome, p := range recusas {
			t.Run(nome, func(t *testing.T) {
				p.TransferGroupID = s.nextID("grp")
				p.UpdatedAt = now()
				err := s.uow.Do(ctx, func(ctx context.Context) error {
					return s.transactions.ConvertToTransferPair(ctx, minha.ID, p)
				})
				require.Error(t, err, "a conta errada não pode converter")

				// Nenhuma das duas pernas foi tocada.
				vivaSaida, err := s.transactions.ByID(ctx, minha.ID, saida.ID)
				require.NoError(t, err)
				assert.Equal(t, transaction.KindExpense, vivaSaida.Kind)
				assert.Nil(t, vivaSaida.TransferGroupID)
				vivaEntrada, err := s.transactions.ByID(ctx, minha.ID, entrada.ID)
				require.NoError(t, err)
				assert.Equal(t, transaction.KindIncome, vivaEntrada.Kind)
				assert.Nil(t, vivaEntrada.TransferGroupID)
			})
		}

		// Com as contas certas, converte.
		require.NoError(t, s.uow.Do(ctx, func(ctx context.Context) error {
			return s.transactions.ConvertToTransferPair(ctx, minha.ID, transaction.TransferPairConversion{
				OutID: saida.ID, OutAccountID: a.ID, InID: entrada.ID, InAccountID: b.ID,
				TransferGroupID: s.nextID("grp"), UpdatedAt: now(),
			})
		}))
		convertida, err := s.transactions.ByID(ctx, minha.ID, saida.ID)
		require.NoError(t, err)
		assert.Equal(t, transaction.KindTransferOut, convertida.Kind)
	})
}
