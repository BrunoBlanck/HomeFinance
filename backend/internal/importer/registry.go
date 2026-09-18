package importer

import (
	"context"
	"encoding/csv"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// MaxPreambleLines é quantas linhas o núcleo tolera ANTES da linha de
// cabeçalho.
//
// Emissor de extrato costuma pôr titular, período, saldo anterior e totais
// antes da tabela de verdade. O Nubank não põe; o extrato do C6 põe 8 linhas
// (spec 0004 §7.3, item 6), e o mecanismo fica no núcleo justamente para que o
// parser novo não precise de nada além da assinatura do cabeçalho.
//
// O teto existe porque cada linha candidata custa um sniff de separador e um
// split: sem ele, um arquivo de 10.000 linhas sem cabeçalho nenhum viraria
// 10.000 tentativas de detecção.
const MaxPreambleLines = 20

// Signature é a assinatura de cabeçalho DECLARADA por um parser.
//
// A regra de casamento, e o porquê de cada metade:
//
//   - as colunas esperadas têm de estar presentes, NESTA ORDEM, no começo do
//     cabeçalho. Coluna faltando quebra a importação, que é o certo — sem a
//     coluna de valor não há o que importar;
//   - colunas EXTRAS ao final são toleradas, com confiança fraca. O banco que
//     acrescenta uma coluna no fim do arquivo não pode derrubar a importação de
//     ninguém;
//   - a comparação é sobre a forma normalizada (textnorm: minúscula, sem
//     acento, sem espaço nas pontas) e o BOM já saiu no csvtext.Decode. Sem
//     isso, o mesmo arquivo reaberto no Excel deixa de casar com qualquer
//     assinatura, porque o cabeçalho passa a ser U+FEFF seguido de "Data".
type Signature struct {
	// Columns são os nomes esperados, JÁ normalizados por NewSignature.
	Columns []string

	// Separator é o separador esperado. Zero significa "qualquer um dos que o
	// csvtext aceita" — útil para um banco que exporta `,` numa região e `;`
	// em outra.
	Separator rune
}

// NewSignature monta a assinatura normalizando os nomes das colunas.
//
// Os nomes entram como o banco os escreve ("Descrição"), e a normalização
// acontece aqui, uma vez: obrigar cada parser a escrever "descricao" à mão
// convidaria ao erro de digitar a forma normalizada errada — e um parser que
// não casa com nada não falha, ele simplesmente some da detecção.
func NewSignature(sep rune, columns ...string) Signature {
	normalizadas := make([]string, 0, len(columns))
	for _, c := range columns {
		normalizadas = append(normalizadas, textnorm.Normalize(c))
	}
	return Signature{Columns: normalizadas, Separator: sep}
}

// Match compara o cabeçalho lido com a assinatura.
func (s Signature) Match(header []string, sep rune) Confidence {
	if len(s.Columns) == 0 || len(header) < len(s.Columns) {
		return ConfidenceNone
	}
	if s.Separator != 0 && s.Separator != sep {
		return ConfidenceNone
	}
	for i, esperada := range s.Columns {
		if textnorm.Normalize(header[i]) != esperada {
			return ConfidenceNone
		}
	}
	if len(header) == len(s.Columns) {
		return ConfidenceExact
	}
	return ConfidenceWeak
}

// Document é o CSV pronto para um parser ler.
type Document struct {
	Table *csvtext.Table

	// HeaderLine é a linha FÍSICA do cabeçalho no arquivo (1-based). Com
	// preâmbulo ela é maior que 1, e é ela que faz "linha 37" na tela de
	// revisão apontar para a linha 37 do arquivo que a pessoa abre no Excel.
	HeaderLine int
}

// Registry escolhe o parser de um documento.
//
// É IMUTÁVEL depois de construído e NÃO existe instância global: quem monta o
// serviço lista os parsers explicitamente, por construtor (injeção manual, como
// o resto do projeto). Um registro global preenchido por init() faria a
// resposta de "quais formatos o sistema aceita?" depender da ordem de import
// dos pacotes — e é essa lista que decide se a fatura entra pelo parser do
// extrato, com o sinal invertido do começo ao fim.
//
// Hoje a lista tem os quatro leiautes (extrato e fatura do Nubank e do C6);
// um leiaute novo é um parser mais uma fixture no construtor, sem tocar no
// núcleo (spec 0004 §7).
type Registry struct {
	parsers []Parser
	byID    map[string]Parser
}

// NewRegistry monta o registro e valida os parsers.
//
// A validação é na construção, e não no primeiro uso, porque todo defeito aqui
// é de programação: id repetido, id vazio, instituição fora da allowlist. Falhar
// na subida do processo é a única hora em que isso não custa nada.
func NewRegistry(parsers ...Parser) (*Registry, error) {
	r := &Registry{
		parsers: make([]Parser, 0, len(parsers)),
		byID:    make(map[string]Parser, len(parsers)),
	}
	for _, p := range parsers {
		if p == nil {
			return nil, fmt.Errorf("%w: parser nulo no registro", ErrParserMisconfigured)
		}
		id := p.ID()
		if strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("%w: parser sem id", ErrParserMisconfigured)
		}
		if _, existe := r.byID[id]; existe {
			return nil, fmt.Errorf("%w: id de parser repetido (%q)", ErrParserMisconfigured, id)
		}
		if !p.Institution().Valid() {
			return nil, fmt.Errorf("%w: instituição inválida em %q", ErrParserMisconfigured, id)
		}
		if !p.DocKind().Valid() {
			return nil, fmt.Errorf("%w: tipo de documento inválido em %q", ErrParserMisconfigured, id)
		}
		r.parsers = append(r.parsers, p)
		r.byID[id] = p
	}
	return r, nil
}

// FormatIDs devolve os ids registrados, em ordem determinística. É a allowlist
// que o cliente pode usar no campo `format`.
func (r *Registry) FormatIDs() []string {
	ids := make([]string, 0, len(r.parsers))
	for _, p := range r.parsers {
		ids = append(ids, p.ID())
	}
	slices.Sort(ids)
	return ids
}

// Candidates devolve os ids dos parsers que reconheceram o cabeçalho, no MAIOR
// nível de confiança presente, em ordem determinística.
//
// "Maior nível presente" é o único desempate automático que existe, e ele é
// explicável numa frase: um parser que case o cabeçalho EXATAMENTE ganha de um
// que só case tolerando colunas extras. Fora disso, nada de automático — dois
// candidatos no mesmo nível viram ambiguidade, e quem desempata é a pessoa.
func (r *Registry) Candidates(header []string, sep rune) []string {
	melhor := ConfidenceNone
	var ids []string
	for _, p := range r.parsers {
		c := p.Detect(header, sep)
		switch {
		case c == ConfidenceNone:
			continue
		case c > melhor:
			melhor = c
			ids = []string{p.ID()}
		case c == melhor:
			ids = append(ids, p.ID())
		}
	}
	slices.Sort(ids)
	return ids
}

// Select escolhe o parser do cabeçalho.
//
// formatID vazio deixa a detecção decidir. formatID preenchido é o desempate
// EXPLÍCITO da pessoa, e ainda assim ele é conferido duas vezes: o id tem de
// estar no registro (a allowlist) e o parser tem de reconhecer o cabeçalho. A
// segunda conferência é a que importa — sem ela, mandar
// `format=nubank.checking.v1` num arquivo de fatura leria a fatura inteira pela
// convenção de sinal do extrato, invertendo todos os lançamentos, e nada
// falharia.
func (r *Registry) Select(header []string, sep rune, formatID string) (Parser, error) {
	candidatos := r.Candidates(header, sep)

	if formatID != "" {
		p, ok := r.byID[formatID]
		if !ok {
			return nil, ErrFormatNotAllowed
		}
		if !slices.Contains(candidatos, formatID) {
			return nil, fmt.Errorf("%w: %q não reconhece este cabeçalho", ErrFormatUnknown, formatID)
		}
		return p, nil
	}

	switch len(candidatos) {
	case 0:
		return nil, ErrFormatUnknown
	case 1:
		return r.byID[candidatos[0]], nil
	default:
		return nil, &AmbiguousFormatError{Candidates: candidatos}
	}
}

// OpenDocument decodifica os bytes e localiza a linha de cabeçalho, pulando o
// preâmbulo que o emissor tenha posto antes da tabela.
//
// A linha de cabeçalho é a PRIMEIRA que algum parser registrado reconheça. Não
// é "a primeira linha com muitos separadores" nem "a primeira depois de N
// linhas": um chute desses acerta num arquivo e escolhe a linha de totais no
// outro.
func (r *Registry) OpenDocument(raw []byte) (*Document, error) {
	texto, _, err := csvtext.Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("lendo o arquivo: %w", err)
	}

	offset := 0
	for linha := 1; linha <= MaxPreambleLines+1; linha++ {
		fim := strings.IndexByte(texto[offset:], '\n')
		var atual string
		if fim < 0 {
			atual = texto[offset:]
		} else {
			atual = texto[offset : offset+fim]
		}
		atual = strings.TrimSuffix(atual, "\r")

		if sep, header, ok := r.headerOf(atual); ok {
			tabela, err := csvtext.ParseWithSeparator([]byte(texto[offset:]), sep)
			if err != nil {
				return nil, fmt.Errorf("lendo a tabela: %w", err)
			}
			// A tabela foi relida do offset, então o cabeçalho dela é o mesmo
			// que acabou de casar. A conferência é barata e fecha a porta para
			// um descompasso entre o que detectamos e o que vamos ler.
			if len(tabela.Header) != len(header) {
				return nil, fmt.Errorf("%w: cabeçalho instável entre a detecção e a leitura", ErrFormatUnknown)
			}
			return &Document{Table: tabela, HeaderLine: linha}, nil
		}

		if fim < 0 {
			break
		}
		offset += fim + 1
	}

	return nil, ErrFormatUnknown
}

// headerOf tenta ler UMA linha física como cabeçalho reconhecido.
func (r *Registry) headerOf(linha string) (rune, []string, bool) {
	if strings.TrimSpace(linha) == "" {
		return 0, nil, false
	}
	// O teto de comprimento é conferido ANTES do sniff: um arquivo de 8 MiB
	// numa única linha faria o sniff e o split varrerem tudo antes de o
	// csvtext ter chance de recusar por linha longa demais.
	if utf8.RuneCountInString(linha) > csvtext.MaxLineRunes {
		return 0, nil, false
	}

	sep, err := csvtext.SniffSeparator(linha)
	if err != nil {
		return 0, nil, false
	}
	header, err := splitHeaderLine(linha, sep)
	if err != nil {
		return 0, nil, false
	}
	if len(header) > csvtext.MaxColumns {
		return 0, nil, false
	}
	if len(r.Candidates(header, sep)) == 0 {
		return 0, nil, false
	}
	return sep, header, true
}

// splitHeaderLine quebra UMA linha em campos, com as mesmas regras do
// csvtext.Parse (aspas estritas, sem trim, sem comentário).
//
// É uma linha só de propósito: parsear o arquivo inteiro a cada candidata de
// cabeçalho custaria MaxPreambleLines leituras completas de um arquivo de até
// 8 MiB.
func splitHeaderLine(linha string, sep rune) ([]string, error) {
	rd := csv.NewReader(strings.NewReader(linha))
	rd.Comma = sep
	rd.LazyQuotes = false
	rd.TrimLeadingSpace = false
	rd.FieldsPerRecord = -1
	rd.ReuseRecord = false
	rd.Comment = 0

	rec, err := rd.Read()
	if err != nil {
		return nil, err
	}
	return rec, nil
}

// Parse é o caminho completo: decodifica, pula preâmbulo, escolhe o parser e lê.
//
// É o que o serviço de importação chama. formatID vazio deixa a detecção
// decidir; preenchido, é o desempate explícito da pessoa (ver Select).
func (r *Registry) Parse(ctx context.Context, raw []byte, formatID string, limits Limits) (ParseResult, error) {
	doc, err := r.OpenDocument(raw)
	if err != nil {
		return ParseResult{}, err
	}

	p, err := r.Select(doc.Table.Header, doc.Table.Separator, formatID)
	if err != nil {
		return ParseResult{}, err
	}

	res, err := p.Parse(ctx, doc.Table, limits)
	if err != nil {
		return ParseResult{}, fmt.Errorf("lendo com %s: %w", p.ID(), err)
	}

	// O parser conta as linhas a partir do cabeçalho que recebeu; só aqui se
	// sabe quantas linhas de preâmbulo ficaram para trás.
	res.shiftLines(doc.HeaderLine - 1)
	return res, nil
}
