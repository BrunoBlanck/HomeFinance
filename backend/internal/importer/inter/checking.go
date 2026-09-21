// Package inter implementa o parser do extrato de conta do Banco Inter.
//
// # O que este documento é, e o que ele NÃO é
//
// Por ora o Inter entra SÓ com o extrato de conta (`inter.checking.v1`). A
// fatura do cartão fica de fora até existir uma amostra real: a convenção de
// sinal é DECLARADA a partir do arquivo, nunca chutada (ADR-024b), e os dois
// bancos já atendidos provam que o mesmo emissor pode inverter o sinal entre o
// extrato e a fatura. Um parser de fatura escrito "por analogia" seria exatamente
// o parser que inverte uma fatura inteira sem que nada falhe.
//
// # As convenções do extrato, e onde elas diferem dos vizinhos
//
//	inter.checking.v1   -9.950,00   negativo é SAÍDA, número BRASILEIRO
//
// O sinal segue o extrato do Nubank (negativo é saída). O NÚMERO, não: o Inter
// escreve vírgula decimal e ponto de milhar (`-9.950,00`, `13.000,00`), que é o
// formato da FATURA do Nubank — e é por isso que o formato numérico é uma
// constante declarada aqui, separada da convenção de sinal. Declarado errado
// (ponto decimal), `-780,00` seria um agrupamento de milhar inválido e a linha
// cairia rejeitada — todas cairiam, e o arquivo inteiro seria recusado por
// ErrTooManyRejected, que é o comportamento certo para "parser errado". O
// teste-ouro (inter_test.go) confere os centavos linha a linha para que a
// constante não possa ser trocada em silêncio.
//
// O extrato traz um PREÂMBULO de 5 linhas (título, conta, período, saldo, linha
// em branco) antes do cabeçalho real, na 6ª linha física. Pular o preâmbulo é do
// núcleo (importer.Registry.OpenDocument, até MaxPreambleLines); o parser só
// declara a assinatura do cabeçalho.
//
// # A descrição
//
// O Inter separa o TIPO da operação ("Pix enviado", "Pagamento efetuado") da
// CONTRAPARTE ("Receita Federal") em duas colunas, Histórico e Descrição. As
// duas são juntadas com " - " — o separador de segmentos que o sanitize já
// entende —, de modo que a descrição gravada fica "Pix enviado - Receita
// Federal": o tipo na frente, como no Nubank depois do mapa canônico, e a
// contraparte depois, onde a palavra-chave da conta (spec 0005) a encontra.
package inter

import (
	"context"
	"fmt"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
)

// CheckingFormatID é o id estável do leiaute do extrato de conta.
const CheckingFormatID = "inter.checking.v1"

// Convenções DECLARADAS do extrato do Inter (ADR-024b).
//
// ⚠️ O número é DecimalComma — o contrário do extrato do Nubank e do C6, que
// usam ponto decimal. É a diferença que mais custa se vier errada: ver o
// comentário do pacote.
const (
	checkingDateFormat   = csvtext.DateDMY                // 11/09/2026
	checkingNumberFormat = csvtext.DecimalComma           // -9.950,00
	checkingSign         = importer.SignNegativeIsOutflow // negativo é saída
)

// Nomes das colunas, como o banco as escreve. Servem para a assinatura e para o
// mapeamento por nome — nunca por posição fixa, para uma coluna nova no fim do
// arquivo não deslocar todo o resto.
const (
	colDataLancamento = "Data Lançamento"
	colHistorico      = "Histórico"
	colDescricao      = "Descrição"
	colValor          = "Valor"
	colSaldo          = "Saldo"
)

// checkingSignature é o cabeçalho esperado, com as 5 colunas na ordem do banco
// e o separador PONTO-E-VÍRGULA. Declarar as 5 dá casamento exato e afasta
// ambiguidade com os outros extratos: o do C6 começa com "Data Lançamento"
// também, mas segue com "Data Contábil" e usa vírgula.
var checkingSignature = importer.NewSignature(';',
	colDataLancamento, colHistorico, colDescricao, colValor, colSaldo)

// separadorDeSegmento é o " - " que o sanitize usa para partir a descrição em
// segmentos. Histórico e Descrição são juntados com ele de propósito, para que
// a descrição gravada tenha a mesma forma das do Nubank (tipo - contraparte).
const separadorDeSegmento = " - "

// prefixosPagamentoFatura é a allowlist de prefixos (já normalizados: sem
// acento, minúsculos) que marcam o pagamento da fatura do cartão DENTRO DO
// EXTRATO, sobre o Histórico — que no Inter é um vocabulário fechado de tipos
// de operação, e não texto livre.
//
// ⚠️ A amostra que originou este parser NÃO tinha pagamento de fatura: o
// prefixo abaixo é o que o Inter escreve segundo o conhecimento disponível na
// data, e fica a confirmar com um extrato real que traga um. Se nunca casar, o
// custo é zero — a linha entra sem sugestão e a pessoa decide na revisão. Se
// casar errado, o custo também é baixo: a sugestão só SUGERE, nunca muda kind
// nem valor. O que NÃO pode acontecer é "qualquer coisa que contenha fatura":
// um boleto da conta de luz vem como "Pagamento efetuado - ENERGIA ..." e
// continua sem sugestão.
var prefixosPagamentoFatura = []string{
	"pagamento de fatura",
}

// Checking lê o extrato de conta do Inter.
//
// Não tem estado: é valor, e pode ser compartilhado entre requisições sem
// sincronização nenhuma.
type Checking struct{}

// NewChecking devolve o parser do extrato.
func NewChecking() Checking { return Checking{} }

func (Checking) ID() string                        { return CheckingFormatID }
func (Checking) Institution() importer.Institution { return importer.InstitutionInter }
func (Checking) DocKind() importer.DocKind         { return importer.DocKindCheckingStatement }
func (Checking) Detect(h []string, sep rune) importer.Confidence {
	return checkingSignature.Match(h, sep)
}

// Parse lê o extrato inteiro.
func (p Checking) Parse(ctx context.Context, t *csvtext.Table, limits importer.Limits) (importer.ParseResult, error) {
	if t == nil {
		return importer.ParseResult{}, importer.ErrNoRows
	}

	iData, iValor := t.IndexOf(colDataLancamento), t.IndexOf(colValor)
	iHist, iDesc := t.IndexOf(colHistorico), t.IndexOf(colDescricao)
	if iData < 0 || iValor < 0 || iHist < 0 || iDesc < 0 {
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

		// O kind sai da CONVENÇÃO DECLARADA e de nada mais. Nem o Histórico
		// ("Pix enviado"/"Pix recebido") nem a Descrição participam desta
		// decisão: um "Pix recebido" com valor negativo é uma saída com texto
		// estranho, não uma entrada "consertada" pelo texto.
		kind, cents, ok := importer.KindFromSigned(assinado, checkingSign)
		if !ok {
			return importer.ParsedRow{}, importer.RejectZeroAmount
		}

		descricao, normalizada := importer.Describe(juntarHistoricoEDescricao(rec[iHist], rec[iDesc]))

		return importer.ParsedRow{
			Kind:            kind,
			OccurredOn:      data,
			AmountCents:     cents,
			Description:     descricao,
			DescriptionNorm: normalizada,
			// O extrato do Inter não numera as linhas: sem chave natural, cai
			// na chave derivada com ordinal, como o extrato do C6.
			ExternalID: nil,
			Suggestion: classifyChecking(normalizada),
		}, ""
	})
}

// juntarHistoricoEDescricao monta o texto CRU que vai ao sanitize a partir das
// duas colunas de texto do Inter.
//
// O Histórico vem com espaço sobrando no fim ("Pix enviado ") no arquivo real;
// as pontas são aparadas ANTES de juntar para que o separador fique exatamente
// " - " e o sanitize enxergue dois segmentos. Coluna vazia não acrescenta
// separador: "Pix enviado" sozinho, e não "Pix enviado - ".
func juntarHistoricoEDescricao(historico, descricao string) string {
	historico = strings.TrimSpace(historico)
	descricao = strings.TrimSpace(descricao)
	switch {
	case historico == "":
		return descricao
	case descricao == "":
		return historico
	default:
		return historico + separadorDeSegmento + descricao
	}
}

// classifyChecking é o palpite do extrato.
//
// Classifica sobre a descrição JÁ SANITIZADA E NORMALIZADA — a mesma que a
// pessoa vê na revisão. Como o Histórico vai na frente, o prefixo casa com o
// tipo da operação, e não com a contraparte.
func classifyChecking(descricaoNorm string) importer.Suggestion {
	for _, prefixo := range prefixosPagamentoFatura {
		if strings.HasPrefix(descricaoNorm, prefixo) {
			return importer.SuggestionCardPayment
		}
	}
	return importer.SuggestionNone
}
