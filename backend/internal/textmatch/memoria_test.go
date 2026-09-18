package textmatch

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Teto de MEMÓRIA da memo (achado A3 da revisão de segurança)
// ---------------------------------------------------------------------------

// maxHeapDaOperacao é o teto de heap VIVO que uma operação pode reter no
// Matcher depois de pontuar o maior lote que o produto permite.
//
// O número é frouxo de propósito — ~5× o medido depois da correção — porque
// medição de heap varia com o GC, com o detector de corrida e com a máquina, e
// um gate apertado viraria teste intermitente. Ele não precisa ser apertado
// para servir: a versão SEM os tetos da memo retinha 118 MB no MESMO cenário,
// ou seja, quase 4× este limite.
const maxHeapDaOperacao = 32 << 20 // 32 MiB

// O cenário do revisor, reproduzido: a memo é a única estrutura do Matcher que
// cresce com DESCRIÇÕES × DONOS e a única que sobrevive à linha, e até a
// correção do A3 ela era limitada em CHAVES (20.000) e nunca em bytes.
//
// A receita, toda dentro dos tetos vigentes do produto:
//
//   - 175 donos (categorias), UMA palavra-chave cada, todas variantes a uma
//     edição de "mercado" — respeitando o índice único de palavra por casa;
//   - 10.000 descrições DISTINTAS (MaxAutoCategorizeRows) contendo "mercado".
//
// Toda descrição ranqueia TODOS os donos: o rank tem 175 Match e a memo guarda
// os 10.000. O revisor mediu 118,4 MB de heap VIVO retido por requisição,
// gastando 6,6% de MaxMatchWork em 0,8 s — nem o orçamento, nem o PlanTimeout,
// nem o pool de conexões pegam isso, e as cotas permitiam empilhar > 2 GB por
// casa (AutoCategorize Burst 3 + InvestmentDetect Burst 3 + ImportUpload, que
// não tem Burst).
//
// O que este teste trava é o HEAP RETIDO POR OPERAÇÃO. Ele falha na versão
// anterior à correção e passa nesta.
func TestMemoNaoRetemHeapAlemDoTeto(t *testing.T) {
	if testing.Short() {
		t.Skip("teste lento (teto de heap da memo): pulado com -short")
	}

	const (
		nDonos      = 175
		nDescricoes = 10_000
	)

	kws := variantesDeUmaEdicao(t, "mercado", nDonos)
	descs := make([]string, nDescricoes)
	for i := range descs {
		// "mercado" em toda descrição — é o token que faz TODO dono ranquear —
		// mais um sufixo curto e único, que torna a descrição distinta (a memo
		// não reaproveita nada) sem custar programação dinâmica (< 5 runas não
		// entra no caminho fuzzy).
		descs[i] = "mercado " + sufixoDeLetras(i)
	}

	m, err := NewMatcher(kws, WithBudget(NewBudget(MaxMatchWork)))
	require.NoError(t, err)

	antes := heapVivo()
	for _, d := range descs {
		rank, err := m.Rank(d)
		require.NoError(t, err, "o cenário tem de caber no orçamento: ele é o teto legítimo do produto")
		require.Len(t, rank, nDonos, "o cenário só mede o que precisa se TODO dono ranquear")
	}
	depois := heapVivo()
	retido := depois - antes

	m.mu.RLock()
	entradas, memorizados := len(m.memo), m.memoMatches
	m.mu.RUnlock()

	t.Logf("heap retido pela operação: %.1f MiB (memo com %d entradas e %d Match; %d descrições × %d donos; trabalho=%d células, %.1f%% de MaxMatchWork) · race=%v",
		float64(retido)/(1<<20), entradas, memorizados, nDescricoes, nDonos,
		m.budget.Spent(), 100*float64(m.budget.Spent())/float64(MaxMatchWork), raceEnabled)

	require.LessOrEqualf(t, retido, int64(maxHeapDaOperacao),
		"uma operação reteve %.1f MiB de heap vivo — o teto é %.1f MiB, e memória não orçada é o achado A3",
		float64(retido)/(1<<20), float64(maxHeapDaOperacao)/(1<<20))

	// O Matcher precisa estar VIVO na hora da medição: sem isto o compilador
	// pode considerá-lo morto antes do segundo ReadMemStats e o teste mediria
	// zero e passaria por engano.
	runtime.KeepAlive(m)
	runtime.KeepAlive(descs)
}

// Os dois tetos da memo são independentes e o de VALOR é o que fecha o buraco
// do A3: com ranking largo, a memo bate nele MUITO antes das 20.000 chaves.
func TestMemoParaDeCrescerNoTetoDeMatches(t *testing.T) {
	t.Parallel()

	const nDonos = 50
	kws := variantesDeUmaEdicao(t, "mercado", nDonos)
	m, err := NewMatcher(kws, WithBudget(NewBudget(MaxMatchWork)))
	require.NoError(t, err)

	// Quantas descrições cabem antes do teto de Match, mais uma dúzia de
	// sobra: o laço tem de atravessar o teto, não parar nele.
	nDescricoes := maxMemoMatches/nDonos + 12
	require.Less(t, nDescricoes, maxMemoEntries, "o teto de CHAVES não pode ser o que corta neste teste")

	for i := range nDescricoes {
		rank, err := m.Rank("mercado " + sufixoDeLetras(i))
		require.NoError(t, err)
		// O resultado continua CERTO depois do teto: a memo deixa de guardar,
		// não de calcular. Truncar em silêncio seria o defeito, não a correção.
		require.Len(t, rank, nDonos)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	require.LessOrEqual(t, m.memoMatches, maxMemoMatches, "o total memorizado não pode passar do teto")
	require.Less(t, len(m.memo), nDescricoes, "a memo tinha de parar de crescer antes do fim do laço")
	require.Equal(t, maxMemoMatches/nDonos, len(m.memo), "o teto de Match é exatamente o que cortou")
}

// variantesDeUmaEdicao devolve n palavras-chave DISTINTAS, cada uma de um dono
// próprio, todas a uma substituição de distância da base — o corpus colidente
// do A3: "mercado" pontua 80 contra todas elas, então toda descrição que o
// contenha ranqueia todos os donos.
func variantesDeUmaEdicao(tb testing.TB, base string, n int) []Keyword {
	tb.Helper()

	runas := []rune(base)
	out := make([]Keyword, 0, n)
	vistas := make(map[string]struct{}, n)
	for pos := range runas {
		for letra := 'a'; letra <= 'z'; letra++ {
			if len(out) == n {
				break
			}
			if letra == runas[pos] {
				continue
			}
			v := make([]rune, len(runas))
			copy(v, runas)
			v[pos] = letra
			p := string(v)
			if _, dup := vistas[p]; dup {
				continue
			}
			vistas[p] = struct{}{}
			out = append(out, Keyword{OwnerID: fmt.Sprintf("owner-%04d", len(out)), Keyword: p, Norm: p})
		}
	}
	require.Lenf(tb, out, n, "a base %q não rende %d variantes distintas de uma edição", base, n)
	for i := range out {
		// Uma substituição no MEIO de "mercado" pontua 80 (o limiar) e uma nas
		// pontas, 90 — as duas casam de verdade. O cenário só mede o que
		// precisa se TODAS pontuarem: se a aritmética da §3 mudar, o teste
		// precisa saber antes de virar uma medição de memo vazia.
		require.GreaterOrEqualf(tb, ScoreWord(runas, []rune(out[i].Keyword)), MinScore,
			"a variante %q precisa casar com a base %q", out[i].Keyword, base)
	}
	return out
}

// sufixoDeLetras converte i num sufixo de 4 letras (26⁴ = 456.976 valores):
// curto o bastante para NÃO entrar no caminho fuzzy (< MinFuzzyRunes runas) e
// único o bastante para a descrição ser distinta.
func sufixoDeLetras(i int) string {
	var b [4]byte
	for p := 3; p >= 0; p-- {
		b[p] = byte('a' + i%26)
		i /= 26
	}
	return string(b[:])
}

// heapVivo devolve o heap em uso depois de coletar. Duas coletas: a primeira
// finaliza o que ficou pendente, a segunda mede o que de fato sobreviveu.
func heapVivo() int64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc)
}
