package transaction

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// O LADO DO DINHEIRO guardado no plano (achado A9)
// ---------------------------------------------------------------------------
//
// Os QUATRO EIXOS da qualificação do destino — excluída, arquivada, grupo com
// filha ativa e natureza — não são testados aqui: eles moram em
// category.DestinoAindaQualifica, e os testes por eixo moram junto, em
// internal/category/destino_test.go. A regra já esteve em duas cópias, uma por
// rota, e divergiu; um teste por rota teria divergido igual.
//
// O que é DESTA rota, e por isso fica aqui, é a outra metade do A9: o lado do
// dinheiro ESPERADO. Ele é a entrada que esta rota passa ao predicado, e é
// montado pelo plano — se ele for montado errado, os quatro eixos podem estar
// perfeitos e a gravação ainda assim estar errada.

// TestAnotarLadoFechaQuandoOsLadosDivergem: a invariante "uma categoria só
// recebe linhas de UM lado do dinheiro" é garantida por construção (o matcher é
// escolhido pelo `kind` da linha, e as palavras-chave de uma categoria entram
// num matcher só). Este teste cobre o dia em que a construção mudar: o plano não
// pode escolher em silêncio um dos dois lados e gravar — tem de FECHAR, e o
// fechamento é o lado vazio, que reprova qualquer natureza.
func TestAnotarLadoFechaQuandoOsLadosDivergem(t *testing.T) {
	t.Parallel()

	sadia := category.LiveState{ID: "cat-1", Kind: category.KindExpense}
	p := &planoDeCategorizacao{ladoPorCategoria: map[string]string{}}

	p.anotarLado("cat-1", KindExpense)
	assert.Equal(t, KindExpense, p.ladoPorCategoria["cat-1"])
	assert.True(t, category.DestinoAindaQualifica(sadia, true, p.ladoPorCategoria["cat-1"]),
		"um lado só, sem divergência, tem de continuar gravando")

	p.anotarLado("cat-1", KindIncome)
	assert.Empty(t, p.ladoPorCategoria["cat-1"], "lados divergentes têm de apagar o lado")
	assert.False(t, category.DestinoAindaQualifica(sadia, true, p.ladoPorCategoria["cat-1"]),
		"lado apagado tem de reprovar qualquer natureza")

	// E o vazio é GRUDENTO: uma terceira linha do lado original não pode
	// "consertar" a divergência e reabrir a gravação.
	p.anotarLado("cat-1", KindExpense)
	assert.Empty(t, p.ladoPorCategoria["cat-1"], "a divergência não pode ser desfeita por uma linha seguinte")
	assert.False(t, category.DestinoAindaQualifica(sadia, true, p.ladoPorCategoria["cat-1"]))
}

// O lado guardado é o do LANÇAMENTO, e cada categoria guarda o seu: duas
// categorias no mesmo plano, uma de cada lado, não podem se contaminar.
func TestAnotarLadoGuardaUmLadoPorCategoria(t *testing.T) {
	t.Parallel()

	p := &planoDeCategorizacao{ladoPorCategoria: map[string]string{}}
	p.anotarLado("cat-mercado", KindExpense)
	p.anotarLado("cat-salario", KindIncome)
	p.anotarLado("cat-mercado", KindExpense)

	assert.Equal(t, KindExpense, p.ladoPorCategoria["cat-mercado"])
	assert.Equal(t, KindIncome, p.ladoPorCategoria["cat-salario"])
}
