package importer

// Teste CAIXA-BRANCA da travaDeConsistencia: exercita a função pura direto, sem
// banco nem multipart. É o que prova os CAMPOS de cada `reason` no ponto onde
// eles nascem — inclusive o unknown_document, que é defesa e não tem como ser
// alcançado ponta a ponta (o registro só produz DocKind válido).

import (
	"errors"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func conta(instituicao, kind string) *account.Account {
	return &account.Account{Institution: instituicao, Kind: kind}
}

func TestTravaDeConsistenciaEnriqueceOMotivo(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome        string
		conta       *account.Account
		instituicao Institution
		doc         DocKind
		reason      string
		detInst     string
		detDoc      string
		accInst     string
	}{
		{
			nome:        "fatura em conta que não é cartão",
			conta:       conta(account.InstitutionNubank, account.KindChecking),
			instituicao: InstitutionNubank,
			doc:         DocKindCardStatement,
			reason:      ReasonExpectedCreditCard,
			detInst:     "nubank",
			detDoc:      "card_statement",
			accInst:     "", // instituição bate: sem accountInstitution.
		},
		{
			nome:        "extrato em conta de cartão",
			conta:       conta(account.InstitutionNubank, account.KindCreditCard),
			instituicao: InstitutionNubank,
			doc:         DocKindCheckingStatement,
			reason:      ReasonExpectedBankAccount,
			detInst:     "nubank",
			detDoc:      "checking_statement",
			accInst:     "",
		},
		{
			nome:        "instituição do arquivo diferente da conta",
			conta:       conta(account.InstitutionC6, account.KindChecking),
			instituicao: InstitutionNubank,
			doc:         DocKindCheckingStatement,
			reason:      ReasonWrongInstitution,
			detInst:     "nubank",
			detDoc:      "checking_statement",
			accInst:     "c6", // a instituição da conta, só aqui.
		},
		{
			nome:        "instituição precede o tipo quando ambos divergem",
			conta:       conta(account.InstitutionC6, account.KindChecking),
			instituicao: InstitutionNubank,
			doc:         DocKindCardStatement, // sozinho daria expected_credit_card.
			reason:      ReasonWrongInstitution,
			detInst:     "nubank",
			detDoc:      "card_statement",
			accInst:     "c6",
		},
		{
			nome:        "other perde a checagem de instituição mas não a de tipo",
			conta:       conta(account.InstitutionOther, account.KindChecking),
			instituicao: InstitutionNubank,
			doc:         DocKindCardStatement,
			reason:      ReasonExpectedCreditCard,
			detInst:     "nubank",
			detDoc:      "card_statement",
			accInst:     "", // other nunca vira wrong_institution.
		},
		{
			nome:        "documento desconhecido (defesa)",
			conta:       conta(account.InstitutionNubank, account.KindChecking),
			instituicao: InstitutionNubank,
			doc:         DocKind("mistério"),
			reason:      ReasonUnknownDocument,
			detInst:     "nubank",
			detDoc:      "", // DocKind fora da allowlist não é publicado.
			accInst:     "",
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()

			err := travaDeConsistencia(c.conta, c.instituicao, c.doc)
			require.Error(t, err)

			// O handler depende de errors.Is para escolher o código HTTP.
			assert.True(t, errors.Is(err, ErrTargetMismatch),
				"o erro tipado precisa continuar sendo um ErrTargetMismatch")

			var tm *targetMismatchError
			require.ErrorAs(t, err, &tm)
			assert.Equal(t, c.reason, tm.Reason)
			assert.Equal(t, c.detInst, tm.DetectedInstitution)
			assert.Equal(t, c.detDoc, tm.DetectedDocKind)
			assert.Equal(t, c.accInst, tm.AccountInstitution)

			// Nenhum campo carrega PII: só enums fechados (ou vazio).
			assert.NotContains(t, err.Error(), "mistério",
				"o Error() não pode ecoar a string crua do documento")
		})
	}
}

func TestTravaDeConsistenciaAceitaOsCasosCertos(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome        string
		conta       *account.Account
		instituicao Institution
		doc         DocKind
	}{
		{"extrato em conta corrente do mesmo emissor",
			conta(account.InstitutionNubank, account.KindChecking), InstitutionNubank, DocKindCheckingStatement},
		{"fatura em cartão do mesmo emissor",
			conta(account.InstitutionNubank, account.KindCreditCard), InstitutionNubank, DocKindCardStatement},
		{"other com tipo compatível não trava",
			conta(account.InstitutionOther, account.KindChecking), InstitutionNubank, DocKindCheckingStatement},
		{"instituição vazia com tipo compatível não trava",
			conta("", account.KindChecking), InstitutionNubank, DocKindCheckingStatement},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			assert.NoError(t, travaDeConsistencia(c.conta, c.instituicao, c.doc))
		})
	}
}
