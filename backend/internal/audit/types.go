// Package audit registra o rastro de eventos sensíveis de conta
// (docs/SEGURANCA.md §9).
//
// Regra do pacote: a entrada de auditoria guarda QUEM, O QUÊ, QUANDO e DE
// QUAL IP — nunca o conteúdo sensível do evento. Não existe campo de detalhe
// livre justamente para que ninguém escreva um código OTP ou uma senha aqui.
package audit

import "time"

// Ações auditadas nesta entrega. São constantes porque viram valor de coluna
// indexada e critério de teste (critério de aceite 19 da spec 0001).
const (
	ActionRegister             = "auth.register"
	ActionRegisterExisting     = "auth.register_existing_email"
	ActionEmailVerified        = "auth.email_verified"
	ActionLogin                = "auth.login"
	ActionLoginFailed          = "auth.login_failed"
	ActionLoginUnverified      = "auth.login_unverified"
	ActionLogout               = "auth.logout"
	ActionRefresh              = "auth.refresh"
	ActionRefreshReuseDetected = "auth.refresh_reuse_detected"
	ActionRefreshRejected      = "auth.refresh_rejected"
	ActionPasswordResetRequest = "auth.password_reset_requested"
	ActionPasswordReset        = "auth.password_reset"
	ActionCodeIssued           = "auth.verification_code_issued"
	ActionCodeFailed           = "auth.verification_code_failed"
	ActionHouseholdCreated     = "household.created"
)

// Entidades auditadas.
const (
	EntityUser      = "user"
	EntityHousehold = "household"
	EntitySession   = "session"
)

// Entry é uma linha do rastro de auditoria.
type Entry struct {
	ID          string
	HouseholdID *string
	UserID      *string
	Action      string
	Entity      string
	EntityID    *string
	IP          string
	CreatedAt   time.Time
}
