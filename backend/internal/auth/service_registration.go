package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/user"
)

// RegisterInput são os dados já normalizados e validados pela borda.
type RegisterInput struct {
	Name     string
	Email    string
	Password string
	IP       string
}

// Register cadastra um usuário NÃO verificado (§3.3, ADR-009 e ADR-012) e
// devolve o registrationToken da tentativa criada.
//
// Grupo A da §3.12 — os três caminhos abaixo devolvem 202 com o mesmo corpo:
//
//	e-mail novo              -> cria a conta pendente + a 1ª tentativa
//	e-mail já verificado      -> não cria nada + envia aviso "alguém tentou"
//	e-mail ainda não          -> cria MAIS UMA tentativa, independente das
//	 verificado                  outras, com as credenciais deste pedido
//
// O registrationToken pode voltar nos TRÊS caminhos sem vazar nada: ele é
// sorteado no servidor com crypto/rand antes de qualquer consulta e não
// depende do estado da conta. Nos caminhos que não abrem tentativa ele é um
// token que não casa com linha nenhuma — indistinguível de fora.
//
// POR QUE CADA PEDIDO ABRE A SUA PRÓPRIA TENTATIVA (achado ALTA de
// pre-hijacking): ver o comentário de RegistrationAttempt. Em resumo, o
// código que chega na caixa do endereço passa a estar amarrado ao segredo de
// quem escolheu a senha, e não mais ao endereço. O código do atacante chega
// à caixa da vítima, mas é inútil na mão dela — ela não tem o token dele; e o
// atacante tem o token dele, mas nunca vê o código.
//
// Nenhum pedido destrói a tentativa alheia: é isso que impede que a defesa
// contra o pre-hijack vire negação permanente de cadastro.
//
// CUSTO UNIFORME: um Hash e um VerifyDummy em TODOS os caminhos, antes de
// saber se o e-mail existe. Sem isso o tempo de resposta separaria "conta
// verificada" de "conta pendente" e de "e-mail novo".
func (s *Service) Register(ctx context.Context, in RegisterInput) (string, error) {
	now := s.clock()

	token, tokenHash, err := NewRegistrationToken()
	if err != nil {
		return "", err
	}

	passwordHash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return "", fmt.Errorf("gerando hash de senha: %w", err)
	}
	s.hasher.VerifyDummy(ctx, in.Password)

	existing, err := s.users.ByEmail(ctx, in.Email)
	switch {
	case err == nil && existing.Verified():
		s.audit.TryRecord(ctx, audit.Params{
			Action:   audit.ActionRegisterExisting,
			Entity:   audit.EntityUser,
			EntityID: existing.ID,
			UserID:   existing.ID,
			IP:       in.IP,
		})
		// O aviso vai com o nome da conta VERIFICADA (o dono provou posse da
		// caixa), nunca com o nome que veio nesta requisição.
		//
		// Cota PRÓPRIA e longa (1 por dia por endereço): o conteúdo é fixo,
		// então da segunda mensagem em diante não há informação nova para o
		// titular — só barulho que um terceiro escolhe mandar.
		if s.allowMail(s.limits.AccountExistsNotice, ScopeAccountExists, existing.Email) {
			s.mailer.EnqueueAccountExistsNotice(existing.Email, existing.Name)
		}
		return token, nil

	case err == nil:
		if err := s.openAttempt(ctx, in, passwordHash, tokenHash, existing.ID, now); err != nil {
			return "", err
		}
		return token, nil

	case errors.Is(err, user.ErrNotFound):
		userID, ok, err := s.claimPendingAccount(ctx, in, passwordHash, now)
		if err != nil {
			return "", err
		}
		if !ok {
			// A conta virou VERIFICADA entre o SELECT e o INSERT. Não há
			// cadastro pendente a abrir; a resposta é a mesma de qualquer
			// jeito, então nada mais acontece.
			return token, nil
		}
		if err := s.openAttempt(ctx, in, passwordHash, tokenHash, userID, now); err != nil {
			return "", err
		}
		return token, nil

	default:
		return "", fmt.Errorf("buscando usuário no registro: %w", err)
	}
}

// claimPendingAccount cria a linha de users que RESERVA o endereço.
//
// Essa linha não é mais a dona das credenciais do cadastro — quem carrega as
// credenciais é a tentativa. Ela existe para (a) garantir a unicidade do
// e-mail e (b) dar ao login não verificado uma senha contra a qual conferir,
// que é o que sustenta o 403 EMAIL_NOT_VERIFIED da §3.12 sem consulta extra.
//
// O hash gravado aqui NUNCA ativa conta sozinho: a ativação sempre grava as
// credenciais da tentativa que recebeu o código (user.ActivatePending).
func (s *Service) claimPendingAccount(ctx context.Context, in RegisterInput, passwordHash string, now time.Time) (string, bool, error) {
	u := &user.User{
		ID:           s.ids(),
		Email:        in.Email,
		PasswordHash: passwordHash,
		Name:         in.Name,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	err := s.users.Create(ctx, u)
	switch {
	case err == nil:
		return u.ID, true, nil

	case errors.Is(err, user.ErrEmailTaken):
		// Corrida: outra requisição criou o mesmo e-mail entre o SELECT e o
		// INSERT. Recarrega e segue como cadastro pendente — informar seria
		// vazar existência.
		again, err := s.users.ByEmail(ctx, in.Email)
		if err != nil {
			if errors.Is(err, user.ErrNotFound) {
				return "", false, nil
			}
			return "", false, fmt.Errorf("recarregando usuário após corrida de registro: %w", err)
		}
		if again.Verified() {
			return "", false, nil
		}
		return again.ID, true, nil

	default:
		return "", false, fmt.Errorf("criando usuário pendente: %w", err)
	}
}

// openAttempt abre UMA tentativa de cadastro e, se o envio estiver liberado,
// entrega o código.
//
// A tentativa é gravada MESMO quando o envio está barrado pelo cooldown ou
// pela cota do endereço, e isso é deliberado: quem pediu o cadastro fica com
// um token amarrado às credenciais DELE e consegue pedir o código mais tarde
// (ResendCode com o token). O contrário — não gravar — devolveria a um
// terceiro o poder de impedir que o dono do endereço se cadastre, gastando a
// cota alheia.
//
// Quando nada é enviado, CodeIssuedAt fica NULO — e isso tem DOIS efeitos,
// ambos deliberados:
//
//  1. o cooldown não é empurrado para frente. É a defesa contra o laço
//     "atacante registra em looping": se toda tentativa contasse como emissão,
//     ninguém receberia mensagem nunca mais;
//  2. o código gravado NÃO é validável (achado ALTA-2 da revisão final).
//     CodeIssuedAt nulo significa "nenhuma mensagem saiu", e LiveByToken mais
//     consumeAttempt recusam a tentativa nesse estado. Sem isso, cada
//     POST /auth/register criava um código novo e chutável sem gastar
//     mensagem, e a cota de mensagens/h por endereço deixava de limitar
//     quantos códigos existem para adivinhar — 5 palpites por cadastro × 5
//     cadastros/h por IP, escalando com o número de IPs.
//
// O código só passa a valer quando a mensagem efetivamente sai: aqui com
// envioLiberado, ou depois, pelo reenvio COM o token (RotateCode), que gasta
// a cota e volta a ser contabilizado por ela.
//
// NULO, e não data mágica: gravar o zero de time.Time fazia o driver do MySQL
// mandar '0000-00-00 00:00:00', que o sql_mode padrão recusa (erro 1292). Lá,
// a tentativa não era gravada, este método devolvia erro e o handler
// respondia 500 — exatamente nos caminhos "e-mail livre" e "e-mail pendente",
// enquanto "e-mail já verificado" (que nem chega aqui) seguia com 202. Dois
// pedidos separavam os casos, e o grupo A da §3.12 caía junto com o
// invariante do ADR-014.
func (s *Service) openAttempt(ctx context.Context, in RegisterInput, passwordHash, tokenHash, userID string, now time.Time) error {
	envioLiberado, err := s.canSendVerificationMail(ctx, in.Email, now)
	if err != nil {
		s.lg.ErrorContext(ctx, "falha ao checar janela de envio do registro", slog.String("reason", err.Error()))
		envioLiberado = false
	}

	code, err := s.otp.GenerateCode()
	if err != nil {
		return err
	}

	att := &RegistrationAttempt{
		ID:           s.ids(),
		Email:        in.Email,
		UserID:       userID,
		Name:         in.Name,
		PasswordHash: passwordHash,
		TokenHash:    tokenHash,
		CodeHash:     s.otp.Hash(PurposeEmailVerification, in.Email, code),
		ExpiresAt:    now.Add(s.opts.OTPTTL),
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if envioLiberado {
		emitido := now
		att.CodeIssuedAt = &emitido
	}

	if err := s.uow.Do(ctx, func(ctx context.Context) error {
		if err := s.attempts.Create(ctx, att); err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Params{
			Action:   audit.ActionRegister,
			Entity:   audit.EntityUser,
			EntityID: userID,
			UserID:   userID,
			IP:       in.IP,
		})
	}); err != nil {
		return err
	}

	// E-mail só depois do COMMIT: mensagem de código que não existe é pior
	// do que atraso de mensagem.
	//
	// O nome NÃO viaja (achado ALTA-2): esta é a mensagem de PRIMEIRO
	// CONTATO, para um endereço que ninguém provou possuir, com texto que
	// quem chamou o endpoint escolheu.
	if envioLiberado {
		s.mailer.EnqueueVerificationCode(in.Email, code, s.opts.OTPTTL)
	}
	return nil
}

// canSendVerificationMail junta os DOIS freios de envio POR ENDEREÇO: o
// cooldown entre códigos e a cota horária.
//
// Este é o ÚNICO ponto por onde uma mensagem de verificação pode sair — o
// registro, o reenvio e a reemissão do login não verificado passam todos por
// aqui, com o MESMO escopo de chave (ScopeRegister). Um teto por endpoint
// seria contornável trocando de rota: 59 reenvios espaçados de 61 s davam 59
// mensagens em uma hora quando só o cooldown era consultado (achado B da
// re-revisão).
//
// Nenhum dos dois freios recusa a REQUISIÇÃO. Recusar daria a um terceiro o
// poder de trancar o cadastro de um endereço que ele não possui — o e-mail
// vem do corpo, sem nenhuma prova de posse.
func (s *Service) canSendVerificationMail(ctx context.Context, email string, now time.Time) (bool, error) {
	last, ok, err := s.attempts.LastCodeIssuedAt(ctx, email)
	if err != nil {
		return false, fmt.Errorf("buscando última emissão de código: %w", err)
	}
	// ok=false é "nunca saiu mensagem para este endereço" — o repositório já
	// ignora tentativa sem emissão. O !last.IsZero() fica como rede: um
	// repositório que um dia devolvesse ok com data zerada não pode
	// congelar o cooldown de quem nunca recebeu nada.
	if ok && !last.IsZero() && now.Sub(last) < s.opts.OTPResendInterval {
		return false, nil
	}
	return s.allowMail(s.limits.VerificationMail, ScopeRegister, email), nil
}

// allowMail consulta a cota por endereço. Limitador nil = sem cota.
//
// A chave é o HMAC do e-mail com o pepper, nunca o e-mail em claro — mesma
// convenção da §7 da spec 0001.
func (s *Service) allowMail(lim RateLimiter, scope, email string) bool {
	if lim == nil {
		return true
	}
	ok, _ := lim.Allow(s.otp.AccountKey(scope, email))
	return ok
}

// ResendCode reemite o código de confirmação (§3.5) e devolve o
// registrationToken que vale dali em diante.
//
// Grupo B da §3.12: e-mail existente, inexistente, conta já verificada,
// pedido dentro do cooldown e pedido SEM token produzem exatamente a mesma
// resposta — inclusive no tamanho do token, que é sempre 64 hexadecimais.
//
// O TOKEN É OBRIGATÓRIO PARA QUE QUALQUER COISA ACONTEÇA (achado ALTA-1 da
// revisão final). Existe um único caminho: quem apresenta o token é o dono
// da tentativa, e o reenvio rotaciona o código DAQUELA tentativa, mantendo as
// credenciais e o próprio token. Vale inclusive para tentativa já queimada
// pelas 5 tentativas erradas — errar o código cinco vezes não pode trancar o
// cadastro para sempre.
//
// POR QUE NÃO EXISTE CAMINHO SEM TOKEN. O pedido de reenvio carrega apenas um
// e-mail, e um e-mail não prova nada; principalmente, ele NÃO TRAZ
// CREDENCIAIS. Qualquer código emitido para quem não apresentou o token teria
// de herdar as credenciais de alguma tentativa que já existe — e herdar as de
// outra pessoa é exatamente a tomada de conta que o ADR-014 fecha.
//
// A regra "existe exatamente UMA tentativa viva, logo ela é de quem está
// pedindo" foi removida por ser FALSA, e o atacante controla sozinho as
// condições dela: basta a tentativa da vítima estar morta (expirada) e a dele
// viva para que a vítima, ao pedir reenvio sem token, receba um código que
// ativa a SENHA DELE. Não há conserto dentro desse caminho.
//
// Quem perdeu o token refaz o cadastro: POST /auth/register sempre funciona,
// nunca é bloqueado por terceiro e sempre amarra o token novo às credenciais
// de quem pediu.
func (s *Service) ResendCode(ctx context.Context, email, rawToken, ip string) (string, error) {
	now := s.clock()

	// A consulta acontece ANTES da checagem do token e em todos os caminhos,
	// de propósito: é o que mantém a forma e o custo do reenvio iguais para
	// e-mail que existe e e-mail que não existe (grupo B da §3.12).
	u, err := s.users.ByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			// D5: NÃO criamos linha de tentativa para e-mail inexistente —
			// uma "linha-isca" por requisição seria um jeito de inflar a
			// tabela sem custo para o atacante.
			return s.fallbackToken(rawToken)
		}
		return "", fmt.Errorf("buscando usuário no reenvio: %w", err)
	}
	if u.Verified() {
		// Conta já ativa: nada a confirmar. O cliente recebe o mesmo 202.
		return s.fallbackToken(rawToken)
	}

	// Sem token bem formado não há tentativa a identificar. A resposta é
	// idêntica; simplesmente NADA é emitido.
	if !ValidRegistrationTokenFormat(rawToken) {
		return s.fallbackToken(rawToken)
	}

	att, err := s.attempts.ByToken(ctx, email, HashRegistrationToken(rawToken))
	switch {
	case err == nil:
		s.rotateAttemptCode(ctx, att, ip, now)
		return rawToken, nil
	case errors.Is(err, ErrNotFound):
		// Token bem formado que não casa com tentativa nenhuma: nada a
		// reemitir e nada a revelar.
		return s.fallbackToken(rawToken)
	default:
		return "", fmt.Errorf("buscando tentativa no reenvio: %w", err)
	}
}

// fallbackToken devolve o token que o cliente deve guardar quando o reenvio
// não emitiu nada.
//
// Devolver o MESMO token que veio na requisição, quando ele tem forma válida,
// evita um tiro no pé de usabilidade: clicar "reenviar" duas vezes dentro do
// cooldown não pode fazer o cliente perder o token bom que ele já tinha. Não
// há vazamento — é o segredo do próprio chamador voltando para ele, e o
// tamanho da resposta não muda.
func (s *Service) fallbackToken(rawToken string) (string, error) {
	if ValidRegistrationTokenFormat(rawToken) {
		return rawToken, nil
	}
	token, _, err := NewRegistrationToken()
	if err != nil {
		return "", err
	}
	return token, nil
}

// errContaJaVerificada aborta a rotação quando a conta foi confirmada entre
// a leitura de users e a escrita. É corrida BENIGNA e esperada: não vira log
// de erro nem muda a resposta HTTP.
var errContaJaVerificada = errors.New("conta verificada durante a rotação")

// rotateAttemptCode troca o código de uma tentativa existente, preservando o
// token e as credenciais. Erros viram log: este caminho nunca pode mudar a
// resposta HTTP, senão vira oráculo de existência de conta.
//
// RESSURREIÇÃO POR CORRIDA (achado BAIXA-3 da revisão final): quem chama
// checou `Verified()` fora da transação, e VerifyEmail pode confirmar a conta
// (e consumir todas as tentativas) na janela até o UPDATE. Como RotateCode
// ressuscita tentativa consumida de propósito, o token antigo reanimaria uma
// tentativa que a verificação já matou — e-mail extra na caixa de quem acabou
// de confirmar, mais uma linha órfã. Por isso o estado da conta é reconferido
// DENTRO da uow.Do, junto com a escrita.
func (s *Service) rotateAttemptCode(ctx context.Context, att *RegistrationAttempt, ip string, now time.Time) {
	envioLiberado, err := s.canSendVerificationMail(ctx, att.Email, now)
	if err != nil {
		s.lg.ErrorContext(ctx, "falha ao checar janela de envio do reenvio", slog.String("reason", err.Error()))
		return
	}
	if !envioLiberado {
		// Nada muda e nada sai. O código que já está na caixa continua
		// valendo — matá-lo aqui seria trocar mail bombing por negação de
		// cadastro.
		return
	}

	code, err := s.otp.GenerateCode()
	if err != nil {
		s.lg.ErrorContext(ctx, "falha ao gerar código de verificação", slog.String("reason", err.Error()))
		return
	}

	err = s.uow.Do(ctx, func(ctx context.Context) error {
		// Reconferência DENTRO da transação: conta já verificada não tem
		// tentativa a ressuscitar.
		u, err := s.users.ByEmail(ctx, att.Email)
		if err != nil {
			if errors.Is(err, user.ErrNotFound) {
				return errContaJaVerificada
			}
			return fmt.Errorf("reconferindo conta antes da rotação: %w", err)
		}
		if u.Verified() {
			return errContaJaVerificada
		}

		ok, err := s.attempts.RotateCode(ctx, att.ID,
			s.otp.Hash(PurposeEmailVerification, att.Email, code), now, now.Add(s.opts.OTPTTL))
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		return s.audit.Record(ctx, audit.Params{
			Action:   audit.ActionCodeIssued,
			Entity:   audit.EntityUser,
			EntityID: att.UserID,
			UserID:   att.UserID,
			IP:       ip,
		})
	})
	switch {
	case errors.Is(err, errContaJaVerificada):
		// Corrida benigna: a conta foi confirmada antes da escrita. Nada
		// mudou no banco e nada sai por e-mail.
		return
	case err != nil:
		s.lg.ErrorContext(ctx, "falha ao rotacionar código da tentativa",
			slog.String("user_id", att.UserID),
			slog.String("reason", err.Error()),
		)
		return
	}
	s.mailer.EnqueueVerificationCode(att.Email, code, s.opts.OTPTTL)
}

// reissueVerificationCode reemite o código no login de conta NÃO verificada
// (D4 da §3.12).
//
// DUAS condições, e as duas existem pela mesma razão — o código reemitido só
// pode pertencer a quem está entrando:
//
//  1. o endereço tem EXATAMENTE UMA tentativa viva. Com o endereço disputado
//     não há como escolher, e rotacionar a errada só queimaria o código de
//     outra pessoa. "Viva" aqui é VALIDÁVEL — com código emitido (BAIXA-1):
//     tentativa que nunca recebeu mensagem não serve para ninguém validar
//     nada, e contá-la deixava um terceiro desligar este caminho de vez, um
//     `register` a cada ~14 min;
//  2. a senha que acabou de ser provada é a MESMA que criou aquela tentativa
//     (achado BAIXA-3). A linha de users guarda o hash de quem cadastrou
//     PRIMEIRO, que pode ser um terceiro; sem esta checagem, esse terceiro
//     entrava com a senha dele e rotacionava a tentativa da vítima, matando o
//     código que ela tinha na mão e gastando a cota de mensagens dela.
//
// Quem cair fora dessas condições usa /auth/resend-code com o seu token, ou
// refaz o cadastro.
//
// A senha em claro chega aqui só para o Verify e nunca é guardada nem logada.
func (s *Service) reissueVerificationCode(ctx context.Context, u *user.User, password, ip string) {
	now := s.clock()

	live, err := s.attempts.LiveByEmail(ctx, u.Email, now, 2)
	if err != nil {
		s.lg.ErrorContext(ctx, "falha ao listar tentativas no login não verificado",
			slog.String("user_id", u.ID),
			slog.String("reason", err.Error()),
		)
		s.hasher.VerifyDummy(ctx, password)
		return
	}
	if len(live) != 1 {
		// CUSTO UNIFORME: o Verify abaixo é um Argon2id, e ele só acontece
		// quando existe exatamente uma tentativa viva. Sem esta isca, o tempo
		// do 403 contaria para quem já sabe a senha quantas tentativas vivas o
		// endereço tem — isto é, se mais alguém se cadastrou nele.
		s.hasher.VerifyDummy(ctx, password)
		return
	}

	if err := s.hasher.Verify(ctx, password, live[0].PasswordHash); err != nil {
		if !errors.Is(err, ErrPasswordMismatch) && !errors.Is(err, ErrInvalidHash) {
			s.lg.ErrorContext(ctx, "falha ao conferir credenciais da tentativa no login não verificado",
				slog.String("user_id", u.ID),
				slog.String("reason", err.Error()),
			)
		}
		// Tentativa viva de OUTRA pessoa. Nada é rotacionado e nada sai: o
		// código que já está na caixa continua valendo para quem o pediu.
		return
	}

	s.rotateAttemptCode(ctx, live[0], ip, now)
}

// VerifyEmailInput são os dados normalizados pela borda.
type VerifyEmailInput struct {
	Email string
	Code  string
	Token string
	IP    string
}

// VerifyEmail confirma o e-mail e já autentica (§3.4).
//
// A busca do código é ESCOPADA PELO TOKEN: um código que não pertence àquela
// tentativa simplesmente não existe para efeito de validação. É isso que faz
// o código do atacante, entregue na caixa da vítima, ser inútil na mão dela.
//
// A ativação grava as credenciais DA TENTATIVA dona do código — nunca as que
// estavam na linha de users, que podem ser de outra pessoa — e destrói todas
// as demais tentativas pendentes daquele endereço.
//
// ADR-012: é AQUI que a casa nasce — "Casa de {primeiro nome}" e a membership
// de owner são criadas na MESMA transação que grava email_verified_at. Spam
// de cadastro não deixa casa órfã, e o invariante "todo usuário verificado
// tem casa" passa a valer no instante da verificação.
func (s *Service) VerifyEmail(ctx context.Context, in VerifyEmailInput) (*Session, error) {
	now := s.clock()

	// A validação roda FORA da transação de propósito: o incremento de
	// tentativas precisa PERSISTIR mesmo quando a requisição termina em erro.
	// Dentro da transação, o rollback do caminho de falha zeraria o contador
	// e o limite de 5 tentativas viraria ficção.
	att, err := s.consumeAttempt(ctx, in, now)
	if err != nil {
		return nil, err
	}

	u, err := s.users.ByEmail(ctx, att.Email)
	if err != nil {
		// Tentativa válida para e-mail sem usuário: estado impossível pelo
		// fluxo normal. Responde como código inválido.
		return nil, ErrInvalidCode
	}

	var sess *Session
	err = s.uow.Do(ctx, func(ctx context.Context) error {
		ativou, err := s.users.ActivatePending(ctx, u.ID, att.Name, att.PasswordHash, now)
		if err != nil {
			return fmt.Errorf("ativando conta pendente: %w", err)
		}
		if !ativou {
			// A conta já estava verificada. Uma tentativa que sobreviveu a
			// isso não pode abrir sessão nenhuma.
			return ErrInvalidCode
		}
		u.Name = att.Name
		u.PasswordHash = att.PasswordHash
		u.EmailVerifiedAt = &now

		// Confirmado o e-mail, NENHUMA outra tentativa daquele endereço pode
		// continuar de pé — nem a de quem quer que tenha tentado se
		// antecipar.
		if _, err := s.attempts.ConsumeAllForEmail(ctx, att.Email, now); err != nil {
			return fmt.Errorf("invalidando tentativas restantes: %w", err)
		}

		hh, err := s.households.EnsureDefault(ctx, u.ID, u.Name)
		if err != nil {
			return fmt.Errorf("criando casa padrão: %w", err)
		}

		if err := s.audit.Record(ctx, audit.Params{
			Action:      audit.ActionEmailVerified,
			Entity:      audit.EntityUser,
			EntityID:    u.ID,
			UserID:      u.ID,
			HouseholdID: hh.ID,
			IP:          in.IP,
		}); err != nil {
			return err
		}

		sess, err = s.issueSession(ctx, u, hh, now)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrInvalidCode) {
			return nil, ErrInvalidCode
		}
		return nil, err
	}
	return sess, nil
}

// consumeAttempt valida e queima o par (token, código).
//
// Ordem obrigatória (docs/SEGURANCA.md §1.1 e §4 da spec 0001):
//  1. buscar a tentativa VIVA do par (e-mail, hash do token) — mesma consulta
//     exista ou não a conta;
//  2. INCREMENTAR AS TENTATIVAS ATOMICAMENTE no banco e só então reler;
//  3. estourou o limite -> queima a tentativa e devolve inválido;
//  4. comparar o código em tempo constante;
//  5. consumir exigindo RowsAffected == 1 (uso único mesmo sob corrida).
//
// Todos os caminhos de falha devolvem ErrInvalidCode, sem distinção — é o
// grupo D da §3.12. "Token que não casa" e "código errado" são, para o
// cliente, exatamente a mesma coisa.
func (s *Service) consumeAttempt(ctx context.Context, in VerifyEmailInput, now time.Time) (*RegistrationAttempt, error) {
	if !ValidCodeFormat(in.Code) || !ValidRegistrationTokenFormat(in.Token) {
		return nil, ErrInvalidCode
	}

	att, err := s.attempts.LiveByToken(ctx, in.Email, HashRegistrationToken(in.Token), now)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrInvalidCode
		}
		return nil, fmt.Errorf("buscando tentativa de cadastro: %w", err)
	}
	// Cinto e suspensório: a consulta já filtra consumida/expirada e sem
	// código emitido, mas se alguém afrouxar o repositório um dia, o serviço
	// ainda recusa aqui.
	if !att.Live(now) || !att.CodeIssued() {
		return nil, ErrInvalidCode
	}

	attempts, counted, err := s.attempts.IncrementAttempts(ctx, att.ID)
	if err != nil {
		return nil, fmt.Errorf("contando tentativa: %w", err)
	}
	if !counted {
		// Consumida por outra requisição entre o SELECT e o UPDATE.
		return nil, ErrInvalidCode
	}

	if attempts > s.opts.OTPMaxAttempts {
		// A tentativa já estava queimada. Garantimos que continue queimada.
		_, _ = s.attempts.Consume(ctx, att.ID, now)
		return nil, ErrInvalidCode
	}

	if !s.otp.Verify(PurposeEmailVerification, att.Email, in.Code, att.CodeHash) {
		if attempts >= s.opts.OTPMaxAttempts {
			// Última tentativa errada: queima o código. É isto que impede a
			// varredura dos 10^6 valores.
			_, _ = s.attempts.Consume(ctx, att.ID, now)
		}
		s.audit.TryRecord(ctx, audit.Params{
			Action:   audit.ActionCodeFailed,
			Entity:   audit.EntityUser,
			EntityID: att.UserID,
			UserID:   att.UserID,
			IP:       in.IP,
		})
		return nil, ErrInvalidCode
	}

	consumida, err := s.attempts.Consume(ctx, att.ID, now)
	if err != nil {
		return nil, fmt.Errorf("consumindo tentativa: %w", err)
	}
	if !consumida {
		return nil, ErrInvalidCode
	}
	return att, nil
}

// issueCode invalida os códigos anteriores do par e emite um novo.
//
// Continua valendo para o propósito password_reset (docs/SEGURANCA.md §1.1:
// "emitir um código novo invalida todos os anteriores do mesmo propósito").
// O cadastro NÃO passa por aqui: lá o código vive dentro da tentativa, e a
// invalidação é por tentativa — ver RegistrationAttempt.
//
// Devolve o código EM CLARO só para o chamador entregar ao mailer: ele nunca
// é persistido nem logado.
func (s *Service) issueCode(ctx context.Context, email, purpose string, userID *string, now time.Time) (string, error) {
	if !ValidPurpose(purpose) {
		return "", fmt.Errorf("propósito de código desconhecido")
	}
	if _, err := s.codes.ConsumeAllActive(ctx, email, purpose, now); err != nil {
		return "", fmt.Errorf("invalidando códigos anteriores: %w", err)
	}

	code, err := s.otp.GenerateCode()
	if err != nil {
		return "", err
	}

	if err := s.codes.Create(ctx, &VerificationCode{
		ID:        s.ids(),
		Email:     email,
		Purpose:   purpose,
		UserID:    userID,
		CodeHash:  s.otp.Hash(purpose, email, code),
		ExpiresAt: now.Add(s.opts.OTPTTL),
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		return "", fmt.Errorf("gravando código: %w", err)
	}
	return code, nil
}

// consumeCode valida e queima um VerificationCode (hoje, só password_reset).
//
// Mesma ordem obrigatória do consumeAttempt, e as mesmas garantias: contador
// atômico no banco, comparação em tempo constante, uso único sob corrida e
// ErrInvalidCode em todos os caminhos de falha.
func (s *Service) consumeCode(ctx context.Context, email, purpose, code, ip string, now time.Time) (*VerificationCode, error) {
	if !ValidCodeFormat(code) {
		return nil, ErrInvalidCode
	}

	vc, err := s.codes.ActiveByEmailPurpose(ctx, email, purpose, now)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrInvalidCode
		}
		return nil, fmt.Errorf("buscando código: %w", err)
	}

	attempts, counted, err := s.codes.IncrementAttempts(ctx, vc.ID)
	if err != nil {
		return nil, fmt.Errorf("contando tentativa: %w", err)
	}
	if !counted {
		return nil, ErrInvalidCode
	}

	if attempts > s.opts.OTPMaxAttempts {
		_, _ = s.codes.Consume(ctx, vc.ID, now)
		return nil, ErrInvalidCode
	}

	if !s.otp.Verify(purpose, email, code, vc.CodeHash) {
		if attempts >= s.opts.OTPMaxAttempts {
			_, _ = s.codes.Consume(ctx, vc.ID, now)
		}
		s.audit.TryRecord(ctx, audit.Params{
			Action:   audit.ActionCodeFailed,
			Entity:   audit.EntityUser,
			EntityID: derefUserID(vc.UserID),
			UserID:   derefUserID(vc.UserID),
			IP:       ip,
		})
		return nil, ErrInvalidCode
	}

	consumed, err := s.codes.Consume(ctx, vc.ID, now)
	if err != nil {
		return nil, fmt.Errorf("consumindo código: %w", err)
	}
	if !consumed {
		return nil, ErrInvalidCode
	}
	return vc, nil
}

func derefUserID(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
