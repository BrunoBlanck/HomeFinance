package category

import (
	"context"
	"fmt"
	"time"
)

// DefaultGroup é um grupo da semente inicial, com as subcategorias que nascem
// junto com ele.
type DefaultGroup struct {
	Name string
	Kind string

	// Children são as subcategorias de fábrica. Vazio em "Outras despesas",
	// que é o balde residual e continua recebendo lançamento diretamente —
	// grupo com filha ativa não recebe (spec 0005 §12/§13).
	Children []DefaultLeaf
}

// DefaultLeaf é uma subcategoria da semente com as suas palavras-chave.
type DefaultLeaf struct {
	Name string

	// Keywords é a forma EXIBÍVEL, como a pessoa veria se tivesse digitado; a
	// forma de comparação (Norm) é derivada por ValidateKeywords, exatamente
	// como na palavra cadastrada à mão. A FOLHA NÃO TEM `Kind`: ela herda a
	// natureza do grupo (ADR-017b, invariante 4 da spec 0003), e um campo
	// aqui seria um convite a divergir do pai.
	Keywords []string
}

// DefaultGroups é a taxonomia que uma casa NOVA recebe (D5 do PLANOS.md,
// ADR-033): 15 grupos, 41 subcategorias e as palavras-chave de fábrica.
//
// Por que semear em vez de deixar a casa vazia: casa vazia obriga a pessoa a
// inventar uma taxonomia antes de conseguir lançar o primeiro gasto, e é
// exatamente aí que se desiste de um app de finanças. Com palavras-chave já
// preenchidas, a categorização automática (ADR-026) funciona no PRIMEIRO
// extrato importado, que é o momento em que o app prova que serve.
//
// Nenhuma categoria daqui é "de sistema": todas são editáveis, arquiváveis e
// excluíveis como qualquer outra. Categoria que o usuário não pode mexer é
// categoria que vai atrapalhar alguém.
//
// A ordem é a de exibição: despesas primeiro (é o que mais se usa), receitas
// depois, aporte e resgate no fim, e "Outras" no fim de cada bloco.
//
// A SEMENTE RODA UMA VEZ POR CASA, NA CRIAÇÃO DELA — na verificação do e-mail
// e, como reparo, no login de usuário verificado que não tem casa nenhuma.
// `household.EnsureDefault` devolve cedo quando o usuário já tem casa, então
// ela NÃO roda a cada login e casa existente NÃO recebe grupo novo (erratum ao
// ADR-029a, que prometeu o contrário). Um backfill, se vier, será ação
// explícita e opt-in.
//
// A LISTA É TRAVADA POR TESTE, e é de propósito que acrescentar uma palavra
// possa quebrar o build (seed_test.go e seed_corpus_test.go):
//
//   - R1 — nenhuma palavra é subconjunto consecutivo de outra em folha
//     diferente do mesmo lado do dinheiro (por isso não existe "mercado" solto:
//     ele empataria 100×100 com "mercado livre");
//   - R2 — produto de investimento vive num lado só, e o outro lado usa frase
//     verbo+produto ("resgate cdb"); sem "resgate"/"aplicação" soltos;
//   - R3 — nenhuma palavra alcança o limiar contra o vocabulário de rotina dos
//     extratos, marcas de banco e nomes de pessoa. É o achado que mudou o
//     desenho: "contador" pontua 89 contra "conta", que aparece em TODA linha
//     de Pix do Nubank;
//   - R4 — nada de meio de pagamento nem marca de banco (pix, ted, boleto,
//     nubank, inter...): marca de banco é palavra-chave de CONTA (ADR-026e) e
//     colidiria com a detecção de transferência;
//   - R5 — palavra com menos de 5 runas só quando o token do extrato é
//     exatamente ela (oi, tim, net, sky, cdb, xp): abaixo disso o motor só casa
//     igualdade, o que é seguro por construção;
//   - R6 — genérico ambíguo fica de fora ("clínica" empata entre veterinária,
//     estética e odontológica);
//   - R7 — nenhuma folha semeada passa de 16 palavras. O teto do domínio é 20
//     (MaxKeywordsPerOwner); as vagas restantes são da pessoa, para o atalho
//     "Reconhecer por «x»" nunca nascer recusado;
//   - R8 — forma redundante não ocupa vaga: se o motor já cobre a variante
//     acima do limiar (singular×plural, uma edição, token contido), a vaga vale
//     mais para outra palavra.
//
// Mudar essas regras, ou afrouxar a lista de tokens perigosos do teste, exige
// emenda ao ADR-033.
func DefaultGroups() []DefaultGroup {
	return []DefaultGroup{
		{Name: "Moradia", Kind: KindExpense, Children: []DefaultLeaf{
			{Name: "Aluguel e condomínio", Keywords: []string{
				"aluguel", "locação", "quintoandar", "condomínio", "cond",
			}},
			{Name: "Água, luz e gás", Keywords: []string{
				"energia", "enel", "cemig", "copel", "cpfl", "light", "equatorial",
				"água", "saneamento", "sabesp", "copasa", "sanepar", "cedae",
				"gás", "comgás", "ultragaz",
			}},
			{Name: "Manutenção e reforma", Keywords: []string{
				"reforma", "reparo", "conserto", "encanador", "eletricista", "pedreiro",
				"marceneiro", "leroy merlin", "obramax", "material de construção", "dedetização",
			}},
		}},
		{Name: "Alimentação", Kind: KindExpense, Children: []DefaultLeaf{
			{Name: "Supermercado", Keywords: []string{
				"supermercado", "mercadinho", "minimercado", "hortifruti", "sacolão", "açougue",
				"atacadão", "assaí", "carrefour", "pão de açúcar", "empório",
				"natural da terra",
			}},
			{Name: "Restaurantes, lanches e padaria", Keywords: []string{
				"restaurante", "lanchonete", "padaria", "panificadora", "confeitaria", "cafeteria",
				"café", "pizza", "bar", "hamburgueria", "sushi", "mcdonalds",
				"burger king", "subway", "açaí",
			}},
			{Name: "Delivery", Keywords: []string{
				"ifood", "rappi", "99food", "aiqfome", "zedelivery", "daki",
			}},
		}},
		{Name: "Transporte", Kind: KindExpense, Children: []DefaultLeaf{
			{Name: "Combustível e manutenção do carro", Keywords: []string{
				"posto", "combustível", "gasolina", "etanol", "shell", "ipiranga", "petrobras",
				"oficina", "mecânica", "auto center", "pneus", "lava jato",
				"troca de óleo", "autopeças", "borracharia",
			}},
			{Name: "Aplicativo e transporte público", Keywords: []string{
				"uber", "99app", "99pop", "99 pop", "táxi", "cabify", "indrive",
				"top recarga", "metrô", "ônibus", "bilhete único", "cptm", "riocard", "brt", "vlt",
			}},
			{Name: "Estacionamento e pedágio", Keywords: []string{
				"estacionamento", "estapar", "zona azul", "pedágio", "sem parar", "conectcar",
				"veloe", "taggy", "multipark",
			}},
		}},
		{Name: "Saúde", Kind: KindExpense, Children: []DefaultLeaf{
			{Name: "Farmácia", Keywords: []string{
				"farmácia", "drogaria", "drogasil", "raia", "pague menos",
				"ultrafarma", "panvel", "nissei",
			}},
			{Name: "Plano de saúde", Keywords: []string{
				"unimed", "amil", "hapvida", "sulamerica", "notredame",
				"prevent senior", "plano de saúde", "golden cross",
			}},
			{Name: "Médico, dentista e exames", Keywords: []string{
				"médico", "hospital", "laboratório", "exame", "dr", "dra",
				"pronto socorro", "fleury", "dentista", "odonto",
				"psicólogo", "terapia",
			}},
			{Name: "Academia e esportes", Keywords: []string{
				"academia", "smart fit", "bluefit", "bodytech", "crossfit", "pilates",
				"yoga", "gympass", "wellhub", "selfit",
			}},
		}},
		{Name: "Educação", Kind: KindExpense, Children: []DefaultLeaf{
			{Name: "Escola e faculdade", Keywords: []string{
				"escola", "colégio", "faculdade", "universidade", "creche", "berçário", "unip",
				"estácio", "anhanguera", "uninove", "unopar", "mackenzie",
			}},
			{Name: "Cursos e material", Keywords: []string{
				"curso", "udemy", "alura", "coursera", "duolingo", "wizard", "ccaa", "fisk",
				"cultura inglesa", "livraria", "livro", "papelaria", "kalunga",
			}},
		}},
		{Name: "Lazer", Kind: KindExpense, Children: []DefaultLeaf{
			{Name: "Streaming, jogos e assinaturas", Keywords: []string{
				"netflix", "spotify", "disney", "hbomax", "hbo max",
				"globoplay", "youtube", "deezer", "apple", "google", "steam", "playstation",
				"xbox", "microsoft",
			}},
			{Name: "Cinema, shows e eventos", Keywords: []string{
				"cinema", "cinépolis", "kinoplex", "ingresso", "sympla", "teatro",
				"museu", "show", "bilheteria", "eventim",
			}},
			{Name: "Viagens", Keywords: []string{
				"hotel", "hostel", "pousada", "airbnb", "booking", "decolar", "123 milhas",
				"latam", "gol", "azul linhas", "cvc", "passagem", "buser",
				"clickbus", "viagem",
			}},
		}},
		{Name: "Compras", Kind: KindExpense, Children: []DefaultLeaf{
			{Name: "Compras online", Keywords: []string{
				"mercado livre", "amazon", "shopee", "temu", "magazine luiza",
				"americanas", "casas bahia", "submarino",
			}},
			{Name: "Eletrônicos e informática", Keywords: []string{
				"informática", "notebook", "computador", "smartphone", "iphone", "samsung",
				"fast shop", "fastshop", "kabum", "pichau", "terabyte", "xiaomi", "motorola",
				"assistência técnica",
			}},
			{Name: "Casa e decoração", Keywords: []string{
				"móveis planejados", "decoração", "tok stok", "tokstok", "mobly", "madeiramadeira", "etna",
				"camicado", "eletrodomésticos", "utilidades",
				"havan", "westwing",
			}},
		}},
		{Name: "Serviços", Kind: KindExpense, Children: []DefaultLeaf{
			{Name: "Telefone, internet e TV", Keywords: []string{
				"claro", "vivo", "tim", "oi", "net", "sky", "banda larga", "fibra", "provedor",
				"algar", "recarga celular", "telefonia", "starlink", "directv",
			}},
			{Name: "Tarifas bancárias e juros", Keywords: []string{
				"tarifa", "anuidade", "iof", "juros", "encargos", "cesta de serviços",
				"pacote de serviços", "manutenção de conta",
			}},
			{Name: "Seguros", Keywords: []string{
				"seguradora", "seguro auto", "seguro de vida", "seguro residencial",
				"prêmio de seguro", "porto seguro", "tokio marine", "mapfre", "hdi",
				"liberty", "youse", "azul seguros", "sompo",
			}},
		}},
		{Name: "Pessoal", Kind: KindExpense, Children: []DefaultLeaf{
			{Name: "Roupas e calçados", Keywords: []string{
				"roupa", "calçados", "vestuário", "moda", "modas", "renner", "riachuelo", "zara",
				"hering", "centauro", "netshoes", "dafiti", "nike", "adidas", "shein",
			}},
			{Name: "Cabelo e beleza", Keywords: []string{
				"salão", "cabeleireiro", "barbearia", "barbeiro", "manicure", "estética",
				"depilação", "sephora", "perfume", "cosméticos", "beleza",
			}},
			{Name: "Pets", Keywords: []string{
				"pet", "petshop", "petz", "cobasi", "veterinário", "petlove",
			}},
		}},
		{Name: "Impostos", Kind: KindExpense, Children: []DefaultLeaf{
			{Name: "IPTU, IPVA e taxas", Keywords: []string{
				"iptu", "prefeitura", "taxa de lixo", "itbi", "ipva", "detran", "licenciamento",
				"dpvat", "multa de trânsito",
			}},
			{Name: "Imposto de renda e DARF", Keywords: []string{
				"imposto", "irpf", "darf", "receita federal", "carnê leão",
				"iss", "mei", "simples nacional",
			}},
		}},
		// "Outras despesas" é o balde residual: SEM filhas e SEM palavras, para
		// continuar aceitando lançamento diretamente.
		{Name: "Outras despesas", Kind: KindExpense},

		{Name: "Salário", Kind: KindIncome, Children: []DefaultLeaf{
			{Name: "Salário, 13º e férias", Keywords: []string{
				"salário", "folha", "holerite", "remuneração",
				"adiantamento", "antecipação", "décimo terceiro", "gratificação natalina",
				"abono pecuniário", "abono de férias", "pagamento de férias", "terço de férias",
			}},
			{Name: "Bônus, comissões e pró-labore", Keywords: []string{
				"bônus", "plr", "participação nos lucros", "comissão de vendas", "prolabore",
				"retirada de sócio", "distribuição de lucros",
			}},
			{Name: "Rescisão e FGTS", Keywords: []string{
				"rescisão", "verbas rescisórias", "fgts", "seguro desemprego", "aviso prévio",
			}},
		}},
		{Name: "Outras receitas", Kind: KindIncome, Children: []DefaultLeaf{
			{Name: "Reembolsos e estornos", Keywords: []string{
				"reembolso", "estorno", "devolução", "ressarcimento",
			}},
			{Name: "Rendimentos e cashback", Keywords: []string{
				"cashback", "dividendo", "jcp", "juros sobre capital", "proventos",
			}},
			{Name: "Aluguel, benefícios e renda extra", Keywords: []string{
				"aluguel recebido", "inquilino", "locatário", "inss", "aposentadoria",
				"bolsa família", "auxílio", "freelance", "honorários", "serviço prestado",
				"olx", "enjoei",
			}},
		}},
		{Name: "Investimentos", Kind: KindInvestment, Children: []DefaultLeaf{
			{Name: "Renda fixa e Tesouro Direto", Keywords: []string{
				"cdb", "rdb", "lci", "lca", "cri", "cra", "debênture", "poupança", "renda fixa",
				"cdi", "caixinha", "tesouro", "título público",
			}},
			{Name: "Ações, fundos e previdência", Keywords: []string{
				"fii", "fiis", "b3", "home broker", "renda variável", "etf", "bdr",
				"bolsa de valores", "fundo", "multimercado", "fic", "previdência", "vgbl",
				"pgbl", "icatu",
			}},
			{Name: "Corretoras e criptomoedas", Keywords: []string{
				"xp", "rico", "clear", "btg pactual", "genial", "órama",
				"warren", "cripto", "bitcoin", "btc",
				"coinbase", "toro",
			}},
		}},
		{Name: "Resgates", Kind: KindRedemption, Children: []DefaultLeaf{
			// Duas folhas repetem o nome das de Investimentos. É legal (a
			// unicidade de nome é entre IRMÃOS) e proposital: a leitura das
			// duas árvores fica espelhada. "Corretoras" não tem espelho porque
			// o sentido de volta ("TED recebida - XP") não tem vocabulário
			// próprio.
			{Name: "Renda fixa e Tesouro Direto", Keywords: []string{
				"resgate cdb", "resgate rdb", "resgate lci", "resgate lca", "resgate poupança",
				"resgate renda fixa", "resgate caixinha", "vencimento cdb", "vencimento lci",
				"vencimento lca", "resgate tesouro", "venda tesouro", "vencimento tesouro",
				"venda título público",
			}},
			{Name: "Ações, fundos e previdência", Keywords: []string{
				"venda ações", "venda fii", "venda etf", "venda bdr", "resgate renda variável",
				"resgate fundo", "resgate fic", "resgate multimercado", "resgate cotas",
				"resgate previdência", "resgate vgbl", "resgate pgbl",
			}},
			{Name: "Criptomoedas", Keywords: []string{
				"venda bitcoin", "venda cripto", "resgate cripto", "venda btc", "venda ethereum",
			}},
		}},
	}
}

// DefaultCategoryCount é quantas categorias uma casa nova recebe: grupos MAIS
// folhas. É o número que os testes comparam com CountAll — `len(DefaultGroups())`
// conta só os grupos e mentiria por 41.
func DefaultCategoryCount() int {
	total := 0
	for _, g := range DefaultGroups() {
		total += 1 + len(g.Children)
	}
	return total
}

// folhaSemeada é uma subcategoria que ESTA execução acabou de criar, com as
// palavras que ela ainda vai receber. Só o que está aqui ganha palavra-chave.
type folhaSemeada struct {
	id       string
	keywords []Keyword
}

// grupoValidado e folhaValidada são a tabela já conferida: nome normalizado e
// palavras validadas, prontos para escrever.
type grupoValidado struct {
	name string
	norm string
	kind string
	// folhas mantém a ordem da tabela, que é a ordem de criação.
	folhas []folhaValidada
}

type folhaValidada struct {
	name string
	norm string
	// keywords já passou por ValidateKeywords: Keyword, Norm e Position
	// preenchidos. ID, casa, categoria e instante são do serviço.
	keywords []Keyword
}

// validarTabela confere a tabela INTEIRA antes da primeira escrita: nome de
// grupo, nome de folha e lista de palavras de cada folha.
//
// Por que antes, e não no meio do laço: erro aqui é bug de código, não entrada
// do usuário, e nos dois casos a transação inteira é desfeita — mas descobri-lo
// na 40ª folha custa 56 INSERT desperdiçados por requisição, e a requisição é a
// que cria a casa, na verificação do e-mail. Falhar antes de tocar no banco
// custa zero (achado A6 da revisão de segurança de 18/09/2026).
//
// Nenhum erro daqui carrega palavra-chave: ele identifica a folha pelo nome,
// que é constante deste arquivo, e o item pelo índice.
func validarTabela() ([]grupoValidado, error) {
	grupos := DefaultGroups()
	out := make([]grupoValidado, 0, len(grupos))
	for _, g := range grupos {
		name, norm, err := NormalizeName(g.Name)
		if err != nil {
			return nil, fmt.Errorf("semente inválida %q: %w", g.Name, err)
		}
		gv := grupoValidado{
			name:   name,
			norm:   norm,
			kind:   g.Kind,
			folhas: make([]folhaValidada, 0, len(g.Children)),
		}
		for _, f := range g.Children {
			nomeFolha, normFolha, err := NormalizeName(f.Name)
			if err != nil {
				return nil, fmt.Errorf("semente inválida %q › %q: %w", g.Name, f.Name, err)
			}
			// Reusa a validação da BORDA: é ela que deriva Norm e recusa
			// palavra que o motor não indexaria.
			kws, err := ValidateKeywords(f.Keywords)
			if err != nil {
				return nil, fmt.Errorf("palavras-chave inválidas na semente (%q › %q): %w", g.Name, f.Name, err)
			}
			gv.folhas = append(gv.folhas, folhaValidada{name: nomeFolha, norm: normFolha, keywords: kws})
		}
		out = append(out, gv)
	}
	return out, nil
}

// SeedDefaults cria a taxonomia inicial da casa: grupos, subcategorias e
// palavras-chave (ADR-033).
//
// **Roda UMA vez por casa, na criação dela** — dentro da transação de
// `household.EnsureDefault`, que é chamada na verificação do e-mail e, como
// reparo, no login de usuário verificado sem casa nenhuma. Quando o usuário já
// tem casa, `EnsureDefault` devolve cedo e nem chega aqui: casa existente NÃO
// recebe grupo novo, e nenhum login "reaplica" a semente.
//
// **Idempotente mesmo assim**, como defesa em profundidade: um grupo cujo nome
// normalizado já exista ATIVO na casa é pulado, e uma segunda execução vira um
// no-op total.
//
// **Folhas e palavras só nascem junto com o grupo que ESTA execução criou.**
// Grupo já existente — com qualquer natureza, com ou sem filhas, com ou sem
// palavras — fica INTOCADO. Uma regra só resolve quatro casos de uma vez:
//
//   - a segunda execução não tem o que fazer (idempotência total);
//   - a casa que criou "Investimentos" à mão como DESPESA continua com a dela,
//     sem ganhar folhas de aporte penduradas numa categoria de despesa (o teste
//     TestSementeNaoDuplicaNomeJaUsadoComOutraNatureza segue valendo, ADR-029a);
//   - o grupo que já tem palavras-chave próprias não ganha filhas, então as
//     palavras dele não ficam inertes (spec 0005 §12: grupo com filha ativa não
//     recebe lançamento, logo palavra nele não sugeriria nada);
//   - dispensa `NameTaken` por folha — o pai acabou de nascer nesta transação e
//     não tem irmãos.
//
// **A idempotência é por NOME, não por (nome, natureza) — e isso é decidido,
// não esquecido.** Criar um segundo grupo com o mesmo nome e natureza diferente
// daria duas categorias homônimas na mesma tela. O caminho dessa casa é a troca
// de natureza (`expense → investment`, ADR-029c), que é explícita, auditada com
// autor e leva as subcategorias junto.
//
// **Não abre transação própria.** Ela roda dentro da transação que cria a casa,
// e é isso que atende o S10 do PLANOS.md: sem chave estrangeira física
// (ADR-013), casa criada com semente pela metade é um estado que o banco não
// barra.
//
// **Respeita MaxPerHousehold e para em silêncio quando o teto chega** — sem
// falhar. Casa nova nunca chega perto (56 categorias contra 200), mas a
// natureza da função é "popular o que couber": estourar o teto deixaria a casa
// acima do que o resto do código assume, e `/investments/detect` com
// `overwriteCategorized` passaria a responder 500 PERMANENTE
// (transaction.ErrTooManyCategories, ADR-029 j.2) sem nenhuma ação de
// autoatendimento. O que foi criado ANTES do teto ainda recebe as suas
// palavras: parar de semear não é motivo para deixar folha muda.
//
// **Não é auditada por ator**: roda dentro da criação da casa, que já tem a sua
// entrada (`household.created`). Registrar 56 linhas de `category.created` sem
// usuário que as pediu encheria o rastro de ruído e esconderia o que uma pessoa
// de fato fez.
func (s *Service) SeedDefaults(ctx context.Context, householdID string) error {
	if householdID == "" {
		return fmt.Errorf("semeando categorias: %w", ErrNotFound)
	}

	// A TABELA é conferida antes de qualquer ida ao banco.
	tabela, err := validarTabela()
	if err != nil {
		return err
	}

	// DUAS consultas antes do laço, e contadores locais depois. Elas
	// substituem os `NameTaken` da versão anterior — um por grupo, que agora
	// seriam 56 — por uma listagem só.
	//
	// `includeArchived = TRUE`, e a escolha é decidida (achado A4 da revisão de
	// 18/09/2026): `NameTaken` enxergava só as ativas, e com essa semântica uma
	// casa com "Moradia" ARQUIVADA ganharia um segundo "Moradia" ativo, com
	// três folhas, ao lado do que a pessoa tinha arquivado de propósito — e a
	// promessa de "segunda execução é no-op TOTAL" seria falsa. Não há caminho
	// que dispare isso hoje (a semente roda uma vez, na criação da casa), e é
	// justamente por isso que o custo de fechar a porta é zero.
	total, err := s.repo.CountAll(ctx, householdID)
	if err != nil {
		return fmt.Errorf("contando categorias antes da semente: %w", err)
	}
	existentes, err := s.repo.List(ctx, householdID, true)
	if err != nil {
		return fmt.Errorf("listando categorias antes da semente: %w", err)
	}
	gruposDaCasa := make(map[string]struct{}, len(existentes))
	for _, c := range existentes {
		if c.ParentID == nil {
			gruposDaCasa[c.NameNorm] = struct{}{}
		}
	}

	now := s.clock()
	criadas := make([]folhaSemeada, 0, DefaultCategoryCount())

semente:
	for _, grupo := range tabela {
		if total >= MaxPerHousehold {
			break semente
		}
		if _, jaExiste := gruposDaCasa[grupo.norm]; jaExiste {
			continue
		}

		pai := Category{
			ID:          s.ids(),
			HouseholdID: householdID,
			Name:        grupo.name,
			NameNorm:    grupo.norm,
			Kind:        grupo.kind,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := s.repo.Create(ctx, &pai); err != nil {
			// Erro de escrita é erro, sem tolerância a ErrNameTaken: as folhas
			// dependem do pai EXISTIR, e seguir adiante as penduraria num id
			// que não foi gravado.
			return fmt.Errorf("criando categoria da semente: %w", err)
		}
		gruposDaCasa[grupo.norm] = struct{}{}
		total++

		for _, folha := range grupo.folhas {
			if total >= MaxPerHousehold {
				break semente
			}
			paiID := pai.ID
			filha := Category{
				ID:          s.ids(),
				HouseholdID: householdID,
				ParentID:    &paiID,
				Name:        folha.name,
				NameNorm:    folha.norm,
				// A natureza vem do GRUPO PAI, nunca de campo da tabela da
				// folha (invariante 4 da spec 0003). DefaultLeaf nem tem esse
				// campo, justamente para a divergência não ser representável.
				Kind:      grupo.kind,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := s.repo.Create(ctx, &filha); err != nil {
				return fmt.Errorf("criando subcategoria da semente: %w", err)
			}
			total++
			if len(folha.keywords) > 0 {
				criadas = append(criadas, folhaSemeada{id: filha.ID, keywords: folha.keywords})
			}
		}
	}

	return s.semearPalavras(ctx, householdID, criadas, now)
}

// semearPalavras grava as palavras-chave das folhas que a semente acabou de
// criar, PULANDO em silêncio a norma que já pertence a outra categoria da casa.
//
// A colisão é decidida EM MEMÓRIA, antes de qualquer escrita, com UMA consulta
// de donas para a casa inteira (o repositório fatia o IN). Isso importa por
// dois motivos:
//
//   - `gravarPalavras` NÃO serve aqui: ele devolve KeywordTakenError, e a
//     semente quer PULAR a palavra, não falhar a criação da casa por causa de
//     uma palavra sugerida;
//   - ERRO DO REPOSITÓRIO É ERRO, inclusive um ErrKeywordTaken cru vindo do
//     índice único. Numa casa recém-criada, dentro da transação que a cria, não
//     existe escritor concorrente (ninguém tem token com esta casa ainda), então
//     esse erro só pode ser bug; e no PostgreSQL um comando que viola constraint
//     ABORTA A TRANSAÇÃO INTEIRA, de modo que "pular e seguir" depois de um erro
//     do banco não seria portátil entre os quatro dialetos. Quem garante o
//     "nunca falhar por colisão" é a pré-checagem em memória, não o tratamento
//     do erro.
//
// Nada aqui vai para log: palavra-chave é dado da casa, e os erros carregam
// apenas o nome da folha (constante deste arquivo) e o índice do item.
func (s *Service) semearPalavras(ctx context.Context, householdID string, criadas []folhaSemeada, now time.Time) error {
	if len(criadas) == 0 {
		return nil
	}

	// As listas já vieram validadas por validarTabela, antes da primeira
	// escrita: aqui só se coleta a norma para UMA consulta de donas.
	normas := make([]string, 0, len(criadas)*8)
	for _, folha := range criadas {
		for _, k := range folha.keywords {
			normas = append(normas, k.Norm)
		}
	}

	donas, err := s.repo.KeywordOwners(ctx, householdID, normas)
	if err != nil {
		return fmt.Errorf("verificando palavras-chave da semente: %w", err)
	}

	for _, folha := range criadas {
		livres := make([]Keyword, 0, len(folha.keywords))
		for _, k := range folha.keywords {
			if _, tomada := donas[k.Norm]; tomada {
				// Só acontece em casa pré-povoada: a palavra já é de outra
				// categoria desta casa, e o índice único é POR CASA.
				continue
			}
			// Position é renumerada: a lista da tela é 0..n-1 sem buracos,
			// mesmo quando uma palavra do meio foi pulada.
			k.Position = len(livres)
			livres = append(livres, k)
		}
		if len(livres) == 0 {
			continue
		}
		s.preencherPalavras(livres, householdID, folha.id, now)
		if err := s.repo.ReplaceKeywords(ctx, householdID, folha.id, livres); err != nil {
			return fmt.Errorf("gravando palavras-chave da semente: %w", err)
		}
	}
	return nil
}
