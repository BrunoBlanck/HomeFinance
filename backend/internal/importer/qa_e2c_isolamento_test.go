package importer_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QA da E2c — ISOLAMENTO ENTRE CASAS (S1/BOLA, §7 da spec 0005 e §7 do plano)
// sobre o banco REAL. Os pacotes de domínio provam a regra com dublês que
// filtram por casa; estes testes provam que o filtro existe onde importa: no
// SQL do gormstore, atravessado pela importação, pelo auto-categorize, pelo
// GET /transfers e pelo 409 KEYWORD_TAKEN.
//
// A "outra casa" é sempre uma casa REAL com dado REAL — id inventado também
// dá 404, mas não prova isolamento.

func (a *ambiente) atorAlheioDeCategoria() category.Actor {
	return category.Actor{HouseholdID: a.alheia.ID, UserID: a.outroUsuario.ID, IP: "203.0.113.8"}
}

func (a *ambiente) atorAlheioDeConta() account.Actor {
	return account.Actor{HouseholdID: a.alheia.ID, UserID: a.outroUsuario.ID, IP: "203.0.113.8"}
}

func (a *ambiente) atorDeLancamento(householdID, userID string) transaction.Actor {
	return transaction.Actor{HouseholdID: householdID, UserID: userID, IP: "203.0.113.7"}
}

// analisarComo roda a fase 1 pelo serviço com o ator informado — para a casa
// alheia importar na conta dela.
func (a *ambiente) analisarComo(t *testing.T, ator importer.Actor, contaID, nome string, conteudo []byte) importer.BatchView {
	t.Helper()
	view, err := a.svc.Analyze(t.Context(), ator, importer.AnalyzeInput{AccountID: contaID, FileName: nome, Content: conteudo})
	require.NoError(t, err)
	return view
}

func (a *ambiente) linhasComo(t *testing.T, ator importer.Actor, loteID string) []importer.RowView {
	t.Helper()
	p, err := a.svc.Preview(t.Context(), ator, loteID, 0, 200)
	require.NoError(t, err)
	return p.Items
}

// parManual grava um par de transferência A→B pelo serviço de lançamentos,
// na casa do ator.
func (a *ambiente) parManual(t *testing.T, ator transaction.Actor, de, para string, valor int64, dia int) string {
	t.Helper()
	grupo := a.proximoID("00000000-0000-7000-f000")
	_, err := a.txSvc.CreateBatch(t.Context(), ator, transaction.CreateBatchInput{
		Source: transaction.SourceManual,
		Rows: []transaction.NewTransaction{
			{Kind: transaction.KindTransferOut, AccountID: de, AmountCents: valor, Description: "Transferência",
				OccurredOn: civil.MustNew(2026, 8, dia), TransferGroupID: &grupo, DedupKey: dedup.PairKey(grupo + "-out")},
			{Kind: transaction.KindTransferIn, AccountID: para, AmountCents: valor, Description: "Transferência",
				OccurredOn: civil.MustNew(2026, 8, dia), TransferGroupID: &grupo, DedupKey: dedup.PairKey(grupo + "-in")},
		},
	})
	require.NoError(t, err)
	return grupo
}

// transfersPelaAPI roda GET /transfers com a identidade informada.
func transfersPelaAPI(t *testing.T, a *ambiente, householdID, userID, query string) *httptest.ResponseRecorder {
	t.Helper()
	r := requisicao(t, http.MethodGet, "/api/v1/transfers?"+query, nil, identidade(householdID, userID))
	rec := httptest.NewRecorder()
	a.handlerLancamento().ListTransfers(rec, r)
	return rec
}

func transfersDecodificadas(t *testing.T, rec *httptest.ResponseRecorder) transaction.TransferListView {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var view transaction.TransferListView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	return view
}

// --- palavra-chave da casa B nunca sugere nada na casa A ----------------------

func TestPalavraChaveDaCasaBNuncaSugereNaCasaANoSQLite(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	// Casa A: conta corrente SEM palavra nenhuma; nenhuma categoria.
	contaA := a.contaCorrente(t)

	// Casa B: categoria "padaria" e conta "itau" — as MESMAS palavras que a
	// casa A usaria.
	contaB := a.conta(t, a.alheia.ID, "Itaú da Vizinha", account.KindChecking, account.InstitutionOther)
	palavras := []string{"itau"}
	_, err := a.contaSvc.Update(t.Context(), a.atorAlheioDeConta(), contaB.ID, account.UpdateInput{Keywords: &palavras})
	require.NoError(t, err)
	catB, err := a.categoriaSvc.Create(t.Context(), a.atorAlheioDeCategoria(), category.CreateInput{
		Name: "Padaria da Vizinha", Kind: category.KindExpense, Keywords: []string{"padaria"},
	})
	require.NoError(t, err)

	extrato := csvExtratoAPI(
		linhaExtrato{5, "-11.00", "01", "Transferência enviada pelo Pix - Padaria Exemplo"},
		linhaExtrato{6, "-150.00", "02", "Transferência enviada pelo Pix - Itau Corrente"},
	)

	// Na casa A: nada sugerido, nenhuma transferência detectada.
	loteA := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extrato))
	assert.Equal(t, 2, loteA.Counts.New)
	assert.Zero(t, loteA.Counts.InternalTransfer)
	for _, l := range revisarPelaAPI(t, a, loteA.ID).Items {
		assert.Equal(t, string(dedup.StatusNew), l.Status)
		assert.Nil(t, l.SuggestedCategoryID, "%s: a palavra da vizinha sugeriu categoria", texto(l.Description))
		assert.Nil(t, l.SuggestedCounterpartAccountID, "%s: a conta da vizinha virou contraparte", texto(l.Description))
		assert.Nil(t, l.MatchScore)
		assert.Nil(t, l.MatchedKeyword)
	}

	// Controle: na casa B as mesmas linhas SÃO sugeridas — a ausência em A é
	// isolamento, não palavra que não casa. (Conta de B é `other`: aceita o
	// CSV do Nubank; a linha "Itau" cita a própria conta do lote, então só a
	// padaria sugere.)
	loteB := a.analisarComo(t, a.atorAlheio(), contaB.ID, "b.csv", extrato)
	padariaB := linhaComDescricao(t, a.linhasComo(t, a.atorAlheio(), loteB.ID), "Pix enviado - Padaria Exemplo")
	assert.Equal(t, catB.ID, texto(padariaB.SuggestedCategoryID))
}

// --- auto-categorize de A nunca toca lançamento de B ---------------------------

func TestAutoCategorizeDaCasaANuncaTocaLancamentoDaCasaBNoSQLite(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	// As duas casas têm a MESMA palavra e o MESMO lançamento sem categoria.
	contaA := a.contaCorrente(t)
	catA := a.categoriaComPalavras(t, "Alimentação", category.KindExpense, "padaria")
	contaB := a.conta(t, a.alheia.ID, "Conta da Vizinha", account.KindChecking, account.InstitutionOther)
	catB, err := a.categoriaSvc.Create(t.Context(), a.atorAlheioDeCategoria(), category.CreateInput{
		Name: "Padaria dela", Kind: category.KindExpense, Keywords: []string{"padaria"},
	})
	require.NoError(t, err)

	extrato := csvExtratoAPI(linhaExtrato{5, "-11.00", "01", "Transferência enviada pelo Pix - Padaria Exemplo"})
	loteA := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extrato))
	linhaA := revisarPelaAPI(t, a, loteA.ID).Items[0]
	// Entra SEM categoria nas duas casas (categoryId: null explícito).
	resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"import","categoryId":null}]}`, linhaA.ID)))

	loteB := a.analisarComo(t, a.atorAlheio(), contaB.ID, "b.csv", extrato)
	linhaB := a.linhasComo(t, a.atorAlheio(), loteB.ID)[0]
	_, err = a.svc.Confirm(t.Context(), a.atorAlheio(), loteB.ID, importer.ConfirmInput{
		Decisions: []importer.Decision{{RowID: linhaB.ID, Action: importer.ActionImport, CategoryID: importer.OptionalCategory{Set: true}}},
	})
	require.NoError(t, err)

	lancA := a.lancamentosDa(t, a.casa.ID, "2026-08")
	lancB := a.lancamentosDa(t, a.alheia.ID, "2026-08")
	require.Len(t, lancA, 1)
	require.Len(t, lancB, 1)
	require.Nil(t, lancA[0].CategoryID)
	require.Nil(t, lancB[0].CategoryID)

	atorA := a.atorDeLancamento(a.casa.ID, a.usuario.ID)
	atorB := a.atorDeLancamento(a.alheia.ID, a.outroUsuario.ID)

	// Prévia de A: só a linha de A, com a categoria de A.
	previa, err := a.txSvc.AutoCategorize(t.Context(), atorA, transaction.AutoCategorizeInput{Month: "2026-08", DryRun: true})
	require.NoError(t, err)
	require.Len(t, previa.Items, 1)
	assert.Equal(t, lancA[0].ID, previa.Items[0].ID)
	assert.Equal(t, catA.ID, previa.Items[0].CategoryID)
	assert.NotEqual(t, catB.ID, previa.Items[0].CategoryID)
	assert.NotContains(t, fmt.Sprint(previa), lancB[0].ID, "o id do lançamento da vizinha não aparece na prévia")

	// Execução real de A: a linha de B continua NULA.
	real, err := a.txSvc.AutoCategorize(t.Context(), atorA, transaction.AutoCategorizeInput{Month: "2026-08"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, real.Categorized)
	depoisA := a.lancamentosDa(t, a.casa.ID, "2026-08")[0]
	depoisB := a.lancamentosDa(t, a.alheia.ID, "2026-08")[0]
	assert.Equal(t, catA.ID, texto(depoisA.CategoryID))
	assert.Nil(t, depoisB.CategoryID, "a execução de A escreveu no lançamento de B")

	// E a de B, depois, grava a categoria de B — nunca a de A.
	realB, err := a.txSvc.AutoCategorize(t.Context(), atorB, transaction.AutoCategorizeInput{Month: "2026-08"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, realB.Categorized)
	depoisB = a.lancamentosDa(t, a.alheia.ID, "2026-08")[0]
	assert.Equal(t, catB.ID, texto(depoisB.CategoryID))
	assert.Equal(t, catA.ID, texto(a.lancamentosDa(t, a.casa.ID, "2026-08")[0].CategoryID), "a execução de B não mexeu em A")
}

// --- GET /transfers de A nunca lista o par de B ---------------------------------

func TestTransfersDaCasaANuncaListaParDaCasaBNoSQLite(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	contaA1 := a.conta(t, a.casa.ID, "Nubank", account.KindChecking, "nubank")
	contaA2 := a.conta(t, a.casa.ID, "Itaú", account.KindChecking, account.InstitutionOther)
	contaB1 := a.conta(t, a.alheia.ID, "Vizinha 1", account.KindChecking, account.InstitutionOther)
	contaB2 := a.conta(t, a.alheia.ID, "Vizinha 2", account.KindChecking, account.InstitutionOther)

	grupoA := a.parManual(t, a.atorDeLancamento(a.casa.ID, a.usuario.ID), contaA1.ID, contaA2.ID, 100_00, 5)
	grupoB := a.parManual(t, a.atorDeLancamento(a.alheia.ID, a.outroUsuario.ID), contaB1.ID, contaB2.ID, 999_99, 5)

	view := transfersDecodificadas(t, transfersPelaAPI(t, a, a.casa.ID, a.usuario.ID, "month=2026-08"))
	require.Len(t, view.Items, 1)
	assert.Equal(t, grupoA, view.Items[0].GroupID)
	require.Len(t, view.Pairs, 1)
	assert.EqualValues(t, 100_00, view.Pairs[0].AToBCents+view.Pairs[0].BToACents)
	require.Len(t, view.Balances, 2)
	for _, b := range view.Balances {
		assert.NotEqual(t, contaB1.ID, b.AccountID)
		assert.NotEqual(t, contaB2.ID, b.AccountID)
	}
	corpo, err := json.Marshal(view)
	require.NoError(t, err)
	assert.NotContains(t, string(corpo), grupoB)
	assert.NotContains(t, string(corpo), "Vizinha")
	assert.NotContains(t, string(corpo), "99999")

	// Filtro pela conta de B, a partir de A: 404 byte a byte igual ao
	// inexistente — e nunca uma lista vazia "bem-sucedida".
	alheia := transfersPelaAPI(t, a, a.casa.ID, a.usuario.ID, "month=2026-08&accountId="+contaB1.ID)
	inexistente := transfersPelaAPI(t, a, a.casa.ID, a.usuario.ID, "month=2026-08&accountId="+idInexistente)
	require.Equal(t, http.StatusNotFound, alheia.Code, alheia.Body.String())
	assert.Equal(t, inexistente.Body.String(), alheia.Body.String())
	contraparteAlheia := transfersPelaAPI(t, a, a.casa.ID, a.usuario.ID, "month=2026-08&accountId="+contaA1.ID+"&counterpartAccountId="+contaB2.ID)
	require.Equal(t, http.StatusNotFound, contraparteAlheia.Code)
	assert.Equal(t, inexistente.Body.String(), contraparteAlheia.Body.String())

	// Do lado de B, só o par de B.
	viewB := transfersDecodificadas(t, transfersPelaAPI(t, a, a.alheia.ID, a.outroUsuario.ID, "month=2026-08"))
	require.Len(t, viewB.Items, 1)
	assert.Equal(t, grupoB, viewB.Items[0].GroupID)
}

// --- KEYWORD_TAKEN nunca cita a dona de B -------------------------------------

func TestKeywordTakenNuncaCitaDonaDaCasaBNoSQLite(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	ctx := t.Context()

	// B tem "padaria" numa categoria e "itau" numa conta.
	catB, err := a.categoriaSvc.Create(ctx, a.atorAlheioDeCategoria(), category.CreateInput{
		Name: "Padaria dela", Kind: category.KindExpense, Keywords: []string{"padaria"},
	})
	require.NoError(t, err)
	contaB := a.conta(t, a.alheia.ID, "Itaú dela", account.KindChecking, account.InstitutionOther)
	itau := []string{"itau"}
	_, err = a.contaSvc.Update(ctx, a.atorAlheioDeConta(), contaB.ID, account.UpdateInput{Keywords: &itau})
	require.NoError(t, err)

	// A grava as mesmas palavras: 2xx — a unicidade é por casa.
	catA := a.categoriaComPalavras(t, "Alimentação", category.KindExpense, "padaria")
	contaA := a.conta(t, a.casa.ID, "Itaú Corrente", account.KindChecking, account.InstitutionOther)
	a.palavrasNaConta(t, contaA.ID, "itau")

	// A mesma palavra em categoria E em conta da MESMA casa: conjuntos
	// independentes, 2xx.
	a.palavrasNaConta(t, contaA.ID, "itau", "padaria")
	lida, err := a.contaSvc.Get(ctx, a.atorDeConta(), contaA.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"itau", "padaria"}, lida.Keywords)

	// Segunda categoria de A com "padaria": 409 citando a categoria DE A.
	_, err = a.categoriaSvc.Create(ctx, a.atorDeCategoria(), category.CreateInput{
		Name: "Casa", Kind: category.KindExpense, Keywords: []string{"PADARIA"},
	})
	var tomada *category.KeywordTakenError
	require.ErrorAs(t, err, &tomada)
	assert.Equal(t, catA.ID, tomada.OwnerID)
	assert.NotEqual(t, catB.ID, tomada.OwnerID, "a dona citada é da OUTRA casa")

	// Segunda conta de A com "itau": 409 citando a conta DE A.
	contaA2 := a.conta(t, a.casa.ID, "Outra", account.KindChecking, account.InstitutionOther)
	_, err = a.contaSvc.Update(ctx, a.atorDeConta(), contaA2.ID, account.UpdateInput{Keywords: &itau})
	var tomadaConta *account.KeywordTakenError
	require.ErrorAs(t, err, &tomadaConta)
	assert.Equal(t, contaA.ID, tomadaConta.OwnerID)
	assert.NotEqual(t, contaB.ID, tomadaConta.OwnerID)

	// Nada disso mexeu nas palavras de B.
	catBLida, err := a.categoriaSvc.Get(ctx, a.atorAlheioDeCategoria(), catB.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"padaria"}, catBLida.Keywords)
	contaBLida, err := a.contaSvc.Get(ctx, a.atorAlheioDeConta(), contaB.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"itau"}, contaBLida.Keywords)
}

// --- transfer para contraparte ARQUIVADA da própria casa -----------------------

// Cruzamento importer → transaction: contraparte explícita arquivada é 422 e
// nada é gravado (nem a linha que entraria por default).
func TestTransferComContraparteArquivadaResponde422ENadaEGravado(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	a.contaItau(t)
	arquivada := a.conta(t, a.casa.ID, "Poupança Antiga", account.KindSavings, account.InstitutionOther)
	_, err := a.contaSvc.Archive(t.Context(), a.atorDeConta(), arquivada.ID)
	require.NoError(t, err)

	extrato := csvExtratoAPI(
		linhaExtrato{5, "-1500.00", "01", "Transferência enviada pelo Pix - Itau Corrente"},
		linhaExtrato{6, "-11.00", "02", "Transferência enviada pelo Pix - Padaria Exemplo"},
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extrato))
	linha := linhaPorStatus(t, revisarPelaAPI(t, a, lote.ID).Items, dedup.StatusInternalTransfer)

	rec := confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}]}`, linha.ID, arquivada.ID))
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "arquivada")
	// Spec 0005 §12: o campo é o da CONTRAPARTE — a única das duas contas que
	// está no corpo do confirm. `accountId` (a conta do lote, herdado da E2)
	// mandaria desarquivar a conta errada.
	campos := camposDoValidationFailed(t, rec)
	assert.Contains(t, campos, "counterpartAccountId")
	assert.NotContains(t, campos, "accountId")
	assert.NotContains(t, rec.Body.String(), arquivada.ID, "a resposta não ecoa o id")
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), "tudo ou nada")
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, lote.ID).Batch.Status)
}
