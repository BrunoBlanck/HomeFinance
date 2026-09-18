// Package textnorm normaliza texto para comparação insensível a caixa e a
// acento, de forma idêntica nos quatro dialetos SQL suportados.
//
// Por que a normalização é feita em Go e gravada numa coluna, em vez de
// resolvida na consulta (armadilha P2 do PLANOS.md): não existe um jeito
// portátil de comparar "Alimentação" com "alimentacao" no banco. `LOWER()`
// não remove acento; a sensibilidade do `LIKE` muda de dialeto para dialeto
// (o PostgreSQL diferencia caixa, o MySQL depende da collation, o SQLite só
// dobra ASCII, o SQL Server segue a collation da coluna). Qualquer solução no
// SQL seria diferente em cada banco, que é justamente o que o requisito
// multi-SQL proíbe.
//
// Gravando a forma normalizada, a comparação vira igualdade de texto simples —
// igual nos quatro — e o índice funciona.
//
// Este pacote é FOLHA: não importa nada do projeto.
package textnorm

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Normalize devolve a forma canônica de comparação: sem espaço nas pontas,
// espaços internos colapsados em um só, sem diacrítico e em minúsculas.
//
// A remoção de acento passa por NFD (decomposição canônica), que separa a
// letra da marca — "ç" vira "c"+cedilha — e então descarta as marcas. É por
// isso que o pacote usa `golang.org/x/text/unicode/norm` em vez de uma tabela
// à mão: escrever o mapeamento manualmente cobriria o português e falharia no
// primeiro nome com "ł", "ø" ou grego.
//
// Limite conhecido e aceito: caracteres que NÃO se decompõem em letra + marca
// (o "ø" dinamarquês, o "ł" polonês) sobrevivem como estão. Para o uso do
// projeto — nome de conta e de categoria em português — isso é irrelevante, e
// o efeito é apenas dois nomes deixarem de colidir.
func Normalize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	decomposto := norm.NFD.String(s)

	var b strings.Builder
	b.Grow(len(decomposto))
	espacoPendente := false

	for _, r := range decomposto {
		// Mn = "mark, nonspacing": exatamente os acentos soltos que a NFD
		// separou da letra.
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsSpace(r) {
			// O espaço só é escrito quando vier um caractere depois, o que
			// colapsa as sequências e dispensa um TrimSpace no fim.
			espacoPendente = b.Len() > 0
			continue
		}
		if espacoPendente {
			b.WriteRune(' ')
			espacoPendente = false
		}
		b.WriteRune(unicode.ToLower(r))
	}

	// Recompõe para NFC: guardar em NFD deixaria a coluna com uma sequência
	// que "parece" igual mas tem bytes diferentes de qualquer outro texto do
	// sistema — e comparação de igualdade é por bytes.
	return norm.NFC.String(b.String())
}

// Equal informa se dois textos são o mesmo depois de normalizados. Existe para
// que a intenção fique legível no service ("já existe uma conta com este
// nome?") em vez de duas chamadas a Normalize comparadas com ==.
func Equal(a, b string) bool { return Normalize(a) == Normalize(b) }
