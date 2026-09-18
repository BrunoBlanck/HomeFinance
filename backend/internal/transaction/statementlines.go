package transaction

import (
	"context"
	"errors"

	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
)

// StatementLines liga a página de detalhe da fatura aos lançamentos dela.
//
// Por que o adaptador mora AQUI, e não lá: o pacote de faturas declara a
// interface que consome (como todo consumidor neste projeto), e quem sabe
// listar lançamento é este pacote. Se a conversão morasse no pacote de faturas,
// ele precisaria importar este — e este já importa aquele, para validar a
// fatura de um lançamento. Duas setas em sentidos opostos entre dois pacotes é
// um ciclo, e o compilador recusa. É o mesmo desenho de StatementTotals.
type StatementLines struct{ svc *Service }

// NewStatementLines monta o adaptador.
func NewStatementLines(svc *Service) StatementLines { return StatementLines{svc: svc} }

// ByStatement devolve os lançamentos da fatura já no DTO da API, paginados
// pelo MESMO cursor de GET /transactions — um só formato de cursor no app
// inteiro.
//
// O householdID vem de quem chama (que o tirou do token) e desce até o WHERE do
// repositório; a fatura é conferida como da casa antes de qualquer leitura de
// linha, então fatura de outra casa devolve ErrNotFound e nunca uma lista.
//
// O retorno é []any de propósito, e é o único lugar do projeto onde isso
// acontece: o pacote de faturas não conhece o DTO de lançamento e não deve
// conhecê-lo — ele só o repassa para o JSON. A alternativa seria espelhar a
// struct de vinte campos do schema Transaction lá, e aí o contrato divergiria
// na primeira coluna nova.
func (l StatementLines) ByStatement(ctx context.Context, householdID, statementID, cursor string, limit int) ([]any, *string, error) {
	itens, proximo, err := l.svc.ListByStatement(ctx, Actor{HouseholdID: householdID}, statementID, cursor, limit)
	if err != nil {
		// A tradução acontece AQUI porque este é o único lugar que conhece os
		// dois vocabulários. Sem ela o handler de faturas receberia um erro
		// que não sabe nomear e responderia 500 a um cursor malformado.
		return nil, nil, traduzirParaFatura(err)
	}
	out := make([]any, 0, len(itens))
	for i := range itens {
		out = append(out, itens[i])
	}
	return out, proximo, nil
}

// traduzirParaFatura converte o erro do domínio de lançamentos para o
// vocabulário do domínio de faturas.
//
// Só os dois que a página de detalhe pode provocar: fatura de outra casa (ou
// inexistente) e cursor malformado. Qualquer outro passa intacto e vira 500 com
// o detalhe só no log, que é o tratamento certo para falha de verdade.
func traduzirParaFatura(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return cardstatement.ErrNotFound
	case errors.Is(err, ErrInvalidCursor):
		return cardstatement.ErrInvalidCursor
	default:
		return err
	}
}
