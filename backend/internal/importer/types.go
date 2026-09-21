// Package importer transforma o arquivo de um banco — extrato de conta ou
// fatura de cartão — em linhas prontas para a pessoa revisar e confirmar.
//
// # O desenho, em uma frase
//
// Um Parser por par (instituição × tipo de documento), escolhido por DETECÇÃO
// de cabeçalho num Registry imutável, e nunca pela instituição que o usuário
// marcou na conta (ADR-024a).
//
// # As três regras que este pacote existe para não deixar ninguém quebrar
//
//  1. **A convenção de sinal e o formato numérico são DECLARADOS pelo parser,
//     nunca deduzidos do arquivo** (ADR-024b). Os dois documentos do Nubank
//     usam convenções OPOSTAS — no extrato o negativo é saída, na fatura o
//     POSITIVO é saída. Um parser que "perceba" o sinal olhando o conteúdo
//     acerta hoje e, no dia em que o banco mudar o arquivo, inverte uma fatura
//     inteira sem que nada falhe. Valor que não obedeça à convenção declarada
//     é linha REJEITADA, nunca valor consertado.
//
//  2. **Nunca "o primeiro parser que casou".** Zero candidatos é
//     ErrFormatUnknown; dois ou mais é AmbiguousFormatError com os ids, e o
//     cliente desempata mandando o formato. Escolher o primeiro é exatamente
//     como a fatura entraria um dia pelo parser do extrato.
//
//  3. **Erro é por LINHA, não por arquivo** — até Limits.MaxRejectedPercent.
//     Acima disso o arquivo inteiro é recusado, porque 20% de lixo quer dizer
//     parser errado, e não dado ruim.
//
// # Fronteiras
//
// Este pacote não conhece HTTP, não conhece GORM e não escreve em disco. A
// leitura do contêiner está em importer/archive, a do texto em
// importer/csvtext, a limpeza da descrição em importer/sanitize e a
// classificação de duplicatas em importer/dedup.
package importer

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
	"github.com/brunorblanck/homefinance/backend/internal/importer/sanitize"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Institution identifica o emissor do documento.
//
// Conjunto FECHADO: instituição nova entra com um parser novo e um teste-ouro,
// nunca com uma string nova vinda do cliente.
type Institution string

const (
	// InstitutionNubank — os dois documentos prontos nesta entrega.
	InstitutionNubank Institution = "nubank"

	// InstitutionC6 — os dois documentos (extrato de conta e fatura de cartão)
	// com parser registrado (pacote importer/c6, spec 0004 §7.3). O extrato usa
	// o modelo de DUAS COLUNAS (Entrada(R$)/Saída(R$)) e traz preâmbulo; a
	// fatura usa valor com sinal (positivo é saída), como a do Nubank.
	InstitutionC6 Institution = "c6"

	// InstitutionInter — por ora SÓ o extrato de conta (pacote importer/inter,
	// spec 0004 §7.4): valor com sinal em formato brasileiro (`-9.950,00`),
	// negativo é saída, preâmbulo de 5 linhas. A fatura do cartão entra quando
	// houver amostra — sem amostra não se declara convenção de sinal.
	InstitutionInter Institution = "inter"
)

// Valid informa se a instituição está na allowlist.
func (i Institution) Valid() bool {
	switch i {
	case InstitutionNubank, InstitutionC6, InstitutionInter:
		return true
	default:
		return false
	}
}

// DocKind é o tipo de documento. Também é conjunto fechado, e é ele que decide
// a trava mais forte da importação: fatura só entra em conta credit_card,
// extrato só entra fora dela (spec 0004 §3.3).
type DocKind string

const (
	DocKindCheckingStatement DocKind = "checking_statement"
	DocKindCardStatement     DocKind = "card_statement"
)

// Valid informa se o tipo de documento está na allowlist.
func (d DocKind) Valid() bool {
	switch d {
	case DocKindCheckingStatement, DocKindCardStatement:
		return true
	default:
		return false
	}
}

// SignConvention diz o que o sinal do número significa NAQUELE documento.
//
// O zero-value é INVÁLIDO de propósito, como em csvtext.NumberFormat: esquecer
// de declarar tem de dar erro, e não virar um palpite. É a armadilha nº 1 desta
// entrega — as duas convenções do Nubank são opostas.
type SignConvention uint8

const (
	_ SignConvention = iota

	// SignNegativeIsOutflow — extrato de conta: `-20.00` é dinheiro saindo.
	SignNegativeIsOutflow

	// SignPositiveIsOutflow — fatura de cartão: `33,70` é uma COMPRA (saída),
	// e `- 2.859,82` é o pagamento da fatura (entrada). Sim, ao contrário do
	// extrato do mesmo banco.
	SignPositiveIsOutflow
)

func (c SignConvention) String() string {
	switch c {
	case SignNegativeIsOutflow:
		return "negativo é saída"
	case SignPositiveIsOutflow:
		return "positivo é saída"
	default:
		return "não declarada"
	}
}

// Confidence é o quanto um parser reconheceu o cabeçalho.
//
// O zero-value é ConfidenceNone, e isso é deliberado: um parser que esqueça de
// responder simplesmente não vira candidato, em vez de virar candidato fraco.
type Confidence uint8

const (
	// ConfidenceNone — não é este formato.
	ConfidenceNone Confidence = iota

	// ConfidenceWeak — as colunas esperadas batem, na ordem, mas o arquivo traz
	// colunas EXTRAS ao final. É o caso "o banco acrescentou uma coluna":
	// continua importando, e perde só para um parser que case exatamente.
	ConfidenceWeak

	// ConfidenceExact — o cabeçalho é exatamente o esperado.
	ConfidenceExact
)

// Suggestion é o palpite do parser sobre o que a linha é.
//
// Ele SUGERE; quem decide é a pessoa na revisão (spec 0004 §7.2). Nenhuma
// sugestão altera o kind nem o valor — os dois vêm da convenção de sinal
// declarada, e só dela.
type Suggestion string

const (
	// SuggestionNone — nada a sugerir.
	SuggestionNone Suggestion = ""

	// SuggestionCardPayment — pagamento da fatura do cartão, visto do extrato
	// ("Pagamento de fatura") ou da própria fatura ("Pagamento recebido").
	// Barrado por default, liberável como transferência (spec 0004 §4.6).
	//
	// O texto é o mesmo de dedup.StatusCardPayment de propósito: é o mesmo
	// fato, e o teste TestSuggestionCasaComTaxonomiaDoDedup trava os dois
	// juntos.
	SuggestionCardPayment Suggestion = "pagamento_de_fatura"

	// SuggestionCardInflow — entrada na fatura que NÃO é pagamento ("Ajuste a
	// crédito"). Entra como receita (decisão D4 da spec 0004 §3.4) e a revisão
	// a exibe com o rótulo "crédito na fatura", porque o relatório de receita
	// fica levemente inflado e isso precisa estar visível.
	//
	// O código é "entrada", e não "crédito", por dois motivos que se somam: em
	// contabilidade "crédito" quer dizer o contrário do que o leigo entende, e
	// o gosec trata qualquer identificador com "cred" como credencial em
	// potencial (G101) — e a regra do projeto é zero achado sem nenhuma supressão.
	SuggestionCardInflow Suggestion = "entrada_na_fatura"
)

// Códigos de rejeição de linha.
//
// São CÓDIGOS curtos e fechados, nunca o conteúdo da linha: eles são gravados
// em import_rows.reject_reason (varchar(32)) e a frase em português é montada
// na borda. Um campo livre aqui viraria o lugar onde o extrato inteiro acabaria
// copiado para uma tabela nova (spec 0004 §3.6).
const (
	RejectShortRow          = "short_row"
	RejectInvalidDate       = "invalid_date"
	RejectInvalidAmount     = "invalid_amount"
	RejectZeroAmount        = "zero_amount"
	RejectMissingExternalID = "missing_external_id"
	RejectInvalidExternalID = "invalid_external_id"
)

// MaxExternalIDBytes espelha transactions.external_id varchar(64).
const MaxExternalIDBytes = 64

// Erros do núcleo. O handler os traduz para os códigos da §5.4 da spec 0004;
// nenhum carrega conteúdo do arquivo.
var (
	// ErrFormatUnknown — nenhum parser reconheceu o cabeçalho. É a resposta
	// para arquivo do C6 hoje (IMPORT_FORMAT_UNKNOWN).
	ErrFormatUnknown = errors.New("formato de arquivo não reconhecido")

	// ErrFormatAmbiguous — mais de um parser reconheceu. Ver
	// AmbiguousFormatError, que carrega os candidatos
	// (IMPORT_FORMAT_AMBIGUOUS).
	ErrFormatAmbiguous = errors.New("mais de um formato reconheceu o arquivo")

	// ErrFormatNotAllowed — o formato pedido pelo cliente não está registrado.
	// A allowlist é o registro: id que não está nele não vira parser.
	ErrFormatNotAllowed = errors.New("formato não está na lista de formatos aceitos")

	// ErrNoRows — arquivo sem nenhuma linha de dados.
	ErrNoRows = errors.New("arquivo sem linhas de dados")

	// ErrTooManyRows — passou do teto de linhas de dados.
	ErrTooManyRows = errors.New("arquivo com linhas demais")

	// ErrTooManyRejected — passou da fração tolerada de linhas rejeitadas.
	// Não é "dado ruim": é sinal de parser errado, e por isso recusa o arquivo
	// inteiro em vez de importar o que sobrou.
	ErrTooManyRejected = errors.New("linhas rejeitadas demais")

	// ErrClassifyTooCostly — a classificação por palavra-chave deste arquivo
	// passou do orçamento de trabalho de UMA operação
	// (textmatch.MaxMatchWork, achado A1 da revisão de segurança).
	//
	// É um LIMITE do arquivo, como os de volume acima: 422 IMPORT_FILE_REJECTED
	// com a mesma orientação — arquivo menor. Nunca análise parcial: um lote em
	// staging com metade das sugestões seria uma revisão que mente.
	ErrClassifyTooCostly = errors.New("classificação por palavra-chave cara demais para um arquivo")

	// ErrDateSpanTooWide — a janela de datas do arquivo é larga demais. Ela
	// limita a consulta de deduplicação (spec 0004 §4.8).
	ErrDateSpanTooWide = errors.New("janela de datas do arquivo larga demais")

	// ErrAnalyzeTimeout — o ORÇAMENTO DE TEMPO da fase 1 (AnalyzeTimeout)
	// acabou e a análise parou por decisão própria. É limite de trabalho, como
	// os de volume acima: 422 IMPORT_FILE_REJECTED, arquivo menor.
	//
	// ⚠️ Esta sentinela é a ÚNICA autorização para dizer "a análise demorou
	// demais", e ela é emitida apenas nas paradas VOLUNTÁRIAS da fase 1 (ver
	// conferirPrazo/erroDeParada em analyze.go) — nunca deduzida do estado do
	// contexto depois de um erro qualquer.
	//
	// O motivo é concreto: desde que o gormstore passou a embrulhar o erro do
	// driver com o motivo do contexto (platform/storage/ctxerr.go), QUALQUER
	// falha de banco ocorrida com o contexto morto casa
	// `errors.Is(err, context.DeadlineExceeded)`. Traduzir isso em 422 fazia
	// duas coisas erradas ao mesmo tempo: culpava o arquivo do usuário por uma
	// falha do SERVIDOR e apagava a única linha de ERROR daquela falha do log.
	// Falha de banco sob contexto morto é 500 com log de ERROR, e é assim que
	// tem de continuar.
	ErrAnalyzeTimeout = errors.New("o prazo da análise do arquivo acabou")

	// ErrParserMisconfigured — defeito de programação no parser (coluna
	// declarada na assinatura que ele não encontra na tabela, convenção não
	// declarada). Nunca deveria chegar ao usuário.
	ErrParserMisconfigured = errors.New("parser mal configurado")
)

// AmbiguousFormatError carrega os ids que reconheceram o arquivo, para que a
// tela mostre um seletor em vez de uma mensagem genérica.
type AmbiguousFormatError struct {
	// Candidates são ids de parser, em ordem determinística.
	Candidates []string
}

func (e *AmbiguousFormatError) Error() string {
	return fmt.Sprintf("%s: %s", ErrFormatAmbiguous.Error(), strings.Join(e.Candidates, ", "))
}

// Is faz errors.Is(err, ErrFormatAmbiguous) funcionar, para o handler não
// precisar conhecer o tipo só para escolher o código HTTP.
func (e *AmbiguousFormatError) Is(target error) bool { return target == ErrFormatAmbiguous }

// ParsedRow é UMA linha do documento já em valores CANÔNICOS do domínio.
//
// "Canônicos" é o ponto: o que está aqui é exatamente o que será gravado em
// transactions, e é sobre isto — nunca sobre o texto cru do arquivo — que a
// chave de deduplicação é calculada (ADR-025c).
type ParsedRow struct {
	// Seq é a ordem no arquivo (1-based), contando também as linhas
	// rejeitadas: é a chave do cursor da revisão e tem de ser estável.
	Seq int

	// LineNo é a linha FÍSICA no arquivo, que é o que a pessoa procura quando
	// quer conferir. Com preâmbulo, ela não é Seq+1.
	LineNo int

	// Kind é transaction.KindIncome ou transaction.KindExpense, derivado
	// EXCLUSIVAMENTE da convenção de sinal declarada pelo parser.
	Kind string

	OccurredOn civil.Date

	// AmountCents é SEMPRE POSITIVO — o sinal vive no Kind (ADR-003).
	AmountCents int64

	// Description já está sanitizada e truncada em sanitize.MaxRunes.
	Description string

	// DescriptionNorm é textnorm.Normalize(Description) — a forma normalizada
	// da descrição JÁ TRUNCADA. É esta, e nenhuma outra, que entra na chave
	// derivada. Ver Describe.
	DescriptionNorm string

	// ExternalID é a chave natural do banco quando o documento tem uma
	// (o Identificador do Nubank). Nil quando o documento não tem — a fatura
	// não tem, e cai na chave derivada com ordinal.
	ExternalID *string

	// Suggestion é o palpite do parser. Não altera nada do que está acima.
	Suggestion Suggestion
}

// RejectedRow é uma linha que o parser não conseguiu interpretar.
//
// Ela NÃO some: vai para a revisão com o número da linha e o código do motivo,
// para a pessoa poder conferir o arquivo. O que não vai junto é o conteúdo.
type RejectedRow struct {
	Seq    int
	LineNo int
	// Reason é um dos códigos Reject*.
	Reason string
}

// StatementHint é o que o DOCUMENTO revelou sobre a fatura.
//
// É palpite de baixa confiança e pode vir inteiramente vazio: os dois arquivos
// do Nubank não trazem fechamento nem vencimento, e o serviço cai para
// accounts.statement_closing_day / statement_due_day. O nome do arquivo NUNCA
// entra aqui — nome de arquivo é entrada do cliente, e uma fatura arquivada no
// mês errado estraga a competência de dezenas de linhas (spec 0004 §5.3).
type StatementHint struct {
	CompetenceMonth string
	ClosingDate     *civil.Date
	DueDate         *civil.Date
}

// ParseResult é o resultado da leitura de um documento inteiro.
type ParseResult struct {
	FormatID    string
	Institution Institution
	DocKind     DocKind

	// Encoding é como os bytes foram interpretados; aparece no preview para
	// que "por que este acento está estranho?" tenha resposta.
	Encoding csvtext.Encoding

	// HeaderLine é a linha física do cabeçalho (1 quando não há preâmbulo).
	HeaderLine int

	Rows     []ParsedRow
	Rejected []RejectedRow

	// MinDate e MaxDate são a janela coberta pelo documento, calculada sobre
	// as linhas APROVEITADAS. Zero quando não sobrou nenhuma.
	MinDate civil.Date
	MaxDate civil.Date

	// Statement é o palpite de fatura, quando o documento revelou algum.
	Statement *StatementHint
}

// Limits são os tetos aplicados à leitura de um documento.
//
// Campo não-positivo cai no default: um Limits{} esquecido tem de virar o
// limite seguro, nunca "sem limite".
type Limits struct {
	// MaxRows é o teto de linhas de DADOS.
	//
	// O csvtext tem o próprio teto, igual a este, e recusa antes mesmo de
	// montar a tabela. A repetição é deliberada: um limite que só exista no
	// chamador deixa de existir no primeiro chamador novo. Na prática, este
	// campo serve para APERTAR o teto (um parser que só faça sentido em
	// arquivos pequenos), nunca para afrouxá-lo.
	MaxRows int

	// MaxRejectedPercent é a fração de linhas rejeitadas tolerada, em 0..100.
	MaxRejectedPercent int

	// MaxDateSpanDays limita a distância entre a menor e a maior data do
	// arquivo. Ele existe para limitar a consulta de deduplicação, que carrega
	// a janela inteira do arquivo numa tacada (spec 0004 §4.8).
	MaxDateSpanDays int
}

// DefaultLimits são os limites da §5.6 da spec 0004.
func DefaultLimits() Limits {
	return Limits{
		MaxRows:            csvtext.MaxRows,
		MaxRejectedPercent: 20,
		// 5 anos, contados com folga de bissexto.
		MaxDateSpanDays: 5 * 366,
	}
}

// normalized aplica os defaults e o teto duro de cada limite.
//
// O teto duro existe porque um chamador não pode AFROUXAR um limite de
// segurança passando um número maior — só apertá-lo.
func (l Limits) normalized() Limits {
	d := DefaultLimits()
	if l.MaxRows <= 0 || l.MaxRows > d.MaxRows {
		l.MaxRows = d.MaxRows
	}
	if l.MaxRejectedPercent <= 0 || l.MaxRejectedPercent > d.MaxRejectedPercent {
		l.MaxRejectedPercent = d.MaxRejectedPercent
	}
	if l.MaxDateSpanDays <= 0 || l.MaxDateSpanDays > d.MaxDateSpanDays {
		l.MaxDateSpanDays = d.MaxDateSpanDays
	}
	return l
}

// KindFromSigned aplica a convenção de sinal DECLARADA e devolve o kind do
// domínio com o valor absoluto.
//
// ⚠️ É o único lugar do importador onde um sinal vira direção de dinheiro.
// Nenhum parser deve olhar a descrição para decidir se é entrada ou saída: a
// classificação (Suggestion) é outra coisa, acontece depois, e não toca no
// kind. O teste-ouro dos parsers falha se esta separação for quebrada.
//
// Valor ZERO não obedece a convenção nenhuma e é recusado (ok=false) — linha
// rejeitada, nunca valor "consertado".
func KindFromSigned(signed int64, conv SignConvention) (kind string, amountCents int64, ok bool) {
	if signed == 0 || signed == math.MinInt64 {
		// MinInt64 não tem simétrico em int64. csvtext.ParseCents já o barra
		// pela faixa; a guarda fica porque o custo é uma comparação e o preço
		// de errar é um valor negativo continuar negativo depois do "abs".
		return "", 0, false
	}

	var outflow bool
	switch conv {
	case SignNegativeIsOutflow:
		outflow = signed < 0
	case SignPositiveIsOutflow:
		outflow = signed > 0
	default:
		return "", 0, false
	}

	abs := signed
	if abs < 0 {
		abs = -abs
	}
	if outflow {
		return transaction.KindExpense, abs, true
	}
	return transaction.KindIncome, abs, true
}

// Describe devolve, JUNTAS, a descrição que será gravada e a forma normalizada
// que entra na chave de deduplicação.
//
// ⚠️ As duas saem daqui de uma vez, e é de propósito que não exista outro
// caminho. A chave derivada é calculada sobre a descrição JÁ sanitizada e JÁ
// truncada em sanitize.MaxRunes (ADR-025c). Se alguém normalizar o texto CRU e
// truncar depois, a reimportação do mesmo arquivo gera uma chave diferente da
// que está gravada, e o dedup simplesmente para de funcionar — sem que nenhum
// teste pequeno acuse.
func Describe(raw string) (description, normalized string) {
	description = sanitize.Description(raw)
	normalized = textnorm.Normalize(description)
	return description, normalized
}

// ValidExternalID confere a chave natural trazida pelo documento.
//
// Ela é dado CONTROLADO PELO CLIENTE (o arquivo é dele e pode ter sido editado
// à mão), então vale o mesmo rigor de qualquer entrada externa: ASCII visível,
// sem espaço, dentro da largura da coluna. Um parser cujo banco use outro
// alfabeto valida por conta própria.
func ValidExternalID(s string) bool {
	if s == "" || len(s) > MaxExternalIDBytes {
		return false
	}
	for i := range len(s) {
		if s[i] < 0x21 || s[i] > 0x7E {
			return false
		}
	}
	return true
}
