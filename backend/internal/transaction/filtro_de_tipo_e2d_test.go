package transaction_test

import (
	"net/http"
	"net/url"
	"os"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Filtro de TIPO de GET /transactions (spec 0004 §12, emenda E2d): o
// `kindGroup` recorta a lista no WHERE e o resumo por ARITMÉTICA em Go sobre
// UMA leitura — nunca por cinco consultas diferentes.
//
// O que estes testes travam, além dos números:
//
//   - a PARTIÇÃO: os quatro grupos somam exatamente o "Tudo", sem sobra e sem
//     repetição. É o teste que denuncia dupla contagem;
//   - `investedCents`/`redeemedCents` INVARIANTES ao filtro (ADR-029e) — eles
//     não descrevem a janela, e é deles que vive a frase "Fora destes números";
//   - a despesa SEM CATEGORIA continuando na aba "Despesas" (a armadilha do
//     `NULL NOT IN`, que sumiria em silêncio);
//   - a pendência ZERADA em transferências (a armadilha das pernas, que têm
//     `category_id IS NULL` na projeção crua e pediriam categoria que não
//     existe);
//   - o curto-circuito de "Investimentos" em casa que não marca nada, que sem
//     ele seria 500 para TODA casa no dia da entrega;
//   - e o 400 que nunca devolve o valor recebido.

// cenarioDeTipos monta setembro com uma linha de cada situação que o filtro
// tem de separar — inclusive as duas que os defeitos clássicos escondem: a
// despesa SEM categoria e a receita SEM categoria.
//
//	Tudo ......... 8 lançamentos
//	receitas ..... 2 (salário + receita sem categoria), 5.100,00
//	despesas ..... 2 (mercado + despesa sem categoria), 330,00
//	transferências 2 (as duas pernas do mesmo par)
//	investimentos  2 (aporte 2.000,00 + resgate 500,00)
func cenarioDeTipos(t *testing.T) *ambiente {
	t.Helper()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-2", "Poupança", account.KindChecking)
	mercado := amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	salario := amb.categoria(minhaCasa, "cat-salario", "Salário", category.KindIncome)
	cdb := amb.categoria(minhaCasa, "cat-cdb", "CDB", category.KindInvestment)
	resgate := amb.categoria(minhaCasa, "cat-resgate", "Resgates", category.KindRedemption)

	semear := func(kind string, valor int64, dia int, descricao string, cat *string) {
		amb.repo.semear(transaction.Transaction{
			HouseholdID: minhaCasa, AccountID: "acc-1", Kind: kind, AmountCents: valor,
			Description: descricao, CategoryID: cat,
			OccurredOn: civil.MustNew(2026, 9, dia), CompetenceMonth: "2026-09",
		})
	}
	semear(transaction.KindIncome, 5_000_00, 5, "Salário", ptr(salario.ID))
	semear(transaction.KindIncome, 100_00, 6, "Reembolso", nil)
	semear(transaction.KindIncome, 500_00, 7, "RESGATE CDB", ptr(resgate.ID))
	semear(transaction.KindExpense, 300_00, 8, "Mercado", ptr(mercado.ID))
	semear(transaction.KindExpense, 30_00, 9, "Padaria", nil)
	semear(transaction.KindExpense, 2_000_00, 10, "CDB 15 DIAS", ptr(cdb.ID))
	amb.parDeTransferencia(minhaCasa, "acc-1", "acc-2", 800_00, 11, "grupo-1")
	return amb
}

// listar é o atalho para a mesma janela com um recorte diferente.
func listar(t *testing.T, amb *ambiente, grupo string) transaction.ListView {
	t.Helper()
	v, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
		Month: "2026-09", KindGroup: grupo,
	})
	require.NoError(t, err)
	return v
}

// osCincoRecortes é a ordem em que os testes percorrem as opções: "Tudo"
// primeiro, porque é contra ele que os outros quatro fecham.
var osCincoRecortes = []string{
	"",
	transaction.KindGroupIncome,
	transaction.KindGroupExpense,
	transaction.KindGroupTransfer,
	transaction.KindGroupInvestment,
}

// O coração da tarefa: os quatro grupos PARTICIONAM a janela. Se um lançamento
// aparecesse em dois grupos (ou em nenhum), esta soma não fecharia — e é
// exatamente isso que aconteceria se `income`/`expense` esquecessem de excluir
// os marcados, ou se o `investment` fosse tratado como um `kind`.
func TestFiltroDeTipoParticionaAJanelaSemSobraESemRepeticao(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTipos(t)
	tudo := listar(t, amb, "")
	receitas := listar(t, amb, transaction.KindGroupIncome)
	despesas := listar(t, amb, transaction.KindGroupExpense)
	transferencias := listar(t, amb, transaction.KindGroupTransfer)
	investimentos := listar(t, amb, transaction.KindGroupInvestment)

	assert.Equal(t, int64(8), tudo.Summary.Count)
	soma := receitas.Summary.Count + despesas.Summary.Count +
		transferencias.Summary.Count + investimentos.Summary.Count
	assert.Equal(t, tudo.Summary.Count, soma,
		"os quatro grupos precisam somar o Tudo: sobra é linha invisível, repetição é dupla contagem")

	// E a mesma partição na LISTA, não só no resumo: o predicado do WHERE e a
	// aritmética do resumo respondem à mesma pergunta.
	assert.Len(t, tudo.Items, 8)
	assert.Len(t, receitas.Items, 2)
	assert.Len(t, despesas.Items, 2)
	assert.Len(t, transferencias.Items, 2)
	assert.Len(t, investimentos.Items, 2)
	assert.Equal(t, len(tudo.Items),
		len(receitas.Items)+len(despesas.Items)+len(transferencias.Items)+len(investimentos.Items))
}

// O número que o filtro nomeia é IDÊNTICO ao de Tudo. Um `expenseCents` que
// mudasse ao filtrar por despesas denunciaria dupla contagem — ou que o
// recorte está somando o que o "Tudo" já tinha descontado (ADR-029e).
func TestFiltroDeTipoNaoMudaONumeroQueEleNomeia(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTipos(t)
	tudo := listar(t, amb, "")
	receitas := listar(t, amb, transaction.KindGroupIncome)
	despesas := listar(t, amb, transaction.KindGroupExpense)

	assert.Equal(t, int64(330_00), tudo.Summary.ExpenseCents)
	assert.Equal(t, tudo.Summary.ExpenseCents, despesas.Summary.ExpenseCents)
	assert.Equal(t, int64(5_100_00), tudo.Summary.IncomeCents)
	assert.Equal(t, tudo.Summary.IncomeCents, receitas.Summary.IncomeCents)

	// E o lado que o filtro NÃO nomeia é zero — exibir um zero que nunca muda
	// é ruído, e a tela não o mostra (DESIGN.md E2d (b)).
	assert.Zero(t, receitas.Summary.ExpenseCents)
	assert.Zero(t, despesas.Summary.IncomeCents)
}

// ADR-029(e) + spec 0006 §3.5.2: `investedCents` e `redeemedCents` são os
// MESMOS nas cinco opções. Eles não descrevem a janela — descrevem o que SAIU
// de despesa e receita —, e é deles que vive a frase "Fora destes números:
// R$ X em aportes". Zerá-los sob `?kindGroup=expense` apagaria a explicação
// exatamente onde a omissão é maior.
func TestAporteEResgateSaoInvariantesAoFiltroDeTipo(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTipos(t)
	for _, grupo := range osCincoRecortes {
		nome := grupo
		if nome == "" {
			nome = "tudo"
		}
		t.Run(nome, func(t *testing.T) {
			v := listar(t, amb, grupo)
			assert.Equal(t, int64(2_000_00), v.Summary.InvestedCents,
				"o aporte não muda com o recorte — ele explica o que saiu de despesas")
			assert.Equal(t, int64(500_00), v.Summary.RedeemedCents)
		})
	}
}

// A ARMADILHA do `NULL NOT IN (…)`: no SQL de três valores ele avalia para
// NULL, e não para verdadeiro. Sem o `category_id IS NULL OR` do repositório,
// TODA despesa sem categoria sumiria da aba "Despesas" — em silêncio, com o
// total caindo junto.
func TestFiltroDeDespesasMostraDespesaSemCategoriaEAContaComoPendencia(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTipos(t)
	despesas := listar(t, amb, transaction.KindGroupExpense)

	descricoes := make([]string, 0, len(despesas.Items))
	for _, it := range despesas.Items {
		descricoes = append(descricoes, it.Description)
	}
	assert.Contains(t, descricoes, "Padaria", "despesa SEM categoria é despesa e continua na lista")
	assert.Contains(t, descricoes, "Mercado")
	assert.NotContains(t, descricoes, "CDB 15 DIAS", "aporte não é despesa (ADR-029e)")

	assert.Equal(t, int64(1), despesas.Summary.UncategorizedCount,
		"a pendência do recorte é a despesa sem categoria")
	assert.Equal(t, int64(330_00), despesas.Summary.ExpenseCents,
		"os 30,00 sem categoria entram no total, como entram em Tudo")

	// E do lado das receitas, a mesma coisa com a receita sem categoria.
	receitas := listar(t, amb, transaction.KindGroupIncome)
	assert.Equal(t, int64(1), receitas.Summary.UncategorizedCount)
	// As duas pendências de Tudo são exatamente estas duas, uma de cada lado.
	assert.Equal(t, int64(2), listar(t, amb, "").Summary.UncategorizedCount)
}

// A ARMADILHA das pernas, e ela foi MEDIDA na camada de dados: em ByKind o
// `Uncategorized` de `transfer_out`/`transfer_in` é IGUAL ao Count, porque
// transferência nunca tem categoria (ADR-016) e toda perna casa com
// `category_id IS NULL` na projeção crua. Somar os quatro kinds faria a tela
// pedir que a pessoa categorizasse pernas de transferência — que não têm
// categoria para receber.
func TestFiltroDeTransferenciasZeraReceitaDespesaLiquidoEPendencia(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTipos(t)
	v := listar(t, amb, transaction.KindGroupTransfer)

	assert.Zero(t, v.Summary.IncomeCents, "transferência não é receita (ADR-016)")
	assert.Zero(t, v.Summary.ExpenseCents, "nem despesa")
	assert.Zero(t, v.Summary.NetCents)
	assert.Zero(t, v.Summary.UncategorizedCount,
		"perna de transferência não é pendência: não há categoria para atribuir a ela")

	// O que NÃO é zero: as duas pernas continuam contadas e listadas, e os
	// dois campos de reconciliação atravessam o recorte intactos.
	assert.Equal(t, int64(2), v.Summary.Count)
	assert.Len(t, v.Items, 2)
	assert.Equal(t, int64(2_000_00), v.Summary.InvestedCents)
	assert.Equal(t, int64(500_00), v.Summary.RedeemedCents)
}

// "Investimentos" é o conjunto complementar: os MESMOS dois kinds de
// receita/despesa, separados pela natureza da categoria (ADR-029b).
func TestFiltroDeInvestimentosTrazAporteEResgateEZeraReceitaEDespesa(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTipos(t)
	v := listar(t, amb, transaction.KindGroupInvestment)

	require.Len(t, v.Items, 2)
	descricoes := []string{v.Items[0].Description, v.Items[1].Description}
	assert.Contains(t, descricoes, "CDB 15 DIAS")
	assert.Contains(t, descricoes, "RESGATE CDB")

	assert.Equal(t, int64(2), v.Summary.Count)
	assert.Zero(t, v.Summary.IncomeCents)
	assert.Zero(t, v.Summary.ExpenseCents)
	assert.Zero(t, v.Summary.NetCents)
	assert.Zero(t, v.Summary.UncategorizedCount,
		"aporte e resgate têm categoria por definição — é ela que os marca")
	assert.Equal(t, int64(2_000_00), v.Summary.InvestedCents)
	assert.Equal(t, int64(500_00), v.Summary.RedeemedCents)
}

// `netCents` SEGUE o filtro, como os dois operandos dele — e a identidade
// `income − expense` vale nas CINCO opções. Um campo que contradiz a própria
// fórmula é pior do que um campo redundante.
func TestNetCentsSegueOFiltroEMantemAIdentidade(t *testing.T) {
	t.Parallel()

	amb := cenarioDeTipos(t)
	for _, grupo := range osCincoRecortes {
		nome := grupo
		if nome == "" {
			nome = "tudo"
		}
		t.Run(nome, func(t *testing.T) {
			v := listar(t, amb, grupo)
			assert.Equal(t, v.Summary.IncomeCents-v.Summary.ExpenseCents, v.Summary.NetCents)
		})
	}
	assert.Equal(t, int64(5_100_00-330_00), listar(t, amb, "").Summary.NetCents)
	assert.Equal(t, int64(5_100_00), listar(t, amb, transaction.KindGroupIncome).Summary.NetCents,
		"com receitas ele é o próprio incomeCents — redundante de propósito")
	assert.Equal(t, int64(-330_00), listar(t, amb, transaction.KindGroupExpense).Summary.NetCents,
		"com despesas ele é o espelho do expenseCents")
}

// ADR-029(f): "Investimentos" numa casa que não marca NENHUMA categoria é
// resposta vazia resolvida EM GO, antes do banco.
//
// Sem o curto-circuito, o conjunto vazio desceria ao repositório, que responde
// ErrEmptyCategoryFilter — e o handler traduz isso em 500 genérico. Ou seja:
// TODA casa sem categoria de investimento tomaria 500 nesta aba, que é o
// estado de toda casa no dia da entrega.
func TestInvestimentosSemCategoriaMarcadaDevolveVazioSemTocarOBanco(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 30_00, 9)

	v, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
		Month: "2026-09", KindGroup: transaction.KindGroupInvestment,
	})
	require.NoError(t, err, "casa sem categoria de investimento não pode tomar 500 nesta aba")

	assert.Empty(t, v.Items)
	assert.NotNil(t, v.Items, "lista vazia é `[]`, nunca `null`")
	assert.Nil(t, v.NextCursor)
	assert.Equal(t, transaction.SummaryView{}, v.Summary,
		"resumo zerado — inclusive aporte e resgate, que são zero mesmo sem categoria marcada")

	// A prova de que o atalho é EM GO: nem a página nem o resumo foram ao
	// repositório. Cobrir só um dos dois deixaria o 500 entrar pelo outro.
	assert.Zero(t, amb.repo.listagens, "a lista não pode ir ao banco")
	assert.Empty(t, amb.repo.resumosPedidos, "o resumo também não")
}

// O curto-circuito vale para `investment` e SÓ para ele: os outros três grupos
// continuam consultando normalmente numa casa que não marca nada.
func TestSemCategoriaMarcadaOsOutrosGruposContinuamConsultando(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 30_00, 9)

	v, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
		Month: "2026-09", KindGroup: transaction.KindGroupExpense,
	})
	require.NoError(t, err)
	assert.Len(t, v.Items, 1)
	assert.Equal(t, int64(30_00), v.Summary.ExpenseCents)
	assert.Equal(t, 1, amb.repo.listagens)
	assert.Len(t, amb.repo.resumosPedidos, 1)
}

// Defesa em profundidade: grupo fora da allowlist chegando ao SERVIÇO falha
// FECHADO. Tratá-lo como "sem filtro" devolveria a janela INTEIRA a quem pediu
// um recorte — o modo silencioso de vazar o que o filtro existia para
// esconder. E nada é consultado.
func TestKindGroupForaDaAllowlistFalhaFechadoNoServico(t *testing.T) {
	t.Parallel()

	for _, valor := range []string{"transfer_out", "transfer_in", "all", "EXPENSE", "redemption", "investment "} {
		t.Run(valor, func(t *testing.T) {
			t.Parallel()
			amb := cenarioDeTipos(t)
			_, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
				Month: "2026-09", KindGroup: valor,
			})
			require.ErrorIs(t, err, transaction.ErrUnknownKindGroup)
			assert.NotContains(t, err.Error(), valor, "o erro não repete o valor recebido")
			assert.Zero(t, amb.repo.listagens)
			assert.Empty(t, amb.repo.resumosPedidos)
		})
	}
}

// --- borda HTTP ------------------------------------------------------------

// A allowlist é FECHADA e sensível a caixa, e o 400 nunca ecoa o valor — nem
// na resposta, nem no log. `transfer_out` é o caso que prova que o `kind` cru
// não vazou para o contrato: ele é um `kind` legítimo da coluna e não é um
// grupo.
func TestListHandlerRecusaKindGroupForaDaAllowlistSemEcoar(t *testing.T) {
	t.Parallel()

	casos := []string{
		"transfer_out",
		"all",
		"EXPENSE",
		"redemption",
		"expense' OR 1=1 --",
		"income,expense",
		"<script>alert(1)</script>",
	}
	for _, valor := range casos {
		t.Run(valor, func(t *testing.T) {
			t.Parallel()
			amb := novoHTTPAmbiente(t)
			alvo := "/transactions?month=2026-09&kindGroup=" + url.QueryEscape(valor)
			rec := amb.chamar(t, minhaCasa, http.MethodGet, alvo, "", amb.handler.List)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			require.Contains(t, campos, "kindGroup")

			// A mensagem cita a ALLOWLIST...
			for _, aceito := range []string{"income", "expense", "transfer", "investment"} {
				assert.Contains(t, campos["kindGroup"], aceito)
			}
			// ...e nunca o valor recebido, nem na resposta nem no log.
			assert.NotContains(t, rec.Body.String(), valor)
			assert.NotContains(t, amb.logs.String(), valor)

			// Nada foi consultado: a recusa acontece na borda.
			assert.Zero(t, amb.repo.listagens)
			assert.Empty(t, amb.repo.resumosPedidos)
		})
	}
}

// A chave REPETIDA é 400, e com mensagem PRÓPRIA (spec 0004 §12.5.5).
//
// Reaproveitar a mensagem da allowlist diria "Informe um destes tipos: …" a
// quem informou dois tipos válidos — instrução errada para o defeito real. A
// nova aponta a ação, não ecoa valor e não diz quantas ocorrências chegaram.
func TestListHandlerRecusaKindGroupRepetidoComMensagemPropria(t *testing.T) {
	t.Parallel()

	// As três formas da §12.5.5, na mesma tabela: valores diferentes, valores
	// IGUAIS e a segunda ocorrência VAZIA — esta última é a pior, porque quem
	// lê a última obtém "Tudo" e a tela mostra o mês inteiro sob o rótulo
	// "Despesas".
	casos := []struct{ nome, query string }{
		{"valores diferentes", "kindGroup=expense&kindGroup=income"},
		{"valores iguais", "kindGroup=expense&kindGroup=expense"},
		{"segunda vazia", "kindGroup=expense&kindGroup="},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			amb := novoHTTPAmbiente(t)
			amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
			amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 30_00, 9)

			rec := amb.chamar(t, minhaCasa, http.MethodGet,
				"/transactions?month=2026-09&"+c.query, "", amb.handler.List)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo,
				"parâmetro repetido é falha de validação, não categoria nova no enum fechado")
			require.Contains(t, campos, "kindGroup")
			assert.Equal(t, "Informe o tipo uma única vez.", campos["kindGroup"])
			assert.NotContains(t, campos["kindGroup"], "expense",
				"a mensagem não ecoa o valor recebido")
			assert.NotContains(t, campos["kindGroup"], "2",
				"a mensagem não conta ao cliente quantas ocorrências chegaram")

			assert.Zero(t, amb.repo.listagens, "nada sai para o banco")
			assert.Empty(t, amb.repo.resumosPedidos)
		})
	}
}

// UMA ocorrência vazia NÃO é ambígua e continua sendo Tudo — a regra é sobre a
// repetição da chave, não sobre o valor vazio. Sem esta linha, "recusar
// ambiguidade" viraria "recusar a URL canônica de Tudo com a chave presente".
//
// `?kindGroup=` (vazio) é "Tudo", exatamente como a ausência da chave — não é
// 400 e não é um quinto estado. Uma URL colada com a chave vazia não pode
// virar erro.
func TestListHandlerTrataKindGroupVazioComoTudo(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 30_00, 9)
	amb.lancamento(minhaCasa, "acc-1", transaction.KindIncome, "Salário", 5_000_00, 5)

	semChave := amb.chamar(t, minhaCasa, http.MethodGet, "/transactions?month=2026-09", "", amb.handler.List)
	require.Equal(t, http.StatusOK, semChave.Code, semChave.Body.String())

	vazio := amb.chamar(t, minhaCasa, http.MethodGet, "/transactions?month=2026-09&kindGroup=", "", amb.handler.List)
	require.Equal(t, http.StatusOK, vazio.Code, vazio.Body.String())

	assert.JSONEq(t, semChave.Body.String(), vazio.Body.String(),
		"a chave vazia responde exatamente o mesmo que a chave ausente")
}

// O caminho feliz pelo HTTP, com o contrato conferido: o recorte responde 200
// e o corpo continua sendo um TransactionList completo.
func TestListHandlerAceitaOsQuatroGruposEMantemOContrato(t *testing.T) {
	t.Parallel()

	schemas := schemasDoContrato(t)
	for _, grupo := range []string{"income", "expense", "transfer", "investment"} {
		t.Run(grupo, func(t *testing.T) {
			t.Parallel()
			amb := novoHTTPAmbiente(t)
			// O cenário inteiro, montado sobre o mesmo ambiente HTTP.
			amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
			amb.conta(minhaCasa, "acc-2", "Poupança", account.KindChecking)
			cdb := amb.categoria(minhaCasa, "cat-cdb", "CDB", category.KindInvestment)
			amb.repo.semear(transaction.Transaction{
				HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
				AmountCents: 2_000_00, Description: "CDB 15 DIAS", CategoryID: ptr(cdb.ID),
				OccurredOn: civil.MustNew(2026, 9, 10), CompetenceMonth: "2026-09",
			})
			amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 30_00, 9)
			amb.lancamento(minhaCasa, "acc-1", transaction.KindIncome, "Salário", 5_000_00, 5)
			amb.parDeTransferencia(minhaCasa, "acc-1", "acc-2", 800_00, 11, "grupo-1")

			rec := amb.chamar(t, minhaCasa, http.MethodGet,
				"/transactions?month=2026-09&kindGroup="+grupo, "", amb.handler.List)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			conferirContrato(t, schemas, "TransactionList", rec.Body.Bytes())
		})
	}
}

// O recorte NÃO PODE virar esconderijo de corrupção. Sob `transfer` a receita
// publicada é zero por construção, então uma receita CRUA impossível
// (marcado > total) passaria despercebida se só o número publicado fosse
// conferido. Ela é verificada na matéria-prima, e o desfecho é 500 genérico —
// nunca um número inventado na tela.
func TestRecorteNaoMascaraResumoCorrompido(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-2", "Poupança", account.KindChecking)
	amb.parDeTransferencia(minhaCasa, "acc-1", "acc-2", 800_00, 11, "grupo-1")
	// Agregados coerentes entre si; a linha CRUA é que é impossível.
	amb.repo.resumoForcado = &transaction.Summary{
		Count: 2,
		ByKind: []transaction.SummaryKindTotals{
			{Kind: transaction.KindIncome, Count: 1, TotalCents: 100_00, MarkedTotalCents: 900_00, MarkedCount: 1},
			{Kind: transaction.KindTransferIn, Count: 1, TotalCents: 800_00, Uncategorized: 1},
			{Kind: transaction.KindTransferOut, Count: 1, TotalCents: 800_00, Uncategorized: 1},
		},
	}

	rec := amb.chamar(t, minhaCasa, http.MethodGet,
		"/transactions?month=2026-09&kindGroup=transfer", "", amb.handler.List)
	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeInternalError, codigo)
	assert.Empty(t, campos, "500 genérico não aponta campo que a pessoa possa corrigir")
	// O log leva CONTAGENS, nunca centavos (S8).
	assert.Contains(t, amb.logs.String(), "lancamentos=")
	assert.NotContains(t, amb.logs.String(), "90000")
}

// --- contrato --------------------------------------------------------------

// O enum publicado e a allowlist do domínio não podem divergir: o dia em que
// divergirem, ou a API recusa o que o contrato promete, ou aceita o que ele
// não declara.
func TestContratoPublicaOFiltroDeTipoIgualAAllowlistDoDominio(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../api/openapi.yaml")
	require.NoError(t, err, "api/openapi.yaml precisa existir (ADR-006)")

	var doc struct {
		Paths map[string]struct {
			Get struct {
				Parameters []struct {
					Name   string `yaml:"name"`
					In     string `yaml:"in"`
					Schema struct {
						Ref string `yaml:"$ref"`
					} `yaml:"schema"`
				} `yaml:"parameters"`
			} `yaml:"get"`
		} `yaml:"paths"`
		Components struct {
			Schemas map[string]struct {
				Enum []string `yaml:"enum"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	esquema, ok := doc.Components.Schemas["TransactionKindGroup"]
	require.True(t, ok, "o schema TransactionKindGroup precisa existir na spec")
	assert.Equal(t, []string{"income", "expense", "transfer", "investment"}, esquema.Enum)

	// Todo valor do enum passa na allowlist do domínio...
	for _, v := range esquema.Enum {
		assert.True(t, transaction.ValidKindGroup(v), "o contrato publica %q, que o domínio recusa", v)
	}
	// ...e o que está fora dele é recusado, inclusive os `kind` crus.
	for _, v := range []string{"all", "transfer_out", "transfer_in", "EXPENSE", "redemption"} {
		assert.False(t, transaction.ValidKindGroup(v), "o domínio aceita %q, que o contrato não declara", v)
	}

	var declarado bool
	for _, p := range doc.Paths["/transactions"].Get.Parameters {
		if p.Name == "kindGroup" {
			declarado = true
			assert.Equal(t, "query", p.In)
			assert.Equal(t, "#/components/schemas/TransactionKindGroup", p.Schema.Ref,
				"o parâmetro precisa apontar para o enum, e não repetir a lista")
		}
	}
	assert.True(t, declarado, "GET /transactions precisa declarar o parâmetro kindGroup")
}
