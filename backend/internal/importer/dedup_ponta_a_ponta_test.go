package importer_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Os cinco cenários de deduplicação da §10.5 da spec 0004 — mais o caso
// patológico da §4.3 —, exercitados PONTA A PONTA PELA API.
//
// Por que repetir aqui o que dedup_test.go já cobre em unidade: o teste de
// unidade prova a regra; este prova a FIAÇÃO. Entre a regra e o banco existem
// o multipart, o parser, a janela carregada do repositório, o recálculo dentro
// da transação do confirm e o índice único. Cada um deles pode desligar a
// garantia sozinho sem que um único teste de unidade fique vermelho.
//
// A exigência do usuário, literal: NUNCA importar linha duplicada, e NUNCA
// engolir lançamento real. O segundo é o que estes testes vigiam com mais
// cuidado — duplicata o usuário vê e apaga; linha sumida ele descobre três
// meses depois, conferindo o extrato.

// --- ferramentas de requisição --------------------------------------------

// enviarPelaAPI roda a fase 1 pelo HANDLER, com multipart de verdade.
func enviarPelaAPI(t *testing.T, a *ambiente, contaID, nomeArquivo string, conteudo []byte, extras ...parte) *httptest.ResponseRecorder {
	t.Helper()

	partes := []parte{
		{nome: importer.PartFile, arquivo: nomeArquivo, conteudo: conteudo},
		{nome: importer.PartAccountID, conteudo: []byte(contaID)},
	}
	partes = append(partes, extras...)

	corpo, tipo := montarMultipart(t, partes...)
	r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", tipo)

	rec := httptest.NewRecorder()
	a.handler(t).Create(rec, r)
	return rec
}

// loteDaResposta exige 201 e devolve o lote decodificado.
func loteDaResposta(t *testing.T, rec *httptest.ResponseRecorder) importer.BatchView {
	t.Helper()
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var view importer.BatchView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	return view
}

// revisarPelaAPI roda GET /imports/{id} e devolve a revisão inteira.
func revisarPelaAPI(t *testing.T, a *ambiente, loteID string) importer.PreviewView {
	t.Helper()

	r := requisicao(t, http.MethodGet, "/api/v1/imports/"+loteID+"?limit=200", nil,
		identidade(a.casa.ID, a.usuario.ID))
	r.SetPathValue("id", loteID)

	rec := httptest.NewRecorder()
	a.handler(t).Get(rec, r)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var view importer.PreviewView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	return view
}

// confirmarPelaAPI roda POST /imports/{id}/confirm com o corpo JSON literal —
// literal de propósito: é assim que o cliente manda, e é a forma que o DTO
// precisa recusar ou aceitar.
func confirmarPelaAPI(t *testing.T, a *ambiente, loteID, corpoJSON string) *httptest.ResponseRecorder {
	t.Helper()

	r := requisicao(t, http.MethodPost, "/api/v1/imports/"+loteID+"/confirm",
		strings.NewReader(corpoJSON), identidade(a.casa.ID, a.usuario.ID))
	r.Header.Set("Content-Type", "application/json")
	r.SetPathValue("id", loteID)

	rec := httptest.NewRecorder()
	a.handler(t).Confirm(rec, r)
	return rec
}

// resultadoDaResposta exige 200 e devolve o resultado do confirm.
func resultadoDaResposta(t *testing.T, rec *httptest.ResponseRecorder) importer.ResultView {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var res importer.ResultView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	return res
}

// listarPelaAPI roda GET /transactions?month=... e devolve a listagem.
func listarPelaAPI(t *testing.T, a *ambiente, mes string) transaction.ListView {
	t.Helper()

	r := requisicao(t, http.MethodGet, "/api/v1/transactions?month="+mes+"&limit=100", nil,
		identidade(a.casa.ID, a.usuario.ID))
	rec := httptest.NewRecorder()
	a.handlerLancamento().List(rec, r)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var view transaction.ListView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	return view
}

// contarDescricao conta quantas linhas da listagem têm exatamente aquela
// descrição. É a pergunta que importa nos cenários B e F: "quantos cafés de R$
// 11,00 existem de verdade?".
func contarDescricao(itens []transaction.View, descricao string) int {
	n := 0
	for _, i := range itens {
		if i.Description == descricao {
			n++
		}
	}
	return n
}

// contarStatus conta as linhas da revisão por status.
func contarStatus(itens []importer.RowView, status dedup.Status) int {
	n := 0
	for _, i := range itens {
		if i.Status == string(status) {
			n++
		}
	}
	return n
}

// --- montadores de CSV -----------------------------------------------------

// csvExtratoAPI monta um extrato Nubank sintético.
//
// Cada linha vem como (dia de agosto, valor com ponto decimal, sufixo do
// identificador, descrição).
type linhaExtrato struct {
	dia       int
	valor     string
	idSufixo  string
	descricao string
}

func csvExtratoAPI(linhas ...linhaExtrato) []byte {
	var b bytes.Buffer
	b.WriteString("Data,Valor,Identificador,Descrição\n")
	for _, l := range linhas {
		fmt.Fprintf(&b, "%02d/08/2026,%s,11111111-1111-4111-8111-1111111111%s,%s\n",
			l.dia, l.valor, l.idSufixo, l.descricao)
	}
	return b.Bytes()
}

// linhaFatura é uma linha da fatura Nubank: data completa, título e valor em
// formato pt-BR. Positivo é SAÍDA nesta convenção — o oposto do extrato.
type linhaFatura struct {
	data   string
	titulo string
	valor  string
}

func csvFaturaAPI(linhas ...linhaFatura) []byte {
	var b bytes.Buffer
	b.WriteString("date,title,amount\n")
	for _, l := range linhas {
		fmt.Fprintf(&b, "%s,%s,%q\n", l.data, l.titulo, l.valor)
	}
	return b.Bytes()
}

// confirmacaoDeFatura é o bloco `statement` obrigatório em lote de fatura.
// Competência 2026-09 porque a competência é o mês do VENCIMENTO (D2).
const confirmacaoDeFatura = `"statement":{"competenceMonth":"2026-09","closingDate":"2026-08-31","dueDate":"2026-09-10"}`

// --- cenário A -------------------------------------------------------------

// TestPontaAPontaA_ReimportarOMesmoArquivo: 100% marcadas, ZERO importadas.
func TestPontaAPontaA_ReimportarOMesmoArquivo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	primeiro := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "NU_2026-08.csv", fixtureExtrato(t)))
	res1 := resultadoDaResposta(t, confirmarPelaAPI(t, a, primeiro.ID, `{"decisions":[]}`))
	require.Equal(t, 12, res1.Imported)

	// O MESMO arquivo, de novo, byte a byte.
	segundo := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "NU_2026-08.csv", fixtureExtrato(t)))
	require.NotNil(t, segundo.Counts)
	assert.Zero(t, segundo.Counts.New, "nenhuma linha pode voltar como nova")
	assert.Equal(t, 12, segundo.Counts.DuplicateExact)
	assert.Equal(t, 1, segundo.Counts.CardPayment, "o pagamento de fatura continua barrado, por outro motivo")

	revisao := revisarPelaAPI(t, a, segundo.ID)
	require.Len(t, revisao.Items, 13)
	for _, linha := range revisao.Items {
		assert.Equal(t, importer.ActionSkip, linha.DefaultAction,
			"100%% das linhas vêm barradas por default (seq %d)", linha.Seq)
	}

	res2 := resultadoDaResposta(t, confirmarPelaAPI(t, a, segundo.ID, `{"decisions":[]}`))
	assert.Zero(t, res2.Imported, "ZERO importadas na reimportação")
	assert.Equal(t, 13, res2.Skipped)
	assert.Zero(t, res2.Restored)

	lista := listarPelaAPI(t, a, "2026-08")
	assert.Len(t, lista.Items, 12, "o total não se mexe: nenhuma duplicata, nenhuma perda")
}

// --- cenário B -------------------------------------------------------------

// TestPontaAPontaB_CompraLegitimaRepetida é o cenário que mais preocupa: duas
// linhas IDÊNTICAS que são dois gastos reais.
//
// As duas entram na 1ª importação. Na 2ª, as duas são barradas. NENHUMA SOME,
// NUNCA.
func TestPontaAPontaB_CompraLegitimaRepetida(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)

	primeiro := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "fatura.csv", fixtureFatura(t)))
	require.NotNil(t, primeiro.Counts)
	assert.Equal(t, 1, primeiro.Counts.RepeatedInFile,
		"o par idêntico é 2ª ocorrência, e 2ª ocorrência ENTRA por default")

	res1 := resultadoDaResposta(t, confirmarPelaAPI(t, a,
		primeiro.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))
	assert.Equal(t, 14, res1.Imported)

	lista := listarPelaAPI(t, a, "2026-09")
	assert.Equal(t, 2, contarDescricao(lista.Items, "Cafe Exemplo"),
		"OS DOIS cafés entram na primeira importação")

	// Segunda importação do mesmo arquivo: as duas linhas do par são barradas,
	// e nenhuma das duas já gravadas desaparece.
	segundo := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "fatura.csv", fixtureFatura(t)))
	require.NotNil(t, segundo.Counts)
	assert.Equal(t, 14, segundo.Counts.DuplicateExact)
	assert.Zero(t, segundo.Counts.New)
	assert.Zero(t, segundo.Counts.RepeatedInFile,
		"na 2ª vez o banco JÁ TEM as duas ocorrências: nenhuma é 'repetida no arquivo'")

	revisao := revisarPelaAPI(t, a, segundo.ID)
	cafes := 0
	for _, linha := range revisao.Items {
		if linha.Description != nil && *linha.Description == "Cafe Exemplo" {
			cafes++
			assert.Equal(t, string(dedup.StatusDuplicateExact), linha.Status)
			assert.Equal(t, importer.ActionSkip, linha.DefaultAction)
			assert.Empty(t, linha.AllowedActions,
				"duplicado_exato não é liberável: o índice único recusaria o INSERT de qualquer jeito")
		}
	}
	assert.Equal(t, 2, cafes, "as DUAS linhas do par aparecem na revisão — nenhuma sumiu do staging")

	res2 := resultadoDaResposta(t, confirmarPelaAPI(t, a,
		segundo.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))
	assert.Zero(t, res2.Imported)

	depois := listarPelaAPI(t, a, "2026-09")
	assert.Equal(t, 2, contarDescricao(depois.Items, "Cafe Exemplo"),
		"continuam DOIS cafés: nem virou três, nem virou um")
	assert.Len(t, depois.Items, 14)
}

// --- cenário C -------------------------------------------------------------

// TestPontaAPontaC_PeriodoRepartidoComChaveNatural: arquivo A cobre 01–15 e
// arquivo B cobre 01–31. Só 16–31 entram.
func TestPontaAPontaC_PeriodoRepartidoComChaveNatural(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	primeiraQuinzena := []linhaExtrato{
		{dia: 3, valor: "-50.00", idSufixo: "01", descricao: "Mercado Exemplo"},
		{dia: 7, valor: "-19.90", idSufixo: "02", descricao: "Farmacia Exemplo"},
		{dia: 12, valor: "1200.00", idSufixo: "03", descricao: "Salario Exemplo"},
		{dia: 15, valor: "-33.00", idSufixo: "04", descricao: "Posto Exemplo"},
	}
	segundaQuinzena := []linhaExtrato{
		{dia: 18, valor: "-72.40", idSufixo: "05", descricao: "Padaria Exemplo"},
		{dia: 22, valor: "-15.00", idSufixo: "06", descricao: "Livraria Exemplo"},
		{dia: 29, valor: "-210.00", idSufixo: "07", descricao: "Seguro Exemplo"},
	}

	arquivoA := csvExtratoAPI(primeiraQuinzena...)
	arquivoB := csvExtratoAPI(append(append([]linhaExtrato{}, primeiraQuinzena...), segundaQuinzena...)...)

	loteA := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "A_01-15.csv", arquivoA))
	resA := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, `{"decisions":[]}`))
	require.Equal(t, 4, resA.Imported)

	loteB := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "B_01-31.csv", arquivoB))
	require.NotNil(t, loteB.Counts)
	assert.Equal(t, 4, loteB.Counts.DuplicateExact, "as quatro linhas de 01–15 voltam marcadas")
	assert.Equal(t, 3, loteB.Counts.New, "e só as três de 16–31 são novas")

	resB := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, `{"decisions":[]}`))
	assert.Equal(t, 3, resB.Imported)
	assert.Equal(t, 4, resB.Skipped)

	lista := listarPelaAPI(t, a, "2026-08")
	assert.Len(t, lista.Items, 7, "quatro mais três, e nada repetido")
	for _, esperada := range append(append([]linhaExtrato{}, primeiraQuinzena...), segundaQuinzena...) {
		assert.Equal(t, 1, contarDescricao(lista.Items, esperada.descricao),
			"%q precisa existir exatamente uma vez", esperada.descricao)
	}
}

// TestPontaAPontaC_PeriodoRepartidoSemChaveNatural é o mesmo cenário no
// documento que NÃO tem identificador por linha — a fatura.
//
// É o caso que a §4.3 diz que só funciona se o ordinal for contado contra o
// BANCO, e não contra o arquivo. Contado por arquivo, este teste passaria por
// acidente com dois arquivos e falharia com três.
func TestPontaAPontaC_PeriodoRepartidoSemChaveNatural(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)

	primeiraQuinzena := []linhaFatura{
		{data: "2026-08-03", titulo: "Mercado Exemplo", valor: "50,00"},
		{data: "2026-08-07", titulo: "Farmacia Exemplo", valor: "19,90"},
		{data: "2026-08-12", titulo: "Livraria Exemplo", valor: "42,10"},
	}
	segundaQuinzena := []linhaFatura{
		{data: "2026-08-19", titulo: "Padaria Exemplo", valor: "7,50"},
		{data: "2026-08-26", titulo: "Posto Exemplo", valor: "180,00"},
	}

	arquivoA := csvFaturaAPI(primeiraQuinzena...)
	arquivoB := csvFaturaAPI(append(append([]linhaFatura{}, primeiraQuinzena...), segundaQuinzena...)...)

	loteA := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "A.csv", arquivoA))
	resA := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))
	require.Equal(t, 3, resA.Imported)

	loteB := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "B.csv", arquivoB))
	require.NotNil(t, loteB.Counts)
	assert.Equal(t, 3, loteB.Counts.DuplicateExact)
	assert.Equal(t, 2, loteB.Counts.New)

	resB := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))
	assert.Equal(t, 2, resB.Imported)

	// E o TERCEIRO arquivo, que é onde a contagem por arquivo quebraria: ele
	// cobre o mesmo mês inteiro de novo, e nada pode entrar.
	loteC := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "C.csv", arquivoB))
	require.NotNil(t, loteC.Counts)
	assert.Equal(t, 5, loteC.Counts.DuplicateExact)
	assert.Zero(t, loteC.Counts.New)

	resC := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteC.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))
	assert.Zero(t, resC.Imported)

	lista := listarPelaAPI(t, a, "2026-09")
	assert.Len(t, lista.Items, 5)
}

// --- cenário D -------------------------------------------------------------

// TestPontaAPontaD_DescricaoQueMudouEntreDownloads: o banco enriqueceu o nome
// do estabelecimento entre um download e outro. A chave derivada NÃO casa — e é
// a marcação fraca que precisa pegar e barrar.
func TestPontaAPontaD_DescricaoQueMudouEntreDownloads(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)

	loteA := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "A.csv", csvFaturaAPI(
		linhaFatura{data: "2026-08-10", titulo: "Padaria Esquina", valor: "25,00"},
		linhaFatura{data: "2026-08-11", titulo: "Livraria Exemplo", valor: "42,10"},
	)))
	resA := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))
	require.Equal(t, 2, resA.Imported)

	// Mesmo dia, mesmo valor, descrição diferente.
	loteB := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "B.csv", csvFaturaAPI(
		linhaFatura{data: "2026-08-10", titulo: "Padaria da Esquina Matriz", valor: "25,00"},
		linhaFatura{data: "2026-08-11", titulo: "Livraria Exemplo", valor: "42,10"},
	)))
	require.NotNil(t, loteB.Counts)
	assert.Equal(t, 1, loteB.Counts.PossibleDup,
		"a chave derivada não casa; quem pega é a marcação fraca (conta, valor, data ±3 dias)")
	assert.Equal(t, 1, loteB.Counts.DuplicateExact, "a linha que não mudou continua sendo pega pela chave")
	assert.Zero(t, loteB.Counts.New)

	revisao := revisarPelaAPI(t, a, loteB.ID)
	var marcada importer.RowView
	for _, linha := range revisao.Items {
		if linha.Status == string(dedup.StatusPossibleDuplicate) {
			marcada = linha
		}
	}
	require.NotEmpty(t, marcada.ID)
	assert.Equal(t, importer.ActionSkip, marcada.DefaultAction, "barrada por DEFAULT")
	assert.Contains(t, marcada.AllowedActions, importer.ActionImport, "mas liberável: é heurística, não veredito")
	require.NotNil(t, marcada.MatchTransactionID, "a revisão aponta a linha existente que motivou a marcação")

	// Sem decisão, não entra.
	resB := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))
	assert.Zero(t, resB.Imported)
	assert.Len(t, listarPelaAPI(t, a, "2026-09").Items, 2)
}

// --- cenário E -------------------------------------------------------------

// TestPontaAPontaE_LinhaMarcadaSoEntraComDecisaoExplicita cobre a exigência
// escrita no contrato: linha marcada só entra com `action: "import"`.
func TestPontaAPontaE_LinhaMarcadaSoEntraComDecisaoExplicita(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)

	loteA := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "A.csv", csvFaturaAPI(
		linhaFatura{data: "2026-08-10", titulo: "Padaria Esquina", valor: "25,00"},
	)))
	_ = resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))

	loteB := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "B.csv", csvFaturaAPI(
		linhaFatura{data: "2026-08-10", titulo: "Padaria da Esquina Matriz", valor: "25,00"},
	)))
	revisao := revisarPelaAPI(t, a, loteB.ID)
	require.Len(t, revisao.Items, 1)
	marcada := revisao.Items[0]
	require.Equal(t, string(dedup.StatusPossibleDuplicate), marcada.Status)

	// 1) Corpo vazio: a linha marcada NÃO entra.
	semDecisao := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID,
		`{"decisions":[],`+confirmacaoDeFatura+`}`))
	require.Zero(t, semDecisao.Imported)
	require.Len(t, listarPelaAPI(t, a, "2026-09").Items, 1)

	// 2) Com a decisão explícita, num lote novo (o anterior já foi committed):
	//    entra como ocorrência nova, sem apagar a existente.
	loteC := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "C.csv", csvFaturaAPI(
		linhaFatura{data: "2026-08-10", titulo: "Padaria da Esquina Matriz", valor: "25,00"},
	)))
	linhaC := revisarPelaAPI(t, a, loteC.ID).Items[0]
	require.Equal(t, string(dedup.StatusPossibleDuplicate), linhaC.Status)

	corpo := fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"import"}],%s}`, linhaC.ID, confirmacaoDeFatura)
	comDecisao := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteC.ID, corpo))
	assert.Equal(t, 1, comDecisao.Imported, "com a decisão explícita, entra")

	lista := listarPelaAPI(t, a, "2026-09")
	assert.Len(t, lista.Items, 2)
	assert.Equal(t, 1, contarDescricao(lista.Items, "Padaria Esquina"), "a existente continua lá")
	assert.Equal(t, 1, contarDescricao(lista.Items, "Padaria da Esquina Matriz"))
}

// --- o caso patológico -----------------------------------------------------

// TestPontaAPontaF_UmCafeNoArquivoADoisNoB é o caso que a §4.3 chama de
// patológico, e é o motivo de o ordinal existir.
//
// Arquivo A tem UM café de R$ 11,00 em 05/08. Arquivo B tem DOIS — o segundo
// caiu depois do download de A. A primeira linha de B tem de ser marcada; a
// SEGUNDA tem de entrar. Sem ordinal, as duas seriam barradas e um gasto real
// sumiria sem deixar rastro.
func TestPontaAPontaF_UmCafeNoArquivoADoisNoB(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)

	cafe := linhaFatura{data: "2026-08-05", titulo: "Cafe Exemplo", valor: "11,00"}
	vizinha := linhaFatura{data: "2026-08-06", titulo: "Livraria Exemplo", valor: "42,10"}

	loteA := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "A.csv", csvFaturaAPI(cafe, vizinha)))
	resA := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))
	require.Equal(t, 2, resA.Imported)
	require.Equal(t, 1, contarDescricao(listarPelaAPI(t, a, "2026-09").Items, "Cafe Exemplo"))

	// Arquivo B: o MESMO café duas vezes.
	loteB := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "B.csv", csvFaturaAPI(cafe, cafe, vizinha)))
	require.NotNil(t, loteB.Counts)
	assert.Equal(t, 2, loteB.Counts.DuplicateExact, "o café #1 e a livraria já existem")
	assert.Equal(t, 1, loteB.Counts.RepeatedInFile, "o café #2 é ocorrência NOVA, e entra por default")
	assert.Zero(t, loteB.Counts.New)

	revisao := revisarPelaAPI(t, a, loteB.ID)
	require.Len(t, revisao.Items, 3)

	// A ordem importa: a PRIMEIRA linha de café é a barrada, a SEGUNDA é a que
	// entra. Trocar as duas daria o mesmo total e o significado errado.
	assert.Equal(t, string(dedup.StatusDuplicateExact), revisao.Items[0].Status)
	assert.Equal(t, importer.ActionSkip, revisao.Items[0].DefaultAction)
	assert.Equal(t, string(dedup.StatusRepeatedInFile), revisao.Items[1].Status)
	assert.Equal(t, importer.ActionImport, revisao.Items[1].DefaultAction)

	resB := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))
	assert.Equal(t, 1, resB.Imported, "UMA linha entra: o segundo café")
	assert.Equal(t, 2, resB.Skipped)
	assert.Zero(t, resB.Blocked)

	lista := listarPelaAPI(t, a, "2026-09")
	assert.Equal(t, 2, contarDescricao(lista.Items, "Cafe Exemplo"),
		"DOIS cafés: o gasto real não sumiu, e nenhuma duplicata nasceu")
	assert.Len(t, lista.Items, 3)

	// E a terceira importação do arquivo B não acrescenta um terceiro café: o
	// banco agora tem duas ocorrências, e as duas linhas colidem.
	loteC := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "C.csv", csvFaturaAPI(cafe, cafe, vizinha)))
	require.NotNil(t, loteC.Counts)
	assert.Equal(t, 3, loteC.Counts.DuplicateExact)
	assert.Zero(t, loteC.Counts.RepeatedInFile)

	resC := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteC.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))
	assert.Zero(t, resC.Imported)
	assert.Equal(t, 2, contarDescricao(listarPelaAPI(t, a, "2026-09").Items, "Cafe Exemplo"))
}

// --- sinais (critérios 3 e 4) ----------------------------------------------

// TestPontaAPontaSinalDoExtrato é o critério 3, e ele FALHA se algum sinal
// inverter: a lista abaixo é golden, linha a linha.
func TestPontaAPontaSinalDoExtrato(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "NU_2026-08.csv", fixtureExtrato(t)))
	assert.Equal(t, 13, lote.RowCount)

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	require.Equal(t, 12, res.Imported)
	require.Equal(t, 1, res.Skipped, "a linha 'Pagamento de fatura' é barrada por default")

	lista := listarPelaAPI(t, a, "2026-08")
	require.Len(t, lista.Items, 12)

	// No EXTRATO, negativo é saída. Cada linha esperada por (data, tipo, valor).
	esperado := []struct {
		data  string
		kind  string
		valor int64
		desc  string
	}{
		{"2026-08-04", transaction.KindExpense, 2000, "Pix enviado - Fulano de Tal Silva"},
		{"2026-08-05", transaction.KindExpense, 18707, "Pix enviado - ENERGIA EXEMPLO S.A."},
		{"2026-08-07", transaction.KindIncome, 160000, "Pix recebido - BELTRANA DE SOUZA"},
		{"2026-08-07", transaction.KindIncome, 85000, "Resgate RDB"},
		{"2026-08-12", transaction.KindExpense, 1100, "Pix enviado - PADARIA EXEMPLO LTDA"},
		{"2026-08-12", transaction.KindExpense, 1100, "Pix enviado - PADARIA EXEMPLO LTDA"},
		{"2026-08-25", transaction.KindIncome, 1028757, "Resgate RDB"},
		{"2026-08-25", transaction.KindExpense, 300000, "Pix enviado - CICRANO EXEMPLO DOS SANTOS"},
		{"2026-08-27", transaction.KindExpense, 13992, "Pix enviado - POSTO EXEMPLO LTDA."},
		{"2026-08-30", transaction.KindExpense, 2900, "Pix enviado - LOJA EXEMPLO"},
		{"2026-08-30", transaction.KindIncome, 2900, "Pix reembolso recebido - LOJA EXEMPLO"},
		{"2026-08-31", transaction.KindExpense, 500000, "Pix enviado - CICRANO EXEMPLO DOS SANTOS"},
	}

	obtido := make([]string, 0, len(lista.Items))
	for _, i := range lista.Items {
		obtido = append(obtido, fmt.Sprintf("%s|%s|%d|%s", i.OccurredOn, i.Kind, i.AmountCents, i.Description))
	}
	querido := make([]string, 0, len(esperado))
	for _, e := range esperado {
		querido = append(querido, fmt.Sprintf("%s|%s|%d|%s", e.data, e.kind, e.valor, e.desc))
	}
	assert.ElementsMatch(t, querido, obtido,
		"sinal invertido no extrato é o defeito que mais custa: ele não aparece na tela, aparece no saldo")

	receitas, despesas := 0, 0
	for _, i := range lista.Items {
		switch i.Kind {
		case transaction.KindIncome:
			receitas++
		case transaction.KindExpense:
			despesas++
		}
	}
	assert.Equal(t, 4, receitas)
	assert.Equal(t, 8, despesas)
	assert.EqualValues(t, 1276657, lista.Summary.IncomeCents)
	assert.EqualValues(t, 839799, lista.Summary.ExpenseCents)
	assert.Equal(t, lista.Summary.IncomeCents-lista.Summary.ExpenseCents, lista.Summary.NetCents)
}

// TestPontaAPontaSinalDaFatura é o critério 4: 13 despesas, 1 crédito e 1
// pagamento ignorado por default.
//
// A convenção da FATURA é a OPOSTA da do extrato — positivo é saída. Um teste
// que só contasse linhas passaria com os sinais trocados; este conta despesa e
// receita separadamente e confere o total.
func TestPontaAPontaSinalDaFatura(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "fatura.csv", fixtureFatura(t)))
	assert.Equal(t, 15, lote.RowCount)

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID,
		`{"decisions":[],`+confirmacaoDeFatura+`}`))
	require.Equal(t, 14, res.Imported)
	require.Equal(t, 1, res.Skipped)
	require.NotNil(t, res.StatementID)

	lista := listarPelaAPI(t, a, "2026-09")
	require.Len(t, lista.Items, 14)

	despesas, receitas := 0, 0
	var creditos []string
	for _, i := range lista.Items {
		switch i.Kind {
		case transaction.KindExpense:
			despesas++
		case transaction.KindIncome:
			receitas++
			creditos = append(creditos, i.Description)
		default:
			t.Fatalf("fatura não pode gerar %q", i.Kind)
		}
		assert.Positive(t, i.AmountCents, "o valor gravado é sempre positivo; o sinal vive no kind")
		require.NotNil(t, i.StatementID)
		assert.Equal(t, *res.StatementID, *i.StatementID)
		assert.Equal(t, "2026-09", i.CompetenceMonth, "competência é o mês do VENCIMENTO (D2)")
	}

	assert.Equal(t, 13, despesas, "13 despesas: positivo na fatura é SAÍDA")
	assert.Equal(t, 1, receitas, "1 crédito — o 'Ajuste a crédito', que é negativo na fatura")
	assert.Equal(t, []string{"Ajuste a crédito"}, creditos)

	// Soma conferida contra o arquivo: 33,70 + 7,95 + 26,98 + 22,89 + 14,10 +
	// 11,00 + 11,00 + 11,00 + 60,00 + 130,00 + 40,85 + 6,02 + 22,64.
	assert.EqualValues(t, 39813, lista.Summary.ExpenseCents)
	assert.EqualValues(t, 5381, lista.Summary.IncomeCents)

	// O "Pagamento recebido" NÃO entrou: ele é pagamento de fatura, e sem
	// decisão explícita ele fica de fora.
	assert.Zero(t, contarDescricao(lista.Items, "Pagamento recebido"))
}

// --- a garantia é do banco (critério 6) ------------------------------------

// TestPontaAPontaIndiceUnicoBloqueiaSemNuncaDarQuinhentos prova o critério 6
// pelo caminho que ele descreve: DOIS confirms do MESMO conteúdo, ao mesmo
// tempo, em lotes diferentes.
//
// A análise da fase 1 é só um palpite — entre o envio e a confirmação o outro
// morador pode ter importado o mesmo arquivo. Os dois confirms calculam o mesmo
// ordinal; o índice único derruba o segundo INSERT, e o serviço tem de traduzir
// isso em "linha bloqueada", NUNCA em 500.
func TestPontaAPontaIndiceUnicoBloqueiaSemNuncaDarQuinhentos(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	// Os DOIS lotes são analisados ANTES de qualquer confirm: é isso que faz os
	// dois acharem que as linhas são novas.
	loteA := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "A.csv", fixtureExtrato(t)))
	loteB := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "B.csv", fixtureExtrato(t)))
	require.NotNil(t, loteB.Counts)
	require.Equal(t, 12, loteB.Counts.New, "no momento da análise, o banco ainda está vazio")

	primeiro := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, `{"decisions":[]}`))
	require.Equal(t, 12, primeiro.Imported)

	rec := confirmarPelaAPI(t, a, loteB.ID, `{"decisions":[]}`)
	require.NotEqual(t, http.StatusInternalServerError, rec.Code,
		"colisão do índice único é linha bloqueada, jamais 500")
	segundo := resultadoDaResposta(t, rec)

	assert.Zero(t, segundo.Imported)
	assert.Equal(t, importer.BatchStatusCommitted, segundo.Status, "o lote não é derrubado pela colisão")

	// As 12 linhas viram `blocked` ou `skipped` — o que não pode acontecer é
	// elas entrarem.
	assert.Equal(t, 13, segundo.Blocked+segundo.Skipped)
	for _, b := range segundo.BlockedRows {
		assert.NotEmpty(t, b.Reason, "o motivo vai como CÓDIGO, e vai sempre")
		assert.NotContains(t, b.Reason, " ", "é código de vocabulário do servidor, não frase")
	}

	lista := listarPelaAPI(t, a, "2026-08")
	assert.Len(t, lista.Items, 12, "UM conjunto de lançamentos, nunca dois")
}

// --- cenário G: a data que mudou entre downloads --------------------------

// TestPontaAPontaG_MesmaChaveNaturalComDataDistante é o cenário que a revisão
// de segurança levantou, e está escrito AQUI porque ele é de deduplicação, não
// de configuração.
//
// O mecanismo, em uma frase: a chave NATURAL não embute a data, mas a JANELA
// carregada do banco (WindowForDedup) filtra por data com folga de
// transaction.DedupWindowDays. Se a data da mesma transação mudar mais do que
// essa folga entre dois downloads — estorno relançado, ajuste do banco, arquivo
// de período diferente —, a gêmea já gravada fica FORA da janela, a análise não
// a enxerga, e a linha volta classificada como `novo`, com default IMPORTAR.
//
// E o índice único não segura: a chave é a mesma, mas o ordinal calculado passa
// a ser 2, e (casa, chave, 2) está livre. A MESMA transação entra duas vezes,
// em silêncio — que é exatamente o que esta entrega existe para impedir.
//
// O teste descreve o comportamento CERTO. Enquanto a correção não chegar, ele
// fica vermelho, e é para ficar: um teste vermelho que diz a verdade vale mais
// do que um verde que descreve o defeito.
func TestPontaAPontaG_MesmaChaveNaturalComDataDistante(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	const idDaTransacao = "42"

	// Arquivo A: a transação em 04/08.
	arquivoA := csvExtratoAPI(linhaExtrato{
		dia: 4, valor: "-20.00", idSufixo: idDaTransacao, descricao: "Mercado Exemplo",
	})
	loteA := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "A.csv", arquivoA))
	resA := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, `{"decisions":[]}`))
	require.Equal(t, 1, resA.Imported)

	// Arquivo B: a MESMA transação — mesmo `Identificador`, mesmo valor, mesma
	// descrição — com a data corrigida para 01/06. Dois meses de distância, bem
	// além dos 3 dias de folga da janela.
	var arquivoB bytes.Buffer
	arquivoB.WriteString("Data,Valor,Identificador,Descrição\n")
	fmt.Fprintf(&arquivoB, "01/06/2026,-20.00,11111111-1111-4111-8111-1111111111%s,Mercado Exemplo\n",
		idDaTransacao)

	loteB := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "B.csv", arquivoB.Bytes()))
	require.NotNil(t, loteB.Counts)

	assert.Equal(t, 1, loteB.Counts.DuplicateExact,
		"a chave natural é a MESMA: a linha tem de voltar marcada, não importa a distância entre as datas")
	assert.Zero(t, loteB.Counts.New,
		"classificar como `novo` é o defeito: `novo` entra por DEFAULT, sem o usuário ver nada")

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, `{"decisions":[]}`))
	assert.Zero(t, res.Imported, "a mesma transação NÃO pode entrar duas vezes")

	// A prova independente da classificação: em toda a conta, nenhuma chave de
	// deduplicação pode aparecer em duas linhas VIVAS. É a invariante que o
	// índice único deveria garantir sozinho, e que o ordinal 2 contorna.
	janela, err := a.repoTx.WindowForDedup(t.Context(), a.casa.ID, conta.ID,
		civil.MustNew(2026, 1, 1), civil.MustNew(2026, 12, 31))
	require.NoError(t, err)

	vivasPorChave := map[string]int{}
	for _, linha := range janela {
		if linha.DeletedAt == nil {
			vivasPorChave[linha.DedupKey]++
		}
	}
	for chave, n := range vivasPorChave {
		assert.Equal(t, 1, n,
			"a chave %s… aparece em %d linhas vivas: é a MESMA transação gravada mais de uma vez",
			chave[:8], n)
	}

	totalVivas := len(a.lancamentosDa(t, a.casa.ID, "2026-08")) + len(a.lancamentosDa(t, a.casa.ID, "2026-06"))
	assert.Equal(t, 1, totalVivas, "UMA transação no total, nos dois meses somados")
}

// TestPontaAPontaG_DataQueMudouDentroDaJanelaContinuaSendoPega é o controle do
// teste acima: com a data mudando MENOS que a folga da janela, a marcação
// funciona hoje.
//
// Ele existe para que a correção do caso de cima não seja "alargar a janela até
// o teste passar": este aqui continua verde de qualquer jeito, e a diferença
// entre os dois é exatamente o tamanho do buraco.
func TestPontaAPontaG_DataQueMudouDentroDaJanelaContinuaSendoPega(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	const idDaTransacao = "43"

	loteA := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "A.csv", csvExtratoAPI(linhaExtrato{
		dia: 10, valor: "-31.50", idSufixo: idDaTransacao, descricao: "Livraria Exemplo",
	})))
	resA := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, `{"decisions":[]}`))
	require.Equal(t, 1, resA.Imported)

	// Dois dias de diferença: dentro da folga de transaction.DedupWindowDays.
	loteB := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "B.csv", csvExtratoAPI(linhaExtrato{
		dia: 12, valor: "-31.50", idSufixo: idDaTransacao, descricao: "Livraria Exemplo",
	})))
	require.NotNil(t, loteB.Counts)
	assert.Equal(t, 1, loteB.Counts.DuplicateExact)
	assert.Zero(t, loteB.Counts.New)

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, `{"decisions":[]}`))
	assert.Zero(t, res.Imported)
	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 1)
}

// --- a conta que precisa fechar -------------------------------------------

// exigirContasFechadas confere a invariante mais simples e mais útil do
// resultado do confirm: TODA linha do arquivo termina em exatamente um balde.
//
//	imported + restored + skipped + blocked + rejected == rowCount
//
// Ela existe porque o número é o que a tela mostra, e um resultado que não
// fecha é pior do que não mostrar número nenhum: ele faz a pessoa procurar um
// lançamento que ela acha que entrou. É também a rede que pega a classe de
// defeito em que um caminho de erro reporta só metade das linhas bloqueadas.
func exigirContasFechadas(t *testing.T, lote importer.BatchView, res importer.ResultView) {
	t.Helper()
	soma := res.Imported + res.Restored + res.Skipped + res.Blocked + res.Rejected
	assert.Equal(t, lote.RowCount, soma,
		"o resultado tem de fechar com as %d linhas do arquivo "+
			"(importadas=%d restauradas=%d ignoradas=%d bloqueadas=%d rejeitadas=%d)",
		lote.RowCount, res.Imported, res.Restored, res.Skipped, res.Blocked, res.Rejected)
}

// TestPontaAPontaOsNumerosDoResultadoSempreFecham roda os caminhos de confirm
// mais comuns e confere a invariante em cada um.
func TestPontaAPontaOsNumerosDoResultadoSempreFecham(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	cartao := a.contaCartao(t)

	t.Run("extrato com os padrões", func(t *testing.T) {
		lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "e.csv", fixtureExtrato(t)))
		res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
		exigirContasFechadas(t, lote, res)
	})

	t.Run("reimportação inteira barrada", func(t *testing.T) {
		lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "e2.csv", fixtureExtrato(t)))
		res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
		exigirContasFechadas(t, lote, res)
		assert.Zero(t, res.Imported)
	})

	t.Run("restauração", func(t *testing.T) {
		alvo := a.lancamentosDa(t, a.casa.ID, "2026-08")[0]
		require.NoError(t, a.txSvc.SoftDelete(t.Context(), transaction.Actor{
			HouseholdID: a.casa.ID, UserID: a.usuario.ID,
		}, alvo.ID))

		lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "e3.csv", fixtureExtrato(t)))
		var excluida importer.RowView
		for _, l := range revisarPelaAPI(t, a, lote.ID).Items {
			if l.Status == string(dedup.StatusDuplicateDeleted) {
				excluida = l
			}
		}
		require.NotEmpty(t, excluida.ID)

		res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID,
			fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"import"}]}`, excluida.ID)))
		exigirContasFechadas(t, lote, res)
		assert.Equal(t, 1, res.Restored)
	})

	t.Run("fatura com transferência", func(t *testing.T) {
		lote := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "f.csv", fixtureFatura(t)))
		res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID,
			`{"decisions":[],`+confirmacaoDeFatura+`}`))
		exigirContasFechadas(t, lote, res)
	})

	t.Run("linhas rejeitadas no meio", func(t *testing.T) {
		conteudo := append(csvExtratoAPI(
			linhaExtrato{dia: 3, valor: "-50.00", idSufixo: "71", descricao: "Mercado Exemplo"},
			linhaExtrato{dia: 4, valor: "-19.90", idSufixo: "72", descricao: "Farmacia Exemplo"},
			linhaExtrato{dia: 5, valor: "-19.90", idSufixo: "73", descricao: "Padaria Exemplo"},
			linhaExtrato{dia: 6, valor: "-19.90", idSufixo: "74", descricao: "Posto Exemplo"},
		), []byte("99/99/2026,nao-e-numero,11111111-1111-4111-8111-111111111175,Linha Ruim\n")...)

		lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "ruim.csv", conteudo))
		require.NotNil(t, lote.Counts)
		require.Equal(t, 1, lote.Counts.Rejected)

		res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
		exigirContasFechadas(t, lote, res)
	})
}

// TestPontaAPontaAvisoDeConteudoRepetidoSobreviveAteARevisao descreve o
// comportamento CERTO de um defeito que a revisão ponta a ponta encontrou.
//
// O aviso "você já importou este mesmo arquivo" (§4.1 e §5.3) é calculado — e
// tem teste de serviço próprio —, mas só é DEVOLVIDO na resposta 201 de
// `POST /imports`. A tela que o exibe é a de REVISÃO, e ela carrega o lote por
// `GET /imports/{id}`, onde o campo volta nulo sempre: `Preview` passa `nil` no
// lugar do `sameContentAt` (importer/service.go). O componente existe, tem
// teste de unidade, e o usuário nunca o vê.
//
// Não é defesa — a §4.1 diz com todas as letras que o hash "economiza tempo;
// não é defesa" —, então o impacto é de usabilidade, não de dinheiro. Mas é
// funcionalidade morta, e o jeito de ela não ficar morta por três entregas é um
// teste vermelho dizendo o que deveria acontecer.
//
// ⚠️ VERMELHO ENQUANTO O DEFEITO EXISTIR.
func TestPontaAPontaAvisoDeConteudoRepetidoSobreviveAteARevisao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	primeiro := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "NU.csv", fixtureExtrato(t)))
	_ = resultadoDaResposta(t, confirmarPelaAPI(t, a, primeiro.ID, `{"decisions":[]}`))

	segundo := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "NU.csv", fixtureExtrato(t)))
	require.NotNil(t, segundo.SameContentImportedAt,
		"na resposta do envio o aviso existe — este é o comportamento que já funciona")

	// E agora a parte que falha: a revisão é a tela que EXIBE o aviso.
	revisao := revisarPelaAPI(t, a, segundo.ID)
	assert.NotNil(t, revisao.Batch.SameContentImportedAt,
		"o aviso precisa sobreviver até GET /imports/{id}, que é de onde a tela de revisão lê o lote")
}
