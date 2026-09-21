package aiimport_test

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/aiimport"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Testes escritos pelo QA na validação da fatia E9b (spec 0010), depois de
// mutar o código de produção e ver quais mutantes SOBREVIVERAM à suíte
// original. Cada teste aqui é a resposta a um sobrevivente ou a um alvo de
// ataque que a suíte não cobria:
//
//   - o confirm é chamado DUAS vezes ao mesmo tempo (corrida real, não
//     simulada por dublê);
//   - Create nunca recebe Keywords (mutante 6 sobreviveu);
//   - ErrTooMany vindo do Create é `household_limit` e o lote continua
//     (mutante 9b sobreviveu);
//   - a categoria desmarcada é pulada ANTES de qualquer resolução de grupo
//     (mutante 3 só foi pego por uma asserção lateral);
//   - a falha no meio de um lote de 5 folhas não deixa as 2 anteriores;
//   - a palavra hostil recusada volta neutralizada e truncada;
//   - `periodTransactions` bate com um COUNT direto no banco;
//   - campo desconhecido e caixa trocada em QUALQUER nível do payload é 400.

// --- corrida real ---------------------------------------------------------------------

// confirmarEmParalelo dispara n confirms do MESMO serviço ao mesmo tempo e
// devolve os relatórios e os erros na ordem das goroutines. O SQLite do
// harness tem UMA conexão, então as transações serializam no Begin — o que
// se prova aqui é que a SEGUNDA relê o estado que a primeira gravou, em vez
// de escrever por cima com um índice velho.
func (a *ambiente) confirmarEmParalelo(t *testing.T, ins ...aiimport.Input) ([]aiimport.Report, []error) {
	t.Helper()
	relatorios := make([]aiimport.Report, len(ins))
	erros := make([]error, len(ins))
	var wg sync.WaitGroup
	for i := range ins {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			relatorios[i], erros[i] = a.svc.Confirm(t.Context(), a.ator(), ins[i])
		}(i)
	}
	wg.Wait()
	return relatorios, erros
}

// Duas confirmações CONCORRENTES com o MESMO JSON: uma grava, a outra vê o
// estado gravado (tudo merged/already_present) ou toma 409 — nunca uma
// segunda categoria com o mesmo nome, nunca uma palavra duplicada.
func TestQACorridaConfirmComOMesmoJSONNaoDuplicaNada(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	exp := category.KindExpense
	in := input(aiimport.Payload{
		NewCategories:    []aiimport.NewCategoryEntry{nova("Saúde", "Farmácia", &exp, "drogaria")},
		CategoryKeywords: []aiimport.CategoryKeywordEntry{entrada(c.mercado.ID, "Alimentação > Mercado", "feira")},
		AccountKeywords:  []aiimport.AccountKeywordEntry{deConta(c.corrente, "Conta Corrente", "nu pagamentos")},
	})

	rs, errs := a.confirmarEmParalelo(t, in, in)

	criadas, conflitos := 0, 0
	for i := range rs {
		switch {
		case errs[i] == nil:
			criadas += rs[i].Totals.CategoriesCreated
		case errors.Is(errs[i], aiimport.ErrConflict):
			conflitos++
		default:
			t.Fatalf("confirm %d falhou com erro que não é 409: %v", i, errs[i])
		}
	}
	assert.Equal(t, 1, criadas, "exatamente UMA execução criou a categoria")
	assert.LessOrEqual(t, conflitos, 1)

	// O banco: um Saúde, uma Farmácia, e nenhuma palavra repetida.
	cats, err := a.categorias.List(t.Context(), a.casa.ID, true)
	require.NoError(t, err)
	porNorm := map[string]int{}
	for _, cat := range cats {
		porNorm[cat.NameNorm]++
	}
	assert.Equal(t, 1, porNorm["saude"], "grupo duplicado")
	assert.Equal(t, 1, porNorm["farmacia"], "folha duplicada")
	assert.Len(t, cats, 5, "3 da casa comum + Saúde + Farmácia")

	farmacia := a.categoriaPorCaminho(t, a.casa.ID, "saude > farmacia")
	require.NotNil(t, farmacia)
	assert.Equal(t, []string{"drogaria"}, a.palavrasDe(t, a.casa.ID, farmacia.ID))
	assert.Equal(t, []string{"zaffari", "feira"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID))
	assert.Equal(t, []string{"nubank", "nu pagamentos"}, a.palavrasDeConta(t, a.casa.ID, c.corrente))

	kws, err := a.categorias.ListKeywords(t.Context(), a.casa.ID)
	require.NoError(t, err)
	vistas := map[string]int{}
	for _, k := range kws {
		vistas[k.CategoryID+"|"+k.Norm]++
		assert.Equal(t, 1, vistas[k.CategoryID+"|"+k.Norm], "palavra %q gravada duas vezes na mesma dona", k.Norm)
	}
}

// Duas confirmações CONCORRENTES com palavras DIFERENTES para os mesmos
// itens: as duas têm de sobreviver no banco (a segunda relê o estado DENTRO
// da transação e faz a união sobre ele), e o teto de 20 vale para a soma das
// duas — nunca 22, nunca uma palavra da primeira apagada pela união velha da
// segunda.
func TestQACorridaConfirmComPalavrasDiferentesUneAsDuasENaoPassaDe20(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	dezoito := make([]string, 0, 18)
	for i := range 18 {
		dezoito = append(dezoito, "palavra "+string(rune('a'+i)))
	}
	_, err := a.categoriaSvc.Update(t.Context(), a.atorCategoria(), c.transporte.ID, category.UpdateInput{Keywords: &dezoito})
	require.NoError(t, err)

	inA := input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{
		entrada(c.transporte.ID, "Transporte", "nova a1", "nova a2"),
		entrada(c.mercado.ID, "Alimentação > Mercado", "feira"),
	}})
	inB := input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{
		entrada(c.transporte.ID, "Transporte", "nova b1", "nova b2"),
		entrada(c.mercado.ID, "Alimentação > Mercado", "carrefour"),
	}})

	rs, errs := a.confirmarEmParalelo(t, inA, inB)

	transporte := a.palavrasDe(t, a.casa.ID, c.transporte.ID)
	assert.Len(t, transporte, category.MaxKeywordsPerOwner, "nunca mais que 20: %v", transporte)
	mercado := a.palavrasDe(t, a.casa.ID, c.mercado.ID)

	// Toda palavra que um relatório disse ter ADICIONADO está no banco — é
	// exatamente o que um índice lido FORA da transação quebraria: a união
	// velha da segunda apagaria as da primeira.
	for i, r := range rs {
		if errs[i] != nil {
			require.ErrorIs(t, errs[i], aiimport.ErrConflict, "confirm %d", i)
			continue
		}
		for _, item := range r.Items {
			for _, adicionada := range item.Added {
				switch item.ID {
				case c.transporte.ID:
					assert.Contains(t, transporte, adicionada, "confirm %d disse que adicionou %q e o banco não tem", i, adicionada)
				case c.mercado.ID:
					assert.Contains(t, mercado, adicionada, "confirm %d disse que adicionou %q e o banco não tem", i, adicionada)
				}
			}
		}
	}

	// Sem 409, as duas entradas de Mercado entraram e o Transporte ficou
	// exatamente em 20, com a perdedora reportando limit_exceeded.
	if errs[0] == nil && errs[1] == nil {
		sort.Strings(mercado)
		assert.Equal(t, []string{"carrefour", "feira", "zaffari"}, mercado)
		excedentes := 0
		for _, r := range rs {
			for _, item := range r.Items {
				if item.ID == c.transporte.ID {
					for _, rej := range item.Rejected {
						if rej.Reason == aiimport.RejectLimitExceeded {
							excedentes++
						}
					}
				}
			}
		}
		assert.Equal(t, 2, excedentes, "a segunda a chegar recusa as 2 dela por teto")
	}
}

// --- mutante 6: Create nunca recebe Keywords ------------------------------------------

// A spec (§10.4) manda criar a folha com Keywords nil e gravar TODAS as
// palavras por SetKeywords: dois caminhos de palavra divergiriam. O banco
// fica igual dos dois jeitos — por isso a prova é na CHAMADA, não no
// resultado.
func TestQACreateNuncaRecebeKeywordsNoImport(t *testing.T) {
	t.Parallel()
	escritor := &escritorDeCategoria{}
	a := novoAmbienteComEscritor(t, escritor)
	a.casaComum(t)
	exp := category.KindExpense

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{NewCategories: []aiimport.NewCategoryEntry{
		nova("Saúde", "Farmácia", &exp, "drogaria", "panvel"),
		nova("Alimentação", "Feira", nil, "hortifruti"),
	}}))
	require.NoError(t, err)
	assert.Equal(t, 2, r.Totals.CategoriesCreated)

	escritor.mu.Lock()
	defer escritor.mu.Unlock()
	require.Len(t, escritor.entradas, 3, "grupo Saúde + Farmácia + Feira")
	for i, in := range escritor.entradas {
		assert.Nil(t, in.Keywords, "Create #%d (%q) recebeu Keywords — as palavras têm de entrar só por SetKeywords", i, in.Name)
	}
	assert.Equal(t, "Saúde", escritor.entradas[0].Name, "o grupo nasce primeiro")
	assert.Nil(t, escritor.entradas[0].ParentID)
	assert.Equal(t, "Farmácia", escritor.entradas[1].Name)
	assert.NotNil(t, escritor.entradas[1].ParentID)

	farmacia := a.categoriaPorCaminho(t, a.casa.ID, "saude > farmacia")
	require.NotNil(t, farmacia)
	assert.Equal(t, []string{"drogaria", "panvel"}, a.palavrasDe(t, a.casa.ID, farmacia.ID), "e mesmo assim as palavras chegaram")
}

// --- mutante 9b: ErrTooMany do Create é household_limit e o lote continua ----------------

// O plano já conta as vagas, então Create só devolve ErrTooMany quando o teto
// foi atingido ENTRE a leitura e a escrita (corrida). A regra do serviço é: é
// o único erro que Create devolve antes de qualquer escrita, então o lote
// CONTINUA, a entrada vira `household_limit` e o resto grava.
func TestQATetoAtingidoNoCreateViraHouseholdLimitEOLoteContinua(t *testing.T) {
	t.Parallel()
	escritor := &escritorDeCategoria{falharNa: 1, erroForca: category.ErrTooMany}
	a := novoAmbienteComEscritor(t, escritor)
	c := a.casaComum(t)
	exp := category.KindExpense

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{
		NewCategories: []aiimport.NewCategoryEntry{
			nova("Saúde", "Farmácia", &exp, "drogaria"), // o grupo Saúde é a 1ª criação: ErrTooMany
			nova("Alimentação", "Feira", nil, "hortifruti"),
		},
		CategoryKeywords: []aiimport.CategoryKeywordEntry{entrada(c.mercado.ID, "Alimentação > Mercado", "feira")},
	}))
	require.NoError(t, err, "ErrTooMany não derruba o lote")
	assert.False(t, errors.Is(err, aiimport.ErrConflict))

	farmacia, feira := r.NewCategories[0], r.NewCategories[1]
	assert.Equal(t, aiimport.OutcomeHouseholdLimit, farmacia.Outcome, "rebaixada no confirm")
	assert.Nil(t, farmacia.CategoryID)
	assert.Empty(t, farmacia.Add, "sem categoria, sem palavra")
	assert.Equal(t, aiimport.OutcomeCreated, feira.Outcome, "o lote continuou")
	assert.Equal(t, 1, r.Totals.CategoriesCreated)

	assert.Nil(t, a.categoriaPorCaminho(t, a.casa.ID, "saude"))
	assert.Nil(t, a.categoriaPorCaminho(t, a.casa.ID, "saude > farmacia"))
	require.NotNil(t, a.categoriaPorCaminho(t, a.casa.ID, "alimentacao > feira"))
	assert.Equal(t, []string{"zaffari", "feira"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID), "as palavras dos outros itens entraram")
	kws, err := a.categorias.ListKeywords(t.Context(), a.casa.ID)
	require.NoError(t, err)
	for _, k := range kws {
		assert.NotEqual(t, "drogaria", k.Norm, "a palavra da categoria que não nasceu não foi para lugar nenhum")
	}
	assert.Contains(t, a.auditoria.acoes(), "ai.keyword_import_confirmed", "a execução valeu e foi auditada")
}

// --- mutante 3: o pulo precede QUALQUER resolução de grupo ------------------------------

// A pessoa desmarcou; o que o servidor responde é `skipped_by_user`, e não
// o desfecho que a entrada teria se fosse avaliada (grupo arquivado, kind
// divergente). Se o pulo viesse depois, a tela mostraria um erro numa linha
// que a pessoa já tirou da jogada.
func TestQACategoriaDesmarcadaEhSkippedMesmoQuandoTeriaOutroDesfecho(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum(t)
	lazer := a.grupo(t, a.casa.ID, "Lazer", category.KindExpense)
	a.arquivar(t, a.casa.ID, lazer.ID)
	inc := category.KindIncome
	antes := a.retrato(t, a.casa.ID)

	pl := aiimport.Payload{NewCategories: []aiimport.NewCategoryEntry{
		nova("Lazer", "Cinema", nil, "cinemark"),          // sem pulo: name_taken_archived
		nova("Alimentação", "Peixaria", &inc, "peixaria"), // sem pulo: kind_mismatch
		nova("Saúde", "Farmácia", nil, "drogaria"),        // sem pulo: kind_required
	}}
	for _, rota := range []string{"preview", "confirm"} {
		var r aiimport.Report
		var err error
		in := input(pl, "lazer > cinema", "alimentacao > peixaria", "saude > farmacia")
		if rota == "preview" {
			r, err = a.svc.Preview(t.Context(), a.ator(), in)
		} else {
			r, err = a.svc.Confirm(t.Context(), a.ator(), in)
		}
		require.NoError(t, err, rota)
		for i, m := range r.NewCategories {
			assert.Equal(t, aiimport.OutcomeSkippedByUser, m.Outcome, "%s: entrada %d", rota, i)
			assert.Nil(t, m.Kind, "%s: entrada %d", rota, i)
			assert.Nil(t, m.CategoryID)
			assert.Empty(t, m.Add)
			assert.Empty(t, m.Rejected)
		}
		assert.Equal(t, 0, r.Totals.Rejected)
	}
	assert.Equal(t, antes, a.retrato(t, a.casa.ID))
}

// --- transação: falha na 3ª de 5 folhas não deixa as 2 anteriores -------------------------

func TestQAFalhaNaTerceiraDeCincoFolhasNaoDeixaAsDuasAnteriores(t *testing.T) {
	t.Parallel()
	escritor := &escritorDeCategoria{falharNa: 3, erroForca: errors.New("falha forçada na terceira folha")}
	a := novoAmbienteComEscritor(t, escritor)
	c := a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	novas := make([]aiimport.NewCategoryEntry, 0, 5)
	for _, n := range []string{"Feira", "Açougue", "Peixaria", "Padaria", "Quitanda"} {
		novas = append(novas, nova("Alimentação", n, nil, strings.ToLower(n)+" xyz"))
	}
	_, err := a.svc.Confirm(t.Context(), a.ator(), input(aiimport.Payload{
		NewCategories:    novas,
		CategoryKeywords: []aiimport.CategoryKeywordEntry{entrada(c.mercado.ID, "Alimentação > Mercado", "carrefour")},
		AccountKeywords:  []aiimport.AccountKeywordEntry{deConta(c.corrente, "Conta Corrente", "nu pagamentos")},
	}))
	require.Error(t, err)
	assert.Equal(t, 3, escritor.criacoes, "Feira e Açougue já tinham sido gravadas quando Peixaria falhou")

	assert.Equal(t, antes, a.retrato(t, a.casa.ID), "nada das duas anteriores, nenhuma palavra de item existente")
	assert.Nil(t, a.categoriaPorCaminho(t, a.casa.ID, "alimentacao > feira"))
	assert.Nil(t, a.categoriaPorCaminho(t, a.casa.ID, "alimentacao > acougue"))
	assert.Equal(t, []string{"zaffari"}, a.palavrasDe(t, a.casa.ID, c.mercado.ID))
	assert.Equal(t, []string{"nubank"}, a.palavrasDeConta(t, a.casa.ID, c.corrente))
	assert.NotContains(t, a.auditoria.acoes(), "ai.keyword_import_confirmed")
	total, err := a.categorias.CountAll(t.Context(), a.casa.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 3, total)
}

// --- palavra hostil recusada: neutralizada e truncada ---------------------------------------

func TestQAPalavraHostilRecusadaVoltaNeutralizadaETruncadaEm40(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)

	const rlo = "\u202E" // RIGHT-TO-LEFT OVERRIDE
	hostis := []string{
		"abc" + rlo + "def",           // override bidi no meio
		"a|b|c",                       // separador de tabela
		strings.Repeat("é", 200),      // 200 runas multibyte
		"feira\x00\x1b[31mvermelha",   // NUL e escape ANSI
		"drog\u200Baria\u200D\uFEFF!", // zero-width e BOM
	}
	r, err := a.svc.Preview(t.Context(), a.ator(), input(aiimport.Payload{CategoryKeywords: []aiimport.CategoryKeywordEntry{
		entrada(c.mercado.ID, "Alimentação > Mercado", hostis...),
	}}))
	require.NoError(t, err)
	item := r.Items[0]
	require.Len(t, item.Rejected, len(hostis), "todas recusadas: %+v", item.Rejected)
	for i, rej := range item.Rejected {
		assert.Equal(t, aiimport.RejectInvalidKeyword, rej.Reason, "hostil %d", i)
		assert.LessOrEqual(t, len([]rune(rej.Keyword)), 40, "hostil %d: %q", i, rej.Keyword)
		for _, invisivel := range []string{rlo, "\x00", "\x1b", "\u200B", "\u200D", "\uFEFF"} {
			assert.NotContains(t, rej.Keyword, invisivel, "hostil %d devolveu rune invisível", i)
		}
	}
	assert.Equal(t, "abc def", item.Rejected[0].Keyword, "o override vira espaço")
	assert.Equal(t, "a|b|c", item.Rejected[1].Keyword, "o pipe desenha e volta como veio — na resposta JSON ele é inofensivo")
	assert.Equal(t, strings.Repeat("é", 40), item.Rejected[2].Keyword)
	assert.Equal(t, "feira [31mvermelha", item.Rejected[3].Keyword, "NUL e ESC colapsam num espaço")
	assert.Equal(t, "drog aria !", item.Rejected[4].Keyword)

	logs := a.logs.String()
	assert.NotContains(t, logs, "vermelha")
	assert.NotContains(t, logs, "drog")
}

// --- periodTransactions bate com COUNT direto no banco ---------------------------------------

func TestQAPeriodTransactionsBateComCountDiretoNoBanco(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	for _, d := range []struct {
		desc, mes, kind string
		n               int
	}{
		{"Pagamento de boleto", "2026-07", transaction.KindExpense, 3},
		{"Salario", "2026-08", transaction.KindIncome, 2},
		{"Aluguel", "2026-09", transaction.KindExpense, 4},
		{"Fora da janela", "2026-06", transaction.KindExpense, 5},
		{"Depois da janela", "2026-10", transaction.KindIncome, 5},
	} {
		for range d.n {
			a.lancar(t, a.casa.ID, d.kind, c.corrente, d.desc, d.mes)
		}
	}
	// Outra conta da mesma casa, na janela: conta.
	for range 3 {
		a.lancar(t, a.casa.ID, transaction.KindExpense, c.cartao, "Compra no cartao", "2026-08")
	}
	contaAlheia := a.conta(t, a.alheia.ID, "Conta alheia")
	for range 7 {
		a.lancar(t, a.alheia.ID, transaction.KindExpense, contaAlheia, "Da outra casa", "2026-08")
	}

	var direto int64
	require.NoError(t, a.db.Gorm().WithContext(t.Context()).Raw(
		`SELECT COUNT(*) FROM transactions
		  WHERE household_id = ? AND deleted_at IS NULL
		    AND competence_month IN (?, ?, ?)
		    AND kind IN (?, ?)`,
		a.casa.ID, "2026-07", "2026-08", "2026-09",
		transaction.KindIncome, transaction.KindExpense,
	).Scan(&direto).Error)
	require.EqualValues(t, 12, direto, "3 + 2 + 4 + 3 do cartão (tudo income/expense na janela)")

	in := input(aiimport.Payload{AccountKeywords: []aiimport.AccountKeywordEntry{deConta(c.cartao, "Cartão", "boleto")}})
	previa, err := a.svc.Preview(t.Context(), a.ator(), in)
	require.NoError(t, err)
	assert.EqualValues(t, direto, previa.Totals.PeriodTransactions)
	confirm, err := a.svc.Confirm(t.Context(), a.ator(), in)
	require.NoError(t, err)
	assert.EqualValues(t, direto, confirm.Totals.PeriodTransactions)
	assert.Equal(t, 3, previa.Items[0].Impact.TransferCandidates, "'boleto' alcança os 3 'Pagamento de boleto'")
}

// --- BOLA com o caminho CERTO da outra casa -----------------------------------------------

// O atacante conhece o nome: manda o id alheio COM o categoryPath/accountName
// corretos. A resposta tem de ser item_not_found — nunca name_mismatch, que
// confirmaria a existência do id — e byte a byte igual à de um id inventado.
func TestQABolaComCaminhoCorretoDaOutraCasaEhItemNotFoundNaoNameMismatch(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum(t)
	lazer := a.grupo(t, a.alheia.ID, "Lazer", category.KindExpense)
	cinema := a.folha(t, a.alheia.ID, lazer.ID, "Cinema", "cinemark")
	contaAlheia := a.conta(t, a.alheia.ID, "Itaú da vizinha", "itau")
	antesAlheia := a.retrato(t, a.alheia.ID)

	for _, rota := range []string{"preview", "confirm"} {
		in := input(aiimport.Payload{
			CategoryKeywords: []aiimport.CategoryKeywordEntry{
				entrada(cinema.ID, "Lazer > Cinema", "ingresso"), // caminho CERTO da outra casa
				entrada(lazer.ID, "Lazer", "ingresso"),
				entrada("018f0000-0000-7000-8000-0000000c0ffe", "Lazer > Cinema", "ingresso"),
			},
			AccountKeywords: []aiimport.AccountKeywordEntry{
				deConta(contaAlheia, "Itaú da vizinha", "itau unibanco"), // nome CERTO
				deConta("018f0000-0000-7000-8000-0000000c0ffe", "Itaú da vizinha", "itau unibanco"),
			},
		})
		var r aiimport.Report
		var err error
		if rota == "preview" {
			r, err = a.svc.Preview(t.Context(), a.ator(), in)
		} else {
			r, err = a.svc.Confirm(t.Context(), a.ator(), in)
		}
		require.NoError(t, err, rota)
		for i, item := range r.Items {
			assert.Nil(t, item.Name, "%s: item %d vazou o nome", rota, i)
			require.Len(t, item.Rejected, 1)
			assert.Equal(t, aiimport.RejectItemNotFound, item.Rejected[0].Reason, "%s: item %d", rota, i)
			assert.Empty(t, item.Rejected[0].OwnerID)
		}
		// Alheio-com-caminho-certo e inventado são a MESMA linha.
		alheio, inventado := r.Items[0], r.Items[2]
		alheio.ID, inventado.ID = "", ""
		assert.Equal(t, alheio, inventado)
		alheia, inventada := r.Items[3], r.Items[4]
		alheia.ID, inventada.ID = "", ""
		assert.Equal(t, alheia, inventada)
		if rota == "preview" {
			for _, item := range r.Items[3:] {
				require.NotNil(t, item.Impact)
				assert.Equal(t, 0, item.Impact.TransferCandidates, "nada medido para conta que não é desta casa")
			}
		}
	}
	assert.Equal(t, antesAlheia, a.retrato(t, a.alheia.ID))
	assert.Equal(t, []string{"cinemark"}, a.palavrasDe(t, a.alheia.ID, cinema.ID))
	assert.Equal(t, []string{"itau"}, a.palavrasDeConta(t, a.alheia.ID, contaAlheia))
}

// --- 400 em QUALQUER nível: campo desconhecido e caixa trocada -------------------------------

func TestQAHandlerCampoDesconhecidoOuCaixaTrocadaEmQualquerNivelEh400(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t)
	antes := a.retrato(t, a.casa.ID)

	casos := []struct{ nome, payload string }{
		{"caixa trocada em categoryKeywords[].categoryId",
			fmt.Sprintf(`{"homefinanceKeywordImport": 1, "categoryKeywords": [{"CategoryId": %q, "categoryPath": "Alimentação > Mercado", "add": ["feira"]}]}`, c.mercado.ID)},
		{"caixa trocada em accountKeywords[].add",
			fmt.Sprintf(`{"homefinanceKeywordImport": 1, "accountKeywords": [{"accountId": %q, "accountName": "Cartão", "ADD": ["pagamento"]}]}`, c.cartao)},
		{"caixa trocada em newCategories[].kind",
			`{"homefinanceKeywordImport": 1, "newCategories": [{"group": "Saúde", "name": "Farmácia", "Kind": "expense", "add": ["drogaria"]}]}`},
		{"campo desconhecido em newCategories[]",
			`{"homefinanceKeywordImport": 1, "newCategories": [{"group": "Saúde", "name": "Farmácia", "kind": "expense", "add": ["drogaria"], "archive": true}]}`},
		{"campo desconhecido em accountKeywords[]",
			fmt.Sprintf(`{"homefinanceKeywordImport": 1, "accountKeywords": [{"accountId": %q, "accountName": "Cartão", "add": ["pagamento"], "openingBalanceCents": 100}]}`, c.cartao)},
		{"campo capaz de mover categoria",
			`{"homefinanceKeywordImport": 1, "newCategories": [{"group": "Saúde", "name": "Farmácia", "kind": "expense", "add": ["drogaria"], "parentId": "x"}]}`},
		{"campo capaz de excluir",
			fmt.Sprintf(`{"homefinanceKeywordImport": 1, "categoryKeywords": [{"categoryId": %q, "categoryPath": "Alimentação > Mercado", "add": ["feira"], "delete": true}]}`, c.mercado.ID)},
		{"add com item que não é texto",
			fmt.Sprintf(`{"homefinanceKeywordImport": 1, "categoryKeywords": [{"categoryId": %q, "categoryPath": "Alimentação > Mercado", "add": [1, null]}]}`, c.mercado.ID)},
		{"versão booleana",
			fmt.Sprintf(`{"homefinanceKeywordImport": true, "categoryKeywords": [{"categoryId": %q, "categoryPath": "Alimentação > Mercado", "add": ["feira"]}]}`, c.mercado.ID)},
		{"versão fracionária",
			fmt.Sprintf(`{"homefinanceKeywordImport": 1.5, "categoryKeywords": [{"categoryId": %q, "categoryPath": "Alimentação > Mercado", "add": ["feira"]}]}`, c.mercado.ID)},
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			for _, rota := range []string{rotaPrevia, rotaConfirm} {
				rec := a.chamar(t, rota, a.casa.ID, envelope(tc.payload))
				require.Equal(t, 400, rec.Code, "%s: %s", rota, rec.Body.String())
				assert.NotContains(t, rec.Body.String(), "feira")
				assert.NotContains(t, rec.Body.String(), "drogaria")
				assert.NotContains(t, rec.Body.String(), c.mercado.ID)
			}
		})
	}
	assert.Equal(t, antes, a.retrato(t, a.casa.ID), "400 não escreve")
}

// --- skipNewCategories hostil é inofensivo -------------------------------------------------

func TestQASkipNewCategoriesHostilEhIgnoradoSemErro(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum(t)
	exp := category.KindExpense
	pl := aiimport.Payload{NewCategories: []aiimport.NewCategoryEntry{nova("Saúde", "Farmácia", &exp, "drogaria")}}

	// Refs que NÃO casam com "saude > farmacia": lixo, injeção, bidi, vazio,
	// sem os espaços do separador. Nenhum erro, nenhum eco no log, e a
	// categoria nasce.
	previa, err := a.svc.Preview(t.Context(), a.ator(), input(pl,
		strings.Repeat("x", 10_000), "<script>alert(1)</script>", "' OR 1=1 --", "\u202Esaude > farmacia\u202C", "", "saude>farmacia"))
	require.NoError(t, err)
	assert.Equal(t, aiimport.OutcomeCreated, previa.NewCategories[0].Outcome)

	// O ref é comparado NORMALIZADO: caixa, acento e espaços colapsados
	// casam — é o que torna o ref estável entre a prévia e o confirm.
	previa, err = a.svc.Preview(t.Context(), a.ator(), input(pl, "SAÚDE  >   Farmácia"))
	require.NoError(t, err)
	assert.Equal(t, aiimport.OutcomeSkippedByUser, previa.NewCategories[0].Outcome)

	r, err := a.svc.Confirm(t.Context(), a.ator(), input(pl,
		strings.Repeat("x", 10_000), "<script>alert(1)</script>", "' OR 1=1 --", "\u202Esaude > farmacia\u202C", "", "saude>farmacia"))
	require.NoError(t, err)
	assert.Equal(t, aiimport.OutcomeCreated, r.NewCategories[0].Outcome)
	require.NotNil(t, a.categoriaPorCaminho(t, a.casa.ID, "saude > farmacia"))
	assert.NotContains(t, a.logs.String(), "<script>")
	assert.NotContains(t, a.logs.String(), "OR 1=1")
}

// --- BUG B1 (encontrado pelo QA em 21/09/2026): 409 determinístico -----------------------
//
// JSON que, no MESMO lote, cria uma folha sob um grupo sem filhas E acrescenta
// palavras a esse grupo. O plano confere o grupo contra o índice de ANTES
// (sem filha → aceita as palavras) e a aplicação cria a folha primeiro; na
// hora de SetKeywords do grupo, podeReceberPalavras vê a filha recém-nascida
// e devolve ErrKeywordsOnGroupWithChildren, que erroDeGravacao traduz em
// ErrConflict. O 409 diz "o estado mudou" — e não mudou: chamar de novo dá o
// mesmo 409, para sempre. A tela entra em "Conferir de novo" → mesma prévia
// verde → mesmo 409. Nada é gravado (a transação desfaz), mas o JSON fica
// inaplicável sem a pessoa adivinhar qual linha tirar — e é a prévia
// divergindo do confirm, que é exatamente o que o desenho de `executar`
// promete que não acontece.
//
// O teste afirma o MÍNIMO que qualquer correção precisa dar: o confirm de um
// payload determinístico não é 409, e prévia e confirm concordam. A correção
// recomendada é o plano recusar as palavras do grupo com `group_has_children`
// quando o próprio lote cria uma folha ativa sob ele (§4.2 5b vale para o
// estado DEPOIS do lote) — aí a prévia já mostra a recusa e o resto entra.
func TestQARegressaoB1FolhaNovaMaisPalavraNoMesmoGrupoNaoEh409(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	c := a.casaComum(t) // Transporte: grupo SEM filhas, com [uber]
	in := input(aiimport.Payload{
		NewCategories:    []aiimport.NewCategoryEntry{nova("Transporte", "Ônibus", nil, "brt")},
		CategoryKeywords: []aiimport.CategoryKeywordEntry{entrada(c.transporte.ID, "Transporte", "99 taxi")},
	})

	previa, err := a.svc.Preview(t.Context(), a.ator(), in)
	require.NoError(t, err)

	r, err := a.svc.Confirm(t.Context(), a.ator(), in)
	require.False(t, errors.Is(err, aiimport.ErrConflict),
		"409 para um payload determinístico: nada mudou entre a prévia e o confirm (err=%v)", err)
	require.NoError(t, err)

	// Prévia e confirm concordam, entrada a entrada.
	assert.Equal(t, previa.NewCategories[0].Outcome, r.NewCategories[0].Outcome)
	assert.Equal(t, previa.Items[0].Added, r.Items[0].Added)
	assert.Equal(t, motivos(previa.Items[0].Rejected), motivos(r.Items[0].Rejected))

	// A folha nasceu, com a palavra dela.
	onibus := a.categoriaPorCaminho(t, a.casa.ID, "transporte > onibus")
	require.NotNil(t, onibus, "a folha do lote tem de nascer")
	assert.Equal(t, []string{"brt"}, a.palavrasDe(t, a.casa.ID, onibus.ID))

	// E o grupo com filha ativa NÃO ganhou palavra nova pelo import (spec 0005
	// §12; spec 0010 §4.2 5b): a palavra é recusada por group_has_children.
	assert.Equal(t, []string{"uber"}, a.palavrasDe(t, a.casa.ID, c.transporte.ID))
	assert.Equal(t, map[string]string{"99 taxi": aiimport.RejectGroupHasChildren}, motivos(r.Items[0].Rejected))
}
