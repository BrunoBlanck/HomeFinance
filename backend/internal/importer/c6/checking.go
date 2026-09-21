// Package c6 implementa os dois parsers dos documentos do C6 Bank.
//
// # A armadilha que este pacote existe para não cair
//
// O extrato e a fatura do C6 modelam o SINAL de formas diferentes — e o extrato
// é diferente de TODO o resto do projeto:
//
//   - c6.card_statement.v1 usa UM valor com sinal, e POSITIVO é saída, igual à
//     fatura do Nubank. KindFromSigned resolve.
//   - c6.checking.v1 NÃO tem valor com sinal. Tem DUAS colunas separadas,
//     Entrada(R$) e Saída(R$), e uma delas é sempre `0.00`. O kind sai de QUAL
//     das duas é maior que zero — e por isso este arquivo NÃO usa KindFromSigned:
//     ele declara a sua própria convenção de "duas colunas" (ver
//     kindFromDuasColunas), tão explícita quanto as constantes de sinal do
//     Nubank.
//
// Como no Nubank, nada disso é inferido do conteúdo. Cada parser DECLARA as suas
// convenções em constantes, no topo do arquivo, e o teste-ouro (c6_test.go)
// confere linha a linha as duas fixtures. Trocar Entrada por Saída no extrato,
// ou o sinal na fatura, deixa o golden vermelho de uma vez — que é exatamente o
// objetivo, porque um extrato com entrada e saída trocadas não parece errado em
// tela nenhuma.
//
// O extrato do C6 ainda traz um PREÂMBULO de 8 linhas (título do banco, agência,
// conta, geração, período, linhas em branco) antes do cabeçalho real. Pular o
// preâmbulo é responsabilidade do núcleo (importer.Registry.OpenDocument, que
// procura a primeira linha que uma assinatura reconheça, até MaxPreambleLines); o
// parser não precisa saber quantas linhas pular, só declarar a assinatura do
// cabeçalho.
package c6

import (
	"context"
	"fmt"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// CheckingFormatID é o id estável do leiaute do extrato de conta.
const CheckingFormatID = "c6.checking.v1"

// Convenções DECLARADAS do extrato do C6.
//
// Repare que NÃO há uma constante de sinal aqui: o extrato do C6 não usa valor
// com sinal, e sim duas colunas (ver kindFromDuasColunas). Compare com card.go,
// que usa importer.SignPositiveIsOutflow — os dois documentos do mesmo banco
// modelam o dinheiro que sai de formas diferentes, de propósito.
const (
	checkingDateFormat = csvtext.DateDMY // 23/08/2026

	// checkingNumberFormat — ponto decimal, SEM separador de milhar: o C6
	// escreve `9950.00` e `6000.00`, e não `9,950.00`. DecimalPoint aceita os
	// dois, então isto continua correto se um dia vier com milhar.
	checkingNumberFormat = csvtext.DecimalPoint
)

// Nomes das colunas do extrato, como o banco as escreve. Servem para a
// assinatura e para o mapeamento por nome — nunca por posição fixa.
const (
	colDataLancamento = "Data Lançamento"
	colDataContabil   = "Data Contábil"
	colTitulo         = "Título"
	colDescricao      = "Descrição"
	colEntrada        = "Entrada(R$)"
	colSaida          = "Saída(R$)"
	colSaldo          = "Saldo do Dia(R$)"
)

// checkingSignature é o cabeçalho esperado, com as 7 colunas na ordem do banco e
// o separador VÍRGULA. Declarar as 7 dá casamento exato e afasta qualquer
// ambiguidade com os outros parsers (a fatura do C6 usa `;` e cabeçalho de
// outras colunas; os dois do Nubank têm outras colunas).
var checkingSignature = importer.NewSignature(',',
	colDataLancamento, colDataContabil, colTitulo, colDescricao, colEntrada, colSaida, colSaldo)

// prefixosPagamentoFatura é a allowlist de prefixos (já normalizados: sem
// acento, minúsculos) que marcam o pagamento da fatura do cartão DENTRO DO
// EXTRATO.
//
// Pequena e fechada de propósito, no mesmo espírito do parser do Nubank: um
// "qualquer coisa que contenha fatura" marcaria a conta de luz ("Fatura de
// energia") como pagamento de cartão, e a linha sairia barrada por default sem o
// usuário entender por quê. O C6 escreve "PGTO FAT CARTAO C6" na coluna Título —
// é dela que a marcação sai. E ela só SUGERE: quem decide é o usuário na revisão.
var prefixosPagamentoFatura = []string{
	"pgto fat cartao",
}

// Checking lê o extrato de conta do C6.
//
// Não tem estado: é valor, e pode ser compartilhado entre requisições sem
// sincronização nenhuma.
type Checking struct{}

// NewChecking devolve o parser do extrato.
func NewChecking() Checking { return Checking{} }

func (Checking) ID() string                        { return CheckingFormatID }
func (Checking) Institution() importer.Institution { return importer.InstitutionC6 }
func (Checking) DocKind() importer.DocKind         { return importer.DocKindCheckingStatement }
func (Checking) Detect(h []string, sep rune) importer.Confidence {
	return checkingSignature.Match(h, sep)
}

// Parse lê o extrato inteiro.
func (p Checking) Parse(ctx context.Context, t *csvtext.Table, limits importer.Limits) (importer.ParseResult, error) {
	if t == nil {
		return importer.ParseResult{}, importer.ErrNoRows
	}

	iData := t.IndexOf(colDataLancamento)
	iTitulo, iDesc := t.IndexOf(colTitulo), t.IndexOf(colDescricao)
	iEntrada, iSaida := t.IndexOf(colEntrada), t.IndexOf(colSaida)
	if iData < 0 || iTitulo < 0 || iDesc < 0 || iEntrada < 0 || iSaida < 0 {
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

		entrada, err := csvtext.ParseCents(rec[iEntrada], checkingNumberFormat)
		if err != nil {
			return importer.ParsedRow{}, importer.RejectInvalidAmount
		}
		saida, err := csvtext.ParseCents(rec[iSaida], checkingNumberFormat)
		if err != nil {
			return importer.ParsedRow{}, importer.RejectInvalidAmount
		}

		// O kind sai da CONVENÇÃO DECLARADA de "duas colunas", e de nada mais. A
		// descrição não participa desta decisão em momento nenhum.
		kind, cents, motivo := kindFromDuasColunas(entrada, saida)
		if motivo != "" {
			return importer.ParsedRow{}, motivo
		}

		// Descrição: o C6 traz o rótulo curto em "Título" (sempre presente nas
		// amostras) e o texto longo em "Descrição". Usamos o Título; se vier
		// vazio, caímos para a Descrição. Passa pelo Describe (sanitiza +
		// normaliza) como no Nubank — é a forma normalizada que entra na chave
		// derivada e a que a pessoa vê na revisão.
		descRaw := rec[iTitulo]
		if strings.TrimSpace(descRaw) == "" {
			descRaw = rec[iDesc]
		}
		descricao, normalizada := importer.Describe(descRaw)

		return importer.ParsedRow{
			Kind:            kind,
			OccurredOn:      data,
			AmountCents:     cents,
			Description:     descricao,
			DescriptionNorm: normalizada,
			// O extrato do C6 não numera as linhas: sem chave natural, cai na
			// chave derivada com ordinal, como a fatura do Nubank.
			ExternalID: nil,
			Suggestion: classifyChecking(normalizada),
		}, ""
	})
}

// kindFromDuasColunas aplica a convenção DECLARADA de "duas colunas" do extrato
// do C6 e devolve o kind do domínio com o valor absoluto.
//
// A convenção, por extenso:
//
//   - as duas colunas são magnitudes NÃO-NEGATIVAS (o sinal vive na escolha da
//     coluna, não no número); um valor negativo viola a convenção e é rejeitado,
//     nunca "consertado";
//   - exatamente UMA das duas > 0: Entrada > 0 é uma ENTRADA (income), Saída > 0
//     é uma SAÍDA (expense);
//   - as DUAS zero → linha sem valor (RejectZeroAmount), como o valor zero do
//     Nubank: não é entrada nem saída, e inventar uma seria pior;
//   - as DUAS > 0 → linha inválida (RejectInvalidAmount): o extrato do C6 põe
//     uma das colunas em `0.00`, e as duas preenchidas quer dizer arquivo fora
//     do formato — não há como decidir a direção do dinheiro sem adivinhar.
//
// Este é o análogo de importer.KindFromSigned para o modelo de duas colunas; a
// separação entre CONVENÇÃO (aqui) e CLASSIFICAÇÃO (classifyChecking) é a mesma.
func kindFromDuasColunas(entrada, saida int64) (kind string, cents int64, motivo string) {
	if entrada < 0 || saida < 0 {
		return "", 0, importer.RejectInvalidAmount
	}
	switch {
	case entrada > 0 && saida > 0:
		return "", 0, importer.RejectInvalidAmount
	case entrada > 0:
		return transaction.KindIncome, entrada, ""
	case saida > 0:
		return transaction.KindExpense, saida, ""
	default:
		return "", 0, importer.RejectZeroAmount
	}
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
