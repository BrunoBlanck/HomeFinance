package cardstatement_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	minhaCasa = "casa-1"
	outraCasa = "casa-2"
	usuario   = "user-1"
)

var agora = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)

func ator(casa string) cardstatement.Actor {
	return cardstatement.Actor{HouseholdID: casa, UserID: usuario, IP: "203.0.113.10"}
}

// --- dublês ---------------------------------------------------------------

// repoFake reproduz o contrato do repositório real, incluindo a parte que mais
// importa: o índice único (casa, conta, competência) e o reúso da fatura
// existente com o id DELA, que é o que torna a importação idempotente.
type repoFake struct {
	linhas  map[string]cardstatement.Statement
	ordem   []string
	criadas int
}

func novoRepo() *repoFake {
	return &repoFake{linhas: map[string]cardstatement.Statement{}}
}

func (r *repoFake) Upsert(_ context.Context, s *cardstatement.Statement) error {
	for _, id := range r.ordem {
		existente := r.linhas[id]
		if existente.HouseholdID == s.HouseholdID &&
			existente.AccountID == s.AccountID &&
			existente.CompetenceMonth == s.CompetenceMonth {
			s.ID = existente.ID
			s.CreatedAt = existente.CreatedAt
			s.DeletedAt = nil
			r.linhas[existente.ID] = *s
			return nil
		}
	}
	r.linhas[s.ID] = *s
	r.ordem = append(r.ordem, s.ID)
	r.criadas++
	return nil
}

func (r *repoFake) ByID(_ context.Context, householdID, id string) (*cardstatement.Statement, error) {
	s, ok := r.linhas[id]
	if !ok || s.HouseholdID != householdID || s.DeletedAt != nil {
		return nil, cardstatement.ErrNotFound
	}
	copia := s
	return &copia, nil
}

func (r *repoFake) List(_ context.Context, householdID string, f cardstatement.ListFilter) ([]cardstatement.Statement, error) {
	var out []cardstatement.Statement
	for _, id := range r.ordem {
		s := r.linhas[id]
		if s.HouseholdID != householdID || s.DeletedAt != nil {
			continue
		}
		if f.AccountID != "" && s.AccountID != f.AccountID {
			continue
		}
		if f.CompetenceMonth != "" && s.CompetenceMonth != f.CompetenceMonth {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

type contasFake struct{ linhas map[string]account.Account }

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

// linhasFake devolve os números derivados, no lugar do repositório de
// lançamentos.
type linhasFake struct {
	totais map[string]cardstatement.Totals
	// chamadas conta as IDAS à fonte de totais — é o que prova que a lista
	// inteira é derivada numa consulta só, e não numa por fatura.
	chamadas int
}

func (l *linhasFake) TotalsByStatement(_ context.Context, _ string, ids []string) (map[string]cardstatement.Totals, error) {
	l.chamadas++
	out := map[string]cardstatement.Totals{}
	for _, id := range ids {
		if t, ok := l.totais[id]; ok {
			out[id] = t
		}
	}
	return out, nil
}

// calendarioFake devolve "hoje" no fuso da casa — que NÃO é o do servidor.
type calendarioFake struct {
	dia civil.Date
	err error
}

func (c calendarioFake) Today(context.Context, string) (civil.Date, error) { return c.dia, c.err }

type txDireto struct{}

func (txDireto) Do(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type auditorFake struct{ registros []cardstatement.AuditParams }

func (a *auditorFake) Record(_ context.Context, p cardstatement.AuditParams) error {
	a.registros = append(a.registros, p)
	return nil
}

func (a *auditorFake) acoes() []string {
	out := make([]string, 0, len(a.registros))
	for _, r := range a.registros {
		out = append(out, r.Action)
	}
	return out
}

type ambiente struct {
	svc     *cardstatement.Service
	repo    *repoFake
	contas  *contasFake
	linhas  *linhasFake
	auditor *auditorFake
}

func novoAmbiente(t *testing.T, hoje civil.Date) *ambiente {
	t.Helper()

	repo := novoRepo()
	contas := novasContas()
	linhas := &linhasFake{totais: map[string]cardstatement.Totals{}}
	auditor := &auditorFake{}

	var seq int
	svc := cardstatement.NewService(repo, contas, calendarioFake{dia: hoje}, linhas, txDireto{},
		cardstatement.WithIDs(func() string {
			seq++
			return fmt.Sprintf("st-%03d", seq)
		}),
		cardstatement.WithClock(func() time.Time { return agora }),
		cardstatement.WithAudit(auditor),
	)
	return &ambiente{svc: svc, repo: repo, contas: contas, linhas: linhas, auditor: auditor}
}

func (a *ambiente) cartao(casa, id string) account.Account {
	return a.contas.add(account.Account{
		ID: id, HouseholdID: casa, Name: "Cartão Nubank", Kind: account.KindCreditCard,
	})
}

func entradaValida(contaID string) cardstatement.UpsertInput {
	return cardstatement.UpsertInput{
		AccountID:       contaID,
		CompetenceMonth: "2026-09",
		ClosingDate:     civil.MustNew(2026, 9, 5),
		DueDate:         civil.MustNew(2026, 9, 13),
	}
}

// --- criação e idempotência ----------------------------------------------

// ADR-023(a): importar a MESMA fatura duas vezes reutiliza a que existe. Duas
// faturas de setembro no mesmo cartão significariam a dívida contada em dobro.
func TestUpsertEhIdempotentePorCasaContaECompetencia(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t, civil.MustNew(2026, 9, 10))
	amb.cartao(minhaCasa, "acc-cartao")

	primeira, err := amb.svc.Upsert(t.Context(), ator(minhaCasa), entradaValida("acc-cartao"))
	require.NoError(t, err)

	// A segunda importação corrige o fechamento; a fatura é a MESMA.
	segundaEntrada := entradaValida("acc-cartao")
	segundaEntrada.ClosingDate = civil.MustNew(2026, 9, 6)
	segunda, err := amb.svc.Upsert(t.Context(), ator(minhaCasa), segundaEntrada)
	require.NoError(t, err)

	assert.Equal(t, primeira.ID, segunda.ID, "reimportar não pode criar uma segunda fatura de setembro")
	assert.Equal(t, 1, amb.repo.criadas)
	assert.Equal(t, "2026-09-06", segunda.ClosingDate.String())

	// Reúso não é criação: uma entrada de auditoria, não duas.
	assert.Equal(t, []string{audit.ActionCardStatementCreated}, amb.auditor.acoes())
	assert.Equal(t, audit.EntityCardStatement, amb.auditor.registros[0].Entity)
	assert.Equal(t, primeira.ID, amb.auditor.registros[0].EntityID)
}

// A trava mais forte da importação (§3.3 da spec 0004): fatura só existe em
// conta de cartão.
func TestFaturaSoExisteEmContaDeCartao(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t, civil.MustNew(2026, 9, 10))
	amb.contas.add(account.Account{
		ID: "acc-corrente", HouseholdID: minhaCasa, Name: "Conta", Kind: account.KindChecking,
	})

	_, err := amb.svc.Upsert(t.Context(), ator(minhaCasa), entradaValida("acc-corrente"))
	require.ErrorIs(t, err, cardstatement.ErrNotCreditCard)
	assert.Zero(t, amb.repo.criadas)
	assert.Empty(t, amb.auditor.registros)
}

func TestFaturaEmContaArquivadaEhRecusada(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t, civil.MustNew(2026, 9, 10))
	arquivada := agora
	amb.contas.add(account.Account{
		ID: "acc-cartao", HouseholdID: minhaCasa, Name: "Cartão Antigo",
		Kind: account.KindCreditCard, ArchivedAt: &arquivada,
	})

	_, err := amb.svc.Upsert(t.Context(), ator(minhaCasa), entradaValida("acc-cartao"))
	require.ErrorIs(t, err, cardstatement.ErrAccountArchived)
}

// BOLA: cartão da vizinha é 404, igual a conta inexistente.
func TestFaturaEmContaDeOutraCasaResponde404(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t, civil.MustNew(2026, 9, 10))
	amb.cartao(outraCasa, "acc-alheia")

	_, err := amb.svc.Upsert(t.Context(), ator(minhaCasa), entradaValida("acc-alheia"))
	require.ErrorIs(t, err, cardstatement.ErrNotFound)
	assert.Zero(t, amb.repo.criadas)
}

// D2 da spec 0004: a competência da fatura é o mês do VENCIMENTO. Deixar
// divergir faria a mesma fatura ser de setembro na listagem e de agosto no
// relatório.
func TestCompetenciaPrecisaSerOMesDoVencimento(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t, civil.MustNew(2026, 9, 10))
	amb.cartao(minhaCasa, "acc-cartao")

	entrada := entradaValida("acc-cartao")
	entrada.CompetenceMonth = "2026-08" // vence em 13/09
	_, err := amb.svc.Upsert(t.Context(), ator(minhaCasa), entrada)
	require.ErrorIs(t, err, cardstatement.ErrCompetenceMismatch)
}

func TestDatasDaFaturaSaoValidadas(t *testing.T) {
	t.Parallel()

	casos := map[string]func(*cardstatement.UpsertInput){
		"sem fechamento":            func(in *cardstatement.UpsertInput) { in.ClosingDate = civil.Date{} },
		"sem vencimento":            func(in *cardstatement.UpsertInput) { in.DueDate = civil.Date{} },
		"fecha depois de vencer":    func(in *cardstatement.UpsertInput) { in.ClosingDate = civil.MustNew(2026, 9, 20) },
		"competência fora da forma": func(in *cardstatement.UpsertInput) { in.CompetenceMonth = "set/2026" },
	}

	for nome, ajustar := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			amb := novoAmbiente(t, civil.MustNew(2026, 9, 10))
			amb.cartao(minhaCasa, "acc-cartao")

			entrada := entradaValida("acc-cartao")
			ajustar(&entrada)

			_, err := amb.svc.Upsert(t.Context(), ator(minhaCasa), entrada)
			require.Error(t, err)
			assert.True(t, cardstatement.IsValidationError(err), "erro de entrada, não falha interna: %v", err)
			assert.Zero(t, amb.repo.criadas)
		})
	}
}

// --- números derivados (ADR-023d) ----------------------------------------

func TestTotalEPagoSaoDerivadosDosLancamentos(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t, civil.MustNew(2026, 9, 10))
	amb.cartao(minhaCasa, "acc-cartao")

	fatura, err := amb.svc.Upsert(t.Context(), ator(minhaCasa), entradaValida("acc-cartao"))
	require.NoError(t, err)

	amb.linhas.totais[fatura.ID] = cardstatement.Totals{
		TotalCents: 1_200_00, PaidCents: 500_00, LineCount: 14,
	}

	view, err := amb.svc.ByID(t.Context(), ator(minhaCasa), fatura.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1_200_00, view.TotalCents)
	assert.EqualValues(t, 500_00, view.PaidCents)
	assert.Equal(t, cardstatement.StatusOpen, view.Status)
}

// ADR-019(a): "hoje" é o dia no fuso da CASA. Uma fatura que vence dia 13 não
// pode virar "vencida" porque o servidor, em UTC, já entrou no dia 14.
func TestSituacaoUsaHojeNoFusoDaCasa(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		hoje     civil.Date
		total    int64
		pago     int64
		esperado string
	}{
		"no dia do vencimento ainda está em aberto": {
			hoje: civil.MustNew(2026, 9, 13), total: 100_00, pago: 0, esperado: cardstatement.StatusOpen,
		},
		"no dia seguinte está vencida": {
			hoje: civil.MustNew(2026, 9, 14), total: 100_00, pago: 0, esperado: cardstatement.StatusOverdue,
		},
		"paga em atraso continua paga": {
			hoje: civil.MustNew(2026, 10, 1), total: 100_00, pago: 100_00, esperado: cardstatement.StatusPaid,
		},
		"pagamento parcial não quita": {
			hoje: civil.MustNew(2026, 9, 14), total: 100_00, pago: 99_99, esperado: cardstatement.StatusOverdue,
		},
		"pagamento a maior quita": {
			hoje: civil.MustNew(2026, 9, 14), total: 100_00, pago: 120_00, esperado: cardstatement.StatusPaid,
		},
	}

	for nome, caso := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			amb := novoAmbiente(t, caso.hoje)
			amb.cartao(minhaCasa, "acc-cartao")

			fatura, err := amb.svc.Upsert(t.Context(), ator(minhaCasa), entradaValida("acc-cartao"))
			require.NoError(t, err)
			amb.linhas.totais[fatura.ID] = cardstatement.Totals{TotalCents: caso.total, PaidCents: caso.pago}

			view, err := amb.svc.ByID(t.Context(), ator(minhaCasa), fatura.ID)
			require.NoError(t, err)
			assert.Equal(t, caso.esperado, view.Status)
		})
	}
}

// DeriveStatus é pura e recebe "hoje" como parâmetro: nenhum caminho deste
// pacote consulta o relógio do servidor para decidir se algo venceu.
func TestDeriveStatusEhPuraEDependeDeHojeInformado(t *testing.T) {
	t.Parallel()

	vence := civil.MustNew(2026, 9, 13)

	assert.Equal(t, cardstatement.StatusPaid, cardstatement.DeriveStatus(100, 100, vence, civil.MustNew(2027, 1, 1)))
	assert.Equal(t, cardstatement.StatusOverdue, cardstatement.DeriveStatus(100, 0, vence, civil.MustNew(2026, 9, 14)))
	assert.Equal(t, cardstatement.StatusOpen, cardstatement.DeriveStatus(100, 0, vence, civil.MustNew(2026, 9, 1)))

	// Fatura sem nada a cobrar: não há o que pagar.
	assert.Equal(t, cardstatement.StatusPaid, cardstatement.DeriveStatus(0, 0, vence, civil.MustNew(2027, 1, 1)))
}

// --- leitura --------------------------------------------------------------

func TestListDevolveAsFaturasDaCasaComDerivados(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t, civil.MustNew(2026, 9, 20))
	amb.cartao(minhaCasa, "acc-cartao")
	amb.cartao(outraCasa, "acc-alheio")

	minha, err := amb.svc.Upsert(t.Context(), ator(minhaCasa), entradaValida("acc-cartao"))
	require.NoError(t, err)
	_, err = amb.svc.Upsert(t.Context(), ator(outraCasa), entradaValida("acc-alheio"))
	require.NoError(t, err)

	amb.linhas.totais[minha.ID] = cardstatement.Totals{TotalCents: 300_00, PaidCents: 0}

	antes := amb.linhas.chamadas
	lista, err := amb.svc.List(t.Context(), ator(minhaCasa), cardstatement.ListInput{})
	require.NoError(t, err)
	require.Len(t, lista.Items, 1, "a fatura da outra casa não pode aparecer")
	assert.Equal(t, minha.ID, lista.Items[0].ID)
	assert.EqualValues(t, 300_00, lista.Items[0].TotalCents)
	assert.Equal(t, cardstatement.StatusOverdue, lista.Items[0].Status)

	// Uma consulta de totais para a lista inteira, e não uma por fatura.
	assert.Equal(t, antes+1, amb.linhas.chamadas)
}

func TestFaturaDeOutraCasaResponde404(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t, civil.MustNew(2026, 9, 10))
	amb.cartao(outraCasa, "acc-alheio")
	alheia, err := amb.svc.Upsert(t.Context(), ator(outraCasa), entradaValida("acc-alheio"))
	require.NoError(t, err)

	_, err = amb.svc.ByID(t.Context(), ator(minhaCasa), alheia.ID)
	require.ErrorIs(t, err, cardstatement.ErrNotFound)

	_, err = amb.svc.ByID(t.Context(), ator(minhaCasa), "st-inexistente")
	require.ErrorIs(t, err, cardstatement.ErrNotFound, "inexistente e alheia são o MESMO erro")
}

func TestFiltrarListaPorContaDeOutraCasaResponde404(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t, civil.MustNew(2026, 9, 10))
	amb.cartao(outraCasa, "acc-alheio")

	_, err := amb.svc.List(t.Context(), ator(minhaCasa), cardstatement.ListInput{AccountID: "acc-alheio"})
	require.ErrorIs(t, err, cardstatement.ErrNotFound)
}

func TestSemCasaNoTokenNadaAcontece(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t, civil.MustNew(2026, 9, 10))
	vazio := cardstatement.Actor{UserID: usuario}

	_, err := amb.svc.Upsert(t.Context(), vazio, entradaValida("acc-cartao"))
	require.ErrorIs(t, err, cardstatement.ErrNotFound)

	_, err = amb.svc.ByID(t.Context(), vazio, "st-001")
	require.ErrorIs(t, err, cardstatement.ErrNotFound)

	_, err = amb.svc.List(t.Context(), vazio, cardstatement.ListInput{})
	require.ErrorIs(t, err, cardstatement.ErrNotFound)
}
