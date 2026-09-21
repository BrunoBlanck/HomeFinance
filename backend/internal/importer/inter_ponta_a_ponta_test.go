package importer_test

// Ponta a ponta do EXTRATO DO INTER pela API real (handler → serviço → trava →
// dedup → staging → confirm → lançamentos).
//
// O teste-ouro em inter/inter_test.go prova o parser isolado; este arquivo
// prova a FIAÇÃO: que a instituição `inter` atravessa a trava de consistência
// (spec 0004 §3.3) nos dois sentidos, que o número brasileiro e o sinal
// sobrevivem ao caminho completo com os centavos certos, e que a reimportação
// do mesmo arquivo não grava nada duas vezes. É o achado B2 da revisão de
// segurança do parser do Inter, fechado.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/c6"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/importer/inter"
	"github.com/brunorblanck/homefinance/backend/internal/importer/nubank"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// habilitarInter reconstrói a.svc com os CINCO parsers, mantendo tudo o mais
// que novoAmbiente ligou (mesmos repositórios, mesma uow, mesmo relógio, mesma
// auditoria). Depois disto, a.handler(t) usa o serviço que reconhece o Inter —
// e continua reconhecendo o Nubank e o C6, que é o que TestInterNaoAmbiguoPelaAPI
// confere.
func (a *ambiente) habilitarInter(t *testing.T) {
	t.Helper()
	reg, err := importer.NewRegistry(
		nubank.NewChecking(), nubank.NewCard(),
		c6.NewChecking(), c6.NewCard(),
		inter.NewChecking(),
	)
	require.NoError(t, err)
	uow := gormstore.NewUnitOfWork(a.db)
	a.svc = importer.NewService(a.repoImport, reg, a.repoConta, a.repoTx,
		a.txSvc, a.stmtSvc, uow, a.classificador,
		importer.WithAudit(a.auditoria),
		importer.WithClock(a.relogio.now),
	)
}

// fixtureInterExtrato lê o arquivo anonimizado do parser do Inter.
func fixtureInterExtrato(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("inter", "testdata", "inter_checking_v1.csv"))
	require.NoError(t, err)
	return b
}

// contaInterCorrente é o destino válido do extrato; contaInterCartao é o
// destino do TIPO errado (extrato em cartão).
func (a *ambiente) contaInterCorrente(t *testing.T) *account.Account {
	return a.conta(t, a.casa.ID, "Inter Conta Corrente", account.KindChecking, account.InstitutionInter)
}

func (a *ambiente) contaInterCartao(t *testing.T) *account.Account {
	return a.conta(t, a.casa.ID, "Inter Cartão", account.KindCreditCard, account.InstitutionInter)
}

// ---------------------------------------------------------------------------
// Extrato Inter — feliz, ponta a ponta
// ---------------------------------------------------------------------------

// TestInterExtratoPontaAPontaPelaAPI prova, pela API real, que o número
// brasileiro (`-9.800,00`) e o sinal (negativo é saída) sobrevivem ao caminho
// completo com os centavos certos, que o preâmbulo de 5 linhas é pulado, e que
// a descrição gravada é "Histórico - Descrição". A troca do formato numérico
// derrubaria o arquivo inteiro; a troca do sinal inverteria 6 das 7 linhas.
func TestInterExtratoPontaAPontaPelaAPI(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarInter(t)
	conta := a.contaInterCorrente(t)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "Extrato_Inter.csv", fixtureInterExtrato(t)))
	assert.Equal(t, inter.CheckingFormatID, lote.FormatID, "o registro escolheu o parser do extrato do Inter")
	require.NotNil(t, lote.Counts)
	assert.Equal(t, 7, lote.Counts.New, "as 7 linhas da fixture são novas na primeira importação")

	// A revisão: nenhuma linha barrada por default — a fixture não tem pagamento
	// de fatura —, e o kind de cada linha já é o da convenção declarada.
	revisao := revisarPelaAPI(t, a, lote.ID)
	require.Len(t, revisao.Items, 7)
	for _, l := range revisao.Items {
		assert.Equal(t, importer.ActionImport, l.DefaultAction, "linha nova entra por default (%v)", l.Description)
		assert.Equal(t, string(dedup.StatusNew), l.Status)
	}
	recebido := acharLinha(t, revisao.Items, "Pix recebido - Empresa Exemplo Ltda")
	require.NotNil(t, recebido.Kind)
	assert.Equal(t, transaction.KindIncome, *recebido.Kind, "o único valor positivo é a única entrada")

	// Confirma aceitando os defaults: as 7 entram.
	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	assert.Equal(t, 7, res.Imported, "as 7 linhas entram")
	assert.Zero(t, res.Skipped, "nada é pulado por default num extrato sem pagamento de fatura")

	// O extrato não tem statement: a competência é o mês do occurred_on, e a
	// fixture inteira cai em agosto.
	ago := listarPelaAPI(t, a, "2026-08").Items
	require.Len(t, ago, 7, "as sete linhas caem em agosto")

	// Os valores e o SINAL de cada linha ÚNICA — os centavos são o que a troca
	// DecimalComma→DecimalPoint mudaria; o kind, o que a troca de sinal mudaria.
	casos := []struct {
		descricao string
		kind      string
		cents     int64
	}{
		{"Pagamento efetuado - ADMINISTRADORA EXEMPLO S/A", transaction.KindExpense, 21078},
		{"Pagamento efetuado - ESCRITORIO EXEMPLO LTDA", transaction.KindExpense, 35000},
		{"Pix recebido - Empresa Exemplo Ltda", transaction.KindIncome, 1250000},
	}
	for _, c := range casos {
		v := acharLancamento(t, ago, c.descricao)
		assert.Equalf(t, c.kind, v.Kind, "SINAL de %q", c.descricao)
		assert.Equalf(t, c.cents, v.AmountCents, "valor de %q", c.descricao)
		assert.Equalf(t, conta.ID, v.AccountID, "%q entra na conta do lote", c.descricao)
	}

	// As descrições repetidas com valores diferentes: os dois Pix para a Receita
	// Federal (700,00 e 160,25) e os dois para Fulano (9.800,00 e 1.250,50) —
	// todos saídas, e os quatro valores presentes.
	var receita, fulano []int64
	for _, v := range ago {
		switch v.Description {
		case "Pix enviado - Receita Federal":
			assert.Equal(t, transaction.KindExpense, v.Kind)
			receita = append(receita, v.AmountCents)
		case "Pix enviado - Fulano de Tal Silva":
			assert.Equal(t, transaction.KindExpense, v.Kind)
			fulano = append(fulano, v.AmountCents)
		}
	}
	assert.ElementsMatch(t, []int64{70000, 16025}, receita, "os dois Pix para a Receita Federal")
	assert.ElementsMatch(t, []int64{980000, 125050}, fulano, "os dois Pix para Fulano")

	// Contagem de sinais: 1 entrada, 6 saídas. Invertido o sinal, vira 6 e 1.
	var entradas, saidas int
	for _, v := range ago {
		switch v.Kind {
		case transaction.KindIncome:
			entradas++
		case transaction.KindExpense:
			saidas++
		}
	}
	assert.Equal(t, 1, entradas, "uma entrada (o único valor positivo)")
	assert.Equal(t, 6, saidas, "seis saídas (os seis valores negativos)")
}

// ---------------------------------------------------------------------------
// Reimportação — NUNCA importar linha duplicada, NUNCA engolir lançamento real
// ---------------------------------------------------------------------------

// TestInterDedupReimportacaoExtrato: o extrato do Inter não tem chave natural,
// então cai na chave derivada com ordinal, como o C6. Reimportar o MESMO
// arquivo, byte a byte, tem de dar 7 duplicadas e ZERO importadas — e os dois
// "Pix enviado - Receita Federal" (valores diferentes) continuam sendo dois.
func TestInterDedupReimportacaoExtrato(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarInter(t)
	conta := a.contaInterCorrente(t)

	primeiro := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "Extrato_Inter.csv", fixtureInterExtrato(t)))
	res1 := resultadoDaResposta(t, confirmarPelaAPI(t, a, primeiro.ID, `{"decisions":[]}`))
	require.Equal(t, 7, res1.Imported)

	ago := listarPelaAPI(t, a, "2026-08").Items
	assert.Equal(t, 2, contarDescricao(ago, "Pix enviado - Receita Federal"))

	segundo := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "Extrato_Inter.csv", fixtureInterExtrato(t)))
	require.NotNil(t, segundo.Counts)
	assert.Zero(t, segundo.Counts.New, "nenhuma linha volta como nova")
	assert.Equal(t, 7, segundo.Counts.DuplicateExact, "as 7 já gravadas voltam como duplicadas")
	assert.Zero(t, segundo.Counts.RepeatedInFile, "não há linha idêntica dentro da fixture")

	res2 := resultadoDaResposta(t, confirmarPelaAPI(t, a, segundo.ID, `{"decisions":[]}`))
	assert.Zero(t, res2.Imported, "ZERO importadas na reimportação")

	depois := listarPelaAPI(t, a, "2026-08").Items
	assert.Len(t, depois, 7, "continuam sete: nada duplicou, nada foi engolido")
	assert.Equal(t, 2, contarDescricao(depois, "Pix enviado - Receita Federal"))
}

// ---------------------------------------------------------------------------
// Detecção com os cinco parsers ligados — ninguém rouba ninguém
// ---------------------------------------------------------------------------

// TestInterNaoAmbiguoPelaAPI prova, pelo handler, que o Inter é aceito (201)
// com o seu FormatID, e que o extrato do C6 — que também começa com "Data
// Lançamento" — e o do Nubank continuam sendo deles com o Inter no registro.
func TestInterNaoAmbiguoPelaAPI(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarInter(t)

	loteInter := loteDaResposta(t, enviarPelaAPI(t, a, a.contaInterCorrente(t).ID, "Extrato_Inter.csv", fixtureInterExtrato(t)))
	assert.Equal(t, inter.CheckingFormatID, loteInter.FormatID,
		"o extrato do Inter é do Inter, nunca do C6 nem ambíguo")

	loteC6 := loteDaResposta(t, enviarPelaAPI(t, a, a.contaC6Corrente(t).ID, "Extrato_C6.csv", fixtureC6Extrato(t)))
	assert.Equal(t, c6.CheckingFormatID, loteC6.FormatID,
		"o extrato do C6 continua do C6 com o Inter no registro")

	loteNubank := loteDaResposta(t, enviarPelaAPI(t, a, a.contaCorrente(t).ID, "NU.csv", fixtureExtrato(t)))
	assert.Equal(t, nubank.CheckingFormatID, loteNubank.FormatID,
		"o extrato do Nubank continua do Nubank com o Inter no registro")
}

// ---------------------------------------------------------------------------
// A trava de consistência (spec 0004 §3.3) — os CAMPOS quando o arquivo é do Inter
// ---------------------------------------------------------------------------

// TestInterExtratoEmContaDeCartaoInterInformaExpectedBankAccount: extrato do
// Inter numa conta de CARTÃO do Inter. Instituição bate (inter==inter), então
// a divergência é de TIPO — expected_bank_account — e o detectedInstitution é
// inter, que é o que o frontend usa para propor "Conta Inter".
func TestInterExtratoEmContaDeCartaoInterInformaExpectedBankAccount(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarInter(t)
	cartao := a.contaInterCartao(t)

	campos := camposDoMismatchQA(t, enviarPelaAPI(t, a, cartao.ID, "Extrato_Inter.csv", fixtureInterExtrato(t)))

	assert.Equal(t, importer.ReasonExpectedBankAccount, campos["reason"])
	assert.Equal(t, string(importer.InstitutionInter), campos["detectedInstitution"])
	assert.Equal(t, string(importer.DocKindCheckingStatement), campos["detectedDocKind"])
	assert.NotContains(t, campos, "accountInstitution", "instituição bate: sem accountInstitution")

	// A trava recusa ANTES do staging: nem lote pendente, nem lançamento.
	a.nadaFicouParaTras(t)
}

// TestInterExtratoEmContaNubankInformaWrongInstitution: extrato do Inter numa
// conta corrente marcada como nubank. O tipo até bate (extrato em conta
// corrente), mas a instituição diverge e PRECEDE — wrong_institution, com
// detectedInstitution=inter e accountInstitution=nubank.
func TestInterExtratoEmContaNubankInformaWrongInstitution(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarInter(t)
	contaNubank := a.contaCorrente(t) // nubank/checking

	campos := camposDoMismatchQA(t, enviarPelaAPI(t, a, contaNubank.ID, "Extrato_Inter.csv", fixtureInterExtrato(t)))

	require.Equal(t, importer.ReasonWrongInstitution, campos["reason"],
		"instituição errada é a razão, mesmo com o tipo certo")
	assert.Equal(t, string(importer.InstitutionInter), campos["detectedInstitution"])
	assert.Equal(t, string(importer.DocKindCheckingStatement), campos["detectedDocKind"])
	assert.Equal(t, account.InstitutionNubank, campos["accountInstitution"])

	a.nadaFicouParaTras(t)
}

// nadaFicouParaTras prova que um arquivo recusado pela trava não deixou NADA
// gravado: nem lote pendente (a lista de lotes é o que denunciaria um staging
// feito antes da recusa), nem lançamento. Asserir só a lista de lançamentos
// provaria menos do que o comentário promete — o staging vive em outra tabela.
func (a *ambiente) nadaFicouParaTras(t *testing.T) {
	t.Helper()
	lotes, err := a.svc.ListBatches(t.Context(), a.ator(), 50)
	require.NoError(t, err)
	assert.Empty(t, lotes.Items, "arquivo recusado não deixa lote pendente para trás")
	assert.Empty(t, listarPelaAPI(t, a, "2026-08").Items, "arquivo recusado não grava lançamento")
}

// TestInterExtratoEmContaOtherNaoTrava guarda o ADR-024a para o emissor novo:
// conta marcada `other` PERDE a checagem de instituição, não a inverte — o
// extrato do Inter entra numa conta corrente sem instituição.
func TestInterExtratoEmContaOtherNaoTrava(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarInter(t)
	semMarca := a.conta(t, a.casa.ID, "Conta Sem Marca", account.KindChecking, account.InstitutionOther)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, semMarca.ID, "Extrato_Inter.csv", fixtureInterExtrato(t)))
	assert.Equal(t, inter.CheckingFormatID, lote.FormatID)
	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	assert.Equal(t, 7, res.Imported)
}
