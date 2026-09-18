package report_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Bordas e casos de abuso levantados na validação de QA da E6a. O que está
// aqui é o que NÃO estava coberto pelos testes da implementação: hierarquia
// irregular (pai arquivado, grupo gordo), empate exato conferido em execuções
// repetidas E com a ordem das linhas embaralhada (um GROUP BY sem ORDER BY
// pode devolver em qualquer ordem), sobra do maior resto caindo em dois níveis
// ao mesmo tempo, contagem alta com valor baixo, meses no extremo da faixa e o
// cenário de banco adulterado.

// --- hierarquia irregular --------------------------------------------------

// Pai ARQUIVADO com filha VIVA: a filha continua dentro do grupo (o grupo não
// vira órfão só porque foi arquivado), o grupo sai com archivedAt preenchido e
// a filha com archivedAt nulo. Dinheiro de categoria arquivada não se apaga
// (PLANOS.md §4.4).
func TestByCategoryPaiArquivadoMantemFilhaNoGrupo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	pai := a.categoria(minhaCasa, "g-pai", "Transporte", category.KindExpense, nil, true)
	filha := a.categoria(minhaCasa, "f-viva", "Combustível", category.KindExpense, ptr(pai.ID), false)
	a.ledger.rows = []report.CategoryTotal{
		linha(ptr(filha.ID), 30_000, 3),
		linha(ptr(pai.ID), 10_000, 1),
	}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	require.Len(t, v.Items, 1, "a filha não é promovida: o pai existe, só está arquivado")
	item := v.Items[0]
	require.NotNil(t, item.ArchivedAt, "o grupo arquivado carrega o instante")
	assert.Equal(t, int64(40_000), item.TotalCents)
	assert.Equal(t, int64(4), item.Count)
	assert.Equal(t, int64(report.BasisPointsTotal), item.ShareBp)
	require.Len(t, item.Children, 1)
	assert.Nil(t, item.Children[0].ArchivedAt, "a filha viva não herda o arquivamento do pai")
	assert.Equal(t, int64(30_000), item.Children[0].TotalCents)
	assert.Equal(t, item.ShareBp, item.DirectShareBp+item.Children[0].ShareBp)
}

// Grupo GORDO: uma casa no teto da taxonomia com quase todas as categorias
// penduradas no mesmo pai. Os invariantes têm de fechar com 180 filhas tanto
// quanto com duas, e Σ filhas + direto tem de dar exatamente o share do grupo.
func TestByCategoryGrupoComMuitasFilhas(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	const nFilhas = 180
	pai := a.categoria(minhaCasa, "g-pai", "Casa", category.KindExpense, nil, false)
	rows := []report.CategoryTotal{linha(ptr(pai.ID), 777, 1)}
	var esperado int64 = 777
	for i := range nFilhas {
		id := fmt.Sprintf("f-%03d", i)
		a.categoria(minhaCasa, id, fmt.Sprintf("Filha %03d", i), category.KindExpense, ptr(pai.ID), false)
		// Valores propositalmente quebrados (primos pequenos) para o maior
		// resto ter trabalho de verdade em 181 partes.
		cents := int64(101 + (i*37)%911)
		esperado += cents
		rows = append(rows, linha(ptr(id), cents, int64(1+i%4)))
	}
	a.ledger.rows = rows

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	require.Len(t, v.Items, 1)
	item := v.Items[0]
	require.Len(t, item.Children, nFilhas)
	assert.Equal(t, esperado, item.TotalCents)
	assert.Equal(t, esperado, v.TotalCents)
	assert.Equal(t, int64(report.BasisPointsTotal), item.ShareBp)

	var somaFilhas int64
	for _, f := range item.Children {
		somaFilhas += f.ShareBp
	}
	assert.Equal(t, int64(report.BasisPointsTotal), somaFilhas+item.DirectShareBp,
		"as 180 filhas e o direto repartem o grupo inteiro, sem sobra nem falta")
}

// --- determinismo ----------------------------------------------------------

// Empates EXATOS entre grupos e entre filhas, conferidos de dois jeitos:
//
//  1. o mesmo pedido repetido N vezes devolve byte a byte a mesma resposta
//     (nada de iteração de map vazando para a saída);
//  2. as MESMAS linhas em ordem EMBARALHADA devolvem a mesma resposta — e isto
//     não é preciosismo: `SumByCategory` é um GROUP BY sem ORDER BY, e a ordem
//     em que as linhas chegam não é contratada por nenhum dos quatro dialetos.
func TestByCategoryEmpatesSaoDeterministicos(t *testing.T) {
	t.Parallel()

	// Três grupos com valor e contagem idênticos (desempate cai no nome) e,
	// dentro do primeiro, três filhas também idênticas.
	montar := func() ([]report.CategoryTotal, *ambiente) {
		a := novoAmbiente(t)
		var rows []report.CategoryTotal
		for _, nome := range []string{"Zebra", "Alface", "Milho"} {
			id := "g-" + strings.ToLower(nome)
			a.categoria(minhaCasa, id, nome, category.KindExpense, nil, false)
			rows = append(rows, linha(ptr(id), 1_000, 2))
		}
		for _, nome := range []string{"Uva", "Banana", "Caqui"} {
			id := "f-" + strings.ToLower(nome)
			a.categoria(minhaCasa, id, nome, category.KindExpense, ptr("g-zebra"), false)
			rows = append(rows, linha(ptr(id), 500, 1))
		}
		rows = append(rows, linha(nil, 1_000, 2)) // balde empatado com os grupos
		return rows, a
	}

	rows, a := montar()
	referencia := serializar(t, a, rows)

	// Ordem dos nomes: o grupo "Zebra" carrega as filhas, então empata em
	// valor com os outros dois só depois de somá-las — o que já prova que a
	// ordenação usa o total dobrado, e não o direto.
	var primeiro report.CategoryReportView
	require.NoError(t, json.Unmarshal([]byte(referencia), &primeiro))
	conferirInvariantes(t, primeiro)

	for i := range 50 {
		_, b := montar()
		assert.Equal(t, referencia, serializar(t, b, rows), "execução %d divergiu com a MESMA entrada", i)
	}

	rng := rand.New(rand.NewPCG(9, 17))
	for i := range 50 {
		embaralhadas := append([]report.CategoryTotal(nil), rows...)
		rng.Shuffle(len(embaralhadas), func(x, y int) {
			embaralhadas[x], embaralhadas[y] = embaralhadas[y], embaralhadas[x]
		})
		_, b := montar()
		assert.Equal(t, referencia, serializar(t, b, embaralhadas),
			"embaralhamento %d mudou a resposta — a ordem do GROUP BY não é contratada", i)
	}
}

// serializar roda o serviço com as linhas dadas e devolve o JSON da resposta.
func serializar(t *testing.T, a *ambiente, rows []report.CategoryTotal) string {
	t.Helper()
	a.ledger.rows = rows
	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// --- maior resto nos dois níveis ao mesmo tempo ----------------------------

// Três grupos em terços (10000 / 3 = 3333 com sobra 1) e, dentro de CADA um,
// três filhas em terços da própria fatia — a sobra tem de ser distribuída no
// nível 1 e no nível 2 na mesma resposta, sem que nenhuma soma fique em 9999.
func TestByCategorySobraCaiEmDoisNiveisNaMesmaResposta(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	var rows []report.CategoryTotal
	for g := range 3 {
		gid := fmt.Sprintf("g-%d", g)
		a.categoria(minhaCasa, gid, fmt.Sprintf("Grupo %d", g), category.KindExpense, nil, false)
		for f := range 3 {
			fid := fmt.Sprintf("f-%d-%d", g, f)
			a.categoria(minhaCasa, fid, fmt.Sprintf("Filha %d %d", g, f), category.KindExpense, ptr(gid), false)
			rows = append(rows, linha(ptr(fid), 100, 1))
		}
	}
	a.ledger.rows = rows

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	require.Len(t, v.Items, 3)
	assert.Equal(t, int64(900), v.TotalCents)

	var somaGrupos int64
	for _, item := range v.Items {
		somaGrupos += item.ShareBp
		var somaFilhas int64
		for _, f := range item.Children {
			somaFilhas += f.ShareBp
			assert.Greater(t, f.ShareBp, int64(0), "terço de terço nunca é fatia zerada aqui")
		}
		assert.Equal(t, item.ShareBp, somaFilhas+item.DirectShareBp)
		assert.Equal(t, int64(0), item.DirectCents)
		assert.Equal(t, int64(0), item.DirectShareBp, "grupo sem lançamento direto não recebe sobra")
	}
	assert.Equal(t, int64(report.BasisPointsTotal), somaGrupos)
	// 10000/3 = 3333 sobrando 1: exatamente um grupo fica com 3334.
	assert.ElementsMatch(t, []int64{3334, 3333, 3333},
		[]int64{v.Items[0].ShareBp, v.Items[1].ShareBp, v.Items[2].ShareBp})
}

// --- contagem alta, valor baixo -------------------------------------------

// Dez mil lançamentos somando 12 centavos: a participação continua exata, a
// contagem não se mistura com o valor e o mês não vira divisão por zero.
func TestByCategoryContagemAltaComValorBaixo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	g1 := a.categoria(minhaCasa, "g-1", "Centavos", category.KindExpense, nil, false)
	g2 := a.categoria(minhaCasa, "g-2", "Migalhas", category.KindExpense, nil, false)
	a.ledger.rows = []report.CategoryTotal{
		linha(ptr(g1.ID), 9, 6_000),
		linha(ptr(g2.ID), 3, 3_999),
		linha(nil, 0, 1), // um lançamento de R$ 0,00 sem categoria
	}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	assert.Equal(t, int64(12), v.TotalCents)
	assert.Equal(t, int64(10_000), v.Count)
	require.Len(t, v.Items, 3)
	assert.Equal(t, int64(7_500), v.Items[0].ShareBp, "9 de 12 centavos = 75 %")
	assert.Equal(t, int64(2_500), v.Items[1].ShareBp)
	assert.Equal(t, int64(0), v.Items[2].ShareBp, "valor zero não recebe fatia, nem a sobra")
	assert.Equal(t, int64(1), v.Items[2].Count, "mas continua contando lançamento")
}

// --- meses no extremo da faixa --------------------------------------------

// A faixa de `month` é a do contrato (YearMonth), não a de sanidade de
// `occurredOn`: `0001-01` e `9999-12` são 200 com mês vazio — quem pede um mês
// sem lançamento recebe um relatório vazio, não um erro. Ano `0000` é recusado
// (civil.New exige ano ≥ 1).
func TestByCategoryMesesNoExtremoDaFaixa(t *testing.T) {
	t.Parallel()

	for _, mes := range []string{"0001-01", "9999-12", "1000-01", "2100-12"} {
		t.Run("aceita "+mes, func(t *testing.T) {
			a := novoAmbiente(t)
			v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: mes})
			require.NoError(t, err)
			assert.Equal(t, mes, v.Month)
			assert.Equal(t, int64(0), v.TotalCents)
			assert.NotNil(t, v.Items)
			assert.Empty(t, v.Items)
			require.Len(t, a.ledger.chamadas, 1)
			assert.Equal(t, mes, a.ledger.chamadas[0].mes, "vai ao banco como veio, sem normalização")
		})
	}

	for _, mes := range []string{"0000-01", "0000-12", "10000-01", "-001-01", "999-01"} {
		t.Run("recusa "+mes, func(t *testing.T) {
			a := novoAmbiente(t)
			_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: mes})
			require.Error(t, err)
			assert.ErrorIs(t, err, transaction.ErrInvalidMonth)
			assert.Empty(t, a.ledger.chamadas, "mês inválido não chega ao banco")
		})
	}
}

// --- banco adulterado ------------------------------------------------------

// O cenário que o revisor de segurança vai levantar: o banco foi adulterado e
// um lançamento da MINHA casa aponta para a categoria da vizinha (grupo e
// filha). A tolerância é deliberada — o dinheiro não some do relatório —, mas
// ela não pode custar sigilo: o JSON inteiro não pode conter nem o id nem o
// nome da categoria alheia, e o total tem de continuar certo.
func TestByCategoryDadoAdulteradoNaoVazaNadaDaOutraCasa(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	grupoAlheio := a.categoria(outraCasa, "g-alheio-0001", "Divórcio Da Vizinha", category.KindExpense, nil, false)
	filhaAlheia := a.categoria(outraCasa, "f-alheia-0002", "Advogado", category.KindExpense, ptr(grupoAlheio.ID), false)
	minha := a.categoria(minhaCasa, "g-minha", "Mercado", category.KindExpense, nil, false)

	a.ledger.rows = []report.CategoryTotal{
		linha(ptr(minha.ID), 50_000, 5),
		linha(ptr(grupoAlheio.ID), 30_000, 2),
		linha(ptr(filhaAlheia.ID), 20_000, 3),
	}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	bruto, err := json.Marshal(v)
	require.NoError(t, err)
	corpo := string(bruto)
	for _, proibido := range []string{grupoAlheio.ID, filhaAlheia.ID, "Divórcio", "Vizinha", "Advogado"} {
		assert.NotContains(t, corpo, proibido, "a resposta não pode conter nada da outra casa")
	}

	// O dinheiro continua no relatório, no balde explícito.
	assert.Equal(t, int64(100_000), v.TotalCents)
	assert.Equal(t, int64(10), v.Count)
	require.Len(t, v.Items, 2)
	assert.Equal(t, int64(50_000), v.Items[0].TotalCents)
	require.Nil(t, v.Items[1].CategoryID, "as duas linhas alheias caem no MESMO balde")
	assert.Equal(t, int64(50_000), v.Items[1].TotalCents)
	assert.Equal(t, int64(5), v.Items[1].Count)

	// O aviso leva só os ids — que já eram conhecidos de quem adulterou o
	// banco — e nunca nome nem centavos.
	log := a.logs.String()
	assert.Contains(t, log, grupoAlheio.ID)
	assert.Contains(t, log, filhaAlheia.ID)
	assert.NotContains(t, log, "Divórcio")
	assert.NotContains(t, log, "Advogado")
	assert.NotContains(t, log, "30000")
	assert.NotContains(t, log, "20000")
}

// Adulteração no sentido contrário: a categoria da vizinha é PAI da minha
// filha. A filha é promovida a grupo (o dinheiro fica), e o nome do pai alheio
// não aparece em lugar nenhum.
func TestByCategoryPaiDeOutraCasaNaoViraGrupo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	paiAlheio := a.categoria(outraCasa, "g-alheio", "Conta Secreta", category.KindExpense, nil, false)
	minhaFilha := a.categoria(minhaCasa, "f-minha", "Padaria", category.KindExpense, ptr(paiAlheio.ID), false)
	a.ledger.rows = []report.CategoryTotal{linha(ptr(minhaFilha.ID), 1_234, 2)}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	bruto, err := json.Marshal(v)
	require.NoError(t, err)
	assert.NotContains(t, string(bruto), paiAlheio.ID)
	assert.NotContains(t, string(bruto), "Secreta")

	require.Len(t, v.Items, 1)
	require.NotNil(t, v.Items[0].CategoryID)
	assert.Equal(t, minhaFilha.ID, *v.Items[0].CategoryID)
	assert.Equal(t, int64(1_234), v.Items[0].TotalCents)
}
