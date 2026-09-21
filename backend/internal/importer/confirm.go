package importer

import (
	"context"
	"errors"
	"fmt"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Fase 2 da importação: a única que escreve em transactions, e escreve TUDO
// dentro de uma transação (S10).
//
// Três invariantes que este arquivo existe para cumprir:
//
//  1. **Nada entra sem confirmação.** Linha marcada só entra com
//     action: "import" explícito. O default de cada status vem de
//     dedup.Status.DefaultImports(), que é a fonte única.
//  2. **O confirm RECALCULA a análise dentro da transação** (§4.8). A prévia é
//     um palpite: entre o envio e a confirmação o outro morador pode ter
//     importado o mesmo arquivo.
//  3. **Idempotente** (S9). A transição pending -> committed é condicional no
//     banco; o segundo clique não encontra mais nada pendente e recebe a mesma
//     resposta, sem importar nada de novo.

// StatementConfirmation é o bloco `statement` do corpo do confirm: competência,
// fechamento e vencimento CONFIRMADOS pelo usuário a partir da sugestão.
type StatementConfirmation struct {
	CompetenceMonth string
	ClosingDate     civil.Date
	DueDate         civil.Date
}

// OptionalCategory é o TRI-ESTADO de `categoryId` numa decisão de importar
// (spec 0005 §4.2.3; mesmo desenho de account.OptionalDay).
//
// Sem ele, `*string` colapsa dois estados que significam coisas opostas:
// "não mexi — use a sugestão da análise" e "quero esta linha SEM categoria
// apesar da sugestão". Colapsados, limpar a sugestão na tela seria impossível
// pela API: o nulo voltaria como "use a sugestão".
type OptionalCategory struct {
	// Set diz que o campo VEIO no corpo da requisição.
	Set bool
	// ID é o valor; nulo com Set verdadeiro significa "sem categoria".
	ID *string
}

// Decision é UMA exceção ao default de uma linha.
//
// Repare no que NÃO existe aqui, e é por construção que não existe (S2 — mass
// assignment): valor, data, descrição, kind e — em `link` — a perna existente.
// Todos vêm do staging, e o corpo não tem onde carregá-los.
type Decision struct {
	RowID  string
	Action string

	// CategoryID só é aceito com ActionImport, e é tri-estado: ausente usa
	// `suggestedCategoryId` (ou `defaultCategoryId`, nesta ordem); nulo
	// explícito grava SEM categoria; valor usa este. Presente — mesmo nulo —
	// com outra ação é 400.
	CategoryID OptionalCategory

	// CounterpartAccountID só existe com ActionTransfer. É a conta da OUTRA
	// perna do par (ADR-016) — ele cria linha numa conta DIFERENTE da do
	// lote, e por isso é o vetor de BOLA mais fácil de esquecer (§6.6).
	// Pode faltar quando a linha tem `suggestedCounterpartAccountId`: o
	// servidor usa a sugerida (que ele mesmo calculou).
	CounterpartAccountID string

	// StatementID é a fatura que este pagamento quita, também só com
	// ActionTransfer.
	StatementID string
}

// ConfirmInput é o corpo do confirm.
type ConfirmInput struct {
	// Decisions carrega APENAS as exceções ao default. Lista vazia é legítima
	// e quer dizer "aceito todos os defaults".
	Decisions []Decision

	// Statement é obrigatório quando o lote é de fatura e proibido em extrato.
	Statement *StatementConfirmation

	// DefaultCategoryID é aplicado a toda linha importada que não traga
	// categoria própria.
	DefaultCategoryID *string
}

// loteBloqueadoError sinaliza que o índice único recusou o lote inteiro.
//
// Ele existe para que a colisão volte como RESPOSTA — "estas linhas não
// entraram" —, e nunca como 500. Dois confirms simultâneos são caso previsto
// pelo ADR-025(b): o índice é o árbitro final, e o resultado certo é o segundo
// confirm reportar as linhas como bloqueadas com o lote INTEIRO desfeito.
//
// Por que o lote inteiro e não linha a linha: num INSERT recusado o PostgreSQL
// aborta a transação, e só um SAVEPOINT por linha permitiria continuar —
// savepoint que o UnitOfWork não expõe, e cujo custo seria pago em TODO arquivo
// para atender a um caso que quase nunca acontece. Metade entrar seria pior do
// que nada entrar: o usuário refaz a revisão e vê o que sobrou.
type loteBloqueadoError struct {
	linhas []BlockedRowView
}

func (e *loteBloqueadoError) Error() string {
	return fmt.Sprintf("lote bloqueado pelo índice único (%d linhas)", len(e.linhas))
}

// errJaConfirmado é a corrida perdida na transição condicional: outra
// requisição confirmou este lote primeiro. Não é erro para o cliente — é o
// caminho da idempotência.
var errJaConfirmado = errors.New("lote já confirmado por outra requisição")

// ErrCounterpartArchived — a conta da OUTRA perna de um `transfer` está
// arquivada (spec 0005 §12). É um erro à parte de ErrAccountArchived porque a
// resposta precisa apontar o campo certo: a conta do lote não está no corpo
// do confirm, e `fields.accountId` mandaria a pessoa desarquivar a conta
// errada. O serviço de lançamentos recusa conta arquivada sem dizer QUAL — por
// isso a contraparte é conferida aqui, antes da escrita. 422, e não 404: a
// conta É da casa, e basta desarquivar. A mensagem não carrega o id.
var ErrCounterpartArchived = errors.New("conta da outra perna arquivada")

// Confirm executa a fase 2.
func (s *Service) Confirm(ctx context.Context, ator Actor, batchID string, in ConfirmInput) (ResultView, error) {
	if ator.HouseholdID == "" || ator.UserID == "" {
		return ResultView{}, ErrBatchNotFound
	}

	// Forma do corpo ANTES de qualquer consulta: um corpo malformado não deve
	// custar a leitura do lote inteiro para ser recusado.
	porLinha, err := indexarDecisoes(in.Decisions)
	if err != nil {
		return ResultView{}, err
	}

	lote, err := s.repo.BatchByID(ctx, ator.HouseholdID, batchID)
	if err != nil {
		return ResultView{}, err
	}

	switch lote.Status {
	case BatchStatusCommitted:
		// Duplo clique em conexão ruim: mesma resposta, nada importado de novo.
		return s.resultadoDeLoteConfirmado(ctx, ator, lote)
	case BatchStatusPending:
		// segue
	default:
		// Descartado ou expirado respondem 404: não se confirma o que já foi
		// jogado fora, e a resposta é a mesma de lote inexistente.
		return ResultView{}, ErrBatchNotFound
	}

	if err := validarBlocoDeFatura(lote, in.Statement); err != nil {
		return ResultView{}, err
	}

	var resultado ResultView
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		r, err := s.confirmar(ctx, ator, lote, in, porLinha)
		if err != nil {
			return err
		}
		resultado = r
		return nil
	})

	switch {
	case err == nil:
		return resultado, nil

	case errors.Is(err, errJaConfirmado):
		// Perdemos a corrida da transição condicional. Nada foi gravado por
		// esta requisição; a resposta certa é a do lote que venceu.
		atual, e := s.repo.BatchByID(ctx, ator.HouseholdID, batchID)
		if e != nil {
			return ResultView{}, e
		}
		if atual.Status != BatchStatusCommitted {
			return ResultView{}, ErrBatchNotFound
		}
		return s.resultadoDeLoteConfirmado(ctx, ator, atual)

	default:
		var bloqueado *loteBloqueadoError
		if errors.As(err, &bloqueado) {
			// A transação inteira voltou atrás: NADA foi gravado. O lote
			// continua pendente, e é isso que a resposta diz — o cliente
			// recarrega a revisão e confirma de novo.
			return s.resultadoBloqueado(lote, bloqueado.linhas), nil
		}
		return ResultView{}, err
	}
}

// confirmar é o miolo, e roda inteiro DENTRO da transação.
func (s *Service) confirmar(ctx context.Context, ator Actor, lote *Batch, in ConfirmInput, porLinha map[string]Decision) (ResultView, error) {
	agora := s.clock()

	// A transição condicional vem PRIMEIRO, e isso é deliberado: ela é o que
	// serializa dois confirms simultâneos, e tomar a linha do lote logo no
	// começo evita fazer todo o trabalho para descobrir no fim que outra
	// requisição já tinha confirmado. Os contadores entram depois, no mesmo
	// registro, quando existirem.
	afetadas, err := s.repo.UpdateBatchStatus(ctx, ator.HouseholdID, lote.ID,
		BatchStatusPending, BatchStatusCommitted, agora, nil)
	if err != nil {
		return ResultView{}, fmt.Errorf("confirmando lote: %w", err)
	}
	if afetadas == 0 {
		return ResultView{}, errJaConfirmado
	}

	linhas, err := s.todasAsLinhas(ctx, ator.HouseholdID, lote.ID)
	if err != nil {
		return ResultView{}, err
	}

	// Toda decisão tem de apontar para uma linha DESTE lote, e a ação tem de
	// estar entre as oferecidas para o status que o cliente VIU. Decisão órfã é
	// 400, nunca ignorada em silêncio (§6.6).
	if err := conferirDecisoes(lote, linhas, porLinha); err != nil {
		return ResultView{}, err
	}

	// RECÁLCULO dentro da transação (§4.8): a análise da fase 1 é um palpite.
	analise, err := s.reanalisar(ctx, lote, linhas)
	if err != nil {
		return ResultView{}, err
	}

	// A fatura nasce antes dos lançamentos porque é o id dela que amarra as
	// linhas — e é a fatura que decide a competência delas (ADR-023b).
	var faturaID *string
	if lote.DocKind == string(DocKindCardStatement) {
		fatura, err := s.statements.Upsert(ctx, ator.statementActor(), cardstatement.UpsertInput{
			AccountID:       lote.AccountID,
			CompetenceMonth: in.Statement.CompetenceMonth,
			ClosingDate:     in.Statement.ClosingDate,
			DueDate:         in.Statement.DueDate,
		})
		if err != nil {
			return ResultView{}, err
		}
		id := fatura.ID
		faturaID = &id
	}

	// A sugestão que a análise gravou pode ter vencido desde então: a
	// categoria foi arquivada, excluída ou ganhou subcategorias (spec 0005
	// §13, "Correção de robustez"). Conferir AQUI, com uma carga por confirm,
	// é o que impede uma sugestão obsoleta de derrubar o lote inteiro com um
	// 422 que o cliente não pediu. O staging NÃO é alterado: o que mudou foi o
	// mundo, não o registro do que a análise viu.
	sugestaoValida, err := s.conferenteDeSugestoes(ctx, ator.HouseholdID, linhas)
	if err != nil {
		return ResultView{}, err
	}

	plano := s.planejar(lote, linhas, analise, porLinha, in, faturaID, sugestaoValida)

	// As contas da OUTRA perna são conferidas aqui, ANTES de qualquer escrita
	// e dentro da mesma transação — e não só pelo serviço de lançamentos, que
	// também as confere: ele recusa conta arquivada sem dizer qual, e a
	// resposta precisa distinguir a contraparte (`counterpartAccountId`, que
	// está no corpo) da conta do lote (`accountId`, que não está). Uma
	// consulta por conta DISTINTA, sempre pela casa do token.
	contasCanonicas, err := s.conferirContrapartes(ctx, ator.HouseholdID, plano.contrapartes)
	if err != nil {
		return ResultView{}, err
	}
	// O que for gravado leva o id CANÔNICO da conta, nunca a string que veio
	// no corpo (ver conferirContrapartes e canonizarContas).
	if err := plano.canonizarContas(lote.AccountID, contasCanonicas); err != nil {
		return ResultView{}, err
	}

	// Restaurar vem antes de inserir: as duas operações são independentes, e a
	// restauração é a que tem contrapartida no índice único — se ela falhar,
	// nada mais precisa ter sido escrito.
	//
	// Uma chamada por linha, e não em lote, e o custo está LIMITADO por
	// construção: restaurar exige decisão EXPLÍCITA (duplicado_excluido é
	// barrado por default), e o corpo do confirm aceita no máximo
	// MaxDecisions decisões. Um arquivo de 10.000 linhas não vira 10.000
	// restaurações; o teto é o do corpo, e não o do arquivo.
	for _, alvo := range plano.restaurar {
		if _, err := s.writer.Restore(ctx, ator.ledgerActor(), alvo.transactionID); err != nil {
			if errors.Is(err, transaction.ErrNotFound) {
				// Alguém restaurou (ou o lançamento sumiu) entre a análise e
				// agora. Não é falha do lote: a linha simplesmente não entrou.
				plano.bloqueadas = append(plano.bloqueadas, BlockedRowView{
					RowID:  alvo.rowID,
					Reason: string(dedup.StatusDuplicateExact),
				})
				plano.restauradas--
				continue
			}
			return ResultView{}, err
		}
	}

	// Vincular vem antes de inserir (spec 0005 §4.2.3, ADR-026f): o `link`
	// escreve numa perna que JÁ EXISTE, e tem o mesmo teto do restaurar — cada
	// vínculo é uma decisão explícita ou o default de uma linha que a análise
	// pareou, nunca mais do que o arquivo tem de linhas.
	//
	// A perna vem do STAGING (gravada pela análise sob o household_id do
	// token), nunca do corpo. O serviço de lançamentos a reconfere dentro
	// desta mesma transação, por casa e pela conta do lote, e cada erro dele
	// tem um destino diferente:
	//   - excluída entre a análise e o confirm → só ESTA linha é bloqueada, o
	//     lote segue (é estado previsto: a pessoa apagou a transferência);
	//   - chave já ocupada → o índice único arbitrou; o lote inteiro volta,
	//     como no CreateBatch;
	//   - não existe na casa / incoerente com a linha → o lote inteiro FALHA
	//     (transação desfeita, 500 genérico, ids no log): é sintoma de staging
	//     adulterado ou de defeito, e nada parcial pode ficar gravado.
	for _, v := range plano.vincular {
		err := s.writer.LinkImport(ctx, ator.ledgerActor(), transaction.LinkImportInput{
			TransactionID: v.transactionID,
			AccountID:     lote.AccountID,
			ExternalID:    v.externalID,
			DedupKey:      v.dedupKey,
			ImportBatchID: lote.ID,
		})
		switch {
		case err == nil:
			continue
		case errors.Is(err, transaction.ErrLinkTargetDeleted):
			plano.bloqueadas = append(plano.bloqueadas, BlockedRowView{
				RowID:  v.rowID,
				Reason: string(dedup.StatusTransferAlreadyRegistered),
			})
			plano.linkadas--
		case transaction.IsBlocked(err):
			return ResultView{}, &loteBloqueadoError{linhas: plano.tentadas}
		default:
			// Só ids na mensagem — nunca descrição, valor ou palavra-chave: o
			// handler a leva ao log.
			return ResultView{}, fmt.Errorf("vinculando a linha %s à perna %s: %w", v.rowID, v.transactionID, err)
		}
	}

	if len(plano.novas) > 0 {
		_, err := s.writer.CreateBatch(ctx, ator.ledgerActor(), transaction.CreateBatchInput{
			Source:        transaction.SourceImport,
			ImportBatchID: &lote.ID,
			Rows:          plano.novas,
		})
		if err != nil {
			if transaction.IsBlocked(err) {
				return ResultView{}, &loteBloqueadoError{linhas: plano.tentadas}
			}
			return ResultView{}, err
		}
	}

	// As linhas de staging são apagadas FISICAMENTE: rascunho com prazo não é
	// dado financeiro, e mantê-lo depois da confirmação só conservaria
	// descrição de terceiros viva sem ninguém para lê-la.
	if _, err := s.repo.DeleteRows(ctx, ator.HouseholdID, lote.ID); err != nil {
		return ResultView{}, fmt.Errorf("apagando linhas do lote confirmado: %w", err)
	}

	// `imported` conta LINHAS DO ARQUIVO que entraram, e não linhas gravadas:
	// a transferência é UMA linha do arquivo que vira DUAS pernas, e contar as
	// duas faria a tela dizer "13 importados" para um arquivo de 12 linhas.
	resultado := ResultView{
		ID:               lote.ID,
		Status:           BatchStatusCommitted,
		Imported:         plano.importadas,
		Restored:         plano.restauradas,
		Skipped:          plano.puladas,
		Blocked:          len(plano.bloqueadas),
		Rejected:         plano.rejeitadas,
		StatementID:      faturaID,
		TransfersCreated: plano.pares,
		Linked:           plano.linkadas,
		BlockedRows:      limitarBloqueadas(plano.bloqueadas),
	}

	outcome := &BatchOutcome{
		ImportedCount: resultado.Imported,
		SkippedCount:  resultado.Skipped,
		BlockedCount:  resultado.Blocked,
		RestoredCount: resultado.Restored,
		RejectedCount: resultado.Rejected,
		// Pares e vínculos são COLUNA desde o schema v4 (ADR-026g): a
		// resposta idempotente os lê do lote, não dos lançamentos — o `link`
		// move o import_batch_id de uma perna existente para este lote, e
		// derivar "pares criados" de import_batch_id mentiria para os dois.
		TransferPairsCount: resultado.TransfersCreated,
		LinkedCount:        resultado.Linked,
	}
	if _, err := s.repo.UpdateBatchStatus(ctx, ator.HouseholdID, lote.ID,
		BatchStatusCommitted, BatchStatusCommitted, agora, outcome); err != nil {
		return ResultView{}, fmt.Errorf("gravando contadores do lote: %w", err)
	}

	// UMA entrada de auditoria para o lote inteiro (§6.10), sem valor nenhum.
	if err := s.registrar(ctx, ator, audit.ActionImportConfirmed, lote.ID); err != nil {
		return ResultView{}, err
	}
	return resultado, nil
}

// restauracao liga a linha do staging ao lançamento existente que ela restaura.
type restauracao struct {
	rowID         string
	transactionID string
}

// vinculo liga a linha do staging à perna de transferência existente que a
// ação `link` vincula (ADR-026f). transactionID é o MatchTransactionID gravado
// pela análise — nunca vindo do corpo.
type vinculo struct {
	rowID         string
	transactionID string
	externalID    *string
	dedupKey      string
}

// planoDeEscrita é o que o confirm decidiu fazer, antes de fazer.
type planoDeEscrita struct {
	novas      []transaction.NewTransaction
	restaurar  []restauracao
	vincular   []vinculo
	bloqueadas []BlockedRowView

	// tentadas são as linhas do arquivo que iriam entrar (ou vincular). Usada
	// só quando o índice único recusa o lote: como NADA entrou, todas elas
	// voltam como bloqueadas.
	tentadas []BlockedRowView

	// contrapartes são as contas da OUTRA perna dos pares planejados — a da
	// decisão ou a sugerida pela análise —, sem repetição e na ordem em que
	// apareceram. confirmar as confere antes de escrever (da casa e ativas).
	// O teto é o de contas por casa, nunca o de linhas do arquivo.
	contrapartes []string

	importadas  int
	restauradas int
	linkadas    int
	puladas     int
	rejeitadas  int
	pares       int
}

// planejar decide, linha a linha, o que acontece — e não escreve nada.
//
// Separar a decisão da escrita é o que torna a regra testável sem banco e o que
// deixa o "tudo ou nada" possível: quando o índice único recusa, o plano diz
// exatamente quais linhas não entraram.
//
// sugestaoValida vem pronto de conferenteDeSugestoes (uma carga por confirm) e
// é consultado só pela categoria SUGERIDA — planejar continua sem tocar no
// banco.
func (s *Service) planejar(
	lote *Batch,
	linhas []Row,
	analise map[int]dedup.RowResult,
	porLinha map[string]Decision,
	in ConfirmInput,
	faturaID *string,
	sugestaoValida categoriaAtribuivel,
) planoDeEscrita {
	var p planoDeEscrita

	for i := range linhas {
		linha := linhas[i]

		if linha.Status == string(dedup.StatusRejected) {
			// Linha malformada não é decidível: ela não produziu valor, data
			// nem descrição, e não há o que inserir.
			p.rejeitadas++
			continue
		}

		veredito, ok := analise[linha.Seq]
		if !ok {
			// Não deveria acontecer: toda linha não rejeitada entra na análise.
			// Se acontecer, a resposta segura é não gravar.
			p.bloqueadas = append(p.bloqueadas, BlockedRowView{RowID: linha.ID, Reason: linha.Status})
			continue
		}

		// O default sai do RECÁLCULO — se a linha piorou desde a prévia, o
		// default piora junto. Com uma exceção deliberada: o recálculo nunca
		// produz `transferencia_ja_registrada` (o pareamento não roda de novo,
		// a perna é a que a análise gravou), então a linha não citada que a
		// revisão mostrou com default `link` mantém o `link` prometido — e o
		// ramo do `link`, abaixo, reconfere o veredito antes de vincular.
		visto := dedup.Status(linha.Status)
		acao := DefaultActionFor(veredito.Status)
		if visto == dedup.StatusTransferAlreadyRegistered {
			acao = DefaultActionFor(visto)
		}
		decisao, citada := porLinha[linha.ID]
		if citada {
			acao = decisao.Action
		}

		if acao == ActionSkip {
			p.puladas++
			continue
		}

		// A partir daqui o RECÁLCULO manda. Ele pode ter piorado desde a
		// prévia — e é exatamente para isso que ele existe.
		switch veredito.Status {
		case dedup.StatusDuplicateExact:
			// Não liberável: o índice único recusaria o INSERT de qualquer
			// jeito. Barrar aqui é dizer a verdade em vez de prometer o
			// impossível.
			p.bloqueadas = append(p.bloqueadas, BlockedRowView{
				RowID:  linha.ID,
				Reason: string(dedup.StatusDuplicateExact),
			})
			continue

		case dedup.StatusDuplicateDeleted:
			if acao != ActionImport || veredito.MatchTransactionID == nil {
				p.bloqueadas = append(p.bloqueadas, BlockedRowView{
					RowID:  linha.ID,
					Reason: string(dedup.StatusDuplicateDeleted),
				})
				continue
			}
			// ADR-025(f): liberar RESTAURA o lançamento existente em vez de
			// inserir outro. A identidade já existe, e "importar de novo" é
			// desfazer a exclusão — a operação toca deleted_at e updated_at, e
			// mais nada.
			p.restaurar = append(p.restaurar, restauracao{rowID: linha.ID, transactionID: *veredito.MatchTransactionID})
			p.restauradas++
			continue
		}

		if acao == ActionLink {
			// A perna é a que a ANÁLISE gravou (MatchTransactionID), e o
			// recálculo precisa continuar dizendo "transferência interna" —
			// sem colisão dura. Qualquer outra coisa bloqueia a linha; o
			// serviço de lançamentos ainda reconfere a perna por casa e conta
			// dentro da transação (ADR-026f).
			if veredito.Status != dedup.StatusInternalTransfer || linha.MatchTransactionID == nil ||
				*linha.MatchTransactionID == "" {
				p.bloqueadas = append(p.bloqueadas, BlockedRowView{
					RowID:  linha.ID,
					Reason: string(dedup.StatusTransferAlreadyRegistered),
				})
				continue
			}
			p.vincular = append(p.vincular, vinculo{
				rowID:         linha.ID,
				transactionID: *linha.MatchTransactionID,
				externalID:    linha.ExternalID,
				dedupKey:      veredito.DedupKey,
			})
			p.tentadas = append(p.tentadas, BlockedRowView{
				RowID:  linha.ID,
				Reason: string(dedup.StatusTransferAlreadyRegistered),
			})
			p.linkadas++
			continue
		}

		if acao == ActionTransfer {
			if veredito.Status != dedup.StatusCardPayment && veredito.Status != dedup.StatusInternalTransfer {
				// A linha deixou de ser classificada como pagamento de fatura
				// ou transferência interna no recálculo: criar o par agora
				// inventaria uma transferência que a revisão não mostrou.
				p.bloqueadas = append(p.bloqueadas, BlockedRowView{RowID: linha.ID, Reason: string(veredito.Status)})
				continue
			}
			contraparte := contraparteDaLinha(decisao, linha)
			if contraparte == "" {
				// conferirDecisoes já exigiu contraparte ou sugestão; se a
				// sugestão sumiu do staging, a resposta segura é não gravar.
				p.bloqueadas = append(p.bloqueadas, BlockedRowView{RowID: linha.ID, Reason: string(veredito.Status)})
				continue
			}
			saida, entrada := s.pernasDaTransferencia(lote, linha, veredito, decisao, contraparte)
			p.novas = append(p.novas, saida, entrada)
			p.lembrarContraparte(contraparte)
			p.tentadas = append(p.tentadas, BlockedRowView{RowID: linha.ID, Reason: string(veredito.Status)})
			p.importadas++
			p.pares++
			continue
		}

		p.novas = append(p.novas, transaction.NewTransaction{
			Kind:      linha.Kind,
			AccountID: lote.AccountID,
			CategoryID: categoriaDaLinha(decisao, citada, linha.SuggestedCategoryID,
				in.DefaultCategoryID, linha.Kind, sugestaoValida),
			AmountCents: linha.AmountCents,
			Description: linha.Description,
			OccurredOn:  linha.OccurredOn,
			// Toda linha de uma FATURA pertence à fatura: é o statement_id que
			// desloca a competência para o mês do vencimento (ADR-023b), e é
			// ele que faz a compra de 28/01 aparecer no mês em que ela será
			// paga.
			StatementID: faturaID,
			ExternalID:  linha.ExternalID,
			DedupKey:    veredito.DedupKey,
		})
		p.tentadas = append(p.tentadas, BlockedRowView{RowID: linha.ID, Reason: string(veredito.Status)})
		p.importadas++
	}

	return p
}

// lembrarContraparte registra a conta da outra perna, uma vez só por conta.
// Busca linear de propósito: o conjunto é limitado pelo número de contas da
// casa, e um mapa auxiliar custaria mais do que ele.
func (p *planoDeEscrita) lembrarContraparte(accountID string) {
	for _, id := range p.contrapartes {
		if id == accountID {
			return
		}
	}
	p.contrapartes = append(p.contrapartes, accountID)
}

// conferirContrapartes garante que cada conta da outra perna é DA CASA DO
// TOKEN e está ativa, antes de o par ser gravado — e devolve, para cada id
// PEDIDO, o id CANÔNICO que o banco reconheceu.
//
// A tradução do erro segue contaDeDestino: conta de outra casa é o MESMO
// ErrAccountNotFound de conta inexistente (404, S1) — a resposta nunca
// confirma a existência do recurso alheio. Arquivada é ErrCounterpartArchived
// (422 em `fields.counterpartAccountId`), e não ErrAccountArchived, que
// aponta a conta do lote. O serviço de lançamentos repete a conferência
// dentro da mesma transação (defesa em profundidade); esta existe para a
// resposta apontar o campo certo.
//
// ⚠️ O MAPA não é conveniência: a contraparte é o ÚNICO id de conta do confirm
// que vem do corpo da requisição, e ele chega sem trim e sem teto de tamanho.
// `WHERE id = ?` casa `"<uuid>   "` no MSSQL (padding ANSI) e `"<UUID>"` no
// MySQL 8 (`utf8mb4_0900_ai_ci`), então a conta existe, o par é aceito — e a
// perna seria GRAVADA com a string do cliente, que nenhum mapa em Go depois
// encontra. O que vale daqui para a frente é `c.ID`.
func (s *Service) conferirContrapartes(ctx context.Context, householdID string, ids []string) (map[string]string, error) {
	canonicas := make(map[string]string, len(ids))
	for _, id := range ids {
		c, err := s.accounts.ByID(ctx, householdID, id)
		if err != nil {
			if errors.Is(err, account.ErrNotFound) {
				return nil, ErrAccountNotFound
			}
			return nil, fmt.Errorf("buscando a conta da outra perna: %w", err)
		}
		if c.ArchivedAt != nil {
			return nil, ErrCounterpartArchived
		}
		canonicas[id] = c.ID
	}
	return canonicas, nil
}

// canonizarContas troca, em cada linha planejada, o id de conta PEDIDO pelo
// CANÔNICO que o banco devolveu — e só depois disso reconfere que nenhum par
// ficou com as duas pernas na mesma conta.
//
// A ordem importa e é o ponto deste método. `conferirDecisoes` já recusa
// contraparte igual à conta do lote (ErrSameAccountTransfer, 400 no campo),
// mas compara STRINGS: com a caixa trocada, `"<UUID-do-lote>"` não é igual a
// `"<uuid-do-lote>"` em Go e passa — enquanto o `ByID` do MySQL resolve os
// dois para a mesma conta. Sem esta reconferência, a canonização
// TRANSFORMARIA um pedido que a borda recusava num par de pernas na mesma
// conta, gravado em silêncio. O erro é o MESMO da borda, para a resposta não
// mudar de forma conforme o dialeto.
func (p *planoDeEscrita) canonizarContas(contaDoLote string, canonicas map[string]string) error {
	for i := range p.novas {
		if canonica, ok := canonicas[p.novas[i].AccountID]; ok {
			p.novas[i].AccountID = canonica
		}
	}
	for i := range p.contrapartes {
		if canonica, ok := canonicas[p.contrapartes[i]]; ok {
			if canonica == contaDoLote {
				return ErrSameAccountTransfer
			}
			p.contrapartes[i] = canonica
		}
	}
	return nil
}

// pernasDaTransferencia monta o PAR (ADR-016) a partir de uma linha de
// pagamento de fatura.
//
// Qual conta é a de saída depende do documento, e errar isso inverteria o
// dinheiro:
//
//   - no EXTRATO, "Pagamento de fatura" é uma DESPESA: sai da conta do lote e
//     entra no cartão (a contraparte);
//   - na FATURA, "Pagamento recebido" é uma RECEITA no cartão: sai da
//     contraparte (a conta corrente) e entra na conta do lote.
//
// A chave de deduplicação segue a mesma lógica da §4.4: a perna que veio do
// ARQUIVO fica com a chave que a análise calculou para ela, e a perna
// SINTÉTICA — a que não existe em documento nenhum — recebe PairKey, que é
// única por construção e nunca barra nada.
//
// A fatura (statementId) vai sempre na perna de ENTRADA: pagar a fatura é uma
// transferência, e é a perna que entra no cartão que quita (ADR-023d).
//
// contraparte é a conta da OUTRA perna já resolvida (a da decisão ou a
// sugerida pela análise — contraparteDaLinha). Ela é validada como da casa
// pelo serviço de lançamentos, dentro da transação (404, tudo ou nada).
func (s *Service) pernasDaTransferencia(lote *Batch, linha Row, veredito dedup.RowResult, decisao Decision, contraparte string) (saida, entrada transaction.NewTransaction) {
	grupo := s.ids()
	chaveSintetica := dedup.PairKey(grupo)

	var faturaDaEntrada *string
	if decisao.StatementID != "" {
		id := decisao.StatementID
		faturaDaEntrada = &id
	}

	saida = transaction.NewTransaction{
		Kind:            transaction.KindTransferOut,
		AmountCents:     linha.AmountCents,
		Description:     linha.Description,
		OccurredOn:      linha.OccurredOn,
		TransferGroupID: &grupo,
	}
	entrada = transaction.NewTransaction{
		Kind:            transaction.KindTransferIn,
		AmountCents:     linha.AmountCents,
		Description:     linha.Description,
		OccurredOn:      linha.OccurredOn,
		TransferGroupID: &grupo,
		StatementID:     faturaDaEntrada,
	}

	if linha.Kind == transaction.KindExpense {
		// Extrato: o dinheiro sai da conta do lote.
		saida.AccountID = lote.AccountID
		saida.ExternalID = linha.ExternalID
		saida.DedupKey = veredito.DedupKey
		entrada.AccountID = contraparte
		entrada.DedupKey = chaveSintetica
		return saida, entrada
	}

	// Fatura (ou receita no extrato): o dinheiro entra na conta do lote.
	entrada.AccountID = lote.AccountID
	entrada.ExternalID = linha.ExternalID
	entrada.DedupKey = veredito.DedupKey
	saida.AccountID = contraparte
	saida.DedupKey = chaveSintetica
	return saida, entrada
}

// contraparteDaLinha resolve a conta da outra perna de um `transfer`: a da
// decisão quando veio; senão a SUGERIDA pela análise (spec 0005 §4.2.3).
// Vazio quando não há nenhuma — conferirDecisoes já terá recusado.
func contraparteDaLinha(decisao Decision, linha Row) string {
	if decisao.CounterpartAccountID != "" {
		return decisao.CounterpartAccountID
	}
	if linha.SuggestedCounterpartAccountID != nil {
		return *linha.SuggestedCounterpartAccountID
	}
	return ""
}

// categoriaAtribuivel responde se a categoria SUGERIDA pela análise ainda pode
// receber um lançamento daquela natureza AGORA, no momento do confirm.
//
// NULO significa "não há como conferir" — nenhuma linha do lote trouxe
// sugestão, ou o serviço foi montado sem classificador. Nesse caso a SUGESTÃO
// é DESCARTADA (achado B4): sem conferente não há como afirmar que a categoria
// sugerida ainda pode receber lançamento, e o lado seguro do erro em dado
// financeiro é "sem categoria", que é visível na tela e corrigível em um
// clique. O conferente vem pronto de conferenteDeSugestoes, que carrega o
// conjunto UMA vez por confirm.
type categoriaAtribuivel func(categoryID, kind string) bool

// categoriaDaLinha escolhe a categoria com a precedência da spec 0005 §4.2.3:
//
//	decisão com valor > decisão nula (SEM categoria) > sugestão da linha >
//	defaultCategoryId do corpo > nenhuma
//
// "Nenhuma" é um estado legítimo e esperado (D3): lançamento importado nasce
// sem categoria, e a tela mostra "N lançamentos sem categoria". O nulo
// EXPLÍCITO da decisão vence a sugestão E o default: é o único jeito de a
// pessoa dizer "esta linha fica sem categoria apesar da sugestão".
//
// A SUGESTÃO — e só ela — é conferida contra o mundo de agora (spec 0005 §13,
// "Correção de robustez"): arquivar, excluir ou dar uma subcategoria à
// categoria sugerida entre a análise e o confirm degrada AQUELA linha para
// "sem categoria", em vez de derrubar o lote inteiro com um 422 que o cliente
// não pediu. A degradação NÃO cai no `defaultCategoryId`: o default é a
// escolha da pessoa para as linhas que a análise não classificou, e enterrar
// nele uma sugestão que venceu esconderia o fato. A linha sai em "sem
// categoria", visível, e a pessoa recategoriza em /lancamentos.
//
// O que o CLIENTE mandou — `decisions[].categoryId` e `defaultCategoryId` — não
// é degradado: continua indo ao serviço de lançamentos e continua virando 422.
func categoriaDaLinha(decisao Decision, citada bool, sugerida, padrao *string, kind string, valida categoriaAtribuivel) *string {
	if citada && decisao.CategoryID.Set {
		if decisao.CategoryID.ID == nil {
			return nil
		}
		id := *decisao.CategoryID.ID
		return &id
	}
	if sugerida != nil && *sugerida != "" {
		id := *sugerida
		if valida != nil && valida(id, kind) {
			return &id
		}
		// Sugestão obsoleta — ou sem conferente para dizer que ela ainda vale
		// (achado B4 da revisão de segurança): a linha entra SEM categoria, e
		// não com o default.
		//
		// `valida == nil` significa "não há como conferir" e ERRA PARA SEM
		// CATEGORIA, em vez de aceitar. Aceitar era o comportamento anterior e
		// dependia de uma coincidência: conferenteDeSugestoes só devolve nil
		// quando nenhuma linha trouxe sugestão (e então este ramo é
		// inalcançável) ou quando o serviço foi montado sem classificador — e
		// nesse segundo caso a sugestão veio de um staging escrito por outra
		// montagem, que é exatamente a hora de desconfiar dela.
		return nil
	}
	if padrao != nil && *padrao != "" {
		id := *padrao
		return &id
	}
	return nil
}

// conferenteDeSugestoes carrega, UMA vez por confirm, as categorias da casa que
// ainda podem receber lançamento, e devolve o conferente das sugestões.
//
// Três economias e uma garantia:
//
//   - ZERO consulta quando nenhuma linha do lote trouxe sugestão (o caso comum
//     de uma casa sem palavras-chave cadastradas);
//   - quatro consultas — o Load do classificador, o mesmo da análise — quando
//     alguma trouxe. NUNCA uma por linha: o conjunto é um mapa em memória, e
//     um extrato de 400 linhas custa o mesmo que uma;
//   - roda DENTRO da transação do confirm, então o conjunto é o mesmo estado
//     que o serviço de lançamentos vai ver ao gravar — conferir fora dela
//     deixaria a janela aberta de novo;
//   - o conjunto é carregado pela casa do TOKEN (householdID), nunca por id
//     vindo do corpo: categoria de outra casa simplesmente não está lá e a
//     sugestão que a apontasse seria degradada.
func (s *Service) conferenteDeSugestoes(ctx context.Context, householdID string, linhas []Row) (categoriaAtribuivel, error) {
	if s.classifier == nil {
		// Sem classificador nenhuma análise deste serviço grava sugestão; a
		// guarda é a mesma defesa em profundidade de classificar(), já que o
		// construtor exige o classificador.
		return nil, nil
	}
	temSugestao := false
	for i := range linhas {
		if linhas[i].SuggestedCategoryID != nil && *linhas[i].SuggestedCategoryID != "" {
			temSugestao = true
			break
		}
	}
	if !temSugestao {
		return nil, nil
	}

	conjunto, err := s.classifier.Load(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("carregando categorias atribuíveis da casa: %w", err)
	}
	return func(categoryID, kind string) bool {
		natureza, ok := conjunto.AssignableKind(categoryID)
		if !ok {
			// Arquivada, excluída, grupo com subcategoria ativa ou de outra
			// casa: nas quatro a resposta é a mesma.
			return false
		}
		return categoriaCombinaComALinha(kind, natureza)
	}, nil
}

// categoriaCombinaComALinha aplica, para a SUGESTÃO, a mesma regra que o
// serviço de lançamentos aplica a toda categoria (ErrCategoryKindMismatch) —
// DELEGANDO para category.AceitaLancamento, a única fonte da verdade
// (ADR-029b).
//
// Ela existia com a regra escrita à mão, cópia gêmea de transaction.
// categoriaCombina, e era a esquecida das duas: seria por esta porta que uma
// despesa importada ganharia categoria de resgate. Despesa aceita `expense` ou
// `investment`; receita, `income` ou `redemption`; perna de transferência não
// recebe categoria nenhuma.
//
// A conferência acontece aqui porque a natureza de uma categoria PODE mudar
// entre a análise e a confirmação — e, desde o ADR-029c, pode mudar até com a
// categoria em uso, dentro do mesmo lado do dinheiro. Uma troca de lado
// (`expense → income`) entre os dois momentos invalida a sugestão, e é esta
// função que a descarta.
func categoriaCombinaComALinha(kindDaLinha, kindDaCategoria string) bool {
	return category.AceitaLancamento(kindDaLinha, kindDaCategoria)
}

// reanalisar refaz a classificação sobre as linhas JÁ GRAVADAS no staging.
//
// Sobre as gravadas, e não sobre o arquivo: o arquivo não existe mais. É essa
// propriedade que torna a análise à prova de adulteração — o cliente não
// devolve valores, ele devolve decisões (§5.5).
func (s *Service) reanalisar(ctx context.Context, lote *Batch, linhas []Row) (map[int]dedup.RowResult, error) {
	entrada := make([]dedup.Row, 0, len(linhas))
	for i := range linhas {
		if linhas[i].Status == string(dedup.StatusRejected) {
			continue
		}
		entrada = append(entrada, dedup.Row{
			Seq:             linhas[i].Seq,
			Kind:            linhas[i].Kind,
			OccurredOn:      linhas[i].OccurredOn,
			AmountCents:     linhas[i].AmountCents,
			DescriptionNorm: linhas[i].DescriptionNorm,
			ExternalID:      linhas[i].ExternalID,
			// O staging não guarda a sugestão do parser; ela sobreviveu como
			// STATUS. Reconstruí-la daqui é o que mantém "pagamento de fatura"
			// classificado como tal no recálculo — sem isso, a única liberação
			// que cria transferência desapareceria entre a prévia e o confirm.
			IsCardPayment: linhas[i].Status == string(dedup.StatusCardPayment),
			// O mesmo passthrough para a marcação por palavra-chave (spec
			// 0005): os dois status de transferência voltam como
			// `transferencia_interna` no recálculo — a colisão dura continua
			// vencendo. O pareamento com a perna NÃO roda de novo: a perna do
			// `link` é a que a análise gravou em MatchTransactionID.
			IsInternalTransfer: linhas[i].Status == string(dedup.StatusInternalTransfer) ||
				linhas[i].Status == string(dedup.StatusTransferAlreadyRegistered),
		})
	}

	res, err := s.analisar(ctx, lote.HouseholdID, lote.AccountID, lote.Institution,
		entrada, lote.MinDate, lote.MaxDate)
	if err != nil {
		return nil, err
	}

	porSeq := make(map[int]dedup.RowResult, len(res.Rows))
	for i := range res.Rows {
		porSeq[res.Rows[i].Seq] = res.Rows[i]
	}
	return porSeq, nil
}

// todasAsLinhas lê o lote inteiro, paginado pelo seq.
//
// O teto de páginas é o mesmo teto de linhas de um arquivo: um laço sem teto
// aqui viraria laço infinito se o repositório algum dia devolvesse página
// repetida.
func (s *Service) todasAsLinhas(ctx context.Context, householdID, batchID string) ([]Row, error) {
	const pagina = 500

	out := make([]Row, 0, pagina)
	depoisDe := 0
	for len(out) <= dedup.MaxFileRows {
		linhas, err := s.repo.ListRows(ctx, householdID, batchID, depoisDe, pagina)
		if err != nil {
			return nil, fmt.Errorf("lendo linhas do lote: %w", err)
		}
		if len(linhas) == 0 {
			return out, nil
		}
		out = append(out, linhas...)
		depoisDe = linhas[len(linhas)-1].Seq
	}
	return nil, fmt.Errorf("lote com mais linhas do que o teto de %d", dedup.MaxFileRows)
}

// indexarDecisoes confere a FORMA de cada decisão e devolve o índice por linha.
//
// Tudo o que é conferido aqui depende só do corpo; o que depende do lote (a
// linha existe? a ação é oferecida para o status dela?) é conferido depois, com
// as linhas em mãos.
func indexarDecisoes(decisoes []Decision) (map[string]Decision, error) {
	if len(decisoes) > MaxDecisions {
		return nil, fmt.Errorf("%w: máximo de %d", ErrTooManyDecisions, MaxDecisions)
	}

	out := make(map[string]Decision, len(decisoes))
	for _, d := range decisoes {
		if d.RowID == "" {
			return nil, ErrRowNotInBatch
		}
		if _, repetida := out[d.RowID]; repetida {
			return nil, ErrDuplicateDecision
		}
		if !ValidAction(d.Action) {
			return nil, ErrActionNotAllowed
		}

		// `categoryId` PRESENTE — mesmo nulo — só existe em `import`. Em
		// `transfer`, categorizar uma perna faria o relatório contar como
		// gasto um dinheiro que só mudou de bolso (ADR-016); em `link` e
		// `skip` não há o que categorizar.
		if d.Action != ActionImport && d.CategoryID.Set {
			return nil, ErrCategoryOnlyOnImport
		}

		switch d.Action {
		case ActionTransfer:
			// A contraparte NÃO é exigida aqui: ela depende da linha (em
			// `transferencia_interna` o servidor usa a sugerida). Quem cobra é
			// conferirDecisoes, com o staging em mãos.
		default:
			// `import`, `skip` e `link`: a outra perna e a fatura só existem
			// em transferência. Em `link` em particular, a perna existente é
			// a do staging — o corpo não a escolhe.
			if d.CounterpartAccountID != "" {
				return nil, ErrCounterpartNotAllowed
			}
			if d.StatementID != "" {
				return nil, ErrStatementOnlyOnTransfer
			}
		}
		out[d.RowID] = d
	}
	return out, nil
}

// conferirDecisoes liga cada decisão à linha dela e confere a ação contra o
// status que o cliente VIU.
//
// Contra o status do staging, e não contra o recalculado, de propósito: o 400 é
// sobre o que o cliente pediu, e ele pediu olhando a prévia. O recálculo decide
// outra coisa — se a linha ENTRA —, e o resultado disso é "bloqueada", não erro
// de requisição.
func conferirDecisoes(lote *Batch, linhas []Row, porLinha map[string]Decision) error {
	if len(porLinha) == 0 {
		return nil
	}

	porID := make(map[string]Row, len(linhas))
	for i := range linhas {
		porID[linhas[i].ID] = linhas[i]
	}

	for rowID, d := range porLinha {
		linha, ok := porID[rowID]
		if !ok {
			// Linha de OUTRO lote (ou inexistente). 400, nunca ignorada em
			// silêncio: ignorar faria o cliente acreditar que a decisão dele
			// valeu.
			return fmt.Errorf("%w: %s", ErrRowNotInBatch, "decisão fora do lote")
		}
		if !acaoOferecida(dedup.Status(linha.Status), d.Action) {
			// É o que faz `link` em linha `novo` (ou `import` em
			// `transferencia_ja_registrada`) ser 400 — critério 8 da spec 0005.
			return fmt.Errorf("%w: %s", ErrActionNotAllowed, d.Action)
		}
		if d.Action == ActionTransfer {
			contraparte := contraparteDaLinha(d, linha)
			if contraparte == "" {
				// Sem contraparte na decisão E sem sugestão da análise não há
				// par possível. Em `pagamento_de_fatura` sem conta batendo é o
				// caso normal: a pessoa escolhe o cartão.
				return ErrCounterpartRequired
			}
			if contraparte == lote.AccountID {
				// A outra perna na MESMA conta seria um no-op que ainda
				// dobraria a linha no extrato daquela conta. O domínio de
				// lançamentos também barra (ErrBrokenTransfer); barrar aqui dá
				// ao cliente o campo exato em vez de um erro sobre "o par". A
				// sugestão nunca é a conta do lote (o classificador a exclui),
				// mas a conferência é sobre a contraparte RESOLVIDA.
				return ErrSameAccountTransfer
			}
		}
	}
	return nil
}

// acaoOferecida confere a ação contra AllowedActionsFor — a lista derivada da
// taxonomia, nunca uma segunda tabela.
func acaoOferecida(status dedup.Status, acao string) bool {
	for _, permitida := range AllowedActionsFor(status) {
		if permitida == acao {
			return true
		}
	}
	return false
}

// validarBlocoDeFatura cobra o `statement` onde ele é obrigatório e o recusa
// onde ele não faz sentido.
//
// A condição depende do LOTE, e não do corpo, então ela não cabe no `required`
// do OpenAPI: está cobrada aqui e testada.
func validarBlocoDeFatura(lote *Batch, s *StatementConfirmation) error {
	ehFatura := lote.DocKind == string(DocKindCardStatement)
	switch {
	case ehFatura && s == nil:
		return ErrStatementRequired
	case !ehFatura && s != nil:
		return ErrStatementNotAllowed
	case !ehFatura:
		return nil
	}
	// A conferência fina (fechamento antes do vencimento, competência igual ao
	// mês do vencimento) é do domínio de faturas, e acontece no Upsert dentro
	// da transação.
	return cardstatement.ValidateDates(s.CompetenceMonth, s.ClosingDate, s.DueDate)
}

// limitarBloqueadas corta a lista ao teto publicado no contrato (maxItems
// 2000). O CONTADOR continua completo: o que é cortado é a lista, não o número.
func limitarBloqueadas(linhas []BlockedRowView) []BlockedRowView {
	if linhas == nil {
		return []BlockedRowView{}
	}
	if len(linhas) > MaxBlockedRowsReported {
		return linhas[:MaxBlockedRowsReported]
	}
	return linhas
}

// resultadoBloqueado é a resposta quando o índice único recusou o lote inteiro.
//
// O lote continua PENDENTE e o resultado diz `imported: 0`: nada entrou, e o
// cliente recarrega a revisão. É a resposta certa para uma corrida — nunca 500,
// e nunca metade do arquivo gravado.
func (s *Service) resultadoBloqueado(lote *Batch, bloqueadas []BlockedRowView) ResultView {
	return ResultView{
		ID:          lote.ID,
		Status:      BatchStatusPending,
		Blocked:     len(bloqueadas),
		Rejected:    lote.RejectedCount,
		BlockedRows: limitarBloqueadas(bloqueadas),
	}
}

// resultadoDeLoteConfirmado reconstrói a resposta de um lote que já foi
// confirmado — o caminho da idempotência.
//
// Os contadores vêm do lote — `transfersCreated` e `linked` inclusive, que
// são coluna desde o schema v4 (ADR-026g: a ação `link` tornou a derivação
// por import_batch_id mentirosa). Só `statementId` continua DERIVADO dos
// lançamentos que o lote gerou (ImportBatchFootprint), pela disciplina do
// ADR-017.
//
// `blockedRows` volta VAZIA, e isso é honesto: as linhas de staging foram
// apagadas fisicamente no commit, então os ids delas não existem mais. O
// CONTADOR `blocked` continua correto, que é o número que a tela mostra.
func (s *Service) resultadoDeLoteConfirmado(ctx context.Context, ator Actor, lote *Batch) (ResultView, error) {
	pegada, err := s.ledger.ImportBatchFootprint(ctx, ator.HouseholdID, lote.ID)
	if err != nil {
		return ResultView{}, fmt.Errorf("lendo o rastro do lote: %w", err)
	}
	return ResultView{
		ID:               lote.ID,
		Status:           lote.Status,
		Imported:         lote.ImportedCount,
		Restored:         lote.RestoredCount,
		Skipped:          lote.SkippedCount,
		Blocked:          lote.BlockedCount,
		Rejected:         lote.RejectedCount,
		StatementID:      pegada.StatementID,
		TransfersCreated: lote.TransferPairsCount,
		Linked:           lote.LinkedCount,
		BlockedRows:      []BlockedRowView{},
	}, nil
}
