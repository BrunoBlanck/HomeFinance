// Package dedup decide, para cada linha de um arquivo importado, se ela é nova,
// repetida ou suspeita — e NÃO fala com banco nenhum.
//
// # Por que puro
//
// Analyze recebe as linhas do arquivo e a janela de lançamentos existentes JÁ
// CARREGADA, e devolve a classificação. O carregamento é do repositório
// (transaction.Repository.WindowForDedup, uma consulta só para o arquivo
// inteiro — §4.8 da spec 0004). Essa separação é o que torna os cinco cenários
// de aceite testáveis sem subir banco, e é o que permite ao confirm RECALCULAR
// tudo dentro da transação usando exatamente o mesmo código da prévia.
//
// # As quatro camadas, e a que mora aqui
//
//  1. hash do arquivo — AVISO, nunca bloqueio. Fica no serviço;
//  2. chave natural (o Identificador do banco) — aqui;
//  3. chave derivada COM ORDINAL — aqui, e é o ponto central;
//  4. índice único (household_id, dedup_key, dedup_ordinal) — no banco. É o
//     árbitro final: dois confirms simultâneos colidem nele, e o segundo
//     reporta "linha bloqueada" em vez de duplicar.
//
// # O ordinal, que é o que impede o sistema de engolir compra legítima
//
// A chave derivada óbvia — hash(conta, data, valor, descrição) — APAGA compra
// repetida em silêncio: dois cafés de R$ 11,00 no mesmo dia na mesma padaria
// são a mesma tupla e são dois gastos reais. Num app de dinheiro, sumir com um
// lançamento é pior do que duplicar um.
//
// A correção é o ordinal: a tupla não é única, a unicidade é (chave, ordinal), e
// o ordinal é a enésima ocorrência daquela tupla NA CASA — contado contra o
// banco (incluindo as linhas excluídas logicamente, que continuam ocupando a
// chave), nunca contra o arquivo. Contado por arquivo, acertaria por acidente
// com dois arquivos e erraria com três.
package dedup

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// WeakWindowDays é a folga de dias da marcação fraca.
//
// É o MESMO número que o repositório usa para carregar a janela
// (transaction.DedupWindowDays), e tem de continuar sendo: se a marcação olhar
// mais longe do que a consulta carregou, ela compara contra linhas que não
// foram trazidas e conclui "não há nada parecido" justamente onde havia.
const WeakWindowDays = transaction.DedupWindowDays

// Tetos de trabalho (§4.8 e §5.6 da spec 0004).
const (
	// MaxExistingRows é o teto da janela carregada. Acima disso o arquivo é
	// recusado com orientação de importar por período menor — melhor recusar
	// com explicação do que estourar memória.
	MaxExistingRows = 20_000

	// MaxFileRows espelha o teto de linhas de dados do csvtext.
	MaxFileRows = 10_000
)

// Erros do pacote. Todos são de PROGRAMAÇÃO ou de limite; nenhum carrega
// conteúdo de linha.
var (
	// ErrNoAccount — a conta de destino não foi informada. Ela entra em TODA
	// chave, então uma conta vazia produziria chaves que colidem entre contas.
	ErrNoAccount = errors.New("conta de destino não informada")

	// ErrNoInstitution — há linha com chave natural e nenhuma instituição. Um
	// id só é único dentro do emissor (§4.2).
	ErrNoInstitution = errors.New("instituição não informada para linha com chave natural")

	// ErrInvalidRow — linha do arquivo sem valor canônico válido. O parser já
	// deveria tê-la rejeitado; a guarda existe para o defeito falhar aqui, e
	// não virar uma chave calculada sobre lixo.
	ErrInvalidRow = errors.New("linha do arquivo com valor canônico inválido")

	// ErrWindowTooLarge — a janela de existentes passou de MaxExistingRows.
	ErrWindowTooLarge = errors.New("janela de deduplicação grande demais")

	// ErrTooManyRows — o arquivo passou de MaxFileRows.
	ErrTooManyRows = errors.New("arquivo com linhas demais para deduplicar")

	// ErrKeyFieldSeparator — um campo de chave que não é o último contém o
	// separador "|", o que tornaria a concatenação ambígua. Ver hashParts.
	ErrKeyFieldSeparator = errors.New("campo de chave contém o separador")
)

// Row é UMA linha do arquivo, já em valores CANÔNICOS do domínio.
//
// ⚠️ "Canônicos" quer dizer: exatamente o que será gravado em transactions. Em
// especial, DescriptionNorm tem de ser a descrição já sanitizada e já truncada
// em 140 — ver o aviso em DerivedKey, que é o erro mais caro e mais invisível
// desta entrega.
type Row struct {
	// Seq é a ordem da linha no arquivo (1-based). É por ele que o serviço
	// reencontra a linha; RowResult devolve o mesmo valor.
	Seq int

	// Kind é transaction.KindIncome ou transaction.KindExpense.
	Kind string

	OccurredOn civil.Date

	// AmountCents é SEMPRE POSITIVO — o sinal vive no Kind.
	AmountCents int64

	DescriptionNorm string

	// ExternalID é a chave natural quando o documento tem uma; nil quando não.
	ExternalID *string

	// IsCardPayment vem da classificação do parser
	// (importer.SuggestionCardPayment). Não altera valor nem kind: só o
	// veredito, e só quando a linha não colidiu com nada mais forte.
	IsCardPayment bool

	// IsInternalTransfer vem da correspondência por palavra-chave de CONTA
	// (spec 0005 §4.2.1, calculada pelo importer com classify.Set): a
	// descrição bateu com outra conta ativa da casa. Como IsCardPayment, não
	// altera valor nem kind — só o veredito, e só quando a linha não colidiu
	// com nada mais forte. Pagamento de fatura vence quando os dois vêm
	// marcados: ele é mais específico e tem a sua própria liberação.
	IsInternalTransfer bool
}

// ExternalUse localiza um external_id já usado em OUTRA conta da casa.
type ExternalUse struct {
	TransactionID string
	AccountID     string
}

// Input é tudo de que Analyze precisa. Nada aqui vem do cliente: a casa e a
// conta já foram validadas pelo serviço, e os existentes vieram do repositório.
type Input struct {
	// Institution é a instituição do PARSER que ganhou a detecção (não a que o
	// usuário marcou na conta). Obrigatória quando alguma linha tem chave
	// natural.
	Institution string

	// AccountID é a conta de DESTINO, já validada como da casa.
	AccountID string

	// Rows são as linhas aproveitadas do arquivo, NA ORDEM do arquivo. Linhas
	// rejeitadas pelo parser não entram aqui — elas não têm valor canônico e,
	// portanto, não têm chave; o serviço as grava direto com StatusRejected.
	Rows []Row

	// Existing é a janela já carregada pelo repositório
	// (WindowForDedup), INCLUINDO as linhas excluídas logicamente.
	//
	// ⚠️ Incluir as excluídas não é detalhe: o índice único conta com elas, e
	// um ordinal calculado ignorando-as colidiria no INSERT. E se o chamador
	// tratar o erro de WindowForDedup como "janela vazia", a análise conclui
	// "nada é duplicado" e grava o arquivo inteiro de novo.
	Existing []transaction.DedupRow

	// ExistingByKey são as ocorrências localizadas PELA CHAVE NATURAL, SEM
	// janela de datas (transaction.Repository.RowsByDedupKeys). Pode ser nil.
	//
	// ⚠️ Ela não é "mais um jeito de carregar a janela": é a única defesa
	// contra o furo que a chave natural tem por construção. A chave natural NÃO
	// embute a data, mas Existing vem filtrado por occurred_on. Quando a data
	// da mesma transação muda mais do que WeakWindowDays entre dois downloads,
	// a gêmea já gravada fica FORA de Existing, a linha volta como StatusNew —
	// que entra por DEFAULT, sem marcação nenhuma — e o ordinal vira 2, que o
	// índice único aceita de bom grado. A mesma transação entra duas vezes, em
	// silêncio, que é exatamente o que esta entrega existe para impedir.
	//
	// A chave DERIVADA não precisa disto: ela embute occurredOn, então toda
	// gêmea dela está necessariamente dentro da janela.
	//
	// Só alimenta porChave. Ela é uma projeção de IDENTIDADE (id, chave,
	// ordinal, deleted_at) e não tem valor nem data para participar da marcação
	// fraca — o tipo estreito é o que garante que ninguém a use como se fosse
	// uma linha inteira.
	ExistingByKey []transaction.DedupKeyRow

	// ExternalIDsElsewhere mapeia external_id → onde ele já foi usado em contas
	// da casa DIFERENTES da de destino. Alimenta a segunda metade da marcação
	// fraca ("esta linha já foi importada na conta X"). Pode ser nil.
	ExternalIDsElsewhere map[string]ExternalUse
}

// RowResult é o veredito de uma linha.
type RowResult struct {
	// Seq é o mesmo Seq da Row de entrada.
	Seq int

	Status Status

	// DedupKey é a chave canônica calculada para a linha. Vai para o staging e,
	// se a linha entrar, para transactions.dedup_key sem recálculo.
	DedupKey string

	// Ordinal é o ordinal que a linha OCUPARÁ se for importada.
	//
	// Ele é reservado para TODA linha da tupla, inclusive as barradas, e é por
	// isso que ele não é "o próximo livre": a pessoa pode liberar qualquer
	// subconjunto das linhas marcadas, e dois ordinais iguais colidiriam no
	// índice único. Numerar em sequência a partir de max(existente)+1 garante
	// que nenhuma combinação de liberações colida.
	//
	// O valor definitivo é reatribuído dentro da transação do confirm (§4.3):
	// entre a prévia e a confirmação, o outro morador pode ter importado.
	Ordinal int

	// MatchTransactionID é o lançamento existente que motivou a marcação. É um
	// PONTEIRO para a pessoa conferir, não um veredito.
	MatchTransactionID *string

	// Import é o default do status: a linha entra sem decisão explícita?
	Import bool
}

// Result é a classificação do arquivo inteiro.
type Result struct {
	// Rows está na mesma ordem de Input.Rows.
	Rows []RowResult

	// Counts tem uma entrada para CADA status da taxonomia, inclusive as
	// zeradas, para a resposta da API ter forma estável. StatusRejected fica em
	// zero aqui: ele não é produzido por Analyze (ver status.go).
	Counts map[Status]int
}

// Importable conta as linhas que entrariam sem nenhuma decisão da pessoa.
func (r Result) Importable() int {
	n := 0
	for _, row := range r.Rows {
		if row.Import {
			n++
		}
	}
	return n
}

// chaveValor é o balde da marcação fraca: mesmo tipo e mesmo valor.
//
// O tipo entra no balde porque um estorno de R$ 29,00 no mesmo dia da compra de
// R$ 29,00 não é duplicata dela — é o par natural de compra e devolução, e
// marcá-lo faria a marcação fraca gritar exatamente onde o dado está certo.
type chaveValor struct {
	kind  string
	cents int64
}

// Analyze classifica cada linha do arquivo.
//
// A ordem de precedência, da mais forte para a mais fraca, e o motivo de cada
// degrau:
//
//  1. colisão de (chave, ordinal) com uma linha existente — é a garantia dura,
//     a mesma que o índice único aplica no banco;
//  2. chave NATURAL repetida dentro do arquivo — a mesma garantia dura, só que
//     a gêmea é a 1ª ocorrência do próprio arquivo (a escrita recusa ordinal
//     > 1 em chave natural, e a classificação tem de dizer o mesmo);
//  3. pagamento de fatura — é específico e tem uma liberação própria
//     ("registrar como transferência"), então ganha do genérico;
//  4. transferência interna (palavra-chave de outra conta bateu — spec 0005)
//     — também tem liberação própria, e perde só para o pagamento de fatura,
//     que é mais específico;
//  5. marcação fraca — heurística, barrada mas liberável;
//  6. repetido no arquivo (só chave DERIVADA) / novo — entram por default.
//
// Analyze nunca produz StatusTransferAlreadyRegistered: esse depende das
// pernas já gravadas, que o importer carrega e pareia depois desta função.
func Analyze(in Input) (Result, error) {
	if in.AccountID == "" {
		return Result{}, ErrNoAccount
	}
	if err := validarCamposDeChave(in); err != nil {
		return Result{}, err
	}
	if len(in.Rows) > MaxFileRows {
		return Result{}, fmt.Errorf("%w: %d linhas (o máximo é %d)", ErrTooManyRows, len(in.Rows), MaxFileRows)
	}
	if len(in.Existing) > MaxExistingRows {
		return Result{}, fmt.Errorf(
			"%w: %d lançamentos na janela (o máximo é %d)", ErrWindowTooLarge, len(in.Existing), MaxExistingRows)
	}
	if len(in.ExistingByKey) > MaxExistingRows {
		// O mesmo teto, pelo mesmo motivo: o que é carregado para a memória
		// tem limite, e recusar com explicação é melhor do que estourar.
		return Result{}, fmt.Errorf(
			"%w: %d ocorrências por chave (o máximo é %d)", ErrWindowTooLarge, len(in.ExistingByKey), MaxExistingRows)
	}

	porChave, porValor := indexar(in.Existing, in.ExistingByKey)

	res := Result{
		Rows:   make([]RowResult, 0, len(in.Rows)),
		Counts: zerarContadores(),
	}

	// vistasNoArquivo conta quantas linhas DESTE arquivo já usaram cada chave.
	vistasNoArquivo := make(map[string]int, len(in.Rows))

	for _, r := range in.Rows {
		if err := validarLinha(r); err != nil {
			return Result{}, err
		}

		chave, err := chaveDe(in, r)
		if err != nil {
			return Result{}, err
		}

		existentes := porChave[chave]
		maiorOrdinal := 0
		if n := len(existentes); n > 0 {
			maiorOrdinal = existentes[n-1].DedupOrdinal
		}

		vistasNoArquivo[chave]++
		i := vistasNoArquivo[chave] // i-ésima ocorrência da tupla NESTE arquivo

		out := RowResult{
			Seq:      r.Seq,
			DedupKey: chave,
			Ordinal:  maiorOrdinal + i,
		}

		switch {
		case i <= len(existentes):
			// A i-ésima ocorrência do arquivo casa com a i-ésima ocorrência já
			// gravada. Comparar por POSIÇÃO na lista ordenada, e não pelo valor
			// do ordinal, é o que mantém a conta certa quando um ordinal ficou
			// vago porque a linha correspondente foi barrada numa importação
			// anterior.
			gemea := existentes[i-1]
			id := gemea.ID
			out.MatchTransactionID = &id
			if gemea.DeletedAt != nil {
				out.Status = StatusDuplicateDeleted
			} else {
				out.Status = StatusDuplicateExact
			}

		case i > 1 && temChaveNatural(r):
			// A 2ª ocorrência da MESMA chave natural dentro do arquivo é a
			// mesma transação da 1ª: o identificador do emissor é único por
			// transação, e "segunda ocorrência legítima" só existe na chave
			// derivada (dois cafés iguais são dois gastos). É a mesma regra
			// que transaction.Service.CreateBatch aplica ao recusar ordinal > 1
			// em chave natural — e as duas camadas TÊM de concordar: se esta
			// devolvesse `repetido_no_arquivo` (que entra por default), a
			// escrita recusaria a linha e desfaria o lote inteiro, inclusive as
			// linhas inocentes, com a tela dizendo que "outra importação gravou
			// primeiro" — que seria falso, e reconfirmar repetiria para sempre.
			//
			// `duplicado_exato`, sem gêmea no banco: a gêmea é a 1ª ocorrência
			// do próprio arquivo, que entra neste mesmo confirm. Barrada e não
			// liberável — liberar não adiantaria, a escrita recusaria de
			// qualquer jeito. Vem ANTES do pagamento de fatura pelo mesmo
			// motivo da colisão com o banco: é garantia dura, não heurística.
			out.Status = StatusDuplicateExact

		case r.IsCardPayment:
			out.Status = StatusCardPayment

		case r.IsInternalTransfer:
			// Palavra-chave de OUTRA conta da casa bateu (spec 0005 §4.2.1).
			// Vem depois do pagamento de fatura (mais específico, com a sua
			// própria liberação) e antes da marcação fraca: assim como o
			// pagamento de fatura, esta classificação tem a liberação que
			// resolve o caso — registrar o par —, e a heurística genérica não
			// deve escondê-la. A contraparte sugerida vive no importer; aqui só
			// o veredito.
			out.Status = StatusInternalTransfer

		default:
			if uso, ok := usoEmOutraConta(in, r); ok {
				out.Status = StatusPossibleDuplicate
				id := uso.TransactionID
				out.MatchTransactionID = &id
			} else if gemea, ok := marcacaoFraca(porValor, r); ok {
				out.Status = StatusPossibleDuplicate
				id := gemea.ID
				out.MatchTransactionID = &id
			} else if i > 1 {
				out.Status = StatusRepeatedInFile
			} else {
				out.Status = StatusNew
			}
		}

		out.Import = out.Status.DefaultImports()
		res.Rows = append(res.Rows, out)
		res.Counts[out.Status]++
	}

	return res, nil
}

// indexar monta os dois índices sobre os existentes.
//
// Dois índices, e não uma varredura por linha: um arquivo de 10.000 linhas
// contra uma janela de 20.000 lançamentos daria 200 milhões de comparações — o
// custo apareceria exatamente onde a pessoa está esperando a tela de revisão.
//
// porChave recebe DUAS fontes: a janela de datas e as ocorrências achadas pela
// chave natural fora dela. A fusão é por ID, e a deduplicação por ID não é
// zelo: as duas fontes se sobrepõem na maioria dos arquivos (a gêmea que está
// DENTRO da janela vem pelas duas), e contá-la duas vezes faria a 2ª ocorrência
// legítima de uma compra repetida ser marcada como duplicata — sumindo com um
// gasto real, que é o erro que este pacote considera pior do que duplicar.
func indexar(
	existentes []transaction.DedupRow,
	porChaveNatural []transaction.DedupKeyRow,
) (map[string][]transaction.DedupRow, map[chaveValor][]transaction.DedupRow) {
	porChave := make(map[string][]transaction.DedupRow)
	porValor := make(map[chaveValor][]transaction.DedupRow)
	vistos := make(map[string]struct{}, len(existentes)+len(porChaveNatural))

	for _, e := range existentes {
		if e.DedupKey != "" {
			porChave[e.DedupKey] = append(porChave[e.DedupKey], e)
			vistos[e.ID] = struct{}{}
		}
		// A marcação fraca só olha linhas VIVAS: bloquear por causa de um
		// lançamento que a pessoa apagou seria devolver a ela, como suspeita, a
		// decisão que ela já tomou.
		if e.DeletedAt == nil {
			k := chaveValor{kind: e.Kind, cents: e.AmountCents}
			porValor[k] = append(porValor[k], e)
		}
	}

	// Só porChave: estas linhas são projeção de IDENTIDADE e não têm valor nem
	// data. Em porValor elas marcariam contra zeros — e contra data zero, toda
	// distância é grande demais, então ali elas só fariam mal.
	for _, e := range porChaveNatural {
		if e.DedupKey == "" {
			continue
		}
		if _, repetida := vistos[e.ID]; repetida {
			continue
		}
		vistos[e.ID] = struct{}{}
		porChave[e.DedupKey] = append(porChave[e.DedupKey], transaction.DedupRow{
			ID:           e.ID,
			DedupKey:     e.DedupKey,
			DedupOrdinal: e.DedupOrdinal,
			DeletedAt:    e.DeletedAt,
		})
	}

	// A ordenação é o que torna o resultado REPRODUZÍVEL: sem ela, a
	// classificação passaria a depender da ordem em que o banco devolveu as
	// linhas, e a mesma prévia daria respostas diferentes em execuções
	// diferentes.
	for k := range porChave {
		slices.SortFunc(porChave[k], ordenarPorOrdinal)
	}
	for k := range porValor {
		slices.SortFunc(porValor[k], ordenarPorData)
	}

	return porChave, porValor
}

func ordenarPorOrdinal(a, b transaction.DedupRow) int {
	if a.DedupOrdinal != b.DedupOrdinal {
		return a.DedupOrdinal - b.DedupOrdinal
	}
	return comparaTexto(a.ID, b.ID)
}

func ordenarPorData(a, b transaction.DedupRow) int {
	if c := a.OccurredOn.Compare(b.OccurredOn); c != 0 {
		return c
	}
	return comparaTexto(a.ID, b.ID)
}

func comparaTexto(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// temChaveNatural informa se a linha foi identificada PELO DOCUMENTO — o
// Identificador que o emissor imprime (§4.2 da spec 0004).
//
// É a MESMA pergunta que transaction.Service faz na escrita, e feita do mesmo
// jeito: sobre o ExternalID, e não sobre a forma da chave, porque a chave é um
// hash e de fora dela não dá para saber que tipo ela é. As duas cópias têm de
// responder igual — é o que mantém a classificação e a escrita de acordo sobre
// o que é "repetição legítima" e o que é "a mesma transação".
func temChaveNatural(r Row) bool {
	return r.ExternalID != nil && *r.ExternalID != ""
}

// chaveDe escolhe entre chave natural e derivada.
func chaveDe(in Input, r Row) (string, error) {
	if temChaveNatural(r) {
		if in.Institution == "" {
			return "", ErrNoInstitution
		}
		return NaturalKey(in.Institution, in.AccountID, *r.ExternalID), nil
	}
	return DerivedKey(in.AccountID, r.Kind, r.OccurredOn, r.AmountCents, r.DescriptionNorm), nil
}

// NaturalKeys devolve as chaves NATURAIS distintas das linhas, na ordem em que
// aparecem no arquivo.
//
// Existe para o serviço perguntar ao repositório "estas chaves já estão
// ocupadas?" SEM janela de datas, e vive AQUI — e não no serviço — porque a
// chave é calculada em UM lugar só. Um segundo caminho de cálculo é um caminho
// que um dia diverge, e chave divergente não dá erro: ela simplesmente não
// encontra a gêmea, e a deduplicação para de funcionar em silêncio.
//
// Devolve vazio quando não há o que perguntar: sem instituição ou sem conta não
// existe chave natural possível (Analyze recusa a entrada logo em seguida), e
// com o separador num campo do meio a chave seria ambígua — a mesma invariante
// que validarCamposDeChave cobra.
func NaturalKeys(institution, accountID string, rows []Row) []string {
	if institution == "" || accountID == "" ||
		strings.Contains(institution, fieldSep) || strings.Contains(accountID, fieldSep) {
		return nil
	}

	vistas := make(map[string]struct{}, len(rows))
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.ExternalID == nil || *r.ExternalID == "" {
			continue
		}
		chave := NaturalKey(institution, accountID, *r.ExternalID)
		if _, repetida := vistas[chave]; repetida {
			continue
		}
		vistas[chave] = struct{}{}
		out = append(out, chave)
	}
	return out
}

// usoEmOutraConta é a metade da marcação fraca que cobre "importei na conta
// errada".
//
// Ela só existe porque a conta entra na chave natural (§4.2): sem a conta na
// chave, importar na conta errada bloquearia a conta certa para sempre; com a
// conta na chave, a segunda importação passa — e esta marcação é o que mostra
// ao usuário o que aconteceu, em vez de deixá-lo com a linha duplicada em duas
// contas sem aviso nenhum.
func usoEmOutraConta(in Input, r Row) (ExternalUse, bool) {
	if r.ExternalID == nil || *r.ExternalID == "" || len(in.ExternalIDsElsewhere) == 0 {
		return ExternalUse{}, false
	}
	uso, ok := in.ExternalIDsElsewhere[*r.ExternalID]
	if !ok || uso.AccountID == in.AccountID {
		// Mesma conta não é "outra conta": se o id já está nesta conta, quem
		// responde é a chave natural, com a garantia dura.
		return ExternalUse{}, false
	}
	return uso, true
}

// marcacaoFraca procura um lançamento vivo com o mesmo tipo e o mesmo valor, a
// até WeakWindowDays de distância, com descrição DIFERENTE.
//
// A condição "descrição diferente" é o que separa esta heurística da chave
// derivada — e é ela que salva o caso patológico: o arquivo A trazia um café de
// R$ 11,00 e o arquivo B traz dois. A segunda linha de B tem a MESMA descrição
// do café já gravado, então a marcação fraca não a pega, e ela entra como
// ocorrência nova. Sem essa condição, as duas seriam barradas e um gasto real
// sumiria.
//
// Devolve a gêmea de data mais próxima, para o link da tela apontar para o
// lançamento que a pessoa está pensando.
func marcacaoFraca(porValor map[chaveValor][]transaction.DedupRow, r Row) (transaction.DedupRow, bool) {
	candidatas := porValor[chaveValor{kind: r.Kind, cents: r.AmountCents}]

	var melhor transaction.DedupRow
	melhorDistancia := -1

	for _, e := range candidatas {
		if e.DescriptionNorm == r.DescriptionNorm {
			continue
		}
		d := diasEntre(e.OccurredOn, r.OccurredOn)
		if d > WeakWindowDays {
			continue
		}
		if melhorDistancia < 0 || d < melhorDistancia {
			melhor, melhorDistancia = e, d
		}
	}

	return melhor, melhorDistancia >= 0
}

// validarCamposDeChave mantém a invariante que torna a chave inequívoca: só o
// último campo de cada chave pode conter o separador (ver hashParts).
//
// Os demais campos não precisam de guarda: kind vem de allowlist, a data tem
// formato fixo e o valor é um inteiro. Os dois que passam por aqui são os únicos
// que chegam como texto de outra camada.
func validarCamposDeChave(in Input) error {
	if strings.Contains(in.AccountID, fieldSep) {
		return fmt.Errorf("%w: conta", ErrKeyFieldSeparator)
	}
	if strings.Contains(in.Institution, fieldSep) {
		return fmt.Errorf("%w: instituição", ErrKeyFieldSeparator)
	}
	return nil
}

func validarLinha(r Row) error {
	switch {
	case r.Seq <= 0:
		return fmt.Errorf("%w: sem ordem no arquivo", ErrInvalidRow)
	case r.Kind != transaction.KindIncome && r.Kind != transaction.KindExpense:
		// Só receita e despesa chegam aqui. A perna de transferência nasce no
		// confirm e usa PairKey, que é única por construção e nunca barra nada.
		return fmt.Errorf("%w: linha %d com kind %q", ErrInvalidRow, r.Seq, r.Kind)
	case r.AmountCents <= 0:
		// O valor é sempre positivo; o sinal vive no kind (ADR-003). Um valor
		// zero ou negativo aqui quer dizer que a convenção de sinal do parser
		// não foi aplicada.
		return fmt.Errorf("%w: linha %d com valor não positivo", ErrInvalidRow, r.Seq)
	case r.OccurredOn.IsZero():
		return fmt.Errorf("%w: linha %d sem data", ErrInvalidRow, r.Seq)
	default:
		return nil
	}
}

func zerarContadores() map[Status]int {
	todos := []Status{
		StatusNew, StatusRepeatedInFile, StatusDuplicateExact, StatusDuplicateDeleted,
		StatusPossibleDuplicate, StatusCardPayment, StatusInternalTransfer,
		StatusTransferAlreadyRegistered, StatusRejected,
	}
	m := make(map[Status]int, len(todos))
	for _, s := range todos {
		m[s] = 0
	}
	return m
}

// diasEntre devolve a distância absoluta em dias entre duas datas civis.
//
// A conta é inteira (algoritmo days_from_civil, de Howard Hinnant) e não passa
// por time.Time: uma data civil não tem hora nem fuso, e atravessar time.Time
// para subtrair traz de volta as duas coisas que o tipo existe para não ter.
//
// A função está duplicada em importer.DaysBetween de propósito: este pacote é
// FOLHA e não pode importar a raiz de importer, que vai importá-lo no serviço.
// São dez linhas de aritmética fechada, cada cópia com o seu teste.
func diasEntre(a, b civil.Date) int {
	d := diasDesdeEpoca(a) - diasDesdeEpoca(b)
	if d < 0 {
		return -d
	}
	return d
}

func diasDesdeEpoca(d civil.Date) int {
	y, m, dia := d.Year(), d.Month(), d.Day()
	if m <= 2 {
		y--
	}
	era := y / 400
	if y < 0 {
		era = (y - 399) / 400
	}
	yoe := y - era*400                     // [0, 399]
	mp := (m + 9) % 12                     // março = 0
	doy := (153*mp+2)/5 + dia - 1          // [0, 365]
	doe := yoe*365 + yoe/4 - yoe/100 + doy // [0, 146096]
	// 719468 move a origem do algoritmo (0000-03-01) para a época Unix.
	return era*146097 + doe - 719468
}
