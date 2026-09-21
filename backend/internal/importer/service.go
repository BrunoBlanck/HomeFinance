package importer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/id"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Este arquivo é o SERVIÇO da importação: quem costura o leitor de arquivo, a
// deduplicação, o staging e a escrita de lançamentos. A fase 1 mora em
// analyze.go e a fase 2 em confirm.go; aqui ficam o tipo, as dependências, os
// limites e o que as duas fases compartilham.
//
// A separação que ele sustenta, e que é a razão de a importação ter DUAS fases
// (ADR-024c): POST /imports não cria lançamento nenhum — ele analisa e grava um
// rascunho com prazo. Só o confirm escreve em transactions, e escreve tudo
// dentro de UMA transação.

// Limites e prazos da importação (§5.6 da spec 0004).
const (
	// MaxUploadBytes é o teto do arquivo enviado. A borda HTTP já corta em 8
	// MiB por rota (httpserver.MaxBytesByPath); a repetição aqui é deliberada:
	// limite que só existe no chamador deixa de existir no primeiro chamador
	// novo.
	MaxUploadBytes = 8 << 20

	// BatchTTL é por quanto tempo o lote pendente sobrevive.
	//
	// 24 h porque é dado financeiro parado numa tabela de rascunho — quanto
	// menos tempo, melhor —, mas menos de um dia transformaria "vou revisar
	// depois do jantar" em "começa tudo de novo".
	BatchTTL = 24 * time.Hour

	// AnalyzeTimeout é o prazo da fase 1.
	//
	// Tem de morrer ANTES do WriteTimeout do servidor (30 s): um contexto sem
	// prazo próprio faz o cliente receber uma conexão cortada em vez de uma
	// resposta, e o lote fica gravado sem ninguém saber.
	AnalyzeTimeout = 15 * time.Second

	// MaxDecisions é o teto de exceções no corpo do confirm. Existe para o
	// corpo caber no limite global de 1 MiB: com 10.000 rowId ele estouraria.
	MaxDecisions = 2000

	// MaxBlockedRowsReported é o teto da lista blockedRows da resposta (o
	// contrato publica maxItems 2000). O CONTADOR `blocked` continua completo;
	// só a lista é cortada.
	MaxBlockedRowsReported = 2000

	// TerminalRetention é por quanto tempo o lote terminal fica no histórico.
	// É a mesma retenção da auditoria, porque é o mesmo tipo de rastro.
	TerminalRetention = 180 * 24 * time.Hour

	// DefaultDueGapDays é o intervalo entre fechamento e vencimento usado na
	// SUGESTÃO quando a conta não tem os dias configurados.
	//
	// É palpite exibido, nunca decisão: o usuário confirma as três datas no
	// passo de revisão, e é a confirmação dele que vira a fatura.
	DefaultDueGapDays = 10
)

// Erros do serviço. Nenhum carrega conteúdo do arquivo nem a senha do ZIP.
var (
	// ErrAccountNotFound — conta de destino inexistente OU de outra casa. Os
	// dois casos são o MESMO erro (S1) e viram 404.
	ErrAccountNotFound = errors.New("conta de destino não encontrada")

	// ErrAccountArchived — conta arquivada como destino. 422 e não 404: a
	// conta É da casa, e basta desarquivar.
	ErrAccountArchived = errors.New("conta de destino arquivada")

	// ErrTargetMismatch — o arquivo não corresponde à conta escolhida: a
	// instituição detectada é outra, ou é fatura numa conta que não é cartão
	// (ou extrato numa que é).
	//
	// A tela manda TROCAR A CONTA DE DESTINO, não o arquivo — é por isso que
	// ele tem código HTTP próprio (IMPORT_TARGET_MISMATCH).
	ErrTargetMismatch = errors.New("o arquivo não corresponde à conta de destino")

	// ErrStatementRequired — fatura sem o bloco `statement` confirmado. As
	// datas da fatura decidem a competência de TODAS as linhas do documento;
	// adivinhá-las estragaria dezenas de lançamentos de uma vez.
	ErrStatementRequired = errors.New("a confirmação da fatura é obrigatória neste lote")

	// ErrStatementNotAllowed — bloco `statement` num lote de extrato.
	ErrStatementNotAllowed = errors.New("este lote não é de fatura")

	// ErrTooManyDecisions — corpo do confirm acima de MaxDecisions.
	ErrTooManyDecisions = errors.New("decisões demais no corpo")

	// ErrDuplicateDecision — a mesma linha citada duas vezes. Não é ignorável:
	// a segunda decisão poderia contradizer a primeira, e "a última vence" é
	// uma regra que ninguém consegue ver na tela.
	ErrDuplicateDecision = errors.New("linha citada mais de uma vez")

	// ErrRowNotInBatch — rowId que não pertence a este lote (ou é de outra
	// casa). É 400, NUNCA "ignorado em silêncio" (§6.6).
	ErrRowNotInBatch = errors.New("linha não pertence a este lote")

	// ErrActionNotAllowed — ação fora de AllowedActionsFor(status) daquela
	// linha.
	ErrActionNotAllowed = errors.New("ação não oferecida para esta linha")

	// ErrCounterpartRequired / ErrCounterpartNotAllowed — a conta da outra
	// perna só existe em `transfer`, e em `transfer` ela é obrigatória.
	ErrCounterpartRequired   = errors.New("a transferência exige a conta da outra perna")
	ErrCounterpartNotAllowed = errors.New("conta da outra perna só existe em transferência")

	// ErrStatementOnlyOnTransfer — statementId numa decisão que não é
	// transferência. Quem liga a linha comum à fatura é o lote, não o cliente.
	ErrStatementOnlyOnTransfer = errors.New("a fatura da decisão só existe em transferência")

	// ErrCategoryOnlyOnImport — categoria numa decisão que não grava nada.
	ErrCategoryOnlyOnImport = errors.New("categoria só existe em decisão de importar")

	// ErrSameAccountTransfer — a outra perna aponta para a própria conta do
	// lote. Transferir para si mesmo é um no-op que ainda dobraria a linha no
	// extrato daquela conta.
	ErrSameAccountTransfer = errors.New("a outra perna precisa ser uma conta diferente")
)

// Actor é quem está agindo: a casa e o usuário vêm do TOKEN (nunca do corpo,
// da query ou do caminho), e o IP vem da borda HTTP.
type Actor struct {
	HouseholdID string
	UserID      string
	IP          string
}

// Auditor registra o rastro, DENTRO da transação que ele descreve.
type Auditor interface {
	Record(ctx context.Context, p AuditParams) error
}

// AuditParams é o evento a registrar. Espelha audit.Params e, como ele, NÃO
// tem campo de valor: nem no lote, onde a tentação seria somar o total (S8).
type AuditParams struct {
	Action      string
	Entity      string
	EntityID    string
	UserID      string
	HouseholdID string
	IP          string
}

// Transactor executa uma função dentro de uma transação. Sem chave estrangeira
// física (ADR-013), conferir e escrever precisa ser atômico.
type Transactor interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Accounts é o que a importação precisa saber da conta de destino: se é da
// casa, se está arquivada, qual a instituição declarada e quais os dias de
// fechamento e vencimento da fatura.
type Accounts interface {
	ByID(ctx context.Context, householdID, id string) (*account.Account, error)
}

// Ledger é a LEITURA do domínio de lançamentos de que a análise depende.
//
// É o repositório, e não o serviço: são duas consultas de projeção que não têm
// regra de negócio nenhuma em cima. A escrita é outra interface (Writer), e
// essa sim passa pelo serviço.
type Ledger interface {
	WindowForDedup(ctx context.Context, householdID, accountID string, minDate, maxDate civil.Date) ([]transaction.DedupRow, error)

	// RowsByDedupKeys é o complemento de WindowForDedup para a chave NATURAL,
	// que não embute data: sem ele, a gêmea de uma linha re-datada fica fora da
	// janela e a mesma transação entra duas vezes (ver o comentário do método
	// na interface do repositório).
	RowsByDedupKeys(ctx context.Context, householdID string, dedupKeys []string) ([]transaction.DedupKeyRow, error)

	ExternalIDsInWindow(ctx context.Context, householdID, excludeAccountID string, minDate, maxDate civil.Date) (map[string]transaction.ExternalIDUse, error)
	ImportBatchFootprint(ctx context.Context, householdID, importBatchID string) (transaction.ImportFootprint, error)

	// TransferLegsForLinking devolve as pernas de transferência VIVAS da conta
	// do lote na janela do arquivo (± DedupWindowDays), com a conta da outra
	// perna resolvida — as candidatas ao pareamento de `transferencia_ja_registrada`
	// (spec 0005 §4.2.1.2). Duas consultas por lote, nenhuma por linha.
	TransferLegsForLinking(ctx context.Context, householdID, accountID string, minDate, maxDate civil.Date) ([]transaction.TransferLeg, error)

	// OccurredOnByIDs devolve id -> occurred_on dos lançamentos apontados pela
	// página da revisão (`matchOccurredOn`). UMA consulta por página; id de
	// outra casa simplesmente não volta.
	OccurredOnByIDs(ctx context.Context, householdID string, ids []string) (map[string]civil.Date, error)
}

// Writer é a ESCRITA de lançamentos, e é o SERVIÇO de lançamentos — nunca o
// repositório.
//
// A diferença importa: é no serviço que moram a conferência de que a conta é da
// casa dentro da transação, a compatibilidade de categoria, a competência da
// fatura e a atribuição do ordinal de deduplicação. Escrever pelo repositório
// pularia todas elas de uma vez.
type Writer interface {
	CreateBatch(ctx context.Context, ator transaction.Actor, in transaction.CreateBatchInput) (transaction.CreateBatchResult, error)
	Restore(ctx context.Context, ator transaction.Actor, transactionID string) (transaction.View, error)

	// LinkImport é a ação `link` (spec 0005 §4.2.3, ADR-026f): grava na perna
	// existente a identidade de deduplicação e o lote da linha. Reconfere a
	// perna por casa E conta do lote dentro da transação; os três erros
	// (ErrLinkTargetMissing/Deleted/Invalid) são tratados um a um no confirm.
	LinkImport(ctx context.Context, ator transaction.Actor, in transaction.LinkImportInput) error
}

// Statements cria ou REUSA a fatura do lote (idempotente por casa, conta e
// competência).
type Statements interface {
	Upsert(ctx context.Context, ator cardstatement.Actor, in cardstatement.UpsertInput) (cardstatement.View, error)
}

// Service concentra a regra de negócio da importação.
type Service struct {
	repo       Repository
	registry   *Registry
	accounts   Accounts
	ledger     Ledger
	writer     Writer
	statements Statements
	tx         Transactor
	// classifier carrega, uma vez por lote, as palavras-chave da casa para a
	// sugestão de categoria e de contraparte (spec 0005 §4.2.1). Obrigatório
	// no construtor; a análise que o consome entra com os status novos.
	classifier *classify.Loader
	audit      Auditor
	ids        id.Generator
	clock      func() time.Time
	limits     Limits
	ttl        time.Duration
	timeout    time.Duration
}

// Option configura o Service.
type Option func(*Service)

// WithIDs injeta o gerador de IDs (teste).
func WithIDs(g id.Generator) Option { return func(s *Service) { s.ids = g } }

// WithClock injeta o relógio (teste e janitor).
func WithClock(c func() time.Time) Option { return func(s *Service) { s.clock = c } }

// WithAudit liga o rastro de auditoria.
func WithAudit(a Auditor) Option { return func(s *Service) { s.audit = a } }

// WithLimits aperta os limites de leitura. Só APERTA: Limits.normalized()
// devolve o teto duro para quem tentar afrouxar.
func WithLimits(l Limits) Option { return func(s *Service) { s.limits = l } }

// WithTTL ajusta o prazo do lote pendente (teste do janitor).
func WithTTL(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.ttl = d
		}
	}
}

// WithAnalyzeTimeout ajusta o prazo da fase 1 (teste).
//
// ELA CONSEGUE ESTICAR o prazo, e isso é deliberado — foi avaliado no achado
// A7 da revisão de segurança e mantido. Limitá-la por AnalyzeTimeout é uma
// linha, mas quebra o único chamador que existe: o teste de desempenho do
// critério 11 (internal/importer/qa_e2c_desempenho_test.go) afrouxa o prazo
// para 5 minutos de propósito, porque sob `-race` e com o pacote inteiro
// rodando a fase 1 passa dos 15 s e o gate de PIPELINE viraria vermelho por
// carga de máquina. O número de produção continua medido e registrado lá.
//
// O que sustenta o prazo de produção não é esta função, é cmd/api: ele monta o
// Service sem opção nenhuma, e AnalyzeTimeout é o valor do construtor. Uma
// Option de teste só afrouxa o que o teste montar.
func WithAnalyzeTimeout(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.timeout = d
		}
	}
}

// NewService monta o serviço.
//
// As oito dependências obrigatórias são posicionais de propósito: esquecer de
// ligar a escrita de lançamentos vira erro de COMPILAÇÃO, e não uma importação
// que confirma e não grava nada.
func NewService(
	repo Repository,
	registry *Registry,
	accounts Accounts,
	ledger Ledger,
	writer Writer,
	statements Statements,
	tx Transactor,
	classifier *classify.Loader,
	opts ...Option,
) *Service {
	s := &Service{
		repo:       repo,
		registry:   registry,
		accounts:   accounts,
		ledger:     ledger,
		writer:     writer,
		statements: statements,
		tx:         tx,
		classifier: classifier,
		ids:        id.New,
		clock:      func() time.Time { return time.Now().UTC() },
		limits:     DefaultLimits(),
		ttl:        BatchTTL,
		timeout:    AnalyzeTimeout,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ListBatches devolve o histórico de lotes da casa, com os contadores dos que
// ainda estão pendentes.
//
// Os `counts` de TODOS os pendentes saem numa consulta só: o histórico mostra
// até 50 lotes, e uma agregação por linha da lista é o tipo de custo que
// ninguém nota até o dia em que nota. Lote terminal não tem contadores — as
// linhas de staging foram apagadas —, e por isso ele publica `outcome`.
func (s *Service) ListBatches(ctx context.Context, ator Actor, limit int) (BatchListView, error) {
	if ator.HouseholdID == "" {
		return BatchListView{}, ErrBatchNotFound
	}
	lotes, err := s.repo.ListBatches(ctx, ator.HouseholdID, limit)
	if err != nil {
		return BatchListView{}, fmt.Errorf("listando lotes de importação: %w", err)
	}

	pendentes := make([]string, 0, len(lotes))
	for i := range lotes {
		if lotes[i].Status == BatchStatusPending {
			pendentes = append(pendentes, lotes[i].ID)
		}
	}

	porLote := map[string]map[string]int{}
	if len(pendentes) > 0 {
		porLote, err = s.repo.CountRowsByStatus(ctx, ator.HouseholdID, pendentes)
		if err != nil {
			return BatchListView{}, fmt.Errorf("contando linhas dos lotes: %w", err)
		}
	}

	itens := make([]BatchView, 0, len(lotes))
	for i := range lotes {
		var contadores *CountsView
		if lotes[i].Status == BatchStatusPending {
			c := countsFrom(porLote[lotes[i].ID])
			contadores = &c
		}
		// sameContentImportedAt fica nulo no HISTÓRICO, e é decisão, não
		// esquecimento: o aviso existe para a tela de revisão decidir sobre UM
		// lote, e enchê-lo aqui custaria uma consulta por linha da lista — 50
		// por tela, pela mesma razão que os contadores saem em uma só.
		itens = append(itens, toBatchView(&lotes[i], contadores, nil))
	}
	return BatchListView{Items: itens}, nil
}

// Preview devolve o lote, os contadores e uma página de linhas.
//
// Lote de outra casa vira ErrBatchNotFound aqui dentro, no repositório, e a
// resposta fica byte a byte igual à de lote inexistente (S1).
func (s *Service) Preview(ctx context.Context, ator Actor, batchID string, afterSeq, limit int) (PreviewView, error) {
	if ator.HouseholdID == "" {
		return PreviewView{}, ErrBatchNotFound
	}

	lote, err := s.repo.BatchByID(ctx, ator.HouseholdID, batchID)
	if err != nil {
		return PreviewView{}, err
	}

	var contadores *CountsView
	if lote.Status == BatchStatusPending {
		porLote, err := s.repo.CountRowsByStatus(ctx, ator.HouseholdID, []string{lote.ID})
		if err != nil {
			return PreviewView{}, fmt.Errorf("contando linhas do lote: %w", err)
		}
		c := countsFrom(porLote[lote.ID])
		contadores = &c
	}

	// Uma linha a mais para saber se existe próxima página sem pagar um COUNT
	// por página.
	linhas, err := s.repo.ListRows(ctx, ator.HouseholdID, lote.ID, afterSeq, limit+1)
	if err != nil {
		return PreviewView{}, fmt.Errorf("listando linhas do lote: %w", err)
	}

	var proximo *int
	if limit > 0 && len(linhas) > limit {
		linhas = linhas[:limit]
		seq := linhas[len(linhas)-1].Seq
		proximo = &seq
	}

	// `matchOccurredOn` (spec 0005): a data do lançamento apontado por cada
	// linha da PÁGINA, numa consulta só — nunca uma por linha. É o que deixa a
	// tela dizer "já registrada em 05/09" em vez de só "já registrada".
	datas, err := s.datasDosApontados(ctx, ator.HouseholdID, linhas)
	if err != nil {
		return PreviewView{}, err
	}

	itens := make([]RowView, 0, len(linhas))
	for i := range linhas {
		itens = append(itens, toRowView(&linhas[i], datas))
	}

	// O aviso "este mesmo conteúdo já foi importado" é recalculado AQUI, e não
	// só na resposta do envio: a tela que o exibe é a de REVISÃO, e ela carrega
	// o lote por este endpoint. Enquanto o campo só existia no 201 do POST, o
	// componente existia, tinha teste e ninguém nunca o via.
	//
	// É uma consulta a mais por lote aberto — um ponto no índice
	// (household_id, content_sha256) —, e não uma por linha da revisão.
	jaImportadoEm, err := s.avisoDeConteudoRepetido(ctx, ator.HouseholdID, lote.ContentSHA256, lote.ID)
	if err != nil {
		return PreviewView{}, err
	}

	return PreviewView{
		Batch:      toBatchView(lote, contadores, jaImportadoEm),
		Items:      itens,
		NextCursor: proximo,
	}, nil
}

// datasDosApontados carrega, numa consulta só, a data (occurred_on) dos
// lançamentos apontados por MatchTransactionID nas linhas da página.
//
// Página sem nenhum apontamento não consulta nada. O repositório filtra pela
// casa do token: um id que não seja da casa simplesmente não volta, e a linha
// sai com `matchOccurredOn` nulo — nunca com a data de outra casa (S1).
func (s *Service) datasDosApontados(ctx context.Context, householdID string, linhas []Row) (map[string]civil.Date, error) {
	ids := make([]string, 0, len(linhas))
	for i := range linhas {
		if id := linhas[i].MatchTransactionID; id != nil && *id != "" {
			ids = append(ids, *id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	datas, err := s.ledger.OccurredOnByIDs(ctx, householdID, ids)
	if err != nil {
		return nil, fmt.Errorf("carregando datas dos lançamentos apontados: %w", err)
	}
	return datas, nil
}

// avisoDeConteudoRepetido é a camada 1 da deduplicação (§4.1): procura outra
// importação da casa com o MESMO conteúdo e devolve quando ela entrou.
//
// É a única fonte do campo `sameContentImportedAt`, usada pelas duas fases de
// propósito: o defeito que ela corrige nasceu justamente de a fase 1 calcular o
// aviso e a revisão devolver nulo.
//
// Só lote que de fato GRAVOU conta, e a razão é o que a frase promete. "Você já
// importou este arquivo" dito sobre um lote descartado, expirado, ainda
// pendente — ou confirmado com todas as linhas em `skip`, que fecha com
// imported_count = 0 — é falso: nenhum deles pôs um lançamento sequer no lugar.
// E o estrago não é cosmético: quem acredita no aviso pula uma importação
// legítima, e linha que nunca entrou é o defeito que a pessoa só descobre meses
// depois, conferindo o extrato. Duplicata ela vê e apaga; ausência, não.
//
// exceptBatchID é o lote corrente. Sem ele, a revisão de um lote já confirmado
// encontraria a si mesma. Vazio na fase 1, onde o lote ainda não existe.
//
// AVISO, nunca bloqueio (ADR-025d): a falta dele não impede importação nenhuma.
func (s *Service) avisoDeConteudoRepetido(ctx context.Context, householdID, contentSHA256, exceptBatchID string) (*time.Time, error) {
	if householdID == "" || contentSHA256 == "" {
		return nil, nil
	}

	anterior, err := s.repo.CommittedByContentHash(ctx, householdID, contentSHA256, exceptBatchID)
	if err != nil {
		if errors.Is(err, ErrBatchNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("procurando importação anterior do mesmo conteúdo: %w", err)
	}

	// A data publicada é a da CONFIRMAÇÃO, não a da criação do rascunho: o
	// campo do contrato se chama sameContentImportedAt, e entre enviar e
	// confirmar cabe um dia inteiro (BatchTTL). O fallback existe porque o
	// aviso não pode sumir por causa de um carimbo ausente — UpdateBatchStatus
	// grava status e committed_at no MESMO comando, então ele não deveria
	// faltar nunca.
	quando := anterior.CreatedAt
	if anterior.CommittedAt != nil {
		quando = *anterior.CommittedAt
	}
	return &quando, nil
}

// Discard descarta o lote pendente e apaga as linhas de staging.
//
// A transição é CONDICIONAL (pending -> discarded): lote já confirmado, já
// descartado ou expirado não muda linha nenhuma, e a resposta é 404 — não se
// descarta o que já virou lançamento.
func (s *Service) Discard(ctx context.Context, ator Actor, batchID string) error {
	if ator.HouseholdID == "" {
		return ErrBatchNotFound
	}

	return s.tx.Do(ctx, func(ctx context.Context) error {
		agora := s.clock()

		afetadas, err := s.repo.UpdateBatchStatus(ctx, ator.HouseholdID, batchID,
			BatchStatusPending, BatchStatusDiscarded, agora, nil)
		if err != nil {
			return fmt.Errorf("descartando lote: %w", err)
		}
		if afetadas == 0 {
			return ErrBatchNotFound
		}

		if _, err := s.repo.DeleteRows(ctx, ator.HouseholdID, batchID); err != nil {
			return fmt.Errorf("apagando linhas do lote descartado: %w", err)
		}
		return s.registrar(ctx, ator, audit.ActionImportDiscarded, batchID)
	})
}

// PurgeExpired é a varredura do janitor (T11 / §3.6 da spec 0004).
//
// Três passos, nesta ordem, e a ordem importa: expirar primeiro faz o lote
// vencido virar terminal, o que o torna elegível para ter as linhas apagadas na
// MESMA passada; apagar os lotes antigos por último garante que nenhuma linha
// fique órfã de lote.
//
// Fora de qualquer requisição e sem householdID: nenhum passo devolve dado de
// ninguém, e todos são condicionados ao ESTADO do lote.
func (s *Service) PurgeExpired(ctx context.Context) (expirados, linhas, lotes int64, err error) {
	agora := s.clock()

	expirados, err = s.repo.ExpireBatches(ctx, agora)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("expirando lotes: %w", err)
	}

	linhas, err = s.repo.DeleteStaleRows(ctx)
	if err != nil {
		return expirados, 0, 0, fmt.Errorf("apagando linhas de lotes terminais: %w", err)
	}

	lotes, err = s.repo.PurgeTerminalBatches(ctx, agora.Add(-TerminalRetention))
	if err != nil {
		return expirados, linhas, 0, fmt.Errorf("apagando lotes antigos: %w", err)
	}
	return expirados, linhas, lotes, nil
}

// --- apoio compartilhado pelas duas fases ---------------------------------

// contaDeDestino confere que a conta é da casa e que não está arquivada.
//
// A tradução do erro importa: conta de outra casa vira o MESMO
// ErrAccountNotFound de conta inexistente, para a resposta não confirmar a
// existência do recurso alheio (S1).
func (s *Service) contaDeDestino(ctx context.Context, householdID, accountID string) (*account.Account, error) {
	if accountID == "" {
		return nil, ErrAccountNotFound
	}
	c, err := s.accounts.ByID(ctx, householdID, accountID)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return nil, ErrAccountNotFound
		}
		return nil, fmt.Errorf("buscando conta de destino: %w", err)
	}
	if c.ArchivedAt != nil {
		return nil, ErrAccountArchived
	}
	return c, nil
}

// Motivos estruturados do IMPORT_TARGET_MISMATCH (campo `reason`). Conjunto
// FECHADO, publicado no `fields` da resposta para o frontend GUIAR o usuário —
// propor "Cartão C6" como conta nova, mandar trocar a conta de destino — em vez
// de dar um beco sem saída. São enums do vocabulário do servidor, nunca PII.
const (
	// ReasonExpectedCreditCard — o arquivo é uma fatura (card_statement), mas a
	// conta de destino não é credit_card. A tela oferece criar/escolher o cartão.
	ReasonExpectedCreditCard = "expected_credit_card"

	// ReasonExpectedBankAccount — o arquivo é um extrato (checking_statement),
	// mas a conta de destino é um cartão. A tela oferece a conta bancária.
	ReasonExpectedBankAccount = "expected_bank_account"

	// ReasonWrongInstitution — a instituição detectada no arquivo é outra que a
	// declarada na conta. A tela propõe a conta certa daquela instituição.
	ReasonWrongInstitution = "wrong_institution"

	// ReasonUnknownDocument — tipo de documento fora da allowlist. É DEFESA: o
	// registro só produz DocKind válido, então não deve ocorrer.
	ReasonUnknownDocument = "unknown_document"
)

// targetMismatchError enriquece ErrTargetMismatch com o MOTIVO estruturado da
// recusa e o mínimo que o frontend precisa para guiar o usuário, tudo no campo
// `fields` do contrato (já um map[string]string livre — sem mexer no OpenAPI).
//
// ⚠️ Segurança (para a revisão não precisar reconstruir o raciocínio):
//   - `DetectedInstitution` e `DetectedDocKind` saem do ARQUIVO DO PRÓPRIO
//     usuário, e a conta já foi validada como dele em contaDeDestino antes desta
//     trava — logo não há BOLA nem vazamento de dado de outra casa aqui.
//   - Todos os campos são ENUMS FECHADOS (reason, as instituições da allowlist
//     de importer.Institution, os dois DocKind).
//     NUNCA entram o nome do arquivo, uma linha do documento ou qualquer PII.
type targetMismatchError struct {
	Reason              string // um dos Reason* acima.
	DetectedInstitution string // instituição detectada no arquivo (importer.Institution).
	DetectedDocKind     string // tipo detectado (card_statement, checking_statement).
	AccountInstitution  string // instituição da conta; só em wrong_institution.
}

// Error descreve a recusa SEM ecoar conteúdo: só o motivo estruturado.
func (e *targetMismatchError) Error() string {
	return fmt.Sprintf("%s: %s", ErrTargetMismatch.Error(), e.Reason)
}

// Unwrap mantém errors.Is(err, ErrTargetMismatch) verdadeiro — o handler
// depende disso para escolher o código HTTP IMPORT_TARGET_MISMATCH sem conhecer
// o tipo concreto.
func (e *targetMismatchError) Unwrap() error { return ErrTargetMismatch }

// travaDeConsistencia é a defesa contra "importei na conta errada" (§3.3).
//
// São duas travas, e a segunda é a mais forte:
//
//  1. instituição DETECTADA diferente da declarada na conta. `other` na conta
//     não trava nada — só perde a checagem;
//  2. fatura só entra em conta credit_card, e extrato só entra em conta que
//     não seja cartão. Esta é a que evita o pior defeito possível aqui: a
//     fatura lida numa conta corrente inverteria o sentido de TODA linha do
//     documento, e o estrago apareceria como "meu saldo está errado", meses
//     depois.
//
// PRECEDÊNCIA (deliberada): a instituição é conferida ANTES do tipo. Instituição
// errada é a pista mais fundamental ("este arquivo é de outro banco"), e é dela
// que o front tira a proposta de conta certa; só quando a instituição bate é que
// a divergência de tipo vira expected_credit_card/expected_bank_account.
//
// O erro devolvido é o targetMismatchError tipado, que embrulha ErrTargetMismatch
// e carrega o motivo estruturado — o handler o lê com errors.As.
func travaDeConsistencia(conta *account.Account, instituicao Institution, doc DocKind) error {
	if conta.Institution != "" && conta.Institution != account.InstitutionOther &&
		conta.Institution != string(instituicao) {
		// `AccountInstitution` sai da CONTA do usuário (já validada como dele) e,
		// neste ramo, é garantidamente um enum real (nem "" nem "other").
		return novoTargetMismatch(ReasonWrongInstitution, instituicao, doc, conta.Institution)
	}

	ehCartao := conta.Kind == account.KindCreditCard
	switch doc {
	case DocKindCardStatement:
		if !ehCartao {
			return novoTargetMismatch(ReasonExpectedCreditCard, instituicao, doc, "")
		}
	case DocKindCheckingStatement:
		if ehCartao {
			return novoTargetMismatch(ReasonExpectedBankAccount, instituicao, doc, "")
		}
	default:
		return novoTargetMismatch(ReasonUnknownDocument, instituicao, doc, "")
	}
	return nil
}

// novoTargetMismatch monta o erro tipado só com enums reconhecidos: instituição
// e tipo entram apenas quando são valores da allowlist (Valid()), para o
// `fields` publicar exclusivamente vocabulário fechado — nunca uma string crua.
func novoTargetMismatch(reason string, instituicao Institution, doc DocKind, accountInstitution string) error {
	e := &targetMismatchError{Reason: reason, AccountInstitution: accountInstitution}
	if instituicao.Valid() {
		e.DetectedInstitution = string(instituicao)
	}
	if doc.Valid() {
		e.DetectedDocKind = string(doc)
	}
	return e
}

// analisar carrega a janela e classifica as linhas.
//
// É UMA função, usada pelas DUAS fases, e isso não é economia de código: é a
// garantia de que o confirm recalcula exatamente o mesmo que a prévia calculou
// (§4.8). Duas implementações divergiriam, e divergir aqui quer dizer gravar
// uma duplicata que a prévia tinha marcado.
func (s *Service) analisar(ctx context.Context, householdID, accountID, instituicao string, linhas []dedup.Row, minDate, maxDate civil.Date) (dedup.Result, error) {
	if len(linhas) == 0 {
		return dedup.Result{}, nil
	}
	// O teto de linhas é conferido ANTES de qualquer consulta. Analyze também o
	// confere — ele é a fonte da regra —, mas conferir só lá faria um arquivo
	// grande demais pagar duas varreduras no banco para ser recusado no fim.
	if len(linhas) > dedup.MaxFileRows {
		return dedup.Result{}, fmt.Errorf("deduplicando: %w: %d linhas (o máximo é %d)",
			dedup.ErrTooManyRows, len(linhas), dedup.MaxFileRows)
	}

	// ⚠️ O erro NÃO pode virar janela vazia. Uma janela vazia silenciosa faz a
	// análise concluir "nada é duplicado" e gravar o arquivo inteiro de novo —
	// exatamente o que esta entrega existe para impedir.
	existentes, err := s.ledger.WindowForDedup(ctx, householdID, accountID, minDate, maxDate)
	if err != nil {
		return dedup.Result{}, fmt.Errorf("carregando janela de deduplicação: %w", err)
	}

	// A janela acima é filtrada por DATA, e a chave NATURAL não tem data
	// dentro. Quando a data da mesma transação muda mais do que a folga da
	// janela entre dois downloads — o usuário corrigindo a data no CSV, o
	// emissor re-datando uma pendente que liquidou —, a gêmea já gravada fica
	// fora dali e a linha voltaria como "novo", que entra por DEFAULT. Por isso
	// as ocorrências da chave natural são procuradas PELA CHAVE, sem data
	// nenhuma: a chave é a identidade da transação no emissor.
	//
	// O erro aqui também NÃO pode virar "não achei nada", pelo mesmo motivo do
	// carregamento da janela: silêncio aqui é duplicata gravada.
	var porChaveNatural []transaction.DedupKeyRow
	if chaves := dedup.NaturalKeys(instituicao, accountID, linhas); len(chaves) > 0 {
		porChaveNatural, err = s.ledger.RowsByDedupKeys(ctx, householdID, chaves)
		if err != nil {
			return dedup.Result{}, fmt.Errorf("carregando ocorrências da chave natural: %w", err)
		}
	}

	var usados map[string]dedup.ExternalUse
	if temChaveNatural(linhas) {
		brutos, err := s.ledger.ExternalIDsInWindow(ctx, householdID, accountID, minDate, maxDate)
		if err != nil {
			return dedup.Result{}, fmt.Errorf("carregando identificadores da janela: %w", err)
		}
		usados = make(map[string]dedup.ExternalUse, len(brutos))
		for chave, uso := range brutos {
			usados[chave] = dedup.ExternalUse{TransactionID: uso.TransactionID, AccountID: uso.AccountID}
		}
	}

	res, err := dedup.Analyze(dedup.Input{
		Institution:          instituicao,
		AccountID:            accountID,
		Rows:                 linhas,
		Existing:             existentes,
		ExistingByKey:        porChaveNatural,
		ExternalIDsElsewhere: usados,
	})
	if err != nil {
		return dedup.Result{}, fmt.Errorf("deduplicando: %w", err)
	}
	return res, nil
}

// temChaveNatural informa se vale a pena consultar os identificadores já usados
// em outras contas — um documento sem chave natural (a fatura) nunca casaria.
func temChaveNatural(linhas []dedup.Row) bool {
	for i := range linhas {
		if linhas[i].ExternalID != nil && *linhas[i].ExternalID != "" {
			return true
		}
	}
	return false
}

// registrar grava o rastro do lote. Chamada SEMPRE de dentro da transação:
// auditoria que cai fora dela pode sobreviver a um rollback, e aí o rastro
// descreve algo que não aconteceu.
func (s *Service) registrar(ctx context.Context, ator Actor, acao, batchID string) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, AuditParams{
		Action:      acao,
		Entity:      audit.EntityImportBatch,
		EntityID:    batchID,
		UserID:      ator.UserID,
		HouseholdID: ator.HouseholdID,
		IP:          ator.IP,
	})
}

// ledgerActor converte o ator da importação para o do domínio de lançamentos.
func (a Actor) ledgerActor() transaction.Actor {
	return transaction.Actor{HouseholdID: a.HouseholdID, UserID: a.UserID, IP: a.IP}
}

// statementActor faz o mesmo para o domínio de faturas.
func (a Actor) statementActor() cardstatement.Actor {
	return cardstatement.Actor{HouseholdID: a.HouseholdID, UserID: a.UserID, IP: a.IP}
}
