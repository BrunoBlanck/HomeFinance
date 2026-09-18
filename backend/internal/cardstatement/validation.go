package cardstatement

import (
	"errors"
	"fmt"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
)

// Situação da fatura. Conjunto FECHADO, e DERIVADO — nenhum destes valores é
// coluna (ADR-023d).
const (
	// StatusOpen — ainda não venceu e ainda não foi paga.
	StatusOpen = "em_aberto"

	// StatusPaid — o que foi pago cobre o que ela cobra.
	StatusPaid = "paga"

	// StatusOverdue — venceu antes de hoje, NO FUSO DA CASA, e não foi paga.
	StatusOverdue = "vencida"
)

// Erros de validação da fatura.
var (
	// ErrNotCreditCard — fatura em conta que não é cartão de crédito.
	//
	// É a trava mais forte da importação (§3.3 da spec 0004): fatura em conta
	// corrente inverteria o sentido de toda linha do documento, e o estrago
	// apareceria como "meu saldo está errado", meses depois.
	ErrNotCreditCard = errors.New("fatura só existe em conta de cartão de crédito")

	// ErrAccountArchived — conta arquivada como destino (422, não 404: a conta
	// É da casa, e basta desarquivar).
	ErrAccountArchived = errors.New("conta arquivada")

	// ErrInvalidMonth — competência fora da forma "YYYY-MM".
	ErrInvalidMonth = errors.New("competência inválida")

	// ErrInvalidDates — fechamento ou vencimento ausentes, ou fechamento
	// depois do vencimento. Uma fatura fecha ANTES de vencer; a ordem
	// invertida é dado digitado errado, e aceitá-la produziria um "vencida"
	// que ninguém consegue explicar.
	ErrInvalidDates = errors.New("datas de fechamento e vencimento inválidas")

	// ErrInvalidCursor — cursor de paginação das LINHAS da fatura malformado.
	//
	// Ele nasce no domínio de lançamentos (o cursor é o mesmo do app inteiro) e
	// é traduzido para cá pelo adaptador transaction.StatementLines, que é
	// quem conhece os dois lados. Sem essa tradução, este pacote precisaria
	// importar o de lançamentos — que já o importa — e o compilador recusaria
	// o ciclo.
	//
	// A resposta ao cliente é 400 SEM detalhe (S5): o cursor é opaco, e
	// explicar por que ele não serve é ensinar a forjá-lo.
	ErrInvalidCursor = errors.New("cursor inválido")

	// ErrCompetenceMismatch — a competência não é o mês do VENCIMENTO.
	//
	// Não é preciosismo: a competência é DEFINIDA como o mês do vencimento (D2
	// da spec 0004). Deixar divergir faria a mesma fatura ser chamada de
	// setembro na listagem e de agosto no relatório, e a conta nunca fecharia.
	ErrCompetenceMismatch = errors.New("a competência da fatura é o mês do vencimento")
)

// ParseMonth valida "YYYY-MM" e devolve o primeiro dia do mês.
func ParseMonth(s string) (civil.Date, error) {
	if len(s) != 7 {
		return civil.Date{}, fmt.Errorf("%w: formato esperado AAAA-MM", ErrInvalidMonth)
	}
	d, err := civil.Parse(s + "-01")
	if err != nil {
		return civil.Date{}, fmt.Errorf("%w: formato esperado AAAA-MM", ErrInvalidMonth)
	}
	return d, nil
}

// ValidateDates confere fechamento, vencimento e a relação dos dois com a
// competência.
func ValidateDates(competenceMonth string, closing, due civil.Date) error {
	if _, err := ParseMonth(competenceMonth); err != nil {
		return err
	}
	if closing.IsZero() || due.IsZero() {
		return fmt.Errorf("%w: as duas datas são obrigatórias", ErrInvalidDates)
	}
	if due.Before(closing) {
		return fmt.Errorf("%w: o fechamento vem antes do vencimento", ErrInvalidDates)
	}
	if due.YearMonth() != competenceMonth {
		return ErrCompetenceMismatch
	}
	return nil
}

// DeriveStatus calcula a situação da fatura (ADR-023d).
//
// `hoje` é PARÂMETRO, sem default, de propósito: "hoje" é o dia no fuso da CASA
// (ADR-019a), nunca o do servidor. Uma fatura que vence dia 13 está em aberto
// às 22h do dia 13 em São Paulo e já seria "vencida" se o servidor em UTC
// decidisse sozinho — e o usuário veria a fatura vencer antes da hora, na
// própria tela, sem ter feito nada.
//
// A ordem das perguntas é a da spec, e importa: paga primeiro. Uma fatura paga
// com atraso é PAGA, não vencida — cobrar de novo quem já pagou é o pior erro
// que esta função poderia cometer.
//
// Fatura sem nada a cobrar (total zero) e sem pagamento cai em "paga" pela
// regra literal `paidCents >= totalCents`. É o resultado certo: não há o que
// pagar.
func DeriveStatus(totalCents, paidCents int64, dueDate, hoje civil.Date) string {
	if paidCents >= totalCents {
		return StatusPaid
	}
	if !hoje.IsZero() && dueDate.Before(hoje) {
		return StatusOverdue
	}
	return StatusOpen
}

// IsValidationError informa se o erro é de entrada do usuário (422) em vez de
// falha interna.
func IsValidationError(err error) bool {
	return errors.Is(err, ErrNotCreditCard) ||
		errors.Is(err, ErrInvalidCursor) ||
		errors.Is(err, ErrAccountArchived) ||
		errors.Is(err, ErrInvalidMonth) ||
		errors.Is(err, ErrInvalidDates) ||
		errors.Is(err, ErrCompetenceMismatch)
}
