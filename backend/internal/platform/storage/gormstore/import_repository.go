package gormstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"gorm.io/gorm"
)

// ImportRepository implementa importer.Repository.
type ImportRepository struct{ base }

// NewImportRepository monta o repositório.
func NewImportRepository(db *storage.DB) *ImportRepository {
	return &ImportRepository{base{db: db}}
}

var _ importer.Repository = (*ImportRepository)(nil)

// Tetos das listagens e do IN (...).
//
// idChunkSize existe pelo mesmo motivo de createBatchSize: o SQL Server aceita
// no máximo 2100 parâmetros por comando e o SQLite historicamente 999. Uma
// lista de ids vinda da tela de revisão pode ter centenas de itens, e mandá-la
// inteira num IN (...) funcionaria em Postgres e estouraria nos outros.
const (
	maxBatchPage   = 50
	maxRowPage     = 500
	defaultRowPage = 100
	idChunkSize    = 200
)

// batchScope filtra pela casa. Lotes não têm exclusão lógica: eles são
// rascunho com prazo de validade, não dado financeiro.
func (r *ImportRepository) batchScope(ctx context.Context, householdID string) *gorm.DB {
	return r.conn(ctx).Model(&ImportBatch{}).Where("household_id = ?", householdID)
}

// rowScope filtra pela casa — nunca por join com o lote. A coluna
// household_id existe em import_rows exatamente para isto.
func (r *ImportRepository) rowScope(ctx context.Context, householdID string) *gorm.DB {
	return r.conn(ctx).Model(&ImportRow{}).Where("household_id = ?", householdID)
}

// CreateBatch insere o lote.
func (r *ImportRepository) CreateBatch(ctx context.Context, b *importer.Batch) error {
	if b == nil || b.HouseholdID == "" || b.AccountID == "" {
		return fmt.Errorf("lote de importação exige casa e conta")
	}
	if err := r.conn(ctx).Create(toImportBatchModel(b)).Error; err != nil {
		return fmt.Errorf("criando lote de importação: %w", err)
	}
	return nil
}

// BatchByID devolve o lote da casa; de outra casa é ErrBatchNotFound (S1).
func (r *ImportRepository) BatchByID(ctx context.Context, householdID, id string) (*importer.Batch, error) {
	var m ImportBatch
	err := r.batchScope(ctx, householdID).Where("id = ?", id).Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, importer.ErrBatchNotFound
		}
		return nil, fmt.Errorf("buscando lote de importação: %w", err)
	}
	return toImportBatchEntity(&m), nil
}

// ListBatches devolve os lotes da casa, do mais recente para o mais antigo.
func (r *ImportRepository) ListBatches(ctx context.Context, householdID string, limit int) ([]importer.Batch, error) {
	if limit <= 0 || limit > maxBatchPage {
		limit = maxBatchPage
	}
	var rows []ImportBatch
	err := r.batchScope(ctx, householdID).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listando lotes de importação: %w", err)
	}
	out := make([]importer.Batch, 0, len(rows))
	for i := range rows {
		out = append(out, *toImportBatchEntity(&rows[i]))
	}
	return out, nil
}

// UpdateBatchStatus faz a transição CONDICIONAL de estado.
//
// O estado de origem entra no WHERE junto com a casa e o id, e é isso — e não
// uma checagem no serviço — que torna a confirmação idempotente e segura em
// concorrência. Dois cliques simultâneos em "confirmar" disputam a MESMA
// linha; o banco garante que só um vê RowsAffected = 1, e só esse importa. O
// outro recebe 0, que não é erro: é "alguém já confirmou".
//
// Ler o estado antes e decidir no Go não resolveria: entre a leitura e a
// escrita cabe a outra requisição inteira.
func (r *ImportRepository) UpdateBatchStatus(ctx context.Context, householdID, id, from, to string, at time.Time, outcome *importer.BatchOutcome) (int64, error) {
	if from == "" || to == "" {
		return 0, fmt.Errorf("transição de lote exige estado de origem e destino")
	}

	campos := map[string]any{"status": to, "updated_at": at}
	if to == importer.BatchStatusCommitted {
		// "Confirmado" e "confirmado em" são o mesmo fato: gravá-los em dois
		// comandos abriria uma janela com lote confirmado e sem data.
		campos["committed_at"] = at
	}
	if outcome != nil {
		campos["imported_count"] = outcome.ImportedCount
		campos["skipped_count"] = outcome.SkippedCount
		campos["blocked_count"] = outcome.BlockedCount
		campos["restored_count"] = outcome.RestoredCount
		campos["rejected_count"] = outcome.RejectedCount
		// schema v4 (ADR-026g): no MESMO comando dos demais — "confirmado" e
		// "vinculou N, criou M pares" são o mesmo fato.
		campos["linked_count"] = outcome.LinkedCount
		campos["transfer_pairs_count"] = outcome.TransferPairsCount
	}

	res := r.batchScope(ctx, householdID).
		Where("id = ?", id).
		Where("status = ?", from).
		Updates(campos)
	if res.Error != nil {
		return 0, fmt.Errorf("mudando estado do lote: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// CommittedByContentHash devolve o lote CONFIRMADO mais recente da casa com
// aquele conteúdo, ignorando exceptBatchID.
//
// É como a API avisa "você já importou este arquivo" sem guardar o arquivo: o
// que fica é o hash. Os dois WHERE de estado são a parte que o nome do método
// promete — lote pendente, descartado, expirado, ou confirmado sem gravar nada,
// não pôs lançamento nenhum no lugar, e um aviso apoiado neles diria a uma
// pessoa que ela já importou um arquivo que na verdade nunca entrou.
//
// A casa vem sempre do escopo (BOLA): sem ela, o hash de um arquivo viraria um
// oráculo para descobrir o que a casa vizinha importou.
func (r *ImportRepository) CommittedByContentHash(ctx context.Context, householdID, contentSHA256, exceptBatchID string) (*importer.Batch, error) {
	if householdID == "" || contentSHA256 == "" {
		return nil, importer.ErrBatchNotFound
	}
	q := r.batchScope(ctx, householdID).
		Where("content_sha256 = ?", contentSHA256).
		Where("status = ?", importer.BatchStatusCommitted).
		// Confirmado NÃO quer dizer que gravou: confirmar um lote com todas as
		// linhas em `skip` deixa imported_count = 0. Os contadores entram no
		// MESMO UPDATE da transição (UpdateBatchStatus), então perguntar por
		// eles não custa consulta nem índice novo. Soma em vez de OR para a
		// cláusula não depender de como o GORM parentiza condição composta.
		Where("imported_count + restored_count > 0")
	if exceptBatchID != "" {
		q = q.Where("id <> ?", exceptBatchID)
	}

	// A ordem é LITERAL e a mesma de ListBatches. Por created_at, e não por
	// committed_at, porque committed_at é anulável e cada dialeto ordena NULL
	// para um lado — o desempate por id mantém o resultado estável nos quatro.
	var m ImportBatch
	err := q.Order("created_at DESC, id DESC").Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, importer.ErrBatchNotFound
		}
		return nil, fmt.Errorf("buscando lote por conteúdo: %w", err)
	}
	return toImportBatchEntity(&m), nil
}

// ExpireBatches marca como expirado todo lote PENDENTE cujo prazo passou.
//
// Único método sem householdID, e a exceção é declarada na interface: é
// varredura de manutenção, não devolve dado de ninguém e só toca em lotes já
// vencidos. O WHERE em status = pending impede que ele reabra ou reescreva um
// lote confirmado.
func (r *ImportRepository) ExpireBatches(ctx context.Context, now time.Time) (int64, error) {
	res := r.conn(ctx).
		Model(&ImportBatch{}).
		Where("status = ?", importer.BatchStatusPending).
		Where("expires_at < ?", now).
		Updates(map[string]any{"status": importer.BatchStatusExpired, "updated_at": now})
	if res.Error != nil {
		return 0, fmt.Errorf("expirando lotes de importação: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// CreateRows insere as linhas do lote.
//
// A guarda de casa é a mesma do lote de lançamentos: linha de outra casa
// derruba a escrita inteira em vez de ser gravada onde não devia.
func (r *ImportRepository) CreateRows(ctx context.Context, householdID string, rows []importer.Row) error {
	if len(rows) == 0 {
		return nil
	}
	if householdID == "" {
		return fmt.Errorf("linhas de importação exigem casa")
	}

	models := make([]ImportRow, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		if row.HouseholdID != householdID {
			return fmt.Errorf("linha de importação de outra casa no lote")
		}
		if row.BatchID == "" || row.Seq <= 0 {
			return fmt.Errorf("linha de importação exige lote e sequência")
		}
		models = append(models, *toImportRowModel(row))
	}

	if err := r.conn(ctx).CreateInBatches(&models, createBatchSize).Error; err != nil {
		if storage.IsDuplicate(err) {
			// (batch_id, seq) é único: a mesma linha do arquivo já está
			// gravada. É reenvio, não defeito.
			return fmt.Errorf("gravando linhas do lote: %w", importer.ErrDuplicateRow)
		}
		return fmt.Errorf("gravando linhas do lote: %w", err)
	}
	return nil
}

// ListRows devolve as linhas do lote em ordem de seq, paginadas por cursor.
//
// O cursor é o próprio seq, que é único dentro do lote e sempre crescente —
// não precisa de desempate nem de OFFSET (armadilha P7).
func (r *ImportRepository) ListRows(ctx context.Context, householdID, batchID string, afterSeq, limit int) ([]importer.Row, error) {
	if limit <= 0 {
		limit = defaultRowPage
	}
	if limit > maxRowPage {
		limit = maxRowPage
	}

	q := r.rowScope(ctx, householdID).Where("batch_id = ?", batchID)
	if afterSeq > 0 {
		q = q.Where("seq > ?", afterSeq)
	}

	var rows []ImportRow
	if err := q.Order("seq ASC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("listando linhas do lote: %w", err)
	}
	return importRowsToEntities(rows), nil
}

// RowsByIDs devolve as linhas escolhidas na revisão.
//
// A lista é quebrada em pedaços porque o IN (...) vira um parâmetro por id, e
// o SQL Server para em 2100 por comando (o SQLite, historicamente, em 999).
// Uma seleção de "marcar todas" numa fatura grande passaria desse teto.
func (r *ImportRepository) RowsByIDs(ctx context.Context, householdID string, ids []string) ([]importer.Row, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	out := make([]importer.Row, 0, len(ids))
	for inicio := 0; inicio < len(ids); inicio += idChunkSize {
		fim := min(inicio+idChunkSize, len(ids))

		var rows []ImportRow
		err := r.rowScope(ctx, householdID).
			Where("id IN ?", ids[inicio:fim]).
			Order("seq ASC").
			Find(&rows).Error
		if err != nil {
			return nil, fmt.Errorf("buscando linhas do lote: %w", err)
		}
		out = append(out, importRowsToEntities(rows)...)
	}
	return out, nil
}

// DeleteRows apaga as linhas do lote. A exclusão é FÍSICA: linha de prévia é
// rascunho com prazo, não dado financeiro — guardá-la depois da confirmação
// só manteria descrição de terceiros viva sem ninguém para lê-la.
func (r *ImportRepository) DeleteRows(ctx context.Context, householdID, batchID string) (int64, error) {
	if batchID == "" {
		return 0, fmt.Errorf("apagar linhas exige o lote")
	}
	res := r.conn(ctx).
		Where("household_id = ?", householdID).
		Where("batch_id = ?", batchID).
		Delete(&ImportRow{})
	if res.Error != nil {
		return 0, fmt.Errorf("apagando linhas do lote: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// rowStatusCount é a projeção de CountRowsByStatus.
type rowStatusCount struct {
	BatchID string
	Status  string
	Cnt     int
}

// CountRowsByStatus conta as linhas por status, para vários lotes de uma vez.
//
// Uma agregação, e não a paginação dos lotes: com 10.000 linhas a tela
// precisaria de 100 páginas só para escrever "12 novas, 3 possíveis
// duplicatas". A lista de ids é fatiada pelo mesmo motivo de RowsByIDs — o teto
// de PARÂMETROS por comando é 2100 no SQL Server e 999 no SQLite. Usa
// ix_import_rows_batch (household_id, batch_id, seq).
func (r *ImportRepository) CountRowsByStatus(ctx context.Context, householdID string, batchIDs []string) (map[string]map[string]int, error) {
	out := make(map[string]map[string]int, len(batchIDs))

	unicos := make([]string, 0, len(batchIDs))
	vistos := make(map[string]struct{}, len(batchIDs))
	for _, id := range batchIDs {
		if id == "" {
			continue
		}
		if _, ok := vistos[id]; ok {
			continue
		}
		vistos[id] = struct{}{}
		unicos = append(unicos, id)
	}
	if len(unicos) == 0 {
		return out, nil
	}

	for inicio := 0; inicio < len(unicos); inicio += idChunkSize {
		fim := min(inicio+idChunkSize, len(unicos))

		var rows []rowStatusCount
		err := r.rowScope(ctx, householdID).
			Where("batch_id IN ?", unicos[inicio:fim]).
			Select("batch_id AS batch_id, status AS status, COUNT(*) AS cnt").
			Group("batch_id").
			Group("status").
			Scan(&rows).Error
		if err != nil {
			return nil, fmt.Errorf("contando linhas do lote por status: %w", err)
		}
		for i := range rows {
			porStatus, ok := out[rows[i].BatchID]
			if !ok {
				porStatus = map[string]int{}
				out[rows[i].BatchID] = porStatus
			}
			porStatus[rows[i].Status] += rows[i].Cnt
		}
	}
	return out, nil
}

// DeleteStaleRows apaga as linhas de staging de lotes que não estão mais
// pendentes.
//
// A condição vai numa SUBCONSULTA sobre import_batches — tabela DIFERENTE da
// que está sendo apagada, o que mantém o comando válido também no MySQL, que
// recusa subconsulta sobre a própria tabela do DELETE. Sem LIMIT de propósito:
// `DELETE ... LIMIT` existe no MySQL e não existe no PostgreSQL nem no SQL
// Server, e um comando que só roda em um dialeto é o que o ADR-008 existe para
// evitar.
func (r *ImportRepository) DeleteStaleRows(ctx context.Context) (int64, error) {
	lotesTerminais := r.conn(ctx).
		Model(&ImportBatch{}).
		Select("id").
		Where("status <> ?", importer.BatchStatusPending)

	res := r.conn(ctx).
		Where("batch_id IN (?)", lotesTerminais).
		Delete(&ImportRow{})
	if res.Error != nil {
		return 0, fmt.Errorf("apagando linhas de lotes terminais: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// PurgeTerminalBatches apaga os lotes terminais criados antes de `before`.
//
// O WHERE em status <> pending é a guarda que impede a varredura de comer um
// lote que alguém ainda pode confirmar — um lote pendente antigo é expirado
// primeiro (ExpireBatches) e só na passada seguinte vira candidato.
func (r *ImportRepository) PurgeTerminalBatches(ctx context.Context, before time.Time) (int64, error) {
	res := r.conn(ctx).
		Where("status <> ?", importer.BatchStatusPending).
		Where("created_at < ?", before).
		Delete(&ImportBatch{})
	if res.Error != nil {
		return 0, fmt.Errorf("apagando lotes de importação antigos: %w", res.Error)
	}
	return res.RowsAffected, nil
}

func importRowsToEntities(rows []ImportRow) []importer.Row {
	out := make([]importer.Row, 0, len(rows))
	for i := range rows {
		out = append(out, *toImportRowEntity(&rows[i]))
	}
	return out
}
