package csvtext

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCentsCasosReais(t *testing.T) {
	casos := []struct {
		entrada  string
		formato  NumberFormat
		esperado int64
		nota     string
	}{
		// Extrato de conta corrente: ponto decimal, sem milhar.
		{"-20.00", DecimalPoint, -2000, "valor negativo do extrato"},
		{"1600.00", DecimalPoint, 160000, ""},
		{"-2859.82", DecimalPoint, -285982, ""},
		{"0.00", DecimalPoint, 0, ""},

		// Fatura de cartão: vírgula decimal.
		{"33,70", DecimalComma, 3370, "valor do cartão"},
		{"7,95", DecimalComma, 795, ""},

		// O caso que quebra quem faz trim ingênuo: ESPAÇO depois do sinal,
		// mais ponto de milhar.
		{"- 2.859,82", DecimalComma, -285982, "espaço após o sinal + milhar"},
		{"- 53,81", DecimalComma, -5381, "espaço após o sinal"},
		{"+ 53,81", DecimalComma, 5381, ""},

		// Milhar válido nos dois formatos.
		{"1.234,56", DecimalComma, 123456, ""},
		{"12.345,60", DecimalComma, 1234560, ""},
		{"999.999.999,99", DecimalComma, MaxAmountCents, "teto exato"},
		{"1,234.56", DecimalPoint, 123456, ""},
		{"999,999,999.99", DecimalPoint, MaxAmountCents, "teto exato"},

		// Sem casas decimais e com uma só.
		{"20", DecimalComma, 2000, "sem casas decimais"},
		{"33,7", DecimalComma, 3370, "uma casa decimal"},
		{"-7", DecimalPoint, -700, ""},

		// Moeda e espaços em volta.
		{"R$ 1.234,56", DecimalComma, 123456, ""},
		{"-R$ 20,00", DecimalComma, -2000, ""},
		{"R$ -20,00", DecimalComma, -2000, ""},
		{"  33,70  ", DecimalComma, 3370, ""},
		{"1 234,56", DecimalComma, 123456, "NBSP (U+00A0) como separador de milhar"},

		// Zero com sinal continua zero.
		{"-0,00", DecimalComma, 0, ""},
	}

	for _, c := range casos {
		nome := c.entrada
		if c.nota != "" {
			nome += " (" + c.nota + ")"
		}
		t.Run(nome, func(t *testing.T) {
			got, err := ParseCents(c.entrada, c.formato)
			require.NoError(t, err)
			require.Equal(t, c.esperado, got)
		})
	}
}

func TestParseCentsRecusa(t *testing.T) {
	casos := []struct {
		nome    string
		entrada string
		formato NumberFormat
		erro    error
	}{
		{"vazio", "", DecimalComma, ErrInvalidNumber},
		{"só espaços", "   ", DecimalComma, ErrInvalidNumber},
		{"texto", "abc", DecimalComma, ErrInvalidNumber},
		{"texto com dígito", "12abc", DecimalComma, ErrInvalidNumber},
		{"só o sinal", "-", DecimalComma, ErrInvalidNumber},
		{"sinal duplo", "--20,00", DecimalComma, ErrInvalidNumber},
		{"1,2,3 em formato ponto", "1,2,3", DecimalPoint, ErrInvalidNumber},
		{"1,2,3 em formato vírgula", "1,2,3", DecimalComma, ErrInvalidNumber},
		{"1.2.3 em formato vírgula", "1.2.3", DecimalComma, ErrInvalidNumber},
		{"1.2.3 em formato ponto", "1.2.3", DecimalPoint, ErrInvalidNumber},
		{"três casas decimais", "12,345", DecimalComma, ErrInvalidNumber},
		{"quatro casas decimais", "0.1234", DecimalPoint, ErrInvalidNumber},
		{"termina no separador", "20,", DecimalComma, ErrInvalidNumber},
		{"começa no separador", ",50", DecimalComma, ErrInvalidNumber},
		{"agrupamento de milhar curto", "1.23,45", DecimalComma, ErrInvalidNumber},
		{"agrupamento de milhar longo", "1.2345,67", DecimalComma, ErrInvalidNumber},
		{"notação científica", "1e5", DecimalComma, ErrInvalidNumber},
		{"parênteses de negativo", "(20,00)", DecimalComma, ErrInvalidNumber},
		{"formato não declarado", "20,00", NumberFormat(0), ErrInvalidNumber},
		{"formato inexistente", "20,00", NumberFormat(99), ErrInvalidNumber},
		{"valor absurdo", "1.000.000.000,00", DecimalComma, ErrAmountOutOfRange},
		{"valor absurdo negativo", "-99.999.999.999,99", DecimalComma, ErrAmountOutOfRange},
		{"muitos dígitos", "123456789012345678901234", DecimalComma, ErrAmountOutOfRange},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := ParseCents(c.entrada, c.formato)
			require.ErrorIs(t, err, c.erro)
		})
	}
}

// TestParseCentsNaoUsaFloat é o teste que FALHARIA se alguém trocasse a
// conversão textual por `strconv.ParseFloat`.
//
// Todos os valores desta lista são representados em binário por um número
// LIGEIRAMENTE MENOR do que parecem, então `int64(v * 100)` trunca um centavo
// para baixo. O clássico é 0.29: em float64 ele vale 0.28999999999999998, e
// `int64(0.29*100)` devolve 28.
func TestParseCentsNaoUsaFloat(t *testing.T) {
	armadilhas := []struct {
		texto         string
		esperado      int64
		comFloatSeria int64
	}{
		{"0.29", 29, 28},
		{"0.57", 57, 56},
		{"1.13", 113, 112},
		{"1.15", 115, 114},
		{"2.01", 201, 200},
		{"2.30", 230, 229},
		{"4.35", 435, 434},
		{"128.14", 12814, 12813},
		{"128.70", 12870, 12869},
	}

	for _, a := range armadilhas {
		t.Run(a.texto, func(t *testing.T) {
			got, err := ParseCents(a.texto, DecimalPoint)
			require.NoError(t, err)
			require.Equal(t, a.esperado, got)
			require.NotEqual(t, a.comFloatSeria, got,
				"%s: este é exatamente o resultado de int64(%s*100) em float64 — o parser voltou a usar ponto flutuante", a.texto, a.texto)
		})
	}
}

// TestParseCentsExaustivoAteDezMil percorre todos os valores de R$ 0,00 a
// R$ 99,99 nos dois formatos e confere contra aritmética inteira pura. Se
// qualquer etapa passar por float, alguma dezena destas falha.
func TestParseCentsExaustivoAteDezMil(t *testing.T) {
	for cents := int64(0); cents < 10_000; cents++ {
		esperado := cents
		ponto := fmt.Sprintf("%d.%02d", cents/100, cents%100)
		virgula := fmt.Sprintf("%d,%02d", cents/100, cents%100)

		got, err := ParseCents(ponto, DecimalPoint)
		require.NoError(t, err, ponto)
		require.Equal(t, esperado, got, ponto)

		got, err = ParseCents(virgula, DecimalComma)
		require.NoError(t, err, virgula)
		require.Equal(t, esperado, got, virgula)

		got, err = ParseCents("-"+virgula, DecimalComma)
		require.NoError(t, err, virgula)
		require.Equal(t, -esperado, got, virgula)
	}
}

// TestPacoteNaoUsaPontoFlutuante lê o código-fonte de produção do pacote e
// recusa qualquer sinal de ponto flutuante. O teste de valores acima pega o uso
// no caminho conhecido; este pega o dia em que alguém acrescentar um caminho
// novo (ADR-003: float é proibido em qualquer camada).
func TestPacoteNaoUsaPontoFlutuante(t *testing.T) {
	// Parênteses e espaço fazem parte dos padrões: procuramos código, não a
	// menção em comentário — a doc de ParseCents cita ParseFloat justamente
	// para dizer que é proibido.
	proibidos := []string{
		"ParseFloat(", "float64(", "float32(", " float64", " float32",
		"math/big", "big.Float",
	}
	for _, arquivo := range fontesDoPacote(t) {
		conteudo, err := os.ReadFile(arquivo)
		require.NoError(t, err)
		for _, p := range proibidos {
			require.NotContains(t, string(conteudo), p,
				"%s usa %q — dinheiro é int64 em centavos (ADR-003)", filepath.Base(arquivo), p)
		}
	}
}

// fontesDoPacote devolve os .go de produção (sem os _test.go).
func fontesDoPacote(t *testing.T) []string {
	t.Helper()
	entradas, err := os.ReadDir(".")
	require.NoError(t, err)
	var fontes []string
	for _, e := range entradas {
		nome := e.Name()
		if e.IsDir() || !strings.HasSuffix(nome, ".go") || strings.HasSuffix(nome, "_test.go") {
			continue
		}
		fontes = append(fontes, nome)
	}
	require.NotEmpty(t, fontes)
	return fontes
}
