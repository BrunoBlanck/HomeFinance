package csvtext

import (
	"fmt"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
)

// DateFormat declara como o layout escreve datas.
//
// Como em NumberFormat, o zero-value é INVÁLIDO: `03/04/2026` é 3 de abril num
// extrato brasileiro e 4 de março num americano, e não existe pista no texto
// que resolva isso — só o dia 13 em diante desempata, e o arquivo pode não ter
// nenhum. Adivinhar aqui é trocar a data de um lançamento em silêncio.
type DateFormat uint8

const (
	_ DateFormat = iota

	// DateDMY — `04/08/2026`.
	DateDMY

	// DateISO — `2026-09-05`.
	DateISO
)

func (f DateFormat) String() string {
	switch f {
	case DateDMY:
		return "DD/MM/YYYY"
	case DateISO:
		return "YYYY-MM-DD"
	default:
		return "não declarado"
	}
}

// ParseDate converte o texto de um campo de data em civil.Date.
//
// Rígido de propósito: largura fixa, só dígitos, sem tolerar `4/8/2026` nem
// sufixo de hora. Entrada externa tem uma forma só, e a validação de calendário
// é a do civil.New — `31/02/2026` é recusado, não normalizado para 03/03.
func ParseDate(s string, format DateFormat) (civil.Date, error) {
	// O trim vale para o espaço que o CSV às vezes deixa em volta do campo;
	// nada além disso é tolerado.
	t := strings.TrimSpace(s)
	if t == "" {
		return civil.Date{}, fmt.Errorf("%w: data vazia", ErrInvalidDate)
	}

	switch format {
	case DateDMY:
		if len(t) != 10 || t[2] != '/' || t[5] != '/' {
			return civil.Date{}, fmt.Errorf("%w: %q não está em DD/MM/YYYY", ErrInvalidDate, s)
		}
		dia, err := doisDigitos(t[0:2])
		if err != nil {
			return civil.Date{}, fmt.Errorf("%w: %q", ErrInvalidDate, s)
		}
		mes, err := doisDigitos(t[3:5])
		if err != nil {
			return civil.Date{}, fmt.Errorf("%w: %q", ErrInvalidDate, s)
		}
		ano, err := quatroDigitos(t[6:10])
		if err != nil {
			return civil.Date{}, fmt.Errorf("%w: %q", ErrInvalidDate, s)
		}
		d, err := civil.New(ano, mes, dia)
		if err != nil {
			return civil.Date{}, fmt.Errorf("%w: %q", ErrInvalidDate, s)
		}
		return d, nil

	case DateISO:
		d, err := civil.Parse(t)
		if err != nil {
			return civil.Date{}, fmt.Errorf("%w: %q", ErrInvalidDate, s)
		}
		return d, nil

	default:
		return civil.Date{}, fmt.Errorf("%w: formato de data não declarado", ErrInvalidDate)
	}
}

func doisDigitos(s string) (int, error) {
	if err := somenteDigitos(s); err != nil {
		return 0, err
	}
	return int(s[0]-'0')*10 + int(s[1]-'0'), nil
}

func quatroDigitos(s string) (int, error) {
	if err := somenteDigitos(s); err != nil {
		return 0, err
	}
	n := 0
	for i := range len(s) {
		n = n*10 + int(s[i]-'0')
	}
	return n, nil
}
