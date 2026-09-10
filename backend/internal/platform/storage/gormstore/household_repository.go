package gormstore

import (
	"context"
	"errors"
	"fmt"

	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"gorm.io/gorm"
)

// HouseholdRepository implementa household.Repository.
type HouseholdRepository struct{ base }

// NewHouseholdRepository monta o repositório.
func NewHouseholdRepository(db *storage.DB) *HouseholdRepository {
	return &HouseholdRepository{base{db: db}}
}

var _ household.Repository = (*HouseholdRepository)(nil)

// Create insere a casa.
func (r *HouseholdRepository) Create(ctx context.Context, h *household.Household) error {
	if err := r.conn(ctx).Create(toHouseholdModel(h)).Error; err != nil {
		return fmt.Errorf("inserindo casa: %w", err)
	}
	return nil
}

// ByID busca a casa por identificador.
func (r *HouseholdRepository) ByID(ctx context.Context, id string) (*household.Household, error) {
	var m Household
	err := r.conn(ctx).Where("id = ?", id).Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, household.ErrNotFound
		}
		return nil, fmt.Errorf("buscando casa: %w", err)
	}
	return toHouseholdEntity(&m), nil
}

// ByIDs devolve várias casas de uma vez.
//
// O IN é parametrizado pelo GORM (placeholder "?" com slice); nenhuma parte
// da consulta é montada por concatenação.
func (r *HouseholdRepository) ByIDs(ctx context.Context, ids []string) ([]household.Household, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []Household
	if err := r.conn(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("buscando casas: %w", err)
	}
	out := make([]household.Household, 0, len(rows))
	for i := range rows {
		out = append(out, *toHouseholdEntity(&rows[i]))
	}
	return out, nil
}
