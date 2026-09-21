package category

import (
	"context"
	"errors"
	"time"
)

// ErrKeywordTaken — a palavra-chave já pertence a OUTRA categoria desta casa
// (spec 0005 §4.1, ADR-026d).
//
// Mora aqui, ao lado do contrato do repositório, porque tem DUAS origens: a
// pré-checagem do serviço via KeywordOwners (que sabe citar a dona) e o índice
// único do banco, quando duas edições disputam a mesma palavra ao mesmo tempo
// (ReplaceKeywords, sem dona). A mensagem NÃO contém a palavra: o erro passa
// pelo log do handler, e palavra-chave é dado da casa.
var ErrKeywordTaken = errors.New("palavra-chave já usada por outra categoria desta casa")

// MaxKeywordsPerOwner é o teto de palavras-chave por categoria (spec 0005
// §4.1). Vale também para conta; o valor é repetido lá para os dois pacotes
// não dependerem um do outro.
const MaxKeywordsPerOwner = 20

// Keyword é UMA palavra-chave de categoria (spec 0005, ADR-026d).
//
// Keyword é a forma EXIBÍVEL (como a pessoa digitou); Norm é a forma de
// comparação — textnorm.Normalize(Keyword) — e é ela que o índice único do
// banco compara, por casa. Os dois são derivados no serviço (S2: nada disso
// vem do cliente pronto).
type Keyword struct {
	ID          string
	HouseholdID string
	CategoryID  string
	Keyword     string
	Norm        string
	// Position é a ordem de cadastro (0..MaxKeywordsPerOwner-1): a lista
	// volta para a tela na ordem em que foi digitada.
	Position  int
	CreatedAt time.Time
}

// Repository persiste categorias. Implementação em gormstore (ADR-008).
//
// Como em account.Repository, todo método recebe householdID: a defesa contra
// BOLA fica na camada mais baixa, e categoria de outra casa não é retornável
// (docs/SEGURANCA.md §2).
type Repository interface {
	Create(ctx context.Context, c *Category) error

	// ByID devolve a categoria da casa. Outra casa, inexistente e excluída
	// produzem o mesmo ErrNotFound.
	ByID(ctx context.Context, householdID, id string) (*Category, error)

	// List devolve TODAS as categorias da casa (grupos e folhas) ordenadas
	// por nome normalizado. A árvore é montada no serviço, em memória: com
	// teto de 200 por casa, montar em Go é mais simples de auditar do que
	// qualquer consulta hierárquica — e é igual nos quatro dialetos.
	List(ctx context.Context, householdID string, includeArchived bool) ([]Category, error)

	// Children devolve as filhas diretas, incluindo arquivadas. Usado pelas
	// regras de exclusão e de arquivamento em cascata.
	Children(ctx context.Context, householdID, parentID string) ([]Category, error)

	Update(ctx context.Context, c *Category) error

	SoftDelete(ctx context.Context, householdID, id string, at time.Time) error

	// CountAll conta as não excluídas (grupos + folhas).
	CountAll(ctx context.Context, householdID string) (int64, error)

	// NameTaken informa se já existe categoria ATIVA com este nome
	// normalizado entre os IRMÃOS — mesmo parentID (nulo para grupos).
	//
	// parentID é *string porque o escopo dos grupos é justamente "parent_id
	// IS NULL", e um índice único sobre coluna anulável seria a armadilha P3
	// (o MSSQL trata NULLs como iguais e deixaria passar só um grupo por
	// casa). Por isso a unicidade é verificada aqui, em consulta, e não pelo
	// banco.
	NameTaken(ctx context.Context, householdID string, parentID *string, nameNorm, exceptID string) (bool, error)

	// --- palavras-chave (schema v4, spec 0005, ADR-026d) ---------------------

	// ListKeywords devolve TODAS as palavras-chave da casa, em UMA consulta,
	// ordenadas por (category_id, position, id).
	//
	// Uma consulta para a casa inteira, e não uma por categoria: o teto
	// natural é 20 × 200 = 4.000 linhas, e é o classificador (internal/classify)
	// quem descarta, em Go, a palavra cuja dona está arquivada ou excluída.
	ListKeywords(ctx context.Context, householdID string) ([]Keyword, error)

	// KeywordOwners devolve, para cada norm informada que já existe na casa,
	// a categoria dona: norm -> categoryID. Norm ausente do mapa está livre.
	//
	// Inclui a PRÓPRIA categoria que está sendo editada — quem chama ignora
	// ownerID == categoryID. É a pré-checagem do 409 KEYWORD_TAKEN com dona;
	// a corrida rara que escapa dela é decidida pelo índice único em
	// ReplaceKeywords. IN fatiado; norm de outra casa nunca volta.
	KeywordOwners(ctx context.Context, householdID string, norms []string) (map[string]string, error)

	// ReplaceKeywords SUBSTITUI a lista da categoria: apaga as atuais e grava
	// as novas, na transação em curso (quem chama abre o UnitOfWork).
	//
	// Cada Keyword precisa vir com ID, Keyword e Norm preenchidos (ids são
	// gerados no serviço, nunca aqui); HouseholdID e CategoryID, quando
	// preenchidos, têm de bater com os argumentos — divergência derruba a
	// escrita inteira. Violação do índice único (household_id, keyword_norm)
	// volta como ErrKeywordTaken.
	ReplaceKeywords(ctx context.Context, householdID, categoryID string, kws []Keyword) error

	// DeleteKeywords apaga fisicamente as palavras-chave da categoria. É
	// chamado pela exclusão da categoria, na mesma transação (ADR-013: sem FK
	// física, é o UnitOfWork que mantém a integridade).
	DeleteKeywords(ctx context.Context, householdID, categoryID string) error
}

// LiveState é o estado ATUAL de uma categoria VIVA, relido para reconferir uma
// escrita em massa no instante em que ela vai acontecer.
//
// Existe porque escrita em massa calcula fora da transação e grava dentro dela
// (ADR-029h, achado A2 da revisão da E6): entre o cálculo e o UPDATE cabe uma
// requisição inteira, e as três coisas abaixo são exatamente as que podem ter
// mudado nesse intervalo e que decidem se a escrita ainda é a que foi
// prometida.
//
// Ele NÃO é a categoria inteira de propósito: quem reconfere não precisa de
// nome, de datas nem de palavra-chave, e devolver a entidade completa
// convidaria alguém a tomar outra decisão com dado relido pela metade.
type LiveState struct {
	ID string

	// Kind é a natureza ATUAL. É o campo que faltava na primeira versão desta
	// reconferência, e a falta era explorável: trocar a natureza DENTRO do
	// mesmo lado do dinheiro é permitido mesmo com a categoria em uso
	// (ADR-029c), então uma categoria de `investment` pode virar `expense` na
	// janela — e quem gravasse sem reler passaria a escrever categoria COMUM
	// por cima de categoria comum, que é recategorização em massa, fora do
	// escopo da spec 0006 §2.2.
	Kind string

	// HasActiveChild indica GRUPO com ao menos uma subcategoria ATIVA, que não
	// recebe lançamento (spec 0005 §12/§13). Filha arquivada não conta: grupo
	// cujas filhas foram todas arquivadas volta a ser destino legítimo.
	HasActiveChild bool

	// Archived indica categoria ARQUIVADA.
	//
	// Quem lê este campo precisa da distinção que o produto faz, e ela é sutil:
	//
	//   - MARCAÇÃO EXISTENTE sobrevive ao arquivamento. Categoria arquivada
	//     continua contando nos relatórios e o lançamento que já aponta para
	//     ela continua apontando (PLANOS.md §4.4). É por isso que a allowlist
	//     do `overwriteCategorized` precisa ALCANÇAR a categoria de despesa
	//     arquivada que a linha tem hoje;
	//   - ATRIBUIÇÃO NOVA, não. `transaction.ErrCategoryArchived` recusa
	//     categoria arquivada no PATCH de uma linha, no lote e na importação.
	//
	// Uma escrita em massa que ignorasse isto seria a única porta do produto a
	// atribuir onde as outras três recusam.
	Archived bool
}

// DestinoAindaQualifica responde se a categoria RELIDA ainda pode receber uma
// ATRIBUIÇÃO NOVA de lançamento do lado do dinheiro informado.
//
// POR QUE ISTO É UMA FUNÇÃO, E AQUI. A pergunta é feita por toda escrita em
// MASSA que calcula fora da transação e grava dentro dela (ADR-029h): hoje
// POST /transactions/auto-categorize e POST /investments/detect, amanhã a
// próxima. Ela já esteve escrita DUAS vezes, uma em cada rota, e as duas
// cópias JÁ DIVERGIRAM — o qualificador do arquivamento entrou numa das rotas
// numa rodada de revisão e só chegou à outra na rodada seguinte, deixando uma
// delas atribuindo a categoria arquivada enquanto a outra recusava.
//
// É exatamente o que o doc de AceitaLancamento (logo abaixo, em types.go) já
// advertia sobre a regra de pareamento: "duas cópias de uma allowlist divergem;
// a segunda é sempre a esquecida". Vale igual para a qualificação do destino, e
// por isso ela mora aqui, junto de LiveState, que é o dado que ela lê.
//
// OS QUATRO EIXOS, e por que cada um reprova:
//
//   - `encontrada` false: a categoria não voltou de LiveStates, ou seja, foi
//     EXCLUÍDA (ou é de outra casa). O `inUse` do DELETE não encontra uso
//     nenhum enquanto a escrita em massa ainda não gravou, então a exclusão é
//     aceita bem no meio da janela; gravar depois penduraria o lote numa
//     categoria que não existe — o estado que ErrInUse existe para impedir;
//   - ARQUIVADA. Aqui mora a distinção que o produto faz, e ela é sutil:
//     MARCAÇÃO EXISTENTE sobrevive ao arquivamento (o lançamento que já aponta
//     continua apontando e a categoria continua contando nos relatórios,
//     PLANOS.md §4.4), mas ATRIBUIÇÃO NOVA não — transaction.ErrCategoryArchived
//     recusa no PATCH de uma linha, no lote e no confirm da importação. Uma
//     escrita em massa que gravasse seria a única porta do produto marcando
//     onde as outras recusam;
//   - GRUPO com subcategoria ATIVA não recebe lançamento (spec 0005 §12/§13);
//   - a NATUREZA relida tem de aceitar o lado do dinheiro do lote, por
//     AceitaLancamento — a única fonte da verdade do pareamento (ADR-029b).
//     Trocar de natureza CRUZANDO o lado é aceito numa categoria de topo, sem
//     filhas e sem uso, que é justamente o estado da categoria recém-criada
//     cujo lote a auto-categorização calcula.
//
// `ladoEsperado` é o `kind` do LANÇAMENTO que o plano destinou a esta categoria
// ("income"/"expense"), e vem do PLANO, nunca da categoria relida: é a natureza
// dela que pode ter mudado. Lado vazio ou desconhecido reprova — sem saber o
// que se está gravando, o desfecho seguro é não gravar.
//
// O que NÃO está aqui: a exigência EXTRA de cada rota. O `detect` de
// investimentos só aceita destino de natureza `investment`/`redemption`, e essa
// pergunta é dele; esta função responde o que as duas rotas têm em comum, e
// cada uma soma o seu por fora. Misturar as duas coisas devolveria o problema
// que ela existe para resolver.
//
// Os quatro eixos são if's separados de propósito. Fundidos numa expressão só,
// um deles pode ser removido sem que teste nenhum perceba porque outro o
// mascara — foi o que a verificação por mutação encontrou na primeira versão
// deste predicado, em que apagar o eixo da exclusão não quebrava nada (a
// excluída volta como zero value, de `Kind` vazio, e o eixo da natureza já
// reprovava o vazio).
func DestinoAindaQualifica(st LiveState, encontrada bool, ladoEsperado string) bool {
	if !encontrada {
		return false
	}
	if st.Archived {
		return false
	}
	if st.HasActiveChild {
		return false
	}
	return AceitaLancamento(ladoEsperado, st.Kind)
}

// UsageChecker informa se a categoria tem dado dependente que impeça exclusão.
//
// Mesma razão de existir de account.UsageChecker: quem sabe responder muda a
// cada entrega (E2 lançamentos, E3 contas fixas, E5 orçamentos), e este pacote
// não precisa conhecer nenhum deles.
//
// A checagem de FILHAS não passa por aqui — ela é do próprio domínio de
// categoria e está no serviço.
type UsageChecker interface {
	CategoryInUse(ctx context.Context, householdID, categoryID string) (bool, error)
}
