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
	ID   string `gorm:"type:varchar(36);primaryKey"`
	Name string `gorm:"type:varchar(120);not null"`
	// Timezone e Currency entram no schema v2 (ADR-019). O default no nível
	// da COLUNA não é decoração: é ele que faz o AutoMigrate preencher as
	// linhas que já existem, em vez de deixá-las com string vazia — e casa
	// sem fuso decide "atrasado" errado.
	Timezone  string    `gorm:"type:varchar(64);not null;default:'America/Sao_Paulo'"`
	Currency  string    `gorm:"type:varchar(3);not null;default:'BRL'"`
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

// Account é o modelo de persistência de internal/account.Account.
//
// Sobre o que NÃO tem aqui:
//   - não há coluna de saldo. O saldo é derivado (ADR-017): coluna
//     materializada é a fonte clássica de saldo errado, porque basta um
//     caminho de escrita esquecido para corrompê-la, e em silêncio.
//   - não há índice ÚNICO em (household_id, name_norm). A regra é "único
//     entre as NÃO arquivadas", o que exigiria índice parcial — armadilha P4:
//     Postgres e SQLite têm, o MySQL não tem, o MSSQL tem com outra sintaxe.
//     A unicidade é verificada no serviço, dentro da transação (D2 da spec
//     0003); o índice não único abaixo é o que torna essa consulta barata.
type Account struct {
	ID          string `gorm:"type:varchar(36);primaryKey"`
	HouseholdID string `gorm:"type:varchar(36);not null;index:ix_accounts_household_norm,priority:1;index:ix_accounts_household_archived,priority:1"`
	Name        string `gorm:"type:varchar(80);not null"`
	NameNorm    string `gorm:"type:varchar(80);not null;index:ix_accounts_household_norm,priority:2"`
	Kind        string `gorm:"type:varchar(20);not null"`
	// Dinheiro é int64 em centavos em TODA camada (ADR-003). BIGINT no banco.
	OpeningBalanceCents int64 `gorm:"not null"`
	// Data civil guardada como TEXTO "YYYY-MM-DD" (D3 da spec 0003): os
	// drivers dos quatro dialetos devolvem DATE como time.Time com fuso
	// implícito, e é daí que nasce o bug de "um dia antes". Como o formato é
	// de largura fixa com zero à esquerda, a ordem lexicográfica é a
	// cronológica, então ORDER BY e BETWEEN continuam funcionando.
	OpeningDate string `gorm:"type:varchar(10);not null"`

	// --- schema v3 (importação e fatura de cartão) ---

	// Institution identifica a instituição para a importação escolher o
	// leiaute. O default no nível da COLUNA não é decoração: é ele que faz o
	// AutoMigrate preencher as contas que já existem em vez de deixá-las com
	// string vazia numa coluna NOT NULL (mesma lição de households.timezone).
	Institution string `gorm:"type:varchar(20);not null;default:'other'"`

	// StatementClosingDay e StatementDueDay são o dia do mês em que a fatura
	// do cartão fecha e vence. São ANULÁVEIS porque só existem em conta de
	// cartão, e "não se aplica" é diferente de "dia zero".
	StatementClosingDay *int `gorm:"type:int"`
	StatementDueDay     *int `gorm:"type:int"`

	ArchivedAt *time.Time `gorm:"index:ix_accounts_household_archived,priority:2"`
	CreatedAt  time.Time  `gorm:"not null"`
	UpdatedAt  time.Time  `gorm:"not null"`
	DeletedAt  *time.Time `gorm:"index:ix_accounts_deleted_at"`
}

// TableName fixa o nome da tabela.
func (Account) TableName() string { return "accounts" }

// Category é o modelo de persistência de internal/category.Category.
//
// ParentID nulo = grupo (nível 1); preenchido = folha (nível 2). Não há
// terceiro nível (ADR-017b), então nenhuma consulta precisa de CTE recursiva —
// que é o que quebraria a portabilidade entre os quatro dialetos.
//
// Também aqui a unicidade do nome entre irmãos NÃO é índice único: o escopo
// inclui parent_id, que é anulável, e o MSSQL trata NULLs como IGUAIS em
// índice único (armadilha P3) — só um grupo por casa passaria.
type Category struct {
	ID          string  `gorm:"type:varchar(36);primaryKey"`
	HouseholdID string  `gorm:"type:varchar(36);not null;index:ix_categories_household_parent,priority:1;index:ix_categories_household_norm,priority:1"`
	ParentID    *string `gorm:"type:varchar(36);index:ix_categories_household_parent,priority:2"`
	Name        string  `gorm:"type:varchar(60);not null"`
	NameNorm    string  `gorm:"type:varchar(60);not null;index:ix_categories_household_norm,priority:2"`
	Kind        string  `gorm:"type:varchar(10);not null"`
	ArchivedAt  *time.Time
	CreatedAt   time.Time  `gorm:"not null"`
	UpdatedAt   time.Time  `gorm:"not null"`
	DeletedAt   *time.Time `gorm:"index:ix_categories_deleted_at"`
}

// TableName fixa o nome da tabela.
func (Category) TableName() string { return "categories" }

// CategoryKeyword é UMA palavra-chave de categoria (internal/category.Keyword)
// — schema v4, spec 0005, ADR-026(d).
//
// Tabela própria, e não JSON na categoria: a unicidade "por casa, dentro do
// tipo" precisa de índice único PORTÁTIL, e JSON não é consultável igual nos
// quatro dialetos. Todas as colunas do índice único são NOT NULL (armadilha
// P3: o MSSQL trata NULLs como iguais em índice único) e não há índice parcial
// (P4: o MySQL não tem). Sem deleted_at: a lista é SUBSTITUÍDA a cada PATCH e
// apagada fisicamente na exclusão da categoria — palavra-chave não é dado
// financeiro, é configuração.
//
// Largura do índice único (household_id, keyword_norm): 36 + 40 caracteres.
// MySQL/InnoDB utf8mb4 → 304 bytes (< 3072 do formato DYNAMIC); SQL Server →
// 76 bytes em varchar, 152 em nvarchar (< 1700 de índice não clusterizado).
// PostgreSQL e SQLite não têm limite relevante. Sem prefixo de índice.
type CategoryKeyword struct {
	ID string `gorm:"type:varchar(36);primaryKey"`
	// HouseholdID é repetido de propósito, mesmo estando em categories: o
	// filtro de isolamento nunca depende de join, e é ele que abre o índice
	// único — a mesma palavra em duas casas diferentes é legítima.
	HouseholdID string `gorm:"type:varchar(36);not null;uniqueIndex:ux_category_keywords_norm,priority:1;index:ix_category_keywords_cat,priority:1"`
	CategoryID  string `gorm:"type:varchar(36);not null;index:ix_category_keywords_cat,priority:2"`
	// Keyword é a forma EXIBÍVEL (caixa e acentos como a pessoa digitou);
	// KeywordNorm é a forma de comparação (textnorm.Normalize), e é ela que o
	// índice único compara.
	Keyword     string `gorm:"type:varchar(40);not null"`
	KeywordNorm string `gorm:"type:varchar(40);not null;uniqueIndex:ux_category_keywords_norm,priority:2"`
	// Position preserva a ordem de cadastro (0..19): UUID v7 não garante ordem
	// entre ids gerados no mesmo milissegundo, e a tela devolve a lista na
	// ordem em que foi digitada. O default de COLUNA garante zero — não nulo —
	// nas linhas antigas se a coluna aparecer depois.
	Position  int       `gorm:"type:int;not null;default:0"`
	CreatedAt time.Time `gorm:"not null"`
}

// TableName fixa o nome da tabela.
func (CategoryKeyword) TableName() string { return "category_keywords" }

// AccountKeyword é UMA palavra-chave de conta (internal/account.Keyword) —
// schema v4, spec 0005, ADR-026(d).
//
// Conjunto INDEPENDENTE do de categoria: o índice único é desta tabela, então
// a mesma palavra pode existir numa categoria E numa conta da mesma casa. As
// decisões de desenho são as de CategoryKeyword, inclusive a largura do índice.
type AccountKeyword struct {
	ID          string    `gorm:"type:varchar(36);primaryKey"`
	HouseholdID string    `gorm:"type:varchar(36);not null;uniqueIndex:ux_account_keywords_norm,priority:1;index:ix_account_keywords_acc,priority:1"`
	AccountID   string    `gorm:"type:varchar(36);not null;index:ix_account_keywords_acc,priority:2"`
	Keyword     string    `gorm:"type:varchar(40);not null"`
	KeywordNorm string    `gorm:"type:varchar(40);not null;uniqueIndex:ux_account_keywords_norm,priority:2"`
	Position    int       `gorm:"type:int;not null;default:0"`
	CreatedAt   time.Time `gorm:"not null"`
}

// TableName fixa o nome da tabela.
func (AccountKeyword) TableName() string { return "account_keywords" }

// Transaction é o modelo de persistência de internal/transaction.Transaction
// (schema v3).
//
// Três decisões deste modelo que merecem ser lidas antes de mexer nele:
//
//  1. **amount_cents é sempre positivo** e o sinal vem de kind. Guardar o
//     sinal no número faria toda soma depender de o serviço ter acertado o
//     sinal na escrita — e um erro desses só aparece no total do mês.
//
//  2. **dedup_key é NOT NULL e a unicidade é (household_id, dedup_key,
//     dedup_ordinal)**, não (household_id, external_id). Índice único sobre
//     coluna anulável é a armadilha P3: o MSSQL trata NULLs como IGUAIS, então
//     o primeiro lançamento sem external_id passaria e TODOS os outros seriam
//     recusados. Um índice único PARCIAL (só onde external_id não é nulo)
//     resolveria em Postgres e SQLite, mas o MySQL não tem índice parcial
//     (P4) e o AutoMigrate não sabe expressá-lo de forma portátil. Por isso a
//     chave é um hash sempre preenchido, calculado no serviço.
//
//  3. **Todos os índices são ascendentes.** Ordem descendente na DEFINIÇÃO do
//     índice não é portátil (e o MySQL a aceitou como ruído até a 8.0); os
//     quatro dialetos varrem um índice ascendente de trás para frente sem
//     custo, então ORDER BY ... DESC continua indexado.
type Transaction struct {
	ID          string `gorm:"type:varchar(36);primaryKey;index:ix_transactions_occurred,priority:3"`
	HouseholdID string `gorm:"type:varchar(36);not null;uniqueIndex:ux_transactions_dedup,priority:1;index:ix_transactions_occurred,priority:1;index:ix_transactions_account_occurred,priority:1;index:ix_transactions_competence,priority:1;index:ix_transactions_statement,priority:1;index:ix_transactions_group,priority:1;index:ix_transactions_category,priority:1"`
	Kind        string `gorm:"type:varchar(12);not null"` // income | expense | transfer_out | transfer_in
	AccountID   string `gorm:"type:varchar(36);not null;index:ix_transactions_account_occurred,priority:2"`
	// CategoryID é anulável: lançamento importado nasce sem categoria, e
	// "sem categoria" é um estado legítimo que a tela mostra como pendência.
	//
	// O índice existe porque duas perguntas frequentes passam por aqui: o 422
	// de "categoria em uso" (ExistsByCategory), que é caminho de usuário, e o
	// acompanhamento de orçamento por categoria da E5. Sem ele, as duas varrem
	// a casa inteira. Anulável em índice COMUM não tem o problema do P3 — o que
	// o MSSQL trata mal é NULL em índice ÚNICO, e este não é.
	CategoryID  *string `gorm:"type:varchar(36);index:ix_transactions_category,priority:2"`
	AmountCents int64   `gorm:"not null"` // BIGINT em centavos, SEMPRE positivo
	Description string  `gorm:"type:varchar(140);not null"`
	// DescriptionNorm sustenta a busca insensível a acento e caixa nos quatro
	// dialetos (armadilha P2) — LIKE se comporta de um jeito em cada um.
	DescriptionNorm string `gorm:"type:varchar(140);not null"`
	// OccurredOn é data civil em TEXTO "YYYY-MM-DD" (D3 da spec 0003): largura
	// fixa, então ordem lexicográfica é ordem cronológica, e nenhum driver
	// converte fuso no caminho.
	OccurredOn string `gorm:"type:varchar(10);not null;index:ix_transactions_occurred,priority:2;index:ix_transactions_account_occurred,priority:3"`
	// YearMonth é o mês de CAIXA e CompetenceMonth o mês de COMPETÊNCIA (P1:
	// a chave de agregação vem pronta da aplicação porque extrair mês tem
	// quatro sintaxes diferentes). Os dois são NOT NULL: competência vazia
	// seria um mês de relatório desaparecendo em silêncio.
	YearMonth       string  `gorm:"type:varchar(7);not null"`
	CompetenceMonth string  `gorm:"type:varchar(7);not null;index:ix_transactions_competence,priority:2"`
	TransferGroupID *string `gorm:"type:varchar(36);index:ix_transactions_group,priority:2"`
	StatementID     *string `gorm:"type:varchar(36);index:ix_transactions_statement,priority:2"`
	Source          string  `gorm:"type:varchar(12);not null"` // manual | import
	// ImportBatchID tem índice SEM household_id à frente de propósito: ele só
	// é consultado por id de lote, que já é único no universo, e o lote já
	// carrega a casa.
	ImportBatchID *string `gorm:"type:varchar(36);index:ix_transactions_batch"`
	ExternalID    *string `gorm:"type:varchar(64)"`
	DedupKey      string  `gorm:"type:varchar(64);not null;uniqueIndex:ux_transactions_dedup,priority:2"`
	// DedupOrdinal desempata repetições legítimas (duas compras idênticas no
	// mesmo dia acontecem). Começa em 1 — o default de COLUNA existe para que
	// uma escrita que esqueça o campo não crie um zero fantasma.
	DedupOrdinal int       `gorm:"type:int;not null;default:1;uniqueIndex:ux_transactions_dedup,priority:3"`
	CreatedBy    string    `gorm:"type:varchar(36);not null"`
	CreatedAt    time.Time `gorm:"not null"`
	UpdatedAt    time.Time `gorm:"not null"`
	// DeletedAt é *time.Time, e não gorm.DeletedAt, porque o projeto filtra a
	// exclusão lógica EXPLICITAMENTE no scope de cada repositório: soft delete
	// automático some da leitura do código e, quando falha, falha calado.
	DeletedAt *time.Time `gorm:"index:ix_transactions_deleted_at"`
}

// TableName fixa o nome da tabela.
func (Transaction) TableName() string { return "transactions" }

// CardStatement é a fatura de cartão (internal/cardstatement.Statement).
//
// Não há total_cents, paid_cents nem status: são DERIVADOS dos lançamentos
// ligados à fatura (disciplina do ADR-017). Materializar dinheiro em coluna é
// a origem clássica do número errado — basta um caminho de escrita esquecido.
type CardStatement struct {
	ID string `gorm:"type:varchar(36);primaryKey"`
	// Não existe um `ix_card_statements_account` ao lado do índice único: ele
	// teria exatamente as mesmas colunas, na mesma ordem, e um índice único
	// atende a toda consulta que o comum atenderia. Seria só custo de escrita —
	// e caro de desfazer, porque o AutoMigrate cria índice mas nunca remove
	// (ADR-008), então tirá-lo depois exigiria o passo manual que este projeto
	// existe para evitar. A hora de não criar é agora.
	HouseholdID string `gorm:"type:varchar(36);not null;uniqueIndex:ux_card_statements,priority:1"`
	AccountID   string `gorm:"type:varchar(36);not null;uniqueIndex:ux_card_statements,priority:2"`
	// CompetenceMonth é o mês do VENCIMENTO — é o que o usuário chama de "a
	// fatura de fevereiro". As três colunas da chave única são NOT NULL: é o
	// que permite que ela seja um índice único de verdade (P3).
	CompetenceMonth string    `gorm:"type:varchar(7);not null;uniqueIndex:ux_card_statements,priority:3"`
	ClosingDate     string    `gorm:"type:varchar(10);not null"`
	DueDate         string    `gorm:"type:varchar(10);not null"`
	Source          string    `gorm:"type:varchar(12);not null"`
	CreatedAt       time.Time `gorm:"not null"`
	UpdatedAt       time.Time `gorm:"not null"`
	DeletedAt       *time.Time
}

// TableName fixa o nome da tabela.
func (CardStatement) TableName() string { return "card_statements" }

// ImportBatch é uma tentativa de importação (internal/importer.Batch).
//
// O arquivo enviado NÃO é guardado: sobrevivem o nome, o hash do conteúdo e as
// linhas já interpretadas. O hash é o que permite avisar "você já importou
// este arquivo" sem manter o documento — que carrega dado de terceiros.
type ImportBatch struct {
	ID          string `gorm:"type:varchar(36);primaryKey"`
	HouseholdID string `gorm:"type:varchar(36);not null;index:ix_import_batches_household,priority:1;index:ix_import_batches_content,priority:1"`
	AccountID   string `gorm:"type:varchar(36);not null"`
	CreatedBy   string `gorm:"type:varchar(36);not null"`

	Institution   string `gorm:"type:varchar(20);not null"`
	DocKind       string `gorm:"type:varchar(16);not null"`
	FormatID      string `gorm:"type:varchar(40);not null"`
	FileName      string `gorm:"type:varchar(200);not null"`
	ContentSHA256 string `gorm:"type:varchar(64);not null;index:ix_import_batches_content,priority:2"`

	// Encoding é como os bytes do CSV foram interpretados. O DEFAULT de coluna
	// existe para o AutoMigrate poder acrescentá-la a uma tabela já povoada
	// sem deixar linha antiga com NULL numa coluna NOT NULL (ADR-008).
	Encoding string `gorm:"type:varchar(16);not null;default:'utf-8'"`

	// Contadores em int (não bigint): são contagens de linhas de um arquivo,
	// e o default de COLUNA garante zero — não nulo — nas linhas antigas se um
	// contador novo aparecer depois.
	RowCount      int `gorm:"type:int;not null;default:0"`
	ImportedCount int `gorm:"type:int;not null;default:0"`
	SkippedCount  int `gorm:"type:int;not null;default:0"`
	BlockedCount  int `gorm:"type:int;not null;default:0"`
	RestoredCount int `gorm:"type:int;not null;default:0"`
	RejectedCount int `gorm:"type:int;not null;default:0"`

	// --- schema v4 (spec 0005, ADR-026g): contadores que deixaram de ser
	// deriváveis dos lançamentos. O `link` move o import_batch_id de uma perna
	// já existente para o lote que a vinculou, e derivar "pares criados" de
	// import_batch_id passaria a mentir para os DOIS lotes. Default de COLUNA
	// para o AutoMigrate preencher as linhas antigas com 0, não NULL.
	LinkedCount        int `gorm:"type:int;not null;default:0"`
	TransferPairsCount int `gorm:"type:int;not null;default:0"`

	// MinDate e MaxDate delimitam o documento; string vazia = sem linha
	// aproveitável (a data zero de civil.Date também é a string vazia, então
	// não existe "0000-00-00" — que o MySQL recusa com NO_ZERO_DATE).
	MinDate string `gorm:"type:varchar(10);not null"`
	MaxDate string `gorm:"type:varchar(10);not null"`

	// Suggested* são palpites da leitura, anuláveis, e nenhum entra em índice
	// único (P3).
	SuggestedCompetenceMonth *string `gorm:"type:varchar(7)"`
	SuggestedClosingDate     *string `gorm:"type:varchar(10)"`
	SuggestedDueDate         *string `gorm:"type:varchar(10)"`

	Status      string    `gorm:"type:varchar(12);not null"` // pending | committed | discarded | expired
	ExpiresAt   time.Time `gorm:"not null;index:ix_import_batches_expires"`
	CommittedAt *time.Time
	CreatedAt   time.Time `gorm:"not null;index:ix_import_batches_household,priority:2"`
	UpdatedAt   time.Time `gorm:"not null"`
}

// TableName fixa o nome da tabela.
func (ImportBatch) TableName() string { return "import_batches" }

// ImportRow é UMA linha já interpretada do documento (internal/importer.Row).
//
// Não existe coluna com a linha crua. Guardá-la traria CPF, CNPJ, agência e
// conta de terceiros para uma tabela nova, com outro ciclo de vida — risco
// novo sem ganho, porque a revisão só mostra o que já está nas colunas abaixo.
// reject_reason é um CÓDIGO curto pelo mesmo motivo: campo livre vira o lugar
// onde o documento acaba copiado.
type ImportRow struct {
	ID string `gorm:"type:varchar(36);primaryKey"`
	// HouseholdID é repetido de propósito, mesmo estando em import_batches:
	// o filtro de isolamento NUNCA pode depender de um join (quem esquece o
	// join devolve tudo).
	HouseholdID string `gorm:"type:varchar(36);not null;index:ix_import_rows_batch,priority:1"`
	BatchID     string `gorm:"type:varchar(36);not null;uniqueIndex:ux_import_rows_seq,priority:1;index:ix_import_rows_batch,priority:2"`
	// (batch_id, seq) é único e as duas colunas são NOT NULL: é o que impede
	// a mesma linha do arquivo de ser gravada duas vezes por um reenvio.
	Seq    int `gorm:"type:int;not null;uniqueIndex:ux_import_rows_seq,priority:2;index:ix_import_rows_batch,priority:3"`
	LineNo int `gorm:"type:int;not null"`

	Kind            string  `gorm:"type:varchar(12);not null"`
	OccurredOn      string  `gorm:"type:varchar(10);not null"`
	AmountCents     int64   `gorm:"not null"`
	Description     string  `gorm:"type:varchar(140);not null"`
	DescriptionNorm string  `gorm:"type:varchar(140);not null"`
	ExternalID      *string `gorm:"type:varchar(64)"`
	DedupKey        string  `gorm:"type:varchar(64);not null"`

	Status             string  `gorm:"type:varchar(24);not null"`
	RejectReason       *string `gorm:"type:varchar(32)"`
	MatchTransactionID *string `gorm:"type:varchar(36)"`

	// --- schema v4 (spec 0005): sugestões da análise por palavra-chave.
	// Todas ANULÁVEIS — são palpites, e "sem sugestão" é diferente de
	// "sugestão vazia" — e nenhuma entra em índice (P3 não se aplica).
	// MatchedKeyword guarda a forma EXIBÍVEL da palavra que decidiu, nunca a
	// norm: é o que a tela mostra ("88% · supermercado").
	SuggestedCategoryID           *string `gorm:"type:varchar(36)"`
	MatchScore                    *int    `gorm:"type:int"`
	MatchedKeyword                *string `gorm:"type:varchar(40)"`
	SuggestedCounterpartAccountID *string `gorm:"type:varchar(36)"`

	CreatedAt time.Time `gorm:"not null"`
}

// TableName fixa o nome da tabela.
func (ImportRow) TableName() string { return "import_rows" }

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
		&Account{},
		&Category{},
		&CategoryKeyword{},
		&AccountKeyword{},
		&Transaction{},
		&CardStatement{},
		&ImportBatch{},
		&ImportRow{},
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
		Account{}.TableName(),
		Category{}.TableName(),
		CategoryKeyword{}.TableName(),
		AccountKeyword{}.TableName(),
		Transaction{}.TableName(),
		CardStatement{}.TableName(),
		ImportBatch{}.TableName(),
		ImportRow{}.TableName(),
	}
}
