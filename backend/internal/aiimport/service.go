package aiimport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/aiprompt"
	"github.com/brunorblanck/homefinance/backend/internal/audit"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// Service é a prévia e a confirmação do import de palavras-chave (spec 0010
// §4, entrega E9b).
//
// Repare no construtor: ele RECEBE Transactor e Auditor — este é o irmão que
// escreve. E repare em como escreve: nunca pelos repositórios. Categoria
// nasce por category.Service.Create (o MESMO caminho do POST /categories,
// §10.4) e palavra entra por AppendKeywords dos dois serviços — a única
// extração que a spec autorizou, e ADITIVA: só as aprovadas são inseridas,
// nada é apagado. Os repositórios entram só como LEITURA, para o índice em
// memória.
type Service struct {
	categories     CategoryStore
	categoryWriter CategoryWriter
	accounts       AccountStore
	accountWriter  AccountWriter
	ledger         Ledger
	tx             Transactor
	audit          Auditor
	lg             *slog.Logger

	// work é o orçamento de trabalho da medição de impacto. Campo, e não a
	// constante direta, para o teste exercitar o estouro sem montar 5.000
	// palavras de verdade; a opção só APERTA (ver WithWorkBudget).
	work int64
}

// Option configura o Service.
type Option func(*Service)

// WithAudit liga o rastro de auditoria da execução. Sem ele o serviço
// funciona — os testes de unidade do plano não precisam de auditoria.
func WithAudit(a Auditor) Option { return func(s *Service) { s.audit = a } }

// WithWorkBudget troca o orçamento de trabalho da medição de impacto (teste).
// Valor menor que 1 mantém textmatch.MaxMatchWork, e o valor é limitado por
// ele: uma opção de teste que afrouxasse o teto de produção seria um teto que
// depende de ninguém chamá-la errado.
func WithWorkBudget(work int64) Option {
	return func(s *Service) {
		if work >= 1 {
			s.work = min(work, textmatch.MaxMatchWork)
		}
	}
}

// NewService monta o serviço. As seis dependências são posicionais de
// propósito: esquecer uma vira erro de COMPILAÇÃO, e não um confirm que grava
// sem transação ou um plano sem contas.
func NewService(categories CategoryStore, categoryWriter CategoryWriter,
	accounts AccountStore, accountWriter AccountWriter,
	ledger Ledger, tx Transactor, lg *slog.Logger, opts ...Option,
) *Service {
	if lg == nil {
		lg = slog.Default()
	}
	s := &Service{
		categories: categories, categoryWriter: categoryWriter,
		accounts: accounts, accountWriter: accountWriter,
		ledger: ledger, tx: tx, lg: lg,
		work: textmatch.MaxMatchWork,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Preview responde POST /ai/keyword-import/preview: roda TODAS as validações,
// mede o impacto das palavras de conta e não escreve uma linha — nem
// categoria, nem palavra-chave, nem auditoria.
func (s *Service) Preview(ctx context.Context, ator Actor, in Input) (Report, error) {
	return s.executar(ctx, ator, in, false)
}

// Confirm responde POST /ai/keyword-import/confirm: revalida tudo DO ZERO
// sobre um índice lido dentro da transação, grava grupos novos → subcategorias
// novas → palavras-chave numa transação só, e audita.
func (s *Service) Confirm(ctx context.Context, ator Actor, in Input) (Report, error) {
	return s.executar(ctx, ator, in, true)
}

// executar é o caminho ÚNICO das duas rotas. `apply` é a única diferença:
// com ele, o índice é lido dentro da transação e o plano é aplicado; sem
// ele, o plano é medido. É assim que se obtém a garantia de que prévia e
// confirmação não divergem — não há um segundo validador para divergir.
//
// Ordem das guardas — nada vai ao banco com entrada não validada:
//
//  1. casa do TOKEN (vazia é ErrUnauthenticated: defesa em profundidade);
//  2. a janela, pelo MESMO validador do export (aiprompt.CompetenceWindow);
//  3. a FORMA do payload (versão, listas, tetos) — 400, nunca relatório;
//  4. a janela agregada — o denominador `periodTransactions` das duas rotas e
//     a matéria da medição de impacto. Fora da transação, porque é leitura
//     pura e porque falhar aqui (422 por volume) tem de acontecer ANTES de
//     qualquer lock;
//  5. só então o índice, o plano e — no confirm — a aplicação.
func (s *Service) executar(ctx context.Context, ator Actor, in Input, apply bool) (Report, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return Report{}, ErrUnauthenticated
	}
	meses, err := aiprompt.CompetenceWindow(in.FromMonth, in.ToMonth)
	if err != nil {
		return Report{}, err
	}
	if err := validarForma(in); err != nil {
		return Report{}, err
	}

	linhas, err := s.ledger.GroupByDescription(ctx, householdID, meses, transaction.MaxDescriptionGroupRows)
	if err != nil {
		return Report{}, fmt.Errorf("agrupando lançamentos da janela: %w", err)
	}
	periodo, err := dobrarPeriodo(linhas)
	if err != nil {
		return Report{}, err
	}

	if !apply {
		ix, err := s.carregarIndice(ctx, householdID)
		if err != nil {
			return Report{}, err
		}
		p := planejar(ix, in)
		if err := medirImpacto(p, periodo, s.work); err != nil {
			return Report{}, err
		}
		return p.relatorio(periodo.total), nil
	}

	var p *plano
	err = s.tx.Do(ctx, func(ctx context.Context) error {
		// O índice é lido DENTRO da transação: é o retrato contra o qual a
		// escrita vai acontecer, e o Create/AppendKeywords reconferem o mesmo
		// estado por dentro (teto, nome, dona da palavra). O que mudar entre
		// esta leitura e a escrita vira 409.
		ix, err := s.carregarIndice(ctx, householdID)
		if err != nil {
			return err
		}
		p = planejar(ix, in)
		if err := s.aplicar(ctx, ator, p); err != nil {
			return err
		}
		// UMA entrada por execução, com a CASA como entidade (achado A8 da
		// emenda §10): é ela a ORIGEM das `category.created`,
		// `category.updated` e `account.updated` que os serviços já gravaram
		// nesta mesma transação. Sem palavra, sem contagem, sem descrição.
		return s.registrar(ctx, ator)
	})
	if err != nil {
		return Report{}, err
	}
	return p.relatorio(periodo.total), nil
}

// validarForma reconfere no serviço o que o schema descreve e não faz valer:
// versão, as três listas, e os tetos de entradas e de palavras. Cada recusa
// aponta o CAMPO, nunca o conteúdo.
func validarForma(in Input) error {
	pl := in.Payload
	if pl.Version == nil || *pl.Version != FormatVersion {
		return &PayloadError{Field: "payload.homefinanceKeywordImport"}
	}
	if len(pl.NewCategories) == 0 && len(pl.CategoryKeywords) == 0 && len(pl.AccountKeywords) == 0 {
		return &PayloadError{Field: "payload"}
	}
	if len(pl.NewCategories) > MaxEntriesPerList {
		return &PayloadError{Field: "payload.newCategories"}
	}
	if len(pl.CategoryKeywords) > MaxEntriesPerList {
		return &PayloadError{Field: "payload.categoryKeywords"}
	}
	if len(pl.AccountKeywords) > MaxEntriesPerList {
		return &PayloadError{Field: "payload.accountKeywords"}
	}
	if len(in.SkipNewCategories) > MaxSkipRefs {
		return &PayloadError{Field: "skipNewCategories"}
	}
	for i := range pl.NewCategories {
		if len(pl.NewCategories[i].Add) > MaxKeywordsPerEntry {
			return &PayloadError{Field: fmt.Sprintf("payload.newCategories[%d].add", i)}
		}
	}
	for i := range pl.CategoryKeywords {
		if len(pl.CategoryKeywords[i].Add) > MaxKeywordsPerEntry {
			return &PayloadError{Field: fmt.Sprintf("payload.categoryKeywords[%d].add", i)}
		}
	}
	for i := range pl.AccountKeywords {
		if len(pl.AccountKeywords[i].Add) > MaxKeywordsPerEntry {
			return &PayloadError{Field: fmt.Sprintf("payload.accountKeywords[%d].add", i)}
		}
	}
	return nil
}

// carregarIndice lê as QUATRO listagens da casa do token e monta o índice —
// nunca uma consulta por entrada. includeArchived é TRUE nas duas listas: é
// a arquivada que responde `item_archived` e `name_taken_archived`.
func (s *Service) carregarIndice(ctx context.Context, householdID string) (*indiceDaCasa, error) {
	cats, err := s.categories.List(ctx, householdID, true)
	if err != nil {
		return nil, fmt.Errorf("carregando categorias da casa: %w", err)
	}
	catKws, err := s.categories.ListKeywords(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("carregando palavras-chave de categoria: %w", err)
	}
	accs, err := s.accounts.List(ctx, householdID, true)
	if err != nil {
		return nil, fmt.Errorf("carregando contas da casa: %w", err)
	}
	accKws, err := s.accounts.ListKeywords(ctx, householdID)
	if err != nil {
		return nil, fmt.Errorf("carregando palavras-chave de conta: %w", err)
	}
	ix := montarIndice(householdID, cats, catKws, accs, accKws)

	// UM aviso por requisição, com CONTAGENS e nada mais: linhas que as
	// fontes devolveram e o índice recusou (outra casa, excluída, órfã). Não
	// deveriam existir; se existirem, o log é o único lugar em que alguém
	// fica sabendo.
	if n := ix.linhasDeOutraCasaIgnoradas + ix.categoriasSemPaiIndexavel +
		ix.palavrasDeCategoriaOrfas + ix.palavrasDeContaOrfas; n > 0 {
		s.lg.WarnContext(ctx, "índice do import de IA descartou linhas inconsistentes",
			slog.Int("fora_da_casa", ix.linhasDeOutraCasaIgnoradas),
			slog.Int("folhas_sem_pai", ix.categoriasSemPaiIndexavel),
			slog.Int("palavras_orfas", ix.palavrasDeCategoriaOrfas+ix.palavrasDeContaOrfas),
		)
	}
	return ix, nil
}

// aplicar grava o plano, dentro da transação aberta por executar, na ordem
// que a spec manda: grupos novos → subcategorias novas → palavras-chave.
//
// QUE ERRO VIRA DESFECHO E QUAL DERRUBA O LOTE — a regra, escrita aqui porque
// é a decisão mais delicada do arquivo:
//
//   - category.ErrTooMany de Create → `household_limit`, e o lote CONTINUA.
//     É o único erro que Create devolve ANTES de qualquer escrita (a
//     contagem vem primeiro), então a transação não está poluída — e no
//     PostgreSQL só um COMANDO que falha aborta a transação; um erro de
//     domínio devolvido por Go não é um comando falho;
//   - category.ErrNameTaken e ErrKeywordTaken (de Create ou de
//     AppendKeywords), ErrKeywordsOnGroupWithChildren, ErrTooManyKeywords e
//     ErrNotFound de AppendKeywords → ABORTA, 409 CONFLICT. Todos foram
//     pré-conferidos pelo índice lido nesta mesma transação — o plano é uma
//     simulação do lote, então nenhum deles é causado pelo próprio lote;
//     chegar aqui é escrita concorrente, e a resposta certa é desfazer o lote
//     e pedir prévia nova;
//   - ErrTooDeep, ErrInvalidKind, ErrInvalidName e ErrNotFound de Create →
//     ABORTA, 500. Também pré-conferidos; chegar aqui é BUG deste arquivo, e
//     bug falha fechado;
//   - qualquer outro erro (banco, contexto) → ABORTA, 500, com o detalhe no
//     log.
//
// Regra geral: capturar erro de domínio e seguir dentro de transação aberta
// só é seguro quando está PROVADO que nada foi escrito. O primeiro caso é o
// único com essa prova.
func (s *Service) aplicar(ctx context.Context, ator Actor, p *plano) error {
	catAtor := category.Actor{HouseholdID: ator.HouseholdID, UserID: ator.UserID, IP: ator.IP}
	accAtor := account.Actor{HouseholdID: ator.HouseholdID, UserID: ator.UserID, IP: ator.IP}

	// 1. Grupos novos: continentes, sem palavra-chave nenhuma. Keywords nil
	//    de propósito — todas as palavras entram por UM caminho (AppendKeywords).
	for i := range p.gruposNovos {
		g := &p.gruposNovos[i]
		if !g.necessario {
			// Planejado só por entradas que a pessoa desmarcou: ocupou as
			// vagas e deu a natureza na simulação, mas nada nasce por ele.
			continue
		}
		v, err := s.categoryWriter.Create(ctx, catAtor, category.CreateInput{Name: g.name, Kind: g.kind})
		switch {
		case err == nil:
			g.id = v.ID
		case errors.Is(err, category.ErrTooMany):
			g.semVaga = true
		default:
			return erroDeCriacao("grupo", err)
		}
	}

	// 2. Subcategorias novas, na ordem do JSON, cada uma sob o seu grupo —
	//    o existente (id do índice) ou o que acabou de nascer.
	for i := range p.novas {
		nova := &p.novas[i]
		if nova.view.Outcome != OutcomeCreated {
			continue
		}
		pai := nova.grupoID
		if nova.grupoNovo >= 0 {
			g := &p.gruposNovos[nova.grupoNovo]
			if g.semVaga {
				semVaga(nova)
				continue
			}
			pai = g.id
		}
		if pai == "" {
			return fmt.Errorf("%w: subcategoria sem grupo resolvido", errPlanoInconsistente)
		}
		v, err := s.categoryWriter.Create(ctx, catAtor, category.CreateInput{Name: nova.nomeParaGravar, ParentID: &pai})
		switch {
		case err == nil:
			nova.view.CategoryID = ptr(v.ID)
			p.donas[nova.dona].id = v.ID
		case errors.Is(err, category.ErrTooMany):
			semVaga(nova)
		default:
			return erroDeCriacao("subcategoria", err)
		}
	}

	// 3. Palavras-chave, dona a dona, na ordem em que apareceram no JSON:
	//    SÓ as aprovadas, por AppendKeywords. Nada da lista atual é reescrito
	//    — a numeração de Position e o teto são relidos no banco pelo
	//    repositório, na mesma transação (achado A2).
	for _, chave := range p.ordemDona {
		d := p.donas[chave]
		if len(d.aprovadas) == 0 {
			continue
		}
		if d.id == "" {
			// Categoria nova que não nasceu (`household_limit` de corrida):
			// as palavras dela não têm para onde ir, e o relatório já diz.
			continue
		}
		if d.tipo == ItemTypeAccount {
			if err := s.accountWriter.AppendKeywords(ctx, accAtor, d.id, novasDeConta(d), audit.ActionAccountUpdated); err != nil {
				return erroDeGravacao("conta", err)
			}
			continue
		}
		if err := s.categoryWriter.AppendKeywords(ctx, catAtor, d.id, novasDeCategoria(d), audit.ActionCategoryUpdated); err != nil {
			return erroDeGravacao("categoria", err)
		}
	}
	return nil
}

// semVaga rebaixa uma entrada `created` para `household_limit` quando o teto
// foi atingido entre o plano e o Create (corrida): as palavras dela não
// entram, e o relatório diz o que DE FATO aconteceu.
func semVaga(nova *entradaNova) {
	nova.view.Outcome = OutcomeHouseholdLimit
	nova.view.CategoryID = nil
	nova.view.Add = []string{}
	nova.view.Skipped = []SkippedKeyword{}
	nova.view.Rejected = []RejectedKeyword{}
}

// novasDeCategoria monta SÓ as palavras aprovadas da dona, na ordem do JSON.
// Position é a ordem relativa; quem numera de verdade é o repositório, que
// continua a numeração lida no banco.
func novasDeCategoria(d *donaPlanejada) []category.Keyword {
	out := make([]category.Keyword, 0, len(d.aprovadas))
	for i, a := range d.aprovadas {
		out = append(out, category.Keyword{Keyword: a.keyword, Norm: a.norm, Position: i})
	}
	return out
}

// novasDeConta é novasDeCategoria para conta.
func novasDeConta(d *donaPlanejada) []account.Keyword {
	out := make([]account.Keyword, 0, len(d.aprovadas))
	for i, a := range d.aprovadas {
		out = append(out, account.Keyword{Keyword: a.keyword, Norm: a.norm, Position: i})
	}
	return out
}

// erroDeCriacao classifica o erro de Create que NÃO é ErrTooMany (ver
// aplicar): corrida vira ErrConflict; regra pré-conferida vira bug; o resto
// sobe como veio.
func erroDeCriacao(oQue string, err error) error {
	switch {
	case errors.Is(err, category.ErrNameTaken), errors.Is(err, category.ErrKeywordTaken):
		return fmt.Errorf("%w: criando %s: %w", ErrConflict, oQue, err)
	case errors.Is(err, category.ErrTooDeep), errors.Is(err, category.ErrInvalidKind),
		errors.Is(err, category.ErrInvalidName), errors.Is(err, category.ErrNotFound):
		return fmt.Errorf("%w: criando %s: %w", errPlanoInconsistente, oQue, err)
	default:
		return fmt.Errorf("criando %s do import: %w", oQue, err)
	}
}

// erroDeGravacao classifica o erro de AppendKeywords: o que o índice
// pré-conferiu e mudou POR FORA é corrida (409) — a palavra tomada, a filha
// que nasceu, o teto de 20 que outra transação encheu, o item que sumiu; o
// que a validação de forma já barrou é bug (500); o resto sobe como veio.
func erroDeGravacao(oQue string, err error) error {
	switch {
	case errors.Is(err, category.ErrKeywordTaken), errors.Is(err, account.ErrKeywordTaken),
		errors.Is(err, category.ErrKeywordsOnGroupWithChildren),
		errors.Is(err, category.ErrTooManyKeywords), errors.Is(err, account.ErrTooManyKeywords),
		errors.Is(err, category.ErrNotFound), errors.Is(err, account.ErrNotFound):
		return fmt.Errorf("%w: gravando palavras-chave de %s: %w", ErrConflict, oQue, err)
	case errors.Is(err, category.ErrInvalidKeyword), errors.Is(err, account.ErrInvalidKeyword),
		errors.Is(err, category.ErrDuplicateKeyword), errors.Is(err, account.ErrDuplicateKeyword):
		return fmt.Errorf("%w: gravando palavras-chave de %s: %w", errPlanoInconsistente, oQue, err)
	default:
		return fmt.Errorf("gravando palavras-chave de %s no import: %w", oQue, err)
	}
}

// registrar grava a entrada de ORIGEM da execução, dentro da transação: se o
// rastro não couber, a escrita não vale.
func (s *Service) registrar(ctx context.Context, ator Actor) error {
	if s.audit == nil {
		return nil
	}
	return s.audit.Record(ctx, AuditParams{
		Action:      audit.ActionAiKeywordImportConfirmed,
		Entity:      audit.EntityHousehold,
		EntityID:    ator.HouseholdID,
		UserID:      ator.UserID,
		HouseholdID: ator.HouseholdID,
		IP:          ator.IP,
	})
}
