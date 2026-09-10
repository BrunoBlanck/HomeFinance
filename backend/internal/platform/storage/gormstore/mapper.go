package gormstore

import (
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/user"
)

// Conversões entre modelo de persistência e entidade de domínio.
//
// A tradução é explícita, campo a campo, de propósito: uma coluna nova só
// chega ao domínio (e portanto à API) quando alguém a escreve aqui. É a
// contrapartida da regra "nunca serializar a entidade do banco"
// (docs/SEGURANCA.md §4).

func toUserModel(u *user.User) *User {
	return &User{
		ID:              u.ID,
		Email:           u.Email,
		PasswordHash:    u.PasswordHash,
		Name:            u.Name,
		EmailVerifiedAt: u.EmailVerifiedAt,
		CreatedAt:       u.CreatedAt,
		UpdatedAt:       u.UpdatedAt,
	}
}

func toUserEntity(m *User) *user.User {
	return &user.User{
		ID:              m.ID,
		Email:           m.Email,
		PasswordHash:    m.PasswordHash,
		Name:            m.Name,
		EmailVerifiedAt: m.EmailVerifiedAt,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
	}
}

func toHouseholdModel(h *household.Household) *Household {
	return &Household{
		ID:        h.ID,
		Name:      h.Name,
		CreatedAt: h.CreatedAt,
		UpdatedAt: h.UpdatedAt,
	}
}

func toHouseholdEntity(m *Household) *household.Household {
	return &household.Household{
		ID:        m.ID,
		Name:      m.Name,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func toMembershipModel(m *household.Membership) *Membership {
	return &Membership{
		ID:          m.ID,
		HouseholdID: m.HouseholdID,
		UserID:      m.UserID,
		Role:        m.Role,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

func toMembershipEntity(m *Membership) *household.Membership {
	return &household.Membership{
		ID:          m.ID,
		HouseholdID: m.HouseholdID,
		UserID:      m.UserID,
		Role:        m.Role,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}

func toRefreshFamilyModel(f *auth.RefreshFamily) *RefreshFamily {
	return &RefreshFamily{
		ID:        f.ID,
		UserID:    f.UserID,
		RevokedAt: f.RevokedAt,
		CreatedAt: f.CreatedAt,
		UpdatedAt: f.UpdatedAt,
	}
}

func toRefreshFamilyEntity(m *RefreshFamily) *auth.RefreshFamily {
	return &auth.RefreshFamily{
		ID:        m.ID,
		UserID:    m.UserID,
		RevokedAt: m.RevokedAt,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	}
}

func toRefreshTokenModel(t *auth.RefreshToken) *RefreshToken {
	return &RefreshToken{
		ID:         t.ID,
		UserID:     t.UserID,
		FamilyID:   t.FamilyID,
		TokenHash:  t.TokenHash,
		ExpiresAt:  t.ExpiresAt,
		RevokedAt:  t.RevokedAt,
		ReplacedBy: t.ReplacedBy,
		CreatedAt:  t.CreatedAt,
		UpdatedAt:  t.UpdatedAt,
	}
}

func toRefreshTokenEntity(m *RefreshToken) *auth.RefreshToken {
	return &auth.RefreshToken{
		ID:         m.ID,
		UserID:     m.UserID,
		FamilyID:   m.FamilyID,
		TokenHash:  m.TokenHash,
		ExpiresAt:  m.ExpiresAt,
		RevokedAt:  m.RevokedAt,
		ReplacedBy: m.ReplacedBy,
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}
}

func toVerificationCodeModel(c *auth.VerificationCode) *VerificationCode {
	return &VerificationCode{
		ID:         c.ID,
		Email:      c.Email,
		Purpose:    c.Purpose,
		UserID:     c.UserID,
		CodeHash:   c.CodeHash,
		ExpiresAt:  c.ExpiresAt,
		ConsumedAt: c.ConsumedAt,
		Attempts:   c.Attempts,
		CreatedAt:  c.CreatedAt,
		UpdatedAt:  c.UpdatedAt,
	}
}

func toVerificationCodeEntity(m *VerificationCode) *auth.VerificationCode {
	return &auth.VerificationCode{
		ID:         m.ID,
		Email:      m.Email,
		Purpose:    m.Purpose,
		UserID:     m.UserID,
		CodeHash:   m.CodeHash,
		ExpiresAt:  m.ExpiresAt,
		ConsumedAt: m.ConsumedAt,
		Attempts:   m.Attempts,
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  m.UpdatedAt,
	}
}

func toAuditModel(e *audit.Entry) *AuditLog {
	return &AuditLog{
		ID:          e.ID,
		HouseholdID: e.HouseholdID,
		UserID:      e.UserID,
		Action:      e.Action,
		Entity:      e.Entity,
		EntityID:    e.EntityID,
		IP:          e.IP,
		CreatedAt:   e.CreatedAt,
	}
}

func toAuditEntity(m *AuditLog) *audit.Entry {
	return &audit.Entry{
		ID:          m.ID,
		HouseholdID: m.HouseholdID,
		UserID:      m.UserID,
		Action:      m.Action,
		Entity:      m.Entity,
		EntityID:    m.EntityID,
		IP:          m.IP,
		CreatedAt:   m.CreatedAt,
	}
}

func toRegistrationAttemptModel(a *auth.RegistrationAttempt) *RegistrationAttempt {
	return &RegistrationAttempt{
		ID:           a.ID,
		Email:        a.Email,
		UserID:       a.UserID,
		Name:         a.Name,
		PasswordHash: a.PasswordHash,
		TokenHash:    a.TokenHash,
		CodeHash:     a.CodeHash,
		CodeIssuedAt: a.CodeIssuedAt,
		ExpiresAt:    a.ExpiresAt,
		ConsumedAt:   a.ConsumedAt,
		Attempts:     a.Attempts,
		CreatedAt:    a.CreatedAt,
		UpdatedAt:    a.UpdatedAt,
	}
}

func toRegistrationAttemptEntity(m *RegistrationAttempt) *auth.RegistrationAttempt {
	return &auth.RegistrationAttempt{
		ID:           m.ID,
		Email:        m.Email,
		UserID:       m.UserID,
		Name:         m.Name,
		PasswordHash: m.PasswordHash,
		TokenHash:    m.TokenHash,
		CodeHash:     m.CodeHash,
		CodeIssuedAt: m.CodeIssuedAt,
		ExpiresAt:    m.ExpiresAt,
		ConsumedAt:   m.ConsumedAt,
		Attempts:     m.Attempts,
		CreatedAt:    m.CreatedAt,
		UpdatedAt:    m.UpdatedAt,
	}
}
