// Package textmatch pontua palavras-chave contra descrições de lançamentos
// (spec 0005, §3): é o motor da categorização automática e da detecção de
// transferências internas.
//
// Por que um algoritmo próprio, em vez de LIKE no banco, regex do usuário ou
// um modelo de linguagem: a comparação precisa dar o MESMO resultado nos
// quatro dialetos SQL, ser explicável para a pessoa ("casou por aproximação,
// 88%, com «supermercado»"), rodar em milissegundos sobre milhares de linhas
// e não aceitar nada configurável que vire vetor de abuso (regex do usuário é
// ReDoS esperando para acontecer). Três regras fixas, inteiras e auditáveis
// resolvem isso: igualdade de palavra inteira, maior substring comum e erro de
// digitação a uma edição de distância.
//
// Tudo aqui é ARITMÉTICA INTEIRA. Pontuação nunca passa por float: a mesma
// entrada dá o mesmo inteiro em qualquer máquina, e a tabela de teste da spec
// confere cada valor.
//
// Os dois lados chegam já normalizados por textnorm.Normalize (sem acento,
// minúsculas, espaços colapsados): a descrição vem de `description_norm` e a
// palavra-chave de `keyword_norm`. Este pacote é FOLHA: do projeto, importa
// apenas textnorm.
package textmatch

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	unorm "golang.org/x/text/unicode/norm"

	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

const (
	// MinScore é o limiar fixo desta entrega: abaixo dele não há sugestão.
	MinScore = 80
	// MinFuzzyRunes é o tamanho mínimo da maior substring comum para a regra 2
	// valer. Como a substring cabe nas duas palavras, uma palavra-chave com
	// menos de 5 runas ("pix", "uber", "c6") só casa inteira — de propósito:
	// aproximar palavras curtas casa com tudo.
	MinFuzzyRunes = 5
	// MinTypoRunes é o comprimento mínimo das DUAS palavras para a regra 3
	// (uma edição de distância) valer.
	MinTypoRunes = 6
	// MinKeywordRunes e MaxKeywordRunes limitam a palavra-chave, tanto na
	// forma exibível quanto na normalizada.
	MinKeywordRunes = 2
	MaxKeywordRunes = 40

	// minTokenRunes: palavra de uma rune só não é palavra ("e", "o", "&").
	minTokenRunes = 2
)

// Stopwords é a lista FECHADA de palavras vazias da §3 da spec: não é
// configurável, e o teste TestStopwordsSaoExatamenteAsDaSpec trava o
// conteúdo. É exportada para o teste e para a fixture que o frontend compara;
// trate como somente leitura.
var Stopwords = map[string]struct{}{
	"de": {}, "do": {}, "da": {}, "dos": {}, "das": {},
	"e": {}, "o": {}, "a": {}, "os": {}, "as": {},
	"em": {}, "no": {}, "na": {}, "nos": {}, "nas": {},
	"um": {}, "uma": {},
	"por": {}, "para": {}, "com": {}, "sem": {},
	"seu": {}, "sua": {},
	"ltda": {}, "me": {}, "sa": {}, "eireli": {}, "epp": {},
}

// ErrInvalidKeyword é devolvido por ValidateKeyword e NewMatcher. O erro
// embrulhado traz a RAZÃO, nunca a palavra: ele pode acabar num log.
var ErrInvalidKeyword = errors.New("invalid keyword")

// isWordRune diz se a rune faz parte de uma palavra. Letra ou número
// (categorias L e N do Unicode — o mesmo `\p{L}\p{N}` do contrato OpenAPI);
// qualquer outra coisa separa palavras.
func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }

// isKeywordRune é a allowlist da §4.1.2: letra, número, espaço e `& . - / '`.
func isKeywordRune(r rune) bool {
	switch r {
	case ' ', '&', '.', '-', '/', '\'':
		return true
	}
	return isWordRune(r)
}

// Tokenize separa a forma NORMALIZADA em palavras: qualquer rune que não seja
// letra ou número separa; palavras com menos de 2 runas e as Stopwords caem.
// "mercado do seu jose" → [mercado jose]; "netflix.com" → [netflix com].
//
// A entrada precisa vir de textnorm.Normalize — a lista de palavras vazias é
// minúscula e sem acento, e só é comparada por igualdade. Determinística:
// mesma entrada, mesma saída, sempre.
func Tokenize(norm string) []string {
	var tokens []string
	start := -1
	for i, r := range norm {
		if isWordRune(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			tokens = appendToken(tokens, norm[start:i])
			start = -1
		}
	}
	if start >= 0 {
		tokens = appendToken(tokens, norm[start:])
	}
	return tokens
}

func appendToken(tokens []string, tok string) []string {
	if utf8.RuneCountInString(tok) < minTokenRunes {
		return tokens
	}
	if _, vazia := Stopwords[tok]; vazia {
		return tokens
	}
	return append(tokens, tok)
}

// ValidateKeyword aplica a §4.1.2 (com a emenda §10.3) a uma palavra-chave
// como veio do cliente e devolve as duas formas que o banco guarda: a
// exibível (como a pessoa digitou, com espaços colapsados e em NFC) e a
// normalizada (textnorm.Normalize), que é a que participa da correspondência
// e do índice único.
//
// Regras, nesta ordem: colapsa espaços; 2–40 runas na forma exibível;
// allowlist de caracteres (letra, número, espaço, & . - / '); 2–40 runas na
// forma normalizada; ao menos uma palavra útil depois de Tokenize — palavra
// só de vazias ("de", "ltda") ou só de letras soltas ("c & a") nunca casaria
// com nada, e recusar é melhor do que gravar.
//
// O erro embrulha ErrInvalidKeyword com a razão e NUNCA ecoa a palavra.
func ValidateKeyword(raw string) (keyword, norm string, err error) {
	// NFC antes de contar e conferir: "é" digitado como "e" + acento
	// combinante (teclados e sistemas que emitem NFD) vira a letra composta,
	// que é o que a allowlist e o rune count esperam. Sem isso, a mesma
	// palavra passaria ou não conforme o teclado de quem digitou.
	keyword = unorm.NFC.String(strings.Join(strings.Fields(raw), " "))

	n := utf8.RuneCountInString(keyword)
	if n < MinKeywordRunes {
		return "", "", fmt.Errorf("%w: shorter than %d runes", ErrInvalidKeyword, MinKeywordRunes)
	}
	if n > MaxKeywordRunes {
		return "", "", fmt.Errorf("%w: longer than %d runes", ErrInvalidKeyword, MaxKeywordRunes)
	}
	for _, r := range keyword {
		if !isKeywordRune(r) {
			return "", "", fmt.Errorf("%w: character outside the allowed set", ErrInvalidKeyword)
		}
	}

	norm = textnorm.Normalize(keyword)
	// A normalização só remove marcas e dobra caixa: a norm nunca é maior que
	// a exibível, mas pode ficar menor (acento solto some). Confere os dois.
	if nn := utf8.RuneCountInString(norm); nn < MinKeywordRunes || nn > MaxKeywordRunes {
		return "", "", fmt.Errorf("%w: normalized form outside %d-%d runes", ErrInvalidKeyword, MinKeywordRunes, MaxKeywordRunes)
	}
	if len(Tokenize(norm)) == 0 {
		return "", "", fmt.Errorf("%w: no useful word after tokenization", ErrInvalidKeyword)
	}
	return keyword, norm, nil
}
