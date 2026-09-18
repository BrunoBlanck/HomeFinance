package importer_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regressão do achado 10 da revisão da E2: o MESMO Identificador em duas
// linhas do MESMO arquivo.
//
// O defeito nasceu da correção do achado 1. A camada de escrita
// (transaction.Service.CreateBatch) passou a recusar ordinal > 1 em chave
// natural — certo, porque o identificador do emissor é único por transação —,
// mas a classificação continuou devolvendo a 2ª ocorrência como
// `repetido_no_arquivo`, que entra por DEFAULT. As duas camadas discordavam:
// a prévia dizia "importa as três", a escrita recusava o ordinal 2 e desfazia
// o lote inteiro — inclusive a linha 3, que não tinha nada a ver. A tela
// mostrava "outra importação gravou estas linhas primeiro", que era falso, e
// reconfirmar repetia para sempre.
//
// A correção mora na classificação: chave natural repetida no arquivo é
// `duplicado_exato` — barrada, não liberável — e a guarda da escrita só
// dispara na corrida real, que é o único caso em que a mensagem é verdadeira.

// csvDaPoC é o CSV literal da PoC do revisor: três linhas, as duas primeiras
// com o mesmo Identificador, a terceira inocente.
func csvDaPoC() []byte {
	return csvExtratoAPI(
		linhaExtrato{dia: 4, valor: "-20.00", idSufixo: "61", descricao: "Mercado Exemplo"},
		linhaExtrato{dia: 5, valor: "-20.00", idSufixo: "61", descricao: "Mercado Exemplo"},
		linhaExtrato{dia: 6, valor: "-9.90", idSufixo: "62", descricao: "Padaria Exemplo"},
	)
}

// TestIdentificadorRepetidoNoArquivoNaoTravaOLote é a PoC, literal.
func TestIdentificadorRepetidoNoArquivoNaoTravaOLote(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "poc.csv", csvDaPoC()))
	require.NotNil(t, lote.Counts)
	assert.Equal(t, 2, lote.Counts.New, "as linhas 1 e 3 são novas")
	assert.Equal(t, 1, lote.Counts.DuplicateExact, "a linha 2 é a MESMA transação da linha 1")
	assert.Zero(t, lote.Counts.RepeatedInFile,
		"`repetido_no_arquivo` é o defeito: entra por default e a escrita recusa")

	// A prévia tem de barrar a linha 2 — e barrar de verdade: sem ação
	// liberável, porque liberar não adiantaria (a escrita recusaria).
	revisao := revisarPelaAPI(t, a, lote.ID)
	require.Len(t, revisao.Items, 3)

	porSeq := map[int]importer.RowView{}
	for _, linha := range revisao.Items {
		porSeq[linha.Seq] = linha
	}

	assert.Equal(t, string(dedup.StatusNew), porSeq[1].Status)
	assert.Equal(t, importer.ActionImport, porSeq[1].DefaultAction)

	assert.Equal(t, string(dedup.StatusDuplicateExact), porSeq[2].Status)
	assert.Equal(t, importer.ActionSkip, porSeq[2].DefaultAction)
	assert.Empty(t, porSeq[2].AllowedActions, "não liberável")
	assert.Nil(t, porSeq[2].MatchTransactionID,
		"a gêmea ainda não existe no banco — está na linha 1 do próprio arquivo")

	assert.Equal(t, string(dedup.StatusNew), porSeq[3].Status)
	assert.Equal(t, importer.ActionImport, porSeq[3].DefaultAction)

	// O confirm sem decisão nenhuma grava as duas e pula a repetida — sem
	// bloquear nada e sem deixar o lote pendente.
	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))
	assert.Equal(t, importer.BatchStatusCommitted, res.Status)
	assert.Equal(t, 2, res.Imported)
	assert.Equal(t, 1, res.Skipped)
	assert.Zero(t, res.Blocked, "bloqueio é só para a corrida real — aqui não houve corrida")
	assert.Empty(t, res.BlockedRows)

	vivas := a.lancamentosDa(t, a.casa.ID, "2026-08")
	assert.Len(t, vivas, 2, "dois lançamentos vivos: a linha 1 e a linha 3")
	for chave, n := range chavesVivasNaConta(t, a, conta.ID) {
		assert.Equal(t, 1, n, "a chave %s… aparece em %d linhas vivas", chave[:8], n)
	}
}

// TestIdentificadorRepetidoNoArquivoReimportadoFicaTodoDeFora fecha o ciclo:
// reimportar o mesmo arquivo depois do confirm barra as três linhas — as duas
// que casam com o banco e a repetida, que continua sem lugar.
func TestIdentificadorRepetidoNoArquivoReimportadoFicaTodoDeFora(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	lote1 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "poc.csv", csvDaPoC()))
	require.Equal(t, 2, resultadoDaResposta(t, confirmarPelaAPI(t, a, lote1.ID, `{"decisions":[]}`)).Imported)

	lote2 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "poc.csv", csvDaPoC()))
	require.NotNil(t, lote2.Counts)
	assert.Equal(t, 3, lote2.Counts.DuplicateExact)
	assert.Zero(t, lote2.Counts.New)
	assert.Zero(t, lote2.Counts.RepeatedInFile)

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote2.ID, `{"decisions":[]}`))
	assert.Equal(t, importer.BatchStatusCommitted, res.Status)
	assert.Zero(t, res.Imported)
	assert.Equal(t, 3, res.Skipped)
	assert.Zero(t, res.Blocked)

	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 2, "continuam dois: nada entrou, nada sumiu")
}

// TestChaveDerivadaRepetidaNoArquivoContinuaEntrando é o CONTROLE: a correção
// vale SÓ para a chave natural. Na fatura — sem identificador por linha —
// duas linhas idênticas continuam sendo `repetido_no_arquivo`, e as DUAS
// entram: dois cafés iguais no mesmo dia são dois gastos reais.
//
// Os cenários B e F de dedup_ponta_a_ponta_test.go já provam isso em detalhe;
// este fica ao lado da regressão para que ninguém "conserte" a natural
// estendendo a regra à derivada por engano.
func TestChaveDerivadaRepetidaNoArquivoContinuaEntrando(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)

	arquivo := csvFaturaAPI(
		linhaFatura{data: "2026-08-14", titulo: "Cafe Exemplo", valor: "11,00"},
		linhaFatura{data: "2026-08-14", titulo: "Cafe Exemplo", valor: "11,00"},
		linhaFatura{data: "2026-08-15", titulo: "Livraria Exemplo", valor: "45,00"},
	)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, cartao.ID, "fatura.csv", arquivo))
	require.NotNil(t, lote.Counts)
	assert.Equal(t, 2, lote.Counts.New)
	assert.Equal(t, 1, lote.Counts.RepeatedInFile, "a 2ª ocorrência idêntica é `repetido_no_arquivo`")
	assert.Zero(t, lote.Counts.DuplicateExact, "na chave derivada NÃO existe 'mesma transação' por construção")

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a,
		lote.ID, `{"decisions":[],`+confirmacaoDeFatura+`}`))
	assert.Equal(t, importer.BatchStatusCommitted, res.Status)
	assert.Equal(t, 3, res.Imported, "as três entram — inclusive o segundo café")
	assert.Zero(t, res.Skipped)

	lista := listarPelaAPI(t, a, "2026-09")
	assert.Equal(t, 2, contarDescricao(lista.Items, "Cafe Exemplo"), "DOIS cafés, nenhum sumiu")
}
