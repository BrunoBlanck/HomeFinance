package gormstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/category"
)

// updateLento é um UPDATE que o SQLite leva cerca de um segundo para avaliar,
// para o contexto conseguir morrer COM O COMANDO EM VOO — que é a única
// condição em que o driver puro-Go devolve "interrupted (9)" em vez do erro do
// contexto.
//
// A tabela precisa TER LINHAS. Sem elas o SQLite nem chega a avaliar a
// condição: o UPDATE volta em 1 ms, sem erro, e um teste escrito sobre a tabela
// vazia passaria sem nunca ter exercitado a interrupção. Foi a primeira medição
// desta investigação, e é por isso que este aviso está escrito aqui.
//
// A condição é sempre FALSA: o que se mede é o TEMPO dentro do passo do SQLite,
// e nenhuma linha pode acabar alterada por um teste sobre erro.
const condicaoLenta = `(WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < 50000000) SELECT count(*) FROM c) < 0`

// updateLento emite o UPDATE pelo MESMO processador do GORM que
// SetCategoryWhereNull e SetCategoryWhereCurrentIn usam (`Updates`), e não por
// `Exec`: `Raw`/`Exec` são proibidos no código de produção, e um teste que
// passasse por eles estaria provando um caminho que a aplicação não percorre.
//
// A condição é uma constante do teste — nenhuma entrada de usuário entra nela.
func updateLento(t *testing.T, s *store, ctx context.Context) error {
	t.Helper()
	return s.db.Gorm().WithContext(ctx).
		Table("transactions").
		Where(condicaoLenta).
		Updates(map[string]any{"updated_at": now()}).Error
}

// comLinhas devolve uma casa com lançamentos, para o updateLento ter o que
// varrer.
func comLinhas(t *testing.T, s *store) {
	t.Helper()
	ctx := t.Context()
	casa, _ := s.duasCasas(t, ctx)
	conta := s.makeAccount(t, ctx, casa.ID, "Conta")
	for range 3 {
		s.makeTransaction(t, ctx, casa.ID, conta.ID, txSpec{})
	}
}

// A regressão que o embrulho de storage/ctxerr.go existe para impedir.
//
// `database/sql` devolve `ctx.Err()` nas LEITURAS (o `Rows` é fechado pelo
// contexto), mas a ESCRITA não tem `Rows`: o driver interrompe o `sqlite3_step`
// e devolve SQLITE_INTERRUPT. Sem o embrulho, a borda deixa de reconhecer prazo
// e cancelamento — e um limite de trabalho previsto vira 500 INTERNAL_ERROR,
// enquanto cada aba fechada no meio de uma escrita vira uma linha de ERROR.
//
// O teste afirma o CONTRATO (`errors.Is` funciona), não a mensagem do driver:
// a mensagem muda com a versão do driver, o contrato não.
func TestPrazoEmEscritaInterrompidaContinuaReconhecivelPorErrorsIs(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		if s.backendIsPG {
			t.Skip("o caso é do driver puro-Go de SQLite; o Postgres devolve o erro do contexto")
		}
		comLinhas(t, s)

		ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
		defer cancel()

		err := updateLento(t, s, ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded,
			"sem isto o handler não distingue prazo de falha do servidor")
	})
}

// A mesma prova do lado do cancelamento: a pessoa fecha a aba no meio da
// escrita, e o erro que sobe tem de continuar dizendo que foi cancelamento.
func TestCancelamentoEmEscritaInterrompidaContinuaReconhecivel(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		if s.backendIsPG {
			t.Skip("o caso é do driver puro-Go de SQLite")
		}
		comLinhas(t, s)

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() {
			time.Sleep(80 * time.Millisecond)
			cancel()
		}()

		err := updateLento(t, s, ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})
}

// O embrulho SOMA informação, nunca troca: um erro de domínio que aconteça com
// o contexto já morto continua reconhecível pelo que ele é.
//
// Se esta propriedade se perder, todo handler que testa o erro de contexto
// antes do erro de domínio passa a responder a coisa errada.
func TestEmbrulhoDeContextoPreservaOErroOriginal(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		proprio := errors.New("erro de domínio qualquer")

		err := s.uow.Do(ctx, func(context.Context) error {
			cancel()
			return proprio
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, proprio, "o erro original tem de continuar na cadeia")
		assert.ErrorIs(t, err, context.Canceled, "e o motivo do contexto tem de estar junto")
	})
}

// Contexto morto com trabalho BEM-SUCEDIDO não inventa falha: quem decide o
// que fazer com o resultado é quem chamou.
//
// A prova é feita no ramo REENTRANTE (transação já aberta no contexto), que é
// onde o valor de retorno de fn chega intacto ao embrulho. No ramo de fora não
// daria para isolar a propriedade: com o contexto cancelado, o próprio COMMIT
// falha, e o erro seria dele, não do embrulho.
func TestContextoMortoComSucessoNaoViraErro(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		var interno error
		abortar := errors.New("aborta de propósito: o commit não é o assunto deste teste")
		_ = s.uow.Do(ctx, func(externo context.Context) error {
			interno = s.uow.Do(externo, func(context.Context) error {
				cancel()
				return nil
			})
			return abortar
		})

		assert.NoError(t, interno)
	})
}

// --- reconferência de categorias (TOCTOU do detect) ------------------------

// LiveStates responde às três perguntas da reconferência, e a EXCLUÍDA é a que
// motivou o método: entre o plano do `detect` e o UPDATE cabe um
// DELETE /categories/{id}, e a checagem `inUse` da exclusão não encontra nada
// porque nada foi gravado ainda.
func TestLiveStatesDeixaDeForaAExcluida(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casa, _ := s.duasCasas(t, ctx)

		viva := s.makeCategory(t, ctx, casa.ID, "Renda fixa", category.KindInvestment, nil)
		morta := s.makeCategory(t, ctx, casa.ID, "Tesouro", category.KindInvestment, nil)
		require.NoError(t, s.categories.SoftDelete(ctx, casa.ID, morta.ID, now()))

		out, err := s.categories.LiveStates(ctx, casa.ID, []string{viva.ID, morta.ID})
		require.NoError(t, err)
		require.Contains(t, out, viva.ID)
		assert.NotContains(t, out, morta.ID)
		assert.Equal(t, category.KindInvestment, out[viva.ID].Kind)
	})
}

// A NATUREZA relida é o campo que faltava, e a falta era explorável: trocar de
// natureza dentro do mesmo lado do dinheiro é aceito mesmo com a categoria em
// uso (ADR-029c), então `investment` vira `expense` sem nenhuma recusa.
func TestLiveStatesDevolveANaturezaATUAL(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casa, _ := s.duasCasas(t, ctx)

		c := s.makeCategory(t, ctx, casa.ID, "Investimentos", category.KindInvestment, nil)

		c.Kind = category.KindExpense
		c.UpdatedAt = now()
		require.NoError(t, s.categories.Update(ctx, c))

		out, err := s.categories.LiveStates(ctx, casa.ID, []string{c.ID})
		require.NoError(t, err)
		assert.Equal(t, category.KindExpense, out[c.ID].Kind,
			"a releitura tem de enxergar a troca, senão o UPDATE grava categoria comum")
	})
}

// ARQUIVADA continua no MAPA, marcada como tal: o repositório informa, e quem
// decide é o chamador — marcação existente sobrevive ao arquivamento
// (a allowlist da troca precisa alcançá-la), atribuição nova não.
func TestLiveStatesMantemAArquivadaEAMarca(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casa, _ := s.duasCasas(t, ctx)

		c := s.makeCategory(t, ctx, casa.ID, "Renda fixa", category.KindInvestment, nil)
		quando := now()
		c.ArchivedAt = &quando
		c.UpdatedAt = quando
		require.NoError(t, s.categories.Update(ctx, c))

		out, err := s.categories.LiveStates(ctx, casa.ID, []string{c.ID})
		require.NoError(t, err)
		require.Contains(t, out, c.ID)
		assert.True(t, out[c.ID].Archived, "o repositório precisa INFORMAR o arquivamento")
		assert.False(t, out[c.ID].HasActiveChild)
	})
}

// Grupo com subcategoria ATIVA não recebe lançamento (spec 0005 §12/§13); com a
// filha arquivada, ele volta a ser destino legítimo. As duas metades na mesma
// consulta.
func TestLiveStatesMarcaGrupoComFilhaAtiva(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casa, _ := s.duasCasas(t, ctx)

		grupo := s.makeCategory(t, ctx, casa.ID, "Investimentos", category.KindInvestment, nil)
		filha := s.makeCategory(t, ctx, casa.ID, "CDB", category.KindInvestment, &grupo.ID)

		out, err := s.categories.LiveStates(ctx, casa.ID, []string{grupo.ID, filha.ID})
		require.NoError(t, err)
		assert.True(t, out[grupo.ID].HasActiveChild, "o grupo não recebe lançamento")
		assert.False(t, out[filha.ID].HasActiveChild, "a folha continua")

		quando := now()
		filha.ArchivedAt = &quando
		filha.UpdatedAt = quando
		require.NoError(t, s.categories.Update(ctx, filha))

		out, err = s.categories.LiveStates(ctx, casa.ID, []string{grupo.ID})
		require.NoError(t, err)
		assert.False(t, out[grupo.ID].HasActiveChild, "filha arquivada não conta como subcategoria ativa")
	})
}

// Isolamento (S1/BOLA): categoria da outra casa simplesmente não existe para
// esta consulta — nem no mapa, nem como filha capaz de marcar um grupo desta
// casa.
func TestLiveStatesNaoEnxergaOutraCasa(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		daVizinha := s.makeCategory(t, ctx, alheia.ID, "Renda fixa", category.KindInvestment, nil)
		minhaCat := s.makeCategory(t, ctx, minha.ID, "Renda fixa", category.KindInvestment, nil)

		out, err := s.categories.LiveStates(ctx, minha.ID, []string{minhaCat.ID, daVizinha.ID})
		require.NoError(t, err)
		assert.Contains(t, out, minhaCat.ID)
		assert.NotContains(t, out, daVizinha.ID)
	})
}

// Lista vazia não vira consulta, e casa vazia é recusada antes do banco.
func TestLiveStatesGuardasDeEntrada(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casa, _ := s.duasCasas(t, ctx)

		out, err := s.categories.LiveStates(ctx, casa.ID, nil)
		require.NoError(t, err)
		assert.Empty(t, out)

		out, err = s.categories.LiveStates(ctx, casa.ID, []string{"", ""})
		require.NoError(t, err)
		assert.Empty(t, out)

		_, err = s.categories.LiveStates(ctx, "", []string{"c-1"})
		assert.Error(t, err, "casa vazia nunca chega ao banco")
	})
}

// Teto: o conjunto NÃO é fatiado. Fatiar significaria aprovar uma parte dos ids
// sem ter olhado o resto (ADR-027f, ADR-029 j.2).
func TestLiveStatesRecusaAcimaDoTeto(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		casa, _ := s.duasCasas(t, ctx)

		ids := make([]string, 0, category.MaxPerHousehold+1)
		for range category.MaxPerHousehold + 1 {
			ids = append(ids, s.nextID("c"))
		}

		_, err := s.categories.LiveStates(ctx, casa.ID, ids)
		assert.ErrorIs(t, err, category.ErrTooManyToCheck)
	})
}
