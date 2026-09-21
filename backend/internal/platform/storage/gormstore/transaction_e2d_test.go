package gormstore_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Este arquivo cobre o filtro de TIPO de GET /transactions (spec 0004 §12, emenda E2d):
// `kindGroup` = income | expense | transfer | investment, allowlist FECHADA.
//
// Nenhuma tabela, nenhuma coluna e NENHUM ÍNDICE novo — o schema continua v4.
// O que precisa de prova aqui é o WHERE: a lógica de três valores do SQL
// (`NULL NOT IN (…)` é NULL, não verdadeiro), o `IN ()` que nunca é emitido, e
// o orçamento de parâmetros por comando, MEDIDO e não estimado.

// cenarioDeTipos monta, no mesmo mês e na mesma conta, uma linha de cada
// situação que o filtro tem de separar — inclusive as duas que os defeitos
// clássicos escondem: a despesa SEM CATEGORIA e a receita SEM CATEGORIA.
type cenarioDeTipos struct {
	casa      string
	conta     string
	outraCont string

	despesaComum        string
	despesaSemCategoria string
	aporte              string

	receitaComum        string
	receitaSemCategoria string
	resgate             string

	pernaSaida   string
	pernaEntrada string

	marcadas []string
}

const mesDoCenario = "2026-09"

func montarCenarioDeTipos(t *testing.T, s *store) cenarioDeTipos {
	t.Helper()

	ctx := t.Context()
	minha, alheia := s.duasCasas(t, ctx)
	conta := s.makeAccount(t, ctx, minha.ID, "Conta Corrente")
	outra := s.makeAccount(t, ctx, minha.ID, "Poupanca")
	contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

	mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
	salario := s.makeCategory(t, ctx, minha.ID, "Salario", category.KindIncome, nil)
	cdb := s.makeCategory(t, ctx, minha.ID, "CDB", category.KindInvestment, nil)
	resgateCat := s.makeCategory(t, ctx, minha.ID, "Resgate CDB", category.KindRedemption, nil)

	dia := civil.MustNew(2026, 9, 10)
	grupo := s.nextID("g")

	c := cenarioDeTipos{
		casa:      minha.ID,
		conta:     conta.ID,
		outraCont: outra.ID,
		marcadas:  []string{cdb.ID, resgateCat.ID},
	}

	c.despesaComum = s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
		Kind: transaction.KindExpense, AmountCents: 150_00, OccurredOn: dia, CategoryID: &mercado.ID,
	}).ID
	c.despesaSemCategoria = s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
		Kind: transaction.KindExpense, AmountCents: 30_00, OccurredOn: dia,
	}).ID
	c.aporte = s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
		Kind: transaction.KindExpense, AmountCents: 2_000_00, OccurredOn: dia, CategoryID: &cdb.ID,
	}).ID

	c.receitaComum = s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
		Kind: transaction.KindIncome, AmountCents: 5_000_00, OccurredOn: dia, CategoryID: &salario.ID,
	}).ID
	c.receitaSemCategoria = s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
		Kind: transaction.KindIncome, AmountCents: 7_00, OccurredOn: dia,
	}).ID
	c.resgate = s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
		Kind: transaction.KindIncome, AmountCents: 850_00, OccurredOn: dia, CategoryID: &resgateCat.ID,
	}).ID

	c.pernaSaida = s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
		Kind: transaction.KindTransferOut, AmountCents: 400_00, OccurredOn: dia, TransferGroupID: &grupo,
	}).ID
	c.pernaEntrada = s.makeTransaction(t, ctx, minha.ID, outra.ID, txSpec{
		Kind: transaction.KindTransferIn, AmountCents: 400_00, OccurredOn: dia, TransferGroupID: &grupo,
	}).ID

	// Da casa vizinha, no mesmo mês e com a MESMA categoria marcada: nenhum
	// filtro pode alcançá-la (BOLA — docs/SEGURANCA.md §2).
	s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
		Kind: transaction.KindExpense, AmountCents: 999_00, OccurredOn: dia, CategoryID: &cdb.ID,
	})

	return c
}

func idsDe(rows []transaction.Transaction) []string {
	out := make([]string, 0, len(rows))
	for i := range rows {
		out = append(out, rows[i].ID)
	}
	return out
}

// O defeito mais perigoso desta feature, escrito como teste: `NULL NOT IN (…)`
// avalia para NULL — e NULL não passa no WHERE — nos QUATRO dialetos. Sem o
// `category_id IS NULL OR`, a aba "Despesas" perderia TODA despesa sem
// categoria, em silêncio, e o total do resumo cairia junto.
//
// A asserção é sobre a linha SEM CATEGORIA estar presente, e não sobre a
// contagem: uma contagem passaria por acidente se outra linha entrasse no
// lugar dela.
func TestFiltroDeTipoDespesaNaoEscondeDespesaSemCategoria(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDeTipos(t, s)

		rows, err := s.transactions.List(ctx, c.casa, transaction.ListFilter{
			CompetenceMonth:       mesDoCenario,
			KindGroup:             transaction.KindGroupExpense,
			InvestmentCategoryIDs: c.marcadas,
		})
		require.NoError(t, err)

		ids := idsDe(rows)
		assert.Contains(t, ids, c.despesaSemCategoria,
			"despesa SEM categoria tem de continuar na aba Despesas: é o que `category_id IS NULL OR` garante")
		assert.Contains(t, ids, c.despesaComum)
		assert.NotContains(t, ids, c.aporte, "o aporte saiu de Despesas: ele é investimento")
		assert.NotContains(t, ids, c.receitaComum)
		assert.NotContains(t, ids, c.pernaSaida)
		assert.Len(t, ids, 2)

		// O mesmo do lado da receita — o resgate sai, a receita sem categoria
		// fica.
		rows, err = s.transactions.List(ctx, c.casa, transaction.ListFilter{
			CompetenceMonth:       mesDoCenario,
			KindGroup:             transaction.KindGroupIncome,
			InvestmentCategoryIDs: c.marcadas,
		})
		require.NoError(t, err)
		ids = idsDe(rows)
		assert.Contains(t, ids, c.receitaSemCategoria)
		assert.Contains(t, ids, c.receitaComum)
		assert.NotContains(t, ids, c.resgate)
		assert.Len(t, ids, 2)
	})
}

// `transfer` são DOIS kinds, da lista CONSTANTE do código, e `investment` são
// os MESMOS dois kinds de receita/despesa separados pela natureza da
// categoria. Nenhum dos dois é a coluna `kind` sozinha.
func TestFiltroDeTipoTransferenciaEInvestimento(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDeTipos(t, s)

		rows, err := s.transactions.List(ctx, c.casa, transaction.ListFilter{
			CompetenceMonth: mesDoCenario,
			KindGroup:       transaction.KindGroupTransfer,
		})
		require.NoError(t, err)
		ids := idsDe(rows)
		assert.ElementsMatch(t, []string{c.pernaSaida, c.pernaEntrada}, ids,
			"as duas pernas entram: o filtro é do GRUPO, não de uma perna")

		rows, err = s.transactions.List(ctx, c.casa, transaction.ListFilter{
			CompetenceMonth:       mesDoCenario,
			KindGroup:             transaction.KindGroupInvestment,
			InvestmentCategoryIDs: c.marcadas,
		})
		require.NoError(t, err)
		ids = idsDe(rows)
		assert.ElementsMatch(t, []string{c.aporte, c.resgate}, ids,
			"aporte e resgate são os dois lados do mesmo grupo (ADR-029d)")

		// Sem filtro, a janela inteira continua sendo a de sempre.
		rows, err = s.transactions.List(ctx, c.casa, transaction.ListFilter{CompetenceMonth: mesDoCenario})
		require.NoError(t, err)
		assert.Len(t, idsDe(rows), 8, "ausente = tudo")
	})
}

// O isolamento entre casas não depende do filtro: nenhum grupo alcança a
// vizinha, nem com a MESMA categoria marcada.
func TestFiltroDeTipoNaoAlcancaOutraCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDeTipos(t, s)

		for _, grupo := range []string{
			transaction.KindGroupIncome, transaction.KindGroupExpense,
			transaction.KindGroupTransfer, transaction.KindGroupInvestment,
		} {
			rows, err := s.transactions.List(ctx, c.casa, transaction.ListFilter{
				CompetenceMonth:       mesDoCenario,
				KindGroup:             grupo,
				InvestmentCategoryIDs: c.marcadas,
			})
			require.NoError(t, err)
			// O cenário tem linha em TODOS os grupos: sem esta guarda o laço
			// abaixo passaria por vacuidade se a consulta voltasse vazia.
			require.NotEmpty(t, rows, "grupo %q: o cenário tem linhas aqui, a lista não pode vir vazia", grupo)
			for i := range rows {
				assert.Equal(t, c.casa, rows[i].HouseholdID, "grupo %q vazou linha de outra casa", grupo)
			}
		}
	})
}

// Grupo fora da allowlist é ERRO FECHADO, nunca "sem filtro": tratá-lo como
// ausente devolveria a janela INTEIRA a quem pediu um recorte — o modo
// silencioso de mostrar exatamente o que o filtro existia para esconder.
// Nenhum comando vai ao banco.
func TestFiltroDeTipoDesconhecidoFalhaFechadoSemTocarOBanco(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDeTipos(t, s)
		spy := s.espiarSQL(t)

		spy.ligar()
		_, err := s.transactions.List(ctx, c.casa, transaction.ListFilter{
			CompetenceMonth: mesDoCenario, KindGroup: "tudo",
		})
		require.ErrorIs(t, err, transaction.ErrUnknownKindGroup)
		assert.Empty(t, spy.comandosEmitidos())

		spy.ligar()
		_, err = s.transactions.Summary(ctx, c.casa, transaction.SummaryFilter{
			CompetenceMonth: mesDoCenario, KindGroup: "kind = 'income' OR 1=1",
		})
		require.ErrorIs(t, err, transaction.ErrUnknownKindGroup)
		assert.Empty(t, spy.comandosEmitidos(),
			"o grupo escolhe a CLÁUSULA, nunca vira texto de SQL")
	})
}

// ADR-029f no filtro novo: `investment` com conjunto VAZIO é
// ErrEmptyCategoryFilter e NENHUM comando é emitido — `IN ()` é erro de
// sintaxe em três dialetos e `1=0` no quarto.
//
// Um teste que só olhasse o resultado vazio passaria igual se a consulta
// tivesse rodado, que é justamente o caso em que o `IN ()` iria ao banco.
func TestFiltroDeTipoInvestimentoComListaVaziaNaoTocaOBanco(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDeTipos(t, s)
		spy := s.espiarSQL(t)

		// As três formas de vazio: nil, slice vazio e slice só com string
		// vazia (dedupeStrings a esvazia).
		for _, vazio := range [][]string{nil, {}, {"", ""}} {
			spy.ligar()
			_, err := s.transactions.List(ctx, c.casa, transaction.ListFilter{
				CompetenceMonth:       mesDoCenario,
				KindGroup:             transaction.KindGroupInvestment,
				InvestmentCategoryIDs: vazio,
			})
			require.ErrorIs(t, err, transaction.ErrEmptyCategoryFilter)
			assert.Empty(t, spy.comandosEmitidos())

			spy.ligar()
			_, err = s.transactions.Summary(ctx, c.casa, transaction.SummaryFilter{
				CompetenceMonth:       mesDoCenario,
				KindGroup:             transaction.KindGroupInvestment,
				InvestmentCategoryIDs: vazio,
			})
			require.ErrorIs(t, err, transaction.ErrEmptyCategoryFilter)
			assert.Empty(t, spy.comandosEmitidos())
		}
	})
}

// A outra metade do ADR-029f: em `income`/`expense`, conjunto vazio NÃO
// acrescenta cláusula nenhuma. O SQL emitido é o de sempre mais `kind = ?`, e
// `NOT IN` não aparece — nem como `IN ()`.
func TestFiltroDeTipoSemCategoriaMarcadaNaoEmiteNotIn(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDeTipos(t, s)
		spy := s.espiarSQL(t)

		emitido := func(t *testing.T, marcadas []string) comandoSQL {
			t.Helper()
			spy.ligar()
			_, err := s.transactions.List(ctx, c.casa, transaction.ListFilter{
				CompetenceMonth:       mesDoCenario,
				KindGroup:             transaction.KindGroupExpense,
				InvestmentCategoryIDs: marcadas,
			})
			require.NoError(t, err)
			cmds := spy.comandosEmitidos()
			require.Len(t, cmds, 1, "a listagem é UMA consulta, sempre")
			return cmds[0]
		}

		semNada := emitido(t, nil)
		vazio := emitido(t, []string{})
		soVazias := emitido(t, []string{"", ""})

		t.Logf("SQL de kindGroup=expense SEM categoria marcada: %s", semNada.SQL)
		assert.Equal(t, semNada.SQL, vazio.SQL)
		assert.Equal(t, semNada.SQL, soVazias.SQL,
			"lista só com string vazia é lista vazia: o NOT IN não pode entrar")
		assert.NotContains(t, semNada.SQL, "NOT IN", "IN () nunca é emitido (ADR-029f)")
		assert.NotContains(t, semNada.SQL, "IS NULL OR")
		assert.Contains(t, semNada.SQL, "kind = ?")
		// casa + mês + kind. O LIMIT vai INLINE no texto ("LIMIT 50"), e não
		// como bind var — por isso o orçamento é medido, e não deduzido da
		// leitura do código.
		assert.Equal(t, 3, semNada.Parametros)

		// Com o conjunto de verdade, a cláusula entra — entre parênteses, sem
		// os quais o OR se espalharia pelo WHERE inteiro e a consulta
		// devolveria a casa toda. São DOIS pares: o de fora é do GORM (que só
		// envolve quando há mais de uma cláusula) e o de dentro é NOSSO,
		// escrito no texto do predicado. A asserção exige os dois: se alguém
		// apagar o nosso, o isolamento entre casas passa a depender de um
		// detalhe do ORM — e é isso que este teste impede.
		comIDs := emitido(t, c.marcadas)
		t.Logf("SQL de kindGroup=expense COM 2 categorias marcadas: %s", comIDs.SQL)
		assert.Contains(t, comIDs.SQL, "((category_id IS NULL OR category_id NOT IN (?,?)))",
			"o OR precisa estar entre parênteses NOSSOS, além dos do ORM")
		assert.Equal(t, 5, comIDs.Parametros, "casa + mês + kind + 2 categorias")
	})
}

// Orçamento de parâmetros por comando, MEDIDO (molde:
// TestSetCategoryWhereCurrentInCabeNoOrcamentoDeParametros).
//
// O pior caso do E2d é o Summary com 200 categorias marcadas: são DUAS
// projeções condicionais sobre o mesmo conjunto — `marked_total` (dinheiro) e
// `marked_cnt` (contagem) —, 200 parâmetros cada. O `kindGroup` NÃO entra no
// WHERE dele, então não há `NOT IN` aqui: o recorte é feito pelo serviço, por
// aritmética sobre as cinco colunas por kind.
//
// A List é folgada em comparação (uma lista só), mesmo com cursor: o cursor
// custa 3 parâmetros e o LIMIT nenhum — o GORM o escreve inline no texto.
func TestFiltroDeTipoCabeNoOrcamentoDeParametros(t *testing.T) {
	t.Parallel()

	const (
		pisoSQLite = 999
		tetoMSSQL  = 2100
	)

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDeTipos(t, s)

		// Os ids não precisam existir: o que está sob teste é o COMANDO
		// emitido, não o que ele encontra. 200 é o teto real (o mesmo
		// category.MaxPerHousehold).
		marcadas := make([]string, 0, 200)
		for i := range 200 {
			marcadas = append(marcadas, fmt.Sprintf("c-marcada-%03d", i))
		}

		spy := s.espiarSQL(t)

		// --- pior caso: Summary, 200 marcadas, conta ---
		spy.ligar()
		_, err := s.transactions.Summary(ctx, c.casa, transaction.SummaryFilter{
			CompetenceMonth:       mesDoCenario,
			AccountID:             c.conta,
			KindGroup:             transaction.KindGroupExpense,
			InvestmentCategoryIDs: marcadas,
		})
		require.NoError(t, err)
		cmds := spy.comandosEmitidos()
		require.Len(t, cmds, 1, "o resumo é UMA consulta, sempre")
		resumo := cmds[0]
		t.Logf("PIOR CASO Summary: %d parâmetros", resumo.Parametros)
		assert.LessOrEqual(t, resumo.Parametros, pisoSQLite,
			"o pior caso tem de caber no piso histórico de 999 do SQLite")
		assert.LessOrEqual(t, resumo.Parametros, tetoMSSQL,
			"o pior caso tem de caber nos 2100 do SQL Server")
		assert.Equal(t, 403, resumo.Parametros,
			"200 de marked_total + 200 de marked_cnt + casa + mês + conta")

		// --- List, mesmo recorte, com conta E cursor ---
		spy.ligar()
		_, err = s.transactions.List(ctx, c.casa, transaction.ListFilter{
			CompetenceMonth:       mesDoCenario,
			AccountID:             c.conta,
			KindGroup:             transaction.KindGroupExpense,
			InvestmentCategoryIDs: marcadas,
			Cursor:                &transaction.Cursor{OccurredOn: civil.MustNew(2026, 9, 30), ID: "t-zzz"},
			Limit:                 transaction.MaxPageSize,
		})
		require.NoError(t, err)
		cmds = spy.comandosEmitidos()
		require.Len(t, cmds, 1)
		lista := cmds[0]
		t.Logf("PIOR CASO List (com cursor): %d parâmetros", lista.Parametros)
		assert.LessOrEqual(t, lista.Parametros, pisoSQLite)
		assert.Equal(t, 207, lista.Parametros,
			"casa + mês + conta + kind + 200 do NOT IN + 3 do cursor (o LIMIT vai inline)")

		// 201 categorias é ERRO ALTO, e não um comando que estoura dentro do
		// driver em dois dialetos só.
		demais := append(marcadas, "c-marcada-200")
		_, err = s.transactions.List(ctx, c.casa, transaction.ListFilter{
			CompetenceMonth: mesDoCenario, KindGroup: transaction.KindGroupExpense,
			InvestmentCategoryIDs: demais,
		})
		require.ErrorIs(t, err, transaction.ErrTooManyCategories)
		_, err = s.transactions.Summary(ctx, c.casa, transaction.SummaryFilter{
			CompetenceMonth: mesDoCenario, KindGroup: transaction.KindGroupExpense,
			InvestmentCategoryIDs: demais,
		})
		require.ErrorIs(t, err, transaction.ErrTooManyCategories)
	})
}

// O resumo NÃO responde ao `kindGroup`, e é isso que precisa de prova: os
// mesmos números saem das cinco opções, porque `investedCents`/`redeemedCents`
// descrevem o que SAIU de receita e despesa (ADR-029e) e alimentam a frase
// "Fora destes números: R$ X em aportes" (spec 0006 §3.5.2).
//
// Quem recorta é o SERVIÇO (T3), por aritmética sobre Summary.ByKind. O que
// esta camada entrega são as cinco colunas por kind, da MESMA leitura.
func TestSummaryNaoRespondeAoFiltroDeTipoEEntregaAsCincoColunasPorKind(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDeTipos(t, s)

		base := transaction.SummaryFilter{
			CompetenceMonth:       mesDoCenario,
			InvestmentCategoryIDs: c.marcadas,
		}

		semFiltro, err := s.transactions.Summary(ctx, c.casa, base)
		require.NoError(t, err)

		// Os marcados do mês: aporte de 2.000,00 e resgate de 850,00.
		assert.EqualValues(t, 2_000_00, semFiltro.InvestedCents)
		assert.EqualValues(t, 850_00, semFiltro.RedeemedCents)
		assert.EqualValues(t, 180_00, semFiltro.ExpenseCents, "150 + 30, sem o aporte")
		assert.EqualValues(t, 5_007_00, semFiltro.IncomeCents, "5.000 + 7, sem o resgate")
		assert.EqualValues(t, 8, semFiltro.Count, "a janela inteira, transferências inclusive")
		assert.EqualValues(t, 2, semFiltro.Uncategorized)

		// As CINCO opções devolvem os MESMOS números. Se um dia alguém puser o
		// grupo no WHERE, é aqui que aparece — antes de a tela perder a frase.
		for _, grupo := range []string{
			transaction.KindGroupIncome, transaction.KindGroupExpense,
			transaction.KindGroupTransfer, transaction.KindGroupInvestment,
		} {
			f := base
			f.KindGroup = grupo
			comFiltro, err := s.transactions.Summary(ctx, c.casa, f)
			require.NoErrorf(t, err, "grupo %q", grupo)
			assert.Equalf(t, semFiltro, comFiltro,
				"o resumo do grupo %q tem de ser IDÊNTICO ao sem filtro: quem recorta é o serviço", grupo)
		}

		// E as cinco colunas por kind estão lá, cruas, em ordem estável — é
		// delas que a T3 monta os cinco recortes por aritmética.
		porKind := make(map[string]transaction.SummaryKindTotals, len(semFiltro.ByKind))
		ordem := make([]string, 0, len(semFiltro.ByKind))
		for _, k := range semFiltro.ByKind {
			porKind[k.Kind] = k
			ordem = append(ordem, k.Kind)
		}
		assert.Equal(t, []string{"expense", "income", "transfer_in", "transfer_out"}, ordem,
			"a ordem é feita em Go: collation não pode mudar o resultado entre dialetos")

		despesa := porKind[transaction.KindExpense]
		assert.EqualValues(t, 3, despesa.Count)
		assert.EqualValues(t, 2_180_00, despesa.TotalCents)
		assert.EqualValues(t, 1, despesa.Uncategorized)
		assert.EqualValues(t, 2_000_00, despesa.MarkedTotalCents)
		assert.EqualValues(t, 1, despesa.MarkedCount)

		receita := porKind[transaction.KindIncome]
		assert.EqualValues(t, 3, receita.Count)
		assert.EqualValues(t, 5_857_00, receita.TotalCents)
		assert.EqualValues(t, 1, receita.Uncategorized)
		assert.EqualValues(t, 850_00, receita.MarkedTotalCents)
		assert.EqualValues(t, 1, receita.MarkedCount)

		// Transferência nunca é marcada: ela não tem categoria por desenho.
		//
		// E a armadilha que a T3 precisa enxergar: `Uncategorized` da perna é
		// igual a `Count`, e NÃO zero — sem categoria, toda perna casa com
		// `category_id IS NULL`. Somar os quatro kinds produziria "pendências"
		// impossíveis de resolver. O agregado Summary.Uncategorized soma só
		// receita e despesa, e é por isso que ele vale 2 no cenário.
		for _, k := range []string{transaction.KindTransferOut, transaction.KindTransferIn} {
			assert.EqualValues(t, 1, porKind[k].Count, k)
			assert.Zero(t, porKind[k].MarkedCount, k)
			assert.Zero(t, porKind[k].MarkedTotalCents, k)
			assert.EqualValues(t, porKind[k].Count, porKind[k].Uncategorized,
				"perna de transferência conta como 'sem categoria' na projeção CRUA: %s", k)
		}
		assert.EqualValues(t, 2, semFiltro.Uncategorized,
			"o agregado soma SÓ receita e despesa — as duas pernas ficam de fora")

		// A aritmética que a T3 vai fazer fecha com o que a LISTA mostra —
		// que é o ponto inteiro de as cinco opções saírem de uma consulta só.
		itens, err := s.transactions.List(ctx, c.casa, transaction.ListFilter{
			CompetenceMonth: mesDoCenario, KindGroup: transaction.KindGroupExpense,
			InvestmentCategoryIDs: c.marcadas,
		})
		require.NoError(t, err)
		assert.EqualValues(t, len(itens), despesa.Count-despesa.MarkedCount,
			"contagem de Despesas = expense.Count - expense.MarkedCount")

		itens, err = s.transactions.List(ctx, c.casa, transaction.ListFilter{
			CompetenceMonth: mesDoCenario, KindGroup: transaction.KindGroupInvestment,
			InvestmentCategoryIDs: c.marcadas,
		})
		require.NoError(t, err)
		assert.EqualValues(t, len(itens), receita.MarkedCount+despesa.MarkedCount,
			"contagem de Investimentos = income.MarkedCount + expense.MarkedCount")
	})
}

// O teste que TRAVA o desenho: as cinco opções emitem o MESMO comando, byte a
// byte, com o mesmo número de parâmetros. Compara o SQL EMITIDO, não o
// resultado — um teste de resultado passaria igual se o WHERE tivesse mudado e
// o dado do cenário não distinguisse os casos.
func TestSummaryEmiteOMesmoSQLParaOsCincoGruposDeTipo(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDeTipos(t, s)
		spy := s.espiarSQL(t)

		emitido := func(t *testing.T, grupo string) comandoSQL {
			t.Helper()
			spy.ligar()
			_, err := s.transactions.Summary(ctx, c.casa, transaction.SummaryFilter{
				CompetenceMonth:       mesDoCenario,
				AccountID:             c.conta,
				KindGroup:             grupo,
				InvestmentCategoryIDs: c.marcadas,
			})
			require.NoErrorf(t, err, "grupo %q", grupo)
			cmds := spy.comandosEmitidos()
			require.Lenf(t, cmds, 1, "o resumo é UMA consulta, sempre (grupo %q)", grupo)
			return cmds[0]
		}

		referencia := emitido(t, "")
		t.Logf("SQL do Summary (idêntico nas cinco opções): %s", referencia.SQL)

		for _, grupo := range []string{
			transaction.KindGroupIncome, transaction.KindGroupExpense,
			transaction.KindGroupTransfer, transaction.KindGroupInvestment,
		} {
			cmd := emitido(t, grupo)
			assert.Equalf(t, referencia.SQL, cmd.SQL,
				"o grupo %q mudou o SQL do resumo: o kindGroup NUNCA entra no WHERE (ADR-029e)", grupo)
			assert.Equalf(t, referencia.Parametros, cmd.Parametros, "grupo %q", grupo)
		}

		assert.NotContains(t, referencia.SQL, "kind = ?",
			"o resumo não filtra por kind: ele AGRUPA por kind")
		assert.NotContains(t, referencia.SQL, "NOT IN")
		assert.Contains(t, referencia.SQL, "marked_total")
		assert.Contains(t, referencia.SQL, "marked_cnt")
		assert.Contains(t, referencia.SQL, "GROUP BY `kind`")
	})
}

// O filtro não pode mudar a ORDEM nem o CURSOR: é a mesma página de sempre,
// com menos linhas. A ordem é constante no código (P6/S4).
func TestFiltroDeTipoMantemOrdemECursor(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")

		var esperado []string
		for dia := 1; dia <= 5; dia++ {
			tx := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
				Kind: transaction.KindExpense, AmountCents: int64(dia) * 100,
				OccurredOn: civil.MustNew(2026, 9, dia),
			})
			esperado = append([]string{tx.ID}, esperado...) // DESC por data
			// Uma receita no mesmo dia, para o filtro ter o que descartar.
			s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
				Kind: transaction.KindIncome, AmountCents: 10_00, OccurredOn: civil.MustNew(2026, 9, dia),
			})
		}

		var vistos []string
		var cursor *transaction.Cursor
		for range 5 {
			rows, err := s.transactions.List(ctx, minha.ID, transaction.ListFilter{
				CompetenceMonth: mesDoCenario,
				KindGroup:       transaction.KindGroupExpense,
				Cursor:          cursor,
				Limit:           2,
			})
			require.NoError(t, err)
			if len(rows) == 0 {
				break
			}
			vistos = append(vistos, idsDe(rows)...)
			ultima := rows[len(rows)-1]
			cursor = &transaction.Cursor{OccurredOn: ultima.OccurredOn, ID: ultima.ID}
		}

		assert.Equal(t, esperado, vistos,
			"o filtro tira linhas; ele não reordena nem repete página")
	})
}

// O `kindGroup` é allowlist do DOMÍNIO, e a borda valida com ela — não com uma
// segunda lista escrita à mão em outro arquivo.
func TestAllowlistDeKindGroupEFechada(t *testing.T) {
	t.Parallel()

	for _, bom := range []string{
		"", transaction.KindGroupIncome, transaction.KindGroupExpense,
		transaction.KindGroupTransfer, transaction.KindGroupInvestment,
	} {
		assert.True(t, transaction.ValidKindGroup(bom), "%q é da allowlist", bom)
	}
	for _, ruim := range []string{
		"INCOME", "transfer_out", "investments", " income", "income ",
		"income'--", "kind = 'income' OR 1=1", strings.Repeat("a", 300),
	} {
		assert.False(t, transaction.ValidKindGroup(ruim), "%q não pode passar", ruim)
	}
}

// A sonda de DIALETO por trás do `category_id IS NULL OR`: aqui a afirmação
// "`NULL NOT IN (…)` não passa no WHERE" é medida no banco, e não deduzida da
// leitura do padrão SQL.
//
// Ela é diferente do teste de comportamento acima, e as duas precisam existir:
// aquele prova que o repositório ESTÁ certo hoje; esta prova POR QUE a
// cláusula é obrigatória — e falharia, com a mesma clareza, num dialeto que
// decidisse tratar NULL como valor comparável. Ela roda em todo banco que a
// suíte tiver (SQLite sempre; PostgreSQL com TEST_POSTGRES_DSN; os outros dois
// quando a suíte testcontainers da E8 entrar).
//
// A consulta é montada pelo construtor do GORM, com placeholders — nada de
// Raw, nada de texto montado; o único motivo de ela existir fora do
// repositório é que o repositório, por desenho, NUNCA emite a forma errada.
func TestNullNotInNaoPassaNoWhereNesteDialeto(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		c := montarCenarioDeTipos(t, s)

		// As despesas do mês são três: comum (Mercado), SEM CATEGORIA e
		// aporte (CDB, que está no conjunto marcado).
		base := func() *gorm.DB {
			return s.db.Gorm().WithContext(ctx).
				Table("transactions").
				Where("household_id = ?", c.casa).
				Where("deleted_at IS NULL").
				Where("competence_month = ?", mesDoCenario).
				Where("kind = ?", transaction.KindExpense)
		}

		var semGuarda int64
		require.NoError(t, base().
			Where("category_id NOT IN ?", c.marcadas).
			Count(&semGuarda).Error)

		var comGuarda int64
		require.NoError(t, base().
			Where("category_id IS NULL OR category_id NOT IN ?", c.marcadas).
			Count(&comGuarda).Error)

		t.Logf("dialeto %s: sem a guarda = %d linha(s); com a guarda = %d linha(s)",
			s.backendName, semGuarda, comGuarda)

		assert.EqualValues(t, 1, semGuarda,
			"sem `IS NULL OR`, a despesa SEM CATEGORIA some: NULL NOT IN (…) é NULL, e NULL não passa no WHERE")
		assert.EqualValues(t, 2, comGuarda,
			"com a guarda, a despesa sem categoria volta — é o predicado que o repositório emite")
	})
}
