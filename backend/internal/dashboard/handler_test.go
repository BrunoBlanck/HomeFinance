package dashboard_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/dashboard"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Os testes de handler provam o CONTRATO de GET /dashboard: status, forma
// exata do JSON e o que NÃO sai na resposta. O abuso completo (critérios 13,
// 14 e 16) fica com o `qa-testes`, em handler_abuso_test.go.

type httpAmbiente struct {
	*ambiente
	handler *dashboard.Handler
}

func novoHTTPAmbiente(t *testing.T) *httpAmbiente {
	t.Helper()
	amb := novoAmbiente(t)
	lg := logging.New(amb.logs, logging.Options{Level: "debug", Format: "json"})
	return &httpAmbiente{ambiente: amb, handler: dashboard.NewHandler(amb.svc, lg)}
}

// chamar monta a requisição já com a identidade no contexto — que é o que o
// RequireAuth faz em produção. A casa NUNCA vem da URL.
func (a *httpAmbiente) chamar(t *testing.T, casa, alvo string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, alvo, nil)
	if casa != "" {
		r = r.WithContext(session.NewContext(r.Context(), session.Identity{
			UserID: usuario, HouseholdID: casa, Role: "owner", SessionID: "sess-1",
		}))
	}
	rec := httptest.NewRecorder()
	a.handler.Summary(rec, r)
	return rec
}

// Caminho feliz: 200 com os NOVE campos do schema DashboardSummary, nem um a
// mais nem um a menos, e o mês devolvido como veio.
func TestHandlerCaminhoFeliz(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	a.casaComum()
	a.ledger.rows = []dashboard.KindAccountTotals{
		{Kind: transaction.KindIncome, AccountID: contaCorrente, Count: 4, TotalCents: 535_000,
			MarkedCount: 1, MarkedTotalCents: 35_000},
		{Kind: transaction.KindExpense, AccountID: cartao, Count: 3, TotalCents: 280_000,
			MarkedCount: 1, MarkedTotalCents: 200_000},
	}

	rec := a.chamar(t, minhaCasa, "/api/v1/dashboard?month="+mes)
	require.Equal(t, http.StatusOK, rec.Code)

	var corpo map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo), "corpo: %s", rec.Body.String())

	esperado := map[string]any{
		"month":                   mes,
		"incomeCents":             float64(500_000),
		"incomeCount":             float64(3),
		"creditCardExpenseCents":  float64(80_000),
		"creditCardExpenseCount":  float64(2),
		"investmentNetCents":      float64(165_000),
		"investmentCount":         float64(2),
		"creditCardAccountCount":  float64(1),
		"investmentCategoryCount": float64(2),
	}
	assert.Equal(t, esperado, corpo, "campo a mais ou a menos é divergência de contrato")
}

// O líquido negativo chega ao JSON COM SINAL — é o ponto da feature, e é o
// único campo de dinheiro da resposta sem `minimum: 0`.
func TestHandlerLiquidoNegativoSaiComSinal(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	a.casaComum()
	a.ledger.rows = []dashboard.KindAccountTotals{
		{Kind: transaction.KindIncome, AccountID: contaCorrente, Count: 1, TotalCents: 90_000,
			MarkedCount: 1, MarkedTotalCents: 90_000},
		{Kind: transaction.KindExpense, AccountID: contaCorrente, Count: 1, TotalCents: 50_000,
			MarkedCount: 1, MarkedTotalCents: 50_000},
	}

	rec := a.chamar(t, minhaCasa, "/api/v1/dashboard?month="+mes)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "\"investmentNetCents\":-40000")
}

// Sem sessão é 401, e NENHUMA consulta é executada.
func TestHandlerSemSessaoEh401(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)

	rec := a.chamar(t, "", "/api/v1/dashboard?month="+mes)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Empty(t, a.ledger.chamadas)
	assert.Empty(t, a.cats.chamadas)
	assert.Empty(t, a.contas.chamadas)
}

// Mês malformado é 400 em `fields.month`, com a MESMA redação de
// GET /reports/by-category. Nada é normalizado.
func TestHandlerMesInvalidoEh400(t *testing.T) {
	t.Parallel()

	for nome, alvo := range map[string]string{
		"ausente":         "/api/v1/dashboard",
		"vazio":           "/api/v1/dashboard?month=",
		"mês 13":          "/api/v1/dashboard?month=2026-13",
		"sem zero":        "/api/v1/dashboard?month=2026-9",
		"espaço à frente": "/api/v1/dashboard?month=%202026-09",
	} {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			a := novoHTTPAmbiente(t)
			a.casaComum()

			rec := a.chamar(t, minhaCasa, alvo)
			require.Equal(t, http.StatusBadRequest, rec.Code)

			var env struct {
				Error struct {
					Code   string            `json:"code"`
					Fields map[string]string `json:"fields"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), "corpo: %s", rec.Body.String())
			assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
			assert.Equal(t, "Informe o mês no formato AAAA-MM.", env.Error.Fields["month"])
			assert.Empty(t, a.ledger.chamadas)
		})
	}
}

// `householdId=` na query é ignorado sem efeito: a casa é a do token, e o
// painel lê UM parâmetro só.
func TestHandlerIgnoraParametroDeCasa(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	a.casaComum()
	a.conta(outraCasa, "conta-da-outra", account.KindCreditCard, false)

	rec := a.chamar(t, minhaCasa, "/api/v1/dashboard?month="+mes+"&householdId="+outraCasa+"&accountId=x")
	require.Equal(t, http.StatusOK, rec.Code)

	require.Len(t, a.ledger.chamadas, 1)
	assert.Equal(t, minhaCasa, a.ledger.chamadas[0].casa)
	assert.NotContains(t, rec.Body.String(), outraCasa)
}

// Teto de categorias sobe EMBRULHADO do repositório e vira 500 genérico — não
// 4xx: quem pediu não tem como corrigir a taxonomia da casa naquele pedido.
// A razão fica só no log; a resposta não diz nada.
func TestHandlerTetoDeCategoriasEh500Generico(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	a.casaComum()
	a.ledger.err = fmt.Errorf("resumindo o mês do painel: %w", transaction.ErrTooManyCategories)

	rec := a.chamar(t, minhaCasa, "/api/v1/dashboard?month="+mes)
	require.Equal(t, http.StatusInternalServerError, rec.Code)

	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	assert.Equal(t, httpserver.CodeInternalError, env.Error.Code)
	assert.Equal(t, httpserver.MsgInternalError, env.Error.Message)
	assert.NotContains(t, rec.Body.String(), "categor", "o detalhe interno não sai na resposta")
	assert.Contains(t, a.logs.String(), "falha no painel")
}

// Invariante violada é 500 genérico, e a resposta NÃO traz número nenhum —
// nem zerado, nem clampado.
func TestHandlerInvarianteVioladaEh500SemNumeros(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)
	a.casaComum()
	a.ledger.rows = []dashboard.KindAccountTotals{
		{Kind: transaction.KindIncome, AccountID: contaCorrente, Count: 1, TotalCents: 10,
			MarkedCount: 1, MarkedTotalCents: 900},
	}

	rec := a.chamar(t, minhaCasa, "/api/v1/dashboard?month="+mes)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "incomeCents")
}
