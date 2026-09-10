package auth_test

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/id"
	"github.com/brunorblanck/homefinance/backend/internal/platform/config"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/mailer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Testes de REGRESSÃO dos três achados que reprovaram a entrega.
//
// Regra que cada um destes testes precisa satisfazer: falhar com o código
// vulnerável e passar com o corrigido. Teste que passa nos dois estados não
// está testando a falha.

// ---------------------------------------------------------------------------
// ALTA-1 — o sucessor da rotação não pode sobreviver à revogação da família
// ---------------------------------------------------------------------------

// ganchoRotacao envolve o repositório de refresh para intercalar uma ação no
// ponto EXATO em que a corrida acontece.
//
// Sem ele o teste dependeria do escalonador — e o arnês usa SQLite com
// MaxOpenConns=1, que serializa tudo e mascararia justamente esta janela. O
// gancho dispara DEPOIS de Rotate ter revogado o antecessor e ANTES de o
// sucessor ser inserido, que é o intervalo descrito no achado.
type ganchoRotacao struct {
	auth.RefreshTokenRepository
	once sync.Once
	apos func()
}

func (g *ganchoRotacao) Rotate(ctx context.Context, id, replacedBy string, at time.Time) (bool, error) {
	rotacionou, err := g.RefreshTokenRepository.Rotate(ctx, id, replacedBy, at)
	if rotacionou && err == nil && g.apos != nil {
		g.once.Do(g.apos)
	}
	return rotacionou, err
}

// A ordem forçada é: rotação do antecessor -> REVOGAÇÃO DA FAMÍLIA (o ladrão
// replica o token antigo) -> inserção do sucessor.
//
// Antes da correção, o UPDATE "WHERE family_id = ? AND revoked_at IS NULL"
// da revogação não encontrava o sucessor, porque ele ainda não existia: o
// elo nascia limpo, e a sessão sobrevivia à detecção do próprio roubo. O
// audit_log dizia que o roubo tinha sido contido enquanto o ladrão seguia
// logado e a vítima, deslogada.
func TestSucessorDaRotacaoNaoSobreviveARevogacaoDaFamilia(t *testing.T) {
	t.Parallel()

	var (
		svc          *auth.Service
		roubado      string
		erroDoLadrao error
		disparou     bool
	)

	h := newHarness(t, harnessOptions{
		WrapTokens: func(inner auth.RefreshTokenRepository) auth.RefreshTokenRepository {
			return &ganchoRotacao{
				RefreshTokenRepository: inner,
				apos: func() {
					disparou = true
					// O ladrão apresenta o token que a vítima acabou de
					// rotacionar. Isso é reúso: derruba a família.
					_, erroDoLadrao = svc.Refresh(context.Background(), roubado, "198.51.100.7")
				},
			}
		},
	})
	svc = h.svc

	c := h.registerAndVerify("bruno@exemplo.com")
	roubado = c.cookies["hf_refresh"]
	require.NotEmpty(t, roubado)

	familia := familiaDoRefresh(t, h, roubado)

	h.clock.Advance(time.Minute)

	// A vítima rotaciona; o gancho intercala o reúso no meio da rotação.
	res := c.refresh()

	require.True(t, disparou, "o gancho precisa ter rodado — senão o teste não exercita a janela")
	assert.ErrorIs(t, erroDoLadrao, auth.ErrInvalidSession, "o reúso em si tem de ser recusado")

	// A família morreu no meio da rotação: a sessão não pode renascer.
	assert.Equal(t, http.StatusUnauthorized, res.Status,
		"a rotação não pode entregar sessão nova depois de a família ser revogada: %s", res.Body)
	if res.Status == http.StatusUnauthorized {
		assert.Equal(t, "INVALID_SESSION", res.errorCode(t))
	}

	// A prova central: NENHUM token da família continua utilizável — nem um
	// sucessor que porventura tenha sido entregue.
	if sucessor := c.cookies["hf_refresh"]; sucessor != "" && sucessor != roubado {
		cli := h.client()
		cli.cookies["hf_refresh"] = sucessor
		assert.Equal(t, http.StatusUnauthorized, cli.refresh().Status,
			"o sucessor escapou da revogação da família — é a sessão do ladrão sobrevivendo")
	}

	// E o estado persistido diz a mesma coisa.
	fam, err := h.tokens.FamilyByID(t.Context(), familia)
	require.NoError(t, err)
	assert.NotNil(t, fam.RevokedAt, "a família precisa ficar marcada como revogada")

	entradas, err := h.audit.ListByAction(t.Context(), audit.ActionRefreshReuseDetected, 5)
	require.NoError(t, err)
	assert.NotEmpty(t, entradas, "o reúso precisa ficar no audit_log")
}

// O MESMO defeito, isolado do tempo: um elo LIMPO dentro de uma família já
// revogada não pode valer.
//
// Este é exatamente o estado que a falha produzia — o sucessor era inserido
// depois da revogação, então nascia com revoked_at NULL e escapava do UPDATE
// por família. Aqui esse estado é construído à mão pelo repositório, sem
// depender de nenhuma corrida: se o Refresh não consultar o estado da
// FAMÍLIA, ele aceita o token e devolve 200.
func TestEloLimpoDentroDeFamiliaRevogadaNaoVale(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.registerAndVerify("bruno@exemplo.com")

	original := c.cookies["hf_refresh"]
	familia := familiaDoRefresh(t, h, original)
	dono, err := h.tokens.ByHash(t.Context(), auth.HashRefreshToken(original))
	require.NoError(t, err)

	agora := h.clock.Now()

	// A família cai (reúso detectado, logout, troca de senha…).
	_, err = h.tokens.RevokeFamily(t.Context(), familia, agora)
	require.NoError(t, err)

	// E só DEPOIS o elo retardatário é gravado — limpo, como o sucessor de
	// uma rotação que estava em voo.
	const retardatario = "sucessor-que-escapou-da-revogacao"
	require.NoError(t, h.tokens.Create(t.Context(), &auth.RefreshToken{
		ID:        id.New(),
		UserID:    dono.UserID,
		FamilyID:  familia,
		TokenHash: auth.HashRefreshToken(retardatario),
		ExpiresAt: agora.Add(14 * 24 * time.Hour),
		CreatedAt: agora,
		UpdatedAt: agora,
	}))

	vivo, err := h.tokens.ByHash(t.Context(), auth.HashRefreshToken(retardatario))
	require.NoError(t, err)
	require.Nil(t, vivo.RevokedAt, "o elo precisa estar limpo para o teste valer a pena")

	cli := h.client()
	cli.cookies["hf_refresh"] = retardatario
	res := cli.refresh()
	assert.Equal(t, http.StatusUnauthorized, res.Status,
		"elo limpo de família morta continuou valendo — é a sessão do ladrão: %s", res.Body)
	if res.Status == http.StatusUnauthorized {
		assert.Equal(t, "INVALID_SESSION", res.errorCode(t))
	}

	// E a varredura derruba o retardatário, para ele não ficar pendurado.
	depois, err := h.tokens.ByHash(t.Context(), auth.HashRefreshToken(retardatario))
	require.NoError(t, err)
	assert.NotNil(t, depois.RevokedAt, "o elo retardatário precisa ser varrido")
}

// familiaDoRefresh descobre o family_id de um refresh em claro.
func familiaDoRefresh(t *testing.T, h *harness, raw string) string {
	t.Helper()

	tok, err := h.tokens.ByHash(t.Context(), auth.HashRefreshToken(raw))
	require.NoError(t, err)
	return tok.FamilyID
}

// O expurgo de famílias mortas NÃO pode derrubar sessão viva.
//
// Com a checagem positiva, apagar a família cedo demais equivaleria a
// deslogar todo mundo: o Refresh falha fechado quando a família não existe.
// A ordem do PurgeExpired (tokens primeiro, e só então as famílias SEM
// nenhum token) é o que impede isso.
func TestExpurgoNaoDerrubaSessaoViva(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{})
	c := h.registerAndVerify("bruno@exemplo.com")

	// Passa do TTL do código (15 min), mas nem perto do TTL do refresh.
	h.clock.Advance(2 * time.Hour)

	codigos, _, err := h.svc.PurgeExpired(t.Context())
	require.NoError(t, err)
	require.GreaterOrEqual(t, codigos, int64(1), "o código consumido tinha de ser expurgado")

	res := c.refresh()
	assert.Equal(t, http.StatusOK, res.Status,
		"o expurgo apagou a família de uma sessão ainda válida: %s", res.Body)
	assert.Equal(t, http.StatusOK, c.me().Status)
}

// ---------------------------------------------------------------------------
// ALTA-2 — /auth/register não entrega texto do atacante na caixa da vítima
// ---------------------------------------------------------------------------

// capturaEnvio implementa mailer.Mailer guardando as mensagens.
type capturaEnvio struct {
	mu    sync.Mutex
	msgs  []mailer.Message
	nomes []string
}

func (c *capturaEnvio) Name() string { return "captura" }

func (c *capturaEnvio) Send(_ context.Context, m mailer.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, m)
	return nil
}

func (c *capturaEnvio) todas() []mailer.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]mailer.Message, len(c.msgs))
	copy(out, c.msgs)
	return out
}

// O texto que o atacante manda no campo "name" do cadastro.
const iscaDeGolpe = "AVISO: sua conta sera bloqueada, ligue 0800-000-0000"

// Este teste roda com o Notifier REAL, não com o mailer de mentira: o que
// está sob julgamento é o conteúdo que sairia pelo SMTP, com o SPF/DKIM/DMARC
// do domínio de verdade.
//
// Antes da correção, o "name" do corpo de POST /auth/register virava o ToName
// do cabeçalho e a saudação do corpo, e a mensagem era entregue ao endereço
// que o atacante escolhesse — sem autenticação e sem prova de posse. Era uma
// primitiva de phishing usando a reputação do produto.
func TestRegistroNaoEntregaTextoDoAtacanteNaCaixaDaVitima(t *testing.T) {
	t.Parallel()

	spy := &capturaEnvio{}
	fila := mailer.NewQueue(spy, logging.Discard(), mailer.QueueOptions{Size: 8, Workers: 1})
	notificador := mailer.NewNotifier(fila, "nao-responda@homefinance.test", "HomeFinance")

	h := newHarness(t, harnessOptions{Mailer: notificador})

	const vitima = "vitima@banco.test"
	res := h.client().do(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"name":     iscaDeGolpe,
		"email":    vitima,
		"password": senhaPadrao,
	})
	require.Equal(t, http.StatusAccepted, res.Status, res.Body)

	require.NoError(t, fila.Close(t.Context()))

	msgs := spy.todas()
	require.Len(t, msgs, 1, "o cadastro precisa ter enfileirado exatamente a mensagem de código")

	m := msgs[0]
	assert.Equal(t, vitima, m.To)
	assert.Empty(t, m.ToName, "nome não verificado nunca pode ir para o cabeçalho To")
	assert.NotContains(t, m.Body, iscaDeGolpe, "o corpo carregou texto escolhido pelo atacante")
	assert.NotContains(t, m.Subject, iscaDeGolpe)
	assert.Contains(t, m.Body, "Olá,\n", "a saudação é neutra até o endereço estar verificado")

	// A prova final é sobre os BYTES que sairiam no SMTP: cabeçalho e corpo.
	bruto, err := m.Build(h.clock.Now(), "teste")
	require.NoError(t, err)
	assert.NotContains(t, string(bruto), iscaDeGolpe,
		"a mensagem entregue não pode conter nada escrito por quem pediu o cadastro")

	// Contraprova: o pedaço legítimo continua lá.
	assert.Contains(t, string(bruto), "Nunca compartilhe este código")
}

// O mesmo vale para o código reemitido no login de conta não verificada e no
// reenvio: são todos primeiro contato.
func TestReenvioDeCodigoTambemNaoLevaNomeNaoVerificado(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Second})
	const vitima = "vitima@banco.test"

	// captureToken porque o corpo do registro é montado à mão (o nome é a
	// isca do teste) e o reenvio EXIGE o registrationToken desde a correção
	// do achado ALTA-1: sem guardar o token, o reenvio não emitiria nada.
	c := h.client()
	require.Equal(t, http.StatusAccepted, c.captureToken(c.do(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"name": iscaDeGolpe, "email": vitima, "password": senhaPadrao,
	})).Status)
	require.Len(t, c.regToken, 64)

	h.clock.Advance(2 * time.Second)
	require.Equal(t, http.StatusAccepted, c.resend(vitima).Status)

	for _, m := range h.mails.all() {
		assert.Equal(t, mailVerification, m.Kind)
		assert.Empty(t, m.Name, "nenhuma mensagem de primeiro contato leva nome")
	}
	assert.Equal(t, 2, h.mails.countOf(mailVerification))
}

// ---------------------------------------------------------------------------
// MÉDIA-3 — /auth/register respeita cooldown e tem limite por conta
// ---------------------------------------------------------------------------

// Repetição do MESMO cadastro (mesma senha) é o vetor de mail bombing: o
// cooldown tem de segurar o envio sem queimar o código que já está na caixa,
// porque esse código já corresponde exatamente a estas credenciais.
func TestRegistroRepetidoIdenticoNaoQueimaOCodigoNemEnviaDeNovo(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Minute})
	const vitima = "vitima@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(vitima, senhaPadrao).Status)
	tokenDaVitima := c.regToken

	codigoDaVitima, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)

	// Martelada de repetições idênticas dentro do cooldown.
	for range 5 {
		require.Equal(t, http.StatusAccepted, c.register(vitima, senhaPadrao).Status,
			"a resposta continua sendo o 202 do grupo A")
	}

	assert.Equal(t, 1, h.mails.countOf(mailVerification),
		"o cooldown precisa segurar o reenvio — senão o registro é amplificador de e-mail")

	// O par (token, código) da PRIMEIRA tentativa continua íntegro. Com as
	// tentativas independentes isto vale sempre, e não só dentro da janela:
	// nenhum pedido de cadastro destrói o de ninguém.
	res := c.verifyCom(vitima, codigoDaVitima.Code, tokenDaVitima)
	require.Equal(t, http.StatusOK, res.Status,
		"repetir o cadastro não pode queimar o par token+código já entregue (%s)", res.Body)

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(vitima, senhaPadrao).Status)
}

// A contrapartida do teste acima, reescrita para o modelo de TENTATIVAS.
//
// Antes, a defesa contra o pre-hijack era matar o código anterior quando
// chegava um cadastro com credenciais diferentes — uma escolha entre dois
// males, que trocava tomada de conta por negação de cadastro. Com o
// registrationToken não há mais escolha a fazer: as duas tentativas convivem,
// cada uma com o seu código e o seu token, e NENHUMA delas ativa credenciais
// que o dono do token não escolheu.
//
// O que este teste cobra:
//   - o código de uma tentativa NÃO vale com o token da outra;
//   - quem conclui, conclui com as credenciais DA PRÓPRIA tentativa;
//   - concluir destrói a tentativa alheia.
func TestCadaTentativaSoAtivaAsCredenciaisDela(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Second})
	const alvo = "alvo@exemplo.com"
	const senhaDoInvasor = "senha-escolhida-pelo-invasor-2026"

	primeira := h.client()
	require.Equal(t, http.StatusAccepted, primeira.register(alvo, senhaPadrao).Status)
	codigoDaPrimeira, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)

	h.clock.Advance(2 * time.Second)
	segunda := h.client()
	require.Equal(t, http.StatusAccepted, segunda.register(alvo, senhaDoInvasor).Status)
	codigoDaSegunda, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	require.NotEqual(t, codigoDaPrimeira.Code, codigoDaSegunda.Code)

	// Cruzar código e token não funciona em nenhuma direção.
	res := primeira.verify(alvo, codigoDaSegunda.Code)
	assert.Equal(t, http.StatusUnauthorized, res.Status,
		"código de OUTRA tentativa não pode valer com este token")
	assert.Equal(t, "INVALID_CODE", res.errorCode(t))

	res = segunda.verify(alvo, codigoDaPrimeira.Code)
	assert.Equal(t, http.StatusUnauthorized, res.Status,
		"código de OUTRA tentativa não pode valer com este token")

	// A primeira conclui com o par dela e ativa a senha dela.
	require.Equal(t, http.StatusOK, primeira.verify(alvo, codigoDaPrimeira.Code).Status)

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(alvo, senhaPadrao).Status)
	assert.Equal(t, http.StatusUnauthorized, novo.login(alvo, senhaDoInvasor).Status,
		"a senha da tentativa perdedora não pode ter sido ativada")

	// E a tentativa alheia morreu junto com a confirmação.
	assert.Equal(t, http.StatusUnauthorized, segunda.verify(alvo, codigoDaSegunda.Code).Status,
		"confirmado o e-mail, nenhuma outra tentativa pode continuar de pé")
}

// Passado o cooldown, o caminho de D3 volta a funcionar como especificado.
func TestRegistroSobreContaPendenteVoltaDepoisDoCooldown(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Minute})
	const email = "pendente@exemplo.com"

	c := h.client()
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	require.Equal(t, 1, h.mails.countOf(mailVerification))

	h.clock.Advance(2 * time.Minute)
	require.Equal(t, http.StatusAccepted, c.register(email, senhaPadrao).Status)
	assert.Equal(t, 2, h.mails.countOf(mailVerification))
}

// O aviso "alguém tentou criar conta com o seu e-mail" não tinha janela
// nenhuma: era mail bombing de qualquer endereço cadastrado. O freio é uma
// cota PRÓPRIA por endereço, que segura a MENSAGEM — a requisição continua
// respondendo 202.
func TestAvisoDeContaExistenteTemCotaPropriaPorEndereco(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{
		AccountExistsLimiter: httpserver.NewLimiter(1, 24*time.Hour, 24*time.Hour),
	})

	const titular = "titular@exemplo.com"
	h.registerAndVerify(titular)
	h.mails.reset()

	c := h.client()
	for range 6 {
		res := c.register(titular, "senha-de-um-invasor-2026")
		require.Equal(t, http.StatusAccepted, res.Status,
			"o registro nunca pode devolver 429 por endereço: seria lockout por procuração")
	}

	assert.Equal(t, 1, h.mails.countOf(mailAccountExists),
		"a cota precisa segurar o aviso — senão o registro bombardeia a caixa do titular")
}

// A cota por endereço NÃO pode virar 429: quem informa o e-mail no cadastro
// não prova posse dele, então recusar a requisição deixaria um terceiro
// trancar o cadastro de um endereço alheio (lockout por procuração).
//
// No modelo de tentativas a garantia fica mais forte: mesmo com a cota de
// ENVIO esgotada por um terceiro, o cadastro do dono é GRAVADO e ele sai com
// um token amarrado às credenciais DELE. Só a mensagem espera a janela — e o
// reenvio com o token dele entrega o código assim que ela abre.
func TestTerceiroNaoTrancaOCadastroDeUmEnderecoAlheio(t *testing.T) {
	t.Parallel()

	// Cota de envio mínima: o atacante a esgota na primeira tentativa. O
	// limitador anda pelo MESMO relógio do arnês, senão avançar o tempo do
	// teste não recomporia a cota.
	relogio := newTestClock()
	h := newHarness(t, harnessOptions{
		Clock:                   relogio,
		VerificationMailLimiter: httpserver.NewLimiter(1, time.Hour, time.Hour).WithClock(relogio.Now),
		ResendInterval:          time.Second,
	})

	const alvo = "alvo@exemplo.com"
	const senhaDoAtacante = "senha-escolhida-pelo-invasor-2026"

	atacante := h.client()
	for range 5 {
		h.clock.Advance(2 * time.Second)
		require.Equal(t, http.StatusAccepted, atacante.register(alvo, senhaDoAtacante).Status)
	}
	require.Equal(t, 1, h.mails.countOf(mailVerification),
		"a cota por endereço tem de segurar o envio a partir da segunda mensagem")

	// O dono do endereço ainda consegue se cadastrar — sem 429 — e sai com o
	// token dele.
	h.clock.Advance(2 * time.Second)
	dono := h.client()
	res := dono.register(alvo, senhaPadrao)
	require.Equal(t, http.StatusAccepted, res.Status,
		"o dono do endereço não pode ser trancado pela cota que um terceiro gastou")
	require.Len(t, dono.regToken, 64, "o cadastro do dono precisa devolver um token utilizável")

	// A tentativa dele está gravada, com as credenciais dele.
	att, err := h.attempts.ByToken(t.Context(), alvo, auth.HashRegistrationToken(dono.regToken))
	require.NoError(t, err, "o cadastro do dono precisa ter sido gravado")

	hasher, err := auth.NewPasswordHasher(auth.Argon2Params{MaxConcurrent: 2})
	require.NoError(t, err)
	assert.NoError(t, hasher.Verify(t.Context(), senhaPadrao, att.PasswordHash),
		"a tentativa do dono carrega a senha DELE")
	assert.Error(t, hasher.Verify(t.Context(), senhaDoAtacante, att.PasswordHash))

	// Passada a janela da cota, o reenvio COM O TOKEN DELE entrega o código e
	// ele conclui o cadastro com as próprias credenciais.
	h.clock.Advance(2 * time.Hour)
	require.Equal(t, http.StatusAccepted, dono.resend(alvo).Status)

	codigo, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, dono.verify(alvo, codigo.Code).Status,
		"a cota não pode impedir o dono de concluir o cadastro para sempre")

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(alvo, senhaPadrao).Status)
	assert.Equal(t, http.StatusUnauthorized, novo.login(alvo, senhaDoAtacante).Status)
}

// O corpo do 202 de registro continua idêntico nos três caminhos do grupo A
// mesmo com o cooldown ativo — inclusive no caminho novo "dentro da janela".
func TestGrupoARegistroContinuaIdenticoComCooldown(t *testing.T) {
	t.Parallel()

	h := newHarness(t, harnessOptions{ResendInterval: time.Hour})
	c := h.client()

	verificado := "verificado@exemplo.com"
	h.registerAndVerify(verificado)

	pendente := "pendente@exemplo.com"
	require.Equal(t, http.StatusAccepted, c.register(pendente, senhaPadrao).Status)

	mesmaResposta(t, "grupo A com cooldown",
		caso{"e-mail novo", c.register("novo@exemplo.com", senhaPadrao), "novo@exemplo.com"},
		caso{"e-mail já verificado", c.register(verificado, senhaPadrao), verificado},
		caso{"pendente DENTRO do cooldown", c.register(pendente, senhaPadrao), pendente},
	)

	// E o cooldown de fato segurou o envio do pendente.
	assert.Equal(t, 1, contarPara(h, mailVerification, pendente))
}

func contarPara(h *harness, kind mailKind, to string) int {
	n := 0
	for _, m := range h.mails.all() {
		if m.Kind == kind && strings.EqualFold(m.To, to) {
			n++
		}
	}
	return n
}

// Os testes de PRE-HIJACK vivem em service_prehijack_test.go: o modelo de
// tentativas com registrationToken mudou o invariante que eles cobram, e
// juntá-los num arquivo só deixa claro qual é a defesa e o que a sustenta.

// ---------------------------------------------------------------------------
// PoC da negação de cadastro por procuração — mitigada pela FOLGA de cota
// ---------------------------------------------------------------------------

// O PoC original provava o ataque: com a cota de mensagens por endereço em
// 3/h e o teto de cadastros por IP em 5/h, um ÚNICO IP zerava o balde do
// endereço da vítima em ~3,7 cadastros por hora, e o dono do endereço nunca
// mais recebia um código. Como a cota governa o ENVIO e não pode recusar a
// requisição (o e-mail vem do corpo, sem prova de posse), não havia como o
// servidor distinguir o atacante do dono.
//
// A decisão de produto (09/09/2026) foi dar FOLGA: a cota por endereço subiu
// para 10/h, acima do teto de 5 cadastros/h por IP. Um IP sozinho não drena
// mais o balde — sobram pelo menos 5 slots para o dono.
//
// Este teste é o PoC com a asserção INVERTIDA, e roda com os NÚMEROS DE
// PRODUÇÃO (config.DefaultRateLimits): o atacante martela no máximo o que o
// teto por IP dele permite, e o dono ainda assim recebe o código e conclui o
// cadastro — sem precisar de reenvio.
//
// LIMITE HONESTO DA MITIGAÇÃO: ela encarece, não elimina. Com pool de IPs o
// atacante volta a drenar os 10 slots; fechar isso depende de reputação de
// IP, que é de outra fase.
func TestDonoConcluiCadastroMesmoComAtacanteQueimandoACotaDeUmIP(t *testing.T) {
	t.Parallel()

	rl := config.DefaultRateLimits()
	// assert (e não require) de propósito: se a relação for quebrada, o teste
	// tem de seguir até o fim e MOSTRAR o dono sendo calado, que é o dano
	// real — não parar numa asserção de configuração.
	assert.Greater(t, rl.RegisterMailPerAccount.Requests, rl.Register.Requests,
		"cota por endereço <= teto por IP: um único IP drena o balde do endereço alheio")

	relogio := newTestClock()
	h := newHarness(t, harnessOptions{
		Clock: relogio,
		// Números de produção, com o relógio do teste.
		VerificationMailLimiter: httpserver.
			NewLimiter(rl.RegisterMailPerAccount.Requests, rl.RegisterMailPerAccount.Window, rl.IdleTTL).
			WithClock(relogio.Now),
		ResendInterval: time.Minute, // cooldown de produção
	})

	const alvo = "vitima@exemplo.com"
	const senhaDoInvasor = "senha-escolhida-pelo-invasor-2026"

	// O atacante gasta TODO o orçamento de um IP: 5 cadastros na hora, cada
	// um fora do cooldown de 60 s para que cada um consuma um slot de cota.
	atacante := h.client()
	for range rl.Register.Requests {
		h.clock.Advance(2 * time.Minute)
		require.Equal(t, http.StatusAccepted, atacante.register(alvo, senhaDoInvasor).Status)
	}
	gastas := h.mails.countOf(mailVerification)
	t.Logf("slots de cota que um único IP conseguiu queimar: %d de %d",
		gastas, rl.RegisterMailPerAccount.Requests)

	// Dentro da MESMA janela de uma hora, o dono se cadastra — de outro IP,
	// como qualquer pessoa que não é o atacante — e precisa RECEBER o código.
	h.clock.Advance(2 * time.Minute)
	dono := h.client()
	require.Equal(t, http.StatusAccepted, dono.register(alvo, senhaPadrao).Status)
	require.Equal(t, gastas+1, h.mails.countOf(mailVerification),
		"a cota queimada por um único IP calou o endereço do dono — negação de cadastro por procuração")

	// E o código que chegou é o DELE: conclui o cadastro com a senha dele, na
	// primeira tentativa, sem depender de reenvio.
	codigo, ok := h.mails.lastOf(mailVerification)
	require.True(t, ok)
	require.Equal(t, http.StatusOK, dono.verify(alvo, codigo.Code).Status)

	novo := h.client()
	assert.Equal(t, http.StatusOK, novo.login(alvo, senhaPadrao).Status)
	assert.Equal(t, http.StatusUnauthorized, novo.login(alvo, senhaDoInvasor).Status,
		"a senha do atacante não pode ativar nada")

	// A folga real que sobrou para o dono nesta janela.
	sobra := rl.RegisterMailPerAccount.Requests - h.mails.countOf(mailVerification)
	t.Logf("slots de cota restantes para o endereço na janela: %d", sobra)
	assert.GreaterOrEqual(t, sobra, 0)
}
