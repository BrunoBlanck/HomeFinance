package gormstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"gorm.io/gorm"
)

// VerificationCodeRepository implementa auth.VerificationCodeRepository.
type VerificationCodeRepository struct{ base }

// NewVerificationCodeRepository monta o repositório.
func NewVerificationCodeRepository(db *storage.DB) *VerificationCodeRepository {
	return &VerificationCodeRepository{base{db: db}}
}

var _ auth.VerificationCodeRepository = (*VerificationCodeRepository)(nil)

// Create insere o código (apenas o HMAC).
func (r *VerificationCodeRepository) Create(ctx context.Context, c *auth.VerificationCode) error {
	if err := r.conn(ctx).Create(toVerificationCodeModel(c)).Error; err != nil {
		return fmt.Errorf("inserindo código de verificação: %w", err)
	}
	return nil
}

// ActiveByEmailPurpose devolve o código vivo mais recente do par
// (e-mail, propósito).
//
// D5 da spec 0001: a consulta é ancorada em email+purpose, com a MESMA forma
// e o mesmo custo exista ou não a conta. É isso que permite responder
// exatamente igual para "e-mail sem código" e "e-mail inexistente"
// (grupo D da §3.12).
func (r *VerificationCodeRepository) ActiveByEmailPurpose(ctx context.Context, email, purpose string, now time.Time) (*auth.VerificationCode, error) {
	var m VerificationCode
	err := r.conn(ctx).
		Where("email = ? AND purpose = ? AND consumed_at IS NULL AND expires_at > ?", email, purpose, now).
		Order("created_at DESC, id DESC").
		Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, auth.ErrNotFound
		}
		return nil, fmt.Errorf("buscando código ativo: %w", err)
	}
	return toVerificationCodeEntity(&m), nil
}

// LastIssuedAt devolve quando o último código do par foi emitido (cooldown).
func (r *VerificationCodeRepository) LastIssuedAt(ctx context.Context, email, purpose string) (time.Time, bool, error) {
	var m VerificationCode
	err := r.conn(ctx).
		Where("email = ? AND purpose = ?", email, purpose).
		Order("created_at DESC, id DESC").
		Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("buscando última emissão: %w", err)
	}
	return m.CreatedAt, true, nil
}

// ConsumeAllActive invalida todos os códigos vivos do par.
// docs/SEGURANCA.md §1.1: emitir um código novo invalida os anteriores.
func (r *VerificationCodeRepository) ConsumeAllActive(ctx context.Context, email, purpose string, at time.Time) (int64, error) {
	res := r.conn(ctx).Model(&VerificationCode{}).
		Where("email = ? AND purpose = ? AND consumed_at IS NULL", email, purpose).
		Updates(map[string]any{"consumed_at": at, "updated_at": at})
	if res.Error != nil {
		return 0, fmt.Errorf("invalidando códigos anteriores: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// IncrementAttempts soma 1 no contador DENTRO do banco e devolve o valor já
// atualizado.
//
// docs/SEGURANCA.md §1.1: o limite de 5 tentativas é o que impede a força
// bruta online dos 10^6 valores possíveis. Se o incremento fosse
// read-modify-write em Go, N tentativas simultâneas contariam como uma só e o
// limite viraria decoração.
//
// gorm.Expr recebe uma expressão CONSTANTE do código ("attempts + 1"), sem
// nenhuma entrada do usuário — é a forma parametrizada de fazer incremento
// atômico no GORM, não interpolação de SQL.
func (r *VerificationCodeRepository) IncrementAttempts(ctx context.Context, id string) (int, bool, error) {
	conn := r.conn(ctx)

	res := conn.Model(&VerificationCode{}).
		Where("id = ? AND consumed_at IS NULL", id).
		Update("attempts", gorm.Expr("attempts + 1"))
	if res.Error != nil {
		return 0, false, fmt.Errorf("incrementando tentativas: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return 0, false, nil
	}

	var m VerificationCode
	if err := conn.Where("id = ?", id).Take(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("relendo tentativas: %w", err)
	}
	return m.Attempts, true, nil
}

// Consume marca o código como usado, uma única vez.
//
// O "AND consumed_at IS NULL" com RowsAffected == 1 garante o uso único
// mesmo sob corrida: duas requisições com o código certo, só uma vence.
func (r *VerificationCodeRepository) Consume(ctx context.Context, id string, at time.Time) (bool, error) {
	res := r.conn(ctx).Model(&VerificationCode{}).
		Where("id = ? AND consumed_at IS NULL", id).
		Updates(map[string]any{"consumed_at": at, "updated_at": at})
	if res.Error != nil {
		return false, fmt.Errorf("consumindo código: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// DeleteExpired expurga códigos expirados e consumidos antes do corte.
func (r *VerificationCodeRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res := r.conn(ctx).
		Where("expires_at < ? OR (consumed_at IS NOT NULL AND consumed_at < ?)", before, before).
		Delete(&VerificationCode{})
	if res.Error != nil {
		return 0, fmt.Errorf("expurgando códigos: %w", res.Error)
	}
	return res.RowsAffected, nil
}
