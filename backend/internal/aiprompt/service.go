package aiprompt

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// errAgregacaoInconsistente — uma linha agregada veio com contagem ou total
// NEGATIVO, ou a soma delas não coube em int64.
//
// Não é entrada do usuário: `COUNT(*)` nunca é negativo e `amount_cents` é
// positivo por invariante do domínio, então só banco adulterado chega aqui.
// Falha FECHADA — 500 genérico, contagem no log, nenhum número inventado no
// prompt. Clampar em silêncio esconderia a corrupção E publicaria um total
// errado num texto que a pessoa vai levar para fora.
var errAgregacaoInconsistente = errors.New("agregação por descrição fora da faixa representável")

// Service monta o prompt do menu IA.
//
// Repare no que o construtor NÃO recebe: não há `Transactor` e não há
// `Auditor`. É a trava da E9a (§10.3 da spec 0010) — sem eles, este serviço
// não tem como escrever nem como auditar, e "o export não escreve" deixa de
// ser promessa para virar tipo.
type Service struct {
	ledger     Ledger
	categories Categories
	accounts   Accounts
	lg         *slog.Logger

	clock func() time.Time
}

// Option configura o Service.
type Option func(*Service)

// WithClock injeta o relógio (teste). O prompt carrega o instante da geração,
// e um teste que compara texto precisa de um instante fixo.
func WithClock(c func() time.Time) Option { return func(s *Service) { s.clock = c } }

// NewService monta o serviço. As três fontes são posicionais de propósito:
// esquecer uma vira erro de COMPILAÇÃO, e não um prompt silenciosamente sem
// contas.
//
// O logger serve a UM propósito: o aviso AGREGADO de anomalia de dado (linha
// agregada apontando para categoria que não é da casa) — um por requisição,
// com a contagem e nada mais. Nunca descrição, nunca centavos, nunca nome
// (docs/SEGURANCA.md §4).
func NewService(ledger Ledger, categories Categories, accounts Accounts, lg *slog.Logger, opts ...Option) *Service {
	if lg == nil {
		lg = slog.Default()
	}
	s := &Service{ledger: ledger, categories: categories, accounts: accounts, lg: lg}
	for _, o := range opts {
		o(s)
	}
	if s.clock == nil {
		s.clock = func() time.Time { return time.Now().UTC() }
	}
	return s
}

// ExportPrompt responde GET /api/v1/ai/export-prompt (spec 0010 §3, E9a).
//
// Ordem das guardas — nada vai ao banco com entrada não validada:
//
//  1. casa do TOKEN (vazia é ErrUnauthenticated: defesa em profundidade, e uma
//     consulta com household_id vazio seria a porta do BOLA);
//  2. forma dos dois meses, por transaction.ParseMonth — o MESMO validador do
//     painel, do relatório e da listagem, para todas as telas recusarem
//     exatamente as mesmas strings. Nada é normalizado;
//  3. sentido e tamanho da janela (invertida, longa demais);
//  4. taxonomia e contas da casa;
//  5. só então a agregação.
//
// Os passos 4 e 5 nessa ordem não são estética: é da taxonomia que sai o mapa
// de natureza que deriva o `kindGroup`, e é da lista de contas que sai o que
// pode ser NOMEADO. Sem eles carregados antes, a dobra teria de consultar de
// novo — e duas leituras separadas podem discordar sob escrita concorrente.
func (s *Service) ExportPrompt(ctx context.Context, ator Actor, in ExportInput) (PromptView, error) {
	householdID := ator.HouseholdID
	if householdID == "" {
		return PromptView{}, ErrUnauthenticated
	}

	meses, err := janelaDeCompetencia(in)
	if err != nil {
		return PromptView{}, err
	}

	tax, err := s.carregarTaxonomia(ctx, householdID)
	if err != nil {
		return PromptView{}, err
	}
	contas, err := s.carregarContas(ctx, householdID)
	if err != nil {
		return PromptView{}, err
	}

	// UMA consulta agregada. O teto é o rail de MEMÓRIA do processo
	// (transaction.MaxDescriptionGroupRows); passar dele é tudo-ou-nada, e o
	// erro sobe embrulhado até o handler traduzir em 422.
	linhas, err := s.ledger.GroupByDescription(ctx, householdID, meses, transaction.MaxDescriptionGroupRows)
	if err != nil {
		return PromptView{}, fmt.Errorf("agrupando lançamentos por descrição: %w", err)
	}

	movimentos, corte, err := s.dobrar(ctx, linhas, tax, contas)
	if err != nil {
		return PromptView{}, err
	}

	nonce, err := sortearNonce()
	if err != nil {
		return PromptView{}, err
	}

	geradoEm := s.clock().UTC()
	texto := montarPrompt(dadosDoPrompt{
		fromMonth:  in.FromMonth,
		toMonth:    in.ToMonth,
		geradoEm:   geradoEm,
		nonce:      nonce,
		contas:     contas.lista,
		grupos:     tax.grupos,
		categorias: tax.categorias,
		movimentos: movimentos,
		truncadas:  corte.truncadas,
	})

	return PromptView{
		Prompt:      texto,
		FromMonth:   in.FromMonth,
		ToMonth:     in.ToMonth,
		GeneratedAt: geradoEm.Format(time.RFC3339),
		Stats: StatsView{
			Accounts:              len(contas.lista),
			Categories:            len(tax.categorias),
			Descriptions:          len(movimentos),
			Transactions:          corte.lancamentos,
			TruncatedDescriptions: corte.truncadas,
		},
	}, nil
}

// --- a janela ---------------------------------------------------------------

// janelaDeCompetencia valida os dois meses e devolve a lista FECHADA de meses
// que vai para o `IN (?, ?, ?)` do repositório.
//
// Ela existe porque a consulta é por IGUALDADE, e não por faixa: sobre uma
// coluna de TEXTO, `BETWEEN` dependeria da collation de cada dialeto (ver
// gormstore.GroupByDescription). Enumerar aqui é o que permite aquela escolha,
// e é por isso que o teto de 3 meses é regra de domínio e não de tela.
func janelaDeCompetencia(in ExportInput) ([]string, error) {
	return CompetenceWindow(in.FromMonth, in.ToMonth)
}

// CompetenceWindow é a janela de trabalho do menu IA (spec 0010 §2.1): valida
// `fromMonth`/`toMonth` e devolve a lista fechada de meses de competência.
//
// Exportada para `internal/aiimport`, que mede o impacto das palavras-chave
// de conta sobre a MESMA janela que o prompt exportou — "um conceito só, sem
// três datas diferentes se contradizendo". Os erros são os quatro sentinelas
// deste pacote (ErrInvalidFromMonth, ErrInvalidToMonth, ErrWindowInverted,
// ErrWindowTooLong), para as duas rotas recusarem exatamente as mesmas strings
// com exatamente o mesmo campo apontado. Nada é normalizado.
func CompetenceWindow(fromMonth, toMonth string) ([]string, error) {
	de, err := transaction.ParseMonth(fromMonth)
	if err != nil {
		return nil, ErrInvalidFromMonth
	}
	ate, err := transaction.ParseMonth(toMonth)
	if err != nil {
		return nil, ErrInvalidToMonth
	}

	primeiro, ultimo := indiceDoMes(de), indiceDoMes(ate)
	if ultimo < primeiro {
		return nil, ErrWindowInverted
	}
	if n := ultimo - primeiro + 1; n > transaction.MaxCompetenceMonthsInWindow {
		return nil, ErrWindowTooLong
	}

	meses := make([]string, 0, ultimo-primeiro+1)
	for i := primeiro; i <= ultimo; i++ {
		meses = append(meses, mesDoIndice(i))
	}
	return meses, nil
}

// indiceDoMes achata (ano, mês) num inteiro monotônico. É aritmética de
// inteiros de propósito: comparar e contar meses com `time.AddDate` traz fuso
// e dias de mês de volta para dentro de uma conta que não tem dia nenhum.
func indiceDoMes(d civil.Date) int { return d.Year()*12 + (d.Month() - 1) }

// mesDoIndice é a volta de indiceDoMes, no formato "AAAA-MM" que a coluna
// competence_month guarda.
func mesDoIndice(i int) string { return fmt.Sprintf("%04d-%02d", i/12, i%12+1) }

// --- taxonomia --------------------------------------------------------------

// taxonomiaDaCasa é o que o prompt sabe sobre as categorias da casa.
type taxonomiaDaCasa struct {
	// naturezaPorID cobre TODAS as categorias da casa, arquivadas inclusive.
	// É a base do predicado de investimento (ADR-031f): arquivar não desfaz a
	// marcação do passado.
	naturezaPorID map[string]string

	// caminhoPorID só tem as ATIVAS: é o conjunto do que pode ser NOMEADO no
	// texto (critério 6 da spec 0010).
	caminhoPorID map[string]string

	// grupos são os nomes dos grupos ativos, em ordem — a lista destacada da
	// seção 7, porque é nela que uma subcategoria nova se encaixa.
	grupos []string

	// categorias é a tabela da seção 7: grupos e folhas ativos, ordenados.
	categorias []categoriaDoPrompt
}

// carregarTaxonomia lê categorias e palavras-chave da casa do token em DUAS
// consultas — nunca duas por categoria.
//
// includeArchived é TRUE, e a lista é usada para dois fins diferentes (ver o
// doc de Categories.List). A casa é RECONFERIDA em Go, linha a linha: o
// repositório já filtra, e reconferir custa uma comparação — categoria de
// outra casa jamais entra num mapa que vira texto.
func (s *Service) carregarTaxonomia(ctx context.Context, householdID string) (taxonomiaDaCasa, error) {
	cats, err := s.categories.List(ctx, householdID, true)
	if err != nil {
		return taxonomiaDaCasa{}, fmt.Errorf("carregando categorias da casa: %w", err)
	}
	palavras, err := s.categories.ListKeywords(ctx, householdID)
	if err != nil {
		return taxonomiaDaCasa{}, fmt.Errorf("carregando palavras-chave de categoria: %w", err)
	}

	tax := taxonomiaDaCasa{
		naturezaPorID: make(map[string]string, len(cats)),
		caminhoPorID:  make(map[string]string, len(cats)),
	}

	// Passo 1: natureza de TODAS (o predicado de investimento) e o nome dos
	// grupos ATIVOS (o pai de uma folha nomeável).
	nomeDoGrupoAtivo := make(map[string]string, len(cats))
	for i := range cats {
		c := cats[i]
		if c.HouseholdID != householdID || c.ID == "" {
			continue
		}
		tax.naturezaPorID[c.ID] = c.Kind
		if c.ArchivedAt == nil && c.IsGroup() {
			nomeDoGrupoAtivo[c.ID] = c.Name
		}
	}

	// Passo 2: o caminho das ATIVAS. Folha cujo grupo não está ativo não é
	// nomeável — o estado não deveria existir (ErrParentArchived o impede na
	// escrita), e nomear a folha sem o grupo produziria um caminho pela
	// metade num texto que vai para fora.
	palavrasPorCategoria := agruparPalavrasDeCategoria(householdID, palavras)
	for i := range cats {
		c := cats[i]
		if c.HouseholdID != householdID || c.ID == "" || c.ArchivedAt != nil {
			continue
		}
		caminho := ""
		switch {
		case c.IsGroup():
			caminho = c.Name
		default:
			pai, ok := nomeDoGrupoAtivo[*c.ParentID]
			if !ok {
				continue
			}
			caminho = pai + separadorDeCaminho + c.Name
		}
		tax.caminhoPorID[c.ID] = caminho
		tax.categorias = append(tax.categorias, categoriaDoPrompt{
			ID:       c.ID,
			Caminho:  caminho,
			Natureza: c.Kind,
			Grupo:    c.IsGroup(),
			Palavras: palavrasPorCategoria[c.ID],
		})
		if c.IsGroup() {
			tax.grupos = append(tax.grupos, c.Name)
		}
	}

	// Ordem determinística por caminho NORMALIZADO: o mesmo banco gera o mesmo
	// prompt duas vezes, e a ordem não depende de acento nem de caixa.
	slices.SortFunc(tax.categorias, func(a, b categoriaDoPrompt) int {
		if d := strings.Compare(textnorm.Normalize(a.Caminho), textnorm.Normalize(b.Caminho)); d != 0 {
			return d
		}
		return strings.Compare(a.ID, b.ID)
	})
	slices.SortFunc(tax.grupos, func(a, b string) int {
		return strings.Compare(textnorm.Normalize(a), textnorm.Normalize(b))
	})
	return tax, nil
}

// agruparPalavrasDeCategoria indexa as palavras-chave por dona, na ORDEM DE
// CADASTRO (Position), que é a ordem em que a tela as mostra.
func agruparPalavrasDeCategoria(householdID string, kws []category.Keyword) map[string][]string {
	porDona := make(map[string][]category.Keyword, len(kws))
	for i := range kws {
		k := kws[i]
		if k.HouseholdID != householdID || k.CategoryID == "" {
			continue
		}
		porDona[k.CategoryID] = append(porDona[k.CategoryID], k)
	}
	out := make(map[string][]string, len(porDona))
	for id, lista := range porDona {
		slices.SortFunc(lista, func(a, b category.Keyword) int {
			if a.Position != b.Position {
				return a.Position - b.Position
			}
			return strings.Compare(a.Norm, b.Norm)
		})
		palavras := make([]string, 0, len(lista))
		for _, k := range lista {
			palavras = append(palavras, k.Keyword)
		}
		out[id] = palavras
	}
	return out
}

// --- contas -----------------------------------------------------------------

// contasDaCasa é o que o prompt sabe sobre as contas.
type contasDaCasa struct {
	// lista é a tabela da seção 6: só contas ATIVAS, ordenadas.
	lista []contaDoPrompt

	// nomePorID cobre as MESMAS contas de `lista`. Conta que não está aqui
	// (arquivada) não é nomeável na coluna "contas" da seção 8.
	nomePorID map[string]string
}

// carregarContas lê contas e palavras-chave da casa do token em DUAS
// consultas. includeArchived é FALSE — ver o doc de Accounts.List.
func (s *Service) carregarContas(ctx context.Context, householdID string) (contasDaCasa, error) {
	accs, err := s.accounts.List(ctx, householdID, false)
	if err != nil {
		return contasDaCasa{}, fmt.Errorf("carregando contas da casa: %w", err)
	}
	palavras, err := s.accounts.ListKeywords(ctx, householdID)
	if err != nil {
		return contasDaCasa{}, fmt.Errorf("carregando palavras-chave de conta: %w", err)
	}
	porDona := agruparPalavrasDeConta(householdID, palavras)

	out := contasDaCasa{nomePorID: make(map[string]string, len(accs))}
	for i := range accs {
		a := accs[i]
		// Mesma reconferência de casa da taxonomia, e pelo mesmo motivo.
		if a.HouseholdID != householdID || a.ID == "" || a.ArchivedAt != nil {
			continue
		}
		out.nomePorID[a.ID] = a.Name
		// QUATRO campos, e nada mais (§3.1 da spec 0010): id, nome, tipo e
		// palavras. `Institution`, `StatementClosingDay`, `StatementDueDay` e
		// `OpeningBalanceCents` existem na entidade e ficam FORA — o prompt vai
		// para fora da casa, e saldo e instituição não têm por que ir junto.
		out.lista = append(out.lista, contaDoPrompt{
			ID:       a.ID,
			Nome:     a.Name,
			Tipo:     a.Kind,
			Palavras: porDona[a.ID],
		})
	}
	slices.SortFunc(out.lista, func(x, y contaDoPrompt) int {
		if d := strings.Compare(textnorm.Normalize(x.Nome), textnorm.Normalize(y.Nome)); d != 0 {
			return d
		}
		return strings.Compare(x.ID, y.ID)
	})
	return out, nil
}

// agruparPalavrasDeConta indexa as palavras-chave de conta por dona, na ordem
// de cadastro.
func agruparPalavrasDeConta(householdID string, kws []account.Keyword) map[string][]string {
	porDona := make(map[string][]account.Keyword, len(kws))
	for i := range kws {
		k := kws[i]
		if k.HouseholdID != householdID || k.AccountID == "" {
			continue
		}
		porDona[k.AccountID] = append(porDona[k.AccountID], k)
	}
	out := make(map[string][]string, len(porDona))
	for id, lista := range porDona {
		slices.SortFunc(lista, func(a, b account.Keyword) int {
			if a.Position != b.Position {
				return a.Position - b.Position
			}
			return strings.Compare(a.Norm, b.Norm)
		})
		palavras := make([]string, 0, len(lista))
		for _, k := range lista {
			palavras = append(palavras, k.Keyword)
		}
		out[id] = palavras
	}
	return out
}

// --- a dobra ----------------------------------------------------------------

// resumoDaDobra são os números que a dobra apura além das linhas.
type resumoDaDobra struct {
	// truncadas são as descrições que ficaram de fora por
	// maxDescriptionsInPrompt.
	truncadas int
	// lancamentos são os lançamentos agrupados nas descrições que FICARAM —
	// o que o texto entrega, não o corpus.
	lancamentos int
}

// acumuladoDaDescricao junta, em memória, as linhas que o SQL separou por
// (kind, conta, categoria) e que o prompt mostra numa linha só.
type acumuladoDaDescricao struct {
	norm        string
	amostra     string
	ocorrencias int64
	totalCents  int64

	// tipos são os `kindGroup` distintos vistos nesta descrição. Mais de um
	// vira "vários": a mesma descrição pode ter sido classificada de formas
	// diferentes, e é essa divergência que o prompt precisa mostrar.
	tipos map[string]struct{}

	// contas são os ids das contas NOMEÁVEIS em que a descrição apareceu.
	contas map[string]struct{}

	// categorias são os ids das categorias NOMEÁVEIS (ativas) da descrição.
	categorias map[string]struct{}

	// semCategoriaNomeavel marca as ocorrências sem categoria E as de
	// categoria ARQUIVADA. As duas caem no mesmo balde de propósito: nenhuma
	// das duas pode ser nomeada no texto (critério 6), e a diferença entre
	// "sem categoria" e "com uma categoria que você não pode ver" não muda
	// nada do que a IA vai propor.
	semCategoriaNomeavel bool
}

// dobrar transforma as linhas agregadas nas linhas do prompt: uma por descrição
// normalizada, ordenada por ocorrências decrescente, cortada no teto.
//
// A ordenação AUTORITATIVA é aqui, em Go, e não no SQL: o `ORDER BY cnt DESC`
// do repositório existe por exigência do dialeto MSSQL e não desempata. Sem
// desempate estável, o mesmo banco geraria dois prompts diferentes.
func (s *Service) dobrar(ctx context.Context, linhas []transaction.DescriptionGroup,
	tax taxonomiaDaCasa, contas contasDaCasa,
) ([]movimentoDoPrompt, resumoDaDobra, error) {
	porNorma := make(map[string]*acumuladoDaDescricao, len(linhas))
	ordem := make([]*acumuladoDaDescricao, 0, len(linhas))
	categoriasDesconhecidas := 0

	for i := range linhas {
		l := linhas[i]
		// Falha FECHADA: COUNT(*) e SUM(amount_cents) não têm como ser
		// negativos no caminho de escrita deste produto.
		if l.Count < 0 || l.TotalCents < 0 {
			return nil, resumoDaDobra{}, fmt.Errorf("%w: linha com contagem ou total negativo", errAgregacaoInconsistente)
		}

		acc := porNorma[l.DescriptionNorm]
		if acc == nil {
			acc = &acumuladoDaDescricao{
				norm:       l.DescriptionNorm,
				amostra:    l.SampleDescription,
				tipos:      map[string]struct{}{},
				contas:     map[string]struct{}{},
				categorias: map[string]struct{}{},
			}
			porNorma[l.DescriptionNorm] = acc
			ordem = append(ordem, acc)
		}

		// A amostra é UMA das grafias do grupo e depende da collation do banco
		// (ver transaction.DescriptionGroup.SampleDescription). Escolher a
		// MENOR em Go não a torna um contrato — torna a escolha determinística
		// dado o mesmo conjunto de linhas, que é o que um teste pode exigir.
		if l.SampleDescription < acc.amostra {
			acc.amostra = l.SampleDescription
		}

		var ok bool
		if acc.ocorrencias, ok = somar(acc.ocorrencias, l.Count); !ok {
			return nil, resumoDaDobra{}, fmt.Errorf("%w: soma de ocorrências estourou", errAgregacaoInconsistente)
		}
		if acc.totalCents, ok = somar(acc.totalCents, l.TotalCents); !ok {
			return nil, resumoDaDobra{}, fmt.Errorf("%w: soma de centavos estourou", errAgregacaoInconsistente)
		}

		acc.tipos[grupoDeTipo(l.Kind, l.CategoryID, tax.naturezaPorID)] = struct{}{}

		if _, nomeavel := contas.nomePorID[l.AccountID]; nomeavel {
			acc.contas[l.AccountID] = struct{}{}
		}

		switch {
		case l.CategoryID == nil:
			acc.semCategoriaNomeavel = true
		default:
			if _, ativa := tax.caminhoPorID[*l.CategoryID]; ativa {
				acc.categorias[*l.CategoryID] = struct{}{}
			} else {
				acc.semCategoriaNomeavel = true
				if _, daCasa := tax.naturezaPorID[*l.CategoryID]; !daCasa {
					categoriasDesconhecidas++
				}
			}
		}
	}

	// UM aviso por requisição, com a CONTAGEM e nada mais. Avisar por linha
	// seria amplificação de log: quem tem o token recarrega a tela e multiplica
	// o volume sem custo.
	if categoriasDesconhecidas > 0 {
		s.lg.WarnContext(ctx, "linhas agregadas apontam para categoria fora da taxonomia da casa",
			slog.Int("linhas", categoriasDesconhecidas))
	}

	slices.SortFunc(ordem, func(a, b *acumuladoDaDescricao) int {
		// Ocorrências decrescente é a ordem que a spec manda (§3.1 item 8) e a
		// ordem do CORTE. As duas chaves seguintes só desempatam, e existem
		// para o corte ser reprodutível.
		if a.ocorrencias != b.ocorrencias {
			return int(sinal(b.ocorrencias - a.ocorrencias))
		}
		if a.totalCents != b.totalCents {
			return int(sinal(b.totalCents - a.totalCents))
		}
		return strings.Compare(a.norm, b.norm)
	})

	resumo := resumoDaDobra{}
	if len(ordem) > maxDescriptionsInPrompt {
		resumo.truncadas = len(ordem) - maxDescriptionsInPrompt
		ordem = ordem[:maxDescriptionsInPrompt]
	}

	movimentos := make([]movimentoDoPrompt, 0, len(ordem))
	for _, acc := range ordem {
		total, ok := somar(int64(resumo.lancamentos), acc.ocorrencias)
		if !ok || total > int64(maxInt) {
			return nil, resumoDaDobra{}, fmt.Errorf("%w: soma de lançamentos estourou", errAgregacaoInconsistente)
		}
		resumo.lancamentos = int(total)
		movimentos = append(movimentos, acc.publicar(tax, contas))
	}
	return movimentos, resumo, nil
}

// publicar converte o acumulado na linha exibível da seção 8.
func (a *acumuladoDaDescricao) publicar(tax taxonomiaDaCasa, contas contasDaCasa) movimentoDoPrompt {
	nomesDeConta := make([]string, 0, len(a.contas))
	for id := range a.contas {
		nomesDeConta = append(nomesDeConta, contas.nomePorID[id])
	}
	slices.SortFunc(nomesDeConta, func(x, y string) int {
		return strings.Compare(textnorm.Normalize(x), textnorm.Normalize(y))
	})

	// "o caminho quando todas as ocorrências têm a mesma; `várias` quando
	// divergem; `—` quando não tem" (§3.1 item 8). Uma categoria nomeável MAIS
	// ocorrências sem categoria também é divergência: são dois estados
	// diferentes na mesma descrição.
	categoria := semCategoria
	switch {
	case len(a.categorias) == 1 && !a.semCategoriaNomeavel:
		for id := range a.categorias {
			categoria = tax.caminhoPorID[id]
		}
	case len(a.categorias) > 1 || (len(a.categorias) == 1 && a.semCategoriaNomeavel):
		categoria = categoriaDivergente
	}

	tipo := tipoDivergente
	if len(a.tipos) == 1 {
		for t := range a.tipos {
			tipo = rotuloDeTipo(t)
		}
	}

	return movimentoDoPrompt{
		Descricao:   a.amostra,
		Ocorrencias: a.ocorrencias,
		TotalCents:  a.totalCents,
		Tipo:        tipo,
		Contas:      nomesDeConta,
		Categoria:   categoria,
	}
}

// grupoDeTipo deriva o `kindGroup` da linha agregada.
//
// O predicado de investimento é transaction.MarcadaComoInvestimento —
// IMPORTADO, nunca reescrito (ADR-031f). Uma segunda cópia divergiria da do
// painel e da listagem, e o mesmo lançamento apareceria como investimento numa
// tela e como despesa noutra.
//
// O mapa de naturezas inclui as ARQUIVADAS de propósito: arquivar uma
// categoria não desfaz a marcação do passado, e o `kindGroup` é uma palavra —
// derivá-lo de uma categoria arquivada não a NOMEIA.
func grupoDeTipo(kind string, categoryID *string, naturezaPorID map[string]string) string {
	switch kind {
	case transaction.KindTransferOut, transaction.KindTransferIn:
		return transaction.KindGroupTransfer
	}
	if categoryID != nil && transaction.MarcadaComoInvestimento(naturezaPorID[*categoryID]) {
		return transaction.KindGroupInvestment
	}
	switch kind {
	case transaction.KindIncome:
		return transaction.KindGroupIncome
	case transaction.KindExpense:
		return transaction.KindGroupExpense
	default:
		// Kind fora do conjunto fechado do domínio. Não inventa rótulo: cai no
		// mesmo "vários" da divergência, que é um texto honesto para "não sei".
		return tipoDivergente
	}
}

// somar soma dois inteiros não negativos e avisa se estourou. Os dois lados
// vêm de COUNT(*) e SUM(amount_cents) já conferidos como não negativos, então
// estouro só acontece com banco adulterado — e aí a resposta é falhar, não
// publicar um total errado.
func somar(a, b int64) (int64, bool) {
	s := a + b
	if s < a {
		return 0, false
	}
	return s, true
}

// sinal devolve -1, 0 ou 1. Existe para a comparação de int64 virar o int que
// slices.SortFunc espera sem passar por uma conversão que pode truncar.
func sinal(n int64) int64 {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}

// nonceBytes é o tamanho do nonce da cerca de dados da seção 8.
//
// 8 bytes (16 hex) não são uma chave: o nonce não autentica nada e não guarda
// segredo nenhum — ele só precisa ser IMPREVISÍVEL para quem escreveu a
// descrição dias antes, e distinto entre requisições. 2^64 fecha isso com
// folga absurda, e 16 caracteres não empurram a tabela da seção 8 para longe.
const nonceBytes = 8

// sortearNonce devolve o identificador da cerca de dados (ver
// marcadorDeAbertura, em prompt.go).
//
// crypto/rand, e não math/rand: o valor precisa ser imprevisível para um
// terceiro que escolheu o texto de uma descrição, e um gerador semeado por
// relógio é adivinhável justamente por quem tem tempo de sobra. Falha do
// gerador é falha FECHADA — erro genérico e nenhum prompt —, porque um nonce
// previsível é uma cerca que não cerca, e entregá-la assim seria pior do que
// não entregar.
func sortearNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("sorteando o delimitador da seção de dados: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// maxInt é o maior valor de `int` na plataforma. Serve à guarda de conversão
// do contador de lançamentos, que sai como `int` no DTO.
const maxInt = int(^uint(0) >> 1)
