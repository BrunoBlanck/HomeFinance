package transaction

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
)

// POST /transfers/detect (spec 0005 §13, ADR-028).
//
// A regra que este arquivo sustenta, em uma frase: o servidor só PAREIA o que
// já está gravado — uma `expense` numa conta com uma `income` em outra, mesmo
// valor, ±DedupWindowDays, escolhidas pela palavra-chave de conta — e nunca
// inventa a perna que falta. Candidata sem espelho fica como está e aparece
// na prévia como "sem par": criar perna sintética aqui duplicaria dinheiro na
// próxima importação da outra conta (ADR-028a). Como no auto-categorize, a
// prévia nunca é confiada: a confirmação recalcula tudo dentro de UMA
// transação e converte com UPDATE condicional, tudo ou nada.

// TransferUnpairedNoMirror é o único motivo de "sem par" desta emenda
// (TransferDetectUnpairedReason do contrato): bate com palavra-chave de
// conta, mas não há, em outra conta ativa, um lançamento vivo de sentido
// oposto, mesmo valor e data a ±DedupWindowDays que ainda esteja livre.
const TransferUnpairedNoMirror = "no_mirror"

// TransferDetectInput é o pedido.
type TransferDetectInput struct {
	// Month é "AAAA-MM", competência, obrigatório.
	Month string

	// DryRun true só calcula; false converte. O handler exige o campo no
	// corpo (ausente é 400): o zero value de bool seria "converter", e
	// converter por um campo esquecido é exatamente o que não pode acontecer.
	DryRun bool
}

// TransferDetectView é a resposta — schema TransferDetectResult do contrato.
//
// Em prévia, Paired é quantos pares SERIAM convertidos e as listas vêm
// preenchidas (até MaxTransferDetectListed cada); na execução real, Paired é
// o número de pares CONVERTIDOS (cada um conferido com duas linhas afetadas) e
// as listas vêm vazias. Unpaired é sempre a contagem completa das candidatas
// sem espelho.
type TransferDetectView struct {
	Month         string                       `json:"month"`
	Paired        int64                        `json:"paired"`
	Unpaired      int64                        `json:"unpaired"`
	Items         []TransferDetectItemView     `json:"items"`
	UnpairedItems []TransferDetectUnpairedView `json:"unpairedItems"`
}

// TransferDetectItemView é UM par da prévia (schema TransferDetectItem): a
// `expense` vira a perna de saída e a `income` a de entrada. OccurredOn e
// Description são os da perna de SAÍDA — o que GET /transfers vai mostrar
// depois da conversão; MatchedKeyword e MatchScore descrevem a CANDIDATA que
// decidiu, que pode ser qualquer uma das duas pernas.
type TransferDetectItemView struct {
	OutTransactionID string     `json:"outTransactionId"`
	InTransactionID  string     `json:"inTransactionId"`
	OccurredOn       civil.Date `json:"occurredOn"`
	FromAccountID    string     `json:"fromAccountId"`
	FromAccountName  string     `json:"fromAccountName"`
	ToAccountID      string     `json:"toAccountId"`
	ToAccountName    string     `json:"toAccountName"`
	AmountCents      int64      `json:"amountCents"`
	Description      string     `json:"description"`
	MatchedKeyword   string     `json:"matchedKeyword"`
	MatchScore       int        `json:"matchScore"`
}

// TransferDetectUnpairedView é uma candidata que CONTINUA como está (schema
// TransferDetectUnpairedItem): bate com palavra-chave de conta, mas não tem a
// outra perna gravada. Nada é feito com ela.
type TransferDetectUnpairedView struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	AccountID      string     `json:"accountId"`
	AccountName    string     `json:"accountName"`
	OccurredOn     civil.Date `json:"occurredOn"`
	AmountCents    int64      `json:"amountCents"`
	Description    string     `json:"description"`
	MatchedKeyword string     `json:"matchedKeyword"`
	Reason         string     `json:"reason"`
}

// parDeTransferencia é o que a execução real converte: a linha que vira
// transfer_out e a que vira transfer_in, cada uma com a SUA conta.
//
// As contas viajam junto porque o UPDATE condicional as reconfere no banco
// (achado A4 da revisão): "as duas pernas estão em contas diferentes" é
// invariante do par (ADR-016), e invariante que só existe na memória do
// serviço some junto com o primeiro defeito no pareamento.
type parDeTransferencia struct {
	outID        string
	outAccountID string
	inID         string
	inAccountID  string
}

// planoDeTransferencias é o resultado do recálculo: os pares que seriam
// convertidos e a prévia já montada. É o mesmo cálculo em dryRun e na
// execução real — só a etapa seguinte muda.
type planoDeTransferencias struct {
	pares         []parDeTransferencia
	unpaired      int64
	items         []TransferDetectItemView
	unpairedItems []TransferDetectUnpairedView
}

// DetectTransfers pareia as receitas e despesas do mês que as palavras-chave
// de conta marcam como transferência e as converte em pares
// transfer_out/transfer_in.
//
// DryRun: calcula e devolve a prévia; NADA é escrito nem auditado. Execução
// real: tudo — carga das palavras, contas, candidatas, janela de espelhos,
// pareamento, UPDATE condicional por par e auditoria — acontece DENTRO de uma
// transação, para que o que foi lido seja o que foi escrito; um par que não
// afeta exatamente duas linhas desfaz tudo (ErrTransferConversionConflict →
// 409). Idempotente por construção: a segunda execução não encontra
// `income`/`expense` sem grupo para os mesmos pares, e Paired volta 0.
func (s *Service) DetectTransfers(ctx context.Context, ator Actor, in TransferDetectInput) (TransferDetectView, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return TransferDetectView{}, ErrNotFound
	}
	if _, err := ParseMonth(in.Month); err != nil {
		return TransferDetectView{}, err
	}
	if s.classifier == nil {
		// Falha de ligação (cmd/api monta o loader), não de entrada. Nunca
		// panic no caminho de request: erro genérico, 500, e o log diz o quê.
		return TransferDetectView{}, errors.New("classificador de palavras-chave não configurado")
	}

	if in.DryRun {
		plano, err := s.planejarTransferenciasComPrazo(ctx, householdID, in.Month)
		if err != nil {
			return TransferDetectView{}, err
		}
		return TransferDetectView{
			Month:         in.Month,
			Paired:        int64(len(plano.pares)),
			Unpaired:      plano.unpaired,
			Items:         plano.items,
			UnpairedItems: plano.unpairedItems,
		}, nil
	}

	var convertidos int64
	var semPar int64
	err := s.tx.Do(ctx, func(ctx context.Context) error {
		plano, err := s.planejarTransferenciasComPrazo(ctx, householdID, in.Month)
		if err != nil {
			return err
		}
		semPar = plano.unpaired

		agora := s.clock()
		for _, par := range ordemDeTravamento(plano.pares) {
			// Grupo NOVO por par, gerado aqui e nunca vindo do cliente — o
			// corpo não tem ids. Cada conversão confere as duas linhas no
			// banco; a primeira que não bater desfaz a transação inteira.
			err := s.repo.ConvertToTransferPair(ctx, householdID, TransferPairConversion{
				OutID:           par.outID,
				OutAccountID:    par.outAccountID,
				InID:            par.inID,
				InAccountID:     par.inAccountID,
				TransferGroupID: s.ids(),
				UpdatedAt:       agora,
			})
			if err != nil {
				return fmt.Errorf("convertendo par em transferência: %w", err)
			}
			convertidos++
		}

		// Uma entrada por execução real, com o MÊS como entidade — sem
		// descrição, sem valor, sem palavra-chave (ADR-028e). Dentro da
		// transação: se o rastro não couber, a conversão não vale.
		return s.registrarEm(ctx, ator, audit.ActionTransactionTransfersDetected, audit.EntityTransactionMonth, in.Month)
	})
	if err != nil {
		return TransferDetectView{}, err
	}

	return TransferDetectView{
		Month:    in.Month,
		Paired:   convertidos,
		Unpaired: semPar,
		// Listas VAZIAS, e não nulas: o contrato publica arrays, e a tela não
		// distingue "ausente" de "acabou".
		Items:         []TransferDetectItemView{},
		UnpairedItems: []TransferDetectUnpairedView{},
	}, nil
}

// ordemDeTravamento devolve os pares na ordem em que as linhas devem ser
// travadas: pelo MENOR id do par e, no empate, pelo maior (achado A5 da
// revisão de segurança).
//
// A ordem de leitura é a das candidatas do MÊS, e duas execuções simultâneas
// de meses diferentes cujas janelas se cruzam podem alcançar as mesmas linhas
// em ordens opostas — uma trava X e pede Y, a outra trava Y e pede X. No
// PostgreSQL isso é deadlock: o banco mata uma das transações, e o erro
// nativo cairia no ramo genérico do handler como 500, quando o certo seria
// 409 (o estado mudou, peça a prévia de novo).
//
// Uma ordem GLOBAL — que não depende do mês, da competência nem da ordem de
// leitura — faz as duas execuções travarem sempre na mesma sequência: a
// segunda espera a primeira e segue, em vez de as duas se enroscarem. O id é
// a chave certa porque é o que o UPDATE usa; a ordenação é feita sobre uma
// CÓPIA, para a prévia e os itens continuarem na ordem de leitura que a tela
// mostra.
func ordemDeTravamento(pares []parDeTransferencia) []parDeTransferencia {
	ordenados := slices.Clone(pares)
	slices.SortStableFunc(ordenados, func(a, b parDeTransferencia) int {
		if c := strings.Compare(min(a.outID, a.inID), min(b.outID, b.inID)); c != 0 {
			return c
		}
		return strings.Compare(max(a.outID, a.inID), max(b.outID, b.inID))
	})
	return ordenados
}

// planejarTransferenciasComPrazo é planejarTransferencias com o prazo próprio
// da fase de cálculo (PlanTimeout).
//
// Diferente do auto-categorize, aqui o cálculo CONTINUA dentro da transação na
// execução real: o pareamento decide COM QUEM cada linha casa, e o
// ConvertToTransferPair exige que as duas pernas continuem exatamente como
// foram lidas (ErrTransferConversionConflict → 409). Tirar a leitura da
// transação trocaria um 409 honesto por um par montado sobre estado velho. O
// custo — uma conexão do pool presa durante o cálculo — é o que o Burst 3
// desta rota já limitava, e agora o prazo fecha por cima.
func (s *Service) planejarTransferenciasComPrazo(ctx context.Context, householdID, month string) (*planoDeTransferencias, error) {
	ctx, cancel := s.ctxDoPlano(ctx)
	defer cancel()
	return s.planejarTransferencias(ctx, householdID, month)
}

// planejarTransferencias faz o recálculo em consultas contadas, nenhuma por
// linha: o conjunto de palavras-chave (classify.Load, 4), as contas ATIVAS
// (1), as candidatas do mês de competência (1, teto + 1) e — só se alguma
// candidata casou — o pool de espelhos da casa inteira na janela real das
// candidatas (1, teto + 1). Pontua cada linha uma vez e entrega ao pareamento
// puro.
func (s *Service) planejarTransferencias(ctx context.Context, householdID, month string) (*planoDeTransferencias, error) {
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

	// Só contas ATIVAS: o par nasce entre contas ativas e distintas (spec
	// 0005 §13.4), e a palavra de conta arquivada já ficou fora do conjunto.
	// O mapa serve de allowlist e de fonte dos nomes da prévia — sem
	// consulta por linha.
	contas, err := s.accounts.List(ctx, householdID, false)
	if err != nil {
		return nil, fmt.Errorf("carregando contas da casa: %w", err)
	}
	ativas := make(map[string]string, len(contas))
	for i := range contas {
		if contas[i].ArchivedAt == nil && contas[i].DeletedAt == nil {
			ativas[contas[i].ID] = contas[i].Name
		}
	}

	candidatas, err := s.repo.ListTransferCandidates(ctx, householdID, month, MaxTransferDetectRows+1)
	if err != nil {
		return nil, fmt.Errorf("listando candidatas a transferência: %w", err)
	}
	if len(candidatas) > MaxTransferDetectRows {
		return nil, fmt.Errorf("%w: máximo de %d por execução", ErrTooManyTransferCandidates, MaxTransferDetectRows)
	}

	// Pontuação de cada candidata contra TODAS as contas ativas (ADR-028b);
	// só quem casou entra no mapa, e é sobre elas que a janela de espelhos é
	// medida — a faixa REAL das candidatas, não as bordas do mês (ADR-028c).
	pontuacoes := make(map[string]textmatch.Match, len(candidatas))
	var minData, maxData civil.Date
	for i := range candidatas {
		if i%64 == 0 {
			// Cliente foi embora ou o prazo venceu: parar de gastar CPU com
			// uma resposta que ninguém vai ler. Quem CLASSIFICA os dois casos
			// é conferirPrazo — prazo vira ErrPlanTimeout (422 em `month`),
			// cancelamento sobe cru (INFO na borda).
			if err := conferirPrazo(ctx, "pontuando candidatas a transferência"); err != nil {
				return nil, err
			}
		}
		r, err := conjunto.MatchAccount(candidatas[i].DescriptionNorm)
		if err != nil {
			// Orçamento de casamento estourado: a operação inteira morre, 422.
			// Nunca "pontua o que deu e pareia o resto" — isso converteria um
			// subconjunto arbitrário do mês em transferência.
			return nil, traduzirOrcamento(err, "pontuando candidatas a transferência")
		}
		if !r.Matched() {
			continue
		}
		pontuacoes[candidatas[i].ID] = r.Match
		dia := candidatas[i].OccurredOn
		if minData.IsZero() || dia.Before(minData) {
			minData = dia
		}
		if maxData.IsZero() || dia.After(maxData) {
			maxData = dia
		}
	}

	plano := &planoDeTransferencias{
		pares:         []parDeTransferencia{},
		items:         []TransferDetectItemView{},
		unpairedItems: []TransferDetectUnpairedView{},
	}
	if len(pontuacoes) == 0 {
		// Nenhuma candidata: não há janela a consultar nem par a montar.
		return plano, nil
	}

	espelhos, err := s.repo.IncomeExpenseInWindow(ctx, householdID, minData, maxData, MaxTransferMirrorRows+1)
	if err != nil {
		return nil, fmt.Errorf("carregando janela de espelhos: %w", err)
	}
	if len(espelhos) > MaxTransferMirrorRows {
		return nil, fmt.Errorf("%w: máximo de %d linhas na janela de espelhos", ErrTooManyTransferCandidates, MaxTransferMirrorRows)
	}

	// O espelho de uma candidata "própria conta" também precisa qualificar-se
	// (ADR-028b), e o desempate prefere o espelho que se qualifica — então
	// cada linha do pool é pontuada uma vez. As candidatas já pontuadas não
	// são repetidas; o matcher memoriza por description_norm.
	for i := range espelhos {
		if i%64 == 0 {
			if err := conferirPrazo(ctx, "pontuando espelhos de transferência"); err != nil {
				return nil, err
			}
		}
		if _, ok := pontuacoes[espelhos[i].ID]; ok {
			continue
		}
		r, err := conjunto.MatchAccount(espelhos[i].DescriptionNorm)
		if err != nil {
			return nil, traduzirOrcamento(err, "pontuando espelhos de transferência")
		}
		if r.Matched() {
			pontuacoes[espelhos[i].ID] = r.Match
		}
	}

	return parearTransferencias(ctx, candidatas, espelhos, ativas, pontuacoes)
}

// chaveDeEspelho indexa o pool por (kind, valor): a candidata procura o
// balde do kind OPOSTO com o MESMO valor. Um mês de 10.000 candidatas contra
// um pool de 20.000 linhas não pode virar produto cartesiano.
type chaveDeEspelho struct {
	kind  string
	cents int64
}

// espelho é uma linha do pool. A reivindicação não mora aqui, e sim num mapa
// por id compartilhado com as candidatas: a mesma linha aparece nas duas
// listas (as candidatas do mês estão dentro do pool), e marcar só uma das
// cópias deixaria a outra livre para um segundo par.
type espelho struct {
	linha *TransferCandidateRow
}

// indiceDeEspelhos é o pool indexado por (kind oposto, valor) e, DENTRO
// disso, por dia.
//
// O segundo nível não é micro-otimização: a janela do espelho é fixa em
// ±DedupWindowDays, então a candidata só precisa olhar 7 dias. Sem ele, um
// mês em que nada pareia faz cada candidata varrer o balde inteiro — 10.000
// candidatas × 20.000 espelhos do mesmo valor eram 5 s de CPU por
// requisição, com o agravante de a execução real segurar uma conexão do pool
// numa transação aberta (achado A2 da revisão de segurança).
type indiceDeEspelhos map[chaveDeEspelho]map[civil.Date][]*espelho

// orcamentoDeComparacoes é o teto DURO de comparações de um pareamento.
//
// O índice por dia resolve o caso realista; este orçamento fecha o caso
// patológico que sobra (tudo no mesmo valor E nos mesmos dias), em que o
// índice não tem por onde separar. Estourar é 422, como todo outro teto
// desta feature — nunca execução parcial, porque um pareamento interrompido
// no meio converteria um subconjunto arbitrário.
type orcamentoDeComparacoes struct{ restante int }

// gastar consome uma comparação. Devolve false quando o orçamento acabou.
func (o *orcamentoDeComparacoes) gastar() bool {
	if o.restante <= 0 {
		return false
	}
	o.restante--
	return true
}

func (o *orcamentoDeComparacoes) esgotado() bool { return o.restante <= 0 }

// parearTransferencias é o pareamento PURO (ADR-028c), testado por tabela —
// a mesma ideia de pairExistingLegs do importador, entre lançamentos vivos.
//
// Percorre as candidatas em ordem (occurred_on, id). Candidata é a linha cuja
// pontuação está em `pontuacoes` e cuja conta está em `ativas`. A dona da
// melhor pontuação decide a semântica: OUTRA conta K → contraparte conhecida,
// o espelho tem de estar em K (bater com palavra não é exigido dele); a
// PRÓPRIA conta → marcador, contraparte desconhecida, o espelho tem de estar
// em outra conta ativa E qualificar-se também (a melhor pontuação dele aponta
// para a própria conta dele ou para a conta da candidata) — sem isso, "Pix
// enviado a mim mesmo" casaria com qualquer receita de mesmo valor na
// vizinhança.
//
// Espelho: linha do pool de kind oposto, mesmo valor, em outra conta ativa, a
// até DedupWindowDays de distância, ainda não reivindicada. Entre vários:
// prefere o que também se QUALIFICA (e não apenas "bate com alguma palavra":
// um espelho cuja melhor pontuação aponta para uma TERCEIRA conta está
// dizendo que o par dele é outro, e preferi-lo seria preferir a leitura
// errada); depois menor distância em dias; depois menor occurred_on; depois
// menor id. Candidata e espelho são REIVINDICADOS — cada linha participa de
// um único par, em qualquer papel. Nunca inventa perna.
//
// A lista de "sem par" é montada numa SEGUNDA passada, depois de todos os
// pares fecharem, e não dentro do laço — esta é a correção do achado A1 da
// revisão de segurança. A regra de contraparte é assimétrica (`t` recusar `u`
// não implica `u` recusar `t`), então uma candidata declarada "sem par" no
// meio do laço podia ser escolhida como espelho por uma candidata POSTERIOR:
// a prévia prometia "fica como está", a conversão apagava a categoria dela, e
// `paired + unpaired` contava a mesma linha duas vezes. Listar no fim mantém
// o par legítimo (spec §13.1.5) e torna `pares` e `unpairedItems` conjuntos
// disjuntos por construção.
//
// As listas param em MaxTransferDetectListed; as contagens são completas.
func parearTransferencias(
	ctx context.Context,
	candidatas []TransferCandidateRow,
	pool []TransferCandidateRow,
	ativas map[string]string,
	pontuacoes map[string]textmatch.Match,
) (*planoDeTransferencias, error) {
	plano := &planoDeTransferencias{
		pares:         []parDeTransferencia{},
		items:         []TransferDetectItemView{},
		unpairedItems: []TransferDetectUnpairedView{},
	}

	indice := make(indiceDeEspelhos, len(pool))
	for i := range pool {
		l := &pool[i]
		if l.ID == "" || l.OccurredOn.IsZero() {
			continue
		}
		if l.Kind != KindIncome && l.Kind != KindExpense {
			continue // perna de transferência nunca é espelho: já tem par
		}
		k := chaveDeEspelho{kind: l.Kind, cents: l.AmountCents}
		porDia := indice[k]
		if porDia == nil {
			porDia = map[civil.Date][]*espelho{}
			indice[k] = porDia
		}
		porDia[l.OccurredOn] = append(porDia[l.OccurredOn], &espelho{linha: l})
	}
	// Cada dia em ordem de id: é o último critério de desempate, e a busca
	// PARA no primeiro espelho qualificado que encontra — se a lista não
	// estivesse ordenada, a escolha dependeria da ordem em que o repositório
	// devolveu as linhas. O repositório já ordena por (occurred_on, id), mas
	// esta função é pura e não pode depender disso.
	for _, porDia := range indice {
		for dia := range porDia {
			slices.SortStableFunc(porDia[dia], func(a, b *espelho) int {
				return strings.Compare(a.linha.ID, b.linha.ID)
			})
		}
	}

	// reivindicadas cobre os DOIS papéis, por id: a candidata que já foi
	// espelho de outra não é percorrida de novo, o espelho usado não serve a
	// mais ninguém, e a candidata já pareada não é encontrada como espelho
	// pela cópia dela que está no pool.
	reivindicadas := make(map[string]struct{}, len(candidatas))

	// Ordem (occurred_on, id) reafirmada aqui, e não só confiada ao
	// repositório: é ela que decide quem reivindica primeiro.
	ordem := make([]int, len(candidatas))
	for i := range ordem {
		ordem[i] = i
	}
	slices.SortStableFunc(ordem, func(a, b int) int {
		if c := candidatas[a].OccurredOn.Compare(candidatas[b].OccurredOn); c != 0 {
			return c
		}
		return strings.Compare(candidatas[a].ID, candidatas[b].ID)
	})

	orcamento := &orcamentoDeComparacoes{restante: MaxTransferPairComparisons}

	for posicao, i := range ordem {
		if posicao%500 == 0 {
			// Cliente foi embora ou o prazo venceu: parar de gastar CPU com
			// uma resposta que ninguém vai ler. O WriteTimeout do servidor
			// não cancela esta goroutine — só o contexto cancela. Quem
			// CLASSIFICA os dois casos é conferirPrazo — prazo vira
			// ErrPlanTimeout (422 em `month`), cancelamento sobe cru (INFO na
			// borda).
			if err := conferirPrazo(ctx, "pareando candidatas"); err != nil {
				return nil, err
			}
		}

		t := &candidatas[i]
		pontuacao, elegivel := candidataElegivel(t, ativas, pontuacoes)
		if !elegivel {
			continue
		}
		if _, ja := reivindicadas[t.ID]; ja {
			continue
		}

		melhor := procurarEspelho(t, pontuacao, indice, ativas, pontuacoes, reivindicadas, orcamento)
		if orcamento.esgotado() {
			return nil, fmt.Errorf("%w: o pareamento passou de %d comparações",
				ErrTooManyTransferCandidates, MaxTransferPairComparisons)
		}
		if melhor == nil {
			continue // a lista de "sem par" sai da segunda passada
		}

		reivindicadas[t.ID] = struct{}{}
		reivindicadas[melhor.linha.ID] = struct{}{}

		// O sentido do par vem do kind: a expense vira transfer_out, a income
		// vira transfer_in — em qualquer ordem que T e M tenham chegado.
		saida, entrada := t, melhor.linha
		if saida.Kind != KindExpense {
			saida, entrada = entrada, saida
		}
		plano.pares = append(plano.pares, parDeTransferencia{
			outID:        saida.ID,
			outAccountID: saida.AccountID,
			inID:         entrada.ID,
			inAccountID:  entrada.AccountID,
		})
		if len(plano.items) < MaxTransferDetectListed {
			plano.items = append(plano.items, TransferDetectItemView{
				OutTransactionID: saida.ID,
				InTransactionID:  entrada.ID,
				OccurredOn:       saida.OccurredOn,
				FromAccountID:    saida.AccountID,
				FromAccountName:  ativas[saida.AccountID],
				ToAccountID:      entrada.AccountID,
				ToAccountName:    ativas[entrada.AccountID],
				AmountCents:      saida.AmountCents,
				Description:      saida.Description,
				MatchedKeyword:   pontuacao.Keyword,
				MatchScore:       pontuacao.Score,
			})
		}
	}

	// Segunda passada: candidata elegível que terminou FORA de `reivindicadas`
	// — em nenhum par, nem como T nem como M — é a que "fica como está".
	listadas := make(map[string]struct{}, len(candidatas))
	for posicao, i := range ordem {
		if posicao%500 == 0 {
			if err := conferirPrazo(ctx, "listando candidatas sem par"); err != nil {
				return nil, err
			}
		}

		t := &candidatas[i]
		pontuacao, elegivel := candidataElegivel(t, ativas, pontuacoes)
		if !elegivel {
			continue
		}
		if _, pareada := reivindicadas[t.ID]; pareada {
			continue
		}
		// O repositório devolve ids únicos; a guarda existe para uma lista
		// repetida nunca inflar a contagem que a tela mostra.
		if _, ja := listadas[t.ID]; ja {
			continue
		}
		listadas[t.ID] = struct{}{}

		plano.unpaired++
		if len(plano.unpairedItems) < MaxTransferDetectListed {
			plano.unpairedItems = append(plano.unpairedItems, TransferDetectUnpairedView{
				ID:             t.ID,
				Kind:           t.Kind,
				AccountID:      t.AccountID,
				AccountName:    ativas[t.AccountID],
				OccurredOn:     t.OccurredOn,
				AmountCents:    t.AmountCents,
				Description:    t.Description,
				MatchedKeyword: pontuacao.Keyword,
				Reason:         TransferUnpairedNoMirror,
			})
		}
	}

	return plano, nil
}

// candidataElegivel diz se a linha participa do pareamento e devolve a
// pontuação que a qualificou: receita ou despesa, de conta ATIVA, que bateu
// com palavra-chave de conta. Está numa função porque as duas passadas
// precisam da MESMA definição — se divergirem, uma linha some das duas listas
// ou aparece nas duas.
func candidataElegivel(t *TransferCandidateRow, ativas map[string]string, pontuacoes map[string]textmatch.Match) (textmatch.Match, bool) {
	pontuacao, casou := pontuacoes[t.ID]
	if !casou {
		return textmatch.Match{}, false // não bateu com palavra de conta
	}
	if _, ativa := ativas[t.AccountID]; !ativa {
		return textmatch.Match{}, false // par só nasce entre contas ativas
	}
	if t.Kind != KindIncome && t.Kind != KindExpense {
		return textmatch.Match{}, false
	}
	return pontuacao, true
}

// procurarEspelho escolhe o melhor espelho livre para a candidata t — ou nil.
//
// Varre só os 2·DedupWindowDays + 1 dias da janela, no balde do kind oposto e
// do mesmo valor, compactando cada dia visitado (quem já foi reivindicado sai
// de vez). Cada comparação gasta do orçamento; esgotá-lo interrompe a busca, e
// quem chama transforma isso em 422.
func procurarEspelho(
	t *TransferCandidateRow,
	pontuacao textmatch.Match,
	indice indiceDeEspelhos,
	ativas map[string]string,
	pontuacoes map[string]textmatch.Match,
	reivindicadas map[string]struct{},
	orcamento *orcamentoDeComparacoes,
) *espelho {
	porDia := indice[chaveDeEspelho{kind: kindOposto(t.Kind), cents: t.AmountCents}]
	if porDia == nil {
		return nil
	}
	propria := pontuacao.OwnerID == t.AccountID

	// Os dias são visitados em ordem de DESEMPATE — distância crescente e,
	// dentro da mesma distância, o dia anterior antes do posterior (menor
	// occurred_on vence) —, e cada lista de dia já está ordenada por id. A
	// ordem de visita é, portanto, a ordem do próprio critério: o PRIMEIRO
	// espelho qualificado que aparecer vence todos os que viriam depois, e a
	// busca pode parar ali. É o que torna barato o caso "tudo pareia", em que
	// varrer o resto do dia não mudaria a escolha (achado A2).
	var melhorSemQualificar *espelho
	var dias [2]civil.Date
	for offset := range DedupWindowDays + 1 {
		for _, dia := range diasNaDistancia(t.OccurredOn, offset, &dias) {
			doDia := porDia[dia]
			if len(doDia) == 0 {
				continue
			}

			// Compacta o dia: quem já foi reivindicado sai de vez. Sem isso,
			// um mês com centenas de lançamentos do MESMO valor no MESMO dia
			// faria cada candidata varrer de novo tudo o que as anteriores já
			// consumiram.
			//
			// A compactação gasta do MESMO orçamento da busca: ela é trabalho
			// por linha, e um teto que só conta a comparação deixaria de fora
			// justamente o laço que percorre a lista inteira.
			livres := doDia[:0]
			for _, m := range doDia {
				if !orcamento.gastar() {
					porDia[dia] = livres
					return nil
				}
				if _, usada := reivindicadas[m.linha.ID]; !usada {
					livres = append(livres, m)
				}
			}
			porDia[dia] = livres

			for _, m := range livres {
				if !orcamento.gastar() {
					return nil
				}
				if m.linha.ID == t.ID || m.linha.AccountID == t.AccountID {
					continue
				}
				if _, ativa := ativas[m.linha.AccountID]; !ativa {
					continue
				}

				// O espelho "se qualifica" quando a melhor pontuação DELE
				// aponta para a própria conta dele (marcador) ou para a conta
				// da candidata (contraparte que a nomeia) — nunca para uma
				// terceira conta, que seria ele dizendo que o par dele é
				// outro.
				pm, tem := pontuacoes[m.linha.ID]
				qualifica := tem && (pm.OwnerID == m.linha.AccountID || pm.OwnerID == t.AccountID)

				if propria {
					if !qualifica {
						continue
					}
				} else if m.linha.AccountID != pontuacao.OwnerID {
					continue
				}

				if qualifica {
					// Vence tudo o que ainda viria: qualificado ganha de não
					// qualificado, e os seguintes têm distância maior, data
					// maior ou id maior.
					return m
				}
				if melhorSemQualificar == nil {
					melhorSemQualificar = m
				}
			}
		}
	}
	return melhorSemQualificar
}

// diasNaDistancia devolve os dias a exatamente `offset` dias da candidata, na
// ordem do desempate: o anterior antes do posterior, porque empate de
// distância é resolvido pelo menor occurred_on. Offset zero é um dia só.
//
// Escreve no array do chamador para não alocar por candidata — são sete
// chamadas por linha, e o caminho quente desta função é o teto de 10.000
// candidatas.
func diasNaDistancia(base civil.Date, offset int, buf *[2]civil.Date) []civil.Date {
	if offset == 0 {
		buf[0] = base
		return buf[:1]
	}
	buf[0] = civil.AddDays(base, -offset)
	buf[1] = civil.AddDays(base, offset)
	return buf[:2]
}

// kindOposto devolve o sentido que o espelho precisa ter: despesa procura
// receita e vice-versa. Qualquer outro kind devolve vazio, e balde vazio é
// "sem espelho".
func kindOposto(kind string) string {
	switch kind {
	case KindExpense:
		return KindIncome
	case KindIncome:
		return KindExpense
	default:
		return ""
	}
}
