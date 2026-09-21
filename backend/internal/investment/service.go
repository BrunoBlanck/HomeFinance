package investment

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Service responde a tela de investimentos e executa o detect.
type Service struct {
	ledger     Ledger
	categories Categories
	accounts   Accounts
	classifier Classifier
	tx         Transactor
	audit      Auditor
	lg         *slog.Logger

	clock func() time.Time

	// planTimeout é o prazo da fase de CÁLCULO do detect. Campo, e não a
	// constante direta, só para o teste conseguir encurtá-lo — produção nunca
	// o ajusta.
	planTimeout time.Duration
}

// Option configura o Service.
type Option func(*Service)

// WithAudit liga o rastro de auditoria. Sem ele o serviço funciona, o que é
// deliberado: os testes de unidade não precisam de auditoria para exercitar
// regra de negócio. Em produção ele é ligado em cmd/api/main.go.
func WithAudit(a Auditor) Option { return func(s *Service) { s.audit = a } }

// WithClock injeta o relógio (teste).
func WithClock(c func() time.Time) Option { return func(s *Service) { s.clock = c } }

// WithPlanTimeout encurta o prazo da fase de cálculo (teste). Valor menor ou
// igual a zero mantém transaction.PlanTimeout.
//
// A opção só ENCURTA: o valor é limitado por transaction.PlanTimeout, igual ao
// WithPlanTimeout do pacote transaction (achado A7 da revisão daquela rota). Um
// prazo de teste que conseguisse ESTICAR o de produção seria um prazo que
// depende de ninguém chamá-lo errado — e o prazo existe justamente porque o
// WriteTimeout do http.Server não cancela esta goroutine: sem teto, uma
// montagem equivocada deixaria o servidor queimando CPU por uma resposta que
// ninguém vai ler.
func WithPlanTimeout(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			d = min(d, transaction.PlanTimeout)
		}
		s.planTimeout = d
	}
}

// NewService monta o serviço.
//
// As cinco dependências obrigatórias são posicionais de propósito: esquecer de
// ligar o classificador vira erro de COMPILAÇÃO, e não um detect que não marca
// nada em produção. Só o auditor é opcional.
func NewService(ledger Ledger, categories Categories, accounts Accounts, classifier Classifier, tx Transactor, lg *slog.Logger, opts ...Option) *Service {
	if lg == nil {
		lg = slog.Default()
	}
	s := &Service{
		ledger:      ledger,
		categories:  categories,
		accounts:    accounts,
		classifier:  classifier,
		tx:          tx,
		lg:          lg,
		clock:       time.Now,
		planTimeout: transaction.PlanTimeout,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// taxonomia é a árvore de categorias da casa já partida pelos dois conjuntos
// que esta feature usa. Sai de UMA leitura (ADR-029d).
type taxonomia struct {
	// porID inclui TODAS as categorias da casa, arquivadas incluídas — é dela
	// que saem os nomes exibidos e a natureza da categoria ATUAL de uma linha.
	porID map[string]category.Category

	// marcadas são os ids das categorias de natureza investment/redemption,
	// ORDENADOS. Arquivadas entram: arquivar não desfaz a marcação do passado
	// (PLANOS.md §4.4).
	marcadas []string

	// comuns são os ids das categorias de natureza income/expense, ORDENADOS.
	// É a allowlist do `WHERE` do overwriteCategorized — e é por ela estar no
	// WHERE, e não no Go, que a troca nunca desfaz uma marcação de
	// investimento (ADR-029h).
	comuns []string
}

// carregarTaxonomia lê a taxonomia da casa do TOKEN e a parte nos dois
// conjuntos. Arquivadas INCLUÍDAS nos dois — ver os comentários dos campos.
//
// A ordenação dos conjuntos é por id e existe para o rastro de consultas ser
// determinístico: o resultado não depende dela, mas um teste que passa por
// acaso não é teste.
func (s *Service) carregarTaxonomia(ctx context.Context, householdID string) (taxonomia, error) {
	cats, err := s.categories.List(ctx, householdID, true)
	if err != nil {
		return taxonomia{}, fmt.Errorf("carregando categorias da casa: %w", err)
	}
	t := taxonomia{porID: make(map[string]category.Category, len(cats))}
	marcadas := make(map[string]struct{}, len(cats))
	comuns := make(map[string]struct{}, len(cats))
	for i := range cats {
		c := cats[i]
		// Defesa em profundidade: o repositório já filtra por casa, e a
		// reconferência aqui custa nada e não depende de a implementação da
		// fonte continuar igual. Categoria de outra casa jamais entra num
		// conjunto que vira `IN (...)` ou allowlist de UPDATE.
		if c.HouseholdID != householdID || c.ID == "" {
			continue
		}
		t.porID[c.ID] = c
		switch c.Kind {
		case category.KindInvestment, category.KindRedemption:
			marcadas[c.ID] = struct{}{}
		case category.KindIncome, category.KindExpense:
			comuns[c.ID] = struct{}{}
		}
		// Natureza fora da allowlist não existe (category.ValidKind na borda);
		// se existisse, ficar de fora dos DOIS conjuntos é o comportamento
		// seguro — por isso este switch é fechado e não tem default.
	}

	// A natureza de INVESTIMENTO vence o empate, e os conjuntos são uma
	// PARTIÇÃO, não duas listas montadas em paralelo.
	//
	// Isto não é teoria: sem a subtração, um id que a fonte devolvesse DUAS
	// vezes com naturezas diferentes entraria nas duas listas — e estar em
	// `comuns` é estar na allowlist do `WHERE`, isto é, ser autorizado a ter a
	// categoria substituída. Uma categoria de investimento na allowlist é
	// exatamente o que o ADR-029(h) promete que não acontece: a troca nunca
	// desfaz uma marcação.
	//
	// O índice único de `categories` não produz id repetido hoje, e é
	// justamente por isso que a guarda é barata: ela custa uma varredura de
	// ≤ 200 itens e fecha a única forma pela qual a promessa dependeria de uma
	// propriedade da FONTE em vez de uma propriedade deste código.
	for id := range marcadas {
		delete(comuns, id)
	}
	t.marcadas = ordenados(marcadas)
	t.comuns = ordenados(comuns)
	return t, nil
}

// ordenados devolve as chaves do conjunto em ordem estável. Ordem determinística
// porque o conjunto vira `IN (...)`: o resultado não depende dela, mas o rastro
// de consultas sim, e teste que passa por acaso não é teste.
func ordenados(conjunto map[string]struct{}) []string {
	if len(conjunto) == 0 {
		return nil
	}
	out := make([]string, 0, len(conjunto))
	for id := range conjunto {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Overview responde GET /api/v1/investments.
//
// Três números e uma lista, com DUAS idas ao banco de lançamentos:
// SumInvestmentsByMonth (que resolve mês, ano-até-o-mês e a série de 12 em ≤ 24
// linhas) e ListByCategories (a página do mês). A janela do ano está sempre
// contida na dos 12 meses, e é isso que permite uma consulta em vez de doze —
// ou de duas, que poderiam discordar entre si sob escrita concorrente.
//
// Casa SEM categoria de investimento — o caso de todas as casas no dia da
// entrega — responde zeros, `items: []` e série de 12 meses zerada SEM tocar
// no repositório de lançamentos (ADR-029f). O curto-circuito é em Go porque
// `IN ()` é erro de sintaxe em três dos quatro dialetos e `1=0` no outro, e a
// diferença entre "não há" e "a consulta quebrou" não pode depender do banco.
func (s *Service) Overview(ctx context.Context, ator Actor, in OverviewInput) (OverviewView, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return OverviewView{}, ErrUnauthenticated
	}

	// Ordem das guardas: casa → forma do mês → forma do cursor → só então o
	// banco. Nada é consultado com entrada não validada.
	d, err := transaction.ParseMonth(in.Month)
	if err != nil {
		return OverviewView{}, err
	}
	alvo := mes{ano: d.Year(), num: d.Month()}

	cursor, err := transaction.ParseCursor(in.Cursor)
	if err != nil {
		return OverviewView{}, err
	}

	tax, err := s.carregarTaxonomia(ctx, householdID)
	if err != nil {
		return OverviewView{}, err
	}

	// A série vem montada e zerada ANTES do curto-circuito: os 12 itens são
	// promessa de contrato (`minItems: 12`), e não consequência de ter havido
	// movimento.
	view := OverviewView{
		Month:  in.Month,
		Series: serieZerada(alvo),
		Items:  []ItemView{},
	}
	if len(tax.marcadas) == 0 {
		return view, nil
	}

	primeiro, ultimo := janelaDaSerie(alvo)
	linhas, err := s.ledger.SumInvestmentsByMonth(ctx, householdID, tax.marcadas, primeiro.String(), ultimo.String())
	if err != nil {
		return OverviewView{}, fmt.Errorf("somando investimentos por mês: %w", err)
	}

	porMes, err := agrupar(linhas)
	if err != nil {
		// Falha FECHADA: nenhum número inventado na tela, e a contagem (nunca
		// os valores) no log (ADR-029 j.1).
		s.lg.ErrorContext(ctx, "agregação de investimentos fora da faixa representável",
			slog.String("month", in.Month),
			slog.Int("linhas", len(linhas)),
		)
		return OverviewView{}, err
	}

	// Série e ano-até-o-mês saem das MESMAS linhas, numa passagem só. O
	// "Y-01 <= chave" é comparação de TEXTO, e ela é cronológica porque
	// "AAAA-MM" tem largura fixa.
	inicioDoAno := primeiroMesDoAno(alvo).String()
	for i := range view.Series {
		chave := primeiro.mais(i).String()
		t := porMes[chave]
		view.Series[i] = SeriesPointView{
			Month:              chave,
			ContributionsCents: t.ContributionsCents,
			RedemptionsCents:   t.RedemptionsCents,
		}
		if chave >= inicioDoAno {
			acumulado, ok := somarTotais(view.YearToDate, t)
			if !ok {
				return OverviewView{}, errTotalsOutOfRange
			}
			view.YearToDate = acumulado
		}
	}
	view.Monthly = porMes[ultimo.String()]

	itens, proximo, err := s.pagina(ctx, householdID, tax, in, cursor)
	if err != nil {
		return OverviewView{}, err
	}
	view.Items = itens
	view.NextCursor = proximo
	return view, nil
}

// serieZerada devolve os SeriesMonths meses da janela, em ordem cronológica
// CRESCENTE e terminando no mês pedido, todos com zeros.
func serieZerada(alvo mes) []SeriesPointView {
	primeiro, _ := janelaDaSerie(alvo)
	out := make([]SeriesPointView, 0, SeriesMonths)
	for i := range SeriesMonths {
		out = append(out, SeriesPointView{Month: primeiro.mais(i).String()})
	}
	return out
}

// agrupar indexa as linhas da agregação por mês, ACUMULANDO em vez de
// sobrescrever.
//
// Acumular é defesa em profundidade contra uma linha repetida: com
// sobrescrita, um mês que voltasse duas vezes perderia dinheiro em silêncio —
// e "sumiu dinheiro" é o defeito que ninguém percebe até o fechamento do mês.
//
// Cada parcela é conferida: negativo ou soma que não cabe em int64 é falha
// FECHADA (ADR-029 j.1). `amount_cents >= 0` é invariante do caminho de
// escrita e NÃO do schema — não há CHECK —, então ela é verificada aqui, e não
// confiada.
func agrupar(linhas []transaction.InvestmentMonthTotals) (map[string]TotalsView, error) {
	porMes := make(map[string]TotalsView, len(linhas))
	for _, r := range linhas {
		soma, ok := somarTotais(porMes[r.Month], TotalsView{
			ContributionsCents: r.ContributionsCents,
			ContributionCount:  r.ContributionCount,
			RedemptionsCents:   r.RedemptionsCents,
			RedemptionCount:    r.RedemptionCount,
		})
		if !ok {
			return nil, errTotalsOutOfRange
		}
		porMes[r.Month] = soma
	}
	return porMes, nil
}

// somarTotais soma os quatro números de duas janelas, conferindo os quatro.
//
// A CONTAGEM passa pela mesma conferência dos centavos de propósito: na
// prática COUNT(*) nunca estoura int64 nem vem negativo, e é a mesma
// disciplina aplicada ao mesmo tipo de número, sem exceção que alguém precise
// lembrar de justificar depois.
func somarTotais(a, b TotalsView) (TotalsView, bool) {
	var out TotalsView
	var ok bool
	if out.ContributionsCents, ok = somaSegura(a.ContributionsCents, b.ContributionsCents); !ok {
		return TotalsView{}, false
	}
	if out.ContributionCount, ok = somaSegura(a.ContributionCount, b.ContributionCount); !ok {
		return TotalsView{}, false
	}
	if out.RedemptionsCents, ok = somaSegura(a.RedemptionsCents, b.RedemptionsCents); !ok {
		return TotalsView{}, false
	}
	if out.RedemptionCount, ok = somaSegura(a.RedemptionCount, b.RedemptionCount); !ok {
		return TotalsView{}, false
	}
	return out, true
}

// pagina lê a página do mês e monta os itens com os rótulos de conta e
// categoria.
//
// Pede uma linha A MAIS para saber se existe próxima página sem pagar um
// COUNT, exatamente como GET /transactions — e o cursor devolvido é o MESMO
// formato, para o app inteiro ter um só.
func (s *Service) pagina(ctx context.Context, householdID string, tax taxonomia, in OverviewInput, cursor transaction.Cursor) ([]ItemView, *string, error) {
	tamanho := tamanhoDaPagina(in.Limit)
	filtro := transaction.CategoryListFilter{
		CompetenceMonth: in.Month,
		CategoryIDs:     tax.marcadas,
		Limit:           tamanho + 1,
	}
	if !cursor.OccurredOn.IsZero() {
		filtro.Cursor = &cursor
	}

	linhas, err := s.ledger.ListByCategories(ctx, householdID, filtro)
	if err != nil {
		return nil, nil, fmt.Errorf("listando lançamentos marcados do mês: %w", err)
	}

	var proximo *string
	if len(linhas) > tamanho {
		linhas = linhas[:tamanho]
		texto := transaction.EncodeCursor(transaction.Cursor{
			OccurredOn: linhas[len(linhas)-1].OccurredOn,
			ID:         linhas[len(linhas)-1].ID,
		})
		if texto != "" {
			proximo = &texto
		}
	}

	itens := make([]ItemView, 0, len(linhas))
	if len(linhas) == 0 {
		return itens, proximo, nil
	}

	contas, err := s.nomesDeConta(ctx, householdID)
	if err != nil {
		return nil, nil, err
	}

	var anomalas int
	for _, t := range linhas {
		// O FLUXO vem do kind do LANÇAMENTO, nunca da natureza da categoria
		// (ADR-029d). O repositório já filtra `kind IN (income, expense)`;
		// a linha que ainda assim não tiver fluxo conhecido é DESCARTADA da
		// lista, em vez de publicada com `flow: ""` — o enum do contrato é
		// fechado, e um valor fora dele quebraria a tela sem dizer por quê.
		fluxo, ok := fluxoDoLancamento(t.Kind)
		if !ok || t.CategoryID == nil {
			anomalas++
			continue
		}
		categoriaID := *t.CategoryID
		var nome string
		if c, ok := tax.porID[categoriaID]; ok {
			nome = c.Name
		}
		itens = append(itens, ItemView{
			ID:           t.ID,
			OccurredOn:   t.OccurredOn,
			Flow:         fluxo,
			AccountID:    t.AccountID,
			AccountName:  contas[t.AccountID],
			CategoryID:   categoriaID,
			CategoryName: nome,
			AmountCents:  t.AmountCents,
			Description:  t.Description,
			Source:       t.Source,
		})
	}
	if anomalas > 0 {
		// UM aviso por requisição, com a CONTAGEM e nada mais: nem id, nem
		// descrição, nem centavos (S8).
		s.lg.WarnContext(ctx, "lançamentos marcados com forma inesperada foram omitidos da lista",
			slog.String("month", in.Month),
			slog.Int("omitidos", anomalas),
		)
	}
	return itens, proximo, nil
}

// nomesDeConta carrega id -> nome das contas da casa, ARQUIVADAS INCLUÍDAS: o
// aporte de março numa conta arquivada em agosto continua tendo acontecido, e
// a lista não pode ficar sem o rótulo por causa disso.
func (s *Service) nomesDeConta(ctx context.Context, householdID string) (map[string]string, error) {
	linhas, err := s.accounts.List(ctx, householdID, true)
	if err != nil {
		return nil, fmt.Errorf("carregando nomes de conta: %w", err)
	}
	out := make(map[string]string, len(linhas))
	for _, a := range linhas {
		out[a.ID] = a.Name
	}
	return out, nil
}

// fluxoDoLancamento traduz o `kind` do LANÇAMENTO no fluxo do contrato
// (ADR-029d): despesa é aporte (o dinheiro saiu da conta), receita é resgate
// (o dinheiro entrou).
//
// A natureza da categoria NÃO participa desta escolha, e é de propósito: é o
// que garante que o que sai de `expenseCents` seja exatamente o que entra em
// `investedCents`, mesmo diante de dado anômalo. Transferência, kind vazio ou
// qualquer outro valor respondem false — perna de transferência não tem
// categoria (ADR-016) e, portanto, não é aporte nem resgate.
func fluxoDoLancamento(kind string) (string, bool) {
	switch kind {
	case transaction.KindExpense:
		return FlowContribution, true
	case transaction.KindIncome:
		return FlowRedemption, true
	default:
		return "", false
	}
}

// tamanhoDaPagina aplica o padrão e o teto de `items`.
//
// O teto é o MESMO nome e o MESMO número de GET /transactions
// (transaction.MaxAPIPageSize = 100), publicado no contrato. A borda já recusa
// acima dele com 400; aqui a reaplicação é defesa em profundidade — nunca
// truncamento silencioso do que o cliente pediu, porque o que passa daqui já
// foi recusado lá.
func tamanhoDaPagina(limit int) int {
	switch {
	case limit <= 0:
		return transaction.DefaultPageSize
	case limit > transaction.MaxAPIPageSize:
		return transaction.MaxAPIPageSize
	default:
		return limit
	}
}
