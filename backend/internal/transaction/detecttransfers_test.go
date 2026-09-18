package transaction_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// POST /transfers/detect (spec 0005 §13, ADR-028) do lado do serviço e do
// handler: a prévia não escreve nem audita; a execução real converte pares
// conferindo duas linhas por par, audita UMA vez e é idempotente; conflito
// desfaz tudo e vira 409; o teto é 422; e nada de outra casa é lido ou
// tocado.

// palavraDeConta cadastra uma palavra-chave numa conta da casa.
func (a *ambiente) palavraDeConta(casa, contaID, palavra string) {
	a.contas.palavras = append(a.contas.palavras, account.Keyword{
		ID: fmt.Sprintf("kwa-%d", len(a.contas.palavras)+1), HouseholdID: casa, AccountID: contaID,
		Keyword: palavra, Norm: textnorm.Normalize(palavra), Position: len(a.contas.palavras),
	})
}

// txComRollback é o Transactor que os testes de conversão usam: tira uma foto
// das linhas antes e a restaura quando a função devolve erro. É a parte do
// comportamento transacional que ESTES testes precisam provar — "409 e nada
// gravado" (critério 7b) —, e que o txDireto dos demais não tem.
type txComRollback struct{ repo *repoFake }

func (t txComRollback) Do(ctx context.Context, fn func(context.Context) error) error {
	antes := clonarLinhas(t.repo)
	if err := fn(ctx); err != nil {
		t.repo.linhas = antes
		return err
	}
	return nil
}

// errFalhaDeAuditoria é a falha injetada no auditor.
var errFalhaDeAuditoria = errors.New("audit_log indisponível")

// novoAmbienteTransacional é novoAmbiente com o Transactor que desfaz.
func novoAmbienteTransacional(t *testing.T, opts ...transaction.Option) *ambiente {
	t.Helper()

	repo := novoRepo()
	contas := novasContas()
	categorias := novasCategorias()
	faturas := novasFaturas()
	auditor := &auditorFake{}

	base := []transaction.Option{
		transaction.WithIDs(repo.proximoID),
		transaction.WithClock(func() time.Time { return agora }),
		transaction.WithAudit(auditor),
	}
	classificador := classify.NewLoader(categorias, contas)
	svc := transaction.NewService(repo, contas, categorias, faturas, txComRollback{repo}, classificador, append(base, opts...)...)

	return &ambiente{svc: svc, repo: repo, contas: contas, categorias: categorias, faturas: faturas, auditor: auditor}
}

// novoHTTPAmbienteTransacional é o httpAmbiente sobre o Transactor que desfaz
// — o que o teste de 409 precisa para provar "nada gravado".
func novoHTTPAmbienteTransacional(t *testing.T) *httpAmbiente {
	t.Helper()
	amb := novoAmbienteTransacional(t)
	logs := &bytes.Buffer{}
	lg := logging.New(logs, logging.Options{Level: "debug", Format: "json"})
	return &httpAmbiente{
		ambiente: amb,
		handler:  transaction.NewHandler(amb.svc, lg, 0),
		logs:     logs,
	}
}

// cenarioDeReprocessamento monta o caso real que a emenda §13 existe para
// resolver: extratos importados ANTES de as palavras-chave existirem.
//
//   - acc-a (Nubank) e acc-b (C6) têm palavra da PRÓPRIA conta: "enviado" e
//     "recebido" — o Pix entre contas próprias, que não nomeia o banco;
//   - acc-b também tem "c6", a palavra que OUTRA conta usa para nomeá-la;
//   - acc-arq está arquivada e nunca participa;
//   - a vizinha tem conta, palavras e lançamentos iguaizinhos.
func cenarioDeReprocessamento(t *testing.T, amb *ambiente) map[string]transaction.Transaction {
	t.Helper()

	amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "C6", account.KindChecking)
	amb.conta(minhaCasa, "acc-c", "Inter", account.KindChecking)
	arquivada := amb.conta(minhaCasa, "acc-arq", "Antiga", account.KindChecking)
	arquivada.ArchivedAt = ptr(agora)
	amb.contas.add(arquivada)

	amb.palavraDeConta(minhaCasa, "acc-a", "enviado")
	amb.palavraDeConta(minhaCasa, "acc-b", "recebido")
	amb.palavraDeConta(minhaCasa, "acc-b", "c6")
	amb.palavraDeConta(minhaCasa, "acc-arq", "antiga")

	linhas := map[string]transaction.Transaction{}
	// Par "própria conta em cada uma" (critério 1).
	linhas["saida"] = amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "Pix enviado - Bruno", 300_00, 25)
	linhas["entrada"] = amb.lancamento(minhaCasa, "acc-b", transaction.KindIncome, "Pix recebido de Bruno", 300_00, 25)
	// Candidata sem espelho (critério 4): nenhuma receita de 80,00 por perto.
	linhas["semEspelho"] = amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "Pix enviado - Maria", 80_00, 10)
	// Receita de mesmo valor SEM palavra: não qualifica o par "própria conta"
	// (critério 3), então semEspelho continua sem par.
	linhas["receitaSemPalavra"] = amb.lancamento(minhaCasa, "acc-b", transaction.KindIncome, "Salario", 80_00, 10)
	// Linha sem palavra nenhuma: não é candidata e não é contada.
	linhas["semPalavra"] = amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "Padaria da esquina", 12_00, 3)
	// Conta arquivada nunca participa, mesmo com palavra e espelho perfeito.
	linhas["arquivada"] = amb.lancamento(minhaCasa, "acc-arq", transaction.KindExpense, "Pix enviado - Antiga", 55_00, 12)
	linhas["espelhoDaArquivada"] = amb.lancamento(minhaCasa, "acc-b", transaction.KindIncome, "Pix recebido - Antiga", 55_00, 12)

	// A vizinha, com tudo igual: nada dela pode aparecer nem mudar.
	amb.conta(outraCasa, "acc-x", "Nubank dela", account.KindChecking)
	amb.conta(outraCasa, "acc-y", "C6 dela", account.KindChecking)
	amb.palavraDeConta(outraCasa, "acc-x", "enviado")
	amb.palavraDeConta(outraCasa, "acc-y", "recebido")
	linhas["saidaAlheia"] = amb.lancamento(outraCasa, "acc-x", transaction.KindExpense, "Pix enviado - Bruno", 300_00, 25)
	linhas["entradaAlheia"] = amb.lancamento(outraCasa, "acc-y", transaction.KindIncome, "Pix recebido de Bruno", 300_00, 25)

	return linhas
}

func TestDetectTransfersPreviaNaoEscreveNemAudita(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	linhas := cenarioDeReprocessamento(t, amb)
	antes := clonarLinhas(amb.repo)

	view, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)

	assert.Equal(t, "2026-09", view.Month)
	assert.EqualValues(t, 1, view.Paired, "o par de 300,00 entre Nubank e C6")
	assert.EqualValues(t, 2, view.Unpaired, "as duas candidatas sem espelho: Maria e o espelho da arquivada")
	require.Len(t, view.Items, 1)
	require.Len(t, view.UnpairedItems, 2)

	item := view.Items[0]
	assert.Equal(t, linhas["saida"].ID, item.OutTransactionID)
	assert.Equal(t, linhas["entrada"].ID, item.InTransactionID)
	assert.Equal(t, civil.MustNew(2026, 9, 25), item.OccurredOn)
	assert.Equal(t, "acc-a", item.FromAccountID)
	assert.Equal(t, "Nubank", item.FromAccountName)
	assert.Equal(t, "acc-b", item.ToAccountID)
	assert.Equal(t, "C6", item.ToAccountName)
	assert.EqualValues(t, 300_00, item.AmountCents)
	assert.Equal(t, "Pix enviado - Bruno", item.Description, "a descrição é a ORIGINAL da perna de saída")
	assert.Equal(t, "enviado", item.MatchedKeyword)
	assert.Equal(t, 100, item.MatchScore)

	for _, u := range view.UnpairedItems {
		assert.Equal(t, "no_mirror", u.Reason)
		assert.NotEmpty(t, u.AccountName)
		assert.NotEmpty(t, u.MatchedKeyword)
	}

	// Estado do repositório byte a byte igual, e nenhuma auditoria.
	assert.Equal(t, antes, clonarLinhas(amb.repo), "a prévia escreveu")
	assert.Empty(t, amb.auditor.registros, "a prévia auditou")
	assert.Zero(t, amb.repo.conversoes)
}

func TestDetectTransfersRealConverteOParEAuditaUmaVez(t *testing.T) {
	t.Parallel()

	amb := novoAmbienteTransacional(t)
	linhas := cenarioDeReprocessamento(t, amb)

	view, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09", DryRun: false})
	require.NoError(t, err)
	assert.EqualValues(t, 1, view.Paired, "pares CONVERTIDOS")
	assert.EqualValues(t, 2, view.Unpaired)
	assert.NotNil(t, view.Items)
	assert.NotNil(t, view.UnpairedItems)
	assert.Empty(t, view.Items, "a execução real não devolve listas")
	assert.Empty(t, view.UnpairedItems)

	le := func(k string) transaction.Transaction { return amb.repo.linhas[linhas[k].ID] }

	saida, entrada := le("saida"), le("entrada")
	assert.Equal(t, transaction.KindTransferOut, saida.Kind)
	assert.Equal(t, transaction.KindTransferIn, entrada.Kind)
	require.NotNil(t, saida.TransferGroupID)
	require.NotNil(t, entrada.TransferGroupID)
	assert.Equal(t, *saida.TransferGroupID, *entrada.TransferGroupID, "o MESMO grupo novo amarra as duas")
	assert.Nil(t, saida.CategoryID)
	assert.Nil(t, entrada.CategoryID)
	assert.Equal(t, agora, saida.UpdatedAt, "updated_at carimbado pelo relógio do serviço")

	// NADA mais muda: valor, data, competência, descrição, origem, lote,
	// external_id, chave e ordinal de deduplicação, fatura.
	for _, k := range []string{"saida", "entrada"} {
		original, depois := linhas[k], le(k)
		depois.Kind, depois.TransferGroupID, depois.CategoryID, depois.UpdatedAt =
			original.Kind, original.TransferGroupID, original.CategoryID, original.UpdatedAt
		assert.Equal(t, original, depois, "%s: a conversão tocou algo além das quatro colunas", k)
	}

	// O que não pode ter mudado.
	assert.Equal(t, transaction.KindExpense, le("semEspelho").Kind, "candidata sem espelho fica como está")
	assert.Nil(t, le("semEspelho").TransferGroupID, "nenhuma perna sintética é criada")
	assert.Equal(t, transaction.KindIncome, le("receitaSemPalavra").Kind)
	assert.Equal(t, transaction.KindExpense, le("semPalavra").Kind)
	assert.Equal(t, transaction.KindExpense, le("arquivada").Kind, "conta arquivada nunca participa")
	assert.Equal(t, transaction.KindExpense, le("saidaAlheia").Kind, "lançamento de outra casa jamais muda")
	assert.Equal(t, transaction.KindIncome, le("entradaAlheia").Kind)

	// Uma entrada de auditoria, do MÊS, sem descrição, sem valor.
	require.Len(t, amb.auditor.registros, 1)
	registro := amb.auditor.registros[0]
	assert.Equal(t, audit.ActionTransactionTransfersDetected, registro.Action)
	assert.Equal(t, "transaction.transfers_detected", registro.Action)
	assert.Equal(t, audit.EntityTransactionMonth, registro.Entity)
	assert.Equal(t, "2026-09", registro.EntityID)
	assert.Equal(t, minhaCasa, registro.HouseholdID)
	assert.Equal(t, usuario, registro.UserID)
}

func TestDetectTransfersSegundaExecucaoPareiaZero(t *testing.T) {
	t.Parallel()

	amb := novoAmbienteTransacional(t)
	cenarioDeReprocessamento(t, amb)

	primeira, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09"})
	require.NoError(t, err)
	require.EqualValues(t, 1, primeira.Paired)

	segunda, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.EqualValues(t, 0, segunda.Paired, "idempotente: as pernas já são transfer_* e saíram das candidatas")
	assert.EqualValues(t, 2, segunda.Unpaired, "as que não têm par continuam contadas")
	assert.Len(t, amb.auditor.registros, 2, "cada execução real audita uma vez, mesmo convertendo 0")
	assert.Equal(t, 1, amb.repo.conversoes, "a segunda execução não converteu nada")
}

// Critério 7(b): entre a LEITURA e o UPDATE da mesma execução, outra
// requisição mexe na linha → o UPDATE condicional não acha o que esperava, a
// transação inteira é desfeita e NADA fica gravado.
func TestDetectTransfersConflitoDesfazTudoENaoAudita(t *testing.T) {
	t.Parallel()

	casos := map[string]func(amb *ambiente, linhas map[string]transaction.Transaction){
		"a linha foi excluída no meio": func(amb *ambiente, linhas map[string]transaction.Transaction) {
			l := amb.repo.linhas[linhas["entrada"].ID]
			l.DeletedAt = ptr(agora)
			amb.repo.linhas[l.ID] = l
		},
		"a linha já virou transferência no meio": func(amb *ambiente, linhas map[string]transaction.Transaction) {
			l := amb.repo.linhas[linhas["entrada"].ID]
			l.Kind = transaction.KindTransferIn
			l.TransferGroupID = ptr("grupo-de-outra-execucao")
			amb.repo.linhas[l.ID] = l
		},
	}

	for nome, sabotar := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			amb := novoAmbienteTransacional(t)
			linhas := cenarioDeReprocessamento(t, amb)
			antes := clonarLinhas(amb.repo)
			amb.repo.antesDeConverter = func(transaction.TransferPairConversion) {
				amb.repo.antesDeConverter = nil
				sabotar(amb, linhas)
			}

			_, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09"})
			require.ErrorIs(t, err, transaction.ErrTransferConversionConflict)
			assert.False(t, transaction.IsValidationError(err), "conflito não é erro de validação: é 409")
			assert.Empty(t, amb.auditor.registros, "conflito não audita")

			// NADA gravado pela execução: toda linha continua como estava,
			// exceto a que a "outra requisição" mexeu.
			depois := clonarLinhas(amb.repo)
			sabotada := linhas["entrada"].ID
			for id, antesDela := range antes {
				if id == sabotada {
					continue
				}
				assert.Equal(t, antesDela, depois[id], "a linha %s foi gravada apesar do conflito", id)
			}
			assert.Equal(t, transaction.KindExpense, depois[linhas["saida"].ID].Kind)
			assert.Nil(t, depois[linhas["saida"].ID].TransferGroupID)
		})
	}
}

func TestDetectTransfersExigeMesEmFormaCanonica(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	for _, mes := range []string{"", "2026-13", "abc", "2026-1", "2026-09-01"} {
		_, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: mes, DryRun: true})
		require.ErrorIs(t, err, transaction.ErrInvalidMonth, "mês %q", mes)
		_, err = amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: mes})
		require.ErrorIs(t, err, transaction.ErrInvalidMonth, "mês %q (real)", mes)
	}
	assert.Empty(t, amb.auditor.registros)
	assert.Zero(t, amb.repo.conversoes)
}

func TestDetectTransfersSemCasaNoTokenNadaAcontece(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	_, err := amb.svc.DetectTransfers(t.Context(), transaction.Actor{UserID: usuario}, transaction.TransferDetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, transaction.ErrNotFound)
	assert.Empty(t, amb.repo.casasConsultadas, "sem casa no token, o repositório nem é consultado")
}

func TestDetectTransfersRecusaMesAcimaDoTeto(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	for i := 0; i <= transaction.MaxTransferDetectRows; i++ {
		amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "x", 1, 1)
	}

	_, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09", DryRun: true})
	require.ErrorIs(t, err, transaction.ErrTooManyTransferCandidates)
	assert.True(t, transaction.IsValidationError(err))
	_, err = amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, transaction.ErrTooManyTransferCandidates)
	assert.Empty(t, amb.auditor.registros, "recusa não audita")
	assert.Zero(t, amb.repo.conversoes, "nunca execução parcial")
}

// Critério 8: a casa do TOKEN é a única que chega ao repositório — provado
// pelas chamadas, e não só pelo resultado vazio.
func TestDetectTransfersSoConsultaACasaDoToken(t *testing.T) {
	t.Parallel()

	amb := novoAmbienteTransacional(t)
	linhas := cenarioDeReprocessamento(t, amb)

	_, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09"})
	require.NoError(t, err)
	require.NotEmpty(t, amb.repo.casasConsultadas)
	for _, casa := range amb.repo.casasConsultadas {
		assert.Equal(t, minhaCasa, casa, "uma consulta saiu com outra casa")
	}

	// Do lado da vizinha, só o par dela — e o meu já convertido não a afeta.
	amb.repo.casasConsultadas = nil
	view, err := amb.svc.DetectTransfers(t.Context(), ator(outraCasa), transaction.TransferDetectInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)
	require.Len(t, view.Items, 1)
	assert.Equal(t, linhas["saidaAlheia"].ID, view.Items[0].OutTransactionID)
	assert.Equal(t, linhas["entradaAlheia"].ID, view.Items[0].InTransactionID)
	for _, casa := range amb.repo.casasConsultadas {
		assert.Equal(t, outraCasa, casa)
	}
}

// O espelho pode estar em OUTRA competência: a janela é por occurred_on, com
// a folga de ±DedupWindowDays em volta da faixa REAL das candidatas
// (ADR-028c). A linha de fatura com competência do mês seguinte é achada.
func TestDetectTransfersAchaEspelhoDeOutraCompetencia(t *testing.T) {
	t.Parallel()

	amb := novoAmbienteTransacional(t)
	amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "C6", account.KindChecking)
	amb.palavraDeConta(minhaCasa, "acc-b", "c6")

	saida := amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "Pagamento fatura C6", 500_00, 30)
	// occurred_on em 02/10, competência OUTUBRO: fora do mês da candidata,
	// dentro da janela de 3 dias.
	entrada := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-b", Kind: transaction.KindIncome, AmountCents: 500_00,
		Description: "Pagamento recebido", DescriptionNorm: textnorm.Normalize("Pagamento recebido"),
		OccurredOn: civil.MustNew(2026, 10, 2), CompetenceMonth: "2026-10",
	})

	view, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, view.Paired)
	assert.Equal(t, transaction.KindTransferOut, amb.repo.linhas[saida.ID].Kind)
	assert.Equal(t, transaction.KindTransferIn, amb.repo.linhas[entrada.ID].Kind)
	assert.Equal(t, "2026-10", amb.repo.linhas[entrada.ID].CompetenceMonth, "a competência do espelho não muda")
}

func TestDetectTransfersFalhaDeAuditoriaAbortaAConversao(t *testing.T) {
	t.Parallel()

	amb := novoAmbienteTransacional(t)
	linhas := cenarioDeReprocessamento(t, amb)
	amb.auditor.falha = errFalhaDeAuditoria

	_, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09"})
	require.ErrorIs(t, err, errFalhaDeAuditoria)
	assert.Equal(t, transaction.KindExpense, amb.repo.linhas[linhas["saida"].ID].Kind, "sem rastro, a conversão não vale")
	assert.Equal(t, transaction.KindIncome, amb.repo.linhas[linhas["entrada"].ID].Kind)
}

// --- handler ---------------------------------------------------------------

func TestDetectTransfersHandlerExigeDryRunExplicito(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect", `{"month":"2026-09"}`, amb.handler.DetectTransfers)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeValidationFailed, codigo)
	assert.Contains(t, campos, "dryRun")
	assert.Empty(t, amb.auditor.registros, "dryRun ausente NUNCA converte")
	assert.Zero(t, amb.repo.conversoes)
}

func TestDetectTransfersHandlerValidaOCorpo(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	casos := []struct {
		nome   string
		corpo  string
		status int
		campo  string
	}{
		{"mês inválido", `{"month":"2026-13","dryRun":true}`, http.StatusBadRequest, "month"},
		{"mês ausente", `{"dryRun":false}`, http.StatusBadRequest, "month"},
		{"dryRun com tipo errado", `{"month":"2026-09","dryRun":"sim"}`, http.StatusBadRequest, "dryRun"},
		{"campo desconhecido", `{"month":"2026-09","dryRun":true,"householdId":"x"}`, http.StatusBadRequest, ""},
		{"ids no corpo não existem", `{"month":"2026-09","dryRun":true,"ids":["t-1"]}`, http.StatusBadRequest, ""},
		{"corpo vazio", ``, http.StatusUnsupportedMediaType, ""},
		{"dois objetos", `{"month":"2026-09","dryRun":true}{}`, http.StatusBadRequest, ""},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect", c.corpo, amb.handler.DetectTransfers)
			require.Equal(t, c.status, rec.Code, rec.Body.String())
			if c.campo != "" {
				_, campos := corpoDeErro(t, rec)
				assert.Contains(t, campos, c.campo)
			}
		})
	}
	assert.Empty(t, amb.auditor.registros)
	assert.Zero(t, amb.repo.conversoes)
}

func TestDetectTransfersHandlerSemSessaoResponde401(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	rec := amb.chamar(t, "", http.MethodPost, "/transfers/detect", `{"month":"2026-09","dryRun":false}`, amb.handler.DetectTransfers)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDetectTransfersHandlerPreviaEConversaoSeguemOContrato(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	linhas := cenarioDeReprocessamento(t, amb.ambiente)

	previa := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect", `{"month":"2026-09","dryRun":true}`, amb.handler.DetectTransfers)
	require.Equal(t, http.StatusOK, previa.Code, previa.Body.String())
	var corpo struct {
		Month         string            `json:"month"`
		Paired        int               `json:"paired"`
		Unpaired      int               `json:"unpaired"`
		Items         []json.RawMessage `json:"items"`
		UnpairedItems []json.RawMessage `json:"unpairedItems"`
	}
	require.NoError(t, json.Unmarshal(previa.Body.Bytes(), &corpo))
	assert.Equal(t, "2026-09", corpo.Month)
	assert.Equal(t, 1, corpo.Paired)
	assert.Equal(t, 2, corpo.Unpaired)
	require.Len(t, corpo.Items, 1)
	require.Len(t, corpo.UnpairedItems, 2)
	assert.Equal(t, transaction.KindExpense, amb.repo.linhas[linhas["saida"].ID].Kind, "a prévia não converteu")

	real := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect", `{"month":"2026-09","dryRun":false}`, amb.handler.DetectTransfers)
	require.Equal(t, http.StatusOK, real.Code, real.Body.String())
	// Listas VAZIAS (`[]`), nunca nulas.
	assert.Contains(t, real.Body.String(), `"items":[]`)
	assert.Contains(t, real.Body.String(), `"unpairedItems":[]`)
	assert.Equal(t, transaction.KindTransferOut, amb.repo.linhas[linhas["saida"].ID].Kind)

	// S8: o log da execução real tem request_id, mês e contagens — nunca a
	// descrição, a palavra-chave, o valor ou o id do lançamento.
	log := amb.logs.String()
	assert.Contains(t, log, `"paired":1`)
	assert.Contains(t, log, `"unpaired":2`)
	assert.Contains(t, log, `"month":"2026-09"`)
	for _, proibido := range []string{"Bruno", "Maria", "enviado", "recebido", "30000", "8000", linhas["saida"].ID} {
		assert.NotContains(t, log, proibido, "vazou no log: %q", proibido)
	}
}

func TestDetectTransfersHandlerRecusaMesAcimaDoTetoCom422(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	for i := 0; i <= transaction.MaxTransferDetectRows; i++ {
		amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "x", 1, 1)
	}

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect", `{"month":"2026-09","dryRun":true}`, amb.handler.DetectTransfers)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeValidationFailed, codigo)
	assert.Contains(t, campos, "month")
	assert.NotContains(t, campos["month"], "10001", "a contagem do mês é dado da casa")
}

// Critério 7(b) na borda: conflito é 409 CONFLICT, com mensagem genérica, sem
// `fields` e sem log de erro (não é falha do servidor).
func TestDetectTransfersHandlerConflitoResponde409(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbienteTransacional(t)
	linhas := cenarioDeReprocessamento(t, amb.ambiente)
	amb.repo.antesDeConverter = func(transaction.TransferPairConversion) {
		amb.repo.antesDeConverter = nil
		l := amb.repo.linhas[linhas["entrada"].ID]
		l.DeletedAt = ptr(agora)
		amb.repo.linhas[l.ID] = l
	}

	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect", `{"month":"2026-09","dryRun":false}`, amb.handler.DetectTransfers)
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	codigo, campos := corpoDeErro(t, rec)
	assert.Equal(t, httpserver.CodeConflict, codigo)
	assert.Equal(t, "CONFLICT", codigo)
	assert.Empty(t, campos, "409 CONFLICT não tem campo a corrigir")
	assert.Equal(t, transaction.KindExpense, amb.repo.linhas[linhas["saida"].ID].Kind, "nada gravado")
	assert.Empty(t, amb.auditor.registros)

	log := amb.logs.String()
	assert.NotContains(t, log, `"level":"ERROR"`, "conflito não é falha do servidor")
	for _, proibido := range []string{"Bruno", "enviado", "30000", linhas["saida"].ID} {
		assert.NotContains(t, log, proibido, "vazou no log: %q", proibido)
	}
}

// A resposta e os itens batem com os schemas publicados, campo a campo.
func TestRespostaDeTransferDetectResultTemTodosOsCamposRequiredDoContrato(t *testing.T) {
	t.Parallel()

	schemas := schemasDoContrato(t)
	amb := novoHTTPAmbiente(t)
	cenarioDeReprocessamento(t, amb.ambiente)

	previa := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect", `{"month":"2026-09","dryRun":true}`, amb.handler.DetectTransfers)
	require.Equal(t, http.StatusOK, previa.Code, previa.Body.String())
	conferirContrato(t, schemas, "TransferDetectResult", previa.Body.Bytes())

	var corpo struct {
		Items         []json.RawMessage `json:"items"`
		UnpairedItems []json.RawMessage `json:"unpairedItems"`
	}
	require.NoError(t, json.Unmarshal(previa.Body.Bytes(), &corpo))
	require.Len(t, corpo.Items, 1)
	require.NotEmpty(t, corpo.UnpairedItems)
	conferirContrato(t, schemas, "TransferDetectItem", corpo.Items[0])
	conferirContrato(t, schemas, "TransferDetectUnpairedItem", corpo.UnpairedItems[0])

	real := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect", `{"month":"2026-09","dryRun":false}`, amb.handler.DetectTransfers)
	require.Equal(t, http.StatusOK, real.Code, real.Body.String())
	conferirContrato(t, schemas, "TransferDetectResult", real.Body.Bytes())
}

func TestTransferDetectRequestAceitaExatamenteOsCamposDoContrato(t *testing.T) {
	t.Parallel()

	schemas := schemasDoContrato(t)
	esquema := schemas["TransferDetectRequest"]
	require.ElementsMatch(t, []string{"month", "dryRun"}, esquema.Required)
	amb := novoHTTPAmbiente(t)
	for campo := range esquema.Properties {
		corpo := fmt.Sprintf(`{"month":"2026-09","dryRun":true,%q:1}`, campo+"X")
		rec := amb.chamar(t, minhaCasa, http.MethodPost, "/transfers/detect", corpo, amb.handler.DetectTransfers)
		assert.Equal(t, http.StatusBadRequest, rec.Code, "campo fora do contrato precisa ser 400")
	}
}

// Decisão do usuário sobre o achado A3: DESPESA de fatura não participa do
// reprocessamento — nem como candidata, nem como espelho —, porque convertê-la
// mudaria o total cobrado da fatura, que a pessoa confere contra o banco. A
// RECEITA de fatura continua participando: é o pagamento da fatura, e virar
// transfer_in é o comportamento desejado (ADR-016).
func TestDetectTransfersNaoConverteDespesaLigadaAFatura(t *testing.T) {
	t.Parallel()

	amb := novoAmbienteTransacional(t)
	amb.conta(minhaCasa, "acc-conta", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-cartao", "Cartão", account.KindCreditCard)
	amb.palavraDeConta(minhaCasa, "acc-conta", "enviado")
	amb.palavraDeConta(minhaCasa, "acc-cartao", "recebido")

	fatura := "stmt-1"
	// Compra do cartão com descrição que bateria com a palavra da conta: se
	// participasse, viraria transfer_out e tiraria 300,00 do total da fatura.
	compra := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-cartao", Kind: transaction.KindExpense,
		AmountCents: 300_00, Description: "Pix enviado - Loja",
		DescriptionNorm: textnorm.Normalize("Pix enviado - Loja"),
		OccurredOn:      civil.MustNew(2026, 9, 10), StatementID: &fatura,
	})
	// O espelho perfeito dela, na conta corrente: sem a exclusão, os dois
	// virariam um par.
	espelhoDaCompra := amb.lancamento(minhaCasa, "acc-conta", transaction.KindIncome, "Pix recebido - Loja", 300_00, 10)

	// O par legítimo: pagamento saindo da conta e entrando no cartão (a
	// receita DA FATURA continua candidata).
	saida := amb.lancamento(minhaCasa, "acc-conta", transaction.KindExpense, "Pix enviado - fatura", 500_00, 20)
	entrada := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-cartao", Kind: transaction.KindIncome,
		AmountCents: 500_00, Description: "Pix recebido - fatura",
		DescriptionNorm: textnorm.Normalize("Pix recebido - fatura"),
		OccurredOn:      civil.MustNew(2026, 9, 20), StatementID: &fatura,
	})

	previa, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09", DryRun: true})
	require.NoError(t, err)
	assert.EqualValues(t, 1, previa.Paired, "só o par do pagamento da fatura")
	require.Len(t, previa.Items, 1)
	assert.Equal(t, saida.ID, previa.Items[0].OutTransactionID)
	assert.Equal(t, entrada.ID, previa.Items[0].InTransactionID)
	for _, item := range previa.Items {
		assert.NotEqual(t, compra.ID, item.OutTransactionID, "despesa de fatura nunca vira perna")
		assert.NotEqual(t, compra.ID, item.InTransactionID)
	}
	// A compra não é candidata, então também não é contada como "sem par":
	// ela está fora do reprocessamento, não sem espelho.
	for _, u := range previa.UnpairedItems {
		assert.NotEqual(t, compra.ID, u.ID, "despesa de fatura não é candidata nem sem-par")
	}
	assert.EqualValues(t, 1, previa.Unpaired, "só o espelho órfão da compra")
	require.Len(t, previa.UnpairedItems, 1)
	assert.Equal(t, espelhoDaCompra.ID, previa.UnpairedItems[0].ID)

	real, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.EqualValues(t, 1, real.Paired)

	assert.Equal(t, transaction.KindExpense, amb.repo.linhas[compra.ID].Kind, "a compra do cartão continua despesa")
	assert.Nil(t, amb.repo.linhas[compra.ID].TransferGroupID)
	assert.Equal(t, transaction.KindIncome, amb.repo.linhas[espelhoDaCompra.ID].Kind)
	assert.Equal(t, transaction.KindTransferOut, amb.repo.linhas[saida.ID].Kind)
	assert.Equal(t, transaction.KindTransferIn, amb.repo.linhas[entrada.ID].Kind, "a receita da fatura vira pagamento")
	require.NotNil(t, amb.repo.linhas[entrada.ID].StatementID)
	assert.Equal(t, fatura, *amb.repo.linhas[entrada.ID].StatementID, "a fatura da perna convertida continua a mesma")
}

// A ordem de travamento também é usada de verdade: a conversão acontece na
// ordem global, e não na ordem em que os pares foram montados.
func TestRegressaoA5ServicoConverteNaOrdemGlobal(t *testing.T) {
	t.Parallel()

	amb := novoAmbienteTransacional(t)
	amb.conta(minhaCasa, "acc-a", "Nubank", account.KindChecking)
	amb.conta(minhaCasa, "acc-b", "C6", account.KindChecking)
	amb.palavraDeConta(minhaCasa, "acc-a", "enviado")
	amb.palavraDeConta(minhaCasa, "acc-b", "recebido")

	// Dois pares em dias diferentes: o par do dia 20 tem os MENORES ids,
	// porque foi semeado primeiro. A leitura é por (occurred_on, id), então
	// o par do dia 10 viria antes; o travamento tem de inverter.
	saidaTarde := amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "Pix enviado - A", 100_00, 20)
	entradaTarde := amb.lancamento(minhaCasa, "acc-b", transaction.KindIncome, "Pix recebido - A", 100_00, 20)
	saidaCedo := amb.lancamento(minhaCasa, "acc-a", transaction.KindExpense, "Pix enviado - B", 200_00, 10)
	entradaCedo := amb.lancamento(minhaCasa, "acc-b", transaction.KindIncome, "Pix recebido - B", 200_00, 10)

	var ordem []string
	amb.repo.antesDeConverter = func(p transaction.TransferPairConversion) {
		ordem = append(ordem, min(p.OutID, p.InID))
	}

	view, err := amb.svc.DetectTransfers(t.Context(), ator(minhaCasa), transaction.TransferDetectInput{Month: "2026-09"})
	require.NoError(t, err)
	require.EqualValues(t, 2, view.Paired)

	require.Len(t, ordem, 2)
	assert.True(t, ordem[0] < ordem[1], "os pares são convertidos em ordem crescente de id, não de data")
	assert.Equal(t, min(saidaTarde.ID, entradaTarde.ID), ordem[0], "o par com o menor id vem primeiro")
	assert.Equal(t, min(saidaCedo.ID, entradaCedo.ID), ordem[1])
}
