package nubank_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/nubank"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// registro monta o registro com os DOIS parsers do Nubank.
//
// Os testes passam pelo registro, e não direto pelo parser, porque é o caminho
// real: se a detecção escolher o parser errado, o teste-ouro tem de falhar
// junto — é exatamente o defeito que ele existe para pegar.
func registro(t *testing.T) *importer.Registry {
	t.Helper()
	r, err := importer.NewRegistry(nubank.NewChecking(), nubank.NewCard())
	require.NoError(t, err)
	return r
}

func lerFixture(t *testing.T, nome string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", nome))
	require.NoError(t, err)
	return b
}

func parse(t *testing.T, raw []byte, formato string) (importer.ParseResult, error) {
	t.Helper()
	return registro(t).Parse(context.Background(), raw, formato, importer.DefaultLimits())
}

// linhaEsperada é a linha do teste-ouro. Tudo o que importa de uma linha
// importada está aqui — em especial o KIND, que é onde a inversão de sinal
// apareceria.
type linhaEsperada struct {
	data       string
	kind       string
	cents      int64
	descricao  string
	externalID string // vazio = sem chave natural
	sugestao   importer.Suggestion
}

func conferirLinhas(t *testing.T, res importer.ParseResult, esperadas []linhaEsperada) {
	t.Helper()
	require.Len(t, res.Rows, len(esperadas), "quantidade de linhas aproveitadas")
	require.Empty(t, res.Rejected, "nenhuma linha da fixture deveria ser rejeitada")

	for i, e := range esperadas {
		linha := res.Rows[i]
		ctx := "linha " + linha.OccurredOn.String() + " / " + linha.Description

		assert.Equal(t, i+1, linha.Seq, "seq da %s", ctx)
		assert.Equal(t, i+2, linha.LineNo, "linha física da %s", ctx)
		assert.Equal(t, e.data, linha.OccurredOn.String(), "data da %s", ctx)
		assert.Equal(t, e.kind, linha.Kind, "SINAL da %s", ctx)
		assert.Equal(t, e.cents, linha.AmountCents, "valor da %s", ctx)
		assert.Equal(t, e.descricao, linha.Description, "descrição da %s", ctx)
		assert.Equal(t, e.sugestao, linha.Suggestion, "sugestão da %s", ctx)

		// O valor é SEMPRE positivo: o sinal vive no kind (ADR-003).
		assert.Positive(t, linha.AmountCents, "valor precisa ser positivo na %s", ctx)

		if e.externalID == "" {
			assert.Nil(t, linha.ExternalID, "a %s não deveria ter chave natural", ctx)
			continue
		}
		require.NotNil(t, linha.ExternalID, "a %s deveria ter chave natural", ctx)
		assert.Equal(t, e.externalID, *linha.ExternalID, "chave natural da %s", ctx)
	}
}

// ---------------------------------------------------------------------------
// Teste-ouro do extrato — 13 linhas, negativo é SAÍDA
// ---------------------------------------------------------------------------

func TestCheckingGolden(t *testing.T) {
	res, err := parse(t, lerFixture(t, "nubank_checking_v1.csv"), "")
	require.NoError(t, err)

	assert.Equal(t, nubank.CheckingFormatID, res.FormatID)
	assert.Equal(t, importer.InstitutionNubank, res.Institution)
	assert.Equal(t, importer.DocKindCheckingStatement, res.DocKind)
	assert.Equal(t, 1, res.HeaderLine, "a fixture não tem preâmbulo")
	assert.Nil(t, res.Statement, "extrato não sugere fatura")

	const uuid = "11111111-1111-4111-8111-1111111111"

	conferirLinhas(t, res, []linhaEsperada{
		{"2026-08-04", transaction.KindExpense, 2000, "Pix enviado - Fulano de Tal Silva", uuid + "01", importer.SuggestionNone},
		{"2026-08-05", transaction.KindExpense, 18707, "Pix enviado - ENERGIA EXEMPLO S.A.", uuid + "02", importer.SuggestionNone},
		{"2026-08-07", transaction.KindIncome, 160000, "Pix recebido - BELTRANA DE SOUZA", uuid + "03", importer.SuggestionNone},
		{"2026-08-07", transaction.KindIncome, 85000, "Resgate RDB", uuid + "04", importer.SuggestionNone},
		{"2026-08-07", transaction.KindExpense, 285982, "Pagamento de fatura", uuid + "05", importer.SuggestionCardPayment},
		{"2026-08-12", transaction.KindExpense, 1100, "Pix enviado - PADARIA EXEMPLO LTDA", uuid + "06", importer.SuggestionNone},
		{"2026-08-12", transaction.KindExpense, 1100, "Pix enviado - PADARIA EXEMPLO LTDA", uuid + "07", importer.SuggestionNone},
		{"2026-08-25", transaction.KindIncome, 1028757, "Resgate RDB", uuid + "08", importer.SuggestionNone},
		{"2026-08-25", transaction.KindExpense, 300000, "Pix enviado - CICRANO EXEMPLO DOS SANTOS", uuid + "09", importer.SuggestionNone},
		{"2026-08-27", transaction.KindExpense, 13992, "Pix enviado - POSTO EXEMPLO LTDA.", uuid + "10", importer.SuggestionNone},
		{"2026-08-30", transaction.KindExpense, 2900, "Pix enviado - LOJA EXEMPLO", uuid + "11", importer.SuggestionNone},
		{"2026-08-30", transaction.KindIncome, 2900, "Pix reembolso recebido - LOJA EXEMPLO", uuid + "12", importer.SuggestionNone},
		{"2026-08-31", transaction.KindExpense, 500000, "Pix enviado - CICRANO EXEMPLO DOS SANTOS", uuid + "13", importer.SuggestionNone},
	})

	assert.Equal(t, "2026-08-04", res.MinDate.String())
	assert.Equal(t, "2026-08-31", res.MaxDate.String())

	// Critério de aceite 3 da spec 0004 §10: 4 entradas e 9 saídas.
	entradas, saidas := contarPorKind(res)
	assert.Equal(t, 4, entradas, "entradas do extrato")
	assert.Equal(t, 9, saidas, "saídas do extrato")
}

// ---------------------------------------------------------------------------
// Teste-ouro da fatura — 15 linhas, POSITIVO é saída
// ---------------------------------------------------------------------------

func TestCardGolden(t *testing.T) {
	res, err := parse(t, lerFixture(t, "nubank_card_statement_v1.csv"), "")
	require.NoError(t, err)

	assert.Equal(t, nubank.CardFormatID, res.FormatID)
	assert.Equal(t, importer.InstitutionNubank, res.Institution)
	assert.Equal(t, importer.DocKindCardStatement, res.DocKind)
	assert.Nil(t, res.Statement, "a fatura do Nubank não traz fechamento nem vencimento")

	conferirLinhas(t, res, []linhaEsperada{
		{"2026-09-05", transaction.KindExpense, 3370, "Lanchonete Exemplo", "", importer.SuggestionNone},
		{"2026-09-03", transaction.KindExpense, 795, "App*Entrega Exemplo", "", importer.SuggestionNone},
		{"2026-09-02", transaction.KindExpense, 2698, "App*Mercado Exemplo", "", importer.SuggestionNone},
		{"2026-08-31", transaction.KindExpense, 2289, "App*Bar Exemplo", "", importer.SuggestionNone},
		{"2026-08-22", transaction.KindIncome, 5381, "Ajuste a crédito", "", importer.SuggestionCardInflow},
		{"2026-08-15", transaction.KindExpense, 1410, "Dl*Corrida Exemplo", "", importer.SuggestionNone},
		{"2026-08-15", transaction.KindExpense, 1100, "Dl*Corrida Exemplo", "", importer.SuggestionNone},
		{"2026-08-14", transaction.KindExpense, 1100, "Cafe Exemplo", "", importer.SuggestionNone},
		{"2026-08-14", transaction.KindExpense, 1100, "Cafe Exemplo", "", importer.SuggestionNone},
		{"2026-08-07", transaction.KindExpense, 6000, "Barbearia Exemplo", "", importer.SuggestionNone},
		{"2026-08-07", transaction.KindIncome, 285982, "Pagamento recebido", "", importer.SuggestionCardPayment},
		{"2026-08-06", transaction.KindExpense, 13000, "Consorcio Exemplo", "", importer.SuggestionNone},
		{"2026-08-06", transaction.KindExpense, 4085, "Supermercado Exemplo", "", importer.SuggestionNone},
		{"2026-08-06", transaction.KindExpense, 602, "Padaria Exemplo", "", importer.SuggestionNone},
		{"2026-08-06", transaction.KindExpense, 2264, "Farmacia Exemplo1233", "", importer.SuggestionNone},
	})

	assert.Equal(t, "2026-08-06", res.MinDate.String())
	assert.Equal(t, "2026-09-05", res.MaxDate.String())

	// Critério de aceite 4 da spec 0004 §10: 13 despesas, 1 crédito e 1
	// pagamento. Se a convenção de sinal inverter, isto vira 2 despesas e 13
	// receitas — e é este número que denuncia.
	entradas, saidas := contarPorKind(res)
	assert.Equal(t, 2, entradas, "créditos da fatura (ajuste + pagamento)")
	assert.Equal(t, 13, saidas, "compras da fatura")
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
// A armadilha nº 1: as duas convenções são OPOSTAS
// ---------------------------------------------------------------------------

// TestConvencoesDeSinalSaoOpostas prova, no menor caso possível, que o MESMO
// sinal significa coisas contrárias nos dois documentos do mesmo banco.
//
// É o teste que falha primeiro se alguém "unificar" as constantes de convenção
// achando que são duplicação.
func TestConvencoesDeSinalSaoOpostas(t *testing.T) {
	extrato, err := parse(t, []byte(
		"Data,Valor,Identificador,Descrição\n"+
			"07/08/2026,-10.00,11111111-1111-4111-8111-111111111199,Compra qualquer\n"), "")
	require.NoError(t, err)
	require.Len(t, extrato.Rows, 1)

	fatura, err := parse(t, []byte(
		"date,title,amount\n"+
			"2026-08-07,Compra qualquer,\"-10,00\"\n"), "")
	require.NoError(t, err)
	require.Len(t, fatura.Rows, 1)

	assert.Equal(t, transaction.KindExpense, extrato.Rows[0].Kind,
		"no EXTRATO, negativo é saída")
	assert.Equal(t, transaction.KindIncome, fatura.Rows[0].Kind,
		"na FATURA, negativo é crédito — positivo é que é saída")

	assert.Equal(t, int64(1000), extrato.Rows[0].AmountCents)
	assert.Equal(t, int64(1000), fatura.Rows[0].AmountCents)
}

// TestCardSinalNaoVemDaDescricao trava a separação entre CONVENÇÃO e
// CLASSIFICAÇÃO.
//
// "Pagamento recebido" com valor POSITIVO é um arquivo estranho, e a resposta
// certa é uma saída com uma sugestão estranha — nunca uma entrada "consertada"
// pelo texto. Um parser que olhe a descrição para decidir o sinal passa em todo
// teste-ouro e inverte a fatura no dia em que o banco mudar o texto.
func TestCardSinalNaoVemDaDescricao(t *testing.T) {
	res, err := parse(t, []byte(
		"date,title,amount\n"+
			"2026-08-07,Pagamento recebido,\"100,00\"\n"), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)

	assert.Equal(t, transaction.KindExpense, res.Rows[0].Kind,
		"o kind vem da convenção declarada, nunca do texto")
	assert.Equal(t, importer.SuggestionCardPayment, res.Rows[0].Suggestion,
		"a classificação continua sugerindo, sem mexer no kind")
}

// TestCheckingSinalNaoVemDaDescricao é o espelho do anterior no extrato.
func TestCheckingSinalNaoVemDaDescricao(t *testing.T) {
	res, err := parse(t, []byte(
		"Data,Valor,Identificador,Descrição\n"+
			"07/08/2026,2859.82,11111111-1111-4111-8111-111111111199,Pagamento de fatura\n"), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)

	assert.Equal(t, transaction.KindIncome, res.Rows[0].Kind)
	assert.Equal(t, importer.SuggestionCardPayment, res.Rows[0].Suggestion)
}

// ---------------------------------------------------------------------------
// Classificação: allowlist pequena, e nada além dela
// ---------------------------------------------------------------------------

func TestClassificacaoDoExtrato(t *testing.T) {
	casos := []struct {
		descricao string
		esperada  importer.Suggestion
	}{
		{"Pagamento de fatura", importer.SuggestionCardPayment},
		{"PAGAMENTO DE FATURA", importer.SuggestionCardPayment},
		{"pagamento de fatura - cartão final 1234", importer.SuggestionCardPayment},
		// A allowlist é de PREFIXO e é curta: nada de "contém a palavra fatura".
		{"FATURA ENERGIA EXEMPLO", importer.SuggestionNone},
		{"Estorno de pagamento de fatura", importer.SuggestionNone},
		{"Resgate RDB", importer.SuggestionNone},
	}

	for _, c := range casos {
		t.Run(c.descricao, func(t *testing.T) {
			res, err := parse(t, []byte(
				"Data,Valor,Identificador,Descrição\n"+
					"07/08/2026,-10.00,11111111-1111-4111-8111-111111111199,"+c.descricao+"\n"), "")
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
		{"Pagamento recebido", "\"- 2.859,82\"", importer.SuggestionCardPayment},
		{"PAGAMENTO RECEBIDO", "\"- 100,00\"", importer.SuggestionCardPayment},
		// Todo OUTRO crédito vira receita marcada como crédito na fatura (D4).
		{"Ajuste a crédito", "\"- 53,81\"", importer.SuggestionCardInflow},
		{"Estorno Loja Exemplo", "\"- 10,00\"", importer.SuggestionCardInflow},
		// Compra é compra: nenhuma sugestão.
		{"Cafe Exemplo", "\"11,00\"", importer.SuggestionNone},
	}

	for _, c := range casos {
		t.Run(c.descricao, func(t *testing.T) {
			res, err := parse(t, []byte(
				"date,title,amount\n2026-08-07,"+c.descricao+","+c.valor+"\n"), "")
			require.NoError(t, err)
			require.Len(t, res.Rows, 1)
			assert.Equal(t, c.esperada, res.Rows[0].Suggestion)
		})
	}
}

// ---------------------------------------------------------------------------
// Erro é por LINHA, até o teto
// ---------------------------------------------------------------------------

func TestLinhaRuimNaoDerrubaArquivo(t *testing.T) {
	// 9 linhas boas e 1 ruim = 10% de rejeição, abaixo do teto de 20%.
	var b strings.Builder
	b.WriteString("date,title,amount\n")
	for i := 1; i <= 9; i++ {
		b.WriteString("2026-08-0" + string(rune('0'+i)) + ",Compra Exemplo,\"10,00\"\n")
	}
	b.WriteString("2026-13-45,Data impossível,\"10,00\"\n")

	res, err := parse(t, []byte(b.String()), "")
	require.NoError(t, err)

	assert.Len(t, res.Rows, 9)
	require.Len(t, res.Rejected, 1)
	assert.Equal(t, 10, res.Rejected[0].Seq)
	assert.Equal(t, 11, res.Rejected[0].LineNo, "linha física, para a pessoa conferir no arquivo")
	assert.Equal(t, importer.RejectInvalidDate, res.Rejected[0].Reason)
}

func TestMuitasLinhasRuinsRecusamOArquivoInteiro(t *testing.T) {
	// 3 boas e 2 ruins = 40%. Acima de 20% quer dizer PARSER ERRADO, e aí
	// importar as três que sobraram seria pior do que recusar.
	raw := []byte("date,title,amount\n" +
		"2026-08-01,Compra Exemplo,\"10,00\"\n" +
		"2026-08-02,Compra Exemplo,\"10,00\"\n" +
		"2026-08-03,Compra Exemplo,\"10,00\"\n" +
		"2026-08-04,Valor impossível,\"abc\"\n" +
		"2026-08-05,Valor impossível,\"\"\n")

	_, err := parse(t, raw, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrTooManyRejected)
}

func TestExtratoRejeitaLinhaSemIdentificador(t *testing.T) {
	raw := []byte("Data,Valor,Identificador,Descrição\n" +
		"04/08/2026,-20.00,11111111-1111-4111-8111-111111111101,Compra Exemplo\n" +
		"05/08/2026,-20.00,,Compra sem id\n" +
		"06/08/2026,-20.00,11111111-1111-4111-8111-111111111103,Compra Exemplo\n" +
		"07/08/2026,-20.00,11111111-1111-4111-8111-111111111104,Compra Exemplo\n" +
		"08/08/2026,-20.00,11111111-1111-4111-8111-111111111105,Compra Exemplo\n")

	res, err := parse(t, raw, "")
	require.NoError(t, err)

	assert.Len(t, res.Rows, 4)
	require.Len(t, res.Rejected, 1)
	assert.Equal(t, importer.RejectMissingExternalID, res.Rejected[0].Reason)
	assert.Equal(t, 3, res.Rejected[0].LineNo)
}

func TestExtratoRejeitaIdentificadorLongoDemais(t *testing.T) {
	longo := strings.Repeat("a", importer.MaxExternalIDBytes+1)
	raw := []byte("Data,Valor,Identificador,Descrição\n" +
		"04/08/2026,-20.00,11111111-1111-4111-8111-111111111101,Compra Exemplo\n" +
		"05/08/2026,-20.00," + longo + ",Compra Exemplo\n" +
		"06/08/2026,-20.00,11111111-1111-4111-8111-111111111103,Compra Exemplo\n" +
		"07/08/2026,-20.00,11111111-1111-4111-8111-111111111104,Compra Exemplo\n" +
		"08/08/2026,-20.00,11111111-1111-4111-8111-111111111105,Compra Exemplo\n")

	res, err := parse(t, raw, "")
	require.NoError(t, err)
	require.Len(t, res.Rejected, 1)
	assert.Equal(t, importer.RejectInvalidExternalID, res.Rejected[0].Reason)
}

func TestValorZeroEhRejeitado(t *testing.T) {
	// Zero não obedece a convenção de sinal nenhuma: não é entrada nem saída.
	// Rejeitar é o certo; "consertar" para receita de R$ 0,00 seria inventar
	// um lançamento.
	raw := []byte("date,title,amount\n" +
		"2026-08-01,Compra Exemplo,\"10,00\"\n" +
		"2026-08-02,Compra Exemplo,\"10,00\"\n" +
		"2026-08-03,Compra Exemplo,\"10,00\"\n" +
		"2026-08-04,Compra Exemplo,\"10,00\"\n" +
		"2026-08-05,Linha zerada,\"0,00\"\n")

	res, err := parse(t, raw, "")
	require.NoError(t, err)
	require.Len(t, res.Rejected, 1)
	assert.Equal(t, importer.RejectZeroAmount, res.Rejected[0].Reason)
}

// ---------------------------------------------------------------------------
// Detecção
// ---------------------------------------------------------------------------

func TestDeteccaoEscolheOParserCerto(t *testing.T) {
	reg := registro(t)

	casos := []struct {
		nome     string
		header   []string
		esperado string
	}{
		{"extrato", []string{"Data", "Valor", "Identificador", "Descrição"}, nubank.CheckingFormatID},
		{"fatura", []string{"date", "title", "amount"}, nubank.CardFormatID},
		// O BOM já saiu no Decode, mas caixa e acento variam de verdade entre
		// exportações do mesmo banco.
		{"extrato sem acento", []string{"DATA", "valor", "IDENTIFICADOR", "descricao"}, nubank.CheckingFormatID},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			p, err := reg.Select(c.header, ',', "")
			require.NoError(t, err)
			assert.Equal(t, c.esperado, p.ID())
		})
	}
}

func TestColunaNovaNoFimNaoQuebra(t *testing.T) {
	// O banco acrescentou uma coluna. Continua importando (confiança fraca).
	raw := []byte("date,title,amount,categoria\n2026-08-07,Cafe Exemplo,\"11,00\",Alimentação\n")
	res, err := parse(t, raw, "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)
	assert.Equal(t, nubank.CardFormatID, res.FormatID)
	assert.Equal(t, int64(1100), res.Rows[0].AmountCents)
}

func TestColunaFaltandoQuebra(t *testing.T) {
	// Sem a coluna de valor não há o que importar — e falhar é o certo.
	_, err := parse(t, []byte("date,title\n2026-08-07,Cafe Exemplo\n"), "")
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatUnknown)
}

func TestFormatoDesconhecidoEhRecusado(t *testing.T) {
	// É a resposta para o arquivo do C6 hoje (spec 0004 §7.3).
	_, err := parse(t, []byte("Data Lançamento;Histórico;Valor (R$)\n01/08/2026;Compra;-10,00\n"), "")
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatUnknown)
}

func TestFormatoExplicitoNaoPodeForcarOParserErrado(t *testing.T) {
	// Forçar o parser do extrato num arquivo de fatura leria a fatura inteira
	// pela convenção de sinal do extrato. Tem de falhar, e não "funcionar".
	_, err := parse(t, []byte("date,title,amount\n2026-08-07,Cafe Exemplo,\"11,00\"\n"), nubank.CheckingFormatID)
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatUnknown)
}

func TestFormatoForaDaAllowlist(t *testing.T) {
	_, err := parse(t, []byte("date,title,amount\n2026-08-07,Cafe Exemplo,\"11,00\"\n"), "banco.inventado.v1")
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatNotAllowed)
}

func TestPreambuloEhPulado(t *testing.T) {
	// Emissor que põe titular e período antes da tabela. O Nubank não põe; o
	// mecanismo existe no núcleo para o C6 (spec 0004 §7.3, item 6).
	raw := []byte(
		"Extrato de conta\n" +
			"Titular: Fulano de Tal Silva\n" +
			"Período: 01/08/2026 a 31/08/2026\n" +
			"\n" +
			"Data,Valor,Identificador,Descrição\n" +
			"04/08/2026,-20.00,11111111-1111-4111-8111-111111111101,Compra Exemplo\n")

	res, err := parse(t, raw, "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)

	assert.Equal(t, 5, res.HeaderLine, "cabeçalho na 5ª linha física")
	assert.Equal(t, 1, res.Rows[0].Seq, "Seq conta linhas de DADOS")
	assert.Equal(t, 6, res.Rows[0].LineNo, "LineNo aponta a linha física do arquivo")
}

func TestDatasCivisNaoSaoNormalizadas(t *testing.T) {
	// 31/02 não vira 03/03: data impossível é linha rejeitada.
	raw := []byte("Data,Valor,Identificador,Descrição\n" +
		"04/08/2026,-20.00,11111111-1111-4111-8111-111111111101,Compra Exemplo\n" +
		"05/08/2026,-20.00,11111111-1111-4111-8111-111111111102,Compra Exemplo\n" +
		"06/08/2026,-20.00,11111111-1111-4111-8111-111111111103,Compra Exemplo\n" +
		"07/08/2026,-20.00,11111111-1111-4111-8111-111111111104,Compra Exemplo\n" +
		"31/02/2026,-20.00,11111111-1111-4111-8111-111111111105,Compra Exemplo\n")

	res, err := parse(t, raw, "")
	require.NoError(t, err)
	require.Len(t, res.Rejected, 1)
	assert.Equal(t, importer.RejectInvalidDate, res.Rejected[0].Reason)
	for _, r := range res.Rows {
		assert.NotEqual(t, civil.MustNew(2026, 3, 3), r.OccurredOn)
	}
}

func TestArquivoVazio(t *testing.T) {
	_, err := parse(t, []byte("date,title,amount\n"), "")
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrNoRows)
}

func TestContextoCanceladoAborta(t *testing.T) {
	ctx, cancelar := context.WithCancel(context.Background())
	cancelar()

	_, err := registro(t).Parse(ctx, lerFixture(t, "nubank_card_statement_v1.csv"), "", importer.DefaultLimits())
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled), "a análise tem de morrer com o contexto")
}
