package transaction_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Achados A1 e A2 da revisão de segurança da E2c, do lado do serviço:
//
//   - o orçamento de trabalho do casamento por palavra-chave estourado vira
//     422 e NUNCA execução parcial (A1);
//   - o cálculo acontece FORA da transação, e só os UPDATE ficam dentro (A2).

// ---------------------------------------------------------------------------
// A1 — orçamento de trabalho
// ---------------------------------------------------------------------------

// servicoComOrcamento monta um serviço igual ao dos outros testes, mas com o
// orçamento de trabalho do classificador apertado — é o que permite exercitar
// o caminho do estouro sem montar 5.000 palavras-chave de verdade. O teto real
// (textmatch.MaxMatchWork) é medido em internal/textmatch/perf_test.go.
func servicoComOrcamento(t *testing.T, amb *ambiente, work int64) *transaction.Service {
	t.Helper()
	loader := classify.NewLoader(amb.categorias, amb.contas, classify.WithWorkBudget(work))
	return transaction.NewService(amb.repo, amb.contas, amb.categorias, amb.faturas, txDireto{}, loader,
		transaction.WithIDs(amb.repo.proximoID),
		transaction.WithClock(func() time.Time { return agora }),
		transaction.WithAudit(amb.auditor),
	)
}

// A prévia estourada é ErrKeywordMatchTooCostly — não um resultado com menos
// sugestões. A diferença importa: "categorizei 40 das 300, e não digo quais
// ficaram de fora" é pior do que não categorizar.
func TestAutoCategorizePreviaComOrcamentoEstouradoRecusa(t *testing.T) {
	t.Parallel()

	amb, _ := cenarioDeCategorizacao(t)
	svc := servicoComOrcamento(t, amb, 1)

	_, err := svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{
		Month: "2026-09", DryRun: true,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, transaction.ErrKeywordMatchTooCostly)
	assert.ErrorIs(t, err, textmatch.ErrWorkBudgetExceeded, "a causa original continua no encadeamento")
	assert.True(t, transaction.IsValidationError(err), "é 422, não 500")
}

// A execução real estourada não escreve NADA — nem as linhas que já tinham
// casado antes de o orçamento acabar, nem a entrada de auditoria.
func TestAutoCategorizeRealComOrcamentoEstouradoNaoEscreveNada(t *testing.T) {
	t.Parallel()

	amb, linhas := cenarioDeCategorizacao(t)
	svc := servicoComOrcamento(t, amb, 1)

	_, err := svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{
		Month: "2026-09", DryRun: false,
	})
	require.ErrorIs(t, err, transaction.ErrKeywordMatchTooCostly)

	lida, err := amb.repo.ByID(t.Context(), minhaCasa, linhas["casa"].ID)
	require.NoError(t, err)
	assert.Nil(t, lida.CategoryID, "nenhuma linha pode ter sido categorizada")
	assert.Empty(t, amb.auditor.registros, "execução recusada não deixa rastro de escrita")
}

// O mesmo orçamento cobre POST /transfers/detect: é o MESMO conjunto de
// matchers, carregado pelo mesmo classify.Load, e por isso o teto vale para as
// duas rotas de uma vez (nota do revisor sobre a §13).
func TestDetectTransfersComOrcamentoEstouradoRecusa(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Nubank", account.KindChecking)
	amb.conta(minhaCasa, "acc-2", "C6", account.KindChecking)
	amb.palavraDeConta(minhaCasa, "acc-2", "nubank")
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Pix enviado NUBANK", 100_00, 3)
	amb.lancamento(minhaCasa, "acc-2", transaction.KindIncome, "Pix recebido NUBANK", 100_00, 3)

	svc := servicoComOrcamento(t, amb, 1)
	_, err := svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{
		Month: "2026-09", DryRun: true,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, transaction.ErrKeywordMatchTooCostly)
	assert.ErrorIs(t, err, textmatch.ErrWorkBudgetExceeded)
}

// Na borda: 422 VALIDATION_FAILED no campo `month`, sem contagem, sem palavra
// e sem descrição na mensagem.
func TestAutoCategorizeHandlerOrcamentoEstouradoResponde422(t *testing.T) {
	t.Parallel()

	amb, _ := cenarioDeCategorizacao(t)
	logs := &bytes.Buffer{}
	lg := logging.New(logs, logging.Options{Level: "debug", Format: "json"})
	h := transaction.NewHandler(servicoComOrcamento(t, amb, 1), lg, 0)
	ambHTTP := &httpAmbiente{ambiente: amb, handler: h, logs: logs}

	rec := ambHTTP.chamar(t, minhaCasa, http.MethodPost, "/transactions/auto-categorize",
		`{"month":"2026-09","dryRun":true}`, h.AutoCategorize)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, "VALIDATION_FAILED", codigo)
	require.Contains(t, campos, "month")
	assert.NotContains(t, campos["month"], "supermercado", "a mensagem não cita palavra-chave")
	assert.NotContains(t, rec.Body.String(), "SUPERMERCADO", "a resposta não cita descrição")
	assert.NotContains(t, logs.String(), "supermercado", "o log não cita palavra-chave")
}

// ---------------------------------------------------------------------------
// A1 — prazo próprio da rota
// ---------------------------------------------------------------------------

// A rota tem prazo PRÓPRIO (PlanTimeout), porque o WriteTimeout do servidor
// não cancela esta goroutine.
//
// A asserção é DETERMINÍSTICA de propósito: em vez de cronometrar (um gate que
// falha por carga da máquina treina o time a ignorar vermelho), o teste olha o
// contexto que chega ao repositório e exige que ele tenha prazo — e que o
// prazo cubra o CÁLCULO, não a escrita.
func TestAutoCategorizeImpoePrazoProprioAoCalculoENaoAEscrita(t *testing.T) {
	t.Parallel()

	amb, _ := cenarioDeCategorizacao(t)
	tx := &txEspiao{}
	espiao := &repoEspiao{Repository: amb.repo, tx: tx}
	svc := transaction.NewService(espiao, amb.contas, amb.categorias, amb.faturas, tx,
		classify.NewLoader(amb.categorias, amb.contas),
		transaction.WithIDs(amb.repo.proximoID),
		transaction.WithClock(func() time.Time { return agora }),
		transaction.WithAudit(amb.auditor),
	)

	_, err := svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{
		Month: "2026-09", DryRun: false,
	})
	require.NoError(t, err)

	// O CÁLCULO corre sob prazo próprio.
	prazo, temPrazo := espiao.ctxDaLeitura.Deadline()
	require.True(t, temPrazo, "a fase de cálculo precisa de prazo próprio (PlanTimeout)")
	assert.LessOrEqual(t, time.Until(prazo), transaction.PlanTimeout)

	// A ESCRITA não herda esse prazo: ela roda no contexto do chamador, e um
	// UPDATE em lote não pode ser cortado no meio por um relógio de cálculo.
	_, escritaTemPrazo := espiao.ctxDaEscrita.Deadline()
	assert.False(t, escritaTemPrazo, "o prazo é do cálculo, não da transação")
}

// Cliente desistiu (ou o prazo venceu): o cálculo para e NADA é escrito. O
// contexto já cancelado torna o caso determinístico — sem depender de
// granularidade de timer, que no Windows é de milissegundos.
func TestAutoCategorizeParaQuandoOContextoMorre(t *testing.T) {
	t.Parallel()

	amb, linhas := cenarioDeCategorizacao(t)
	ctx, cancelar := context.WithCancel(t.Context())
	cancelar()

	_, err := amb.svc.AutoCategorize(ctx, ator(minhaCasa), transaction.AutoCategorizeInput{
		Month: "2026-09", DryRun: false,
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	lida, err := amb.repo.ByID(t.Context(), minhaCasa, linhas["casa"].ID)
	require.NoError(t, err)
	assert.Nil(t, lida.CategoryID, "contexto morto não grava nada")
	assert.Empty(t, amb.auditor.registros)
}

// O prazo padrão morre antes do WriteTimeout do http.Server (30 s), que é o
// motivo de ele existir.
func TestPrazoDoPlanoMorreAntesDoWriteTimeout(t *testing.T) {
	t.Parallel()
	assert.Less(t, transaction.PlanTimeout, 30*time.Second)
	assert.Positive(t, transaction.PlanTimeout)
}

// ---------------------------------------------------------------------------
// A2 — o cálculo sai de dentro da transação
// ---------------------------------------------------------------------------

// txEspiao marca quando o código está DENTRO da transação.
type txEspiao struct{ dentro bool }

func (x *txEspiao) Do(ctx context.Context, fn func(context.Context) error) error {
	x.dentro = true
	defer func() { x.dentro = false }()
	return fn(ctx)
}

// repoEspiao registra de que lado da transação cada chamada aconteceu.
type repoEspiao struct {
	transaction.Repository
	tx *txEspiao

	leuDentro    bool
	leuFora      bool
	escreveuFora bool

	// Os contextos de cada lado, para o teste do prazo próprio conferir QUEM
	// corre sob relógio e quem não.
	ctxDaLeitura context.Context
	ctxDaEscrita context.Context
}

func (r *repoEspiao) ListUncategorized(ctx context.Context, householdID, month string, limite int) ([]transaction.UncategorizedRow, error) {
	if r.tx.dentro {
		r.leuDentro = true
	} else {
		r.leuFora = true
	}
	r.ctxDaLeitura = ctx
	return r.Repository.ListUncategorized(ctx, householdID, month, limite)
}

func (r *repoEspiao) SetCategoryWhereNull(ctx context.Context, householdID string, ids []string, categoryID string, at time.Time) (int64, error) {
	if !r.tx.dentro {
		r.escreveuFora = true
	}
	r.ctxDaEscrita = ctx
	return r.Repository.SetCategoryWhereNull(ctx, householdID, ids, categoryID, at)
}

// ACHADO A2: a pontuação de até 10.000 linhas não pode rodar com uma conexão
// do pool presa numa transação aberta. A leitura e o cálculo saem; os UPDATE
// condicionais ficam.
//
// O que torna isso seguro é o WHERE do próprio UPDATE (`category_id IS NULL`),
// e não a transação: quem categorizar a linha no meio do caminho não é
// sobrescrito, e a contagem devolvida é a de linhas AFETADAS.
func TestAutoCategorizeCalculaForaDaTransacaoEEscreveDentro(t *testing.T) {
	t.Parallel()

	amb, linhas := cenarioDeCategorizacao(t)
	tx := &txEspiao{}
	espiao := &repoEspiao{Repository: amb.repo, tx: tx}
	svc := transaction.NewService(espiao, amb.contas, amb.categorias, amb.faturas, tx,
		classify.NewLoader(amb.categorias, amb.contas),
		transaction.WithIDs(amb.repo.proximoID),
		transaction.WithClock(func() time.Time { return agora }),
		transaction.WithAudit(amb.auditor),
	)

	view, err := svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{
		Month: "2026-09", DryRun: false,
	})
	require.NoError(t, err)
	assert.Positive(t, view.Categorized, "o cenário precisa categorizar alguma coisa")

	assert.True(t, espiao.leuFora, "a leitura das linhas sem categoria roda FORA da transação")
	assert.False(t, espiao.leuDentro, "nenhuma leitura de linha pode ter sobrado dentro da transação")
	assert.False(t, espiao.escreveuFora, "o UPDATE continua DENTRO da transação")

	// E o resultado continua o mesmo de sempre.
	lida, err := amb.repo.ByID(t.Context(), minhaCasa, linhas["casa"].ID)
	require.NoError(t, err)
	require.NotNil(t, lida.CategoryID)
	assert.Equal(t, "cat-mercado", *lida.CategoryID)
	require.Len(t, amb.auditor.registros, 1)
}

// Prévia nem abre transação: nada é escrito e nada é auditado.
func TestAutoCategorizePreviaNemAbreTransacao(t *testing.T) {
	t.Parallel()

	amb, _ := cenarioDeCategorizacao(t)
	tx := &txAbortada{}
	svc := transaction.NewService(amb.repo, amb.contas, amb.categorias, amb.faturas, tx,
		classify.NewLoader(amb.categorias, amb.contas),
		transaction.WithIDs(amb.repo.proximoID),
		transaction.WithClock(func() time.Time { return agora }),
		transaction.WithAudit(amb.auditor),
	)

	view, err := svc.AutoCategorize(t.Context(), ator(minhaCasa), transaction.AutoCategorizeInput{
		Month: "2026-09", DryRun: true,
	})
	require.NoError(t, err)
	assert.Positive(t, view.Categorized, "a prévia continua calculando")
	assert.Zero(t, tx.chamadas, "prévia não abre transação")
}

// txAbortada falha se alguém abrir transação — é como a prévia prova que não
// abre nenhuma.
type txAbortada struct{ chamadas int }

func (x *txAbortada) Do(context.Context, func(context.Context) error) error {
	x.chamadas++
	return errors.New("a prévia não deveria abrir transação")
}
