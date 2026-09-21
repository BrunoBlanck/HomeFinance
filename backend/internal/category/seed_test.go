package category_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// Testes da SEMENTE de casa nova (ADR-033): a tabela de `seed.go` e o que
// SeedDefaults faz com ela.
//
// A tabela é dado, e dado errado aqui é caro de um jeito específico: uma
// palavra mal escolhida não erra UMA linha — ela categoriza, em silêncio, TODAS
// as transferências da casa, e o `auto-categorize` com `dryRun: false` grava
// isso. Por isso os invariantes abaixo são travas de build, não sugestões.
//
// REGRA AO MEXER NA LISTA (ADR-026 e ADR-033): empate e falso positivo se
// resolvem NA LISTA, nunca no motor `textmatch` e nunca afrouxando um destes
// testes. Palavra que reprova sai ou é trocada por uma frase, e o motivo fica
// escrito aqui.

// --- leitura da tabela ------------------------------------------------------

// folhaDaSemente é uma folha achatada, com o grupo e o LADO DO DINHEIRO já
// resolvidos. O lado é o que importa na correspondência: os matchers são por
// lado (ADR-029g), não por natureza.
type folhaDaSemente struct {
	grupo    string
	folha    string
	kind     string
	lado     string // "income" ou "expense": o kind do LANÇAMENTO que a aceita
	dona     string // "Grupo › Folha" — serve de OwnerID nos matchers
	keywords []string
}

func folhasDaSemente() []folhaDaSemente {
	var out []folhaDaSemente
	for _, g := range category.DefaultGroups() {
		for _, f := range g.Children {
			out = append(out, folhaDaSemente{
				grupo:    g.Name,
				folha:    f.Name,
				kind:     g.Kind,
				lado:     ladoDoDinheiro(g.Kind),
				dona:     g.Name + " › " + f.Name,
				keywords: f.Keywords,
			})
		}
	}
	return out
}

// ladoDoDinheiro devolve o `kind` de LANÇAMENTO que aceita esta natureza,
// derivado de AceitaLancamento para não virar uma segunda cópia da regra de
// pareamento (ADR-029b).
func ladoDoDinheiro(kind string) string {
	if category.AceitaLancamento("income", kind) {
		return "income"
	}
	return "expense"
}

var ladosDoDinheiro = []string{"expense", "income"}

// matcherDoLado monta o matcher com TODAS as palavras da semente de um lado do
// dinheiro, com a folha como dona — exatamente como `classify.Load` monta em
// produção para uma casa recém-semeada.
//
// Um matcher NOVO por teste, e não um global: o orçamento de trabalho
// (textmatch.Budget) é por matcher e é sticky, então um teste que o estourasse
// faria os seguintes responderem erro em vez de pontuação.
func matcherDoLado(t *testing.T, lado string) *textmatch.Matcher {
	t.Helper()

	var kws []textmatch.Keyword
	for _, f := range folhasDaSemente() {
		if f.lado != lado {
			continue
		}
		for _, p := range f.keywords {
			exibivel, norm, err := textmatch.ValidateKeyword(p)
			require.NoErrorf(t, err, "palavra inválida em %s", f.dona)
			kws = append(kws, textmatch.Keyword{OwnerID: f.dona, Keyword: exibivel, Norm: norm})
		}
	}

	m, err := textmatch.NewMatcher(kws)
	require.NoError(t, err)
	return m
}

// normaDaPalavra devolve a forma de comparação, que é a que participa do
// índice único e da correspondência.
func normaDaPalavra(t *testing.T, palavra string) string {
	t.Helper()
	_, norm, err := textmatch.ValidateKeyword(palavra)
	require.NoError(t, err)
	return norm
}

// pontua devolve quanto a palavra-chave `kw` tira contra a descrição `desc`,
// pelas MESMAS funções que o motor usa. Serve aos testes de redundância, que
// olham um par isolado em vez de um ranking.
func pontua(desc, kw string) int {
	return textmatch.ScoreKeyword(
		textmatch.Tokenize(textnorm.Normalize(desc)),
		textmatch.Tokenize(textnorm.Normalize(kw)),
	)
}

// --- invariante 1: tudo na tabela é válido ----------------------------------

func TestSementeSoTemNomeNaturezaEPalavraValidos(t *testing.T) {
	t.Parallel()

	for _, g := range category.DefaultGroups() {
		assert.Truef(t, category.ValidKind(g.Kind), "natureza inválida no grupo %q", g.Name)

		if _, _, err := category.NormalizeName(g.Name); err != nil {
			t.Errorf("nome de grupo inválido %q: %v", g.Name, err)
		}
		for _, f := range g.Children {
			if _, _, err := category.NormalizeName(f.Name); err != nil {
				t.Errorf("nome de folha inválido %q › %q: %v", g.Name, f.Name, err)
			}
			for i, p := range f.Keywords {
				if _, _, err := textmatch.ValidateKeyword(p); err != nil {
					t.Errorf("palavra inválida em %q › %q [%d]: %v", g.Name, f.Name, i, err)
				}
			}
			// A lista inteira também precisa passar pela validação da BORDA,
			// que é quem a semente usa: ela recusa repetição pela norma e o
			// teto por dona.
			if _, err := category.ValidateKeywords(f.Keywords); err != nil {
				t.Errorf("lista de palavras recusada em %q › %q: %v", g.Name, f.Name, err)
			}
		}
	}

	// Folga larga contra MaxPerHousehold (200): a semente não pode chegar perto
	// do teto da casa, senão a pessoa nasce sem espaço para a taxonomia dela.
	assert.LessOrEqual(t, category.DefaultCategoryCount(), 120,
		"a semente é enxuta por decisão do usuário (ADR-033e)")
	assert.Greater(t, category.DefaultCategoryCount(), len(category.DefaultGroups()),
		"DefaultCategoryCount conta grupos MAIS folhas")
}

// --- invariante 2 (R7): teto de 16 palavras por folha semeada ---------------

// R7 existe por uma razão de produto, não de motor: MaxKeywordsPerOwner é 20, e
// uma folha que nascesse com 20 faria o atalho "Reconhecer por «x»" da tela de
// lançamentos responder 422 no PRIMEIRO uso — a pessoa receberia um "limite
// atingido" numa categoria em que nunca cadastrou nada.
func TestNenhumaFolhaSemeadaPassaDeDezesseisPalavras(t *testing.T) {
	t.Parallel()

	const teto = 16
	require.Less(t, teto, category.MaxKeywordsPerOwner,
		"o teto da semente tem de deixar vaga para a pessoa")

	for _, f := range folhasDaSemente() {
		assert.LessOrEqualf(t, len(f.keywords), teto,
			"%s tem %d palavras: passa do teto da semente", f.dona, len(f.keywords))
	}
}

// --- invariante 3: unicidade de nome e de norma -----------------------------

func TestNomesDaSementeSaoUnicosEntreIrmaos(t *testing.T) {
	t.Parallel()

	grupos := map[string]string{}
	for _, g := range category.DefaultGroups() {
		_, norm, err := category.NormalizeName(g.Name)
		require.NoError(t, err)
		if antes, repetido := grupos[norm]; repetido {
			t.Errorf("dois grupos com o mesmo nome normalizado: %q e %q", antes, g.Name)
		}
		grupos[norm] = g.Name

		folhas := map[string]string{}
		for _, f := range g.Children {
			_, normFolha, err := category.NormalizeName(f.Name)
			require.NoError(t, err)
			if antes, repetido := folhas[normFolha]; repetido {
				t.Errorf("duas folhas com o mesmo nome em %q: %q e %q", g.Name, antes, f.Name)
			}
			folhas[normFolha] = f.Name
		}
	}
}

// A unicidade da PALAVRA é global na casa, não por folha: o índice único do
// banco é (household_id, keyword_norm). Uma norma repetida em duas folhas da
// semente derrubaria a criação da casa — ou, pior, seria pulada em silêncio
// pela pré-checagem e a segunda folha nasceria sem ela.
func TestNormaDePalavraEhUnicaEmTodaASemente(t *testing.T) {
	t.Parallel()

	donas := map[string]string{}
	for _, f := range folhasDaSemente() {
		for _, p := range f.keywords {
			norm := normaDaPalavra(t, p)
			if antes, repetida := donas[norm]; repetida {
				t.Errorf("a mesma palavra em duas folhas: %s e %s (o índice único é POR CASA)", antes, f.dona)
			}
			donas[norm] = f.dona
		}
	}
}

// --- invariante 4: auto-consistência (sem empate no próprio vocabulário) ----

// Cada palavra da semente, usada como se fosse a descrição inteira do
// lançamento, tem de sugerir a PRÓPRIA folha. É o teste que pega o empate que
// R1 existe para evitar: com "mercado" solto ao lado de "mercado livre", a
// descrição "mercado livre" empataria 100×100 e o produto não sugeriria NADA —
// ambíguo é pior que vazio em dado financeiro.
func TestCadaPalavraDaSementeVenceParaAPropriaFolha(t *testing.T) {
	t.Parallel()

	for _, lado := range ladosDoDinheiro {
		m := matcherDoLado(t, lado)
		for _, f := range folhasDaSemente() {
			if f.lado != lado {
				continue
			}
			for _, p := range f.keywords {
				norm := normaDaPalavra(t, p)
				res, err := m.Best(norm)
				require.NoError(t, err)
				if !res.Matched() {
					t.Errorf("%s: a palavra não sugere nada contra si mesma (%s)", f.dona, res.Reason)
					continue
				}
				assert.Equalf(t, f.dona, res.Match.OwnerID,
					"%s: a palavra sugere a folha errada (%s, %d pontos)", f.dona, res.Match.OwnerID, res.Match.Score)
			}
		}
	}
}

// --- invariante 5 (R8): forma redundante não ocupa vaga ---------------------

// Duas palavras da MESMA folha em que uma já cobre a outra acima do limiar
// desperdiçam uma das 16 vagas: o motor sozinho já resolve singular×plural,
// erro de digitação e token contido. Entre folhas DIFERENTES a regra não vale —
// ali quem manda é o invariante 4, e cobertura cruzada é justamente o que ele
// proíbe.
func TestNenhumaPalavraDaSementeEhRedundanteNaPropriaFolha(t *testing.T) {
	t.Parallel()

	for _, f := range folhasDaSemente() {
		for i, a := range f.keywords {
			for j, b := range f.keywords {
				if i == j {
					continue
				}
				if s := pontua(a, b); s >= textmatch.MinScore {
					t.Errorf("%s: %q já é coberta por %q (%d pontos) — uma das duas tem de sair",
						f.dona, a, b, s)
				}
			}
		}
	}
}

// --- invariante 6 (R3/R4): o vocabulário de rotina dos extratos -------------

// tokensDeRotina é a LISTA FECHADA contra a qual nenhuma palavra da semente
// pode alcançar o limiar, em lado nenhum do dinheiro.
//
// É o teste mais importante deste arquivo, e a razão é a ameaça REAL desta
// feature: uma palavra que case com o boilerplate dos extratos não erra uma
// linha — ela categoriza TODAS as transferências da casa em silêncio, e o
// `POST /transactions/auto-categorize` com `dryRun: false` grava isso. Foi esse
// achado que mudou o desenho da lista durante o planejamento: "contador"
// pontua 89 contra o token "conta", que aparece em TODA linha de Pix do Nubank;
// "seguro" pontua 90 contra "pagseguro"; "perfumaria", 85 contra "maria".
//
// Cada token é testado ISOLADO, como se fosse a descrição inteira: é o pior
// caso e o mais fácil de auditar. A lista NÃO ENCOLHE sem justificativa escrita
// (ADR-033d) — tirar um token daqui para fazer uma palavra caber é exatamente o
// movimento que este teste existe para impedir.
//
// Os blocos dizem de onde cada grupo vem.
//
// O QUE FICOU DELIBERADAMENTE DE FORA, e por quê — registrado aqui para a
// ausência ser auditável em vez de silenciosa. Estes tokens CARREGAM sinal de
// categoria: eles são o que a semente quer casar, não o ruído que ela precisa
// evitar. Pô-los na lista não provaria segurança nenhuma; só forçaria a remoção
// da palavra que os atende.
//
//	rendimento, estorno, devolucao, reembolso → são a receita em si
//	                                            ("RENDIMENTO PAGO", "Estorno Tarifa")
//	atacado                                   → é uma compra de supermercado
//	condominios                               → é a despesa de moradia
//
//	mercado  → A EXCEÇÃO MAIOR, e a que faltava estar escrita aqui (achado A2
//	           da revisão de 18/09/2026). Ele NÃO entra, e o falso positivo
//	           `MERCADO PAGO` → Supermercado (89) é aceito, porque não há
//	           conserto barato: medido, «supermercado» sozinho já tira 88
//	           contra o token `mercado`, então tirar «minimercado» não resolve
//	           — só sairia «supermercado», que é a palavra mais usada do app.
//	           O caso está fixado no corpus com a nota.
//
//	ultra  → MODIFICADOR DE MARCA sem conserto grátis (achado N1 da
//	         reconferência de 18/09/2026). «ultragaz» tira 89 contra ele e
//	         «ultrafarma» 85, e as duas marcas se chamam assim de verdade:
//	         remover qualquer uma perde a marca inteira («gás» sozinha tira 0
//	         contra `ULTRAGAZ SA`). É o PIOR caso aceito da semente, e vale
//	         dito sem suavizar: em `ULTRA COMERCIO DE PECAS` a resposta CERTA
//	         («autopeças», 87) PERDE para a errada («ultragaz», 89). Resposta
//	         errada ganhando de resposta certa é pior que ganhar do silêncio.
//
//	colar  → mesma família: «decolar» tira 91 contra ele, e `JOALHERIA COLAR
//	         DE PRATA` vira Lazer › Viagens. Tirar «decolar» perde a marca
//	         («viagem» e «passagem» tiram 0 contra `DECOLAR.COM`). Nota de
//	         margem: `escolar` também alcança «decolar», a 80 — hoje é inócuo
//	         porque «escola» co-dispara a 96 e vence, mas a folga é de 16
//	         pontos e depende de «escola» continuar na semente.
//
//	smart  → MESMA FAMÍLIA de `ultra` e `colar`, e por isso EXCEÇÃO ACEITA —
//	         não "pendente". O rótulo importa: "pendente" promete um conserto
//	         que, medido, tem preço, e pendência sem prazo vira dívida
//	         invisível. Os DOIS números, lado a lado:
//
//	         o que custa — «smartphone» tira 85 contra `smart`, e
//	         `SMART TECH SOLUCOES` cai em Compras › Eletrônicos;
//
//	         o que sustenta — sem ela cai a família genérica inteira:
//	         `COMPRA SMARTPHONE` 100, `CELULAR E SMARTPHONE` 100,
//	         `IPLACE SMARTPHONE` 100, `LOJA DE SMARTPHONES` 97,
//	         `SMARTPHONES E ACESSORIOS` 97. A palavra mais próxima da folha,
//	         «iphone», chega a 77–78 nessas linhas: abaixo do limiar. As
//	         MARCAS não dependem dela (`SAMSUNG STORE`, `XIAOMI BRASIL`,
//	         `IPHONE 15 PRO`, `NOTEBOOK DELL`, `KABUM` casam a 100 por palavra
//	         própria), mas "IPLACE SMARTPHONE" e "LOJA DE SMARTPHONES" são
//	         nome de loja de verdade. E não há grafia de escape: «smartphones»
//	         ainda entrega `smart`, a 84.
//
//	         A favor, e medido: o resíduo ficou ESTRITAMENTE MENOS ERRADO que
//	         antes da troca de «smartfit» — caiu de 89 para 85, e o destino
//	         saiu de "Saúde › Academia e esportes" (absurdo para uma
//	         consultoria) para "Compras › Eletrônicos", que é adjacente ao que
//	         uma empresa chamada SMART TECH de fato faz.
//
// O CRITÉRIO, estabelecido na reconferência: um token só vira exceção quando
// pô-lo na lista obrigaria a SACRIFICAR uma palavra. Se a palavra pode sair ou
// virar frase sem perder cobertura, o token entra na lista — foi o que
// aconteceu com `prime`, que está no bloco (d″).
//
// Quem NÃO é exceção, e vale dizer porque a pergunta volta: `express` ESTÁ na
// lista (bloco d). Ele coube porque «aliexpress» já tinha saído da semente por
// este mesmo motivo (91 contra `express`), então não havia palavra a sacrificar.
//
// A fronteira é esta: entra na lista o token que aparece numa fração GRANDE das
// linhas sem dizer nada sobre a natureza do gasto (boilerplate, nome de pessoa,
// marca de banco, lugar, modificador de razão social). Um token que identifica o
// que foi comprado não entra — e se ele criar um falso positivo mesmo assim,
// quem resolve é o corpus (`seed_corpus_test.go`), com o caso escrito e medido.
//
// ⚠️ Esta lista de exceções é INVENTÁRIO COMPLETO, não uma amostra. Se você
// decidir não pôr um token perigoso aqui dentro, ele entra nesta relação com o
// número — foi a omissão de `mercado` que a revisão apontou.
var tokensDeRotina = []string{
	// (a) Boilerplate dos extratos. Colhido dos parsers reais em
	// backend/internal/importer/{nubank,inter,c6} e dos seus testdata:
	// "Transferência enviada pelo Pix - FULANO - ... - BANCO (0077) Agência: 1
	// Conta: 123-4", "Pagamento de boleto efetuado", "Compra no débito",
	// "TRANSF ENVIADA PIX", "PGTO FAT CARTAO", "Pix automático enviado para".
	"pix", "ted", "doc", "tev", "transf", "transferencia", "transferencias",
	"enviada", "enviado", "enviados", "recebida", "recebido", "recebidos",
	"agencia", "conta", "contas", "corrente", "saldo", "extrato", "lancamento",
	"lancamentos", "historico", "descricao", "data", "valor", "credito",
	"debito", "compra", "compras", "pagamento", "pagamentos", "pago", "pagto",
	"pgto", "efetuado", "efetuada", "cartao", "boleto", "fatura", "parcela",
	"parcelamento", "parcelado", "unica", "estabelecimento", "operacao",
	"documento", "cpf", "cnpj", "titular", "favorecido", "remetente",
	"destinatario", "beneficiario", "chave", "deposito", "saque", "convenio",
	"cobranca", "vencimento", "automatico", "agendado", "agendamento",
	"recorrente", "assinatura", "mensalidade", "aplicacao", "resgate",
	"ajuste", "inclusao", "emissao", "emissoes", "cheque", "cheques",
	"ordem", "liquidacao", "compensacao", "cotacao", "manutencao",
	"aut", "ref", "obs", "seq", "nsu", "num", "numero", "codigo", "id",
	"entrada", "saida", "periodo", "contabil", "dia", "mes", "ano",

	// (b) Marcas de banco, fintech e adquirente. Elas são palavra-chave de
	// CONTA (ADR-026e/028b) e nunca de categoria: uma delas na semente
	// disputaria a detecção de transferência interna com a conta da própria
	// pessoa.
	"nubank", "nu", "inter", "itau", "unibanco", "bradesco",
	"santander", "banco", "bco", "brasil", "caixa", "bb", "sicoob", "sicredi",
	"safra", "original", "neon", "next", "c6", "btg", "modal", "daycoval",
	"pine", "abc", "votorantim", "banrisul", "brb", "bnb", "bmg", "pan",
	"agibank", "digio", "superdigital", "iti", "will", "willbank", "picpay",
	"pagseguro", "pagbank", "mercadopago", "stone", "sumup", "getnet", "cielo",
	"rede", "pagarme", "infinitepay", "cloudwalk", "zoop", "adyen", "paypal",
	"wise", "remessa", "ebanx", "juno", "cora", "asaas", "celcoin", "dock",
	"tecban", "banco24horas", "crefisa", "creditas", "recargapay", "ame",
	"bs2", "sofisa", "ourinvest", "paulista", "tribanco", "bradescard",
	"losango", "fininvest", "credsystem", "portocred", "omni", "biz",

	// (c) Nomes e sobrenomes comuns. São o corpo de QUALQUER Pix entre pessoas
	// — a descrição mais frequente de um extrato brasileiro. Foi aqui que
	// "pintor" (95 contra "pinto"), "perfumaria" (85 contra "maria") e
	// "ferias" (83 contra "farias") reprovaram na validação da lista.
	"silva", "santos", "souza", "sousa", "oliveira", "pereira", "lima",
	"carvalho", "ferreira", "rodrigues", "almeida", "costa", "gomes",
	"martins", "araujo", "melo", "barbosa", "ribeiro", "alves", "monteiro",
	"mendes", "barros", "freitas", "nascimento", "andrade", "moreira", "nunes",
	"marques", "machado", "rocha", "dias", "campos", "cardoso", "teixeira",
	"correia", "cunha", "dantas", "duarte", "fernandes", "figueiredo",
	"fonseca", "galvao", "garcia", "goncalves", "guimaraes", "jesus", "leal",
	"leite", "lopes", "macedo", "magalhaes", "maia", "medeiros", "miranda",
	"moraes", "morais", "moura", "neves", "nogueira", "pacheco", "paiva",
	"pinheiro", "pinto", "pires", "prado", "queiroz", "ramos", "reis",
	"rezende", "sales", "sampaio", "sanches", "saraiva", "siqueira", "tavares",
	"torres", "vasconcelos", "veloso", "viana", "vieira", "xavier", "farias",
	"batista", "bezerra", "borges", "brito", "camargo", "cavalcante",
	"coelho", "domingues", "esteves", "furtado", "godoy", "lacerda", "lemos",
	"menezes", "mota", "peixoto", "pontes", "rangel", "salgado", "simoes",
	"soares", "valente", "assuncao", "bandeira", "caldeira", "chaves",
	"cordeiro", "couto", "drumond", "fagundes", "gouveia", "guedes",
	"junqueira", "loureiro", "matias", "mesquita", "novaes", "padilha",
	"quintanilha", "rabelo", "sarmento", "seixas", "tenorio", "ulhoa",
	"varella", "zanetti",
	"joao", "jose", "maria", "ana", "antonio", "francisco", "carlos", "paulo",
	"pedro", "lucas", "luiz", "luis", "marcos", "luiza", "gabriel", "rafael",
	"daniel", "marcelo", "bruno", "eduardo", "felipe", "raimundo", "rodrigo",
	"manoel", "mateus", "matheus", "andre", "fernando", "fabio", "leonardo",
	"ricardo", "sergio", "thiago", "tiago", "gustavo", "guilherme", "vitor",
	"victor", "alexandre", "diego", "juliana", "adriana", "fernanda",
	"patricia", "aline", "sandra", "camila", "amanda", "bruna", "jessica",
	"leticia", "julia", "luciana", "vanessa", "mariana", "gabriela", "vera",
	"vitoria", "larissa", "claudia", "simone", "carla", "beatriz", "rosa",
	"isabel", "alice", "helena", "laura", "manuela", "sofia", "valentina",
	"heitor", "arthur", "miguel", "davi", "bernardo", "theo", "lorenzo",
	"samuel", "henrique", "isaac", "benicio", "enzo", "nicolas", "joaquim",
	"benjamin", "vicente", "caio", "emanuel", "leandro", "wesley",
	"wellington", "jefferson", "anderson", "cristiano", "robson", "renato",
	"roberto", "ronaldo", "rogerio", "sebastiao", "severino", "valdir",
	"wagner", "washington", "willian", "william", "cleber", "edson", "elias",
	"everton", "geraldo", "gilberto", "hugo", "ivan", "jair", "jorge", "julio",
	"marcio", "mario", "mauricio", "nelson", "osvaldo", "otavio", "raul",
	"reinaldo", "rui", "saulo", "silvio", "tadeu", "valter", "walter",
	"cristina", "regina", "elaine", "denise", "monica", "solange", "rita",
	"marta", "teresa", "cecilia", "eliane", "silvia", "sonia", "angela",
	"debora", "priscila", "renata", "tatiane", "viviane", "michele", "carmen",

	// (d) Lugares e modificadores de razão social. Aparecem coladas ao nome do
	// estabelecimento ("ENEL DISTRIBUICAO SAO PAULO", "COMERCIO DE ALIMENTOS
	// LTDA") e por isso competem com a palavra que deveria decidir.
	"sao", "rio", "janeiro", "belo", "horizonte", "brasilia",
	"salvador", "fortaleza", "curitiba", "recife", "porto", "alegre",
	"manaus", "belem", "goiania", "campinas", "niteroi", "osasco", "sorocaba",
	"uberlandia", "contagem", "joinville", "londrina", "natal", "teresina",
	"cuiaba", "maceio", "aracaju", "florianopolis", "palmas", "macapa",
	"centro", "jardim", "vila", "bairro", "avenida", "rua", "praca", "norte",
	"sul", "leste", "oeste", "matriz", "filial", "shopping", "galeria",
	"comercio", "comercial", "industria", "servicos", "servico",
	"empreendimentos", "participacoes", "holding", "grupo", "distribuidora",
	"distribuicao", "representacoes", "assessoria", "consultoria", "solucoes",
	"sistemas", "tecnologia", "negocios", "gestao", "administradora",
	"empresa", "associacao", "instituto", "fundacao", "cooperativa",
	"imobiliaria", "engenharia", "construtora", "transportes",
	"logistica", "alimentos", "bebidas", "produtos", "materiais",
	"varejo", "loja", "lojas", "magazine", "central", "nacional", "regional",
	"brasileira", "brasileiro",

	// (d′) Razão social — REFORÇO de 18/09/2026, exigido pela revisão de
	// segurança (achado A1). O desequilíbrio era estrutural e vale escrito:
	// `sanitize.Description` CORTA a descrição no primeiro segmento com
	// documento (sanitize.go), então numa linha de Pix do Nubank o nome da
	// instituição NUNCA chega ao matcher — a linha enorme do Itaú vira
	// "pix enviado - energia exemplo s.a.". Ou seja, boa parte do bloco (b)
	// defende um texto que, naquele formato, não existe.
	//
	// O que SOBREVIVE inteiro é a razão social: no Inter ela vem no par
	// Histórico;Descrição ("Pagamento efetuado;ADMINISTRADORA DE IMOVEIS
	// LTDA") e no C6 no Título — sem documento na linha, não há onde cortar.
	// É por isso que TODOS os falsos positivos achados pela revisão estavam
	// aqui, e é para cá que a lista cresce.
	"imoveis", "imovel", "imobiliarios", "mobiliario", "automoveis",
	"veiculos", "sindicato", "sindical", "otica", "oticas", "express",
	"administracao", "incorporadora", "incorporacoes", "patrimonial",
	"patrimonio", "agropecuaria", "agricola", "industrial", "industrias",
	"financeira", "fomento", "escritorio", "confeccoes", "confeccao",
	"equipamentos", "maquinas", "ferramentas", "artigos", "importacao",
	"exportacao", "locadora", "editora", "grafica", "multimarcas",
	"franquia", "franqueado", "unidade", "estabelecimentos",

	// (d″) Modificador de MARCA — achado N1 da reconferência de 18/09/2026.
	// Marca escrita COLADA vaza o pedaço comum: `prime` tirava 84 contra
	// «amazonprime», e `PRIME CONSULTORIA` virava Lazer › Streaming. Coube na
	// lista porque «amazonprime» pôde sair sem custo — «amazon» sozinha cobre
	// `AMAZON PRIME BR` (100) e até a grafia colada `AMAZONPRIMEBR` (84).
	// Os que NÃO couberam estão no bloco de exceções acima, com o número.
	"prime",
}

func TestNenhumaPalavraDaSementeCasaComOVocabularioDeRotina(t *testing.T) {
	t.Parallel()

	// IGUALDADE EXATA, não piso (achado A3 da revisão de 18/09/2026). Um piso
	// com folga admite exatamente o movimento que o ADR-033(d) proíbe:
	// acrescentar uma palavra perigosa à semente e remover em silêncio os três
	// ou quatro tokens que ela quebraria, com o build verde. Com igualdade,
	// TROCAR um token por outro continua possível e vira um diff de duas linhas
	// que o revisor vê; PODAR quebra.
	assert.Equal(t, 581, len(tokensDeRotina),
		"a lista de tokens perigosos não muda de tamanho sem emenda ao ADR-033")

	for _, lado := range ladosDoDinheiro {
		m := matcherDoLado(t, lado)
		for _, token := range tokensDeRotina {
			rank, err := m.Rank(textnorm.Normalize(token))
			require.NoError(t, err)
			for _, match := range rank {
				if match.Score >= textmatch.MinScore {
					t.Errorf("token de rotina %q casa com %q (%s) a %d pontos no lado %s",
						token, match.Keyword, match.OwnerID, match.Score, lado)
				}
			}
		}
	}
}

// A lista de tokens perigosos só vale se não tiver repetição escondida — 420
// entradas com 80 duplicatas são 340 entradas.
func TestTokensDeRotinaNaoTemRepeticao(t *testing.T) {
	t.Parallel()

	vistos := map[string]struct{}{}
	for _, tok := range tokensDeRotina {
		norm := textnorm.Normalize(tok)
		require.NotEmpty(t, norm, "token vazio na lista de rotina")
		assert.Equalf(t, tok, norm, "o token %q tem de ser escrito já normalizado", tok)
		if _, repetido := vistos[norm]; repetido {
			t.Errorf("token repetido na lista de rotina: %q", tok)
		}
		vistos[norm] = struct{}{}
	}
}

// --- invariante 7: denylist literal -----------------------------------------

// R4 em forma de lista: meio de pagamento e marca de banco NUNCA são palavra de
// categoria. O invariante 6 já cobriria a maioria por pontuação, mas esta trava
// é literal de propósito — ela não depende de o motor continuar pontuando do
// mesmo jeito.
//
// A comparação é pela palavra INTEIRA (norma), não por token contido: "folha de
// pagamento" é legítima porque a frase exige os DOIS tokens, e "pagamento"
// sozinho nunca a alcança (o invariante 6 confere isso com o motor de verdade).
func TestSementeNaoUsaMeioDePagamentoNemMarcaDeBanco(t *testing.T) {
	t.Parallel()

	proibidas := []string{
		"pix", "ted", "doc", "debito", "credito", "compra", "pagamento",
		"cartao", "boleto", "transferencia", "deposito", "saque",
		"nubank", "inter", "itau", "bradesco", "santander", "caixa", "c6",
		"picpay", "mercado pago", "pagseguro", "pagbank", "stone", "cielo",
		"getnet", "paypal", "banco", "bco",
	}

	tomadas := map[string]string{}
	for _, f := range folhasDaSemente() {
		for _, p := range f.keywords {
			tomadas[normaDaPalavra(t, p)] = f.dona
		}
	}
	for _, proibida := range proibidas {
		if dona, existe := tomadas[textnorm.Normalize(proibida)]; existe {
			t.Errorf("a semente usa %q como palavra-chave (em %s) — R4 proíbe", proibida, dona)
		}
	}
}

// --- invariante 8 auxiliar: nenhuma palavra vaza em erro --------------------

// A semente não pode vazar palavra-chave em erro nenhum: o erro do serviço
// passa pelo log estruturado do handler, e palavra-chave é dado da casa
// (docs/SEGURANCA.md §4). Aqui o dado é de FÁBRICA, mas o caminho do erro é o
// mesmo — e um erro que ecoasse a palavra numa casa seria o mesmo erro que
// ecoaria a palavra de outra.
func TestErroDaSementeNaoEcoaPalavraChave(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	repo.erroPalavras = fmt.Errorf("falha de infraestrutura")
	svc := novoServico(t, repo)

	err := svc.SeedDefaults(t.Context(), minhaCasa)
	require.Error(t, err)

	// Os DOIS lados normalizados: comparar a mensagem crua contra a norma
	// deixaria passar exatamente a palavra acentuada ("farmácia" na mensagem
	// nunca conteria "farmacia"), que é metade da tabela.
	msg := textnorm.Normalize(err.Error())
	require.NotEmpty(t, msg)
	for _, f := range folhasDaSemente() {
		for _, p := range f.keywords {
			// Palavra com menos de 5 runas casaria por acaso dentro de
			// qualquer frase ("id", "oi", "bar"): para essas, a comparação é
			// por PALAVRA INTEIRA, não por substring.
			norm := normaDaPalavra(t, p)
			if utf8.RuneCountInString(norm) < 5 {
				assert.NotContainsf(t, strings.Fields(msg), norm,
					"o erro da semente ecoa a palavra %q", p)
				continue
			}
			assert.NotContainsf(t, msg, norm, "o erro da semente ecoa a palavra %q", p)
		}
	}
}

// --- invariante 9: a semente numa casa --------------------------------------

func TestSementeCriaAArvoreInteiraNaCasaNova(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	total, err := repo.CountAll(ctx, minhaCasa)
	require.NoError(t, err)
	assert.EqualValues(t, category.DefaultCategoryCount(), total)

	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)

	grupos := map[string]category.View{}
	for _, lista := range [][]category.View{arvore.Expense, arvore.Income, arvore.Investment, arvore.Redemption} {
		for _, g := range lista {
			grupos[g.Name] = g
		}
	}

	var palavras int
	for _, esperado := range category.DefaultGroups() {
		g, achado := grupos[esperado.Name]
		require.Truef(t, achado, "o grupo %q não foi criado", esperado.Name)
		assert.Nil(t, g.ParentID, "grupo da semente é de topo")
		assert.Equal(t, esperado.Kind, g.Kind)
		assert.Emptyf(t, g.Keywords, "grupo com filhas não recebe palavra (spec 0005 §12): %q", g.Name)
		require.Lenf(t, g.Children, len(esperado.Children), "filhas de %q", g.Name)

		// A árvore volta ORDENADA POR NOME (o repositório ordena por
		// name_norm), não na ordem da tabela — por isso a busca é por nome.
		filhas := map[string]category.View{}
		for _, f := range g.Children {
			filhas[f.Name] = f
		}
		for _, folhaEsperada := range esperado.Children {
			folha, achada := filhas[folhaEsperada.Name]
			require.Truef(t, achada, "a folha %q › %q não foi criada", g.Name, folhaEsperada.Name)
			// A natureza da folha vem do PAI, nunca de campo da tabela dela.
			assert.Equalf(t, esperado.Kind, folha.Kind, "a folha %q não herdou a natureza do grupo", folha.Name)
			require.NotNil(t, folha.ParentID)
			assert.Equal(t, g.ID, *folha.ParentID)
			assert.Equalf(t, folhaEsperada.Keywords, folha.Keywords,
				"palavras de %q › %q, na ordem de cadastro", g.Name, folha.Name)
			palavras += len(folha.Keywords)
		}
	}

	assert.Equal(t, palavrasDaSemente(), palavras, "toda palavra da tabela foi gravada")

	// Position 0..n-1, sem buraco: é a ordem que volta para a tela.
	for _, lista := range repo.palavras {
		for i, k := range lista {
			assert.Equal(t, i, k.Position)
			assert.Equal(t, minhaCasa, k.HouseholdID)
			assert.NotEmpty(t, k.ID)
			assert.NotEmpty(t, k.Norm)
		}
	}
}

// palavrasDaSemente conta as palavras da tabela — derivado, nunca um número
// escrito à mão: a lista muda, e um literal aqui só diria que ela mudou.
func palavrasDaSemente() int {
	total := 0
	for _, f := range folhasDaSemente() {
		total += len(f.keywords)
	}
	return total
}

// O orçamento de comandos da semente é constante e pequeno, e isso é parte do
// contrato: ela roda DENTRO da transação que cria a casa, na verificação do
// e-mail. Uma consulta por grupo (o desenho anterior, com NameTaken) viraria 56
// idas ao banco; uma consulta de donas por folha viraria 41.
func TestSementeConsultaDonasUmaVezENuncaNameTaken(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	require.NoError(t, svc.SeedDefaults(t.Context(), minhaCasa))

	assert.LessOrEqual(t, repo.donasConsultadas, 1, "uma KeywordOwners para a casa inteira")
	assert.Zero(t, repo.nomesConsultados, "a semente não consulta NameTaken: ela já leu os nomes com List")
}

func TestSementeRodadaTresVezesNaoMudaContagemNenhuma(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))
	categorias, err := repo.CountAll(ctx, minhaCasa)
	require.NoError(t, err)
	palavras, err := repo.ListKeywords(ctx, minhaCasa)
	require.NoError(t, err)

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))
	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	depoisCategorias, err := repo.CountAll(ctx, minhaCasa)
	require.NoError(t, err)
	depoisPalavras, err := repo.ListKeywords(ctx, minhaCasa)
	require.NoError(t, err)

	assert.EqualValues(t, categorias, depoisCategorias)
	assert.Len(t, depoisPalavras, len(palavras))
	assert.EqualValues(t, category.DefaultCategoryCount(), depoisCategorias)
}

// Grupo que a casa já tem fica INTOCADO — com qualquer natureza, com ou sem
// palavras. É a decisão (b) do ADR-033: folha e palavra só nascem junto com o
// grupo que a própria execução criou.
func TestSementeNaoTocaGrupoPreExistenteNemAsPalavrasDele(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	// "Alimentação" criada à mão, com uma palavra própria e sem filhas.
	daCasa, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{
		Name:     "Alimentação",
		Kind:     category.KindExpense,
		Keywords: []string{"quitanda do ze"},
	})
	require.NoError(t, err)

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	atual, err := svc.Get(ctx, ator(minhaCasa), daCasa.ID)
	require.NoError(t, err)
	assert.Empty(t, atual.Children, "o grupo da casa não ganha filhas da semente")
	assert.Equal(t, []string{"quitanda do ze"}, atual.Keywords,
		"as palavras do grupo da casa continuam valendo — elas não ficam inertes")

	// E as folhas de Alimentação da semente não existem em lugar nenhum.
	for _, c := range repo.linhas {
		assert.NotEqual(t, "Supermercado", c.Name)
		assert.NotEqual(t, "Delivery", c.Name)
	}
}

// Grupo ARQUIVADO com o mesmo nome também segura a semente (achado A4 da
// revisão de 18/09/2026).
//
// Com o `List(includeArchived=false)` da primeira versão, a casa que arquivou
// "Moradia" ganharia um SEGUNDO "Moradia" — ativo, com três folhas — ao lado do
// que a pessoa escondeu de propósito, e a promessa de "segunda execução é no-op
// TOTAL" (ADR-033b) seria falsa. Não há caminho de produção que dispare isso
// hoje, e é exatamente por isso que fechar a porta custa zero.
func TestSementeNaoRessuscitaGrupoArquivado(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	daCasa := criarGrupo(t, svc, minhaCasa, "Moradia", category.KindExpense)
	_, err := svc.Archive(ctx, ator(minhaCasa), daCasa.ID)
	require.NoError(t, err)

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	var moradias int
	for _, c := range repo.linhas {
		if c.HouseholdID == minhaCasa && c.NameNorm == "moradia" {
			moradias++
		}
	}
	assert.Equal(t, 1, moradias, "existe exatamente UMA Moradia, a que a pessoa arquivou")

	// E ela continua arquivada e sem filhas: a semente não tocou em nada dela.
	assert.NotNil(t, repo.linhas[daCasa.ID].ArchivedAt)
	filhas, err := repo.Children(ctx, minhaCasa, daCasa.ID)
	require.NoError(t, err)
	assert.Empty(t, filhas, "grupo pré-existente não ganha folhas, nem arquivado")
}

// Palavra que a casa já usa em OUTRA categoria é pulada em silêncio: a folha da
// semente nasce sem ela e com as demais renumeradas. É o que impede a criação
// da casa de falhar por causa de uma palavra sugerida.
func TestSementePulaPalavraJaUsadaPelaCasa(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	_, err := svc.Create(ctx, ator(minhaCasa), category.CreateInput{
		Name:     "Comida",
		Kind:     category.KindExpense,
		Keywords: []string{"ifood"},
	})
	require.NoError(t, err)

	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	delivery := folhaPorNome(t, svc, "Alimentação", "Delivery")
	esperadas := semA(t, "Alimentação", "Delivery", "ifood")
	assert.Equal(t, esperadas, delivery.Keywords, "a folha nasce sem a palavra já tomada")

	for i, k := range repo.palavras[delivery.ID] {
		assert.Equal(t, i, k.Position, "as posições são renumeradas sem buraco")
	}
}

// folhaPorNome acha a folha da semente na árvore devolvida pelo serviço.
func folhaPorNome(t *testing.T, svc *category.Service, grupo, folha string) category.View {
	t.Helper()
	arvore, err := svc.List(t.Context(), ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)
	for _, lista := range [][]category.View{arvore.Expense, arvore.Income, arvore.Investment, arvore.Redemption} {
		for _, g := range lista {
			if g.Name != grupo {
				continue
			}
			for _, f := range g.Children {
				if f.Name == folha {
					return f
				}
			}
		}
	}
	t.Fatalf("folha %q › %q não encontrada", grupo, folha)
	return category.View{}
}

// semA devolve as palavras da folha da tabela menos a informada.
func semA(t *testing.T, grupo, folha, palavra string) []string {
	t.Helper()
	for _, f := range folhasDaSemente() {
		if f.grupo != grupo || f.folha != folha {
			continue
		}
		out := make([]string, 0, len(f.keywords))
		for _, p := range f.keywords {
			if p != palavra {
				out = append(out, p)
			}
		}
		require.Len(t, out, len(f.keywords)-1, "a palavra tinha de estar na tabela")
		return out
	}
	t.Fatalf("folha %q › %q não encontrada na tabela", grupo, folha)
	return nil
}

// O teto da casa para a semente em silêncio, mas o que já nasceu recebe as suas
// palavras: parar de semear não é motivo para deixar folha muda.
func TestSementeParaNoTetoSemDeixarFolhaMuda(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	const sobra = 5
	encherTaxonomia(t, svc, minhaCasa, category.MaxPerHousehold-sobra)
	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa))

	total, err := repo.CountAll(ctx, minhaCasa)
	require.NoError(t, err)
	assert.EqualValues(t, category.MaxPerHousehold, total, "a semente não empurra a casa além do teto")

	arvore, err := svc.List(ctx, ator(minhaCasa), category.ListInput{})
	require.NoError(t, err)

	var moradia *category.View
	for i := range arvore.Expense {
		if arvore.Expense[i].Name == "Moradia" {
			moradia = &arvore.Expense[i]
		}
	}
	require.NotNil(t, moradia, "o primeiro grupo da semente cabe nas 5 vagas")
	require.Len(t, moradia.Children, 3, "Moradia inteira coube: 1 grupo + 3 folhas")
	for _, folha := range moradia.Children {
		assert.NotEmptyf(t, folha.Keywords, "a folha %q nasceu antes do teto e tem de ter palavras", folha.Name)
	}

	// A quinta vaga foi para o grupo seguinte, que entrou SEM folhas — o teto
	// bateu no meio dele. Nenhuma palavra sobrou pendurada nele.
	alimentacao := grupoPorNome(arvore.Expense, "Alimentação")
	require.NotNil(t, alimentacao)
	assert.Empty(t, alimentacao.Children)
	assert.Empty(t, alimentacao.Keywords)
}

func TestSementeNaCasaCheiaNaoCriaNadaENaoFalha(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)
	ctx := t.Context()

	encherTaxonomia(t, svc, minhaCasa, category.MaxPerHousehold)
	require.NoError(t, svc.SeedDefaults(ctx, minhaCasa),
		"falhar aqui trancaria a criação da casa de quem já tem a taxonomia cheia")

	total, err := repo.CountAll(ctx, minhaCasa)
	require.NoError(t, err)
	assert.EqualValues(t, category.MaxPerHousehold, total)
	assert.Empty(t, repo.palavras, "sem categoria criada, não há palavra a gravar")
}

func grupoPorNome(lista []category.View, nome string) *category.View {
	for i := range lista {
		if lista[i].Name == nome {
			return &lista[i]
		}
	}
	return nil
}

// Erro do repositório é ERRO, e a criação da casa desfaz a transação inteira.
// Vale inclusive para o ErrKeywordTaken cru do índice único: numa casa
// recém-criada não há escritor concorrente, então ele só pode ser bug — e no
// PostgreSQL uma violação de constraint aborta a transação inteira, de modo que
// "pular e seguir" nem seria portátil.
func TestSementeDevolveErroDoRepositorioDePalavras(t *testing.T) {
	t.Parallel()

	casos := map[string]error{
		"falha de infraestrutura": fmt.Errorf("conexão perdida"),
		"colisão crua do índice":  category.ErrKeywordTaken,
	}
	for nome, falha := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			repo := novoRepo()
			repo.erroPalavras = falha
			svc := novoServico(t, repo)

			err := svc.SeedDefaults(t.Context(), minhaCasa)
			require.Error(t, err)
			assert.ErrorIs(t, err, falha)
		})
	}
}

func TestSementeExigeACasa(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	assert.ErrorIs(t, svc.SeedDefaults(t.Context(), ""), category.ErrNotFound)
	assert.Empty(t, repo.linhas)
}

// --- relatório da tabela (não é asserção: é o que orienta quem vai mexer) ----

// TestResumoDaSemente não trava nada — ele IMPRIME a forma da tabela com
// `go test -v`, para quem for mexer na lista ver o tamanho do que está
// mudando sem precisar contar à mão.
func TestResumoDaSemente(t *testing.T) {
	t.Parallel()

	porLado := map[string]int{}
	var folhas int
	for _, f := range folhasDaSemente() {
		folhas++
		porLado[f.lado] += len(f.keywords)
	}
	lados := make([]string, 0, len(porLado))
	for lado := range porLado {
		lados = append(lados, lado)
	}
	sort.Strings(lados)

	t.Logf("semente: %d grupos, %d folhas, %d categorias, %d palavras",
		len(category.DefaultGroups()), folhas, category.DefaultCategoryCount(), palavrasDaSemente())
	for _, lado := range lados {
		t.Logf("  lado %s: %d palavras", lado, porLado[lado])
	}
}
