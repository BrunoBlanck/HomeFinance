package transaction_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	minhaCasa = "casa-1"
	outraCasa = "casa-2"
	usuario   = "11111111-1111-7111-8111-111111111111"
)

func ator(casa string) transaction.Actor {
	return transaction.Actor{HouseholdID: casa, UserID: usuario, IP: "203.0.113.10"}
}

var agora = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

// chave monta uma chave de deduplicação com a forma canônica (SHA-256 em
// hexadecimal, 64 caracteres), que é o que o serviço exige. O conteúdo não
// importa para o teste; a FORMA importa, porque é ela que o banco guarda em
// varchar(64) e é ela que o serviço confere.
func chave(semente string) string {
	soma := sha256.Sum256([]byte(semente))
	return hex.EncodeToString(soma[:])
}

// --- dublês ---------------------------------------------------------------

// repoFake guarda os lançamentos em memória, MAS mantém os dois contratos que
// mais importam do repositório real: nada é devolvido sem bater household_id, e
// (household_id, dedup_key, dedup_ordinal) é ÚNICO. Um dublê frouxo aqui
// esconderia justamente o que estes testes existem para provar.
type repoFake struct {
	linhas   map[string]transaction.Transaction
	ordem    []string
	proximo  int
	criadas  int
	erroList error

	// aoLerOrdinal, quando definido, roda logo depois de cada leitura do
	// ordinal máximo. É o gancho que reproduz o confirm concorrente.
	aoLerOrdinal func(dedupKey string, maximo int)

	// casasConsultadas registra o householdID de CADA chamada aos métodos do
	// reprocessamento de transferências (spec 0005 §13.5, critério 8): o
	// teste de isolamento confere que só a casa do token chega ao
	// repositório, e não apenas que o resultado ficou vazio.
	casasConsultadas []string

	// antesDeConverter, quando definido, roda antes de cada
	// ConvertToTransferPair — o gancho que reproduz a requisição concorrente
	// entre a leitura e o UPDATE da mesma execução (critério 7b).
	antesDeConverter func(p transaction.TransferPairConversion)
	conversoes       int
	// resumoForcado atropela o cálculo de Summary — é como o teste encena um
	// resumo impossível (parcela negativa) vindo do banco.
	resumoForcado *transaction.Summary
	// resumosPedidos guarda o conjunto de categorias de investimento que cada
	// chamada de Summary recebeu: é com ele que o teste prova que o serviço
	// passou (ou não) o filtro.
	resumosPedidos [][]string
	// listagens conta as chamadas de List. É o par de resumosPedidos nos
	// testes de falha fechada: sem ele, "nada foi consultado" provaria só que
	// o RESUMO não foi pedido, e a página teria saído do mesmo jeito.
	listagens int

	// erroUncategorized e erroCandidatas injetam FALHA DE BANCO na primeira
	// consulta de cada rota que varre o mês. Elas existem para o achado A12:
	// falha de driver embrulhada por storage.ComErroDeContexto com o contexto
	// morto casa `context.DeadlineExceeded`/`context.Canceled`, e é essa a
	// armadilha que separa "limite previsto" de "falha do servidor".
	erroUncategorized error
	erroCandidatas    error
}

func novoRepo() *repoFake {
	return &repoFake{linhas: map[string]transaction.Transaction{}}
}

func (r *repoFake) proximoID() string {
	r.proximo++
	return fmt.Sprintf("00000000-0000-7000-8000-%012d", r.proximo)
}

func (r *repoFake) semear(t transaction.Transaction) transaction.Transaction {
	if t.ID == "" {
		t.ID = r.proximoID()
	}
	if t.DedupOrdinal == 0 {
		t.DedupOrdinal = 1
	}
	if t.DedupKey == "" {
		t.DedupKey = chave(fmt.Sprintf("%x", len(r.ordem)+1))
	}
	if t.CompetenceMonth == "" {
		t.CompetenceMonth = t.OccurredOn.YearMonth()
	}
	if t.Source == "" {
		t.Source = transaction.SourceImport
	}
	r.linhas[t.ID] = t
	r.ordem = append(r.ordem, t.ID)
	return t
}

func (r *repoFake) CreateBatch(_ context.Context, householdID string, txs []transaction.Transaction) error {
	if householdID == "" {
		return transaction.ErrHouseholdMismatch
	}
	for i := range txs {
		t := txs[i]
		if t.HouseholdID != householdID {
			return transaction.ErrHouseholdMismatch
		}
		if t.CompetenceMonth == "" || t.DedupKey == "" || t.DedupOrdinal < 1 {
			return transaction.ErrIncomplete
		}
		// O índice único do banco, reproduzido: é ele o árbitro final da
		// deduplicação (ADR-025), e o teste precisa que ele exista para provar
		// que o serviço traduz a violação em vez de devolver 500.
		for _, existente := range r.linhas {
			if existente.HouseholdID == t.HouseholdID &&
				existente.DedupKey == t.DedupKey &&
				existente.DedupOrdinal == t.DedupOrdinal {
				return fmt.Errorf("inserindo lançamentos: %w", transaction.ErrDuplicateDedup)
			}
		}
		r.linhas[t.ID] = t
		r.ordem = append(r.ordem, t.ID)
		r.criadas++
	}
	return nil
}

func (r *repoFake) ByID(_ context.Context, householdID, id string) (*transaction.Transaction, error) {
	t, ok := r.linhas[id]
	if !ok || t.HouseholdID != householdID || t.DeletedAt != nil {
		return nil, transaction.ErrNotFound
	}
	copia := t
	return &copia, nil
}

func (r *repoFake) List(_ context.Context, householdID string, f transaction.ListFilter) ([]transaction.Transaction, error) {
	r.listagens++
	if r.erroList != nil {
		return nil, r.erroList
	}
	var out []transaction.Transaction
	for _, id := range r.ordem {
		t := r.linhas[id]
		if t.HouseholdID != householdID || t.DeletedAt != nil {
			continue
		}
		if f.CompetenceMonth != "" && t.CompetenceMonth != f.CompetenceMonth {
			continue
		}
		if f.AccountID != "" && t.AccountID != f.AccountID {
			continue
		}
		if f.StatementID != "" && (t.StatementID == nil || *t.StatementID != f.StatementID) {
			continue
		}
		out = append(out, t)
	}

	slices.SortFunc(out, func(a, b transaction.Transaction) int {
		if c := b.OccurredOn.Compare(a.OccurredOn); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})

	if f.Cursor != nil {
		filtrado := out[:0]
		for _, t := range out {
			depois := t.OccurredOn.Before(f.Cursor.OccurredOn) ||
				(t.OccurredOn.Compare(f.Cursor.OccurredOn) == 0 && t.ID < f.Cursor.ID)
			if depois {
				filtrado = append(filtrado, t)
			}
		}
		out = filtrado
	}

	limite := f.Limit
	if limite <= 0 {
		limite = transaction.DefaultPageSize
	}
	if limite > transaction.MaxPageSize {
		limite = transaction.MaxPageSize
	}
	if len(out) > limite {
		out = out[:limite]
	}
	return out, nil
}

// Summary reproduz o contrato do repositório real depois da E7 (ADR-029e): a
// receita e a despesa devolvidas já vêm LÍQUIDAS do que foi marcado, e o
// marcado sai à parte em InvestedCents/RedeemedCents. O fluxo vem do kind do
// LANÇAMENTO — despesa marcada é aporte, receita marcada é resgate (ADR-029d).
//
// resumoForcado, quando presente, atropela tudo: é como o teste encena um
// banco em estado que a aplicação não produz (parcela negativa) sem precisar
// de um banco adulterado de verdade.
func (r *repoFake) Summary(_ context.Context, householdID string, f transaction.SummaryFilter) (transaction.Summary, error) {
	r.resumosPedidos = append(r.resumosPedidos, append([]string(nil), f.InvestmentCategoryIDs...))
	if r.resumoForcado != nil {
		return *r.resumoForcado, nil
	}
	if len(f.InvestmentCategoryIDs) > 200 {
		return transaction.Summary{}, transaction.ErrTooManyCategories
	}
	marcadas := conjunto(f.InvestmentCategoryIDs)

	var out transaction.Summary
	marcada := func(t transaction.Transaction) bool {
		if t.CategoryID == nil {
			return false
		}
		_, ok := marcadas[*t.CategoryID]
		return ok
	}
	for _, t := range r.linhas {
		if t.HouseholdID != householdID || t.DeletedAt != nil {
			continue
		}
		if f.CompetenceMonth != "" && t.CompetenceMonth != f.CompetenceMonth {
			continue
		}
		if f.AccountID != "" && t.AccountID != f.AccountID {
			continue
		}
		out.Count++
		switch t.Kind {
		case transaction.KindIncome:
			if marcada(t) {
				out.RedeemedCents += t.AmountCents
			} else {
				out.IncomeCents += t.AmountCents
			}
			if t.CategoryID == nil {
				out.Uncategorized++
			}
		case transaction.KindExpense:
			if marcada(t) {
				out.InvestedCents += t.AmountCents
			} else {
				out.ExpenseCents += t.AmountCents
			}
			if t.CategoryID == nil {
				out.Uncategorized++
			}
		}
	}
	out.NetCents = out.IncomeCents - out.ExpenseCents
	return out, nil
}

func (r *repoFake) SumByAccount(_ context.Context, householdID string) (map[string]int64, error) {
	out := map[string]int64{}
	for _, t := range r.linhas {
		if t.HouseholdID != householdID || t.DeletedAt != nil {
			continue
		}
		out[t.AccountID] += t.SignedAmountCents()
	}
	return out, nil
}

func (r *repoFake) WindowForDedup(_ context.Context, householdID, accountID string, minDate, maxDate civil.Date) ([]transaction.DedupRow, error) {
	var out []transaction.DedupRow
	for _, t := range r.linhas {
		if t.HouseholdID != householdID || t.AccountID != accountID {
			continue
		}
		if t.OccurredOn.Before(minDate) || t.OccurredOn.After(maxDate) {
			continue
		}
		out = append(out, transaction.DedupRow{
			ID: t.ID, OccurredOn: t.OccurredOn, AmountCents: t.AmountCents,
			Kind: t.Kind, DescriptionNorm: t.DescriptionNorm, ExternalID: t.ExternalID,
			DedupKey: t.DedupKey, DedupOrdinal: t.DedupOrdinal, DeletedAt: t.DeletedAt,
		})
	}
	return out, nil
}

// RowsByDedupKeys procura PELA CHAVE, sem olhar data nenhuma — é justamente a
// diferença dela para WindowForDedup, e é o que fecha o furo da chave natural
// quando a data da mesma transação muda entre dois downloads.
func (r *repoFake) RowsByDedupKeys(_ context.Context, householdID string, dedupKeys []string) ([]transaction.DedupKeyRow, error) {
	if householdID == "" {
		return nil, errors.New("busca por chave exige a casa")
	}
	procuradas := make(map[string]struct{}, len(dedupKeys))
	for _, chave := range dedupKeys {
		if chave != "" {
			procuradas[chave] = struct{}{}
		}
	}

	var out []transaction.DedupKeyRow
	// A ordem vem de r.ordem para o resultado ser estável entre execuções —
	// varrer o mapa daria uma ordem diferente a cada vez.
	for _, id := range r.ordem {
		t := r.linhas[id]
		if t.HouseholdID != householdID {
			continue
		}
		if _, quer := procuradas[t.DedupKey]; !quer {
			continue
		}
		out = append(out, transaction.DedupKeyRow{
			ID: t.ID, DedupKey: t.DedupKey, DedupOrdinal: t.DedupOrdinal, DeletedAt: t.DeletedAt,
		})
	}
	return out, nil
}

func (r *repoFake) ExternalIDsInWindow(_ context.Context, householdID, excludeAccountID string, minDate, maxDate civil.Date) (map[string]transaction.ExternalIDUse, error) {
	out := map[string]transaction.ExternalIDUse{}
	for _, t := range r.linhas {
		if t.HouseholdID != householdID || t.DeletedAt != nil || t.ExternalID == nil || *t.ExternalID == "" {
			continue
		}
		if excludeAccountID != "" && t.AccountID == excludeAccountID {
			continue
		}
		if t.OccurredOn.Before(minDate) || t.OccurredOn.After(maxDate) {
			continue
		}
		if _, ocupado := out[*t.ExternalID]; ocupado {
			continue
		}
		out[*t.ExternalID] = transaction.ExternalIDUse{TransactionID: t.ID, AccountID: t.AccountID}
	}
	return out, nil
}

func (r *repoFake) ImportBatchFootprint(_ context.Context, householdID, importBatchID string) (transaction.ImportFootprint, error) {
	var out transaction.ImportFootprint
	for _, id := range r.ordem {
		t := r.linhas[id]
		if t.HouseholdID != householdID || t.ImportBatchID == nil || *t.ImportBatchID != importBatchID {
			continue
		}
		if out.StatementID == nil && t.StatementID != nil && *t.StatementID != "" {
			sid := *t.StatementID
			out.StatementID = &sid
		}
	}
	return out, nil
}

// --- schema v4 (spec 0005) --------------------------------------------------
//
// Os métodos abaixo reproduzem o contrato do repositório real no que importa
// para o serviço: escopo por casa, só linhas vivas onde o real usa scope, e o
// `category_id IS NULL` de SetCategoryWhereNull.

func (r *repoFake) ListUncategorized(_ context.Context, householdID, competenceMonth string, limit int) ([]transaction.UncategorizedRow, error) {
	if r.erroUncategorized != nil {
		return nil, r.erroUncategorized
	}
	var out []transaction.UncategorizedRow
	for _, id := range r.ordem {
		t := r.linhas[id]
		if t.HouseholdID != householdID || t.DeletedAt != nil || t.CompetenceMonth != competenceMonth {
			continue
		}
		if t.CategoryID != nil || (t.Kind != transaction.KindIncome && t.Kind != transaction.KindExpense) {
			continue
		}
		out = append(out, transaction.UncategorizedRow{
			ID: t.ID, Kind: t.Kind, Description: t.Description, DescriptionNorm: t.DescriptionNorm,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *repoFake) SetCategoryWhereNull(_ context.Context, householdID string, ids []string, categoryID string, at time.Time) (int64, error) {
	var afetadas int64
	for _, id := range ids {
		t, ok := r.linhas[id]
		if !ok || t.HouseholdID != householdID || t.DeletedAt != nil || t.CategoryID != nil {
			continue
		}
		if t.Kind != transaction.KindIncome && t.Kind != transaction.KindExpense {
			continue
		}
		cat := categoryID
		t.CategoryID = &cat
		t.UpdatedAt = at
		r.linhas[id] = t
		afetadas++
	}
	return afetadas, nil
}

// UpdateCategory reproduz o WHERE do real: casa, viva, id e só receita/despesa.
// Aqui a categoria já escolhida É substituída (é a pessoa recategorizando).
func (r *repoFake) UpdateCategory(_ context.Context, householdID, id, categoryID string, at time.Time) error {
	if householdID == "" || id == "" || categoryID == "" {
		return errors.New("categorizar lançamento exige casa, lançamento e categoria")
	}
	t, ok := r.linhas[id]
	if !ok || t.HouseholdID != householdID || t.DeletedAt != nil {
		return transaction.ErrNotFound
	}
	if t.Kind != transaction.KindIncome && t.Kind != transaction.KindExpense {
		return transaction.ErrNotFound
	}
	cat := categoryID
	t.CategoryID = &cat
	t.UpdatedAt = at
	r.linhas[id] = t
	return nil
}

func (r *repoFake) ListTransferLegs(_ context.Context, householdID string, f transaction.TransferFilter) ([]transaction.Transaction, error) {
	gruposDaContraparte := map[string]bool{}
	if f.CounterpartAccountID != "" {
		for _, t := range r.linhas {
			if t.HouseholdID == householdID && t.DeletedAt == nil && t.IsTransfer() &&
				t.AccountID == f.CounterpartAccountID && t.CompetenceMonth == f.CompetenceMonth && t.TransferGroupID != nil {
				gruposDaContraparte[*t.TransferGroupID] = true
			}
		}
	}
	var out []transaction.Transaction
	for _, id := range r.ordem {
		t := r.linhas[id]
		if t.HouseholdID != householdID || t.DeletedAt != nil || !t.IsTransfer() || t.CompetenceMonth != f.CompetenceMonth {
			continue
		}
		if f.AccountID == "" && t.Kind != transaction.KindTransferOut {
			continue
		}
		if f.AccountID != "" && t.AccountID != f.AccountID {
			continue
		}
		if f.CounterpartAccountID != "" && (t.TransferGroupID == nil || !gruposDaContraparte[*t.TransferGroupID]) {
			continue
		}
		if f.Cursor != nil && !antesDoCursor(t, f.Cursor) {
			continue
		}
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b transaction.Transaction) int {
		if c := b.OccurredOn.Compare(a.OccurredOn); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
	limite := f.Limit
	if limite <= 0 {
		limite = transaction.DefaultPageSize
	}
	if len(out) > limite {
		out = out[:limite]
	}
	return out, nil
}

// antesDoCursor reproduz `occurred_on < ? OR (occurred_on = ? AND id < ?)`.
func antesDoCursor(t transaction.Transaction, c *transaction.Cursor) bool {
	if t.OccurredOn.Before(c.OccurredOn) {
		return true
	}
	return t.OccurredOn == c.OccurredOn && t.ID < c.ID
}

func (r *repoFake) ByTransferGroups(_ context.Context, householdID string, groupIDs []string) ([]transaction.Transaction, error) {
	var out []transaction.Transaction
	for _, id := range r.ordem {
		t := r.linhas[id]
		if t.HouseholdID != householdID || t.DeletedAt != nil || t.TransferGroupID == nil {
			continue
		}
		if slices.Contains(groupIDs, *t.TransferGroupID) {
			out = append(out, t)
		}
	}
	return out, nil
}

func (r *repoFake) TransferLegsOfMonth(_ context.Context, householdID, competenceMonth string, limit int) ([]transaction.TransferLegSummary, error) {
	var out []transaction.TransferLegSummary
	for _, id := range r.ordem {
		t := r.linhas[id]
		if t.HouseholdID != householdID || t.DeletedAt != nil || !t.IsTransfer() ||
			t.CompetenceMonth != competenceMonth || t.TransferGroupID == nil {
			continue
		}
		out = append(out, transaction.TransferLegSummary{
			TransferGroupID: *t.TransferGroupID, AccountID: t.AccountID, Kind: t.Kind, AmountCents: t.AmountCents,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *repoFake) SumByAccountUntil(_ context.Context, householdID string, until civil.Date) (map[string]int64, error) {
	out := map[string]int64{}
	for _, t := range r.linhas {
		if t.HouseholdID != householdID || t.DeletedAt != nil || t.OccurredOn.After(until) {
			continue
		}
		out[t.AccountID] += t.SignedAmountCents()
	}
	return out, nil
}

func (r *repoFake) TransferLegsForLinking(_ context.Context, householdID, accountID string, minDate, maxDate civil.Date) ([]transaction.TransferLeg, error) {
	contraparte := map[string]string{}
	for _, t := range r.linhas {
		if t.HouseholdID == householdID && t.DeletedAt == nil && t.TransferGroupID != nil && t.AccountID != accountID {
			contraparte[*t.TransferGroupID] = t.AccountID
		}
	}
	var out []transaction.TransferLeg
	for _, id := range r.ordem {
		t := r.linhas[id]
		if t.HouseholdID != householdID || t.DeletedAt != nil || !t.IsTransfer() ||
			t.AccountID != accountID || t.TransferGroupID == nil {
			continue
		}
		outra, ok := contraparte[*t.TransferGroupID]
		if !ok {
			continue
		}
		out = append(out, transaction.TransferLeg{
			ID: t.ID, Kind: t.Kind, OccurredOn: t.OccurredOn, AmountCents: t.AmountCents,
			TransferGroupID: *t.TransferGroupID, CounterpartAccountID: outra,
			ExternalID: t.ExternalID, ImportBatchID: t.ImportBatchID,
		})
	}
	return out, nil
}

func (r *repoFake) ByIDIncludingDeleted(_ context.Context, householdID, id string) (*transaction.Transaction, error) {
	t, ok := r.linhas[id]
	if !ok || t.HouseholdID != householdID {
		return nil, transaction.ErrNotFound
	}
	copia := t
	return &copia, nil
}

func (r *repoFake) LinkImport(_ context.Context, householdID, id string, f transaction.LinkFields) error {
	t, ok := r.linhas[id]
	if !ok || t.HouseholdID != householdID || t.DeletedAt != nil || t.AccountID != f.AccountID {
		return transaction.ErrNotFound
	}
	for _, outra := range r.linhas {
		if outra.ID != id && outra.HouseholdID == householdID && outra.DedupKey == f.DedupKey && outra.DedupOrdinal == f.DedupOrdinal {
			return transaction.ErrDuplicateDedup
		}
	}
	t.ExternalID = f.ExternalID
	t.DedupKey = f.DedupKey
	t.DedupOrdinal = f.DedupOrdinal
	lote := f.ImportBatchID
	t.ImportBatchID = &lote
	t.UpdatedAt = f.UpdatedAt
	r.linhas[id] = t
	return nil
}

func (r *repoFake) OccurredOnByIDs(_ context.Context, householdID string, ids []string) (map[string]civil.Date, error) {
	out := map[string]civil.Date{}
	for _, id := range ids {
		if t, ok := r.linhas[id]; ok && t.HouseholdID == householdID {
			out[id] = t.OccurredOn
		}
	}
	return out, nil
}

// --- reprocessamento de transferências (spec 0005 §13) ----------------------
//
// Os três reproduzem o WHERE do repositório real: casa, linha viva, receita
// ou despesa, sem grupo — e a conversão condicional com "exatamente uma linha
// afetada" por perna.

func (r *repoFake) candidataDe(t transaction.Transaction) transaction.TransferCandidateRow {
	return transaction.TransferCandidateRow{
		ID: t.ID, AccountID: t.AccountID, Kind: t.Kind, AmountCents: t.AmountCents,
		OccurredOn: t.OccurredOn, Description: t.Description, DescriptionNorm: t.DescriptionNorm,
	}
}

func (r *repoFake) ehCandidata(t transaction.Transaction, householdID string) bool {
	if t.HouseholdID != householdID || t.DeletedAt != nil || t.TransferGroupID != nil {
		return false
	}
	// DESPESA de fatura fica fora (nem candidata, nem espelho): convertê-la
	// mudaria o total cobrado da fatura. A receita de fatura continua.
	if t.StatementID != nil && t.Kind == transaction.KindExpense {
		return false
	}
	return t.Kind == transaction.KindIncome || t.Kind == transaction.KindExpense
}

// emOrdem devolve os ids em (occurred_on, id), a ordem constante do real.
func (r *repoFake) emOrdem() []string {
	ids := slices.Clone(r.ordem)
	slices.SortStableFunc(ids, func(a, b string) int {
		if c := r.linhas[a].OccurredOn.Compare(r.linhas[b].OccurredOn); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	})
	return ids
}

func (r *repoFake) ListTransferCandidates(_ context.Context, householdID, competenceMonth string, limit int) ([]transaction.TransferCandidateRow, error) {
	r.casasConsultadas = append(r.casasConsultadas, householdID)
	if r.erroCandidatas != nil {
		return nil, r.erroCandidatas
	}
	if householdID == "" || competenceMonth == "" || limit <= 0 {
		return nil, errors.New("listar candidatas a transferência exige casa, mês e limite")
	}
	var out []transaction.TransferCandidateRow
	for _, id := range r.emOrdem() {
		t := r.linhas[id]
		if !r.ehCandidata(t, householdID) || t.CompetenceMonth != competenceMonth {
			continue
		}
		out = append(out, r.candidataDe(t))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *repoFake) IncomeExpenseInWindow(_ context.Context, householdID string, minDate, maxDate civil.Date, limit int) ([]transaction.TransferCandidateRow, error) {
	r.casasConsultadas = append(r.casasConsultadas, householdID)
	if householdID == "" || minDate.IsZero() || maxDate.IsZero() || maxDate.Before(minDate) || limit <= 0 {
		return nil, errors.New("janela de espelhos exige casa, intervalo válido e limite")
	}
	var out []transaction.TransferCandidateRow
	for _, id := range r.emOrdem() {
		t := r.linhas[id]
		if !r.ehCandidata(t, householdID) {
			continue
		}
		// A folga de ±DedupWindowDays é do repositório (como WindowForDedup).
		if civil.DaysBetween(t.OccurredOn, minDate) > transaction.DedupWindowDays && t.OccurredOn.Before(minDate) {
			continue
		}
		if civil.DaysBetween(t.OccurredOn, maxDate) > transaction.DedupWindowDays && t.OccurredOn.After(maxDate) {
			continue
		}
		out = append(out, r.candidataDe(t))
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *repoFake) ConvertToTransferPair(_ context.Context, householdID string, p transaction.TransferPairConversion) error {
	r.casasConsultadas = append(r.casasConsultadas, householdID)
	if r.antesDeConverter != nil {
		r.antesDeConverter(p)
	}
	if householdID == "" || p.OutID == "" || p.InID == "" || p.TransferGroupID == "" || p.OutID == p.InID {
		return transaction.ErrTransferConversionConflict
	}
	// O real leva a CONTA de cada perna no WHERE e recusa as duas na mesma
	// conta (A4 da revisão de segurança).
	if p.OutAccountID == "" || p.InAccountID == "" || p.OutAccountID == p.InAccountID {
		return transaction.ErrTransferConversionConflict
	}
	pernas := []struct{ id, conta, de, para string }{
		{p.OutID, p.OutAccountID, transaction.KindExpense, transaction.KindTransferOut},
		{p.InID, p.InAccountID, transaction.KindIncome, transaction.KindTransferIn},
	}
	// Duas fases — confere as duas, depois grava as duas. No real são dois
	// UPDATEs condicionais dentro da transação, e a segunda linha que não
	// afeta exatamente 1 desfaz a primeira; aqui, conferir antes é o que
	// reproduz esse efeito líquido sem transação de verdade.
	for _, perna := range pernas {
		t, ok := r.linhas[perna.id]
		// O WHERE do real: casa, viva, a conta da perna, sem grupo e com o
		// kind ESPERADO.
		if !ok || t.HouseholdID != householdID || t.DeletedAt != nil || t.TransferGroupID != nil {
			return transaction.ErrTransferConversionConflict
		}
		if t.AccountID != perna.conta || t.Kind != perna.de {
			return transaction.ErrTransferConversionConflict
		}
	}
	for _, perna := range pernas {
		t := r.linhas[perna.id]
		grupo := p.TransferGroupID
		t.Kind = perna.para
		t.TransferGroupID = &grupo
		t.CategoryID = nil
		t.UpdatedAt = p.UpdatedAt
		r.linhas[perna.id] = t
	}
	r.conversoes++
	return nil
}

func (r *repoFake) MaxDedupOrdinal(_ context.Context, householdID, dedupKey string) (int, error) {
	maximo := 0
	for _, t := range r.linhas {
		// Conta TAMBÉM as excluídas: elas continuam ocupando o índice único.
		if t.HouseholdID == householdID && t.DedupKey == dedupKey && t.DedupOrdinal > maximo {
			maximo = t.DedupOrdinal
		}
	}
	// Gancho de corrida: simula o outro confirm COMMITANDO entre a leitura do
	// máximo e o INSERT. É a única janela que leitura nenhuma fecha, e é
	// justamente a que o índice único existe para arbitrar.
	if r.aoLerOrdinal != nil {
		r.aoLerOrdinal(dedupKey, maximo)
	}
	return maximo, nil
}

func (r *repoFake) SoftDelete(_ context.Context, householdID, id string, at time.Time) error {
	t, ok := r.linhas[id]
	if !ok || t.HouseholdID != householdID || t.DeletedAt != nil {
		return transaction.ErrNotFound
	}
	t.DeletedAt = &at
	t.UpdatedAt = at
	r.linhas[id] = t
	return nil
}

func (r *repoFake) Restore(_ context.Context, householdID, id string, at time.Time) error {
	t, ok := r.linhas[id]
	if !ok || t.HouseholdID != householdID || t.DeletedAt == nil {
		return transaction.ErrNotFound
	}
	t.DeletedAt = nil
	t.UpdatedAt = at
	r.linhas[id] = t
	return nil
}

func (r *repoFake) ExistsByAccount(_ context.Context, householdID, accountID string) (bool, error) {
	for _, t := range r.linhas {
		if t.HouseholdID == householdID && t.AccountID == accountID {
			return true, nil
		}
	}
	return false, nil
}

func (r *repoFake) ExistsByCategory(_ context.Context, householdID, categoryID string) (bool, error) {
	for _, t := range r.linhas {
		if t.HouseholdID == householdID && t.CategoryID != nil && *t.CategoryID == categoryID {
			return true, nil
		}
	}
	return false, nil
}

func (r *repoFake) ByTransferGroup(_ context.Context, householdID, groupID string) ([]transaction.Transaction, error) {
	if groupID == "" {
		return nil, nil
	}
	var out []transaction.Transaction
	for _, id := range r.ordem {
		t := r.linhas[id]
		if t.HouseholdID != householdID || t.TransferGroupID == nil || *t.TransferGroupID != groupID {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

func (r *repoFake) SumByStatement(_ context.Context, householdID string, ids []string) (map[string]transaction.StatementSum, error) {
	out := map[string]transaction.StatementSum{}
	for _, t := range r.linhas {
		if t.HouseholdID != householdID || t.DeletedAt != nil || t.StatementID == nil {
			continue
		}
		if !slices.Contains(ids, *t.StatementID) {
			continue
		}
		soma := out[*t.StatementID]
		soma.LineCount++
		switch t.Kind {
		case transaction.KindExpense:
			soma.TotalCents += t.AmountCents
		case transaction.KindIncome:
			soma.TotalCents -= t.AmountCents
		case transaction.KindTransferIn:
			soma.PaidCents += t.AmountCents
		}
		out[*t.StatementID] = soma
	}
	return out, nil
}

// contasFake responde como o repositório de contas: nada sai sem bater a casa.
type contasFake struct {
	linhas map[string]account.Account
	// palavras alimenta o classificador (spec 0005); vazio por padrão.
	palavras []account.Keyword
}

func novasContas() *contasFake { return &contasFake{linhas: map[string]account.Account{}} }

func (c *contasFake) add(a account.Account) account.Account {
	c.linhas[a.ID] = a
	return a
}

func (c *contasFake) ByID(_ context.Context, householdID, id string) (*account.Account, error) {
	a, ok := c.linhas[id]
	if !ok || a.HouseholdID != householdID {
		return nil, account.ErrNotFound
	}
	copia := a
	return &copia, nil
}

func (c *contasFake) List(_ context.Context, householdID string, includeArchived bool) ([]account.Account, error) {
	var out []account.Account
	for _, a := range c.linhas {
		if a.HouseholdID != householdID {
			continue
		}
		if !includeArchived && a.ArchivedAt != nil {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func (c *contasFake) ListKeywords(_ context.Context, householdID string) ([]account.Keyword, error) {
	var out []account.Keyword
	for _, k := range c.palavras {
		if k.HouseholdID == householdID {
			out = append(out, k)
		}
	}
	return out, nil
}

type categoriasFake struct {
	linhas map[string]category.Category
	// palavras alimenta o classificador (spec 0005); vazio por padrão.
	palavras []category.Keyword
	// filhasPedidas conta as consultas de subcategorias por grupo (spec 0005
	// §13): é com ele que o teste prova que a regra não faz N+1 e que folha
	// não consulta nada.
	filhasPedidas map[string]int
	// listadas conta as chamadas de List. É a prova, POR CONTAGEM, de que
	// GET /transactions faz UMA leitura da taxonomia por requisição
	// (critério 17 da E7) — inspecionar o código não prova nada contra a
	// próxima alteração.
	listadas int
	// atribuiveisPedidas conta as chamadas de LiveStates — a reconferência
	// dos achados A4/A9. Uma por EXECUÇÃO real, zero na prévia.
	atribuiveisPedidas int
	// ignorarCasa faz List devolver TODAS as categorias, de todas as casas —
	// uma fonte defeituosa de propósito. Existe para que o teste de
	// isolamento meça a defesa do SERVIÇO, e não a do dublê: um dublê que
	// filtra por casa prova apenas que ele filtra.
	ignorarCasa bool
}

func novasCategorias() *categoriasFake {
	return &categoriasFake{linhas: map[string]category.Category{}, filhasPedidas: map[string]int{}}
}

func (c *categoriasFake) add(k category.Category) category.Category {
	c.linhas[k.ID] = k
	return k
}

func (c *categoriasFake) ByID(_ context.Context, householdID, id string) (*category.Category, error) {
	k, ok := c.linhas[id]
	if !ok || k.HouseholdID != householdID {
		return nil, category.ErrNotFound
	}
	copia := k
	return &copia, nil
}

func (c *categoriasFake) List(_ context.Context, householdID string, includeArchived bool) ([]category.Category, error) {
	c.listadas++
	var out []category.Category
	for _, k := range c.linhas {
		if k.HouseholdID != householdID && !c.ignorarCasa {
			continue
		}
		if !includeArchived && k.ArchivedAt != nil {
			continue
		}
		out = append(out, k)
	}
	return out, nil
}

// Children devolve as filhas diretas, incluindo as ARQUIVADAS e excluindo as
// excluídas — o mesmo contrato do repositório real (gormstore filtra
// deleted_at IS NULL e não filtra archived_at).
func (c *categoriasFake) Children(_ context.Context, householdID, parentID string) ([]category.Category, error) {
	c.filhasPedidas[parentID]++
	var out []category.Category
	for _, k := range c.linhas {
		if k.HouseholdID != householdID || k.DeletedAt != nil {
			continue
		}
		if k.ParentID != nil && *k.ParentID == parentID {
			out = append(out, k)
		}
	}
	return out, nil
}

// LiveStates reproduz o contrato do repositório real (gormstore): escopo por
// casa, fora a EXCLUÍDA, a NATUREZA ATUAL de cada uma, HasActiveChild no grupo
// com subcategoria ATIVA e Archived na arquivada. A arquivada CONTINUA na
// resposta, MARCADA, e não some: quem decide o que fazer com ela é o chamador,
// e um dublê que a escondesse faria a reconferência parecer certa sem nunca ter
// sido exercitada contra o caso.
//
// A natureza é devolvida RELIDA da linha do dublê, e não congelada na chamada,
// porque é exatamente isso que o achado A9 exercita: o teste troca o `kind` da
// categoria na janela e espera que a reconferência enxergue a troca.
//
// A contagem em `atribuiveisPedidas` é a prova, POR CHAMADA, de que a
// reconferência dos achados A4/A9 é UMA por execução — nunca uma por categoria.
func (c *categoriasFake) LiveStates(_ context.Context, householdID string, ids []string) (map[string]category.LiveState, error) {
	c.atribuiveisPedidas++

	viva := map[string]category.Category{}
	comFilhaAtiva := map[string]struct{}{}
	for _, k := range c.linhas {
		if k.HouseholdID != householdID || k.DeletedAt != nil {
			continue
		}
		viva[k.ID] = k
		if k.ParentID != nil && k.ArchivedAt == nil {
			comFilhaAtiva[*k.ParentID] = struct{}{}
		}
	}

	out := map[string]category.LiveState{}
	for _, id := range ids {
		k, ok := viva[id]
		if !ok {
			continue
		}
		_, grupo := comFilhaAtiva[id]
		out[id] = category.LiveState{ID: id, Kind: k.Kind, HasActiveChild: grupo, Archived: k.ArchivedAt != nil}
	}
	return out, nil
}

func (c *categoriasFake) ListKeywords(_ context.Context, householdID string) ([]category.Keyword, error) {
	var out []category.Keyword
	for _, k := range c.palavras {
		if k.HouseholdID == householdID {
			out = append(out, k)
		}
	}
	return out, nil
}

type faturasFake struct {
	linhas map[string]cardstatement.Statement
}

func novasFaturas() *faturasFake {
	return &faturasFake{linhas: map[string]cardstatement.Statement{}}
}

func (f *faturasFake) add(s cardstatement.Statement) cardstatement.Statement {
	f.linhas[s.ID] = s
	return s
}

func (f *faturasFake) ByID(_ context.Context, householdID, id string) (*cardstatement.Statement, error) {
	s, ok := f.linhas[id]
	if !ok || s.HouseholdID != householdID {
		return nil, cardstatement.ErrNotFound
	}
	copia := s
	return &copia, nil
}

// txDireto executa sem transação de verdade — o comportamento transacional é
// exercitado nos testes de repositório, contra o SQLite.
type txDireto struct{}

func (txDireto) Do(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

// ambiente reúne o serviço e os dublês.
type ambiente struct {
	svc        *transaction.Service
	repo       *repoFake
	contas     *contasFake
	categorias *categoriasFake
	faturas    *faturasFake
	auditor    *auditorFake
}

func novoAmbiente(t *testing.T, opts ...transaction.Option) *ambiente {
	t.Helper()

	repo := novoRepo()
	contas := novasContas()
	categorias := novasCategorias()
	faturas := novasFaturas()
	auditor := &auditorFake{}

	base := []transaction.Option{
		transaction.WithIDs(repo.proximoID),
		transaction.WithClock(func() time.Time { return agora }),
		transaction.WithAudit(auditor),
	}
	classificador := classify.NewLoader(categorias, contas)
	svc := transaction.NewService(repo, contas, categorias, faturas, txDireto{}, classificador, append(base, opts...)...)

	return &ambiente{svc: svc, repo: repo, contas: contas, categorias: categorias, faturas: faturas, auditor: auditor}
}

// conta cria uma conta na casa informada.
func (a *ambiente) conta(casa, id, nome, tipo string) account.Account {
	return a.contas.add(account.Account{
		ID: id, HouseholdID: casa, Name: nome, NameNorm: textnorm.Normalize(nome),
		Kind: tipo, OpeningDate: civil.MustNew(2026, 1, 1),
	})
}

func (a *ambiente) categoria(casa, id, nome, natureza string) category.Category {
	return a.categorias.add(category.Category{
		ID: id, HouseholdID: casa, Name: nome, NameNorm: textnorm.Normalize(nome), Kind: natureza,
	})
}

func ptr[T any](v T) *T { return &v }

// --- listagem -------------------------------------------------------------

// ADR-023(c): o "mês" do app é COMPETÊNCIA. A compra feita em 28/08 dentro da
// fatura que vence em setembro aparece em setembro — que é onde a pessoa
// espera vê-la, e onde ela vai pagá-la.
func TestListFiltraPorCompetenciaENaoPorCaixa(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Cartão", account.KindCreditCard)
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 50_00, Description: "Padaria",
		OccurredOn: civil.MustNew(2026, 8, 28),
		// Caixa é agosto; competência é setembro, porque a linha está na
		// fatura de setembro.
		CompetenceMonth: "2026-09",
	})

	setembro, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)
	require.Len(t, setembro.Items, 1)
	assert.Equal(t, "2026-08", setembro.Items[0].YearMonth, "o mês de caixa continua gravado e visível")
	assert.Equal(t, "2026-09", setembro.Items[0].CompetenceMonth)

	agosto, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-08"})
	require.NoError(t, err)
	assert.Empty(t, agosto.Items, "filtrar por caixa traria a compra no mês errado")
}

// O summary fala do MESMO filtro da lista, e conta a dívida que a D3 cria:
// lançamento importado nasce sem categoria.
func TestListDevolveResumoComSemCategoria(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	cat := amb.categoria(minhaCasa, "cat-1", "Mercado", category.KindExpense)

	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindIncome,
		AmountCents: 1_000_00, Description: "Salário", OccurredOn: civil.MustNew(2026, 9, 5),
	})
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 300_00, Description: "Mercado", OccurredOn: civil.MustNew(2026, 9, 6),
		CategoryID: &cat.ID,
	})
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 20_00, Description: "Sem categoria", OccurredOn: civil.MustNew(2026, 9, 7),
	})

	view, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.EqualValues(t, 1_000_00, view.Summary.IncomeCents)
	assert.EqualValues(t, 320_00, view.Summary.ExpenseCents)
	assert.EqualValues(t, 680_00, view.Summary.NetCents)
	assert.EqualValues(t, 3, view.Summary.Count)
	assert.EqualValues(t, 2, view.Summary.UncategorizedCount)

	// O nome da conta e o da categoria vêm resolvidos pelo servidor.
	assert.Equal(t, "Conta", view.Items[0].AccountName)
}

func TestListPaginaPeloCursorSemRepetirNemPular(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	for dia := 1; dia <= 5; dia++ {
		amb.repo.semear(transaction.Transaction{
			HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
			AmountCents: int64(dia) * 100, Description: fmt.Sprintf("Dia %d", dia),
			OccurredOn: civil.MustNew(2026, 9, dia),
		})
	}

	primeira, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09", Limit: 2})
	require.NoError(t, err)
	require.Len(t, primeira.Items, 2)
	require.NotNil(t, primeira.NextCursor)

	segunda, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
		Month: "2026-09", Limit: 2, Cursor: *primeira.NextCursor,
	})
	require.NoError(t, err)
	require.Len(t, segunda.Items, 2)

	vistos := map[string]bool{}
	for _, it := range append(primeira.Items, segunda.Items...) {
		assert.False(t, vistos[it.ID], "a mesma linha apareceu em duas páginas")
		vistos[it.ID] = true
	}
	// Ordem decrescente por data: a página 1 traz os dias 5 e 4.
	assert.Equal(t, "2026-09-05", primeira.Items[0].OccurredOn.String())
	assert.Equal(t, "2026-09-03", segunda.Items[0].OccurredOn.String())

	ultima, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
		Month: "2026-09", Limit: 10,
	})
	require.NoError(t, err)
	assert.Nil(t, ultima.NextCursor, "sem próxima página o cursor é nulo, nunca uma string vazia")
}

func TestListExigeMesEmFormaCanonica(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	for _, mes := range []string{"", "2026-13", "abc", "2026-1", "2026-01-01", " 2026-01"} {
		_, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: mes})
		require.ErrorIs(t, err, transaction.ErrInvalidMonth, "mês %q", mes)
	}
}

func TestListRecusaCursorForjado(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	_, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
		Month: "2026-09", Cursor: "nao-e-base64-valido!!",
	})
	require.ErrorIs(t, err, transaction.ErrInvalidCursor)
}

// BOLA (docs/SEGURANCA.md §2): conta da vizinha é 404, igual a conta
// inexistente — e o lançamento dela não aparece em consulta nenhuma.
func TestFiltroPorContaDeOutraCasaResponde404(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(outraCasa, "acc-alheia", "Conta da Vizinha", account.KindChecking)
	amb.repo.semear(transaction.Transaction{
		HouseholdID: outraCasa, AccountID: "acc-alheia", Kind: transaction.KindExpense,
		AmountCents: 999_00, Description: "Compra alheia", OccurredOn: civil.MustNew(2026, 9, 10),
	})

	_, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
		Month: "2026-09", AccountID: "acc-alheia",
	})
	require.ErrorIs(t, err, transaction.ErrNotFound)

	// E sem filtro nenhum, o dinheiro da vizinha também não entra no meu mês.
	view, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.Empty(t, view.Items)
	assert.EqualValues(t, 0, view.Summary.ExpenseCents)
}

func TestByIDDeOutraCasaResponde404(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(outraCasa, "acc-alheia", "Conta da Vizinha", account.KindChecking)
	alheio := amb.repo.semear(transaction.Transaction{
		HouseholdID: outraCasa, AccountID: "acc-alheia", Kind: transaction.KindExpense,
		AmountCents: 10_00, Description: "Alheio", OccurredOn: civil.MustNew(2026, 9, 10),
	})

	_, err := amb.svc.ByID(t.Context(), ator(minhaCasa), alheio.ID)
	require.ErrorIs(t, err, transaction.ErrNotFound)

	_, err = amb.svc.ByID(t.Context(), ator(minhaCasa), "00000000-0000-7000-8000-999999999999")
	require.ErrorIs(t, err, transaction.ErrNotFound, "inexistente e alheio são o MESMO erro")
}

// --- exclusão -------------------------------------------------------------

// ADR-016: excluir uma perna exclui o par. Meia transferência faria o dinheiro
// sair de uma conta sem entrar na outra.
func TestExcluirUmaPernaExcluiOParInteiro(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-corrente", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-cartao", "Cartão", account.KindCreditCard)

	grupo := "grp-1"
	saida := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-corrente", Kind: transaction.KindTransferOut,
		AmountCents: 500_00, Description: "Pagamento de fatura", OccurredOn: civil.MustNew(2026, 9, 13),
		TransferGroupID: &grupo,
	})
	entrada := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-cartao", Kind: transaction.KindTransferIn,
		AmountCents: 500_00, Description: "Pagamento de fatura", OccurredOn: civil.MustNew(2026, 9, 13),
		TransferGroupID: &grupo,
	})

	require.NoError(t, amb.svc.SoftDelete(t.Context(), ator(minhaCasa), saida.ID))

	assert.NotNil(t, amb.repo.linhas[saida.ID].DeletedAt)
	assert.NotNil(t, amb.repo.linhas[entrada.ID].DeletedAt, "a outra perna tem de cair junto")

	// Uma entrada de auditoria por perna: as duas são escrita financeira.
	assert.Equal(t, []string{"transaction.deleted", "transaction.deleted"}, amb.auditor.acoes())

	// O saldo derivado volta ao que era: nada de dinheiro sobrando de um lado.
	somas, err := amb.repo.SumByAccount(t.Context(), minhaCasa)
	require.NoError(t, err)
	assert.Empty(t, somas["acc-corrente"])
	assert.Empty(t, somas["acc-cartao"])
}

func TestExcluirLancamentoSimplesNaoTocaEmOutros(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	alvo := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 10_00, Description: "Café", OccurredOn: civil.MustNew(2026, 9, 10),
	})
	vizinho := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 10_00, Description: "Café", OccurredOn: civil.MustNew(2026, 9, 10),
	})

	require.NoError(t, amb.svc.SoftDelete(t.Context(), ator(minhaCasa), alvo.ID))
	assert.NotNil(t, amb.repo.linhas[alvo.ID].DeletedAt)
	assert.Nil(t, amb.repo.linhas[vizinho.ID].DeletedAt)
}

func TestExcluirLancamentoDeOutraCasaResponde404(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	alheio := amb.repo.semear(transaction.Transaction{
		HouseholdID: outraCasa, AccountID: "acc-alheia", Kind: transaction.KindExpense,
		AmountCents: 10_00, Description: "Alheio", OccurredOn: civil.MustNew(2026, 9, 10),
	})

	err := amb.svc.SoftDelete(t.Context(), ator(minhaCasa), alheio.ID)
	require.ErrorIs(t, err, transaction.ErrNotFound)
	assert.Nil(t, amb.repo.linhas[alheio.ID].DeletedAt, "nada da outra casa pode ter sido tocado")
	assert.Empty(t, amb.auditor.registros, "operação recusada não gera rastro de escrita")
}

// --- restauração (ADR-025f) ----------------------------------------------

// A restauração é a exceção estreita: ela devolve o MESMO lançamento, com os
// MESMOS valores. Se ela pudesse mexer em valor, data ou conta, o arquivo
// reimportado sobrescreveria o dado já conferido pela pessoa.
func TestRestaurarDevolveOMesmoLancamentoSemTocarEmCampoFinanceiro(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	cat := amb.categoria(minhaCasa, "cat-1", "Mercado", category.KindExpense)

	original := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 123_45, Description: "Mercado do Bairro", DescriptionNorm: "mercado do bairro",
		OccurredOn: civil.MustNew(2026, 9, 10), CompetenceMonth: "2026-09",
		CategoryID: &cat.ID, DedupKey: chave("abc"), DedupOrdinal: 3,
	})
	require.NoError(t, amb.svc.SoftDelete(t.Context(), ator(minhaCasa), original.ID))
	amb.auditor.registros = nil

	view, err := amb.svc.Restore(t.Context(), ator(minhaCasa), original.ID)
	require.NoError(t, err)

	restaurado := amb.repo.linhas[original.ID]
	assert.Equal(t, original.ID, view.ID, "restaurar devolve o mesmo id, não cria outro lançamento")
	assert.Nil(t, restaurado.DeletedAt)
	assert.Equal(t, agora, restaurado.UpdatedAt)

	// Nada além de deleted_at e updated_at pode ter mudado.
	assert.EqualValues(t, 123_45, restaurado.AmountCents)
	assert.Equal(t, original.OccurredOn, restaurado.OccurredOn)
	assert.Equal(t, original.AccountID, restaurado.AccountID)
	assert.Equal(t, original.CategoryID, restaurado.CategoryID)
	assert.Equal(t, original.CompetenceMonth, restaurado.CompetenceMonth)
	assert.Equal(t, original.DedupKey, restaurado.DedupKey)
	assert.Equal(t, original.DedupOrdinal, restaurado.DedupOrdinal)
	assert.Equal(t, original.CreatedAt, restaurado.CreatedAt)

	assert.Equal(t, []string{"transaction.restored"}, amb.auditor.acoes())
}

func TestRestaurarTransferenciaVoltaComOParInteiro(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-corrente", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-cartao", "Cartão", account.KindCreditCard)

	grupo := "grp-1"
	saida := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-corrente", Kind: transaction.KindTransferOut,
		AmountCents: 500_00, Description: "Pagamento", OccurredOn: civil.MustNew(2026, 9, 13),
		TransferGroupID: &grupo,
	})
	entrada := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-cartao", Kind: transaction.KindTransferIn,
		AmountCents: 500_00, Description: "Pagamento", OccurredOn: civil.MustNew(2026, 9, 13),
		TransferGroupID: &grupo,
	})
	require.NoError(t, amb.svc.SoftDelete(t.Context(), ator(minhaCasa), saida.ID))

	_, err := amb.svc.Restore(t.Context(), ator(minhaCasa), saida.ID)
	require.NoError(t, err)

	assert.Nil(t, amb.repo.linhas[saida.ID].DeletedAt)
	assert.Nil(t, amb.repo.linhas[entrada.ID].DeletedAt, "meia transferência não existe, nem na volta")
}

func TestRestaurarLancamentoDeOutraCasaResponde404(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	excluido := agora
	alheio := amb.repo.semear(transaction.Transaction{
		HouseholdID: outraCasa, AccountID: "acc-alheia", Kind: transaction.KindExpense,
		AmountCents: 10_00, Description: "Alheio", OccurredOn: civil.MustNew(2026, 9, 10),
		DeletedAt: &excluido,
	})

	_, err := amb.svc.Restore(t.Context(), ator(minhaCasa), alheio.ID)
	require.ErrorIs(t, err, transaction.ErrNotFound)
	assert.NotNil(t, amb.repo.linhas[alheio.ID].DeletedAt, "o lançamento da vizinha continua excluído")
}

// --- escrita em lote ------------------------------------------------------

func loteValido(rows ...transaction.NewTransaction) transaction.CreateBatchInput {
	return transaction.CreateBatchInput{
		Source:        transaction.SourceImport,
		ImportBatchID: ptr("lote-1"),
		Rows:          rows,
	}
}

func linha(conta string, valor int64, dia int, k string) transaction.NewTransaction {
	return transaction.NewTransaction{
		Kind: transaction.KindExpense, AccountID: conta, AmountCents: valor,
		Description: "Padaria Exemplo", OccurredOn: civil.MustNew(2026, 9, dia),
		DedupKey: chave(k),
	}
}

// ADR-025(b): o ordinal é a enésima ocorrência da tupla NA CASA, contado no
// banco e continuado em memória para as linhas do próprio lote. É o que faz
// duas compras idênticas no mesmo dia entrarem as duas — sumir com um gasto
// real é pior do que duplicá-lo.
func TestCreateBatchAtribuiOrdinalContinuandoOQueJaExisteNoBanco(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)

	// Já existe uma ocorrência da mesma tupla, inclusive uma EXCLUÍDA: as duas
	// ocupam o índice único.
	excluido := agora
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 11_00, Description: "Cafe Exemplo", OccurredOn: civil.MustNew(2026, 9, 5),
		DedupKey: chave("cafe"), DedupOrdinal: 1,
	})
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 11_00, Description: "Cafe Exemplo", OccurredOn: civil.MustNew(2026, 9, 5),
		DedupKey: chave("cafe"), DedupOrdinal: 2, DeletedAt: &excluido,
	})

	res, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(
		linha("acc-1", 11_00, 5, "cafe"),
		linha("acc-1", 11_00, 5, "cafe"),
	))
	require.NoError(t, err)
	require.Len(t, res.IDs, 2)

	assert.Equal(t, 3, amb.repo.linhas[res.IDs[0]].DedupOrdinal)
	assert.Equal(t, 4, amb.repo.linhas[res.IDs[1]].DedupOrdinal)
}

// Critério de aceite 6 da spec: a garantia é do BANCO. O serviço traduz a
// violação do índice em erro tipado — "linha bloqueada" —, nunca em 500.
func TestCreateBatchTraduzViolacaoDoIndiceUnicoEmLinhaBloqueada(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)

	// O outro morador confirma o MESMO arquivo no mesmo instante: a linha dele
	// é gravada DEPOIS da nossa leitura do ordinal máximo e ANTES do nosso
	// INSERT. Nenhuma leitura fecha essa janela — quem arbitra é o índice
	// único, e o segundo confirm tem de sair dela com "linha bloqueada".
	amb.repo.aoLerOrdinal = func(dedupKey string, maximo int) {
		amb.repo.aoLerOrdinal = nil
		amb.repo.semear(transaction.Transaction{
			HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
			AmountCents: 11_00, Description: "Cafe", OccurredOn: civil.MustNew(2026, 9, 5),
			DedupKey: dedupKey, DedupOrdinal: maximo + 1,
		})
	}

	_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(
		linha("acc-1", 11_00, 5, "dup"),
	))
	require.Error(t, err)
	assert.True(t, transaction.IsBlocked(err), "a colisão precisa chegar tipada em quem confirma o lote: %v", err)
	assert.ErrorIs(t, err, transaction.ErrDuplicateDedup)
}

// ADR-023(b): dentro da fatura, a competência é a DA FATURA.
func TestCreateBatchDerivaCompetenciaDaFaturaEDoCaixaForaDela(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-cartao", "Cartão", account.KindCreditCard)
	amb.conta(minhaCasa, "acc-corrente", "Conta", account.KindChecking)
	amb.faturas.add(cardstatement.Statement{
		ID: "st-1", HouseholdID: minhaCasa, AccountID: "acc-cartao",
		CompetenceMonth: "2026-09", ClosingDate: civil.MustNew(2026, 9, 5),
		DueDate: civil.MustNew(2026, 9, 13),
	})

	naFatura := linha("acc-cartao", 80_00, 28, "fat")
	naFatura.OccurredOn = civil.MustNew(2026, 8, 28)
	naFatura.StatementID = ptr("st-1")

	res, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(
		naFatura,
		linha("acc-corrente", 20_00, 7, "cx"),
	))
	require.NoError(t, err)

	assert.Equal(t, "2026-09", amb.repo.linhas[res.IDs[0]].CompetenceMonth, "na fatura, competência é a da fatura")
	assert.Equal(t, "2026-09", amb.repo.linhas[res.IDs[1]].CompetenceMonth, "fora da fatura, competência é o caixa")
	assert.Equal(t, "2026-08", amb.repo.linhas[res.IDs[0]].OccurredOn.YearMonth(), "o caixa continua sendo agosto")
}

func TestCreateBatchRecusaFaturaDeOutraConta(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-cartao-a", "Cartão A", account.KindCreditCard)
	amb.conta(minhaCasa, "acc-cartao-b", "Cartão B", account.KindCreditCard)
	amb.faturas.add(cardstatement.Statement{
		ID: "st-a", HouseholdID: minhaCasa, AccountID: "acc-cartao-a", CompetenceMonth: "2026-09",
	})

	linhaErrada := linha("acc-cartao-b", 80_00, 3, "x")
	linhaErrada.StatementID = ptr("st-a")

	_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(linhaErrada))
	require.ErrorIs(t, err, transaction.ErrStatementMismatch)
	assert.Zero(t, amb.repo.criadas, "nada é gravado quando o lote é recusado")
}

// BOLA no lote: conta, categoria e fatura de outra casa são 404, e o lote
// inteiro não entra.
func TestCreateBatchRecusaReferenciasDeOutraCasa(t *testing.T) {
	t.Parallel()

	casos := map[string]func(*ambiente) transaction.NewTransaction{
		"conta": func(a *ambiente) transaction.NewTransaction {
			a.conta(outraCasa, "acc-alheia", "Alheia", account.KindChecking)
			return linha("acc-alheia", 10_00, 3, "a")
		},
		"categoria": func(a *ambiente) transaction.NewTransaction {
			a.conta(minhaCasa, "acc-1", "Minha", account.KindChecking)
			a.categoria(outraCasa, "cat-alheia", "Alheia", category.KindExpense)
			l := linha("acc-1", 10_00, 3, "b")
			l.CategoryID = ptr("cat-alheia")
			return l
		},
		"fatura": func(a *ambiente) transaction.NewTransaction {
			a.conta(minhaCasa, "acc-cartao", "Cartão", account.KindCreditCard)
			a.faturas.add(cardstatement.Statement{
				ID: "st-alheia", HouseholdID: outraCasa, AccountID: "acc-cartao", CompetenceMonth: "2026-09",
			})
			l := linha("acc-cartao", 10_00, 3, "c")
			l.StatementID = ptr("st-alheia")
			return l
		},
	}

	for nome, montar := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			amb := novoAmbiente(t)
			_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(montar(amb)))
			require.ErrorIs(t, err, transaction.ErrNotFound)
			assert.Zero(t, amb.repo.criadas)
		})
	}
}

func TestCreateBatchRecusaContaEcategoriaArquivadas(t *testing.T) {
	t.Parallel()

	arquivada := agora

	t.Run("conta", func(t *testing.T) {
		t.Parallel()
		amb := novoAmbiente(t)
		amb.contas.add(account.Account{
			ID: "acc-1", HouseholdID: minhaCasa, Name: "Antiga", Kind: account.KindChecking,
			ArchivedAt: &arquivada,
		})
		_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(linha("acc-1", 10_00, 3, "a")))
		require.ErrorIs(t, err, transaction.ErrAccountArchived)
	})

	t.Run("categoria", func(t *testing.T) {
		t.Parallel()
		amb := novoAmbiente(t)
		amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
		amb.categorias.add(category.Category{
			ID: "cat-1", HouseholdID: minhaCasa, Name: "Antiga", Kind: category.KindExpense,
			ArchivedAt: &arquivada,
		})
		l := linha("acc-1", 10_00, 3, "a")
		l.CategoryID = ptr("cat-1")
		_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(l))
		require.ErrorIs(t, err, transaction.ErrCategoryArchived)
	})
}

func TestCreateBatchRecusaCategoriaDeNaturezaTrocada(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-receita", "Salário", category.KindIncome)

	l := linha("acc-1", 10_00, 3, "a") // despesa
	l.CategoryID = ptr("cat-receita")

	_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(l))
	require.ErrorIs(t, err, transaction.ErrCategoryKindMismatch)
}

// ADR-016: transferência nunca tem categoria, e nunca é meia.
func TestCreateBatchProtegeAsInvariantesDaTransferencia(t *testing.T) {
	t.Parallel()

	grupo := "grp-1"
	saida := func() transaction.NewTransaction {
		return transaction.NewTransaction{
			Kind: transaction.KindTransferOut, AccountID: "acc-1", AmountCents: 500_00,
			Description: "Pagamento", OccurredOn: civil.MustNew(2026, 9, 13),
			DedupKey: chave("out"), TransferGroupID: &grupo,
		}
	}
	entrada := func() transaction.NewTransaction {
		return transaction.NewTransaction{
			Kind: transaction.KindTransferIn, AccountID: "acc-2", AmountCents: 500_00,
			Description: "Pagamento", OccurredOn: civil.MustNew(2026, 9, 13),
			DedupKey: chave("in"), TransferGroupID: &grupo,
		}
	}

	casos := map[string]struct {
		rows []transaction.NewTransaction
		erro error
	}{
		"meia transferência": {
			rows: []transaction.NewTransaction{saida()},
			erro: transaction.ErrBrokenTransfer,
		},
		"perna sem grupo": {
			rows: []transaction.NewTransaction{func() transaction.NewTransaction {
				l := saida()
				l.TransferGroupID = nil
				return l
			}()},
			erro: transaction.ErrBrokenTransfer,
		},
		"valores diferentes": {
			rows: []transaction.NewTransaction{saida(), func() transaction.NewTransaction {
				l := entrada()
				l.AmountCents = 400_00
				return l
			}()},
			erro: transaction.ErrBrokenTransfer,
		},
		"mesma conta dos dois lados": {
			rows: []transaction.NewTransaction{saida(), func() transaction.NewTransaction {
				l := entrada()
				l.AccountID = "acc-1"
				return l
			}()},
			erro: transaction.ErrBrokenTransfer,
		},
		"com categoria": {
			rows: []transaction.NewTransaction{func() transaction.NewTransaction {
				l := saida()
				l.CategoryID = ptr("cat-1")
				return l
			}(), entrada()},
			erro: transaction.ErrCategoryOnTransfer,
		},
		"grupo em linha que não é transferência": {
			rows: []transaction.NewTransaction{func() transaction.NewTransaction {
				l := linha("acc-1", 10_00, 3, "a")
				l.TransferGroupID = &grupo
				return l
			}()},
			erro: transaction.ErrBrokenTransfer,
		},
	}

	for nome, caso := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			amb := novoAmbiente(t)
			amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
			amb.conta(minhaCasa, "acc-2", "Cartão", account.KindCreditCard)
			amb.categoria(minhaCasa, "cat-1", "Mercado", category.KindExpense)

			_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(caso.rows...))
			require.ErrorIs(t, err, caso.erro)
			assert.Zero(t, amb.repo.criadas)
		})
	}
}

func TestCreateBatchGravaOParCompletoDaTransferencia(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-2", "Cartão", account.KindCreditCard)

	grupo := "grp-1"
	res, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(
		transaction.NewTransaction{
			Kind: transaction.KindTransferOut, AccountID: "acc-1", AmountCents: 500_00,
			Description: "Pagamento de fatura", OccurredOn: civil.MustNew(2026, 9, 13),
			DedupKey: chave("out"), TransferGroupID: &grupo,
		},
		transaction.NewTransaction{
			Kind: transaction.KindTransferIn, AccountID: "acc-2", AmountCents: 500_00,
			Description: "Pagamento de fatura", OccurredOn: civil.MustNew(2026, 9, 13),
			DedupKey: chave("in"), TransferGroupID: &grupo,
		},
	))
	require.NoError(t, err)
	require.Len(t, res.IDs, 2)

	// A soma de saldos da casa fica em zero: transferir move dinheiro, não cria.
	somas, err := amb.repo.SumByAccount(t.Context(), minhaCasa)
	require.NoError(t, err)
	assert.EqualValues(t, -500_00, somas["acc-1"])
	assert.EqualValues(t, 500_00, somas["acc-2"])
}

// S2 (mass assignment): o que é derivado é derivado no servidor, sempre.
func TestCreateBatchDerivaOsCamposDoServidor(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)

	l := linha("acc-1", 42_00, 9, "a")
	l.Description = "Pix enviado - Fulano de Tal Silva"

	res, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(l))
	require.NoError(t, err)

	gravado := amb.repo.linhas[res.IDs[0]]
	assert.Equal(t, minhaCasa, gravado.HouseholdID, "a casa vem do token")
	assert.Equal(t, usuario, gravado.CreatedBy)
	assert.Equal(t, transaction.SourceImport, gravado.Source)
	assert.Equal(t, "lote-1", *gravado.ImportBatchID)
	assert.Equal(t, agora, gravado.CreatedAt)
	assert.Equal(t, "2026-09", gravado.CompetenceMonth)
	assert.Equal(t, textnorm.Normalize(l.Description), gravado.DescriptionNorm,
		"description_norm é derivada da descrição, nunca aceita de fora")
	assert.Equal(t, 1, gravado.DedupOrdinal)
}

func TestCreateBatchValidaFormaDasLinhas(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		ajustar func(*transaction.NewTransaction)
		erro    error
	}{
		"tipo fora da allowlist": {
			ajustar: func(l *transaction.NewTransaction) { l.Kind = "saque" },
			erro:    transaction.ErrInvalidKind,
		},
		"valor negativo": {
			ajustar: func(l *transaction.NewTransaction) { l.AmountCents = -1 },
			erro:    transaction.ErrInvalidAmount,
		},
		"valor acima do teto": {
			ajustar: func(l *transaction.NewTransaction) { l.AmountCents = transaction.MaxAmountCents + 1 },
			erro:    transaction.ErrInvalidAmount,
		},
		"data ausente": {
			ajustar: func(l *transaction.NewTransaction) { l.OccurredOn = civil.Date{} },
			erro:    transaction.ErrInvalidDate,
		},
		"data absurda": {
			ajustar: func(l *transaction.NewTransaction) { l.OccurredOn = civil.MustNew(1800, 1, 1) },
			erro:    transaction.ErrInvalidDate,
		},
		"descrição acima de 140": {
			ajustar: func(l *transaction.NewTransaction) {
				l.Description = strings.Repeat("á", transaction.MaxDescriptionLen+1)
			},
			erro: transaction.ErrInvalidDescription,
		},
		"chave de deduplicação fora da forma": {
			ajustar: func(l *transaction.NewTransaction) { l.DedupKey = "chave-curta" },
			erro:    transaction.ErrInvalidDedupKey,
		},
	}

	for nome, caso := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			amb := novoAmbiente(t)
			amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)

			l := linha("acc-1", 10_00, 3, "a")
			caso.ajustar(&l)

			_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(l))
			require.ErrorIs(t, err, caso.erro)
			assert.Zero(t, amb.repo.criadas, "linha malformada não grava nada do lote")
		})
	}
}

// O contrato publica amountCents com minimum 0: "sempre não negativo". Uma
// linha legítima de R$ 0,00 não pode ser recusada pelo serviço enquanto a spec
// a permite — divergência entre código e contrato publicado é defeito dos dois.
func TestCreateBatchAceitaValorZeroPorqueOContratoAceita(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)

	_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(linha("acc-1", 0, 3, "a")))
	require.NoError(t, err)
}

func TestCreateBatchExigeCoerenciaEntreOrigemELote(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	l := linha("acc-1", 10_00, 3, "a")

	semLote := transaction.CreateBatchInput{Source: transaction.SourceImport, Rows: []transaction.NewTransaction{l}}
	_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), semLote)
	require.ErrorIs(t, err, transaction.ErrInvalidSource, "importado sem lote de origem perde a rastreabilidade")

	manualComLote := transaction.CreateBatchInput{
		Source: transaction.SourceManual, ImportBatchID: ptr("lote-1"),
		Rows: []transaction.NewTransaction{l},
	}
	_, err = amb.svc.CreateBatch(t.Context(), ator(minhaCasa), manualComLote)
	require.ErrorIs(t, err, transaction.ErrInvalidSource)

	origemInventada := transaction.CreateBatchInput{Source: "api", Rows: []transaction.NewTransaction{l}}
	_, err = amb.svc.CreateBatch(t.Context(), ator(minhaCasa), origemInventada)
	require.ErrorIs(t, err, transaction.ErrInvalidSource)
}

func TestCreateBatchRecusaLoteVazioOuGrandeDemais(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)

	_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido())
	require.ErrorIs(t, err, transaction.ErrEmptyBatch)

	grande := make([]transaction.NewTransaction, transaction.MaxBatchRows+1)
	for i := range grande {
		grande[i] = linha("acc-1", 10_00, 3, fmt.Sprintf("%x", i))
	}
	_, err = amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(grande...))
	require.ErrorIs(t, err, transaction.ErrBatchTooLarge)
}

// A casa vem do token. Sem casa não há operação — e não há, em hipótese
// nenhuma, uma casa vinda do corpo da requisição.
func TestSemCasaNoTokenNadaAcontece(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	vazio := transaction.Actor{UserID: usuario}

	_, err := amb.svc.List(t.Context(), vazio, transaction.ListInput{Month: "2026-09"})
	require.ErrorIs(t, err, transaction.ErrNotFound)

	_, err = amb.svc.ByID(t.Context(), vazio, "qualquer")
	require.ErrorIs(t, err, transaction.ErrNotFound)

	require.ErrorIs(t, amb.svc.SoftDelete(t.Context(), vazio, "qualquer"), transaction.ErrNotFound)

	_, err = amb.svc.Restore(t.Context(), vazio, "qualquer")
	require.ErrorIs(t, err, transaction.ErrNotFound)

	_, err = amb.svc.CreateBatch(t.Context(), vazio, loteValido(linha("acc-1", 10_00, 3, "a")))
	require.ErrorIs(t, err, transaction.ErrNotFound)
}

// --- listagem por fatura --------------------------------------------------

func TestListByStatementSoTrazAsLinhasDaFatura(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-cartao", "Cartão", account.KindCreditCard)
	amb.faturas.add(cardstatement.Statement{
		ID: "st-1", HouseholdID: minhaCasa, AccountID: "acc-cartao", CompetenceMonth: "2026-09",
	})

	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-cartao", Kind: transaction.KindExpense,
		AmountCents: 80_00, Description: "Na fatura", OccurredOn: civil.MustNew(2026, 8, 28),
		CompetenceMonth: "2026-09", StatementID: ptr("st-1"),
	})
	// Mesma conta, mesma competência, FORA da fatura: não pode entrar.
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-cartao", Kind: transaction.KindExpense,
		AmountCents: 10_00, Description: "Fora da fatura", OccurredOn: civil.MustNew(2026, 9, 20),
		CompetenceMonth: "2026-09",
	})

	itens, _, err := amb.svc.ListByStatement(t.Context(), ator(minhaCasa), "st-1", "", 10)
	require.NoError(t, err)
	require.Len(t, itens, 1)
	assert.Equal(t, "Na fatura", itens[0].Description)
}

func TestListByStatementDeOutraCasaResponde404(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.faturas.add(cardstatement.Statement{
		ID: "st-alheia", HouseholdID: outraCasa, AccountID: "acc-alheia", CompetenceMonth: "2026-09",
	})

	_, _, err := amb.svc.ListByStatement(t.Context(), ator(minhaCasa), "st-alheia", "", 10)
	require.ErrorIs(t, err, transaction.ErrNotFound)
}
