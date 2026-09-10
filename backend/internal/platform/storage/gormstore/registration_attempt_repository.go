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

// RegistrationAttemptRepository implementa auth.RegistrationAttemptRepository.
//
// Toda consulta é por placeholder "?" (docs/SEGURANCA.md §3): nenhum Raw,
// nenhum Exec, nenhuma string montada em Where/Order/Select.
type RegistrationAttemptRepository struct{ base }

// NewRegistrationAttemptRepository monta o repositório.
func NewRegistrationAttemptRepository(db *storage.DB) *RegistrationAttemptRepository {
	return &RegistrationAttemptRepository{base{db: db}}
}

var _ auth.RegistrationAttemptRepository = (*RegistrationAttemptRepository)(nil)

// Create insere a tentativa. Só hashes são gravados: nem o código de 6
// dígitos nem o token opaco existem em texto no banco.
func (r *RegistrationAttemptRepository) Create(ctx context.Context, a *auth.RegistrationAttempt) error {
	if err := r.conn(ctx).Create(toRegistrationAttemptModel(a)).Error; err != nil {
		return fmt.Errorf("inserindo tentativa de cadastro: %w", err)
	}
	return nil
}

// LiveByToken devolve a tentativa VALIDÁVEL do par (e-mail, hash do token).
//
// É esta consulta que dá ao código o escopo da tentativa: sem o token certo,
// o código que está na caixa de entrada não casa com linha nenhuma. A forma e
// o custo são os mesmos exista ou não a conta (grupo D da §3.12).
//
// O filtro por code_issued_at é o achado ALTA-2: um código gravado mas NUNCA
// ENVIADO (cooldown ou cota seguraram a mensagem) não pode ser validável,
// senão cada POST /auth/register criaria um alvo de chute novo sem gastar
// mensagem e o teto de envio por endereço deixaria de limitar quantos
// códigos existem para adivinhar.
//
// O teste da sentinela é IS NOT NULL, e não uma comparação com data-piso: o
// nulo é o único valor que todos os dialetos representam do mesmo jeito. A
// versão anterior gravava o zero de time.Time, que o MySQL recusa
// (erro 1292 com NO_ZERO_DATE) — a linha nem chegava a existir lá.
func (r *RegistrationAttemptRepository) LiveByToken(ctx context.Context, email, tokenHash string, now time.Time) (*auth.RegistrationAttempt, error) {
	var m RegistrationAttempt
	err := r.conn(ctx).
		Where("email = ? AND token_hash = ? AND consumed_at IS NULL AND expires_at > ? AND code_issued_at IS NOT NULL",
			email, tokenHash, now).
		Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, auth.ErrNotFound
		}
		return nil, fmt.Errorf("buscando tentativa de cadastro viva: %w", err)
	}
	return toRegistrationAttemptEntity(&m), nil
}

// ByToken devolve a tentativa do par (e-mail, hash do token) qualquer que
// seja o estado dela — inclusive consumida ou expirada.
func (r *RegistrationAttemptRepository) ByToken(ctx context.Context, email, tokenHash string) (*auth.RegistrationAttempt, error) {
	var m RegistrationAttempt
	err := r.conn(ctx).
		Where("email = ? AND token_hash = ?", email, tokenHash).
		Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, auth.ErrNotFound
		}
		return nil, fmt.Errorf("buscando tentativa de cadastro: %w", err)
	}
	return toRegistrationAttemptEntity(&m), nil
}

// LiveByEmail devolve até `limit` tentativas VALIDÁVEIS do endereço, da mais
// recente para a mais antiga.
//
// "Validável" exige o código EMITIDO (achado BAIXA-1 da revisão final). O
// único consumidor pergunta se o endereço tem exatamente UMA tentativa
// utilizável para decidir se pode reemitir o código no login não verificado;
// tentativa sem emissão não é utilizável por ninguém, e contá-la dava a um
// terceiro o poder de desligar essa reemissão para sempre — bastava um
// POST /auth/register a cada ~14 min para manter duas "vivas".
//
// O IS NOT NULL também deixa o ORDER BY determinístico entre dialetos: sem
// ele, a ordenação de nulos difere (Postgres põe NULLS FIRST em DESC).
func (r *RegistrationAttemptRepository) LiveByEmail(ctx context.Context, email string, now time.Time, limit int) ([]*auth.RegistrationAttempt, error) {
	if limit <= 0 {
		limit = 1
	}
	var ms []RegistrationAttempt
	err := r.conn(ctx).
		Where("email = ? AND consumed_at IS NULL AND expires_at > ? AND code_issued_at IS NOT NULL", email, now).
		Order("code_issued_at DESC, id DESC").
		Limit(limit).
		Find(&ms).Error
	if err != nil {
		return nil, fmt.Errorf("listando tentativas vivas: %w", err)
	}
	out := make([]*auth.RegistrationAttempt, 0, len(ms))
	for i := range ms {
		out = append(out, toRegistrationAttemptEntity(&ms[i]))
	}
	return out, nil
}

// LastCodeIssuedAt devolve quando o último código do endereço foi emitido.
//
// Tentativa SEM emissão é ignorada (code_issued_at nulo): ela não empurra o
// cooldown de quem nunca recebeu mensagem nenhuma — se empurrasse, um
// terceiro registrando em looping calaria o endereço para sempre. O bool
// false significa exatamente "nunca saiu mensagem para este endereço".
func (r *RegistrationAttemptRepository) LastCodeIssuedAt(ctx context.Context, email string) (time.Time, bool, error) {
	var m RegistrationAttempt
	err := r.conn(ctx).
		Where("email = ? AND code_issued_at IS NOT NULL", email).
		Order("code_issued_at DESC, id DESC").
		Take(&m).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("buscando última emissão de código: %w", err)
	}
	if m.CodeIssuedAt == nil {
		// Inalcançável pelo WHERE acima; fica como rede para o dia em que
		// alguém afrouxar a consulta.
		return time.Time{}, false, nil
	}
	return *m.CodeIssuedAt, true, nil
}

// RotateCode troca o código da tentativa e ZERA o contador de tentativas.
//
// Zerar é obrigatório e não é afrouxamento: o limite de 5 é POR CÓDIGO
// (docs/SEGURANCA.md §1.1). Se o contador atravessasse a rotação, cinco
// chutes errados de um terceiro trancariam o cadastro do dono do endereço
// para sempre. Quem segura o abuso aqui é o cooldown mais a cota de envio
// por endereço, não o contador.
func (r *RegistrationAttemptRepository) RotateCode(ctx context.Context, id, codeHash string, issuedAt, expiresAt time.Time) (bool, error) {
	res := r.conn(ctx).Model(&RegistrationAttempt{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"code_hash":      codeHash,
			"code_issued_at": issuedAt,
			"expires_at":     expiresAt,
			"consumed_at":    nil,
			"attempts":       0,
			"updated_at":     issuedAt,
		})
	if res.Error != nil {
		return false, fmt.Errorf("rotacionando código da tentativa: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// IncrementAttempts soma 1 no contador DENTRO do banco e devolve o valor já
// atualizado.
//
// gorm.Expr recebe uma expressão CONSTANTE do código ("attempts + 1"), sem
// nenhuma entrada do usuário — é a forma parametrizada de fazer incremento
// atômico no GORM, não interpolação de SQL.
func (r *RegistrationAttemptRepository) IncrementAttempts(ctx context.Context, id string) (int, bool, error) {
	conn := r.conn(ctx)

	res := conn.Model(&RegistrationAttempt{}).
		Where("id = ? AND consumed_at IS NULL", id).
		Update("attempts", gorm.Expr("attempts + 1"))
	if res.Error != nil {
		return 0, false, fmt.Errorf("incrementando tentativas do cadastro: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		return 0, false, nil
	}

	var m RegistrationAttempt
	if err := conn.Where("id = ?", id).Take(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("relendo tentativas do cadastro: %w", err)
	}
	return m.Attempts, true, nil
}

// Consume marca a tentativa como usada, uma única vez.
//
// O "AND consumed_at IS NULL" com RowsAffected == 1 garante o uso único mesmo
// sob corrida: duas requisições com o mesmo par token+código, só uma vence.
func (r *RegistrationAttemptRepository) Consume(ctx context.Context, id string, at time.Time) (bool, error) {
	res := r.conn(ctx).Model(&RegistrationAttempt{}).
		Where("id = ? AND consumed_at IS NULL", id).
		Updates(map[string]any{"consumed_at": at, "updated_at": at})
	if res.Error != nil {
		return false, fmt.Errorf("consumindo tentativa de cadastro: %w", res.Error)
	}
	return res.RowsAffected == 1, nil
}

// ConsumeAllForEmail invalida todas as tentativas vivas do endereço.
func (r *RegistrationAttemptRepository) ConsumeAllForEmail(ctx context.Context, email string, at time.Time) (int64, error) {
	res := r.conn(ctx).Model(&RegistrationAttempt{}).
		Where("email = ? AND consumed_at IS NULL", email).
		Updates(map[string]any{"consumed_at": at, "updated_at": at})
	if res.Error != nil {
		return 0, fmt.Errorf("invalidando tentativas de cadastro: %w", res.Error)
	}
	return res.RowsAffected, nil
}

// DeleteExpired expurga tentativas mortas antes do corte.
func (r *RegistrationAttemptRepository) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res := r.conn(ctx).
		Where("expires_at < ? AND (consumed_at IS NULL OR consumed_at < ?)", before, before).
		Delete(&RegistrationAttempt{})
	if res.Error != nil {
		return 0, fmt.Errorf("expurgando tentativas de cadastro: %w", res.Error)
	}
	return res.RowsAffected, nil
}
