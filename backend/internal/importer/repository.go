package importer

import (
	"context"
	"errors"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
)

// Este arquivo é a FRONTEIRA DE PERSISTÊNCIA da importação: o contrato do
// repositório e as duas entidades que ele grava e lê. O resto do pacote
// (detecção de formato, leitura dos arquivos, classificação das linhas) vive
// nos demais arquivos e subpacotes.
//
// Nada aqui conhece GORM: a implementação é
// internal/platform/storage/gormstore.ImportRepository (ADR-008).

// Estados do lote. Conjunto FECHADO.
//
// O lote nasce pendente, e só de pendente ele sai — a transição é condicional
// no banco (UpdateBatchStatus), e é isso que torna "confirmar" idempotente: o
// segundo clique não encontra mais nenhuma linha pendente para mudar e,
// portanto, não importa nada de novo.
const (
	BatchStatusPending   = "pending"
	BatchStatusCommitted = "committed"
	BatchStatusDiscarded = "discarded"
	BatchStatusExpired   = "expired"
)

var (
	// ErrBatchNotFound — lote inexistente ou de outra casa (o mesmo erro: S1).
	ErrBatchNotFound = errors.New("lote de importação não encontrado")

	// ErrDuplicateRow — o índice único (batch_id, seq) recusou a linha, ou
	// seja: aquela linha do arquivo já está gravada neste lote. É erro tipado
	// porque a resposta certa é "já registrei isso", nunca 500.
	ErrDuplicateRow = errors.New("linha do lote já gravada")
)

// Batch é uma tentativa de importação: um arquivo, uma conta, uma pessoa.
//
// Ele existe para que a importação tenha DUAS etapas — prévia e confirmação —
// sem guardar o arquivo em lugar nenhum. O que sobrevive entre as duas etapas
// é esta linha e as linhas já interpretadas (Row), nunca o documento original.
type Batch struct {
	ID          string
	HouseholdID string
	AccountID   string
	CreatedBy   string

	// Institution, DocKind e FormatID descrevem COMO o arquivo foi lido (qual
	// instituição, extrato ou fatura, qual leiaute). São vocabulário do
	// pacote, guardados como texto.
	Institution string
	DocKind     string
	FormatID    string

	// FileName é o nome do arquivo enviado, para que a pessoa reconheça o
	// lote na lista. Não há caminho e não há conteúdo.
	FileName string

	// ContentSHA256 é o hash do conteúdo. É o que permite dizer "você já
	// importou este arquivo" sem guardar o arquivo.
	ContentSHA256 string

	// Encoding é como os bytes foram interpretados (utf-8 ou windows-1252).
	// Fica guardado porque aparece na revisão: quando o usuário vê um acento
	// estranho, a resposta para "por quê?" precisa estar registrada, e não ser
	// recalculada a partir de um arquivo que não guardamos.
	Encoding string

	RowCount      int
	ImportedCount int
	SkippedCount  int
	BlockedCount  int
	RestoredCount int
	RejectedCount int

	// LinkedCount e TransferPairsCount são contadores do schema v4 (spec
	// 0005, ADR-026g). Eles são COLUNA, e não derivados dos lançamentos, por
	// um motivo preciso: a ação `link` move o import_batch_id de uma perna já
	// existente para o lote que a vinculou, e derivar "pares criados" de
	// import_batch_id passaria a mentir para os dois lotes.
	LinkedCount        int
	TransferPairsCount int

	// MinDate e MaxDate são o intervalo coberto pelo documento; a data zero
	// significa "arquivo sem nenhuma linha aproveitável".
	MinDate civil.Date
	MaxDate civil.Date

	// Suggested* são PALPITES da leitura do documento (competência e datas da
	// fatura), que a pessoa confirma ou corrige na tela de revisão. São
	// anuláveis porque nem todo documento os revela — e nenhum deles entra em
	// índice único, onde anulável seria a armadilha P3.
	SuggestedCompetenceMonth *string
	SuggestedClosingDate     *civil.Date
	SuggestedDueDate         *civil.Date

	Status string

	// ExpiresAt é obrigatório: lote pendente é dado financeiro parado
	// esperando confirmação, e ele tem prazo de validade.
	ExpiresAt   time.Time
	CommittedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Row é UMA linha já interpretada do documento.
//
// O que esta struct deliberadamente NÃO tem é a linha crua do arquivo.
// Guardá-la traria CPF, CNPJ, agência e conta de terceiros para dentro de uma
// tabela nova, com outro ciclo de vida e outro público de leitura — risco novo
// sem ganho: tudo o que a revisão precisa mostrar já está nos campos abaixo.
type Row struct {
	ID string
	// HouseholdID é repetido aqui de propósito, mesmo existindo em Batch: o
	// filtro de isolamento NUNCA pode depender de um join. Uma consulta que
	// esquece o join devolve tudo; uma que esquece a coluna nem passa pelo
	// scope do repositório.
	HouseholdID string
	BatchID     string

	// Seq é a ordem estável dentro do lote (base do cursor); LineNo é a linha
	// física no arquivo, que é o que a pessoa procura quando quer conferir.
	Seq    int
	LineNo int

	Kind            string
	OccurredOn      civil.Date
	AmountCents     int64
	Description     string
	DescriptionNorm string
	ExternalID      *string
	DedupKey        string

	// Status é o veredito da prévia (importar, pular, bloquear, restaurar,
	// rejeitar) — vocabulário do serviço de importação, guardado como texto.
	Status string

	// RejectReason é um CÓDIGO curto, nunca o conteúdo da linha: a mensagem
	// para o usuário é montada na borda, e um campo livre aqui viraria o lugar
	// onde o documento inteiro acabaria copiado.
	RejectReason *string

	// MatchTransactionID aponta o lançamento que a linha encontrou (duplicata,
	// candidato a restauração ou — schema v4 — a perna de transferência já
	// registrada que a ação `link` vincula).
	MatchTransactionID *string

	// --- schema v4 (spec 0005): sugestões da análise por palavra-chave.
	//
	// Todas ANULÁVEIS: são palpites calculados no servidor, e "sem sugestão" é
	// diferente de "sugestão vazia". Nenhuma vem do cliente (S2).
	//
	// SuggestedCategoryID é a categoria que as palavras-chave sugeriram —
	// o default da linha no confirm. MatchScore/MatchedKeyword descrevem a
	// categoria quando há SuggestedCategoryID, e a contraparte em linha
	// transferencia_* (emenda §10.4 da spec). MatchedKeyword é a forma
	// EXIBÍVEL, não a norm. SuggestedCounterpartAccountID é a conta da outra
	// perna sugerida pelas palavras-chave de conta.
	SuggestedCategoryID           *string
	MatchScore                    *int
	MatchedKeyword                *string
	SuggestedCounterpartAccountID *string

	CreatedAt time.Time
}

// BatchOutcome são os contadores do resultado da confirmação.
//
// Vão junto com a mudança de estado, no MESMO UPDATE condicional, porque
// "confirmado" e "importou N linhas" são o mesmo fato: gravá-los em dois
// comandos abriria uma janela em que o lote está confirmado e vazio.
type BatchOutcome struct {
	ImportedCount int
	SkippedCount  int
	BlockedCount  int
	RestoredCount int
	RejectedCount int

	// LinkedCount e TransferPairsCount (schema v4, ADR-026g) vão no mesmo
	// UPDATE condicional dos demais contadores.
	LinkedCount        int
	TransferPairsCount int
}

// Repository persiste lotes e linhas de importação.
//
// Todo método recebe householdID e o aplica no WHERE — com uma exceção
// declarada: ExpireBatches, que é varredura de manutenção e não devolve dado
// de ninguém (ver o comentário no método).
type Repository interface {
	// CreateBatch insere o lote.
	CreateBatch(ctx context.Context, b *Batch) error

	// BatchByID devolve o lote da casa; de outra casa é ErrBatchNotFound.
	BatchByID(ctx context.Context, householdID, id string) (*Batch, error)

	// ListBatches devolve os lotes da casa, do mais recente para o mais
	// antigo.
	ListBatches(ctx context.Context, householdID string, limit int) ([]Batch, error)

	// UpdateBatchStatus faz a transição CONDICIONAL de estado e devolve
	// quantas linhas mudaram.
	//
	// O from entra no WHERE junto com a casa e o id. É esse WHERE que torna a
	// confirmação idempotente e segura em concorrência: duas confirmações
	// simultâneas disputam a MESMA linha, e o banco garante que só uma vê
	// RowsAffected = 1. Quem recebe 0 não importou nada e não deve tratar isso
	// como erro — deve reler o lote.
	//
	// outcome nil não toca nos contadores; presente, grava TODOS eles —
	// inclusive linked_count e transfer_pairs_count (schema v4). Quando to é
	// BatchStatusCommitted, committed_at é carimbado no mesmo comando.
	UpdateBatchStatus(ctx context.Context, householdID, id, from, to string, at time.Time, outcome *BatchOutcome) (int64, error)

	// CreateRows insere as linhas do lote de uma vez (uma transação, poucos
	// comandos).
	CreateRows(ctx context.Context, householdID string, rows []Row) error

	// ListRows devolve as linhas do lote em ordem de seq, paginadas por cursor
	// (afterSeq). Zero em afterSeq começa do início.
	ListRows(ctx context.Context, householdID, batchID string, afterSeq, limit int) ([]Row, error)

	// RowsByIDs devolve as linhas escolhidas pela pessoa na revisão,
	// filtrando pela casa: id de linha de outra casa simplesmente não volta.
	RowsByIDs(ctx context.Context, householdID string, ids []string) ([]Row, error)

	// DeleteRows apaga as linhas do lote (exclusão FÍSICA: linha de prévia não
	// é dado financeiro, é rascunho) e devolve quantas apagou.
	DeleteRows(ctx context.Context, householdID, batchID string) (int64, error)

	// ExpireBatches marca como expirado todo lote PENDENTE cujo prazo passou.
	//
	// É o único método sem householdID, e a exceção é deliberada: ele é
	// varredura de manutenção, roda fora de qualquer requisição, não devolve
	// nenhum dado e não muda nada além do estado de lotes já vencidos.
	ExpireBatches(ctx context.Context, now time.Time) (int64, error)

	// CommittedByContentHash devolve o lote CONFIRMADO mais recente da casa
	// com aquele conteúdo — é como a API avisa "este arquivo já foi
	// importado" sem guardar o arquivo (§4.1). Procura em qualquer conta da
	// casa, e nunca fora dela.
	//
	// Duas restrições estão no NOME do método de propósito, porque as duas são
	// fáceis de esquecer em um chamador novo:
	//
	//  1. só lote CONFIRMADO E QUE GRAVOU conta. Pendente, descartado e
	//     expirado não puseram lançamento nenhum no lugar — e confirmado
	//     tampouco garante isso: confirmar com todas as linhas em `skip`
	//     fecha o lote com imported_count = 0. O aviso promete o contrário;
	//  2. exceptBatchID sai do resultado. É o lote corrente: sem ele, a
	//     revisão de um lote já confirmado encontraria a si mesma e avisaria
	//     sobre a própria importação. Vazio não exclui nada (é o caso da fase
	//     1, em que o lote ainda nem existe).
	//
	// Nenhuma ocorrência é ErrBatchNotFound — "não achei" e "não existe" são o
	// mesmo caso aqui.
	CommittedByContentHash(ctx context.Context, householdID, contentSHA256, exceptBatchID string) (*Batch, error)

	// CountRowsByStatus conta as linhas por status, para VÁRIOS lotes de uma
	// vez: o resultado é batchID -> status -> quantidade.
	//
	// É o que alimenta os `counts` da revisão sem paginar o lote inteiro — um
	// arquivo de 10.000 linhas mostraria 100 páginas antes de a tela conseguir
	// escrever "12 novas, 3 possíveis duplicatas".
	//
	// Em lote, e não um por vez, pelo mesmo motivo de SumByStatement: o
	// histórico mostra 50 lotes, e 50 agregações por tela é o tipo de custo que
	// ninguém nota até o dia em que nota.
	CountRowsByStatus(ctx context.Context, householdID string, batchIDs []string) (map[string]map[string]int, error)

	// DeleteStaleRows apaga as linhas de *staging* de todo lote que não está
	// mais PENDENTE — confirmado, descartado ou expirado.
	//
	// É varredura de manutenção e, como ExpireBatches, não recebe householdID:
	// ela não devolve dado de ninguém. A condição é sobre o ESTADO do lote, e
	// não sobre uma lista de ids lida antes, de propósito: entre a leitura e a
	// exclusão cabe uma confirmação inteira, e apagar as linhas de um lote que
	// acabou de virar pendente-confirmado no meio do caminho tiraria o chão de
	// quem está confirmando.
	DeleteStaleRows(ctx context.Context) (int64, error)

	// PurgeTerminalBatches apaga os lotes TERMINAIS criados antes de `before`.
	//
	// Lote pendente nunca é apagado por aqui: ele é expirado primeiro
	// (ExpireBatches), e só então vira terminal. A retenção é a mesma da
	// auditoria — 180 dias —, porque é o mesmo tipo de rastro.
	PurgeTerminalBatches(ctx context.Context, before time.Time) (int64, error)
}
