package dedup_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

const (
	instituicao = "nubank"
	conta       = "11111111-1111-7111-8111-000000000001"
	outraConta  = "11111111-1111-7111-8111-000000000002"
)

func data(s string) civil.Date {
	d, err := civil.Parse(s)
	if err != nil {
		panic(err)
	}
	return d
}

func ptr(s string) *string { return &s }

// ---------------------------------------------------------------------------
// Banco de mentira: o mínimo para encadear importações
// ---------------------------------------------------------------------------

// banco simula o que a casa já tem gravado, INCLUSIVE o índice único
// (household_id, dedup_key, dedup_ordinal).
//
// A simulação do índice é o ponto: ele é o árbitro final da garantia
// anti-duplicata, e um teste que grave livremente provaria menos do que parece.
// Aqui, uma colisão de (chave, ordinal) reprova o teste na hora.
type banco struct {
	linhas   []transaction.DedupRow
	ocupados map[string]struct{}
	seq      int
}

func novoBanco() *banco {
	return &banco{ocupados: map[string]struct{}{}}
}

func (b *banco) analisar(t *testing.T, linhas []dedup.Row) dedup.Result {
	t.Helper()
	res, err := dedup.Analyze(dedup.Input{
		Institution: instituicao,
		AccountID:   conta,
		Rows:        linhas,
		Existing:    b.linhas,
	})
	require.NoError(t, err)
	require.Len(t, res.Rows, len(linhas))
	return res
}

// confirmar grava as linhas que entram PELO DEFAULT — sem nenhuma decisão da
// pessoa. É assim que os cenários de aceite são medidos.
func (b *banco) confirmar(t *testing.T, linhas []dedup.Row, res dedup.Result) int {
	t.Helper()
	return b.confirmarCom(t, linhas, res, nil)
}

// confirmarCom grava as do default MAIS as liberadas explicitamente (por Seq).
func (b *banco) confirmarCom(t *testing.T, linhas []dedup.Row, res dedup.Result, liberadas map[int]bool) int {
	t.Helper()
	gravadas := 0
	for i, veredito := range res.Rows {
		if !veredito.Import && !liberadas[veredito.Seq] {
			continue
		}
		// Liberar duplicado_excluido RESTAURA a existente em vez de inserir
		// (ADR-025f) — não passa por aqui.
		if veredito.Status == dedup.StatusDuplicateDeleted {
			continue
		}

		indice := fmt.Sprintf("%s#%d", veredito.DedupKey, veredito.Ordinal)
		if _, colide := b.ocupados[indice]; colide {
			t.Fatalf("o índice único recusaria (chave, ordinal) repetido na linha %d", veredito.Seq)
		}
		b.ocupados[indice] = struct{}{}

		b.seq++
		r := linhas[i]
		b.linhas = append(b.linhas, transaction.DedupRow{
			ID:              fmt.Sprintf("tx-%03d", b.seq),
			OccurredOn:      r.OccurredOn,
			AmountCents:     r.AmountCents,
			Kind:            r.Kind,
			DescriptionNorm: r.DescriptionNorm,
			ExternalID:      r.ExternalID,
			DedupKey:        veredito.DedupKey,
			DedupOrdinal:    veredito.Ordinal,
		})
		gravadas++
	}
	return gravadas
}

func (b *banco) excluir(t *testing.T, id string) {
	t.Helper()
	agora := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	for i := range b.linhas {
		if b.linhas[i].ID == id {
			b.linhas[i].DeletedAt = &agora
			return
		}
	}
	t.Fatalf("lançamento %q não existe no banco de teste", id)
}

func statusDe(res dedup.Result) []dedup.Status {
	out := make([]dedup.Status, 0, len(res.Rows))
	for _, r := range res.Rows {
		out = append(out, r.Status)
	}
	return out
}

// ---------------------------------------------------------------------------
// Fábricas de linha
// ---------------------------------------------------------------------------

func despesa(seq int, dia string, cents int64, descNorm string) dedup.Row {
	return dedup.Row{
		Seq:             seq,
		Kind:            transaction.KindExpense,
		OccurredOn:      data(dia),
		AmountCents:     cents,
		DescriptionNorm: descNorm,
	}
}

func receita(seq int, dia string, cents int64, descNorm string) dedup.Row {
	r := despesa(seq, dia, cents, descNorm)
	r.Kind = transaction.KindIncome
	return r
}

func comID(r dedup.Row, externalID string) dedup.Row {
	r.ExternalID = ptr(externalID)
	return r
}

// ---------------------------------------------------------------------------
// Cenário A — reimportar o MESMO arquivo
// ---------------------------------------------------------------------------

func TestCenarioA_ReimportarOMesmoArquivo(t *testing.T) {
	arquivo := []dedup.Row{
		despesa(1, "2026-08-14", 1100, "cafe exemplo"),
		despesa(2, "2026-08-14", 1100, "cafe exemplo"),
		despesa(3, "2026-08-06", 602, "padaria exemplo"),
	}

	b := novoBanco()

	primeira := b.analisar(t, arquivo)
	assert.Equal(t, []dedup.Status{
		dedup.StatusNew, dedup.StatusRepeatedInFile, dedup.StatusNew,
	}, statusDe(primeira))
	assert.Equal(t, 3, b.confirmar(t, arquivo, primeira))

	segunda := b.analisar(t, arquivo)
	assert.Equal(t, []dedup.Status{
		dedup.StatusDuplicateExact, dedup.StatusDuplicateExact, dedup.StatusDuplicateExact,
	}, statusDe(segunda), "100% das linhas marcadas")
	assert.Equal(t, 0, segunda.Importable(), "zero importadas pelo default")
	assert.Equal(t, 0, b.confirmar(t, arquivo, segunda))
	assert.Len(t, b.linhas, 3, "nada entrou e nada saiu")
}

// ---------------------------------------------------------------------------
// Cenário B — compra legítima repetida. NENHUMA some, NUNCA.
// ---------------------------------------------------------------------------

func TestCenarioB_CompraLegitimaRepetida(t *testing.T) {
	// O par idêntico da fixture da fatura: dois cafés de R$ 11,00 no mesmo dia.
	arquivo := []dedup.Row{
		despesa(1, "2026-08-14", 1100, "cafe exemplo"),
		despesa(2, "2026-08-14", 1100, "cafe exemplo"),
	}

	b := novoBanco()

	primeira := b.analisar(t, arquivo)
	assert.True(t, primeira.Rows[0].Import, "o primeiro café entra")
	assert.True(t, primeira.Rows[1].Import, "o SEGUNDO café também entra — são dois gastos reais")
	assert.Equal(t, dedup.StatusRepeatedInFile, primeira.Rows[1].Status)
	assert.Equal(t, 1, primeira.Rows[0].Ordinal)
	assert.Equal(t, 2, primeira.Rows[1].Ordinal)
	assert.Equal(t, primeira.Rows[0].DedupKey, primeira.Rows[1].DedupKey,
		"mesma tupla, mesma chave: quem separa é o ordinal")

	require.Equal(t, 2, b.confirmar(t, arquivo, primeira))
	assert.Len(t, b.linhas, 2)

	segunda := b.analisar(t, arquivo)
	assert.Equal(t, []dedup.Status{
		dedup.StatusDuplicateExact, dedup.StatusDuplicateExact,
	}, statusDe(segunda), "na 2ª importação as duas são barradas")
	assert.Equal(t, 0, b.confirmar(t, arquivo, segunda))
	assert.Len(t, b.linhas, 2, "continuam sendo dois — nenhum sumiu e nenhum duplicou")
}

// ---------------------------------------------------------------------------
// Cenário C — o mesmo período repartido em dois arquivos
// ---------------------------------------------------------------------------

func TestCenarioC_PeriodoRepartido(t *testing.T) {
	arquivoA := []dedup.Row{
		despesa(1, "2026-08-05", 1000, "compra a"),
		despesa(2, "2026-08-10", 2000, "compra b"),
		despesa(3, "2026-08-15", 3000, "compra c"),
	}
	// B cobre 01–31: repete as três de A e traz duas novas.
	arquivoB := []dedup.Row{
		despesa(1, "2026-08-05", 1000, "compra a"),
		despesa(2, "2026-08-10", 2000, "compra b"),
		despesa(3, "2026-08-15", 3000, "compra c"),
		despesa(4, "2026-08-20", 4000, "compra d"),
		despesa(5, "2026-08-25", 5000, "compra e"),
	}

	b := novoBanco()
	require.Equal(t, 3, b.confirmar(t, arquivoA, b.analisar(t, arquivoA)))

	res := b.analisar(t, arquivoB)
	assert.Equal(t, []dedup.Status{
		dedup.StatusDuplicateExact, dedup.StatusDuplicateExact, dedup.StatusDuplicateExact,
		dedup.StatusNew, dedup.StatusNew,
	}, statusDe(res))

	assert.Equal(t, 2, b.confirmar(t, arquivoB, res), "só as de 16–31 entram")
	assert.Len(t, b.linhas, 5)
}

// ---------------------------------------------------------------------------
// Cenário D — a descrição mudou entre dois downloads
// ---------------------------------------------------------------------------

func TestCenarioD_DescricaoQueMudou(t *testing.T) {
	// 1ª importação: o banco escreveu o nome cru do estabelecimento.
	arquivoAntigo := []dedup.Row{despesa(1, "2026-08-15", 1410, "dl*99 ride")}

	b := novoBanco()
	require.Equal(t, 1, b.confirmar(t, arquivoAntigo, b.analisar(t, arquivoAntigo)))

	// 2ª importação: o banco enriqueceu o nome. A chave derivada NÃO casa.
	arquivoNovo := []dedup.Row{despesa(1, "2026-08-15", 1410, "99 tecnologia ltda")}

	res := b.analisar(t, arquivoNovo)
	require.Len(t, res.Rows, 1)

	assert.NotEqual(t, b.linhas[0].DedupKey, res.Rows[0].DedupKey,
		"descrição diferente = chave derivada diferente; é o buraco que a marcação fraca cobre")
	assert.Equal(t, dedup.StatusPossibleDuplicate, res.Rows[0].Status)
	assert.False(t, res.Rows[0].Import, "barrada por default")
	assert.True(t, res.Rows[0].Status.Releasable(), "mas liberável caso a caso")
	require.NotNil(t, res.Rows[0].MatchTransactionID)
	assert.Equal(t, "tx-001", *res.Rows[0].MatchTransactionID, "a tela aponta o lançamento suspeito")

	assert.Equal(t, 0, b.confirmar(t, arquivoNovo, res))
}

// ---------------------------------------------------------------------------
// Cenário E — linha marcada só entra com decisão explícita
// ---------------------------------------------------------------------------

func TestCenarioE_LinhaMarcadaSoEntraComDecisao(t *testing.T) {
	arquivoAntigo := []dedup.Row{despesa(1, "2026-08-15", 1410, "dl*99 ride")}
	b := novoBanco()
	require.Equal(t, 1, b.confirmar(t, arquivoAntigo, b.analisar(t, arquivoAntigo)))

	arquivoNovo := []dedup.Row{despesa(1, "2026-08-15", 1410, "99 tecnologia ltda")}
	res := b.analisar(t, arquivoNovo)
	require.Equal(t, dedup.StatusPossibleDuplicate, res.Rows[0].Status)

	// Sem decisão: não entra.
	assert.Equal(t, 0, b.confirmar(t, arquivoNovo, res))
	assert.Len(t, b.linhas, 1)

	// Com decisão explícita: entra como ocorrência nova.
	assert.Equal(t, 1, b.confirmarCom(t, arquivoNovo, res, map[int]bool{1: true}))
	assert.Len(t, b.linhas, 2)
}

func TestDefaultEReleasableSeguemATaxonomia(t *testing.T) {
	casos := []struct {
		status    dedup.Status
		importa   bool
		liberavel bool
	}{
		{dedup.StatusNew, true, false},
		{dedup.StatusRepeatedInFile, true, false},
		{dedup.StatusDuplicateExact, false, false},
		{dedup.StatusDuplicateDeleted, false, true},
		{dedup.StatusPossibleDuplicate, false, true},
		{dedup.StatusCardPayment, false, true},
		{dedup.StatusInternalTransfer, false, true},
		{dedup.StatusTransferAlreadyRegistered, false, true},
		{dedup.StatusRejected, false, false},
	}

	for _, c := range casos {
		t.Run(string(c.status), func(t *testing.T) {
			assert.True(t, c.status.Valid())
			assert.Equal(t, c.importa, c.status.DefaultImports())
			assert.Equal(t, c.liberavel, c.status.Releasable())
		})
	}

	assert.False(t, dedup.Status("inventado").Valid())
	assert.False(t, dedup.Status("inventado").DefaultImports(),
		"status desconhecido nunca entra por default")
}

// ---------------------------------------------------------------------------
// O caso patológico da §4.3: sem ordinal, um gasto real sumiria
// ---------------------------------------------------------------------------

func TestCasoPatologico_UmCafeNoArquivoADoisNoB(t *testing.T) {
	// Arquivo A, baixado cedo: UM café de R$ 11,00 em 05/08.
	arquivoA := []dedup.Row{despesa(1, "2026-08-05", 1100, "cafe exemplo")}
	// Arquivo B, baixado depois: DOIS — o segundo caiu após o download de A.
	arquivoB := []dedup.Row{
		despesa(1, "2026-08-05", 1100, "cafe exemplo"),
		despesa(2, "2026-08-05", 1100, "cafe exemplo"),
	}

	b := novoBanco()
	require.Equal(t, 1, b.confirmar(t, arquivoA, b.analisar(t, arquivoA)))

	res := b.analisar(t, arquivoB)
	assert.Equal(t, dedup.StatusDuplicateExact, res.Rows[0].Status, "a 1ª de B é a mesma de A")
	assert.False(t, res.Rows[0].Import)
	assert.Equal(t, dedup.StatusRepeatedInFile, res.Rows[1].Status, "a 2ª de B é um gasto novo")
	assert.True(t, res.Rows[1].Import, "sem ordinal, ela seria barrada e o gasto sumiria")

	// Os ordinais são reservados em sequência para TODAS as linhas da tupla,
	// inclusive a barrada: a pessoa pode liberar qualquer subconjunto, e dois
	// ordinais iguais colidiriam no índice único.
	assert.Equal(t, 2, res.Rows[0].Ordinal)
	assert.Equal(t, 3, res.Rows[1].Ordinal)

	require.Equal(t, 1, b.confirmar(t, arquivoB, res))
	require.Len(t, b.linhas, 2)
	assert.Equal(t, 1, b.linhas[0].DedupOrdinal)
	assert.Equal(t, 3, b.linhas[1].DedupOrdinal, "o ordinal 2 ficou vago, e isso é normal")

	// Terceira passada: o casamento é por POSIÇÃO na lista ordenada, não pelo
	// valor do ordinal — é o que mantém a conta certa com o ordinal 2 vago.
	terceira := b.analisar(t, arquivoB)
	assert.Equal(t, []dedup.Status{
		dedup.StatusDuplicateExact, dedup.StatusDuplicateExact,
	}, statusDe(terceira))
	assert.Equal(t, 0, b.confirmar(t, arquivoB, terceira))
	assert.Len(t, b.linhas, 2)
}

// ---------------------------------------------------------------------------
// Soft delete
// ---------------------------------------------------------------------------

func TestOrdinalContaAsExcluidasLogicamente(t *testing.T) {
	arquivo := []dedup.Row{despesa(1, "2026-08-14", 1100, "cafe exemplo")}

	b := novoBanco()
	require.Equal(t, 1, b.confirmar(t, arquivo, b.analisar(t, arquivo)))
	b.excluir(t, "tx-001")

	res := b.analisar(t, arquivo)
	require.Len(t, res.Rows, 1)

	assert.Equal(t, dedup.StatusDuplicateDeleted, res.Rows[0].Status,
		"a linha excluída continua ocupando a chave — o índice único conta com ela")
	assert.False(t, res.Rows[0].Import)
	assert.True(t, res.Rows[0].Status.Releasable(), "liberar RESTAURA a existente (ADR-025f)")
	require.NotNil(t, res.Rows[0].MatchTransactionID)
	assert.Equal(t, "tx-001", *res.Rows[0].MatchTransactionID)
	assert.Equal(t, 2, res.Rows[0].Ordinal, "ordinal 1 segue ocupado pela excluída")
}

func TestMarcacaoFracaIgnoraExcluidas(t *testing.T) {
	// Bloquear por causa de um lançamento que a pessoa apagou seria devolver a
	// ela, como suspeita, a decisão que ela já tomou.
	antigo := []dedup.Row{despesa(1, "2026-08-14", 1100, "compra antiga")}
	b := novoBanco()
	require.Equal(t, 1, b.confirmar(t, antigo, b.analisar(t, antigo)))
	b.excluir(t, "tx-001")

	novo := []dedup.Row{despesa(1, "2026-08-15", 1100, "compra com outro nome")}
	res := b.analisar(t, novo)
	assert.Equal(t, dedup.StatusNew, res.Rows[0].Status)
	assert.True(t, res.Rows[0].Import)
}

// ---------------------------------------------------------------------------
// Marcação fraca
// ---------------------------------------------------------------------------

func TestMarcacaoFracaRespeitaAJanelaDeDias(t *testing.T) {
	casos := []struct {
		dia      string
		esperado dedup.Status
	}{
		{"2026-08-11", dedup.StatusPossibleDuplicate}, // -3
		{"2026-08-14", dedup.StatusPossibleDuplicate}, // 0
		{"2026-08-17", dedup.StatusPossibleDuplicate}, // +3
		{"2026-08-10", dedup.StatusNew},               // -4
		{"2026-08-18", dedup.StatusNew},               // +4
	}

	for _, c := range casos {
		t.Run(c.dia, func(t *testing.T) {
			antigo := []dedup.Row{despesa(1, "2026-08-14", 1100, "nome antigo")}
			b := novoBanco()
			require.Equal(t, 1, b.confirmar(t, antigo, b.analisar(t, antigo)))

			res := b.analisar(t, []dedup.Row{despesa(1, c.dia, 1100, "nome novo")})
			assert.Equal(t, c.esperado, res.Rows[0].Status)
		})
	}
}

// TestMarcacaoFracaRespeitaOKind evita o falso positivo mais comum de todos:
// um estorno de R$ 29,00 no mesmo dia da compra de R$ 29,00 não é duplicata
// dela — é o par natural de compra e devolução, e está no extrato de exemplo.
func TestMarcacaoFracaRespeitaOKind(t *testing.T) {
	compra := []dedup.Row{despesa(1, "2026-08-30", 2900, "pix enviado - loja exemplo")}
	b := novoBanco()
	require.Equal(t, 1, b.confirmar(t, compra, b.analisar(t, compra)))

	estorno := []dedup.Row{receita(1, "2026-08-30", 2900, "pix reembolso recebido - loja exemplo")}
	res := b.analisar(t, estorno)
	assert.Equal(t, dedup.StatusNew, res.Rows[0].Status)
	assert.True(t, res.Rows[0].Import)
}

func TestMarcacaoFracaNaoDisparaComADescricaoIgual(t *testing.T) {
	// Descrição igual é trabalho da chave derivada com ordinal, não da
	// heurística. Se a marcação fraca pegasse aqui, o caso patológico do café
	// voltaria a perder um gasto.
	arquivoA := []dedup.Row{despesa(1, "2026-08-05", 1100, "cafe exemplo")}
	b := novoBanco()
	require.Equal(t, 1, b.confirmar(t, arquivoA, b.analisar(t, arquivoA)))

	res := b.analisar(t, []dedup.Row{
		despesa(1, "2026-08-05", 1100, "cafe exemplo"),
		despesa(2, "2026-08-05", 1100, "cafe exemplo"),
	})
	assert.Equal(t, dedup.StatusRepeatedInFile, res.Rows[1].Status)
}

func TestMarcacaoFracaApontaAGemeaMaisProxima(t *testing.T) {
	antigo := []dedup.Row{
		despesa(1, "2026-08-11", 1100, "nome a"),
		despesa(2, "2026-08-14", 1100, "nome b"),
	}
	b := novoBanco()
	require.Equal(t, 2, b.confirmar(t, antigo, b.analisar(t, antigo)))

	res := b.analisar(t, []dedup.Row{despesa(1, "2026-08-15", 1100, "nome c")})
	require.Equal(t, dedup.StatusPossibleDuplicate, res.Rows[0].Status)
	require.NotNil(t, res.Rows[0].MatchTransactionID)
	assert.Equal(t, "tx-002", *res.Rows[0].MatchTransactionID, "a de 14/08 é a mais próxima de 15/08")
}

func TestExternalIDEmOutraContaViraPossivelDuplicado(t *testing.T) {
	// A conta entra na chave natural de propósito (§4.2): sem isso, importar na
	// conta errada bloquearia a conta certa para sempre. Esta marcação é o que
	// mostra o engano ao usuário em vez de deixá-lo com a linha em duas contas.
	res, err := dedup.Analyze(dedup.Input{
		Institution: instituicao,
		AccountID:   conta,
		Rows:        []dedup.Row{comID(despesa(1, "2026-08-04", 2000, "compra exemplo"), "uuid-1")},
		ExternalIDsElsewhere: map[string]dedup.ExternalUse{
			"uuid-1": {TransactionID: "tx-outro", AccountID: outraConta},
		},
	})
	require.NoError(t, err)

	assert.Equal(t, dedup.StatusPossibleDuplicate, res.Rows[0].Status)
	assert.False(t, res.Rows[0].Import)
	require.NotNil(t, res.Rows[0].MatchTransactionID)
	assert.Equal(t, "tx-outro", *res.Rows[0].MatchTransactionID)
}

func TestExternalIDNaPropriaContaNaoViraMarcacaoFraca(t *testing.T) {
	// Na mesma conta quem responde é a chave natural, com a garantia dura.
	res, err := dedup.Analyze(dedup.Input{
		Institution: instituicao,
		AccountID:   conta,
		Rows:        []dedup.Row{comID(despesa(1, "2026-08-04", 2000, "compra exemplo"), "uuid-1")},
		ExternalIDsElsewhere: map[string]dedup.ExternalUse{
			"uuid-1": {TransactionID: "tx-001", AccountID: conta},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, dedup.StatusNew, res.Rows[0].Status)
}

// ---------------------------------------------------------------------------
// Chave natural
// ---------------------------------------------------------------------------

func TestChaveNaturalReconheceAReimportacao(t *testing.T) {
	arquivo := []dedup.Row{
		comID(despesa(1, "2026-08-04", 2000, "pix enviado - fulano de tal silva"), "uuid-01"),
		comID(despesa(2, "2026-08-05", 18707, "pix enviado - energia exemplo s.a."), "uuid-02"),
	}

	b := novoBanco()
	require.Equal(t, 2, b.confirmar(t, arquivo, b.analisar(t, arquivo)))

	res := b.analisar(t, arquivo)
	assert.Equal(t, []dedup.Status{
		dedup.StatusDuplicateExact, dedup.StatusDuplicateExact,
	}, statusDe(res))
}

func TestChaveNaturalRepetidaNoArquivoEhBarrada(t *testing.T) {
	// Arquivo editado à mão, com o MESMO id em duas linhas diferentes.
	//
	// Este teste já afirmou o contrário ("as duas entram", como
	// `repetido_no_arquivo`). Mudou com a correção do achado 1 da revisão da
	// E2: a escrita passou a recusar ordinal > 1 em chave natural — o
	// identificador do emissor é único por transação —, e uma classificação
	// que liberasse a 2ª linha por default faria a escrita desfazer o lote
	// inteiro (achado 10). A 2ª ocorrência NÃO é perda silenciosa: ela aparece
	// na revisão, barrada, com o motivo — e a 1ª entra.
	arquivo := []dedup.Row{
		comID(despesa(1, "2026-08-04", 2000, "compra a"), "uuid-01"),
		comID(despesa(2, "2026-08-05", 3000, "compra b"), "uuid-01"),
	}

	b := novoBanco()
	res := b.analisar(t, arquivo)
	assert.Equal(t, []dedup.Status{dedup.StatusNew, dedup.StatusDuplicateExact}, statusDe(res))
	assert.True(t, res.Rows[0].Import)
	assert.False(t, res.Rows[1].Import, "a repetição do identificador é barrada por default")
	assert.False(t, res.Rows[1].Status.Releasable(), "e não é liberável: a escrita recusaria")
	assert.Nil(t, res.Rows[1].MatchTransactionID, "a gêmea é a linha 1 do arquivo, não um lançamento do banco")
	assert.Equal(t, 1, b.confirmar(t, arquivo, res), "só a primeira entra")
}

func TestChaveNaturalEscopadaPorConta(t *testing.T) {
	// A mesma linha, na mesma casa, em contas diferentes, tem chaves
	// diferentes: é o que permite corrigir uma importação na conta errada.
	linha := comID(despesa(1, "2026-08-04", 2000, "compra exemplo"), "uuid-01")

	na := func(accountID string) string {
		res, err := dedup.Analyze(dedup.Input{
			Institution: instituicao,
			AccountID:   accountID,
			Rows:        []dedup.Row{linha},
		})
		require.NoError(t, err)
		return res.Rows[0].DedupKey
	}

	assert.NotEqual(t, na(conta), na(outraConta))
}

// ---------------------------------------------------------------------------
// Precedência entre os status
// ---------------------------------------------------------------------------

func TestPagamentoDeFaturaEhBarradoPorDefault(t *testing.T) {
	linha := despesa(1, "2026-08-07", 285982, "pagamento de fatura")
	linha.IsCardPayment = true

	b := novoBanco()
	res := b.analisar(t, []dedup.Row{linha})

	assert.Equal(t, dedup.StatusCardPayment, res.Rows[0].Status)
	assert.False(t, res.Rows[0].Import)
	assert.True(t, res.Rows[0].Status.Releasable(), "liberável como transferência (ADR-016)")
}

func TestDuplicadoExatoGanhaDePagamentoDeFatura(t *testing.T) {
	// A garantia dura vem antes da classificação: liberar não adiantaria,
	// porque o índice único recusaria o INSERT de qualquer jeito.
	linha := despesa(1, "2026-08-07", 285982, "pagamento de fatura")
	linha.IsCardPayment = true

	b := novoBanco()
	// Grava a linha uma vez, liberando-a explicitamente.
	primeira := b.analisar(t, []dedup.Row{linha})
	require.Equal(t, 1, b.confirmarCom(t, []dedup.Row{linha}, primeira, map[int]bool{1: true}))

	segunda := b.analisar(t, []dedup.Row{linha})
	assert.Equal(t, dedup.StatusDuplicateExact, segunda.Rows[0].Status)
	assert.False(t, segunda.Rows[0].Status.Releasable())
}

func TestPagamentoDeFaturaGanhaDaMarcacaoFraca(t *testing.T) {
	// Pagamento de fatura é específico e tem uma liberação própria ("registrar
	// como transferência"), então ganha do genérico "talvez seja duplicata".
	antigo := []dedup.Row{despesa(1, "2026-08-07", 285982, "outro nome qualquer")}
	b := novoBanco()
	require.Equal(t, 1, b.confirmar(t, antigo, b.analisar(t, antigo)))

	linha := despesa(1, "2026-08-07", 285982, "pagamento de fatura")
	linha.IsCardPayment = true

	res := b.analisar(t, []dedup.Row{linha})
	assert.Equal(t, dedup.StatusCardPayment, res.Rows[0].Status)
}

// ---------------------------------------------------------------------------
// Contadores
// ---------------------------------------------------------------------------

func TestCountsTemTodosOsStatus(t *testing.T) {
	b := novoBanco()
	res := b.analisar(t, []dedup.Row{despesa(1, "2026-08-14", 1100, "cafe exemplo")})

	for _, s := range []dedup.Status{
		dedup.StatusNew, dedup.StatusRepeatedInFile, dedup.StatusDuplicateExact,
		dedup.StatusDuplicateDeleted, dedup.StatusPossibleDuplicate,
		dedup.StatusCardPayment, dedup.StatusInternalTransfer,
		dedup.StatusTransferAlreadyRegistered, dedup.StatusRejected,
	} {
		_, existe := res.Counts[s]
		assert.True(t, existe, "a resposta da API precisa de forma estável: falta %q", s)
	}
	assert.Equal(t, 1, res.Counts[dedup.StatusNew])
	assert.Equal(t, 0, res.Counts[dedup.StatusRejected], "Analyze não produz rejeitado")
}

// ---------------------------------------------------------------------------
// Entradas inválidas — defeito de programação tem de doer aqui, não no banco
// ---------------------------------------------------------------------------

func TestAnalyzeRecusaEntradaInvalida(t *testing.T) {
	linhaOK := despesa(1, "2026-08-14", 1100, "cafe exemplo")

	casos := []struct {
		nome     string
		entrada  dedup.Input
		esperado error
	}{
		{
			"sem conta",
			dedup.Input{Institution: instituicao, Rows: []dedup.Row{linhaOK}},
			dedup.ErrNoAccount,
		},
		{
			"chave natural sem instituição",
			dedup.Input{AccountID: conta, Rows: []dedup.Row{comID(linhaOK, "uuid-1")}},
			dedup.ErrNoInstitution,
		},
		{
			"conta com o separador da chave",
			dedup.Input{Institution: instituicao, AccountID: "conta|forjada", Rows: []dedup.Row{linhaOK}},
			dedup.ErrKeyFieldSeparator,
		},
		{
			"instituição com o separador da chave",
			dedup.Input{Institution: "nu|bank", AccountID: conta, Rows: []dedup.Row{linhaOK}},
			dedup.ErrKeyFieldSeparator,
		},
		{
			"linha sem ordem no arquivo",
			dedup.Input{Institution: instituicao, AccountID: conta, Rows: []dedup.Row{despesa(0, "2026-08-14", 1100, "x")}},
			dedup.ErrInvalidRow,
		},
		{
			"kind fora do domínio",
			dedup.Input{Institution: instituicao, AccountID: conta, Rows: []dedup.Row{{
				Seq: 1, Kind: transaction.KindTransferOut, OccurredOn: data("2026-08-14"), AmountCents: 1100,
			}}},
			dedup.ErrInvalidRow,
		},
		{
			"valor não positivo",
			dedup.Input{Institution: instituicao, AccountID: conta, Rows: []dedup.Row{despesa(1, "2026-08-14", 0, "x")}},
			dedup.ErrInvalidRow,
		},
		{
			"valor negativo",
			dedup.Input{Institution: instituicao, AccountID: conta, Rows: []dedup.Row{despesa(1, "2026-08-14", -1100, "x")}},
			dedup.ErrInvalidRow,
		},
		{
			"linha sem data",
			dedup.Input{Institution: instituicao, AccountID: conta, Rows: []dedup.Row{{
				Seq: 1, Kind: transaction.KindExpense, AmountCents: 1100,
			}}},
			dedup.ErrInvalidRow,
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			_, err := dedup.Analyze(c.entrada)
			require.Error(t, err)
			assert.ErrorIs(t, err, c.esperado)
		})
	}
}

func TestAnalyzeRecusaJanelaGrandeDemais(t *testing.T) {
	existentes := make([]transaction.DedupRow, dedup.MaxExistingRows+1)
	_, err := dedup.Analyze(dedup.Input{
		Institution: instituicao,
		AccountID:   conta,
		Rows:        []dedup.Row{despesa(1, "2026-08-14", 1100, "x")},
		Existing:    existentes,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, dedup.ErrWindowTooLarge)
}

func TestAnalyzeRecusaArquivoGrandeDemais(t *testing.T) {
	linhas := make([]dedup.Row, dedup.MaxFileRows+1)
	_, err := dedup.Analyze(dedup.Input{Institution: instituicao, AccountID: conta, Rows: linhas})
	require.Error(t, err)
	assert.ErrorIs(t, err, dedup.ErrTooManyRows)
}

// TestAnalyzeEhDeterministico: a ordem em que o banco devolveu a janela não
// pode mudar o veredito. Sem a ordenação interna, a mesma prévia daria
// respostas diferentes em execuções diferentes.
func TestAnalyzeEhDeterministico(t *testing.T) {
	arquivo := []dedup.Row{
		despesa(1, "2026-08-14", 1100, "cafe exemplo"),
		despesa(2, "2026-08-14", 1100, "cafe exemplo"),
	}

	b := novoBanco()
	require.Equal(t, 2, b.confirmar(t, arquivo, b.analisar(t, arquivo)))

	direto, err := dedup.Analyze(dedup.Input{
		Institution: instituicao, AccountID: conta, Rows: arquivo, Existing: b.linhas,
	})
	require.NoError(t, err)

	invertido := make([]transaction.DedupRow, len(b.linhas))
	copy(invertido, b.linhas)
	invertido[0], invertido[1] = invertido[1], invertido[0]

	reverso, err := dedup.Analyze(dedup.Input{
		Institution: instituicao, AccountID: conta, Rows: arquivo, Existing: invertido,
	})
	require.NoError(t, err)

	assert.Equal(t, direto.Rows, reverso.Rows)
}
