package report

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Service monta os relatórios. Só leitura: não há UnitOfWork nem auditoria.
type Service struct {
	ledger     Ledger
	categories Categories
	accounts   Accounts
	lg         *slog.Logger
}

// NewService monta o serviço. O logger é usado APENAS para o aviso AGREGADO
// de anomalias de dado (categoria que o lançamento aponta e a casa não tem;
// folha cujo pai sumiu; conta que a linha aponta e a casa não tem) — um por
// requisição, com contagens e uma amostra curta de ids, nunca centavos nem
// nome.
func NewService(ledger Ledger, categories Categories, accounts Accounts, lg *slog.Logger) *Service {
	if lg == nil {
		lg = slog.Default()
	}
	return &Service{ledger: ledger, categories: categories, accounts: accounts, lg: lg}
}

// maxRowsByCategoryAndAccount é o teto ESTRUTURAL de linhas da agregação:
// (categorias da casa + a linha nula) × contas da casa. Os dois fatores são
// tetos reais — categoria e conta com lançamento não podem ser excluídas
// (UsageChecker responde 422) —, então toda linha que a agregação devolve
// aponta para algo vivo e da casa, e mais do que isso é banco em estado que
// a aplicação não produz.
const maxRowsByCategoryAndAccount = (category.MaxPerHousehold + 1) * account.MaxPerHousehold

// grupo é o acumulador de UM item de nível 1 durante a dobra. `id` vazio é o
// balde "Sem categoria".
type grupo struct {
	id         string
	nome       string
	nomeNorm   string
	archivedAt *time.Time

	directCents int64
	directCount int64
	filhos      map[string]*filho
	// ordemFilhos guarda a ordem de chegada para a ordenação final ser
	// determinística mesmo em empate total (map não tem ordem).
	ordemFilhos []string
}

type filho struct {
	id         string
	nome       string
	nomeNorm   string
	archivedAt *time.Time
	totalCents int64
	count      int64
}

// ByCategory responde "quanto foi para cada categoria neste mês" (ADR-027).
//
// Ordem das guardas: casa → forma do mês → allowlist da natureza → allowlist
// do recorte de contas → só então o banco. Nada é consultado com entrada não
// validada, e o kind é comparado contra o conjunto fechado antes de virar
// placeholder. Uma falha por resposta: a primeira guarda que recusa é a que
// responde.
//
// O enum de `kind` continua `income|expense` (ADR-029h): investimento não é
// uma natureza a mais do relatório, é tela própria. O que muda com a E7 é que
// as linhas cuja CATEGORIA é de natureza `investment`/`redemption` são
// descartadas na dobra — sem consulta nova, com o mapa de categorias que esta
// função já carrega.
//
// # O recorte crédito/débito (ADR-032) é PARTIÇÃO, não segunda consulta
//
// A agregação é sempre a MESMA — `GROUP BY category_id, account_id`, sem id
// de conta no SQL — e o recorte é feito em Go: `credit` fica com as linhas
// cuja conta é cartão da casa, `debit` com o complemento. Duas consultas (uma
// com `account_id IN (?)` e outra com `NOT IN`) poderiam DISCORDAR se uma
// escrita entrasse entre elas, e `NOT IN` ainda estouraria o orçamento de
// parâmetros do dialeto mais estreito (ADR-031b). Sendo partição das mesmas
// linhas, `total(credit) + total(debit) == total(todas)` vale por construção
// — por total, por contagem e por categoria.
//
// A lista de contas só é carregada quando HÁ recorte: sob "todas" o caminho
// continua com as duas leituras de sempre (agregação e categorias).
func (s *Service) ByCategory(ctx context.Context, ator Actor, in ByCategoryInput) (CategoryReportView, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return CategoryReportView{}, ErrUnauthenticated
	}
	if _, err := transaction.ParseMonth(in.Month); err != nil {
		return CategoryReportView{}, err
	}
	kind := in.Kind
	if kind == "" {
		kind = transaction.KindExpense
	}
	// Allowlist FECHADA e exata: "EXPENSE", "expense,income" e
	// "transfer_out" são todos recusados com o mesmo erro. Transferência não
	// é natureza de relatório (ADR-016).
	if kind != transaction.KindExpense && kind != transaction.KindIncome {
		return CategoryReportView{}, ErrInvalidKind
	}
	// Allowlist FECHADA e exata do recorte, DEPOIS da natureza e ANTES de
	// qualquer consulta. Vazio é "todas as contas"; qualquer outra coisa que
	// não seja EXATAMENTE `credit` ou `debit` — "CREDIT", " credit",
	// "credit_card", "credit,debit" — é recusada com o mesmo erro, sem
	// TrimSpace nem ToLower: normalizar ensinaria ao cliente o que o servidor
	// tolera. O que segue adiante é a CONSTANTE, e é ela que a resposta ecoa.
	var grupoConta *string
	switch in.AccountGroup {
	case "":
	case AccountGroupCredit:
		grupoConta = ptrString(AccountGroupCredit)
	case AccountGroupDebit:
		grupoConta = ptrString(AccountGroupDebit)
	default:
		return CategoryReportView{}, ErrInvalidAccountGroup
	}

	rows, err := s.ledger.SumByCategoryAndAccount(ctx, householdID, in.Month, kind)
	if err != nil {
		return CategoryReportView{}, fmt.Errorf("somando lançamentos por categoria e conta: %w", err)
	}
	// A saída é limitada pela ESTRUTURA ((categorias + 1) × contas da casa),
	// não pelo volume de lançamentos. Mais do que isso é banco em estado que
	// a aplicação não produz — 500, com a contagem no log e nada mais.
	//
	// Esta anomalia falha FECHADA e as outras (id órfão) falham ABERTAS de
	// propósito: aqui o teto da própria casa foi violado e o relatório não
	// sabe mais o que é a taxonomia dela; lá a referência é órfã e o dinheiro
	// tem para onde ir (o balde). Regra registrada no ADR-027(f).
	if len(rows) > maxRowsByCategoryAndAccount {
		return CategoryReportView{}, fmt.Errorf("%w: %d linhas (teto %d)",
			errTooManyRows, len(rows), maxRowsByCategoryAndAccount)
	}

	view := CategoryReportView{
		Month:        in.Month,
		Kind:         kind,
		AccountGroup: grupoConta,
		Items:        []CategoryReportGroupView{},
	}
	if len(rows) == 0 {
		return view, nil
	}

	// As anomalias das duas etapas (recorte e dobra) vão para UM acumulador,
	// porque o aviso é UM por requisição — mais abaixo.
	var anom anomalias
	if grupoConta != nil {
		contas, err := s.carregarContas(ctx, householdID)
		if err != nil {
			return CategoryReportView{}, err
		}
		rows = recortarPorConta(rows, *grupoConta, contas, &anom)
		// Casa sem cartão sob `credit` (ou só com cartões sob `debit`): a
		// partição fica vazia e a resposta é o mês vazio bem formado, com o
		// recorte ecoado — sem carregar categorias, que não há o que dobrar.
		if len(rows) == 0 {
			anom.avisar(s.lg)
			return view, nil
		}
	}

	// Arquivadas INCLUÍDAS: o lançamento de uma categoria arquivada continua
	// sendo gasto daquele mês (PLANOS.md §4.4). A lista é da CASA DO TOKEN —
	// um category_id que não esteja nela é, por construção, algo que esta
	// casa não pode ver, e cai no balde sem ser consultado.
	cats, err := s.categories.List(ctx, householdID, true)
	if err != nil {
		return CategoryReportView{}, fmt.Errorf("carregando categorias da casa: %w", err)
	}
	porID := make(map[string]category.Category, len(cats))
	for i := range cats {
		porID[cats[i].ID] = cats[i]
	}

	grupos, ordem := dobrar(rows, porID, &anom)
	// UM aviso por requisição, depois da dobra. Avisar por LINHA seria
	// amplificação de log: quem tem o token recarrega a tela e multiplica o
	// volume sem custo. A contagem preserva o diagnóstico; a amostra dá por
	// onde começar a investigar.
	anom.avisar(s.lg)

	// Totais do grupo e do mês, com conferência de estouro: cada parcela
	// respeita MaxAmountCents, mas a SOMA de um mês inteiro é do banco, e o
	// relatório publica int64 — se não couber, não pode fingir que coube.
	type acumulado struct {
		g          *grupo
		totalCents int64
		count      int64
	}
	acum := make([]acumulado, 0, len(ordem))
	var grandCents, grandCount int64
	for _, chave := range ordem {
		g := grupos[chave]
		total, count := g.directCents, g.directCount
		for _, fid := range g.ordemFilhos {
			f := g.filhos[fid]
			var ok bool
			if total, ok = somaSegura(total, f.totalCents); !ok {
				return CategoryReportView{}, errTotalOverflow
			}
			// A contagem passa pela mesma conferência dos centavos. Na
			// prática `COUNT(*)` nunca estoura int64 nem vem negativo; é a
			// mesma disciplina aplicada ao mesmo tipo de número, sem exceção
			// que alguém precise lembrar de justificar depois.
			if count, ok = somaSegura(count, f.count); !ok {
				return CategoryReportView{}, errTotalOverflow
			}
		}
		var ok bool
		if grandCents, ok = somaSegura(grandCents, total); !ok {
			return CategoryReportView{}, errTotalOverflow
		}
		if grandCount, ok = somaSegura(grandCount, count); !ok {
			return CategoryReportView{}, errTotalOverflow
		}
		acum = append(acum, acumulado{g: g, totalCents: total, count: count})
	}

	// Ordenação: valor desc, contagem desc, nome asc; o balde "Sem categoria"
	// fica por último em empate — ele não tem nome para desempatar.
	slices.SortStableFunc(acum, func(a, b acumulado) int {
		if a.totalCents != b.totalCents {
			return cmpDesc(a.totalCents, b.totalCents)
		}
		if a.count != b.count {
			return cmpDesc(a.count, b.count)
		}
		if (a.g.id == "") != (b.g.id == "") {
			if a.g.id == "" {
				return 1
			}
			return -1
		}
		return strings.Compare(a.g.nomeNorm, b.g.nomeNorm)
	})

	// Nível 1: 10000 pontos-base entre os grupos (Σ = 10000 exato quando o
	// mês tem valor). Nível 2: a fatia do grupo entre as filhas e o "direto",
	// de modo que grupo.shareBp == directShareBp + Σ filhas.shareBp.
	pesos := make([]int64, len(acum))
	for i := range acum {
		pesos[i] = acum[i].totalCents
	}
	shares := apportion(pesos, BasisPointsTotal)

	view.TotalCents = grandCents
	view.Count = grandCount
	view.Items = make([]CategoryReportGroupView, 0, len(acum))
	for i := range acum {
		view.Items = append(view.Items, montarGrupo(acum[i].g, acum[i].totalCents, acum[i].count, shares[i]))
	}
	return view, nil
}

// contasDaCasa é o que o recorte precisa saber sobre as contas da casa.
type contasDaCasa struct {
	// todas é o conjunto de ids da casa. Serve para reconhecer a ANOMALIA:
	// uma linha agregada cuja conta não está aqui.
	todas map[string]struct{}
	// cartoes são os ids de tipo credit_card, ARQUIVADOS INCLUÍDOS — o gasto
	// de um cartão arquivado continua sendo gasto de cartão daquele mês.
	cartoes map[string]struct{}
}

// carregarContas lê as contas da casa do TOKEN e monta os dois conjuntos do
// recorte. Só é chamada quando há recorte (ADR-032).
//
// includeArchived é TRUE: pedir a lista sem as arquivadas faria o gasto de
// cartão encolher no mês em que alguém arquivasse um cartão — isto é, mudaria
// o passado —, e faria o recorte `credit` divergir do `creditCardExpenseCents`
// do painel, que inclui as arquivadas. A casa é RECONFERIDA em Go: o
// repositório já filtra, e reconferir custa uma comparação por conta — conta
// de outra casa jamais entra num conjunto que decide o que é cartão.
func (s *Service) carregarContas(ctx context.Context, householdID string) (contasDaCasa, error) {
	lista, err := s.accounts.List(ctx, householdID, true)
	if err != nil {
		return contasDaCasa{}, fmt.Errorf("carregando contas da casa: %w", err)
	}
	c := contasDaCasa{
		todas:   make(map[string]struct{}, len(lista)),
		cartoes: make(map[string]struct{}, len(lista)),
	}
	for i := range lista {
		a := lista[i]
		if a.HouseholdID != householdID || a.ID == "" {
			continue
		}
		c.todas[a.ID] = struct{}{}
		if a.Kind == account.KindCreditCard {
			c.cartoes[a.ID] = struct{}{}
		}
	}
	return c, nil
}

// recortarPorConta devolve só as linhas do grupo pedido: `credit` mantém as
// linhas cuja conta é cartão; `debit` mantém o COMPLEMENTO. As duas
// partições são disjuntas e cobrem todas as linhas — é isso que faz
// `total(credit) + total(debit) == total(todas)` valer por construção, sem
// nenhuma soma nova.
//
// Linha cuja conta não está na lista da casa é ANOMALIA de dado (só banco
// adulterado ou restauração parcial produz isso — conta com lançamento não
// pode ser excluída), e falha ABERTA no molde do ADR-027f: ela não é cartão,
// então cai em `debit`, e o dinheiro continua aparecendo. Descartá-la sumiria
// com dinheiro por causa de um id órfão; tratá-la como cartão inventaria um
// cartão. O aviso é agregado — só o id, nunca centavos.
//
// A saída é uma fatia NOVA, e não `rows[:0]` reaproveitado. Filtrar no lugar
// seria mais barato e dependeria de uma propriedade que ESTE código não
// controla: que o Ledger devolva um array recém-construído a cada chamada. O
// dia em que alguém puser um cache ali, filtrar no lugar corromperia a fatia
// guardada — e, como o filtro depende da lista de contas da CASA, a corrupção
// seria entre casas. Uma cópia de ≤ 10 050 structs é barata demais para se
// pagar esse risco.
func recortarPorConta(rows []CategoryAccountTotal, grupo string, c contasDaCasa, anom *anomalias) []CategoryAccountTotal {
	querCartao := grupo == AccountGroupCredit
	out := make([]CategoryAccountTotal, 0, len(rows))
	for i := range rows {
		row := rows[i]
		ehCartao := false
		if _, ok := c.todas[row.AccountID]; !ok {
			anom.contaFora(row.AccountID)
		} else if _, ok := c.cartoes[row.AccountID]; ok {
			ehCartao = true
		}
		if ehCartao == querCartao {
			out = append(out, row)
		}
	}
	return out
}

// ptrString devolve um ponteiro para a string — o eco de `accountGroup` é
// ponteiro para poder ser `null`.
func ptrString(s string) *string { return &s }

// dobrar distribui as linhas planas da agregação na árvore de dois níveis.
//
// Casos, na ordem em que aparecem no código:
//   - category_id nulo → balde "Sem categoria" (direto);
//   - id que a casa não tem → balde + aviso (só o id no log). Só banco
//     adulterado ou categoria excluída com uso chega aqui — a exclusão é
//     barrada pelo UsageChecker —, e o relatório não pode sumir com dinheiro;
//   - categoria de natureza `investment`/`redemption` → DESCARTADA, dos totais
//     e das linhas (ADR-029e). Não vai para o balde: ela tem categoria;
//   - grupo (ParentID nulo) → direto no grupo;
//   - folha cujo pai a casa não tem → promovida a grupo próprio + aviso;
//   - folha → soma no grupo do pai, como filha.
//
// As linhas chegam por (categoria, conta) — ADR-032 —, e a mesma categoria
// vinda em várias linhas é SOMADA nos mesmos acumuladores: a dobra nunca
// dependeu de uma linha por categoria, e é por isso que o recorte por conta
// não precisou de código novo aqui.
//
// Os "avisos" não saem aqui: a função só ACUMULA as anomalias no acumulador
// recebido, para o chamador emitir um único registro por requisição.
func dobrar(rows []CategoryAccountTotal, porID map[string]category.Category, anom *anomalias) (map[string]*grupo, []string) {
	grupos := map[string]*grupo{}
	var ordem []string
	obterGrupo := func(c *category.Category) *grupo {
		chave := ""
		if c != nil {
			chave = c.ID
		}
		if g, ok := grupos[chave]; ok {
			return g
		}
		g := &grupo{id: chave, filhos: map[string]*filho{}}
		if c != nil {
			g.nome = c.Name
			g.nomeNorm = c.NameNorm
			g.archivedAt = c.ArchivedAt
		}
		grupos[chave] = g
		ordem = append(ordem, chave)
		return g
	}

	for _, row := range rows {
		if row.CategoryID == nil {
			g := obterGrupo(nil)
			g.directCents += row.TotalCents
			g.directCount += row.Count
			continue
		}
		cat, ok := porID[*row.CategoryID]
		if !ok {
			anom.categoriaFora(*row.CategoryID)
			g := obterGrupo(nil)
			g.directCents += row.TotalCents
			g.directCount += row.Count
			continue
		}
		// Aporte e resgate NÃO são despesa nem receita deste relatório
		// (ADR-029e): a linha é DESCARTADA — dos totais e das linhas.
		//
		// Ela não cai no balde "Sem categoria": ela TEM categoria, e jogá-la
		// lá transformaria o dinheiro investido numa pendência de
		// categorização que ninguém consegue resolver. O balde não muda.
		//
		// O predicado é o do `summary` de GET /transactions, importado de
		// `transaction` — é a mesma pergunta, e ela só pode ter uma resposta:
		// duas cópias divergiriam, e a mesma despesa sairia de `expenseCents`
		// e continuaria aqui. A natureza olhada é a da PRÓPRIA categoria da
		// linha (a folha herda a do grupo — ADR-017b), nunca a do pai: é
		// assim que as duas agregações enxergam exatamente o mesmo conjunto.
		//
		// Nada é registrado em log: descartar aporte é regra de negócio, e
		// não anomalia de dado.
		if transaction.MarcadaComoInvestimento(cat.Kind) {
			continue
		}
		if cat.ParentID == nil {
			g := obterGrupo(&cat)
			g.directCents += row.TotalCents
			g.directCount += row.Count
			continue
		}
		pai, ok := porID[*cat.ParentID]
		if !ok {
			anom.paiFora(cat.ID)
			g := obterGrupo(&cat)
			g.directCents += row.TotalCents
			g.directCount += row.Count
			continue
		}
		g := obterGrupo(&pai)
		f, ok := g.filhos[cat.ID]
		if !ok {
			f = &filho{id: cat.ID, nome: cat.Name, nomeNorm: cat.NameNorm, archivedAt: cat.ArchivedAt}
			g.filhos[cat.ID] = f
			g.ordemFilhos = append(g.ordemFilhos, cat.ID)
		}
		f.totalCents += row.TotalCents
		f.count += row.Count
	}
	return grupos, ordem
}

// maxAmostraAnomalia é o teto de ids DISTINTOS por tipo de anomalia no aviso.
// O aviso é um só por requisição, e uma amostra curta basta para investigar —
// a contagem já diz o tamanho do estrago.
const maxAmostraAnomalia = 5

// anomalias acumula, durante a dobra, as linhas que apontam para estado que a
// API não produz (só banco adulterado ou restauração parcial chega lá). Guarda
// contagens e ids — nunca nome de categoria, nunca centavos, nunca a consulta.
type anomalias struct {
	// categoriaDesconhecida: o lançamento aponta um category_id que não está
	// na taxonomia DESTA casa. Cai no balde "Sem categoria".
	categoriaDesconhecida int64
	// paiAusente: a folha é da casa, mas o parent_id não está na taxonomia
	// dela. A folha é promovida a grupo.
	paiAusente int64
	// contaDesconhecida: a linha agregada aponta uma conta que não está na
	// lista DESTA casa (só é detectada quando há recorte, que é quando a
	// lista é carregada). A linha não é cartão: cai em `debit`.
	contaDesconhecida int64
	amostraCategoria  []string
	amostraPaiAusente []string
	amostraConta      []string
}

func (an *anomalias) categoriaFora(id string) {
	an.categoriaDesconhecida++
	an.amostraCategoria = amostrar(an.amostraCategoria, id)
}

func (an *anomalias) paiFora(id string) {
	an.paiAusente++
	an.amostraPaiAusente = amostrar(an.amostraPaiAusente, id)
}

func (an *anomalias) contaFora(id string) {
	an.contaDesconhecida++
	an.amostraConta = amostrar(an.amostraConta, id)
}

// amostrar junta o id se ele ainda não estiver na amostra e houver espaço. A
// busca linear é barata de propósito: a amostra tem no máximo 5 elementos.
func amostrar(amostra []string, id string) []string {
	if len(amostra) >= maxAmostraAnomalia || slices.Contains(amostra, id) {
		return amostra
	}
	return append(amostra, id)
}

// avisar emite, no máximo, UM registro por requisição — a alternativa (um por
// linha) é amplificação de log a custo zero para quem tem o token. Os dois
// tipos continuam distinguíveis por campo próprio.
func (an anomalias) avisar(lg *slog.Logger) {
	total := an.categoriaDesconhecida + an.paiAusente + an.contaDesconhecida
	if total == 0 {
		return
	}
	attrs := []any{
		slog.Int64("anomalias", total),
		slog.Int64("categoria_desconhecida", an.categoriaDesconhecida),
		slog.Int64("pai_ausente", an.paiAusente),
		slog.Int64("conta_desconhecida", an.contaDesconhecida),
	}
	if len(an.amostraCategoria) > 0 {
		attrs = append(attrs, slog.Any("amostra_categoria_desconhecida", an.amostraCategoria))
	}
	if len(an.amostraPaiAusente) > 0 {
		attrs = append(attrs, slog.Any("amostra_pai_ausente", an.amostraPaiAusente))
	}
	if len(an.amostraConta) > 0 {
		attrs = append(attrs, slog.Any("amostra_conta_desconhecida", an.amostraConta))
	}
	lg.Warn("relatório por categoria encontrou linhas com referência inconsistente", attrs...)
}

// montarGrupo fecha UM item de nível 1: ordena as filhas, reparte a fatia do
// grupo entre elas e o direto, e monta o DTO no formato do contrato.
func montarGrupo(g *grupo, totalCents, count, shareBp int64) CategoryReportGroupView {
	filhos := make([]*filho, 0, len(g.ordemFilhos))
	for _, fid := range g.ordemFilhos {
		filhos = append(filhos, g.filhos[fid])
	}
	slices.SortStableFunc(filhos, func(a, b *filho) int {
		if a.totalCents != b.totalCents {
			return cmpDesc(a.totalCents, b.totalCents)
		}
		if a.count != b.count {
			return cmpDesc(a.count, b.count)
		}
		return strings.Compare(a.nomeNorm, b.nomeNorm)
	})

	// A última parte é o "direto": grupo.shareBp == Σ filhas + directShareBp.
	pesos := make([]int64, 0, len(filhos)+1)
	for _, f := range filhos {
		pesos = append(pesos, f.totalCents)
	}
	pesos = append(pesos, g.directCents)
	shares := apportion(pesos, shareBp)

	out := CategoryReportGroupView{
		TotalCents:    totalCents,
		Count:         count,
		ShareBp:       shareBp,
		DirectCents:   g.directCents,
		DirectCount:   g.directCount,
		DirectShareBp: shares[len(shares)-1],
		Children:      make([]CategoryReportChildView, 0, len(filhos)),
	}
	if g.id != "" {
		id, nome := g.id, g.nome
		out.CategoryID = &id
		out.Name = &nome
		out.ArchivedAt = formatarInstante(g.archivedAt)
	}
	for i, f := range filhos {
		out.Children = append(out.Children, CategoryReportChildView{
			CategoryID: f.id,
			Name:       f.nome,
			ArchivedAt: formatarInstante(f.archivedAt),
			TotalCents: f.totalCents,
			Count:      f.count,
			ShareBp:    shares[i],
		})
	}
	return out
}

// somaSegura soma dois valores NÃO NEGATIVOS e informa se coube em int64.
//
// A não negatividade é VERIFICADA, não confiada: com `a < 0`, a conta
// `math.MaxInt64-a` transbordaria e a função passaria a responder "não coube"
// para quase todo `b` — errada, ainda que fechada. Nem centavos (ADR-003,
// `ValidateAmount` recusa negativo) nem `COUNT(*)` podem ser negativos aqui;
// se um deles for, é banco em estado que a aplicação não produz, e o
// relatório prefere 500 a publicar um total inventado.
func somaSegura(a, b int64) (int64, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if b > math.MaxInt64-a {
		return 0, false
	}
	return a + b, true
}

// cmpDesc compara para ordem DECRESCENTE.
func cmpDesc(a, b int64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	default:
		return 0
	}
}

// formatarInstante devolve o instante em RFC 3339 UTC, como o resto da API
// (category.View.ArchivedAt), ou nulo.
func formatarInstante(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
