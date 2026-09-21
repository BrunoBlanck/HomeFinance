package category_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Testes da entrega E7 (spec 0006, ADR-029): as naturezas `investment` e
// `redemption`, a regra ÚNICA de pareamento, a árvore de quatro arrays, a
// semente e a troca de natureza dentro do mesmo lado do dinheiro.

// --- allowlist ---------------------------------------------------------------

func TestValidKindEhAllowlistFechadaDeQuatro(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{"income", "expense", "investment", "redemption"} {
		assert.True(t, category.ValidKind(kind), "%q pertence à allowlist", kind)
	}

	// Conjunto FECHADO: nada mais entra, e a comparação é exata — sem aparar
	// espaço, sem ignorar caixa. Um `kind` com espaço sobrando que passasse
	// aqui viraria uma quinta natureza de fato, invisível para todo switch.
	for _, kind := range []string{
		"", " ", "Investment", "INVESTMENT", " investment", "investment ",
		"investments", "invest", "redemptions", "transfer_in", "transfer_out",
		"null", "*", "income,expense",
	} {
		assert.False(t, category.ValidKind(kind), "%q não pertence à allowlist", kind)
	}
}

// AceitaLancamento é a única fonte da verdade do pareamento (ADR-029b). A
// tabela cobre os 4 tipos de lançamento × as 4 naturezas — as 16 combinações,
// nenhuma implícita — mais o lixo.
func TestAceitaLancamentoCobreAsDezesseisCombinacoes(t *testing.T) {
	t.Parallel()

	casos := []struct {
		lancamento string
		categoria  string
		aceita     bool
	}{
		// Receita: aceita receita e RESGATE (dinheiro que entra na conta).
		{"income", "income", true},
		{"income", "redemption", true},
		{"income", "expense", false},
		{"income", "investment", false},

		// Despesa: aceita despesa e APORTE (dinheiro que sai da conta).
		{"expense", "expense", true},
		{"expense", "investment", true},
		{"expense", "income", false},
		{"expense", "redemption", false},

		// Transferência não tem categoria de natureza nenhuma (ADR-016).
		{"transfer_in", "income", false},
		{"transfer_in", "expense", false},
		{"transfer_in", "investment", false},
		{"transfer_in", "redemption", false},
		{"transfer_out", "income", false},
		{"transfer_out", "expense", false},
		{"transfer_out", "investment", false},
		{"transfer_out", "redemption", false},
	}

	for _, c := range casos {
		t.Run(c.lancamento+"/"+c.categoria, func(t *testing.T) {
			assert.Equal(t, c.aceita, category.AceitaLancamento(c.lancamento, c.categoria))
		})
	}
}

func TestAceitaLancamentoRecusaEntradaFonteDeErro(t *testing.T) {
	t.Parallel()

	// Vazio, lixo e caixa trocada: nenhum deles casa com lado nenhum.
	for _, lancamento := range []string{"", " ", "Income", "EXPENSE", "investment", "redemption", "x"} {
		for _, cat := range []string{"income", "expense", "investment", "redemption", ""} {
			assert.False(t, category.AceitaLancamento(lancamento, cat),
				"lançamento %q não aceita categoria %q", lancamento, cat)
		}
	}

	// Argumentos TROCADOS (categoria primeiro) só podem produzir o falso
	// positivo das duas naturezas homônimas — e é por isso que a ordem está no
	// nome da função. As quatro trocas que importam:
	assert.False(t, category.AceitaLancamento("investment", "expense"), "ordem trocada não aprova")
	assert.False(t, category.AceitaLancamento("redemption", "income"), "ordem trocada não aprova")
	assert.False(t, category.AceitaLancamento("investment", "income"), "ordem trocada não aprova")
	assert.False(t, category.AceitaLancamento("redemption", "expense"), "ordem trocada não aprova")
}

// --- CRUD das naturezas novas (aceite 1) -------------------------------------

func TestCRUDCompletoDasNaturezasNovas(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{category.KindInvestment, category.KindRedemption} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			repo := novoRepo()
			svc := novoServico(t, repo)
			ctx := t.Context()

			grupo := criarGrupo(t, svc, minhaCasa, "Carteira", kind)
			assert.Equal(t, kind, grupo.Kind)

			// A folha herda a natureza do grupo, como em qualquer outra.
			folha := criarFolha(t, svc, minhaCasa, "CDB", grupo.ID)
			assert.Equal(t, kind, folha.Kind, "a folha herda a natureza do grupo")

			// Terceiro nível continua recusado (ADR-017b).
			_, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{
				Name: "CDB 15 dias", ParentID: &folha.ID,
			})
			require.ErrorIs(t, err, category.ErrTooDeep)

			// Renomear, arquivar, desarquivar e excluir: o ciclo inteiro.
			novoNome := "Carteira de longo prazo"
			renomeada, err := svc.Update(ctx, ator(minhaCasa), grupo.ID, category.UpdateInput{Name: &novoNome})
			require.NoError(t, err)
			assert.Equal(t, novoNome, renomeada.Name)
			assert.Equal(t, kind, renomeada.Kind, "renomear não mexe na natureza")

			arquivada, err := svc.Archive(ctx, ator(minhaCasa), folha.ID)
			require.NoError(t, err)
			require.NotNil(t, arquivada.ArchivedAt)

			desarquivada, err := svc.Unarchive(ctx, ator(minhaCasa), folha.ID)
			require.NoError(t, err)
			assert.Nil(t, desarquivada.ArchivedAt)

			require.NoError(t, svc.Delete(ctx, ator(minhaCasa), folha.ID))
			require.NoError(t, svc.Delete(ctx, ator(minhaCasa), grupo.ID))
		})
	}
}

// O teto de 200 por casa não conhece natureza: ele conta grupos e folhas das
// quatro juntos (aceite 1).
func TestTetoPorCasaValeIgualParaAsNaturezasNovas(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	for i := 0; i < category.MaxPerHousehold; i++ {
		_, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{
			Name: "Despesa " + string(rune('A'+i%26)) + string(rune('a'+i/26)),
			Kind: category.KindExpense,
		})
		require.NoError(t, err, "categoria %d", i)
	}

	_, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{
		Name: "Investimentos", Kind: category.KindInvestment,
	})
	require.ErrorIs(t, err, category.ErrTooMany, "o teto conta as quatro naturezas juntas")
}

func TestCriarGrupoComNaturezaForaDaAllowlistEhRecusado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	for _, kind := range []string{"", "Investment", "investments", "transfer_in"} {
		_, err := svc.Create(t.Context(), ator(minhaCasa), category.CreateInput{Name: "X", Kind: kind})
		require.ErrorIs(t, err, category.ErrInvalidKind, "kind %q", kind)
	}
}

// --- ListView com quatro arrays ----------------------------------------------

func TestListaSeparaAsQuatroNaturezas(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	criarGrupo(t, svc, minhaCasa, "Salário", category.KindIncome)
	criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	aporte := criarGrupo(t, svc, minhaCasa, "Investimentos", category.KindInvestment)
	criarGrupo(t, svc, minhaCasa, "Resgates", category.KindRedemption)
	criarFolha(t, svc, minhaCasa, "CDB", aporte.ID)

	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)

	require.Len(t, arvore.Income, 1)
	require.Len(t, arvore.Expense, 1)
	require.Len(t, arvore.Investment, 1)
	require.Len(t, arvore.Redemption, 1)
	assert.Equal(t, "Investimentos", arvore.Investment[0].Name)
	require.Len(t, arvore.Investment[0].Children, 1)
	assert.Equal(t, "CDB", arvore.Investment[0].Children[0].Name)
	assert.Equal(t, category.KindInvestment, arvore.Investment[0].Children[0].Kind)

	// Nenhuma categoria de aporte ou resgate vaza para os arrays de receita e
	// despesa — é justamente o vazamento que faria a tela de despesa oferecer
	// uma categoria de resgate.
	assert.Equal(t, "Moradia", arvore.Expense[0].Name)
	assert.Equal(t, "Salário", arvore.Income[0].Name)
}

// Os quatro arrays são `required` no contrato (CategoryTree): sempre
// presentes, `[]` quando vazios, NUNCA null.
func TestArvoreVaziaTrazOsQuatroArraysComoListaVazia(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	arvore, err := svc.List(t.Context(), ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)

	assert.NotNil(t, arvore.Income)
	assert.NotNil(t, arvore.Expense)
	assert.NotNil(t, arvore.Investment)
	assert.NotNil(t, arvore.Redemption)

	// A prova que vale é a do JSON: é ele que a tela lê.
	bruto, err := json.Marshal(arvore)
	require.NoError(t, err)
	assert.JSONEq(t, `{"income":[],"expense":[],"investment":[],"redemption":[]}`, string(bruto))
}

func TestFiltroPorNaturezaAceitaAsNovasERecusaLixo(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	criarGrupo(t, svc, minhaCasa, "Investimentos", category.KindInvestment)

	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{Kind: category.KindInvestment})
	require.NoError(t, err)
	assert.Len(t, arvore.Investment, 1)
	assert.Empty(t, arvore.Expense, "o filtro não devolve o que não foi pedido")
	assert.NotNil(t, arvore.Expense, "mas o array continua presente")

	_, err = svc.List(ctx, ator(minhaCasa), category.ListInput{Kind: "investments"})
	require.ErrorIs(t, err, category.ErrInvalidKind)
}

// --- semente ------------------------------------------------------------------

func TestSementeTrazInvestimentosEResgates(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)

	require.Len(t, arvore.Investment, 1)
	assert.Equal(t, "Investimentos", arvore.Investment[0].Name)
	require.Len(t, arvore.Redemption, 1)
	assert.Equal(t, "Resgates", arvore.Redemption[0].Name)

	// E a semente continua idempotente com os dois grupos novos — agora com as
	// folhas deles junto (ADR-033). Ela roda UMA vez por casa, na criação; a
	// idempotência é defesa em profundidade, não rotina.
	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))
	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))
	total, err := repo.CountAll(ctx, minhaCasa)
	require.NoError(t, err)
	assert.EqualValues(t, category.DefaultCategoryCount(), total)
}

// COMPORTAMENTO ACEITO, FIXADO AQUI DE PROPÓSITO (ADR-029a + o achado do
// arquiteto): SeedDefaults pula por NOME normalizado e NÃO olha o `kind`.
//
// A casa que já criou "Investimentos" à mão como categoria de DESPESA
// continua com ela e não recebe o grupo novo de natureza `investment`. O
// caminho dessa casa é a troca de natureza (o teste seguinte), que é
// explícita, auditada com autor e leva as subcategorias junto.
//
// Este teste existe para que ninguém "conserte" a semente para semear por
// (nome, natureza): isso daria duas categorias "Investimentos" na mesma tela,
// recriadas a cada login pelo auto-reparo.
func TestSementeNaoDuplicaNomeJaUsadoComOutraNatureza(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	// A casa já tem "Investimentos" como DESPESA, criada à mão.
	daCasa := criarGrupo(t, svc, minhaCasa, "Investimentos", category.KindExpense)

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)

	assert.Empty(t, arvore.Investment,
		"a semente NÃO cria um segundo 'Investimentos' com natureza diferente")
	require.Len(t, arvore.Redemption, 1, "'Resgates', cujo nome está livre, entra normalmente")

	var achados int
	for _, g := range arvore.Expense {
		if g.Name == "Investimentos" {
			achados++
			assert.Equal(t, daCasa.ID, g.ID, "a categoria da casa é preservada, nunca substituída")
		}
	}
	assert.Equal(t, 1, achados, "existe exatamente UMA categoria chamada Investimentos")
}

// --- troca de natureza (ADR-029c, aceites 3, 14 e 15) -------------------------

func TestTrocaDeNaturezaDentroDoMesmoLadoEhPermitidaMesmoEmUso(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome, de, para string
	}{
		{"despesa vira aporte", category.KindExpense, category.KindInvestment},
		{"aporte volta a despesa", category.KindInvestment, category.KindExpense},
		{"receita vira resgate", category.KindIncome, category.KindRedemption},
		{"resgate volta a receita", category.KindRedemption, category.KindIncome},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()

			repo := novoRepo()
			// A categoria está EM USO: há lançamento pendurado nela.
			svc := novoServico(t, repo, category.WithUsageCheckers(usoFake{emUso: true}))

			grupo := criarGrupo(t, svc, minhaCasa, "Investimentos", c.de)
			trocada, err := svc.Update(t.Context(), ator(minhaCasa), grupo.ID,
				category.UpdateInput{Kind: &c.para})
			require.NoError(t, err, "dentro do mesmo lado do dinheiro a troca é permitida")
			assert.Equal(t, c.para, trocada.Kind)
		})
	}
}

func TestTrocaDeNaturezaCruzandoOLadoContinuaTravadaComUso(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome, de, para string
	}{
		{"despesa vira receita", category.KindExpense, category.KindIncome},
		{"receita vira despesa", category.KindIncome, category.KindExpense},
		{"aporte vira receita", category.KindInvestment, category.KindIncome},
		{"aporte vira resgate", category.KindInvestment, category.KindRedemption},
		{"resgate vira despesa", category.KindRedemption, category.KindExpense},
		{"resgate vira aporte", category.KindRedemption, category.KindInvestment},
		{"receita vira aporte", category.KindIncome, category.KindInvestment},
		{"despesa vira resgate", category.KindExpense, category.KindRedemption},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()

			repo := novoRepo()
			svc := novoServico(t, repo, category.WithUsageCheckers(usoFake{emUso: true}))

			grupo := criarGrupo(t, svc, minhaCasa, "Categoria", c.de)
			_, err := svc.Update(t.Context(), ator(minhaCasa), grupo.ID, category.UpdateInput{Kind: &c.para})
			require.ErrorIs(t, err, category.ErrKindLocked, "cruzar o lado do dinheiro continua recusado")

			// E nada mudou.
			atual, err := svc.Get(t.Context(), ator(minhaCasa), grupo.ID)
			require.NoError(t, err)
			assert.Equal(t, c.de, atual.Kind)
		})
	}
}

// Sem uso e sem filhas, cruzar o lado continua permitido — é o comportamento
// que já existia e o ADR-029c não revoga.
func TestTrocaCruzandoOLadoContinuaPermitidaSemUsoESemFilhas(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	grupo := criarGrupo(t, svc, minhaCasa, "Categoria", category.KindExpense)
	novo := category.KindIncome
	trocada, err := svc.Update(t.Context(), ator(minhaCasa), grupo.ID, category.UpdateInput{Kind: &novo})
	require.NoError(t, err)
	assert.Equal(t, category.KindIncome, trocada.Kind)
}

// Aceite 14: a troca dentro do mesmo lado CASCATEIA para todas as filhas,
// ativas e arquivadas. Sem a cascata a regra seria inútil — mover categoria de
// grupo não existe no v1 (ErrParentImmutable).
func TestTrocaDentroDoMesmoLadoCascateiaParaTodasAsFilhas(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo, category.WithUsageCheckers(usoFake{emUso: true}))
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Investimentos", category.KindExpense)
	ativa := criarFolha(t, svc, minhaCasa, "CDB", grupo.ID)
	arquivadaFolha := criarFolha(t, svc, minhaCasa, "Tesouro", grupo.ID)
	_, err := svc.Archive(ctx, ator(minhaCasa), arquivadaFolha.ID)
	require.NoError(t, err)

	novo := category.KindInvestment
	trocado, err := svc.Update(ctx, ator(minhaCasa), grupo.ID, category.UpdateInput{Kind: &novo})
	require.NoError(t, err)
	assert.Equal(t, category.KindInvestment, trocado.Kind)

	// A filha ATIVA acompanhou.
	filhaAtiva, err := svc.Get(ctx, ator(minhaCasa), ativa.ID)
	require.NoError(t, err)
	assert.Equal(t, category.KindInvestment, filhaAtiva.Kind, "a filha ativa acompanha o grupo")

	// A ARQUIVADA também: arquivar não desfaz a marcação do passado, e uma
	// filha arquivada com a natureza velha voltaria divergente do grupo no dia
	// em que fosse desarquivada.
	filhaArquivada, err := svc.Get(ctx, ator(minhaCasa), arquivadaFolha.ID)
	require.NoError(t, err)
	assert.Equal(t, category.KindInvestment, filhaArquivada.Kind, "a filha arquivada também acompanha")
	assert.NotNil(t, filhaArquivada.ArchivedAt, "e continua arquivada")
}

// A cascata é ATÔMICA: se a gravação de UMA filha falhar, nada muda — nem o
// grupo, nem as filhas que já tinham sido gravadas.
func TestCascataDaNaturezaEhAtomica(t *testing.T) {
	t.Parallel()

	base := novoRepo()
	falha := errors.New("banco fora do ar")
	repo := &repoQueFalhaNoUpdate{repoFake: base}
	svc := category.NewService(repo, txComRollback{repo: base},
		category.WithIDs(base.proximoID),
		category.WithUsageCheckers(usoFake{emUso: true}),
	)
	ctx := t.Context()

	grupo, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{
		Name: "Investimentos", Kind: category.KindExpense,
	})
	require.NoError(t, err)
	primeira, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{Name: "CDB", ParentID: &grupo.ID})
	require.NoError(t, err)
	segunda, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{Name: "Tesouro", ParentID: &grupo.ID})
	require.NoError(t, err)

	// A SEGUNDA filha a ser gravada falha.
	repo.falharEm, repo.erro = segunda.ID, falha

	novo := category.KindInvestment
	_, err = svc.Update(ctx, ator(minhaCasa), grupo.ID, category.UpdateInput{Kind: &novo})
	require.ErrorIs(t, err, falha)

	// Nada mudou: nem o grupo, nem a filha que já tinha sido gravada.
	repo.falharEm, repo.erro = "", nil
	for _, id := range []string{grupo.ID, primeira.ID, segunda.ID} {
		atual, err := svc.Get(ctx, ator(minhaCasa), id)
		require.NoError(t, err)
		assert.Equal(t, category.KindExpense, atual.Kind,
			"a transação inteira foi desfeita (id %s)", id)
	}
}

// Aceite 15: a FOLHA sozinha continua travada em qualquer direção, inclusive
// dentro do mesmo lado do dinheiro. Mudar só a filha criaria uma filha de
// natureza diferente do grupo — o estado que a cascata existe para evitar.
func TestFolhaContinuaTravadaEmQualquerDirecao(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo) // SEM uso: a trava da folha não depende disso
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Investimentos", category.KindExpense)
	folha := criarFolha(t, svc, minhaCasa, "CDB", grupo.ID)

	for _, novo := range []string{
		category.KindInvestment, // mesmo lado
		category.KindIncome,     // outro lado
		category.KindRedemption,
	} {
		_, err := svc.Update(ctx, ator(minhaCasa), folha.ID, category.UpdateInput{Kind: &novo})
		require.ErrorIs(t, err, category.ErrKindLocked, "folha -> %s", novo)

		atual, err := svc.Get(ctx, ator(minhaCasa), folha.ID)
		require.NoError(t, err)
		assert.Equal(t, category.KindExpense, atual.Kind)
	}
}

func TestTrocaParaNaturezaForaDaAllowlistEhInvalidKindNaoKindLocked(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo, category.WithUsageCheckers(usoFake{emUso: true}))

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	lixo := "investments"
	_, err := svc.Update(t.Context(), ator(minhaCasa), grupo.ID, category.UpdateInput{Kind: &lixo})
	require.ErrorIs(t, err, category.ErrInvalidKind)
}

// --- palavra-chave entre as quatro naturezas (aceite 4) ------------------------

func TestPalavraChaveContinuaUnicaPorCasaEntreAsQuatroNaturezas(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	// "CDB" já pertence a uma categoria de DESPESA.
	dona, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{
		Name: "Aplicações", Kind: category.KindExpense, Keywords: []string{"CDB"},
	})
	require.NoError(t, err)

	// Cadastrá-la numa de INVESTIMENTO é 409 KEYWORD_TAKEN, com a palavra e a
	// dona — a ambiguidade entre naturezas nasce barrada, sem regra nova.
	_, err = svc.Create(ctx, ator(minhaCasa), category.CreateInput{
		Name: "Renda fixa", Kind: category.KindInvestment, Keywords: []string{"cdb"},
	})
	var tomada *category.KeywordTakenError
	require.ErrorAs(t, err, &tomada)
	assert.Equal(t, "cdb", tomada.Keyword, "a palavra que volta é a que o cliente enviou")
	assert.Equal(t, dona.ID, tomada.OwnerID)

	// E o mesmo no sentido inverso, entre resgate e receita.
	_, err = svc.Create(ctx, ator(minhaCasa), category.CreateInput{
		Name: "Rendimentos", Kind: category.KindRedemption, Keywords: []string{"juros"},
	})
	require.NoError(t, err)
	_, err = svc.Create(ctx, ator(minhaCasa), category.CreateInput{
		Name: "Outras receitas", Kind: category.KindIncome, Keywords: []string{"JUROS"},
	})
	require.ErrorAs(t, err, &tomada)
	assert.Equal(t, "JUROS", tomada.Keyword)
}

// --- isolamento por casa -------------------------------------------------------

func TestCategoriaDeInvestimentoDeOutraCasaNaoEhAlcancavel(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	alheia := criarGrupo(t, svc, outraCasa, "Investimentos", category.KindInvestment)

	_, err := svc.Get(ctx, ator(minhaCasa), alheia.ID)
	require.ErrorIs(t, err, category.ErrNotFound)

	novo := category.KindExpense
	_, err = svc.Update(ctx, ator(minhaCasa), alheia.ID, category.UpdateInput{Kind: &novo})
	require.ErrorIs(t, err, category.ErrNotFound, "a troca de natureza não alcança outra casa")

	// A árvore da minha casa não a enxerga.
	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)
	assert.Empty(t, arvore.Investment)
}

// --- dublê auxiliar ------------------------------------------------------------

// repoQueFalhaNoUpdate é o repoFake com um Update que falha numa categoria
// escolhida — é como a atomicidade da cascata é provada sem um banco real.
type repoQueFalhaNoUpdate struct {
	*repoFake
	falharEm string
	erro     error
}

func (r *repoQueFalhaNoUpdate) Update(ctx context.Context, c *category.Category) error {
	if r.erro != nil && c.ID == r.falharEm {
		return r.erro
	}
	return r.repoFake.Update(ctx, c)
}

// --- borda HTTP ----------------------------------------------------------------

// Os códigos `CATEGORY_KIND_MISMATCH` e `KIND_LOCKED` que a spec 0006 cita NÃO
// existem no enum fechado de ErrorCode do projeto. O comportamento real — e o
// que estes testes travam — é 422 VALIDATION_FAILED com o campo certo.
func TestBordaHTTPDasNaturezasNovas(t *testing.T) {
	t.Parallel()

	t.Run("cria grupo de aporte e de resgate", func(t *testing.T) {
		t.Parallel()
		amb := novoAmbiente(t)

		for kind, nome := range map[string]string{"investment": "Investimentos", "redemption": "Resgates"} {
			rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
				fmt.Sprintf(`{"name":%q,"kind":%q}`, nome, kind), amb.handler.Create, "")
			require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

			var criada category.View
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &criada))
			assert.Equal(t, kind, criada.Kind)
		}
	})

	t.Run("natureza fora da allowlist é 400 em fields.kind", func(t *testing.T) {
		t.Parallel()
		amb := novoAmbiente(t)

		rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
			`{"name":"X","kind":"investments"}`, amb.handler.Create, "")
		require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

		codigo, campos := corpoDeErro(t, rec)
		assert.Equal(t, "VALIDATION_FAILED", codigo)
		assert.Contains(t, campos, "kind")
	})

	t.Run("cruzar o lado em uso é 422 VALIDATION_FAILED em fields.kind", func(t *testing.T) {
		t.Parallel()
		amb := novoAmbiente(t, category.WithUsageCheckers(usoFake{emUso: true}))

		grupo := criarGrupo(t, amb.svc, minhaCasa, "Moradia", category.KindExpense)
		rec := amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+grupo.ID,
			`{"kind":"income"}`, amb.handler.Update, grupo.ID)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

		codigo, campos := corpoDeErro(t, rec)
		assert.Equal(t, "VALIDATION_FAILED", codigo, "não existe código KIND_LOCKED no enum")
		assert.Contains(t, campos, "kind")
		assert.NotContains(t, rec.Body.String(), "KIND_LOCKED")
	})

	t.Run("trocar dentro do mesmo lado em uso responde 200", func(t *testing.T) {
		t.Parallel()
		amb := novoAmbiente(t, category.WithUsageCheckers(usoFake{emUso: true}))

		grupo := criarGrupo(t, amb.svc, minhaCasa, "Investimentos", category.KindExpense)
		rec := amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+grupo.ID,
			`{"kind":"investment"}`, amb.handler.Update, grupo.ID)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var trocada category.View
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &trocada))
		assert.Equal(t, category.KindInvestment, trocada.Kind)
	})

	t.Run("a árvore traz os quatro arrays, nunca null", func(t *testing.T) {
		t.Parallel()
		amb := novoAmbiente(t)

		rec := amb.chamar(t, minhaCasa, http.MethodGet, "/categories", "", amb.handler.List, "")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

		var bruto map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &bruto))
		for _, campo := range []string{"income", "expense", "investment", "redemption"} {
			valor, presente := bruto[campo]
			require.True(t, presente, "o campo %q é required no contrato", campo)
			assert.Equal(t, "[]", string(valor), "%q vem como lista vazia, nunca null", campo)
		}
		assert.Len(t, bruto, 4, "nenhum campo a mais na resposta")
	})
}
