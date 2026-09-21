package gormstore_test

import (
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// O teste mais importante deste arquivo. BOLA é o risco nº 1 do projeto
// (docs/SEGURANCA.md §2), e o jeito de provar que a defesa existe é ter dado
// REAL na outra casa e mostrar que ele não aparece em NENHUMA consulta — nem
// na busca por id, nem na listagem, nem na contagem, nem na verificação de
// nome duplicado. A contagem e a verificação de nome importam tanto quanto as
// outras: se vazassem, o limite de contas e a unicidade de nome de uma casa
// seriam afetados pelo que a vizinha faz.
func TestContaDeOutraCasaNaoApareceEmNenhumaConsulta(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		daAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta Corrente")
		s.makeAccount(t, ctx, minha.ID, "Carteira")

		// 1) Busca por id: existe de verdade, mas não para mim.
		_, err := s.accounts.ByID(ctx, minha.ID, daAlheia.ID)
		require.ErrorIs(t, err, account.ErrNotFound)

		// 2) Listagem.
		lista, err := s.accounts.List(ctx, minha.ID, true)
		require.NoError(t, err)
		require.Len(t, lista, 1)
		assert.Equal(t, "Carteira", lista[0].Name)

		// 3) Contagem — se vazasse, o teto de 50 contas da minha casa seria
		// consumido pelas contas da vizinha.
		total, err := s.accounts.CountAll(ctx, minha.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)

		// 4) Unicidade de nome — se vazasse, eu não poderia criar uma conta
		// com um nome que a outra casa usa, e isso revelaria que ela usa.
		taken, err := s.accounts.NameTaken(ctx, minha.ID, daAlheia.NameNorm, "")
		require.NoError(t, err)
		assert.False(t, taken, "o nome da outra casa não pode bloquear a minha")
	})
}

func TestEscritaEmContaDeOutraCasaNaoAcontece(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		daAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta Corrente")

		// Forjar o household_id da entidade não ajuda: ele entra no WHERE,
		// então o UPDATE não encontra a linha. É o que impede um bug de
		// serviço de virar escrita em dado alheio.
		forjada := *daAlheia
		forjada.HouseholdID = minha.ID
		forjada.Name = "Sequestrada"
		require.ErrorIs(t, s.accounts.Update(ctx, &forjada), account.ErrNotFound)

		require.ErrorIs(t, s.accounts.SoftDelete(ctx, minha.ID, daAlheia.ID, now()), account.ErrNotFound)

		// A conta alheia continua intacta.
		intacta, err := s.accounts.ByID(ctx, alheia.ID, daAlheia.ID)
		require.NoError(t, err)
		assert.Equal(t, "Conta Corrente", intacta.Name)
		assert.Nil(t, intacta.DeletedAt)
	})
}

func TestExcluidaLogicamenteSomeDeTudo(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "Poupança")

		require.NoError(t, s.accounts.SoftDelete(ctx, minha.ID, a.ID, now()))

		_, err := s.accounts.ByID(ctx, minha.ID, a.ID)
		assert.ErrorIs(t, err, account.ErrNotFound)

		lista, err := s.accounts.List(ctx, minha.ID, true)
		require.NoError(t, err)
		assert.Empty(t, lista, "includeArchived mostra ARQUIVADA, não EXCLUÍDA")

		total, err := s.accounts.CountAll(ctx, minha.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 0, total, "excluída não ocupa vaga no limite")

		// O nome volta a ficar livre.
		taken, err := s.accounts.NameTaken(ctx, minha.ID, a.NameNorm, "")
		require.NoError(t, err)
		assert.False(t, taken)

		// Excluir de novo não encontra nada.
		assert.ErrorIs(t, s.accounts.SoftDelete(ctx, minha.ID, a.ID, now()), account.ErrNotFound)
	})
}

func TestArquivadaContinuaExistindoMasSaiDaListaPadrao(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "Conta Antiga")

		quando := now()
		a.ArchivedAt = &quando
		a.UpdatedAt = quando
		require.NoError(t, s.accounts.Update(ctx, a))

		semArquivadas, err := s.accounts.List(ctx, minha.ID, false)
		require.NoError(t, err)
		assert.Empty(t, semArquivadas)

		comArquivadas, err := s.accounts.List(ctx, minha.ID, true)
		require.NoError(t, err)
		require.Len(t, comArquivadas, 1)
		assert.NotNil(t, comArquivadas[0].ArchivedAt)

		// Arquivada OCUPA vaga: ela ainda existe e pode voltar.
		total, err := s.accounts.CountAll(ctx, minha.ID)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total)

		// Mas libera o nome, porque a unicidade vale entre as ATIVAS.
		taken, err := s.accounts.NameTaken(ctx, minha.ID, a.NameNorm, "")
		require.NoError(t, err)
		assert.False(t, taken)
	})
}

// Desarquivar grava NULL em archived_at. Este teste existe porque o caminho
// "gravar NULL" é o que um Updates com struct silenciosamente NÃO faz — o GORM
// ignora campos zerados. É o tipo de bug que passa despercebido até alguém
// tentar desarquivar em produção.
func TestDesarquivarGravaNuloDeVerdade(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "Conta Sazonal")

		quando := now()
		a.ArchivedAt = &quando
		require.NoError(t, s.accounts.Update(ctx, a))

		a.ArchivedAt = nil
		require.NoError(t, s.accounts.Update(ctx, a))

		recarregada, err := s.accounts.ByID(ctx, minha.ID, a.ID)
		require.NoError(t, err)
		assert.Nil(t, recarregada.ArchivedAt, "archived_at precisa voltar a ser NULL")
	})
}

func TestNameTakenIgnoraAPropriaConta(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		a := s.makeAccount(t, ctx, minha.ID, "Nubank")

		// Sem a exceção, renomear a conta para o mesmo nome (ou só trocar a
		// caixa) colidiria com ela própria.
		taken, err := s.accounts.NameTaken(ctx, minha.ID, a.NameNorm, a.ID)
		require.NoError(t, err)
		assert.False(t, taken)

		taken, err = s.accounts.NameTaken(ctx, minha.ID, a.NameNorm, "")
		require.NoError(t, err)
		assert.True(t, taken)
	})
}

// A data civil é gravada como texto (D3 da spec 0003). Este teste prova que
// ela sobrevive à ida e volta sem deslocamento de fuso — que é o bug que o
// tipo civil.Date existe para impedir.
func TestDataCivilSobreviveAoBancoSemDeslocarDia(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)

		// 1º de janeiro é o caso que mais dói: qualquer deslocamento negativo
		// muda o ANO, não só o dia.
		data := civil.MustNew(2026, 1, 1)
		nome, norm, err := account.NormalizeName("Conta com data")
		require.NoError(t, err)
		a := &account.Account{
			ID: s.nextID("a"), HouseholdID: minha.ID, Name: nome, NameNorm: norm,
			Kind: account.KindCash, OpeningBalanceCents: 0, OpeningDate: data,
			CreatedAt: now(), UpdatedAt: now(),
		}
		require.NoError(t, s.accounts.Create(ctx, a))

		lida, err := s.accounts.ByID(ctx, minha.ID, a.ID)
		require.NoError(t, err)
		assert.Equal(t, "2026-01-01", lida.OpeningDate.String())
		assert.Equal(t, data, lida.OpeningDate)
	})
}

// Saldo negativo tem que sobreviver: é como o cartão de crédito representa
// dívida no v1 (ADR-019c).
func TestSaldoNegativoSobrevive(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)

		nome, norm, err := account.NormalizeName("Cartão")
		require.NoError(t, err)
		a := &account.Account{
			ID: s.nextID("a"), HouseholdID: minha.ID, Name: nome, NameNorm: norm,
			Kind: account.KindCreditCard, OpeningBalanceCents: -account.MaxAmountCents,
			OpeningDate: civil.MustNew(2026, 3, 15), CreatedAt: now(), UpdatedAt: now(),
		}
		require.NoError(t, s.accounts.Create(ctx, a))

		lida, err := s.accounts.ByID(ctx, minha.ID, a.ID)
		require.NoError(t, err)
		assert.EqualValues(t, -account.MaxAmountCents, lida.OpeningBalanceCents)
	})
}

// A ordenação é por nome NORMALIZADO, que é o que dá ordem alfabética
// insensível a acento e caixa nos quatro dialetos — sem depender de collation.
func TestListaVemOrdenadaIgnorandoAcentoECaixa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)

		for _, nome := range []string{"Zebra", "ábaco", "Carteira"} {
			s.makeAccount(t, ctx, minha.ID, nome)
		}

		lista, err := s.accounts.List(ctx, minha.ID, false)
		require.NoError(t, err)
		nomes := make([]string, 0, len(lista))
		for _, a := range lista {
			nomes = append(nomes, a.Name)
		}
		// "ábaco" vem primeiro porque normaliza para "abaco"; com LOWER() puro
		// e collation binária ele iria para o fim.
		assert.Equal(t, []string{"ábaco", "Carteira", "Zebra"}, nomes)
	})
}

// Colunas do schema v3 na conta: a instituição nunca fica vazia (o default da
// coluna cobre a conta antiga) e os dias da fatura são anuláveis — "não se
// aplica" não é "dia zero". As três são graváveis pelo Update, senão quem as
// implementar no serviço vai setar o campo e não ver nada acontecer.
func TestContaGravaEAtualizaColunasDeImportacaoEFatura(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)

		criada := s.makeAccount(t, ctx, minha.ID, "Cartão")
		lida, err := s.accounts.ByID(ctx, minha.ID, criada.ID)
		require.NoError(t, err)
		assert.Equal(t, account.InstitutionOther, lida.Institution,
			"conta criada sem instituição não pode ficar com a coluna NOT NULL vazia")
		assert.Nil(t, lida.StatementClosingDay)
		assert.Nil(t, lida.StatementDueDay)

		fechamento, vencimento := 2, 10
		lida.Institution = "nubank"
		lida.StatementClosingDay = &fechamento
		lida.StatementDueDay = &vencimento
		lida.UpdatedAt = now().Add(time.Minute)
		require.NoError(t, s.accounts.Update(ctx, lida))

		depois, err := s.accounts.ByID(ctx, minha.ID, criada.ID)
		require.NoError(t, err)
		assert.Equal(t, "nubank", depois.Institution)
		require.NotNil(t, depois.StatementClosingDay)
		assert.Equal(t, 2, *depois.StatementClosingDay)
		require.NotNil(t, depois.StatementDueDay)
		assert.Equal(t, 10, *depois.StatementDueDay)
	})
}
