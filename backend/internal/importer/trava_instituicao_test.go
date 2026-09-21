package importer_test

import (
	"net/http"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A trava de consistência da §3.3 tem DUAS metades, e uma delas estava inerte:
// `institution` não existia no corpo de POST /accounts nem no DTO de resposta,
// então toda conta nascia `other` — e `other` não trava nada. A metade
// declarada da spec existia só no papel.
//
// Estes testes usam o caminho REAL do usuário: a conta é criada pelo SERVIÇO de
// contas (o mesmo que o handler chama), não escrita direto no repositório. É a
// diferença entre provar que a trava funciona e provar que ela é ALCANÇÁVEL.

// atorDeConta monta o Actor do domínio de contas para a casa principal.
func (a *ambiente) atorDeConta() account.Actor {
	return account.Actor{HouseholdID: a.casa.ID, UserID: a.usuario.ID, IP: "203.0.113.7"}
}

// cartaoPelaAPI cria um cartão de crédito pelo serviço de contas, com
// instituição e dias de fatura — exatamente o que o corpo do POST carrega.
func (a *ambiente) cartaoPelaAPI(t *testing.T, nome, instituicao string, fechamento, vencimento *int) account.View {
	t.Helper()
	view, err := a.contaSvc.Create(t.Context(), a.atorDeConta(), account.CreateInput{
		Name:                nome,
		Kind:                account.KindCreditCard,
		Institution:         instituicao,
		StatementClosingDay: fechamento,
		StatementDueDay:     vencimento,
		OpeningBalanceCents: 0,
		OpeningDate:         civil.MustNew(2026, 1, 1),
	})
	require.NoError(t, err)
	return view
}

func dia(n int) *int { return &n }

// TestTravaDeInstituicaoDisparaEmContaCriadaPelaAPI é o teste que a revisão
// pediu: fatura do NUBANK numa conta marcada como C6 é recusada, e nada entra.
func TestTravaDeInstituicaoDisparaEmContaCriadaPelaAPI(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	conta := a.cartaoPelaAPI(t, "C6 Cartão", account.InstitutionC6, dia(28), dia(5))
	require.Equal(t, account.InstitutionC6, conta.Institution,
		"sem este campo na resposta, o front recebe undefined onde o tipo diz que há um enum")

	rec := enviarPelaAPI(t, a, conta.ID, "fatura.csv", fixtureFatura(t))

	// Código próprio porque a AÇÃO da tela é trocar a conta de destino, e não
	// o arquivo.
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "IMPORT_TARGET_MISMATCH")

	// Nada foi gravado: nem lote, nem linha de staging, nem lançamento.
	lista, err := a.svc.ListBatches(t.Context(), a.ator(), 50)
	require.NoError(t, err)
	assert.Empty(t, lista.Items, "arquivo recusado não deixa lote pendente para trás")
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-09"))
}

// TestContaComInstituicaoCertaContinuaImportando é o controle: a trava recusa o
// emissor ERRADO, não a importação inteira. Sem este par, "recusar tudo"
// passaria no teste de cima.
func TestContaComInstituicaoCertaContinuaImportando(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	conta := a.cartaoPelaAPI(t, "Nubank Cartão API", account.InstitutionNubank, dia(5), dia(13))

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "fatura.csv", fixtureFatura(t)))
	assert.Equal(t, string(importer.DocKindCardStatement), lote.DocKind)

	// E os dias configurados NA CONTA passam a valer: a sugestão da fatura sai
	// deles, e não mais da inferência "maior data + 10 dias". Era o segundo
	// efeito de não haver caminho de escrita — o dado existia na coluna e nunca
	// chegava lá.
	require.NotNil(t, lote.StatementSuggestion)
	assert.Equal(t, "2026-09-05", lote.StatementSuggestion.ClosingDate.String())
	assert.Equal(t, "2026-09-13", lote.StatementSuggestion.DueDate.String())
	assert.Equal(t, "2026-09", lote.StatementSuggestion.CompetenceMonth)
}

// TestOsDoisVocabulariosDeInstituicaoConferem é o teste que paga a duplicação:
// account.Institutions() e importer.Institution são listas SEPARADAS (o pacote
// de importação importa o de contas, então a dependência inversa seria ciclo).
//
// Separadas e divergentes, a trava viraria ruído: uma instituição que existe só
// de um lado ou nunca casa (e nunca trava) ou trava sempre.
func TestOsDoisVocabulariosDeInstituicaoConferem(t *testing.T) {
	t.Parallel()

	for _, instituicao := range account.Institutions() {
		if instituicao == account.InstitutionOther {
			// `other` é o "sem instituição" da conta, e de propósito não existe
			// como emissor: nenhum arquivo é detectado como `other`.
			assert.False(t, importer.Institution(instituicao).Valid(),
				"`other` não pode ser um emissor detectável")
			continue
		}
		assert.True(t, importer.Institution(instituicao).Valid(),
			"instituição %q existe na conta e não existe na importação", instituicao)
	}

	// E o caminho de volta: todo emissor conhecido pela importação precisa ser
	// marcável na conta.
	for _, emissor := range []importer.Institution{importer.InstitutionNubank, importer.InstitutionC6, importer.InstitutionInter} {
		assert.True(t, account.ValidInstitution(string(emissor)),
			"emissor %q não é marcável em conta nenhuma", emissor)
	}
}

// TestContaOtherNaoTravaNada guarda a decisão do ADR-024a: `other` PERDE a
// checagem, não a inverte. Quem não marcou a instituição continua importando.
func TestContaOtherNaoTravaNada(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	conta := a.cartaoPelaAPI(t, "Cartão Sem Marca", account.InstitutionOther, nil, nil)
	require.Equal(t, account.InstitutionOther, conta.Institution)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "fatura.csv", fixtureFatura(t)))
	assert.Equal(t, "nubank", lote.Institution, "a instituição do LOTE é a detectada no arquivo")
}
