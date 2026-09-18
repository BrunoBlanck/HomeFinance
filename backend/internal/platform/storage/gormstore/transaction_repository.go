package gormstore

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"gorm.io/gorm"
)

// TransactionRepository implementa transaction.Repository.
type TransactionRepository struct{ base }

// NewTransactionRepository monta o repositório.
func NewTransactionRepository(db *storage.DB) *TransactionRepository {
	return &TransactionRepository{base{db: db}}
}

var _ transaction.Repository = (*TransactionRepository)(nil)

// createBatchSize é quantas linhas vão em cada INSERT.
//
// O número é baixo de propósito, e o motivo é portabilidade: o SQL Server
// aceita no máximo 2100 PARÂMETROS por comando, e o SQLite historicamente
// limita em 999 (o default só subiu para 32766 na 3.32). Com ~24 colunas por
// lançamento, 30 linhas dão 720 parâmetros — folgado nos quatro dialetos. Um
// lote maior funcionaria em Postgres e MySQL e explodiria exatamente nos
// outros dois, que é o tipo de bug que só aparece em produção.
const createBatchSize = 30

// scope é a base de toda LEITURA normal: filtra pela casa e esconde o que foi
// excluído logicamente. Sem household_id a consulta vaza dado de outra casa
// (BOLA — docs/SEGURANCA.md §2); sem deleted_at o lançamento excluído
// ressuscita no extrato e no saldo.
func (r *TransactionRepository) scope(ctx context.Context, householdID string) *gorm.DB {
	return r.conn(ctx).
		Model(&Transaction{}).
		Where("household_id = ?", householdID).
		Where("deleted_at IS NULL")
}

// scopeAll filtra SÓ pela casa: enxerga também o excluído logicamente.
//
// Existe para os três casos em que ignorar a linha excluída seria um erro:
// a deduplicação (a linha excluída continua ocupando a chave única do banco),
// o ordinal máximo (idem) e a pergunta "esta conta já foi usada?" — que é
// sobre o passado, e o passado inclui o que foi excluído.
func (r *TransactionRepository) scopeAll(ctx context.Context, householdID string) *gorm.DB {
	return r.conn(ctx).
		Model(&Transaction{}).
		Where("household_id = ?", householdID)
}

// shiftDays soma dias a uma data civil sem passar perto de fuso: a conversão
// é feita em UTC e a hora é descartada na volta.
func shiftDays(d civil.Date, days int) civil.Date {
	if d.IsZero() {
		return d
	}
	t := time.Date(d.Year(), time.Month(d.Month()), d.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, days)
	out, err := civil.New(t.Year(), int(t.Month()), t.Day())
	if err != nil {
		return d
	}
	return out
}

// pageSize aplica o default e o teto. O cliente não define o custo da
// consulta: limite ausente vira o default, e limite absurdo vira o teto.
func pageSize(limit int) int {
	switch {
	case limit <= 0:
		return transaction.DefaultPageSize
	case limit > transaction.MaxPageSize:
		return transaction.MaxPageSize
	default:
		return limit
	}
}

// CreateBatch insere os lançamentos em lote, na transação em curso.
//
// Duas guardas antes de qualquer SQL, as duas de defesa em profundidade:
//
//   - linha de outra casa no lote derruba a escrita INTEIRA. O serviço já
//     deveria ter barrado; se não barrou, é melhor a importação falhar do que
//     gravar dinheiro na casa errada.
//   - competência ou chave de deduplicação vazias derrubam a escrita. As duas
//     colunas são NOT NULL no banco, mas string vazia PASSA no NOT NULL — e
//     competência vazia é um mês inteiro sumindo do relatório sem erro nenhum.
func (r *TransactionRepository) CreateBatch(ctx context.Context, householdID string, txs []transaction.Transaction) error {
	if len(txs) == 0 {
		return nil
	}
	if householdID == "" {
		return transaction.ErrHouseholdMismatch
	}

	models := make([]Transaction, 0, len(txs))
	for i := range txs {
		t := &txs[i]
		if t.HouseholdID != householdID {
			return transaction.ErrHouseholdMismatch
		}
		if t.CompetenceMonth == "" || t.DedupKey == "" || t.DedupOrdinal < 1 {
			return transaction.ErrIncomplete
		}
		models = append(models, *toTransactionModel(t))
	}

	if err := r.conn(ctx).CreateInBatches(&models, createBatchSize).Error; err != nil {
		// gorm.ErrDuplicatedKey é o ÚNICO jeito portátil de reconhecer
		// violação de unicidade: cada dialeto tem o seu código (23505 no
		// Postgres, 1062 no MySQL, 2627/2601 no MSSQL, SQLITE_CONSTRAINT_UNIQUE
		// no SQLite) e comparar mensagem de erro por string quebraria na
		// primeira atualização de driver. Quem liga a tradução é
		// gorm.Config.TranslateError, em storage.Open.
		//
		// O erro do banco NÃO é embrulhado junto: a mensagem nativa ecoa os
		// valores da linha recusada, e isso iria parar no log.
		if storage.IsDuplicate(err) {
			return fmt.Errorf("inserindo lançamentos: %w", transaction.ErrDuplicateDedup)
		}
		return fmt.Errorf("inserindo lançamentos: %w", err)
	}
	return nil
}

// ByID devolve o lançamento da casa. De outra casa, inexistente e excluído são
// o MESMO ErrNotFound — distinguir confirmaria a existência do recurso alheio
// (S1), que é o vazamento que o 404 existe para fechar.
func (r *TransactionRepository) ByID(ctx context.Context, householdID, id string) (*transaction.Transaction, error) {
	var m Transaction
	err := r.scope(ctx, householdID).Where("id = ?", id).Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, transaction.ErrNotFound
		}
		return nil, fmt.Errorf("buscando lançamento: %w", err)
	}
	return toTransactionEntity(&m), nil
}

// List devolve a página, da linha mais recente para a mais antiga.
//
// A ordenação é CONSTANTE no código, nunca vinda do cliente (armadilha P6 e
// S4: concatenar ORDER BY é injeção). O índice
// (household_id, occurred_on, id) é ascendente e serve esta consulta
// descendente sem custo — os quatro dialetos varrem índice de trás para
// frente.
//
// O cursor compara (occurred_on, id) na forma expandida, e não como tupla
// ((a,b) < (c,d)): comparação de tupla existe em Postgres e MySQL, e não no
// SQL Server.
func (r *TransactionRepository) List(ctx context.Context, householdID string, f transaction.ListFilter) ([]transaction.Transaction, error) {
	q := r.scope(ctx, householdID)
	if f.CompetenceMonth != "" {
		q = q.Where("competence_month = ?", f.CompetenceMonth)
	}
	if f.AccountID != "" {
		q = q.Where("account_id = ?", f.AccountID)
	}
	if f.StatementID != "" {
		q = q.Where("statement_id = ?", f.StatementID)
	}
	if f.Cursor != nil {
		on := f.Cursor.OccurredOn.String()
		q = q.Where("occurred_on < ? OR (occurred_on = ? AND id < ?)", on, on, f.Cursor.ID)
	}

	var rows []Transaction
	err := q.Order("occurred_on DESC, id DESC").Limit(pageSize(f.Limit)).Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listando lançamentos: %w", err)
	}

	out := make([]transaction.Transaction, 0, len(rows))
	for i := range rows {
		out = append(out, *toTransactionEntity(&rows[i]))
	}
	return out, nil
}

// summaryRow é a projeção agregada por kind. Fica aqui, e não no domínio,
// porque é formato de consulta e não conceito de negócio.
type summaryRow struct {
	Kind          string
	Cnt           int64
	Total         int64
	Uncategorized int64
	// MarkedTotal é a parte de Total que está marcada como investimento
	// (ADR-029). Vem da MESMA linha agregada, e é zero quando a casa não tem
	// categoria de investimento — aí a coluna nem entra na consulta.
	MarkedTotal int64
}

// Projeção do Summary. As duas são CONSTANTES de compilação: a segunda é a
// primeira mais uma coluna, e nenhum pedaço de nenhuma das duas vem de fora —
// os ids entram por placeholder `?`, nunca no texto.
const (
	summaryProjecao = "kind AS kind, COUNT(*) AS cnt, COALESCE(SUM(amount_cents), 0) AS total, " +
		"SUM(CASE WHEN category_id IS NULL THEN 1 ELSE 0 END) AS uncategorized"

	summaryProjecaoComMarcados = summaryProjecao +
		", COALESCE(SUM(CASE WHEN category_id IN (?) THEN amount_cents ELSE 0 END), 0) AS marked_total"
)

// Summary agrega a MESMA janela da listagem em UMA consulta.
//
// Uma consulta, e não quatro: somar receita, somar despesa, contar e contar
// sem categoria em comandos separados varreria o mesmo índice quatro vezes e,
// pior, permitiria que os números discordassem entre si se uma escrita
// entrasse no meio.
//
// O CASE WHEN e o COALESCE são SQL padrão nos quatro dialetos; a string é
// CONSTANTE, sem nenhum pedaço vindo de fora.
//
// # Os marcados (ADR-029d/e)
//
// Quando a casa tem categoria de investimento, a MESMA consulta traz, por
// kind, quanto daquele total está marcado — e o que sobra é o que a tela
// mostra como receita e despesa. Continua sendo UMA consulta: a subtração
// acontece DENTRO da linha agregada (`total` e `marked_total` são dois
// agregados das MESMAS linhas, e amount_cents é sempre positivo, então
// `marked_total <= total` por construção). Duas consultas com subtração entre
// elas divergiriam sob escrita concorrente, e a diferença apareceria na tela
// como uma despesa NEGATIVA.
//
// A expressão condicional fica na PROJEÇÃO, e não no GROUP BY, por uma
// restrição real da camada: `gorm.DB.Group` recebe `string` e NÃO aceita
// variáveis de bind, então repetir a expressão como chave de agrupamento
// obrigaria a escrever os ids no texto do SQL — que é injeção (docs/SEGURANCA.md
// §3) e é recusado pelo TestSemSQLMontadoNoGormstore. Agrupar por `inv` como
// ALIAS não é alternativa: MSSQL e PostgreSQL não agrupam por alias. A forma
// escolhida entrega os mesmos quatro números por kind, com o mesmo plano de
// hoje (`GROUP BY kind`), e é a única parametrizada.
//
// Com o conjunto VAZIO — o caso de toda casa no dia da entrega — a coluna
// condicional NÃO entra na consulta: o SQL emitido é, byte a byte, o que era
// emitido antes do E7, e `IN ()` não aparece em dialeto nenhum (ADR-029f).
func (r *TransactionRepository) Summary(ctx context.Context, householdID string, f transaction.SummaryFilter) (transaction.Summary, error) {
	marcadas := dedupeStrings(f.InvestmentCategoryIDs)
	if len(marcadas) > maxCategoryFilterIDs {
		return transaction.Summary{}, fmt.Errorf("resumindo lançamentos: %w", transaction.ErrTooManyCategories)
	}

	q := r.scope(ctx, householdID)
	if f.CompetenceMonth != "" {
		q = q.Where("competence_month = ?", f.CompetenceMonth)
	}
	if f.AccountID != "" {
		q = q.Where("account_id = ?", f.AccountID)
	}
	if len(marcadas) == 0 {
		q = q.Select(summaryProjecao)
	} else {
		q = q.Select(summaryProjecaoComMarcados, marcadas)
	}

	var rows []summaryRow
	err := q.
		Group("kind").
		Scan(&rows).Error
	if err != nil {
		return transaction.Summary{}, fmt.Errorf("resumindo lançamentos: %w", err)
	}

	var out transaction.Summary
	for _, row := range rows {
		out.Count += row.Cnt
		switch row.Kind {
		case transaction.KindIncome:
			// O fluxo vem do kind do LANÇAMENTO: receita marcada é RESGATE
			// (dinheiro que entrou), nunca aporte (ADR-029d).
			out.RedeemedCents = row.MarkedTotal
			out.IncomeCents = row.Total - row.MarkedTotal
			out.Uncategorized += row.Uncategorized
		case transaction.KindExpense:
			// Despesa marcada é APORTE: dinheiro que saiu da conta.
			out.InvestedCents = row.MarkedTotal
			out.ExpenseCents = row.Total - row.MarkedTotal
			out.Uncategorized += row.Uncategorized
		}
		// Transferência entra só na contagem: ela aparece na lista, mas não é
		// receita nem despesa (ADR-016), e não tem categoria por desenho —
		// contá-la como "sem categoria" viraria uma pendência que ninguém
		// consegue resolver. E, sem categoria, ela também nunca é marcada.
	}
	out.NetCents = out.IncomeCents - out.ExpenseCents
	return out, nil
}

// accountSumRow é a projeção de SumByAccount.
type accountSumRow struct {
	AccountID string
	Kind      string
	Total     int64
}

// SumByAccount devolve, por conta, a soma COM SINAL dos lançamentos.
//
// O sinal é aplicado em Go, a partir do kind, porque no banco amount_cents é
// sempre positivo (ADR-016/ADR-017). Um CASE WHEN faria o mesmo em SQL, mas
// deixaria a regra de sinal escrita em dois lugares — e um dia só um dos dois
// seria corrigido.
//
// Conta sem lançamento não aparece no mapa: o saldo dela é o saldo de
// abertura, e inventar uma linha zerada aqui obrigaria a consulta a varrer a
// tabela de contas junto.
func (r *TransactionRepository) SumByAccount(ctx context.Context, householdID string) (map[string]int64, error) {
	return r.sumByAccount(r.scope(ctx, householdID))
}

// SumByAccountUntil é SumByAccount restrito a occurred_on <= until — o saldo
// de CAIXA no fim de um mês (GET /transfers, `balances`). A comparação é
// lexicográfica sobre "YYYY-MM-DD", que é cronológica por construção (D3 da
// spec 0003), e usa o prefixo de ix_transactions_occurred.
func (r *TransactionRepository) SumByAccountUntil(ctx context.Context, householdID string, until civil.Date) (map[string]int64, error) {
	// Data zero é ERRO, e não "sem limite": o zero de civil.Date vira string
	// vazia, e `occurred_on <= ''` devolveria conjunto vazio em silêncio — um
	// saldo igual ao de abertura para toda conta, sem nenhum erro para avisar.
	if until.IsZero() {
		return nil, fmt.Errorf("soma por conta até uma data exige a data")
	}
	return r.sumByAccount(r.scope(ctx, householdID).Where("occurred_on <= ?", until.String()))
}

// sumByAccount é o miolo compartilhado de SumByAccount e SumByAccountUntil: a
// agregação e a regra de sinal ficam escritas UMA vez.
func (r *TransactionRepository) sumByAccount(q *gorm.DB) (map[string]int64, error) {
	var rows []accountSumRow
	err := q.
		Select("account_id AS account_id, kind AS kind, COALESCE(SUM(amount_cents), 0) AS total").
		Group("account_id").
		Group("kind").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("somando lançamentos por conta: %w", err)
	}

	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		switch row.Kind {
		case transaction.KindIncome, transaction.KindTransferIn:
			out[row.AccountID] += row.Total
		case transaction.KindExpense, transaction.KindTransferOut:
			out[row.AccountID] -= row.Total
		}
	}
	return out, nil
}

// dedupProjection é a projeção mínima da janela de deduplicação.
type dedupProjection struct {
	ID              string
	OccurredOn      string
	AmountCents     int64
	Kind            string
	DescriptionNorm string
	ExternalID      *string
	DedupKey        string
	DedupOrdinal    int
	DeletedAt       *time.Time
}

// WindowForDedup carrega a janela inteira da conta em UMA consulta.
//
// Por que uma consulta só: um extrato com 400 linhas viraria 400 idas ao
// banco se cada linha perguntasse por si, e o custo cairia exatamente no
// caminho em que a pessoa está esperando a tela de revisão aparecer.
//
// Por que INCLUI o excluído logicamente: a chave única do banco não distingue
// linha excluída. Ignorá-la faria a importação tentar inserir de novo algo que
// o índice vai recusar — e o usuário veria erro em vez da opção de restaurar.
//
// Por que a folga de DedupWindowDays: banco e cartão lançam a mesma compra com
// um ou dois dias de diferença conforme o documento, e sem folga a mesma
// compra entra duas vezes.
func (r *TransactionRepository) WindowForDedup(ctx context.Context, householdID, accountID string, minDate, maxDate civil.Date) ([]transaction.DedupRow, error) {
	// Intervalo inválido é ERRO, e não janela vazia: uma janela vazia
	// silenciosa faria a importação concluir que nada é duplicado e gravar o
	// arquivo inteiro de novo.
	if accountID == "" || minDate.IsZero() || maxDate.IsZero() || maxDate.Before(minDate) {
		return nil, fmt.Errorf("janela de deduplicação exige conta e intervalo de datas válidos")
	}

	from := shiftDays(minDate, -transaction.DedupWindowDays)
	to := shiftDays(maxDate, transaction.DedupWindowDays)

	var rows []dedupProjection
	err := r.scopeAll(ctx, householdID).
		Where("account_id = ?", accountID).
		Where("occurred_on >= ?", from.String()).
		Where("occurred_on <= ?", to.String()).
		Select("id", "occurred_on", "amount_cents", "kind", "description_norm", "external_id", "dedup_key", "dedup_ordinal", "deleted_at").
		Order("occurred_on ASC, id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("carregando janela de deduplicação: %w", err)
	}

	out := make([]transaction.DedupRow, 0, len(rows))
	for i := range rows {
		out = append(out, transaction.DedupRow{
			ID:              rows[i].ID,
			OccurredOn:      civilOrZero(rows[i].OccurredOn),
			AmountCents:     rows[i].AmountCents,
			Kind:            rows[i].Kind,
			DescriptionNorm: rows[i].DescriptionNorm,
			ExternalID:      rows[i].ExternalID,
			DedupKey:        rows[i].DedupKey,
			DedupOrdinal:    rows[i].DedupOrdinal,
			DeletedAt:       rows[i].DeletedAt,
		})
	}
	return out, nil
}

// maxOrdinalRow é a projeção de MaxDedupOrdinal.
type maxOrdinalRow struct {
	MaxOrdinal int
}

// MaxDedupOrdinal devolve o maior ordinal já usado por esta chave na casa.
//
// Conta as linhas EXCLUÍDAS também: elas continuam ocupando a chave única, e
// reaproveitar um ordinal delas faria o INSERT ser recusado pelo banco.
//
// COALESCE porque MAX() de conjunto vazio é NULL nos quatro dialetos, e NULL
// em coluna int quebraria a leitura; o zero significa "nenhum ainda", então o
// primeiro ordinal é 1.
func (r *TransactionRepository) MaxDedupOrdinal(ctx context.Context, householdID, dedupKey string) (int, error) {
	if dedupKey == "" {
		return 0, transaction.ErrIncomplete
	}
	var row maxOrdinalRow
	err := r.scopeAll(ctx, householdID).
		Where("dedup_key = ?", dedupKey).
		Select("COALESCE(MAX(dedup_ordinal), 0) AS max_ordinal").
		Scan(&row).Error
	if err != nil {
		return 0, fmt.Errorf("buscando ordinal de deduplicação: %w", err)
	}
	return row.MaxOrdinal, nil
}

// dedupKeyProjection é a projeção de identidade de RowsByDedupKeys: quatro
// colunas, nenhuma delas conteúdo do lançamento.
type dedupKeyProjection struct {
	ID           string
	DedupKey     string
	DedupOrdinal int
	DeletedAt    *time.Time
}

// RowsByDedupKeys carrega as ocorrências já gravadas das chaves informadas,
// SEM janela de datas.
//
// Por que existir, já havendo WindowForDedup: a chave NATURAL não embute a
// data. Se a data da mesma transação mudar mais do que a folga da janela entre
// dois downloads, a gêmea já gravada fica fora dela e a MESMA transação entra
// de novo com ordinal 2 — que o índice único aceita, porque (casa, chave, 2)
// está livre. Aqui a busca é pela CHAVE, que é a identidade, e identidade não
// tem data.
//
// A projeção é mínima de propósito: id, chave, ordinal e deleted_at. É tudo o
// que a classificação lê, e não trazer valor nem descrição mantém a consulta
// incapaz de revelar conteúdo de lançamento — inclusive no caso improvável de
// uma chave chegar aqui por engano.
//
// household_id entra no WHERE, como em toda consulta deste repositório (BOLA —
// docs/SEGURANCA.md §2). A chave natural já embute a conta, então filtrar por
// account_id não acrescentaria escopo nenhum; o escopo que importa é o da casa.
//
// A lista é fatiada em idChunkSize pelo mesmo motivo de RowsByIDs: o IN (...)
// vira um PARÂMETRO por chave, e o teto por comando é 2100 no SQL Server e 999
// no SQLite. Usa o prefixo do índice único ux_transactions_dedup
// (household_id, dedup_key, dedup_ordinal).
//
// scopeAll, e não scope: a linha excluída logicamente continua ocupando a chave
// única, e é ela que vira restauração em vez de INSERT recusado.
func (r *TransactionRepository) RowsByDedupKeys(ctx context.Context, householdID string, dedupKeys []string) ([]transaction.DedupKeyRow, error) {
	// Casa vazia é ERRO, e não consulta sem filtro: uma consulta sem
	// household_id devolveria lançamento de todo mundo.
	if householdID == "" {
		return nil, fmt.Errorf("busca por chave de deduplicação exige a casa")
	}

	unicas := make([]string, 0, len(dedupKeys))
	vistas := make(map[string]struct{}, len(dedupKeys))
	for _, chave := range dedupKeys {
		if chave == "" {
			continue
		}
		if _, repetida := vistas[chave]; repetida {
			continue
		}
		vistas[chave] = struct{}{}
		unicas = append(unicas, chave)
	}
	if len(unicas) == 0 {
		return nil, nil
	}

	out := make([]transaction.DedupKeyRow, 0, len(unicas))
	for inicio := 0; inicio < len(unicas); inicio += idChunkSize {
		fim := min(inicio+idChunkSize, len(unicas))

		var rows []dedupKeyProjection
		err := r.scopeAll(ctx, householdID).
			Where("dedup_key IN ?", unicas[inicio:fim]).
			Select("id", "dedup_key", "dedup_ordinal", "deleted_at").
			// A ordem fixa é o que torna a classificação REPRODUZÍVEL: sem
			// ela, o resultado passaria a depender da ordem em que o banco
			// devolveu as linhas.
			Order("dedup_key ASC, dedup_ordinal ASC, id ASC").
			Scan(&rows).Error
		if err != nil {
			return nil, fmt.Errorf("buscando ocorrências por chave de deduplicação: %w", err)
		}
		for i := range rows {
			out = append(out, transaction.DedupKeyRow{
				ID:           rows[i].ID,
				DedupKey:     rows[i].DedupKey,
				DedupOrdinal: rows[i].DedupOrdinal,
				DeletedAt:    rows[i].DeletedAt,
			})
		}
	}
	return out, nil
}

// SoftDelete marca a exclusão lógica. Em finanças o histórico importa: apagar
// de verdade destruiria a prova de que o dinheiro se moveu.
func (r *TransactionRepository) SoftDelete(ctx context.Context, householdID, id string, at time.Time) error {
	res := r.scope(ctx, householdID).
		Where("id = ?", id).
		Updates(map[string]any{"deleted_at": at, "updated_at": at})
	if res.Error != nil {
		return fmt.Errorf("excluindo lançamento: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return transaction.ErrNotFound
	}
	return nil
}

// Restore desfaz a exclusão lógica.
//
// O SET tem EXATAMENTE duas colunas: deleted_at e updated_at. É o que garante
// que restaurar um lançamento devolva o lançamento ORIGINAL — valor, data,
// conta, categoria e competência como estavam. Um Updates com struct
// reescreveria o registro inteiro com o que viesse na memória do serviço, e o
// arquivo que pediu a restauração passaria por cima do dado que já existia.
//
// O filtro deleted_at IS NOT NULL não é decoração: sem ele, "restaurar" uma
// linha ativa seria um UPDATE que não muda nada, e o MySQL responde
// RowsAffected = 0 para UPDATE sem mudança real — o mesmo número de "não
// encontrei". Com o filtro, zero significa sempre a mesma coisa nos quatro
// dialetos: não havia o que restaurar.
func (r *TransactionRepository) Restore(ctx context.Context, householdID, id string, at time.Time) error {
	res := r.scopeAll(ctx, householdID).
		Where("id = ?", id).
		Where("deleted_at IS NOT NULL").
		Updates(map[string]any{"deleted_at": nil, "updated_at": at})
	if res.Error != nil {
		return fmt.Errorf("restaurando lançamento: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return transaction.ErrNotFound
	}
	return nil
}

// ExistsByAccount responde ao UsageChecker da conta.
//
// Usa scopeAll: a pergunta que o 422 responde é "esta conta JÁ foi usada?", e
// um lançamento excluído logicamente foi. Considerar só os ativos deixaria
// excluir a conta de quem apagou os lançamentos — e o histórico deles ficaria
// pendurado em uma conta que não existe mais.
func (r *TransactionRepository) ExistsByAccount(ctx context.Context, householdID, accountID string) (bool, error) {
	return r.exists(r.scopeAll(ctx, householdID).Where("account_id = ?", accountID), "conta")
}

// ExistsByCategory responde ao UsageChecker da categoria, pelo mesmo critério.
func (r *TransactionRepository) ExistsByCategory(ctx context.Context, householdID, categoryID string) (bool, error) {
	return r.exists(r.scopeAll(ctx, householdID).Where("category_id = ?", categoryID), "categoria")
}

// exists pergunta ao banco por UMA linha, não pela contagem: COUNT varreria
// todas as linhas que casam só para descobrir que existe pelo menos uma.
func (r *TransactionRepository) exists(q *gorm.DB, alvo string) (bool, error) {
	var ids []string
	if err := q.Limit(1).Pluck("id", &ids).Error; err != nil {
		return false, fmt.Errorf("verificando uso de %s: %w", alvo, err)
	}
	return len(ids) > 0, nil
}

// ByTransferGroup devolve as duas pernas da transferência, INCLUSIVE as
// excluídas logicamente.
//
// scopeAll, e não scope, porque a restauração precisa enxergar a perna
// excluída: ela é justamente a que tem de voltar. Quem chama decide o que
// fazer com DeletedAt — excluir olha as vivas, restaurar olha as excluídas.
//
// A ordem é constante no código (P6) e serve para o resultado ser estável
// entre bancos: transfer_out antes de transfer_in por ordem de kind, e o id
// desempata.
func (r *TransactionRepository) ByTransferGroup(ctx context.Context, householdID, transferGroupID string) ([]transaction.Transaction, error) {
	// Grupo vazio não é "todas as linhas sem grupo": é pergunta sem sentido, e
	// responder com uma varredura da casa inteira seria o pior resultado
	// possível. Devolve vazio.
	if transferGroupID == "" {
		return nil, nil
	}

	var rows []Transaction
	err := r.scopeAll(ctx, householdID).
		Where("transfer_group_id = ?", transferGroupID).
		Order("kind ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("carregando pernas da transferência: %w", err)
	}

	out := make([]transaction.Transaction, 0, len(rows))
	for i := range rows {
		out = append(out, *toTransactionEntity(&rows[i]))
	}
	return out, nil
}

// statementSumRow é a projeção agregada por fatura e por kind.
type statementSumRow struct {
	StatementID string
	Kind        string
	Cnt         int64
	Total       int64
}

// statementIDChunk é quantos ids de fatura vão em cada IN (...).
//
// Mesmo motivo de createBatchSize, e o mesmo número que o resto do pacote usa
// para IN: o teto é de PARÂMETROS POR COMANDO — 2100 no SQL Server e 999 no
// SQLite —, e 200 ids cabem com folga nos quatro dialetos. Na prática a tela
// pede 24 faturas; fatiar é barato e evita descobrir o limite em produção.
const statementIDChunk = 200

// SumByStatement agrega as linhas VIVAS de cada fatura (ADR-023d).
//
// scope (e não scopeAll): linha excluída não cobra nada. Ela continua ocupando
// a chave de deduplicação, mas não entra em total nem em pago — somar o que
// foi excluído faria a fatura pedir dinheiro que ninguém deve.
//
// O sinal é aplicado em Go a partir do kind, como em SumByAccount: no banco
// amount_cents é sempre positivo (ADR-016/ADR-017), e escrever a regra de
// sinal em SQL a deixaria duplicada em dois lugares — um dia só um dos dois
// seria corrigido.
func (r *TransactionRepository) SumByStatement(ctx context.Context, householdID string, statementIDs []string) (map[string]transaction.StatementSum, error) {
	out := make(map[string]transaction.StatementSum, len(statementIDs))
	if len(statementIDs) == 0 {
		return out, nil
	}

	// Ids repetidos na entrada não podem virar soma dobrada: o IN é um
	// conjunto, mas o mapa de saída é montado por linha do resultado, e o
	// cuidado aqui é com quem chama mandar o mesmo id duas vezes.
	unicos := make([]string, 0, len(statementIDs))
	vistos := make(map[string]struct{}, len(statementIDs))
	for _, id := range statementIDs {
		if id == "" {
			continue
		}
		if _, ok := vistos[id]; ok {
			continue
		}
		vistos[id] = struct{}{}
		unicos = append(unicos, id)
	}

	for inicio := 0; inicio < len(unicos); inicio += statementIDChunk {
		fim := min(inicio+statementIDChunk, len(unicos))

		var rows []statementSumRow
		err := r.scope(ctx, householdID).
			Where("statement_id IN ?", unicos[inicio:fim]).
			Select("statement_id AS statement_id, kind AS kind, COUNT(*) AS cnt, COALESCE(SUM(amount_cents), 0) AS total").
			Group("statement_id").
			Group("kind").
			Scan(&rows).Error
		if err != nil {
			return nil, fmt.Errorf("somando linhas da fatura: %w", err)
		}

		for _, row := range rows {
			soma := out[row.StatementID]
			soma.LineCount += row.Cnt
			switch row.Kind {
			case transaction.KindExpense:
				soma.TotalCents += row.Total
			case transaction.KindIncome:
				// Crédito na fatura (estorno lançado como receita — D4 da spec
				// 0004) ABATE o que a fatura cobra.
				soma.TotalCents -= row.Total
			case transaction.KindTransferIn:
				// Pagar a fatura é transferência (ADR-016): a perna de entrada
				// no cartão é o que quita.
				soma.PaidCents += row.Total
			}
			// transfer_out ligado a uma fatura não é produzido por nenhum
			// caminho de escrita do v1 (só a importação preenche statement_id,
			// e ela nunca cria saída no cartão). Ele é ignorado de propósito,
			// em vez de receber uma regra inventada que ninguém conseguiria
			// testar contra dado real.
			out[row.StatementID] = soma
		}
	}
	return out, nil
}

// maxExternalIDWindow é o teto de linhas que ExternalIDsInWindow carrega.
//
// É o mesmo número da janela de deduplicação (dedup.MaxExistingRows), repetido
// aqui como constante local para o repositório não depender do pacote de
// deduplicação. Acima do teto a consulta RECUSA em vez de truncar: uma lista
// truncada em silêncio faria a marcação fraca concluir "não há nada parecido"
// justamente onde havia.
const maxExternalIDWindow = 20_000

// externalIDProjection é a projeção de ExternalIDsInWindow — três colunas, não
// a linha inteira.
type externalIDProjection struct {
	ID         string
	AccountID  string
	ExternalID *string
}

// ExternalIDsInWindow carrega os identificadores de documento já usados em
// OUTRAS contas da casa, dentro da janela de datas do arquivo.
//
// scope (e não scopeAll): só linha VIVA marca. Ver o contrato completo em
// transaction.Repository.
func (r *TransactionRepository) ExternalIDsInWindow(ctx context.Context, householdID, excludeAccountID string, minDate, maxDate civil.Date) (map[string]transaction.ExternalIDUse, error) {
	// Intervalo inválido é ERRO, e não janela vazia — mesma regra de
	// WindowForDedup, e pelo mesmo motivo: a ausência silenciosa de marcação é
	// indistinguível de "conferi e não achei nada".
	if minDate.IsZero() || maxDate.IsZero() || maxDate.Before(minDate) {
		return nil, fmt.Errorf("janela de identificadores exige intervalo de datas válido")
	}

	from := shiftDays(minDate, -transaction.DedupWindowDays)
	to := shiftDays(maxDate, transaction.DedupWindowDays)

	q := r.scope(ctx, householdID).
		Where("occurred_on >= ?", from.String()).
		Where("occurred_on <= ?", to.String()).
		Where("external_id IS NOT NULL").
		Where("external_id <> ?", "")
	if excludeAccountID != "" {
		q = q.Where("account_id <> ?", excludeAccountID)
	}

	var rows []externalIDProjection
	err := q.
		Select("id", "account_id", "external_id").
		Order("occurred_on ASC, id ASC").
		Limit(maxExternalIDWindow + 1).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("carregando identificadores da janela: %w", err)
	}
	if len(rows) > maxExternalIDWindow {
		return nil, fmt.Errorf("janela de identificadores grande demais: mais de %d lançamentos", maxExternalIDWindow)
	}

	out := make(map[string]transaction.ExternalIDUse, len(rows))
	for i := range rows {
		if rows[i].ExternalID == nil || *rows[i].ExternalID == "" {
			continue
		}
		// O primeiro vence: a ordenação por data e id torna a escolha
		// REPRODUZÍVEL, e o link que a tela oferece é sempre o mesmo.
		if _, ocupado := out[*rows[i].ExternalID]; ocupado {
			continue
		}
		out[*rows[i].ExternalID] = transaction.ExternalIDUse{
			TransactionID: rows[i].ID,
			AccountID:     rows[i].AccountID,
		}
	}
	return out, nil
}

// importFootprintRow é a projeção de ImportBatchFootprint: só a fatura.
type importFootprintRow struct {
	StatementID *string
}

// ImportBatchFootprint devolve a fatura a que as linhas do lote ficaram
// ligadas.
//
// scopeAll (e não scope): o rastro do lote continua existindo mesmo depois de
// alguém excluir logicamente uma das linhas importadas. A resposta do confirm
// descreve o que o confirm FEZ, não o que sobreviveu depois.
//
// Desde o schema v4 (ADR-026g) NÃO conta mais os pares de transferência: a
// ação `link` grava o import_batch_id do lote vinculador numa perna que outro
// lote criou, e contar pernas por lote passaria a mentir para os dois. Os
// pares são coluna (import_batches.transfer_pairs_count), gravada no mesmo
// UPDATE condicional dos demais contadores.
func (r *TransactionRepository) ImportBatchFootprint(ctx context.Context, householdID, importBatchID string) (transaction.ImportFootprint, error) {
	var out transaction.ImportFootprint
	if importBatchID == "" {
		return out, nil
	}

	var rows []importFootprintRow
	err := r.scopeAll(ctx, householdID).
		Where("import_batch_id = ?", importBatchID).
		Where("statement_id IS NOT NULL").
		Select("statement_id AS statement_id").
		Group("statement_id").
		Scan(&rows).Error
	if err != nil {
		return out, fmt.Errorf("lendo o rastro do lote de importação: %w", err)
	}

	for i := range rows {
		if rows[i].StatementID != nil && *rows[i].StatementID != "" {
			id := *rows[i].StatementID
			out.StatementID = &id
			break
		}
	}
	return out, nil
}

// --- schema v4 (spec 0005): auto-categorização -------------------------------

// categorizableKinds são os únicos kinds que recebem categoria. Transferência
// não tem categoria por desenho (ADR-016), e a lista é CONSTANTE no código —
// nunca vem de fora.
var categorizableKinds = []string{transaction.KindIncome, transaction.KindExpense}

// uncategorizedProjection é a projeção de ListUncategorized: quatro colunas,
// sem valor e sem conta — a categorização não olha para eles.
type uncategorizedProjection struct {
	ID              string
	Kind            string
	Description     string
	DescriptionNorm string
}

// ListUncategorized devolve as receitas e despesas VIVAS do mês de competência
// ainda sem categoria, em ordem (occurred_on, id), até `limit` linhas.
//
// Usa ix_transactions_competence (household_id, competence_month); a ordem é
// constante no código (P6) e serve para a prévia ser REPRODUZÍVEL — a mesma
// pergunta devolve a mesma lista, na mesma ordem, nos quatro dialetos.
func (r *TransactionRepository) ListUncategorized(ctx context.Context, householdID, competenceMonth string, limit int) ([]transaction.UncategorizedRow, error) {
	// Mês vazio é ERRO, e não "todos os meses": seria a casa inteira numa
	// consulta que a tela pede por mês.
	if competenceMonth == "" {
		return nil, fmt.Errorf("listar lançamentos sem categoria exige o mês")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("listar lançamentos sem categoria exige um limite positivo")
	}

	var rows []uncategorizedProjection
	err := r.scope(ctx, householdID).
		Where("competence_month = ?", competenceMonth).
		Where("category_id IS NULL").
		Where("kind IN ?", categorizableKinds).
		Select("id", "kind", "description", "description_norm").
		Order("occurred_on ASC, id ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listando lançamentos sem categoria: %w", err)
	}

	out := make([]transaction.UncategorizedRow, 0, len(rows))
	for i := range rows {
		out = append(out, transaction.UncategorizedRow{
			ID:              rows[i].ID,
			Kind:            rows[i].Kind,
			Description:     rows[i].Description,
			DescriptionNorm: rows[i].DescriptionNorm,
		})
	}
	return out, nil
}

// SetCategoryWhereNull grava a categoria nos lançamentos informados que AINDA
// estão sem categoria, e devolve as linhas afetadas.
//
// O `category_id IS NULL` no WHERE é a regra inteira da spec 0005 §4.3
// ("nunca sobrescreve categoria já escolhida") escrita no lugar em que não
// pode ser contornada: entre a prévia e a confirmação cabe uma edição manual, e
// é o banco — não a memória do serviço — que decide o que ainda estava vazio.
// O SET tem exatamente duas colunas. household_id, como sempre, só no WHERE.
//
// A lista é fatiada em idChunkSize (teto de parâmetros por comando) e as
// linhas afetadas de cada fatia são somadas — é o `categorized` da resposta.
func (r *TransactionRepository) SetCategoryWhereNull(ctx context.Context, householdID string, ids []string, categoryID string, at time.Time) (int64, error) {
	if householdID == "" || categoryID == "" {
		return 0, fmt.Errorf("categorizar em massa exige casa e categoria")
	}
	unicos := dedupeStrings(ids)
	if len(unicos) == 0 {
		return 0, nil
	}

	var afetadas int64
	for inicio := 0; inicio < len(unicos); inicio += idChunkSize {
		fim := min(inicio+idChunkSize, len(unicos))

		res := r.scope(ctx, householdID).
			Where("id IN ?", unicos[inicio:fim]).
			Where("category_id IS NULL").
			Where("kind IN ?", categorizableKinds).
			Updates(map[string]any{"category_id": categoryID, "updated_at": at})
		if res.Error != nil {
			return afetadas, fmt.Errorf("categorizando lançamentos: %w", res.Error)
		}
		afetadas += res.RowsAffected
	}
	return afetadas, nil
}

// UpdateCategory grava a categoria em UM lançamento (spec 0005 §11).
//
// O SET tem EXATAMENTE duas colunas: category_id e updated_at. Valor, data,
// conta, descrição e competência ficam como estão — categorizar não altera
// movimento. O WHERE reconfere, no banco, tudo que o serviço já conferiu na
// memória: casa (scope), linha viva (scope), o id e `kind IN (income,
// expense)`. Perna de transferência nunca recebe categoria (ADR-016), e a
// guarda aqui é defesa em profundidade — o serviço responde 422 antes.
//
// Zero linhas afetadas é ErrNotFound. O serviço só chama depois de ler a linha
// nesta transação e de descartar "já é esta categoria" (updated_at sempre
// muda quando a chamada chega aqui), então zero nunca é "UPDATE sem mudança"
// — nem no MySQL, que conta só mudança real.
func (r *TransactionRepository) UpdateCategory(ctx context.Context, householdID, id, categoryID string, at time.Time) error {
	if householdID == "" || id == "" || categoryID == "" {
		return fmt.Errorf("categorizar lançamento exige casa, lançamento e categoria")
	}

	res := r.scope(ctx, householdID).
		Where("id = ?", id).
		Where("kind IN ?", categorizableKinds).
		Updates(map[string]any{"category_id": categoryID, "updated_at": at})
	if res.Error != nil {
		return fmt.Errorf("categorizando lançamento: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return transaction.ErrNotFound
	}
	return nil
}

// --- schema v4 (spec 0005): GET /transfers ----------------------------------

// transferKinds são os dois kinds de perna. Constante no código.
var transferKinds = []string{transaction.KindTransferOut, transaction.KindTransferIn}

// ListTransferLegs devolve a página de pernas-âncora do mês (ver a interface).
//
// Sem AccountID, a âncora é a perna de SAÍDA: todo par tem exatamente uma
// (ADR-016), então cada transferência aparece uma vez. Com AccountID, são as
// pernas daquela conta — de saída e de entrada — porque "tudo que toca a
// conta" é a pergunta da tela.
//
// Com CounterpartAccountID, a restrição vai numa SUBCONSULTA parametrizada
// montada pelo GORM (transfer_group_id IN (SELECT transfer_group_id ... WHERE
// account_id = ?)): é SQL ANSI, roda nos quatro dialetos, e nenhum pedaço
// dela é string montada. Subconsulta sobre a mesma tabela num SELECT é aceita
// pelo MySQL — a restrição dele é só em UPDATE/DELETE.
//
// Cursor e ordem são os mesmos de List: (occurred_on DESC, id DESC) servido
// pelo índice ascendente, comparação expandida (tupla não existe no SQL
// Server).
func (r *TransactionRepository) ListTransferLegs(ctx context.Context, householdID string, f transaction.TransferFilter) ([]transaction.Transaction, error) {
	if f.CompetenceMonth == "" {
		return nil, fmt.Errorf("listar transferências exige o mês")
	}
	// Defesa em profundidade: o serviço já respondeu 400. Aqui, contraparte
	// sem conta seria "grupos que tocam B" — uma pergunta diferente da que a
	// interface promete.
	if f.CounterpartAccountID != "" && f.AccountID == "" {
		return nil, fmt.Errorf("filtro por contraparte exige a conta")
	}

	q := r.scope(ctx, householdID).
		Where("competence_month = ?", f.CompetenceMonth)
	if f.AccountID == "" {
		q = q.Where("kind = ?", transaction.KindTransferOut)
	} else {
		q = q.Where("kind IN ?", transferKinds).Where("account_id = ?", f.AccountID)
	}
	if f.CounterpartAccountID != "" {
		gruposDaContraparte := r.scope(ctx, householdID).
			Select("transfer_group_id").
			Where("account_id = ?", f.CounterpartAccountID).
			Where("competence_month = ?", f.CompetenceMonth).
			Where("kind IN ?", transferKinds).
			Where("transfer_group_id IS NOT NULL")
		q = q.Where("transfer_group_id IN (?)", gruposDaContraparte)
	}
	if f.Cursor != nil {
		on := f.Cursor.OccurredOn.String()
		q = q.Where("occurred_on < ? OR (occurred_on = ? AND id < ?)", on, on, f.Cursor.ID)
	}

	var rows []Transaction
	err := q.Order("occurred_on DESC, id DESC").Limit(pageSize(f.Limit)).Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listando transferências: %w", err)
	}

	out := make([]transaction.Transaction, 0, len(rows))
	for i := range rows {
		out = append(out, *toTransactionEntity(&rows[i]))
	}
	return out, nil
}

// ByTransferGroups devolve as pernas VIVAS dos grupos informados, em ordem
// (transfer_group_id, kind, id). É ByTransferGroup em lote e SÓ com as vivas:
// a listagem quer a outra perna de cada âncora, e uma perna excluída não é
// par de ninguém. IN fatiado; usa ix_transactions_group.
func (r *TransactionRepository) ByTransferGroups(ctx context.Context, householdID string, groupIDs []string) ([]transaction.Transaction, error) {
	if householdID == "" {
		return nil, fmt.Errorf("buscar pernas por grupo exige a casa")
	}
	unicos := dedupeStrings(groupIDs)
	if len(unicos) == 0 {
		return nil, nil
	}

	out := make([]transaction.Transaction, 0, 2*len(unicos))
	for inicio := 0; inicio < len(unicos); inicio += idChunkSize {
		fim := min(inicio+idChunkSize, len(unicos))

		var rows []Transaction
		err := r.scope(ctx, householdID).
			Where("transfer_group_id IN ?", unicos[inicio:fim]).
			Order("transfer_group_id ASC, kind ASC, id ASC").
			Find(&rows).Error
		if err != nil {
			return nil, fmt.Errorf("carregando pernas por grupo: %w", err)
		}
		for i := range rows {
			out = append(out, *toTransactionEntity(&rows[i]))
		}
	}
	return out, nil
}

// transferLegProjection é a projeção de TransferLegsOfMonth: quatro colunas.
// transfer_group_id vem como string, e não *string, porque o WHERE já exclui
// o nulo.
type transferLegProjection struct {
	TransferGroupID string
	AccountID       string
	Kind            string
	AmountCents     int64
}

// TransferLegsOfMonth devolve as pernas vivas do mês em quatro colunas, até
// `limit`. A soma por par é feita em Go: GROUP BY sobre (menor id, maior id)
// exigiria LEAST/GREATEST, que não existem no SQL Server — e o teto de
// MaxTransferLegsPerMonth mantém a lista pequena. Usa
// ix_transactions_competence.
func (r *TransactionRepository) TransferLegsOfMonth(ctx context.Context, householdID, competenceMonth string, limit int) ([]transaction.TransferLegSummary, error) {
	if competenceMonth == "" {
		return nil, fmt.Errorf("somar transferências exige o mês")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("somar transferências exige um limite positivo")
	}

	var rows []transferLegProjection
	err := r.scope(ctx, householdID).
		Where("competence_month = ?", competenceMonth).
		Where("kind IN ?", transferKinds).
		Where("transfer_group_id IS NOT NULL").
		Select("transfer_group_id", "account_id", "kind", "amount_cents").
		Order("transfer_group_id ASC, kind ASC, id ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("carregando pernas do mês: %w", err)
	}

	out := make([]transaction.TransferLegSummary, 0, len(rows))
	for i := range rows {
		out = append(out, transaction.TransferLegSummary{
			TransferGroupID: rows[i].TransferGroupID,
			AccountID:       rows[i].AccountID,
			Kind:            rows[i].Kind,
			AmountCents:     rows[i].AmountCents,
		})
	}
	return out, nil
}

// --- schema v4 (spec 0005): ação `link` da importação ------------------------

// linkCandidateProjection é a projeção da primeira consulta de
// TransferLegsForLinking: o que o pareamento compara (kind, data, valor,
// grupo) e o que o `link` vai sobrescrever (external_id, import_batch_id).
type linkCandidateProjection struct {
	ID              string
	Kind            string
	OccurredOn      string
	AmountCents     int64
	TransferGroupID string
	ExternalID      *string
	ImportBatchID   *string
}

// counterpartProjection é a projeção da segunda consulta: grupo -> conta da
// outra perna.
type counterpartProjection struct {
	TransferGroupID string
	AccountID       string
}

// TransferLegsForLinking devolve as pernas de transferência VIVAS da conta na
// janela do arquivo (± DedupWindowDays), com a conta da outra perna resolvida.
//
// Duas consultas, nenhuma por linha: a primeira varre a conta pela janela de
// datas (ix_transactions_account_occurred); a segunda busca, pelos grupos
// encontrados e em fatias, a perna que está em OUTRA conta
// (ix_transactions_group). Perna cujo grupo não tem contraparte viva é
// descartada — não há par para vincular, e devolvê-la faria o pareamento
// apontar para meia transferência.
//
// scope (e não scopeAll) nas duas: `link` escreve numa perna existente, e
// escrever numa perna excluída ressuscitaria movimento que a pessoa apagou.
func (r *TransactionRepository) TransferLegsForLinking(ctx context.Context, householdID, accountID string, minDate, maxDate civil.Date) ([]transaction.TransferLeg, error) {
	// Intervalo inválido é ERRO, e não janela vazia — mesma regra de
	// WindowForDedup: "não achei nenhuma perna" precisa ser uma resposta
	// verdadeira, e não o efeito colateral de uma pergunta malformada.
	if householdID == "" || accountID == "" || minDate.IsZero() || maxDate.IsZero() || maxDate.Before(minDate) {
		return nil, fmt.Errorf("janela de pareamento exige casa, conta e intervalo de datas válidos")
	}

	from := shiftDays(minDate, -transaction.DedupWindowDays)
	to := shiftDays(maxDate, transaction.DedupWindowDays)

	var candidatas []linkCandidateProjection
	err := r.scope(ctx, householdID).
		Where("account_id = ?", accountID).
		Where("kind IN ?", transferKinds).
		Where("transfer_group_id IS NOT NULL").
		Where("occurred_on >= ?", from.String()).
		Where("occurred_on <= ?", to.String()).
		Select("id", "kind", "occurred_on", "amount_cents", "transfer_group_id", "external_id", "import_batch_id").
		Order("occurred_on ASC, id ASC").
		Limit(maxExternalIDWindow + 1).
		Scan(&candidatas).Error
	if err != nil {
		return nil, fmt.Errorf("carregando pernas para vínculo: %w", err)
	}
	// Acima do teto a consulta RECUSA em vez de truncar: lista truncada em
	// silêncio faria o pareamento concluir "não há perna" onde havia.
	if len(candidatas) > maxExternalIDWindow {
		return nil, fmt.Errorf("janela de pareamento grande demais: mais de %d pernas", maxExternalIDWindow)
	}
	if len(candidatas) == 0 {
		return nil, nil
	}

	grupos := make([]string, 0, len(candidatas))
	for i := range candidatas {
		grupos = append(grupos, candidatas[i].TransferGroupID)
	}
	grupos = dedupeStrings(grupos)

	contraparte := make(map[string]string, len(grupos))
	for inicio := 0; inicio < len(grupos); inicio += idChunkSize {
		fim := min(inicio+idChunkSize, len(grupos))

		var outras []counterpartProjection
		err := r.scope(ctx, householdID).
			Where("transfer_group_id IN ?", grupos[inicio:fim]).
			Where("account_id <> ?", accountID).
			Select("transfer_group_id", "account_id").
			Order("transfer_group_id ASC, id ASC").
			Scan(&outras).Error
		if err != nil {
			return nil, fmt.Errorf("carregando contrapartes para vínculo: %w", err)
		}
		for i := range outras {
			// A primeira vence: ADR-016 garante uma contraparte por grupo, e a
			// ordem fixa torna qualquer desvio disso reproduzível.
			if _, ok := contraparte[outras[i].TransferGroupID]; !ok {
				contraparte[outras[i].TransferGroupID] = outras[i].AccountID
			}
		}
	}

	out := make([]transaction.TransferLeg, 0, len(candidatas))
	for i := range candidatas {
		c := &candidatas[i]
		outra, ok := contraparte[c.TransferGroupID]
		if !ok {
			continue
		}
		out = append(out, transaction.TransferLeg{
			ID:                   c.ID,
			Kind:                 c.Kind,
			OccurredOn:           civilOrZero(c.OccurredOn),
			AmountCents:          c.AmountCents,
			TransferGroupID:      c.TransferGroupID,
			CounterpartAccountID: outra,
			ExternalID:           c.ExternalID,
			ImportBatchID:        c.ImportBatchID,
		})
	}
	return out, nil
}

// ByIDIncludingDeleted devolve o lançamento da casa, excluído ou não. Outra
// casa e inexistente continuam sendo o MESMO ErrNotFound (S1); só a linha
// excluída da PRÓPRIA casa passa a ser visível — é o que a reconferência do
// `link` precisa para bloquear a linha em vez de derrubar o lote.
func (r *TransactionRepository) ByIDIncludingDeleted(ctx context.Context, householdID, id string) (*transaction.Transaction, error) {
	if householdID == "" || id == "" {
		return nil, transaction.ErrNotFound
	}
	var m Transaction
	err := r.scopeAll(ctx, householdID).Where("id = ?", id).Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, transaction.ErrNotFound
		}
		return nil, fmt.Errorf("buscando lançamento: %w", err)
	}
	return toTransactionEntity(&m), nil
}

// LinkImport grava na perna existente a identidade de deduplicação e o lote da
// linha do arquivo (ADR-026f).
//
// O SET tem EXATAMENTE cinco colunas: external_id, dedup_key, dedup_ordinal,
// import_batch_id e updated_at. Valor, data, conta, categoria e competência
// da perna são os originais — `link` não cria nem altera movimento, só
// registra "este lançamento também é a linha X do arquivo Y".
//
// O WHERE leva casa, id, a CONTA DO LOTE e deleted_at IS NULL (scope). É a
// reconferência de S1 no banco: perna de outra conta, excluída entre a análise
// e o confirm, ou de outra casa afeta ZERO linhas — e zero é ErrNotFound. O
// import_batch_id muda sempre (é o lote novo), então RowsAffected nunca é 0
// por "nada mudou" — nem no MySQL, que conta só mudança real.
func (r *TransactionRepository) LinkImport(ctx context.Context, householdID, id string, f transaction.LinkFields) error {
	if householdID == "" || id == "" || f.AccountID == "" || f.ImportBatchID == "" {
		return fmt.Errorf("vincular importação exige casa, lançamento, conta e lote")
	}
	if f.DedupKey == "" || f.DedupOrdinal < 1 {
		return transaction.ErrIncomplete
	}

	res := r.scope(ctx, householdID).
		Where("id = ?", id).
		Where("account_id = ?", f.AccountID).
		Updates(map[string]any{
			"external_id":     f.ExternalID,
			"dedup_key":       f.DedupKey,
			"dedup_ordinal":   f.DedupOrdinal,
			"import_batch_id": f.ImportBatchID,
			"updated_at":      f.UpdatedAt,
		})
	if res.Error != nil {
		// O índice único (household_id, dedup_key, dedup_ordinal) arbitra a
		// chave. Erro nativo não embrulhado: a mensagem ecoa os valores.
		if storage.IsDuplicate(res.Error) {
			return transaction.ErrDuplicateDedup
		}
		return fmt.Errorf("vinculando importação ao lançamento: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return transaction.ErrNotFound
	}
	return nil
}

// occurredOnProjection é a projeção de OccurredOnByIDs: id e data, nada mais.
type occurredOnProjection struct {
	ID         string
	OccurredOn string
}

// OccurredOnByIDs devolve id -> occurred_on, inclusive das linhas excluídas
// logicamente (scopeAll): a tela diz "já registrada em dd/mm" e a data continua
// verdadeira mesmo se a perna sumiu entre a análise e a revisão. IN fatiado;
// id de outra casa simplesmente não volta.
func (r *TransactionRepository) OccurredOnByIDs(ctx context.Context, householdID string, ids []string) (map[string]civil.Date, error) {
	if householdID == "" {
		return nil, fmt.Errorf("buscar datas por id exige a casa")
	}
	unicos := dedupeStrings(ids)
	out := make(map[string]civil.Date, len(unicos))
	for inicio := 0; inicio < len(unicos); inicio += idChunkSize {
		fim := min(inicio+idChunkSize, len(unicos))

		var rows []occurredOnProjection
		err := r.scopeAll(ctx, householdID).
			Where("id IN ?", unicos[inicio:fim]).
			Select("id", "occurred_on").
			Scan(&rows).Error
		if err != nil {
			return nil, fmt.Errorf("buscando datas de lançamentos: %w", err)
		}
		for i := range rows {
			out[rows[i].ID] = civilOrZero(rows[i].OccurredOn)
		}
	}
	return out, nil
}

// --- spec 0005 §13 (ADR-028): reprocessar transferências --------------------

// despesaDeFaturaFicaDeFora exclui a DESPESA ligada a uma fatura de cartão do
// reprocessamento de transferências — nem candidata, nem espelho (decisão do
// usuário sobre o achado A3 da revisão de segurança).
//
// O motivo é o total da fatura: `SumByStatement` soma só `income`/`expense`
// vivas, então converter uma despesa de fatura em `transfer_out` REDUZIRIA o
// total cobrado — um número que a pessoa confere contra a cobrança do banco e
// que a promessa do endpoint ("nada mais muda") não pode alterar sozinha.
//
// A RECEITA de fatura continua participando: é o "Pagamento recebido" no
// cartão, e convertê-la em `transfer_in` é exatamente o desejado (ADR-016) —
// ela diminui a dívida do cartão, não o total cobrado.
//
// Escrito como OR sobre `statement_id IS NULL` em vez de NOT(...AND...): a
// forma é a mesma nos quatro dialetos e o placeholder continua sendo o kind,
// nunca texto montado.
const despesaDeFaturaFicaDeFora = "(statement_id IS NULL OR kind <> ?)"

// transferCandidateProjection é a projeção de ListTransferCandidates e de
// IncomeExpenseInWindow: sete colunas — o que o pareamento compara, o que o
// matcher pontua e o que a prévia mostra. Nem categoria nem chave de
// deduplicação: a conversão não as toca.
type transferCandidateProjection struct {
	ID              string
	AccountID       string
	Kind            string
	AmountCents     int64
	OccurredOn      string
	Description     string
	DescriptionNorm string
}

// ListTransferCandidates devolve as receitas e despesas VIVAS do mês de
// competência ainda sem transfer_group_id, em ordem (occurred_on, id), até
// `limit` linhas.
//
// `kind IN (income, expense)` vem da lista CONSTANTE categorizableKinds —
// são os mesmos dois kinds que podem virar perna — e `transfer_group_id IS
// NULL` é defesa em profundidade: receita ou despesa com grupo não existe
// (validarLinha recusa na escrita), e se existisse, não poderia ser
// reamarrada a outro grupo. Usa ix_transactions_competence; a ordem é
// constante no código (P6) para a prévia ser reproduzível nos quatro
// dialetos.
func (r *TransactionRepository) ListTransferCandidates(ctx context.Context, householdID, competenceMonth string, limit int) ([]transaction.TransferCandidateRow, error) {
	// Mês vazio é ERRO, e não "todos os meses": seria a casa inteira numa
	// consulta que a tela pede por mês.
	if householdID == "" || competenceMonth == "" {
		return nil, fmt.Errorf("listar candidatas a transferência exige a casa e o mês")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("listar candidatas a transferência exige um limite positivo")
	}

	var rows []transferCandidateProjection
	err := r.scope(ctx, householdID).
		Where("competence_month = ?", competenceMonth).
		Where("transfer_group_id IS NULL").
		Where(despesaDeFaturaFicaDeFora, transaction.KindExpense).
		Where("kind IN ?", categorizableKinds).
		Select("id", "account_id", "kind", "amount_cents", "occurred_on", "description", "description_norm").
		Order("occurred_on ASC, id ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listando candidatas a transferência: %w", err)
	}
	return transferCandidateRows(rows), nil
}

// IncomeExpenseInWindow devolve as receitas e despesas VIVAS da casa inteira
// com occurred_on em [minDate - DedupWindowDays, maxDate + DedupWindowDays],
// de QUALQUER competência, em ordem (occurred_on, id), até `limit` linhas.
//
// A folga é aplicada aqui, como em WindowForDedup, para o serviço passar o
// intervalo REAL das candidatas — e não as bordas do mês, porque uma
// candidata de fatura pode ter occurred_on fora do mês de competência
// (ADR-028c). A janela é o que permite a consulta usar ix_transactions_occurred
// (household_id, occurred_on, id) em vez de varrer a casa.
//
// `transfer_group_id IS NULL` aqui também: um espelho que já é perna de
// transferência não é espelho — já tem par.
func (r *TransactionRepository) IncomeExpenseInWindow(ctx context.Context, householdID string, minDate, maxDate civil.Date, limit int) ([]transaction.TransferCandidateRow, error) {
	// Intervalo inválido é ERRO, e não janela vazia: uma janela vazia
	// silenciosa faria toda candidata parecer sem par — e a prévia diria
	// "importe o extrato da outra conta" para quem já importou.
	if householdID == "" || minDate.IsZero() || maxDate.IsZero() || maxDate.Before(minDate) {
		return nil, fmt.Errorf("janela de espelhos exige a casa e um intervalo de datas válido")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("janela de espelhos exige um limite positivo")
	}

	from := shiftDays(minDate, -transaction.DedupWindowDays)
	to := shiftDays(maxDate, transaction.DedupWindowDays)

	var rows []transferCandidateProjection
	err := r.scope(ctx, householdID).
		Where("occurred_on >= ?", from.String()).
		Where("occurred_on <= ?", to.String()).
		Where("transfer_group_id IS NULL").
		Where(despesaDeFaturaFicaDeFora, transaction.KindExpense).
		Where("kind IN ?", categorizableKinds).
		Select("id", "account_id", "kind", "amount_cents", "occurred_on", "description", "description_norm").
		Order("occurred_on ASC, id ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("carregando janela de espelhos: %w", err)
	}
	return transferCandidateRows(rows), nil
}

// transferCandidateRows converte a projeção no tipo do domínio.
func transferCandidateRows(rows []transferCandidateProjection) []transaction.TransferCandidateRow {
	out := make([]transaction.TransferCandidateRow, 0, len(rows))
	for i := range rows {
		out = append(out, transaction.TransferCandidateRow{
			ID:              rows[i].ID,
			AccountID:       rows[i].AccountID,
			Kind:            rows[i].Kind,
			AmountCents:     rows[i].AmountCents,
			OccurredOn:      civilOrZero(rows[i].OccurredOn),
			Description:     rows[i].Description,
			DescriptionNorm: rows[i].DescriptionNorm,
		})
	}
	return out
}

// ConvertToTransferPair converte um par com dois UPDATEs condicionais, cada
// um obrigado a afetar EXATAMENTE uma linha (ADR-028d).
//
// O WHERE repete, no banco, tudo o que o serviço leu na memória: casa e linha
// viva (scope), o id, a CONTA daquela perna, `transfer_group_id IS NULL` e o
// `kind` ESPERADO (expense para a perna de saída, income para a de entrada).
// Entre a leitura e este UPDATE cabe outra requisição — exclusão, conversão
// pela execução concorrente, edição, e até a linha ter MUDADO DE CONTA —, e é
// o banco, não a memória, que decide se a linha ainda é o que era. Zero
// linhas é ErrTransferConversionConflict.
//
// CHAME SEMPRE DENTRO DA TRANSAÇÃO (Service.DetectTransfers o faz). São dois
// comandos: se o segundo recusa, o primeiro JÁ ESTÁ escrito, e é o rollback
// da transação que o desfaz. Fora dela, uma recusa deixaria meia
// transferência gravada — dinheiro saindo de uma conta sem entrar na outra,
// que é exatamente o que o ADR-016 existe para impedir.
//
// O SET tem quatro colunas e nenhuma outra: kind, transfer_group_id,
// category_id = NULL (transferência não tem categoria — ADR-016) e
// updated_at. O kind muda SEMPRE, então o MySQL — que só conta mudança real
// — nunca devolve 0 por "nada mudou". Sem Raw nem Exec: construtor do GORM
// com placeholders, e a lista de kinds é constante no código.
func (r *TransactionRepository) ConvertToTransferPair(ctx context.Context, householdID string, p transaction.TransferPairConversion) error {
	if householdID == "" || p.OutID == "" || p.InID == "" || p.TransferGroupID == "" {
		return fmt.Errorf("converter par em transferência exige casa, as duas linhas e o grupo")
	}
	if p.OutAccountID == "" || p.InAccountID == "" {
		return fmt.Errorf("converter par em transferência exige a conta de cada perna")
	}
	if p.OutID == p.InID {
		// Uma linha só não é um par. O serviço nunca monta isto; a guarda
		// existe para o defeito falhar aqui e não virar uma linha com dois
		// UPDATEs em sequência.
		return transaction.ErrTransferConversionConflict
	}
	if p.OutAccountID == p.InAccountID {
		// Transferir de uma conta para ela mesma é um no-op que dobraria a
		// linha no extrato daquela conta (ADR-016, validarPares). A checagem
		// em memória do pareamento já recusa; esta é a que sobrevive a um
		// defeito lá.
		return transaction.ErrTransferConversionConflict
	}

	pernas := []struct{ id, conta, de, para string }{
		{p.OutID, p.OutAccountID, transaction.KindExpense, transaction.KindTransferOut},
		{p.InID, p.InAccountID, transaction.KindIncome, transaction.KindTransferIn},
	}
	// As duas linhas são travadas em ordem de ID, e não na ordem
	// saída-depois-entrada (achado A5 da revisão de segurança): junto com a
	// ordem global dos pares no serviço, isso faz QUALQUER execução desta
	// rota travar sempre na mesma sequência. Ordens opostas sobre as mesmas
	// duas linhas são o que produz deadlock no PostgreSQL — e um deadlock
	// nativo cairia no ramo genérico do handler como 500, quando o certo
	// seria 409. O resultado da conversão não depende da ordem: cada UPDATE
	// tem o seu próprio WHERE.
	if pernas[1].id < pernas[0].id {
		pernas[0], pernas[1] = pernas[1], pernas[0]
	}
	for _, perna := range pernas {
		res := r.scope(ctx, householdID).
			Where("id = ?", perna.id).
			Where("account_id = ?", perna.conta).
			Where("transfer_group_id IS NULL").
			Where("kind = ?", perna.de).
			Updates(map[string]any{
				"kind":              perna.para,
				"transfer_group_id": p.TransferGroupID,
				"category_id":       nil,
				"updated_at":        p.UpdatedAt,
			})
		if res.Error != nil {
			return fmt.Errorf("convertendo lançamento em perna de transferência: %w", res.Error)
		}
		if res.RowsAffected != 1 {
			return transaction.ErrTransferConversionConflict
		}
	}
	return nil
}

// --- spec 0006 (ADR-029): investimentos e resgates ---------------------------

// maxCategoryFilterIDs é o teto de ids de categoria num único IN (...).
//
// É o mesmo número de category.MaxPerHousehold (200), e não por coincidência:
// o conjunto de categorias de investimento de uma casa é um subconjunto das
// categorias dela, então 200 é o teto REAL. O valor está repetido aqui, e não
// importado do domínio de categoria, porque a camada de persistência não
// depende daquele pacote — e o que este número protege é o teto de PARÂMETROS
// por comando (2100 no SQL Server, 999 historicamente no SQLite), que é
// assunto daqui.
//
// Ultrapassar é ErrTooManyCategories, e não uma consulta fatiada: as consultas
// que usam este teto ou são agregações (fatiar obrigaria a somar fatias, e
// somar fatias de dinheiro é como se começa a ter dois números) ou são
// paginadas por cursor (fatiar quebraria a página). Se algum dia o teto do
// domínio subir, o erro aparece ALTO aqui em vez de estourar dentro do driver,
// em dois dialetos só, em produção.
const maxCategoryFilterIDs = 200

// investmentMonthRow é a projeção de SumInvestmentsByMonth: uma linha por
// (mês, kind).
type investmentMonthRow struct {
	CompetenceMonth string
	Kind            string
	Cnt             int64
	Total           int64
}

// SumInvestmentsByMonth soma, por mês de competência e por kind, os
// lançamentos marcados como investimento na janela [fromMonth, toMonth].
//
// UMA consulta para os TRÊS números da tela. A janela do ano-até-o-mês
// (Y-01..M) está sempre contida na dos últimos 12 meses (M-11..M), então as no
// máximo 24 linhas desta consulta (12 meses x 2 kinds) bastam para o mês, para
// o acumulado do ano e para a série — montados em Go. Doze consultas (uma por
// mês da série) ou duas (uma para o mês, outra para o ano) custariam mais e,
// pior, poderiam discordar entre si se uma escrita entrasse no meio.
//
// A comparação de mês é lexicográfica sobre "YYYY-MM": largura fixa, então
// ordem de texto é ordem cronológica — a mesma propriedade que SumByAccountUntil
// usa em "YYYY-MM-DD" (D3 da spec 0003), e a razão de não existir nenhuma
// função de data no SQL (são quatro sintaxes diferentes — armadilha P1).
// Usa ix_transactions_competence (household_id, competence_month).
//
// `kind IN (income, expense)` vem da lista CONSTANTE categorizableKinds: perna
// de transferência não tem categoria e nunca poderia ser marcada, mas a guarda
// fica escrita porque é ela que garante que a tela de investimentos e os
// totais do resumo somem exatamente as mesmas linhas.
func (r *TransactionRepository) SumInvestmentsByMonth(ctx context.Context, householdID string, categoryIDs []string, fromMonth, toMonth string) ([]transaction.InvestmentMonthTotals, error) {
	if householdID == "" {
		return nil, fmt.Errorf("somando investimentos exige a casa")
	}
	// Janela vazia ou invertida é ERRO, e não resultado vazio: um "de" maior
	// que o "até" devolveria zeros em silêncio, e a tela mostraria "você não
	// investiu nada" para quem investiu.
	if fromMonth == "" || toMonth == "" || fromMonth > toMonth {
		return nil, fmt.Errorf("somando investimentos exige uma janela de meses válida")
	}

	ids := dedupeStrings(categoryIDs)
	if len(ids) == 0 {
		return nil, transaction.ErrEmptyCategoryFilter
	}
	if len(ids) > maxCategoryFilterIDs {
		return nil, transaction.ErrTooManyCategories
	}

	var rows []investmentMonthRow
	err := r.scope(ctx, householdID).
		Where("kind IN ?", categorizableKinds).
		Where("category_id IN ?", ids).
		Where("competence_month >= ?", fromMonth).
		Where("competence_month <= ?", toMonth).
		Select("competence_month AS competence_month, kind AS kind, COUNT(*) AS cnt, COALESCE(SUM(amount_cents), 0) AS total").
		Group("competence_month").
		Group("kind").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("somando investimentos por mês: %w", err)
	}

	// A ordenação é feita em Go, e não por ORDER BY: são no máximo 24 linhas, e
	// ordenar aqui tira do caminho a única diferença que sobraria entre os
	// dialetos (collation). Mês sem movimento não volta — quem chama preenche
	// os zeros da série de 12.
	porMes := make(map[string]*transaction.InvestmentMonthTotals, len(rows))
	meses := make([]string, 0, len(rows))
	for _, row := range rows {
		item, ok := porMes[row.CompetenceMonth]
		if !ok {
			item = &transaction.InvestmentMonthTotals{Month: row.CompetenceMonth}
			porMes[row.CompetenceMonth] = item
			meses = append(meses, row.CompetenceMonth)
		}
		// O FLUXO vem do kind do LANÇAMENTO (ADR-029d): despesa marcada é
		// aporte (o dinheiro saiu), receita marcada é resgate (entrou).
		switch row.Kind {
		case transaction.KindExpense:
			item.ContributionsCents += row.Total
			item.ContributionCount += row.Cnt
		case transaction.KindIncome:
			item.RedemptionsCents += row.Total
			item.RedemptionCount += row.Cnt
		}
	}

	slices.Sort(meses)
	out := make([]transaction.InvestmentMonthTotals, 0, len(meses))
	for _, mes := range meses {
		out = append(out, *porMes[mes])
	}
	return out, nil
}

// ListByCategories devolve a página dos lançamentos do mês cuja categoria está
// no conjunto informado — os itens da tela de investimentos.
//
// Ordem e cursor são os MESMOS de List: (occurred_on DESC, id DESC), com a
// comparação do cursor na forma expandida (tupla não existe no SQL Server) e a
// ordem CONSTANTE no código (armadilha P6 e S4 — ORDER BY concatenado é
// injeção). Peça Limit+1 para saber se há próxima página.
//
// `kind IN (income, expense)` não é redundância com o filtro de categoria:
// é o que garante que esta lista mostre EXATAMENTE as linhas que
// SumInvestmentsByMonth soma. Duas perguntas diferentes para o mesmo dinheiro
// é como se começa a ter dois números.
func (r *TransactionRepository) ListByCategories(ctx context.Context, householdID string, f transaction.CategoryListFilter) ([]transaction.Transaction, error) {
	if householdID == "" {
		return nil, fmt.Errorf("listar por categoria exige a casa")
	}
	// Mês vazio é ERRO, e não "todos os meses": seria a casa inteira numa
	// consulta que a tela pede por mês.
	if f.CompetenceMonth == "" {
		return nil, fmt.Errorf("listar por categoria exige o mês")
	}

	ids := dedupeStrings(f.CategoryIDs)
	if len(ids) == 0 {
		return nil, transaction.ErrEmptyCategoryFilter
	}
	if len(ids) > maxCategoryFilterIDs {
		return nil, transaction.ErrTooManyCategories
	}

	q := r.scope(ctx, householdID).
		Where("competence_month = ?", f.CompetenceMonth).
		Where("kind IN ?", categorizableKinds).
		Where("category_id IN ?", ids)
	if f.Cursor != nil {
		on := f.Cursor.OccurredOn.String()
		q = q.Where("occurred_on < ? OR (occurred_on = ? AND id < ?)", on, on, f.Cursor.ID)
	}

	var rows []Transaction
	if err := q.Order("occurred_on DESC, id DESC").Limit(pageSize(f.Limit)).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("listando lançamentos por categoria: %w", err)
	}

	out := make([]transaction.Transaction, 0, len(rows))
	for i := range rows {
		out = append(out, *toTransactionEntity(&rows[i]))
	}
	return out, nil
}

// categorizableProjection é a projeção mínima que o `detect` de investimentos
// lê: UncategorizedRow mais a categoria atual.
type categorizableProjection struct {
	ID              string
	Kind            string
	Description     string
	DescriptionNorm string
	CategoryID      *string
}

// ListIncomeExpenseOfMonth devolve as receitas e despesas VIVAS do mês —
// com e sem categoria —, em ordem (occurred_on, id), até `limit` linhas.
//
// A diferença para ListUncategorized é o `category_id IS NULL` que NÃO está
// aqui, e ela é o assunto do `detect` de investimentos: a linha sem categoria
// ele marca, a linha COM categoria ele mostra em lista separada e só troca com
// `overwriteCategorized` (spec 0006 §3.3.4). Duas consultas, uma para cada
// grupo, leriam o mesmo índice duas vezes e poderiam discordar entre si se uma
// edição manual entrasse no meio — e a prévia mostraria uma linha em nenhuma
// das listas, ou nas duas.
//
// Peça o teto + 1 para descobrir que ele foi ultrapassado; o repositório não
// conta. Usa ix_transactions_competence; a ordem é constante no código (P6)
// para a prévia ser REPRODUZÍVEL nos quatro dialetos.
func (r *TransactionRepository) ListIncomeExpenseOfMonth(ctx context.Context, householdID, competenceMonth string, limit int) ([]transaction.CategorizableRow, error) {
	if householdID == "" || competenceMonth == "" {
		return nil, fmt.Errorf("listar receitas e despesas do mês exige a casa e o mês")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("listar receitas e despesas do mês exige um limite positivo")
	}

	var rows []categorizableProjection
	err := r.scope(ctx, householdID).
		Where("competence_month = ?", competenceMonth).
		Where("kind IN ?", categorizableKinds).
		Select("id", "kind", "description", "description_norm", "category_id").
		Order("occurred_on ASC, id ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listando receitas e despesas do mês: %w", err)
	}

	out := make([]transaction.CategorizableRow, 0, len(rows))
	for i := range rows {
		out = append(out, transaction.CategorizableRow{
			ID:              rows[i].ID,
			Kind:            rows[i].Kind,
			Description:     rows[i].Description,
			DescriptionNorm: rows[i].DescriptionNorm,
			CategoryID:      rows[i].CategoryID,
		})
	}
	return out, nil
}

// SetCategoryWhereCurrentIn troca a categoria das linhas informadas que HOJE
// estão numa das categorias da allowlist, e devolve as linhas afetadas.
//
// É a escrita do `overwriteCategorized` (ADR-029h). A regra que a torna segura
// — "só troca categoria comum, nunca desfaz marcação de investimento" — mora
// no WHERE, e não no Go: entre a prévia e a confirmação cabe uma requisição
// inteira (outra execução do detect, uma edição manual, o outro morador da
// casa), e é o banco, não a memória do serviço, que decide o que a linha ainda
// era no instante da escrita. Passar a allowlist como conjunto de ids, e não
// como natureza, é o que mantém a decisão FECHADA: categoria criada depois da
// leitura da taxonomia simplesmente não está na lista, e a linha dela não é
// tocada.
//
// Linha sem categoria também não é alcançada — NULL nunca está num IN (...) —,
// e isso é desenho: quem marca o que está vazio é SetCategoryWhereNull.
//
// É o primeiro comando do projeto com DOIS IN (...): a allowlist vai inteira
// (≤ 200) e os alvos vão em fatias de idChunkSize (200). O pior caso são
// ~405 parâmetros por comando — abaixo do piso histórico de 999 do SQLite e
// muito abaixo dos 2100 do SQL Server.
func (r *TransactionRepository) SetCategoryWhereCurrentIn(ctx context.Context, householdID string, ids, currentCategoryIDs []string, categoryID string, at time.Time) (int64, error) {
	if householdID == "" || categoryID == "" {
		return 0, fmt.Errorf("trocar categoria em massa exige casa e categoria")
	}

	// A allowlist é conferida ANTES dos alvos: vazia, ela significaria
	// "qualquer categoria serve", e a flag opt-in viraria um sobrescrevedor
	// universal — inclusive das marcações de investimento que ela existe para
	// preservar.
	atuais := dedupeStrings(currentCategoryIDs)
	if len(atuais) == 0 {
		return 0, transaction.ErrEmptyCategoryFilter
	}
	if len(atuais) > maxCategoryFilterIDs {
		return 0, transaction.ErrTooManyCategories
	}

	alvos := dedupeStrings(ids)
	if len(alvos) == 0 {
		return 0, nil
	}

	var afetadas int64
	for inicio := 0; inicio < len(alvos); inicio += idChunkSize {
		fim := min(inicio+idChunkSize, len(alvos))

		res := r.scope(ctx, householdID).
			Where("id IN ?", alvos[inicio:fim]).
			Where("kind IN ?", categorizableKinds).
			Where("category_id IN ?", atuais).
			Updates(map[string]any{"category_id": categoryID, "updated_at": at})
		if res.Error != nil {
			return afetadas, fmt.Errorf("trocando categoria de lançamentos: %w", res.Error)
		}
		afetadas += res.RowsAffected
	}
	return afetadas, nil
}
