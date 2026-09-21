// Package nubank implementa os parsers dos dois documentos do Nubank.
//
// # A armadilha que este pacote existe para não cair
//
// Os dois arquivos do MESMO banco usam convenções de sinal OPOSTAS:
//
//	nubank.checking.v1       -20.00      negativo é SAÍDA
//	nubank.card_statement.v1  33,70      POSITIVO é saída
//
// E os formatos numéricos também são opostos: o extrato usa ponto decimal sem
// milhar (`-20.00`); a fatura usa vírgula decimal, ponto de milhar e espaço
// depois do sinal (`"- 2.859,82"`).
//
// Nada disso é inferido do conteúdo. Cada parser DECLARA as suas convenções em
// constantes, no topo do arquivo, e o teste-ouro (nubank_test.go) confere linha
// a linha as duas fixtures, com o sinal esperado de cada documento. Se alguém
// trocar uma constante, o teste falha em 28 linhas de uma vez — que é
// exatamente o objetivo, porque uma fatura com o sinal invertido não parece
// errada em lugar nenhum da tela.
package nubank

import (
	"context"
	"fmt"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
)

// CheckingFormatID é o id estável do leiaute do extrato de conta.
const CheckingFormatID = "nubank.checking.v1"

// Convenções DECLARADAS do extrato do Nubank (ADR-024b).
//
// ⚠️ Compare com card.go antes de mexer em qualquer uma das três: elas são
// diferentes lá, e são diferentes de propósito.
const (
	checkingDateFormat   = csvtext.DateDMY                // 04/08/2026
	checkingNumberFormat = csvtext.DecimalPoint           // -20.00
	checkingSign         = importer.SignNegativeIsOutflow // negativo é saída
)

// Nomes das colunas, como o banco as escreve. Servem para a assinatura e para o
// mapeamento por nome — nunca por posição fixa, para uma coluna nova no fim do
// arquivo não deslocar todo o resto.
const (
	colData          = "Data"
	colValor         = "Valor"
	colIdentificador = "Identificador"
	colDescricao     = "Descrição"
)

// checkingSignature é o cabeçalho esperado: Data,Valor,Identificador,Descrição.
var checkingSignature = importer.NewSignature(',', colData, colValor, colIdentificador, colDescricao)

// prefixosPagamentoFatura é a allowlist de prefixos (já normalizados: sem
// acento, minúsculos) que marcam o pagamento da fatura do cartão DENTRO DO
// EXTRATO.
//
// Ela é pequena e fechada de propósito. Um "qualquer coisa que contenha
// fatura" marcaria "FATURA ENERGIA EXEMPLO" — a conta de luz — como pagamento
// de cartão, e a linha sairia barrada por default, com o usuário sem entender
// por quê. E a marcação só SUGERE: quem decide é ele, na revisão.
var prefixosPagamentoFatura = []string{
	"pagamento de fatura",
}

// Checking lê o extrato de conta do Nubank.
//
// Não tem estado: é valor, e pode ser compartilhado entre requisições sem
// sincronização nenhuma.
type Checking struct{}

// NewChecking devolve o parser do extrato.
func NewChecking() Checking { return Checking{} }

func (Checking) ID() string                        { return CheckingFormatID }
func (Checking) Institution() importer.Institution { return importer.InstitutionNubank }
func (Checking) DocKind() importer.DocKind         { return importer.DocKindCheckingStatement }
func (Checking) Detect(h []string, sep rune) importer.Confidence {
	return checkingSignature.Match(h, sep)
}

// Parse lê o extrato inteiro.
func (p Checking) Parse(ctx context.Context, t *csvtext.Table, limits importer.Limits) (importer.ParseResult, error) {
	if t == nil {
		return importer.ParseResult{}, importer.ErrNoRows
	}

	iData, iValor := t.IndexOf(colData), t.IndexOf(colValor)
	iID, iDesc := t.IndexOf(colIdentificador), t.IndexOf(colDescricao)
	if iData < 0 || iValor < 0 || iID < 0 || iDesc < 0 {
		// Só acontece se Detect e Parse discordarem, o que é defeito de
		// programação — e não um arquivo ruim.
		return importer.ParseResult{}, fmt.Errorf(
			"%w: %s não encontrou as colunas da própria assinatura", importer.ErrParserMisconfigured, CheckingFormatID)
	}

	return importer.ParseTable(ctx, p, t, limits, func(rec []string) (importer.ParsedRow, string) {
		data, err := csvtext.ParseDate(rec[iData], checkingDateFormat)
		if err != nil {
			return importer.ParsedRow{}, importer.RejectInvalidDate
		}

		assinado, err := csvtext.ParseCents(rec[iValor], checkingNumberFormat)
		if err != nil {
			return importer.ParsedRow{}, importer.RejectInvalidAmount
		}

		// O kind sai da CONVENÇÃO DECLARADA e de nada mais. A descrição não
		// participa desta decisão em momento nenhum.
		kind, cents, ok := importer.KindFromSigned(assinado, checkingSign)
		if !ok {
			return importer.ParsedRow{}, importer.RejectZeroAmount
		}

		// O Identificador é a chave natural do documento (um UUID por
		// transação). Linha sem ele é REJEITADA em vez de cair na chave
		// derivada: misturar os dois tipos de chave no mesmo arquivo mudaria a
		// contagem de ordinal em silêncio. Rejeitada, ela aparece na revisão
		// com o número da linha — nada some.
		externo := strings.TrimSpace(rec[iID])
		if externo == "" {
			return importer.ParsedRow{}, importer.RejectMissingExternalID
		}
		if !importer.ValidExternalID(externo) {
			return importer.ParsedRow{}, importer.RejectInvalidExternalID
		}

		descricao, normalizada := importer.Describe(rec[iDesc])

		return importer.ParsedRow{
			Kind:            kind,
			OccurredOn:      data,
			AmountCents:     cents,
			Description:     descricao,
			DescriptionNorm: normalizada,
			ExternalID:      &externo,
			Suggestion:      classifyChecking(normalizada),
		}, ""
	})
}

// classifyChecking é o palpite do extrato.
//
// Classifica sobre a descrição JÁ SANITIZADA E NORMALIZADA — a mesma que a
// pessoa vê na revisão. Classificar sobre o texto cru faria a tela mostrar um
// rótulo que o texto exibido ao lado não explica.
func classifyChecking(descricaoNorm string) importer.Suggestion {
	for _, prefixo := range prefixosPagamentoFatura {
		if strings.HasPrefix(descricaoNorm, prefixo) {
			return importer.SuggestionCardPayment
		}
	}
	return importer.SuggestionNone
}
