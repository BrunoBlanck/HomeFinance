package transaction_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/config"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QA da emenda §11 — o que um cliente HOSTIL manda para PATCH
// /transactions/{id}. O caminho feliz e as recusas nomeadas estão em
// updatecategory_test.go e handler_e2c_test.go; aqui ficam as formas de corpo
// e de path que ninguém digita sem querer: JSON com tipo trocado, id forjado,
// corpo grande, método/mídia errados, id repetido no corpo.
//
// A pergunta de todos: "isto grava alguma coisa?" — e a resposta tem de ser
// não, sempre, sem exceção.

// requisicaoPatch monta a requisição do PATCH sem executá-la — é o que
// permite mexer em cabeçalho e query antes de chamar o handler.
func requisicaoPatch(t *testing.T, casa, id, corpo string) *http.Request {
	t.Helper()
	r := httptest.NewRequest(http.MethodPatch, "/transactions/"+url.PathEscape(id), strings.NewReader(corpo))
	r.Header.Set("Content-Type", "application/json")
	// O ServeMux já entrega o segmento DECODIFICADO — o teste faz o mesmo.
	r.SetPathValue("id", id)
	if casa != "" {
		r = r.WithContext(session.NewContext(r.Context(), session.Identity{
			UserID: usuario, HouseholdID: casa, Role: "owner", SessionID: "sess-1",
		}))
	}
	return r
}

func executar(amb *httpAmbiente, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	amb.handler.UpdateCategory(rec, r)
	return rec
}

// ambienteDeAbuso monta a casa com uma linha categorizável e uma categoria.
func ambienteDeAbuso(t *testing.T) (*httpAmbiente, transaction.Transaction) {
	t.Helper()
	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, catMercado, "Mercado", category.KindExpense)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)
	return amb, alvo
}

// O corpo tem de ser UM objeto JSON com UM campo de texto. Nada mais entra —
// e nenhuma das formas abaixo chega a tocar o repositório.
func TestUpdateCategoryHandlerRecusaCorposHostis(t *testing.T) {
	t.Parallel()

	amb, alvo := ambienteDeAbuso(t)

	casos := []struct {
		nome   string
		corpo  string
		status int
	}{
		{"array no lugar do objeto", `[{"categoryId":"` + catMercado + `"}]`, http.StatusBadRequest},
		{"string no lugar do objeto", `"` + catMercado + `"`, http.StatusBadRequest},
		{"null no lugar do objeto", `null`, http.StatusBadRequest},
		{"número no lugar do objeto", `42`, http.StatusBadRequest},
		{"categoryId como objeto", `{"categoryId":{"id":"` + catMercado + `"}}`, http.StatusBadRequest},
		{"categoryId como array", `{"categoryId":["` + catMercado + `"]}`, http.StatusBadRequest},
		{"categoryId booleano", `{"categoryId":true}`, http.StatusBadRequest},
		// A forma de UUID é conferida na borda: 35 e 37 caracteres, letra fora
		// do hexa, separador no lugar errado, espaço nas pontas.
		{"uuid curto", `{"categoryId":"00000000-0000-7000-8000-00000000c00"}`, http.StatusBadRequest},
		{"uuid longo", `{"categoryId":"00000000-0000-7000-8000-00000000c0011"}`, http.StatusBadRequest},
		{"uuid com letra fora do hexa", `{"categoryId":"00000000-0000-7000-8000-00000000c00z"}`, http.StatusBadRequest},
		{"uuid sem hífen", `{"categoryId":"0000000000007000800000000000c001"}`, http.StatusBadRequest},
		{"uuid com espaço na ponta", `{"categoryId":" ` + catMercado + `"}`, http.StatusBadRequest},
		{"uuid com unicode homoglyph", `{"categoryId":"00000000-0000-7000-8000-00000000с001"}`, http.StatusBadRequest},
		// Injeção não tem por onde entrar: a forma recusa antes de qualquer SQL.
		{"SQL no campo", `{"categoryId":"' OR 1=1 --"}`, http.StatusBadRequest},
		{"HTML no campo", `{"categoryId":"<script>alert(1)</script>"}`, http.StatusBadRequest},
		{"NUL no campo", `{"categoryId":"` + "\x00" + `"}`, http.StatusBadRequest},
		// Mass assignment por variação de nome do campo do token.
		{"household_id em snake_case", `{"categoryId":"` + catMercado + `","household_id":"x"}`, http.StatusBadRequest},
		{"id no corpo", `{"categoryId":"` + catMercado + `","id":"x"}`, http.StatusBadRequest},
		{"source no corpo", `{"categoryId":"` + catMercado + `","source":"manual"}`, http.StatusBadRequest},
		{"deletedAt no corpo", `{"categoryId":"` + catMercado + `","deletedAt":null}`, http.StatusBadRequest},
		{"createdBy no corpo", `{"categoryId":"` + catMercado + `","createdBy":"x"}`, http.StatusBadRequest},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			rec := amb.patchCategoria(t, minhaCasa, alvo.ID, c.corpo)
			require.Equal(t, c.status, rec.Code, rec.Body.String())
			// A resposta nunca devolve o que veio no corpo.
			assert.NotContains(t, rec.Body.String(), "script")
			assert.NotContains(t, rec.Body.String(), "OR 1=1")
		})
	}

	assert.Nil(t, amb.repo.linhas[alvo.ID].CategoryID, "nenhum corpo hostil gravou")
	assert.Empty(t, amb.auditor.registros)
}

// Corpo enorme: o middleware MaxBytes é da cadeia, então aqui o decodificador
// vê um objeto gigante — que continua sendo 400 por campo desconhecido, sem
// nunca alcançar o banco.
func TestUpdateCategoryHandlerRecusaCorpoEnormeSemGravar(t *testing.T) {
	t.Parallel()

	amb, alvo := ambienteDeAbuso(t)
	enchimento := strings.Repeat("a", 200_000)

	rec := amb.patchCategoria(t, minhaCasa, alvo.ID,
		`{"categoryId":"`+catMercado+`","lixo":"`+enchimento+`"}`)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Less(t, rec.Body.Len(), 1_000, "a resposta não devolve o corpo recebido")
	assert.Nil(t, amb.repo.linhas[alvo.ID].CategoryID)
	assert.Empty(t, amb.auditor.registros)
}

// O id do PATH também é entrada externa. Forjado em qualquer forma, é 404 com
// o corpo genérico — nunca 500, nunca detalhe.
func TestUpdateCategoryHandlerIdDoPathForjadoEh404Generico(t *testing.T) {
	t.Parallel()

	amb, alvo := ambienteDeAbuso(t)
	corpo := `{"categoryId":"` + catMercado + `"}`
	referencia := amb.patchCategoria(t, minhaCasa, "00000000-0000-7000-8000-000000000999", corpo)
	require.Equal(t, http.StatusNotFound, referencia.Code)

	ids := map[string]string{
		"vazio":        "",
		"fora do UUID": "tx-1",
		"SQL":          "' OR 1=1 --",
		"caminho":      "../../etc/passwd",
		"com curinga":  "%",
		"muito longo":  strings.Repeat("a", 5_000),
		"unicode":      "лançamento",
		"com NUL":      "abc" + "\x00" + "def",
		"json no path": `{"id":"x"}`,
	}
	for nome, id := range ids {
		t.Run(nome, func(t *testing.T) {
			// `requisicaoPatch` escapa o id na URL e o entrega cru no
			// PathValue — exatamente o que o ServeMux faz com um segmento
			// percent-encoded.
			rec := executar(amb, requisicaoPatch(t, minhaCasa, id, corpo))
			require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			assert.Equal(t, referencia.Body.String(), rec.Body.String(), "corpo idêntico ao inexistente")
		})
	}

	assert.Nil(t, amb.repo.linhas[alvo.ID].CategoryID)
	assert.Empty(t, amb.auditor.registros)
	// Nem os ids forjados nem a descrição da linha aparecem no log.
	assert.NotContains(t, amb.logs.String(), "OR 1=1")
	assert.NotContains(t, amb.logs.String(), "etc/passwd")
}

// Content-Type é exigido: form, texto e multipart são 415 — mandar JSON
// disfarçado de formulário não passa por cima do decodificador.
func TestUpdateCategoryHandlerExigeContentTypeJSON(t *testing.T) {
	t.Parallel()

	amb, alvo := ambienteDeAbuso(t)
	corpo := `{"categoryId":"` + catMercado + `"}`

	for nome, tipo := range map[string]string{
		"texto puro":    "text/plain",
		"formulário":    "application/x-www-form-urlencoded",
		"multipart":     "multipart/form-data; boundary=x",
		"inventado":     "application/vnd.homefinance+json",
		"malformado":    "application/json; charset",
		"sem cabeçalho": "",
	} {
		t.Run(nome, func(t *testing.T) {
			r := requisicaoPatch(t, minhaCasa, alvo.ID, corpo)
			if tipo == "" {
				r.Header.Del("Content-Type")
			} else {
				r.Header.Set("Content-Type", tipo)
			}
			rec := executar(amb, r)
			assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code, rec.Body.String())
		})
	}

	// `application/json; charset=utf-8` continua valendo.
	r := requisicaoPatch(t, minhaCasa, alvo.ID, corpo)
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	assert.Equal(t, http.StatusOK, executar(amb, r).Code)
}

// §13 da spec 0005 — achado do QA de 17/09/2026, CORRIGIDO.
//
// O `encoding/json` casa o nome do campo IGNORANDO a caixa, e o
// DisallowUnknownFields não muda isso: `{"CategoryId":…}` casava com
// `categoryId` e passava. Não abria nada (o valor continuava passando por toda
// a validação e pela casa do token), mas dava quatro grafias ao mesmo campo,
// contra o contrato, que declara uma. Agora é 400 de corpo malformado, como
// qualquer nome fora do contrato — a conferência mora em
// httpserver.DecodeJSON e vale para todo corpo JSON da API.
func TestUpdateCategoryHandlerRecusaOCampoEmOutraCaixa(t *testing.T) {
	t.Parallel()

	for _, corpo := range []string{
		`{"CategoryId":"` + catMercado + `"}`,
		`{"categoryid":"` + catMercado + `"}`,
		`{"CATEGORYID":"` + catMercado + `"}`,
		`{"cAtEgOrYiD":"` + catMercado + `"}`,
	} {
		t.Run(corpo, func(t *testing.T) {
			t.Parallel()
			amb, alvo := ambienteDeAbuso(t)

			rec := amb.patchCategoria(t, minhaCasa, alvo.ID, corpo)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			// Resposta genérica: nada da estrutura interna, nem o nome que o
			// cliente inventou, volta no corpo.
			assert.NotContains(t, rec.Body.String(), "CategoryId")
			assert.Nil(t, amb.repo.linhas[alvo.ID].CategoryID, "nada gravado")
			assert.Empty(t, amb.auditor.registros, "nada auditado")
		})
	}

	// O nome do contrato continua valendo, na caixa exata.
	amb, alvo := ambienteDeAbuso(t)
	rec := amb.patchCategoria(t, minhaCasa, alvo.ID, `{"categoryId":"`+catMercado+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, amb.repo.linhas[alvo.ID].CategoryID)
	assert.Equal(t, catMercado, *amb.repo.linhas[alvo.ID].CategoryID)
}

// Chave repetida no JSON: a ÚLTIMA vence (regra do encoding/json). O que
// importa é que a repetição não confunde a validação — a categoria alheia na
// primeira posição não "cola" numa segunda válida, e vice-versa.
func TestUpdateCategoryHandlerComChaveRepetidaUsaAUltimaEValidaNormalmente(t *testing.T) {
	t.Parallel()

	amb, alvo := ambienteDeAbuso(t)
	amb.categoria(outraCasa, catDaVizinha, "Da vizinha", category.KindExpense)

	// Válida primeiro, alheia depois: a última vence e é 404.
	rec := amb.patchCategoria(t, minhaCasa, alvo.ID,
		`{"categoryId":"`+catMercado+`","categoryId":"`+catDaVizinha+`"}`)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Nil(t, amb.repo.linhas[alvo.ID].CategoryID, "nada gravado")

	// Alheia primeiro, válida depois: grava a válida — a primeira não deixa
	// resíduo nenhum.
	rec = amb.patchCategoria(t, minhaCasa, alvo.ID,
		`{"categoryId":"`+catDaVizinha+`","categoryId":"`+catMercado+`"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NotNil(t, amb.repo.linhas[alvo.ID].CategoryID)
	assert.Equal(t, catMercado, *amb.repo.linhas[alvo.ID].CategoryID)
}

// Repetir o MESMO pedido não acumula auditoria nem escrita: a segunda chamada
// encontra a categoria que já está e volta 200 sem tocar em nada. É o que
// segura o clique duplo e o retry de rede.
func TestUpdateCategoryHandlerRepetidoNaoAcumulaEscritaNemAuditoria(t *testing.T) {
	t.Parallel()

	amb, alvo := ambienteDeAbuso(t)
	corpo := `{"categoryId":"` + catMercado + `"}`

	primeira := amb.patchCategoria(t, minhaCasa, alvo.ID, corpo)
	require.Equal(t, http.StatusOK, primeira.Code)
	gravado := amb.repo.linhas[alvo.ID]

	for range 5 {
		rec := amb.patchCategoria(t, minhaCasa, alvo.ID, corpo)
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, primeira.Body.String(), rec.Body.String(), "a resposta é a mesma")
	}
	assert.Equal(t, gravado, amb.repo.linhas[alvo.ID], "nada mudou depois da primeira")
	assert.Len(t, amb.auditor.registros, 1, "um evento, não seis")
}

// A casa vem SEMPRE do token: quem está na casa A não alcança a linha de B nem
// mandando a casa de B no corpo, na query ou no cabeçalho.
func TestUpdateCategoryHandlerIgnoraACasaVindaDoCliente(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(outraCasa, "acc-x", "Alheia", account.KindChecking)
	amb.categoria(minhaCasa, catMercado, "Mercado", category.KindExpense)
	amb.categoria(outraCasa, catDaVizinha, "Da vizinha", category.KindExpense)
	daVizinha := amb.lancamento(outraCasa, "acc-x", transaction.KindExpense, "Da vizinha", 99_00, 3)

	corpo := `{"categoryId":"` + catDaVizinha + `"}`
	referencia := amb.patchCategoria(t, minhaCasa, "00000000-0000-7000-8000-000000000999",
		`{"categoryId":"`+catMercado+`"}`)

	// Cabeçalho e query com a casa alheia: o handler não lê nenhum dos dois.
	r := requisicaoPatch(t, minhaCasa, daVizinha.ID, corpo)
	r.Header.Set("X-Household-Id", outraCasa)
	r.URL.RawQuery = "householdId=" + outraCasa
	rec := executar(amb, r)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Equal(t, referencia.Body.String(), rec.Body.String())

	assert.Nil(t, amb.repo.linhas[daVizinha.ID].CategoryID, "a linha da vizinha continua intacta")
	assert.Empty(t, amb.auditor.registros)
	_, campos := corpoDeErro(t, rec)
	assert.Empty(t, campos, "404 não tem `fields`: não se diz o que existe")
}

// O 422 de regra de negócio também não pode virar oráculo: a mensagem é a
// mesma esteja a categoria arquivada ou com a natureza trocada, e nenhum id
// aparece no corpo.
func TestUpdateCategoryHandler422NaoEcoaIdNemNome(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta Sigilosa", account.KindChecking)
	amb.categoria(minhaCasa, catSalario, "Salário Secreto", category.KindIncome)
	arquivada := amb.categoria(minhaCasa, catArquivada, "Antiga Secreta", category.KindExpense)
	arquivada.ArchivedAt = ptr(agora)
	amb.categorias.add(arquivada)
	despesa := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado Segredo", 150_00, 3)

	for _, categoria := range []string{catSalario, catArquivada} {
		rec := amb.patchCategoria(t, minhaCasa, despesa.ID, `{"categoryId":"`+categoria+`"}`)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
		corpo := rec.Body.String()
		assert.NotContains(t, corpo, categoria, "o id não volta")
		assert.NotContains(t, corpo, "Secreto")
		assert.NotContains(t, corpo, "Secreta")
		assert.NotContains(t, corpo, "Segredo")
		codigo, campos := corpoDeErro(t, rec)
		assert.Equal(t, httpserver.CodeValidationFailed, codigo)
		assert.Contains(t, campos, "categoryId")
	}
	assert.NotContains(t, amb.logs.String(), "Segredo")
	assert.NotContains(t, amb.logs.String(), "Secreta")
}

// Rate limit, parte 1 — o HANDLER não limita nada por conta própria.
//
// O balde vive na CADEIA DA ROTA (cmd/api/routes.go), nunca dentro do handler:
// limitador escondido no handler não aparece na tabela de rotas, não é
// revisável e não é testável junto com os outros. Este teste é o que quebra no
// dia em que alguém puser um — e quebra também se o handler começar a recusar
// a repetição por conta própria.
//
// A prova do teto de verdade (429 na 121ª chamada da hora) está logo abaixo,
// montando a mesma cadeia de `cmd/api`.
func TestUpdateCategoryHandlerNaoLimitaPorContaPropria(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, catMercado, "Mercado", category.KindExpense)
	amb.categoria(minhaCasa, catLazer, "Lazer", category.KindExpense)

	// 120 chamadas — o teto INTEIRO do balde da rota — direto no handler,
	// alternando a categoria para que cada uma seja uma escrita de verdade.
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)
	for i := range 120 {
		categoria := catMercado
		if i%2 == 1 {
			categoria = catLazer
		}
		rec := amb.patchCategoria(t, minhaCasa, alvo.ID, `{"categoryId":"`+categoria+`"}`)
		require.Equal(t, http.StatusOK, rec.Code, "chamada %d: %s", i, rec.Body.String())
	}
	assert.Len(t, amb.auditor.registros, 120, "cada troca real é um evento de auditoria")
}

// Rate limit, parte 2 — o balde PRÓPRIO por casa da rota (decisão do usuário,
// 17/09/2026; emenda §11).
//
// Até então o único teto sobre o atalho de categoria era o GLOBAL de 100/min
// por IP, que não é por casa: a rota grava uma linha e escreve uma linha de
// auditoria a CADA chamada, e quem tivesse endereços sobrando enchia a
// `audit_log` de uma casa sem passar por teto nenhum. O teto novo é 120/h POR
// CASA — generoso porque categorizar a fatura recém-importada é rajada
// legítima, e finito porque a rota escreve.
//
// A cadeia montada aqui é a mesma de cmd/api/routes.go: RateLimit(limitador,
// HouseholdKey(hash)) em volta do handler, com a identidade já no contexto (o
// que o RequireAuth publica antes).

// cadeiaPatchComLimite devolve o handler embrulhado no limitador por casa e o
// relógio que o limitador enxerga — congelado, para as 121 chamadas caírem
// todas dentro da MESMA hora.
func cadeiaPatchComLimite(t *testing.T, amb *httpAmbiente) (http.Handler, *time.Time) {
	t.Helper()
	rl := config.DefaultRateLimits()
	regra := rl.TransactionUpdate
	require.Equal(t, 120, regra.Requests, "emenda §11: 120 por hora por casa")
	require.Equal(t, time.Hour, regra.Window)

	agoraNoLimitador := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	limitador := httpserver.NewLimiter(regra.Requests, regra.Window, rl.IdleTTL).
		WithClock(func() time.Time { return agoraNoLimitador })
	// A chave por casa é um HMAC em produção; aqui basta ser injetiva e não
	// ecoar o id em claro.
	hash := func(householdID string) string { return "h:" + strings.ToUpper(householdID) }
	h := httpserver.RateLimit(limitador, httpserver.HouseholdKey(hash))(http.HandlerFunc(amb.handler.UpdateCategory))
	return h, &agoraNoLimitador
}

func TestUpdateCategoriaCentesimaVigesimaPrimeiraChamadaNaHoraE429ENaoEscreve(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, catMercado, "Mercado", category.KindExpense)
	amb.categoria(minhaCasa, catLazer, "Lazer", category.KindExpense)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)

	amb.conta(outraCasa, "acc-x", "Conta da Vizinha", account.KindChecking)
	amb.categoria(outraCasa, catDaVizinha, "Da vizinha", category.KindExpense)
	alvoDela := amb.lancamento(outraCasa, "acc-x", transaction.KindExpense, "Mercado dela", 10_00, 3)

	h, relogio := cadeiaPatchComLimite(t, amb)
	servir := func(casa, id, corpo string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, requisicaoPatch(t, casa, id, corpo))
		return rec
	}

	// 120 escritas de verdade na mesma hora: todas passam.
	for i := range 120 {
		categoria := catMercado
		if i%2 == 1 {
			categoria = catLazer
		}
		rec := servir(minhaCasa, alvo.ID, `{"categoryId":"`+categoria+`"}`)
		require.Equal(t, http.StatusOK, rec.Code, "chamada %d: %s", i, rec.Body.String())
	}
	require.Len(t, amb.auditor.registros, 120)
	gravadoAntes := amb.repo.linhas[alvo.ID]

	// A 121ª é 429: nada escrito, nada auditado, Retry-After presente, corpo
	// genérico sem casa, sem id e sem rota.
	rec := servir(minhaCasa, alvo.ID, `{"categoryId":"`+catMercado+`"}`)
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), httpserver.CodeRateLimited)
	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, retry, 1)
	assert.LessOrEqual(t, retry, 30, "120/h é um token a cada 30 s")
	assert.NotContains(t, rec.Body.String(), minhaCasa)
	assert.NotContains(t, rec.Body.String(), alvo.ID)
	assert.NotContains(t, rec.Body.String(), "transactions")
	assert.Equal(t, gravadoAntes, amb.repo.linhas[alvo.ID], "o 429 não pode ter chegado ao serviço")
	assert.Len(t, amb.auditor.registros, 120, "o 429 não audita")

	// Repetir dá 429 de novo (o 429 não "gasta" token nem libera).
	assert.Equal(t, http.StatusTooManyRequests,
		servir(minhaCasa, alvo.ID, `{"categoryId":"`+catLazer+`"}`).Code)

	// A OUTRA casa tem balde próprio: passa e grava o dela — e só o dela.
	recDela := servir(outraCasa, alvoDela.ID, `{"categoryId":"`+catDaVizinha+`"}`)
	require.Equal(t, http.StatusOK, recDela.Code, recDela.Body.String())
	require.NotNil(t, amb.repo.linhas[alvoDela.ID].CategoryID)
	assert.Equal(t, catDaVizinha, *amb.repo.linhas[alvoDela.ID].CategoryID)
	assert.Equal(t, gravadoAntes, amb.repo.linhas[alvo.ID], "a escrita da vizinha não tocou a minha linha")

	// Passado o Retry-After, um token voltou: a escrita passa de novo.
	*relogio = relogio.Add(time.Duration(retry) * time.Second)
	recDepois := servir(minhaCasa, alvo.ID, `{"categoryId":"`+catMercado+`"}`)
	require.Equal(t, http.StatusOK, recDepois.Code, recDepois.Body.String())

	// E o seguinte, no mesmo instante, volta a ser 429.
	assert.Equal(t, http.StatusTooManyRequests,
		servir(minhaCasa, alvo.ID, `{"categoryId":"`+catLazer+`"}`).Code)
}
