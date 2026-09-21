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
// ainda não estavam na tabela, o parâmetro repetido (HPP — desde o ADR-032
// os três parâmetros são lidos por httpserver.SoleQueryValue, e chave
// repetida é 400 sem consultar nada) e os parâmetros espúrios em branco.

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

// Parâmetro REPETIDO é 400 no campo repetido, sem consultar nada (HPP,
// ADR-032 — substitui a regra antiga de "o primeiro vence", que deixava proxy,
// WAF e log lerem uma natureza enquanto o servidor respondia outra).
//
// Os três parâmetros, e as três formas da repetição: valores diferentes,
// valores IGUAIS (a URL continua ambígua) e a segunda ocorrência VAZIA (a pior
// das três — quem lê a última recebe "sem recorte" e a tela mostra tudo sob o
// rótulo do recorte). A redação é própria do campo e não ecoa nenhum valor.
func TestHandlerParametroRepetidoEh400(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		alvo     string
		campo    string
		mensagem string
	}{
		"month diferentes":        {"/api/v1/reports/by-category?month=2026-13&month=2026-09", "month", "Informe o mês uma única vez."},
		"month iguais":            {"/api/v1/reports/by-category?month=2026-09&month=2026-09", "month", "Informe o mês uma única vez."},
		"month segunda vazia":     {"/api/v1/reports/by-category?month=2026-09&month=", "month", "Informe o mês uma única vez."},
		"kind diferentes":         {"/api/v1/reports/by-category?month=2026-09&kind=expense&kind=income", "kind", "Informe a natureza uma única vez."},
		"kind iguais":             {"/api/v1/reports/by-category?month=2026-09&kind=income&kind=income", "kind", "Informe a natureza uma única vez."},
		"kind segunda vazia":      {"/api/v1/reports/by-category?month=2026-09&kind=income&kind=", "kind", "Informe a natureza uma única vez."},
		"kind válido depois lixo": {"/api/v1/reports/by-category?month=2026-09&kind=income&kind=%27%3B+DROP+TABLE+transactions%3B--", "kind", "Informe a natureza uma única vez."},
		"kind inválido e válido":  {"/api/v1/reports/by-category?month=2026-09&kind=transfer_out&kind=expense", "kind", "Informe a natureza uma única vez."},
		"accountGroup diferentes": {"/api/v1/reports/by-category?month=2026-09&accountGroup=credit&accountGroup=debit", "accountGroup", "Informe o grupo de contas uma única vez."},
		"accountGroup iguais":     {"/api/v1/reports/by-category?month=2026-09&accountGroup=credit&accountGroup=credit", "accountGroup", "Informe o grupo de contas uma única vez."},
		"accountGroup 2ª vazia":   {"/api/v1/reports/by-category?month=2026-09&accountGroup=credit&accountGroup=", "accountGroup", "Informe o grupo de contas uma única vez."},
		"accountGroup 1ª vazia":   {"/api/v1/reports/by-category?month=2026-09&accountGroup=&accountGroup=credit", "accountGroup", "Informe o grupo de contas uma única vez."},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			a := novoHTTPAmbiente(t)
			rec := a.chamar(t, minhaCasa, c.alvo)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			require.Len(t, campos, 1, "um campo por recusa: %v", campos)
			assert.Equal(t, c.mensagem, campos[c.campo])
			assert.Empty(t, a.ledger.chamadas, "chave repetida não chega ao banco")
			assert.Empty(t, a.cats.chamadas)
			assert.Empty(t, a.contas.chamadas)
			for _, eco := range []string{"DROP", "transfer_out", "2026-13", "credit", "debit"} {
				assert.NotContains(t, rec.Body.String(), eco, "a recusa não ecoa o valor enviado")
			}
		})
	}

	// Uma falha por resposta, na ordem month → kind → accountGroup: com os
	// três repetidos, só `month` aparece.
	t.Run("três repetidos: só o primeiro", func(t *testing.T) {
		t.Parallel()
		a := novoHTTPAmbiente(t)
		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09&month=2026-09&kind=income&kind=income&accountGroup=credit&accountGroup=credit")
		require.Equal(t, http.StatusBadRequest, rec.Code)
		_, campos := corpoDeErro(t, rec)
		assert.Equal(t, map[string]string{"month": "Informe o mês uma única vez."}, campos)
		assert.Empty(t, a.ledger.chamadas)
	})

	// Repetição vem ANTES da forma: `kind` repetido com `month` inválido é
	// recusado pelo `month` (a leitura é na ordem, e o `month` vem primeiro),
	// mas `accountGroup` repetido com `kind` inválido é recusado pelo
	// `accountGroup` repetido — a URL ambígua nem chega ao serviço.
	t.Run("repetição precede a validação de conteúdo", func(t *testing.T) {
		t.Parallel()
		a := novoHTTPAmbiente(t)
		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09&kind=bogus&accountGroup=credit&accountGroup=debit")
		require.Equal(t, http.StatusBadRequest, rec.Code)
		_, campos := corpoDeErro(t, rec)
		assert.Equal(t, map[string]string{"accountGroup": "Informe o grupo de contas uma única vez."}, campos)
		assert.Empty(t, a.ledger.chamadas)
	})

	// UMA ocorrência de cada continua sendo o caminho normal — e uma
	// ocorrência VAZIA de `accountGroup` não é ambígua: é "todas as contas".
	t.Run("uma ocorrência de cada é 200", func(t *testing.T) {
		t.Parallel()
		a := novoHTTPAmbiente(t)
		rec := a.chamar(t, minhaCasa, "/api/v1/reports/by-category?month=2026-09&kind=income&accountGroup=")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var v report.CategoryReportView
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
		assert.Equal(t, "income", v.Kind)
		assert.Nil(t, v.AccountGroup)
		require.Len(t, a.ledger.chamadas, 1)
		assert.Equal(t, "income", a.ledger.chamadas[0].kind)
	})
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
