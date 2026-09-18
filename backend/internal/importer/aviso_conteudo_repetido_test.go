package importer_test

import (
	"slices"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Camada 1 da deduplicação (§4.1 da spec 0004): o aviso "você já importou este
// mesmo arquivo".
//
// Ele é AVISO, nunca bloqueio — nenhum teste daqui impede importação nenhuma.
// O que este arquivo vigia são as três formas de ele dar errado:
//
//  1. SUMIR no meio do caminho. Era o defeito D1: o aviso era calculado na
//     resposta do envio e devolvido nulo em GET /imports/{id}, que é de onde a
//     tela de revisão — a que o exibe — lê o lote;
//  2. MENTIR. "Você já importou" apoiado num lote pendente, descartado ou
//     expirado é falso: nenhum dos três gravou um lançamento sequer. Quem
//     acredita no aviso pula a importação, e linha que nunca entrou é o defeito
//     que a pessoa só descobre meses depois conferindo o extrato — duplicata
//     ela vê e apaga, ausência não;
//  3. ATRAVESSAR A CASA. O hash de um arquivo responderia "já importado" para
//     quem só quer descobrir o que a casa vizinha importou.

// avisoNaRevisao devolve o campo como a TELA o recebe: pelo Preview, que é o
// GET /imports/{id}.
func avisoNaRevisao(t *testing.T, a *ambiente, ator importer.Actor, loteID string) *string {
	t.Helper()
	revisao, err := a.svc.Preview(t.Context(), ator, loteID, 0, 50)
	require.NoError(t, err)
	return revisao.Batch.SameContentImportedAt
}

// TestAvisoDeConteudoRepetidoChegaAteARevisaoComAHoraDaConfirmacao é a
// regressão do defeito D1.
func TestAvisoDeConteudoRepetidoChegaAteARevisaoComAHoraDaConfirmacao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	primeiro := a.enviarExtrato(t, conta.ID)
	require.Nil(t, primeiro.SameContentImportedAt, "o primeiro envio não tem passado nenhum")

	// Duas horas entre enviar e confirmar, mais uma até o reenvio: é o que
	// separa "quando o rascunho nasceu" de "quando o arquivo entrou".
	a.relogio.avancar(2 * time.Hour)
	confirmadoEm := a.relogio.now()
	_, err := a.svc.Confirm(t.Context(), a.ator(), primeiro.ID, importer.ConfirmInput{})
	require.NoError(t, err)

	a.relogio.avancar(time.Hour)
	segundo := a.enviarExtrato(t, conta.ID)
	require.NotNil(t, segundo.SameContentImportedAt, "na resposta do envio o aviso existe")

	aviso := avisoNaRevisao(t, a, a.ator(), segundo.ID)
	require.NotNil(t, aviso,
		"o aviso precisa sobreviver até GET /imports/{id}: é de lá que a tela de revisão lê o lote")
	assert.Equal(t, confirmadoEm.UTC().Format(time.RFC3339), *aviso,
		"a data publicada é a da CONFIRMAÇÃO — entre enviar e confirmar cabe um dia inteiro (BatchTTL)")
	assert.Equal(t, *segundo.SameContentImportedAt, *aviso,
		"as duas fases respondem a MESMA coisa: é a mesma consulta, e foi separá-las que matou o aviso")
}

// TestAvisoDeConteudoRepetidoSoOlhaLoteQueRealmenteImportou percorre os quatro
// estados do lote e cobra o aviso em exatamente um deles.
func TestAvisoDeConteudoRepetidoSoOlhaLoteQueRealmenteImportou(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	// (1) PENDENTE — o rascunho de quem abriu a revisão e não voltou.
	pendente := a.enviarExtrato(t, conta.ID)
	comPendente := a.enviarExtrato(t, conta.ID)
	assert.Nil(t, comPendente.SameContentImportedAt,
		"lote pendente não gravou lançamento nenhum: ninguém importou nada ainda")
	assert.Nil(t, avisoNaRevisao(t, a, a.ator(), comPendente.ID))

	// (2) DESCARTADO — o lote que a pessoa jogou fora de propósito.
	require.NoError(t, a.svc.Discard(t.Context(), a.ator(), pendente.ID))
	require.NoError(t, a.svc.Discard(t.Context(), a.ator(), comPendente.ID))

	comDescartado := a.enviarExtrato(t, conta.ID)
	assert.Nil(t, comDescartado.SameContentImportedAt,
		"lote descartado é o contrário de importado")
	assert.Nil(t, avisoNaRevisao(t, a, a.ator(), comDescartado.ID))

	// (3) EXPIRADO — e pelo caminho real, o janitor, que é quem produz esse
	// estado em produção.
	a.relogio.avancar(importer.BatchTTL + time.Hour)
	expirados, _, _, err := a.svc.PurgeExpired(t.Context())
	require.NoError(t, err)
	require.EqualValues(t, 1, expirados, "só o lote ainda pendente vence")

	comExpirado := a.enviarExtrato(t, conta.ID)
	assert.Nil(t, comExpirado.SameContentImportedAt,
		"lote vencido morreu sem confirmar: também não importou nada")
	assert.Nil(t, avisoNaRevisao(t, a, a.ator(), comExpirado.ID))

	// (4) CONFIRMADO — o único estado em que a frase é verdadeira.
	_, err = a.svc.Confirm(t.Context(), a.ator(), comExpirado.ID, importer.ConfirmInput{})
	require.NoError(t, err)

	depois := a.enviarExtrato(t, conta.ID)
	assert.NotNil(t, depois.SameContentImportedAt, "agora sim: este conteúdo virou lançamento")
	assert.NotNil(t, avisoNaRevisao(t, a, a.ator(), depois.ID))
}

// pularTudo monta a decisão explícita de PULAR cada linha do lote — é o corpo
// de quem abriu a revisão, olhou e decidiu que não queria nada daquele arquivo.
func pularTudo(t *testing.T, a *ambiente, loteID string) []importer.Decision {
	t.Helper()

	revisao, err := a.svc.Preview(t.Context(), a.ator(), loteID, 0, 200)
	require.NoError(t, err)

	decisoes := make([]importer.Decision, 0, len(revisao.Items))
	for _, linha := range revisao.Items {
		// Linha `rejeitado` não aceita ação nenhuma: citá-la seria 400.
		if slices.Contains(linha.AllowedActions, importer.ActionSkip) {
			decisoes = append(decisoes, importer.Decision{
				RowID:  linha.ID,
				Action: importer.ActionSkip,
			})
		}
	}
	require.NotEmpty(t, decisoes)
	return decisoes
}

// TestAvisoDeConteudoRepetidoIgnoraLoteConfirmadoQueNaoGravouNada cobre o
// buraco entre "confirmado" e "importado".
//
// Confirmar não é importar: quem manda pular todas as linhas fecha o lote como
// `committed` com imported_count = 0. Avisar com base nele diria "este arquivo
// já entrou" sobre um arquivo do qual nenhuma linha entrou — e a pessoa que
// acredita nisso não importa de novo.
func TestAvisoDeConteudoRepetidoIgnoraLoteConfirmadoQueNaoGravouNada(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	lote := a.enviarExtrato(t, conta.ID)
	res, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
		Decisions: pularTudo(t, a, lote.ID),
	})
	require.NoError(t, err)
	require.Equal(t, importer.BatchStatusCommitted, res.Status)
	require.Zero(t, res.Imported, "o lote fecha confirmado e sem gravar nada")
	require.Zero(t, res.Restored)

	depois := a.enviarExtrato(t, conta.ID)
	assert.Nil(t, depois.SameContentImportedAt,
		"confirmar pulando tudo não é importar: não há o que a pessoa vá reencontrar na lista")
	assert.Nil(t, avisoNaRevisao(t, a, a.ator(), depois.ID))
}

// TestAvisoDeConteudoRepetidoNaoAtravessaAsCasas é o teste de BOLA do aviso.
func TestAvisoDeConteudoRepetidoNaoAtravessaAsCasas(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	minhaConta := a.contaCorrente(t)

	// A minha casa importa o arquivo de verdade.
	meu := a.enviarExtrato(t, minhaConta.ID)
	_, err := a.svc.Confirm(t.Context(), a.ator(), meu.ID, importer.ConfirmInput{})
	require.NoError(t, err)

	// A outra casa envia o MESMO arquivo, byte a byte. A conta dela é real,
	// não um id inventado: id inexistente provaria só que a busca falha.
	contaAlheia := a.conta(t, a.alheia.ID, "Nubank Conta", account.KindChecking, "nubank")
	dela, err := a.svc.Analyze(t.Context(), a.atorAlheio(), importer.AnalyzeInput{
		AccountID: contaAlheia.ID,
		FileName:  "NU_2026-08.csv",
		Content:   fixtureExtrato(t),
	})
	require.NoError(t, err)

	assert.Nil(t, dela.SameContentImportedAt,
		"o aviso viraria um oráculo: o hash de um arquivo diria quais arquivos a casa vizinha importou")
	assert.Nil(t, avisoNaRevisao(t, a, a.atorAlheio(), dela.ID))

	// O controle, para o teste não passar por a consulta estar quebrada: na
	// casa que de fato importou, o aviso aparece.
	outro := a.enviarExtrato(t, minhaConta.ID)
	assert.NotNil(t, outro.SameContentImportedAt)
	assert.NotNil(t, avisoNaRevisao(t, a, a.ator(), outro.ID))

	// E o lote da outra casa continua inalcançável daqui (S1).
	_, err = a.svc.Preview(t.Context(), a.ator(), dela.ID, 0, 50)
	assert.ErrorIs(t, err, importer.ErrBatchNotFound)
}

// TestAvisoDeConteudoRepetidoNaoAvisaSobreOProprioLote cobre a exclusão do lote
// corrente — a parte que só aparece quando o lote consultado JÁ é o mais
// recente com aquele conteúdo.
func TestAvisoDeConteudoRepetidoNaoAvisaSobreOProprioLote(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	primeiro := a.enviarExtrato(t, conta.ID)
	primeiroConfirmadoEm := a.relogio.now()
	_, err := a.svc.Confirm(t.Context(), a.ator(), primeiro.ID, importer.ConfirmInput{})
	require.NoError(t, err)

	assert.Nil(t, avisoNaRevisao(t, a, a.ator(), primeiro.ID),
		"sem excluir o lote corrente, a revisão de um lote confirmado avisa sobre a PRÓPRIA importação")

	// Já o SEGUNDO lote do mesmo conteúdo enxerga o primeiro: o aviso aponta
	// para fora, e é essa a direção útil.
	a.relogio.avancar(time.Hour)
	segundo := a.enviarExtrato(t, conta.ID)

	doSegundo := avisoNaRevisao(t, a, a.ator(), segundo.ID)
	require.NotNil(t, doSegundo)
	assert.Equal(t, primeiroConfirmadoEm.UTC().Format(time.RFC3339), *doSegundo)

	// E depois de o segundo também virar terminal — confirmado, importando
	// zero, porque tudo nele já está no banco —, o primeiro continua sem
	// avisar sobre si mesmo.
	res, err := a.svc.Confirm(t.Context(), a.ator(), segundo.ID, importer.ConfirmInput{})
	require.NoError(t, err)
	require.Zero(t, res.Imported)

	assert.Nil(t, avisoNaRevisao(t, a, a.ator(), primeiro.ID))
	assert.NotNil(t, avisoNaRevisao(t, a, a.ator(), segundo.ID),
		"o segundo continua apontando para o primeiro, que é quem de fato gravou")
}
