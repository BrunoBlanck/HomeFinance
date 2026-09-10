package gormstore

import (
	"context"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
)

// AuditRepository implementa audit.Repository.
type AuditRepository struct{ base }

// NewAuditRepository monta o repositório.
func NewAuditRepository(db *storage.DB) *AuditRepository {
	return &AuditRepository{base{db: db}}
}

var _ audit.Repository = (*AuditRepository)(nil)

// Create grava a entrada de auditoria.
func (r *AuditRepository) Create(ctx context.Context, e *audit.Entry) error {
	if err := r.conn(ctx).Create(toAuditModel(e)).Error; err != nil {
		return fmt.Errorf("gravando auditoria: %w", err)
	}
	return nil
}

// DeleteOlderThan expurga o rastro antigo.
func (r *AuditRepository) DeleteOlderThan(ctx context.Context, before time.Time) (int64, error) {
	res := r.conn(ctx).Where("created_at < ?", before).Delete(&AuditLog{})
	if res.Error != nil {
		return 0, fmt.Errorf("expurgando auditoria: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// ListByAction devolve entradas de uma ação, da mais recente para a mais
// antiga. Existe para teste e diagnóstico; não há API de leitura do audit
// log nesta entrega (§1 da spec 0001).
func (r *AuditRepository) ListByAction(ctx context.Context, action string, limit int) ([]audit.Entry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var rows []AuditLog
	err := r.conn(ctx).
		Where("action = ?", action).
		Order("created_at DESC, id DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("listando auditoria: %w", err)
	}
	out := make([]audit.Entry, 0, len(rows))
	for i := range rows {
		out = append(out, *toAuditEntity(&rows[i]))
	}
	return out, nil
}
