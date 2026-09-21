package gormstore_test

import (
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Testes das consultas que a E2 acrescentou ao repositório: os contadores da
// revisão, a varredura do janitor e as duas projeções de que a deduplicação e a
// idempotência do confirm dependem.
//
// Todos são, antes de tudo, testes de ISOLAMENTO: o assunto é sempre "a casa
// vizinha não aparece e não é afetada".

func TestCountRowsByStatusContaSoOLoteDaCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		contaMinha := s.makeAccount(t, ctx, minha.ID, "Minha Conta")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta Vizinha")

		loteMeu := s.makeBatch(t, ctx, minha.ID, contaMinha.ID, "")
		s.makeRows(t, ctx, loteMeu, 3)

		loteAlheio := s.makeBatch(t, ctx, alheia.ID, contaAlheia.ID, "")
		s.makeRows(t, ctx, loteAlheio, 5)

		// Os dois ids vão juntos na MESMA consulta, que é como o histórico os
		// pede. O lote da vizinha simplesmente não aparece no resultado: o
		// filtro é a coluna household_id da própria linha, nunca um join.
		contagem, err := s.imports.CountRowsByStatus(ctx, minha.ID, []string{loteMeu.ID, loteAlheio.ID})
		require.NoError(t, err)
		assert.Equal(t, map[string]map[string]int{loteMeu.ID: {"importar": 3}}, contagem)

		vazio, err := s.imports.CountRowsByStatus(ctx, minha.ID, nil)
		require.NoError(t, err)
		assert.Empty(t, vazio)
	})
}

func TestVarreduraExpiraApagaLinhasERemoveLotesAntigos(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		contaMinha := s.makeAccount(t, ctx, minha.ID, "Minha Conta")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta Vizinha")

		meu := s.makeBatch(t, ctx, minha.ID, contaMinha.ID, "")
		s.makeRows(t, ctx, meu, 4)

		daVizinha := s.makeBatch(t, ctx, alheia.ID, contaAlheia.ID, "")
		s.makeRows(t, ctx, daVizinha, 2)

		// O helper cria o lote com prazo de 2 h. Vencê-lo é varrer com um
		// "agora" posterior — que é exatamente como o janitor funciona, com o
		// relógio vindo de fora.
		futuro := now().Add(3 * time.Hour)

		// ⚠️ Os contadores desta varredura são GLOBAIS por desenho — ela é
		// manutenção e não recebe casa. Em SQLite cada teste abre um banco só
		// seu, e o número exato vale; em PostgreSQL o banco é COMPARTILHADO
		// pela suíte, e o retorno inclui o que os testes vizinhos deixaram.
		//
		// Por isso a asserção é "pelo menos o que este teste criou" no banco
		// compartilhado e "exatamente" no banco exclusivo. O que NÃO muda entre
		// os dois é a parte que interessa: os lotes DESTE teste terminam no
		// estado certo, e as linhas DELE somem. Exigir o número exato num banco
		// compartilhado produziria falha vermelha sem defeito nenhum, que é o
		// pior tipo de teste — ele ensina a suíte a ser ignorada.
		aoMenos := func(t *testing.T, esperado, obtido int64, msg string) {
			t.Helper()
			if s.backendIsPG {
				assert.GreaterOrEqual(t, obtido, esperado, msg)
				return
			}
			assert.EqualValues(t, esperado, obtido, msg)
		}

		expirados, err := s.imports.ExpireBatches(ctx, futuro)
		require.NoError(t, err)
		aoMenos(t, 2, expirados,
			"a varredura é de manutenção: ela não recebe casa e expira o que estiver vencido")

		apagadas, err := s.imports.DeleteStaleRows(ctx)
		require.NoError(t, err)
		aoMenos(t, 6, apagadas, "as linhas de todo lote terminal somem")

		restantes, err := s.imports.ListRows(ctx, minha.ID, meu.ID, 0, 100)
		require.NoError(t, err)
		assert.Empty(t, restantes)

		// O lote sobrevive como histórico, com o estado certo.
		lote, err := s.imports.BatchByID(ctx, minha.ID, meu.ID)
		require.NoError(t, err)
		assert.Equal(t, importer.BatchStatusExpired, lote.Status)

		// E só some depois da retenção. A prova é sobre ESTE lote — num banco
		// compartilhado, o contador global poderia apagar lotes velhos de
		// outros testes e não diria nada sobre o nosso.
		_, err = s.imports.PurgeTerminalBatches(ctx, now().Add(-time.Hour))
		require.NoError(t, err)
		_, err = s.imports.BatchByID(ctx, minha.ID, meu.ID)
		require.NoError(t, err, "lote recente não é apagado")

		apagados, err := s.imports.PurgeTerminalBatches(ctx, now().Add(time.Hour))
		require.NoError(t, err)
		aoMenos(t, 2, apagados, "passada a retenção, os lotes terminais somem")

		_, err = s.imports.BatchByID(ctx, minha.ID, meu.ID)
		assert.ErrorIs(t, err, importer.ErrBatchNotFound)
	})
}

func TestDeleteStaleRowsNaoTocaNasLinhasDeLotePendente(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Minha Conta")

		pendente := s.makeBatch(t, ctx, minha.ID, conta.ID, "")
		s.makeRows(t, ctx, pendente, 3)

		apagadas, err := s.imports.DeleteStaleRows(ctx)
		require.NoError(t, err)
		assert.Zero(t, apagadas, "lote pendente ainda pode ser confirmado: as linhas dele ficam")

		restantes, err := s.imports.ListRows(ctx, minha.ID, pendente.ID, 0, 100)
		require.NoError(t, err)
		assert.Len(t, restantes, 3)
	})
}

func TestPurgeTerminalBatchesNaoTocaEmLotePendente(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Minha Conta")

		pendente := s.makeBatch(t, ctx, minha.ID, conta.ID, "")

		// Mesmo "muito antigo", um lote PENDENTE não é apagado: ele ainda pode
		// ser confirmado. Quem o torna elegível é ExpireBatches, na passada
		// anterior.
		// A prova é sobre ESTE lote continuar existindo, e não sobre o contador
		// global do expurgo: a varredura é de manutenção e, num PostgreSQL
		// compartilhado pela suíte, ela legitimamente apaga lotes terminais de
		// outros testes. O contador não diria nada sobre o lote pendente daqui.
		_, err := s.imports.PurgeTerminalBatches(ctx, now().Add(365*24*time.Hour))
		require.NoError(t, err)

		_, err = s.imports.BatchByID(ctx, minha.ID, pendente.ID)
		require.NoError(t, err, "lote PENDENTE não é apagado nem depois de um ano: ele ainda pode ser confirmado")
	})
}

func TestExternalIDsInWindowIgnoraAContaDeDestinoEAOutraCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		destino := s.makeAccount(t, ctx, minha.ID, "Conta Destino")
		outraMinha := s.makeAccount(t, ctx, minha.ID, "Outra Conta Minha")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta Vizinha")

		idNaConta := s.nextID("ext")
		idEmOutraConta := s.nextID("ext")
		idDaVizinha := s.nextID("ext")
		emFevereiro := civil.MustNew(2026, 2, 10)

		s.makeTransaction(t, ctx, minha.ID, destino.ID, txSpec{
			OccurredOn: emFevereiro, ExternalID: &idNaConta,
		})
		esperado := s.makeTransaction(t, ctx, minha.ID, outraMinha.ID, txSpec{
			OccurredOn: emFevereiro, ExternalID: &idEmOutraConta,
		})
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			OccurredOn: emFevereiro, ExternalID: &idDaVizinha,
		})

		usos, err := s.transactions.ExternalIDsInWindow(ctx, minha.ID, destino.ID,
			civil.MustNew(2026, 2, 1), civil.MustNew(2026, 2, 28))
		require.NoError(t, err)

		// Só o da OUTRA conta da MESMA casa: é ele que vira a marcação "esta
		// linha já foi importada na conta X".
		require.Len(t, usos, 1)
		assert.Equal(t, transaction.ExternalIDUse{
			TransactionID: esperado.ID,
			AccountID:     outraMinha.ID,
		}, usos[idEmOutraConta])

		_, naConta := usos[idNaConta]
		assert.False(t, naConta, "a conta de destino não entra: quem responde por ela é a chave natural")
		_, daVizinha := usos[idDaVizinha]
		assert.False(t, daVizinha, "a casa vizinha nunca aparece")

		// Fora da janela de datas, nada volta.
		fora, err := s.transactions.ExternalIDsInWindow(ctx, minha.ID, destino.ID,
			civil.MustNew(2026, 6, 1), civil.MustNew(2026, 6, 30))
		require.NoError(t, err)
		assert.Empty(t, fora)

		// Intervalo inválido é ERRO, e não janela vazia silenciosa — uma janela
		// vazia faria a análise concluir "não há nada parecido" justamente onde
		// havia.
		_, err = s.transactions.ExternalIDsInWindow(ctx, minha.ID, destino.ID,
			civil.MustNew(2026, 6, 30), civil.MustNew(2026, 6, 1))
		assert.Error(t, err)
	})
}

func TestExternalIDsInWindowIgnoraLinhaExcluida(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		destino := s.makeAccount(t, ctx, minha.ID, "Conta Destino")
		outra := s.makeAccount(t, ctx, minha.ID, "Outra Conta")

		id := s.nextID("ext")
		linha := s.makeTransaction(t, ctx, minha.ID, outra.ID, txSpec{ExternalID: &id})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, linha.ID, now()))

		usos, err := s.transactions.ExternalIDsInWindow(ctx, minha.ID, destino.ID,
			civil.MustNew(2026, 2, 1), civil.MustNew(2026, 2, 28))
		require.NoError(t, err)
		assert.Empty(t, usos,
			"marcar por causa de um lançamento que a pessoa apagou seria devolver a ela a decisão que já tomou")
	})
}

func TestImportBatchFootprintSoEnxergaOLoteDaCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Minha Conta")
		outra := s.makeAccount(t, ctx, minha.ID, "Minha Outra Conta")

		loteID := s.nextID("ib")
		faturaID := s.nextID("cs")
		grupo := s.nextID("tg")

		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 1_000,
			ImportBatchID: &loteID, StatementID: &faturaID,
		})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindTransferOut, AmountCents: 5_000,
			ImportBatchID: &loteID, TransferGroupID: &grupo,
		})
		s.makeTransaction(t, ctx, minha.ID, outra.ID, txSpec{
			Kind: transaction.KindTransferIn, AmountCents: 5_000,
			ImportBatchID: &loteID, TransferGroupID: &grupo,
		})

		// Desde o schema v4 o rastro e SO a fatura: os pares sao coluna do lote
		// (ADR-026g), porque o `link` tornou a derivacao por import_batch_id
		// mentirosa. As duas pernas acima existem para provar que elas NAO
		// atrapalham a leitura da fatura.
		pegada, err := s.transactions.ImportBatchFootprint(ctx, minha.ID, loteID)
		require.NoError(t, err)
		require.NotNil(t, pegada.StatementID)
		assert.Equal(t, faturaID, *pegada.StatementID)

		// O MESMO id de lote, pedido pela casa vizinha, não devolve nada.
		daVizinha, err := s.transactions.ImportBatchFootprint(ctx, alheia.ID, loteID)
		require.NoError(t, err)
		assert.Nil(t, daVizinha.StatementID)
	})
}
