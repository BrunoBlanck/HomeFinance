package category

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/id"
)

// Actor é quem está agindo: a casa e o usuário vêm do TOKEN (nunca do corpo
// nem da URL), e o IP vem da borda HTTP.
type Actor struct {
	HouseholdID string
	UserID      string
	IP          string
}

// Auditor registra o rastro das escritas financeiras (§4.7 do PLANOS.md).
// A gravação acontece DENTRO da transação da escrita que ela descreve.
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

// Transactor executa uma função dentro de uma transação. Sem FK física
// (ADR-013), verificação e escrita precisam ser atômicas.
type Transactor interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Service concentra a regra de negócio das categorias.
type Service struct {
	repo  Repository
	tx    Transactor
	audit Auditor
	usage []UsageChecker
	ids   id.Generator
	clock func() time.Time
}

// Option configura o Service.
type Option func(*Service)

// WithIDs injeta o gerador de IDs (teste).
func WithIDs(g id.Generator) Option { return func(s *Service) { s.ids = g } }

// WithClock injeta o relógio (teste).
func WithClock(c func() time.Time) Option { return func(s *Service) { s.clock = c } }

// WithAudit liga o rastro de auditoria. Sem ele o serviço funciona — os testes
// de unidade não precisam de auditoria para exercitar regra de negócio.
func WithAudit(a Auditor) Option { return func(s *Service) { s.audit = a } }

// WithUsageCheckers registra quem sabe dizer se uma categoria está em uso.
func WithUsageCheckers(checkers ...UsageChecker) Option {
	return func(s *Service) { s.usage = append(s.usage, checkers...) }
}

// NewService monta o serviço.
func NewService(repo Repository, tx Transactor, opts ...Option) *Service {
	s := &Service{
		repo:  repo,
		tx:    tx,
		ids:   id.New,
		clock: func() time.Time { return time.Now().UTC() },
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// CreateInput é o DTO de criação.
//
// Kind só é lido quando ParentID é nulo: a folha HERDA a natureza do grupo
// (invariante 4 da spec 0003). Mandar Kind numa folha não é erro — o contrato
// marca o campo como opcional e o servidor simplesmente manda.
type CreateInput struct {
	Name     string
	Kind     string
	ParentID *string

	// Keywords são as palavras-chave iniciais (spec 0005 §4.1). Ausente ou
	// vazia = nenhuma. Validadas por ValidateKeywords antes de qualquer
	// escrita.
	Keywords []string
}

// UpdateInput é o DTO de edição parcial. ParentID não está aqui de propósito:
// mover categoria de grupo não existe no v1 (invariante 6).
type UpdateInput struct {
	Name *string
	Kind *string

	// Keywords é TRI-ESTADO: nulo = não mexe; presente SUBSTITUI a lista
	// inteira, e a lista vazia limpa (spec 0005 §4.1.3). Não existe
	// "acrescentar": a tela lê a lista atual e manda a nova completa.
	Keywords *[]string
}

// Create cria grupo (ParentID nulo) ou folha (ParentID preenchido).
func (s *Service) Create(ctx context.Context, ator Actor, in CreateInput) (View, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return View{}, ErrNotFound
	}
	name, norm, err := NormalizeName(in.Name)
	if err != nil {
		return View{}, err
	}
	// Palavras-chave validadas ANTES de abrir a transação: é validação pura
	// (forma, teto, repetição) e não depende de estado nenhum.
	//
	// A regra "grupo com subcategoria ativa não recebe palavra" (spec 0005
	// §12, podeReceberPalavras) não se aplica à criação: grupo novo ainda não
	// tem filha, e folha nova não é grupo. E a criação de uma folha sob um
	// grupo que JÁ tem palavras não é recusada — as palavras do pai só ficam
	// inertes (caso residual documentado na spec).
	kws, err := ValidateKeywords(in.Keywords)
	if err != nil {
		return View{}, err
	}

	// A categoria não sobrevive ao rollback de uma tentativa que falhou: não
	// há "própria" a excluir da busca pela dona, por isso o ownID vazio. A
	// operação é re-executável por construção — id novo, estado relido.
	var out View
	err = s.escreverComPalavras(ctx, householdID, "", kws, func(ctx context.Context) error {
		total, err := s.repo.CountAll(ctx, householdID)
		if err != nil {
			return fmt.Errorf("contando categorias: %w", err)
		}
		if total >= MaxPerHousehold {
			return ErrTooMany
		}

		kind := in.Kind
		var parentID *string

		if in.ParentID != nil {
			// O pai é buscado NA CASA DO TOKEN. É o ponto exato onde um
			// parentId de outra casa vira 404 em vez de virar uma categoria
			// pendurada em árvore alheia (S1 do PLANOS.md).
			parent, err := s.repo.ByID(ctx, householdID, *in.ParentID)
			if err != nil {
				return err
			}
			if !parent.IsGroup() {
				// Terceiro nível: recusado por ADR-017b.
				return ErrTooDeep
			}
			// A natureza da folha é SEMPRE a do pai — nunca a do cliente.
			kind = parent.Kind
			parentID = &parent.ID
		} else if !ValidKind(kind) {
			return ErrInvalidKind
		}

		taken, err := s.repo.NameTaken(ctx, householdID, parentID, norm, "")
		if err != nil {
			return fmt.Errorf("verificando nome: %w", err)
		}
		if taken {
			return ErrNameTaken
		}

		now := s.clock()
		created := Category{
			ID:          s.ids(),
			HouseholdID: householdID,
			ParentID:    parentID,
			Name:        name,
			NameNorm:    norm,
			Kind:        kind,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.repo.Create(ctx, &created); err != nil {
			return err
		}
		// As palavras entram na MESMA transação da categoria: sem FK física
		// (ADR-013), categoria criada com palavras pela metade é um estado
		// que o banco não barra.
		if len(kws) > 0 {
			if err := s.gravarPalavras(ctx, householdID, created.ID, kws); err != nil {
				return err
			}
		}
		out = toView(created)
		return s.registrar(ctx, ator, audit.ActionCategoryCreated, created.ID)
	})
	if err != nil {
		return View{}, err
	}
	return s.comPalavras(ctx, householdID, out)
}

// Get devolve uma categoria da casa.
func (s *Service) Get(ctx context.Context, ator Actor, categoryID string) (View, error) {
	c, err := s.repo.ByID(ctx, ator.HouseholdID, categoryID)
	if err != nil {
		return View{}, err
	}
	return s.comPalavras(ctx, ator.HouseholdID, toView(*c))
}

// ListInput filtra a listagem.
type ListInput struct {
	// Kind vazio = as duas naturezas. Valor fora da allowlist é erro, não
	// filtro ignorado — filtro silenciosamente descartado faz a tela mostrar
	// dado que ela não pediu (S4).
	Kind            string
	IncludeArchived bool
}

// List devolve a árvore de dois níveis, separada por natureza.
//
// A árvore é montada em memória de propósito: com teto de 200 categorias por
// casa, o custo é irrelevante, e o código fica idêntico nos quatro dialetos —
// nenhuma consulta hierárquica, nenhuma CTE.
func (s *Service) List(ctx context.Context, ator Actor, in ListInput) (ListView, error) {
	householdID := ator.HouseholdID
	if in.Kind != "" && !ValidKind(in.Kind) {
		return ListView{}, ErrInvalidKind
	}

	rows, err := s.repo.List(ctx, householdID, in.IncludeArchived)
	if err != nil {
		return ListView{}, fmt.Errorf("listando categorias: %w", err)
	}
	// UMA consulta de palavras-chave para a casa inteira, distribuída por
	// categoria em memória — nunca uma consulta por categoria (N+1).
	porDona, err := s.palavrasPorDona(ctx, householdID)
	if err != nil {
		return ListView{}, err
	}

	// Índice das filhas por pai, e a lista de grupos na ordem que veio do
	// banco (já ordenada por nome normalizado).
	filhasPorPai := make(map[string][]Category)
	grupos := make([]Category, 0, len(rows))
	for _, c := range rows {
		if c.IsGroup() {
			grupos = append(grupos, c)
			continue
		}
		filhasPorPai[*c.ParentID] = append(filhasPorPai[*c.ParentID], c)
	}

	// Os quatro arrays nascem inicializados: o contrato os marca como
	// `required` e a tela não trata null.
	out := ListView{Income: []View{}, Expense: []View{}, Investment: []View{}, Redemption: []View{}}
	for _, grupo := range grupos {
		if in.Kind != "" && grupo.Kind != in.Kind {
			continue
		}
		v := toView(grupo)
		v.Keywords = palavrasDe(porDona, grupo.ID)
		for _, filha := range filhasPorPai[grupo.ID] {
			f := toView(filha)
			f.Keywords = palavrasDe(porDona, filha.ID)
			v.Children = append(v.Children, f)
		}
		// Ordem estável e previsível dentro do grupo.
		sort.SliceStable(v.Children, func(i, j int) bool {
			return v.Children[i].Name < v.Children[j].Name
		})
		// switch sobre a allowlist FECHADA, e não if/else: natureza fora dela
		// não existe (ValidKind na borda) e, se existisse, o else a jogaria
		// silenciosamente na despesa — que é exatamente como uma categoria de
		// resgate apareceria num seletor de despesa.
		switch grupo.Kind {
		case KindIncome:
			out.Income = append(out.Income, v)
		case KindExpense:
			out.Expense = append(out.Expense, v)
		case KindInvestment:
			out.Investment = append(out.Investment, v)
		case KindRedemption:
			out.Redemption = append(out.Redemption, v)
		}
	}
	return out, nil
}

// Update aplica edição parcial.
func (s *Service) Update(ctx context.Context, ator Actor, categoryID string, in UpdateInput) (View, error) {
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
	err := s.escreverComPalavras(ctx, householdID, categoryID, kws, func(ctx context.Context) error {
		current, err := s.repo.ByID(ctx, householdID, categoryID)
		if err != nil {
			return err
		}

		if in.Name != nil {
			name, norm, err := NormalizeName(*in.Name)
			if err != nil {
				return err
			}
			if norm != current.NameNorm {
				taken, err := s.repo.NameTaken(ctx, householdID, current.ParentID, norm, current.ID)
				if err != nil {
					return fmt.Errorf("verificando nome: %w", err)
				}
				if taken {
					return ErrNameTaken
				}
			}
			current.Name, current.NameNorm = name, norm
		}

		// Filhas que precisam ACOMPANHAR a troca de natureza (ADR-029c). Vazia
		// quando não há troca, quando a categoria é folha ou quando o grupo
		// não tem filhas.
		var cascata []Category
		if in.Kind != nil && *in.Kind != current.Kind {
			filhas, err := s.podeTrocarNatureza(ctx, current, *in.Kind)
			if err != nil {
				return err
			}
			current.Kind = *in.Kind
			cascata = filhas
		}

		if in.Keywords != nil {
			if err := s.podeReceberPalavras(ctx, current, kws); err != nil {
				return err
			}
			if err := s.gravarPalavras(ctx, householdID, current.ID, kws); err != nil {
				return err
			}
		}

		current.UpdatedAt = s.clock()
		if err := s.repo.Update(ctx, current); err != nil {
			return err
		}
		// A cascata roda na MESMA transação da gravação do grupo: se a
		// gravação de uma filha falhar, nada muda — grupo de aporte com filha
		// de despesa é um estado que a tela não sabe desenhar e que o
		// pareamento não sabe responder.
		if err := s.propagarNatureza(ctx, householdID, current.Kind, current.UpdatedAt, cascata); err != nil {
			return err
		}
		out = toView(*current)
		// A auditoria é o evento que já existia (spec 0005 §4.1.5): sem as
		// palavras — só ação, entidade, id, ator e IP.
		return s.registrar(ctx, ator, audit.ActionCategoryUpdated, current.ID)
	})
	if err != nil {
		return View{}, err
	}
	return s.comPalavras(ctx, householdID, out)
}

// podeReceberPalavras aplica a emenda §12 da spec 0005: grupo com subcategoria
// ATIVA não recebe lançamento diretamente (o seletor só oferece folhas e
// grupos sem filhas), então palavra-chave nele nunca sugeriria nada — lista
// NÃO vazia é recusada com ErrKeywordsOnGroupWithChildren. Limpar (`[]`)
// continua aceito, e grupo sem filha ativa (nenhuma, ou só arquivadas e
// excluídas) segue aceitando.
//
// UMA consulta de filhas, e só quando há o que recusar (grupo E lista não
// vazia); folha nunca consulta. É a mesma Children que a exclusão e a troca
// de natureza usam — o filtro por "ativa" é feito aqui, em memória, porque a
// exclusão precisa enxergar as arquivadas e esta regra não.
//
// Caso residual, aceito pela spec: grupo COM palavras que só DEPOIS ganha uma
// subcategoria mantém as palavras, inertes — a criação da filha não é
// recusada por isso (Create não olha as palavras do pai), e é o classificador
// (internal/classify) quem já deixa de fora a palavra de grupo com filha
// ativa. GET e List continuam devolvendo a lista, para a tela mostrar a nota
// com a contagem.
func (s *Service) podeReceberPalavras(ctx context.Context, current *Category, kws []Keyword) error {
	if len(kws) == 0 || !current.IsGroup() {
		return nil
	}
	filhas, err := s.repo.Children(ctx, current.HouseholdID, current.ID)
	if err != nil {
		return fmt.Errorf("carregando filhas: %w", err)
	}
	for i := range filhas {
		if filhas[i].ArchivedAt == nil {
			return ErrKeywordsOnGroupWithChildren
		}
	}
	return nil
}

// podeTrocarNatureza aplica os invariantes 4 e 5 da spec 0003 com a emenda do
// ADR-029c, e devolve as FILHAS que precisam acompanhar a troca.
//
// A regra depende do LADO DO DINHEIRO:
//
//   - MESMO lado (`expense ↔ investment`, `income ↔ redemption`): permitido
//     mesmo com a categoria em uso e mesmo com subcategorias. Nenhum
//     lançamento existente muda de sinal, de conta ou de saldo, e todos
//     continuam válidos contra AceitaLancamento — a troca é de rótulo de
//     intenção. Sem ela a alínea seria inútil na prática: mover categoria de
//     grupo não existe no v1 (ErrParentImmutable), então quem já tem uma
//     categoria "Investimentos" cheia de lançamentos não teria saída.
//   - CRUZANDO o lado (`expense ↔ income`): continua recusado com
//     ErrKindLocked assim que houver filha ou uso. Transformaria despesa
//     registrada em receita, inverteria o resultado de meses fechados e
//     deixaria TODO lançamento da categoria em violação do pareamento.
//
// Folha continua travada em QUALQUER direção: ela herda a natureza do grupo
// (ADR-017b), e mudar só a folha criaria uma filha de natureza diferente do
// pai — que é o mesmo estado que a cascata do grupo existe para evitar.
//
// UMA consulta de filhas, sempre a mesma que a exclusão e o arquivamento
// usam; ela inclui as ARQUIVADAS de propósito (a cascata precisa delas) e
// nunca as excluídas nem as de outra casa (o repositório filtra as duas).
func (s *Service) podeTrocarNatureza(ctx context.Context, current *Category, novo string) ([]Category, error) {
	if !ValidKind(novo) {
		return nil, ErrInvalidKind
	}
	if !current.IsGroup() {
		return nil, ErrKindLocked
	}
	filhas, err := s.repo.Children(ctx, current.HouseholdID, current.ID)
	if err != nil {
		return nil, fmt.Errorf("carregando filhas: %w", err)
	}
	if mesmoLadoDoDinheiro(current.Kind, novo) {
		return filhas, nil
	}
	if len(filhas) > 0 {
		return nil, ErrKindLocked
	}
	usada, err := s.inUse(ctx, current.HouseholdID, current.ID)
	if err != nil {
		return nil, err
	}
	if usada {
		return nil, ErrKindLocked
	}
	return nil, nil
}

// propagarNatureza desce a natureza nova para todas as subcategorias, ativas e
// arquivadas, dentro da transação já aberta pelo chamador (ADR-029c).
//
// Arquivada também: arquivar não desfaz a marcação do passado (PLANOS.md
// §4.4), e uma filha arquivada que ficasse com a natureza velha voltaria
// divergente do grupo no dia em que fosse desarquivada.
//
// A casa é reconferida em Go mesmo com o repositório já filtrando por
// household_id: esta é uma ESCRITA, e a única resposta segura a uma linha de
// outra casa que tenha vazado da fonte é não tocá-la. O mesmo para a excluída,
// que não tem por que voltar a ser gravada.
//
// **A cascata NÃO gera um evento de auditoria por filha**, e isso é decidido: o
// rastro é o `category.updated` do GRUPO, com autor e IP, e a natureza da filha
// não é editável por conta própria (folha é sempre ErrKindLocked). Um evento
// por subcategoria multiplicaria o rastro de UMA decisão humana por N linhas e
// esconderia, no meio delas, o que uma pessoa de fato pediu — a mesma razão
// pela qual a semente não é auditada linha a linha.
func (s *Service) propagarNatureza(ctx context.Context, householdID, kind string, at time.Time, filhas []Category) error {
	for i := range filhas {
		if filhas[i].HouseholdID != householdID || filhas[i].DeletedAt != nil {
			continue
		}
		if filhas[i].Kind == kind {
			continue
		}
		filhas[i].Kind = kind
		filhas[i].UpdatedAt = at
		if err := s.repo.Update(ctx, &filhas[i]); err != nil {
			return fmt.Errorf("propagando natureza para a subcategoria: %w", err)
		}
	}
	return nil
}

// Archive arquiva a categoria. Arquivar um GRUPO arquiva as filhas junto, na
// mesma transação: grupo invisível com filha visível é um estado que a tela
// não sabe desenhar. Idempotente.
func (s *Service) Archive(ctx context.Context, ator Actor, categoryID string) (View, error) {
	householdID := ator.HouseholdID
	var out View
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		current, err := s.repo.ByID(ctx, householdID, categoryID)
		if err != nil {
			return err
		}
		now := s.clock()

		if current.IsGroup() {
			filhas, err := s.repo.Children(ctx, householdID, current.ID)
			if err != nil {
				return fmt.Errorf("carregando filhas: %w", err)
			}
			for i := range filhas {
				if filhas[i].ArchivedAt != nil {
					continue
				}
				filhas[i].ArchivedAt = &now
				filhas[i].UpdatedAt = now
				if err := s.repo.Update(ctx, &filhas[i]); err != nil {
					return err
				}
			}
		}

		if current.ArchivedAt == nil {
			current.ArchivedAt = &now
			current.UpdatedAt = now
			if err := s.repo.Update(ctx, current); err != nil {
				return err
			}
		}
		// Arquivar NÃO toca nas palavras-chave: a categoria as mantém, e é o
		// classificador quem a deixa fora da correspondência (spec 0005 §4.1.4).
		out = toView(*current)
		return s.registrar(ctx, ator, audit.ActionCategoryArchived, current.ID)
	})
	if err != nil {
		return View{}, err
	}
	return s.comPalavras(ctx, householdID, out)
}

// Unarchive desarquiva a categoria.
//
// Folha só volta se o grupo dela estiver ativo — o contrário produziria a
// mesma inconsistência que o arquivamento em cascata evita. Desarquivar um
// grupo NÃO desarquiva as filhas: quem arquivou o grupo pode ter arquivado
// filhas antes, por motivos próprios, e restaurar tudo apagaria essa escolha.
func (s *Service) Unarchive(ctx context.Context, ator Actor, categoryID string) (View, error) {
	householdID := ator.HouseholdID
	var out View
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		current, err := s.repo.ByID(ctx, householdID, categoryID)
		if err != nil {
			return err
		}
		if current.ArchivedAt == nil {
			out = toView(*current)
			return nil
		}
		if current.ParentID != nil {
			parent, err := s.repo.ByID(ctx, householdID, *current.ParentID)
			if err != nil {
				return err
			}
			if parent.ArchivedAt != nil {
				return ErrParentArchived
			}
		}
		taken, err := s.repo.NameTaken(ctx, householdID, current.ParentID, current.NameNorm, current.ID)
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
		return s.registrar(ctx, ator, audit.ActionCategoryUnarchived, current.ID)
	})
	if err != nil {
		return View{}, err
	}
	return s.comPalavras(ctx, householdID, out)
}

// Delete exclui logicamente, e só se a categoria não tiver filhas nem uso.
func (s *Service) Delete(ctx context.Context, ator Actor, categoryID string) error {
	householdID := ator.HouseholdID
	return s.tx.Do(ctx, func(ctx context.Context) error {
		current, err := s.repo.ByID(ctx, householdID, categoryID)
		if err != nil {
			return err
		}

		// Filhas contam como uso, e esta é a única forma de "em uso" que já
		// existe na E1 — é ela que o teste de aceite 2 da spec 0003 exercita.
		filhas, err := s.repo.Children(ctx, householdID, current.ID)
		if err != nil {
			return fmt.Errorf("carregando filhas: %w", err)
		}
		if len(filhas) > 0 {
			return ErrInUse
		}

		usada, err := s.inUse(ctx, householdID, current.ID)
		if err != nil {
			return err
		}
		if usada {
			return ErrInUse
		}
		if err := s.repo.SoftDelete(ctx, householdID, current.ID, s.clock()); err != nil {
			return err
		}
		// As palavras-chave saem FISICAMENTE, na mesma transação, mesmo com
		// a exclusão da categoria sendo lógica: palavra-chave não é dado
		// financeiro, e mantida ela seguiria ocupando a vaga no índice único
		// da casa — a palavra ficaria "em uso" por uma categoria que ninguém
		// mais vê (ADR-013: sem FK física, é o UnitOfWork que limpa).
		if err := s.repo.DeleteKeywords(ctx, householdID, current.ID); err != nil {
			return fmt.Errorf("apagando palavras-chave: %w", err)
		}
		return s.registrar(ctx, ator, audit.ActionCategoryDeleted, current.ID)
	})
}

// inUse consulta os verificadores registrados. Erro aborta a exclusão: tratar
// indisponibilidade como "não está em uso" causaria perda de dado.
func (s *Service) inUse(ctx context.Context, householdID, categoryID string) (bool, error) {
	for _, checker := range s.usage {
		used, err := checker.CategoryInUse(ctx, householdID, categoryID)
		if err != nil {
			return false, fmt.Errorf("verificando uso da categoria: %w", err)
		}
		if used {
			return true, nil
		}
	}
	return false, nil
}

// registrar grava o rastro da escrita, sempre de dentro da transação: rastro
// que sobrevive a um rollback descreve algo que não aconteceu.
func (s *Service) registrar(ctx context.Context, ator Actor, acao, entityID string) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, AuditParams{
		Action:      acao,
		Entity:      audit.EntityCategory,
		EntityID:    entityID,
		UserID:      ator.UserID,
		HouseholdID: ator.HouseholdID,
		IP:          ator.IP,
	})
}

// gravarPalavras SUBSTITUI a lista de palavras-chave da categoria, dentro da
// transação em curso.
//
// A pré-checagem via KeywordOwners é o que permite o 409 citar a dona: ela
// só enxerga a casa do token (o repositório filtra por household_id) e ignora
// a própria categoria, cuja lista está sendo trocada. A corrida que escapa
// dela — duas edições disputando a mesma palavra ao mesmo tempo — é decidida
// pelo índice único em ReplaceKeywords, que devolve ErrKeywordTaken sem dona.
//
// Recebe as Keywords já validadas (Keyword, Norm, Position) e preenche o que
// é do servidor: id, casa, categoria e instante.
func (s *Service) gravarPalavras(ctx context.Context, householdID, categoryID string, kws []Keyword) error {
	if len(kws) > 0 {
		donas, err := s.repo.KeywordOwners(ctx, householdID, norms(kws))
		if err != nil {
			return fmt.Errorf("verificando palavras-chave: %w", err)
		}
		for _, k := range kws {
			if dona, existe := donas[k.Norm]; existe && dona != categoryID {
				return &KeywordTakenError{Keyword: k.Keyword, OwnerID: dona}
			}
		}
	}
	now := s.clock()
	for i := range kws {
		kws[i].ID = s.ids()
		kws[i].HouseholdID = householdID
		kws[i].CategoryID = categoryID
		kws[i].CreatedAt = now
	}
	if err := s.repo.ReplaceKeywords(ctx, householdID, categoryID, kws); err != nil {
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
// duas são: releem o estado atual e geram ids novos a cada execução, e a
// transação anterior foi desfeita por inteiro.
//
// Sem palavras na lista não há corrida a resolver (ReplaceKeywords com lista
// vazia só apaga), e a transação roda uma vez, sem tratamento.
func (s *Service) escreverComPalavras(ctx context.Context, householdID, categoryID string, kws []Keyword, op func(ctx context.Context) error) error {
	if len(kws) == 0 {
		return s.tx.Do(ctx, op)
	}
	err := s.donaNaCorrida(ctx, householdID, categoryID, kws, s.tx.Do(ctx, op))
	if !colisaoSemDona(err) {
		return err
	}
	// Desfecho 2: a dona sumiu, a colisão também. Uma tentativa a mais.
	err = s.donaNaCorrida(ctx, householdID, categoryID, kws, s.tx.Do(ctx, op))
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
func (s *Service) donaNaCorrida(ctx context.Context, householdID, categoryID string, kws []Keyword, err error) error {
	if !colisaoSemDona(err) || len(kws) == 0 {
		return err
	}
	donas, lookupErr := s.repo.KeywordOwners(ctx, householdID, norms(kws))
	if lookupErr != nil {
		// O erro original prevalece: ele já diz o que importa (409).
		return err
	}
	for _, k := range kws {
		if dona, existe := donas[k.Norm]; existe && dona != categoryID {
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
// distribui por categoria, na forma exibível e na ordem de cadastro.
func (s *Service) palavrasPorDona(ctx context.Context, householdID string) (map[string][]string, error) {
	kws, err := s.repo.ListKeywords(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("listando palavras-chave: %w", err)
	}
	// O repositório já ordena por (categoria, posição); a ordenação estável
	// aqui é só para a resposta não depender de quem implementa a interface.
	sort.SliceStable(kws, func(i, j int) bool { return kws[i].Position < kws[j].Position })
	out := make(map[string][]string)
	for _, k := range kws {
		out[k.CategoryID] = append(out[k.CategoryID], k.Keyword)
	}
	return out, nil
}

// palavrasDe devolve a lista da categoria, ou `[]` — nunca nil, porque o
// campo é `required` no contrato e a tela não trata null.
func palavrasDe(porDona map[string][]string, categoryID string) []string {
	if p := porDona[categoryID]; p != nil {
		return p
	}
	return []string{}
}

// comPalavras completa a View de UMA categoria com as suas palavras-chave.
// Usada por toda resposta de escrita e pelo GET, para PATCH e GET devolverem
// exatamente a mesma lista.
func (s *Service) comPalavras(ctx context.Context, householdID string, v View) (View, error) {
	porDona, err := s.palavrasPorDona(ctx, householdID)
	if err != nil {
		return View{}, err
	}
	v.Keywords = palavrasDe(porDona, v.ID)
	return v, nil
}

// IsValidationError informa se o erro é de entrada do usuário.
func IsValidationError(err error) bool {
	return errors.Is(err, ErrInvalidName) ||
		errors.Is(err, ErrInvalidKind) ||
		errors.Is(err, ErrInvalidKeyword) ||
		errors.Is(err, ErrDuplicateKeyword) ||
		errors.Is(err, ErrTooManyKeywords) ||
		errors.Is(err, ErrKeywordsOnGroupWithChildren)
}

func toView(c Category) View {
	v := View{
		ID:        c.ID,
		Name:      c.Name,
		Kind:      c.Kind,
		ParentID:  c.ParentID,
		Keywords:  []string{},
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt: c.UpdatedAt.UTC().Format(time.RFC3339),
		Children:  []View{},
	}
	if c.ArchivedAt != nil {
		formatted := c.ArchivedAt.UTC().Format(time.RFC3339)
		v.ArchivedAt = &formatted
	}
	return v
}
