// Package cardstatement modela a fatura de cartão de crédito: o período que
// fecha numa data e vence em outra.
//
// Por que existe (e por que é pequeno): a fatura é o que separa CAIXA de
// COMPETÊNCIA. A compra acontece no dia 28/01, entra na fatura que fecha em
// 02/02 e vence em 10/02 — e o usuário espera vê-la no mês de fevereiro. Sem
// esta tabela, ou o gasto aparece no mês errado, ou cada lançamento carrega
// uma cópia solta das datas da fatura, que passa a divergir na primeira
// correção.
//
// Sobre o que NÃO tem aqui (disciplina do ADR-017): total, valor pago e
// status são DERIVADOS dos lançamentos ligados à fatura, nunca colunas.
// Coluna materializada de dinheiro é a origem clássica do número errado — um
// caminho de escrita esquecido a corrompe, e em silêncio.
package cardstatement

import (
	"errors"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
)

// Origem da fatura: criada à mão ou deduzida de um arquivo importado.
const (
	SourceManual = "manual"
	SourceImport = "import"
)

// Erros de domínio.
var (
	// ErrNotFound — fatura inexistente ou de outra casa (o mesmo erro, S1).
	ErrNotFound = errors.New("fatura não encontrada")

	// ErrDuplicate — duas escritas simultâneas tentaram criar a MESMA fatura
	// (mesma casa, mesma conta, mesma competência) e o índice único recusou a
	// segunda. É erro tipado para que o serviço possa reler e seguir, em vez
	// de devolver 500 por uma corrida cujo resultado já está correto.
	ErrDuplicate = errors.New("fatura já existe para esta competência")
)

// Statement é a fatura.
type Statement struct {
	ID          string
	HouseholdID string
	AccountID   string

	// CompetenceMonth é "YYYY-MM" e é o mês do VENCIMENTO — é ele que o
	// usuário chama de "a fatura de fevereiro", mesmo que o período coberto
	// comece em janeiro.
	CompetenceMonth string

	// ClosingDate e DueDate são datas civis (D3 da spec 0003): dia do
	// calendário, sem hora e sem fuso.
	ClosingDate civil.Date
	DueDate     civil.Date

	Source    string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}
