// Package civil modela data civil — dia do calendário, sem hora e sem fuso.
//
// Por que existe um tipo em vez de time.Time (D3 da spec 0003): "9 de setembro
// de 2026" não é um instante. Guardar isso num time.Time obriga a escolher uma
// hora e um fuso que ninguém pediu, e é dessa escolha que nasce o bug clássico
// de "a conta venceu um dia antes" — o valor sai do banco como meia-noite UTC,
// o servidor o converte para America/Sao_Paulo e vira 21h do dia anterior
// (risco R6 do PLANOS.md).
//
// Aqui a data é o que ela é: ano, mês e dia. Ela é persistida como texto
// "YYYY-MM-DD", o que também a torna comparável e ordenável lexicograficamente
// nos quatro dialetos SQL, sem depender de tipo DATE nem do driver.
//
// Este pacote é FOLHA: não importa nada do projeto.
package civil

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Layout é o formato textual canônico — o mesmo do JSON, da URL e do banco.
const Layout = "2006-01-02"

// ErrInvalidDate — texto que não é uma data civil válida.
var ErrInvalidDate = errors.New("data inválida")

// Date é um dia do calendário.
//
// O zero-value é a data zero, que NÃO é uma data válida: use IsZero para
// distinguir "não informado" de "informado".
type Date struct {
	year  int
	month int
	day   int
}

// New monta a data validando o calendário de verdade — 31/02 não existe, e
// 29/02 só existe em ano bissexto.
func New(year, month, day int) (Date, error) {
	if year < 1 || year > 9999 || month < 1 || month > 12 || day < 1 || day > 31 {
		return Date{}, fmt.Errorf("%w: %04d-%02d-%02d", ErrInvalidDate, year, month, day)
	}
	// time.Date normaliza excesso (31/02 vira 03/03); comparar de volta é o que
	// transforma a normalização silenciosa em recusa explícita.
	t := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	if t.Year() != year || int(t.Month()) != month || t.Day() != day {
		return Date{}, fmt.Errorf("%w: %04d-%02d-%02d", ErrInvalidDate, year, month, day)
	}
	return Date{year: year, month: month, day: day}, nil
}

// MustNew é New que entra em pânico no erro. Uso restrito a constante de teste
// e a literal do próprio código — nunca com entrada externa.
func MustNew(year, month, day int) Date {
	d, err := New(year, month, day)
	if err != nil {
		panic(err)
	}
	return d
}

// Parse lê "YYYY-MM-DD". Rígido de propósito: nada de aceitar "2026-9-9",
// espaços em volta ou sufixo de hora. Entrada externa tem uma forma só.
func Parse(s string) (Date, error) {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return Date{}, fmt.Errorf("%w: formato esperado YYYY-MM-DD", ErrInvalidDate)
	}
	year, err := atoiExato(s[0:4])
	if err != nil {
		return Date{}, err
	}
	month, err := atoiExato(s[5:7])
	if err != nil {
		return Date{}, err
	}
	day, err := atoiExato(s[8:10])
	if err != nil {
		return Date{}, err
	}
	return New(year, month, day)
}

// atoiExato recusa sinal, espaço e qualquer coisa que não seja dígito —
// strconv.Atoi sozinho aceitaria "+1" e " 12".
func atoiExato(s string) (int, error) {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("%w: %q não é numérico", ErrInvalidDate, s)
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%w: %q", ErrInvalidDate, s)
	}
	return n, nil
}

// Today devolve o dia de hoje NO FUSO INFORMADO.
//
// O fuso é parâmetro obrigatório, sem default, de propósito: "hoje" é regra de
// negócio no HomeFinance (é ele que decide se uma conta está atrasada), e ele é
// o da casa — nunca o do servidor nem o do navegador (ADR-019).
func Today(loc *time.Location) Date {
	return FromTime(time.Now(), loc)
}

// FromTime projeta um instante no fuso informado e descarta a hora.
func FromTime(t time.Time, loc *time.Location) Date {
	if loc == nil {
		loc = time.UTC
	}
	local := t.In(loc)
	return Date{year: local.Year(), month: int(local.Month()), day: local.Day()}
}

// Year, Month e Day expõem os componentes.
func (d Date) Year() int  { return d.year }
func (d Date) Month() int { return d.month }
func (d Date) Day() int   { return d.day }

// IsZero informa se a data não foi preenchida.
func (d Date) IsZero() bool { return d == Date{} }

// String devolve "YYYY-MM-DD"; a data zero devolve string vazia, para nunca
// escrever "0000-00-00" — que o MySQL recusa com NO_ZERO_DATE.
func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.year, d.month, d.day)
}

// YearMonth devolve "YYYY-MM" — a chave de agregação portátil do projeto
// (armadilha P1: cada dialeto tem uma sintaxe diferente para extrair o mês, e
// nenhuma é necessária se a aplicação gravar a chave pronta).
func (d Date) YearMonth() string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d", d.year, d.month)
}

// Compare devolve -1, 0 ou 1. Como o formato é de largura fixa e zero à
// esquerda, a ordem lexicográfica do texto é a mesma — é o que permite ordenar
// no banco sem função de data.
func (d Date) Compare(other Date) int {
	switch {
	case d.year != other.year:
		return sinal(d.year - other.year)
	case d.month != other.month:
		return sinal(d.month - other.month)
	case d.day != other.day:
		return sinal(d.day - other.day)
	default:
		return 0
	}
}

// Before informa se d é anterior a other.
func (d Date) Before(other Date) bool { return d.Compare(other) < 0 }

// After informa se d é posterior a other.
func (d Date) After(other Date) bool { return d.Compare(other) > 0 }

func sinal(n int) int {
	if n < 0 {
		return -1
	}
	return 1
}

// MarshalJSON serializa como string "YYYY-MM-DD".
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return []byte(`"` + d.String() + `"`), nil
}

// UnmarshalJSON aceita apenas a string canônica ou null.
//
// O erro devolvido é um *json.UnmarshalTypeError, e isso é deliberado: o
// `encoding/json` só preenche `Struct` e `Field` — ou seja, só diz QUAL campo
// do corpo estava errado — para esse tipo de erro. Devolvendo um erro comum, a
// API responderia "dados inválidos" sem dizer onde, e o formulário não teria
// como destacar o campo. É a única forma documentada de recuperar o nome do
// campo a partir de um Unmarshaler próprio.
func (d *Date) UnmarshalJSON(data []byte) error {
	s := string(data)
	if s == "null" {
		*d = Date{}
		return nil
	}
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return erroDeTipo(s)
	}
	parsed, err := Parse(strings.TrimSuffix(strings.TrimPrefix(s, `"`), `"`))
	if err != nil {
		return erroDeTipo(s)
	}
	*d = parsed
	return nil
}

func erroDeTipo(valor string) error {
	return &json.UnmarshalTypeError{
		Value: "valor " + valor + " (esperava data AAAA-MM-DD)",
		Type:  reflect.TypeOf(Date{}),
	}
}

// DaysBetween devolve a distância ABSOLUTA em dias entre duas datas civis.
//
// A conta é feita em aritmética inteira pelo algoritmo days_from_civil, e não
// convertendo para time.Time: uma data civil não tem hora nem fuso, e
// atravessar time.Time para subtrair traria de volta exatamente as duas coisas
// que o tipo existe para não ter — além de estourar o int64 de Duration em
// intervalos de séculos.
//
// Data zero devolve 0: "não informado" não tem distância. Mora aqui, e não
// no importador, porque a folga de ±DedupWindowDays é medida por dois
// consumidores (a análise da importação e o reprocessamento de
// transferências), e o pacote transaction não pode importar importer.
func DaysBetween(a, b Date) int {
	if a.IsZero() || b.IsZero() {
		return 0
	}
	d := daysFromCivil(a) - daysFromCivil(b)
	if d < 0 {
		return -d
	}
	return d
}

// AddDays desloca a data em n dias (n negativo anda para trás).
//
// A conta atravessa time.Date em UTC e descarta a hora: é a única aritmética
// de calendário confiável (fevereiro bissexto, fim de mês, fim de ano), e em
// UTC não há horário de verão para encurtar ou esticar o dia. Data zero
// continua zero — "não informado" não se desloca —, e um resultado fora do
// calendário válido devolve a data original em vez de um valor inventado.
func AddDays(d Date, n int) Date {
	if d.IsZero() {
		return d
	}
	t := time.Date(d.year, time.Month(d.month), d.day, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n)
	out, err := New(t.Year(), int(t.Month()), t.Day())
	if err != nil {
		return d
	}
	return out
}

// daysFromCivil converte a data civil no número de dias desde 1970-01-01.
//
// É o algoritmo de Howard Hinnant (chrono do C++20): só soma, subtração e
// divisão inteira, válido para todo o calendário gregoriano proléptico.
func daysFromCivil(d Date) int {
	y, m, dia := d.year, d.month, d.day
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 {
		era = (y - 399) / 400
	}
	yoe := y - era*400                     // [0, 399]
	mp := (m + 9) % 12                     // março = 0
	doy := (153*mp+2)/5 + dia - 1          // [0, 365]
	doe := yoe*365 + yoe/4 - yoe/100 + doy // [0, 146096]
	// 719468 é a distância de 0000-03-01 a 1970-01-01, que é o que move a
	// origem do algoritmo (março do ano zero) para a época Unix.
	return era*146097 + doe - 719468
}
