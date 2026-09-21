package transaction_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Lacunas do QA sobre POST /transfers/detect (spec 0005 §13.5): o critério
// 7(a) — que o dev cobriu só na alínea (b) —, o teto de 500 da lista com
// contagem completa medido pelo SERVIÇO (e não só pelo pareamento puro), e as
// bordas de corpo e de isolamento que faltavam.

// Critério 7(a): a candidata some ENTRE a prévia e a confirmação. O recálculo
// dentro da transação a ignora, `paired` diminui e NÃO há erro — o 409 é só
// para a linha que muda entre a leitura e o UPDATE da MESMA execução (7b).
func TestQADetectTransfersCandidataSumidaEntrePreviaEConfirmacaoNaoEErro(t *testing.T) {
	t.Parallel()

	sabotagens := map[string]func(amb *ambiente, id string){
		"excluída por outra requisição": func(amb *ambiente, id string) {
			l := amb.repo.linhas[id]
			l.DeletedAt = ptr(agora)
			amb.repo.linhas[id] = l
		},
		"já convertida por outra execução": func(amb *ambiente, id string) {
			l := amb.repo.linhas[id]
			l.Kind = transaction.KindTransferIn
			l.TransferGroupID = ptr("grupo-de-outra-execucao")
			amb.repo.linhas[id] = l
		},
	}

	for nome, sabotar := range sabotagens {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			amb := novoAmbienteTransacional(t)
			linhas := cenarioDeReprocessamento(t, amb)

			previa, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa),
				transaction.TransferDetectInput{Month: "2026-09", DryRun: true})
			require.NoError(t, err)
			require.EqualValues(t, 1, previa.Paired)

			// Entre a prévia e a confirmação, a perna de entrada some.
			sabotar(amb, linhas["entrada"].ID)

			real, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa),
				transaction.TransferDetectInput{Month: "2026-09", DryRun: false})
			require.NoError(t, err, "a prévia envelhecida não é conflito: é recálculo")
			assert.EqualValues(t, 0, real.Paired, "o par sumiu, `paired` diminui")
			assert.EqualValues(t, 3, real.Unpaired,
				"a perna que sobrou entra na conta de sem par, junto das outras duas")

			// A perna que sobrou continua receita/despesa: nada meio
			// convertido, nenhuma perna sintética.
			saida := amb.repo.linhas[linhas["saida"].ID]
			assert.Equal(t, transaction.KindExpense, saida.Kind)
			assert.Nil(t, saida.TransferGroupID)
			assert.Zero(t, amb.repo.conversoes)

			// A execução real audita mesmo convertendo zero (§13.1.7).
			assert.Len(t, amb.auditor.registros, 1)
		})
	}
}

// Teto de 500 na LISTA com contagem COMPLETA (§13.2), medido de ponta a ponta
// pelo serviço: 501 pares e 501 sem par num mês.
func TestQADetectTransfersListaParaEm500MasPaiedEUnpairedSaoCompletos(t *testing.T) {
	t.Parallel()

	amb := novoAmbienteTransacional(t)
	amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "C6", account.KindChecking)
	amb.palavraDeConta(minhaCasa, "acc-a", "enviado")
	amb.palavraDeConta(minhaCasa, "acc-b", "recebido")

	const total = transaction.MaxTransferDetectListed + 1
	for i := range total {
		// Um valor distinto por par, para o balde não misturá-los.
		valor := int64(1_00 + i)
		amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "Pix enviado - Bruno", valor, 10)
		amb.lancamento(minhaCasa, "acc-b", transaction.KindIncome, "Pix recebido de Bruno", valor, 10)
		// Uma despesa sem espelho por valor, bem longe dos pares.
		amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "Pix enviado - Maria", 900_000+valor, 10)
	}

	previa, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa),
		transaction.TransferDetectInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)
	assert.EqualValues(t, total, previa.Paired, "a contagem é completa")
	assert.EqualValues(t, total, previa.Unpaired)
	assert.Len(t, previa.Items, transaction.MaxTransferDetectListed, "a lista para no teto")
	assert.Len(t, previa.UnpairedItems, transaction.MaxTransferDetectListed)

	real, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa),
		transaction.TransferDetectInput{Month: "2026-09", DryRun: false})
	require.NoError(t, err)
	assert.EqualValues(t, total, real.Paired, "todos os pares são convertidos, não só os listados")
	assert.Empty(t, real.Items)
	assert.Equal(t, total, amb.repo.conversoes)
}

// A prévia não muda NADA — nem `updated_at`, nem a contagem de linhas —
// mesmo rodada várias vezes (§13.5.6). E é estável: duas prévias seguidas dão
// exatamente a mesma resposta.
func TestQADetectTransfersPreviaEIdempotenteEEstavel(t *testing.T) {
	t.Parallel()

	amb := novoAmbienteTransacional(t)
	cenarioDeReprocessamento(t, amb)
	antes := clonarLinhas(amb.repo)

	primeira, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa),
		transaction.TransferDetectInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)
	segunda, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa),
		transaction.TransferDetectInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)

	assert.Equal(t, primeira, segunda, "a prévia tem de ser determinística")
	assert.Equal(t, antes, clonarLinhas(amb.repo), "a prévia escreveu")
	assert.Len(t, antes, len(clonarLinhas(amb.repo)), "nenhuma linha criada nem removida")
	assert.Empty(t, amb.auditor.registros)
	assert.Zero(t, amb.repo.conversoes)
}

// Mês sem candidata nenhuma: zero, listas vazias, nenhuma consulta de janela
// desperdiçada, e a execução real ainda audita uma vez.
func TestQADetectTransfersMesSemCandidataDevolveZeroSemConsultarAJanela(t *testing.T) {
	t.Parallel()

	amb := novoAmbienteTransacional(t)
	amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	amb.palavraDeConta(minhaCasa, "acc-a", "enviado")
	amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "Padaria da esquina", 12_00, 3)

	view, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa),
		transaction.TransferDetectInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)
	assert.Zero(t, view.Paired)
	assert.Zero(t, view.Unpaired)
	assert.NotNil(t, view.Items)
	assert.NotNil(t, view.UnpairedItems)
	assert.Empty(t, view.Items)
	assert.Empty(t, view.UnpairedItems)

	real, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa),
		transaction.TransferDetectInput{Month: "2026-09", DryRun: false})
	require.NoError(t, err)
	assert.Zero(t, real.Paired)
	assert.Len(t, amb.auditor.registros, 1, "execução real audita mesmo com zero")
	assert.Zero(t, amb.repo.conversoes)
}

// Mês na virada do ano: dezembro procura espelho em janeiro do ano seguinte
// (a janela é por data, não por mês), e a competência do espelho não muda.
func TestQADetectTransfersAtravessaAViradaDoAno(t *testing.T) {
	t.Parallel()

	amb := novoAmbienteTransacional(t)
	amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "C6", account.KindChecking)
	amb.palavraDeConta(minhaCasa, "acc-b", "c6")

	saida := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-a", Kind: transaction.KindExpense, AmountCents: 500_00,
		Description: "Pagamento fatura C6", DescriptionNorm: "pagamento fatura c6",
		OccurredOn: civil.MustNew(2026, 12, 31), CompetenceMonth: "2026-12",
	})
	entrada := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-b", Kind: transaction.KindIncome, AmountCents: 500_00,
		Description: "Pagamento recebido", DescriptionNorm: "pagamento recebido",
		OccurredOn: civil.MustNew(2027, 1, 2), CompetenceMonth: "2027-01",
	})

	view, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa),
		transaction.TransferDetectInput{Month: "2026-12", DryRun: false})
	require.NoError(t, err)
	assert.EqualValues(t, 1, view.Paired, "2 dias de distância atravessando o ano")
	assert.Equal(t, transaction.KindTransferOut, amb.repo.linhas[saida.ID].Kind)
	assert.Equal(t, transaction.KindTransferIn, amb.repo.linhas[entrada.ID].Kind)
	assert.Equal(t, "2027-01", amb.repo.linhas[entrada.ID].CompetenceMonth)
}

// --- handler: as bordas de corpo que faltavam (§13.5.9) --------------------

func TestQADetectTransfersHandlerRecusaCorposHostis(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	cenarioDeReprocessamento(t, amb.ambiente)

	casos := []struct {
		nome   string
		corpo  string
		status int
	}{
		{"items no corpo", `{"month":"2026-09","dryRun":false,"items":[{"outTransactionId":"x"}]}`, http.StatusBadRequest},
		{"ids no corpo", `{"month":"2026-09","dryRun":false,"ids":["t-1","t-2"]}`, http.StatusBadRequest},
		{"householdId no corpo (BOLA)", `{"month":"2026-09","dryRun":false,"householdId":"casa-da-vizinha"}`, http.StatusBadRequest},
		{"transferGroupId escolhido pelo cliente", `{"month":"2026-09","dryRun":false,"transferGroupId":"g"}`, http.StatusBadRequest},
		{"array em vez de objeto", `[{"month":"2026-09","dryRun":false}]`, http.StatusBadRequest},
		{"null", `null`, http.StatusBadRequest},
		{"month com SQL", `{"month":"2026-09' OR '1'='1","dryRun":true}`, http.StatusBadRequest},
		{"month com HTML", `{"month":"<script>alert(1)</script>","dryRun":true}`, http.StatusBadRequest},
		{"month com unicode", `{"month":"２０２６-０９","dryRun":true}`, http.StatusBadRequest},
		{"month nulo", `{"month":null,"dryRun":true}`, http.StatusBadRequest},
		{"dryRun nulo", `{"month":"2026-09","dryRun":null}`, http.StatusBadRequest},
		{"dryRun numérico", `{"month":"2026-09","dryRun":1}`, http.StatusBadRequest},
		{"JSON truncado", `{"month":"2026-09","dryRun":`, http.StatusBadRequest},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect", c.corpo, amb.handler.DetectTransfers)
			require.Equal(t, c.status, rec.Code, rec.Body.String())
			// A resposta de erro nunca ecoa a entrada — nem o month hostil.
			assert.NotContains(t, rec.Body.String(), "<script>")
			assert.NotContains(t, rec.Body.String(), "OR '1'='1")
		})
	}

	assert.Zero(t, amb.repo.conversoes, "nenhum corpo hostil converteu nada")
	assert.Empty(t, amb.auditor.registros)
}

// Critério 11: nem o 422 do teto nem o corpo de erro carregam id, valor ou
// descrição — e o log do 422 não vira log de ERROR.
func TestQADetectTransfers422NaoVazaDadoDaCasa(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	var ids []string
	for i := 0; i <= transaction.MaxTransferDetectRows; i++ {
		l := amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense,
			fmt.Sprintf("Pix enviado - Segredo %d", i), 4242, 1)
		if i < 3 {
			ids = append(ids, l.ID)
		}
	}

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect", `{"month":"2026-09","dryRun":false}`, amb.handler.DetectTransfers)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	corpo := rec.Body.String()
	for _, proibido := range append(ids, "Segredo", "4242", "10001") {
		assert.NotContains(t, corpo, proibido, "vazou na resposta 422: %q", proibido)
	}
	log := amb.logs.String()
	for _, proibido := range append(ids, "Segredo", "4242") {
		assert.NotContains(t, log, proibido, "vazou no log: %q", proibido)
	}
	assert.Zero(t, amb.repo.conversoes, "422 nunca é execução parcial")
	assert.Empty(t, amb.auditor.registros)
}

// Critério 8, pela outra ponta: a vizinha que reprocessa o MESMO mês nunca
// lê nem converte nada da minha casa, mesmo com descrição e valor idênticos.
func TestQADetectTransfersDaVizinhaNaoTocaNaMinhaCasa(t *testing.T) {
	t.Parallel()

	amb := novoAmbienteTransacional(t)
	linhas := cenarioDeReprocessamento(t, amb)
	minhasAntes := map[string]transaction.Transaction{}
	for id, l := range clonarLinhas(amb.repo) {
		if l.HouseholdID == minhaCasa {
			minhasAntes[id] = l
		}
	}

	amb.repo.casasConsultadas = nil
	view, err := amb.svc.DetectTransfers(t.Context(), ator(outraCasa),
		transaction.TransferDetectInput{Month: "2026-09", DryRun: false})
	require.NoError(t, err)
	assert.EqualValues(t, 1, view.Paired, "só o par dela")

	require.NotEmpty(t, amb.repo.casasConsultadas)
	for _, casa := range amb.repo.casasConsultadas {
		assert.Equal(t, outraCasa, casa, "uma chamada ao repositório saiu com a casa errada")
	}

	// Byte a byte: nenhuma linha minha mudou.
	for id, antes := range minhasAntes {
		assert.Equal(t, antes, amb.repo.linhas[id], "a execução da vizinha alterou a linha %s da minha casa", id)
	}
	assert.Equal(t, transaction.KindExpense, amb.repo.linhas[linhas["saida"].ID].Kind)
	assert.Equal(t, transaction.KindIncome, amb.repo.linhas[linhas["entrada"].ID].Kind)

	// A auditoria da execução dela é da casa dela.
	require.Len(t, amb.auditor.registros, 1)
	assert.Equal(t, outraCasa, amb.auditor.registros[0].HouseholdID)
}
