package auth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mailer de teste
// ---------------------------------------------------------------------------

type mailKind string

const (
	mailVerification  mailKind = "verification"
	mailPasswordReset mailKind = "password_reset"
	mailAccountExists mailKind = "account_exists"
)

type sentMail struct {
	Kind  mailKind
	To    string
	Name  string
	Code  string
	TTL   time.Duration
	Order int
}

// fakeMailer implementa auth.Mailer. Como o contrato é fire-and-forget, ele
// só guarda o que seria enviado.
type fakeMailer struct {
	mu   sync.Mutex
	sent []sentMail
}

func (m *fakeMailer) record(kind mailKind, to, name, code string, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMail{Kind: kind, To: to, Name: name, Code: code, TTL: ttl, Order: len(m.sent)})
}

// EnqueueVerificationCode não recebe nome de propósito (achado ALTA-2): a
// mensagem de primeiro contato não pode carregar texto de quem pediu o
// cadastro. O campo Name do registro fica vazio, e é isso que o teste de
// regressão confere.
func (m *fakeMailer) EnqueueVerificationCode(to, code string, ttl time.Duration) {
	m.record(mailVerification, to, "", code, ttl)
}

func (m *fakeMailer) EnqueuePasswordResetCode(to, name, code string, ttl time.Duration) {
	m.record(mailPasswordReset, to, name, code, ttl)
}

func (m *fakeMailer) EnqueueAccountExistsNotice(to, name string) {
	m.record(mailAccountExists, to, name, "", 0)
}

func (m *fakeMailer) all() []sentMail {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]sentMail, len(m.sent))
	copy(out, m.sent)
	return out
}

func (m *fakeMailer) reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = nil
}

// lastOf devolve o último e-mail do tipo pedido.
func (m *fakeMailer) lastOf(kind mailKind) (sentMail, bool) {
	all := m.all()
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Kind == kind {
			return all[i], true
		}
	}
	return sentMail{}, false
}

func (m *fakeMailer) countOf(kind mailKind) int {
	n := 0
	for _, s := range m.all() {
		if s.Kind == kind {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Relógio controlável
// ---------------------------------------------------------------------------

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func newTestClock() *testClock {
	return &testClock{now: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// ---------------------------------------------------------------------------
// Arnês
// ---------------------------------------------------------------------------

type harnessOptions struct {
	MinResponseTime time.Duration
	CookieSecure    bool
	OTPMaxAttempts  int
	ResendInterval  time.Duration
	LoginLimiter    auth.RateLimiter
	ForgotLimiter   auth.RateLimiter
	// Cotas de ENVIO por endereço (auth.MailLimiters), não limitadores de
	// requisição: elas seguram mensagem, nunca devolvem 429.
	VerificationMailLimiter auth.RateLimiter
	AccountExistsLimiter    auth.RateLimiter
	// Middlewares envolvem o mux, do mais externo para o mais interno.
	Middlewares []httpserver.Middleware

	// WrapTokens envolve o repositório de refresh, para o teste intercalar
	// operações em pontos EXATOS do fluxo. É o que permite provar a corrida
	// da rotação de forma determinística, sem depender de escalonamento.
	WrapTokens func(auth.RefreshTokenRepository) auth.RefreshTokenRepository

	// WrapUsers envolve o repositório de usuários, pelo mesmo motivo do
	// WrapTokens: intercalar uma escrita numa janela EXATA (por exemplo,
	// "a verificação do e-mail commitou entre a leitura e a rotação") sem
	// depender de escalonamento de goroutine.
	WrapUsers func(user.Repository) user.Repository

	// Mailer substitui o mailer de teste. Serve para exercitar as mensagens
	// REAIS (mailer.Notifier), quando o que está sob teste é o conteúdo
	// entregue e não o fluxo.
	Mailer auth.Mailer

	// Clock permite ao teste construir o relógio ANTES do arnês, para que um
	// limitador passado nas opções acima possa compartilhá-lo
	// (httpserver.Limiter.WithClock). Sem isso, as cotas por endereço
	// andariam pelo relógio de parede enquanto o resto do fluxo anda pelo
	// relógio congelado — e nenhum teste de janela longa seria possível.
	Clock *testClock
}

type harness struct {
	t        *testing.T
	svc      *auth.Service
	handler  *auth.Handler
	mux      http.Handler
	mails    *fakeMailer
	clock    *testClock
	logs     *bytes.Buffer
	db       *storage.DB
	audit    *gormstore.AuditRepository
	users    user.Repository
	tokens   auth.RefreshTokenRepository
	codes    auth.VerificationCodeRepository
	attempts auth.RegistrationAttemptRepository
	jar      *auth.CookieJar
}

func newHarness(t *testing.T, opts harnessOptions) *harness {
	t.Helper()

	ctx := t.Context()
	logs := &bytes.Buffer{}
	lg := logging.New(logs, logging.Options{Level: "debug", Format: "json"})

	db, err := storage.Open(ctx, storage.Options{
		Driver:       storage.DriverSQLite,
		DSN:          filepath.Join(t.TempDir(), "auth-test.db"),
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}, logging.Discard())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, storage.Migrate(ctx, db, nil, gormstore.Models()...))

	var userRepo user.Repository = gormstore.NewUserRepository(db)
	if opts.WrapUsers != nil {
		userRepo = opts.WrapUsers(userRepo)
	}
	householdRepo := gormstore.NewHouseholdRepository(db)
	membershipRepo := gormstore.NewMembershipRepository(db)
	var refreshRepo auth.RefreshTokenRepository = gormstore.NewRefreshTokenRepository(db)
	if opts.WrapTokens != nil {
		refreshRepo = opts.WrapTokens(refreshRepo)
	}
	codeRepo := gormstore.NewVerificationCodeRepository(db)
	attemptRepo := gormstore.NewRegistrationAttemptRepository(db)
	auditRepo := gormstore.NewAuditRepository(db)
	uow := gormstore.NewUnitOfWork(db)

	clock := opts.Clock
	if clock == nil {
		clock = newTestClock()
	}
	mails := &fakeMailer{}

	var envio auth.Mailer = mails
	if opts.Mailer != nil {
		envio = opts.Mailer
	}

	auditSvc := audit.NewService(auditRepo, lg, audit.WithClock(clock.Now))
	householdSvc := household.NewService(householdRepo, membershipRepo, household.WithClock(clock.Now))
	userSvc := user.NewService(userRepo, householdSvc)

	hasher, err := auth.NewPasswordHasher(auth.Argon2Params{MaxConcurrent: 4})
	require.NoError(t, err)

	tokenSvc, err := auth.NewTokenService(auth.TokenOptions{
		Secret:     segredoDeTeste,
		Issuer:     "homefinance",
		Audience:   "homefinance-api",
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 14 * 24 * time.Hour,
		Clock:      clock.Now,
	})
	require.NoError(t, err)

	otpSvc, err := auth.NewOTP(pepperDeTeste)
	require.NoError(t, err)

	jar := auth.NewCookieJar(auth.CookieOptions{
		Secure:     opts.CookieSecure,
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 14 * 24 * time.Hour,
	})

	maxAttempts := opts.OTPMaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 5
	}
	resend := opts.ResendInterval
	if resend == 0 {
		resend = time.Minute
	}

	svc, err := auth.NewService(auth.Deps{
		Users:      userRepo,
		Households: householdSvc,
		Tokens:     refreshRepo,
		Codes:      codeRepo,
		Attempts:   attemptRepo,
		Audit:      auditSvc,
		UoW:        uow,
		Hasher:     hasher,
		Policy:     auth.NewPasswordPolicy(),
		TokenSvc:   tokenSvc,
		OTP:        otpSvc,
		Mailer:     envio,
		Cookies:    jar,
		View:       userSvc,
		Logger:     lg,
		MailLimits: auth.MailLimiters{
			VerificationMail:    opts.VerificationMailLimiter,
			AccountExistsNotice: opts.AccountExistsLimiter,
		},
		Clock: clock.Now,
	}, auth.ServiceOptions{
		OTPTTL:            15 * time.Minute,
		OTPMaxAttempts:    maxAttempts,
		OTPResendInterval: resend,
		RefreshRetention:  30 * 24 * time.Hour,
	})
	require.NoError(t, err)

	handler := auth.NewHandler(svc, lg, auth.HandlerOptions{
		MinResponseTime: opts.MinResponseTime,
		Limiters: auth.AccountLimiters{
			Login:          opts.LoginLimiter,
			ForgotPassword: opts.ForgotLimiter,
		},
	})
	userHandler := user.NewHandler(userSvc, jar, lg)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/register", handler.Register)
	mux.HandleFunc("POST /api/v1/auth/verify-email", handler.VerifyEmail)
	mux.HandleFunc("POST /api/v1/auth/resend-code", handler.ResendCode)
	mux.HandleFunc("POST /api/v1/auth/login", handler.Login)
	mux.HandleFunc("POST /api/v1/auth/refresh", handler.Refresh)
	mux.HandleFunc("POST /api/v1/auth/logout", handler.Logout)
	mux.HandleFunc("POST /api/v1/auth/forgot-password", handler.ForgotPassword)
	mux.HandleFunc("POST /api/v1/auth/reset-password", handler.ResetPassword)
	mux.Handle("GET /api/v1/me", httpserver.RequireAuth(handler)(http.HandlerFunc(userHandler.Me)))

	return &harness{
		t:        t,
		svc:      svc,
		handler:  handler,
		mux:      httpserver.Chain(httpserver.ErrorShim(mux), opts.Middlewares...),
		mails:    mails,
		clock:    clock,
		logs:     logs,
		db:       db,
		audit:    auditRepo,
		users:    userRepo,
		tokens:   refreshRepo,
		codes:    codeRepo,
		attempts: attemptRepo,
		jar:      jar,
	}
}

// ---------------------------------------------------------------------------
// Cliente HTTP de teste
// ---------------------------------------------------------------------------

type response struct {
	Status  int
	Body    string
	Header  http.Header
	Cookies []*http.Cookie
}

func (r response) json(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.Body), &out), "corpo não é JSON: %s", r.Body)
	return out
}

func (r response) errorCode(t *testing.T) string {
	t.Helper()
	body := r.json(t)
	errObj, ok := body["error"].(map[string]any)
	require.True(t, ok, "resposta sem envelope de erro: %s", r.Body)
	code, _ := errObj["code"].(string)
	return code
}

func (r response) cookie(name string) *http.Cookie {
	for _, c := range r.Cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// client guarda os cookies E o registrationToken entre chamadas, como a aba
// do navegador que conduz o cadastro faria.
//
// O token viver no cliente é a parte que importa nos testes de pre-hijack:
// dois `client` diferentes são duas pessoas diferentes, e uma NÃO tem o
// segredo da outra.
type client struct {
	h        *harness
	cookies  map[string]string
	ip       string
	regToken string
}

func (h *harness) client() *client {
	return &client{h: h, cookies: map[string]string{}, ip: "203.0.113.10"}
}

// captureToken guarda o registrationToken devolvido por register/resend-code.
func (c *client) captureToken(res response) response {
	var corpo struct {
		RegistrationToken string `json:"registrationToken"`
	}
	if err := json.Unmarshal([]byte(res.Body), &corpo); err == nil && corpo.RegistrationToken != "" {
		c.regToken = corpo.RegistrationToken
	}
	return res
}

func (c *client) do(method, path string, body any) response {
	c.h.t.Helper()

	var reader *strings.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(c.h.t, err)
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}

	r := httptest.NewRequest(method, path, reader)
	r.RemoteAddr = c.ip + ":54321"
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	for name, value := range c.cookies {
		r.AddCookie(&http.Cookie{Name: name, Value: value})
	}

	rec := httptest.NewRecorder()
	c.h.mux.ServeHTTP(rec, r)

	res := rec.Result()
	out := response{
		Status:  res.StatusCode,
		Body:    rec.Body.String(),
		Header:  res.Header.Clone(),
		Cookies: res.Cookies(),
	}
	for _, ck := range out.Cookies {
		if ck.MaxAge < 0 || ck.Value == "" {
			delete(c.cookies, ck.Name)
			continue
		}
		c.cookies[ck.Name] = ck.Value
	}
	return out
}

// doRaw envia um corpo cru (para testar 400/413/415).
func (c *client) doRaw(method, path, contentType, body string) response {
	c.h.t.Helper()

	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = c.ip + ":54321"
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	for name, value := range c.cookies {
		r.AddCookie(&http.Cookie{Name: name, Value: value})
	}

	rec := httptest.NewRecorder()
	c.h.mux.ServeHTTP(rec, r)
	res := rec.Result()
	return response{Status: res.StatusCode, Body: rec.Body.String(), Header: res.Header.Clone(), Cookies: res.Cookies()}
}

// ---------------------------------------------------------------------------
// Atalhos de fluxo
// ---------------------------------------------------------------------------

const (
	senhaPadrao = "cavalo-bateria-grampo-2026"
	nomePadrao  = "Bruno Blanck"
)

func (c *client) register(email, senha string) response {
	return c.captureToken(c.do(http.MethodPost, "/api/v1/auth/register", map[string]string{
		"name": nomePadrao, "email": email, "password": senha,
	}))
}

// resend usa o token que ESTE cliente tem. Sem token, cai no caminho de
// recuperação (só vale para endereço não disputado).
func (c *client) resend(email string) response {
	corpo := map[string]string{"email": email}
	if c.regToken != "" {
		corpo["registrationToken"] = c.regToken
	}
	return c.captureToken(c.do(http.MethodPost, "/api/v1/auth/resend-code", corpo))
}

// resendSemToken simula quem recarregou a página e perdeu o token.
func (c *client) resendSemToken(email string) response {
	return c.captureToken(c.do(http.MethodPost, "/api/v1/auth/resend-code", map[string]string{"email": email}))
}

// verify apresenta o código JUNTO do registrationToken deste cliente. É o
// escopo que torna o código do atacante inútil na mão da vítima.
func (c *client) verify(email, code string) response {
	return c.do(http.MethodPost, "/api/v1/auth/verify-email", map[string]string{
		"email": email, "code": code, "registrationToken": c.regToken,
	})
}

// verifyCom apresenta o código com um token ESCOLHIDO pelo teste.
func (c *client) verifyCom(email, code, token string) response {
	return c.do(http.MethodPost, "/api/v1/auth/verify-email", map[string]string{
		"email": email, "code": code, "registrationToken": token,
	})
}

func (c *client) login(email, senha string) response {
	return c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{
		"email": email, "password": senha,
	})
}

func (c *client) me() response {
	return c.do(http.MethodGet, "/api/v1/me", nil)
}

func (c *client) refresh() response {
	return c.do(http.MethodPost, "/api/v1/auth/refresh", nil)
}

func (c *client) logout() response {
	return c.do(http.MethodPost, "/api/v1/auth/logout", nil)
}

// registerAndVerify faz o caminho feliz completo e devolve o cliente logado.
func (h *harness) registerAndVerify(email string) *client {
	h.t.Helper()

	c := h.client()
	res := c.register(email, senhaPadrao)
	require.Equal(h.t, http.StatusAccepted, res.Status, "registro falhou: %s", res.Body)

	mail, ok := h.mails.lastOf(mailVerification)
	require.True(h.t, ok, "nenhum código de verificação foi enfileirado")

	res = c.verify(email, mail.Code)
	require.Equal(h.t, http.StatusOK, res.Status, "verificação falhou: %s", res.Body)
	return c
}

func (h *harness) logOutput() string { return h.logs.String() }

// injetarCodigoNaTentativa força um código de 6 dígitos ESCOLHIDO pelo teste
// dentro da tentativa que aquele token identifica.
//
// Existe porque o código real é gerado por crypto/rand: não dá para esperar
// que o servidor sorteie "000123" para exercitar os zeros à esquerda. Grava
// pelo mesmo caminho do serviço (só o HMAC, nunca o código), então o que o
// teste prova é o caminho de LEITURA/comparação/consumo de verdade.
func (h *harness) injetarCodigoNaTentativa(t *testing.T, email, token, code string) {
	t.Helper()

	att, err := h.attempts.ByToken(t.Context(), email, auth.HashRegistrationToken(token))
	require.NoError(t, err, "tentativa de cadastro não encontrada para o token")

	now := h.clock.Now()
	ok, err := h.attempts.RotateCode(t.Context(), att.ID,
		h.svc.OTP().Hash(auth.PurposeEmailVerification, email, code), now, now.Add(15*time.Minute))
	require.NoError(t, err)
	require.True(t, ok)
}

// injetarCodigoSemEmitir grava um código ESCOLHIDO pelo teste na tentativa
// daquele token SEM tocar em code_issued_at — ou seja, simula exatamente o
// estado de uma tentativa cujo código foi gerado mas cuja mensagem NUNCA
// saiu (cooldown ou cota seguraram o envio).
//
// Existe porque o código real vem de crypto/rand e, nesse estado, não chega
// a e-mail nenhum: sem injetar, o teste não teria como exercitar "e se o
// atacante adivinhasse o código que ninguém recebeu?". É o UPDATE mínimo
// possível (só code_hash), para que o resto do caminho de validação seja o
// de produção.
func (h *harness) injetarCodigoSemEmitir(t *testing.T, email, token, code string) {
	t.Helper()

	att, err := h.attempts.ByToken(t.Context(), email, auth.HashRegistrationToken(token))
	require.NoError(t, err, "tentativa de cadastro não encontrada para o token")
	require.Nil(t, att.CodeIssuedAt,
		"esta tentativa JÁ teve código emitido; o teste precisa de uma que não teve")

	res := h.db.Gorm().WithContext(t.Context()).
		Table("registration_attempts").
		Where("id = ?", att.ID).
		Update("code_hash", h.svc.OTP().Hash(auth.PurposeEmailVerification, email, code))
	require.NoError(t, res.Error)
	require.EqualValues(t, 1, res.RowsAffected)
}

// forgeAccessToken assina um access token válido com claims arbitrárias.
//
// Serve para provar que a assinatura correta NÃO basta: o hid ainda é
// revalidado contra o banco (risco 2 da §9 da spec 0001).
func (h *harness) forgeAccessToken(userID, householdID, role, sessionID string) string {
	h.t.Helper()

	svc, err := auth.NewTokenService(auth.TokenOptions{
		Secret:     segredoDeTeste,
		Issuer:     "homefinance",
		Audience:   "homefinance-api",
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 14 * 24 * time.Hour,
		Clock:      h.clock.Now,
	})
	require.NoError(h.t, err)

	raw, _, err := svc.IssueAccessToken(session.Identity{
		UserID: userID, HouseholdID: householdID, Role: role, SessionID: sessionID,
	})
	require.NoError(h.t, err)
	return raw
}
