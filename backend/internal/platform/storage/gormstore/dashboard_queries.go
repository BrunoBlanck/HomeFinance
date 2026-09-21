package gormstore

import (
	"context"
	"fmt"

	"github.com/brunorblanck/homefinance/backend/internal/dashboard"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Este arquivo reúne as consultas que servem o PAINEL (spec 0008, ADR-031):
// leitura agregada sobre lançamentos, exposta ao pacote dashboard pela
// interface que ELE declara. Nenhuma escrita.
//
// O arranjo é o mesmo de report_queries.go (ADR-027a): o método é do
// TransactionRepository e NÃO entra na interface transaction.Repository —
// quem precisa dele é o painel, e é o painel que diz do que precisa.

var _ dashboard.Ledger = (*TransactionRepository)(nil)

// kindAccountRow é a projeção de SumMonthByKindAndAccount: uma linha por
// (kind, conta).
//
// AccountID é `string` e não ponteiro, ao contrário do CategoryID de
// SumByCategory: `transactions.account_id` é NOT NULL — todo lançamento tem
// conta —, então aqui não existe a linha dos nulos que lá vira o balde
// "Sem categoria".
type kindAccountRow struct {
	Kind        string
	AccountID   string
	Cnt         int64
	Total       int64
	MarkedTotal int64
	MarkedCnt   int64
}

// Projeção do painel. As duas são CONSTANTES de compilação, como as do
// Summary: a segunda é a primeira mais duas colunas, e nenhum pedaço de
// nenhuma das duas vem de fora — os ids entram por placeholder `?`, nunca no
// texto (docs/SEGURANCA.md §3, TestSemSQLMontadoNoGormstore).
//
// A segunda tem DOIS `?`, e os dois recebem o MESMO conjunto: o dinheiro
// marcado e a contagem marcada são perguntas diferentes sobre as MESMAS
// linhas, e é por saírem da mesma varredura que elas não podem discordar.
const (
	dashboardProjecao = "kind AS kind, account_id AS account_id, COUNT(*) AS cnt, " +
		"COALESCE(SUM(amount_cents), 0) AS total"

	dashboardProjecaoComMarcados = dashboardProjecao +
		", COALESCE(SUM(CASE WHEN category_id IN (?) THEN amount_cents ELSE 0 END), 0) AS marked_total" +
		", SUM(CASE WHEN category_id IN (?) THEN 1 ELSE 0 END) AS marked_cnt"
)

// SumMonthByKindAndAccount agrega, por (kind, conta), os lançamentos VIVOS da
// casa no mês de COMPETÊNCIA, lendo apenas `income` e `expense`.
//
// UMA consulta para os TRÊS números da faixa do painel (receita, gasto no
// cartão e investido líquido). Três consultas — uma por número — varreriam o
// mesmo índice três vezes e, pior, poderiam DISCORDAR entre si se uma escrita
// entrasse no meio: a receita sairia sem os resgates de uma leitura e o
// líquido com os resgates de outra, e a mesma tela mostraria dois dinheiros.
//
// # Por que a CONTA vai na CHAVE de agrupamento, e não na projeção
//
// Esta é a decisão que alguém vai querer "simplificar" daqui a seis meses,
// então ela está escrita: o rascunho §5 da spec previa `GROUP BY kind` com o
// cartão como uma TERCEIRA coluna condicional (`SUM(CASE WHEN account_id IN
// (?) …)`), e essa forma foi REJEITADA por dois motivos MEDIDOS — não por
// gosto.
//
//  1. **Perderia dinheiro em silêncio.** Com o cartão na projeção seria
//     preciso uma coluna de INTERSEÇÃO (cartão ∧ marcado), e a alternativa
//     tentadora — `account_id IN (?) AND (category_id NOT IN (?))` — apaga
//     TODA despesa de cartão SEM categoria nos quatro dialetos:
//     `NULL NOT IN (…)` avalia para NULL (lógica de três valores do SQL-92),
//     `CASE WHEN NULL` cai no `ELSE`, e o `WHERE` só deixa passar o que é
//     VERDADEIRO. Sem categoria é o estado NORMAL logo depois de importar uma
//     fatura, ou seja: o gasto do cartão apareceria menor exatamente no mês em
//     que o usuário acabou de importar o cartão. A guarda que salva a forma
//     rejeitada é `category_id IS NULL OR …` (ADR-029/E2d, provada em
//     TestNullNotInNaoPassaNoWhereNesteDialeto) — e depender dela para o
//     dinheiro do painel é confiar que ninguém vai "limpar" o OR.
//     Com a conta na CHAVE o problema deixa de existir: a linha sem categoria
//     cai no `ELSE` da coluna MARCADA (que é o desejado — ela não é aporte) e
//     continua inteira em `COUNT(*)` e em `SUM(amount_cents)`, que é de onde
//     sai o gasto bruto do cartão.
//
//  2. **Estouraria o orçamento de parâmetros.** Três pares de colunas
//     condicionais dão `2 (casa + mês) + 4 × 200 (categorias) + 4 × 50
//     (contas) = 1002` parâmetros — ACIMA do piso histórico de 999 do SQLite,
//     que o projeto adota como teto de portabilidade. Funcionaria em
//     PostgreSQL e MySQL e estouraria dentro do driver nos outros dois: o tipo
//     de defeito que só aparece em produção. Com a conta na chave o pior caso
//     cai para **404** (200 + 200 + casa + mês + 2 kinds), e os ids de CONTA
//     nunca entram no SQL — o conjunto de cartões é interseccionado em Go,
//     sobre a lista de contas da própria casa.
//
// O preço é uma linha por (kind, conta) em vez de uma por kind, e ele é
// barato: a saída é ≤ 2 × account.MaxPerHousehold (100) linhas, e o teto é
// REAL porque conta com qualquer lançamento não pode ser excluída (o
// UsageChecker responde 422) — toda conta que aparece na agregação é viva e da
// casa. Quem dobra as ≤ 100 linhas nos três números é o SERVIÇO.
//
// # O que torna a consulta portátil
//
// `SUM`, `COUNT`, `CASE WHEN`, `COALESCE` e `IN` com lista parametrizada são
// ANSI, idênticos nos quatro dialetos; não há nenhuma função de data (extrair
// mês tem quatro sintaxes — armadilha P1), porque `competence_month` já chega
// pronto da aplicação. **Sem `ORDER BY`**: a dobra é em Go sobre ≤ 100 linhas,
// e isso tira do caminho a última diferença que sobraria entre os dialetos
// (collation). Entra por ix_transactions_competence (household_id,
// competence_month) — NENHUM índice novo.
//
// A expressão condicional fica na PROJEÇÃO, e não no GROUP BY, pela restrição
// de sempre: `gorm.DB.Group` recebe `string` e não aceita variáveis de bind,
// então agrupar por ela obrigaria a escrever os ids no TEXTO do SQL (injeção),
// e agrupar por ALIAS não funciona em MSSQL nem em PostgreSQL.
//
// # `kind IN (income, expense)` é o que torna os critérios 6 e 7 estruturais
//
// A lista vem da constante categorizableKinds. Perna de transferência
// (`transfer_in`/`transfer_out`) nunca é LIDA — não é podada depois, em Go —,
// então nem o gasto do cartão nem a receita podem incluí-la (ADR-016), e
// pagar a fatura do cartão não mexe em número nenhum do painel.
//
// # Conjunto de categorias vazio NÃO é erro aqui
//
// Diferente de SumInvestmentsByMonth, em que vazio é ErrEmptyCategoryFilter
// porque vazio significaria "todas as categorias": aqui vazio significa "esta
// casa não marca investimento", que é o estado de toda casa no dia da entrega.
// As duas colunas condicionais simplesmente NÃO entram na consulta, e `IN ()`
// — erro de sintaxe em três dialetos e `1=0` no quarto — nunca é emitido
// (ADR-029f).
func (r *TransactionRepository) SumMonthByKindAndAccount(
	ctx context.Context, householdID, competenceMonth string, markedCategoryIDs []string,
) ([]dashboard.KindAccountTotals, error) {
	// As guardas vêm ANTES de qualquer SQL, no molde de SumByCategory:
	// `competence_month = ''` devolveria um mês inteiramente zerado em
	// silêncio — a tela diria "você não gastou nada" —, e casa vazia é a porta
	// do BOLA (docs/SEGURANCA.md §2).
	if householdID == "" || competenceMonth == "" {
		return nil, fmt.Errorf("resumo do painel exige a casa e a competência")
	}

	ids := dedupeStrings(markedCategoryIDs)
	// Acima do teto é ERRO ALTO, e não uma consulta fatiada: fatiar uma
	// agregação obrigaria a somar fatias de dinheiro. O serviço mapeia isto
	// para 500 genérico (ADR-029 j.2) — é o teto da própria taxonomia que foi
	// violado, não entrada do usuário, e falhar fechado é melhor do que um
	// comando que estoura dentro do driver em dois dialetos só.
	if len(ids) > maxCategoryFilterIDs {
		return nil, fmt.Errorf("resumindo o mês do painel: %w", transaction.ErrTooManyCategories)
	}

	q := r.scope(ctx, householdID).
		Where("competence_month = ?", competenceMonth).
		Where("kind IN ?", categorizableKinds)

	if len(ids) == 0 {
		q = q.Select(dashboardProjecao)
	} else {
		// O MESMO conjunto nos dois `?`: dinheiro marcado e contagem marcada
		// são duas perguntas sobre as MESMAS linhas.
		q = q.Select(dashboardProjecaoComMarcados, ids, ids)
	}

	var rows []kindAccountRow
	err := q.
		Group("kind").
		Group("account_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("resumindo o mês do painel: %w", err)
	}

	// A projeção sobe CRUA: nenhuma subtração acontece aqui. `total − marked`
	// é aritmética do SERVIÇO, e só depois de ele VERIFICAR a desigualdade
	// `0 ≤ marcado ≤ total` (ADR-029 j.1) — verificar, nunca confiar, porque
	// ela depende de `amount_cents ≥ 0`, que é invariante do caminho de
	// escrita e não do schema. Derivar no repositório esconderia a diferença
	// justamente de quem precisa vê-la.
	out := make([]dashboard.KindAccountTotals, 0, len(rows))
	for i := range rows {
		out = append(out, dashboard.KindAccountTotals{
			Kind:             rows[i].Kind,
			AccountID:        rows[i].AccountID,
			Count:            rows[i].Cnt,
			TotalCents:       rows[i].Total,
			MarkedCount:      rows[i].MarkedCnt,
			MarkedTotalCents: rows[i].MarkedTotal,
		})
	}
	return out, nil
}
