package transaction_test

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
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

// QA da E2c — rate limit de POST /transactions/auto-categorize (spec 0005
// emenda §10.8, §9 do plano): 60/h POR CASA com o valor REAL da configuração,
// a 61ª chamada é 429 com Retry-After, o 429 não escreve nem audita, a outra
// casa tem balde próprio, e o balde se recompõe com o tempo.
//
// A cadeia montada aqui é a mesma de cmd/api/routes.go: RateLimit(limiter,
// HouseholdKey(hash)) em volta do handler, com a identidade já no contexto
// (o que RequireAuth faz antes).

// cadeiaComLimite devolve o handler embrulhado no limitador por casa e o
// relógio que o limitador enxerga.
func cadeiaComLimite(t *testing.T, amb *httpAmbiente) (http.Handler, *time.Time) {
	t.Helper()
	regra := config.DefaultRateLimits().AutoCategorize
	require.Equal(t, 60, regra.Requests, "emenda §10.8: 60 por hora")
	require.Equal(t, time.Hour, regra.Window)

	agoraNoLimitador := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	limitador := httpserver.NewLimiter(regra.Requests, regra.Window, config.DefaultRateLimits().IdleTTL).
		WithClock(func() time.Time { return agoraNoLimitador })
	// A chave por casa é um HMAC em produção; aqui basta ser injetiva e não
	// ecoar o id em claro.
	hash := func(householdID string) string { return "h:" + strings.ToUpper(householdID) }
	h := httpserver.RateLimit(limitador, httpserver.HouseholdKey(hash))(http.HandlerFunc(amb.handler.AutoCategorize))
	return h, &agoraNoLimitador
}

func (a *httpAmbiente) chamarNaCadeia(t *testing.T, h http.Handler, casa, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transactions/auto-categorize", strings.NewReader(corpo))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(session.NewContext(r.Context(), session.Identity{
		UserID: usuario, HouseholdID: casa, Role: "owner", SessionID: "sess-1",
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestAutoCategorizeSexagesimaPrimeiraChamadaNaHoraE429ENaoEscreve(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-1", "Mercado", category.KindExpense)
	amb.palavraDeCategoria(minhaCasa, "cat-1", "supermercado")
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado Segredo", 150_00, 3)

	amb.conta(outraCasa, "acc-x", "Conta da Vizinha", account.KindChecking)
	amb.categoria(outraCasa, "cat-x", "Mercado dela", category.KindExpense)
	amb.palavraDeCategoria(outraCasa, "cat-x", "supermercado")
	alvoDela := amb.lancamento(outraCasa, "acc-x", transaction.KindExpense, "Supermercado dela", 10_00, 3)

	h, relogio := cadeiaComLimite(t, amb)
	previa := `{"month":"2026-09","dryRun":true}`
	real := `{"month":"2026-09","dryRun":false}`

	// 60 prévias na mesma hora: todas passam, nenhuma grava.
	for i := 1; i <= 60; i++ {
		rec := amb.chamarNaCadeia(t, h, minhaCasa, previa)
		require.Equal(t, http.StatusOK, rec.Code, "chamada %d: %s", i, rec.Body.String())
	}
	require.Nil(t, amb.repo.linhas[alvo.ID].CategoryID)
	require.Empty(t, amb.auditor.registros)

	// A 61ª — justamente a que GRAVARIA — é 429: nada escrito, nada auditado,
	// Retry-After presente, corpo genérico sem casa nem rota.
	rec := amb.chamarNaCadeia(t, h, minhaCasa, real)
	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), httpserver.CodeRateLimited)
	retry, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, retry, 1)
	assert.LessOrEqual(t, retry, 60, "60/h é um token por minuto: o próximo cabe em até 60 s")
	assert.NotContains(t, rec.Body.String(), minhaCasa)
	assert.NotContains(t, rec.Body.String(), "auto-categorize")
	assert.Nil(t, amb.repo.linhas[alvo.ID].CategoryID, "o 429 não pode ter chegado ao serviço")
	assert.Empty(t, amb.auditor.registros, "o 429 não audita")
	assert.NotContains(t, amb.logs.String(), "categorizados automaticamente", "o handler não rodou")

	// Repetir dá 429 de novo (o 429 não "gasta" token nem libera).
	assert.Equal(t, http.StatusTooManyRequests, amb.chamarNaCadeia(t, h, minhaCasa, real).Code)

	// A OUTRA casa tem balde próprio: passa e grava o dela — e só o dela.
	recDela := amb.chamarNaCadeia(t, h, outraCasa, real)
	require.Equal(t, http.StatusOK, recDela.Code, recDela.Body.String())
	assert.Equal(t, "cat-x", *amb.repo.linhas[alvoDela.ID].CategoryID)
	assert.Nil(t, amb.repo.linhas[alvo.ID].CategoryID, "a execução da vizinha não categorizou a minha linha")

	// Um minuto depois um token voltou: a execução real passa e grava.
	*relogio = relogio.Add(time.Duration(retry) * time.Second)
	recDepois := amb.chamarNaCadeia(t, h, minhaCasa, real)
	require.Equal(t, http.StatusOK, recDepois.Code, recDepois.Body.String())
	require.NotNil(t, amb.repo.linhas[alvo.ID].CategoryID)
	assert.Equal(t, "cat-1", *amb.repo.linhas[alvo.ID].CategoryID)

	// E o seguinte, no mesmo instante, volta a ser 429.
	assert.Equal(t, http.StatusTooManyRequests, amb.chamarNaCadeia(t, h, minhaCasa, previa).Code)

	// Nada sensível no log em nenhum dos caminhos.
	//
	// A conferência roda sobre o log SEM o carimbo de tempo (semRelogio): o
	// `"time"` do slog é RFC3339 com nanossegundos, e uma fração como
	// `.1000123` casa o literal "1000" por acaso — falha medida em 18/09/2026,
	// numa execução com -race do pacote inteiro. Um teste de vazamento que
	// acusa o próprio relógio ensina a equipe a ignorá-lo, que é o pior
	// desfecho possível para uma asserção de segurança. O que o campo de tempo
	// não pode conter, por construção, é dado da casa.
	semTempo := semRelogio(amb.logs.String())
	for _, proibido := range []string{"Segredo", "supermercado", "15007", "1000", alvo.ID} {
		assert.NotContains(t, semTempo, proibido, "vazou no log: %q", proibido)
	}
}

// semRelogio apaga o campo `"time":"…"` de cada linha do log estruturado.
//
// Ele existe para que asserções de VAZAMENTO por substring numérica não sejam
// decididas pelo relógio da máquina. Nada mais é removido: a mensagem, os
// atributos e os valores continuam inteiros sob os olhos do teste.
func semRelogio(logs string) string {
	var b strings.Builder
	resto := logs
	for {
		i := strings.Index(resto, `"time":"`)
		if i < 0 {
			b.WriteString(resto)
			return b.String()
		}
		b.WriteString(resto[:i])
		fim := strings.Index(resto[i+len(`"time":"`):], `"`)
		if fim < 0 {
			return b.String()
		}
		resto = resto[i+len(`"time":"`)+fim+1:]
	}
}

// Sem identidade no contexto (RequireAuth não rodou) a chave é constante e o
// limitador não deixa passar mais do que a cota — e o handler responde 401 de
// qualquer jeito. O importante: nunca uma chave por casa vinda do corpo.
func TestAutoCategorizeLimitadorSemIdentidadeNaoUsaNadaDoCorpo(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	h, _ := cadeiaComLimite(t, amb)
	for i := 0; i < 60; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/transactions/auto-categorize",
			strings.NewReader(`{"month":"2026-09","dryRun":false,"householdId":"casa-1"}`))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		require.Equal(t, http.StatusUnauthorized, rec.Code, "chamada %d", i+1)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/transactions/auto-categorize", strings.NewReader(`{"month":"2026-09","dryRun":false}`))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	assert.Equal(t, http.StatusTooManyRequests, rec.Code, "sem identidade cai numa chave única e limitada")
	assert.Empty(t, amb.auditor.registros)
}

// --- log dos caminhos de erro de GET /transfers ---------------------------------

// O 400 do cursor forjado e o 404 da conta alheia não levam nada da query
// para o log — nem o cursor, nem o id — e o 500 (quando acontece) é o único
// que loga, com request_id e razão, nunca com o corpo.
func TestListTransfersCaminhosDeErroNaoLogamAQuery(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "A", account.KindChecking)
	amb.conta(outraCasa, "acc-secreta", "Segredo da Vizinha", account.KindChecking)

	forjado := base64.RawURLEncoding.EncodeToString([]byte("occ=2026-09-01|id=' OR 1=1 --"))
	rec := amb.chamar(t, minhaCasa, http.MethodGet, "/transfers?month=2026-09&cursor="+forjado, "", amb.handler.ListTransfers)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	rec = amb.chamar(t, minhaCasa, http.MethodGet, "/transfers?month=2026-09&accountId=acc-secreta", "", amb.handler.ListTransfers)
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec = amb.chamar(t, minhaCasa, http.MethodGet, "/transfers?month=2026-13&accountId=acc-secreta", "", amb.handler.ListTransfers)
	require.Equal(t, http.StatusBadRequest, rec.Code)

	log := amb.logs.String()
	assert.NotContains(t, log, forjado)
	assert.NotContains(t, log, "OR 1=1")
	assert.NotContains(t, log, "acc-secreta")
	assert.NotContains(t, log, "Segredo")
	assert.NotContains(t, log, "2026-13")
}
