// Package sanitize normaliza a descrição de um lançamento importado.
//
// # Por que isto é contrato, e não formatação
//
// A descrição sanitizada entra na chave de deduplicação da importação. Mudar
// este pacote depois que houver dados importados **cega o dedup contra o
// passado**: o mesmo lançamento, reimportado, produz outra descrição, outra
// chave, e vira uma duplicata que ninguém pediu. Por isso a transformação é
// **determinística** (nada de mapa iterado, nada de aleatório, nada de "hoje")
// e **versionada** em Version. Alterar qualquer regra obriga a subir a versão e
// a decidir explicitamente o que fazer com o que já foi importado.
//
// # O que sai e o que fica
//
// Sai o que identifica TERCEIROS e não ajuda ninguém a se lembrar do gasto:
// CPF (mascarado ou não), CNPJ, agência, conta e código do banco. Fica o que
// responde "quem eu paguei": o nome da contraparte.
//
// # Trojan Source
//
// A limpeza remove os overrides de direção do Unicode (U+202A–U+202E,
// U+2066–U+2069). Eles reordenam a exibição sem mudar os bytes: uma descrição
// pode ser exibida como "PADARIA DA ESQUINA" e estar guardada como outra coisa
// inteiramente. Num app financeiro isso é uma fraude pronta — o usuário aprova
// o que lê, e o que existe é outro texto. Eles somem aqui, na borda.
//
// # O que este pacote NÃO faz: CSV injection
//
// Uma descrição que começa com `=`, `+`, `-` ou `@` é perigosa **quando
// exportada para uma planilha**, não quando guardada. A defesa correta é na
// EXPORTAÇÃO (entrega E6), porque neutralizar na entrada significaria alterar o
// dado do usuário — uma descrição que legitimamente começa com `-` deixaria de
// ser o que ele viu no extrato — para proteger um programa de terceiro que
// talvez nunca abra o arquivo. Guardamos fiel; a planilha que se proteja na
// hora de virar planilha.
//
// Este pacote é FOLHA: depende apenas de `textnorm`.
package sanitize

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// Version identifica a versão do sanitizador.
//
// ⚠️ Faz parte do contrato de deduplicação. Qualquer mudança de comportamento
// neste pacote — inclusive acrescentar um prefixo ao mapa canônico — exige
// subir esta versão e registrar a decisão sobre o histórico já importado.
const Version = "v1"

// MaxRunes é o teto da descrição guardada.
//
// Medido em RUNAS, não em bytes: "Transferência" tem 13 runas e 15 bytes, e
// cortar por byte partiria um caractere multibyte no meio, produzindo UTF-8
// inválido no banco.
const MaxRunes = 140

// separador é o que divide os campos dentro da descrição do banco.
const separador = " - "

// prefixosCanonicos traduz o começo verboso do banco para um rótulo curto e
// estável.
//
// As chaves estão na forma NORMALIZADA (textnorm: sem acento, minúscula,
// espaços colapsados) — é o que faz "Transferência enviada pelo Pix",
// "TRANSFERENCIA ENVIADA PELO PIX" e "Transferencia  enviada pelo Pix" caírem
// todas no mesmo rótulo, sem uma entrada por variação.
//
// Allowlist fechada de propósito: o que não estiver aqui passa intacto. Um
// "reescreva qualquer coisa que se pareça com Pix" acabaria reescrevendo o nome
// de um estabelecimento.
var prefixosCanonicos = map[string]string{
	"transferencia enviada pelo pix":             "Pix enviado",
	"transferencia recebida pelo pix":            "Pix recebido",
	"reembolso enviado pelo pix":                 "Pix reembolso enviado",
	"reembolso recebido pelo pix":                "Pix reembolso recebido",
	"estorno de transferencia enviada pelo pix":  "Pix enviado estornado",
	"estorno de transferencia recebida pelo pix": "Pix recebido estornado",
}

// Padrões de identificador de terceiros.
//
// As máscaras aceitas são `•` (U+2022, o que o Nubank usa) e `*`. Os escapes
// `\x{2022}` evitam colar o caractere cru no fonte.
var (
	// reCNPJ — 11.222.333/0001-44. Conferido ANTES do CPF porque é o padrão
	// mais específico.
	reCNPJ = regexp.MustCompile(`[0-9\x{2022}*]{2}\.[0-9\x{2022}*]{3}\.[0-9\x{2022}*]{3}/[0-9\x{2022}*]{4}-[0-9\x{2022}*]{2}`)

	// reCPF — 123.456.789-01 e a forma mascarada •••.996.897-••.
	reCPF = regexp.MustCompile(`[0-9\x{2022}*]{3}\.[0-9\x{2022}*]{3}\.[0-9\x{2022}*]{3}-[0-9\x{2022}*]{2}`)

	// reAgencia / reConta — "Agência: 1", "Agencia 1234", "Conta: 8703544-1".
	reAgencia = regexp.MustCompile(`(?i)ag[êe]ncia\s*:?\s*[0-9]{1,12}(?:-[0-9xX])?`)
	reConta   = regexp.MustCompile(`(?i)conta\s*:?\s*[0-9]{1,12}(?:-[0-9xX])?`)

	// reCodigoBanco — o COMPE entre parênteses: (0260), (0341).
	reCodigoBanco = regexp.MustCompile(`\(\s*[0-9]{3,4}\s*\)`)

	// reDocumentoSemMascara — CPF/CNPJ digitado sem pontuação.
	reDocumentoSemMascara = regexp.MustCompile(`\b[0-9]{11,14}\b`)

	// reEspacos colapsa espaços já normalizados para ' '.
	reEspacos = regexp.MustCompile(` {2,}`)
)

// identificadores é a ordem FIXA de remoção. Ordem fixa é o que torna o
// resultado reproduzível.
var identificadores = []*regexp.Regexp{
	reCNPJ, reCPF, reAgencia, reConta, reCodigoBanco, reDocumentoSemMascara,
}

// Description devolve a descrição pronta para guardar.
//
// O pipeline, nesta ordem exata:
//
//  1. limpeza de caracteres invisíveis e de controle (inclusive os overrides
//     de direção do Trojan Source);
//  2. quebra em segmentos por " - ";
//  3. tradução do segmento inicial pelo mapa canônico;
//  4. corte a partir do primeiro segmento que contenha um documento — dali
//     para a frente é só dado da instituição;
//  5. remoção dos identificadores que sobraram nos segmentos mantidos;
//  6. colapso de espaços;
//  7. truncagem em MaxRunes runas.
func Description(s string) string {
	limpo := limparInvisiveis(s)
	if limpo == "" {
		return ""
	}

	segmentos := strings.Split(limpo, separador)

	// O primeiro segmento é o que o banco usa como "tipo de operação".
	if canonico, ok := prefixosCanonicos[textnorm.Normalize(segmentos[0])]; ok {
		segmentos[0] = canonico
	}

	// Corte no primeiro segmento com documento.
	//
	// Nunca no índice 0: uma descrição que é SÓ um documento ("CPF
	// 123.456.789-01") viraria string vazia, e aí a limpeza teria apagado o
	// lançamento em vez de anonimizá-lo. No índice 0 o documento é removido
	// pela etapa seguinte, como qualquer outro identificador.
	for i := 1; i < len(segmentos); i++ {
		if contemDocumento(segmentos[i]) {
			segmentos = segmentos[:i]
			break
		}
	}

	mantidos := make([]string, 0, len(segmentos))
	for _, seg := range segmentos {
		seg = removerIdentificadores(seg)
		seg = strings.TrimSpace(reEspacos.ReplaceAllString(seg, " "))
		seg = aparar(seg)
		if seg == "" {
			continue
		}
		mantidos = append(mantidos, seg)
	}

	resultado := strings.Join(mantidos, separador)
	resultado = strings.TrimSpace(reEspacos.ReplaceAllString(resultado, " "))
	return truncarRunas(resultado, MaxRunes)
}

// limparInvisiveis tira tudo que não deveria ser visível numa descrição.
//
// Tabulação, quebra de linha e retorno viram ESPAÇO (apagá-los grudaria as
// palavras vizinhas); os demais controles, o BOM, os zero-width, os overrides
// de direção e todo o resto da categoria Cf somem; bytes UTF-8 inválidos são
// descartados.
func limparInvisiveis(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			// Byte UTF-8 inválido (o `range` o entrega como RuneError) ou um
			// U+FFFD que já veio no texto: nos dois casos não é informação.
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(' ')
		case r < 0x20 || r == 0x7F:
			// Controles C0 e DEL: somem.
		case r >= 0x80 && r <= 0x9F:
			// Controles C1 — chegam aqui quando o arquivo era Windows-1252 e
			// trazia bytes da faixa não atribuída.
		case unicode.Is(unicode.Cf, r):
			// Cf ("format"): BOM (U+FEFF), zero-width (U+200B–U+200D),
			// marcas de direção (U+200E/U+200F) e os OVERRIDES de direção
			// (U+202A–U+202E, U+2066–U+2069) — o truque do Trojan Source.
		case unicode.Is(unicode.Co, r):
			// Área de uso privado: não tem significado acordado, e é onde
			// moram os glifos de fonte proprietária.
		case unicode.IsSpace(r):
			// Qualquer espaço (inclusive NBSP) vira o espaço simples, para o
			// colapso seguinte enxergar todos do mesmo jeito.
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(reEspacos.ReplaceAllString(b.String(), " "))
}

// contemDocumento informa se o segmento carrega um CPF ou CNPJ — mascarado ou
// não. É o marcador de "daqui para a frente é dado da instituição".
func contemDocumento(seg string) bool {
	return reCNPJ.MatchString(seg) || reCPF.MatchString(seg) || reDocumentoSemMascara.MatchString(seg)
}

// removerIdentificadores aplica os padrões na ordem fixa.
func removerIdentificadores(seg string) string {
	for _, re := range identificadores {
		seg = re.ReplaceAllString(seg, " ")
	}
	return seg
}

// aparar tira pontuação de ligação que sobrou nas pontas depois das remoções.
//
// O ponto final NÃO é aparado de propósito: "ENERGIA EXEMPLO S.A." perderia o
// ponto da sigla e viraria outro texto — e outro texto é outra chave de dedup.
func aparar(seg string) string {
	return strings.Trim(seg, " -–—,;/|")
}

// truncarRunas corta em `max` runas e limpa a ponta.
func truncarRunas(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	contador := 0
	for i := range s {
		if contador == max {
			return aparar(strings.TrimSpace(s[:i]))
		}
		contador++
	}
	return s
}
