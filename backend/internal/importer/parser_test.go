package importer_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/importer/sanitize"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// ---------------------------------------------------------------------------
// Convenção de sinal
// ---------------------------------------------------------------------------

func TestKindFromSigned(t *testing.T) {
	casos := []struct {
		nome      string
		assinado  int64
		convencao importer.SignConvention
		kind      string
		cents     int64
		ok        bool
	}{
		{"extrato: negativo é saída", -2000, importer.SignNegativeIsOutflow, transaction.KindExpense, 2000, true},
		{"extrato: positivo é entrada", 2000, importer.SignNegativeIsOutflow, transaction.KindIncome, 2000, true},
		{"fatura: positivo é saída", 3370, importer.SignPositiveIsOutflow, transaction.KindExpense, 3370, true},
		{"fatura: negativo é entrada", -285982, importer.SignPositiveIsOutflow, transaction.KindIncome, 285982, true},
		// Zero não obedece a convenção nenhuma: linha rejeitada, nunca valor
		// "consertado" para R$ 0,00.
		{"zero é recusado", 0, importer.SignNegativeIsOutflow, "", 0, false},
		// Convenção não declarada é defeito de programação, e tem de doer.
		{"convenção não declarada", -2000, 0, "", 0, false},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			kind, cents, ok := importer.KindFromSigned(c.assinado, c.convencao)
			assert.Equal(t, c.ok, ok)
			assert.Equal(t, c.kind, kind)
			assert.Equal(t, c.cents, cents)
			if ok {
				assert.Positive(t, cents, "o valor canônico é sempre positivo")
			}
		})
	}
}

// TestKindFromSignedEhSimetrico prova que trocar a convenção troca o kind — e
// só o kind. É a propriedade que faz o teste-ouro dos parsers ter valor.
func TestKindFromSignedEhSimetrico(t *testing.T) {
	for _, assinado := range []int64{-1, -100, -285982, 1, 100, 3370} {
		k1, c1, ok1 := importer.KindFromSigned(assinado, importer.SignNegativeIsOutflow)
		k2, c2, ok2 := importer.KindFromSigned(assinado, importer.SignPositiveIsOutflow)

		require.True(t, ok1)
		require.True(t, ok2)
		assert.Equal(t, c1, c2, "o valor absoluto não depende da convenção")
		assert.NotEqual(t, k1, k2, "o kind depende SÓ da convenção")
	}
}

// ---------------------------------------------------------------------------
// Descrição e chave — a armadilha invisível do dedup
// ---------------------------------------------------------------------------

// TestDescribeNormalizaDepoisDeTruncar é o teste de regressão do erro mais caro
// e mais silencioso desta entrega (ADR-025c).
//
// Se alguém normalizar o texto CRU e truncar depois, a chave derivada calculada
// na prévia deixa de ser a chave da descrição GRAVADA. Nada quebra, nada falha,
// e a deduplicação simplesmente para de funcionar — o usuário descobre semanas
// depois, com o extrato duplicado.
func TestDescribeNormalizaDepoisDeTruncar(t *testing.T) {
	cru := strings.Repeat("ÁGUA ", 40) + "FINAL QUE NÃO CABE"
	require.Greater(t, utf8.RuneCountInString(cru), sanitize.MaxRunes)

	descricao, normalizada := importer.Describe(cru)

	assert.LessOrEqual(t, utf8.RuneCountInString(descricao), sanitize.MaxRunes,
		"a descrição gravada cabe na coluna")
	assert.Equal(t, textnorm.Normalize(descricao), normalizada,
		"a forma normalizada tem de vir da descrição JÁ truncada")
	assert.NotEqual(t, textnorm.Normalize(cru), normalizada,
		"normalizar o texto cru daria outra coisa — e é esse 'outra coisa' que cega o dedup")

	// E o efeito prático: as duas chaves são diferentes. Usar a errada faz a
	// reimportação nunca reencontrar o que ela mesma gravou.
	data := civil.MustNew(2026, 8, 14)
	chaveCerta := dedup.DerivedKey("conta-1", transaction.KindExpense, data, 1100, normalizada)
	chaveErrada := dedup.DerivedKey("conta-1", transaction.KindExpense, data, 1100, textnorm.Normalize(cru))
	assert.NotEqual(t, chaveCerta, chaveErrada)
}

// TestDescribeEhDeterministico: a mesma entrada dá sempre a mesma saída. É o que
// permite a reimportação reencontrar a chave gravada.
func TestDescribeEhDeterministico(t *testing.T) {
	cru := "Transferência enviada pelo Pix - Fulano de Tal Silva - •••.111.222-•• - NU PAGAMENTOS - IP (0260) Agência: 1 Conta: 1000001-1"
	d1, n1 := importer.Describe(cru)
	d2, n2 := importer.Describe(cru)
	assert.Equal(t, d1, d2)
	assert.Equal(t, n1, n2)
	assert.Equal(t, "Pix enviado - Fulano de Tal Silva", d1,
		"identificadores de terceiros saem; o nome fica (§6.8)")
}

// TestSuggestionCasaComTaxonomiaDoDedup trava os dois vocabulários juntos.
//
// O parser sugere e o dedup classifica; se os textos divergirem, a linha
// classificada como pagamento de fatura deixaria de ser reconhecida como tal na
// revisão — e entraria como despesa comum.
func TestSuggestionCasaComTaxonomiaDoDedup(t *testing.T) {
	assert.Equal(t, string(dedup.StatusCardPayment), string(importer.SuggestionCardPayment))
}

// ---------------------------------------------------------------------------
// ValidExternalID
// ---------------------------------------------------------------------------

func TestValidExternalID(t *testing.T) {
	casos := []struct {
		nome  string
		valor string
		ok    bool
	}{
		{"uuid", "11111111-1111-4111-8111-111111111101", true},
		{"alfanumérico curto", "TX-2026-0001", true},
		{"vazio", "", false},
		{"largura da coluna", strings.Repeat("a", importer.MaxExternalIDBytes), true},
		{"passa da coluna", strings.Repeat("a", importer.MaxExternalIDBytes+1), false},
		{"com espaço", "tx 0001", false},
		{"com quebra de linha", "tx\n0001", false},
		{"com byte nulo", "tx\x000001", false},
		{"não-ASCII", "transação", false},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			assert.Equal(t, c.ok, importer.ValidExternalID(c.valor))
		})
	}
}

// ---------------------------------------------------------------------------
// Limites do laço de leitura
// ---------------------------------------------------------------------------

func csvFalso(linhas int) []byte {
	var b strings.Builder
	b.WriteString("data,valor,descricao\n")
	for i := range linhas {
		b.WriteString("2026-08-01,-10.00,Compra ")
		b.WriteString(string(rune('A' + i%26)))
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func lerFalso(t *testing.T, raw []byte, limites importer.Limits) (importer.ParseResult, error) {
	t.Helper()
	reg, err := importer.NewRegistry(novoFalso("a.b.v1"))
	require.NoError(t, err)
	return reg.Parse(context.Background(), raw, "", limites)
}

func TestRazaoDeRejeicaoNoLimite(t *testing.T) {
	// 5 linhas, 1 rejeitada = exatamente 20%. Passa.
	raw := []byte("data,valor,descricao\n" +
		"2026-08-01,-10.00,Compra A\n" +
		"2026-08-02,-10.00,Compra B\n" +
		"2026-08-03,-10.00,Compra C\n" +
		"2026-08-04,-10.00,Compra D\n" +
		"2026-99-99,-10.00,Data impossível\n")

	res, err := lerFalso(t, raw, importer.DefaultLimits())
	require.NoError(t, err)
	assert.Len(t, res.Rows, 4)
	assert.Len(t, res.Rejected, 1)
}

func TestRazaoDeRejeicaoAcimaDoLimite(t *testing.T) {
	// 4 linhas, 1 rejeitada = 25%. Recusa o arquivo inteiro.
	raw := []byte("data,valor,descricao\n" +
		"2026-08-01,-10.00,Compra A\n" +
		"2026-08-02,-10.00,Compra B\n" +
		"2026-08-03,-10.00,Compra C\n" +
		"2026-99-99,-10.00,Data impossível\n")

	_, err := lerFalso(t, raw, importer.DefaultLimits())
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrTooManyRejected)
}

// folgado tenta afrouxar TODOS os limites de uma vez. Nenhum deles cede.
var folgado = importer.Limits{MaxRows: 1_000_000, MaxRejectedPercent: 100, MaxDateSpanDays: 1_000_000}

func TestLimitsNaoPodeSerAfrouxado(t *testing.T) {
	// Um chamador não pode AUMENTAR um limite de segurança: o teto duro é o
	// default, e o que vem de fora só serve para apertar.

	t.Run("razão de rejeição", func(t *testing.T) {
		raw := []byte("data,valor,descricao\n" +
			"2026-08-01,-10.00,Compra A\n" +
			"2026-08-02,-10.00,Compra B\n" +
			"2026-08-03,-10.00,Compra C\n" +
			"2026-99-99,-10.00,Data impossível\n")

		_, err := lerFalso(t, raw, folgado)
		require.Error(t, err)
		assert.ErrorIs(t, err, importer.ErrTooManyRejected)
	})

	t.Run("janela de datas", func(t *testing.T) {
		raw := []byte("data,valor,descricao\n" +
			"2015-01-01,-10.00,Compra antiga\n" +
			"2026-08-01,-10.00,Compra nova\n")

		_, err := lerFalso(t, raw, folgado)
		require.Error(t, err)
		assert.ErrorIs(t, err, importer.ErrDateSpanTooWide)
	})

	t.Run("teto de linhas", func(t *testing.T) {
		// Aqui quem recusa primeiro é o csvtext, que tem o próprio teto de
		// 10.000 linhas e nem chega a montar a tabela. Os dois tetos existem
		// de propósito: um limite que só exista no chamador deixa de existir no
		// primeiro chamador novo.
		_, err := lerFalso(t, csvFalso(csvtext.MaxRows+1), folgado)
		require.Error(t, err)
		assert.ErrorIs(t, err, csvtext.ErrTooManyRows)
	})
}

func TestLimitsPodeSerApertado(t *testing.T) {
	_, err := lerFalso(t, csvFalso(5), importer.Limits{MaxRows: 3})
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrTooManyRows)
}

func TestLimitsZeradoUsaODefault(t *testing.T) {
	// Limits{} esquecido tem de virar o limite seguro, nunca "sem limite".
	res, err := lerFalso(t, csvFalso(3), importer.Limits{})
	require.NoError(t, err)
	assert.Len(t, res.Rows, 3)

	raw := []byte("data,valor,descricao\n" +
		"2026-08-01,-10.00,Compra A\n" +
		"2026-08-02,-10.00,Compra B\n" +
		"2026-08-03,-10.00,Compra C\n" +
		"2026-99-99,-10.00,Data impossível\n")

	_, err = lerFalso(t, raw, importer.Limits{})
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrTooManyRejected)
}

func TestJanelaDeDatasLargaDemais(t *testing.T) {
	raw := []byte("data,valor,descricao\n" +
		"2015-01-01,-10.00,Compra antiga\n" +
		"2026-08-01,-10.00,Compra nova\n")

	_, err := lerFalso(t, raw, importer.DefaultLimits())
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrDateSpanTooWide)
}

func TestArquivoSoComLinhasRuinsNaoPassa(t *testing.T) {
	raw := []byte("data,valor,descricao\n" +
		"2026-99-99,-10.00,Data impossível\n" +
		"2026-99-98,-10.00,Data impossível\n")

	_, err := lerFalso(t, raw, importer.DefaultLimits())
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrTooManyRejected)
}

// ---------------------------------------------------------------------------
// DaysBetween
// ---------------------------------------------------------------------------

func TestDaysBetween(t *testing.T) {
	casos := []struct {
		nome     string
		a, b     civil.Date
		esperado int
	}{
		{"mesma data", civil.MustNew(2026, 8, 14), civil.MustNew(2026, 8, 14), 0},
		{"um dia", civil.MustNew(2026, 8, 14), civil.MustNew(2026, 8, 15), 1},
		{"é absoluto", civil.MustNew(2026, 8, 15), civil.MustNew(2026, 8, 14), 1},
		{"vira o mês", civil.MustNew(2026, 8, 31), civil.MustNew(2026, 9, 1), 1},
		{"vira o ano", civil.MustNew(2025, 12, 31), civil.MustNew(2026, 1, 1), 1},
		{"ano bissexto", civil.MustNew(2024, 2, 28), civil.MustNew(2024, 3, 1), 2},
		{"ano comum", civil.MustNew(2026, 2, 28), civil.MustNew(2026, 3, 1), 1},
		{"século não bissexto", civil.MustNew(1900, 2, 28), civil.MustNew(1900, 3, 1), 1},
		{"século bissexto", civil.MustNew(2000, 2, 28), civil.MustNew(2000, 3, 1), 2},
		{"um ano comum", civil.MustNew(2026, 1, 1), civil.MustNew(2027, 1, 1), 365},
		{"data zero devolve zero", civil.Date{}, civil.MustNew(2026, 8, 14), 0},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			assert.Equal(t, c.esperado, importer.DaysBetween(c.a, c.b))
		})
	}
}

// ---------------------------------------------------------------------------
// Allowlists de vocabulário
// ---------------------------------------------------------------------------

func TestAllowlistsDeVocabulario(t *testing.T) {
	assert.True(t, importer.InstitutionNubank.Valid())
	assert.True(t, importer.InstitutionC6.Valid())
	assert.False(t, importer.Institution("banco_inventado").Valid())
	assert.False(t, importer.Institution("").Valid())

	assert.True(t, importer.DocKindCheckingStatement.Valid())
	assert.True(t, importer.DocKindCardStatement.Valid())
	assert.False(t, importer.DocKind("boleto").Valid())
}
