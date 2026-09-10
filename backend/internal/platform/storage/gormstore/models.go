// Package gormstore implementa, com GORM, as interfaces de repositório
// declaradas nos pacotes de domínio (ADR-008).
//
// Regras que este pacote sustenta:
//   - *gorm.DB não sai daqui nem do pacote storage;
//   - nenhuma consulta usa Raw, Exec ou string montada em Where/Order/
//     Select/Table — tudo por placeholder "?" (docs/SEGURANCA.md §3);
//   - IDs são gerados no service (internal/id), nunca aqui e nunca em hook;
//   - sem chave estrangeira física (ADR-013): índice explícito em toda
//     coluna de referência e integridade garantida em transação.
package gormstore

import "time"

// User é o modelo de persistência de internal/user.User.
type User struct {
	ID              string     `gorm:"type:varchar(36);primaryKey"`
	Email           string     `gorm:"type:varchar(254);not null;uniqueIndex:ux_users_email"`
	PasswordHash    string     `gorm:"type:varchar(255);not null"` // PHC Argon2id
	Name            string     `gorm:"type:varchar(120);not null"`
	EmailVerifiedAt *time.Time `gorm:"index:ix_users_email_verified_at"`
	CreatedAt       time.Time  `gorm:"not null"`
	UpdatedAt       time.Time  `gorm:"not null"`
}

// TableName fixa o nome da tabela.
func (User) TableName() string { return "users" }

// Household é o modelo de persistência de internal/household.Household.
type Household struct {
	ID        string    `gorm:"type:varchar(36);primaryKey"`
	Name      string    `gorm:"type:varchar(120);not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName fixa o nome da tabela.
func (Household) TableName() string { return "households" }

// Membership é o vínculo usuário-casa.
type Membership struct {
	ID          string    `gorm:"type:varchar(36);primaryKey"`
	HouseholdID string    `gorm:"type:varchar(36);not null;uniqueIndex:ux_memberships_household_user,priority:1;index:ix_memberships_household"`
	UserID      string    `gorm:"type:varchar(36);not null;uniqueIndex:ux_memberships_household_user,priority:2;index:ix_memberships_user"`
	Role        string    `gorm:"type:varchar(20);not null"` // owner | member
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}

// TableName fixa o nome da tabela.
func (Membership) TableName() string { return "memberships" }

// RefreshFamily é o estado de uma FAMÍLIA de refresh tokens.
//
// Existe porque a revogação precisa ter granularidade de família, não de
// linha: a revogação por UPDATE em refresh_tokens só alcança os elos que já
// estão gravados, e o sucessor de uma rotação em curso ainda não está. Um
// registro por família dá um alvo ÚNICO e sempre existente para marcar
// "esta sessão morreu", que o Refresh consulta antes de emitir qualquer
// coisa (docs/SEGURANCA.md §1, risco 5 da §9 da spec 0001).
type RefreshFamily struct {
	ID        string `gorm:"type:varchar(36);primaryKey"` // = refresh_tokens.family_id
	UserID    string `gorm:"type:varchar(36);not null;index:ix_refresh_families_user"`
	RevokedAt *time.Time
	CreatedAt time.Time `gorm:"not null;index:ix_refresh_families_created_at"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName fixa o nome da tabela.
func (RefreshFamily) TableName() string { return "refresh_families" }

// RefreshToken guarda apenas o SHA-256 hex do token opaco — nunca o token.
type RefreshToken struct {
	ID         string    `gorm:"type:varchar(36);primaryKey"`
	UserID     string    `gorm:"type:varchar(36);not null;index:ix_refresh_tokens_user"`
	FamilyID   string    `gorm:"type:varchar(36);not null;index:ix_refresh_tokens_family"`
	TokenHash  string    `gorm:"type:varchar(64);not null;uniqueIndex:ux_refresh_tokens_hash"`
	ExpiresAt  time.Time `gorm:"not null;index:ix_refresh_tokens_expires_at"`
	RevokedAt  *time.Time
	ReplacedBy *string   `gorm:"type:varchar(36)"` // forense da rotação
	CreatedAt  time.Time `gorm:"not null"`
	UpdatedAt  time.Time `gorm:"not null"`
}

// TableName fixa o nome da tabela.
func (RefreshToken) TableName() string { return "refresh_tokens" }

// VerificationCode guarda apenas o HMAC-SHA-256 hex do código de 6 dígitos.
// O código em texto nunca é persistido (docs/SEGURANCA.md §1.1).
type VerificationCode struct {
	ID         string    `gorm:"type:varchar(36);primaryKey"`
	Email      string    `gorm:"type:varchar(254);not null;index:ix_vcodes_email_purpose,priority:1"`
	Purpose    string    `gorm:"type:varchar(32);not null;index:ix_vcodes_email_purpose,priority:2"`
	UserID     *string   `gorm:"type:varchar(36);index:ix_vcodes_user"`
	CodeHash   string    `gorm:"type:varchar(64);not null"`
	ExpiresAt  time.Time `gorm:"not null;index:ix_vcodes_expires_at"`
	ConsumedAt *time.Time
	Attempts   int       `gorm:"not null;default:0"`
	CreatedAt  time.Time `gorm:"not null"`
	UpdatedAt  time.Time `gorm:"not null"`
}

// TableName fixa o nome da tabela.
func (VerificationCode) TableName() string { return "verification_codes" }

// RegistrationAttempt é UMA tentativa de cadastro pendente: as credenciais
// escolhidas por quem pediu, o código que foi para a caixa do endereço e o
// hash do token opaco que amarra os dois (auth.RegistrationAttempt).
//
// O token_hash é único: ele é o segredo que separa a tentativa da vítima da
// tentativa do atacante para o MESMO endereço.
type RegistrationAttempt struct {
	ID           string `gorm:"type:varchar(36);primaryKey"`
	Email        string `gorm:"type:varchar(254);not null;index:ix_reg_attempts_email,priority:1"`
	UserID       string `gorm:"type:varchar(36);not null;index:ix_reg_attempts_user"`
	Name         string `gorm:"type:varchar(120);not null"`
	PasswordHash string `gorm:"type:varchar(255);not null"` // PHC Argon2id
	TokenHash    string `gorm:"type:varchar(64);not null;uniqueIndex:ux_reg_attempts_token"`
	CodeHash     string `gorm:"type:varchar(64);not null"`
	// CodeIssuedAt é NULO enquanto nenhuma mensagem tiver saído.
	//
	// NULO em vez do zero de time.Time por PORTABILIDADE (revisão de
	// 09/09/2026): go-sql-driver/mysql serializa o zero como
	// '0000-00-00 00:00:00' e o sql_mode padrão do MySQL 8
	// (STRICT_TRANS_TABLES + NO_ZERO_DATE) recusa o INSERT com erro 1292.
	// Postgres, SQL Server e SQLite aceitavam o ano 1; o MySQL, que é banco
	// de produção suportado, não — e a tentativa simplesmente não nascia.
	CodeIssuedAt *time.Time `gorm:"index:ix_reg_attempts_email,priority:2"`
	ExpiresAt    time.Time  `gorm:"not null;index:ix_reg_attempts_expires_at"`
	ConsumedAt   *time.Time
	Attempts     int       `gorm:"not null;default:0"`
	CreatedAt    time.Time `gorm:"not null"`
	UpdatedAt    time.Time `gorm:"not null"`
}

// TableName fixa o nome da tabela.
func (RegistrationAttempt) TableName() string { return "registration_attempts" }

// AuditLog é o rastro de eventos sensíveis (docs/SEGURANCA.md §9).
type AuditLog struct {
	ID          string    `gorm:"type:varchar(36);primaryKey"`
	HouseholdID *string   `gorm:"type:varchar(36);index:ix_audit_log_household"`
	UserID      *string   `gorm:"type:varchar(36);index:ix_audit_log_user"`
	Action      string    `gorm:"type:varchar(64);not null;index:ix_audit_log_action"`
	Entity      string    `gorm:"type:varchar(64);not null"`
	EntityID    *string   `gorm:"type:varchar(36)"`
	IP          string    `gorm:"type:varchar(45);not null"` // comporta IPv6
	CreatedAt   time.Time `gorm:"not null;index:ix_audit_log_created_at"`
}

// TableName fixa o nome da tabela.
func (AuditLog) TableName() string { return "audit_log" }

// Models devolve todos os modelos na ordem de criação.
//
// É a única fonte da lista usada por storage.Migrate — modelo esquecido aqui
// é tabela que não existe em produção.
func Models() []any {
	return []any{
		&User{},
		&Household{},
		&Membership{},
		&RefreshFamily{},
		&RefreshToken{},
		&VerificationCode{},
		&RegistrationAttempt{},
		&AuditLog{},
	}
}

// TableNames devolve os nomes das tabelas, para teste de migração.
func TableNames() []string {
	return []string{
		User{}.TableName(),
		Household{}.TableName(),
		Membership{}.TableName(),
		RefreshFamily{}.TableName(),
		RefreshToken{}.TableName(),
		VerificationCode{}.TableName(),
		RegistrationAttempt{}.TableName(),
		AuditLog{}.TableName(),
	}
}
