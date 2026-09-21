package dedup_test

import (
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Testes de UNIDADE da fusão de ExistingByKey — as ocorrências achadas pela
// chave natural FORA da janela de datas.
//
// O ponta a ponta prova a fiação; estes provam a regra, e provam em especial os
// dois jeitos de errar a fusão:
//
//   - não fundir: a gêmea re-datada não é vista, a linha volta `novo` (que entra
//     por DEFAULT) e a mesma transação é gravada duas vezes;
//   - fundir sem deduplicar por ID: a MESMA gêmea entra duas vezes na conta de
//     ocorrências (ela vem pela janela E pela chave), e a segunda compra
//     legítima de uma tupla repetida passa a ser marcada como duplicata — um
//     gasto real somindo, que é o erro que este pacote considera o pior.

// linhaNatural monta uma linha de arquivo com Identificador (chave natural).
func linhaNatural(seq int, dia string, cents int64, externo string) dedup.Row {
	return dedup.Row{
		Seq:             seq,
		Kind:            transaction.KindExpense,
		OccurredOn:      data(dia),
		AmountCents:     cents,
		DescriptionNorm: "mercado exemplo",
		ExternalID:      ptr(externo),
	}
}

func TestExistingByKeyMarcaGemeaForaDaJanela(t *testing.T) {
	t.Parallel()

	linha := linhaNatural(1, "2026-06-01", 2000, "id-42")
	chave := dedup.NaturalKey(instituicao, conta, "id-42")

	res, err := dedup.Analyze(dedup.Input{
		Institution: instituicao,
		AccountID:   conta,
		Rows:        []dedup.Row{linha},
		// A janela de datas está VAZIA — é o que o repositório devolve quando a
		// gêmea foi gravada com outra data, longe daqui.
		Existing: nil,
		ExistingByKey: []transaction.DedupKeyRow{
			{ID: "tx-1", DedupKey: chave, DedupOrdinal: 1},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)

	assert.Equal(t, dedup.StatusDuplicateExact, res.Rows[0].Status)
	assert.False(t, res.Rows[0].Import, "duplicado_exato não entra por default")
	require.NotNil(t, res.Rows[0].MatchTransactionID)
	assert.Equal(t, "tx-1", *res.Rows[0].MatchTransactionID)
	assert.Equal(t, 2, res.Rows[0].Ordinal,
		"o ordinal reservado continua sendo max+1; quem impede a gravação é o status, "+
			"e a recusa de ordinal > 1 em chave natural no serviço de lançamentos")
}

func TestExistingByKeyComGemeaExcluidaViraDuplicadoExcluido(t *testing.T) {
	t.Parallel()

	excluida := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	chave := dedup.NaturalKey(instituicao, conta, "id-43")

	res, err := dedup.Analyze(dedup.Input{
		Institution: instituicao,
		AccountID:   conta,
		Rows:        []dedup.Row{linhaNatural(1, "2026-06-01", 7730, "id-43")},
		ExistingByKey: []transaction.DedupKeyRow{
			{ID: "tx-2", DedupKey: chave, DedupOrdinal: 1, DeletedAt: &excluida},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)

	assert.Equal(t, dedup.StatusDuplicateDeleted, res.Rows[0].Status,
		"a linha excluída continua ocupando a chave — e liberar a linha RESTAURA a existente")
	assert.False(t, res.Rows[0].Import)
}

// A gêmea que está DENTRO da janela vem pelas DUAS fontes. Contá-la duas vezes
// seria transformar a 2ª ocorrência legítima em duplicata.
func TestExistingByKeyNaoContaAMesmaGemeaDuasVezes(t *testing.T) {
	t.Parallel()

	chave := dedup.NaturalKey(instituicao, conta, "id-44")
	janela := []transaction.DedupRow{{
		ID:              "tx-3",
		OccurredOn:      data("2026-08-04"),
		AmountCents:     2000,
		Kind:            transaction.KindExpense,
		DescriptionNorm: "mercado exemplo",
		ExternalID:      ptr("id-44"),
		DedupKey:        chave,
		DedupOrdinal:    1,
	}}

	res, err := dedup.Analyze(dedup.Input{
		Institution: instituicao,
		AccountID:   conta,
		Rows:        []dedup.Row{linhaNatural(1, "2026-08-04", 2000, "id-44")},
		Existing:    janela,
		// A MESMA linha, agora vinda da busca por chave.
		ExistingByKey: []transaction.DedupKeyRow{
			{ID: "tx-3", DedupKey: chave, DedupOrdinal: 1},
		},
	})
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)

	assert.Equal(t, dedup.StatusDuplicateExact, res.Rows[0].Status)
	assert.Equal(t, 2, res.Rows[0].Ordinal,
		"UMA ocorrência existente, não duas: o ordinal reservado é 2, e não 3")
}

// NaturalKeys é o que o serviço pergunta ao repositório. Ela existe no pacote da
// chave para haver UM caminho de cálculo: um segundo caminho um dia diverge, e
// chave divergente não dá erro — ela só para de encontrar a gêmea.
func TestNaturalKeysDevolveAsChavesDistintasDasLinhasComIdentificador(t *testing.T) {
	t.Parallel()

	linhas := []dedup.Row{
		linhaNatural(1, "2026-08-04", 2000, "id-1"),
		linhaNatural(2, "2026-08-05", 3000, "id-2"),
		// Repetida: a mesma chave não é perguntada duas vezes.
		linhaNatural(3, "2026-08-06", 4000, "id-1"),
		// Sem identificador: chave DERIVADA, que embute a data e não precisa
		// desta busca.
		{Seq: 4, Kind: transaction.KindExpense, OccurredOn: data("2026-08-07"), AmountCents: 500},
	}

	chaves := dedup.NaturalKeys(instituicao, conta, linhas)
	require.Len(t, chaves, 2)
	assert.Equal(t, dedup.NaturalKey(instituicao, conta, "id-1"), chaves[0])
	assert.Equal(t, dedup.NaturalKey(instituicao, conta, "id-2"), chaves[1])

	// Sem instituição não há chave natural possível — e Analyze recusa a
	// entrada logo em seguida, com ErrNoInstitution.
	assert.Empty(t, dedup.NaturalKeys("", conta, linhas))
	assert.Empty(t, dedup.NaturalKeys(instituicao, "", linhas))

	// A invariante do separador: campo do meio com "|" tornaria a concatenação
	// ambígua, e duas linhas diferentes virariam a mesma chave.
	assert.Empty(t, dedup.NaturalKeys("nu|bank", conta, linhas))
	assert.Empty(t, dedup.NaturalKeys(instituicao, "con|ta", linhas))
}

// O teto de memória vale para as duas fontes: o que é carregado tem limite.
func TestExistingByKeyRespeitaOTetoDeLinhas(t *testing.T) {
	t.Parallel()

	demais := make([]transaction.DedupKeyRow, dedup.MaxExistingRows+1)
	for i := range demais {
		demais[i] = transaction.DedupKeyRow{ID: "tx", DedupKey: "chave", DedupOrdinal: 1}
	}

	_, err := dedup.Analyze(dedup.Input{
		Institution:   instituicao,
		AccountID:     conta,
		Rows:          []dedup.Row{linhaNatural(1, "2026-08-04", 2000, "id-45")},
		ExistingByKey: demais,
	})
	require.ErrorIs(t, err, dedup.ErrWindowTooLarge)
}
