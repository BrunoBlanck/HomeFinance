package gormstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"gorm.io/gorm"
)

// UserRepository implementa user.Repository.
type UserRepository struct{ base }

// NewUserRepository monta o repositório.
func NewUserRepository(db *storage.DB) *UserRepository {
	return &UserRepository{base{db: db}}
}

var _ user.Repository = (*UserRepository)(nil)

// Create insere o usuário. Violação da unicidade de e-mail vira
// user.ErrEmailTaken (não a mensagem crua do driver, que ecoa o valor).
func (r *UserRepository) Create(ctx context.Context, u *user.User) error {
	if err := r.conn(ctx).Create(toUserModel(u)).Error; err != nil {
		if storage.IsDuplicate(err) {
			return user.ErrEmailTaken
		}
		return fmt.Errorf("inserindo usuário: %w", err)
	}
	return nil
}

// ByID busca por identificador.
func (r *UserRepository) ByID(ctx context.Context, id string) (*user.User, error) {
	var m User
	err := r.conn(ctx).Where("id = ?", id).Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, user.ErrNotFound
		}
		return nil, fmt.Errorf("buscando usuário por id: %w", err)
	}
	return toUserEntity(&m), nil
}

// ByEmail busca pelo e-mail já normalizado.
func (r *UserRepository) ByEmail(ctx context.Context, email string) (*user.User, error) {
	var m User
	err := r.conn(ctx).Where("email = ?", email).Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, user.ErrNotFound
		}
		return nil, fmt.Errorf("buscando usuário por e-mail: %w", err)
	}
	return toUserEntity(&m), nil
}

// ActivatePending grava nome, hash de senha e a data de verificação numa
// única linha de UPDATE.
//
// O "AND email_verified_at IS NULL" é a defesa central deste método, e agora
// vale por dois motivos: (1) reconfirmar não pode reescrever a data original,
// e (2) um "registro" repetido sobre um e-mail já ativo jamais pode trocar a
// senha do titular — seria tomada de conta trivial. Zero linhas afetadas =
// a conta já estava verificada; a operação é idempotente.
func (r *UserRepository) ActivatePending(ctx context.Context, id, name, passwordHash string, at time.Time) (bool, error) {
	res := r.conn(ctx).Model(&User{}).
		Where("id = ? AND email_verified_at IS NULL", id).
		Updates(map[string]any{
			"name":              name,
			"password_hash":     passwordHash,
			"email_verified_at": at,
			"updated_at":        at,
		})
	if res.Error != nil {
		return false, fmt.Errorf("ativando conta pendente: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// UpdatePassword troca a senha.
func (r *UserRepository) UpdatePassword(ctx context.Context, id, passwordHash string, at time.Time) error {
	res := r.conn(ctx).Model(&User{}).
		Where("id = ?", id).
		Updates(map[string]any{"password_hash": passwordHash, "updated_at": at})
	if res.Error != nil {
		return fmt.Errorf("atualizando senha: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return user.ErrNotFound
	}
	return nil
}
