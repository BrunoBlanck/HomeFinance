package report_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Casos de abuso que faltavam no handler: as formas hostis de `month` que
// ainda não estavam na tabela, o parâmetro repetido (que em Go tem uma regra
// definida, e ela precisa estar testada para não mudar sem ninguém ver) e os
// parâmetros espúrios em branco.

// `month` hostil: injeção, travessia de caminho e um valor de 10 kB. Todos
// terminam na MESMA recusa de 400 em fields.month, byte a byte, sem eco do
// que foi enviado e sem chegar ao banco.
func TestHandlerMesHostilEh400Identico(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"sqli aspas":      "2026-09' OR '1'='1",
		"sqli comentário": "2026-09'--",
		"travessia":       "../",
		"travessia longa": "../../../../etc/passwd",
		"html":            "<script>alert(1)</script>",
		"unicode":         "２０２６-０９", // dígitos de largura total
		"dez kB":          strings.Repeat("9", 10*1024),
		"nulo no meio":    "2026\x00-09",
		"nova linha":      "2026-09\n",
	}

	var mensagens []string
	for nome, mes := range casos {
		t.Run(nome, func(t *testing.T) {
			a := novoHTTPAmbiente(t)
			q := url.Values{"month": {mes}}
			rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?"+q.Encode())

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			assert.Contains(t, campos, "month")
			assert.NotContains(t, campos, "kind")
			assert.Empty(t, a.ledger.chamadas, "entrada hostil não chega ao banco")
			for _, eco := range []string{"OR '1'", "script", "passwd", "9999999999"} {
				assert.NotContains(t, rec.Body.String(), eco, "a recusa não ecoa o valor enviado")
			}
			mensagens = append(mensagens, rec.Body.String())
		})
	}
	for i := 1; i < len(mensagens); i++ {
		assert.Equal(t, mensagens[0], mensagens[i], "toda recusa de mês é idêntica")
	}
}

// `?kind=expense&kind=income`: `url.Values.Get` devolve o PRIMEIRO valor, e é
// isso que precisa estar contratado — não porque o primeiro seja melhor que o
// último, mas porque a regra tem de ser uma só e visível. Repetido 25 vezes
// para provar que não depende de ordem de map.
func TestHandlerKindRepetidoUsaSempreOPrimeiro(t *testing.T) {
	t.Parallel()

	casos := []struct {
		alvo     string
		esperado string
	}{
		{"/api/v1/reports/by-category?month=2026-09&kind=expense&kind=income", "expense"},
		{"/api/v1/reports/by-category?month=2026-09&kind=income&kind=expense", "income"},
	}
	for _, c := range casos {
		t.Run(c.esperado, func(t *testing.T) {
			for range 25 {
				a := novoHTTPAmbiente(t)
				rec := a.chamar(t, minhaCasa, c.alvo)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				var v report.CategoryReportView
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
				require.Equal(t, c.esperado, v.Kind)
				require.Len(t, a.ledger.chamadas, 1)
				require.Equal(t, c.esperado, a.ledger.chamadas[0].kind)
			}
		})
	}

	// O primeiro valor continua valendo mesmo quando o segundo é lixo — e o
	// lixo não vira erro, porque nem é lido.
	a := novoHTTPAmbiente(t)
	rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09&kind=income&kind=%27%3B+DROP+TABLE+transactions%3B--")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, a.ledger.chamadas, 1)
	assert.Equal(t, "income", a.ledger.chamadas[0].kind)

	// E o inverso: kind inválido PRIMEIRO é 400, mesmo com um válido depois.
	a = novoHTTPAmbiente(t)
	rec = a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09&kind=transfer_out&kind=expense")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	_, campos := corpoDeErro(t, rec)
	assert.Contains(t, campos, "kind")
	assert.Empty(t, a.ledger.chamadas)
}

// Parâmetros espúrios EM BRANCO (`householdId=`, `categoryId=`, `limit=`,
// `offset=`) são ignorados como qualquer outro: a resposta é 200 e a casa
// continua sendo a do token. O `month` repetido depois deles não muda nada.
func TestHandlerParametrosEspuriosEmBrancoSaoIgnorados(t *testing.T) {
	t.Parallel()
	a := novoHTTPAmbiente(t)

	alvo := "/api/v1/reports/by-category?householdId=&household_id=&categoryId=&category_id=&limit=&offset=&sort=&month=2026-09&page=-1&kind="
	rec := a.chamar(t, minhaCasa, alvo)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, a.ledger.chamadas, 1)
	assert.Equal(t, minhaCasa, a.ledger.chamadas[0].casa)
	assert.Equal(t, "2026-09", a.ledger.chamadas[0].mes)
	assert.Equal(t, "expense", a.ledger.chamadas[0].kind, "kind vazio é expense, como o ausente")

	var v report.CategoryReportView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, "2026-09", v.Month)
	assert.Equal(t, "expense", v.Kind)
}

// O `householdId` de outra casa na query não muda a consulta NEM quando vem
// junto de um mês válido e de um kind válido — é a checagem de BOLA no ponto
// de entrada do handler, com os três parâmetros preenchidos.
func TestHandlerHouseholdIDDaQueryNuncaVence(t *testing.T) {
	t.Parallel()

	for _, chave := range []string{"householdId", "household_id", "household", "casa", "hid"} {
		t.Run(chave, func(t *testing.T) {
			a := novoHTTPAmbiente(t)
			q := url.Values{"month": {"2026-09"}, "kind": {"income"}, chave: {outraCasa}}
			rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?"+q.Encode())

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Len(t, a.ledger.chamadas, 1)
			assert.Equal(t, minhaCasa, a.ledger.chamadas[0].casa)
			assert.NotContains(t, rec.Body.String(), outraCasa)
		})
	}
}
