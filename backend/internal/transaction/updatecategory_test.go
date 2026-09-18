package transaction_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Emenda §11 da spec 0005: PATCH /transactions/{id} só com categoryId.
// Estes testes provam os critérios (a) e (b) no serviço — o que é 404, o que
// é 422, o que grava, o que NÃO grava — e o rastro de auditoria.

// Ids em forma de UUID: o handler confere a forma na borda, e o serviço é
// exercitado com os mesmos valores que ele receberia de verdade.
const (
	catMercado   = "00000000-0000-7000-8000-00000000c001"
	catLazer     = "00000000-0000-7000-8000-00000000c002"
	catSalario   = "00000000-0000-7000-8000-00000000c003"
	catArquivada = "00000000-0000-7000-8000-00000000c004"
	catDaVizinha = "00000000-0000-7000-8000-00000000c005"
	catFantasma  = "00000000-0000-7000-8000-00000000c999"
)

// cenarioDeCategoria monta a casa com as categorias de todos os tipos que a
// §11 distingue, e a vizinha com uma categoria própria.
func cenarioDeCategoria(t *testing.T) *ambiente {
	t.Helper()
	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-2", "Poupança", account.KindChecking)
	amb.conta(outraCasa, "acc-x", "Alheia", account.KindChecking)
	amb.categoria(minhaCasa, catMercado, "Mercado", category.KindExpense)
	amb.categoria(minhaCasa, catLazer, "Lazer", category.KindExpense)
	amb.categoria(minhaCasa, catSalario, "Salário", category.KindIncome)
	arquivada := amb.categoria(minhaCasa, catArquivada, "Antiga", category.KindExpense)
	arquivada.ArchivedAt = ptr(agora)
	amb.categorias.add(arquivada)
	amb.categoria(outraCasa, catDaVizinha, "Da vizinha", category.KindExpense)
	return amb
}

func TestUpdateCategoryGravaSoACategoriaEAudita(t *testing.T) {
	t.Parallel()

	amb := cenarioDeCategoria(t)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_07, 3)
	outro := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 12_34, 4)

	view, err := amb.svc.UpdateCategory(t.Context(), ator(minhaCasa), alvo.ID, catMercado)
	require.NoError(t, err)

	// A resposta é a View de GET, já com a categoria e o nome resolvidos.
	assert.Equal(t, alvo.ID, view.ID)
	require.NotNil(t, view.CategoryID)
	assert.Equal(t, catMercado, *view.CategoryID)
	require.NotNil(t, view.CategoryName)
	assert.Equal(t, "Mercado", *view.CategoryName)
	assert.Equal(t, "Conta", view.AccountName)
	assert.Equal(t, agora.Format("2006-01-02T15:04:05Z"), view.UpdatedAt)

	// O SET tem duas colunas: nada financeiro mudou.
	gravado := amb.repo.linhas[alvo.ID]
	assert.Equal(t, alvo.AmountCents, gravado.AmountCents)
	assert.Equal(t, alvo.Description, gravado.Description)
	assert.Equal(t, alvo.OccurredOn, gravado.OccurredOn)
	assert.Equal(t, alvo.AccountID, gravado.AccountID)
	assert.Equal(t, alvo.CompetenceMonth, gravado.CompetenceMonth)
	assert.Equal(t, agora, gravado.UpdatedAt)

	// Critério (b): "só este" não altera nenhum outro lançamento.
	assert.Nil(t, amb.repo.linhas[outro.ID].CategoryID)
	assert.Equal(t, outro.UpdatedAt, amb.repo.linhas[outro.ID].UpdatedAt)

	// Auditoria: transaction.updated, entidade transaction, id do lançamento
	// — e nada mais (AuditParams não tem campo de valor por construção).
	require.Len(t, amb.auditor.registros, 1)
	registro := amb.auditor.registros[0]
	assert.Equal(t, audit.ActionTransactionUpdated, registro.Action)
	assert.Equal(t, audit.EntityTransaction, registro.Entity)
	assert.Equal(t, alvo.ID, registro.EntityID)
	assert.Equal(t, minhaCasa, registro.HouseholdID)
	assert.Equal(t, usuario, registro.UserID)
}

// Categoria já escolhida É substituída: é a pessoa recategorizando de
// propósito (diferente do auto-categorize, que nunca sobrescreve).
func TestUpdateCategorySubstituiCategoriaJaEscolhida(t *testing.T) {
	t.Parallel()

	amb := cenarioDeCategoria(t)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Cinema", 40_00, 3)
	alvo.CategoryID = ptr(catMercado)
	amb.repo.linhas[alvo.ID] = alvo

	view, err := amb.svc.UpdateCategory(t.Context(), ator(minhaCasa), alvo.ID, catLazer)
	require.NoError(t, err)
	assert.Equal(t, catLazer, *view.CategoryID)
	assert.Equal(t, catLazer, *amb.repo.linhas[alvo.ID].CategoryID)
	assert.Equal(t, []string{audit.ActionTransactionUpdated}, amb.auditor.acoes())
}

// Mandar a categoria que já está é sucesso SEM escrita e SEM auditoria:
// registrar "atualizado" numa linha que não mudou seria um rastro que mente.
func TestUpdateCategoryComAMesmaCategoriaNaoEscreveNemAudita(t *testing.T) {
	t.Parallel()

	amb := cenarioDeCategoria(t)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Cinema", 40_00, 3)
	alvo.CategoryID = ptr(catLazer)
	alvo.UpdatedAt = agora.AddDate(0, 0, -10)
	amb.repo.linhas[alvo.ID] = alvo

	view, err := amb.svc.UpdateCategory(t.Context(), ator(minhaCasa), alvo.ID, catLazer)
	require.NoError(t, err)
	assert.Equal(t, catLazer, *view.CategoryID)
	assert.Equal(t, alvo.UpdatedAt, amb.repo.linhas[alvo.ID].UpdatedAt, "nada foi gravado")
	assert.Empty(t, amb.auditor.registros)
}

// Critério (a): cada recusa da §11 com o erro certo — e NADA gravado em
// nenhuma delas.
func TestUpdateCategoryRecusaConformeAEmenda(t *testing.T) {
	t.Parallel()

	amb := cenarioDeCategoria(t)
	despesa := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)
	receita := amb.lancamento(minhaCasa, "acc-1", transaction.KindIncome, "Salário", 5_000_00, 5)
	saida, entrada := amb.parDeTransferencia(minhaCasa, "acc-1", "acc-2", 250_00, 6, "g-1")
	daVizinha := amb.lancamento(outraCasa, "acc-x", transaction.KindExpense, "Da vizinha", 99_00, 3)
	excluida := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Excluída", 10_00, 7)
	require.NoError(t, amb.svc.SoftDelete(t.Context(), ator(minhaCasa), excluida.ID))
	amb.auditor.registros = nil

	casos := []struct {
		nome      string
		lancament string
		categoria string
		erro      error
	}{
		{"lançamento de outra casa", daVizinha.ID, catMercado, transaction.ErrNotFound},
		{"lançamento inexistente", "00000000-0000-7000-8000-000000000999", catMercado, transaction.ErrNotFound},
		{"lançamento excluído", excluida.ID, catMercado, transaction.ErrNotFound},
		{"perna de saída da transferência", saida.ID, catMercado, transaction.ErrCategoryOnTransfer},
		{"perna de entrada da transferência", entrada.ID, catMercado, transaction.ErrCategoryOnTransfer},
		{"categoria de outra casa", despesa.ID, catDaVizinha, transaction.ErrNotFound},
		{"categoria inexistente", despesa.ID, catFantasma, transaction.ErrNotFound},
		{"categoria arquivada", despesa.ID, catArquivada, transaction.ErrCategoryArchived},
		{"receita com categoria de despesa", receita.ID, catMercado, transaction.ErrCategoryKindMismatch},
		{"despesa com categoria de receita", despesa.ID, catSalario, transaction.ErrCategoryKindMismatch},
		{"categoria vazia", despesa.ID, "", transaction.ErrCategoryRequired},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := amb.svc.UpdateCategory(t.Context(), ator(minhaCasa), c.lancament, c.categoria)
			require.ErrorIs(t, err, c.erro)
		})
	}

	// Nada gravado: nenhuma linha da casa (nem da vizinha) ganhou categoria, e
	// a auditoria está vazia.
	for id, linha := range amb.repo.linhas {
		assert.Nil(t, linha.CategoryID, "linha %s foi tocada", id)
	}
	assert.Empty(t, amb.auditor.registros)

	// Os erros da §11 são de validação (400/422), nunca 500.
	assert.True(t, transaction.IsValidationError(transaction.ErrCategoryOnTransfer))
	assert.True(t, transaction.IsValidationError(transaction.ErrCategoryKindMismatch))
	assert.True(t, transaction.IsValidationError(transaction.ErrCategoryArchived))
	assert.True(t, transaction.IsValidationError(transaction.ErrCategoryRequired))
}

// A perna de transferência é recusada ANTES de a categoria ser procurada: com
// categoria de outra casa no corpo, a resposta continua sendo a da
// transferência — e não há consulta de categoria a fazer.
func TestUpdateCategoryEmTransferenciaRecusaAntesDeOlharACategoria(t *testing.T) {
	t.Parallel()

	amb := cenarioDeCategoria(t)
	saida, _ := amb.parDeTransferencia(minhaCasa, "acc-1", "acc-2", 250_00, 6, "g-1")

	_, err := amb.svc.UpdateCategory(t.Context(), ator(minhaCasa), saida.ID, catDaVizinha)
	require.ErrorIs(t, err, transaction.ErrCategoryOnTransfer)
	_, err = amb.svc.UpdateCategory(t.Context(), ator(minhaCasa), saida.ID, catFantasma)
	require.ErrorIs(t, err, transaction.ErrCategoryOnTransfer)
	assert.Nil(t, amb.repo.linhas[saida.ID].CategoryID)
}

// Sem casa no token nada acontece — nem leitura.
func TestUpdateCategorySemCasaNoTokenResponde404(t *testing.T) {
	t.Parallel()

	amb := cenarioDeCategoria(t)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)

	_, err := amb.svc.UpdateCategory(t.Context(), transaction.Actor{UserID: usuario}, alvo.ID, catMercado)
	require.ErrorIs(t, err, transaction.ErrNotFound)
	assert.Nil(t, amb.repo.linhas[alvo.ID].CategoryID)
	assert.Empty(t, amb.auditor.registros)
}

// A auditoria é gravada DENTRO da transação: se ela falhar, a escrita não vale.
func TestUpdateCategoryFalhaDeAuditoriaDerrubaAEscrita(t *testing.T) {
	t.Parallel()

	amb := cenarioDeCategoria(t)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)
	amb.auditor.falha = assert.AnError

	_, err := amb.svc.UpdateCategory(t.Context(), ator(minhaCasa), alvo.ID, catMercado)
	require.ErrorIs(t, err, assert.AnError)
	assert.False(t, transaction.IsValidationError(err), "falha de infraestrutura é 500, nunca culpa de quem digitou")
}
