package aiimport_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/aiimport"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Os testes de handler provam o CONTRATO das duas rotas: status, o campo
// apontado em cada 400, a forma exata do JSON do relatório, o 409 sem campos,
// o 413 antes de qualquer parsing de negócio — e o que NUNCA sai na resposta
// nem no log (`notes`, palavra recusada bruta).

const (
	rotaPrevia  = "/api/v1/ai/keyword-import/preview"
	rotaConfirm = "/api/v1/ai/keyword-import/confirm"
)

// chamar monta a requisição já com a identidade no contexto — o que o
// RequireAuth faz em produção. A casa NUNCA vem do corpo nem da URL.
func (a *ambiente) chamar(t *testing.T, rota, casa, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, rota, strings.NewReader(corpo))
	r.Header.Set("Content-Type", "application/json")
	if casa != "" {
		r = r.WithContext(session.NewContext(r.Context(), session.Identity{
			UserID: a.usuario, HouseholdID: casa, Role: session.RoleOwner, SessionID: "sess-1",
		}))
	}
	rec := httptest.NewRecorder()
	switch rota {
	case rotaPrevia:
		a.handler.Preview(rec, r)
	case rotaConfirm:
		a.handler.Confirm(rec, r)
	default:
		t.Fatalf("rota desconhecida %q", rota)
	}
	return rec
}

func erroDe(t *testing.T, rec *httptest.ResponseRecorder) (string, map[string]string) {
	t.Helper()
	var corpo struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Fields  map[string]string `json:"fields"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo), "corpo: %s", rec.Body.String())
	return corpo.Error.Code, corpo.Error.Fields
}

// envelope monta o corpo das duas rotas a partir de um payload JSON cru.
func envelope(payload string, extras ...string) string {
	partes := append([]string{
		`"payload": ` + payload,
		`"fromMonth": "` + mesInicial + `"`,
		`"toMonth": "` + mesFinal + `"`,
	}, extras...)
	return "{" + strings.Join(partes, ", ") + "}"
}

func chaves(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// --- caminho feliz e a forma do relatório -------------------------------------------

func TestHandlerCaminhoFelizEFormaExataDoRelatorio(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	a.lancar(t, a.casa.ID, transaction.KindExpense, c.corrente, "Pagamento de boleto", "2026-08")

	payload := fmt.Sprintf(`{
		"homefinanceKeywordImport": 1,
		"newCategories": [{"group": "Saúde", "name": "Farmácia", "kind": "expense", "add": ["drogaria"]}],
		"categoryKeywords": [{"categoryId": %q, "categoryPath": "Alimentação > Mercado", "add": ["feira", "zaffari", "x"]}],
		"accountKeywords": [{"accountId": %q, "accountName": "Cartão", "add": ["pagamento"]}],
		"notes": "a IA explicando o que fez"
	}`, c.mercado.ID, c.cartao)

	rec := a.chamar(t, rotaPrevia, a.casa.ID, envelope(payload))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var corpo map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
	assert.Equal(t, []string{"items", "newCategories", "totals"}, chaves(corpo))

	totais := corpo["totals"].(map[string]any)
	assert.Equal(t, []string{"added", "categoriesCreated", "periodTransactions", "rejected", "skipped"}, chaves(totais))
	assert.EqualValues(t, 1, totais["categoriesCreated"])
	assert.EqualValues(t, 3, totais["added"])
	assert.EqualValues(t, 1, totais["skipped"])
	assert.EqualValues(t, 1, totais["rejected"])
	assert.EqualValues(t, 1, totais["periodTransactions"])

	novas := corpo["newCategories"].([]any)
	require.Len(t, novas, 1)
	nova := novas[0].(map[string]any)
	assert.Equal(t, []string{"add", "categoryId", "group", "groupIsNew", "kind", "name", "outcome", "ref", "rejected", "skipped"}, chaves(nova))
	assert.Equal(t, "created", nova["outcome"])
	assert.Equal(t, "saude > farmacia", nova["ref"])
	assert.Equal(t, "expense", nova["kind"])
	assert.Equal(t, true, nova["groupIsNew"])
	assert.Nil(t, nova["categoryId"], "na prévia a categoria ainda não existe: null, e a chave presente")

	itens := corpo["items"].([]any)
	require.Len(t, itens, 2)
	categoria := itens[0].(map[string]any)
	assert.Equal(t, []string{"added", "id", "name", "rejected", "skipped", "type"}, chaves(categoria),
		"item de CATEGORIA não tem a chave impact")
	assert.Equal(t, "category", categoria["type"])
	assert.Equal(t, c.mercado.ID, categoria["id"])
	assert.Equal(t, "Alimentação > Mercado", categoria["name"])
	assert.Equal(t, []any{"feira"}, categoria["added"])
	assert.Equal(t, []any{map[string]any{"keyword": "zaffari", "reason": "already_present"}}, categoria["skipped"])
	assert.Equal(t, []any{map[string]any{"keyword": "x", "reason": "invalid_keyword"}}, categoria["rejected"],
		"sem ownerId fora de keyword_taken")

	conta := itens[1].(map[string]any)
	assert.Equal(t, []string{"added", "id", "impact", "name", "rejected", "skipped", "type"}, chaves(conta))
	assert.Equal(t, map[string]any{
		"transferCandidates": float64(1),
		"byKeyword":          []any{map[string]any{"keyword": "pagamento", "transferCandidates": float64(1)}},
	}, conta["impact"])

	// `notes` não volta — nem como chave, nem como texto.
	assert.NotContains(t, rec.Body.String(), "notes")
	assert.NotContains(t, rec.Body.String(), "a IA explicando")

	// O confirm, com o MESMO corpo, devolve a mesma forma — sem impact.
	rec = a.chamar(t, rotaConfirm, a.casa.ID, envelope(payload))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
	for _, it := range corpo["items"].([]any) {
		_, temImpact := it.(map[string]any)["impact"]
		assert.False(t, temImpact, "impact não aparece no confirm")
	}
	nova = corpo["newCategories"].([]any)[0].(map[string]any)
	assert.NotNil(t, nova["categoryId"], "no confirm a recém-nascida volta com id")
	assert.Equal(t, []string{"drogaria"}, a.palavrasDe(t, a.casa.ID, nova["categoryId"].(string)))
}

// Item não resolvido: `name` é null (chave presente) e o id não canônico
// não é ecoado.
func TestHandlerItemNaoEncontradoTemNomeNuloEIdSoQuandoCanonico(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum(t)

	rec := a.chamar(t, rotaPrevia, a.casa.ID, envelope(`{
		"homefinanceKeywordImport": 1,
		"categoryKeywords": [
			{"categoryId": "018f0000-0000-7000-8000-00000000dead", "categoryPath": "x", "add": ["feira"]},
			{"categoryId": "<script>alert(1)</script>", "categoryPath": "x", "add": ["feira"]}
		]
	}`))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var corpo struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
	require.Len(t, corpo.Items, 2)
	assert.Equal(t, "018f0000-0000-7000-8000-00000000dead", corpo.Items[0]["id"])
	assert.Nil(t, corpo.Items[0]["name"])
	_, temName := corpo.Items[0]["name"]
	assert.True(t, temName, "a chave name está presente, com null")
	assert.Equal(t, "", corpo.Items[1]["id"])
	assert.NotContains(t, rec.Body.String(), "<script>", "texto que não é uuid não volta para dentro do app")
}

// --- 400: a forma do envelope e do payload ----------------------------------------------

func TestHandler400PorForma(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	item := fmt.Sprintf(`{"categoryId": %q, "categoryPath": "Alimentação > Mercado", "add": ["feira"]}`, c.mercado.ID)
	muitos := make([]string, 0, aiimport.MaxEntriesPerList+1)
	for range aiimport.MaxEntriesPerList + 1 {
		muitos = append(muitos, item)
	}
	vinteEUma := make([]string, 0, 21)
	for i := range 21 {
		vinteEUma = append(vinteEUma, fmt.Sprintf("%q", "palavra "+string(rune('a'+i))))
	}

	casos := []struct {
		nome  string
		corpo string
		campo string
	}{
		{"campo desconhecido no raiz do payload",
			envelope(`{"homefinanceKeywordImport": 1, "deleteCategories": ["x"], "categoryKeywords": [` + item + `]}`), ""},
		{"campo capaz de renomear é desconhecido",
			envelope(`{"homefinanceKeywordImport": 1, "renameCategories": [{"from": "a", "to": "b"}], "categoryKeywords": [` + item + `]}`), ""},
		{"campo desconhecido numa entrada",
			envelope(`{"homefinanceKeywordImport": 1, "categoryKeywords": [{"categoryId": "x", "categoryPath": "x", "add": ["feira"], "remove": ["zaffari"]}]}`), ""},
		{"campo desconhecido no envelope",
			envelope(`{"homefinanceKeywordImport": 1, "categoryKeywords": [`+item+`]}`, `"householdId": "outra"`), ""},
		{"nome de campo com a caixa trocada",
			envelope(`{"HomefinanceKeywordImport": 1, "categoryKeywords": [` + item + `]}`), ""},
		{"versão ausente", envelope(`{"categoryKeywords": [` + item + `]}`), "payload.homefinanceKeywordImport"},
		{"versão 2", envelope(`{"homefinanceKeywordImport": 2, "categoryKeywords": [` + item + `]}`), "payload.homefinanceKeywordImport"},
		{"versão como texto", envelope(`{"homefinanceKeywordImport": "1", "categoryKeywords": [` + item + `]}`), "payload.homefinanceKeywordImport"},
		{"listas vazias", envelope(`{"homefinanceKeywordImport": 1, "newCategories": [], "categoryKeywords": []}`), "payload"},
		{"payload ausente", `{"fromMonth": "` + mesInicial + `", "toMonth": "` + mesFinal + `"}`, "payload"},
		{"payload que não é objeto", envelope(`"texto"`), "payload"},
		{"201 entradas", envelope(`{"homefinanceKeywordImport": 1, "categoryKeywords": [` + strings.Join(muitos, ",") + `]}`), "payload.categoryKeywords"},
		{"21 palavras", envelope(fmt.Sprintf(`{"homefinanceKeywordImport": 1, "categoryKeywords": [{"categoryId": %q, "categoryPath": "x", "add": [%s]}]}`,
			c.mercado.ID, strings.Join(vinteEUma, ","))), "payload.categoryKeywords[0].add"},
		{"notes longo demais", envelope(`{"homefinanceKeywordImport": 1, "categoryKeywords": [` + item + `], "notes": "` + strings.Repeat("a", aiimport.MaxNotesRunes+1) + `"}`), "payload.notes"},
		{"notes que não é texto", envelope(`{"homefinanceKeywordImport": 1, "categoryKeywords": [` + item + `], "notes": {"a": 1}}`), ""},
		{"mês malformado", `{"payload": {"homefinanceKeywordImport": 1, "categoryKeywords": [` + item + `]}, "fromMonth": "2026-7", "toMonth": "` + mesFinal + `"}`, "fromMonth"},
		{"janela longa", `{"payload": {"homefinanceKeywordImport": 1, "categoryKeywords": [` + item + `]}, "fromMonth": "2026-05", "toMonth": "` + mesFinal + `"}`, "toMonth"},
		{"JSON malformado", `{"payload": {`, ""},
		{"dois valores no corpo", envelope(`{"homefinanceKeywordImport": 1, "categoryKeywords": [`+item+`]}`) + `{}`, ""},
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			for _, rota := range []string{rotaPrevia, rotaConfirm} {
				rec := a.chamar(t, rota, a.casa.ID, tc.corpo)
				require.Equal(t, http.StatusBadRequest, rec.Code, "%s: %s", rota, rec.Body.String())
				code, fields := erroDe(t, rec)
				assert.Equal(t, httpserver.CodeValidationFailed, code)
				if tc.campo != "" {
					assert.Contains(t, fields, tc.campo, "campo apontado: %v", fields)
				}
				// Nenhum eco do conteúdo colado.
				assert.NotContains(t, rec.Body.String(), "feira")
				assert.NotContains(t, rec.Body.String(), "deleteCategories")
				assert.NotContains(t, rec.Body.String(), c.mercado.ID)
			}
		})
	}
	assert.Equal(t, antes, a.retrato(t, a.casa.ID), "400 não escreve")
}

// Critério 21: `notes` com 10 KB é aceito e NÃO aparece em lugar nenhum —
// resposta, banco e log.
func TestHandlerNotesDe10KBEhAceitoEDescartado(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	marcador := "MARCADOR-DAS-NOTAS-xyzzy"
	notas := marcador + strings.Repeat("n", aiimport.MaxNotesRunes-len(marcador))
	require.Len(t, notas, aiimport.MaxNotesRunes)

	corpo := envelope(fmt.Sprintf(`{"homefinanceKeywordImport": 1,
		"categoryKeywords": [{"categoryId": %q, "categoryPath": "Alimentação > Mercado", "add": ["feira"]}],
		"notes": %q}`, c.mercado.ID, notas))
	for _, rota := range []string{rotaPrevia, rotaConfirm} {
		rec := a.chamar(t, rota, a.casa.ID, corpo)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.NotContains(t, rec.Body.String(), marcador)
	}
	assert.NotContains(t, a.logs.String(), marcador, "notes no log")
	// Nada com o marcador no banco: nem categoria, nem palavra.
	r := a.retrato(t, a.casa.ID)
	for _, linha := range append(append(r.categorias, r.palavras...), r.deConta...) {
		assert.NotContains(t, linha, marcador)
	}
	assert.Equal(t, []string{"zaffari", "feira"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID), "o resto do payload valeu")
}

// Critério 20: corpo de 200 KB é 413 PAYLOAD_TOO_LARGE antes de qualquer
// parsing de negócio — pelo MaxBytesReader da cadeia global, por caminho
// exato.
func TestHandler413PeloTetoDeCorpoDaCadeia(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum(t)

	// O mesmo middleware que main.go liga, com o mesmo mapa por caminho.
	protegido := httpserver.MaxBytesByPath(1<<20, map[string]int64{
		rotaPrevia:  aiimport.MaxPayloadBytes,
		rotaConfirm: aiimport.MaxPayloadBytes,
	})(http.HandlerFunc(a.handler.Preview))

	grande := envelope(`{"homefinanceKeywordImport": 1, "notes": "` + strings.Repeat("a", 200<<10) + `"}`)
	r := httptest.NewRequest(http.MethodPost, rotaPrevia, strings.NewReader(grande))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID: a.usuario, HouseholdID: a.casa.ID, Role: session.RoleOwner, SessionID: "sess-1",
	}))
	rec := httptest.NewRecorder()
	protegido.ServeHTTP(rec, r)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
	code, _ := erroDe(t, rec)
	assert.Equal(t, httpserver.CodePayloadTooLarge, code)
}

// --- 401, 409, 415, 422 -----------------------------------------------------------------

func TestHandlerSemIdentidadeEh401(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	for _, rota := range []string{rotaPrevia, rotaConfirm} {
		rec := a.chamar(t, rota, "", envelope(`{"homefinanceKeywordImport": 1}`))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		code, _ := erroDe(t, rec)
		assert.Equal(t, httpserver.CodeUnauthenticated, code)
	}
}

func TestHandlerContentTypeErradoEh415(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	r := httptest.NewRequest(http.MethodPost, rotaPrevia, strings.NewReader(envelope(`{"homefinanceKeywordImport": 1}`)))
	r.Header.Set("Content-Type", "text/plain")
	r = r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID: a.usuario, HouseholdID: a.casa.ID, Role: session.RoleOwner, SessionID: "sess-1",
	}))
	rec := httptest.NewRecorder()
	a.handler.Preview(rec, r)
	assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
}

// O estado que muda entre a leitura e a escrita da mesma transação é 409
// CONFLICT sem campos, e NADA fica gravado. A corrida é simulada pelo
// escritor de categoria devolvendo ErrNameTaken na primeira criação — o que
// o índice já tinha pré-conferido como livre.
func TestHandlerCorridaNaConfirmacaoEh409SemNadaGravado(t *testing.T) {
	t.Parallel()
	escritor := &escritorDeCategoria{falharNa: 1, erroForca: category.ErrNameTaken}
	a := novoAmbienteComEscritor(t, escritor)
	c := a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	corpo := envelope(fmt.Sprintf(`{"homefinanceKeywordImport": 1,
		"categoryKeywords": [{"categoryId": %q, "categoryPath": "Alimentação > Mercado", "add": ["feira"]}],
		"newCategories": [{"group": "Alimentação", "name": "Feira", "add": ["hortifruti"]}]}`, c.mercado.ID))
	rec := a.chamar(t, rotaConfirm, a.casa.ID, corpo)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	code, fields := erroDe(t, rec)
	assert.Equal(t, httpserver.CodeConflict, code)
	assert.Empty(t, fields, "409 sem campos: citar o que mudou seria vazar o estado de outra requisição")
	assert.Equal(t, antes, a.retrato(t, a.casa.ID), "nada gravado")
	assert.NotContains(t, a.logs.String(), `"level":"ERROR"`, "corrida não é falha do servidor")

	// A prévia com o mesmo corpo continua respondendo 200: a corrida é do
	// confirm.
	rec = a.chamar(t, rotaPrevia, a.casa.ID, corpo)
	assert.Equal(t, http.StatusOK, rec.Code)
}

// Bug do plano (erro que Create só devolve para o que foi pré-conferido) é
// 500 genérico, com a razão só no log — e nada gravado.
func TestHandlerPlanoInconsistenteEh500SemDetalhe(t *testing.T) {
	t.Parallel()
	escritor := &escritorDeCategoria{falharNa: 1, erroForca: category.ErrTooDeep}
	a := novoAmbienteComEscritor(t, escritor)
	a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	rec := a.chamar(t, rotaConfirm, a.casa.ID, envelope(`{"homefinanceKeywordImport": 1,
		"newCategories": [{"group": "Alimentação", "name": "Feira", "add": ["hortifruti"]}]}`))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	code, _ := erroDe(t, rec)
	assert.Equal(t, httpserver.CodeInternalError, code)
	assert.NotContains(t, rec.Body.String(), "níveis")
	assert.Contains(t, a.logs.String(), "plano do import inconsistente")
	assert.NotContains(t, a.logs.String(), "hortifruti", "palavra no log")
	assert.Equal(t, antes, a.retrato(t, a.casa.ID))
}

// Janela com descrições demais para a medição é 422 em fields.toMonth, o
// mesmo desfecho do export — nunca uma medição parcial.
func TestHandlerJanelaComDescricoesDemaisEh422(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	// O serviço pede transaction.MaxDescriptionGroupRows ao razão; o 422
	// nasce do erro do repositório. Simular 5.001 grupos custaria demais em
	// SQLite, então a prova aqui é da TRADUÇÃO: um razão que devolve o
	// sentinela.
	svc := aiimport.NewService(a.categorias, a.categoriaSvc, a.contas, a.contaSvc,
		razaoQueEstoura{}, a.uow, nil)
	h := aiimport.NewHandler(svc, nil, 0)

	corpo := envelope(fmt.Sprintf(`{"homefinanceKeywordImport": 1,
		"categoryKeywords": [{"categoryId": %q, "categoryPath": "Alimentação > Mercado", "add": ["feira"]}]}`, c.mercado.ID))
	for _, op := range []http.HandlerFunc{h.Preview, h.Confirm} {
		r := httptest.NewRequest(http.MethodPost, rotaPrevia, strings.NewReader(corpo))
		r.Header.Set("Content-Type", "application/json")
		r = r.WithContext(session.NewContext(r.Context(), session.Identity{
			UserID: a.usuario, HouseholdID: a.casa.ID, Role: session.RoleOwner, SessionID: "sess-1",
		}))
		rec := httptest.NewRecorder()
		op(rec, r)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		code, fields := erroDe(t, rec)
		assert.Equal(t, httpserver.CodeValidationFailed, code)
		assert.Contains(t, fields, "toMonth")
	}
	assert.Equal(t, []string{"zaffari"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID), "o confirm falha ANTES da transação")
}

// razaoQueEstoura devolve o sentinela de volume do repositório.
type razaoQueEstoura struct{}

func (razaoQueEstoura) GroupByDescription(_ context.Context, _ string, _ []string, _ int) ([]transaction.DescriptionGroup, error) {
	return nil, transaction.ErrTooManyDescriptionGroups
}

// O orçamento de trabalho da medição estourado é 422 em fields.toMonth — o
// mesmo desfecho de todo teto que chega ao matcher —, nunca uma medição
// parcial com 200.
func TestHandlerOrcamentoDaMedicaoEstouradoEh422(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t, aiimport.WithWorkBudget(1))
	c := a.casaComum(t)
	a.lancar(t, a.casa.ID, transaction.KindExpense, c.corrente, "Pagamento de boleto bancario", "2026-08")

	rec := a.chamar(t, rotaPrevia, a.casa.ID, envelope(fmt.Sprintf(`{"homefinanceKeywordImport": 1,
		"accountKeywords": [{"accountId": %q, "accountName": "Cartão", "add": ["pagamento boleto"]}]}`, c.cartao)))
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	code, fields := erroDe(t, rec)
	assert.Equal(t, httpserver.CodeValidationFailed, code)
	assert.Contains(t, fields, "toMonth")
	assert.NotContains(t, rec.Body.String(), "pagamento boleto", "sem eco da palavra")
}

// O cliente que desiste no meio (aba fechada, fetch abortado) NÃO é falha do
// servidor: a linha do handler sai em INFO, não em ERROR — o log de ERROR
// existe para mostrar sinal de segurança, e uma aba fechada não é sinal de
// nada. A resposta continua sendo o 500 genérico do enum fechado do
// contrato, que ninguém lê porque a conexão já foi (mesma decisão de
// internal/importer).
func TestHandlerClienteQueDesisteNaoViraErroNoLog(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)

	corpo := envelope(fmt.Sprintf(`{"homefinanceKeywordImport": 1,
		"categoryKeywords": [{"categoryId": %q, "categoryPath": "Alimentação > Mercado", "add": ["feira"]}]}`, c.mercado.ID))
	for _, rota := range []string{rotaPrevia, rotaConfirm} {
		r := httptest.NewRequest(http.MethodPost, rota, strings.NewReader(corpo))
		r.Header.Set("Content-Type", "application/json")
		ctx, cancelar := context.WithCancel(session.NewContext(r.Context(), session.Identity{
			UserID: a.usuario, HouseholdID: a.casa.ID, Role: session.RoleOwner, SessionID: "sess-1",
		}))
		cancelar() // o cliente já foi embora quando o handler começa
		r = r.WithContext(ctx)
		rec := httptest.NewRecorder()
		switch rota {
		case rotaPrevia:
			a.handler.Preview(rec, r)
		default:
			a.handler.Confirm(rec, r)
		}
		require.Equal(t, http.StatusInternalServerError, rec.Code, "%s: %s", rota, rec.Body.String())
	}
	logs := a.logs.String()
	assert.NotContains(t, logs, `"level":"ERROR"`, "cliente que desistiu não é erro do servidor: %s", logs)
	assert.Contains(t, logs, "cliente desistiu no meio")
	assert.NotContains(t, logs, "feira", "palavra no log")
	assert.Equal(t, []string{"zaffari"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID), "nada gravado")
}
