// Package dashboard responde a primeira pergunta de quem abre o app — "como
// foi este mês?" — numa faixa de três números: investido (líquido), receita e
// gasto no cartão de crédito. Leitura pura (spec 0008, ADR-031).
//
// # Por que um pacote próprio, no molde do internal/report
//
// Como o relatório (ADR-027a) e o investimento (ADR-029), o painel é
// CONSUMIDOR de três domínios — lançamento, categoria e conta — e não pertence
// a nenhum deles. Pôr as rotas em `transaction` faria aquele serviço, que já
// conhece conta, fatura, deduplicação, transferência e importação, aprender
// também a árvore de categorias e, na E4 completa, as contas fixas; pôr em
// `report` arrastaria conta fixa para dentro do relatório; pôr em `investment`
// colocaria receita e cartão num pacote que, além disso, ESCREVE.
//
// Por isso este pacote declara as interfaces de que precisa (Ledger,
// Categories, Accounts) e os repositórios que já existem as satisfazem, sem
// uma linha nova neles — a consulta agregada mora em arquivo próprio do
// gormstore, como o SumByCategory do relatório, que é método do
// TransactionRepository e NÃO está na interface transaction.Repository.
//
// O painel cresce por AQUI (PLANOS.md §7.4): saldo por conta, próximos
// vencimentos, top categorias e comparação com o mês anterior entram como
// campos novos no MESMO schema e métodos novos nas interfaces DESTE pacote —
// uma rota, um pedido de rede, uma tela.
//
// # Leitura pura
//
// Nenhuma escrita, nenhum UnitOfWork, nenhuma auditoria: não há o que desfazer
// nem o que rastrear. É também por isso que Actor não tem IP — auditoria é que
// precisa dele, e aqui não há auditoria.
//
// # Regras que o pacote sustenta
//
//   - toda consulta é escopada pelo household_id do TOKEN (docs/SEGURANCA.md
//     §2; BOLA é o risco nº 1). `month` é o único parâmetro lido da
//     requisição, e um `householdId=` na query é ignorado sem efeito. Os ids
//     de categoria e de conta que o serviço usa saíram de listas da PRÓPRIA
//     casa — id de outra casa não tem como chegar ao WHERE;
//   - dinheiro é int64 em CENTAVOS (ADR-003) — nenhum float em nenhuma camada,
//     e nenhuma subtração feita no cliente (ADR-027c): o número nasce no
//     servidor, o cliente formata;
//   - o "mês" é COMPETÊNCIA (ADR-023c), nunca caixa, e nada é normalizado: um
//     mês malformado é recusado como veio;
//   - transferência interna não entra em número nenhum (ADR-016), e isso é
//     ESTRUTURAL: a consulta só lê `kind IN (income, expense)`, então a perna
//     que cai no cartão (`transfer_in`) nunca chega a ser somada;
//   - conjunto de categorias VAZIO nunca vira consulta com `IN ()` (ADR-029f):
//     sem categoria de investimento, as colunas condicionais simplesmente não
//     entram na projeção;
//   - o predicado de investimento é UM só — transaction.MarcadaComoInvestimento
//     (ADR-029d), sobre a taxonomia da casa com as ARQUIVADAS INCLUÍDAS. Este
//     pacote não escreve a sua própria versão dele: duas definições divergem.
//
// # investmentNetCents é o ÚNICO número com sinal, e isso é deliberado
//
// Todo campo de dinheiro do contrato declara `minimum: 0`. O líquido do painel
// não (ADR-031c): ele é aportes − resgates da competência, e o mês em que se
// resgatou mais do que se aportou tem líquido NEGATIVO — essa é a resposta
// certa, não um erro a consertar.
//
// Ele é também a única subtração do pacote que pode dar negativo, e é sempre
// representável: com `a ≥ 0` e `b ≥ 0`, `a − b ∈ [−b, a] ⊂ int64`. As outras
// subtrações (receita sem os resgates, cartão sem os aportes e as duas
// contagens correspondentes) são `total − marcado` e só acontecem DEPOIS de o
// serviço VERIFICAR que `0 ≤ marcado ≤ total` — verificar, nunca confiar
// (ADR-029 j.1). Violação dessa desigualdade falha FECHADA: erro genérico,
// contagens no log, nenhum número inventado.
//
// ⚠️ O schema InvestmentTotals de GET /investments continua SEM líquido (spec
// 0006 §2.2, ADR-031d): lá a pergunta é "quanto entrou e quanto saiu", aqui é
// "quanto ficou investido neste mês". Duas perguntas, dois números, nenhum dos
// dois calculado no cliente.
package dashboard

import (
	"context"
	"errors"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
)

// Erros de domínio. Nenhum carrega detalhe interno nem dado de outra casa.
var (
	// ErrUnauthenticated — ator sem casa. O handler já barrou antes; a guarda
	// no serviço é defesa em profundidade, para que nenhuma consulta rode com
	// household_id vazio (que devolveria o mês inteiro zerado em silêncio, ou
	// pior).
	ErrUnauthenticated = errors.New("autenticação necessária")

	// errTooManyRows — a agregação devolveu mais linhas do que
	// `GROUP BY kind, account_id` permite: o teto é 2 × account.MaxPerHousehold
	// (dois kinds × as contas da casa), e ele é REAL porque conta com
	// lançamento não pode ser excluída (o UsageChecker a barra), então toda
	// conta que aparece na agregação é viva e da casa.
	//
	// Não é entrada do usuário: é banco em estado inesperado. Falha FECHADA —
	// 500 genérico com a CONTAGEM no log, nunca os valores.
	errTooManyRows = errors.New("linhas demais na agregação do painel")

	// errTotalsOutOfRange — uma linha agregada violou a desigualdade que o
	// serviço VERIFICA antes de publicar (`0 ≤ marcado ≤ total`, nos centavos
	// e nas contagens), ou uma soma não coube em int64.
	//
	// Só um banco adulterado chega aqui — o caminho de escrita garante
	// amount_cents ≥ 0 e COUNT(*) nunca é negativo —, mas o painel publica
	// `minimum: 0` em quatro dos seus números e não pode mentir. Falha
	// FECHADA: 500 genérico, contagens no log, nenhum número inventado.
	// Clampar em silêncio seria pior — esconderia a corrupção E ainda
	// publicaria um número errado (ADR-029 j.1).
	errTotalsOutOfRange = errors.New("totais do painel fora da faixa representável")
)

// --- Interfaces declaradas no CONSUMIDOR (ADR-027a) ------------------------

// KindAccountTotals é UMA linha da agregação `GROUP BY kind, account_id`.
//
// É projeção de consulta, não conceito de negócio: quem dobra os três números
// da faixa é o serviço. A conta está na CHAVE, e não na projeção (ADR-031b),
// por dois motivos medidos: uma coluna condicional por conta estouraria o
// orçamento de parâmetros do dialeto mais estreito, e excluir os marcados do
// cartão por `category_id NOT IN (…)` perderia, em silêncio, toda despesa de
// cartão SEM categoria — o estado normal logo depois de importar uma fatura.
type KindAccountTotals struct {
	// Kind é `income` ou `expense`. Transferência não aparece porque a
	// consulta não a lê (ADR-016).
	Kind string

	// AccountID é a conta da linha. A interseção com o conjunto de cartões é
	// feita em Go, sobre a lista de contas da própria casa: id de conta nunca
	// entra no SQL.
	AccountID string

	// Count e TotalCents são a linha inteira: COUNT(*) e SUM(amount_cents).
	Count      int64
	TotalCents int64

	// MarkedCount e MarkedTotalCents são a PARTE de Count e TotalCents cuja
	// categoria é de natureza investment/redemption. Vêm da MESMA varredura —
	// nunca de uma segunda leitura, que uma escrita concorrente poderia
	// separar (ADR-029j) — e obedecem a `0 ≤ marcado ≤ total`, que o serviço
	// confere antes de publicar.
	MarkedCount      int64
	MarkedTotalCents int64
}

// Ledger é o que o painel precisa do domínio de lançamentos. Satisfeita por
// gormstore.TransactionRepository, sem nenhuma adição à interface
// transaction.Repository.
type Ledger interface {
	// SumMonthByKindAndAccount agrega, por (kind, conta), os lançamentos VIVOS
	// da casa no mês de COMPETÊNCIA, lendo apenas `income` e `expense`.
	//
	// householdID e competenceMonth não podem ser vazios — a implementação
	// recusa, porque `competence_month = ''` devolveria um mês zerado em
	// silêncio, e casa vazia é a porta do BOLA.
	//
	// markedCategoryIDs são as categorias de natureza investment/redemption da
	// casa. Lista VAZIA não é erro: significa "esta casa não marca
	// investimento", e então as duas colunas condicionais não entram na
	// consulta — `IN ()` nunca é emitido (ADR-029f).
	SumMonthByKindAndAccount(ctx context.Context, householdID, competenceMonth string,
		markedCategoryIDs []string) ([]KindAccountTotals, error)
}

// Categories é o que o painel precisa do domínio de categorias. Já satisfeita
// por gormstore.CategoryRepository.
type Categories interface {
	// List devolve TODAS as categorias da casa. O painel sempre pede
	// includeArchived = true, pela mesma razão do relatório e do investimento:
	// arquivar não desfaz a marcação do passado (PLANOS.md §4.4), e o conjunto
	// que marca os lançamentos tem de ser o MESMO nas três agregações.
	List(ctx context.Context, householdID string, includeArchived bool) ([]category.Category, error)
}

// Accounts é o que o painel precisa do domínio de contas: o tipo (para saber
// quais são cartão de crédito) e o estado de arquivamento.
type Accounts interface {
	// List devolve as contas da casa. O painel sempre pede
	// includeArchived = true, e os dois usos da lista são deliberadamente
	// DIFERENTES: o gasto de um cartão ARQUIVADO continua contando no mês
	// (arquivar não apaga o passado), mas creditCardAccountCount conta só as
	// vivas e NÃO arquivadas — é o número que responde "você tem um cartão
	// cadastrado?".
	List(ctx context.Context, householdID string, includeArchived bool) ([]account.Account, error)
}

// --- Entradas --------------------------------------------------------------

// Actor é quem pede o painel. Casa e usuário vêm do TOKEN, nunca da
// requisição. Sem IP: leitura não audita.
type Actor struct {
	HouseholdID string
	UserID      string
}

// SummaryInput é a entrada de GET /dashboard, como veio da query — a validação
// acontece no serviço, antes de qualquer consulta.
type SummaryInput struct {
	// Month é "AAAA-MM" (competência) e é OBRIGATÓRIO. Nunca "o mês atual por
	// padrão": um default aqui faria a tela mostrar números de um mês que
	// ninguém pediu. Nada é normalizado — " 2026-09" e "2026-9" são recusados
	// como vieram, para a recusa não ensinar nada sobre o servidor.
	Month string
}

// --- DTO de resposta (docs/SEGURANCA.md §4: nunca a entidade direto) -------

// SummaryView é a resposta de GET /dashboard (schema DashboardSummary).
//
// São NOVE campos, todos obrigatórios e sempre presentes: o schema é
// `additionalProperties: false` e mês sem movimento devolve zeros — nunca
// null, nunca campo ausente, porque um número que falta obriga a tela a
// inventar o que mostrar. Campo a mais aqui é divergência de contrato, não
// bônus.
//
// As contagens são int64 como os centavos: um tipo inteiro só no DTO inteiro.
// Todas nascem do mesmo COUNT(*) ou de listas com teto de domínio
// (≤ 50 contas, ≤ 200 categorias).
type SummaryView struct {
	// Month é o mês pedido, devolvido de volta sem normalização.
	Month string `json:"month"`

	// IncomeCents são as receitas vivas da competência MENOS os resgates
	// marcados, e IncomeCount são as linhas que sobraram. Idêntico ao
	// `summary.incomeCents` de GET /transactions no mesmo mês: incluir os
	// resgates faria o mesmo dinheiro aparecer duas vezes na mesma faixa
	// (+1.000 na receita e −1.000 no investimento) e as duas telas divergirem
	// (ADR-029e).
	IncomeCents int64 `json:"incomeCents"`
	IncomeCount int64 `json:"incomeCount"`

	// CreditCardExpenseCents são as despesas vivas da competência em contas de
	// tipo credit_card — TODOS os cartões num número só — menos os aportes
	// lançados em cartão, para o mesmo dinheiro não ser contado duas vezes na
	// mesma faixa. Cartão arquivado continua contando.
	CreditCardExpenseCents int64 `json:"creditCardExpenseCents"`
	CreditCardExpenseCount int64 `json:"creditCardExpenseCount"`

	// InvestmentNetCents é aportes − resgates da competência, COM SINAL — o
	// único campo desta resposta que pode ser negativo, e um dos pouquíssimos
	// campos de dinheiro do contrato sem `minimum: 0`, ao lado do saldo de
	// conta (ADR-031c). InvestmentCount é a SOMA dos aportes e resgates que o
	// formaram, nunca a diferença.
	InvestmentNetCents int64 `json:"investmentNetCents"`
	InvestmentCount    int64 `json:"investmentCount"`

	// CreditCardAccountCount conta as contas credit_card VIVAS E NÃO
	// ARQUIVADAS; InvestmentCategoryCount conta as categorias de natureza
	// investment/redemption da casa COM as arquivadas. Os dois critérios são
	// diferentes de propósito: eles existem para a tela distinguir "não tem
	// cartão cadastrado" de "tem cartão e não gastou" sem adivinhar — e zero
	// com o contador em zero pede um texto, não um R$ 0,00.
	CreditCardAccountCount  int64 `json:"creditCardAccountCount"`
	InvestmentCategoryCount int64 `json:"investmentCategoryCount"`
}
