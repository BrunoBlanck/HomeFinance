package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer/archive"
	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/importer/sanitize"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
)

// Fase 1 da importação (ADR-024c): extrair, detectar, parsear, deduplicar e
// gravar um RASCUNHO. Nenhum lançamento é criado aqui — quem grava em
// transactions é o confirm, e só ele.

// zipMagic são os magic bytes do cabeçalho local de um ZIP.
//
// É por ELES, e nunca pela extensão nem pelo Content-Type da parte do
// multipart, que o tipo real do arquivo é decidido: os dois últimos são texto
// escolhido pelo cliente, e um ".csv" que na verdade é um ZIP cifrado
// responderia "arquivo binário" em vez de pedir a senha.
var zipMagic = []byte{'P', 'K', 0x03, 0x04}

// IsZIP informa se os bytes começam com o cabeçalho local de um ZIP.
func IsZIP(b []byte) bool { return bytes.HasPrefix(b, zipMagic) }

// AnalyzeInput é o pedido da fase 1.
//
// Repare no que NÃO existe: householdId, userId e qualquer contador. A casa e o
// usuário vêm do Actor, ou seja, do token.
type AnalyzeInput struct {
	// AccountID é a conta de DESTINO, conferida como da casa.
	AccountID string

	// FileName é o nome que o cliente mandou. É sanitizado antes de ser
	// guardado, e NUNCA é regra: ele não escolhe o parser e não decide a
	// competência da fatura. Nome de arquivo é entrada do cliente, e uma
	// fatura arquivada no mês errado estraga a competência de dezenas de
	// linhas (§5.3).
	FileName string

	// Content são os bytes enviados: CSV solto ou ZIP.
	Content []byte

	// Password é a senha do ZIP, quando houver.
	//
	// ⚠️ CONTRATO: Analyze toma posse do slice e o ZERA antes de retornar, em
	// todos os caminhos, inclusive nos de erro. Ele nunca vira string, nunca
	// entra em log, em auditoria, no banco, em mensagem de erro ou em resposta.
	// É o CPF do titular — dado pessoal, não apenas credencial (§6.3).
	Password []byte

	// FormatID é o desempate manual, necessário APENAS depois de um
	// IMPORT_FORMAT_AMBIGUOUS. Vazio deixa a detecção decidir.
	FormatID string
}

// Analyze executa a fase 1 e devolve o lote em staging.
func (s *Service) Analyze(ctx context.Context, ator Actor, in AnalyzeInput) (BatchView, error) {
	// A senha morre aqui, aconteça o que acontecer. archive.Extract também a
	// zera — zerar duas vezes um slice já zerado não custa nada, e é o preço
	// de nenhum caminho de erro deixá-la viva.
	defer archive.Zero(in.Password)

	if ator.HouseholdID == "" || ator.UserID == "" {
		return BatchView{}, ErrBatchNotFound
	}
	if len(in.Content) == 0 {
		return BatchView{}, fmt.Errorf("%w: arquivo vazio", csvtext.ErrEmpty)
	}
	if len(in.Content) > MaxUploadBytes {
		return BatchView{}, fmt.Errorf("%w: passa de %d bytes", archive.ErrTooLarge, MaxUploadBytes)
	}

	// O prazo próprio existe para a análise MORRER ANTES do WriteTimeout do
	// servidor: sem ele, o cliente recebe uma conexão cortada em vez de uma
	// resposta, e o lote fica gravado sem ninguém saber que ele existe.
	//
	// ⚠️ Quem impõe o prazo é ESTA função, e por isso é ela — e as paradas
	// voluntárias espalhadas pelos estágios abaixo — quem tem o direito de
	// dizer "a análise demorou demais" (ErrAnalyzeTimeout). A borda NÃO deduz
	// mais isso do contexto: ver o comentário de ErrAnalyzeTimeout em types.go.
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	if err := conferirPrazo(ctx, "abrindo a análise"); err != nil {
		return BatchView{}, err
	}

	conta, err := s.contaDeDestino(ctx, ator.HouseholdID, in.AccountID)
	if err != nil {
		return BatchView{}, err
	}

	// --- contêiner: ZIP (com ou sem senha) ou CSV solto -------------------
	if err := conferirPrazo(ctx, "abrindo o contêiner"); err != nil {
		return BatchView{}, err
	}
	nomeInterno := ""
	bruto := in.Content
	if IsZIP(in.Content) {
		entrada, err := archive.Extract(in.Content, in.Password)
		if err != nil {
			return BatchView{}, err
		}
		nomeInterno, bruto = entrada.Name, entrada.Data
	}

	// --- conteúdo normalizado: é sobre ELE que o hash é calculado ---------
	texto, codificacao, err := normalizarConteudo(bruto)
	if err != nil {
		return BatchView{}, err
	}
	soma := sha256.Sum256([]byte(texto))
	hashConteudo := hex.EncodeToString(soma[:])

	// --- leitura -----------------------------------------------------------
	if err := conferirPrazo(ctx, "lendo o documento"); err != nil {
		return BatchView{}, err
	}
	res, err := s.registry.Parse(ctx, []byte(texto), in.FormatID, s.limits)
	if err != nil {
		return BatchView{}, err
	}

	if err := travaDeConsistencia(conta, res.Institution, res.DocKind); err != nil {
		return BatchView{}, err
	}

	// --- deduplicação ------------------------------------------------------
	linhasDedup := make([]dedup.Row, 0, len(res.Rows))
	for i := range res.Rows {
		linhasDedup = append(linhasDedup, dedupRowDe(res.Rows[i]))
	}

	// --- classificação por palavra-chave (spec 0005 §4.2.1) ---------------
	//
	// ANTES da deduplicação, porque ela só produz marcações (IsInternalTransfer)
	// que a deduplicação ordena junto com as outras: colisão dura vence,
	// pagamento de fatura vence, e só então a transferência interna.
	if err := conferirPrazo(ctx, "classificando linhas"); err != nil {
		return BatchView{}, err
	}
	sugestoes, err := s.classificar(ctx, ator.HouseholdID, conta.ID, res.Rows, linhasDedup)
	if err != nil {
		return BatchView{}, err
	}

	if err := conferirPrazo(ctx, "deduplicando linhas"); err != nil {
		return BatchView{}, err
	}
	analise, err := s.analisar(ctx, ator.HouseholdID, in.AccountID, string(res.Institution),
		linhasDedup, res.MinDate, res.MaxDate)
	if err != nil {
		return BatchView{}, err
	}

	// --- pareamento com a perna já existente (§4.2.1.2 + emenda §10.2) ----
	//
	// DEPOIS da deduplicação e ANTES de montar as linhas: só as que ficaram
	// `transferencia_interna` (ou `pagamento_de_fatura` com contraparte
	// sugerida) procuram a perna. Só acontece aqui, na fase 1: o confirm não
	// pareia de novo — a perna do `link` é a que a análise gravou no staging.
	if err := s.parearComPernasExistentes(ctx, ator.HouseholdID, conta.ID, res, &analise, sugestoes); err != nil {
		return BatchView{}, err
	}

	// --- aviso "você já importou este arquivo" -----------------------------
	//
	// A MESMA função que a revisão usa (§4.1): o aviso nasce aqui e precisa
	// sobreviver até GET /imports/{id}, que é de onde a tela de revisão lê o
	// lote — dois cálculos separados foi como ele morreu antes. É AVISO, nunca
	// bloqueio (ADR-025d): o hash é frouxo (uma linha nova muda tudo) e
	// rigoroso ao mesmo tempo (travaria quem reimporta de propósito para pegar
	// as três linhas que o banco acrescentou).
	//
	// Sem lote a excluir: nesta altura ele ainda não existe.
	jaImportadoEm, err := s.avisoDeConteudoRepetido(ctx, ator.HouseholdID, hashConteudo, "")
	if err != nil {
		return BatchView{}, err
	}

	// --- montagem do lote ---------------------------------------------------
	agora := s.clock()
	lote := &Batch{
		ID:            s.ids(),
		HouseholdID:   ator.HouseholdID,
		AccountID:     in.AccountID,
		CreatedBy:     ator.UserID,
		Institution:   string(res.Institution),
		DocKind:       string(res.DocKind),
		FormatID:      res.FormatID,
		FileName:      sanitize.FileName(escolherNome(in.FileName, nomeInterno)),
		ContentSHA256: hashConteudo,
		Encoding:      string(codificacao),
		RowCount:      len(res.Rows) + len(res.Rejected),
		RejectedCount: len(res.Rejected),
		MinDate:       res.MinDate,
		MaxDate:       res.MaxDate,
		Status:        BatchStatusPending,
		ExpiresAt:     agora.Add(s.ttl),
		CreatedAt:     agora,
		UpdatedAt:     agora,
	}

	if res.DocKind == DocKindCardStatement {
		competencia, fechamento, vencimento := s.sugerirFatura(conta, res.Statement, res.MaxDate)
		lote.SuggestedCompetenceMonth = &competencia
		lote.SuggestedClosingDate = &fechamento
		lote.SuggestedDueDate = &vencimento
	}

	linhas := s.montarLinhas(lote, res, analise, sugestoes, agora)

	// Última parada voluntária antes da ESCRITA. Depois daqui, erro é erro de
	// gravação — 500 com log de ERROR —, e não "o arquivo é grande demais":
	// começar uma transação com o orçamento já estourado só produziria um
	// rollback com a culpa trocada.
	if err := conferirPrazo(ctx, "gravando o lote"); err != nil {
		return BatchView{}, err
	}

	err = s.tx.Do(ctx, func(ctx context.Context) error {
		if err := s.repo.CreateBatch(ctx, lote); err != nil {
			return fmt.Errorf("gravando lote de importação: %w", err)
		}
		if err := s.repo.CreateRows(ctx, ator.HouseholdID, linhas); err != nil {
			return fmt.Errorf("gravando linhas do lote: %w", err)
		}
		// UMA entrada por lote, não uma por linha (§6.10). A entrada não
		// carrega valor nenhum — nem o total do arquivo (S8).
		return s.registrar(ctx, ator, audit.ActionImportCreated, lote.ID)
	})
	if err != nil {
		return BatchView{}, err
	}

	contadores := countsFrom(contarStatus(linhas))
	return toBatchView(lote, &contadores, jaImportadoEm), nil
}

// conferirPrazo é a PARADA VOLUNTÁRIA da fase 1: ela olha o relógio do
// contexto e, se o orçamento acabou, interrompe a análise antes de gastar mais
// trabalho na etapa seguinte.
//
// # Por que a classificação do erro nasce AQUI, e em nenhum outro lugar
//
// Só quem impôs o prazo sabe que foi o prazo que venceu. A borda não sabe:
// desde que o gormstore passou a somar o motivo do contexto ao erro do driver
// (platform/storage/ctxerr.go), `errors.Is(err, context.DeadlineExceeded)` é
// verdadeiro para QUALQUER falha de banco que aconteça com o contexto morto —
// tabela corrompida, arquivo de banco sem permissão, defeito de programação.
// Um handler que traduzisse aquilo em "o seu arquivo demorou demais" culparia o
// usuário por uma falha do servidor E apagaria a linha de ERROR que é o único
// registro dela.
//
// Aqui não há ambiguidade: nada falhou. O código conferiu o relógio, viu que o
// orçamento acabou e desistiu por conta própria — esse, e só esse, é o
// ErrAnalyzeTimeout.
//
// O preço, deliberado: um prazo que vença DENTRO de uma consulta ao banco não
// vira 422, vira 500 com log de ERROR. É o lado certo para errar — o orçamento
// de 15 s existe para limitar o trabalho sobre as LINHAS do arquivo (leitura,
// classificação, deduplicação), e uma única consulta que sozinha o estoura é
// problema de infraestrutura, não do arquivo de quem importou.
//
// # Uma nota sobre o relógio
//
// A conferência tem DUAS perguntas porque o contexto responde a primeira com
// atraso: o cancelamento por prazo é feito por um timer, e no Windows a
// granularidade do timer é de milissegundos. Entre o instante em que o prazo
// passa e o instante em que `ctx.Err()` deixa de ser nil cabe uma etapa inteira
// da análise começando com o orçamento já no vermelho. Perguntar o PRAZO direto
// fecha essa janela — e quem responde continua sendo o relógio, nunca um erro
// alheio.
//
// ⚠️ Invariante que sustenta a tradução: o único prazo no contexto da análise é
// o AnalyzeTimeout. O projeto não tem middleware de prazo por requisição
// (conferido em internal/platform/httpserver e cmd/api). No dia em que tiver,
// esta função precisa distinguir o prazo DELE do nosso antes de continuar
// chamando os dois de ErrAnalyzeTimeout.
func conferirPrazo(ctx context.Context, etapa string) error {
	if motivo := ctx.Err(); motivo != nil {
		return erroDeParada(motivo, etapa)
	}
	if prazo, temPrazo := ctx.Deadline(); temPrazo && !time.Now().Before(prazo) {
		return erroDeParada(context.DeadlineExceeded, etapa)
	}
	return nil
}

// erroDeParada embrulha o motivo do contexto OBSERVADO por uma parada
// voluntária nossa.
//
// A distinção entre os dois motivos é a razão de esta função existir:
//
//   - prazo vencido é limite de TRABALHO previsto — vira ErrAnalyzeTimeout, e
//     a borda responde 422 IMPORT_FILE_REJECTED;
//   - cancelamento é o CLIENTE que foi embora (aba fechada, app morto). Não é
//     422 (não há a quem orientar) nem incidente: sobe cru e a borda o
//     reconhece pelo contexto da REQUISIÇÃO, registrando em INFO.
//
// O motivo original continua na cadeia nos dois casos: o log precisa dele, e
// nenhum dos dois carrega conteúdo do arquivo.
func erroDeParada(motivo error, etapa string) error {
	if errors.Is(motivo, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %s: %w", ErrAnalyzeTimeout, etapa, motivo)
	}
	return fmt.Errorf("%s: %w", etapa, motivo)
}

// normalizarConteudo devolve o texto do CSV já em UTF-8, sem BOM e com quebra
// de linha em LF, junto da codificação detectada.
//
// ⚠️ É sobre ESTE texto que o content_sha256 é calculado, e não sobre os bytes
// enviados. Sobre os bytes do ZIP o hash seria inútil: duas compactações do
// mesmo CSV diferem em timestamp, nível de compressão e vetor da cifra, e o
// aviso "você já importou este arquivo" nunca dispararia. Normalizar a quebra
// de linha cobre o mesmo arquivo reaberto e salvo no Windows.
func normalizarConteudo(raw []byte) (string, csvtext.Encoding, error) {
	texto, codificacao, err := csvtext.Decode(raw)
	if err != nil {
		return "", "", err
	}
	// CRLF primeiro; o CR solto do Mac clássico sobra para a segunda troca.
	texto = strings.ReplaceAll(texto, "\r\n", "\n")
	texto = strings.ReplaceAll(texto, "\r", "\n")
	return texto, codificacao, nil
}

// escolherNome prefere o nome de dentro do ZIP ao nome do envelope.
//
// O de dentro descreve o documento que realmente foi lido; o de fora é o nome
// que o navegador mandou. Os dois são conteúdo do usuário e passam pela mesma
// sanitização — a preferência é só sobre qual é mais informativo na tela.
func escolherNome(externo, interno string) string {
	if strings.TrimSpace(interno) != "" {
		return interno
	}
	return externo
}

// dedupRowDe converte a linha lida na entrada da deduplicação.
//
// Os valores são os CANÔNICOS (kind, valor positivo, data civil e a descrição
// já sanitizada e já truncada) — nunca o texto cru do arquivo. Calculada sobre
// o texto cru, a chave derivada mudaria na reimportação e a deduplicação
// deixaria de funcionar em silêncio (ADR-025c).
func dedupRowDe(r ParsedRow) dedup.Row {
	return dedup.Row{
		Seq:             r.Seq,
		Kind:            r.Kind,
		OccurredOn:      r.OccurredOn,
		AmountCents:     r.AmountCents,
		DescriptionNorm: r.DescriptionNorm,
		ExternalID:      r.ExternalID,
		IsCardPayment:   r.Suggestion == SuggestionCardPayment,
	}
}

// classificar aplica as palavras-chave da casa a cada linha aproveitada (spec
// 0005 §4.2.1) e devolve as sugestões, na MESMA ordem de rows. Como efeito,
// marca IsInternalTransfer nas linhas de deduplicação correspondentes.
//
// Ordem por linha, e ela é normativa (plano E2c §4.3):
//
//  1. conta batendo VENCE categoria batendo — o dinheiro não saiu da casa. A
//     linha que não é pagamento de fatura vira candidata a
//     `transferencia_interna` e NÃO recebe categoria;
//  2. pagamento de fatura com conta batendo só ganha a contraparte sugerida
//     (o status fica; a pontuação da contraparte não é exposta — emenda
//     §10.4) e segue para a categoria, para o caso de ser importado como
//     linha comum;
//  3. categoria: só as ATIVAS e atribuíveis da natureza do kind (o Set já
//     separa receita de despesa; `income` nunca recebe categoria `expense`).
//
// O conjunto é carregado UMA vez por lote (quatro consultas). A conta do lote é
// excluída antes da escolha (classify.Set.SuggestCounterpart): a palavra-chave
// da própria conta nunca gera transferência. O contexto é conferido a cada
// 500 linhas — o AnalyzeTimeout continua mandando.
func (s *Service) classificar(ctx context.Context, householdID, accountID string, rows []ParsedRow, linhasDedup []dedup.Row) ([]sugestaoDaLinha, error) {
	sugestoes := make([]sugestaoDaLinha, len(rows))
	if len(rows) == 0 || s.classifier == nil {
		// Sem classificador não há sugestão — nunca pânico no caminho da
		// requisição. O construtor o exige; a guarda é defesa em profundidade.
		return sugestoes, nil
	}

	conjunto, err := s.classifier.Load(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("carregando palavras-chave da casa: %w", err)
	}

	for i := range rows {
		if i%64 == 0 {
			if err := conferirPrazo(ctx, "classificando linhas"); err != nil {
				return nil, err
			}
		}
		r := &rows[i]

		contra, err := conjunto.SuggestCounterpart(accountID, r.DescriptionNorm)
		if err != nil {
			return nil, classificacaoCara(err)
		}
		if contra.Matched() && r.Suggestion != SuggestionCardPayment {
			id := contra.Match.OwnerID
			score := contra.Match.Score
			palavra := contra.Match.Keyword
			sugestoes[i] = sugestaoDaLinha{CounterpartID: &id, Score: &score, Keyword: &palavra}
			if i < len(linhasDedup) {
				linhasDedup[i].IsInternalTransfer = true
			}
			continue // transferencia_* NÃO recebe categoria
		}
		if contra.Matched() {
			id := contra.Match.OwnerID
			sugestoes[i].CounterpartID = &id
		}

		cat, err := conjunto.SuggestCategory(r.Kind, r.DescriptionNorm)
		if err != nil {
			return nil, classificacaoCara(err)
		}
		if cat.Matched() {
			id := cat.Match.OwnerID
			score := cat.Match.Score
			palavra := cat.Match.Keyword
			sugestoes[i].CategoryID = &id
			sugestoes[i].Score = &score
			sugestoes[i].Keyword = &palavra
		}
	}
	return sugestoes, nil
}

// classificacaoCara traduz o estouro do orçamento de casamento por
// palavra-chave no limite de arquivo do importador — 422, arquivo menor.
// Qualquer outro erro sobe embrulhado com contexto.
func classificacaoCara(err error) error {
	if errors.Is(err, textmatch.ErrWorkBudgetExceeded) {
		return fmt.Errorf("%w: %w", ErrClassifyTooCostly, err)
	}
	return fmt.Errorf("classificando linhas: %w", err)
}

// parearComPernasExistentes carrega as pernas de transferência vivas da conta
// na janela do arquivo e aplica pairExistingLegs, promovendo as linhas que
// casaram a `transferencia_ja_registrada` com MatchTransactionID apontando para
// a perna.
//
// Só consulta o banco quando alguma linha participa: arquivo sem palavra-chave
// de conta batendo não paga as duas consultas. O erro NÃO vira "nenhuma
// perna": silêncio aqui proporia um segundo par para uma transferência que já
// existe — a duplicata que a spec 0004 existe para impedir.
func (s *Service) parearComPernasExistentes(
	ctx context.Context,
	householdID, accountID string,
	res ParseResult,
	analise *dedup.Result,
	sugestoes []sugestaoDaLinha,
) error {
	if len(analise.Rows) != len(res.Rows) || len(sugestoes) != len(res.Rows) {
		return nil
	}

	participa := false
	for i := range analise.Rows {
		if participaDoPareamento(analise.Rows[i].Status, sugestoes[i]) {
			participa = true
			break
		}
	}
	if !participa {
		return nil
	}

	pernas, err := s.ledger.TransferLegsForLinking(ctx, householdID, accountID, res.MinDate, res.MaxDate)
	if err != nil {
		return fmt.Errorf("carregando pernas de transferência para pareamento: %w", err)
	}

	vinculos := pairExistingLegs(res.Rows, analise.Rows, sugestoes, pernas)
	if len(vinculos) == 0 {
		return nil
	}

	for i := range analise.Rows {
		pernaID, ok := vinculos[analise.Rows[i].Seq]
		if !ok {
			continue
		}
		id := pernaID
		anterior := analise.Rows[i].Status
		analise.Rows[i].Status = dedup.StatusTransferAlreadyRegistered
		analise.Rows[i].MatchTransactionID = &id
		analise.Rows[i].Import = false
		if analise.Counts != nil {
			analise.Counts[anterior]--
			analise.Counts[dedup.StatusTransferAlreadyRegistered]++
		}
	}
	return nil
}

// montarLinhas junta as linhas aproveitadas (já classificadas) e as rejeitadas
// numa lista só, ordenada por Seq.
//
// A linha rejeitada NÃO some: ela vai para a revisão com o número da linha
// física e o CÓDIGO do motivo, para a pessoa conferir o arquivo. O que não vai
// junto é o conteúdo (§3.6).
func (s *Service) montarLinhas(lote *Batch, res ParseResult, analise dedup.Result, sugestoes []sugestaoDaLinha, agora time.Time) []Row {
	linhas := make([]Row, 0, len(res.Rows)+len(res.Rejected))

	for i := range res.Rows {
		r := res.Rows[i]
		veredito := analise.Rows[i]
		linha := Row{
			ID:                 s.ids(),
			HouseholdID:        lote.HouseholdID,
			BatchID:            lote.ID,
			Seq:                r.Seq,
			LineNo:             r.LineNo,
			Kind:               r.Kind,
			OccurredOn:         r.OccurredOn,
			AmountCents:        r.AmountCents,
			Description:        r.Description,
			DescriptionNorm:    r.DescriptionNorm,
			ExternalID:         r.ExternalID,
			DedupKey:           veredito.DedupKey,
			Status:             string(veredito.Status),
			MatchTransactionID: veredito.MatchTransactionID,
			CreatedAt:          agora,
		}
		if i < len(sugestoes) {
			// Copiadas por valor: o staging é o que a revisão lê e o que o
			// confirm usa como default — nada aqui vem do cliente.
			linha.SuggestedCategoryID = copiarTexto(sugestoes[i].CategoryID)
			linha.MatchScore = copiarInteiro(sugestoes[i].Score)
			linha.MatchedKeyword = copiarTexto(sugestoes[i].Keyword)
			linha.SuggestedCounterpartAccountID = copiarTexto(sugestoes[i].CounterpartID)
		}
		linhas = append(linhas, linha)
	}

	for i := range res.Rejected {
		r := res.Rejected[i]
		motivo := r.Reason
		rowID := s.ids()
		linhas = append(linhas, Row{
			ID:          rowID,
			HouseholdID: lote.HouseholdID,
			BatchID:     lote.ID,
			Seq:         r.Seq,
			LineNo:      r.LineNo,
			// Sem valores canônicos: a linha não produziu nenhum, e é por isso
			// que ela não é liberável. dedup_key é NOT NULL e participa de
			// índice único, então ela recebe uma chave ÚNICA POR CONSTRUÇÃO
			// (a mesma forma do lançamento manual, §4.4) — que nunca barra
			// nada e nunca colide com a chave de nenhuma linha real.
			DedupKey:     dedup.ManualKey(rowID),
			Status:       string(dedup.StatusRejected),
			RejectReason: &motivo,
			CreatedAt:    agora,
		})
	}

	// Ordem do arquivo: Seq é a chave do cursor da revisão, e a tela mostra as
	// linhas na ordem em que elas aparecem no documento.
	ordenarPorSeq(linhas)
	return linhas
}

// ordenarPorSeq põe as linhas na ordem do arquivo. Seq é único por lote
// (ux_import_rows_seq), então não há empate a desfazer.
func ordenarPorSeq(linhas []Row) {
	for i := 1; i < len(linhas); i++ {
		atual := linhas[i]
		j := i - 1
		for j >= 0 && linhas[j].Seq > atual.Seq {
			linhas[j+1] = linhas[j]
			j--
		}
		linhas[j+1] = atual
	}
}

// contarStatus monta o mapa status -> quantidade das linhas recém-montadas,
// sem ida ao banco: quem acabou de classificar já sabe o resultado.
func contarStatus(linhas []Row) map[string]int {
	m := make(map[string]int, 9)
	for i := range linhas {
		m[linhas[i].Status]++
	}
	return m
}

// sugerirFatura devolve competência, fechamento e vencimento SUGERIDOS.
//
// A ordem das fontes, da mais confiável para a menos:
//
//  1. o que o DOCUMENTO revelou (StatementHint). Os dois arquivos do Nubank não
//     revelam nada; um emissor que revele ganha de qualquer inferência;
//  2. os dias configurados na conta (statement_closing_day / statement_due_day);
//  3. inferência a partir da MAIOR DATA do arquivo.
//
// O nome do arquivo não entra em lugar nenhum desta função — ele é entrada do
// cliente, e uma fatura arquivada no mês errado estraga a competência de
// dezenas de linhas (§5.3).
//
// A competência é sempre o mês do VENCIMENTO (D2): uma fatura com linhas de
// 06/08 a 05/09 que vence em 13/09 é a "fatura de setembro" para qualquer
// pessoa.
func (s *Service) sugerirFatura(conta *account.Account, hint *StatementHint, maxDate civil.Date) (string, civil.Date, civil.Date) {
	if hint != nil && hint.ClosingDate != nil && hint.DueDate != nil &&
		!hint.ClosingDate.IsZero() && !hint.DueDate.IsZero() {
		competencia := hint.CompetenceMonth
		if competencia == "" {
			competencia = hint.DueDate.YearMonth()
		}
		return competencia, *hint.ClosingDate, *hint.DueDate
	}

	if conta.StatementClosingDay != nil && conta.StatementDueDay != nil {
		fechamento := proximoDia(maxDate, *conta.StatementClosingDay)
		vencimento := proximoDia(fechamento, *conta.StatementDueDay)
		return vencimento.YearMonth(), fechamento, vencimento
	}

	fechamento := maxDate
	vencimento := somarDias(fechamento, DefaultDueGapDays)
	return vencimento.YearMonth(), fechamento, vencimento
}

// proximoDia devolve a primeira ocorrência de `dia` a partir de `base`
// (inclusive), pulando para o mês seguinte quando ela já passou.
//
// O dia é GRAMPEADO ao último dia do mês: um fechamento configurado no dia 31
// em fevereiro vira 28 (ou 29), e não uma data impossível.
func proximoDia(base civil.Date, dia int) civil.Date {
	candidato := diaDoMes(base.Year(), base.Month(), dia)
	if !candidato.Before(base) {
		return candidato
	}
	ano, mes := base.Year(), base.Month()+1
	if mes > 12 {
		ano, mes = ano+1, 1
	}
	return diaDoMes(ano, mes, dia)
}

// diaDoMes monta a data grampeando o dia à duração do mês.
func diaDoMes(ano, mes, dia int) civil.Date {
	// O dia 0 do mês seguinte é o último do mês pedido — a aritmética de
	// calendário da stdlib, sem tabela de meses escrita à mão.
	ultimo := time.Date(ano, time.Month(mes)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if dia > ultimo {
		dia = ultimo
	}
	if dia < 1 {
		dia = 1
	}
	d, err := civil.New(ano, mes, dia)
	if err != nil {
		// Só acontece com ano fora da faixa civil, o que não chega aqui: a data
		// vem do próprio arquivo, já validada pela janela de sanidade.
		return civil.Date{}
	}
	return d
}

// somarDias soma dias a uma data civil sem passar perto de fuso: a conta é em
// UTC e a hora é descartada na volta.
func somarDias(d civil.Date, dias int) civil.Date {
	if d.IsZero() {
		return d
	}
	t := time.Date(d.Year(), time.Month(d.Month()), d.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, dias)
	out, err := civil.New(t.Year(), int(t.Month()), t.Day())
	if err != nil {
		return d
	}
	return out
}
