package storage

import (
	"context"
	"fmt"
	"log/slog"
)

// Migrate roda o AutoMigrate do GORM sobre os modelos informados (ADR-008).
//
// Os modelos vêm por parâmetro (gormstore.Models()) e não por import: assim
// storage não depende de gormstore, e gormstore pode depender de storage sem
// ciclo.
//
// Limite conhecido e aceito do ADR-008: AutoMigrate CRIA tabela, coluna,
// índice e constraint que faltam, mas nunca remove nem renomeia coluna, e não
// altera tipo de forma destrutiva. Mudança destrutiva exige migração manual
// registrada em ADR próprio.
func Migrate(ctx context.Context, db *DB, lg *slog.Logger, models ...any) error {
	if db == nil {
		return fmt.Errorf("migrando schema: banco não inicializado")
	}
	if len(models) == 0 {
		return nil
	}

	if lg != nil {
		lg.InfoContext(ctx, "aplicando automigrate", slog.Int("models", len(models)))
	}

	if err := db.Gorm().WithContext(ctx).AutoMigrate(models...); err != nil {
		return fmt.Errorf("aplicando automigrate: %w", scrub(err, db.dsn))
	}

	if lg != nil {
		lg.InfoContext(ctx, "automigrate concluído")
	}
	return nil
}
