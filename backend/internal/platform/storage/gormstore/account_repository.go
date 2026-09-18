package gormstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"gorm.io/gorm"
)

// AccountRepository implementa account.Repository.
type AccountRepository struct{ base }

// NewAccountRepository monta o repositório.
func NewAccountRepository(db *storage.DB) *AccountRepository {
	return &AccountRepository{base{db: db}}
}

var _ account.Repository = (*AccountRepository)(nil)

// scope é a base de TODA consulta deste repositório: filtra pela casa e
// esconde o que foi excluído logicamente.
//
// Existe como método único, e não copiado em cada consulta, porque as duas
// condições são exatamente as que não podem faltar: sem `household_id` a
// consulta vaza dado de outra casa (BOLA, docs/SEGURANCA.md §2), e sem
// `deleted_at IS NULL` a conta excluída ressuscita. Concentrar as duas num
// lugar torna o esquecimento visível na revisão.
func (r *AccountRepository) scope(ctx context.Context, householdID string) *gorm.DB {
	return r.conn(ctx).
		Model(&Account{}).
		Where("household_id = ?", householdID).
		Where("deleted_at IS NULL")
}

// Create insere a conta.
func (r *AccountRepository) Create(ctx context.Context, a *account.Account) error {
	if err := r.conn(ctx).Create(toAccountModel(a)).Error; err != nil {
		return fmt.Errorf("inserindo conta: %w", err)
	}
	return nil
}

// ByID devolve a conta da casa. Conta de outra casa é indistinguível de conta
// inexistente — os dois caminhos levam a account.ErrNotFound, que o handler
// traduz para 404 (S1 do PLANOS.md: 403 confirmaria a existência).
func (r *AccountRepository) ByID(ctx context.Context, householdID, id string) (*account.Account, error) {
	var m Account
	err := r.scope(ctx, householdID).Where("id = ?", id).Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, account.ErrNotFound
		}
		return nil, fmt.Errorf("buscando conta: %w", err)
	}
	return toAccountEntity(&m), nil
}

// List devolve as contas da casa, ordenadas por nome normalizado.
//
// A ordenação é por coluna FIXA escrita no código, nunca por entrada do
// cliente (armadilha P6 e S4: concatenar ORDER BY é injeção). Ordenar pela
// forma normalizada dá ordem alfabética que ignora acento e caixa, que é a
// que o usuário espera; o desempate por id mantém o resultado estável.
func (r *AccountRepository) List(ctx context.Context, householdID string, includeArchived bool) ([]account.Account, error) {
	q := r.scope(ctx, householdID)
	if !includeArchived {
		q = q.Where("archived_at IS NULL")
	}

	var rows []Account
	if err := q.Order("name_norm ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("listando contas: %w", err)
	}

	out := make([]account.Account, 0, len(rows))
	for i := range rows {
		out = append(out, *toAccountEntity(&rows[i]))
	}
	return out, nil
}

// Update grava os campos mutáveis.
//
// As três colunas do schema v3 (institution e os dias de fechamento e
// vencimento da fatura) entram na lista porque são MUTÁVEIS de verdade — o
// cartão muda o dia de vencimento. Deixá-las de fora faria o dia seguinte de
// quem as implementar no serviço ser "eu setei o campo e nada aconteceu", que é
// o pior tipo de falha: silenciosa. O serviço faz ler-alterar-gravar, então
// incluí-las aqui não sobrescreve nada por engano.
//
// Usa Select explícito: sem ele, um Updates com struct ignoraria os campos
// zerados (e desarquivar, que grava NULL em archived_at, não funcionaria),
// enquanto um Save reescreveria household_id e created_at a partir do que
// estivesse na struct. A lista explícita também garante que household_id
// jamais entre no SET — ele é só filtro.
func (r *AccountRepository) Update(ctx context.Context, a *account.Account) error {
	m := toAccountModel(a)
	res := r.scope(ctx, a.HouseholdID).
		Where("id = ?", a.ID).
		Select("name", "name_norm", "kind", "opening_balance_cents", "opening_date",
			"institution", "statement_closing_day", "statement_due_day",
			"archived_at", "updated_at").
		Updates(m)
	if res.Error != nil {
		return fmt.Errorf("atualizando conta: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return account.ErrNotFound
	}
	return nil
}

// SoftDelete marca a exclusão lógica.
func (r *AccountRepository) SoftDelete(ctx context.Context, householdID, id string, at time.Time) error {
	res := r.scope(ctx, householdID).
		Where("id = ?", id).
		Updates(map[string]any{"deleted_at": at, "updated_at": at})
	if res.Error != nil {
		return fmt.Errorf("excluindo conta: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return account.ErrNotFound
	}
	return nil
}

// CountAll conta as contas não excluídas da casa — arquivada inclusive, porque
// ela ainda existe e ocupa vaga no limite.
func (r *AccountRepository) CountAll(ctx context.Context, householdID string) (int64, error) {
	var total int64
	if err := r.scope(ctx, householdID).Count(&total).Error; err != nil {
		return 0, fmt.Errorf("contando contas: %w", err)
	}
	return total, nil
}

// NameTaken informa se já existe conta ATIVA com este nome normalizado.
func (r *AccountRepository) NameTaken(ctx context.Context, householdID, nameNorm, exceptID string) (bool, error) {
	q := r.scope(ctx, householdID).
		Where("archived_at IS NULL").
		Where("name_norm = ?", nameNorm)
	if exceptID != "" {
		q = q.Where("id <> ?", exceptID)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return false, fmt.Errorf("verificando nome de conta: %w", err)
	}
	return total > 0, nil
}

// --- palavras-chave (schema v4, spec 0005, ADR-026d) ------------------------
//
// Espelho de CategoryRepository: tabela própria (account_keywords), índice
// único (household_id, keyword_norm) INDEPENDENTE do de categoria — a mesma
// palavra pode estar numa conta e numa categoria da mesma casa. As decisões
// de desenho estão comentadas lá; aqui só o que difere.

// keywordScope filtra as palavras-chave da conta pela casa.
func (r *AccountRepository) keywordScope(ctx context.Context, householdID string) *gorm.DB {
	return r.conn(ctx).Model(&AccountKeyword{}).Where("household_id = ?", householdID)
}

// ListKeywords devolve todas as palavras-chave de conta da casa em UMA
// consulta, em ordem (account_id, position, id).
func (r *AccountRepository) ListKeywords(ctx context.Context, householdID string) ([]account.Keyword, error) {
	if householdID == "" {
		return nil, fmt.Errorf("listar palavras-chave exige a casa")
	}
	var rows []AccountKeyword
	err := r.keywordScope(ctx, householdID).
		Order("account_id ASC, position ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listando palavras-chave de conta: %w", err)
	}
	out := make([]account.Keyword, 0, len(rows))
	for i := range rows {
		out = append(out, *toAccountKeywordEntity(&rows[i]))
	}
	return out, nil
}

// KeywordOwners devolve norm -> conta dona, para as norms que já existem na
// casa. IN fatiado em idChunkSize (teto de parâmetros por comando).
func (r *AccountRepository) KeywordOwners(ctx context.Context, householdID string, norms []string) (map[string]string, error) {
	if householdID == "" {
		return nil, fmt.Errorf("consultar donas de palavra-chave exige a casa")
	}
	unicas := dedupeStrings(norms)
	out := make(map[string]string, len(unicas))
	for inicio := 0; inicio < len(unicas); inicio += idChunkSize {
		fim := min(inicio+idChunkSize, len(unicas))

		var rows []keywordOwnerRow
		err := r.keywordScope(ctx, householdID).
			Where("keyword_norm IN ?", unicas[inicio:fim]).
			Select("keyword_norm AS keyword_norm, account_id AS owner_id").
			Scan(&rows).Error
		if err != nil {
			return nil, fmt.Errorf("consultando donas de palavra-chave: %w", err)
		}
		for i := range rows {
			out[rows[i].KeywordNorm] = rows[i].OwnerID
		}
	}
	return out, nil
}

// ReplaceKeywords apaga a lista atual da conta e grava a nova, na transação
// em curso. Guardas de casa e de dona como em CategoryRepository.
func (r *AccountRepository) ReplaceKeywords(ctx context.Context, householdID, accountID string, kws []account.Keyword) error {
	if householdID == "" || accountID == "" {
		return fmt.Errorf("substituir palavras-chave exige casa e conta")
	}

	models := make([]AccountKeyword, 0, len(kws))
	for i := range kws {
		k := kws[i]
		if k.HouseholdID != "" && k.HouseholdID != householdID {
			return fmt.Errorf("palavra-chave de outra casa na lista")
		}
		if k.AccountID != "" && k.AccountID != accountID {
			return fmt.Errorf("palavra-chave de outra conta na lista")
		}
		if k.ID == "" || k.Keyword == "" || k.Norm == "" {
			return fmt.Errorf("palavra-chave sem id, texto ou forma normalizada")
		}
		k.HouseholdID = householdID
		k.AccountID = accountID
		models = append(models, *toAccountKeywordModel(&k))
	}

	if err := r.DeleteKeywords(ctx, householdID, accountID); err != nil {
		return err
	}
	if len(models) == 0 {
		return nil
	}
	if err := r.conn(ctx).CreateInBatches(&models, keywordBatchSize).Error; err != nil {
		// Erro nativo não embrulhado: a mensagem do banco ecoa a palavra.
		if storage.IsDuplicate(err) {
			return account.ErrKeywordTaken
		}
		return fmt.Errorf("gravando palavras-chave de conta: %w", err)
	}
	return nil
}

// DeleteKeywords apaga fisicamente as palavras-chave da conta.
func (r *AccountRepository) DeleteKeywords(ctx context.Context, householdID, accountID string) error {
	if householdID == "" || accountID == "" {
		return fmt.Errorf("apagar palavras-chave exige casa e conta")
	}
	err := r.conn(ctx).
		Where("household_id = ?", householdID).
		Where("account_id = ?", accountID).
		Delete(&AccountKeyword{}).Error
	if err != nil {
		return fmt.Errorf("apagando palavras-chave de conta: %w", err)
	}
	return nil
}
