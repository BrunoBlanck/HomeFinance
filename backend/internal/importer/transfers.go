package importer

import (
	"slices"

	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Pareamento com a outra perna JÁ EXISTENTE (spec 0005 §4.2.1.2, estendida
// pela emenda §10.2): é o que transforma `transferencia_interna` (e
// `pagamento_de_fatura` com contraparte sugerida) em
// `transferencia_ja_registrada`.
//
// O caso que ele fecha: a pessoa importou o extrato de A e registrou o par
// A→B. Depois importa o extrato de B, onde a mesma transferência aparece como
// receita. Sem o pareamento, a revisão proporia um SEGUNDO par — a duplicata
// que a spec 0004 existe para impedir. Com ele, a linha aponta para a perna que
// já está em B, e a liberação é `link`: nada de movimento novo, só a chave.
//
// Puro, sem banco: as pernas já vêm carregadas
// (Ledger.TransferLegsForLinking, duas consultas por lote) e o resultado é
// testado por tabela.

// sugestaoDaLinha é o que a classificação por palavra-chave produziu para UMA
// linha aproveitada do arquivo (spec 0005 §4.2.1). Todos anuláveis: "sem
// sugestão" é o caso normal.
//
// Quando a linha é `transferencia_*`, Score e Keyword descrevem a CONTRAPARTE;
// quando há CategoryID, descrevem a categoria (emenda §10.4). Em
// `pagamento_de_fatura` com conta batendo, só CounterpartID é preenchido — a
// pontuação da contraparte não é exposta ali.
type sugestaoDaLinha struct {
	CategoryID    *string
	Score         *int
	Keyword       *string
	CounterpartID *string
}

// pairExistingLegs percorre as linhas em ordem de Seq e, para cada uma que
// participa (transferência interna, ou pagamento de fatura com contraparte
// sugerida), procura a perna existente que a espelha.
//
// Candidatas: pernas com Kind na DIREÇÃO da linha (`expense` → transfer_out,
// `income` → transfer_in), contraparte igual à sugerida, mesmo valor, data a
// até transaction.DedupWindowDays de distância e ainda não reivindicadas por
// linha anterior. Entre as candidatas, vence a de menor distância em dias;
// empate → menor OccurredOn; depois menor ID. A vencedora é REIVINDICADA:
// duas transferências iguais no mesmo dia casam com pernas distintas, e a
// terceira — sem perna sobrando — fica `transferencia_interna` (critério 6).
//
// Devolve seq -> id da perna, só para as linhas que casaram. Não altera os
// argumentos.
func pairExistingLegs(
	rows []ParsedRow,
	results []dedup.RowResult,
	sugestoes []sugestaoDaLinha,
	pernas []transaction.TransferLeg,
) map[int]string {
	out := make(map[int]string)
	if len(rows) == 0 || len(pernas) == 0 || len(results) != len(rows) || len(sugestoes) != len(rows) {
		// Comprimentos desiguais são defeito de programação do chamador: a
		// resposta segura é não parear nada, nunca parear pelo índice errado.
		return out
	}

	// Índice por (direção, contraparte, valor): um arquivo de 10.000 linhas
	// contra uma janela cheia de pernas não pode virar produto cartesiano.
	balde := make(map[chavePerna][]*candidataPerna, len(pernas))
	for i := range pernas {
		p := &pernas[i]
		if p.ID == "" || p.CounterpartAccountID == "" || p.OccurredOn.IsZero() {
			continue
		}
		k := chavePerna{kind: p.Kind, contraparte: p.CounterpartAccountID, cents: p.AmountCents}
		balde[k] = append(balde[k], &candidataPerna{perna: p})
	}

	// Ordem de Seq, e não de índice: é a ordem do arquivo, e é ela que decide
	// quem reivindica primeiro quando há mais linhas do que pernas.
	ordem := make([]int, len(rows))
	for i := range ordem {
		ordem[i] = i
	}
	slices.SortStableFunc(ordem, func(a, b int) int { return rows[a].Seq - rows[b].Seq })

	for _, i := range ordem {
		if results[i].Seq != rows[i].Seq {
			// Veredito e linha desalinhados: defeito do chamador. Parear pelo
			// índice errado vincularia a perna à linha errada — melhor não.
			continue
		}
		if !participaDoPareamento(results[i].Status, sugestoes[i]) {
			continue
		}
		direcao, ok := pernaNaDirecaoDe(rows[i].Kind)
		if !ok {
			continue
		}
		k := chavePerna{kind: direcao, contraparte: *sugestoes[i].CounterpartID, cents: rows[i].AmountCents}

		var melhor *candidataPerna
		melhorDistancia := -1
		for _, c := range balde[k] {
			if c.reivindicada {
				continue
			}
			d := DaysBetween(c.perna.OccurredOn, rows[i].OccurredOn)
			if d > transaction.DedupWindowDays {
				continue
			}
			if melhor == nil || pernaMaisProxima(c.perna, d, melhor.perna, melhorDistancia) {
				melhor, melhorDistancia = c, d
			}
		}
		if melhor == nil {
			continue
		}
		melhor.reivindicada = true
		out[rows[i].Seq] = melhor.perna.ID
	}
	return out
}

// participaDoPareamento diz se a linha procura a perna existente: transferência
// interna (sempre com contraparte, por construção) ou pagamento de fatura com
// contraparte sugerida (emenda §10.2). Só a contraparte sugerida PELA ANÁLISE
// participa — nunca uma vinda do cliente.
func participaDoPareamento(status dedup.Status, s sugestaoDaLinha) bool {
	if s.CounterpartID == nil || *s.CounterpartID == "" {
		return false
	}
	return status == dedup.StatusInternalTransfer || status == dedup.StatusCardPayment
}

// pernaNaDirecaoDe traduz o kind da linha do ARQUIVO na perna que a conta do
// lote teria: dinheiro saindo (despesa) é transfer_out nesta conta; entrando
// (receita) é transfer_in. Errar isto casaria a linha com a perna do sentido
// oposto — uma transferência de ida vinculada à de volta.
func pernaNaDirecaoDe(kind string) (string, bool) {
	switch kind {
	case transaction.KindExpense:
		return transaction.KindTransferOut, true
	case transaction.KindIncome:
		return transaction.KindTransferIn, true
	default:
		return "", false
	}
}

// pernaMaisProxima decide se a candidata `c` (a `dc` dias) vence a atual
// `melhor` (a `dm` dias): menor distância; empate → menor OccurredOn; depois
// menor ID. Determinístico para a mesma prévia dar sempre a mesma resposta.
func pernaMaisProxima(c *transaction.TransferLeg, dc int, melhor *transaction.TransferLeg, dm int) bool {
	if dc != dm {
		return dc < dm
	}
	if cmp := c.OccurredOn.Compare(melhor.OccurredOn); cmp != 0 {
		return cmp < 0
	}
	return c.ID < melhor.ID
}

type chavePerna struct {
	kind        string
	contraparte string
	cents       int64
}

type candidataPerna struct {
	perna        *transaction.TransferLeg
	reivindicada bool
}
