package gormstore

import (
	"context"

	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"gorm.io/gorm"
)

type txKey struct{}

// withTx publica a transação em curso no contexto.
func withTx(ctx context.Context, tx *gorm.DB) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// txFromContext recupera a transação em curso, se houver.
func txFromContext(ctx context.Context) (*gorm.DB, bool) {
	tx, ok := ctx.Value(txKey{}).(*gorm.DB)
	return tx, ok && tx != nil
}

// base é a raiz compartilhada por todos os repositórios.
type base struct {
	db *storage.DB
}

// conn devolve o handle certo para a operação: a transação em curso, quando
// o contexto carrega uma; senão, o pool.
//
// É isto que faz o UnitOfWork funcionar sem passar transação por parâmetro em
// toda assinatura de repositório — e, mais importante, o que garante que uma
// escrita esquecida dentro de um UnitOfWork não escape da transação
// silenciosamente (ADR-013: sem FK física, isso deixaria linha órfã).
func (b base) conn(ctx context.Context) *gorm.DB {
	if tx, ok := txFromContext(ctx); ok {
		return tx.WithContext(ctx)
	}
	return b.db.Gorm().WithContext(ctx)
}
