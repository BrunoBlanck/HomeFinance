package report

import (
	"math"
	"math/bits"
	"sort"
)

// apportion reparte `total` entre partes proporcionalmente a `weights`
// (centavos, ≥ 0) pelo método do MAIOR RESTO (Hamilton), de modo que a soma
// das partes seja EXATAMENTE `total`.
//
// Por que não float: 33,33 % + 33,33 % + 33,33 % dá 99,99 %, e o gráfico fica
// com um buraco. Com inteiros e sobra distribuída, a soma fecha por
// construção (ADR-027c). Por que 128 bits: `centavos × 10000` estoura int64 a
// partir de ~R$ 9,2 trilhões — improvável numa casa, mas "improvável" não é
// garantia, e este código roda em caminho de requisição, onde panic é
// proibido.
//
// Garantias:
//   - Σ out == total quando Σ weights > 0 e total > 0; senão todos zero;
//   - nenhuma parte negativa; parte de peso 0 nunca recebe sobra;
//   - ordem preservada: w_i > w_j ⇒ out_i ≥ out_j;
//   - sem panic: Div64 só roda com divisor > 0 e hi < divisor (w_i ≤ Σw e
//     total < 2^64 garantem hi < Σw), e a soma dos pesos é conferida contra
//     estouro de uint64.
//
// Peso negativo é tratado como zero: é defesa contra chamador errado, não
// caso de uso — o serviço só passa somas de amount_cents, que é ≥ 0.
//
// Desempate da sobra: maior resto; depois maior peso; depois menor índice
// (estável). Isso torna o resultado determinístico para a mesma entrada.
func apportion(weights []int64, total int64) []int64 {
	out := make([]int64, len(weights))
	if len(weights) == 0 || total <= 0 {
		return out
	}

	// Σw em uint64, com detecção de carry: se não couber, não há divisão
	// segura a fazer — devolve zeros em vez de estourar em silêncio. O
	// serviço já recusou antes um total que não cabe em int64, então este
	// caminho é inalcançável em produção; existe para o invariante "sem
	// panic" não depender do chamador.
	var sum uint64
	for _, w := range weights {
		if w <= 0 {
			continue
		}
		var carry uint64
		sum, carry = bits.Add64(sum, uint64(w), 0)
		if carry != 0 {
			return out
		}
	}
	if sum == 0 {
		return out
	}

	type resto struct {
		index int
		rem   uint64
		w     uint64
	}
	restos := make([]resto, 0, len(weights))
	var distribuido uint64
	t := uint64(total)
	for i, w := range weights {
		if w <= 0 {
			continue
		}
		// hi:lo = w × total. Como w ≤ Σw e total < 2^64, hi < Σw — a
		// precondição de Div64 para não entrar em pânico.
		hi, lo := bits.Mul64(uint64(w), t)
		q, rem := bits.Div64(hi, lo, sum)
		// q ≤ total ≤ MaxInt64 por construção (w ≤ Σw). A guarda torna a
		// conversão verificável em vez de confiada: se um dia o invariante
		// quebrar, a resposta é "tudo zero", nunca um número negativo.
		if q > math.MaxInt64 {
			return make([]int64, len(weights))
		}
		out[i] = int64(q)
		distribuido += q
		if rem > 0 {
			restos = append(restos, resto{index: i, rem: rem, w: uint64(w)})
		}
	}

	// sobra = total − Σ quotas = Σ rem_i / Σw, que é < n. Cada resto é < Σw,
	// então há SEMPRE mais partes com resto > 0 do que unidades a distribuir
	// — o laço abaixo nunca fica sem destinatário.
	sobra := t - distribuido
	if sobra == 0 {
		return out
	}
	sort.SliceStable(restos, func(a, b int) bool {
		if restos[a].rem != restos[b].rem {
			return restos[a].rem > restos[b].rem
		}
		return restos[a].w > restos[b].w
	})
	for i := 0; i < len(restos) && sobra > 0; i++ {
		out[restos[i].index]++
		sobra--
	}
	return out
}
