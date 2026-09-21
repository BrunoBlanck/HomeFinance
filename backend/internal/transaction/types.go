// Package transaction modela lançamento — dinheiro entrando, saindo ou
// mudando de conta.
//
// Regras que este pacote sustenta (PLANOS.md §3.3, ADR-016, ADR-017):
//   - toda operação é escopada por household_id vindo do TOKEN (BOLA é o
//     risco nº 1 — docs/SEGURANCA.md §2);
//   - dinheiro é int64 em centavos e amount_cents é SEMPRE POSITIVO: o sinal
//     vem do kind, nunca do número. Isso evita a classe inteira de bug em que
//     uma despesa é gravada positiva e some do total;
//   - transferência é um PAR de linhas (transfer_out + transfer_in) amarrado
//     por transfer_group_id, e nunca entra em receita/despesa (ADR-016);
//   - saldo é derivado (ADR-017): não há coluna de saldo em lugar nenhum.
package transaction

import (
	"errors"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
)

// Tipos de lançamento. Conjunto FECHADO — validação por allowlist, nunca
// "qualquer string que o cliente mandar".
const (
	KindIncome      = "income"
	KindExpense     = "expense"
	KindTransferOut = "transfer_out"
	KindTransferIn  = "transfer_in"
)

// Origem do lançamento: digitado por uma pessoa ou vindo de um arquivo.
const (
	SourceManual = "manual"
	SourceImport = "import"
)

// Limites de domínio.
const (
	// MaxDescriptionLen é medido em runas (o usuário conta as que vê).
	MaxDescriptionLen = 140

	// MaxAmountCents é R$ 999.999.999,99.
	MaxAmountCents = 99_999_999_999

	// DedupWindowDays é a folga, em dias, que a janela de deduplicação abre
	// para cada lado do intervalo do arquivo importado. Existe porque banco e
	// cartão lançam a mesma compra com um ou dois dias de diferença conforme o
	// documento (extrato x fatura), e sem a folga a mesma compra entraria duas
	// vezes.
	DedupWindowDays = 3

	// DefaultPageSize e MaxPageSize limitam a listagem por cursor (P7).
	DefaultPageSize = 50
	MaxPageSize     = 200

	// MaxAutoCategorizeRows é o teto de lançamentos sem categoria que UMA
	// execução do auto-categorize aceita processar num mês (spec 0005 §7).
	// Acima disso a resposta é 422, e não uma execução parcial: categorizar
	// "os 10.000 primeiros" deixaria a pessoa sem saber quais ficaram de fora.
	// Espelha MaxBatchRows — é o mesmo tamanho de "um mês grande".
	MaxAutoCategorizeRows = 10_000

	// MaxAutoCategorizeListed é o teto de itens em CADA lista da prévia
	// (items e unmatchedItems). As contagens são sempre completas; a lista é
	// uma amostra que a tela consegue mostrar. Publicado no contrato
	// (maxItems: 500).
	MaxAutoCategorizeListed = 500

	// MaxTransferLegsPerMonth é o teto de pernas de transferência que
	// GET /transfers soma em memória para montar `pairs`. Acima disso é 422:
	// os totais são do mês INTEIRO, e um total calculado sobre parte do mês
	// seria um número errado apresentado como certo.
	MaxTransferLegsPerMonth = 10_000

	// MaxTransferDetectRows é o teto de CANDIDATAS (receitas e despesas vivas
	// do mês de competência ainda sem grupo) que UMA execução de
	// POST /transfers/detect aceita (spec 0005 §13.1.9, ADR-028f). Acima é
	// 422, nunca execução parcial: parear "os 10.000 primeiros" deixaria a
	// pessoa sem saber quais ficaram de fora. Mesmo tamanho de "um mês
	// grande" de MaxAutoCategorizeRows.
	MaxTransferDetectRows = 10_000

	// MaxTransferMirrorRows é o teto do POOL DE ESPELHOS — as receitas e
	// despesas vivas da casa inteira na janela de datas das candidatas
	// (qualquer competência). É o dobro das candidatas porque o espelho de
	// cada uma pode estar em outra competência (linha de fatura), e a janela
	// ±DedupWindowDays soma vizinhança dos dois lados. Acima é 422 também.
	MaxTransferMirrorRows = 20_000

	// MaxTransferPairComparisons é o teto DURO de comparações candidata ×
	// espelho de UMA execução (achado A2 da revisão de segurança).
	//
	// O índice por (kind, valor, dia) já corta o custo do caso realista, mas
	// não do patológico: 10.000 candidatas e 20.000 espelhos com o MESMO
	// valor nos MESMOS dias não têm por onde ser separados, e uma rajada
	// dessas requisições prenderia CPU e — na execução real — conexões do
	// pool em transação aberta. Passar do teto é 422, como todo outro teto
	// desta feature: nunca execução parcial.
	//
	// 2 milhões custam ~0,3 s de CPU na máquina de desenvolvimento — e é o
	// TEMPO que importa aqui, porque a execução real gasta esse tempo com uma
	// transação aberta segurando conexão do pool. O número é ordens de
	// grandeza acima de qualquer mês real: um mês com 300 lançamentos do
	// mesmo valor no mesmo dia gasta ~90.000 comparações, e o caso normal
	// (valores e dias variados) gasta algumas por candidata.
	MaxTransferPairComparisons = 2_000_000

	// MaxTransferDetectListed é o teto de itens em CADA lista da prévia
	// (items e unpairedItems). As contagens são sempre completas; a lista é
	// uma amostra que a tela consegue mostrar. Publicado no contrato
	// (maxItems: 500).
	MaxTransferDetectListed = 500
)

// Erros de domínio. Nenhum carrega detalhe interno nem dado de outra casa.
var (
	// ErrNotFound — lançamento inexistente OU de outra casa. Os dois casos são
	// o MESMO erro de propósito: distinguir confirmaria a existência do
	// recurso alheio (S1).
	ErrNotFound = errors.New("lançamento não encontrado")

	// ErrDuplicateDedup — o índice único (household_id, dedup_key,
	// dedup_ordinal) recusou a linha.
	//
	// É um erro TIPADO, e não um error genérico, porque o serviço de
	// importação precisa traduzi-lo em "linha bloqueada" na tela de revisão e
	// jamais em 500. O repositório o produz a partir de gorm.ErrDuplicatedKey
	// (TranslateError), que é o único mecanismo que se comporta igual nos
	// quatro dialetos.
	ErrDuplicateDedup = errors.New("lançamento duplicado")

	// ErrHouseholdMismatch — alguém tentou gravar em lote uma linha de outra
	// casa. É defesa em profundidade: o serviço já deveria ter barrado, e se
	// não barrou, a escrita inteira falha em vez de vazar.
	ErrHouseholdMismatch = errors.New("lançamento de outra casa no lote")

	// ErrIncomplete — linha sem competência ou sem chave de deduplicação.
	// As duas colunas são NOT NULL e nunca podem sair vazias do serviço; a
	// guarda existe para o defeito falhar na hora, e não virar um mês de
	// relatório vazio meses depois.
	ErrIncomplete = errors.New("lançamento sem competência ou sem chave de deduplicação")

	// Erros da ação `link` da importação (spec 0005 §4.2, ADR-026f). Os três
	// são distintos DE PROPÓSITO, porque o importador reage a cada um de um
	// jeito (plano E2c §4.5): perna excluída bloqueia SÓ a linha e o lote
	// segue; perna que não existe na casa ou incoerente com a linha derruba o
	// lote INTEIRO — é o sintoma de staging adulterado ou de defeito, e nada
	// parcial pode ficar gravado. Nenhum deles é IsBlocked: colisão de chave
	// continua sendo ErrDuplicateDedup.

	// ErrLinkTargetMissing — a perna apontada pelo staging não existe NESTA
	// casa (inexistente e de outra casa são o mesmo erro — S1).
	ErrLinkTargetMissing = errors.New("perna de transferência não encontrada")

	// ErrLinkTargetDeleted — a perna existe na casa mas foi excluída
	// logicamente entre a análise e o confirm.
	ErrLinkTargetDeleted = errors.New("perna de transferência excluída")

	// ErrLinkTargetInvalid — a perna é da casa e está viva, mas não serve: é
	// de OUTRA conta que não a do lote, não é transferência ou não tem grupo.
	ErrLinkTargetInvalid = errors.New("perna de transferência incoerente com a linha")
)

// LinkImportInput é o que a ação `link` da importação grava na perna de
// transferência JÁ EXISTENTE (ADR-026f).
//
// TransactionID é a perna apontada pelo staging — gravada pela análise sob o
// household_id do token, nunca vinda do corpo da requisição (o corpo não tem
// esse campo; campo desconhecido é 400). AccountID é a conta DO LOTE, e é
// reconferida contra a perna: perna de outra conta não é vinculada. O que NÃO
// existe aqui, por construção: valor, data, categoria, descrição — `link` não
// cria nem altera movimento.
type LinkImportInput struct {
	TransactionID string
	AccountID     string
	ExternalID    *string
	DedupKey      string
	ImportBatchID string
}

// Transaction é o lançamento.
//
// Sobre o que NÃO tem aqui:
//   - não há year_month: ele é projeção de OccurredOn, gravada pela camada de
//     persistência para nunca divergir (armadilha P1). CompetenceMonth, ao
//     contrário, é DADO — a fatura do cartão desloca a competência, e nenhuma
//     função consegue derivá-la de occurred_on;
//   - não há saldo, total nem status: tudo derivado (ADR-017).
type Transaction struct {
	ID          string
	HouseholdID string
	Kind        string
	AccountID   string
	CategoryID  *string

	// AmountCents é SEMPRE POSITIVO. O sinal é o Kind.
	AmountCents int64

	Description string
	// DescriptionNorm é derivado de Description em toda escrita (S2 — mass
	// assignment) e é o que torna a busca insensível a acento e caixa nos
	// quatro dialetos (armadilha P2).
	DescriptionNorm string

	// OccurredOn é data civil: dia do calendário, sem hora e sem fuso (D3 da
	// spec 0003).
	OccurredOn civil.Date

	// CompetenceMonth é "YYYY-MM" e é SEMPRE preenchido — inclusive quando é
	// igual ao mês de caixa. Coluna anulável aqui significaria relatório de
	// competência com buraco silencioso.
	CompetenceMonth string

	// TransferGroupID amarra as duas pernas de uma transferência (ADR-016).
	TransferGroupID *string

	// StatementID liga o lançamento à fatura de cartão que o contém.
	StatementID *string

	Source        string
	ImportBatchID *string

	// ExternalID é o identificador que o documento importado trouxe, quando
	// trouxe. É anulável de propósito e NUNCA entra em índice único: o MSSQL
	// trata NULLs como iguais (armadilha P3) e todo lançamento sem external_id
	// colidiria com o primeiro.
	ExternalID *string

	// DedupKey é hash SEMPRE PREENCHIDO, calculado no serviço. É ele, e não o
	// external_id, que sustenta a unicidade — justamente porque não pode ser
	// nulo.
	DedupKey string

	// DedupOrdinal desempata repetições legítimas: duas compras idênticas no
	// mesmo dia, no mesmo valor, no mesmo estabelecimento acontecem de
	// verdade. Começa em 1.
	DedupOrdinal int

	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// IsTransfer informa se o lançamento é perna de transferência.
func (t Transaction) IsTransfer() bool {
	return t.Kind == KindTransferOut || t.Kind == KindTransferIn
}

// SignedAmountCents devolve o valor com o sinal que o kind implica. Existe
// para que nenhum chamador precise repetir o switch — repetir é como um
// relatório acaba somando despesa como receita.
func (t Transaction) SignedAmountCents() int64 {
	switch t.Kind {
	case KindIncome, KindTransferIn:
		return t.AmountCents
	case KindExpense, KindTransferOut:
		return -t.AmountCents
	default:
		return 0
	}
}

// --- spec 0004 §12 (emenda E2d): filtro de TIPO de GET /transactions -------

// Grupos do filtro `kindGroup`. Conjunto FECHADO, validado por allowlist na
// borda — o valor NUNCA é interpolado em SQL, e ausente quer dizer "tudo".
//
// O grupo não é o `kind` da linha: `transfer` são DOIS kinds, e `investment`
// são os MESMOS dois kinds de `income`/`expense` separados pela NATUREZA DA
// CATEGORIA (ADR-029a). Por isso ele é um vocabulário próprio, e não a coluna.
const (
	KindGroupIncome     = "income"
	KindGroupExpense    = "expense"
	KindGroupTransfer   = "transfer"
	KindGroupInvestment = "investment"
)

// ValidKindGroup é a allowlist do filtro. String vazia é "sem filtro" e passa;
// qualquer outra coisa é recusada ANTES de chegar ao repositório.
func ValidKindGroup(g string) bool {
	switch g {
	case "", KindGroupIncome, KindGroupExpense, KindGroupTransfer, KindGroupInvestment:
		return true
	default:
		return false
	}
}

// --- spec 0010 §3.1 item 8 (E9a): agrupamento por descrição ----------------

// MaxDescriptionGroupRows é o TETO DURO de linhas que uma agregação por
// descrição pode devolver, e a fronteira entre "casa grande" e "pergunta que
// este produto não responde".
//
// O número é MEDIDO, não arbitrado (lição de 18/09/2026 — custo do corte
// medido, nunca estimado). A medição de 21/09/2026, em SQLite, sobre a janela
// máxima de 3 meses de competência:
//
//   - banco de desenvolvimento real ............................ 83 linhas
//   - casa pesada realista (750 lançamentos na janela) ..... 336 a 712 linhas
//   - extremo dos testes de volume (10.000 na janela) ... 3.974 a 7.278 linhas
//
// (as duas faixas são o corpus sintético com e sem correlação entre descrição,
// conta e categoria; a forma correlacionada é a do mundo real, a outra é o
// teto pessimista.)
//
// 5.000 fica ~7× acima da casa pesada realista e ~60× acima do banco real, e
// prende o pior caso de memória por requisição em ~2 MB (5.001 linhas × ~400 B
// com descrição no comprimento máximo). Acima dele a resposta é 422 — nunca
// uma resposta PARCIAL, que é a única saída de verdade ruim aqui: um prompt
// silenciosamente incompleto faria a IA propor palavra-chave para metade da
// casa sem ninguém saber.
const MaxDescriptionGroupRows = 5000

// MaxCompetenceMonthsInWindow é a janela de trabalho do menu IA: no máximo 3
// meses de competência, inclusive (spec 0010 §2.1 e emenda §10 achado A2 —
// decisão do usuário de 21/09/2026).
//
// É ela que autoriza o `IN (?, ?, ?)` da consulta: com a janela travada em 3,
// a faixa nunca precisa virar `BETWEEN`, que sobre uma coluna de TEXTO
// dependeria da collation de cada dialeto.
const MaxCompetenceMonthsInWindow = 3

// ErrTooManyDescriptionGroups — a agregação por descrição passou de
// MaxDescriptionGroupRows linhas na janela pedida.
//
// A semântica é TUDO OU NADA: quem recebe este erro não recebe linha nenhuma.
// Devolver as primeiras N seria pior que falhar — o chamador montaria um
// prompt que parece completo e não é.
var ErrTooManyDescriptionGroups = errors.New("descrições demais na janela")

// DescriptionGroup é a projeção de uma leitura AGREGADA de lançamentos: UMA
// linha por (description_norm, kind, account_id, category_id).
//
// Mora aqui, no domínio de lançamento, porque é de lançamento que ela fala —
// e não em `aiprompt`/`aiimport`, que são só dois dos seus consumidores. Cada
// um deles declara a sua própria interface ESTREITA sobre ela, do mesmo jeito
// que `dashboard` declara `Ledger` e importa `transaction.MarcadaComoInvestimento`
// em vez de reescrever o predicado.
//
// As quatro colunas da chave são as que mudam a RESPOSTA que o prompt precisa
// dar sobre a descrição: `kind` e `category_id` porque a mesma descrição pode
// ter sido classificada de formas diferentes (e é justamente essa divergência
// que vira "várias" na linha do prompt), e `account_id` porque o prompt lista
// em que contas a descrição apareceu. Agrupar só por `description_norm`
// perderia essas três respostas e obrigaria a uma segunda consulta.
type DescriptionGroup struct {
	// DescriptionNorm é a chave do agrupamento: é ela que faz "MERCADO DO SEU
	// JOSÉ" e "mercado do seu jose" caírem na MESMA linha (critério 5 da
	// spec 0010).
	DescriptionNorm string

	// SampleDescription é UMA das grafias originais do grupo — uma AMOSTRA
	// para o texto ficar legível, NUNCA um contrato.
	//
	// Vem de `MIN(description)`, e qual das grafias o `MIN` escolhe depende da
	// COLLATION do banco: o mesmo dado devolve "Zaffari" num dialeto e
	// "ZAFFARI" noutro, e as duas respostas estão certas. Nenhum teste deste
	// projeto pode assertar qual grafia veio, e nenhuma regra de negócio pode
	// depender disso. O que é estável, e o que a lógica usa, é
	// DescriptionNorm.
	SampleDescription string

	// Kind é o `kind` cru da linha (income, expense, transfer_in,
	// transfer_out) — NÃO o `kindGroup` da tela, que é agrupamento e depende
	// da natureza da categoria (emenda §12 da spec 0004). Quem quiser o grupo
	// o deriva, como o resto do projeto já faz.
	Kind string

	// AccountID nunca é nulo: `transactions.account_id` é NOT NULL.
	AccountID string

	// CategoryID é ponteiro para receber a linha em que category_id IS NULL —
	// os quatro dialetos agrupam os nulos numa linha só, e é ela que vira o
	// "—" (sem categoria) do prompt.
	CategoryID *string

	// Count é quantos lançamentos vivos caíram neste grupo.
	Count int64

	// TotalCents é a soma dos valores do grupo, em CENTAVOS e sempre positiva
	// — `amount_cents` é positivo por invariante do domínio e o sinal vem do
	// kind. Somar grupos de kinds diferentes sem passar pelo sinal seria
	// somar despesa com receita; quem precisa de saldo usa
	// Transaction.SignedAmountCents.
	TotalCents int64
}
