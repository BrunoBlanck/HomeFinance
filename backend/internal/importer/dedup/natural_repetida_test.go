package dedup_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
)

// Achado 10 da revisão da E2 — chave NATURAL repetida dentro do arquivo.
//
// A regra, em uma frase: "segunda ocorrência legítima" só existe na chave
// derivada. Na natural, o identificador do emissor é único por transação, e a
// classificação tem de dizer o mesmo que a escrita (transaction.Service
// recusa ordinal > 1 em chave natural) — senão a prévia libera a linha por
// default, a escrita a recusa e o lote inteiro volta atrás.

// TestChaveNaturalRepetidaGanhaDePagamentoDeFatura: a repetição do
// identificador é garantia dura e vem antes da heurística. Se o pagamento de
// fatura ganhasse, a 2ª linha ficaria liberável ("importar como despesa mesmo
// assim") — e liberá-la faria a escrita recusar o lote.
func TestChaveNaturalRepetidaGanhaDePagamentoDeFatura(t *testing.T) {
	pagamento := comID(despesa(1, "2026-08-07", 285982, "pagamento de fatura"), "uuid-07")
	pagamento.IsCardPayment = true
	repetida := pagamento
	repetida.Seq = 2

	b := novoBanco()
	res := b.analisar(t, []dedup.Row{pagamento, repetida})

	assert.Equal(t, []dedup.Status{dedup.StatusCardPayment, dedup.StatusDuplicateExact}, statusDe(res))
	assert.False(t, res.Rows[1].Import)
	assert.False(t, res.Rows[1].Status.Releasable())
}

// TestChaveNaturalRepetidaComGemeaNoBanco: o banco já tem a transação, e o
// arquivo a traz duas vezes. A 1ª casa com o banco (e aponta para ele); a 2ª
// é barrada pelo mesmo motivo, sem gêmea para apontar.
func TestChaveNaturalRepetidaComGemeaNoBanco(t *testing.T) {
	linha := comID(despesa(1, "2026-08-04", 2000, "mercado exemplo"), "uuid-01")

	b := novoBanco()
	require.Equal(t, 1, b.confirmar(t, []dedup.Row{linha}, b.analisar(t, []dedup.Row{linha})))

	segunda := linha
	segunda.Seq = 2
	res := b.analisar(t, []dedup.Row{linha, segunda})

	assert.Equal(t, []dedup.Status{dedup.StatusDuplicateExact, dedup.StatusDuplicateExact}, statusDe(res))
	require.NotNil(t, res.Rows[0].MatchTransactionID, "a 1ª aponta para o lançamento gravado")
	assert.Equal(t, "tx-001", *res.Rows[0].MatchTransactionID)
	assert.Nil(t, res.Rows[1].MatchTransactionID)
	assert.Equal(t, 0, res.Importable())
	assert.Equal(t, 0, b.confirmar(t, []dedup.Row{linha, segunda}, res))
	assert.Len(t, b.linhas, 1, "continua UMA transação")
}

// TestChaveNaturalRepetidaNaoContaComoRepetidoNoArquivo: os contadores da
// resposta são o que a tela resume — a repetição do identificador tem de cair
// em `duplicado_exato`, e `repetido_no_arquivo` tem de ficar em zero.
func TestChaveNaturalRepetidaNaoContaComoRepetidoNoArquivo(t *testing.T) {
	arquivo := []dedup.Row{
		comID(despesa(1, "2026-08-04", 2000, "mercado exemplo"), "uuid-01"),
		comID(despesa(2, "2026-08-04", 2000, "mercado exemplo"), "uuid-01"),
		comID(despesa(3, "2026-08-04", 2000, "mercado exemplo"), "uuid-01"),
		comID(despesa(4, "2026-08-06", 990, "padaria exemplo"), "uuid-02"),
	}

	res := novoBanco().analisar(t, arquivo)

	assert.Equal(t, 2, res.Counts[dedup.StatusNew])
	assert.Equal(t, 2, res.Counts[dedup.StatusDuplicateExact], "a 2ª E a 3ª ocorrência")
	assert.Equal(t, 0, res.Counts[dedup.StatusRepeatedInFile])
	assert.Equal(t, 2, res.Importable())
}

// TestChaveDerivadaRepetidaNoArquivoContinuaEntrando é o CONTROLE da regra
// acima, com a MESMA tupla — só que sem identificador. Aqui a 2ª ocorrência
// continua sendo `repetido_no_arquivo`, e entra: dois cafés iguais no mesmo dia
// são dois gastos reais (cenário B). Se este teste quebrar, a correção da
// chave natural vazou para a derivada — e passou a engolir gasto real.
func TestChaveDerivadaRepetidaNoArquivoContinuaEntrando(t *testing.T) {
	arquivo := []dedup.Row{
		despesa(1, "2026-08-04", 2000, "mercado exemplo"),
		despesa(2, "2026-08-04", 2000, "mercado exemplo"),
	}

	b := novoBanco()
	res := b.analisar(t, arquivo)

	assert.Equal(t, []dedup.Status{dedup.StatusNew, dedup.StatusRepeatedInFile}, statusDe(res))
	assert.True(t, res.Rows[1].Import, "a 2ª ocorrência da chave DERIVADA entra por default")
	assert.Equal(t, 2, res.Rows[1].Ordinal)
	assert.Equal(t, 2, b.confirmar(t, arquivo, res), "as duas entram")
}
