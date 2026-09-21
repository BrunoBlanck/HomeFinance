package gormstore_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Este arquivo cobre as duas tabelas de palavras-chave do schema v4 (spec
// 0005, ADR-026d). O que importa provar aqui mora no BANCO — o índice único
// por casa e o escopo por casa —, então nenhum dublê serviria.

// kw monta uma palavra-chave de categoria pronta; a norm é derivada como o
// serviço faz.
func (s *store) kw(text string, pos int) category.Keyword {
	return category.Keyword{ID: s.nextID("kw"), Keyword: text, Norm: textnorm.Normalize(text), Position: pos, CreatedAt: now()}
}

// akw é o equivalente para conta.
func (s *store) akw(text string, pos int) account.Keyword {
	return account.Keyword{ID: s.nextID("akw"), Keyword: text, Norm: textnorm.Normalize(text), Position: pos, CreatedAt: now()}
}

func TestPalavraChaveDeOutraCasaNaoApareceEmNenhumaConsulta(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		minhaCat := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		alheiaCat := s.makeCategory(t, ctx, alheia.ID, "Mercado", category.KindExpense, nil)
		minhaConta := s.makeAccount(t, ctx, minha.ID, "Nubank")
		alheiaConta := s.makeAccount(t, ctx, alheia.ID, "Nubank")

		require.NoError(t, s.categories.ReplaceKeywords(ctx, minha.ID, minhaCat.ID, []category.Keyword{s.kw("Supermercado", 0)}))
		require.NoError(t, s.categories.ReplaceKeywords(ctx, alheia.ID, alheiaCat.ID, []category.Keyword{s.kw("padaria", 0)}))
		require.NoError(t, s.accounts.ReplaceKeywords(ctx, minha.ID, minhaConta.ID, []account.Keyword{s.akw("nubank", 0)}))
		require.NoError(t, s.accounts.ReplaceKeywords(ctx, alheia.ID, alheiaConta.ID, []account.Keyword{s.akw("nu pagamentos", 0)}))

		// Listagem: só a minha.
		lista, err := s.categories.ListKeywords(ctx, minha.ID)
		require.NoError(t, err)
		require.Len(t, lista, 1)
		assert.Equal(t, "Supermercado", lista[0].Keyword, "a forma exibível é preservada")
		assert.Equal(t, "supermercado", lista[0].Norm)
		assert.Equal(t, minhaCat.ID, lista[0].CategoryID)
		assert.Equal(t, minha.ID, lista[0].HouseholdID)

		contas, err := s.accounts.ListKeywords(ctx, minha.ID)
		require.NoError(t, err)
		require.Len(t, contas, 1)
		assert.Equal(t, minhaConta.ID, contas[0].AccountID)

		// Donas: a norm da vizinha está LIVRE na minha casa.
		donas, err := s.categories.KeywordOwners(ctx, minha.ID, []string{"supermercado", "padaria"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"supermercado": minhaCat.ID}, donas)

		donasConta, err := s.accounts.KeywordOwners(ctx, minha.ID, []string{"nubank", "nu pagamentos"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"nubank": minhaConta.ID}, donasConta)

		// Apagar pela casa errada não apaga nada: o id da categoria alheia,
		// pedido com a MINHA casa, não alcança a linha dela.
		require.NoError(t, s.categories.DeleteKeywords(ctx, minha.ID, alheiaCat.ID))
		require.NoError(t, s.accounts.DeleteKeywords(ctx, minha.ID, alheiaConta.ID))
		daVizinha, err := s.categories.ListKeywords(ctx, alheia.ID)
		require.NoError(t, err)
		assert.Len(t, daVizinha, 1, "a palavra da vizinha continua lá")
		daVizinhaConta, err := s.accounts.ListKeywords(ctx, alheia.ID)
		require.NoError(t, err)
		assert.Len(t, daVizinhaConta, 1)

		// E substituir apontando para a categoria alheia com a minha casa
		// tampouco grava nela: a lista nasce vazia sob a minha casa.
		require.NoError(t, s.categories.ReplaceKeywords(ctx, minha.ID, alheiaCat.ID, []category.Keyword{s.kw("invasao", 0)}))
		daVizinha, err = s.categories.ListKeywords(ctx, alheia.ID)
		require.NoError(t, err)
		require.Len(t, daVizinha, 1)
		assert.Equal(t, "padaria", daVizinha[0].Norm, "a lista da vizinha não foi tocada")
	})
}

// ADR-026(d): a unicidade é por casa e DENTRO do tipo. Verificado pelo índice
// do banco, e não por consulta prévia — é ele que decide a corrida.
func TestIndiceUnicoDePalavraChaveEPorCasaEPorTipo(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)

		mercado := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		farmacia := s.makeCategory(t, ctx, minha.ID, "Farmácia", category.KindExpense, nil)
		conta := s.makeAccount(t, ctx, minha.ID, "Nubank")
		catAlheia := s.makeCategory(t, ctx, alheia.ID, "Mercado", category.KindExpense, nil)

		require.NoError(t, s.categories.ReplaceKeywords(ctx, minha.ID, mercado.ID, []category.Keyword{s.kw("Supermercado", 0)}))

		// Mesma norm (caixa e acento diferentes) em OUTRA categoria da mesma
		// casa: duplicata, traduzida no erro de domínio, sem a palavra no texto.
		err := s.categories.ReplaceKeywords(ctx, minha.ID, farmacia.ID, []category.Keyword{s.kw("SUPERMERCADO", 0)})
		require.ErrorIs(t, err, category.ErrKeywordTaken)
		assert.NotContains(t, err.Error(), "supermercado")

		// Mesma norm em conta da mesma casa: conjuntos independentes.
		require.NoError(t, s.accounts.ReplaceKeywords(ctx, minha.ID, conta.ID, []account.Keyword{s.akw("supermercado", 0)}))

		// Mesma norm em outra casa: aceita.
		require.NoError(t, s.categories.ReplaceKeywords(ctx, alheia.ID, catAlheia.ID, []category.Keyword{s.kw("supermercado", 0)}))

		// A duplicata NÃO deixou meia lista gravada: a farmácia continua sem
		// palavra nenhuma (o DELETE e o INSERT correm na mesma transação do
		// chamador; aqui, sem transação, o INSERT recusado não grava nada).
		donas, err := s.categories.KeywordOwners(ctx, minha.ID, []string{"supermercado"})
		require.NoError(t, err)
		assert.Equal(t, mercado.ID, donas["supermercado"], "a dona continua sendo a primeira")

		// Repetição DENTRO da mesma lista também é recusada pelo índice.
		err = s.categories.ReplaceKeywords(ctx, minha.ID, farmacia.ID, []category.Keyword{s.kw("drogaria", 0), s.kw("Drogaria", 1)})
		require.ErrorIs(t, err, category.ErrKeywordTaken)

		// E a mesma categoria pode regravar a PRÓPRIA palavra (substituição).
		require.NoError(t, s.categories.ReplaceKeywords(ctx, minha.ID, mercado.ID, []category.Keyword{s.kw("supermercado", 0), s.kw("hortifruti", 1)}))
		lista, err := s.categories.ListKeywords(ctx, minha.ID)
		require.NoError(t, err)
		require.Len(t, lista, 2)
	})
}

// PATCH com a lista inteira SUBSTITUI: o que não veio, some; a ordem de
// cadastro é a de Position; e [] limpa.
func TestReplaceKeywordsSubstituiEPreservaAOrdem(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		cat := s.makeCategory(t, ctx, minha.ID, "Transporte", category.KindExpense, nil)
		outra := s.makeCategory(t, ctx, minha.ID, "Lazer", category.KindExpense, nil)

		require.NoError(t, s.uow.Do(ctx, func(ctx context.Context) error {
			return s.categories.ReplaceKeywords(ctx, minha.ID, cat.ID, []category.Keyword{
				s.kw("uber", 0), s.kw("99 pop", 1), s.kw("metrô", 2),
			})
		}))
		require.NoError(t, s.categories.ReplaceKeywords(ctx, minha.ID, outra.ID, []category.Keyword{s.kw("cinema", 0)}))

		lista, err := s.categories.ListKeywords(ctx, minha.ID)
		require.NoError(t, err)
		require.Len(t, lista, 4)

		// Dentro da mesma dona, a ordem é a de Position — não a alfabética nem
		// a do id.
		var doTransporte []string
		for _, k := range lista {
			if k.CategoryID == cat.ID {
				doTransporte = append(doTransporte, k.Keyword)
			}
		}
		assert.Equal(t, []string{"uber", "99 pop", "metrô"}, doTransporte)

		// Substitui: "uber" sai, "onibus" entra, a de Lazer não é tocada.
		require.NoError(t, s.uow.Do(ctx, func(ctx context.Context) error {
			return s.categories.ReplaceKeywords(ctx, minha.ID, cat.ID, []category.Keyword{
				s.kw("metrô", 0), s.kw("ônibus", 1),
			})
		}))
		donas, err := s.categories.KeywordOwners(ctx, minha.ID, []string{"uber", "metro", "onibus", "cinema"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"metro": cat.ID, "onibus": cat.ID, "cinema": outra.ID}, donas)

		// Lista vazia limpa; a de Lazer continua.
		require.NoError(t, s.categories.ReplaceKeywords(ctx, minha.ID, cat.ID, nil))
		lista, err = s.categories.ListKeywords(ctx, minha.ID)
		require.NoError(t, err)
		require.Len(t, lista, 1)
		assert.Equal(t, outra.ID, lista[0].CategoryID)

		// DeleteKeywords apaga a lista da dona — e só a dela.
		require.NoError(t, s.categories.DeleteKeywords(ctx, minha.ID, outra.ID))
		lista, err = s.categories.ListKeywords(ctx, minha.ID)
		require.NoError(t, err)
		assert.Empty(t, lista)
	})
}

// O repositório recusa antes de qualquer SQL o que o serviço não deveria
// mandar: palavra apontando para outra casa/dona, ou sem id/texto/norm.
func TestReplaceKeywordsRecusaListaIncoerente(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		cat := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		conta := s.makeAccount(t, ctx, minha.ID, "Nubank")

		comCasaErrada := s.kw("padaria", 0)
		comCasaErrada.HouseholdID = alheia.ID
		assert.Error(t, s.categories.ReplaceKeywords(ctx, minha.ID, cat.ID, []category.Keyword{comCasaErrada}))

		comDonaErrada := s.kw("padaria", 0)
		comDonaErrada.CategoryID = "outra-categoria"
		assert.Error(t, s.categories.ReplaceKeywords(ctx, minha.ID, cat.ID, []category.Keyword{comDonaErrada}))

		semNorm := s.kw("padaria", 0)
		semNorm.Norm = ""
		assert.Error(t, s.categories.ReplaceKeywords(ctx, minha.ID, cat.ID, []category.Keyword{semNorm}))

		semID := s.akw("nubank", 0)
		semID.ID = ""
		assert.Error(t, s.accounts.ReplaceKeywords(ctx, minha.ID, conta.ID, []account.Keyword{semID}))

		contaErrada := s.akw("nubank", 0)
		contaErrada.AccountID = "outra-conta"
		assert.Error(t, s.accounts.ReplaceKeywords(ctx, minha.ID, conta.ID, []account.Keyword{contaErrada}))

		// Nada foi gravado por nenhuma das tentativas.
		lista, err := s.categories.ListKeywords(ctx, minha.ID)
		require.NoError(t, err)
		assert.Empty(t, lista)

		// Casa vazia é erro, nunca "todas as casas".
		_, err = s.categories.ListKeywords(ctx, "")
		assert.Error(t, err)
		_, err = s.accounts.KeywordOwners(ctx, "", []string{"x"})
		assert.Error(t, err)
	})
}

// KeywordOwners fatia o IN em idChunkSize (200): uma lista maior do que isso
// precisa continuar encontrando o que está no fim dela.
func TestKeywordOwnersFatiaListasGrandes(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		cat := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)
		require.NoError(t, s.categories.ReplaceKeywords(ctx, minha.ID, cat.ID, []category.Keyword{s.kw("supermercado", 0)}))

		norms := make([]string, 0, 260)
		for i := range 255 {
			norms = append(norms, fmt.Sprintf("palavra-%03d", i))
		}
		// Repetições e vazios não contam como parâmetro nem quebram nada.
		norms = append(norms, "", "supermercado", "supermercado")

		donas, err := s.categories.KeywordOwners(ctx, minha.ID, norms)
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"supermercado": cat.ID}, donas)
	})
}

// --- AppendKeywords (spec 0010 §10.4, achado A2 da revisão da E9b) --------------------

// AppendKeywords ACRESCENTA: o que já estava na dona fica, a numeração de
// Position continua do maior valor lido no banco (não da lista do chamador),
// e a lista de outra dona não é tocada. É o contrário de ReplaceKeywords —
// e é por não ter DELETE que uma palavra comitada por outra transação entre a
// leitura e a escrita não some.
func TestAppendKeywordsAcrescentaSemApagarEContinuaANumeracao(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		cat := s.makeCategory(t, ctx, minha.ID, "Transporte", category.KindExpense, nil)
		outra := s.makeCategory(t, ctx, minha.ID, "Lazer", category.KindExpense, nil)
		conta := s.makeAccount(t, ctx, minha.ID, "Nubank")

		// Estado inicial com um BURACO na numeração (0 e 5): o próximo é 6,
		// não len(lista).
		require.NoError(t, s.categories.ReplaceKeywords(ctx, minha.ID, cat.ID, []category.Keyword{
			s.kw("uber", 0), s.kw("99 pop", 5),
		}))
		require.NoError(t, s.categories.ReplaceKeywords(ctx, minha.ID, outra.ID, []category.Keyword{s.kw("cinema", 0)}))
		require.NoError(t, s.accounts.ReplaceKeywords(ctx, minha.ID, conta.ID, []account.Keyword{s.akw("nubank", 0)}))

		// A Position do chamador é IGNORADA: o banco numera.
		require.NoError(t, s.uow.Do(ctx, func(ctx context.Context) error {
			return s.categories.AppendKeywords(ctx, minha.ID, cat.ID, []category.Keyword{
				s.kw("metrô", 0), s.kw("ônibus", 1),
			})
		}))
		require.NoError(t, s.accounts.AppendKeywords(ctx, minha.ID, conta.ID, []account.Keyword{s.akw("nu pagamentos", 0)}))

		lista, err := s.categories.ListKeywords(ctx, minha.ID)
		require.NoError(t, err)
		var doTransporte []string
		var posicoes []int
		for _, k := range lista {
			if k.CategoryID == cat.ID {
				doTransporte = append(doTransporte, k.Keyword)
				posicoes = append(posicoes, k.Position)
			}
		}
		assert.Equal(t, []string{"uber", "99 pop", "metrô", "ônibus"}, doTransporte, "nada apagado; as novas no fim")
		assert.Equal(t, []int{0, 5, 6, 7}, posicoes, "continua do maior Position lido")

		donas, err := s.categories.KeywordOwners(ctx, minha.ID, []string{"cinema", "metro"})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"cinema": outra.ID, "metro": cat.ID}, donas, "a outra dona não é tocada")

		contas, err := s.accounts.ListKeywords(ctx, minha.ID)
		require.NoError(t, err)
		require.Len(t, contas, 2)
		assert.Equal(t, []int{0, 1}, []int{contas[0].Position, contas[1].Position})

		// Lista vazia é no-op — nem DELETE, nem INSERT.
		require.NoError(t, s.categories.AppendKeywords(ctx, minha.ID, cat.ID, nil))
		lista, err = s.categories.ListKeywords(ctx, minha.ID)
		require.NoError(t, err)
		assert.Len(t, lista, 5)
	})
}

// A colisão é decidida pelo ÍNDICE ÚNICO — de outra dona ou da própria —, e o
// teto por dona é reconferido contra o COUNT(*) vivo, não contra o que o
// chamador acha que existe.
func TestAppendKeywordsRecusaColisaoETetoPeloBanco(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		cat := s.makeCategory(t, ctx, minha.ID, "Transporte", category.KindExpense, nil)
		outra := s.makeCategory(t, ctx, minha.ID, "Lazer", category.KindExpense, nil)
		conta := s.makeAccount(t, ctx, minha.ID, "Nubank")

		require.NoError(t, s.categories.ReplaceKeywords(ctx, minha.ID, cat.ID, []category.Keyword{s.kw("uber", 0)}))
		require.NoError(t, s.categories.ReplaceKeywords(ctx, minha.ID, outra.ID, []category.Keyword{s.kw("cinema", 0)}))
		require.NoError(t, s.accounts.ReplaceKeywords(ctx, minha.ID, conta.ID, []account.Keyword{s.akw("nubank", 0)}))

		// Palavra de OUTRA dona: ErrKeywordTaken, e nada da lista entra —
		// a transação do chamador desfaz o resto.
		err := s.uow.Do(ctx, func(ctx context.Context) error {
			return s.categories.AppendKeywords(ctx, minha.ID, cat.ID, []category.Keyword{s.kw("metrô", 0), s.kw("CINEMA", 1)})
		})
		require.ErrorIs(t, err, category.ErrKeywordTaken)
		// Palavra da PRÓPRIA dona também colide: acrescentar não é "garantir
		// presença", e quem chama já pulou as que existem (already_present).
		err = s.uow.Do(ctx, func(ctx context.Context) error {
			return s.categories.AppendKeywords(ctx, minha.ID, cat.ID, []category.Keyword{s.kw("Uber", 0)})
		})
		require.ErrorIs(t, err, category.ErrKeywordTaken)
		err = s.uow.Do(ctx, func(ctx context.Context) error {
			return s.accounts.AppendKeywords(ctx, minha.ID, conta.ID, []account.Keyword{s.akw("NUBANK", 0)})
		})
		require.ErrorIs(t, err, account.ErrKeywordTaken)

		lista, err := s.categories.ListKeywords(ctx, minha.ID)
		require.NoError(t, err)
		assert.Len(t, lista, 2, "a transação desfeita não deixou 'metrô' pela metade")

		// Teto: 1 existente + 20 novas passa de MaxKeywordsPerOwner.
		vinte := make([]category.Keyword, 0, 20)
		for i := range 20 {
			vinte = append(vinte, s.kw(fmt.Sprintf("palavra %c", 'a'+i), i))
		}
		err = s.uow.Do(ctx, func(ctx context.Context) error {
			return s.categories.AppendKeywords(ctx, minha.ID, cat.ID, vinte)
		})
		require.ErrorIs(t, err, category.ErrTooManyKeywords)
		// 1 + 19 cabe exatamente.
		require.NoError(t, s.uow.Do(ctx, func(ctx context.Context) error {
			return s.categories.AppendKeywords(ctx, minha.ID, cat.ID, vinte[:19])
		}))
		lista, err = s.categories.ListKeywords(ctx, minha.ID)
		require.NoError(t, err)
		assert.Len(t, lista, 21) // 20 do Transporte + 1 do Lazer

		// Recusa antes de qualquer SQL o que o serviço não deveria mandar.
		deOutra := s.kw("x", 0)
		deOutra.CategoryID = outra.ID
		require.Error(t, s.categories.AppendKeywords(ctx, minha.ID, cat.ID, []category.Keyword{deOutra}))
		semNorm := s.kw("y", 0)
		semNorm.Norm = ""
		require.Error(t, s.categories.AppendKeywords(ctx, minha.ID, cat.ID, []category.Keyword{semNorm}))
		require.Error(t, s.categories.AppendKeywords(ctx, "", cat.ID, []category.Keyword{s.kw("z", 0)}))
	})
}
