package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

// CodeLength é o tamanho do código enviado por e-mail (ADR-009).
const CodeLength = 6

// codeSpace é o número de valores possíveis: 000000..999999.
var codeSpace = big.NewInt(1_000_000)

// OTP gera e confere os códigos de 6 dígitos.
//
// Regras normativas implementadas aqui (docs/SEGURANCA.md §1.1):
//   - geração com crypto/rand, uniforme, SEM viés de módulo;
//   - código tratado como STRING de 6 caracteres — zeros à esquerda contam;
//   - guarda apenas HMAC-SHA-256(pepper, purpose|email|code);
//   - comparação em tempo constante com hmac.Equal.
//
// Por que HMAC com pepper e não SHA-256 puro: 6 dígitos são ~20 bits. Um
// vazamento do banco permitiria varrer os 10^6 hashes em segundos. O pepper
// mora em variável de ambiente, fora do banco, e sem ele o ataque offline não
// existe. Amarrar purpose e email ao HMAC impede replay do código de
// verificação de e-mail no fluxo de troca de senha.
type OTP struct {
	pepper []byte
}

// NewOTP monta o serviço de código. O pepper precisa de pelo menos 32 bytes
// (validado também no boot, em config).
func NewOTP(pepper string) (*OTP, error) {
	if len(pepper) < 32 {
		return nil, fmt.Errorf("pepper do OTP precisa de pelo menos 32 bytes")
	}
	return &OTP{pepper: []byte(pepper)}, nil
}

// GenerateCode devolve um código uniformemente distribuído em 000000..999999.
//
// crypto/rand.Int faz a rejeição de amostras enviesadas internamente; é
// justamente por isso que NÃO usamos "n % 1000000" sobre bytes aleatórios,
// que concentraria probabilidade nos valores baixos.
func (o *OTP) GenerateCode() (string, error) {
	n, err := rand.Int(rand.Reader, codeSpace)
	if err != nil {
		return "", fmt.Errorf("gerando código: %w", err)
	}
	// %06d preserva zeros à esquerda: "000123" é um código válido e
	// diferente de "123".
	return fmt.Sprintf("%0*d", CodeLength, n), nil
}

// Hash devolve o HMAC-SHA-256 hex de (purpose, email, code).
//
// Os campos são separados por 0x00, que não pode ocorrer em nenhum deles.
// Sem separador, ("ab","c") e ("a","bc") colidiriam.
func (o *OTP) Hash(purpose, email, code string) string {
	mac := hmac.New(sha256.New, o.pepper)
	mac.Write([]byte(purpose))
	mac.Write([]byte{0})
	mac.Write([]byte(strings.ToLower(email)))
	mac.Write([]byte{0})
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify compara em tempo constante.
func (o *OTP) Verify(purpose, email, code, storedHash string) bool {
	if !ValidCodeFormat(code) || storedHash == "" {
		return false
	}
	return hmac.Equal([]byte(o.Hash(purpose, email, code)), []byte(storedHash))
}

// AccountKey devolve a chave de rate limit "por conta".
//
// §7 da spec 0001: o e-mail em claro NUNCA entra no mapa do limitador. Um
// dump de memória (ou um endpoint de diagnóstico mal feito) não pode virar
// lista de e-mails cadastrados.
func (o *OTP) AccountKey(scope, email string) string {
	mac := hmac.New(sha256.New, o.pepper)
	mac.Write([]byte("ratelimit"))
	mac.Write([]byte{0})
	mac.Write([]byte(scope))
	mac.Write([]byte{0})
	mac.Write([]byte(strings.ToLower(strings.TrimSpace(email))))
	return hex.EncodeToString(mac.Sum(nil))
}

// ValidCodeFormat aceita exatamente 6 dígitos ASCII.
func ValidCodeFormat(code string) bool {
	if len(code) != CodeLength {
		return false
	}
	for i := range CodeLength {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}
