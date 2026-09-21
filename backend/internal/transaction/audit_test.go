package transaction_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// auditorFake grava em memória e pode falhar sob demanda.
type auditorFake struct {
	registros []transaction.AuditParams
	falha     error
}

func (a *auditorFake) Record(_ context.Context, p transaction.AuditParams) error {
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

// §4.7 do PLANOS.md: toda escrita financeira AVULSA gera entrada.
func TestExclusaoERestauracaoGeramRastro(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	alvo := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 10_00, Description: "Café", OccurredOn: civil.MustNew(2026, 9, 10),
	})

	require.NoError(t, amb.svc.SoftDelete(t.Context(), ator(minhaCasa), alvo.ID))
	_, err := amb.svc.Restore(t.Context(), ator(minhaCasa), alvo.ID)
	require.NoError(t, err)

	assert.Equal(t, []string{
		audit.ActionTransactionDeleted,
		audit.ActionTransactionRestored,
	}, amb.auditor.acoes())

	registro := amb.auditor.registros[0]
	assert.Equal(t, audit.EntityTransaction, registro.Entity)
	assert.Equal(t, alvo.ID, registro.EntityID)
	assert.Equal(t, minhaCasa, registro.HouseholdID)
	assert.Equal(t, usuario, registro.UserID)
	assert.Equal(t, "203.0.113.10", registro.IP)
}

// O desvio declarado da §6.10: a importação de um lote gera UMA entrada
// (import.confirmed, do serviço de importação), e NÃO uma por lançamento. Dez
// mil linhas de auditoria por arquivo afogariam o rastro que a auditoria existe
// para preservar — a rastreabilidade por lançamento vive em import_batch_id,
// created_by e created_at, que são mais precisos.
func TestGravacaoEmLoteNaoGeraUmaEntradaPorLancamento(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)

	_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(
		linha("acc-1", 10_00, 3, "a"),
		linha("acc-1", 20_00, 4, "b"),
		linha("acc-1", 30_00, 5, "c"),
	))
	require.NoError(t, err)
	assert.Empty(t, amb.auditor.registros)
}

// A auditoria roda DENTRO da transação da escrita: se ela falhar, a escrita
// inteira precisa cair — senão o dinheiro se mexe sem rastro.
func TestFalhaDeAuditoriaAbortaAEscrita(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	alvo := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 10_00, Description: "Café", OccurredOn: civil.MustNew(2026, 9, 10),
	})
	amb.auditor.falha = errors.New("audit_log indisponível")

	err := amb.svc.SoftDelete(t.Context(), ator(minhaCasa), alvo.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit_log indisponível")
}

func TestLeituraNaoGeraAuditoria(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	alvo := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 10_00, Description: "Café", OccurredOn: civil.MustNew(2026, 9, 10),
	})

	_, err := amb.svc.ByID(t.Context(), ator(minhaCasa), alvo.ID)
	require.NoError(t, err)
	_, err = amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Empty(t, amb.auditor.registros)
}

// S8 do PLANOS.md: NENHUMA entrada de auditoria carrega valor monetário.
//
// Este teste não olha um caso: ele olha a FORMA dos structs por reflexão, em
// todos os pacotes que auditam. É a única maneira de impedir que alguém, daqui
// a três entregas, acrescente um "AmountCents int64" ou um "Detail string" com
// o total do lote dentro — e o teste continue passando porque ninguém escreveu
// um caso para o campo novo.
//
// A regra é dupla: nenhum campo numérico (onde um centavo caberia), e nenhum
// campo de texto livre fora da lista conhecida (onde um centavo caberia
// formatado). Campo novo aqui exige decisão consciente e ADR, não um commit.
func TestNenhumaEstruturaDeAuditoriaTemCampoDeValor(t *testing.T) {
	t.Parallel()

	permitidos := map[string]bool{
		"Action": true, "Entity": true, "EntityID": true,
		"UserID": true, "HouseholdID": true, "IP": true,
		"ID": true, "CreatedAt": true,
	}

	estruturas := map[string]any{
		"transaction.AuditParams":   transaction.AuditParams{},
		"cardstatement.AuditParams": cardstatement.AuditParams{},
		"account.AuditParams":       account.AuditParams{},
		"category.AuditParams":      category.AuditParams{},
		"audit.Params":              audit.Params{},
		"audit.Entry":               audit.Entry{},
	}

	for nome, valor := range estruturas {
		tipo := reflect.TypeOf(valor)
		for i := range tipo.NumField() {
			campo := tipo.Field(i)

			assert.True(t, permitidos[campo.Name],
				"%s.%s: campo novo em estrutura de auditoria — auditoria guarda QUEM mexeu em QUÊ, nunca QUANTO (S8)",
				nome, campo.Name)

			k := campo.Type.Kind()
			numerico := k >= reflect.Int && k <= reflect.Float64
			assert.False(t, numerico,
				"%s.%s é numérico: é exatamente o campo onde um centavo entraria", nome, campo.Name)

			assert.False(t, strings.Contains(strings.ToLower(campo.Name), "cents"),
				"%s.%s cheira a dinheiro", nome, campo.Name)
		}
	}
}
