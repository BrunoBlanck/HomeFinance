package textmatch

// Este arquivo é a aritmética da §3: funções PURAS, sem estado, sem índice.
// O Matcher (matcher.go) só as chama menos vezes — nunca as substitui. É o
// que permite ao teste de propriedade comparar o resultado com prefiltro ao
// resultado por força bruta e exigir igualdade.

// roundDiv divide n por d arredondando meio-para-cima, em inteiros. É a
// única forma de arredondamento do pacote: `(2n + d) / (2d)` dá 92,5 → 93 e
// 87,5 → 88, como a tabela da spec espera. Exige n ≥ 0 e d > 0 — os dois
// chamadores garantem isso (o numerador da regra 2 é sempre positivo porque
// cada "sobra" fica abaixo de 100%).
func roundDiv(n, d int) int { return (2*n + d) / (2 * d) }

// ScoreWord pontua UMA palavra da descrição contra UMA palavra da
// palavra-chave. A primeira regra que casar decide:
//
//  1. Iguais → 100.
//  2. Maior substring comum C ≥ MinFuzzyRunes → 100 − 30·(sobra da maior) −
//     40·(sobra da menor), onde a sobra é a fração de runas fora da parte
//     comum. Em inteiros: num = 100·LM·Lm − 30·(LM−C)·Lm − 40·(Lm−C)·LM,
//     den = LM·Lm, pontuação = roundDiv(num, den).
//  3. As duas com ≥ MinTypoRunes runas e a uma edição de distância (OSA) →
//     roundDiv(100·L − 100, L) com L = comprimento da maior (≈ 100 − 100/L).
//
// Senão 0. Uma palavra com menos de 5 runas nunca alcança a regra 2 (a
// substring comum cabe nela) nem a 3 — ou seja, só casa inteira.
//
// PODA POR COMPRIMENTO (achado A1 da revisão de segurança): o par cujo TETO
// de pontuação — MaxScoreForLengths, o caso de contenção total — já é menor
// que MinScore devolve 0 SEM rodar a programação dinâmica. A poda é
// provadamente inócua para o produto (nenhuma pontuação utilizável é
// perdida; ver MaxScoreForLengths e o teste de força bruta), e é o que
// impede uma palavra-chave de 40 runas de pagar uma matriz de 140×40 células
// contra cada palavra de cada descrição.
//
// O efeito colateral é DECLARADO: um par podado devolve 0 em vez da sua
// pontuação real, que estaria abaixo de MinScore e portanto nunca seria
// usada. Rank e Best não mudam de resposta — ver o doc de Rank.
func ScoreWord(word, keyword []rune) int {
	if len(word) == 0 || len(keyword) == 0 {
		return 0
	}
	if runesEqual(word, keyword) {
		return 100
	}

	lm, lM := min(len(word), len(keyword)), max(len(word), len(keyword))

	// Poda por comprimento: nem o melhor C possível alcança o limiar.
	if MaxScoreForLengths(lM, lm) < MinScore {
		return 0
	}

	// A substring comum nunca passa da palavra menor: abaixo do mínimo, nem
	// vale calcular.
	if lm >= MinFuzzyRunes {
		if c := LongestCommonSubstring(word, keyword); c >= MinFuzzyRunes {
			num := 100*lM*lm - 30*(lM-c)*lm - 40*(lm-c)*lM
			return roundDiv(num, lM*lm)
		}
	}

	if lm >= MinTypoRunes && EditDistanceIsOne(word, keyword) {
		return roundDiv(100*lM-100, lM)
	}

	return 0
}

// MaxScoreForLengths devolve o MAIOR valor que ScoreWord pode dar a um par de
// palavras com lM runas (a maior) e lm runas (a menor) — o teto do par, antes
// de olhar uma única rune.
//
// Derivação, da MESMA fórmula da regra 2 e com o MESMO roundDiv inteiro: a
// pontuação cresce com C (a maior substring comum), e C nunca passa de lm.
// No caso extremo C = lm (contenção total), a sobra da menor zera e sobra
//
//	num = 100·lM·lm − 30·(lM−lm)·lm,  den = lM·lm
//
// que, dividido por lm em cima e embaixo — roundDiv(k·n, k·d) == roundDiv(n, d)
// para k > 0 —, é roundDiv(100·lM − 30·(lM−lm), lM).
//
// ATENÇÃO — por que NÃO é a poda "lM > 3·lm" do plano §3.2: com lM=22, lm=7 e
// C=7 a conta dá 79,545…, que roundDiv leva a 80 = MinScore. A poda literal do
// plano descartaria esse par e mudaria o resultado. Esta função não: ela
// calcula o teto com a aritmética de verdade e devolve 80 nesse par.
//
// As regras 1 e 3 não escapam do teto: a 1 exige lM == lm (teto 100) e a 3
// exige |lM − lm| ≤ 1, caso em que o teto nunca fica abaixo de 85. Ou seja,
// teto < MinScore implica que NENHUMA das três regras alcança MinScore — é o
// que o teste de força bruta confere par a par.
func MaxScoreForLengths(lM, lm int) int {
	if lM <= 0 || lm <= 0 {
		return 0
	}
	return roundDiv(100*lM-30*(lM-lm), lM)
}

// WorkOfScoreWord devolve o CUSTO que ScoreWord vai pagar por este par, na
// unidade do orçamento de trabalho (Budget): células da matriz de programação
// dinâmica.
//
// Existe para o Matcher cobrar ANTES de gastar — cobrar depois é um teto que
// chega tarde. E é preciso ser o custo REAL, não uma estimativa por cima: o
// par podado por comprimento custa uma comparação de inteiros, e cobrá-lo
// como se rodasse a matriz inteira faria descrições longas e legítimas
// estourarem um orçamento que elas nunca gastaram.
//
// Os três regimes, na ordem de ScoreWord:
//
//	podado por comprimento      → 1   (só a aritmética do teto)
//	menor abaixo de MinFuzzy    → lM  (só a comparação de igualdade)
//	caso geral                  → lM · lm (a matriz de LongestCommonSubstring)
func WorkOfScoreWord(word, keyword []rune) int {
	lm, lM := min(len(word), len(keyword)), max(len(word), len(keyword))
	if lM == 0 {
		return 1
	}
	if MaxScoreForLengths(lM, lm) < MinScore {
		return 1
	}
	if lm < MinFuzzyRunes {
		return lM
	}
	return lM * lm
}

// ScoreKeyword pontua uma palavra-chave (já tokenizada) contra uma descrição
// (já tokenizada), as duas por Tokenize:
//
//   - Regra 1 (100): a palavra-chave de N tokens aparece como N tokens
//     CONSECUTIVOS, na ordem, entre os da descrição — é o que "frase inteira
//     com fronteira dos dois lados" significa depois da tokenização, já que os
//     dois lados passam pela mesma remoção de palavras vazias.
//   - Senão, cada token da palavra-chave fica com o MÁXIMO de ScoreWord contra
//     os tokens da descrição, e a palavra-chave vale o MÍNIMO entre os seus
//     tokens: todos precisam casar.
func ScoreKeyword(descTokens, keywordTokens []string) int {
	if len(descTokens) == 0 || len(keywordTokens) == 0 {
		return 0
	}
	if containsRun(descTokens, keywordTokens) {
		return 100
	}

	score := 100
	for _, k := range keywordTokens {
		kr := []rune(k)
		best := 0
		for _, w := range descTokens {
			if s := ScoreWord([]rune(w), kr); s > best {
				best = s
				if best == 100 {
					break
				}
			}
		}
		if best == 0 {
			return 0
		}
		score = min(score, best)
	}
	return score
}

// containsRun diz se `run` aparece inteira e consecutiva dentro de `tokens`.
func containsRun(tokens, run []string) bool {
	if len(run) == 0 || len(run) > len(tokens) {
		return false
	}
	for i := 0; i+len(run) <= len(tokens); i++ {
		if tokens[i] != run[0] {
			continue
		}
		j := 1
		for j < len(run) && tokens[i+j] == run[j] {
			j++
		}
		if j == len(run) {
			return true
		}
	}
	return false
}

// LongestCommonSubstring devolve o comprimento da maior substring CONTÍGUA
// comum a `a` e `b` (não confundir com subsequência). Programação dinâmica
// clássica em duas linhas, O(len(a)·len(b)) de tempo e O(min) de memória —
// as palavras têm no máximo dezenas de runas, então é barato.
func LongestCommonSubstring(a, b []rune) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// A linha da DP acompanha a palavra menor: menos memória e menos trabalho.
	if len(b) > len(a) {
		a, b = b, a
	}

	// Buffer na pilha para o caso comum (palavra-chave ≤ 40 runas); só aloca
	// se a entrada for maior — ScoreWord roda milhares de vezes por lote.
	const stackWidth = 64
	var buf [2][stackWidth]int
	var prev, cur []int
	if len(b) < stackWidth {
		prev, cur = buf[0][:len(b)+1], buf[1][:len(b)+1]
	} else {
		prev, cur = make([]int, len(b)+1), make([]int, len(b)+1)
	}

	best := 0
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			if a[i-1] == b[j-1] {
				cur[j] = prev[j-1] + 1
				if cur[j] > best {
					best = cur[j]
				}
			} else {
				cur[j] = 0
			}
		}
		prev, cur = cur, prev
	}
	return best
}

// EditDistanceIsOne diz se a distância OSA (Damerau-Levenshtein restrita:
// inserção, remoção, substituição ou transposição de vizinhas, cada uma
// custando 1) entre `a` e `b` é EXATAMENTE 1. Iguais → false; diferença de
// comprimento maior que 1 → false.
//
// Não roda a DP completa: distância 1 quer dizer que UMA operação transforma
// uma na outra, então basta achar o primeiro desacordo e conferir se o resto
// coincide sob cada uma das quatro operações. Linear e sem alocação.
func EditDistanceIsOne(a, b []rune) bool {
	switch len(a) - len(b) {
	case 0:
		i := 0
		for i < len(a) && a[i] == b[i] {
			i++
		}
		if i == len(a) {
			return false // iguais: distância 0
		}
		// Substituição em i.
		if runesEqual(a[i+1:], b[i+1:]) {
			return true
		}
		// Transposição de i com i+1.
		return i+1 < len(a) && a[i] == b[i+1] && a[i+1] == b[i] && runesEqual(a[i+2:], b[i+2:])
	case 1:
		return isOneInsertion(a, b)
	case -1:
		return isOneInsertion(b, a)
	default:
		return false
	}
}

// isOneInsertion confere se `long` (com exatamente uma rune a mais) é `short`
// com uma rune inserida em algum ponto.
func isOneInsertion(long, short []rune) bool {
	i := 0
	for i < len(short) && long[i] == short[i] {
		i++
	}
	return runesEqual(long[i+1:], short[i:])
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
