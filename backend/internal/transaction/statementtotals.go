package transaction

import (
	"context"

	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
)

// StatementTotals liga os lançamentos à fatura: é ele que responde quanto a
// fatura cobra e quanto já foi pago (ADR-023d).
//
// Por que o adaptador mora AQUI e não lá: a fatura declara a interface que
// consome (cardstatement.Lines), como todo consumidor neste projeto, e quem
// sabe somar lançamento é este pacote. Se a conversão morasse no pacote de
// faturas, ele precisaria importar este — e este já importa aquele, para
// validar a fatura de um lançamento. Duas setas em sentidos opostos entre dois
// pacotes é um ciclo, e o compilador recusa.
//
// A conversão em si é boba de propósito: dois structs com os mesmos três
// campos. É o preço de cada pacote declarar o que consome, e é barato perto da
// alternativa — um tipo compartilhado num pacote "common" que, com o tempo,
// vira o lugar onde tudo mora.
type StatementTotals struct{ repo Repository }

// NewStatementTotals monta o adaptador.
func NewStatementTotals(repo Repository) StatementTotals { return StatementTotals{repo: repo} }

var _ cardstatement.Lines = StatementTotals{}

// TotalsByStatement devolve os números derivados das faturas informadas.
//
// O householdID vem de quem chama — que o tirou do token — e desce até o WHERE
// do repositório: fatura de outra casa simplesmente não tem linha para somar
// aqui, mesmo que alguém consiga o id dela.
func (t StatementTotals) TotalsByStatement(ctx context.Context, householdID string, statementIDs []string) (map[string]cardstatement.Totals, error) {
	somas, err := t.repo.SumByStatement(ctx, householdID, statementIDs)
	if err != nil {
		return nil, err
	}

	out := make(map[string]cardstatement.Totals, len(somas))
	for id, s := range somas {
		out[id] = cardstatement.Totals{
			TotalCents: s.TotalCents,
			PaidCents:  s.PaidCents,
			LineCount:  s.LineCount,
		}
	}
	return out, nil
}
