package importer

import (
	"context"
	"fmt"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
)

// Parser é o plugin de UM par (instituição × tipo de documento).
//
// Um banco novo custa uma implementação disto mais uma fixture anonimizada, e
// nada no núcleo (ADR-024a). O que o parser é obrigado a DECLARAR, e nunca a
// inferir do conteúdo, está na §7.3 da spec 0004: assinatura de cabeçalho,
// separador, formato de data, formato numérico e — o item que mais custa se
// vier errado — a convenção de sinal.
type Parser interface {
	// ID é o identificador estável do leiaute, no formato
	// "<instituicao>.<documento>.v<N>". É ele que o cliente manda em `format`
	// para desempatar uma detecção ambígua, e é ele que fica gravado no lote.
	// Versão nova de leiaute é um id novo, nunca uma mudança em silêncio no
	// mesmo id: o lote antigo tem de continuar dizendo com o que foi lido.
	ID() string

	// Institution e DocKind são o par que o parser atende.
	Institution() Institution
	DocKind() DocKind

	// Detect diz se este parser reconhece o cabeçalho. NÃO pode ter efeito
	// colateral e NÃO pode olhar as linhas de dados: detecção que olha o
	// conteúdo vira adivinhação de formato.
	Detect(header []string, sep rune) Confidence

	// Parse lê a tabela inteira. O erro devolvido é de ARQUIVO (limite
	// estourado, cabeçalho impossível); erro de linha vira ParseResult.Rejected.
	Parse(ctx context.Context, t *csvtext.Table, limits Limits) (ParseResult, error)
}

// RowFunc interpreta UMA linha de dados.
//
// Devolve a linha pronta, OU um código de rejeição (um dos Reject*) — nunca as
// duas coisas, e nunca um valor "consertado". Seq e LineNo são preenchidos pelo
// núcleo: o parser não precisa (nem deve) contar linhas, porque com preâmbulo a
// conta não é a óbvia.
type RowFunc func(rec []string) (ParsedRow, string)

// ctxCheckInterval é de quantas em quantas linhas o cancelamento é conferido.
//
// A análise roda com deadline de 15 s (spec 0004 §5.6) e precisa morrer antes
// do WriteTimeout de 30 s. Conferir a cada linha custaria uma chamada por linha
// sem ganho nenhum; a cada 256 o atraso máximo é imperceptível.
const ctxCheckInterval = 256

// ParseTable é o laço ÚNICO por onde todo parser passa.
//
// Ele existe porque os tetos, a razão de linhas rejeitadas, a numeração de Seq
// e LineNo e a janela de datas precisam ser idênticos em todos os parsers. Um
// parser que faça o próprio laço reimplementa cinco limites de segurança e vai
// errar pelo menos um — e o que ele errar só aparece no arquivo de um usuário,
// meses depois.
//
// O contrato de Seq é importante e não é óbvio: a linha REJEITADA também
// consome um Seq. Seq é a ordem no arquivo, é chave do cursor da revisão e é
// única por lote (ux_import_rows_seq); pular os rejeitados faria a revisão
// mostrar "linha 7" para a oitava linha do arquivo.
func ParseTable(ctx context.Context, p Parser, t *csvtext.Table, limits Limits, fn RowFunc) (ParseResult, error) {
	if p == nil || fn == nil {
		return ParseResult{}, fmt.Errorf("%w: parser ou leitor de linha ausente", ErrParserMisconfigured)
	}
	if t == nil {
		return ParseResult{}, ErrNoRows
	}

	lim := limits.normalized()

	total := len(t.Rows)
	if total == 0 {
		return ParseResult{}, ErrNoRows
	}
	if total > lim.MaxRows {
		return ParseResult{}, fmt.Errorf("%w: %d linhas (o máximo é %d)", ErrTooManyRows, total, lim.MaxRows)
	}

	res := ParseResult{
		FormatID:    p.ID(),
		Institution: p.Institution(),
		DocKind:     p.DocKind(),
		Encoding:    t.Encoding,
		// Sem preâmbulo o cabeçalho é a linha 1. Registry.Parse corrige quando
		// houver preâmbulo (ver shiftLines).
		HeaderLine: 1,
		Rows:       make([]ParsedRow, 0, total),
	}

	largura := len(t.Header)

	for i, rec := range t.Rows {
		if i%ctxCheckInterval == 0 {
			// Parada VOLUNTÁRIA: é daqui que sai o ErrAnalyzeTimeout quando o
			// orçamento da fase 1 acaba no meio da leitura. Ver conferirPrazo
			// em analyze.go para o porquê de a classificação nascer aqui e não
			// na borda.
			if err := conferirPrazo(ctx, "lendo o documento"); err != nil {
				return ParseResult{}, err
			}
		}

		seq := i + 1
		lineNo := i + 2 // 1 é o cabeçalho

		// O csv.Reader com FieldsPerRecord já recusa largura diferente, então
		// esta guarda é redundante — e fica porque o rowFunc indexa o registro
		// por posição, e um índice fora da faixa aqui seria panic em caminho
		// de requisição.
		if len(rec) < largura {
			res.Rejected = append(res.Rejected, RejectedRow{Seq: seq, LineNo: lineNo, Reason: RejectShortRow})
			continue
		}

		row, motivo := fn(rec)
		if motivo != "" {
			res.Rejected = append(res.Rejected, RejectedRow{Seq: seq, LineNo: lineNo, Reason: motivo})
			continue
		}

		row.Seq, row.LineNo = seq, lineNo
		res.Rows = append(res.Rows, row)

		if res.MinDate.IsZero() || row.OccurredOn.Before(res.MinDate) {
			res.MinDate = row.OccurredOn
		}
		if res.MaxDate.IsZero() || row.OccurredOn.After(res.MaxDate) {
			res.MaxDate = row.OccurredOn
		}
	}

	// Erro é por linha ATÉ o teto; passou dele, o arquivo inteiro é recusado.
	// A conta é feita em inteiros de propósito: `float64(n)/float64(total)*100`
	// aqui seria a única aritmética de fração de um pacote que trata dinheiro.
	if len(res.Rejected)*100 > lim.MaxRejectedPercent*total {
		return ParseResult{}, fmt.Errorf(
			"%w: %d de %d linhas (o máximo é %d%%)",
			ErrTooManyRejected, len(res.Rejected), total, lim.MaxRejectedPercent)
	}

	if len(res.Rows) == 0 {
		return ParseResult{}, ErrNoRows
	}

	if span := DaysBetween(res.MinDate, res.MaxDate); span > lim.MaxDateSpanDays {
		return ParseResult{}, fmt.Errorf(
			"%w: %d dias (o máximo é %d)", ErrDateSpanTooWide, span, lim.MaxDateSpanDays)
	}

	return res, nil
}

// shiftLines empurra LineNo das linhas aproveitadas e das rejeitadas quando o
// documento tinha preâmbulo. Seq NÃO se mexe: ele conta linhas de dados, não
// linhas físicas.
func (r *ParseResult) shiftLines(offset int) {
	if offset <= 0 {
		return
	}
	for i := range r.Rows {
		r.Rows[i].LineNo += offset
	}
	for i := range r.Rejected {
		r.Rejected[i].LineNo += offset
	}
	r.HeaderLine += offset
}

// DaysBetween devolve a distância ABSOLUTA em dias entre duas datas civis.
//
// Delega a civil.DaysBetween: a aritmética passou a morar no pacote de data
// porque o reprocessamento de transferências (internal/transaction, ADR-028)
// mede a mesma folga de ±DedupWindowDays e não pode importar este pacote —
// seria ciclo. O nome fica aqui para os chamadores do importador não mudarem.
//
// Data zero devolve 0: "arquivo sem linha aproveitável" não tem janela.
func DaysBetween(a, b civil.Date) int {
	return civil.DaysBetween(a, b)
}
