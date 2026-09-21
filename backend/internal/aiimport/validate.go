package aiimport

import (
	"slices"
	"strings"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/aiprompt"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/id"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// Este arquivo é o que a spec 0010 chama de validação (§§4.2–4.3): funções
// PURAS sobre um índice em memória, sem I/O. É aqui que a maior parte dos
// testes bate, e é por ser puro que a prévia e o confirm não têm como
// divergir — os dois chamam `planejar` sobre o mesmo índice, e só o confirm
// aplica o plano.

// separadorDeCaminho é o ` > ` do `ref` e do caminho exibível. O mesmo que o
// prompt usa, para o `categoryPath` que a IA copiou do export bater com o
// que este lado reconstrói.
const separadorDeCaminho = " > "

// prefixoDeDonaNova marca a chave de uma categoria que ainda NÃO existe no
// plano das donas. Um uuid nunca começa assim, então a chave sintética não
// colide com id nenhum.
const prefixoDeDonaNova = "new:"

// --- o índice ----------------------------------------------------------------

// categoriaIndexada é o que o plano sabe de UMA categoria da casa.
type categoriaIndexada struct {
	id       string
	name     string
	nameNorm string
	kind     string
	parentID *string
	archived bool
}

func (c *categoriaIndexada) grupo() bool { return c.parentID == nil }

// contaIndexada é o que o plano sabe de UMA conta da casa.
type contaIndexada struct {
	id       string
	name     string
	nameNorm string
	archived bool
}

// indiceDaCasa é o retrato da casa no instante do plano — montado de UMA
// listagem de categorias (arquivadas inclusive), uma de palavras de
// categoria, uma de contas e uma de palavras de conta. É o padrão que a
// semente provou (category/seed.go): responde `name_taken_archived`,
// `merged_into_existing`, `group_has_children`, o teto de 200 e o dono de
// cada palavra sem uma consulta por entrada.
//
// A casa é RECONFERIDA linha a linha na montagem (docs/SEGURANCA.md §2): o
// repositório já filtra por household_id, e reconferir custa uma comparação —
// uma linha de outra casa jamais entra num índice que decide escrita.
type indiceDaCasa struct {
	householdID string

	// totalCategorias conta grupos e folhas NÃO EXCLUÍDAS, arquivadas
	// inclusive — é exatamente o que category.Repository.CountAll conta, e é
	// contra ele que o teto de 200 é conferido.
	totalCategorias int

	categoriasPorID map[string]*categoriaIndexada

	// gruposPorNorm indexa os grupos pelo nome normalizado. Quando uma casa
	// tem um grupo ATIVO e um ARQUIVADO com o mesmo nome (NameTaken só
	// enxerga as ativas, então o estado é legítimo), o ativo vence: é ele que
	// responde `merged`/herança, e só na ausência de ativo o arquivado
	// responde `name_taken_archived`.
	gruposPorNorm map[string]*categoriaIndexada

	// folhasPorPai indexa as folhas por (id do pai, nome normalizado), com a
	// mesma preferência pela ativa.
	folhasPorPai map[string]map[string]*categoriaIndexada

	// filhasAtivasPorGrupo é o que responde `group_has_children` (spec 0005
	// §12): grupo com subcategoria ativa não recebe lançamento, logo não
	// recebe palavra.
	filhasAtivasPorGrupo map[string]int

	// palavrasDeCategoria são as palavras ATUAIS de cada categoria, na ordem
	// de cadastro; donoDaPalavraDeCategoria é o índice único da casa em
	// memória (norm -> categoria dona), arquivadas inclusive — a arquivada
	// continua ocupando a vaga.
	palavrasDeCategoria        map[string][]category.Keyword
	donoDaPalavraDeCategoria   map[string]string
	contasPorID                map[string]*contaIndexada
	palavrasDeConta            map[string][]account.Keyword
	donoDaPalavraDeConta       map[string]string
	categoriasSemPaiIndexavel  int
	palavrasDeCategoriaOrfas   int
	palavrasDeContaOrfas       int
	linhasDeOutraCasaIgnoradas int
}

// montarIndice constrói o índice a partir das quatro listagens. É puro: não
// consulta nada, só reorganiza o que já veio.
func montarIndice(householdID string, cats []category.Category, catKws []category.Keyword,
	accs []account.Account, accKws []account.Keyword,
) *indiceDaCasa {
	ix := &indiceDaCasa{
		householdID:              householdID,
		categoriasPorID:          make(map[string]*categoriaIndexada, len(cats)),
		gruposPorNorm:            make(map[string]*categoriaIndexada),
		folhasPorPai:             make(map[string]map[string]*categoriaIndexada),
		filhasAtivasPorGrupo:     make(map[string]int),
		palavrasDeCategoria:      make(map[string][]category.Keyword),
		donoDaPalavraDeCategoria: make(map[string]string, len(catKws)),
		contasPorID:              make(map[string]*contaIndexada, len(accs)),
		palavrasDeConta:          make(map[string][]account.Keyword),
		donoDaPalavraDeConta:     make(map[string]string, len(accKws)),
	}

	for i := range cats {
		c := cats[i]
		if c.HouseholdID != householdID || c.ID == "" || c.DeletedAt != nil {
			ix.linhasDeOutraCasaIgnoradas++
			continue
		}
		ix.totalCategorias++
		entrada := &categoriaIndexada{
			id: c.ID, name: c.Name, nameNorm: c.NameNorm, kind: c.Kind,
			parentID: c.ParentID, archived: c.ArchivedAt != nil,
		}
		ix.categoriasPorID[c.ID] = entrada
	}
	for _, c := range ix.categoriasPorID {
		if c.grupo() {
			ix.gruposPorNorm[c.nameNorm] = preferirAtiva(ix.gruposPorNorm[c.nameNorm], c)
			continue
		}
		pai := *c.parentID
		if _, ok := ix.categoriasPorID[pai]; !ok {
			// Folha cujo pai não é desta casa (ou não existe): o estado não
			// deveria existir — Create resolve o pai na casa do token — e a
			// folha fica de fora de qualquer caminho. Contada para o aviso
			// agregado do serviço, nunca nomeada.
			ix.categoriasSemPaiIndexavel++
			continue
		}
		irmas := ix.folhasPorPai[pai]
		if irmas == nil {
			irmas = make(map[string]*categoriaIndexada)
			ix.folhasPorPai[pai] = irmas
		}
		irmas[c.nameNorm] = preferirAtiva(irmas[c.nameNorm], c)
		if !c.archived {
			ix.filhasAtivasPorGrupo[pai]++
		}
	}

	for i := range catKws {
		k := catKws[i]
		if k.HouseholdID != householdID || k.CategoryID == "" || k.Norm == "" {
			ix.linhasDeOutraCasaIgnoradas++
			continue
		}
		if _, ok := ix.categoriasPorID[k.CategoryID]; !ok {
			// Palavra de categoria excluída: a exclusão apaga as palavras na
			// mesma transação, então isto é banco inconsistente. Ela continua
			// ocupando a vaga no índice único, e por isso continua contando
			// como dona — mas não é palavra "do item" de ninguém listável.
			ix.palavrasDeCategoriaOrfas++
		}
		ix.palavrasDeCategoria[k.CategoryID] = append(ix.palavrasDeCategoria[k.CategoryID], k)
		if _, ocupada := ix.donoDaPalavraDeCategoria[k.Norm]; !ocupada {
			ix.donoDaPalavraDeCategoria[k.Norm] = k.CategoryID
		}
	}
	for dona := range ix.palavrasDeCategoria {
		slices.SortStableFunc(ix.palavrasDeCategoria[dona], func(a, b category.Keyword) int {
			if a.Position != b.Position {
				return a.Position - b.Position
			}
			return strings.Compare(a.ID, b.ID)
		})
	}

	for i := range accs {
		a := accs[i]
		if a.HouseholdID != householdID || a.ID == "" || a.DeletedAt != nil {
			ix.linhasDeOutraCasaIgnoradas++
			continue
		}
		ix.contasPorID[a.ID] = &contaIndexada{
			id: a.ID, name: a.Name, nameNorm: a.NameNorm, archived: a.ArchivedAt != nil,
		}
	}
	for i := range accKws {
		k := accKws[i]
		if k.HouseholdID != householdID || k.AccountID == "" || k.Norm == "" {
			ix.linhasDeOutraCasaIgnoradas++
			continue
		}
		if _, ok := ix.contasPorID[k.AccountID]; !ok {
			ix.palavrasDeContaOrfas++
		}
		ix.palavrasDeConta[k.AccountID] = append(ix.palavrasDeConta[k.AccountID], k)
		if _, ocupada := ix.donoDaPalavraDeConta[k.Norm]; !ocupada {
			ix.donoDaPalavraDeConta[k.Norm] = k.AccountID
		}
	}
	for dona := range ix.palavrasDeConta {
		slices.SortStableFunc(ix.palavrasDeConta[dona], func(a, b account.Keyword) int {
			if a.Position != b.Position {
				return a.Position - b.Position
			}
			return strings.Compare(a.ID, b.ID)
		})
	}
	return ix
}

// preferirAtiva escolhe, entre duas categorias de mesmo nome no mesmo escopo,
// a que responde pelo nome: a ativa. Sem ativa, a arquivada; sem nada, a nova.
func preferirAtiva(atual, nova *categoriaIndexada) *categoriaIndexada {
	if atual == nil || (atual.archived && !nova.archived) {
		return nova
	}
	return atual
}

// caminhoNorm é o caminho NORMALIZADO de uma categoria existente — o que o
// `categoryPath` do JSON precisa igualar depois de normalizado.
func (ix *indiceDaCasa) caminhoNorm(c *categoriaIndexada) string {
	if c.grupo() {
		return c.nameNorm
	}
	if pai, ok := ix.categoriasPorID[*c.parentID]; ok {
		return pai.nameNorm + separadorDeCaminho + c.nameNorm
	}
	return c.nameNorm
}

// caminhoExibivel é o caminho `Grupo > Folha` na forma do SERVIDOR — o que
// vai em `items[].name`, nunca a string do JSON.
func (ix *indiceDaCasa) caminhoExibivel(c *categoriaIndexada) string {
	if c.grupo() {
		return c.name
	}
	if pai, ok := ix.categoriasPorID[*c.parentID]; ok {
		return pai.name + separadorDeCaminho + c.name
	}
	return c.name
}

// --- o plano -------------------------------------------------------------------

// grupoPlanejado é um grupo que NASCE nesta operação: continente das folhas
// novas, sem palavra-chave nenhuma (spec 0010 §4.3).
type grupoPlanejado struct {
	norm string
	name string
	kind string

	// id é preenchido pelo confirm depois do Create; semVaga marca o grupo
	// que o Create recusou por teto (estado mudou) — as folhas dele caem em
	// `household_limit`.
	id      string
	semVaga bool

	// necessario marca o grupo que alguma folha NÃO DESMARCADA precisa. Um
	// grupo planejado só por entradas desmarcadas continua ocupando as vagas
	// e dando a natureza às irmãs na simulação, mas não é gravado: nada
	// nasce por causa de uma entrada que a pessoa tirou.
	necessario bool
}

// entradaNova é uma entrada de `newCategories` depois do plano.
type entradaNova struct {
	view NewCategoryView

	// simulado é o desfecho que a entrada TERIA sem o pulo da pessoa — o
	// que a simulação do lote usa. view.Outcome é o publicado: igual ao
	// simulado, ou `skipped_by_user`.
	simulado string

	// pulada marca a entrada que a pessoa desmarcou. Ela continua ocupando o
	// lugar dela na simulação (vagas, grupo, ambiguidade, teto) e só deixa
	// de ESCREVER — ver o doc de planejar.
	pulada bool

	// nomeParaGravar é o nome da folha na forma que o servidor grava
	// (NormalizeName). Só em `created`.
	nomeParaGravar string

	// grupoNovo aponta o grupo planejado (índice em plano.gruposNovos) quando
	// ele nasce aqui; grupoID é o id do grupo EXISTENTE caso contrário.
	grupoNovo int
	grupoID   string

	// dona é a chave da dona das palavras desta entrada no plano (id da
	// categoria existente em `merged`, chave sintética em `created`); vazia
	// quando a entrada não recebe palavra.
	dona string
}

// itemPlanejado é uma entrada de `categoryKeywords`/`accountKeywords` depois
// do plano.
type itemPlanejado struct {
	view ItemView
	dona string // id do item; vazio quando a entrada inteira foi recusada
}

// palavraCandidata é uma palavra que passou pelas regras 6–8 e 10 da §4.2 e
// aguarda as regras 9 (ambiguidade) e 11 (teto). Ela lembra de QUEM veio,
// para o desfecho voltar à linha certa do relatório.
type palavraCandidata struct {
	keyword string
	norm    string
	dona    string
	tipo    string

	// pulada marca a palavra de uma entrada desmarcada: disputa ambiguidade
	// e teto como qualquer outra, mas não é aprovada para escrita.
	pulada bool

	// destino são as listas do relatório da entrada que a carregou — para
	// uma entrada pulada, listas descartáveis.
	destino *listasDeRelatorio
}

// listasDeRelatorio são as três listas por entrada, apontadas por ponteiro
// porque NewCategoryView e ItemView as têm com o mesmo formato.
type listasDeRelatorio struct {
	added    *[]string
	skipped  *[]SkippedKeyword
	rejected *[]RejectedKeyword
}

// donaPlanejada acumula o que vai ser GRAVADO em cada dona: as palavras
// atuais (para `already_present` e para o teto) e as aprovadas — só estas
// são escritas, por AppendKeywords.
type donaPlanejada struct {
	tipo string
	// id é o id da dona existente. Vazio em categoria nova até o confirm
	// criá-la; se continuar vazio na hora de gravar, a categoria não nasceu
	// (`household_limit` de corrida) e as palavras não têm para onde ir.
	id string

	existentesDeCategoria []category.Keyword
	existentesDeConta     []account.Keyword

	// aprovadas são as palavras que entram, na ordem do JSON; normasAceitas
	// é o conjunto delas MAIS as reservadas, para a mesma palavra em duas
	// entradas da mesma dona virar `already_present` na segunda.
	aprovadas     []palavraAprovada
	normasAceitas map[string]struct{}

	// reservadas são as vagas do teto tomadas por palavras de entradas
	// DESMARCADAS: ocupam o lugar na simulação (o item seguinte da mesma
	// dona vê o mesmo teto que a prévia viu) e não são escritas.
	reservadas int
}

// palavraAprovada é uma palavra que ENTRA: a forma exibível validada e a
// forma normalizada. O id, a casa, a dona e o instante são do serviço de
// destino (category/account.Service), nunca daqui.
type palavraAprovada struct {
	keyword string
	norm    string
}

func (d *donaPlanejada) totalAtual() int {
	if d.tipo == ItemTypeAccount {
		return len(d.existentesDeConta) + len(d.aprovadas) + d.reservadas
	}
	return len(d.existentesDeCategoria) + len(d.aprovadas) + d.reservadas
}

// plano é a saída de `planejar`: tudo o que o confirm precisa aplicar e tudo
// o que o relatório precisa mostrar, calculado uma vez.
type plano struct {
	novas       []entradaNova
	itens       []itemPlanejado
	gruposNovos []grupoPlanejado

	donas     map[string]*donaPlanejada
	ordemDona []string

	// gruposQueGanhamFolha são os grupos EXISTENTES sob os quais o próprio
	// lote cria uma folha (`created`). É a parte do "estado depois do lote"
	// que a regra 5b precisa enxergar: um grupo que passa a ter filha ativa
	// não recebe palavra, e é o lote quem lhe dá a filha.
	gruposQueGanhamFolha map[string]struct{}
}

// planejar aplica as §§4.2–4.3 da spec 0010 sobre o índice e o payload. É
// determinístico e não escreve nada: a prévia devolve o resultado como
// relatório, o confirm o aplica.
//
// O PLANO É UMA SIMULAÇÃO DO LOTE SOBRE O ÍNDICE. Toda regra que `aplicar`
// vai reconferir por dentro — o teto de 200 no Create, o nome livre entre
// irmãos, a filha ativa em podeReceberPalavras, a dona da palavra em
// KeywordOwners, o teto de 20 — tem de ter sido avaliada aqui contra o
// estado que o LOTE PRODUZ, não contra o estado inicial. Se o plano olhar o
// estado de antes, a prévia fica verde, o confirm toma 409 por uma "corrida"
// que o próprio lote causou, a tela pede prévia nova e o laço é
// determinístico (achado B1 do qa-testes, 21/09/2026). 409 é só para o que
// mudou POR FORA, entre a leitura e a escrita.
//
// O PULO NÃO MUDA A SIMULAÇÃO (achado A1 da revisão de segurança, 21/09/2026).
// A prévia roda sem `skipNewCategories` e o confirm roda com; se a entrada
// desmarcada saísse da simulação, as entradas SEGUINTES mudariam de desfecho
// entre uma e outra — uma folha que a prévia recusou por teto nasceria, uma
// irmã perderia o `kind` do grupo, e a palavra que a prévia recusou por
// ambiguidade com a categoria desmarcada seria GRAVADA num item que a pessoa
// nunca desmarcou. Por isso a entrada pulada continua ocupando o lugar dela
// em tudo — consome as vagas, planeja o grupo com o `kind` dela para as
// irmãs, mantém as palavras na disputa de ambiguidade e de teto, tira a
// palavra do grupo que ganharia a folha — e só deixa de ESCREVER: a folha
// não nasce, o grupo não nasce se só ela precisava dele, as palavras dela
// não entram. O invariante, travado por teste: para todo subconjunto de
// pulos, o que o confirm grava é subconjunto do que a prévia sem pulo mostrou
// como "entra", e as entradas não desmarcadas mantêm o desfecho da prévia.
//
// Onde cada reconferência do `aplicar` é simulada:
//
//   - teto de 200 (Create/CountAll): `vagas` desconta grupo e folha que o
//     lote cria, na ordem do JSON;
//   - nome livre entre irmãos (Create/NameTaken): folha ativa no índice é
//     `merged`, o mesmo ref duas vezes no JSON é `duplicate_in_payload`, e
//     um grupo novo é planejado uma vez por nome;
//   - filha ativa (AppendKeywords/podeReceberPalavras): a regra 5b consulta o
//     índice MAIS gruposQueGanhamFolha — os grupos que ganham folha `created`
//     neste lote;
//   - dona da palavra (AppendKeywords/índice único): quem já tem a palavra no
//     índice é `keyword_taken`; a palavra que o lote daria a dois itens é
//     `ambiguous_in_payload` nos dois, e a que o lote daria duas vezes à
//     mesma dona entra uma vez;
//   - teto de 20 (AppendKeywords): existentes + aprovadas neste lote, por dona.
//
// A ORDEM das etapas é a da spec, e importa:
//
//  1. as entradas de `newCategories` são resolvidas primeiro (nome, pulo,
//     duplicata, grupo, folha, teto) — é aqui que nascem as donas novas;
//  2. depois cada entrada (nova com dona, item de categoria, item de conta)
//     tem as palavras conferidas pelas regras 6–8 e 10 da §4.2, produzindo
//     candidatas;
//  3. a ambiguidade (regra 9) é decidida sobre TODAS as candidatas de cada
//     conjunto — categoria e conta são conjuntos independentes;
//  4. o teto (regra 11) é aplicado na ordem do JSON sobre o que sobrou.
func planejar(ix *indiceDaCasa, in Input) *plano {
	p := &plano{donas: make(map[string]*donaPlanejada)}

	pular := make(map[string]struct{}, len(in.SkipNewCategories))
	for _, ref := range in.SkipNewCategories {
		pular[textnorm.Normalize(ref)] = struct{}{}
	}

	p.planejarCategoriasNovas(ix, in.Payload.NewCategories, pular)
	p.simularFolhasDoLote()
	p.planejarItens(ix, in.Payload)

	candidatas := p.conferirPalavras(ix, in.Payload)
	candidatas = recusarAmbiguas(candidatas)
	p.aplicarTeto(candidatas)
	return p
}

// planejarCategoriasNovas resolve a §4.3, entrada a entrada, na ordem do
// JSON.
func (p *plano) planejarCategoriasNovas(ix *indiceDaCasa, entradas []NewCategoryEntry, pular map[string]struct{}) {
	vagas := category.MaxPerHousehold - ix.totalCategorias
	refsVistos := make(map[string]struct{}, len(entradas))
	grupoPlanejadoPorNorm := make(map[string]int)

	p.novas = make([]entradaNova, 0, len(entradas))
	for i := range entradas {
		e := entradas[i]
		nova := entradaNova{grupoNovo: -1}
		v := &nova.view
		v.Add, v.Skipped, v.Rejected = []string{}, []SkippedKeyword{}, []RejectedKeyword{}

		// (1) nome do grupo e da folha: 1–60 caracteres como no POST
		// /categories, e sem `>` — o `ref` precisa ser decomponível. O
		// pattern do schema não vale nada em runtime (e `[^>]` casa `\n` em
		// ECMA-262): a regra é esta, em Go.
		grupoNome, grupoNorm, errGrupo := nomeDeCategoria(e.Group)
		folhaNome, folhaNorm, errFolha := nomeDeCategoria(e.Name)
		if errGrupo != nil || errFolha != nil {
			v.Group, v.Name = neutralizar(e.Group, category.MaxNameLen), neutralizar(e.Name, category.MaxNameLen)
			v.Ref = refNeutro(v.Group, v.Name)
			v.Outcome = OutcomeInvalidName
			p.concluirNova(nova)
			continue
		}
		v.Group, v.Name = grupoNome, folhaNome
		v.Ref = grupoNorm + separadorDeCaminho + folhaNorm

		// (9) o pulo pela pessoa é DECIDIDO aqui e APLICADO no fim
		// (concluirNova): a entrada segue pela simulação inteira como se
		// estivesse marcada, e só o que ela ESCREVERIA é suprimido.
		_, nova.pulada = pular[v.Ref]

		// (8) o mesmo `group > name` duas vezes no JSON: o primeiro vale.
		if _, visto := refsVistos[v.Ref]; visto {
			v.Outcome = OutcomeDuplicateInPayload
			p.concluirNova(nova)
			continue
		}
		refsVistos[v.Ref] = struct{}{}

		// (2)(3)(5) o grupo: existente e ativo (natureza herdada), existente
		// e arquivado (`name_taken_archived`), planejado por uma entrada
		// anterior deste JSON, ou novo (kind obrigatório).
		var kind string
		grupoExistente := ix.gruposPorNorm[grupoNorm]
		switch {
		case grupoExistente != nil && grupoExistente.archived:
			// Folha ativa pendurada em grupo invisível é o estado que
			// ErrParentArchived existe para impedir: a saída é desarquivar.
			v.Kind = ptr(grupoExistente.kind)
			v.Group = grupoExistente.name
			v.Outcome = OutcomeNameTakenArchived
			p.concluirNova(nova)
			continue

		case grupoExistente != nil:
			kind = grupoExistente.kind
			v.Group = grupoExistente.name
			nova.grupoID = grupoExistente.id
			if e.Kind != nil && *e.Kind != kind {
				// Divergir é recusa, não silêncio — ao contrário do POST
				// /categories, que ignora o kind da folha. Aqui o JSON
				// afirmou uma natureza que não é a que a folha teria.
				v.Kind = ptr(kind)
				v.Outcome = OutcomeKindMismatch
				p.concluirNova(nova)
				continue
			}

		default:
			v.GroupIsNew = true
			if idx, planejado := grupoPlanejadoPorNorm[grupoNorm]; planejado {
				kind = p.gruposNovos[idx].kind
				v.Group = p.gruposNovos[idx].name
				nova.grupoNovo = idx
				if e.Kind != nil && *e.Kind != kind {
					v.Kind = ptr(kind)
					v.Outcome = OutcomeKindMismatch
					p.concluirNova(nova)
					continue
				}
			} else {
				if e.Kind == nil {
					v.Outcome = OutcomeKindRequired
					p.concluirNova(nova)
					continue
				}
				if !category.ValidKind(*e.Kind) {
					v.Outcome = OutcomeInvalidKind
					p.concluirNova(nova)
					continue
				}
				kind = *e.Kind
			}
		}
		v.Kind = ptr(kind)

		// (4)(5) a folha, só quando o grupo existe no banco: ativa é
		// `merged_into_existing`; arquivada é `name_taken_archived`.
		if nova.grupoID != "" {
			if folha := ix.folhasPorPai[nova.grupoID][folhaNorm]; folha != nil {
				v.Name = folha.name
				if folha.archived {
					v.Outcome = OutcomeNameTakenArchived
					p.concluirNova(nova)
					continue
				}
				v.Outcome = OutcomeMergedIntoExisting
				v.CategoryID = ptr(folha.id)
				nova.dona = folha.id
				p.dona(folha.id, ItemTypeCategory, ix)
				p.concluirNova(nova)
				continue
			}
		}

		// (7) o teto de 200 conta grupo E folha: o grupo só nasce como
		// continente, então sem vaga para os dois nada nasce.
		precisa := 1
		if v.GroupIsNew && nova.grupoNovo < 0 {
			precisa = 2
		}
		if precisa > vagas {
			v.Outcome = OutcomeHouseholdLimit
			p.concluirNova(nova)
			continue
		}
		vagas -= precisa
		if v.GroupIsNew && nova.grupoNovo < 0 {
			p.gruposNovos = append(p.gruposNovos, grupoPlanejado{norm: grupoNorm, name: grupoNome, kind: kind})
			nova.grupoNovo = len(p.gruposNovos) - 1
			grupoPlanejadoPorNorm[grupoNorm] = nova.grupoNovo
		}
		if nova.grupoNovo >= 0 && !nova.pulada {
			p.gruposNovos[nova.grupoNovo].necessario = true
		}

		v.Outcome = OutcomeCreated
		nova.nomeParaGravar = folhaNome
		nova.dona = prefixoDeDonaNova + v.Ref
		p.dona(nova.dona, ItemTypeCategory, ix)
		p.concluirNova(nova)
	}
}

// concluirNova fecha uma entrada de `newCategories`: guarda o desfecho
// SIMULADO (o que a entrada teria, e o que o resto do lote enxerga) e, se a
// pessoa a desmarcou, publica `skipped_by_user` no lugar dele. A entrada
// pulada não escreve nada — folha, id e palavras — e por isso o relatório
// dela sai limpo; mas a dona, o grupo planejado e as vagas que ela consumiu
// ficam, para as outras entradas verem o mesmo lote que a prévia viu.
func (p *plano) concluirNova(nova entradaNova) {
	nova.simulado = nova.view.Outcome
	if nova.pulada {
		v := &nova.view
		v.Outcome = OutcomeSkippedByUser
		v.Kind = nil
		v.GroupIsNew = false
		v.CategoryID = nil
		v.Add, v.Skipped, v.Rejected = []string{}, []SkippedKeyword{}, []RejectedKeyword{}
	}
	p.novas = append(p.novas, nova)
}

// simularFolhasDoLote registra os grupos existentes que vão ganhar uma folha
// ativa por uma entrada `created` — o pedaço do estado DEPOIS do lote que a
// regra 5b consulta. Roda depois de planejarCategoriasNovas e antes de
// planejarItens, que é a ordem em que `aplicar` grava (folhas antes das
// palavras). Grupo novo não entra: ele não tem id, e nenhuma entrada de
// `categoryKeywords` consegue apontá-lo.
func (p *plano) simularFolhasDoLote() {
	p.gruposQueGanhamFolha = make(map[string]struct{})
	for i := range p.novas {
		nova := &p.novas[i]
		// Pelo desfecho SIMULADO, pulada inclusive: a prévia (sem pulo)
		// recusou a palavra do grupo por causa desta folha, e o confirm com
		// o pulo não pode passar a gravá-la.
		if nova.simulado == OutcomeCreated && nova.grupoID != "" {
			p.gruposQueGanhamFolha[nova.grupoID] = struct{}{}
		}
	}
}

// grupoTeraFilhaAtiva responde a regra 5b contra o estado DEPOIS do lote:
// filha ativa que já existe no índice, ou folha que o próprio lote cria sob
// o grupo.
func (p *plano) grupoTeraFilhaAtiva(ix *indiceDaCasa, grupoID string) bool {
	if ix.filhasAtivasPorGrupo[grupoID] > 0 {
		return true
	}
	_, ganha := p.gruposQueGanhamFolha[grupoID]
	return ganha
}

// planejarItens resolve as regras 3, 4, 5 e 5b da §4.2 para cada entrada de
// `categoryKeywords` e de `accountKeywords`, na ordem do JSON (categorias
// primeiro, como o contrato manda para `items`).
func (p *plano) planejarItens(ix *indiceDaCasa, payload Payload) {
	p.itens = make([]itemPlanejado, 0, len(payload.CategoryKeywords)+len(payload.AccountKeywords))

	for i := range payload.CategoryKeywords {
		e := payload.CategoryKeywords[i]
		item := itemPlanejado{view: novoItemView(ItemTypeCategory, e.CategoryID)}
		v := &item.view

		// BOLA: o id é procurado no índice montado a partir de List(casa do
		// token). Id de outra casa não está no mapa e é indistinguível de id
		// inexistente. Forma não canônica também é `item_not_found` — nunca
		// 400, que diria ao atacante que a forma estava certa.
		var cat *categoriaIndexada
		if id.IsCanonical(e.CategoryID) {
			cat = ix.categoriasPorID[e.CategoryID]
		}
		switch {
		case cat == nil:
			recusarEntrada(v, e.Add, RejectItemNotFound)
		case cat.archived:
			v.Name = ptr(ix.caminhoExibivel(cat))
			recusarEntrada(v, e.Add, RejectItemArchived)
		case textnorm.Normalize(e.CategoryPath) != ix.caminhoNorm(cat):
			// Conferência obrigatória, não decoração: é o que pega id
			// alucinado, id trocado entre linhas e JSON do export de outra
			// casa. O nome que volta é o do SERVIDOR.
			v.Name = ptr(ix.caminhoExibivel(cat))
			recusarEntrada(v, e.Add, RejectNameMismatch)
		case cat.grupo() && p.grupoTeraFilhaAtiva(ix, cat.id):
			// Spec 0005 §12 (achado A6 da emenda): grupo com filha ativa não
			// recebe lançamento, logo não recebe palavra. Sem esta linha o
			// import seria a única porta do produto a gravar onde as outras
			// três recusam. E a filha pode ser a que o PRÓPRIO lote cria:
			// `aplicar` grava a folha antes das palavras, e
			// podeReceberPalavras vai vê-la (achado B1) — recusar aqui é o
			// que impede a prévia verde de virar 409 no confirm.
			v.Name = ptr(ix.caminhoExibivel(cat))
			recusarEntrada(v, e.Add, RejectGroupHasChildren)
		default:
			v.Name = ptr(ix.caminhoExibivel(cat))
			item.dona = cat.id
			p.dona(cat.id, ItemTypeCategory, ix)
		}
		p.itens = append(p.itens, item)
	}

	for i := range payload.AccountKeywords {
		e := payload.AccountKeywords[i]
		item := itemPlanejado{view: novoItemView(ItemTypeAccount, e.AccountID)}
		v := &item.view

		var acc *contaIndexada
		if id.IsCanonical(e.AccountID) {
			acc = ix.contasPorID[e.AccountID]
		}
		switch {
		case acc == nil:
			recusarEntrada(v, e.Add, RejectItemNotFound)
		case acc.archived:
			v.Name = ptr(acc.name)
			recusarEntrada(v, e.Add, RejectItemArchived)
		case textnorm.Normalize(e.AccountName) != acc.nameNorm:
			v.Name = ptr(acc.name)
			recusarEntrada(v, e.Add, RejectNameMismatch)
		default:
			v.Name = ptr(acc.name)
			item.dona = acc.id
			p.dona(acc.id, ItemTypeAccount, ix)
		}
		p.itens = append(p.itens, item)
	}
}

// novoItemView monta a linha do relatório de um item, com as listas nascendo
// `[]` (o contrato as marca `required` e a tela não trata null) e o id
// ecoado SÓ quando tem a forma canônica: texto que não é uuid não volta para
// dentro do app.
func novoItemView(tipo, rawID string) ItemView {
	v := ItemView{Type: tipo, Added: []string{}, Skipped: []SkippedKeyword{}, Rejected: []RejectedKeyword{}}
	if id.IsCanonical(rawID) {
		v.ID = rawID
	}
	return v
}

// recusarEntrada devolve TODAS as palavras da entrada com o mesmo motivo
// (recusa da entrada inteira). As palavras passam pelo mesmo neutralizador
// das recusas de forma: a entrada não foi validada, e o texto dela é o que
// o cliente mandou.
func recusarEntrada(v *ItemView, add []string, motivo string) {
	for _, raw := range add {
		v.Rejected = append(v.Rejected, RejectedKeyword{Keyword: palavraRecusada(raw), Reason: motivo})
	}
}

// dona registra (uma vez) a dona no plano, com as palavras atuais dela.
func (p *plano) dona(chave, tipo string, ix *indiceDaCasa) *donaPlanejada {
	if d, ok := p.donas[chave]; ok {
		return d
	}
	d := &donaPlanejada{tipo: tipo, normasAceitas: make(map[string]struct{})}
	if !strings.HasPrefix(chave, prefixoDeDonaNova) {
		d.id = chave
		if tipo == ItemTypeAccount {
			d.existentesDeConta = ix.palavrasDeConta[chave]
		} else {
			d.existentesDeCategoria = ix.palavrasDeCategoria[chave]
		}
	}
	p.donas[chave] = d
	p.ordemDona = append(p.ordemDona, chave)
	return d
}

// conferirPalavras aplica as regras 6, 7, 8 e 10 da §4.2 a cada palavra de
// cada entrada que tem dona, na ordem do JSON, e devolve as candidatas que
// sobreviveram — na mesma ordem, que é a ordem do teto.
func (p *plano) conferirPalavras(ix *indiceDaCasa, payload Payload) []palavraCandidata {
	var candidatas []palavraCandidata

	for i := range p.novas {
		nova := &p.novas[i]
		if nova.dona == "" {
			continue
		}
		destino := &listasDeRelatorio{&nova.view.Add, &nova.view.Skipped, &nova.view.Rejected}
		if nova.pulada {
			// As palavras da entrada desmarcada disputam ambiguidade e teto
			// como na prévia, mas o relatório dela sai limpo: listas
			// descartáveis, e nada é aprovado para escrita.
			destino = &listasDeRelatorio{new([]string), new([]SkippedKeyword), new([]RejectedKeyword)}
		}
		candidatas = p.conferirLista(ix, candidatas, payload.NewCategories[i].Add, nova.dona, ItemTypeCategory,
			destino, nova.pulada)
	}

	nCat := len(payload.CategoryKeywords)
	for i := range p.itens {
		item := &p.itens[i]
		if item.dona == "" {
			continue
		}
		var add []string
		if i < nCat {
			add = payload.CategoryKeywords[i].Add
		} else {
			add = payload.AccountKeywords[i-nCat].Add
		}
		candidatas = p.conferirLista(ix, candidatas, add, item.dona, item.view.Type,
			&listasDeRelatorio{&item.view.Added, &item.view.Skipped, &item.view.Rejected}, false)
	}
	return candidatas
}

// conferirLista é conferirPalavras para UMA entrada.
func (p *plano) conferirLista(ix *indiceDaCasa, candidatas []palavraCandidata, add []string,
	dona, tipo string, destino *listasDeRelatorio, pulada bool,
) []palavraCandidata {
	d := p.donas[dona]
	vistasNaEntrada := make(map[string]struct{}, len(add))

	for _, raw := range add {
		// (6) forma: 2–40 runas normalizada, charset, ao menos uma palavra
		// útil. A palavra que volta é a BRUTA, neutralizada e truncada —
		// sem ela a tela não aponta a linha.
		keyword, norm, err := textmatch.ValidateKeyword(raw)
		if err != nil {
			*destino.rejected = append(*destino.rejected,
				RejectedKeyword{Keyword: palavraRecusada(raw), Reason: RejectInvalidKeyword})
			continue
		}

		// (10) repetida no mesmo item: deduplica em silêncio.
		if _, repetida := vistasNaEntrada[norm]; repetida {
			continue
		}
		vistasNaEntrada[norm] = struct{}{}

		// (7) já existe no próprio item: pula — é o que torna reimportar o
		// mesmo JSON inofensivo.
		if temNorma(d, norm) {
			*destino.skipped = append(*destino.skipped, SkippedKeyword{Keyword: keyword, Reason: SkipAlreadyPresent})
			continue
		}

		// (8) já existe em OUTRO item do mesmo tipo: recusa, dizendo de quem
		// é — sempre um recurso desta casa, porque o índice só tem esta casa.
		if outra, tomada := donoDe(ix, tipo, norm); tomada && outra != d.id {
			*destino.rejected = append(*destino.rejected,
				RejectedKeyword{Keyword: keyword, Reason: RejectKeywordTaken, OwnerID: outra})
			continue
		}

		candidatas = append(candidatas, palavraCandidata{
			keyword: keyword, norm: norm, dona: dona, tipo: tipo, pulada: pulada, destino: destino,
		})
	}
	return candidatas
}

// temNorma diz se a dona JÁ TEM a palavra (pela forma normalizada).
func temNorma(d *donaPlanejada, norm string) bool {
	if d.tipo == ItemTypeAccount {
		for i := range d.existentesDeConta {
			if d.existentesDeConta[i].Norm == norm {
				return true
			}
		}
		return false
	}
	for i := range d.existentesDeCategoria {
		if d.existentesDeCategoria[i].Norm == norm {
			return true
		}
	}
	return false
}

// donoDe devolve quem já tem a palavra no conjunto do tipo — categoria e
// conta são conjuntos independentes (spec 0005 §4.1).
func donoDe(ix *indiceDaCasa, tipo, norm string) (string, bool) {
	if tipo == ItemTypeAccount {
		dona, ok := ix.donoDaPalavraDeConta[norm]
		return dona, ok
	}
	dona, ok := ix.donoDaPalavraDeCategoria[norm]
	return dona, ok
}

// recusarAmbiguas aplica a regra 9 da §4.2: a mesma palavra em DOIS itens
// diferentes do próprio JSON recusa AS DUAS — ambíguo é pior que vazio. A
// mesma dona citada duas vezes não é ambiguidade (é a regra 10 entre
// entradas, resolvida no teto). Os conjuntos de categoria e de conta são
// independentes.
func recusarAmbiguas(candidatas []palavraCandidata) []palavraCandidata {
	type chave struct{ tipo, norm string }
	donasPorPalavra := make(map[chave]map[string]struct{})
	for _, c := range candidatas {
		k := chave{c.tipo, c.norm}
		if donasPorPalavra[k] == nil {
			donasPorPalavra[k] = make(map[string]struct{})
		}
		donasPorPalavra[k][c.dona] = struct{}{}
	}

	sobreviventes := candidatas[:0]
	for _, c := range candidatas {
		if len(donasPorPalavra[chave{c.tipo, c.norm}]) > 1 {
			*c.destino.rejected = append(*c.destino.rejected,
				RejectedKeyword{Keyword: c.keyword, Reason: RejectAmbiguousInPayload})
			continue
		}
		sobreviventes = append(sobreviventes, c)
	}
	return sobreviventes
}

// aplicarTeto aplica a regra 11 da §4.2 na ordem do JSON: o total da dona
// (existentes + aprovadas + reservadas) fica em MaxKeywordsPerOwner, e as
// excedentes são recusadas — nunca truncadas em silêncio. A mesma palavra
// numa segunda entrada da mesma dona é `already_present` (ela entra pela
// primeira). A palavra de uma entrada DESMARCADA toma a vaga e não é escrita.
func (p *plano) aplicarTeto(candidatas []palavraCandidata) {
	for _, c := range candidatas {
		d := p.donas[c.dona]
		if _, aceita := d.normasAceitas[c.norm]; aceita {
			*c.destino.skipped = append(*c.destino.skipped, SkippedKeyword{Keyword: c.keyword, Reason: SkipAlreadyPresent})
			continue
		}
		if d.totalAtual() >= category.MaxKeywordsPerOwner {
			*c.destino.rejected = append(*c.destino.rejected,
				RejectedKeyword{Keyword: c.keyword, Reason: RejectLimitExceeded})
			continue
		}
		d.normasAceitas[c.norm] = struct{}{}
		if c.pulada {
			d.reservadas++
			continue
		}
		d.aprovadas = append(d.aprovadas, palavraAprovada{keyword: c.keyword, norm: c.norm})
		*c.destino.added = append(*c.destino.added, c.keyword)
	}
}

// --- o relatório -----------------------------------------------------------------

// relatorio publica o plano no formato do contrato. Os totais são SOMAS das
// listas — nunca um contador paralelo.
func (p *plano) relatorio(periodTransactions int) Report {
	r := Report{
		NewCategories: make([]NewCategoryView, 0, len(p.novas)),
		Items:         make([]ItemView, 0, len(p.itens)),
	}
	r.Totals.PeriodTransactions = periodTransactions
	for i := range p.novas {
		v := p.novas[i].view
		if v.Outcome == OutcomeCreated {
			r.Totals.CategoriesCreated++
		}
		r.Totals.Added += len(v.Add)
		r.Totals.Skipped += len(v.Skipped)
		r.Totals.Rejected += len(v.Rejected)
		r.NewCategories = append(r.NewCategories, v)
	}
	for i := range p.itens {
		v := p.itens[i].view
		r.Totals.Added += len(v.Added)
		r.Totals.Skipped += len(v.Skipped)
		r.Totals.Rejected += len(v.Rejected)
		r.Items = append(r.Items, v)
	}
	return r
}

// --- helpers de forma ---------------------------------------------------------------

// nomeDeCategoria valida o nome como o POST /categories (category.NormalizeName:
// 1–60 caracteres, não vazio depois de normalizado) e, a mais, recusa:
//
//   - `>`, o separador do `ref` — sem isso o caminho não seria decomponível;
//   - qualquer rune que não seja o espaço simples (U+0020) nem passe na
//     allowlist de runas VISÍVEIS do prompt (aiprompt.Drawable): controles,
//     overrides bidirecionais (U+202E inverte visualmente a célula da prévia
//     e vai junto para toda tela que renderizar o nome), zero-width, área de
//     uso privado. Este é o primeiro caminho do produto em que um TERCEIRO —
//     a IA, ou quem moldou o JSON — escreve o nome de uma categoria (achado
//     A3 da revisão de segurança da E9b).
//
// A assimetria com o POST /categories, que só colapsa espaços e aceita o
// resto, NÃO é resolvida aqui — como a do `>`: levar a regra para
// category.NormalizeName é decisão de produto fora desta fatia, registrada
// para o `arquiteto`. Devolve a forma exibível que o servidor gravaria e a
// forma normalizada.
func nomeDeCategoria(raw string) (name, norm string, err error) {
	for _, r := range raw {
		if r == '>' || (r != ' ' && !aiprompt.Drawable(r)) {
			return "", "", category.ErrInvalidName
		}
	}
	return category.NormalizeName(raw)
}

// refNeutro monta um `ref` bem formado para uma entrada de nome INVÁLIDO —
// ele não vai ser usado para nada (só `created`/`merged` têm caixa para
// desmarcar), mas o campo é obrigatório e a forma é a do schema.
func refNeutro(grupo, nome string) string {
	return ladoDeRef(grupo) + separadorDeCaminho + ladoDeRef(nome)
}

func ladoDeRef(s string) string {
	s = textnorm.Normalize(strings.ReplaceAll(s, ">", " "))
	s = truncarRunas(s, category.MaxNameLen)
	if s == "" {
		return "-"
	}
	return s
}

// palavraRecusada é a palavra que VOLTA no relatório quando a entrada não
// passou pela validação de forma: a que o cliente mandou, neutralizada com a
// allowlist do prompt e truncada em 40 runas. Nunca vai para o log.
func palavraRecusada(raw string) string {
	return neutralizar(raw, maxRejectedKeywordRunes)
}

// neutralizar mantém só as runas que DESENHAM (aiprompt.Drawable — a mesma
// allowlist de `celula`, reusada e não copiada), colapsa o resto em um
// espaço e trunca em maxRunes. Sem cortar, traduzir ou "limpar" além disso:
// o texto tem de continuar reconhecível como a linha que a pessoa colou.
func neutralizar(s string, maxRunes int) string {
	var b strings.Builder
	b.Grow(len(s))
	n := 0
	espacoPendente := false
	comConteudo := false
	for _, r := range s {
		if !aiprompt.Drawable(r) {
			if comConteudo {
				espacoPendente = true
			}
			continue
		}
		if espacoPendente {
			if n >= maxRunes {
				break
			}
			b.WriteRune(' ')
			n++
			espacoPendente = false
		}
		if n >= maxRunes {
			break
		}
		b.WriteRune(r)
		n++
		comConteudo = true
	}
	return b.String()
}

// truncarRunas corta o texto em maxRunes RUNAS (nunca no meio de um
// caractere).
func truncarRunas(s string, maxRunes int) string {
	n := 0
	for i := range s {
		if n == maxRunes {
			return s[:i]
		}
		n++
	}
	return s
}

func ptr(s string) *string { return &s }
