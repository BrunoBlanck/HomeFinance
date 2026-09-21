package importer_test

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QA da E2c — critério 11 medido PONTA A PONTA: o textmatch/perf_test.go
// cronometra só o Matcher; este cronometra o Analyze inteiro (parse, dedup,
// classificação e gravação do staging em SQLite) com 10.000 linhas (2.500
// descrições distintas) e ~1.000 palavras-chave da casa — o teto de linhas
// por arquivo e o teto prático de palavras (200 categorias × 5).
//
// ESTE TESTE NÃO TEM GATE DE RELÓGIO (decisão de 18/09/2026).
//
// A spec fala em "≤ 2 s no SQLite local" para a análise, e medido isolado em
// 17/09/2026 ele dava 0,5–0,9 s. Só que o número depende da MÁQUINA e da
// carga: com o pacote inteiro rodando sob `-race` mediu-se 91 s contra um
// limite de 60 s, e o mesmo caso sozinho levou 36 s. Um gate que falha por
// contenção, e não por regressão, ensina o time a ignorar vermelho — que é
// pior do que não ter gate nenhum.
//
// O que ficou no lugar:
//
//   - AQUI, asserções DETERMINÍSTICAS de comportamento: a fase 1 termina sem
//     ser cortada pelo próprio prazo (AnalyzeTimeout é um deadline de contexto
//     DE VERDADE dentro de Analyze — análise lenta demais não "passa devagar",
//     ela falha), nenhuma linha é rejeitada, e a classificação de fato rodou;
//   - o TEMPO continua medido e escrito no log, para o reporte;
//   - o gate de REGRESSÃO de desempenho mudou de unidade e de lugar: é a
//     CONTAGEM DE TRABALHO do casamento por palavra-chave, em
//     textmatch/perf_test.go (TestOrcamentoCobreOTetoLegitimo), que é
//     determinística — não depende de máquina, de carga nem de `-race` — e
//     falha se o custo do algoritmo crescer.
//
// Lento de propósito: `-short` pula.

func TestDesempenhoDaAnaliseComDezMilLinhasEMilPalavrasChave(t *testing.T) {
	if testing.Short() {
		t.Skip("teste lento (critério 11 ponta a ponta): pulado com -short")
	}
	// Sem t.Parallel() de propósito: os testes seriais rodam ANTES dos
	// paralelos, sozinhos — a medição não fica distorcida pelos outros
	// bancos SQLite do pacote.

	const (
		nCategorias  = 190 // < MaxPerHousehold (200)
		porCategoria = 5   // 950 palavras de categoria
		nDistintas   = 2_500
		nLinhas      = 10_000
		letras       = "abcdefghijklmnopqrstuvwxyz"
	)
	const criterioDaSpec = 2 * time.Second
	rng := rand.New(rand.NewPCG(11, 2026))
	palavra := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = letras[rng.IntN(len(letras))]
		}
		return string(b)
	}

	// O AnalyzeTimeout de produção (15 s) também é relógio, e pelo mesmo
	// motivo não pode ser o gate: com o pacote inteiro sob `-race` a fase 1
	// mediu 10,4 s sozinha e passou dos 15 s sob contenção, e o que se via era
	// um 500 no meio do gate — carga de máquina virando vermelho. Aqui ele é
	// afrouxado para o teste medir o PIPELINE; o número de produção continua
	// medido e escrito no log.
	a := novoAmbiente(t, importer.WithAnalyzeTimeout(5*time.Minute))
	contaA := a.contaNubankComPalavra(t)
	contaB := a.contaItau(t)
	a.palavrasNaConta(t, contaB.ID, "itau", "banco itau", "itau unibanco")

	// 190 categorias × 5 palavras únicas de 6–12 letras (regras 2 e 3 só
	// rodam a partir de 5 runas).
	inicio := time.Now()
	usadas := map[string]bool{"itau": true}
	var todas []string
	for c := range nCategorias {
		kws := make([]string, 0, porCategoria)
		for len(kws) < porCategoria {
			p := palavra(6 + rng.IntN(7))
			if usadas[p] {
				continue
			}
			usadas[p] = true
			kws = append(kws, p)
		}
		todas = append(todas, kws...)
		kind := category.KindExpense
		if c%10 == 0 {
			kind = category.KindIncome
		}
		a.categoriaComPalavras(t, fmt.Sprintf("Categoria %03d", c), kind, kws...)
	}
	t.Logf("cadastro: %d categorias com %d palavras em %s", nCategorias, len(todas), time.Since(inicio))

	// 2.500 descrições distintas derivadas das palavras: exata, com prefixo e
	// sufixo, com um erro de digitação, ou sem relação nenhuma.
	distintas := make([]string, nDistintas)
	for i := range distintas {
		p := todas[rng.IntN(len(todas))]
		switch rng.IntN(5) {
		case 0:
			distintas[i] = "Transferência enviada pelo Pix - " + p
			if i%3 == 0 {
				// Cita a conta B: exercita a detecção de transferência também.
				distintas[i] = "Transferência enviada pelo Pix - Itau Unibanco " + p
			}
		case 1:
			distintas[i] = "Compra no débito - " + p + " LTDA " + palavra(3)
		case 2:
			r := []rune(p)
			j := rng.IntN(len(r))
			r[j] = rune(letras[rng.IntN(len(letras))])
			distintas[i] = "Compra no débito - " + string(r)
		case 3:
			distintas[i] = "Compra no débito - " + p[:len(p)-1] + "inho"
		default:
			distintas[i] = "Compra no débito - " + palavra(8) + " " + palavra(5)
		}
	}

	var csv bytes.Buffer
	csv.WriteString("Data,Valor,Identificador,Descrição\n")
	for i := range nLinhas {
		d := distintas[rng.IntN(len(distintas))]
		valor := fmt.Sprintf("-%d.%02d", 1+rng.IntN(500), rng.IntN(100))
		if i%50 == 0 {
			valor = valor[1:] // receita de vez em quando
		}
		fmt.Fprintf(&csv, "%02d/08/2026,%s,%08x-1111-4111-8111-%012d,%s\n", 1+rng.IntN(31), valor, rng.Uint32(), i, d)
	}

	inicio = time.Now()
	lote := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "grande.csv", csv.Bytes()))
	analise := time.Since(inicio)
	require.NotNil(t, lote.Counts)
	t.Logf("critério 11 ponta a ponta: Analyze de %d linhas (%d distintas) × %d palavras-chave em %s (critério da spec: %s — atendido=%v; prazo de produção: %s; race=%v) · novo=%d repetido=%d transferencia_interna=%d rejeitado=%d",
		nLinhas, nDistintas, len(todas)+4, analise, criterioDaSpec, analise <= criterioDaSpec,
		importer.AnalyzeTimeout, raceEnabled,
		lote.Counts.New, lote.Counts.RepeatedInFile, lote.Counts.InternalTransfer, lote.Counts.Rejected)

	// Determinístico: a fase 1 não foi cortada pelo próprio prazo nem pelo
	// orçamento de trabalho. Se tivesse sido, não haveria lote nenhum — o
	// AnalyzeTimeout é deadline de contexto, e o estouro do orçamento é 422.
	assert.Zero(t, lote.Counts.Rejected)
	classificadas := lote.Counts.New + lote.Counts.RepeatedInFile + lote.Counts.DuplicateExact +
		lote.Counts.DuplicateDeleted + lote.Counts.PossibleDup + lote.Counts.CardPayment +
		lote.Counts.InternalTransfer + lote.Counts.TransferAlreadyRegistered + lote.Counts.Rejected
	assert.EqualValues(t, nLinhas, classificadas,
		"a análise precisa ter classificado TODAS as linhas, não uma parte")

	// A classificação aconteceu de verdade: uma fatia relevante das linhas
	// ganhou sugestão, e a revisão (uma página) volta rápido.
	inicio = time.Now()
	primeira := revisarPelaAPI(t, a, lote.ID)
	t.Logf("revisão (primeira página de 200): %s", time.Since(inicio))
	sugeridas := 0
	for _, l := range primeira.Items {
		if l.SuggestedCategoryID != nil || l.Status == string(dedup.StatusInternalTransfer) {
			sugeridas++
		}
	}
	assert.Greater(t, sugeridas, len(primeira.Items)/4, "o cenário precisa exercitar as regras de verdade")
}
