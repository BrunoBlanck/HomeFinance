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
