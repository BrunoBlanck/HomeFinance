package transaction_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// §13 da spec 0005 — "grupo com subcategoria ATIVA não recebe lançamento" no
// SERVIDOR, nos dois caminhos de escrita que atribuem categoria: o
// UpdateCategory (PATCH /transactions/{id}) e o CreateBatch (confirm da
// importação, decisão ou defaultCategoryId).
//
// Estes testes ficam no nível do serviço, contra dublês, porque é aqui que a
// regra mora; a fiação HTTP e o caminho pelo banco real estão em
// internal/importer (qa_e11_atalho_categoria_test.go).

const (
	grupoComFilha  = "00000000-0000-7000-8000-00000000d001"
	filhaAtiva     = "00000000-0000-7000-8000-00000000d002"
	grupoSozinho   = "00000000-0000-7000-8000-00000000d003"
	grupoDeReceita = "00000000-0000-7000-8000-00000000d004"
)

// filha cria uma subcategoria do grupo, com a natureza herdada dele.
func (a *ambiente) filha(casa, id, nome, paiID string, arquivada, excluida bool) category.Category {
	pai := a.categorias.linhas[paiID]
	c := category.Category{
		ID: id, HouseholdID: casa, ParentID: &paiID, Name: nome,
		NameNorm: textnorm.Normalize(nome), Kind: pai.Kind,
	}
	if arquivada {
		c.ArchivedAt = &agora
	}
	if excluida {
		c.DeletedAt = &agora
	}
	return a.categorias.add(c)
}

// cenarioDeGrupos monta a casa com um grupo COM filha ativa, um grupo sem
// filha nenhuma e uma linha de despesa para categorizar.
func cenarioDeGrupos(t *testing.T) (*ambiente, transaction.Transaction) {
	t.Helper()
	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, grupoComFilha, "Alimentação", category.KindExpense)
	amb.filha(minhaCasa, filhaAtiva, "Restaurantes", grupoComFilha, false, false)
	amb.categoria(minhaCasa, grupoSozinho, "Assinaturas", category.KindExpense)
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Supermercado", 150_00, 3)
	return amb, alvo
}

func TestUpdateCategoryRecusaGrupoComSubcategoriaAtiva(t *testing.T) {
	t.Parallel()

	amb, alvo := cenarioDeGrupos(t)

	_, err := amb.svc.UpdateCategory(t.Context(), ator(minhaCasa), alvo.ID, grupoComFilha)
	require.ErrorIs(t, err, transaction.ErrCategoryIsParentGroup)
	assert.Nil(t, amb.repo.linhas[alvo.ID].CategoryID, "nada gravado")
	assert.Empty(t, amb.auditor.registros, "nada auditado")

	// É erro de validação (422), nunca 500, e a mensagem não carrega nome nem
	// id — ela atravessa o log do handler.
	assert.True(t, transaction.IsValidationError(transaction.ErrCategoryIsParentGroup))
	assert.NotContains(t, transaction.ErrCategoryIsParentGroup.Error(), grupoComFilha)
	assert.NotContains(t, transaction.ErrCategoryIsParentGroup.Error(), "Alimentação")
}

// Os casos que CONTINUAM valendo: grupo sem filhas recebe, e o grupo cuja
// única filha foi arquivada (ou excluída) volta a receber. É a mesma fronteira
// da §12, que solta as palavras-chave do grupo pelo mesmo critério.
func TestUpdateCategoryAceitaGrupoSemFilhaAtiva(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome  string
		monta func(a *ambiente)
	}{
		{"grupo sem filha nenhuma", func(*ambiente) {}},
		{"filha arquivada", func(a *ambiente) {
			a.filha(minhaCasa, filhaAtiva, "Restaurantes", grupoSozinho, true, false)
		}},
		{"filha excluída", func(a *ambiente) {
			a.filha(minhaCasa, filhaAtiva, "Restaurantes", grupoSozinho, false, true)
		}},
		{"filha ativa de OUTRO grupo", func(a *ambiente) {
			a.categoria(minhaCasa, grupoComFilha, "Alimentação", category.KindExpense)
			a.filha(minhaCasa, filhaAtiva, "Restaurantes", grupoComFilha, false, false)
		}},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			amb := novoAmbiente(t)
			amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
			amb.categoria(minhaCasa, grupoSozinho, "Assinaturas", category.KindExpense)
			c.monta(amb)
			alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Streaming", 30_00, 3)

			view, err := amb.svc.UpdateCategory(t.Context(), ator(minhaCasa), alvo.ID, grupoSozinho)
			require.NoError(t, err)
			require.NotNil(t, view.CategoryID)
			assert.Equal(t, grupoSozinho, *view.CategoryID)
		})
	}
}

// A filha de outra CASA não conta: o repositório recebe o household do token e
// nunca vê a árvore da vizinha. A prova é dupla — a escrita passa, e a
// consulta de filhas foi feita pela casa certa.
func TestGrupoNaoHerdaFilhaDaCasaVizinha(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, grupoSozinho, "Assinaturas", category.KindExpense)
	// A vizinha tem um grupo com o MESMO id de pai — cenário impossível na
	// prática, e exatamente por isso é o que o teste força.
	amb.categorias.add(category.Category{
		ID: filhaAtiva, HouseholdID: outraCasa, ParentID: ptr(grupoSozinho),
		Name: "Da vizinha", NameNorm: "da vizinha", Kind: category.KindExpense,
	})
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Streaming", 30_00, 3)

	_, err := amb.svc.UpdateCategory(t.Context(), ator(minhaCasa), alvo.ID, grupoSozinho)
	require.NoError(t, err, "filha da vizinha não bloqueia o grupo desta casa")
}

// FOLHA não consulta subcategoria: a árvore tem dois níveis, e quem tem pai
// não tem filha. É o que mantém o caminho comum do PATCH com UMA consulta de
// categoria, como antes da §13.
func TestCategoriaFolhaNaoConsultaSubcategorias(t *testing.T) {
	t.Parallel()

	amb, alvo := cenarioDeGrupos(t)

	_, err := amb.svc.UpdateCategory(t.Context(), ator(minhaCasa), alvo.ID, filhaAtiva)
	require.NoError(t, err)
	assert.Empty(t, amb.categorias.filhasPedidas, "folha não pergunta por filhas")
}

func TestCreateBatchRecusaGrupoComSubcategoriaAtiva(t *testing.T) {
	t.Parallel()

	amb, _ := cenarioDeGrupos(t)

	l := linha("acc-1", 45_90, 3, "a")
	l.CategoryID = ptr(grupoComFilha)
	_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(l))
	require.ErrorIs(t, err, transaction.ErrCategoryIsParentGroup)

	// O lote inteiro volta atrás: nenhuma linha nova no repositório.
	for _, gravada := range amb.repo.linhas {
		assert.NotEqual(t, "Padaria Exemplo", gravada.Description, "nada gravado")
	}
}

// Sem N+1: um lote de 200 linhas apontando o MESMO grupo consulta as filhas
// UMA vez; dois grupos distintos, duas vezes. O confirm de um extrato inteiro
// não pode virar uma consulta por linha.
func TestCreateBatchConsultaFilhasUmaVezPorCategoriaDistinta(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, grupoSozinho, "Assinaturas", category.KindExpense)
	amb.categoria(minhaCasa, grupoDeReceita, "Rendimentos", category.KindIncome)
	amb.categoria(minhaCasa, grupoComFilha, "Alimentação", category.KindExpense)
	amb.filha(minhaCasa, filhaAtiva, "Restaurantes", grupoComFilha, false, false)

	linhas := make([]transaction.NewTransaction, 0, 200)
	for i := range 200 {
		l := linha("acc-1", 10_00, 3, fmt.Sprintf("%x", i))
		switch i % 3 {
		case 0:
			l.CategoryID = ptr(grupoSozinho)
		case 1:
			// FOLHA: não consulta nada.
			l.CategoryID = ptr(filhaAtiva)
		case 2:
			l.Kind = transaction.KindIncome
			l.CategoryID = ptr(grupoDeReceita)
		}
		linhas = append(linhas, l)
	}

	_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(linhas...))
	require.NoError(t, err)
	assert.Equal(t, map[string]int{grupoSozinho: 1, grupoDeReceita: 1}, amb.categorias.filhasPedidas)
}

// A §13 não migra nem altera o que já foi gravado: a linha que já está
// pendurada no grupo continua lá, e continua saindo na leitura.
func TestLancamentoJaGravadoEmGrupoContinuaIntacto(t *testing.T) {
	t.Parallel()

	amb, _ := cenarioDeGrupos(t)
	antigo := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 80_00, Description: "Feira antiga", DescriptionNorm: "feira antiga",
		CategoryID:      ptr(grupoComFilha),
		OccurredOn:      civil.MustNew(2026, 9, 3),
		CompetenceMonth: "2026-09",
	})

	view, err := amb.svc.ByID(t.Context(), ator(minhaCasa), antigo.ID)
	require.NoError(t, err)
	require.NotNil(t, view.CategoryID)
	assert.Equal(t, grupoComFilha, *view.CategoryID, "nada foi migrado")

	pagina, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)
	achou := false
	for _, item := range pagina.Items {
		if item.ID == antigo.ID {
			achou = true
		}
	}
	assert.True(t, achou, "a linha antiga continua na listagem")
}

// Reenviar a categoria que JÁ está gravada não é exceção: a linha antiga
// continua no grupo (a §13 não migra nada), mas qualquer PATCH que reafirme
// aquele grupo é 422 — a mesma ordem que já valia para categoria ARQUIVADA, em
// que o atalho de "nada mudou" também vem depois das recusas. Assim não existe
// corpo nenhum que devolva 200 apontando para grupo com filha ativa.
func TestUpdateCategoryRepetindoOGrupoJaGravadoTambemEhRecusado(t *testing.T) {
	t.Parallel()

	amb, _ := cenarioDeGrupos(t)
	antigo := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 80_00, Description: "Feira antiga", DescriptionNorm: "feira antiga",
		CategoryID:      ptr(grupoComFilha),
		OccurredOn:      civil.MustNew(2026, 9, 3),
		CompetenceMonth: "2026-09",
	})

	_, err := amb.svc.UpdateCategory(t.Context(), ator(minhaCasa), antigo.ID, grupoComFilha)
	require.ErrorIs(t, err, transaction.ErrCategoryIsParentGroup)
	require.NotNil(t, amb.repo.linhas[antigo.ID].CategoryID)
	assert.Equal(t, grupoComFilha, *amb.repo.linhas[antigo.ID].CategoryID, "o que já estava gravado não muda")
	assert.Empty(t, amb.auditor.registros)
}

// categoriasQueVazam é a fonte de categorias DEFEITUOSA: devolve as filhas de
// qualquer casa, ignorando o household do argumento. Existe para provar a
// defesa em profundidade do serviço — se um dia o repositório escorregar, a
// filha da vizinha ainda não decide o que esta casa pode gravar.
type categoriasQueVazam struct {
	*categoriasFake
}

func (c *categoriasQueVazam) Children(_ context.Context, _, parentID string) ([]category.Category, error) {
	var out []category.Category
	for _, k := range c.linhas {
		if k.ParentID != nil && *k.ParentID == parentID {
			out = append(out, k)
		}
	}
	return out, nil
}

func TestFilhaVazadaDeOutraCasaNaoBloqueiaAEscritaDestaCasa(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, grupoSozinho, "Assinaturas", category.KindExpense)
	amb.categorias.add(category.Category{
		ID: filhaAtiva, HouseholdID: outraCasa, ParentID: ptr(grupoSozinho),
		Name: "Da vizinha", NameNorm: "da vizinha", Kind: category.KindExpense,
	})
	alvo := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Streaming", 30_00, 3)

	vazada := &categoriasQueVazam{categoriasFake: amb.categorias}
	svc := transaction.NewService(amb.repo, amb.contas, vazada, amb.faturas, txDireto{},
		classify.NewLoader(vazada, amb.contas),
		transaction.WithIDs(amb.repo.proximoID),
		transaction.WithClock(func() time.Time { return agora }),
		transaction.WithAudit(amb.auditor),
	)

	_, err := svc.UpdateCategory(t.Context(), ator(minhaCasa), alvo.ID, grupoSozinho)
	require.NoError(t, err, "filha vazada de outra casa não pode recusar a escrita desta")
}
