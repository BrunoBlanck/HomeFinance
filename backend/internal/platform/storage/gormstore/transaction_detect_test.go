package gormstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Consultas do reprocessamento de transferências (spec 0005 §13, ADR-028)
// contra banco real: o que importa provar é o WHERE — quem entra na lista,
// quem entra na janela e o que o UPDATE condicional recusa.

func TestListTransferCandidatesSoDevolveReceitaEDespesaVivaSemGrupoDoMesDaCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "A")
		b := s.makeAccount(t, ctx, minha.ID, "B")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		despesa := s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{
			Description: "Pix enviado - Bruno", OccurredOn: civil.MustNew(2026, 8, 25), AmountCents: 300_00,
		})
		receita := s.makeTransaction(t, ctx, minha.ID, b.ID, txSpec{
			Kind: transaction.KindIncome, Description: "Pix recebido de Bruno",
			OccurredOn: civil.MustNew(2026, 8, 20), AmountCents: 300_00,
		})
		// Não entram: pernas de transferência (já têm par), excluída, outro
		// mês de competência, outra casa.
		s.par(t, minha.ID, a.ID, b.ID, 100_00, civil.MustNew(2026, 8, 22))
		excluida := s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{
			Description: "Excluída", OccurredOn: civil.MustNew(2026, 8, 23),
		})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{
			Description: "Outro mês", OccurredOn: civil.MustNew(2026, 9, 1),
		})
		// Caixa em agosto, COMPETÊNCIA em setembro (linha de fatura): a
		// candidata é escolhida por competência, então esta fica fora de agosto.
		s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{
			Description: "Compra na fatura", OccurredOn: civil.MustNew(2026, 8, 28), CompetenceMonth: "2026-09",
		})
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Description: "Pix enviado - Bruno", OccurredOn: civil.MustNew(2026, 8, 25), AmountCents: 300_00,
		})

		linhas, err := s.transactions.ListTransferCandidates(ctx, minha.ID, "2026-08", 100)
		require.NoError(t, err)
		require.Len(t, linhas, 2)
		// Ordem constante: (occurred_on, id) ascendente.
		assert.Equal(t, receita.ID, linhas[0].ID)
		assert.Equal(t, transaction.KindIncome, linhas[0].Kind)
		assert.Equal(t, b.ID, linhas[0].AccountID)
		assert.EqualValues(t, 300_00, linhas[0].AmountCents)
		assert.Equal(t, civil.MustNew(2026, 8, 20), linhas[0].OccurredOn)
		assert.Equal(t, despesa.ID, linhas[1].ID)
		assert.Equal(t, "Pix enviado - Bruno", linhas[1].Description)
		assert.Equal(t, "pix enviado - bruno", linhas[1].DescriptionNorm)

		// O limite corta; é como o serviço descobre que passou do teto.
		umaSo, err := s.transactions.ListTransferCandidates(ctx, minha.ID, "2026-08", 1)
		require.NoError(t, err)
		assert.Len(t, umaSo, 1)

		// Casa vizinha: nada meu aparece.
		daVizinha, err := s.transactions.ListTransferCandidates(ctx, alheia.ID, "2026-08", 100)
		require.NoError(t, err)
		require.Len(t, daVizinha, 1)
		assert.Equal(t, contaAlheia.ID, daVizinha[0].AccountID)

		// Mês vazio, casa vazia e limite inválido são erro, nunca varredura.
		_, err = s.transactions.ListTransferCandidates(ctx, minha.ID, "", 100)
		assert.Error(t, err)
		_, err = s.transactions.ListTransferCandidates(ctx, "", "2026-08", 100)
		assert.Error(t, err)
		_, err = s.transactions.ListTransferCandidates(ctx, minha.ID, "2026-08", 0)
		assert.Error(t, err)
	})
}

// A janela de espelhos é por occurred_on, com a folga de ±DedupWindowDays
// aplicada NO REPOSITÓRIO, e enxerga a casa inteira em QUALQUER competência
// (ADR-028c) — é o que permite achar a linha de fatura do mês seguinte.
func TestIncomeExpenseInWindowAbreFolgaDeTresDiasEIgnoraCompetencia(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "A")
		b := s.makeAccount(t, ctx, minha.ID, "B")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		// Competência de SETEMBRO, caixa dentro da janela de agosto: entra.
		outraCompetencia := s.makeTransaction(t, ctx, minha.ID, b.ID, txSpec{
			Kind: transaction.KindIncome, Description: "Pagamento recebido",
			OccurredOn: civil.MustNew(2026, 8, 26), CompetenceMonth: "2026-09",
		})
		// Nas bordas da folga: 22/08 (25 − 3) e 28/08 (25 + 3) entram.
		bordaAntes := s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{OccurredOn: civil.MustNew(2026, 8, 22)})
		bordaDepois := s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{OccurredOn: civil.MustNew(2026, 8, 28)})
		// Fora da folga: 21/08 e 29/08.
		s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{OccurredOn: civil.MustNew(2026, 8, 21)})
		s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{OccurredOn: civil.MustNew(2026, 8, 29)})
		// Excluída, perna de transferência e outra casa nunca entram.
		excluida := s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{OccurredOn: civil.MustNew(2026, 8, 25)})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		s.par(t, minha.ID, a.ID, b.ID, 10_00, civil.MustNew(2026, 8, 25))
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{OccurredOn: civil.MustNew(2026, 8, 25)})

		linhas, err := s.transactions.IncomeExpenseInWindow(ctx, minha.ID, civil.MustNew(2026, 8, 25), civil.MustNew(2026, 8, 25), 100)
		require.NoError(t, err)

		ids := make([]string, 0, len(linhas))
		for _, l := range linhas {
			ids = append(ids, l.ID)
		}
		assert.ElementsMatch(t, []string{bordaAntes.ID, bordaDepois.ID, outraCompetencia.ID}, ids,
			"a janela é por occurred_on, com folga de 3 dias, em qualquer competência")

		// Ordem constante: (occurred_on, id) ascendente.
		require.Len(t, linhas, 3)
		assert.Equal(t, bordaAntes.ID, linhas[0].ID)
		assert.Equal(t, outraCompetencia.ID, linhas[1].ID)
		assert.Equal(t, bordaDepois.ID, linhas[2].ID)

		// O limite corta; é como o serviço descobre que passou do teto.
		umaSo, err := s.transactions.IncomeExpenseInWindow(ctx, minha.ID, civil.MustNew(2026, 8, 25), civil.MustNew(2026, 8, 25), 1)
		require.NoError(t, err)
		assert.Len(t, umaSo, 1)

		// Intervalo invertido, data zero, casa vazia e limite inválido são
		// ERRO — nunca janela vazia silenciosa, que faria toda candidata
		// parecer sem par.
		_, err = s.transactions.IncomeExpenseInWindow(ctx, minha.ID, civil.MustNew(2026, 8, 26), civil.MustNew(2026, 8, 25), 100)
		assert.Error(t, err)
		_, err = s.transactions.IncomeExpenseInWindow(ctx, minha.ID, civil.Date{}, civil.MustNew(2026, 8, 25), 100)
		assert.Error(t, err)
		_, err = s.transactions.IncomeExpenseInWindow(ctx, "", civil.MustNew(2026, 8, 25), civil.MustNew(2026, 8, 25), 100)
		assert.Error(t, err)
		_, err = s.transactions.IncomeExpenseInWindow(ctx, minha.ID, civil.MustNew(2026, 8, 25), civil.MustNew(2026, 8, 25), 0)
		assert.Error(t, err)
	})
}

// O UPDATE condicional do ADR-028(d): duas linhas afetadas por par, e nada
// mais muda. Qualquer divergência é ErrTransferConversionConflict — e a
// primeira perna não fica gravada sozinha.
func TestConvertToTransferPairAfetaExatamenteDuasLinhasEFalhaSemEscrever(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "A")
		b := s.makeAccount(t, ctx, minha.ID, "B")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")
		cat := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		lote := s.nextID("lote")
		externo := s.nextID("ext")

		saida := s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{
			Description: "Pix enviado - Bruno", OccurredOn: civil.MustNew(2026, 8, 25),
			AmountCents: 300_00, CategoryID: &cat.ID, ImportBatchID: &lote, ExternalID: &externo,
		})
		entrada := s.makeTransaction(t, ctx, minha.ID, b.ID, txSpec{
			Kind: transaction.KindIncome, Description: "Pix recebido de Bruno",
			OccurredOn: civil.MustNew(2026, 8, 25), AmountCents: 300_00,
		})

		grupo := s.nextID("grp")
		depois := now().Add(time.Hour)
		require.NoError(t, s.transactions.ConvertToTransferPair(ctx, minha.ID, transaction.TransferPairConversion{
			OutID: saida.ID, OutAccountID: a.ID, InID: entrada.ID, InAccountID: b.ID,
			TransferGroupID: grupo, UpdatedAt: depois,
		}))

		convertidaSaida, err := s.transactions.ByID(ctx, minha.ID, saida.ID)
		require.NoError(t, err)
		convertidaEntrada, err := s.transactions.ByID(ctx, minha.ID, entrada.ID)
		require.NoError(t, err)

		assert.Equal(t, transaction.KindTransferOut, convertidaSaida.Kind)
		assert.Equal(t, transaction.KindTransferIn, convertidaEntrada.Kind)
		require.NotNil(t, convertidaSaida.TransferGroupID)
		require.NotNil(t, convertidaEntrada.TransferGroupID)
		assert.Equal(t, grupo, *convertidaSaida.TransferGroupID)
		assert.Equal(t, grupo, *convertidaEntrada.TransferGroupID)
		assert.Nil(t, convertidaSaida.CategoryID, "transferência não tem categoria")
		assert.Equal(t, depois.UTC(), convertidaSaida.UpdatedAt.UTC())

		// NADA mais muda: a reimportação do mesmo arquivo continua caindo em
		// duplicado_exato, e o saldo das contas não se altera.
		assert.Equal(t, saida.AmountCents, convertidaSaida.AmountCents)
		assert.Equal(t, saida.OccurredOn, convertidaSaida.OccurredOn)
		assert.Equal(t, saida.CompetenceMonth, convertidaSaida.CompetenceMonth)
		assert.Equal(t, saida.Description, convertidaSaida.Description)
		assert.Equal(t, saida.DescriptionNorm, convertidaSaida.DescriptionNorm)
		assert.Equal(t, saida.Source, convertidaSaida.Source)
		assert.Equal(t, saida.DedupKey, convertidaSaida.DedupKey)
		assert.Equal(t, saida.DedupOrdinal, convertidaSaida.DedupOrdinal)
		require.NotNil(t, convertidaSaida.ExternalID)
		assert.Equal(t, externo, *convertidaSaida.ExternalID)
		require.NotNil(t, convertidaSaida.ImportBatchID)
		assert.Equal(t, lote, *convertidaSaida.ImportBatchID)

		// Idempotente: a segunda passada não acha mais income/expense.
		err = s.transactions.ConvertToTransferPair(ctx, minha.ID, transaction.TransferPairConversion{
			OutID: saida.ID, OutAccountID: a.ID, InID: entrada.ID, InAccountID: b.ID,
			TransferGroupID: s.nextID("grp"), UpdatedAt: now(),
		})
		assert.ErrorIs(t, err, transaction.ErrTransferConversionConflict)

		// --- as recusas, cada uma sem deixar a primeira perna gravada ----
		outraSaida := s.makeTransaction(t, ctx, minha.ID, a.ID, txSpec{OccurredOn: civil.MustNew(2026, 8, 26), AmountCents: 50_00})
		outraEntrada := s.makeTransaction(t, ctx, minha.ID, b.ID, txSpec{
			Kind: transaction.KindIncome, OccurredOn: civil.MustNew(2026, 8, 26), AmountCents: 50_00,
		})
		excluida := s.makeTransaction(t, ctx, minha.ID, b.ID, txSpec{
			Kind: transaction.KindIncome, OccurredOn: civil.MustNew(2026, 8, 26), AmountCents: 50_00,
		})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))
		daVizinha := s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindIncome, OccurredOn: civil.MustNew(2026, 8, 26), AmountCents: 50_00,
		})

		recusas := map[string]transaction.TransferPairConversion{
			"entrada de outra casa":  {OutID: outraSaida.ID, OutAccountID: a.ID, InID: daVizinha.ID, InAccountID: contaAlheia.ID},
			"kind errado na saída":   {OutID: outraEntrada.ID, OutAccountID: b.ID, InID: outraEntrada.ID, InAccountID: b.ID},
			"kind errado na entrada": {OutID: outraSaida.ID, OutAccountID: a.ID, InID: outraSaida.ID, InAccountID: a.ID},
			"entrada excluída":       {OutID: outraSaida.ID, OutAccountID: a.ID, InID: excluida.ID, InAccountID: b.ID},
			"entrada já com grupo":   {OutID: outraSaida.ID, OutAccountID: a.ID, InID: entrada.ID, InAccountID: b.ID},
			"entrada inexistente":    {OutID: outraSaida.ID, OutAccountID: a.ID, InID: s.nextID("t"), InAccountID: b.ID},
			// A4: a conta entra no WHERE. Uma perna que MUDOU de conta entre
			// a leitura e o UPDATE não é mais a linha que foi pareada.
			"saída em outra conta da casa":   {OutID: outraSaida.ID, OutAccountID: b.ID, InID: outraEntrada.ID, InAccountID: b.ID},
			"entrada em outra conta da casa": {OutID: outraSaida.ID, OutAccountID: a.ID, InID: outraEntrada.ID, InAccountID: a.ID},
			"as duas pernas na mesma conta":  {OutID: outraSaida.ID, OutAccountID: a.ID, InID: outraEntrada.ID, InAccountID: a.ID},
		}
		for nome, p := range recusas {
			t.Run(nome, func(t *testing.T) {
				p.TransferGroupID = s.nextID("grp")
				p.UpdatedAt = now()

				// DENTRO da transação, como o serviço faz: o erro do segundo
				// UPDATE tem de desfazer também o primeiro. Testar o método
				// solto provaria menos do que a regra (ADR-028d) promete —
				// "nada parcial" é propriedade da transação, e é assim que a
				// execução real chama.
				err := s.uow.Do(ctx, func(ctx context.Context) error {
					return s.transactions.ConvertToTransferPair(ctx, minha.ID, p)
				})
				require.ErrorIs(t, err, transaction.ErrTransferConversionConflict)

				// ZERO escritas: a despesa que serviria de perna de saída
				// continua intacta, sem ter virado meia transferência.
				intacta, err := s.transactions.ByID(ctx, minha.ID, outraSaida.ID)
				require.NoError(t, err)
				assert.Equal(t, transaction.KindExpense, intacta.Kind)
				assert.Nil(t, intacta.TransferGroupID)

				// E a linha da vizinha jamais é tocada.
				dela, err := s.transactions.ByID(ctx, alheia.ID, daVizinha.ID)
				require.NoError(t, err)
				assert.Equal(t, transaction.KindIncome, dela.Kind)
				assert.Nil(t, dela.TransferGroupID)
			})
		}

		// Argumentos incompletos falham antes de qualquer SQL: sem casa, sem
		// grupo e sem a conta de uma das pernas (A4).
		assert.Error(t, s.transactions.ConvertToTransferPair(ctx, "", transaction.TransferPairConversion{
			OutID: outraSaida.ID, OutAccountID: a.ID, InID: outraEntrada.ID, InAccountID: b.ID,
			TransferGroupID: grupo, UpdatedAt: now(),
		}))
		assert.Error(t, s.transactions.ConvertToTransferPair(ctx, minha.ID, transaction.TransferPairConversion{
			OutID: outraSaida.ID, OutAccountID: a.ID, InID: outraEntrada.ID, InAccountID: b.ID, UpdatedAt: now(),
		}))
		assert.Error(t, s.transactions.ConvertToTransferPair(ctx, minha.ID, transaction.TransferPairConversion{
			OutID: outraSaida.ID, InID: outraEntrada.ID, InAccountID: b.ID,
			TransferGroupID: s.nextID("grp"), UpdatedAt: now(),
		}), "sem a conta da perna de saída não há o que reconferir")

		// A mesma linha nos dois lados, e as duas pernas na mesma conta, são
		// conflito ANTES do SQL — meia transferência não existe (ADR-016).
		assert.ErrorIs(t, s.transactions.ConvertToTransferPair(ctx, minha.ID, transaction.TransferPairConversion{
			OutID: outraSaida.ID, OutAccountID: a.ID, InID: outraSaida.ID, InAccountID: a.ID,
			TransferGroupID: s.nextID("grp"), UpdatedAt: now(),
		}), transaction.ErrTransferConversionConflict)
		assert.ErrorIs(t, s.transactions.ConvertToTransferPair(ctx, minha.ID, transaction.TransferPairConversion{
			OutID: outraSaida.ID, OutAccountID: a.ID, InID: outraEntrada.ID, InAccountID: a.ID,
			TransferGroupID: s.nextID("grp"), UpdatedAt: now(),
		}), transaction.ErrTransferConversionConflict)
	})
}

// Decisão do usuário sobre o achado A3: a DESPESA ligada a uma fatura fica
// fora do reprocessamento — nem candidata, nem espelho. Converter uma compra
// do cartão em transfer_out reduziria o total cobrado da fatura, que é o
// número que a pessoa confere contra o banco, e o endpoint promete que nada
// além de kind/grupo/categoria muda. A RECEITA de fatura continua entrando: é
// o pagamento da fatura, e virar transfer_in é o comportamento desejado
// (ADR-016).
func TestCandidatasExcluemDespesaDeFaturaMasMantemAReceita(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		cartao := s.makeAccount(t, ctx, minha.ID, "Cartão")
		fatura := s.makeStatement(t, ctx, minha.ID, cartao.ID, "2026-08")

		// Compra na fatura: FORA, nos dois papéis.
		compra := s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindExpense, Description: "Compra no cartão",
			OccurredOn: civil.MustNew(2026, 8, 10), AmountCents: 300_00, StatementID: &fatura.ID,
		})
		// Pagamento recebido no cartão: DENTRO (vira transfer_in).
		pagamentoRecebido := s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindIncome, Description: "Pagamento recebido",
			OccurredOn: civil.MustNew(2026, 8, 10), AmountCents: 300_00, StatementID: &fatura.ID,
		})
		// Despesa SEM fatura na conta corrente: DENTRO (é o pagamento saindo).
		pagamentoEnviado := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, Description: "Pagamento fatura cartão",
			OccurredOn: civil.MustNew(2026, 8, 10), AmountCents: 300_00,
		})

		candidatas, err := s.transactions.ListTransferCandidates(ctx, minha.ID, "2026-08", 100)
		require.NoError(t, err)
		ids := map[string]bool{}
		for _, c := range candidatas {
			ids[c.ID] = true
		}
		assert.False(t, ids[compra.ID], "despesa de fatura não é candidata")
		assert.True(t, ids[pagamentoRecebido.ID], "receita de fatura continua candidata")
		assert.True(t, ids[pagamentoEnviado.ID], "despesa sem fatura continua candidata")

		// E o mesmo na janela de espelhos: a despesa de fatura também não
		// pode ser ESCOLHIDA como espelho, porque o espelho é convertido.
		janela, err := s.transactions.IncomeExpenseInWindow(ctx, minha.ID,
			civil.MustNew(2026, 8, 10), civil.MustNew(2026, 8, 10), 100)
		require.NoError(t, err)
		ids = map[string]bool{}
		for _, l := range janela {
			ids[l.ID] = true
		}
		assert.False(t, ids[compra.ID], "despesa de fatura não é espelho")
		assert.True(t, ids[pagamentoRecebido.ID])
		assert.True(t, ids[pagamentoEnviado.ID])
	})
}

// O que o reprocessamento NÃO pode mudar na fatura, e o que ele corrige.
//
// A COMPRA do cartão está fora do reprocessamento (decisão do A3), então o
// valor que ela soma em `totalCents` é intocável — é o número que a pessoa
// confere contra a cobrança do banco.
//
// A RECEITA da fatura continua sendo convertida, e aí há um efeito que vale
// registrar: `SumByStatement` lê `income` na fatura como ESTORNO, abatendo o
// total (`TotalCents -= ...`), e lê `transfer_in` como PAGAMENTO
// (`PaidCents += ...`). Converter o "Pagamento recebido" move, portanto, o
// mesmo valor da coluna "total" para a coluna "pago" — que é a leitura certa
// (ADR-016: pagar a fatura é transferência, e a perna de entrada no cartão é
// o que quita) e é justamente a correção que a feature existe para fazer. O
// que não muda, e é o que este teste trava: a DÍVIDA LÍQUIDA da fatura
// (total − pago) e o saldo das duas contas.
func TestSumByStatementNaoMudaComAConversaoDaReceitaDaFatura(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")
		cartao := s.makeAccount(t, ctx, minha.ID, "Cartão")
		fatura := s.makeStatement(t, ctx, minha.ID, cartao.ID, "2026-08")

		compra := s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindExpense, Description: "Compra no cartão",
			OccurredOn: civil.MustNew(2026, 8, 5), AmountCents: 300_00, StatementID: &fatura.ID,
		})
		pagamentoRecebido := s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			Kind: transaction.KindIncome, Description: "Pagamento recebido",
			OccurredOn: civil.MustNew(2026, 8, 10), AmountCents: 300_00, StatementID: &fatura.ID,
		})
		pagamentoEnviado := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, Description: "Pagamento fatura cartão",
			OccurredOn: civil.MustNew(2026, 8, 10), AmountCents: 300_00,
		})

		antes, err := s.transactions.SumByStatement(ctx, minha.ID, []string{fatura.ID})
		require.NoError(t, err)
		saldosAntes, err := s.transactions.SumByAccount(ctx, minha.ID)
		require.NoError(t, err)

		// A conversão que o reprocessamento faria: a despesa da conta vira
		// saída e a receita do cartão vira entrada.
		require.NoError(t, s.transactions.ConvertToTransferPair(ctx, minha.ID, transaction.TransferPairConversion{
			OutID: pagamentoEnviado.ID, OutAccountID: conta.ID,
			InID: pagamentoRecebido.ID, InAccountID: cartao.ID,
			TransferGroupID: s.nextID("grp"), UpdatedAt: now(),
		}))

		depois, err := s.transactions.SumByStatement(ctx, minha.ID, []string{fatura.ID})
		require.NoError(t, err)

		// A dívida líquida da fatura é a mesma antes e depois.
		assert.Equal(t,
			antes[fatura.ID].TotalCents-antes[fatura.ID].PaidCents,
			depois[fatura.ID].TotalCents-depois[fatura.ID].PaidCents,
			"a dívida da fatura não pode mudar com o reprocessamento")
		assert.Equal(t, antes[fatura.ID].LineCount, depois[fatura.ID].LineCount,
			"nenhuma linha entra nem sai da fatura")

		// O que muda é a COMPOSIÇÃO, e para o lado certo: a compra volta a
		// aparecer no total cobrado (300,00) e o pagamento passa a contar
		// como pago, em vez de ser lido como estorno.
		assert.EqualValues(t, 300_00, depois[fatura.ID].TotalCents,
			"a compra de 300,00 é o total cobrado, e ela não foi tocada")
		assert.EqualValues(t, 300_00, depois[fatura.ID].PaidCents,
			"o pagamento recebido passa a quitar a fatura (ADR-016)")

		saldosDepois, err := s.transactions.SumByAccount(ctx, minha.ID)
		require.NoError(t, err)
		assert.Equal(t, saldosAntes, saldosDepois, "nem um centavo se move nas duas contas")

		// E a compra continua intocada: expense, na fatura, sem grupo.
		compraDepois, err := s.transactions.ByID(ctx, minha.ID, compra.ID)
		require.NoError(t, err)
		assert.Equal(t, transaction.KindExpense, compraDepois.Kind)
		assert.Nil(t, compraDepois.TransferGroupID)
		require.NotNil(t, compraDepois.StatementID)
		assert.Equal(t, fatura.ID, *compraDepois.StatementID)
	})
}
