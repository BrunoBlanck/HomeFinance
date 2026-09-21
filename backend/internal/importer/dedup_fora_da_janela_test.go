package importer_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regressão do furo da CHAVE NATURAL FORA DA JANELA DE DATAS.
//
// O mecanismo, em uma frase: a chave natural não embute a data, mas os
// existentes vinham de uma consulta filtrada por `occurred_on`. Quando a data
// da mesma transação mudava mais do que transaction.DedupWindowDays entre dois
// downloads, a gêmea já gravada ficava fora da janela, a linha voltava como
// `novo` — que entra por DEFAULT, sem marcação nenhuma — e o ordinal calculado
// virava 2. O índice único não recusava nada: (casa, chave, 2) estava livre.
//
// Os dois caminhos mais prováveis, e nenhum deles é exótico:
//
//  1. a pessoa corrige a data de uma linha no CSV e reimporta — que é
//     exatamente o "erro do usuário" que esta entrega existe para absorver;
//  2. o emissor re-data uma transação entre dois exports (pendente que
//     liquidou), mantendo o mesmo Identificador.
//
// A correção tem duas camadas, e os testes deste arquivo cobrem as duas: a
// classificação procura as ocorrências PELA CHAVE (sem data), e o serviço de
// lançamentos recusa ordinal > 1 em chave natural.

// csvExtratoComData monta um extrato Nubank de UMA linha com a data literal —
// os montadores existentes fixam o mês, e o defeito é justamente sobre a data
// mudar de mês.
func csvExtratoComData(data, valor, idSufixo, descricao string) []byte {
	var b bytes.Buffer
	b.WriteString("Data,Valor,Identificador,Descrição\n")
	fmt.Fprintf(&b, "%s,%s,11111111-1111-4111-8111-1111111111%s,%s\n", data, valor, idSufixo, descricao)
	return b.Bytes()
}

// chavesVivasNaConta conta, por chave de deduplicação, quantas linhas VIVAS
// existem na conta — a invariante que o índice único deveria garantir sozinho e
// que o ordinal 2 contornava.
func chavesVivasNaConta(t *testing.T, a *ambiente, contaID string) map[string]int {
	t.Helper()
	janela, err := a.repoTx.WindowForDedup(t.Context(), a.casa.ID, contaID,
		civil.MustNew(2026, 1, 1), civil.MustNew(2026, 12, 31))
	require.NoError(t, err)

	out := map[string]int{}
	for _, linha := range janela {
		if linha.DeletedAt == nil {
			out[linha.DedupKey]++
		}
	}
	return out
}

// TestForaDaJanelaMesmoIdentificadorVoltaComoDuplicadoExato é a PoC da revisão,
// literal: o MESMO Identificador em 04/08/2026 e em 01/06/2026 — dois meses de
// distância, contra uma folga de janela de 3 dias.
func TestForaDaJanelaMesmoIdentificadorVoltaComoDuplicadoExato(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	const identificador = "51"

	// Lote 1: a transação como o banco a exportou da primeira vez.
	lote1 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "agosto.csv",
		csvExtratoComData("04/08/2026", "-20.00", identificador, "Mercado Exemplo")))
	res1 := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote1.ID, `{"decisions":[]}`))
	require.Equal(t, 1, res1.Imported)

	// Lote 2: a MESMA transação, com a data corrigida para junho.
	lote2 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "junho.csv",
		csvExtratoComData("01/06/2026", "-20.00", identificador, "Mercado Exemplo")))
	require.NotNil(t, lote2.Counts)
	assert.Equal(t, 1, lote2.Counts.DuplicateExact,
		"a chave natural é a mesma: a distância entre as datas não pode apagar a identidade")
	assert.Zero(t, lote2.Counts.New,
		"`novo` é o defeito: ele entra por DEFAULT, sem o usuário ver nada")

	// A revisão tem de mostrar a linha barrada E não liberável — liberar não
	// adiantaria nada, porque a identidade já existe.
	revisao := revisarPelaAPI(t, a, lote2.ID)
	require.Len(t, revisao.Items, 1)
	assert.Equal(t, string(dedup.StatusDuplicateExact), revisao.Items[0].Status)
	assert.Equal(t, importer.ActionSkip, revisao.Items[0].DefaultAction)
	assert.Empty(t, revisao.Items[0].AllowedActions)
	assert.NotNil(t, revisao.Items[0].MatchTransactionID,
		"a tela precisa apontar para o lançamento que já existe")

	res2 := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote2.ID, `{"decisions":[]}`))
	assert.Zero(t, res2.Imported, "a mesma transação NÃO pode entrar duas vezes")
	assert.Equal(t, 1, res2.Skipped)

	// Prova independente da classificação: nenhuma chave em duas linhas vivas.
	for chave, n := range chavesVivasNaConta(t, a, conta.ID) {
		assert.Equal(t, 1, n, "a chave %s… aparece em %d linhas vivas", chave[:8], n)
	}
	totalVivas := len(a.lancamentosDa(t, a.casa.ID, "2026-08")) + len(a.lancamentosDa(t, a.casa.ID, "2026-06"))
	assert.Equal(t, 1, totalVivas, "UMA transação no total, nos dois meses somados")
}

// TestForaDaJanelaGemeaExcluidaVoltaComoDuplicadoExcluido cobre a outra metade
// do mesmo furo: a gêmea fora da janela está EXCLUÍDA logicamente.
//
// Sem a busca por chave, ela também não era encontrada — e a linha entrava como
// `novo`, criando uma segunda cópia ao lado da que a pessoa tinha apagado. O
// certo é `duplicado_excluido`, que é liberável e cuja liberação RESTAURA
// (ADR-025f), preservando a data ORIGINAL do lançamento.
func TestForaDaJanelaGemeaExcluidaVoltaComoDuplicadoExcluido(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	const identificador = "52"

	lote1 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "agosto.csv",
		csvExtratoComData("04/08/2026", "-77.30", identificador, "Farmacia Exemplo")))
	require.Equal(t, 1, resultadoDaResposta(t, confirmarPelaAPI(t, a, lote1.ID, `{"decisions":[]}`)).Imported)

	gravada := a.lancamentosDa(t, a.casa.ID, "2026-08")[0]
	require.NoError(t, a.txSvc.SoftDelete(t.Context(), transaction.Actor{
		HouseholdID: a.casa.ID, UserID: a.usuario.ID,
	}, gravada.ID))

	lote2 := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "junho.csv",
		csvExtratoComData("01/06/2026", "-77.30", identificador, "Farmacia Exemplo")))
	require.NotNil(t, lote2.Counts)
	assert.Equal(t, 1, lote2.Counts.DuplicateDeleted,
		"a gêmea excluída continua ocupando a chave, esteja a data onde estiver")
	assert.Zero(t, lote2.Counts.New)

	revisao := revisarPelaAPI(t, a, lote2.ID)
	require.Len(t, revisao.Items, 1)
	linha := revisao.Items[0]
	require.Equal(t, string(dedup.StatusDuplicateDeleted), linha.Status)
	require.Contains(t, linha.AllowedActions, importer.ActionImport,
		"duplicado_excluido é liberável — é a única saída do beco sem saída do índice único")

	res := resultadoDaResposta(t, confirmarPelaAPI(t, a, lote2.ID,
		fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"import"}]}`, linha.ID)))
	assert.Equal(t, 1, res.Restored, "liberar RESTAURA a existente")
	assert.Zero(t, res.Imported, "e não insere outra")

	// A restauração devolve o lançamento ORIGINAL: a data do arquivo que pediu
	// a restauração não pode passar por cima do dado que já estava lá.
	vivas := a.lancamentosDa(t, a.casa.ID, "2026-08")
	require.Len(t, vivas, 1)
	assert.Equal(t, "2026-08-04", vivas[0].OccurredOn.String())
	assert.Equal(t, gravada.ID, vivas[0].ID)
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-06"))
}

// TestChaveNaturalNuncaRecebeOrdinalDois é a SEGUNDA camada, exercitada sem
// passar pela importação: mesmo que a classificação erre (ou que alguém chame o
// serviço de lançamentos direto), gravar a mesma chave natural de novo é
// recusado em vez de ganhar ordinal 2.
//
// É o teste que impede a correção de ser só a da classificação: a camada de
// escrita tem de dizer não sozinha.
func TestChaveNaturalNuncaRecebeOrdinalDois(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	const identificador = "53"

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "agosto.csv",
		csvExtratoComData("04/08/2026", "-42.00", identificador, "Padaria Exemplo")))
	require.Equal(t, 1, resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`)).Imported)

	// A mesma chave natural, agora direto no serviço de lançamentos — é o que
	// um confirm com a análise cega tentaria fazer.
	externo := "11111111-1111-4111-8111-1111111111" + identificador
	loteForjado := "00000000-0000-7000-d000-000000000001"
	_, err := a.txSvc.CreateBatch(t.Context(), transaction.Actor{
		HouseholdID: a.casa.ID, UserID: a.usuario.ID, IP: "203.0.113.7",
	}, transaction.CreateBatchInput{
		Source:        transaction.SourceImport,
		ImportBatchID: &loteForjado,
		Rows: []transaction.NewTransaction{{
			Kind:        transaction.KindExpense,
			AccountID:   conta.ID,
			AmountCents: 4200,
			Description: "Padaria Exemplo",
			OccurredOn:  civil.MustNew(2026, 6, 1),
			ExternalID:  &externo,
			DedupKey:    dedup.NaturalKey("nubank", conta.ID, externo),
		}},
	})

	require.Error(t, err)
	assert.True(t, transaction.IsBlocked(err),
		"a recusa tem de ser ErrDuplicateDedup — é o erro que a importação traduz em "+
			"'esta linha não entrou', nunca em 500")
	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 1)
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-06"), "nada foi gravado")
}

// TestChaveDerivadaContinuaPodendoRepetir é o CONTROLE da defesa acima, e existe
// para que ela não vire uma trava que engole gasto real.
//
// A recusa de ordinal > 1 vale SÓ para chave natural. Na derivada — a fatura,
// que não numera as linhas — dois cafés de R$ 11,00 no mesmo dia na mesma
// padaria são dois gastos reais, e o ordinal é justamente o que os mantém
// separados. Num app de dinheiro, sumir com um lançamento é pior do que
// duplicar um.
func TestChaveDerivadaContinuaPodendoRepetir(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	linha := func() transaction.NewTransaction {
		return transaction.NewTransaction{
			Kind:        transaction.KindExpense,
			AccountID:   conta.ID,
			AmountCents: 1100,
			Description: "Cafe Exemplo",
			OccurredOn:  civil.MustNew(2026, 8, 4),
			// Sem ExternalID: é o que faz a chave ser DERIVADA.
			DedupKey: dedup.DerivedKey(conta.ID, transaction.KindExpense,
				civil.MustNew(2026, 8, 4), 1100, "cafe exemplo"),
		}
	}

	loteForjado := "00000000-0000-7000-d000-000000000002"
	res, err := a.txSvc.CreateBatch(t.Context(), transaction.Actor{
		HouseholdID: a.casa.ID, UserID: a.usuario.ID, IP: "203.0.113.7",
	}, transaction.CreateBatchInput{
		Source:        transaction.SourceImport,
		ImportBatchID: &loteForjado,
		Rows:          []transaction.NewTransaction{linha(), linha()},
	})
	require.NoError(t, err, "duas compras idênticas de verdade entram as DUAS")
	require.Len(t, res.IDs, 2)

	vivas := a.lancamentosDa(t, a.casa.ID, "2026-08")
	assert.Len(t, vivas, 2)

	ordinais := []int{}
	for _, v := range vivas {
		ordinais = append(ordinais, v.DedupOrdinal)
	}
	assert.ElementsMatch(t, []int{1, 2}, ordinais,
		"é o ordinal que separa as duas — e ele continua vivo na chave derivada")
}
