package importer_test

import (
	"errors"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// contaCorrente e contaCartao são os dois destinos possíveis.
func (a *ambiente) contaCorrente(t *testing.T) *account.Account {
	return a.conta(t, a.casa.ID, "Nubank Conta", account.KindChecking, "nubank")
}

func (a *ambiente) contaCartao(t *testing.T) *account.Account {
	return a.conta(t, a.casa.ID, "Nubank Cartão", account.KindCreditCard, "nubank")
}

// enviarExtrato roda a fase 1 com a fixture do extrato.
func (a *ambiente) enviarExtrato(t *testing.T, contaID string) importer.BatchView {
	t.Helper()
	view, err := a.svc.Analyze(t.Context(), a.ator(), importer.AnalyzeInput{
		AccountID: contaID,
		FileName:  "NU_2026-08.csv",
		Content:   fixtureExtrato(t),
	})
	require.NoError(t, err)
	return view
}

func (a *ambiente) enviarFatura(t *testing.T, contaID string) importer.BatchView {
	t.Helper()
	view, err := a.svc.Analyze(t.Context(), a.ator(), importer.AnalyzeInput{
		AccountID: contaID,
		FileName:  "Nubank_2026-09-13.csv",
		Content:   fixtureFatura(t),
	})
	require.NoError(t, err)
	return view
}

// linhasDoLote devolve a revisão inteira.
func (a *ambiente) linhasDoLote(t *testing.T, batchID string) []importer.RowView {
	t.Helper()
	preview, err := a.svc.Preview(t.Context(), a.ator(), batchID, 0, 200)
	require.NoError(t, err)
	return preview.Items
}

// linhaComDescricao acha a linha da revisão pela descrição já sanitizada.
func linhaComDescricao(t *testing.T, linhas []importer.RowView, trecho string) importer.RowView {
	t.Helper()
	for _, l := range linhas {
		if l.Description != nil && *l.Description == trecho {
			return l
		}
	}
	t.Fatalf("linha %q não encontrada na revisão", trecho)
	return importer.RowView{}
}

// --- fase 1 ----------------------------------------------------------------

func TestAnalisarExtratoClassificaEGrava(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	lote := a.enviarExtrato(t, conta.ID)

	assert.Equal(t, importer.BatchStatusPending, lote.Status)
	assert.Equal(t, "nubank", lote.Institution)
	assert.Equal(t, string(importer.DocKindCheckingStatement), lote.DocKind)
	assert.Equal(t, "nubank.checking.v1", lote.FormatID)
	assert.Equal(t, "utf-8", lote.Encoding)
	assert.Equal(t, 13, lote.RowCount)
	assert.Equal(t, "2026-08-04", lote.MinDate.String())
	assert.Equal(t, "2026-08-31", lote.MaxDate.String())

	// Extrato não tem fatura: a sugestão precisa vir NULA, senão a tela
	// mostraria um bloco de competência que não faz sentido nenhum aqui.
	assert.Nil(t, lote.StatementSuggestion)
	assert.Nil(t, lote.SameContentImportedAt)

	require.NotNil(t, lote.Counts)
	assert.Equal(t, 12, lote.Counts.New)
	assert.Equal(t, 1, lote.Counts.CardPayment, "a linha 'Pagamento de fatura' é barrada por default")
	assert.Zero(t, lote.Counts.DuplicateExact)
	assert.Zero(t, lote.Counts.Rejected)

	// A descrição gravada é a SANITIZADA: o nome da contraparte fica, os
	// identificadores numéricos saem (§6.8).
	linhas := a.linhasDoLote(t, lote.ID)
	require.Len(t, linhas, 13)
	pix := linhaComDescricao(t, linhas, "Pix enviado - Fulano de Tal Silva")
	require.NotNil(t, pix.AmountCents)
	assert.EqualValues(t, 2000, *pix.AmountCents, "o valor é positivo; o sinal vive no kind")
	require.NotNil(t, pix.Kind)
	assert.Equal(t, transaction.KindExpense, *pix.Kind)

	// A auditoria do LOTE é uma entrada só, e nenhuma delas carrega valor.
	assert.Equal(t, []string{"import.created"}, a.auditoria.acoes())
}

func TestAnalisarRecusaFaturaEmContaQueNaoECartao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	_, err := a.svc.Analyze(t.Context(), a.ator(), importer.AnalyzeInput{
		AccountID: conta.ID,
		FileName:  "fatura.csv",
		Content:   fixtureFatura(t),
	})
	require.ErrorIs(t, err, importer.ErrTargetMismatch)
}

func TestAnalisarRecusaExtratoEmContaDeCartao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)

	_, err := a.svc.Analyze(t.Context(), a.ator(), importer.AnalyzeInput{
		AccountID: cartao.ID,
		FileName:  "extrato.csv",
		Content:   fixtureExtrato(t),
	})
	require.ErrorIs(t, err, importer.ErrTargetMismatch)
}

func TestAnalisarRecusaInstituicaoDiferenteDaConta(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.conta(t, a.casa.ID, "C6 Conta", account.KindChecking, "c6")

	_, err := a.svc.Analyze(t.Context(), a.ator(), importer.AnalyzeInput{
		AccountID: conta.ID,
		FileName:  "extrato.csv",
		Content:   fixtureExtrato(t),
	})
	require.ErrorIs(t, err, importer.ErrTargetMismatch)
}

func TestAnalisarAceitaContaComInstituicaoOther(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.conta(t, a.casa.ID, "Conta Genérica", account.KindChecking, account.InstitutionOther)

	lote := a.enviarExtrato(t, conta.ID)
	assert.Equal(t, 13, lote.RowCount)
}

func TestAnalisarRecusaContaDeOutraCasa(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	// A conta É real — só não é da casa de quem está agindo. É esse o cenário
	// que prova isolamento; id inventado provaria só que a busca falha.
	_, err := a.svc.Analyze(t.Context(), a.atorAlheio(), importer.AnalyzeInput{
		AccountID: conta.ID,
		FileName:  "extrato.csv",
		Content:   fixtureExtrato(t),
	})
	require.ErrorIs(t, err, importer.ErrAccountNotFound)
}

func TestAnalisarAvisaQueOMesmoConteudoJaFoiImportado(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	primeiro := a.enviarExtrato(t, conta.ID)
	require.Nil(t, primeiro.SameContentImportedAt)

	// O aviso fala de IMPORTAÇÃO, então ele só existe depois que uma
	// importação existe: enquanto o primeiro lote é rascunho pendente, o
	// segundo envio não avisa nada — nenhum lançamento foi gravado ainda.
	semConfirmar := a.enviarExtrato(t, conta.ID)
	require.Nil(t, semConfirmar.SameContentImportedAt,
		"lote pendente não importou nada: dizer que importou faria a pessoa pular uma importação legítima")

	_, err := a.svc.Confirm(t.Context(), a.ator(), primeiro.ID, importer.ConfirmInput{})
	require.NoError(t, err)

	segundo := a.enviarExtrato(t, conta.ID)
	require.NotNil(t, segundo.SameContentImportedAt, "o hash do conteúdo é AVISO, e o aviso tem de aparecer")
	// E não é bloqueio: o segundo lote existe e é analisável.
	assert.Equal(t, importer.BatchStatusPending, segundo.Status)
	assert.NotEqual(t, primeiro.ID, segundo.ID)
}

func TestAnalisarSugereFaturaPelosDiasDaConta(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	fechamento, vencimento := 5, 13
	cartao := a.contaCartao(t)
	cartao.StatementClosingDay = &fechamento
	cartao.StatementDueDay = &vencimento
	require.NoError(t, a.repoConta.Update(t.Context(), cartao))

	lote := a.enviarFatura(t, cartao.ID)

	require.NotNil(t, lote.StatementSuggestion)
	assert.Equal(t, "2026-09", lote.StatementSuggestion.CompetenceMonth)
	assert.Equal(t, "2026-09-05", lote.StatementSuggestion.ClosingDate.String())
	assert.Equal(t, "2026-09-13", lote.StatementSuggestion.DueDate.String())
}

func TestAnalisarSugereFaturaPelaMaiorDataQuandoAContaNaoTemDias(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)

	lote := a.enviarFatura(t, cartao.ID)

	require.NotNil(t, lote.StatementSuggestion)
	// Fechamento na maior data do arquivo; vencimento na folga padrão. O nome
	// do arquivo ("Nubank_2026-09-13.csv") NÃO entra na conta — se entrasse,
	// bastaria renomear o arquivo para mudar a competência de dezenas de
	// linhas.
	assert.Equal(t, "2026-09-05", lote.StatementSuggestion.ClosingDate.String())
	assert.Equal(t, "2026-09-15", lote.StatementSuggestion.DueDate.String())
	assert.Equal(t, "2026-09", lote.StatementSuggestion.CompetenceMonth)
}

func TestAnalisarSanitizaONomeDoArquivo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	lote, err := a.svc.Analyze(t.Context(), a.ator(), importer.AnalyzeInput{
		AccountID: conta.ID,
		FileName:  `..\..\Windows\System32\extrato.csv`,
		Content:   fixtureExtrato(t),
	})
	require.NoError(t, err)
	assert.Equal(t, "extrato.csv", lote.FileName)
}

// --- fase 2 ----------------------------------------------------------------

func TestConfirmarExtratoImportaOsPadroesEIgnoraOPagamentoDeFatura(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	res, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{})
	require.NoError(t, err)

	assert.Equal(t, importer.BatchStatusCommitted, res.Status)
	assert.Equal(t, 12, res.Imported)
	assert.Equal(t, 1, res.Skipped, "o pagamento de fatura NÃO entra sem decisão explícita")
	assert.Zero(t, res.Blocked)
	assert.Zero(t, res.Restored)
	assert.Nil(t, res.StatementID)
	assert.Zero(t, res.TransfersCreated)
	assert.Empty(t, res.BlockedRows)

	linhas := a.lancamentosDa(t, a.casa.ID, "2026-08")
	require.Len(t, linhas, 12)

	receitas, despesas := 0, 0
	for _, l := range linhas {
		switch l.Kind {
		case transaction.KindIncome:
			receitas++
		case transaction.KindExpense:
			despesas++
		}
		assert.Positive(t, l.AmountCents, "o valor gravado é sempre positivo (ADR-003)")
		assert.Equal(t, transaction.SourceImport, l.Source)
		require.NotNil(t, l.ImportBatchID)
		assert.Equal(t, lote.ID, *l.ImportBatchID)
		assert.Nil(t, l.CategoryID, "lançamento importado nasce SEM categoria (D3)")
	}
	assert.Equal(t, 4, receitas)
	assert.Equal(t, 8, despesas)

	// As linhas de staging somem fisicamente no commit.
	preview, err := a.svc.Preview(t.Context(), a.ator(), lote.ID, 0, 200)
	require.NoError(t, err)
	assert.Empty(t, preview.Items)
	assert.Nil(t, preview.Batch.Counts, "lote terminal não tem mais análise por status")
	require.NotNil(t, preview.Batch.Outcome)
	assert.Equal(t, 12, preview.Batch.Outcome.Imported)

	// UMA entrada de auditoria por lote — não doze de transaction.created.
	assert.Equal(t, []string{"import.created", "import.confirmed"}, a.auditoria.acoes())
}

func TestReimportarOMesmoArquivoNaoGravaNada(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	primeiro := a.enviarExtrato(t, conta.ID)
	_, err := a.svc.Confirm(t.Context(), a.ator(), primeiro.ID, importer.ConfirmInput{})
	require.NoError(t, err)

	segundo := a.enviarExtrato(t, conta.ID)
	require.NotNil(t, segundo.Counts)
	assert.Equal(t, 12, segundo.Counts.DuplicateExact, "as 12 linhas gravadas voltam marcadas")
	assert.Zero(t, segundo.Counts.New)

	res, err := a.svc.Confirm(t.Context(), a.ator(), segundo.ID, importer.ConfirmInput{})
	require.NoError(t, err)
	assert.Zero(t, res.Imported, "ZERO linhas entram na reimportação com os padrões")
	assert.Equal(t, 13, res.Skipped)

	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 12, "nenhuma duplicata entrou")
}

func TestCompraLegitimaRepetidaEntraDuasVezesENenhumaSome(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)

	lote := a.enviarFatura(t, cartao.ID)
	require.NotNil(t, lote.Counts)
	assert.Equal(t, 13, lote.Counts.New)
	assert.Equal(t, 1, lote.Counts.RepeatedInFile, "o par 'Cafe Exemplo' idêntico é 2ª ocorrência, não duplicata")
	assert.Equal(t, 1, lote.Counts.CardPayment)

	res, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
		Statement: &importer.StatementConfirmation{
			CompetenceMonth: "2026-09",
			ClosingDate:     civil.MustNew(2026, 9, 5),
			DueDate:         civil.MustNew(2026, 9, 13),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, 14, res.Imported)
	assert.Equal(t, 1, res.Skipped)
	require.NotNil(t, res.StatementID)

	// Os DOIS cafés estão lá. Num app de dinheiro, sumir com um lançamento é
	// pior do que duplicar um.
	linhas := a.lancamentosDa(t, a.casa.ID, "2026-09")
	cafes := 0
	for _, l := range linhas {
		if l.Description == "Cafe Exemplo" {
			cafes++
			assert.EqualValues(t, 1100, l.AmountCents)
		}
	}
	assert.Equal(t, 2, cafes)

	// Todas as linhas da fatura têm competência do VENCIMENTO (D2) e apontam
	// para a fatura criada.
	require.Len(t, linhas, 14)
	for _, l := range linhas {
		assert.Equal(t, "2026-09", l.CompetenceMonth)
		require.NotNil(t, l.StatementID)
		assert.Equal(t, *res.StatementID, *l.StatementID)
	}

	// Reimportar a mesma fatura barra os dois cafés — e não engole nenhum.
	segundo := a.enviarFatura(t, cartao.ID)
	require.NotNil(t, segundo.Counts)
	assert.Equal(t, 14, segundo.Counts.DuplicateExact)
	assert.Zero(t, segundo.Counts.New)
	assert.Zero(t, segundo.Counts.RepeatedInFile)
}

func TestConfirmarFaturaReusaAFaturaExistente(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)

	confirmacao := importer.ConfirmInput{
		Statement: &importer.StatementConfirmation{
			CompetenceMonth: "2026-09",
			ClosingDate:     civil.MustNew(2026, 9, 5),
			DueDate:         civil.MustNew(2026, 9, 13),
		},
	}

	primeiro := a.enviarFatura(t, cartao.ID)
	res1, err := a.svc.Confirm(t.Context(), a.ator(), primeiro.ID, confirmacao)
	require.NoError(t, err)

	segundo := a.enviarFatura(t, cartao.ID)
	res2, err := a.svc.Confirm(t.Context(), a.ator(), segundo.ID, confirmacao)
	require.NoError(t, err)

	require.NotNil(t, res1.StatementID)
	require.NotNil(t, res2.StatementID)
	assert.Equal(t, *res1.StatementID, *res2.StatementID,
		"importar a fatura de setembro duas vezes reusa a MESMA fatura")

	faturas, err := a.repoFatura.List(t.Context(), a.casa.ID, cardstatement.ListFilter{})
	require.NoError(t, err)
	assert.Len(t, faturas, 1)
}

func TestConfirmarFaturaExigeOBlocoDeConfirmacao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	cartao := a.contaCartao(t)
	lote := a.enviarFatura(t, cartao.ID)

	_, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{})
	require.ErrorIs(t, err, importer.ErrStatementRequired)
}

func TestConfirmarExtratoRecusaOBlocoDeFatura(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	_, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
		Statement: &importer.StatementConfirmation{
			CompetenceMonth: "2026-09",
			ClosingDate:     civil.MustNew(2026, 9, 5),
			DueDate:         civil.MustNew(2026, 9, 13),
		},
	})
	require.ErrorIs(t, err, importer.ErrStatementNotAllowed)
}

func TestConfirmarEIdempotente(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	primeiro, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{})
	require.NoError(t, err)

	segundo, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{})
	require.NoError(t, err, "duplo clique em conexão ruim responde 200, não erro")

	assert.Equal(t, primeiro.Imported, segundo.Imported)
	assert.Equal(t, primeiro.Skipped, segundo.Skipped)
	assert.Equal(t, primeiro.Status, segundo.Status)
	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 12, "UM conjunto de lançamentos, não dois")

	// A segunda confirmação não gera entrada de auditoria nova: ela não fez
	// nada.
	assert.Equal(t, []string{"import.created", "import.confirmed"}, a.auditoria.acoes())
}

func TestLiberarLinhaExcluidaRestauraOMesmoLancamento(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	primeiro := a.enviarExtrato(t, conta.ID)
	_, err := a.svc.Confirm(t.Context(), a.ator(), primeiro.ID, importer.ConfirmInput{})
	require.NoError(t, err)

	gravados := a.lancamentosDa(t, a.casa.ID, "2026-08")
	alvo := gravados[0]
	require.NoError(t, a.txSvc.SoftDelete(t.Context(), transaction.Actor{
		HouseholdID: a.casa.ID, UserID: a.usuario.ID,
	}, alvo.ID))
	assert.Len(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), 11)

	segundo := a.enviarExtrato(t, conta.ID)
	require.NotNil(t, segundo.Counts)
	assert.Equal(t, 1, segundo.Counts.DuplicateDeleted)

	linhas := a.linhasDoLote(t, segundo.ID)
	var excluida importer.RowView
	for _, l := range linhas {
		if l.Status == string(dedup.StatusDuplicateDeleted) {
			excluida = l
		}
	}
	require.NotEmpty(t, excluida.ID)
	assert.Equal(t, importer.ActionSkip, excluida.DefaultAction, "barrada por default")
	assert.Contains(t, excluida.AllowedActions, importer.ActionImport, "mas liberável")
	require.NotNil(t, excluida.MatchTransactionID)
	assert.Equal(t, alvo.ID, *excluida.MatchTransactionID)

	res, err := a.svc.Confirm(t.Context(), a.ator(), segundo.ID, importer.ConfirmInput{
		Decisions: []importer.Decision{{RowID: excluida.ID, Action: importer.ActionImport}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.Restored)
	assert.Zero(t, res.Imported, "restaurar NÃO insere outro lançamento")

	voltou, err := a.repoTx.ByID(t.Context(), a.casa.ID, alvo.ID)
	require.NoError(t, err, "é o MESMO id de antes")
	assert.Equal(t, alvo.AmountCents, voltou.AmountCents)
	assert.Equal(t, alvo.Description, voltou.Description)
	assert.Nil(t, voltou.DeletedAt)

	assert.Contains(t, a.auditoria.acoes(), "transaction.restored",
		"a restauração é escrita AVULSA e tem entrada própria")
}

func TestLiberarPagamentoDeFaturaCriaOParDeTransferencia(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	cartao := a.contaCartao(t)

	lote := a.enviarExtrato(t, conta.ID)
	pagamento := linhaComDescricao(t, a.linhasDoLote(t, lote.ID), "Pagamento de fatura")
	assert.Equal(t, string(dedup.StatusCardPayment), pagamento.Status)
	assert.Contains(t, pagamento.AllowedActions, importer.ActionTransfer)

	res, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
		Decisions: []importer.Decision{{
			RowID:                pagamento.ID,
			Action:               importer.ActionTransfer,
			CounterpartAccountID: cartao.ID,
		}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, res.TransfersCreated)
	assert.Equal(t, 13, res.Imported, "a transferência é UMA linha do arquivo, não duas")
	assert.Zero(t, res.Skipped)

	linhas := a.lancamentosDa(t, a.casa.ID, "2026-08")
	var saida, entrada *transaction.Transaction
	for i := range linhas {
		switch linhas[i].Kind {
		case transaction.KindTransferOut:
			saida = &linhas[i]
		case transaction.KindTransferIn:
			entrada = &linhas[i]
		}
	}
	require.NotNil(t, saida)
	require.NotNil(t, entrada)
	assert.Equal(t, conta.ID, saida.AccountID, "o dinheiro sai da conta do arquivo")
	assert.Equal(t, cartao.ID, entrada.AccountID, "e entra no cartão")
	assert.Equal(t, saida.AmountCents, entrada.AmountCents)
	require.NotNil(t, saida.TransferGroupID)
	require.NotNil(t, entrada.TransferGroupID)
	assert.Equal(t, *saida.TransferGroupID, *entrada.TransferGroupID)
	assert.Nil(t, saida.CategoryID, "transferência nunca tem categoria (ADR-016)")
}

func TestTransferenciaParaContaDeOutraCasaResponde404(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	// A conta da outra perna existe e é REAL — só que é da OUTRA casa. É o
	// vetor de BOLA mais fácil de esquecer, porque ela cria linha numa conta
	// diferente da conta do lote.
	alheia := a.conta(t, a.alheia.ID, "Cartão Alheio", account.KindCreditCard, "nubank")

	lote := a.enviarExtrato(t, conta.ID)
	pagamento := linhaComDescricao(t, a.linhasDoLote(t, lote.ID), "Pagamento de fatura")

	_, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
		Decisions: []importer.Decision{{
			RowID:                pagamento.ID,
			Action:               importer.ActionTransfer,
			CounterpartAccountID: alheia.ID,
		}},
	})
	// A contraparte é conferida pela própria importação, pela casa do token,
	// ANTES de o par chegar ao domínio de lançamentos (spec 0005 §12): conta
	// de outra casa é o MESMO ErrAccountNotFound de conta inexistente — o
	// mesmo 404 da conta do lote alheia. O domínio de lançamentos repete a
	// conferência dentro da transação (defesa em profundidade).
	require.ErrorIs(t, err, importer.ErrAccountNotFound)
	require.NotErrorIs(t, err, importer.ErrCounterpartArchived, "alheia nunca vira 422")

	// E NADA foi gravado: o lote inteiro volta atrás.
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
	assert.Empty(t, a.lancamentosDa(t, a.alheia.ID, "2026-08"))
}

func TestDecisaoComLinhaDeOutroLoteResponde400(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	primeiro := a.enviarExtrato(t, conta.ID)
	segundo := a.enviarExtrato(t, conta.ID)

	linhaDoOutro := a.linhasDoLote(t, primeiro.ID)[0]
	_, err := a.svc.Confirm(t.Context(), a.ator(), segundo.ID, importer.ConfirmInput{
		Decisions: []importer.Decision{{RowID: linhaDoOutro.ID, Action: importer.ActionSkip}},
	})
	require.ErrorIs(t, err, importer.ErrRowNotInBatch, "nunca ignorada em silêncio")
}

func TestDecisaoComAcaoNaoOferecidaResponde400(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	primeiro := a.enviarExtrato(t, conta.ID)
	_, err := a.svc.Confirm(t.Context(), a.ator(), primeiro.ID, importer.ConfirmInput{})
	require.NoError(t, err)

	segundo := a.enviarExtrato(t, conta.ID)
	linhas := a.linhasDoLote(t, segundo.ID)
	var exata importer.RowView
	for _, l := range linhas {
		if l.Status == string(dedup.StatusDuplicateExact) {
			exata = l
			break
		}
	}
	require.NotEmpty(t, exata.ID)
	assert.Empty(t, exata.AllowedActions, "duplicado_exato não é liberável")

	_, err = a.svc.Confirm(t.Context(), a.ator(), segundo.ID, importer.ConfirmInput{
		Decisions: []importer.Decision{{RowID: exata.ID, Action: importer.ActionImport}},
	})
	require.ErrorIs(t, err, importer.ErrActionNotAllowed)
}

func TestTransferenciaExigeAContaDaOutraPerna(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)
	pagamento := linhaComDescricao(t, a.linhasDoLote(t, lote.ID), "Pagamento de fatura")

	_, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
		Decisions: []importer.Decision{{RowID: pagamento.ID, Action: importer.ActionTransfer}},
	})
	require.ErrorIs(t, err, importer.ErrCounterpartRequired)

	_, err = a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
		Decisions: []importer.Decision{{
			RowID:                pagamento.ID,
			Action:               importer.ActionTransfer,
			CounterpartAccountID: conta.ID,
		}},
	})
	require.ErrorIs(t, err, importer.ErrSameAccountTransfer)
}

func TestDecisoesDemaisSaoRecusadas(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	decisoes := make([]importer.Decision, importer.MaxDecisions+1)
	for i := range decisoes {
		decisoes[i] = importer.Decision{RowID: a.proximoID("r"), Action: importer.ActionSkip}
	}
	_, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{Decisions: decisoes})
	require.ErrorIs(t, err, importer.ErrTooManyDecisions)
}

func TestCategoriaPadraoEAplicadaEAComumVence(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	// Categoria de outra casa como padrão é 404, e o lote inteiro volta atrás.
	_, err := a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
		DefaultCategoryID: ptr(a.proximoID("cat-inexistente")),
	})
	require.ErrorIs(t, err, transaction.ErrNotFound)
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
}

func ptr[T any](v T) *T { return &v }

// --- BOLA e ciclo de vida do lote -----------------------------------------

func TestLoteDeOutraCasaNaoEAlcancavel(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	casos := map[string]func() error{
		"preview": func() error {
			_, err := a.svc.Preview(t.Context(), a.atorAlheio(), lote.ID, 0, 100)
			return err
		},
		"confirm": func() error {
			_, err := a.svc.Confirm(t.Context(), a.atorAlheio(), lote.ID, importer.ConfirmInput{})
			return err
		},
		"discard": func() error {
			return a.svc.Discard(t.Context(), a.atorAlheio(), lote.ID)
		},
	}
	for nome, fn := range casos {
		t.Run(nome, func(t *testing.T) {
			require.ErrorIs(t, fn(), importer.ErrBatchNotFound)
		})
	}

	// E o lote continua íntegro para o dono.
	preview, err := a.svc.Preview(t.Context(), a.ator(), lote.ID, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, importer.BatchStatusPending, preview.Batch.Status)
	assert.Len(t, preview.Items, 13)
}

func TestDescartarApagaAsLinhasENaoPermiteConfirmar(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	require.NoError(t, a.svc.Discard(t.Context(), a.ator(), lote.ID))

	preview, err := a.svc.Preview(t.Context(), a.ator(), lote.ID, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, importer.BatchStatusDiscarded, preview.Batch.Status)
	assert.Empty(t, preview.Items)

	_, err = a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{})
	require.ErrorIs(t, err, importer.ErrBatchNotFound, "lote descartado responde 404 no confirm")

	// Descartar de novo também é 404: a transição é condicional.
	require.ErrorIs(t, a.svc.Discard(t.Context(), a.ator(), lote.ID), importer.ErrBatchNotFound)
	assert.Equal(t, []string{"import.created", "import.discarded"}, a.auditoria.acoes())
}

func TestJanitorExpiraLotesVencidosEApagaAsLinhas(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	// Antes do prazo, nada acontece.
	expirados, linhas, lotes, err := a.svc.PurgeExpired(t.Context())
	require.NoError(t, err)
	assert.Zero(t, expirados)
	assert.Zero(t, linhas)
	assert.Zero(t, lotes)

	// O relógio injetado é o que torna as 24 h testáveis sem esperar 24 h.
	a.relogio.avancar(importer.BatchTTL + time.Minute)

	expirados, linhas, lotes, err = a.svc.PurgeExpired(t.Context())
	require.NoError(t, err)
	assert.EqualValues(t, 1, expirados)
	assert.EqualValues(t, 13, linhas, "as linhas de staging somem junto")
	assert.Zero(t, lotes, "o lote sobrevive como histórico por 180 dias")

	preview, err := a.svc.Preview(t.Context(), a.ator(), lote.ID, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, importer.BatchStatusExpired, preview.Batch.Status)
	assert.Empty(t, preview.Items)

	_, err = a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{})
	require.ErrorIs(t, err, importer.ErrBatchNotFound)

	// Passados os 180 dias, o lote terminal também some.
	a.relogio.avancar(importer.TerminalRetention + time.Hour)
	_, _, lotes, err = a.svc.PurgeExpired(t.Context())
	require.NoError(t, err)
	assert.EqualValues(t, 1, lotes)

	_, err = a.svc.Preview(t.Context(), a.ator(), lote.ID, 0, 100)
	require.True(t, errors.Is(err, importer.ErrBatchNotFound))
}

func TestHistoricoSoMostraOsLotesDaCasa(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := a.enviarExtrato(t, conta.ID)

	minha, err := a.svc.ListBatches(t.Context(), a.ator(), 50)
	require.NoError(t, err)
	require.Len(t, minha.Items, 1)
	assert.Equal(t, lote.ID, minha.Items[0].ID)

	// Lote PENDENTE traz a análise por status e `outcome` nulo…
	require.NotNil(t, minha.Items[0].Counts)
	assert.Equal(t, 12, minha.Items[0].Counts.New)
	assert.Nil(t, minha.Items[0].Outcome)

	alheia, err := a.svc.ListBatches(t.Context(), a.atorAlheio(), 50)
	require.NoError(t, err)
	assert.Empty(t, alheia.Items)

	// …e o lote CONFIRMADO traz o contrário: as linhas de staging foram
	// apagadas, então a análise por status deixa de existir.
	_, err = a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{})
	require.NoError(t, err)

	depois, err := a.svc.ListBatches(t.Context(), a.ator(), 50)
	require.NoError(t, err)
	require.Len(t, depois.Items, 1)
	assert.Nil(t, depois.Items[0].Counts)
	require.NotNil(t, depois.Items[0].Outcome)
	assert.Equal(t, 12, depois.Items[0].Outcome.Imported)
}

// TestTaxonomiaVemDeUmaFonteSo trava o par default/ações contra dedup.Status —
// a fonte única da §4.6. Uma segunda tabela aqui divergiria no dia em que um
// status novo entrasse, e divergir quer dizer importar linha que o usuário
// mandou barrar.
func TestTaxonomiaVemDeUmaFonteSo(t *testing.T) {
	t.Parallel()

	todos := []dedup.Status{
		dedup.StatusNew, dedup.StatusRepeatedInFile, dedup.StatusDuplicateExact,
		dedup.StatusDuplicateDeleted, dedup.StatusPossibleDuplicate,
		dedup.StatusCardPayment, dedup.StatusInternalTransfer,
		dedup.StatusTransferAlreadyRegistered, dedup.StatusRejected,
	}
	for _, s := range todos {
		acoes := importer.AllowedActionsFor(s)
		padrao := importer.DefaultActionFor(s)

		switch {
		case s.DefaultImports():
			assert.Equal(t, importer.ActionImport, padrao, "%s", s)
		case s == dedup.StatusTransferAlreadyRegistered:
			// A única exceção nomeada (spec 0005 §4.2.2): o default é `link`,
			// que não cria movimento nenhum — continua verdade que nenhuma
			// linha barrada ENTRA sem decisão explícita.
			assert.Equal(t, importer.ActionLink, padrao, "%s", s)
		default:
			assert.Equal(t, importer.ActionSkip, padrao, "%s", s)
		}

		liberavel := len(acoes) > 0
		assert.Equal(t, s.DefaultImports() || s.Releasable(), liberavel,
			"%s: a lista de ações tem de seguir Releasable()", s)

		if s == dedup.StatusCardPayment || s == dedup.StatusInternalTransfer {
			assert.Contains(t, acoes, importer.ActionTransfer)
		} else {
			assert.NotContains(t, acoes, importer.ActionTransfer,
				"%s: transferência só é oferecida em pagamento de fatura e transferência interna", s)
		}

		if s == dedup.StatusTransferAlreadyRegistered {
			assert.Contains(t, acoes, importer.ActionLink)
			assert.NotContains(t, acoes, importer.ActionImport,
				"importar a perna já registrada seria a duplicata que a spec 0004 impede")
		} else {
			assert.NotContains(t, acoes, importer.ActionLink,
				"%s: link só é oferecido em transferência já registrada", s)
		}
	}
}
