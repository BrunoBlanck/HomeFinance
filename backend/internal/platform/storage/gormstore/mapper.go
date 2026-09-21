package gormstore

import (
	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/auth"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
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
	// Casa montada em código antigo pode chegar sem fuso ou sem moeda; o
	// default é aplicado aqui para que a coluna NOT NULL nunca receba string
	// vazia — que passaria no banco e depois faria Location() escolher o fuso
	// errado calado.
	timezone := h.Timezone
	if timezone == "" {
		timezone = household.DefaultTimezone
	}
	currency := h.Currency
	if currency == "" {
		currency = household.DefaultCurrency
	}
	return &Household{
		ID:        h.ID,
		Name:      h.Name,
		Timezone:  timezone,
		Currency:  currency,
		CreatedAt: h.CreatedAt,
		UpdatedAt: h.UpdatedAt,
	}
}

func toHouseholdEntity(m *Household) *household.Household {
	return &household.Household{
		ID:        m.ID,
		Name:      m.Name,
		Timezone:  m.Timezone,
		Currency:  m.Currency,
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

// --- account -------------------------------------------------------------

func toAccountModel(a *account.Account) *Account {
	// Conta criada por código anterior ao schema v3 chega sem instituição; o
	// default é aplicado aqui para que a coluna NOT NULL nunca receba string
	// vazia — que passaria no banco e depois faria a importação não achar
	// leiaute nenhum sem dizer por quê (mesma lição de households.timezone).
	institution := a.Institution
	if institution == "" {
		institution = account.InstitutionOther
	}
	return &Account{
		Institution:         institution,
		StatementClosingDay: a.StatementClosingDay,
		StatementDueDay:     a.StatementDueDay,
		ID:                  a.ID,
		HouseholdID:         a.HouseholdID,
		Name:                a.Name,
		NameNorm:            a.NameNorm,
		Kind:                a.Kind,

		OpeningBalanceCents: a.OpeningBalanceCents,
		// A data civil vira texto "YYYY-MM-DD" aqui, e só aqui (D3 da spec
		// 0003). Nenhuma outra camada manipula a string crua.
		OpeningDate: a.OpeningDate.String(),
		ArchivedAt:  a.ArchivedAt,
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
		DeletedAt:   a.DeletedAt,
	}
}

func toAccountEntity(m *Account) *account.Account {
	// Data ilegível no banco (linha adulterada, migração manual malfeita) vira
	// data zero em vez de derrubar a listagem inteira. O erro é visível — a
	// tela mostra campo vazio — sem transformar um registro estragado em 500.
	d, err := civil.Parse(m.OpeningDate)
	if err != nil {
		d = civil.Date{}
	}
	return &account.Account{
		ID:                  m.ID,
		HouseholdID:         m.HouseholdID,
		Name:                m.Name,
		NameNorm:            m.NameNorm,
		Kind:                m.Kind,
		OpeningBalanceCents: m.OpeningBalanceCents,
		OpeningDate:         d,
		Institution:         m.Institution,
		StatementClosingDay: m.StatementClosingDay,
		StatementDueDay:     m.StatementDueDay,
		ArchivedAt:          m.ArchivedAt,
		CreatedAt:           m.CreatedAt,
		UpdatedAt:           m.UpdatedAt,
		DeletedAt:           m.DeletedAt,
	}
}

// --- category ------------------------------------------------------------

func toCategoryModel(c *category.Category) *Category {
	return &Category{
		ID:          c.ID,
		HouseholdID: c.HouseholdID,
		ParentID:    c.ParentID,
		Name:        c.Name,
		NameNorm:    c.NameNorm,
		Kind:        c.Kind,
		ArchivedAt:  c.ArchivedAt,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
		DeletedAt:   c.DeletedAt,
	}
}

func toCategoryEntity(m *Category) *category.Category {
	return &category.Category{
		ID:          m.ID,
		HouseholdID: m.HouseholdID,
		ParentID:    m.ParentID,
		Name:        m.Name,
		NameNorm:    m.NameNorm,
		Kind:        m.Kind,
		ArchivedAt:  m.ArchivedAt,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
		DeletedAt:   m.DeletedAt,
	}
}

// --- palavras-chave (schema v4) -----------------------------------------

func toCategoryKeywordModel(k *category.Keyword) *CategoryKeyword {
	return &CategoryKeyword{
		ID:          k.ID,
		HouseholdID: k.HouseholdID,
		CategoryID:  k.CategoryID,
		Keyword:     k.Keyword,
		KeywordNorm: k.Norm,
		Position:    k.Position,
		CreatedAt:   k.CreatedAt,
	}
}

func toCategoryKeywordEntity(m *CategoryKeyword) *category.Keyword {
	return &category.Keyword{
		ID:          m.ID,
		HouseholdID: m.HouseholdID,
		CategoryID:  m.CategoryID,
		Keyword:     m.Keyword,
		Norm:        m.KeywordNorm,
		Position:    m.Position,
		CreatedAt:   m.CreatedAt,
	}
}

func toAccountKeywordModel(k *account.Keyword) *AccountKeyword {
	return &AccountKeyword{
		ID:          k.ID,
		HouseholdID: k.HouseholdID,
		AccountID:   k.AccountID,
		Keyword:     k.Keyword,
		KeywordNorm: k.Norm,
		Position:    k.Position,
		CreatedAt:   k.CreatedAt,
	}
}

func toAccountKeywordEntity(m *AccountKeyword) *account.Keyword {
	return &account.Keyword{
		ID:          m.ID,
		HouseholdID: m.HouseholdID,
		AccountID:   m.AccountID,
		Keyword:     m.Keyword,
		Norm:        m.KeywordNorm,
		Position:    m.Position,
		CreatedAt:   m.CreatedAt,
	}
}

// --- data civil ----------------------------------------------------------

// civilOrZero lê a data civil guardada como texto.
//
// Data ilegível no banco (linha adulterada, migração manual malfeita) vira
// data zero em vez de derrubar a listagem inteira: o erro fica visível na tela
// como campo vazio, sem transformar um registro estragado em 500.
func civilOrZero(s string) civil.Date {
	d, err := civil.Parse(s)
	if err != nil {
		return civil.Date{}
	}
	return d
}

// civilPtrOrNil converte data civil anulável para texto anulável. Data zero
// vira NULL, nunca "0000-00-00" — que o MySQL recusa com NO_ZERO_DATE.
func civilPtrOrNil(d *civil.Date) *string {
	if d == nil || d.IsZero() {
		return nil
	}
	s := d.String()
	return &s
}

// civilPtrFromString faz o caminho inverso.
func civilPtrFromString(s *string) *civil.Date {
	if s == nil || *s == "" {
		return nil
	}
	d, err := civil.Parse(*s)
	if err != nil {
		return nil
	}
	return &d
}

// --- transaction ---------------------------------------------------------

func toTransactionModel(t *transaction.Transaction) *Transaction {
	return &Transaction{
		ID:              t.ID,
		HouseholdID:     t.HouseholdID,
		Kind:            t.Kind,
		AccountID:       t.AccountID,
		CategoryID:      t.CategoryID,
		AmountCents:     t.AmountCents,
		Description:     t.Description,
		DescriptionNorm: t.DescriptionNorm,
		OccurredOn:      t.OccurredOn.String(),
		// year_month é PROJEÇÃO de occurred_on, calculada aqui — e só aqui —
		// para que as duas colunas não possam divergir. Se fosse campo da
		// entidade, bastaria um caminho de escrita esquecê-la para um mês
		// inteiro sumir do relatório em silêncio (armadilha P1).
		//
		// competence_month NÃO é projeção: a fatura do cartão desloca a
		// competência e nenhuma função a deriva da data. Ela é dado, vem do
		// serviço, e o repositório recusa lote com ela vazia.
		YearMonth:       t.OccurredOn.YearMonth(),
		CompetenceMonth: t.CompetenceMonth,
		TransferGroupID: t.TransferGroupID,
		StatementID:     t.StatementID,
		Source:          t.Source,
		ImportBatchID:   t.ImportBatchID,
		ExternalID:      t.ExternalID,
		DedupKey:        t.DedupKey,
		DedupOrdinal:    t.DedupOrdinal,
		CreatedBy:       t.CreatedBy,
		CreatedAt:       t.CreatedAt,
		UpdatedAt:       t.UpdatedAt,
		DeletedAt:       t.DeletedAt,
	}
}

func toTransactionEntity(m *Transaction) *transaction.Transaction {
	return &transaction.Transaction{
		ID:              m.ID,
		HouseholdID:     m.HouseholdID,
		Kind:            m.Kind,
		AccountID:       m.AccountID,
		CategoryID:      m.CategoryID,
		AmountCents:     m.AmountCents,
		Description:     m.Description,
		DescriptionNorm: m.DescriptionNorm,
		OccurredOn:      civilOrZero(m.OccurredOn),
		CompetenceMonth: m.CompetenceMonth,
		TransferGroupID: m.TransferGroupID,
		StatementID:     m.StatementID,
		Source:          m.Source,
		ImportBatchID:   m.ImportBatchID,
		ExternalID:      m.ExternalID,
		DedupKey:        m.DedupKey,
		DedupOrdinal:    m.DedupOrdinal,
		CreatedBy:       m.CreatedBy,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
		DeletedAt:       m.DeletedAt,
	}
}

// --- card_statement ------------------------------------------------------

func toCardStatementModel(s *cardstatement.Statement) *CardStatement {
	return &CardStatement{
		ID:              s.ID,
		HouseholdID:     s.HouseholdID,
		AccountID:       s.AccountID,
		CompetenceMonth: s.CompetenceMonth,
		ClosingDate:     s.ClosingDate.String(),
		DueDate:         s.DueDate.String(),
		Source:          s.Source,
		CreatedAt:       s.CreatedAt,
		UpdatedAt:       s.UpdatedAt,
		DeletedAt:       s.DeletedAt,
	}
}

func toCardStatementEntity(m *CardStatement) *cardstatement.Statement {
	return &cardstatement.Statement{
		ID:              m.ID,
		HouseholdID:     m.HouseholdID,
		AccountID:       m.AccountID,
		CompetenceMonth: m.CompetenceMonth,
		ClosingDate:     civilOrZero(m.ClosingDate),
		DueDate:         civilOrZero(m.DueDate),
		Source:          m.Source,
		CreatedAt:       m.CreatedAt,
		UpdatedAt:       m.UpdatedAt,
		DeletedAt:       m.DeletedAt,
	}
}

// --- importer ------------------------------------------------------------

func toImportBatchModel(b *importer.Batch) *ImportBatch {
	return &ImportBatch{
		ID:                       b.ID,
		HouseholdID:              b.HouseholdID,
		AccountID:                b.AccountID,
		CreatedBy:                b.CreatedBy,
		Institution:              b.Institution,
		DocKind:                  b.DocKind,
		FormatID:                 b.FormatID,
		FileName:                 b.FileName,
		ContentSHA256:            b.ContentSHA256,
		Encoding:                 b.Encoding,
		RowCount:                 b.RowCount,
		ImportedCount:            b.ImportedCount,
		SkippedCount:             b.SkippedCount,
		BlockedCount:             b.BlockedCount,
		RestoredCount:            b.RestoredCount,
		RejectedCount:            b.RejectedCount,
		LinkedCount:              b.LinkedCount,
		TransferPairsCount:       b.TransferPairsCount,
		MinDate:                  b.MinDate.String(),
		MaxDate:                  b.MaxDate.String(),
		SuggestedCompetenceMonth: b.SuggestedCompetenceMonth,
		SuggestedClosingDate:     civilPtrOrNil(b.SuggestedClosingDate),
		SuggestedDueDate:         civilPtrOrNil(b.SuggestedDueDate),
		Status:                   b.Status,
		ExpiresAt:                b.ExpiresAt,
		CommittedAt:              b.CommittedAt,
		CreatedAt:                b.CreatedAt,
		UpdatedAt:                b.UpdatedAt,
	}
}

func toImportBatchEntity(m *ImportBatch) *importer.Batch {
	return &importer.Batch{
		ID:                       m.ID,
		HouseholdID:              m.HouseholdID,
		AccountID:                m.AccountID,
		CreatedBy:                m.CreatedBy,
		Institution:              m.Institution,
		DocKind:                  m.DocKind,
		FormatID:                 m.FormatID,
		FileName:                 m.FileName,
		ContentSHA256:            m.ContentSHA256,
		Encoding:                 m.Encoding,
		RowCount:                 m.RowCount,
		ImportedCount:            m.ImportedCount,
		SkippedCount:             m.SkippedCount,
		BlockedCount:             m.BlockedCount,
		RestoredCount:            m.RestoredCount,
		RejectedCount:            m.RejectedCount,
		LinkedCount:              m.LinkedCount,
		TransferPairsCount:       m.TransferPairsCount,
		MinDate:                  civilOrZero(m.MinDate),
		MaxDate:                  civilOrZero(m.MaxDate),
		SuggestedCompetenceMonth: m.SuggestedCompetenceMonth,
		SuggestedClosingDate:     civilPtrFromString(m.SuggestedClosingDate),
		SuggestedDueDate:         civilPtrFromString(m.SuggestedDueDate),
		Status:                   m.Status,
		ExpiresAt:                m.ExpiresAt,
		CommittedAt:              m.CommittedAt,
		CreatedAt:                m.CreatedAt,
		UpdatedAt:                m.UpdatedAt,
	}
}

func toImportRowModel(r *importer.Row) *ImportRow {
	return &ImportRow{
		ID:                 r.ID,
		HouseholdID:        r.HouseholdID,
		BatchID:            r.BatchID,
		Seq:                r.Seq,
		LineNo:             r.LineNo,
		Kind:               r.Kind,
		OccurredOn:         r.OccurredOn.String(),
		AmountCents:        r.AmountCents,
		Description:        r.Description,
		DescriptionNorm:    r.DescriptionNorm,
		ExternalID:         r.ExternalID,
		DedupKey:           r.DedupKey,
		Status:             r.Status,
		RejectReason:       r.RejectReason,
		MatchTransactionID: r.MatchTransactionID,

		SuggestedCategoryID:           r.SuggestedCategoryID,
		MatchScore:                    r.MatchScore,
		MatchedKeyword:                r.MatchedKeyword,
		SuggestedCounterpartAccountID: r.SuggestedCounterpartAccountID,

		CreatedAt: r.CreatedAt,
	}
}

func toImportRowEntity(m *ImportRow) *importer.Row {
	return &importer.Row{
		ID:                 m.ID,
		HouseholdID:        m.HouseholdID,
		BatchID:            m.BatchID,
		Seq:                m.Seq,
		LineNo:             m.LineNo,
		Kind:               m.Kind,
		OccurredOn:         civilOrZero(m.OccurredOn),
		AmountCents:        m.AmountCents,
		Description:        m.Description,
		DescriptionNorm:    m.DescriptionNorm,
		ExternalID:         m.ExternalID,
		DedupKey:           m.DedupKey,
		Status:             m.Status,
		RejectReason:       m.RejectReason,
		MatchTransactionID: m.MatchTransactionID,

		SuggestedCategoryID:           m.SuggestedCategoryID,
		MatchScore:                    m.MatchScore,
		MatchedKeyword:                m.MatchedKeyword,
		SuggestedCounterpartAccountID: m.SuggestedCounterpartAccountID,

		CreatedAt: m.CreatedAt,
	}
}
