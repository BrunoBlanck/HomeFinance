// Package id gera identificadores públicos das entidades.
//
// Pacote-FOLHA (D14): só depende da stdlib e de github.com/google/uuid.
// Usamos UUID v7 (RFC 9562) porque o prefixo temporal mantém as chaves
// primárias aproximadamente ordenadas por criação — o que preserva a
// localidade dos índices B-tree nos quatro dialetos suportados — sem revelar
// contagem de registros como um inteiro sequencial revelaria.
package id

import (
	"fmt"
	"sync/atomic"

	"github.com/google/uuid"
)

// Generator permite injetar IDs determinísticos em teste.
type Generator func() string

var degradedCounter atomic.Uint64

// New devolve um UUID v7 em formato canônico (36 caracteres).
//
// O ID é sempre gerado no service, nunca no repositório e nunca em hook do
// GORM (§4 da spec 0001), para que o teste do service consiga prever o valor.
func New() string {
	if v, err := uuid.NewV7(); err == nil {
		return v.String()
	}
	// Caminho praticamente inalcançável: desde o Go 1.24 crypto/rand não
	// devolve erro. Ainda assim não usamos panic (proibido no caminho de
	// request) nem devolvemos string vazia, que viraria chave primária
	// inválida.
	if v, err := uuid.NewRandom(); err == nil {
		return v.String()
	}
	return fmt.Sprintf("00000000-0000-7000-8000-%012x", degradedCounter.Add(1))
}

// Fixed devolve um Generator que sempre entrega os IDs informados, em ordem,
// repetindo o último. Existe para testes.
func Fixed(ids ...string) Generator {
	var i atomic.Int64
	return func() string {
		if len(ids) == 0 {
			return New()
		}
		ultimo := int64(len(ids)) - 1
		n := i.Add(1) - 1
		if n > ultimo {
			n = ultimo
		}
		return ids[n]
	}
}

// uuidLen é o comprimento da forma canônica 8-4-4-4-12 com hífens.
const uuidLen = 36

// IsCanonical diz se `s` tem a FORMA canônica de um identificador do projeto:
// 8-4-4-4-12 em hexadecimal, 36 caracteres, com os hífens nas posições certas.
//
// # Por que a forma importa na BORDA
//
// A identidade de um id é conferida em SQL (`WHERE id = ?`), e a semântica
// dessa igualdade vem da COLLATION da coluna — que não é a mesma nos quatro
// dialetos. As colunas de id são `varchar(36)` sem collation declarada, então
// no MySQL 8 valem `utf8mb4_0900_ai_ci` (ignora caixa e acento) e no MSSQL
// `CI_AS` com padding ANSI (ignora espaço à direita). Em SQLite e PostgreSQL o
// `=` é sensível a caixa e a espaço. Ou seja: `"<uuid>   "` e `"<UUID>"`
// encontram a linha em dois dialetos e não encontram nos outros dois.
//
// Esta função fecha a metade do problema que a FORMA resolve — espaço,
// controle, aspas, percent-encoding, id mais longo que a coluna. A outra
// metade (caixa trocada, que é forma canônica válida) só se fecha gravando o
// `.ID` que veio do banco em vez da string do cliente; é a regra de
// canonização da borda de escrita, e as duas defesas andam juntas.
//
// Aceita hexadecimal em maiúscula e em minúscula de propósito: é a mesma
// semântica de `looksLikeUUID` do cursor de paginação, e não é aqui que a
// diferença de caixa se resolve. Não é `uuid.Parse` pelo mesmo motivo daquele:
// o parser da biblioteca aceita variantes (com chaves, sem hífen, com `urn:`),
// e aceitar variante significaria que a mesma linha tem mais de um id válido.
// Uma forma só.
func IsCanonical(s string) bool {
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
