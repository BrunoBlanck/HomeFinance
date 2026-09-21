package transaction

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Bateria ADVERSARIAL do pareamento puro (QA, spec 0005 §13.5.5 e ADR-028c).
//
// O alvo é a REIVINDICAÇÃO DUPLA: o defeito que o dev corrigiu era uma linha
// consumida como espelho voltar a ser percorrida como candidata (e vice-versa).
// Aqui a correção é atacada de vários ângulos — 3×2, 2×3, valores repetidos em
// dias vizinhos dentro da janela, cadeias A→B→C e uma varredura aleatória —
// e o que se afirma é sempre o mesmo INVARIANTE, não um resultado decorado:
// nenhum lançamento aparece em dois pares, nem em um par e na lista de "sem
// par" ao mesmo tempo.

// invariantesDoPlano afirma o que tem de valer para QUALQUER entrada. É a
// forma de o teste continuar valendo quando o desempate mudar: o que se prova
// é a consistência do plano, não a escolha.
func invariantesDoPlano(t *testing.T, plano *planoDeTransferencias) {
	t.Helper()

	vistos := map[string]int{}
	for _, p := range plano.pares {
		assert.NotEqual(t, p.outID, p.inID, "as duas pernas do par nunca são a mesma linha")
		vistos[p.outID]++
		vistos[p.inID]++
	}
	for id, n := range vistos {
		assert.Equalf(t, 1, n, "lançamento %s entrou em %d pares — reivindicação dupla", id, n)
	}

	for _, u := range plano.unpairedItems {
		assert.NotContainsf(t, vistos, u.ID,
			"lançamento %s foi convertido E listado como sem par", u.ID)
		assert.Equal(t, TransferUnpairedNoMirror, u.Reason)
	}

	// Item da prévia e par convertido descrevem o mesmo conjunto, até o teto.
	assert.Len(t, plano.items, min(len(plano.pares), MaxTransferDetectListed))
	assert.Len(t, plano.unpairedItems, min(int(plano.unpaired), MaxTransferDetectListed))
	for i, item := range plano.items {
		assert.Equal(t, plano.pares[i].outID, item.OutTransactionID)
		assert.Equal(t, plano.pares[i].inID, item.InTransactionID)
	}
}

// proprias pontua cada linha com a palavra da PRÓPRIA conta dela — a semântica
// nova (marcador "isto é transferência entre as minhas contas").
func proprias(linhas ...TransferCandidateRow) map[string]textmatch.Match {
	m := map[string]textmatch.Match{}
	for _, l := range linhas {
		m[l.ID] = textmatch.Match{OwnerID: l.AccountID, Keyword: "pix", Score: 100}
	}
	return m
}

// TestQAPareamentoNuncaReivindicaALinhaDuasVezes ataca o critério 5 com as
// variações que o enunciado pediu.
func TestQAPareamentoNuncaReivindicaALinhaDuasVezes(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6", "C": "Inter"}

	casos := []struct {
		nome    string
		linhas  []TransferCandidateRow
		pares   int
		semPar  int
		confere func(t *testing.T, plano *planoDeTransferencias)
	}{
		{
			nome: "3 despesas em A + 2 receitas em B, mesmo dia → 2 pares, 1 sem par",
			linhas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 50_00, 5, "pix enviado"),
				linha("t2", "A", KindExpense, 50_00, 5, "pix enviado"),
				linha("t3", "A", KindExpense, 50_00, 5, "pix enviado"),
				linha("u1", "B", KindIncome, 50_00, 5, "pix recebido"),
				linha("u2", "B", KindIncome, 50_00, 5, "pix recebido"),
			},
			pares: 2, semPar: 1,
		},
		{
			nome: "2 despesas em A + 3 receitas em B (o espelho é que sobra) → 2 pares, 1 sem par",
			linhas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 50_00, 5, "pix enviado"),
				linha("t2", "A", KindExpense, 50_00, 5, "pix enviado"),
				linha("u1", "B", KindIncome, 50_00, 5, "pix recebido"),
				linha("u2", "B", KindIncome, 50_00, 5, "pix recebido"),
				linha("u3", "B", KindIncome, 50_00, 5, "pix recebido"),
			},
			pares: 2, semPar: 1,
		},
		{
			nome: "5 despesas + 5 receitas, todas iguais no mesmo dia → 5 pares, nenhuma sem par",
			linhas: func() []TransferCandidateRow {
				var l []TransferCandidateRow
				for i := range 5 {
					l = append(l,
						linha(fmt.Sprintf("t%d", i), "A", KindExpense, 50_00, 5, "pix enviado"),
						linha(fmt.Sprintf("u%d", i), "B", KindIncome, 50_00, 5, "pix recebido"),
					)
				}
				return l
			}(),
			pares: 5, semPar: 0,
		},
		{
			nome: "valores repetidos em dias VIZINHOS dentro da janela de ±3",
			linhas: []TransferCandidateRow{
				linha("t1", "A", KindExpense, 50_00, 5, "pix enviado"),
				linha("t2", "A", KindExpense, 50_00, 6, "pix enviado"),
				linha("t3", "A", KindExpense, 50_00, 7, "pix enviado"),
				linha("u1", "B", KindIncome, 50_00, 5, "pix recebido"),
				linha("u2", "B", KindIncome, 50_00, 6, "pix recebido"),
				linha("u3", "B", KindIncome, 50_00, 8, "pix recebido"),
			},
			pares: 3, semPar: 0,
			confere: func(t *testing.T, plano *planoDeTransferencias) {
				// Cada despesa fica com a receita do MESMO dia quando existe;
				// a distância zero vence a de 1 e 2 dias.
				assert.Equal(t, []parDeTransferencia{
					{outID: "t1", outAccountID: "A", inID: "u1", inAccountID: "B"},
					{outID: "t2", outAccountID: "A", inID: "u2", inAccountID: "B"},
					{outID: "t3", outAccountID: "A", inID: "u3", inAccountID: "B"},
				}, plano.pares)
			},
		},
		{
			nome: "cadeia A→B→C: a linha do meio em B não pode servir aos dois lados",
			linhas: []TransferCandidateRow{
				linha("a-out", "A", KindExpense, 200_00, 10, "pix enviado"),
				linha("b-in", "B", KindIncome, 200_00, 10, "pix recebido"),
				linha("b-out", "B", KindExpense, 200_00, 10, "pix enviado"),
				linha("c-in", "C", KindIncome, 200_00, 10, "pix recebido"),
			},
			pares: 2, semPar: 0,
			confere: func(t *testing.T, plano *planoDeTransferencias) {
				assert.Equal(t, []parDeTransferencia{
					{outID: "a-out", outAccountID: "A", inID: "b-in", inAccountID: "B"},
					{outID: "b-out", outAccountID: "B", inID: "c-in", inAccountID: "C"},
				}, plano.pares)
			},
		},
		{
			nome: "cadeia A→B→C com a perna do meio ÍMPAR: sobra exatamente uma sem par",
			linhas: []TransferCandidateRow{
				linha("a-out", "A", KindExpense, 200_00, 10, "pix enviado"),
				linha("b-in", "B", KindIncome, 200_00, 10, "pix recebido"),
				linha("b-out", "B", KindExpense, 200_00, 10, "pix enviado"),
			},
			pares: 1, semPar: 1,
		},
		{
			nome: "a MESMA linha é candidata e espelho em potencial dos dois lados (A↔B simétrico)",
			linhas: []TransferCandidateRow{
				linha("x1", "A", KindExpense, 70_00, 9, "pix enviado"),
				linha("x2", "B", KindIncome, 70_00, 9, "pix recebido"),
				linha("x3", "B", KindExpense, 70_00, 9, "pix enviado"),
				linha("x4", "A", KindIncome, 70_00, 9, "pix recebido"),
			},
			pares: 2, semPar: 0,
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			pontuacoes := proprias(c.linhas...)
			plano, errPareamento := parearTransferencias(t.Context(), c.linhas, c.linhas, ativas, pontuacoes)
			require.NoError(t, errPareamento)

			invariantesDoPlano(t, plano)
			assert.Len(t, plano.pares, c.pares, "pares: %+v", plano.pares)
			assert.EqualValues(t, c.semPar, plano.unpaired)
			if c.confere != nil {
				c.confere(t, plano)
			}
			// Determinístico e sem efeito colateral nas entradas: rodar de
			// novo com as MESMAS fatias dá exatamente o mesmo plano.
			denovo, errDenovo := parearTransferencias(t.Context(), c.linhas, c.linhas, ativas, pontuacoes)
			require.NoError(t, errDenovo)
			assert.Equal(t, plano, denovo)
		})
	}
}

// TestQAPareamentoAleatorioMantemOsInvariantes varre entradas geradas — muitos
// valores repetidos, dias vizinhos e três contas — procurando a combinação que
// quebre a reivindicação. Semente fixa: um defeito encontrado aqui é
// reproduzível.
func TestQAPareamentoAleatorioMantemOsInvariantes(t *testing.T) {
	t.Parallel()

	// "Z" NÃO está em `ativas`: é a conta arquivada, que pode ser sorteada
	// como dona da linha e como dona da pontuação, e nunca pode virar par.
	ativas := map[string]string{"A": "Nubank", "B": "C6", "C": "Inter"}
	contas := []string{"A", "B", "C", "Z"}
	kinds := []string{KindExpense, KindIncome}

	for semente := int64(1); semente <= 200; semente++ {
		t.Run(fmt.Sprintf("semente-%02d", semente), func(t *testing.T) {
			t.Parallel()
			r := rand.New(rand.NewSource(semente))

			n := 4 + r.Intn(56)
			linhas := make([]TransferCandidateRow, 0, n)
			pontuacoes := map[string]textmatch.Match{}
			for i := range n {
				conta := contas[r.Intn(len(contas))]
				l := linha(
					fmt.Sprintf("id-%03d", i),
					conta,
					kinds[r.Intn(len(kinds))],
					// Poucos valores distintos, de propósito: é o que cria os
					// baldes grandes onde a reivindicação dupla apareceria.
					int64(50_00+r.Intn(4)*10_00),
					1+r.Intn(20),
					"pix",
				)
				linhas = append(linhas, l)
				switch r.Intn(4) {
				case 0: // não bate com palavra nenhuma
				case 1: // palavra da PRÓPRIA conta
					pontuacoes[l.ID] = textmatch.Match{OwnerID: conta, Keyword: "pix", Score: 100}
				default: // palavra de OUTRA conta, sorteada
					pontuacoes[l.ID] = textmatch.Match{
						OwnerID: contas[r.Intn(len(contas))], Keyword: "pix", Score: 100,
					}
				}
			}

			plano, errPareamento := parearTransferencias(t.Context(), linhas, linhas, ativas, pontuacoes)
			require.NoError(t, errPareamento)
			invariantesDoPlano(t, plano)

			// Toda linha pareada tinha de ser elegível: contas distintas e
			// ativas, sentidos opostos, mesmo valor, dentro da janela.
			porID := map[string]TransferCandidateRow{}
			for _, l := range linhas {
				porID[l.ID] = l
			}
			for _, p := range plano.pares {
				out, in := porID[p.outID], porID[p.inID]
				assert.Equal(t, KindExpense, out.Kind)
				assert.Equal(t, KindIncome, in.Kind)
				assert.NotEqual(t, out.AccountID, in.AccountID, "par entre contas distintas")
				assert.Contains(t, ativas, out.AccountID)
				assert.Contains(t, ativas, in.AccountID)
				assert.Equal(t, out.AmountCents, in.AmountCents)
				assert.LessOrEqual(t, civil.DaysBetween(out.OccurredOn, in.OccurredOn), DedupWindowDays)
			}
		})
	}
}

// TestQAPareamentoFronteiraExataDaJanela fecha a borda de ±3 e ±4 dias nos
// DOIS sentidos — o espelho antes e o espelho depois da candidata.
func TestQAPareamentoFronteiraExataDaJanela(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6"}
	require.Equal(t, 3, DedupWindowDays, "a fronteira testada aqui é a de DedupWindowDays")

	casos := []struct {
		nome    string
		diaDoM  int
		esperaP bool
	}{
		{"espelho 3 dias ANTES: dentro", 7, true},
		{"espelho 4 dias ANTES: fora", 6, false},
		{"espelho no mesmo dia: dentro", 10, true},
		{"espelho 3 dias DEPOIS: dentro", 13, true},
		{"espelho 4 dias DEPOIS: fora", 14, false},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			tt := linha("t", "A", KindExpense, 100_00, 10, "ted para c6")
			m := linha("m", "B", KindIncome, 100_00, c.diaDoM, "x")
			pontuacoes := pontua(map[string]textmatch.Match{}, "t", "B", "c6", 100)

			plano, errPareamento := parearTransferencias(
				t.Context(),
				[]TransferCandidateRow{tt},
				[]TransferCandidateRow{tt, m},
				ativas, pontuacoes,
			)
			require.NoError(t, errPareamento)
			invariantesDoPlano(t, plano)
			if c.esperaP {
				require.Len(t, plano.pares, 1)
				assert.Zero(t, plano.unpaired)
			} else {
				assert.Empty(t, plano.pares)
				assert.EqualValues(t, 1, plano.unpaired)
			}
		})
	}
}

// TestQAPareamentoIgnoraLinhaQueJaEPernaDeTransferencia: uma linha com
// transfer_group_id preenchido nunca chega aqui (as duas consultas filtram
// `transfer_group_id IS NULL`), mas se chegasse — kind transfer_in/out — não
// pode virar espelho nem candidata.
func TestQAPareamentoIgnoraLinhaQueJaEPernaDeTransferencia(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6"}
	tt := linha("t", "A", KindExpense, 100_00, 10, "ted para c6")
	perna := linha("perna", "B", KindTransferIn, 100_00, 10, "ted")
	pontuacoes := pontua(pontua(map[string]textmatch.Match{},
		"t", "B", "c6", 100), "perna", "B", "c6", 100)

	plano, errPareamento := parearTransferencias(
		t.Context(),
		[]TransferCandidateRow{tt, perna},
		[]TransferCandidateRow{tt, perna},
		ativas, pontuacoes,
	)
	require.NoError(t, errPareamento)
	invariantesDoPlano(t, plano)
	assert.Empty(t, plano.pares, "perna de transferência não é espelho")
	assert.EqualValues(t, 1, plano.unpaired, "só a candidata viva conta")
	require.Len(t, plano.unpairedItems, 1)
	assert.Equal(t, "t", plano.unpairedItems[0].ID)
}

// TestQAPareamentoContaArquivadaNaoEntraNemComoTNemComoM cobre os dois papéis
// separadamente — o teste existente cobre os dois juntos.
func TestQAPareamentoContaArquivadaNaoEntraNemComoTNemComoM(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6"}
	const arquivada = "Z"

	t.Run("arquivada como T: não é candidata e não é contada como sem par", func(t *testing.T) {
		t.Parallel()
		tt := linha("t", arquivada, KindExpense, 100_00, 10, "pix enviado")
		m := linha("m", "B", KindIncome, 100_00, 10, "pix recebido")
		pontuacoes := proprias(tt, m)

		plano, errPareamento := parearTransferencias(t.Context(), []TransferCandidateRow{tt}, []TransferCandidateRow{tt, m}, ativas, pontuacoes)
		require.NoError(t, errPareamento)
		invariantesDoPlano(t, plano)
		assert.Empty(t, plano.pares)
		assert.Zero(t, plano.unpaired)
	})

	t.Run("arquivada como M: a candidata viva fica sem par", func(t *testing.T) {
		t.Parallel()
		tt := linha("t", "A", KindExpense, 100_00, 10, "pix enviado")
		m := linha("m", arquivada, KindIncome, 100_00, 10, "pix recebido")
		pontuacoes := proprias(tt, m)

		plano, errPareamento := parearTransferencias(t.Context(), []TransferCandidateRow{tt}, []TransferCandidateRow{tt, m}, ativas, pontuacoes)
		require.NoError(t, errPareamento)
		invariantesDoPlano(t, plano)
		assert.Empty(t, plano.pares)
		assert.EqualValues(t, 1, plano.unpaired)
	})
}

// TestQADefeitoLinhaPareadaTambemContadaComoSemPar é a REPRODUÇÃO MÍNIMA de um
// defeito encontrado pelo QA em 17/09/2026 — ele FALHA hoje, de propósito, e
// só passa quando o pareamento for corrigido.
//
// O que acontece: a candidata sem espelho é contada e listada DENTRO do laço,
// mas NÃO é reivindicada (detecttransfers.go, ramo `melhor == nil`). Uma
// candidata processada depois pode então escolhê-la como espelho — e a linha
// termina convertida E listada como "sem par", com o motivo no_mirror.
//
// Repro: `t1` é despesa em A que bate com palavra da conta B (contraparte
// conhecida: o espelho tem de estar em B). `t2` é receita em C que bate com
// palavra da conta A (contraparte conhecida: o espelho tem de estar em A).
// Mesmo valor, mesmo dia. `t1` é percorrida primeiro (id menor), não acha
// espelho em B e vai para unpairedItems; `t2`, logo depois, acha `t1` em A e
// pareia com ela.
//
// Resultado obtido: pares=[{t1,t2}], unpaired=1, unpairedItems=[t1].
// Resultado esperado: pares=[{t1,t2}], unpaired=0, unpairedItems=[].
//
// Efeito visível: a prévia mostra o par E diz "importe o extrato da outra
// conta" para uma das pernas dele; na execução real, `paired: 1` e
// `unpaired: 1` para duas únicas candidatas, das quais nenhuma ficou de fora.
// A conversão em si continua correta (cada linha entra em um só par), então é
// defeito de CONTAGEM e de prévia, não de gravação.
//
// Correção sugerida (desenho — cabe ao dev-backend-go): não contar "sem par"
// dentro do laço; depois de montar todos os pares, varrer as candidatas
// elegíveis que não estão em `reivindicadas` e só então montar unpaired e
// unpairedItems.
func TestQADefeitoLinhaPareadaTambemContadaComoSemPar(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6", "C": "Inter"}
	linhas := []TransferCandidateRow{
		linha("t1", "A", KindExpense, 100_00, 10, "ted para c6"),
		linha("t2", "C", KindIncome, 100_00, 10, "pix de nubank"),
	}
	pontuacoes := map[string]textmatch.Match{
		"t1": {OwnerID: "B", Keyword: "c6", Score: 100},
		"t2": {OwnerID: "A", Keyword: "nubank", Score: 100},
	}

	plano, errPareamento := parearTransferencias(t.Context(), linhas, linhas, ativas, pontuacoes)
	require.NoError(t, errPareamento)

	require.Equal(t, []parDeTransferencia{{outID: "t1", outAccountID: "A", inID: "t2", inAccountID: "C"}}, plano.pares)
	assert.Zero(t, plano.unpaired,
		"t1 foi convertida: não pode ser contada como sem par")
	assert.Empty(t, plano.unpairedItems,
		"a prévia não pode pedir 'importe o extrato da outra conta' para uma perna que ela mesma vai converter")
}

// --- superfície nova: orçamento de comparações (achado A2 da revisão) ------

// O orçamento é o teto DURO de comparações candidata × espelho. O que importa
// provar é que ele NUNCA deixa passar mais do que o teto e que, uma vez
// esgotado, quem depende dele para de trabalhar — em vez de devolver um plano
// parcial, que seria pior do que o 422.
func TestQAOrcamentoDeComparacoesNuncaPassaDoTeto(t *testing.T) {
	t.Parallel()

	o := &orcamentoDeComparacoes{restante: 3}
	for i := range 3 {
		assert.Truef(t, o.gastar(), "a comparação %d está dentro do teto", i+1)
	}
	assert.False(t, o.gastar(), "a 4ª comparação passa do teto de 3")
	assert.False(t, o.gastar(), "e continua recusando")

	// Esgotado JÁ no teto exato: quem gastou as 3 é recusado junto de quem
	// tentaria a 4ª. É deliberadamente conservador — um teto de DoS erra para
	// o lado de recusar, e 422 é a resposta certa nos dois casos.
	assert.True(t, o.esgotado())

	assert.False(t, (&orcamentoDeComparacoes{restante: 0}).gastar(), "teto zero não gasta nada")
	assert.True(t, (&orcamentoDeComparacoes{restante: 0}).esgotado())
	assert.False(t, (&orcamentoDeComparacoes{restante: -1}).gastar(), "negativo nunca vira crédito")
}

// Com o orçamento já esgotado, procurarEspelho para na primeira comparação e
// devolve nil — nenhum par sai de um orçamento estourado.
func TestQAProcurarEspelhoParaQuandoOOrcamentoAcaba(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6"}
	tt := linha("t", "A", KindExpense, 100_00, 10, "pix enviado")
	m := linha("m", "B", KindIncome, 100_00, 10, "pix recebido")
	pontuacoes := proprias(tt, m)

	// Índice com o espelho perfeito disponível.
	indice := make(indiceDeEspelhos, 1)
	chave := chaveDeEspelho{kind: KindIncome, cents: 100_00}
	indice[chave] = map[civil.Date][]*espelho{m.OccurredOn: {{linha: &m}}}

	comCota := &orcamentoDeComparacoes{restante: 100}
	achou := procurarEspelho(&tt, pontuacoes[tt.ID], indice, ativas, pontuacoes,
		map[string]struct{}{}, comCota)
	require.NotNil(t, achou, "com cota, o espelho perfeito é encontrado")
	assert.Equal(t, "m", achou.linha.ID)

	semCota := &orcamentoDeComparacoes{restante: 0}
	assert.Nil(t, procurarEspelho(&tt, pontuacoes[tt.ID], indice, ativas, pontuacoes,
		map[string]struct{}{}, semCota), "orçamento esgotado não pode produzir par")
}

// O pareamento inteiro respeita o cancelamento do contexto: cliente que
// desistiu não deixa o servidor varrendo um mês inteiro.
func TestQAPareamentoRespeitaOCancelamentoDoContexto(t *testing.T) {
	t.Parallel()

	ativas := map[string]string{"A": "Nubank", "B": "C6"}
	var linhas []TransferCandidateRow
	for i := range 600 {
		linhas = append(linhas,
			linha(fmt.Sprintf("t-%04d", i), "A", KindExpense, int64(i+1), 10, "pix enviado"),
			linha(fmt.Sprintf("u-%04d", i), "B", KindIncome, int64(i+1), 10, "pix recebido"),
		)
	}
	pontuacoes := proprias(linhas...)

	ctx, cancelar := context.WithCancel(t.Context())
	cancelar()
	_, err := parearTransferencias(ctx, linhas, linhas, ativas, pontuacoes)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
