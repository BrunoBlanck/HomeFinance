package account_test

import (
	"context"
	"errors"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auditorFake grava em memória e pode falhar sob demanda.
type auditorFake struct {
	registros []account.AuditParams
	falha     error
}

func (a *auditorFake) Record(_ context.Context, p account.AuditParams) error {
	if a.falha != nil {
		return a.falha
	}
	a.registros = append(a.registros, p)
	return nil
}

func (a *auditorFake) acoes() []string {
	out := make([]string, 0, len(a.registros))
	for _, r := range a.registros {
		out = append(out, r.Action)
	}
	return out
}

// §4.7 do PLANOS.md: TODA escrita financeira gera entrada em audit_log.
func TestTodaEscritaDeContaGeraAuditoria(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	auditor := &auditorFake{}
	svc := novoServico(t, repo, account.WithAudit(auditor))
	ctx := t.Context()
	quemAge := ator(minhaCasa)

	criada, err := svc.Create(ctx, quemAge, entradaValida())
	require.NoError(t, err)

	novoNome := "Conta Nova"
	_, err = svc.Update(ctx, quemAge, criada.ID, account.UpdateInput{Name: &novoNome})
	require.NoError(t, err)

	_, err = svc.Archive(ctx, quemAge, criada.ID)
	require.NoError(t, err)
	_, err = svc.Unarchive(ctx, quemAge, criada.ID)
	require.NoError(t, err)
	require.NoError(t, svc.Delete(ctx, quemAge, criada.ID))

	assert.Equal(t, []string{
		audit.ActionAccountCreated,
		audit.ActionAccountUpdated,
		audit.ActionAccountArchived,
		audit.ActionAccountUnarchived,
		audit.ActionAccountDeleted,
	}, auditor.acoes())
}

// A entrada diz QUEM, ONDE e DE QUAL IP — e nada além disso.
func TestAuditoriaGuardaAtorSemGuardarValor(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	auditor := &auditorFake{}
	svc := novoServico(t, repo, account.WithAudit(auditor))

	criada, err := svc.Create(t.Context(), ator(minhaCasa), entradaValida())
	require.NoError(t, err)

	require.Len(t, auditor.registros, 1)
	registro := auditor.registros[0]

	assert.Equal(t, audit.EntityAccount, registro.Entity)
	assert.Equal(t, criada.ID, registro.EntityID)
	assert.Equal(t, minhaCasa, registro.HouseholdID)
	assert.Equal(t, "user-1", registro.UserID)
	assert.Equal(t, "203.0.113.10", registro.IP)

	// S8 do PLANOS.md: a auditoria guarda ação e entidade, NÃO valor. Não
	// existe campo livre onde um centavo possa entrar — e este teste é o que
	// impede alguém de somar um "detalhe" depois.
	assert.NotContains(t, registro.Action, "15000")
	assert.NotContains(t, registro.EntityID, "15000")
}

// A auditoria roda DENTRO da transação da escrita. Se ela falhar, a escrita
// inteira precisa cair — senão o dinheiro se mexe sem rastro, que é exatamente
// o que a auditoria existe para impedir.
//
// O dublê de transação deste pacote executa direto (sem rollback de verdade);
// o que este teste garante é o ELO: o erro do auditor sobe e vira erro da
// operação, em vez de ser engolido. O rollback em si é do UnitOfWork, coberto
// nos testes de repositório contra o SQLite.
func TestFalhaDeAuditoriaAbortaAEscrita(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	auditor := &auditorFake{falha: errors.New("audit_log indisponível")}
	svc := novoServico(t, repo, account.WithAudit(auditor))

	_, err := svc.Create(t.Context(), ator(minhaCasa), entradaValida())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit_log indisponível")
}

// Sem auditor configurado o serviço funciona: é o que permite aos testes de
// unidade exercitarem regra de negócio sem montar a infraestrutura toda.
func TestSemAuditorOServicoContinuaFuncionando(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	_, err := svc.Create(t.Context(), ator(minhaCasa), entradaValida())
	assert.NoError(t, err)
}

// Leitura não é escrita: listar e buscar não sujam o rastro.
func TestLeituraNaoGeraAuditoria(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	auditor := &auditorFake{}
	svc := novoServico(t, repo, account.WithAudit(auditor))
	ctx := t.Context()
	quemAge := ator(minhaCasa)

	criada, err := svc.Create(ctx, quemAge, entradaValida())
	require.NoError(t, err)
	antes := len(auditor.registros)

	_, err = svc.Get(ctx, quemAge, criada.ID)
	require.NoError(t, err)
	_, err = svc.List(ctx, quemAge, true)
	require.NoError(t, err)

	assert.Len(t, auditor.registros, antes)
}
