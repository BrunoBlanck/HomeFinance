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

// RefreshTokenRepository implementa auth.RefreshTokenRepository.
type RefreshTokenRepository struct{ base }

// NewRefreshTokenRepository monta o repositório.
func NewRefreshTokenRepository(db *storage.DB) *RefreshTokenRepository {
	return &RefreshTokenRepository{base{db: db}}
}

var _ auth.RefreshTokenRepository = (*RefreshTokenRepository)(nil)

// CreateFamily abre a família da sessão.
//
// A linha nasce junto com o PRIMEIRO token da família (login ou verificação
// de e-mail), dentro da mesma transação: sem ela, o Refresh não teria onde
// consultar o estado da sessão e falharia fechado.
func (r *RefreshTokenRepository) CreateFamily(ctx context.Context, f *auth.RefreshFamily) error {
	if err := r.conn(ctx).Create(toRefreshFamilyModel(f)).Error; err != nil {
		return fmt.Errorf("inserindo família de refresh: %w", err)
	}
	return nil
}

// FamilyByID devolve o estado da família.
//
// É a CHECAGEM POSITIVA da rotação: o Refresh só emite sessão nova se a
// família existir e não estiver revogada. Diferente do UPDATE em
// refresh_tokens, esta linha já existe antes de qualquer rotação começar —
// então a revogação disparada por um reúso alcança até o sucessor que ainda
// nem foi inserido.
func (r *RefreshTokenRepository) FamilyByID(ctx context.Context, familyID string) (*auth.RefreshFamily, error) {
	var m RefreshFamily
	err := r.conn(ctx).Where("id = ?", familyID).Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, auth.ErrNotFound
		}
		return nil, fmt.Errorf("buscando família de refresh: %w", err)
	}
	return toRefreshFamilyEntity(&m), nil
}

// Create insere o token (só o hash).
func (r *RefreshTokenRepository) Create(ctx context.Context, t *auth.RefreshToken) error {
	if err := r.conn(ctx).Create(toRefreshTokenModel(t)).Error; err != nil {
		return fmt.Errorf("inserindo refresh token: %w", err)
	}
	return nil
}

// ByHash busca pelo SHA-256 hex do token apresentado.
func (r *RefreshTokenRepository) ByHash(ctx context.Context, tokenHash string) (*auth.RefreshToken, error) {
	var m RefreshToken
	err := r.conn(ctx).Where("token_hash = ?", tokenHash).Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, auth.ErrNotFound
		}
		return nil, fmt.Errorf("buscando refresh token: %w", err)
	}
	return toRefreshTokenEntity(&m), nil
}

// Rotate revoga o token apontando para o sucessor.
//
// ESTE É O CORAÇÃO DA DETECÇÃO DE ROUBO (risco 5 da §9 da spec 0001):
// o "AND revoked_at IS NULL" mais a contagem de linhas afetadas tornam a
// rotação atômica. Se duas requisições chegarem com o MESMO refresh, só uma
// afeta linha; a outra recebe false, e o service trata como reúso e derruba
// a família inteira. Um read-modify-write em Go deixaria as duas passarem.
func (r *RefreshTokenRepository) Rotate(ctx context.Context, id, replacedBy string, at time.Time) (bool, error) {
	res := r.conn(ctx).Model(&RefreshToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Updates(map[string]any{
			"revoked_at":  at,
			"replaced_by": replacedBy,
			"updated_at":  at,
		})
	if res.Error != nil {
		return false, fmt.Errorf("rotacionando refresh token: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// RevokeFamily mata a FAMÍLIA e derruba todos os tokens vivos dela.
//
// A ordem importa: a família é marcada PRIMEIRO. É essa marca que alcança o
// elo que ainda está sendo inserido por uma rotação concorrente — o UPDATE
// em refresh_tokens, sozinho, só enxerga o que já está gravado, e por isso
// deixava o sucessor do ladrão escapar.
//
// O carimbo da PRIMEIRA revogação é preservado ("AND revoked_at IS NULL"):
// uma segunda passagem varre os elos retardatários sem reescrever a hora em
// que a sessão morreu, que é o dado forense.
//
// O contador devolvido continua sendo o de TOKENS revogados.
func (r *RefreshTokenRepository) RevokeFamily(ctx context.Context, familyID string, at time.Time) (int64, error) {
	if err := r.conn(ctx).Model(&RefreshFamily{}).
		Where("id = ? AND revoked_at IS NULL", familyID).
		Updates(map[string]any{"revoked_at": at, "updated_at": at}).Error; err != nil {
		return 0, fmt.Errorf("revogando família de refresh: %w", err)
	}

	res := r.conn(ctx).Model(&RefreshToken{}).
		Where("family_id = ? AND revoked_at IS NULL", familyID).
		Updates(map[string]any{"revoked_at": at, "updated_at": at})
	if res.Error != nil {
		return 0, fmt.Errorf("revogando família de refresh: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// RevokeAllForUser derruba todas as sessões do usuário.
// docs/SEGURANCA.md §1.1: trocar a senha revoga TODAS as famílias.
func (r *RefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID string, at time.Time) (int64, error) {
	// Mesma razão do RevokeFamily: a família primeiro, para que nenhuma
	// rotação em voo consiga entregar um sucessor depois da troca de senha.
	if err := r.conn(ctx).Model(&RefreshFamily{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Updates(map[string]any{"revoked_at": at, "updated_at": at}).Error; err != nil {
		return 0, fmt.Errorf("revogando famílias do usuário: %w", err)
	}

	res := r.conn(ctx).Model(&RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Updates(map[string]any{"revoked_at": at, "updated_at": at})
	if res.Error != nil {
		return 0, fmt.Errorf("revogando sessões do usuário: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// DeleteExpired remove tokens expirados e os revogados antes do corte.
//
// Os revogados só são apagados depois do corte porque eles são o que permite
// detectar reúso: apagar cedo demais transformaria um token roubado em
// "desconhecido" em vez de "reúso".
func (r *RefreshTokenRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res := r.conn(ctx).
		Where("expires_at < ? OR (revoked_at IS NOT NULL AND revoked_at < ?)", before, before).
		Delete(&RefreshToken{})
	if res.Error != nil {
		return 0, fmt.Errorf("expurgando refresh tokens: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// DeleteDeadFamilies remove as famílias que não têm mais nenhum token.
//
// Uma família sem elo é uma sessão que já expirou por completo (os tokens
// foram para o DeleteExpired acima). Sem este expurgo, refresh_families
// cresceria para sempre — o mapa do rate limiter tem o mesmo problema e a
// mesma solução (risco 9 da §9 da spec 0001).
//
// O corte por created_at protege a família recém-aberta cujo primeiro token
// ainda não foi commitado por outra conexão.
func (r *RefreshTokenRepository) DeleteDeadFamilies(ctx context.Context, before time.Time) (int64, error) {
	vivas := r.conn(ctx).Model(&RefreshToken{}).Select("family_id")

	res := r.conn(ctx).
		Where("created_at < ? AND id NOT IN (?)", before, vivas).
		Delete(&RefreshFamily{})
	if res.Error != nil {
		return 0, fmt.Errorf("expurgando famílias de refresh: %w", res.Error)
	}
	return res.RowsAffected, nil
}
