package account

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/id"
)

// Calendar resolve "hoje" no fuso da casa.
//
// Interface declarada aqui, no consumidor, e implementada por
// household.Service: é o que permite validar data sem este pacote depender do
// de casas. "Hoje" é regra de negócio e vem do fuso da casa (ADR-019).
type Calendar interface {
	Today(ctx context.Context, householdID string) (civil.Date, error)
}

// Actor é quem está agindo: a casa e o usuário vêm do TOKEN (nunca do corpo
// nem da URL), e o IP vem da borda HTTP.
//
// Existe como valor com nome, e não como três strings soltas em sete
// assinaturas, porque a próxima pessoa a somar um método não pode ter dúvida
// sobre de onde cada uma sai — e é essa dúvida que produz um household_id
// vindo do cliente.
type Actor struct {
	HouseholdID string
	UserID      string
	IP          string
}

// Auditor registra o rastro das escritas financeiras (§4.7 do PLANOS.md).
//
// Interface no consumidor, implementada por audit.Service. A gravação acontece
// DENTRO da transação da escrita que ela descreve: se o registro de auditoria
// não couber, a escrita não vale — "toda escrita financeira gera entrada" é
// invariante, não intenção.
type Auditor interface {
	Record(ctx context.Context, p AuditParams) error
}

// AuditParams é o evento a registrar. Espelha audit.Params sem que este pacote
// precise importar aquele.
type AuditParams struct {
	Action      string
	Entity      string
	EntityID    string
	UserID      string
	HouseholdID string
	IP          string
}

// Balances devolve a soma COM SINAL dos lançamentos de cada conta da casa.
//
// É a metade variável do saldo derivado (ADR-017a); a outra metade é o
// opening_balance_cents, que já está na conta. Interface declarada aqui, no
// consumidor, e implementada pelo repositório de lançamentos: é o que permite a
// conta ter saldo real sem este pacote depender do de lançamentos.
//
// Conta sem lançamento não aparece no mapa, e não precisa aparecer: o saldo
// dela é o saldo de abertura.
type Balances interface {
	SumByAccount(ctx context.Context, householdID string) (map[string]int64, error)
}

// Transactor executa uma função dentro de uma transação.
//
// Necessário porque não há chave estrangeira física (ADR-013): as verificações
// de unicidade e de limite só valem se acontecerem na MESMA transação da
// escrita que elas autorizam.
type Transactor interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Service concentra a regra de negócio das contas.
type Service struct {
	repo     Repository
	calendar Calendar
	tx       Transactor
	audit    Auditor
	usage    []UsageChecker
	balances Balances
	ids      id.Generator
	clock    func() time.Time
}

// Option configura o Service.
type Option func(*Service)

// WithIDs injeta o gerador de IDs (teste).
func WithIDs(g id.Generator) Option { return func(s *Service) { s.ids = g } }

// WithClock injeta o relógio (teste).
func WithClock(c func() time.Time) Option { return func(s *Service) { s.clock = c } }

// WithAudit liga o rastro de auditoria. Sem ele o serviço funciona, o que é
// deliberado: os testes de unidade não precisam de auditoria para exercitar
// regra de negócio. Em produção ele é ligado em cmd/api/main.go.
func WithAudit(a Auditor) Option { return func(s *Service) { s.audit = a } }

// WithUsageCheckers registra quem sabe dizer se uma conta está em uso. Na E2 o
// repositório de lançamentos entra por aqui, via transaction.NewUsageChecker.
func WithUsageCheckers(checkers ...UsageChecker) Option {
	return func(s *Service) { s.usage = append(s.usage, checkers...) }
}

// WithBalances liga a soma dos lançamentos ao saldo derivado (ADR-017).
//
// Sem ela o serviço funciona e o saldo é o de abertura — que é a verdade
// enquanto não existe lançamento, e é o que mantém os testes de unidade deste
// pacote sem precisar do domínio de lançamentos. Em produção ela é ligada em
// cmd/api/main.go, e a partir daí balanceCents é abertura + soma.
func WithBalances(b Balances) Option { return func(s *Service) { s.balances = b } }

// NewService monta o serviço.
func NewService(repo Repository, calendar Calendar, tx Transactor, opts ...Option) *Service {
	s := &Service{
		repo:     repo,
		calendar: calendar,
		tx:       tx,
		ids:      id.New,
		clock:    func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// CreateInput é o DTO de criação. Campos derivados (householdId, nameNorm,
// timestamps) NÃO estão aqui de propósito: é assim que mass assignment deixa
// de ser possível por construção (S2 do PLANOS.md).
type CreateInput struct {
	Name string
	Kind string

	// Institution é a instituição da conta (allowlist fechada). Vazia vira
	// InstitutionOther, como o contrato diz: "ausente = other".
	Institution string

	// StatementClosingDay e StatementDueDay só existem em cartão de crédito.
	// Nulos significam "não configurado", e é o estado normal de toda conta que
	// não é cartão.
	StatementClosingDay *int
	StatementDueDay     *int

	OpeningBalanceCents int64
	OpeningDate         civil.Date

	// Keywords são as palavras-chave iniciais (spec 0005 §4.1). Ausente ou
	// vazia = nenhuma. Validadas por ValidateKeywords antes de qualquer
	// escrita.
	Keywords []string
}

// UpdateInput é o DTO de edição parcial. Ponteiro nulo = "não mexer".
type UpdateInput struct {
	Name *string
	Kind *string

	// Institution nula não mexe. Não existe "limpar a instituição": a coluna é
	// NOT NULL e o vazio dela chama-se `other`.
	Institution *string

	// Os dois dias são TRI-ESTADO (OptionalDay): ausente não mexe, nulo limpa,
	// valor grava. É a diferença entre "não toquei" e "quero apagar", e ela
	// precisa existir porque apagar é uma operação legítima aqui.
	StatementClosingDay OptionalDay
	StatementDueDay     OptionalDay

	OpeningBalanceCents *int64
	OpeningDate         *civil.Date

	// Keywords é TRI-ESTADO: nulo = não mexe; presente SUBSTITUI a lista
	// inteira, e a lista vazia limpa (spec 0005 §4.1.3). Não existe
	// "acrescentar": a tela lê a lista atual e manda a nova completa.
	Keywords *[]string
}

// Create cria a conta.
func (s *Service) Create(ctx context.Context, ator Actor, in CreateInput) (View, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return View{}, ErrNotFound
	}

	name, norm, err := NormalizeName(in.Name)
	if err != nil {
		return View{}, err
	}
	if !ValidKind(in.Kind) {
		return View{}, ErrInvalidKind
	}
	instituicao, err := NormalizeInstitution(in.Institution)
	if err != nil {
		return View{}, err
	}
	// O tipo aqui é o da própria criação: não há estado anterior com que
	// combinar.
	if err := ValidateStatementDays(in.Kind, in.StatementClosingDay, in.StatementDueDay); err != nil {
		return View{}, err
	}
	if err := ValidateAmount(in.OpeningBalanceCents); err != nil {
		return View{}, err
	}
	hoje, err := s.calendar.Today(ctx, householdID)
	if err != nil {
		return View{}, fmt.Errorf("resolvendo hoje na casa: %w", err)
	}
	if err := ValidateOpeningDate(in.OpeningDate, hoje); err != nil {
		return View{}, err
	}
	// Palavras-chave validadas ANTES de abrir a transação: é validação pura
	// (forma, teto, repetição) e não depende de estado nenhum.
	kws, err := ValidateKeywords(in.Keywords)
	if err != nil {
		return View{}, err
	}

	now := s.clock()
	created := Account{
		ID:                  s.ids(),
		HouseholdID:         householdID,
		Name:                name,
		NameNorm:            norm,
		Kind:                in.Kind,
		Institution:         instituicao,
		StatementClosingDay: in.StatementClosingDay,
		StatementDueDay:     in.StatementDueDay,
		OpeningBalanceCents: in.OpeningBalanceCents,
		OpeningDate:         in.OpeningDate,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	// Limite e unicidade dentro da MESMA transação da inserção: conferir fora
	// dela é conferir um estado que pode ter mudado antes do INSERT.
	//
	// A conta não sobrevive ao rollback de uma tentativa que falhou: não há
	// "própria" a excluir da busca pela dona, por isso o ownID vazio. A
	// operação é re-executável: `created` é montada fora, mas a transação
	// anterior foi desfeita por inteiro, então o mesmo id está livre, e o
	// restante (contagem, nome, palavras) é relido e regerado a cada execução.
	err = s.escreverComPalavras(ctx, householdID, "", kws, func(ctx context.Context) error {
		total, err := s.repo.CountAll(ctx, householdID)
		if err != nil {
			return fmt.Errorf("contando contas: %w", err)
		}
		if total >= MaxPerHousehold {
			return ErrTooMany
		}
		taken, err := s.repo.NameTaken(ctx, householdID, norm, "")
		if err != nil {
			return fmt.Errorf("verificando nome: %w", err)
		}
		if taken {
			return ErrNameTaken
		}
		if err := s.repo.Create(ctx, &created); err != nil {
			return err
		}
		// As palavras entram na MESMA transação da conta: sem FK física
		// (ADR-013), conta criada com palavras pela metade é um estado que o
		// banco não barra.
		if len(kws) > 0 {
			if err := s.gravarPalavras(ctx, householdID, created.ID, kws); err != nil {
				return err
			}
		}
		return s.registrar(ctx, ator, audit.ActionAccountCreated, created.ID)
	})
	if err != nil {
		return View{}, err
	}
	return s.respostaCompleta(ctx, householdID, toView(created), nil)
}

// Get devolve uma conta da casa.
func (s *Service) Get(ctx context.Context, ator Actor, accountID string) (View, error) {
	a, err := s.repo.ByID(ctx, ator.HouseholdID, accountID)
	if err != nil {
		return View{}, err
	}
	return s.respostaCompleta(ctx, ator.HouseholdID, toView(*a), nil)
}

// List devolve as contas da casa e o total.
func (s *Service) List(ctx context.Context, ator Actor, includeArchived bool) (ListView, error) {
	rows, err := s.repo.List(ctx, ator.HouseholdID, includeArchived)
	if err != nil {
		return ListView{}, fmt.Errorf("listando contas: %w", err)
	}

	// UMA consulta de soma para a lista inteira: o saldo é derivado (ADR-017),
	// e derivá-lo conta a conta faria a tela de contas custar N agregações.
	somas, err := s.somas(ctx, ator.HouseholdID)
	if err != nil {
		return ListView{}, err
	}
	// E UMA consulta de palavras-chave para a casa inteira, distribuída por
	// conta em memória — nunca uma por conta (N+1).
	porDona, err := s.palavrasPorDona(ctx, ator.HouseholdID)
	if err != nil {
		return ListView{}, err
	}

	out := ListView{Items: make([]View, 0, len(rows))}
	for _, a := range rows {
		v := comSaldo(toView(a), somas)
		v.Keywords = palavrasDe(porDona, a.ID)
		out.Items = append(out.Items, v)
		// O total soma o que a lista mostra — inclusive arquivadas, quando o
		// cliente pediu para vê-las. Somar um conjunto diferente do exibido
		// faria a tela mostrar um total que não bate com as linhas.
		out.TotalBalanceCents += v.BalanceCents
	}
	return out, nil
}

// Update aplica edição parcial.
func (s *Service) Update(ctx context.Context, ator Actor, accountID string, in UpdateInput) (View, error) {
	householdID := ator.HouseholdID

	// Tri-estado: nulo não mexe nas palavras; presente substitui a lista
	// inteira (vazia limpa). Validação pura, antes da transação.
	var kws []Keyword
	if in.Keywords != nil {
		var err error
		if kws, err = ValidateKeywords(*in.Keywords); err != nil {
			return View{}, err
		}
	}

	var out View
	err := s.escreverComPalavras(ctx, householdID, accountID, kws, func(ctx context.Context) error {
		current, err := s.repo.ByID(ctx, householdID, accountID)
		if err != nil {
			return err
		}

		if in.Name != nil {
			name, norm, err := NormalizeName(*in.Name)
			if err != nil {
				return err
			}
			if norm != current.NameNorm {
				taken, err := s.repo.NameTaken(ctx, householdID, norm, current.ID)
				if err != nil {
					return fmt.Errorf("verificando nome: %w", err)
				}
				if taken {
					return ErrNameTaken
				}
			}
			current.Name, current.NameNorm = name, norm
		}

		if in.Kind != nil {
			if !ValidKind(*in.Kind) {
				return ErrInvalidKind
			}
			current.Kind = *in.Kind
		}

		if in.Institution != nil {
			// Na EDIÇÃO a allowlist é conferida direto, sem o "vazio vira
			// other" da criação: aqui o ponteiro não nulo significa que o campo
			// VEIO no corpo, e string vazia veio de alguém que quis mandar
			// alguma coisa. Traduzi-la para `other` em silêncio desligaria a
			// trava de consistência da importação sem ninguém pedir.
			if !ValidInstitution(*in.Institution) {
				return ErrInvalidInstitution
			}
			current.Institution = *in.Institution
		}

		// Tri-estado aplicado ANTES da conferência, e a conferência sobre o
		// estado FINAL: é ela que impede a conta de deixar de ser cartão e
		// continuar com um dia de vencimento pendurado.
		if in.StatementClosingDay.Set {
			current.StatementClosingDay = in.StatementClosingDay.Day
		}
		if in.StatementDueDay.Set {
			current.StatementDueDay = in.StatementDueDay.Day
		}
		if err := ValidateStatementDays(current.Kind, current.StatementClosingDay, current.StatementDueDay); err != nil {
			return err
		}

		if in.OpeningBalanceCents != nil {
			if err := ValidateAmount(*in.OpeningBalanceCents); err != nil {
				return err
			}
			current.OpeningBalanceCents = *in.OpeningBalanceCents
		}

		if in.OpeningDate != nil {
			hoje, err := s.calendar.Today(ctx, householdID)
			if err != nil {
				return fmt.Errorf("resolvendo hoje na casa: %w", err)
			}
			if err := ValidateOpeningDate(*in.OpeningDate, hoje); err != nil {
				return err
			}
			current.OpeningDate = *in.OpeningDate
		}

		if in.Keywords != nil {
			if err := s.gravarPalavras(ctx, householdID, current.ID, kws); err != nil {
				return err
			}
		}

		current.UpdatedAt = s.clock()
		if err := s.repo.Update(ctx, current); err != nil {
			return err
		}
		out = toView(*current)
		// A auditoria é o evento que já existia (spec 0005 §4.1.5): sem as
		// palavras — só ação, entidade, id, ator e IP.
		return s.registrar(ctx, ator, audit.ActionAccountUpdated, current.ID)
	})
	if err != nil {
		return View{}, err
	}
	return s.respostaCompleta(ctx, householdID, out, nil)
}

// Archive arquiva a conta. É IDEMPOTENTE: arquivar uma conta já arquivada
// devolve o estado atual sem erro — dois cliques não são um problema para
// resolver com uma mensagem.
func (s *Service) Archive(ctx context.Context, ator Actor, accountID string) (View, error) {
	householdID := ator.HouseholdID
	var out View
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		current, err := s.repo.ByID(ctx, householdID, accountID)
		if err != nil {
			return err
		}
		if current.ArchivedAt != nil {
			out = toView(*current)
			return nil
		}
		now := s.clock()
		current.ArchivedAt = &now
		current.UpdatedAt = now
		if err := s.repo.Update(ctx, current); err != nil {
			return err
		}
		out = toView(*current)
		return s.registrar(ctx, ator, audit.ActionAccountArchived, current.ID)
	})
	return s.respostaCompleta(ctx, householdID, out, err)
}

// Unarchive desarquiva a conta.
//
// Recusa se o nome tiver sido tomado por outra conta enquanto esta estava
// arquivada — a unicidade vale entre as ATIVAS, e desarquivar é justamente o
// ato de voltar a ser ativa.
func (s *Service) Unarchive(ctx context.Context, ator Actor, accountID string) (View, error) {
	householdID := ator.HouseholdID
	var out View
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		current, err := s.repo.ByID(ctx, householdID, accountID)
		if err != nil {
			return err
		}
		if current.ArchivedAt == nil {
			out = toView(*current)
			return nil
		}
		taken, err := s.repo.NameTaken(ctx, householdID, current.NameNorm, current.ID)
		if err != nil {
			return fmt.Errorf("verificando nome: %w", err)
		}
		if taken {
			return ErrNameTaken
		}
		current.ArchivedAt = nil
		current.UpdatedAt = s.clock()
		if err := s.repo.Update(ctx, current); err != nil {
			return err
		}
		out = toView(*current)
		return s.registrar(ctx, ator, audit.ActionAccountUnarchived, current.ID)
	})
	return s.respostaCompleta(ctx, householdID, out, err)
}

// Delete exclui logicamente, e só se a conta nunca tiver sido usada.
func (s *Service) Delete(ctx context.Context, ator Actor, accountID string) error {
	householdID := ator.HouseholdID
	return s.tx.Do(ctx, func(ctx context.Context) error {
		current, err := s.repo.ByID(ctx, householdID, accountID)
		if err != nil {
			return err
		}
		inUse, err := s.inUse(ctx, householdID, current.ID)
		if err != nil {
			return err
		}
		if inUse {
			return ErrInUse
		}
		if err := s.repo.SoftDelete(ctx, householdID, current.ID, s.clock()); err != nil {
			return err
		}
		// As palavras-chave saem FISICAMENTE, na mesma transação, mesmo com
		// a exclusão da conta sendo lógica: palavra-chave não é dado
		// financeiro, e mantida ela seguiria ocupando a vaga no índice único
		// da casa — a palavra ficaria "em uso" por uma conta que ninguém
		// mais vê (ADR-013: sem FK física, é o UnitOfWork que limpa).
		if err := s.repo.DeleteKeywords(ctx, householdID, current.ID); err != nil {
			return fmt.Errorf("apagando palavras-chave: %w", err)
		}
		return s.registrar(ctx, ator, audit.ActionAccountDeleted, current.ID)
	})
}

// inUse consulta todos os verificadores registrados.
//
// Erro de qualquer um deles ABORTA a exclusão. Tratar falha de infraestrutura
// como "não está em uso" transformaria uma indisponibilidade momentânea em
// perda de dado — o usuário excluiria uma conta que tem lançamento.
func (s *Service) inUse(ctx context.Context, householdID, accountID string) (bool, error) {
	for _, checker := range s.usage {
		used, err := checker.AccountInUse(ctx, householdID, accountID)
		if err != nil {
			return false, fmt.Errorf("verificando uso da conta: %w", err)
		}
		if used {
			return true, nil
		}
	}
	return false, nil
}

// somas carrega a soma dos lançamentos por conta da casa.
//
// Sem Balances ligado, devolve mapa nulo — e a leitura de mapa nulo em Go é
// zero, que é exatamente o saldo variável de quem não tem lançamento. É o que
// mantém este pacote testável sozinho.
func (s *Service) somas(ctx context.Context, householdID string) (map[string]int64, error) {
	if s.balances == nil {
		return nil, nil
	}
	somas, err := s.balances.SumByAccount(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("somando lançamentos da casa: %w", err)
	}
	return somas, nil
}

// comSaldo aplica o ADR-017: saldo é abertura MAIS a soma dos lançamentos.
func comSaldo(v View, somas map[string]int64) View {
	v.BalanceCents = v.OpeningBalanceCents + somas[v.ID]
	return v
}

// respostaCompleta completa a View de UMA conta com o saldo derivado e as
// palavras-chave.
//
// Existe para que POST, PATCH, arquivar e desarquivar respondam o MESMO saldo
// e a MESMA lista de palavras que o GET responderia. Sem isto, a tela
// mostraria o saldo derivado ao carregar e o saldo de abertura logo depois de
// editar o nome da conta — o número mudando sozinho na frente do usuário, que
// é a pior forma de errar num app de dinheiro.
func (s *Service) respostaCompleta(ctx context.Context, householdID string, v View, err error) (View, error) {
	if err != nil {
		return View{}, err
	}
	somas, err := s.somas(ctx, householdID)
	if err != nil {
		return View{}, err
	}
	porDona, err := s.palavrasPorDona(ctx, householdID)
	if err != nil {
		return View{}, err
	}
	v = comSaldo(v, somas)
	v.Keywords = palavrasDe(porDona, v.ID)
	return v, nil
}

// gravarPalavras SUBSTITUI a lista de palavras-chave da conta, dentro da
// transação em curso.
//
// A pré-checagem via KeywordOwners é o que permite o 409 citar a dona: ela
// só enxerga a casa do token (o repositório filtra por household_id) e ignora
// a própria conta, cuja lista está sendo trocada. A corrida que escapa dela —
// duas edições disputando a mesma palavra ao mesmo tempo — é decidida pelo
// índice único em ReplaceKeywords, que devolve ErrKeywordTaken sem dona.
//
// Recebe as Keywords já validadas (Keyword, Norm, Position) e preenche o que
// é do servidor: id, casa, conta e instante.
func (s *Service) gravarPalavras(ctx context.Context, householdID, accountID string, kws []Keyword) error {
	if len(kws) > 0 {
		donas, err := s.repo.KeywordOwners(ctx, householdID, norms(kws))
		if err != nil {
			return fmt.Errorf("verificando palavras-chave: %w", err)
		}
		for _, k := range kws {
			if dona, existe := donas[k.Norm]; existe && dona != accountID {
				return &KeywordTakenError{Keyword: k.Keyword, OwnerID: dona}
			}
		}
	}
	now := s.clock()
	for i := range kws {
		kws[i].ID = s.ids()
		kws[i].HouseholdID = householdID
		kws[i].AccountID = accountID
		kws[i].CreatedAt = now
	}
	if err := s.repo.ReplaceKeywords(ctx, householdID, accountID, kws); err != nil {
		return fmt.Errorf("gravando palavras-chave: %w", err)
	}
	return nil
}

// escreverComPalavras executa a operação (uma transação inteira) e resolve a
// corrida do índice único das palavras-chave, com uma garantia: nenhum
// ErrKeywordTaken sai daqui sem a palavra recusada. O contrato do 409
// (`KeywordConflict` no openapi.yaml) exige `fields.keyword`, e só a dona é
// opcional.
//
// A corrida é rara: duas edições disputam a mesma palavra ao mesmo tempo, a
// pré-checagem das duas passa e o índice único recusa a segunda com um
// ErrKeywordTaken cru — o banco não diz quem ganhou. Daí os três desfechos:
//
//  1. donaNaCorrida reconsulta fora da transação desfeita e ACHA a dona —
//     409 com palavra e dona, o caso comum;
//  2. não acha (a vencedora largou a palavra nesse meio-tempo): a colisão
//     desapareceu, e a operação inteira roda UMA segunda vez, em transação
//     nova. Passando, a resposta é a de sucesso normal;
//  3. a segunda tentativa também colide e de novo não há dona (corrida
//     tripla): 409 com a PRIMEIRA palavra enviada e sem dona. É o melhor
//     esforço possível — ReplaceKeywords só sabe devolver ErrKeywordTaken,
//     sem a norma colidida, porque extraí-la exigiria interpretar a mensagem
//     do driver, e isso não é portátil entre os quatro dialetos.
//
// Nunca há terceira tentativa: quem colide duas vezes seguidas com dona
// invisível está numa disputa que a tela resolve melhor recarregando a lista
// do que o servidor insistindo. A operação precisa ser re-executável, e as
// duas são: releem o estado atual e regeram o que é gerado a cada execução,
// e a transação anterior foi desfeita por inteiro.
//
// Sem palavras na lista não há corrida a resolver (ReplaceKeywords com lista
// vazia só apaga), e a transação roda uma vez, sem tratamento.
func (s *Service) escreverComPalavras(ctx context.Context, householdID, accountID string, kws []Keyword, op func(ctx context.Context) error) error {
	if len(kws) == 0 {
		return s.tx.Do(ctx, op)
	}
	err := s.donaNaCorrida(ctx, householdID, accountID, kws, s.tx.Do(ctx, op))
	if !colisaoSemDona(err) {
		return err
	}
	// Desfecho 2: a dona sumiu, a colisão também. Uma tentativa a mais.
	err = s.donaNaCorrida(ctx, householdID, accountID, kws, s.tx.Do(ctx, op))
	if !colisaoSemDona(err) {
		return err
	}
	// Desfecho 3: corrida tripla. A palavra é a primeira enviada porque o
	// repositório não informa qual colidiu; a dona fica vazia e o handler
	// omite `ownerId`, como o contrato admite.
	return &KeywordTakenError{Keyword: kws[0].Keyword}
}

// colisaoSemDona informa se o erro é o ErrKeywordTaken cru do índice único,
// ainda sem a dona identificada. É o único erro que escreverComPalavras
// trata; qualquer outro passa intocado.
func colisaoSemDona(err error) bool {
	var comDona *KeywordTakenError
	return errors.Is(err, ErrKeywordTaken) && !errors.As(err, &comDona)
}

// donaNaCorrida completa o ErrKeywordTaken vindo do índice único — que não
// sabe dizer quem ganhou — consultando, já FORA da transação desfeita, quem
// ficou com a palavra. Qualquer outro erro (ou nil) passa intocado.
//
// Se a vencedora tiver largado a palavra nesse meio-tempo, o erro volta como
// veio, cru, e é escreverComPalavras quem decide o que fazer com ele. A
// consulta filtra pela casa do token, então a dona citada é sempre desta
// casa.
func (s *Service) donaNaCorrida(ctx context.Context, householdID, accountID string, kws []Keyword, err error) error {
	if !colisaoSemDona(err) || len(kws) == 0 {
		return err
	}
	donas, lookupErr := s.repo.KeywordOwners(ctx, householdID, norms(kws))
	if lookupErr != nil {
		// O erro original prevalece: ele já diz o que importa (409).
		return err
	}
	for _, k := range kws {
		if dona, existe := donas[k.Norm]; existe && dona != accountID {
			return &KeywordTakenError{Keyword: k.Keyword, OwnerID: dona}
		}
	}
	return err
}

func norms(kws []Keyword) []string {
	out := make([]string, 0, len(kws))
	for _, k := range kws {
		out = append(out, k.Norm)
	}
	return out
}

// palavrasPorDona carrega as palavras-chave da casa em UMA consulta e as
// distribui por conta, na forma exibível e na ordem de cadastro.
func (s *Service) palavrasPorDona(ctx context.Context, householdID string) (map[string][]string, error) {
	kws, err := s.repo.ListKeywords(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("listando palavras-chave: %w", err)
	}
	// O repositório já ordena por (conta, posição); a ordenação estável aqui
	// é só para a resposta não depender de quem implementa a interface.
	sort.SliceStable(kws, func(i, j int) bool { return kws[i].Position < kws[j].Position })
	out := make(map[string][]string)
	for _, k := range kws {
		out[k.AccountID] = append(out[k.AccountID], k.Keyword)
	}
	return out, nil
}

// palavrasDe devolve a lista da conta, ou `[]` — nunca nil, porque o campo é
// `required` no contrato e a tela não trata null.
func palavrasDe(porDona map[string][]string, accountID string) []string {
	if p := porDona[accountID]; p != nil {
		return p
	}
	return []string{}
}

// registrar grava o rastro da escrita. Chamada SEMPRE de dentro da transação:
// auditoria que cai fora dela pode sobreviver a um rollback, e aí o rastro
// descreve algo que não aconteceu.
func (s *Service) registrar(ctx context.Context, ator Actor, acao, entityID string) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, AuditParams{
		Action:      acao,
		Entity:      audit.EntityAccount,
		EntityID:    entityID,
		UserID:      ator.UserID,
		HouseholdID: ator.HouseholdID,
		IP:          ator.IP,
	})
}

// IsValidationError informa se o erro é de entrada do usuário (400/422) em vez
// de falha interna. O handler usa isto para não vazar erro de infraestrutura
// como se fosse culpa de quem digitou.
func IsValidationError(err error) bool {
	return errors.Is(err, ErrInvalidName) ||
		errors.Is(err, ErrInvalidKind) ||
		errors.Is(err, ErrInvalidAmount) ||
		errors.Is(err, ErrInvalidDate) ||
		errors.Is(err, ErrInvalidInstitution) ||
		errors.Is(err, ErrInvalidStatementDay) ||
		errors.Is(err, ErrStatementDayNotAllowed) ||
		errors.Is(err, ErrInvalidKeyword) ||
		errors.Is(err, ErrDuplicateKeyword) ||
		errors.Is(err, ErrTooManyKeywords)
}

func toView(a Account) View {
	// Conta gravada antes do schema v3 pode ter instituição vazia no banco, e
	// vazio não está no enum do contrato: o cliente receberia um valor que o
	// tipo dele diz ser impossível. O default é aplicado na saída pelo mesmo
	// motivo que na entrada — a coluna nunca deveria estar vazia, e quando
	// estiver, `other` é a verdade.
	instituicao := a.Institution
	if instituicao == "" {
		instituicao = InstitutionOther
	}
	v := View{
		ID:                  a.ID,
		Name:                a.Name,
		Kind:                a.Kind,
		Institution:         instituicao,
		StatementClosingDay: a.StatementClosingDay,
		StatementDueDay:     a.StatementDueDay,
		OpeningBalanceCents: a.OpeningBalanceCents,
		OpeningDate:         a.OpeningDate,
		// ADR-017: o saldo é derivado, nunca coluna. Aqui ele nasce como a
		// abertura, e quem soma os lançamentos é comSaldo — que roda em toda
		// resposta, de leitura ou de escrita. Deixar a soma fora daqui é
		// deliberado: toView não tem contexto nem repositório, e um DTO que
		// consulta banco é o começo da consulta escondida.
		BalanceCents: a.OpeningBalanceCents,
		// Idem para as palavras-chave: nascem `[]` e quem as carrega é
		// respostaCompleta/List — uma consulta por resposta, nunca por conta.
		Keywords:  []string{},
		CreatedAt: a.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: a.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if a.ArchivedAt != nil {
		formatted := a.ArchivedAt.UTC().Format(time.RFC3339)
		v.ArchivedAt = &formatted
	}
	return v
}
