package gormstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"gorm.io/gorm"
)

// CardStatementRepository implementa cardstatement.Repository.
type CardStatementRepository struct{ base }

// NewCardStatementRepository monta o repositório.
func NewCardStatementRepository(db *storage.DB) *CardStatementRepository {
	return &CardStatementRepository{base{db: db}}
}

var _ cardstatement.Repository = (*CardStatementRepository)(nil)

// maxStatementPage é o teto da listagem: 24 faturas são dois anos de cartão, e
// ninguém precisa de mais numa tela.
const maxStatementPage = 24

// scope filtra pela casa e esconde a fatura excluída logicamente.
func (r *CardStatementRepository) scope(ctx context.Context, householdID string) *gorm.DB {
	return r.conn(ctx).
		Model(&CardStatement{}).
		Where("household_id = ?", householdID).
		Where("deleted_at IS NULL")
}

// Upsert grava a fatura de forma idempotente por (casa, conta, competência).
//
// É SELECT + INSERT/UPDATE, e não um upsert de dialeto (armadilha P8): ON
// CONFLICT (Postgres/SQLite), ON DUPLICATE KEY UPDATE (MySQL) e MERGE (MSSQL)
// são três sintaxes com três semânticas, e o AutoMigrate não muda isso.
//
// A busca IGNORA deleted_at de propósito: o índice único do banco não
// distingue linha excluída, então uma fatura excluída continua ocupando a
// competência. Procurar só entre as ativas faria o INSERT bater no índice e
// aquela competência ficaria bloqueada para sempre; achando a excluída,
// restauramos.
//
// Chame SEMPRE dentro de um UnitOfWork: entre o SELECT e o INSERT existe uma
// janela, e é o índice único que a fecha — a colisão volta como ErrDuplicate,
// que quem chama trata relendo, não como 500.
func (r *CardStatementRepository) Upsert(ctx context.Context, s *cardstatement.Statement) error {
	if s == nil || s.HouseholdID == "" || s.AccountID == "" || s.CompetenceMonth == "" {
		return fmt.Errorf("fatura exige casa, conta e competência")
	}

	var existing CardStatement
	err := r.conn(ctx).
		Model(&CardStatement{}).
		Where("household_id = ?", s.HouseholdID).
		Where("account_id = ?", s.AccountID).
		Where("competence_month = ?", s.CompetenceMonth).
		Take(&existing).Error

	switch {
	case err == nil:
		// A fatura já existe: o id dela é a verdade, e quem chamou precisa
		// dele para ligar os lançamentos à fatura certa.
		s.ID = existing.ID
		s.CreatedAt = existing.CreatedAt
		s.DeletedAt = nil
		res := r.conn(ctx).
			Model(&CardStatement{}).
			Where("household_id = ?", s.HouseholdID).
			Where("id = ?", existing.ID).
			Updates(map[string]any{
				"closing_date": s.ClosingDate.String(),
				"due_date":     s.DueDate.String(),
				"source":       s.Source,
				"updated_at":   s.UpdatedAt,
				"deleted_at":   nil,
			})
		if res.Error != nil {
			return fmt.Errorf("atualizando fatura: %w", res.Error)
		}
		return nil

	case errors.Is(err, gorm.ErrRecordNotFound):
		if cerr := r.conn(ctx).Create(toCardStatementModel(s)).Error; cerr != nil {
			if storage.IsDuplicate(cerr) {
				// Outra requisição criou a mesma fatura entre o SELECT e o
				// INSERT. O índice único fez o seu trabalho.
				return fmt.Errorf("criando fatura: %w", cardstatement.ErrDuplicate)
			}
			return fmt.Errorf("criando fatura: %w", cerr)
		}
		return nil

	default:
		return fmt.Errorf("buscando fatura: %w", err)
	}
}

// ByID devolve a fatura da casa; de outra casa é ErrNotFound (S1).
func (r *CardStatementRepository) ByID(ctx context.Context, householdID, id string) (*cardstatement.Statement, error) {
	var m CardStatement
	err := r.scope(ctx, householdID).Where("id = ?", id).Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, cardstatement.ErrNotFound
		}
		return nil, fmt.Errorf("buscando fatura: %w", err)
	}
	return toCardStatementEntity(&m), nil
}

// List devolve as faturas da casa, da competência mais recente para a mais
// antiga. A ordem é constante no código (P6).
func (r *CardStatementRepository) List(ctx context.Context, householdID string, f cardstatement.ListFilter) ([]cardstatement.Statement, error) {
	q := r.scope(ctx, householdID)
	if f.AccountID != "" {
		q = q.Where("account_id = ?", f.AccountID)
	}
	if f.CompetenceMonth != "" {
		q = q.Where("competence_month = ?", f.CompetenceMonth)
	}

	limit := f.Limit
	if limit <= 0 || limit > maxStatementPage {
		limit = maxStatementPage
	}

	var rows []CardStatement
	if err := q.Order("competence_month DESC, id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("listando faturas: %w", err)
	}

	out := make([]cardstatement.Statement, 0, len(rows))
	for i := range rows {
		out = append(out, *toCardStatementEntity(&rows[i]))
	}
	return out, nil
}
