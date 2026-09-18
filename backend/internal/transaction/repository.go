package transaction

import (
	"context"
	"errors"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
)

// Cursor é a posição de leitura da listagem (armadilha P7).
//
// A paginação é KEYSET, nunca OFFSET: com OFFSET, um lançamento criado entre
// duas páginas empurra a lista e o usuário vê a mesma linha duas vezes (ou
// nenhuma), e o custo cresce com a profundidade. O par (occurred_on, id) é
// único e tem ordem total, então a página seguinte é sempre "o que vem depois
// desta linha".
//
// O cursor que trafega na API é opaco (base64) e é decodificado e VALIDADO na
// borda; aqui ele já chega como valor.
type Cursor struct {
	OccurredOn civil.Date
	ID         string
}

// ListFilter descreve a janela da listagem. Campo vazio = sem filtro.
type ListFilter struct {
	// CompetenceMonth é "YYYY-MM" e filtra por COMPETÊNCIA, não por caixa:
	// é o mês que o usuário vê na tela (a compra de cartão aparece no mês da
	// fatura).
	CompetenceMonth string

	// AccountID restringe ao extrato de uma conta.
	AccountID string

	// StatementID restringe às linhas de UMA fatura de cartão.
	//
	// Ele não é "mais um filtro da tela": é o que a página de detalhe da fatura
	// pergunta. Filtrar por (conta + competência) daria quase a mesma coisa e
	// erraria justamente nas bordas — um lançamento manual naquele cartão
	// naquele mês entraria numa fatura de que ele não faz parte, e o total
	// exibido deixaria de bater com o total cobrado.
	StatementID string

	// Cursor, quando presente, continua de onde a página anterior parou.
	Cursor *Cursor

	// Limit é o tamanho da página. Zero usa DefaultPageSize; acima de
	// MaxPageSize é reduzido ao teto (o cliente não define o custo da
	// consulta).
	Limit int
}

// SummaryFilter é a MESMA janela da listagem, sem paginação: o resumo tem de
// falar exatamente do que a lista mostra.
type SummaryFilter struct {
	CompetenceMonth string
	AccountID       string

	// InvestmentCategoryIDs é o conjunto das categorias de natureza
	// `investment`/`redemption` da casa (ADR-029a), com as ARQUIVADAS
	// INCLUÍDAS — arquivar não desfaz a marcação do passado (PLANOS.md §4.4).
	//
	// Não é mais um filtro de janela, e por isso não tem par em ListFilter: a
	// lista continua mostrando os lançamentos marcados (eles existem e saíram
	// da conta — ADR-029e). O que este conjunto faz é decidir, DENTRO da mesma
	// consulta, o que sai de IncomeCents/ExpenseCents e entra em
	// RedeemedCents/InvestedCents.
	//
	// Vazio quer dizer "esta casa não marca nada" — o caso de TODA casa no dia
	// da entrega. Aí a expressão condicional não entra na consulta e o SQL
	// volta a ser exatamente o de antes do E7: `IN ()` não é emitido em
	// dialeto nenhum (ADR-029f).
	InvestmentCategoryIDs []string
}

// CategoryListFilter é a janela da listagem POR CATEGORIA — os itens da tela
// de investimentos (spec 0006 §3.4.5).
//
// É um tipo próprio, e não um campo a mais em ListFilter, por um motivo de
// segurança de dado e não de gosto: em ListFilter todo campo vazio quer dizer
// "sem filtro", e um conjunto de categorias vazio significando "todas"
// devolveria o mês INTEIRO na tela de investimentos. Aqui a lista vazia é
// ErrEmptyCategoryFilter, nunca "sem filtro".
type CategoryListFilter struct {
	// CompetenceMonth é "YYYY-MM" e é OBRIGATÓRIO: a tela sempre pergunta por
	// um mês, e sem ele a consulta viraria varredura da casa inteira.
	CompetenceMonth string

	// CategoryIDs é o conjunto de categorias a mostrar (≤ MaxPerHousehold).
	// Obrigatório e não vazio.
	CategoryIDs []string

	// Cursor e Limit funcionam como em ListFilter — o MESMO cursor de
	// GET /transactions, um só formato no app inteiro.
	Cursor *Cursor
	Limit  int
}

// TransferFilter descreve a janela de GET /transfers (spec 0005 §4.4).
//
// O mês é OBRIGATÓRIO: transferência sem mês seria varredura da casa inteira,
// e a tela sempre pergunta por um mês. CounterpartAccountID só faz sentido
// com AccountID — o serviço responde 400 antes; o repositório recusa também,
// por defesa em profundidade.
type TransferFilter struct {
	// CompetenceMonth é "YYYY-MM" — competência, como em ListFilter.
	CompetenceMonth string

	// AccountID, quando presente, traz TODAS as pernas que tocam a conta
	// (saída e entrada). Ausente, a listagem devolve UMA perna por par (a de
	// saída), para cada transferência aparecer uma vez.
	AccountID string

	// CounterpartAccountID, com AccountID, restringe ao par entre as duas
	// contas: só as pernas cujo grupo tem a outra perna na contraparte.
	CounterpartAccountID string

	// Cursor e Limit funcionam como em ListFilter — o MESMO cursor de
	// GET /transactions, um só formato no app inteiro.
	Cursor *Cursor
	Limit  int
}

// UncategorizedRow é a projeção mínima que o auto-categorize lê (spec 0005
// §4.3): o que o matcher precisa (description_norm) e o que a prévia mostra
// (description). Nem valor nem conta — a categorização não olha para eles.
type UncategorizedRow struct {
	ID              string
	Kind            string
	Description     string
	DescriptionNorm string
}

// TransferLegSummary é UMA perna de transferência em quatro colunas — o que
// basta para somar os pares do mês em Go (GET /transfers, `pairs`).
type TransferLegSummary struct {
	TransferGroupID string
	AccountID       string
	Kind            string
	AmountCents     int64
}

// TransferLeg é uma perna de transferência VIVA da conta do lote, com a conta
// da OUTRA perna já resolvida — é o candidato a `link` do pareamento da
// importação (spec 0005 §4.2, ADR-026f).
type TransferLeg struct {
	ID              string
	Kind            string
	OccurredOn      civil.Date
	AmountCents     int64
	TransferGroupID string
	// CounterpartAccountID é a conta da outra perna do grupo. Perna cuja
	// contraparte não está viva não vira TransferLeg: o repositório a
	// descarta, porque não há par para vincular.
	CounterpartAccountID string
	ExternalID           *string
	ImportBatchID        *string
}

// TransferCandidateRow é a projeção que o reprocessamento de transferências
// lê (spec 0005 §13, ADR-028c) — tanto para as CANDIDATAS do mês quanto para
// o POOL DE ESPELHOS da janela: o que o pareamento compara (conta, kind,
// valor, data), o que o matcher pontua (description_norm) e o que a prévia
// mostra (description). Sem categoria, sem chave de deduplicação: a conversão
// não olha para elas.
type TransferCandidateRow struct {
	ID              string
	AccountID       string
	Kind            string
	AmountCents     int64
	OccurredOn      civil.Date
	Description     string
	DescriptionNorm string
}

// TransferPairConversion é UM par a converter (ADR-028d): a `expense` que
// vira transfer_out, a `income` que vira transfer_in e o grupo novo que as
// amarra. UpdatedAt é o relógio do serviço, gravado nas duas.
//
// As CONTAS das duas pernas viajam junto e entram no WHERE de cada UPDATE:
// "as duas pernas estão em contas diferentes" é invariante do par (ADR-016),
// e o repositório a reconfere em vez de confiar na checagem em memória do
// pareamento — defesa em profundidade pedida pela revisão de segurança (A4).
type TransferPairConversion struct {
	OutID           string
	OutAccountID    string
	InID            string
	InAccountID     string
	TransferGroupID string
	UpdatedAt       time.Time
}

// LinkFields é o que a ação `link` grava na perna existente (ADR-026f): a
// identidade de deduplicação da linha do arquivo e o lote que a vinculou.
// Nunca valor, data, conta ou categoria — `link` não cria nem altera movimento.
type LinkFields struct {
	// AccountID é a conta do LOTE, e entra no WHERE (não no SET): a perna só
	// é atualizada se for daquela conta. É a reconferência de S1 no banco.
	AccountID     string
	ExternalID    *string
	DedupKey      string
	DedupOrdinal  int
	ImportBatchID string
	UpdatedAt     time.Time
}

// Summary é o resultado agregado da janela.
//
// Transferência não entra em IncomeCents nem em ExpenseCents (ADR-016):
// mover dinheiro entre contas da própria casa não é receita nem despesa, e
// contá-la dobraria o movimento do mês.
type Summary struct {
	IncomeCents  int64
	ExpenseCents int64
	// NetCents = IncomeCents - ExpenseCents.
	NetCents int64
	// Count conta TODOS os lançamentos da janela, transferências inclusive —
	// é o total da lista, e a lista as mostra.
	Count int64
	// Uncategorized conta apenas receitas e despesas sem categoria;
	// transferência não tem categoria por desenho e não é pendência.
	//
	// Aporte e resgate NÃO mudam este número: eles têm categoria por
	// definição (é ela que os marca), então nunca foram pendência (ADR-029e).
	Uncategorized int64

	// InvestedCents e RedeemedCents são o dinheiro marcado como investimento
	// (ADR-029d/e): InvestedCents é a soma das DESPESAS e RedeemedCents a das
	// RECEITAS cuja categoria está em SummaryFilter.InvestmentCategoryIDs.
	//
	// O fluxo vem do kind do LANÇAMENTO, nunca da natureza da categoria:
	// aporte é o que SAI da conta e resgate é o que ENTRA — é o que o extrato
	// diz. É isso que garante que o que sai de ExpenseCents seja exatamente o
	// que entra em InvestedCents, e que a tela de investimentos e a faixa do
	// mês em /lancamentos não possam divergir nem diante de dado anômalo.
	//
	// Os dois saem da MESMA linha agregada da MESMA consulta que produz
	// IncomeCents e ExpenseCents. Duas consultas com subtração entre elas
	// divergiriam sob escrita concorrente, e a diferença apareceria como uma
	// despesa NEGATIVA na tela.
	InvestedCents int64
	RedeemedCents int64
}

// InvestmentMonthTotals são os dois fluxos de UM mês de competência
// (spec 0006 §4): quanto foi aportado e quanto foi resgatado.
//
// Aporte é o lançamento `expense` marcado e resgate é o `income` marcado
// (ADR-029d) — a natureza da categoria diz apenas QUE é investimento, o kind
// do lançamento diz PARA QUE LADO o dinheiro andou.
type InvestmentMonthTotals struct {
	// Month é "YYYY-MM".
	Month string

	ContributionsCents int64
	ContributionCount  int64
	RedemptionsCents   int64
	RedemptionCount    int64
}

// CategorizableRow é a projeção mínima que o `detect` de investimentos lê
// (spec 0006 §3.3): o que o matcher compara (DescriptionNorm), o que a prévia
// mostra (Description) e o que decide em qual das três listas a linha cai
// (CategoryID — nulo é "sem categoria", preenchido é "já categorizada").
//
// É UncategorizedRow mais a categoria, e não UncategorizedRow reaproveitada,
// porque aqui a categoria é o ASSUNTO: sem ela o serviço não sabe distinguir
// o que ele pode marcar do que só pode listar como `alreadyCategorized` — e
// teria de perguntar linha a linha, que é a consulta por linha que o projeto
// recusa desde a spec 0004.
type CategorizableRow struct {
	ID              string
	Kind            string
	Description     string
	DescriptionNorm string
	CategoryID      *string
}

// ErrEmptyCategoryFilter é o que as consultas filtradas por categoria
// devolvem quando o conjunto chega VAZIO.
//
// É erro, e nunca "sem filtro", porque as duas leituras que o usam respondem
// a perguntas que não têm resposta sem o conjunto: "os lançamentos marcados
// do mês" com filtro vazio devolveria o mês inteiro na tela de investimentos,
// e "quanto foi aportado" devolveria a despesa inteira da casa. Quem chama
// faz o CURTO-CIRCUITO EM GO antes (ADR-029f): casa sem categoria de
// investimento responde zeros e lista vazia sem ir ao banco, e `IN ()` — erro
// de sintaxe em três dos quatro dialetos e `1=0` no outro — nunca é emitido.
var ErrEmptyCategoryFilter = errors.New("consulta por categoria exige ao menos uma categoria")

// ErrTooManyCategories é o teto de ids num único IN (...).
//
// O limite não é estético: o SQL Server aceita 2100 parâmetros por comando e o
// SQLite historicamente 999. O domínio já garante ≤ MaxPerHousehold (200)
// categorias por casa, então este erro é defesa em profundidade — se algum dia
// o teto do domínio subir, a consulta falha ALTO aqui em vez de estourar
// dentro do driver, em dois dialetos só, em produção.
var ErrTooManyCategories = errors.New("consulta por categoria recebeu categorias demais")

// DedupRow é a projeção mínima usada pela deduplicação da importação.
//
// É um tipo próprio, e não Transaction, porque a importação carrega uma
// JANELA INTEIRA de lançamentos da conta para a memória e compara linha a
// linha: trazer descrição original, categoria e carimbos de auditoria
// multiplicaria o tráfego sem que nada disso participe da comparação.
type DedupRow struct {
	ID              string
	OccurredOn      civil.Date
	AmountCents     int64
	Kind            string
	DescriptionNorm string
	ExternalID      *string
	DedupKey        string
	DedupOrdinal    int
	// DeletedAt é parte da projeção de propósito: a linha excluída
	// logicamente continua ocupando a chave única, então a importação precisa
	// enxergá-la para RESTAURAR em vez de tentar inserir e quebrar.
	DeletedAt *time.Time
}

// Repository persiste lançamentos. Implementação em
// internal/platform/storage/gormstore (ADR-008).
//
// TODO método recebe householdID, e ele entra sempre no WHERE, nunca no SET.
// Não é redundância: é a defesa contra BOLA aplicada na camada mais baixa
// (docs/SEGURANCA.md §2). Lançamento de outra casa não é retornável nem
// alterável, aconteça o que acontecer na camada de cima.
type Repository interface {
	// CreateBatch insere de uma vez, dentro da transação em curso.
	//
	// É lote porque as duas escritas mais importantes do domínio são
	// múltiplas e indivisíveis: a transferência (duas pernas) e a importação
	// (centenas de linhas). Inserir uma a uma deixaria meia transferência ou
	// meio arquivo gravados se o processo caísse no meio.
	//
	// Violação do índice único (household_id, dedup_key, dedup_ordinal) volta
	// como ErrDuplicateDedup, nunca como erro genérico.
	CreateBatch(ctx context.Context, householdID string, txs []Transaction) error

	// ByID devolve o lançamento da casa. De outra casa, inexistente ou
	// excluído são o MESMO ErrNotFound.
	ByID(ctx context.Context, householdID, id string) (*Transaction, error)

	// List devolve a página ordenada por (occurred_on DESC, id DESC).
	//
	// Para saber se há próxima página, peça Limit+1 e descarte o excedente:
	// o repositório não faz COUNT, que custaria uma varredura por página.
	List(ctx context.Context, householdID string, f ListFilter) ([]Transaction, error)

	// Summary agrega a MESMA janela da listagem em UMA consulta.
	Summary(ctx context.Context, householdID string, f SummaryFilter) (Summary, error)

	// SumByAccount devolve, por conta, a soma COM SINAL dos lançamentos não
	// excluídos. É a metade variável do saldo derivado (ADR-017); a outra
	// metade é o opening_balance_cents da conta.
	//
	// Conta sem lançamento simplesmente não aparece no mapa — o saldo dela é
	// o saldo de abertura.
	SumByAccount(ctx context.Context, householdID string) (map[string]int64, error)

	// WindowForDedup carrega, em UMA consulta, as linhas da conta no
	// intervalo [minDate - DedupWindowDays, maxDate + DedupWindowDays],
	// INCLUINDO as excluídas logicamente.
	//
	// Uma consulta só, e não uma por linha do arquivo: um extrato com 400
	// linhas viraria 400 idas ao banco, e o custo apareceria exatamente no
	// caminho em que o usuário está esperando a tela de revisão.
	WindowForDedup(ctx context.Context, householdID, accountID string, minDate, maxDate civil.Date) ([]DedupRow, error)

	// MaxDedupOrdinal devolve o maior ordinal já usado por esta chave na casa
	// (0 se nenhum), contando as linhas excluídas — elas continuam ocupando a
	// chave única.
	MaxDedupOrdinal(ctx context.Context, householdID, dedupKey string) (int, error)

	// RowsByDedupKeys devolve as ocorrências já gravadas das chaves
	// informadas, SEM janela de datas, incluindo as excluídas logicamente.
	//
	// Ela existe por um motivo específico, e é o mais caro desta entrega: a
	// chave NATURAL (o Identificador do banco) NÃO embute a data (§4.4 da spec
	// 0004), mas WindowForDedup filtra por occurred_on. Quando a data da mesma
	// transação muda mais do que DedupWindowDays entre dois downloads — o
	// usuário corrigindo a data no CSV, o emissor re-datando uma pendente que
	// liquidou —, a gêmea já gravada fica FORA da janela, a análise não a
	// enxerga, a linha volta classificada como "novo" (que entra por DEFAULT) e
	// o ordinal calculado passa a ser 2. O índice único não recusa nada: a
	// tupla (casa, chave, 2) está livre. A MESMA transação entra duas vezes, em
	// silêncio.
	//
	// Procurar PELA CHAVE fecha o buraco na raiz: a chave natural É a
	// identidade da transação no emissor, e identidade não tem data.
	//
	// Só para chave natural: a chave DERIVADA embute occurredOn, então toda
	// gêmea dela está necessariamente dentro da janela e esta consulta não teria
	// o que acrescentar — só custo.
	RowsByDedupKeys(ctx context.Context, householdID string, dedupKeys []string) ([]DedupKeyRow, error)

	// ExternalIDsInWindow carrega, em UMA consulta, os identificadores que o
	// documento trouxe e que já estão em uso em OUTRAS contas da casa, dentro
	// da mesma janela de datas.
	//
	// É a segunda metade da marcação fraca (ADR-025e): "esta linha já foi
	// importada na conta X". Ela existe porque a conta entra na chave natural
	// (§4.2 da spec 0004) — sem a conta na chave, importar na conta errada
	// bloquearia a conta certa para sempre; com a conta na chave, a segunda
	// importação passa, e esta consulta é o que mostra ao usuário o que
	// aconteceu em vez de deixá-lo com a linha duplicada em duas contas sem
	// aviso nenhum.
	//
	// A janela de datas não é economia: ela é o que permite a consulta usar
	// ix_transactions_occurred (household_id, occurred_on, id). Sem ela, um
	// IN (...) com 10.000 identificadores varreria a tabela inteira da casa —
	// exatamente o custo que a §4.8 existe para impedir.
	//
	// Só linhas VIVAS: marcar por causa de um lançamento que a pessoa apagou
	// seria devolver a ela, como suspeita, a decisão que ela já tomou.
	ExternalIDsInWindow(ctx context.Context, householdID, excludeAccountID string, minDate, maxDate civil.Date) (map[string]ExternalIDUse, error)

	// ImportBatchFootprint devolve o que um lote de importação DE FATO gravou:
	// a fatura a que as linhas ficaram ligadas.
	//
	// Existe para a idempotência do confirm (S9): o segundo clique precisa
	// devolver a MESMA resposta do primeiro, e a fatura não é coluna de
	// import_batches — é derivada do dado (ADR-017, ADR-023d). Usa
	// ix_transactions_batch (import_batch_id).
	//
	// Os PARES de transferência deixaram de ser derivados daqui no schema v4
	// (ADR-026g): a ação `link` move o import_batch_id de uma perna para o
	// lote que a vinculou, e contar pernas por lote passaria a mentir para os
	// dois lotes. Eles são coluna (import_batches.transfer_pairs_count).
	ImportBatchFootprint(ctx context.Context, householdID, importBatchID string) (ImportFootprint, error)

	// SoftDelete marca a exclusão lógica. Em finanças, histórico importa:
	// exclusão física apagaria a prova de que o dinheiro se moveu.
	SoftDelete(ctx context.Context, householdID, id string, at time.Time) error

	// Restore desfaz a exclusão lógica. Toca EXCLUSIVAMENTE deleted_at e
	// updated_at: valor, data, conta, categoria e competência do lançamento
	// restaurado são os originais, nunca os do arquivo que pediu a
	// restauração.
	Restore(ctx context.Context, householdID, id string, at time.Time) error

	// ExistsByAccount e ExistsByCategory alimentam o UsageChecker de conta e
	// de categoria (spec 0003 §1). Contam também o que foi excluído
	// logicamente: a pergunta que o 422 responde é "isto JÁ foi usado?", e
	// um lançamento excluído foi.
	ExistsByAccount(ctx context.Context, householdID, accountID string) (bool, error)
	ExistsByCategory(ctx context.Context, householdID, categoryID string) (bool, error)

	// ByTransferGroup devolve TODAS as pernas da transferência, INCLUSIVE as
	// excluídas logicamente (quem chama filtra por DeletedAt).
	//
	// Existe porque o ADR-016 é uma regra sobre o PAR, não sobre a linha:
	// excluir uma perna exclui a outra, e restaurar uma restaura a outra.
	// Meia transferência é dinheiro aparecendo de um lado sem sair do outro —
	// o saldo de uma das contas fica errado e ninguém consegue explicar por
	// quê. Sem esta consulta a regra não teria como ser cumprida: o par mora
	// em DUAS contas, e ByID/List só alcançam uma delas de cada vez.
	//
	// Devolve as excluídas porque a restauração precisa enxergá-las — ByID
	// esconde o que tem deleted_at, então sem isto a segunda perna ficaria
	// inalcançável justamente na operação que existe para trazê-la de volta.
	//
	// Usa o índice ix_transactions_group (household_id, transfer_group_id).
	ByTransferGroup(ctx context.Context, householdID, transferGroupID string) ([]Transaction, error)

	// SumByStatement agrega, por fatura, as linhas VIVAS ligadas a ela.
	//
	// É a fonte dos números derivados do ADR-023(d) — `totalCents`, `paidCents`
	// e, a partir deles, o `status`. Nenhum dos três é coluna: coluna
	// materializada de dinheiro só precisa de UM caminho de escrita esquecido
	// para ficar errada, e errada em silêncio.
	//
	// Em lote (uma consulta para a lista inteira de faturas) e não uma por
	// fatura, pelo mesmo motivo de WindowForDedup: a tela de faturas mostra 24
	// linhas, e 24 idas ao banco por tela é o tipo de custo que ninguém nota
	// até o dia em que nota.
	//
	// Usa o índice ix_transactions_statement (household_id, statement_id).
	SumByStatement(ctx context.Context, householdID string, statementIDs []string) (map[string]StatementSum, error)

	// --- schema v4 (spec 0005): auto-categorização ---------------------------

	// ListUncategorized devolve as receitas e despesas VIVAS do mês de
	// competência que ainda não têm categoria, em ordem (occurred_on, id) e até
	// `limit` linhas. Transferência nunca entra: não tem categoria por desenho.
	//
	// Peça o teto + 1 para descobrir que ele foi ultrapassado; o repositório
	// não conta. Usa ix_transactions_competence.
	ListUncategorized(ctx context.Context, householdID, competenceMonth string, limit int) ([]UncategorizedRow, error)

	// SetCategoryWhereNull grava a categoria nos lançamentos informados que
	// AINDA estão sem categoria — e só neles: linha com categoria escolhida
	// não é tocada, aconteça o que acontecer na camada de cima (spec 0005
	// §4.3, ADR-026h). Só receita/despesa viva. Devolve as linhas AFETADAS,
	// que é o número que a resposta mostra. IN fatiado.
	SetCategoryWhereNull(ctx context.Context, householdID string, ids []string, categoryID string, at time.Time) (int64, error)

	// UpdateCategory grava a categoria em UM lançamento (PATCH
	// /transactions/{id}, spec 0005 §11). O SET tem exatamente duas colunas
	// (category_id e updated_at); o WHERE leva casa, id, kind IN (income,
	// expense) e deleted_at IS NULL — perna de transferência, linha excluída,
	// inexistente ou de outra casa afeta ZERO linhas, e zero é ErrNotFound.
	// Diferente de SetCategoryWhereNull, aqui a categoria já escolhida É
	// substituída: é a pessoa recategorizando de propósito, uma linha por vez.
	UpdateCategory(ctx context.Context, householdID, id, categoryID string, at time.Time) error

	// --- schema v4 (spec 0005): GET /transfers ------------------------------

	// ListTransferLegs devolve a página de pernas-âncora do mês, ordenada por
	// (occurred_on DESC, id DESC), com o MESMO cursor de List. Sem AccountID,
	// uma perna por par (a de saída); com AccountID, as pernas da conta; com
	// CounterpartAccountID, só os grupos cuja outra perna está naquela conta.
	// Peça Limit+1 para saber se há próxima página.
	ListTransferLegs(ctx context.Context, householdID string, f TransferFilter) ([]Transaction, error)

	// ByTransferGroups devolve as pernas VIVAS dos grupos informados, em
	// ordem (transfer_group_id, kind, id) — é como a listagem acha a outra
	// perna de cada âncora em UMA consulta por fatia, e não uma por linha.
	ByTransferGroups(ctx context.Context, householdID string, groupIDs []string) ([]Transaction, error)

	// TransferLegsOfMonth devolve TODAS as pernas vivas do mês de competência
	// em quatro colunas, até `limit` — a matéria-prima dos totais por par,
	// somados em Go. Peça o teto + 1 para descobrir que foi ultrapassado.
	TransferLegsOfMonth(ctx context.Context, householdID, competenceMonth string, limit int) ([]TransferLegSummary, error)

	// SumByAccountUntil é SumByAccount restrito a occurred_on <= until: a
	// metade variável do saldo de CAIXA no fim de um mês (ADR-017).
	SumByAccountUntil(ctx context.Context, householdID string, until civil.Date) (map[string]int64, error)

	// --- schema v4 (spec 0005): ação `link` da importação --------------------

	// TransferLegsForLinking devolve as pernas de transferência VIVAS da conta
	// na janela [minDate - DedupWindowDays, maxDate + DedupWindowDays], com a
	// conta da outra perna resolvida. Perna cuja contraparte não está viva é
	// descartada — não há par para vincular. Duas consultas, nenhuma por
	// linha (ix_transactions_account_occurred e ix_transactions_group).
	TransferLegsForLinking(ctx context.Context, householdID, accountID string, minDate, maxDate civil.Date) ([]TransferLeg, error)

	// ByIDIncludingDeleted é ByID que ENXERGA a linha excluída logicamente.
	//
	// Existe para a reconferência do `link` no commit: "a perna foi excluída
	// entre a análise e o confirm" (linha bloqueada) é diferente de "a perna
	// não existe nesta casa" (lote inteiro falha), e ByID mistura os dois.
	// Outra casa continua sendo ErrNotFound.
	ByIDIncludingDeleted(ctx context.Context, householdID, id string) (*Transaction, error)

	// LinkImport grava na perna existente a identidade de deduplicação e o
	// lote da linha do arquivo (ADR-026f). O WHERE leva casa, id, a CONTA DO
	// LOTE e deleted_at IS NULL: perna de outra conta, excluída ou de outra
	// casa é ErrNotFound (nenhuma linha afetada). Chave já ocupada volta como
	// ErrDuplicateDedup — o índice único arbitra.
	LinkImport(ctx context.Context, householdID, id string, f LinkFields) error

	// OccurredOnByIDs devolve id -> occurred_on dos lançamentos informados,
	// inclusive os excluídos logicamente (a tela diz "já registrada em dd/mm"
	// mesmo se a perna sumiu entre a análise e a revisão). UMA consulta por
	// página, fatiada; id de outra casa não volta.
	OccurredOnByIDs(ctx context.Context, householdID string, ids []string) (map[string]civil.Date, error)

	// --- spec 0005 §13 (ADR-028): reprocessar transferências ----------------

	// ListTransferCandidates devolve as receitas e despesas VIVAS do mês de
	// competência que ainda NÃO têm transfer_group_id, em ordem (occurred_on,
	// id) e até `limit` linhas — a matéria-prima do pareamento. Perna de
	// transferência nunca entra: já é transferência. Peça o teto + 1 para
	// descobrir que ele foi ultrapassado. Usa ix_transactions_competence.
	//
	// DESPESA ligada a uma fatura fica de fora: converter uma compra do
	// cartão em transfer_out reduziria o total cobrado da fatura, que é o
	// número que a pessoa confere contra o banco. A RECEITA de fatura
	// continua entrando — é o pagamento da fatura, e virar transfer_in é o
	// comportamento desejado (ADR-016).
	ListTransferCandidates(ctx context.Context, householdID, competenceMonth string, limit int) ([]TransferCandidateRow, error)

	// IncomeExpenseInWindow devolve as receitas e despesas VIVAS da CASA
	// INTEIRA, de qualquer competência, com occurred_on em
	// [minDate - DedupWindowDays, maxDate + DedupWindowDays] — a folga é
	// aplicada AQUI, como em WindowForDedup, para o chamador passar o
	// intervalo REAL das candidatas e não precisar conhecer a constante. É o
	// pool onde o espelho de cada candidata é procurado; a competência do
	// espelho pode ser outra (linha de fatura — ADR-028c). Intervalo inválido
	// é ERRO, não janela vazia: uma janela vazia silenciosa faria toda
	// candidata parecer sem par. Em ordem (occurred_on, id), até `limit`.
	// Usa ix_transactions_occurred.
	//
	// A MESMA exclusão de ListTransferCandidates vale aqui: despesa de fatura
	// não é espelho, porque o espelho também é convertido — e converter a
	// perna de saída pela porta do espelho mudaria o total da fatura do mesmo
	// jeito.
	IncomeExpenseInWindow(ctx context.Context, householdID string, minDate, maxDate civil.Date, limit int) ([]TransferCandidateRow, error)

	// ConvertToTransferPair converte UM par em transferência com dois UPDATEs
	// condicionais (ADR-028d): SET kind, transfer_group_id, category_id =
	// NULL, updated_at WHERE household_id = ? AND id = ? AND deleted_at IS
	// NULL AND transfer_group_id IS NULL AND kind = <expense|income>. Cada um
	// tem de afetar EXATAMENTE uma linha; qualquer outro número é
	// ErrTransferConversionConflict. Nada mais muda: valor, data,
	// competência, descrição, source, lote, external_id, dedup_key,
	// dedup_ordinal e statement_id ficam como estão.
	//
	// SÓ dentro da transação: são dois comandos, e é o rollback que desfaz o
	// primeiro quando o segundo recusa. Meia transferência gravada seria
	// dinheiro saindo de uma conta sem entrar na outra (ADR-016).
	ConvertToTransferPair(ctx context.Context, householdID string, p TransferPairConversion) error

	// --- spec 0006 (ADR-029): investimentos e resgates ----------------------

	// SumInvestmentsByMonth soma, por mês de competência e por FLUXO, os
	// lançamentos vivos da casa cuja categoria está no conjunto informado, com
	// competence_month entre fromMonth e toMonth (inclusive nos dois lados).
	//
	// UMA consulta serve os TRÊS números da tela de investimentos: o mês, o
	// ano até o mês e a série de 12 meses. A janela do ano-até-o-mês
	// (Y-01..M) está sempre CONTIDA na dos 12 meses (M-11..M), então pedir a
	// maior das duas e montar as três em Go é uma ida ao banco, e não doze nem
	// duas. Mês sem movimento simplesmente não volta — quem chama preenche os
	// zeros da série.
	//
	// A comparação de mês é lexicográfica sobre "YYYY-MM", que é cronológica
	// por construção (a mesma propriedade que SumByAccountUntil usa em
	// "YYYY-MM-DD"). Usa ix_transactions_competence.
	//
	// Conjunto VAZIO é ErrEmptyCategoryFilter, nunca "todas as categorias".
	SumInvestmentsByMonth(ctx context.Context, householdID string, categoryIDs []string, fromMonth, toMonth string) ([]InvestmentMonthTotals, error)

	// ListByCategories devolve a página dos lançamentos VIVOS do mês de
	// competência cuja categoria está no conjunto informado, na MESMA ordem e
	// com o MESMO cursor de List — (occurred_on DESC, id DESC).
	//
	// Para saber se há próxima página, peça Limit+1 e descarte o excedente,
	// como em List: o repositório não faz COUNT.
	//
	// Conjunto VAZIO é ErrEmptyCategoryFilter: um slice vazio querendo dizer
	// "tudo" devolveria o mês inteiro na tela de investimentos.
	ListByCategories(ctx context.Context, householdID string, f CategoryListFilter) ([]Transaction, error)

	// ListIncomeExpenseOfMonth devolve as receitas e despesas VIVAS do mês de
	// competência — COM e SEM categoria —, em ordem (occurred_on, id) e até
	// `limit` linhas. É a matéria-prima do `detect` de investimentos, que
	// precisa das duas: as sem categoria ele marca, as com categoria ele lista
	// como `alreadyCategorized` (e só troca com overwriteCategorized).
	//
	// Peça o teto + 1 para descobrir que ele foi ultrapassado: é assim que o
	// 422 de MaxAutoCategorizeRows é decidido sem ler o mês inteiro. Usa
	// ix_transactions_competence.
	ListIncomeExpenseOfMonth(ctx context.Context, householdID, competenceMonth string, limit int) ([]CategorizableRow, error)

	// SetCategoryWhereCurrentIn troca a categoria dos lançamentos informados
	// que HOJE estão numa das categorias da allowlist — e só neles.
	//
	// É a escrita do `overwriteCategorized` (ADR-029h), a primeira do projeto
	// autorizada a substituir categoria já escolhida. A restrição que a torna
	// segura mora no WHERE, e não no Go: `category_id IN (<allowlist>)`, onde a
	// allowlist é o conjunto das categorias de natureza `income`/`expense` da
	// casa. Com isso a troca NUNCA desfaz uma marcação de investimento — nem a
	// de outra execução, nem a que a pessoa fez à mão —, aconteça o que
	// acontecer na camada de cima e caiba o que couber entre a prévia e a
	// confirmação.
	//
	// Linha sem categoria também não é alcançada (NULL não está em IN): quem
	// marca o que está vazio é SetCategoryWhereNull, e são operações
	// diferentes de propósito.
	//
	// Devolve as linhas AFETADAS. Allowlist vazia é ErrEmptyCategoryFilter —
	// vazia querendo dizer "qualquer categoria" transformaria a flag num
	// sobrescrevedor universal.
	SetCategoryWhereCurrentIn(ctx context.Context, householdID string, ids, currentCategoryIDs []string, categoryID string, at time.Time) (int64, error)
}

// DedupKeyRow é a projeção de IDENTIDADE de um lançamento: apenas o que
// responde "esta chave já está ocupada, e por quem".
//
// É um tipo próprio, e NÃO DedupRow, de propósito. Ele é o resultado de uma
// consulta que varre por chave, sem janela de datas, e trazer valor, descrição
// e data junto faria duas coisas ruins: pagaria tráfego por campo que ninguém
// compara e daria à camada de cima uma DedupRow pela metade, que um dia
// alguém usaria como se estivesse inteira (a marcação fraca compara valor e
// data — contra zeros, ela marcaria o que não deve). O tipo estreito torna o
// engano impossível em vez de improvável.
type DedupKeyRow struct {
	ID           string
	DedupKey     string
	DedupOrdinal int
	// DeletedAt é parte da projeção porque a linha excluída logicamente
	// continua ocupando a chave única: é ela que transforma a reimportação em
	// RESTAURAÇÃO (ADR-025f) em vez de erro.
	DeletedAt *time.Time
}

// ExternalIDUse localiza um identificador de documento já gravado: qual
// lançamento o carrega e em que conta.
type ExternalIDUse struct {
	TransactionID string
	AccountID     string
}

// ImportFootprint é o rastro que um lote de importação deixou em transactions.
//
// Só a fatura. Os pares de transferência e os vínculos (`link`) são COLUNAS do
// lote desde o schema v4 (ADR-026g) — ver ImportBatchFootprint.
type ImportFootprint struct {
	// StatementID é a fatura a que as linhas do lote ficaram ligadas. Nulo em
	// extrato, e nulo também quando o lote não gravou nenhuma linha.
	StatementID *string
}

// StatementSum são os números derivados de UMA fatura (ADR-023d).
//
// Não existe campo de status aqui: status depende de "hoje" no fuso da CASA
// (ADR-019a), que o repositório não conhece e não deve conhecer.
type StatementSum struct {
	// TotalCents é o que a fatura cobra: soma das despesas menos a soma das
	// receitas das linhas daquela fatura (crédito na fatura abate o total —
	// spec 0004 §3.2).
	TotalCents int64

	// PaidCents é a soma dos transfer_in ligados à fatura: pagar a fatura é
	// uma transferência da conta corrente para a conta do cartão (ADR-016), e
	// é a perna de ENTRADA no cartão que quita.
	PaidCents int64

	// LineCount conta as linhas vivas da fatura.
	LineCount int64
}
