package gormstore_test

import (
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// O teste mais importante deste arquivo. BOLA é o risco nº 1 do projeto
// (docs/SEGURANCA.md §2), e com lançamento o vazamento é pior do que ver um
// nome: as consultas AGREGADAS somam dinheiro. Um household_id esquecido em
// Summary ou em SumByAccount não aparece como "apareceu uma linha estranha" —
// aparece como um saldo errado que ninguém sabe explicar.
func TestLancamentoDeOutraCasaNaoApareceEmNenhumaConsulta(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		minhaConta := s.makeAccount(t, ctx, minha.ID, "Carteira")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta da Vizinha")
		catAlheia := s.makeCategory(t, ctx, alheia.ID, "Mercado", "expense", nil)

		daAlheia := s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 500_00,
			OccurredOn: civil.MustNew(2026, 2, 10), CategoryID: &catAlheia.ID,
		})
		s.makeTransaction(t, ctx, minha.ID, minhaConta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 30_00,
			OccurredOn: civil.MustNew(2026, 2, 10),
		})

		// 1) Busca por id: existe de verdade, mas não para mim.
		_, err := s.transactions.ByID(ctx, minha.ID, daAlheia.ID)
		require.ErrorIs(t, err, transaction.ErrNotFound)

		// 2) Listagem.
		lista, err := s.transactions.List(ctx, minha.ID, transaction.ListFilter{CompetenceMonth: "2026-02"})
		require.NoError(t, err)
		require.Len(t, lista, 1)
		assert.EqualValues(t, 30_00, lista[0].AmountCents)

		// 3) Resumo — aqui o vazamento viraria DINHEIRO no total do mês.
		resumo, err := s.transactions.Summary(ctx, minha.ID, transaction.SummaryFilter{CompetenceMonth: "2026-02"})
		require.NoError(t, err)
		assert.EqualValues(t, 30_00, resumo.ExpenseCents, "a despesa da vizinha não pode entrar no meu mês")
		assert.EqualValues(t, 1, resumo.Count)

		// 4) Saldo por conta — idem, e ainda por cima numa conta que não é minha.
		saldos, err := s.transactions.SumByAccount(ctx, minha.ID)
		require.NoError(t, err)
		assert.EqualValues(t, -30_00, saldos[minhaConta.ID])
		assert.NotContains(t, saldos, contaAlheia.ID, "conta de outra casa não pode aparecer no saldo")

		// 5) Janela de deduplicação — se vazasse, a importação da minha casa
		// marcaria como "duplicada" uma compra que é da vizinha, e a linha
		// legítima seria bloqueada por um dado que eu nem posso ver.
		janela, err := s.transactions.WindowForDedup(ctx, minha.ID, contaAlheia.ID,
			civil.MustNew(2026, 2, 1), civil.MustNew(2026, 2, 28))
		require.NoError(t, err)
		assert.Empty(t, janela)

		// 6) Ordinal máximo por chave: a chave da vizinha não pode empurrar o
		// meu ordinal (e nem revelar que ela existe).
		ord, err := s.transactions.MaxDedupOrdinal(ctx, minha.ID, daAlheia.DedupKey)
		require.NoError(t, err)
		assert.Equal(t, 0, ord)

		// 7) Uso de conta e categoria: se vazasse, eu não conseguiria excluir
		// uma conta minha por causa de lançamento alheio.
		usada, err := s.transactions.ExistsByAccount(ctx, minha.ID, contaAlheia.ID)
		require.NoError(t, err)
		assert.False(t, usada)

		usada, err = s.transactions.ExistsByCategory(ctx, minha.ID, catAlheia.ID)
		require.NoError(t, err)
		assert.False(t, usada)
	})
}

// A condição de cursor é a única do repositório que usa OR, e OR é onde o
// isolamento por casa morre: se o SQL sair como
// "household_id = ? AND occurred_on = ? OR occurred_on < ?", o segundo ramo
// do OR não tem casa nenhuma e passa a devolver a tabela inteira.
//
// Este teste existe para provar o parêntese no SQL gerado, e não para testar
// paginação: ele põe dado REAL na casa vizinha e pagina até o fim.
func TestCursorNaoVazaLancamentoDeOutraCasa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		minhaConta := s.makeAccount(t, ctx, minha.ID, "Carteira")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta da Vizinha")

		// A vizinha tem lançamentos em TODOS os dias da janela, inclusive
		// antes e depois dos meus — qualquer vazamento aparece.
		for dia := 1; dia <= 6; dia++ {
			s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
				OccurredOn: civil.MustNew(2026, 2, dia), AmountCents: 999_00,
			})
		}
		for dia := 2; dia <= 4; dia++ {
			s.makeTransaction(t, ctx, minha.ID, minhaConta.ID, txSpec{
				OccurredOn: civil.MustNew(2026, 2, dia), AmountCents: int64(dia) * 100,
			})
		}

		var vistos []transaction.Transaction
		filtro := transaction.ListFilter{CompetenceMonth: "2026-02", Limit: 2}
		for range 5 { // teto de segurança: a lista tem 3 linhas, 5 páginas bastam
			pagina, err := s.transactions.List(ctx, minha.ID, filtro)
			require.NoError(t, err)
			if len(pagina) == 0 {
				break
			}
			vistos = append(vistos, pagina...)
			ultimo := pagina[len(pagina)-1]
			filtro.Cursor = &transaction.Cursor{OccurredOn: ultimo.OccurredOn, ID: ultimo.ID}
		}

		require.Len(t, vistos, 3, "a paginação não pode devolver mais nem menos que os meus lançamentos")
		for _, tx := range vistos {
			assert.Equal(t, minha.ID, tx.HouseholdID)
			assert.EqualValues(t, minhaConta.ID, tx.AccountID)
		}
		// Ordem decrescente por data, que é o contrato da listagem.
		assert.Equal(t, "2026-02-04", vistos[0].OccurredOn.String())
		assert.Equal(t, "2026-02-02", vistos[2].OccurredOn.String())
	})
}

// O índice único é a última linha de defesa contra lançamento em duplicidade —
// e o serviço só consegue traduzi-lo em "linha bloqueada" na revisão da
// importação se ele voltar como erro TIPADO. Se voltasse como error genérico,
// viraria 500 e o usuário veria "erro inesperado" para uma situação normal.
func TestIndiceUnicoDeDedupRecusaRepetidoComErroReconhecivel(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		minhaConta := s.makeAccount(t, ctx, minha.ID, "Carteira")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta da Vizinha")

		const chave = "3f1c9a7e5b2d4f6a8c0e1d2b3a4c5d6e7f8a9b0c1d2e3f4a5b6c7d8e9f0a1b2c"
		s.makeTransaction(t, ctx, minha.ID, minhaConta.ID, txSpec{DedupKey: chave, DedupOrdinal: 1})

		repetido := transaction.Transaction{
			ID: s.nextID("t"), HouseholdID: minha.ID, Kind: transaction.KindExpense,
			AccountID: minhaConta.ID, AmountCents: 1_00, Description: "Outra coisa",
			DescriptionNorm: "outra coisa", OccurredOn: civil.MustNew(2026, 2, 11),
			CompetenceMonth: "2026-02", Source: transaction.SourceImport,
			DedupKey: chave, DedupOrdinal: 1, CreatedBy: s.nextID("u"),
			CreatedAt: now(), UpdatedAt: now(),
		}
		err := s.transactions.CreateBatch(ctx, minha.ID, []transaction.Transaction{repetido})
		require.ErrorIs(t, err, transaction.ErrDuplicateDedup,
			"violação do índice único precisa ser reconhecível como tal, não erro genérico")

		// O ordinal seguinte é a saída legítima: mesma chave, ordinal 2 entra.
		ord, err := s.transactions.MaxDedupOrdinal(ctx, minha.ID, chave)
		require.NoError(t, err)
		require.Equal(t, 1, ord)
		repetido.DedupOrdinal = ord + 1
		require.NoError(t, s.transactions.CreateBatch(ctx, minha.ID, []transaction.Transaction{repetido}))

		// E a MESMA chave na outra casa não colide: household_id é a primeira
		// coluna do índice, e é o que impede uma casa de bloquear a outra.
		daVizinha := transaction.Transaction{
			ID: s.nextID("t"), HouseholdID: alheia.ID, Kind: transaction.KindExpense,
			AccountID: contaAlheia.ID, AmountCents: 1_00, Description: "Coisa da vizinha",
			DescriptionNorm: "coisa da vizinha", OccurredOn: civil.MustNew(2026, 2, 11),
			CompetenceMonth: "2026-02", Source: transaction.SourceImport,
			DedupKey: chave, DedupOrdinal: 1, CreatedBy: s.nextID("u"),
			CreatedAt: now(), UpdatedAt: now(),
		}
		require.NoError(t, s.transactions.CreateBatch(ctx, alheia.ID, []transaction.Transaction{daVizinha}))
	})
}

// Restaurar um lançamento tem que devolver o lançamento ORIGINAL. Se o
// Restore tocasse em qualquer campo financeiro, a importação que restaura uma
// linha excluída passaria por cima do valor, da data ou da categoria que a
// pessoa já tinha corrigido à mão — e ninguém perceberia, porque a linha
// "voltou" como esperado.
func TestRestoreNaoAlteraNenhumCampoFinanceiro(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Carteira")
		cat := s.makeCategory(t, ctx, minha.ID, "Mercado", "expense", nil)

		criado := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 123_45,
			OccurredOn: civil.MustNew(2026, 1, 28), CompetenceMonth: "2026-02",
			Description: "Padaria da Esquina", CategoryID: &cat.ID,
			DedupOrdinal: 3,
		})
		antes, err := s.transactions.ByID(ctx, minha.ID, criado.ID)
		require.NoError(t, err)

		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, criado.ID, now()))
		_, err = s.transactions.ByID(ctx, minha.ID, criado.ID)
		require.ErrorIs(t, err, transaction.ErrNotFound)

		depoisDe := now().Add(time.Hour)
		require.NoError(t, s.transactions.Restore(ctx, minha.ID, criado.ID, depoisDe))

		depois, err := s.transactions.ByID(ctx, minha.ID, criado.ID)
		require.NoError(t, err)

		assert.Nil(t, depois.DeletedAt)
		assert.Equal(t, antes.Kind, depois.Kind)
		assert.Equal(t, antes.AmountCents, depois.AmountCents)
		assert.Equal(t, antes.AccountID, depois.AccountID)
		assert.Equal(t, antes.CategoryID, depois.CategoryID)
		assert.Equal(t, antes.OccurredOn, depois.OccurredOn)
		assert.Equal(t, antes.CompetenceMonth, depois.CompetenceMonth)
		assert.Equal(t, antes.Description, depois.Description)
		assert.Equal(t, antes.DescriptionNorm, depois.DescriptionNorm)
		assert.Equal(t, antes.DedupKey, depois.DedupKey)
		assert.Equal(t, antes.DedupOrdinal, depois.DedupOrdinal)
		assert.Equal(t, antes.CreatedBy, depois.CreatedBy)
		assert.Equal(t, antes.CreatedAt.UTC(), depois.CreatedAt.UTC())
		// updated_at é o ÚNICO campo que muda junto com deleted_at.
		assert.True(t, depois.UpdatedAt.After(antes.UpdatedAt))

		// Restaurar de novo não encontra nada para restaurar — e isso é
		// resposta estável nos quatro dialetos porque o WHERE exige
		// deleted_at IS NOT NULL (UPDATE sem mudança devolve 0 no MySQL).
		assert.ErrorIs(t, s.transactions.Restore(ctx, minha.ID, criado.ID, now()), transaction.ErrNotFound)

		// E restaurar lançamento de outra casa não acontece.
		_, alheia := s.duasCasas(t, ctx)
		assert.ErrorIs(t, s.transactions.Restore(ctx, alheia.ID, criado.ID, now()), transaction.ErrNotFound)
	})
}

// Transferência move dinheiro entre contas da própria casa: ela aparece no
// extrato, mas não é receita nem despesa (ADR-016). Contá-la no resumo
// dobraria o movimento do mês e faria o "resultado" mentir.
func TestSummarySeparaTransferenciaESemCategoria(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		corrente := s.makeAccount(t, ctx, minha.ID, "Conta Corrente")
		poupanca := s.makeAccount(t, ctx, minha.ID, "Poupança")
		cat := s.makeCategory(t, ctx, minha.ID, "Salário", "income", nil)

		grupo := s.nextID("tg")
		s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
			Kind: transaction.KindIncome, AmountCents: 5_000_00, CategoryID: &cat.ID,
		})
		s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
			Kind: transaction.KindExpense, AmountCents: 1_200_00,
		})
		s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{
			Kind: transaction.KindTransferOut, AmountCents: 800_00, TransferGroupID: &grupo,
		})
		s.makeTransaction(t, ctx, minha.ID, poupanca.ID, txSpec{
			Kind: transaction.KindTransferIn, AmountCents: 800_00, TransferGroupID: &grupo,
		})

		resumo, err := s.transactions.Summary(ctx, minha.ID, transaction.SummaryFilter{CompetenceMonth: "2026-02"})
		require.NoError(t, err)

		assert.EqualValues(t, 5_000_00, resumo.IncomeCents)
		assert.EqualValues(t, 1_200_00, resumo.ExpenseCents)
		assert.EqualValues(t, 3_800_00, resumo.NetCents, "transferência não pode entrar no resultado")
		assert.EqualValues(t, 4, resumo.Count, "a contagem é a da lista, e a lista mostra transferência")
		assert.EqualValues(t, 1, resumo.Uncategorized,
			"só a despesa sem categoria conta; transferência não tem categoria por desenho")

		// Filtrar por conta aperta a mesma janela.
		porConta, err := s.transactions.Summary(ctx, minha.ID, transaction.SummaryFilter{
			CompetenceMonth: "2026-02", AccountID: poupanca.ID,
		})
		require.NoError(t, err)
		assert.EqualValues(t, 0, porConta.IncomeCents)
		assert.EqualValues(t, 1, porConta.Count)
	})
}

// O saldo é derivado (ADR-017), e derivar significa aplicar o sinal a partir
// do kind — no banco, amount_cents é sempre positivo. Um erro aqui aparece
// como saldo errado, que é o pior tipo de bug de um app de finanças.
func TestSumByAccountAplicaSinalPorKindEIgnoraExcluido(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		corrente := s.makeAccount(t, ctx, minha.ID, "Conta Corrente")
		poupanca := s.makeAccount(t, ctx, minha.ID, "Poupança")

		s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{Kind: transaction.KindIncome, AmountCents: 1_000_00})
		s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{Kind: transaction.KindExpense, AmountCents: 250_00})
		s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{Kind: transaction.KindTransferOut, AmountCents: 300_00})
		s.makeTransaction(t, ctx, minha.ID, poupanca.ID, txSpec{Kind: transaction.KindTransferIn, AmountCents: 300_00})
		excluido := s.makeTransaction(t, ctx, minha.ID, corrente.ID, txSpec{Kind: transaction.KindExpense, AmountCents: 9_999_00})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluido.ID, now()))

		saldos, err := s.transactions.SumByAccount(ctx, minha.ID)
		require.NoError(t, err)

		assert.EqualValues(t, 450_00, saldos[corrente.ID], "1000 - 250 - 300")
		assert.EqualValues(t, 300_00, saldos[poupanca.ID])
		assert.Len(t, saldos, 2, "conta sem lançamento não entra no mapa")
	})
}

// A janela de deduplicação é o que impede a mesma compra de entrar duas vezes.
// Ela precisa de duas coisas que não são óbvias: a folga de três dias (banco e
// cartão lançam a mesma compra com um ou dois dias de diferença) e as linhas
// EXCLUÍDAS (que continuam ocupando a chave única do banco).
func TestWindowForDedupAbreFolgaDeTresDiasEEnxergaExcluido(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Cartão")
		outra := s.makeAccount(t, ctx, minha.ID, "Carteira")

		dentroAntes := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{OccurredOn: civil.MustNew(2026, 2, 7)})   // min - 3
		dentroDepois := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{OccurredOn: civil.MustNew(2026, 2, 23)}) // max + 3
		foraAntes := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{OccurredOn: civil.MustNew(2026, 2, 6)})
		foraDepois := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{OccurredOn: civil.MustNew(2026, 2, 24)})
		deOutraConta := s.makeTransaction(t, ctx, minha.ID, outra.ID, txSpec{OccurredOn: civil.MustNew(2026, 2, 15)})

		apagado := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{OccurredOn: civil.MustNew(2026, 2, 15)})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, apagado.ID, now()))

		janela, err := s.transactions.WindowForDedup(ctx, minha.ID, conta.ID,
			civil.MustNew(2026, 2, 10), civil.MustNew(2026, 2, 20))
		require.NoError(t, err)

		vistos := map[string]bool{}
		for _, row := range janela {
			vistos[row.ID] = true
		}
		assert.True(t, vistos[dentroAntes.ID], "3 dias antes do início entra")
		assert.True(t, vistos[dentroDepois.ID], "3 dias depois do fim entra")
		assert.True(t, vistos[apagado.ID], "linha excluída continua ocupando a chave única e precisa aparecer")
		assert.False(t, vistos[foraAntes.ID], "4 dias antes já é fora da janela")
		assert.False(t, vistos[foraDepois.ID])
		assert.False(t, vistos[deOutraConta.ID], "a janela é por conta")

		for _, row := range janela {
			if row.ID == apagado.ID {
				assert.NotNil(t, row.DeletedAt, "a projeção precisa dizer que a linha está excluída")
			}
			assert.NotEmpty(t, row.DedupKey)
			assert.NotZero(t, row.DedupOrdinal)
		}

		// Intervalo inválido é ERRO, e não janela vazia: janela vazia em
		// silêncio faria a importação concluir que nada é duplicado.
		_, err = s.transactions.WindowForDedup(ctx, minha.ID, conta.ID, civil.Date{}, civil.MustNew(2026, 2, 20))
		assert.Error(t, err)
		_, err = s.transactions.WindowForDedup(ctx, minha.ID, "", civil.MustNew(2026, 2, 10), civil.MustNew(2026, 2, 20))
		assert.Error(t, err)
	})
}

// Guardas do lote: as duas recusas abaixo são defesa em profundidade, e
// nenhuma delas depende do banco. Uma linha de outra casa gravada por engano
// seria dinheiro na casa errada; competência vazia passaria no NOT NULL (string
// vazia é um valor) e sumiria um mês inteiro do relatório sem erro nenhum.
func TestCreateBatchRecusaLoteInvalidoAntesDeTocarNoBanco(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Carteira")

		base := transaction.Transaction{
			ID: s.nextID("t"), HouseholdID: minha.ID, Kind: transaction.KindExpense,
			AccountID: conta.ID, AmountCents: 10_00, Description: "Feira",
			DescriptionNorm: "feira", OccurredOn: civil.MustNew(2026, 2, 10),
			CompetenceMonth: "2026-02", Source: transaction.SourceManual,
			DedupKey: s.nextID("dk"), DedupOrdinal: 1, CreatedBy: s.nextID("u"),
			CreatedAt: now(), UpdatedAt: now(),
		}

		daOutraCasa := base
		daOutraCasa.HouseholdID = alheia.ID
		assert.ErrorIs(t, s.transactions.CreateBatch(ctx, minha.ID, []transaction.Transaction{base, daOutraCasa}),
			transaction.ErrHouseholdMismatch)

		semCompetencia := base
		semCompetencia.CompetenceMonth = ""
		assert.ErrorIs(t, s.transactions.CreateBatch(ctx, minha.ID, []transaction.Transaction{semCompetencia}),
			transaction.ErrIncomplete)

		semChave := base
		semChave.DedupKey = ""
		assert.ErrorIs(t, s.transactions.CreateBatch(ctx, minha.ID, []transaction.Transaction{semChave}),
			transaction.ErrIncomplete)

		ordinalZero := base
		ordinalZero.DedupOrdinal = 0
		assert.ErrorIs(t, s.transactions.CreateBatch(ctx, minha.ID, []transaction.Transaction{ordinalZero}),
			transaction.ErrIncomplete)

		// Nenhuma das recusas pode ter gravado nada: o lote é tudo ou nada.
		lista, err := s.transactions.List(ctx, minha.ID, transaction.ListFilter{})
		require.NoError(t, err)
		assert.Empty(t, lista)
	})
}

// O lote de verdade: 70 linhas passam por três INSERTs (createBatchSize = 30),
// e o teto de parâmetros por comando do MSSQL/SQLite é justamente o motivo de
// o lote ser fatiado. Se alguém aumentar o tamanho do lote sem pensar nisso,
// este teste continua verde no SQLite — mas o comentário da constante explica
// por que não se deve.
func TestCreateBatchGravaLoteGrandeEmFatias(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta Corrente")

		lote := make([]transaction.Transaction, 0, 70)
		for i := range 70 {
			lote = append(lote, transaction.Transaction{
				ID: s.nextID("t"), HouseholdID: minha.ID, Kind: transaction.KindExpense,
				AccountID: conta.ID, AmountCents: int64(i+1) * 100, Description: "Importado",
				DescriptionNorm: "importado", OccurredOn: civil.MustNew(2026, 2, (i%28)+1),
				CompetenceMonth: "2026-02", Source: transaction.SourceImport,
				DedupKey: s.nextID("dk"), DedupOrdinal: 1, CreatedBy: s.nextID("u"),
				CreatedAt: now(), UpdatedAt: now(),
			})
		}
		require.NoError(t, s.transactions.CreateBatch(ctx, minha.ID, lote))

		resumo, err := s.transactions.Summary(ctx, minha.ID, transaction.SummaryFilter{CompetenceMonth: "2026-02"})
		require.NoError(t, err)
		assert.EqualValues(t, 70, resumo.Count)
		assert.EqualValues(t, 248_500, resumo.ExpenseCents, "soma de 100..7000 em centavos")
	})
}

// year_month é PROJEÇÃO de occurred_on e competence_month é DADO. A diferença
// só aparece com compra de cartão: comprada em janeiro, competência de
// fevereiro. Se as duas colunas fossem a mesma coisa, o extrato do mês de
// caixa e o relatório de competência dariam o mesmo número — e um dos dois
// estaria errado.
func TestCaixaECompetenciaSaoColunasDiferentes(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		cartao := s.makeAccount(t, ctx, minha.ID, "Cartão")

		s.makeTransaction(t, ctx, minha.ID, cartao.ID, txSpec{
			OccurredOn: civil.MustNew(2026, 1, 28), CompetenceMonth: "2026-02", AmountCents: 90_00,
		})

		emFevereiro, err := s.transactions.Summary(ctx, minha.ID, transaction.SummaryFilter{CompetenceMonth: "2026-02"})
		require.NoError(t, err)
		assert.EqualValues(t, 90_00, emFevereiro.ExpenseCents, "a compra de cartão aparece no mês da fatura")

		emJaneiro, err := s.transactions.Summary(ctx, minha.ID, transaction.SummaryFilter{CompetenceMonth: "2026-01"})
		require.NoError(t, err)
		assert.EqualValues(t, 0, emJaneiro.ExpenseCents)

		lista, err := s.transactions.List(ctx, minha.ID, transaction.ListFilter{CompetenceMonth: "2026-02"})
		require.NoError(t, err)
		require.Len(t, lista, 1)
		assert.Equal(t, "2026-01-28", lista[0].OccurredOn.String(), "a data de caixa continua sendo a da compra")
	})
}

// "Esta conta já foi usada?" é pergunta sobre o PASSADO, e o passado inclui o
// que foi excluído logicamente. Se a resposta olhasse só para os lançamentos
// ativos, quem apagasse os lançamentos poderia excluir a conta — e o histórico
// deles ficaria pendurado numa conta que não existe mais.
func TestExistsContaEcategoriaEnxergamOPassadoInclusiveOExcluido(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Carteira")
		vazia := s.makeAccount(t, ctx, minha.ID, "Conta Nova")
		cat := s.makeCategory(t, ctx, minha.ID, "Mercado", "expense", nil)

		usada, err := s.transactions.ExistsByAccount(ctx, minha.ID, vazia.ID)
		require.NoError(t, err)
		assert.False(t, usada, "conta sem nenhum lançamento pode ser excluída")

		tx := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{CategoryID: &cat.ID})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, tx.ID, now()))

		usada, err = s.transactions.ExistsByAccount(ctx, minha.ID, conta.ID)
		require.NoError(t, err)
		assert.True(t, usada, "lançamento excluído ainda é uso da conta")

		usada, err = s.transactions.ExistsByCategory(ctx, minha.ID, cat.ID)
		require.NoError(t, err)
		assert.True(t, usada)
	})
}

// MaxDedupOrdinal precisa contar as linhas excluídas: elas continuam ocupando
// a chave única, e reaproveitar o ordinal delas faria o INSERT ser recusado.
func TestMaxDedupOrdinalContaLinhaExcluida(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Carteira")

		const chave = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"
		primeiro := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{DedupKey: chave, DedupOrdinal: 1})
		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{DedupKey: chave, DedupOrdinal: 2})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, primeiro.ID, now()))

		ord, err := s.transactions.MaxDedupOrdinal(ctx, minha.ID, chave)
		require.NoError(t, err)
		assert.Equal(t, 2, ord)

		semUso, err := s.transactions.MaxDedupOrdinal(ctx, minha.ID, "chave-que-ninguem-usou")
		require.NoError(t, err)
		assert.Equal(t, 0, semUso, "chave inédita começa do zero, para o primeiro ordinal ser 1")
	})
}

// Escrita em lançamento de outra casa não acontece, nem com household_id
// forjado na entidade: ele entra no WHERE, nunca no SET.
func TestEscritaEmLancamentoDeOutraCasaNaoAcontece(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta da Vizinha")
		daAlheia := s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{AmountCents: 777_00})

		assert.ErrorIs(t, s.transactions.SoftDelete(ctx, minha.ID, daAlheia.ID, now()), transaction.ErrNotFound)
		assert.ErrorIs(t, s.transactions.Restore(ctx, minha.ID, daAlheia.ID, now()), transaction.ErrNotFound)

		intacto, err := s.transactions.ByID(ctx, alheia.ID, daAlheia.ID)
		require.NoError(t, err)
		assert.Nil(t, intacto.DeletedAt)
		assert.EqualValues(t, 777_00, intacto.AmountCents)
	})
}
