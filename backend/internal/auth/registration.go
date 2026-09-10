package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// RegistrationTokenBytes é a entropia do registrationToken: 256 bits, bem
// acima dos 128 exigidos por docs/SEGURANCA.md §1.
const RegistrationTokenBytes = 32

// registrationTokenHexLen é o tamanho do token em hexadecimal.
const registrationTokenHexLen = RegistrationTokenBytes * 2

// RegistrationAttempt é UMA tentativa de cadastro: as credenciais que quem
// pediu escolheu, o código de 6 dígitos que foi para a caixa do endereço e o
// segredo que amarra os dois.
//
// POR QUE ESTA ENTIDADE EXISTE (achado ALTA "pre-hijacking" da re-revisão).
//
// Enquanto o cadastro pendente era UMA linha em users, o código que chegava
// na caixa da vítima não estava ligado a quem tinha escolhido a senha. Isso
// torna o ataque insolúvel por política de sobrescrita:
//
//	último escreve vence -> o atacante cadastra por último; o código que a
//	    vítima recebe está amarrado à senha DELE, e ela ativa a conta do
//	    atacante ao usar o próprio código;
//	primeiro escreve vence -> o atacante cadastra primeiro e o cadastro da
//	    vítima vira no-op; ela ativa a senha dele do mesmo jeito;
//	bloquear e-mail com cadastro pendente -> negação permanente de cadastro,
//	    porque qualquer um pode ocupar um endereço alheio.
//
// A raiz é o VÍNCULO QUE FALTAVA. Aqui cada pedido de cadastro cria a sua
// própria tentativa, com as suas credenciais e o seu código, atada a um
// token opaco que só existe no navegador de quem pediu. O código do atacante
// até chega à caixa da vítima, mas ela não tem o token dele — então aquele
// código é inútil na mão dela; e o atacante tem o token dele, mas nunca
// recebe o código. Nenhum dos dois fecha o par; só a vítima fecha o dela.
//
// Disciplina de guarda (docs/SEGURANCA.md §1 e §1.1):
//   - TokenHash é o SHA-256 do token opaco — o valor em claro só existe na
//     resposta que o emitiu e no cliente, nunca no banco e nunca em log;
//   - CodeHash é o HMAC-SHA-256 com pepper de (purpose|email|code) — mesma
//     regra do VerificationCode;
//   - PasswordHash é Argon2id (PHC), igual ao de users.
type RegistrationAttempt struct {
	ID    string
	Email string
	// UserID é a linha de users que RESERVA o endereço. Fica na tentativa
	// só para a auditoria ter a quem apontar sem uma consulta extra no
	// caminho de falha (que viraria diferença de tempo entre conta existente
	// e inexistente).
	UserID       string
	Name         string
	PasswordHash string
	TokenHash    string
	CodeHash     string
	// CodeIssuedAt é quando o código ATUAL desta tentativa foi emitido. É o
	// que alimenta o cooldown por endereço; CreatedAt não serve, porque a
	// rotação de código mantém a tentativa (e o token) de pé.
	//
	// PONTEIRO, e NULO significa "nenhuma mensagem saiu" (revisão de
	// 09/09/2026). A sentinela anterior era o zero de time.Time, e ela não é
	// portátil: o driver do MySQL serializa esse zero como
	// '0000-00-00 00:00:00', que o sql_mode padrão do MySQL 8 recusa com o
	// erro 1292 (STRICT_TRANS_TABLES + NO_ZERO_DATE). O INSERT falhava, a
	// tentativa não nascia, e com ela caíam o invariante do ADR-014 ("a
	// tentativa é gravada MESMO quando o cooldown ou a cota seguram a
	// mensagem") e a uniformidade do grupo A da §3.12 — endereço já
	// verificado respondia 202 e endereço livre respondia 500.
	//
	// NULO é a representação honesta de "não aconteceu": nenhum dialeto
	// precisa interpretar data mágica, e o filtro vira IS NOT NULL.
	CodeIssuedAt *time.Time
	ExpiresAt    time.Time
	ConsumedAt   *time.Time
	Attempts     int
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Live informa se a tentativa ainda está de pé: não consumida e não expirada.
func (a *RegistrationAttempt) Live(now time.Time) bool {
	return a != nil && a.ConsumedAt == nil && now.Before(a.ExpiresAt)
}

// CodeIssued informa se o código ATUAL desta tentativa chegou a ser
// efetivamente ENTREGUE por e-mail.
//
// É metade da resposta a "esse código pode ser validado?" — a outra metade é
// Live. Uma tentativa nasce com código gravado mesmo quando o cooldown ou a
// cota seguram a mensagem (ver openAttempt), e um código que ninguém recebeu
// não pode ser chutável: senão a cota de mensagens por endereço deixa de
// limitar quantos códigos existem para adivinhar, e os 20 bits de entropia
// dos 6 dígitos perdem o freio de que dependem (docs/SEGURANCA.md §1.1).
//
// A checagem é do PONTEIRO: nulo = nunca emitido. Nenhuma data mágica.
func (a *RegistrationAttempt) CodeIssued() bool {
	return a != nil && a.CodeIssuedAt != nil
}

// RegistrationAttemptRepository persiste as tentativas de cadastro.
//
// Implementação em internal/platform/storage/gormstore (ADR-008).
type RegistrationAttemptRepository interface {
	Create(ctx context.Context, a *RegistrationAttempt) error

	// LiveByToken devolve a tentativa VALIDÁVEL do par (e-mail, hash do
	// token): não consumida, não expirada E com o código EFETIVAMENTE
	// EMITIDO (code_issued_at IS NOT NULL).
	//
	// É a consulta que sustenta o escopo do código: um código que não
	// pertence àquela tentativa simplesmente NÃO EXISTE para a validação —
	// e um código que nunca foi entregue por e-mail também não (ver
	// CodeIssued).
	//
	// A forma e o custo da consulta são os mesmos exista ou não a conta —
	// é o que mantém o grupo D da §3.12 de pé.
	LiveByToken(ctx context.Context, email, tokenHash string, now time.Time) (*RegistrationAttempt, error)

	// ByToken devolve a tentativa do par (e-mail, hash do token)
	// INDEPENDENTE de estar consumida ou expirada. Serve ao reenvio: quem
	// queimou as 5 tentativas de um código continua sendo o dono daquela
	// tentativa e pode pedir outro código — é o que impede que errar o
	// código cinco vezes vire bloqueio permanente do cadastro.
	ByToken(ctx context.Context, email, tokenHash string) (*RegistrationAttempt, error)

	// LiveByEmail devolve até `limit` tentativas VALIDÁVEIS do endereço, da
	// mais recente para a mais antiga. O chamador usa limit=2 só para saber
	// se a tentativa é ÚNICA (endereço não disputado).
	//
	// "Validável" inclui o código EMITIDO (achado BAIXA-1 da revisão final):
	// uma tentativa sem emissão não serve para validar nada, então contá-la
	// como viva só desligava a reemissão do login não verificado para o
	// dono — um `register` a cada ~14 min mantinha duas "vivas" para sempre.
	LiveByEmail(ctx context.Context, email string, now time.Time, limit int) ([]*RegistrationAttempt, error)

	// LastCodeIssuedAt devolve quando o último código de verificação saiu
	// para o endereço, para o cooldown. Tentativas SEM emissão são ignoradas
	// (nulas), e o bool false significa "nunca saiu mensagem para este
	// endereço" — não empurrar o cooldown de quem nunca recebeu nada é o que
	// impede o laço "atacante registra em looping" de calar o endereço.
	LastCodeIssuedAt(ctx context.Context, email string) (time.Time, bool, error)

	// RotateCode troca o código da tentativa e ZERA o contador de
	// tentativas, mantendo o token. Devolve false quando nenhuma linha foi
	// afetada. Ressuscita tentativa consumida/expirada de propósito: é o
	// caminho de quem pediu outro código.
	RotateCode(ctx context.Context, id, codeHash string, issuedAt, expiresAt time.Time) (bool, error)

	// IncrementAttempts soma 1 ATOMICAMENTE no banco e devolve o valor já
	// atualizado. Nunca é read-modify-write em Go: duas tentativas
	// simultâneas contariam como uma só e furariam o limite de 5.
	IncrementAttempts(ctx context.Context, id string) (int, bool, error)

	// Consume marca a tentativa como usada, uma única vez.
	Consume(ctx context.Context, id string, at time.Time) (bool, error)

	// ConsumeAllForEmail invalida TODAS as tentativas vivas do endereço.
	// É o que a ativação faz: confirmado o e-mail, nenhuma outra tentativa
	// pendente pode continuar de pé.
	ConsumeAllForEmail(ctx context.Context, email string, at time.Time) (int64, error)

	// DeleteExpired expurga tentativas mortas antes do corte.
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

// NewRegistrationToken sorteia o token opaco e devolve (valor em claro, hash).
//
// Hexadecimal, e não base64url, por uma razão concreta de teste e de log: o
// alfabeto [0-9a-f] não consegue formar as palavras que as asserções de
// vazamento procuram numa resposta ("sql", "gorm", "panic"), então o token
// nunca provoca falso positivo nem falso negativo nesses testes.
func NewRegistrationToken() (plain, hash string, err error) {
	buf := make([]byte, RegistrationTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("sorteando token de cadastro: %w", err)
	}
	plain = hex.EncodeToString(buf)
	return plain, HashRegistrationToken(plain), nil
}

// HashRegistrationToken devolve o SHA-256 hex do token apresentado.
//
// SHA-256 puro basta (mesma justificativa do refresh, D6 da spec 0001): com
// 256 bits de entropia não há espaço de busca para força bruta, então um
// segredo a mais não compraria nada.
func HashRegistrationToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// ValidRegistrationTokenFormat aceita exatamente 64 dígitos hexadecimais
// minúsculos. É checagem de BORDA: barra lixo antes de virar consulta.
func ValidRegistrationTokenFormat(token string) bool {
	if len(token) != registrationTokenHexLen {
		return false
	}
	for i := range len(token) {
		c := token[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
