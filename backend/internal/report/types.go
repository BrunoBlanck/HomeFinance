// Package report produz os relatórios do mês — leitura pura (ADR-027).
//
// Relatório é CONSUMIDOR de três domínios (lançamento, categoria e, na E6,
// conta) e não pertence a nenhum deles: colocá-lo em `transaction` faria
// aquele serviço conhecer a árvore de categorias e crescer sem fim. Por isso
// este pacote declara as interfaces que precisa (Ledger, Categories) e os
// repositórios já existentes as satisfazem. Nenhuma escrita, nenhuma
// auditoria, nenhum UnitOfWork.
//
// Regras que o pacote sustenta:
//   - toda consulta é escopada pelo household_id do TOKEN (docs/SEGURANCA.md
//     §2); o id de uma categoria de outra casa nunca é sequer consultado;
//   - dinheiro é int64 em centavos (ADR-003) e o percentual trafega como
//     inteiro em pontos-base, apurado AQUI (ADR-027c) — o cliente formata,
//     nunca calcula;
//   - o "mês" é COMPETÊNCIA (ADR-023c) e transferência nunca entra (ADR-016).
package report

import (
	"context"
	"errors"

	"github.com/brunorblanck/homefinance/backend/internal/category"
)

// BasisPointsTotal é o inteiro que representa 100 % (1 bp = 0,01 %).
const BasisPointsTotal = 10_000

// Erros de domínio. Nenhum carrega detalhe interno nem dado de outra casa.
var (
	// ErrUnauthenticated — ator sem casa. O handler já barrou antes; a guarda
	// no serviço é defesa em profundidade, para que nenhuma consulta rode com
	// household_id vazio (que devolveria vazio em silêncio, ou pior).
	ErrUnauthenticated = errors.New("autenticação necessária")

	// ErrInvalidKind — natureza fora da allowlist {expense, income}. É 400 em
	// `fields.kind`, conferido ANTES de qualquer consulta.
	ErrInvalidKind = errors.New("natureza inválida")

	// errTooManyRows — a agregação devolveu mais linhas do que a taxonomia
	// permite (category.MaxPerHousehold + 1). Não é entrada do usuário: é
	// banco em estado inesperado, e a resposta é 500 genérico com a CONTAGEM
	// no log — nunca os valores.
	errTooManyRows = errors.New("linhas demais na agregação por categoria")

	// errTotalOverflow — a soma dos totais não é representável: ou não cabe
	// em int64, ou uma das parcelas é negativa (centavos e COUNT(*) nunca
	// são). Só um banco adulterado chega aqui — cada lançamento respeita
	// MaxAmountCents —, mas o relatório publica totalCents como int64 e não
	// pode mentir: 500, e nenhum número inventado (ADR-027f).
	errTotalOverflow = errors.New("total do mês fora da faixa representável")
)

// CategoryTotal é UMA linha da agregação `GROUP BY category_id`.
//
// CategoryID nulo é "sem categoria" — a linha em que category_id IS NULL, que
// os quatro dialetos agrupam numa só. É projeção de consulta, não conceito de
// negócio; o serviço a dobra na árvore de dois níveis.
type CategoryTotal struct {
	CategoryID *string
	TotalCents int64
	Count      int64
}

// Ledger é o que o relatório precisa do domínio de lançamentos. Interface no
// consumidor (ADR-027a), satisfeita por gormstore.TransactionRepository.
type Ledger interface {
	// SumByCategory soma, por categoria, os lançamentos VIVOS da casa no mês
	// de competência com a natureza pedida. Nenhum dos três filtros pode ser
	// vazio — a implementação recusa, porque `competence_month = ''` devolve
	// vazio em silêncio.
	SumByCategory(ctx context.Context, householdID, competenceMonth, kind string) ([]CategoryTotal, error)
}

// Categories é o que o relatório precisa do domínio de categorias. Já
// satisfeita por gormstore.CategoryRepository.
type Categories interface {
	// List devolve TODAS as categorias da casa (grupos e folhas). O relatório
	// sempre pede includeArchived = true: categoria arquivada continua
	// contando (PLANOS.md §4.4).
	List(ctx context.Context, householdID string, includeArchived bool) ([]category.Category, error)
}

// Actor é quem pede o relatório. Casa e usuário vêm do token, nunca da
// requisição. Sem IP: leitura não audita.
type Actor struct {
	HouseholdID string
	UserID      string
}

// ByCategoryInput é a entrada de GET /reports/by-category, como veio da
// query — a validação acontece no serviço, antes de qualquer consulta.
type ByCategoryInput struct {
	// Month é "YYYY-MM" (competência). Obrigatório.
	Month string
	// Kind é `expense` ou `income`. Vazio vira `expense`.
	Kind string
}

// --- DTOs de resposta (docs/SEGURANCA.md §4: nunca a entidade direto) -----

// CategoryReportView é a resposta de GET /reports/by-category (schema
// CategoryReport).
type CategoryReportView struct {
	Month      string `json:"month"`
	Kind       string `json:"kind"`
	TotalCents int64  `json:"totalCents"`
	Count      int64  `json:"count"`
	// Items está sempre inicializado: mês vazio é `[]`, nunca `null`.
	Items []CategoryReportGroupView `json:"items"`
}

// CategoryReportGroupView é um grupo (nível 1) ou o balde "Sem categoria"
// (schema CategoryReportGroup). No balde, CategoryID e Name são nulos,
// Children é vazio e direct* == total*.
type CategoryReportGroupView struct {
	CategoryID    *string `json:"categoryId"`
	Name          *string `json:"name"`
	ArchivedAt    *string `json:"archivedAt"`
	TotalCents    int64   `json:"totalCents"`
	Count         int64   `json:"count"`
	ShareBp       int64   `json:"shareBp"`
	DirectCents   int64   `json:"directCents"`
	DirectCount   int64   `json:"directCount"`
	DirectShareBp int64   `json:"directShareBp"`
	// Children está sempre inicializado: grupo sem filha é `[]`, nunca `null`.
	Children []CategoryReportChildView `json:"children"`
}

// CategoryReportChildView é uma subcategoria dentro do grupo (schema
// CategoryReportChild).
type CategoryReportChildView struct {
	CategoryID string  `json:"categoryId"`
	Name       string  `json:"name"`
	ArchivedAt *string `json:"archivedAt"`
	TotalCents int64   `json:"totalCents"`
	Count      int64   `json:"count"`
	ShareBp    int64   `json:"shareBp"`
}
