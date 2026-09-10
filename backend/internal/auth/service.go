package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/id"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/brunorblanck/homefinance/backend/internal/user"
)

// AuditRecorder é o que o auth precisa da auditoria.
type AuditRecorder interface {
	Record(ctx context.Context, p audit.Params) error
	TryRecord(ctx context.Context, p audit.Params)
}

// ServiceOptions reúne os parâmetros de comportamento.
type ServiceOptions struct {
	OTPTTL            time.Duration
	OTPMaxAttempts    int
	OTPResendInterval time.Duration
	// RefreshRetention é quanto tempo um refresh revogado fica no banco
	// depois de expirar — é o que permite detectar reúso tardio.
	RefreshRetention time.Duration
}

// MailLimiters são as cotas de ENVIO por endereço.
//
// Elas ficam no serviço, e não no handler, de propósito: o e-mail vem do
// corpo da requisição sem nenhuma prova de posse, então um teto que RECUSA a
// requisição vira lockout por procuração — qualquer um tranca o cadastro de
// um endereço alheio gastando a cota dele. Aqui o teto governa só a saída de
// mensagem; a resposta HTTP continua sendo o 202 do grupo A.
//
// Limitador nil = sem cota (é o caso da maior parte dos testes).
type MailLimiters struct {
	// VerificationMail limita os códigos de verificação disparados pelo
	// registro, por endereço.
	VerificationMail RateLimiter
	// AccountExistsNotice limita o aviso "alguém tentou criar conta com o
	// seu e-mail". Conteúdo fixo: repetir não informa nada novo.
	AccountExistsNotice RateLimiter
}

// Deps são as dependências do serviço.
type Deps struct {
	Users      user.Repository
	Households Households
	Tokens     RefreshTokenRepository
	Codes      VerificationCodeRepository
	Attempts   RegistrationAttemptRepository
	Audit      AuditRecorder
	UoW        UnitOfWork
	Hasher     *PasswordHasher
	Policy     *PasswordPolicy
	TokenSvc   *TokenService
	OTP        *OTP
	Mailer     Mailer
	Cookies    *CookieJar
	View       SessionView
	Logger     *slog.Logger

	// MailLimits são as cotas de envio por endereço (opcionais).
	MailLimits MailLimiters

	// Opcionais (teste).
	Clock Clock
	IDs   id.Generator
}

// Service implementa os fluxos de autenticação.
type Service struct {
	users      user.Repository
	households Households
	tokens     RefreshTokenRepository
	codes      VerificationCodeRepository
	attempts   RegistrationAttemptRepository
	audit      AuditRecorder
	uow        UnitOfWork
	hasher     *PasswordHasher
	policy     *PasswordPolicy
	tokenSvc   *TokenService
	otp        *OTP
	mailer     Mailer
	cookies    *CookieJar
	view       SessionView
	lg         *slog.Logger
	clock      Clock
	ids        id.Generator
	opts       ServiceOptions
	limits     MailLimiters
}

// NewService monta o serviço, validando que nenhuma dependência crítica veio
// nula — um nil aqui viraria panic no caminho de request.
func NewService(d Deps, opts ServiceOptions) (*Service, error) {
	switch {
	case d.Users == nil:
		return nil, errors.New("auth: repositório de usuários é obrigatório")
	case d.Households == nil:
		return nil, errors.New("auth: serviço de casas é obrigatório")
	case d.Tokens == nil:
		return nil, errors.New("auth: repositório de refresh tokens é obrigatório")
	case d.Codes == nil:
		return nil, errors.New("auth: repositório de códigos é obrigatório")
	case d.Attempts == nil:
		return nil, errors.New("auth: repositório de tentativas de cadastro é obrigatório")
	case d.Audit == nil:
		return nil, errors.New("auth: auditoria é obrigatória")
	case d.UoW == nil:
		return nil, errors.New("auth: unidade de trabalho é obrigatória")
	case d.Hasher == nil:
		return nil, errors.New("auth: hasher de senha é obrigatório")
	case d.Policy == nil:
		return nil, errors.New("auth: política de senha é obrigatória")
	case d.TokenSvc == nil:
		return nil, errors.New("auth: serviço de tokens é obrigatório")
	case d.OTP == nil:
		return nil, errors.New("auth: serviço de OTP é obrigatório")
	case d.Mailer == nil:
		return nil, errors.New("auth: mailer é obrigatório")
	case d.Cookies == nil:
		return nil, errors.New("auth: jar de cookies é obrigatório")
	case d.View == nil:
		return nil, errors.New("auth: montador de /me é obrigatório")
	case d.Logger == nil:
		return nil, errors.New("auth: logger é obrigatório")
	}

	if opts.OTPTTL <= 0 {
		opts.OTPTTL = 15 * time.Minute
	}
	if opts.OTPMaxAttempts <= 0 {
		opts.OTPMaxAttempts = 5
	}
	if opts.OTPResendInterval <= 0 {
		opts.OTPResendInterval = time.Minute
	}
	if opts.RefreshRetention <= 0 {
		opts.RefreshRetention = 30 * 24 * time.Hour
	}

	s := &Service{
		users:      d.Users,
		households: d.Households,
		tokens:     d.Tokens,
		codes:      d.Codes,
		attempts:   d.Attempts,
		audit:      d.Audit,
		uow:        d.UoW,
		hasher:     d.Hasher,
		policy:     d.Policy,
		tokenSvc:   d.TokenSvc,
		otp:        d.OTP,
		mailer:     d.Mailer,
		cookies:    d.Cookies,
		view:       d.View,
		lg:         d.Logger,
		clock:      d.Clock,
		ids:        d.IDs,
		opts:       opts,
		limits:     d.MailLimits,
	}
	if s.clock == nil {
		s.clock = func() time.Time { return time.Now().UTC() }
	}
	if s.ids == nil {
		s.ids = id.New
	}
	return s, nil
}

// Cookies expõe o jar (usado pelo handler e pelo wiring de rotas).
func (s *Service) Cookies() *CookieJar { return s.cookies }

// OTP expõe o serviço de código (usado para a chave de rate limit por conta).
func (s *Service) OTP() *OTP { return s.otp }

// Policy expõe a política de senha (validação na borda).
func (s *Service) Policy() *PasswordPolicy { return s.policy }

// CodeTTL devolve a validade do código, para o corpo de resposta 202.
func (s *Service) CodeTTL() time.Duration { return s.opts.OTPTTL }

// Session é o resultado de um login bem-sucedido.
type Session struct {
	AccessToken  string
	RefreshToken string
	View         *user.MeView
}

// LoginInput são os dados JÁ NORMALIZADOS pela borda.
type LoginInput struct {
	Email    string
	Password string
	IP       string
}

// Login autentica por e-mail e senha (§3.6 da spec 0001).
//
// Grupo E da §3.12: "e-mail não existe" e "senha errada" precisam devolver o
// mesmo status, o mesmo corpo E o mesmo custo de CPU. Por isso, quando o
// usuário não existe, ainda assim rodamos um Argon2id contra o hash-isca.
func (s *Service) Login(ctx context.Context, in LoginInput) (*Session, error) {
	now := s.clock()

	u, err := s.users.ByEmail(ctx, in.Email)
	if err != nil {
		if !errors.Is(err, user.ErrNotFound) {
			return nil, fmt.Errorf("buscando usuário no login: %w", err)
		}
		s.hasher.VerifyDummy(ctx, in.Password)
		s.audit.TryRecord(ctx, audit.Params{
			Action: audit.ActionLoginFailed,
			Entity: audit.EntityUser,
			IP:     in.IP,
		})
		return nil, ErrInvalidCredentials
	}

	if err := s.hasher.Verify(ctx, in.Password, u.PasswordHash); err != nil {
		if !errors.Is(err, ErrPasswordMismatch) && !errors.Is(err, ErrInvalidHash) {
			return nil, fmt.Errorf("verificando senha: %w", err)
		}
		s.audit.TryRecord(ctx, audit.Params{
			Action:   audit.ActionLoginFailed,
			Entity:   audit.EntityUser,
			EntityID: u.ID,
			UserID:   u.ID,
			IP:       in.IP,
		})
		return nil, ErrInvalidCredentials
	}

	// D4: o 403 só aparece DEPOIS de a senha conferir. Quem chega aqui já
	// provou saber a senha, então não há informação nova sendo revelada.
	if !u.Verified() {
		s.reissueVerificationCode(ctx, u, in.Password, in.IP)
		s.audit.TryRecord(ctx, audit.Params{
			Action:   audit.ActionLoginUnverified,
			Entity:   audit.EntityUser,
			EntityID: u.ID,
			UserID:   u.ID,
			IP:       in.IP,
		})
		return nil, ErrEmailNotVerified
	}

	var sess *Session
	err = s.uow.Do(ctx, func(ctx context.Context) error {
		// D2: auto-reparo. Garante o invariante "usuário verificado tem casa",
		// do qual a claim hid depende.
		hh, err := s.households.EnsureDefault(ctx, u.ID, u.Name)
		if err != nil {
			return fmt.Errorf("garantindo casa no login: %w", err)
		}
		sess, err = s.issueSession(ctx, u, hh, now)
		if err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Params{
			Action:      audit.ActionLogin,
			Entity:      audit.EntitySession,
			UserID:      u.ID,
			HouseholdID: hh.ID,
			IP:          in.IP,
		})
	})
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// Refresh rotaciona a sessão (§3.7 da spec 0001).
//
// Regras que este método implementa (docs/SEGURANCA.md §1 e risco 5 da §9):
//   - o refresh usado é SEMPRE invalidado (rotação obrigatória);
//   - reúso de um refresh já revogado derruba a FAMÍLIA inteira, porque
//     significa que alguém tem uma cópia — o legítimo ou o ladrão, e não dá
//     para saber qual;
//   - a família derrubada fica derrubada: o estado vive na tabela
//     refresh_families e é conferido ANTES de emitir e de novo ao gravar o
//     sucessor. Sem isso, o sucessor inserido logo depois da revogação
//     escapava dela e a sessão do ladrão sobrevivia à própria detecção do
//     roubo (achado ALTA-1 da revisão de segurança);
//   - o hid é REDERIVADO do banco, nunca copiado do token antigo.
func (s *Service) Refresh(ctx context.Context, rawRefresh, ip string) (*Session, error) {
	if rawRefresh == "" {
		return nil, ErrInvalidSession
	}
	now := s.clock()

	stored, err := s.tokens.ByHash(ctx, HashRefreshToken(rawRefresh))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.audit.TryRecord(ctx, audit.Params{
				Action: audit.ActionRefreshRejected,
				Entity: audit.EntitySession,
				IP:     ip,
			})
			return nil, ErrInvalidSession
		}
		return nil, fmt.Errorf("buscando refresh token: %w", err)
	}

	if stored.RevokedAt != nil {
		s.handleRefreshReuse(ctx, stored, ip)
		return nil, ErrInvalidSession
	}

	// CHECAGEM POSITIVA DA FAMÍLIA (achado ALTA-1).
	//
	// O token só vale se a SESSÃO dele ainda estiver viva. É isto que torna
	// inofensivo o sucessor que uma rotação concorrente conseguiu inserir
	// DEPOIS de a família ter sido derrubada: o elo existe e não está
	// revogado, mas a família está — e quem manda é a família.
	if err := s.ensureFamilyActive(ctx, stored.FamilyID, stored.UserID); err != nil {
		if !errors.Is(err, errFamilyRevoked) {
			return nil, err
		}
		// Varre os elos retardatários; o carimbo da revogação original é
		// preservado pelo repositório.
		if _, err := s.tokens.RevokeFamily(ctx, stored.FamilyID, now); err != nil {
			s.lg.ErrorContext(ctx, "falha ao varrer família já revogada",
				slog.String("user_id", stored.UserID),
				slog.String("reason", err.Error()),
			)
		}
		s.audit.TryRecord(ctx, audit.Params{
			Action:   audit.ActionRefreshRejected,
			Entity:   audit.EntitySession,
			EntityID: stored.FamilyID,
			UserID:   stored.UserID,
			IP:       ip,
		})
		return nil, ErrInvalidSession
	}

	if !now.Before(stored.ExpiresAt) {
		return nil, ErrInvalidSession
	}

	u, err := s.users.ByID(ctx, stored.UserID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			_, _ = s.tokens.RevokeFamily(ctx, stored.FamilyID, now)
			return nil, ErrInvalidSession
		}
		return nil, fmt.Errorf("carregando usuário do refresh: %w", err)
	}
	if !u.Verified() {
		_, _ = s.tokens.RevokeFamily(ctx, stored.FamilyID, now)
		return nil, ErrInvalidSession
	}

	hh, err := s.households.Active(ctx, u.ID)
	if err != nil {
		if errors.Is(err, household.ErrNotFound) {
			_, _ = s.tokens.RevokeFamily(ctx, stored.FamilyID, now)
			return nil, ErrInvalidSession
		}
		return nil, fmt.Errorf("carregando casa ativa: %w", err)
	}

	newID := s.ids()

	// A rotação roda FORA de transação de propósito: quando ela não afeta
	// linha nenhuma, precisamos gravar a revogação da família e a auditoria
	// e AINDA ASSIM devolver erro. Dentro de uma transação, esse rollback
	// apagaria justamente a evidência do roubo.
	rotated, err := s.tokens.Rotate(ctx, stored.ID, newID, now)
	if err != nil {
		return nil, fmt.Errorf("rotacionando refresh: %w", err)
	}
	if !rotated {
		// Outra requisição rotacionou entre o SELECT e o UPDATE: mesmo
		// sintoma do reúso, mesmo tratamento.
		s.handleRefreshReuse(ctx, stored, ip)
		return nil, ErrInvalidSession
	}

	plain, hash, err := s.tokenSvc.NewRefreshToken()
	if err != nil {
		return nil, err
	}

	ident := session.Identity{
		UserID:      u.ID,
		HouseholdID: hh.ID,
		Role:        hh.Role,
		SessionID:   stored.FamilyID,
	}
	access, _, err := s.tokenSvc.IssueAccessToken(ident)
	if err != nil {
		return nil, err
	}

	err = s.uow.Do(ctx, func(ctx context.Context) error {
		// Reconferência ANTES do insert: a família pode ter caído entre a
		// checagem lá em cima e agora — a janela é curta, mas é justamente
		// nela que o reúso concorrente acontece.
		if err := s.ensureFamilyActive(ctx, stored.FamilyID, stored.UserID); err != nil {
			return err
		}
		if err := s.tokens.Create(ctx, &RefreshToken{
			ID:        newID,
			UserID:    u.ID,
			FamilyID:  stored.FamilyID,
			TokenHash: hash,
			ExpiresAt: now.Add(s.tokenSvc.RefreshTTL()),
			CreatedAt: now,
			UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("gravando novo refresh: %w", err)
		}
		// E DEPOIS do insert. ATENÇÃO AO ALCANCE REAL desta segunda leitura:
		// ela só enxerga uma revogação commitada no meio da transação sob
		// READ COMMITTED (padrão do PostgreSQL e do SQL Server). No MySQL/
		// InnoDB, que é REPEATABLE READ por padrão, e no SQLite em WAL, a
		// transação lê do mesmo snapshot e esta checagem repete o resultado
		// da anterior.
		//
		// Ou seja: ela é oportunista, não é a garantia. A garantia em todos
		// os dialetos é a checagem de ENTRADA do Refresh, que roda fora de
		// transação e portanto sempre lê o estado atual — um sucessor
		// commitado numa família morta existe, mas não é utilizável. O que
		// esta leitura evita, onde funciona, é devolver 200 e um access de
		// 15 minutos para uma sessão que já morreu.
		if err := s.ensureFamilyActive(ctx, stored.FamilyID, stored.UserID); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Params{
			Action:      audit.ActionRefresh,
			Entity:      audit.EntitySession,
			UserID:      u.ID,
			HouseholdID: hh.ID,
			IP:          ip,
		})
	})
	if err != nil {
		if errors.Is(err, errFamilyRevoked) {
			// A sessão morreu no meio da rotação. O sucessor foi desfeito
			// pelo rollback; ainda assim varremos a família, porque outra
			// rotação simultânea pode ter conseguido gravar a dela.
			s.lg.WarnContext(ctx, "família revogada durante a rotação; sucessor descartado",
				slog.String("user_id", u.ID),
			)
			if _, err := s.tokens.RevokeFamily(ctx, stored.FamilyID, now); err != nil {
				s.lg.ErrorContext(ctx, "falha ao varrer família revogada na rotação",
					slog.String("user_id", u.ID),
					slog.String("reason", err.Error()),
				)
			}
			return nil, ErrInvalidSession
		}
		return nil, err
	}

	view, err := s.view.Me(ctx, ident)
	if err != nil {
		return nil, fmt.Errorf("montando sessão: %w", err)
	}
	return &Session{AccessToken: access, RefreshToken: plain, View: view}, nil
}

// errFamilyRevoked é interno: sinaliza que a sessão morreu no meio da
// rotação. Nunca sai do pacote — vira ErrInvalidSession (grupo F da §3.12).
var errFamilyRevoked = errors.New("família de refresh revogada")

// ensureFamilyActive devolve errFamilyRevoked se a sessão já morreu.
//
// Família inexistente também é "morta": falhamos FECHADO. É o caso de um
// refresh emitido antes desta tabela existir, ou de um estado impossível —
// nos dois, negar é a resposta certa.
//
// O dono é conferido junto (ADR-013: sem FK física, a integridade é 100%
// código). Uma família apontada por um token de outro usuário ficaria imune
// ao RevokeAllForUser da troca de senha do dono real.
func (s *Service) ensureFamilyActive(ctx context.Context, familyID, userID string) error {
	fam, err := s.tokens.FamilyByID(ctx, familyID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return errFamilyRevoked
		}
		return fmt.Errorf("carregando família de refresh: %w", err)
	}
	if !fam.Active() {
		return errFamilyRevoked
	}
	if fam.UserID != userID {
		s.lg.ErrorContext(ctx, "família de refresh com dono divergente",
			slog.String("family_id", familyID),
		)
		return errFamilyRevoked
	}
	return nil
}

// handleRefreshReuse derruba a família e registra o evento.
//
// Critério de aceite 19 da spec 0001: a ação gravada é exatamente
// "auth.refresh_reuse_detected".
func (s *Service) handleRefreshReuse(ctx context.Context, stored *RefreshToken, ip string) {
	now := s.clock()
	revoked, err := s.tokens.RevokeFamily(ctx, stored.FamilyID, now)
	if err != nil {
		s.lg.ErrorContext(ctx, "falha ao revogar família de refresh",
			slog.String("user_id", stored.UserID),
			slog.String("reason", err.Error()),
		)
	}
	s.lg.WarnContext(ctx, "reúso de refresh token detectado",
		slog.String("user_id", stored.UserID),
		slog.Int64("revogados", revoked),
	)
	s.audit.TryRecord(ctx, audit.Params{
		Action:   audit.ActionRefreshReuseDetected,
		Entity:   audit.EntitySession,
		EntityID: stored.FamilyID,
		UserID:   stored.UserID,
		IP:       ip,
	})
}

// Logout encerra a sessão. É IDEMPOTENTE: sem cookie, com cookie inválido ou
// com cookie já revogado, o resultado é o mesmo (§3.8).
func (s *Service) Logout(ctx context.Context, rawRefresh, ip string) {
	if rawRefresh == "" {
		return
	}
	now := s.clock()

	stored, err := s.tokens.ByHash(ctx, HashRefreshToken(rawRefresh))
	if err != nil {
		return
	}
	if _, err := s.tokens.RevokeFamily(ctx, stored.FamilyID, now); err != nil {
		s.lg.ErrorContext(ctx, "falha ao revogar família no logout",
			slog.String("user_id", stored.UserID),
			slog.String("reason", err.Error()),
		)
		return
	}
	s.audit.TryRecord(ctx, audit.Params{
		Action:   audit.ActionLogout,
		Entity:   audit.EntitySession,
		EntityID: stored.FamilyID,
		UserID:   stored.UserID,
		IP:       ip,
	})
}

// issueSession cria o refresh e assina o access. Deve rodar dentro da
// transação do chamador quando faz parte de uma escrita maior.
func (s *Service) issueSession(ctx context.Context, u *user.User, hh household.Summary, now time.Time) (*Session, error) {
	familyID := s.ids()

	plain, hash, err := s.tokenSvc.NewRefreshToken()
	if err != nil {
		return nil, err
	}

	// A família nasce ANTES do primeiro elo, na mesma transação: é o
	// registro que o Refresh vai consultar em toda rotação futura.
	if err := s.tokens.CreateFamily(ctx, &RefreshFamily{
		ID:        familyID,
		UserID:    u.ID,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("abrindo família de refresh: %w", err)
	}

	if err := s.tokens.Create(ctx, &RefreshToken{
		ID:        s.ids(),
		UserID:    u.ID,
		FamilyID:  familyID,
		TokenHash: hash,
		ExpiresAt: now.Add(s.tokenSvc.RefreshTTL()),
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return nil, fmt.Errorf("gravando refresh token: %w", err)
	}

	ident := session.Identity{
		UserID:      u.ID,
		HouseholdID: hh.ID,
		Role:        hh.Role,
		SessionID:   familyID,
	}
	access, _, err := s.tokenSvc.IssueAccessToken(ident)
	if err != nil {
		return nil, err
	}

	view, err := s.view.Me(ctx, ident)
	if err != nil {
		return nil, fmt.Errorf("montando sessão: %w", err)
	}
	return &Session{AccessToken: access, RefreshToken: plain, View: view}, nil
}

// PurgeExpired é o expurgo periódico do janitor. O primeiro contador soma
// códigos de verificação e tentativas de cadastro; o segundo, tokens e
// famílias de refresh mortas.
func (s *Service) PurgeExpired(ctx context.Context) (codes, tokens int64, err error) {
	now := s.clock()

	codes, err = s.codes.DeleteExpired(ctx, now)
	if err != nil {
		return 0, 0, fmt.Errorf("expurgando códigos: %w", err)
	}

	// As tentativas de cadastro ganham uma carência de um TTL depois de
	// expirar: é a janela em que quem queimou as 5 tentativas de um código
	// ainda consegue pedir outro apresentando o token (ResendCode). Sem ela,
	// errar o código cinco vezes viraria bloqueio do cadastro.
	tentativas, err := s.attempts.DeleteExpired(ctx, now.Add(-s.opts.OTPTTL))
	if err != nil {
		return codes, 0, fmt.Errorf("expurgando tentativas de cadastro: %w", err)
	}
	codes += tentativas

	cutoff := now.Add(-s.opts.RefreshRetention)

	tokens, err = s.tokens.DeleteExpired(ctx, cutoff)
	if err != nil {
		return codes, 0, fmt.Errorf("expurgando refresh tokens: %w", err)
	}

	// As famílias que ficaram sem nenhum elo vão junto: senão a tabela de
	// sessões só cresce.
	familias, err := s.tokens.DeleteDeadFamilies(ctx, cutoff)
	if err != nil {
		return codes, tokens, fmt.Errorf("expurgando famílias de refresh: %w", err)
	}
	return codes, tokens + familias, nil
}
