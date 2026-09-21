// Package category modela categoria de receita, despesa, aporte e resgate.
//
// A natureza (`kind`) é o que marca um lançamento como investimento: não há
// tipo de lançamento novo nem conta de carteira (ADR-029a). É também aqui que
// mora a ÚNICA regra de pareamento entre lançamento e categoria
// (AceitaLancamento) — ela já esteve escrita em dois lugares e divergiu.
//
// A árvore tem EXATAMENTE dois níveis — grupo e subcategoria (ADR-017b).
// Profundidade arbitrária exigiria CTE recursiva, cujo suporte e sintaxe
// variam entre os quatro dialetos SQL suportados; é justamente o tipo de
// construção que quebra o requisito multi-banco. Com dois níveis, toda
// consulta é plana.
package category

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// Natureza da categoria. Conjunto FECHADO de QUATRO valores, organizados em
// dois LADOS DO DINHEIRO (ADR-029a):
//
//	| lado           | naturezas               | lançamento que a aceita |
//	|----------------|-------------------------|-------------------------|
//	| sai da conta   | expense, investment     | expense                 |
//	| entra na conta | income, redemption      | income                  |
//
// `investment` (aporte) e `redemption` (resgate) NÃO criam tipo de lançamento
// novo: o aporte continua sendo dinheiro que sai da conta e o saldo derivado
// (ADR-017) não muda uma linha. As duas palavras têm exatamente 10 caracteres
// e cabem no varchar(10) que já existe — o schema não muda de versão.
const (
	KindIncome     = "income"
	KindExpense    = "expense"
	KindInvestment = "investment"
	KindRedemption = "redemption"
)

// Os dois `kind` de LANÇAMENTO que aceitam categoria. Repetidos como literal
// aqui, e NÃO importados de internal/transaction, porque aquele pacote importa
// este: a dependência inversa seria ciclo. A igualdade das strings é travada
// por teste nos dois lados (AceitaLancamento).
const (
	txKindIncome  = "income"
	txKindExpense = "expense"
)

// Limites de domínio (§4.5 do PLANOS.md).
const (
	// MaxNameLen é medido em runas.
	MaxNameLen = 60

	// MaxPerHousehold conta grupos e folhas juntos, entre as não excluídas.
	MaxPerHousehold = 200

	// MaxDepth existe como constante para que a regra seja legível no código
	// e não um "if parent.ParentID != nil" solto.
	MaxDepth = 2
)

// Erros de domínio.
var (
	// ErrNotFound — categoria inexistente OU de outra casa. Um erro só, de
	// propósito: distinguir confirmaria a existência do recurso alheio (S1).
	ErrNotFound = errors.New("categoria não encontrada")

	// ErrNameTaken — já existe categoria ativa com este nome entre os irmãos.
	ErrNameTaken = errors.New("já existe uma categoria com este nome aqui")

	// ErrInvalidKind — natureza fora da allowlist.
	ErrInvalidKind = errors.New("natureza de categoria inválida")

	// ErrInvalidName — nome vazio ou longo demais.
	ErrInvalidName = errors.New("nome de categoria inválido")

	// ErrTooDeep — tentativa de criar um terceiro nível.
	ErrTooDeep = errors.New("categoria só aceita dois níveis")

	// ErrTooMany — teto de categorias por casa atingido.
	ErrTooMany = errors.New("limite de categorias por casa atingido")

	// ErrInUse — tem filhas, lançamento, conta fixa ou orçamento.
	ErrInUse = errors.New("categoria em uso")

	// ErrTooManyToCheck — pediram a reconferência de mais ids do que a
	// taxonomia de uma casa comporta (MaxPerHousehold).
	//
	// Falha ALTO e FECHADO, como ErrTooManyCategories do lado dos lançamentos
	// (ADR-029 j.2): o conjunto NÃO é fatiado, porque fatiar uma reconferência
	// significaria aprovar uma parte dos destinos sem ter olhado o resto. Passar
	// do teto significa que a taxonomia da casa foi violada — 500 genérico, com
	// a contagem no log e nunca os ids.
	ErrTooManyToCheck = errors.New("categorias demais para reconferir")

	// ErrKindLocked — não dá para mudar a natureza de um grupo que já tem
	// filhas ou uso: mudaria o significado do dado já registrado.
	ErrKindLocked = errors.New("natureza não pode mudar com a categoria em uso")

	// ErrParentImmutable — mover categoria de lugar não existe no v1.
	ErrParentImmutable = errors.New("categoria não muda de grupo")

	// ErrParentArchived — desarquivar folha cujo grupo está arquivado
	// produziria uma folha ativa pendurada num grupo invisível.
	ErrParentArchived = errors.New("o grupo desta categoria está arquivado")

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
	ErrTooManyKeywords = errors.New("limite de palavras-chave por categoria excedido")

	// ErrKeywordsOnGroupWithChildren — lista NÃO vazia de palavras-chave num
	// grupo que tem subcategoria ativa (spec 0005 §12). Grupo com filha ativa
	// não recebe lançamento diretamente, então palavra-chave nele nunca
	// sugeriria nada: é 400 em `fields.keywords`. `[]` (limpar) continua
	// aceito. A mensagem não carrega palavra nenhuma.
	ErrKeywordsOnGroupWithChildren = errors.New("palavras-chave ficam nas subcategorias")
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
// categoria DA MESMA CASA (spec 0005 §4.1.2). É a ÚNICA forma em que a
// colisão sai do serviço — Create e Update nunca devolvem ErrKeywordTaken
// cru, porque o contrato exige `fields.keyword`.
//
// Keyword é a forma exibível que o próprio cliente enviou e volta em
// `fields.keyword`; OwnerID é a categoria da casa do token que já a tem (a
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

// Category é a categoria.
type Category struct {
	ID          string
	HouseholdID string
	ParentID    *string
	Name        string
	NameNorm    string
	Kind        string
	ArchivedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	DeletedAt   *time.Time
}

// IsGroup informa se a categoria é de nível 1.
func (c Category) IsGroup() bool { return c.ParentID == nil }

// View é uma categoria no formato da API. DTO próprio (docs/SEGURANCA.md §4).
type View struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Kind     string  `json:"kind"`
	ParentID *string `json:"parentId"`
	// Keywords são as palavras-chave na forma exibível, na ordem de cadastro
	// (spec 0005 §4.1). `required` no contrato: sempre presente, `[]` quando
	// vazio — nunca null.
	Keywords   []string `json:"keywords"`
	ArchivedAt *string  `json:"archivedAt"`
	CreatedAt  string   `json:"createdAt"`
	UpdatedAt  string   `json:"updatedAt"`
	// Children só é preenchido nos grupos, na resposta em árvore. Fica como
	// slice sempre inicializado para que o JSON traga [] em vez de null — a
	// tela não precisa de um caso a mais.
	Children []View `json:"children"`
}

// ListView é a resposta de GET /categories: a árvore separada por natureza,
// que é como as telas usam (nunca se mostra receita, despesa, aporte e resgate
// misturados).
//
// Os QUATRO arrays vêm sempre presentes — `[]` quando vazios, nunca `null` —
// porque o contrato (`CategoryTree`) os marca como `required` e a tela não
// distingue "ausente" de "nenhuma".
type ListView struct {
	Income     []View `json:"income"`
	Expense    []View `json:"expense"`
	Investment []View `json:"investment"`
	Redemption []View `json:"redemption"`
}

// ValidKind informa se a natureza pertence à allowlist FECHADA.
func ValidKind(kind string) bool {
	switch kind {
	case KindIncome, KindExpense, KindInvestment, KindRedemption:
		return true
	default:
		return false
	}
}

// AceitaLancamento é a ÚNICA fonte da verdade do pareamento entre o tipo de um
// lançamento e a natureza da categoria que ele pode receber (ADR-029b).
//
// Despesa aceita `expense` ou `investment`; receita aceita `income` ou
// `redemption`; transferência (e qualquer outro valor, inclusive vazio) não
// aceita natureza nenhuma — perna de transferência nunca tem categoria
// (ADR-016).
//
// Ela mora aqui, e não em quem chama, porque a regra já esteve escrita DUAS
// vezes (`transaction.categoriaCombina` e `importer.categoriaCombinaComALinha`)
// e duas cópias de uma allowlist divergem: a segunda é sempre a esquecida — e
// seria por ela que uma despesa ganharia categoria de resgate. Os dois
// chamadores agora delegam.
//
// A ordem dos argumentos importa e é a do nome: primeiro o tipo do LANÇAMENTO,
// depois a natureza da CATEGORIA. Trocá-los faz a função responder false para
// tudo (nenhuma natureza de categoria é "income"/"expense" do lado do
// lançamento além das duas homônimas), o que é o desfecho seguro — mas os
// testes de tabela travam os dois sentidos.
func AceitaLancamento(transactionKind, categoryKind string) bool {
	switch transactionKind {
	case txKindIncome:
		return categoryKind == KindIncome || categoryKind == KindRedemption
	case txKindExpense:
		return categoryKind == KindExpense || categoryKind == KindInvestment
	default:
		return false
	}
}

// mesmoLadoDoDinheiro informa se duas naturezas ficam do MESMO lado do caixa —
// o que decide se a troca de natureza é permitida com a categoria em uso
// (ADR-029c).
//
// Dentro do mesmo lado nenhum lançamento existente muda de sinal, de conta ou
// de saldo, e todos continuam válidos contra AceitaLancamento: a troca é de
// rótulo de intenção, não de fato financeiro. Cruzar o lado transformaria
// despesa registrada em receita e deixaria TODO lançamento da categoria em
// violação do pareamento no instante seguinte.
//
// Natureza fora da allowlist não tem lado, então nunca é "o mesmo" de nada —
// nem de si mesma. Quem chama valida com ValidKind antes.
func mesmoLadoDoDinheiro(a, b string) bool {
	ladoA, okA := ladoDoDinheiro(a)
	ladoB, okB := ladoDoDinheiro(b)
	return okA && okB && ladoA == ladoB
}

// ladoDoDinheiro devolve o `kind` de LANÇAMENTO que aceita esta natureza —
// que é justamente o nome do lado. Natureza inválida devolve false.
func ladoDoDinheiro(kind string) (string, bool) {
	switch kind {
	case KindIncome, KindRedemption:
		return txKindIncome, true
	case KindExpense, KindInvestment:
		return txKindExpense, true
	default:
		return "", false
	}
}

// NormalizeName valida e devolve o nome limpo com a sua forma de comparação.
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

// ValidateKeywords valida a lista de palavras-chave como veio do cliente e
// devolve as Keywords prontas para gravar — com Keyword (exibível), Norm e
// Position preenchidos; ID, HouseholdID, CategoryID e CreatedAt são do
// serviço, nunca daqui.
//
// Ordem das regras: o teto (MaxKeywordsPerOwner) é conferido ANTES de olhar
// qualquer item, para uma lista enorme não custar validação item a item;
// depois cada item passa por textmatch.ValidateKeyword (2–40 runas,
// allowlist, ≥ 1 palavra útil — spec 0005 §4.1.2 e emenda §10.3); por fim a
// repetição dentro da própria lista, pela forma normalizada ("Padaria" e
// "padaria" são a mesma). O erro de item é um *KeywordValidationError com o
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

func collapseSpaces(s string) string {
	out := make([]rune, 0, len(s))
	espacoPendente := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			espacoPendente = len(out) > 0
			continue
		}
		if espacoPendente {
			out = append(out, ' ')
			espacoPendente = false
		}
		out = append(out, r)
	}
	return string(out)
}
