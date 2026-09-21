package main

import (
	"context"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/aiimport"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// auditBridge adapta o audit.Service às interfaces `Auditor` declaradas pelos
// pacotes de domínio.
//
// A ponte existe para que `account` e `category` não importem `audit`: cada um
// declara a interface mínima que consome, e a composição acontece aqui, no
// único lugar do projeto que conhece todos os pacotes. É o mesmo desenho dos
// repositórios (interface no domínio, implementação em `platform/storage`).
type auditBridge struct{ svc *audit.Service }

var (
	_ account.Auditor       = auditBridge{}
	_ category.Auditor      = categoryAuditBridge{}
	_ transaction.Auditor   = transactionAuditBridge{}
	_ cardstatement.Auditor = cardStatementAuditBridge{}
	_ importer.Auditor      = importAuditBridge{}
	_ investment.Auditor    = investmentAuditBridge{}
	_ aiimport.Auditor      = aiImportAuditBridge{}
)

// Record grava o evento de conta.
//
// Usa `Record` e não `TryRecord`: a chamada acontece dentro da transação da
// escrita financeira, e o §4.7 do PLANOS.md diz que TODA escrita financeira
// gera entrada. Se a auditoria não couber, a escrita não vale — o contrário
// deixaria dinheiro se mexendo sem rastro, que é exatamente o que a auditoria
// existe para impedir.
//
// A diferença para os eventos de autenticação (que usam `TryRecord`) é
// deliberada: lá, uma auditoria indisponível não pode virar negação de login.
func (b auditBridge) Record(ctx context.Context, p account.AuditParams) error {
	return b.svc.Record(ctx, audit.Params{
		Action:      p.Action,
		Entity:      p.Entity,
		EntityID:    p.EntityID,
		UserID:      p.UserID,
		HouseholdID: p.HouseholdID,
		IP:          p.IP,
	})
}

// categoryAuditBridge existe porque Go não permite um mesmo método com dois
// tipos de parâmetro. Os dois structs de parâmetro são idênticos de propósito:
// cada domínio declara o seu, sem depender do outro.
type categoryAuditBridge struct{ svc *audit.Service }

func (b categoryAuditBridge) Record(ctx context.Context, p category.AuditParams) error {
	return b.svc.Record(ctx, audit.Params{
		Action:      p.Action,
		Entity:      p.Entity,
		EntityID:    p.EntityID,
		UserID:      p.UserID,
		HouseholdID: p.HouseholdID,
		IP:          p.IP,
	})
}

// transactionAuditBridge registra as escritas AVULSAS de lançamento.
//
// Avulsas, e só elas: não existe "transaction.created". A confirmação de uma
// importação de 10.000 linhas gera UMA entrada (import.confirmed), e não
// 10.000 — o desvio declarado da §6.10 da spec 0004. O que passa por aqui é
// transaction.deleted e transaction.restored, que são decisões individuais de
// uma pessoa e merecem cada uma a sua linha.
type transactionAuditBridge struct{ svc *audit.Service }

func (b transactionAuditBridge) Record(ctx context.Context, p transaction.AuditParams) error {
	return b.svc.Record(ctx, audit.Params{
		Action:      p.Action,
		Entity:      p.Entity,
		EntityID:    p.EntityID,
		UserID:      p.UserID,
		HouseholdID: p.HouseholdID,
		IP:          p.IP,
	})
}

// investmentAuditBridge registra UMA execução real de POST /investments/detect
// (ADR-029h).
//
// A entidade é o MÊS e o id é "AAAA-MM": a execução toca N lançamentos de uma
// vez, e uma entrada por lançamento afogaria o rastro — a mesma decisão do
// auto-categorize e do transfers/detect. As contagens não passam por aqui
// porque audit.Params não tem campo de detalhe, de propósito: é o que garante
// que descrição, valor e palavra-chave nunca entrem no rastro.
type investmentAuditBridge struct{ svc *audit.Service }

func (b investmentAuditBridge) Record(ctx context.Context, p investment.AuditParams) error {
	return b.svc.Record(ctx, audit.Params{
		Action:      p.Action,
		Entity:      p.Entity,
		EntityID:    p.EntityID,
		UserID:      p.UserID,
		HouseholdID: p.HouseholdID,
		IP:          p.IP,
	})
}

// cardStatementAuditBridge registra o nascimento da fatura. Reimportar a mesma
// fatura REUSA a existente, e reúso não é criação: não gera entrada nova.
type cardStatementAuditBridge struct{ svc *audit.Service }

func (b cardStatementAuditBridge) Record(ctx context.Context, p cardstatement.AuditParams) error {
	return b.svc.Record(ctx, audit.Params{
		Action:      p.Action,
		Entity:      p.Entity,
		EntityID:    p.EntityID,
		UserID:      p.UserID,
		HouseholdID: p.HouseholdID,
		IP:          p.IP,
	})
}

// importAuditBridge registra o LOTE: import.created, import.confirmed e
// import.discarded — uma entrada por lote, sem valor monetário nenhum (S8).
type importAuditBridge struct{ svc *audit.Service }

func (b importAuditBridge) Record(ctx context.Context, p importer.AuditParams) error {
	return b.svc.Record(ctx, audit.Params{
		Action:      p.Action,
		Entity:      p.Entity,
		EntityID:    p.EntityID,
		UserID:      p.UserID,
		HouseholdID: p.HouseholdID,
		IP:          p.IP,
	})
}

// aiImportAuditBridge registra UMA execução real de
// POST /ai/keyword-import/confirm (spec 0010, achado A8 da emenda §10): a
// entidade é a CASA e o id é o household_id. É o registro de ORIGEM das
// `category.created`, `category.updated` e `account.updated` que os serviços
// de categoria e de conta gravam na mesma transação — nenhuma delas sabe
// dizer que veio do import de IA. As palavras nunca entram no log.
type aiImportAuditBridge struct{ svc *audit.Service }

func (b aiImportAuditBridge) Record(ctx context.Context, p aiimport.AuditParams) error {
	return b.svc.Record(ctx, audit.Params{
		Action:      p.Action,
		Entity:      p.Entity,
		EntityID:    p.EntityID,
		UserID:      p.UserID,
		HouseholdID: p.HouseholdID,
		IP:          p.IP,
	})
}
