package dedup_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Precedência de `transferencia_interna` (spec 0005 §4.2.1 e emenda §10.1):
// entra onde a linha ENTRARIA por default, perde para colisão dura e para
// pagamento de fatura, e ganha da marcação fraca — como o pagamento de fatura
// já ganha.

func TestTransferenciaInternaEntraOndeALinhaSeriaNova(t *testing.T) {
	linha := despesa(1, "2026-08-05", 150000, "transferencia enviada pelo pix itau")
	linha.IsInternalTransfer = true

	b := novoBanco()
	res := b.analisar(t, []dedup.Row{linha})

	require.Len(t, res.Rows, 1)
	assert.Equal(t, dedup.StatusInternalTransfer, res.Rows[0].Status)
	assert.False(t, res.Rows[0].Import, "barrada por default: a pessoa escolhe transfer, import ou skip")
	assert.True(t, res.Rows[0].Status.Releasable())
	assert.Nil(t, res.Rows[0].MatchTransactionID, "Analyze não pareia com perna existente")
	assert.Equal(t, 1, res.Counts[dedup.StatusInternalTransfer])
}

func TestTransferenciaInternaTambemNaRepetidaNoArquivo(t *testing.T) {
	// Emenda §10.1: três linhas idênticas de chave DERIVADA seriam `novo,
	// repetido, repetido` — e as três precisam ser elegíveis, senão o critério
	// 6 (duas pernas distintas + a terceira como transferência) falha.
	var linhas []dedup.Row
	for seq := 1; seq <= 3; seq++ {
		l := despesa(seq, "2026-08-05", 150000, "pix enviado itau")
		l.IsInternalTransfer = true
		linhas = append(linhas, l)
	}

	res := novoBanco().analisar(t, linhas)

	require.Len(t, res.Rows, 3)
	for i := range res.Rows {
		assert.Equal(t, dedup.StatusInternalTransfer, res.Rows[i].Status, "linha %d", i+1)
		assert.Equal(t, i+1, res.Rows[i].Ordinal, "o ordinal continua reservado por ocorrência")
	}
	assert.Equal(t, 3, res.Counts[dedup.StatusInternalTransfer])
	assert.Equal(t, 0, res.Counts[dedup.StatusRepeatedInFile])
}

func TestDuplicadoExatoGanhaDeTransferenciaInterna(t *testing.T) {
	linha := despesa(1, "2026-08-05", 150000, "pix enviado itau")
	linha.IsInternalTransfer = true

	b := novoBanco()
	primeira := b.analisar(t, []dedup.Row{linha})
	require.Equal(t, 1, b.confirmarCom(t, []dedup.Row{linha}, primeira, map[int]bool{1: true}))

	segunda := b.analisar(t, []dedup.Row{linha})
	assert.Equal(t, dedup.StatusDuplicateExact, segunda.Rows[0].Status,
		"colisão dura vence: liberar não adiantaria, o índice único recusaria")
	require.NotNil(t, segunda.Rows[0].MatchTransactionID)
}

func TestPagamentoDeFaturaGanhaDeTransferenciaInterna(t *testing.T) {
	// As duas marcações juntas: a palavra-chave do cartão bateu na linha
	// "pagamento de fatura". O status fica `pagamento_de_fatura` — mais
	// específico, com a liberação própria; a contraparte sugerida vive no
	// importer, fora deste pacote.
	linha := despesa(1, "2026-08-07", 285982, "pagamento de fatura")
	linha.IsCardPayment = true
	linha.IsInternalTransfer = true

	res := novoBanco().analisar(t, []dedup.Row{linha})
	assert.Equal(t, dedup.StatusCardPayment, res.Rows[0].Status)
	assert.Equal(t, 0, res.Counts[dedup.StatusInternalTransfer])
}

func TestTransferenciaInternaGanhaDaMarcacaoFraca(t *testing.T) {
	// Mesmo valor, data próxima, descrição diferente já gravada: sem a
	// palavra-chave a linha seria `possivel_duplicado`. Com ela, a
	// classificação específica (que tem a liberação que resolve o caso)
	// vence a heurística, exatamente como o pagamento de fatura.
	antigo := []dedup.Row{despesa(1, "2026-08-05", 150000, "outro nome qualquer")}
	b := novoBanco()
	require.Equal(t, 1, b.confirmar(t, antigo, b.analisar(t, antigo)))

	linha := despesa(1, "2026-08-06", 150000, "pix enviado itau")
	linha.IsInternalTransfer = true

	res := b.analisar(t, []dedup.Row{linha})
	assert.Equal(t, dedup.StatusInternalTransfer, res.Rows[0].Status)
	assert.Nil(t, res.Rows[0].MatchTransactionID)
}

func TestSemMarcacaoALinhaContinuaNova(t *testing.T) {
	// O campo é opt-in: quem não marca não muda nada da spec 0004.
	res := novoBanco().analisar(t, []dedup.Row{despesa(1, "2026-08-05", 150000, "pix enviado itau")})
	assert.Equal(t, dedup.StatusNew, res.Rows[0].Status)
	assert.True(t, res.Rows[0].Import)
}
