package importer_test

// QA — validação PONTA A PONTA do C6 pela API (multipart real → analyze →
// confirm → GET /transactions), e da trava de conta virada em caminho com os
// CAMPOS certos quando o arquivo é do C6.
//
// Por que este arquivo existe, separado do c6_test.go (que é golden de PARSER):
// o golden prova a convenção linha a linha DENTRO do parser; aqui prova a
// FIAÇÃO — o multipart, a detecção pelo registro de 4 parsers, o staging, o
// recálculo dentro da transação do confirm, o statement_id da fatura e a
// listagem por competência. Cada um pode desligar a garantia sozinho sem que um
// teste de parser fique vermelho.
//
// O harness de integração (harness_test.go) monta o serviço com registro
// SÓ-NUBANK. Para o C6 passar pela API é preciso reconstruir o serviço com os 4
// parsers — é o que habilitarC6 faz, reusando repositórios, uow e relógio já
// montados. Sem isso a detecção nunca escolheria um parser do C6.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/c6"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/importer/nubank"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// habilitarC6 reconstrói a.svc com os QUATRO parsers, mantendo tudo o mais que
// novoAmbiente ligou (mesmos repositórios, mesma uow, mesmo relógio, mesma
// auditoria). Depois disto, a.handler(t) usa o serviço que reconhece o C6.
func (a *ambiente) habilitarC6(t *testing.T) {
	t.Helper()
	reg, err := importer.NewRegistry(nubank.NewChecking(), nubank.NewCard(), c6.NewChecking(), c6.NewCard())
	require.NoError(t, err)
	uow := gormstore.NewUnitOfWork(a.db)
	a.svc = importer.NewService(a.repoImport, reg, a.repoConta, a.repoTx,
		a.txSvc, a.stmtSvc, uow, a.classificador,
		importer.WithAudit(a.auditoria),
		importer.WithClock(a.relogio.now),
	)
}

// fixtureC6 lê um arquivo anonimizado do parser do C6.
func fixtureC6(t *testing.T, nome string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("c6", "testdata", nome))
	require.NoError(t, err)
	return b
}

func fixtureC6Extrato(t *testing.T) []byte { return fixtureC6(t, "c6_checking_v1.csv") }
func fixtureC6Fatura(t *testing.T) []byte  { return fixtureC6(t, "c6_card_statement_v1.csv") }

// contaC6Corrente e contaC6Cartao são os destinos válidos de cada documento.
func (a *ambiente) contaC6Corrente(t *testing.T) *account.Account {
	return a.conta(t, a.casa.ID, "C6 Conta Corrente", account.KindChecking, account.InstitutionC6)
}

func (a *ambiente) contaC6Cartao(t *testing.T) *account.Account {
	return a.conta(t, a.casa.ID, "C6 Cartão", account.KindCreditCard, account.InstitutionC6)
}

// acharLancamento devolve o primeiro lançamento com a descrição exata.
func acharLancamento(t *testing.T, itens []transaction.View, descricao string) transaction.View {
	t.Helper()
	for _, v := range itens {
		if v.Description == descricao {
			return v
		}
	}
	require.FailNowf(t, "lançamento não encontrado", "descrição %q não está na listagem", descricao)
	return transaction.View{}
}

func acharLinha(t *testing.T, linhas []importer.RowView, descricao string) importer.RowView {
	t.Helper()
	for _, l := range linhas {
		if l.Description != nil && *l.Description == descricao {
			return l
		}
	}
	require.FailNowf(t, "linha não encontrada", "descrição %q não está na revisão", descricao)
	return importer.RowView{}
}

// ---------------------------------------------------------------------------
// Extrato C6 — feliz, ponta a ponta
// ---------------------------------------------------------------------------

// TestC6ExtratoPontaAPontaPelaAPI prova, pela API real, que Entrada→income e
// Saída→expense sobrevivem ao caminho completo, com o valor certo, e que o
// preâmbulo de 8 linhas é pulado. A mutação Entrada↔Saída deixaria isto
// vermelho: os três Pix recebidos (income) trocariam de sinal.
func TestC6ExtratoPontaAPontaPelaAPI(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarC6(t)
	conta := a.contaC6Corrente(t)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "Extrato_C6.csv", fixtureC6Extrato(t)))
	assert.Equal(t, c6.CheckingFormatID, lote.FormatID, "o registro escolheu o parser do extrato do C6")

	// A revisão prova a classificação do pagamento de fatura ANTES de confirmar.
	revisao := revisarPelaAPI(t, a, lote.ID)
	pgto := acharLinha(t, revisao.Items, "PGTO FAT CARTAO C6")
	assert.Equal(t, string(dedup.StatusCardPayment), pgto.Status,
		"PGTO FAT CARTAO C6 é classificado como pagamento_de_fatura")
	assert.Equal(t, importer.ActionSkip, pgto.DefaultAction, "pagamento de fatura vem BARRADO por default")
	assert.Contains(t, pgto.AllowedActions, importer.ActionTransfer,
		"mas é LIBERÁVEL como transferência")
	require.NotNil(t, pgto.Kind)
	assert.Equal(t, transaction.KindExpense, *pgto.Kind, "no extrato a Saída da linha é uma despesa")

	// Confirma aceitando os defaults: o pagamento de fatura fica de fora, o resto entra.
	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	assert.Equal(t, 9, res.Imported, "9 linhas entram (as 10 do arquivo menos o pagamento de fatura barrado)")
	assert.Equal(t, 1, res.Skipped, "o pagamento de fatura é a única pulada por default")

	// O extrato não tem statement: a competência é o mês do occurred_on. Agosto e
	// setembro guardam parte das linhas.
	ago := listarPelaAPI(t, a, "2026-08").Items
	set := listarPelaAPI(t, a, "2026-09").Items
	assert.Len(t, ago, 4, "quatro linhas caem em agosto")
	assert.Len(t, set, 5, "cinco linhas caem em setembro")
	todos := append(append([]transaction.View{}, ago...), set...)

	// Os valores e o SINAL de cada linha ÚNICA, o que a mutação Entrada↔Saída
	// inverteria.
	casos := []struct {
		descricao string
		kind      string
		cents     int64
	}{
		{"CDB C6 LIM.GARANT.", transaction.KindExpense, 150000},
		{"APLICAÇÃO DE CDB", transaction.KindExpense, 600000},
		{"Pix recebido de EMPRESA EXEMPLO LTDA", transaction.KindIncome, 995000},
		{"TOTTA EXEMPLO LTDA", transaction.KindExpense, 200770},
		{"Pix automático enviado para CLARO", transaction.KindExpense, 12281},
	}
	for _, c := range casos {
		v := acharLancamento(t, todos, c.descricao)
		assert.Equalf(t, c.kind, v.Kind, "SINAL de %q", c.descricao)
		assert.Equalf(t, c.cents, v.AmountCents, "valor de %q", c.descricao)
		assert.Equalf(t, conta.ID, v.AccountID, "%q entra na conta do lote", c.descricao)
	}

	// A descrição "Pix recebido de Fulano de Tal Silva" aparece DUAS vezes, com
	// 3000,00 e 5000,00 — as duas Entradas. As duas têm de ser income e os dois
	// valores têm de estar presentes (a mutação Entrada↔Saída derrubaria o kind).
	var fulano []int64
	for _, v := range todos {
		if v.Description == "Pix recebido de Fulano de Tal Silva" {
			assert.Equal(t, transaction.KindIncome, v.Kind, "as duas linhas Fulano são Entradas")
			fulano = append(fulano, v.AmountCents)
		}
	}
	assert.ElementsMatch(t, []int64{300000, 500000}, fulano,
		"os dois Pix de Fulano entram com 3000,00 e 5000,00")

	// Contagem de sinais: 3 entradas (os Pix recebidos, um deles em agosto e é o
	// de 5000 reais), 6 saídas. Se Entrada e Saída trocarem, isto vira 6 e 3.
	var entradas, saidas int
	for _, v := range todos {
		switch v.Kind {
		case transaction.KindIncome:
			entradas++
		case transaction.KindExpense:
			saidas++
		}
	}
	assert.Equal(t, 3, entradas, "três Entradas viram income")
	assert.Equal(t, 6, saidas, "seis Saídas (o pagamento de fatura ficou de fora) viram expense")
}

// ---------------------------------------------------------------------------
// Fatura C6 — feliz, ponta a ponta
// ---------------------------------------------------------------------------

// TestC6FaturaPontaAPontaPelaAPI prova, pela API real, que compra positiva vira
// expense, que o crédito (Estorno Tarifa, negativo) vira income, que a parcela
// 2/7 é preservada e que dois cartões diferentes (finais 1111 e 2222) caem na
// MESMA conta. Inverter o sinal deixaria as oito compras como receita.
func TestC6FaturaPontaAPontaPelaAPI(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarC6(t)
	cartao := a.contaC6Cartao(t)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "Fatura_C6.csv", fixtureC6Fatura(t)))
	assert.Equal(t, c6.CardFormatID, lote.FormatID, "o registro escolheu o parser da fatura do C6")

	// "Inclusao de Pagamento" é crédito (income) na leitura, mas barrado por
	// default como pagamento de fatura — a revisão prova os dois fatos.
	revisao := revisarPelaAPI(t, a, lote.ID)
	pgto := acharLinha(t, revisao.Items, "Inclusao de Pagamento")
	assert.Equal(t, string(dedup.StatusCardPayment), pgto.Status)
	assert.Equal(t, importer.ActionSkip, pgto.DefaultAction, "pagamento recebido vem BARRADO por default")
	require.NotNil(t, pgto.Kind)
	assert.Equal(t, transaction.KindIncome, *pgto.Kind, "o pagamento (negativo) é crédito")

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID,
		`{"decisions":[],`+confirmacaoDeFatura+`}`))
	assert.Equal(t, 9, res.Imported, "nove linhas entram (as 10 menos o pagamento de fatura barrado)")
	assert.Equal(t, 1, res.Skipped, "só o pagamento de fatura fica de fora por default")
	require.NotNil(t, res.StatementID, "a fatura foi criada e amarra as linhas")

	// Toda linha da fatura entra sob a competência do vencimento (2026-09), e não
	// do mês da compra — é o statement_id que desloca.
	itens := listarPelaAPI(t, a, "2026-09").Items
	require.Len(t, itens, 9)

	casos := []struct {
		descricao string
		kind      string
		cents     int64
	}{
		{"LOJA EXEMPLO MOVEIS- · 2/7", transaction.KindExpense, 28401}, // parcela preservada
		{"LOJA ROUPA EXEMPLO", transaction.KindExpense, 13999},
		{"DL*APPCORRIDA", transaction.KindExpense, 957},
		{"Estorno Tarifa", transaction.KindIncome, 5000},                // crédito negativo → income
		{"Anuidade Diferenciada · 1/12", transaction.KindExpense, 5000}, // outra parcela preservada
		{"MERCADO EXEMPLO 475", transaction.KindExpense, 848},           // cartão final 2222
		{"BISTRO EXEMPLO", transaction.KindExpense, 3839},               // cartão final 2222
	}
	for _, c := range casos {
		v := acharLancamento(t, itens, c.descricao)
		assert.Equalf(t, c.kind, v.Kind, "SINAL de %q", c.descricao)
		assert.Equalf(t, c.cents, v.AmountCents, "valor de %q", c.descricao)
		assert.Equalf(t, cartao.ID, v.AccountID, "%q cai na MESMA conta (finais diferentes, mesmo destino)", c.descricao)
		require.NotNilf(t, v.StatementID, "%q pertence à fatura", c.descricao)
	}

	// 8 compras (expense) e 1 crédito (income); o pagamento ficou de fora. Se o
	// sinal inverter, isto vira 1 despesa e 8 receitas.
	var entradas, saidas int
	for _, v := range itens {
		switch v.Kind {
		case transaction.KindIncome:
			entradas++
		case transaction.KindExpense:
			saidas++
		}
	}
	assert.Equal(t, 1, entradas, "só o Estorno Tarifa é crédito")
	assert.Equal(t, 8, saidas, "as oito compras são saídas")

	// O par idêntico "PADARIA EXEMPLO" (6,69) é dois gastos reais: os DOIS entram.
	assert.Equal(t, 2, contarDescricao(itens, "PADARIA EXEMPLO"),
		"as duas linhas idênticas da fixture entram — nenhuma é engolida")
}

// ---------------------------------------------------------------------------
// Dedup do C6 — o par idêntico entra na 1ª e é barrado na reimportação
// ---------------------------------------------------------------------------

// TestC6DedupReimportacaoExtrato prova, para o extrato, o que o usuário exigiu:
// NUNCA importar linha duplicada, NUNCA engolir lançamento real. O par "Pix
// enviado para Beltrano" (dois de R$ 103,00) entra duas vezes na 1ª importação
// e é 100% barrado na 2ª — o total não se mexe.
func TestC6DedupReimportacaoExtrato(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarC6(t)
	conta := a.contaC6Corrente(t)

	primeiro := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "Extrato_C6.csv", fixtureC6Extrato(t)))
	res1 := resultadoDaResposta(t, confirmarPelaAPI(t, a, primeiro.ID, `{"decisions":[]}`))
	require.Equal(t, 9, res1.Imported)

	set := listarPelaAPI(t, a, "2026-09").Items
	assert.Equal(t, 2, contarDescricao(set, "Pix enviado para Beltrano Exemplo"),
		"os DOIS pix idênticos entram na primeira importação")

	// Reimporta o MESMO arquivo, byte a byte.
	segundo := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "Extrato_C6.csv", fixtureC6Extrato(t)))
	require.NotNil(t, segundo.Counts)
	assert.Zero(t, segundo.Counts.New, "nenhuma linha volta como nova")
	assert.Equal(t, 9, segundo.Counts.DuplicateExact, "as 9 já gravadas voltam como duplicadas")
	assert.Zero(t, segundo.Counts.RepeatedInFile,
		"na 2ª vez o banco já tem os dois pix: nenhum é 'repetido no arquivo'")

	res2 := resultadoDaResposta(t, confirmarPelaAPI(t, a, segundo.ID, `{"decisions":[]}`))
	assert.Zero(t, res2.Imported, "ZERO importadas na reimportação")

	depois := listarPelaAPI(t, a, "2026-09").Items
	assert.Equal(t, 2, contarDescricao(depois, "Pix enviado para Beltrano Exemplo"),
		"continuam DOIS: nem virou três (duplicata), nem virou um (linha engolida)")
}

// TestC6DedupReimportacaoFatura é o mesmo contrato para a fatura, onde o par
// idêntico é "PADARIA EXEMPLO" (6,69) e a chave é derivada (a fatura não numera
// as linhas).
func TestC6DedupReimportacaoFatura(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarC6(t)
	cartao := a.contaC6Cartao(t)

	primeiro := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "Fatura_C6.csv", fixtureC6Fatura(t)))
	res1 := resultadoDaResposta(t, confirmarPelaAPI(t, a, primeiro.ID,
		`{"decisions":[],`+confirmacaoDeFatura+`}`))
	require.Equal(t, 9, res1.Imported)

	itens := listarPelaAPI(t, a, "2026-09").Items
	require.Equal(t, 2, contarDescricao(itens, "PADARIA EXEMPLO"))

	segundo := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "Fatura_C6.csv", fixtureC6Fatura(t)))
	require.NotNil(t, segundo.Counts)
	assert.Zero(t, segundo.Counts.New, "nenhuma linha volta como nova")
	assert.Equal(t, 9, segundo.Counts.DuplicateExact)

	res2 := resultadoDaResposta(t, confirmarPelaAPI(t, a, segundo.ID,
		`{"decisions":[],`+confirmacaoDeFatura+`}`))
	assert.Zero(t, res2.Imported, "ZERO importadas na reimportação")

	depois := listarPelaAPI(t, a, "2026-09").Items
	assert.Equal(t, 2, contarDescricao(depois, "PADARIA EXEMPLO"),
		"continuam DOIS: o ordinal derivado guarda o par igual ao Nubank")
	assert.Len(t, depois, 9, "o total da competência não se mexe")
}

// ---------------------------------------------------------------------------
// Não-ambiguidade pela API — cada arquivo do C6 casa com um parser só
// ---------------------------------------------------------------------------

// TestC6NaoAmbiguoPelaAPI prova, pelo handler, que um arquivo do C6 é aceito
// (201) e classificado com o FormatID do C6 — nunca recusado como
// IMPORT_FORMAT_AMBIGUOUS por dois candidatos, nem lido pelo parser do Nubank.
func TestC6NaoAmbiguoPelaAPI(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarC6(t)

	// Cada fixture vai para a conta compatível, para o 201 provar só a detecção.
	corrente := a.contaC6Corrente(t)
	cartao := a.contaC6Cartao(t)

	loteExtrato := loteDaResposta(t, enviarPelaAPI(t, a, corrente.ID, "Extrato_C6.csv", fixtureC6Extrato(t)))
	assert.Equal(t, c6.CheckingFormatID, loteExtrato.FormatID,
		"o extrato do C6 é do C6, nunca do Nubank nem ambíguo")

	loteFatura := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "Fatura_C6.csv", fixtureC6Fatura(t)))
	assert.Equal(t, c6.CardFormatID, loteFatura.FormatID,
		"a fatura do C6 é do C6, nunca do Nubank nem ambígua")

	// E o inverso: um arquivo do Nubank continua sendo do Nubank com os 4 parsers
	// ligados — o C6 não o rouba.
	nubankConta := a.contaCorrente(t) // nubank/checking
	loteNubank := loteDaResposta(t, enviarPelaAPI(t, a, nubankConta.ID, "NU.csv", fixtureExtrato(t)))
	assert.Equal(t, nubank.CheckingFormatID, loteNubank.FormatID,
		"o extrato do Nubank continua do Nubank com o C6 no registro")
}

// ---------------------------------------------------------------------------
// A trava virada em caminho — os CAMPOS quando o arquivo é do C6
// ---------------------------------------------------------------------------
//
// O target_mismatch_fields_test.go (do dev) cobre os campos com fixtures do
// NUBANK. O ponto 2 pede explicitamente "fatura (Nubank ou C6)" e
// "detectedInstitution certo": estes dois casos provam que a detecção do C6
// alimenta os `fields` — detectedInstitution = c6 —, que é o que o frontend usa
// para propor "Cartão C6".

func camposDoMismatchQA(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	var env httpserver.ErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
	require.Equal(t, httpserver.CodeImportTargetMismatch, env.Error.Code, rec.Body.String())
	return env.Error.Fields
}

// TestC6FaturaEmContaCorrenteC6InformaExpectedCreditCard: fatura do C6 numa
// conta corrente do C6. Instituição bate (c6==c6), então a divergência é de
// TIPO — expected_credit_card — e o detectedInstitution é c6.
func TestC6FaturaEmContaCorrenteC6InformaExpectedCreditCard(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarC6(t)
	conta := a.contaC6Corrente(t)

	campos := camposDoMismatchQA(t, enviarPelaAPI(t, a, conta.ID, "Fatura_C6.csv", fixtureC6Fatura(t)))

	assert.Equal(t, importer.ReasonExpectedCreditCard, campos["reason"])
	assert.Equal(t, string(importer.InstitutionC6), campos["detectedInstitution"],
		"a instituição detectada no arquivo do C6 é c6 — é o que o front usa para propor 'Cartão C6'")
	assert.Equal(t, string(importer.DocKindCardStatement), campos["detectedDocKind"])
	assert.NotContains(t, campos, "accountInstitution", "instituição bate: sem accountInstitution")
}

// TestC6ExtratoEmContaDeCartaoC6InformaExpectedBankAccount: extrato do C6 numa
// conta de cartão do C6 → expected_bank_account, detectedInstitution = c6.
func TestC6ExtratoEmContaDeCartaoC6InformaExpectedBankAccount(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarC6(t)
	cartao := a.contaC6Cartao(t)

	campos := camposDoMismatchQA(t, enviarPelaAPI(t, a, cartao.ID, "Extrato_C6.csv", fixtureC6Extrato(t)))

	assert.Equal(t, importer.ReasonExpectedBankAccount, campos["reason"])
	assert.Equal(t, string(importer.InstitutionC6), campos["detectedInstitution"])
	assert.Equal(t, string(importer.DocKindCheckingStatement), campos["detectedDocKind"])
	assert.NotContains(t, campos, "accountInstitution")
}

// TestC6FaturaEmContaNubankInformaWrongInstitution: fatura do C6 numa conta de
// cartão marcada como nubank. Instituição diverge e PRECEDE o tipo (que aqui até
// bateria: é fatura em cartão). O accountInstitution é nubank.
func TestC6FaturaEmContaNubankInformaWrongInstitution(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.habilitarC6(t)
	cartaoNubank := a.contaCartao(t) // nubank/credit_card

	campos := camposDoMismatchQA(t, enviarPelaAPI(t, a, cartaoNubank.ID, "Fatura_C6.csv", fixtureC6Fatura(t)))

	require.Equal(t, importer.ReasonWrongInstitution, campos["reason"],
		"instituição errada precede a divergência de tipo")
	assert.Equal(t, string(importer.InstitutionC6), campos["detectedInstitution"])
	assert.Equal(t, string(importer.DocKindCardStatement), campos["detectedDocKind"])
	assert.Equal(t, account.InstitutionNubank, campos["accountInstitution"])
}
