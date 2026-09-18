package textmatch

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Critério 11 da spec 0005: 10.000 linhas (2.500 descrições distintas —
// extrato repete descrição) contra 1.000 palavras-chave da casa em ≤ 2 s.
// Mede o que o importador paga: construir o Matcher e pontuar cada linha
// com Best (exclusão de uma conta inclusa, como na detecção de
// transferência).
//
// Lento de propósito; `go test -short` pula. Sob o detector de corrida a
// instrumentação multiplica o tempo por 5–10×, então o limite é relaxado
// (raceEnabled, ver race_*_test.go) — o critério é sobre o binário real, e
// o tempo medido é sempre registrado no log para o reporte.
func TestDesempenhoCriterio11(t *testing.T) {
	if testing.Short() {
		t.Skip("teste lento (critério 11): pulado com -short")
	}

	const (
		nKeywords = 1_000
		nOwners   = 200
		nDistinct = 2_500
		nTotal    = 10_000
	)
	limite := 2 * time.Second
	if raceEnabled {
		limite = 20 * time.Second
	}

	rng := rand.New(rand.NewPCG(11, 2026))
	kws, distintas := materialDeCarga(t, rng, nKeywords, nOwners, nDistinct)
	linhas := make([]string, nTotal)
	for i := range linhas {
		linhas[i] = distintas[rng.IntN(len(distintas))]
	}

	inicio := time.Now()
	m, err := NewMatcher(kws)
	require.NoError(t, err)
	construcao := time.Since(inicio)

	var casou, abaixo, ambiguas int
	for _, d := range linhas {
		switch bestOK(m.Best(d, "owner-000")).Reason {
		case ReasonMatched:
			casou++
		case ReasonBelowThreshold:
			abaixo++
		case ReasonAmbiguous:
			ambiguas++
		}
	}
	total := time.Since(inicio)

	t.Logf("critério 11: %d linhas (%d distintas) × %d palavras-chave: total %s (construção %s) · matched=%d below_threshold=%d ambiguous=%d · race=%v",
		nTotal, nDistinct, nKeywords, total, construcao, casou, abaixo, ambiguas, raceEnabled)
	require.Greater(t, casou, nTotal/10, "o cenário precisa exercitar as regras de verdade")

	// O gate de RELÓGIO fica porque este é o critério 11 da spec, escrito em
	// segundos — mas ele é o teto FROUXO (8× a medida isolada, 10× sob -race).
	// Quem pega regressão de desempenho de verdade é a contagem de trabalho de
	// TestOrcamentoCobreOTetoLegitimo, que é determinística.
	require.LessOrEqualf(t, total, limite, "critério 11: %s > %s", total, limite)
}

// ---------------------------------------------------------------------------
// Orçamento de trabalho (achado A1 da revisão de segurança)
// ---------------------------------------------------------------------------

// orcamentoDeMedicao é um Budget grande o bastante para NÃO limitar nada: os
// testes de medição precisam ver o custo inteiro do cenário para poder
// compará-lo com MaxMatchWork. Um quarto de MaxInt64 é folga absurda e ainda
// deixa espaço para o contador passar do teto sem transbordar.
func orcamentoDeMedicao() *Budget { return NewBudget(math.MaxInt64 / 4) }

// TETO LEGÍTIMO DO PRODUTO, medido: é ele que justifica o número de
// MaxMatchWork, e é o gate DETERMINÍSTICO de regressão de desempenho do
// casamento por palavra-chave (a contagem de células não depende da máquina,
// da carga nem do detector de corrida — o tempo, sim, e por isso ele só vai
// para o log).
//
// O maior conjunto de palavras-chave que uma casa consegue cadastrar é
// category.MaxPerHousehold (200) × MaxKeywordsPerOwner (20) = 4.000 palavras de
// categoria, mais account.MaxPerHousehold (50) × 20 = 1.000 de conta. Os três
// matchers de uma operação COMPARTILHAM o orçamento (internal/classify), então
// o teto real de uma execução é 5.000 palavras. O maior número de descrições
// DISTINTAS de uma execução é MaxAutoCategorizeRows (10.000) — extrato repete
// descrição, e a memo cobra a repetida uma vez só.
//
// O teste roda SÓ o extremo (5.000), porque ele domina: a mesma medição com as
// 4.000 palavras do cenário do revisor deu 708.713.779 células em 3,2 s
// (31,0 s sob -race) em 18/09/2026, contra 883.054.177 células em 4,1 s
// (39,0 s sob -race) aqui. Rodar os dois só dobrava o tempo do gate.
//
// A folga exigida é de ao menos 1,5× sobre MaxMatchWork. É 1,5× e não mais
// porque o cenário JÁ É o extremo do produto — não existe casa maior que esta.
func TestOrcamentoCobreOTetoLegitimo(t *testing.T) {
	if testing.Short() {
		t.Skip("teste lento (teto legítimo): pulado com -short")
	}

	const (
		nKeywords = 5_000
		nOwners   = 250
		nDistinct = 10_000
	)

	rng := rand.New(rand.NewPCG(uint64(nKeywords), 2026))
	kws, distintas := materialDeCarga(t, rng, nKeywords, nOwners, nDistinct)

	orcamento := orcamentoDeMedicao()
	inicio := time.Now()
	m, err := NewMatcher(kws, WithBudget(orcamento))
	require.NoError(t, err)
	construcao := time.Since(inicio)

	var casou int
	for _, d := range distintas {
		r, err := m.Best(d, "owner-000")
		require.NoError(t, err, "o teto legítimo não pode estourar nem com orçamento de medição")
		if r.Matched() {
			casou++
		}
	}
	total := time.Since(inicio)
	gasto := orcamento.Spent()

	t.Logf("teto legítimo: %d descrições distintas × %d palavras-chave: total %s (construção %s) · matched=%d · trabalho=%d células (%.1f%% de MaxMatchWork) · race=%v",
		nDistinct, nKeywords, total, construcao, casou, gasto,
		100*float64(gasto)/float64(MaxMatchWork), raceEnabled)

	require.Greater(t, casou, nDistinct/10, "o cenário precisa exercitar as regras de verdade")
	require.Lessf(t, 3*gasto, int64(2*MaxMatchWork),
		"o teto legítimo gasta %d células e MaxMatchWork é %d: menos de 1,5× de folga é apertado demais sobre o maior conjunto que uma casa consegue cadastrar",
		gasto, MaxMatchWork)
}

// CASO ADVERSARIAL: o que o orçamento existe para cortar.
//
// A receita é a do achado A1: palavras-chave do tamanho máximo (40 runas) com
// um PREFIXO COMUM longo — assim toda palavra compartilha trigrama com todo
// token de toda descrição, e o prefiltro por trigrama não separa nada — contra
// descrições distintas do tamanho máximo (140 runas), quebradas em tokens
// grandes o bastante para a poda por comprimento não descartá-los. Sem
// orçamento, o revisor mediu 7 min 20 s de CPU por requisição.
//
// O teste é DETERMINÍSTICO e RÁPIDO, e essa é uma escolha, não um atalho:
//
//  1. mede o custo REAL de UMA linha adversarial (uma chamada, ~30 ms) e faz a
//     divisão — com MaxMatchWork, quantas linhas o ataque consegue antes do
//     422? A resposta é aritmética, não cronometrada;
//  2. exercita o caminho de recusa DE VERDADE com um orçamento do tamanho de
//     duas linhas, provando que o erro sai e que ele para o laço.
//
// A alternativa — queimar os 1,5 × 10⁹ do orçamento de produção até o estouro —
// levava 2 s normalmente e 42 s sob -race, para provar a mesma coisa.
func TestOrcamentoCortaOCasoAdversarial(t *testing.T) {
	t.Parallel()

	const (
		nKeywords = 4_000
		nOwners   = 200
		nLinhas   = 10_000 // MaxAutoCategorizeRows: o tamanho do ataque
	)

	// Prefixo comum de 33 runas + 7 runas próprias = 40 (MaxKeywordRunes).
	prefixo := strings.Repeat("ab", 16) + "c"
	require.Len(t, []rune(prefixo), 33)
	kws := make([]Keyword, nKeywords)
	for i := range kws {
		p := prefixo + fmt.Sprintf("%07d", i)
		require.Len(t, []rune(p), MaxKeywordRunes)
		kws[i] = Keyword{OwnerID: fmt.Sprintf("owner-%03d", i%nOwners), Keyword: p, Norm: p}
	}

	// Descrições de 140 runas (MaxDescriptionLen), em três tokens de 46 runas:
	// 46 contra 40 tem teto 96 — a poda por comprimento NÃO ajuda aqui, e é
	// exatamente por isso que o orçamento precisa existir.
	descricao := func(i int) string {
		var b strings.Builder
		for j := range 3 {
			if j > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(prefixo + fmt.Sprintf("%013d", i*3+j))
		}
		return b.String()
	}
	require.Len(t, []rune(descricao(0)), 140)
	require.GreaterOrEqual(t, MaxScoreForLengths(46, 40), MinScore,
		"o cenário precisa de pares que a poda por comprimento NÃO descarta")

	// (1) Custo de UMA linha adversarial, medido.
	medicao := orcamentoDeMedicao()
	m, err := NewMatcher(kws, WithBudget(medicao))
	require.NoError(t, err)

	inicio := time.Now()
	_, err = m.Best(descricao(0))
	umaLinha := time.Since(inicio)
	require.NoError(t, err, "o orçamento de medição não limita: a linha tem de passar")

	custoDaLinha := medicao.Spent()
	require.Positive(t, custoDaLinha)
	linhasQuePassam := int64(MaxMatchWork) / custoDaLinha

	t.Logf("adversarial: %d palavras de %d runas (prefixo comum de %d) × descrições distintas de 140 runas: %d células por linha (%s) · com MaxMatchWork o ataque recusa depois de ~%d de %d linhas · race=%v",
		nKeywords, MaxKeywordRunes, len([]rune(prefixo)), custoDaLinha, umaLinha, linhasQuePassam, nLinhas, raceEnabled)

	require.LessOrEqualf(t, linhasQuePassam, int64(nLinhas/100),
		"o ataque consegue ~%d das %d linhas antes do 422 — o teto está alto demais", linhasQuePassam, nLinhas)

	// (2) O caminho de recusa, de verdade: orçamento do tamanho de duas linhas.
	apertado, err := NewMatcher(kws, WithBudget(NewBudget(2*custoDaLinha)))
	require.NoError(t, err)

	var passaram int
	var errFinal error
	for i := range 10 {
		if _, errFinal = apertado.Best(descricao(i)); errFinal != nil {
			break
		}
		passaram++
	}
	require.ErrorIs(t, errFinal, ErrWorkBudgetExceeded,
		"o cenário adversarial precisa ESTOURAR o orçamento, não passar")
	require.LessOrEqual(t, passaram, 2, "o orçamento de duas linhas não pode render mais que duas")
}

// O estouro é STICKY para a operação: depois dele, nenhuma consulta volta a
// responder como se nada tivesse acontecido. É o que garante que nenhum
// chamador consiga "tentar de novo" e montar um resultado pela metade.
func TestOrcamentoEstouradoNaoRessuscita(t *testing.T) {
	t.Parallel()

	m, err := NewMatcher([]Keyword{{OwnerID: "A", Keyword: "supermercado"}}, WithBudget(NewBudget(1)))
	require.NoError(t, err)

	_, err = m.Best("supermercado extra")
	require.ErrorIs(t, err, ErrWorkBudgetExceeded)
	require.True(t, m.budget.Exhausted())

	// Outra descrição, depois do estouro: continua recusando.
	_, err = m.Rank("padaria central")
	require.ErrorIs(t, err, ErrWorkBudgetExceeded)

	// E nada foi memorizado: memo com resultado parcial seria o resultado
	// truncado em silêncio que este achado existe para impedir.
	require.Empty(t, m.memo)
}

// materialDeCarga separa o que cenarioDeCarga gera para o teste cronometrar
// a construção do Matcher também.
func materialDeCarga(tb testing.TB, rng *rand.Rand, nKeywords, nOwners, nDistinct int) ([]Keyword, []string) {
	tb.Helper()
	m, descs := cenarioDeCarga(tb, rng, nKeywords, nOwners, nDistinct)
	kws := make([]Keyword, len(m.entries))
	for i, e := range m.entries {
		kws[i] = Keyword{OwnerID: m.owners[e.owner], Keyword: e.keyword, Norm: e.keyword}
	}
	return kws, descs
}
