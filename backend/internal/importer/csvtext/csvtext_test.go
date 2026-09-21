package csvtext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// bom é o U+FEFF escrito de forma ESCAPADA.
//
// Nunca cole o caractere cru num fonte Go: o compilador só aceita BOM como
// primeiro byte do arquivo e falha com "illegal byte order mark" em qualquer
// outra posição — um erro que derruba o pacote inteiro e aparece como dezenas
// de "undefined" nos arquivos vizinhos.
const bom = "\uFEFF"

func TestDecodeRemoveBOM(t *testing.T) {
	raw := []byte(bom + "Data,Valor\n04/08/2026,-20.00\n")

	texto, enc, err := Decode(raw)
	require.NoError(t, err)
	require.Equal(t, UTF8, enc)
	require.True(t, strings.HasPrefix(texto, "Data,"), "o BOM tem de sair antes do cabeçalho: %q", texto[:10])

	tabela, err := Parse(raw)
	require.NoError(t, err)
	require.Equal(t, []string{"Data", "Valor"}, tabela.Header)
	require.Equal(t, 0, tabela.IndexOf("Data"), "com BOM sobrando, o mapeamento de coluna não acharia 'Data'")
}

func TestDecodeRemoveBOMInterno(t *testing.T) {
	texto, _, err := Decode([]byte("Data,Valor\n04/08/2026," + bom + "-20.00\n"))
	require.NoError(t, err)
	require.NotContains(t, texto, bom)
}

func TestDecodeUTF8(t *testing.T) {
	texto, enc, err := Decode([]byte("Descrição,Agência\nTransferência,1\n"))
	require.NoError(t, err)
	require.Equal(t, UTF8, enc)
	require.Contains(t, texto, "Descrição")
}

// TestDecodeWindows1252 cobre a faixa 0x80–0x9F, que é exatamente onde o
// Latin-1 puro falharia: ela é vazia no ISO-8859-1 e, no CP1252, carrega o
// travessão, as aspas curvas e o euro — que é onde eles caem nos arquivos de
// banco brasileiros.
func TestDecodeWindows1252(t *testing.T) {
	// "Transferência – Pagamento 'X' €" em CP1252:
	//   0xEA = ê · 0x96 = – (en dash) · 0x91/0x92 = aspas curvas · 0x80 = €
	raw := []byte{
		'T', 'r', 'a', 'n', 's', 'f', 'e', 'r', 0xEA, 'n', 'c', 'i', 'a',
		' ', 0x96, ' ', 0x91, 'X', 0x92, ' ', 0x80, '\n',
	}
	require.False(t, utf8.Valid(raw), "a fixture precisa ser UTF-8 INVÁLIDO para exercitar o fallback")

	texto, enc, err := Decode(raw)
	require.NoError(t, err)
	require.Equal(t, Windows1252, enc)
	require.Equal(t, "Transferência – ‘X’ €\n", texto)
}

func TestDecodeRecusa(t *testing.T) {
	casos := []struct {
		nome string
		raw  []byte
		erro error
	}{
		{"vazio", nil, ErrEmpty},
		{"só o BOM", []byte(bom), ErrEmpty},
		{"grande demais", make([]byte, MaxInputBytes+1), ErrTooLarge},
		{"UTF-16 LE", []byte{0xFF, 0xFE, 'D', 0x00}, ErrUnsupportedEncoding},
		{"UTF-16 BE", []byte{0xFE, 0xFF, 0x00, 'D'}, ErrUnsupportedEncoding},
		{"conteúdo binário", []byte("Data,Valor\n\x00\x01\n"), ErrBinaryContent},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, _, err := Decode(c.raw)
			require.ErrorIs(t, err, c.erro)
		})
	}
}

func TestSniffSeparator(t *testing.T) {
	casos := []struct {
		nome     string
		texto    string
		esperado rune
	}{
		{"vírgula", "Data,Valor,Descrição\n1,2,3\n", ','},
		{"ponto e vírgula (padrão brasileiro)", "Data;Valor;Descrição\n1;2;3\n", ';'},
		{"tabulação", "Data\tValor\tDescrição\n", '\t'},
		{"CRLF não atrapalha", "Data;Valor\r\n1;2\r\n", ';'},
		{"ponto e vírgula vence vírgula de conteúdo", `Data;"Silva, Maria";Valor` + "\n", ';'},
		{"vírgula dentro de aspas não elege a vírgula", `"a, b, c, d";x` + "\n", ';'},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			sep, err := SniffSeparator(c.texto)
			require.NoError(t, err)
			require.Equal(t, c.esperado, sep)
		})
	}
}

func TestSniffSeparatorSemSeparador(t *testing.T) {
	_, err := SniffSeparator("ColunaUnica\nvalor\n")
	require.ErrorIs(t, err, ErrNoSeparator)
}

func TestParseTabelaSimples(t *testing.T) {
	tabela, err := Parse([]byte("Data;Valor;Descrição\n04/08/2026;-20,00;Pagamento\n05/08/2026;33,70;Lanche\n"))
	require.NoError(t, err)
	require.Equal(t, ';', tabela.Separator)
	require.Equal(t, UTF8, tabela.Encoding)
	require.Equal(t, []string{"Data", "Valor", "Descrição"}, tabela.Header)
	require.Len(t, tabela.Rows, 2)
	require.Equal(t, []string{"05/08/2026", "33,70", "Lanche"}, tabela.Rows[1])
}

func TestParsePreservaEspacosDoCampo(t *testing.T) {
	// Sem TrimLeadingSpace: "- 53,81" chega inteiro ao ParseCents, que é quem
	// sabe o que fazer com o espaço depois do sinal.
	tabela, err := Parse([]byte("date,title,amount\n2026-08-22,Ajuste,\"- 53,81\"\n"))
	require.NoError(t, err)
	require.Equal(t, "- 53,81", tabela.Rows[0][2])
}

func TestParseWithSeparator(t *testing.T) {
	tabela, err := ParseWithSeparator([]byte("a;b\n1;2\n"), ';')
	require.NoError(t, err)
	require.Equal(t, ';', tabela.Separator)

	_, err = ParseWithSeparator([]byte("a|b\n"), '|')
	require.ErrorIs(t, err, ErrNoSeparator)
}

func TestParseRecusa(t *testing.T) {
	t.Run("colunas demais", func(t *testing.T) {
		header := strings.Repeat("c,", MaxColumns) + "ultima"
		_, err := Parse([]byte(header + "\n"))
		require.ErrorIs(t, err, ErrTooManyColumns)
	})

	t.Run("linhas demais", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("a,b\n")
		for range MaxRows + 1 {
			b.WriteString("1,2\n")
		}
		_, err := Parse([]byte(b.String()))
		require.ErrorIs(t, err, ErrTooManyRows)
	})

	t.Run("exatamente no limite de linhas passa", func(t *testing.T) {
		var b strings.Builder
		b.WriteString("a,b\n")
		for range MaxRows {
			b.WriteString("1,2\n")
		}
		tabela, err := Parse([]byte(b.String()))
		require.NoError(t, err)
		require.Len(t, tabela.Rows, MaxRows)
	})

	t.Run("linha longa demais", func(t *testing.T) {
		linha := "a," + strings.Repeat("x", MaxLineRunes)
		_, err := Parse([]byte("a,b\n" + linha + "\n"))
		require.ErrorIs(t, err, ErrLineTooLong)
	})

	t.Run("limite de linha é em runas, não em bytes", func(t *testing.T) {
		// 900 runas acentuadas = 1800 bytes: passa do limite em bytes e não
		// passa do limite em runas, que é o que vale.
		linha := strings.Repeat("ç", 900)
		tabela, err := Parse([]byte("a,b\nx," + linha + "\n"))
		require.NoError(t, err)
		require.Equal(t, linha, tabela.Rows[0][1])
	})

	t.Run("largura irregular", func(t *testing.T) {
		_, err := Parse([]byte("a,b,c\n1,2\n"))
		require.ErrorIs(t, err, ErrMalformed)
	})

	t.Run("aspas soltas (LazyQuotes desligado)", func(t *testing.T) {
		_, err := Parse([]byte("a,b\n\"nao fecha,2\n"))
		require.ErrorIs(t, err, ErrMalformed)
	})

	t.Run("cabeçalho repetido", func(t *testing.T) {
		_, err := Parse([]byte("Descrição,descricao\n1,2\n"))
		require.ErrorIs(t, err, ErrDuplicateHeader)
	})

	t.Run("arquivo vazio", func(t *testing.T) {
		_, err := Parse(nil)
		require.ErrorIs(t, err, ErrEmpty)
	})
}

func TestIndexOf(t *testing.T) {
	tabela := &Table{Header: []string{"Data", "Valor", "Descrição", ""}}
	require.Equal(t, 0, tabela.IndexOf("data"))
	require.Equal(t, 2, tabela.IndexOf("DESCRICAO"))
	require.Equal(t, 2, tabela.IndexOf(" descrição "))
	require.Equal(t, -1, tabela.IndexOf("inexistente"))
	require.Equal(t, -1, tabela.IndexOf(""))
}

// TestParseFixturesReais roda o parser nos arquivos anonimizados de verdade.
// Eles são apenas LIDOS — as fixtures pertencem à tarefa do adaptador Nubank.
func TestParseFixturesReais(t *testing.T) {
	t.Run("extrato de conta corrente", func(t *testing.T) {
		raw := lerFixture(t, "nubank_checking_v1.csv")
		tabela, err := Parse(raw)
		require.NoError(t, err)
		require.Equal(t, ',', tabela.Separator)
		require.Equal(t, UTF8, tabela.Encoding)
		require.Equal(t, []string{"Data", "Valor", "Identificador", "Descrição"}, tabela.Header)
		require.NotEmpty(t, tabela.Rows)

		iData, iValor := tabela.IndexOf("Data"), tabela.IndexOf("Valor")
		require.GreaterOrEqual(t, iData, 0)
		require.GreaterOrEqual(t, iValor, 0)
		for _, linha := range tabela.Rows {
			_, err := ParseDate(linha[iData], DateDMY)
			require.NoError(t, err, "data %q", linha[iData])
			_, err = ParseCents(linha[iValor], DecimalPoint)
			require.NoError(t, err, "valor %q", linha[iValor])
		}

		require.Equal(t, "04/08/2026", tabela.Rows[0][iData])
		cents, err := ParseCents(tabela.Rows[0][iValor], DecimalPoint)
		require.NoError(t, err)
		require.Equal(t, int64(-2000), cents)
	})

	t.Run("fatura de cartão", func(t *testing.T) {
		raw := lerFixture(t, "nubank_card_statement_v1.csv")
		tabela, err := Parse(raw)
		require.NoError(t, err)
		require.Equal(t, ',', tabela.Separator)
		require.Equal(t, []string{"date", "title", "amount"}, tabela.Header)

		iDate, iAmount := tabela.IndexOf("date"), tabela.IndexOf("amount")
		for _, linha := range tabela.Rows {
			_, err := ParseDate(linha[iDate], DateISO)
			require.NoError(t, err, "data %q", linha[iDate])
			_, err = ParseCents(linha[iAmount], DecimalComma)
			require.NoError(t, err, "valor %q", linha[iAmount])
		}

		cents, err := ParseCents(tabela.Rows[0][iAmount], DecimalComma)
		require.NoError(t, err)
		require.Equal(t, int64(3370), cents)
	})
}

func lerFixture(t *testing.T, nome string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "nubank", "testdata", nome))
	require.NoError(t, err)
	return raw
}
