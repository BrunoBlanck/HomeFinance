package cardstatement

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/id"
)

// Actor é quem está agindo: a casa e o usuário vêm do TOKEN, nunca do corpo nem
// da URL; o IP vem da borda HTTP.
type Actor struct {
	HouseholdID string
	UserID      string
	IP          string
}

// Auditor registra o rastro das escritas financeiras, DENTRO da transação que
// elas descrevem (§4.7 do PLANOS.md).
type Auditor interface {
	Record(ctx context.Context, p AuditParams) error
}

// AuditParams é o evento a registrar. Espelha audit.Params — e, como ele, não
// tem campo de valor: o total da fatura é exatamente o número que a tentação
// mandaria guardar aqui, e é exatamente o que o S8 proíbe.
type AuditParams struct {
	Action      string
	Entity      string
	EntityID    string
	UserID      string
	HouseholdID string
	IP          string
}

// Transactor executa uma função dentro de uma transação. Sem chave estrangeira
// física (ADR-013), conferir a conta e gravar a fatura precisa ser atômico.
type Transactor interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Calendar resolve "hoje" no fuso da casa.
//
// Interface declarada aqui, no consumidor, e implementada por household.Service:
// é o que permite derivar "vencida" sem este pacote depender do de casas. O
// fuso é regra de negócio e é o da CASA (ADR-019a) — este pacote recebe o dia
// pronto e não decide fuso nenhum.
type Calendar interface {
	Today(ctx context.Context, householdID string) (civil.Date, error)
}

// Accounts é o que a fatura precisa saber de conta: se ela é desta casa, se é
// cartão de crédito e se está arquivada.
type Accounts interface {
	ByID(ctx context.Context, householdID, id string) (*account.Account, error)
}

// Totals são os números que a fatura DERIVA dos lançamentos ligados a ela.
//
// Eles não são colunas, e não podem virar colunas (ADR-023d): coluna
// materializada de dinheiro precisa de um só caminho de escrita esquecido para
// ficar errada, e fica errada em silêncio.
type Totals struct {
	// TotalCents é o que a fatura cobra: despesas menos receitas das linhas
	// daquela fatura (crédito na fatura abate).
	TotalCents int64

	// PaidCents é a soma dos transfer_in ligados à fatura — pagar a fatura é
	// uma transferência (ADR-016), e é a perna de entrada no cartão que quita.
	PaidCents int64

	// LineCount conta as linhas vivas da fatura. Não vai para a API (o schema
	// CardStatement é additionalProperties:false e não tem este campo); existe
	// para o serviço distinguir "fatura sem linha" de "fatura zerada" em teste
	// e em log.
	LineCount int64
}

// Lines é a fonte dos números derivados.
//
// Interface no consumidor, com tipos deste pacote, e isso é o que evita o
// ciclo: quem lê lançamento é o pacote transaction, que já importa este aqui
// para validar a fatura de um lançamento. O adaptador mora lá
// (transaction.StatementTotals) e converte; assim a seta de dependência aponta
// para um lado só.
type Lines interface {
	TotalsByStatement(ctx context.Context, householdID string, statementIDs []string) (map[string]Totals, error)
}

// Service concentra a regra de negócio das faturas.
type Service struct {
	repo     Repository
	accounts Accounts
	calendar Calendar
	lines    Lines
	tx       Transactor
	audit    Auditor
	ids      id.Generator
	clock    func() time.Time
}

// Option configura o Service.
type Option func(*Service)

// WithIDs injeta o gerador de IDs (teste).
func WithIDs(g id.Generator) Option { return func(s *Service) { s.ids = g } }

// WithClock injeta o relógio (teste).
func WithClock(c func() time.Time) Option { return func(s *Service) { s.clock = c } }

// WithAudit liga o rastro de auditoria.
func WithAudit(a Auditor) Option { return func(s *Service) { s.audit = a } }

// NewService monta o serviço.
func NewService(
	repo Repository,
	accounts Accounts,
	calendar Calendar,
	lines Lines,
	tx Transactor,
	opts ...Option,
) *Service {
	s := &Service{
		repo:     repo,
		accounts: accounts,
		calendar: calendar,
		lines:    lines,
		tx:       tx,
		ids:      id.New,
		clock:    func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// View é a fatura no formato da API.
//
// Forma do schema CardStatement do openapi.yaml, que é
// additionalProperties:false — campo a mais aqui é divergência de contrato.
type View struct {
	ID              string     `json:"id"`
	AccountID       string     `json:"accountId"`
	CompetenceMonth string     `json:"competenceMonth"`
	ClosingDate     civil.Date `json:"closingDate"`
	DueDate         civil.Date `json:"dueDate"`

	// TotalCents, PaidCents e Status são DERIVADOS a cada leitura (ADR-023d).
	TotalCents int64  `json:"totalCents"`
	PaidCents  int64  `json:"paidCents"`
	Status     string `json:"status"`

	Source    string `json:"source"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// ListView é a resposta de GET /card-statements.
type ListView struct {
	Items []View `json:"items"`
}

// UpsertInput é o pedido de criação ou reúso de uma fatura.
//
// Não tem householdId, id, source, nem os números derivados: todos vêm do
// servidor. A origem é sempre `import` no v1 — a fatura nasce da importação, e
// não há rota que a crie à mão.
type UpsertInput struct {
	AccountID       string
	CompetenceMonth string
	ClosingDate     civil.Date
	DueDate         civil.Date
}

// Upsert cria a fatura ou REUSA a que já existe para (casa, conta,
// competência).
//
// Idempotência é o ponto: importar a fatura de setembro duas vezes tem de
// produzir UMA fatura de setembro, com os lançamentos da segunda importação
// apontando para a mesma. Quem garante isso é o índice único do banco; o
// repositório procura antes e, se achar, devolve o id existente.
//
// Chame SEMPRE dentro do UnitOfWork da importação — o Transactor é reentrante,
// então o s.tx.Do aqui reaproveita a transação em curso em vez de abrir outra.
// Se duas requisições simultâneas criarem a mesma competência, a segunda volta
// com ErrDuplicate e a transação inteira desfaz: quem chama refaz o confirm, e
// aí encontra a fatura já criada. Não retentamos aqui de propósito — depois de
// um INSERT recusado, o PostgreSQL aborta a transação, e "tentar de novo" na
// mesma transação morta só trocaria um erro claro por um confuso.
func (s *Service) Upsert(ctx context.Context, ator Actor, in UpsertInput) (View, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return View{}, ErrNotFound
	}
	if err := ValidateDates(in.CompetenceMonth, in.ClosingDate, in.DueDate); err != nil {
		return View{}, err
	}

	novoID := s.ids()
	agora := s.clock()
	fatura := Statement{
		ID:              novoID,
		HouseholdID:     householdID,
		AccountID:       in.AccountID,
		CompetenceMonth: in.CompetenceMonth,
		ClosingDate:     in.ClosingDate,
		DueDate:         in.DueDate,
		Source:          SourceImport,
		CreatedAt:       agora,
		UpdatedAt:       agora,
	}

	err := s.tx.Do(ctx, func(ctx context.Context) error {
		// A conta é conferida DENTRO da transação da escrita: conferida fora,
		// ela pode ter sido excluída ou arquivada entre a conferência e o
		// INSERT (TOCTOU — spec 0004 §6.6).
		cartao, err := s.contaDeCartao(ctx, householdID, in.AccountID)
		if err != nil {
			return err
		}
		// O que é GRAVADO é o id que o banco devolveu, e não o que o chamador
		// pediu: `WHERE id = ?` casa por COLLATION (o MySQL 8 ignora a caixa,
		// o MSSQL o espaço à direita), enquanto a fatura é depois casada com a
		// conta em Go — `fatura.AccountID != conta.ID` no lote de lançamentos,
		// o nome da conta na tela. Guardar a string do cliente deixaria uma
		// fatura que o SQL encontra e que o Go não liga a conta nenhuma.
		fatura.AccountID = cartao.ID

		if err := s.repo.Upsert(ctx, &fatura); err != nil {
			return err
		}

		// O repositório sobrescreve o id quando encontra a fatura existente —
		// é assim, e só assim, que dá para saber se houve criação. Reúso não é
		// criação e não gera entrada: a auditoria ficaria cheia de
		// "card_statement.created" para a mesma fatura de setembro.
		if fatura.ID != novoID {
			return nil
		}
		return s.registrar(ctx, ator, audit.ActionCardStatementCreated, fatura.ID)
	})
	if err != nil {
		return View{}, err
	}

	return s.comDerivados(ctx, householdID, []Statement{fatura})
}

// ByID devolve uma fatura da casa, com os números derivados.
func (s *Service) ByID(ctx context.Context, ator Actor, statementID string) (View, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return View{}, ErrNotFound
	}
	f, err := s.repo.ByID(ctx, householdID, statementID)
	if err != nil {
		return View{}, err
	}
	return s.comDerivados(ctx, householdID, []Statement{*f})
}

// ListInput é a janela da listagem.
type ListInput struct {
	AccountID       string
	CompetenceMonth string
	Limit           int
}

// List devolve as faturas da casa, da competência mais recente para a mais
// antiga, já com total, pago e situação.
func (s *Service) List(ctx context.Context, ator Actor, in ListInput) (ListView, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return ListView{}, ErrNotFound
	}
	if in.CompetenceMonth != "" {
		if _, err := ParseMonth(in.CompetenceMonth); err != nil {
			return ListView{}, err
		}
	}
	// Conta do filtro conferida como da casa: id que não é meu é 404, igual a
	// id que não existe (S1). Sem isto a resposta seria uma lista vazia, que
	// não vaza dado mas também não é a resposta certa.
	//
	// O que desce para a consulta é o id CANÔNICO — mesmo motivo do Upsert: a
	// conferência de posse é feita por uma igualdade de SQL insensível a caixa
	// no MySQL, e o filtro não pode carregar adiante uma grafia que o resto do
	// código não reconhece.
	contaDoFiltro := in.AccountID
	if in.AccountID != "" {
		conta, err := s.contaDaCasa(ctx, householdID, in.AccountID)
		if err != nil {
			return ListView{}, err
		}
		contaDoFiltro = conta.ID
	}

	linhas, err := s.repo.List(ctx, householdID, ListFilter{
		AccountID:       contaDoFiltro,
		CompetenceMonth: in.CompetenceMonth,
		Limit:           in.Limit,
	})
	if err != nil {
		return ListView{}, fmt.Errorf("listando faturas: %w", err)
	}

	itens, err := s.views(ctx, householdID, linhas)
	if err != nil {
		return ListView{}, err
	}
	return ListView{Items: itens}, nil
}

// --- apoio ----------------------------------------------------------------

// comDerivados monta a View de UMA fatura.
func (s *Service) comDerivados(ctx context.Context, householdID string, linhas []Statement) (View, error) {
	views, err := s.views(ctx, householdID, linhas)
	if err != nil {
		return View{}, err
	}
	if len(views) == 0 {
		return View{}, ErrNotFound
	}
	return views[0], nil
}

// views resolve os números derivados de todas as faturas de uma vez.
//
// Uma consulta de totais para a lista inteira, e uma de calendário: derivar
// fatura a fatura faria a tela de 24 faturas custar 24 agregações e 24
// resoluções de fuso, para um resultado idêntico.
func (s *Service) views(ctx context.Context, householdID string, linhas []Statement) ([]View, error) {
	out := make([]View, 0, len(linhas))
	if len(linhas) == 0 {
		return out, nil
	}

	ids := make([]string, 0, len(linhas))
	for i := range linhas {
		ids = append(ids, linhas[i].ID)
	}

	totais, err := s.lines.TotalsByStatement(ctx, householdID, ids)
	if err != nil {
		return nil, fmt.Errorf("somando linhas das faturas: %w", err)
	}

	hoje, err := s.calendar.Today(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("resolvendo hoje na casa: %w", err)
	}

	for i := range linhas {
		f := linhas[i]
		t := totais[f.ID]
		out = append(out, View{
			ID:              f.ID,
			AccountID:       f.AccountID,
			CompetenceMonth: f.CompetenceMonth,
			ClosingDate:     f.ClosingDate,
			DueDate:         f.DueDate,
			TotalCents:      t.TotalCents,
			PaidCents:       t.PaidCents,
			Status:          DeriveStatus(t.TotalCents, t.PaidCents, f.DueDate, hoje),
			Source:          f.Source,
			CreatedAt:       f.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt:       f.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

// contaDaCasa confere que a conta é da casa e traduz o erro: conta de outra
// casa vira o MESMO ErrNotFound de fatura inexistente (S1).
func (s *Service) contaDaCasa(ctx context.Context, householdID, accountID string) (*account.Account, error) {
	c, err := s.accounts.ByID(ctx, householdID, accountID)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return nil, fmt.Errorf("conta da fatura: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("buscando conta da fatura: %w", err)
	}
	return c, nil
}

// contaDeCartao confere, além da casa, que a conta é cartão de crédito e que
// não está arquivada.
func (s *Service) contaDeCartao(ctx context.Context, householdID, accountID string) (*account.Account, error) {
	c, err := s.contaDaCasa(ctx, householdID, accountID)
	if err != nil {
		return nil, err
	}
	if c.Kind != account.KindCreditCard {
		return nil, ErrNotCreditCard
	}
	if c.ArchivedAt != nil {
		return nil, ErrAccountArchived
	}
	return c, nil
}

// registrar grava o rastro da escrita, sempre de dentro da transação.
func (s *Service) registrar(ctx context.Context, ator Actor, acao, entityID string) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, AuditParams{
		Action:      acao,
		Entity:      audit.EntityCardStatement,
		EntityID:    entityID,
		UserID:      ator.UserID,
		HouseholdID: ator.HouseholdID,
		IP:          ator.IP,
	})
}
