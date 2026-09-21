package investment

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
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// POST /investments/detect (spec 0006 §3.3, ADR-029h).
//
// A regra que este arquivo sustenta, em uma frase: o servidor recalcula, grava
// só onde o WHERE deixa, e a prévia promete EXATAMENTE o que a escrita fará.
//
// Entre a prévia e a confirmação cabe uma requisição inteira — alguém
// categorizou à mão, importou, excluiu —, então a prévia nunca é confiada:
// `dryRun: false` refaz a conta e as restrições que tornam a escrita segura
// (`category_id IS NULL` numa, `category_id IN (<allowlist>)` na outra) moram
// no `WHERE`, não neste arquivo.

// planoDeDeteccao é o resultado do recálculo: o que seria gravado, por
// categoria de destino, e as três listas da prévia já montadas.
//
// É o MESMO cálculo em dryRun e na execução real — só a etapa seguinte muda —,
// e é isso que impede a prévia de prometer o que a escrita não faz.
type planoDeDeteccao struct {
	// semCategoria é destino -> ids das linhas SEM categoria. Vai para
	// SetCategoryWhereNull.
	semCategoria map[string][]string

	// paraTrocar é destino -> ids das linhas que JÁ TÊM categoria de natureza
	// income/expense. Só é preenchido com overwriteCategorized; vai para
	// SetCategoryWhereCurrentIn, com a allowlist abaixo.
	paraTrocar map[string][]string

	// allowlist são os ids das categorias de natureza income/expense da casa —
	// o conjunto que entra no `WHERE` da troca.
	//
	// Ele é montado no plano para que a prévia e a escrita partam do MESMO
	// conjunto, e é PODADO dentro da transação (reconferirDestinos): categoria
	// que virou de investimento na janela sai daqui, senão a troca desfaria a
	// marcação que a pessoa acabou de fazer à mão — o contrário do que o
	// ADR-029h promete.
	allowlist []string

	// ladoPorDestino é destino -> `kind` do LANÇAMENTO das linhas daquele
	// lote. É o que permite à transação repetir, com natureza RELIDA, a mesma
	// pergunta que o plano fez (marcaInvestimento).
	//
	// O lote de um destino é homogêneo por construção: marcaInvestimento é a
	// única porta para os dois mapas e exige category.AceitaLancamento, que
	// casa `investment` só com despesa e `redemption` só com receita.
	ladoPorDestino map[string]string

	marked             int64
	unmatched          int64
	alreadyCategorized int64

	items          []DetectItemView
	unmatchedItems []DetectUnmatchedItemView
	alreadyItems   []DetectAlreadyCategorizedItemView
}

// Detect responde POST /api/v1/investments/detect.
//
// DryRun: calcula e devolve a prévia; NADA é escrito nem auditado.
//
// Execução real: o CÁLCULO (taxonomia, palavras-chave, leitura do mês e
// pontuação) roda FORA da transação, com prazo próprio; só os UPDATE
// condicionais e a auditoria rodam DENTRO dela (achado A2 da revisão de
// segurança da entrega anterior — pontuar 10.000 linhas com uma conexão do
// pool presa numa transação aberta é o que derruba a API sob rajada).
//
// Isso é seguro, e não um afrouxamento, porque a escrita não confia no que foi
// lido: os dois comandos repetem no WHERE a casa, "linha viva",
// `kind IN (income, expense)` e a condição sobre a categoria ATUAL, e devolvem
// as linhas AFETADAS. Se alguém marcar uma linha entre o cálculo e o UPDATE,
// ela não é sobrescrita e não é contada.
//
// O que o WHERE NÃO alcança é a categoria de DESTINO — ela é valor do SET, não
// condição. Por isso a transação começa reconferindo os destinos numa consulta
// só (reconferirDestinos): destino excluído no meio do caminho é
// ErrDestinationChanged, e nada é gravado.
//
// Idempotente por construção: a segunda execução não encontra mais
// `category_id IS NULL` para as mesmas linhas — e, com a flag, não encontra
// mais a categoria comum na allowlist —, então Marked volta 0.
func (s *Service) Detect(ctx context.Context, ator Actor, in DetectInput) (DetectView, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return DetectView{}, ErrUnauthenticated
	}
	if _, err := transaction.ParseMonth(in.Month); err != nil {
		return DetectView{}, err
	}
	if s.classifier == nil {
		// Falha de ligação (cmd/api monta o loader), não de entrada. Nunca
		// panic no caminho de request: 500 genérico, e o log diz o quê.
		return DetectView{}, errClassifierMissing
	}
	if !in.DryRun && s.tx == nil {
		// Mesma natureza: o construtor exige o Transactor, então chegar aqui
		// sem ele é montagem errada. Um erro é melhor do que a
		// desreferência de interface nula — panic é proibido no caminho de
		// request (docs/SEGURANCA.md), e a barreira de recover existe só como
		// última linha.
		return DetectView{}, errTransactorMissing
	}

	plano, err := s.planejarComPrazo(ctx, householdID, in)
	if err != nil {
		return DetectView{}, err
	}

	if in.DryRun {
		return DetectView{
			Month:                   in.Month,
			Marked:                  plano.marked,
			Unmatched:               plano.unmatched,
			AlreadyCategorized:      plano.alreadyCategorized,
			Items:                   plano.items,
			UnmatchedItems:          plano.unmatchedItems,
			AlreadyCategorizedItems: plano.alreadyItems,
		}, nil
	}

	afetadas, err := s.gravar(ctx, ator, in, plano)
	if err != nil {
		return DetectView{}, err
	}

	return DetectView{
		Month:     in.Month,
		Marked:    afetadas,
		Unmatched: plano.unmatched,
		// AlreadyCategorized é o que a execução NÃO tocou. Com a flag ligada
		// ele é zero por construção: aquele conjunto foi para `paraTrocar`.
		AlreadyCategorized: plano.alreadyCategorized,
		// Listas VAZIAS, e não nulas: o contrato publica arrays, e a tela não
		// distingue "ausente" de "acabou".
		Items:                   []DetectItemView{},
		UnmatchedItems:          []DetectUnmatchedItemView{},
		AlreadyCategorizedItems: []DetectAlreadyCategorizedItemView{},
	}, nil
}

// gravar executa os UPDATE condicionais e a auditoria DENTRO de uma transação.
//
// Ordem determinística por categoria de destino: o resultado não depende dela,
// mas o rastro de comandos sim, e teste que passa por acaso não é teste.
func (s *Service) gravar(ctx context.Context, ator Actor, in DetectInput, plano *planoDeDeteccao) (int64, error) {
	var afetadas int64
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		// Zerado a cada tentativa: o Transactor pode chamar a função de novo,
		// e uma contagem acumulada entre tentativas mentiria para a tela.
		afetadas = 0
		agora := s.clock()

		// PRIMEIRO comando da transação, antes de qualquer escrita.
		if err := s.reconferirDestinos(ctx, ator.HouseholdID, plano); err != nil {
			return err
		}

		for _, destino := range destinosOrdenados(plano.semCategoria) {
			n, err := s.ledger.SetCategoryWhereNull(ctx, ator.HouseholdID, plano.semCategoria[destino], destino, agora)
			if err != nil {
				return fmt.Errorf("marcando lançamentos sem categoria: %w", err)
			}
			afetadas += n
		}

		// A allowlist vai no comando MESMO quando alguém marcou a linha entre
		// a prévia e a confirmação: é o banco que decide o que a linha ainda
		// era, e o UPDATE afeta ZERO linhas nesse caso — que é resultado
		// legítimo (idempotência), não erro.
		//
		// Ela já vem PODADA pela reconferência. Vazia significa que nenhuma
		// categoria comum sobrou para substituir — o lote inteiro é pulado, e
		// isso é o mesmo resultado de um `WHERE` sem correspondência, não um
		// estado intermediário inventado. O curto-circuito é em Go porque
		// `IN ()` é erro de sintaxe em três dos quatro dialetos (ADR-029f).
		if len(plano.allowlist) == 0 && len(plano.paraTrocar) > 0 {
			return s.registrar(ctx, ator, in)
		}

		for _, destino := range destinosOrdenados(plano.paraTrocar) {
			n, err := s.ledger.SetCategoryWhereCurrentIn(ctx, ator.HouseholdID, plano.paraTrocar[destino], plano.allowlist, destino, agora)
			if err != nil {
				return fmt.Errorf("substituindo categoria comum por categoria de investimento: %w", err)
			}
			afetadas += n
		}

		// UMA entrada por execução real, com o MÊS como entidade — sem
		// descrição, sem valor, sem palavra-chave e sem as contagens, que vão
		// para a resposta e para o log da borda (spec 0006 §6.4). Dentro da
		// transação: se o rastro não couber, a escrita não vale.
		return s.registrar(ctx, ator, in)
	})
	if err != nil {
		return 0, err
	}
	return afetadas, nil
}

// reconferirDestinos refaz, DENTRO da transação e antes de qualquer UPDATE, as
// perguntas que o plano respondeu lá fora — com dado RELIDO.
//
// # Por que ela existe
//
// O cálculo roda FORA da transação (achado A2) e entre ele e a escrita cabe uma
// requisição inteira. Duas sequências, as duas com um único ator autenticado:
//
//  1. o plano decide que a categoria X recebe os lançamentos R1..Rn; outro
//     morador chama DELETE /categories/X; a checagem `inUse` da exclusão não
//     encontra NADA, porque nada foi gravado ainda; o UPDATE gravaria X em n
//     lançamentos — o `WHERE` confere a casa, a linha viva, o `kind` e a
//     categoria ATUAL, mas nunca a categoria de DESTINO, que é valor do SET;
//
//  2. o plano escolhe X porque a natureza dela é `investment`; na janela de
//     15 s da fase de cálculo (transaction.PlanTimeout), um
//     PATCH /categories/X {"kind":"expense"} é ACEITO — trocar de natureza
//     dentro do mesmo lado do dinheiro é permitido mesmo com a categoria em
//     uso, e mesmo com subcategorias (ADR-029c). X continua viva e sem filha
//     ativa, então a reconferência antiga a aprovava; o UPDATE então gravava
//     uma categoria COMUM por cima de `Mercado` e `Padaria`, em fatias de 200
//     e até 10.000 linhas. Isso é recategorização em massa de comum para
//     comum: o que a spec 0006 §2.2 põe explicitamente FORA de escopo, sem
//     desfazer, e contornando o teto de 120/h do PATCH /transactions/{id}.
//
// A segunda sequência é a que a primeira versão desta função declarava não
// cobrir. A frase que dizia que a natureza "não invalida a marcação" era falsa
// para esta rota: a natureza do destino é precisamente o que qualifica a linha
// como aporte ou resgate (marcaInvestimento).
//
// # Os QUATRO eixos, todos com dado relido e cada um no seu `if`
//
// Para cada destino dos DOIS lotes, tudo numa consulta só:
//
//   - continua VIVO? (id ausente do mapa = excluído, ou de outra casa);
//   - continua NÃO ARQUIVADO? (atribuição nova não vai para categoria
//     aposentada — ver o eixo 2 no corpo);
//   - continua ATRIBUÍVEL? (não virou grupo com subcategoria ativa);
//   - a natureza RELIDA ainda faz dele um destino de investimento para o lado
//     do dinheiro daquele lote? É `marcaInvestimento` outra vez — a mesma
//     função que o plano usou, agora com `kind` fresco. Ela recusa tanto
//     `investment → expense` quanto `investment → redemption`.
//
// Os quatro são `if`s SEPARADOS, e a separação é a própria garantia: fundidos,
// um eixo pode ser removido sem derrubar teste nenhum porque outro o mascara.
//
// Qualquer não é ErrDestinationChanged: a transação inteira é desfeita, NADA é
// gravado, e nenhuma linha "sai do lote" em silêncio. A alternativa — escrever
// o que sobrou — devolveria um `marked` menor que o da prévia sem dizer quais
// lançamentos ficaram para trás, e esta rota promete o contrário. Mesmo
// desfecho do ADR-028d: 409 CONFLICT, e a tela pede a prévia de novo.
//
// # A allowlist é PODADA, não reconferida
//
// `plano.allowlist` congela quem era "categoria comum" no instante do cálculo.
// Se na janela um grupo `expense` virar `investment` — o caminho que o ADR-029c
// existe para oferecer —, ele continuaria na allowlist e o UPDATE trocaria a
// categoria de uma linha que a pessoa ACABOU de marcar como investimento à mão.
// Isso é o oposto da garantia publicada do `overwriteCategorized`.
//
// Aqui a resposta é PODAR, e não falhar, e a assimetria é deliberada: allowlist
// menor só faz o `WHERE` alcançar menos linhas, que é o mesmo resultado
// legítimo de alguém ter editado a linha no meio (idempotência, já previsto no
// contrato). Falhar a operação inteira porque uma categoria que nem estava no
// lote mudou puniria a pessoa por um conflito que não a atinge.
//
// ARQUIVAR não recusa nada: arquivar é benigno (PLANOS.md §4.4), a categoria
// arquivada continua contando e o lançamento marcado continua marcado.
func (s *Service) reconferirDestinos(ctx context.Context, householdID string, plano *planoDeDeteccao) error {
	destinos := destinosDoPlano(plano)
	if len(destinos) == 0 {
		return nil
	}

	// A allowlist só é relida quando existe lote de troca: sem ele ela não vai
	// a comando nenhum, e pedir 200 ids a mais seria custo sem pergunta.
	consulta := destinos
	if len(plano.paraTrocar) > 0 {
		consulta = append(append([]string{}, destinos...), plano.allowlist...)
	}

	estados, err := s.categories.LiveStates(ctx, householdID, consulta)
	if err != nil {
		return fmt.Errorf("reconferindo categorias de destino: %w", err)
	}

	// Os QUATRO eixos são `if`s SEPARADOS de propósito, e a ordem é a mesma do
	// predicado compartilhado que nasceu do mesmo defeito
	// (category.DestinoAindaQualifica): encontrada, arquivada, grupo com filha
	// ativa, natureza. Fundidos numa expressão só — como `!vivo ||
	// st.HasActiveChild` esteve —, um eixo pode ser apagado sem que teste
	// nenhum perceba, porque outro o mascara. Um eixo sustentado por acidente
	// de outro é um eixo que a próxima refatoração apaga sem derrubar o verde.
	//
	// SEM o id em nenhuma das quatro saídas: o erro atravessa o log do handler,
	// e id de categoria é dado da casa (S8). É por isso também que as quatro
	// devolvem a MESMA sentinela — a tela não aprende qual eixo reprovou.
	for _, destino := range destinos {
		st, vivo := estados[destino]

		// EIXO 1 — EXCLUÍDA (ou de outra casa): id que não voltou de
		// LiveStates. A checagem `inUse` de DELETE /categories/{id} não
		// encontra nada enquanto esta escrita não gravou, então a exclusão é
		// aceita bem no meio da janela; gravar depois penduraria o lote numa
		// categoria que não existe.
		//
		// ⚠️ Este é o único dos quatro que NENHUM teste deste pacote derruba
		// sozinho, e a razão é da linguagem: em Go o `ok` falso de um mapa vem
		// sempre acompanhado do zero value, então apagar esta guarda faz a
		// excluída cair no EIXO 4 com `Kind` vazio, que também reprova. Separar
		// as condições não desfaz o mascaramento — desfaz só quem trata
		// `encontrada` como PARÂMETRO em vez do `ok` de um mapa, que é o
		// predicado compartilhado category.DestinoAindaQualifica (migração
		// declarada como dívida).
		//
		// Quem guarda este eixo, então, é o COMPILADOR, e é uma guarda mais
		// forte do que um teste porque não se pula: apagar este `if` deixa
		// `vivo` declarado e não usado, e o pacote NÃO COMPILA
		// ("declared and not used: vivo"). A única forma de fazer o mutante
		// compilar é DESCARTAR `vivo` num identificador em branco — que é
		// exatamente o padrão que a revisão de segurança caça por grep, e
		// exatamente o mutante vivo que o revisor encontrou na árvore. Se você
		// veio até aqui para silenciar `vivo`, pare: o defeito é a alteração,
		// não o aviso do compilador.
		//
		// A cobertura dos dois eixos que este `if` guarda (a CASA e a EXCLUSÃO)
		// é do repositório, e tem nome: gormstore.TestLiveStatesNaoEnxergaOutraCasa
		// e gormstore.TestLiveStatesDeixaDeForaAExcluida.
		if !vivo {
			return ErrDestinationChanged
		}

		// EIXO 2 — ARQUIVADA. ATRIBUIÇÃO NOVA a categoria arquivada é recusada
		// em todas as outras portas (transaction.ErrCategoryArchived: PATCH de
		// uma linha, lote e importação). Sem esta recusa, o `detect` seria a
		// única porta do produto a gravar onde as outras três recusam.
		//
		// Isto NÃO contradiz "arquivar é benigno": aquilo é sobre marcação que
		// JÁ EXISTE, que sobrevive ao arquivamento e continua contando. A
		// prévia nunca escolhe destino arquivado (classify só expõe dona
		// ativa), então só se chega aqui pela corrida.
		if st.Archived {
			return ErrDestinationChanged
		}

		// EIXO 3 — GRUPO com subcategoria ATIVA não recebe lançamento (spec
		// 0005 §12/§13). Filha arquivada não conta: grupo cujas filhas foram
		// todas arquivadas volta a ser destino legítimo.
		if st.HasActiveChild {
			return ErrDestinationChanged
		}

		// EIXO 4 — NATUREZA: a MESMA pergunta do plano, com a natureza relida.
		// O lado do dinheiro vem do LOTE, e não da categoria, justamente porque
		// é a categoria que pode ter mudado.
		if !marcaInvestimento(plano.ladoPorDestino[destino], st.Kind) {
			return ErrDestinationChanged
		}
	}

	if len(plano.paraTrocar) > 0 {
		plano.allowlist = podarAllowlist(plano.allowlist, estados)
	}
	return nil
}

// podarAllowlist devolve os ids da allowlist que CONTINUAM sendo categoria
// comum (income/expense) e vivos.
//
// Some quem foi excluído e quem virou natureza de investimento na janela. A
// ordem da entrada é preservada: o conjunto vira `IN (...)`, o resultado não
// depende da ordem, mas o rastro de comandos sim.
//
// ⚠️ ARQUIVADA CONTINUA na allowlist, e a assimetria com o DESTINO é a regra do
// produto, não um descuido: a allowlist descreve a categoria que a linha JÁ TEM
// — marcação existente sobrevive ao arquivamento —, enquanto o destino é
// ATRIBUIÇÃO NOVA, que categoria arquivada não recebe. Tirar a arquivada daqui
// deixaria sem conserto justamente a linha presa numa categoria de despesa que
// a casa aposentou, que é um dos casos que a flag existe para resolver.
//
// Allowlist vazia NÃO vira erro nem `IN ()`: quem chama pula o lote de troca,
// porque "nenhuma categoria comum sobrou para substituir" é a resposta CERTA —
// exatamente o que um `WHERE` sem correspondência produziria.
func podarAllowlist(allowlist []string, estados map[string]category.LiveState) []string {
	out := make([]string, 0, len(allowlist))
	for _, id := range allowlist {
		st, vivo := estados[id]
		// Mesma propriedade do EIXO 1 da reconferência, e mesma guarda: o filtro
		// de natureza logo abaixo mascara este `if` no comportamento, mas apagá-lo
		// deixa `vivo` declarado e não usado e o pacote não compila. Silenciar a
		// variável num identificador em branco para "consertar" seria escrever o
		// mutante à mão.
		if !vivo {
			continue
		}
		if st.Kind != category.KindIncome && st.Kind != category.KindExpense {
			continue
		}
		out = append(out, id)
	}
	return out
}

// destinosDoPlano junta, em ordem estável e sem repetição, as categorias de
// destino dos DOIS lotes — é exatamente o conjunto que o UPDATE gravaria.
func destinosDoPlano(plano *planoDeDeteccao) []string {
	vistos := make(map[string]struct{}, len(plano.semCategoria)+len(plano.paraTrocar))
	out := make([]string, 0, len(plano.semCategoria)+len(plano.paraTrocar))
	for _, id := range destinosOrdenados(plano.semCategoria) {
		vistos[id] = struct{}{}
		out = append(out, id)
	}
	for _, id := range destinosOrdenados(plano.paraTrocar) {
		if _, ok := vistos[id]; ok {
			continue
		}
		vistos[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

// destinosOrdenados devolve as categorias de destino em ordem estável.
func destinosOrdenados(porDestino map[string][]string) []string {
	out := make([]string, 0, len(porDestino))
	for id := range porDestino {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// registrar grava a entrada de auditoria da execução real.
//
// A AÇÃO distingue as duas execuções, e a distinção é de segurança: preencher
// o que estava vazio e SUBSTITUIR categoria que uma pessoa escolheu à mão são
// operações diferentes, a segunda não tem desfazer, e sem ações separadas a
// perícia dependeria da retenção do log de aplicação em vez da tabela de
// auditoria (ver audit.ActionTransactionInvestmentsOverwritten).
//
// A ação é escolhida pela FLAG do pedido, e não por ter havido linha trocada:
// quem pediu autorização para substituir fica registrado mesmo que o UPDATE
// tenha alcançado zero linhas — a intenção é parte do rastro.
func (s *Service) registrar(ctx context.Context, ator Actor, in DetectInput) error {
	if s.audit == nil {
		return nil
	}
	acao := audit.ActionTransactionInvestmentsDetected
	if in.OverwriteCategorized {
		acao = audit.ActionTransactionInvestmentsOverwritten
	}
	return s.audit.Record(ctx, AuditParams{
		Action:      acao,
		Entity:      audit.EntityTransactionMonth,
		EntityID:    in.Month,
		UserID:      ator.UserID,
		HouseholdID: ator.HouseholdID,
		IP:          ator.IP,
	})
}

// planejarComPrazo é planejar com o prazo PRÓPRIO da fase de cálculo. O cancel
// corre ANTES de qualquer escrita: o prazo é do cálculo, não da transação — um
// UPDATE em lote não pode ser interrompido no meio por causa dele.
//
// O prazo existe porque o WriteTimeout do http.Server não cancela esta
// goroutine: ele só corta a conexão, e sem prazo próprio o servidor seguiria
// queimando CPU por uma resposta que ninguém vai ler.
//
// ⚠️ Quem IMPÕE o prazo é esta função, e por isso é ela — e as paradas
// voluntárias de `planejar`, logo abaixo — quem tem o direito de dizer "a
// detecção deste mês demorou demais" (ErrPlanTimeout). A borda NÃO deduz mais
// isso do contexto: ver o comentário de ErrPlanTimeout em types.go.
func (s *Service) planejarComPrazo(ctx context.Context, householdID string, in DetectInput) (*planoDeDeteccao, error) {
	prazo := s.planTimeout
	if prazo <= 0 {
		prazo = transaction.PlanTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, prazo)
	defer cancel()
	return s.planejar(ctx, householdID, in)
}

// planejar faz o recálculo COMPARTILHADO entre a prévia e a execução: lê a
// taxonomia (1 consulta), carrega as palavras-chave da casa (classify.Load, 4
// consultas, só donas ATIVAS) e lê as receitas e despesas vivas do mês (1
// consulta, teto + 1). Nenhuma consulta por linha.
//
// O teto é conferido ANTES de pontuar e, portanto, antes de qualquer escrita:
// mês grande demais é 422, nunca execução parcial (spec 0006 §3.3.5).
func (s *Service) planejar(ctx context.Context, householdID string, in DetectInput) (*planoDeDeteccao, error) {
	// Parada voluntária de ABERTURA: com o orçamento já no vermelho, nem a
	// primeira consulta sai. Sem ela, um contexto que chega vencido faria a
	// leitura da taxonomia falhar pelo driver — e falha de driver sob contexto
	// morto é 500, não 422. O prazo tem de ser observado pelo RELÓGIO, nunca
	// por um erro alheio.
	if err := conferirPrazo(ctx, "abrindo o cálculo"); err != nil {
		return nil, err
	}

	tax, err := s.carregarTaxonomia(ctx, householdID)
	if err != nil {
		return nil, err
	}

	conjunto, err := s.classifier.Load(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("carregando palavras-chave da casa: %w", err)
	}

	// "Candidato" é toda receita/despesa viva do mês, COM ou SEM categoria,
	// porque todas são lidas para montar a prévia. Peça o teto + 1 para
	// descobrir que ele foi ultrapassado sem ler o mês inteiro.
	linhas, err := s.ledger.ListIncomeExpenseOfMonth(ctx, householdID, in.Month, MaxCandidates+1)
	if err != nil {
		return nil, fmt.Errorf("listando lançamentos do mês: %w", err)
	}
	if len(linhas) > MaxCandidates {
		// Sem a contagem na mensagem: o teto está no contrato, e quantos
		// lançamentos a casa tem é dado dela.
		return nil, fmt.Errorf("%w: máximo de %d por execução", ErrTooManyCandidates, MaxCandidates)
	}

	comuns := make(map[string]struct{}, len(tax.comuns))
	for _, id := range tax.comuns {
		comuns[id] = struct{}{}
	}

	plano := &planoDeDeteccao{
		semCategoria:   map[string][]string{},
		paraTrocar:     map[string][]string{},
		ladoPorDestino: map[string]string{},
		allowlist:      tax.comuns,
		items:          []DetectItemView{},
		unmatchedItems: []DetectUnmatchedItemView{},
		alreadyItems:   []DetectAlreadyCategorizedItemView{},
	}
	for i := range linhas {
		// A cada 64 linhas, e não 500: com 500 o pior caso medido ficava 22 s
		// sem olhar o contexto (achado A1 da entrega anterior). O intervalo
		// entre duas checagens é 64 descrições mais o que o orçamento de
		// trabalho do matcher ainda permitir — e esse orçamento é o teto da
		// OPERAÇÃO inteira, então ele próprio limita quanto uma linha custa.
		if i%64 == 0 {
			if err := conferirPrazo(ctx, "pontuando os lançamentos do mês"); err != nil {
				return nil, err
			}
		}
		if err := plano.pontuar(conjunto, tax, comuns, linhas[i], in.OverwriteCategorized); err != nil {
			return nil, traduzirOrcamento(err, "pontuando lançamentos do mês")
		}
	}
	return plano, nil
}

// conferirPrazo é a PARADA VOLUNTÁRIA da fase de cálculo: pergunta ao RELÓGIO
// se o orçamento acabou e devolve o motivo já classificado.
//
// # Por que a classificação do erro nasce AQUI, e em nenhum outro lugar
//
// Só quem impôs o prazo sabe que foi o prazo que venceu. A borda não sabe:
// desde que o gormstore passou a somar o motivo do contexto ao erro do driver
// (platform/storage/ctxerr.go), `errors.Is(err, context.DeadlineExceeded)` é
// verdadeiro para QUALQUER falha de banco que aconteça com o contexto morto —
// conexão derrubada, pool esgotado, arquivo de banco corrompido. Um handler que
// traduzisse aquilo em "a detecção deste mês demorou demais" culparia o MÊS da
// pessoa por uma falha do SERVIDOR e apagaria a linha de ERROR que é o único
// registro dela.
//
// Aqui não há ambiguidade: nada falhou. O código conferiu o relógio, viu que o
// orçamento acabou e desistiu por conta própria — esse, e só esse, é o
// ErrPlanTimeout. É a mesma forma do conferirPrazo do importador
// (importer/analyze.go), e de propósito: as duas rotas fecham a mesma
// armadilha, e duas formas diferentes para o mesmo perigo divergem.
//
// # Uma nota sobre o relógio
//
// A conferência tem DUAS perguntas porque o contexto responde a primeira com
// atraso: o cancelamento por prazo é feito por um timer, e no Windows a
// granularidade do timer é de milissegundos. Entre o instante em que o prazo
// passa e o instante em que `ctx.Err()` deixa de ser nil cabe uma etapa inteira
// do cálculo começando com o orçamento já no vermelho. Perguntar o PRAZO direto
// fecha essa janela — e quem responde continua sendo o relógio, nunca um erro
// alheio.
//
// ⚠️ Invariante que sustenta a tradução: o único prazo no contexto desta fase é
// o transaction.PlanTimeout imposto por planejarComPrazo. O projeto não tem
// middleware de prazo por requisição (conferido em internal/platform/httpserver
// e cmd/api). No dia em que tiver, esta função precisa distinguir o prazo DELE
// do nosso antes de continuar chamando os dois de ErrPlanTimeout.
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
//     borda responde 422 em `fields.month`;
//   - cancelamento é o CLIENTE que foi embora (aba fechada, app morto). Não é
//     422 (não há a quem orientar) nem incidente: sobe cru e a borda o
//     reconhece pelo contexto da REQUISIÇÃO, registrando em INFO.
//
// O motivo original continua na cadeia nos dois casos: o log precisa dele, e
// nenhum dos dois carrega dado da casa.
func erroDeParada(motivo error, etapa string) error {
	if errors.Is(motivo, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %s: %w", ErrPlanTimeout, etapa, motivo)
	}
	return fmt.Errorf("detecção de investimentos interrompida: %s: %w", etapa, motivo)
}

// traduzirOrcamento transforma o estouro do orçamento de casamento por
// palavra-chave num erro de DOMÍNIO — 422 na borda, como todo outro teto desta
// família de rotas. Qualquer outro erro sobe embrulhado com contexto.
//
// Reaproveita transaction.ErrKeywordMatchTooCostly de propósito: é o mesmo
// acontecimento, na mesma engrenagem, e a tela mostra a mesma ação. Um erro
// novo aqui seria um segundo nome para a mesma coisa.
func traduzirOrcamento(err error, contexto string) error {
	if errors.Is(err, textmatch.ErrWorkBudgetExceeded) {
		return fmt.Errorf("%w: %w", transaction.ErrKeywordMatchTooCostly, err)
	}
	return fmt.Errorf("%s: %w", contexto, err)
}

// pontuar classifica UMA linha e a acomoda no plano.
//
// As quatro saídas possíveis, e por que cada uma existe:
//
//  1. SEM categoria + vencedora de investimento → marcada (`items`);
//  2. SEM categoria + qualquer outro desfecho → `unmatchedItems`, com o
//     motivo. É por isso que `marked` + `unmatched` cobrem exatamente as
//     linhas sem categoria do mês quando a flag está desligada;
//  3. COM categoria de natureza income/expense + vencedora de investimento →
//     `alreadyCategorizedItems` (sem a flag) ou marcada (com a flag);
//  4. qualquer outro caso COM categoria → NENHUMA lista.
//
// O caso 4 é o que a entrega anterior pagou caro para aprender: o lançamento
// que já tem categoria de INVESTIMENTO não aparece em lugar nenhum, porque o
// `WHERE` da troca recusaria (a natureza dele não está na allowlist) e
// oferecê-lo seria a prévia prometendo o que a escrita não cumpre. O
// lançamento categorizado que não bate com palavra-chave de investimento
// também não aparece: não é assunto desta tela.
func (p *planoDeDeteccao) pontuar(conjunto *classify.Set, tax taxonomia, comuns map[string]struct{}, linha transaction.CategorizableRow, overwrite bool) error {
	r, err := conjunto.SuggestCategory(linha.Kind, linha.DescriptionNorm)
	if err != nil {
		// Orçamento estourado: a operação INTEIRA morre. Nada de "esta linha
		// fica sem sugestão e o resto segue" — isso seria resultado truncado
		// em silêncio, e a pessoa não teria como saber quais linhas perderam a
		// chance de casar.
		return err
	}

	temCategoria := linha.CategoryID != nil && *linha.CategoryID != ""

	if !r.Matched() {
		if !temCategoria {
			p.naoMarcado(linha, motivoDoMotor(r.Reason))
		}
		return nil
	}

	destino := r.Match.OwnerID
	kindDestino := tax.porID[destino].Kind

	if !marcaInvestimento(linha.Kind, kindDestino) {
		if !temCategoria {
			p.naoMarcado(linha, motivoDaVencedora(kindDestino))
		}
		return nil
	}

	fluxo, ok := fluxoDoLancamento(linha.Kind)
	if !ok {
		// O repositório já filtra `kind IN (income, expense)`; chegar aqui é
		// banco em estado que a aplicação não produz. Não marcar é o desfecho
		// seguro — a alternativa seria publicar um item com `flow` fora do
		// enum do contrato.
		if !temCategoria {
			p.naoMarcado(linha, ReasonBelowThreshold)
		}
		return nil
	}

	if !temCategoria {
		p.semCategoria[destino] = append(p.semCategoria[destino], linha.ID)
		p.ladoPorDestino[destino] = linha.Kind
		p.marked++
		p.adicionarItem(linha, destino, tax.porID[destino].Name, fluxo, r.Match)
		return nil
	}

	atual := *linha.CategoryID
	if _, trocavel := comuns[atual]; !trocavel {
		// Caso 4: já marcado como investimento, ou apontando categoria que
		// esta casa não tem. O `WHERE` recusaria — e prometer o que ele recusa
		// é o defeito "prévia mentindo".
		return nil
	}

	if !overwrite {
		p.alreadyCategorized++
		if len(p.alreadyItems) < MaxListed {
			p.alreadyItems = append(p.alreadyItems, DetectAlreadyCategorizedItemView{
				ID:                  linha.ID,
				Description:         linha.Description,
				CurrentCategoryID:   atual,
				CurrentCategoryName: tax.porID[atual].Name,
				CategoryID:          destino,
				CategoryName:        tax.porID[destino].Name,
				Flow:                fluxo,
				MatchScore:          r.Match.Score,
			})
		}
		return nil
	}

	p.paraTrocar[destino] = append(p.paraTrocar[destino], linha.ID)
	p.ladoPorDestino[destino] = linha.Kind
	p.marked++
	p.adicionarItem(linha, destino, tax.porID[destino].Name, fluxo, r.Match)
	return nil
}

// adicionarItem soma o item à prévia, respeitando o teto da lista. A CONTAGEM
// já foi somada por quem chama: ela é sempre completa, a lista é amostra.
func (p *planoDeDeteccao) adicionarItem(linha transaction.CategorizableRow, destino, nome, fluxo string, m textmatch.Match) {
	if len(p.items) >= MaxListed {
		return
	}
	p.items = append(p.items, DetectItemView{
		ID:             linha.ID,
		Description:    linha.Description,
		CategoryID:     destino,
		CategoryName:   nome,
		Flow:           fluxo,
		MatchScore:     m.Score,
		MatchedKeyword: m.Keyword,
	})
}

// naoMarcado soma UMA linha sem categoria à contagem de `unmatched` e, se
// couber, à lista.
func (p *planoDeDeteccao) naoMarcado(linha transaction.CategorizableRow, motivo string) {
	p.unmatched++
	if len(p.unmatchedItems) >= MaxListed {
		return
	}
	p.unmatchedItems = append(p.unmatchedItems, DetectUnmatchedItemView{
		ID:          linha.ID,
		Description: linha.Description,
		Reason:      motivo,
	})
}

// marcaInvestimento decide se a categoria vencedora marca a linha como aporte
// ou resgate.
//
// São DUAS perguntas, e as duas precisam ser sim:
//
//   - a natureza da vencedora é `investment` ou `redemption`? (é o que
//     distingue esta rota do auto-categorize);
//   - o pareamento vale? (category.AceitaLancamento — a ÚNICA fonte da verdade
//     do que um lançamento pode receber, ADR-029b).
//
// A segunda é defesa em profundidade: o `classify` já seleciona o matcher pelo
// lado do dinheiro, então uma despesa nunca deveria vencer com categoria de
// resgate. Mas quem grava aqui é esta rota, e sugerir o que a ESCRITA vai
// recusar é prometer o que não se cumpre — a checagem custa uma comparação.
func marcaInvestimento(transactionKind, categoryKind string) bool {
	if categoryKind != category.KindInvestment && categoryKind != category.KindRedemption {
		return false
	}
	return category.AceitaLancamento(transactionKind, categoryKind)
}

// motivoDoMotor traduz a razão do matcher para o vocabulário do contrato. Só
// dois valores do motor chegam aqui; qualquer outra coisa vira
// below_threshold, que é o "não casou" genérico.
func motivoDoMotor(r textmatch.Reason) string {
	if r == textmatch.ReasonAmbiguous {
		return ReasonAmbiguous
	}
	return ReasonBelowThreshold
}

// motivoDaVencedora explica por que uma linha que TEVE vencedora continua sem
// marca de investimento.
//
// `other_category` é o caso real e o motivo de o enum ter três valores e não
// dois: "Mercado do seu José" bateu com a categoria Mercado a 100, e dizer
// `below_threshold` seria mostrar "não bateu com nada" para uma linha que
// bateu. Quem a categoriza é POST /transactions/auto-categorize.
//
// Vencedora cuja natureza esta casa não conhece (categoria que sumiu da
// taxonomia entre duas leituras) cai em `below_threshold`: afirmar
// `other_category` seria afirmar uma natureza que não se leu.
func motivoDaVencedora(categoryKind string) string {
	if categoryKind == category.KindIncome || categoryKind == category.KindExpense {
		return ReasonOtherCategory
	}
	return ReasonBelowThreshold
}
