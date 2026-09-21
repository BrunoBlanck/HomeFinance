package transaction

import (
	"context"
	"fmt"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tabela do pareamento PURO (spec 0005 §13.3 e §13.5, ADR-028c): sem banco,
// sem matcher — a pontuação de cada linha entra pronta, e o que se prova é a
// regra de contraparte, o desempate e a reivindicação.

// linha monta uma TransferCandidateRow com o mínimo que o pareamento olha.
func linha(id, conta, kind string, cents int64, dia int, descricao string) TransferCandidateRow {
	return TransferCandidateRow{
		ID: id, AccountID: conta, Kind: kind, AmountCents: cents,
		OccurredOn: civil.MustNew(2026, 8, dia), Description: descricao, DescriptionNorm: descricao,
	}
}

// pontua registra que a linha `id` bateu com palavra da conta `dona`.
func pontua(m map[string]textmatch.Match, id, dona, palavra string, score int) map[string]textmatch.Match {
	m[id] = textmatch.Match{OwnerID: dona, Keyword: palavra, Score: score}
	return m
}

func TestParearTransferenciasSegueAsRegrasDeContraparte(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6", "C": "Inter"}
	// Conta arquivada: fora de `ativas`.
	const arquivada = "Z"

	type par struct{ out, in string }
	casos := []struct {
		nome       string
		candidatas []TransferCandidateRow
		pool       []TransferCandidateRow
		pontuacoes map[string]textmatch.Match
		pares      []par
		semPar     []string
	}{
		{
			nome: "critério 1: própria conta em cada uma → pareia",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "pix enviado bruno"),
				linha("t2", "B", KindIncome, 300_00, 25, "pix recebido bruno"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "pix enviado bruno"),
				linha("t2", "B", KindIncome, 300_00, 25, "pix recebido bruno"),
			},
			pontuacoes: pontua(pontua(map[string]textmatch.Match{}, "t1", "A", "pix enviado bruno", 100), "t2", "B", "pix recebido bruno", 100),
			pares:      []par{{"t1", "t2"}},
		},
		{
			nome: "critério 2: palavra de OUTRA conta K → espelho só em K; terceira conta é ignorada",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 100_00, 10, "ted para c6"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 100_00, 10, "ted para c6"),
				linha("m-c", "C", KindIncome, 100_00, 10, "ted recebida"), // mais perto, mas em C
				linha("m-b", "B", KindIncome, 100_00, 12, "ted recebida"), // em K = B, sem palavra
			},
			pontuacoes: pontua(map[string]textmatch.Match{}, "t1", "B", "c6", 100),
			pares:      []par{{"t1", "m-b"}},
		},
		{
			nome: "critério 2b: palavra de K mas nada em K → sem par, mesmo com receita igual em C",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 100_00, 10, "ted para c6"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 100_00, 10, "ted para c6"),
				linha("m-c", "C", KindIncome, 100_00, 10, "ted recebida"),
			},
			pontuacoes: pontua(map[string]textmatch.Match{}, "t1", "B", "c6", 100),
			semPar:     []string{"t1"},
		},
		{
			nome: "critério 3: própria conta em T e receita SEM palavra em outra conta → não pareia",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "pix enviado bruno"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "pix enviado bruno"),
				linha("m", "B", KindIncome, 300_00, 25, "salario"),
			},
			pontuacoes: pontua(map[string]textmatch.Match{}, "t1", "A", "pix enviado bruno", 100),
			semPar:     []string{"t1"},
		},
		{
			nome: "critério 3b: a mesma receita COM palavra da própria conta → pareia",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "pix enviado bruno"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "pix enviado bruno"),
				linha("m", "B", KindIncome, 300_00, 25, "pix recebido bruno"),
			},
			pontuacoes: pontua(pontua(map[string]textmatch.Match{}, "t1", "A", "pix enviado bruno", 100), "m", "B", "pix recebido", 100),
			pares:      []par{{"t1", "m"}},
		},
		{
			nome: "critério 3c: própria conta em T e o espelho aponta para a conta de T → pareia",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "pix enviado bruno"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "pix enviado bruno"),
				linha("m", "B", KindIncome, 300_00, 26, "pix de nubank"),
			},
			pontuacoes: pontua(pontua(map[string]textmatch.Match{}, "t1", "A", "pix enviado bruno", 100), "m", "A", "nubank", 100),
			pares:      []par{{"t1", "m"}},
		},
		{
			nome: "critério 3d: própria conta em T e o espelho aponta para uma TERCEIRA conta → não pareia",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "pix enviado bruno"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "pix enviado bruno"),
				linha("m", "B", KindIncome, 300_00, 25, "pix de inter"),
			},
			pontuacoes: pontua(pontua(map[string]textmatch.Match{}, "t1", "A", "pix enviado bruno", 100), "m", "C", "inter", 100),
			semPar:     []string{"t1"},
		},
		{
			nome: "critério 4: sem espelho nenhum → unpaired, nada inventado",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "pix enviado bruno"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "pix enviado bruno"),
			},
			pontuacoes: pontua(map[string]textmatch.Match{}, "t1", "A", "pix enviado bruno", 100),
			semPar:     []string{"t1"},
		},
		{
			nome: "critério 5: duas iguais no mesmo dia → dois pares distintos; a terceira fica sem par",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 50_00, 5, "pix enviado bruno"),
				linha("t2", "A", KindExpense, 50_00, 5, "pix enviado bruno"),
				linha("t3", "A", KindExpense, 50_00, 5, "pix enviado bruno"),
				linha("m1", "B", KindIncome, 50_00, 5, "pix recebido bruno"),
				linha("m2", "B", KindIncome, 50_00, 5, "pix recebido bruno"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 50_00, 5, "pix enviado bruno"),
				linha("t2", "A", KindExpense, 50_00, 5, "pix enviado bruno"),
				linha("t3", "A", KindExpense, 50_00, 5, "pix enviado bruno"),
				linha("m1", "B", KindIncome, 50_00, 5, "pix recebido bruno"),
				linha("m2", "B", KindIncome, 50_00, 5, "pix recebido bruno"),
			},
			pontuacoes: pontua(pontua(pontua(pontua(pontua(map[string]textmatch.Match{},
				"t1", "A", "pix enviado bruno", 100), "t2", "A", "pix enviado bruno", 100), "t3", "A", "pix enviado bruno", 100),
				"m1", "B", "pix recebido bruno", 100), "m2", "B", "pix recebido bruno", 100),
			// Ordem (occurred_on, id): m1 e m2 são candidatas também, mas t1
			// (id menor) vem antes e reivindica m1; t2 reivindica m2; t3 fica;
			// m1 e m2 já foram reivindicadas e não são percorridas de novo.
			pares:  []par{{"t1", "m1"}, {"t2", "m2"}},
			semPar: []string{"t3"},
		},
		{
			nome: "conta arquivada nunca: nem como candidata nem como espelho",
			candidatas: []TransferCandidateRow{
				linha("t-arq", arquivada, KindExpense, 10_00, 1, "pix enviado bruno"),
				linha("t1", "A", KindExpense, 20_00, 2, "pix enviado bruno"),
			},
			pool: []TransferCandidateRow{
				linha("t-arq", arquivada, KindExpense, 10_00, 1, "pix enviado bruno"),
				linha("m-arq", arquivada, KindIncome, 20_00, 2, "pix recebido bruno"),
				linha("t1", "A", KindExpense, 20_00, 2, "pix enviado bruno"),
			},
			pontuacoes: pontua(pontua(pontua(map[string]textmatch.Match{},
				"t-arq", "A", "nubank", 100), "t1", "A", "pix enviado bruno", 100), "m-arq", arquivada, "x", 100),
			// t-arq não é candidata (conta inativa) e não conta como sem par;
			// t1 não acha espelho porque o único está em conta arquivada.
			semPar: []string{"t1"},
		},
		{
			nome: "espelho de OUTRA competência é encontrado: o pool é por occurred_on, não por mês",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 31, "pagamento fatura c6"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 31, "pagamento fatura c6"),
				// A linha da fatura tem occurred_on em 2026-09-02 e competência
				// setembro — está no pool porque a janela é por data.
				{ID: "m", AccountID: "B", Kind: KindIncome, AmountCents: 300_00,
					OccurredOn: civil.MustNew(2026, 9, 2), Description: "pagamento recebido", DescriptionNorm: "pagamento recebido"},
			},
			pontuacoes: pontua(map[string]textmatch.Match{}, "t1", "B", "c6", 100),
			pares:      []par{{"t1", "m"}},
		},
		{
			nome: "empate de pontuação → não é candidata (não está em pontuacoes) e não é contada",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "ted inter c6"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 300_00, 25, "ted inter c6"),
				linha("m", "B", KindIncome, 300_00, 25, "ted"),
			},
			pontuacoes: map[string]textmatch.Match{},
		},
		{
			nome: "fora da folga de 3 dias não é espelho; dentro, é",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 100_00, 10, "ted para c6"),
				linha("t2", "A", KindExpense, 200_00, 10, "ted para c6"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 100_00, 10, "ted para c6"),
				linha("t2", "A", KindExpense, 200_00, 10, "ted para c6"),
				linha("m-longe", "B", KindIncome, 100_00, 14, "ted"), // 4 dias
				linha("m-perto", "B", KindIncome, 200_00, 13, "ted"), // 3 dias
			},
			pontuacoes: pontua(pontua(map[string]textmatch.Match{}, "t1", "B", "c6", 100), "t2", "B", "c6", 100),
			pares:      []par{{"t2", "m-perto"}},
			semPar:     []string{"t1"},
		},
		{
			nome: "mesmo sentido, mesma conta ou valor diferente nunca é espelho",
			candidatas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 100_00, 10, "ted para c6"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 100_00, 10, "ted para c6"),
				linha("m-mesmo-sentido", "B", KindExpense, 100_00, 10, "ted"),
				linha("m-mesma-conta", "A", KindIncome, 100_00, 10, "ted"),
				linha("m-outro-valor", "B", KindIncome, 100_01, 10, "ted"),
				linha("m-transfer", "B", KindTransferIn, 100_00, 10, "ted"),
			},
			pontuacoes: pontua(map[string]textmatch.Match{}, "t1", "B", "c6", 100),
			semPar:     []string{"t1"},
		},
		{
			nome: "a candidata income vira a perna de ENTRADA: o sentido vem do kind",
			candidatas: []TransferCandidateRow{
				linha("t1", "B", KindIncome, 300_00, 25, "pix recebido de nubank"),
			},
			pool: []TransferCandidateRow{
				linha("t1", "B", KindIncome, 300_00, 25, "pix recebido de nubank"),
				linha("m", "A", KindExpense, 300_00, 24, "pix enviado"),
			},
			pontuacoes: pontua(map[string]textmatch.Match{}, "t1", "A", "nubank", 100),
			pares:      []par{{"m", "t1"}},
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			plano, errPareamento := parearTransferencias(t.Context(), c.candidatas, c.pool, ativas, c.pontuacoes)
			require.NoError(t, errPareamento)

			pares := make([]par, 0, len(plano.pares))
			for _, p := range plano.pares {
				pares = append(pares, par{p.outID, p.inID})
			}
			if c.pares == nil {
				c.pares = []par{}
			}
			assert.Equal(t, c.pares, pares, "pares")

			semPar := make([]string, 0, len(plano.unpairedItems))
			for _, u := range plano.unpairedItems {
				semPar = append(semPar, u.ID)
				assert.Equal(t, TransferUnpairedNoMirror, u.Reason)
			}
			if c.semPar == nil {
				c.semPar = []string{}
			}
			assert.Equal(t, c.semPar, semPar, "sem par")
			assert.EqualValues(t, len(c.semPar), plano.unpaired)
			assert.Len(t, plano.items, len(c.pares))
			assert.NotNil(t, plano.items)
			assert.NotNil(t, plano.unpairedItems)

			// Determinístico: a mesma entrada dá sempre a mesma saída.
			denovo, err := parearTransferencias(t.Context(), c.candidatas, c.pool, ativas, c.pontuacoes)
			require.NoError(t, err)
			assert.Equal(t, plano, denovo)
		})
	}
}

// O item da prévia descreve a perna de SAÍDA (data, descrição, de → para) e a
// candidata que DECIDIU (palavra e pontuação) — que pode ser a perna de
// entrada.
func TestParearTransferenciasMontaOItemPelaPernaDeSaidaEPelaCandidata(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6"}
	candidatas := []TransferCandidateRow{
		linha("t-in", "B", KindIncome, 300_00, 26, "Pix recebido de Bruno"),
	}
	pool := []TransferCandidateRow{
		linha("t-in", "B", KindIncome, 300_00, 26, "Pix recebido de Bruno"),
		linha("m-out", "A", KindExpense, 300_00, 25, "Pix enviado - BRUNO"),
	}
	// T é "própria conta" (a dona da pontuação é a conta dela), então o
	// espelho também precisa qualificar-se — aqui, pela palavra da conta dele.
	pontuacoes := pontua(pontua(map[string]textmatch.Match{},
		"t-in", "B", "pix recebido", 88), "m-out", "A", "pix enviado", 100)

	plano, errPareamento := parearTransferencias(t.Context(), candidatas, pool, ativas, pontuacoes)
	require.NoError(t, errPareamento)
	require.Len(t, plano.items, 1)
	item := plano.items[0]
	assert.Equal(t, "m-out", item.OutTransactionID)
	assert.Equal(t, "t-in", item.InTransactionID)
	assert.Equal(t, civil.MustNew(2026, 8, 25), item.OccurredOn, "data da perna de saída")
	assert.Equal(t, "Pix enviado - BRUNO", item.Description, "descrição da perna de saída")
	assert.Equal(t, "A", item.FromAccountID)
	assert.Equal(t, "Nubank", item.FromAccountName)
	assert.Equal(t, "B", item.ToAccountID)
	assert.Equal(t, "C6", item.ToAccountName)
	assert.EqualValues(t, 300_00, item.AmountCents)
	assert.Equal(t, "pix recebido", item.MatchedKeyword, "a palavra é da candidata que decidiu")
	assert.Equal(t, 88, item.MatchScore)
}

// Desempate entre vários espelhos: quem também se qualifica vence mesmo
// estando mais longe; entre iguais, menor distância; depois menor data; depois
// menor id.
func TestParearTransferenciasDesempataDeFormaDeterministica(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6"}
	t1 := linha("t1", "A", KindExpense, 100_00, 10, "ted para c6")
	pontuacoes := pontua(map[string]textmatch.Match{}, "t1", "B", "c6", 100)

	t.Run("prefere o espelho que se qualifica, mesmo mais longe", func(t *testing.T) {
		t.Parallel()
		pool := []TransferCandidateRow{
			t1,
			linha("m-perto", "B", KindIncome, 100_00, 10, "salario"),
			linha("m-longe", "B", KindIncome, 100_00, 13, "ted de nubank"),
		}
		p := pontua(map[string]textmatch.Match{"t1": pontuacoes["t1"]}, "m-longe", "A", "nubank", 100)
		plano, errPareamento := parearTransferencias(t.Context(), []TransferCandidateRow{t1}, pool, ativas, p)
		require.NoError(t, errPareamento)
		require.Len(t, plano.pares, 1)
		assert.Equal(t, "m-longe", plano.pares[0].inID)
	})

	t.Run("sem qualificação, menor distância", func(t *testing.T) {
		t.Parallel()
		pool := []TransferCandidateRow{
			t1,
			linha("m-13", "B", KindIncome, 100_00, 13, "x"),
			linha("m-11", "B", KindIncome, 100_00, 11, "x"),
			linha("m-09", "B", KindIncome, 100_00, 9, "x"),
		}
		plano, errPareamento := parearTransferencias(t.Context(), []TransferCandidateRow{t1}, pool, ativas, pontuacoes)
		require.NoError(t, errPareamento)
		require.Len(t, plano.pares, 1)
		assert.Equal(t, "m-09", plano.pares[0].inID, "1 dia antes ganha de 1 dia depois pelo occurred_on menor")
	})

	t.Run("mesma distância e data, menor id", func(t *testing.T) {
		t.Parallel()
		pool := []TransferCandidateRow{
			t1,
			linha("m-b", "B", KindIncome, 100_00, 10, "x"),
			linha("m-a", "B", KindIncome, 100_00, 10, "x"),
		}
		plano, errPareamento := parearTransferencias(t.Context(), []TransferCandidateRow{t1}, pool, ativas, pontuacoes)
		require.NoError(t, errPareamento)
		require.Len(t, plano.pares, 1)
		assert.Equal(t, "m-a", plano.pares[0].inID)
	})
}

// As listas param no teto; as contagens são completas; nada é nulo.
func TestParearTransferenciasListasParamNoTetoMasContagensSaoCompletas(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6"}
	total := MaxTransferDetectListed + 3
	var candidatas, pool []TransferCandidateRow
	pontuacoes := map[string]textmatch.Match{}
	for i := 0; i < total; i++ {
		// Um par por valor distinto, e uma candidata sem par por valor.
		cents := int64(i + 1)
		tp := linha(fmt.Sprintf("t-%05d", i), "A", KindExpense, cents, 10, "ted para c6")
		m := linha(fmt.Sprintf("m-%05d", i), "B", KindIncome, cents, 10, "x")
		sem := linha(fmt.Sprintf("s-%05d", i), "A", KindExpense, cents+100_000, 10, "ted para c6")
		candidatas = append(candidatas, tp, sem)
		pool = append(pool, tp, m, sem)
		pontua(pontuacoes, tp.ID, "B", "c6", 100)
		pontua(pontuacoes, sem.ID, "B", "c6", 100)
	}

	plano, errPareamento := parearTransferencias(t.Context(), candidatas, pool, ativas, pontuacoes)
	require.NoError(t, errPareamento)
	assert.Len(t, plano.pares, total, "todos os pares são convertidos, além do teto da lista")
	assert.EqualValues(t, total, plano.unpaired)
	assert.Len(t, plano.items, MaxTransferDetectListed)
	assert.Len(t, plano.unpairedItems, MaxTransferDetectListed)
}

func TestParearTransferenciasComEntradasVaziasDevolveListasVaziasNaoNulas(t *testing.T) {
	t.Parallel()

	plano, errPareamento := parearTransferencias(t.Context(), nil, nil, nil, nil)
	require.NoError(t, errPareamento)
	assert.NotNil(t, plano.items)
	assert.NotNil(t, plano.unpairedItems)
	assert.NotNil(t, plano.pares)
	assert.Empty(t, plano.items)
	assert.Zero(t, plano.unpaired)
}

// ---------------------------------------------------------------------------
// Regressões da revisão de segurança (A1 e A2)
// ---------------------------------------------------------------------------

// invariantesDisjuntas afirma o que a correção do A1 garante por construção:
// `pares` e `unpairedItems` não têm nenhuma linha em comum, ninguém entra em
// dois pares, e as contagens cabem no conjunto de candidatas.
func invariantesDisjuntas(t *testing.T, plano *planoDeTransferencias, candidatas []TransferCandidateRow) {
	t.Helper()

	pareadas := map[string]int{}
	for _, p := range plano.pares {
		assert.NotEqual(t, p.outID, p.inID, "as duas pernas do par nunca são a mesma linha")
		assert.NotEqual(t, p.outAccountID, p.inAccountID, "o par nasce entre contas diferentes")
		pareadas[p.outID]++
		pareadas[p.inID]++
	}
	for id, n := range pareadas {
		assert.Equalf(t, 1, n, "lançamento %s entrou em %d pares", id, n)
	}
	for _, u := range plano.unpairedItems {
		assert.NotContainsf(t, pareadas, u.ID,
			"lançamento %s foi convertido E listado como sem par", u.ID)
	}

	// Cada CANDIDATA termina em exatamente um dos dois destinos, ou em
	// nenhum (não é elegível). Contar as pareadas que pertencem ao conjunto
	// de candidatas cobre os dois formatos de entrada: o pool igual às
	// candidatas (todo espelho também é candidata → 2 por par) e o pool maior
	// que elas, como no serviço real, em que o espelho pode ser de outra
	// competência (1 por par).
	daCandidata := map[string]struct{}{}
	for i := range candidatas {
		daCandidata[candidatas[i].ID] = struct{}{}
	}
	pareadasQueSaoCandidatas := 0
	for id := range pareadas {
		if _, ok := daCandidata[id]; ok {
			pareadasQueSaoCandidatas++
		}
	}
	assert.LessOrEqual(t, pareadasQueSaoCandidatas+int(plano.unpaired), len(candidatas),
		"as candidatas pareadas mais as sem par não podem passar do total de candidatas")
	assert.GreaterOrEqual(t, pareadasQueSaoCandidatas, len(plano.pares),
		"todo par consome pelo menos uma candidata")
}

// Regressão do achado A1 (ALTO) da revisão de segurança, com as duas linhas
// exatas do PoC.
//
// A regra de contraparte é ASSIMÉTRICA: `t1` recusar `t2` não implica `t2`
// recusar `t1`. Enquanto a lista de "sem par" era montada dentro do laço,
// `t1` — percorrida primeiro, sem espelho na conta que a palavra dela nomeia —
// entrava em unpairedItems e, logo depois, era escolhida como espelho por
// `t2`. A prévia dizia "fica como está" para uma linha que a confirmação
// convertia, apagando a categoria dela (category_id = NULL, sem desfazer), e
// paired + unpaired contava a mesma linha duas vezes.
func TestRegressaoA1LinhaPareadaNuncaAparaceComoSemPar(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6", "C": "Inter"}
	candidatas := []TransferCandidateRow{
		// Despesa na Nubank cuja palavra-chave nomeia a C6 (contraparte
		// conhecida B): o espelho teria de estar em B, e não está.
		linha("t1", "A", KindExpense, 100_00, 10, "ted para c6"),
		// Receita na Inter cuja palavra-chave nomeia a Nubank (contraparte
		// conhecida A): o espelho está em A — é a própria t1.
		linha("t2", "C", KindIncome, 100_00, 10, "pix de nubank"),
	}
	pontuacoes := map[string]textmatch.Match{
		"t1": {OwnerID: "B", Keyword: "c6", Score: 100},
		"t2": {OwnerID: "A", Keyword: "nubank", Score: 100},
	}

	plano, err := parearTransferencias(t.Context(), candidatas, candidatas, ativas, pontuacoes)
	require.NoError(t, err)
	invariantesDisjuntas(t, plano, candidatas)

	require.Len(t, plano.pares, 1, "o par legítimo é preservado")
	assert.Equal(t, parDeTransferencia{outID: "t1", outAccountID: "A", inID: "t2", inAccountID: "C"}, plano.pares[0])
	assert.Zero(t, plano.unpaired, "t1 foi convertida: não pode ser contada como sem par")
	assert.Empty(t, plano.unpairedItems,
		"a prévia não pode mandar importar o extrato da outra conta para uma perna que ela mesma converte")
	assert.Len(t, plano.items, 1)
}

// A mesma assimetria, agora com a linha "sem par" de verdade no meio: o que
// não pareia continua sendo contado UMA vez, e o que pareia não é contado.
func TestRegressaoA1ContagensNaoSeSobrepoemComRegraAssimetrica(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6", "C": "Inter"}
	candidatas := []TransferCandidateRow{
		linha("t1", "A", KindExpense, 100_00, 10, "ted para c6"),
		linha("t2", "C", KindIncome, 100_00, 10, "pix de nubank"),
		// Terceira linha, sem nenhum espelho possível (valor só dela).
		linha("t3", "A", KindExpense, 999_00, 10, "ted para c6"),
	}
	pontuacoes := map[string]textmatch.Match{
		"t1": {OwnerID: "B", Keyword: "c6", Score: 100},
		"t2": {OwnerID: "A", Keyword: "nubank", Score: 100},
		"t3": {OwnerID: "B", Keyword: "c6", Score: 100},
	}

	plano, err := parearTransferencias(t.Context(), candidatas, candidatas, ativas, pontuacoes)
	require.NoError(t, err)
	invariantesDisjuntas(t, plano, candidatas)

	require.Len(t, plano.pares, 1)
	assert.EqualValues(t, 1, plano.unpaired)
	require.Len(t, plano.unpairedItems, 1)
	assert.Equal(t, "t3", plano.unpairedItems[0].ID, "só a linha que ninguém escolheu")
	assert.Equal(t, TransferUnpairedNoMirror, plano.unpairedItems[0].Reason)
}

// Regressão do achado A2 (ALTO): o pareamento é interrompido quando o cliente
// vai embora. O WriteTimeout do servidor NÃO cancela a goroutine — só o
// contexto cancela, e sem esta checagem o trabalho continuava até o fim para
// uma resposta que ninguém leria.
func TestRegressaoA2PareamentoParaComContextoCancelado(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6"}
	var candidatas []TransferCandidateRow
	pontuacoes := map[string]textmatch.Match{}
	for i := range 1_000 {
		l := linha(fmt.Sprintf("t-%05d", i), "A", KindExpense, int64(i+1), 10, "ted para c6")
		candidatas = append(candidatas, l)
		pontua(pontuacoes, l.ID, "B", "c6", 100)
	}

	ctx, cancelar := context.WithCancel(t.Context())
	cancelar()

	plano, err := parearTransferencias(ctx, candidatas, candidatas, ativas, pontuacoes)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, plano, "interrompido não devolve plano pela metade")
}

// Regressão do achado A2: o teto DURO de comparações. O índice por dia resolve
// o caso realista; o caso patológico que sobra — tudo no mesmo valor e no
// mesmo dia, sem nada pareando (então o balde nunca encolhe) — para no teto e
// vira 422, como todo outro teto desta feature. Nunca execução parcial.
func TestRegressaoA2PareamentoRecusaAcimaDoTetoDeComparacoes(t *testing.T) {
	t.Parallel()

	// 1.500 × 1.500 = 2,25 milhões de comparações > MaxTransferPairComparisons.
	const lado = 1_500
	require.Greater(t, lado*lado, MaxTransferPairComparisons, "o cenário precisa passar do teto")

	ativas := map[string]string{"A": "Nubank", "B": "C6", "C": "Inter"}
	var candidatas, pool []TransferCandidateRow
	pontuacoes := map[string]textmatch.Match{}
	for i := range lado {
		// Candidata cuja palavra nomeia a Inter (C): o espelho teria de estar
		// em C, e todo o pool está em B — nada pareia, nada sai do balde.
		c := linha(fmt.Sprintf("t-%05d", i), "A", KindExpense, 50_00, 10, "pix para inter")
		m := linha(fmt.Sprintf("m-%05d", i), "B", KindIncome, 50_00, 10, "pix recebido")
		candidatas = append(candidatas, c)
		pool = append(pool, c, m)
		pontua(pontuacoes, c.ID, "C", "inter", 100)
	}

	plano, err := parearTransferencias(t.Context(), candidatas, pool, ativas, pontuacoes)
	require.ErrorIs(t, err, ErrTooManyTransferCandidates)
	assert.True(t, IsValidationError(err), "o teto é 422, não 500")
	assert.Nil(t, plano, "acima do teto não existe resultado parcial")
	assert.NotContains(t, err.Error(), "pix", "a mensagem não leva descrição nem palavra-chave")
}

// O índice por dia é o que faz o caso REALISTA não pagar o preço do
// patológico: com os mesmos 5.000 × 5.000, mas espalhados por dias diferentes,
// cada candidata compara só a vizinhança de ±DedupWindowDays e o pareamento
// termina folgado dentro do teto.
func TestRegressaoA2IndicePorDiaMantemOCasoRealistaBaratoEExato(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6"}
	const total = 5_000
	var candidatas, pool []TransferCandidateRow
	pontuacoes := map[string]textmatch.Match{}
	for i := range total {
		// Um par por dia, espalhado por ~14 anos de calendário.
		dia := civil.AddDays(civil.MustNew(2026, 1, 1), i)
		c := TransferCandidateRow{
			ID: fmt.Sprintf("t-%05d", i), AccountID: "A", Kind: KindExpense, AmountCents: 50_00,
			OccurredOn: dia, Description: "pix enviado", DescriptionNorm: "pix enviado",
		}
		m := TransferCandidateRow{
			ID: fmt.Sprintf("m-%05d", i), AccountID: "B", Kind: KindIncome, AmountCents: 50_00,
			OccurredOn: dia, Description: "pix recebido", DescriptionNorm: "pix recebido",
		}
		candidatas = append(candidatas, c)
		pool = append(pool, c, m)
		pontua(pontuacoes, c.ID, "B", "c6", 100)
	}

	plano, err := parearTransferencias(t.Context(), candidatas, pool, ativas, pontuacoes)
	require.NoError(t, err, "o caso realista não pode esbarrar no teto")
	invariantesDisjuntas(t, plano, candidatas)
	assert.Len(t, plano.pares, total, "cada despesa achou a receita do seu dia")
	assert.Zero(t, plano.unpaired)
}

// ---------------------------------------------------------------------------
// Medição do pior caso (achado A2) — não roda na suíte normal.
//
// go test ./internal/transaction -run XXX -bench BenchmarkPareamentoPiorCaso
// ---------------------------------------------------------------------------

func BenchmarkPareamentoRealista(b *testing.B) {
	// 10.000 candidatas e 20.000 linhas no pool, com valores e dias variados
	// — o mês grande de verdade. Cada candidata compara só a vizinhança.
	ativas := map[string]string{"A": "Nubank", "B": "C6"}
	var candidatas, pool []TransferCandidateRow
	pontuacoes := map[string]textmatch.Match{}
	base := civil.MustNew(2026, 1, 1)
	for i := range 10_000 {
		dia := civil.AddDays(base, i%28)
		cents := int64(1_00 + i%5_000)
		t := TransferCandidateRow{
			ID: fmt.Sprintf("t-%05d", i), AccountID: "A", Kind: KindExpense, AmountCents: cents,
			OccurredOn: dia, Description: "pix enviado", DescriptionNorm: "pix enviado",
		}
		m := TransferCandidateRow{
			ID: fmt.Sprintf("m-%05d", i), AccountID: "B", Kind: KindIncome, AmountCents: cents,
			OccurredOn: dia, Description: "pix recebido", DescriptionNorm: "pix recebido",
		}
		candidatas = append(candidatas, t)
		pool = append(pool, t, m)
		pontua(pontuacoes, t.ID, "B", "c6", 100)
	}

	b.ResetTimer()
	for range b.N {
		if _, err := parearTransferencias(context.Background(), candidatas, pool, ativas, pontuacoes); err != nil {
			b.Fatalf("o mês realista não pode esbarrar em teto: %v", err)
		}
	}
}

func BenchmarkPareamentoPiorCaso(b *testing.B) {
	cenarios := []struct {
		nome   string
		pareia bool
	}{
		{"nada pareia (balde nunca encolhe)", false},
		{"tudo pareia", true},
	}

	for _, c := range cenarios {
		b.Run(c.nome, func(b *testing.B) {
			ativas := map[string]string{"A": "Nubank", "B": "C6", "C": "Inter"}
			// Os tetos que o código permite: 10.000 candidatas e 20.000
			// linhas no pool, tudo no MESMO valor e no MESMO dia.
			var candidatas, pool []TransferCandidateRow
			pontuacoes := map[string]textmatch.Match{}
			dona := "C" // nada pareia: o espelho teria de estar na Inter
			if c.pareia {
				dona = "B"
			}
			for i := range 10_000 {
				t := linha(fmt.Sprintf("t-%05d", i), "A", KindExpense, 50_00, 10, "pix")
				m := linha(fmt.Sprintf("m-%05d", i), "B", KindIncome, 50_00, 10, "pix")
				candidatas = append(candidatas, t)
				pool = append(pool, t, m)
				pontua(pontuacoes, t.ID, dona, "pix", 100)
			}

			b.ResetTimer()
			for range b.N {
				_, _ = parearTransferencias(context.Background(), candidatas, pool, ativas, pontuacoes)
			}
		})
	}
}

// Regressão do achado A5: as linhas são travadas numa ordem GLOBAL — o menor
// id do par, depois o maior —, e não na ordem de leitura das candidatas do
// mês. Duas execuções simultâneas de meses diferentes cujas janelas se cruzam
// alcançariam as mesmas linhas em ordens opostas, e ordens opostas são o que
// produz deadlock no PostgreSQL (que sairia como 500, não como 409).
func TestRegressaoA5ConversaoTravaNaMesmaOrdemIndependenteDaLeitura(t *testing.T) {
	t.Parallel()

	pares := []parDeTransferencia{
		{outID: "t-90", outAccountID: "A", inID: "t-10", inAccountID: "B"},
		{outID: "t-50", outAccountID: "A", inID: "t-60", inAccountID: "B"},
		{outID: "t-20", outAccountID: "A", inID: "t-80", inAccountID: "B"},
		{outID: "t-20b", outAccountID: "A", inID: "t-20a", inAccountID: "B"},
	}

	esperada := []parDeTransferencia{
		{outID: "t-90", outAccountID: "A", inID: "t-10", inAccountID: "B"},   // min t-10
		{outID: "t-20", outAccountID: "A", inID: "t-80", inAccountID: "B"},   // min t-20
		{outID: "t-20b", outAccountID: "A", inID: "t-20a", inAccountID: "B"}, // min t-20a
		{outID: "t-50", outAccountID: "A", inID: "t-60", inAccountID: "B"},   // min t-50
	}
	assert.Equal(t, esperada, ordemDeTravamento(pares))

	// Qualquer ordem de leitura leva à MESMA ordem de travamento — é isso que
	// impede duas execuções de se enroscarem.
	embaralhados := []parDeTransferencia{pares[2], pares[3], pares[0], pares[1]}
	assert.Equal(t, esperada, ordemDeTravamento(embaralhados))

	// E a fatia de entrada não é reordenada: a prévia e os itens continuam na
	// ordem de leitura que a tela mostra.
	assert.Equal(t, "t-90", pares[0].outID)
	assert.Equal(t, "t-50", pares[1].outID)
}
