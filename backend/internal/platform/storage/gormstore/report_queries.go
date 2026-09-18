package gormstore

import (
	"context"
	"fmt"

	"github.com/brunorblanck/homefinance/backend/internal/report"
)

// Este arquivo reúne as consultas que servem os RELATÓRIOS (ADR-027): leituras
// agregadas sobre lançamentos, expostas ao pacote report pelas interfaces que
// ele declara. Nenhuma escrita.

var _ report.Ledger = (*TransactionRepository)(nil)

// categoryTotalRow é a projeção de SumByCategory. CategoryID é ponteiro para
// receber a linha em que category_id IS NULL — os quatro dialetos agrupam os
// nulos numa linha só, e é ela que vira o balde "Sem categoria".
type categoryTotalRow struct {
	CategoryID *string
	Cnt        int64
	Total      int64
}

// SumByCategory soma, por categoria, os lançamentos VIVOS da casa no mês de
// competência com a natureza pedida (ADR-027b).
//
// A consulta é UM `GROUP BY category_id` sobre a janela (household_id,
// competence_month, kind, deleted_at IS NULL) — SQL ANSI igual nos quatro
// dialetos, servida por ix_transactions_competence. Sem LIMIT: LIMIT em
// GROUP BY sem ORDER BY é não determinístico, e a saída já é limitada pela
// taxonomia (≤ category.MaxPerHousehold + 1 linhas), não pelo volume.
//
// As três guardas de vazio são a mesma lição de SumByAccountUntil:
// `competence_month = ”` ou `kind = ”` devolveriam conjunto vazio em
// silêncio — um mês de relatório zerado sem nenhum erro para avisar —, e
// household_id vazio é a porta do BOLA. A natureza é conferida por allowlist
// no serviço ANTES de chegar aqui; o placeholder `?` é o que a leva ao banco.
func (r *TransactionRepository) SumByCategory(ctx context.Context, householdID, competenceMonth, kind string) ([]report.CategoryTotal, error) {
	if householdID == "" || competenceMonth == "" || kind == "" {
		return nil, fmt.Errorf("soma por categoria exige casa, competência e natureza")
	}

	var rows []categoryTotalRow
	err := r.scope(ctx, householdID).
		Where("competence_month = ?", competenceMonth).
		Where("kind = ?", kind).
		Select("category_id AS category_id, COUNT(*) AS cnt, COALESCE(SUM(amount_cents), 0) AS total").
		Group("category_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("somando lançamentos por categoria: %w", err)
	}

	out := make([]report.CategoryTotal, 0, len(rows))
	for i := range rows {
		out = append(out, report.CategoryTotal{
			CategoryID: rows[i].CategoryID,
			TotalCents: rows[i].Total,
			Count:      rows[i].Cnt,
		})
	}
	return out, nil
}
