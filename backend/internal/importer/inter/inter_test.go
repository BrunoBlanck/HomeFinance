package inter_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/c6"
	"github.com/brunorblanck/homefinance/backend/internal/importer/inter"
	"github.com/brunorblanck/homefinance/backend/internal/importer/nubank"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// registro monta o registro com os CINCO parsers (2 Nubank + 2 C6 + 1 Inter).
//
// Os testes passam pelo registro completo, e não direto pelo parser, porque é o
// caminho real: se a detecção escolher o parser errado — o extrato do Inter lido
// pelo do C6, que também começa com "Data Lançamento" —, o teste-ouro tem de
// falhar junto.
func registro(t *testing.T) *importer.Registry {
	t.Helper()
	r, err := importer.NewRegistry(
		nubank.NewChecking(), nubank.NewCard(),
		c6.NewChecking(), c6.NewCard(),
		inter.NewChecking(),
	)
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

// linhaEsperada é a linha do teste-ouro. O KIND é onde a inversão de sinal
// apareceria; os CENTAVOS são onde a troca do formato numérico apareceria.
type linhaEsperada struct {
	data      string
	kind      string
	cents     int64
	descricao string
	sugestao  importer.Suggestion
}

// conferirLinhas confere linha a linha. headerLine é a linha física do
// cabeçalho: com o preâmbulo do Inter ela é 6, e o LineNo de cada linha de
// dados é headerLine + Seq.
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

		// O extrato do Inter não tem chave natural por linha.
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
// Teste-ouro do extrato — 7 linhas, valor com sinal em formato brasileiro,
// com preâmbulo de 5 linhas
// ---------------------------------------------------------------------------

func TestCheckingGolden(t *testing.T) {
	res, err := parse(t, lerFixture(t, "inter_checking_v1.csv"), "")
	require.NoError(t, err)

	assert.Equal(t, inter.CheckingFormatID, res.FormatID)
	assert.Equal(t, importer.InstitutionInter, res.Institution)
	assert.Equal(t, importer.DocKindCheckingStatement, res.DocKind)
	assert.Equal(t, 6, res.HeaderLine, "o cabeçalho vem depois de 5 linhas de preâmbulo")
	assert.Nil(t, res.Statement, "extrato não sugere fatura")

	// A descrição é "Histórico - Descrição": o tipo da operação na frente e a
	// contraparte depois, com o espaço sobrando de "Pix enviado " já aparado.
	conferirLinhas(t, res, res.HeaderLine, []linhaEsperada{
		{"2026-08-27", transaction.KindExpense, 21078, "Pagamento efetuado - ADMINISTRADORA EXEMPLO S/A", importer.SuggestionNone},
		{"2026-08-25", transaction.KindExpense, 70000, "Pix enviado - Receita Federal", importer.SuggestionNone},
		{"2026-08-25", transaction.KindExpense, 980000, "Pix enviado - Fulano de Tal Silva", importer.SuggestionNone},
		{"2026-08-19", transaction.KindExpense, 35000, "Pagamento efetuado - ESCRITORIO EXEMPLO LTDA", importer.SuggestionNone},
		{"2026-08-19", transaction.KindExpense, 16025, "Pix enviado - Receita Federal", importer.SuggestionNone},
		{"2026-08-19", transaction.KindExpense, 125050, "Pix enviado - Fulano de Tal Silva", importer.SuggestionNone},
		{"2026-08-18", transaction.KindIncome, 1250000, "Pix recebido - Empresa Exemplo Ltda", importer.SuggestionNone},
	})

	// O arquivo vem do mais recente para o mais antigo; a janela sai certa
	// mesmo assim, porque ela é o mínimo e o máximo, não a primeira e a última.
	assert.Equal(t, "2026-08-18", res.MinDate.String())
	assert.Equal(t, "2026-08-27", res.MaxDate.String())

	// Negativo é saída: 1 entrada e 6 saídas. Se o sinal inverter, isto vira 6
	// entradas e 1 saída — e é este número que denuncia.
	entradas, saidas := contarPorKind(res)
	assert.Equal(t, 1, entradas, "entradas do extrato (o único valor positivo)")
	assert.Equal(t, 6, saidas, "saídas do extrato (os seis valores negativos)")
}

// ---------------------------------------------------------------------------
// Preâmbulo — título, conta, período, saldo e linha em branco antes do cabeçalho
// ---------------------------------------------------------------------------

// TestPreambuloDoExtratoEhPulado prova que as 5 linhas do banco são puladas até
// a linha de cabeçalho conhecida, e que os números de linha física apontam para
// o arquivo que a pessoa abre no Excel. Repare que o preâmbulo do Inter tem
// linhas COM `;` ("Conta ;100000001", "Saldo ;42,17"): elas têm separador, mas
// nenhum parser as reconhece como cabeçalho — é isso que as pula, e não a
// ausência de separador.
func TestPreambuloDoExtratoEhPulado(t *testing.T) {
	res, err := parse(t, lerFixture(t, "inter_checking_v1.csv"), "")
	require.NoError(t, err)

	assert.Equal(t, 6, res.HeaderLine, "cabeçalho na 6ª linha física (5 de preâmbulo)")
	require.NotEmpty(t, res.Rows)
	assert.Equal(t, 1, res.Rows[0].Seq, "Seq conta linhas de DADOS, não físicas")
	assert.Equal(t, 7, res.Rows[0].LineNo, "LineNo aponta a linha física do arquivo")
}

// ---------------------------------------------------------------------------
// Detecção — cada uma das 5 fixtures casa com EXATAMENTE UM parser
// ---------------------------------------------------------------------------

// TestCincoFixturesCasamComExatamenteUmParser é a prova de que o parser novo
// não criou ambiguidade com nenhum dos quatro que já existiam — nem o inverso.
// O extrato do C6 e o do Inter começam os dois com "Data Lançamento"; é o resto
// do cabeçalho e o separador que os separam.
func TestCincoFixturesCasamComExatamenteUmParser(t *testing.T) {
	reg := registro(t)
	nubankDir := filepath.Join("..", "nubank", "testdata")
	c6Dir := filepath.Join("..", "c6", "testdata")

	casos := []struct {
		arquivo  string
		dir      string
		esperado string
	}{
		{"inter_checking_v1.csv", "testdata", inter.CheckingFormatID},
		{"c6_checking_v1.csv", c6Dir, c6.CheckingFormatID},
		{"c6_card_statement_v1.csv", c6Dir, c6.CardFormatID},
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

func TestSniffSeparadorDoExtrato(t *testing.T) {
	doc, err := registro(t).OpenDocument(lerFixture(t, "inter_checking_v1.csv"))
	require.NoError(t, err)
	assert.Equal(t, ';', doc.Table.Separator, "extrato do Inter é separado por ponto-e-vírgula")
}

// ---------------------------------------------------------------------------
// A convenção de SINAL (o guard da mutação de sinal)
// ---------------------------------------------------------------------------

const cabecalhoExtrato = "Data Lançamento;Histórico;Descrição;Valor;Saldo\n"

func extrato(linhas ...string) []byte {
	s := cabecalhoExtrato
	for _, l := range linhas {
		s += l + "\n"
	}
	return []byte(s)
}

// TestExtratoNegativoESaida trava a convenção declarada: negativo é saída,
// positivo é entrada. Fica vermelho se checkingSign for trocado.
func TestExtratoNegativoESaida(t *testing.T) {
	res, err := parse(t, extrato(
		"01/08/2026;Pix enviado ;Fulano;-150,00;850,00",
		"02/08/2026;Pix recebido;Beltrano;90,00;940,00",
	), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 2)

	assert.Equal(t, transaction.KindExpense, res.Rows[0].Kind, "valor NEGATIVO é SAÍDA")
	assert.Equal(t, int64(15000), res.Rows[0].AmountCents)
	assert.Equal(t, transaction.KindIncome, res.Rows[1].Kind, "valor POSITIVO é ENTRADA")
	assert.Equal(t, int64(9000), res.Rows[1].AmountCents)
}

// TestExtratoSinalNaoVemDoHistorico: "Pix recebido" com valor NEGATIVO é uma
// saída com texto estranho, não uma entrada "consertada" pelo Histórico. O
// kind vem da convenção, e só dela.
func TestExtratoSinalNaoVemDoHistorico(t *testing.T) {
	res, err := parse(t, extrato(
		"01/08/2026;Pix recebido;Fulano;-150,00;850,00",
		"02/08/2026;Pix enviado ;Beltrano;90,00;940,00",
	), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 2)

	assert.Equal(t, transaction.KindExpense, res.Rows[0].Kind, "o sinal decide o kind, não o Histórico")
	assert.Equal(t, transaction.KindIncome, res.Rows[1].Kind, "o sinal decide o kind, não o Histórico")
}

// ---------------------------------------------------------------------------
// O formato NUMÉRICO (o guard da mutação DecimalComma → DecimalPoint)
// ---------------------------------------------------------------------------

// TestExtratoNumeroBrasileiro trava o formato declarado: vírgula decimal e
// ponto de milhar. `-9.950,00` são 995.000 centavos — não 9,95, não 995 e não
// um erro. Se checkingNumberFormat virar DecimalPoint, cada uma destas linhas
// cai rejeitada, e o teste denuncia.
func TestExtratoNumeroBrasileiro(t *testing.T) {
	res, err := parse(t, extrato(
		"01/08/2026;Pix enviado ;A;-9.950,00;0,00",
		"02/08/2026;Pix recebido;B;13.000,00;0,00",
		"03/08/2026;Pix enviado ;C;-0,01;0,00",
		"04/08/2026;Pix recebido;D;1.234.567,89;0,00",
	), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 4)
	require.Empty(t, res.Rejected)

	assert.Equal(t, int64(995000), res.Rows[0].AmountCents)
	assert.Equal(t, int64(1300000), res.Rows[1].AmountCents)
	assert.Equal(t, int64(1), res.Rows[2].AmountCents)
	assert.Equal(t, int64(123456789), res.Rows[3].AmountCents)
}

// TestExtratoRejeitaPontoDecimal: um valor no formato do Nubank (`-20.00`)
// dentro de um extrato do Inter NÃO é lido "do jeito que der" — é linha
// rejeitada, porque o formato é declarado e não adivinhado por linha.
func TestExtratoRejeitaPontoDecimal(t *testing.T) {
	// 1 ruim em 5 = 20%, no limite tolerado.
	res, err := parse(t, extrato(
		"01/08/2026;Pix enviado ;Boa;-10,00;0,00",
		"02/08/2026;Pix enviado ;Boa;-20,00;0,00",
		"03/08/2026;Pix enviado ;Boa;-30,00;0,00",
		"04/08/2026;Pix enviado ;Boa;-40,00;0,00",
		"05/08/2026;Pix enviado ;Ponto decimal;-20.00;0,00",
	), "")
	require.NoError(t, err)
	assert.Len(t, res.Rows, 4)
	require.Len(t, res.Rejected, 1)
	assert.Equal(t, importer.RejectInvalidAmount, res.Rejected[0].Reason)
	assert.Equal(t, 5, res.Rejected[0].Seq)
	assert.Equal(t, 6, res.Rejected[0].LineNo, "linha física, para a pessoa conferir")
}

func TestExtratoRejeitaValorZero(t *testing.T) {
	res, err := parse(t, extrato(
		"01/08/2026;Pix enviado ;Boa;-10,00;0,00",
		"02/08/2026;Pix enviado ;Boa;-20,00;0,00",
		"03/08/2026;Pix enviado ;Boa;-30,00;0,00",
		"04/08/2026;Pix enviado ;Boa;-40,00;0,00",
		"05/08/2026;Tarifa;Zerada;0,00;0,00",
	), "")
	require.NoError(t, err)
	assert.Len(t, res.Rows, 4)
	require.Len(t, res.Rejected, 1)
	assert.Equal(t, importer.RejectZeroAmount, res.Rejected[0].Reason)
	assert.Equal(t, 6, res.Rejected[0].LineNo)
}

func TestExtratoRejeitaDataInvalida(t *testing.T) {
	res, err := parse(t, extrato(
		"01/08/2026;Pix enviado ;Boa;-10,00;0,00",
		"02/08/2026;Pix enviado ;Boa;-20,00;0,00",
		"03/08/2026;Pix enviado ;Boa;-30,00;0,00",
		"04/08/2026;Pix enviado ;Boa;-40,00;0,00",
		"2026-08-05;Pix enviado ;Data ISO;-50,00;0,00",
	), "")
	require.NoError(t, err)
	assert.Len(t, res.Rows, 4)
	require.Len(t, res.Rejected, 1)
	assert.Equal(t, importer.RejectInvalidDate, res.Rejected[0].Reason)
}

// ---------------------------------------------------------------------------
// A descrição: "Histórico - Descrição"
// ---------------------------------------------------------------------------

func TestDescricaoJuntaHistoricoEDescricao(t *testing.T) {
	casos := []struct {
		nome      string
		historico string
		descricao string
		esperada  string
	}{
		{"as duas colunas", "Pix enviado", "Receita Federal", "Pix enviado - Receita Federal"},
		// O espaço sobrando no fim do Histórico (o arquivo real traz "Pix
		// enviado ") é aparado antes de juntar: o separador fica " - " exato.
		{"espaco sobrando no historico", "Pix enviado ", "Receita Federal", "Pix enviado - Receita Federal"},
		{"espacos nas duas pontas", "  Pagamento efetuado  ", "  LOJA  ", "Pagamento efetuado - LOJA"},
		// Coluna vazia não acrescenta separador pendurado.
		{"so historico", "Tarifa bancária", "", "Tarifa bancária"},
		{"so descricao", "", "LOJA EXEMPLO", "LOJA EXEMPLO"},
		{"so espacos", "   ", "LOJA EXEMPLO", "LOJA EXEMPLO"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			res, err := parse(t, extrato(
				"01/08/2026;"+c.historico+";"+c.descricao+";-10,00;0,00",
			), "")
			require.NoError(t, err)
			require.Len(t, res.Rows, 1)
			assert.Equal(t, c.esperada, res.Rows[0].Description)
		})
	}
}

// TestDescricaoPassaPeloSanitize: o texto cru nunca chega ao banco — CPF e
// CNPJ de terceiros somem, como em qualquer outro parser.
//
// O que acontece com o RESTO do segmento depende de onde o documento veio, e
// é regra do sanitize (versionada, contrato do dedup), não deste parser:
//
//   - documento como segmento PRÓPRIO ("Fulano - 123.456.789-01", o desenho
//     do Nubank): o corte é dali para a frente, e o nome fica;
//   - documento DENTRO da contraparte ("Fulano 123.456.789-01"): o segmento
//     inteiro é tratado como dado da instituição e sai — sobra o Histórico.
//     É o lado seguro do erro: perde-se um nome, nunca vaza um documento. A
//     amostra real do Inter não traz documento na Descrição; se um dia trouxer,
//     a decisão é do sanitize, com subida de versão.
func TestDescricaoPassaPeloSanitize(t *testing.T) {
	res, err := parse(t, extrato(
		"01/08/2026;Pix enviado ;Fulano de Tal - 123.456.789-01;-10,00;0,00",
		"02/08/2026;Pix enviado ;Fulano de Tal 123.456.789-01;-10,00;0,00",
		"03/08/2026;Pagamento efetuado;EMPRESA LTDA 11.222.333/0001-44;-20,00;0,00",
	), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 3)

	assert.Equal(t, "Pix enviado - Fulano de Tal", res.Rows[0].Description)
	assert.Equal(t, "Pix enviado", res.Rows[1].Description)
	assert.Equal(t, "Pagamento efetuado", res.Rows[2].Description)

	for _, r := range res.Rows {
		assert.NotContains(t, r.Description, "123.456.789")
		assert.NotContains(t, r.Description, "11.222.333")
	}
}

// TestLinhaSemTextoNenhumNaoEhRejeitada: as duas colunas de texto vazias dão
// descrição vazia — a linha continua valendo, porque tem data e valor, e é a
// pessoa que decide o que fazer com ela na revisão.
func TestLinhaSemTextoNenhumNaoEhRejeitada(t *testing.T) {
	res, err := parse(t, extrato(
		"01/08/2026;;;-10,00;0,00",
	), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)
	require.Empty(t, res.Rejected)
	assert.Equal(t, "", res.Rows[0].Description)
	assert.Equal(t, int64(1000), res.Rows[0].AmountCents)
}

// ---------------------------------------------------------------------------
// Classificação — allowlist pequena, sobre o Histórico, e nada além dela
// ---------------------------------------------------------------------------

func TestClassificacaoDoExtrato(t *testing.T) {
	casos := []struct {
		historico string
		descricao string
		esperada  importer.Suggestion
	}{
		{"Pagamento de fatura", "Cartão Inter", importer.SuggestionCardPayment},
		{"PAGAMENTO DE FATURA", "", importer.SuggestionCardPayment},
		// Não é "contém fatura": o boleto da conta de luz não vira pagamento
		// de cartão — e é assim que o Inter escreve um boleto.
		{"Pagamento efetuado", "FATURA ENERGIA EXEMPLO S.A.", importer.SuggestionNone},
		// O prefixo é sobre o Histórico, que vai na frente; "fatura" na
		// contraparte não conta.
		{"Pix enviado", "Pagamento de fatura da loja", importer.SuggestionNone},
		{"Pix recebido", "Fulano", importer.SuggestionNone},
	}
	for _, c := range casos {
		t.Run(c.historico+"/"+c.descricao, func(t *testing.T) {
			res, err := parse(t, extrato(
				"01/08/2026;"+c.historico+";"+c.descricao+";-10,00;0,00",
			), "")
			require.NoError(t, err)
			require.Len(t, res.Rows, 1)
			assert.Equal(t, c.esperada, res.Rows[0].Suggestion)
		})
	}
}

// TestSugestaoNaoMexeNoKind: pagamento de fatura com valor POSITIVO continua
// sendo entrada — a sugestão só sugere.
func TestSugestaoNaoMexeNoKind(t *testing.T) {
	res, err := parse(t, extrato(
		"01/08/2026;Pagamento de fatura;Cartão;500,00;0,00",
	), "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)

	assert.Equal(t, transaction.KindIncome, res.Rows[0].Kind, "o sinal decide o kind, não a sugestão")
	assert.Equal(t, importer.SuggestionCardPayment, res.Rows[0].Suggestion)
}

// ---------------------------------------------------------------------------
// Guardas de formato
// ---------------------------------------------------------------------------

// TestOutrosExtratosNaoEntramPeloInter: forçar o parser do Inter num arquivo do
// C6 ou do Nubank (e vice-versa) tem de falhar — não "funcionar" com o número ou
// o sinal errado.
func TestOutrosExtratosNaoEntramPeloInter(t *testing.T) {
	c6Dir := filepath.Join("..", "c6", "testdata")
	nubankDir := filepath.Join("..", "nubank", "testdata")

	_, err := parse(t, lerFixtureDe(t, c6Dir, "c6_checking_v1.csv"), inter.CheckingFormatID)
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatUnknown)

	_, err = parse(t, lerFixtureDe(t, nubankDir, "nubank_checking_v1.csv"), inter.CheckingFormatID)
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatUnknown)

	_, err = parse(t, lerFixture(t, "inter_checking_v1.csv"), c6.CheckingFormatID)
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatUnknown)

	_, err = parse(t, lerFixture(t, "inter_checking_v1.csv"), nubank.CheckingFormatID)
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatUnknown)
}

// TestColunaExtraNoFimContinuaLendo: o banco acrescentar uma coluna ao fim do
// cabeçalho não derruba a importação (confiança fraca, spec 0004 §7.1).
func TestColunaExtraNoFimContinuaLendo(t *testing.T) {
	raw := []byte("Data Lançamento;Histórico;Descrição;Valor;Saldo;Categoria\n" +
		"01/08/2026;Pix enviado ;Fulano;-10,00;0,00;Transferências\n")
	res, err := parse(t, raw, "")
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)
	assert.Equal(t, inter.CheckingFormatID, res.FormatID)
	assert.Equal(t, int64(1000), res.Rows[0].AmountCents)
}

// TestColunaFaltandoNaoEhReconhecida: sem a coluna de valor não há o que
// importar — e o parser não pode "adivinhar" outra coluna no lugar.
func TestColunaFaltandoNaoEhReconhecida(t *testing.T) {
	raw := []byte("Data Lançamento;Histórico;Descrição;Saldo\n" +
		"01/08/2026;Pix enviado ;Fulano;0,00\n")
	_, err := parse(t, raw, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatUnknown)
}
