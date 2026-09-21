package transaction

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
)

// Este arquivo guarda as INVARIANTES de forma do lançamento: o que pode entrar,
// com que aparência, e o erro exato de cada recusa. Ele é separado do serviço
// de propósito — a regra fica legível sozinha, e o teste que a prova não
// precisa montar repositório nenhum.
//
// Validação de FORMA mora aqui. Validação de EXISTÊNCIA (a conta é da casa? a
// categoria existe? a fatura é deste cartão?) mora no serviço e acontece DENTRO
// da transação da escrita: conferida fora dela, a resposta pode ter mudado
// antes do INSERT, e essa janela é TOCTOU (spec 0004 §6.6).

// MaxBatchRows é o teto de linhas de uma escrita em lote.
//
// Espelha o teto de linhas de dados da importação (§5.6 da spec 0004) e é
// repetido aqui de propósito: limite que só existe no chamador deixa de existir
// no primeiro chamador novo.
const MaxBatchRows = 10_000

// Janela de sanidade da data do lançamento.
//
// Estática de propósito. O teto "não pode ser no futuro" depende de hoje no
// fuso da CASA (ADR-019a), e este pacote não decide fuso — essa regra entra no
// caminho de criação manual (E2b), que já recebe o calendário. O que estas duas
// bordas impedem é o absurdo: ano 0001 ou ano 9999 vindos de um arquivo mal
// lido, que estragam índice, relatório e gráfico de uma vez só.
var (
	minOccurredOn = civil.MustNew(1970, 1, 1)
	maxOccurredOn = civil.MustNew(2100, 1, 1)
)

// Erros de validação. O handler os traduz em 400/422; nenhum carrega detalhe
// interno nem dado de outra casa.
var (
	// ErrInvalidKind — tipo fora da allowlist.
	ErrInvalidKind = errors.New("tipo de lançamento inválido")

	// ErrInvalidSource — origem fora da allowlist.
	ErrInvalidSource = errors.New("origem de lançamento inválida")

	// ErrInvalidAmount — valor negativo ou acima da faixa de sanidade.
	//
	// Negativo é inválido porque o sinal vive no kind: aceitar -R$ 20,00 numa
	// despesa criaria duas formas de dizer a mesma coisa, e a soma do saldo
	// escolheria a errada em metade dos casos.
	//
	// ZERO é válido. O contrato publica amountCents com minimum 0 ("sempre não
	// negativo"), e recusar zero aqui faria o serviço divergir da spec
	// publicada. Linha de R$ 0,00 sem sentido é recusada uma camada acima, na
	// leitura do arquivo (importer.RejectZeroAmount), que é onde existe
	// contexto para dizer que aquilo é lixo do documento.
	ErrInvalidAmount = errors.New("valor fora da faixa permitida")

	// ErrInvalidDate — data ausente ou fora da janela de sanidade.
	ErrInvalidDate = errors.New("data do lançamento inválida")

	// ErrInvalidDescription — descrição maior que o limite de armazenamento.
	//
	// Recusa em vez de truncar, e isso é regra de deduplicação, não de gosto:
	// a chave derivada é calculada sobre a descrição JÁ truncada (ADR-025c).
	// Truncar aqui, depois do cálculo, faria a reimportação gerar chave
	// diferente da gravada — e a deduplicação pararia de funcionar em silêncio.
	ErrInvalidDescription = errors.New("descrição de lançamento inválida")

	// ErrInvalidMonth — mês fora da forma "YYYY-MM".
	ErrInvalidMonth = errors.New("mês inválido")

	// ErrInvalidCursor — cursor de paginação malformado. A resposta ao cliente
	// é 400 SEM detalhe (S5 do PLANOS.md): o cursor é opaco, e explicar por que
	// ele não serve é ensinar a forjá-lo.
	ErrInvalidCursor = errors.New("cursor inválido")

	// ErrAccountArchived — conta arquivada como destino. Arquivar quer dizer
	// "não use mais em lançamento novo"; é 422 e não 404 porque a conta É da
	// casa, e a pessoa precisa saber que basta desarquivar.
	ErrAccountArchived = errors.New("conta arquivada")

	// ErrCategoryArchived — mesma regra, do lado da categoria.
	ErrCategoryArchived = errors.New("categoria arquivada")

	// ErrCategoryOnTransfer — transferência com categoria (ADR-016).
	// Transferir não é receita nem despesa: categorizar uma perna faria o
	// relatório contar como gasto um dinheiro que só mudou de bolso.
	ErrCategoryOnTransfer = errors.New("transferência não tem categoria")

	// ErrCategoryKindMismatch — despesa em categoria de receita, ou o
	// contrário. O relatório soma por natureza; aceitar a mistura aqui produz
	// um número que não fecha com nada.
	ErrCategoryKindMismatch = errors.New("categoria de natureza incompatível com o lançamento")

	// ErrCategoryIsParentGroup — a categoria é um GRUPO que tem ao menos uma
	// subcategoria ativa (spec 0005 §13).
	//
	// A invariante "grupo com filhas não recebe lançamento" já valia em dois
	// lugares — o seletor da tela e o classificador (internal/classify, que
	// nunca sugere grupo com filha ativa) — e a §12 recusa palavra-chave nesse
	// mesmo grupo JUSTAMENTE porque ele não recebe lançamento. Faltava o
	// servidor: sem esta recusa, um corpo montado à mão pendura a despesa no
	// grupo e o relatório da E6 passa a ter o mesmo dinheiro em dois níveis.
	//
	// É 422, e não 400: o corpo está bem formado e a categoria existe na casa
	// — é a regra de negócio que recusa. A mensagem não cita nome nem id: ela
	// atravessa o log do handler, e nome de categoria é dado da casa.
	ErrCategoryIsParentGroup = errors.New("categoria de grupo com subcategorias não recebe lançamento")

	// ErrStatementMismatch — a fatura informada é de OUTRA conta que não a do
	// lançamento. Ligar uma compra à fatura do outro cartão inventaria dívida
	// num cartão e sumiria com ela no outro.
	ErrStatementMismatch = errors.New("fatura não pertence à conta do lançamento")

	// ErrBrokenTransfer — o lote descreve uma transferência que não é um par
	// (ADR-016): perna sem grupo, grupo com uma perna só, pernas com valores ou
	// datas diferentes, ou as duas na mesma conta.
	ErrBrokenTransfer = errors.New("transferência precisa de um par de pernas coerente")

	// ErrBatchTooLarge — lote acima de MaxBatchRows.
	ErrBatchTooLarge = errors.New("lote de lançamentos grande demais")

	// ErrEmptyBatch — lote sem nenhuma linha. Não é falha de infraestrutura, e
	// também não é sucesso silencioso: quem pediu para gravar nada tem um
	// defeito a corrigir.
	ErrEmptyBatch = errors.New("lote de lançamentos vazio")

	// ErrInvalidDedupKey — chave fora da forma canônica (SHA-256 em
	// hexadecimal, 64 caracteres).
	//
	// A forma é conferida aqui porque a coluna é varchar(64) e participa do
	// índice único: uma chave de 70 caracteres seria truncada ou recusada pelo
	// banco conforme o dialeto, e "conforme o dialeto" é o que o projeto
	// inteiro existe para evitar.
	ErrInvalidDedupKey = errors.New("chave de deduplicação inválida")

	// ErrInvalidExternalID — identificador do documento acima do limite da
	// coluna (64 bytes).
	ErrInvalidExternalID = errors.New("identificador externo inválido")

	// ErrCounterpartNeedsAccount — GET /transfers com counterpartAccountId e
	// sem accountId. "O par entre X e ninguém" não é uma pergunta; é 400 no
	// campo counterpartAccountId (spec 0005 §4.4).
	ErrCounterpartNeedsAccount = errors.New("a contraparte exige a conta principal do filtro")

	// ErrSameAccountFilter — accountId igual a counterpartAccountId. Não há
	// transferência de uma conta para ela mesma (validarPares recusa na
	// escrita), então o filtro só devolveria vazio fingindo que é resposta.
	ErrSameAccountFilter = errors.New("conta e contraparte precisam ser diferentes")

	// ErrTooManyUncategorized — o mês tem mais lançamentos sem categoria do
	// que MaxAutoCategorizeRows. É 422 no campo month: o pedido é válido em
	// forma, mas não cabe numa execução.
	ErrTooManyUncategorized = errors.New("lançamentos sem categoria demais para uma execução")

	// ErrKeywordMatchTooCostly — o casamento por palavra-chave passou do
	// orçamento de trabalho de UMA execução (textmatch.MaxMatchWork, achado
	// A1 da revisão de segurança).
	//
	// É a mesma FAMÍLIA de ErrTooManyUncategorized — 422 no campo `month` —
	// mas um erro próprio, porque a causa é outra: não é o mês que tem linhas
	// demais, é o conjunto palavras-chave × descrições que ficou caro demais
	// para uma execução. Nunca execução parcial: categorizar "até onde deu"
	// deixaria a pessoa sem saber quais linhas ficaram de fora.
	ErrKeywordMatchTooCostly = errors.New("casamento por palavra-chave caro demais para uma execução")

	// ErrTooManyTransfers — o mês tem mais pernas de transferência do que
	// MaxTransferLegsPerMonth. Também 422 em month.
	ErrTooManyTransfers = errors.New("transferências demais no mês para somar de uma vez")

	// ErrCategoryRequired — PATCH /transactions/{id} sem categoria (spec 0005
	// §11): o campo é obrigatório e a emenda não tem "tirar a categoria". É
	// 400 no campo categoryId. O handler já recusa antes; a guarda aqui é
	// defesa em profundidade para o serviço nunca procurar a categoria "".
	ErrCategoryRequired = errors.New("categoria obrigatória")

	// ErrTooManyTransferCandidates — o mês tem mais candidatas a
	// transferência do que MaxTransferDetectRows, ou a janela de espelhos tem
	// mais linhas do que MaxTransferMirrorRows (spec 0005 §13, ADR-028f).
	// 422 no campo month: o pedido é válido em forma, mas não cabe numa
	// execução — e execução parcial não existe.
	ErrTooManyTransferCandidates = errors.New("candidatas a transferência demais para uma execução")

	// ErrTransferConversionConflict — o UPDATE condicional de um par não
	// afetou exatamente as duas linhas esperadas (ADR-028d): alguém excluiu,
	// converteu ou editou um dos lançamentos ENTRE a leitura e a escrita da
	// mesma execução. A transação inteira é desfeita — nada parcial — e o
	// handler responde 409 CONFLICT: a ação da tela é pedir a prévia de novo.
	//
	// É tipado, e não genérico, justamente para não virar 500: não é falha do
	// servidor, é o estado que mudou debaixo da operação. Não é
	// IsValidationError: 409 é a sua própria classe.
	ErrTransferConversionConflict = errors.New("lançamento mudou durante a conversão em transferência")

	// ErrCategoryChanged — uma categoria do plano de auto-categorização deixou
	// de ser ATRIBUÍVEL entre o cálculo e a escrita (achado A4 da revisão de
	// segurança).
	//
	// É a janela TOCTOU que nasce do achado A2: o plano roda FORA da transação
	// — de propósito, para não segurar conexão do pool durante a pontuação de
	// até 10.000 linhas — e entre ele e o UPDATE cabe uma requisição inteira.
	// Se nela outro membro da casa excluir a categoria, a checagem `inUse`
	// daquela exclusão não encontra nada (nada foi gravado ainda) e o UPDATE
	// penduraria lançamentos numa categoria EXCLUÍDA: exatamente o estado que
	// category.ErrInUse existe para impedir. Não é BOLA — os ids vêm do plano,
	// já filtrado pela casa do token —, é integridade.
	//
	// A decisão é falhar a operação INTEIRA, e não pular as linhas daquela
	// categoria: a pessoa pediu para categorizar EM X, e X deixou de existir
	// no meio. Seguir com um subconjunto devolveria um `categorized` menor sem
	// dizer quais linhas ficaram para trás — pior do que falhar e deixar que
	// ela decida de novo. Mesmo desfecho, pelo mesmo motivo, de
	// ErrTransferConversionConflict (ADR-028d) e de
	// investment.ErrDestinationChanged: 409 CONFLICT sem campos, transação
	// desfeita, NADA auditado, e a tela pede a prévia de novo.
	//
	// O QUE derruba a operação são QUATRO eixos, e eles não saem de uma lista
	// mantida à mão: saem da pergunta "o que qualificava este destino no
	// momento em que o plano o escolheu?" (spec 0005 §17, corolário do A9). A
	// reconferência relê os quatro da MESMA linha, por
	// category.DestinoAindaQualifica:
	//
	//   1. VIVA — ausente do mapa é excluída (ou de outra casa);
	//   2. não é GRUPO com subcategoria ATIVA (spec 0005 §12/§13);
	//   3. não está ARQUIVADA;
	//   4. a NATUREZA RELIDA ainda aceita o lado do dinheiro do lote
	//      (category.AceitaLancamento, ADR-029b) — o achado A9: PATCH
	//      /categories/{id} aceita `expense → income` numa categoria de topo
	//      sem filhas e sem uso, que é exatamente o estado da categoria
	//      recém-criada cujo lote esta rota calcula.
	//
	// ⚠️ O eixo 3 é o que alguém vai querer reabrir, e a versão anterior DESTE
	// comentário mandava reabri-lo ("arquivar é benigno, não cai aqui") — era o
	// comentário que estava errado, não o código. A distinção que o produto faz
	// é outra, e é a mesma de investment.ErrDestinationChanged: marcação
	// EXISTENTE sobrevive ao arquivamento — "lançamento com categoria
	// arquivada" continua sendo estado legítimo e continua contando nos
	// relatórios —, mas ATRIBUIÇÃO NOVA a categoria arquivada é recusada em
	// todas as outras portas do produto (ErrCategoryArchived no PATCH de uma
	// linha, no lote e na importação). Esta rota ATRIBUI: sem o eixo 3 ela
	// seria a única porta do produto gravando onde as outras três recusam.
	//
	// É tipado, e não genérico, justamente para não virar 500: não é falha do
	// servidor, é o estado que mudou debaixo da operação. Não é
	// IsValidationError: 409 é a sua própria classe.
	ErrCategoryChanged = errors.New("categoria do plano mudou durante a auto-categorização")

	// ErrPlanTimeout — o prazo PRÓPRIO da fase de cálculo (PlanTimeout) acabou
	// e a varredura do mês parou por decisão própria. Vale para as DUAS rotas
	// que varrem o mês inteiro: POST /transactions/auto-categorize e
	// POST /transfers/detect.
	//
	// É limite de TRABALHO, da mesma família de ErrTooManyUncategorized e
	// ErrTooManyTransferCandidates: 422 em `fields.month`, com a mesma
	// orientação — período menor. Sem ele, um limite PREVISTO chegava ao
	// `default` do handler e virava 500 INTERNAL_ERROR, enquanto a rota irmã
	// POST /investments/detect — com o MESMO PlanTimeout — já respondia 422.
	// Duas rotas, o mesmo prazo, duas respostas.
	//
	// ⚠️ Esta sentinela é a ÚNICA autorização para dizer "este mês demorou
	// demais", e ela é emitida apenas nas paradas VOLUNTÁRIAS da fase de
	// cálculo (conferirPrazo/erroDeParada, em autocategorize.go) — NUNCA
	// deduzida do estado do contexto depois de um erro qualquer.
	//
	// O motivo é concreto, e é o mesmo que fez o importador
	// (importer.ErrAnalyzeTimeout) e o detect de investimentos
	// (investment.ErrPlanTimeout) abandonarem a dedução: desde que o gormstore
	// passou a somar o motivo do contexto ao erro do driver
	// (platform/storage/ctxerr.go), QUALQUER falha de banco ocorrida com o
	// contexto morto casa `errors.Is(err, context.DeadlineExceeded)`. Um ramo
	// de borda que casasse o erro de CONTEXTO faria duas coisas erradas ao
	// mesmo tempo: responderia "este mês demorou demais" — culpando o mês de
	// quem pediu — por uma falha do SERVIDOR, e apagaria a única linha de
	// ERROR daquela falha do log.
	//
	// O preço é declarado: um prazo que vença DENTRO de uma consulta não vira
	// 422, vira 500 com ERROR. É o lado certo para errar — o prazo existe para
	// limitar o trabalho sobre as LINHAS do mês (leitura, pontuação,
	// pareamento), e uma única consulta que sozinha o estoura é problema de
	// infraestrutura, não do mês de quem pediu.
	ErrPlanTimeout = errors.New("o prazo da fase de cálculo do mês acabou")
)

// MaxExternalIDBytes espelha transactions.external_id varchar(64).
const MaxExternalIDBytes = 64

// dedupKeyLen é o comprimento do SHA-256 em hexadecimal.
const dedupKeyLen = 64

// validarLote confere a FORMA do lote inteiro, antes de qualquer consulta.
//
// Antes, e não durante: uma linha malformada na posição 300 não deve custar 299
// consultas de conta para ser descoberta — e, mais importante, não deve deixar
// nada meio gravado enquanto é descoberta.
func validarLote(in CreateBatchInput) error {
	if len(in.Rows) == 0 {
		return ErrEmptyBatch
	}
	if len(in.Rows) > MaxBatchRows {
		return fmt.Errorf("%w: máximo de %d linhas", ErrBatchTooLarge, MaxBatchRows)
	}
	if !ValidSource(in.Source) {
		return ErrInvalidSource
	}
	// import sem lote de origem quebraria a rastreabilidade que substitui
	// 10.000 entradas de auditoria (spec 0004 §6.10); manual COM lote mentiria
	// sobre a origem do dado.
	if in.Source == SourceImport && (in.ImportBatchID == nil || *in.ImportBatchID == "") {
		return fmt.Errorf("%w: lançamento importado exige o lote de origem", ErrInvalidSource)
	}
	if in.Source == SourceManual && in.ImportBatchID != nil {
		return fmt.Errorf("%w: lançamento manual não tem lote de origem", ErrInvalidSource)
	}

	for i := range in.Rows {
		if err := validarLinha(in.Rows[i]); err != nil {
			// A posição é o índice na entrada, nunca o conteúdo da linha: o que
			// veio do arquivo não entra em mensagem de erro (spec 0004 §3.6).
			return fmt.Errorf("linha %d: %w", i+1, err)
		}
	}
	return validarPares(in.Rows)
}

// validarLinha confere a forma de UMA linha.
func validarLinha(r NewTransaction) error {
	if !ValidKind(r.Kind) {
		return ErrInvalidKind
	}
	if r.AccountID == "" {
		// Conta ausente é o mesmo desfecho de conta que não é da casa: 404.
		// Distinguir os dois não ajudaria ninguém e daria ao cliente um sinal
		// a mais sobre o que existe do outro lado (S1).
		return fmt.Errorf("conta ausente: %w", ErrNotFound)
	}
	if err := ValidateAmount(r.AmountCents); err != nil {
		return err
	}
	if err := ValidateOccurredOn(r.OccurredOn); err != nil {
		return err
	}
	if err := ValidateDescription(r.Description); err != nil {
		return err
	}
	if !dedupKeyValida(r.DedupKey) {
		return ErrInvalidDedupKey
	}
	if r.ExternalID != nil && len(*r.ExternalID) > MaxExternalIDBytes {
		return ErrInvalidExternalID
	}

	ehTransferencia := r.Kind == KindTransferOut || r.Kind == KindTransferIn
	if ehTransferencia && r.CategoryID != nil {
		return ErrCategoryOnTransfer
	}
	if !ehTransferencia && r.TransferGroupID != nil {
		// Grupo de transferência em linha que não é transferência amarraria
		// duas linhas que a exclusão em par depois trataria como um par — e
		// excluir uma despesa apagaria outra, sem explicação.
		return ErrBrokenTransfer
	}
	if ehTransferencia && (r.TransferGroupID == nil || *r.TransferGroupID == "") {
		return ErrBrokenTransfer
	}
	return nil
}

// validarPares confere que toda transferência do lote é um PAR coerente
// (ADR-016).
//
// As duas pernas nascem juntas, na mesma transação, e é aqui que "meia
// transferência não existe" deixa de ser intenção e vira invariante: sem esta
// conferência, um lote com só a perna de saída gravaria dinheiro saindo de uma
// conta sem entrar em lugar nenhum.
func validarPares(rows []NewTransaction) error {
	grupos := map[string][]int{}
	for i := range rows {
		r := rows[i]
		if r.Kind != KindTransferOut && r.Kind != KindTransferIn {
			continue
		}
		grupos[*r.TransferGroupID] = append(grupos[*r.TransferGroupID], i)
	}

	for _, indices := range grupos {
		if len(indices) != 2 {
			return fmt.Errorf("%w: cada transferência tem exatamente duas pernas", ErrBrokenTransfer)
		}
		saida, entrada := rows[indices[0]], rows[indices[1]]
		if saida.Kind == entrada.Kind {
			return fmt.Errorf("%w: uma perna de saída e uma de entrada", ErrBrokenTransfer)
		}
		if saida.AmountCents != entrada.AmountCents {
			return fmt.Errorf("%w: as duas pernas movem o mesmo valor", ErrBrokenTransfer)
		}
		if saida.OccurredOn.Compare(entrada.OccurredOn) != 0 {
			return fmt.Errorf("%w: as duas pernas acontecem no mesmo dia", ErrBrokenTransfer)
		}
		if saida.AccountID == entrada.AccountID {
			// Transferir para a própria conta é um no-op que ainda por cima
			// dobraria a linha no extrato daquela conta.
			return fmt.Errorf("%w: origem e destino precisam ser contas diferentes", ErrBrokenTransfer)
		}
	}
	return nil
}

// dedupKeyValida confere os 64 hexadecimais do SHA-256.
func dedupKeyValida(s string) bool {
	if len(s) != dedupKeyLen {
		return false
	}
	for i := range dedupKeyLen {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// ValidKind informa se o tipo pertence à allowlist.
func ValidKind(kind string) bool {
	switch kind {
	case KindIncome, KindExpense, KindTransferOut, KindTransferIn:
		return true
	default:
		return false
	}
}

// ValidSource informa se a origem pertence à allowlist.
func ValidSource(source string) bool {
	return source == SourceManual || source == SourceImport
}

// ParseMonth valida "YYYY-MM" e devolve o primeiro dia do mês.
//
// Rígido como civil.Parse, e pelo mesmo motivo: entrada externa tem uma forma
// só. "2026-13", "2026-1", "2026-01-01" e " 2026-01" são todos recusados, com a
// MESMA mensagem — o cliente não aprende nada sobre o servidor pela recusa.
func ParseMonth(s string) (civil.Date, error) {
	if len(s) != 7 {
		return civil.Date{}, fmt.Errorf("%w: formato esperado AAAA-MM", ErrInvalidMonth)
	}
	d, err := civil.Parse(s + "-01")
	if err != nil {
		return civil.Date{}, fmt.Errorf("%w: formato esperado AAAA-MM", ErrInvalidMonth)
	}
	return d, nil
}

// ValidateAmount recusa valor negativo e valor acima da faixa de sanidade.
func ValidateAmount(cents int64) error {
	if cents < 0 {
		return fmt.Errorf("%w: o valor não pode ser negativo (o sinal vem do tipo)", ErrInvalidAmount)
	}
	if cents > MaxAmountCents {
		return fmt.Errorf("%w: máximo de %d centavos", ErrInvalidAmount, MaxAmountCents)
	}
	return nil
}

// ValidateOccurredOn recusa data zero e data fora da janela de sanidade.
func ValidateOccurredOn(d civil.Date) error {
	if d.IsZero() {
		return fmt.Errorf("%w: data ausente", ErrInvalidDate)
	}
	if d.Before(minOccurredOn) || d.After(maxOccurredOn) {
		return fmt.Errorf("%w: fora da janela de %s a %s", ErrInvalidDate, minOccurredOn, maxOccurredOn)
	}
	return nil
}

// ValidateDescription recusa descrição maior que o teto de armazenamento.
//
// O teto é medido em RUNAS, como a coluna varchar(140) do schema: "Alimentação"
// tem 11 runas e 13 bytes, e é a runa que o usuário conta.
func ValidateDescription(s string) error {
	if utf8.RuneCountInString(s) > MaxDescriptionLen {
		return fmt.Errorf("%w: máximo de %d caracteres", ErrInvalidDescription, MaxDescriptionLen)
	}
	return nil
}

// IsValidationError informa se o erro é de entrada (400/422) e não falha
// interna. O handler usa isto para não vazar erro de infraestrutura como se
// fosse culpa de quem digitou.
func IsValidationError(err error) bool {
	return errors.Is(err, ErrInvalidKind) ||
		errors.Is(err, ErrInvalidSource) ||
		errors.Is(err, ErrInvalidAmount) ||
		errors.Is(err, ErrInvalidDate) ||
		errors.Is(err, ErrInvalidDescription) ||
		errors.Is(err, ErrInvalidMonth) ||
		errors.Is(err, ErrInvalidCursor) ||
		errors.Is(err, ErrAccountArchived) ||
		errors.Is(err, ErrCategoryArchived) ||
		errors.Is(err, ErrCategoryOnTransfer) ||
		errors.Is(err, ErrCategoryKindMismatch) ||
		errors.Is(err, ErrCategoryIsParentGroup) ||
		errors.Is(err, ErrStatementMismatch) ||
		errors.Is(err, ErrBrokenTransfer) ||
		errors.Is(err, ErrBatchTooLarge) ||
		errors.Is(err, ErrEmptyBatch) ||
		errors.Is(err, ErrCounterpartNeedsAccount) ||
		errors.Is(err, ErrSameAccountFilter) ||
		errors.Is(err, ErrTooManyUncategorized) ||
		errors.Is(err, ErrKeywordMatchTooCostly) ||
		errors.Is(err, ErrTooManyTransfers) ||
		errors.Is(err, ErrTooManyTransferCandidates) ||
		errors.Is(err, ErrCategoryRequired)
}

// IsBlocked informa que a escrita bateu no índice único
// (household_id, dedup_key, dedup_ordinal).
//
// Existe para que a importação traduza a colisão em "esta linha não entrou" na
// tela de revisão — NUNCA em 500. Dois confirms simultâneos são caso previsto
// pelo ADR-025(b), não falha do servidor: o índice é o árbitro final, e o
// resultado correto é o segundo confirm reportar a linha como bloqueada.
func IsBlocked(err error) bool {
	return errors.Is(err, ErrDuplicateDedup)
}

// validarParesCanonicos reconfere, sobre as linhas JÁ MONTADAS com os ids
// canônicos, que nenhuma transferência ficou com as duas pernas na mesma
// conta.
//
// É a segunda metade de validarPares, e existe porque as duas rodam sobre
// dados diferentes: validarPares roda ANTES de qualquer consulta, sobre as
// strings que o chamador mandou; esta roda DEPOIS da canonização, sobre o id
// que o banco devolveu. Com a caixa do id trocada, `"<UUID>"` e `"<uuid>"` são
// strings diferentes para o Go e a MESMA conta para o MySQL — o par passaria
// pela primeira e só a segunda o pega.
//
// Confere SÓ a igualdade de conta: as outras invariantes do par (duas pernas,
// uma de cada sentido, mesmo valor, mesmo dia) não dependem da identidade da
// conta e já foram decididas antes, sobre dados que a canonização não toca.
func validarParesCanonicos(linhas []Transaction) error {
	grupos := map[string][]int{}
	for i := range linhas {
		if !linhas[i].IsTransfer() || linhas[i].TransferGroupID == nil {
			continue
		}
		grupos[*linhas[i].TransferGroupID] = append(grupos[*linhas[i].TransferGroupID], i)
	}
	for _, indices := range grupos {
		if len(indices) != 2 {
			// Não deveria acontecer (validarPares já exigiu o par), e se
			// acontecer o erro é o mesmo: meia transferência não existe.
			return fmt.Errorf("%w: cada transferência tem exatamente duas pernas", ErrBrokenTransfer)
		}
		if linhas[indices[0]].AccountID == linhas[indices[1]].AccountID {
			return fmt.Errorf("%w: origem e destino precisam ser contas diferentes", ErrBrokenTransfer)
		}
	}
	return nil
}
