package transaction_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// O NÍVEL do log dos 500 de lançamento — achados B-A12.1 e B-A12.2 da revisão.
//
// A resposta ao cliente é sempre a MESMA (500 genérico do enum fechado do
// contrato); o que muda é o que fica registrado, e é só isso que estes testes
// olham. Duas regras, e elas são independentes:
//
//   - B-A12.2 — quem rebaixa para INFO é a CLASSE da falha, não só o estado da
//     conexão. Ruído de cliente (falha de execução com o socket já fechado)
//     rebaixa; INVARIANTE VIOLADA — taxonomia da casa estourada, resumo fora da
//     faixa, filtro vazio por defeito de ligação — não rebaixa nunca, porque a
//     afirmação "os dados desta casa contradizem o domínio" não deixa de ser
//     verdadeira porque a aba fechou. Sem isto, bastaria provocar a condição e
//     fechar a conexão em seguida para a quebra sair como INFO, e todo alerta
//     apoiado em `level=ERROR` ou na mensagem "falha em lançamento" perderia o
//     evento;
//   - B-A12.1 — o motivo que rebaixa é `context.Canceled`, e SÓ ele. `!= nil`
//     também pegaria `DeadlineExceeded`, e no dia em que existir um prazo por
//     requisição um estouro do orçamento do SERVIDOR seria gravado como INFO
//     "cliente desistiu no meio": uma afirmação falsa sobre o cliente, e um
//     incidente fora do log de ERROR.
//
// Os pares de MUTAÇÃO:
//
//	invarianteVioladaComClienteForaContinuaERROR / falhaDeExecucaoComClienteForaContinuaINFO
//	prazoDoServidorNaoViraDesistenciaDoCliente   / falhaDeExecucaoComClienteForaContinuaINFO

// chamarComContextoEMetodo é `chamar` com método E contexto escolhidos pelo
// teste — o `chamarComContexto` vizinho só monta POST, e o caminho mais curto
// até os erros de invariante é o GET /transactions.
//
// A identidade continua vindo do contexto, como o RequireAuth faz: a casa nunca
// vem da URL nem do corpo.
func (a *httpAmbiente) chamarComContextoEMetodo(t *testing.T, ctx context.Context, casa, metodo, alvo, corpo string, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(metodo, alvo, strings.NewReader(corpo))
	r = r.WithContext(ctx)
	if corpo != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if casa != "" {
		r = comIdentidade(r, casa)
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

// casaComTaxonomiaEstourada monta a casa cujo número de categorias passou do
// teto do domínio — a condição de transaction.ErrTooManyCategories (ADR-029
// j.2). É a mesma receita de investimentos_handler_test.go.
func casaComTaxonomiaEstourada(t *testing.T) *httpAmbiente {
	t.Helper()
	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	for i := 0; i <= category.MaxPerHousehold; i++ {
		amb.categoria(minhaCasa, fmt.Sprintf("cat-%03d", i), "Aporte", category.KindInvestment)
	}
	return amb
}

// --- (a) invariante violada com o cliente fora: continua ERROR -------------

// B-A12.2. O cenário é o do revisor: um membro autenticado provoca a condição e
// fecha o socket logo depois de mandar o pedido. Quando `erroInterno` roda,
// `r.Context().Err()` já é não-nil — e o rebaixamento para INFO, que é certo
// para falha de execução, apagaria daqui uma quebra de invariante.
//
// MUTAÇÃO: tirar `&& classe == falhaDeExecucao` da condição de `erroInterno`
// (ou passar `falhaDeExecucao` no `case` dos fail-closed) faz as duas
// subvariantes virarem INFO "cliente desistiu no meio" — e o sinal de corrupção
// some do log de ERROR, sob controle de quem provocou a condição.
func TestInvarianteVioladaComClienteForaContinuaERROR(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome    string
		montar  func(t *testing.T) *httpAmbiente
		noLog   string // a contagem que o erro embrulhou
		foraDoL string // o que NÃO pode aparecer no log
	}{
		{
			nome:    "taxonomia estourada (ErrTooManyCategories)",
			montar:  casaComTaxonomiaEstourada,
			noLog:   "categorias marcadas",
			foraDoL: "cat-001",
		},
		{
			nome: "resumo fora da faixa publicável (errResumoInconsistente)",
			montar: func(t *testing.T) *httpAmbiente {
				t.Helper()
				amb := novoHTTPAmbiente(t)
				amb.repo.resumoForcado = &transaction.Summary{
					IncomeCents: 10, ExpenseCents: -1, InvestedCents: 2_000_00, Count: 7, Uncategorized: 2,
				}
				return amb
			},
			noLog:   "lancamentos=7",
			foraDoL: "200000",
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()

			amb := c.montar(t)
			rec := amb.chamarComContextoEMetodo(t, cancelado(t), minhaCasa,
				http.MethodGet, "/transactions?month=2026-09", "", amb.handler.List)

			// O CONTRATO não muda: 500 genérico, sem campos, como já era.
			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeInternalError, codigo)
			assert.Empty(t, campos)

			registro := amb.logs.String()
			assert.Contains(t, registro, `"level":"ERROR"`,
				"quebra de invariante é sinal de CORRUPÇÃO: não deixa de ser verdade porque o socket fechou (B-A12.2)")
			assert.Contains(t, registro, "falha em lançamento",
				"o alerta se apoia nesta mensagem — ela não pode depender de o cliente ter esperado a resposta")
			assert.NotContains(t, registro, "cliente desistiu no meio",
				"não foi o cliente que causou isto; ele só fechou a aba")

			// A informação que o ramo de INFO carregava no texto não se perde:
			// ninguém leu esta resposta, e a linha de ERROR diz isso.
			assert.Contains(t, registro, `"client_gone":true`)

			// E o conteúdo do log continua sendo só contagem (S8).
			assert.Contains(t, registro, c.noLog)
			assert.NotContains(t, registro, c.foraDoL)
		})
	}
}

// --- (b) falha de execução com o cliente fora: continua INFO ---------------

// O outro lado da regra, e o que impede a correção do B-A12.2 de virar "tudo é
// ERROR": aba fechada no meio de uma falha comum continua sendo ruído de
// cliente, em INFO, com o `reason` preservado.
//
// MUTAÇÃO: passar `invarianteViolada` no `default` do fail — ou remover a
// classe e logar sempre ERROR — faz este teste falhar, e o log volta a ganhar
// uma linha de ERROR por aba fechada, repetível sob demanda do cliente.
func TestFalhaDeExecucaoComClienteForaContinuaINFO(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.repo.erroList = errors.New("driver: bad connection")

	rec := amb.chamarComContextoEMetodo(t, cancelado(t), minhaCasa,
		http.MethodGet, "/transactions?month=2026-09", "", amb.handler.List)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.NotContains(t, rec.Body.String(), "bad connection", "detalhe técnico fica no log, nunca na resposta")

	registro := amb.logs.String()
	assert.Contains(t, registro, `"level":"INFO"`)
	assert.Contains(t, registro, "cliente desistiu no meio")
	assert.Contains(t, registro, `"reason"`,
		"a razão não some do log: a linha muda de nível, não de conteúdo")
	assert.NotContains(t, registro, `"level":"ERROR"`,
		"uma linha de ERROR por aba fechada polui o log que existe para mostrar sinal de segurança")
}

// --- (c) falha de execução com o cliente VIVO: ERROR -----------------------

// A requisição viva nunca entra no ramo de INFO — e a linha de ERROR diz, em
// campo, que o cliente estava lá para ler a resposta.
func TestFalhaDeExecucaoComClienteVivoEhERROR(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.repo.erroList = errors.New("driver: bad connection")

	rec := amb.chamar(t, minhaCasa, http.MethodGet, "/transactions?month=2026-09", "", amb.handler.List)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	registro := amb.logs.String()
	assert.Contains(t, registro, `"level":"ERROR"`)
	assert.Contains(t, registro, "falha em lançamento")
	assert.Contains(t, registro, `"client_gone":false`)
	assert.NotContains(t, registro, "cliente desistiu no meio")
}

// --- (d) só `Canceled` rebaixa -------------------------------------------

// B-A12.1. O contexto da REQUISIÇÃO morto por PRAZO, e não por cancelamento:
// hoje o net/http não põe prazo em `r.Context()`, então isto só acontece se
// alguém puser — que é exatamente a hipótese prevista na invariante anotada em
// `conferirPrazo` (autocategorize.go) e repetida em `erroInterno`.
//
// Quando esse dia chegar, o estouro é do orçamento do SERVIDOR: um incidente
// real. Gravá-lo como "cliente desistiu no meio" seria uma afirmação FALSA
// sobre o cliente — e, pior, em INFO.
//
// MUTAÇÃO: voltar a condição para `r.Context().Err() != nil` faz este teste
// virar uma linha de INFO "cliente desistiu no meio", sem nenhum ERROR.
func TestPrazoDoServidorNaoViraDesistenciaDoCliente(t *testing.T) {
	t.Parallel()

	// A premissa do cenário, escrita: o contexto está morto por DEADLINE, e o
	// motivo NÃO é cancelamento. Sem isto o teste passaria por não exercitar
	// nada.
	ctx := vencido(t)
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	require.NotErrorIs(t, ctx.Err(), context.Canceled)

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.repo.erroList = errors.New("orçamento da requisição estourou")

	rec := amb.chamarComContextoEMetodo(t, ctx, minhaCasa,
		http.MethodGet, "/transactions?month=2026-09", "", amb.handler.List)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	registro := amb.logs.String()
	assert.Contains(t, registro, `"level":"ERROR"`,
		"prazo do servidor é incidente do servidor: só `context.Canceled` rebaixa (B-A12.1)")
	assert.NotContains(t, registro, "cliente desistiu no meio",
		"dizer que o cliente desistiu quando foi o orçamento do servidor é afirmação falsa sobre o cliente")
	assert.Contains(t, registro, `"client_gone":false`,
		"o cliente NÃO foi embora — quem morreu foi o prazo")
}

// E a mesma regra valendo para o conjunto fail-closed sob prazo: invariante
// violada é ERROR por qualquer um dos dois motivos de contexto.
func TestInvarianteVioladaSobPrazoContinuaERROR(t *testing.T) {
	t.Parallel()

	amb := casaComTaxonomiaEstourada(t)
	rec := amb.chamarComContextoEMetodo(t, vencido(t), minhaCasa,
		http.MethodGet, "/transactions?month=2026-09", "", amb.handler.List)

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	registro := amb.logs.String()
	assert.Contains(t, registro, `"level":"ERROR"`)
	assert.NotContains(t, registro, "cliente desistiu no meio")
	assert.NotContains(t, registro, "cat-001")
}
