package dashboard

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Service monta a faixa de resumo do painel. Só leitura: não há UnitOfWork
// nem auditoria (ADR-031a).
type Service struct {
	ledger     Ledger
	categories Categories
	accounts   Accounts
	lg         *slog.Logger
}

// NewService monta o serviço. O logger é usado APENAS para o aviso AGREGADO
// de anomalia de dado (linha agregada cuja conta não está na lista da casa) —
// um por requisição, com a contagem e uma amostra curta de ids, nunca centavos
// e nunca nome de conta (docs/SEGURANCA.md §4).
func NewService(ledger Ledger, categories Categories, accounts Accounts, lg *slog.Logger) *Service {
	if lg == nil {
		lg = slog.Default()
	}
	return &Service{ledger: ledger, categories: categories, accounts: accounts, lg: lg}
}

// Summary responde GET /api/v1/dashboard: os três números do mês numa chamada
// só (spec 0008, ADR-031).
//
// Ordem das guardas — nada vai ao banco com entrada não validada:
//
//  1. casa do TOKEN (vazia é ErrUnauthenticated, defesa em profundidade: o
//     handler já barrou, e uma consulta com household_id vazio devolveria o
//     mês zerado em silêncio ou, pior, dado que não é de ninguém);
//  2. forma do mês, por transaction.ParseMonth — o MESMO validador de
//     GET /transactions e de GET /reports/by-category, para as três telas
//     recusarem exatamente as mesmas strings. Nada é normalizado;
//  3. taxonomia da casa (as categorias que marcam investimento);
//  4. contas da casa (quais são cartão de crédito);
//  5. só então a agregação.
//
// Os passos 3 e 4 vêm ANTES da consulta porque é deles que sai o conjunto que
// entra no `IN (?)` — e esse conjunto é SEMPRE da casa do token, que é o que
// impede um id de outra casa de chegar ao WHERE (docs/SEGURANCA.md §2).
func (s *Service) Summary(ctx context.Context, ator Actor, in SummaryInput) (SummaryView, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return SummaryView{}, ErrUnauthenticated
	}
	if _, err := transaction.ParseMonth(in.Month); err != nil {
		return SummaryView{}, err
	}

	marcadas, err := s.categoriasMarcadas(ctx, householdID)
	if err != nil {
		return SummaryView{}, err
	}
	contas, err := s.carregarContas(ctx, householdID)
	if err != nil {
		return SummaryView{}, err
	}

	// UMA consulta para os três números. Conjunto VAZIO não é erro: a casa que
	// ainda não marca investimento recebe a consulta de quatro colunas, e
	// `IN ()` nunca é emitido (ADR-029f).
	rows, err := s.ledger.SumMonthByKindAndAccount(ctx, householdID, in.Month, marcadas)
	if err != nil {
		return SummaryView{}, fmt.Errorf("resumindo o mês do painel: %w", err)
	}

	// O teto é ESTRUTURAL: `GROUP BY kind, account_id` sobre dois kinds não
	// pode passar de 2 × as contas da casa, e o teto de contas é REAL porque
	// conta com lançamento não pode ser excluída (o UsageChecker responde
	// 422). Mais linhas do que isso é banco em estado que a aplicação não
	// produz — falha FECHADA, com a CONTAGEM no log e nada mais.
	if len(rows) > 2*account.MaxPerHousehold {
		return SummaryView{}, fmt.Errorf("%w: %d linhas (teto %d)",
			errTooManyRows, len(rows), 2*account.MaxPerHousehold)
	}

	acumulado, anom, err := dobrar(rows, contas)
	// UM aviso por requisição, depois da dobra — inclusive quando ela falhou,
	// porque a anomalia que a antecedeu é parte do diagnóstico. Avisar por
	// LINHA seria amplificação de log: quem tem o token recarrega a home e
	// multiplica o volume sem custo.
	anom.avisar(s.lg)
	if err != nil {
		return SummaryView{}, err
	}

	// As duas contagens de estado vazio nascem de `len()` de listas com teto
	// de domínio (≤ 50 contas, ≤ 200 categorias): a conversão para int64 — que
	// o DTO usa para ter UM só tipo inteiro — não tem como estourar.
	return acumulado.publicar(in.Month, contas.cartoesVivos, int64(len(marcadas)))
}

// categoriasMarcadas devolve os ids das categorias da casa que MARCAM um
// lançamento como aporte ou resgate.
//
// Três disciplinas, e nenhuma é decorativa:
//
//   - o predicado é transaction.MarcadaComoInvestimento — IMPORTADO, nunca
//     reescrito (ADR-031f). Uma segunda cópia divergiria da do `summary` de
//     GET /transactions, e aí o mesmo dinheiro sairia da receita de uma tela e
//     continuaria na outra;
//   - includeArchived é TRUE, como em transaction.rotulos, no relatório e no
//     investimento: arquivar uma categoria não desfaz a marcação do passado
//     (PLANOS.md §4.4), e o conjunto que marca os lançamentos tem de ser o
//     MESMO nas três agregações;
//   - a casa é reconferida em Go. O repositório já filtra, e reconferir custa
//     uma comparação por categoria: categoria de outra casa jamais entra num
//     conjunto que vira `IN (...)`.
//
// O conjunto devolvido é o MESMO que vira `investmentCategoryCount` — nunca
// uma segunda definição, que contaria um conjunto e filtraria outro. A ordem é
// determinística (ids ordenados) para o rastro de consultas não depender da
// iteração de um mapa: o resultado não muda com ela, mas teste que passa por
// acaso não é teste.
func (s *Service) categoriasMarcadas(ctx context.Context, householdID string) ([]string, error) {
	cats, err := s.categories.List(ctx, householdID, true)
	if err != nil {
		return nil, fmt.Errorf("carregando categorias da casa: %w", err)
	}
	conjunto := make(map[string]struct{}, len(cats))
	for i := range cats {
		c := cats[i]
		if c.HouseholdID != householdID || c.ID == "" {
			continue
		}
		if transaction.MarcadaComoInvestimento(c.Kind) {
			conjunto[c.ID] = struct{}{}
		}
	}
	return ordenados(conjunto), nil
}

// contas é o que o painel precisa saber sobre as contas da casa.
type contas struct {
	// todas é o conjunto de ids da casa. Serve para reconhecer a ANOMALIA:
	// uma linha agregada cuja conta não está aqui.
	todas map[string]struct{}

	// cartoes são os ids de tipo credit_card, ARQUIVADOS INCLUÍDOS — o gasto
	// de um cartão arquivado continua sendo gasto daquele mês.
	cartoes map[string]struct{}

	// cartoesVivos conta os cartões NÃO arquivados, e é um critério
	// deliberadamente DIFERENTE do de `cartoes` (critério 12 da spec): este
	// número responde "você tem um cartão cadastrado?", e é ele que deixa a
	// tela distinguir "não tem cartão" de "tem cartão e não gastou".
	cartoesVivos int64
}

// carregarContas lê as contas da casa do TOKEN e monta os dois critérios de
// cartão.
//
// includeArchived é TRUE pelo motivo do parágrafo acima: os dois usos da mesma
// lista são diferentes de propósito, e pedir a lista sem as arquivadas faria o
// gasto do cartão encolher no mês em que alguém arquivasse um cartão — isto é,
// mudaria o passado.
func (s *Service) carregarContas(ctx context.Context, householdID string) (contas, error) {
	lista, err := s.accounts.List(ctx, householdID, true)
	if err != nil {
		return contas{}, fmt.Errorf("carregando contas da casa: %w", err)
	}
	c := contas{
		todas:   make(map[string]struct{}, len(lista)),
		cartoes: make(map[string]struct{}, len(lista)),
	}
	for i := range lista {
		a := lista[i]
		// Mesma reconferência de casa das categorias, e pelo mesmo motivo.
		if a.HouseholdID != householdID || a.ID == "" {
			continue
		}
		c.todas[a.ID] = struct{}{}
		if a.Kind != account.KindCreditCard {
			continue
		}
		c.cartoes[a.ID] = struct{}{}
		if a.ArchivedAt == nil {
			c.cartoesVivos++
		}
	}
	return c, nil
}

// --- a dobra ---------------------------------------------------------------

// acumulador guarda os números do mês enquanto as ≤ 100 linhas agregadas são
// percorridas. Tudo aqui é BRUTO: nenhuma subtração acontece durante a dobra.
type acumulador struct {
	receitaBruta, receitaCnt  int64
	resgates, resgatesCnt     int64
	aportes, aportesCnt       int64
	cartaoBruto, cartaoCnt    int64
	cartaoMarc, cartaoMarcCnt int64
}

// dobrar transforma as linhas `(kind, conta)` nos acumuladores do mês.
//
// # A desigualdade é VERIFICADA linha a linha, nunca confiada
//
// `0 ≤ marcado ≤ total`, nos centavos e nas contagens. Ela vale porque
// `amount_cents ≥ 0` é invariante do caminho de ESCRITA (ValidateAmount) e
// porque `COUNT(*)` não é negativo — duas propriedades que ESTE código não
// controla. Violá-la é banco adulterado, e o painel publica `minimum: 0` em
// quatro dos seus números: falha FECHADA (500 genérico, contagens no log,
// nenhum número inventado). Clampar em silêncio esconderia a corrupção E ainda
// publicaria um número errado (ADR-029 j.1).
//
// # O fluxo vem do KIND DO LANÇAMENTO, nunca da natureza da categoria
//
// `expense` marcado é APORTE (saiu da conta) e `income` marcado é RESGATE
// (entrou) — que é o que o extrato diz (ADR-029d).
func dobrar(rows []KindAccountTotals, c contas) (acumulador, anomalias, error) {
	var acc acumulador
	var anom anomalias

	for i := range rows {
		row := rows[i]
		if row.TotalCents < 0 || row.Count < 0 ||
			row.MarkedTotalCents < 0 || row.MarkedCount < 0 ||
			row.MarkedTotalCents > row.TotalCents || row.MarkedCount > row.Count {
			// Nem centavos nem contagem da linha no erro: só a POSIÇÃO. O
			// valor é dado da casa, e este erro termina no log do 500.
			return acumulador{}, anom, fmt.Errorf("%w: linha %d de %d",
				errTotalsOutOfRange, i+1, len(rows))
		}

		// Conta que não está na lista da casa é ANOMALIA de dado, e falha
		// ABERTA (molde do ADR-027f): ela simplesmente não é cartão, e o
		// dinheiro continua contando na receita se a linha for receita.
		// Descartar a linha sumiria com dinheiro por causa de um id órfão;
		// tratá-la como cartão inventaria um cartão. O aviso é agregado.
		ehCartao := false
		if _, ok := c.todas[row.AccountID]; !ok {
			anom.contaFora(row.AccountID)
		} else if _, ok := c.cartoes[row.AccountID]; ok {
			ehCartao = true
		}

		var ok bool
		switch row.Kind {
		case transaction.KindIncome:
			ok = acumular(&acc.receitaBruta, row.TotalCents) &&
				acumular(&acc.receitaCnt, row.Count) &&
				acumular(&acc.resgates, row.MarkedTotalCents) &&
				acumular(&acc.resgatesCnt, row.MarkedCount)

		case transaction.KindExpense:
			ok = acumular(&acc.aportes, row.MarkedTotalCents) &&
				acumular(&acc.aportesCnt, row.MarkedCount)
			if ok && ehCartao {
				ok = acumular(&acc.cartaoBruto, row.TotalCents) &&
					acumular(&acc.cartaoCnt, row.Count) &&
					acumular(&acc.cartaoMarc, row.MarkedTotalCents) &&
					acumular(&acc.cartaoMarcCnt, row.MarkedCount)
			}

		default:
			// Não pode acontecer: a consulta lê `kind IN (income, expense)`, e
			// é isso que torna ESTRUTURAL o corte da transferência interna
			// (ADR-016). Se acontecesse, ficar de fora dos dois baldes é o
			// comportamento seguro — uma perna `transfer_in` somada ao gasto
			// do cartão faria PAGAR a fatura parecer um gasto novo. Entra no
			// aviso agregado, sem id: o kind é enum fechado.
			anom.kindInesperado++
			ok = true
		}
		if !ok {
			return acumulador{}, anom, fmt.Errorf("%w: soma estourou na linha %d de %d",
				errTotalsOutOfRange, i+1, len(rows))
		}
	}
	return acc, anom, nil
}

// publicar fecha os nove campos da resposta.
//
// # As quatro subtrações de `total − marcado`
//
// São seguras porque a desigualdade `0 ≤ marcado ≤ total` foi VERIFICADA linha
// a linha e a soma preserva a ordem (Σ marcado ≤ Σ total, já que nenhuma das
// duas somas estourou — somaSegura garante isso). Mesmo assim elas passam por
// diferencaConferida: a propriedade está provada acima, e conferi-la custa uma
// comparação. No dia em que alguém mexer na dobra, quem avisa é o 500 — não um
// número negativo publicado contra um `minimum: 0`.
//
// # A subtração do LÍQUIDO é a única exceção, e é deliberada
//
// `aportes − resgates` é a ÚNICA subtração do painel que pode dar NEGATIVO, e
// isso é o PONTO da feature (ADR-031c): o mês em que se resgatou mais do que se
// aportou tem líquido negativo, e essa é a resposta certa, não um erro.
//
// Ela é sempre representável: com `a ≥ 0` e `b ≥ 0`, `a − b ∈ [−b, a] ⊂ int64`
// — não há estouro possível em nenhum dos dois sentidos, e por isso ela não
// precisa de guarda nenhuma. ⚠️ NÃO a "conserte" com somaSegura nem com
// diferencaConferida: as duas recusam resultado negativo, e recusar o negativo
// aqui quebraria exatamente o número que a feature existe para mostrar.
func (a acumulador) publicar(month string, cartoesVivos, categoriasMarcadas int64) (SummaryView, error) {
	receita, ok := diferencaConferida(a.receitaBruta, a.resgates)
	if !ok {
		return SummaryView{}, fmt.Errorf("%w: receita menos resgates", errTotalsOutOfRange)
	}
	receitaCnt, ok := diferencaConferida(a.receitaCnt, a.resgatesCnt)
	if !ok {
		return SummaryView{}, fmt.Errorf("%w: contagem de receita menos resgates", errTotalsOutOfRange)
	}
	cartao, ok := diferencaConferida(a.cartaoBruto, a.cartaoMarc)
	if !ok {
		return SummaryView{}, fmt.Errorf("%w: cartão menos aportes em cartão", errTotalsOutOfRange)
	}
	cartaoCnt, ok := diferencaConferida(a.cartaoCnt, a.cartaoMarcCnt)
	if !ok {
		return SummaryView{}, fmt.Errorf("%w: contagem de cartão menos aportes em cartão", errTotalsOutOfRange)
	}
	// SOMA, e não diferença: investmentCount é quantos lançamentos formaram o
	// líquido — um aporte e um resgate são DOIS lançamentos, mesmo quando se
	// anulam em centavos.
	investimentoCnt, ok := somaSegura(a.aportesCnt, a.resgatesCnt)
	if !ok {
		return SummaryView{}, fmt.Errorf("%w: contagem de investimentos", errTotalsOutOfRange)
	}

	return SummaryView{
		Month:                   month,
		IncomeCents:             receita,
		IncomeCount:             receitaCnt,
		CreditCardExpenseCents:  cartao,
		CreditCardExpenseCount:  cartaoCnt,
		InvestmentNetCents:      a.aportes - a.resgates,
		InvestmentCount:         investimentoCnt,
		CreditCardAccountCount:  cartoesVivos,
		InvestmentCategoryCount: categoriasMarcadas,
	}, nil
}

// --- aritmética ------------------------------------------------------------

// acumular soma a parcela no destino e diz se coube. Existe para que TODA soma
// da dobra passe por somaSegura sem transformar o laço em trinta linhas de
// `if`: a disciplina é a mesma, a repetição é que não.
func acumular(destino *int64, parcela int64) bool {
	soma, ok := somaSegura(*destino, parcela)
	if !ok {
		return false
	}
	*destino = soma
	return true
}

// somaSegura soma dois valores NÃO NEGATIVOS e informa se coube em int64.
//
// Cópia deliberada de report.somaSegura e de investment.somaSegura (achado B3
// da revisão de segurança daquela entrega), e NÃO um import: os três pacotes
// são leitores independentes, e exportar um helper de doze linhas só para isso
// criaria uma dependência entre pacotes que não se conhecem — pior do que a
// duplicação, que vem acompanhada do mesmo teste, caso por caso.
//
// A não negatividade é VERIFICADA, não confiada: com `a < 0`, a conta
// `math.MaxInt64-a` transbordaria e a função passaria a responder "não coube"
// para quase todo `b` — errada, ainda que fechada. Nem centavos (ValidateAmount
// recusa negativo) nem `COUNT(*)` podem ser negativos aqui; se um deles for, é
// banco em estado que a aplicação não produz, e o painel prefere 500 a publicar
// um total inventado.
func somaSegura(a, b int64) (int64, bool) {
	if a < 0 || b < 0 {
		return 0, false
	}
	if b > math.MaxInt64-a {
		return 0, false
	}
	return a + b, true
}

// diferencaConferida devolve `total − parte` depois de CONFERIR
// `0 ≤ parte ≤ total`. É a forma das quatro subtrações que o contrato publica
// com `minimum: 0`.
//
// ⚠️ Não serve para investmentNetCents: lá o negativo é a resposta certa.
func diferencaConferida(total, parte int64) (int64, bool) {
	if total < 0 || parte < 0 || parte > total {
		return 0, false
	}
	return total - parte, true
}

// ordenados devolve as chaves do conjunto em ordem estável. Ordem
// determinística porque o conjunto vira `IN (...)`: o resultado não depende
// dela, mas o rastro de consultas sim.
func ordenados(conjunto map[string]struct{}) []string {
	if len(conjunto) == 0 {
		return nil
	}
	out := make([]string, 0, len(conjunto))
	for id := range conjunto {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// --- anomalias de dado -----------------------------------------------------

// maxAmostraAnomalia é o teto de ids DISTINTOS no aviso. O aviso é um só por
// requisição, e uma amostra curta basta para investigar — a contagem já diz o
// tamanho do estrago.
const maxAmostraAnomalia = 5

// anomalias acumula, durante a dobra, o que a API não produz (só banco
// adulterado ou restauração parcial chega aqui). Guarda CONTAGENS e ids —
// nunca centavos, nunca nome de conta, nunca a consulta.
type anomalias struct {
	// contaDesconhecida: a linha agregada aponta uma conta que não está na
	// lista DESTA casa. A linha continua contando na receita; cartão ela não
	// é.
	contaDesconhecida int64

	// kindInesperado: a agregação devolveu um kind fora de income/expense, que
	// o WHERE da consulta torna impossível. Sem id e sem amostra: o kind é
	// enum fechado e a linha não entra em número nenhum.
	kindInesperado int64

	amostraConta []string
}

func (an *anomalias) contaFora(id string) {
	an.contaDesconhecida++
	// Busca linear barata de propósito: a amostra tem no máximo 5 elementos.
	if len(an.amostraConta) >= maxAmostraAnomalia || slices.Contains(an.amostraConta, id) {
		return
	}
	an.amostraConta = append(an.amostraConta, id)
}

// avisar emite, no máximo, UM registro por requisição — a alternativa (um por
// linha) é amplificação de log a custo zero para quem tem o token.
func (an anomalias) avisar(lg *slog.Logger) {
	total := an.contaDesconhecida + an.kindInesperado
	if total == 0 {
		return
	}
	attrs := []any{
		slog.Int64("anomalias", total),
		slog.Int64("conta_desconhecida", an.contaDesconhecida),
		slog.Int64("kind_inesperado", an.kindInesperado),
	}
	if len(an.amostraConta) > 0 {
		attrs = append(attrs, slog.Any("amostra_conta_desconhecida", an.amostraConta))
	}
	lg.Warn("resumo do painel encontrou linhas com conta inconsistente", attrs...)
}
