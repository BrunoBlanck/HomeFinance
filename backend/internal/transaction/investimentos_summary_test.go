package transaction_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Spec 0006 §3.5.2 e ADR-029(d)(e): aporte e resgate saem de
// incomeCents/expenseCents/netCents e aparecem em investedCents/redeemedCents.
//
// O que estes testes travam, além dos números: que o conjunto de categorias
// marcadas sai da MESMA leitura que rotula a página (critério 17, conferido
// por CONTAGEM de chamadas), e que uma parcela impossível falha FECHADA em vez
// de ir para a tela (ADR-029 j.1).

// cenarioInvestimento monta setembro com receita, despesa, aporte e resgate.
func cenarioInvestimento(t *testing.T) *ambiente {
	t.Helper()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	mercado := amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	salario := amb.categoria(minhaCasa, "cat-salario", "Salário", category.KindIncome)
	cdb := amb.categoria(minhaCasa, "cat-cdb", "CDB", category.KindInvestment)
	resgate := amb.categoria(minhaCasa, "cat-resgate", "Resgates", category.KindRedemption)

	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindIncome,
		AmountCents: 5_000_00, Description: "Salário", CategoryID: ptr(salario.ID),
		OccurredOn: civil.MustNew(2026, 9, 5), CompetenceMonth: "2026-09",
	})
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 300_00, Description: "Mercado", CategoryID: ptr(mercado.ID),
		OccurredOn: civil.MustNew(2026, 9, 6), CompetenceMonth: "2026-09",
	})
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 2_000_00, Description: "CDB 15 DIAS", CategoryID: ptr(cdb.ID),
		OccurredOn: civil.MustNew(2026, 9, 10), CompetenceMonth: "2026-09",
	})
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindIncome,
		AmountCents: 500_00, Description: "RESGATE CDB", CategoryID: ptr(resgate.ID),
		OccurredOn: civil.MustNew(2026, 9, 12), CompetenceMonth: "2026-09",
	})
	return amb
}

// O coração da tarefa: os dois marcados saem das agregações de receita e
// despesa e aparecem nos campos próprios, sem sumir da LISTA nem da contagem.
func TestSummaryTiraAporteEResgateDeReceitaEDespesa(t *testing.T) {
	t.Parallel()

	amb := cenarioInvestimento(t)

	v, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Equal(t, int64(5_000_00), v.Summary.IncomeCents, "o resgate saiu da receita")
	assert.Equal(t, int64(300_00), v.Summary.ExpenseCents, "o aporte saiu da despesa")
	assert.Equal(t, int64(5_000_00-300_00), v.Summary.NetCents, "o líquido é o das duas parcelas exibidas")
	assert.Equal(t, int64(2_000_00), v.Summary.InvestedCents)
	assert.Equal(t, int64(500_00), v.Summary.RedeemedCents)

	// A mesma linha é contada UMA vez, e continua na lista: ela existe e saiu
	// da conta (ADR-029e, spec 0006 §3.5.3).
	assert.Equal(t, int64(4), v.Summary.Count)
	assert.Equal(t, int64(0), v.Summary.UncategorizedCount, "aporte tem categoria por definição, nunca é pendência")
	require.Len(t, v.Items, 4)
	descricoes := make([]string, 0, len(v.Items))
	for _, it := range v.Items {
		descricoes = append(descricoes, it.Description)
	}
	assert.Contains(t, descricoes, "CDB 15 DIAS", "o aporte continua visível em /lancamentos")
	assert.Contains(t, descricoes, "RESGATE CDB")
}

// Casa sem nenhuma categoria de investimento — o caso de TODA casa no dia da
// entrega: os dois campos vêm presentes e zerados, e o filtro chega VAZIO ao
// repositório (é o que faz o SQL voltar a ser o de antes da E7 — ADR-029f).
func TestSummarySemCategoriaDeInvestimentoDevolveZeros(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	cat := amb.categoria(minhaCasa, "cat-1", "Mercado", category.KindExpense)
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 300_00, Description: "Mercado", CategoryID: ptr(cat.ID),
		OccurredOn: civil.MustNew(2026, 9, 6), CompetenceMonth: "2026-09",
	})

	v, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.Equal(t, int64(300_00), v.Summary.ExpenseCents)
	assert.Equal(t, int64(0), v.Summary.InvestedCents)
	assert.Equal(t, int64(0), v.Summary.RedeemedCents)

	require.Len(t, amb.repo.resumosPedidos, 1)
	assert.Empty(t, amb.repo.resumosPedidos[0], "sem categoria marcada o filtro chega vazio, e o IN (...) não é montado")
}

// O conjunto que vai ao repositório é o das categorias das DUAS naturezas
// marcadas, com as ARQUIVADAS incluídas (PLANOS.md §4.4) — e nenhuma de outra
// casa.
func TestConjuntoDeCategoriasMarcadasIncluiArquivadasEExcluiOutraCasa(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	amb.categoria(minhaCasa, "cat-cdb", "CDB", category.KindInvestment)
	amb.categoria(minhaCasa, "cat-resgate", "Resgates", category.KindRedemption)
	amb.categoria(outraCasa, "cat-alheia", "Tesouro", category.KindInvestment)

	arquivada := amb.categoria(minhaCasa, "cat-antiga", "Poupança", category.KindInvestment)
	quando := agora
	arquivada.ArchivedAt = &quando
	amb.categorias.add(arquivada)

	_, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)

	require.Len(t, amb.repo.resumosPedidos, 1)
	assert.ElementsMatch(t,
		[]string{"cat-cdb", "cat-resgate", "cat-antiga"},
		amb.repo.resumosPedidos[0],
		"arquivada continua marcando o passado; categoria de outra casa nunca entra",
	)

	// E a casa é reconferida em GO, não só pela fonte: com uma implementação
	// de Categories que esquecesse o filtro, o id alheio ainda assim não entra
	// no conjunto. É ESTA asserção que prova a defesa do serviço — a de cima
	// prova o dublê, e um dublê que filtra não é uma defesa.
	amb.categorias.ignorarCasa = true
	amb.repo.resumosPedidos = nil

	_, err = amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)
	require.Len(t, amb.repo.resumosPedidos, 1)
	assert.NotContains(t, amb.repo.resumosPedidos[0], "cat-alheia",
		"categoria de outra casa não entra no conjunto nem com a fonte vazando")
}

// Critério 17 do plano, conferido por CONTAGEM de chamadas e não por leitura
// do código: GET /transactions faz UMA leitura de categorias por requisição.
func TestListLeAsCategoriasUmaVezPorRequisicao(t *testing.T) {
	t.Parallel()

	amb := cenarioInvestimento(t)
	amb.categorias.listadas = 0

	_, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.Equal(t, 1, amb.categorias.listadas, "duas leituras pagam a consulta duas vezes e podem discordar entre si")
}

// E vale também quando a PÁGINA sai vazia: a lista acabou, mas o resumo
// continua falando da janela inteira — e sem o conjunto ele somaria aporte
// dentro de despesa.
func TestListLeAsCategoriasMesmoComPaginaVazia(t *testing.T) {
	t.Parallel()

	amb := cenarioInvestimento(t)
	amb.categorias.listadas = 0

	v, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-10"})
	require.NoError(t, err)
	require.Empty(t, v.Items)
	assert.Equal(t, 1, amb.categorias.listadas)
	require.Len(t, amb.repo.resumosPedidos, 1)
	assert.Len(t, amb.repo.resumosPedidos[0], 2, "o filtro vai completo mesmo sem linha na página")
}

// ADR-029(j.1): "0 <= marcado <= total" é VERIFICADO antes de publicar.
// Parcela negativa — que só um banco em estado que a aplicação não produz
// gera — falha FECHADA. Clampar em silêncio esconderia a corrupção e ainda
// publicaria um total errado.
func TestResumoComParcelaNegativaFalhaFechado(t *testing.T) {
	t.Parallel()

	// Os valores são DISTINTIVOS de propósito: 314159 e 271828 não aparecem em
	// mais nada da mensagem, então asserir a AUSÊNCIA deles prova que nenhum
	// centavo vazou. Asserir a ausência da palavra "cents" não provaria nada —
	// bastaria o formato mudar de nome para o teste continuar passando com o
	// valor dentro.
	const (
		centavosA int64 = 314159
		centavosB int64 = 271828
	)
	casos := map[string]transaction.Summary{
		"despesa negativa (marcado maior que o total)": {ExpenseCents: -1, InvestedCents: centavosA, Count: 3},
		"receita negativa (marcado maior que o total)": {IncomeCents: -1, RedeemedCents: centavosA, Count: 3},
		"aporte negativo":   {ExpenseCents: centavosB, InvestedCents: -1, Count: 3},
		"resgate negativo":  {IncomeCents: centavosB, RedeemedCents: -1, Count: 3},
		"contagem negativa": {IncomeCents: centavosB, Count: -1},
	}
	for nome, resumo := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			amb := novoAmbiente(t)
			forcado := resumo
			amb.repo.resumoForcado = &forcado

			_, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
			require.Error(t, err, "número impossível não pode chegar à tela")
			// Não é erro de validação: o pedido estava correto, o DADO é que
			// não está. Ir para 4xx apontaria um campo que a pessoa não tem
			// como corrigir.
			assert.False(t, transaction.IsValidationError(err))
			// E a mensagem leva CONTAGENS, nunca centavos (S8): nenhum dos
			// dois valores plantados pode aparecer nela.
			assert.NotContains(t, err.Error(), "314159")
			assert.NotContains(t, err.Error(), "271828")
			assert.Contains(t, err.Error(), "categorias_marcadas=")
		})
	}
}

// Líquido NEGATIVO é legítimo — é o mês que fechou no vermelho — e não pode
// ser confundido com parcela impossível.
func TestResumoComLiquidoNegativoContinuaValido(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.repo.resumoForcado = &transaction.Summary{
		IncomeCents: 100_00, ExpenseCents: 900_00, NetCents: -800_00,
		InvestedCents: 2_000_00, Count: 4,
	}

	v, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.Equal(t, int64(-800_00), v.Summary.NetCents)
	assert.Equal(t, int64(2_000_00), v.Summary.InvestedCents)
}

// ADR-029(j.2): teto do conjunto de categorias falha FECHADO na borda, ANTES
// de qualquer SQL — o repositório nem é chamado.
func TestTaxonomiaEstouradaFalhaAntesDeConsultarOResumo(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	for i := 0; i <= category.MaxPerHousehold; i++ {
		amb.categoria(minhaCasa, fmt.Sprintf("cat-%03d", i), "Aporte", category.KindInvestment)
	}

	_, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.ErrorIs(t, err, transaction.ErrTooManyCategories)
	// Nem o resumo nem a página são consultados. A leitura da taxonomia, essa
	// já aconteceu — é dela que sai a contagem que dispara a guarda —, e é por
	// isso que a asserção fala das DUAS consultas de lançamento em vez de
	// "nenhum SQL".
	assert.Empty(t, amb.repo.resumosPedidos, "o resumo não é pedido com a taxonomia estourada")
	assert.Zero(t, amb.repo.listagens, "e a página também não")
}

// A armadilha do nome, escrita como teste: ErrTooManyCategories NÃO é erro de
// validação. ErrTooManyUncategorized é 422 em "fields.month" porque conta
// LANÇAMENTOS, que a pessoa divide em dois meses; este conta CATEGORIAS DA
// PRÓPRIA CASA, que já têm teto próprio.
func TestErrosDeConjuntoDeCategoriasNaoSaoErroDeValidacao(t *testing.T) {
	t.Parallel()

	assert.False(t, transaction.IsValidationError(transaction.ErrTooManyCategories))
	assert.False(t, transaction.IsValidationError(transaction.ErrEmptyCategoryFilter))
	assert.True(t, transaction.IsValidationError(transaction.ErrTooManyUncategorized),
		"o vizinho de nome continua sendo 422 — é justamente por isso que o de cima precisa do teste")
}

// O predicado é um só e responde exatamente às duas naturezas novas.
func TestMarcadaComoInvestimentoRespondeSoAsDuasNaturezas(t *testing.T) {
	t.Parallel()

	assert.True(t, transaction.MarcadaComoInvestimento(category.KindInvestment))
	assert.True(t, transaction.MarcadaComoInvestimento(category.KindRedemption))
	assert.False(t, transaction.MarcadaComoInvestimento(category.KindExpense))
	assert.False(t, transaction.MarcadaComoInvestimento(category.KindIncome))
	assert.False(t, transaction.MarcadaComoInvestimento(""))
	assert.False(t, transaction.MarcadaComoInvestimento("INVESTMENT"), "a allowlist é exata")
}

// O contrato: os dois campos são SEMPRE serializados, mesmo zerados
// (TransactionSummary os declara required, e additionalProperties: false).
func TestSummaryViewSempreTrazOsDoisCamposNovos(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	v, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)

	bruto, err := json.Marshal(v.Summary)
	require.NoError(t, err)
	var campos map[string]any
	require.NoError(t, json.Unmarshal(bruto, &campos))

	for _, campo := range []string{
		"incomeCents", "expenseCents", "netCents", "count", "uncategorizedCount",
		"investedCents", "redeemedCents",
	} {
		assert.Contains(t, campos, campo)
	}
	assert.Len(t, campos, 7, "campo a mais aqui é divergência de contrato")
}
