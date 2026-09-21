package category_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Emenda §12 da spec 0005 (achado do QA da E2c): grupo com subcategoria ATIVA
// não recebe palavra-chave — lista não vazia é 400 `VALIDATION_FAILED` em
// `fields.keywords`. `[]` continua limpando; grupo cuja única filha está
// arquivada ou excluída segue aceitando; folha nunca é afetada. Caso residual
// aceito: grupo que JÁ tinha palavras e depois ganha uma filha mantém as
// palavras inertes, e GET/List continuam devolvendo-as.

// --- serviço ---------------------------------------------------------------

func TestGrupoComFilhaAtivaRecusaPalavrasChaveSemGravarNada(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Alimentação", category.KindExpense)
	criarFolha(t, svc, minhaCasa, "Restaurantes", grupo.ID)

	_, err := svc.Update(ctx, ator(minhaCasa), grupo.ID, category.UpdateInput{Keywords: palavras("mercado")})
	require.ErrorIs(t, err, category.ErrKeywordsOnGroupWithChildren)
	assert.True(t, category.IsValidationError(err), "é entrada do usuário, não falha do servidor")
	// A mensagem nunca carrega a palavra: o erro passa pelo log do handler.
	assert.NotContains(t, err.Error(), "mercado")

	// Nada foi gravado: nem palavra, nem a categoria mudou.
	assert.Empty(t, repo.palavras[grupo.ID])
	lida, err := svc.Get(ctx, ator(minhaCasa), grupo.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{}, lida.Keywords)

	// A regra é sobre a LISTA junto com outra edição também: nome + palavras
	// no mesmo PATCH é recusado por inteiro (transação), e o nome não muda.
	nome := "Comida"
	_, err = svc.Update(ctx, ator(minhaCasa), grupo.ID, category.UpdateInput{Name: &nome, Keywords: palavras("mercado")})
	require.ErrorIs(t, err, category.ErrKeywordsOnGroupWithChildren)
	lida, err = svc.Get(ctx, ator(minhaCasa), grupo.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alimentação", lida.Name)
}

func TestGrupoSemFilhaAtivaAceitaPalavrasChave(t *testing.T) {
	t.Parallel()

	svc := novoServico(t, novoRepo())
	ctx := t.Context()

	// Única filha ARQUIVADA: o grupo volta a receber lançamento, então
	// recebe palavra.
	arquivada := criarGrupo(t, svc, minhaCasa, "Saúde", category.KindExpense)
	filha := criarFolha(t, svc, minhaCasa, "Farmácia", arquivada.ID)
	_, err := svc.Archive(ctx, ator(minhaCasa), filha.ID)
	require.NoError(t, err)
	v, err := svc.Update(ctx, ator(minhaCasa), arquivada.ID, category.UpdateInput{Keywords: palavras("farmácia")})
	require.NoError(t, err)
	assert.Equal(t, []string{"farmácia"}, v.Keywords)

	// Única filha EXCLUÍDA: idem.
	excluida := criarGrupo(t, svc, minhaCasa, "Lazer", category.KindExpense)
	filha = criarFolha(t, svc, minhaCasa, "Cinema", excluida.ID)
	require.NoError(t, svc.Delete(ctx, ator(minhaCasa), filha.ID))
	v, err = svc.Update(ctx, ator(minhaCasa), excluida.ID, category.UpdateInput{Keywords: palavras("cinema")})
	require.NoError(t, err)
	assert.Equal(t, []string{"cinema"}, v.Keywords)

	// Grupo sem filha nenhuma: já coberto pelos testes de palavra-chave, mas
	// fica aqui como o contraste explícito da regra.
	solto := criarGrupo(t, svc, minhaCasa, "Transporte", category.KindExpense)
	v, err = svc.Update(ctx, ator(minhaCasa), solto.ID, category.UpdateInput{Keywords: palavras("uber")})
	require.NoError(t, err)
	assert.Equal(t, []string{"uber"}, v.Keywords)
}

func TestGrupoComFilhaAtivaAindaLimpaAsPalavrasComListaVazia(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	// Caso residual: o grupo ganhou as palavras ANTES da filha. A criação da
	// filha não é recusada por isso, e as palavras ficam — inertes.
	grupo := criarGrupoComPalavras(t, svc, minhaCasa, "Alimentação", "mercado", "padaria")
	folha, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{Name: "Restaurantes", ParentID: &grupo.ID})
	require.NoError(t, err, "criar a filha de um grupo com palavras não é recusado")
	require.NotNil(t, folha.ParentID)

	// GET e List continuam devolvendo as palavras inertes: é o que a tela usa
	// para mostrar a nota com a contagem.
	lida, err := svc.Get(ctx, ator(minhaCasa), grupo.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"mercado", "padaria"}, lida.Keywords)
	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)
	require.Len(t, arvore.Expense, 1)
	assert.Equal(t, []string{"mercado", "padaria"}, arvore.Expense[0].Keywords)

	// Trocar a lista é recusado (a filha está ativa)...
	_, err = svc.Update(ctx, ator(minhaCasa), grupo.ID, category.UpdateInput{Keywords: palavras("feira")})
	require.ErrorIs(t, err, category.ErrKeywordsOnGroupWithChildren)
	assert.Len(t, repo.palavras[grupo.ID], 2, "a lista antiga continua intacta")

	// ...mas LIMPAR continua aceito: é o caminho para mover as palavras à mão.
	v, err := svc.Update(ctx, ator(minhaCasa), grupo.ID, category.UpdateInput{Keywords: palavras()})
	require.NoError(t, err)
	assert.Equal(t, []string{}, v.Keywords)
	assert.Empty(t, repo.palavras[grupo.ID])

	// E a palavra liberada pode ir para a subcategoria.
	v, err = svc.Update(ctx, ator(minhaCasa), folha.ID, category.UpdateInput{Keywords: palavras("mercado")})
	require.NoError(t, err)
	assert.Equal(t, []string{"mercado"}, v.Keywords)
}

func TestFolhaNaoEhAfetadaPelaRegraDoGrupo(t *testing.T) {
	t.Parallel()

	svc := novoServico(t, novoRepo())
	ctx := t.Context()

	grupo := criarGrupo(t, svc, minhaCasa, "Alimentação", category.KindExpense)
	folha := criarFolha(t, svc, minhaCasa, "Restaurantes", grupo.ID)
	outra := criarFolha(t, svc, minhaCasa, "Padarias", grupo.ID)

	// Folha aceita palavra mesmo tendo "irmãs" ativas — a regra é sobre o
	// GRUPO ter filhas, não sobre a folha ter irmãs.
	v, err := svc.Update(ctx, ator(minhaCasa), folha.ID, category.UpdateInput{Keywords: palavras("ifood", "restaurante")})
	require.NoError(t, err)
	assert.Equal(t, []string{"ifood", "restaurante"}, v.Keywords)
	v, err = svc.Update(ctx, ator(minhaCasa), outra.ID, category.UpdateInput{Keywords: palavras("padaria")})
	require.NoError(t, err)
	assert.Equal(t, []string{"padaria"}, v.Keywords)
}

// --- API ---------------------------------------------------------------------

func TestPatchKeywordsEmGrupoComFilhaAtivaEh400EmFieldsKeywords(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	grupo := criarGrupo(t, amb.svc, minhaCasa, "Alimentação", category.KindExpense)
	criarFolha(t, amb.svc, minhaCasa, "Restaurantes", grupo.ID)

	rec := amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+grupo.ID,
		`{"keywords":["mercado"]}`, amb.handler.Update, grupo.ID)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, "VALIDATION_FAILED", codigo)
	assert.Equal(t, category.MsgPalavrasChaveNoGrupo, campos["keywords"])
	assert.Equal(t, "Palavras-chave ficam nas subcategorias.", campos["keywords"], "texto exato da spec §12")
	// A resposta não ecoa a palavra.
	assert.NotContains(t, rec.Body.String(), "mercado")

	// Nada foi gravado.
	rec = amb.chamar(t, minhaCasa, http.MethodGet, "/categories/"+grupo.ID, "", amb.handler.Get, grupo.ID)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"keywords":[]`)

	// `[]` (limpar) continua 2xx no mesmo grupo.
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+grupo.ID,
		`{"keywords":[]}`, amb.handler.Update, grupo.ID)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestPatchKeywordsEmGrupoCujaUnicaFilhaEstaArquivadaEh2xx(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	grupo := criarGrupo(t, amb.svc, minhaCasa, "Saúde", category.KindExpense)
	filha := criarFolha(t, amb.svc, minhaCasa, "Farmácia", grupo.ID)

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories/"+filha.ID+"/archive", "", amb.handler.Archive, filha.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+grupo.ID,
		`{"keywords":["farmácia","drogaria"]}`, amb.handler.Update, grupo.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var v category.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, []string{"farmácia", "drogaria"}, v.Keywords)

	// Desarquivar a filha faz o grupo voltar a recusar lista não vazia; as
	// palavras já gravadas ficam (inertes) e o GET do grupo as devolve.
	rec = amb.chamar(t, minhaCasa, http.MethodPost, "/categories/"+filha.ID+"/unarchive", "", amb.handler.Unarchive, filha.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+grupo.ID,
		`{"keywords":["farmácia"]}`, amb.handler.Update, grupo.ID)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	rec = amb.chamar(t, minhaCasa, http.MethodGet, "/categories/"+grupo.ID, "", amb.handler.Get, grupo.ID)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, []string{"farmácia", "drogaria"}, v.Keywords, "GET de grupo com filhas ainda devolve as palavras inertes")
}

func TestPostFolhaComKeywordsSobGrupoComIrmasEh2xx(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	grupo := criarGrupo(t, amb.svc, minhaCasa, "Alimentação", category.KindExpense)
	criarFolha(t, amb.svc, minhaCasa, "Restaurantes", grupo.ID)

	// Folha nova, com palavras, num grupo que já tem filha: a regra é do
	// grupo, e a folha passa.
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		fmt.Sprintf(`{"name":"Padarias","parentId":%q,"keywords":["padaria"]}`, grupo.ID), amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var v category.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, []string{"padaria"}, v.Keywords)
}
