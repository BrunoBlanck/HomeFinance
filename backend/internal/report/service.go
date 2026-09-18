package report

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Service monta os relatórios. Só leitura: não há UnitOfWork nem auditoria.
type Service struct {
	ledger     Ledger
	categories Categories
	lg         *slog.Logger
}

// NewService monta o serviço. O logger é usado APENAS para o aviso AGREGADO
// de anomalias de dado (categoria que o lançamento aponta e a casa não tem;
// folha cujo pai sumiu) — um por requisição, com contagens e uma amostra
// curta de ids, nunca centavos nem nome.
func NewService(ledger Ledger, categories Categories, lg *slog.Logger) *Service {
	if lg == nil {
		lg = slog.Default()
	}
	return &Service{ledger: ledger, categories: categories, lg: lg}
}

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
// Ordem das guardas: casa → forma do mês → allowlist da natureza → só então o
// banco. Nada é consultado com entrada não validada, e o kind é comparado
// contra o conjunto fechado antes de virar placeholder.
//
// O enum de `kind` continua `income|expense` (ADR-029h): investimento não é
// uma natureza a mais do relatório, é tela própria. O que muda com a E7 é que
// as linhas cuja CATEGORIA é de natureza `investment`/`redemption` são
// descartadas na dobra — sem consulta nova, com o mapa de categorias que esta
// função já carrega.
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

	rows, err := s.ledger.SumByCategory(ctx, householdID, in.Month, kind)
	if err != nil {
		return CategoryReportView{}, fmt.Errorf("somando lançamentos por categoria: %w", err)
	}
	// A saída é limitada pela TAXONOMIA (≤ MaxPerHousehold categorias + a
	// linha nula), não pelo volume de lançamentos. Mais do que isso é banco
	// em estado que a aplicação não produz — 500, com a contagem no log e
	// nada mais.
	//
	// Esta anomalia falha FECHADA e as outras (id órfão) falham ABERTAS de
	// propósito: aqui o teto da própria casa foi violado e o relatório não
	// sabe mais o que é a taxonomia dela; lá a referência é órfã e o dinheiro
	// tem para onde ir (o balde). Regra registrada no ADR-027(f).
	if len(rows) > category.MaxPerHousehold+1 {
		return CategoryReportView{}, fmt.Errorf("%w: %d linhas", errTooManyRows, len(rows))
	}

	view := CategoryReportView{
		Month: in.Month,
		Kind:  kind,
		Items: []CategoryReportGroupView{},
	}
	if len(rows) == 0 {
		return view, nil
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

	grupos, ordem, anom := dobrar(rows, porID)
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
// Os "avisos" não saem aqui: a função só ACUMULA as anomalias e devolve o
// acumulador, para o chamador emitir um único registro por requisição.
func dobrar(rows []CategoryTotal, porID map[string]category.Category) (map[string]*grupo, []string, anomalias) {
	grupos := map[string]*grupo{}
	var ordem []string
	var anom anomalias
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
	return grupos, ordem, anom
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
	paiAusente        int64
	amostraCategoria  []string
	amostraPaiAusente []string
}

func (an *anomalias) categoriaFora(id string) {
	an.categoriaDesconhecida++
	an.amostraCategoria = amostrar(an.amostraCategoria, id)
}

func (an *anomalias) paiFora(id string) {
	an.paiAusente++
	an.amostraPaiAusente = amostrar(an.amostraPaiAusente, id)
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
	total := an.categoriaDesconhecida + an.paiAusente
	if total == 0 {
		return
	}
	attrs := []any{
		slog.Int64("anomalias", total),
		slog.Int64("categoria_desconhecida", an.categoriaDesconhecida),
		slog.Int64("pai_ausente", an.paiAusente),
	}
	if len(an.amostraCategoria) > 0 {
		attrs = append(attrs, slog.Any("amostra_categoria_desconhecida", an.amostraCategoria))
	}
	if len(an.amostraPaiAusente) > 0 {
		attrs = append(attrs, slog.Any("amostra_pai_ausente", an.amostraPaiAusente))
	}
	lg.Warn("relatório por categoria encontrou linhas com categoria inconsistente", attrs...)
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
