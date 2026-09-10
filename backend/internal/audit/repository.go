package audit

import (
	"context"
	"time"
)

// Repository é a interface de persistência do rastro de auditoria. A
// implementação vive em internal/platform/storage/gormstore (ADR-008).
type Repository interface {
	// Create grava uma entrada. Quando o contexto carrega uma transação
	// aberta, a gravação entra nela (ver UnitOfWork em internal/auth).
	Create(ctx context.Context, e *Entry) error

	// DeleteOlderThan expurga o rastro antigo. Usado pelo janitor.
	DeleteOlderThan(ctx context.Context, before time.Time) (int64, error)
}
