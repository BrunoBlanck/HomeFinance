// Package csvtext transforma os bytes de um CSV de banco em linhas, números e
// datas — com as decisões de formato DECLARADAS por quem chama, nunca
// adivinhadas linha a linha.
//
// Por que "declarado, nunca adivinhado" é regra e não estilo: um extrato
// brasileiro tem `1.234,56` e um extrato em formato americano tem `1,234.56`.
// Um palpite por linha acerta quase sempre e erra exatamente onde dói — em
// `1.234` (mil e duzentos e trinta e quatro reais ou um real e vinte e três?).
// Errar a escala do dinheiro por três ordens de grandeza é pior do que recusar
// o arquivo, então o adaptador do banco declara o formato que aquele layout usa
// e o parser obedece. O zero-value de NumberFormat e DateFormat é INVÁLIDO de
// propósito: esquecer de declarar dá erro, não dá palpite.
//
// As três armadilhas de arquivo real que este pacote resolve:
//
//  1. **BOM.** Um CSV reaberto e salvo no Excel volta com `EF BB BF` na frente.
//     O cabeçalho passa a ser U+FEFF seguido de "Data" e não casa com nada.
//  2. **Codificação.** Nem todo banco exporta UTF-8. O fallback é
//     **Windows-1252**, não Latin-1: a faixa 0x80–0x9F, vazia no Latin-1,
//     carrega no CP1252 o travessão, as aspas curvas e o símbolo do euro — que
//     é onde eles caem nas descrições brasileiras.
//  3. **Separador.** `;` é a regra no CSV brasileiro (o Excel em pt-BR usa a
//     vírgula como decimal e não pode usá-la como separador), não a exceção.
//
// Dinheiro NUNCA passa por float (ADR-003): `strconv.ParseFloat` é proibido
// neste caminho, e há teste que falha se alguém o reintroduzir.
//
// Este pacote é FOLHA no domínio: depende apenas de `civil` e `textnorm`.
package csvtext

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"

	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// Limites do parser. Todos RECUSAM; nenhum trunca em silêncio.
const (
	// MaxRows é o número máximo de linhas de DADOS (fora o cabeçalho).
	MaxRows = 10_000

	// MaxColumns limita a largura do cabeçalho.
	MaxColumns = 16

	// MaxLineRunes limita o comprimento de cada linha FÍSICA, em runas.
	// Medido em runas porque "Transferência" ocupa 13 runas e 15 bytes, e o
	// limite existe para conter o trabalho do parser, não a acentuação.
	MaxLineRunes = 1_000

	// MaxAmountCents é R$ 999.999.999,99 — o mesmo teto do domínio de contas
	// (docs/SEGURANCA.md §3: "rejeitar valores absurdos"). Está duplicado aqui,
	// e não importado, porque este pacote é folha de propósito.
	MaxAmountCents int64 = 99_999_999_999

	// MaxInputBytes limita os bytes que entram no parser.
	//
	// O importador já recebe conteúdo limitado a 8 MiB pelo `archive`, mas o
	// teto é repetido aqui porque este pacote é utilizável sozinho: um limite
	// que só existe no chamador deixa de existir no primeiro chamador novo.
	MaxInputBytes = 8 << 20
)

// bomUTF8 é a marca de ordem de bytes do UTF-8.
var bomUTF8 = []byte{0xEF, 0xBB, 0xBF}

// Erros do pacote.
var (
	// ErrEmpty — arquivo sem conteúdo.
	ErrEmpty = errors.New("arquivo vazio")

	// ErrBinaryContent — há byte NUL no conteúdo; CSV de texto não tem.
	ErrBinaryContent = errors.New("o arquivo não parece ser um CSV de texto")

	// ErrUnsupportedEncoding — codificação que não sabemos ler (UTF-16).
	ErrUnsupportedEncoding = errors.New("codificação de texto não suportada")

	// ErrNoSeparator — não foi possível identificar o separador do cabeçalho.
	ErrNoSeparator = errors.New("não identifiquei o separador de colunas")

	// ErrTooManyRows / ErrTooManyColumns / ErrLineTooLong / ErrTooLarge —
	// tetos do parser.
	ErrTooManyRows    = errors.New("arquivo com linhas demais")
	ErrTooManyColumns = errors.New("arquivo com colunas demais")
	ErrLineTooLong    = errors.New("arquivo com linha longa demais")
	ErrTooLarge       = errors.New("arquivo grande demais")

	// ErrMalformed — o CSV não fecha (aspas soltas, largura irregular).
	ErrMalformed = errors.New("arquivo CSV malformado")

	// ErrDuplicateHeader — duas colunas com o mesmo nome; o mapeamento por
	// nome ficaria ambíguo, e ambiguidade em importação de dinheiro é erro.
	ErrDuplicateHeader = errors.New("o cabeçalho tem colunas repetidas")

	// ErrInvalidNumber — texto que não é um valor monetário válido.
	ErrInvalidNumber = errors.New("valor monetário inválido")

	// ErrAmountOutOfRange — valor fora da faixa aceitável.
	ErrAmountOutOfRange = errors.New("valor monetário fora da faixa aceitável")

	// ErrInvalidDate — texto que não é uma data válida no formato declarado.
	ErrInvalidDate = errors.New("data inválida")
)

// Encoding identifica como os bytes foram interpretados. É devolvido ao
// chamador para poder aparecer no relatório da importação — se o usuário vir
// acento estranho, a resposta a "por quê" já está registrada.
type Encoding string

const (
	// UTF8 — os bytes já eram UTF-8 válido.
	UTF8 Encoding = "utf-8"

	// Windows1252 — os bytes não eram UTF-8 e foram lidos como CP1252.
	Windows1252 Encoding = "windows-1252"
)

// Separadores aceitos, em ordem de preferência para desempate.
//
// A ordem importa e é FIXA: o sniff tem de ser determinístico, porque o mesmo
// arquivo importado duas vezes precisa produzir exatamente as mesmas linhas
// (é o que a deduplicação assume).
var separadores = []rune{';', ',', '\t'}

// Table é o CSV já decodificado e conferido.
type Table struct {
	// Encoding é como os bytes foram interpretados.
	Encoding Encoding

	// Separator é o separador efetivamente usado.
	Separator rune

	// Header são os nomes das colunas, como vieram (sem trim: fidelidade).
	Header []string

	// Rows são as linhas de dados, todas com len(Header) campos.
	Rows [][]string
}

// IndexOf devolve o índice da coluna cujo nome bate com `name`, comparando de
// forma insensível a caixa, a acento e a espaço (textnorm) — porque o mesmo
// banco escreve "Descrição" num layout e "descricao" em outro. Devolve -1 se
// não existir.
func (t *Table) IndexOf(name string) int {
	alvo := textnorm.Normalize(name)
	if alvo == "" {
		return -1
	}
	for i, h := range t.Header {
		if textnorm.Normalize(h) == alvo {
			return i
		}
	}
	return -1
}

// Decode converte os bytes crus em texto UTF-8, removendo o BOM.
//
// A ordem é deliberada: o BOM sai ANTES do teste de validade UTF-8, senão um
// arquivo UTF-8 com BOM continuaria válido e carregaria o U+FEFF para dentro
// do primeiro cabeçalho — o bug silencioso que só aparece quando o mapeamento
// de coluna não acha "Data".
func Decode(raw []byte) (string, Encoding, error) {
	if len(raw) == 0 {
		return "", "", ErrEmpty
	}
	if len(raw) > MaxInputBytes {
		return "", "", fmt.Errorf("%w: passa de %d bytes", ErrTooLarge, MaxInputBytes)
	}
	// UTF-16 tem BOM próprio e é o que o Excel produz em "Texto Unicode".
	// Recusar explicitamente evita que ele seja lido como CP1252 e vire um
	// arquivo cheio de NUL "quase válido".
	if bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) || bytes.HasPrefix(raw, []byte{0xFE, 0xFF}) {
		return "", "", fmt.Errorf("%w: UTF-16", ErrUnsupportedEncoding)
	}
	raw = bytes.TrimPrefix(raw, bomUTF8)
	if len(raw) == 0 {
		return "", "", ErrEmpty
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return "", "", ErrBinaryContent
	}

	if utf8.Valid(raw) {
		return limparBOMInterno(string(raw)), UTF8, nil
	}

	// Windows-1252 é single-byte e mapeia todos os 256 valores, então a
	// decodificação não falha — o `err` fica aqui por contrato, não por
	// expectativa.
	decodificado, err := charmap.Windows1252.NewDecoder().Bytes(raw)
	if err != nil {
		return "", "", fmt.Errorf("%w: %v", ErrUnsupportedEncoding, err)
	}
	return limparBOMInterno(string(decodificado)), Windows1252, nil
}

// limparBOMInterno tira qualquer U+FEFF restante no meio do texto. Excel e
// alguns exportadores deixam um por concatenação de arquivos.
func limparBOMInterno(s string) string {
	if !strings.ContainsRune(s, '\uFEFF') {
		return s
	}
	return strings.ReplaceAll(s, "\uFEFF", "")
}

// SniffSeparator descobre o separador contando ocorrências na primeira linha,
// FORA de aspas.
//
// Contar fora de aspas é o que impede que `"Silva, Maria"` numa descrição
// eleja a vírgula num arquivo que na verdade usa `;`.
func SniffSeparator(text string) (rune, error) {
	cabecalho := primeiraLinha(text)
	if cabecalho == "" {
		return 0, ErrEmpty
	}

	melhor := rune(0)
	melhorContagem := 0
	for _, sep := range separadores {
		n := contarForaDeAspas(cabecalho, sep)
		if n > melhorContagem {
			melhor, melhorContagem = sep, n
		}
	}
	if melhorContagem == 0 {
		return 0, ErrNoSeparator
	}
	return melhor, nil
}

// primeiraLinha devolve o cabeçalho, sem o `\r` do CRLF.
func primeiraLinha(text string) string {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSuffix(text, "\r")
}

// contarForaDeAspas conta `sep` ignorando o que está entre aspas duplas.
func contarForaDeAspas(linha string, sep rune) int {
	dentro := false
	n := 0
	for _, r := range linha {
		switch {
		case r == '"':
			dentro = !dentro
		case r == sep && !dentro:
			n++
		}
	}
	return n
}

// Parse decodifica, identifica o separador e lê a tabela inteira.
func Parse(raw []byte) (*Table, error) {
	text, enc, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	sep, err := SniffSeparator(text)
	if err != nil {
		return nil, err
	}
	return parseText(text, enc, sep)
}

// ParseWithSeparator é Parse com o separador imposto pelo chamador, para quando
// o layout do banco é conhecido e não se quer depender do sniff.
func ParseWithSeparator(raw []byte, sep rune) (*Table, error) {
	if sep != ';' && sep != ',' && sep != '\t' {
		return nil, fmt.Errorf("%w: separador %q não é aceito", ErrNoSeparator, sep)
	}
	text, enc, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	return parseText(text, enc, sep)
}

func parseText(text string, enc Encoding, sep rune) (*Table, error) {
	if err := checkLineLengths(text); err != nil {
		return nil, err
	}

	r := csv.NewReader(strings.NewReader(text))
	r.Comma = sep
	// LazyQuotes=false é obrigatório: no modo tolerante, uma aspa solta faz o
	// leitor engolir o resto do arquivo como um campo só e o erro aparece
	// depois, como "coluna faltando" — bem longe da causa.
	r.LazyQuotes = false
	// Sem trim: o dado é guardado como veio, e quem interpreta cada campo
	// (ParseCents, ParseDate, sanitize) faz o seu próprio saneamento.
	r.TrimLeadingSpace = false
	r.ReuseRecord = false
	// Sem caractere de comentário: `#` é conteúdo legítimo de descrição.
	r.Comment = 0

	header, err := r.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrEmpty
		}
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if len(header) > MaxColumns {
		return nil, fmt.Errorf("%w: %d colunas (máximo %d)", ErrTooManyColumns, len(header), MaxColumns)
	}
	if err := checkDuplicateHeader(header); err != nil {
		return nil, err
	}

	// A largura passa a ser a do cabeçalho, e o leitor recusa qualquer linha
	// diferente. O `csv.Reader` já faria isso sozinho a partir do primeiro
	// registro; fixar explicitamente deixa a regra visível.
	r.FieldsPerRecord = len(header)

	rows := make([][]string, 0, 64)
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
		}
		if len(rows) >= MaxRows {
			return nil, fmt.Errorf("%w: máximo de %d linhas", ErrTooManyRows, MaxRows)
		}
		rows = append(rows, rec)
	}

	return &Table{Encoding: enc, Separator: sep, Header: header, Rows: rows}, nil
}

// checkLineLengths aplica o teto por linha FÍSICA.
//
// É medido aqui, e não no csv.Reader, porque o `encoding/csv` não tem teto de
// linha: um arquivo de uma linha só, com 8 MiB, seria lido inteiro na memória
// como um campo antes de qualquer validação nossa.
func checkLineLengths(text string) error {
	runas := 0
	linha := 1
	for _, r := range text {
		if r == '\n' {
			runas, linha = 0, linha+1
			continue
		}
		if r == '\r' {
			continue
		}
		runas++
		if runas > MaxLineRunes {
			return fmt.Errorf("%w: linha %d passa de %d caracteres", ErrLineTooLong, linha, MaxLineRunes)
		}
	}
	return nil
}

// checkDuplicateHeader recusa nomes de coluna repetidos (comparando pela forma
// normalizada). Colunas sem nome são ignoradas: um separador sobrando no fim da
// linha produz uma coluna vazia, e isso é comum e inofensivo.
func checkDuplicateHeader(header []string) error {
	vistos := make(map[string]struct{}, len(header))
	for _, h := range header {
		n := textnorm.Normalize(h)
		if n == "" {
			continue
		}
		if _, existe := vistos[n]; existe {
			return fmt.Errorf("%w: %q", ErrDuplicateHeader, h)
		}
		vistos[n] = struct{}{}
	}
	return nil
}
