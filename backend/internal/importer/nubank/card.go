package nubank

import (
	"context"
	"fmt"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// CardFormatID é o id estável do leiaute da fatura de cartão.
const CardFormatID = "nubank.card_statement.v1"

// Convenções DECLARADAS da fatura do Nubank (ADR-024b).
//
// ⚠️ As TRÊS são diferentes das do extrato (checking.go), no mesmo banco:
//
//   - a data é ISO aqui e DD/MM/YYYY lá;
//   - o número é `"33,70"` e `"- 2.859,82"` aqui (vírgula decimal, ponto de
//     milhar, espaço depois do sinal) e `-20.00` lá;
//   - e, o que mais custa: aqui o POSITIVO é saída. Uma compra de R$ 33,70
//     aparece como `33,70`, e o pagamento da fatura aparece como
//     `- 2.859,82`. Trocar esta constante pela do extrato inverte a fatura
//     inteira — todas as compras viram receita — e nenhuma tela do sistema
//     acusa nada.
const (
	cardDateFormat   = csvtext.DateISO                // 2026-09-05
	cardNumberFormat = csvtext.DecimalComma           // "- 2.859,82"
	cardSign         = importer.SignPositiveIsOutflow // positivo é SAÍDA
)

// Nomes das colunas da fatura, minúsculos no arquivo real. A comparação é
// insensível a caixa e a acento, mas os nomes ficam como o banco os escreve.
const (
	colDate   = "date"
	colTitle  = "title"
	colAmount = "amount"
)

// cardSignature é o cabeçalho esperado: date,title,amount.
var cardSignature = importer.NewSignature(',', colDate, colTitle, colAmount)

// prefixosPagamentoRecebido é a allowlist de prefixos (já normalizados) que
// marcam o pagamento da fatura DENTRO DA PRÓPRIA FATURA.
//
// Mesma disciplina do extrato: allowlist curta e fechada, e a marcação apenas
// SUGERE. "Pagamento recebido" é o texto que o Nubank usa; qualquer outro
// crédito cai na regra do estorno (ver classifyCard).
var prefixosPagamentoRecebido = []string{
	"pagamento recebido",
}

// Card lê a fatura de cartão do Nubank.
//
// A fatura NÃO tem identificador por linha: a deduplicação dela depende
// inteiramente da chave derivada com ordinal (ADR-025b). É por isso que a
// fixture tem um par `Cafe Exemplo` idêntico — dois cafés de R$ 11,00 no mesmo
// dia são dois gastos reais, e engolir um deles seria pior do que duplicar.
type Card struct{}

// NewCard devolve o parser da fatura.
func NewCard() Card { return Card{} }

func (Card) ID() string                        { return CardFormatID }
func (Card) Institution() importer.Institution { return importer.InstitutionNubank }
func (Card) DocKind() importer.DocKind         { return importer.DocKindCardStatement }

func (Card) Detect(h []string, sep rune) importer.Confidence { return cardSignature.Match(h, sep) }

// Parse lê a fatura inteira.
//
// ParseResult.Statement fica NIL: este arquivo não traz fechamento, vencimento
// nem total — só as linhas. A sugestão de competência vem de
// accounts.statement_closing_day / statement_due_day, no serviço. Inferir o mês
// pelo nome do arquivo está proibido (spec 0004 §5.3): nome de arquivo é
// entrada do cliente, e uma fatura arquivada no mês errado estragaria a
// competência de dezenas de linhas de uma vez.
func (p Card) Parse(ctx context.Context, t *csvtext.Table, limits importer.Limits) (importer.ParseResult, error) {
	if t == nil {
		return importer.ParseResult{}, importer.ErrNoRows
	}

	iDate, iTitle, iAmount := t.IndexOf(colDate), t.IndexOf(colTitle), t.IndexOf(colAmount)
	if iDate < 0 || iTitle < 0 || iAmount < 0 {
		return importer.ParseResult{}, fmt.Errorf(
			"%w: %s não encontrou as colunas da própria assinatura", importer.ErrParserMisconfigured, CardFormatID)
	}

	return importer.ParseTable(ctx, p, t, limits, func(rec []string) (importer.ParsedRow, string) {
		data, err := csvtext.ParseDate(rec[iDate], cardDateFormat)
		if err != nil {
			return importer.ParsedRow{}, importer.RejectInvalidDate
		}

		assinado, err := csvtext.ParseCents(rec[iAmount], cardNumberFormat)
		if err != nil {
			return importer.ParsedRow{}, importer.RejectInvalidAmount
		}

		kind, cents, ok := importer.KindFromSigned(assinado, cardSign)
		if !ok {
			return importer.ParsedRow{}, importer.RejectZeroAmount
		}

		descricao, normalizada := importer.Describe(rec[iTitle])

		return importer.ParsedRow{
			Kind:            kind,
			OccurredOn:      data,
			AmountCents:     cents,
			Description:     descricao,
			DescriptionNorm: normalizada,
			// Sem chave natural: a fatura não numera as linhas.
			ExternalID: nil,
			Suggestion: classifyCard(kind, normalizada),
		}, ""
	})
}

// classifyCard é o palpite da fatura.
//
// Duas regras, nesta ordem:
//
//  1. prefixo na allowlist de "pagamento recebido" → pagamento de fatura,
//     barrado por default e liberável como transferência;
//  2. qualquer OUTRA entrada (kind income, que nesta convenção quer dizer valor
//     negativo no arquivo) → SuggestionCardInflow, exibida como "crédito na
//     fatura". Ela entra como receita, que é a decisão D4 da spec 0004 §3.4:
//     modelar estorno de verdade é do tamanho de uma entrega, e no v1 o saldo
//     fica correto com o relatório de receita levemente inflado — visível na
//     revisão, e não escondido.
//
// ⚠️ O `kind` chega pronto, vindo da convenção de sinal declarada. Esta função
// NÃO o recalcula e NÃO o corrige: se um arquivo trouxer "Pagamento recebido"
// com valor positivo, a linha é uma SAÍDA com uma sugestão estranha, e não uma
// entrada "consertada" pelo texto. O teste
// TestCardSinalNaoVemDaDescricao trava esse comportamento.
func classifyCard(kind, descricaoNorm string) importer.Suggestion {
	for _, prefixo := range prefixosPagamentoRecebido {
		if strings.HasPrefix(descricaoNorm, prefixo) {
			return importer.SuggestionCardPayment
		}
	}
	if kind == transaction.KindIncome {
		return importer.SuggestionCardInflow
	}
	return importer.SuggestionNone
}
