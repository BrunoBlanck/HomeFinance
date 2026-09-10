// Package auth implementa registro, verificação de e-mail por código de 6
// dígitos, login, rotação de refresh token, logout e recuperação de senha.
//
// Toda regra normativa deste pacote está em docs/SEGURANCA.md §1 e §1.1 —
// especialmente: código gerado com crypto/rand sem viés, guardado apenas como
// HMAC-SHA-256 com pepper, comparado em tempo constante, de uso único, com
// expiração curta e limite de tentativas; e respostas que nunca revelam se um
// e-mail existe.
package auth

import (
	"context"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/user"
)

// Propósitos de código de verificação. O propósito FAZ PARTE do que é
// validado: um código de verificação de e-mail nunca serve para trocar senha
// (docs/SEGURANCA.md §1.1).
const (
	PurposeEmailVerification = "email_verification"
	PurposePasswordReset     = "password_reset"
)

// ValidPurpose informa se o propósito é conhecido.
func ValidPurpose(p string) bool {
	return p == PurposeEmailVerification || p == PurposePasswordReset
}

// RefreshFamily é o estado de uma família de refresh tokens — ou seja, de
// UMA sessão, do login até o logout, atravessando todas as rotações.
//
// Por que existe (correção do achado ALTA-1 da revisão de segurança): a
// revogação por "UPDATE refresh_tokens WHERE family_id = ?" só alcança as
// linhas JÁ GRAVADAS. Numa rotação, o sucessor é inserido depois de o
// antecessor ser revogado; se um reúso dispara a revogação nesse intervalo,
// o sucessor nasce fora do alcance do UPDATE e sobrevive — exatamente a
// sessão do ladrão. Pôr rotação e inserção na mesma transação NÃO resolve,
// porque a linha nova continua invisível para o UPDATE alheio até o commit.
//
// A família dá um alvo único, que existe desde o começo da sessão e não
// depende de nenhuma linha futura. O Refresh consulta esse estado ANTES de
// emitir (checagem positiva) e reconfere na gravação do sucessor.
type RefreshFamily struct {
	ID        string
	UserID    string
	RevokedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Active informa se a família ainda está viva.
func (f *RefreshFamily) Active() bool { return f != nil && f.RevokedAt == nil }

// RefreshToken é o token opaco de sessão longa.
//
// Só o HASH é guardado (SHA-256 hex). O valor em claro existe apenas no
// cookie do cliente e na memória durante a requisição que o emitiu.
type RefreshToken struct {
	ID         string
	UserID     string
	FamilyID   string
	TokenHash  string
	ExpiresAt  time.Time
	RevokedAt  *time.Time
	ReplacedBy *string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Active informa se o token pode ser usado neste instante.
func (t *RefreshToken) Active(now time.Time) bool {
	return t != nil && t.RevokedAt == nil && now.Before(t.ExpiresAt)
}

// VerificationCode é o registro de um código de 6 dígitos.
//
// CodeHash guarda HMAC-SHA-256(pepper, purpose|email|code). O código em texto
// NUNCA é persistido, logado ou devolvido.
type VerificationCode struct {
	ID         string
	Email      string
	Purpose    string
	UserID     *string
	CodeHash   string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	Attempts   int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// UnitOfWork executa uma função dentro de uma transação de banco.
//
// ADR-013: o schema não tem chave estrangeira física. Toda escrita
// multi-tabela (registro, verificação, reset de senha) TEM de passar por
// aqui, senão um erro no meio deixa casa sem vínculo — e o banco não vai
// barrar.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Mailer é o que o pacote auth precisa do envio de e-mail.
//
// Os métodos NÃO recebem contexto e NÃO devolvem erro de propósito: o envio é
// sempre assíncrono (D7 da spec 0001). SMTP dentro do request seria a maior
// fonte de variação de tempo do fluxo "esqueci a senha" e, com ela, o
// vazamento de existência de conta que a §3.12 proíbe.
//
// EnqueueVerificationCode NÃO recebe nome, e isso é uma decisão de segurança
// (achado ALTA-2 da revisão): ela é a única mensagem de PRIMEIRO CONTATO —
// vai para um endereço que ninguém provou possuir, a pedido de quem quer
// que tenha chamado POST /auth/register. Se o nome do corpo da requisição
// chegasse até aqui, qualquer um mandaria texto escolhido, assinado com o
// SPF/DKIM/DMARC do domínio real, para o endereço que quisesse. Os outros
// dois métodos só são disparados para conta JÁ VERIFICADA, onde o nome
// pertence a quem provou posse da caixa.
type Mailer interface {
	EnqueueVerificationCode(to, code string, ttl time.Duration)
	EnqueuePasswordResetCode(to, name, code string, ttl time.Duration)
	EnqueueAccountExistsNotice(to, name string)
}

// RateLimiter é o limitador por conta usado dentro do handler (a chave só é
// conhecida depois de decodificar o corpo).
type RateLimiter interface {
	Allow(key string) (bool, time.Duration)
}

// SessionView monta o corpo de /me devolvido por login, verificação e
// refresh (§3.4, §3.6 e §3.7 da spec 0001).
type SessionView interface {
	Me(ctx context.Context, ident session.Identity) (*user.MeView, error)
}

// Households é o que o auth precisa do domínio household.
type Households interface {
	EnsureDefault(ctx context.Context, userID, userName string) (household.Summary, error)
	Active(ctx context.Context, userID string) (household.Summary, error)
}

// Clock permite congelar o tempo em teste.
type Clock func() time.Time
