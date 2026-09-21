package importer_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/aiimport"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage/gormstore"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QA da E9c (spec 0010 §5.3 e §9, critérios 39 a 45): os invariantes HERDADOS
// de POST /transfers/detect e POST /transactions/auto-categorize, reexecutados
// no cenário COMPLETO da E9 — e não em unidade, onde cada rota já os prova.
//
// O cenário é o da tela: extratos de duas contas importados ANTES de qualquer
// palavra-chave (o Pix entre elas entra como despesa numa e receita na outra),
// uma fatura de cartão com uma despesa que TERIA espelho, palavras-chave de
// conta e de categoria gravadas por POST /ai/keyword-import/confirm, e então
// o plano da E9c em duas fases (spec 0010 §10.8): detect em todos os meses da
// janela, depois auto-categorize em todos os meses — tudo pela borda HTTP,
// sobre SQLite real.
//
// O que se afirma depois do fluxo inteiro:
//
//   - 39: a perna de par que também bateria com palavra de categoria termina
//     transfer_out/transfer_in com categoryId NULO — a prévia da categorização
//     medida antes das transferências a listava como categorizável;
//   - 40: o saldo das duas contas (e do cartão) não muda;
//   - 41: reimportar os mesmos extratos cai em duplicado_exato, linha a linha;
//   - 42: a despesa de fatura não vira perna nem espelho e o total da fatura
//     não muda;
//   - 43: a segunda rodada inteira converte 0 e categoriza 0;
//   - 45: candidata sem espelho sai como no_mirror e a contagem de lançamentos
//     da casa não muda.
//
// Os nomes das fixtures (QUORBIX, TRELNAV, MOSKARP, FIBLUNTO, DRAVEKO) foram
// conferidos contra o motor real com a semente de categorias em 21/09/2026:
// nenhum alcança 80 em nenhuma palavra de fábrica.

const (
	e9cMesJul = "2026-07"
	e9cMesAgo = "2026-08"
	e9cMesSet = "2026-09"
)

// importDeIA monta o serviço e o handler REAIS de POST /ai/keyword-import
// sobre o mesmo banco do harness. Os serviços de categoria e conta são
// remontados com o KeywordAppender — a escrita aditiva que o AppendKeywords
// exige e que o harness da importação não liga — sobre os MESMOS repositórios
// e o mesmo UnitOfWork, exatamente como cmd/api.
func (a *ambiente) importDeIA(t *testing.T) *aiimport.Handler {
	t.Helper()
	uow := gormstore.NewUnitOfWork(a.db)
	categorias := a.contadorDeCategorias.CategoryRepository
	catSvc := category.NewService(categorias, uow,
		category.WithClock(a.relogio.now),
		category.WithKeywordAppender(categorias),
	)
	householdSvc := household.NewService(gormstore.NewHouseholdRepository(a.db), gormstore.NewMembershipRepository(a.db))
	contaSvc := account.NewService(a.repoConta, householdSvc, uow,
		account.WithBalances(a.repoTx),
		account.WithClock(a.relogio.now),
		account.WithKeywordAppender(a.repoConta),
	)
	svc := aiimport.NewService(categorias, catSvc, a.repoConta, contaSvc, a.repoTx, uow, logging.Discard())
	return aiimport.NewHandler(svc, logging.Discard(), 0)
}

// confirmarImportDeIA roda POST /ai/keyword-import/confirm com o envelope
// literal e exige 200.
func confirmarImportDeIA(t *testing.T, a *ambiente, h *aiimport.Handler, corpoJSON string) aiimport.Report {
	t.Helper()
	r := requisicao(t, http.MethodPost, "/api/v1/ai/keyword-import/confirm",
		strings.NewReader(corpoJSON), identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Confirm(rec, r)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var rel aiimport.Report
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &rel))
	return rel
}

// detectarPelaAPI roda POST /transfers/detect pela borda e exige 200.
func detectarPelaAPI(t *testing.T, a *ambiente, mes string, dryRun bool) transaction.TransferDetectView {
	t.Helper()
	corpo := fmt.Sprintf(`{"month":%q,"dryRun":%t}`, mes, dryRun)
	r := requisicao(t, http.MethodPost, "/api/v1/transfers/detect", strings.NewReader(corpo),
		identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handlerLancamento().DetectTransfers(rec, r)
	require.Equal(t, http.StatusOK, rec.Code, "detect %s dryRun=%t: %s", mes, dryRun, rec.Body.String())
	var view transaction.TransferDetectView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	require.Equal(t, mes, view.Month, "eco do mês")
	return view
}

// categorizarPelaAPI roda POST /transactions/auto-categorize pela borda.
func categorizarPelaAPI(t *testing.T, a *ambiente, mes string, dryRun bool) transaction.AutoCategorizeView {
	t.Helper()
	corpo := fmt.Sprintf(`{"month":%q,"dryRun":%t}`, mes, dryRun)
	r := requisicao(t, http.MethodPost, "/api/v1/transactions/auto-categorize", strings.NewReader(corpo),
		identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	a.handlerLancamento().AutoCategorize(rec, r)
	require.Equal(t, http.StatusOK, rec.Code, "auto-categorize %s dryRun=%t: %s", mes, dryRun, rec.Body.String())
	var view transaction.AutoCategorizeView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	require.Equal(t, mes, view.Month, "eco do mês")
	return view
}

// reprocessarComoATela executa o plano da E9c (spec 0010 §10.8): fase 1 =
// detect em TODOS os meses da janela; fase 2 = auto-categorize em todos.
// Devolve os totais das respostas de EXECUÇÃO (nunca da prévia) e o registro
// da ordem, para o teste afirmar a sequência.
func reprocessarComoATela(t *testing.T, a *ambiente, meses []string) (pares, categorizados int64, ordem []string) {
	t.Helper()
	for _, mes := range meses {
		pares += detectarPelaAPI(t, a, mes, false).Paired
		ordem = append(ordem, "detect:"+mes)
	}
	for _, mes := range meses {
		categorizados += categorizarPelaAPI(t, a, mes, false).Categorized
		ordem = append(ordem, "categorize:"+mes)
	}
	return pares, categorizados, ordem
}

// csvExtratoNoMes é csvExtratoAPI com o mês escolhido e um prefixo de
// identificador próprio deste arquivo (a chave natural da deduplicação).
func csvExtratoNoMes(mes int, linhas ...linhaExtrato) []byte {
	var b strings.Builder
	b.WriteString("Data,Valor,Identificador,Descrição\n")
	for _, l := range linhas {
		fmt.Fprintf(&b, "%02d/%02d/2026,%s,e9c00000-e9c0-4e9c-8e9c-e9c0e9c0e9%s,%s\n",
			l.dia, mes, l.valor, l.idSufixo, l.descricao)
	}
	return []byte(b.String())
}

// lancamentosPorID indexa os lançamentos vivos da casa nos meses dados.
func (a *ambiente) lancamentosPorID(t *testing.T, meses ...string) map[string]transaction.Transaction {
	t.Helper()
	out := map[string]transaction.Transaction{}
	for _, mes := range meses {
		for _, l := range a.lancamentosDa(t, a.casa.ID, mes) {
			out[l.ID] = l
		}
	}
	return out
}

// unicoPorDescricaoEValor acha O lançamento com aquela descrição e valor —
// e exige que seja um só, porque a fixture foi desenhada assim.
func unicoPorDescricaoEValor(t *testing.T, todos map[string]transaction.Transaction, contaID, descricao string, cents int64) transaction.Transaction {
	t.Helper()
	var achados []transaction.Transaction
	for _, l := range todos {
		if l.AccountID == contaID && l.Description == descricao && l.AmountCents == cents {
			achados = append(achados, l)
		}
	}
	require.Len(t, achados, 1, "esperava exatamente um %q de %d na conta %s", descricao, cents, contaID)
	return achados[0]
}

// NÃO é paralelo, ao contrário da maioria deste pacote, e a medição é o
// motivo: o cenário abre um SQLite próprio com MaxOpenConns: 1 e faz três
// importações, um import de IA e doze chamadas de reprocessamento. Sozinho ele
// custa ~1 s sob `-race`; somado ao PICO de testes paralelos do pacote ele
// empurrou TestNadaEEscritoEmDisco (prazo de 15 s, ~11,6 s isolado sob `-race`)
// para além do prazo, com 500 em vez de 201 — medido em 21/09/2026. Sequencial,
// ele roda na fase em que aquele teste também roda, um de cada vez, e o custo
// que acrescenta é o dele mesmo.
func TestE9cReprocessamentoPreservaOsInvariantesHerdadosDasRotas(t *testing.T) {
	a := novoAmbiente(t)
	ia := a.importDeIA(t)
	janela := []string{e9cMesJul, e9cMesAgo, e9cMesSet}

	// --- montagem: duas contas correntes, um cartão e uma categoria sem palavra
	quorbix := a.conta(t, a.casa.ID, "Banco Quorbix", account.KindChecking, account.InstitutionOther)
	trelnav := a.conta(t, a.casa.ID, "Banco Trelnav", account.KindChecking, account.InstitutionOther)
	cartao := a.contaCartao(t)
	loja := a.categoriaComPalavras(t, "QA Loja", category.KindExpense)

	// --- 1) extratos importados ANTES de qualquer palavra-chave --------------
	//
	// Quorbix (agosto): a saída do Pix espelhado (q1), uma compra reconhecível
	// por palavra de categoria (q2) e um Pix SEM espelho (q3).
	extratoQ := csvExtratoNoMes(8,
		linhaExtrato{10, "-250.00", "q1", "Pix enviado - TRELNAV"},
		linhaExtrato{12, "-40.00", "q2", "MOSKARP LOJA CENTRO"},
		linhaExtrato{14, "-90.00", "q3", "Pix enviado - TRELNAV"},
	)
	// Trelnav (agosto): a entrada espelhada (t1), um Pix cujo ÚNICO espelho
	// possível é a despesa de fatura do cartão (t2), e duas linhas que nenhuma
	// palavra reconhece (t3, t4).
	extratoT := csvExtratoNoMes(8,
		linhaExtrato{10, "250.00", "t1", "Pix recebido - QUORBIX"},
		linhaExtrato{20, "77.00", "t2", "Pix recebido - QUORBIX"},
		linhaExtrato{15, "-30.00", "t3", "FIBLUNTO MENSAL"},
		linhaExtrato{22, "500.00", "t4", "DRAVEKO DEPOSITO"},
	)
	// Cartão (fatura de setembro, compra em 20/08): a despesa que TERIA espelho
	// em t2 — mesmo valor, mesmo dia, outra conta — se a fatura participasse.
	fatura := csvFaturaAPI(
		linhaFatura{data: "2026-08-20", titulo: "PIX TRELNAV CARTAO", valor: "77,00"},
		linhaFatura{data: "2026-08-21", titulo: "GLIMPO CARTAO", valor: "23,50"},
	)

	loteQ := loteDaResposta(t, enviarPelaAPI(t, a, quorbix.ID, "quorbix.csv", extratoQ))
	require.Zero(t, loteQ.Counts.InternalTransfer, "sem palavra de conta, nada parece transferência")
	require.Equal(t, 3, resultadoDaResposta(t, confirmarPelaAPI(t, a, loteQ.ID, `{"decisions":[]}`)).Imported)

	loteT := loteDaResposta(t, enviarPelaAPI(t, a, trelnav.ID, "trelnav.csv", extratoT))
	require.Zero(t, loteT.Counts.InternalTransfer)
	require.Zero(t, loteT.Counts.TransferAlreadyRegistered)
	require.Equal(t, 4, resultadoDaResposta(t, confirmarPelaAPI(t, a, loteT.ID, `{"decisions":[]}`)).Imported)

	loteC := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "Nubank_2026-09-13.csv", fatura))
	resC := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteC.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))
	require.Equal(t, 2, resC.Imported)

	todosAntes := a.lancamentosPorID(t, janela...)
	require.Len(t, todosAntes, 9, "3 + 4 + 2 lançamentos vivos na janela")
	for _, l := range todosAntes {
		require.Nil(t, l.CategoryID, "nada entra categorizado: %q", l.Description)
		require.Nil(t, l.TransferGroupID, "nada entra como transferência: %q", l.Description)
	}
	q1 := unicoPorDescricaoEValor(t, todosAntes, quorbix.ID, "Pix enviado - TRELNAV", 25000)
	q3 := unicoPorDescricaoEValor(t, todosAntes, quorbix.ID, "Pix enviado - TRELNAV", 9000)
	t1 := unicoPorDescricaoEValor(t, todosAntes, trelnav.ID, "Pix recebido - QUORBIX", 25000)
	t2 := unicoPorDescricaoEValor(t, todosAntes, trelnav.ID, "Pix recebido - QUORBIX", 7700)
	despesaDeFatura := unicoPorDescricaoEValor(t, todosAntes, cartao.ID, "PIX TRELNAV CARTAO", 7700)
	require.NotNil(t, despesaDeFatura.StatementID, "a despesa de fatura nasce ligada à fatura")
	require.Equal(t, e9cMesSet, despesaDeFatura.CompetenceMonth, "competência da fatura é o vencimento")

	saldosAntes := a.saldos(t)
	require.Equal(t, int64(-38000), saldosAntes[quorbix.ID])
	require.Equal(t, int64(79700), saldosAntes[trelnav.ID])

	atorFatura := cardstatement.Actor{HouseholdID: a.casa.ID, UserID: a.usuario.ID}
	faturas, err := a.stmtSvc.List(t.Context(), atorFatura, cardstatement.ListInput{AccountID: cartao.ID})
	require.NoError(t, err)
	require.Len(t, faturas.Items, 1)
	faturaAntes := faturas.Items[0]
	require.Equal(t, int64(10050), faturaAntes.TotalCents, "77,00 + 23,50")

	// --- 2) palavras-chave pelo import de IA (a resposta "da IA") ------------
	//
	// Conta: o nome de UMA vista do extrato da OUTRA. Categoria: «moskarp»
	// (a compra) e «pix enviado trelnav» — que bate com a perna q1 do par E
	// com q3, o Pix sem espelho. É o critério 39 armado de propósito.
	corpo := fmt.Sprintf(`{
	  "payload": {
	    "homefinanceKeywordImport": 1,
	    "categoryKeywords": [
	      {"categoryId": %q, "categoryPath": "QA Loja", "add": ["moskarp", "pix enviado trelnav"]}
	    ],
	    "accountKeywords": [
	      {"accountId": %q, "accountName": "Banco Trelnav", "add": ["trelnav"]},
	      {"accountId": %q, "accountName": "Banco Quorbix", "add": ["quorbix"]}
	    ],
	    "notes": "a IA explicando"
	  },
	  "fromMonth": %q,
	  "toMonth": %q,
	  "skipNewCategories": []
	}`, loja.ID, trelnav.ID, quorbix.ID, e9cMesJul, e9cMesSet)
	rel := confirmarImportDeIA(t, a, ia, corpo)
	require.Equal(t, 4, rel.Totals.Added, "%+v", rel)
	require.Zero(t, rel.Totals.Rejected, "%+v", rel)
	require.Equal(t, 9, rel.Totals.PeriodTransactions, "as 9 receitas/despesas vivas da janela, cartão incluído (é o universo da medição, não o da detecção)")

	// A gravação de palavra, sozinha, NÃO mexe em lançamento nenhum (§5).
	assert.Equal(t, todosAntes, a.lancamentosPorID(t, janela...), "palavra-chave nova não toca o que já está gravado")

	// --- 3) a prévia da tela: seis dryRun, e a sobreposição do critério 39 --
	previaDetect := map[string]transaction.TransferDetectView{}
	previaCategorize := map[string]transaction.AutoCategorizeView{}
	for _, mes := range janela {
		previaDetect[mes] = detectarPelaAPI(t, a, mes, true)
		previaCategorize[mes] = categorizarPelaAPI(t, a, mes, true)
	}
	assert.Equal(t, int64(1), previaDetect[e9cMesAgo].Paired, "q1 ↔ t1")
	assert.Equal(t, int64(2), previaDetect[e9cMesAgo].Unpaired, "q3 (90) e t2 (77, cujo espelho é fatura)")
	assert.Zero(t, previaDetect[e9cMesJul].Paired+previaDetect[e9cMesJul].Unpaired)
	assert.Zero(t, previaDetect[e9cMesSet].Paired, "a despesa de fatura não é candidata em setembro")
	assert.Zero(t, previaDetect[e9cMesSet].Unpaired, "nem aparece como sem par")

	// Antes das transferências, a categorização enxerga q1 (a perna do par)
	// como categorizável — é a medição que a tela mostra, e é por isso que a
	// nota da prévia avisa que o número real pode ser menor.
	previstos := map[string]string{}
	for _, it := range previaCategorize[e9cMesAgo].Items {
		previstos[it.ID] = it.CategoryID
	}
	assert.Equal(t, int64(3), previaCategorize[e9cMesAgo].Categorized, "q1, q2 e q3 na prévia")
	assert.Equal(t, loja.ID, previstos[q1.ID], "a perna do par TERIA categoria por «pix enviado trelnav»")
	assert.Equal(t, loja.ID, previstos[q3.ID])
	assert.Equal(t, int64(4), previaCategorize[e9cMesAgo].Unmatched, "t1, t2, t3, t4")
	assert.Zero(t, previaCategorize[e9cMesSet].Categorized, "nenhuma palavra bate com as linhas do cartão")

	// A prévia não escreveu nada.
	assert.Equal(t, todosAntes, a.lancamentosPorID(t, janela...), "dryRun não grava")

	// --- 4) a execução, na ordem da tela (§10.8) ------------------------------
	pares, categorizados, ordem := reprocessarComoATela(t, a, janela)
	assert.Equal(t, []string{
		"detect:2026-07", "detect:2026-08", "detect:2026-09",
		"categorize:2026-07", "categorize:2026-08", "categorize:2026-09",
	}, ordem)
	assert.Equal(t, int64(1), pares)
	assert.Equal(t, int64(2), categorizados, "q2 e q3 — q1 saiu do universo ao virar transferência")

	depois := a.lancamentosPorID(t, janela...)

	// 45: nenhuma perna inventada — a contagem de lançamentos não mudou, e as
	// candidatas sem espelho continuam exatamente como estavam (kind e conta).
	assert.Len(t, depois, len(todosAntes), "nenhum lançamento criado nem apagado")
	for _, id := range []string{q3.ID, t2.ID} {
		assert.Equal(t, todosAntes[id].Kind, depois[id].Kind, "candidata sem espelho não muda de kind")
		assert.Nil(t, depois[id].TransferGroupID, "candidata sem espelho não ganha grupo")
	}
	for _, item := range previaDetect[e9cMesAgo].UnpairedItems {
		assert.Equal(t, transaction.TransferUnpairedNoMirror, item.Reason)
	}
	semPar := map[string]bool{}
	for _, item := range previaDetect[e9cMesAgo].UnpairedItems {
		semPar[item.ID] = true
	}
	assert.Equal(t, map[string]bool{q3.ID: true, t2.ID: true}, semPar)

	// 39: o par virou transfer_out/transfer_in com categoryId NULO — apesar de
	// «pix enviado trelnav» bater em q1 — e a categorização real não o tocou.
	assert.Equal(t, transaction.KindTransferOut, depois[q1.ID].Kind)
	assert.Equal(t, transaction.KindTransferIn, depois[t1.ID].Kind)
	assert.Nil(t, depois[q1.ID].CategoryID, "transferência não tem categoria")
	assert.Nil(t, depois[t1.ID].CategoryID)
	require.NotNil(t, depois[q1.ID].TransferGroupID)
	require.NotNil(t, depois[t1.ID].TransferGroupID)
	assert.Equal(t, *depois[q1.ID].TransferGroupID, *depois[t1.ID].TransferGroupID, "mesmo grupo")
	// E o que NÃO é par recebeu a categoria — inclusive q3, que tem a mesma
	// descrição de q1: a diferença entre os dois é só o espelho.
	require.NotNil(t, depois[q3.ID].CategoryID)
	assert.Equal(t, loja.ID, *depois[q3.ID].CategoryID)
	q2 := unicoPorDescricaoEValor(t, depois, quorbix.ID, "MOSKARP LOJA CENTRO", 4000)
	require.NotNil(t, q2.CategoryID)
	assert.Equal(t, loja.ID, *q2.CategoryID)

	// Só kind e categoryId mudaram nas pernas: valor, data, competência,
	// descrição, origem, lote e chave de deduplicação são os mesmos (§5.3).
	for _, id := range []string{q1.ID, t1.ID} {
		antes, agora := todosAntes[id], depois[id]
		assert.Equal(t, antes.AmountCents, agora.AmountCents)
		assert.Equal(t, antes.OccurredOn, agora.OccurredOn)
		assert.Equal(t, antes.CompetenceMonth, agora.CompetenceMonth)
		assert.Equal(t, antes.Description, agora.Description)
		assert.Equal(t, antes.Source, agora.Source)
		assert.Equal(t, antes.ImportBatchID, agora.ImportBatchID)
		assert.Equal(t, antes.ExternalID, agora.ExternalID)
		assert.Equal(t, antes.DedupKey, agora.DedupKey, "a chave de deduplicação sobrevive à conversão")
		assert.Equal(t, antes.AccountID, agora.AccountID)
	}

	// 40: o saldo das duas contas (e do cartão) não mudou.
	saldosDepois := a.saldos(t)
	assert.Equal(t, saldosAntes[quorbix.ID], saldosDepois[quorbix.ID], "converter o par não move dinheiro em Quorbix")
	assert.Equal(t, saldosAntes[trelnav.ID], saldosDepois[trelnav.ID], "nem em Trelnav")
	assert.Equal(t, saldosAntes[cartao.ID], saldosDepois[cartao.ID])
	assert.Equal(t, somaDeSaldos(saldosAntes), somaDeSaldos(saldosDepois))

	// 42: a despesa de fatura continua despesa, ligada à mesma fatura, sem
	// categoria nem grupo — e o total da fatura é o mesmo.
	fat := depois[despesaDeFatura.ID]
	assert.Equal(t, transaction.KindExpense, fat.Kind)
	assert.Nil(t, fat.TransferGroupID)
	require.NotNil(t, fat.StatementID)
	assert.Equal(t, *despesaDeFatura.StatementID, *fat.StatementID)
	faturaDepois, err := a.stmtSvc.ByID(t.Context(), atorFatura, faturaAntes.ID)
	require.NoError(t, err)
	assert.Equal(t, faturaAntes.TotalCents, faturaDepois.TotalCents, "o total cobrado da fatura não muda")
	assert.Equal(t, faturaAntes.PaidCents, faturaDepois.PaidCents)
	// ...e t2, cujo único espelho possível era a fatura, ficou como receita.
	assert.Equal(t, transaction.KindIncome, depois[t2.ID].Kind)

	// 41: reimportar os MESMOS extratos cai em duplicado_exato — inclusive as
	// pernas convertidas, porque a chave de deduplicação foi preservada.
	reQ := loteDaResposta(t, enviarPelaAPI(t, a, quorbix.ID, "quorbix-de-novo.csv", extratoQ))
	assert.Equal(t, 3, reQ.Counts.DuplicateExact)
	assert.Zero(t, reQ.Counts.New)
	assert.Zero(t, reQ.Counts.InternalTransfer)
	assert.Zero(t, reQ.Counts.TransferAlreadyRegistered, "a perna já é DESTA conta: é duplicata, não vínculo")
	revisaoQ := revisarPelaAPI(t, a, reQ.ID)
	assert.Equal(t, 3, contarStatus(revisaoQ.Items, dedup.StatusDuplicateExact))
	pernaQ := linhaComDescricao(t, revisaoQ.Items, "Pix enviado - TRELNAV")
	assert.Equal(t, string(dedup.StatusDuplicateExact), pernaQ.Status)

	reT := loteDaResposta(t, enviarPelaAPI(t, a, trelnav.ID, "trelnav-de-novo.csv", extratoT))
	assert.Equal(t, 4, reT.Counts.DuplicateExact)
	assert.Zero(t, reT.Counts.New)
	assert.Zero(t, reT.Counts.InternalTransfer)
	assert.Zero(t, reT.Counts.TransferAlreadyRegistered)
	assert.Zero(t, resultadoDaResposta(t, confirmarPelaAPI(t, a, reT.ID, `{"decisions":[]}`)).Imported)
	assert.Len(t, a.lancamentosPorID(t, janela...), len(todosAntes), "a reimportação não gravou nada")

	// 43: a segunda rodada inteira converte 0 e categoriza 0, e o banco fica
	// exatamente como estava depois da primeira.
	pares2, categorizados2, _ := reprocessarComoATela(t, a, janela)
	assert.Zero(t, pares2, "idempotente: nada a converter")
	assert.Zero(t, categorizados2, "idempotente: nada a categorizar")
	assert.Equal(t, depois, a.lancamentosPorID(t, janela...), "a segunda execução não muda uma linha")
	// E a prévia nova diz o mesmo — é o "volta com 0" que a tela promete.
	for _, mes := range janela {
		assert.Zero(t, detectarPelaAPI(t, a, mes, true).Paired, mes)
		assert.Zero(t, categorizarPelaAPI(t, a, mes, true).Categorized, mes)
	}
	assert.Equal(t, int64(2), detectarPelaAPI(t, a, e9cMesAgo, true).Unpaired, "as sem espelho continuam sem espelho")
	assert.Equal(t, saldosDepois, a.saldos(t))
}
