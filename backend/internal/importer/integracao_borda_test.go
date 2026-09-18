package importer_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Testes da borda de LANÇAMENTOS e FATURAS, e da fiação que a E2 acende.
//
// Por que eles moram aqui, no pacote de teste da importação: este é o único
// lugar do projeto onde a pilha financeira inteira está montada de verdade —
// repositórios reais em SQLite, serviço de lançamento, serviço de fatura,
// serviço de conta com saldo derivado e o UsageChecker registrado. Os testes
// abaixo dependem de dado REAL gravado por uma importação REAL; escritos com
// dublês em cada pacote, eles provariam que os dublês funcionam.

func (a *ambiente) handlerLancamento() *transaction.Handler {
	return transaction.NewHandler(a.txSvc, logging.Discard(), 0)
}

func (a *ambiente) handlerFatura() *cardstatement.Handler {
	return cardstatement.NewHandler(a.stmtSvc, transaction.NewStatementLines(a.txSvc), logging.Discard(), 0)
}

// importarExtrato roda o ciclo completo e devolve o lote.
func (a *ambiente) importarExtrato(t *testing.T, contaID string) importer.BatchView {
	t.Helper()
	lote := a.enviarExtrato(t, contaID)
	_, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{})
	require.NoError(t, err)
	return lote
}

// --- GET /transactions -----------------------------------------------------

func TestGetTransactionsDevolveOMesEOResumo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	a.importarExtrato(t, conta.ID)

	r := requisicao(t, http.MethodGet, "/api/v1/transactions?month=2026-08", nil,
		identidade(a.casa.ID, a.usuario.ID))
	rec := httptest.NewRecorder()
	a.handlerLancamento().List(rec, r)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var view transaction.ListView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))

	assert.Len(t, view.Items, 12)
	assert.EqualValues(t, 12, view.Summary.Count)
	assert.EqualValues(t, 12, view.Summary.UncategorizedCount,
		"lançamento importado nasce sem categoria, e o número é o gancho da faixa na tela")
	assert.Equal(t, view.Summary.IncomeCents-view.Summary.ExpenseCents, view.Summary.NetCents)
	assert.Nil(t, view.NextCursor)

	// A casa VIZINHA não vê nada, mesmo pedindo o mesmo mês.
	rAlheio := requisicao(t, http.MethodGet, "/api/v1/transactions?month=2026-08", nil,
		identidade(a.alheia.ID, a.outroUsuario.ID))
	recAlheio := httptest.NewRecorder()
	a.handlerLancamento().List(recAlheio, rAlheio)
	require.Equal(t, http.StatusOK, recAlheio.Code)

	var vazia transaction.ListView
	require.NoError(t, json.Unmarshal(recAlheio.Body.Bytes(), &vazia))
	assert.Empty(t, vazia.Items)
}

func TestGetTransactionsValidaOsParametros(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	casos := map[string]string{
		"sem month":        "",
		"month 2026-13":    "?month=2026-13",
		"month abc":        "?month=abc",
		"month sem dia":    "?month=2026-8",
		"limit acima":      "?month=2026-08&limit=10000",
		"limit zero":       "?month=2026-08&limit=0",
		"cursor forjado":   "?month=2026-08&cursor=nao-e-base64-valido!",
		"cursor plausivel": "?month=2026-08&cursor=b2NjPWFiYw",
	}
	for nome, query := range casos {
		t.Run(nome, func(t *testing.T) {
			r := requisicao(t, http.MethodGet, "/api/v1/transactions"+query, nil,
				identidade(a.casa.ID, a.usuario.ID))
			rec := httptest.NewRecorder()
			a.handlerLancamento().List(rec, r)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}

	// Conta de OUTRA casa no filtro é 404 — nunca lista vazia, que seria
	// indistinguível de "conta sem lançamentos" e confirmaria a existência do
	// recurso alheio.
	alheia := a.conta(t, a.alheia.ID, "Conta Alheia", account.KindChecking, "nubank")
	r := requisicao(t, http.MethodGet, "/api/v1/transactions?month=2026-08&accountId="+alheia.ID, nil,
		identidade(a.casa.ID, a.usuario.ID))
	rec := httptest.NewRecorder()
	a.handlerLancamento().List(rec, r)
	assert.Equal(t, http.StatusNotFound, rec.Code)

	// E a conta própria continua funcionando.
	rOk := requisicao(t, http.MethodGet, "/api/v1/transactions?month=2026-08&accountId="+conta.ID, nil,
		identidade(a.casa.ID, a.usuario.ID))
	recOk := httptest.NewRecorder()
	a.handlerLancamento().List(recOk, rOk)
	assert.Equal(t, http.StatusOK, recOk.Code)
}

func TestGetTransactionDeOutraCasaResponde404(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	a.importarExtrato(t, conta.ID)

	alvo := a.lancamentosDa(t, a.casa.ID, "2026-08")[0]
	const inexistente = "00000000-0000-7000-e000-000000000001"

	get := func(t *testing.T, id, householdID, userID string) *httptest.ResponseRecorder {
		t.Helper()
		r := requisicao(t, http.MethodGet, "/api/v1/transactions/"+id, nil, identidade(householdID, userID))
		r.SetPathValue("id", id)
		rec := httptest.NewRecorder()
		a.handlerLancamento().Get(rec, r)
		return rec
	}

	alheio := get(t, alvo.ID, a.alheia.ID, a.outroUsuario.ID)
	fantasma := get(t, inexistente, a.alheia.ID, a.outroUsuario.ID)
	assert.Equal(t, http.StatusNotFound, alheio.Code)
	assert.Equal(t, fantasma.Body.Bytes(), alheio.Body.Bytes(), "byte a byte igual")

	dono := get(t, alvo.ID, a.casa.ID, a.usuario.ID)
	assert.Equal(t, http.StatusOK, dono.Code)
}

func TestDeleteTransactionExcluiOParInteiro(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	cartao := a.contaCartao(t)

	lote := a.enviarExtrato(t, conta.ID)
	pagamento := linhaComDescricao(t, a.linhasDoLote(t, lote.ID), "Pagamento de fatura")
	_, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
		Decisions: []importer.Decision{{
			RowID:                pagamento.ID,
			Action:               importer.ActionTransfer,
			CounterpartAccountID: cartao.ID,
		}},
	})
	require.NoError(t, err)

	antes := a.lancamentosDa(t, a.casa.ID, "2026-08")
	require.Len(t, antes, 14)

	var saida transaction.Transaction
	for _, l := range antes {
		if l.Kind == transaction.KindTransferOut {
			saida = l
		}
	}
	require.NotEmpty(t, saida.ID)

	r := requisicao(t, http.MethodDelete, "/api/v1/transactions/"+saida.ID, nil,
		identidade(a.casa.ID, a.usuario.ID))
	r.SetPathValue("id", saida.ID)
	rec := httptest.NewRecorder()
	a.handlerLancamento().Delete(rec, r)
	require.Equal(t, http.StatusNoContent, rec.Code)

	// AS DUAS pernas saem: meia transferência é dinheiro saindo de uma conta
	// sem entrar na outra (ADR-016).
	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 12)
}

// --- GET /card-statements --------------------------------------------------

func TestGetCardStatementsListaEDetalha(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)

	lote := a.enviarFatura(t, cartao.ID)
	res, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
		Statement: &importer.StatementConfirmation{
			CompetenceMonth: "2026-09",
			ClosingDate:     civil.MustNew(2026, 9, 5),
			DueDate:         civil.MustNew(2026, 9, 13),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, res.StatementID)

	// Lista.
	r := requisicao(t, http.MethodGet, "/api/v1/card-statements", nil, identidade(a.casa.ID, a.usuario.ID))
	rec := httptest.NewRecorder()
	a.handlerFatura().List(rec, r)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var lista cardstatement.ListView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &lista))
	require.Len(t, lista.Items, 1)
	assert.Equal(t, "2026-09", lista.Items[0].CompetenceMonth)
	assert.Positive(t, lista.Items[0].TotalCents, "o total é derivado das linhas")
	assert.Zero(t, lista.Items[0].PaidCents, "o pagamento foi ignorado por default")

	// Detalhe, com as linhas.
	rDetalhe := requisicao(t, http.MethodGet, "/api/v1/card-statements/"+*res.StatementID, nil,
		identidade(a.casa.ID, a.usuario.ID))
	rDetalhe.SetPathValue("id", *res.StatementID)
	recDetalhe := httptest.NewRecorder()
	a.handlerFatura().Get(recDetalhe, rDetalhe)
	require.Equal(t, http.StatusOK, recDetalhe.Code, recDetalhe.Body.String())

	var detalhe cardstatement.DetailView
	require.NoError(t, json.Unmarshal(recDetalhe.Body.Bytes(), &detalhe))
	assert.Equal(t, *res.StatementID, detalhe.Statement.ID)
	assert.Len(t, detalhe.Items, 14)

	// Fatura de outra casa é 404, igual a fatura inexistente.
	const inexistente = "00000000-0000-7000-f000-000000000001"
	buscar := func(t *testing.T, id string) *httptest.ResponseRecorder {
		t.Helper()
		req := requisicao(t, http.MethodGet, "/api/v1/card-statements/"+id, nil,
			identidade(a.alheia.ID, a.outroUsuario.ID))
		req.SetPathValue("id", id)
		w := httptest.NewRecorder()
		a.handlerFatura().Get(w, req)
		return w
	}
	alheio := buscar(t, *res.StatementID)
	fantasma := buscar(t, inexistente)
	assert.Equal(t, http.StatusNotFound, alheio.Code)
	assert.Equal(t, fantasma.Body.Bytes(), alheio.Body.Bytes())
}

func TestGetCardStatementValidaLimit(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	const id = "00000000-0000-7000-f000-000000000002"
	r := requisicao(t, http.MethodGet, "/api/v1/card-statements/"+id+"?limit=10000", nil,
		identidade(a.casa.ID, a.usuario.ID))
	r.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	a.handlerFatura().Get(rec, r)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- a fiação que a E2 acende ---------------------------------------------

// TestSaldoDerivadoSomaOsLancamentosImportados é o critério de aceite 1: com o
// WithBalances ligado, balanceCents deixa de ser o saldo de abertura e passa a
// ser abertura + soma com sinal. Continua sem nenhuma coluna de saldo.
func TestSaldoDerivadoSomaOsLancamentosImportados(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	a.importarExtrato(t, conta.ID)

	var esperado int64
	for _, l := range a.lancamentosDa(t, a.casa.ID, "2026-08") {
		esperado += l.SignedAmountCents()
	}

	view, err := a.contaSvc.List(t.Context(), account.Actor{
		HouseholdID: a.casa.ID, UserID: a.usuario.ID,
	}, false)
	require.NoError(t, err)

	var achou bool
	for _, c := range view.Items {
		if c.ID == conta.ID {
			achou = true
			assert.Equal(t, conta.OpeningBalanceCents+esperado, c.BalanceCents)
		}
	}
	assert.True(t, achou)
	assert.Equal(t, conta.OpeningBalanceCents+esperado, view.TotalBalanceCents,
		"o total da lista bate com a soma das linhas exibidas")
}

// TestExcluirContaComLancamentoResponde422 é o critério de aceite 2 — a dívida
// que a spec 0003 §1 deixou declarada e que esta entrega paga.
func TestExcluirContaComLancamentoResponde422(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	a.importarExtrato(t, conta.ID)

	ator := account.Actor{HouseholdID: a.casa.ID, UserID: a.usuario.ID}
	err := a.contaSvc.Delete(t.Context(), ator, conta.ID)
	require.ErrorIs(t, err, account.ErrInUse,
		"a saída é ARQUIVAR, e é isso que a mensagem do 422 diz")

	// E arquivar funciona: é a operação certa para "não uso mais esta conta".
	_, err = a.contaSvc.Archive(t.Context(), ator, conta.ID)
	require.NoError(t, err)
}

func TestExcluirCategoriaEmUsoResponde422(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	ator := category.Actor{HouseholdID: a.casa.ID, UserID: a.usuario.ID}
	cat, err := a.categoriaSvc.Create(t.Context(), ator, category.CreateInput{
		Name: "Alimentação",
		Kind: category.KindExpense,
	})
	require.NoError(t, err)

	// A categoria vai numa linha ESPECÍFICA, e não como padrão do lote: o
	// extrato tem receitas e despesas, e categoria de despesa numa receita é
	// recusada pelo domínio (ErrCategoryKindMismatch) — que é a regra certa.
	lote := a.enviarExtrato(t, conta.ID)
	despesa := linhaComDescricao(t, a.linhasDoLote(t, lote.ID), "Pix enviado - Fulano de Tal Silva")
	_, err = a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
		Decisions: []importer.Decision{{
			RowID:      despesa.ID,
			Action:     importer.ActionImport,
			CategoryID: importer.OptionalCategory{Set: true, ID: &cat.ID},
		}},
	})
	require.NoError(t, err)

	require.ErrorIs(t, a.categoriaSvc.Delete(t.Context(), ator, cat.ID), category.ErrInUse)
}

// TestCategoriaPadraoIncompativelDerrubaOLoteInteiro documenta a consequência
// da regra acima: categoria de DESPESA como padrão de um arquivo que tem
// receita recusa o lote inteiro, e não grava metade.
func TestCategoriaPadraoIncompativelDerrubaOLoteInteiro(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	cat, err := a.categoriaSvc.Create(t.Context(), category.Actor{
		HouseholdID: a.casa.ID, UserID: a.usuario.ID,
	}, category.CreateInput{Name: "Alimentação", Kind: category.KindExpense})
	require.NoError(t, err)

	lote := a.enviarExtrato(t, conta.ID)
	_, err = a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
		DefaultCategoryID: &cat.ID,
	})
	require.ErrorIs(t, err, transaction.ErrCategoryKindMismatch)
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), "tudo ou nada")
}
