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

// categoryAccountTotalRow é a projeção de SumByCategoryAndAccount: uma linha
// por (categoria, conta).
//
// CategoryID é ponteiro para receber a linha em que category_id IS NULL — os
// quatro dialetos agrupam os nulos numa linha só, e é ela que vira o balde
// "Sem categoria". AccountID é `string`, como o kindAccountRow do painel:
// `transactions.account_id` é NOT NULL — todo lançamento tem conta —, então
// aqui não existe linha de nulos.
type categoryAccountTotalRow struct {
	CategoryID *string
	AccountID  string
	Cnt        int64
	Total      int64
}

// Projeção do relatório por categoria. CONSTANTE de compilação, como a do
// painel (dashboardProjecao): nenhum pedaço dela vem de fora, e os três
// filtros entram por placeholder `?`, nunca no texto (docs/SEGURANCA.md §3,
// TestSemSQLMontadoNoGormstore).
const reportProjecao = "category_id AS category_id, account_id AS account_id, " +
	"COUNT(*) AS cnt, COALESCE(SUM(amount_cents), 0) AS total"

// SumByCategoryAndAccount soma, por (categoria, conta), os lançamentos VIVOS
// da casa no mês de competência com a natureza pedida (ADR-027b, ADR-032).
//
// A consulta é UM `GROUP BY category_id, account_id` sobre a janela
// (household_id, competence_month, kind, deleted_at IS NULL) — SQL ANSI igual
// nos quatro dialetos, servida por ix_transactions_competence, com TRÊS
// variáveis de bind e nenhum `IN`, `JOIN` ou `ORDER BY`. Sem LIMIT: LIMIT em
// GROUP BY sem ORDER BY é não determinístico, e a saída já é limitada pela
// estrutura (≤ (category.MaxPerHousehold + 1) × account.MaxPerHousehold
// linhas), não pelo volume.
//
// # Por que a CONTA vai na CHAVE de agrupamento, e o id de conta nunca no SQL
//
// É a mesma decisão do painel (ADR-031b), pelos mesmos dois motivos medidos:
// filtrar por `account_id IN (?)`/`NOT IN (?)` com os cartões da casa poria
// até 50 ids em cada consulta — e o recorte `debit`, sendo o complemento,
// precisaria de `NOT IN`, que estoura o orçamento de parâmetros do dialeto
// mais estreito quando somado ao resto e ainda obrigaria DUAS consultas para
// os dois recortes, que uma escrita concorrente poderia fazer discordar.
// Com a conta na chave a consulta é UMA, sempre a mesma, e `credit`/`debit`
// viram PARTIÇÕES das mesmas linhas, feitas em Go pelo serviço sobre a lista
// de contas da própria casa: `total(credit) + total(debit) == total(todas)`
// vale por construção. O preço é uma linha por (categoria, conta) em vez de
// uma por categoria, e ele é barato — quem dobra é o serviço.
//
// As três guardas de vazio são a mesma lição de SumByAccountUntil:
// `competence_month = ”` ou `kind = ”` devolveriam conjunto vazio em
// silêncio — um mês de relatório zerado sem nenhum erro para avisar —, e
// household_id vazio é a porta do BOLA. A natureza é conferida por allowlist
// no serviço ANTES de chegar aqui; o placeholder `?` é o que a leva ao banco.
func (r *TransactionRepository) SumByCategoryAndAccount(ctx context.Context, householdID, competenceMonth, kind string) ([]report.CategoryAccountTotal, error) {
	if householdID == "" || competenceMonth == "" || kind == "" {
		return nil, fmt.Errorf("soma por categoria e conta exige casa, competência e natureza")
	}

	var rows []categoryAccountTotalRow
	err := r.scope(ctx, householdID).
		Where("competence_month = ?", competenceMonth).
		Where("kind = ?", kind).
		Select(reportProjecao).
		Group("category_id").
		Group("account_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("somando lançamentos por categoria e conta: %w", err)
	}

	out := make([]report.CategoryAccountTotal, 0, len(rows))
	for i := range rows {
		out = append(out, report.CategoryAccountTotal{
			CategoryID: rows[i].CategoryID,
			AccountID:  rows[i].AccountID,
			TotalCents: rows[i].Total,
			Count:      rows[i].Cnt,
		})
	}
	return out, nil
}
