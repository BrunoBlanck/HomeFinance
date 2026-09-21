package transaction_test

import (
	"net/http"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Critério 2 da spec 0006, com a CORREÇÃO DE CONTRATO do `arquiteto`: o código
// `CATEGORY_KIND_MISMATCH` que a letra da spec cita **não existe** no enum
// fechado de ErrorCode do projeto. O que a borda responde — e o que este
// arquivo trava — é **422 VALIDATION_FAILED com `fields.categoryId`**.
//
// Por que pelo HTTP, se `pareamento_internal_test.go` já cobre as dezesseis
// combinações: aquele teste prova a REGRA; este prova o CONTRATO. São coisas
// diferentes, e já divergiram uma vez — foi por isso que a correção precisou
// ser feita. A regra pode estar certa e a borda responder 400, ou responder um
// código que o cliente não conhece, ou pôr o erro no campo errado, e a tela
// não saberia onde pintar a mensagem.
//
// Os quatro casos do critério, numa tabela: despesa aceita `investment`,
// receita aceita `redemption`, o lado errado é 422 nos dois sentidos, e
// transferência não aceita natureza nenhuma.

// Ids em forma de UUID: o handler recusa `categoryId` que não pareça um
// (`looksLikeUUID`) ANTES de chamar o serviço, e um id de mentira faria este
// teste medir a guarda de forma, não o pareamento.
const (
	catDespesaPar = "00000000-0000-7000-8000-00000000e001"
	catReceitaPar = "00000000-0000-7000-8000-00000000e002"
	catAportePar  = "00000000-0000-7000-8000-00000000e003"
	catResgatePar = "00000000-0000-7000-8000-00000000e004"
)

// cenarioDePareamento monta uma casa com as QUATRO naturezas e um lançamento
// de cada tipo.
func cenarioDePareamento(t *testing.T) (*httpAmbiente, map[string]transaction.Transaction) {
	t.Helper()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-2", "Poupança", account.KindChecking)
	amb.categoria(minhaCasa, catDespesaPar, "Mercado", category.KindExpense)
	amb.categoria(minhaCasa, catReceitaPar, "Salário", category.KindIncome)
	amb.categoria(minhaCasa, catAportePar, "CDB", category.KindInvestment)
	amb.categoria(minhaCasa, catResgatePar, "Resgates", category.KindRedemption)

	linhas := map[string]transaction.Transaction{
		"despesa": amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "CDB 15 DIAS", 2_000_00, 5),
		"receita": amb.lancamento(minhaCasa, "acc-1", transaction.KindIncome, "RESGATE CDB", 850_00, 6),
	}

	// A transferência nasce com o par, como o serviço a produz: perna que sai
	// numa conta, perna que entra na outra, ligadas pelo grupo.
	grupo := "tg-0000-0000"
	saida := amb.lancamento(minhaCasa, "acc-1", transaction.KindTransferOut, "Para a poupança", 100_00, 7)
	saida.TransferGroupID = &grupo
	amb.repo.semear(saida)
	entrada := amb.lancamento(minhaCasa, "acc-2", transaction.KindTransferIn, "Da conta", 100_00, 7)
	entrada.TransferGroupID = &grupo
	amb.repo.semear(entrada)
	linhas["transferencia"] = saida

	return amb, linhas
}

// O lado CERTO do dinheiro passa: despesa aceita aporte, receita aceita
// resgate. É a alínea (b) do ADR-029 vista pela borda.
func TestPareamentoHTTPAceitaAsNaturezasNovasDoLadoCerto(t *testing.T) {
	t.Parallel()

	amb, linhas := cenarioDePareamento(t)

	for _, caso := range []struct{ lancamento, categoria, nome string }{
		{"despesa", catAportePar, "despesa recebe categoria de APORTE"},
		{"receita", catResgatePar, "receita recebe categoria de RESGATE"},
	} {
		rec := amb.patchCategoria(t, minhaCasa, linhas[caso.lancamento].ID,
			`{"categoryId":"`+caso.categoria+`"}`)
		require.Equal(t, http.StatusOK, rec.Code, "%s: %s", caso.nome, rec.Body.String())

		gravada := amb.repo.linhas[linhas[caso.lancamento].ID]
		require.NotNil(t, gravada.CategoryID, caso.nome)
		assert.Equal(t, caso.categoria, *gravada.CategoryID, caso.nome)
	}
}

// O lado ERRADO é 422 VALIDATION_FAILED em `fields.categoryId` — nunca 400, e
// nunca um código fora do enum.
//
// 422 e não 400 porque o corpo está bem formado e a categoria EXISTE nesta
// casa: é a regra de negócio que recusa, e é isso que a tela precisa poder
// distinguir de "você mandou lixo".
func TestPareamentoHTTPRecusaOLadoErradoCom422EmCategoryId(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome       string
		lancamento string
		categoria  string
	}{
		{"despesa com categoria de RESGATE", "despesa", catResgatePar},
		{"despesa com categoria de RECEITA", "despesa", catReceitaPar},
		{"receita com categoria de APORTE", "receita", catAportePar},
		{"receita com categoria de DESPESA", "receita", catDespesaPar},
	}

	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			t.Parallel()

			amb, linhas := cenarioDePareamento(t)
			rec := amb.patchCategoria(t, minhaCasa, linhas[caso.lancamento].ID,
				`{"categoryId":"`+caso.categoria+`"}`)

			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo,
				"o enum de ErrorCode não tem CATEGORY_KIND_MISMATCH")
			assert.NotContains(t, rec.Body.String(), "CATEGORY_KIND_MISMATCH")
			assert.Contains(t, campos, "categoryId", "a tela precisa saber ONDE pintar a mensagem")

			// A recusa não ecoa o id nem conta a natureza da categoria: o 422
			// não pode virar oráculo de taxonomia.
			assert.NotContains(t, rec.Body.String(), caso.categoria)
			assert.NotContains(t, rec.Body.String(), "investment")
			assert.NotContains(t, rec.Body.String(), "redemption")

			// E nada foi gravado.
			assert.Nil(t, amb.repo.linhas[linhas[caso.lancamento].ID].CategoryID)
			assert.Empty(t, amb.auditor.registros, "recusa não audita")
		})
	}
}

// Transferência não aceita natureza NENHUMA, nem as duas novas (ADR-016).
//
// Ela responde por um caminho próprio (ErrCategoryOnTransfer), e é de propósito
// que este teste não afirme qual: o critério é "recusa e não grava". Afirmar o
// status exato aqui amarraria o teste a uma escolha de borda que o pareamento
// não decide.
func TestTransferenciaNaoAceitaNaturezaDeInvestimento(t *testing.T) {
	t.Parallel()

	amb, linhas := cenarioDePareamento(t)
	perna := linhas["transferencia"]

	for _, categoria := range []string{catAportePar, catResgatePar, catDespesaPar, catReceitaPar} {
		rec := amb.patchCategoria(t, minhaCasa, perna.ID, `{"categoryId":"`+categoria+`"}`)
		require.GreaterOrEqual(t, rec.Code, 400, "categoria %s: %s", categoria, rec.Body.String())
		require.Less(t, rec.Code, 500, "recusa de regra nunca é 500: %s", rec.Body.String())
		assert.Nil(t, amb.repo.linhas[perna.ID].CategoryID,
			"perna de transferência não recebe categoria, de natureza nenhuma")
	}
	assert.Empty(t, amb.auditor.registros)
}
