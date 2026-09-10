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

// ForgotPassword inicia a recuperação de senha (§3.9).
//
// Grupo C da §3.12: e-mail existente e verificado, existente e não
// verificado, ou inexistente — todos devolvem 202 com o MESMO corpo. Erro
// nenhum escapa deste método por um caminho que dependa da existência da
// conta; o que der errado vira log.
//
// Conta ainda não verificada NÃO recebe código de troca de senha: ela é
// inutilizável, e o passo que falta é confirmar o e-mail, não redefinir a
// senha.
func (s *Service) ForgotPassword(ctx context.Context, email, ip string) error {
	now := s.clock()

	u, err := s.users.ByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("buscando usuário na recuperação: %w", err)
	}
	if !u.Verified() {
		return nil
	}

	allowed, err := s.withinResendWindow(ctx, u.Email, PurposePasswordReset, now)
	if err != nil {
		s.lg.ErrorContext(ctx, "falha ao checar cooldown de recuperação", slog.String("reason", err.Error()))
		return nil
	}
	if !allowed {
		return nil
	}

	var code string
	err = s.uow.Do(ctx, func(ctx context.Context) error {
		var err error
		code, err = s.issueCode(ctx, u.Email, PurposePasswordReset, &u.ID, now)
		if err != nil {
			return err
		}
		return s.audit.Record(ctx, audit.Params{
			Action:   audit.ActionPasswordResetRequest,
			Entity:   audit.EntityUser,
			EntityID: u.ID,
			UserID:   u.ID,
			IP:       ip,
		})
	})
	if err != nil {
		s.lg.ErrorContext(ctx, "falha ao emitir código de recuperação",
			slog.String("user_id", u.ID),
			slog.String("reason", err.Error()),
		)
		return nil
	}

	s.mailer.EnqueuePasswordResetCode(u.Email, u.Name, code, s.opts.OTPTTL)
	return nil
}

// withinResendWindow informa se já passou o intervalo mínimo desde a última
// emissão de código do par (e-mail, propósito).
//
// Vale para o fluxo de RECUPERAÇÃO DE SENHA. O cadastro tem o freio próprio
// em canSendVerificationMail, que consulta a tabela de tentativas e a cota
// por endereço no MESMO ponto.
func (s *Service) withinResendWindow(ctx context.Context, email, purpose string, now time.Time) (bool, error) {
	last, ok, err := s.codes.LastIssuedAt(ctx, email, purpose)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	return now.Sub(last) >= s.opts.OTPResendInterval, nil
}

// ResetPasswordInput são os dados normalizados pela borda.
type ResetPasswordInput struct {
	Email       string
	Code        string
	NewPassword string
	IP          string
}

// ResetPassword troca a senha depois de conferir o código (§3.10).
//
// docs/SEGURANCA.md §1.1: ao redefinir a senha, TODOS os refresh tokens do
// usuário (todas as famílias) são revogados — trocar a senha derruba todas as
// sessões, que é o que a vítima de um roubo de conta espera que aconteça.
//
// O código é validado com purpose = password_reset: um código emitido para
// verificar e-mail NÃO serve aqui, porque o purpose entra no HMAC.
func (s *Service) ResetPassword(ctx context.Context, in ResetPasswordInput) error {
	now := s.clock()

	if _, err := s.consumeCode(ctx, in.Email, PurposePasswordReset, in.Code, in.IP, now); err != nil {
		return err
	}

	u, err := s.users.ByEmail(ctx, in.Email)
	if err != nil {
		return ErrInvalidCode
	}

	passwordHash, err := s.hasher.Hash(ctx, in.NewPassword)
	if err != nil {
		return fmt.Errorf("gerando hash da nova senha: %w", err)
	}

	return s.uow.Do(ctx, func(ctx context.Context) error {
		if err := s.users.UpdatePassword(ctx, u.ID, passwordHash, now); err != nil {
			return fmt.Errorf("atualizando senha: %w", err)
		}
		if _, err := s.tokens.RevokeAllForUser(ctx, u.ID, now); err != nil {
			return fmt.Errorf("revogando sessões: %w", err)
		}
		// Tentativas de cadastro pendentes do mesmo endereço também caem:
		// quem trocou a senha não deve deixar nenhum par token+código antigo
		// circulando.
		if _, err := s.attempts.ConsumeAllForEmail(ctx, u.Email, now); err != nil {
			return fmt.Errorf("invalidando tentativas de cadastro pendentes: %w", err)
		}
		if _, err := s.codes.ConsumeAllActive(ctx, u.Email, PurposeEmailVerification, now); err != nil {
			return fmt.Errorf("invalidando códigos pendentes: %w", err)
		}
		return s.audit.Record(ctx, audit.Params{
			Action:   audit.ActionPasswordReset,
			Entity:   audit.EntityUser,
			EntityID: u.ID,
			UserID:   u.ID,
			IP:       in.IP,
		})
	})
}
