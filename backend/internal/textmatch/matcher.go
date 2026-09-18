package textmatch

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// Keyword é uma palavra-chave de um dono (categoria ou conta) como o banco a
// guarda. Norm é textnorm.Normalize(Keyword) — a coluna `keyword_norm`; se
// vier vazia, o Matcher a deriva da forma exibível.
type Keyword struct {
	OwnerID string
	Keyword string // forma exibível, a que a tela mostra
	Norm    string // forma de comparação
}

// Match é a pontuação de UM dono: a maior entre as suas palavras-chave, com
// a palavra (exibível) que decidiu.
type Match struct {
	OwnerID string
	Keyword string
	Score   int
}

// Reason explica o resultado de Best. Os valores são os do contrato
// (`AutoCategorizeUnmatchedReason`), por isso são strings e não ints.
type Reason string

const (
	ReasonMatched        Reason = "matched"
	ReasonBelowThreshold Reason = "below_threshold"
	ReasonAmbiguous      Reason = "ambiguous"
)

// Result é a escolha da §3. Match só vem preenchido quando Reason é
// ReasonMatched: assim quem lê não usa por engano o dono de um empate ou de
// uma pontuação abaixo do limiar.
type Result struct {
	Match  Match
	Reason Reason
}

// Matched diz se há sugestão utilizável.
func (r Result) Matched() bool { return r.Reason == ReasonMatched }

// maxMemoEntries limita a memorização por CHAVE. O Matcher vive por
// requisição (uma análise de lote, uma auto-categorização) e o teto de
// linhas dessas operações é 10.000; o dobro cobre com folga sem permitir que
// um Matcher de vida longa cresça sem fim.
const maxMemoEntries = 20_000

// maxMemoMatches limita a memorização por VALOR: o total de Match guardados na
// memo, somando todos os rankings. É o teto que faltava.
//
// POR QUE DOIS TETOS (achado A3 da revisão de segurança). A memo é a única
// estrutura do Matcher que cresce com DESCRIÇÕES × DONOS e a única que
// sobrevive à linha; todo o resto é linear nos tetos do produto e morre com a
// descrição. Um teto só de CHAVES é um teto de memória apenas enquanto o
// ranking for curto — e ele não é sempre. O corpus que o quebra cabe inteiro
// dentro dos limites vigentes: 200 categorias com uma palavra-chave cada,
// todas variantes a uma edição de uma palavra comum ("mercado" → "mercaxo",
// "merzado"; ~390 variantes distintas, respeitando o índice único da casa) e
// um mês com 10.000 descrições distintas contendo "mercado". Aí TODA descrição
// ranqueia TODO dono, e a memo guarda descrições × donos Match.
//
// O revisor mediu: 175 donos × 10.000 descrições = 118,4 MB de heap VIVO
// retido por requisição, gastando 6,6% de MaxMatchWork em 0,8 s — ou seja, nem
// o orçamento, nem o transaction.PlanTimeout (0,8 s!), nem o pool de conexões
// (a prévia do auto-categorize nem abre transação) pegavam isso. Com as cotas
// das rotas — AutoCategorize, TransferDetect e InvestmentDetect com Burst 3
// cada, mais ImportUpload, que não tem Burst e admite os 10 da cota
// simultâneos: 19 operações — uma única casa empilhava > 2 GB de heap vivo, e
// não há GOMEMLIMIT nem teto de requisições em voo: o OOM derrubaria TODAS as
// casas.
//
// POR QUE ESTE NÚMERO. Um Match são 40 bytes: dois cabeçalhos de string e um
// int. O TEXTO não é copiado — OwnerID e Keyword apontam para as strings que o
// índice já guarda —, então 40 bytes é o custo real de reter um Match.
//
// A CONTA, MEDIDA NO TETO DO PRODUTO (números refeitos na QUARTA rodada da
// revisão, achado B3: as duas versões anteriores deste quadro erravam o TOTAL,
// e é esta base que a próxima revisão vai herdar — por isso ela está escrita
// com a operação inteira à vista, e não só com o resultado):
//
//	100.000 Match × 40 B = 4 MB ...... 3,8 MiB de memo por MATCHER
//	× 3 matchers (ver internal/classify) = 12 MB ... 11,4 MiB por OPERAÇÃO
//	+ os índices das palavras-chave ........ 15,7 MiB de heap do Set
//	+ as até 10.000 linhas lidas ........... ~18–20 MiB por OPERAÇÃO
//	                                         ------------------------
//	  por OPERAÇÃO ......................... ~45–47 MiB
//
// ⚠️ As três parcelas somam POR OPERAÇÃO: o Set é carregado por REQUISIÇÃO
// (classify.Load, chamado em transaction/autocategorize.go), não compartilhado
// entre elas. Somar só as linhas lidas subestima o empilhamento em ~2,4×.
//
// O PIOR EMPILHAMENTO QUE AS COTAS PERMITEM, POR CASA, são 19 operações
// simultâneas: AutoCategorize 3 + TransferDetect 3 + InvestmentDetect 3
// (Burst 3 cada) + ImportUpload 10 — esta última porque `Burst` 0 significa
// `Burst = Requests`, e a cota dela é 10/h (config.RateLimits). São
// 19 × ~46 MiB = ~860–900 MiB (≈ 900–940 MB) por casa — e NÃO os ~200 MB da
// primeira versão deste comentário, nem os ~350–380 MB da segunda, que
// somavam APENAS a linha das 10.000 linhas lidas (19 × ~19 MiB) e deixavam de
// fora a memo e o Set. As unidades também estavam trocadas: 100.000 × 40 B × 3
// são 12 MB, ou 11,4 MiB — não "12,1 MiB".
//
// A conclusão não muda com o número certo: o termo NÃO-LINEAR (descrições ×
// donos, que media 118,4 MB numa requisição só) morreu com este teto, e o que
// sobra é linear nos tetos do produto — cresce com as cotas, não com o corpus
// que a pessoa cadastra. É por isso que o valor continua 100.000.
//
// DEPENDÊNCIA ENTRE ENTREGAS, e ela é de mão dupla: TRÊS rotas alcançam este
// teto, e uma delas — POST /investments/detect — é de OUTRA entrega. Se o teto
// for removido, afrouxado, ou reordenado para depois da gravação da memo,
// aquela entrega volta a ter caminho de OOM sem que uma linha dela seja tocada.
// Quem mexer aqui reavalia os três chamadores, não só o desta entrega.
//
// O caminho LEGÍTIMO não chega perto: 10.000 descrições distintas com ranking
// realista — poucos donos por descrição, porque as palavras-chave de uma casa
// de verdade não colidem entre si — cabem inteiras com sobra.
//
// O QUE ACONTECE AO BATER NO TETO: nada além de a memo parar de crescer. A
// descrição seguinte continua sendo CALCULADA e o resultado continua CERTO —
// só não é guardado. Recalcular custa orçamento de trabalho, e o orçamento já
// é um teto conferido e testado: degradar para "mais lento" é o desfecho
// aceitável; truncar resultado em silêncio não seria.
//
// POR QUE NÃO BASTA COBRAR A MEMORIZAÇÃO DO ORÇAMENTO (a cobrança existe, em
// Rank, e é honesta — mas ela não é o que segura a memória): a 40 bytes por
// Match, os 1,5 × 10⁹ de MaxMatchWork implicam 60 GB. O orçamento amarra o
// TEMPO da operação; quem amarra os BYTES é este teto.
const maxMemoMatches = 100_000

// ---------------------------------------------------------------------------
// Orçamento de trabalho (achado A1 da revisão de segurança)
// ---------------------------------------------------------------------------

// ErrWorkBudgetExceeded é devolvido por Rank e Best quando a operação passou
// do orçamento de trabalho. NUNCA é resultado parcial: quem recebe este erro
// não recebe ranking nenhum, e a borda o traduz em 422 — a operação não cabe,
// e "categorizou metade" seria pior do que não categorizar.
var ErrWorkBudgetExceeded = errors.New("text matching work budget exceeded")

// MaxMatchWork é o orçamento de trabalho de UMA operação — uma análise de
// importação, uma auto-categorização, um reprocessamento de transferências.
//
// A unidade é a CÉLULA: uma célula da matriz de programação dinâmica de
// LongestCommonSubstring, ou uma visita a uma lista de candidatas do índice.
// As duas custam a mesma ordem de grandeza (um compare e um store), e contar
// as duas é o que fecha o buraco: um conjunto de palavras-chave de tokens
// CURTOS (que não entram no índice de trigramas) gastaria tempo em visitas
// sem pagar uma célula de DP sequer.
//
// POR QUE ELE EXISTE: o prefiltro por trigrama corta o caso realista, não o
// patológico. Quando toda palavra-chave compartilha trigrama com todo token
// da descrição, cada par paga uma matriz inteira, e o custo vira
// palavras × tokens × runas² POR LINHA. A cota horária da rota não segura
// isso — 60 execuções/h de 7 min de CPU cada são ~7 CPU-hora por hora, por
// casa — e o WriteTimeout do http.Server NÃO cancela a goroutine (o mesmo
// registro está em internal/transaction/detecttransfers.go).
//
// POR QUE ESTE NÚMERO (medido em 18/09/2026 na máquina de desenvolvimento;
// os dois testes que produzem as medidas são TestOrcamentoCobreOTetoLegitimo e
// TestOrcamentoCortaOCasoAdversarial, e eles FALHAM se o número deixar de
// valer):
//
//   - TETO LEGÍTIMO do produto, com texto realista e todas as descrições
//     DISTINTAS (o pior caso: a memo não reaproveita nada):
//     4.000 palavras-chave × 10.000 descrições = 7,1 × 10⁸ células em 3,2 s;
//     5.000 palavras-chave × 10.000 descrições = 8,8 × 10⁸ células em 4,1 s.
//     5.000 é o máximo que uma casa consegue cadastrar — 200 categorias × 20
//     palavras mais 50 contas × 20 —, e os três matchers de uma operação
//     dividem ESTE orçamento. 1,5 × 10⁹ dá 1,7× de folga sobre esse extremo.
//   - TETO ADVERSARIAL — 4.000 palavras de 40 runas com prefixo comum de 33
//     contra descrições distintas de 140 runas: o revisor mediu 7 min 20 s de
//     CPU sem orçamento; com este teto a operação recusa na 67ª linha, depois
//     de 2,0 s. A cota da casa cai de ~7 CPU-hora por hora para ~2 CPU-minuto
//     por hora (60 execuções × 2 s), e o Burst 3 limita quantas correm juntas.
//
// A honestidade do número exige dizer o que ele NÃO faz: como o próprio teto
// legítimo custa segundos de CPU, nenhum orçamento que caiba nele corta o
// adversarial em milissegundos. O que sobra é a combinação — orçamento (2 s),
// prazo da rota (transaction.PlanTimeout, 15 s) e estouro 3 por casa.
//
// Estourar é 422, como todo outro teto desta feature — nunca execução parcial.
const MaxMatchWork = 1_500_000_000

// Budget é o orçamento de trabalho COMPARTILHADO por um ou mais Matchers.
//
// Compartilhado de propósito: uma operação carrega TRÊS matchers (entrada,
// saída e conta — ver internal/classify), e um orçamento por matcher deixaria
// o teto real ser o triplo do declarado. Um Budget por operação é o que torna
// o número acima o teto de verdade.
//
// Seguro para uso concorrente; o ponteiro nulo não limita nada (é o que os
// testes de unidade das funções puras usam).
type Budget struct {
	teto  int64
	gasto atomic.Int64
}

// NewBudget cria um orçamento com `work` unidades. Valor menor que 1 vira 1:
// orçamento zero seria "recusa tudo", e um defeito de configuração não pode
// virar indisponibilidade silenciosa.
func NewBudget(work int64) *Budget {
	if work < 1 {
		work = 1
	}
	return &Budget{teto: work}
}

// gastar consome n unidades e devolve false quando o orçamento acabou. O
// contador continua subindo depois do estouro — o chamador aborta na primeira
// recusa, então ele não corre.
func (b *Budget) gastar(n int) bool {
	if b == nil {
		return true
	}
	return b.gasto.Add(int64(n)) <= b.teto
}

// Exhausted diz se o orçamento estourou.
func (b *Budget) Exhausted() bool { return b != nil && b.gasto.Load() > b.teto }

// Spent é quanto já foi gasto. Existe para os testes de desempenho
// reportarem o número que justifica MaxMatchWork.
func (b *Budget) Spent() int64 {
	if b == nil {
		return 0
	}
	return b.gasto.Load()
}

// entry é uma palavra-chave indexada.
type entry struct {
	owner   int      // índice em Matcher.owners
	keyword string   // forma exibível
	tokens  []string // tokens da forma normalizada
	runes   [][]rune // os mesmos tokens, em runas (evita converter a cada comparação)
	refIdx  []int    // por token: índice em Matcher.refs, ou -1 se o token é curto (< MinFuzzyRunes)
}

// tokenRef aponta para um token de uma entrada — o que o índice de trigramas
// devolve como candidato às regras 2 e 3.
type tokenRef struct{ entry, token int }

// trigram é a chave do índice: três runas da forma preenchida. Array, e não
// string, para a busca não alocar.
type trigram [3]rune

// Matcher pontua descrições contra um conjunto fixo de palavras-chave. É
// construído uma vez por casa e por operação (as palavras-chave de uma casa
// cabem em memória: ≤ 20 por dono) e é seguro para uso concorrente.
//
// Estrutura: `exact` resolve a regra 1 por consulta em mapa; `trigram` reduz
// as regras 2 e 3 aos pares que compartilham ao menos um trigrama da forma
// preenchida; `memo` guarda o ranking por descrição, porque extrato repete
// descrição. Nenhum dos três muda o resultado — o teste de propriedade
// compara com a força bruta sobre ScoreKeyword.
type Matcher struct {
	owners  []string
	entries []entry
	exact   map[string][]int  // primeiro token da palavra-chave → entradas (regra 1)
	byToken map[string][]int  // qualquer token → entradas que o contêm (caminho token a token)
	refs    []tokenRef        // tokens com ≥ MinFuzzyRunes runas
	trigram map[trigram][]int // trigrama preenchido → índices em refs

	// maxTokens é o maior número de tokens de uma entrada. Serve para cobrar
	// o custo de uma lista de candidatas sem percorrê-la duas vezes: o
	// orçamento é cobrado ANTES do trabalho, nunca depois.
	maxTokens int

	// budget é o teto de trabalho da OPERAÇÃO (achado A1). Nulo não limita.
	budget *Budget

	mu   sync.RWMutex
	memo map[string][]Match
	// memoMatches é o total de Match guardados na memo, somando os rankings.
	// É ele — e não o número de chaves — que expressa o quanto de MEMÓRIA a
	// memo retém (achado A3). Protegido pelo mesmo mu.
	memoMatches int
}

// Option configura o Matcher na construção.
type Option func(*Matcher)

// WithBudget faz o Matcher compartilhar um orçamento com outros — é assim que
// internal/classify dá UM teto à operação inteira, e não um por matcher.
func WithBudget(b *Budget) Option { return func(m *Matcher) { m.budget = b } }

// NewMatcher indexa as palavras-chave. Devolve ErrInvalidKeyword (com a
// posição, nunca a palavra) se alguma não produz token útil ou não tem dono —
// isso só acontece se o banco tiver algo que ValidateKeyword não deixaria
// entrar, e é melhor falhar alto do que ignorar em silêncio.
func NewMatcher(keywords []Keyword, opts ...Option) (*Matcher, error) {
	m := &Matcher{
		exact:   make(map[string][]int),
		byToken: make(map[string][]int),
		trigram: make(map[trigram][]int),
		memo:    make(map[string][]Match),
	}
	for _, o := range opts {
		o(m)
	}
	if m.budget == nil {
		// Sem orçamento explícito, o Matcher ganha o seu: nenhum caminho de
		// produção pode existir sem teto por esquecimento do chamador.
		m.budget = NewBudget(MaxMatchWork)
	}
	ownerIdx := make(map[string]int)

	for i, kw := range keywords {
		if kw.OwnerID == "" {
			return nil, fmt.Errorf("%w: keyword #%d has no owner", ErrInvalidKeyword, i)
		}
		norm := kw.Norm
		if norm == "" {
			norm = textnorm.Normalize(kw.Keyword)
		}
		tokens := Tokenize(norm)
		if len(tokens) == 0 {
			return nil, fmt.Errorf("%w: keyword #%d has no useful word", ErrInvalidKeyword, i)
		}

		o, ok := ownerIdx[kw.OwnerID]
		if !ok {
			o = len(m.owners)
			m.owners = append(m.owners, kw.OwnerID)
			ownerIdx[kw.OwnerID] = o
		}

		e := len(m.entries)
		ent := entry{
			owner:   o,
			keyword: kw.Keyword,
			tokens:  tokens,
			runes:   make([][]rune, len(tokens)),
			refIdx:  make([]int, len(tokens)),
		}
		m.exact[tokens[0]] = append(m.exact[tokens[0]], e)

		seenTok := make(map[string]struct{}, len(tokens))
		for t, tok := range tokens {
			ent.runes[t] = []rune(tok)
			ent.refIdx[t] = -1
			if _, dup := seenTok[tok]; !dup {
				seenTok[tok] = struct{}{}
				m.byToken[tok] = append(m.byToken[tok], e)
			}
			if len(ent.runes[t]) < MinFuzzyRunes {
				continue // curto: só casa inteiro, não entra no índice de trigramas
			}
			ref := len(m.refs)
			m.refs = append(m.refs, tokenRef{entry: e, token: t})
			ent.refIdx[t] = ref
			for _, tri := range paddedTrigrams(ent.runes[t]) {
				m.trigram[tri] = append(m.trigram[tri], ref)
			}
		}
		m.entries = append(m.entries, ent)
		m.maxTokens = max(m.maxTokens, len(tokens))
	}
	return m, nil
}

// spend cobra n unidades do orçamento da operação.
func (m *Matcher) spend(n int) bool { return m.budget.gastar(n) }

// errOrcamento embrulha o estouro com o teto — número do servidor, nunca dado
// da casa.
func (m *Matcher) errOrcamento() error {
	teto := int64(0)
	if m.budget != nil {
		teto = m.budget.teto
	}
	return fmt.Errorf("%w: budget of %d units", ErrWorkBudgetExceeded, teto)
}

// paddedTrigrams devolve os trigramas DISTINTOS da forma preenchida
// `"  " + token + " "` (dois espaços antes, um depois). O preenchimento é o
// que garante a invariante do prefiltro para a regra 3: uma transposição no
// meio de "abcdef" → "abdcef" destrói todos os trigramas interiores, mas
// "  a", " ab" e "ef " sobrevivem. Para a regra 2, uma substring comum de 5
// runas contém três trigramas interiores inteiros.
func paddedTrigrams(tok []rune) []trigram {
	padded := make([]rune, 0, len(tok)+3)
	padded = append(padded, ' ', ' ')
	padded = append(padded, tok...)
	padded = append(padded, ' ')

	out := make([]trigram, 0, len(tok)+1)
	for i := 0; i+3 <= len(padded); i++ {
		tri := trigram{padded[i], padded[i+1], padded[i+2]}
		if !slices.Contains(out, tri) {
			out = append(out, tri)
		}
	}
	return out
}

// Rank devolve TODOS os donos com pontuação > 0 para a descrição, ordenados
// por Score desc, OwnerID asc, Keyword asc. Memorizado por descriptionNorm:
// a mesma descrição devolve o MESMO slice — trate-o como somente leitura.
// Seguro para uso concorrente. Matcher nulo devolve nil.
//
// RESSALVA (poda por comprimento — ver ScoreWord): o dono cuja melhor
// pontuação é provadamente menor que MinScore pode não aparecer. Best não
// muda de resposta por isso: ela só olha a pontuação máxima e o empate NELA,
// e os dois exigem ≥ MinScore.
//
// ERRO: ErrWorkBudgetExceeded quando a OPERAÇÃO passou do orçamento de
// trabalho. Nesse caso o ranking devolvido é nil e NADA é memorizado — o
// chamador aborta a operação inteira em vez de seguir com um resultado
// incompleto.
func (m *Matcher) Rank(descriptionNorm string) ([]Match, error) {
	if m == nil {
		return nil, nil
	}
	// STICKY: depois do estouro, NENHUMA consulta volta a responder — nem a
	// que não custaria nada. Sem isto, um chamador que ignorasse o erro
	// conseguiria seguir o laço e montar um resultado pela metade, que é
	// exatamente o que este teto existe para impedir.
	if m.budget.Exhausted() {
		return nil, m.errOrcamento()
	}

	m.mu.RLock()
	rank, ok := m.memo[descriptionNorm]
	m.mu.RUnlock()
	if ok {
		return rank, nil
	}

	rank, err := m.compute(descriptionNorm)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Outra goroutine pode ter calculado enquanto esta computava: quem chegou
	// primeiro vence, e as duas devolvem o mesmo objeto.
	if prev, ok := m.memo[descriptionNorm]; ok {
		return prev, nil
	}
	// DOIS tetos, e os dois conferidos ANTES de gravar: chaves e total de
	// Match (achado A3 — ver maxMemoMatches). Estourar qualquer um dos dois
	// não é erro: a memo simplesmente para de crescer, e o ranking JÁ
	// CALCULADO é devolvido igual. Recalcular a próxima repetição custa
	// orçamento de trabalho, que é o teto que já existe.
	if len(m.memo) < maxMemoEntries && m.memoMatches+len(rank) <= maxMemoMatches {
		// Memorizar é trabalho — um store por Match retido —, e neste pacote
		// trabalho se cobra ANTES de fazer. A cobrança acontece aqui, e não
		// junto do cálculo, porque só aqui se sabe que a entrada vai mesmo ser
		// gravada: cobrar pela memorização que os tetos acima recusam seria
		// cobrar por trabalho que não foi feito.
		//
		// Se ela estourar, NADA é gravado e o ranking NÃO é devolvido. É o que
		// mantém a cobrança coerente com o estouro sticky do topo desta função:
		// não existe entrada meia-gravada, nem resultado devolvido junto com o
		// erro — o chamador aborta a operação inteira, que vira 422.
		if !m.spend(len(rank)) {
			return nil, m.errOrcamento()
		}
		m.memo[descriptionNorm] = rank
		m.memoMatches += len(rank)
	}
	return rank, nil
}

// Best aplica a escolha da §3 sobre Rank: ignora os donos em `exclude`
// ANTES de escolher (a conta do lote nunca é contraparte de si mesma), exige
// Score ≥ MinScore e recusa empate entre donos diferentes na pontuação
// máxima — ambíguo é pior que vazio em dado financeiro.
func (m *Matcher) Best(descriptionNorm string, exclude ...string) (Result, error) {
	rank, err := m.Rank(descriptionNorm)
	if err != nil {
		// Sem sugestão E com erro: quem ignorar o erro não recebe um palpite
		// calculado pela metade.
		return Result{Reason: ReasonBelowThreshold}, err
	}

	first, second := -1, -1
	for i := range rank {
		if slices.Contains(exclude, rank[i].OwnerID) {
			continue
		}
		if first < 0 {
			first = i
			continue
		}
		second = i
		break
	}

	if first < 0 || rank[first].Score < MinScore {
		return Result{Reason: ReasonBelowThreshold}, nil
	}
	if second >= 0 && rank[second].Score == rank[first].Score {
		return Result{Reason: ReasonAmbiguous}, nil
	}
	return Result{Match: rank[first], Reason: ReasonMatched}, nil
}

// Estados de entryScore durante o cálculo.
const (
	scoreUntouched = -1 // nenhuma pista de que esta entrada casa: fica de fora
	scorePending   = -2 // apareceu como candidata; a pontuação é calculada no fim
)

// compute é o cálculo sem memo: o que Rank faria se não houvesse cache. O
// resultado é, por construção, igual a rankBruteForce — o teste de
// propriedade exige isso.
//
// Todo trabalho SUPERLINEAR — o que cresce com palavras-chave × descrições ×
// runas, e é o que explode — é cobrado do orçamento ANTES de ser feito
// (achado A1): a varredura das listas de candidatas dos três índices, as
// células de DP de cada ScoreWord e a passagem final por entrada tocada.
//
// O QUE NÃO É COBRADO, e por quê (dito aqui para ninguém descobrir sozinho):
//
//   - o custo que depende só da DESCRIÇÃO (Tokenize, paddedTrigrams do token
//     da descrição) — teto natural de linhas por execução × MaxDescriptionLen²
//     (10.000 × 140² ≈ 2 × 10⁸ comparações de rune), e cobrá-lo faria uma casa
//     SEM palavra-chave nenhuma estourar o orçamento;
//   - os dois vetores por descrição (refBest e stamp), lineares no número de
//     tokens indexados — teto natural de linhas × palavras-chave (10.000 ×
//     5.000 ≈ 10⁸ posições zeradas).
//
// Os dois são LINEARES nos tetos do produto, não amplificáveis, e o tempo
// deles já está dentro da medição do teto legítimo. Quem fecha por cima é o
// prazo da rota (transaction.PlanTimeout).
//
// O QUE ESSA LISTA OMITIA, e por que a omissão era um buraco (achado A3): a
// MEMO. Ela é a única coisa aqui que não é linear nos tetos — cresce com
// descrições × donos — e a única que SOBREVIVE à linha, então "o prazo da rota
// fecha por cima" não valia para ela: o prazo mata o tempo, não os bytes já
// retidos. Hoje a memorização é cobrada do orçamento em Rank E limitada em
// bytes por maxMemoMatches; a segunda é que é o teto de verdade, pelo motivo
// aritmético que está lá.
func (m *Matcher) compute(descriptionNorm string) ([]Match, error) {
	tokens := Tokenize(descriptionNorm)
	if len(tokens) == 0 || len(m.entries) == 0 {
		return nil, nil
	}
	tokenSet := make(map[string]struct{}, len(tokens))
	for _, tok := range tokens {
		tokenSet[tok] = struct{}{}
	}

	entryScore := make([]int, len(m.entries))
	for i := range entryScore {
		entryScore[i] = scoreUntouched
	}
	touched := make([]int, 0, 16)
	touch := func(e int) {
		if entryScore[e] == scoreUntouched {
			entryScore[e] = scorePending
			touched = append(touched, e)
		}
	}

	// Regra 1: a frase inteira, consecutiva, a partir de cada posição. O
	// custo é cobrado pela lista INTEIRA antes de percorrê-la, com o maior
	// número de tokens de uma entrada — runMatchesAt compara até isso.
	for i, tok := range tokens {
		candidatas := m.exact[tok]
		if len(candidatas) > 0 && !m.spend(len(candidatas)*(1+m.maxTokens)) {
			return nil, m.errOrcamento()
		}
		for _, e := range candidatas {
			if entryScore[e] != 100 && runMatchesAt(tokens, i, m.entries[e].tokens) {
				entryScore[e] = 100
			}
		}
	}

	// Regras 2 e 3: cada token da descrição com ≥ 5 runas contra os tokens
	// indexados que compartilham um trigrama preenchido. refBest guarda a
	// melhor pontuação por token indexado; stamp evita pontuar o mesmo par
	// duas vezes quando dois trigramas apontam para ele.
	refBest := make([]int, len(m.refs))
	stamp := make([]int, len(m.refs))
	for gen, tok := range tokens {
		// Entradas que contêm o token literalmente: cobre os tokens curtos, que
		// não estão no índice de trigramas, e é o atalho dos longos. É o
		// caminho SEM programação dinâmica — e é por isso que ele também paga:
		// um conjunto de palavras-chave só de tokens curtos gastaria tempo
		// aqui sem tocar numa célula de DP.
		literais := m.byToken[tok]
		if len(literais) > 0 && !m.spend(len(literais)) {
			return nil, m.errOrcamento()
		}
		for _, e := range literais {
			touch(e)
		}
		if utf8.RuneCountInString(tok) < MinFuzzyRunes {
			continue
		}
		w := []rune(tok)
		for _, tri := range paddedTrigrams(w) {
			postagem := m.trigram[tri]
			if len(postagem) > 0 && !m.spend(len(postagem)) {
				return nil, m.errOrcamento()
			}
			for _, ref := range postagem {
				if stamp[ref] == gen+1 {
					continue
				}
				stamp[ref] = gen + 1
				r := m.refs[ref]
				kw := m.entries[r.entry].runes[r.token]
				// O custo do par, cobrado ANTES de a matriz existir: é este
				// produto que explode no caso adversarial. Quem calcula é
				// WorkOfScoreWord, para a cobrança ser o custo REAL — o par
				// podado por comprimento paga 1, não a matriz inteira.
				if !m.spend(WorkOfScoreWord(w, kw)) {
					return nil, m.errOrcamento()
				}
				s := ScoreWord(w, kw)
				if s > refBest[ref] {
					refBest[ref] = s
				}
				if s > 0 {
					touch(r.entry)
				}
			}
		}
	}

	// Caminho token a token: mínimo, entre os tokens da palavra-chave, do
	// máximo que cada um alcançou.
	if len(touched) > 0 && !m.spend(len(touched)*m.maxTokens) {
		return nil, m.errOrcamento()
	}
	for _, e := range touched {
		if entryScore[e] != scorePending {
			continue // já fechou em 100 pela regra 1
		}
		ent := &m.entries[e]
		score := 100
		for t, tok := range ent.tokens {
			best := 0
			if _, ok := tokenSet[tok]; ok {
				best = 100
			} else if ref := ent.refIdx[t]; ref >= 0 {
				best = refBest[ref]
			}
			if best == 0 {
				score = 0
				break
			}
			score = min(score, best)
		}
		entryScore[e] = score
	}

	return m.aggregate(entryScore), nil
}

// aggregate reduz a pontuação por entrada à pontuação por dono (a maior; em
// empate, a palavra exibível menor, para o resultado não depender da ordem
// de cadastro) e ordena.
func (m *Matcher) aggregate(entryScore []int) []Match {
	ownerBest := make([]int, len(m.owners))
	ownerEntry := make([]int, len(m.owners))
	for e, s := range entryScore {
		if s <= 0 {
			continue
		}
		o := m.entries[e].owner
		if s > ownerBest[o] || (s == ownerBest[o] && m.entries[e].keyword < m.entries[ownerEntry[o]].keyword) {
			ownerBest[o] = s
			ownerEntry[o] = e
		}
	}

	// Capacidade EXATA (achado A3). O slice que sai daqui é o que a memo
	// RETÉM, então a folga do append não é desperdício passageiro: ela fica.
	// Com 175 donos casando, o crescimento do append parava em 256 — 69 bytes
	// por dono em vez de 40, 70% a mais retido por descrição memorizada.
	//
	// É a contagem exata, e não make(..., 0, len(m.owners)): pré-dimensionar
	// pelo número de donos seria pior no caso COMUM, em que a descrição casa
	// com um ou dois — uma casa com 250 donos passaria a reter 10 KB por
	// entrada memorizada no lugar de 80 bytes. A passagem extra é linear em
	// donos e roda uma vez por descrição, contra a matriz de DP que já rodou.
	n := 0
	for _, s := range ownerBest {
		if s > 0 {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	out := make([]Match, 0, n)
	for o, s := range ownerBest {
		if s > 0 {
			out = append(out, Match{OwnerID: m.owners[o], Keyword: m.entries[ownerEntry[o]].keyword, Score: s})
		}
	}
	slices.SortFunc(out, compareMatches)
	return out
}

// compareMatches: Score desc, OwnerID asc, Keyword asc.
func compareMatches(a, b Match) int {
	if c := cmp.Compare(b.Score, a.Score); c != 0 {
		return c
	}
	if c := cmp.Compare(a.OwnerID, b.OwnerID); c != 0 {
		return c
	}
	return cmp.Compare(a.Keyword, b.Keyword)
}

// runMatchesAt diz se `run` aparece em `tokens` começando exatamente em i.
func runMatchesAt(tokens []string, i int, run []string) bool {
	if i+len(run) > len(tokens) {
		return false
	}
	for j := range run {
		if tokens[i+j] != run[j] {
			return false
		}
	}
	return true
}

// rankBruteForce é a referência do teste de propriedade: pontua TODAS as
// entradas com ScoreKeyword, sem índice nem prefiltro, e agrega igual.
func (m *Matcher) rankBruteForce(descriptionNorm string) []Match {
	tokens := Tokenize(descriptionNorm)
	if len(tokens) == 0 || len(m.entries) == 0 {
		return nil
	}
	entryScore := make([]int, len(m.entries))
	for e := range m.entries {
		entryScore[e] = ScoreKeyword(tokens, m.entries[e].tokens)
	}
	return m.aggregate(entryScore)
}
