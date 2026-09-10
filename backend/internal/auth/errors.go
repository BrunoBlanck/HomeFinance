package auth

import "errors"

// Erros de domínio do pacote auth.
//
// Cada um mapeia para exatamente um par (status, código) do contrato. Os
// erros são propositalmente POBRES em informação: ErrInvalidCode cobre código
// errado, expirado, consumido, de outro propósito, com tentativas esgotadas e
// e-mail sem código nenhum — é o que sustenta o grupo D da §3.12.
var (
	// ErrInvalidCredentials — e-mail inexistente OU senha errada (grupo E).
	ErrInvalidCredentials = errors.New("credenciais inválidas")

	// ErrInvalidCode — qualquer falha de validação de código (grupo D).
	ErrInvalidCode = errors.New("código inválido")

	// ErrEmailNotVerified — senha correta, conta ainda não confirmada.
	// Só pode ser emitido DEPOIS de a senha conferir (§3.12, nota do 403).
	ErrEmailNotVerified = errors.New("e-mail não verificado")

	// ErrInvalidSession — refresh ausente, malformado, expirado, revogado ou
	// reusado (grupo F).
	ErrInvalidSession = errors.New("sessão inválida")

	// ErrWeakPassword — senha reprovada pela política.
	ErrWeakPassword = errors.New("senha fraca")

	// ErrPasswordMismatch — hash não confere (uso interno do verificador).
	ErrPasswordMismatch = errors.New("senha não confere")

	// ErrInvalidHash — string PHC malformada ou de algoritmo desconhecido.
	ErrInvalidHash = errors.New("hash de senha inválido")

	// ErrNoHousehold — usuário verificado sem nenhuma casa. Invariante
	// quebrado; o auto-reparo do EnsureDefault deveria ter evitado.
	ErrNoHousehold = errors.New("usuário sem casa")
)

// PasswordProblem detalha por que a senha foi reprovada, para o campo
// "fields" do erro de validação. Nunca contém a senha.
type PasswordProblem struct {
	Reason string
}

// Error implementa error.
func (p PasswordProblem) Error() string { return p.Reason }

// Is faz PasswordProblem casar com ErrWeakPassword em errors.Is.
func (p PasswordProblem) Is(target error) bool { return target == ErrWeakPassword }
