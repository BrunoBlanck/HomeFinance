package gormstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/aiprompt"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Este arquivo reúne as consultas que servem o MENU IA (spec 0010, E9a):
// leitura agregada sobre lançamentos, exposta aos pacotes `aiprompt` e
// `aiimport` pelas interfaces que ELES declaram. Nenhuma escrita — a E9a não
// tem `UnitOfWork` nem `Auditor`, e este arquivo é uma das razões de isso ser
// verificável em vez de prometido.
//
// O arranjo é o mesmo de report_queries.go e dashboard_queries.go (ADR-027a):
// o método é do TransactionRepository e NÃO entra na interface
// transaction.Repository — quem precisa dele é o menu IA, e é ele que diz do
// que precisa.
//
// A asserção de interface mora aqui, como a de `dashboard.Ledger` em
// dashboard_queries.go: quem tem de quebrar o build quando a assinatura mudar
// é o IMPLEMENTADOR, não o consumidor.

var _ aiprompt.Ledger = (*TransactionRepository)(nil)

// descriptionGroupRow é a projeção de GroupByDescription: uma linha por
// (descrição normalizada, kind, conta, categoria).
//
// CategoryID é ponteiro para receber a linha em que category_id IS NULL, como
// o categoryAccountTotalRow do relatório. AccountID é `string`:
// `transactions.account_id` é NOT NULL, então aqui não existe linha de nulos.
type descriptionGroupRow struct {
	DescriptionNorm string
	Sample          string
	Kind            string
	AccountID       string
	CategoryID      *string
	Cnt             int64
	Total           int64
}

// Projeção e ordenação do agrupamento por descrição. As duas são CONSTANTES
// de compilação, como as do painel e as do relatório: nenhum pedaço delas vem
// de fora, e casa, meses e limite entram por placeholder `?`, nunca no texto
// (docs/SEGURANCA.md §3, TestSemSQLMontadoNoGormstore).
const (
	aiDescriptionGroupProjecao = "description_norm AS description_norm, " +
		"MIN(description) AS sample, kind AS kind, account_id AS account_id, " +
		"category_id AS category_id, COUNT(*) AS cnt, " +
		"COALESCE(SUM(amount_cents), 0) AS total"

	// Ordenação por COUNT(*), que é INTEIRO — e é isso que a torna aceitável
	// aqui. Ver o comentário de GroupByDescription: ela existe porque o
	// dialeto MSSQL EXIGE um ORDER BY para emitir o LIMIT, e ordenar por
	// coluna de texto traria a collation de volta ao caminho.
	aiDescriptionGroupOrdem = "cnt DESC"
)

// GroupByDescription agrupa os lançamentos VIVOS da casa, na janela de
// competência pedida, por (descrição normalizada, kind, conta, categoria) —
// a matéria-prima do item 8 do prompt da spec 0010 §3.1.
//
// # `IN (?, ?, ?)`, e não `BETWEEN`
//
// `competence_month` é `varchar(7)`, não data. Uma FAIXA sobre coluna de texto
// (`BETWEEN '2026-07' AND '2026-09'`) depende da COLLATION para decidir o que
// é "entre", e as quatro collations padrão do projeto não são a mesma coisa —
// a de MySQL é insensível a caixa, a do MSSQL depende da instalação. Com a
// janela travada em MaxCompetenceMonthsInWindow (3, decisão do usuário de
// 21/09/2026), a faixa cabe inteira em IGUALDADES: `IN` compara por igualdade
// em qualquer collation, e o formato "YYYY-MM" é fixo, então nenhuma delas tem
// oportunidade de discordar. É a mesma escolha que o resto do projeto já faz
// com mês: o mês chega PRONTO da aplicação, e nenhuma função de data entra na
// consulta (armadilha P1 — extrair mês tem quatro sintaxes).
//
// # O LIMIT, o ORDER BY e o que o MSSQL impõe — medido em 21/09/2026
//
// A intenção original era `LIMIT` **sem** `ORDER BY`, para tirar a collation
// do caminho. Ela NÃO SE SUSTENTA nos quatro dialetos, e o desvio está
// reportado em docs/BANCO-DE-DADOS.md em vez de implementado em silêncio:
// T-SQL não tem `LIMIT`, e o driver `gorm.io/driver/sqlserver` o traduz para
// `OFFSET … FETCH NEXT`, que EXIGE `ORDER BY`. Quando não há um, o driver
// INVENTA `ORDER BY "id"` (sqlserver.go:77-85) — e `id` não está no `GROUP BY`
// nem dentro de agregado, o que é erro 8127 do SQL Server. O SQL gerado foi
// medido nos quatro dialetos (DryRun) e está na tabela do documento.
//
// `ORDER BY cnt DESC` resolve os dois lados: `cnt` é `COUNT(*)`, INTEIRO, sem
// collation nenhuma — a razão de ser do "sem ORDER BY" continua honrada — e o
// MSSQL passa a emitir `ORDER BY cnt DESC OFFSET 0 ROW FETCH NEXT ? ROWS
// ONLY`, que é válido.
//
// # Por que o LIMIT aqui não repete o erro que report_queries.go recusa
//
// O comentário de SumByCategoryAndAccount (report_queries.go:44-47) diz que
// "LIMIT em GROUP BY sem ORDER BY é não determinístico", e por isso lá não há
// LIMIT. Aqui há, e as duas decisões são compatíveis por DOIS motivos, nesta
// ordem:
//
//  1. o `ORDER BY cnt DESC` acima torna o corte determinístico POR POSTO: o
//     que ficaria de fora são as descrições menos frequentes, que é
//     exatamente a ordem de corte que a spec 0010 §3.1 manda usar;
//  2. e, principalmente, o corte NUNCA É ENTREGUE. A semântica é TUDO OU
//     NADA: pedimos `limit + 1` linhas e, se a linha extra vier, jogamos as
//     `limit + 1` fora e devolvemos ErrTooManyDescriptionGroups, que o serviço
//     traduz em 422. Uma resposta PARCIAL é o único resultado de verdade ruim
//     aqui — um prompt que parece completo e não é faria a IA propor
//     palavra-chave para metade da casa sem ninguém saber.
//
// A saída do relatório é limitada pela ESTRUTURA (categorias × contas), e por
// isso lá o volume não pode estourá-la. Esta aqui é limitada pelo VOLUME: o
// número de descrições distintas cresce com a vida financeira da casa e não
// tem teto de domínio nenhum. É essa diferença — e não gosto — que faz uma
// precisar de rail e a outra não.
//
// O `LIMIT` não economiza trabalho do BANCO, e isso também foi medido: o plano
// é `USE TEMP B-TREE FOR GROUP BY`, e um b-tree temporário tem de ser
// concluído antes da primeira linha sair. O que ele prende é o que atravessa o
// driver e vira heap em Go — que é justamente o que precisa de rail.
//
// # Ordenação de verdade é em Go
//
// A ordenação final por ocorrências (spec 0010 §3.1 item 8) é do SERVIÇO, em
// Go, sobre no máximo MaxDescriptionGroupRows linhas: `ORDER BY cnt DESC`
// sozinho não desempata, e desempate estável é o que faz o mesmo banco gerar o
// mesmo prompt duas vezes.
//
// # Guardas
//
// Casa vazia é a porta do BOLA. Mês vazio devolveria conjunto vazio em
// silêncio — um prompt sem movimentação nenhuma, sem nenhum erro para avisar —,
// a mesma lição de SumByAccountUntil e de SumByCategoryAndAccount. Janela
// acima de 3 meses e limite fora da faixa são recusados AQUI, e não só na
// borda: o rail que protege a memória do processo não pode depender de quem
// chama lembrar dele.
func (r *TransactionRepository) GroupByDescription(ctx context.Context, householdID string,
	competenceMonths []string, limit int,
) ([]transaction.DescriptionGroup, error) {
	if householdID == "" {
		return nil, fmt.Errorf("agrupamento por descrição exige casa")
	}
	if len(competenceMonths) == 0 {
		return nil, fmt.Errorf("agrupamento por descrição exige ao menos um mês de competência")
	}
	if len(competenceMonths) > transaction.MaxCompetenceMonthsInWindow {
		return nil, fmt.Errorf("janela de competência com %d meses passa do máximo de %d",
			len(competenceMonths), transaction.MaxCompetenceMonthsInWindow)
	}
	for _, m := range competenceMonths {
		if strings.TrimSpace(m) == "" {
			return nil, fmt.Errorf("agrupamento por descrição não aceita mês vazio na janela")
		}
	}
	if limit <= 0 || limit > transaction.MaxDescriptionGroupRows {
		return nil, fmt.Errorf("limite %d fora da faixa 1..%d", limit, transaction.MaxDescriptionGroupRows)
	}

	var rows []descriptionGroupRow
	err := r.scope(ctx, householdID).
		Where("competence_month IN ?", competenceMonths).
		Select(aiDescriptionGroupProjecao).
		Group("description_norm").
		Group("kind").
		Group("account_id").
		Group("category_id").
		Order(aiDescriptionGroupOrdem).
		Limit(limit + 1).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("agrupando lançamentos por descrição: %w", err)
	}

	// Tudo ou nada: a linha extra existe só para DETECTAR o estouro, e nada
	// do que veio junto com ela é publicável.
	if len(rows) > limit {
		return nil, fmt.Errorf("%w: mais de %d grupos em %d mês(es) de competência",
			transaction.ErrTooManyDescriptionGroups, limit, len(competenceMonths))
	}

	out := make([]transaction.DescriptionGroup, 0, len(rows))
	for i := range rows {
		out = append(out, transaction.DescriptionGroup{
			DescriptionNorm:   rows[i].DescriptionNorm,
			SampleDescription: rows[i].Sample,
			Kind:              rows[i].Kind,
			AccountID:         rows[i].AccountID,
			CategoryID:        rows[i].CategoryID,
			Count:             rows[i].Cnt,
			TotalCents:        rows[i].Total,
		})
	}
	return out, nil
}
