// Package account modela conta — onde o dinheiro está: carteira, conta
// bancária, poupança, cartão.
//
// Regras que este pacote sustenta (spec 0003):
//   - toda operação é escopada por household_id vindo do TOKEN, nunca do
//     cliente (docs/SEGURANCA.md §2 — BOLA é o risco nº 1);
//   - dinheiro é int64 em centavos, sempre (ADR-003);
//   - saldo é DERIVADO, nunca coluna (ADR-017);
//   - arquivar ≠ excluir, e excluir é lógico.
package account

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// Tipos de conta. Conjunto FECHADO — a validação é por allowlist, nunca por
// "qualquer string que o cliente mandar".
//
// KindCreditCard é, no v1, apenas um rótulo: não há fatura, fechamento nem
// competência separada de caixa (ADR-019c). Saldo negativo é a dívida.
const (
	KindCash       = "cash"
	KindChecking   = "checking"
	KindSavings    = "savings"
	KindCreditCard = "credit_card"
	KindOther      = "other"
)

// Instituições. Conjunto FECHADO, exatamente o enum `Institution` do contrato
// (c6 · inter · nubank · other) — texto livre aqui viraria uma trava de
// importação que nunca dispara.
//
// A instituição NÃO escolhe o parser da importação: quem escolhe é a detecção
// por cabeçalho (ADR-024a). Ela é TRAVA DE CONSISTÊNCIA — arquivo detectado
// como de outro emissor não entra nesta conta (§3.3 da spec 0004) —, e é por
// isso que ela precisa ter caminho de escrita: sem ele, toda conta fica em
// `other`, e `other` não trava nada.
//
// O vocabulário é duplicado de propósito em relação a importer.Institution:
// aquele pacote importa este, então a dependência inversa seria um ciclo. São
// quatro strings estáveis e um teste que confere as duas listas lado a lado.
const (
	// InstitutionOther é o default da coluna institution (schema v3): conta
	// que não veio de nenhuma instituição conhecida pela importação.
	InstitutionOther  = "other"
	InstitutionC6     = "c6"
	InstitutionInter  = "inter"
	InstitutionNubank = "nubank"
)

// Faixa dos dias de fatura. É o dia DO MÊS, então 1–31 — o grampeamento ao
// último dia de um mês curto (31 em fevereiro) é feito por quem calcula a data,
// e não recusando a configuração.
const (
	MinStatementDay = 1
	MaxStatementDay = 31
)

// Limites de domínio (§4.5 do PLANOS.md). Todo limite RECUSA com erro claro;
// nenhum trunca em silêncio.
const (
	// MaxNameLen é medido em runas, não em bytes: "Alimentação" tem 11 runas
	// e 13 bytes, e o usuário conta as que vê.
	MaxNameLen = 80

	// MaxPerHousehold conta as contas NÃO EXCLUÍDAS — arquivada ocupa vaga,
	// porque ela continua existindo e pode ser desarquivada.
	MaxPerHousehold = 50

	// MaxAmountCents é R$ 999.999.999,99. O saldo de abertura pode ser
	// negativo, então a faixa é simétrica.
	MaxAmountCents = 99_999_999_999
)

// Erros de domínio. O handler os traduz para HTTP; nenhum carrega detalhe
// interno nem dado de outra casa.
var (
	// ErrNotFound — conta inexistente OU de outra casa. Os dois casos são o
	// MESMO erro de propósito: distinguir confirmaria a existência do recurso
	// alheio, que é o vazamento que o 404 do S1 existe para fechar.
	ErrNotFound = errors.New("conta não encontrada")

	// ErrNameTaken — já existe conta ativa com este nome na casa.
	ErrNameTaken = errors.New("já existe uma conta com este nome")

	// ErrInvalidKind — tipo fora da allowlist.
	ErrInvalidKind = errors.New("tipo de conta inválido")

	// ErrInvalidName — nome vazio ou longo demais.
	ErrInvalidName = errors.New("nome de conta inválido")

	// ErrInvalidAmount — valor fora da faixa de sanidade.
	ErrInvalidAmount = errors.New("valor fora da faixa permitida")

	// ErrInvalidDate — data de abertura ausente ou impossível.
	ErrInvalidDate = errors.New("data de abertura inválida")

	// ErrTooMany — teto de contas por casa atingido.
	ErrTooMany = errors.New("limite de contas por casa atingido")

	// ErrInUse — a conta tem lançamento; excluir não é permitido, arquivar é.
	ErrInUse = errors.New("conta em uso")

	// ErrInvalidInstitution — instituição fora da allowlist.
	ErrInvalidInstitution = errors.New("instituição inválida")

	// ErrInvalidStatementDay — dia de fechamento ou de vencimento fora de 1–31.
	ErrInvalidStatementDay = errors.New("dia de fatura inválido")

	// ErrStatementDayNotAllowed — dia de fatura em conta que não é cartão de
	// crédito.
	//
	// É recusa, e não "ignorar o campo": ignorado, o usuário configuraria o
	// vencimento numa conta corrente, veria a tela aceitar e nunca entenderia
	// por que a importação não usa o dado. Vale também quando o TIPO muda — a
	// conta que deixa de ser cartão tem de limpar os dias na mesma edição, para
	// não sobrar configuração órfã apontando para uma regra que não se aplica
	// mais.
	ErrStatementDayNotAllowed = errors.New("dia de fatura só existe em cartão de crédito")

	// ErrInvalidKeyword — palavra-chave fora das regras da spec 0005 §4.1.2
	// (2–40 runas, allowlist de caracteres, ao menos uma palavra útil).
	// Nunca carrega a palavra: o erro pode acabar num log.
	ErrInvalidKeyword = errors.New("palavra-chave inválida")

	// ErrDuplicateKeyword — a mesma palavra (pela forma normalizada) veio
	// duas vezes na lista enviada.
	ErrDuplicateKeyword = errors.New("palavra-chave repetida na lista")

	// ErrTooManyKeywords — a lista passa de MaxKeywordsPerOwner. É recusa,
	// nunca truncamento: cortar em silêncio faria a pessoa achar que a 21ª
	// palavra foi gravada.
	ErrTooManyKeywords = errors.New("limite de palavras-chave por conta excedido")
)

// KeywordValidationError aponta QUAL item da lista de palavras-chave foi
// recusado — é o que vira `fields.keywords[i]` no 400 do contrato.
//
// Err é ErrInvalidKeyword (embrulhando a razão do textmatch, que nunca ecoa
// a palavra) ou ErrDuplicateKeyword. A mensagem também não contém a palavra.
type KeywordValidationError struct {
	Index int
	Err   error
}

func (e *KeywordValidationError) Error() string {
	return fmt.Sprintf("keywords[%d]: %v", e.Index, e.Err)
}

func (e *KeywordValidationError) Unwrap() error { return e.Err }

// KeywordTakenError é o 409 KEYWORD_TAKEN: a palavra já pertence a OUTRA
// conta DA MESMA CASA (spec 0005 §4.1.2). Conta e categoria são conjuntos
// independentes — a mesma palavra numa categoria não colide. É a ÚNICA forma
// em que a colisão sai do serviço — Create e Update nunca devolvem
// ErrKeywordTaken cru, porque o contrato exige `fields.keyword`.
//
// Keyword é a forma exibível que o próprio cliente enviou e volta em
// `fields.keyword`; OwnerID é a conta da casa do token que já a tem (a
// consulta filtra por household_id, então nunca cita recurso alheio). OwnerID
// VAZIO significa dona desconhecida: é a corrida tripla que
// Service.escreverComPalavras descreve, e o handler omite `ownerId`.
// Error() NÃO contém a palavra — o erro passa pelo log do handler — e
// desembrulha para ErrKeywordTaken, para que os dois caminhos (pré-checagem
// e índice único na corrida) sejam tratados juntos por errors.Is.
type KeywordTakenError struct {
	Keyword string
	OwnerID string
}

func (e *KeywordTakenError) Error() string { return ErrKeywordTaken.Error() }

func (e *KeywordTakenError) Unwrap() error { return ErrKeywordTaken }

// Account é a conta.
//
// NameNorm não é campo do cliente: ele é derivado de Name em toda escrita
// (S2 — mass assignment). Está aqui, e não só no modelo de persistência,
// porque a regra de unicidade é de domínio.
type Account struct {
	ID                  string
	HouseholdID         string
	Name                string
	NameNorm            string
	Kind                string
	OpeningBalanceCents int64
	OpeningDate         civil.Date

	// --- schema v3 (importação e fatura de cartão) ---

	// Institution é a instituição da conta, usada pela importação para
	// escolher o leiaute do arquivo. O vocabulário de instituições pertence ao
	// pacote de importação; aqui é texto, com InstitutionOther como default —
	// conta manual nunca fica com o campo vazio numa coluna NOT NULL.
	Institution string

	// StatementClosingDay e StatementDueDay são o dia do mês em que a fatura
	// do cartão fecha e vence. Anuláveis porque só existem em conta de cartão:
	// "não se aplica" não é "dia zero".
	StatementClosingDay *int
	StatementDueDay     *int

	ArchivedAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
	DeletedAt  *time.Time
}

// View é o que sai na API. DTO próprio de propósito: nunca serializamos a
// entidade (docs/SEGURANCA.md §4), e assim uma coluna nova não vaza por
// acidente.
type View struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`

	// Os três campos do schema v3 são `required` no contrato, e os dois dias
	// são anuláveis. Eles saem SEMPRE — inclusive nulos —, porque o tipo gerado
	// no frontend os declara não opcionais: omiti-los faria
	// `ROTULO[conta.institution]` receber undefined em runtime, numa tela que
	// compilou sem um único aviso.
	Institution         string `json:"institution"`
	StatementClosingDay *int   `json:"statementClosingDay"`
	StatementDueDay     *int   `json:"statementDueDay"`

	OpeningBalanceCents int64      `json:"openingBalanceCents"`
	OpeningDate         civil.Date `json:"openingDate"`
	// BalanceCents é DERIVADO (ADR-017): saldo de abertura MAIS a soma com
	// sinal dos lançamentos não excluídos da conta. Nunca é coluna, e o
	// contrato não mudou quando a soma entrou — que era o ponto de derivá-lo
	// desde o começo.
	BalanceCents int64 `json:"balanceCents"`
	// Keywords são as palavras-chave na forma exibível, na ordem de cadastro
	// (spec 0005 §4.1). `required` no contrato: sempre presente, `[]` quando
	// vazio — nunca null.
	Keywords   []string `json:"keywords"`
	ArchivedAt *string  `json:"archivedAt"`
	CreatedAt  string   `json:"createdAt"`
	UpdatedAt  string   `json:"updatedAt"`
}

// ListView é a resposta de GET /accounts.
//
// O total vem junto, calculado no servidor, para que a tela não some centavos
// no cliente — soma em dois lugares é divergência esperando acontecer.
type ListView struct {
	Items             []View `json:"items"`
	TotalBalanceCents int64  `json:"totalBalanceCents"`
}

// ValidKind informa se o tipo pertence à allowlist.
func ValidKind(kind string) bool {
	switch kind {
	case KindCash, KindChecking, KindSavings, KindCreditCard, KindOther:
		return true
	default:
		return false
	}
}

// Kinds devolve a allowlist, na ordem de exibição.
func Kinds() []string {
	return []string{KindCash, KindChecking, KindSavings, KindCreditCard, KindOther}
}

// ValidInstitution informa se a instituição pertence à allowlist.
func ValidInstitution(institution string) bool {
	switch institution {
	case InstitutionC6, InstitutionInter, InstitutionNubank, InstitutionOther:
		return true
	default:
		return false
	}
}

// Institutions devolve a allowlist, na ordem de exibição.
func Institutions() []string {
	return []string{InstitutionC6, InstitutionInter, InstitutionNubank, InstitutionOther}
}

// NormalizeInstitution valida a instituição e devolve o valor a gravar.
//
// Ausente vira InstitutionOther: a coluna é NOT NULL, e conta criada sem
// instituição não pode ficar com string vazia — ela passaria no banco e depois
// faria a trava de consistência da importação comparar contra o nada.
func NormalizeInstitution(raw string) (string, error) {
	if raw == "" {
		return InstitutionOther, nil
	}
	if !ValidInstitution(raw) {
		// A mensagem não ecoa o valor recebido: ela pode ir para o log, e
		// entrada do usuário não tem o que fazer lá.
		return "", ErrInvalidInstitution
	}
	return raw, nil
}

// OptionalDay é o TRI-ESTADO de um dia de fatura numa edição parcial.
//
// Sem ele, `*int` colapsa dois estados que significam coisas opostas: "não
// mexi neste campo" e "quero apagar o que estava lá". Colapsados, o usuário que
// limpa o dia de vencimento do cartão vê o valor antigo voltar — e é o tipo de
// erro que ninguém reporta, porque parece que a pessoa é que esqueceu de
// salvar.
type OptionalDay struct {
	// Set diz que o campo VEIO no corpo da requisição.
	Set bool
	// Day é o valor; nulo com Set verdadeiro significa "limpar".
	Day *int
}

// ValidateStatementDays confere os dois dias de fatura contra o TIPO da conta.
//
// A conferência é sobre o estado FINAL da conta (o tipo depois da edição, os
// dias depois da edição), e não sobre o que veio no corpo: é a única forma de
// não deixar passar a conta que deixa de ser cartão e fica com um dia de
// vencimento pendurado, apontando para uma regra que não se aplica mais.
func ValidateStatementDays(kind string, closingDay, dueDay *int) error {
	if kind != KindCreditCard {
		if closingDay != nil || dueDay != nil {
			return ErrStatementDayNotAllowed
		}
		return nil
	}
	if err := validateStatementDay(closingDay); err != nil {
		return err
	}
	return validateStatementDay(dueDay)
}

func validateStatementDay(day *int) error {
	if day == nil {
		// Nulo é "não configurado", e é um estado legítimo: a importação de
		// fatura então pede as datas ao usuário no passo de revisão.
		return nil
	}
	if *day < MinStatementDay || *day > MaxStatementDay {
		return fmt.Errorf("%w: use de %d a %d", ErrInvalidStatementDay, MinStatementDay, MaxStatementDay)
	}
	return nil
}

// NormalizeName valida e devolve o nome limpo junto da sua forma de
// comparação.
//
// A validação é feita sobre o texto JÁ normalizado quanto a espaços: um nome
// com só espaços é vazio, e não um nome de N caracteres.
func NormalizeName(raw string) (name, norm string, err error) {
	norm = textnorm.Normalize(raw)
	if norm == "" {
		return "", "", fmt.Errorf("%w: nome vazio", ErrInvalidName)
	}
	name = collapseSpaces(raw)
	if utf8.RuneCountInString(name) > MaxNameLen {
		return "", "", fmt.Errorf("%w: máximo de %d caracteres", ErrInvalidName, MaxNameLen)
	}
	return name, norm, nil
}

// ValidateAmount recusa valor fora da faixa de sanidade (S3: o teto existe
// tanto contra abuso quanto contra dedo errado). Zero e negativo são válidos
// para saldo de abertura — o cartão nasce devendo.
func ValidateAmount(cents int64) error {
	if cents > MaxAmountCents || cents < -MaxAmountCents {
		return fmt.Errorf("%w: máximo de %d centavos", ErrInvalidAmount, MaxAmountCents)
	}
	return nil
}

// ValidateOpeningDate recusa data zero e data fora da janela de sanidade
// (§4.5 do PLANOS.md: de 01/01/1970 a hoje + 10 anos). `hoje` é parâmetro
// porque "hoje" depende do fuso da casa, e este pacote não decide fuso.
func ValidateOpeningDate(d, hoje civil.Date) error {
	if d.IsZero() {
		return fmt.Errorf("%w: data ausente", ErrInvalidDate)
	}
	piso := civil.MustNew(1970, 1, 1)
	if d.Before(piso) {
		return fmt.Errorf("%w: anterior a %s", ErrInvalidDate, piso)
	}
	teto, err := civil.New(hoje.Year()+10, hoje.Month(), 1)
	if err != nil {
		return fmt.Errorf("%w: janela indeterminada", ErrInvalidDate)
	}
	if d.After(teto) {
		return fmt.Errorf("%w: mais de 10 anos no futuro", ErrInvalidDate)
	}
	return nil
}

// ValidateKeywords valida a lista de palavras-chave como veio do cliente e
// devolve as Keywords prontas para gravar — com Keyword (exibível), Norm e
// Position preenchidos; ID, HouseholdID, AccountID e CreatedAt são do
// serviço, nunca daqui.
//
// Ordem das regras: o teto (MaxKeywordsPerOwner) é conferido ANTES de olhar
// qualquer item, para uma lista enorme não custar validação item a item;
// depois cada item passa por textmatch.ValidateKeyword (2–40 runas,
// allowlist, ≥ 1 palavra útil — spec 0005 §4.1.2 e emenda §10.3); por fim a
// repetição dentro da própria lista, pela forma normalizada ("Nubank" e
// "nubank" são a mesma). O erro de item é um *KeywordValidationError com o
// índice; nenhum erro ecoa a palavra.
func ValidateKeywords(raw []string) ([]Keyword, error) {
	if len(raw) > MaxKeywordsPerOwner {
		return nil, fmt.Errorf("%w: máximo de %d", ErrTooManyKeywords, MaxKeywordsPerOwner)
	}
	out := make([]Keyword, 0, len(raw))
	vistas := make(map[string]struct{}, len(raw))
	for i, item := range raw {
		keyword, norm, err := textmatch.ValidateKeyword(item)
		if err != nil {
			return nil, &KeywordValidationError{Index: i, Err: fmt.Errorf("%w: %w", ErrInvalidKeyword, err)}
		}
		if _, repetida := vistas[norm]; repetida {
			return nil, &KeywordValidationError{Index: i, Err: ErrDuplicateKeyword}
		}
		vistas[norm] = struct{}{}
		out = append(out, Keyword{Keyword: keyword, Norm: norm, Position: i})
	}
	return out, nil
}

// collapseSpaces preserva a caixa e os acentos que o usuário escolheu, mas
// arruma o espaçamento — é o nome que ele vai VER, e "Conta  Corrente" com
// dois espaços é erro de digitação, não escolha.
func collapseSpaces(s string) string {
	campos := make([]rune, 0, len(s))
	espacoPendente := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			espacoPendente = len(campos) > 0
			continue
		}
		if espacoPendente {
			campos = append(campos, ' ')
			espacoPendente = false
		}
		campos = append(campos, r)
	}
	return string(campos)
}
