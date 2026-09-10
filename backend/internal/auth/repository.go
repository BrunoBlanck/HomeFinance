package auth

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound — registro inexistente na camada de persistência do auth.
var ErrNotFound = errors.New("registro não encontrado")

// RefreshTokenRepository persiste os refresh tokens.
//
// As operações de mudança de estado são declaradas como UPDATE condicional
// devolvendo "afetou?" porque a atomicidade é a defesa: sem
// "WHERE revoked_at IS NULL" mais a checagem de linhas afetadas, duas
// requisições simultâneas com o mesmo refresh ou passam as duas (o roubo
// funciona) ou derrubam o usuário legítimo (risco 5 da §9 da spec 0001).
type RefreshTokenRepository interface {
	// CreateFamily abre a família da sessão. Roda na mesma transação do
	// primeiro token.
	CreateFamily(ctx context.Context, f *RefreshFamily) error
	// FamilyByID devolve o estado da família. É a checagem POSITIVA que
	// sustenta a rotação: ao contrário de uma linha de token, esta existe
	// desde o início da sessão, então a revogação disparada por um reúso
	// alcança inclusive o sucessor que ainda está sendo inserido.
	FamilyByID(ctx context.Context, familyID string) (*RefreshFamily, error)

	Create(ctx context.Context, t *RefreshToken) error
	// ByHash busca pelo SHA-256 hex do token apresentado.
	ByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	// Rotate marca o token como revogado apontando para o sucessor.
	// Devolve false quando NENHUMA linha foi afetada — sinal de que o token
	// já estava revogado, ou seja, REÚSO.
	Rotate(ctx context.Context, id, replacedBy string, at time.Time) (bool, error)
	// RevokeFamily mata a família E revoga todos os tokens vivos dela,
	// nessa ordem. Devolve quantos TOKENS foram revogados. É idempotente e
	// preserva o carimbo da primeira revogação.
	RevokeFamily(ctx context.Context, familyID string, at time.Time) (int64, error)
	// RevokeAllForUser revoga todas as famílias do usuário (troca de senha).
	RevokeAllForUser(ctx context.Context, userID string, at time.Time) (int64, error)
	// DeleteExpired expurga tokens expirados ou revogados há bastante tempo.
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
	// DeleteDeadFamilies expurga as famílias que já não têm nenhum token.
	DeleteDeadFamilies(ctx context.Context, before time.Time) (int64, error)
}

// VerificationCodeRepository persiste os códigos de 6 dígitos.
type VerificationCodeRepository interface {
	Create(ctx context.Context, c *VerificationCode) error
	// ActiveByEmailPurpose devolve o código vivo mais recente para o par
	// (e-mail, propósito). A consulta tem SEMPRE a mesma forma e o mesmo
	// custo, exista ou não a conta — é o que sustenta o grupo D da §3.12
	// (D5 da spec 0001).
	ActiveByEmailPurpose(ctx context.Context, email, purpose string, now time.Time) (*VerificationCode, error)
	// LastIssuedAt devolve quando o último código daquele par foi emitido,
	// para o cooldown de reenvio.
	LastIssuedAt(ctx context.Context, email, purpose string) (time.Time, bool, error)
	// ConsumeAllActive invalida todos os códigos vivos do par. Emitir um
	// código novo invalida os anteriores (docs/SEGURANCA.md §1.1).
	ConsumeAllActive(ctx context.Context, email, purpose string, at time.Time) (int64, error)
	// IncrementAttempts soma 1 ATOMICAMENTE no banco e devolve o valor já
	// atualizado. Nunca é read-modify-write em Go: duas tentativas
	// simultâneas contariam como uma só e furariam o limite de 5.
	// Devolve false quando nenhuma linha foi afetada (código consumido).
	IncrementAttempts(ctx context.Context, id string) (int, bool, error)
	// Consume marca o código como usado. Devolve false quando outra
	// requisição consumiu antes.
	Consume(ctx context.Context, id string, at time.Time) (bool, error)
	// DeleteExpired expurga códigos expirados/consumidos.
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}
