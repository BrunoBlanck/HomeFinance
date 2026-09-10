package auth_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Testes de regressão do ataque de PRE-HIJACKING de conta.
//
// Origem: os quatro PoCs da re-revisão de segurança (arquivo temporário
// zz_rev2_tmp_test.go), aqui convertidos em testes permanentes com as
// asserções INVERTIDAS — eles afirmam o comportamento seguro, e falham se a
// defesa for removida.
//
// A defesa que todos eles exercitam é uma só: o código de 6 dígitos vive
// dentro de uma TENTATIVA DE CADASTRO e só pode ser validado junto do
// registrationToken daquela tentativa. O código do atacante chega à caixa da
// vítima, mas é inútil na mão dela — ela não tem o token dele; e o atacante
// tem o token dele, mas nunca vê o código. Nenhum dos dois fecha o par.
//
// Ver internal/auth/registration.go para o porquê de nenhuma política de
// sobrescrita de credenciais resolver este ataque.

const senhaDoAtacante = "senha-escolhida-pelo-invasor-2026"

// ---------------------------------------------------------------------------
// PoC 1 — pre-hijack na ORDEM INVERSA (atacante primeiro)
// ---------------------------------------------------------------------------

// O atacante cadastra o e-mail da vítima com a SENHA DELE; o código legítimo
// vai para a caixa da VÍTIMA. A vítima então se cadastra normalmente, dentro
// da janela de cooldown, e usa o código que está na caixa dela.
//
// Antes da correção, esse código estava amarrado à senha do atacante e a
// vítima entregava a casa financeira dela ao usá-lo.
//
// O que este teste cobra agora, em qualquer ordem de uso dos códigos: a senha
// do atacante NUNCA entra, e a vítima consegue concluir com a senha dela.
func TestPreHijackNaOrdemInversaNaoEntregaAConta(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Minute})
	const vitima = "vitima@exemplo.com"

	// 1. O atacante cria a tentativa dele com a senha dele.
	atacante := h.client()
	require.Equal(t, http.StatusAccepted, atacante.register(vitima, senhaDoAtacante).Status)
	codigoDoAtacante, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok, "o código do atacante vai para a caixa da vítima")

	// 2. Segundos depois, a vítima se cadastra de verdade — DENTRO da janela
	//    de cooldown, que é exatamente o cenário que antes engolia o cadastro
	//    dela em silêncio.
	h.clock.Advance(10 * time.Second)
	dela := h.client()
	require.Equal(t, http.StatusAccepted, dela.register(vitima, senhaPadrao).Status)
	require.Len(t, dela.regToken, 64, "o cadastro da vítima precisa devolver o token dela")

	// 3. A vítima usa o código do ATACANTE (é o único que chegou na caixa
	//    dela até agora). Com o token dela, ele não vale nada.
	res := dela.verify(vitima, codigoDoAtacante.Code)
	assert.Equal(t, http.StatusUnauthorized, res.Status,
		"o código do atacante não pode valer com o token da vítima")
	assert.Equal(t, "INVALID_CODE", res.errorCode(t))

	// 4. E o atacante, que tem o token dele, não tem como fechar o par: o
	//    código dele foi para a caixa da vítima e ele nunca o viu. Prova
	//    direta: nem o código dele com o token dele adianta depois que a
	//    vítima conclui.
	h.clock.Advance(2 * time.Minute)
	require.Equal(t, http.StatusAccepted, dela.resend(vitima).Status)
	codigoDela, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	require.NotEqual(t, codigoDoAtacante.Code, codigoDela.Code)

	require.Equal(t, http.StatusOK, dela.verify(vitima, codigoDela.Code).Status,
		"a vítima precisa conseguir concluir o cadastro dela")

	// 5. A conta é DELA.
	invasor := h.client()
	assert.Equal(t, http.StatusUnauthorized, invasor.login(vitima, senhaDoAtacante).Status,
		"a senha do atacante entrou numa conta ativada pela vítima — pre-hijack")

	legitima := h.client()
	assert.Equal(t, http.StatusOK, legitima.login(vitima, senhaPadrao).Status,
		"a vítima precisa entrar com a senha que ELA escolheu")

	// 6. A tentativa do atacante morreu na confirmação.
	assert.Equal(t, http.StatusUnauthorized, atacante.verify(vitima, codigoDoAtacante.Code).Status,
		"confirmado o e-mail, nenhuma outra tentativa pode continuar de pé")
}

// ---------------------------------------------------------------------------
// Espelho da PoC 1 — vítima primeiro, atacante por ÚLTIMO, fora do cooldown
// ---------------------------------------------------------------------------

// A ordem que o teste anterior não cobria, e que derruba a defesa de
// "primeiro escreve vence": a vítima se cadastra, e o atacante cadastra
// depois, FORA do cooldown, de modo que o código DELE é o mais recente na
// caixa da vítima.
//
// Se o código mais recente ativasse credenciais que a vítima não escolheu, o
// ataque venceria só por ser o último. Aqui ele não vence: o que decide não é
// a ordem, é quem tem o token.
func TestPreHijackComAtacantePorUltimoNaoEntregaAConta(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Second})
	const vitima = "vitima@exemplo.com"

	// 1. A vítima se cadastra primeiro e recebe o código dela.
	dela := h.client()
	require.Equal(t, http.StatusAccepted, dela.register(vitima, senhaPadrao).Status)
	codigoDela, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)

	// 2. FORA do cooldown, o atacante cadastra o mesmo endereço com a senha
	//    dele. O código dele é o MAIS RECENTE na caixa da vítima.
	h.clock.Advance(2 * time.Second)
	atacante := h.client()
	require.Equal(t, http.StatusAccepted, atacante.register(vitima, senhaDoAtacante).Status)
	codigoDoAtacante, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	require.NotEqual(t, codigoDela.Code, codigoDoAtacante.Code)

	// 3. A vítima faz o que qualquer pessoa faria: usa o código mais recente
	//    da caixa dela. Ele não vale — não é a tentativa dela.
	res := dela.verify(vitima, codigoDoAtacante.Code)
	assert.Equal(t, http.StatusUnauthorized, res.Status,
		"o código mais recente não pode ativar credenciais que a vítima não escolheu")
	assert.Equal(t, "INVALID_CODE", res.errorCode(t))

	// 4. Ela usa o código DELA e conclui.
	require.Equal(t, http.StatusOK, dela.verify(vitima, codigoDela.Code).Status,
		"o código da própria tentativa da vítima precisa continuar valendo")

	// 5. A conta é dela.
	invasor := h.client()
	assert.Equal(t, http.StatusUnauthorized, invasor.login(vitima, senhaDoAtacante).Status,
		"o cadastro feito por último não pode virar dono da conta")

	legitima := h.client()
	assert.Equal(t, http.StatusOK, legitima.login(vitima, senhaPadrao).Status)

	// 6. O atacante ficou com um token cuja tentativa foi destruída.
	assert.Equal(t, http.StatusUnauthorized, atacante.verify(vitima, codigoDoAtacante.Code).Status)
}

// O laço do atacante não sustenta as credenciais dele: por mais que ele
// insista, cada cadastro da vítima nasce com o token e a senha DELA, e é ele
// que conclui.
func TestLacoDoAtacanteNaoSustentaAsCredenciaisDele(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Minute})
	const vitima = "vitima@exemplo.com"

	atacante := h.client()
	dela := h.client()

	require.Equal(t, http.StatusAccepted, atacante.register(vitima, senhaDoAtacante).Status)

	// "vítima tenta cadastrar / atacante reemite", em laço.
	for range 10 {
		h.clock.Advance(10 * time.Second)
		require.Equal(t, http.StatusAccepted, dela.register(vitima, senhaPadrao).Status)
		h.clock.Advance(10 * time.Second)
		require.Equal(t, http.StatusAccepted, atacante.resend(vitima).Status)
	}

	// Passado o cooldown, a vítima pede o código da tentativa DELA e conclui.
	h.clock.Advance(2 * time.Minute)
	require.Equal(t, http.StatusAccepted, dela.resend(vitima).Status)
	codigo, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, dela.verify(vitima, codigo.Code).Status)

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(vitima, senhaPadrao).Status)
	assert.Equal(t, http.StatusUnauthorized, novo.login(vitima, senhaDoAtacante).Status,
		"a senha do atacante sobreviveu a 10 cadastros da vítima")
}

// ---------------------------------------------------------------------------
// PoC 2 — negação PERMANENTE de cadastro
// ---------------------------------------------------------------------------

// A armadilha da defesa anterior: bloquear (ou engolir) o cadastro enquanto
// houver um pendente transformava a proteção contra o pre-hijack numa
// negação de serviço — qualquer um ocupava um endereço alheio para sempre.
//
// Com tentativas independentes isso deixa de existir: nenhum pedido destrói
// nem bloqueia o de ninguém. Este teste martela o endereço com cadastros de
// terceiros ANTES e DEPOIS do cadastro do dono, e cobra que o dono conclua.
func TestCadastroDeTerceiroNuncaNegaOCadastroDoDono(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Minute})
	const alvo = "dono@exemplo.com"

	atacante := h.client()
	for range 10 {
		h.clock.Advance(5 * time.Second)
		require.Equal(t, http.StatusAccepted, atacante.register(alvo, senhaDoAtacante).Status,
			"o cadastro nunca devolve 429/409 por endereço — seria lockout por procuração")
	}

	// O dono se cadastra no meio do bombardeio.
	h.clock.Advance(5 * time.Second)
	dono := h.client()
	require.Equal(t, http.StatusAccepted, dono.register(alvo, senhaPadrao).Status)
	require.Len(t, dono.regToken, 64)

	// E o bombardeio continua depois.
	for range 10 {
		h.clock.Advance(5 * time.Second)
		require.Equal(t, http.StatusAccepted, atacante.register(alvo, senhaDoAtacante).Status)
	}

	// Passado o cooldown, o dono pede o código DELE e conclui. Nenhum
	// cadastro de terceiro conseguiu impedir isso.
	h.clock.Advance(2 * time.Minute)
	require.Equal(t, http.StatusAccepted, dono.resend(alvo).Status)
	codigo, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)

	res := dono.verify(alvo, codigo.Code)
	require.Equal(t, http.StatusOK, res.Status,
		"cadastro de terceiro não pode negar o cadastro do dono do endereço: %s", res.Body)

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(alvo, senhaPadrao).Status)
	assert.Equal(t, http.StatusUnauthorized, novo.login(alvo, senhaDoAtacante).Status)
}

// ---------------------------------------------------------------------------
// ALTA-1 da revisão final — o reenvio SEM token não pode emitir nada, nunca
// ---------------------------------------------------------------------------

// Este teste já existiu com a asserção invertida: ele afirmava que quem
// perdeu o token se recuperava pelo reenvio quando o endereço não estava
// "disputado" (exatamente uma tentativa viva). A inferência era falsa — o
// pedido de reenvio carrega só um e-mail, que não prova nada e não traz
// credenciais — e o teste seguinte mostra como o atacante fabrica a condição
// sozinho.
//
// Regra em vigor: sem token, 202 idêntico e NADA emitido. Quem perdeu o token
// refaz o cadastro.
func TestReenvioSemTokenNaoEmiteNemEmEnderecoNaoDisputado(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Second})
	const email = "sozinho@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	codigoOriginal, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	enviadosAntes := h.mails.countOf(mailVerification)

	// A aba morreu: o token se perdeu junto. Endereço com UMA tentativa viva,
	// que era o caso que o caminho removido atendia.
	outraAba := h.client()
	h.clock.Advance(2 * time.Second)
	res := outraAba.resendSemToken(email)
	require.Equal(t, http.StatusAccepted, res.Status, "a resposta continua sendo o 202 do grupo B")
	require.Len(t, outraAba.regToken, 64, "o corpo continua trazendo um token bem formado")

	assert.Equal(t, enviadosAntes, h.mails.countOf(mailVerification),
		"o reenvio sem token não pode emitir código nenhum")

	// O token devolvido é decorativo: não casa com tentativa nenhuma, então
	// nem o código que já estava na caixa vale com ele.
	assert.Equal(t, http.StatusUnauthorized, outraAba.verify(email, codigoOriginal.Code).Status,
		"token devolvido sem emissão não pode abrir a tentativa de ninguém")

	// Quem tem o token de verdade continua concluindo normalmente.
	require.Equal(t, http.StatusOK, c.verify(email, codigoOriginal.Code).Status)

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(email, senhaPadrao).Status)
}

// A PoC que reprovou o caminho sem token, agora como regressão permanente.
//
// O atacante controla sozinho a condição "existe exatamente UMA tentativa
// viva, e ela é minha": basta esperar a tentativa da vítima EXPIRAR e
// registrar a dele em seguida. A vítima, que perdeu o token (aba fechada),
// pede reenvio; o caminho antigo criava uma sucessora com as credenciais DELE
// e entregava o token dessa sucessora para ELA. Ela digitava o código que
// chegou na caixa dela e ativava a conta com a senha do atacante.
func TestReenvioSemTokenNaoHerdaCredenciaisDeTentativaAlheia(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Second})
	const vitima = "vitima@exemplo.com"

	// 1. A vítima se cadastra e perde o token (a aba fechou).
	dela := h.client()
	require.Equal(t, http.StatusAccepted, dela.register(vitima, senhaPadrao).Status)

	// 2. Passado o TTL de 15 min, a tentativa DELA morre.
	h.clock.Advance(16 * time.Minute)

	// 3. O atacante registra o mesmo endereço com a senha DELE. Agora a única
	//    tentativa viva do endereço é a dele.
	atacante := h.client()
	require.Equal(t, http.StatusAccepted, atacante.register(vitima, senhaDoAtacante).Status)
	_, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok, "o código do atacante vai para a caixa da vítima")
	enviadosAntes := h.mails.countOf(mailVerification)

	// 4. A vítima pede reenvio SEM token — e não pode receber nada. Era aqui
	//    que o caminho removido criava a sucessora da tentativa DO ATACANTE e
	//    entregava o token dela para ELA.
	semToken := h.client()
	h.clock.Advance(2 * time.Second)
	res := semToken.resendSemToken(vitima)
	require.Equal(t, http.StatusAccepted, res.Status)
	require.Len(t, semToken.regToken, 64)
	assert.Equal(t, enviadosAntes, h.mails.countOf(mailVerification),
		"o reenvio sem token emitiu um código herdando credenciais alheias")

	// 5. Ela digita o código mais recente da caixa dela — o que qualquer
	//    pessoa faria. Com o token devolvido pelo reenvio, ele não vale nada.
	codigoNaCaixa, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	verificacao := semToken.verify(vitima, codigoNaCaixa.Code)
	assert.Equal(t, http.StatusUnauthorized, verificacao.Status,
		"o reenvio sem token entregou à vítima um código amarrado às credenciais do atacante")
	assert.Equal(t, "INVALID_CODE", verificacao.errorCode(t))

	// 6. Nenhuma conta foi ativada com a senha do atacante.
	invasor := h.client()
	assert.Equal(t, http.StatusUnauthorized, invasor.login(vitima, senhaDoAtacante).Status,
		"a senha do atacante entrou numa conta que a vítima ativou — pre-hijack pelo reenvio")

	// 7. E a recuperação de verdade continua aberta: refazer o cadastro.
	h.clock.Advance(2 * time.Second)
	require.Equal(t, http.StatusAccepted, dela.register(vitima, senhaPadrao).Status)
	codigoDela, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, dela.verify(vitima, codigoDela.Code).Status,
		"quem perdeu o token se recupera refazendo o cadastro")

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(vitima, senhaPadrao).Status)
	assert.Equal(t, http.StatusUnauthorized, novo.login(vitima, senhaDoAtacante).Status)
}

// Num endereço DISPUTADO o resultado é o mesmo — e sempre foi, mas agora pela
// regra geral e não por uma exceção. A recuperação segura continua sendo
// refazer o cadastro.
func TestReenvioSemTokenNaoEmiteEmEnderecoDisputado(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Second})
	const vitima = "disputado@exemplo.com"

	atacante := h.client()
	require.Equal(t, http.StatusAccepted, atacante.register(vitima, senhaDoAtacante).Status)

	h.clock.Advance(2 * time.Second)
	dela := h.client()
	require.Equal(t, http.StatusAccepted, dela.register(vitima, senhaPadrao).Status)

	enviadosAntes := h.mails.countOf(mailVerification)

	h.clock.Advance(2 * time.Second)
	semToken := h.client()
	res := semToken.resendSemToken(vitima)
	assert.Equal(t, http.StatusAccepted, res.Status, "a resposta continua sendo o 202 do grupo B")
	assert.Equal(t, enviadosAntes, h.mails.countOf(mailVerification),
		"em endereço disputado o reenvio sem token não pode emitir código nenhum")

	// E o caminho seguro continua aberto: refazer o cadastro.
	h.clock.Advance(2 * time.Second)
	require.Equal(t, http.StatusAccepted, dela.register(vitima, senhaPadrao).Status)
	codigo, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, dela.verify(vitima, codigo.Code).Status)

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(vitima, senhaPadrao).Status)
	assert.Equal(t, http.StatusUnauthorized, novo.login(vitima, senhaDoAtacante).Status)
}

// ---------------------------------------------------------------------------
// ALTA-2 da revisão final — código nunca enviado não é código
// ---------------------------------------------------------------------------

// A segurança dos 20 bits do código de 6 dígitos vem de "5 tentativas por
// código MAIS o teto de envio por endereço" (docs/SEGURANCA.md §1.1). Isso só
// vale enquanto a cota de mensagens limitar quantos códigos EXISTEM para
// chutar.
//
// Como /auth/register não tem teto por endereço (de propósito: teria seria
// lockout por procuração), cada cadastro criava um código novo e validável
// SEM gastar mensagem — 5 palpites por cadastro, 5 cadastros/h por IP,
// escalando com o número de IPs, e o prêmio era uma conta VERIFICADA num
// endereço que o atacante nunca leu.
//
// A regra em vigor: só é validável o código que foi EFETIVAMENTE EMITIDO.
func TestCodigoNuncaEnviadoPorEmailNaoAtivaConta(t *testing.T) {
	t.Parallel()

	// Cota de UMA mensagem por hora para deixar o cenário nítido: a primeira
	// sai, as seguintes não.
	h := newHarness(t, harnessOptions{
		VerificationMailLimiter: httpserver.NewLimiter(1, time.Hour, time.Hour),
		ResendInterval:          time.Second,
	})
	const alvo = "alvo@exemplo.com"

	// 1. O dono se cadastra: a única mensagem da cota vai para a caixa dele.
	dono := h.client()
	require.Equal(t, http.StatusAccepted, dono.register(alvo, senhaPadrao).Status)
	require.Equal(t, 1, h.mails.countOf(mailVerification))

	// 2. O atacante registra o mesmo endereço. A cota acabou: NENHUMA
	//    mensagem sai — mas a tentativa dele é gravada, com código.
	h.clock.Advance(2 * time.Second)
	atacante := h.client()
	require.Equal(t, http.StatusAccepted, atacante.register(alvo, senhaDoAtacante).Status)
	require.Len(t, atacante.regToken, 64)
	require.Equal(t, 1, h.mails.countOf(mailVerification),
		"a cota tinha de segurar a segunda mensagem")

	// 3. O código dessa tentativa nunca saiu por e-mail. Damos ao atacante o
	//    que 25 palpites/h/IP comprariam: o código certo na mão.
	h.injetarCodigoSemEmitir(t, alvo, atacante.regToken, "424242")

	// 4. Código que ninguém recebeu não ativa conta nenhuma.
	res := atacante.verify(alvo, "424242")
	assert.Equal(t, http.StatusUnauthorized, res.Status,
		"um código NUNCA enviado por e-mail verificou a conta: %s", res.Body)
	assert.Equal(t, "INVALID_CODE", res.errorCode(t))

	// 5. Nem repetindo: o código continua não existindo para a validação, e
	//    o orçamento de palpites não é sequer consumido.
	for range 10 {
		assert.Equal(t, http.StatusUnauthorized, atacante.verify(alvo, "424242").Status)
	}

	// 6. Nenhuma sessão, nenhuma conta verificada, nenhuma senha do atacante.
	invasor := h.client()
	assert.Equal(t, http.StatusUnauthorized, invasor.login(alvo, senhaDoAtacante).Status,
		"a conta foi ativada com a senha de quem nunca recebeu e-mail")

	// 7. E quem RECEBEU a mensagem conclui normalmente: nada legítimo quebra.
	codigo, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, dono.verify(alvo, codigo.Code).Status,
		"quem recebeu o código de verdade precisa continuar concluindo")

	legitimo := h.client()
	assert.Equal(t, http.StatusOK, legitimo.login(alvo, senhaPadrao).Status)
}

// A contrapartida do teste acima: quem ficou sem mensagem por causa do
// cooldown ou da cota se recupera pelo reenvio COM o token, que gasta uma
// mensagem e volta a ser contabilizado pela cota. Nada legítimo fica preso.
func TestTentativaSemEnvioViraValidavelPeloReenvioComToken(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: 10 * time.Minute})
	const alvo = "dono@exemplo.com"

	// Um terceiro gasta a janela de cooldown primeiro.
	terceiro := h.client()
	require.Equal(t, http.StatusAccepted, terceiro.register(alvo, senhaDoAtacante).Status)
	require.Equal(t, 1, h.mails.countOf(mailVerification))

	// O dono se cadastra DENTRO do cooldown: tentativa gravada, mensagem
	// segurada, código ainda não validável.
	h.clock.Advance(time.Second)
	dono := h.client()
	require.Equal(t, http.StatusAccepted, dono.register(alvo, senhaPadrao).Status)
	require.Equal(t, 1, h.mails.countOf(mailVerification))

	// Passado o cooldown, o reenvio COM o token dele emite de verdade.
	h.clock.Advance(11 * time.Minute)
	require.Equal(t, http.StatusAccepted, dono.resend(alvo).Status)
	require.Equal(t, 2, h.mails.countOf(mailVerification))

	codigo, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, dono.verify(alvo, codigo.Code).Status,
		"o reenvio com token precisa tornar a tentativa validável")

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(alvo, senhaPadrao).Status)
	assert.Equal(t, http.StatusUnauthorized, novo.login(alvo, senhaDoAtacante).Status)
}

// ---------------------------------------------------------------------------
// BAIXA-3 — a reemissão do login pertence a quem provou a senha
// ---------------------------------------------------------------------------

// A linha de users guarda o hash de quem cadastrou PRIMEIRO. Esse terceiro
// conhece essa senha e, com ela, o login não verificado dispara a reemissão,
// que rotacionava a ÚNICA tentativa viva — que pode ser a da vítima.
//
// Não era tomada de conta (a rotação preserva credenciais e o código novo vai
// para a mesma caixa), mas invalidava o código que a vítima tinha na mão e
// queimava a cota de mensagens dela. Agora só reemite quem provou a senha DA
// TENTATIVA.
func TestLoginNaoVerificadoNaoRotacionaTentativaDeOutraPessoa(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Second})
	const alvo = "vitima@exemplo.com"

	// 1. O atacante cadastra primeiro: é a senha DELE que fica na linha de
	//    users, que é a que o login confere.
	atacante := h.client()
	require.Equal(t, http.StatusAccepted, atacante.register(alvo, senhaDoAtacante).Status)

	// 2. A tentativa dele expira; a vítima se cadastra e recebe o código dela.
	h.clock.Advance(16 * time.Minute)
	dela := h.client()
	require.Equal(t, http.StatusAccepted, dela.register(alvo, senhaPadrao).Status)
	codigoDela, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	enviadosAntes := h.mails.countOf(mailVerification)

	// 3. O atacante entra com a senha DELE: 403, e nada pode ser reemitido —
	//    a única tentativa viva é a da vítima.
	h.clock.Advance(2 * time.Second)
	assert.Equal(t, http.StatusForbidden, atacante.login(alvo, senhaDoAtacante).Status)
	assert.Equal(t, enviadosAntes, h.mails.countOf(mailVerification),
		"o login de um terceiro rotacionou a tentativa da vítima e gastou a cota dela")

	// 4. O código que a vítima tem na mão continua valendo.
	require.Equal(t, http.StatusOK, dela.verify(alvo, codigoDela.Code).Status,
		"o login de um terceiro invalidou o código que a vítima tinha na mão")

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(alvo, senhaPadrao).Status)
	assert.Equal(t, http.StatusUnauthorized, novo.login(alvo, senhaDoAtacante).Status)
}

// ---------------------------------------------------------------------------
// PoC 3 — o reenvio ignorava a cota POR ENDEREÇO
// ---------------------------------------------------------------------------

// O reenvio só consultava o cooldown de 60 s, nunca a cota por endereço: 59
// pedidos espaçados de 61 s entregavam 59 mensagens em uma hora ao mesmo
// endereço. O teto tem de ser POR MENSAGEM DE VERIFICAÇÃO, com o mesmo
// escopo/chave do registro — um teto por endpoint seria contornável só
// trocando de rota.
func TestReenvioRespeitaACotaPorEndereco(t *testing.T) {
	t.Parallel()

	// Cota apertada (3/h) de propósito: o que está sob teste é o teto ser
	// respeitado pelo reenvio, e um teto baixo torna o furo visível em poucas
	// iterações. O padrão de produção é 10/h (config.DefaultRateLimits).
	h := newHarness(t, harnessOptions{
		VerificationMailLimiter: httpserver.NewLimiter(3, time.Hour, time.Hour),
		ResendInterval:          time.Minute,
	})
	const alvo = "alvo@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(alvo, senhaPadrao).Status)

	// 59 reenvios espaçados de 61 s: o cooldown libera TODOS eles.
	for range 59 {
		h.clock.Advance(61 * time.Second)
		require.Equal(t, http.StatusAccepted, c.resend(alvo).Status,
			"o reenvio nunca devolve 429 por endereço")
	}

	entregues := h.mails.countOf(mailVerification)
	t.Logf("mensagens de verificação entregues em 60 pedidos: %d", entregues)
	assert.LessOrEqual(t, entregues, 3,
		"o reenvio furou a cota por endereço: %d mensagens saíram para %s", entregues, alvo)
	assert.GreaterOrEqual(t, entregues, 1, "a cota não pode zerar o fluxo legítimo")
}

// A mesma cota vale para a reemissão disparada pelo LOGIN de conta não
// verificada: se ela tivesse balde próprio, bastaria alternar as rotas para
// multiplicar as mensagens.
func TestReemissaoDoLoginUsaOMesmoBaldeDoReenvio(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{
		VerificationMailLimiter: httpserver.NewLimiter(2, time.Hour, time.Hour),
		ResendInterval:          time.Minute,
	})
	const alvo = "alvo@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(alvo, senhaPadrao).Status)

	// Alterna reenvio e login não verificado, sempre fora do cooldown.
	for range 20 {
		h.clock.Advance(61 * time.Second)
		require.Equal(t, http.StatusAccepted, c.resend(alvo).Status)
		h.clock.Advance(61 * time.Second)
		require.Equal(t, http.StatusForbidden, c.login(alvo, senhaPadrao).Status)
	}

	entregues := h.mails.countOf(mailVerification)
	t.Logf("mensagens de verificação entregues alternando as rotas: %d", entregues)
	assert.LessOrEqual(t, entregues, 2,
		"alternar /resend-code e /login furou o teto por endereço: %d mensagens", entregues)
}

// ---------------------------------------------------------------------------
// PoC 4 — a cota de 24 h durava 1 h (causa-raiz no limitador)
// ---------------------------------------------------------------------------

// Ponta a ponta do achado corrigido em httpserver.NewLimiter: o aviso
// "alguém tentou criar conta com o seu e-mail" tem cota de 1 por dia, e ela
// precisa valer o dia inteiro mesmo com o IdleTTL do limitador configurado
// em 1 h — que é o padrão de produção (config.DefaultRateLimits).
func TestCotaDe24hDoAvisoDuraAs24hInteiras(t *testing.T) {
	t.Parallel()

	relogio := newTestClock()
	// IdleTTL MENOR que a janela, exatamente como em produção antes da
	// correção. NewLimiter tem de elevar o TTL efetivo para 24 h.
	limitador := httpserver.NewLimiter(1, 24*time.Hour, time.Hour).WithClock(relogio.Now)

	h := newHarness(t, harnessOptions{Clock: relogio, AccountExistsLimiter: limitador})
	const titular = "titular@exemplo.com"
	h.registerAndVerify(titular)
	h.mails.reset()

	c := h.client()
	// 23 tentativas espaçadas de 61 min: cada uma cai depois do IdleTTL de
	// 1 h, que era quando o balde renascia CHEIO.
	for range 23 {
		h.clock.Advance(61 * time.Minute)
		require.Equal(t, http.StatusAccepted, c.register(titular, senhaDoAtacante).Status)
	}

	avisos := h.mails.countOf(mailAccountExists)
	t.Logf("avisos entregues em 24 h: %d", avisos)
	assert.LessOrEqual(t, avisos, 1,
		"a cota de 24 h virou cota de 1 h: %d avisos saíram para o titular em um dia", avisos)
}

// ---------------------------------------------------------------------------
// Bordas do contrato do registrationToken
// ---------------------------------------------------------------------------

// Sem token não há o que validar; com token de forma inválida, também não.
// Nenhum dos dois pode virar caminho alternativo de confirmação.
func TestVerifyEmailExigeORegistrationToken(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	const email = "bruno@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	codigo, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)

	semCampo := c.do(http.MethodPost, "/api/v1/auth/verify-email", map[string]string{
		"email": email, "code": codigo.Code,
	})
	assert.Equal(t, http.StatusBadRequest, semCampo.Status, semCampo.Body)
	assert.Equal(t, "VALIDATION_FAILED", semCampo.errorCode(t))

	curto := c.verifyCom(email, codigo.Code, "abc123")
	assert.Equal(t, http.StatusBadRequest, curto.Status)

	// Token bem formado mas de ninguém: 401, igual a qualquer outra falha de
	// código (grupo D).
	inexistente := c.verifyCom(email, codigo.Code, "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	assert.Equal(t, http.StatusUnauthorized, inexistente.Status)
	assert.Equal(t, "INVALID_CODE", inexistente.errorCode(t))

	// E o par certo continua funcionando: as tentativas erradas acima não
	// podem ter queimado a tentativa legítima.
	assert.Equal(t, http.StatusOK, c.verify(email, codigo.Code).Status)
}

// O token nunca pode aparecer em log (docs/SEGURANCA.md §4): ele é o segredo
// que separa a tentativa da vítima da tentativa do atacante.
func TestRegistrationTokenNuncaVazaEmLog(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Second})
	const email = "bruno@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	token := c.regToken
	require.Len(t, token, 64)

	// Caminhos de erro, que são os que mais logam.
	c.verifyCom(email, "000000", token)
	c.verifyCom(email, "000000", "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	h.clock.Advance(2 * time.Second)
	c.resend(email)

	logs := h.logOutput()
	assert.NotContains(t, logs, token, "o registrationToken vazou no log estruturado")
	assert.NotContains(t, logs, auth.HashRegistrationToken(token),
		"nem o hash do token tem por que aparecer em log")
}

// ---------------------------------------------------------------------------
// BAIXA-1 da revisão final — tentativa sem envio não desliga a reemissão
// ---------------------------------------------------------------------------

// A reemissão do login não verificado só acontece quando o endereço tem
// EXATAMENTE UMA tentativa viva. Enquanto "viva" incluía tentativa cujo
// código nunca foi emitido, um terceiro desligava esse caminho de graça: um
// POST /auth/register a cada janela de cooldown mantinha uma segunda
// "tentativa viva" para sempre, e o dono do endereço — que tem a senha e o
// código na caixa — perdia o único jeito de pedir outro código sem o token.
//
// Tentativa sem emissão não valida nada para ninguém; agora ela também não
// conta como viva.
func TestTentativaSemEnvioNaoDesligaAReemissaoDoLogin(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: 10 * time.Minute})
	const alvo = "dono@exemplo.com"

	// 1. O dono se cadastra: código emitido e mensagem na caixa dele.
	dono := h.client()
	require.Equal(t, http.StatusAccepted, dono.register(alvo, senhaPadrao).Status)
	require.Equal(t, 1, h.mails.countOf(mailVerification))

	// 2. Um terceiro registra o mesmo endereço DENTRO do cooldown: a
	//    tentativa dele é gravada, mas nenhuma mensagem sai — o código dele
	//    nunca foi emitido.
	h.clock.Advance(time.Second)
	terceiro := h.client()
	require.Equal(t, http.StatusAccepted, terceiro.register(alvo, senhaDoAtacante).Status)
	require.Equal(t, 1, h.mails.countOf(mailVerification),
		"o cooldown tinha de segurar a segunda mensagem")

	// 3. Passado o cooldown, o dono entra com a senha DELE. A conta ainda não
	//    está confirmada: 403 e o código é reemitido, porque a única
	//    tentativa UTILIZÁVEL do endereço é a dele.
	h.clock.Advance(11 * time.Minute)
	res := dono.login(alvo, senhaPadrao)
	require.Equal(t, http.StatusForbidden, res.Status, res.Body)
	assert.Equal(t, "EMAIL_NOT_VERIFIED", res.errorCode(t))
	require.Equal(t, 2, h.mails.countOf(mailVerification),
		"a tentativa sem envio do terceiro desligou a reemissão do dono")

	// 4. E o código reemitido conclui o cadastro com a senha do DONO.
	codigo, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, dono.verify(alvo, codigo.Code).Status)

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(alvo, senhaPadrao).Status)
	assert.Equal(t, http.StatusUnauthorized, novo.login(alvo, senhaDoAtacante).Status,
		"a senha do terceiro não pode ativar nada")
}

// ---------------------------------------------------------------------------
// BAIXA-3 da revisão final — ressurreição por corrida
// ---------------------------------------------------------------------------

// usersComVerificacaoNaJanela devolve a leitura OBSOLETA de users e, logo
// depois dela, commita a verificação do e-mail — reproduzindo, sem depender
// de escalonamento, a janela entre o SELECT do ResendCode e a escrita do
// RotateCode.
type usersComVerificacaoNaJanela struct {
	user.Repository

	t      *testing.T
	agora  func() time.Time
	armado atomic.Bool
	tiros  atomic.Int32
}

func (u *usersComVerificacaoNaJanela) ByEmail(ctx context.Context, email string) (*user.User, error) {
	antes, err := u.Repository.ByEmail(ctx, email)
	if err != nil || !u.armado.Load() {
		return antes, err
	}
	if u.tiros.Add(1) != 1 {
		return antes, nil
	}

	// É AQUI que a verificação do e-mail commita: depois da leitura de quem
	// está reenviando, antes de qualquer escrita dele.
	ativou, err := u.Repository.ActivatePending(ctx, antes.ID, antes.Name, antes.PasswordHash, u.agora())
	require.NoError(u.t, err)
	require.True(u.t, ativou)

	// A cópia devolvida é a de ANTES: quem leu não enxerga a mudança.
	return antes, nil
}

// Um token antigo não pode RESSUSCITAR a tentativa que a verificação já
// matou.
//
// RotateCode ressuscita tentativa consumida de propósito (é o caminho de quem
// errou o código cinco vezes e pede outro), então a checagem de conta
// verificada não pode ficar só fora da transação: na janela entre o SELECT em
// users e o UPDATE, o VerifyEmail de outra requisição confirma a conta e
// consome todas as tentativas. O custo era um e-mail de código na caixa de
// quem acabou de confirmar o cadastro, mais uma linha viva órfã.
//
// A defesa é reconferir Verified() DENTRO da uow.Do, junto com a escrita.
func TestTokenAntigoNaoRessuscitaTentativaDepoisDaVerificacao(t *testing.T) {
	t.Parallel()

	relogio := newTestClock()
	espiao := &usersComVerificacaoNaJanela{t: t, agora: relogio.Now}

	h := newHarness(t, harnessOptions{
		ResendInterval: time.Second,
		Clock:          relogio,
		WrapUsers: func(r user.Repository) user.Repository {
			espiao.Repository = r
			return espiao
		},
	})
	const alvo = "corrida@exemplo.com"

	dono := h.client()
	require.Equal(t, http.StatusAccepted, dono.register(alvo, senhaPadrao).Status)
	require.Equal(t, 1, h.mails.countOf(mailVerification))
	token := dono.regToken
	require.Len(t, token, 64)

	// O VerifyEmail vencedor consome TODAS as tentativas do endereço na mesma
	// transação em que grava email_verified_at. Aqui a parte "consome" já
	// aconteceu; a parte "grava" acontece dentro da janela, no espião.
	_, err := h.attempts.ConsumeAllForEmail(t.Context(), alvo, h.clock.Now())
	require.NoError(t, err)

	h.clock.Advance(2 * time.Second)
	espiao.armado.Store(true)
	res := dono.resend(alvo)
	espiao.armado.Store(false)

	require.Equal(t, http.StatusAccepted, res.Status, "a resposta continua sendo o 202 do grupo B")
	// A janela precisa ter sido exercitada: se a leitura obsoleta não
	// acontecesse, o teste passaria sem provar nada. (Com a defesa em pé são
	// DUAS leituras — a de fora e a reconferência dentro da transação.)
	require.GreaterOrEqual(t, espiao.tiros.Load(), int32(1),
		"a janela da corrida não foi exercitada")

	// 1. Nenhuma mensagem nova na caixa de quem acabou de confirmar a conta.
	assert.Equal(t, 1, h.mails.countOf(mailVerification),
		"o reenvio emitiu código para uma conta já verificada")

	// 2. E nenhuma tentativa voltou à vida.
	_, err = h.attempts.LiveByToken(t.Context(), alvo, auth.HashRegistrationToken(token), h.clock.Now())
	assert.ErrorIs(t, err, auth.ErrNotFound,
		"o token antigo ressuscitou uma tentativa que a verificação já tinha matado")

	// 3. A conta verificada continua verificada — o rollback da rotação não
	//    pode desfazer a confirmação de ninguém.
	u, err := h.users.ByEmail(t.Context(), alvo)
	require.NoError(t, err)
	assert.True(t, u.Verified(), "a verificação do e-mail foi desfeita pela rotação abortada")
}
