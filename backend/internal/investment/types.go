// Package investment responde "quanto guardei este mês e no ano" e marca
// retroativamente, por palavra-chave, o que já está gravado (spec 0006,
// ADR-029).
//
// # Por que um pacote próprio, no molde do internal/report
//
// Como o relatório (ADR-027a), este pacote é CONSUMIDOR de três domínios —
// lançamento, categoria e conta — e não pertence a nenhum deles: pôr as duas
// rotas dentro de `transaction` faria aquele serviço, que já conhece conta,
// fatura e deduplicação, aprender também a árvore de categorias e o motor de
// palavras-chave. Por isso ele declara as interfaces de que precisa (Ledger,
// Categories, Accounts, Classifier) e os repositórios que já existem as
// satisfazem, sem uma linha nova neles.
//
// Diferente do `report`, este pacote ESCREVE: o `detect` precisa de Transactor
// e de Auditor, e as duas chegam pelo construtor.
//
// # O predicado, escrito uma vez
//
// Tudo o que este pacote soma, lista ou marca responde à MESMA pergunta
// (ADR-029d): *"a categoria deste lançamento é de natureza `investment` ou
// `redemption`?"*. O conjunto sai de UMA leitura da taxonomia da casa
// (≤ category.MaxPerHousehold linhas, ARQUIVADAS INCLUÍDAS — arquivar não
// desfaz a marcação do passado, PLANOS.md §4.4).
//
// Dentro dele, o FLUXO vem do `kind` do LANÇAMENTO, nunca da natureza da
// categoria: `expense` é aporte (contribution) e `income` é resgate
// (redemption). É o que garante que o que sai de `expenseCents` no summary de
// GET /transactions seja exatamente o que entra em `investedCents` — duas
// perguntas diferentes para o mesmo número é como se começa a ter dois
// números.
//
// # Regras que o pacote sustenta
//
//   - toda consulta e toda escrita são escopadas pelo household_id do TOKEN
//     (docs/SEGURANCA.md §2); casa e usuário nunca vêm do corpo nem da URL;
//   - dinheiro é int64 em centavos (ADR-003) — nenhum float em nenhuma camada;
//   - o "mês" é COMPETÊNCIA (ADR-023c), e a comparação de "AAAA-MM" é
//     lexicográfica porque a largura é fixa: nenhuma função de data no SQL;
//   - conjunto de categorias VAZIO nunca vira consulta: o curto-circuito é em
//     Go (ADR-029f), porque `IN ()` é erro de sintaxe em três dos quatro
//     dialetos e `1=0` no outro.
package investment

import (
	"context"
	"errors"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Limites desta feature.
const (
	// SeriesMonths é o tamanho FIXO da série da tela. O cliente não escolhe:
	// publicar `minItems: 12, maxItems: 12` no contrato é prometer que a tela
	// nunca precisa tratar buraco nem página.
	SeriesMonths = 12

	// MaxCandidates é o teto de CANDIDATOS de uma execução do detect.
	//
	// "Candidato" é toda receita e despesa VIVA do mês — com ou sem
	// categoria —, porque todas são lidas para montar a prévia. Contar só as
	// sem categoria subestimaria o custo real justamente no mês em que ele é
	// maior. Acima do teto é 422, nunca execução parcial.
	//
	// É o mesmo número de transaction.MaxAutoCategorizeRows, e é dele que sai:
	// "um mês grande" tem um tamanho só no projeto inteiro.
	MaxCandidates = transaction.MaxAutoCategorizeRows

	// MaxListed é o teto de itens em CADA uma das três listas da prévia. As
	// CONTAGENS são sempre completas; a lista é a amostra que a tela consegue
	// mostrar. Publicado no contrato (maxItems: 500).
	MaxListed = transaction.MaxAutoCategorizeListed
)

// Fluxo do dinheiro (schema InvestmentFlow). Derivado do `kind` do LANÇAMENTO,
// nunca da natureza da categoria (ADR-029d). Enum FECHADO.
const (
	FlowContribution = "contribution"
	FlowRedemption   = "redemption"
)

// Motivos de NÃO-marcação (schema InvestmentDetectUnmatchedReason). Enum
// FECHADO de três valores.
//
// Os dois primeiros vêm do motor (textmatch.Reason); o terceiro é desta rota.
// `other_category` existe porque dizer `below_threshold` para uma linha que
// bateu com "Mercado" a 100 seria mentira na tela: a pessoa leria "não bateu
// com nada" sobre uma linha que bateu — só que com uma categoria comum, que
// quem grava é POST /transactions/auto-categorize.
// Os dois primeiros são DERIVADOS de textmatch.Reason, e não reescritos como
// literal: são os mesmos dois valores que o auto-categorize publica, e duas
// cópias de um enum divergem.
const (
	ReasonBelowThreshold = string(textmatch.ReasonBelowThreshold)
	ReasonAmbiguous      = string(textmatch.ReasonAmbiguous)
	ReasonOtherCategory  = "other_category"
)

// Erros de domínio. Nenhum carrega detalhe interno nem dado de outra casa.
var (
	// ErrUnauthenticated — ator sem casa. O handler já barrou antes; a guarda
	// no serviço é defesa em profundidade, para que nenhuma consulta rode com
	// household_id vazio (que devolveria vazio em silêncio, ou pior).
	ErrUnauthenticated = errors.New("autenticação necessária")

	// ErrTooManyCandidates — o mês tem mais receitas e despesas vivas do que
	// MaxCandidates. É 422 no campo `month`, decidido ANTES de qualquer
	// escrita: o pedido é válido em forma, mas o mês não cabe numa execução.
	//
	// Ele é da MESMA família de transaction.ErrTooManyUncategorized, e NÃO o
	// mesmo erro, porque conta outra coisa: lá são as linhas sem categoria,
	// aqui são todas as linhas que a prévia precisa ler.
	ErrTooManyCandidates = errors.New("lançamentos demais para uma execução")

	// ErrDestinationChanged — uma categoria de DESTINO do plano deixou de ser
	// atribuível entre o cálculo e a escrita.
	//
	// É a janela TOCTOU que nasce do achado A2 da entrega anterior: o plano
	// roda FORA da transação — de propósito, para não segurar conexão do pool
	// durante o cálculo — e entre ele e o UPDATE cabe uma requisição inteira.
	// Se nela alguém excluir a categoria de destino, a checagem `inUse` da
	// exclusão não encontra nada (nada foi gravado ainda) e o UPDATE
	// penduraria lançamentos numa categoria EXCLUÍDA: o estado que
	// category.ErrInUse existe para impedir. Não é BOLA — os ids vêm do
	// conjunto já filtrado pela casa do token —, é integridade.
	//
	// A decisão é falhar a operação INTEIRA, e não tirar as linhas do lote:
	// esta rota promete que a prévia diz EXATAMENTE o que a escrita fará, e um
	// lote parcial devolveria um `marked` menor sem dizer quais linhas ficaram
	// para trás. É o mesmo desfecho de transaction.ErrTransferConversionConflict
	// (ADR-028d) — 409 CONFLICT, nada gravado, a tela pede a prévia de novo.
	//
	// ⚠️ ARQUIVAR o destino CAI aqui, e a distinção é sutil o bastante para
	// alguém querer reabri-la (a primeira versão deste comentário dizia o
	// contrário, e era o comentário que estava errado, não o código):
	// MARCAÇÃO EXISTENTE sobrevive ao arquivamento — categoria arquivada
	// continua contando e o lançamento que já aponta para ela continua
	// apontando (PLANOS.md §4.4) —, mas ATRIBUIÇÃO NOVA a categoria arquivada é
	// recusada em todas as outras portas do produto
	// (transaction.ErrCategoryArchived no PATCH de uma linha, no lote e na
	// importação). O `detect` ATRIBUI: sem esta recusa ele seria a única porta
	// a gravar onde as outras três recusam. A categoria de ORIGEM arquivada,
	// essa sim, continua trocável — ver podarAllowlist em detect.go.
	ErrDestinationChanged = errors.New("categoria de destino mudou durante a detecção")

	// ErrPlanTimeout — o prazo PRÓPRIO da fase de cálculo
	// (transaction.PlanTimeout) acabou e a detecção parou por decisão própria.
	// É limite de TRABALHO, como ErrTooManyCandidates: 422 em `fields.month`,
	// com a mesma orientação — período menor.
	//
	// ⚠️ Esta sentinela é a ÚNICA autorização para dizer "a detecção deste mês
	// demorou demais", e ela é emitida apenas nas paradas VOLUNTÁRIAS da fase
	// de cálculo (conferirPrazo/erroDeParada, em detect.go) — NUNCA deduzida do
	// estado do contexto depois de um erro qualquer.
	//
	// O motivo é concreto, e é o mesmo que fez o importador abandonar a dedução
	// (ver importer.ErrAnalyzeTimeout): desde que o gormstore passou a somar o
	// motivo do contexto ao erro do driver (platform/storage/ctxerr.go),
	// QUALQUER falha de banco ocorrida com o contexto morto casa
	// `errors.Is(err, context.DeadlineExceeded)`. A versão anterior do ramo da
	// borda casava o erro de CONTEXTO, e com isso fazia duas coisas erradas ao
	// mesmo tempo: respondia "a detecção deste mês demorou demais" — culpando o
	// MÊS da pessoa — por uma falha do SERVIDOR, e apagava a única linha de
	// ERROR daquela falha do log. Falha de banco sob contexto morto é 500 com
	// log de ERROR, e é assim que tem de continuar.
	//
	// O preço é declarado: um prazo que vença DENTRO de uma consulta não vira
	// 422, vira 500 com ERROR. É o lado certo para errar — o prazo existe para
	// limitar o trabalho sobre as LINHAS do mês (leitura, pontuação), e uma
	// única consulta que sozinha o estoura é problema de infraestrutura, não do
	// mês de quem pediu.
	ErrPlanTimeout = errors.New("o prazo da fase de cálculo da detecção acabou")

	// errClassifierMissing — falha de LIGAÇÃO (cmd/api monta o loader), não de
	// entrada. Nunca panic no caminho de request: erro genérico, 500, e o log
	// diz o quê.
	errClassifierMissing = errors.New("classificador de palavras-chave não configurado")

	// errTransactorMissing — o mesmo caso, do lado da transação. A execução
	// real emite vários comandos mais a auditoria: sem transação, meia
	// marcação ficaria gravada sem rastro.
	errTransactorMissing = errors.New("transação não configurada")

	// errTotalsOutOfRange — a agregação devolveu total negativo, contagem
	// negativa ou uma soma que não cabe em int64.
	//
	// Falha FECHADA (ADR-029 j.1): a resposta publica `minimum: 0` para os
	// quatro números, e um só lançamento com amount_cents negativo — que o
	// caminho de escrita recusa, mas o schema não impede — publicaria um
	// aporte negativo contra um contrato que promete o contrário. Clampar em
	// silêncio seria pior: esconderia a corrupção e ainda assim publicaria um
	// número errado. 500 genérico, contagens no log, nenhum número inventado.
	errTotalsOutOfRange = errors.New("totais de investimento fora da faixa representável")
)

// --- Interfaces declaradas no CONSUMIDOR (ADR-027a) ------------------------

// Ledger é o que esta feature precisa do domínio de lançamentos. Satisfeita
// por gormstore.TransactionRepository sem nenhuma adição (T1).
//
// TODO método recebe householdID, e ele entra sempre no WHERE, nunca no SET:
// é a defesa contra BOLA aplicada na camada mais baixa (docs/SEGURANCA.md §2).
type Ledger interface {
	// SumInvestmentsByMonth é a consulta ÚNICA que resolve os TRÊS números da
	// tela: o mês, o ano até o mês e a série de 12. A janela do ano-até-o-mês
	// (Y-01..M) está sempre CONTIDA na dos 12 meses (M-11..M), então pedir a
	// maior das duas e montar as três em Go é UMA ida ao banco — nem doze,
	// nem duas que possam discordar entre si.
	SumInvestmentsByMonth(ctx context.Context, householdID string, categoryIDs []string, fromMonth, toMonth string) ([]transaction.InvestmentMonthTotals, error)

	// ListByCategories é a página do mês, com a MESMA ordem e o MESMO cursor
	// de GET /transactions — um só formato de cursor no app inteiro.
	ListByCategories(ctx context.Context, householdID string, f transaction.CategoryListFilter) ([]transaction.Transaction, error)

	// ListIncomeExpenseOfMonth é a matéria-prima do detect: as receitas e
	// despesas vivas do mês COM e SEM categoria, numa consulta só.
	ListIncomeExpenseOfMonth(ctx context.Context, householdID, competenceMonth string, limit int) ([]transaction.CategorizableRow, error)

	// SetCategoryWhereNull marca o que está VAZIO. O `category_id IS NULL`
	// mora no WHERE: linha que ganhou categoria entre a prévia e a confirmação
	// não é sobrescrita e não é contada.
	SetCategoryWhereNull(ctx context.Context, householdID string, ids []string, categoryID string, at time.Time) (int64, error)

	// SetCategoryWhereCurrentIn é a escrita do overwriteCategorized. A
	// allowlist das categorias de natureza income/expense entra no WHERE, e é
	// por isso que a troca NUNCA desfaz uma marcação de investimento — nem a
	// de outra execução, nem a que a pessoa fez à mão (ADR-029h).
	SetCategoryWhereCurrentIn(ctx context.Context, householdID string, ids, currentCategoryIDs []string, categoryID string, at time.Time) (int64, error)
}

// Categories é o que esta feature precisa do domínio de categorias. Já
// satisfeita por gormstore.CategoryRepository.
type Categories interface {
	// List devolve TODAS as categorias da casa. Esta feature sempre pede
	// includeArchived = true, e não é detalhe: arquivar uma categoria de
	// investimento não desfaz a marcação do passado (PLANOS.md §4.4), e a
	// allowlist do overwriteCategorized precisa alcançar a categoria de
	// despesa arquivada que a linha tem hoje.
	List(ctx context.Context, householdID string, includeArchived bool) ([]category.Category, error)

	// LiveStates devolve o estado ATUAL — natureza inclusive — das categorias
	// vivas da casa, dentre os ids informados, em UMA consulta e nunca uma por
	// id. Id ausente do mapa não existe mais.
	//
	// É a reconferência que fecha a janela entre o plano (fora da transação) e
	// os UPDATE (dentro dela): ver ErrDestinationChanged. Categoria ARQUIVADA
	// continua no mapa — arquivar é benigno; quem sai é a EXCLUÍDA.
	LiveStates(ctx context.Context, householdID string, ids []string) (map[string]category.LiveState, error)
}

// Accounts é o que esta feature precisa do domínio de contas: só o NOME que a
// lista do mês exibe.
type Accounts interface {
	List(ctx context.Context, householdID string, includeArchived bool) ([]account.Account, error)
}

// Classifier carrega o conjunto de palavras-chave da casa.
//
// Interface, e não o *classify.Loader concreto, porque é a única dependência
// do pacote que o teste precisa trocar sem montar quatro repositórios — e
// porque a decisão de QUAIS palavras entram no matcher é do classify, não
// daqui. Este pacote NÃO edita classify (ADR-029g): consome SuggestCategory
// como ela é.
type Classifier interface {
	Load(ctx context.Context, householdID string) (*classify.Set, error)
}

// Transactor executa uma função dentro de UMA transação. Satisfeita por
// gormstore.UnitOfWork.
//
// O detect precisa dela porque a execução real pode emitir vários UPDATE (um
// por categoria de destino, mais o do overwrite) e a auditoria: ou tudo vale,
// ou nada vale. Auditoria DENTRO da transação — se o rastro não couber, a
// escrita não vale (§4.7 do PLANOS.md).
type Transactor interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Auditor registra o rastro das escritas financeiras. Interface no consumidor,
// implementada por audit.Service através da ponte de cmd/api.
type Auditor interface {
	Record(ctx context.Context, p AuditParams) error
}

// AuditParams é o evento a registrar. Espelha audit.Params sem que este pacote
// precise importar aquele — e, como ele, NÃO tem campo de valor nem de
// detalhe: auditoria de dinheiro guarda QUEM mexeu em QUÊ e QUANDO, nunca
// quanto, nunca a descrição e nunca a palavra-chave (S8 do PLANOS.md).
//
// As CONTAGENS da execução não cabem aqui de propósito: elas vão para a
// resposta e para o log estruturado da borda, como no auto-categorize (emenda
// §10.5 da spec 0005).
type AuditParams struct {
	Action      string
	Entity      string
	EntityID    string
	UserID      string
	HouseholdID string
	IP          string
}

// Actor é quem está agindo. Casa e usuário vêm do TOKEN, nunca da requisição;
// o IP vem da borda HTTP e só serve à auditoria.
type Actor struct {
	HouseholdID string
	UserID      string
	IP          string
}

// --- Entradas --------------------------------------------------------------

// OverviewInput é a janela de GET /investments, como veio da query. A
// validação acontece no serviço, antes de qualquer consulta.
type OverviewInput struct {
	// Month é "AAAA-MM" (competência) e é OBRIGATÓRIO. Nunca "o mês atual por
	// padrão": um default aqui faria a tela mostrar números de um mês que
	// ninguém pediu.
	Month string

	// Cursor é o nextCursor da página anterior — o MESMO de GET /transactions.
	Cursor string

	// Limit é o tamanho da página de `items`; zero usa o padrão. O teto já foi
	// conferido na borda (400 acima de 100), e aqui ele é reaplicado como
	// defesa em profundidade.
	Limit int
}

// DetectInput é o corpo de POST /investments/detect.
type DetectInput struct {
	Month string

	// DryRun true só calcula; false grava. O handler EXIGE o campo no corpo
	// (ausente é 400): o zero value de bool seria "gravar", e gravar por um
	// campo esquecido é exatamente o que não pode acontecer.
	DryRun bool

	// OverwriteCategorized ausente é FALSE, sempre — e o ausente é o seguro.
	// Vale nos DOIS modos: em dryRun ele muda o que a prévia PROMETE, e a
	// promessa e a escrita são calculadas pelo mesmo código (planejar).
	OverwriteCategorized bool
}

// --- DTOs de resposta (docs/SEGURANCA.md §4: nunca a entidade direto) ------

// TotalsView são os quatro números de uma janela (schema InvestmentTotals).
//
// Não existe líquido nem saldo aqui: aporte menos resgate não é patrimônio, e
// publicar essa subtração convidaria a lê-la como tal (spec 0006 §2.2).
//
// ⚠️ Escopo, para quem vier depois: a regra é DESTE schema, e continua valendo
// por decisão explícita do usuário em 18/09/2026. O PAINEL publica, desde a
// mesma data, o líquido do mês (aportes − resgates, com sinal, podendo ser
// negativo) em schema PRÓPRIO — são duas perguntas diferentes, e nenhuma das
// duas respostas é calculada no cliente. Ver `LICOES-BACKEND.md`.
type TotalsView struct {
	ContributionsCents int64 `json:"contributionsCents"`
	ContributionCount  int64 `json:"contributionCount"`
	RedemptionsCents   int64 `json:"redemptionsCents"`
	RedemptionCount    int64 `json:"redemptionCount"`
}

// SeriesPointView é um mês da série (schema InvestmentSeriesPoint). Mês sem
// movimento vem com zeros, nunca ausente.
type SeriesPointView struct {
	Month              string `json:"month"`
	ContributionsCents int64  `json:"contributionsCents"`
	RedemptionsCents   int64  `json:"redemptionsCents"`
}

// ItemView é um lançamento marcado do mês (schema InvestmentItem).
//
// CategoryID e CategoryName são NÃO anuláveis, diferente de transaction.View:
// sem categoria o lançamento não estaria nesta lista.
type ItemView struct {
	ID           string     `json:"id"`
	OccurredOn   civil.Date `json:"occurredOn"`
	Flow         string     `json:"flow"`
	AccountID    string     `json:"accountId"`
	AccountName  string     `json:"accountName"`
	CategoryID   string     `json:"categoryId"`
	CategoryName string     `json:"categoryName"`
	AmountCents  int64      `json:"amountCents"`
	Description  string     `json:"description"`
	Source       string     `json:"source"`
}

// OverviewView é a resposta de GET /investments (schema InvestmentOverview).
type OverviewView struct {
	Month      string     `json:"month"`
	Monthly    TotalsView `json:"monthly"`
	YearToDate TotalsView `json:"yearToDate"`

	// Series tem SEMPRE SeriesMonths itens, em ordem cronológica crescente,
	// terminando em Month.
	Series []SeriesPointView `json:"series"`

	// Items está sempre inicializado: mês vazio é `[]`, nunca `null`.
	Items []ItemView `json:"items"`

	// NextCursor é nulo quando não há mais página, e está SEMPRE presente para
	// a tela não precisar distinguir "ausente" de "acabou".
	NextCursor *string `json:"nextCursor"`
}

// DetectItemView é um lançamento que recebe (ou receberia) categoria de
// investimento (schema InvestmentDetectItem).
type DetectItemView struct {
	ID             string `json:"id"`
	Description    string `json:"description"`
	CategoryID     string `json:"categoryId"`
	CategoryName   string `json:"categoryName"`
	Flow           string `json:"flow"`
	MatchScore     int    `json:"matchScore"`
	MatchedKeyword string `json:"matchedKeyword"`
}

// DetectUnmatchedItemView é um lançamento SEM categoria que continua sem marca
// de investimento (schema InvestmentDetectUnmatchedItem).
type DetectUnmatchedItemView struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Reason      string `json:"reason"`
}

// DetectAlreadyCategorizedItemView é um lançamento que JÁ TEM categoria de
// natureza income/expense e que bateria numa palavra-chave de investimento
// (schema InvestmentDetectAlreadyCategorizedItem).
//
// A lista existe para a pessoa DECIDIR, não para prometer mudança: sem a flag
// nada aqui é alterado. Quem já tem categoria de INVESTIMENTO não entra —
// oferecer a troca seria prometer o que o WHERE recusa.
type DetectAlreadyCategorizedItemView struct {
	ID                  string `json:"id"`
	Description         string `json:"description"`
	CurrentCategoryID   string `json:"currentCategoryId"`
	CurrentCategoryName string `json:"currentCategoryName"`
	CategoryID          string `json:"categoryId"`
	CategoryName        string `json:"categoryName"`
	Flow                string `json:"flow"`
	MatchScore          int    `json:"matchScore"`
}

// DetectView é a resposta de POST /investments/detect (schema
// InvestmentDetectResult).
//
// Em dryRun, Marked é quantos RECEBERIAM categoria e as três listas vêm
// preenchidas (MaxListed cada). Na execução real, Marked é o número de linhas
// AFETADAS pelos UPDATE condicionais e as listas vêm vazias.
//
// Invariantes publicados no contrato:
//
//   - sem overwriteCategorized, Marked + Unmatched é EXATAMENTE o total de
//     lançamentos sem categoria do mês;
//   - AlreadyCategorized conta só os que já têm categoria de natureza
//     income/expense E bateriam numa palavra-chave de investimento — isto é,
//     os que a flag conseguiria trocar;
//   - com overwriteCategorized, esse conjunto migra INTEIRO para Marked e
//     AlreadyCategorized volta 0 (e então Marked passa a somar os dois grupos:
//     a prévia diz exatamente o que a escrita fará, nem mais nem menos).
type DetectView struct {
	Month              string `json:"month"`
	Marked             int64  `json:"marked"`
	Unmatched          int64  `json:"unmatched"`
	AlreadyCategorized int64  `json:"alreadyCategorized"`

	// As três listas estão sempre inicializadas: `[]`, nunca `null`.
	Items                   []DetectItemView                   `json:"items"`
	UnmatchedItems          []DetectUnmatchedItemView          `json:"unmatchedItems"`
	AlreadyCategorizedItems []DetectAlreadyCategorizedItemView `json:"alreadyCategorizedItems"`
}
