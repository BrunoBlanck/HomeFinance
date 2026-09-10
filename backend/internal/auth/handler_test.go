package auth_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Critério de aceite 25 da spec 0001.
func TestEntradaMalformadaEhRejeitadaNaBorda(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{
		Middlewares: []httpserver.Middleware{httpserver.MaxBytes(2048)},
	})
	c := h.client()

	t.Run("campo desconhecido vira 400", func(t *testing.T) {
		res := c.doRaw(http.MethodPost, "/api/v1/auth/register", "application/json",
			`{"name":"Bruno","email":"bruno@exemplo.com","password":"cavalo-bateria-2026","role":"admin"}`)
		assert.Equal(t, http.StatusBadRequest, res.Status)
		assert.Equal(t, "VALIDATION_FAILED", res.errorCode(t))
	})

	t.Run("content-type errado vira 415", func(t *testing.T) {
		res := c.doRaw(http.MethodPost, "/api/v1/auth/register", "text/plain",
			`{"name":"Bruno","email":"bruno@exemplo.com","password":"cavalo-bateria-2026"}`)
		assert.Equal(t, http.StatusUnsupportedMediaType, res.Status)
		assert.Equal(t, "UNSUPPORTED_MEDIA_TYPE", res.errorCode(t))
	})

	t.Run("sem content-type vira 415", func(t *testing.T) {
		res := c.doRaw(http.MethodPost, "/api/v1/auth/login", "", `{"email":"a@b.com","password":"x"}`)
		assert.Equal(t, http.StatusUnsupportedMediaType, res.Status)
	})

	t.Run("corpo grande demais vira 413", func(t *testing.T) {
		gigante := `{"name":"` + strings.Repeat("a", 8000) + `","email":"bruno@exemplo.com","password":"cavalo-bateria-2026"}`
		res := c.doRaw(http.MethodPost, "/api/v1/auth/register", "application/json", gigante)
		assert.Equal(t, http.StatusRequestEntityTooLarge, res.Status)
		assert.Equal(t, "PAYLOAD_TOO_LARGE", res.errorCode(t))
	})

	t.Run("json invalido vira 400", func(t *testing.T) {
		res := c.doRaw(http.MethodPost, "/api/v1/auth/register", "application/json", `{ isso não é json`)
		assert.Equal(t, http.StatusBadRequest, res.Status)
	})

	t.Run("corpo vazio vira 400", func(t *testing.T) {
		res := c.doRaw(http.MethodPost, "/api/v1/auth/register", "application/json", "")
		assert.Equal(t, http.StatusBadRequest, res.Status)
	})

	t.Run("metodo errado vira 405 no formato unico", func(t *testing.T) {
		res := c.doRaw(http.MethodGet, "/api/v1/auth/register", "", "")
		assert.Equal(t, http.StatusMethodNotAllowed, res.Status)
		assert.Equal(t, "METHOD_NOT_ALLOWED", res.errorCode(t))
	})

	t.Run("rota inexistente vira 404 no formato unico", func(t *testing.T) {
		res := c.doRaw(http.MethodGet, "/api/v1/nao-existe", "", "")
		assert.Equal(t, http.StatusNotFound, res.Status)
		assert.Equal(t, "NOT_FOUND", res.errorCode(t))
	})
}

func TestValidacaoDeCampos(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.client()

	casos := []struct {
		nome   string
		rota   string
		corpo  map[string]string
		campo  string
		trecho string
	}{
		{
			nome:  "senha curta",
			rota:  "/api/v1/auth/register",
			corpo: map[string]string{"name": "Bruno", "email": "bruno@exemplo.com", "password": "curta"},
			campo: "password",
		},
		{
			nome:  "e-mail invalido",
			rota:  "/api/v1/auth/register",
			corpo: map[string]string{"name": "Bruno", "email": "sem-arroba", "password": senhaPadrao},
			campo: "email",
		},
		{
			nome:  "nome vazio",
			rota:  "/api/v1/auth/register",
			corpo: map[string]string{"name": "   ", "email": "bruno@exemplo.com", "password": senhaPadrao},
			campo: "name",
		},
		{
			nome:  "codigo com letras",
			rota:  "/api/v1/auth/verify-email",
			corpo: map[string]string{"email": "bruno@exemplo.com", "code": "12345a"},
			campo: "code",
		},
		{
			nome:  "codigo curto",
			rota:  "/api/v1/auth/verify-email",
			corpo: map[string]string{"email": "bruno@exemplo.com", "code": "1234"},
			campo: "code",
		},
		{
			nome:  "senha nova fraca",
			rota:  "/api/v1/auth/reset-password",
			corpo: map[string]string{"email": "bruno@exemplo.com", "code": "123456", "newPassword": "123456789012"},
			campo: "newPassword",
		},
	}

	for _, cs := range casos {
		t.Run(cs.nome, func(t *testing.T) {
			res := c.do(http.MethodPost, cs.rota, cs.corpo)
			require.Equal(t, http.StatusBadRequest, res.Status, res.Body)

			corpo := res.json(t)
			errObj := corpo["error"].(map[string]any)
			assert.Equal(t, "VALIDATION_FAILED", errObj["code"])
			assert.Equal(t, "Dados inválidos.", errObj["message"])

			fields, ok := errObj["fields"].(map[string]any)
			require.True(t, ok, "VALIDATION_FAILED precisa trazer fields: %s", res.Body)
			assert.Contains(t, fields, cs.campo)

			// A mensagem de campo nunca ecoa o valor enviado.
			for _, v := range cs.corpo {
				if len(v) >= 6 {
					assert.NotContains(t, res.Body, v)
				}
			}
		})
	}
}

// "fields" só existe em VALIDATION_FAILED (§3 da spec 0001).
func TestFieldsSoApareceEmValidacao(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.client()

	res := c.login("ninguem@exemplo.com", senhaPadrao)
	require.Equal(t, http.StatusUnauthorized, res.Status)
	errObj := res.json(t)["error"].(map[string]any)
	assert.NotContains(t, errObj, "fields")
}

// Critério de aceite 27: o 429 do limite por conta não pode revelar se a
// conta existe.
func TestLimitePorContaResponde429ComRetryAfter(t *testing.T) {
	t.Parallel()

	limiter := httpserver.NewLimiter(2, time.Minute, time.Hour)
	h := newHarness(t, harnessOptions{LoginLimiter: limiter})
	const email = "bruno@exemplo.com"
	h.registerAndVerify(email)

	c := h.client()
	// As duas primeiras tentativas passam pelo limitador.
	assert.Equal(t, http.StatusUnauthorized, c.login(email, "senha-errada-mas-longa-2026").Status)
	assert.Equal(t, http.StatusUnauthorized, c.login(email, "senha-errada-mas-longa-2026").Status)

	res := c.login(email, senhaPadrao)
	require.Equal(t, http.StatusTooManyRequests, res.Status)
	assert.Equal(t, "RATE_LIMITED", res.errorCode(t))

	retry, err := strconv.Atoi(res.Header.Get("Retry-After"))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, retry, 1)

	// Conta inexistente recebe exatamente a mesma resposta ao estourar.
	outro := h.client()
	assert.Equal(t, http.StatusUnauthorized, outro.login("ninguem@exemplo.com", senhaPadrao).Status)
	assert.Equal(t, http.StatusUnauthorized, outro.login("ninguem@exemplo.com", senhaPadrao).Status)

	resInexistente := outro.login("ninguem@exemplo.com", senhaPadrao)
	assert.Equal(t, http.StatusTooManyRequests, resInexistente.Status)
	assert.Equal(t, res.Body, resInexistente.Body,
		"o 429 precisa ser idêntico para conta existente e inexistente")
}

func TestLimitePorContaDeEsqueciSenha(t *testing.T) {
	t.Parallel()

	limiter := httpserver.NewLimiter(1, time.Hour, time.Hour)
	h := newHarness(t, harnessOptions{ForgotLimiter: limiter})
	const email = "bruno@exemplo.com"
	h.registerAndVerify(email)

	c := h.client()
	pedir := func() response {
		return c.do(http.MethodPost, "/api/v1/auth/forgot-password", map[string]string{"email": email})
	}

	assert.Equal(t, http.StatusAccepted, pedir().Status)
	res := pedir()
	assert.Equal(t, http.StatusTooManyRequests, res.Status)
	assert.NotEmpty(t, res.Header.Get("Retry-After"))
}

// Critério de aceite 17 pelo caminho HTTP real.
func TestCookiesDeSessaoTemFlagsCorretas(t *testing.T) {
	t.Parallel()

	t.Run("com COOKIE_SECURE=true", func(t *testing.T) {
		h := newHarness(t, harnessOptions{CookieSecure: true})
		c := h.client()
		require.Equal(t, http.StatusAccepted, c.register("bruno@exemplo.com", senhaPadrao).Status)
		mail, _ := h.mails.lastOf(mailVerification)

		res := c.verify("bruno@exemplo.com", mail.Code)
		require.Equal(t, http.StatusOK, res.Status)
		require.Len(t, res.Cookies, 2)

		for _, ck := range res.Cookies {
			assert.True(t, strings.HasPrefix(ck.Name, "__Host-"), "cookie %s sem prefixo __Host-", ck.Name)
			assert.True(t, ck.HttpOnly)
			assert.True(t, ck.Secure)
			assert.Equal(t, http.SameSiteStrictMode, ck.SameSite)
			assert.Equal(t, "/", ck.Path)
			assert.Empty(t, ck.Domain)
		}
	})

	t.Run("com COOKIE_SECURE=false", func(t *testing.T) {
		h := newHarness(t, harnessOptions{CookieSecure: false})
		c := h.registerAndVerify("bruno@exemplo.com")
		res := c.login("bruno@exemplo.com", senhaPadrao)
		require.Equal(t, http.StatusOK, res.Status)

		for _, ck := range res.Cookies {
			assert.False(t, strings.HasPrefix(ck.Name, "__Host-"))
			assert.True(t, ck.HttpOnly, "HttpOnly vale mesmo em desenvolvimento")
			assert.Equal(t, http.SameSiteStrictMode, ck.SameSite)
		}
	})
}

// O token de sessão nunca pode aparecer no CORPO da resposta — só em cookie
// HttpOnly (docs/SEGURANCA.md §1).
func TestTokensSoViajamEmCookie(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.registerAndVerify("bruno@exemplo.com")

	res := c.login("bruno@exemplo.com", senhaPadrao)
	require.Equal(t, http.StatusOK, res.Status)

	corpo := res.Body
	assert.NotContains(t, corpo, "accessToken")
	assert.NotContains(t, corpo, "refreshToken")
	assert.NotContains(t, corpo, "token")
	for _, ck := range res.Cookies {
		assert.NotContains(t, corpo, ck.Value)
	}
}

// §3.12: os endpoints sensíveis passam por padTo.
func TestEndpointsSensiveisTemPisoDeTempo(t *testing.T) {
	t.Parallel()

	const piso = 150 * time.Millisecond
	h := newHarness(t, harnessOptions{MinResponseTime: piso})
	c := h.client()

	rotas := []struct {
		nome  string
		rota  string
		corpo map[string]string
	}{
		{"register", "/api/v1/auth/register", map[string]string{"name": "Bruno", "email": "a@exemplo.com", "password": senhaPadrao}},
		{"resend-code", "/api/v1/auth/resend-code", map[string]string{"email": "b@exemplo.com"}},
		{"verify-email", "/api/v1/auth/verify-email", map[string]string{"email": "c@exemplo.com", "code": "123456"}},
		{"login", "/api/v1/auth/login", map[string]string{"email": "d@exemplo.com", "password": senhaPadrao}},
		{"forgot-password", "/api/v1/auth/forgot-password", map[string]string{"email": "e@exemplo.com"}},
		{"reset-password", "/api/v1/auth/reset-password", map[string]string{"email": "f@exemplo.com", "code": "123456", "newPassword": senhaPadrao}},
	}

	for _, r := range rotas {
		t.Run(r.nome, func(t *testing.T) {
			inicio := time.Now()
			c.do(http.MethodPost, r.rota, r.corpo)
			assert.GreaterOrEqual(t, time.Since(inicio), piso-20*time.Millisecond,
				"%s respondeu antes do piso: o tempo vira canal lateral", r.nome)
		})
	}
}

// Refresh e logout NÃO passam por padTo: não há existência de conta a
// esconder ali, e segurar 300 ms por renovação seria custo puro.
//
// O piso é propositalmente ENORME e a margem de folga também: este pacote
// roda com t.Parallel() em massa e Argon2 de 64 MiB, então uma asserção
// apertada de relógio de parede aqui falha por escalonamento, não por
// regressão — foi o que aconteceu com a versão anterior (piso de 400 ms,
// teto de 300 ms, medido 320 ms). Com 5 s de piso, qualquer padTo aplicado
// por engano estoura o teto de 2 s com folga de uma ordem de grandeza.
func TestRefreshNaoTemPisoDeTempo(t *testing.T) {
	t.Parallel()

	const piso = 5 * time.Second
	h := newHarness(t, harnessOptions{MinResponseTime: piso})
	c := h.client()

	inicio := time.Now()
	res := c.refresh()
	decorrido := time.Since(inicio)

	assert.Equal(t, http.StatusUnauthorized, res.Status)
	assert.Less(t, decorrido, 2*time.Second,
		"o refresh ganhou piso de tempo (padTo de %s aplicado)", piso)
}

// Nenhuma resposta pode carregar detalhe interno (docs/SEGURANCA.md §4).
func TestRespostasNaoVazamDetalheInterno(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.client()

	respostas := []response{
		c.register("bruno@exemplo.com", senhaPadrao),
		c.login("ninguem@exemplo.com", senhaPadrao),
		c.verify("ninguem@exemplo.com", "000000"),
		c.refresh(),
		c.me(),
		c.doRaw(http.MethodPost, "/api/v1/auth/register", "application/json", "{"),
	}

	proibidos := []string{
		"gorm", "sql", "SELECT", "UPDATE", "goroutine", "argon2",
		"/internal/", "backend\\", ".go:", "panic",
	}
	for _, r := range respostas {
		for _, p := range proibidos {
			assert.NotContains(t, r.Body, p, "resposta vazou %q: %s", p, r.Body)
		}
	}
}
