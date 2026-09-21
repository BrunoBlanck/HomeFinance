package transaction

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Teste INTERNO da dobra em Go do filtro de tipo (spec 0004 §12, emenda
// E2d) e da tabela de invariantes que a acompanha.
//
// Ele é interno porque as duas funções são a REGRA, e não o resultado: pela
// borda pública só se vê o número final, e metade das invariantes de
// conferirResumo é INALCANÇÁVEL por lá justamente porque recortarResumo está
// correto. Elas existem para o dia em que ele deixar de estar — e um guarda
// que ninguém consegue testar é um guarda que ninguém sabe se funciona.

// janelaDeExemplo é a MESMA janela nas cinco opções, na forma em que o
// repositório a devolve: agregados líquidos do marcado, mais a linha CRUA por
// kind.
//
//	receitas ... 2 linhas vivas (5.100,00), 1 sem categoria
//	resgate .... 1 linha (500,00), marcada
//	despesas ... 2 linhas (330,00), 1 sem categoria
//	aporte ..... 1 linha (2.000,00), marcada
//	transferência: 2 pernas (800,00 cada), com Uncategorized IGUAL a Count —
//	é assim que a projeção crua devolve, e é a armadilha que o recorte evita.
func janelaDeExemplo() Summary {
	return Summary{
		IncomeCents:   5_100_00,
		ExpenseCents:  330_00,
		NetCents:      5_100_00 - 330_00,
		Count:         8,
		Uncategorized: 2,
		InvestedCents: 2_000_00,
		RedeemedCents: 500_00,
		ByKind: []SummaryKindTotals{
			{Kind: KindExpense, Count: 3, TotalCents: 2_330_00, Uncategorized: 1, MarkedTotalCents: 2_000_00, MarkedCount: 1},
			{Kind: KindIncome, Count: 3, TotalCents: 5_600_00, Uncategorized: 1, MarkedTotalCents: 500_00, MarkedCount: 1},
			{Kind: KindTransferIn, Count: 1, TotalCents: 800_00, Uncategorized: 1},
			{Kind: KindTransferOut, Count: 1, TotalCents: 800_00, Uncategorized: 1},
		},
	}
}

// A tabela normativa do arquiteto, linha por linha.
func TestRecortarResumoSegueATabelaNormativa(t *testing.T) {
	t.Parallel()

	casos := []struct {
		grupo         string
		count         int64
		income        int64
		expense       int64
		uncategorized int64
	}{
		{grupo: "", count: 8, income: 5_100_00, expense: 330_00, uncategorized: 2},
		{grupo: KindGroupIncome, count: 2, income: 5_100_00, expense: 0, uncategorized: 1},
		{grupo: KindGroupExpense, count: 2, income: 0, expense: 330_00, uncategorized: 1},
		{grupo: KindGroupTransfer, count: 2, income: 0, expense: 0, uncategorized: 0},
		{grupo: KindGroupInvestment, count: 2, income: 0, expense: 0, uncategorized: 0},
	}
	for _, c := range casos {
		nome := c.grupo
		if nome == "" {
			nome = "tudo"
		}
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			out, err := recortarResumo(janelaDeExemplo(), c.grupo)
			require.NoError(t, err)

			assert.Equal(t, c.count, out.Count, "contagem")
			assert.Equal(t, c.income, out.IncomeCents, "receita")
			assert.Equal(t, c.expense, out.ExpenseCents, "despesa")
			assert.Equal(t, c.uncategorized, out.Uncategorized, "pendência")
			assert.Equal(t, out.IncomeCents-out.ExpenseCents, out.NetCents, "a identidade do líquido")

			// Os dois campos de reconciliação são os MESMOS nas cinco opções:
			// eles descrevem o que SAIU de receita e despesa, não a janela
			// (ADR-029e).
			assert.Equal(t, int64(2_000_00), out.InvestedCents)
			assert.Equal(t, int64(500_00), out.RedeemedCents)

			// A matéria-prima atravessa intacta — é sobre ela que
			// conferirResumo verifica `0 ≤ marcado ≤ total` por kind, e sem
			// ela o recorte esconderia corrupção.
			assert.Equal(t, janelaDeExemplo().ByKind, out.ByKind)

			// E o recorte passa na própria conferência.
			assert.NoError(t, conferirResumo(out, c.grupo, 2))
		})
	}
}

// A soma dos quatro grupos fecha com o Tudo — a partição, provada sobre a
// aritmética e não só sobre o dublê do repositório.
func TestRecortarResumoParticionaAContagem(t *testing.T) {
	t.Parallel()

	janela := janelaDeExemplo()
	var soma int64
	for _, g := range []string{KindGroupIncome, KindGroupExpense, KindGroupTransfer, KindGroupInvestment} {
		out, err := recortarResumo(janela, g)
		require.NoError(t, err)
		soma += out.Count
	}
	assert.Equal(t, janela.Count, soma)
}

// Grupo desconhecido falha FECHADO também aqui — devolver a janela inteira sob
// um recorte que ninguém sabe qual é seria vazar o que o filtro existia para
// esconder. O erro não carrega o valor.
func TestRecortarResumoRecusaGrupoDesconhecido(t *testing.T) {
	t.Parallel()

	out, err := recortarResumo(janelaDeExemplo(), "transfer_out")
	require.ErrorIs(t, err, ErrUnknownKindGroup)
	assert.NotContains(t, err.Error(), "transfer_out")
	assert.Equal(t, Summary{}, out, "nada parcial volta de um recorte recusado")
}

// A tabela de invariantes de conferirResumo, linha por linha. Cada caso é um
// resumo que NÃO PODE chegar à tela: ou ele é impossível (parcela negativa,
// marcado maior que o total), ou ele contradiz o próprio contrato (líquido que
// não é a diferença, recorte que publica o lado que deveria estar zerado).
func TestConferirResumoRecusaOQueNaoPodeSerPublicado(t *testing.T) {
	t.Parallel()

	base := func() Summary { return janelaDeExemplo() }

	casos := map[string]struct {
		grupo  string
		montar func(Summary) Summary
	}{
		"receita negativa": {montar: func(s Summary) Summary {
			s.IncomeCents, s.NetCents = -1, -1-s.ExpenseCents
			return s
		}},
		"despesa negativa": {montar: func(s Summary) Summary {
			s.ExpenseCents, s.NetCents = -1, s.IncomeCents+1
			return s
		}},
		"aporte negativo":   {montar: func(s Summary) Summary { s.InvestedCents = -1; return s }},
		"resgate negativo":  {montar: func(s Summary) Summary { s.RedeemedCents = -1; return s }},
		"contagem negativa": {montar: func(s Summary) Summary { s.Count = -1; return s }},
		"pendência negativa": {montar: func(s Summary) Summary {
			s.Uncategorized = -1
			return s
		}},
		"líquido que não é a diferença": {montar: func(s Summary) Summary {
			s.NetCents++
			return s
		}},
		"marcado maior que o total, em dinheiro": {montar: func(s Summary) Summary {
			s.ByKind[0].MarkedTotalCents = s.ByKind[0].TotalCents + 1
			return s
		}},
		"marcado maior que o total, em contagem": {montar: func(s Summary) Summary {
			s.ByKind[0].MarkedCount = s.ByKind[0].Count + 1
			return s
		}},
		"linha crua com contagem negativa": {montar: func(s Summary) Summary {
			s.ByKind[0].Count, s.ByKind[0].MarkedCount = -1, -1
			return s
		}},
		"linha crua com pendência negativa": {montar: func(s Summary) Summary {
			s.ByKind[0].Uncategorized = -1
			return s
		}},
		// As quatro abaixo são inalcançáveis hoje — recortarResumo zera
		// exatamente estes campos. São o guarda para o dia em que ele deixar
		// de zerar, que é o dia em que a tela passaria a mostrar o número do
		// lado que o filtro não pediu.
		"receitas publicando despesa": {grupo: KindGroupIncome, montar: func(s Summary) Summary {
			s.ExpenseCents, s.NetCents = 1, s.IncomeCents-1
			return s
		}},
		"despesas publicando receita": {grupo: KindGroupExpense, montar: func(s Summary) Summary {
			s.IncomeCents, s.NetCents = 1, 1-s.ExpenseCents
			return s
		}},
		"transferências publicando dinheiro": {grupo: KindGroupTransfer, montar: func(s Summary) Summary {
			s.IncomeCents, s.ExpenseCents, s.NetCents = 0, 0, 0
			s.Uncategorized = 1
			return s
		}},
		"investimentos publicando pendência": {grupo: KindGroupInvestment, montar: func(s Summary) Summary {
			s.IncomeCents, s.ExpenseCents, s.NetCents = 0, 0, 0
			s.Uncategorized = 1
			return s
		}},
	}

	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			err := conferirResumo(c.montar(base()), c.grupo, 2)
			require.ErrorIs(t, err, errResumoInconsistente, "número impossível não pode chegar à tela")

			// A mensagem leva só CONTAGENS: centavos não entram em log (S8), e
			// nenhum dos valores da janela pode aparecer nela.
			assert.Contains(t, err.Error(), "categorias_marcadas=2")
			assert.NotContains(t, err.Error(), "510000")
			assert.NotContains(t, err.Error(), "200000")
		})
	}
}

// O caso legítimo que NÃO pode ser confundido com parcela impossível: líquido
// negativo é o mês que fechou no vermelho.
func TestConferirResumoAceitaLiquidoNegativo(t *testing.T) {
	t.Parallel()

	s := janelaDeExemplo()
	s.IncomeCents, s.ExpenseCents = 100_00, 900_00
	s.NetCents = -800_00
	assert.NoError(t, conferirResumo(s, "", 2))
}
