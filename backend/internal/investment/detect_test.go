package investment_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// casaComTudo monta a casa que os testes de detect usam: as quatro naturezas,
// palavras-chave sem colisão, e a casa vizinha com as MESMAS palavras em
// categorias dela — o isolamento não pode passar por acaso.
func casaComTudo() *cenario {
	c := montar()
	c.categorias.juntar(casaA, "cat-mercado", "Mercado", category.KindExpense, "supermercado")
	c.categorias.juntar(casaA, "cat-salario", "Salário", category.KindIncome, "folha")
	c.categorias.juntar(casaA, "cat-cdb", "Renda fixa", category.KindInvestment, "cdb")
	c.categorias.juntar(casaA, "cat-resgate", "Resgates", category.KindRedemption, "resgate")
	c.contas.juntar(casaA, "acc-1", "Nubank")

	c.categorias.juntar(casaB, "cat-b-cdb", "Renda fixa B", category.KindInvestment, "cdb")
	c.contas.juntar(casaB, "acc-1", "Itaú")
	return c
}

func detectar(t *testing.T, c *cenario, casa string, in investment.DetectInput) investment.DetectView {
	t.Helper()
	view, err := c.svc.Detect(context.Background(), ator(casa), in)
	require.NoError(t, err)
	return view
}

// Aceite 8: dryRun não escreve NADA — conferido relendo os lançamentos, e
// também pela ausência de chamada de escrita, de transação e de auditoria.
func TestDryRunNaoEscreveNada(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 5, "2026-09", ""))
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindIncome, "RESGATE CDB", 85000, 6, "2026-09", ""))

	view := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})

	assert.EqualValues(t, 2, view.Marked)
	require.Len(t, view.Items, 2)

	assert.Zero(t, c.ledger.chamadasSetNull)
	assert.Zero(t, c.ledger.chamadasSetCurrent)
	assert.Zero(t, c.tx.chamadas, "prévia abriu transação")
	assert.Empty(t, c.auditoria.entradas, "prévia gerou auditoria")
	assert.Nil(t, c.ledger.por(uuidDe(1)).CategoryID)
	assert.Nil(t, c.ledger.por(uuidDe(2)).CategoryID)
}

// Aceite 8, segunda metade: a execução real marca exatamente os itens
// listados, e a SEGUNDA execução marca 0 (idempotência por construção — o
// `category_id IS NULL` está no WHERE).
func TestExecucaoRealMarcaOsItensDaPreviaEEhIdempotente(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 5, "2026-09", ""))
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindIncome, "RESGATE CDB", 85000, 6, "2026-09", ""))

	previa := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})
	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09"})

	assert.Equal(t, previa.Marked, real.Marked, "a prévia prometeu o que a escrita fez")
	assert.EqualValues(t, 2, real.Marked)
	assert.Equal(t, 1, c.tx.chamadas)

	// Listas VAZIAS na execução real, e não nulas.
	assert.NotNil(t, real.Items)
	assert.Empty(t, real.Items)
	assert.NotNil(t, real.UnmatchedItems)
	assert.NotNil(t, real.AlreadyCategorizedItems)

	require.NotNil(t, c.ledger.por(uuidDe(1)).CategoryID)
	assert.Equal(t, "cat-cdb", *c.ledger.por(uuidDe(1)).CategoryID)
	require.NotNil(t, c.ledger.por(uuidDe(2)).CategoryID)
	assert.Equal(t, "cat-resgate", *c.ledger.por(uuidDe(2)).CategoryID)
	assert.Equal(t, relogioFixo, c.ledger.por(uuidDe(1)).UpdatedAt)

	segunda := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09"})
	assert.Zero(t, segunda.Marked, "a segunda execução tem de marcar 0")
}

// Os TRÊS motivos existem, e o terceiro é o que a entrega acrescentou:
// `other_category` para a linha cuja vencedora é uma categoria COMUM. Sem ele,
// "Mercado do seu José" apareceria como "não bateu com nada", que é mentira.
func TestOsTresMotivosDeNaoMarcacao(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	// Vencedora comum: bate com "supermercado" (despesa), não é investimento.
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "SUPERMERCADO DO SEU JOSE", 9000, 1, "2026-09", ""))
	// Nada parecido com palavra nenhuma.
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindExpense, "ZZZZ QQQQ WWWW", 9000, 2, "2026-09", ""))

	// Empate entre duas categorias de investimento: ambíguo é pior que vazio.
	c.categorias.juntar(casaA, "cat-acoes", "Ações", category.KindInvestment, "acoes")
	c.categorias.juntar(casaA, "cat-renda", "Renda", category.KindInvestment, "renda")
	c.ledger.juntar(lanc(casaA, uuidDe(3), transaction.KindExpense, "Acoes Renda", 9000, 3, "2026-09", ""))

	view := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})

	motivos := map[string]string{}
	for _, it := range view.UnmatchedItems {
		motivos[it.ID] = it.Reason
	}
	assert.Equal(t, investment.ReasonOtherCategory, motivos[uuidDe(1)],
		"linha que bateu com categoria comum não pode aparecer como `não bateu com nada`")
	assert.Equal(t, investment.ReasonBelowThreshold, motivos[uuidDe(2)])
	assert.Equal(t, investment.ReasonAmbiguous, motivos[uuidDe(3)])

	assert.Zero(t, view.Marked)
	assert.EqualValues(t, 3, view.Unmatched)
}

// INVARIANTE do contrato: sem a flag, marked + unmatched cobre EXATAMENTE os
// lançamentos sem categoria do mês.
func TestMarcadosMaisNaoMarcadosCobremOsSemCategoria(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	semCategoria := 0
	for _, l := range []transaction.Transaction{
		lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""),
		lanc(casaA, uuidDe(2), transaction.KindExpense, "SUPERMERCADO", 9000, 2, "2026-09", ""),
		lanc(casaA, uuidDe(3), transaction.KindExpense, "ZZZZ QQQQ", 9000, 3, "2026-09", ""),
		lanc(casaA, uuidDe(4), transaction.KindIncome, "RESGATE CDB", 5000, 4, "2026-09", ""),
		// Com categoria: fora da conta dos dois primeiros números.
		lanc(casaA, uuidDe(5), transaction.KindExpense, "CDB ANTIGO", 1000, 5, "2026-09", "cat-mercado"),
		lanc(casaA, uuidDe(6), transaction.KindExpense, "CDB JA MARCADO", 1000, 6, "2026-09", "cat-cdb"),
	} {
		if l.CategoryID == nil {
			semCategoria++
		}
		c.ledger.juntar(l)
	}

	view := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})
	assert.EqualValues(t, semCategoria, view.Marked+view.Unmatched,
		"marked + unmatched tem de ser o total de linhas SEM categoria do mês")
}

// Aceite 9 e item 16 do plano, primeira metade: sem a flag, o lançamento que já
// tem categoria não é alterado; e o que já tem categoria DE INVESTIMENTO não
// aparece nem em `alreadyCategorizedItems` — oferecer a troca seria prometer o
// que o WHERE recusa.
func TestSemAFlagNadaJaCategorizadoEhAlteradoENemOfertado(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	// Categoria COMUM, bate com palavra de investimento: cabe na oferta.
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))
	// Já marcada como investimento: NÃO cabe na oferta.
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindExpense, "CDB 30 DIAS", 100000, 2, "2026-09", "cat-cdb"))
	// Categorizada e sem relação com investimento: também não é assunto.
	c.ledger.juntar(lanc(casaA, uuidDe(3), transaction.KindExpense, "SUPERMERCADO", 9000, 3, "2026-09", "cat-mercado"))

	previa := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})

	assert.EqualValues(t, 1, previa.AlreadyCategorized)
	require.Len(t, previa.AlreadyCategorizedItems, 1)
	oferta := previa.AlreadyCategorizedItems[0]
	assert.Equal(t, uuidDe(1), oferta.ID)
	assert.Equal(t, "cat-mercado", oferta.CurrentCategoryID)
	assert.Equal(t, "Mercado", oferta.CurrentCategoryName)
	assert.Equal(t, "cat-cdb", oferta.CategoryID)
	assert.Equal(t, "Renda fixa", oferta.CategoryName)
	assert.Equal(t, investment.FlowContribution, oferta.Flow)

	assert.Zero(t, previa.Marked)
	assert.Zero(t, previa.Unmatched)

	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09"})
	assert.Zero(t, real.Marked)
	assert.Zero(t, c.ledger.chamadasSetCurrent, "sem a flag, o UPDATE de troca não é sequer emitido")
	assert.Equal(t, "cat-mercado", *c.ledger.por(uuidDe(1)).CategoryID)
	assert.Equal(t, "cat-cdb", *c.ledger.por(uuidDe(2)).CategoryID)
	assert.Equal(t, "cat-mercado", *c.ledger.por(uuidDe(3)).CategoryID)
}

// `overwriteCategorized` AUSENTE é false, sempre: o caminho seguro é o padrão.
func TestFlagAusenteEhFalse(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))

	// DetectInput sem OverwriteCategorized: zero value é false.
	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09"})
	assert.Zero(t, real.Marked)
	assert.EqualValues(t, 1, real.AlreadyCategorized)
	assert.Equal(t, "cat-mercado", *c.ledger.por(uuidDe(1)).CategoryID)
}

// Aceite 9, segunda metade: com a flag, o conjunto de `alreadyCategorized`
// migra INTEIRO para `marked`, `alreadyCategorized` volta 0, e a troca NUNCA
// desfaz uma marcação de investimento — a restrição está na allowlist do
// WHERE, e o teste confere a allowlist que foi ao repositório.
func TestComAFlagOConjuntoMigraInteiroENuncaDesfazMarcacao(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindExpense, "CDB 30 DIAS", 100000, 2, "2026-09", "cat-cdb"))
	c.ledger.juntar(lanc(casaA, uuidDe(3), transaction.KindExpense, "CDB NOVO", 50000, 3, "2026-09", ""))

	semFlag := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})
	comFlag := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true, OverwriteCategorized: true})

	assert.EqualValues(t, 1, semFlag.Marked)
	assert.EqualValues(t, 1, semFlag.AlreadyCategorized)
	assert.EqualValues(t, semFlag.Marked+semFlag.AlreadyCategorized, comFlag.Marked,
		"o conjunto de alreadyCategorized tem de migrar INTEIRO para marked")
	assert.Zero(t, comFlag.AlreadyCategorized)
	assert.Empty(t, comFlag.AlreadyCategorizedItems)
	assert.Equal(t, semFlag.Unmatched, comFlag.Unmatched, "a flag não mexe em quem não bateu")

	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})
	assert.EqualValues(t, 2, real.Marked)

	// A allowlist que foi ao WHERE são as categorias income/expense — e SÓ
	// elas. É o que impede a troca de desfazer uma marcação.
	assert.Equal(t, []string{"cat-mercado", "cat-salario"}, c.ledger.ultimaAllowlist)
	assert.NotContains(t, c.ledger.ultimaAllowlist, "cat-cdb")
	assert.NotContains(t, c.ledger.ultimaAllowlist, "cat-resgate")

	assert.Equal(t, "cat-cdb", *c.ledger.por(uuidDe(1)).CategoryID, "a categoria comum foi substituída")
	assert.Equal(t, "cat-cdb", *c.ledger.por(uuidDe(2)).CategoryID, "a marcação anterior continua de pé")
	assert.Equal(t, "cat-cdb", *c.ledger.por(uuidDe(3)).CategoryID)

	segunda := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})
	assert.Zero(t, segunda.Marked, "com a flag, a segunda execução também marca 0")
}

// Item 16 do plano, segunda metade: se alguém marcar a linha ENTRE a prévia e
// a confirmação, o UPDATE afeta ZERO linhas.
//
// A corrida é encenada onde ela existe de verdade: o cálculo roda FORA da
// transação (achado A2), então o gancho `antes` do txFake escreve exatamente
// na janela entre o plano e o UPDATE.
func TestTrocaNaoAconteceSeAlguemMarcarEntreAPreviaEAConfirmacao(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))

	c.tx.antes = func() {
		// Alguém marcou a linha à mão, com outra categoria de investimento.
		c.categorias.juntar(casaA, "cat-tesouro", "Tesouro", category.KindInvestment)
		t := c.ledger.por(uuidDe(1))
		outra := "cat-tesouro"
		t.CategoryID = &outra
		c.ledger.juntar(t)
	}

	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})

	assert.Equal(t, 1, c.ledger.chamadasSetCurrent, "o UPDATE foi emitido — é o banco que decide")
	assert.Zero(t, real.Marked, "o UPDATE tem de afetar 0 linhas: a categoria de hoje não está na allowlist")
	assert.Equal(t, "cat-tesouro", *c.ledger.por(uuidDe(1)).CategoryID, "a marcação de quem chegou antes foi preservada")
}

// O mesmo, do lado do `category_id IS NULL`: quem categorizou a linha entre o
// plano e o UPDATE não é sobrescrito, e não é contado.
func TestMarcacaoNaoSobrescreveQuemCategorizouNoMeio(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	c.tx.antes = func() {
		t := c.ledger.por(uuidDe(1))
		escolhida := "cat-mercado"
		t.CategoryID = &escolhida
		c.ledger.juntar(t)
	}

	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09"})
	assert.Zero(t, real.Marked)
	assert.Equal(t, "cat-mercado", *c.ledger.por(uuidDe(1)).CategoryID, "a escolha humana foi sobrescrita")
}

// Aceite 10: nunca toca lançamento excluído, de transferência, ou de outra
// casa. As duas casas têm o MESMO mês e a MESMA palavra-chave.
func TestDetectNaoTocaExcluidoTransferenciaNemOutraCasa(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	excluida := lanc(casaA, uuidDe(2), transaction.KindExpense, "CDB EXCLUIDO", 1000, 2, "2026-09", "")
	excluida.DeletedAt = &relogioFixo
	c.ledger.juntar(excluida)

	perna := lanc(casaA, uuidDe(3), transaction.KindTransferOut, "CDB TRANSFERENCIA", 1000, 3, "2026-09", "")
	c.ledger.juntar(perna)

	daVizinha := lanc(casaB, uuidDe(4), transaction.KindExpense, "CDB DA VIZINHA", 999999, 4, "2026-09", "")
	c.ledger.juntar(daVizinha)

	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09"})

	assert.EqualValues(t, 1, real.Marked)
	assert.Equal(t, casaA, c.ledger.ultimaCasaDoMes, "a leitura do mês foi feita com a casa do token")
	assert.Nil(t, c.ledger.por(uuidDe(2)).CategoryID, "excluído foi tocado")
	assert.Nil(t, c.ledger.por(uuidDe(3)).CategoryID, "perna de transferência foi tocada")
	assert.Nil(t, c.ledger.por(uuidDe(4)).CategoryID, "lançamento da outra casa foi tocado")

	// E do lado de lá: a vizinha vê a linha DELA, e só.
	daVizinhaView := detectar(t, c, casaB, investment.DetectInput{Month: "2026-09", DryRun: true})
	require.Len(t, daVizinhaView.Items, 1)
	assert.Equal(t, uuidDe(4), daVizinhaView.Items[0].ID)
	assert.Equal(t, "cat-b-cdb", daVizinhaView.Items[0].CategoryID)
}

// Aceite 11: mês com mais de MaxCandidates candidatos é 422 e NADA escrito —
// nem uma execução parcial. "Candidato" é toda receita/despesa viva do mês,
// COM ou SEM categoria, porque todas são lidas para montar a prévia.
func TestMesGrandeDemaisEhRecusadoAntesDeQualquerEscrita(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	for i := range investment.MaxCandidates + 1 {
		categoria := ""
		if i%2 == 0 {
			// Metade JÁ TEM categoria: elas contam para o teto do mesmo jeito.
			categoria = "cat-mercado"
		}
		c.ledger.juntar(lanc(casaA, uuidDe(i+1), transaction.KindExpense, "LINHA", 100, 1, "2026-09", categoria))
	}

	_, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, investment.ErrTooManyCandidates)

	assert.Equal(t, investment.MaxCandidates+1, c.ledger.ultimoLimiteDoMes,
		"o teto é descoberto pedindo teto+1, sem ler o mês inteiro")
	assert.Zero(t, c.tx.chamadas, "abriu transação num mês que não cabe")
	assert.Zero(t, c.ledger.chamadasSetNull)
	assert.Zero(t, c.ledger.chamadasSetCurrent)
	assert.Empty(t, c.auditoria.entradas)
}

// Auditoria: UMA entrada por execução real, com o MÊS como entidade e sem
// nenhum campo que pudesse carregar descrição, valor ou palavra-chave.
func TestAuditoriaDaExecucaoReal(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})
	assert.Empty(t, c.auditoria.entradas, "a prévia não escreveu nada, então não audita nada")

	detectar(t, c, casaA, investment.DetectInput{Month: "2026-09"})
	require.Len(t, c.auditoria.entradas, 1)
	e := c.auditoria.entradas[0]
	assert.Equal(t, audit.ActionTransactionInvestmentsDetected, e.Action)
	assert.Equal(t, audit.EntityTransactionMonth, e.Entity)
	assert.Equal(t, "2026-09", e.EntityID)
	assert.Equal(t, casaA, e.HouseholdID)
	assert.Equal(t, "user-1", e.UserID)
	assert.Equal(t, "203.0.113.7", e.IP)
}

// A auditoria roda DENTRO da transação: se o rastro não couber, a escrita não
// vale, e o erro sobe.
func TestFalhaDaAuditoriaDerrubaAExecucao(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))
	c.auditoria.erro = errFalhaDoBanco

	_, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, errFalhaDoBanco)
	assert.Empty(t, c.auditoria.entradas)
}

// Ator sem casa nunca chega ao banco, e mês malformado é recusado antes de
// tudo — inclusive antes de carregar palavras-chave.
func TestDetectValidaAntesDeConsultar(t *testing.T) {
	t.Parallel()

	t.Run("sem casa", func(t *testing.T) {
		t.Parallel()
		c := casaComTudo()
		_, err := c.svc.Detect(context.Background(), investment.Actor{}, investment.DetectInput{Month: "2026-09"})
		require.ErrorIs(t, err, investment.ErrUnauthenticated)
		assert.Zero(t, c.ledger.chamadasListDoMes)
		assert.Zero(t, c.tx.chamadas)
	})

	for _, mes := range []string{"", "2026-13", "2026-9", "setembro"} {
		t.Run("mês "+mes, func(t *testing.T) {
			t.Parallel()
			c := casaComTudo()
			_, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: mes})
			require.ErrorIs(t, err, transaction.ErrInvalidMonth)
			assert.Zero(t, c.ledger.chamadasListDoMes)
			assert.Zero(t, c.tx.chamadas)
		})
	}
}

// O RECÁLCULO é compartilhado entre a prévia e a execução: o que a prévia
// promete é o que a escrita faz, nos dois modos da flag. Este é o teste que o
// plano pede explicitamente.
func TestPreviaEExecucaoUsamOMesmoCalculo(t *testing.T) {
	t.Parallel()

	for _, flag := range []bool{false, true} {
		t.Run(map[bool]string{false: "sem flag", true: "com flag"}[flag], func(t *testing.T) {
			t.Parallel()

			c := casaComTudo()
			c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))
			c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindExpense, "CDB ANTIGO", 100000, 2, "2026-09", "cat-mercado"))
			c.ledger.juntar(lanc(casaA, uuidDe(3), transaction.KindExpense, "ZZZZ QQQQ", 9000, 3, "2026-09", ""))
			c.ledger.juntar(lanc(casaA, uuidDe(4), transaction.KindIncome, "RESGATE CDB", 5000, 4, "2026-09", ""))

			previa := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true, OverwriteCategorized: flag})
			real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", OverwriteCategorized: flag})

			assert.Equal(t, previa.Marked, real.Marked, "prévia e execução discordaram sobre `marked`")
			assert.Equal(t, previa.Unmatched, real.Unmatched)
			assert.Equal(t, previa.AlreadyCategorized, real.AlreadyCategorized)
			assert.Len(t, previa.Items, int(previa.Marked), "a lista da prévia tem de descrever exatamente o que ela promete")
		})
	}
}

// A prévia mostra pontuação e palavra-chave — mas a palavra NUNCA sai daqui
// para log nem para auditoria (só para a tela).
func TestPreviaTrazPontuacaoEPalavra(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	view := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})
	require.Len(t, view.Items, 1)
	it := view.Items[0]
	assert.Equal(t, "cat-cdb", it.CategoryID)
	assert.Equal(t, "Renda fixa", it.CategoryName)
	assert.Equal(t, investment.FlowContribution, it.Flow)
	assert.GreaterOrEqual(t, it.MatchScore, 80)
	assert.LessOrEqual(t, it.MatchScore, 100)
	assert.Equal(t, "cdb", it.MatchedKeyword)
}

// Receita nunca recebe categoria de APORTE e despesa nunca de RESGATE: o
// pareamento é uma pergunta só (category.AceitaLancamento), e esta rota o
// reconfere antes de gravar.
func TestPareamentoEhRespeitadoNaEscrita(t *testing.T) {
	t.Parallel()

	c := montar()
	// A casa só tem categoria de APORTE, com a palavra que casa com a receita.
	c.categorias.juntar(casaA, "cat-cdb", "Renda fixa", category.KindInvestment, "cdb")
	c.contas.juntar(casaA, "acc-1", "Nubank")
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindIncome, "CDB 15 DIAS", 5000, 1, "2026-09", ""))

	view := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09"})
	assert.Zero(t, view.Marked, "receita não pode receber categoria de aporte")
	assert.Nil(t, c.ledger.por(uuidDe(1)).CategoryID)
}

// As três listas têm teto; as CONTAGENS são sempre completas.
func TestListasTemTetoEContagensSaoCompletas(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	total := investment.MaxListed + 25
	for i := range total {
		c.ledger.juntar(lanc(casaA, uuidDe(i+1), transaction.KindExpense, "CDB 15 DIAS", 100, 1, "2026-09", ""))
	}

	view := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})
	assert.EqualValues(t, total, view.Marked, "a contagem tem de ser completa")
	assert.Len(t, view.Items, investment.MaxListed, "a lista tem teto")
}

// ---------------------------------------------------------------------------
// A allowlist do `WHERE` (ADR-029h) — a pergunta fechada com teste
// ---------------------------------------------------------------------------
//
// A promessa é "a troca nunca desfaz uma marcação de investimento", e ela se
// apoia em duas metades. A ORIGEM: só id de natureza income/expense entra na
// allowlist do WHERE. O DESTINO: o valor gravado é SEMPRE uma categoria de
// investimento/resgate (marcaInvestimento). As duas são testadas aqui, porque
// nenhuma das duas sozinha sustenta a promessa.

// ORIGEM, caso hostil: a fonte de categorias devolve o MESMO id duas vezes, com
// naturezas diferentes. Sem a partição estrita, o id entraria nas DUAS listas —
// e estar na allowlist é ser autorizado a ter a categoria substituída.
//
// O índice único de `categories` não produz isso hoje; o teste existe para a
// promessa ser propriedade DESTE código e não da fonte.
func TestIDComNaturezaAmbiguaNuncaEntraNaAllowlist(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	// A fonte devolve "cat-hibrida" duas vezes: primeiro como despesa comum,
	// depois como investimento. A natureza de investimento tem de vencer.
	c.categorias.juntar(casaA, "cat-hibrida", "Híbrida", category.KindExpense)
	c.categorias.juntar(casaA, "cat-hibrida", "Híbrida", category.KindInvestment)

	// Uma linha na categoria ambígua e uma numa categoria comum de verdade: a
	// segunda existe para o UPDATE ser de fato emitido e a allowlist viajar.
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB HIBRIDO", 100000, 1, "2026-09", "cat-hibrida"))
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindExpense, "CDB COMUM", 200000, 2, "2026-09", "cat-mercado"))

	previa := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", DryRun: true})
	for _, it := range previa.AlreadyCategorizedItems {
		assert.NotEqual(t, "cat-hibrida", it.CurrentCategoryID,
			"a prévia ofereceu trocar a categoria de uma linha já marcada — prometer o que o WHERE recusa")
	}

	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})

	require.Equal(t, 1, c.ledger.chamadasSetCurrent, "o UPDATE precisa ter sido emitido para a allowlist ser observável")
	assert.NotContains(t, c.ledger.ultimaAllowlist, "cat-hibrida",
		"id de natureza de investimento chegou à allowlist do WHERE")
	assert.Equal(t, []string{"cat-mercado", "cat-salario"}, c.ledger.ultimaAllowlist)

	assert.EqualValues(t, 1, real.Marked, "só a linha da categoria comum de verdade foi trocada")
	assert.Equal(t, "cat-hibrida", *c.ledger.por(uuidDe(1)).CategoryID, "a marcação ambígua foi desfeita")
	assert.Equal(t, "cat-cdb", *c.ledger.por(uuidDe(2)).CategoryID)
}

// ORIGEM, o caso trivial que precisa continuar trivial: categoria de
// investimento e de resgate NUNCA aparecem na allowlist, em nenhum arranjo.
func TestAllowlistTemSomenteNaturezaComum(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.categorias.juntar(casaA, "cat-tesouro", "Tesouro", category.KindInvestment, "tesouro")
	c.categorias.juntar(casaA, "cat-venda", "Venda de ativo", category.KindRedemption, "venda")
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 100000, 1, "2026-09", "cat-mercado"))

	detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})

	require.NotEmpty(t, c.ledger.ultimaAllowlist)
	for _, id := range c.ledger.ultimaAllowlist {
		kind := map[string]string{
			"cat-mercado": category.KindExpense,
			"cat-salario": category.KindIncome,
		}[id]
		assert.NotEmpty(t, kind, "id sem natureza comum conhecida na allowlist: %s", id)
	}
	assert.NotContains(t, c.ledger.ultimaAllowlist, "cat-cdb")
	assert.NotContains(t, c.ledger.ultimaAllowlist, "cat-resgate")
	assert.NotContains(t, c.ledger.ultimaAllowlist, "cat-tesouro")
	assert.NotContains(t, c.ledger.ultimaAllowlist, "cat-venda")
}

// ORIGEM que vira de investimento na janela: a linha NÃO é trocada.
//
// ⚠️ A expectativa deste teste foi INVERTIDA pelo achado A1 da segunda revisão
// de segurança, e a inversão é o conserto, não uma regressão.
//
// A versão anterior afirmava que a linha continuava marcada, porque o valor
// gravado é sempre uma categoria de investimento — "troca de categoria dentro
// da marcação, nunca desmarcação". O raciocínio olhava só o destino e ignorava
// o que a pessoa fez: ao tornar "Mercado" uma categoria de investimento
// (ADR-029c), ela MARCOU aquelas linhas à mão. Substituí-las viola a garantia
// publicada do `overwriteCategorized`, que é "só substitui categoria de
// natureza income/expense" — e, depois da troca, "Mercado" não é mais nenhuma
// das duas.
//
// Agora a allowlist é PODADA dentro da transação com a natureza relida, e a
// linha fica onde a pessoa a pôs. A poda não é conflito: allowlist menor só faz
// o `WHERE` alcançar menos linhas, que é o mesmo resultado legítimo de alguém
// ter editado a linha no meio.
func TestOrigemQueVirouDeInvestimentoNoMeioNaoEhTrocada(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 100000, 1, "2026-09", "cat-mercado"))

	c.tx.antes = func() {
		// Alguém exerce o ADR-029(c): "Mercado" vira natureza de investimento
		// depois de a prévia ter sido calculada — isto é, a pessoa marcou
		// estas linhas como investimento à mão.
		for i := range c.categorias.porCasa[casaA] {
			if c.categorias.porCasa[casaA][i].ID == "cat-mercado" {
				c.categorias.porCasa[casaA][i].Kind = category.KindInvestment
			}
		}
	}

	real := detectar(t, c, casaA, investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})

	assert.Zero(t, real.Marked, "a marcação feita à mão na janela não pode ser substituída")
	assert.Equal(t, "cat-mercado", *c.ledger.por(uuidDe(1)).CategoryID)
	assert.NotContains(t, c.ledger.ultimaAllowlist, "cat-mercado",
		"a categoria que virou de investimento tem de sair do WHERE da troca")

	// A promessa que continua valendo: o que a linha tem agora é de natureza de
	// investimento — só que por escolha da PESSOA, não por escrita desta rota.
	assert.Contains(t,
		[]string{category.KindInvestment, category.KindRedemption},
		categoriaKind(c, casaA, *c.ledger.por(uuidDe(1)).CategoryID))
}

// categoriaKind devolve a natureza de uma categoria da casa, como a fonte a
// enxerga no instante da consulta.
func categoriaKind(c *cenario, casa, id string) string {
	for _, cat := range c.categorias.porCasa[casa] {
		if cat.ID == id {
			return cat.Kind
		}
	}
	return ""
}
