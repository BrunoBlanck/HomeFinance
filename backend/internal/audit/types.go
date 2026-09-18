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

// Ações do domínio financeiro (§4.7 do PLANOS.md: toda escrita financeira
// gera entrada).
//
// Repare no que NÃO existe: nenhuma constante carrega valor. A auditoria
// guarda QUEM mexeu em QUAL entidade e QUANDO — o histórico do dado está no
// próprio dado (soft delete + updated_at), e log é superfície de vazamento
// (S8 do PLANOS.md). Por isso não há "conta.saldo_alterado_para_X".
const (
	ActionAccountCreated     = "account.created"
	ActionAccountUpdated     = "account.updated"
	ActionAccountArchived    = "account.archived"
	ActionAccountUnarchived  = "account.unarchived"
	ActionAccountDeleted     = "account.deleted"
	ActionCategoryCreated    = "category.created"
	ActionCategoryUpdated    = "category.updated"
	ActionCategoryArchived   = "category.archived"
	ActionCategoryUnarchived = "category.unarchived"
	ActionCategoryDeleted    = "category.deleted"

	// Lançamento: só as escritas AVULSAS têm ação própria.
	//
	// Não existe "transaction.created": a confirmação de uma importação de
	// 10.000 linhas gera UMA entrada (import.confirmed, entidade
	// import_batch), e não 10.000 (spec 0004 §6.10). Dez mil linhas de
	// auditoria por importação afogariam justamente o rastro que a auditoria
	// existe para preservar, e a rastreabilidade por lançamento já está no
	// próprio dado — import_batch_id, created_by e created_at são mais
	// precisos do que a auditoria seria.
	ActionTransactionDeleted = "transaction.deleted"

	// ActionTransactionUpdated registra a edição AVULSA de um lançamento —
	// nesta entrega, só a troca de categoria por PATCH /transactions/{id}
	// (spec 0005 §11). O id é o do lançamento; como em toda ação, sem valor,
	// sem descrição e sem a categoria escolhida: o QUÊ mudou está no próprio
	// dado (category_id + updated_at), e aqui fica só QUEM e QUANDO.
	ActionTransactionUpdated = "transaction.updated"

	// ActionTransactionRestored registra a exceção estreita do ADR-025(f):
	// liberar uma linha cuja gêmea está excluída RESTAURA a existente, em vez
	// de inserir outra. A restauração mexe em deleted_at e updated_at e em
	// mais nada — e é por ser exceção que ela tem ação própria.
	ActionTransactionRestored = "transaction.restored"

	// ActionTransactionAutoCategorized registra UMA execução real de
	// POST /transactions/auto-categorize (spec 0005 §4.3, ADR-026h): a entidade
	// é o MÊS (EntityTransactionMonth) e o id é "AAAA-MM". Uma entrada por
	// execução, e não uma por lançamento categorizado — o mesmo desvio da
	// importação (§6.10 da spec 0004). As contagens ficam na resposta e no
	// log; aqui não há campo de detalhe, de propósito (emenda §10.5 da spec).
	// A prévia (dryRun) não gera entrada: não escreveu nada.
	ActionTransactionAutoCategorized = "transaction.auto_categorized"

	// ActionTransactionTransfersDetected registra UMA execução real de
	// POST /transfers/detect (spec 0005 §13, ADR-028e): a entidade é o MÊS
	// (EntityTransactionMonth) e o id é "AAAA-MM". Uma entrada por execução,
	// e não uma por par convertido — a mesma decisão do auto-categorize. As
	// contagens (paired, unpaired) ficam na resposta e no log; descrição,
	// valor e palavra-chave nunca entram aqui. A prévia (dryRun) não gera
	// entrada: não escreveu nada.
	ActionTransactionTransfersDetected = "transaction.transfers_detected"

	// ActionTransactionInvestmentsDetected registra UMA execução real de
	// POST /investments/detect (spec 0006 §3.3.6, ADR-029h): a entidade é o
	// MÊS (EntityTransactionMonth) e o id é "AAAA-MM". Uma entrada por
	// execução, e não uma por lançamento marcado — a mesma decisão do
	// auto-categorize e do transfers/detect.
	//
	// As contagens (marked, unmatched, alreadyCategorized) ficam na resposta e
	// no log estruturado da borda; aqui não há campo de detalhe, de propósito.
	// Descrição de lançamento, valor e palavra-chave NUNCA entram — e é
	// justamente por não existir o campo que ninguém consegue escrevê-los.
	//
	// Ela cobre a execução que só PREENCHE o que estava vazio. A execução que
	// SUBSTITUI categoria já escolhida tem ação própria — ver a seguir.
	ActionTransactionInvestmentsDetected = "transaction.investments_detected"

	// ActionTransactionInvestmentsOverwritten registra a execução real de
	// POST /investments/detect com `overwriteCategorized: true` — a ÚNICA
	// escrita do projeto autorizada a substituir categoria escolhida por uma
	// pessoa, em massa e sem desfazer.
	//
	// Ação separada, e não um campo, por duas razões. A primeira é de
	// PERÍCIA: sem ela, "preencheu 500 lançamentos vazios" e "trocou 500
	// categorias que alguém escolheu à mão" são a mesma linha de auditoria, e
	// a diferença entre as duas só existiria no log de aplicação — que tem
	// retenção própria e não é o rastro. A segunda é de FORMA: `action` já é
	// coluna indexada, então "toda substituição em massa desta casa" é uma
	// consulta por índice, enquanto um booleano novo em `audit_log` seria uma
	// coluna nula em todas as outras ações e o primeiro campo de detalhe de
	// uma tabela que não tem nenhum de propósito (é a ausência de campo livre
	// que impede alguém de escrever um código OTP aqui).
	//
	// O que vale para a irmã vale para esta: UMA entrada por execução, a
	// entidade é o MÊS, e nada de contagem, descrição, valor ou palavra-chave.
	ActionTransactionInvestmentsOverwritten = "transaction.investments_overwritten"

	// ActionTransactionImportLinked registra a ação `link` da importação
	// (ADR-026f): a perna de transferência JÁ EXISTENTE recebeu a identidade
	// de deduplicação e o lote da linha do arquivo. Escrita avulsa numa linha
	// existente, por isso tem entrada própria, com o id da perna — nunca a
	// chave nem o identificador externo.
	ActionTransactionImportLinked = "transaction.import_linked"

	// ActionCardStatementCreated marca o nascimento da fatura. Reimportar a
	// mesma fatura REUSA a existente (índice único de casa + conta +
	// competência), e reúso não é criação: não gera entrada nova.
	ActionCardStatementCreated = "card_statement.created"

	// Importação: UMA entrada por LOTE, e este é o desvio declarado da §6.10
	// da spec 0004.
	//
	// Confirmar um lote de 10.000 linhas grava UMA entrada import.confirmed —
	// nunca 10.000 transaction.created, que afogariam justamente o rastro que
	// a auditoria existe para preservar. A rastreabilidade por lançamento já
	// está no dado (transactions.import_batch_id + created_by + created_at), e
	// ela é mais precisa do que a auditoria seria.
	//
	// Escrita AVULSA continua tendo entrada própria: excluir um lançamento é
	// transaction.deleted, e restaurar um (ADR-025f) é transaction.restored,
	// inclusive quando quem restaura é a importação.
	ActionImportCreated   = "import.created"
	ActionImportConfirmed = "import.confirmed"
	ActionImportDiscarded = "import.discarded"
)

// Entidades auditadas.
const (
	EntityUser          = "user"
	EntityHousehold     = "household"
	EntitySession       = "session"
	EntityAccount       = "account"
	EntityCategory      = "category"
	EntityTransaction   = "transaction"
	EntityCardStatement = "card_statement"

	// EntityTransactionMonth é o MÊS de competência como entidade — a do
	// auto-categorize, cujo id é "AAAA-MM". Existe porque a execução real toca
	// N lançamentos de uma vez e a auditoria guarda uma entrada por execução;
	// o "quê" auditado é o mês, e o quanto está no próprio dado (updated_at).
	EntityTransactionMonth = "transaction_month"

	// EntityImportBatch é o LOTE de importação — a entidade das três ações
	// import.*. O id registrado é o do lote, que é por onde se chega a todos
	// os lançamentos que ele gerou.
	EntityImportBatch = "import_batch"
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
