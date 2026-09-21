package aiimport_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/aiimport"
	"github.com/brunorblanck/homefinance/backend/internal/aiprompt"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Os testes deste arquivo provam os critérios 11–36 da spec 0010 que exigem
// RELER O BANCO: a prévia que não grava, o confirm que grava numa transação
// só, a idempotência, o teto real de 200, a auditoria e a medição de
// impacto. A matriz das regras em si está em validate_test.go.

// casaComum monta: Alimentação > Mercado [zaffari] · Transporte (grupo sem
// filha) [uber] · Conta Corrente [nubank] · Cartão.
type casaComum struct {
	alimentacao, mercado, transporte category.View
	corrente, cartao                 string
}

func (a *ambiente) casaComum(t *testing.T) casaComum {
	t.Helper()
	c := casaComum{}
	c.alimentacao = a.grupo(t, a.casa.ID, "Alimentação", category.KindExpense)
	c.mercado = a.folha(t, a.casa.ID, c.alimentacao.ID, "Mercado", "zaffari")
	c.transporte = a.grupo(t, a.casa.ID, "Transporte", category.KindExpense, "uber")
	c.corrente = a.conta(t, a.casa.ID, "Conta Corrente", "nubank")
	c.cartao = a.conta(t, a.casa.ID, "Cartão")
	a.auditoria.zerar()
	return c
}

// Critério 11: a prévia mostra 2 em `added` e NADA é gravado (relendo o
// banco); o confirm grava as 2.
func TestCriterio11PreviaNaoGravaEConfirmGrava(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	in := input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{
		entrada(c.mercado.ID, "Alimentação > Mercado", "mercado do seu jose", "carrefour"),
	}})

	previa, err := a.svc.Preview(t.Context(), a.ator(), in)
	require.NoError(t, err)
	require.Len(t, previa.Items, 1)
	assert.Equal(t, []string{"mercado do seu jose", "carrefour"}, previa.Items[0].Added)
	assert.Equal(t, 2, previa.Totals.Added)
	assert.Equal(t, antes, a.retrato(t, a.casa.ID), "a prévia não escreve uma linha")
	assert.Empty(t, a.auditoria.acoes(), "a prévia não audita")

	confirm, err := a.svc.Confirm(t.Context(), a.ator(), in)
	require.NoError(t, err)
	assert.Equal(t, previa.Totals.Added, confirm.Totals.Added, "a prévia e o confirm não divergem")
	assert.Equal(t, []string{"zaffari", "mercado do seu jose", "carrefour"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID),
		"a união preserva a ordem de cadastro e acrescenta no fim")
	assert.NotEqual(t, antes, a.retrato(t, a.casa.ID))
}

// Critério 12: reimportar o MESMO JSON — 0 adicionadas, 2 already_present,
// banco inalterado.
func TestCriterio12ReimportarOMesmoJSONEhInofensivo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	exp := category.KindExpense
	in := input(aiimport.Payload{
		NewCategories:    []aiimport.NewCategoryEntry{nova("Saúde", "Farmácia", &exp, "drogaria")},
		CategoryKeywords: []aiimport.CategoryKeywordEntry{entrada(c.mercado.ID, "Alimentação > Mercado", "feira", "carrefour")},
		AccountKeywords:  []aiimport.AccountKeywordEntry{deConta(c.corrente, "Conta Corrente", "nu pagamentos")},
	})

	primeiro, err := a.svc.Confirm(t.Context(), a.ator(), in)
	require.NoError(t, err)
	assert.Equal(t, 1, primeiro.Totals.CategoriesCreated)
	assert.Equal(t, 4, primeiro.Totals.Added)
	depois := a.retrato(t, a.casa.ID)

	segundo, err := a.svc.Confirm(t.Context(), a.ator(), in)
	require.NoError(t, err)
	assert.Equal(t, 0, segundo.Totals.Added)
	assert.Equal(t, 0, segundo.Totals.CategoriesCreated)
	assert.Equal(t, 4, segundo.Totals.Skipped)
	assert.Equal(t, aiimport.OutcomeMergedIntoExisting, segundo.NewCategories[0].Outcome,
		"a categoria criada na primeira vez agora existe: merged, com a palavra já presente")
	assert.Equal(t, []aiimport.SkippedKeyword{{Keyword: "drogaria", Reason: aiimport.SkipAlreadyPresent}}, segundo.NewCategories[0].Skipped)
	assert.Equal(t, depois, a.retrato(t, a.casa.ID), "banco inalterado")
}

// Critério 13: palavra de outra categoria é keyword_taken com a dona; as
// demais entram.
func TestCriterio13KeywordTakenCitaADonaEAsDemaisEntram(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{
		entrada(c.transporte.ID, "Transporte", "zaffari", "99 taxi"),
	}}))
	require.NoError(t, err)
	assert.Equal(t, []aiimport.RejectedKeyword{{Keyword: "zaffari", Reason: aiimport.RejectKeywordTaken, OwnerID: c.mercado.ID}}, r.Items[0].Rejected)
	assert.Equal(t, []string{"uber", "99 taxi"}, a.palavrasDe(t, a.casa.ID, c.transporte.ID))
	assert.Equal(t, []string{"zaffari"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID), "a dona não muda")
}

// Critério 14: mesma palavra em duas categorias do JSON — as duas recusadas,
// e nenhuma gravada.
func TestCriterio14AmbiguaNoPayloadNaoEntraEmNenhuma(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{
		entrada(c.transporte.ID, "Transporte", "hortifruti"),
		entrada(c.mercado.ID, "Alimentação > Mercado", "Hortifrúti"),
	}}))
	require.NoError(t, err)
	// Asserção de tamanho ANTES do índice: quando a regra 9 quebrar, o teste
	// tem de falhar com a mensagem, não com index out of range.
	require.Len(t, r.Items, 2)
	assert.Equal(t, map[string]string{"hortifruti": aiimport.RejectAmbiguousInPayload}, motivos(r.Items[0].Rejected))
	assert.Equal(t, map[string]string{"Hortifrúti": aiimport.RejectAmbiguousInPayload}, motivos(r.Items[1].Rejected))
	assert.Empty(t, r.Items[0].Added)
	assert.Empty(t, r.Items[1].Added)
	assert.Equal(t, antes, a.retrato(t, a.casa.ID))
}

// Critério 15: item com 18 palavras recebendo 3 — entram 2 e a 3ª é
// limit_exceeded, conferido no banco (20 gravadas, nunca 21).
func TestCriterio15TetoDe20NoBanco(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	dezoito := make([]string, 0, 18)
	for i := range 18 {
		dezoito = append(dezoito, "palavra "+string(rune('a'+i)))
	}
	_, err := a.categoriaSvc.Update(t.Context(), a.atorCategoria(), c.transporte.ID, category.UpdateInput{Keywords: &dezoito})
	require.NoError(t, err)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{
		entrada(c.transporte.ID, "Transporte", "nova um", "nova dois", "nova tres"),
	}}))
	require.NoError(t, err)
	assert.Equal(t, []string{"nova um", "nova dois"}, r.Items[0].Added)
	assert.Equal(t, []aiimport.RejectedKeyword{{Keyword: "nova tres", Reason: aiimport.RejectLimitExceeded}}, r.Items[0].Rejected)
	gravadas := a.palavrasDe(t, a.casa.ID, c.transporte.ID)
	assert.Len(t, gravadas, category.MaxKeywordsPerOwner)
	assert.Equal(t, "nova dois", gravadas[19])
}

// Critério 16: categoryId certo com categoryPath errado — name_mismatch,
// nada gravado para aquele item, e o resto do lote entra.
func TestCriterio16NameMismatchNaoGravaOItem(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{
		entrada(c.mercado.ID, "Alimentação > Padaria", "feira"),
		entrada(c.transporte.ID, "Transporte", "99 taxi"),
	}}))
	require.NoError(t, err)
	require.Len(t, r.Items, 2)
	assert.Equal(t, map[string]string{"feira": aiimport.RejectNameMismatch}, motivos(r.Items[0].Rejected))
	assert.Equal(t, "Alimentação > Mercado", *r.Items[0].Name)
	assert.Equal(t, []string{"zaffari"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID))
	assert.Equal(t, []string{"uber", "99 taxi"}, a.palavrasDe(t, a.casa.ID, c.transporte.ID))
}

// Critério 17: categoryId de OUTRA CASA — item_not_found, indistinguível de
// id inexistente, e a outra casa intacta. Duas casas povoadas de verdade.
func TestCriterio17IdDeOutraCasaEhIndistinguivelDeInexistente(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum(t)
	alheia := a.grupo(t, a.alheia.ID, "Lazer", category.KindExpense, "cinema")
	contaAlheia := a.conta(t, a.alheia.ID, "Conta da outra casa", "itau")
	antesAlheia := a.retrato(t, a.alheia.ID)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{
		CategoryKeywords: []aiimport.CategoryKeywordEntry{
			entrada(alheia.ID, "Lazer", "netflix"),
			entrada("018f0000-0000-7000-8000-00000000dead", "Lazer", "netflix"),
		},
		AccountKeywords: []aiimport.AccountKeywordEntry{
			deConta(contaAlheia, "Conta da outra casa", "banco"),
			deConta("018f0000-0000-7000-8000-00000000beef", "Conta da outra casa", "banco"),
		},
	}))
	require.NoError(t, err)
	require.Len(t, r.Items, 4)
	for i := 0; i < 4; i += 2 {
		alheio, inexistente := r.Items[i], r.Items[i+1]
		alheio.ID, inexistente.ID = "", ""
		assert.Equal(t, alheio, inexistente, "item %d", i)
		require.Len(t, r.Items[i].Rejected, 1)
		assert.Equal(t, aiimport.RejectItemNotFound, r.Items[i].Rejected[0].Reason)
		assert.Nil(t, r.Items[i].Name)
	}
	assert.Equal(t, antesAlheia, a.retrato(t, a.alheia.ID), "a outra casa não muda")
	assert.Equal(t, []string{"cinema"}, a.palavrasDe(t, a.alheia.ID, alheia.ID))
}

// Critério 18: categoria arquivada é item_archived, nada gravado.
func TestCriterio18CategoriaArquivadaEhItemArchived(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	a.arquivar(t, a.casa.ID, c.transporte.ID)
	antes := a.retrato(t, a.casa.ID)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{
		entrada(c.transporte.ID, "Transporte", "99 taxi"),
	}}))
	require.NoError(t, err)
	require.Len(t, r.Items, 1)
	assert.Equal(t, map[string]string{"99 taxi": aiimport.RejectItemArchived}, motivos(r.Items[0].Rejected))
	assert.Equal(t, antes, a.retrato(t, a.casa.ID))
}

// Critério 19: palavra com 1 runa, 41 runas, marcação HTML, fragmento de SQL
// ou só palavras vazias — invalid_keyword, e o banco segue íntegro.
func TestCriterio19PalavrasInvalidasNaoTocamOBanco(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{
		entrada(c.transporte.ID, "Transporte", "x", strings.Repeat("a", 41), "<img src=x onerror=alert(1)>",
			"' OR 1=1; DROP TABLE category_keywords; --", "de ltda"),
	}}))
	require.NoError(t, err)
	require.Len(t, r.Items[0].Rejected, 5)
	for _, rej := range r.Items[0].Rejected {
		assert.Equal(t, aiimport.RejectInvalidKeyword, rej.Reason)
	}
	assert.Equal(t, antes, a.retrato(t, a.casa.ID), "banco íntegro")
	// A palavra recusada não vai para o log — nem a bruta, nem a truncada.
	assert.NotContains(t, a.logs.String(), "onerror")
	assert.NotContains(t, a.logs.String(), "DROP TABLE")
}

// Critério 22: falha forçada no meio do lote não deixa nada pela metade — a
// transação é uma só. O primeiro Create passa e grava; o segundo falha; o
// banco tem de voltar ao retrato de antes.
func TestCriterio22FalhaNoMeioDoLoteDesfazTudo(t *testing.T) {
	t.Parallel()
	escritor := &escritorDeCategoria{falharNa: 2, erroForca: errors.New("falha forçada no meio do lote")}
	a := novoAmbienteComEscritor(t, escritor)
	c := a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)
	exp := category.KindExpense

	_, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{
		NewCategories: []aiimport.NewCategoryEntry{
			nova("Saúde", "Farmácia", &exp, "drogaria"), // grupo (1ª criação) + folha (2ª: falha)
		},
		CategoryKeywords: []aiimport.CategoryKeywordEntry{entrada(c.mercado.ID, "Alimentação > Mercado", "feira")},
	}))
	require.Error(t, err)
	assert.False(t, errors.Is(err, aiimport.ErrConflict), "falha genérica não é 409")
	assert.Equal(t, 2, escritor.criacoes, "a falha veio na segunda criação, depois de a primeira ter gravado")
	assert.Equal(t, antes, a.retrato(t, a.casa.ID), "nada pela metade")
	assert.Nil(t, a.categoriaPorCaminho(t, a.casa.ID, "saude"), "o grupo criado antes da falha foi desfeito")
	assert.Equal(t, []string{"zaffari"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID))
	// E a auditoria da execução não sobrevive ao rollback: o serviço a
	// grava DEPOIS da aplicação, na mesma transação.
	assert.NotContains(t, a.auditoria.acoes(), "ai.keyword_import_confirmed")
}

// Critérios 23 e 32: a confirmação gera category.created por criação,
// category.updated / account.updated por item que recebeu palavra e UMA
// ai.keyword_import_confirmed — SEM as palavras em lugar nenhum.
func TestCriterios23e32AuditoriaSemPalavras(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	exp := category.KindExpense

	_, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{
		NewCategories:    []aiimport.NewCategoryEntry{nova("Saúde", "Farmácia", &exp, "drogaria xyzzy")},
		CategoryKeywords: []aiimport.CategoryKeywordEntry{entrada(c.mercado.ID, "Alimentação > Mercado", "feira plugh")},
		AccountKeywords:  []aiimport.AccountKeywordEntry{deConta(c.corrente, "Conta Corrente", "nu pagamentos quux")},
	}))
	require.NoError(t, err)

	assert.Equal(t, []string{
		"account.updated",
		"ai.keyword_import_confirmed",
		"category.created", "category.created", // o grupo Saúde e a folha Farmácia
		"category.updated", "category.updated", // Farmácia (as palavras) e Mercado
	}, a.auditoria.acoes())

	a.auditoria.mu.Lock()
	defer a.auditoria.mu.Unlock()
	for _, e := range a.auditoria.entradas {
		assert.Equal(t, a.casa.ID, e.HouseholdID)
		assert.Equal(t, a.usuario, e.UserID)
		assert.Equal(t, "203.0.113.7", e.IP)
		for _, palavra := range []string{"xyzzy", "plugh", "quux", "drogaria", "feira", "nu pagamentos"} {
			assert.NotContains(t, e.EntityID, palavra)
			assert.NotContains(t, e.Entity, palavra)
		}
		if e.Action == "ai.keyword_import_confirmed" {
			assert.Equal(t, "household", e.Entity)
			assert.Equal(t, a.casa.ID, e.EntityID, "a entidade da origem é a CASA")
		}
	}
	logs := a.logs.String()
	for _, palavra := range []string{"xyzzy", "plugh", "quux"} {
		assert.NotContains(t, logs, palavra, "palavra no log de aplicação")
	}
}

// Critério 24: grupo existente + folha nova — natureza HERDADA; kind
// divergente no JSON é kind_mismatch e nada nasce.
func TestCriterio24FolhaNovaHerdaANaturezaDoGrupo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum(t)
	inc := category.KindIncome

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{NewCategories: []aiimport.NewCategoryEntry{
		nova("Alimentação", "Feira", nil, "hortifruti"),
		nova("Alimentação", "Peixaria", &inc, "peixaria"),
	}}))
	require.NoError(t, err)
	feira := a.categoriaPorCaminho(t, a.casa.ID, "alimentacao > feira")
	require.NotNil(t, feira)
	assert.Equal(t, category.KindExpense, feira.Kind, "herdada")
	assert.Equal(t, feira.ID, *r.NewCategories[0].CategoryID, "no confirm o id da recém-nascida volta")
	assert.Equal(t, []string{"hortifruti"}, a.palavrasDe(t, a.casa.ID, feira.ID))
	assert.Equal(t, aiimport.OutcomeKindMismatch, r.NewCategories[1].Outcome)
	assert.Nil(t, a.categoriaPorCaminho(t, a.casa.ID, "alimentacao > peixaria"))
}

// Critérios 25 e 26: grupo novo sem kind é kind_required (nada nasce); grupo
// novo + folha nascem na MESMA transação, e o GRUPO não recebe palavra
// nenhuma (verificado no banco).
func TestCriterios25e26GrupoNovoNasceComoContinenteSemPalavras(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{NewCategories: []aiimport.NewCategoryEntry{
		nova("Saúde", "Farmácia", nil, "drogaria"),
	}}))
	require.NoError(t, err)
	assert.Equal(t, aiimport.OutcomeKindRequired, r.NewCategories[0].Outcome)
	assert.Equal(t, antes, a.retrato(t, a.casa.ID))

	exp := category.KindExpense
	r, err = a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{NewCategories: []aiimport.NewCategoryEntry{
		nova("Saúde", "Farmácia", &exp, "drogaria", "panvel"),
		nova("Saúde", "Dentista", nil, "odonto"),
	}}))
	require.NoError(t, err)
	assert.Equal(t, 2, r.Totals.CategoriesCreated)
	assert.True(t, r.NewCategories[0].GroupIsNew)
	assert.True(t, r.NewCategories[1].GroupIsNew)

	saude := a.categoriaPorCaminho(t, a.casa.ID, "saude")
	require.NotNil(t, saude, "o grupo nasceu")
	assert.Equal(t, category.KindExpense, saude.Kind)
	assert.Empty(t, a.palavrasDe(t, a.casa.ID, saude.ID), "o grupo NUNCA recebe palavra-chave")
	farmacia := a.categoriaPorCaminho(t, a.casa.ID, "saude > farmacia")
	require.NotNil(t, farmacia)
	assert.Equal(t, saude.ID, *farmacia.ParentID)
	assert.Equal(t, []string{"drogaria", "panvel"}, a.palavrasDe(t, a.casa.ID, farmacia.ID))
	dentista := a.categoriaPorCaminho(t, a.casa.ID, "saude > dentista")
	require.NotNil(t, dentista)
	assert.Equal(t, []string{"odonto"}, a.palavrasDe(t, a.casa.ID, dentista.ID))
}

// Critério 27: `group > name` que já existe ativo — merged_into_existing, as
// palavras entram na existente e NENHUMA categoria é criada.
func TestCriterio27MergedIntoExistingNaoCriaCategoria(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{NewCategories: []aiimport.NewCategoryEntry{
		nova("alimentacao", "MERCADO", nil, "carrefour", "zaffari"),
	}}))
	require.NoError(t, err)
	m := r.NewCategories[0]
	assert.Equal(t, aiimport.OutcomeMergedIntoExisting, m.Outcome)
	assert.Equal(t, c.mercado.ID, *m.CategoryID)
	assert.Equal(t, 0, r.Totals.CategoriesCreated)
	assert.Equal(t, []string{"zaffari", "carrefour"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID))
	depois := a.retrato(t, a.casa.ID)
	assert.Equal(t, antes.categorias, depois.categorias, "nenhuma categoria criada")
}

// Critério 28: nome igual ao de uma ARQUIVADA no mesmo pai — nada criado.
// E o caso extra: o próprio GRUPO arquivado.
func TestCriterio28NameTakenArchivedNaoCriaSegundaComOMesmoNome(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	padaria := a.folha(t, a.casa.ID, c.alimentacao.ID, "Padaria", "padaria")
	a.arquivar(t, a.casa.ID, padaria.ID)
	lazer := a.grupo(t, a.casa.ID, "Lazer", category.KindExpense)
	a.arquivar(t, a.casa.ID, lazer.ID)
	antes := a.retrato(t, a.casa.ID)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{NewCategories: []aiimport.NewCategoryEntry{
		nova("Alimentação", "padaria", nil, "pao"),
		nova("Lazer", "Cinema", nil, "cinemark"),
	}}))
	require.NoError(t, err)
	assert.Equal(t, aiimport.OutcomeNameTakenArchived, r.NewCategories[0].Outcome)
	assert.Equal(t, aiimport.OutcomeNameTakenArchived, r.NewCategories[1].Outcome, "grupo arquivado orienta desarquivar")
	assert.Equal(t, antes, a.retrato(t, a.casa.ID))
}

// Critério 29: casa com 199 categorias recebendo 3 novas — entra 1, as
// outras 2 são household_limit. O teto real é o do category.Service.Create,
// e o banco fica com EXATAMENTE 200.
func TestCriterio29TetoDe200DaCasaNoBanco(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t) // 3 categorias
	// Direto no repositório, para não pagar 196 transações do serviço.
	for i := 3; i < category.MaxPerHousehold-1; i++ {
		nome := "Grupo " + strings.Repeat("x", i%40) + string(rune('a'+i%26))
		require.NoError(t, a.categorias.Create(t.Context(), &category.Category{
			ID: a.proximoID("c0"), HouseholdID: a.casa.ID, Name: nome + string(rune('0'+i%10)),
			NameNorm: strings.ToLower(nome) + string(rune('0'+i%10)), Kind: category.KindExpense,
			CreatedAt: a.momento, UpdatedAt: a.momento,
		}))
	}
	total, err := a.categorias.CountAll(t.Context(), a.casa.ID)
	require.NoError(t, err)
	require.EqualValues(t, category.MaxPerHousehold-1, total)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{NewCategories: []aiimport.NewCategoryEntry{
		nova("Alimentação", "Feira", nil, "feira"),
		nova("Alimentação", "Açougue", nil, "acougue"),
		nova("Alimentação", "Peixaria", nil, "peixaria"),
	}}))
	require.NoError(t, err)
	assert.Equal(t, aiimport.OutcomeCreated, r.NewCategories[0].Outcome)
	assert.Equal(t, aiimport.OutcomeHouseholdLimit, r.NewCategories[1].Outcome)
	assert.Equal(t, aiimport.OutcomeHouseholdLimit, r.NewCategories[2].Outcome)
	total, err = a.categorias.CountAll(t.Context(), a.casa.ID)
	require.NoError(t, err)
	assert.EqualValues(t, category.MaxPerHousehold, total)
	_ = c
}

// Critério 30: categoria desmarcada na prévia não nasce e as palavras dela
// não entram em lugar nenhum.
func TestCriterio30CategoriaDesmarcadaNaoNasceNemDeixaPalavra(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum(t)
	exp := category.KindExpense
	pl := aiimport.Payload{NewCategories: []aiimport.NewCategoryEntry{
		nova("Saúde", "Farmácia", &exp, "drogaria"),
		nova("Saúde", "Dentista", &exp, "odonto"),
	}}

	previa, err := a.svc.Preview(t.Context(), a.ator(), input(pl))
	require.NoError(t, err)
	refDesmarcado := previa.NewCategories[0].Ref
	assert.Equal(t, "saude > farmacia", refDesmarcado)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(pl, refDesmarcado))
	require.NoError(t, err)
	assert.Equal(t, aiimport.OutcomeSkippedByUser, r.NewCategories[0].Outcome)
	assert.Equal(t, aiimport.OutcomeCreated, r.NewCategories[1].Outcome)
	assert.Nil(t, a.categoriaPorCaminho(t, a.casa.ID, "saude > farmacia"))
	require.NotNil(t, a.categoriaPorCaminho(t, a.casa.ID, "saude > dentista"))
	kws, err := a.categorias.ListKeywords(t.Context(), a.casa.ID)
	require.NoError(t, err)
	for _, k := range kws {
		assert.NotEqual(t, "drogaria", k.Norm, "a palavra da desmarcada não entrou em lugar nenhum")
	}
}

// Critério 31: dois blocos com o mesmo `group > name` — o primeiro vale, e o
// banco tem UMA categoria.
func TestCriterio31DuplicataNoPayloadCriaUmaSo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum(t)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{NewCategories: []aiimport.NewCategoryEntry{
		nova("Alimentação", "Feira", nil, "hortifruti"),
		nova("Alimentação", "feira", nil, "quitanda"),
	}}))
	require.NoError(t, err)
	assert.Equal(t, aiimport.OutcomeCreated, r.NewCategories[0].Outcome)
	assert.Equal(t, aiimport.OutcomeDuplicateInPayload, r.NewCategories[1].Outcome)
	feira := a.categoriaPorCaminho(t, a.casa.ID, "alimentacao > feira")
	require.NotNil(t, feira)
	assert.Equal(t, []string{"hortifruti"}, a.palavrasDe(t, a.casa.ID, feira.ID))
	cats, err := a.categorias.List(t.Context(), a.casa.ID, true)
	require.NoError(t, err)
	assert.Len(t, cats, 4)
}

// Extra exigido: categoryId de grupo com filha ativa é group_has_children, e
// o SetKeywords reconfere por dentro (podeReceberPalavras) — nada gravado.
func TestGrupoComFilhaAtivaNaoRecebePalavraNemPelaImportacao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{
		entrada(c.alimentacao.ID, "Alimentação", "comida"),
	}}))
	require.NoError(t, err)
	require.Len(t, r.Items, 1)
	assert.Equal(t, map[string]string{"comida": aiimport.RejectGroupHasChildren}, motivos(r.Items[0].Rejected))
	assert.Equal(t, antes, a.retrato(t, a.casa.ID))
}

// Critérios 34, 35 e 36: o impacto medido bate com o matcher direto, a
// medição não escreve nada, não aparece no confirm, e item de categoria não
// traz impact.
func TestCriterios34a36ImpactoMedido(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)

	// Janela: jul–set. "pagamento" alcança as três descrições com a palavra
	// inteira (5 lançamentos) e ainda "Nu Pagamentos" por erro de digitação
	// (2); "nu pagamentos" alcança 1 descrição (2 lançamentos); um lançamento
	// de OUTUBRO fica fora da janela; a outra casa não conta.
	descricoes := []struct {
		desc, mes, kind string
		n               int
	}{
		{"Pagamento de boleto", "2026-07", transaction.KindExpense, 3},
		{"PAGAMENTO fatura cartão", "2026-08", transaction.KindExpense, 1},
		{"Pix pagamento aluguel", "2026-09", transaction.KindExpense, 1},
		{"Nu Pagamentos SA", "2026-09", transaction.KindIncome, 2},
		{"Pagamento de outubro", "2026-10", transaction.KindExpense, 1},
		{"Zaffari centro", "2026-08", transaction.KindExpense, 2},
	}
	for _, d := range descricoes {
		for range d.n {
			a.lancar(t, a.casa.ID, d.kind, c.corrente, d.desc, d.mes)
		}
	}
	contaAlheia := a.conta(t, a.alheia.ID, "Conta alheia")
	a.lancar(t, a.alheia.ID, transaction.KindExpense, contaAlheia, "Pagamento alheio", "2026-08")
	antes := a.retrato(t, a.casa.ID)

	in := input(aiimport.Payload{
		AccountKeywords: []aiimport.AccountKeywordEntry{
			deConta(c.cartao, "Cartão", "pagamento"),
			deConta(c.corrente, "Conta Corrente", "nu pagamentos", "nubank"),
		},
		CategoryKeywords: []aiimport.CategoryKeywordEntry{entrada(c.mercado.ID, "Alimentação > Mercado", "zaffari centro")},
	})
	previa, err := a.svc.Preview(t.Context(), a.ator(), in)
	require.NoError(t, err)
	assert.Equal(t, antes, a.retrato(t, a.casa.ID), "medir não escreve")

	// Universo: 3+1+1+2+2 = 9 lançamentos vivos income/expense na janela.
	assert.Equal(t, 9, previa.Totals.PeriodTransactions)

	cartao, corrente, mercado := previa.Items[1], previa.Items[2], previa.Items[0]
	require.NotNil(t, cartao.Impact)
	// Contagem de referência, obtida rodando o matcher DIRETO.
	assert.Equal(t, contarDireto(t, "pagamento", descricoes), cartao.Impact.TransferCandidates)
	// 3 + 1 + 1 pela regra 1 (palavra inteira) MAIS os 2 de "Nu Pagamentos"
	// pela regra 3 ("pagamentos" está a uma edição de "pagamento") — é
	// exatamente o estrago que a medição existe para mostrar. A de outubro
	// fica fora da janela; a transferência e a outra casa não contam.
	assert.Equal(t, 7, cartao.Impact.TransferCandidates)
	assert.Equal(t, []aiimport.KeywordImpact{{Keyword: "pagamento", TransferCandidates: 7}}, cartao.Impact.ByKeyword)
	require.NotNil(t, corrente.Impact)
	assert.Equal(t, 2, corrente.Impact.TransferCandidates, "'nubank' já existia (skipped) e não conta; 'nu pagamentos' alcança 2")
	assert.Equal(t, []aiimport.KeywordImpact{{Keyword: "nu pagamentos", TransferCandidates: 2}}, corrente.Impact.ByKeyword,
		"só as palavras de added: a pulada não tem impacto a medir")
	assert.Nil(t, mercado.Impact, "palavra de CATEGORIA não traz impact")

	confirm, err := a.svc.Confirm(t.Context(), a.ator(), in)
	require.NoError(t, err)
	for _, item := range confirm.Items {
		assert.Nil(t, item.Impact, "impact não aparece no confirm")
	}
	assert.Equal(t, 9, confirm.Totals.PeriodTransactions, "o denominador vem nas duas rotas")
}

// contarDireto é a contagem de referência do critério 34: o matcher rodado
// à mão sobre as descrições da janela.
func contarDireto(t *testing.T, palavra string, descricoes []struct {
	desc, mes, kind string
	n               int
}) int {
	t.Helper()
	m, err := textmatch.NewMatcher([]textmatch.Keyword{{OwnerID: "x", Keyword: palavra}})
	require.NoError(t, err)
	total := 0
	for _, d := range descricoes {
		if d.mes < mesInicial || d.mes > mesFinal {
			continue
		}
		res, err := m.Best(textnorm.Normalize(d.desc))
		require.NoError(t, err)
		if res.Matched() {
			total += d.n
		}
	}
	return total
}

// Item de conta SEM palavra que entraria tem impact zero, e continua sendo
// só de conta.
func TestImpactoZeroQuandoNadaEntraria(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	a.lancar(t, a.casa.ID, transaction.KindExpense, c.corrente, "Nubank pagamento", "2026-08")

	previa, err := a.svc.Preview(t.Context(), a.ator(), input(aiimport.Payload{
		AccountKeywords: []aiimport.AccountKeywordEntry{
			deConta(c.corrente, "Conta Corrente", "nubank"),                // já presente
			deConta(c.cartao, "Cartão", "x"),                               // inválida
			deConta("018f0000-0000-7000-8000-00000000dead", "?", "nubank"), // não encontrada
		},
	}))
	require.NoError(t, err)
	for _, item := range previa.Items {
		require.NotNil(t, item.Impact, "todo item de conta traz impact na prévia")
		assert.Equal(t, 0, item.Impact.TransferCandidates)
		assert.NotNil(t, item.Impact.ByKeyword, "sempre presente: [] e não null")
		assert.Empty(t, item.Impact.ByKeyword)
	}
	assert.Equal(t, 1, previa.Totals.PeriodTransactions)
}

// A medição é POR PALAVRA, e o total do item é a UNIÃO: um item de conta que
// recebe uma palavra genérica e uma específica mostra os dois números — e a
// genérica salta aos olhos sozinha. A soma de `byKeyword` passa do total do
// item quando as duas alcançam o mesmo lançamento.
func TestImpactoPorPalavraETotalDoItemEhAUniao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	for _, d := range []struct {
		desc string
		n    int
	}{
		{"Pagamento de boleto", 3},
		{"PAGAMENTO fatura cartão", 1},
		{"Pix pagamento aluguel", 1},
		{"Nu Pagamentos SA", 2}, // alcançada pelas DUAS palavras
		{"Zaffari centro", 2},
	} {
		for range d.n {
			a.lancar(t, a.casa.ID, transaction.KindExpense, c.corrente, d.desc, "2026-08")
		}
	}

	previa, err := a.svc.Preview(t.Context(), a.ator(), input(aiimport.Payload{
		AccountKeywords: []aiimport.AccountKeywordEntry{deConta(c.cartao, "Cartão", "pagamento", "nu pagamentos")},
	}))
	require.NoError(t, err)
	impacto := previa.Items[0].Impact
	require.NotNil(t, impacto)
	assert.Equal(t, []aiimport.KeywordImpact{
		{Keyword: "pagamento", TransferCandidates: 7},     // 3 + 1 + 1 inteiras + 2 por erro de digitação
		{Keyword: "nu pagamentos", TransferCandidates: 2}, // só "Nu Pagamentos SA"
	}, impacto.ByKeyword, "uma entrada por palavra de added, na mesma ordem")
	assert.Equal(t, 7, impacto.TransferCandidates, "a união: os 2 de 'Nu Pagamentos' já estavam nos 7")
	assert.Greater(t, 7+2, impacto.TransferCandidates, "a soma por palavra passa do total do item")
	assert.Equal(t, 9, previa.Totals.PeriodTransactions)
}

// O orçamento de trabalho da medição é UM por operação e estourá-lo é erro
// (422 na borda), nunca uma medição parcial.
func TestOrcamentoDaMedicaoEstouraFechado(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t, aiimport.WithWorkBudget(1))
	c := a.casaComum(t)
	a.lancar(t, a.casa.ID, transaction.KindExpense, c.corrente, "Pagamento de boleto bancario", "2026-08")

	_, err := a.svc.Preview(t.Context(), a.ator(), input(aiimport.Payload{
		AccountKeywords: []aiimport.AccountKeywordEntry{deConta(c.cartao, "Cartão", "pagamento boleto")},
	}))
	require.ErrorIs(t, err, textmatch.ErrWorkBudgetExceeded)
}

// Prévia e confirm passam pelas MESMAS guardas de forma (400), na mesma
// ordem, antes de tocar no banco.
func TestFormaDoPayloadEh400NasDuasRotas(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	muitas := make([]aiimport.CategoryKeywordEntry, 0, aiimport.MaxEntriesPerList+1)
	for range aiimport.MaxEntriesPerList + 1 {
		muitas = append(muitas, entrada(c.mercado.ID, "Alimentação > Mercado", "feira"))
	}
	vinteEUma := make([]string, 0, 21)
	for i := range 21 {
		vinteEUma = append(vinteEUma, "palavra "+string(rune('a'+i)))
	}
	semVersao := input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{entrada(c.mercado.ID, "x", "y")}})
	semVersao.Payload.Version = nil
	dois := int64(2)
	versaoErrada := input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{entrada(c.mercado.ID, "x", "y")}})
	versaoErrada.Payload.Version = &dois

	casos := []struct {
		nome  string
		in    aiimport.Input
		campo string
	}{
		{"versão ausente", semVersao, "payload.homefinanceKeywordImport"},
		{"versão 2", versaoErrada, "payload.homefinanceKeywordImport"},
		{"listas vazias", input(aiimport.Payload{}), "payload"},
		{"201 entradas", input(aiimport.Payload{CategoryKeywords: muitas}), "payload.categoryKeywords"},
		{"21 palavras", input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{
			entrada(c.mercado.ID, "Alimentação > Mercado", vinteEUma...)}}), "payload.categoryKeywords[0].add"},
		{"janela invertida", aiimport.Input{FromMonth: mesFinal, ToMonth: mesInicial,
			Payload: aiimport.Payload{Version: versao(), CategoryKeywords: []aiimport.CategoryKeywordEntry{entrada(c.mercado.ID, "x", "y")}}}, ""},
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			_, errPrevia := a.svc.Preview(t.Context(), a.ator(), tc.in)
			_, errConfirm := a.svc.Confirm(t.Context(), a.ator(), tc.in)
			require.Error(t, errPrevia)
			require.Error(t, errConfirm)
			assert.Equal(t, errPrevia.Error(), errConfirm.Error(), "as duas rotas recusam igual")
			if tc.campo != "" {
				var pe *aiimport.PayloadError
				require.ErrorAs(t, errPrevia, &pe)
				assert.Equal(t, tc.campo, pe.Field)
			}
		})
	}
	assert.Equal(t, antes, a.retrato(t, a.casa.ID))
}

// A suíte de category e account não muda: AppendKeywords é a única extração,
// e as duas rotas de escrita existentes seguem passando por
// escreverComPalavras. Aqui, a prova de que AppendKeywords reconfere a CASA
// por dentro: um id de outra casa, chamado direto, é ErrNotFound — defesa em
// profundidade.
func TestAppendKeywordsReconfereACasaPorDentro(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum(t)
	alheia := a.grupo(t, a.alheia.ID, "Lazer", category.KindExpense)
	contaAlheia := a.conta(t, a.alheia.ID, "Conta alheia")

	err := a.uow.Do(t.Context(), func(ctx_ context.Context) error {
		return a.categoriaSvc.AppendKeywords(ctx_, a.atorCategoria(), alheia.ID,
			[]category.Keyword{{Keyword: "cinema", Norm: "cinema"}}, "category.updated")
	})
	require.ErrorIs(t, err, category.ErrNotFound)
	err = a.uow.Do(t.Context(), func(ctx_ context.Context) error {
		return a.contaSvc.AppendKeywords(ctx_, a.atorConta(), contaAlheia,
			[]account.Keyword{{Keyword: "itau", Norm: "itau"}}, "account.updated")
	})
	require.ErrorIs(t, err, account.ErrNotFound)
	assert.Empty(t, a.palavrasDe(t, a.alheia.ID, alheia.ID))
}

// Achado B1 do qa-testes, generalizado: para um payload DETERMINÍSTICO (nada
// muda por fora entre a prévia e o confirm), o confirm NUNCA é 409 — o plano
// simula o lote, e tudo o que `aplicar` reconfere por dentro já foi decidido
// contra o estado que o lote produz. Prévia e confirm concordam entrada a
// entrada, e o segundo confirm não muda nada.
func TestPlanoSimulaOLoteConfirmNuncaEh409ParaPayloadDeterministico(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)

	in := input(aiimport.Payload{
		NewCategories: []aiimport.NewCategoryEntry{
			nova("Transporte", "Ônibus", nil, "brt"),                               // tira a palavra do grupo Transporte
			nova("Alimentação", "Feira", nil, "zaffari", "hortifruti", "quitanda"), // zaffari é do Mercado; hortifruti ambígua
			nova("Alimentação", "Mercado", nil, "feira livre"),                     // merged: a mesma dona do item abaixo
		},
		CategoryKeywords: []aiimport.CategoryKeywordEntry{
			entrada(c.transporte.ID, "Transporte", "99 taxi"),
			entrada(c.alimentacao.ID, "Alimentação", "comida"), // grupo com filha ativa (Mercado)
			entrada(c.mercado.ID, "Alimentação > Mercado", "feira livre", "atacadao", "hortifruti"),
		},
		AccountKeywords: []aiimport.AccountKeywordEntry{
			deConta(c.corrente, "Conta Corrente", "nubank", "nu pagamentos"),
			deConta(c.cartao, "Cartão", "nu pagamentos"), // ambígua entre as duas contas
		},
	})

	previa, err := a.svc.Preview(t.Context(), a.ator(), in)
	require.NoError(t, err)
	r, err := a.svc.Confirm(t.Context(), a.ator(), in)
	require.False(t, errors.Is(err, aiimport.ErrConflict), "409 para payload determinístico (err=%v)", err)
	require.NoError(t, err)

	require.Len(t, r.NewCategories, len(previa.NewCategories))
	for i := range r.NewCategories {
		assert.Equal(t, previa.NewCategories[i].Outcome, r.NewCategories[i].Outcome, "nova %d", i)
		assert.Equal(t, previa.NewCategories[i].Add, r.NewCategories[i].Add, "nova %d", i)
		assert.Equal(t, motivos(previa.NewCategories[i].Rejected), motivos(r.NewCategories[i].Rejected), "nova %d", i)
	}
	require.Len(t, r.Items, len(previa.Items))
	for i := range r.Items {
		assert.Equal(t, previa.Items[i].Added, r.Items[i].Added, "item %d", i)
		assert.Equal(t, motivos(previa.Items[i].Rejected), motivos(r.Items[i].Rejected), "item %d", i)
	}

	// O que ficou no banco é o que o relatório disse.
	onibus := a.categoriaPorCaminho(t, a.casa.ID, "transporte > onibus")
	require.NotNil(t, onibus)
	assert.Equal(t, []string{"brt"}, a.palavrasDe(t, a.casa.ID, onibus.ID))
	assert.Equal(t, []string{"uber"}, a.palavrasDe(t, a.casa.ID, c.transporte.ID), "grupo que ganhou folha não ganhou palavra")
	feira := a.categoriaPorCaminho(t, a.casa.ID, "alimentacao > feira")
	require.NotNil(t, feira)
	assert.Equal(t, []string{"quitanda"}, a.palavrasDe(t, a.casa.ID, feira.ID))
	assert.Equal(t, []string{"zaffari", "feira livre", "atacadao"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID))
	assert.Empty(t, a.palavrasDe(t, a.casa.ID, c.alimentacao.ID))
	assert.Equal(t, []string{"nubank"}, a.palavrasDeConta(t, a.casa.ID, c.corrente), "nu pagamentos ambígua: em nenhuma")
	assert.Empty(t, a.palavrasDeConta(t, a.casa.ID, c.cartao))

	// Idempotente: o segundo confirm não muda nada e não é 409.
	depois := a.retrato(t, a.casa.ID)
	segundo, err := a.svc.Confirm(t.Context(), a.ator(), in)
	require.NoError(t, err)
	assert.Equal(t, 0, segundo.Totals.Added)
	assert.Equal(t, 0, segundo.Totals.CategoriesCreated)
	assert.Equal(t, depois, a.retrato(t, a.casa.ID))
}

// Achado A1 da revisão de segurança, no banco: desmarcar uma categoria na
// prévia não faz o confirm gravar nada que a prévia (sem pulo) tenha mostrado
// como recusado — a isca com palavras comuns, desmarcada, não libera as
// palavras para os itens reais; e a irmã que herdava o `kind` do grupo da
// desmarcada continua nascendo, com o grupo.
func TestA1ConfirmComPuloSoGravaOQueAPreviaMostrouComoEntra(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	exp := category.KindExpense
	antes := a.retrato(t, a.casa.ID)

	pl := aiimport.Payload{
		NewCategories: []aiimport.NewCategoryEntry{
			nova("Isca", "Isca", &exp, "drogaria", "posto ipiranga", "padaria nova"), // a isca
			nova("Saúde", "Farmácia", &exp, "panvel"),                                // dá o kind ao grupo Saúde
			nova("Saúde", "Dentista", nil, "odonto"),                                 // herda
		},
		CategoryKeywords: []aiimport.CategoryKeywordEntry{
			entrada(c.transporte.ID, "Transporte", "drogaria", "posto ipiranga", "99 taxi"),
			entrada(c.mercado.ID, "Alimentação > Mercado", "padaria nova"),
		},
	}
	previa, err := a.svc.Preview(t.Context(), a.ator(), input(pl))
	require.NoError(t, err)
	assert.Equal(t, []string{"99 taxi"}, previa.Items[0].Added, "só a que não colide com a isca")
	assert.Empty(t, previa.Items[1].Added)
	assert.Equal(t, antes, a.retrato(t, a.casa.ID))

	// A pessoa desmarca a isca E a Farmácia.
	r, err := a.svc.Confirm(t.Context(), a.ator(), input(pl, "isca > isca", "saude > farmacia"))
	require.NoError(t, err)

	assert.Equal(t, aiimport.OutcomeSkippedByUser, r.NewCategories[0].Outcome)
	assert.Equal(t, aiimport.OutcomeSkippedByUser, r.NewCategories[1].Outcome)
	dentistaConfirm := r.NewCategories[2]
	require.NotNil(t, dentistaConfirm.CategoryID, "no confirm a recém-nascida volta com id")
	dentistaConfirm.CategoryID = nil
	assert.Equal(t, previa.NewCategories[2], dentistaConfirm, "Dentista mantém o desfecho da prévia")
	assert.Equal(t, previa.Items[0], r.Items[0], "o item não desmarcado mantém o desfecho da prévia")
	assert.Equal(t, previa.Items[1], r.Items[1])

	// No banco: nem isca, nem Farmácia; Saúde nasceu (Dentista precisava);
	// Transporte só ganhou "99 taxi"; Mercado não ganhou nada.
	assert.Nil(t, a.categoriaPorCaminho(t, a.casa.ID, "isca"))
	assert.Nil(t, a.categoriaPorCaminho(t, a.casa.ID, "isca > isca"))
	assert.Nil(t, a.categoriaPorCaminho(t, a.casa.ID, "saude > farmacia"))
	saude := a.categoriaPorCaminho(t, a.casa.ID, "saude")
	require.NotNil(t, saude, "o grupo nasce porque a Dentista precisa dele")
	assert.Equal(t, category.KindExpense, saude.Kind, "com o kind que a Farmácia desmarcada deu na simulação")
	dentista := a.categoriaPorCaminho(t, a.casa.ID, "saude > dentista")
	require.NotNil(t, dentista)
	assert.Equal(t, []string{"odonto"}, a.palavrasDe(t, a.casa.ID, dentista.ID))
	assert.Equal(t, []string{"uber", "99 taxi"}, a.palavrasDe(t, a.casa.ID, c.transporte.ID))
	assert.Equal(t, []string{"zaffari"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID))
	kws, err := a.categorias.ListKeywords(t.Context(), a.casa.ID)
	require.NoError(t, err)
	for _, k := range kws {
		assert.NotContains(t, []string{"drogaria", "posto ipiranga", "padaria nova", "panvel"}, k.Norm,
			"palavra que a prévia recusou ou que era da desmarcada foi gravada")
	}
}

// Achado A3: o nome de categoria criada pelo import passa pela allowlist de
// runas visíveis — override bidirecional, controle e zero-width são
// `invalid_name`, e o nome que volta para a tela sai neutralizado.
func TestA3NomeDeCategoriaComRunaInvisivelEhInvalidName(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{NewCategories: []aiimport.NewCategoryEntry{
		nova("Alimentação", "Padaria‮", nil, "pao"),           // RIGHT-TO-LEFT OVERRIDE
		nova("Alimentação", "Pada​ria", nil, "pao"),           // ZERO WIDTH SPACE
		nova("Alimentação", "Padaria\x00", nil, "pao"),        // controle
		nova("Alimen tação", "Padaria", nil, "pao"),           // NBSP no grupo
		nova("Alimentação", "Padaria\tArtesanal", nil, "pao"), // tab: só o espaço simples passa
		nova("Alimentação", "Padaria Artesanal", nil, "pao"),  // válida, com espaço simples
		nova("Alimentação", "Confeitaria José", nil),         // NFD: a marca combinante desenha, passa
	}}))
	require.NoError(t, err)
	for i := range 5 {
		assert.Equal(t, aiimport.OutcomeInvalidName, r.NewCategories[i].Outcome, "entrada %d", i)
		assert.NotContains(t, r.NewCategories[i].Name, "‮")
		assert.NotContains(t, r.NewCategories[i].Name, "​")
		assert.NotContains(t, r.NewCategories[i].Name, "\x00")
	}
	assert.Equal(t, "Padaria", r.NewCategories[0].Name, "o RLO vira nada: o que a tela vê é o nome sem a carga")
	assert.Equal(t, "Pada ria", r.NewCategories[1].Name)
	assert.Equal(t, aiimport.OutcomeCreated, r.NewCategories[5].Outcome)
	assert.Equal(t, aiimport.OutcomeCreated, r.NewCategories[6].Outcome)
	require.NotNil(t, a.categoriaPorCaminho(t, a.casa.ID, "alimentacao > padaria artesanal"))
	assert.Equal(t, 2, r.Totals.CategoriesCreated)
	assert.NotEqual(t, antes, a.retrato(t, a.casa.ID))
	// Nenhum nome gravado carrega rune invisível.
	cats, err := a.categorias.List(t.Context(), a.casa.ID, true)
	require.NoError(t, err)
	for _, cat := range cats {
		for _, r := range cat.Name {
			assert.True(t, r == ' ' || aiprompt.Drawable(r), "rune invisível gravada no nome %q", cat.Name)
		}
	}
}
