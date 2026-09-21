package textnorm_test

import (
	"testing"
	"unicode/utf8"

	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeDobraCaixaEAcento(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"Alimentação":       "alimentacao",
		"ALIMENTAÇÃO":       "alimentacao",
		"alimentacao":       "alimentacao",
		"Moradia":           "moradia",
		"Saúde":             "saude",
		"Educação":          "educacao",
		"Cartão de Crédito": "cartao de credito",
		"Ônibus":            "onibus",
		"Água e Esgoto":     "agua e esgoto",
	}
	for entrada, esperado := range casos {
		assert.Equal(t, esperado, textnorm.Normalize(entrada), "entrada %q", entrada)
	}
}

func TestNormalizeColapsaEspacoEmBrancoDeTodoTipo(t *testing.T) {
	t.Parallel()

	// Os três precisam colidir: quem digita "Conta  Corrente" com dois espaços
	// não está criando uma conta diferente de "Conta Corrente".
	esperado := "conta corrente"
	for _, entrada := range []string{
		"Conta Corrente",
		"  Conta Corrente  ",
		"Conta  Corrente",
		"Conta\tCorrente",
		"Conta\n Corrente",
	} {
		assert.Equal(t, esperado, textnorm.Normalize(entrada), "entrada %q", entrada)
	}
}

func TestNormalizeVazioEApenasEspaco(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", textnorm.Normalize(""))
	assert.Equal(t, "", textnorm.Normalize("   "))
	assert.Equal(t, "", textnorm.Normalize("\t\n "))
}

// A saída precisa estar em NFC. Se ela saísse em NFD, dois textos que a pessoa
// vê iguais teriam bytes diferentes, e a comparação de igualdade — que é por
// bytes, no Go e no banco — falharia sem explicação visível.
func TestNormalizeDevolveNFC(t *testing.T) {
	t.Parallel()

	// "ação" digitado com o "ç" já composto e com "c" + cedilha combinante.
	composto := "Ação"
	decomposto := "Ação"

	assert.Equal(t, textnorm.Normalize(composto), textnorm.Normalize(decomposto))
	assert.Equal(t, "acao", textnorm.Normalize(decomposto))
	assert.True(t, utf8.ValidString(textnorm.Normalize(decomposto)))
}

func TestEqual(t *testing.T) {
	t.Parallel()

	assert.True(t, textnorm.Equal("Alimentação", "  alimentacao "))
	assert.True(t, textnorm.Equal("Conta  Corrente", "conta corrente"))
	assert.False(t, textnorm.Equal("Moradia", "Morada"))
	assert.False(t, textnorm.Equal("Salário", "Salários"))
}

// Documenta o limite declarado no pacote, para que ele seja uma decisão
// registrada e não uma surpresa numa revisão futura.
func TestLimiteConhecidoLetrasQueNaoSeDecompoem(t *testing.T) {
	t.Parallel()

	// "ø" e "ł" têm o traço embutido no ponto de código — não são letra +
	// marca combinante — então a NFD não tem o que separar e eles sobrevivem.
	assert.Equal(t, "øre", textnorm.Normalize("Øre"))
	assert.False(t, textnorm.Equal("Øre", "Ore"))

	// No mesmo texto, o que É letra + acento continua sendo dobrado: em
	// "ŁÓDŹ" o "Ó" e o "Ź" perdem o acento e só o "Ł" resiste.
	assert.Equal(t, "łodz", textnorm.Normalize("ŁÓDŹ"))
}
