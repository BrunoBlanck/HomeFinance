package importer_test

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QA da E2c — BORDAS que os exemplos dos devs não percorrem: o mesmo arquivo
// enviado duas vezes antes de confirmar (dois lotes pendentes apontando para a
// MESMA perna), a contraparte sugerida arquivada entre a análise e o confirm,
// `defaultCategoryId` junto de `link`, e o pareamento ±3 dias atravessando a
// virada de mês e de ano (competências diferentes nas duas pernas).

// csvExtratoDatado é csvExtratoAPI com a data completa por linha — para as
// viradas de mês e de ano.
type linhaDatada struct {
	data      string // dd/mm/aaaa
	valor     string
	idSufixo  string
	descricao string
}

func csvExtratoDatado(linhas ...linhaDatada) []byte {
	var b bytes.Buffer
	b.WriteString("Data,Valor,Identificador,Descrição\n")
	for _, l := range linhas {
		fmt.Fprintf(&b, "%s,%s,11111111-1111-4111-8111-1111111111%s,%s\n", l.data, l.valor, l.idSufixo, l.descricao)
	}
	return b.Bytes()
}

func TestMesmoArquivoEnviadoDuasVezesAntesDeConfirmarNaoVinculaDuasVezesNemDa500(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	_, contaB, loteB1, linhaB1, pernaB := cenarioComPernaRegistrada(t, a)

	// O mesmo extrato de B, enviado de novo ANTES de confirmar o primeiro:
	// o segundo lote também aponta para a mesma perna (a análise olha o
	// ledger, e nele a perna ainda não tem a chave).
	extratoB := csvExtratoAPI(
		linhaExtrato{5, "1500.00", "51", "Transferência recebida pelo Pix - Nubank Conta"},
		linhaExtrato{6, "-20.00", "52", "Transferência enviada pelo Pix - Farmacia Exemplo"},
	)
	loteB2 := loteDaResposta(t, enviarPelaAPI(t, a, contaB.ID, "b-de-novo.csv", extratoB))
	linhaB2 := linhaPorStatus(t, revisarPelaAPI(t, a, loteB2.ID).Items, dedup.StatusTransferAlreadyRegistered)
	assert.Equal(t, pernaB.ID, texto(linhaB2.MatchTransactionID), "os dois lotes pendentes apontam para a MESMA perna")
	assert.Equal(t, texto(linhaB1.MatchTransactionID), texto(linhaB2.MatchTransactionID))

	// Confirma o primeiro: vincula.
	res1 := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB1.ID, `{"decisions":[]}`))
	assert.Equal(t, 1, res1.Linked)
	assert.Equal(t, 1, res1.Imported)
	saldos := a.saldos(t)
	antes := a.lancamentosDa(t, a.casa.ID, "2026-08")

	// Confirma o segundo: a perna JÁ tem esta chave. Nunca 500, nunca um
	// segundo vínculo, nunca uma segunda farmácia; o recálculo no confirm vê
	// as duplicatas e o lote fecha sem gravar nada.
	rec := confirmarPelaAPI(t, a, loteB2.ID, `{"decisions":[]}`)
	require.NotEqual(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	res2 := resultadoDaResposta(t, rec)
	t.Logf("segundo confirm: status=%s imported=%d linked=%d skipped=%d blocked=%d blockedRows=%+v",
		res2.Status, res2.Imported, res2.Linked, res2.Skipped, res2.Blocked, res2.BlockedRows)
	assert.Zero(t, res2.Linked, "a perna não pode ser vinculada duas vezes")
	assert.Zero(t, res2.Imported, "a farmácia já entrou pelo primeiro lote")
	assert.Zero(t, res2.TransfersCreated)
	assert.Equal(t, saldos, a.saldos(t), "nada moveu")
	assert.Equal(t, antes, a.lancamentosDa(t, a.casa.ID, "2026-08"))

	pernaDepois := a.pernasDe(t, contaB.ID, "2026-08")
	require.Len(t, pernaDepois, 1)
	assert.Equal(t, loteB1.ID, texto(pernaDepois[0].ImportBatchID), "a perna continua vinculada ao PRIMEIRO lote")
	assert.Equal(t, texto(linhaB1.ExternalID), texto(pernaDepois[0].ExternalID))
}

func TestContraparteSugeridaArquivadaEntreAAnaliseEOConfirmNaoGravaNadaParcial(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	contaB := a.contaItau(t)

	extrato := csvExtratoAPI(
		linhaExtrato{5, "-1500.00", "01", "Transferência enviada pelo Pix - Itau Corrente"},
		linhaExtrato{6, "-11.00", "02", "Transferência enviada pelo Pix - Padaria Exemplo"},
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extrato))
	linha := linhaPorStatus(t, revisarPelaAPI(t, a, lote.ID).Items, dedup.StatusInternalTransfer)
	require.Equal(t, contaB.ID, texto(linha.SuggestedCounterpartAccountID))

	// B é arquivada depois da prévia.
	_, err := a.contaSvc.Archive(t.Context(), a.atorDeConta(), contaB.ID)
	require.NoError(t, err)

	// `transfer` sem contraparte explícita usa a sugerida — que agora está
	// arquivada: recusa (não 500), e NADA entra — nem a padaria.
	rec := confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"transfer"}]}`, linha.ID))
	t.Logf("confirm com sugerida arquivada: %d %s", rec.Code, rec.Body.String())
	require.NotEqual(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	// Spec 0005 §12: mesmo quando a contraparte veio da SUGESTÃO (e não do
	// corpo), o campo é `counterpartAccountId` — é ele que a tela abre para
	// a pessoa escolher outra conta.
	campos := camposDoValidationFailed(t, rec)
	assert.Contains(t, campos, "counterpartAccountId")
	assert.NotContains(t, campos, "accountId")
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), "tudo ou nada")
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, lote.ID).Batch.Status)

	// A pessoa ainda tem saída: `import` comum ou `skip` na linha.
	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"skip"}]}`, linha.ID)))
	assert.Equal(t, 1, res.Imported)
	assert.Equal(t, 1, res.Skipped)
}

func TestLinkIgnoraDefaultCategoryIdENaoGravaCategoriaNaPerna(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	_, contaB, loteB, linhaB, pernaB := cenarioComPernaRegistrada(t, a)
	outros := a.categoriaComPalavras(t, "Outros", category.KindExpense)

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, fmt.Sprintf(`{"decisions":[],"defaultCategoryId":%q}`, outros.ID)))
	assert.Equal(t, 1, res.Linked)
	assert.Equal(t, 1, res.Imported)

	perna := a.pernasDe(t, contaB.ID, "2026-08")[0]
	assert.Equal(t, pernaB.ID, perna.ID)
	assert.Nil(t, perna.CategoryID, "transferência nunca ganha categoria — nem pelo defaultCategoryId do link")
	assert.Equal(t, texto(linhaB.ExternalID), texto(perna.ExternalID))
	for _, l := range a.lancamentosDa(t, a.casa.ID, "2026-08") {
		if l.Kind == transaction.KindExpense {
			assert.Equal(t, outros.ID, texto(l.CategoryID), "a farmácia (import) recebeu o default")
		}
	}
}

// Virada de MÊS: A transfere em 31/08, B recebe em 02/09. As duas pernas
// nascem com a competência da linha de A (agosto); o extrato de B, de
// setembro, acha a perna a 2 dias e vincula — sem criar um segundo par em
// setembro. Depois, GET /transfers mostra o par UMA vez, em agosto.
func TestPareamentoAtravessaAViradaDeMes(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	contaB := a.contaItau(t)

	loteA := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", csvExtratoDatado(
		linhaDatada{"31/08/2026", "-1500.00", "01", "Transferência enviada pelo Pix - Itau Corrente"},
	)))
	linhaA := linhaPorStatus(t, revisarPelaAPI(t, a, loteA.ID).Items, dedup.StatusInternalTransfer)
	resA := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"transfer"}]}`, linhaA.ID)))
	require.Equal(t, 1, resA.TransfersCreated)

	loteB := loteDaResposta(t, enviarPelaAPI(t, a, contaB.ID, "b.csv", csvExtratoDatado(
		linhaDatada{"02/09/2026", "1500.00", "51", "Transferência recebida pelo Pix - Nubank Conta"},
	)))
	assert.Equal(t, 1, loteB.Counts.TransferAlreadyRegistered, "2 dias de distância, mês diferente: ainda casa")
	linhaB := linhaPorStatus(t, revisarPelaAPI(t, a, loteB.ID).Items, dedup.StatusTransferAlreadyRegistered)
	require.NotNil(t, linhaB.MatchOccurredOn)
	assert.Equal(t, civil.MustNew(2026, 8, 31), *linhaB.MatchOccurredOn)

	saldos := a.saldos(t)
	resB := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, `{"decisions":[]}`))
	assert.Equal(t, 1, resB.Linked)
	assert.Equal(t, saldos, a.saldos(t))

	agosto := transfersDecodificadas(t, transfersPelaAPI(t, a, a.casa.ID, a.usuario.ID, "month=2026-08"))
	require.Len(t, agosto.Items, 1)
	assert.Equal(t, civil.MustNew(2026, 8, 31), agosto.Items[0].OccurredOn)
	setembro := transfersDecodificadas(t, transfersPelaAPI(t, a, a.casa.ID, a.usuario.ID, "month=2026-09"))
	assert.Empty(t, setembro.Items, "o par é de agosto; setembro não o repete")
	assert.Empty(t, setembro.Pairs)

	// Reimportar B em setembro: duplicado_exato, mesmo com a perna em agosto.
	reB := loteDaResposta(t, enviarPelaAPI(t, a, contaB.ID, "b2.csv", csvExtratoDatado(
		linhaDatada{"02/09/2026", "1500.00", "51", "Transferência recebida pelo Pix - Nubank Conta"},
	)))
	assert.Equal(t, 1, reB.Counts.DuplicateExact)
}

// Virada de ANO: 30/12 → 02/01 do ano seguinte, a 3 dias (o limite da
// janela). E 29/12 → 02/01 (4 dias) NÃO casa: vira transferencia_interna.
func TestPareamentoAtravessaAViradaDeAnoNoLimiteDaJanela(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	contaB := a.contaItau(t)

	loteA := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", csvExtratoDatado(
		linhaDatada{"30/12/2025", "-100.00", "01", "Transferência enviada pelo Pix - Itau Corrente"},
		linhaDatada{"29/12/2025", "-200.00", "02", "Transferência enviada pelo Pix - Itau Corrente"},
	)))
	itens := revisarPelaAPI(t, a, loteA.ID).Items
	require.Len(t, itens, 2)
	resA := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"transfer"},{"rowId":%q,"action":"transfer"}]}`, itens[0].ID, itens[1].ID)))
	require.Equal(t, 2, resA.TransfersCreated)

	loteB := loteDaResposta(t, enviarPelaAPI(t, a, contaB.ID, "b.csv", csvExtratoDatado(
		linhaDatada{"02/01/2026", "100.00", "51", "Transferência recebida pelo Pix - Nubank Conta"}, // 3 dias: casa
		linhaDatada{"02/01/2026", "200.00", "52", "Transferência recebida pelo Pix - Nubank Conta"}, // 4 dias: não casa
	)))
	assert.Equal(t, 1, loteB.Counts.TransferAlreadyRegistered)
	assert.Equal(t, 1, loteB.Counts.InternalTransfer)
	itensB := revisarPelaAPI(t, a, loteB.ID).Items
	cem := linhaComValor(t, itensB, 100_00)
	duzentos := linhaComValor(t, itensB, 200_00)
	assert.Equal(t, string(dedup.StatusTransferAlreadyRegistered), cem.Status)
	assert.Equal(t, civil.MustNew(2025, 12, 30), *cem.MatchOccurredOn)
	assert.Equal(t, string(dedup.StatusInternalTransfer), duzentos.Status, "4 dias é fora da janela de ±3")
	assert.Nil(t, duzentos.MatchTransactionID)
	assert.Equal(t, contaA.ID, texto(duzentos.SuggestedCounterpartAccountID))
}

func linhaComValor(t *testing.T, itens []importer.RowView, cents int64) importer.RowView {
	t.Helper()
	for _, l := range itens {
		if l.AmountCents != nil && *l.AmountCents == cents {
			return l
		}
	}
	t.Fatalf("nenhuma linha com valor %d", cents)
	return importer.RowView{}
}
