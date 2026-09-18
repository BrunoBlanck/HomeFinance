package importer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// O IMPORT_TARGET_MISMATCH deixou de ser um beco sem saída: além do código (que
// já mandava "troque a conta"), a resposta carrega em `fields` o MOTIVO
// estruturado para o frontend GUIAR o usuário — propor o cartão certo, apontar a
// instituição detectada. Estes testes provam os CAMPOS, ponta a ponta pelo
// handler, não só o código HTTP.
//
// Todos os valores publicados são enums fechados vindos do arquivo do próprio
// usuário (a conta já foi validada como dele antes da trava): não há PII nem
// dado de outra casa em `fields`.

// camposDoTargetMismatch exige 422 IMPORT_TARGET_MISMATCH e devolve o `fields`.
func camposDoTargetMismatch(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	var env httpserver.ErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.Equal(t, httpserver.CodeImportTargetMismatch, env.Error.Code, rec.Body.String())
	return env.Error.Fields
}

// TestFaturaEmContaCorrenteInformaExpectedCreditCard — reason expected_credit_card.
//
// Conta e arquivo são do MESMO emissor (nubank), então a instituição bate e a
// divergência é de TIPO: fatura numa conta que não é cartão.
func TestFaturaEmContaCorrenteInformaExpectedCreditCard(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t) // nubank, checking

	campos := camposDoTargetMismatch(t, enviarPelaAPI(t, a, conta.ID, "fatura.csv", fixtureFatura(t)))

	assert.Equal(t, importer.ReasonExpectedCreditCard, campos["reason"])
	assert.Equal(t, string(importer.InstitutionNubank), campos["detectedInstitution"])
	assert.Equal(t, string(importer.DocKindCardStatement), campos["detectedDocKind"])
	// accountInstitution só existe no wrong_institution — aqui a instituição bate.
	assert.NotContains(t, campos, "accountInstitution")
}

// TestExtratoEmContaDeCartaoInformaExpectedBankAccount — reason expected_bank_account.
func TestExtratoEmContaDeCartaoInformaExpectedBankAccount(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t) // nubank, credit_card

	campos := camposDoTargetMismatch(t, enviarPelaAPI(t, a, cartao.ID, "extrato.csv", fixtureExtrato(t)))

	assert.Equal(t, importer.ReasonExpectedBankAccount, campos["reason"])
	assert.Equal(t, string(importer.InstitutionNubank), campos["detectedInstitution"])
	assert.Equal(t, string(importer.DocKindCheckingStatement), campos["detectedDocKind"])
	assert.NotContains(t, campos, "accountInstitution")
}

// TestInstituicaoTrocadaInformaWrongInstitution — reason wrong_institution.
//
// Arquivo do nubank numa conta marcada como c6. O `accountInstitution` é o que o
// front usa para dizer "este arquivo é do Nubank, mas a conta é C6".
func TestInstituicaoTrocadaInformaWrongInstitution(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.conta(t, a.casa.ID, "C6 Conta", account.KindChecking, account.InstitutionC6)

	campos := camposDoTargetMismatch(t, enviarPelaAPI(t, a, conta.ID, "extrato.csv", fixtureExtrato(t)))

	assert.Equal(t, importer.ReasonWrongInstitution, campos["reason"])
	assert.Equal(t, string(importer.InstitutionNubank), campos["detectedInstitution"])
	assert.Equal(t, string(importer.DocKindCheckingStatement), campos["detectedDocKind"])
	assert.Equal(t, account.InstitutionC6, campos["accountInstitution"])
}

// TestInstituicaoTemPrecedenciaSobreTipo guarda a ordem da §3.3: quando a
// instituição E o tipo divergem, vence wrong_institution — a pista mais
// fundamental. Aqui é fatura (card_statement) numa conta CORRENTE c6: o tipo
// sozinho daria expected_credit_card, mas a instituição errada precede.
func TestInstituicaoTemPrecedenciaSobreTipo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.conta(t, a.casa.ID, "C6 Conta", account.KindChecking, account.InstitutionC6)

	campos := camposDoTargetMismatch(t, enviarPelaAPI(t, a, conta.ID, "fatura.csv", fixtureFatura(t)))

	require.Equal(t, importer.ReasonWrongInstitution, campos["reason"],
		"instituição errada precede a divergência de tipo")
	assert.Equal(t, string(importer.InstitutionNubank), campos["detectedInstitution"])
	assert.Equal(t, string(importer.DocKindCardStatement), campos["detectedDocKind"])
	assert.Equal(t, account.InstitutionC6, campos["accountInstitution"])
}

// TestContaOtherNaoDisparaWrongInstitutionMasDisparaTipo prova os dois lados da
// regra do `other`: ele PERDE a checagem de instituição (nunca vira
// wrong_institution), mas continua sujeito à trava de TIPO. Fatura numa conta
// corrente `other` sai como expected_credit_card, sem accountInstitution.
func TestContaOtherNaoDisparaWrongInstitutionMasDisparaTipo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.conta(t, a.casa.ID, "Conta Genérica", account.KindChecking, account.InstitutionOther)

	campos := camposDoTargetMismatch(t, enviarPelaAPI(t, a, conta.ID, "fatura.csv", fixtureFatura(t)))

	assert.Equal(t, importer.ReasonExpectedCreditCard, campos["reason"],
		"`other` perde a checagem de instituição, mas não a de tipo")
	assert.NotEqual(t, importer.ReasonWrongInstitution, campos["reason"])
	assert.Equal(t, string(importer.InstitutionNubank), campos["detectedInstitution"])
	assert.Equal(t, string(importer.DocKindCardStatement), campos["detectedDocKind"])
	// `other` nunca chega ao wrong_institution, então não há accountInstitution.
	assert.NotContains(t, campos, "accountInstitution")
}
