package auth

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/user"
	"github.com/go-playground/validator/v10"
)

// Escopos das chaves de rate limit por conta.
const (
	ScopeLogin          = "login"
	ScopeForgotPassword = "forgot_password"
	// ScopeRegister e ScopeAccountExists são usados pelas cotas de ENVIO do
	// serviço (auth.MailLimiters), não por limitador de requisição.
	ScopeRegister      = "register"
	ScopeAccountExists = "account_exists"
)

// AccountLimiters são os limitadores "por conta" da §7 da spec 0001.
//
// A chave nunca é o e-mail: é o HMAC do e-mail com o pepper (OTP.AccountKey),
// para que a lista de e-mails tentados não fique legível em memória.
//
// NÃO existe limitador por conta para /auth/register aqui, e é decisão de
// segurança: o e-mail do cadastro vem do corpo, sem prova de posse, então um
// 429 por endereço seria LOCKOUT POR PROCURAÇÃO — três requisições bem
// formadas de um terceiro trancariam o cadastro de um endereço alheio, e o
// dono legítimo (que precisa se cadastrar para reassumir o pendente) ficaria
// de fora. O teto por endereço exigido pela §1.1 existe, mas governa o ENVIO
// dentro do serviço (auth.MailLimiters).
//
// Login e forgot-password podem ter teto por conta porque neles o 429 não
// impede ninguém de recuperar a própria conta de forma permanente.
type AccountLimiters struct {
	Login          RateLimiter
	ForgotPassword RateLimiter
}

// Handler expõe os endpoints de autenticação.
type Handler struct {
	svc             *Service
	cookies         *CookieJar
	lg              *slog.Logger
	validate        *validator.Validate
	limits          AccountLimiters
	minResponseTime time.Duration
	trustedProxies  int
}

// HandlerOptions configura o handler.
type HandlerOptions struct {
	MinResponseTime   time.Duration
	TrustedProxyCount int
	Limiters          AccountLimiters
}

// NewHandler monta o handler.
func NewHandler(svc *Service, lg *slog.Logger, opts HandlerOptions) *Handler {
	return &Handler{
		svc:             svc,
		cookies:         svc.Cookies(),
		lg:              lg,
		validate:        validator.New(validator.WithRequiredStructEnabled()),
		limits:          opts.Limiters,
		minResponseTime: opts.MinResponseTime,
		trustedProxies:  opts.TrustedProxyCount,
	}
}

// Authenticate implementa httpserver.Authenticator.
//
// Lê o cookie de access e valida o JWT. NÃO toca no banco: é o caminho quente
// de toda requisição autenticada. A revalidação do vínculo contra a realidade
// acontece em /me e no refresh (risco 2 da §9 da spec 0001).
func (h *Handler) Authenticate(r *http.Request) (session.Identity, error) {
	return h.svc.tokenSvc.ParseAccessToken(h.cookies.ReadAccessToken(r))
}

// ---------------------------------------------------------------------------
// Corpos de resposta
// ---------------------------------------------------------------------------

// verificationRequiredResponse é o corpo ÚNICO de /auth/register e
// /auth/resend-code (grupos A e B da §3.12). O e-mail devolvido é o que o
// cliente mandou, já normalizado — não revela nada sobre a base.
//
// registrationToken é o segredo que amarra o código de 6 dígitos à SESSÃO DE
// CADASTRO que o pediu (ver auth.RegistrationAttempt). Ele pode viajar aqui
// sem furar os grupos A e B porque é sorteado no servidor com crypto/rand,
// tem sempre 64 hexadecimais e NÃO depende do estado da conta: os três
// caminhos do registro e os quatro do reenvio devolvem um valor com a mesma
// forma e o mesmo tamanho.
//
// Ele NÃO é credencial de sessão: sozinho não autentica nada, e só serve
// junto do código que foi para a caixa de entrada. Por isso vai no corpo, e
// não em cookie — o cliente precisa guardá-lo entre duas telas do cadastro.
type verificationRequiredResponse struct {
	Status            string `json:"status"`
	Email             string `json:"email"`
	ExpiresInSeconds  int    `json:"expiresInSeconds"`
	RegistrationToken string `json:"registrationToken"`
}

// acceptedResponse é o corpo ÚNICO de /auth/forgot-password (grupo C).
type acceptedResponse struct {
	Status           string `json:"status"`
	Message          string `json:"message"`
	ExpiresInSeconds int    `json:"expiresInSeconds"`
}

// acceptedRecoveryMessage é a mensagem única do grupo C da §3.12.
const acceptedRecoveryMessage = "Se este e-mail estiver cadastrado, enviamos um código de 6 dígitos."

// ---------------------------------------------------------------------------
// Corpos de requisição
// ---------------------------------------------------------------------------

type registerRequest struct {
	Name     string `json:"name"     validate:"required,min=1,max=120"`
	Email    string `json:"email"    validate:"required,min=3,max=254"`
	Password string `json:"password" validate:"required,min=12,max=256"`
}

type emailOnlyRequest struct {
	Email string `json:"email" validate:"required,min=3,max=254"`
}

// resendCodeRequest é separado de emailOnlyRequest de propósito: o decodifi-
// cador recusa campo desconhecido, então /auth/forgot-password não pode
// passar a aceitar um registrationToken só porque o reenvio aceita.
//
// O token é `omitempty` aqui E fora de `required` no openapi.yaml — o
// contrato e o handler dizem a MESMA coisa (achado BAIXA-2 da revisão final;
// antes o contrato prometia uma obrigatoriedade que o servidor não cobrava).
//
// Sem o token o serviço não emite absolutamente nada, e a resposta continua
// sendo o mesmo 202 dos outros casos do grupo B da §3.12. Devolver 400 só
// quando o campo falta seria uma resposta a mais para o cliente distinguir
// sem ganho nenhum de segurança — o formato do PRÓPRIO pedido não é segredo
// de ninguém, mas manter uma única forma de resposta é mais barato de
// auditar.
//
// O campo continua com forma checada quando VEM: 64 hexadecimais ou 400.
type resendCodeRequest struct {
	Email             string `json:"email"             validate:"required,min=3,max=254"`
	RegistrationToken string `json:"registrationToken" validate:"omitempty,len=64,hexadecimal"`
}

type verifyEmailRequest struct {
	Email string `json:"email" validate:"required,min=3,max=254"`
	Code  string `json:"code"  validate:"required,len=6,number"`
	// Obrigatório: o código só é procurado DENTRO da tentativa que este
	// token identifica. Sem ele não há o que validar.
	RegistrationToken string `json:"registrationToken" validate:"required,len=64,hexadecimal"`
}

type loginRequest struct {
	Email    string `json:"email"    validate:"required,min=3,max=254"`
	Password string `json:"password" validate:"required,min=1,max=256"`
}

type resetPasswordRequest struct {
	Email       string `json:"email"       validate:"required,min=3,max=254"`
	Code        string `json:"code"        validate:"required,len=6,number"`
	NewPassword string `json:"newPassword" validate:"required,min=12,max=256"`
}

// ---------------------------------------------------------------------------
// Endpoints
// ---------------------------------------------------------------------------

// Register trata POST /api/v1/auth/register.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer h.pad(r, start)

	req, ok := decode[registerRequest](h, w, r)
	if !ok {
		return
	}

	email, name, ok := h.normalizeIdentity(w, req.Email, req.Name)
	if !ok {
		return
	}
	if err := h.svc.Policy().Validate(req.Password, email, name); err != nil {
		httpserver.WriteValidationError(w, map[string]string{"password": err.Error()})
		return
	}

	// Sem limitador por conta aqui de propósito (ver AccountLimiters): o teto
	// por endereço vive no serviço e limita o ENVIO, não a aceitação.
	token, err := h.svc.Register(r.Context(), RegisterInput{
		Name:     name,
		Email:    email,
		Password: req.Password,
		IP:       h.clientIP(r),
	})
	if err != nil {
		h.internal(w, r, "falha no registro", err)
		return
	}

	httpserver.WriteJSON(w, http.StatusAccepted, verificationRequiredResponse{
		Status:            "verification_required",
		Email:             email,
		ExpiresInSeconds:  int(h.svc.CodeTTL().Seconds()),
		RegistrationToken: token,
	})
}

// ResendCode trata POST /api/v1/auth/resend-code.
func (h *Handler) ResendCode(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer h.pad(r, start)

	req, ok := decode[resendCodeRequest](h, w, r)
	if !ok {
		return
	}
	email, ok := h.normalizeEmail(w, req.Email)
	if !ok {
		return
	}

	token, err := h.svc.ResendCode(r.Context(), email, req.RegistrationToken, h.clientIP(r))
	if err != nil {
		h.internal(w, r, "falha no reenvio de código", err)
		return
	}

	httpserver.WriteJSON(w, http.StatusAccepted, verificationRequiredResponse{
		Status:            "verification_required",
		Email:             email,
		ExpiresInSeconds:  int(h.svc.CodeTTL().Seconds()),
		RegistrationToken: token,
	})
}

// VerifyEmail trata POST /api/v1/auth/verify-email.
func (h *Handler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer h.pad(r, start)

	req, ok := decode[verifyEmailRequest](h, w, r)
	if !ok {
		return
	}
	email, ok := h.normalizeEmail(w, req.Email)
	if !ok {
		return
	}

	sess, err := h.svc.VerifyEmail(r.Context(), VerifyEmailInput{
		Email: email,
		Code:  req.Code,
		Token: req.RegistrationToken,
		IP:    h.clientIP(r),
	})
	if err != nil {
		if errors.Is(err, ErrInvalidCode) {
			httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeInvalidCode, httpserver.MsgInvalidCode)
			return
		}
		h.internal(w, r, "falha na verificação de e-mail", err)
		return
	}

	h.cookies.SetSessionCookies(w, sess.AccessToken, sess.RefreshToken)
	httpserver.WriteJSON(w, http.StatusOK, sess.View)
}

// Login trata POST /api/v1/auth/login.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer h.pad(r, start)

	req, ok := decode[loginRequest](h, w, r)
	if !ok {
		return
	}
	email, ok := h.normalizeEmail(w, req.Email)
	if !ok {
		return
	}

	// Limite por conta. A chave é o HMAC do e-mail: vale igual para conta
	// existente e inexistente, então o 429 não vira oráculo.
	if !h.allowAccount(w, h.limits.Login, ScopeLogin, email) {
		return
	}

	sess, err := h.svc.Login(r.Context(), LoginInput{
		Email:    email,
		Password: req.Password,
		IP:       h.clientIP(r),
	})
	switch {
	case err == nil:
		h.cookies.SetSessionCookies(w, sess.AccessToken, sess.RefreshToken)
		httpserver.WriteJSON(w, http.StatusOK, sess.View)

	case errors.Is(err, ErrInvalidCredentials):
		httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeInvalidCredentials, httpserver.MsgInvalidCredentials)

	case errors.Is(err, ErrEmailNotVerified):
		// §3.12: emitido só DEPOIS de a senha conferir. O frontend leva o
		// usuário para /confirmar-email; o código novo já foi enfileirado.
		httpserver.WriteError(w, http.StatusForbidden, httpserver.CodeEmailNotVerified, httpserver.MsgEmailNotVerified)

	default:
		h.internal(w, r, "falha no login", err)
	}
}

// Refresh trata POST /api/v1/auth/refresh.
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	sess, err := h.svc.Refresh(r.Context(), h.cookies.ReadRefreshToken(r), h.clientIP(r))
	if err != nil {
		if errors.Is(err, ErrInvalidSession) {
			// Grupo F: ausente, malformado, expirado, revogado e reusado
			// devolvem exatamente isto. Os cookies caem SEMPRE, para o
			// cliente não ficar num laço de refresh com credencial morta.
			h.cookies.ClearSessionCookies(w)
			httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeInvalidSession, httpserver.MsgInvalidSession)
			return
		}
		h.internal(w, r, "falha no refresh", err)
		return
	}

	h.cookies.SetSessionCookies(w, sess.AccessToken, sess.RefreshToken)
	httpserver.WriteJSON(w, http.StatusOK, sess.View)
}

// Logout trata POST /api/v1/auth/logout. Sempre 204 (§3.8).
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	h.svc.Logout(r.Context(), h.cookies.ReadRefreshToken(r), h.clientIP(r))
	h.cookies.ClearSessionCookies(w)
	httpserver.WriteNoContent(w)
}

// ForgotPassword trata POST /api/v1/auth/forgot-password.
func (h *Handler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer h.pad(r, start)

	req, ok := decode[emailOnlyRequest](h, w, r)
	if !ok {
		return
	}
	email, ok := h.normalizeEmail(w, req.Email)
	if !ok {
		return
	}
	if !h.allowAccount(w, h.limits.ForgotPassword, ScopeForgotPassword, email) {
		return
	}

	if err := h.svc.ForgotPassword(r.Context(), email, h.clientIP(r)); err != nil {
		h.internal(w, r, "falha na recuperação de senha", err)
		return
	}

	httpserver.WriteJSON(w, http.StatusAccepted, acceptedResponse{
		Status:           "accepted",
		Message:          acceptedRecoveryMessage,
		ExpiresInSeconds: int(h.svc.CodeTTL().Seconds()),
	})
}

// ResetPassword trata POST /api/v1/auth/reset-password.
func (h *Handler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer h.pad(r, start)

	req, ok := decode[resetPasswordRequest](h, w, r)
	if !ok {
		return
	}
	email, ok := h.normalizeEmail(w, req.Email)
	if !ok {
		return
	}
	// A política é avaliada só contra a senha e o e-mail informados: usar o
	// nome do titular exigiria carregar a conta e vazaria existência.
	if err := h.svc.Policy().Validate(req.NewPassword, email, ""); err != nil {
		httpserver.WriteValidationError(w, map[string]string{"newPassword": err.Error()})
		return
	}

	err := h.svc.ResetPassword(r.Context(), ResetPasswordInput{
		Email:       email,
		Code:        req.Code,
		NewPassword: req.NewPassword,
		IP:          h.clientIP(r),
	})
	if err != nil {
		if errors.Is(err, ErrInvalidCode) {
			httpserver.WriteError(w, http.StatusUnauthorized, httpserver.CodeInvalidCode, httpserver.MsgInvalidCode)
			return
		}
		h.internal(w, r, "falha ao redefinir senha", err)
		return
	}

	// A senha mudou e todas as sessões caíram: os cookies desta requisição
	// também precisam morrer.
	h.cookies.ClearSessionCookies(w)
	httpserver.WriteNoContent(w)
}

// ---------------------------------------------------------------------------
// Apoio
// ---------------------------------------------------------------------------

// pad normaliza o tempo de resposta dos endpoints sensíveis (§3.12).
func (h *Handler) pad(r *http.Request, start time.Time) {
	padTo(r.Context(), start, h.minResponseTime)
}

func (h *Handler) clientIP(r *http.Request) string {
	return httpserver.ClientIP(r, h.trustedProxies)
}

// decode lê e valida o corpo, já respondendo o erro apropriado.
func decode[T any](h *Handler, w http.ResponseWriter, r *http.Request) (T, bool) {
	var zero T
	req, err := httpserver.DecodeJSON[T](w, r)
	if err != nil {
		httpserver.WriteDecodeError(w, err)
		return zero, false
	}
	if err := h.validate.Struct(req); err != nil {
		httpserver.WriteValidationError(w, validationFields(err))
		return zero, false
	}
	return req, true
}

// validationFields traduz o erro do validator para o mapa "fields" do
// contrato. As mensagens são genéricas e em pt-BR; nenhum valor enviado pelo
// cliente é ecoado de volta.
func validationFields(err error) map[string]string {
	fields := map[string]string{}

	var verrs validator.ValidationErrors
	if !errors.As(err, &verrs) {
		return map[string]string{"_": "dados inválidos"}
	}
	for _, fe := range verrs {
		name := jsonFieldName(fe.Field())
		switch fe.Tag() {
		case "required":
			fields[name] = "campo obrigatório"
		case "min":
			fields[name] = fmt.Sprintf("mínimo de %s caracteres", fe.Param())
		case "max":
			fields[name] = fmt.Sprintf("máximo de %s caracteres", fe.Param())
		case "len":
			fields[name] = fmt.Sprintf("deve ter exatamente %s caracteres", fe.Param())
		case "number":
			fields[name] = "deve conter apenas dígitos"
		case "hexadecimal":
			fields[name] = "formato inválido"
		default:
			fields[name] = "valor inválido"
		}
	}
	return fields
}

func jsonFieldName(structField string) string {
	switch structField {
	case "Name":
		return "name"
	case "Email":
		return "email"
	case "Password":
		return "password"
	case "NewPassword":
		return "newPassword"
	case "Code":
		return "code"
	case "RegistrationToken":
		return "registrationToken"
	default:
		return structField
	}
}

func (h *Handler) normalizeEmail(w http.ResponseWriter, raw string) (string, bool) {
	email, err := user.NormalizeEmail(raw)
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{"email": "e-mail inválido"})
		return "", false
	}
	return email, true
}

func (h *Handler) normalizeIdentity(w http.ResponseWriter, rawEmail, rawName string) (email, name string, ok bool) {
	email, err := user.NormalizeEmail(rawEmail)
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{"email": "e-mail inválido"})
		return "", "", false
	}
	name, err = user.NormalizeName(rawName)
	if err != nil {
		httpserver.WriteValidationError(w, map[string]string{"name": "nome inválido"})
		return "", "", false
	}
	return email, name, true
}

// allowAccount aplica o limitador por conta. Devolve false quando já
// respondeu 429.
func (h *Handler) allowAccount(w http.ResponseWriter, lim RateLimiter, scope, email string) bool {
	if lim == nil {
		return true
	}
	ok, retry := lim.Allow(h.svc.OTP().AccountKey(scope, email))
	if !ok {
		httpserver.WriteRateLimited(w, retry)
		return false
	}
	return true
}

// internal loga o detalhe e devolve 500 genérico (docs/SEGURANCA.md §4).
func (h *Handler) internal(w http.ResponseWriter, r *http.Request, msg string, err error) {
	h.lg.ErrorContext(r.Context(), msg,
		slog.String("request_id", httpserver.RequestIDFromContext(r.Context())),
		slog.String("reason", err.Error()),
	)
	httpserver.WriteError(w, http.StatusInternalServerError, httpserver.CodeInternalError, httpserver.MsgInternalError)
}
