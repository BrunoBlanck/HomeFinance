package auth_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Critério de aceite 10: registrar -> ler o código -> verificar -> cookies ->
// /me com a casa "Casa de {nome}" e papel owner.
func TestFluxoDeCadastroCompleto(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{CookieSecure: true})
	c := h.client()
	const email = "bruno@exemplo.com"

	res := c.register(email, senhaPadrao)
	require.Equal(t, http.StatusAccepted, res.Status)

	corpo := res.json(t)
	assert.Equal(t, "verification_required", corpo["status"])
	assert.Equal(t, email, corpo["email"])
	assert.EqualValues(t, 900, corpo["expiresInSeconds"])

	// A resposta NUNCA carrega o código (docs/SEGURANCA.md §1.1).
	mail, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	require.Len(t, mail.Code, 6)
	assert.NotContains(t, res.Body, mail.Code)
	assert.Equal(t, email, mail.To)
	assert.Equal(t, 15*time.Minute, mail.TTL)

	// Antes de verificar, /me não existe.
	assert.Equal(t, http.StatusUnauthorized, c.me().Status)

	res = c.verify(email, mail.Code)
	require.Equal(t, http.StatusOK, res.Status, res.Body)

	require.NotNil(t, res.cookie("__Host-hf_access"))
	require.NotNil(t, res.cookie("__Host-hf_refresh"))

	me := res.json(t)
	usuario := me["user"].(map[string]any)
	assert.Equal(t, email, usuario["email"])
	assert.Equal(t, nomePadrao, usuario["name"])
	assert.NotNil(t, usuario["emailVerifiedAt"])

	casa := me["household"].(map[string]any)
	assert.Equal(t, "Casa de Bruno", casa["name"])
	assert.Equal(t, "owner", casa["role"])
	assert.Len(t, me["households"], 1)

	// A sessão emitida na verificação já autentica /me.
	res = c.me()
	require.Equal(t, http.StatusOK, res.Status)
	assert.Equal(t, "Casa de Bruno", res.json(t)["household"].(map[string]any)["name"])
}

// Critério de aceite 11.
func TestLoginAntesDeVerificarReemitreCodigo(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Minute})
	c := h.client()
	const email = "bruno@exemplo.com"

	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	primeiro, _ := h.mails.lastOf(mailVerification)

	// Passa o cooldown para o login poder reemitir.
	h.clock.Advance(2 * time.Minute)

	res := c.login(email, senhaPadrao)
	assert.Equal(t, http.StatusForbidden, res.Status)
	assert.Equal(t, "EMAIL_NOT_VERIFIED", res.errorCode(t))

	segundo, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	assert.NotEqual(t, primeiro.Code, segundo.Code, "o login não verificado dispara código novo")

	// Critério 22: emitir código novo invalida o anterior.
	assert.Equal(t, http.StatusUnauthorized, c.verify(email, primeiro.Code).Status)

	res = c.verify(email, segundo.Code)
	require.Equal(t, http.StatusOK, res.Status)

	// Agora o login funciona.
	novo := h.client()
	res = novo.login(email, senhaPadrao)
	require.Equal(t, http.StatusOK, res.Status)
	assert.NotNil(t, res.cookie("hf_access"))
	assert.NotNil(t, res.cookie("hf_refresh"))
}

// Critério de aceite 12.
func TestRefreshRotacionaOsDoisCookies(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.registerAndVerify("bruno@exemplo.com")

	accessAntigo := c.cookies["hf_access"]
	refreshAntigo := c.cookies["hf_refresh"]
	require.NotEmpty(t, refreshAntigo)

	h.clock.Advance(time.Minute)

	res := c.refresh()
	require.Equal(t, http.StatusOK, res.Status, res.Body)
	assert.NotEqual(t, refreshAntigo, c.cookies["hf_refresh"], "o refresh precisa rotacionar")
	assert.NotEqual(t, accessAntigo, c.cookies["hf_access"])
	assert.Equal(t, "Casa de Bruno", res.json(t)["household"].(map[string]any)["name"])

	// O refresh anterior não vale mais.
	velho := h.client()
	velho.cookies["hf_refresh"] = refreshAntigo
	res = velho.refresh()
	assert.Equal(t, http.StatusUnauthorized, res.Status)
	assert.Equal(t, "INVALID_SESSION", res.errorCode(t))

	// E o reúso derrubou a família: o cookie novo também morreu.
	res = c.refresh()
	assert.Equal(t, http.StatusUnauthorized, res.Status)
}

// Critério de aceite 13.
func TestFluxoDeRecuperacaoDeSenha(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	const email = "bruno@exemplo.com"
	const novaSenha = "outra-senha-bem-diferente-2026"

	logado := h.registerAndVerify(email)
	refreshAntigo := logado.cookies["hf_refresh"]
	require.NotEmpty(t, refreshAntigo)

	c := h.client()
	res := c.do(http.MethodPost, "/api/v1/auth/forgot-password", map[string]string{"email": email})
	require.Equal(t, http.StatusAccepted, res.Status)

	corpo := res.json(t)
	assert.Equal(t, "accepted", corpo["status"])
	assert.Equal(t, "Se este e-mail estiver cadastrado, enviamos um código de 6 dígitos.", corpo["message"])

	mail, ok := h.mails.lastOf(mailPasswordReset)
	require.True(t, ok)
	assert.NotContains(t, res.Body, mail.Code)

	res = c.do(http.MethodPost, "/api/v1/auth/reset-password", map[string]string{
		"email": email, "code": mail.Code, "newPassword": novaSenha,
	})
	require.Equal(t, http.StatusNoContent, res.Status, res.Body)

	// docs/SEGURANCA.md §1.1: troca de senha derruba TODAS as sessões.
	velho := h.client()
	velho.cookies["hf_refresh"] = refreshAntigo
	assert.Equal(t, http.StatusUnauthorized, velho.refresh().Status)

	// A senha antiga não vale mais; a nova, sim.
	novo := h.client()
	assert.Equal(t, http.StatusUnauthorized, novo.login(email, senhaPadrao).Status)
	assert.Equal(t, http.StatusOK, novo.login(email, novaSenha).Status)
}

// Critério de aceite 14.
func TestLogoutEhIdempotente(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.registerAndVerify("bruno@exemplo.com")
	refreshAntigo := c.cookies["hf_refresh"]

	res := c.logout()
	require.Equal(t, http.StatusNoContent, res.Status)
	assert.Empty(t, res.Body)
	assert.NotNil(t, res.cookie("hf_access"))
	assert.NotNil(t, res.cookie("hf_refresh"))
	for _, ck := range res.Cookies {
		assert.Negative(t, ck.MaxAge)
	}

	// O refresh deixou de valer.
	velho := h.client()
	velho.cookies["hf_refresh"] = refreshAntigo
	assert.Equal(t, http.StatusUnauthorized, velho.refresh().Status)

	// Segundo logout, agora sem cookie nenhum, continua 204.
	semCookie := h.client()
	assert.Equal(t, http.StatusNoContent, semCookie.logout().Status)
	assert.Equal(t, http.StatusNoContent, c.logout().Status)
}

// Critério de aceite 21: código de um propósito não serve para o outro.
func TestCodigoNaoAtravessaProposito(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	const email = "bruno@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	codigoVerificacao, _ := h.mails.lastOf(mailVerification)

	// Código de verificação de e-mail no reset-password.
	res := c.do(http.MethodPost, "/api/v1/auth/reset-password", map[string]string{
		"email": email, "code": codigoVerificacao.Code, "newPassword": "senha-nova-do-invasor-2026",
	})
	assert.Equal(t, http.StatusUnauthorized, res.Status)
	assert.Equal(t, "INVALID_CODE", res.errorCode(t))

	// E o caminho inverso: conclui o cadastro e pede recuperação.
	require.Equal(t, http.StatusOK, c.verify(email, codigoVerificacao.Code).Status)
	h.clock.Advance(2 * time.Minute)

	require.Equal(t, http.StatusAccepted,
		c.do(http.MethodPost, "/api/v1/auth/forgot-password", map[string]string{"email": email}).Status)
	codigoReset, ok := h.mails.lastOf(mailPasswordReset)
	require.True(t, ok)

	// Código de troca de senha no verify-email.
	res = c.verify(email, codigoReset.Code)
	assert.Equal(t, http.StatusUnauthorized, res.Status)
	assert.Equal(t, "INVALID_CODE", res.errorCode(t))
}

// Critério de aceite 22: emitir código novo invalida todos os anteriores.
func TestCodigoNovoInvalidaOsAnteriores(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: 30 * time.Second})
	const email = "bruno@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	primeiro, _ := h.mails.lastOf(mailVerification)

	h.clock.Advance(time.Minute)
	require.Equal(t, http.StatusAccepted, c.resend(email).Status)
	segundo, _ := h.mails.lastOf(mailVerification)
	require.NotEqual(t, primeiro.Code, segundo.Code)

	h.clock.Advance(time.Minute)
	require.Equal(t, http.StatusAccepted, c.resend(email).Status)
	terceiro, _ := h.mails.lastOf(mailVerification)

	assert.Equal(t, http.StatusUnauthorized, c.verify(email, primeiro.Code).Status)
	assert.Equal(t, http.StatusUnauthorized, c.verify(email, segundo.Code).Status)
	assert.Equal(t, http.StatusOK, c.verify(email, terceiro.Code).Status)
}

// Cooldown do reenvio (grupo B e defesa contra mail bombing).
func TestReenvioRespeitaCooldown(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Minute})
	const email = "bruno@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	require.Equal(t, 1, h.mails.countOf(mailVerification))

	// Dentro do cooldown: resposta idêntica, mas NENHUM e-mail novo.
	for range 3 {
		require.Equal(t, http.StatusAccepted, c.resend(email).Status)
	}
	assert.Equal(t, 1, h.mails.countOf(mailVerification), "o cooldown precisa segurar o reenvio")

	h.clock.Advance(2 * time.Minute)
	require.Equal(t, http.StatusAccepted, c.resend(email).Status)
	assert.Equal(t, 2, h.mails.countOf(mailVerification))
}

// D3: registro sobre e-mail não verificado sobrescreve as credenciais
// pendentes; sobre e-mail VERIFICADO não toca em nada.
func TestRegistroSobreContaExistente(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Second})
	const email = "bruno@exemplo.com"
	const senhaDoInvasor = "senha-escolhida-pelo-invasor-2026"

	c := h.client()

	// Conta pendente: o segundo registro assume o cadastro.
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	h.clock.Advance(2 * time.Second)
	require.Equal(t, http.StatusAccepted, c.register(email, senhaDoInvasor).Status)

	codigo, _ := h.mails.lastOf(mailVerification)
	require.Equal(t, http.StatusOK, c.verify(email, codigo.Code).Status)

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(email, senhaDoInvasor).Status,
		"conta pendente pode ser reassumida — ela ainda exigia a caixa de entrada")

	// Conta VERIFICADA: o registro não muda nada.
	h.mails.reset()
	h.clock.Advance(2 * time.Second)
	res := c.register(email, "mais-uma-senha-de-invasor-2026")
	require.Equal(t, http.StatusAccepted, res.Status)

	assert.Equal(t, 1, h.mails.countOf(mailAccountExists), "o titular é avisado da tentativa")
	assert.Equal(t, 0, h.mails.countOf(mailVerification), "nenhum código novo é emitido")

	outro := h.client()
	assert.Equal(t, http.StatusUnauthorized, outro.login(email, "mais-uma-senha-de-invasor-2026").Status)
	assert.Equal(t, http.StatusOK, outro.login(email, senhaDoInvasor).Status,
		"a senha da conta verificada continua intacta")
}

// Critério de aceite 19: reúso derruba a família e grava a ação exata no
// audit_log.
func TestReusoDeRefreshDerrubaFamiliaEGravaAuditoria(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.registerAndVerify("bruno@exemplo.com")

	roubado := c.cookies["hf_refresh"]
	h.clock.Advance(time.Minute)

	require.Equal(t, http.StatusOK, c.refresh().Status)
	legitimo := c.cookies["hf_refresh"]
	require.NotEqual(t, roubado, legitimo)

	// O ladrão usa o token antigo.
	ladrao := h.client()
	ladrao.cookies["hf_refresh"] = roubado
	res := ladrao.refresh()
	assert.Equal(t, http.StatusUnauthorized, res.Status)
	assert.Equal(t, "INVALID_SESSION", res.errorCode(t))

	// A vítima também é derrubada — é o comportamento correto: não dá para
	// saber quem é quem, então a família inteira cai.
	assert.Equal(t, http.StatusUnauthorized, c.refresh().Status)

	entradas, err := h.audit.ListByAction(t.Context(), audit.ActionRefreshReuseDetected, 10)
	require.NoError(t, err)
	require.NotEmpty(t, entradas, "o reúso precisa ficar registrado no audit_log")
	assert.Equal(t, "auth.refresh_reuse_detected", entradas[0].Action)
	require.NotNil(t, entradas[0].UserID)
}

func TestRefreshExpiradoNaoRenova(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.registerAndVerify("bruno@exemplo.com")

	h.clock.Advance(15 * 24 * time.Hour)

	res := c.refresh()
	assert.Equal(t, http.StatusUnauthorized, res.Status)
	assert.Equal(t, "INVALID_SESSION", res.errorCode(t))
}

func TestCodigoExpiradoNaoVerifica(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	const email = "bruno@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	mail, _ := h.mails.lastOf(mailVerification)

	h.clock.Advance(16 * time.Minute)

	res := c.verify(email, mail.Code)
	assert.Equal(t, http.StatusUnauthorized, res.Status)
	assert.Equal(t, "INVALID_CODE", res.errorCode(t))
}

func TestCodigoEhDeUsoUnico(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	const email = "bruno@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	mail, _ := h.mails.lastOf(mailVerification)

	token := c.regToken
	require.Equal(t, http.StatusOK, c.verify(email, mail.Code).Status)

	// A segunda requisição apresenta o MESMO par token+código: se o teste
	// mandasse outro token, ele passaria por escopo e não por uso único.
	segunda := h.client()
	res := segunda.verifyCom(email, mail.Code, token)
	assert.Equal(t, http.StatusUnauthorized, res.Status)
	assert.Equal(t, "INVALID_CODE", res.errorCode(t))
}

// Recuperação de senha para conta ainda não verificada não emite código de
// reset (a conta é inutilizável; o passo que falta é confirmar o e-mail).
func TestRecuperacaoNaoAtendeContaNaoVerificada(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	const email = "bruno@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	h.mails.reset()

	res := c.do(http.MethodPost, "/api/v1/auth/forgot-password", map[string]string{"email": email})
	assert.Equal(t, http.StatusAccepted, res.Status)
	assert.Zero(t, h.mails.countOf(mailPasswordReset))
}

func TestPurgeExpiredLimpaCodigosETokens(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	h.registerAndVerify("bruno@exemplo.com")

	h.clock.Advance(400 * 24 * time.Hour)

	codigos, tokens, err := h.svc.PurgeExpired(t.Context())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, codigos+tokens, int64(1))
}

func TestServicoRecusaDependenciaNula(t *testing.T) {
	t.Parallel()

	_, err := auth.NewService(auth.Deps{}, auth.ServiceOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "obrigatóri")
}
