package investment_test

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

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/storage"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Prazo, FALHA DE BANCO e cancelamento na borda das rotas de investimento.
//
// Os três existem por motivos diferentes e precisam de respostas diferentes:
//
//   - o PRAZO é o servidor cumprindo um limite que ele mesmo publicou
//     (transaction.PlanTimeout na fase de cálculo). É limite de TRABALHO, não
//     falha: 422 em `month`, com a mesma orientação dos outros tetos desta
//     família — e não o 500 INTERNAL_ERROR que um `default:` produziria;
//   - a FALHA DE BANCO é do servidor, e continua sendo dele MESMO que o
//     contexto já estivesse morto quando ela apareceu: 500 com linha de ERROR;
//   - o CANCELAMENTO é a pessoa fechando a aba. É comportamento normal de
//     cliente: não pode virar linha de ERROR no log, que é onde se procura
//     sinal de segurança.
//
// ⚠️ O segundo item é o achado N1 desta revisão, e ele tem DOIS gêmeos no
// mesmo `switch`, pelo mesmo motivo. Desde que o gormstore passou a somar o
// motivo do contexto ao erro do driver (platform/storage/ctxerr.go), QUALQUER
// falha de banco ocorrida com o contexto morto casa o erro de contexto:
//
//   - o ramo do PRAZO casava `context.DeadlineExceeded` e respondia "a detecção
//     deste mês demorou demais" para uma conexão derrubada, culpando o mês da
//     pessoa e apagando a única linha de ERROR daquela falha. Hoje ele casa a
//     SENTINELA investment.ErrPlanTimeout;
//   - o ramo do CANCELAMENTO casava `errors.Is(err, context.Canceled)` e
//     classificava a mesma falha como "cliente desistiu no meio" — e era PIOR,
//     porque aquela linha de INFO nem levava `reason`: o registro do defeito
//     não mudava de nível, SUMIA. Hoje ele casa `r.Context().Err()`, que é o
//     fato do ambiente da requisição, e a linha leva `reason`.
//
// A regra que vale para os dois: nenhuma falha do servidor é reportada como
// ação do cliente, e nenhuma some do log.
//
// Os dois pares abaixo são a verificação por MUTAÇÃO disso:
//
//	TestHandlerTraduzPrazoEm422       / TestFalhaDeBancoComPrazoVencidoNaoViraTetoDoMes
//	TestClienteQueDesisteNaoViraLinhaDeErro / TestFalhaDeBancoNaoViraDesistenciaDoCliente

// postComContexto é `post` com um contexto escolhido pelo teste — é assim que
// se encena a aba fechada no meio da execução.
func (h *httpCenario) postComContexto(t *testing.T, ctx context.Context, casa, corpo string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/investments/detect", strings.NewReader(corpo))
	r = comSessao(r.WithContext(ctx), casa)
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.handler.Detect(rec, r)
	return rec
}

// casaHTTPComCDB monta a casa mínima das rotas: uma categoria de investimento
// com palavra-chave e um lançamento do mês que casa com ela.
func casaHTTPComCDB(t *testing.T) *httpCenario {
	t.Helper()
	h := montarHTTP(t)
	h.categorias.juntar(casaA, "cat-cdb", "Renda fixa", category.KindInvestment, "cdb")
	h.contas.juntar(casaA, "acc-1", "Nubank")
	h.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))
	return h
}

// O prazo estourando na fase de CÁLCULO, pelo caminho real e sem erro
// injetado: a conferência do laço de pontuação (a cada 64 linhas, e a linha 0 é
// uma delas) encontra o contexto morto e devolve o motivo embrulhado.
//
// O prazo vem do contexto de ENTRADA já vencido, e não de um `WithPlanTimeout`
// de 1 ns, porque o segundo dependeria de o timer do sistema disparar entre
// duas instruções — teste que passa por sorte de escalonamento não é teste. O
// efeito no código é o mesmo: `planejarComPrazo` herda a data do pai quando ela
// é mais apertada, e é essa a data que o laço enxerga.
func TestPrazoDaFaseDeCalculoChegaEmbrulhadoNaBorda(t *testing.T) {
	t.Parallel()

	c := montarCom(nil, investment.WithPlanTimeout(time.Minute))
	c.categorias.juntar(casaA, "cat-cdb", "Renda fixa", category.KindInvestment, "cdb")
	c.contas.juntar(casaA, "acc-1", "Nubank")
	c.ledger.juntar(lanc(casaA, uuidDe(1), transaction.KindExpense, "CDB 15 DIAS", 200000, 1, "2026-09", ""))

	vencido, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := c.svc.Detect(vencido, ator(casaA), investment.DetectInput{Month: "2026-09"})
	require.Error(t, err)
	assert.ErrorIs(t, err, investment.ErrPlanTimeout,
		"quem impôs o prazo é quem o nomeia: é a SENTINELA que distingue 422 de 500, nunca o erro de contexto")
	assert.ErrorIs(t, err, context.DeadlineExceeded,
		"o motivo original continua na cadeia — o log precisa dele")

	assert.Zero(t, c.tx.chamadas, "o prazo é do CÁLCULO: ele corre antes de qualquer escrita")
	assert.Zero(t, c.ledger.chamadasSetNull)
	assert.Empty(t, c.auditoria.entradas)
}

// O mesmo prazo, agora pelo handler: 422 VALIDATION_FAILED em `fields.month`,
// que é o que o contrato declara — nunca 500.
//
// O prazo chega pelo CAMINHO REAL: contexto de entrada já vencido, e a parada
// voluntária de abertura de `planejar` encontrando o relógio no vermelho. Nada
// é injetado na camada de dados — injetar `context.DeadlineExceeded` num
// repositório seria encenar uma FALHA DE BANCO e chamá-la de prazo, que é
// exatamente a confusão que o achado N1 desfez.
func TestHandlerTraduzPrazoEm422(t *testing.T) {
	t.Parallel()

	h := casaHTTPComCDB(t)

	vencido, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	rec := h.postComContexto(t, vencido, casaA, `{"month":"2026-09","dryRun":false}`)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	codigo, campos := erroDoCorpo(t, rec)
	assert.Equal(t, httpserver.CodeValidationFailed, codigo)
	assert.Contains(t, campos, "month", "o campo é `month`, como nos outros tetos desta família")
	assert.NotContains(t, campos["month"], "deadline", "o vocabulário do servidor não vaza para a tela")

	assert.NotContains(t, h.logs.String(), `"level":"ERROR"`,
		"limite de trabalho previsto não é incidente")

	assert.Zero(t, h.tx.chamadas, "o prazo é do CÁLCULO: ele corre antes de qualquer escrita")
	assert.Empty(t, h.auditoria.entradas)
}

// A OUTRA metade do par, e é ela que guarda o defeito: o banco FALHOU, e a
// falha é do SERVIDOR mesmo que o contexto já estivesse vencido quando ela
// apareceu.
//
// Cenário de produção: conexão derrubada ou pool esgotado no meio da leitura do
// mês, com o prazo da fase de cálculo já passado. O erro que chega à borda casa
// `context.DeadlineExceeded` — não porque houve prazo, mas porque o gormstore
// soma o motivo do contexto ao erro do driver.
//
// MUTAÇÃO: trocar `errors.Is(err, ErrPlanTimeout)` de volta por
// `errors.Is(err, context.DeadlineExceeded)` no handler faz este teste virar
// 422 "A detecção deste mês demorou demais", sem nenhuma linha de ERROR — que é
// precisamente o defeito N1.
func TestFalhaDeBancoComPrazoVencidoNaoViraTetoDoMes(t *testing.T) {
	t.Parallel()

	h := casaHTTPComCDB(t)

	// Erro de DRIVER genérico, sem relação nenhuma com prazo, embrulhado
	// exatamente como o gormstore o entrega hoje — pela função REAL, e não por
	// uma imitação: se a forma do embrulho mudar, é aqui que se vê.
	falhaDoDriver := errors.New("database is locked")
	vencido, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	h.ledger.erroDoMes = storage.ComErroDeContexto(vencido, falhaDoDriver)

	// A premissa do cenário: o erro entregue à borda REALMENTE casa o motivo do
	// contexto. Sem isto o teste passaria por não exercitar nada.
	require.ErrorIs(t, h.ledger.erroDoMes, context.DeadlineExceeded,
		"o embrulho do gormstore precisa fazer a falha de driver casar DeadlineExceeded — é essa a armadilha")
	require.NotErrorIs(t, h.ledger.erroDoMes, investment.ErrPlanTimeout)

	// A REQUISIÇÃO está viva: quem morreu foi o contexto de dentro da consulta.
	rec := h.post(t, casaA, `{"month":"2026-09","dryRun":false}`)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	codigo, _ := erroDoCorpo(t, rec)
	assert.Equal(t, httpserver.CodeInternalError, codigo)
	assert.NotContains(t, rec.Body.String(), "demorou demais",
		"falha do servidor não pode ser reportada como limite do mês de quem pediu")

	registro := h.logs.String()
	assert.Contains(t, registro, `"level":"ERROR"`,
		"a falha do servidor precisa deixar a linha de ERROR: ela é o único registro dela")
	assert.NotContains(t, rec.Body.String(), "database is locked",
		"o detalhe técnico fica no log, nunca na resposta")
	assert.NotContains(t, registro, "CDB 15 DIAS", "descrição de lançamento nunca vai para o log (S8)")
}

// A rota de LEITURA não impõe prazo NENHUM, e é por isso que ela não pode
// responder 422 por erro de contexto.
//
// 422 em `month` é uma afirmação sobre o MÊS ("é grande demais para uma
// execução"), e ela só é verdadeira quando o servidor cumpriu um orçamento que
// ele mesmo publicou. O Overview não publica orçamento: ele faz duas consultas
// limitadas (≤ 24 linhas da agregação e uma página). Um erro de contexto vindo
// daqui é falha — 500 com ERROR —, e chamá-lo de teto do mês culparia a pessoa
// por algo que não é dela e apagaria o registro do incidente.
func TestPrazoNoOverviewNaoInventaTetoDeMes(t *testing.T) {
	t.Parallel()

	h := casaHTTPComCDB(t)

	falhaDoDriver := errors.New("connection reset by peer")
	vencido, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	h.ledger.erroSum = storage.ComErroDeContexto(vencido, falhaDoDriver)
	require.ErrorIs(t, h.ledger.erroSum, context.DeadlineExceeded)

	rec := h.get(t, casaA, "/api/v1/investments?month=2026-09")
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())

	codigo, campos := erroDoCorpo(t, rec)
	assert.Equal(t, httpserver.CodeInternalError, codigo)
	assert.Empty(t, campos, "não há campo do pedido para a pessoa corrigir")
	assert.Contains(t, h.logs.String(), `"level":"ERROR"`)
}

// Aba fechada no meio da execução: nenhuma linha de ERROR. O log de erro é
// sinal de segurança, e enchê-lo de comportamento normal de cliente é o mesmo
// que apagá-lo.
func TestClienteQueDesisteNaoViraLinhaDeErro(t *testing.T) {
	t.Parallel()

	h := casaHTTPComCDB(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.ledger.erroDoMes = context.Canceled

	rec := h.postComContexto(t, ctx, casaA, `{"month":"2026-09","dryRun":false}`)

	// A resposta existe e é o 500 genérico do enum FECHADO do contrato —
	// ninguém a lê, porque a conexão já foi embora, e inventar um status fora
	// do enum publicado seria pior do que escrever um que não chega a lugar
	// nenhum.
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	registro := h.logs.String()
	assert.NotContains(t, registro, `"level":"ERROR"`, "cliente que desiste não é incidente do servidor")
	assert.Contains(t, registro, `"level":"INFO"`)
	assert.Contains(t, registro, "cliente desistiu no meio")
	assert.Contains(t, registro, `"reason"`,
		"a desistência muda o NÍVEL do registro, nunca pode fazer o registro sumir")
	assert.NotContains(t, registro, "CDB 15 DIAS", "descrição de lançamento nunca vai para o log (S8)")
}

// O cliente foi embora E o banco falhou na MESMA janela — o cenário que o
// revisor da outra frente levantou.
//
// Aqui a classificação de desistência está CERTA (a pessoa fechou a aba de
// verdade), então a linha é INFO. O que não pode acontecer é o registro do
// defeito do servidor SUMIR junto: o `reason` carrega a falha do driver, e é o
// único lugar em que ela existe.
//
// MUTAÇÃO: apagar `slog.String("reason", err.Error())` da linha de INFO derruba
// este teste. Era assim que o ramo estava antes desta rodada.
func TestDesistenciaDoClienteNaoApagaAFalhaDoBancoDoLog(t *testing.T) {
	t.Parallel()

	h := casaHTTPComCDB(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// A aba fecha DENTRO da transação, imediatamente antes da escrita — e o
	// UPDATE encontra o banco quebrado. O gancho `antes` é a janela real, não
	// sorte de escalonamento.
	falhaDoDriver := errors.New("database disk image is malformed")
	h.tx.antes = func() {
		cancel()
		h.ledger.erroSetNull = storage.ComErroDeContexto(ctx, falhaDoDriver)
	}

	rec := h.postComContexto(t, ctx, casaA, `{"month":"2026-09","dryRun":false}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	registro := h.logs.String()
	assert.Contains(t, registro, "cliente desistiu no meio", "a pessoa realmente desistiu")
	assert.Contains(t, registro, `"level":"INFO"`)
	assert.Contains(t, registro, "malformed",
		"a falha do banco tem de sobreviver no `reason`: sem ela o defeito não existe em lugar nenhum")

	// E o detalhe técnico continua fora da resposta.
	assert.NotContains(t, rec.Body.String(), "malformed")
	assert.NotContains(t, registro, "CDB 15 DIAS", "descrição de lançamento nunca vai para o log (S8)")
}

// A outra metade do par, e é ela que guarda o defeito: a REQUISIÇÃO está viva,
// e só o erro é que passou perto de um contexto cancelado.
//
// Cenário: o contexto de uma consulta interna é cancelado e o driver falha; o
// gormstore soma o motivo ao erro, e `errors.Is(err, context.Canceled)` fica
// verdadeiro para uma falha que não tem nada a ver com cliente nenhum. Ninguém
// desistiu — a pessoa está do outro lado esperando a resposta.
//
// MUTAÇÃO: trocar `errors.Is(r.Context().Err(), context.Canceled)` de volta por
// `errors.Is(err, context.Canceled)` no handler faz esta falha de servidor virar
// INFO "cliente desistiu no meio", sem nenhuma linha de ERROR — que é
// precisamente o defeito.
func TestFalhaDeBancoNaoViraDesistenciaDoCliente(t *testing.T) {
	t.Parallel()

	h := casaHTTPComCDB(t)

	cancelado, cancel := context.WithCancel(context.Background())
	cancel()
	falhaDoDriver := errors.New("database disk image is malformed")
	h.ledger.erroDoMes = storage.ComErroDeContexto(cancelado, falhaDoDriver)

	// A premissa do cenário: o erro entregue à borda REALMENTE casa
	// context.Canceled. Sem isto o teste passaria por não exercitar nada.
	require.ErrorIs(t, h.ledger.erroDoMes, context.Canceled,
		"o embrulho do gormstore precisa fazer a falha de driver casar Canceled — é essa a armadilha")

	// A REQUISIÇÃO está viva: `h.post` usa o contexto de fundo do httptest.
	rec := h.post(t, casaA, `{"month":"2026-09","dryRun":false}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	registro := h.logs.String()
	assert.NotContains(t, registro, "cliente desistiu no meio",
		"ninguém desistiu: falha do servidor não pode ser reportada como ação do cliente")
	assert.Contains(t, registro, `"level":"ERROR"`,
		"a falha do servidor precisa deixar a linha de ERROR: ela é o único registro dela")
	assert.Contains(t, registro, "malformed")
	assert.NotContains(t, rec.Body.String(), "malformed",
		"o detalhe técnico fica no log, nunca na resposta")
}

// O cancelamento também não pode ser confundido com o prazo: 422 é uma
// afirmação sobre o MÊS ("é grande demais"), e ela seria falsa sobre uma
// execução que a pessoa interrompeu.
func TestCancelamentoNaoViraTetoDeMes(t *testing.T) {
	t.Parallel()

	h := casaHTTPComCDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.ledger.erroDoMes = context.Canceled

	rec := h.postComContexto(t, ctx, casaA, `{"month":"2026-09","dryRun":false}`)
	assert.NotEqual(t, http.StatusUnprocessableEntity, rec.Code)
}
