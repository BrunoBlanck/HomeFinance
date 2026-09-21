package transaction_test

import (
	"net/http"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Filtro de CATEGORIA de GET /transactions — o atalho "Ver lançamentos" do
// relatório por categoria.
//
// O que estes testes travam:
//
//   - GRUPO INCLUI AS FILHAS. É a razão de o atalho existir: a linha do grupo
//     no relatório soma o grupo mais as subcategorias, e abrir a lista num
//     subconjunto daquele número seria um atalho que mente sobre o que foi
//     clicado;
//   - o RESUMO recebe o mesmo conjunto da lista. Sem isto, a faixa mostraria
//     os totais do mês inteiro embaixo das linhas de uma categoria só;
//   - categoria de OUTRA CASA é 404, nunca lista vazia (S1);
//   - o recorte combina com o de tipo sem que um afrouxe o outro — é aqui que
//     "nunca misturar receita com despesa" é medido do lado do servidor.

// cenarioDeCategorias monta setembro com um GRUPO de duas filhas, um gasto
// lançado direto no grupo, uma categoria solta e uma despesa sem categoria.
//
//	Alimentação (grupo) .. 300,00 direto
//	  Mercado ............ 150,00
//	  Restaurante ........  50,00
//	Transporte ...........  80,00
//	(sem categoria) ......  20,00
//	Salário (receita) ... 5.000,00
func cenarioDeCategorias(t *testing.T) (*ambiente, map[string]string) {
	t.Helper()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)

	alimentacao := amb.categoria(minhaCasa, "cat-alimentacao", "Alimentação", category.KindExpense)
	mercado := amb.categorias.add(category.Category{
		ID: "cat-mercado", HouseholdID: minhaCasa, Name: "Mercado",
		Kind: category.KindExpense, ParentID: ptr(alimentacao.ID),
	})
	restaurante := amb.categorias.add(category.Category{
		ID: "cat-restaurante", HouseholdID: minhaCasa, Name: "Restaurante",
		Kind: category.KindExpense, ParentID: ptr(alimentacao.ID),
	})
	transporte := amb.categoria(minhaCasa, "cat-transporte", "Transporte", category.KindExpense)
	salario := amb.categoria(minhaCasa, "cat-salario", "Salário", category.KindIncome)

	semear := func(kind string, valor int64, dia int, descricao string, cat *string) {
		amb.repo.semear(transaction.Transaction{
			HouseholdID: minhaCasa, AccountID: "acc-1", Kind: kind, AmountCents: valor,
			Description: descricao, CategoryID: cat,
			OccurredOn: civil.MustNew(2026, 9, dia), CompetenceMonth: "2026-09",
		})
	}
	semear(transaction.KindExpense, 300_00, 5, "Feira do bairro", ptr(alimentacao.ID))
	semear(transaction.KindExpense, 150_00, 6, "Mercado", ptr(mercado.ID))
	semear(transaction.KindExpense, 50_00, 7, "Restaurante", ptr(restaurante.ID))
	semear(transaction.KindExpense, 80_00, 8, "Ônibus", ptr(transporte.ID))
	semear(transaction.KindExpense, 20_00, 9, "Padaria", nil)
	semear(transaction.KindIncome, 5_000_00, 10, "Salário", ptr(salario.ID))

	return amb, map[string]string{
		"alimentacao": alimentacao.ID,
		"mercado":     mercado.ID,
		"restaurante": restaurante.ID,
		"transporte":  transporte.ID,
		"salario":     salario.ID,
	}
}

func listarPorCategoria(t *testing.T, amb *ambiente, categoryID, grupo string) transaction.ListView {
	t.Helper()
	v, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
		Month: "2026-09", CategoryID: categoryID, KindGroup: grupo,
	})
	require.NoError(t, err)
	return v
}

// O coração da tarefa: pedir o GRUPO traz as filhas junto, e o total bate com
// o que o relatório soma naquela linha (300 + 150 + 50 = 500,00).
func TestFiltroPorGrupoTrazAsSubcategoriasJunto(t *testing.T) {
	t.Parallel()

	amb, ids := cenarioDeCategorias(t)
	view := listarPorCategoria(t, amb, ids["alimentacao"], "")

	assert.Len(t, view.Items, 3)
	assert.Equal(t, int64(3), view.Summary.Count)
	assert.Equal(t, int64(500_00), view.Summary.ExpenseCents)

	// E o conjunto que desceu para a consulta é o grupo MAIS as filhas, não
	// só o grupo: é a expansão do serviço, e é ela que faz o total fechar.
	require.NotEmpty(t, amb.repo.categoriasNaLista)
	assert.ElementsMatch(t,
		[]string{ids["alimentacao"], ids["mercado"], ids["restaurante"]},
		amb.repo.categoriasNaLista[0],
	)
}

// Subcategoria é só ela: quem clicou em "Mercado" não quer o grupo inteiro.
func TestFiltroPorSubcategoriaTrazSomenteEla(t *testing.T) {
	t.Parallel()

	amb, ids := cenarioDeCategorias(t)
	view := listarPorCategoria(t, amb, ids["mercado"], "")

	require.Len(t, view.Items, 1)
	assert.Equal(t, "Mercado", view.Items[0].Description)
	assert.Equal(t, int64(150_00), view.Summary.ExpenseCents)
	// Folha não tem filha e a árvore tem dois níveis: a consulta de
	// subcategorias não acontece.
	assert.Zero(t, amb.categorias.filhasPedidas[ids["mercado"]])
}

// O resumo fala da MESMA janela da lista. Sem o conjunto aqui, a faixa
// mostraria os 5.600,00 do mês embaixo das três linhas de Alimentação.
func TestResumoRecebeOMesmoRecorteDeCategoriaDaLista(t *testing.T) {
	t.Parallel()

	amb, ids := cenarioDeCategorias(t)
	view := listarPorCategoria(t, amb, ids["transporte"], "")

	assert.Equal(t, int64(80_00), view.Summary.ExpenseCents)
	assert.Zero(t, view.Summary.IncomeCents)
	// Dentro de uma categoria não existe pendência: toda linha tem categoria.
	assert.Zero(t, view.Summary.UncategorizedCount)

	require.NotEmpty(t, amb.repo.categoriasNoResumo)
	assert.Equal(t, amb.repo.categoriasNaLista[0], amb.repo.categoriasNoResumo[0],
		"lista e resumo precisam falar da mesma janela")
}

// Os dois recortes são predicados independentes, e nenhum afrouxa o outro.
func TestFiltroDeCategoriaCombinaComOFiltroDeTipo(t *testing.T) {
	t.Parallel()

	amb, ids := cenarioDeCategorias(t)

	// Despesas dentro do grupo: as três linhas continuam lá.
	assert.Len(t, listarPorCategoria(t, amb, ids["alimentacao"], transaction.KindGroupExpense).Items, 3)
	// Receitas dentro do MESMO grupo: nenhuma. O tipo não cede à categoria.
	assert.Empty(t, listarPorCategoria(t, amb, ids["alimentacao"], transaction.KindGroupIncome).Items)
	// E transferência nunca tem categoria (ADR-016): a combinação é vazia.
	assert.Empty(t, listarPorCategoria(t, amb, ids["alimentacao"], transaction.KindGroupTransfer).Items)
}

// Categoria de outra casa é 404, igual à inexistente — nunca lista vazia, que
// seria indistinguível de "categoria sem lançamentos" e confirmaria a
// existência do recurso alheio (S1).
func TestFiltroPorCategoriaDeOutraCasaE404(t *testing.T) {
	t.Parallel()

	amb, _ := cenarioDeCategorias(t)
	amb.categoria(outraCasa, "cat-alheia", "Mercado da vizinha", category.KindExpense)

	for _, id := range []string{"cat-alheia", "cat-que-nao-existe"} {
		_, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
			Month: "2026-09", CategoryID: id,
		})
		require.ErrorIs(t, err, transaction.ErrNotFound, "id %q", id)
	}
}

// A chave repetida é 400 na borda, e a mensagem não ecoa nenhum dos ids
// recebidos — entrada bruta de terceiro não volta pela porta do erro (S8).
func TestCategoryIDRepetidoNaQueryE400(t *testing.T) {
	t.Parallel()

	casos := []struct{ nome, query string }{
		{"ids diferentes", "categoryId=cat-mercado&categoryId=cat-transporte"},
		{"ids iguais", "categoryId=cat-mercado&categoryId=cat-mercado"},
		{"segunda vazia", "categoryId=cat-mercado&categoryId="},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			amb := novoHTTPAmbiente(t)
			amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
			amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
			amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 30_00, 9)

			rec := amb.chamar(t, minhaCasa, http.MethodGet,
				"/transactions?month=2026-09&"+c.query, "", amb.handler.List)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			require.Contains(t, campos, "categoryId")
			assert.Equal(t, "Informe a categoria uma única vez.", campos["categoryId"])
			// A mensagem não ecoa nenhum dos ids recebidos (S8).
			assert.NotContains(t, campos["categoryId"], "cat-mercado")

			assert.Zero(t, amb.repo.listagens, "nada sai para o banco")
			assert.Empty(t, amb.repo.resumosPedidos)
		})
	}
}
