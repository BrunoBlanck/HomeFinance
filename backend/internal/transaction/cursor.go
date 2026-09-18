package transaction

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
)

// O cursor da listagem, na forma em que ele trafega.
//
// Ele é base64url de "occ=AAAA-MM-DD|id=<uuid>" — legível quando decodificado,
// opaco o bastante para ninguém tentar montar um à mão na tela.
//
// Por que NÃO é assinado (HMAC): o cursor não carrega autorização nenhuma. A
// casa vem sempre do token, e toda consulta filtra por ela; um cursor forjado
// consegue, no máximo, pedir uma página estranha DOS PRÓPRIOS dados. Assinar
// custaria uma chave para rotacionar e um modo de falha novo, sem fechar
// vazamento nenhum.
//
// O que ele precisa ser, e é: VALIDADO ESTRITAMENTE. Data civil por
// civil.Parse e id na forma de UUID — qualquer outra coisa é ErrInvalidCursor,
// que a borda traduz em 400 sem detalhe (S5 do PLANOS.md). A validação estrita
// é o que impede um "id" com aspas, vírgula ou percent-encoding de chegar à
// camada de consulta; ela vai parametrizada de qualquer jeito, mas a defesa em
// profundidade é justamente não depender disso.
const (
	cursorPrefixDate = "occ="
	cursorPrefixID   = "|id="

	// maxCursorLen espelha o teto do contrato (pattern de 1 a 256 caracteres).
	// Decodificar antes de medir seria alocar o que o cliente mandar.
	maxCursorLen = 256

	// uuidLen é o comprimento canônico de um UUID com hífens.
	uuidLen = 36
)

// EncodeCursor devolve a forma textual do cursor.
//
// Cursor de posição vazia é string vazia, e não um base64 de nada: "não há
// próxima página" é ausência, e a API publica nextCursor nulo nesse caso.
func EncodeCursor(c Cursor) string {
	if c.OccurredOn.IsZero() || c.ID == "" {
		return ""
	}
	bruto := cursorPrefixDate + c.OccurredOn.String() + cursorPrefixID + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(bruto))
}

// ParseCursor lê a forma textual e devolve a posição.
//
// Texto vazio devolve cursor zerado SEM erro: "primeira página" é a ausência do
// parâmetro, e transformar isso em erro obrigaria todo chamador a tratar o caso
// mais comum de todos.
func ParseCursor(raw string) (Cursor, error) {
	if raw == "" {
		return Cursor{}, nil
	}
	if len(raw) > maxCursorLen {
		return Cursor{}, fmt.Errorf("%w: comprimento", ErrInvalidCursor)
	}

	// RawURLEncoding (sem "=") de propósito: o cursor viaja em query string, e
	// o padding traria um caractere que precisa de escape. Decodificação
	// estrita — base64 com padding, com "+/" ou com lixo no fim é recusado
	// aqui, não "corrigido".
	decodificado, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: base64", ErrInvalidCursor)
	}

	texto := string(decodificado)
	if !strings.HasPrefix(texto, cursorPrefixDate) {
		return Cursor{}, fmt.Errorf("%w: forma", ErrInvalidCursor)
	}
	resto := strings.TrimPrefix(texto, cursorPrefixDate)

	// Corte no PRIMEIRO separador, e o id não pode conter outro: assim
	// "occ=...|id=a|id=b" não vira um id com separador dentro.
	data, id, achou := strings.Cut(resto, cursorPrefixID)
	if !achou || strings.Contains(id, cursorPrefixID) {
		return Cursor{}, fmt.Errorf("%w: forma", ErrInvalidCursor)
	}

	occurredOn, err := civil.Parse(data)
	if err != nil {
		return Cursor{}, fmt.Errorf("%w: data", ErrInvalidCursor)
	}
	if !looksLikeUUID(id) {
		return Cursor{}, fmt.Errorf("%w: id", ErrInvalidCursor)
	}
	return Cursor{OccurredOn: occurredOn, ID: id}, nil
}

// looksLikeUUID confere a FORMA canônica 8-4-4-4-12 em hexadecimal minúsculo ou
// maiúsculo.
//
// Não é uuid.Parse de propósito: aquele aceita variantes (com chaves, sem
// hífen, com urn:), e aceitar variante aqui significaria que a mesma linha tem
// mais de um cursor válido. Uma forma só.
func looksLikeUUID(s string) bool {
	if len(s) != uuidLen {
		return false
	}
	for i := range uuidLen {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		case c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
