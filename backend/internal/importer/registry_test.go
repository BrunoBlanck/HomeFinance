package importer_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
)

// parserFalso é um parser de teste: assinatura declarada, leitura trivial.
type parserFalso struct {
	id          string
	instituicao importer.Institution
	documento   importer.DocKind
	assinatura  importer.Signature
}

func (p parserFalso) ID() string                        { return p.id }
func (p parserFalso) Institution() importer.Institution { return p.instituicao }
func (p parserFalso) DocKind() importer.DocKind         { return p.documento }

func (p parserFalso) Detect(h []string, sep rune) importer.Confidence {
	return p.assinatura.Match(h, sep)
}

func (p parserFalso) Parse(ctx context.Context, t *csvtext.Table, l importer.Limits) (importer.ParseResult, error) {
	return importer.ParseTable(ctx, p, t, l, func(rec []string) (importer.ParsedRow, string) {
		data, err := csvtext.ParseDate(rec[0], csvtext.DateISO)
		if err != nil {
			return importer.ParsedRow{}, importer.RejectInvalidDate
		}
		cents, err := csvtext.ParseCents(rec[1], csvtext.DecimalPoint)
		if err != nil {
			return importer.ParsedRow{}, importer.RejectInvalidAmount
		}
		kind, abs, ok := importer.KindFromSigned(cents, importer.SignNegativeIsOutflow)
		if !ok {
			return importer.ParsedRow{}, importer.RejectZeroAmount
		}
		desc, norm := importer.Describe(rec[2])
		return importer.ParsedRow{
			Kind: kind, OccurredOn: data, AmountCents: abs,
			Description: desc, DescriptionNorm: norm,
		}, ""
	})
}

func novoFalso(id string) parserFalso {
	return parserFalso{
		id:          id,
		instituicao: importer.InstitutionNubank,
		documento:   importer.DocKindCheckingStatement,
		assinatura:  importer.NewSignature(',', "data", "valor", "descricao"),
	}
}

// ---------------------------------------------------------------------------
// Assinatura de cabeçalho
// ---------------------------------------------------------------------------

func TestSignatureMatch(t *testing.T) {
	assinatura := importer.NewSignature(',', "Data", "Valor", "Descrição")

	casos := []struct {
		nome     string
		header   []string
		sep      rune
		esperado importer.Confidence
	}{
		{"exato", []string{"Data", "Valor", "Descrição"}, ',', importer.ConfidenceExact},
		{"caixa e acento variados", []string{"DATA", "valor", "descricao"}, ',', importer.ConfidenceExact},
		{"espaço nas pontas", []string{" Data ", "Valor", "Descrição "}, ',', importer.ConfidenceExact},
		// Coluna nova no FIM do arquivo não pode derrubar a importação.
		{"coluna extra ao final", []string{"Data", "Valor", "Descrição", "Saldo"}, ',', importer.ConfidenceWeak},
		// Coluna faltando quebra, que é o certo.
		{"coluna faltando", []string{"Data", "Valor"}, ',', importer.ConfidenceNone},
		// A ordem faz parte da assinatura.
		{"ordem trocada", []string{"Valor", "Data", "Descrição"}, ',', importer.ConfidenceNone},
		// Coluna extra no COMEÇO desloca tudo: não é o mesmo leiaute.
		{"coluna extra no começo", []string{"Banco", "Data", "Valor", "Descrição"}, ',', importer.ConfidenceNone},
		{"separador diferente", []string{"Data", "Valor", "Descrição"}, ';', importer.ConfidenceNone},
		{"cabeçalho vazio", []string{}, ',', importer.ConfidenceNone},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			assert.Equal(t, c.esperado, assinatura.Match(c.header, c.sep))
		})
	}
}

func TestSignatureSeparadorLivre(t *testing.T) {
	// Separator zero aceita qualquer separador — para o banco que exporta `,`
	// numa região e `;` em outra.
	assinatura := importer.NewSignature(0, "Data", "Valor")
	assert.Equal(t, importer.ConfidenceExact, assinatura.Match([]string{"Data", "Valor"}, ';'))
	assert.Equal(t, importer.ConfidenceExact, assinatura.Match([]string{"Data", "Valor"}, ','))
}

// ---------------------------------------------------------------------------
// Registro
// ---------------------------------------------------------------------------

func TestNewRegistryRecusaConfiguracaoInvalida(t *testing.T) {
	t.Run("id repetido", func(t *testing.T) {
		_, err := importer.NewRegistry(novoFalso("a.b.v1"), novoFalso("a.b.v1"))
		require.Error(t, err)
		assert.ErrorIs(t, err, importer.ErrParserMisconfigured)
	})

	t.Run("id vazio", func(t *testing.T) {
		_, err := importer.NewRegistry(novoFalso("   "))
		require.Error(t, err)
		assert.ErrorIs(t, err, importer.ErrParserMisconfigured)
	})

	t.Run("instituição fora da allowlist", func(t *testing.T) {
		p := novoFalso("a.b.v1")
		p.instituicao = "banco_inventado"
		_, err := importer.NewRegistry(p)
		require.Error(t, err)
		assert.ErrorIs(t, err, importer.ErrParserMisconfigured)
	})

	t.Run("documento fora da allowlist", func(t *testing.T) {
		p := novoFalso("a.b.v1")
		p.documento = "boleto"
		_, err := importer.NewRegistry(p)
		require.Error(t, err)
		assert.ErrorIs(t, err, importer.ErrParserMisconfigured)
	})

	t.Run("parser nulo", func(t *testing.T) {
		_, err := importer.NewRegistry(nil)
		require.Error(t, err)
		assert.ErrorIs(t, err, importer.ErrParserMisconfigured)
	})
}

func TestSelectZeroCandidatos(t *testing.T) {
	reg, err := importer.NewRegistry(novoFalso("a.b.v1"))
	require.NoError(t, err)

	_, err = reg.Select([]string{"coluna", "estranha"}, ',', "")
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatUnknown)
}

// TestSelectDoisCandidatosNuncaEscolheOPrimeiro é o teste que impede o defeito
// mais caro do importador: escolher "o primeiro que casou" é como a fatura
// entraria um dia pelo parser do extrato, com o sinal invertido do começo ao
// fim.
func TestSelectDoisCandidatosNuncaEscolheOPrimeiro(t *testing.T) {
	reg, err := importer.NewRegistry(novoFalso("zzz.segundo.v1"), novoFalso("aaa.primeiro.v1"))
	require.NoError(t, err)

	p, err := reg.Select([]string{"data", "valor", "descricao"}, ',', "")
	assert.Nil(t, p)
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatAmbiguous)

	var ambiguo *importer.AmbiguousFormatError
	require.ErrorAs(t, err, &ambiguo)
	// Ordem determinística, para a tela mostrar sempre a mesma lista.
	assert.Equal(t, []string{"aaa.primeiro.v1", "zzz.segundo.v1"}, ambiguo.Candidates)
}

// TestSelectExatoGanhaDoFraco é o ÚNICO desempate automático que existe, e ele
// é explicável numa frase: casar o cabeçalho inteiro ganha de casar tolerando
// colunas extras.
func TestSelectExatoGanhaDoFraco(t *testing.T) {
	exato := novoFalso("exato.v1")
	exato.assinatura = importer.NewSignature(',', "data", "valor", "descricao", "saldo")

	fraco := novoFalso("fraco.v1") // três colunas: casa com extra ao final

	reg, err := importer.NewRegistry(fraco, exato)
	require.NoError(t, err)

	p, err := reg.Select([]string{"data", "valor", "descricao", "saldo"}, ',', "")
	require.NoError(t, err)
	assert.Equal(t, "exato.v1", p.ID())
}

func TestSelectDesempateExplicito(t *testing.T) {
	reg, err := importer.NewRegistry(novoFalso("a.primeiro.v1"), novoFalso("b.segundo.v1"))
	require.NoError(t, err)

	p, err := reg.Select([]string{"data", "valor", "descricao"}, ',', "b.segundo.v1")
	require.NoError(t, err)
	assert.Equal(t, "b.segundo.v1", p.ID())
}

func TestFormatIDsEhAllowlistDeterministica(t *testing.T) {
	reg, err := importer.NewRegistry(novoFalso("z.v1"), novoFalso("a.v1"), novoFalso("m.v1"))
	require.NoError(t, err)
	assert.Equal(t, []string{"a.v1", "m.v1", "z.v1"}, reg.FormatIDs())
}

// ---------------------------------------------------------------------------
// Preâmbulo
// ---------------------------------------------------------------------------

func TestOpenDocumentPulaPreambulo(t *testing.T) {
	reg, err := importer.NewRegistry(novoFalso("a.b.v1"))
	require.NoError(t, err)

	raw := []byte(
		"Relatório de movimentações\n" +
			"Titular: Fulano de Tal Silva\n" +
			"Saldo anterior: 1.234,56\n" +
			"\n" +
			"data,valor,descricao\n" +
			"2026-08-04,-20.00,Compra Exemplo\n")

	doc, err := reg.OpenDocument(raw)
	require.NoError(t, err)
	assert.Equal(t, 5, doc.HeaderLine)
	assert.Equal(t, []string{"data", "valor", "descricao"}, doc.Table.Header)
	assert.Len(t, doc.Table.Rows, 1)
}

func TestOpenDocumentDesisteDepoisDoTeto(t *testing.T) {
	reg, err := importer.NewRegistry(novoFalso("a.b.v1"))
	require.NoError(t, err)

	var b strings.Builder
	for range importer.MaxPreambleLines + 1 {
		b.WriteString("linha de preâmbulo qualquer\n")
	}
	b.WriteString("data,valor,descricao\n2026-08-04,-20.00,Compra Exemplo\n")

	_, err = reg.OpenDocument([]byte(b.String()))
	require.Error(t, err)
	assert.ErrorIs(t, err, importer.ErrFormatUnknown)
}

func TestOpenDocumentIgnoraBOM(t *testing.T) {
	reg, err := importer.NewRegistry(novoFalso("a.b.v1"))
	require.NoError(t, err)

	// Um CSV reaberto e salvo no Excel volta com EF BB BF na frente.
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte("data,valor,descricao\n2026-08-04,-20.00,Compra\n")...)

	doc, err := reg.OpenDocument(raw)
	require.NoError(t, err)
	assert.Equal(t, 1, doc.HeaderLine)
	assert.Equal(t, "data", doc.Table.Header[0])
}
