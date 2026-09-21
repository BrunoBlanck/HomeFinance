package importer_test

import (
	"fmt"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QA da E2c — ciclo de vida das palavras-chave ATRAVESSANDO os pacotes
// (category/account → classify → importer/transaction) sobre SQLite real:
// arquivar tira da correspondência e desarquivar devolve; PATCH de palavras
// numa categoria em uso não toca lançamento gravado; 20 cabem e 21 não; acento
// casa nos dois sentidos; o empate `ambiguous` some quando uma das palavras é
// removida.

func (a *ambiente) previaDeCategorizacao(t *testing.T, mes string) transaction.AutoCategorizeView {
	t.Helper()
	view, err := a.txSvc.AutoCategorize(t.Context(), a.atorDeLancamento(a.casa.ID, a.usuario.ID),
		transaction.AutoCategorizeInput{Month: mes, DryRun: true})
	require.NoError(t, err)
	return view
}

func (a *ambiente) categorizarDeVerdade(t *testing.T, mes string) transaction.AutoCategorizeView {
	t.Helper()
	view, err := a.txSvc.AutoCategorize(t.Context(), a.atorDeLancamento(a.casa.ID, a.usuario.ID),
		transaction.AutoCategorizeInput{Month: mes, DryRun: false})
	require.NoError(t, err)
	return view
}

// --- arquivar / desarquivar --------------------------------------------------

func TestContaArquivadaVoltaASerContraparteAoDesarquivar(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	contaB := a.contaItau(t)
	_, err := a.contaSvc.Archive(t.Context(), a.atorDeConta(), contaB.ID)
	require.NoError(t, err)

	extrato := csvExtratoAPI(linhaExtrato{5, "-150.00", "01", "Transferência enviada pelo Pix - Itau Corrente"})

	// Arquivada: a linha é `novo`, sem contraparte.
	lote1 := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a1.csv", extrato))
	assert.Zero(t, lote1.Counts.InternalTransfer)
	linha1 := revisarPelaAPI(t, a, lote1.ID).Items[0]
	assert.Equal(t, string(dedup.StatusNew), linha1.Status)
	assert.Nil(t, linha1.SuggestedCounterpartAccountID)

	// Desarquivada: as palavras nunca saíram, e voltam a participar.
	voltou, err := a.contaSvc.Unarchive(t.Context(), a.atorDeConta(), contaB.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"itau"}, voltou.Keywords)

	lote2 := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a2.csv", extrato))
	assert.Equal(t, 1, lote2.Counts.InternalTransfer)
	linha2 := revisarPelaAPI(t, a, lote2.ID).Items[0]
	assert.Equal(t, string(dedup.StatusInternalTransfer), linha2.Status)
	assert.Equal(t, contaB.ID, texto(linha2.SuggestedCounterpartAccountID))
	assert.Equal(t, "itau", texto(linha2.MatchedKeyword))

	// O lote analisado ENQUANTO arquivada não ganha a sugestão depois: a
	// análise já passou (a revisão lê o staging, não recalcula).
	deNovo := revisarPelaAPI(t, a, lote1.ID).Items[0]
	assert.Equal(t, string(dedup.StatusNew), deNovo.Status)
	assert.Nil(t, deNovo.SuggestedCounterpartAccountID)
}

func TestCategoriaArquivadaVoltaASerSugeridaAoDesarquivar(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	alimentacao := a.categoriaComPalavras(t, "Alimentação", category.KindExpense, "padaria")

	// Um lançamento sem categoria já gravado, para o auto-categorize.
	extrato := csvExtratoAPI(linhaExtrato{5, "-11.00", "01", "Transferência enviada pelo Pix - Padaria Exemplo"})
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv", extrato))
	linha := revisarPelaAPI(t, a, lote.ID).Items[0]
	require.Equal(t, alimentacao.ID, texto(linha.SuggestedCategoryID), "ativa: sugere")
	resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"import","categoryId":null}]}`, linha.ID)))

	_, err := a.categoriaSvc.Archive(t.Context(), a.atorDeCategoria(), alimentacao.ID)
	require.NoError(t, err)

	// Arquivada: nem a importação nem o auto-categorize sugerem.
	lote2 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a2.csv", csvExtratoAPI(linhaExtrato{6, "-12.00", "02", "Transferência enviada pelo Pix - Padaria Exemplo"})))
	assert.Nil(t, revisarPelaAPI(t, a, lote2.ID).Items[0].SuggestedCategoryID)
	previa := a.previaDeCategorizacao(t, "2026-08")
	assert.EqualValues(t, 0, previa.Categorized)
	require.Len(t, previa.UnmatchedItems, 1)
	assert.Equal(t, "below_threshold", previa.UnmatchedItems[0].Reason, "sem palavra ativa é abaixo do limiar, não ambíguo")
	real := a.categorizarDeVerdade(t, "2026-08")
	assert.EqualValues(t, 0, real.Categorized)
	assert.Nil(t, a.lancamentosDa(t, a.casa.ID, "2026-08")[0].CategoryID)

	// Desarquivada: os dois voltam a sugerir a mesma categoria.
	_, err = a.categoriaSvc.Unarchive(t.Context(), a.atorDeCategoria(), alimentacao.ID)
	require.NoError(t, err)
	lote3 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a3.csv", csvExtratoAPI(linhaExtrato{7, "-13.00", "03", "Transferência enviada pelo Pix - Padaria Exemplo"})))
	assert.Equal(t, alimentacao.ID, texto(revisarPelaAPI(t, a, lote3.ID).Items[0].SuggestedCategoryID))
	previa = a.previaDeCategorizacao(t, "2026-08")
	require.Len(t, previa.Items, 1)
	assert.Equal(t, alimentacao.ID, previa.Items[0].CategoryID)
	real = a.categorizarDeVerdade(t, "2026-08")
	assert.EqualValues(t, 1, real.Categorized)
	assert.Equal(t, alimentacao.ID, texto(a.lancamentosDa(t, a.casa.ID, "2026-08")[0].CategoryID))
}

// --- PATCH de palavras numa categoria em uso ---------------------------------

func TestPatchDePalavrasChaveNaoAfetaLancamentosJaGravados(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	alimentacao := a.categoriaComPalavras(t, "Alimentação", category.KindExpense, "padaria")
	saude := a.categoriaComPalavras(t, "Saúde", category.KindExpense)

	extrato := csvExtratoAPI(linhaExtrato{5, "-11.00", "01", "Transferência enviada pelo Pix - Padaria Exemplo"})
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv", extrato))
	resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	gravado := a.lancamentosDa(t, a.casa.ID, "2026-08")[0]
	require.Equal(t, alimentacao.ID, texto(gravado.CategoryID), "entrou com a sugerida")

	// A palavra muda de dona: sai de Alimentação e vai para Saúde.
	vazia := []string{}
	_, err := a.categoriaSvc.Update(t.Context(), a.atorDeCategoria(), alimentacao.ID, category.UpdateInput{Keywords: &vazia})
	require.NoError(t, err)
	padaria := []string{"padaria"}
	_, err = a.categoriaSvc.Update(t.Context(), a.atorDeCategoria(), saude.ID, category.UpdateInput{Keywords: &padaria})
	require.NoError(t, err)

	// O lançamento gravado não muda — nem pelo PATCH, nem pelo auto-categorize
	// (que só toca category_id IS NULL).
	depois := a.lancamentosDa(t, a.casa.ID, "2026-08")[0]
	assert.Equal(t, gravado, depois, "o PATCH não toca lançamento")
	real := a.categorizarDeVerdade(t, "2026-08")
	assert.EqualValues(t, 0, real.Categorized)
	assert.EqualValues(t, 0, real.Unmatched)
	assert.Equal(t, gravado, a.lancamentosDa(t, a.casa.ID, "2026-08")[0])

	// O efeito é "da próxima importação em diante": a mesma descrição passa a
	// sugerir Saúde.
	lote2 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a2.csv", csvExtratoAPI(linhaExtrato{6, "-12.00", "02", "Transferência enviada pelo Pix - Padaria Exemplo"})))
	assert.Equal(t, saude.ID, texto(revisarPelaAPI(t, a, lote2.ID).Items[0].SuggestedCategoryID))
}

// --- limite de 20 --------------------------------------------------------------

func TestVinteCabemEVinteEUmaNaoNoSQLite(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	ctx := t.Context()

	vinte := make([]string, 0, 21)
	for i := range 20 {
		vinte = append(vinte, fmt.Sprintf("loja%02d", i))
	}
	vinteEUma := append(append([]string{}, vinte...), "loja20")

	t.Run("categoria", func(t *testing.T) {
		cat := a.categoriaComPalavras(t, "Compras", category.KindExpense)
		v, err := a.categoriaSvc.Update(ctx, a.atorDeCategoria(), cat.ID, category.UpdateInput{Keywords: &vinte})
		require.NoError(t, err)
		assert.Len(t, v.Keywords, 20)

		_, err = a.categoriaSvc.Update(ctx, a.atorDeCategoria(), cat.ID, category.UpdateInput{Keywords: &vinteEUma})
		require.ErrorIs(t, err, category.ErrTooManyKeywords)
		lida, err := a.categoriaSvc.Get(ctx, a.atorDeCategoria(), cat.ID)
		require.NoError(t, err)
		assert.Equal(t, vinte, lida.Keywords, "a lista de 21 foi recusada inteira: as 20 anteriores ficaram")

		_, err = a.categoriaSvc.Create(ctx, a.atorDeCategoria(), category.CreateInput{Name: "Outra", Kind: category.KindExpense, Keywords: vinteEUma})
		require.ErrorIs(t, err, category.ErrTooManyKeywords)
	})

	t.Run("conta", func(t *testing.T) {
		c := a.conta(t, a.casa.ID, "Conta Cheia", account.KindChecking, account.InstitutionOther)
		outras := make([]string, 0, 21)
		for i := range 21 {
			outras = append(outras, fmt.Sprintf("banco%02d", i))
		}
		vinteContas := outras[:20]
		v, err := a.contaSvc.Update(ctx, a.atorDeConta(), c.ID, account.UpdateInput{Keywords: &vinteContas})
		require.NoError(t, err)
		assert.Len(t, v.Keywords, 20)

		_, err = a.contaSvc.Update(ctx, a.atorDeConta(), c.ID, account.UpdateInput{Keywords: &outras})
		require.ErrorIs(t, err, account.ErrTooManyKeywords)
		lida, err := a.contaSvc.Get(ctx, a.atorDeConta(), c.ID)
		require.NoError(t, err)
		assert.Equal(t, vinteContas, lida.Keywords)
	})
}

// --- acento nos dois sentidos ------------------------------------------------

func TestPalavraComAcentoCasaComDescricaoSemAcentoEViceVersa(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	comAcento := a.categoriaComPalavras(t, "Padarias", category.KindExpense, "Padaria São João")
	semAcento := a.categoriaComPalavras(t, "Açougues", category.KindExpense, "acougue do ze")
	itau := a.conta(t, a.casa.ID, "Itaú Corrente", account.KindChecking, account.InstitutionOther)
	a.palavrasNaConta(t, itau.ID, "Itaú")

	extrato := csvExtratoAPI(
		linhaExtrato{5, "-11.00", "01", "Transferência enviada pelo Pix - PADARIA SAO JOAO LTDA"},
		linhaExtrato{6, "-12.00", "02", "Transferência enviada pelo Pix - Açougue do Zé"},
		linhaExtrato{7, "-150.00", "03", "Transferência enviada pelo Pix - ITAU CORRENTE"},
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv", extrato))
	itens := revisarPelaAPI(t, a, lote.ID).Items
	require.Len(t, itens, 3)

	padaria := linhaComDescricao(t, itens, "Pix enviado - PADARIA SAO JOAO LTDA")
	assert.Equal(t, comAcento.ID, texto(padaria.SuggestedCategoryID), "palavra com acento casa descrição sem acento")
	assert.Equal(t, 100, *padaria.MatchScore)
	assert.Equal(t, "Padaria São João", texto(padaria.MatchedKeyword), "a palavra devolvida é a forma exibível")

	acougue := linhaComDescricao(t, itens, "Pix enviado - Açougue do Zé")
	assert.Equal(t, semAcento.ID, texto(acougue.SuggestedCategoryID), "palavra sem acento casa descrição com acento")
	assert.Equal(t, 100, *acougue.MatchScore)

	transferencia := linhaComDescricao(t, itens, "Pix enviado - ITAU CORRENTE")
	assert.Equal(t, string(dedup.StatusInternalTransfer), transferencia.Status)
	assert.Equal(t, itau.ID, texto(transferencia.SuggestedCounterpartAccountID))
	assert.Equal(t, "Itaú", texto(transferencia.MatchedKeyword))
}

// --- empate ambíguo ------------------------------------------------------------

func TestEmpateAmbiguoSomeQuandoUmaDasPalavrasERemovida(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	padarias := a.categoriaComPalavras(t, "Padarias", category.KindExpense, "padaria")
	centro := a.categoriaComPalavras(t, "Centro", category.KindExpense, "central")

	// Na importação: empate 100 × 100 → nenhuma sugestão, entra sem categoria.
	extrato := csvExtratoAPI(linhaExtrato{5, "-9.00", "01", "Transferência enviada pelo Pix - Padaria Central"})
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv", extrato))
	linha := revisarPelaAPI(t, a, lote.ID).Items[0]
	assert.Nil(t, linha.SuggestedCategoryID, "empate é ambíguo: sem sugestão")
	assert.Nil(t, linha.MatchScore)
	resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	require.Nil(t, a.lancamentosDa(t, a.casa.ID, "2026-08")[0].CategoryID)

	// No auto-categorize: `ambiguous`, e a execução real não grava.
	previa := a.previaDeCategorizacao(t, "2026-08")
	require.Len(t, previa.UnmatchedItems, 1)
	assert.Equal(t, "ambiguous", previa.UnmatchedItems[0].Reason)
	assert.EqualValues(t, 0, a.categorizarDeVerdade(t, "2026-08").Categorized)
	require.Nil(t, a.lancamentosDa(t, a.casa.ID, "2026-08")[0].CategoryID)

	// Remove "central" de Centro: o empate some e Padarias vence.
	vazia := []string{}
	_, err := a.categoriaSvc.Update(t.Context(), a.atorDeCategoria(), centro.ID, category.UpdateInput{Keywords: &vazia})
	require.NoError(t, err)

	previa = a.previaDeCategorizacao(t, "2026-08")
	require.Len(t, previa.Items, 1)
	assert.Equal(t, padarias.ID, previa.Items[0].CategoryID)
	assert.Equal(t, "padaria", previa.Items[0].MatchedKeyword)
	assert.Empty(t, previa.UnmatchedItems)
	real := a.categorizarDeVerdade(t, "2026-08")
	assert.EqualValues(t, 1, real.Categorized)
	assert.Equal(t, padarias.ID, texto(a.lancamentosDa(t, a.casa.ID, "2026-08")[0].CategoryID))

	// E a importação seguinte também sugere.
	lote2 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a2.csv", csvExtratoAPI(linhaExtrato{6, "-9.50", "02", "Transferência enviada pelo Pix - Padaria Central"})))
	linha2 := revisarPelaAPI(t, a, lote2.ID).Items[0]
	assert.Equal(t, padarias.ID, texto(linha2.SuggestedCategoryID))
	assert.Equal(t, 100, *linha2.MatchScore)
}
