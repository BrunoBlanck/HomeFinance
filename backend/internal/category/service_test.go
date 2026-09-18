package category_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	minhaCasa = "casa-1"
	outraCasa = "casa-2"
)

// ator monta quem age. Casa e usuário vêm do token em produção; aqui basta a
// casa, que é o que as regras deste pacote usam.
func ator(casa string) category.Actor {
	return category.Actor{HouseholdID: casa, UserID: "user-1", IP: "203.0.113.10"}
}

// --- dublês ---------------------------------------------------------------

// repoFake mantém o contrato de escopo do repositório real: nada sai sem bater
// household_id, e o `parent_id IS NULL` dos grupos é respeitado.
type repoFake struct {
	linhas   map[string]category.Category
	ordemIDs []string
	proximo  int

	// palavras guarda as palavras-chave por categoria (schema v4). O dublê
	// mantém os dois contratos do repositório real: escopo por casa e norm
	// ÚNICA por casa entre categorias diferentes.
	palavras map[string][]category.Keyword

	// Ganchos dos testes de palavra-chave (spec 0005):
	//   - listagens conta as chamadas a ListKeywords — a prova de "uma
	//     consulta por resposta, nunca N+1";
	//   - ocultarDonasUmaVez faz a PRÓXIMA KeywordOwners devolver vazio,
	//     simulando a corrida em que a outra edição ainda não tinha
	//     confirmado na pré-checagem, mas já confirmou quando o índice
	//     único decide em ReplaceKeywords;
	//   - erroPalavras, quando preenchido, é devolvido por ReplaceKeywords
	//     (falha de infraestrutura);
	//   - colisoesForcadas faz as PRÓXIMAS N chamadas a ReplaceKeywords
	//     devolverem ErrKeywordTaken cru sem gravar nada — é o índice único
	//     recusando por uma linha de transação concorrente que a
	//     pré-checagem não viu E que já sumiu quando o serviço reconsulta a
	//     dona (a "dona sumida" de escreverComPalavras);
	//   - substituicoes conta as chamadas a ReplaceKeywords — a prova de
	//     "uma segunda tentativa, nunca uma terceira".
	listagens          int
	ocultarDonasUmaVez bool
	erroPalavras       error
	colisoesForcadas   int
	substituicoes      int
}

func novoRepo() *repoFake {
	return &repoFake{
		linhas:   map[string]category.Category{},
		palavras: map[string][]category.Keyword{},
	}
}

func (r *repoFake) Create(_ context.Context, c *category.Category) error {
	r.linhas[c.ID] = *c
	r.ordemIDs = append(r.ordemIDs, c.ID)
	return nil
}

func (r *repoFake) ByID(_ context.Context, householdID, id string) (*category.Category, error) {
	c, ok := r.linhas[id]
	if !ok || c.HouseholdID != householdID || c.DeletedAt != nil {
		return nil, category.ErrNotFound
	}
	copia := c
	return &copia, nil
}

func (r *repoFake) List(_ context.Context, householdID string, includeArchived bool) ([]category.Category, error) {
	var out []category.Category
	for _, id := range r.ordemIDs {
		c := r.linhas[id]
		if c.HouseholdID != householdID || c.DeletedAt != nil {
			continue
		}
		if !includeArchived && c.ArchivedAt != nil {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *repoFake) Children(_ context.Context, householdID, parentID string) ([]category.Category, error) {
	var out []category.Category
	for _, id := range r.ordemIDs {
		c := r.linhas[id]
		if c.HouseholdID != householdID || c.DeletedAt != nil || c.ParentID == nil {
			continue
		}
		if *c.ParentID == parentID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *repoFake) Update(_ context.Context, c *category.Category) error {
	atual, ok := r.linhas[c.ID]
	if !ok || atual.HouseholdID != c.HouseholdID || atual.DeletedAt != nil {
		return category.ErrNotFound
	}
	// O repositório real deixa parent_id FORA do SET; o dublê reproduz isso,
	// senão um teste passaria aqui e falharia contra o banco.
	atualizado := *c
	atualizado.ParentID = atual.ParentID
	r.linhas[c.ID] = atualizado
	return nil
}

func (r *repoFake) SoftDelete(_ context.Context, householdID, id string, at time.Time) error {
	c, ok := r.linhas[id]
	if !ok || c.HouseholdID != householdID || c.DeletedAt != nil {
		return category.ErrNotFound
	}
	c.DeletedAt = &at
	r.linhas[id] = c
	return nil
}

func (r *repoFake) CountAll(_ context.Context, householdID string) (int64, error) {
	var total int64
	for _, c := range r.linhas {
		if c.HouseholdID == householdID && c.DeletedAt == nil {
			total++
		}
	}
	return total, nil
}

func (r *repoFake) ListKeywords(_ context.Context, householdID string) ([]category.Keyword, error) {
	r.listagens++
	var out []category.Keyword
	for _, id := range r.ordemIDs {
		for _, k := range r.palavras[id] {
			if k.HouseholdID == householdID {
				out = append(out, k)
			}
		}
	}
	return out, nil
}

func (r *repoFake) KeywordOwners(_ context.Context, householdID string, norms []string) (map[string]string, error) {
	out := map[string]string{}
	if r.ocultarDonasUmaVez {
		r.ocultarDonasUmaVez = false
		return out, nil
	}
	for _, lista := range r.palavras {
		for _, k := range lista {
			if k.HouseholdID != householdID {
				continue
			}
			for _, n := range norms {
				if n == k.Norm {
					out[n] = k.CategoryID
				}
			}
		}
	}
	return out, nil
}

func (r *repoFake) ReplaceKeywords(_ context.Context, householdID, categoryID string, kws []category.Keyword) error {
	r.substituicoes++
	if r.erroPalavras != nil {
		return r.erroPalavras
	}
	if r.colisoesForcadas > 0 {
		r.colisoesForcadas--
		return category.ErrKeywordTaken
	}
	// O índice único (household_id, keyword_norm) do banco, reproduzido.
	for dona, lista := range r.palavras {
		if dona == categoryID {
			continue
		}
		for _, k := range lista {
			if k.HouseholdID != householdID {
				continue
			}
			for _, nova := range kws {
				if nova.Norm == k.Norm {
					return category.ErrKeywordTaken
				}
			}
		}
	}
	novas := make([]category.Keyword, 0, len(kws))
	for _, k := range kws {
		k.HouseholdID = householdID
		k.CategoryID = categoryID
		novas = append(novas, k)
	}
	r.palavras[categoryID] = novas
	return nil
}

func (r *repoFake) DeleteKeywords(_ context.Context, householdID, categoryID string) error {
	restantes := r.palavras[categoryID][:0:0]
	for _, k := range r.palavras[categoryID] {
		if k.HouseholdID != householdID {
			restantes = append(restantes, k)
		}
	}
	r.palavras[categoryID] = restantes
	return nil
}

func (r *repoFake) NameTaken(_ context.Context, householdID string, parentID *string, nameNorm, exceptID string) (bool, error) {
	for _, c := range r.linhas {
		if c.HouseholdID != householdID || c.DeletedAt != nil || c.ArchivedAt != nil || c.ID == exceptID {
			continue
		}
		if c.NameNorm != nameNorm {
			continue
		}
		mesmoPai := (parentID == nil && c.ParentID == nil) ||
			(parentID != nil && c.ParentID != nil && *parentID == *c.ParentID)
		if mesmoPai {
			return true, nil
		}
	}
	return false, nil
}

func (r *repoFake) proximoID() string {
	r.proximo++
	return fmt.Sprintf("cat-%03d", r.proximo)
}

type txDireto struct{}

func (txDireto) Do(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

// txComRollback desfaz o estado do dublê quando a operação falha — o que o
// UnitOfWork real faz com a transação. Só os testes da segunda tentativa
// (escreverComPalavras) precisam disso: sem rollback, a categoria da primeira
// tentativa ficaria no dublê e a segunda tomaria ErrNameTaken, um artefato do
// teste que o banco real não produz.
//
// Clonar os mapas basta: o dublê nunca muda uma fatia no lugar, sempre
// atribui uma nova. Os ganchos (contadores, colisões) ficam fora do rollback
// de propósito — eles descrevem o que aconteceu, não o estado.
type txComRollback struct{ repo *repoFake }

func (t txComRollback) Do(ctx context.Context, fn func(context.Context) error) error {
	linhas := maps.Clone(t.repo.linhas)
	palavras := maps.Clone(t.repo.palavras)
	ordem := slices.Clone(t.repo.ordemIDs)
	if err := fn(ctx); err != nil {
		t.repo.linhas, t.repo.palavras, t.repo.ordemIDs = linhas, palavras, ordem
		return err
	}
	return nil
}

type usoFake struct {
	emUso bool
	err   error
}

func (u usoFake) CategoryInUse(context.Context, string, string) (bool, error) {
	return u.emUso, u.err
}

func novoServico(t *testing.T, repo *repoFake, opts ...category.Option) *category.Service {
	t.Helper()
	return novoServicoComTx(t, repo, txDireto{}, opts...)
}

func novoServicoComTx(t *testing.T, repo *repoFake, tx category.Transactor, opts ...category.Option) *category.Service {
	t.Helper()
	base := []category.Option{
		category.WithIDs(repo.proximoID),
		category.WithClock(func() time.Time { return time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC) }),
	}
	return category.NewService(repo, tx, append(base, opts...)...)
}

func criarGrupo(t *testing.T, svc *category.Service, casa, nome, kind string) category.View {
	t.Helper()
	v, err := svc.Create(t.Context(), ator(casa), category.CreateInput{Name: nome, Kind: kind})
	require.NoError(t, err)
	return v
}

func criarFolha(t *testing.T, svc *category.Service, casa, nome, paiID string) category.View {
	t.Helper()
	v, err := svc.Create(t.Context(), ator(casa), category.CreateInput{Name: nome, ParentID: &paiID})
	require.NoError(t, err)
	return v
}

// --- árvore de dois níveis ------------------------------------------------

func TestFolhaHerdaANaturezaDoGrupo(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)

	// O cliente manda "income" de propósito. O servidor IGNORA e usa a do pai
	// — não é erro, é o servidor mandando no campo (invariante 4).
	folha, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{
		Name:     "Energia",
		Kind:     category.KindIncome,
		ParentID: &grupo.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, category.KindExpense, folha.Kind, "a natureza vem do grupo, não do cliente")
}

func TestTerceiroNivelEhRecusado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	folha := criarFolha(t, svc, minhaCasa, "Energia", grupo.ID)

	// Pendurar na FOLHA seria o nível 3, que o ADR-017b proíbe — é ele que
	// obrigaria consulta recursiva e quebraria a portabilidade multi-banco.
	_, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{Name: "Bandeira", ParentID: &folha.ID})
	assert.ErrorIs(t, err, category.ErrTooDeep)
}

// parentId apontando para categoria de OUTRA casa precisa ser 404, e não um
// erro que revele que aquele id existe em algum lugar (S1).
func TestParentIdDeOutraCasaEhNotFound(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupoAlheio := criarGrupo(t, svc, outraCasa, "Moradia", category.KindExpense)

	_, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{Name: "Energia", ParentID: &grupoAlheio.ID})
	assert.ErrorIs(t, err, category.ErrNotFound)
}

func TestParentIdInexistenteEhNotFound(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	inexistente := "cat-nao-existe"

	_, err := svc.Create(t.Context(), ator(minhaCasa), category.CreateInput{Name: "Energia", ParentID: &inexistente})
	assert.ErrorIs(t, err, category.ErrNotFound)
}

func TestGrupoExigeNaturezaValida(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	for _, kind := range []string{"", "receita", "INCOME", "expense ", "outro"} {
		_, err := svc.Create(t.Context(), ator(minhaCasa), category.CreateInput{Name: "X " + kind, Kind: kind})
		assert.ErrorIs(t, err, category.ErrInvalidKind, "kind %q deveria ser recusado", kind)
	}
}

func TestListaMontaAArvoreSeparadaPorNatureza(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	moradia := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	criarFolha(t, svc, minhaCasa, "Energia", moradia.ID)
	criarFolha(t, svc, minhaCasa, "Água", moradia.ID)
	criarGrupo(t, svc, minhaCasa, "Salário", category.KindIncome)

	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)

	require.Len(t, arvore.Expense, 1)
	require.Len(t, arvore.Income, 1)
	assert.Equal(t, "Moradia", arvore.Expense[0].Name)
	assert.Equal(t, "Salário", arvore.Income[0].Name)

	// As filhas vêm DENTRO do grupo, nunca soltas no primeiro nível.
	require.Len(t, arvore.Expense[0].Children, 2)
	assert.Equal(t, []string{"Energia", "Água"},
		[]string{arvore.Expense[0].Children[0].Name, arvore.Expense[0].Children[1].Name})
	assert.Empty(t, arvore.Income[0].Children, "grupo sem filha vem com lista vazia, não nula")
}

func TestFiltroPorNaturezaEhValidado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	criarGrupo(t, svc, minhaCasa, "Salário", category.KindIncome)

	so, err := svc.List(ctx, ator(minhaCasa), category.ListInput{Kind: category.KindExpense})
	require.NoError(t, err)
	assert.Len(t, so.Expense, 1)
	assert.Empty(t, so.Income)

	// Filtro inválido é ERRO, não filtro ignorado: ignorar faria a tela
	// mostrar um conjunto diferente do que ela pediu (S4).
	_, err = svc.List(ctx, ator(minhaCasa), category.ListInput{Kind: "receita"})
	assert.ErrorIs(t, err, category.ErrInvalidKind)
}

func TestCategoriaDeOutraCasaNaoApareceNaArvore(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	criarGrupo(t, svc, outraCasa, "Moradia", category.KindExpense)

	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)
	assert.Empty(t, arvore.Expense)
	assert.Empty(t, arvore.Income)
}

// --- unicidade ------------------------------------------------------------

func TestUnicidadeEhEntreIrmaos(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	moradia := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	lazer := criarGrupo(t, svc, minhaCasa, "Lazer", category.KindExpense)
	criarFolha(t, svc, minhaCasa, "Assinaturas", moradia.ID)

	// Mesmo nome no MESMO pai: recusa.
	_, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{Name: "assinaturas", ParentID: &moradia.ID})
	assert.ErrorIs(t, err, category.ErrNameTaken)

	// Mesmo nome em OUTRO pai: passa.
	_, err = svc.Create(ctx, ator(minhaCasa), category.CreateInput{Name: "Assinaturas", ParentID: &lazer.ID})
	assert.NoError(t, err)

	// E como GRUPO também passa — grupo e folha não são irmãos.
	_, err = svc.Create(ctx, ator(minhaCasa), category.CreateInput{Name: "Assinaturas", Kind: category.KindExpense})
	assert.NoError(t, err)
}

func TestGrupoDuplicadoEhRecusado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)

	_, err := svc.Create(t.Context(), ator(minhaCasa), category.CreateInput{
		Name: "  MORADIA ", Kind: category.KindIncome,
	})
	assert.ErrorIs(t, err, category.ErrNameTaken,
		"a colisão é por nome normalizado, independente da natureza")
}

func TestNomeInvalidoEhRecusado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	for _, nome := range []string{"", "   ", "\t"} {
		_, err := svc.Create(t.Context(), ator(minhaCasa), category.CreateInput{Name: nome, Kind: category.KindExpense})
		assert.ErrorIs(t, err, category.ErrInvalidName, "nome %q", nome)
	}

	_, err := svc.Create(t.Context(), ator(minhaCasa), category.CreateInput{
		Name: strings.Repeat("á", category.MaxNameLen+1), Kind: category.KindExpense,
	})
	assert.ErrorIs(t, err, category.ErrInvalidName)
}

func TestTetoDeCategoriasPorCasa(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	for i := range category.MaxPerHousehold {
		_, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{
			Name: fmt.Sprintf("Grupo %03d", i), Kind: category.KindExpense,
		})
		require.NoError(t, err)
	}

	_, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{Name: "Excedente", Kind: category.KindExpense})
	assert.ErrorIs(t, err, category.ErrTooMany)
}

// --- natureza -------------------------------------------------------------

func TestNaturezaDeGrupoLivreMuda(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Bicos", category.KindExpense)

	novo := category.KindIncome
	atualizado, err := svc.Update(ctx, ator(minhaCasa), grupo.ID, category.UpdateInput{Kind: &novo})
	require.NoError(t, err)
	assert.Equal(t, category.KindIncome, atualizado.Kind)
}

func TestNaturezaTravaComFilhas(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	criarFolha(t, svc, minhaCasa, "Energia", grupo.ID)

	novo := category.KindIncome
	_, err := svc.Update(ctx, ator(minhaCasa), grupo.ID, category.UpdateInput{Kind: &novo})
	assert.ErrorIs(t, err, category.ErrKindLocked,
		"mudar o grupo deixaria as filhas com natureza divergente")
}

func TestNaturezaTravaComUso(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo, category.WithUsageCheckers(usoFake{emUso: true}))
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)

	novo := category.KindIncome
	_, err := svc.Update(ctx, ator(minhaCasa), grupo.ID, category.UpdateInput{Kind: &novo})
	assert.ErrorIs(t, err, category.ErrKindLocked,
		"transformaria despesa registrada em receita e mudaria meses fechados")
}

func TestNaturezaDeFolhaNaoMudaSozinha(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	folha := criarFolha(t, svc, minhaCasa, "Energia", grupo.ID)

	novo := category.KindIncome
	_, err := svc.Update(ctx, ator(minhaCasa), folha.ID, category.UpdateInput{Kind: &novo})
	assert.ErrorIs(t, err, category.ErrKindLocked, "a folha herda; ela não decide sozinha")
}

// --- arquivamento ---------------------------------------------------------

func TestArquivarGrupoArquivaAsFilhas(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	energia := criarFolha(t, svc, minhaCasa, "Energia", grupo.ID)
	agua := criarFolha(t, svc, minhaCasa, "Água", grupo.ID)

	_, err := svc.Archive(ctx, ator(minhaCasa), grupo.ID)
	require.NoError(t, err)

	// Grupo invisível com filha visível é um estado que a tela não sabe
	// desenhar; por isso a cascata, e por isso ela é transacional.
	for _, id := range []string{energia.ID, agua.ID} {
		filha, err := svc.Get(ctx, ator(minhaCasa), id)
		require.NoError(t, err)
		assert.NotNil(t, filha.ArchivedAt, "filha %s deveria estar arquivada", filha.Name)
	}

	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)
	assert.Empty(t, arvore.Expense)
}

func TestDesarquivarGrupoNaoDesarquivaAsFilhas(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	energia := criarFolha(t, svc, minhaCasa, "Energia", grupo.ID)

	_, err := svc.Archive(ctx, ator(minhaCasa), grupo.ID)
	require.NoError(t, err)
	_, err = svc.Unarchive(ctx, ator(minhaCasa), grupo.ID)
	require.NoError(t, err)

	// Restaurar tudo apagaria a escolha de quem arquivou uma filha antes, por
	// motivo próprio. Quem quiser a filha de volta a desarquiva.
	filha, err := svc.Get(ctx, ator(minhaCasa), energia.ID)
	require.NoError(t, err)
	assert.NotNil(t, filha.ArchivedAt)
}

func TestDesarquivarFolhaExigeGrupoAtivo(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	energia := criarFolha(t, svc, minhaCasa, "Energia", grupo.ID)

	_, err := svc.Archive(ctx, ator(minhaCasa), grupo.ID)
	require.NoError(t, err)

	_, err = svc.Unarchive(ctx, ator(minhaCasa), energia.ID)
	assert.ErrorIs(t, err, category.ErrParentArchived)

	// Com o grupo de volta, a folha volta.
	_, err = svc.Unarchive(ctx, ator(minhaCasa), grupo.ID)
	require.NoError(t, err)
	voltou, err := svc.Unarchive(ctx, ator(minhaCasa), energia.ID)
	require.NoError(t, err)
	assert.Nil(t, voltou.ArchivedAt)
}

func TestArquivarEhIdempotente(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)

	primeira, err := svc.Archive(ctx, ator(minhaCasa), grupo.ID)
	require.NoError(t, err)
	segunda, err := svc.Archive(ctx, ator(minhaCasa), grupo.ID)
	require.NoError(t, err)
	assert.Equal(t, *primeira.ArchivedAt, *segunda.ArchivedAt)
}

// --- exclusão -------------------------------------------------------------

// Critério de aceite 2 da spec 0003, e o único "em uso" que já existe na E1.
func TestExcluirGrupoComFilhasEhRecusado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	folha := criarFolha(t, svc, minhaCasa, "Energia", grupo.ID)

	assert.ErrorIs(t, svc.Delete(ctx, ator(minhaCasa), grupo.ID), category.ErrInUse)

	// Sem a filha, o grupo sai.
	require.NoError(t, svc.Delete(ctx, ator(minhaCasa), folha.ID))
	assert.NoError(t, svc.Delete(ctx, ator(minhaCasa), grupo.ID))
}

// Filha ARQUIVADA continua sendo filha. Se a exclusão só olhasse as ativas, o
// grupo sairia e a filha ficaria órfã — e sem FK física (ADR-013) o banco não
// barraria isso.
func TestExcluirGrupoComFilhaArquivadaTambemEhRecusado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	folha := criarFolha(t, svc, minhaCasa, "Energia", grupo.ID)
	_, err := svc.Archive(ctx, ator(minhaCasa), folha.ID)
	require.NoError(t, err)

	assert.ErrorIs(t, svc.Delete(ctx, ator(minhaCasa), grupo.ID), category.ErrInUse)
}

func TestExcluirCategoriaEmUsoEhRecusado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo, category.WithUsageCheckers(usoFake{emUso: true}))
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	assert.ErrorIs(t, svc.Delete(ctx, ator(minhaCasa), grupo.ID), category.ErrInUse)
}

func TestVerificadorDeUsoQueFalhaAbortaAExclusao(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo, category.WithUsageCheckers(usoFake{err: errors.New("timeout")}))
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)

	err := svc.Delete(ctx, ator(minhaCasa), grupo.ID)
	require.Error(t, err)
	assert.NotErrorIs(t, err, category.ErrInUse)
	assert.Nil(t, repo.linhas[grupo.ID].DeletedAt, "na dúvida, não exclui")
}

func TestExcluirCategoriaDeOutraCasaNaoAcontece(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	daOutra := criarGrupo(t, svc, outraCasa, "Moradia", category.KindExpense)

	assert.ErrorIs(t, svc.Delete(ctx, ator(minhaCasa), daOutra.ID), category.ErrNotFound)
	assert.Nil(t, repo.linhas[daOutra.ID].DeletedAt)
}

// --- semente --------------------------------------------------------------

func TestSementeCriaOsGruposIniciais(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)
	assert.Len(t, arvore.Expense, 10)
	assert.Len(t, arvore.Income, 2)

	nomes := make([]string, 0, 12)
	for _, g := range append(append([]category.View{}, arvore.Expense...), arvore.Income...) {
		nomes = append(nomes, g.Name)
		assert.Nil(t, g.ParentID, "a semente cria só grupos")
	}
	assert.Contains(t, nomes, "Moradia")
	assert.Contains(t, nomes, "Alimentação")
	assert.Contains(t, nomes, "Salário")
}

// EnsureDefault roda também no login, como auto-reparo. Sem idempotência, cada
// login duplicaria as doze categorias.
func TestSementeEhIdempotente(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))
	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))
	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	total, err := repo.CountAll(ctx, minhaCasa)
	require.NoError(t, err)
	// Contra a lista, e não contra um número escrito à mão: a semente cresce
	// (ADR-029a acrescentou "Investimentos" e "Resgates") e o que este teste
	// fixa é a IDEMPOTÊNCIA, não o tamanho.
	assert.EqualValues(t, len(category.DefaultGroups()), total)
}

// Nenhuma categoria da semente é "de sistema": todas se editam, arquivam e
// excluem como qualquer outra.
func TestCategoriaDaSementeEhEditavelEExcluivel(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))
	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)
	primeira := arvore.Expense[0]

	novoNome := "Casa e moradia"
	renomeada, err := svc.Update(ctx, ator(minhaCasa), primeira.ID, category.UpdateInput{Name: &novoNome})
	require.NoError(t, err)
	assert.Equal(t, "Casa e moradia", renomeada.Name)

	assert.NoError(t, svc.Delete(ctx, ator(minhaCasa), primeira.ID))
}

func TestSementeNaoVazaEntreCasas(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))
	require.NoError(t, svc.SeedDefaults(ctx, outraCasa))

	// Cada casa tem a SUA semente inteira — a idempotência é por casa, não
	// global.
	for _, casa := range []string{minhaCasa, outraCasa} {
		total, err := repo.CountAll(ctx, casa)
		require.NoError(t, err)
		assert.EqualValues(t, len(category.DefaultGroups()), total, "casa %s", casa)
	}
}
