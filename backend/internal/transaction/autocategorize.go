package transaction

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
)

// POST /transactions/auto-categorize (spec 0005 §4.3, ADR-026h).
//
// A regra que este arquivo sustenta, em uma frase: o servidor só grava onde
// `category_id IS NULL`, só em receita e despesa, só na casa do token, e só
// depois de recalcular — a prévia que a tela mostrou nunca é confiada, porque
// entre a prévia e a confirmação o mês pode ter mudado (alguém categorizou à
// mão, importou, excluiu). Recalcular custa uma consulta e não deixa nenhum
// caminho em que a tela decide o que o banco grava.

// AutoCategorizeInput é o pedido.
type AutoCategorizeInput struct {
	// Month é "AAAA-MM", competência, obrigatório.
	Month string

	// DryRun true só calcula; false grava. O handler exige o campo no corpo
	// (ausente é 400): o zero value de bool seria "gravar", e gravar por um
	// campo esquecido é exatamente o que não pode acontecer.
	DryRun bool
}

// AutoCategorizeView é a resposta — schema AutoCategorizeResult do contrato.
//
// Em prévia, Categorized é quantos RECEBERIAM categoria e as listas vêm
// preenchidas (até MaxAutoCategorizeListed cada); na execução real,
// Categorized é o número de linhas AFETADAS pelo UPDATE condicional e as
// listas vêm vazias. Unmatched é sempre a contagem completa.
type AutoCategorizeView struct {
	Month          string                        `json:"month"`
	Categorized    int64                         `json:"categorized"`
	Unmatched      int64                         `json:"unmatched"`
	Items          []AutoCategorizeItemView      `json:"items"`
	UnmatchedItems []AutoCategorizeUnmatchedView `json:"unmatchedItems"`

	// MatchWorkSpent NÃO é contrato: `json:"-"` o mantém fora do corpo
	// (AutoCategorizeResult é additionalProperties:false). Ele existe para o
	// LOG da borda e é dado do SERVIDOR — uma contagem de células do orçamento
	// de textmatch, que não descreve lançamento, valor, descrição nem
	// palavra-chave.
	//
	// É a medida que diz se a folga de textmatch.MaxMatchWork é real contra o
	// tráfego de verdade, em vez de só contra os cenários do teste (achado A6
	// da revisão de segurança).
	MatchWorkSpent int64 `json:"-"`
}

// AutoCategorizeItemView é uma linha da prévia que receberia categoria.
type AutoCategorizeItemView struct {
	ID           string `json:"id"`
	Description  string `json:"description"`
	CategoryID   string `json:"categoryId"`
	CategoryName string `json:"categoryName"`
	// MatchScore é a pontuação da §3 (80–100) e MatchedKeyword a palavra
	// (forma exibível) que decidiu — a tela mostra "88% · supermercado".
	MatchScore     int    `json:"matchScore"`
	MatchedKeyword string `json:"matchedKeyword"`
}

// AutoCategorizeUnmatchedView é uma linha da prévia que continuaria sem
// categoria, com o motivo (AutoCategorizeUnmatchedReason do contrato).
type AutoCategorizeUnmatchedView struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Reason      string `json:"reason"`
}

// planoDeCategorizacao é o resultado do recálculo: o que seria gravado, por
// categoria, e a prévia já montada. É o mesmo cálculo em dryRun e na execução
// real — só a etapa seguinte muda.
type planoDeCategorizacao struct {
	// porCategoria é categoria -> ids dos lançamentos que a receberiam.
	porCategoria map[string][]string

	// ladoPorCategoria é categoria -> o `kind` do LANÇAMENTO que vai para ela
	// (`income` ou `expense`), guardado junto do lote (achado A9 da revisão de
	// segurança).
	//
	// Cada categoria só recebe linhas de UM lado do dinheiro, e isso é
	// construção, não coincidência: classify.SuggestCategory escolhe o matcher
	// pelo `kind` da LINHA, e as palavras-chave de uma categoria entram em um
	// único matcher (o de entrada para `income`/`redemption`, o de saída para
	// `expense`/`investment`). Por isso um valor por categoria basta.
	//
	// Ele existe porque a reconferência dentro da transação precisa perguntar
	// "esta categoria AINDA aceita ESTE lançamento?", e não só "ela ainda
	// existe?" — e o lado tem de vir do PLANO, nunca da categoria relida:
	// é justamente a natureza da categoria que pode ter mudado na janela.
	ladoPorCategoria map[string]string

	categorized    int64
	unmatched      int64
	items          []AutoCategorizeItemView
	unmatchedItems []AutoCategorizeUnmatchedView

	// trabalho é quanto do orçamento de casamento por palavra-chave o cálculo
	// gastou. Vai para o log da borda (achado A6), nunca para a resposta.
	trabalho int64
}

// PlanTimeout é o prazo PRÓPRIO da fase de cálculo das rotas que varrem o mês
// inteiro: POST /transactions/auto-categorize e POST /transfers/detect.
//
// Existe pelo mesmo motivo de importer.AnalyzeTimeout, e o motivo é literal:
// o WriteTimeout do http.Server (30 s) NÃO cancela esta goroutine — ele só
// corta a conexão. Sem prazo próprio, o cliente recebe uma conexão cortada e o
// servidor segue queimando CPU por uma resposta que ninguém vai ler. 15 s
// morre com folga antes dos 30 s e é ordens de grandeza acima do teto
// legítimo medido (4.000 palavras-chave × 10.000 linhas em ~2,8 s).
//
// Ele cobre SÓ o cálculo. Na execução real do auto-categorize o cálculo
// acontece FORA da transação (achado A2), então este prazo termina antes de a
// transação abrir: um UPDATE em lote não é interrompido no meio por causa
// dele.
const PlanTimeout = 15 * time.Second

// ctxDoPlano aplica o prazo próprio da fase de cálculo.
func (s *Service) ctxDoPlano(ctx context.Context) (context.Context, context.CancelFunc) {
	prazo := s.planTimeout
	if prazo <= 0 {
		prazo = PlanTimeout
	}
	return context.WithTimeout(ctx, prazo)
}

// conferirPrazo é a PARADA VOLUNTÁRIA da fase de cálculo das DUAS rotas que
// varrem o mês inteiro: pergunta ao RELÓGIO se o orçamento acabou e devolve o
// motivo já classificado.
//
// # Por que a classificação do erro nasce AQUI, e em nenhum outro lugar
//
// Só quem impôs o prazo sabe que foi o prazo que venceu. A borda não sabe:
// desde que o gormstore passou a somar o motivo do contexto ao erro do driver
// (platform/storage/ctxerr.go), `errors.Is(err, context.DeadlineExceeded)` é
// verdadeiro para QUALQUER falha de banco que aconteça com o contexto morto —
// conexão derrubada, pool esgotado, arquivo de banco corrompido. Um handler que
// traduzisse aquilo em "este mês demorou demais" culparia o MÊS da pessoa por
// uma falha do SERVIDOR e apagaria a linha de ERROR que é o único registro
// dela.
//
// Aqui não há ambiguidade: nada falhou. O código conferiu o relógio, viu que o
// orçamento acabou e desistiu por conta própria — esse, e só esse, é o
// ErrPlanTimeout. É a MESMA forma do conferirPrazo do importador
// (importer/analyze.go) e do detect de investimentos (investment/detect.go), e
// de propósito: as três fecham a mesma armadilha, e três formas diferentes para
// o mesmo perigo divergem.
//
// # Uma nota sobre o relógio
//
// A conferência tem DUAS perguntas porque o contexto responde a primeira com
// atraso: o cancelamento por prazo é feito por um timer, e no Windows a
// granularidade do timer é de milissegundos. Entre o instante em que o prazo
// passa e o instante em que `ctx.Err()` deixa de ser nil cabe uma fatia inteira
// do cálculo começando com o orçamento já no vermelho. Perguntar o PRAZO direto
// fecha essa janela — e quem responde continua sendo o relógio, nunca um erro
// alheio.
//
// ⚠️ Invariante que sustenta a tradução: o único prazo no contexto desta fase é
// o PlanTimeout imposto por planejarComPrazo / planejarTransferenciasComPrazo.
// O projeto não tem middleware de prazo por requisição (conferido em
// internal/platform/httpserver e cmd/api). No dia em que tiver, esta função
// precisa distinguir o prazo DELE do nosso antes de continuar chamando os dois
// de ErrPlanTimeout.
func conferirPrazo(ctx context.Context, etapa string) error {
	if motivo := ctx.Err(); motivo != nil {
		return erroDeParada(motivo, etapa)
	}
	if prazo, temPrazo := ctx.Deadline(); temPrazo && !time.Now().Before(prazo) {
		return erroDeParada(context.DeadlineExceeded, etapa)
	}
	return nil
}

// erroDeParada embrulha o motivo do contexto OBSERVADO por uma parada
// voluntária nossa.
//
// A distinção entre os dois motivos é a razão de esta função existir:
//
//   - prazo vencido é limite de TRABALHO previsto — vira ErrPlanTimeout, e a
//     borda responde 422 em `fields.month`, como a rota irmã
//     POST /investments/detect, que compartilha este mesmo PlanTimeout;
//   - cancelamento é o CLIENTE que foi embora (aba fechada, app morto). Não é
//     422 (não há a quem orientar) nem incidente: sobe CRU e a borda o
//     reconhece pelo contexto da REQUISIÇÃO — nunca por
//     `errors.Is(err, context.Canceled)`, que sob o embrulho do gormstore
//     rebaixaria para INFO um defeito de servidor —, registrando em INFO.
//
// O motivo original continua na cadeia nos dois casos: o log precisa dele, e
// nenhum dos dois carrega dado da casa.
func erroDeParada(motivo error, etapa string) error {
	if errors.Is(motivo, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %s: %w", ErrPlanTimeout, etapa, motivo)
	}
	return fmt.Errorf("varredura do mês interrompida: %s: %w", etapa, motivo)
}

// AutoCategorize aplica as palavras-chave da casa aos lançamentos sem
// categoria do mês.
//
// DryRun: calcula e devolve a prévia; NADA é escrito nem auditado.
//
// Execução real: o CÁLCULO (carga das palavras, leitura das linhas sem
// categoria e pontuação) roda FORA da transação, com prazo próprio; só os
// UPDATE condicionais e a auditoria rodam DENTRO dela (achado A2 da revisão
// de segurança).
//
// Isso é seguro — e não um afrouxamento — porque a escrita não confia no que
// foi lido: SetCategoryWhereNull repete `category_id IS NULL`,
// `kind IN (income, expense)`, a casa e "linha viva" no WHERE, e devolve as
// linhas AFETADAS. Se outra escrita categorizar uma linha entre o cálculo e o
// UPDATE, ela não é sobrescrita e não é contada. O que se ganha: a pontuação
// de até 10.000 linhas deixa de segurar uma conexão do pool (25, por
// DB_MAX_OPEN_CONNS) numa transação aberta.
//
// A escrita também não confia nas CATEGORIAS que o cálculo escolheu: dentro da
// transação, UMA consulta (LiveStates) reconfere que todas continuam
// atribuíveis, ANTES do primeiro UPDATE (achados A4 e A9). Se alguma não
// estiver mais, a operação INTEIRA falha com ErrCategoryChanged → 409, a
// transação é desfeita e nada é auditado — e não "pula a categoria que mudou e
// segue com o resto". A pessoa pediu para categorizar EM X, e X deixou de ser
// X no meio: continuar com um subconjunto silencioso é pior do que falhar e
// deixar que ela decida de novo. É o mesmo desenho de POST /investments/detect
// e de ErrTransferConversionConflict (ADR-028d), para as duas rotas não
// responderem coisas diferentes ao mesmo acontecimento.
//
// "Atribuível" aqui são QUATRO perguntas, e as quatro saem da mesma linha
// relida:
//
//   - VIVA. Ausente do mapa é excluída (ou de outra casa), e gravar nela
//     deixaria o lançamento apontando para o nada — o estado que
//     category.ErrInUse existe para impedir (achado A4);
//   - não é GRUPO com subcategoria ATIVA, que não recebe lançamento (spec 0005
//     §12/§13);
//   - não está ARQUIVADA. Marcação EXISTENTE sobrevive ao arquivamento;
//     ATRIBUIÇÃO NOVA, não — e esta rota faz atribuição nova. As outras três
//     portas recusam com ErrCategoryArchived, e uma escrita em massa que
//     gravasse seria a única porta do produto marcando onde as demais
//     recusam;
//   - a NATUREZA RELIDA ainda aceita o lado do dinheiro do lote, por
//     category.AceitaLancamento — a única fonte da verdade do pareamento
//     (ADR-029b). É a metade do A4 que ficou aberta e virou o achado A9:
//     PATCH /categories/{id} aceita `expense → income` numa categoria de topo
//     sem filhas e sem uso (podeTrocarNatureza só trava o cruzamento de lado
//     quando há filha OU uso), e "sem uso" é exatamente o estado da categoria
//     recém-criada que esta rota serve. Sem reler o `kind`, o lote inteiro de
//     despesas ia parar numa categoria de receita — estado que TODA outra
//     porta recusa (PATCH /transactions/{id} dá 422, o confirm da importação
//     degrada a sugestão, o detect de investimentos relê a natureza). Não é
//     BOLA: os ids saem do plano, já filtrado pela casa do token. É
//     integridade.
//
// A distinção que o produto faz sobre ARQUIVAR, e que o comentário anterior
// desta função enunciava pela metade: arquivar não desfaz a marcação que já
// existe — "lançamento com categoria arquivada" segue sendo estado legítimo e
// continua contando nos relatórios —, mas também não autoriza marcação NOVA.
//
// Idempotente por construção: a segunda execução não encontra
// `category_id IS NULL` para as mesmas linhas, e Categorized volta 0.
func (s *Service) AutoCategorize(ctx context.Context, ator Actor, in AutoCategorizeInput) (AutoCategorizeView, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return AutoCategorizeView{}, ErrNotFound
	}
	if _, err := ParseMonth(in.Month); err != nil {
		return AutoCategorizeView{}, err
	}
	if s.classifier == nil {
		// Falha de ligação (cmd/api monta o loader), não de entrada. Nunca
		// panic no caminho de request: erro genérico, 500, e o log diz o quê.
		return AutoCategorizeView{}, errors.New("classificador de palavras-chave não configurado")
	}

	plano, err := s.planejarComPrazo(ctx, householdID, in.Month)
	if err != nil {
		return AutoCategorizeView{}, err
	}

	if in.DryRun {
		return AutoCategorizeView{
			Month:          in.Month,
			Categorized:    plano.categorized,
			Unmatched:      plano.unmatched,
			Items:          plano.items,
			UnmatchedItems: plano.unmatchedItems,
			MatchWorkSpent: plano.trabalho,
		}, nil
	}

	// Ordem determinística por categoria: o resultado não depende dela, mas o
	// rastro de consultas sim, e teste que passa por acaso não é teste.
	categorias := make([]string, 0, len(plano.porCategoria))
	for id := range plano.porCategoria {
		categorias = append(categorias, id)
	}
	sort.Strings(categorias)

	var afetadas int64
	semCategoria := plano.unmatched
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		// Zerado a cada tentativa: o Transactor pode chamar a função de novo,
		// e uma contagem acumulada entre tentativas mentiria para a tela.
		afetadas = 0
		agora := s.clock()

		// RECONFERÊNCIA DAS CATEGORIAS DO PLANO (achados A4 e A9 da revisão de
		// segurança). UMA consulta, aqui dentro, antes de qualquer UPDATE — e
		// uma por EXECUÇÃO, nunca uma por linha nem uma por categoria.
		//
		// A janela que ela fecha: o cálculo roda FORA da transação (achado A2),
		// então entre "o plano decidiu que a categoria X recebe R1..Rn" e o
		// UPDATE cabe uma requisição INTEIRA de outro membro da casa (ou da
		// mesma pessoa em outra aba). Duas dessas requisições são aceitas hoje
		// e mudam a resposta da escrita:
		//
		//   - `DELETE /categories/X` — aceito, porque o `inUse` dele não
		//     encontra uso nenhum (nada foi gravado ainda) e as palavras-chave
		//     de X são apagadas FISICAMENTE na mesma transação;
		//   - `PATCH /categories/X` trocando a NATUREZA de lado — aceito numa
		//     categoria de topo, sem filhas e sem uso, que é precisamente a
		//     categoria recém-criada cujo lote esta rota está calculando.
		//
		// O WHERE de SetCategoryWhereNull confere `category_id IS NULL`,
		// `kind IN (...)`, a casa e a linha viva, mas NÃO conhece a categoria:
		// sem esta releitura os n lançamentos ficariam apontando para uma
		// categoria excluída, ou despesas ficariam penduradas numa categoria de
		// receita. Não é BOLA — os ids saem do plano, já filtrado pela casa do
		// token —, é integridade.
		//
		// É a MESMA chamada, com a MESMA resposta, que POST /investments/detect
		// faz pela mesma janela: duas rotas com duas regras de "atribuível", ou
		// com dois desfechos para o mesmo acontecimento, seria uma divergência
		// esperando acontecer — e é por isso que o ARQUIVAMENTO também derruba
		// aqui, exatamente como em investment/detect.go.
		estados, err := s.categories.LiveStates(ctx, householdID, categorias)
		if err != nil {
			return fmt.Errorf("reconferindo categorias do plano: %w", err)
		}
		for _, categoriaID := range categorias {
			st, viva := estados[categoriaID]
			if !category.DestinoAindaQualifica(st, viva, plano.ladoPorCategoria[categoriaID]) {
				// SEM o id na mensagem: o erro atravessa o log do handler, e
				// id de categoria é dado da casa (S8). SEM dizer QUAL dos
				// quatro qualificadores reprovou, também: a diferença entre
				// "foi excluída" e "virou receita" é informação sobre a
				// taxonomia da casa, e o cliente não precisa dela para agir —
				// recarregar a tela responde tudo.
				//
				// A operação inteira morre aqui, ANTES do primeiro UPDATE:
				// nada gravado, nada auditado, transação desfeita.
				return ErrCategoryChanged
			}
		}

		for _, categoriaID := range categorias {
			n, err := s.repo.SetCategoryWhereNull(ctx, householdID, plano.porCategoria[categoriaID], categoriaID, agora)
			if err != nil {
				return fmt.Errorf("gravando categoria em lote: %w", err)
			}
			afetadas += n
		}

		// Uma entrada por execução real, com o MÊS como entidade — sem
		// descrição, sem contagem, sem valor (emenda §10.5 da spec 0005).
		// Dentro da transação: se o rastro não couber, a escrita não vale.
		return s.registrarEm(ctx, ator, audit.ActionTransactionAutoCategorized, audit.EntityTransactionMonth, in.Month)
	})
	if err != nil {
		return AutoCategorizeView{}, err
	}

	return AutoCategorizeView{
		Month:       in.Month,
		Categorized: afetadas,
		Unmatched:   semCategoria,
		// Listas VAZIAS, e não nulas: o contrato publica arrays, e a tela não
		// distingue "ausente" de "acabou".
		Items:          []AutoCategorizeItemView{},
		UnmatchedItems: []AutoCategorizeUnmatchedView{},
		MatchWorkSpent: plano.trabalho,
	}, nil
}

// A qualificação do destino NÃO mora mais aqui: ela é
// category.DestinoAindaQualifica, junto de category.LiveState, que é o dado que
// ela lê. Estava escrita em duas cópias — uma nesta rota, outra no
// POST /investments/detect — e as duas divergiram: o qualificador do
// arquivamento entrou numa rodada de revisão em uma delas e só na seguinte na
// outra. É o mesmo motivo pelo qual AceitaLancamento foi centralizada
// (ADR-029b). A exigência EXTRA de cada rota continua sendo de cada rota; esta
// aqui não tem nenhuma.

// planejarComPrazo é planejarCategorizacao com o prazo próprio da rota. O
// cancel corre ANTES de qualquer escrita: o prazo é do cálculo, não da
// transação.
func (s *Service) planejarComPrazo(ctx context.Context, householdID, month string) (*planoDeCategorizacao, error) {
	ctx, cancel := s.ctxDoPlano(ctx)
	defer cancel()
	return s.planejarCategorizacao(ctx, householdID, month)
}

// planejarCategorizacao faz o recálculo: carrega o conjunto de palavras-chave
// da casa (4 consultas, só donas ATIVAS — classify.Load), lê as linhas sem
// categoria do mês (1 consulta, teto + 1) e pontua cada uma contra o matcher
// da natureza do seu kind. Nenhuma consulta por linha.
func (s *Service) planejarCategorizacao(ctx context.Context, householdID, month string) (*planoDeCategorizacao, error) {
	// Parada voluntária de ABERTURA: com o orçamento já no vermelho, nem a
	// primeira consulta sai. Sem ela, um contexto que chega vencido faria a
	// carga das palavras-chave falhar pelo DRIVER — e falha de driver sob
	// contexto morto é 500, não 422. O prazo tem de ser observado pelo
	// RELÓGIO, nunca por um erro alheio.
	if err := conferirPrazo(ctx, "abrindo o cálculo"); err != nil {
		return nil, err
	}

	conjunto, err := s.classifier.Load(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("carregando palavras-chave da casa: %w", err)
	}

	linhas, err := s.repo.ListUncategorized(ctx, householdID, month, MaxAutoCategorizeRows+1)
	if err != nil {
		return nil, fmt.Errorf("listando lançamentos sem categoria: %w", err)
	}
	if len(linhas) > MaxAutoCategorizeRows {
		return nil, fmt.Errorf("%w: máximo de %d por execução", ErrTooManyUncategorized, MaxAutoCategorizeRows)
	}

	plano := &planoDeCategorizacao{
		porCategoria:     map[string][]string{},
		ladoPorCategoria: map[string]string{},
		items:            []AutoCategorizeItemView{},
		unmatchedItems:   []AutoCategorizeUnmatchedView{},
	}
	for i := range linhas {
		// A cada 64 linhas, e não 500: com 500 o pior caso medido ficava 22 s
		// sem olhar o contexto (achado A1). O intervalo máximo entre duas
		// checagens é, agora, 64 linhas de custo de DESCRIÇÃO (microssegundos)
		// mais o que o orçamento de trabalho ainda permitir — e o orçamento é
		// o teto do trabalho da OPERAÇÃO inteira, não de uma linha, então ele
		// próprio limita quanto uma única linha consegue custar.
		if i%64 == 0 {
			// Cliente foi embora ou o prazo venceu: parar de gastar CPU com
			// uma resposta que ninguém vai ler. Quem CLASSIFICA os dois casos
			// é conferirPrazo — prazo vira ErrPlanTimeout (422 em `month`),
			// cancelamento sobe cru (INFO na borda).
			if err := conferirPrazo(ctx, "pontuando lançamentos do mês"); err != nil {
				return nil, err
			}
		}
		if err := plano.pontuar(conjunto, linhas[i]); err != nil {
			return nil, traduzirOrcamento(err, "pontuando lançamentos do mês")
		}
	}
	plano.trabalho = conjunto.WorkSpent()
	return plano, nil
}

// traduzirOrcamento transforma o estouro do orçamento de casamento por
// palavra-chave num erro de DOMÍNIO — 422 na borda, como todo outro teto
// desta feature. Qualquer outro erro sobe embrulhado com contexto.
func traduzirOrcamento(err error, contexto string) error {
	if errors.Is(err, textmatch.ErrWorkBudgetExceeded) {
		return fmt.Errorf("%w: %w", ErrKeywordMatchTooCostly, err)
	}
	return fmt.Errorf("%s: %w", contexto, err)
}

// pontuar classifica UMA linha e a acomoda no plano: na lista de quem recebe
// categoria (com a dona, a pontuação e a palavra) ou na de quem continua sem
// (com o motivo). As contagens são sempre completas; as listas param em
// MaxAutoCategorizeListed.
func (p *planoDeCategorizacao) pontuar(conjunto *classify.Set, linha UncategorizedRow) error {
	r, err := conjunto.SuggestCategory(linha.Kind, linha.DescriptionNorm)
	if err != nil {
		// Orçamento estourado: a operação INTEIRA morre. Nada de "esta linha
		// fica sem sugestão e o resto segue" — isso seria resultado truncado
		// em silêncio, e a pessoa não teria como saber quais linhas perderam
		// a chance de casar.
		return err
	}
	if !r.Matched() {
		p.unmatched++
		if len(p.unmatchedItems) < MaxAutoCategorizeListed {
			p.unmatchedItems = append(p.unmatchedItems, AutoCategorizeUnmatchedView{
				ID:          linha.ID,
				Description: linha.Description,
				Reason:      motivoDaPrevia(r.Reason),
			})
		}
		return nil
	}

	categoriaID := r.Match.OwnerID
	p.porCategoria[categoriaID] = append(p.porCategoria[categoriaID], linha.ID)
	p.anotarLado(categoriaID, linha.Kind)
	p.categorized++
	if len(p.items) < MaxAutoCategorizeListed {
		// O nome vem do conjunto já carregado: só categoria ATIVA da casa
		// entra no matcher, então ela sempre está lá. Se não estiver, o nome
		// vazio é o comportamento seguro — melhor do que uma consulta por
		// linha ou do que falhar a prévia inteira por um rótulo.
		nome, _ := conjunto.CategoryName(categoriaID)
		p.items = append(p.items, AutoCategorizeItemView{
			ID:             linha.ID,
			Description:    linha.Description,
			CategoryID:     categoriaID,
			CategoryName:   nome,
			MatchScore:     r.Match.Score,
			MatchedKeyword: r.Match.Keyword,
		})
	}
	return nil
}

// anotarLado registra o lado do dinheiro que a categoria vai receber (achado
// A9). É o que a reconferência dentro da transação compara com a natureza
// RELIDA.
//
// A divergência tratada abaixo é impossível por construção — uma categoria só
// aparece em um matcher, e cada matcher só é consultado por um `kind` de
// lançamento —, mas o desfecho dela é FECHAR: o lado vira vazio, e
// category.AceitaLancamento("", …) responde false para qualquer natureza, de
// modo que a execução morre em 409 em vez de gravar um pareamento que ninguém
// conferiu. Invariante quebrada não vira gravação silenciosa.
func (p *planoDeCategorizacao) anotarLado(categoriaID, kind string) {
	if anterior, jaTem := p.ladoPorCategoria[categoriaID]; jaTem && anterior != kind {
		p.ladoPorCategoria[categoriaID] = ""
		return
	}
	p.ladoPorCategoria[categoriaID] = kind
}

// motivoDaPrevia traduz a razão do matcher para o vocabulário do contrato
// (AutoCategorizeUnmatchedReason). Só dois valores existem lá; qualquer outra
// coisa vira below_threshold, que é o "não casou" genérico.
func motivoDaPrevia(r textmatch.Reason) string {
	if r == textmatch.ReasonAmbiguous {
		return string(textmatch.ReasonAmbiguous)
	}
	return string(textmatch.ReasonBelowThreshold)
}
