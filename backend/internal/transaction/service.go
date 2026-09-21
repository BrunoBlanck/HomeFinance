package transaction

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/id"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// Actor é quem está agindo: a casa e o usuário vêm do TOKEN (nunca do corpo nem
// da URL), e o IP vem da borda HTTP.
//
// Existe como valor com nome, e não como três strings soltas em seis
// assinaturas, porque a próxima pessoa a somar um método não pode ter dúvida
// sobre de onde cada uma sai — e é dessa dúvida que nasce um household_id vindo
// do cliente.
type Actor struct {
	HouseholdID string
	UserID      string
	IP          string
}

// Auditor registra o rastro das escritas financeiras (§4.7 do PLANOS.md).
//
// Interface no consumidor, implementada por audit.Service. A gravação acontece
// DENTRO da transação da escrita que ela descreve: se o registro não couber, a
// escrita não vale.
type Auditor interface {
	Record(ctx context.Context, p AuditParams) error
}

// AuditParams é o evento a registrar. Espelha audit.Params sem que este pacote
// precise importar aquele — e, como ele, NÃO tem campo de valor: auditoria de
// dinheiro guarda quem mexeu no quê, nunca quanto (S8 do PLANOS.md).
type AuditParams struct {
	Action      string
	Entity      string
	EntityID    string
	UserID      string
	HouseholdID string
	IP          string
}

// Transactor executa uma função dentro de uma transação.
//
// Necessário porque não há chave estrangeira física (ADR-013): a conferência de
// que a conta, a categoria e a fatura são desta casa só vale se acontecer na
// MESMA transação da escrita que ela autoriza (spec 0004 §6.6 — TOCTOU).
type Transactor interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Accounts é o que o lançamento precisa saber de conta.
//
// Interface mínima declarada aqui, no consumidor; quem implementa é o
// repositório de contas em platform/storage — o mesmo desenho de todo
// repositório do projeto. Repare que ela recebe householdID em tudo: é a defesa
// de BOLA aplicada também às referências, e não só à entidade principal.
type Accounts interface {
	ByID(ctx context.Context, householdID, id string) (*account.Account, error)
	List(ctx context.Context, householdID string, includeArchived bool) ([]account.Account, error)
}

// Categories é o que o lançamento precisa saber de categoria.
type Categories interface {
	ByID(ctx context.Context, householdID, id string) (*category.Category, error)
	List(ctx context.Context, householdID string, includeArchived bool) ([]category.Category, error)

	// Children devolve as filhas DIRETAS do grupo, incluindo as arquivadas
	// (excluídas nunca voltam). É o que sustenta a §13 da spec 0005: grupo com
	// ao menos uma filha ATIVA não recebe lançamento.
	//
	// A pergunta é feita à fonte de categorias, e não respondida por uma
	// coluna desnormalizada no grupo, porque a contagem mudaria em toda
	// criação, arquivamento e exclusão de filha — e uma contagem que
	// dessincroniza é uma invariante que passa a mentir. O custo é uma
	// consulta por GRUPO distinto citado na escrita (folha não consulta nada:
	// a árvore tem dois níveis e folha não tem filha).
	Children(ctx context.Context, householdID, parentID string) ([]category.Category, error)

	// LiveStates devolve o estado ATUAL — natureza inclusive — das categorias
	// VIVAS da casa, dentre os ids informados, em UMA consulta e nunca uma por
	// id. Id ausente do mapa não existe mais (excluído, ou de outra casa).
	//
	// É a reconferência dos achados A4 e A9 da revisão de segurança: a execução
	// real do auto-categorize calcula o plano FORA da transação (achado A2) e
	// pergunta isto DENTRO dela, antes dos UPDATE. A mesma pergunta, com a
	// mesma implementação, que POST /investments/detect faz — dois lugares com
	// duas regras de "atribuível" seria uma divergência esperando acontecer.
	//
	// É LiveStates, e não a fachada mais estreita que devolve só os ids
	// atribuíveis, porque aquela DESCARTA o `kind` — e o `kind` é justamente o
	// campo cuja falta era explorável (A9): trocar a natureza de lado é aceito
	// numa categoria de topo, sem filhas e sem uso, que é o estado normal da
	// categoria recém-criada cujo lote esta rota calcula. Quem reconfere sem a
	// natureza aprova gravar despesa em categoria de receita.
	//
	// ARQUIVADA continua no MAPA — só a EXCLUSÃO tira dele —, e é o CHAMADOR
	// que decide o que fazer com ela, pelo campo Archived. Aqui a decisão é
	// RECUSAR: arquivar não desfaz a marcação que já existe, mas também não
	// autoriza marcação NOVA, e o auto-categorize faz atribuição nova (as
	// outras três portas recusam com ErrCategoryArchived). O grupo que ganhou
	// subcategoria ativa volta com HasActiveChild.
	LiveStates(ctx context.Context, householdID string, ids []string) (map[string]category.LiveState, error)
}

// Statements é o que o lançamento precisa saber de fatura.
type Statements interface {
	ByID(ctx context.Context, householdID, id string) (*cardstatement.Statement, error)
}

// Service concentra a regra de negócio dos lançamentos.
type Service struct {
	repo       Repository
	accounts   Accounts
	categories Categories
	statements Statements
	tx         Transactor
	// classifier carrega as palavras-chave da casa para o auto-categorize
	// (spec 0005 §4.3). Obrigatório no construtor por ser dependência de
	// produção; a lógica que o consome entra com o endpoint.
	classifier *classify.Loader
	audit      Auditor
	ids        id.Generator
	clock      func() time.Time

	// planTimeout é o prazo da fase de CÁLCULO das rotas que varrem o mês
	// inteiro (PlanTimeout). Campo, e não constante direta, só para o teste
	// conseguir encurtá-lo — produção nunca o ajusta.
	planTimeout time.Duration
}

// Option configura o Service.
type Option func(*Service)

// WithIDs injeta o gerador de IDs (teste).
func WithIDs(g id.Generator) Option { return func(s *Service) { s.ids = g } }

// WithClock injeta o relógio (teste).
func WithClock(c func() time.Time) Option { return func(s *Service) { s.clock = c } }

// WithPlanTimeout encurta o prazo da fase de cálculo (teste). Valor menor ou
// igual a zero mantém PlanTimeout.
//
// A opção só ENCURTA: o valor é limitado por PlanTimeout (achado A7 da revisão
// de segurança). Um prazo de teste que conseguisse esticar o de produção seria
// um prazo que depende de ninguém chamá-lo errado.
func WithPlanTimeout(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			d = min(d, PlanTimeout)
		}
		s.planTimeout = d
	}
}

// WithAudit liga o rastro de auditoria. Sem ele o serviço funciona, o que é
// deliberado: os testes de unidade não precisam de auditoria para exercitar
// regra de negócio. Em produção ele é ligado em cmd/api/main.go.
func WithAudit(a Auditor) Option { return func(s *Service) { s.audit = a } }

// NewService monta o serviço.
//
// As seis dependências obrigatórias são posicionais de propósito: esquecer de
// ligar o repositório de categorias vira erro de COMPILAÇÃO, e não uma
// categoria que deixa de ser validada em produção. Só o auditor é opcional.
func NewService(
	repo Repository,
	accounts Accounts,
	categories Categories,
	statements Statements,
	tx Transactor,
	classifier *classify.Loader,
	opts ...Option,
) *Service {
	s := &Service{
		repo:        repo,
		accounts:    accounts,
		categories:  categories,
		statements:  statements,
		tx:          tx,
		classifier:  classifier,
		ids:         id.New,
		clock:       func() time.Time { return time.Now().UTC() },
		planTimeout: PlanTimeout,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ListInput é a janela pedida pelo cliente.
//
// O filtro é month + accountId + kindGroup + categoryId e mais nada (spec 0004
// §1.2 e §1.6): busca por descrição, faixa de valor e ordenação entram na E2b.
// Um filtro a menos é uma consulta a menos para revisar — e `sort`, em
// particular, é ordenação vinda do cliente, que sem allowlist é injeção com
// outro nome.
type ListInput struct {
	// Month é "YYYY-MM" e é OBRIGATÓRIO. Sem ele a consulta viraria "todos os
	// lançamentos da casa", que é a página que fica lenta primeiro e a que
	// ninguém pediu.
	//
	// Ele filtra COMPETÊNCIA, não caixa (ADR-023c): o mês do app inteiro é um
	// só, e no cartão é o mês da fatura que a pessoa espera ver.
	Month string

	// AccountID é opcional; vazio quer dizer "todas as contas da casa".
	AccountID string

	// KindGroup é o recorte por TIPO (spec 0004 §12, emenda E2d), da
	// allowlist FECHADA KindGroup*. Vazio é "Tudo" — não existe o valor
	// `all`.
	//
	// Ele recorta a LISTA (predicado no WHERE) e o RESUMO (aritmética em Go
	// sobre Summary.ByKind, nunca um WHERE diferente). O valor é conferido na
	// borda, de novo aqui e de novo no repositório: ele escolhe qual cláusula
	// entra na consulta e jamais vira texto de SQL.
	KindGroup string

	// CategoryID é o recorte por CATEGORIA — o atalho "Ver lançamentos" do
	// relatório por categoria. Vazio quer dizer "todas".
	//
	// GRUPO INCLUI AS FILHAS: pedir uma categoria de nível 1 traz os
	// lançamentos dela e os das subcategorias, arquivadas incluídas. É o
	// conjunto que a linha do grupo soma em GET /reports/by-category, e é o
	// que faz o atalho entre as duas telas mostrar o mesmo dinheiro — trazer
	// só o que foi lançado direto no grupo abriria a lista num subconjunto do
	// número em que a pessoa clicou.
	//
	// A categoria é conferida como sendo DA CASA antes de virar filtro: id de
	// outra casa é 404, igual ao inexistente, como em AccountID (S1).
	CategoryID string

	// Cursor é a forma textual devolvida pela página anterior.
	//
	// O cursor é POSIÇÃO, não filtro: reenviá-lo com outro KindGroup devolve
	// uma página do filtro novo a partir daquela posição. Quem troca de filtro
	// descarta o cursor — é o contrato publicado.
	Cursor string

	// Limit é o tamanho da página; zero usa DefaultPageSize.
	Limit int
}

// List devolve a página e o resumo da MESMA janela.
func (s *Service) List(ctx context.Context, ator Actor, in ListInput) (ListView, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return ListView{}, ErrNotFound
	}
	if _, err := ParseMonth(in.Month); err != nil {
		return ListView{}, err
	}

	// Allowlist do filtro de tipo, de novo (a borda já recusou): grupo
	// desconhecido falha FECHADO, nunca é tratado como "sem filtro". Tratá-lo
	// como "tudo" devolveria a janela INTEIRA para quem pediu um recorte —
	// o modo silencioso de vazar exatamente o que o filtro existia para
	// esconder. O erro não carrega o valor recebido.
	if !ValidKindGroup(in.KindGroup) {
		return ListView{}, fmt.Errorf("%w: na listagem", ErrUnknownKindGroup)
	}

	// A conta do filtro é conferida como sendo DA CASA. Sem isto, pedir a conta
	// da vizinha devolveria uma lista vazia — o que não vaza dado, mas também
	// não é a resposta certa: id que não é meu é 404, igual a id que não
	// existe (S1).
	//
	// E o que segue para a consulta é o id CANÔNICO, nunca o que o cliente
	// mandou: o `ByID` casa por COLLATION (no MySQL 8, `utf8mb4_0900_ai_ci`
	// ignora a caixa), então o filtro pode ser aceito com uma grafia que as
	// comparações em Go do resto do serviço — o nome da conta, o recorte por
	// grupo — não reconheceriam.
	contaDoFiltro := in.AccountID
	if in.AccountID != "" {
		conta, err := s.contaDaCasa(ctx, householdID, in.AccountID)
		if err != nil {
			return ListView{}, err
		}
		contaDoFiltro = conta.ID
	}

	// A categoria do filtro passa pela MESMA porta da conta: id que não é da
	// casa é 404, nunca lista vazia (S1). O que segue para a consulta são os
	// ids CANÔNICOS do banco — o do grupo mais os das filhas —, nunca a
	// grafia que o cliente mandou.
	categoriasDoFiltro, err := s.recorteDeCategoria(ctx, householdID, in.CategoryID)
	if err != nil {
		return ListView{}, err
	}

	cursor, err := ParseCursor(in.Cursor)
	if err != nil {
		return ListView{}, err
	}

	filtro := ListFilter{
		CompetenceMonth: in.Month,
		AccountID:       contaDoFiltro,
		KindGroup:       in.KindGroup,
		CategoryIDs:     categoriasDoFiltro,
	}
	if !cursor.OccurredOn.IsZero() {
		filtro.Cursor = &cursor
	}

	// UMA leitura de contas e categorias por requisição, e ela vem ANTES da
	// consulta: os mesmos nomes que rotulam a página dão o conjunto das
	// categorias de investimento de que o resumo precisa (ADR-029d). Perguntar
	// duas vezes pagaria a consulta duas vezes E deixaria a lista e o resumo
	// responderem a taxonomias diferentes se alguém trocasse a natureza de uma
	// categoria no meio do caminho.
	//
	// Ela acontece mesmo quando a página sai vazia: com cursor no fim do mês a
	// lista acaba, mas o resumo continua falando da JANELA INTEIRA — e sem o
	// conjunto ele somaria aporte dentro de despesa.
	rot, err := s.rotulos(ctx, householdID)
	if err != nil {
		return ListView{}, err
	}
	// Teto do conjunto de categorias: falha FECHADA aqui, antes do resumo e
	// antes da página (ADR-029 j.2) — a leitura da taxonomia logo acima já
	// aconteceu, e é dela que sai a contagem. Passar de MaxPerHousehold
	// significa que o teto da taxonomia da própria casa foi violado: o
	// servidor não sabe mais o que é
	// a taxonomia dela, e qualquer resposta seria invenção. É 500 genérico, e
	// nunca 4xx: não há campo que a pessoa possa corrigir neste pedido.
	if len(rot.marcadas) > category.MaxPerHousehold {
		return ListView{}, fmt.Errorf("%w: %d categorias marcadas", ErrTooManyCategories, len(rot.marcadas))
	}

	// CURTO-CIRCUITO obrigatório (ADR-029f): "Investimentos" numa casa que não
	// marca NENHUMA categoria é resposta vazia, resolvida aqui, em Go, ANTES
	// de tocar o banco.
	//
	// Sem ele, o conjunto vazio desceria para o repositório, que responde
	// ErrEmptyCategoryFilter — de propósito, porque `IN ()` não existe e
	// "vazio = todas as categorias" devolveria o mês inteiro como se fosse
	// investimento —, e o handler traduziria isso em 500 genérico. Ou seja:
	// TODA casa sem categoria de investimento tomaria 500 nesta aba, que é o
	// estado de toda casa no dia da entrega.
	//
	// Cobre a requisição INTEIRA — lista e resumo —, e não só a lista: pedir
	// o resumo aqui daria o mesmo 500 pelo outro caminho. Os dois campos de
	// reconciliação vêm zero porque são zero mesmo: sem categoria marcada não
	// há aporte nem resgate para descontar.
	//
	// O erro do repositório CONTINUA existindo: ele é a defesa em
	// profundidade para o dia em que outro chamador esquecer este atalho.
	if in.KindGroup == KindGroupInvestment && len(rot.marcadas) == 0 {
		return ListView{Items: []View{}, NextCursor: nil, Summary: SummaryView{}}, nil
	}
	filtro.InvestmentCategoryIDs = rot.marcadas

	linhas, proximo, err := s.paginaBruta(ctx, householdID, filtro, in.Limit)
	if err != nil {
		return ListView{}, err
	}

	// O resumo vai SEM recorte de tipo no WHERE: o grupo viaja só para o
	// repositório recusar o que está fora da allowlist. A distinção entre as
	// cinco opções é feita logo abaixo, por aritmética sobre a MESMA leitura
	// (ver recortarResumo).
	resumo, err := s.repo.Summary(ctx, householdID, SummaryFilter{
		CompetenceMonth: in.Month,
		AccountID:       contaDoFiltro,
		KindGroup:       in.KindGroup,
		// O recorte por categoria vai junto: ele é janela, e o resumo fala
		// da MESMA janela da lista. Sem ele aqui, a tela mostraria os totais
		// do mês inteiro embaixo das linhas de uma categoria só.
		CategoryIDs:           categoriasDoFiltro,
		InvestmentCategoryIDs: rot.marcadas,
	})
	if err != nil {
		return ListView{}, fmt.Errorf("resumindo lançamentos: %w", err)
	}
	recorte, err := recortarResumo(resumo, in.KindGroup)
	if err != nil {
		return ListView{}, err
	}
	// A conferência roda sobre o que VAI SER PUBLICADO, e não sobre o que o
	// banco devolveu: é o número que chega à tela que precisa ser defensável.
	// As parcelas cruas continuam sob os olhos dela — recortarResumo preserva
	// ByKind, e é lá que `0 ≤ marcado ≤ total` é verificado por kind.
	if err := conferirResumo(recorte, in.KindGroup, len(rot.marcadas)); err != nil {
		return ListView{}, err
	}

	return ListView{Items: montarViews(linhas, rot), NextCursor: proximo, Summary: toSummaryView(recorte)}, nil
}

// recortarResumo dobra, EM GO, o resumo da janela no recorte pedido — a
// segunda metade do desenho cujo primeiro passo é "o SQL do resumo é o mesmo
// para as cinco opções" (ver SummaryFilter.KindGroup e o comentário de
// Summary no gormstore).
//
// Uma leitura, cinco respostas que não podem discordar entre si. Cinco WHEREs
// diferentes poderiam: bastaria uma escrita entrar entre duas consultas para
// a soma dos recortes deixar de fechar com o total.
//
// ⚠️ InvestedCents e RedeemedCents atravessam o recorte INTACTOS, nas cinco
// opções. Eles não descrevem a janela — descrevem o que SAIU de
// ExpenseCents/IncomeCents (ADR-029e) — e alimentam a frase "Fora destes
// números: R$ X em aportes" (spec 0006 §3.5.2). Zerá-los sob o recorte de
// despesas apagaria a explicação exatamente onde a omissão é maior.
//
// ⚠️ Uncategorized sai APENAS de `income` e `expense` — a de cada recorte é a
// do seu próprio kind, e é ZERO em `transfer` e `investment`. Isso foi MEDIDO,
// não suposto: em ByKind as pernas de transferência trazem Uncategorized IGUAL
// a Count, porque transferência nunca tem categoria (ADR-016) e toda perna
// casa com `category_id IS NULL` na projeção crua. Usar a linha crua da perna
// faria a tela pedir que a pessoa categorizasse transferências, que não têm
// categoria para receber.
//
// O dinheiro vem dos campos JÁ AGREGADOS (IncomeCents/ExpenseCents), que o
// repositório devolve líquidos do marcado; das linhas cruas vêm só as
// CONTAGENS, que não existem prontas. Recalcular o dinheiro aqui criaria uma
// segunda fórmula para o mesmo número.
func recortarResumo(s Summary, grupo string) (Summary, error) {
	if grupo == "" {
		return s, nil
	}

	porKind := make(map[string]SummaryKindTotals, len(s.ByKind))
	for _, k := range s.ByKind {
		porKind[k.Kind] = k
	}
	receita, despesa := porKind[KindIncome], porKind[KindExpense]

	out := Summary{
		InvestedCents: s.InvestedCents,
		RedeemedCents: s.RedeemedCents,
		ByKind:        s.ByKind,
	}
	switch grupo {
	case KindGroupIncome:
		// Receitas SEM os resgates: o dinheiro já vem líquido do repositório,
		// e a contagem desconta as linhas marcadas da mesma leitura.
		out.Count = receita.Count - receita.MarkedCount
		out.IncomeCents = s.IncomeCents
		out.Uncategorized = receita.Uncategorized

	case KindGroupExpense:
		// Despesas SEM os aportes. A despesa sem categoria continua aqui — é
		// ela que o `category_id IS NULL OR …` do repositório preserva, e é
		// ela que este Uncategorized conta.
		out.Count = despesa.Count - despesa.MarkedCount
		out.ExpenseCents = s.ExpenseCents
		out.Uncategorized = despesa.Uncategorized

	case KindGroupTransfer:
		// As DUAS pernas: cada uma é uma linha da lista. Receita, despesa e
		// pendência são zero por desenho — transferência não é receita nem
		// despesa (ADR-016) e não tem categoria para receber.
		out.Count = porKind[KindTransferOut].Count + porKind[KindTransferIn].Count

	case KindGroupInvestment:
		// Aportes (despesa marcada) + resgates (receita marcada). Pendência é
		// zero: aporte e resgate têm categoria por definição — é ela que os
		// marca.
		out.Count = receita.MarkedCount + despesa.MarkedCount

	default:
		// Inalcançável: List e o repositório já recusaram o que está fora da
		// allowlist. Fica fechado mesmo assim — devolver `s` aqui seria
		// publicar a janela inteira sob um recorte desconhecido.
		return Summary{}, fmt.Errorf("%w: ao recortar o resumo", ErrUnknownKindGroup)
	}

	// A identidade é RECONSTITUÍDA, nunca herdada: os dois operandos acabaram
	// de mudar, e conferirResumo a verifica logo em seguida.
	out.NetCents = out.IncomeCents - out.ExpenseCents
	return out, nil
}

// errResumoInconsistente — o resumo voltou do banco com uma parcela NEGATIVA,
// o que só acontece se `0 ≤ marcado ≤ total` tiver sido violado.
//
// A desigualdade é VERIFICADA e nunca confiada (ADR-029 j.1): a prova de que a
// subtração do marcado não produz despesa negativa depende de
// `amount_cents ≥ 0`, que é invariante do CAMINHO DE ESCRITA e não do schema —
// não há CHECK na coluna, e criar um agora seria alteração destrutiva de uma
// coluna povoada. Uma única linha negativa (corrupção, ou um caminho de escrita
// futuro com defeito) publicaria `investedCents` negativo contra um contrato
// que declara `minimum: 0`, ou uma despesa maior que a real.
//
// Clampar em silêncio seria pior do que falhar: esconderia a corrupção e ainda
// assim publicaria um total errado. É a mesma disciplina do `somaSegura` do
// `internal/report` depois do achado B3 — 500 genérico, uma linha de log com
// as CONTAGENS, e nenhum número inventado na tela.
var errResumoInconsistente = errors.New("resumo do mês fora da faixa publicável")

// conferirResumo aplica a alínea (j.1) do ADR-029 antes de publicar.
//
// O repositório já devolve a receita e a despesa LÍQUIDAS do marcado
// (`total − marcado`) e o marcado à parte, então as quatro parcelas não
// negativas dizem exatamente `0 ≤ marcado ≤ total` nos dois lados do dinheiro:
// `marcado ≥ 0` é InvestedCents/RedeemedCents, e `marcado ≤ total` é
// IncomeCents/ExpenseCents. NetCents fica de fora de propósito — ele pode ser
// negativo, e legitimamente: é o mês que fechou no vermelho.
//
// Count e Uncategorized entram na conferência pelo mesmo motivo que o
// `somaSegura` do `internal/report` confere a contagem junto dos centavos: o
// contrato declara `minimum: 0` para os seis campos, e a mesma disciplina
// aplicada ao mesmo tipo de número não deixa exceção que alguém precise
// lembrar de justificar depois. Na prática COUNT(*) nunca vem negativo — o
// valor é que uma projeção futura não publique negativo contra um mínimo já
// publicado.
//
// A mensagem leva só CONTAGENS. Centavos não entram em log (S8), e nem a
// descrição do lançamento: o diagnóstico que interessa é "quantas linhas e
// quantas categorias marcadas havia na janela", não quanto dinheiro era.
//
// A partir da emenda E2d a conferência cobre também o RECORTE por tipo. A
// tabela completa do que é verificado:
//
//	| condição                                        | vale para           |
//	|-------------------------------------------------|---------------------|
//	| as seis parcelas ≥ 0                            | sempre              |
//	| NetCents == IncomeCents − ExpenseCents          | sempre              |
//	| 0 ≤ marcado ≤ total, por kind (valor e contagem)| sempre              |
//	| ExpenseCents == 0                               | income              |
//	| IncomeCents == 0                                | expense             |
//	| receita, despesa e pendência == 0               | transfer/investment |
//
// InvestedCents e RedeemedCents NÃO têm invariante por grupo: eles não
// dependem do grupo, e é justamente disso que vive a frase "Fora destes
// números" (ADR-029e). Exigir zero deles em algum recorte seria codificar o
// contrário do contrato publicado.
//
// A linha por kind é o que impede o recorte de MASCARAR corrupção: sob
// `transfer` a receita publicada é zero por construção, então uma receita
// crua negativa passaria despercebida se só o número publicado fosse
// conferido. Ela é conferida na matéria-prima, que recortarResumo preserva.
func conferirResumo(s Summary, grupo string, marcadas int) error {
	impossivel := func() error {
		return fmt.Errorf("%w: lancamentos=%d sem_categoria=%d categorias_marcadas=%d",
			errResumoInconsistente, s.Count, s.Uncategorized, marcadas)
	}

	if s.IncomeCents < 0 || s.ExpenseCents < 0 || s.InvestedCents < 0 || s.RedeemedCents < 0 ||
		s.Count < 0 || s.Uncategorized < 0 {
		return impossivel()
	}
	// A identidade do líquido é conferida, e não presumida: ela é o que o
	// contrato publica como "sempre", inclusive sob recorte. NetCents continua
	// fora da checagem de ≥ 0 — pode ser negativo, e legitimamente: é o mês
	// que fechou no vermelho.
	if s.NetCents != s.IncomeCents-s.ExpenseCents {
		return impossivel()
	}
	for _, k := range s.ByKind {
		if k.Count < 0 || k.TotalCents < 0 || k.Uncategorized < 0 ||
			k.MarkedCount < 0 || k.MarkedTotalCents < 0 ||
			k.MarkedCount > k.Count || k.MarkedTotalCents > k.TotalCents {
			return impossivel()
		}
	}

	switch grupo {
	case KindGroupIncome:
		if s.ExpenseCents != 0 {
			return impossivel()
		}
	case KindGroupExpense:
		if s.IncomeCents != 0 {
			return impossivel()
		}
	case KindGroupTransfer, KindGroupInvestment:
		if s.IncomeCents != 0 || s.ExpenseCents != 0 || s.Uncategorized != 0 {
			return impossivel()
		}
	}
	return nil
}

// ListByStatement devolve os lançamentos de UMA fatura, paginados pelo mesmo
// cursor da listagem.
//
// É método próprio, e não mais um campo em ListInput, porque a pergunta é
// outra: aqui o recurso é a fatura (que precisa ser conferida como da casa
// antes de qualquer coisa), e o mês não entra — as linhas da fatura são as
// linhas daquele statement_id, ponto.
func (s *Service) ListByStatement(ctx context.Context, ator Actor, statementID, cursorTexto string, limit int) ([]View, *string, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return nil, nil, ErrNotFound
	}
	if _, err := s.faturaDaCasa(ctx, householdID, statementID); err != nil {
		return nil, nil, err
	}

	cursor, err := ParseCursor(cursorTexto)
	if err != nil {
		return nil, nil, err
	}

	filtro := ListFilter{StatementID: statementID}
	if !cursor.OccurredOn.IsZero() {
		filtro.Cursor = &cursor
	}
	return s.pagina(ctx, householdID, filtro, limit)
}

// ByID devolve um lançamento da casa.
func (s *Service) ByID(ctx context.Context, ator Actor, transactionID string) (View, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return View{}, ErrNotFound
	}
	t, err := s.repo.ByID(ctx, householdID, transactionID)
	if err != nil {
		return View{}, err
	}
	rot, err := s.rotulos(ctx, householdID)
	if err != nil {
		return View{}, err
	}
	return toView(*t, rot.contas, rot.categorias), nil
}

// SoftDelete exclui logicamente, e exclui o PAR INTEIRO quando o lançamento é
// perna de transferência (ADR-016).
//
// Excluir só uma perna faria dinheiro sair de uma conta sem entrar na outra: o
// saldo de uma delas ficaria errado, e a pessoa não teria como descobrir por
// quê — a linha que explicaria sumiu. As duas exclusões acontecem na MESMA
// transação, com uma entrada de auditoria para cada uma.
func (s *Service) SoftDelete(ctx context.Context, ator Actor, transactionID string) error {
	householdID := ator.HouseholdID
	if householdID == "" {
		return ErrNotFound
	}

	return s.tx.Do(ctx, func(ctx context.Context) error {
		atual, err := s.repo.ByID(ctx, householdID, transactionID)
		if err != nil {
			return err
		}

		alvos, err := s.pernasVivas(ctx, householdID, atual)
		if err != nil {
			return err
		}

		agora := s.clock()
		for _, alvo := range alvos {
			if err := s.repo.SoftDelete(ctx, householdID, alvo, agora); err != nil {
				return err
			}
			if err := s.registrar(ctx, ator, audit.ActionTransactionDeleted, alvo); err != nil {
				return err
			}
		}
		return nil
	})
}

// Restore desfaz a exclusão lógica — a exceção estreita do ADR-025(f).
//
// Ela existe por causa do beco sem saída que o índice único cria: linha
// excluída continua ocupando (dedup_key, dedup_ordinal), então reimportar o
// arquivo bateria num duplicado que ninguém consegue liberar. Restaurar é a
// operação certa conceitualmente — a identidade já existe, e "importar de novo"
// é desfazer a exclusão.
//
// O que ela toca: deleted_at e updated_at. Mais nada. Valor, data, conta,
// categoria e competência do lançamento restaurado são os ORIGINAIS, nunca os
// do arquivo que pediu a restauração — quem garante isso é o repositório, cujo
// SET tem exatamente duas colunas.
//
// Transferência é restaurada aos pares, pelo mesmo motivo de ser excluída aos
// pares: meia transferência não existe (ADR-016).
func (s *Service) Restore(ctx context.Context, ator Actor, transactionID string) (View, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return View{}, ErrNotFound
	}

	var restaurado *Transaction
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		agora := s.clock()

		// A conferência de casa e de existência é do próprio Restore do
		// repositório: ele filtra household_id e deleted_at IS NOT NULL e
		// devolve ErrNotFound quando nada casa. ByID não serviria aqui — ele
		// esconde o que está excluído, que é justamente o que queremos.
		if err := s.repo.Restore(ctx, householdID, transactionID, agora); err != nil {
			return err
		}
		if err := s.registrar(ctx, ator, audit.ActionTransactionRestored, transactionID); err != nil {
			return err
		}

		atual, err := s.repo.ByID(ctx, householdID, transactionID)
		if err != nil {
			return err
		}
		restaurado = atual

		if !atual.IsTransfer() || atual.TransferGroupID == nil {
			return nil
		}
		pernas, err := s.repo.ByTransferGroup(ctx, householdID, *atual.TransferGroupID)
		if err != nil {
			return fmt.Errorf("carregando par da transferência: %w", err)
		}
		for i := range pernas {
			if pernas[i].ID == transactionID || pernas[i].DeletedAt == nil {
				continue
			}
			if err := s.repo.Restore(ctx, householdID, pernas[i].ID, agora); err != nil {
				return err
			}
			if err := s.registrar(ctx, ator, audit.ActionTransactionRestored, pernas[i].ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return View{}, err
	}

	rot, err := s.rotulos(ctx, householdID)
	if err != nil {
		return View{}, err
	}
	return toView(*restaurado, rot.contas, rot.categorias), nil
}

// UpdateCategory troca a categoria de UM lançamento — a emenda §11 da spec
// 0005 (atalho "Sem categoria" em /lancamentos). É o único campo que o PATCH
// aceita nesta entrega; valor, data, descrição e conta ficam para a E2b.
//
// A ordem das conferências é a do contrato, e cada uma tem um motivo:
//   - o lançamento é lido PELA CASA DO TOKEN: inexistente e de outra casa são
//     o MESMO ErrNotFound (S1), e a leitura acontece dentro da transação da
//     escrita, junto da conferência da categoria (TOCTOU — spec 0004 §6.6);
//   - perna de transferência é recusada ANTES de a categoria ser procurada
//     (ErrCategoryOnTransfer → 422): transferência não tem categoria por
//     desenho (ADR-016), e não há por que gastar uma consulta para descobrir
//     algo que a resposta não vai usar;
//   - a categoria é buscada pela casa (de outra casa é o mesmo 404 de
//     inexistente), arquivada é 422 (a pessoa precisa saber que basta
//     desarquivar) e natureza trocada é 422 — são as MESMAS regras do
//     CreateBatch, e o relatório soma por natureza.
//
// Categoria igual à atual é sucesso sem escrita e sem auditoria: registrar
// "atualizado" para uma linha que não mudou seria um rastro que mente. É
// também o que elimina a única forma de o UPDATE afetar zero linhas por
// "nada mudou" (MySQL conta só mudança real): quando o repositório é chamado,
// updated_at sempre muda.
//
// A resposta é o lançamento RELIDO depois da escrita — a mesma View de ByID,
// com os nomes de conta e de categoria resolvidos —, e não a linha remendada
// em memória: o que a tela mostra é o que o banco tem.
func (s *Service) UpdateCategory(ctx context.Context, ator Actor, transactionID, categoryID string) (View, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return View{}, ErrNotFound
	}
	if categoryID == "" {
		return View{}, ErrCategoryRequired
	}

	var atualizado *Transaction
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		atual, err := s.repo.ByID(ctx, householdID, transactionID)
		if err != nil {
			return err
		}
		if atual.IsTransfer() {
			return ErrCategoryOnTransfer
		}

		cat, err := s.categoriaDaCasa(ctx, householdID, categoryID)
		if err != nil {
			return err
		}
		if cat.ArchivedAt != nil {
			return ErrCategoryArchived
		}
		if !categoriaCombina(atual.Kind, cat.Kind) {
			return ErrCategoryKindMismatch
		}
		// Por último entre as recusas da categoria: é a única que custa uma
		// consulta, e natureza trocada já teria saído antes dela.
		if err := s.recusarGrupoComFilhas(ctx, householdID, cat, nil); err != nil {
			return err
		}

		if atual.CategoryID != nil && *atual.CategoryID == cat.ID {
			atualizado = atual
			return nil
		}

		if err := s.repo.UpdateCategory(ctx, householdID, atual.ID, cat.ID, s.clock()); err != nil {
			// ErrNotFound aqui é anomalia: a linha foi lida viva, nesta
			// transação, nesta casa, com kind categorizável. Ainda assim
			// atravessa como 404 — é a resposta honesta para "não havia o que
			// atualizar", e nada foi gravado.
			return fmt.Errorf("atualizando categoria do lançamento: %w", err)
		}
		if err := s.registrar(ctx, ator, audit.ActionTransactionUpdated, atual.ID); err != nil {
			return err
		}

		relido, err := s.repo.ByID(ctx, householdID, atual.ID)
		if err != nil {
			return fmt.Errorf("relendo lançamento categorizado: %w", err)
		}
		atualizado = relido
		return nil
	})
	if err != nil {
		return View{}, err
	}

	rot, err := s.rotulos(ctx, householdID)
	if err != nil {
		return View{}, err
	}
	return toView(*atualizado, rot.contas, rot.categorias), nil
}

// NewTransaction é UMA linha a gravar.
//
// Repare no que NÃO existe aqui, e é por construção que não existe (S2 — mass
// assignment): householdId, id, competenceMonth, yearMonth, descriptionNorm,
// dedupOrdinal, source, createdBy, createdAt e deletedAt. Todos são derivados
// ou vêm do token; nenhum é aceitável vindo de fora.
//
// DedupKey é a exceção que confirma a regra: ela vem calculada da importação
// porque é lá que existem a instituição, o external_id e a descrição
// sanitizada — e recalculá-la aqui, sobre outros valores, produziria uma chave
// diferente da que a análise usou e cegaria a deduplicação (ADR-025c).
type NewTransaction struct {
	Kind        string
	AccountID   string
	CategoryID  *string
	AmountCents int64

	// Description já vem sanitizada e truncada em MaxDescriptionLen pela
	// importação (spec 0004 §6.8).
	Description string

	OccurredOn civil.Date

	// StatementID liga a linha à fatura. Quando presente, é ELE que decide a
	// competência do lançamento.
	StatementID *string

	// TransferGroupID amarra as duas pernas de uma transferência.
	TransferGroupID *string

	ExternalID *string
	DedupKey   string
}

// CreateBatchInput é uma escrita em lote.
//
// Lote, e não linha a linha, porque as duas escritas mais importantes do
// domínio são múltiplas e indivisíveis: a transferência (duas pernas) e a
// importação (centenas de linhas).
type CreateBatchInput struct {
	// Source é manual ou import. Nesta entrega só a importação grava.
	Source string

	// ImportBatchID é obrigatório quando Source é import: é ele que dá a
	// rastreabilidade por lançamento e que torna aceitável a importação gerar
	// UMA entrada de auditoria em vez de 10.000 (spec 0004 §6.10).
	ImportBatchID *string

	Rows []NewTransaction
}

// CreateBatchResult diz o que entrou.
type CreateBatchResult struct {
	// IDs está na MESMA ordem de Rows: é assim que a importação liga cada
	// linha do arquivo ao lançamento que ela virou.
	IDs []string
}

// CreateBatch grava o lote inteiro dentro de UMA transação.
//
// É aqui que o ORDINAL de deduplicação é atribuído (ADR-025b), e ele é
// atribuído DENTRO da transação de propósito: calculado antes, o número já
// pode estar ocupado quando o INSERT chegar. O valor é
// max(dedup_ordinal) da chave naquela casa — contando as linhas excluídas, que
// continuam ocupando o índice — mais um por linha do próprio lote.
//
// Com UMA exceção, e ela é uma garantia: em linha de chave NATURAL o ordinal
// nunca passa de 1. Repetição de tupla é fato da vida na chave derivada (dois
// cafés iguais no mesmo dia são dois gastos reais), mas não na natural — o
// identificador do emissor é único por transação, e uma segunda ocorrência da
// mesma chave natural é sempre a mesma transação. Ali o incremento não
// desempata nada: ele contorna o índice único e grava a duplicata.
//
// Resta uma janela que nenhuma leitura fecha: dois confirms simultâneos leem o
// mesmo máximo e calculam o mesmo ordinal. Quem decide é o índice único, e a
// segunda escrita volta como ErrDuplicateDedup — que IsBlocked reconhece e a
// importação traduz em "esta linha não entrou", NUNCA em 500. O lote inteiro
// volta atrás nesse caso (nada é gravado pela metade), e quem chama refaz a
// análise e o confirm: é a única saída portátil, porque num INSERT recusado o
// PostgreSQL aborta a transação inteira e só um SAVEPOINT por linha permitiria
// continuar — savepoint que o UnitOfWork não expõe, e cujo custo por linha
// seria pago em todo arquivo para atender a um caso que quase nunca acontece.
func (s *Service) CreateBatch(ctx context.Context, ator Actor, in CreateBatchInput) (CreateBatchResult, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return CreateBatchResult{}, ErrNotFound
	}
	if ator.UserID == "" {
		// created_by é NOT NULL e é a rastreabilidade de quem confirmou o lote.
		// Falha de ligação, não entrada do usuário — por isso não é um erro de
		// validação.
		return CreateBatchResult{}, errors.New("lançamento exige o usuário do token")
	}
	if err := validarLote(in); err != nil {
		return CreateBatchResult{}, err
	}

	resultado := CreateBatchResult{IDs: make([]string, 0, len(in.Rows))}
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		// Cada cache guarda o que já foi conferido NESTA transação: um extrato
		// de 400 linhas na mesma conta faz UMA consulta de conta, não 400. O
		// cache é local à chamada — nada sobrevive entre requisições, e por
		// isso ele não pode servir dado de outra casa por engano.
		contas := map[string]*account.Account{}
		categorias := map[string]*category.Category{}
		faturas := map[string]*cardstatement.Statement{}
		proximoOrdinal := map[string]int{}
		// grupoComFilhas guarda a resposta da §13 por categoria distinta do
		// lote: o confirm de um extrato inteiro apontando o mesmo grupo faz UMA
		// consulta de filhas, não uma por linha.
		grupoComFilhas := map[string]bool{}

		agora := s.clock()
		linhas := make([]Transaction, 0, len(in.Rows))
		ids := make([]string, 0, len(in.Rows))

		for i := range in.Rows {
			r := in.Rows[i]

			conta, err := s.contaCache(ctx, householdID, r.AccountID, contas)
			if err != nil {
				return err
			}
			if conta.ArchivedAt != nil {
				// Sem o nome da conta na mensagem: ela pode ser ecoada ao
				// cliente, e mensagem de erro é lugar de dizer O QUE houve, não
				// de repetir dado do usuário.
				return ErrAccountArchived
			}

			// categoriaID e faturaID guardam o id CANÔNICO (o que o banco
			// devolveu), em cópias próprias: apontar para dentro da entidade
			// do cache faria a linha a gravar compartilhar memória com um
			// objeto que outra iteração pode reusar.
			var categoriaID *string
			var faturaID *string

			if r.CategoryID != nil {
				cat, err := s.categoriaCache(ctx, householdID, *r.CategoryID, categorias)
				if err != nil {
					return err
				}
				if cat.ArchivedAt != nil {
					return ErrCategoryArchived
				}
				if !categoriaCombina(r.Kind, cat.Kind) {
					return ErrCategoryKindMismatch
				}
				// §13: vale para a categoria da decisão, para a sugerida e
				// para o defaultCategoryId — os três chegam aqui como
				// r.CategoryID, e a regra não precisa ser repetida no
				// importador.
				if err := s.recusarGrupoComFilhas(ctx, householdID, cat, grupoComFilhas); err != nil {
					return err
				}
				copia := cat.ID
				categoriaID = &copia
			}

			competencia := r.OccurredOn.YearMonth()
			if r.StatementID != nil {
				fatura, err := s.faturaCache(ctx, householdID, *r.StatementID, faturas)
				if err != nil {
					return err
				}
				// A comparação é CANÔNICA dos dois lados: `fatura.AccountID`
				// veio do banco e `conta.ID` também. Comparar com
				// `r.AccountID` — a string do cliente — faria a fatura certa
				// parecer de outra conta sempre que a caixa do id chegasse
				// trocada (o MySQL casa o `ByID`, o Go não casa as strings), e
				// a importação da fatura inteira morreria em 422.
				if fatura.AccountID != conta.ID {
					return ErrStatementMismatch
				}
				// ADR-023(b): dentro da fatura, a competência é a DA FATURA.
				// É isto que faz a compra de 28/01 aparecer no mês em que a
				// pessoa vai pagá-la, e é isto que mantém
				// SumByStatement coerente com o mês da listagem.
				competencia = fatura.CompetenceMonth
				copia := fatura.ID
				faturaID = &copia
			}

			ordinal, err := s.proximoOrdinal(ctx, householdID, r.DedupKey, proximoOrdinal)
			if err != nil {
				return err
			}
			if temChaveNatural(r) && ordinal > 1 {
				// DEFESA EM PROFUNDIDADE (ADR-025b): ordinal maior que 1 não
				// tem significado legítimo em chave NATURAL. O identificador do
				// emissor é único por transação, então uma segunda ocorrência
				// da mesma chave natural é sempre a MESMA transação — vinda de
				// um arquivo re-datado, de um período repartido ou de uma
				// classificação que não enxergou a gêmea.
				//
				// Incrementar o ordinal aqui seria contornar o índice único em
				// silêncio: (casa, chave, 2) está livre, o INSERT passa, e a
				// despesa aparece duas vezes no saldo sem aviso nenhum. Recusar
				// devolve ErrDuplicateDedup, que IsBlocked reconhece e a
				// importação traduz em "esta linha não entrou" — nunca 500.
				//
				// A mensagem não carrega a chave nem o identificador: ela pode
				// acabar em log, e nem um nem outro têm o que fazer lá.
				return fmt.Errorf("%w: chave natural já gravada", ErrDuplicateDedup)
			}

			novoID := s.ids()
			ids = append(ids, novoID)
			linhas = append(linhas, Transaction{
				ID:          novoID,
				HouseholdID: householdID,
				Kind:        r.Kind,
				// ⚠️ O QUE É GRAVADO É O ID QUE O BANCO DEVOLVEU, nunca o que
				// o chamador pediu — mesmo tendo os dois em mãos.
				//
				// A posse do id foi conferida em SQL (`WHERE id = ?`), cuja
				// semântica vem da COLLATION da coluna: `varchar(36)` sem
				// collation declarada é `utf8mb4_0900_ai_ci` no MySQL 8
				// (ignora caixa e acento) e `CI_AS` com padding ANSI no MSSQL
				// (ignora espaço à direita). A IDENTIDADE do mesmo id, depois,
				// é comparada em Go, byte a byte — o recorte crédito/débito do
				// relatório e o painel casam `account_id` contra as contas da
				// casa num mapa, e a listagem resolve o nome da conta assim.
				// Gravar a string do chamador deixaria uma linha que o SQL
				// encontra e que NENHUM mapa em Go encontra: a despesa de
				// cartão sumiria do quadro "Despesas no crédito" em silêncio e
				// para sempre.
				//
				// Vale para os três: conta, categoria e fatura. Em SQLite e
				// PostgreSQL o `=` é sensível a caixa e a espaço, então nada
				// disso reproduz ali — é por isso que a defesa mora no código,
				// e não num teste contra um dialeto só.
				AccountID:   conta.ID,
				CategoryID:  categoriaID,
				AmountCents: r.AmountCents,
				Description: r.Description,
				// DescriptionNorm é derivada SEMPRE, aqui, e nunca aceita de
				// fora: é ela que sustenta a busca insensível a acento e caixa
				// nos quatro dialetos (armadilha P2).
				DescriptionNorm: textnorm.Normalize(r.Description),
				OccurredOn:      r.OccurredOn,
				CompetenceMonth: competencia,
				TransferGroupID: r.TransferGroupID,
				StatementID:     faturaID,
				Source:          in.Source,
				ImportBatchID:   in.ImportBatchID,
				ExternalID:      r.ExternalID,
				DedupKey:        r.DedupKey,
				DedupOrdinal:    ordinal,
				CreatedBy:       ator.UserID,
				CreatedAt:       agora,
				UpdatedAt:       agora,
			})
		}

		// Reconferência dos PARES sobre os ids CANÔNICOS.
		//
		// validarLote já rodou validarPares — mas sobre as strings que o
		// chamador mandou, antes de qualquer consulta. Com a caixa trocada,
		// `"<UUID>"` e `"<uuid>"` são duas strings diferentes para o Go e a
		// MESMA conta para o MySQL: o par passaria pela forma e, depois da
		// canonização, viraria uma transferência com as duas pernas na mesma
		// conta — dinheiro saindo e entrando no mesmo lugar, dobrando a linha
		// no extrato. A canonização não pode ABRIR o que a validação fechava.
		if err := validarParesCanonicos(linhas); err != nil {
			return err
		}

		if err := s.repo.CreateBatch(ctx, householdID, linhas); err != nil {
			// O erro tipado é preservado na cadeia: é por ele que a importação
			// distingue "linha bloqueada" de falha de verdade (IsBlocked).
			return fmt.Errorf("gravando lote de lançamentos: %w", err)
		}
		resultado.IDs = ids
		return nil
	})
	if err != nil {
		return CreateBatchResult{}, err
	}
	return resultado, nil
}

// LinkImport grava, numa perna de transferência JÁ EXISTENTE, a identidade de
// deduplicação e o lote de uma linha importada — a ação `link` da importação
// (spec 0005 §4.2, ADR-026f). Não cria lançamento, não altera valor, data,
// conta nem categoria: depois dela, reimportar o mesmo arquivo cai em
// `duplicado_exato`, e é só isso que ela existe para conseguir.
//
// É chamada pelo importador DENTRO da transação do lote (o UnitOfWork é
// reentrante), e a reconferência abaixo acontece nessa mesma transação — é a
// defesa contra BOLA do vetor novo desta entrega (plano E2c §4.5, S1): a perna
// vem do staging, e o staging foi gravado pela análise sob o household_id do
// token, mas o commit não CONFIA nisso. Ele lê a perna de novo, pela casa do
// token, e recusa o que não bate:
//   - não existe na casa → ErrLinkTargetMissing (o lote inteiro falha);
//   - excluída entre a análise e o confirm → ErrLinkTargetDeleted (só a
//     linha é bloqueada);
//   - de outra conta que não a do lote, não é transferência ou não tem grupo
//     → ErrLinkTargetInvalid (o lote inteiro falha).
//
// O ordinal segue a MESMA regra do CreateBatch (ADR-025b): max(dedup_ordinal)
// da chave na casa, contando as excluídas, mais um — e em chave NATURAL o
// ordinal nunca passa de 1, porque a segunda ocorrência de um identificador do
// emissor é sempre a mesma transação. Aí é ErrDuplicateDedup, que IsBlocked
// reconhece. O UPDATE do repositório leva casa, id, CONTA DO LOTE e
// deleted_at IS NULL no WHERE: é a reconferência feita também no banco.
func (s *Service) LinkImport(ctx context.Context, ator Actor, in LinkImportInput) error {
	householdID := ator.HouseholdID
	if householdID == "" {
		return ErrNotFound
	}
	if ator.UserID == "" {
		// A auditoria do vínculo precisa de quem vinculou. Falha de ligação,
		// não entrada do usuário — por isso não é erro de validação.
		return errors.New("vínculo de importação exige o usuário do token")
	}
	if err := validarVinculo(in); err != nil {
		return err
	}

	return s.tx.Do(ctx, func(ctx context.Context) error {
		perna, err := s.repo.ByIDIncludingDeleted(ctx, householdID, in.TransactionID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("reconferindo a perna do vínculo: %w", ErrLinkTargetMissing)
			}
			return fmt.Errorf("reconferindo a perna do vínculo: %w", err)
		}
		if perna.DeletedAt != nil {
			return ErrLinkTargetDeleted
		}
		if perna.AccountID != in.AccountID || !perna.IsTransfer() ||
			perna.TransferGroupID == nil || *perna.TransferGroupID == "" {
			// Sem dizer QUAL das três condições falhou: o erro pode acabar em
			// log, e a distinção só interessaria a quem adulterou o staging.
			return ErrLinkTargetInvalid
		}

		maximo, err := s.repo.MaxDedupOrdinal(ctx, householdID, in.DedupKey)
		if err != nil {
			return fmt.Errorf("buscando ordinal de deduplicação: %w", err)
		}
		ordinal := maximo + 1
		if in.ExternalID != nil && *in.ExternalID != "" && ordinal > 1 {
			// Mesma defesa em profundidade do CreateBatch (ADR-025b): em chave
			// natural, ordinal 2 seria contornar o índice único em silêncio.
			return fmt.Errorf("%w: chave natural já gravada", ErrDuplicateDedup)
		}

		err = s.repo.LinkImport(ctx, householdID, perna.ID, LinkFields{
			AccountID:     in.AccountID,
			ExternalID:    in.ExternalID,
			DedupKey:      in.DedupKey,
			DedupOrdinal:  ordinal,
			ImportBatchID: in.ImportBatchID,
			UpdatedAt:     s.clock(),
		})
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				// Acabamos de ler a perna viva, nesta transação, nesta casa e
				// nesta conta; zero linhas afetadas é anomalia, não estado
				// previsto — e anomalia derruba o lote, nunca fica parcial.
				return fmt.Errorf("perna sumiu durante o vínculo: %w", ErrLinkTargetMissing)
			}
			// ErrDuplicateDedup atravessa preservado: é por ele que a
			// importação distingue "linha bloqueada" de falha (IsBlocked).
			return fmt.Errorf("vinculando importação à perna: %w", err)
		}
		return s.registrar(ctx, ator, audit.ActionTransactionImportLinked, perna.ID)
	})
}

// validarVinculo confere a FORMA da entrada do LinkImport, antes de qualquer
// consulta — as mesmas regras de forma que validarLinha aplica à chave e ao
// identificador externo, porque as colunas são as mesmas.
func validarVinculo(in LinkImportInput) error {
	if in.TransactionID == "" {
		return fmt.Errorf("perna ausente: %w", ErrLinkTargetMissing)
	}
	if in.AccountID == "" {
		return fmt.Errorf("conta do lote ausente: %w", ErrNotFound)
	}
	if in.ImportBatchID == "" {
		return fmt.Errorf("%w: vínculo exige o lote de origem", ErrInvalidSource)
	}
	if !dedupKeyValida(in.DedupKey) {
		return ErrInvalidDedupKey
	}
	if in.ExternalID != nil && len(*in.ExternalID) > MaxExternalIDBytes {
		return ErrInvalidExternalID
	}
	return nil
}

// --- apoio ----------------------------------------------------------------

// paginaBruta executa a consulta paginada e devolve as LINHAS, junto do cursor
// da próxima página (nulo quando acabou).
//
// Ela não rotula nada: quem chama decide de onde vêm os nomes. A separação
// existe por causa do critério de UMA leitura de categorias por requisição —
// em GET /transactions os rótulos já foram carregados antes da consulta,
// porque o resumo precisa da MESMA lista de categorias (ADR-029d).
func (s *Service) paginaBruta(ctx context.Context, householdID string, filtro ListFilter, limit int) ([]Transaction, *string, error) {
	tamanho := tamanhoDaPagina(limit)

	// Pedimos uma linha a mais para saber se existe próxima página sem pagar um
	// COUNT — que varreria o mês inteiro a cada página só para devolver um
	// booleano.
	filtro.Limit = tamanho + 1

	linhas, err := s.repo.List(ctx, householdID, filtro)
	if err != nil {
		return nil, nil, fmt.Errorf("listando lançamentos: %w", err)
	}

	var proximo *string
	if len(linhas) > tamanho {
		linhas = linhas[:tamanho]
		texto := EncodeCursor(Cursor{
			OccurredOn: linhas[len(linhas)-1].OccurredOn,
			ID:         linhas[len(linhas)-1].ID,
		})
		if texto != "" {
			proximo = &texto
		}
	}
	return linhas, proximo, nil
}

// pagina é paginaBruta com os rótulos carregados SOB DEMANDA — só quando há
// linha para rotular. É a forma usada por quem não precisa do conjunto de
// categorias marcadas (ListByStatement).
func (s *Service) pagina(ctx context.Context, householdID string, filtro ListFilter, limit int) ([]View, *string, error) {
	linhas, proximo, err := s.paginaBruta(ctx, householdID, filtro, limit)
	if err != nil {
		return nil, nil, err
	}
	if len(linhas) == 0 {
		return []View{}, proximo, nil
	}
	rot, err := s.rotulos(ctx, householdID)
	if err != nil {
		return nil, nil, err
	}
	return montarViews(linhas, rot), proximo, nil
}

// montarViews converte as linhas em DTO com os rótulos JÁ carregados.
func montarViews(linhas []Transaction, rot etiquetas) []View {
	itens := make([]View, 0, len(linhas))
	for i := range linhas {
		itens = append(itens, toView(linhas[i], rot.contas, rot.categorias))
	}
	return itens
}

// tamanhoDaPagina aplica default e teto.
//
// O teto é MaxPageSize-1 porque a consulta pede sempre uma linha a mais: pedir
// MaxPageSize+1 faria o repositório cortar de volta para MaxPageSize, e a
// última página do mês pareceria ter sempre uma próxima que não existe.
func tamanhoDaPagina(limit int) int {
	switch {
	case limit <= 0:
		return DefaultPageSize
	case limit >= MaxPageSize:
		return MaxPageSize - 1
	default:
		return limit
	}
}

// etiquetas é o que UMA leitura de contas e categorias da casa produz.
//
// É um valor com nome, e não três retornos soltos, porque o terceiro campo
// nasceu de uma exigência de custo: o conjunto de categorias de investimento
// sai da MESMA lista que rotula a página (ADR-029d). Separá-los convidaria a
// segunda leitura da taxonomia por requisição — e duas leituras podem
// discordar entre si no instante em que alguém troca a natureza de uma
// categoria no meio do caminho.
type etiquetas struct {
	// contas e categorias são id -> nome, para o DTO.
	contas     map[string]string
	categorias map[string]string

	// marcadas são os ids das categorias de natureza `investment`/`redemption`
	// da casa, ARQUIVADAS INCLUÍDAS: arquivar não desfaz a marcação do passado
	// (PLANOS.md §4.4). Vazio quer dizer "esta casa não marca nada", e é o caso
	// de toda casa no dia da entrega — aí o conjunto não vira filtro nenhum e o
	// SQL do resumo volta a ser o de antes da E7 (ADR-029f).
	marcadas []string
}

// MarcadaComoInvestimento responde à ÚNICA pergunta que esta entrega faz sobre
// uma categoria (ADR-029d): a natureza dela marca o lançamento como aporte ou
// resgate?
//
// Mora aqui, exportada, porque `internal/report` precisa da MESMA resposta
// para descartar as linhas marcadas do relatório por categoria. Duas cópias do
// predicado divergiriam, e aí a mesma despesa sairia de `expenseCents` e
// continuaria no relatório de despesas — dois números para o mesmo dinheiro.
//
// O FLUXO (aporte ou resgate) não se decide aqui: ele vem do `kind` do
// LANÇAMENTO, nunca da natureza da categoria. Aporte é o que SAI da conta e
// resgate é o que ENTRA, que é o que o extrato diz.
func MarcadaComoInvestimento(categoryKind string) bool {
	return categoryKind == category.KindInvestment || categoryKind == category.KindRedemption
}

// rotulos carrega, em duas consultas, os nomes de conta e de categoria da casa
// — e, da MESMA lista de categorias, o conjunto das marcadas como
// investimento.
//
// Duas consultas para a página inteira, e não duas por linha: com teto de 50
// contas e 200 categorias por casa, o mapa inteiro é menor do que a página que
// ele rotula. A alternativa (JOIN no repositório) espalharia a regra de escopo
// por mais SQL, que é exatamente onde o household_id costuma ser esquecido.
//
// includeArchived é true: um lançamento antigo pode apontar para conta ou
// categoria já arquivada, e nesse caso o nome continua sendo a informação
// certa a mostrar — e, no caso da categoria marcada, a natureza continua
// valendo para o mês passado.
func (s *Service) rotulos(ctx context.Context, householdID string) (etiquetas, error) {
	linhasConta, err := s.accounts.List(ctx, householdID, true)
	if err != nil {
		return etiquetas{}, fmt.Errorf("carregando nomes de conta: %w", err)
	}
	out := etiquetas{contas: make(map[string]string, len(linhasConta))}
	for _, c := range linhasConta {
		if c.HouseholdID != householdID {
			continue
		}
		out.contas[c.ID] = c.Name
	}

	linhasCategoria, err := s.categories.List(ctx, householdID, true)
	if err != nil {
		return etiquetas{}, fmt.Errorf("carregando nomes de categoria: %w", err)
	}
	out.categorias = make(map[string]string, len(linhasCategoria))
	for _, c := range linhasCategoria {
		// A CASA é reconferida em Go mesmo com o repositório já filtrando —
		// a mesma defesa em profundidade de recusarGrupoComFilhas. Aqui ela
		// pesa mais do que lá: esta lista não vira só rótulo, ela vira o
		// conjunto que decide o que SAI de expenseCents. Uma implementação de
		// Categories que esquecesse o filtro mandaria id alheio para dentro
		// de um IN (...) — inofensivo hoje, porque a consulta do resumo é
		// escopada por casa, mas é uma linha para nunca depender disso.
		if c.HouseholdID != householdID {
			continue
		}
		out.categorias[c.ID] = c.Name
		if MarcadaComoInvestimento(c.Kind) {
			out.marcadas = append(out.marcadas, c.ID)
		}
	}
	return out, nil
}

// pernasVivas devolve os ids a excluir: o próprio lançamento, ou todas as
// pernas vivas da transferência a que ele pertence.
func (s *Service) pernasVivas(ctx context.Context, householdID string, t *Transaction) ([]string, error) {
	if !t.IsTransfer() || t.TransferGroupID == nil || *t.TransferGroupID == "" {
		return []string{t.ID}, nil
	}

	pernas, err := s.repo.ByTransferGroup(ctx, householdID, *t.TransferGroupID)
	if err != nil {
		return nil, fmt.Errorf("carregando par da transferência: %w", err)
	}

	ids := make([]string, 0, len(pernas))
	for i := range pernas {
		if pernas[i].DeletedAt == nil {
			ids = append(ids, pernas[i].ID)
		}
	}
	if len(ids) == 0 {
		// Nunca deveria acontecer (o lançamento que abriu a operação está vivo),
		// mas se acontecer é melhor excluir o que temos em mãos do que devolver
		// sucesso sem ter excluído nada.
		return []string{t.ID}, nil
	}
	return ids, nil
}

// contaDaCasa confere que a conta é da casa e traduz o erro para o vocabulário
// deste pacote.
//
// A tradução importa: conta de outra casa tem de virar o MESMO ErrNotFound de
// lançamento inexistente, para que a resposta seja byte a byte igual e não
// confirme a existência do recurso alheio (S1).
func (s *Service) contaDaCasa(ctx context.Context, householdID, accountID string) (*account.Account, error) {
	c, err := s.accounts.ByID(ctx, householdID, accountID)
	if err != nil {
		if errors.Is(err, account.ErrNotFound) {
			return nil, fmt.Errorf("conta do lançamento: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("buscando conta do lançamento: %w", err)
	}
	return c, nil
}

// recorteDeCategoria traduz o `categoryId` do cliente no CONJUNTO de ids que
// a consulta usa: a categoria pedida mais as filhas diretas dela.
//
// Três coisas acontecem aqui, e nenhuma pode faltar:
//
//  1. A categoria é conferida como sendo DA CASA. Sem isso, pedir a categoria
//     da vizinha devolveria lista vazia — que não vaza dado, mas também não é
//     a resposta certa: id que não é meu é 404, igual a id que não existe
//     (S1). É a mesma porta por onde o filtro de conta passa.
//  2. O que sobe é o id CANÔNICO do banco, nunca a grafia recebida. No MySQL 8
//     o `ByID` casa por collation (`utf8mb4_0900_ai_ci` ignora caixa), então o
//     filtro pode ser aceito com uma grafia que um `IN (...)` sobre a coluna
//     não precisa reconhecer do mesmo jeito em todo dialeto.
//  3. O grupo vira `{ele} ∪ {filhas}`, arquivadas incluídas. Arquivar não
//     desfaz o passado, e a linha do grupo no relatório soma exatamente isto
//     — o atalho de uma tela para a outra tem de abrir no mesmo dinheiro.
//     Folha não tem filha e a árvore tem dois níveis, então a consulta extra
//     é barata e não recursa.
//
// Vazio entra e vazio sai: "sem filtro" é o caso comum desta rota.
func (s *Service) recorteDeCategoria(ctx context.Context, householdID, categoryID string) ([]string, error) {
	if categoryID == "" {
		return nil, nil
	}

	cat, err := s.categoriaDaCasa(ctx, householdID, categoryID)
	if err != nil {
		return nil, err
	}

	ids := []string{cat.ID}
	// Só grupo tem filha. Perguntar pelas filhas de uma folha seria uma
	// consulta garantidamente vazia em toda requisição do atalho — e a
	// maioria dos cliques do relatório cai numa subcategoria.
	if cat.IsGroup() {
		filhas, err := s.categories.Children(ctx, householdID, cat.ID)
		if err != nil {
			return nil, fmt.Errorf("carregando subcategorias do filtro: %w", err)
		}
		for i := range filhas {
			// A casa é reconferida em Go mesmo com o repositório já
			// filtrando: esta lista não vira rótulo, vira o conjunto de um
			// `IN (...)`. Mesma defesa em profundidade de `rotulos`.
			if filhas[i].HouseholdID != householdID {
				continue
			}
			ids = append(ids, filhas[i].ID)
		}
	}
	return ids, nil
}

func (s *Service) categoriaDaCasa(ctx context.Context, householdID, categoryID string) (*category.Category, error) {
	c, err := s.categories.ByID(ctx, householdID, categoryID)
	if err != nil {
		if errors.Is(err, category.ErrNotFound) {
			return nil, fmt.Errorf("categoria do lançamento: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("buscando categoria do lançamento: %w", err)
	}
	return c, nil
}

func (s *Service) faturaDaCasa(ctx context.Context, householdID, statementID string) (*cardstatement.Statement, error) {
	f, err := s.statements.ByID(ctx, householdID, statementID)
	if err != nil {
		if errors.Is(err, cardstatement.ErrNotFound) {
			return nil, fmt.Errorf("fatura do lançamento: %w", ErrNotFound)
		}
		return nil, fmt.Errorf("buscando fatura do lançamento: %w", err)
	}
	return f, nil
}

func (s *Service) contaCache(ctx context.Context, householdID, accountID string, cache map[string]*account.Account) (*account.Account, error) {
	if c, ok := cache[accountID]; ok {
		return c, nil
	}
	c, err := s.contaDaCasa(ctx, householdID, accountID)
	if err != nil {
		return nil, err
	}
	cache[accountID] = c
	return c, nil
}

func (s *Service) categoriaCache(ctx context.Context, householdID, categoryID string, cache map[string]*category.Category) (*category.Category, error) {
	if c, ok := cache[categoryID]; ok {
		return c, nil
	}
	c, err := s.categoriaDaCasa(ctx, householdID, categoryID)
	if err != nil {
		return nil, err
	}
	cache[categoryID] = c
	return c, nil
}

func (s *Service) faturaCache(ctx context.Context, householdID, statementID string, cache map[string]*cardstatement.Statement) (*cardstatement.Statement, error) {
	if f, ok := cache[statementID]; ok {
		return f, nil
	}
	f, err := s.faturaDaCasa(ctx, householdID, statementID)
	if err != nil {
		return nil, err
	}
	cache[statementID] = f
	return f, nil
}

// temChaveNatural informa se a linha foi identificada PELO DOCUMENTO — é o
// Identificador que o emissor imprime, e é ele que torna a chave dela natural
// em vez de derivada (§4.4 da spec 0004).
//
// A pergunta é feita sobre o ExternalID, e não sobre a forma da chave, porque a
// chave é um hash: de fora dela não dá para saber que tipo ela é, e inventar um
// prefixo legível só para descobrir isso enfraqueceria a chave sem necessidade.
func temChaveNatural(r NewTransaction) bool {
	return r.ExternalID != nil && *r.ExternalID != ""
}

// proximoOrdinal devolve o ordinal que a linha vai ocupar.
//
// A primeira linha de cada chave consulta o banco (max + 1); as seguintes só
// incrementam em memória. Duas compras idênticas no mesmo arquivo entram como
// #N+1 e #N+2 — que é exatamente o caso que a chave derivada sozinha apagaria,
// e apagar um gasto real é pior do que duplicá-lo (ADR-025).
func (s *Service) proximoOrdinal(ctx context.Context, householdID, dedupKey string, cache map[string]int) (int, error) {
	if _, ok := cache[dedupKey]; !ok {
		maximo, err := s.repo.MaxDedupOrdinal(ctx, householdID, dedupKey)
		if err != nil {
			return 0, fmt.Errorf("buscando ordinal de deduplicação: %w", err)
		}
		cache[dedupKey] = maximo
	}
	cache[dedupKey]++
	return cache[dedupKey], nil
}

// registrar grava o rastro da escrita. Chamada SEMPRE de dentro da transação:
// auditoria que cai fora dela pode sobreviver a um rollback, e aí o rastro
// descreve algo que não aconteceu.
func (s *Service) registrar(ctx context.Context, ator Actor, acao, entityID string) error {
	return s.registrarEm(ctx, ator, acao, audit.EntityTransaction, entityID)
}

// registrarEm é registrar com a entidade explícita — para a única ação deste
// pacote cuja entidade não é o lançamento: o auto-categorize audita o MÊS
// (audit.EntityTransactionMonth), porque toca N lançamentos numa execução só.
func (s *Service) registrarEm(ctx context.Context, ator Actor, acao, entidade, entityID string) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, AuditParams{
		Action:      acao,
		Entity:      entidade,
		EntityID:    entityID,
		UserID:      ator.UserID,
		HouseholdID: ator.HouseholdID,
		IP:          ator.IP,
	})
}

// recusarGrupoComFilhas devolve ErrCategoryIsParentGroup quando a categoria é
// um GRUPO com ao menos uma subcategoria ativa (spec 0005 §13).
//
// Duas economias deliberadas:
//   - FOLHA não consulta nada. A árvore tem dois níveis (category.MaxDepth), e
//     quem tem pai não tem filha — o caso comum do PATCH e da importação sai
//     daqui sem tocar no banco.
//   - o cache é por CATEGORIA distinta da escrita, não por linha. Um confirm de
//     400 linhas apontando o mesmo grupo faz UMA consulta, e não 400 (o mesmo
//     desenho dos caches de conta, categoria e fatura do CreateBatch). Ele é
//     local à chamada: nada sobrevive à requisição, e por isso não tem como
//     servir a resposta de outra casa.
//
// cache nil é aceito (o caminho de uma linha só não precisa alocar mapa).
// Arquivada NÃO conta como filha: um grupo cujas filhas foram todas arquivadas
// volta a ser um destino legítimo, que é o que a §12 diz do lado das
// palavras-chave — as duas regras precisam concordar.
func (s *Service) recusarGrupoComFilhas(ctx context.Context, householdID string, cat *category.Category, cache map[string]bool) error {
	if !cat.IsGroup() {
		return nil
	}
	if cache != nil {
		if tem, ok := cache[cat.ID]; ok {
			if tem {
				return ErrCategoryIsParentGroup
			}
			return nil
		}
	}

	filhas, err := s.categories.Children(ctx, householdID, cat.ID)
	if err != nil {
		return fmt.Errorf("conferindo subcategorias da categoria: %w", err)
	}
	tem := false
	for i := range filhas {
		// A CASA e o DeletedAt são reconferidos em Go mesmo com o repositório
		// já filtrando os dois: é a mesma defesa em profundidade do
		// classificador, custa nada e não depende de a implementação da fonte
		// continuar igual. Filha de outra casa aqui bloquearia uma escrita
		// legítima desta — e uma recusa vinda de dado alheio é tão errada
		// quanto uma permissão.
		if filhas[i].HouseholdID != householdID {
			continue
		}
		if filhas[i].ArchivedAt == nil && filhas[i].DeletedAt == nil {
			tem = true
			break
		}
	}
	if cache != nil {
		cache[cat.ID] = tem
	}
	if tem {
		return ErrCategoryIsParentGroup
	}
	return nil
}

// categoriaCombina confere a natureza da categoria contra o tipo do lançamento.
//
// DELEGA para category.AceitaLancamento, que é a única fonte da verdade da
// regra (ADR-029b). Esta função existiu com a regra escrita à mão, e a cópia
// gêmea no importador (categoriaCombinaComALinha) é a que teria ficado para
// trás quando `investment` e `redemption` entraram na allowlist — seria por
// ela que uma despesa ganharia categoria de resgate. O corpo aqui é uma linha
// de propósito: se um dia precisar de um `case`, é sinal de que a regra
// voltou a ter duas versões.
//
// Despesa aceita `expense` ou `investment`; receita aceita `income` ou
// `redemption`; transferência não aceita nenhuma — e nem chega aqui, porque
// categoria em transferência já foi recusada na validação de forma.
func categoriaCombina(kind, categoryKind string) bool {
	return category.AceitaLancamento(kind, categoryKind)
}
