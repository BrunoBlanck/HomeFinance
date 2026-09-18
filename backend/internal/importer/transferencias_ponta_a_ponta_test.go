package importer_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

// Spec 0005 — palavras-chave, categorização automática e transferências
// internas — exercitada PONTA A PONTA pela API, sobre SQLite real: critérios 3
// a 8 da §8 e os casos de abuso da §9 do plano E2c.
//
// O que estes testes vigiam com mais cuidado é o vetor novo desta entrega: a
// ação `link` ESCREVE numa linha que já existe. A perna tem de vir só do
// staging (nunca do corpo), ser reconferida por casa e conta no commit, e
// nunca criar nem alterar movimento de dinheiro.

// --- montagem ---------------------------------------------------------------

func (a *ambiente) atorDeCategoria() category.Actor {
	return category.Actor{HouseholdID: a.casa.ID, UserID: a.usuario.ID, IP: "203.0.113.7"}
}

// palavrasNaConta grava as palavras-chave pelo SERVIÇO de contas — o mesmo
// caminho do PATCH /accounts/{id}, com a validação e a unicidade reais.
func (a *ambiente) palavrasNaConta(t *testing.T, contaID string, palavras ...string) {
	t.Helper()
	_, err := a.contaSvc.Update(t.Context(), a.atorDeConta(), contaID, account.UpdateInput{Keywords: &palavras})
	require.NoError(t, err)
}

// categoriaComPalavras cria uma categoria folha com palavras-chave.
func (a *ambiente) categoriaComPalavras(t *testing.T, nome, kind string, palavras ...string) category.View {
	t.Helper()
	v, err := a.categoriaSvc.Create(t.Context(), a.atorDeCategoria(), category.CreateInput{
		Name: nome, Kind: kind, Keywords: palavras,
	})
	require.NoError(t, err)
	return v
}

// contaItau é a conta B dos cenários: corrente, instituição `other` (aceita o
// CSV do Nubank sem travar), com a palavra-chave "itau".
func (a *ambiente) contaItau(t *testing.T) *account.Account {
	t.Helper()
	c := a.conta(t, a.casa.ID, "Itaú Corrente", account.KindChecking, account.InstitutionOther)
	a.palavrasNaConta(t, c.ID, "itau")
	return c
}

// contaNubankComPalavra é a conta A: a corrente do Nubank com a palavra-chave
// "nubank", para o extrato de B reconhecê-la como contraparte.
func (a *ambiente) contaNubankComPalavra(t *testing.T) *account.Account {
	t.Helper()
	c := a.contaCorrente(t)
	a.palavrasNaConta(t, c.ID, "nubank")
	return c
}

// saldos devolve id -> saldo derivado de cada conta da casa, pelo serviço.
func (a *ambiente) saldos(t *testing.T) map[string]int64 {
	t.Helper()
	lista, err := a.contaSvc.List(t.Context(), a.atorDeConta(), true)
	require.NoError(t, err)
	out := make(map[string]int64, len(lista.Items))
	for _, c := range lista.Items {
		out[c.ID] = c.BalanceCents
	}
	return out
}

// somaDeSaldos é a propriedade que o `link` tem de preservar: nenhum
// movimento novo, nenhum centavo a mais em lugar nenhum.
func somaDeSaldos(m map[string]int64) int64 {
	var total int64
	for _, v := range m {
		total += v
	}
	return total
}

// pernasDe devolve as pernas de transferência vivas de uma conta no mês.
func (a *ambiente) pernasDe(t *testing.T, contaID, mes string) []transaction.Transaction {
	t.Helper()
	var out []transaction.Transaction
	for _, l := range a.lancamentosDa(t, a.casa.ID, mes) {
		if l.AccountID == contaID && (l.Kind == transaction.KindTransferOut || l.Kind == transaction.KindTransferIn) {
			out = append(out, l)
		}
	}
	return out
}

// linhaPorStatus acha a primeira linha da revisão com aquele status.
func linhaPorStatus(t *testing.T, itens []importer.RowView, status dedup.Status) importer.RowView {
	t.Helper()
	for _, l := range itens {
		if l.Status == string(status) {
			return l
		}
	}
	t.Fatalf("nenhuma linha com status %q", status)
	return importer.RowView{}
}

func texto(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

// --- critérios 3 e 4: classificação -----------------------------------------

func TestClassificacaoSugereCategoriaEContraparteComAsRegrasDaSpec(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	contaA := a.contaNubankComPalavra(t)
	contaB := a.contaItau(t)
	arquivada := a.conta(t, a.casa.ID, "Bradesco Antigo", account.KindChecking, account.InstitutionOther)
	a.palavrasNaConta(t, arquivada.ID, "bradesco")
	_, err := a.contaSvc.Archive(t.Context(), a.atorDeConta(), arquivada.ID)
	require.NoError(t, err)
	cartao := a.contaCartao(t)
	a.palavrasNaConta(t, cartao.ID, "fatura")

	alimentacao := a.categoriaComPalavras(t, "Alimentação", category.KindExpense, "padaria")
	transporte := a.categoriaComPalavras(t, "Transporte", category.KindExpense, "uber")
	_, err = a.categoriaSvc.Archive(t.Context(), a.atorDeCategoria(), transporte.ID)
	require.NoError(t, err)

	csv := csvExtratoAPI(
		linhaExtrato{5, "-150.00", "01", "Transferência enviada pelo Pix - Padaria Itau"},     // conta E categoria batem
		linhaExtrato{6, "-11.00", "02", "Transferência enviada pelo Pix - Padaria Exemplo"},   // só categoria
		linhaExtrato{7, "-30.00", "03", "Transferência enviada pelo Pix - Nubank Proprio"},    // palavra da PRÓPRIA conta
		linhaExtrato{8, "-40.00", "04", "Transferência enviada pelo Pix - Bradesco Antigo"},   // conta arquivada
		linhaExtrato{9, "-50.00", "05", "Uber Trip"},                                          // categoria arquivada
		linhaExtrato{10, "100.00", "06", "Transferência recebida pelo Pix - Padaria Exemplo"}, // receita: categoria de despesa não serve
		linhaExtrato{11, "-2859.82", "07", "Pagamento de fatura"},                             // pagamento de fatura com conta batendo
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "extrato.csv", csv))
	require.NotNil(t, lote.Counts)
	assert.Equal(t, 1, lote.Counts.InternalTransfer)
	assert.Equal(t, 0, lote.Counts.TransferAlreadyRegistered)
	assert.Equal(t, 1, lote.Counts.CardPayment)
	assert.Equal(t, 5, lote.Counts.New)

	revisao := revisarPelaAPI(t, a, lote.ID)
	itens := revisao.Items
	require.Len(t, itens, 7)

	// Critério 3: conta batendo VENCE categoria batendo — e a linha
	// `transferencia_*` não recebe categoria.
	ambas := linhaComDescricao(t, itens, "Pix enviado - Padaria Itau")
	assert.Equal(t, string(dedup.StatusInternalTransfer), ambas.Status)
	assert.Equal(t, importer.ActionSkip, ambas.DefaultAction, "barrada: nada entra sem decisão")
	assert.ElementsMatch(t, []string{importer.ActionTransfer, importer.ActionImport, importer.ActionSkip}, ambas.AllowedActions)
	assert.Equal(t, contaB.ID, texto(ambas.SuggestedCounterpartAccountID))
	assert.Nil(t, ambas.SuggestedCategoryID, "transferência não recebe categoria")
	require.NotNil(t, ambas.MatchScore)
	assert.Equal(t, 100, *ambas.MatchScore, "a pontuação descreve a CONTRAPARTE em transferencia_*")
	assert.Equal(t, "itau", texto(ambas.MatchedKeyword))
	assert.Nil(t, ambas.MatchTransactionID)
	assert.Nil(t, ambas.MatchOccurredOn)

	// Só categoria: sugerida como default, com pontuação e palavra.
	soCategoria := linhaComDescricao(t, itens, "Pix enviado - Padaria Exemplo")
	assert.Equal(t, string(dedup.StatusNew), soCategoria.Status)
	assert.Equal(t, alimentacao.ID, texto(soCategoria.SuggestedCategoryID))
	assert.Equal(t, 100, *soCategoria.MatchScore)
	assert.Equal(t, "padaria", texto(soCategoria.MatchedKeyword))
	assert.Nil(t, soCategoria.SuggestedCounterpartAccountID)

	// Critério 4a: a palavra-chave da PRÓPRIA conta do lote nunca gera
	// transferência.
	propria := linhaComDescricao(t, itens, "Pix enviado - Nubank Proprio")
	assert.Equal(t, string(dedup.StatusNew), propria.Status)
	assert.Nil(t, propria.SuggestedCounterpartAccountID)
	assert.Nil(t, propria.SuggestedCategoryID)

	// 4b: conta arquivada nunca é contraparte.
	arq := linhaComDescricao(t, itens, "Pix enviado - Bradesco Antigo")
	assert.Equal(t, string(dedup.StatusNew), arq.Status)
	assert.Nil(t, arq.SuggestedCounterpartAccountID)

	// 4c: categoria arquivada nunca é sugerida.
	uber := linhaComDescricao(t, itens, "Uber Trip")
	assert.Nil(t, uber.SuggestedCategoryID)
	assert.Nil(t, uber.MatchScore)
	assert.Nil(t, uber.MatchedKeyword)

	// 4d: `income` nunca recebe categoria `expense`.
	receita := linhaComDescricao(t, itens, "Pix recebido - Padaria Exemplo")
	assert.Equal(t, transaction.KindIncome, texto(receita.Kind))
	assert.Nil(t, receita.SuggestedCategoryID)

	// Pagamento de fatura com conta batendo: o status FICA, só ganha a
	// contraparte; a pontuação da contraparte não é exposta (emenda §10.4).
	pagamento := linhaComDescricao(t, itens, "Pagamento de fatura")
	assert.Equal(t, string(dedup.StatusCardPayment), pagamento.Status)
	assert.Equal(t, cartao.ID, texto(pagamento.SuggestedCounterpartAccountID))
	assert.Nil(t, pagamento.MatchScore)
	assert.Nil(t, pagamento.MatchedKeyword)
	assert.Nil(t, pagamento.SuggestedCategoryID)

	// Forma do contrato: as cinco chaves novas SEMPRE presentes (nulas quando
	// não há) e as duas chaves novas de `counts`.
	rec := revisarBrutoPelaAPI(t, a, lote.ID)
	var bruto struct {
		Batch struct {
			Counts map[string]int `json:"counts"`
		} `json:"batch"`
		Items []map[string]json.RawMessage `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec, &bruto))
	assert.Contains(t, bruto.Batch.Counts, "transferencia_interna")
	assert.Contains(t, bruto.Batch.Counts, "transferencia_ja_registrada")
	for _, item := range bruto.Items {
		for _, chave := range []string{"suggestedCategoryId", "matchScore", "matchedKeyword", "suggestedCounterpartAccountId", "matchOccurredOn"} {
			assert.Contains(t, item, chave, "ImportRow.%s é required no contrato", chave)
		}
	}
}

// revisarBrutoPelaAPI devolve o JSON cru de GET /imports/{id}.
func revisarBrutoPelaAPI(t *testing.T, a *ambiente, loteID string) []byte {
	t.Helper()
	r := requisicao(t, http.MethodGet, "/api/v1/imports/"+loteID+"?limit=200", nil,
		identidade(a.casa.ID, a.usuario.ID))
	r.SetPathValue("id", loteID)
	rec := httptest.NewRecorder()
	a.handler(t).Get(rec, r)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec.Body.Bytes()
}

// --- critério 5: fluxo A → B, link e reimportações --------------------------

func TestFluxoAParaBVinculaAPernaEReimportarCaiEmDuplicadoExato(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	contaB := a.contaItau(t)

	// 1) Extrato de A: a transferência para B vira `transferencia_interna`, e
	//    `transfer` SEM counterpartAccountId usa a sugerida.
	extratoA := csvExtratoAPI(
		linhaExtrato{5, "-1500.00", "01", "Transferência enviada pelo Pix - Itau Corrente"},
		linhaExtrato{6, "-11.00", "02", "Transferência enviada pelo Pix - Padaria Exemplo"},
	)
	loteA := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extratoA))
	assert.Equal(t, 1, loteA.Counts.InternalTransfer)

	linhaA := linhaPorStatus(t, revisarPelaAPI(t, a, loteA.ID).Items, dedup.StatusInternalTransfer)
	assert.Equal(t, contaB.ID, texto(linhaA.SuggestedCounterpartAccountID))

	resA := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID,
		fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"transfer"}]}`, linhaA.ID)))
	assert.Equal(t, 1, resA.TransfersCreated)
	assert.Equal(t, 2, resA.Imported)
	assert.Zero(t, resA.Linked)

	pernasA := a.pernasDe(t, contaA.ID, "2026-08")
	require.Len(t, pernasA, 1)
	assert.Equal(t, transaction.KindTransferOut, pernasA[0].Kind)
	pernasB := a.pernasDe(t, contaB.ID, "2026-08")
	require.Len(t, pernasB, 1)
	assert.Equal(t, transaction.KindTransferIn, pernasB[0].Kind)
	assert.Equal(t, *pernasA[0].TransferGroupID, *pernasB[0].TransferGroupID)

	saldosAntes := a.saldos(t)
	assert.Equal(t, int64(-151100), saldosAntes[contaA.ID])
	assert.Equal(t, int64(150000), saldosAntes[contaB.ID])

	// 2) Extrato de B: a linha espelhada fica `transferencia_ja_registrada`,
	//    apontando para a perna de B, com a data dela na prévia.
	extratoB := csvExtratoAPI(
		linhaExtrato{6, "1500.00", "51", "Transferência recebida pelo Pix - Nubank Conta"}, // um dia depois: ±3 cabe
		linhaExtrato{7, "-20.00", "52", "Transferência enviada pelo Pix - Farmacia Exemplo"},
	)
	loteB := loteDaResposta(t, enviarPelaAPI(t, a, contaB.ID, "b.csv", extratoB))
	assert.Equal(t, 1, loteB.Counts.TransferAlreadyRegistered)
	assert.Equal(t, 0, loteB.Counts.InternalTransfer)
	assert.Equal(t, 1, loteB.Counts.New)

	revisaoB := revisarPelaAPI(t, a, loteB.ID)
	linhaB := linhaPorStatus(t, revisaoB.Items, dedup.StatusTransferAlreadyRegistered)
	assert.Equal(t, importer.ActionLink, linhaB.DefaultAction)
	assert.ElementsMatch(t, []string{importer.ActionLink, importer.ActionSkip}, linhaB.AllowedActions)
	assert.NotContains(t, linhaB.AllowedActions, importer.ActionImport, "importar seria a duplicata")
	assert.Equal(t, pernasB[0].ID, texto(linhaB.MatchTransactionID), "a perna é a de B, não a de A")
	assert.Equal(t, contaA.ID, texto(linhaB.SuggestedCounterpartAccountID))
	require.NotNil(t, linhaB.MatchOccurredOn, "matchOccurredOn deixa a tela dizer 'já registrada em 05/08'")
	assert.Equal(t, civil.MustNew(2026, 8, 5), *linhaB.MatchOccurredOn)
	assert.Nil(t, linhaB.SuggestedCategoryID)

	// 3) Confirm com os defaults: `link` grava a chave na perna, nada entra
	//    de novo, e a soma dos saldos não muda.
	resB := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, `{"decisions":[]}`))
	assert.Equal(t, 1, resB.Linked)
	assert.Equal(t, 1, resB.Imported, "só a farmácia entrou")
	assert.Zero(t, resB.TransfersCreated)
	assert.Zero(t, resB.Blocked)
	assert.Zero(t, resB.Skipped)

	saldosDepois := a.saldos(t)
	assert.Equal(t, saldosAntes[contaA.ID], saldosDepois[contaA.ID], "o link não move dinheiro em A")
	assert.Equal(t, saldosAntes[contaB.ID]-2000, saldosDepois[contaB.ID], "em B só a farmácia entrou")
	assert.Equal(t, somaDeSaldos(saldosAntes)-2000, somaDeSaldos(saldosDepois))

	pernasBDepois := a.pernasDe(t, contaB.ID, "2026-08")
	require.Len(t, pernasBDepois, 1, "nenhuma perna nova em B")
	assert.Equal(t, pernasB[0].ID, pernasBDepois[0].ID)
	assert.Equal(t, pernasB[0].AmountCents, pernasBDepois[0].AmountCents)
	assert.Equal(t, pernasB[0].OccurredOn, pernasBDepois[0].OccurredOn)
	require.NotNil(t, pernasBDepois[0].ExternalID)
	assert.Equal(t, texto(linhaB.ExternalID), *pernasBDepois[0].ExternalID, "a perna ganhou o identificador da linha de B")
	require.NotNil(t, pernasBDepois[0].ImportBatchID)
	assert.Equal(t, loteB.ID, *pernasBDepois[0].ImportBatchID)
	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 4, "padaria + par (2) + farmácia — nenhuma perna nova")

	// `linked` sobrevive no lote (outcome) e no confirm idempotente.
	depois := revisarPelaAPI(t, a, loteB.ID)
	require.NotNil(t, depois.Batch.Outcome)
	assert.Equal(t, 1, depois.Batch.Outcome.Linked)
	assert.Equal(t, 1, depois.Batch.Outcome.Imported)
	deNovo := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, `{"decisions":[]}`))
	assert.Equal(t, resB.Linked, deNovo.Linked)
	assert.Equal(t, resB.Imported, deNovo.Imported)
	assert.Equal(t, resB.TransfersCreated, deNovo.TransfersCreated)
	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 4, "o segundo confirm não grava nada")

	// 4) Reimportar B: a linha vinculada cai em `duplicado_exato`.
	reB := loteDaResposta(t, enviarPelaAPI(t, a, contaB.ID, "b-de-novo.csv", extratoB))
	assert.Equal(t, 2, reB.Counts.DuplicateExact)
	assert.Zero(t, reB.Counts.TransferAlreadyRegistered)
	assert.Zero(t, reB.Counts.InternalTransfer)
	vinculada := linhaComDescricao(t, revisarPelaAPI(t, a, reB.ID).Items, "Pix recebido - Nubank Conta")
	assert.Equal(t, string(dedup.StatusDuplicateExact), vinculada.Status)
	assert.Equal(t, pernasB[0].ID, texto(vinculada.MatchTransactionID))

	// 5) Reimportar A: continua `duplicado_exato` em A também — o link de B
	//    não mexeu na perna de A.
	reA := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a-de-novo.csv", extratoA))
	assert.Equal(t, 2, reA.Counts.DuplicateExact)
	assert.Zero(t, reA.Counts.InternalTransfer)
	resReA := resultadoDaResposta(t, confirmarPelaAPI(t, a, reA.ID, `{"decisions":[]}`))
	assert.Zero(t, resReA.Imported)
	assert.Zero(t, resReA.Linked)
	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 4)

	// Auditoria: UMA entrada de vínculo por perna, com id de lançamento — e
	// nunca valor, descrição ou palavra-chave.
	assert.Contains(t, a.auditoria.acoes(), "transaction.import_linked")
	for _, e := range a.auditoria.entradas {
		assert.NotContains(t, e.EntityID+e.Action+e.Entity, "1500")
		assert.NotContains(t, e.EntityID+e.Action+e.Entity, "Nubank Conta")
	}
}

func TestTransferenciaInternaAceitaContraparteExplicitaEImportComum(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	contaB := a.contaItau(t)
	contaC := a.conta(t, a.casa.ID, "Caixa Poupança", account.KindSavings, account.InstitutionOther)
	alimentacao := a.categoriaComPalavras(t, "Alimentação", category.KindExpense, "padaria")

	extrato := csvExtratoAPI(
		linhaExtrato{5, "-100.00", "01", "Transferência enviada pelo Pix - Itau Corrente"},
		linhaExtrato{6, "-200.00", "02", "Transferência enviada pelo Pix - Itau Corrente"},
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extrato))
	itens := revisarPelaAPI(t, a, lote.ID).Items
	require.Len(t, itens, 2)
	primeira, segunda := itens[0], itens[1]
	assert.Equal(t, string(dedup.StatusInternalTransfer), primeira.Status)
	assert.Equal(t, string(dedup.StatusInternalTransfer), segunda.Status)

	// A contraparte da DECISÃO vence a sugerida; `import` entra como despesa
	// comum, com categoria.
	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(
		`{"decisions":[
			{"rowId":%q,"action":"transfer","counterpartAccountId":%q},
			{"rowId":%q,"action":"import","categoryId":%q}
		]}`, primeira.ID, contaC.ID, segunda.ID, alimentacao.ID)))
	assert.Equal(t, 1, res.TransfersCreated)
	assert.Equal(t, 2, res.Imported)

	require.Len(t, a.pernasDe(t, contaC.ID, "2026-08"), 1, "o par foi para C, a contraparte informada")
	assert.Empty(t, a.pernasDe(t, contaB.ID, "2026-08"), "a sugerida (B) não recebeu nada")

	var comum *transaction.Transaction
	for _, l := range a.lancamentosDa(t, a.casa.ID, "2026-08") {
		if l.Kind == transaction.KindExpense {
			c := l
			comum = &c
		}
	}
	require.NotNil(t, comum)
	assert.Equal(t, int64(20000), comum.AmountCents)
	assert.Equal(t, alimentacao.ID, texto(comum.CategoryID))
}

// --- emenda §10.2 (#9): pagamento de fatura também acha a perna --------------

func TestPagamentoDeFaturaComContraparteSugeridaAchaAPernaJaRegistrada(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	cartao := a.contaCartao(t)
	a.palavrasNaConta(t, cartao.ID, "fatura")

	// 1) A fatura entra primeiro, e "Pagamento recebido" vira o par
	//    (transfer_out na corrente, transfer_in no cartão) — contraparte
	//    explícita, porque a fatura não cita a corrente.
	fatura := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "fatura.csv", fixtureFatura(t)))
	recebido := linhaComDescricao(t, revisarPelaAPI(t, a, fatura.ID).Items, "Pagamento recebido")
	require.Equal(t, string(dedup.StatusCardPayment), recebido.Status)
	assert.Nil(t, recebido.SuggestedCounterpartAccountID, "a corrente não tem palavra-chave")

	resFatura := resultadoDaResposta(t, confirmarPelaAPI(t, a, fatura.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}],%s}`,
		recebido.ID, conta.ID, confirmacaoDeFatura)))
	assert.Equal(t, 1, resFatura.TransfersCreated)
	pernaNaCorrente := a.pernasDe(t, conta.ID, "2026-08")
	require.Len(t, pernaNaCorrente, 1)
	assert.Equal(t, transaction.KindTransferOut, pernaNaCorrente[0].Kind)

	// 2) O extrato da corrente traz "Pagamento de fatura" (mesmo valor, mesmo
	//    dia): a palavra-chave do cartão sugere a contraparte, e o pareamento
	//    acha a perna já registrada — em vez de propor um SEGUNDO par.
	extrato := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "extrato.csv", fixtureExtrato(t)))
	assert.Equal(t, 1, extrato.Counts.TransferAlreadyRegistered)
	assert.Zero(t, extrato.Counts.CardPayment, "o pagamento de fatura virou transferencia_ja_registrada")

	pagamento := linhaComDescricao(t, revisarPelaAPI(t, a, extrato.ID).Items, "Pagamento de fatura")
	assert.Equal(t, string(dedup.StatusTransferAlreadyRegistered), pagamento.Status)
	assert.Equal(t, pernaNaCorrente[0].ID, texto(pagamento.MatchTransactionID))
	assert.Equal(t, cartao.ID, texto(pagamento.SuggestedCounterpartAccountID))
	assert.Equal(t, importer.ActionLink, pagamento.DefaultAction)

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, extrato.ID, `{"decisions":[]}`))
	assert.Equal(t, 1, res.Linked)
	assert.Equal(t, 12, res.Imported)
	assert.Zero(t, res.TransfersCreated, "nenhum segundo par")
	require.Len(t, a.pernasDe(t, conta.ID, "2026-08"), 1)

	// 3) Reimportar o extrato: o pagamento agora é `duplicado_exato`.
	reimportado := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "extrato2.csv", fixtureExtrato(t)))
	assert.Equal(t, 13, reimportado.Counts.DuplicateExact)
	assert.Zero(t, reimportado.Counts.TransferAlreadyRegistered)
	assert.Zero(t, reimportado.Counts.CardPayment)
}

// --- critério 6: duas iguais no mesmo dia -----------------------------------

func TestDuasTransferenciasIguaisNoMesmoDiaCasamComPernasDistintasEATerceiraFica(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	contaB := a.contaItau(t)

	// Duas transferências iguais de A para B no mesmo dia, registradas como
	// dois pares.
	extratoA := csvExtratoAPI(
		linhaExtrato{5, "-100.00", "01", "Transferência enviada pelo Pix - Itau Corrente"},
		linhaExtrato{5, "-100.00", "02", "Transferência enviada pelo Pix - Itau Corrente"},
	)
	loteA := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extratoA))
	itensA := revisarPelaAPI(t, a, loteA.ID).Items
	require.Len(t, itensA, 2)
	resA := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"transfer"},{"rowId":%q,"action":"transfer"}]}`,
		itensA[0].ID, itensA[1].ID)))
	require.Equal(t, 2, resA.TransfersCreated)
	pernasB := a.pernasDe(t, contaB.ID, "2026-08")
	require.Len(t, pernasB, 2)

	// O extrato de B traz TRÊS linhas iguais no mesmo dia.
	extratoB := csvExtratoAPI(
		linhaExtrato{5, "100.00", "51", "Transferência recebida pelo Pix - Nubank Conta"},
		linhaExtrato{5, "100.00", "52", "Transferência recebida pelo Pix - Nubank Conta"},
		linhaExtrato{5, "100.00", "53", "Transferência recebida pelo Pix - Nubank Conta"},
	)
	loteB := loteDaResposta(t, enviarPelaAPI(t, a, contaB.ID, "b.csv", extratoB))
	assert.Equal(t, 2, loteB.Counts.TransferAlreadyRegistered)
	assert.Equal(t, 1, loteB.Counts.InternalTransfer)

	itensB := revisarPelaAPI(t, a, loteB.ID).Items
	require.Len(t, itensB, 3)
	apontadas := map[string]int{}
	for _, l := range itensB[:2] {
		assert.Equal(t, string(dedup.StatusTransferAlreadyRegistered), l.Status)
		apontadas[texto(l.MatchTransactionID)]++
	}
	assert.Len(t, apontadas, 2, "cada linha casa com UMA perna distinta")
	for _, p := range pernasB {
		assert.Equal(t, 1, apontadas[p.ID])
	}
	assert.Equal(t, string(dedup.StatusInternalTransfer), itensB[2].Status, "a terceira não tem perna sobrando")
	assert.Nil(t, itensB[2].MatchTransactionID)

	// Confirm com os defaults: duas vinculadas, a terceira barrada (skip).
	resB := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, `{"decisions":[]}`))
	assert.Equal(t, 2, resB.Linked)
	assert.Zero(t, resB.Imported)
	assert.Equal(t, 1, resB.Skipped)
	require.Len(t, a.pernasDe(t, contaB.ID, "2026-08"), 2, "nenhuma perna nova")
}

// --- critério 7: categoryId tri-estado --------------------------------------

func TestCategoryIdTriEstadoNoConfirm(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	alimentacao := a.categoriaComPalavras(t, "Alimentação", category.KindExpense, "padaria")
	outros := a.categoriaComPalavras(t, "Outros", category.KindExpense)

	extrato := csvExtratoAPI(
		linhaExtrato{5, "-11.00", "01", "Transferência enviada pelo Pix - Padaria Exemplo"}, // ausente → sugerida
		linhaExtrato{6, "-12.00", "02", "Transferência enviada pelo Pix - Padaria Exemplo"}, // null → sem categoria
		linhaExtrato{7, "-13.00", "03", "Transferência enviada pelo Pix - Padaria Exemplo"}, // valor → Outros
		linhaExtrato{8, "-14.00", "04", "Transferência enviada pelo Pix - Fulano"},          // sem sugestão → default do corpo
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv", extrato))
	itens := revisarPelaAPI(t, a, lote.ID).Items
	require.Len(t, itens, 4)
	for _, l := range itens[:3] {
		assert.Equal(t, alimentacao.ID, texto(l.SuggestedCategoryID))
	}
	assert.Nil(t, itens[3].SuggestedCategoryID)

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(
		`{"decisions":[
			{"rowId":%q,"action":"import","categoryId":null},
			{"rowId":%q,"action":"import","categoryId":%q}
		],"defaultCategoryId":%q}`, itens[1].ID, itens[2].ID, outros.ID, outros.ID)))
	assert.Equal(t, 4, res.Imported)

	porValor := map[int64]*string{}
	for _, l := range a.lancamentosDa(t, a.casa.ID, "2026-08") {
		porValor[l.AmountCents] = l.CategoryID
	}
	assert.Equal(t, alimentacao.ID, texto(porValor[1100]), "decisão ausente grava a sugerida")
	assert.Nil(t, porValor[1200], "null explícito grava SEM categoria apesar da sugestão E do defaultCategoryId")
	assert.Equal(t, outros.ID, texto(porValor[1300]), "valor na decisão vence a sugestão")
	assert.Equal(t, outros.ID, texto(porValor[1400]), "sem sugestão, o defaultCategoryId vale")
}

func TestCategoriaDeOutraCasaNaDecisaoResponde404ENadaEGravado(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	a.categoriaComPalavras(t, "Alimentação", category.KindExpense, "padaria")

	alheia, err := a.categoriaSvc.Create(t.Context(),
		category.Actor{HouseholdID: a.alheia.ID, UserID: a.outroUsuario.ID},
		category.CreateInput{Name: "Alheia", Kind: category.KindExpense})
	require.NoError(t, err)

	extrato := csvExtratoAPI(
		linhaExtrato{5, "-11.00", "01", "Transferência enviada pelo Pix - Padaria Exemplo"},
		linhaExtrato{6, "-12.00", "02", "Transferência enviada pelo Pix - Fulano"},
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "a.csv", extrato))
	itens := revisarPelaAPI(t, a, lote.ID).Items

	rec := confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"import","categoryId":%q}]}`, itens[1].ID, alheia.ID))
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())

	// Tudo ou nada: a linha com a sugestão legítima também não entrou.
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, lote.ID).Batch.Status)

	// Inexistente responde byte a byte igual.
	recInexistente := confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"import","categoryId":%q}]}`, itens[1].ID, a.proximoID("cat-inexistente")))
	assert.Equal(t, rec.Code, recInexistente.Code)
	assert.Equal(t, rec.Body.String(), recInexistente.Body.String())
}

// --- critério 8 e §9: abusos ------------------------------------------------

// cenarioComPernaRegistrada monta A→B e devolve o lote de B com a linha
// `transferencia_ja_registrada` ainda pendente.
func cenarioComPernaRegistrada(t *testing.T, a *ambiente) (contaA, contaB *account.Account, loteB importer.BatchView, linhaB importer.RowView, pernaB transaction.Transaction) {
	t.Helper()
	contaA = a.contaNubankComPalavra(t)
	contaB = a.contaItau(t)

	extratoA := csvExtratoAPI(linhaExtrato{5, "-1500.00", "01", "Transferência enviada pelo Pix - Itau Corrente"})
	loteA := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extratoA))
	linhaA := revisarPelaAPI(t, a, loteA.ID).Items[0]
	resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID,
		fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"transfer"}]}`, linhaA.ID)))
	pernas := a.pernasDe(t, contaB.ID, "2026-08")
	require.Len(t, pernas, 1)
	pernaB = pernas[0]

	extratoB := csvExtratoAPI(
		linhaExtrato{5, "1500.00", "51", "Transferência recebida pelo Pix - Nubank Conta"},
		linhaExtrato{6, "-20.00", "52", "Transferência enviada pelo Pix - Farmacia Exemplo"},
	)
	loteB = loteDaResposta(t, enviarPelaAPI(t, a, contaB.ID, "b.csv", extratoB))
	linhaB = linhaPorStatus(t, revisarPelaAPI(t, a, loteB.ID).Items, dedup.StatusTransferAlreadyRegistered)
	return contaA, contaB, loteB, linhaB, pernaB
}

func TestAbusosDoLinkEDoTransferRespondem400Ou404(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA, contaB, loteB, linhaB, _ := cenarioComPernaRegistrada(t, a)
	nova := linhaPorStatus(t, revisarPelaAPI(t, a, loteB.ID).Items, dedup.StatusNew)
	alheia := a.conta(t, a.alheia.ID, "Conta Alheia", account.KindChecking, account.InstitutionOther)

	casos := map[string]struct {
		corpo  string
		status int
		campo  string
	}{
		"link em linha novo": {
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"link"}]}`, nova.ID),
			http.StatusBadRequest, "decisions",
		},
		"import em transferencia_ja_registrada": {
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"import"}]}`, linhaB.ID),
			http.StatusBadRequest, "decisions",
		},
		"transfer em transferencia_ja_registrada": {
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}]}`, linhaB.ID, contaA.ID),
			http.StatusBadRequest, "decisions",
		},
		"matchTransactionId no corpo": {
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"link","matchTransactionId":"forjado"}]}`, linhaB.ID),
			http.StatusBadRequest, "",
		},
		"link com categoryId": {
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"link","categoryId":"qualquer"}]}`, linhaB.ID),
			http.StatusBadRequest, "categoryId",
		},
		"link com categoryId nulo": {
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"link","categoryId":null}]}`, linhaB.ID),
			http.StatusBadRequest, "categoryId",
		},
		"link com counterpartAccountId": {
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"link","counterpartAccountId":%q}]}`, linhaB.ID, contaA.ID),
			http.StatusBadRequest, "counterpartAccountId",
		},
		"link com statementId": {
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"link","statementId":"qualquer"}]}`, linhaB.ID),
			http.StatusBadRequest, "statementId",
		},
		"transfer com categoryId nulo": {
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q,"categoryId":null}]}`, nova.ID, contaA.ID),
			http.StatusBadRequest, "categoryId",
		},
		"campo desconhecido na raiz": {
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"skip"}],"linked":1}`, nova.ID),
			http.StatusBadRequest, "",
		},
		"skip com categoryId nulo": {
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"skip","categoryId":null}]}`, nova.ID),
			http.StatusBadRequest, "categoryId",
		},
		"acao inventada": {
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"vincular"}]}`, linhaB.ID),
			http.StatusBadRequest, "decisions",
		},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			rec := confirmarPelaAPI(t, a, loteB.ID, c.corpo)
			assert.Equal(t, c.status, rec.Code, rec.Body.String())
			if c.campo != "" {
				assert.Contains(t, rec.Body.String(), `"`+c.campo+`"`)
			}
		})
	}

	// Depois de todas as tentativas: nada entrou, a perna continua sem a
	// chave da linha e o lote segue pendente.
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, loteB.ID).Batch.Status)
	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 2, "só o par de A")
	perna := a.pernasDe(t, contaB.ID, "2026-08")[0]
	assert.Nil(t, perna.ExternalID)
	_ = alheia
}

func TestTransferEmTransferenciaInternaComContraparteAlheiaResponde404ENadaEGravado(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	a.contaItau(t)
	alheia := a.conta(t, a.alheia.ID, "Conta Alheia", account.KindChecking, account.InstitutionOther)

	extrato := csvExtratoAPI(
		linhaExtrato{5, "-1500.00", "01", "Transferência enviada pelo Pix - Itau Corrente"},
		linhaExtrato{6, "-11.00", "02", "Transferência enviada pelo Pix - Padaria Exemplo"},
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extrato))
	linha := linhaPorStatus(t, revisarPelaAPI(t, a, lote.ID).Items, dedup.StatusInternalTransfer)

	// Contraparte REAL, só que da outra casa: 404 idêntico ao inexistente e
	// nada gravado — nem a padaria, que entraria por default.
	rec := confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}]}`, linha.ID, alheia.ID))
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
	assert.Empty(t, a.lancamentosDa(t, a.alheia.ID, "2026-08"))
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, lote.ID).Batch.Status)

	recInexistente := confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}]}`, linha.ID, a.proximoID("conta-inexistente")))
	assert.Equal(t, rec.Body.String(), recInexistente.Body.String())

	// Contraparte igual à conta do lote é 400 com o campo.
	recMesma := confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}]}`, linha.ID, contaA.ID))
	assert.Equal(t, http.StatusBadRequest, recMesma.Code, recMesma.Body.String())
	assert.Contains(t, recMesma.Body.String(), `"counterpartAccountId"`)
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
}

func TestTransferSemContraparteESemSugestaoResponde400(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	// Sem palavra-chave em conta nenhuma: o pagamento de fatura não tem
	// contraparte sugerida, e `transfer` sem counterpartAccountId é 400.
	lote := a.enviarExtrato(t, conta.ID)
	pagamento := linhaComDescricao(t, a.linhasDoLote(t, lote.ID), "Pagamento de fatura")
	assert.Nil(t, pagamento.SuggestedCounterpartAccountID)

	rec := confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"transfer"}]}`, pagamento.ID))
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"counterpartAccountId"`)
}

func TestPernaExcluidaEntreAAnaliseEOConfirmBloqueiaSoALinha(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	_, contaB, loteB, linhaB, pernaB := cenarioComPernaRegistrada(t, a)

	// A pessoa apaga a transferência depois da prévia e antes do confirm.
	require.NoError(t, a.txSvc.SoftDelete(t.Context(),
		transaction.Actor{HouseholdID: a.casa.ID, UserID: a.usuario.ID, IP: "203.0.113.7"}, pernaB.ID))
	antes := len(a.lancamentosDa(t, a.casa.ID, "2026-08"))

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, `{"decisions":[]}`))
	assert.Equal(t, importer.BatchStatusCommitted, res.Status, "o lote SEGUE")
	assert.Zero(t, res.Linked)
	assert.Equal(t, 1, res.Blocked)
	require.Len(t, res.BlockedRows, 1)
	assert.Equal(t, linhaB.ID, res.BlockedRows[0].RowID)
	assert.Equal(t, string(dedup.StatusTransferAlreadyRegistered), res.BlockedRows[0].Reason)
	assert.Equal(t, 1, res.Imported, "a farmácia entrou")

	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), antes+1)
	assert.Empty(t, a.pernasDe(t, contaB.ID, "2026-08"), "a perna excluída NÃO foi ressuscitada pelo link")

	// O lote guarda linked = 0 e blocked = 1.
	depois := revisarPelaAPI(t, a, loteB.ID)
	require.NotNil(t, depois.Batch.Outcome)
	assert.Zero(t, depois.Batch.Outcome.Linked)
	assert.Equal(t, 1, depois.Batch.Outcome.Blocked)
}

func TestPernaDeOutraCasaNoStagingDerrubaOLoteInteiroSemNadaParcial(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	_, contaB, loteB, linhaB, _ := cenarioComPernaRegistrada(t, a)

	// Banco adulterado: o staging passa a apontar para uma perna de OUTRA
	// casa (real). O corpo não tem esse campo; este é o único jeito de
	// forjar a perna — e o commit reconfere por casa e conta.
	alheiaConta := a.conta(t, a.alheia.ID, "Alheia", account.KindChecking, account.InstitutionOther)
	alheiaDestino := a.conta(t, a.alheia.ID, "Alheia 2", account.KindChecking, account.InstitutionOther)
	grupo := a.proximoID("00000000-0000-7000-f000")
	_, err := a.txSvc.CreateBatch(t.Context(),
		transaction.Actor{HouseholdID: a.alheia.ID, UserID: a.outroUsuario.ID},
		transaction.CreateBatchInput{Source: transaction.SourceManual, Rows: []transaction.NewTransaction{
			{Kind: transaction.KindTransferOut, AccountID: alheiaConta.ID, AmountCents: 150000, Description: "alheia",
				OccurredOn: civil.MustNew(2026, 8, 5), TransferGroupID: &grupo, DedupKey: dedup.PairKey(grupo + "-a")},
			{Kind: transaction.KindTransferIn, AccountID: alheiaDestino.ID, AmountCents: 150000, Description: "alheia",
				OccurredOn: civil.MustNew(2026, 8, 5), TransferGroupID: &grupo, DedupKey: dedup.PairKey(grupo + "-b")},
		}})
	require.NoError(t, err)
	pernasAlheias := a.lancamentosDa(t, a.alheia.ID, "2026-08")
	require.Len(t, pernasAlheias, 2)
	pernaAlheia := pernasAlheias[0]

	adulterado := a.db.Gorm().WithContext(t.Context()).
		Table("import_rows").
		Where("id = ? AND household_id = ?", linhaB.ID, a.casa.ID).
		Update("match_transaction_id", pernaAlheia.ID)
	require.NoError(t, adulterado.Error)
	require.EqualValues(t, 1, adulterado.RowsAffected)

	var log bytes.Buffer
	handler := importer.NewHandler(a.svc, slog.New(slog.NewJSONHandler(&log, nil)), 0)
	r := requisicao(t, http.MethodPost, "/api/v1/imports/"+loteB.ID+"/confirm",
		strings.NewReader(`{"decisions":[]}`), identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", loteB.ID)
	rec := httptest.NewRecorder()
	handler.Confirm(rec, r)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "INTERNAL_ERROR")

	// Nada parcial: a farmácia (que entraria por default) NÃO entrou, o lote
	// continua pendente, e a perna alheia continua intocada.
	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 2, "só o par de A")
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, loteB.ID).Batch.Status)
	for _, p := range a.lancamentosDa(t, a.alheia.ID, "2026-08") {
		assert.Nil(t, p.ExternalID)
		assert.Nil(t, p.ImportBatchID)
	}
	_ = contaB

	// O log tem os ids (para a forense) e NUNCA descrição, valor ou palavra.
	registro := log.String()
	assert.Contains(t, registro, linhaB.ID)
	assert.Contains(t, registro, pernaAlheia.ID)
	assert.NotContains(t, registro, "Nubank Conta")
	assert.NotContains(t, registro, "Pix recebido")
	assert.NotContains(t, registro, "1500")
	assert.NotContains(t, registro, "nubank")
	assert.NotContains(t, registro, "itau")
	assert.NotContains(t, rec.Body.String(), pernaAlheia.ID, "a resposta não confirma a existência da perna alheia")
}

func TestLogDoCaminhoFelizNaoCarregaDescricaoValorNemPalavra(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	a.contaItau(t)
	a.categoriaComPalavras(t, "Alimentação", category.KindExpense, "padaria")

	var log bytes.Buffer
	handler := importer.NewHandler(a.svc, slog.New(slog.NewJSONHandler(&log, nil)), 0)

	extrato := csvExtratoAPI(
		linhaExtrato{5, "-1500.00", "01", "Transferência enviada pelo Pix - Itau Corrente"},
		linhaExtrato{6, "-11.00", "02", "Transferência enviada pelo Pix - Padaria Exemplo"},
	)
	corpo, tipo := montarMultipart(t,
		parte{nome: importer.PartFile, arquivo: "a.csv", conteudo: extrato},
		parte{nome: importer.PartAccountID, conteudo: []byte(contaA.ID)},
	)
	r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", tipo)
	rec := httptest.NewRecorder()
	handler.Create(rec, r)
	lote := loteDaResposta(t, rec)

	linha := linhaPorStatus(t, revisarPelaAPI(t, a, lote.ID).Items, dedup.StatusInternalTransfer)
	rc := requisicao(t, http.MethodPost, "/api/v1/imports/"+lote.ID+"/confirm",
		strings.NewReader(fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"transfer"}]}`, linha.ID)),
		identidade(a.casa.ID, a.usuario.ID))
	rc.Header.Set("Content-Type", "application/json")
	rc.SetPathValue("id", lote.ID)
	recC := httptest.NewRecorder()
	handler.Confirm(recC, rc)
	res := resultadoDaResposta(t, recC)
	assert.Equal(t, 1, res.TransfersCreated)

	for _, proibido := range []string{"Itau Corrente", "Padaria Exemplo", "1500", "150000", "1100", "itau", "padaria"} {
		assert.NotContains(t, log.String(), proibido)
	}
}
