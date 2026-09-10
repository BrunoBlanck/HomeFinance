package gormstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"gorm.io/gorm"
)

// MembershipRepository implementa household.MembershipRepository.
type MembershipRepository struct{ base }

// NewMembershipRepository monta o repositório.
func NewMembershipRepository(db *storage.DB) *MembershipRepository {
	return &MembershipRepository{base{db: db}}
}

var _ household.MembershipRepository = (*MembershipRepository)(nil)

// Create insere o vínculo.
func (r *MembershipRepository) Create(ctx context.Context, m *household.Membership) error {
	if err := r.conn(ctx).Create(toMembershipModel(m)).Error; err != nil {
		if storage.IsDuplicate(err) {
			return household.ErrNotFound
		}
		return fmt.Errorf("inserindo vínculo: %w", err)
	}
	return nil
}

// ListByUser devolve os vínculos do usuário, do mais antigo para o mais novo.
//
// A ordenação é uma string CONSTANTE do código — nunca entrada do usuário.
// Ordenação dinâmica, quando existir, entra por allowlist de colunas
// (docs/SEGURANCA.md §3).
func (r *MembershipRepository) ListByUser(ctx context.Context, userID string) ([]household.Membership, error) {
	var rows []Membership
	err := r.conn(ctx).
		Where("user_id = ?", userID).
		Order("created_at ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listando vínculos: %w", err)
	}
	out := make([]household.Membership, 0, len(rows))
	for i := range rows {
		out = append(out, *toMembershipEntity(&rows[i]))
	}
	return out, nil
}

// ByUserAndHousehold filtra pelos DOIS lados na mesma consulta.
//
// docs/SEGURANCA.md §2: buscar por um e conferir o outro depois é proibido
// (janela de TOCTOU e risco de esquecer a checagem).
func (r *MembershipRepository) ByUserAndHousehold(ctx context.Context, userID, householdID string) (*household.Membership, error) {
	var m Membership
	err := r.conn(ctx).
		Where("user_id = ? AND household_id = ?", userID, householdID).
		Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, household.ErrNotFound
		}
		return nil, fmt.Errorf("buscando vínculo: %w", err)
	}
	return toMembershipEntity(&m), nil
}

// CountByUser conta os vínculos do usuário.
func (r *MembershipRepository) CountByUser(ctx context.Context, userID string) (int64, error) {
	var n int64
	if err := r.conn(ctx).Model(&Membership{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
		return 0, fmt.Errorf("contando vínculos: %w", err)
	}
	return n, nil
}
