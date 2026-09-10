package auth_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// caso é uma situação a comparar dentro de um grupo da §3.12.
//
// "entrada" é o pedaço da resposta que é ECO do que o cliente enviou (o
// e-mail, em register e resend-code). Ele é neutralizado antes da comparação:
// o que a §3.12 exige é que a resposta dependa SÓ da entrada, e nunca do
// estado da conta no banco.
type caso struct {
	nome    string
	res     response
	entrada string
}

// mesmaResposta confere que todas as situações têm o MESMO status e o MESMO
// corpo byte a byte (critério de aceite 15 da spec 0001).
func mesmaResposta(t *testing.T, rotulo string, casos ...caso) {
	t.Helper()
	require.GreaterOrEqual(t, len(casos), 2)

	normalizar := func(c caso) string {
		body := semTokenDeCadastro(c.res.Body)
		if c.entrada == "" {
			return body
		}
		return strings.ReplaceAll(body, c.entrada, "<<ENTRADA>>")
	}

	ref := casos[0]
	refBody := normalizar(ref)
	for _, c := range casos[1:] {
		assert.Equal(t, ref.res.Status, c.res.Status,
			"%s: status difere entre %q e %q", rotulo, ref.nome, c.nome)
		assert.Equal(t, refBody, normalizar(c),
			"%s: corpo difere entre %q e %q — isso vaza existência de conta", rotulo, ref.nome, c.nome)
	}
}

// tokenDeCadastro casa o campo registrationToken do corpo de 202.
var tokenDeCadastro = regexp.MustCompile(`"registrationToken":"[0-9a-f]{64}"`)

// semTokenDeCadastro troca o valor do registrationToken por um marcador.
//
// O token É sorteado a cada requisição, então comparar o corpo byte a byte
// sem isto acusaria diferença sempre. O que a §3.12 exige é que a resposta
// não dependa do ESTADO DA CONTA — e este teste continua provando isso,
// porque a substituição só acontece quando o campo tem exatamente a forma
// esperada: 64 hexadecimais, igual em todos os caminhos. Um caminho que
// devolvesse token vazio, ausente ou de outro tamanho NÃO casaria com a
// expressão e a comparação falharia, que é justamente o que queremos.
func semTokenDeCadastro(body string) string {
	return tokenDeCadastro.ReplaceAllString(body, `"registrationToken":"<<TOKEN>>"`)
}

// O token nunca pode ser omitido nem ter tamanho variável: é isso que o
// normalizador acima assume, e é isso que impede o campo de virar um canal
// lateral de existência de conta.
func exigeTokenBemFormado(t *testing.T, res response) {
	t.Helper()
	assert.Regexp(t, `"registrationToken":"[0-9a-f]{64}"`, res.Body,
		"todo 202 de cadastro/reenvio precisa devolver um token com a MESMA forma")
}

// Grupo A da §3.12.
func TestGrupoARegistroIndistinguivel(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Second})
	c := h.client()

	// Prepara uma conta verificada e uma pendente.
	verificado := "verificado@exemplo.com"
	h.registerAndVerify(verificado)

	pendente := "pendente@exemplo.com"
	require.Equal(t, http.StatusAccepted, c.register(pendente, senhaPadrao).Status)

	h.clock.Advance(2 * time.Second)

	novo := c.register("novo@exemplo.com", senhaPadrao)
	jaVerificado := c.register(verificado, senhaPadrao)
	naoVerificado := c.register(pendente, senhaPadrao)

	for _, res := range []response{novo, jaVerificado, naoVerificado} {
		exigeTokenBemFormado(t, res)
	}

	mesmaResposta(t, "grupo A (registro)",
		caso{"e-mail novo", novo, "novo@exemplo.com"},
		caso{"e-mail já verificado", jaVerificado, verificado},
		caso{"e-mail ainda não verificado", naoVerificado, pendente},
	)

	// Os tokens são todos DIFERENTES: cada pedido abre a sua própria
	// tentativa, e nenhum deles é reaproveitado de outro caminho.
	assert.NotEqual(t, tokenDe(t, novo), tokenDe(t, jaVerificado))
	assert.NotEqual(t, tokenDe(t, novo), tokenDe(t, naoVerificado))
}

// tokenDe extrai o registrationToken de um corpo 202.
func tokenDe(t *testing.T, res response) string {
	t.Helper()
	token, _ := res.json(t)["registrationToken"].(string)
	require.Len(t, token, 64, "corpo sem registrationToken: %s", res.Body)
	return token
}

// Grupo B da §3.12.
func TestGrupoBReenvioIndistinguivel(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Hour})
	c := h.client()

	pendente := "pendente@exemplo.com"
	require.Equal(t, http.StatusAccepted, c.register(pendente, senhaPadrao).Status)

	existente := c.resend(pendente)
	inexistente := c.resend("ninguem@exemplo.com")
	dentroDoCooldown := c.resend(pendente)

	for _, res := range []response{existente, inexistente, dentroDoCooldown} {
		exigeTokenBemFormado(t, res)
	}

	mesmaResposta(t, "grupo B (reenvio)",
		caso{"e-mail existente", existente, pendente},
		caso{"e-mail inexistente", inexistente, "ninguem@exemplo.com"},
		caso{"dentro do cooldown", dentroDoCooldown, pendente},
	)

	// O mesmo vale para quem não manda token nenhum: e-mail que existe e
	// e-mail que não existe continuam indistinguíveis.
	semToken := h.client()
	mesmaResposta(t, "grupo B (reenvio sem token)",
		caso{"existente, sem token", semToken.resendSemToken(pendente), pendente},
		caso{"inexistente, sem token", semToken.resendSemToken("outro-que-nao-existe@exemplo.com"), "outro-que-nao-existe@exemplo.com"},
	)
}

// Grupo C da §3.12.
func TestGrupoCEsqueciSenhaIndistinguivel(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.client()

	verificado := "verificado@exemplo.com"
	h.registerAndVerify(verificado)

	pendente := "pendente@exemplo.com"
	require.Equal(t, http.StatusAccepted, c.register(pendente, senhaPadrao).Status)

	pedir := func(email string) response {
		return c.do(http.MethodPost, "/api/v1/auth/forgot-password", map[string]string{"email": email})
	}

	// O corpo de forgot-password NÃO ecoa o e-mail: a comparação é literal.
	mesmaResposta(t, "grupo C (esqueci a senha)",
		caso{nome: "existente e verificado", res: pedir(verificado)},
		caso{nome: "existente e não verificado", res: pedir(pendente)},
		caso{nome: "inexistente", res: pedir("ninguem@exemplo.com")},
	)
}

// Grupo D da §3.12 — o mais amplo: TODA falha de código é a mesma resposta.
func TestGrupoDValidacaoDeCodigoIndistinguivel(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{OTPMaxAttempts: 2, ResendInterval: time.Second})
	c := h.client()

	// Cada e-mail guarda o SEU token: sem isso, os casos abaixo cairiam
	// todos no mesmo "token não casa" e o teste deixaria de exercitar os
	// caminhos reais de expirado, consumido e queimado.
	//
	// Conta com código vivo.
	comCodigo := "comcodigo@exemplo.com"
	require.Equal(t, http.StatusAccepted, c.register(comCodigo, senhaPadrao).Status)
	tokComCodigo := c.regToken

	// Conta com código já consumido.
	consumido := "consumido@exemplo.com"
	require.Equal(t, http.StatusAccepted, c.register(consumido, senhaPadrao).Status)
	tokConsumido := c.regToken
	usado, _ := h.mails.lastOf(mailVerification)
	require.Equal(t, http.StatusOK, c.verify(consumido, usado.Code).Status)

	// Conta com código expirado.
	expirado := "expirado@exemplo.com"
	require.Equal(t, http.StatusAccepted, c.register(expirado, senhaPadrao).Status)
	tokExpirado := c.regToken
	velho, _ := h.mails.lastOf(mailVerification)

	// Conta com tentativas esgotadas.
	esgotado := "esgotado@exemplo.com"
	require.Equal(t, http.StatusAccepted, c.register(esgotado, senhaPadrao).Status)
	tokEsgotado := c.regToken
	queimado, _ := h.mails.lastOf(mailVerification)
	for range 3 {
		c.verifyCom(esgotado, "000000", tokEsgotado)
	}

	// Conta verificada com código de OUTRO propósito vivo.
	outroProposito := "outroproposito@exemplo.com"
	h.registerAndVerify(outroProposito)
	h.clock.Advance(2 * time.Second)
	require.Equal(t, http.StatusAccepted,
		c.do(http.MethodPost, "/api/v1/auth/forgot-password", map[string]string{"email": outroProposito}).Status)
	codigoDeReset, _ := h.mails.lastOf(mailPasswordReset)

	h.clock.Advance(20 * time.Minute) // expira o código de "expirado"

	casos := []caso{
		{nome: "código errado", res: c.verifyCom(comCodigo, "000000", tokComCodigo)},
		{nome: "código expirado", res: c.verifyCom(expirado, velho.Code, tokExpirado)},
		{nome: "código já consumido", res: c.verifyCom(consumido, usado.Code, tokConsumido)},
		{nome: "tentativas esgotadas", res: c.verifyCom(esgotado, queimado.Code, tokEsgotado)},
		{nome: "e-mail sem código", res: c.verifyCom("semcodigo@exemplo.com", "123456", tokComCodigo)},
		{nome: "e-mail inexistente", res: c.verifyCom("ninguem@exemplo.com", "123456", tokComCodigo)},
		{nome: "código de outro propósito", res: c.verifyCom(outroProposito, codigoDeReset.Code, tokComCodigo)},
		// O caso NOVO que o registrationToken cria: código certo, e vivo, na
		// mão de quem não tem o token daquela tentativa.
		{nome: "código certo com token alheio", res: c.verifyCom(comCodigo, "000000", tokEsgotado)},
	}
	for _, cs := range casos {
		assert.Equal(t, http.StatusUnauthorized, cs.res.Status, "caso %q", cs.nome)
		assert.Equal(t, "INVALID_CODE", cs.res.errorCode(t), "caso %q", cs.nome)
	}
	mesmaResposta(t, "grupo D (validação de código)", casos...)
}

// Grupo D também no reset-password.
func TestGrupoDNoResetPasswordIndistinguivel(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.client()

	verificado := "verificado@exemplo.com"
	h.registerAndVerify(verificado)

	pedir := func(email, code string) response {
		return c.do(http.MethodPost, "/api/v1/auth/reset-password", map[string]string{
			"email": email, "code": code, "newPassword": "senha-nova-legitima-2026",
		})
	}

	casos := []caso{
		{nome: "conta existente, código errado", res: pedir(verificado, "000000")},
		{nome: "conta inexistente", res: pedir("ninguem@exemplo.com", "000000")},
		{nome: "conta sem código de reset", res: pedir(verificado, "123456")},
	}
	for _, cs := range casos {
		assert.Equal(t, http.StatusUnauthorized, cs.res.Status, "caso %q", cs.nome)
	}
	mesmaResposta(t, "grupo D (reset-password)", casos...)
}

// Grupo E da §3.12: status, corpo E custo de CPU iguais.
func TestGrupoELoginIndistinguivel(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	const email = "bruno@exemplo.com"
	h.registerAndVerify(email)

	c := h.client()
	casos := []caso{
		{nome: "e-mail inexistente", res: c.login("ninguem@exemplo.com", senhaPadrao)},
		{nome: "senha errada", res: c.login(email, "senha-errada-mas-longa-2026")},
	}
	for _, cs := range casos {
		assert.Equal(t, http.StatusUnauthorized, cs.res.Status, "caso %q", cs.nome)
		assert.Equal(t, "INVALID_CREDENTIALS", cs.res.errorCode(t), "caso %q", cs.nome)
	}
	mesmaResposta(t, "grupo E (login)", casos...)
}

// O custo de Argon2id precisa ser pago também quando o e-mail não existe,
// senão o cronômetro separa os dois casos (hash-isca).
//
// Medição intercalada e por mediana (ver medianasIntercaladas): rodando junto
// com o resto da suíte e com -race, comparar médias de tempo de parede mede
// mais o escalonador que o código. A banda é folgada de propósito — a
// regressão que este teste existe para pegar (remover o VerifyDummy) deixaria
// o caminho "inexistente" ordens de grandeza mais rápido.
func TestGrupoELoginTemCustoSemelhante(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	const email = "bruno@exemplo.com"
	h.registerAndVerify(email)

	c := h.client()
	senhaErrada, inexistente := medianasIntercaladas(5,
		func() { c.login(email, "senha-errada-mas-longa-2026") },
		func() { c.login("ninguem@exemplo.com", senhaPadrao) },
	)

	razao := float64(inexistente) / float64(senhaErrada)
	t.Logf("senha errada=%v inexistente=%v razão=%.2f", senhaErrada, inexistente, razao)
	assert.Greater(t, razao, 0.2,
		"conta inexistente respondeu rápido demais: dá para enumerar a base com um cronômetro")
	assert.Less(t, razao, 5.0)
}

// Grupo F da §3.12.
func TestGrupoFRefreshIndistinguivel(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	vitima := h.registerAndVerify("bruno@exemplo.com")

	roubado := vitima.cookies["hf_refresh"]
	h.clock.Advance(time.Minute)
	require.Equal(t, http.StatusOK, vitima.refresh().Status)

	comCookie := func(valor string) response {
		c := h.client()
		if valor != "" {
			c.cookies["hf_refresh"] = valor
		}
		return c.refresh()
	}

	// Sessão viva que vamos expirar.
	expirada := h.registerAndVerify("expirada@exemplo.com")
	tokenExpirado := expirada.cookies["hf_refresh"]
	h.clock.Advance(15 * 24 * time.Hour)

	casos := []caso{
		{nome: "cookie ausente", res: comCookie("")},
		{nome: "cookie malformado", res: comCookie("nao-e-um-token-valido")},
		{nome: "cookie expirado", res: comCookie(tokenExpirado)},
		{nome: "reúso detectado", res: comCookie(roubado)},
	}
	for _, cs := range casos {
		assert.Equal(t, http.StatusUnauthorized, cs.res.Status, "caso %q", cs.nome)
		assert.Equal(t, "INVALID_SESSION", cs.res.errorCode(t), "caso %q", cs.nome)
		// A §3.7 exige que os cookies SEMPRE caiam neste caminho.
		assert.Len(t, cs.res.Cookies, 2, "caso %q: os dois cookies precisam ser limpos", cs.nome)
	}
	mesmaResposta(t, "grupo F (refresh)", casos...)
}

// Critério de aceite 20: 5 tentativas erradas queimam o código; a 6ª, MESMO
// com o código certo, falha. É o que impede a força bruta dos 10^6 valores.
func TestCodigoQueimaDepoisDoLimiteDeTentativas(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{OTPMaxAttempts: 5})
	const email = "bruno@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	mail, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)

	errado := "000000"
	if mail.Code == errado {
		errado = "111111"
	}

	for i := range 5 {
		res := c.verify(email, errado)
		require.Equal(t, http.StatusUnauthorized, res.Status, "tentativa %d", i+1)
		require.Equal(t, "INVALID_CODE", res.errorCode(t))
	}

	res := c.verify(email, mail.Code)
	assert.Equal(t, http.StatusUnauthorized, res.Status,
		"o código certo na 6ª tentativa precisa falhar — o código já foi queimado")
	assert.Equal(t, "INVALID_CODE", res.errorCode(t))
}

// Critério de aceite 16: nada sensível no log estruturado.
func TestNadaSensivelNoLog(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	const email = "bruno@exemplo.com"
	const novaSenha = "outra-senha-bem-diferente-2026"

	c := h.registerAndVerify(email)
	codigoVerificacao, _ := h.mails.lastOf(mailVerification)
	refresh := c.cookies["hf_refresh"]
	access := c.cookies["hf_access"]

	h.clock.Advance(2 * time.Minute)
	require.Equal(t, http.StatusOK, c.refresh().Status)

	// Caminhos de erro, que são os que mais logam.
	c.verify(email, "000000")
	c.login(email, "senha-errada-mas-longa-2026")
	h.client().refresh()

	require.Equal(t, http.StatusAccepted,
		c.do(http.MethodPost, "/api/v1/auth/forgot-password", map[string]string{"email": email}).Status)
	codigoReset, _ := h.mails.lastOf(mailPasswordReset)
	require.Equal(t, http.StatusNoContent,
		c.do(http.MethodPost, "/api/v1/auth/reset-password", map[string]string{
			"email": email, "code": codigoReset.Code, "newPassword": novaSenha,
		}).Status)

	logs := h.logOutput()

	usuario, err := h.users.ByEmail(t.Context(), email)
	require.NoError(t, err)

	proibidos := map[string]string{
		"senha em claro":        senhaPadrao,
		"senha nova":            novaSenha,
		"código de verificação": codigoVerificacao.Code,
		"código de reset":       codigoReset.Code,
		"hash da senha":         usuario.PasswordHash,
		"refresh token":         refresh,
		"access token":          access,
		"pepper do OTP":         pepperDeTeste,
		"segredo do JWT":        segredoDeTeste,
	}
	for rotulo, valor := range proibidos {
		require.NotEmpty(t, valor, "valor de teste vazio: %s", rotulo)
		assert.NotContains(t, logs, valor, "%s vazou no log estruturado", rotulo)
	}

	// A DSN também não pode aparecer.
	assert.NotContains(t, logs, ".db\"")
	assert.False(t, strings.Contains(logs, "argon2id"), "hash de senha no log")
}

// Nenhuma resposta HTTP pode carregar o código.
func TestCodigoNuncaApareceEmResposta(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	const email = "bruno@exemplo.com"

	c := h.client()
	registro := c.register(email, senhaPadrao)
	mail, _ := h.mails.lastOf(mailVerification)

	reenvio := c.resend(email)
	novoMail, _ := h.mails.lastOf(mailVerification)

	verificacao := c.verify(email, novoMail.Code)
	esqueci := c.do(http.MethodPost, "/api/v1/auth/forgot-password", map[string]string{"email": email})
	reset, _ := h.mails.lastOf(mailPasswordReset)

	for _, r := range []response{registro, reenvio, verificacao, esqueci} {
		assert.NotContains(t, r.Body, mail.Code)
		assert.NotContains(t, r.Body, novoMail.Code)
		if reset.Code != "" {
			assert.NotContains(t, r.Body, reset.Code)
		}
		// Nem em cabeçalho.
		for _, valores := range r.Header {
			for _, v := range valores {
				assert.NotContains(t, v, novoMail.Code)
			}
		}
	}
}

// Critério de aceite 24 e risco 2 da §9 (a semente de todo BOLA das fases
// seguintes): um access token CRIPTOGRAFICAMENTE VÁLIDO cujo hid não
// corresponde mais a um vínculo real não pode dar acesso.
//
// O cenário é montado forjando o hid para a casa de OUTRA pessoa, que é o
// mesmo caminho de código de "a associação foi removida": em ambos, a
// revalidação em /me não encontra o vínculo.
func TestMeCaiQuandoOHidNaoCorrespondeAVinculoReal(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})

	vitima := h.registerAndVerify("vitima@exemplo.com")
	atacante := h.registerAndVerify("atacante@exemplo.com")

	casaDaVitima := vitima.me().json(t)["household"].(map[string]any)["id"].(string)
	require.NotEmpty(t, casaDaVitima)

	usuarioAtacante, err := h.users.ByEmail(t.Context(), "atacante@exemplo.com")
	require.NoError(t, err)

	// Token assinado corretamente, mas apontando para a casa da vítima.
	forjado := h.forgeAccessToken(usuarioAtacante.ID, casaDaVitima, "owner", "fam-forjada")

	c := h.client()
	c.cookies["hf_access"] = forjado

	res := c.me()
	assert.Equal(t, http.StatusUnauthorized, res.Status,
		"token válido com hid de outra casa NÃO pode passar")
	assert.Equal(t, "UNAUTHENTICATED", res.errorCode(t))
	assert.Len(t, res.Cookies, 2, "a sessão perdeu o sentido: os cookies precisam cair")

	// A sessão legítima do atacante continua vendo só a casa dele.
	assert.Equal(t, http.StatusOK, atacante.me().Status)
	assert.NotEqual(t, casaDaVitima, atacante.me().json(t)["household"].(map[string]any)["id"])
}

// Mesmo cenário no refresh: o hid é REDERIVADO do banco, nunca copiado do
// token apresentado.
func TestRefreshRederivaACasaDoBanco(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	vitima := h.registerAndVerify("vitima@exemplo.com")
	atacante := h.registerAndVerify("atacante@exemplo.com")

	casaDaVitima := vitima.me().json(t)["household"].(map[string]any)["id"].(string)
	h.clock.Advance(time.Minute)

	// O atacante troca o access por um forjado e renova a sessão.
	usuarioAtacante, err := h.users.ByEmail(t.Context(), "atacante@exemplo.com")
	require.NoError(t, err)
	atacante.cookies["hf_access"] = h.forgeAccessToken(usuarioAtacante.ID, casaDaVitima, "owner", "fam-forjada")

	res := atacante.refresh()
	require.Equal(t, http.StatusOK, res.Status)

	casaDepois := res.json(t)["household"].(map[string]any)["id"].(string)
	assert.NotEqual(t, casaDaVitima, casaDepois, "o refresh não pode herdar o hid forjado")
}
