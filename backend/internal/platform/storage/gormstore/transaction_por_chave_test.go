package gormstore_test

import (
	"fmt"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RowsByDedupKeys é a consulta que fecha o furo da chave natural: ela procura
// PELA CHAVE, sem janela de datas, porque a chave natural não embute a data e a
// gêmea de uma linha re-datada cai fora da janela.
//
// Os quatro testes abaixo cobrem o que ela promete — e o primeiro é o mais
// importante, porque uma consulta nova é uma chance nova de esquecer o
// household_id (BOLA, docs/SEGURANCA.md §2).

func TestRowsByDedupKeysNaoAlcancaAOutraCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta da Vizinha")

		// A MESMA chave nas duas casas é o pior caso possível: se o
		// household_id faltar no WHERE, a importação da minha casa marcaria
		// como duplicada uma linha que é da vizinha — e eu ficaria sem
		// conseguir importar a minha, por causa de um dado que não posso ver.
		const chave = "chave-compartilhada-entre-casas"
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			OccurredOn: civil.MustNew(2026, 2, 10), DedupKey: chave,
		})

		linhas, err := s.transactions.RowsByDedupKeys(ctx, minha.ID, []string{chave})
		require.NoError(t, err)
		assert.Empty(t, linhas, "chave de outra casa não pode voltar")

		// E a casa vazia é ERRO, nunca "consulta sem filtro".
		_, err = s.transactions.RowsByDedupKeys(ctx, "", []string{chave})
		require.Error(t, err)
	})
}

func TestRowsByDedupKeysIgnoraAJanelaDeDatasEEnxergaOExcluido(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Nubank Conta")

		externo := "id-do-banco-42"
		longe := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			OccurredOn: civil.MustNew(2026, 1, 5), DedupKey: "chave-natural-42",
			ExternalID: &externo,
		})
		apagada := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			OccurredOn: civil.MustNew(2025, 11, 30), DedupKey: "chave-natural-43",
		})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, apagada.ID, now()))

		// A janela de agosto não enxerga nenhuma das duas — é exatamente a
		// situação em que a linha voltava como `novo` e entrava de novo.
		janela, err := s.transactions.WindowForDedup(ctx, minha.ID, conta.ID,
			civil.MustNew(2026, 8, 1), civil.MustNew(2026, 8, 31))
		require.NoError(t, err)
		assert.Empty(t, janela)

		linhas, err := s.transactions.RowsByDedupKeys(ctx, minha.ID,
			[]string{"chave-natural-42", "chave-natural-43"})
		require.NoError(t, err)
		require.Len(t, linhas, 2, "a busca por chave não olha data nenhuma")

		porID := map[string]transaction.DedupKeyRow{}
		for _, l := range linhas {
			porID[l.ID] = l
		}

		require.Contains(t, porID, longe.ID)
		assert.Equal(t, "chave-natural-42", porID[longe.ID].DedupKey)
		assert.Equal(t, 1, porID[longe.ID].DedupOrdinal)
		assert.Nil(t, porID[longe.ID].DeletedAt)

		require.Contains(t, porID, apagada.ID)
		assert.NotNil(t, porID[apagada.ID].DeletedAt,
			"a excluída continua ocupando a chave única — é ela que vira RESTAURAÇÃO")
	})
}

// A lista de chaves é fatiada porque o IN (...) vira um parâmetro por chave, e o
// teto por comando é 2100 no SQL Server e 999 no SQLite. Uma fatura grande passa
// desse teto sozinha.
func TestRowsByDedupKeysFatiaListaGrande(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Nubank Conta")

		const total = 1200 // acima dos 999 do SQLite e de vários blocos de 200
		chaves := make([]string, 0, total)
		for i := range total {
			chave := fmt.Sprintf("chave-em-lote-%04d", i)
			chaves = append(chaves, chave)
		}
		// Só três existem de verdade: o teste é sobre a consulta aguentar a
		// lista, não sobre o volume de dados.
		for _, i := range []int{0, 600, total - 1} {
			s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
				OccurredOn: civil.MustNew(2026, 2, 10), DedupKey: chaves[i],
			})
		}

		linhas, err := s.transactions.RowsByDedupKeys(ctx, minha.ID, chaves)
		require.NoError(t, err)
		assert.Len(t, linhas, 3)
	})
}

func TestRowsByDedupKeysSemChaveNaoConsultaNada(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)

		linhas, err := s.transactions.RowsByDedupKeys(ctx, minha.ID, nil)
		require.NoError(t, err)
		assert.Empty(t, linhas)

		// Chave vazia é descartada: ela casaria com dedup_key = "", que não
		// existe por ser NOT NULL — mas mandar a consulta assim mesmo seria
		// gastar uma ida ao banco por engano.
		linhas, err = s.transactions.RowsByDedupKeys(ctx, minha.ID, []string{"", ""})
		require.NoError(t, err)
		assert.Empty(t, linhas)
	})
}
