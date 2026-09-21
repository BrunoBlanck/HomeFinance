package aiimport

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Heap retido pela MEDIÇÃO DE IMPACTO (ADR-036 (f), textmatch/matcher.go)
// ---------------------------------------------------------------------------

// maxHeapDaMedicao é o teto de heap VIVO que UMA prévia pode reter na
// medição de impacto, no maior cenário que o produto permite.
//
// Ele é a parcela desta rota na CONTA DE HEAP POR CASA de
// internal/textmatch/matcher.go: a prévia tem `Burst` 3, então três medições
// podem estar em voo ao mesmo tempo. O número é frouxo de propósito (~3× o
// medido), como o gate irmão em textmatch/memoria_test.go: medição de heap
// varia com o GC, com o detector de corrida e com a máquina, e um gate
// apertado viraria teste intermitente. O que ele trava é a ORDEM DE
// GRANDEZA — se um dia a medição passar a reter o que uma auto-categorização
// retém (~45–47 MiB), a conta do quadro está errada e este teste avisa.
const maxHeapDaMedicao = 24 << 20 // 24 MiB

// O cenário, todo dentro dos tetos do produto:
//
//   - 200 itens de conta (MaxEntriesPerList) com 5 palavras aprovadas cada —
//     1.000 palavras, o máximo que 50 contas × 20 comportam —, todas
//     variantes a uma edição de seis bases, para TODA descrição ranquear
//     TODO item;
//   - 5.000 descrições DISTINTAS (transaction.MaxDescriptionGroupRows)
//     contendo as seis bases.
//
// É a receita do achado A3 aplicada à medição: descrições × donos é o único
// termo não linear, e a memo do ÚNICO matcher desta operação é quem o
// retém — limitada em bytes por maxMemoMatches.
func TestMedicaoDeImpactoNaoRetemHeapAlemDoTeto(t *testing.T) {
	if testing.Short() {
		t.Skip("teste lento (teto de heap da medição): pulado com -short")
	}

	const (
		nItens         = MaxEntriesPerList
		palavrasPorItm = 5
		nDescricoes    = 5000
	)
	// Bases de SETE runas: numa palavra maior, a substituição no meio deixa
	// uma substring comum de 5 runas e a regra 2 decide ANTES da regra 3, com
	// pontuação abaixo do limiar ("faXmacia" → 74). Com sete runas a regra 3
	// é quem responde, ≥ 80 — e as variantes são FILTRADAS pela pontuação
	// real de qualquer forma, para o cenário medir o que precisa medir.
	bases := []string{"mercado", "padaria", "clinica", "estacao", "cinemas", "livrari",
		"academi", "petshop", "lanchon", "pizzari", "shoppin", "farmaci", "drogari", "cafeter", "restaur"}

	variantes := make([]string, 0, nItens*palavrasPorItm)
	vistas := map[string]struct{}{}
	for _, base := range bases {
		runas := []rune(base)
		for pos := range runas {
			for letra := 'a'; letra <= 'z'; letra++ {
				if letra == runas[pos] {
					continue
				}
				v := make([]rune, len(runas))
				copy(v, runas)
				v[pos] = letra
				p := string(v)
				if _, dup := vistas[p]; dup || textmatch.ScoreWord(runas, v) < textmatch.MinScore {
					continue
				}
				vistas[p] = struct{}{}
				variantes = append(variantes, p)
			}
		}
	}
	require.GreaterOrEqual(t, len(variantes), nItens*palavrasPorItm)

	p := &plano{}
	for i := range nItens {
		item := itemPlanejado{view: novoItemView(ItemTypeAccount, "018f0000-0000-7000-8000-"+fmt.Sprintf("%012d", i)), dona: "conta"}
		for j := range palavrasPorItm {
			item.view.Added = append(item.view.Added, variantes[i*palavrasPorItm+j])
		}
		p.itens = append(p.itens, item)
	}

	periodo := periodoAgregado{ocorrenciasPorNorm: make(map[string]int64, nDescricoes), total: nDescricoes}
	prefixo := ""
	for _, b := range bases {
		prefixo += b + " "
	}
	for i := range nDescricoes {
		periodo.ocorrenciasPorNorm[prefixo+sufixoDeLetras(i)] = 1
	}

	// O heap é medido com o matcher VIVO — é o que a requisição retém
	// enquanto está em voo, e é esse número que se empilha por casa. Medir
	// depois de ele morrer mediria zero.
	antes := heapVivo()
	m, err := montarMedidor(p, textmatch.MaxMatchWork)
	require.NoError(t, err)
	require.NoError(t, m.medir(p, periodo))
	depois := heapVivo()
	retido := depois - antes
	runtime.KeepAlive(m)

	// Toda descrição alcança todo item e toda palavra: é o que faz o cenário
	// medir o que precisa medir.
	for i := range p.itens {
		impacto := p.itens[i].view.Impact
		require.EqualValues(t, nDescricoes, impacto.TransferCandidates, "item %d", i)
		require.Len(t, impacto.ByKeyword, palavrasPorItm)
		for _, k := range impacto.ByKeyword {
			require.EqualValues(t, nDescricoes, k.TransferCandidates)
		}
	}

	t.Logf("heap retido pela medição de impacto: %.1f MiB (%d itens × %d palavras; %d descrições) · race=%v",
		float64(retido)/(1<<20), nItens, palavrasPorItm, nDescricoes, raceEnabled)
	require.LessOrEqual(t, retido, int64(maxHeapDaMedicao),
		"a medição retém %.1f MiB, acima do teto de %d MiB — refaça a conta de heap por casa em textmatch/matcher.go",
		float64(retido)/(1<<20), maxHeapDaMedicao>>20)
	runtime.KeepAlive(p)
}

// sufixoDeLetras converte i num sufixo de 4 letras: curto o bastante para
// não entrar no caminho fuzzy e único o bastante para a descrição ser
// distinta (o mesmo helper de textmatch/memoria_test.go).
func sufixoDeLetras(i int) string {
	var b [4]byte
	for p := 3; p >= 0; p-- {
		b[p] = byte('a' + i%26)
		i /= 26
	}
	return string(b[:])
}

// heapVivo devolve o heap em uso depois de coletar duas vezes.
func heapVivo() int64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc)
}
