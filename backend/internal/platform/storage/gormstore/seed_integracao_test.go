package gormstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/household"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// tempoMaximoDaSemente é o orçamento da semente numa casa nova, no SQLite em
// arquivo.
//
// Ele importa porque a semente roda DENTRO da transação que cria a casa, no
// caminho da verificação do e-mail: o que ela gasta, a pessoa espera olhando a
// tela de cadastro. O orçamento de comandos é constante (uma contagem, uma
// listagem, uma consulta de donas, 56 INSERT de categoria e 41 pares
// DELETE+INSERT em lote de palavras), então passar daqui significa que alguém
// trocou um comando por casa por um comando por item.
func tempoMaximoDaSemente() time.Duration {
	// Sob o detector de corrida a instrumentação multiplica o tempo de parede
	// por uma ordem de grandeza, e o critério é sobre o binário REAL — mesmo
	// tratamento do TestDesempenhoCriterio11 em internal/textmatch. O tempo
	// medido vai para o log nos dois casos, que é o que se reporta.
	if raceEnabled {
		return 10 * time.Second
	}
	return 500 * time.Millisecond
}

// A semente de casa nova ponta a ponta, contra banco de verdade: household
// real, category.Service real como seeder, e o classificador montado em cima
// do que ficou gravado.
//
// Os testes de `internal/category` provam a tabela e a regra com dublês; este
// prova as três coisas que só o banco responde: que as 445 palavras cabem no
// índice único (household_id, keyword_norm) sem colidir entre si, que
// `classify.Load` consegue montar os matchers com o que foi gravado, e que o
// custo disso cabe no caminho do cadastro.
func TestSementeDeCasaNovaNoBanco(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		dono := s.makeUser(t, ctx, true)

		categorySvc := category.NewService(s.categories, s.uow)
		householdSvc := household.NewService(s.households, s.memberships,
			household.WithSeeders(categorySvc))

		var casa household.Summary
		inicio := time.Now()
		require.NoError(t, s.uow.Do(ctx, func(ctx context.Context) error {
			var err error
			casa, err = householdSvc.EnsureDefault(ctx, dono.ID, dono.Name)
			return err
		}))
		duracao := time.Since(inicio)
		require.NotEmpty(t, casa.ID)

		// --- a árvore ---------------------------------------------------------
		total, err := s.categories.CountAll(ctx, casa.ID)
		require.NoError(t, err)
		assert.EqualValues(t, category.DefaultCategoryCount(), total)

		categorias, err := s.categories.List(ctx, casa.ID, false)
		require.NoError(t, err)
		require.Len(t, categorias, category.DefaultCategoryCount())

		porID := make(map[string]category.Category, len(categorias))
		for _, c := range categorias {
			porID[c.ID] = c
		}
		var folhas int
		for _, c := range categorias {
			assert.Equal(t, casa.ID, c.HouseholdID)
			if c.ParentID == nil {
				continue
			}
			folhas++
			pai, achado := porID[*c.ParentID]
			require.Truef(t, achado, "a folha %q aponta para um pai que não voltou da consulta", c.Name)
			assert.Nil(t, pai.ParentID, "a árvore tem exatamente dois níveis (ADR-017b)")
			// A natureza da folha é a do PAI, conferida no dado GRAVADO — não
			// só no caminho de memória que o teste de unidade exercita.
			assert.Equalf(t, pai.Kind, c.Kind, "a folha %q não herdou a natureza de %q", c.Name, pai.Name)
		}
		assert.Equal(t, category.DefaultCategoryCount()-len(category.DefaultGroups()), folhas)

		// --- as palavras ------------------------------------------------------
		palavras, err := s.categories.ListKeywords(ctx, casa.ID)
		require.NoError(t, err)
		assert.Len(t, palavras, palavrasEsperadasDaSemente(),
			"toda palavra da tabela coube no índice único da casa")

		porFolha := map[string]int{}
		for _, k := range palavras {
			assert.Equal(t, casa.ID, k.HouseholdID)
			assert.NotEmpty(t, k.Norm)
			dona, achada := porID[k.CategoryID]
			require.True(t, achada, "palavra pendurada em categoria que não existe")
			assert.NotNil(t, dona.ParentID, "palavra da semente mora na FOLHA, nunca no grupo")
			porFolha[k.CategoryID]++
		}
		assert.LessOrEqual(t, len(porFolha), folhas)

		// --- o classificador em cima do que foi gravado -----------------------
		set, err := classify.NewLoader(s.categories, s.accounts).Load(ctx, casa.ID)
		require.NoError(t, err, "classify.Load monta os matchers com a semente inteira")

		res, err := set.SuggestCategory("expense", textnorm.Normalize("compra no debito - ifood *ifood"))
		require.NoError(t, err)
		require.True(t, res.Matched(), "a primeira importação já sugere categoria de fábrica")
		nome, ok := set.CategoryName(res.Match.OwnerID)
		require.True(t, ok)
		assert.Equal(t, "Delivery", nome)

		// A linha de Pix entre pessoas continua sem sugestão — é a ameaça que a
		// §8 do plano nomeia, conferida aqui contra o dado real.
		semSugestao, err := set.SuggestCategory("expense", textnorm.Normalize(
			"Transferência enviada pelo Pix - JOAO DA SILVA - 123.456.789-00 - BANCO INTER S.A. (0077) Agência: 1 Conta: 123-4"))
		require.NoError(t, err)
		assert.False(t, semSugestao.Matched(), "nada da semente pode casar com o boilerplate do Pix")

		// --- o custo ----------------------------------------------------------
		t.Logf("semente de casa nova em %s: %d categorias, %d palavras, %s (race=%t)",
			s.backendName, total, len(palavras), duracao, raceEnabled)
		if !s.backendIsPG {
			assert.LessOrEqualf(t, duracao, tempoMaximoDaSemente(),
				"a semente roda no caminho do cadastro: %s é tempo que a pessoa espera", duracao)
		}
	})
}

// A semente NÃO é reaplicada quando o usuário já tem casa: EnsureDefault
// devolve cedo. É o erratum ao ADR-029a, e ele é afirmado contra o banco
// porque o texto errado dizia justamente o contrário.
func TestSementeNaoRodaDeNovoParaQuemJaTemCasa(t *testing.T) {
	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		dono := s.makeUser(t, ctx, true)

		categorySvc := category.NewService(s.categories, s.uow)
		householdSvc := household.NewService(s.households, s.memberships,
			household.WithSeeders(categorySvc))

		var primeira household.Summary
		require.NoError(t, s.uow.Do(ctx, func(ctx context.Context) error {
			var err error
			primeira, err = householdSvc.EnsureDefault(ctx, dono.ID, dono.Name)
			return err
		}))

		// A pessoa exclui uma folha, como qualquer categoria dela.
		categorias, err := s.categories.List(ctx, primeira.ID, false)
		require.NoError(t, err)
		var alvo category.Category
		for _, c := range categorias {
			if c.ParentID != nil {
				alvo = c
				break
			}
		}
		require.NotEmpty(t, alvo.ID)
		require.NoError(t, s.uow.Do(ctx, func(ctx context.Context) error {
			if err := s.categories.SoftDelete(ctx, primeira.ID, alvo.ID, now()); err != nil {
				return err
			}
			return s.categories.DeleteKeywords(ctx, primeira.ID, alvo.ID)
		}))

		// Segunda entrada: EnsureDefault devolve a MESMA casa e não semeia nada.
		var segunda household.Summary
		require.NoError(t, s.uow.Do(ctx, func(ctx context.Context) error {
			var err error
			segunda, err = householdSvc.EnsureDefault(ctx, dono.ID, dono.Name)
			return err
		}))
		assert.Equal(t, primeira.ID, segunda.ID)

		total, err := s.categories.CountAll(ctx, primeira.ID)
		require.NoError(t, err)
		assert.EqualValues(t, category.DefaultCategoryCount()-1, total,
			"a folha excluída NÃO volta: a semente não roda no login de quem já tem casa")
	})
}

// palavrasEsperadasDaSemente conta as palavras da tabela — derivado, nunca um
// número escrito à mão.
func palavrasEsperadasDaSemente() int {
	total := 0
	for _, g := range category.DefaultGroups() {
		for _, f := range g.Children {
			total += len(f.Keywords)
		}
	}
	return total
}
