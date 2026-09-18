package investment_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Os fakes deste arquivo reproduzem o contrato do repositório REAL no que
// importa para o serviço: escopo por casa, só linha viva, só receita e
// despesa, conjunto de categorias vazio é ErrEmptyCategoryFilter (nunca
// "todas"), teto de 200 ids, e a allowlist de SetCategoryWhereCurrentIn
// conferida sobre a categoria ATUAL.
//
// Eles também CONTAM as chamadas. Não é luxo: os dois critérios mais
// importantes desta entrega — "casa sem categoria de investimento não consulta
// nada" e "casa nenhuma enxerga a outra" — passariam num teste que só olhasse
// o resultado, porque o resultado vazio é o mesmo tendo a consulta rodado ou
// não. Contar chamadas é a única forma de separar os dois.

const (
	casaA = "hh-aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	casaB = "hh-bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	// maxIDsNoFiltro espelha gormstore.maxCategoryFilterIDs, que é o mesmo
	// category.MaxPerHousehold.
	maxIDsNoFiltro = category.MaxPerHousehold
)

var errFalhaDoBanco = errors.New("falha simulada do banco")

// --- lançamentos ------------------------------------------------------------

type ledgerFake struct {
	linhas map[string]transaction.Transaction
	ordem  []string

	// Contadores por método.
	chamadasSum        int
	chamadasList       int
	chamadasListDoMes  int
	chamadasSetNull    int
	chamadasSetCurrent int

	// Últimos argumentos, para os testes que aferem o que foi ao banco.
	ultimaCasaSum     string
	ultimaCasaList    string
	ultimaCasaDoMes   string
	ultimoFrom        string
	ultimoTo          string
	ultimasCategorias []string
	ultimaAllowlist   []string
	ultimoLimiteDoMes int

	// Injeção de falha.
	erroSum       error
	erroList      error
	erroDoMes     error
	erroSetNull   error
	erroSetAtual  error
	linhasDoSumOv []transaction.InvestmentMonthTotals // substitui o cálculo
}

func novoLedger() *ledgerFake {
	return &ledgerFake{linhas: map[string]transaction.Transaction{}}
}

func (l *ledgerFake) por(id string) transaction.Transaction { return l.linhas[id] }

func (l *ledgerFake) juntar(txs ...transaction.Transaction) {
	for _, t := range txs {
		if _, existe := l.linhas[t.ID]; !existe {
			l.ordem = append(l.ordem, t.ID)
		}
		l.linhas[t.ID] = t
	}
}

// vivasDaCasa devolve as linhas vivas da casa em ordem (occurred_on, id)
// crescente — a ordem base das leituras do repositório real.
func (l *ledgerFake) vivasDaCasa(householdID string) []transaction.Transaction {
	out := make([]transaction.Transaction, 0, len(l.ordem))
	for _, id := range l.ordem {
		t := l.linhas[id]
		if t.HouseholdID != householdID || t.DeletedAt != nil {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.OccurredOn.String() != b.OccurredOn.String() {
			return a.OccurredOn.String() < b.OccurredOn.String()
		}
		return a.ID < b.ID
	})
	return out
}

func conjuntoDeIDs(ids []string) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		out[id] = struct{}{}
	}
	if len(out) == 0 {
		return nil, transaction.ErrEmptyCategoryFilter
	}
	if len(out) > maxIDsNoFiltro {
		return nil, transaction.ErrTooManyCategories
	}
	return out, nil
}

func categorizavel(t transaction.Transaction) bool {
	return t.Kind == transaction.KindIncome || t.Kind == transaction.KindExpense
}

func (l *ledgerFake) SumInvestmentsByMonth(_ context.Context, householdID string, categoryIDs []string, fromMonth, toMonth string) ([]transaction.InvestmentMonthTotals, error) {
	l.chamadasSum++
	l.ultimaCasaSum = householdID
	l.ultimasCategorias = append([]string(nil), categoryIDs...)
	l.ultimoFrom, l.ultimoTo = fromMonth, toMonth

	marcadas, err := conjuntoDeIDs(categoryIDs)
	if err != nil {
		return nil, err
	}
	if l.erroSum != nil {
		return nil, l.erroSum
	}
	if l.linhasDoSumOv != nil {
		return l.linhasDoSumOv, nil
	}

	porMes := map[string]*transaction.InvestmentMonthTotals{}
	var meses []string
	for _, t := range l.vivasDaCasa(householdID) {
		if !categorizavel(t) || t.CategoryID == nil {
			continue
		}
		if _, ok := marcadas[*t.CategoryID]; !ok {
			continue
		}
		// "AAAA-MM" tem largura fixa: ordem de texto é ordem cronológica.
		if t.CompetenceMonth < fromMonth || t.CompetenceMonth > toMonth {
			continue
		}
		item, ok := porMes[t.CompetenceMonth]
		if !ok {
			item = &transaction.InvestmentMonthTotals{Month: t.CompetenceMonth}
			porMes[t.CompetenceMonth] = item
			meses = append(meses, t.CompetenceMonth)
		}
		if t.Kind == transaction.KindExpense {
			item.ContributionsCents += t.AmountCents
			item.ContributionCount++
		} else {
			item.RedemptionsCents += t.AmountCents
			item.RedemptionCount++
		}
	}
	sort.Strings(meses)
	out := make([]transaction.InvestmentMonthTotals, 0, len(meses))
	for _, m := range meses {
		out = append(out, *porMes[m])
	}
	return out, nil
}

func (l *ledgerFake) ListByCategories(_ context.Context, householdID string, f transaction.CategoryListFilter) ([]transaction.Transaction, error) {
	l.chamadasList++
	l.ultimaCasaList = householdID

	marcadas, err := conjuntoDeIDs(f.CategoryIDs)
	if err != nil {
		return nil, err
	}
	if l.erroList != nil {
		return nil, l.erroList
	}

	candidatas := l.vivasDaCasa(householdID)
	// Do mais recente para o mais antigo, como GET /transactions.
	sort.Slice(candidatas, func(i, j int) bool {
		a, b := candidatas[i], candidatas[j]
		if a.OccurredOn.String() != b.OccurredOn.String() {
			return a.OccurredOn.String() > b.OccurredOn.String()
		}
		return a.ID > b.ID
	})

	limite := f.Limit
	if limite <= 0 {
		limite = transaction.DefaultPageSize
	}
	if limite > transaction.MaxPageSize {
		limite = transaction.MaxPageSize
	}

	var out []transaction.Transaction
	for _, t := range candidatas {
		if !categorizavel(t) || t.CompetenceMonth != f.CompetenceMonth || t.CategoryID == nil {
			continue
		}
		if _, ok := marcadas[*t.CategoryID]; !ok {
			continue
		}
		if f.Cursor != nil {
			on, cursorOn := t.OccurredOn.String(), f.Cursor.OccurredOn.String()
			if on > cursorOn || (on == cursorOn && t.ID >= f.Cursor.ID) {
				continue
			}
		}
		out = append(out, t)
		if len(out) >= limite {
			break
		}
	}
	return out, nil
}

func (l *ledgerFake) ListIncomeExpenseOfMonth(_ context.Context, householdID, competenceMonth string, limit int) ([]transaction.CategorizableRow, error) {
	l.chamadasListDoMes++
	l.ultimaCasaDoMes = householdID
	l.ultimoLimiteDoMes = limit
	if l.erroDoMes != nil {
		return nil, l.erroDoMes
	}

	var out []transaction.CategorizableRow
	for _, t := range l.vivasDaCasa(householdID) {
		if !categorizavel(t) || t.CompetenceMonth != competenceMonth {
			continue
		}
		out = append(out, transaction.CategorizableRow{
			ID:              t.ID,
			Kind:            t.Kind,
			Description:     t.Description,
			DescriptionNorm: t.DescriptionNorm,
			CategoryID:      t.CategoryID,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (l *ledgerFake) SetCategoryWhereNull(_ context.Context, householdID string, ids []string, categoryID string, at time.Time) (int64, error) {
	l.chamadasSetNull++
	if l.erroSetNull != nil {
		return 0, l.erroSetNull
	}
	var afetadas int64
	for _, id := range ids {
		t, ok := l.linhas[id]
		if !ok || t.HouseholdID != householdID || t.DeletedAt != nil || !categorizavel(t) {
			continue
		}
		// O `category_id IS NULL` mora no WHERE: linha que ganhou categoria
		// entre a prévia e a confirmação não é tocada.
		if t.CategoryID != nil {
			continue
		}
		nova := categoryID
		t.CategoryID = &nova
		t.UpdatedAt = at
		l.linhas[id] = t
		afetadas++
	}
	return afetadas, nil
}

func (l *ledgerFake) SetCategoryWhereCurrentIn(_ context.Context, householdID string, ids, currentCategoryIDs []string, categoryID string, at time.Time) (int64, error) {
	l.chamadasSetCurrent++
	l.ultimaAllowlist = append([]string(nil), currentCategoryIDs...)

	permitidas, err := conjuntoDeIDs(currentCategoryIDs)
	if err != nil {
		return 0, err
	}
	if l.erroSetAtual != nil {
		return 0, l.erroSetAtual
	}

	var afetadas int64
	for _, id := range ids {
		t, ok := l.linhas[id]
		if !ok || t.HouseholdID != householdID || t.DeletedAt != nil || !categorizavel(t) {
			continue
		}
		// NULL nunca está num IN (...), e categoria de investimento não está
		// na allowlist: as duas ficam de fora pelo WHERE, não pelo Go.
		if t.CategoryID == nil {
			continue
		}
		if _, ok := permitidas[*t.CategoryID]; !ok {
			continue
		}
		nova := categoryID
		t.CategoryID = &nova
		t.UpdatedAt = at
		l.linhas[id] = t
		afetadas++
	}
	return afetadas, nil
}

// --- categorias e contas ----------------------------------------------------

type categoriasFake struct {
	porCasa   map[string][]category.Category
	palavras  map[string][]category.Keyword
	chamadas  int
	erro      error
	ultimaSet bool

	// Reconferência dos destinos dentro da transação (TOCTOU). Contada e com
	// os argumentos guardados: "uma consulta só, nunca uma por id" é afirmação
	// que só se prova contando.
	chamadasAtribuiveis int
	ultimosAtribuiveis  []string
	erroAtribuiveis     error
}

func novasCategorias() *categoriasFake {
	return &categoriasFake{
		porCasa:  map[string][]category.Category{},
		palavras: map[string][]category.Keyword{},
	}
}

func (c *categoriasFake) List(_ context.Context, householdID string, includeArchived bool) ([]category.Category, error) {
	c.chamadas++
	c.ultimaSet = includeArchived
	if c.erro != nil {
		return nil, c.erro
	}
	var out []category.Category
	for _, cat := range c.porCasa[householdID] {
		if !includeArchived && (cat.ArchivedAt != nil || cat.DeletedAt != nil) {
			continue
		}
		if cat.DeletedAt != nil {
			continue
		}
		out = append(out, cat)
	}
	return out, nil
}

// LiveStates reproduz o contrato do repositório real: escopo por casa, fora a
// EXCLUÍDA, natureza ATUAL, e "grupo com subcategoria ativa" marcado. Arquivada
// CONTINUA no mapa — arquivar é benigno, e o teste que confundir os dois
// aprovaria a regressão que este método existe para barrar.
func (c *categoriasFake) LiveStates(_ context.Context, householdID string, ids []string) (map[string]category.LiveState, error) {
	c.chamadasAtribuiveis++
	c.ultimosAtribuiveis = append([]string(nil), ids...)
	if c.erroAtribuiveis != nil {
		return nil, c.erroAtribuiveis
	}

	viva := map[string]category.Category{}
	comFilhaAtiva := map[string]struct{}{}
	for _, cat := range c.porCasa[householdID] {
		if cat.HouseholdID != householdID || cat.DeletedAt != nil {
			continue
		}
		viva[cat.ID] = cat
		if cat.ParentID != nil && cat.ArchivedAt == nil {
			comFilhaAtiva[*cat.ParentID] = struct{}{}
		}
	}

	out := map[string]category.LiveState{}
	for _, id := range ids {
		cat, ok := viva[id]
		if !ok {
			continue
		}
		_, grupo := comFilhaAtiva[id]
		out[id] = category.LiveState{ID: id, Kind: cat.Kind, HasActiveChild: grupo, Archived: cat.ArchivedAt != nil}
	}
	return out, nil
}

func (c *categoriasFake) ListKeywords(_ context.Context, householdID string) ([]category.Keyword, error) {
	return c.palavras[householdID], nil
}

// juntar cadastra uma categoria e, opcionalmente, as palavras-chave dela.
func (c *categoriasFake) juntar(householdID, id, nome, kind string, palavras ...string) {
	c.porCasa[householdID] = append(c.porCasa[householdID], category.Category{
		ID:          id,
		HouseholdID: householdID,
		Name:        nome,
		NameNorm:    textnorm.Normalize(nome),
		Kind:        kind,
	})
	for i, p := range palavras {
		c.palavras[householdID] = append(c.palavras[householdID], category.Keyword{
			ID:          id + "-kw-" + p,
			HouseholdID: householdID,
			CategoryID:  id,
			Keyword:     p,
			Norm:        textnorm.Normalize(p),
			Position:    i,
		})
	}
}

// arquivar marca a categoria como arquivada (ela continua contando no passado).
func (c *categoriasFake) arquivar(householdID, id string) {
	quando := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i := range c.porCasa[householdID] {
		if c.porCasa[householdID][i].ID == id {
			c.porCasa[householdID][i].ArchivedAt = &quando
		}
	}
}

type contasFake struct {
	porCasa  map[string][]account.Account
	chamadas int
	erro     error
}

func novasContas() *contasFake {
	return &contasFake{porCasa: map[string][]account.Account{}}
}

func (a *contasFake) List(_ context.Context, householdID string, _ bool) ([]account.Account, error) {
	a.chamadas++
	if a.erro != nil {
		return nil, a.erro
	}
	return a.porCasa[householdID], nil
}

func (a *contasFake) ListKeywords(_ context.Context, _ string) ([]account.Keyword, error) {
	return nil, nil
}

func (a *contasFake) juntar(householdID, id, nome string) {
	a.porCasa[householdID] = append(a.porCasa[householdID], account.Account{
		ID: id, HouseholdID: householdID, Name: nome,
	})
}

// --- transação e auditoria --------------------------------------------------

// txFake executa a função na hora e registra se houve rollback. Ele NÃO
// desfaz as escritas do ledgerFake: o que os testes afirmam sobre o rollback é
// que o erro sobe e que a auditoria não foi gravada.
type txFake struct {
	chamadas int
	erro     error

	// antes roda DENTRO da transação, imediatamente antes da função de
	// escrita. É como se simula a corrida real desta rota: o cálculo acontece
	// FORA da transação (achado A2), então entre a prévia e o UPDATE cabe
	// outra escrita — alguém categorizando a mesma linha à mão, por exemplo.
	// Sem este gancho, a corrida só seria testável por sorte de escalonamento.
	antes func()
}

func (t *txFake) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	t.chamadas++
	if t.erro != nil {
		return t.erro
	}
	if t.antes != nil {
		t.antes()
	}
	return fn(ctx)
}

type auditoriaFake struct {
	entradas []investment.AuditParams
	erro     error
}

func (a *auditoriaFake) Record(_ context.Context, p investment.AuditParams) error {
	if a.erro != nil {
		return a.erro
	}
	a.entradas = append(a.entradas, p)
	return nil
}

// --- montagem ---------------------------------------------------------------

// cenario junta as peças de um teste. O classificador é o loader REAL
// (classify.NewLoader) sobre os fakes de categoria e conta: o algoritmo da
// spec 0005 §3 — limiar 80, desempate, ambiguidade — é exercitado de verdade,
// e não simulado por um mock que sempre concorda com o teste.
type cenario struct {
	ledger     *ledgerFake
	categorias *categoriasFake
	contas     *contasFake
	tx         *txFake
	auditoria  *auditoriaFake
	svc        *investment.Service
}

func montar(opts ...investment.Option) *cenario {
	return montarCom(nil, opts...)
}

// montarCom é montar com um logger explícito — o mesmo que o handler recebe,
// para que o aviso agregado do serviço caia no mesmo buffer que o teste lê.
func montarCom(lg *slog.Logger, opts ...investment.Option) *cenario {
	c := &cenario{
		ledger:     novoLedger(),
		categorias: novasCategorias(),
		contas:     novasContas(),
		tx:         &txFake{},
		auditoria:  &auditoriaFake{},
	}
	loader := classify.NewLoader(c.categorias, c.contas)
	opcoes := append([]investment.Option{
		investment.WithAudit(c.auditoria),
		investment.WithClock(func() time.Time { return relogioFixo }),
	}, opts...)
	c.svc = investment.NewService(c.ledger, c.categorias, c.contas, loader, c.tx, lg, opcoes...)
	return c
}

var relogioFixo = time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)

func ator(householdID string) investment.Actor {
	return investment.Actor{HouseholdID: householdID, UserID: "user-1", IP: "203.0.113.7"}
}

// lanc monta um lançamento pronto para o fake. `categoria` vazia é "sem
// categoria".
func lanc(householdID, id, kind, descricao string, cents int64, dia int, mes, categoria string) transaction.Transaction {
	t := transaction.Transaction{
		ID:              id,
		HouseholdID:     householdID,
		Kind:            kind,
		AccountID:       "acc-1",
		AmountCents:     cents,
		Description:     descricao,
		DescriptionNorm: textnorm.Normalize(descricao),
		OccurredOn:      civil.MustNew(anoDe(mes), mesDe(mes), dia),
		CompetenceMonth: mes,
		Source:          transaction.SourceImport,
	}
	if categoria != "" {
		id := categoria
		t.CategoryID = &id
	}
	return t
}

// uuidDe devolve um id na FORMA canônica de UUID. O cursor da listagem valida
// a forma do id (transaction.ParseCursor), então id de brinquedo como "tx-1"
// faz a segunda página falhar — e falhar por um detalhe do teste, não do
// código, é o jeito mais rápido de alguém "consertar" o teste errado.
func uuidDe(n int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
}

func anoDe(mes string) int {
	return int(mes[0]-'0')*1000 + int(mes[1]-'0')*100 + int(mes[2]-'0')*10 + int(mes[3]-'0')
}
func mesDe(mes string) int { return int(mes[5]-'0')*10 + int(mes[6]-'0') }
