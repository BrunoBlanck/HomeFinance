package gormstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeBatch grava um lote pendente pronto.
func (s *store) makeBatch(t *testing.T, ctx context.Context, householdID, accountID, hash string) *importer.Batch {
	t.Helper()

	if hash == "" {
		hash = s.nextID("sha")
	}
	b := &importer.Batch{
		ID:            s.nextID("ib"),
		HouseholdID:   householdID,
		AccountID:     accountID,
		CreatedBy:     s.nextID("u"),
		Institution:   "nubank",
		DocKind:       "card_statement",
		FormatID:      "nubank_card_statement_v1",
		FileName:      "fatura-fevereiro.csv",
		ContentSHA256: hash,
		RowCount:      3,
		MinDate:       civil.MustNew(2026, 1, 5),
		MaxDate:       civil.MustNew(2026, 1, 30),
		Status:        importer.BatchStatusPending,
		ExpiresAt:     now().Add(2 * time.Hour),
		CreatedAt:     now(),
		UpdatedAt:     now(),
	}
	require.NoError(t, s.imports.CreateBatch(ctx, b))
	return b
}

// makeRows grava n linhas sequenciais no lote.
func (s *store) makeRows(t *testing.T, ctx context.Context, b *importer.Batch, n int) []importer.Row {
	t.Helper()

	rows := make([]importer.Row, 0, n)
	for i := 1; i <= n; i++ {
		rows = append(rows, importer.Row{
			ID: s.nextID("ir"), HouseholdID: b.HouseholdID, BatchID: b.ID,
			Seq: i, LineNo: i + 4, Kind: "expense",
			OccurredOn: civil.MustNew(2026, 1, i), AmountCents: int64(i) * 1_000,
			Description: "Compra", DescriptionNorm: "compra",
			DedupKey: s.nextID("dk"), Status: "importar",
			CreatedAt: now(),
		})
	}
	require.NoError(t, s.imports.CreateRows(ctx, b.HouseholdID, rows))
	return rows
}

// Isolamento por casa no lote e nas linhas. A coluna household_id existe em
// import_rows justamente para que o filtro NÃO dependa de um join com o lote —
// e este teste prova que ela é usada.
func TestLoteDeImportacaoDeOutraCasaNaoApareceEmNenhumaConsulta(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		minhaConta := s.makeAccount(t, ctx, minha.ID, "Cartão")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Cartão da Vizinha")

		loteAlheio := s.makeBatch(t, ctx, alheia.ID, contaAlheia.ID, "hash-da-vizinha")
		linhasAlheias := s.makeRows(t, ctx, loteAlheio, 3)
		meuLote := s.makeBatch(t, ctx, minha.ID, minhaConta.ID, "")
		s.makeRows(t, ctx, meuLote, 2)

		_, err := s.imports.BatchByID(ctx, minha.ID, loteAlheio.ID)
		require.ErrorIs(t, err, importer.ErrBatchNotFound)

		lotes, err := s.imports.ListBatches(ctx, minha.ID, 0)
		require.NoError(t, err)
		require.Len(t, lotes, 1)
		assert.Equal(t, meuLote.ID, lotes[0].ID)

		// Linhas do lote alheio: nem pelo id do lote, nem pelos ids das linhas.
		linhas, err := s.imports.ListRows(ctx, minha.ID, loteAlheio.ID, 0, 0)
		require.NoError(t, err)
		assert.Empty(t, linhas, "o filtro de casa não pode depender de join com o lote")

		ids := []string{linhasAlheias[0].ID, linhasAlheias[1].ID}
		porID, err := s.imports.RowsByIDs(ctx, minha.ID, ids)
		require.NoError(t, err)
		assert.Empty(t, porID)

		// Apagar as linhas da vizinha usando a MINHA casa não apaga nada.
		apagadas, err := s.imports.DeleteRows(ctx, minha.ID, loteAlheio.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 0, apagadas)
		sobraram, err := s.imports.ListRows(ctx, alheia.ID, loteAlheio.ID, 0, 0)
		require.NoError(t, err)
		assert.Len(t, sobraram, 3, "as linhas da vizinha continuam lá")

		// Mudar o estado do lote da vizinha pela minha casa não muda nada.
		mudadas, err := s.imports.UpdateBatchStatus(ctx, minha.ID, loteAlheio.ID,
			importer.BatchStatusPending, importer.BatchStatusDiscarded, now(), nil)
		require.NoError(t, err)
		assert.EqualValues(t, 0, mudadas)

		// E o hash do arquivo dela não é encontrável pela minha casa — se
		// fosse, eu descobriria quais arquivos a vizinha importou.
		//
		// O lote dela é CONFIRMADO e COM LINHAS GRAVADAS antes da pergunta, de
		// propósito: pendente (ou confirmado sem gravar nada), a consulta
		// responderia "não achei" pelo filtro de estado, e o teste passaria sem
		// nunca exercitar o filtro de casa.
		confirmadas, err := s.imports.UpdateBatchStatus(ctx, alheia.ID, loteAlheio.ID,
			importer.BatchStatusPending, importer.BatchStatusCommitted, now(), importou(3))
		require.NoError(t, err)
		require.EqualValues(t, 1, confirmadas)

		_, err = s.imports.CommittedByContentHash(ctx, minha.ID, "hash-da-vizinha", "")
		assert.ErrorIs(t, err, importer.ErrBatchNotFound)

		// O controle: pela casa DELA o mesmo hash responde. É o que separa
		// "isolamento funciona" de "a consulta está quebrada".
		dela, err := s.imports.CommittedByContentHash(ctx, alheia.ID, "hash-da-vizinha", "")
		require.NoError(t, err)
		assert.Equal(t, loteAlheio.ID, dela.ID)
	})
}

// A transição condicional é o que torna "confirmar importação" idempotente.
// Sem ela, dois cliques (ou um clique e um retry do navegador) importariam o
// arquivo duas vezes — e o usuário veria o mês dobrado.
func TestUpdateBatchStatusSoSaiDoEstadoDeOrigem(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Cartão")
		lote := s.makeBatch(t, ctx, minha.ID, conta.ID, "")

		confirmadoEm := now().Add(time.Minute)
		resultado := &importer.BatchOutcome{ImportedCount: 2, SkippedCount: 1, BlockedCount: 0, RestoredCount: 1, RejectedCount: 3}

		mudadas, err := s.imports.UpdateBatchStatus(ctx, minha.ID, lote.ID,
			importer.BatchStatusPending, importer.BatchStatusCommitted, confirmadoEm, resultado)
		require.NoError(t, err)
		assert.EqualValues(t, 1, mudadas, "a primeira confirmação é a que vale")

		// A SEGUNDA confirmação não encontra mais nada pendente: zero linhas
		// afetadas, e nenhum lançamento novo entra por causa dela.
		mudadas, err = s.imports.UpdateBatchStatus(ctx, minha.ID, lote.ID,
			importer.BatchStatusPending, importer.BatchStatusCommitted, now().Add(time.Hour), resultado)
		require.NoError(t, err)
		assert.EqualValues(t, 0, mudadas, "confirmar duas vezes não pode importar duas vezes")

		depois, err := s.imports.BatchByID(ctx, minha.ID, lote.ID)
		require.NoError(t, err)
		assert.Equal(t, importer.BatchStatusCommitted, depois.Status)
		require.NotNil(t, depois.CommittedAt, "confirmado e confirmado-em são o mesmo fato")
		assert.EqualValues(t, 2, depois.ImportedCount)
		assert.EqualValues(t, 1, depois.SkippedCount)
		assert.EqualValues(t, 1, depois.RestoredCount)
		assert.EqualValues(t, 3, depois.RejectedCount)
		assert.EqualValues(t, 3, depois.RowCount, "o total de linhas do arquivo não é reescrito pela confirmação")
	})
}

// (batch_id, seq) é único: reenviar a mesma prévia não pode duplicar as linhas.
// O erro precisa ser reconhecível, porque a resposta certa é "já registrei
// isso", nunca 500.
func TestCreateRowsRecusaSequenciaRepetidaComErroTipado(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Cartão")
		lote := s.makeBatch(t, ctx, minha.ID, conta.ID, "")
		s.makeRows(t, ctx, lote, 2)

		repetida := importer.Row{
			ID: s.nextID("ir"), HouseholdID: minha.ID, BatchID: lote.ID,
			Seq: 1, LineNo: 99, Kind: "expense",
			OccurredOn: civil.MustNew(2026, 1, 9), AmountCents: 1_00,
			Description: "Outra", DescriptionNorm: "outra",
			DedupKey: s.nextID("dk"), Status: "importar", CreatedAt: now(),
		}
		err := s.imports.CreateRows(ctx, minha.ID, []importer.Row{repetida})
		assert.ErrorIs(t, err, importer.ErrDuplicateRow)

		// Linha de outra casa dentro do lote derruba a escrita inteira.
		_, alheia := s.duasCasas(t, ctx)
		invasora := repetida
		invasora.ID = s.nextID("ir")
		invasora.Seq = 3
		invasora.HouseholdID = alheia.ID
		assert.Error(t, s.imports.CreateRows(ctx, minha.ID, []importer.Row{invasora}))

		restantes, err := s.imports.ListRows(ctx, minha.ID, lote.ID, 0, 0)
		require.NoError(t, err)
		assert.Len(t, restantes, 2)
	})
}

// Cursor por seq, RowsByIDs e DeleteRows: o caminho que a tela de revisão
// percorre. O cursor é o próprio seq, único dentro do lote e sempre crescente,
// então não precisa de desempate nem de OFFSET (P7).
func TestLinhasDoLotePaginamPorSeqESaoApagadasPorLote(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Cartão")
		lote := s.makeBatch(t, ctx, minha.ID, conta.ID, "")
		outroLote := s.makeBatch(t, ctx, minha.ID, conta.ID, "")
		linhas := s.makeRows(t, ctx, lote, 5)
		s.makeRows(t, ctx, outroLote, 2)

		primeira, err := s.imports.ListRows(ctx, minha.ID, lote.ID, 0, 2)
		require.NoError(t, err)
		require.Len(t, primeira, 2)
		assert.Equal(t, 1, primeira[0].Seq)
		assert.Equal(t, 2, primeira[1].Seq)

		segunda, err := s.imports.ListRows(ctx, minha.ID, lote.ID, primeira[1].Seq, 2)
		require.NoError(t, err)
		require.Len(t, segunda, 2)
		assert.Equal(t, 3, segunda[0].Seq)

		// Seleção da revisão: só as linhas pedidas, e só as da minha casa.
		escolhidas, err := s.imports.RowsByIDs(ctx, minha.ID, []string{linhas[0].ID, linhas[4].ID, "id-que-nao-existe"})
		require.NoError(t, err)
		require.Len(t, escolhidas, 2)
		assert.Equal(t, 1, escolhidas[0].Seq)
		assert.Equal(t, 5, escolhidas[1].Seq)

		// Descartar o lote apaga as linhas DELE, e só as dele.
		apagadas, err := s.imports.DeleteRows(ctx, minha.ID, lote.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 5, apagadas)

		sobraram, err := s.imports.ListRows(ctx, minha.ID, outroLote.ID, 0, 0)
		require.NoError(t, err)
		assert.Len(t, sobraram, 2, "o outro lote não pode ser atingido")
	})
}

// Um lote pendente é dado financeiro parado esperando confirmação: ele tem
// prazo. A varredura só pode atingir o que está PENDENTE e vencido — nunca
// reabrir nem reescrever um lote já confirmado.
func TestExpireBatchesSoAtingePendenteVencido(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Cartão")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Cartão da Vizinha")

		vencido := s.makeBatch(t, ctx, minha.ID, conta.ID, "")
		vencido.ExpiresAt = now().Add(-time.Hour)
		require.NoError(t, s.db.Gorm().
			Model(&gormstore.ImportBatch{}).
			Where("id = ?", vencido.ID).
			Update("expires_at", vencido.ExpiresAt).Error)

		noPrazo := s.makeBatch(t, ctx, minha.ID, conta.ID, "")

		confirmado := s.makeBatch(t, ctx, alheia.ID, contaAlheia.ID, "")
		_, err := s.imports.UpdateBatchStatus(ctx, alheia.ID, confirmado.ID,
			importer.BatchStatusPending, importer.BatchStatusCommitted, now(), nil)
		require.NoError(t, err)
		require.NoError(t, s.db.Gorm().
			Model(&gormstore.ImportBatch{}).
			Where("id = ?", confirmado.ID).
			Update("expires_at", now().Add(-time.Hour)).Error)

		expirados, err := s.imports.ExpireBatches(ctx, now())
		require.NoError(t, err)
		assert.EqualValues(t, 1, expirados)

		lido, err := s.imports.BatchByID(ctx, minha.ID, vencido.ID)
		require.NoError(t, err)
		assert.Equal(t, importer.BatchStatusExpired, lido.Status)

		lido, err = s.imports.BatchByID(ctx, minha.ID, noPrazo.ID)
		require.NoError(t, err)
		assert.Equal(t, importer.BatchStatusPending, lido.Status)

		lido, err = s.imports.BatchByID(ctx, alheia.ID, confirmado.ID)
		require.NoError(t, err)
		assert.Equal(t, importer.BatchStatusCommitted, lido.Status, "lote confirmado não expira")
	})
}

// mudarEstadoDoLote leva o lote de PENDENTE ao estado pedido pelo caminho real
// — a transição condicional —, e não por UPDATE cru: um teste que escreve o
// status por fora deixa de provar que a transição existe.
//
// O outcome vai junto porque é assim que o serviço confirma: estado e
// contadores no MESMO comando. Passar nil deixa os contadores zerados, que é
// exatamente o lote "confirmado sem gravar nada".
func (s *store) mudarEstadoDoLote(t *testing.T, ctx context.Context, householdID, batchID, para string, outcome *importer.BatchOutcome) {
	t.Helper()
	n, err := s.imports.UpdateBatchStatus(ctx, householdID, batchID,
		importer.BatchStatusPending, para, now(), outcome)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
}

// importou é o outcome de um lote que de fato gravou linhas.
func importou(n int) *importer.BatchOutcome {
	return &importer.BatchOutcome{ImportedCount: n}
}

// CommittedByContentHash é como a API diz "você já importou este arquivo" sem
// guardar o arquivo: o que fica é o hash do conteúdo.
//
// As restrições do método são testadas aqui porque cada uma, quando falta,
// produz um aviso MENTIROSO: só lote confirmado E QUE GRAVOU conta (rascunho,
// lote jogado fora e confirmação que pulou tudo não puseram lançamento nenhum
// no lugar) e o lote corrente sai do resultado (senão a revisão de um lote
// confirmado avisa sobre si mesma).
func TestCommittedByContentHashSoEnxergaLoteConfirmadoDaCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Cartão")
		outraConta := s.makeAccount(t, ctx, minha.ID, "Conta Corrente")

		const hash = "9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08"

		// Três lotes com o mesmo conteúdo, e nenhum deles gravou linha: um
		// pendente, um descartado e um CONFIRMADO com os contadores zerados —
		// que é o lote de quem confirmou mandando pular tudo. Nenhum dos três
		// pode responder.
		s.makeBatch(t, ctx, minha.ID, conta.ID, hash)
		descartado := s.makeBatch(t, ctx, minha.ID, conta.ID, hash)
		s.mudarEstadoDoLote(t, ctx, minha.ID, descartado.ID, importer.BatchStatusDiscarded, nil)

		pulouTudo := s.makeBatch(t, ctx, minha.ID, conta.ID, hash)
		s.mudarEstadoDoLote(t, ctx, minha.ID, pulouTudo.ID, importer.BatchStatusCommitted, importou(0))

		_, err := s.imports.CommittedByContentHash(ctx, minha.ID, hash, "")
		require.ErrorIs(t, err, importer.ErrBatchNotFound,
			"nenhum dos três importou lançamento nenhum — nem o confirmado que pulou todas as linhas")

		// Agora dois CONFIRMADOS: o mais velho em outra conta da mesma casa —
		// o aviso é da casa, não da conta, porque o mesmo arquivo pode ter
		// sido importado no destino errado e é exatamente isso que a pessoa
		// precisa lembrar.
		antigo := s.makeBatch(t, ctx, minha.ID, outraConta.ID, hash)
		require.NoError(t, s.db.Gorm().
			Model(&gormstore.ImportBatch{}).
			Where("id = ?", antigo.ID).
			Update("created_at", now().Add(-24*time.Hour)).Error)
		s.mudarEstadoDoLote(t, ctx, minha.ID, antigo.ID, importer.BatchStatusCommitted, importou(3))

		recente := s.makeBatch(t, ctx, minha.ID, conta.ID, hash)
		s.mudarEstadoDoLote(t, ctx, minha.ID, recente.ID, importer.BatchStatusCommitted, importou(3))

		achado, err := s.imports.CommittedByContentHash(ctx, minha.ID, hash, "")
		require.NoError(t, err)
		assert.Equal(t, recente.ID, achado.ID, "sem exclusão, responde o mais recente")

		// Excluindo o corrente, responde o anterior — e não os três que não
		// gravaram nada, que continuam de fora.
		achado, err = s.imports.CommittedByContentHash(ctx, minha.ID, hash, recente.ID)
		require.NoError(t, err)
		assert.Equal(t, antigo.ID, achado.ID)

		// Lote confirmado sozinho não avisa sobre SI MESMO.
		const hashSolitario = "0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9"
		sozinho := s.makeBatch(t, ctx, minha.ID, conta.ID, hashSolitario)
		s.mudarEstadoDoLote(t, ctx, minha.ID, sozinho.ID, importer.BatchStatusCommitted, importou(3))

		_, err = s.imports.CommittedByContentHash(ctx, minha.ID, hashSolitario, sozinho.ID)
		assert.ErrorIs(t, err, importer.ErrBatchNotFound)

		// Entradas vazias não viram consulta sem filtro.
		_, err = s.imports.CommittedByContentHash(ctx, minha.ID, "", "")
		assert.ErrorIs(t, err, importer.ErrBatchNotFound)

		_, err = s.imports.CommittedByContentHash(ctx, "", hash, "")
		assert.ErrorIs(t, err, importer.ErrBatchNotFound, "sem casa não existe consulta")
	})
}
