package transaction_test

import (
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A ação `link` da importação (spec 0005 §4.2, critérios 5 e 8; ADR-026f) é
// o vetor novo desta entrega: escrita numa linha existente. Estes testes
// provam a reconferência no commit — casa, exclusão, conta, natureza — e que
// o vínculo não mexe em dinheiro.

// parDeTransferencia semeia um par vivo A→B na casa e devolve as duas pernas.
func (a *ambiente) parDeTransferencia(casa, de, para string, valor int64, dia int, grupo string) (saida, entrada transaction.Transaction) {
	saida = a.repo.semear(transaction.Transaction{
		HouseholdID: casa, AccountID: de, Kind: transaction.KindTransferOut, AmountCents: valor,
		Description: "Transferência", DescriptionNorm: "transferencia",
		OccurredOn: civil.MustNew(2026, 9, dia), TransferGroupID: &grupo,
	})
	entrada = a.repo.semear(transaction.Transaction{
		HouseholdID: casa, AccountID: para, Kind: transaction.KindTransferIn, AmountCents: valor,
		Description: "Transferência", DescriptionNorm: "transferencia",
		OccurredOn: civil.MustNew(2026, 9, dia), TransferGroupID: &grupo,
	})
	return saida, entrada
}

func vinculo(perna transaction.Transaction, conta, semente string, externo *string) transaction.LinkImportInput {
	return transaction.LinkImportInput{
		TransactionID: perna.ID,
		AccountID:     conta,
		ExternalID:    externo,
		DedupKey:      chave(semente),
		ImportBatchID: "lote-b",
	}
}

func TestLinkImportGravaAIdentidadeNaPernaSemMexerEmDinheiro(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "C6", account.KindChecking)
	_, entrada := amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 250_00, 5, "grupo-1")

	saldosAntes, err := amb.repo.SumByAccount(t.Context(), minhaCasa)
	require.NoError(t, err)

	externo := "TXN-B-0001"
	require.NoError(t, amb.svc.LinkImport(t.Context(), ator(minhaCasa), vinculo(entrada, "acc-b", "b-1", &externo)))

	depois := amb.repo.linhas[entrada.ID]
	require.NotNil(t, depois.ExternalID)
	assert.Equal(t, "TXN-B-0001", *depois.ExternalID)
	assert.Equal(t, chave("b-1"), depois.DedupKey)
	assert.Equal(t, 1, depois.DedupOrdinal)
	require.NotNil(t, depois.ImportBatchID)
	assert.Equal(t, "lote-b", *depois.ImportBatchID)
	assert.Equal(t, agora, depois.UpdatedAt)

	// Nada além da identidade e do carimbo mudou.
	esperada := entrada
	esperada.ExternalID, esperada.DedupKey, esperada.DedupOrdinal = depois.ExternalID, depois.DedupKey, depois.DedupOrdinal
	esperada.ImportBatchID, esperada.UpdatedAt = depois.ImportBatchID, depois.UpdatedAt
	assert.Equal(t, esperada, depois)

	saldosDepois, err := amb.repo.SumByAccount(t.Context(), minhaCasa)
	require.NoError(t, err)
	assert.Equal(t, saldosAntes, saldosDepois, "critério 5: o saldo das duas contas não muda com o link")

	require.Len(t, amb.auditor.registros, 1)
	registro := amb.auditor.registros[0]
	assert.Equal(t, audit.ActionTransactionImportLinked, registro.Action)
	assert.Equal(t, audit.EntityTransaction, registro.Entity)
	assert.Equal(t, entrada.ID, registro.EntityID)
	assert.Equal(t, minhaCasa, registro.HouseholdID)
}

// S1: a perna de outra casa não é vinculada — e o erro é o mesmo de perna
// inexistente, porque distinguir confirmaria a existência do recurso alheio.
func TestLinkImportRecusaPernaDeOutraCasaComoInexistente(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(outraCasa, "acc-x", "X", account.KindChecking)
	amb.conta(outraCasa, "acc-y", "Y", account.KindChecking)
	_, entradaAlheia := amb.parDeTransferencia(outraCasa, "acc-x", "acc-y", 10_00, 1, "grupo-alheio")
	antes := amb.repo.linhas[entradaAlheia.ID]

	err := amb.svc.LinkImport(t.Context(), ator(minhaCasa), vinculo(entradaAlheia, "acc-y", "k", nil))
	require.ErrorIs(t, err, transaction.ErrLinkTargetMissing)

	inexistente := transaction.Transaction{ID: "00000000-0000-7000-8000-999999999999"}
	err2 := amb.svc.LinkImport(t.Context(), ator(minhaCasa), vinculo(inexistente, "acc-y", "k", nil))
	require.ErrorIs(t, err2, transaction.ErrLinkTargetMissing)

	assert.Equal(t, antes, amb.repo.linhas[entradaAlheia.ID], "a perna da vizinha não mudou")
	assert.Empty(t, amb.auditor.registros)
}

func TestLinkImportRecusaPernaExcluida(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "A", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "B", account.KindChecking)
	_, entrada := amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 10_00, 1, "grupo-1")
	require.NoError(t, amb.svc.SoftDelete(t.Context(), ator(minhaCasa), entrada.ID))
	amb.auditor.registros = nil

	err := amb.svc.LinkImport(t.Context(), ator(minhaCasa), vinculo(entrada, "acc-b", "k", nil))
	require.ErrorIs(t, err, transaction.ErrLinkTargetDeleted)
	assert.NotErrorIs(t, err, transaction.ErrLinkTargetMissing, "excluída é diferente de inexistente: o importador bloqueia só a linha")
	assert.Empty(t, amb.auditor.registros)
}

func TestLinkImportRecusaPernaIncoerenteComALinha(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "A", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "B", account.KindChecking)
	saida, entrada := amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 10_00, 1, "grupo-1")

	t.Run("conta diferente da do lote", func(t *testing.T) {
		// A perna existe, é viva e é da casa — mas é da conta A, e o lote é
		// da conta B. Vincular gravaria a identidade da linha de B numa perna
		// de A, e a reimportação de B continuaria entrando.
		err := amb.svc.LinkImport(t.Context(), ator(minhaCasa), vinculo(saida, "acc-b", "k1", nil))
		require.ErrorIs(t, err, transaction.ErrLinkTargetInvalid)
	})

	t.Run("não é transferência", func(t *testing.T) {
		despesa := amb.repo.semear(transaction.Transaction{
			HouseholdID: minhaCasa, AccountID: "acc-b", Kind: transaction.KindExpense, AmountCents: 10_00,
			Description: "Café", OccurredOn: civil.MustNew(2026, 9, 1),
		})
		err := amb.svc.LinkImport(t.Context(), ator(minhaCasa), vinculo(despesa, "acc-b", "k2", nil))
		require.ErrorIs(t, err, transaction.ErrLinkTargetInvalid)
	})

	t.Run("transferência sem grupo", func(t *testing.T) {
		semGrupo := amb.repo.semear(transaction.Transaction{
			HouseholdID: minhaCasa, AccountID: "acc-b", Kind: transaction.KindTransferIn, AmountCents: 10_00,
			Description: "Órfã", OccurredOn: civil.MustNew(2026, 9, 1),
		})
		err := amb.svc.LinkImport(t.Context(), ator(minhaCasa), vinculo(semGrupo, "acc-b", "k3", nil))
		require.ErrorIs(t, err, transaction.ErrLinkTargetInvalid)
	})

	assert.Nil(t, amb.repo.linhas[entrada.ID].ImportBatchID, "nada foi vinculado")
	assert.Empty(t, amb.auditor.registros)
}

// ADR-025(b), a mesma regra do CreateBatch: chave NATURAL nunca ganha ordinal
// 2. Se a identidade do emissor já está gravada na casa, o link é uma
// colisão — ErrDuplicateDedup, que IsBlocked reconhece e a importação traduz
// em "linha bloqueada", nunca em 500.
func TestLinkImportRespeitaOIndiceUnicoEAsRegrasDoOrdinal(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "A", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "B", account.KindChecking)
	_, entrada := amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 10_00, 1, "grupo-1")
	_, entrada2 := amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 10_00, 1, "grupo-2")

	// A chave natural já ocupada por OUTRA linha da casa.
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-b", Kind: transaction.KindExpense, AmountCents: 5_00,
		Description: "Ocupante", OccurredOn: civil.MustNew(2026, 9, 2),
		DedupKey: chave("natural"), DedupOrdinal: 1, ExternalID: ptr("EXT-1"),
	})
	err := amb.svc.LinkImport(t.Context(), ator(minhaCasa), vinculo(entrada, "acc-b", "natural", ptr("EXT-1")))
	require.ErrorIs(t, err, transaction.ErrDuplicateDedup)
	assert.True(t, transaction.IsBlocked(err), "colisão de chave é linha bloqueada, não 500")

	// Chave DERIVADA repetida é legítima: ordinal continua de onde parou.
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-b", Kind: transaction.KindExpense, AmountCents: 5_00,
		Description: "Gêmea", OccurredOn: civil.MustNew(2026, 9, 2),
		DedupKey: chave("derivada"), DedupOrdinal: 1,
	})
	require.NoError(t, amb.svc.LinkImport(t.Context(), ator(minhaCasa), vinculo(entrada2, "acc-b", "derivada", nil)))
	assert.Equal(t, 2, amb.repo.linhas[entrada2.ID].DedupOrdinal)

	// Os três erros do link NÃO são "bloqueio": o importador os trata de
	// outro jeito (linha bloqueada só na excluída; lote inteiro nos outros).
	for _, e := range []error{transaction.ErrLinkTargetMissing, transaction.ErrLinkTargetDeleted, transaction.ErrLinkTargetInvalid} {
		assert.False(t, transaction.IsBlocked(e), "%v", e)
		assert.False(t, transaction.IsValidationError(e), "%v", e)
	}
}

func TestLinkImportValidaAFormaAntesDeConsultar(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "A", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "B", account.KindChecking)
	_, entrada := amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 10_00, 1, "grupo-1")
	base := vinculo(entrada, "acc-b", "k", nil)

	casos := []struct {
		nome  string
		muda  func(*transaction.LinkImportInput)
		quero error
	}{
		{"chave fora da forma canônica", func(in *transaction.LinkImportInput) { in.DedupKey = "abc" }, transaction.ErrInvalidDedupKey},
		{"identificador externo longo demais", func(in *transaction.LinkImportInput) { in.ExternalID = ptr(strings.Repeat("x", 65)) }, transaction.ErrInvalidExternalID},
		{"sem lote", func(in *transaction.LinkImportInput) { in.ImportBatchID = "" }, transaction.ErrInvalidSource},
		{"sem conta", func(in *transaction.LinkImportInput) { in.AccountID = "" }, transaction.ErrNotFound},
		{"sem perna", func(in *transaction.LinkImportInput) { in.TransactionID = "" }, transaction.ErrLinkTargetMissing},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			in := base
			c.muda(&in)
			require.ErrorIs(t, amb.svc.LinkImport(t.Context(), ator(minhaCasa), in), c.quero)
		})
	}

	require.ErrorIs(t, amb.svc.LinkImport(t.Context(), transaction.Actor{UserID: usuario}, base), transaction.ErrNotFound, "sem casa no token")
	require.Error(t, amb.svc.LinkImport(t.Context(), transaction.Actor{HouseholdID: minhaCasa}, base), "sem usuário no token")

	assert.Nil(t, amb.repo.linhas[entrada.ID].ImportBatchID)
	assert.Empty(t, amb.auditor.registros)
}

func TestLinkImportFalhaDeAuditoriaDesfazOVinculo(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "A", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "B", account.KindChecking)
	_, entrada := amb.parDeTransferencia(minhaCasa, "acc-a", "acc-b", 10_00, 1, "grupo-1")
	amb.auditor.falha = assert.AnError

	err := amb.svc.LinkImport(t.Context(), ator(minhaCasa), vinculo(entrada, "acc-b", "k", nil))
	require.ErrorIs(t, err, assert.AnError)
	// Com txDireto o fake não desfaz; o que este teste prova é que o erro
	// da auditoria SOBE — a transação real faz o rollback.
}
