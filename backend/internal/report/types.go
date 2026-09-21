// Package report produz os relatórios do mês — leitura pura (ADR-027).
//
// Relatório é CONSUMIDOR de três domínios (lançamento, categoria e conta) e
// não pertence a nenhum deles: colocá-lo em `transaction` faria aquele
// serviço conhecer a árvore de categorias e crescer sem fim. Por isso este
// pacote declara as interfaces que precisa (Ledger, Categories, Accounts) e
// os repositórios já existentes as satisfazem. Nenhuma escrita, nenhuma
// auditoria, nenhum UnitOfWork.
//
// Regras que o pacote sustenta:
//   - toda consulta é escopada pelo household_id do TOKEN (docs/SEGURANCA.md
//     §2); o id de uma categoria ou de uma conta de outra casa nunca é sequer
//     consultado;
//   - dinheiro é int64 em centavos (ADR-003) e o percentual trafega como
//     inteiro em pontos-base, apurado AQUI (ADR-027c) — o cliente formata,
//     nunca calcula;
//   - o "mês" é COMPETÊNCIA (ADR-023c) e transferência nunca entra (ADR-016);
//   - o recorte crédito/débito (ADR-032) é resolvido em Go sobre a lista de
//     contas da PRÓPRIA casa: id de conta nunca entra no SQL, e a agregação é
//     UMA só — `credit` e `debit` são partições das mesmas linhas, de modo que
//     `total(credit) + total(debit) == total(todas)` por construção.
package report

import (
	"context"
	"errors"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
)

// BasisPointsTotal é o inteiro que representa 100 % (1 bp = 0,01 %).
const BasisPointsTotal = 10_000

// Valores do recorte `accountGroup` (ADR-032). A allowlist é FECHADA e exata:
// nada é normalizado — "CREDIT", " credit" e "credit_card" são recusados como
// vieram — e o que a resposta ecoa é a CONSTANTE, nunca a string recebida.
//
// `credit` são as contas de tipo account.KindCreditCard, arquivadas
// incluídas (arquivar não apaga o passado); `debit` é o COMPLEMENTO — toda
// conta que não é cartão —, e não uma segunda lista de tipos: assim as duas
// partições cobrem exatamente o conjunto inteiro, sem conta que caia fora das
// duas nem que caia nas duas.
const (
	AccountGroupCredit = "credit"
	AccountGroupDebit  = "debit"
)

// Erros de domínio. Nenhum carrega detalhe interno nem dado de outra casa.
var (
	// ErrUnauthenticated — ator sem casa. O handler já barrou antes; a guarda
	// no serviço é defesa em profundidade, para que nenhuma consulta rode com
	// household_id vazio (que devolveria vazio em silêncio, ou pior).
	ErrUnauthenticated = errors.New("autenticação necessária")

	// ErrInvalidKind — natureza fora da allowlist {expense, income}. É 400 em
	// `fields.kind`, conferido ANTES de qualquer consulta.
	ErrInvalidKind = errors.New("natureza inválida")

	// ErrInvalidAccountGroup — recorte fora da allowlist {credit, debit}
	// (ADR-032). É 400 em `fields.accountGroup`, conferido depois de `kind` e
	// ANTES de qualquer consulta. O valor recusado não viaja no erro: ele é
	// entrada bruta de terceiro, e não entra em resposta nem em log.
	ErrInvalidAccountGroup = errors.New("grupo de contas inválido")

	// errTooManyRows — a agregação devolveu mais linhas do que a estrutura
	// permite: `GROUP BY category_id, account_id` não passa de
	// (category.MaxPerHousehold + 1) × account.MaxPerHousehold linhas, e os
	// dois tetos são REAIS porque categoria e conta com lançamento não podem
	// ser excluídas (UsageChecker). Não é entrada do usuário: é banco em
	// estado inesperado, e a resposta é 500 genérico com a CONTAGEM no log —
	// nunca os valores.
	errTooManyRows = errors.New("linhas demais na agregação por categoria e conta")

	// errTotalOverflow — a soma dos totais não é representável: ou não cabe
	// em int64, ou uma das parcelas é negativa (centavos e COUNT(*) nunca
	// são). Só um banco adulterado chega aqui — cada lançamento respeita
	// MaxAmountCents —, mas o relatório publica totalCents como int64 e não
	// pode mentir: 500, e nenhum número inventado (ADR-027f).
	errTotalOverflow = errors.New("total do mês fora da faixa representável")
)

// CategoryAccountTotal é UMA linha da agregação `GROUP BY category_id,
// account_id` (ADR-032).
//
// CategoryID nulo é "sem categoria" — a linha em que category_id IS NULL, que
// os quatro dialetos agrupam numa só. AccountID NÃO é ponteiro:
// `transactions.account_id` é NOT NULL, todo lançamento tem conta, então aqui
// não existe a linha dos nulos que lá vira o balde. É projeção de consulta,
// não conceito de negócio; o serviço recorta por conta (quando pedido) e
// dobra na árvore de dois níveis — a mesma categoria vinda em várias linhas
// (uma por conta) é SOMADA na dobra.
type CategoryAccountTotal struct {
	CategoryID *string
	AccountID  string
	TotalCents int64
	Count      int64
}

// Ledger é o que o relatório precisa do domínio de lançamentos. Interface no
// consumidor (ADR-027a), satisfeita por gormstore.TransactionRepository.
type Ledger interface {
	// SumByCategoryAndAccount soma, por (categoria, conta), os lançamentos
	// VIVOS da casa no mês de competência com a natureza pedida. Nenhum dos
	// três filtros pode ser vazio — a implementação recusa, porque
	// `competence_month = ''` devolve vazio em silêncio. Nenhum id de conta
	// entra na consulta: o recorte crédito/débito é feito em Go pelo serviço.
	SumByCategoryAndAccount(ctx context.Context, householdID, competenceMonth, kind string) ([]CategoryAccountTotal, error)
}

// Categories é o que o relatório precisa do domínio de categorias. Já
// satisfeita por gormstore.CategoryRepository.
type Categories interface {
	// List devolve TODAS as categorias da casa (grupos e folhas). O relatório
	// sempre pede includeArchived = true: categoria arquivada continua
	// contando (PLANOS.md §4.4).
	List(ctx context.Context, householdID string, includeArchived bool) ([]category.Category, error)
}

// Accounts é o que o relatório precisa do domínio de contas: o tipo, para
// saber quais são cartão de crédito (ADR-032). Já satisfeita por
// gormstore.AccountRepository, como no painel.
type Accounts interface {
	// List devolve as contas da casa. O relatório sempre pede
	// includeArchived = true: o gasto de um cartão ARQUIVADO continua sendo
	// gasto de cartão daquele mês — arquivar não muda o passado —, e é o
	// mesmo critério do `creditCardExpenseCents` do painel, com o qual o
	// recorte `credit` tem de bater.
	//
	// A lista só é carregada quando há recorte: sob "todas as contas" o
	// caminho continua com as duas leituras de sempre.
	List(ctx context.Context, householdID string, includeArchived bool) ([]account.Account, error)
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
	// AccountGroup é `credit`, `debit` ou vazio (= todas as contas). Ortogonal
	// a Kind: toda combinação é aceita e calculada (ADR-032).
	AccountGroup string
}

// --- DTOs de resposta (docs/SEGURANCA.md §4: nunca a entidade direto) -----

// CategoryReportView é a resposta de GET /reports/by-category (schema
// CategoryReport).
type CategoryReportView struct {
	Month string `json:"month"`
	Kind  string `json:"kind"`
	// AccountGroup é o recorte pedido, ecoado — SEMPRE presente no JSON:
	// `null` é "todas as contas", senão a constante `credit`/`debit` (nunca a
	// string recebida). O contrato o lista em `required` de propósito: um
	// campo que falta obrigaria a tela a adivinhar sob que rótulo está o
	// número.
	AccountGroup *string `json:"accountGroup"`
	TotalCents   int64   `json:"totalCents"`
	Count        int64   `json:"count"`
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
