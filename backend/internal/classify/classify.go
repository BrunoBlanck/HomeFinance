// Package classify é a COLA entre as palavras-chave guardadas no banco e o
// motor de correspondência de internal/textmatch (spec 0005 §3 e §4.2.1,
// ADR-026). É ele que decide QUAIS palavras-chave entram no matcher — e é essa
// decisão, não a pontuação, que carrega as regras de negócio.
//
// # Por que um pacote à parte
//
// textmatch é folha de propósito: só sabe pontuar palavras contra descrições e
// não conhece categoria, conta, casa nem arquivamento. Os dois consumidores
// (a análise da importação e o auto-categorize de lançamentos) precisam da
// MESMA seleção: só categoria ATIVA, da MESMA natureza da linha, que possa
// receber um lançamento; só conta ATIVA, nunca a conta do lote. Escrever essa
// seleção uma vez, aqui, é o que garante que a prévia da importação e a
// categorização em massa nunca discordem sobre quem pode ser sugerido.
//
// # O que fica de fora do matcher, e onde
//
// A palavra-chave de dona arquivada ou excluída é descartada AQUI, antes de o
// matcher existir: ele nunca a vê, então não há como uma exclusão esquecida em
// algum chamador ressuscitá-la. O mesmo vale para a natureza — há um matcher
// por LADO DO DINHEIRO (ADR-029g): o de entrada carrega `income` e
// `redemption`, o de saída carrega `expense` e `investment`, e uma linha
// `income` só consulta o de entrada. São DOIS matchers e UMA passagem, não
// quatro matchers nem dois passes: o limiar, o desempate e a regra de
// ambiguidade do ADR-026(a) só valem se a escolha for uma.
// A conta do lote, ao contrário, é excluída NA ESCOLHA (textmatch.Best com
// exclude), porque o conjunto é carregado uma vez por casa e a conta muda por
// lote.
//
// # A dupla semântica da palavra-chave de conta (ADR-028b)
//
// A mesma palavra de conta é lida de dois jeitos, por dois consumidores:
//
//   - a ANÁLISE DA IMPORTAÇÃO (SuggestCounterpart) exclui a conta do lote
//     antes da escolha — a palavra que casa nomeia a CONTRAPARTE, e a conta do
//     lote nunca é contraparte de si mesma (ADR-026e);
//   - o REPROCESSAMENTO DE TRANSFERÊNCIAS (MatchAccount) escolhe entre TODAS
//     as contas ativas, sem excluir nenhuma, e é o chamador que compara a dona
//     com a conta da linha: outra conta → contraparte conhecida; a própria →
//     marcador "isto é transferência entre as minhas contas", contraparte
//     desconhecida (spec 0005 §13). "Pix enviado - FULANO" não nomeia o banco
//     de destino, e a pessoa cadastra a palavra na conta onde o texto APARECE
//     — que a primeira semântica exclui.
//
// As duas existem só aqui, uma por método, para nenhum chamador obter a outra
// por truque de argumento vazio. Unificá-las na importação é backlog (ADR-028g).
//
// # Categoria atribuível
//
// Só entra no matcher a categoria que a tela oferece no seletor de um
// lançamento: a FOLHA, ou o GRUPO sem filhas ativas
// (frontend/src/lib/categories.ts, opcoesDeCategoria). O backend, na escrita
// (internal/transaction/service.go, CreateBatch), exige casa, não arquivada e
// natureza compatível — e não tem regra de nível; esta é a mais restritiva das
// duas, para a sugestão nunca apontar algo que a pessoa não conseguiria
// escolher à mão.
//
// # Isolamento
//
// Load recebe o householdID do TOKEN e o repassa às quatro consultas tal qual;
// os repositórios filtram por casa na camada mais baixa (docs/SEGURANCA.md §2).
// Nada de outra casa chega ao Set — o teste de isolamento confere isso pelas
// chamadas às fontes, não só pelo resultado.
package classify

import (
	"context"
	"fmt"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
)

// CategorySource é o que o classificador precisa saber de categoria. Interface
// mínima declarada no consumidor; category.Repository a satisfaz.
type CategorySource interface {
	// List devolve as categorias da casa; com includeArchived=false, só as
	// ativas. O loader chama com false.
	List(ctx context.Context, householdID string, includeArchived bool) ([]category.Category, error)
	// ListKeywords devolve TODAS as palavras-chave de categoria da casa, em
	// uma consulta.
	ListKeywords(ctx context.Context, householdID string) ([]category.Keyword, error)
}

// AccountSource é o que o classificador precisa saber de conta.
// account.Repository a satisfaz.
type AccountSource interface {
	List(ctx context.Context, householdID string, includeArchived bool) ([]account.Account, error)
	ListKeywords(ctx context.Context, householdID string) ([]account.Keyword, error)
}

// Loader carrega, para UMA casa, o conjunto de palavras-chave utilizável e o
// entrega pronto para consulta. É construído uma vez em cmd/api e
// compartilhado pelos serviços; não guarda estado entre chamadas.
type Loader struct {
	categories CategorySource
	accounts   AccountSource

	// work é o orçamento de trabalho que cada Set carrega. Campo, e não a
	// constante direta, só para o teste conseguir exercitar o caminho do
	// estouro sem montar 5.000 palavras-chave de verdade.
	work int64
}

// Option configura o Loader.
type Option func(*Loader)

// WithWorkBudget troca o orçamento de trabalho de cada Set carregado (teste).
// Valor menor que 1 mantém textmatch.MaxMatchWork — nenhum caminho consegue
// desligar o teto.
//
// A opção só APERTA: o valor é limitado por textmatch.MaxMatchWork (achado A7
// da revisão de segurança). Uma opção de teste que conseguisse afrouxar o teto
// de produção seria um teto que depende de ninguém chamá-la errado, e a
// montagem de produção fica em cmd/api, longe de quem lê este arquivo.
func WithWorkBudget(work int64) Option {
	return func(l *Loader) {
		if work >= 1 {
			l.work = min(work, textmatch.MaxMatchWork)
		}
	}
}

// NewLoader monta o loader. As duas fontes são posicionais de propósito:
// esquecer uma vira erro de compilação, não um matcher silenciosamente vazio.
func NewLoader(categories CategorySource, accounts AccountSource, opts ...Option) *Loader {
	l := &Loader{categories: categories, accounts: accounts, work: textmatch.MaxMatchWork}
	for _, o := range opts {
		o(l)
	}
	return l
}

// Load faz exatamente QUATRO consultas — categorias ativas, palavras de
// categoria, contas ativas, palavras de conta — e monta o Set da casa.
//
// A palavra cuja dona não está entre as ativas (arquivada ou excluída) é
// descartada aqui; a de categoria vai para o matcher da natureza do Kind da
// dona; a de grupo com filhas ativas fica de fora (ver o doc do pacote). Um
// Set sem palavra nenhuma é válido: toda consulta responde below_threshold.
//
// Palavra gravada que o matcher recusa (não deveria existir: ValidateKeyword
// barra na borda) devolve erro embrulhado SEM a palavra — o erro passa pelo
// log do handler, e palavra-chave é dado da casa.
func (l *Loader) Load(ctx context.Context, householdID string) (*Set, error) {
	categorias, err := l.categories.List(ctx, householdID, false)
	if err != nil {
		return nil, fmt.Errorf("listing active categories: %w", err)
	}
	palavrasDeCategoria, err := l.categories.ListKeywords(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("listing category keywords: %w", err)
	}
	contas, err := l.accounts.List(ctx, householdID, false)
	if err != nil {
		return nil, fmt.Errorf("listing active accounts: %w", err)
	}
	palavrasDeConta, err := l.accounts.ListKeywords(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("listing account keywords: %w", err)
	}

	set := &Set{categoryNames: make(map[string]string, len(categorias))}

	// Índice das categorias ATIVAS e contagem de filhas ativas por grupo. O
	// repositório já filtra arquivada e excluída quando includeArchived é
	// false; a reconferência em Go é defesa em profundidade — custa nada e
	// não depende de a implementação da fonte continuar igual.
	ativas := make(map[string]category.Category, len(categorias))
	filhasAtivas := make(map[string]int)
	for _, c := range categorias {
		if !ativa(c.ArchivedAt, c.DeletedAt) {
			continue
		}
		ativas[c.ID] = c
		set.categoryNames[c.ID] = c.Name
		if c.ParentID != nil {
			filhasAtivas[*c.ParentID]++
		}
	}

	// atribuiveis é o MESMO filtro que a palavra-chave sofre logo abaixo —
	// ativa e não-grupo-com-filhas —, só que indexado por id e guardando a
	// natureza.
	//
	// Ele existe porque o confirm da importação precisa perguntar POR ID se a
	// categoria que a análise sugeriu ainda pode receber lançamento (spec 0005
	// §13, "Correção de robustez"). `categoryNames` não serve para isso: ele
	// nomeia toda categoria ativa, inclusive o grupo com filhas ativas — que é
	// justamente o gatilho novo da §13.
	set.atribuiveis = make(map[string]string, len(ativas))
	for id, c := range ativas {
		// A casa é reconferida aqui, e não só na fonte: diferente dos
		// matchers, este mapa responde a uma pergunta de AUTORIZAÇÃO — "esta
		// categoria, cujo id veio de fora, é minha e ainda vale?". Uma fonte
		// que vazasse outra casa aprovaria o id alheio. Mesma defesa em
		// profundidade de daCasa(), abaixo.
		if !daCasa(c.HouseholdID, householdID) {
			continue
		}
		if c.IsGroup() && filhasAtivas[id] > 0 {
			continue
		}
		set.atribuiveis[id] = c.Kind
	}

	// DOIS matchers, por LADO DO DINHEIRO — não quatro, um por natureza
	// (ADR-029g). O de entrada carrega as palavras de `income` E de
	// `redemption`; o de saída, as de `expense` E de `investment`.
	//
	// Dois matchers e não dois passes: o limiar 80, o desempate e a regra
	// "empate → nenhuma sugestão" do ADR-026(a) só valem se a escolha for
	// UMA. Com um passe por natureza, "CDB" (investimento, 88) e "Mercado"
	// (despesa, 88) produziriam dois vencedores e alguém teria de inventar um
	// critério entre passes — exatamente a ambiguidade que a regra existe
	// para recusar.
	var entrada, saida []textmatch.Keyword
	for _, kw := range palavrasDeCategoria {
		if !daCasa(kw.HouseholdID, householdID) {
			continue
		}
		dona, ok := ativas[kw.CategoryID]
		if !ok {
			continue // arquivada ou excluída: o matcher nunca a vê
		}
		if dona.IsGroup() && filhasAtivas[dona.ID] > 0 {
			continue // grupo com filhas ativas não recebe lançamento
		}
		item := textmatch.Keyword{OwnerID: kw.CategoryID, Keyword: kw.Keyword, Norm: kw.Norm}
		switch dona.Kind {
		case category.KindIncome, category.KindRedemption:
			entrada = append(entrada, item)
		case category.KindExpense, category.KindInvestment:
			saida = append(saida, item)
		}
		// Natureza fora da allowlist não existe (ValidKind na borda); se
		// existisse, ficar de fora dos dois matchers é o comportamento seguro
		// — é por isso que este switch é fechado e não tem default que
		// "chute" um dos lados.
	}

	contasAtivas := make(map[string]struct{}, len(contas))
	for _, a := range contas {
		if ativa(a.ArchivedAt, a.DeletedAt) {
			contasAtivas[a.ID] = struct{}{}
		}
	}
	var deConta []textmatch.Keyword
	for _, kw := range palavrasDeConta {
		if !daCasa(kw.HouseholdID, householdID) {
			continue
		}
		if _, ok := contasAtivas[kw.AccountID]; !ok {
			continue
		}
		deConta = append(deConta, textmatch.Keyword{OwnerID: kw.AccountID, Keyword: kw.Keyword, Norm: kw.Norm})
	}

	// UM orçamento de trabalho para os TRÊS matchers (achado A1 da revisão de
	// segurança). Compartilhado, e não um por matcher, porque o teto tem de
	// ser o da OPERAÇÃO: um Set é carregado uma vez por análise de importação,
	// por auto-categorização ou por reprocessamento de transferências, e três
	// orçamentos independentes fariam o teto real ser o triplo do declarado.
	set.budget = textmatch.NewBudget(l.work)

	if set.inflow, err = matcher(entrada, set.budget); err != nil {
		return nil, fmt.Errorf("building inflow category matcher: %w", err)
	}
	if set.outflow, err = matcher(saida, set.budget); err != nil {
		return nil, fmt.Errorf("building outflow category matcher: %w", err)
	}
	if set.accounts, err = matcher(deConta, set.budget); err != nil {
		return nil, fmt.Errorf("building account matcher: %w", err)
	}
	return set, nil
}

// matcher constrói o Matcher, ou devolve nil quando não há palavra: o Matcher
// nulo é seguro (responde below_threshold) e evita três índices vazios por
// casa sem palavra-chave nenhuma.
func matcher(kws []textmatch.Keyword, budget *textmatch.Budget) (*textmatch.Matcher, error) {
	if len(kws) == 0 {
		return nil, nil
	}
	return textmatch.NewMatcher(kws, textmatch.WithBudget(budget))
}

// ativa diz se o dono pode participar da correspondência: nem arquivado nem
// excluído.
func ativa(archivedAt, deletedAt *time.Time) bool {
	return archivedAt == nil && deletedAt == nil
}

// daCasa reconfere o household_id da palavra contra o pedido. A coluna é
// repetida na tabela de palavras-chave justamente para o isolamento nunca
// depender de join (ADR-026d); aqui ela é lida de novo, como defesa em
// profundidade — uma fonte que vazasse outra casa não chegaria ao matcher.
func daCasa(palavraHousehold, householdID string) bool {
	return palavraHousehold == householdID
}

// Set é o conjunto de palavras-chave de UMA casa, pronto para consulta. Três
// matchers independentes — entrada, saída e conta — porque o LADO DO DINHEIRO
// da linha decide qual deles é consultado, e conta é conjunto separado de
// categoria (spec 0005 §4.1). Seguro para uso concorrente; o zero value e o
// ponteiro nulo respondem "sem sugestão" a tudo.
type Set struct {
	// inflow são as palavras das categorias que um lançamento de RECEITA pode
	// receber: natureza `income` ou `redemption`. outflow, as de DESPESA:
	// `expense` ou `investment` (ADR-029g). Os nomes falam do lado do caixa,
	// e não da natureza, justamente para não sugerirem "um matcher por
	// natureza" a quem for mexer aqui.
	inflow        *textmatch.Matcher
	outflow       *textmatch.Matcher
	accounts      *textmatch.Matcher
	categoryNames map[string]string

	// atribuiveis é id -> natureza das categorias que podem RECEBER lançamento
	// diretamente. Subconjunto de categoryNames: fica de fora o grupo com
	// subcategoria ativa (spec 0005 §13).
	atribuiveis map[string]string

	// budget é o teto de trabalho da OPERAÇÃO, compartilhado pelos três
	// matchers. Ver Load.
	budget *textmatch.Budget
}

// SuggestCategory aplica a escolha da §3 contra as categorias ativas do LADO
// DO DINHEIRO do lançamento: `expense` concorre contra `expense` +
// `investment`, `income` contra `income` + `redemption`. Qualquer outro kind —
// transferência, vazio, inválido — responde below_threshold: transferência não
// tem categoria, e nunca se sugere despesa para receita.
//
// **Os ARGUMENTOS não mudaram com o ADR-029, e é isso que faz a análise da
// importação (spec 0005 §4.2) e o auto-categorize ganharem aporte e resgate
// sem uma linha de código nova.** O argumento é o `kind` do LANÇAMENTO
// (`income`/`expense`, os mesmos valores de transaction.Kind*), nunca a
// natureza de uma categoria: passar `investment` aqui não casa com lado
// nenhum e responde sem sugestão, que é o desfecho seguro.
//
// A escolha do matcher é DERIVADA de category.AceitaLancamento, e não de um
// switch próprio, para que a seleção nunca possa divergir da regra de
// pareamento que o serviço aplica na escrita (ADR-029b): sugerir o que a
// escrita vai recusar é prometer o que não se cumpre. `classify` não pode
// importar `transaction` (é `transaction` quem importa `classify`), então a
// pergunta é feita pelo lado da categoria.
//
// O ERRO é textmatch.ErrWorkBudgetExceeded e só ele: a operação passou do
// orçamento de trabalho (achado A1). Quem o recebe ABORTA a operação — não
// existe "classificar o resto sem sugestão", porque isso seria um resultado
// truncado em silêncio.
func (s *Set) SuggestCategory(kind, descriptionNorm string) (textmatch.Result, error) {
	if s == nil {
		return semSugestao(), nil
	}
	switch {
	case category.AceitaLancamento(kind, category.KindIncome):
		return s.inflow.Best(descriptionNorm)
	case category.AceitaLancamento(kind, category.KindExpense):
		return s.outflow.Best(descriptionNorm)
	default:
		return semSugestao(), nil
	}
}

// SuggestCounterpart aplica a escolha da §3 contra as contas ativas da casa,
// EXCLUINDO a conta do lote ANTES da escolha: se ela tem a melhor pontuação,
// vence a segunda (e o empate é medido entre as que sobram). A conta do lote
// nunca é contraparte de si mesma.
func (s *Set) SuggestCounterpart(excludeAccountID, descriptionNorm string) (textmatch.Result, error) {
	if s == nil {
		return semSugestao(), nil
	}
	return s.accounts.Best(descriptionNorm, excludeAccountID)
}

// MatchAccount aplica a escolha da §3 contra TODAS as contas ativas da casa,
// sem excluir nenhuma — é a consulta do reprocessamento de transferências
// (spec 0005 §13, ADR-028b), em que a palavra-chave de conta tem DUAS
// semânticas e quem as distingue é o chamador, comparando a dona da melhor
// pontuação com a conta da linha: outra conta → contraparte conhecida; a
// própria → marcador "isto é transferência entre as minhas contas".
//
// É um método à parte, e não SuggestCounterpart com exclusão vazia, porque o
// nome daquele promete "contraparte" e o chamador dependeria de um truque de
// string vazia para obter outra semântica. Empate na pontuação máxima entre
// contas diferentes — inclusive entre a própria e outra — continua sendo
// ambiguous: ambíguo é pior que vazio, e a saída é cadastrar a palavra mais
// específica.
func (s *Set) MatchAccount(descriptionNorm string) (textmatch.Result, error) {
	if s == nil {
		return semSugestao(), nil
	}
	return s.accounts.Best(descriptionNorm)
}

// CategoryName devolve o nome de uma categoria ATIVA da casa carregada. É o
// que o auto-categorize mostra na prévia sem uma consulta por linha.
func (s *Set) CategoryName(id string) (string, bool) {
	if s == nil {
		return "", false
	}
	name, ok := s.categoryNames[id]
	return name, ok
}

// WorkSpent é quanto do orçamento de trabalho (textmatch.MaxMatchWork) esta
// operação já consumiu. Existe para a borda REGISTRAR o número (achado A6 da
// revisão de segurança): a folga do teto foi calculada sobre cenários de
// teste, e é o tráfego real que diz se ela é mesmo folga. É dado do SERVIDOR —
// uma contagem de células —, não descreve lançamento, valor, descrição nem
// palavra-chave, e por isso pode ir para o log.
func (s *Set) WorkSpent() int64 {
	if s == nil {
		return 0
	}
	return s.budget.Spent()
}

// AssignableKind devolve a NATUREZA da categoria quando ela, na casa
// carregada, ainda pode receber lançamento diretamente: ativa (nem arquivada
// nem excluída) e sem ser grupo com subcategoria ativa.
//
// É a pergunta "esta categoria ainda vale?" feita POR ID, e não por palavra —
// o que o confirm da importação precisa para descartar a sugestão que ficou
// obsoleta entre a análise e a confirmação (spec 0005 §13). Categoria de outra
// casa nunca entra no Set, então ela responde false pelo mesmo caminho.
//
// O zero value e o ponteiro nulo respondem false: sem conjunto carregado não
// há como afirmar que a categoria vale.
func (s *Set) AssignableKind(id string) (string, bool) {
	if s == nil {
		return "", false
	}
	kind, ok := s.atribuiveis[id]
	return kind, ok
}

func semSugestao() textmatch.Result {
	return textmatch.Result{Reason: textmatch.ReasonBelowThreshold}
}
