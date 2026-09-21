package aiimport

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Os testes deste arquivo provam o PLANO — as regras das §§4.2–4.3 da spec
// 0010 como funções puras sobre um índice em memória. Sem banco, sem I/O:
// cada caso é um índice pequeno, um payload e o relatório esperado. Os
// critérios que exigem reler o banco (gravação, idempotência, transação,
// auditoria) estão em service_test.go, sobre SQLite real.

const (
	casa     = "casa-1"
	outra    = "casa-2"
	idGrupo  = "018f0000-0000-7000-8000-000000000001"
	idFolha  = "018f0000-0000-7000-8000-000000000002"
	idFolha2 = "018f0000-0000-7000-8000-000000000003"
	idArq    = "018f0000-0000-7000-8000-000000000004"
	idGrupo2 = "018f0000-0000-7000-8000-000000000005"
	idConta  = "018f0000-0000-7000-8000-0000000000a1"
	idConta2 = "018f0000-0000-7000-8000-0000000000a2"
	idAlheio = "018f0000-0000-7000-8000-0000000000ff"
)

var arquivadaEm = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

// casaPadrao monta a casa dos testes:
//
//	Alimentação (grupo, expense)
//	  ├─ Mercado  [zaffari, carrefour]
//	  └─ Padaria (ARQUIVADA) [padaria]
//	Transporte (grupo, expense, sem filhas) [uber]
//	Conta Corrente [nubank] · Cartão [ ]
func casaPadrao() *indiceDaCasa {
	return montarIndice(casa,
		[]category.Category{
			{ID: idGrupo, HouseholdID: casa, Name: "Alimentação", NameNorm: "alimentacao", Kind: category.KindExpense},
			{ID: idFolha, HouseholdID: casa, ParentID: ptr(idGrupo), Name: "Mercado", NameNorm: "mercado", Kind: category.KindExpense},
			{ID: idArq, HouseholdID: casa, ParentID: ptr(idGrupo), Name: "Padaria", NameNorm: "padaria", Kind: category.KindExpense, ArchivedAt: &arquivadaEm},
			{ID: idGrupo2, HouseholdID: casa, Name: "Transporte", NameNorm: "transporte", Kind: category.KindExpense},
		},
		[]category.Keyword{
			{HouseholdID: casa, CategoryID: idFolha, Keyword: "Zaffari", Norm: "zaffari", Position: 0},
			{HouseholdID: casa, CategoryID: idFolha, Keyword: "Carrefour", Norm: "carrefour", Position: 1},
			{HouseholdID: casa, CategoryID: idArq, Keyword: "padaria", Norm: "padaria", Position: 0},
			{HouseholdID: casa, CategoryID: idGrupo2, Keyword: "uber", Norm: "uber", Position: 0},
		},
		[]account.Account{
			{ID: idConta, HouseholdID: casa, Name: "Conta Corrente", NameNorm: "conta corrente"},
			{ID: idConta2, HouseholdID: casa, Name: "Cartão", NameNorm: "cartao"},
		},
		[]account.Keyword{
			{HouseholdID: casa, AccountID: idConta, Keyword: "nubank", Norm: "nubank", Position: 0},
		},
	)
}

func versao1() *int64 { v := FormatVersion; return &v }

func entrada(id, caminho string, add ...string) CategoryKeywordEntry {
	return CategoryKeywordEntry{CategoryID: id, CategoryPath: caminho, Add: add}
}

func nova(grupo, nome string, kind *string, add ...string) NewCategoryEntry {
	return NewCategoryEntry{Group: grupo, Name: nome, Kind: kind, Add: add}
}

func planoDe(ix *indiceDaCasa, pl Payload, pular ...string) *plano {
	pl.Version = versao1()
	return planejar(ix, Input{FromMonth: "2026-07", ToMonth: "2026-09", SkipNewCategories: pular, Payload: pl})
}

func motivos(rej []RejectedKeyword) map[string]string {
	out := map[string]string{}
	for _, r := range rej {
		out[r.Keyword] = r.Reason
	}
	return out
}

// --- §4.2, regras 3–5b: a entrada inteira ---------------------------------------

func TestItemDeOutraCasaEhItemNotFoundIndistinguivelDeInexistente(t *testing.T) {
	t.Parallel()
	ix := casaPadrao()

	// A "outra casa" não está no índice — que é exatamente o que List(casa
	// do token) garante. Um id inexistente e um id alheio produzem a MESMA
	// linha, byte a byte.
	p := planoDe(ix, Payload{CategoryKeywords: []CategoryKeywordEntry{
		entrada(idAlheio, "Alimentação > Mercado", "mercado do seu jose"),
		entrada("018f0000-0000-7000-8000-00000000dead", "Alimentação > Mercado", "mercado do seu jose"),
	}})
	r := p.relatorio(0)
	require.Len(t, r.Items, 2)
	a, b := r.Items[0], r.Items[1]
	a.ID, b.ID = "", ""
	assert.Equal(t, a, b, "outra casa e inexistente precisam ser indistinguíveis")
	assert.Nil(t, a.Name, "sem item resolvido não há nome do servidor")
	assert.Equal(t, map[string]string{"mercado do seu jose": RejectItemNotFound}, motivos(a.Rejected))
}

func TestIdForaDaFormaCanonicaEhItemNotFoundNunca400(t *testing.T) {
	t.Parallel()
	ix := casaPadrao()

	for _, id := range []string{"", "abc", " " + idFolha, idFolha + " ", "{" + idFolha + "}", strings.ToUpper(idFolha)} {
		p := planoDe(ix, Payload{CategoryKeywords: []CategoryKeywordEntry{entrada(id, "Alimentação > Mercado", "feira")}})
		r := p.relatorio(0)
		require.Len(t, r.Items, 1)
		assert.Equal(t, map[string]string{"feira": RejectItemNotFound}, motivos(r.Items[0].Rejected), "id %q", id)
		assert.Nil(t, r.Items[0].Name)
	}
	// O id NÃO canônico não é ecoado: texto que não é uuid não volta.
	p := planoDe(ix, Payload{CategoryKeywords: []CategoryKeywordEntry{entrada("<script>", "x", "feira")}})
	assert.Equal(t, "", p.relatorio(0).Items[0].ID)
	// O canônico é ecoado como veio, para a tela casar a linha.
	p = planoDe(ix, Payload{CategoryKeywords: []CategoryKeywordEntry{entrada(idFolha, "Alimentação > Mercado", "feira")}})
	assert.Equal(t, idFolha, p.relatorio(0).Items[0].ID)
}

func TestCategoriaArquivadaEhItemArchived(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{CategoryKeywords: []CategoryKeywordEntry{
		entrada(idArq, "Alimentação > Padaria", "pao quente"),
	}})
	item := p.relatorio(0).Items[0]
	assert.Equal(t, map[string]string{"pao quente": RejectItemArchived}, motivos(item.Rejected))
	require.NotNil(t, item.Name)
	assert.Equal(t, "Alimentação > Padaria", *item.Name, "o nome é o do SERVIDOR")
}

func TestCaminhoQueNaoBateEhNameMismatchComONomeDoServidor(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{CategoryKeywords: []CategoryKeywordEntry{
		entrada(idFolha, "Alimentação > Padaria", "feira"),
		entrada(idFolha, "Mercado", "feira"),
		entrada(idFolha, "", "feira"),
	}})
	for _, item := range p.relatorio(0).Items {
		assert.Equal(t, map[string]string{"feira": RejectNameMismatch}, motivos(item.Rejected))
		require.NotNil(t, item.Name)
		assert.Equal(t, "Alimentação > Mercado", *item.Name)
	}
}

func TestCaminhoComparadoNormalizadoAceitaAcentoCaixaEEspacos(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{CategoryKeywords: []CategoryKeywordEntry{
		entrada(idFolha, "  ALIMENTACAO  >  mercado ", "feira"),
	}})
	item := p.relatorio(0).Items[0]
	assert.Equal(t, []string{"feira"}, item.Added)
	assert.Empty(t, item.Rejected)
}

func TestGrupoComFilhaAtivaEhGroupHasChildren(t *testing.T) {
	t.Parallel()
	ix := casaPadrao()
	// Alimentação tem Mercado ATIVA (Padaria arquivada não conta sozinha).
	p := planoDe(ix, Payload{CategoryKeywords: []CategoryKeywordEntry{
		entrada(idGrupo, "Alimentação", "comida"),
		// Transporte é grupo SEM filha: recebe palavra (spec 0005 §12).
		entrada(idGrupo2, "Transporte", "99 taxi"),
	}})
	r := p.relatorio(0)
	assert.Equal(t, map[string]string{"comida": RejectGroupHasChildren}, motivos(r.Items[0].Rejected))
	assert.Equal(t, []string{"99 taxi"}, r.Items[1].Added)
}

func TestGrupoCujaUnicaFilhaEstaArquivadaVoltaAReceberPalavra(t *testing.T) {
	t.Parallel()
	ix := montarIndice(casa,
		[]category.Category{
			{ID: idGrupo, HouseholdID: casa, Name: "Saúde", NameNorm: "saude", Kind: category.KindExpense},
			{ID: idArq, HouseholdID: casa, ParentID: ptr(idGrupo), Name: "Farmácia", NameNorm: "farmacia", Kind: category.KindExpense, ArchivedAt: &arquivadaEm},
		}, nil, nil, nil)
	p := planoDe(ix, Payload{CategoryKeywords: []CategoryKeywordEntry{entrada(idGrupo, "Saúde", "drogaria")}})
	assert.Equal(t, []string{"drogaria"}, p.relatorio(0).Items[0].Added)
}

// --- §4.2, regras 6–11: palavra a palavra -------------------------------------------

func TestPalavraInvalidaEhInvalidKeywordComAFormaBrutaNeutralizada(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{CategoryKeywords: []CategoryKeywordEntry{
		entrada(idFolha, "Alimentação > Mercado",
			"x",                            // 1 runa
			strings.Repeat("a", 41),        // 41 runas
			"<b>mercado</b>",               // marcação
			"'; DROP TABLE categories; --", // fragmento de SQL
			"de ltda",                      // só palavras vazias
			"feira​boa",                    // invisível no meio
		),
	}})
	item := p.relatorio(0).Items[0]
	assert.Empty(t, item.Added)
	require.Len(t, item.Rejected, 6)
	for _, r := range item.Rejected {
		assert.Equal(t, RejectInvalidKeyword, r.Reason)
		assert.LessOrEqual(t, len([]rune(r.Keyword)), 40, "truncada em 40 runas")
		assert.NotContains(t, r.Keyword, "​", "invisível neutralizado")
	}
	assert.Equal(t, "x", item.Rejected[0].Keyword)
	assert.Equal(t, strings.Repeat("a", 40), item.Rejected[1].Keyword)
	assert.Equal(t, "<b>mercado</b>", item.Rejected[2].Keyword, "a forma bruta volta para a tela apontar a linha")
	assert.Equal(t, "feira boa", item.Rejected[5].Keyword, "o que não desenha vira espaço")
}

func TestPalavraJaNoItemEhAlreadyPresentEReimportarEhInofensivo(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{CategoryKeywords: []CategoryKeywordEntry{
		entrada(idFolha, "Alimentação > Mercado", "ZAFFARI", "carrefour", "feira"),
	}})
	item := p.relatorio(0).Items[0]
	assert.Equal(t, []string{"feira"}, item.Added)
	assert.Equal(t, []SkippedKeyword{{"ZAFFARI", SkipAlreadyPresent}, {"carrefour", SkipAlreadyPresent}}, item.Skipped)
	assert.Empty(t, item.Rejected)
}

func TestPalavraDeOutraCategoriaEhKeywordTakenComADona(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{CategoryKeywords: []CategoryKeywordEntry{
		entrada(idGrupo2, "Transporte", "zaffari", "padaria", "99 taxi"),
	}})
	item := p.relatorio(0).Items[0]
	assert.Equal(t, []string{"99 taxi"}, item.Added, "as demais entram normalmente")
	require.Len(t, item.Rejected, 2)
	assert.Equal(t, RejectedKeyword{Keyword: "zaffari", Reason: RejectKeywordTaken, OwnerID: idFolha}, item.Rejected[0])
	// A dona ARQUIVADA continua ocupando a vaga do índice único.
	assert.Equal(t, RejectedKeyword{Keyword: "padaria", Reason: RejectKeywordTaken, OwnerID: idArq}, item.Rejected[1])
}

func TestConjuntosDeCategoriaEDeContaSaoIndependentes(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{
		CategoryKeywords: []CategoryKeywordEntry{entrada(idGrupo2, "Transporte", "nubank")},
		AccountKeywords:  []AccountKeywordEntry{{AccountID: idConta2, AccountName: "Cartão", Add: []string{"zaffari"}}},
	})
	r := p.relatorio(0)
	assert.Equal(t, []string{"nubank"}, r.Items[0].Added, "palavra de conta pode estar numa categoria")
	assert.Equal(t, []string{"zaffari"}, r.Items[1].Added, "palavra de categoria pode estar numa conta")
	// Mas em DUAS contas, não.
	p = planoDe(casaPadrao(), Payload{AccountKeywords: []AccountKeywordEntry{
		{AccountID: idConta2, AccountName: "Cartão", Add: []string{"nubank"}},
	}})
	assert.Equal(t, RejectedKeyword{Keyword: "nubank", Reason: RejectKeywordTaken, OwnerID: idConta},
		p.relatorio(0).Items[0].Rejected[0])
}

func TestMesmaPalavraEmDoisItensDoJSONRecusaAsDuas(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{
		NewCategories:    []NewCategoryEntry{nova("Alimentação", "Feira", nil, "hortifruti")},
		CategoryKeywords: []CategoryKeywordEntry{entrada(idGrupo2, "Transporte", "Hortifrúti", "99 taxi")},
	})
	r := p.relatorio(0)
	assert.Equal(t, map[string]string{"hortifruti": RejectAmbiguousInPayload}, motivos(r.NewCategories[0].Rejected))
	assert.Equal(t, map[string]string{"Hortifrúti": RejectAmbiguousInPayload}, motivos(r.Items[0].Rejected))
	assert.Equal(t, []string{"99 taxi"}, r.Items[0].Added)
	assert.Equal(t, OutcomeCreated, r.NewCategories[0].Outcome, "a categoria continua nascendo, só sem a palavra")
	assert.Equal(t, 2, r.Totals.Rejected)
}

func TestAmbiguidadeNaoAlcancaConjuntoDiferenteNemAMesmaDona(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{
		CategoryKeywords: []CategoryKeywordEntry{
			entrada(idGrupo2, "Transporte", "cabify"),
			entrada(idGrupo2, "Transporte", "cabify"), // mesma dona duas vezes: não é ambíguo
		},
		AccountKeywords: []AccountKeywordEntry{{AccountID: idConta2, AccountName: "Cartão", Add: []string{"cabify"}}},
	})
	r := p.relatorio(0)
	assert.Equal(t, []string{"cabify"}, r.Items[0].Added)
	assert.Equal(t, []SkippedKeyword{{"cabify", SkipAlreadyPresent}}, r.Items[1].Skipped, "entra pela primeira entrada")
	assert.Equal(t, []string{"cabify"}, r.Items[2].Added, "conta é outro conjunto")
}

func TestRepetidaNoMesmoItemDeduplicaEmSilencio(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{CategoryKeywords: []CategoryKeywordEntry{
		entrada(idGrupo2, "Transporte", "cabify", "CABIFY", "Cabífy"),
	}})
	item := p.relatorio(0).Items[0]
	assert.Equal(t, []string{"cabify"}, item.Added)
	assert.Empty(t, item.Skipped)
	assert.Empty(t, item.Rejected)
}

// Critério 15 (corrigido pelo achado A5): 18 + 3 → entram 2; 19 + 3 → entra 1.
func TestTetoDe20RecusaAsExcedentesNaOrdemDoJSON(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		existentes, entram, sobram int
	}{
		{18, 2, 1},
		{19, 1, 2},
		{20, 0, 3},
		{17, 3, 0},
	} {
		kws := make([]category.Keyword, 0, tc.existentes)
		for i := range tc.existentes {
			w := "palavra" + string(rune('a'+i))
			kws = append(kws, category.Keyword{HouseholdID: casa, CategoryID: idGrupo2, Keyword: w, Norm: w, Position: i})
		}
		ix := montarIndice(casa, []category.Category{
			{ID: idGrupo2, HouseholdID: casa, Name: "Transporte", NameNorm: "transporte", Kind: category.KindExpense},
		}, kws, nil, nil)

		p := planoDe(ix, Payload{CategoryKeywords: []CategoryKeywordEntry{
			entrada(idGrupo2, "Transporte", "nova um", "nova dois", "nova tres"),
		}})
		item := p.relatorio(0).Items[0]
		assert.Len(t, item.Added, tc.entram, "%d existentes", tc.existentes)
		assert.Len(t, item.Rejected, tc.sobram, "%d existentes", tc.existentes)
		for _, r := range item.Rejected {
			assert.Equal(t, RejectLimitExceeded, r.Reason)
		}
		// Nunca truncado em silêncio: adicionadas + recusadas = 3.
		assert.Equal(t, 3, len(item.Added)+len(item.Rejected))
		if tc.sobram > 0 {
			assert.Equal(t, "nova tres", item.Rejected[len(item.Rejected)-1].Keyword, "a última do JSON é a última recusada")
		}
	}
}

// --- §4.3: categoria nova ------------------------------------------------------------

func TestGrupoExistenteMaisFolhaNovaHerdaANaturezaEKindDivergenteEhMismatch(t *testing.T) {
	t.Parallel()
	inc := category.KindIncome
	exp := category.KindExpense
	p := planoDe(casaPadrao(), Payload{NewCategories: []NewCategoryEntry{
		nova("alimentação", "Feira", nil, "hortifruti"),
		nova("Alimentação", "Açougue", &exp, "acougue do ze"),
		nova("Alimentação", "Peixaria", &inc, "peixaria"),
		nova("Alimentação", "Quitanda", ptr("foo"), "quitanda"),
	}})
	r := p.relatorio(0)
	require.Len(t, r.NewCategories, 4)

	feira := r.NewCategories[0]
	assert.Equal(t, OutcomeCreated, feira.Outcome)
	assert.Equal(t, "alimentacao > feira", feira.Ref)
	assert.Equal(t, "Alimentação", feira.Group, "o nome exibível do grupo é o do SERVIDOR")
	assert.Equal(t, "Feira", feira.Name)
	require.NotNil(t, feira.Kind)
	assert.Equal(t, category.KindExpense, *feira.Kind, "herdada do grupo")
	assert.False(t, feira.GroupIsNew)
	assert.Nil(t, feira.CategoryID, "na prévia a categoria ainda não existe")
	assert.Equal(t, []string{"hortifruti"}, feira.Add)

	assert.Equal(t, OutcomeCreated, r.NewCategories[1].Outcome, "kind igual ao do grupo não é divergência")
	assert.Equal(t, OutcomeKindMismatch, r.NewCategories[2].Outcome)
	assert.Equal(t, OutcomeKindMismatch, r.NewCategories[3].Outcome, "kind fora do conjunto também diverge do grupo")
	assert.Empty(t, r.NewCategories[2].Add, "entrada recusada não lista palavra")
	assert.Equal(t, 2, r.Totals.CategoriesCreated)
}

func TestGrupoNovoExigeKindDoConjuntoFechado(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		kind    *string
		outcome string
	}{
		{nil, OutcomeKindRequired},
		{ptr(""), OutcomeInvalidKind},
		{ptr("despesa"), OutcomeInvalidKind},
		{ptr("EXPENSE"), OutcomeInvalidKind},
		{ptr(category.KindExpense), OutcomeCreated},
	} {
		p := planoDe(casaPadrao(), Payload{NewCategories: []NewCategoryEntry{
			nova("Saúde", "Farmácia", tc.kind, "drogaria"),
		}})
		m := p.relatorio(0).NewCategories[0]
		assert.Equal(t, tc.outcome, m.Outcome)
		assert.True(t, m.GroupIsNew)
		if tc.outcome == OutcomeCreated {
			require.NotNil(t, m.Kind)
			assert.Equal(t, category.KindExpense, *m.Kind)
			require.Len(t, p.gruposNovos, 1)
			assert.Equal(t, category.KindExpense, p.gruposNovos[0].kind)
		} else {
			assert.Nil(t, m.Kind, "recusada antes de haver natureza a resolver")
			assert.Empty(t, m.Add)
			assert.Empty(t, p.gruposNovos)
		}
	}
}

func TestOPrimeiroBlocoTomaOCaminhoMesmoQuandoRecusado(t *testing.T) {
	t.Parallel()
	// "O primeiro vale, os demais são duplicate_in_payload" (§4.3, regra 8)
	// é literal: o primeiro bloco bem nomeado toma o caminho, com o desfecho
	// que tiver. Um JSON com o mesmo `group > name` duas vezes é a IA se
	// contradizendo, e a resposta é apontar a contradição — não escolher por
	// ela o bloco que "daria certo".
	p := planoDe(casaPadrao(), Payload{NewCategories: []NewCategoryEntry{
		nova("Saúde", "Farmácia", nil, "drogaria"),
		nova("Saúde", "Farmácia", ptr(category.KindExpense), "drogaria"),
	}})
	r := p.relatorio(0)
	assert.Equal(t, OutcomeKindRequired, r.NewCategories[0].Outcome)
	assert.Equal(t, OutcomeDuplicateInPayload, r.NewCategories[1].Outcome)
	assert.Empty(t, p.gruposNovos)
}

func TestGrupoNovoCompartilhadoPorDuasFolhasNasceUmaVez(t *testing.T) {
	t.Parallel()
	exp := category.KindExpense
	inc := category.KindIncome
	p := planoDe(casaPadrao(), Payload{NewCategories: []NewCategoryEntry{
		nova("Saúde", "Farmácia", &exp, "drogaria"),
		nova("saude", "Dentista", nil, "odonto"),
		nova("Saúde", "Plano", &inc, "unimed"),
	}})
	r := p.relatorio(0)
	assert.Equal(t, OutcomeCreated, r.NewCategories[0].Outcome)
	assert.Equal(t, OutcomeCreated, r.NewCategories[1].Outcome, "kind ausente herda do grupo planejado")
	assert.Equal(t, category.KindExpense, *r.NewCategories[1].Kind)
	assert.Equal(t, "Saúde", r.NewCategories[1].Group, "o nome exibível é o da primeira entrada")
	assert.Equal(t, OutcomeKindMismatch, r.NewCategories[2].Outcome, "diverge do grupo planejado")
	require.Len(t, p.gruposNovos, 1)
}

func TestFolhaQueJaExisteAtivaEhMergedIntoExisting(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{NewCategories: []NewCategoryEntry{
		nova("Alimentação", "MERCADO", nil, "zaffari", "feira", "uber"),
	}})
	m := p.relatorio(0).NewCategories[0]
	assert.Equal(t, OutcomeMergedIntoExisting, m.Outcome)
	require.NotNil(t, m.CategoryID)
	assert.Equal(t, idFolha, *m.CategoryID)
	assert.Equal(t, "Mercado", m.Name, "nome exibível do SERVIDOR")
	// As palavras passam por TODAS as validações da §4.2 contra a existente.
	assert.Equal(t, []string{"feira"}, m.Add)
	assert.Equal(t, []SkippedKeyword{{"zaffari", SkipAlreadyPresent}}, m.Skipped)
	assert.Equal(t, []RejectedKeyword{{Keyword: "uber", Reason: RejectKeywordTaken, OwnerID: idGrupo2}}, m.Rejected)
	assert.Equal(t, 0, p.relatorio(0).Totals.CategoriesCreated, "merged não conta como criação")
}

func TestNomeDeArquivadaNoMesmoPaiEhNameTakenArchived(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{NewCategories: []NewCategoryEntry{
		nova("Alimentação", "padaria", nil, "pao"),
	}})
	m := p.relatorio(0).NewCategories[0]
	assert.Equal(t, OutcomeNameTakenArchived, m.Outcome)
	assert.Equal(t, "Padaria", m.Name)
	assert.Nil(t, m.CategoryID)
	assert.Empty(t, m.Add)
}

func TestGrupoArquivadoEhNameTakenArchived(t *testing.T) {
	t.Parallel()
	ix := montarIndice(casa, []category.Category{
		{ID: idGrupo, HouseholdID: casa, Name: "Lazer", NameNorm: "lazer", Kind: category.KindExpense, ArchivedAt: &arquivadaEm},
	}, nil, nil, nil)
	exp := category.KindExpense
	p := planoDe(ix, Payload{NewCategories: []NewCategoryEntry{nova("Lazer", "Cinema", &exp, "cinemark")}})
	m := p.relatorio(0).NewCategories[0]
	assert.Equal(t, OutcomeNameTakenArchived, m.Outcome, "folha ativa em grupo invisível é o estado que ErrParentArchived impede")
	assert.False(t, m.GroupIsNew)
	assert.Empty(t, p.gruposNovos)
}

func TestGrupoAtivoVenceOArquivadoDeMesmoNome(t *testing.T) {
	t.Parallel()
	ix := montarIndice(casa, []category.Category{
		{ID: idGrupo, HouseholdID: casa, Name: "Lazer", NameNorm: "lazer", Kind: category.KindExpense, ArchivedAt: &arquivadaEm},
		{ID: idGrupo2, HouseholdID: casa, Name: "Lazer", NameNorm: "lazer", Kind: category.KindExpense},
	}, nil, nil, nil)
	p := planoDe(ix, Payload{NewCategories: []NewCategoryEntry{nova("Lazer", "Cinema", nil, "cinemark")}})
	m := p.relatorio(0).NewCategories[0]
	assert.Equal(t, OutcomeCreated, m.Outcome)
	assert.Equal(t, idGrupo2, p.novas[0].grupoID)
}

func TestNomeInvalidoInclusiveComSeparadorEhInvalidName(t *testing.T) {
	t.Parallel()
	exp := category.KindExpense
	p := planoDe(casaPadrao(), Payload{NewCategories: []NewCategoryEntry{
		nova("", "Feira", &exp),
		nova("Alimentação", "   ", &exp),
		nova("Alimentação", strings.Repeat("x", 61), &exp),
		nova("A > B", "Feira", &exp),
		nova("Alimentação", "Feira\n> x", &exp),
	}})
	r := p.relatorio(0)
	for i, m := range r.NewCategories {
		assert.Equal(t, OutcomeInvalidName, m.Outcome, "entrada %d", i)
		assert.Nil(t, m.Kind)
		assert.LessOrEqual(t, len([]rune(m.Group)), 60)
		assert.LessOrEqual(t, len([]rune(m.Name)), 60)
		// O ref continua bem formado: `lado > lado`, sem `>` dentro dos lados.
		lados := strings.Split(m.Ref, separadorDeCaminho)
		require.Len(t, lados, 2, "ref %q", m.Ref)
		assert.NotEmpty(t, lados[0])
		assert.NotEmpty(t, lados[1])
	}
	assert.Equal(t, "A > B", r.NewCategories[3].Group, "a forma do cliente volta neutralizada para a tela apontar a linha")
	assert.Equal(t, "Feira > x", r.NewCategories[4].Name, "quebra de linha vira espaço")
}

func TestCasaCom199CategoriasRecebendo3EntraUmaEDuasSaoHouseholdLimit(t *testing.T) {
	t.Parallel()
	cats := []category.Category{
		{ID: idGrupo, HouseholdID: casa, Name: "Alimentação", NameNorm: "alimentacao", Kind: category.KindExpense},
	}
	for i := 1; i < category.MaxPerHousehold-1; i++ {
		nome := "G" + strings.Repeat("x", i%50) + string(rune('a'+i%26)) + string(rune('a'+i/26))
		cats = append(cats, category.Category{
			ID:          "018f0000-0000-7000-8000-" + strings.Repeat("0", 8) + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + "00",
			HouseholdID: casa, Name: nome, NameNorm: textnorm.Normalize(nome), Kind: category.KindExpense,
		})
	}
	ix := montarIndice(casa, cats, nil, nil, nil)
	// Os ids sintéticos acima podem colidir entre si; o que importa é o
	// TOTAL contado a partir do que o mapa reteve.
	ix.totalCategorias = category.MaxPerHousehold - 1

	p := planoDe(ix, Payload{NewCategories: []NewCategoryEntry{
		nova("Alimentação", "Feira", nil, "feira"),
		nova("Alimentação", "Açougue", nil, "acougue"),
		nova("Alimentação", "Peixaria", nil, "peixaria"),
	}})
	r := p.relatorio(0)
	assert.Equal(t, OutcomeCreated, r.NewCategories[0].Outcome)
	assert.Equal(t, OutcomeHouseholdLimit, r.NewCategories[1].Outcome)
	assert.Equal(t, OutcomeHouseholdLimit, r.NewCategories[2].Outcome)
	assert.Empty(t, r.NewCategories[1].Add, "sem categoria, sem palavra")
	assert.Equal(t, 1, r.Totals.CategoriesCreated)
}

func TestGrupoNovoPrecisaDeDuasVagasEUmaFolhaEmGrupoExistentePassaNaFrente(t *testing.T) {
	t.Parallel()
	ix := casaPadrao()
	ix.totalCategorias = category.MaxPerHousehold - 1 // uma vaga
	exp := category.KindExpense
	p := planoDe(ix, Payload{NewCategories: []NewCategoryEntry{
		nova("Saúde", "Farmácia", &exp, "drogaria"), // grupo novo + folha: 2 vagas
		nova("Alimentação", "Feira", nil, "feira"),  // folha em grupo existente: 1 vaga
	}})
	r := p.relatorio(0)
	assert.Equal(t, OutcomeHouseholdLimit, r.NewCategories[0].Outcome, "o grupo só nasce como continente da folha")
	assert.Equal(t, OutcomeCreated, r.NewCategories[1].Outcome)
	assert.Empty(t, p.gruposNovos)
}

func TestCategoriaDesmarcadaNaoNasceEAsPalavrasSomemJunto(t *testing.T) {
	t.Parallel()
	exp := category.KindExpense
	pl := Payload{
		NewCategories: []NewCategoryEntry{
			nova("Saúde", "Farmácia", &exp, "drogaria"),
			nova("Saúde", "Dentista", nil, "odonto"),
		},
		CategoryKeywords: []CategoryKeywordEntry{entrada(idGrupo2, "Transporte", "drogaria")},
	}
	previa := planoDe(casaPadrao(), pl).relatorio(0)
	p := planoDe(casaPadrao(), pl, "saude > farmacia", "Saúde > Farmácia", "ref-que-nao-existe")
	r := p.relatorio(0)

	f := r.NewCategories[0]
	assert.Equal(t, OutcomeSkippedByUser, f.Outcome)
	assert.Empty(t, f.Add)
	assert.Empty(t, f.Rejected)
	assert.Nil(t, f.Kind)
	assert.Nil(t, f.CategoryID)
	_, temDona := p.donas[prefixoDeDonaNova+"saude > farmacia"]
	assert.True(t, temDona, "a dona existe na simulação...")
	assert.Empty(t, p.donas[prefixoDeDonaNova+"saude > farmacia"].aprovadas, "...mas nada dela é aprovado para escrita")

	// A entrada desmarcada continua ocupando o lugar dela na simulação
	// (achado A1): Dentista herda o `kind` do grupo que a Farmácia planejou
	// — exatamente como na prévia sem pulo — e o grupo nasce porque Dentista
	// precisa dele.
	assert.Equal(t, OutcomeCreated, r.NewCategories[1].Outcome)
	assert.Equal(t, previa.NewCategories[1], r.NewCategories[1], "a irmã não muda de desfecho pelo pulo")
	require.Len(t, p.gruposNovos, 1)
	assert.True(t, p.gruposNovos[0].necessario)
	assert.Equal(t, 1, r.Totals.CategoriesCreated)

	// E "drogaria" continua AMBÍGUA para o Transporte: a prévia a recusou, e
	// desmarcar a Farmácia não pode fazê-la entrar num item que a pessoa
	// nunca desmarcou.
	assert.Equal(t, previa.Items[0], r.Items[0])
	assert.Empty(t, r.Items[0].Added)
	assert.Equal(t, map[string]string{"drogaria": RejectAmbiguousInPayload}, motivos(r.Items[0].Rejected))
}

// Grupo planejado SÓ por entradas desmarcadas não nasce — mas ocupou as
// vagas e deu a natureza na simulação.
func TestGrupoPlanejadoSoPorDesmarcadasNaoNasce(t *testing.T) {
	t.Parallel()
	exp := category.KindExpense
	p := planoDe(casaPadrao(), Payload{NewCategories: []NewCategoryEntry{
		nova("Saúde", "Farmácia", &exp, "drogaria"),
		nova("Saúde", "Dentista", nil, "odonto"),
	}}, "saude > farmacia", "saude > dentista")
	r := p.relatorio(0)
	assert.Equal(t, OutcomeSkippedByUser, r.NewCategories[0].Outcome)
	assert.Equal(t, OutcomeSkippedByUser, r.NewCategories[1].Outcome)
	require.Len(t, p.gruposNovos, 1)
	assert.False(t, p.gruposNovos[0].necessario, "ninguém marcado precisa do grupo: ele não é criado")
	assert.Equal(t, 0, r.Totals.CategoriesCreated)
}

func TestDoisBlocosComOMesmoCaminhoOPrimeiroVale(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{NewCategories: []NewCategoryEntry{
		nova("Alimentação", "Feira", nil, "hortifruti"),
		nova("ALIMENTAÇÃO", "feira", nil, "quitanda"),
	}})
	r := p.relatorio(0)
	assert.Equal(t, OutcomeCreated, r.NewCategories[0].Outcome)
	assert.Equal(t, OutcomeDuplicateInPayload, r.NewCategories[1].Outcome)
	assert.Empty(t, r.NewCategories[1].Add)
	assert.Equal(t, 1, r.Totals.CategoriesCreated)
}

func TestCategoriaExistenteComSeparadorNoNomeNaoQuebraOImport(t *testing.T) {
	t.Parallel()
	// A assimetria com o POST /categories (que aceita `>`) NÃO é resolvida
	// aqui. O que se garante: uma categoria existente com `>` no nome (1) é
	// alcançável por ID em `categoryKeywords`, com o caminho comparado por
	// inteiro; (2) nunca é alvo de `newCategories`, porque `group`/`name`
	// com `>` é `invalid_name` — recusa clara, nunca um `ref` ambíguo.
	ix := montarIndice(casa, []category.Category{
		{ID: idGrupo, HouseholdID: casa, Name: "Casa > Obras", NameNorm: "casa > obras", Kind: category.KindExpense},
		{ID: idFolha, HouseholdID: casa, ParentID: ptr(idGrupo), Name: "Pedreiro", NameNorm: "pedreiro", Kind: category.KindExpense},
	}, nil, nil, nil)
	p := planoDe(ix, Payload{
		CategoryKeywords: []CategoryKeywordEntry{entrada(idFolha, "Casa > Obras > Pedreiro", "mestre de obras")},
		NewCategories:    []NewCategoryEntry{nova("Casa > Obras", "Eletricista", nil, "eletricista")},
	})
	r := p.relatorio(0)
	assert.Equal(t, []string{"mestre de obras"}, r.Items[0].Added)
	assert.Equal(t, "Casa > Obras > Pedreiro", *r.Items[0].Name)
	assert.Equal(t, OutcomeInvalidName, r.NewCategories[0].Outcome)
}

// --- o relatório ---------------------------------------------------------------------

func TestTotaisSaoASomaDasListas(t *testing.T) {
	t.Parallel()
	exp := category.KindExpense
	p := planoDe(casaPadrao(), Payload{
		NewCategories: []NewCategoryEntry{
			nova("Saúde", "Farmácia", &exp, "drogaria", "x"),
			nova("Alimentação", "Mercado", nil, "zaffari", "feira"),
		},
		CategoryKeywords: []CategoryKeywordEntry{
			entrada(idGrupo2, "Transporte", "uber", "99 taxi", "zaffari"),
			entrada(idAlheio, "Nada", "a", "b"),
		},
		AccountKeywords: []AccountKeywordEntry{
			{AccountID: idConta, AccountName: "Conta Corrente", Add: []string{"nubank", "nu pagamentos"}},
		},
	})
	r := p.relatorio(212)
	assert.Equal(t, Totals{CategoriesCreated: 1, Added: 4, Skipped: 3, Rejected: 4, PeriodTransactions: 212}, r.Totals)
	// Listas nascem `[]`, nunca nulas — o contrato as marca required.
	for _, m := range r.NewCategories {
		assert.NotNil(t, m.Add)
		assert.NotNil(t, m.Skipped)
		assert.NotNil(t, m.Rejected)
	}
	for _, it := range r.Items {
		assert.NotNil(t, it.Added)
		assert.NotNil(t, it.Skipped)
		assert.NotNil(t, it.Rejected)
		assert.Nil(t, it.Impact, "o plano puro não mede impacto")
	}
}

func TestIndiceRecusaLinhaDeOutraCasaMesmoQueAFonteAVaze(t *testing.T) {
	t.Parallel()
	ix := montarIndice(casa,
		[]category.Category{{ID: idAlheio, HouseholdID: outra, Name: "Alheia", NameNorm: "alheia", Kind: category.KindExpense}},
		[]category.Keyword{{HouseholdID: outra, CategoryID: idAlheio, Keyword: "alheia", Norm: "alheia"}},
		[]account.Account{{ID: idConta, HouseholdID: outra, Name: "Alheia", NameNorm: "alheia"}},
		[]account.Keyword{{HouseholdID: outra, AccountID: idConta, Keyword: "alheia", Norm: "alheia"}},
	)
	assert.Empty(t, ix.categoriasPorID)
	assert.Empty(t, ix.contasPorID)
	assert.Empty(t, ix.donoDaPalavraDeCategoria)
	assert.Empty(t, ix.donoDaPalavraDeConta)
	assert.Equal(t, 0, ix.totalCategorias)
	assert.Equal(t, 4, ix.linhasDeOutraCasaIgnoradas)
}

func TestNeutralizarTruncaEmRunasENuncaNoMeioDeUmCaractere(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "ção", neutralizar("ção\x00\x01", 3))
	assert.Equal(t, "a b", neutralizar("  a \t\n b  ", 10))
	assert.Equal(t, "", neutralizar("​​", 10))
	assert.Equal(t, strings.Repeat("é", 40), neutralizar(strings.Repeat("é", 45), 40))
}

// --- o plano é uma simulação do lote (achado B1 do qa-testes) ---------------------

// A regra 5b enxerga a folha que o PRÓPRIO lote cria: grupo sem filhas que
// ganha uma folha `created` neste JSON não recebe palavra — recusada no
// plano, e não descoberta por podeReceberPalavras no confirm (que seria um
// 409 determinístico, com a prévia verde).
func TestPlanoSimulaOLoteFolhaCriadaTiraPalavraDoGrupo(t *testing.T) {
	t.Parallel()
	ix := casaPadrao() // Transporte: grupo SEM filhas, [uber]

	p := planoDe(ix, Payload{
		NewCategories:    []NewCategoryEntry{nova("Transporte", "Ônibus", nil, "brt")},
		CategoryKeywords: []CategoryKeywordEntry{entrada(idGrupo2, "Transporte", "99 taxi")},
	})
	r := p.relatorio(0)
	assert.Equal(t, OutcomeCreated, r.NewCategories[0].Outcome)
	assert.Equal(t, []string{"brt"}, r.NewCategories[0].Add)
	require.Len(t, r.Items, 1)
	assert.Equal(t, map[string]string{"99 taxi": RejectGroupHasChildren}, motivos(r.Items[0].Rejected))
	assert.Empty(t, r.Items[0].Added)
	_, temDona := p.donas[idGrupo2]
	assert.False(t, temDona, "nada a gravar no grupo: o confirm nem chama SetKeywords para ele")
}

// Só a folha que a SIMULAÇÃO cria tira a palavra do grupo: sem vaga ou
// recusada, o grupo continua sem filha e recebe a palavra. A folha
// DESMARCADA continua tirando — ela ocupa o lugar dela na simulação (achado
// A1): a prévia sem pulo recusou a palavra do grupo por causa dela, e o
// confirm com o pulo não pode passar a gravá-la.
func TestPlanoSimulaOLoteSoAFolhaQueNasceTiraAPalavraDoGrupo(t *testing.T) {
	t.Parallel()
	inc := category.KindIncome
	for _, tc := range []struct {
		nome    string
		payload Payload
		pular   []string
		ajustar func(*indiceDaCasa)
		outcome string
	}{
		{"sem vaga", Payload{
			NewCategories:    []NewCategoryEntry{nova("Transporte", "Ônibus", nil, "brt")},
			CategoryKeywords: []CategoryKeywordEntry{entrada(idGrupo2, "Transporte", "99 taxi")},
		}, nil, func(ix *indiceDaCasa) { ix.totalCategorias = category.MaxPerHousehold }, OutcomeHouseholdLimit},
		{"natureza divergente", Payload{
			NewCategories:    []NewCategoryEntry{nova("Transporte", "Ônibus", &inc, "brt")},
			CategoryKeywords: []CategoryKeywordEntry{entrada(idGrupo2, "Transporte", "99 taxi")},
		}, nil, nil, OutcomeKindMismatch},
	} {
		t.Run(tc.nome, func(t *testing.T) {
			ix := casaPadrao()
			if tc.ajustar != nil {
				tc.ajustar(ix)
			}
			r := planoDe(ix, tc.payload, tc.pular...).relatorio(0)
			assert.Equal(t, tc.outcome, r.NewCategories[0].Outcome)
			require.Len(t, r.Items, 1)
			assert.Equal(t, []string{"99 taxi"}, r.Items[0].Added, "sem folha nova, o grupo recebe a palavra")
		})
	}

	t.Run("desmarcada continua tirando", func(t *testing.T) {
		r := planoDe(casaPadrao(), Payload{
			NewCategories:    []NewCategoryEntry{nova("Transporte", "Ônibus", nil, "brt")},
			CategoryKeywords: []CategoryKeywordEntry{entrada(idGrupo2, "Transporte", "99 taxi")},
		}, "transporte > onibus").relatorio(0)
		assert.Equal(t, OutcomeSkippedByUser, r.NewCategories[0].Outcome)
		require.Len(t, r.Items, 1)
		assert.Equal(t, map[string]string{"99 taxi": RejectGroupHasChildren}, motivos(r.Items[0].Rejected))
	})
}

// Palavra de folha `created` que já pertence a uma categoria existente é
// `keyword_taken` no plano — e não um 409 do KeywordOwners no confirm.
// Palavra que o lote daria à folha nova E a uma existente é ambígua nas
// duas. Palavra dada duas vezes à mesma dona (merged + item) entra uma vez.
func TestPlanoSimulaOLoteDonasDePalavraDepoisDoLote(t *testing.T) {
	t.Parallel()
	p := planoDe(casaPadrao(), Payload{
		NewCategories: []NewCategoryEntry{
			nova("Alimentação", "Feira", nil, "zaffari", "hortifruti", "quitanda"),
			nova("Alimentação", "Mercado", nil, "feira livre"),
		},
		CategoryKeywords: []CategoryKeywordEntry{
			entrada(idGrupo2, "Transporte", "hortifruti"),
			entrada(idFolha, "Alimentação > Mercado", "feira livre", "atacadao"),
		},
	})
	r := p.relatorio(0)

	feira := r.NewCategories[0]
	assert.Equal(t, OutcomeCreated, feira.Outcome)
	assert.Equal(t, []string{"quitanda"}, feira.Add)
	assert.Equal(t, map[string]string{
		"zaffari":    RejectKeywordTaken,       // já é do Mercado existente
		"hortifruti": RejectAmbiguousInPayload, // o lote a daria também ao Transporte
	}, motivos(feira.Rejected))
	require.Len(t, feira.Rejected, 2)
	assert.Equal(t, idFolha, feira.Rejected[0].OwnerID)
	assert.Equal(t, map[string]string{"hortifruti": RejectAmbiguousInPayload}, motivos(r.Items[0].Rejected))

	// Mercado aparece DUAS vezes (merged + item): uma dona só no plano, a
	// palavra repetida entra pela primeira e é already_present na segunda.
	assert.Equal(t, []string{"feira livre"}, r.NewCategories[1].Add)
	assert.Equal(t, []string{"atacadao"}, r.Items[1].Added)
	assert.Equal(t, []SkippedKeyword{{"feira livre", SkipAlreadyPresent}}, r.Items[1].Skipped)
	require.NotNil(t, p.donas[idFolha])
	assert.Len(t, p.donas[idFolha].aprovadas, 2, "feira livre + atacadao, uma união só")
}

// --- achado A1: o pulo não muda o desfecho de quem não foi desmarcado --------------

// escritasDo devolve o CONJUNTO do que o plano escreveria: as folhas que
// nascem (pelo ref), os grupos que nascem (pela norma) e cada (dona, palavra)
// aprovada. É contra este conjunto que o invariante do pulo é conferido.
func escritasDo(p *plano) map[string]struct{} {
	out := map[string]struct{}{}
	for i := range p.novas {
		if p.novas[i].view.Outcome == OutcomeCreated {
			out["folha:"+p.novas[i].view.Ref] = struct{}{}
		}
	}
	for i := range p.gruposNovos {
		if p.gruposNovos[i].necessario {
			out["grupo:"+p.gruposNovos[i].norm] = struct{}{}
		}
	}
	for chave, d := range p.donas {
		for _, a := range d.aprovadas {
			out["palavra:"+chave+"|"+a.norm] = struct{}{}
		}
	}
	return out
}

func subconjunto(t *testing.T, menor, maior map[string]struct{}, msg string) {
	t.Helper()
	for k := range menor {
		_, ok := maior[k]
		assert.True(t, ok, "%s: %q seria gravado sem ter aparecido como 'entra' na prévia", msg, k)
	}
}

// Os três cenários que o revisor confirmou (achado A1), um a um.
func TestA1DesmarcarNaoMudaODesfechoDasEntradasNaoDesmarcadas(t *testing.T) {
	t.Parallel()
	exp := category.KindExpense

	t.Run("teto: B não ganha a vaga da A desmarcada", func(t *testing.T) {
		ix := casaPadrao()
		ix.totalCategorias = category.MaxPerHousehold - 2 // duas vagas
		pl := Payload{NewCategories: []NewCategoryEntry{
			nova("Saúde", "Farmácia", &exp, "drogaria"), // grupo novo + folha: 2 vagas
			nova("Alimentação", "Feira", nil, "feira"),  // folha em grupo existente: 1 vaga
		}}
		previa := planoDe(ix, pl)
		assert.Equal(t, OutcomeCreated, previa.novas[0].view.Outcome)
		assert.Equal(t, OutcomeHouseholdLimit, previa.novas[1].view.Outcome)

		p := planoDe(ix, pl, "saude > farmacia")
		assert.Equal(t, OutcomeSkippedByUser, p.novas[0].view.Outcome)
		assert.Equal(t, OutcomeHouseholdLimit, p.novas[1].view.Outcome, "B nunca teve caixa para desmarcar: continua sem vaga")
		assert.Empty(t, escritasDo(p), "nada nasce: o grupo era só da A")
		subconjunto(t, escritasDo(p), escritasDo(previa), "teto")
	})

	t.Run("kind: B herda do grupo que a A desmarcada planejou", func(t *testing.T) {
		pl := Payload{NewCategories: []NewCategoryEntry{
			nova("Saúde", "Farmácia", &exp, "drogaria"),
			nova("Saúde", "Dentista", nil, "odonto"),
		}}
		previa := planoDe(casaPadrao(), pl)
		p := planoDe(casaPadrao(), pl, "saude > farmacia")
		assert.Equal(t, OutcomeSkippedByUser, p.novas[0].view.Outcome)
		assert.Equal(t, previa.novas[1].view, p.novas[1].view, "B continua created, expense")
		assert.Equal(t, category.KindExpense, *p.novas[1].view.Kind)
		subconjunto(t, escritasDo(p), escritasDo(previa), "kind")
		assert.Contains(t, escritasDo(p), "grupo:saude", "o grupo nasce porque B precisa dele")
		assert.NotContains(t, escritasDo(p), "folha:saude > farmacia")
	})

	t.Run("isca: a palavra que a prévia recusou não é gravada", func(t *testing.T) {
		// A exploração: uma categoria-isca com palavras comuns e as MESMAS
		// palavras em itens reais. A prévia mostra tudo ambíguo; a pessoa
		// desmarca a isca; o confirm NÃO pode gravar as palavras nos itens.
		pl := Payload{
			NewCategories: []NewCategoryEntry{nova("Isca", "Isca", &exp, "drogaria", "padaria nova", "posto")},
			CategoryKeywords: []CategoryKeywordEntry{
				entrada(idGrupo2, "Transporte", "drogaria", "posto"),
				entrada(idFolha, "Alimentação > Mercado", "padaria nova"),
			},
		}
		previa := planoDe(casaPadrao(), pl)
		assert.Empty(t, previa.itens[0].view.Added)
		assert.Empty(t, previa.itens[1].view.Added)

		p := planoDe(casaPadrao(), pl, "isca > isca")
		assert.Equal(t, OutcomeSkippedByUser, p.novas[0].view.Outcome)
		assert.Equal(t, previa.itens[0].view, p.itens[0].view)
		assert.Equal(t, previa.itens[1].view, p.itens[1].view)
		assert.Equal(t, map[string]string{"drogaria": RejectAmbiguousInPayload, "posto": RejectAmbiguousInPayload},
			motivos(p.itens[0].view.Rejected))
		assert.Empty(t, escritasDo(p))
	})
}

// O INVARIANTE, para todo subconjunto de pulos: escritas(confirm) ⊆
// entra(prévia sem pulo), e as entradas não desmarcadas (e todos os itens)
// mantêm o desfecho da prévia. Enumeração exaustiva dos 2⁸ subconjuntos de
// um payload que cruza grupo novo, herança de kind, merged, teto, ambiguidade
// e teto de palavras — e depois subconjuntos aleatórios sobre um payload
// maior, com semente fixa.
func TestA1InvarianteDoPuloParaTodoSubconjunto(t *testing.T) {
	t.Parallel()
	exp := category.KindExpense
	inc := category.KindIncome

	pl := Payload{
		NewCategories: []NewCategoryEntry{
			nova("Saúde", "Farmácia", &exp, "drogaria", "panvel"),
			nova("Saúde", "Dentista", nil, "odonto", "drogaria"),
			nova("Saúde", "Plano", &inc, "unimed"),
			nova("Alimentação", "Feira", nil, "hortifruti", "zaffari"),
			nova("Alimentação", "Mercado", nil, "atacadao", "carrefour"),
			nova("Transporte", "Ônibus", nil, "brt"),
			nova("Lazer", "Cinema", nil, "cinemark"),
			nova("Alimentação", "Padaria", nil, "pao"),
		},
		CategoryKeywords: []CategoryKeywordEntry{
			entrada(idGrupo2, "Transporte", "99 taxi", "brt"),
			entrada(idFolha, "Alimentação > Mercado", "atacadao", "feira", "hortifruti"),
		},
		AccountKeywords: []AccountKeywordEntry{
			deContaP(idConta, "Conta Corrente", "nu pagamentos"),
		},
	}
	ix := casaPadrao()
	ix.totalCategorias = category.MaxPerHousehold - 5

	previa := planoDe(ix, pl)
	entra := escritasDo(previa)
	refs := make([]string, 0, len(previa.novas))
	for i := range previa.novas {
		refs = append(refs, previa.novas[i].view.Ref)
	}
	require.Len(t, refs, 8)

	for mascara := 0; mascara < 1<<len(refs); mascara++ {
		var pular []string
		for i, ref := range refs {
			if mascara&(1<<i) != 0 {
				pular = append(pular, ref)
			}
		}
		p := planoDe(ix, pl, pular...)
		subconjunto(t, escritasDo(p), entra, fmt.Sprintf("máscara %08b", mascara))
		for i := range p.novas {
			if mascara&(1<<i) != 0 {
				assert.Equal(t, OutcomeSkippedByUser, p.novas[i].view.Outcome, "máscara %08b, entrada %d", mascara, i)
				continue
			}
			assert.Equal(t, previa.novas[i].view, p.novas[i].view, "máscara %08b, entrada %d não desmarcada mudou", mascara, i)
		}
		for i := range p.itens {
			assert.Equal(t, previa.itens[i].view, p.itens[i].view, "máscara %08b, item %d mudou", mascara, i)
		}
	}
}

// deContaP é o helper de entrada de conta do pacote interno.
func deContaP(id, nome string, add ...string) AccountKeywordEntry {
	return AccountKeywordEntry{AccountID: id, AccountName: nome, Add: add}
}

// Subconjuntos ALEATÓRIOS sobre um payload maior (60 entradas novas, 20
// itens), com semente fixa para o caso que falhar ser reproduzível.
func TestA1InvarianteDoPuloEmSubconjuntosAleatorios(t *testing.T) {
	t.Parallel()
	exp := category.KindExpense
	rng := rand.New(rand.NewPCG(2026, 921))

	grupos := []string{"Saúde", "Alimentação", "Transporte", "Lazer", "Casa"}
	pl := Payload{}
	palavras := []string{}
	for i := range 60 {
		grupo := grupos[i%len(grupos)]
		var kind *string
		if i%3 == 0 {
			kind = &exp
		}
		// Palavras repetidas entre entradas de propósito: ambiguidade,
		// already_present e teto entram em jogo.
		add := []string{fmt.Sprintf("palavra %c%c", 'a'+i%7, 'a'+i%5), fmt.Sprintf("marca %d", i%9)}
		palavras = append(palavras, add...)
		pl.NewCategories = append(pl.NewCategories, nova(grupo, fmt.Sprintf("Folha %d", i%23), kind, add...))
	}
	for i := range 20 {
		id := idFolha
		caminho := "Alimentação > Mercado"
		if i%2 == 0 {
			id, caminho = idGrupo2, "Transporte"
		}
		pl.CategoryKeywords = append(pl.CategoryKeywords, entrada(id, caminho, palavras[(i*7)%len(palavras)], fmt.Sprintf("so minha %d", i)))
	}
	ix := casaPadrao()
	ix.totalCategorias = category.MaxPerHousehold - 12

	previa := planoDe(ix, pl)
	entra := escritasDo(previa)
	refs := make([]string, 0, len(previa.novas))
	for i := range previa.novas {
		refs = append(refs, previa.novas[i].view.Ref)
	}

	for rodada := range 300 {
		// O pulo é por REF: um ref duplicado no JSON é pulado em todos os
		// blocos que o carregam.
		pular := map[string]bool{}
		for _, ref := range refs {
			if rng.IntN(3) == 0 {
				pular[ref] = true
			}
		}
		lista := make([]string, 0, len(pular))
		for ref := range pular {
			lista = append(lista, ref)
		}
		p := planoDe(ix, pl, lista...)
		subconjunto(t, escritasDo(p), entra, fmt.Sprintf("rodada %d", rodada))
		for i := range p.novas {
			if pular[refs[i]] {
				assert.Equal(t, OutcomeSkippedByUser, p.novas[i].view.Outcome, "rodada %d, entrada %d", rodada, i)
				continue
			}
			assert.Equal(t, previa.novas[i].view, p.novas[i].view, "rodada %d, entrada %d não desmarcada mudou", rodada, i)
		}
		for i := range p.itens {
			assert.Equal(t, previa.itens[i].view, p.itens[i].view, "rodada %d, item %d mudou", rodada, i)
		}
	}
}
