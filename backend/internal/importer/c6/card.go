package c6

import (
	"context"
	"fmt"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// CardFormatID é o id estável do leiaute da fatura de cartão.
const CardFormatID = "c6.card_statement.v1"

// Convenções DECLARADAS da fatura do C6.
//
// ⚠️ Diferentes das do extrato (checking.go), no mesmo banco:
//
//   - o separador é `;`, e não `,`;
//   - o modelo de sinal é o de UM valor com sinal (não as duas colunas do
//     extrato): POSITIVO é saída. Uma compra de R$ 284,01 aparece como `284.01`,
//     e um crédito (pagamento, estorno) aparece negativo, `-500.00`. Trocar esta
//     constante pela convenção oposta inverte a fatura inteira — todas as compras
//     viram receita — e nenhuma tela do sistema acusa nada.
//
// O número é ponto decimal, como no extrato; o que muda é o sinal.
const (
	cardDateFormat   = csvtext.DateDMY                // 13/07/2026 (as linhas NÃO vêm ordenadas)
	cardNumberFormat = csvtext.DecimalPoint           // 284.01, -500.00
	cardSign         = importer.SignPositiveIsOutflow // positivo é SAÍDA
)

// Nomes das colunas da fatura, como o banco as escreve. A fatura tem 9 colunas;
// mapeamos 4 (data, descrição, parcela e o valor em reais) e ignoramos as
// outras. A assinatura declara as 9 para casar exatamente.
const (
	colDataCompra    = "Data de Compra"
	colNomeCartao    = "Nome no Cartão"
	colFinalCartao   = "Final do Cartão"
	colCategoria     = "Categoria"
	colDescricaoCard = "Descrição"
	colParcela       = "Parcela"
	colValorUSD      = "Valor (em US$)"
	colCotacao       = "Cotação (em R$)"
	colValorBRL      = "Valor (em R$)"
)

// cardSignature é o cabeçalho esperado, com as 9 colunas na ordem do banco e o
// separador PONTO-E-VÍRGULA.
var cardSignature = importer.NewSignature(';',
	colDataCompra, colNomeCartao, colFinalCartao, colCategoria, colDescricaoCard,
	colParcela, colValorUSD, colCotacao, colValorBRL)

// prefixosPagamentoRecebido é a allowlist de prefixos (já normalizados) que
// marcam o pagamento da fatura DENTRO DA PRÓPRIA FATURA.
//
// Mesma disciplina do Nubank: allowlist curta e fechada, e a marcação apenas
// SUGERE. "Inclusao de Pagamento" é o texto que o C6 usa; qualquer outro crédito
// (negativo que não é pagamento, como "Estorno Tarifa") cai na regra de crédito
// na fatura (ver classifyCard).
var prefixosPagamentoRecebido = []string{
	"inclusao de pagamento",
}

// parcelaUnica é o rótulo do C6 (normalizado: sem acento, minúsculo) para a
// compra à vista. "Única" não acrescenta nada à descrição; qualquer outra forma
// ("2/7", "1/12") é preservada.
const parcelaUnica = "unica"

// separadorParcela precede o número da parcela na descrição.
//
// É o ponto médio U+00B7 (` · `), e NÃO o hífen, por dois motivos que se somam:
// o sanitize divide a descrição em segmentos por " - " e apara hífen das pontas,
// então um " - 2/7" seria mexido; o ponto médio passa intacto e fica visível na
// revisão.
const separadorParcela = " · "

// Card lê a fatura de cartão do C6.
//
// A fatura NÃO tem identificador por linha: a deduplicação depende da chave
// derivada com ordinal. É por isso que a fixture tem um par idêntico ("PADARIA
// EXEMPLO", mesma data e valor) — duas compras iguais no mesmo dia são dois
// gastos reais, e engolir uma seria pior do que duplicar. Vários cartões
// adicionais (Final do Cartão 1111, 2222…) convivem na MESMA fatura: o final do
// cartão não muda o destino, e por isso não entra na chave nem na descrição.
type Card struct{}

// NewCard devolve o parser da fatura.
func NewCard() Card { return Card{} }

func (Card) ID() string                        { return CardFormatID }
func (Card) Institution() importer.Institution { return importer.InstitutionC6 }
func (Card) DocKind() importer.DocKind         { return importer.DocKindCardStatement }

func (Card) Detect(h []string, sep rune) importer.Confidence { return cardSignature.Match(h, sep) }

// Parse lê a fatura inteira.
//
// ParseResult.Statement fica NIL: a amostra não traz fechamento, vencimento nem
// total no corpo do CSV — a competência vem de accounts.statement_closing_day /
// statement_due_day, no serviço. Inferir o mês pelo nome do arquivo está
// proibido (spec 0004 §5.3).
func (p Card) Parse(ctx context.Context, t *csvtext.Table, limits importer.Limits) (importer.ParseResult, error) {
	if t == nil {
		return importer.ParseResult{}, importer.ErrNoRows
	}

	iData := t.IndexOf(colDataCompra)
	iDesc, iParcela := t.IndexOf(colDescricaoCard), t.IndexOf(colParcela)
	// O valor em reais é a ÚLTIMA coluna; "Valor (em US$)" e "Cotação (em R$)"
	// são zero nas compras em real e ficam de fora. IndexOf casa pelo nome exato
	// normalizado, então não há risco de "Valor (em R$)" colar em "Valor (em
	// US$)".
	iValor := t.IndexOf(colValorBRL)
	if iData < 0 || iDesc < 0 || iParcela < 0 || iValor < 0 {
		return importer.ParseResult{}, fmt.Errorf(
			"%w: %s não encontrou as colunas da própria assinatura", importer.ErrParserMisconfigured, CardFormatID)
	}

	return importer.ParseTable(ctx, p, t, limits, func(rec []string) (importer.ParsedRow, string) {
		data, err := csvtext.ParseDate(rec[iData], cardDateFormat)
		if err != nil {
			return importer.ParsedRow{}, importer.RejectInvalidDate
		}

		assinado, err := csvtext.ParseCents(rec[iValor], cardNumberFormat)
		if err != nil {
			return importer.ParsedRow{}, importer.RejectInvalidAmount
		}

		kind, cents, ok := importer.KindFromSigned(assinado, cardSign)
		if !ok {
			return importer.ParsedRow{}, importer.RejectZeroAmount
		}

		// A parcela é juntada à descrição ANTES do Describe, de propósito: assim
		// Description e DescriptionNorm saem os dois do mesmo Describe e a
		// invariante do dedup (norm == Normalize(description)) é mantida. Juntar
		// depois exigiria normalizar por fora e furaria essa invariante.
		descricao, normalizada := importer.Describe(comParcela(rec[iDesc], rec[iParcela]))

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

// comParcela junta a descrição da compra ao número da parcela quando ele
// acrescenta informação. "Única" (compra à vista) e parcela vazia não viram
// sufixo nenhum.
func comParcela(descricao, parcela string) string {
	p := strings.TrimSpace(parcela)
	if p == "" || textnorm.Normalize(p) == parcelaUnica {
		return descricao
	}
	return descricao + separadorParcela + p
}

// classifyCard é o palpite da fatura, espelhando o do Nubank.
//
// Duas regras, nesta ordem:
//
//  1. prefixo na allowlist de "inclusao de pagamento" → pagamento de fatura,
//     barrado por default e liberável como transferência;
//  2. qualquer OUTRA entrada (kind income, que nesta convenção quer dizer valor
//     negativo no arquivo, como "Estorno Tarifa") → SuggestionCardInflow,
//     exibida como "crédito na fatura". Entra como receita (decisão D4 da spec
//     0004 §3.4): o saldo fica correto e o relatório de receita fica levemente
//     inflado, visível na revisão e não escondido.
//
// ⚠️ O `kind` chega pronto, vindo da convenção de sinal declarada. Esta função
// NÃO o recalcula e NÃO o corrige: se um arquivo trouxer "Inclusao de Pagamento"
// com valor positivo, a linha é uma SAÍDA com uma sugestão estranha, e não uma
// entrada "consertada" pelo texto.
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
