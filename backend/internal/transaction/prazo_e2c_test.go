package transaction_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// PRAZO, FALHA DE BANCO e CANCELAMENTO nas duas rotas que varrem o mês inteiro
// (POST /transactions/auto-categorize e POST /transfers/detect) — achado A12 da
// revisão de segurança.
//
// Os três acontecimentos existem por motivos diferentes e precisam de respostas
// diferentes:
//
//   - o PRAZO é o servidor cumprindo um limite que ele mesmo publicou
//     (transaction.PlanTimeout na fase de cálculo). É limite de TRABALHO, não
//     falha: 422 em `month`, como todo outro teto desta família — e não o 500
//     INTERNAL_ERROR que o `default:` produzia antes do A12, enquanto a rota
//     irmã POST /investments/detect, com o MESMO PlanTimeout, já respondia 422;
//   - a FALHA DE BANCO é do SERVIDOR, e continua sendo dele MESMO que o
//     contexto já estivesse morto quando ela apareceu: 500 com linha de ERROR;
//   - o CANCELAMENTO é a pessoa fechando a aba. É comportamento NORMAL de
//     cliente, repetível até 60×/h por casa: não pode virar linha de ERROR no
//     log, que é onde se procura sinal de segurança.
//
// ⚠️ As DUAS armadilhas que estes testes guardam, e é por elas que existem em
// PARES. Desde que o gormstore passou a somar o motivo do contexto ao erro do
// driver (platform/storage/ctxerr.go), QUALQUER falha de banco ocorrida com o
// contexto morto casa o erro de contexto:
//
//   - o ramo do PRAZO casa a SENTINELA transaction.ErrPlanTimeout, e NUNCA
//     `context.DeadlineExceeded`: casar o contexto responderia "este mês
//     demorou demais" para uma conexão derrubada, culpando o mês de quem pediu
//     e apagando a única linha de ERROR daquela falha;
//   - o ramo do CANCELAMENTO pergunta a `r.Context().Err()` — o fato do
//     ambiente da REQUISIÇÃO —, e NUNCA `errors.Is(err, context.Canceled)`:
//     perguntar ao erro classificaria a mesma falha de banco como "cliente
//     desistiu no meio", rebaixando para INFO um defeito do servidor. É o
//     achado B4 que a revisão abriu contra a outra entrega.
//
// Os pares de MUTAÇÃO, por rota:
//
//	traduzPrazoEm422           / falhaDeBancoComPrazoVencidoNaoViraTetoDoMes
//	clienteQueDesisteViraINFO  / falhaDeBancoCanceladaNaoViraDesistenciaDoCliente

// --- cenários mínimos ------------------------------------------------------

// casaParaCategorizar monta a casa mínima do auto-categorize: uma conta, uma
// categoria com palavra-chave e uma despesa do mês que casa com ela.
func casaParaCategorizar(t *testing.T, opts ...transaction.Option) *httpAmbiente {
	t.Helper()
	amb := novoHTTPAmbiente(t, opts...)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	amb.palavraDeCategoria(minhaCasa, "cat-mercado", "supermercado")
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "SUPERMERCADO EXTRA 123", 150_00, 3)
	return amb
}

// casaParaReprocessar monta a casa mínima do detect-transfers: duas contas com
// palavra-chave e um par de linhas que se espelham no mês.
func casaParaReprocessar(t *testing.T, opts ...transaction.Option) *httpAmbiente {
	t.Helper()
	amb := novoHTTPAmbiente(t, opts...)
	amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "C6", account.KindChecking)
	amb.palavraDeConta(minhaCasa, "acc-a", "enviado")
	amb.palavraDeConta(minhaCasa, "acc-b", "recebido")
	amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "Pix enviado - Bruno", 300_00, 25)
	amb.lancamento(minhaCasa, "acc-b", transaction.KindIncome, "Pix recebido de Bruno", 300_00, 25)
	return amb
}

// chamarComContexto é `chamar` com um contexto escolhido pelo teste — é assim
// que se encena a aba fechada no meio da execução, e o prazo já vencido na
// entrada. A identidade continua vindo do contexto, como o RequireAuth faz.
func (a *httpAmbiente) chamarComContexto(t *testing.T, ctx context.Context, casa, alvo, corpo string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, alvo, strings.NewReader(corpo))
	r = r.WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	if casa != "" {
		r = comIdentidade(r, casa)
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

// vencido devolve um contexto cujo prazo JÁ passou.
//
// O prazo vem do contexto de ENTRADA, e não de um `WithPlanTimeout` de 1 ns,
// porque este é o cenário de produção mais próximo (o cálculo herda a data mais
// apertada) e porque nada aqui depende de o timer do sistema disparar entre
// duas instruções. O PlanTimeout próprio é exercitado à parte, nos testes de
// serviço abaixo.
func vencido(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	t.Cleanup(cancel)
	return ctx
}

// cancelado devolve um contexto já cancelado — a aba fechada.
func cancelado(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	t.Cleanup(cancel)
	return ctx
}

// falhaDeDriverSobContexto é o erro que o gormstore entrega hoje quando o banco
// falha com o contexto já morto — montado pela função REAL, e não por uma
// imitação: se a forma do embrulho mudar, é aqui que se vê.
func falhaDeDriverSobContexto(t *testing.T, ctx context.Context, texto string) error {
	t.Helper()
	err := storage.ComErroDeContexto(ctx, errors.New(texto))
	// A premissa do cenário: o erro entregue à borda REALMENTE casa o motivo do
	// contexto. Sem isto o teste passaria por não exercitar nada.
	require.ErrorIs(t, err, ctx.Err(),
		"o embrulho do gormstore precisa fazer a falha de driver casar o motivo do contexto — é essa a armadilha")
	require.NotErrorIs(t, err, transaction.ErrPlanTimeout)
	return err
}

// --- (a) o prazo venceu: 422 em `month`, sem ERROR -------------------------

// O prazo chegando ao SERVIÇO já classificado: a sentinela do domínio na
// cadeia, com o motivo original preservado para o log.
//
// O prazo vem do contexto de ENTRADA já vencido, e não de um `WithPlanTimeout`
// de 1 ns: o segundo dependeria da resolução de `time.Now()`, que no Windows é
// de milissegundos — um prazo de 1 ns lido no mesmo tick do relógio ainda não
// venceu, e o teste passaria (ou não) por sorte de escalonamento. O efeito no
// código é o mesmo: `ctxDoPlano` herda a data do pai quando ela é mais
// apertada, e é essa a data que a parada voluntária enxerga. A SEGUNDA pergunta
// de conferirPrazo — a que fecha justamente essa janela de granularidade — é
// exercitada de forma determinística em conferirprazo_internal_test.go.
func TestAutoCategorizePrazoChegaEmbrulhadoNoServico(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	amb.palavraDeCategoria(minhaCasa, "cat-mercado", "supermercado")
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "SUPERMERCADO EXTRA 123", 150_00, 3)

	_, err := amb.svc.AutoCategorize(vencido(t), ator(minhaCasa),
		transaction.AutoCategorizeInput{Month: "2026-09", DryRun: false})
	require.Error(t, err)
	assert.ErrorIs(t, err, transaction.ErrPlanTimeout,
		"quem impôs o prazo é quem o nomeia: é a SENTINELA que distingue 422 de 500, nunca o erro de contexto")
	assert.ErrorIs(t, err, context.DeadlineExceeded,
		"o motivo original continua na cadeia — o log precisa dele")
	assert.Empty(t, amb.auditor.registros, "o prazo é do CÁLCULO: ele corre antes de qualquer escrita")
}

func TestDetectTransfersPrazoChegaEmbrulhadoNoServico(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "C6", account.KindChecking)
	amb.palavraDeConta(minhaCasa, "acc-a", "enviado")
	amb.palavraDeConta(minhaCasa, "acc-b", "recebido")
	amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "Pix enviado - Bruno", 300_00, 25)
	amb.lancamento(minhaCasa, "acc-b", transaction.KindIncome, "Pix recebido de Bruno", 300_00, 25)

	_, err := amb.svc.DetectTransfers(vencido(t), ator(minhaCasa),
		transaction.TransferDetectInput{Month: "2026-09", DryRun: false})
	require.Error(t, err)
	assert.ErrorIs(t, err, transaction.ErrPlanTimeout)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, amb.auditor.registros)
	assert.Zero(t, amb.repo.conversoes, "nada é convertido quando o cálculo não termina")
}

// O mesmo prazo, agora pelo handler: 422 VALIDATION_FAILED em `fields.month`,
// que é o que o contrato já declara (BusinessRuleRejected nas duas rotas) —
// nunca 500.
//
// A prévia (dryRun) também: ela gasta o MESMO cálculo da execução real, e é ela
// que a tela pede primeiro.
func TestAutoCategorizeHandlerTraduzPrazoEm422(t *testing.T) {
	t.Parallel()

	for _, dryRun := range []string{"true", "false"} {
		t.Run("dryRun="+dryRun, func(t *testing.T) {
			t.Parallel()

			amb := casaParaCategorizar(t)
			rec := amb.chamarComContexto(t, vencido(t), minhaCasa, "/transactions/auto-categorize",
				`{"month":"2026-09","dryRun":`+dryRun+`}`, amb.handler.AutoCategorize)

			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			assert.Contains(t, campos, "month", "o campo é `month`, como nos outros tetos desta família")
			assert.NotContains(t, campos["month"], "deadline", "o vocabulário do servidor não vaza para a tela")
			assert.NotContains(t, campos["month"], "context")

			assert.NotContains(t, amb.logs.String(), `"level":"ERROR"`,
				"limite de trabalho PREVISTO não é incidente: uma linha de ERROR aqui é ruído sob demanda do cliente")
			assert.Empty(t, amb.auditor.registros)
		})
	}
}

func TestDetectTransfersHandlerTraduzPrazoEm422(t *testing.T) {
	t.Parallel()

	for _, dryRun := range []string{"true", "false"} {
		t.Run("dryRun="+dryRun, func(t *testing.T) {
			t.Parallel()

			amb := casaParaReprocessar(t)
			rec := amb.chamarComContexto(t, vencido(t), minhaCasa, "/transfers/detect",
				`{"month":"2026-09","dryRun":`+dryRun+`}`, amb.handler.DetectTransfers)

			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			assert.Contains(t, campos, "month")
			assert.NotContains(t, campos["month"], "deadline")

			assert.NotContains(t, amb.logs.String(), `"level":"ERROR"`)
			assert.Empty(t, amb.auditor.registros)
			assert.Zero(t, amb.repo.conversoes)
		})
	}
}

// --- (b) o cliente desistiu: INFO com `reason`, sem ERROR ------------------

// Aba fechada no meio da execução: nenhuma linha de ERROR, e a razão continua
// registrada — só muda de NÍVEL. Antes do A12 isto era um 500 com ERROR, por
// requisição, repetível até 60×/h por casa.
func TestAutoCategorizeClienteQueDesisteNaoViraLinhaDeErro(t *testing.T) {
	t.Parallel()

	amb := casaParaCategorizar(t)
	rec := amb.chamarComContexto(t, cancelado(t), minhaCasa, "/transactions/auto-categorize",
		`{"month":"2026-09","dryRun":true}`, amb.handler.AutoCategorize)

	// A resposta existe e é o 500 genérico do enum FECHADO do contrato —
	// ninguém a lê, porque a conexão já foi embora, e inventar um status fora
	// do enum publicado seria pior do que escrever um que não chega a lugar
	// nenhum.
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.NotEqual(t, http.StatusUnprocessableEntity, rec.Code,
		"422 é uma afirmação sobre o MÊS, e ela seria FALSA sobre uma execução que a pessoa interrompeu")

	registro := amb.logs.String()
	assert.NotContains(t, registro, `"level":"ERROR"`, "cliente que desiste não é incidente do servidor")
	assert.Contains(t, registro, `"level":"INFO"`)
	assert.Contains(t, registro, "cliente desistiu no meio")
	assert.Contains(t, registro, `"reason"`,
		"a razão não some do log: a linha muda de nível, não de conteúdo")
	assert.NotContains(t, registro, "SUPERMERCADO EXTRA", "descrição de lançamento nunca vai para o log (S8)")
	assert.Empty(t, amb.auditor.registros)
}

func TestDetectTransfersClienteQueDesisteNaoViraLinhaDeErro(t *testing.T) {
	t.Parallel()

	amb := casaParaReprocessar(t)
	rec := amb.chamarComContexto(t, cancelado(t), minhaCasa, "/transfers/detect",
		`{"month":"2026-09","dryRun":true}`, amb.handler.DetectTransfers)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.NotEqual(t, http.StatusUnprocessableEntity, rec.Code)

	registro := amb.logs.String()
	assert.NotContains(t, registro, `"level":"ERROR"`)
	assert.Contains(t, registro, `"level":"INFO"`)
	assert.Contains(t, registro, "cliente desistiu no meio")
	assert.Contains(t, registro, `"reason"`)
	assert.NotContains(t, registro, "Pix enviado", "descrição de lançamento nunca vai para o log (S8)")
	assert.Empty(t, amb.auditor.registros)
}

// --- (c) falha de servidor GENUÍNA, cliente vivo: 500 com ERROR -----------

// A outra metade do primeiro par, e é ela que guarda o defeito: o banco FALHOU,
// e a falha é do SERVIDOR mesmo que o contexto já estivesse vencido quando ela
// apareceu.
//
// MUTAÇÃO: trocar `errors.Is(err, ErrPlanTimeout)` por
// `errors.Is(err, context.DeadlineExceeded)` no handler faz este teste virar
// 422 "este mês demorou demais", sem nenhuma linha de ERROR.
func TestAutoCategorizeFalhaDeBancoComPrazoVencidoNaoViraTetoDoMes(t *testing.T) {
	t.Parallel()

	amb := casaParaCategorizar(t)
	amb.repo.erroUncategorized = falhaDeDriverSobContexto(t, vencido(t), "database is locked")

	// A REQUISIÇÃO está viva: quem morreu foi o contexto de dentro da consulta.
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transactions/auto-categorize",
		`{"month":"2026-09","dryRun":true}`, amb.handler.AutoCategorize)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	codigo, _ := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeInternalError, codigo)
	assert.NotContains(t, rec.Body.String(), "demorou demais",
		"falha do servidor não pode ser reportada como limite do mês de quem pediu")
	assert.NotContains(t, rec.Body.String(), "database is locked",
		"o detalhe técnico fica no log, nunca na resposta")

	registro := amb.logs.String()
	assert.Contains(t, registro, `"level":"ERROR"`,
		"a falha do servidor precisa deixar a linha de ERROR: ela é o único registro dela")
	assert.NotContains(t, registro, "cliente desistiu no meio")
	assert.NotContains(t, registro, "SUPERMERCADO EXTRA")
}

func TestDetectTransfersFalhaDeBancoComPrazoVencidoNaoViraTetoDoMes(t *testing.T) {
	t.Parallel()

	amb := casaParaReprocessar(t)
	amb.repo.erroCandidatas = falhaDeDriverSobContexto(t, vencido(t), "database is locked")

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect",
		`{"month":"2026-09","dryRun":true}`, amb.handler.DetectTransfers)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	codigo, _ := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeInternalError, codigo)
	assert.NotContains(t, rec.Body.String(), "demorou demais")

	registro := amb.logs.String()
	assert.Contains(t, registro, `"level":"ERROR"`)
	assert.NotContains(t, registro, "cliente desistiu no meio")
	assert.NotContains(t, registro, "Pix enviado")
}

// A outra metade do SEGUNDO par — o achado B4 desta vez. O banco falhou com o
// contexto da CONSULTA cancelado, mas a REQUISIÇÃO está viva: ninguém desistiu
// de nada, e isto é um defeito do servidor.
//
// MUTAÇÃO: trocar `r.Context().Err() != nil` por
// `errors.Is(err, context.Canceled)` em `erroInterno` faz este teste virar uma
// linha de INFO "cliente desistiu no meio" — e o defeito some do log de ERROR,
// que é o único lugar onde ele seria visto.
func TestAutoCategorizeFalhaDeBancoCanceladaNaoViraDesistenciaDoCliente(t *testing.T) {
	t.Parallel()

	amb := casaParaCategorizar(t)
	amb.repo.erroUncategorized = falhaDeDriverSobContexto(t, cancelado(t), "driver: bad connection")

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transactions/auto-categorize",
		`{"month":"2026-09","dryRun":true}`, amb.handler.AutoCategorize)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	registro := amb.logs.String()
	assert.Contains(t, registro, `"level":"ERROR"`,
		"quem decide 'o cliente foi embora' é o contexto da REQUISIÇÃO, jamais o erro (achado B4)")
	assert.NotContains(t, registro, "cliente desistiu no meio")
	assert.NotContains(t, rec.Body.String(), "bad connection")
}

func TestDetectTransfersFalhaDeBancoCanceladaNaoViraDesistenciaDoCliente(t *testing.T) {
	t.Parallel()

	amb := casaParaReprocessar(t)
	amb.repo.erroCandidatas = falhaDeDriverSobContexto(t, cancelado(t), "driver: bad connection")

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect",
		`{"month":"2026-09","dryRun":true}`, amb.handler.DetectTransfers)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	registro := amb.logs.String()
	assert.Contains(t, registro, `"level":"ERROR"`)
	assert.NotContains(t, registro, "cliente desistiu no meio")
	assert.NotContains(t, rec.Body.String(), "bad connection")
}

// A rota de LEITURA continua sem prazo NENHUM, e é por isso que ela não pode
// responder 422 por erro de contexto.
//
// 422 em `month` é uma afirmação sobre o MÊS ("é grande demais para uma
// execução"), e ela só é verdadeira quando o servidor cumpriu um orçamento que
// ele mesmo publicou. GET /transactions não publica orçamento: um erro de
// contexto vindo dela é falha — 500 com ERROR —, e chamá-lo de teto do mês
// culparia a pessoa por algo que não é dela.
func TestListagemNaoInventaTetoDeMesPorErroDeContexto(t *testing.T) {
	t.Parallel()

	amb := casaParaCategorizar(t)
	amb.repo.erroList = falhaDeDriverSobContexto(t, vencido(t), "connection reset by peer")

	rec := amb.chamar(t, minhaCasa, http.MethodGet, "/transactions?month=2026-09", "", amb.handler.List)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeInternalError, codigo)
	assert.Empty(t, campos, "não há campo do pedido para a pessoa corrigir")
	assert.Contains(t, amb.logs.String(), `"level":"ERROR"`)
}
