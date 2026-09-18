package report_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/report"
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

func ator(casa string) report.Actor {
	return report.Actor{HouseholdID: casa, UserID: usuario}
}

var arquivadaEm = time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC)

// --- dublês ---------------------------------------------------------------

type chamadaLedger struct{ casa, mes, kind string }

// ledgerFake devolve as linhas programadas e registra COM QUE ARGUMENTOS foi
// chamado — é assim que o teste prova que a casa é a do token e que a
// natureza foi conferida antes de chegar aqui.
type ledgerFake struct {
	rows     []report.CategoryTotal
	err      error
	chamadas []chamadaLedger
}

func (l *ledgerFake) SumByCategory(_ context.Context, householdID, competenceMonth, kind string) ([]report.CategoryTotal, error) {
	l.chamadas = append(l.chamadas, chamadaLedger{householdID, competenceMonth, kind})
	if l.err != nil {
		return nil, l.err
	}
	return l.rows, nil
}

type chamadaCategorias struct {
	casa            string
	includeArchived bool
}

// categoriasFake filtra por casa como o repositório real: categoria de outra
// casa NÃO é devolvida.
type categoriasFake struct {
	cats     []category.Category
	err      error
	chamadas []chamadaCategorias
}

func (c *categoriasFake) List(_ context.Context, householdID string, includeArchived bool) ([]category.Category, error) {
	c.chamadas = append(c.chamadas, chamadaCategorias{householdID, includeArchived})
	if c.err != nil {
		return nil, c.err
	}
	var out []category.Category
	for _, cat := range c.cats {
		if cat.HouseholdID != householdID {
			continue
		}
		if !includeArchived && cat.ArchivedAt != nil {
			continue
		}
		out = append(out, cat)
	}
	return out, nil
}

type ambiente struct {
	ledger *ledgerFake
	cats   *categoriasFake
	logs   *bytes.Buffer
	svc    *report.Service
}

func novoAmbiente(t *testing.T) *ambiente {
	t.Helper()
	logs := &bytes.Buffer{}
	lg := logging.New(logs, logging.Options{Level: "debug", Format: "json"})
	a := &ambiente{ledger: &ledgerFake{}, cats: &categoriasFake{}, logs: logs}
	a.svc = report.NewService(a.ledger, a.cats, lg)
	return a
}

func (a *ambiente) categoria(casa, id, nome, kind string, parentID *string, arquivada bool) category.Category {
	c := category.Category{
		ID: id, HouseholdID: casa, ParentID: parentID,
		Name: nome, NameNorm: textnorm.Normalize(nome), Kind: kind,
		CreatedAt: arquivadaEm, UpdatedAt: arquivadaEm,
	}
	if arquivada {
		at := arquivadaEm
		c.ArchivedAt = &at
	}
	a.cats.cats = append(a.cats.cats, c)
	return c
}

func ptr(s string) *string { return &s }

func linha(id *string, cents, count int64) report.CategoryTotal {
	return report.CategoryTotal{CategoryID: id, TotalCents: cents, Count: count}
}

// conferirInvariantes é o critério de aceite 1 e 2 em uma função: totais e
// contagens fecham em todos os níveis, shares somam 10000 quando há valor,
// share do grupo == direto + Σ filhas, ordem de share acompanha ordem de
// total, e nenhum slice é nil.
func conferirInvariantes(t *testing.T, v report.CategoryReportView) {
	t.Helper()
	require.NotNil(t, v.Items, "items nunca é null")

	var somaTotal, somaCount, somaShare int64
	for i, item := range v.Items {
		require.NotNil(t, item.Children, "children nunca é null (item %d)", i)

		var filhosCents, filhosCount, filhosShare int64
		for _, f := range item.Children {
			filhosCents += f.TotalCents
			filhosCount += f.Count
			filhosShare += f.ShareBp
			assert.GreaterOrEqual(t, f.ShareBp, int64(0))
			assert.LessOrEqual(t, f.ShareBp, int64(report.BasisPointsTotal))
			assert.GreaterOrEqual(t, f.Count, int64(1), "filha sem lançamento não aparece")
		}
		assert.Equal(t, item.TotalCents, item.DirectCents+filhosCents, "item %d: total == direto + Σ filhas", i)
		assert.Equal(t, item.Count, item.DirectCount+filhosCount, "item %d: count == direto + Σ filhas", i)
		assert.Equal(t, item.ShareBp, item.DirectShareBp+filhosShare, "item %d: share == directShare + Σ filhas", i)
		assert.GreaterOrEqual(t, item.Count, int64(1), "item sem lançamento não aparece")

		if item.CategoryID == nil {
			assert.Nil(t, item.Name, "balde sem nome")
			assert.Nil(t, item.ArchivedAt)
			assert.Empty(t, item.Children, "balde sem filhas")
			assert.Equal(t, item.TotalCents, item.DirectCents, "balde: direct == total")
			assert.Equal(t, item.Count, item.DirectCount)
			assert.Equal(t, item.ShareBp, item.DirectShareBp)
		} else {
			require.NotNil(t, item.Name)
		}

		// Ordem: total desc, count desc, nome asc (balde por último em empate).
		if i > 0 {
			ant := v.Items[i-1]
			switch {
			case ant.TotalCents != item.TotalCents:
				assert.Greater(t, ant.TotalCents, item.TotalCents, "itens fora de ordem em %d", i)
			case ant.Count != item.Count:
				assert.Greater(t, ant.Count, item.Count, "empate de total fora de ordem em %d", i)
			case ant.CategoryID == nil:
				t.Errorf("balde sem categoria antes de um grupo com o mesmo total/contagem (posição %d)", i)
			case item.CategoryID != nil:
				assert.LessOrEqual(t, textnorm.Normalize(*ant.Name), textnorm.Normalize(*item.Name), "nome fora de ordem em %d", i)
			}
			assert.GreaterOrEqual(t, ant.ShareBp, item.ShareBp, "share não acompanha o total em %d", i)
		}
		for j := 1; j < len(item.Children); j++ {
			ant, cur := item.Children[j-1], item.Children[j]
			switch {
			case ant.TotalCents != cur.TotalCents:
				assert.Greater(t, ant.TotalCents, cur.TotalCents)
			case ant.Count != cur.Count:
				assert.Greater(t, ant.Count, cur.Count)
			default:
				assert.LessOrEqual(t, textnorm.Normalize(ant.Name), textnorm.Normalize(cur.Name))
			}
			assert.GreaterOrEqual(t, ant.ShareBp, cur.ShareBp)
		}

		somaTotal += item.TotalCents
		somaCount += item.Count
		somaShare += item.ShareBp
	}
	assert.Equal(t, v.TotalCents, somaTotal, "totalCents == Σ items")
	assert.Equal(t, v.Count, somaCount, "count == Σ items")
	if v.TotalCents > 0 {
		assert.Equal(t, int64(report.BasisPointsTotal), somaShare, "Σ shareBp == 10000")
	} else {
		assert.Equal(t, int64(0), somaShare, "mês sem valor: todos os shares zero")
	}
}

// --- testes ---------------------------------------------------------------

// Critérios 1, 2 e 6: a árvore fecha, o grupo soma as filhas, o direto fica
// separado, o balde é explícito e cada nível reparte a fatia exata.
func TestByCategoryArvoreFechaEmDoisNiveis(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	casa := a.categoria(minhaCasa, "g-casa", "Casa", category.KindExpense, nil, false)
	luz := a.categoria(minhaCasa, "f-luz", "Luz", category.KindExpense, ptr(casa.ID), false)
	agua := a.categoria(minhaCasa, "f-agua", "Água", category.KindExpense, ptr(casa.ID), false)
	lazer := a.categoria(minhaCasa, "g-lazer", "Lazer", category.KindExpense, nil, false)
	// Grupo com filha cadastrada mas SEM lançamento: a filha não aparece.
	a.categoria(minhaCasa, "f-cinema", "Cinema", category.KindExpense, ptr(lazer.ID), false)

	a.ledger.rows = []report.CategoryTotal{
		linha(ptr(luz.ID), 30_000, 2),
		linha(ptr(agua.ID), 10_000, 1),
		linha(ptr(casa.ID), 5_000, 1), // direto no grupo
		linha(ptr(lazer.ID), 20_000, 3),
		linha(nil, 15_000, 4), // sem categoria
	}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	assert.Equal(t, "2026-09", v.Month)
	assert.Equal(t, "expense", v.Kind)
	assert.Equal(t, int64(80_000), v.TotalCents)
	assert.Equal(t, int64(11), v.Count)
	require.Len(t, v.Items, 3)

	// Casa (45.000) > Lazer (20.000) > Sem categoria (15.000).
	casaV := v.Items[0]
	require.NotNil(t, casaV.CategoryID)
	assert.Equal(t, casa.ID, *casaV.CategoryID)
	assert.Equal(t, "Casa", *casaV.Name)
	assert.Nil(t, casaV.ArchivedAt)
	assert.Equal(t, int64(45_000), casaV.TotalCents)
	assert.Equal(t, int64(4), casaV.Count)
	assert.Equal(t, int64(5_000), casaV.DirectCents)
	assert.Equal(t, int64(1), casaV.DirectCount)
	require.Len(t, casaV.Children, 2)
	assert.Equal(t, "Luz", casaV.Children[0].Name)
	assert.Equal(t, int64(30_000), casaV.Children[0].TotalCents)
	assert.Equal(t, "Água", casaV.Children[1].Name)
	assert.Equal(t, int64(10_000), casaV.Children[1].TotalCents)

	lazerV := v.Items[1]
	assert.Equal(t, lazer.ID, *lazerV.CategoryID)
	assert.Equal(t, int64(20_000), lazerV.TotalCents)
	assert.Equal(t, int64(20_000), lazerV.DirectCents, "grupo sem filha com lançamento: direto == total")
	assert.Equal(t, lazerV.ShareBp, lazerV.DirectShareBp)
	assert.Empty(t, lazerV.Children)
	assert.NotNil(t, lazerV.Children, "children é [] e não null")

	balde := v.Items[2]
	assert.Nil(t, balde.CategoryID)
	assert.Nil(t, balde.Name)
	assert.Equal(t, int64(15_000), balde.TotalCents)
	assert.Equal(t, int64(4), balde.Count)

	// Shares: 45000/80000 = 5625; 20000/80000 = 2500; 15000/80000 = 1875.
	assert.Equal(t, int64(5625), casaV.ShareBp)
	assert.Equal(t, int64(2500), lazerV.ShareBp)
	assert.Equal(t, int64(1875), balde.ShareBp)
	// Dentro de Casa (5625): Luz 30/45 = 3750, Água 10/45 = 1250, direto 5/45 = 625.
	assert.Equal(t, int64(3750), casaV.Children[0].ShareBp)
	assert.Equal(t, int64(1250), casaV.Children[1].ShareBp)
	assert.Equal(t, int64(625), casaV.DirectShareBp)

	// A casa do TOKEN chegou ao ledger e às categorias, com arquivadas.
	require.Len(t, a.ledger.chamadas, 1)
	assert.Equal(t, chamadaLedger{minhaCasa, "2026-09", "expense"}, a.ledger.chamadas[0])
	require.Len(t, a.cats.chamadas, 1)
	assert.Equal(t, chamadaCategorias{minhaCasa, true}, a.cats.chamadas[0])
}

// Critério 4 (visto do serviço): ledger vazio → items [] e total 0, sem
// consultar categorias.
func TestByCategoryMesVazio(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09", Kind: "income"})
	require.NoError(t, err)
	conferirInvariantes(t, v)
	assert.Equal(t, int64(0), v.TotalCents)
	assert.Equal(t, int64(0), v.Count)
	assert.NotNil(t, v.Items)
	assert.Empty(t, v.Items)
	assert.Equal(t, "income", v.Kind)
	assert.Empty(t, a.cats.chamadas, "sem linhas não há o que dobrar")
}

// Critério 5: categoria arquivada aparece, conta e traz archivedAt.
func TestByCategoryArquivadaContaComArchivedAt(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	g := a.categoria(minhaCasa, "g-antiga", "Antiga", category.KindExpense, nil, true)
	f := a.categoria(minhaCasa, "f-velha", "Velha", category.KindExpense, ptr(g.ID), true)
	a.ledger.rows = []report.CategoryTotal{linha(ptr(f.ID), 1_000, 1), linha(ptr(g.ID), 500, 1)}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)
	require.Len(t, v.Items, 1)
	require.NotNil(t, v.Items[0].ArchivedAt)
	assert.Equal(t, "2026-08-01T10:30:00Z", *v.Items[0].ArchivedAt)
	require.Len(t, v.Items[0].Children, 1)
	require.NotNil(t, v.Items[0].Children[0].ArchivedAt)
	assert.Equal(t, "2026-08-01T10:30:00Z", *v.Items[0].Children[0].ArchivedAt)
	assert.Equal(t, int64(1_500), v.TotalCents)
}

// Critério 7: total desc, count desc, nome asc; balde por último no empate.
func TestByCategoryOrdenacaoDeterministica(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	zebra := a.categoria(minhaCasa, "g-z", "Zebra", category.KindExpense, nil, false)
	alfa := a.categoria(minhaCasa, "g-a", "Álfa", category.KindExpense, nil, false)
	beta := a.categoria(minhaCasa, "g-b", "beta", category.KindExpense, nil, false)
	maior := a.categoria(minhaCasa, "g-m", "Maior", category.KindExpense, nil, false)
	maisContas := a.categoria(minhaCasa, "g-c", "Mais contas", category.KindExpense, nil, false)

	a.ledger.rows = []report.CategoryTotal{
		linha(ptr(zebra.ID), 1_000, 1),
		linha(nil, 1_000, 1), // empate total com Zebra/Álfa/beta: vai por último
		linha(ptr(alfa.ID), 1_000, 1),
		linha(ptr(beta.ID), 1_000, 1),
		linha(ptr(maisContas.ID), 1_000, 3), // mesmo total, mais contagem: antes
		linha(ptr(maior.ID), 2_000, 1),
	}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)

	nomes := make([]string, 0, len(v.Items))
	for _, it := range v.Items {
		if it.Name == nil {
			nomes = append(nomes, "<sem categoria>")
			continue
		}
		nomes = append(nomes, *it.Name)
	}
	assert.Equal(t, []string{"Maior", "Mais contas", "Álfa", "beta", "Zebra", "<sem categoria>"}, nomes)
}

// Critério 8: valor zero conta em count, 0 em total, shareBp 0 — e o mês
// inteiro zerado tem todos os shares zero (Σ ≠ 10000 é o esperado aqui).
func TestByCategoryValorZero(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	g := a.categoria(minhaCasa, "g-zero", "Zerada", category.KindExpense, nil, false)
	h := a.categoria(minhaCasa, "g-cheia", "Cheia", category.KindExpense, nil, false)

	t.Run("zero entre valores", func(t *testing.T) {
		a.ledger.rows = []report.CategoryTotal{linha(ptr(g.ID), 0, 2), linha(ptr(h.ID), 1_000, 1)}
		v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
		require.NoError(t, err)
		conferirInvariantes(t, v)
		require.Len(t, v.Items, 2)
		assert.Equal(t, "Cheia", *v.Items[0].Name)
		assert.Equal(t, int64(10_000), v.Items[0].ShareBp)
		assert.Equal(t, "Zerada", *v.Items[1].Name)
		assert.Equal(t, int64(0), v.Items[1].TotalCents)
		assert.Equal(t, int64(2), v.Items[1].Count)
		assert.Equal(t, int64(0), v.Items[1].ShareBp)
	})

	t.Run("mês inteiro zerado", func(t *testing.T) {
		a.ledger.rows = []report.CategoryTotal{linha(ptr(g.ID), 0, 2), linha(nil, 0, 1)}
		v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
		require.NoError(t, err)
		conferirInvariantes(t, v)
		assert.Equal(t, int64(0), v.TotalCents)
		assert.Equal(t, int64(3), v.Count)
		for _, it := range v.Items {
			assert.Equal(t, int64(0), it.ShareBp)
		}
	})
}

// Critério 10 (lado do serviço): kind ausente vira expense; fora da allowlist
// é ErrInvalidKind SEM tocar o ledger; mês inválido idem.
func TestByCategoryValidaAntesDoBanco(t *testing.T) {
	t.Parallel()

	t.Run("kind ausente é expense", func(t *testing.T) {
		a := novoAmbiente(t)
		_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
		require.NoError(t, err)
		require.Len(t, a.ledger.chamadas, 1)
		assert.Equal(t, "expense", a.ledger.chamadas[0].kind)
	})

	for _, kind := range []string{"transfer_out", "transfer_in", "EXPENSE", "Income", "expense,income", "'; DROP TABLE transactions;--", " expense", "expense "} {
		t.Run("kind "+kind, func(t *testing.T) {
			a := novoAmbiente(t)
			_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09", Kind: kind})
			assert.ErrorIs(t, err, report.ErrInvalidKind)
			assert.Empty(t, a.ledger.chamadas, "kind inválido não pode chegar ao banco")
			assert.Empty(t, a.cats.chamadas)
		})
	}

	for _, mes := range []string{"", "2026-13", "2026-1", "2026-09-01", " 2026-09", "2026-09\x00", "2026/09", "202609"} {
		t.Run(fmt.Sprintf("mês %q", mes), func(t *testing.T) {
			a := novoAmbiente(t)
			_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: mes})
			assert.ErrorIs(t, err, transaction.ErrInvalidMonth)
			assert.Empty(t, a.ledger.chamadas)
		})
	}
}

// Critério 11 (lado do serviço): ator sem casa é ErrUnauthenticated, e nada
// é consultado — defesa em profundidade atrás do handler.
func TestByCategorySemCasa(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	_, err := a.svc.ByCategory(t.Context(), report.Actor{}, report.ByCategoryInput{Month: "2026-09"})
	assert.ErrorIs(t, err, report.ErrUnauthenticated)
	assert.Empty(t, a.ledger.chamadas)
}

// Critério 12 (lado do serviço): a categoria de OUTRA casa, ainda que um
// lançamento a aponte, nunca é resolvida — cai no balde e o aviso leva só o
// id. É o que acontece se o banco for adulterado; em condições normais o
// ledger, filtrado por casa, nunca devolve esse id.
func TestByCategoryCategoriaDeOutraCasaCaiNoBalde(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	alheia := a.categoria(outraCasa, "g-alheia", "Segredo Da Vizinha", category.KindExpense, nil, false)
	minha := a.categoria(minhaCasa, "g-minha", "Minha", category.KindExpense, nil, false)
	a.ledger.rows = []report.CategoryTotal{linha(ptr(alheia.ID), 777_777, 1), linha(ptr(minha.ID), 100, 1)}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)
	require.Len(t, v.Items, 2)
	assert.Nil(t, v.Items[0].CategoryID, "o valor da categoria desconhecida vai para o balde")
	assert.Equal(t, int64(777_777), v.Items[0].TotalCents)

	log := a.logs.String()
	assert.Equal(t, 1, strings.Count(log, `"level":"WARN"`), "um aviso por requisição, nunca por linha")
	assert.Contains(t, log, `"categoria_desconhecida":1`)
	assert.Contains(t, log, alheia.ID, "o aviso leva o id")
	assert.NotContains(t, log, "777777", "nunca centavos no log")
	assert.NotContains(t, log, "Segredo", "nunca nome no log")
	assert.NotContains(t, log, "Vizinha")
}

// Filha cujo pai a casa não tem: promovida a grupo próprio, com aviso só do
// id. O dinheiro não some.
func TestByCategoryFilhaSemPaiVuraGrupo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	orfa := a.categoria(minhaCasa, "f-solta", "Órfã", category.KindExpense, ptr("g-inexistente"), false)
	a.ledger.rows = []report.CategoryTotal{linha(ptr(orfa.ID), 4_200, 2)}

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)
	require.Len(t, v.Items, 1)
	require.NotNil(t, v.Items[0].CategoryID)
	assert.Equal(t, orfa.ID, *v.Items[0].CategoryID)
	assert.Equal(t, int64(4_200), v.Items[0].DirectCents)
	assert.Equal(t, int64(10_000), v.Items[0].ShareBp)

	log := a.logs.String()
	assert.Equal(t, 1, strings.Count(log, `"level":"WARN"`), "um aviso por requisição, nunca por linha")
	assert.Contains(t, log, `"pai_ausente":1`)
	assert.Contains(t, log, orfa.ID)
	assert.NotContains(t, log, "4200")
	assert.NotContains(t, log, "Órfã")
	assert.NotContains(t, log, "orfa") // nem a forma normalizada
}

// B1 da revisão de segurança da E6a: o aviso de anomalia é UM por requisição,
// agregado. Antes saía um Warn por LINHA anômala — até 200 por requisição,
// multiplicáveis por recarga de tela dentro do limite de 100 req/min. O teste
// trava as duas metades do contrato: volume (um registro só, com a amostra
// limitada) e diagnóstico (as duas anomalias continuam distinguíveis e a
// contagem é exata, mesmo com mais linhas do que a amostra comporta).
func TestByCategoryAvisoDeAnomaliaEhUmSoPorRequisicao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	const desconhecidas, orfas = 9, 7
	var rows []report.CategoryTotal
	for i := 0; i < desconhecidas; i++ {
		rows = append(rows, linha(ptr(fmt.Sprintf("sumida-%02d", i)), 1_000, 1))
	}
	for i := 0; i < orfas; i++ {
		id := fmt.Sprintf("orfa-%02d", i)
		a.categoria(minhaCasa, id, fmt.Sprintf("Solta %02d", i), category.KindExpense, ptr("g-que-nao-existe"), false)
		rows = append(rows, linha(ptr(id), 2_000, 1))
	}
	a.ledger.rows = rows

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)
	assert.Equal(t, int64(desconhecidas*1_000+orfas*2_000), v.TotalCents, "nenhum centavo some por causa do aviso")

	log := a.logs.String()
	assert.Equal(t, 1, strings.Count(log, `"level":"WARN"`),
		"16 linhas anômalas produzem UM registro, não 16")
	assert.Contains(t, log, `"anomalias":16`)
	assert.Contains(t, log, `"categoria_desconhecida":9`)
	assert.Contains(t, log, `"pai_ausente":7`)

	// A amostra é curta e por tipo — nunca a lista inteira de ids.
	var registro struct {
		AmostraCategoria  []string `json:"amostra_categoria_desconhecida"`
		AmostraPaiAusente []string `json:"amostra_pai_ausente"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(log)), &registro))
	assert.Len(t, registro.AmostraCategoria, 5)
	assert.Len(t, registro.AmostraPaiAusente, 5)
	for _, id := range registro.AmostraCategoria {
		assert.True(t, strings.HasPrefix(id, "sumida-"), "id %q no balde errado", id)
	}
	for _, id := range registro.AmostraPaiAusente {
		assert.True(t, strings.HasPrefix(id, "orfa-"), "id %q no balde errado", id)
	}
	assert.NotContains(t, log, "Solta", "nunca nome no log")
	assert.NotContains(t, log, "2000", "nunca centavos no log")
}

// A mesma linha anômala repetida não infla a amostra: ids distintos, contagem
// completa. (A agregação do banco não repete category_id, mas a amostra não
// pode depender disso para ser útil.)
func TestByCategoryAmostraDeAnomaliaNaoRepeteID(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	a.ledger.rows = []report.CategoryTotal{
		linha(ptr("sumida"), 100, 1),
		linha(ptr("sumida"), 200, 1),
		linha(ptr("sumida"), 300, 1),
	}

	_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)

	log := a.logs.String()
	assert.Equal(t, 1, strings.Count(log, `"level":"WARN"`))
	assert.Contains(t, log, `"anomalias":3`)
	assert.Equal(t, 1, strings.Count(log, `"sumida"`), "o id entra na amostra uma vez só")
}

// Mês limpo não gera aviso nenhum: nada de registro com contagem zero.
func TestByCategorySemAnomaliaNaoAvisa(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	g := a.categoria(minhaCasa, "g-1", "Mercado", category.KindExpense, nil, false)
	a.ledger.rows = []report.CategoryTotal{linha(ptr(g.ID), 1_000, 1), linha(nil, 500, 1)}

	_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.NotContains(t, a.logs.String(), `"level":"WARN"`)
}

// Critério 13: mais linhas do que a taxonomia permite é erro interno (500),
// não de validação, e as categorias nem são consultadas.
func TestByCategoryLinhasDemaisEhErroInterno(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	rows := make([]report.CategoryTotal, 0, category.MaxPerHousehold+2)
	for i := 0; i < category.MaxPerHousehold+2; i++ {
		rows = append(rows, linha(ptr(fmt.Sprintf("c-%03d", i)), 100, 1))
	}
	a.ledger.rows = rows

	_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.Error(t, err)
	assert.False(t, transaction.IsValidationError(err), "não é culpa de quem pediu")
	assert.NotErrorIs(t, err, report.ErrInvalidKind)
	assert.NotErrorIs(t, err, report.ErrUnauthenticated)
	assert.Contains(t, err.Error(), "202 linhas", "a contagem vai no erro (e daí ao log)")
	assert.Empty(t, a.cats.chamadas)
}

// Exatamente MaxPerHousehold + 1 linhas (todas as categorias + a nula) é o
// teto e passa.
func TestByCategoryNoTetoDeLinhasPassa(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	rows := make([]report.CategoryTotal, 0, category.MaxPerHousehold+1)
	for i := 0; i < category.MaxPerHousehold; i++ {
		id := fmt.Sprintf("c-%03d", i)
		a.categoria(minhaCasa, id, "Cat "+id, category.KindExpense, nil, false)
		rows = append(rows, linha(ptr(id), int64(i+1), 1))
	}
	rows = append(rows, linha(nil, 1, 1))
	a.ledger.rows = rows

	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)
	assert.Len(t, v.Items, category.MaxPerHousehold+1)
}

// Erros de infraestrutura sobem embrulhados, com contexto, e sem virar erro
// de validação.
func TestByCategoryPropagaErroDeInfra(t *testing.T) {
	t.Parallel()

	t.Run("ledger", func(t *testing.T) {
		a := novoAmbiente(t)
		a.ledger.err = errors.New("banco fora")
		_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "somando lançamentos por categoria")
		assert.False(t, transaction.IsValidationError(err))
	})

	t.Run("categorias", func(t *testing.T) {
		a := novoAmbiente(t)
		a.ledger.rows = []report.CategoryTotal{linha(nil, 1, 1)}
		a.cats.err = errors.New("banco fora")
		_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "carregando categorias da casa")
	})
}

// Total que não cabe em int64 é erro interno, nunca número errado nem panic.
func TestByCategoryTotalEstouradoEhErroInterno(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	g1 := a.categoria(minhaCasa, "g-1", "Um", category.KindExpense, nil, false)
	g2 := a.categoria(minhaCasa, "g-2", "Dois", category.KindExpense, nil, false)
	a.ledger.rows = []report.CategoryTotal{linha(ptr(g1.ID), math.MaxInt64, 1), linha(ptr(g2.ID), 1, 1)}

	assert.NotPanics(t, func() {
		_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
		require.Error(t, err)
		assert.False(t, transaction.IsValidationError(err))
		assert.NotContains(t, err.Error(), "9223372036854775807", "sem centavos no erro")
	})
}

// B3 da revisão de segurança da E6a: parcela negativa (que centavos e
// COUNT(*) nunca são) é erro interno, não um total esquisito publicado como
// se fosse dinheiro. A guarda vale para os dois números e para o grupo com e
// sem filha — nenhum caminho de soma escapa dela.
func TestByCategoryParcelaNegativaEhErroInterno(t *testing.T) {
	t.Parallel()

	casos := map[string][]report.CategoryTotal{
		"centavos negativos no grupo": {linha(ptr("g-1"), -1, 1)},
		"centavos negativos na filha": {linha(ptr("g-1"), 10, 1), linha(ptr("f-1"), -1, 1)},
		"contagem negativa no grupo":  {linha(ptr("g-1"), 10, -1)},
		"contagem negativa na filha":  {linha(ptr("g-1"), 10, 1), linha(ptr("f-1"), 10, -1)},
		"negativo no balde":           {linha(nil, -5, 1)},
	}

	for nome, rows := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			a := novoAmbiente(t)
			a.categoria(minhaCasa, "g-1", "Um", category.KindExpense, nil, false)
			a.categoria(minhaCasa, "f-1", "Filha", category.KindExpense, ptr("g-1"), false)
			a.ledger.rows = rows

			assert.NotPanics(t, func() {
				_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
				require.Error(t, err)
				assert.False(t, transaction.IsValidationError(err), "não é culpa de quem pediu")
			})
		})
	}
}

// A mesma categoria vinda em duas linhas (não acontece com GROUP BY, mas o
// serviço não pode depender disso) é somada, não duplicada.
func TestByCategoryLinhasRepetidasSomam(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	g := a.categoria(minhaCasa, "g", "Grupo", category.KindExpense, nil, false)
	f := a.categoria(minhaCasa, "f", "Filha", category.KindExpense, ptr(g.ID), false)
	a.ledger.rows = []report.CategoryTotal{
		linha(ptr(f.ID), 100, 1), linha(ptr(f.ID), 200, 2),
		linha(ptr(g.ID), 10, 1), linha(ptr(g.ID), 20, 1),
		linha(nil, 1, 1), linha(nil, 2, 1),
	}
	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	conferirInvariantes(t, v)
	require.Len(t, v.Items, 2)
	assert.Equal(t, int64(330), v.Items[0].TotalCents)
	require.Len(t, v.Items[0].Children, 1)
	assert.Equal(t, int64(300), v.Items[0].Children[0].TotalCents)
	assert.Equal(t, int64(3), v.Items[0].Children[0].Count)
	assert.Equal(t, int64(30), v.Items[0].DirectCents)
	assert.Equal(t, int64(3), v.Items[1].TotalCents)
}

// Propriedade: árvores aleatórias com semente fixa — os invariantes de
// fechamento valem para qualquer combinação de grupos, filhas, diretos,
// balde, arquivadas e zeros.
func TestByCategoryPropriedades(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(27, 2026))
	for iter := 0; iter < 300; iter++ {
		a := novoAmbiente(t)
		nGrupos := 1 + rng.IntN(12)
		var rows []report.CategoryTotal
		for g := 0; g < nGrupos; g++ {
			gid := fmt.Sprintf("g-%d", g)
			a.categoria(minhaCasa, gid, fmt.Sprintf("Grupo %d", rng.IntN(5)), category.KindExpense, nil, rng.IntN(5) == 0)
			if rng.IntN(3) > 0 {
				rows = append(rows, linha(ptr(gid), rng.Int64N(1_000_000), 1+rng.Int64N(5)))
			}
			for f := 0; f < rng.IntN(5); f++ {
				fid := fmt.Sprintf("f-%d-%d", g, f)
				a.categoria(minhaCasa, fid, fmt.Sprintf("Filha %d", rng.IntN(5)), category.KindExpense, ptr(gid), rng.IntN(5) == 0)
				if rng.IntN(4) > 0 {
					valor := rng.Int64N(1_000_000)
					if rng.IntN(8) == 0 {
						valor = 0
					}
					rows = append(rows, linha(ptr(fid), valor, 1+rng.Int64N(5)))
				}
			}
		}
		if rng.IntN(2) == 0 {
			rows = append(rows, linha(nil, rng.Int64N(1_000_000), 1+rng.Int64N(5)))
		}
		a.ledger.rows = rows

		v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
		require.NoError(t, err)
		conferirInvariantes(t, v)
		if t.Failed() {
			t.Fatalf("iteração %d falhou com %d linhas", iter, len(rows))
		}
		// Nenhum aviso: todo id existe na casa.
		assert.NotContains(t, a.logs.String(), `"level":"WARN"`, "iteração %d", iter)
	}
}

// Logger nulo não derruba nada: cai no slog.Default.
func TestNewServiceAceitaLoggerNulo(t *testing.T) {
	t.Parallel()
	svc := report.NewService(&ledgerFake{}, &categoriasFake{}, nil)
	v, err := svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.Empty(t, v.Items)
}

// Sanidade do dublê: o log capturado é JSON com um registro por linha.
func TestLogCapturadoEhJSON(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	lg := logging.New(a.logs, logging.Options{Level: "debug", Format: "json"})
	lg.Warn("x", slog.String("k", "v"))
	assert.True(t, strings.HasPrefix(a.logs.String(), "{"))
}
