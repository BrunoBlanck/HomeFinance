package importer_test

import (
	"fmt"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 0005 §13, "Correção de robustez" (achado M3 da revisão da emenda).
//
// A categoria que a ANÁLISE sugeriu fica gravada no staging e vira o default
// da linha no confirm. Entre os dois momentos o mundo muda: alguém arquiva a
// categoria, exclui, ou cria uma subcategoria nela (o gatilho novo da §13).
// Antes desta correção, o serviço de lançamentos recusava a linha e o LOTE
// INTEIRO voltava com um 422 apontando `categoryId` — um campo que o cliente
// nem tinha mandado.
//
// A regra que estes testes vigiam: a sugestão obsoleta é degradada para "sem
// categoria" NAQUELA linha, o lote commita, e só a categoria que o CLIENTE
// enviou (`decisions[].categoryId` ou `defaultCategoryId`) continua produzindo
// 422. O staging não é reescrito — o que mudou foi o mundo, não o registro do
// que a análise viu.

// --- montagem ---------------------------------------------------------------

// Os identificadores do extrato de teste. Procurar o lançamento pelo
// `externalId` (e não pela descrição) é o que torna a asserção imune ao
// sanitizador: ele reescreve a descrição, nunca o identificador.
const (
	idPosto   = "11111111-1111-4111-8111-111111111101"
	idEnergia = "11111111-1111-4111-8111-111111111102"
	idSemPala = "11111111-1111-4111-8111-111111111103"
	idSalario = "11111111-1111-4111-8111-111111111104"
)

// extratoComDuasSugestoes é o arquivo dos cenários: duas linhas com sugestão
// (uma por categoria distinta), uma despesa sem palavra nenhuma e uma receita.
//
// Duas categorias distintas de propósito: a que fica obsoleta prova a
// degradação, e a que continua válida prova que a correção não jogou fora as
// sugestões boas junto.
func extratoComDuasSugestoes() []byte {
	return csvExtratoAPI(
		linhaExtrato{3, "-139.92", "01", "Compra no Posto Exemplo"},
		linhaExtrato{4, "-187.07", "02", "Conta de Energia Exemplo"},
		linhaExtrato{5, "-20.00", "03", "Compra sem nenhuma palavra conhecida"},
		linhaExtrato{6, "1600.00", "04", "Deposito recebido"},
	)
}

// cenarioDeSugestao devolve o ambiente com a conta do lote e as DUAS
// categorias sugeridas: `combustivel` (palavra "posto") é a que cada teste
// estraga; `energia` (palavra "energia") é a testemunha que tem de sobreviver.
func cenarioDeSugestao(t *testing.T) (*ambiente, *account.Account, category.View, category.View) {
	t.Helper()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	combustivel := a.categoriaComPalavras(t, "Combustível", category.KindExpense, "posto")
	energia := a.categoriaComPalavras(t, "Energia", category.KindExpense, "energia")
	return a, conta, combustivel, energia
}

// loteComSugestoes envia o extrato e CONFERE na revisão que as duas sugestões
// chegaram à tela. Sem esta conferência o teste passaria à toa: uma análise
// que não sugere nada também não é derrubada pelo confirm.
func loteComSugestoes(t *testing.T, a *ambiente, conta *account.Account, combustivel, energia category.View) importer.BatchView {
	t.Helper()
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "NU_2026-08.csv", extratoComDuasSugestoes()))
	itens := revisarPelaAPI(t, a, lote.ID).Items
	require.Equal(t, combustivel.ID, texto(linhaPorExternalID(t, itens, idPosto).SuggestedCategoryID),
		"a revisão precisa ter VISTO a sugestão que vai ficar obsoleta")
	require.Equal(t, energia.ID, texto(linhaPorExternalID(t, itens, idEnergia).SuggestedCategoryID))
	return lote
}

// linhaPorExternalID acha a linha da revisão pelo identificador do arquivo.
func linhaPorExternalID(t *testing.T, itens []importer.RowView, externalID string) importer.RowView {
	t.Helper()
	for _, l := range itens {
		if l.ExternalID != nil && *l.ExternalID == externalID {
			return l
		}
	}
	t.Fatalf("linha com externalId %q não encontrada na revisão", externalID)
	return importer.RowView{}
}

// lancamentoPorExternalID acha o lançamento GRAVADO pelo identificador.
func (a *ambiente) lancamentoPorExternalID(t *testing.T, mes, externalID string) transaction.Transaction {
	t.Helper()
	for _, l := range a.lancamentosDa(t, a.casa.ID, mes) {
		if l.ExternalID != nil && *l.ExternalID == externalID {
			return l
		}
	}
	t.Fatalf("lançamento com externalId %q não encontrado em %s", externalID, mes)
	return transaction.Transaction{}
}

// conferirDegradacao é o desfecho comum aos três gatilhos: o lote commita, a
// linha da sugestão obsoleta entra SEM categoria, e todas as demais entram
// normalmente — inclusive a que tinha a outra sugestão, que continua válida.
func conferirDegradacao(t *testing.T, a *ambiente, lote importer.BatchView, energia category.View) {
	t.Helper()

	rec := confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`)
	res := resultadoDaResposta(t, rec) // exige 200: nada de 422
	assert.Equal(t, 4, res.Imported, "as quatro linhas entram")
	assert.Zero(t, res.Blocked, "degradar não bloqueia linha")
	assert.Equal(t, importer.BatchStatusCommitted, res.Status)

	posto := a.lancamentoPorExternalID(t, "2026-08", idPosto)
	assert.Nil(t, posto.CategoryID, "sugestão obsoleta: a linha entra SEM categoria")

	luz := a.lancamentoPorExternalID(t, "2026-08", idEnergia)
	require.NotNil(t, luz.CategoryID, "a sugestão que continua válida não pode ser degradada junto")
	assert.Equal(t, energia.ID, *luz.CategoryID)

	assert.Nil(t, a.lancamentoPorExternalID(t, "2026-08", idSemPala).CategoryID)
	assert.Nil(t, a.lancamentoPorExternalID(t, "2026-08", idSalario).CategoryID)
	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 4)
}

// --- os três gatilhos --------------------------------------------------------

// Gatilho 1 — ARQUIVADA. É o modo de falha que já existia antes da §13
// (ErrCategoryArchived): abrir a revisão, arquivar a categoria numa outra aba
// e confirmar derrubava o lote inteiro.
func TestSugestaoArquivadaEntreAnaliseEConfirmNaoDerrubaOLote(t *testing.T) {
	t.Parallel()
	a, conta, combustivel, energia := cenarioDeSugestao(t)
	lote := loteComSugestoes(t, a, conta, combustivel, energia)

	_, err := a.categoriaSvc.Archive(t.Context(), a.atorDeCategoria(), combustivel.ID)
	require.NoError(t, err)

	conferirDegradacao(t, a, lote, energia)
}

// Gatilho 2 — virou GRUPO COM SUBCATEGORIA ativa. É o que a §13 acrescentou:
// a categoria continua ativa, continua da casa, e mesmo assim deixou de
// receber lançamento.
func TestSugestaoViraGrupoComSubcategoriaEntreAnaliseEConfirmNaoDerrubaOLote(t *testing.T) {
	t.Parallel()
	a, conta, combustivel, energia := cenarioDeSugestao(t)
	lote := loteComSugestoes(t, a, conta, combustivel, energia)

	filha, err := a.categoriaSvc.Create(t.Context(), a.atorDeCategoria(), category.CreateInput{
		Name: "Gasolina", ParentID: &combustivel.ID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, filha.ID)

	conferirDegradacao(t, a, lote, energia)
}

// Gatilho 3 — EXCLUÍDA. A exclusão é lógica e leva as palavras-chave junto; a
// sugestão gravada fica apontando para o que ninguém mais vê.
func TestSugestaoExcluidaEntreAnaliseEConfirmNaoDerrubaOLote(t *testing.T) {
	t.Parallel()
	a, conta, combustivel, energia := cenarioDeSugestao(t)
	lote := loteComSugestoes(t, a, conta, combustivel, energia)

	require.NoError(t, a.categoriaSvc.Delete(t.Context(), a.atorDeCategoria(), combustivel.ID))

	conferirDegradacao(t, a, lote, energia)
}

// Gatilho 4 — a NATUREZA da categoria mudou. Trocar receita por despesa só é
// possível enquanto a categoria não está em uso, e uma sugestão ainda não
// aplicada é exatamente isso: a categoria continua ativa, da casa e sem
// filhas, e mesmo assim deixou de servir para aquela linha
// (ErrCategoryKindMismatch).
func TestSugestaoQueTrocouDeNaturezaEntreAnaliseEConfirmNaoDerrubaOLote(t *testing.T) {
	t.Parallel()
	a, conta, combustivel, energia := cenarioDeSugestao(t)
	lote := loteComSugestoes(t, a, conta, combustivel, energia)

	receita := category.KindIncome
	_, err := a.categoriaSvc.Update(t.Context(), a.atorDeCategoria(), combustivel.ID,
		category.UpdateInput{Kind: &receita})
	require.NoError(t, err)

	conferirDegradacao(t, a, lote, energia)
}

// --- o contraste: o que o CLIENTE mandou continua sendo 422 ------------------

// A categoria da DECISÃO é escolha explícita de quem confirmou: se ela não
// vale, a resposta certa é 422 em `fields.categoryId`, com o lote inteiro
// intacto. Degradá-la em silêncio gravaria um lançamento sem a categoria que
// a pessoa acabou de escolher.
func TestCategoriaArquivadaNaDecisaoDoClienteContinuaSendo422(t *testing.T) {
	t.Parallel()
	a, conta, combustivel, energia := cenarioDeSugestao(t)
	lote := loteComSugestoes(t, a, conta, combustivel, energia)
	linha := linhaPorExternalID(t, revisarPelaAPI(t, a, lote.ID).Items, idSemPala)

	_, err := a.categoriaSvc.Archive(t.Context(), a.atorDeCategoria(), combustivel.ID)
	require.NoError(t, err)

	corpo := fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"import","categoryId":%q}]}`,
		linha.ID, combustivel.ID)
	campos := camposDoValidationFailed(t, confirmarPelaAPI(t, a, lote.ID, corpo))
	assert.Contains(t, campos, "categoryId")

	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), "tudo ou nada: nada gravado")
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, lote.ID).Batch.Status)
}

// O `defaultCategoryId` também é do cliente — e chega no corpo, não no
// staging. Continua 422.
func TestDefaultCategoryIdArquivadoContinuaSendo422(t *testing.T) {
	t.Parallel()
	a, conta, combustivel, energia := cenarioDeSugestao(t)
	lote := loteComSugestoes(t, a, conta, combustivel, energia)

	padrao := a.categoriaComPalavras(t, "Outros Gastos", category.KindExpense)
	_, err := a.categoriaSvc.Archive(t.Context(), a.atorDeCategoria(), padrao.ID)
	require.NoError(t, err)

	corpo := fmt.Sprintf(`{"decisions":[],"defaultCategoryId":%q}`, padrao.ID)
	campos := camposDoValidationFailed(t, confirmarPelaAPI(t, a, lote.ID, corpo))
	// A mensagem confirma que o 422 é o de ARQUIVADA — e não o de natureza
	// incompatível, que o mesmo campo também produz.
	assert.Contains(t, campos["categoryId"], "arquivada")

	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), "tudo ou nada: nada gravado")
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, lote.ID).Batch.Status)
}

// O grupo-com-filhas mandado pelo CLIENTE também continua 422 — o contraste
// exato do gatilho 2.
func TestGrupoComSubcategoriaNaDecisaoDoClienteContinuaSendo422(t *testing.T) {
	t.Parallel()
	a, conta, combustivel, energia := cenarioDeSugestao(t)
	lote := loteComSugestoes(t, a, conta, combustivel, energia)
	linha := linhaPorExternalID(t, revisarPelaAPI(t, a, lote.ID).Items, idSemPala)

	_, err := a.categoriaSvc.Create(t.Context(), a.atorDeCategoria(), category.CreateInput{
		Name: "Gasolina", ParentID: &combustivel.ID,
	})
	require.NoError(t, err)

	corpo := fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"import","categoryId":%q}]}`,
		linha.ID, combustivel.ID)
	campos := camposDoValidationFailed(t, confirmarPelaAPI(t, a, lote.ID, corpo))
	assert.Contains(t, campos, "categoryId")

	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), "tudo ou nada: nada gravado")
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, lote.ID).Batch.Status)
}

// --- não-regressão: a sugestão boa continua sendo aplicada ------------------

// Sem nada mudar entre a análise e o confirm, as DUAS sugestões entram. É o
// teste que impede a correção de virar "nenhuma sugestão vale".
func TestSugestaoValidaContinuaSendoAplicadaNoConfirm(t *testing.T) {
	t.Parallel()
	a, conta, combustivel, energia := cenarioDeSugestao(t)
	lote := loteComSugestoes(t, a, conta, combustivel, energia)

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	assert.Equal(t, 4, res.Imported)

	posto := a.lancamentoPorExternalID(t, "2026-08", idPosto)
	require.NotNil(t, posto.CategoryID)
	assert.Equal(t, combustivel.ID, *posto.CategoryID)

	luz := a.lancamentoPorExternalID(t, "2026-08", idEnergia)
	require.NotNil(t, luz.CategoryID)
	assert.Equal(t, energia.ID, *luz.CategoryID)
}

// A sugestão obsoleta NÃO cai no `defaultCategoryId`: ela sai em "sem
// categoria", visível, para a pessoa recategorizar. Enterrá-la no balde que a
// pessoa escolheu para as linhas SEM sugestão esconderia o fato de que a
// sugestão que a tela mostrou venceu.
func TestSugestaoObsoletaNaoCaiNoDefaultCategoryId(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	combustivel := a.categoriaComPalavras(t, "Combustível", category.KindExpense, "posto")
	padrao := a.categoriaComPalavras(t, "Outros Gastos", category.KindExpense)

	// Extrato só de DESPESAS: `defaultCategoryId` é um id só para o arquivo
	// inteiro, e uma receita nele bateria em ErrCategoryKindMismatch antes de
	// o teste chegar ao que quer provar.
	extrato := csvExtratoAPI(
		linhaExtrato{3, "-139.92", "01", "Compra no Posto Exemplo"},
		linhaExtrato{5, "-20.00", "03", "Compra sem nenhuma palavra conhecida"},
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "NU_2026-08.csv", extrato))
	require.Equal(t, combustivel.ID,
		texto(linhaPorExternalID(t, revisarPelaAPI(t, a, lote.ID).Items, idPosto).SuggestedCategoryID))

	_, err := a.categoriaSvc.Archive(t.Context(), a.atorDeCategoria(), combustivel.ID)
	require.NoError(t, err)

	corpo := fmt.Sprintf(`{"decisions":[],"defaultCategoryId":%q}`, padrao.ID)
	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, corpo))
	assert.Equal(t, 2, res.Imported)

	assert.Nil(t, a.lancamentoPorExternalID(t, "2026-08", idPosto).CategoryID,
		"a linha da sugestão obsoleta fica SEM categoria, não no padrão")

	// A testemunha do outro lado: a linha que NUNCA teve sugestão continua
	// recebendo o padrão do cliente, como sempre.
	semPalavra := a.lancamentoPorExternalID(t, "2026-08", idSemPala)
	require.NotNil(t, semPalavra.CategoryID)
	assert.Equal(t, padrao.ID, *semPalavra.CategoryID)
}

// --- desempenho: uma carga por confirm, nunca uma por linha ------------------

// Sem N+1: 120 linhas com sugestão carregam o conjunto de categorias da casa
// UMA vez (as duas consultas de categoria do classify.Load), e não 120.
//
// O contador embute o repositório real, então o que ele mede é a consulta que
// de fato foi ao SQLite.
func TestConferenciaDasSugestoesNaoConsultaPorLinha(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	combustivel := a.categoriaComPalavras(t, "Combustível", category.KindExpense, "posto")

	linhas := make([]linhaExtrato, 0, 120)
	for i := range 120 {
		linhas = append(linhas, linhaExtrato{
			dia:       1 + i%28,
			valor:     fmt.Sprintf("-%d.%02d", 10+i, i%100),
			idSufixo:  fmt.Sprintf("%02x", i),
			descricao: "Compra no Posto Exemplo",
		})
	}
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "NU_2026-08.csv", csvExtratoAPI(linhas...)))
	require.Equal(t, 120, lote.RowCount)

	// Zerar DEPOIS da análise: o que se mede é o confirm.
	a.contadorDeCategorias.zerar()
	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	require.Equal(t, 120, res.Imported)

	listas, palavras := a.contadorDeCategorias.carregamentos()
	assert.Equal(t, int64(1), listas, "uma listagem de categorias no confirm inteiro")
	assert.Equal(t, int64(1), palavras, "uma listagem de palavras-chave no confirm inteiro")

	// E a sugestão valeu para todas as 120.
	for _, l := range a.lancamentosDa(t, a.casa.ID, "2026-08") {
		require.NotNil(t, l.CategoryID)
		require.Equal(t, combustivel.ID, *l.CategoryID)
	}
}

// Lote sem sugestão nenhuma não paga nada: zero consulta de categoria a mais
// no confirm.
func TestLoteSemSugestaoNaoCarregaAsCategorias(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	extrato := csvExtratoAPI(
		linhaExtrato{3, "-139.92", "01", "Compra sem nenhuma palavra conhecida"},
		linhaExtrato{4, "-187.07", "02", "Outra compra qualquer"},
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "NU_2026-08.csv", extrato))

	a.contadorDeCategorias.zerar()
	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	require.Equal(t, 2, res.Imported)

	listas, palavras := a.contadorDeCategorias.carregamentos()
	assert.Zero(t, listas, "nenhuma linha trouxe sugestão: nada a conferir")
	assert.Zero(t, palavras)
}
