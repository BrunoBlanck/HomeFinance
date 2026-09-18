package csvtext

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// NumberFormat declara como o layout escreve dinheiro.
//
// O zero-value é INVÁLIDO de propósito: o formato é uma decisão do adaptador do
// banco, e esquecer de declarar tem de dar erro — não um palpite.
type NumberFormat uint8

const (
	_ NumberFormat = iota

	// DecimalPoint — `-20.00`, `1,234.56`: ponto decimal, vírgula de milhar.
	DecimalPoint

	// DecimalComma — `33,70`, `- 2.859,82`: vírgula decimal, ponto de milhar.
	// É o formato do CSV brasileiro.
	DecimalComma
)

func (f NumberFormat) String() string {
	switch f {
	case DecimalPoint:
		return "ponto decimal"
	case DecimalComma:
		return "vírgula decimal"
	default:
		return "não declarado"
	}
}

// maxIntDigits limita a parte inteira antes de qualquer conversão. É folgado em
// relação a MaxAmountCents (9 dígitos) só para que "1234567890123456789012" dê
// erro de faixa em vez de erro de conversão.
const maxIntDigits = 15

// ParseCents converte o texto de um campo de valor em **centavos (int64)**.
//
// ⚠️ ESTE CAMINHO NÃO PODE VER UM FLOAT (ADR-003). `strconv.ParseFloat` é
// proibido aqui, e o motivo é aritmético, não estilístico: `0.29` não existe em
// binário. O que existe é 0.28999999999999998..., e `int64(0.29 * 100)` devolve
// **28**, não 29. Um centavo por lançamento, em milhares de lançamentos
// importados, é um extrato que não fecha — e o erro aparece semanas depois, na
// conciliação, longe da causa.
//
// A conversão correta é textual: separa a parte inteira das casas decimais,
// junta os dígitos e monta o `int64`. Nenhuma etapa envolve ponto flutuante.
//
// O que é aceito, e nada além disso:
//   - sinal `-` ou `+` no início, com ou sem espaço depois (`- 53,81` é o que o
//     extrato de cartão realmente escreve);
//   - símbolo de moeda `R$` ou `$` antes ou depois do sinal;
//   - separador de milhar, desde que o agrupamento seja válido (1–3 dígitos no
//     primeiro grupo, exatamente 3 nos seguintes) — é essa validação que faz
//     `1,2,3` ser recusado em vez de virar `123`;
//   - 0, 1 ou 2 casas decimais. **Três ou mais é recusado**: arredondar seria
//     alterar dinheiro em silêncio, e o mais provável é que o formato
//     declarado esteja errado.
func ParseCents(s string, format NumberFormat) (int64, error) {
	var decSep, thoSep byte
	switch format {
	case DecimalPoint:
		decSep, thoSep = '.', ','
	case DecimalComma:
		decSep, thoSep = ',', '.'
	default:
		return 0, fmt.Errorf("%w: formato numérico não declarado", ErrInvalidNumber)
	}

	original := s
	// Espaços (inclusive o NBSP, que alguns bancos usam como separador de
	// milhar) e caracteres invisíveis saem por completo: um número não tem
	// espaço significativo, e é isso que resolve `- 2.859,82`.
	cleaned := removerInvisiveis(s)
	if cleaned == "" {
		return 0, fmt.Errorf("%w: valor vazio", ErrInvalidNumber)
	}

	cleaned = trimCurrency(cleaned)
	negative := false
	switch {
	case strings.HasPrefix(cleaned, "-"):
		negative = true
		cleaned = cleaned[1:]
	case strings.HasPrefix(cleaned, "+"):
		cleaned = cleaned[1:]
	}
	cleaned = trimCurrency(cleaned) // "-R$ 20,00"
	if cleaned == "" {
		return 0, fmt.Errorf("%w: %q não tem dígitos", ErrInvalidNumber, original)
	}

	intText, fracText := cleaned, ""
	if i := strings.IndexByte(cleaned, decSep); i >= 0 {
		intText, fracText = cleaned[:i], cleaned[i+1:]
		if strings.IndexByte(fracText, decSep) >= 0 {
			return 0, fmt.Errorf("%w: %q tem mais de um separador decimal", ErrInvalidNumber, original)
		}
		if fracText == "" {
			return 0, fmt.Errorf("%w: %q termina no separador decimal", ErrInvalidNumber, original)
		}
	}

	intDigits, err := digitosAgrupados(intText, thoSep)
	if err != nil {
		return 0, fmt.Errorf("%w: %q — %v", ErrInvalidNumber, original, err)
	}

	centavosFrac, err := fracaoEmCentavos(fracText)
	if err != nil {
		return 0, fmt.Errorf("%w: %q — %v", ErrInvalidNumber, original, err)
	}

	if len(intDigits) > maxIntDigits {
		return 0, fmt.Errorf("%w: %q", ErrAmountOutOfRange, original)
	}
	unidades, err := strconv.ParseInt(intDigits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrAmountOutOfRange, original)
	}
	// A checagem vem ANTES da multiplicação: em Go o overflow de int64 é
	// silencioso, então "verificar depois" verificaria um número já errado.
	if unidades > MaxAmountCents/100 {
		return 0, fmt.Errorf("%w: %q", ErrAmountOutOfRange, original)
	}

	cents := unidades*100 + centavosFrac
	if cents > MaxAmountCents {
		return 0, fmt.Errorf("%w: %q", ErrAmountOutOfRange, original)
	}
	if negative {
		cents = -cents
	}
	return cents, nil
}

// removerInvisiveis tira espaços (todos, inclusive NBSP) e caracteres de
// formatação Unicode (BOM, zero-width) de dentro do número.
func removerInvisiveis(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// trimCurrency remove um símbolo de moeda do início. Allowlist fechada: só
// `R$` e `$`, nada de "qualquer coisa não numérica no começo".
func trimCurrency(s string) string {
	for _, prefixo := range []string{"R$", "r$", "$"} {
		if depois, ok := strings.CutPrefix(s, prefixo); ok {
			return depois
		}
	}
	return s
}

// digitosAgrupados valida o agrupamento de milhar e devolve só os dígitos.
//
// É esta validação que recusa `1,2,3`: sem ela, remover os separadores daria
// "123" — um número plausível e completamente inventado.
func digitosAgrupados(s string, thoSep byte) (string, error) {
	if s == "" {
		return "", fmt.Errorf("sem parte inteira")
	}
	grupos := strings.Split(s, string(thoSep))
	if len(grupos) > 1 {
		for i, g := range grupos {
			if i == 0 {
				if len(g) < 1 || len(g) > 3 {
					return "", fmt.Errorf("agrupamento de milhar inválido")
				}
				continue
			}
			if len(g) != 3 {
				return "", fmt.Errorf("agrupamento de milhar inválido")
			}
		}
	}
	digitos := strings.Join(grupos, "")
	if err := somenteDigitos(digitos); err != nil {
		return "", err
	}
	return digitos, nil
}

// fracaoEmCentavos converte as casas decimais em centavos.
func fracaoEmCentavos(frac string) (int64, error) {
	if err := somenteDigitos(frac); err != nil {
		return 0, err
	}
	switch len(frac) {
	case 0:
		return 0, nil
	case 1:
		return int64(frac[0]-'0') * 10, nil
	case 2:
		return int64(frac[0]-'0')*10 + int64(frac[1]-'0'), nil
	default:
		return 0, fmt.Errorf("%d casas decimais (o máximo é 2)", len(frac))
	}
}

func somenteDigitos(s string) error {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return fmt.Errorf("caractere inesperado")
		}
	}
	return nil
}
