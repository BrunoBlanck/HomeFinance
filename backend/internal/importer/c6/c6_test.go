package c6_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/c6"
	"github.com/brunorblanck/homefinance/backend/internal/importer/nubank"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// registro monta o registro com os QUATRO parsers (2 Nubank + 2 C6).
//
// Os testes passam pelo registro completo, e não direto pelo parser, porque é o
// caminho real: se a detecção escolher o parser errado — um arquivo do C6 lido
// pelo parser do Nubank, ou vice-versa —, o teste-ouro tem de falhar junto.
func registro(t *testing.T) *importer.Registry {
	t.Helper()
	r, err := importer.NewRegistry(nubank.NewChecking(), nubank.NewCard(), c6.NewChecking(), c6.NewCard())
	require.NoError(t, err)
	return r
}

func lerFixture(t *testing.T, nome string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", nome))
	require.NoError(t, err)
	return b
}

func lerFixtureDe(t *testing.T, dir, nome string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, nome))
	require.NoError(t, err)
	return b
}

func parse(t *testing.T, raw []byte, formato string) (importer.ParseResult, error) {
	t.Helper()
	return registro(t).Parse(context.Background(), raw, formato, importer.DefaultLimits())
}

// linhaEsperada é a linha do teste-ouro. O KIND é onde a inversão de convenção
// apareceria — no extrato, a troca Entrada↔Saída; na fatura, o sinal.
type linhaEsperada struct {
	data      string
	kind      string
	cents     int64
	descricao string
	sugestao  importer.Suggestion
}

// conferirLinhas confere linha a linha. headerLine é a linha física do
// cabeçalho: com preâmbulo (extrato do C6) ela é 9, e o LineNo de cada linha de
// dados é headerLine + Seq. Sem preâmbulo (fatura) ela é 1.
func conferirLinhas(t *testing.T, res importer.ParseResult, headerLine int, esperadas []linhaEsperada) {
	t.Helper()
	require.Len(t, res.Rows, len(esperadas), "quantidade de linhas aproveitadas")
	require.Empty(t, res.Rejected, "nenhuma linha da fixture deveria ser rejeitada")

	for i, e := range esperadas {
		linha := res.Rows[i]
		ctx := "linha " + linha.OccurredOn.String() + " / " + linha.Description

		assert.Equal(t, i+1, linha.Seq, "seq da %s", ctx)
		assert.Equal(t, headerLine+i+1, linha.LineNo, "linha física da %s", ctx)
		assert.Equal(t, e.data, linha.OccurredOn.String(), "data da %s", ctx)
		assert.Equal(t, e.kind, linha.Kind, "SINAL da %s", ctx)
		assert.Equal(t, e.cents, linha.AmountCents, "valor da %s", ctx)
		assert.Equal(t, e.descricao, linha.Description, "descrição da %s", ctx)
		assert.Equal(t, e.sugestao, linha.Suggestion, "sugestão da %s", ctx)

		// O valor é SEMPRE positivo: o sinal vive no kind (ADR-003).
		assert.Positive(t, linha.AmountCents, "valor precisa ser positivo na %s", ctx)

		// Nenhum documento do C6 tem chave natural por linha.
		assert.Nil(t, linha.ExternalID, "a %s não deveria ter chave natural", ctx)
	}
}

func contarPorKind(res importer.ParseResult) (entradas, saidas int) {
	for _, r := range res.Rows {
		switch r.Kind {
		case transaction.KindIncome:
			entradas++
		case transaction.KindExpense:
			saidas++
		}
	}
	return entradas, saidas
}

// ---------------------------------------------------------------------------
// Teste-ouro do extrato — 10 linhas, DUAS colunas (Entrada/Saída), com preâmbulo
// ---------------------------------------------------------------------------

func TestCheckingGolden(t *testing.T) {
	res, err := parse(t, lerFixture(t, "c6_checking_v1.csv"), "")
	require.NoError(t, err)

	assert.Equal(t, c6.CheckingFormatID, res.FormatID)
	assert.Equal(t, importer.InstitutionC6, res.Institution)
	assert.Equal(t, importer.DocKindCheckingStatement, res.DocKind)
	assert.Equal(t, 9, res.HeaderLine, "o cabeçalho vem depois de 8 linhas de preâmbulo")
	assert.Nil(t, res.Statement, "extrato não sugere fatura")

	conferirLinhas(t, res, res.HeaderLine, []linhaEsperada{
		{"2026-08-23", transaction.KindExpense, 50000, "PGTO FAT CARTAO C6", importer.SuggestionCardPayment},
		{"2026-08-25", transaction.KindIncome, 300000, "Pix recebido de Fulano de Tal Silva", importer.SuggestionNone},
		{"2026-08-25", transaction.KindExpense, 150000, "CDB C6 LIM.GARANT.", importer.SuggestionNone},
		{"2026-08-31", transaction.KindIncome, 500000, "Pix recebido de Fulano de Tal Silva", importer.SuggestionNone},
		{"2026-08-31", transaction.KindExpense, 600000, "APLICAÇÃO DE CDB", importer.SuggestionNone},
		{"2026-09-10", transaction.KindIncome, 995000, "Pix recebido de EMPRESA EXEMPLO LTDA", importer.SuggestionNone},
		{"2026-09-10", transaction.KindExpense, 200770, "TOTTA EXEMPLO LTDA", importer.SuggestionNone},
		{"2026-09-15", transaction.KindExpense, 12281, "Pix automático enviado para CLARO", importer.SuggestionNone},
		{"2026-09-16", transaction.KindExpense, 10300, "Pix enviado para Beltrano Exemplo", importer.SuggestionNone},
		{"2026-09-16", transaction.KindExpense, 10300, "Pix enviado para Beltrano Exemplo", importer.SuggestionNone},
	})

	assert.Equal(t, "2026-08-23", res.MinDate.String())
	assert.Equal(t, "2026-09-16", res.MaxDate.String())

	// Entrada vira income, Saída vira expense: 3 entradas e 7 saídas.
	entradas, saidas := contarPorKind(res)
	assert.Equal(t, 3, entradas, "entradas do extrato (as três colunas Entrada preenchidas)")
	assert.Equal(t, 7, saidas, "saídas do extrato (as sete colunas Saída preenchidas)")
}

// ---------------------------------------------------------------------------
// Teste-ouro da fatura — 10 linhas, POSITIVO é saída, sem preâmbulo
// ---------------------------------------------------------------------------

func TestCardGolden(t *testing.T) {
	res, err := parse(t, lerFixture(t, "c6_card_statement_v1.csv"), "")
	require.NoError(t, err)

	assert.Equal(t, c6.CardFormatID, res.FormatID)
	assert.Equal(t, importer.InstitutionC6, res.Institution)
	assert.Equal(t, importer.DocKindCardStatement, res.DocKind)
	assert.Equal(t, 1, res.HeaderLine, "a fatura não tem preâmbulo")
	assert.Nil(t, res.Statement, "a fatura do C6 não traz fechamento nem vencimento no corpo")

	conferirLinhas(t, res, res.HeaderLine, []linhaEsperada{
		{"2026-07-13", transaction.KindExpense, 28401, "LOJA EXEMPLO MOVEIS- · 2/7", importer.SuggestionNone},
		{"2026-08-08", transaction.KindIncome, 50000, "Inclusao de Pagamento", importer.SuggestionCardPayment},
		{"2026-08-15", transaction.KindExpense, 13999, "LOJA ROUPA EXEMPLO", importer.SuggestionNone},
		{"2026-08-24", transaction.KindExpense, 957, "DL*APPCORRIDA", importer.SuggestionNone},
		{"2026-09-02", transaction.KindIncome, 5000, "Estorno Tarifa", importer.SuggestionCardInflow},
		{"2026-09-02", transaction.KindExpense, 5000, "Anuidade Diferenciada · 1/12", importer.SuggestionNone},
		{"2026-08-11", transaction.KindExpense, 848, "MERCADO EXEMPLO 475", importer.SuggestionNone},
		{"2026-08-18", transaction.KindExpense, 3839, "BISTRO EXEMPLO", importer.SuggestionNone},
		{"2026-08-20", transaction.KindExpense, 669, "PADARIA EXEMPLO", importer.SuggestionNone},
		{"2026-08-20", transaction.KindExpense, 669, "PADARIA EXEMPLO", importer.SuggestionNone},
	})

	assert.Equal(t, "2026-07-13", res.MinDate.String())
	assert.Equal(t, "2026-09-02", res.MaxDate.String())

	// POSITIVO é saída: 8 compras e 2 créditos (pagamento + estorno). Se o sinal
	// inverter, isto vira 2 despesas e 8 receitas — e é este número que denuncia.
	entradas, saidas := contarPorKind(res)
	assert.Equal(t, 2, entradas, "créditos da fatura (pagamento + estorno)")
	assert.Equal(t, 8, saidas, "compras da fatura")
}

// ---------------------------------------------------------------------------
// Preâmbulo — primeiro exercício real do mecanismo do núcleo no projeto
// ---------------------------------------------------------------------------

// TestPreambuloDoExtratoEhPulado prova que as 8 linhas de cabeçalho do banco
// (título, agência/conta, geração, período, linhas em branco) são puladas até a
// linha de cabeçalho conhecida, e que os números de linha física apontam para o
// arquivo que a pessoa abre no Excel.
func TestPreambuloDoExtratoEhPulado(t *testing.T) {
	res, err := parse(t, lerFixture(t, "c6_checking_v1.csv"), "")
	require.NoError(t, err)

	assert.Equal(t, 9, res.HeaderLine, "cabeçalho na 9ª linha física (8 de preâmbulo)")
	require.NotEmpty(t, res.Rows)
	assert.Equal(t, 1, res.Rows[0].Seq, "Seq conta linhas de DADOS, não físicas")
	assert.Equal(t, 10, res.Rows[0].LineNo, "LineNo aponta a linha física do arquivo")
}

// ---------------------------------------------------------------------------
// Detecção — cada uma das 4 fixtures casa com EXATAMENTE UM parser
// ---------------------------------------------------------------------------

func TestQuatroFixturesCasamComExatamenteUmParser(t *testing.T) {
	reg := registro(t)
	nubankDir := filepath.Join("..", "nubank", "testdata")

	casos := []struct {
		arquivo  string
		dir      string
		esperado string
	}{
		{"c6_checking_v1.csv", "testdata", c6.CheckingFormatID},
		{"c6_card_statement_v1.csv", "testdata", c6.CardFormatID},
		{"nubank_checking_v1.csv", nubankDir, nubank.CheckingFormatID},
		{"nubank_card_statement_v1.csv", nubankDir, nubank.CardFormatID},
	}

	for _, c := range casos {
		t.Run(c.arquivo, func(t *testing.T) {
			raw := lerFixtureDe(t, c.dir, c.arquivo)

			// Exatamente um candidato: nem IMPORT_FORMAT_UNKNOWN (zero) nem
			// IMPORT_FORMAT_AMBIGUOUS (dois ou mais).
			doc, err := reg.OpenDocument(raw)
			require.NoError(t, err)
			candidatos := reg.Candidates(doc.Table.Header, doc.Table.Separator)
			assert.Equal(t, []string{c.esperado}, candidatos, "deve haver exatamente um parser candidato")

			// E o caminho completo escolhe o mesmo.
			res, err := reg.Parse(context.Background(), raw, "", importer.DefaultLimits())
			require.NoError(t, err)
			assert.Equal(t, c.esperado, res.FormatID)
		})
	}
}

// ---------------------------------------------------------------------------
// Separador — sniff acerta `,` no extrato e `;` na fatura
// ---------------------------------------------------------------------------

func TestSniffSeparadorPorDocumento(t *testing.T) {
	reg := registro(t)

	// Table.Separator é a saída do csvtext.SniffSeparator sobre a linha de
	// cabeçalho (depois de pulado o preâmbulo). Provar que ele vem `,` no extrato
	// e `;` na fatura é provar que o sniff acerta os dois — que é o que a §7.3
	// pede: o extrato do C6 usa vírgula e a fatura ponto-e-vírgula.
	docExtrato, err := reg.OpenDocument(lerFixture(t, "c6_checking_v1.csv"))
	require.NoError(t, err)
	assert.Equal(t, ',', docExtrato.Table.Separator, "extrato do C6 é separado por vírgula")

	docFatura, err := reg.OpenDocument(lerFixture(t, "c6_card_statement_v1.csv"))
	require.NoError(t, err)
	assert.Equal(t, ';', docFatura.Table.Separator, "fatura do C6 é separada por ponto-e-vírgula")
}

// ---------------------------------------------------------------------------
// A convenção de DUAS COLUNAS do extrato (o guard da mutação Entrada↔Saída)
// ---------------------------------------------------------------------------

const cabecalhoExtrato = "Data Lançamento,Data Contábil,Título,Descrição,Entrada(R$),Saída(R$),Saldo do Dia(R$)\n"

func extrato(linhas ...string) []byte {
	s := cabecalhoExtrato
	for _, l := range linhas {
		s += l + "\n"
	}
	return []byte(s)
}

// TestExtratoEntradaVsSaida trava a convenção declarada: Entrada > 0 é income,
// Saída > 0 é expense. É o teste que fica vermelho se alguém trocar as duas
// colunas em kindFromDuasColunas.
func TestExtratoEntradaVsSaida(t *testing.T) {
	res, err := parse(t, extrato(
		"01/08/2026,01/08/2026,So entrada,,150.00,0.00,150.00",
		"02/08/2026,02/08/2026,So saida,,0.00,90.00,60.00",
	), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 2)

	assert.Equal(t, transaction.KindIncome, res.Rows[0].Kind, "Entrada > 0 é ENTRADA")
	assert.Equal(t, int64(15000), res.Rows[0].AmountCents)
	assert.Equal(t, transaction.KindExpense, res.Rows[1].Kind, "Saída > 0 é SAÍDA")
	assert.Equal(t, int64(9000), res.Rows[1].AmountCents)
}

// TestExtratoSinalNaoVemDaDescricao é o espelho do teste do Nubank: o kind vem
// das colunas, nunca do texto. "PGTO FAT CARTAO" numa linha de ENTRADA é uma
// entrada com sugestão de pagamento — não uma saída "consertada" pelo texto.
func TestExtratoSinalNaoVemDaDescricao(t *testing.T) {
	res, err := parse(t, extrato(
		"01/08/2026,01/08/2026,PGTO FAT CARTAO C6,Fatura de cartão,500.00,0.00,500.00",
	), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)

	assert.Equal(t, transaction.KindIncome, res.Rows[0].Kind, "a coluna decide o kind, não a descrição")
	assert.Equal(t, importer.SuggestionCardPayment, res.Rows[0].Suggestion, "a classificação continua sugerindo, sem mexer no kind")
}

func TestExtratoRejeitaDuasColunasZero(t *testing.T) {
	// 1 ruim em 5 = 20%, no limite tolerado.
	res, err := parse(t, extrato(
		"01/08/2026,01/08/2026,Boa,,10.00,0.00,10.00",
		"02/08/2026,02/08/2026,Boa,,20.00,0.00,30.00",
		"03/08/2026,03/08/2026,Boa,,0.00,5.00,25.00",
		"04/08/2026,04/08/2026,Boa,,0.00,5.00,20.00",
		"05/08/2026,05/08/2026,Zerada,,0.00,0.00,20.00",
	), "")
	require.NoError(t, err)
	assert.Len(t, res.Rows, 4)
	require.Len(t, res.Rejected, 1)
	assert.Equal(t, importer.RejectZeroAmount, res.Rejected[0].Reason)
	assert.Equal(t, 5, res.Rejected[0].Seq)
	assert.Equal(t, 6, res.Rejected[0].LineNo, "linha física, para a pessoa conferir")
}

func TestExtratoRejeitaDuasColunasPreenchidas(t *testing.T) {
	res, err := parse(t, extrato(
		"01/08/2026,01/08/2026,Boa,,10.00,0.00,10.00",
		"02/08/2026,02/08/2026,Boa,,20.00,0.00,30.00",
		"03/08/2026,03/08/2026,Boa,,0.00,5.00,25.00",
		"04/08/2026,04/08/2026,Boa,,0.00,5.00,20.00",
		"05/08/2026,05/08/2026,Ambigua,,10.00,10.00,20.00",
	), "")
	require.NoError(t, err)
	assert.Len(t, res.Rows, 4)
	require.Len(t, res.Rejected, 1)
	assert.Equal(t, importer.RejectInvalidAmount, res.Rejected[0].Reason)
	assert.Equal(t, 6, res.Rejected[0].LineNo)
}

// ---------------------------------------------------------------------------
// A convenção de SINAL da fatura (o guard da mutação de sinal)
// ---------------------------------------------------------------------------

const cabecalhoFatura = "Data de Compra;Nome no Cartão;Final do Cartão;Categoria;Descrição;Parcela;Valor (em US$);Cotação (em R$);Valor (em R$)\n"

func fatura(linhas ...string) []byte {
	s := cabecalhoFatura
	for _, l := range linhas {
		s += l + "\n"
	}
	return []byte(s)
}

// TestFaturaPositivoESaida trava a convenção: compra positiva é saída, valor
// negativo é crédito. Fica vermelho se cardSign for trocado.
func TestFaturaPositivoESaida(t *testing.T) {
	res, err := parse(t, fatura(
		"01/08/2026;FULANO;1111;Casa;Compra qualquer;Única;0;0;100.00",
		"02/08/2026;FULANO;1111;-;Credito qualquer;Única;0;0;-40.00",
	), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 2)

	assert.Equal(t, transaction.KindExpense, res.Rows[0].Kind, "compra POSITIVA é SAÍDA")
	assert.Equal(t, int64(10000), res.Rows[0].AmountCents)
	assert.Equal(t, transaction.KindIncome, res.Rows[1].Kind, "valor NEGATIVO é crédito")
	assert.Equal(t, int64(4000), res.Rows[1].AmountCents)
}

// TestFaturaSinalNaoVemDaDescricao: "Inclusao de Pagamento" com valor POSITIVO
// é uma saída com sugestão estranha, não uma entrada "consertada" pelo texto.
func TestFaturaSinalNaoVemDaDescricao(t *testing.T) {
	res, err := parse(t, fatura(
		"01/08/2026;FULANO;1111;-;Inclusao de Pagamento;Única;0;0;100.00",
	), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)

	assert.Equal(t, transaction.KindExpense, res.Rows[0].Kind, "o kind vem da convenção, nunca do texto")
	assert.Equal(t, importer.SuggestionCardPayment, res.Rows[0].Suggestion)
}

// ---------------------------------------------------------------------------
// Parcela
// ---------------------------------------------------------------------------

func TestFaturaParcela(t *testing.T) {
	res, err := parse(t, fatura(
		"01/08/2026;FULANO;1111;Casa;Movel parcelado;3/10;0;0;100.00",
		"02/08/2026;FULANO;1111;Casa;Compra a vista;Única;0;0;50.00",
		"03/08/2026;FULANO;1111;Casa;Sem parcela;;0;0;50.00",
	), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 3)

	assert.Equal(t, "Movel parcelado · 3/10", res.Rows[0].Description, "parcela ≠ Única é preservada")
	assert.Equal(t, "Compra a vista", res.Rows[1].Description, "Única não acrescenta nada")
	assert.Equal(t, "Sem parcela", res.Rows[2].Description, "parcela vazia não acrescenta nada")
}

// ---------------------------------------------------------------------------
// Classificação — allowlist pequena, e nada além dela
// ---------------------------------------------------------------------------

func TestClassificacaoDoExtrato(t *testing.T) {
	casos := []struct {
		titulo   string
		esperada importer.Suggestion
	}{
		{"PGTO FAT CARTAO C6", importer.SuggestionCardPayment},
		{"pgto fat cartao c6", importer.SuggestionCardPayment},
		// Não é "contém fatura": a conta de luz não vira pagamento de cartão.
		{"Fatura de energia", importer.SuggestionNone},
		{"Pix recebido de Fulano", importer.SuggestionNone},
	}
	for _, c := range casos {
		t.Run(c.titulo, func(t *testing.T) {
			res, err := parse(t, extrato(
				"01/08/2026,01/08/2026,"+c.titulo+",,0.00,10.00,10.00",
			), "")
			require.NoError(t, err)
			require.Len(t, res.Rows, 1)
			assert.Equal(t, c.esperada, res.Rows[0].Suggestion)
		})
	}
}

func TestClassificacaoDaFatura(t *testing.T) {
	casos := []struct {
		descricao string
		valor     string
		esperada  importer.Suggestion
	}{
		{"Inclusao de Pagamento", "-500.00", importer.SuggestionCardPayment},
		{"INCLUSAO DE PAGAMENTO", "-100.00", importer.SuggestionCardPayment},
		// Todo OUTRO crédito vira receita marcada como crédito na fatura (D4).
		{"Estorno Tarifa", "-50.00", importer.SuggestionCardInflow},
		// Compra é compra: nenhuma sugestão.
		{"Padaria Exemplo", "6.69", importer.SuggestionNone},
	}
	for _, c := range casos {
		t.Run(c.descricao, func(t *testing.T) {
			res, err := parse(t, fatura(
				"01/08/2026;FULANO;1111;-;"+c.descricao+";Única;0;0;"+c.valor,
			), "")
			require.NoError(t, err)
			require.Len(t, res.Rows, 1)
			assert.Equal(t, c.esperada, res.Rows[0].Suggestion)
		})
	}
}

// ---------------------------------------------------------------------------
// Guardas de formato
// ---------------------------------------------------------------------------

// TestFaturaNaoEntraPeloExtrato: forçar o parser do extrato num arquivo de
// fatura (e vice-versa) tem de falhar — não "funcionar" com o sinal errado.
func TestFaturaNaoEntraPeloExtrato(t *testing.T) {
	_, err := parse(t, lerFixture(t, "c6_card_statement_v1.csv"), c6.CheckingFormatID)
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatUnknown)

	_, err = parse(t, lerFixture(t, "c6_checking_v1.csv"), c6.CardFormatID)
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatUnknown)
}
