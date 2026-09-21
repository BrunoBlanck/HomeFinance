package category_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Palavras-chave de categoria (spec 0005 §4.1, critério de aceite 2 e casos
// de abuso da §9 do plano). Os testes de serviço provam a regra; os de
// handler provam o CONTRATO (status, `fields.keywords[i]`, 409 com `ownerId`)
// e a promessa de segurança de que a palavra nunca aparece em log nem em
// auditoria.

func palavras(itens ...string) *[]string { return &itens }

func criarGrupoComPalavras(t *testing.T, svc *category.Service, casa, nome string, kws ...string) category.View {
	t.Helper()
	v, err := svc.Create(t.Context(), ator(casa), category.CreateInput{
		Name: nome, Kind: category.KindExpense, Keywords: kws,
	})
	require.NoError(t, err)
	return v
}

// --- serviço: gravação e leitura -------------------------------------------

func TestCriarComPalavrasChaveGravaNaOrdemEComOsCamposDoServidor(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	v := criarGrupoComPalavras(t, svc, minhaCasa, "Alimentação", "Supermercado Extra", "  Padaria  ", "Hortifrúti")

	// A forma EXIBÍVEL volta como foi digitada (espaços colapsados), na
	// ordem de cadastro.
	assert.Equal(t, []string{"Supermercado Extra", "Padaria", "Hortifrúti"}, v.Keywords)

	gravadas := repo.palavras[v.ID]
	require.Len(t, gravadas, 3)
	for i, k := range gravadas {
		// Casa, dona, id e posição são do SERVIDOR — nada disso veio do cliente (S2).
		assert.Equal(t, minhaCasa, k.HouseholdID)
		assert.Equal(t, v.ID, k.CategoryID)
		assert.NotEmpty(t, k.ID)
		assert.Equal(t, i, k.Position)
		assert.False(t, k.CreatedAt.IsZero())
	}
	// A norm é a forma de comparação: sem acento, sem caixa.
	assert.Equal(t, "supermercado extra", gravadas[0].Norm)
	assert.Equal(t, "hortifruti", gravadas[2].Norm)
}

func TestCriarSemPalavrasChaveDevolveListaVaziaNuncaNula(t *testing.T) {
	t.Parallel()

	svc := novoServico(t, novoRepo())
	v := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)

	require.NotNil(t, v.Keywords, "o campo é required no contrato: [] e não null")
	assert.Empty(t, v.Keywords)

	lida, err := svc.Get(t.Context(), ator(minhaCasa), v.ID)
	require.NoError(t, err)
	require.NotNil(t, lida.Keywords)
	assert.Empty(t, lida.Keywords)
}

func TestGetEListaTrazemAsPalavrasEmUmaConsultaPorResposta(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	grupo := criarGrupoComPalavras(t, svc, minhaCasa, "Alimentação", "mercado")
	folha, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{
		Name: "Restaurantes", ParentID: &grupo.ID, Keywords: []string{"ifood", "restaurante"},
	})
	require.NoError(t, err)
	criarGrupoComPalavras(t, svc, outraCasa, "Alimentação", "mercado da outra casa")

	repo.listagens = 0
	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)
	assert.Equal(t, 1, repo.listagens, "UMA consulta de palavras para a casa inteira, nunca N+1")

	require.Len(t, arvore.Expense, 1)
	assert.Equal(t, []string{"mercado"}, arvore.Expense[0].Keywords)
	require.Len(t, arvore.Expense[0].Children, 1)
	assert.Equal(t, []string{"ifood", "restaurante"}, arvore.Expense[0].Children[0].Keywords)

	repo.listagens = 0
	lida, err := svc.Get(ctx, ator(minhaCasa), folha.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, repo.listagens)
	assert.Equal(t, []string{"ifood", "restaurante"}, lida.Keywords)

	// A palavra da OUTRA casa não aparece em lugar nenhum (S1).
	corpo, err := json.Marshal(arvore)
	require.NoError(t, err)
	assert.NotContains(t, string(corpo), "outra casa")
}

// --- serviço: unicidade por casa (critério 2 da spec 0005) -----------------

func TestMesmaPalavraEmOutraCategoriaDaMesmaCasaEh409ComADona(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	alimentacao := criarGrupoComPalavras(t, svc, minhaCasa, "Alimentação", "Supermercado")

	// Na criação...
	_, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{
		Name: "Casa", Kind: category.KindExpense, Keywords: []string{"SUPERMERCADO"},
	})
	var tomada *category.KeywordTakenError
	require.ErrorAs(t, err, &tomada, "a comparação é pela forma normalizada")
	assert.Equal(t, alimentacao.ID, tomada.OwnerID, "a dona é a categoria da MESMA casa")
	assert.Equal(t, "SUPERMERCADO", tomada.Keyword, "a palavra devolvida é a que o cliente mandou")
	assert.ErrorIs(t, err, category.ErrKeywordTaken)
	assert.NotContains(t, err.Error(), "SUPERMERCADO", "a mensagem do erro vai para o log: sem a palavra")
	assert.NotContains(t, strings.ToLower(err.Error()), "supermercado")
	for dona, lista := range repo.palavras {
		if len(lista) > 0 {
			assert.Equal(t, alimentacao.ID, dona, "só a dona original tem palavras: a lista recusada não gravou nada")
		}
	}

	// ...e na edição.
	lazer := criarGrupo(t, svc, minhaCasa, "Lazer", category.KindExpense)
	_, err = svc.Update(ctx, ator(minhaCasa), lazer.ID, category.UpdateInput{Keywords: palavras("cinema", "supermercado")})
	require.ErrorAs(t, err, &tomada)
	assert.Equal(t, alimentacao.ID, tomada.OwnerID)
	assert.Empty(t, repo.palavras[lazer.ID], "a lista inteira é recusada: nem 'cinema' entra")
}

func TestMesmaPalavraEmOutraCasaNaoColide(t *testing.T) {
	t.Parallel()

	svc := novoServico(t, novoRepo())

	criarGrupoComPalavras(t, svc, outraCasa, "Alimentação", "supermercado")
	v := criarGrupoComPalavras(t, svc, minhaCasa, "Alimentação", "supermercado")
	assert.Equal(t, []string{"supermercado"}, v.Keywords)
}

func TestReenviarAsPropriasPalavrasNaoColideConsigoMesma(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	v := criarGrupoComPalavras(t, svc, minhaCasa, "Alimentação", "supermercado", "padaria")

	// A tela lê a lista e manda de volta com uma a mais — a pré-checagem
	// ignora a própria categoria (KeywordOwners inclui a dona em edição).
	atualizada, err := svc.Update(t.Context(), ator(minhaCasa), v.ID,
		category.UpdateInput{Keywords: palavras("supermercado", "padaria", "açougue")})
	require.NoError(t, err)
	assert.Equal(t, []string{"supermercado", "padaria", "açougue"}, atualizada.Keywords)
}

// A corrida: duas edições disputam a mesma palavra; a pré-checagem de ambas
// passa e o índice único decide. O serviço volta ao banco, fora da transação
// desfeita, para citar quem ganhou.
func TestCorridaDecididaPeloIndiceUnicoAindaCitaADona(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	vencedora := criarGrupoComPalavras(t, svc, minhaCasa, "Alimentação", "supermercado")
	perdedora := criarGrupo(t, svc, minhaCasa, "Casa", category.KindExpense)

	repo.ocultarDonasUmaVez = true // a pré-checagem ainda não vê a vencedora
	repo.substituicoes = 0
	_, err := svc.Update(ctx, ator(minhaCasa), perdedora.ID, category.UpdateInput{Keywords: palavras("supermercado")})

	var tomada *category.KeywordTakenError
	require.ErrorAs(t, err, &tomada)
	assert.Equal(t, vencedora.ID, tomada.OwnerID)
	assert.Empty(t, repo.palavras[perdedora.ID])
	assert.Equal(t, 1, repo.substituicoes, "com a dona encontrada não há segunda tentativa")
}

// A corrida em que a dona SOME: o índice único recusou por uma linha
// concorrente que já não existe quando o serviço reconsulta (a vencedora
// largou a palavra). A colisão desapareceu, e a operação inteira roda uma
// segunda vez — que passa.
func TestCorridaComDonaSumidaTentaDeNovoUmaVezEPassa(t *testing.T) {
	t.Parallel()

	t.Run("edição", func(t *testing.T) {
		t.Parallel()

		repo := novoRepo()
		svc := novoServico(t, repo)
		lazer := criarGrupo(t, svc, minhaCasa, "Lazer", category.KindExpense)

		repo.colisoesForcadas = 1
		v, err := svc.Update(t.Context(), ator(minhaCasa), lazer.ID, category.UpdateInput{Keywords: palavras("Cinema", "Teatro")})
		require.NoError(t, err)
		assert.Equal(t, []string{"Cinema", "Teatro"}, v.Keywords)
		assert.Equal(t, 2, repo.substituicoes, "exatamente uma segunda tentativa")
		assert.Equal(t, []string{"Cinema", "Teatro"}, []string{repo.palavras[lazer.ID][0].Keyword, repo.palavras[lazer.ID][1].Keyword})
	})

	t.Run("criação", func(t *testing.T) {
		t.Parallel()

		// Com rollback: a categoria da primeira tentativa não pode sobrar, senão
		// a segunda tomaria ErrNameTaken — coisa que o banco real não faz.
		repo := novoRepo()
		svc := novoServicoComTx(t, repo, txComRollback{repo: repo})

		repo.colisoesForcadas = 1
		v, err := svc.Create(t.Context(), ator(minhaCasa), category.CreateInput{
			Name: "Lazer", Kind: category.KindExpense, Keywords: []string{"Cinema"},
		})
		require.NoError(t, err)
		assert.Equal(t, []string{"Cinema"}, v.Keywords)
		assert.Equal(t, 2, repo.substituicoes)

		total, err := repo.CountAll(t.Context(), minhaCasa)
		require.NoError(t, err)
		assert.EqualValues(t, 1, total, "só a categoria da tentativa que passou existe")
		assert.Len(t, repo.palavras[v.ID], 1)
	})
}

// A corrida TRIPLA: colide de novo na segunda tentativa e de novo ninguém
// aparece como dona. Não há terceira tentativa, e o 409 sai com a palavra —
// o contrato exige `fields.keyword` — mas sem dona, que o serviço não tem
// como saber (melhor esforço: a primeira palavra enviada).
func TestCorridaTriplaDevolve409ComAPalavraESemDona(t *testing.T) {
	t.Parallel()

	t.Run("edição", func(t *testing.T) {
		t.Parallel()

		repo := novoRepo()
		svc := novoServico(t, repo)
		lazer := criarGrupo(t, svc, minhaCasa, "Lazer", category.KindExpense)

		repo.colisoesForcadas = 2
		_, err := svc.Update(t.Context(), ator(minhaCasa), lazer.ID, category.UpdateInput{Keywords: palavras("Cinema", "Teatro")})

		var tomada *category.KeywordTakenError
		require.ErrorAs(t, err, &tomada, "nunca um ErrKeywordTaken cru: o handler precisa da palavra")
		assert.Equal(t, "Cinema", tomada.Keyword, "a primeira palavra enviada, como foi digitada")
		assert.Empty(t, tomada.OwnerID, "dona desconhecida fica vazia, nunca inventada")
		assert.ErrorIs(t, err, category.ErrKeywordTaken)
		assert.NotContains(t, strings.ToLower(err.Error()), "cinema", "a mensagem pode ir para o log")
		assert.Equal(t, 2, repo.substituicoes, "duas tentativas, nunca uma terceira")
		assert.Empty(t, repo.palavras[lazer.ID])
	})

	t.Run("criação", func(t *testing.T) {
		t.Parallel()

		repo := novoRepo()
		svc := novoServicoComTx(t, repo, txComRollback{repo: repo})

		repo.colisoesForcadas = 2
		_, err := svc.Create(t.Context(), ator(minhaCasa), category.CreateInput{
			Name: "Lazer", Kind: category.KindExpense, Keywords: []string{"Cinema"},
		})

		var tomada *category.KeywordTakenError
		require.ErrorAs(t, err, &tomada)
		assert.Equal(t, "Cinema", tomada.Keyword)
		assert.Empty(t, tomada.OwnerID)
		assert.Equal(t, 2, repo.substituicoes)

		total, err := repo.CountAll(t.Context(), minhaCasa)
		require.NoError(t, err)
		assert.Zero(t, total, "nenhuma das duas tentativas deixou categoria")
	})
}

// --- serviço: tri-estado do PATCH -----------------------------------------

func TestPatchSemKeywordsNaoMexeComListaVaziaLimpaEPresenteSubstitui(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	v := criarGrupoComPalavras(t, svc, minhaCasa, "Alimentação", "supermercado", "padaria")

	// Ausente: renomeia e as palavras ficam.
	nome := "Comida"
	depois, err := svc.Update(ctx, ator(minhaCasa), v.ID, category.UpdateInput{Name: &nome})
	require.NoError(t, err)
	assert.Equal(t, []string{"supermercado", "padaria"}, depois.Keywords)

	// Presente: substitui a lista INTEIRA — "padaria" some, "feira" entra.
	depois, err = svc.Update(ctx, ator(minhaCasa), v.ID, category.UpdateInput{Keywords: palavras("feira", "supermercado")})
	require.NoError(t, err)
	assert.Equal(t, []string{"feira", "supermercado"}, depois.Keywords)
	assert.Equal(t, []int{0, 1}, []int{repo.palavras[v.ID][0].Position, repo.palavras[v.ID][1].Position})

	// Vazia: limpa, e a palavra fica livre para outra categoria.
	depois, err = svc.Update(ctx, ator(minhaCasa), v.ID, category.UpdateInput{Keywords: palavras()})
	require.NoError(t, err)
	require.NotNil(t, depois.Keywords)
	assert.Empty(t, depois.Keywords)
	assert.Empty(t, repo.palavras[v.ID])

	outra := criarGrupoComPalavras(t, svc, minhaCasa, "Casa", "supermercado")
	assert.Equal(t, []string{"supermercado"}, outra.Keywords)
}

// --- serviço: validação da lista -------------------------------------------

func TestListaDePalavrasChaveEhValidadaSemGravarNada(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	v := criarGrupo(t, svc, minhaCasa, "Alimentação", category.KindExpense)

	vinteEUma := make([]string, 0, category.MaxKeywordsPerOwner+1)
	for i := range category.MaxKeywordsPerOwner + 1 {
		vinteEUma = append(vinteEUma, fmt.Sprintf("loja%02d", i))
	}

	casos := []struct {
		nome   string
		lista  []string
		erro   error
		indice int // -1 = erro sem índice
	}{
		{"21 itens é recusa, nunca truncamento", vinteEUma, category.ErrTooManyKeywords, -1},
		{"repetida pela forma normalizada", []string{"Padaria", "padaria"}, category.ErrDuplicateKeyword, 1},
		{"repetida com acento", []string{"Açougue", "acougue"}, category.ErrDuplicateKeyword, 1},
		{"caractere fora da allowlist", []string{"<script>"}, category.ErrInvalidKeyword, 0},
		{"só palavra vazia", []string{"de"}, category.ErrInvalidKeyword, 0},
		{"só letras soltas", []string{"c & a"}, category.ErrInvalidKeyword, 0},
		{"41 runas", []string{strings.Repeat("ç", textmatch.MaxKeywordRunes+1)}, category.ErrInvalidKeyword, 0},
		{"1 runa", []string{"x"}, category.ErrInvalidKeyword, 0},
		{"vazia", []string{"   "}, category.ErrInvalidKeyword, 0},
		{"o índice aponta o item ruim", []string{"mercado", "padaria", "'; DROP TABLE categories--"}, category.ErrInvalidKeyword, 2},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			_, err := svc.Update(ctx, ator(minhaCasa), v.ID, category.UpdateInput{Keywords: &caso.lista})
			require.ErrorIs(t, err, caso.erro)
			assert.True(t, category.IsValidationError(err), "é erro do usuário, não de infra")

			var item *category.KeywordValidationError
			if caso.indice < 0 {
				assert.False(t, errors.As(err, &item))
			} else {
				require.ErrorAs(t, err, &item)
				assert.Equal(t, caso.indice, item.Index)
			}
			// A mensagem do erro pode ir para o log: nenhuma palavra da lista.
			for _, palavra := range caso.lista {
				if p := strings.TrimSpace(palavra); p != "" {
					assert.NotContains(t, err.Error(), p)
				}
			}
			assert.Empty(t, repo.palavras[v.ID], "lista recusada não grava nem parte")
		})
	}

	// Vinte cabem.
	vinte := vinteEUma[:category.MaxKeywordsPerOwner]
	_, err := svc.Update(ctx, ator(minhaCasa), v.ID, category.UpdateInput{Keywords: &vinte})
	require.NoError(t, err)
	assert.Len(t, repo.palavras[v.ID], category.MaxKeywordsPerOwner)
}

// --- serviço: ciclo de vida da dona ---------------------------------------

func TestExcluirCategoriaApagaAsPalavrasEliberaAPalavra(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	v := criarGrupoComPalavras(t, svc, minhaCasa, "Alimentação", "supermercado")
	require.NoError(t, svc.Delete(ctx, ator(minhaCasa), v.ID))

	// A exclusão da categoria é lógica; a das palavras é FÍSICA e na mesma
	// transação — senão a palavra ficaria "em uso" por um fantasma.
	assert.NotNil(t, repo.linhas[v.ID].DeletedAt)
	assert.Empty(t, repo.palavras[v.ID])

	outra := criarGrupoComPalavras(t, svc, minhaCasa, "Casa", "supermercado")
	assert.Equal(t, []string{"supermercado"}, outra.Keywords)
}

func TestArquivarMantemAsPalavras(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	v := criarGrupoComPalavras(t, svc, minhaCasa, "Alimentação", "supermercado")

	arquivada, err := svc.Archive(ctx, ator(minhaCasa), v.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"supermercado"}, arquivada.Keywords)

	// Arquivada continua dona: a palavra segue reservada nesta casa (§4.1.4).
	_, err = svc.Create(ctx, ator(minhaCasa), category.CreateInput{
		Name: "Casa", Kind: category.KindExpense, Keywords: []string{"supermercado"},
	})
	assert.ErrorIs(t, err, category.ErrKeywordTaken)

	voltou, err := svc.Unarchive(ctx, ator(minhaCasa), v.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"supermercado"}, voltou.Keywords)
}

func TestPalavrasDeCategoriaDeOutraCasaNaoSaoEditaveis(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	daOutra := criarGrupoComPalavras(t, svc, outraCasa, "Alimentação", "supermercado")

	_, err := svc.Update(t.Context(), ator(minhaCasa), daOutra.ID, category.UpdateInput{Keywords: palavras("sequestrada")})
	assert.ErrorIs(t, err, category.ErrNotFound)
	assert.Equal(t, "supermercado", repo.palavras[daOutra.ID][0].Keyword)
}

// --- serviço: auditoria e log ----------------------------------------------

type auditorFake struct {
	registros []category.AuditParams
}

func (a *auditorFake) Record(_ context.Context, p category.AuditParams) error {
	a.registros = append(a.registros, p)
	return nil
}

// §4.1.5 da spec 0005: alterar palavras usa o evento que já existe, e a
// palavra NUNCA entra no rastro.
func TestAuditoriaNaoGuardaAPalavraChave(t *testing.T) {
	t.Parallel()

	auditor := &auditorFake{}
	svc := novoServico(t, novoRepo(), category.WithAudit(auditor))
	ctx := t.Context()

	v := criarGrupoComPalavras(t, svc, minhaCasa, "Alimentação", "supermercado")
	_, err := svc.Update(ctx, ator(minhaCasa), v.ID, category.UpdateInput{Keywords: palavras("padaria")})
	require.NoError(t, err)
	require.NoError(t, svc.Delete(ctx, ator(minhaCasa), v.ID))

	acoes := make([]string, 0, len(auditor.registros))
	for _, r := range auditor.registros {
		acoes = append(acoes, r.Action)
		serializado, err := json.Marshal(r)
		require.NoError(t, err)
		assert.NotContains(t, string(serializado), "supermercado")
		assert.NotContains(t, string(serializado), "padaria")
	}
	assert.Equal(t, []string{
		audit.ActionCategoryCreated,
		audit.ActionCategoryUpdated,
		audit.ActionCategoryDeleted,
	}, acoes, "nenhum evento novo: os existentes cobrem a edição de palavras")
}

func TestFalhaDeInfraNasPalavrasNaoEcoaAPalavra(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	repo.erroPalavras = errors.New("banco fora do ar")
	svc := novoServico(t, repo)

	_, err := svc.Create(t.Context(), ator(minhaCasa), category.CreateInput{
		Name: "Alimentação", Kind: category.KindExpense, Keywords: []string{"supermercado"},
	})
	require.Error(t, err)
	assert.NotErrorIs(t, err, category.ErrKeywordTaken)
	assert.False(t, category.IsValidationError(err))
	assert.NotContains(t, err.Error(), "supermercado", "o erro vai para o log do handler")
}

// --- handler: o contrato --------------------------------------------------

func TestPostSemKeywordsRespondeListaVaziaEComKeywordsGrava(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		`{"name":"Moradia","kind":"expense"}`, amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"keywords":[]`)

	rec = amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		`{"name":"Alimentação","kind":"expense","keywords":["Supermercado","padaria"]}`, amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var v category.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, []string{"Supermercado", "padaria"}, v.Keywords)

	// E a listagem traz as mesmas, no grupo certo.
	rec = amb.chamar(t, minhaCasa, http.MethodGet, "/categories", "", amb.handler.List, "")
	require.Equal(t, http.StatusOK, rec.Code)
	var arvore category.ListView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &arvore))
	require.Len(t, arvore.Expense, 2)
	porNome := map[string][]string{}
	for _, g := range arvore.Expense {
		porNome[g.Name] = g.Keywords
	}
	assert.Equal(t, []string{"Supermercado", "padaria"}, porNome["Alimentação"])
	assert.Equal(t, []string{}, porNome["Moradia"])
}

func TestPatchKeywordsTriEstadoPelaAPI(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	v := criarGrupoComPalavras(t, amb.svc, minhaCasa, "Alimentação", "supermercado", "padaria")

	// Ausente: não mexe.
	rec := amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+v.ID, `{"name":"Comida"}`, amb.handler.Update, v.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var depois category.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &depois))
	assert.Equal(t, []string{"supermercado", "padaria"}, depois.Keywords)

	// Presente: substitui.
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+v.ID, `{"keywords":["feira"]}`, amb.handler.Update, v.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &depois))
	assert.Equal(t, []string{"feira"}, depois.Keywords)

	// `[]`: limpa.
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+v.ID, `{"keywords":[]}`, amb.handler.Update, v.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), `"keywords":[]`)

	// `null` não existe no contrato (type: array): 400, e nada muda.
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+v.ID, `{"keywords":null}`, amb.handler.Update, v.ID)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

	// Item com tipo errado é corpo malformado.
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+v.ID, `{"keywords":[1]}`, amb.handler.Update, v.ID)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+v.ID, `{"keywords":"padaria"}`, amb.handler.Update, v.ID)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestKeywordsInvalidasDevolvem400ApontandoOItem(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)

	vinteEUma := make([]string, 0, 21)
	for i := range 21 {
		vinteEUma = append(vinteEUma, fmt.Sprintf("loja%02d", i))
	}
	listaDe21, err := json.Marshal(vinteEUma)
	require.NoError(t, err)

	casos := []struct {
		nome  string
		lista string
		campo string
	}{
		{"21 itens", string(listaDe21), "keywords"},
		{"repetida", `["Padaria","padaria"]`, "keywords[1]"},
		{"script", `["<script>alert(1)</script>"]`, "keywords[0]"},
		{"só stopword", `["de"]`, "keywords[0]"},
		{"só letras soltas", `["c & a"]`, "keywords[0]"},
		{"41 runas", `["` + strings.Repeat("a", 41) + `"]`, "keywords[0]"},
		{"terceiro item", `["mercado","padaria","x"]`, "keywords[2]"},
	}
	for _, caso := range casos {
		t.Run(caso.nome, func(t *testing.T) {
			corpo := `{"name":"` + caso.nome + `","kind":"expense","keywords":` + caso.lista + `}`
			rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories", corpo, amb.handler.Create, "")
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())

			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, "VALIDATION_FAILED", codigo)
			assert.Contains(t, campos, caso.campo, "a tela precisa saber qual ficha destacar")
			// A resposta não ecoa a palavra (a tela já a tem; ecoar seria
			// reflexão de entrada).
			assert.NotContains(t, rec.Body.String(), "<script>")
			assert.NotContains(t, rec.Body.String(), "loja00")
		})
	}
	// Nenhuma categoria foi criada por nenhum dos casos.
	rec := amb.chamar(t, minhaCasa, http.MethodGet, "/categories", "", amb.handler.List, "")
	assert.Contains(t, rec.Body.String(), `"expense":[]`)
}

// Critério 2 da spec 0005 na borda: 409 KEYWORD_TAKEN com `keyword` e
// `ownerId`, e a dona é sempre desta casa.
func TestPalavraChaveTomadaDevolve409ComADonaDaCasa(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	dona := criarGrupoComPalavras(t, amb.svc, minhaCasa, "Alimentação", "Supermercado")
	criarGrupoComPalavras(t, amb.svc, outraCasa, "Alimentação", "Padaria")

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		`{"name":"Casa","kind":"expense","keywords":["supermercado"]}`, amb.handler.Create, "")
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, "KEYWORD_TAKEN", codigo)
	assert.Equal(t, "supermercado", campos["keyword"])
	assert.Equal(t, dona.ID, campos["ownerId"])

	// A palavra da OUTRA casa está livre aqui: 201. (Nome diferente porque o
	// dublê de transação não desfaz a categoria do 409 acima.)
	rec = amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		`{"name":"Pães","kind":"expense","keywords":["padaria"]}`, amb.handler.Create, "")
	assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// PATCH também.
	lazer := criarGrupo(t, amb.svc, minhaCasa, "Lazer", category.KindExpense)
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+lazer.ID,
		`{"keywords":["Supermercado"]}`, amb.handler.Update, lazer.ID)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	_, campos = corpoDeErro(t, rec)
	assert.Equal(t, dona.ID, campos["ownerId"])
}

// A corrida do índice único na borda: com a dona sumida a segunda tentativa
// responde 200 como se nada tivesse acontecido; na corrida tripla o 409 sai
// com `fields.keyword` (obrigatório no contrato KeywordConflict) e SEM
// `ownerId` — nunca um 409 sem `fields`. E nada disso passa pelo log.
func TestCorridaDoIndiceUnicoPelaAPINuncaOmiteFields(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	repo := novoRepo()
	svc := novoServico(t, repo)
	amb := &ambiente{
		handler: category.NewHandler(svc, logging.New(&logs, logging.Options{Level: "debug", Format: "json"}), 0),
		repo:    repo,
		svc:     svc,
	}
	lazer := criarGrupo(t, amb.svc, minhaCasa, "Lazer", category.KindExpense)

	// (a) dona sumida: a segunda tentativa passa.
	amb.repo.colisoesForcadas = 1
	rec := amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+lazer.ID,
		`{"keywords":["Cinema","Teatro"]}`, amb.handler.Update, lazer.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var v category.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))
	assert.Equal(t, []string{"Cinema", "Teatro"}, v.Keywords)

	// (b) corrida tripla: 409 com a palavra e sem dona.
	amb.repo.colisoesForcadas = 2
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/categories/"+lazer.ID,
		`{"keywords":["Streaming","Teatro"]}`, amb.handler.Update, lazer.ID)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())

	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, "KEYWORD_TAKEN", codigo)
	require.NotNil(t, campos, "o contrato exige `fields` no 409")
	assert.Equal(t, "Streaming", campos["keyword"], "a primeira palavra enviada")
	_, temDona := campos["ownerId"]
	assert.False(t, temDona, "dona desconhecida é omitida, nunca enviada vazia (formato uuid)")
	assert.NotContains(t, rec.Body.String(), "ownerId")

	// A lista anterior ficou como estava: a tentativa recusada não gravou.
	assert.Equal(t, []string{"Cinema", "Teatro"}, []string{repo.palavras[lazer.ID][0].Keyword, repo.palavras[lazer.ID][1].Keyword})

	// Nem a tentativa repetida nem o 409 passam pelo log (S8).
	for _, palavra := range []string{"cinema", "teatro", "streaming"} {
		assert.NotContains(t, strings.ToLower(logs.String()), palavra)
	}
}

// S8: a palavra-chave é dado da casa e não entra no log — nem no 400, nem no
// 409, nem no 500 de infraestrutura.
func TestNenhumaPalavraChaveVaiParaOLog(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	repo := novoRepo()
	svc := novoServico(t, repo)
	amb := &ambiente{
		handler: category.NewHandler(svc, logging.New(&logs, logging.Options{Level: "debug", Format: "json"}), 0),
		repo:    repo,
		svc:     svc,
	}
	criarGrupoComPalavras(t, amb.svc, minhaCasa, "Alimentação", "supermercado")

	const marcador = "palavrasecreta"
	corpos := []string{
		`{"name":"A","kind":"expense","keywords":["<` + marcador + `>"]}`,                  // 400
		`{"name":"B","kind":"expense","keywords":["` + marcador + `","` + marcador + `"]}`, // 400 repetida
		`{"name":"C","kind":"expense","keywords":["supermercado"]}`,                        // 409
	}
	for _, corpo := range corpos {
		amb.chamar(t, minhaCasa, http.MethodPost, "/categories", corpo, amb.handler.Create, "")
	}

	// E o 500: o repositório falha ao gravar as palavras.
	amb.repo.erroPalavras = errors.New("banco fora do ar")
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		`{"name":"D","kind":"expense","keywords":["`+marcador+`"]}`, amb.handler.Create, "")
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "banco fora do ar", "detalhe interno só no log")

	assert.Contains(t, logs.String(), "banco fora do ar", "o 500 foi logado")
	assert.NotContains(t, logs.String(), marcador)
	assert.NotContains(t, logs.String(), "supermercado")
}

// --- handler: o payload REAL contra o contrato ------------------------------

func schemaCategory(t *testing.T) (required []string, properties map[string]yaml.Node) {
	t.Helper()

	raw, err := os.ReadFile("../../api/openapi.yaml")
	require.NoError(t, err, "api/openapi.yaml precisa existir (ADR-006)")

	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Required   []string             `yaml:"required"`
				Properties map[string]yaml.Node `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	esquema, ok := doc.Components.Schemas["Category"]
	require.True(t, ok, "o schema Category precisa existir na spec")
	require.NotEmpty(t, esquema.Required)
	require.Contains(t, esquema.Required, "keywords", "spec 0005: keywords é required em Category")
	return esquema.Required, esquema.Properties
}

func chavesDoObjeto(t *testing.T, bruto []byte) []string {
	t.Helper()
	var objeto map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(bruto, &objeto), "corpo: %s", string(bruto))
	out := make([]string, 0, len(objeto))
	for chave := range objeto {
		out = append(out, chave)
	}
	sort.Strings(out)
	return out
}

// O mesmo cuidado de account.TestRespostaDeContaTemTodosOsCamposRequiredDoContrato:
// a lista de `required` é lida da spec, para o teste envelhecer junto com ela.
func TestRespostaDeCategoriaTemTodosOsCamposRequiredDoContratoENenhumForaDele(t *testing.T) {
	t.Parallel()

	obrigatorios, declarados := schemaCategory(t)

	amb := novoAmbiente(t)
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		`{"name":"Alimentação","kind":"expense","keywords":["supermercado"]}`, amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var grupo category.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &grupo))
	folha := amb.chamar(t, minhaCasa, http.MethodPost, "/categories",
		fmt.Sprintf(`{"name":"Padaria","parentId":%q}`, grupo.ID), amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, folha.Code, folha.Body.String())

	conferir := func(t *testing.T, bruto []byte) {
		t.Helper()
		chaves := chavesDoObjeto(t, bruto)
		for _, campo := range obrigatorios {
			assert.Contains(t, chaves, campo, "campo `required` ausente na resposta")
		}
		for _, chave := range chaves {
			assert.Contains(t, declarados, chave, "a resposta publica um campo que o contrato não declara")
		}
	}

	t.Run("POST /categories", func(t *testing.T) { conferir(t, rec.Body.Bytes()) })

	t.Run("GET /categories/{id}", func(t *testing.T) {
		lida := amb.chamar(t, minhaCasa, http.MethodGet, "/categories/"+grupo.ID, "", amb.handler.Get, grupo.ID)
		require.Equal(t, http.StatusOK, lida.Code)
		conferir(t, lida.Body.Bytes())
	})

	t.Run("GET /categories (árvore, grupo e filha)", func(t *testing.T) {
		lista := amb.chamar(t, minhaCasa, http.MethodGet, "/categories", "", amb.handler.List, "")
		require.Equal(t, http.StatusOK, lista.Code, lista.Body.String())

		var corpo struct {
			Expense []json.RawMessage `json:"expense"`
		}
		require.NoError(t, json.Unmarshal(lista.Body.Bytes(), &corpo))
		require.Len(t, corpo.Expense, 1)
		conferir(t, corpo.Expense[0])

		var comFilhas struct {
			Children []json.RawMessage `json:"children"`
		}
		require.NoError(t, json.Unmarshal(corpo.Expense[0], &comFilhas))
		require.Len(t, comFilhas.Children, 1)
		conferir(t, comFilhas.Children[0])
	})
}
