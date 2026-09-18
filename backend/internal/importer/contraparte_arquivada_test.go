package importer_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 0005 §12 (achado do QA da E2c): `transfer` para contraparte ARQUIVADA
// responde 422 VALIDATION_FAILED em `fields.counterpartAccountId` — a conta
// da outra perna é a única das duas que está no corpo do confirm. A conta do
// LOTE arquivada continua em `fields.accountId`. Nos dois casos nada é
// gravado (tudo ou nada) e o lote segue pendente.

// camposDoValidationFailed exige 422 VALIDATION_FAILED e devolve o `fields`.
func camposDoValidationFailed(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	var env httpserver.ErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.Equal(t, httpserver.CodeValidationFailed, env.Error.Code, rec.Body.String())
	return env.Error.Fields
}

// A conta do LOTE arquivada entre a análise e o confirm continua apontando
// `accountId`: é o contraste da regra nova — o campo muda só quando a conta
// arquivada é a contraparte.
func TestContaDoLoteArquivadaEntreAAnaliseEOConfirmApontaAccountId(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)

	extrato := csvExtratoAPI(
		linhaExtrato{6, "-11.00", "02", "Transferência enviada pelo Pix - Padaria Exemplo"},
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extrato))

	_, err := a.contaSvc.Archive(t.Context(), a.atorDeConta(), contaA.ID)
	require.NoError(t, err)

	rec := confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`)
	campos := camposDoValidationFailed(t, rec)
	assert.Contains(t, campos, "accountId")
	assert.NotContains(t, campos, "counterpartAccountId")
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), "tudo ou nada")
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, lote.ID).Batch.Status)
}

// Na FATURA a contraparte é a perna de SAÍDA (o dinheiro sai da corrente e
// entra no cartão): a regra vale para as duas direções do par.
func TestPagamentoDeFaturaComContraparteArquivadaApontaCounterpartAccountId(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)
	corrente := a.contaCorrente(t)
	_, err := a.contaSvc.Archive(t.Context(), a.atorDeConta(), corrente.ID)
	require.NoError(t, err)

	fatura := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "fatura.csv", fixtureFatura(t)))
	recebido := linhaComDescricao(t, revisarPelaAPI(t, a, fatura.ID).Items, "Pagamento recebido")
	require.Equal(t, string(dedup.StatusCardPayment), recebido.Status)

	rec := confirmarPelaAPI(t, a, fatura.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}],%s}`,
		recebido.ID, corrente.ID, confirmacaoDeFatura))
	campos := camposDoValidationFailed(t, rec)
	assert.Contains(t, campos, "counterpartAccountId")
	assert.NotContains(t, campos, "accountId")
	assert.NotContains(t, rec.Body.String(), corrente.ID, "a resposta não ecoa o id")

	// Nada entrou — nem as compras da fatura, nem a fatura em si (o upsert
	// dela roda na mesma transação e volta junto).
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-09"))
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, fatura.ID).Batch.Status)

	// Desarquivada a corrente, o mesmo pedido passa: a recusa era só o estado
	// da conta.
	_, err = a.contaSvc.Unarchive(t.Context(), a.atorDeConta(), corrente.ID)
	require.NoError(t, err)
	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, fatura.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}],%s}`,
		recebido.ID, corrente.ID, confirmacaoDeFatura)))
	assert.Equal(t, 1, res.TransfersCreated)
}

// Contraparte de OUTRA casa continua 404, byte a byte igual a inexistente: a
// conferência nova das contrapartes roda pela casa do token e nunca vira 422.
func TestContraparteArquivadaDeOutraCasaEh404ENao422(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	a.contaItau(t)
	alheia := a.conta(t, a.alheia.ID, "Poupança Alheia", account.KindSavings, account.InstitutionOther)
	_, err := a.contaSvc.Archive(t.Context(), account.Actor{HouseholdID: a.alheia.ID, UserID: a.outroUsuario.ID, IP: "203.0.113.8"}, alheia.ID)
	require.NoError(t, err)

	extrato := csvExtratoAPI(
		linhaExtrato{5, "-1500.00", "01", "Transferência enviada pelo Pix - Itau Corrente"},
	)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extrato))
	linha := linhaPorStatus(t, revisarPelaAPI(t, a, lote.ID).Items, dedup.StatusInternalTransfer)

	rec := confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}]}`, linha.ID, alheia.ID))
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "arquivada", "arquivada ou não, conta alheia é só 'não encontrada'")
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
	assert.Empty(t, a.lancamentosDa(t, a.alheia.ID, "2026-08"))
}
