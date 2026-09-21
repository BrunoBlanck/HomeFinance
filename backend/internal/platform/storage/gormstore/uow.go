package gormstore

import (
	"context"
	"fmt"

	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"gorm.io/gorm"
)

// UnitOfWork executa uma função dentro de uma transação.
//
// ADR-013(c): o schema NÃO tem chave estrangeira física. Isso significa que
// uma casa sem vínculo, ou um vínculo apontando para usuário inexistente,
// NÃO é barrado pelo banco. Toda escrita multi-tabela (registro,
// verificação de e-mail, redefinição de senha) precisa passar por aqui.
type UnitOfWork struct {
	db *storage.DB
}

// NewUnitOfWork monta a unidade de trabalho.
func NewUnitOfWork(db *storage.DB) *UnitOfWork { return &UnitOfWork{db: db} }

// Do abre a transação e a publica no contexto, de modo que todo repositório
// chamado dentro de fn use automaticamente a mesma transação.
//
// Reentrante: se o contexto já carrega uma transação, reaproveitamos em vez
// de abrir outra (o GORM criaria um savepoint aninhado, o que muda a
// semântica de rollback sem necessidade).
//
// Todo erro de saída passa por storage.ComErroDeContexto. A raiz está na
// abertura da conexão (storage/ctxerr.go), que já traduz o "interrupted (9)"
// do driver em erro de prazo/cancelamento reconhecível por `errors.Is`; a
// repetição AQUI cobre o que não é um comando SQL — o Begin, o Commit e o erro
// que a própria fn devolve — e é um no-op quando a raiz já fez o trabalho.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFromContext(ctx); ok {
		return storage.ComErroDeContexto(ctx, fn(ctx))
	}
	err := u.db.Gorm().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(withTx(ctx, tx))
	})
	if err != nil {
		return storage.ComErroDeContexto(ctx, fmt.Errorf("transação: %w", err))
	}
	return nil
}
