package report_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// O contrato de `accountGroup` em GET /reports/by-category (ADR-032), visto
// do handler: a recusa idêntica para todo valor hostil, o campo SEMPRE
// presente na resposta, e os parâmetros que tentam escolher a conta por id.

// Todo valor fora de {credit, debit} é 400 em `fields.accountGroup`, com a
// MESMA mensagem byte a byte, sem eco do valor no corpo nem no log, e sem
// tocar em nenhum repositório.
func TestHandlerAccountGroupInvalidoEh400(t *testing.T) {
	t.Parallel()

	hostis := map[string]string{
		"maiúsculas":      "CREDIT",
		"capitalizado":    "Debit",
		"espaço antes":    " credit",
		"espaço depois":   "credit ",
		"nome do tipo":    "credit_card",
		"lista":           "credit,debit",
		"ponto e vírgula": "debit;",
		"sqli":            "'; DROP TABLE transactions;--",
		"nulo antes":      "\x00credit",
		"nulo depois":     "credit\x00",
		"nova linha":      "credit\n",
		"id de conta":     "11111111-1111-7111-8111-000000000001",
		"all":             "all",
		"dez kB":          strings.Repeat("c", 10*1024),
		"largura total":   "ｃｒｅｄｉｔ",
	}

	var corpos []string
	for nome, valor := range hostis {
		t.Run(nome, func(t *testing.T) {
			a := novoHTTPAmbiente(t)
			a.ledger.rows = []report.CategoryAccountTotal{linha(nil, 1, 1)}
			q := url.Values{"month": {"2026-09"}, "accountGroup": {valor}}
			rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?"+q.Encode())

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			assert.Equal(t, map[string]string{"accountGroup": "Informe credit ou debit."}, campos, "só fields.accountGroup")
			assert.Empty(t, a.ledger.chamadas, "recorte inválido não chega ao banco")
			assert.Empty(t, a.contas.chamadas)
			assert.Empty(t, a.cats.chamadas)

			for _, eco := range []string{"DROP", "CREDIT", "Debit", "credit_card", "credit,", "11111111", "ｃ", strings.Repeat("c", 100)} {
				assert.NotContains(t, rec.Body.String(), eco, "a recusa não ecoa o valor enviado")
				assert.NotContains(t, a.logs.String(), eco, "o valor recusado não vai para o log")
			}
			corpos = append(corpos, rec.Body.String())
		})
	}
	for i := 1; i < len(corpos); i++ {
		assert.Equal(t, corpos[0], corpos[i], "toda recusa de accountGroup é idêntica, byte a byte")
	}
}

// Só a PRIMEIRA guarda que falha aparece: mês inválido vence, depois a
// natureza, depois o recorte — nunca dois campos na mesma recusa.
func TestHandlerAccountGroupVemDepoisDeMesEKind(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		alvo  string
		campo string
	}{
		"mês e accountGroup inválidos":  {"/api/v1/reports/by-category?month=x&accountGroup=CREDIT", "month"},
		"kind e accountGroup inválidos": {"/api/v1/reports/by-category?month=2026-09&kind=y&accountGroup=CREDIT", "kind"},
		"os três inválidos":             {"/api/v1/reports/by-category?month=x&kind=y&accountGroup=z", "month"},
		"só accountGroup inválido":      {"/api/v1/reports/by-category?month=2026-09&kind=income&accountGroup=z", "accountGroup"},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			a := novoHTTPAmbiente(t)
			rec := a.chamar(t, minhaCasa, c.alvo)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			_, campos := corpoDeErro(t, rec)
			require.Len(t, campos, 1, "um campo por recusa: %v", campos)
			assert.Contains(t, campos, c.campo)
			assert.Empty(t, a.ledger.chamadas)
		})
	}
}

// A forma exata do JSON com o recorte: `accountGroup` SEMPRE presente —
// `null` sem recorte (ou com uma ocorrência vazia), a constante com recorte —
// e nenhuma chave além das do contrato.
func TestHandlerFormaDoJSONComAccountGroup(t *testing.T) {
	t.Parallel()

	montar := func(t *testing.T) *httpAmbiente {
		t.Helper()
		a := novoHTTPAmbiente(t)
		g := a.categoria(minhaCasa, "11111111-1111-7111-8111-000000000001", "Casa", category.KindExpense, nil, false)
		a.conta(minhaCasa, "cartao", "Cartão", account.KindCreditCard, false)
		a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)
		a.ledger.rows = []report.CategoryAccountTotal{
			linhaConta(ptr(g.ID), "cartao", 3_000, 1),
			linhaConta(nil, "corrente", 1_000, 2),
		}
		return a
	}

	t.Run("sem recorte é null", func(t *testing.T) {
		for _, alvo := range []string{
			"/api/v1/reports/by-category?month=2026-09",
			"/api/v1/reports/by-category?month=2026-09&accountGroup=",
		} {
			a := montar(t)
			rec := a.chamar(t, minhaCasa, alvo)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), `"accountGroup":null`)
			var bruto map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &bruto))
			assert.ElementsMatch(t, []string{"month", "kind", "accountGroup", "totalCents", "count", "items"}, chaves(bruto))
			assert.Equal(t, float64(4_000), bruto["totalCents"])
			assert.Empty(t, a.contas.chamadas, "sem recorte, contas não são lidas")
		}
	})

	t.Run("credit", func(t *testing.T) {
		a := montar(t)
		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09&accountGroup=credit")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{
			"month":"2026-09","kind":"expense","accountGroup":"credit","totalCents":3000,"count":1,
			"items":[
				{"categoryId":"11111111-1111-7111-8111-000000000001","name":"Casa","archivedAt":null,
				 "totalCents":3000,"count":1,"shareBp":10000,
				 "directCents":3000,"directCount":1,"directShareBp":10000,"children":[]}
			]}`, rec.Body.String())
	})

	t.Run("debit", func(t *testing.T) {
		a := montar(t)
		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09&accountGroup=debit")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{
			"month":"2026-09","kind":"expense","accountGroup":"debit","totalCents":1000,"count":2,
			"items":[
				{"categoryId":null,"name":null,"archivedAt":null,
				 "totalCents":1000,"count":2,"shareBp":10000,
				 "directCents":1000,"directCount":2,"directShareBp":10000,"children":[]}
			]}`, rec.Body.String())
	})

	t.Run("credit em casa sem cartão", func(t *testing.T) {
		a := novoHTTPAmbiente(t)
		a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)
		a.ledger.rows = []report.CategoryAccountTotal{linhaConta(nil, "corrente", 1_000, 2)}
		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09&accountGroup=credit")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"month":"2026-09","kind":"expense","accountGroup":"credit","totalCents":0,"count":0,"items":[]}`, rec.Body.String())
	})

	t.Run("income com credit", func(t *testing.T) {
		a := montar(t)
		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09&kind=income&accountGroup=credit")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var v report.CategoryReportView
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
		assert.Equal(t, "income", v.Kind)
		require.NotNil(t, v.AccountGroup)
		assert.Equal(t, "credit", *v.AccountGroup)
		require.Len(t, a.ledger.chamadas, 1)
		assert.Equal(t, "income", a.ledger.chamadas[0].kind)
	})
}

// O recorte NUNCA aceita id de conta: `accountId=`, `account_id=`,
// `householdId=` e afins na query são ignorados sem efeito, e o conjunto de
// cartões continua sendo o da casa do token.
func TestHandlerAccountIdNaQueryNaoEscolheConta(t *testing.T) {
	t.Parallel()

	a := novoHTTPAmbiente(t)
	a.conta(minhaCasa, "meu-cartao", "Meu cartão", account.KindCreditCard, false)
	a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)
	alheio := a.conta(outraCasa, "cartao-alheio", "Cartão alheio", account.KindCreditCard, false)
	a.ledger.rows = []report.CategoryAccountTotal{
		linhaConta(nil, "meu-cartao", 1_000, 1),
		linhaConta(nil, "corrente", 2_000, 1),
	}

	q := url.Values{}
	q.Set("month", "2026-09")
	q.Set("accountGroup", "credit")
	q.Set("accountId", "corrente")
	q.Set("account_id", alheio.ID)
	q.Set("accountIds", "corrente,meu-cartao")
	q.Set("householdId", outraCasa)
	q.Set("kind", "expense")
	rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?"+q.Encode())

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var v report.CategoryReportView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, int64(1_000), v.TotalCents, "só o cartão da casa do token, apesar do accountId= na query")
	require.Len(t, a.ledger.chamadas, 1)
	assert.Equal(t, minhaCasa, a.ledger.chamadas[0].casa)
	require.Len(t, a.contas.chamadas, 1)
	assert.Equal(t, minhaCasa, a.contas.chamadas[0].casa)
	assert.NotContains(t, rec.Body.String(), alheio.ID)
	assert.NotContains(t, rec.Body.String(), outraCasa)
}

// O aviso de conta órfã, pela rota: 200 com o dinheiro em `debit`, o log
// leva só o id e nunca centavos, nome de conta ou a query.
func TestHandlerContaOrfaNaoDerrubaNemVazaNoLog(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	a.conta(minhaCasa, "cartao", "Cartão Preferido", account.KindCreditCard, false)
	a.ledger.rows = []report.CategoryAccountTotal{
		linhaConta(nil, "cartao", 1_000, 1),
		linhaConta(nil, "conta-que-nao-e-da-casa", 654_321, 1),
	}

	rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09&accountGroup=debit")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var v report.CategoryReportView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, int64(654_321), v.TotalCents, "falha aberta: o dinheiro aparece")

	log := a.logs.String()
	assert.Equal(t, 1, strings.Count(log, `"level":"WARN"`))
	assert.Contains(t, log, "conta-que-nao-e-da-casa", "o aviso leva o id")
	assert.NotContains(t, log, "654321")
	assert.NotContains(t, log, "Preferido")
	assert.NotContains(t, log, "accountGroup=", "a query não vai para o log")
	assert.NotContains(t, log, `"level":"ERROR"`)
}
