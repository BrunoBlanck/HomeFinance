package investment_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// A NATUREZA do destino, relida dentro da transação — achado A1 da segunda
// revisão de segurança.
//
// A primeira versão da reconferência DECLARAVA não conferir natureza, com a
// justificativa de que trocá-la dentro do mesmo lado do dinheiro "não invalida
// a marcação". A justificativa era falsa para esta rota: `mesmoLadoDoDinheiro`
// põe `expense` e `investment` do MESMO lado, então `investment → expense` é
// aceito mesmo com a categoria em uso (ADR-029c) — e a natureza do destino é
// exatamente o que qualifica a linha como aporte (`marcaInvestimento`).
//
// Foi a ausência destes casos que deixou o furo passar pelo QA.

// trocarNatureza aplica o que PATCH /categories/{id} faz quando a troca é
// dentro do mesmo lado do dinheiro: aceita mesmo com a categoria em uso e mesmo
// com subcategorias.
func (c *categoriasFake) trocarNatureza(householdID, id, kind string) {
	for i := range c.porCasa[householdID] {
		if c.porCasa[householdID][i].ID == id {
			c.porCasa[householdID][i].Kind = kind
		}
	}
}

// Primeira face, a exploração completa: um único ator autenticado transforma o
// `detect` em recategorização em massa de comum para comum.
//
// O grupo CDB é de investimento quando o plano roda; na janela de cálculo
// (15 s de transaction.PlanTimeout) um PATCH o torna `expense`, o que é ACEITO;
// sem a releitura da natureza, o UPDATE gravaria CDB — agora categoria COMUM —
// por cima de Mercado, em massa, sem desfazer e contornando o teto de 120/h do
// PATCH /transactions/{id}.
func TestDestinoQueVirouCategoriaComumNaJanelaNaoEhGravado(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindExpense, "CDB 30 DIAS", 100000, 2, "2026-09", "cat-mercado"))

	c.tx.antes = func() {
		// PATCH /categories/cat-cdb {"kind":"expense"} — aceito: grupo, mesmo
		// lado do dinheiro, uso irrelevante.
		c.categorias.trocarNatureza(casaA, "cat-cdb", category.KindExpense)
	}

	_, err := c.svc.Detect(context.Background(), ator(casaA),
		investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})
	require.ErrorIs(t, err, investment.ErrDestinationChanged)

	assert.Zero(t, c.ledger.chamadasSetCurrent, "nenhum UPDATE pode ter sido emitido")
	assert.Zero(t, c.ledger.chamadasSetNull)
	assert.Equal(t, "cat-mercado", *c.ledger.por(uuidDe(1)).CategoryID, "a categoria escolhida à mão ficou intacta")
	assert.Equal(t, "cat-mercado", *c.ledger.por(uuidDe(2)).CategoryID)
	assert.Empty(t, c.auditoria.entradas)
}

// A mesma face SEM a flag: o lote de `category_id IS NULL` gravaria o mesmo
// destino já trocado em toda linha sem categoria do mês.
func TestDestinoQueVirouCategoriaComumTambemBarraOLoteSemCategoria(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	c.tx.antes = func() { c.categorias.trocarNatureza(casaA, "cat-cdb", category.KindExpense) }

	_, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, investment.ErrDestinationChanged)

	assert.Zero(t, c.ledger.chamadasSetNull)
	assert.Nil(t, c.ledger.por(uuidDe(1)).CategoryID)
}

// Trocar de investimento para RESGATE também é recusado: muda o lado do
// dinheiro, e despesa não aceita categoria de resgate
// (category.AceitaLancamento). A reconferência repete a MESMA pergunta do
// plano, com natureza fresca.
func TestDestinoQueTrocouDeLadoDoDinheiroNaJanelaEhConflito(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	c.tx.antes = func() { c.categorias.trocarNatureza(casaA, "cat-cdb", category.KindRedemption) }

	_, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, investment.ErrDestinationChanged)
	assert.Zero(t, c.ledger.chamadasSetNull)
}

// Segunda face: a ORIGEM. `plano.allowlist` congela quem era categoria comum.
// Se na janela um grupo `expense` virar `investment` — o caminho que o ADR-029c
// existe para oferecer —, ele continuaria na allowlist e a troca desfaria a
// marcação que a pessoa ACABOU de fazer à mão.
//
// Aqui a resposta é PODAR, e não falhar: allowlist menor só faz o `WHERE`
// alcançar menos linhas, que é o mesmo resultado legítimo de alguém ter editado
// a linha no meio.
//
// ⚠️ Este teste fixa o eixo da NATUREZA da poda, e só ele. O `if !vivo` que
// vem antes dele em podarAllowlist tem a mesma propriedade do EIXO 1 da
// reconferência: nenhum teste deste pacote o derruba (a excluída volta como
// zero value e o filtro de natureza já a descarta), e quem o guarda é o
// COMPILADOR — apagá-lo deixa `vivo` declarado e não usado. A explicação
// inteira, com o mapa de qual teste mata qual mutante, está no cabeçalho de
// detect_toctou_test.go.
func TestOrigemQueVirouInvestimentoNaJanelaSaiDaAllowlist(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	// A pessoa marcou esta linha à mão tornando "Mercado" categoria de
	// investimento; o detect não pode desfazer isso.
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))
	// Esta continua numa categoria comum de verdade e PODE ser trocada.
	c.ledger.juntar(lanc(casaA, uuidDe(2), transaction.KindIncome, "RESGATE CDB", 50000, 2, "2026-09", "cat-salario"))

	c.tx.antes = func() { c.categorias.trocarNatureza(casaA, "cat-mercado", category.KindInvestment) }

	view, err := c.svc.Detect(context.Background(), ator(casaA),
		investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})
	require.NoError(t, err, "podar não é conflito: a operação segue com o que continua comum")

	assert.NotContains(t, c.ledger.ultimaAllowlist, "cat-mercado",
		"categoria que virou de investimento na janela NÃO pode ir ao WHERE da troca")
	assert.Contains(t, c.ledger.ultimaAllowlist, "cat-salario")

	assert.Equal(t, "cat-mercado", *c.ledger.por(uuidDe(1)).CategoryID,
		"a marcação feita à mão na janela foi preservada")
	assert.Equal(t, "cat-resgate", *c.ledger.por(uuidDe(2)).CategoryID)
	assert.EqualValues(t, 1, view.Marked, "só a linha que continuava comum foi trocada")
}

// O extremo da poda: TODA categoria comum virou de investimento na janela. O
// lote de troca é pulado inteiro — zero linha afetada, que é a resposta certa —
// e nunca se emite um IN vazio, que é erro de sintaxe em três dos quatro
// dialetos.
func TestAllowlistQueEsvaziaPulaOLoteDeTrocaSemQuebrar(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))

	c.tx.antes = func() {
		c.categorias.trocarNatureza(casaA, "cat-mercado", category.KindInvestment)
		c.categorias.trocarNatureza(casaA, "cat-salario", category.KindRedemption)
	}

	view, err := c.svc.Detect(context.Background(), ator(casaA),
		investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})
	require.NoError(t, err)

	assert.Zero(t, c.ledger.chamadasSetCurrent, "nenhum comando com allowlist vazia")
	assert.Zero(t, view.Marked)
	assert.Equal(t, "cat-mercado", *c.ledger.por(uuidDe(1)).CategoryID)
	assert.Len(t, c.auditoria.entradas, 1, "a execução real aconteceu e continua auditada")
}

// A releitura da allowlist só acontece quando existe lote de troca: sem ele ela
// não vai a comando nenhum, e pedir 200 ids a mais seria custo sem pergunta.
func TestSemLoteDeTrocaAReconferenciaSoPedeOsDestinos(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	_, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Equal(t, 1, c.categorias.chamadasAtribuiveis)
	assert.Equal(t, []string{"cat-cdb"}, c.categorias.ultimosAtribuiveis)
}

// --- auditoria: a execução destrutiva tem ação PRÓPRIA (achado A5) ----------

// Sem ações separadas, "preencheu 500 lançamentos vazios" e "trocou 500
// categorias que alguém escolheu à mão" são a mesma linha de auditoria — e a
// segunda não tem desfazer. A perícia não pode depender da retenção do log de
// aplicação.
func TestAuditoriaDistingueSubstituicaoDePreenchimento(t *testing.T) {
	t.Parallel()

	semFlag := casaComTudo()
	semFlag.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))
	_, err := semFlag.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.NoError(t, err)
	require.Len(t, semFlag.auditoria.entradas, 1)
	assert.Equal(t, audit.ActionTransactionInvestmentsDetected, semFlag.auditoria.entradas[0].Action)

	comFlag := casaComTudo()
	comFlag.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", "cat-mercado"))
	_, err = comFlag.svc.Detect(context.Background(), ator(casaA),
		investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})
	require.NoError(t, err)
	require.Len(t, comFlag.auditoria.entradas, 1)
	assert.Equal(t, audit.ActionTransactionInvestmentsOverwritten, comFlag.auditoria.entradas[0].Action)

	// A entidade continua sendo o MÊS, e a entrada continua sem contagem, sem
	// descrição e sem palavra-chave (S8).
	assert.Equal(t, audit.EntityTransactionMonth, comFlag.auditoria.entradas[0].Entity)
	assert.Equal(t, "2026-09", comFlag.auditoria.entradas[0].EntityID)
}

// A ação sai da FLAG do pedido, e não de ter havido linha trocada: quem pediu
// autorização para substituir fica registrado mesmo com zero linha afetada.
func TestAuditoriaDeSubstituicaoRegistraAIntencaoMesmoComZeroLinhas(t *testing.T) {
	t.Parallel()

	c := casaComTudo()
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "PADARIA DA ESQUINA", 1500, 1, "2026-09", ""))

	view, err := c.svc.Detect(context.Background(), ator(casaA),
		investment.DetectInput{Month: "2026-09", OverwriteCategorized: true})
	require.NoError(t, err)
	assert.Zero(t, view.Marked)

	require.Len(t, c.auditoria.entradas, 1)
	assert.Equal(t, audit.ActionTransactionInvestmentsOverwritten, c.auditoria.entradas[0].Action)
}

// --- a janela do PATCH /categories/{id}, provada e não deduzida -------------

// `podeTrocarNatureza` só trava quando há FILHA ou USO, e `IsGroup()` é apenas
// `ParentID == nil` — toda categoria de primeiro nível. Logo
// `investment → expense` numa categoria de TOPO ainda SEM USO passa pelo PATCH
// sem nenhuma recusa.
//
// Isso importa exatamente aqui: o DESTINO do `detect` é justamente uma
// categoria que pode ainda não ter uso — é o `detect` que vai dar uso a ela. O
// teste que já existia do lado do `category` (investimentos_test.go) cobre só
// `emUso: true`, então este prova a outra metade pela rota que sofre a corrida,
// em vez de deduzir que está coberta.
//
// Trocar a natureza de uma categoria de topo ainda sem uso é legítimo como
// produto (criar "Mercado" como despesa e mudar antes de usar) e é anterior à
// E7. O que não pode é a CORRIDA — e quem a fecha é a releitura aqui dentro.
func TestDestinoDeTopoSemUsoQueTrocaDeNaturezaNaJanelaEhConflito(t *testing.T) {
	t.Parallel()

	c := casaComTudo()

	// Pré-condição explícita: cat-cdb é de TOPO (sem pai), sem filhas e SEM
	// USO — nenhum lançamento aponta para ela antes desta execução. É o estado
	// em que o PATCH aceita a troca sem nenhuma trava.
	semUso := true
	for _, id := range c.ledger.ordem {
		if l := c.ledger.linhas[id]; l.CategoryID != nil && *l.CategoryID == "cat-cdb" {
			semUso = false
		}
	}
	require.True(t, semUso, "a pré-condição do PoC é a categoria de destino AINDA NÃO TER uso")
	for _, cat := range c.categorias.porCasa[casaA] {
		require.NotEqual(t, "cat-cdb", derefParent(cat.ParentID), "cat-cdb não pode ter filhas")
	}

	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	c.tx.antes = func() {
		// O PoC: PATCH aceito porque a categoria é de topo, sem filha e sem uso.
		c.categorias.trocarNatureza(casaA, "cat-cdb", category.KindExpense)
	}

	_, err := c.svc.Detect(context.Background(), ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, investment.ErrDestinationChanged,
		"a corrida do PATCH numa categoria de topo sem uso tem de cair na reconferência")

	assert.Zero(t, c.ledger.chamadasSetNull)
	assert.Nil(t, c.ledger.por(uuidDe(1)).CategoryID)
}

// derefParent devolve o id do pai, ou vazio.
func derefParent(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// O prazo da fase de cálculo só pode ser ENCURTADO pela opção de teste: um
// prazo de teste que esticasse o de produção seria um prazo que depende de
// ninguém chamá-lo errado.
func TestWithPlanTimeoutNaoEstica(t *testing.T) {
	t.Parallel()

	c := montarCom(nil, investment.WithPlanTimeout(24*time.Hour))
	c.categorias.juntar(casaA, "cat-cdb", "Renda fixa", category.KindInvestment, "cdb")
	c.contas.juntar(casaA, "acc-1", "Nubank")
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	// Com o teto respeitado, um contexto já vencido continua vencendo a fase de
	// cálculo. Se a opção esticasse o prazo, o teto de produção deixaria de
	// existir para qualquer montagem que a chamasse.
	vencido, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := c.svc.Detect(vencido, ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, investment.ErrPlanTimeout,
		"a parada VOLUNTÁRIA da fase de cálculo é quem nomeia o prazo (achado N1)")
	require.ErrorIs(t, err, context.DeadlineExceeded, "o motivo original continua na cadeia")
}
