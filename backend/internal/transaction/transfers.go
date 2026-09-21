package transaction

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
)

// GET /transfers (spec 0005 §4.4, ADR-016, ADR-026).
//
// Uma transferência é um PAR de pernas (transfer_out + transfer_in) amarrado
// por transfer_group_id; a tela quer vê-lo como UMA linha — "de A para B,
// tanto, em tal dia". Este arquivo faz essa dobra e, junto, soma os totais do
// mês por par de contas e o saldo de caixa de cada conta no fim do mês.
//
// Nenhuma consulta por linha: âncoras (1) + outras pernas por fatia de IN (1)
// + pernas do mês (1) + contas (1) + somas até o fim do mês (1), mais a
// conferência de casa das contas do filtro. Tudo filtrado por household_id no
// repositório; as contas do filtro são conferidas como DA CASA antes de
// qualquer consulta — de outra casa é o MESMO ErrNotFound de id inexistente,
// byte a byte (S1).

// TransferListInput é a janela pedida pelo cliente.
type TransferListInput struct {
	// Month é "AAAA-MM", competência, obrigatório.
	Month string

	// AccountID, opcional, traz tudo que toca a conta. CounterpartAccountID
	// exige AccountID e restringe ao par entre as duas.
	AccountID            string
	CounterpartAccountID string

	// Cursor é a forma textual devolvida pela página anterior — o MESMO
	// cursor de GET /transactions.
	Cursor string

	// Limit é o tamanho da página; zero usa DefaultPageSize.
	Limit int
}

// TransferListView é a resposta — schema TransferList do contrato. Os três
// arrays estão SEMPRE presentes (vazios, se for o caso) e NextCursor é nulo
// quando acabou.
type TransferListView struct {
	Items      []TransferItemView    `json:"items"`
	Pairs      []TransferPairView    `json:"pairs"`
	Balances   []TransferBalanceView `json:"balances"`
	NextCursor *string               `json:"nextCursor"`
}

// TransferItemView é UM par visto como uma linha (schema TransferItem).
type TransferItemView struct {
	GroupID         string     `json:"groupId"`
	OccurredOn      civil.Date `json:"occurredOn"`
	CompetenceMonth string     `json:"competenceMonth"`
	FromAccountID   string     `json:"fromAccountId"`
	FromAccountName string     `json:"fromAccountName"`
	ToAccountID     string     `json:"toAccountId"`
	ToAccountName   string     `json:"toAccountName"`
	AmountCents     int64      `json:"amountCents"`
	Description     string     `json:"description"`
	Source          string     `json:"source"`
}

// TransferPairView são os totais do MÊS INTEIRO entre duas contas (schema
// TransferPair). A é a conta de menor id, para o par ser o mesmo
// independentemente do sentido; NetCents = AToBCents − BToACents.
type TransferPairView struct {
	AccountAID   string `json:"accountAId"`
	AccountAName string `json:"accountAName"`
	AccountBID   string `json:"accountBId"`
	AccountBName string `json:"accountBName"`
	AToBCents    int64  `json:"aToBCents"`
	BToACents    int64  `json:"bToACents"`
	NetCents     int64  `json:"netCents"`
	Count        int64  `json:"count"`
}

// TransferBalanceView é o saldo de CAIXA de uma conta no último dia do mês
// (schema TransferBalance): abertura mais a soma com sinal dos lançamentos
// vivos com occurred_on ≤ esse dia (ADR-017).
type TransferBalanceView struct {
	AccountID              string `json:"accountId"`
	AccountName            string `json:"accountName"`
	BalanceAtMonthEndCents int64  `json:"balanceAtMonthEndCents"`
}

// ListTransfers devolve a página de pares, os totais por par do mês e os
// saldos no fim do mês.
func (s *Service) ListTransfers(ctx context.Context, ator Actor, in TransferListInput) (TransferListView, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return TransferListView{}, ErrNotFound
	}
	primeiroDia, err := ParseMonth(in.Month)
	if err != nil {
		return TransferListView{}, err
	}
	if in.CounterpartAccountID != "" && in.AccountID == "" {
		return TransferListView{}, ErrCounterpartNeedsAccount
	}
	if in.CounterpartAccountID != "" && in.CounterpartAccountID == in.AccountID {
		return TransferListView{}, ErrSameAccountFilter
	}

	// As contas do filtro são conferidas como DA CASA antes de qualquer
	// consulta. Conta da vizinha devolveria uma lista vazia — o que não vaza
	// dado, mas não é a resposta certa: id que não é meu é 404, igual a id
	// que não existe (S1).
	//
	// ⚠️ E o que segue daqui para baixo é o id CANÔNICO — `conta.ID`, o que o
	// banco devolveu —, nunca a string do cliente. Esta rota é o caso mais
	// agudo do projeto: a posse é conferida em SQL (`WHERE id = ?`, cuja
	// igualdade vem da COLLATION da coluna, insensível a caixa no MySQL 8 e a
	// espaço à direita no MSSQL), mas o filtro é aplicado em GO — paresDoMes
	// compara `a != accountID` byte a byte, e saldosNoFimDoMes usa o id como
	// CHAVE DE MAPA (`nomes`, `abertura`). Com a string do cliente, o `ByID`
	// aceitaria `"<UUID>"`, nenhum par casaria, nenhum nome seria encontrado —
	// e a tela mostraria "nenhuma transferência neste mês" para uma conta
	// cheia delas, sem erro nenhum.
	contaDoFiltro, contraparteDoFiltro := in.AccountID, in.CounterpartAccountID
	for _, alvo := range []*string{&contaDoFiltro, &contraparteDoFiltro} {
		if *alvo == "" {
			continue
		}
		conta, err := s.contaDaCasa(ctx, householdID, *alvo)
		if err != nil {
			return TransferListView{}, err
		}
		*alvo = conta.ID
	}
	// Reconferência da igualdade sobre os ids CANÔNICOS: a recusa acima
	// comparou as strings do cliente, e com a caixa trocada `"<UUID>"` e
	// `"<uuid>"` passariam por ela sendo a mesma conta para o banco.
	if contraparteDoFiltro != "" && contraparteDoFiltro == contaDoFiltro {
		return TransferListView{}, ErrSameAccountFilter
	}

	cursor, err := ParseCursor(in.Cursor)
	if err != nil {
		return TransferListView{}, err
	}

	// Contas da casa, uma vez: nomes para os três blocos e abertura para os
	// saldos. Arquivadas incluídas — uma transferência antiga pode envolver
	// conta já arquivada, e o nome continua sendo a informação certa.
	contas, err := s.accounts.List(ctx, householdID, true)
	if err != nil {
		return TransferListView{}, fmt.Errorf("carregando contas da casa: %w", err)
	}
	nomes := make(map[string]string, len(contas))
	abertura := make(map[string]int64, len(contas))
	for i := range contas {
		nomes[contas[i].ID] = contas[i].Name
		abertura[contas[i].ID] = contas[i].OpeningBalanceCents
	}

	filtro := TransferFilter{
		CompetenceMonth:      in.Month,
		AccountID:            contaDoFiltro,
		CounterpartAccountID: contraparteDoFiltro,
	}
	if !cursor.OccurredOn.IsZero() {
		filtro.Cursor = &cursor
	}
	itens, proximo, err := s.paginaDeTransferencias(ctx, householdID, filtro, in.Limit, nomes)
	if err != nil {
		return TransferListView{}, err
	}

	pares, err := s.paresDoMes(ctx, householdID, in.Month, contaDoFiltro, contraparteDoFiltro, nomes)
	if err != nil {
		return TransferListView{}, err
	}

	saldos, err := s.saldosNoFimDoMes(ctx, householdID, primeiroDia, contaDoFiltro, contraparteDoFiltro, pares, nomes, abertura)
	if err != nil {
		return TransferListView{}, err
	}

	return TransferListView{Items: itens, Pairs: pares, Balances: saldos, NextCursor: proximo}, nil
}

// paginaDeTransferencias lê as pernas-âncora da página (uma por par), busca
// as outras pernas dos seus grupos em UMA consulta por fatia e dobra cada
// grupo numa linha. Grupo que não tem exatamente duas pernas vivas, uma de
// saída e uma de entrada, é descartado: ADR-016 garante que isso não
// acontece, e se acontecer, mostrar meia transferência como se fosse inteira
// seria pior do que omiti-la.
func (s *Service) paginaDeTransferencias(ctx context.Context, householdID string, filtro TransferFilter, limit int, nomes map[string]string) ([]TransferItemView, *string, error) {
	tamanho := tamanhoDaPagina(limit)
	// Uma linha a mais para saber se há próxima página sem pagar um COUNT.
	filtro.Limit = tamanho + 1

	ancoras, err := s.repo.ListTransferLegs(ctx, householdID, filtro)
	if err != nil {
		return nil, nil, fmt.Errorf("listando transferências: %w", err)
	}

	var proximo *string
	if len(ancoras) > tamanho {
		ancoras = ancoras[:tamanho]
		texto := EncodeCursor(Cursor{
			OccurredOn: ancoras[len(ancoras)-1].OccurredOn,
			ID:         ancoras[len(ancoras)-1].ID,
		})
		if texto != "" {
			proximo = &texto
		}
	}

	itens := make([]TransferItemView, 0, len(ancoras))
	if len(ancoras) == 0 {
		return itens, proximo, nil
	}

	grupos := make([]string, 0, len(ancoras))
	for i := range ancoras {
		if ancoras[i].TransferGroupID != nil && *ancoras[i].TransferGroupID != "" {
			grupos = append(grupos, *ancoras[i].TransferGroupID)
		}
	}
	pernas, err := s.repo.ByTransferGroups(ctx, householdID, grupos)
	if err != nil {
		return nil, nil, fmt.Errorf("carregando pares das transferências: %w", err)
	}
	porGrupo := make(map[string][]Transaction, len(grupos))
	for i := range pernas {
		if pernas[i].TransferGroupID == nil || pernas[i].DeletedAt != nil {
			continue
		}
		porGrupo[*pernas[i].TransferGroupID] = append(porGrupo[*pernas[i].TransferGroupID], pernas[i])
	}

	for i := range ancoras {
		if ancoras[i].TransferGroupID == nil {
			continue
		}
		item, ok := dobrarPar(porGrupo[*ancoras[i].TransferGroupID], nomes)
		if !ok {
			continue
		}
		itens = append(itens, item)
	}
	return itens, proximo, nil
}

// dobrarPar transforma as duas pernas de um grupo numa linha. Devolve false
// quando o grupo não é um par coerente.
func dobrarPar(pernas []Transaction, nomes map[string]string) (TransferItemView, bool) {
	if len(pernas) != 2 {
		return TransferItemView{}, false
	}
	saida, entrada := pernas[0], pernas[1]
	if saida.Kind != KindTransferOut {
		saida, entrada = entrada, saida
	}
	if saida.Kind != KindTransferOut || entrada.Kind != KindTransferIn || saida.AccountID == entrada.AccountID {
		return TransferItemView{}, false
	}
	return TransferItemView{
		GroupID:         *saida.TransferGroupID,
		OccurredOn:      saida.OccurredOn,
		CompetenceMonth: saida.CompetenceMonth,
		FromAccountID:   saida.AccountID,
		FromAccountName: nomes[saida.AccountID],
		ToAccountID:     entrada.AccountID,
		ToAccountName:   nomes[entrada.AccountID],
		AmountCents:     saida.AmountCents,
		Description:     saida.Description,
		Source:          saida.Source,
	}, true
}

// somaDoPar acumula os totais de um par de contas enquanto o mês é varrido.
type somaDoPar struct {
	aToB, bToA, count int64
}

// paresDoMes lê TODAS as pernas vivas do mês (quatro colunas, teto + 1),
// agrupa por transfer_group_id em Go e soma por par de contas — do mês
// INTEIRO, não da página. O par é {min(id), max(id)}: identificável
// independentemente do sentido. Depois aplica o filtro (só pares que tocam A;
// com B, só o par A–B) e ordena por (accountAId, accountBId).
func (s *Service) paresDoMes(ctx context.Context, householdID, month, accountID, counterpartID string, nomes map[string]string) ([]TransferPairView, error) {
	pernas, err := s.repo.TransferLegsOfMonth(ctx, householdID, month, MaxTransferLegsPerMonth+1)
	if err != nil {
		return nil, fmt.Errorf("somando transferências do mês: %w", err)
	}
	if len(pernas) > MaxTransferLegsPerMonth {
		return nil, fmt.Errorf("%w: máximo de %d pernas", ErrTooManyTransfers, MaxTransferLegsPerMonth)
	}

	porGrupo := make(map[string][]TransferLegSummary, len(pernas)/2+1)
	for i := range pernas {
		porGrupo[pernas[i].TransferGroupID] = append(porGrupo[pernas[i].TransferGroupID], pernas[i])
	}

	type chaveDoPar struct{ a, b string }
	somas := map[chaveDoPar]*somaDoPar{}
	for _, grupo := range porGrupo {
		if len(grupo) != 2 {
			continue
		}
		saida, entrada := grupo[0], grupo[1]
		if saida.Kind != KindTransferOut {
			saida, entrada = entrada, saida
		}
		if saida.Kind != KindTransferOut || entrada.Kind != KindTransferIn || saida.AccountID == entrada.AccountID {
			continue
		}
		a, b := saida.AccountID, entrada.AccountID
		if b < a {
			a, b = b, a
		}
		if accountID != "" && a != accountID && b != accountID {
			continue
		}
		if counterpartID != "" && a != counterpartID && b != counterpartID {
			continue
		}
		k := chaveDoPar{a, b}
		soma := somas[k]
		if soma == nil {
			soma = &somaDoPar{}
			somas[k] = soma
		}
		if saida.AccountID == a {
			soma.aToB += saida.AmountCents
		} else {
			soma.bToA += saida.AmountCents
		}
		soma.count++
	}

	pares := make([]TransferPairView, 0, len(somas))
	for k, soma := range somas {
		pares = append(pares, TransferPairView{
			AccountAID:   k.a,
			AccountAName: nomes[k.a],
			AccountBID:   k.b,
			AccountBName: nomes[k.b],
			AToBCents:    soma.aToB,
			BToACents:    soma.bToA,
			NetCents:     soma.aToB - soma.bToA,
			Count:        soma.count,
		})
	}
	sort.Slice(pares, func(i, j int) bool {
		if pares[i].AccountAID != pares[j].AccountAID {
			return pares[i].AccountAID < pares[j].AccountAID
		}
		return pares[i].AccountBID < pares[j].AccountBID
	})
	return pares, nil
}

// saldosNoFimDoMes calcula abertura + soma com sinal até o último dia do mês
// para as contas envolvidas: as do filtro quando há filtro, senão todas as que
// aparecem em `pares`. UMA consulta de somas para a casa inteira.
func (s *Service) saldosNoFimDoMes(ctx context.Context, householdID string, primeiroDia civil.Date, accountID, counterpartID string, pares []TransferPairView, nomes map[string]string, abertura map[string]int64) ([]TransferBalanceView, error) {
	envolvidas := map[string]struct{}{}
	if accountID != "" {
		envolvidas[accountID] = struct{}{}
		if counterpartID != "" {
			envolvidas[counterpartID] = struct{}{}
		}
	} else {
		for i := range pares {
			envolvidas[pares[i].AccountAID] = struct{}{}
			envolvidas[pares[i].AccountBID] = struct{}{}
		}
	}

	saldos := make([]TransferBalanceView, 0, len(envolvidas))
	if len(envolvidas) == 0 {
		return saldos, nil
	}

	ultimoDia, err := fimDoMes(primeiroDia)
	if err != nil {
		return nil, err
	}
	somas, err := s.repo.SumByAccountUntil(ctx, householdID, ultimoDia)
	if err != nil {
		return nil, fmt.Errorf("somando saldos até o fim do mês: %w", err)
	}

	ids := make([]string, 0, len(envolvidas))
	for id := range envolvidas {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		nome, daCasa := nomes[id]
		if !daCasa {
			// Conta que não está na lista da casa não tem abertura conhecida:
			// um saldo inventado seria pior do que a ausência. (Com filtro,
			// contaDaCasa já garantiu que ela existe; sem filtro, os ids vêm
			// de lançamentos da própria casa.)
			continue
		}
		saldos = append(saldos, TransferBalanceView{
			AccountID:              id,
			AccountName:            nome,
			BalanceAtMonthEndCents: abertura[id] + somas[id],
		})
	}
	return saldos, nil
}

// fimDoMes devolve o último dia civil do mês de d. Via time.Date com dia 0 do
// mês seguinte — a única aritmética de calendário confiável, fevereiro
// bissexto incluído.
func fimDoMes(d civil.Date) (civil.Date, error) {
	ultimo := time.Date(d.Year(), time.Month(d.Month())+1, 0, 0, 0, 0, 0, time.UTC).Day()
	fim, err := civil.New(d.Year(), d.Month(), ultimo)
	if err != nil {
		return civil.Date{}, fmt.Errorf("calculando o fim do mês: %w", err)
	}
	return fim, nil
}
