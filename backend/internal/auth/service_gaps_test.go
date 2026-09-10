package auth_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/id"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// injetarCodigo grava um código de 6 dígitos ESCOLHIDO pelo teste para o par
// (e-mail, propósito), invalidando antes tudo o que houver.
//
// Serve ao fluxo de RECUPERAÇÃO DE SENHA, que continua guardando o código em
// verification_codes. O cadastro tem o equivalente próprio em
// harness.injetarCodigoNaTentativa, porque lá o código vive dentro da
// tentativa e é escopado pelo registrationToken.
func (h *harness) injetarCodigo(t *testing.T, email, purpose, code string) {
	t.Helper()

	now := h.clock.Now()
	_, err := h.codes.ConsumeAllActive(t.Context(), email, purpose, now)
	require.NoError(t, err)

	usuario, err := h.users.ByEmail(t.Context(), email)
	require.NoError(t, err)

	require.NoError(t, h.codes.Create(t.Context(), &auth.VerificationCode{
		ID:        id.New(),
		Email:     email,
		Purpose:   purpose,
		UserID:    &usuario.ID,
		CodeHash:  h.svc.OTP().Hash(purpose, email, code),
		ExpiresAt: now.Add(15 * time.Minute),
		CreatedAt: now,
		UpdatedAt: now,
	}))
}

func (c *client) resetar(email, code, novaSenha string) response {
	return c.do(http.MethodPost, "/api/v1/auth/reset-password", map[string]string{
		"email": email, "code": code, "newPassword": novaSenha,
	})
}

func (c *client) esquecer(email string) response {
	return c.do(http.MethodPost, "/api/v1/auth/forgot-password", map[string]string{"email": email})
}

// ---------------------------------------------------------------------------
// Critério 15 — grupo D COMPLETO no reset-password
// ---------------------------------------------------------------------------

// TestGrupoDNoResetPasswordCobreTodosOsCasos fecha a lacuna do teste
// existente, que só comparava três das sete situações da §3.12.
//
// A regressão que este teste pega: qualquer caminho de reset-password que
// passe a responder diferente para "expirado", "já consumido", "tentativas
// esgotadas" ou "código do outro propósito" — todos vazam a existência da
// conta e o estado do código.
func TestGrupoDNoResetPasswordCobreTodosOsCasos(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{OTPMaxAttempts: 2, ResendInterval: time.Second})
	c := h.client()
	const nova = "senha-nova-que-nao-sera-usada-2026"

	// (a) conta verificada com código de reset VIVO -> tentativa errada.
	comCodigo := "comcodigo@exemplo.com"
	h.registerAndVerify(comCodigo)
	h.clock.Advance(2 * time.Second)
	require.Equal(t, http.StatusAccepted, c.esquecer(comCodigo).Status)

	// (b) código de reset JÁ CONSUMIDO.
	consumido := "consumido@exemplo.com"
	h.registerAndVerify(consumido)
	h.clock.Advance(2 * time.Second)
	require.Equal(t, http.StatusAccepted, c.esquecer(consumido).Status)
	usado, ok := h.mails.lastOf(mailPasswordReset)
	require.True(t, ok)
	require.Equal(t, http.StatusNoContent, c.resetar(consumido, usado.Code, nova).Status)

	// (c) código de reset EXPIRADO.
	expirado := "expirado@exemplo.com"
	h.registerAndVerify(expirado)
	h.clock.Advance(2 * time.Second)
	require.Equal(t, http.StatusAccepted, c.esquecer(expirado).Status)
	velho, ok := h.mails.lastOf(mailPasswordReset)
	require.True(t, ok)

	// (d) TENTATIVAS ESGOTADAS (OTPMaxAttempts=2 neste arnês).
	esgotado := "esgotado@exemplo.com"
	h.registerAndVerify(esgotado)
	h.clock.Advance(2 * time.Second)
	require.Equal(t, http.StatusAccepted, c.esquecer(esgotado).Status)
	queimado, ok := h.mails.lastOf(mailPasswordReset)
	require.True(t, ok)
	for range 3 {
		c.resetar(esgotado, "000000", nova)
	}

	// (e) código do OUTRO propósito (email_verification) vivo.
	outroProposito := "outroproposito@exemplo.com"
	cOutro := h.client()
	require.Equal(t, http.StatusAccepted, cOutro.register(outroProposito, senhaPadrao).Status)
	codigoDeVerificacao, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)

	h.clock.Advance(20 * time.Minute) // mata o código de "expirado"

	casos := []caso{
		{nome: "código errado, conta com código vivo", res: c.resetar(comCodigo, "000000", nova)},
		{nome: "código expirado", res: c.resetar(expirado, velho.Code, nova)},
		{nome: "código já consumido", res: c.resetar(consumido, usado.Code, nova)},
		{nome: "tentativas esgotadas", res: c.resetar(esgotado, queimado.Code, nova)},
		{nome: "e-mail sem código de reset", res: c.resetar("semcodigo@exemplo.com", "123456", nova)},
		{nome: "e-mail inexistente", res: c.resetar("ninguem@exemplo.com", "123456", nova)},
		{nome: "código de email_verification", res: c.resetar(outroProposito, codigoDeVerificacao.Code, nova)},
	}
	for _, cs := range casos {
		assert.Equal(t, http.StatusUnauthorized, cs.res.Status, "caso %q", cs.nome)
		assert.Equal(t, "INVALID_CODE", cs.res.errorCode(t), "caso %q", cs.nome)
		assert.Empty(t, cs.res.Cookies, "caso %q: falha de código não emite cookie", cs.nome)
	}
	mesmaResposta(t, "grupo D (reset-password, completo)", casos...)

	// Nenhum dos e-mails acima teve a senha trocada por estas tentativas.
	for _, email := range []string{comCodigo, expirado, esgotado} {
		novo := h.client()
		assert.Equal(t, http.StatusUnauthorized, novo.login(email, nova).Status,
			"a senha de %s não podia ter sido trocada", email)
	}
}

// ---------------------------------------------------------------------------
// Critério 20 — esgotamento de tentativas
// ---------------------------------------------------------------------------

// O limite de 5 tentativas é o que impede a varredura dos 10^6 valores. O
// teste existente só cobria verify-email; reset-password tem o mesmo risco e
// consequência pior (troca de senha).
func TestTentativasEsgotamTambemNoResetPassword(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{OTPMaxAttempts: 5, ResendInterval: time.Second})
	const email = "bruno@exemplo.com"
	const nova = "girassol-vinagre-tapete-4417"

	h.registerAndVerify(email)
	h.clock.Advance(2 * time.Second)

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.esquecer(email).Status)
	mail, ok := h.mails.lastOf(mailPasswordReset)
	require.True(t, ok)

	errado := "000000"
	if mail.Code == errado {
		errado = "111111"
	}

	for i := range 5 {
		res := c.resetar(email, errado, nova)
		require.Equal(t, http.StatusUnauthorized, res.Status, "tentativa %d", i+1)
		require.Equal(t, "INVALID_CODE", res.errorCode(t))
	}

	res := c.resetar(email, mail.Code, nova)
	assert.Equal(t, http.StatusUnauthorized, res.Status,
		"o código certo na 6ª tentativa precisa falhar — o código já foi queimado")
	assert.Equal(t, "INVALID_CODE", res.errorCode(t))

	// E a senha continua a antiga.
	novo := h.client()
	assert.Equal(t, http.StatusUnauthorized, novo.login(email, nova).Status)
	assert.Equal(t, http.StatusOK, novo.login(email, senhaPadrao).Status)
}

// Borda exata do limite: com máximo 5, a QUINTA tentativa ainda precisa
// aceitar o código certo. Um `>` trocado por `>=` no lugar errado tiraria uma
// tentativa legítima do usuário sem ninguém perceber.
func TestQuintaTentativaAindaAceitaOCodigoCerto(t *testing.T) {
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
	for range 4 {
		require.Equal(t, http.StatusUnauthorized, c.verify(email, errado).Status)
	}

	res := c.verify(email, mail.Code)
	assert.Equal(t, http.StatusOK, res.Status,
		"a 5ª tentativa com o código certo ainda precisa valer (máximo = 5)")
}

// O contador de tentativas é POR CÓDIGO. Se ele fosse por conta, um atacante
// travaria a conta alheia para sempre com 5 chutes — e o usuário legítimo
// nunca mais conseguiria pedir um código utilizável.
func TestLimiteDeTentativasEhPorCodigoNaoPorConta(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{OTPMaxAttempts: 5, ResendInterval: time.Second})
	const email = "bruno@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)

	// Queima o primeiro código.
	for range 5 {
		require.Equal(t, http.StatusUnauthorized, c.verify(email, "000000").Status)
	}

	// O usuário legítimo pede outro e acerta de primeira.
	h.clock.Advance(2 * time.Second)
	require.Equal(t, http.StatusAccepted, c.resend(email).Status)
	novo, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)

	res := c.verify(email, novo.Code)
	assert.Equal(t, http.StatusOK, res.Status,
		"o contador de tentativas não pode atravessar de um código para o outro")
}

// ---------------------------------------------------------------------------
// docs/SEGURANCA.md §1.1 — o código é STRING de 6 caracteres
// ---------------------------------------------------------------------------

// Zeros à esquerda são o bug clássico deste fluxo: basta alguém guardar ou
// comparar o código como inteiro em qualquer ponto da cadeia para "000123"
// virar "123" e 10% dos códigos pararem de funcionar (ou, pior, colidirem).
//
// GenerateCode já é testado; o que faltava era o caminho de VALIDAÇÃO com um
// código de zeros à esquerda de ponta a ponta.
func TestCodigoComZerosAEsquerdaFunciona(t *testing.T) {
	t.Parallel()

	casos := []string{"000123", "000000", "012345", "100000"}
	for _, codigo := range casos {
		t.Run("codigo_"+codigo, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, harnessOptions{})
			const email = "bruno@exemplo.com"

			c := h.client()
			require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
			token := c.regToken
			h.injetarCodigoNaTentativa(t, email, token, codigo)

			// A variante sem os zeros NÃO pode ser aceita — e nem sequer
			// passa no formato de 6 dígitos.
			semZeros := semZerosAEsquerda(codigo)
			if semZeros != codigo && semZeros != "" {
				res := c.verify(email, semZeros)
				assert.NotEqual(t, http.StatusOK, res.Status,
					"%q não pode valer pelo código %q", semZeros, codigo)
			}

			res := c.verify(email, codigo)
			require.Equal(t, http.StatusOK, res.Status,
				"código %q com zeros à esquerda precisa verificar: %s", codigo, res.Body)
			assert.NotNil(t, res.cookie("hf_access"))

			// Uso único vale igual — mesmo apresentando o token certo.
			outro := h.client()
			assert.Equal(t, http.StatusUnauthorized, outro.verifyCom(email, codigo, token).Status)
		})
	}
}

// semZerosAEsquerda remove zeros à esquerda ("000123" -> "123").
func semZerosAEsquerda(s string) string {
	i := 0
	for i < len(s)-1 && s[i] == '0' {
		i++
	}
	return s[i:]
}

// O HMAC precisa distinguir "000123" de "123" mesmo com o mesmo par
// (purpose, email) — é a contraprova do teste acima no nível do OTP.
func TestHashDistingueZerosAEsquerda(t *testing.T) {
	t.Parallel()

	o, err := auth.NewOTP(pepperDeTeste)
	require.NoError(t, err)

	const email = "bruno@exemplo.com"
	h1 := o.Hash(auth.PurposeEmailVerification, email, "000123")
	h2 := o.Hash(auth.PurposeEmailVerification, email, "123")
	assert.NotEqual(t, h1, h2)

	assert.True(t, o.Verify(auth.PurposeEmailVerification, email, "000123", h1))
	assert.False(t, o.Verify(auth.PurposeEmailVerification, email, "123", h1),
		"código sem os zeros à esquerda não tem o formato válido nem o mesmo HMAC")
	assert.False(t, o.Verify(auth.PurposeEmailVerification, email, "0000123", h1))
	assert.True(t, auth.ValidCodeFormat("000000"))
	assert.True(t, auth.ValidCodeFormat("000123"))
}

// ---------------------------------------------------------------------------
// Critério 22 — emitir código novo invalida os anteriores (também no reset)
// ---------------------------------------------------------------------------

func TestCodigoNovoInvalidaOsAnterioresNoResetDeSenha(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: 30 * time.Second})
	const email = "bruno@exemplo.com"
	const nova = "girassol-vinagre-tapete-4417"

	h.registerAndVerify(email)
	c := h.client()

	h.clock.Advance(time.Minute)
	require.Equal(t, http.StatusAccepted, c.esquecer(email).Status)
	primeiro, ok := h.mails.lastOf(mailPasswordReset)
	require.True(t, ok)

	h.clock.Advance(time.Minute)
	require.Equal(t, http.StatusAccepted, c.esquecer(email).Status)
	segundo, ok := h.mails.lastOf(mailPasswordReset)
	require.True(t, ok)
	require.NotEqual(t, primeiro.Code, segundo.Code)

	assert.Equal(t, http.StatusUnauthorized, c.resetar(email, primeiro.Code, nova).Status,
		"o código anterior precisa morrer quando um novo é emitido")
	assert.Equal(t, http.StatusNoContent, c.resetar(email, segundo.Code, nova).Status)
}

// Trocar a senha também derruba os códigos de verificação de e-mail pendentes
// (ver ResetPassword). Sem isso, um código emitido antes do reset continuaria
// circulando depois dele.
func TestResetDeSenhaInvalidaCodigoDeVerificacaoPendente(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Second})
	const email = "bruno@exemplo.com"
	const nova = "girassol-vinagre-tapete-4417"

	// Uma tentativa de cadastro pendente PRECISA existir antes da troca de
	// senha. Criamos uma sobre um endereço ainda não verificado e só depois
	// verificamos a conta, para chegar ao reset com uma tentativa viva.
	cadastro := h.client()
	require.Equal(t, http.StatusAccepted, cadastro.register(email, senhaPadrao).Status)
	tokenDoCadastro := cadastro.regToken
	primeiro, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)

	// Uma SEGUNDA tentativa, de outra aba, fica pendente enquanto a primeira
	// conclui o cadastro... e é justamente ela que a troca de senha derruba.
	h.clock.Advance(2 * time.Second)
	outraAba := h.client()
	require.Equal(t, http.StatusAccepted, outraAba.register(email, senhaPadrao).Status)
	tokenPendente := outraAba.regToken
	h.injetarCodigoNaTentativa(t, email, tokenPendente, "654321")

	require.Equal(t, http.StatusOK, cadastro.verifyCom(email, primeiro.Code, tokenDoCadastro).Status)

	// A verificação já derruba as outras tentativas; reabrimos uma para
	// provar que o RESET também derruba.
	h.clock.Advance(2 * time.Second)
	_, err := h.attempts.RotateCode(t.Context(), attemptID(t, h, email, tokenPendente),
		h.svc.OTP().Hash(auth.PurposeEmailVerification, email, "654321"),
		h.clock.Now(), h.clock.Now().Add(15*time.Minute))
	require.NoError(t, err)

	c := h.client()
	h.clock.Advance(2 * time.Second)
	require.Equal(t, http.StatusAccepted, c.esquecer(email).Status)
	reset, ok := h.mails.lastOf(mailPasswordReset)
	require.True(t, ok)
	require.Equal(t, http.StatusNoContent, c.resetar(email, reset.Code, nova).Status)

	assert.Equal(t, http.StatusUnauthorized, c.verifyCom(email, "654321", tokenPendente).Status,
		"a tentativa de cadastro pendente precisa cair junto com a troca de senha")
}

// attemptID resolve o id da tentativa a partir do token.
func attemptID(t *testing.T, h *harness, email, token string) string {
	t.Helper()
	att, err := h.attempts.ByToken(t.Context(), email, auth.HashRegistrationToken(token))
	require.NoError(t, err)
	return att.ID
}

// ---------------------------------------------------------------------------
// Normalização de e-mail no fluxo inteiro
// ---------------------------------------------------------------------------

// Se o registro normalizasse o e-mail de um jeito e o login de outro, dava
// para criar DUAS contas com o "mesmo" endereço — e a segunda seria uma conta
// não verificada capaz de receber códigos do endereço real.
func TestEmailEhNormalizadoEmTodoOFluxo(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Second})
	const canonico = "bruno@exemplo.com"

	c := h.client()
	res := c.captureToken(c.do(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"name": nomePadrao, "email": "  Bruno@Exemplo.COM  ", "password": senhaPadrao,
	}))
	require.Equal(t, http.StatusAccepted, res.Status, res.Body)
	assert.Equal(t, canonico, res.json(t)["email"], "a resposta ecoa o e-mail já canônico")

	mail, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	assert.Equal(t, canonico, mail.To)

	// Verifica usando a grafia canônica o código emitido para a grafia com
	// maiúsculas: é o mesmo registro.
	require.Equal(t, http.StatusOK, c.verify(canonico, mail.Code).Status)

	// Login com qualquer grafia entra na MESMA conta.
	for _, grafia := range []string{canonico, "BRUNO@EXEMPLO.COM", " Bruno@Exemplo.Com "} {
		novo := h.client()
		res := novo.login(grafia, senhaPadrao)
		require.Equal(t, http.StatusOK, res.Status, "grafia %q não entrou: %s", grafia, res.Body)
		assert.Equal(t, canonico, res.json(t)["user"].(map[string]any)["email"])
	}

	// Registrar de novo com outra grafia NÃO cria segunda conta: cai no
	// caminho "conta verificada" (D3), que só avisa o titular.
	h.mails.reset()
	h.clock.Advance(2 * time.Second)
	require.Equal(t, http.StatusAccepted, c.register("BRUNO@EXEMPLO.COM", "senha-de-um-invasor-qualquer-2026").Status)
	assert.Equal(t, 1, h.mails.countOf(mailAccountExists))
	assert.Equal(t, 0, h.mails.countOf(mailVerification))

	novo := h.client()
	assert.Equal(t, http.StatusUnauthorized, novo.login(canonico, "senha-de-um-invasor-qualquer-2026").Status)
	assert.Equal(t, http.StatusOK, novo.login(canonico, senhaPadrao).Status)
}

// ---------------------------------------------------------------------------
// Risco 5 da §9 — corrida na rotação do refresh
// ---------------------------------------------------------------------------

// Duas (ou mais) requisições simultâneas com o MESMO refresh: NUNCA duas
// podem passar — se passassem, o token roubado seria tão válido quanto o
// legítimo.
//
// ASSERÇÃO CORRIGIDA (achado ALTA-1 da revisão de segurança). A versão
// anterior exigia "exatamente 1×200 e n−1×401", e essa exigência só era
// satisfazível PORQUE a falha existia: apresentar o mesmo refresh n vezes é,
// para o servidor, indistinguível de um roubo, e o tratamento de reúso
// derruba a família — inclusive o sucessor que a requisição vencedora
// acabou de criar. Deixar o vencedor sobreviver à revogação da própria
// família era exatamente o buraco relatado: o ladrão ficava logado.
//
// O invariante correto, e o que este teste passa a cobrar:
//
//	(1) no máximo UMA rotação vence (nunca duas sessões vivas);
//	(2) detectado o reúso, NADA da família continua utilizável — nem o
//	    token original, nem o sucessor devolvido a quem venceu a corrida.
func TestRefreshConcorrenteSoDeixaUmPassar(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.registerAndVerify("bruno@exemplo.com")
	compartilhado := c.cookies["hf_refresh"]
	require.NotEmpty(t, compartilhado)

	h.clock.Advance(time.Minute)

	const n = 6
	var wg sync.WaitGroup
	status := make([]int, n)
	corpos := make([]string, n)
	sucessores := make([]string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cli := h.client()
			cli.cookies["hf_refresh"] = compartilhado
			res := cli.refresh()
			status[i] = res.Status
			corpos[i] = res.Body
			sucessores[i] = cli.cookies["hf_refresh"]
		}()
	}
	wg.Wait()

	ok, negados := 0, 0
	for i, s := range status {
		switch s {
		case http.StatusOK:
			ok++
		case http.StatusUnauthorized:
			negados++
		default:
			t.Fatalf("requisição %d devolveu status inesperado %d: %s", i, s, corpos[i])
		}
	}
	require.Equal(t, n, ok+negados)
	assert.LessOrEqual(t, ok, 1, "duas rotações simultâneas jamais podem vencer (status=%v)", status)

	// (2) A família morreu. Nenhum sucessor eventualmente devolvido pode
	// continuar valendo: é ele que, antes da correção, mantinha viva a
	// sessão de quem venceu a corrida — na prática, a do ladrão.
	for i, tok := range sucessores {
		if tok == "" || tok == compartilhado {
			continue
		}
		cli := h.client()
		cli.cookies["hf_refresh"] = tok
		res := cli.refresh()
		assert.Equal(t, http.StatusUnauthorized, res.Status,
			"o sucessor da requisição %d sobreviveu à revogação da família: %s", i, res.Body)
	}

	// E o token original também está morto.
	cli := h.client()
	cli.cookies["hf_refresh"] = compartilhado
	assert.Equal(t, http.StatusUnauthorized, cli.refresh().Status)

	entradas, err := h.audit.ListByAction(t.Context(), audit.ActionRefreshReuseDetected, 10)
	require.NoError(t, err)
	assert.NotEmpty(t, entradas, "n requisições com o mesmo refresh são reúso e precisam ficar registradas")
}

// ---------------------------------------------------------------------------
// Bordas exatas da política de senha
// ---------------------------------------------------------------------------

func TestBordasExatasDaPoliticaDeSenha(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.client()

	casos := []struct {
		nome   string
		senha  string
		status int
	}{
		{"11 caracteres é curto demais", "Zx7qLmP2vNb", http.StatusBadRequest},
		{"12 caracteres exatos passam", "Zx7qLmP2vNbK", http.StatusAccepted},
		{"256 caracteres exatos passam", repetir("Kp7v", 64), http.StatusAccepted},
		{"257 caracteres estouram", repetir("Kp7v", 64) + "z", http.StatusBadRequest},
	}

	for i, cs := range casos {
		t.Run(cs.nome, func(t *testing.T) {
			email := emailNumerado(i)
			res := c.do(http.MethodPost, "/api/v1/auth/register", map[string]string{
				"name": "Ana", "email": email, "password": cs.senha,
			})
			require.Equal(t, cs.status, res.Status, "senha de %d caracteres: %s", len(cs.senha), res.Body)

			if cs.status == http.StatusBadRequest {
				fields := res.json(t)["error"].(map[string]any)["fields"].(map[string]any)
				assert.Contains(t, fields, "password")
				// A mensagem nunca ecoa a senha.
				assert.NotContains(t, res.Body, cs.senha)
			}
		})
	}
}

func repetir(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for range n {
		out = append(out, s...)
	}
	return string(out)
}

func emailNumerado(i int) string {
	return string(rune('a'+i)) + "borda@exemplo.com"
}

// ---------------------------------------------------------------------------
// §3.8 — logout revoga a FAMÍLIA, e só ela
// ---------------------------------------------------------------------------

// Sair no celular não pode derrubar o notebook: cada login abre uma família
// própria. O teste existente só provava que o token usado morre.
func TestLogoutDerrubaSoAFamiliaDaSessao(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	const email = "bruno@exemplo.com"
	h.registerAndVerify(email)

	celular := h.client()
	require.Equal(t, http.StatusOK, celular.login(email, senhaPadrao).Status)
	notebook := h.client()
	require.Equal(t, http.StatusOK, notebook.login(email, senhaPadrao).Status)

	// O celular ainda rotaciona uma vez: a família passa a ter dois elos.
	h.clock.Advance(time.Minute)
	require.Equal(t, http.StatusOK, celular.refresh().Status)

	require.Equal(t, http.StatusNoContent, celular.logout().Status)

	// A família inteira do celular caiu.
	assert.Equal(t, http.StatusUnauthorized, celular.refresh().Status)

	// O notebook continua vivo.
	assert.Equal(t, http.StatusOK, notebook.refresh().Status,
		"logout de uma sessão não pode derrubar as outras")
	assert.Equal(t, http.StatusOK, notebook.me().Status)
}

// ---------------------------------------------------------------------------
// Critério 27 — limite por IP no login
// ---------------------------------------------------------------------------

// O teste existente cobre o limitador POR CONTA. O limitador POR IP é o que
// segura a varredura de e-mails: precisa devolver 429 com Retry-After e um
// corpo que não diga nada sobre a conta.
func TestLimitePorIPDoLoginResponde429SemVazarConta(t *testing.T) {
	t.Parallel()

	limiter := httpserver.NewLimiter(3, time.Minute, time.Hour)
	h := newHarness(t, harnessOptions{
		Middlewares: []httpserver.Middleware{
			httpserver.RateLimit(limiter, httpserver.IPKey(0)),
		},
	})
	const email = "bruno@exemplo.com"
	h.registerAndVerify(email)

	c := h.client()
	// Gasta a cota com e-mails que não existem.
	for range 3 {
		c.login("ninguem@exemplo.com", senhaPadrao)
	}

	inexistente := c.login("outro-que-nao-existe@exemplo.com", senhaPadrao)
	require.Equal(t, http.StatusTooManyRequests, inexistente.Status)
	assert.Equal(t, "RATE_LIMITED", inexistente.errorCode(t))
	assert.NotEmpty(t, inexistente.Header.Get("Retry-After"))

	// A conta que EXISTE, do mesmo IP, recebe resposta idêntica.
	existente := c.login(email, senhaPadrao)
	assert.Equal(t, http.StatusTooManyRequests, existente.Status)
	assert.Equal(t, inexistente.Body, existente.Body,
		"o 429 por IP não pode diferenciar conta existente de inexistente")
	assert.Equal(t, inexistente.Header.Get("Retry-After"), existente.Header.Get("Retry-After"))
	assert.Empty(t, existente.Cookies, "requisição barrada não emite sessão")
}
