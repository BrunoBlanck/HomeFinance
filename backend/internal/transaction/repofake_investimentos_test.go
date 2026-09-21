package transaction_test

import (
	"context"
	"sort"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Este arquivo completa o repoFake com os métodos da spec 0006 (ADR-029).
//
// Fica em arquivo próprio, e não no meio de service_test.go, por dois motivos:
// o fake já é grande, e as quatro consultas de investimento têm uma regra
// comum que merece ser lida junta — o conjunto de categorias VAZIO é erro, e
// nunca "todas as categorias". É essa regra que obriga o serviço ao
// curto-circuito em Go (ADR-029f), e um fake que devolvesse "tudo" com filtro
// vazio esconderia exatamente o defeito que ela existe para impedir.
//
// Os métodos reproduzem o contrato do repositório real no que importa para o
// serviço: escopo por casa, só linhas vivas, só receita e despesa, e a
// allowlist de SetCategoryWhereCurrentIn conferida sobre a categoria ATUAL.
//
// Os QUATRO registram o householdID recebido em `casasConsultadas` (o mesmo
// campo do reprocessamento de transferências), e é sobre essa contagem que os
// testes de isolamento e de curto-circuito de `investimentos_isolamento_test.go`
// são escritos. A razão é que "não veio nada" e "não foi perguntado" são fatos
// diferentes: um teste que só olhasse o resultado vazio passaria igual se a
// consulta tivesse ido ao banco — que é exatamente o que o ADR-029(f) proíbe.

// vivasDaCasa devolve as linhas vivas da casa em ordem (occurred_on, id)
// crescente — a ordem que as leituras do repositório real usam como base.
func (r *repoFake) vivasDaCasa(householdID string) []transaction.Transaction {
	out := make([]transaction.Transaction, 0, len(r.ordem))
	for _, id := range r.ordem {
		t := r.linhas[id]
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

// conjunto transforma a lista de ids em conjunto, descartando vazios — o mesmo
// que dedupeStrings faz no repositório real antes de montar o IN (...).
func conjunto(ids []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		out[id] = struct{}{}
	}
	return out
}

func categorizavel(t transaction.Transaction) bool {
	return t.Kind == transaction.KindIncome || t.Kind == transaction.KindExpense
}

func (r *repoFake) SumInvestmentsByMonth(_ context.Context, householdID string, categoryIDs []string, fromMonth, toMonth string) ([]transaction.InvestmentMonthTotals, error) {
	r.casasConsultadas = append(r.casasConsultadas, householdID)
	marcadas := conjunto(categoryIDs)
	if len(marcadas) == 0 {
		return nil, transaction.ErrEmptyCategoryFilter
	}
	if len(marcadas) > 200 {
		return nil, transaction.ErrTooManyCategories
	}

	porMes := map[string]*transaction.InvestmentMonthTotals{}
	var meses []string
	for _, t := range r.vivasDaCasa(householdID) {
		if !categorizavel(t) || t.CategoryID == nil {
			continue
		}
		if _, ok := marcadas[*t.CategoryID]; !ok {
			continue
		}
		// "YYYY-MM" tem largura fixa: ordem de texto é ordem cronológica.
		if t.CompetenceMonth < fromMonth || t.CompetenceMonth > toMonth {
			continue
		}
		item, ok := porMes[t.CompetenceMonth]
		if !ok {
			item = &transaction.InvestmentMonthTotals{Month: t.CompetenceMonth}
			porMes[t.CompetenceMonth] = item
			meses = append(meses, t.CompetenceMonth)
		}
		// O fluxo vem do kind do LANÇAMENTO (ADR-029d).
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
	for _, mes := range meses {
		out = append(out, *porMes[mes])
	}
	return out, nil
}

func (r *repoFake) ListByCategories(_ context.Context, householdID string, f transaction.CategoryListFilter) ([]transaction.Transaction, error) {
	r.casasConsultadas = append(r.casasConsultadas, householdID)
	marcadas := conjunto(f.CategoryIDs)
	if len(marcadas) == 0 {
		return nil, transaction.ErrEmptyCategoryFilter
	}
	if len(marcadas) > 200 {
		return nil, transaction.ErrTooManyCategories
	}

	candidatas := r.vivasDaCasa(householdID)
	// A listagem é do mais recente para o mais antigo, como GET /transactions.
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

func (r *repoFake) ListIncomeExpenseOfMonth(_ context.Context, householdID, competenceMonth string, limit int) ([]transaction.CategorizableRow, error) {
	r.casasConsultadas = append(r.casasConsultadas, householdID)
	var out []transaction.CategorizableRow
	for _, t := range r.vivasDaCasa(householdID) {
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

func (r *repoFake) SetCategoryWhereCurrentIn(_ context.Context, householdID string, ids, currentCategoryIDs []string, categoryID string, at time.Time) (int64, error) {
	r.casasConsultadas = append(r.casasConsultadas, householdID)
	permitidas := conjunto(currentCategoryIDs)
	if len(permitidas) == 0 {
		return 0, transaction.ErrEmptyCategoryFilter
	}
	if len(permitidas) > 200 {
		return 0, transaction.ErrTooManyCategories
	}

	var afetadas int64
	for _, id := range ids {
		t, ok := r.linhas[id]
		if !ok || t.HouseholdID != householdID || t.DeletedAt != nil || !categorizavel(t) {
			continue
		}
		// Linha sem categoria não é alcançada (NULL nunca está num IN), e
		// linha JÁ MARCADA como investimento também não: a natureza dela não
		// está na allowlist. É a regra que mora no WHERE do repositório real.
		if t.CategoryID == nil {
			continue
		}
		if _, ok := permitidas[*t.CategoryID]; !ok {
			continue
		}
		nova := categoryID
		t.CategoryID = &nova
		t.UpdatedAt = at
		r.linhas[id] = t
		afetadas++
	}
	return afetadas, nil
}
