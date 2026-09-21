package transaction_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Critério 9 da spec 0005 (§8) e os abusos da §9 do plano E2c: a prévia não
// escreve nem audita; a execução real só toca `category_id IS NULL` de
// receita/despesa da casa do token; a segunda execução categoriza 0; uma
// entrada de auditoria por execução real, sem descrição alguma.

// palavraDeCategoria cadastra uma palavra-chave numa categoria da casa.
func (a *ambiente) palavraDeCategoria(casa, categoriaID, palavra string) {
	a.categorias.palavras = append(a.categorias.palavras, category.Keyword{
		ID: fmt.Sprintf("kw-%d", len(a.categorias.palavras)+1), HouseholdID: casa, CategoryID: categoriaID,
		Keyword: palavra, Norm: textnorm.Normalize(palavra), Position: len(a.categorias.palavras),
	})
}

// lancamento semeia um lançamento com description_norm derivada, como o
// serviço faz em toda escrita.
func (a *ambiente) lancamento(casa, conta, kind, descricao string, valor int64, dia int) transaction.Transaction {
	return a.repo.semear(transaction.Transaction{
		HouseholdID: casa, AccountID: conta, Kind: kind, AmountCents: valor,
		Description: descricao, DescriptionNorm: textnorm.Normalize(descricao),
		OccurredOn: civil.MustNew(2026, 9, dia),
	})
}

// cenarioDeCategorizacao monta uma casa com duas categorias com palavras e
// um mês com linhas de todo tipo: as que casam, as que já têm categoria, uma
// transferência com descrição que casaria, uma receita cuja palavra é de
// despesa, e a vizinha com a mesma palavra.
func cenarioDeCategorizacao(t *testing.T) (*ambiente, map[string]transaction.Transaction) {
	t.Helper()
	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-2", "Poupança", account.KindChecking)
	amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	amb.categoria(minhaCasa, "cat-transporte", "Transporte", category.KindExpense)
	amb.categoria(minhaCasa, "cat-salario", "Salário", category.KindIncome)
	amb.palavraDeCategoria(minhaCasa, "cat-mercado", "supermercado")
	amb.palavraDeCategoria(minhaCasa, "cat-transporte", "uber")
	amb.palavraDeCategoria(minhaCasa, "cat-salario", "folha")

	linhas := map[string]transaction.Transaction{}
	linhas["casa"] = amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "SUPERMERCADO EXTRA 123", 150_00, 3)
	linhas["casa2"] = amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Uber *Trip", 25_00, 4)
	linhas["salario"] = amb.lancamento(minhaCasa, "acc-1", transaction.KindIncome, "Folha de pagamento", 5_000_00, 5)
	// Receita cuja descrição só casa com palavra de DESPESA: income nunca
	// recebe categoria expense (critério 4).
	linhas["receitaComPalavraDeDespesa"] = amb.lancamento(minhaCasa, "acc-1", transaction.KindIncome, "Estorno supermercado", 30_00, 6)
	// Já categorizada: o auto-categorize nunca sobrescreve.
	jaCategorizada := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense, AmountCents: 80_00,
		Description: "Supermercado do bairro", DescriptionNorm: textnorm.Normalize("Supermercado do bairro"),
		OccurredOn: civil.MustNew(2026, 9, 7), CategoryID: ptr("cat-transporte"),
	})
	linhas["jaCategorizada"] = jaCategorizada
	// Transferência com descrição que casaria: não tem categoria por desenho.
	grupo := "grupo-1"
	linhas["transferOut"] = amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindTransferOut, AmountCents: 100_00,
		Description: "Uber para poupança", DescriptionNorm: textnorm.Normalize("Uber para poupança"),
		OccurredOn: civil.MustNew(2026, 9, 8), TransferGroupID: &grupo,
	})
	linhas["transferIn"] = amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-2", Kind: transaction.KindTransferIn, AmountCents: 100_00,
		Description: "Uber para poupança", DescriptionNorm: textnorm.Normalize("Uber para poupança"),
		OccurredOn: civil.MustNew(2026, 9, 8), TransferGroupID: &grupo,
	})
	// Sem correspondência nenhuma.
	linhas["semPalavra"] = amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria da esquina", 12_00, 9)
	// Outro mês: fora da janela.
	linhas["outroMes"] = amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense, AmountCents: 10_00,
		Description: "Supermercado agosto", DescriptionNorm: textnorm.Normalize("Supermercado agosto"),
		OccurredOn: civil.MustNew(2026, 8, 30),
	})

	// A vizinha tem a MESMA palavra e um lançamento igualzinho: nada dela
	// aparece nem muda.
	amb.conta(outraCasa, "acc-alheia", "Conta da Vizinha", account.KindChecking)
	amb.categoria(outraCasa, "cat-alheia", "Mercado dela", category.KindExpense)
	amb.palavraDeCategoria(outraCasa, "cat-alheia", "supermercado")
	linhas["alheia"] = amb.lancamento(outraCasa, "acc-alheia", transaction.KindExpense, "SUPERMERCADO EXTRA 123", 150_00, 3)

	return amb, linhas
}

func TestAutoCategorizePreviaNaoEscreveNemAudita(t *testing.T) {
	t.Parallel()

	amb, linhas := cenarioDeCategorizacao(t)
	antes := clonarLinhas(amb.repo)

	view, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)

	assert.Equal(t, "2026-09", view.Month)
	assert.EqualValues(t, 3, view.Categorized, "supermercado, uber e folha")
	assert.EqualValues(t, 2, view.Unmatched, "a receita com palavra de despesa e a padaria")
	require.Len(t, view.Items, 3)
	require.Len(t, view.UnmatchedItems, 2)

	// A prévia mostra a categoria, o nome, a pontuação e a palavra que decidiu.
	porID := map[string]transaction.AutoCategorizeItemView{}
	for _, it := range view.Items {
		porID[it.ID] = it
	}
	sup := porID[linhas["casa"].ID]
	assert.Equal(t, "cat-mercado", sup.CategoryID)
	assert.Equal(t, "Mercado", sup.CategoryName)
	assert.Equal(t, 100, sup.MatchScore)
	assert.Equal(t, "supermercado", sup.MatchedKeyword)
	assert.Equal(t, "SUPERMERCADO EXTRA 123", sup.Description, "a prévia mostra a descrição ORIGINAL, não a normalizada")

	for _, u := range view.UnmatchedItems {
		assert.Equal(t, "below_threshold", u.Reason)
	}

	// Estado do repositório byte a byte igual, e nenhuma auditoria.
	assert.Equal(t, antes, clonarLinhas(amb.repo), "a prévia escreveu")
	assert.Empty(t, amb.auditor.registros, "a prévia auditou")
}

func TestAutoCategorizeRealSoTocaCategoriaNulaDeReceitaEDespesa(t *testing.T) {
	t.Parallel()

	amb, linhas := cenarioDeCategorizacao(t)

	view, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: "2026-09", DryRun: false})
	require.NoError(t, err)
	assert.EqualValues(t, 3, view.Categorized, "linhas AFETADAS")
	assert.EqualValues(t, 2, view.Unmatched)
	assert.NotNil(t, view.Items)
	assert.NotNil(t, view.UnmatchedItems)
	assert.Empty(t, view.Items, "a execução real não devolve listas")
	assert.Empty(t, view.UnmatchedItems)

	le := func(k string) transaction.Transaction { return amb.repo.linhas[linhas[k].ID] }

	require.NotNil(t, le("casa").CategoryID)
	assert.Equal(t, "cat-mercado", *le("casa").CategoryID)
	assert.Equal(t, agora, le("casa").UpdatedAt, "updated_at carimbado pelo relógio do serviço")
	require.NotNil(t, le("casa2").CategoryID)
	assert.Equal(t, "cat-transporte", *le("casa2").CategoryID)
	require.NotNil(t, le("salario").CategoryID)
	assert.Equal(t, "cat-salario", *le("salario").CategoryID)

	assert.Nil(t, le("receitaComPalavraDeDespesa").CategoryID, "income nunca recebe categoria expense")
	assert.Equal(t, "cat-transporte", *le("jaCategorizada").CategoryID, "categoria escolhida nunca é sobrescrita")
	assert.Nil(t, le("transferOut").CategoryID, "transferência não tem categoria")
	assert.Nil(t, le("transferIn").CategoryID)
	assert.Nil(t, le("semPalavra").CategoryID)
	assert.Nil(t, le("outroMes").CategoryID, "outro mês fica fora")
	assert.Nil(t, le("alheia").CategoryID, "lançamento de outra casa jamais muda")

	// Nada além de category_id e updated_at foi tocado.
	original := linhas["casa"]
	depois := le("casa")
	depois.CategoryID, depois.UpdatedAt = original.CategoryID, original.UpdatedAt
	assert.Equal(t, original, depois)

	// Uma entrada de auditoria, do MÊS, sem descrição, sem contagem.
	require.Len(t, amb.auditor.registros, 1)
	registro := amb.auditor.registros[0]
	assert.Equal(t, audit.ActionTransactionAutoCategorized, registro.Action)
	assert.Equal(t, audit.EntityTransactionMonth, registro.Entity)
	assert.Equal(t, "2026-09", registro.EntityID)
	assert.Equal(t, minhaCasa, registro.HouseholdID)
	assert.Equal(t, usuario, registro.UserID)
}

func TestAutoCategorizeSegundaExecucaoCategorizaZero(t *testing.T) {
	t.Parallel()

	amb, _ := cenarioDeCategorizacao(t)
	primeira, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: "2026-09"})
	require.NoError(t, err)
	require.EqualValues(t, 3, primeira.Categorized)

	segunda, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.EqualValues(t, 0, segunda.Categorized, "idempotente: nada mais está com category_id nulo e casando")
	assert.EqualValues(t, 2, segunda.Unmatched, "as que não casam continuam contadas")
	assert.Len(t, amb.auditor.registros, 2, "cada execução real audita uma vez, mesmo categorizando 0")
}

func TestAutoCategorizePreviaDaVizinhaNaoVeMinhasLinhas(t *testing.T) {
	t.Parallel()

	amb, linhas := cenarioDeCategorizacao(t)

	// Do lado da vizinha, só a linha dela aparece — e a minha não muda.
	view, err := amb.svc.AutoCategorize(t.Context(), ator(outraCasa), transaction.AutoCategorizeInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)
	require.Len(t, view.Items, 1)
	assert.Equal(t, linhas["alheia"].ID, view.Items[0].ID)
	assert.Equal(t, "cat-alheia", view.Items[0].CategoryID)

	real, err := amb.svc.AutoCategorize(t.Context(), ator(outraCasa), transaction.AutoCategorizeInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, real.Categorized)
	assert.Nil(t, amb.repo.linhas[linhas["casa"].ID].CategoryID, "a execução da vizinha não tocou na minha linha")
}

func TestAutoCategorizeEmpateFicaSemCategoriaComMotivoAmbiguous(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-a", "Padaria", category.KindExpense)
	amb.categoria(minhaCasa, "cat-b", "Centro", category.KindExpense)
	amb.palavraDeCategoria(minhaCasa, "cat-a", "padaria")
	amb.palavraDeCategoria(minhaCasa, "cat-b", "central")
	empate := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria Central", 9_00, 1)

	view, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)
	assert.EqualValues(t, 0, view.Categorized)
	require.Len(t, view.UnmatchedItems, 1)
	assert.Equal(t, empate.ID, view.UnmatchedItems[0].ID)
	assert.Equal(t, "ambiguous", view.UnmatchedItems[0].Reason)

	real, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.EqualValues(t, 0, real.Categorized)
	assert.Nil(t, amb.repo.linhas[empate.ID].CategoryID, "ambíguo é pior que vazio: nada gravado")
}

func TestAutoCategorizeExigeMesEmFormaCanonica(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	for _, mes := range []string{"", "2026-13", "abc", "2026-1", "2026-01-01"} {
		_, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: mes, DryRun: true})
		require.ErrorIs(t, err, transaction.ErrInvalidMonth, "mês %q", mes)
		_, err = amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: mes})
		require.ErrorIs(t, err, transaction.ErrInvalidMonth, "mês %q (real)", mes)
	}
	assert.Empty(t, amb.auditor.registros)
}

func TestAutoCategorizeSemCasaNoTokenNadaAcontece(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	_, err := amb.svc.AutoCategorize(t.Context(), transaction.Actor{UserID: usuario}, transaction.AutoCategorizeInput{Month: "2026-09"})
	require.ErrorIs(t, err, transaction.ErrNotFound)
}

func TestAutoCategorizeRecusaMesAcimaDoTeto(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	for i := 0; i <= transaction.MaxAutoCategorizeRows; i++ {
		amb.repo.semear(transaction.Transaction{
			HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense, AmountCents: 1,
			Description: "x", DescriptionNorm: "x", OccurredOn: civil.MustNew(2026, 9, 1),
		})
	}

	_, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: "2026-09", DryRun: true})
	require.ErrorIs(t, err, transaction.ErrTooManyUncategorized)
	assert.True(t, transaction.IsValidationError(err))
	_, err = amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: "2026-09"})
	require.ErrorIs(t, err, transaction.ErrTooManyUncategorized)
	assert.Empty(t, amb.auditor.registros, "recusa não audita")
}

func TestAutoCategorizeListasDaPreviaParamNoTetoMasAsContagensSaoCompletas(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-1", "Mercado", category.KindExpense)
	amb.palavraDeCategoria(minhaCasa, "cat-1", "supermercado")
	total := transaction.MaxAutoCategorizeListed + 7
	for i := 0; i < total; i++ {
		amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 1, 1)
		amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 1, 1)
	}

	view, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)
	assert.EqualValues(t, total, view.Categorized)
	assert.EqualValues(t, total, view.Unmatched)
	assert.Len(t, view.Items, transaction.MaxAutoCategorizeListed)
	assert.Len(t, view.UnmatchedItems, transaction.MaxAutoCategorizeListed)

	real, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.EqualValues(t, total, real.Categorized, "a gravação vai além do teto da lista")
}

func TestAutoCategorizeFalhaDeAuditoriaAbortaAEscrita(t *testing.T) {
	t.Parallel()

	amb, _ := cenarioDeCategorizacao(t)
	amb.auditor.falha = errors.New("audit_log indisponível")

	_, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: "2026-09"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "audit_log indisponível")
}

func TestAutoCategorizeCategoriaArquivadaNuncaESugerida(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	arquivada := amb.categoria(minhaCasa, "cat-arq", "Antiga", category.KindExpense)
	arquivada.ArchivedAt = ptr(agora)
	amb.categorias.add(arquivada)
	amb.palavraDeCategoria(minhaCasa, "cat-arq", "supermercado")
	linha := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 1, 1)

	view, err := amb.svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.EqualValues(t, 0, view.Categorized)
	assert.Nil(t, amb.repo.linhas[linha.ID].CategoryID)
}

// clonarLinhas tira uma foto do estado do repositório em memória.
func clonarLinhas(r *repoFake) map[string]transaction.Transaction {
	out := make(map[string]transaction.Transaction, len(r.linhas))
	for k, v := range r.linhas {
		out[k] = v
	}
	return out
}
