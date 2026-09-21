package category_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
)

// CORPUS DA SEMENTE — descrições reais de extrato contra a folha que a semente
// sugere para elas (ADR-033).
//
// Os invariantes de `seed_test.go` provam propriedades da tabela; este arquivo
// prova COMPORTAMENTO, que é o que a pessoa vê. Ele existe por três razões:
//
//  1. fixar o acerto — mexer na lista e perder `IFOOD` ou `ENEL` quebra o build;
//  2. fixar o que NÃO tem sugestão de fábrica — Pix entre pessoas, marca de
//     banco, boilerplate. "Nenhuma" é resposta certa, e um teste que só olhasse
//     acertos aceitaria em silêncio uma lista que categoriza tudo;
//  3. escrever os FALSOS POSITIVOS e os EMPATES aceitos. Eles existem, foram
//     medidos e a decisão de conviver com eles está aqui, com o número — não
//     numa conversa que ninguém vai reler.
//
// A descrição vai como o extrato a traz (maiúsculas, acento, pontuação); a
// normalização é a mesma de produção.
//
// POR QUE ESTE ARQUIVO NÃO CHAMA `importer.Describe` (achado A5 da revisão de
// 18/09/2026). O texto que chega ao classificador é `sanitize.Description` +
// `textnorm`, e não a descrição crua. Este teste normaliza direto, e isso é
// DELIBERADO por dois motivos:
//
//   - para as linhas de Pix, normalizar direto é MAIS SEVERO que produção: o
//     sanitizador CORTA no primeiro segmento com documento, então o texto real
//     é menor do que o testado aqui. Quem passa no corpus passa em produção;
//   - para os casos que a revisão encontrou — razão social do Inter e do C6 —
//     `Describe` é literalmente um NO-OP: não há documento na linha para cortar
//     nem prefixo verboso para reescrever. Conferido: "Pagamento efetuado -
//     ADMINISTRADORA DE IMOVEIS LTDA" sai de `Describe` idêntico. Para esses
//     casos o corpus não é "mais severo", é EXATO.
//
// O que o sanitizador de fato FABRICA — os seis prefixos canônicos de
// `sanitize.prefixosCanonicos` — está no corpus na forma já reescrita ("Pix
// reembolso recebido - …"), que é o que o matcher vê. `internal/category` não
// importa `internal/importer` de propósito: o domínio não conhece o parser.
type casoDoCorpus struct {
	desc string
	// lado é o `kind` do LANÇAMENTO: "expense" (sai da conta) ou "income".
	lado string
	// folha é "Grupo › Folha", ou uma das duas constantes abaixo.
	folha string
	// nota explica o caso quando ele não é óbvio — falso positivo aceito,
	// empate, perda conhecida do enxugamento.
	nota string
}

const (
	// nenhuma: sem sugestão de fábrica. É resposta CERTA, não lacuna.
	nenhuma = "«nenhuma»"
	// ambigua: dois donos empatam na pontuação máxima e o motor se cala —
	// ambíguo é pior que vazio em dado financeiro (§3 da spec 0005).
	ambigua = "«ambígua»"
)

func TestCorpusDaSemente(t *testing.T) {
	t.Parallel()

	casos := corpusDaSemente()
	// IGUALDADE EXATA, não piso (achado A3 da revisão de 18/09/2026): com
	// folga, um caso inconveniente pode ser podado em silêncio enquanto outro
	// entra. Trocar continua sendo um diff visível; remover quebra.
	require.Equal(t, 296, len(casos), "o corpus não muda de tamanho sem emenda ao ADR-033")

	matchers := map[string]*textmatch.Matcher{}
	for _, lado := range ladosDoDinheiro {
		matchers[lado] = matcherDoLado(t, lado)
	}
	// Os dois lados estão representados: um corpus só de despesa deixaria a
	// metade do vocabulário sem prova.
	porLado := map[string]int{}
	for _, c := range casos {
		porLado[c.lado]++
	}
	assert.GreaterOrEqual(t, porLado["income"], 40, "o lado da receita também é corpus")
	assert.GreaterOrEqual(t, porLado["expense"], 100)

	for _, caso := range casos {
		m := matchers[caso.lado]
		require.NotNilf(t, m, "lado inválido em %q", caso.desc)

		res, err := m.Best(textnorm.Normalize(caso.desc))
		require.NoError(t, err)

		switch caso.folha {
		case nenhuma:
			assert.Falsef(t, res.Matched(),
				"%q devia ficar SEM sugestão e veio %s por %q (%d pontos) — %s",
				caso.desc, res.Match.OwnerID, res.Match.Keyword, res.Match.Score, caso.nota)
		case ambigua:
			assert.Equalf(t, textmatch.ReasonAmbiguous, res.Reason,
				"%q devia empatar e veio %s — %s", caso.desc, res.Reason, caso.nota)
		default:
			if !res.Matched() {
				t.Errorf("%q não sugeriu nada (%s); esperado %s", caso.desc, res.Reason, caso.folha)
				continue
			}
			assert.Equalf(t, caso.folha, res.Match.OwnerID,
				"%q sugeriu %s por %q (%d pontos)", caso.desc, res.Match.OwnerID, res.Match.Keyword, res.Match.Score)
		}
	}
}

// corpusDaSemente é a tabela. Cresce quando alguém encontra um caso real que a
// semente erra; nunca encolhe para fazer uma palavra caber.
func corpusDaSemente() []casoDoCorpus {
	return []casoDoCorpus{
		// --- Moradia ---------------------------------------------------------
		{desc: "ALUGUEL APTO 302", lado: "expense", folha: "Moradia › Aluguel e condomínio"},
		{desc: "CONDOMINIO EDIFICIO SOLAR", lado: "expense", folha: "Moradia › Aluguel e condomínio"},
		{desc: "TAXA CONDOMINIAL MARCO", lado: "expense", folha: "Moradia › Aluguel e condomínio",
			nota: "a palavra «taxa condominial» saiu por redundância (91 contra «condomínio»); o acerto continua"},
		{desc: "QUINTOANDAR SERVICOS IMOBILIARIOS", lado: "expense", folha: "Moradia › Aluguel e condomínio"},
		{desc: "ENEL DISTRIBUICAO SAO PAULO", lado: "expense", folha: "Moradia › Água, luz e gás"},
		{desc: "CEMIG DISTRIBUICAO SA", lado: "expense", folha: "Moradia › Água, luz e gás"},
		{desc: "COPEL DIS", lado: "expense", folha: "Moradia › Água, luz e gás"},
		{desc: "SABESP", lado: "expense", folha: "Moradia › Água, luz e gás"},
		{desc: "COMPANHIA DE SANEAMENTO", lado: "expense", folha: "Moradia › Água, luz e gás"},
		{desc: "COMGAS", lado: "expense", folha: "Moradia › Água, luz e gás"},
		{desc: "ULTRAGAZ SA", lado: "expense", folha: "Moradia › Água, luz e gás",
			nota: "a marca fica, e o preço dela está no caso ULTRA COMERCIO DE PECAS: «gás» sozinha tira 0 contra esta linha, então remover «ultragaz» perderia a marca inteira"},
		{desc: "ULTRA COMERCIO DE PECAS", lado: "expense", folha: "Moradia › Água, luz e gás",
			nota: "FALSO POSITIVO ACEITO, e o PIOR da semente (achado N1, 18/09/2026): a resposta CERTA («autopeças», 87) PERDE para a errada («ultragaz», 89). Aceito porque não há conserto grátis — «ultragaz» e «ultrafarma» se chamam assim de verdade e as duas vazam o pedaço `ultra`. É por isso que `ultra` está no bloco de exceções documentadas de seed_test.go, e não na lista de tokens de rotina"},
		{desc: "LEROY MERLIN", lado: "expense", folha: "Moradia › Manutenção e reforma"},
		{desc: "MATERIAL DE CONSTRUCAO SILVA", lado: "expense", folha: "Moradia › Manutenção e reforma"},
		{desc: "PAGAMENTO ENCANADOR", lado: "expense", folha: "Moradia › Manutenção e reforma"},
		{desc: "DEDETIZACAO PREDIAL", lado: "expense", folha: "Moradia › Manutenção e reforma"},
		{desc: "CELESC DISTRIBUICAO", lado: "expense", folha: nenhuma,
			nota: "perda medida e aceita do enxugamento (ADR-033e): marca regional de energia fora da lista"},
		{desc: "EMBASA SALVADOR", lado: "expense", folha: nenhuma, nota: "idem, saneamento regional"},

		// --- Alimentação -----------------------------------------------------
		{desc: "SUPERMERCADO SAO JOSE", lado: "expense", folha: "Alimentação › Supermercado"},
		{desc: "MERCADO SAO JOSE", lado: "expense", folha: "Alimentação › Supermercado",
			nota: "R1: «mercado» solto não existe; «supermercado» pega por aproximação"},
		{desc: "MERCADINHO DA ESQUINA", lado: "expense", folha: "Alimentação › Supermercado"},
		{desc: "ATACADAO SA", lado: "expense", folha: "Alimentação › Supermercado"},
		{desc: "ASSAI ATACADISTA", lado: "expense", folha: "Alimentação › Supermercado"},
		{desc: "CARREFOUR COM IND", lado: "expense", folha: "Alimentação › Supermercado"},
		{desc: "PAO DE ACUCAR", lado: "expense", folha: "Alimentação › Supermercado"},
		{desc: "HORTIFRUTI CENTRAL", lado: "expense", folha: "Alimentação › Supermercado"},
		{desc: "ACOUGUE BOI GORDO", lado: "expense", folha: "Alimentação › Supermercado"},
		{desc: "SUPER ADEGA", lado: "expense", folha: "Alimentação › Supermercado",
			nota: "aproximação a 83 por «supermercado»: uma adega não é mercado, mas o gasto é da mesma família e a sugestão é editável"},
		{desc: "MERCADO PAGO IP LTDA", lado: "expense", folha: "Alimentação › Supermercado",
			nota: "FALSO POSITIVO ACEITO (89): «MERCADO PAGO» é adquirente, mas «supermercado» alcança o token «mercado». Conviver com ele é mais barato que tirar «supermercado» da semente"},
		{desc: "RESTAURANTE DO CHEF", lado: "expense", folha: "Alimentação › Restaurantes, lanches e padaria"},
		{desc: "PADARIA PAO QUENTE", lado: "expense", folha: "Alimentação › Restaurantes, lanches e padaria"},
		{desc: "PANIFICADORA CENTRAL", lado: "expense", folha: "Alimentação › Restaurantes, lanches e padaria"},
		{desc: "PIZZARIA BELLA NAPOLI", lado: "expense", folha: "Alimentação › Restaurantes, lanches e padaria",
			nota: "R8: «pizzaria» saiu da lista — «pizza» já a cobre a 89"},
		{desc: "Pizza Hut", lado: "expense", folha: "Alimentação › Restaurantes, lanches e padaria"},
		{desc: "MC DONALDS", lado: "expense", folha: "Alimentação › Restaurantes, lanches e padaria"},
		{desc: "BURGER KING BR", lado: "expense", folha: "Alimentação › Restaurantes, lanches e padaria"},
		{desc: "CAFETERIA GRAO FINO", lado: "expense", folha: "Alimentação › Restaurantes, lanches e padaria"},
		{desc: "ACAI DA PRAIA", lado: "expense", folha: "Alimentação › Restaurantes, lanches e padaria"},
		{desc: "Compra no débito - IFOOD *IFOOD", lado: "expense", folha: "Alimentação › Delivery"},
		{desc: "RAPPI BRASIL", lado: "expense", folha: "Alimentação › Delivery"},
		{desc: "AIQFOME", lado: "expense", folha: "Alimentação › Delivery"},
		{desc: "ZE DELIVERY", lado: "expense", folha: "Alimentação › Delivery",
			nota: "R8: a forma com espaço saiu; «zedelivery» a cobre a 94"},
		{desc: "STARBUCKS COFFEE", lado: "expense", folha: nenhuma,
			nota: "perda medida e aceita do enxugamento: marca de cauda fora da lista"},

		// --- Transporte ------------------------------------------------------
		{desc: "AUTO POSTO TREVO", lado: "expense", folha: "Transporte › Combustível e manutenção do carro"},
		{desc: "POSTO IPIRANGA", lado: "expense", folha: "Transporte › Combustível e manutenção do carro"},
		{desc: "SHELL BOX", lado: "expense", folha: "Transporte › Combustível e manutenção do carro"},
		{desc: "POSTO DE SAUDE MUNICIPAL", lado: "expense", folha: "Transporte › Combustível e manutenção do carro",
			nota: "FALSO POSITIVO ACEITO (100): «posto» casa igual. Tirar «posto» custaria todo abastecimento, que é semanal; posto de saúde raramente cobra"},
		{desc: "OFICINA DO JOAO", lado: "expense", folha: "Transporte › Combustível e manutenção do carro"},
		{desc: "OFICINA MECANICA SILVA", lado: "expense", folha: "Transporte › Combustível e manutenção do carro"},
		{desc: "CARTORIO 2 OFICIO", lado: "expense", folha: "Transporte › Combustível e manutenção do carro",
			nota: "FALSO POSITIVO ACEITO (85, «oficina»~«ofício»). MEDIDO contra as duas alternativas: a frase «oficina mecânica» não acrescenta NADA (quem casa «OFICINA MECANICA SILVA» é «mecânica», a 100) e ainda seria redundante por R8; tirar «oficina» perderia OFICINA DO JOAO, AUTO OFICINA CENTRAL e OFICINA AUTOMOTIVA, que são despesa recorrente, para poupar cartório, que é esporádico"},
		{desc: "AUTOPECAS BOM PRECO", lado: "expense", folha: "Transporte › Combustível e manutenção do carro"},
		{desc: "BORRACHARIA DO ZE", lado: "expense", folha: "Transporte › Combustível e manutenção do carro"},
		{desc: "UBER *TRIP", lado: "expense", folha: "Transporte › Aplicativo e transporte público"},
		{desc: "99APP *99", lado: "expense", folha: "Transporte › Aplicativo e transporte público"},
		{desc: "CABIFY BRASIL", lado: "expense", folha: "Transporte › Aplicativo e transporte público"},
		{desc: "INDRIVER TECNOLOGIA", lado: "expense", folha: "Transporte › Aplicativo e transporte público",
			nota: "R8: «indriver» não ocupa vaga — «indrive» a cobre a 96"},
		{desc: "BILHETE UNICO RECARGA", lado: "expense", folha: "Transporte › Aplicativo e transporte público"},
		{desc: "METRO SP", lado: "expense", folha: "Transporte › Aplicativo e transporte público"},
		{desc: "ESTAPAR ESTACIONAMENTO", lado: "expense", folha: "Transporte › Estacionamento e pedágio"},
		{desc: "SEM PARAR PEDAGIO", lado: "expense", folha: "Transporte › Estacionamento e pedágio"},
		{desc: "CONECTCAR", lado: "expense", folha: "Transporte › Estacionamento e pedágio"},
		{desc: "ZONA AZUL DIGITAL", lado: "expense", folha: "Transporte › Estacionamento e pedágio"},
		{desc: "LAVA RAPIDO DO ZE", lado: "expense", folha: nenhuma,
			nota: "perda medida e aceita: a lista tem «lava jato», não «lava rápido»"},

		// --- Saúde -----------------------------------------------------------
		{desc: "DROGARIA SAO PAULO", lado: "expense", folha: "Saúde › Farmácia"},
		{desc: "FARMACIA POPULAR", lado: "expense", folha: "Saúde › Farmácia"},
		{desc: "RAIA DROGASIL SA", lado: "expense", folha: "Saúde › Farmácia"},
		{desc: "DROGA RAIA", lado: "expense", folha: "Saúde › Farmácia",
			nota: "R8: «droga raia» saiu; «raia» (exata) e «drogaria» (89) cobrem"},
		{desc: "ULTRAFARMA", lado: "expense", folha: "Saúde › Farmácia"},
		{desc: "PAGUE MENOS FILIAL", lado: "expense", folha: "Saúde › Farmácia"},
		{desc: "UNIMED SEGUROS SAUDE", lado: "expense", folha: "Saúde › Plano de saúde"},
		{desc: "AMIL ASSISTENCIA MEDICA", lado: "expense", folha: "Saúde › Plano de saúde"},
		{desc: "HAPVIDA", lado: "expense", folha: "Saúde › Plano de saúde"},
		{desc: "SUL AMERICA SEGURO SAUDE", lado: "expense", folha: ambigua,
			nota: "EMPATE ACEITO (91×91): o token «america» alcança «sulamerica» e «americanas» com a mesma nota. As duas palavras FICAM: tirar qualquer uma piora o caso dela sem consertar este — sem «sulamerica», «LOJAS AMERICANAS» continua certo mas «SULAMERICA SEGUROS» cairia em Compras a 82, e vice-versa. Cada marca vence com a SUA grafia (os dois casos logo abaixo); só a forma com espaço empata, e empate devolve NADA"},
		{desc: "SULAMERICA SEGUROS", lado: "expense", folha: "Saúde › Plano de saúde"},
		{desc: "LOJAS AMERICANAS", lado: "expense", folha: "Compras › Compras online"},
		{desc: "PLANO DE SAUDE MENSALIDADE", lado: "expense", folha: "Saúde › Plano de saúde"},
		{desc: "HOSPITAL SANTA CASA", lado: "expense", folha: "Saúde › Médico, dentista e exames"},
		{desc: "LABORATORIO FLEURY", lado: "expense", folha: "Saúde › Médico, dentista e exames"},
		{desc: "EXAMES LABORATORIAIS", lado: "expense", folha: "Saúde › Médico, dentista e exames"},
		{desc: "DENTISTA DRA ANA", lado: "expense", folha: "Saúde › Médico, dentista e exames"},
		{desc: "ODONTOCOMPANY", lado: "expense", folha: "Saúde › Médico, dentista e exames"},
		{desc: "CONSULTA MEDICA", lado: "expense", folha: "Saúde › Médico, dentista e exames",
			nota: "«consulta» e «consultório» saíram (92 e 94 contra «consultoria», modificador de razão social); «médico» ainda alcança «MEDICA» a 83"},
		{desc: "FISIOTERAPIA ORTOPEDICA", lado: "expense", folha: "Saúde › Médico, dentista e exames",
			nota: "R8: «fisioterapia» saiu; «terapia» a cobre a 88"},
		{desc: "ACADEMIA CORPO E MENTE", lado: "expense", folha: "Saúde › Academia e esportes"},
		{desc: "SMART FIT SAO PAULO", lado: "expense", folha: "Saúde › Academia e esportes",
			nota: "a palavra é a FRASE «smart fit» desde o achado N1 (reconferência de 18/09/2026): a forma colada «smartfit» vazava o pedaço `smart` a 89. A frase casa inteira a 100 e vence «smartphone» (85) sem empate"},
		{desc: "SMARTFIT ACADEMIA", lado: "expense", folha: "Saúde › Academia e esportes",
			nota: "custo zero da troca, medido: quem decide aqui é «academia», a 100 — a frase não casa a grafia colada e não precisa"},
		{desc: "SMARTFIT", lado: "expense", folha: nenhuma,
			nota: "o ÚNICO custo da troca, medido: a grafia colada SOZINHA deixa de casar («smartphone» chega a 70, abaixo do limiar)"},
		{desc: "SMART TECH SOLUCOES", lado: "expense", folha: "Compras › Eletrônicos e informática",
			nota: "FALSO POSITIVO ACEITO (85), mesma família de `ultra` e `colar` — modificador comum colado a palavra real, sem grafia de escape («smartphones» ainda entrega `smart`, a 84). O preço de remover «smartphone» foi medido e NÃO é zero: cairia a família genérica inteira (`COMPRA SMARTPHONE` 100, `LOJA DE SMARTPHONES` 97, `IPLACE SMARTPHONE` 100 — nomes de loja reais), porque a palavra mais próxima da folha, «iphone», só chega a 77–78 nessas linhas. As MARCAS não dependem dela. A favor: depois da troca de «smartfit» por frase, o resíduo ficou estritamente menos errado — caiu de 89 para 85 e o destino saiu de Saúde › Academia (absurdo) para Compras › Eletrônicos, adjacente ao que uma SMART TECH de fato faz"},
		{desc: "GYMPASS BRASIL", lado: "expense", folha: "Saúde › Academia e esportes"},
		{desc: "CROSSFIT VILA", lado: "expense", folha: "Saúde › Academia e esportes"},
		{desc: "HOSPITAL VETERINARIO", lado: "expense", folha: ambigua,
			nota: "EMPATE ACEITO: «hospital» (Saúde) e «veterinário» (Pets) tiram 100 cada. O motor se cala, e é o desfecho certo — a linha é de um dos dois e o servidor não tem como saber qual"},
		{desc: "HERMES PARDINI", lado: "expense", folha: nenhuma, nota: "perda aceita: laboratório regional fora da lista"},

		// --- Educação --------------------------------------------------------
		{desc: "ESCOLA MUNICIPAL", lado: "expense", folha: "Educação › Escola e faculdade"},
		{desc: "COLEGIO OBJETIVO", lado: "expense", folha: "Educação › Escola e faculdade"},
		{desc: "FACULDADE ANHANGUERA", lado: "expense", folha: "Educação › Escola e faculdade"},
		{desc: "CRECHE ARCO IRIS", lado: "expense", folha: "Educação › Escola e faculdade"},
		{desc: "UNIVERSIDADE ESTACIO", lado: "expense", folha: "Educação › Escola e faculdade"},
		{desc: "ESCOLA DE NATACAO", lado: "expense", folha: "Educação › Escola e faculdade",
			nota: "era EMPATE (100×100) até «natação» sair da semente na revisão de 18/09/2026 (ela alcançava os tokens de rotina «cotação» e «atacado», os dois a 80). Sem ela, «escola» decide sozinha — e para uma ESCOLA de natação esse é o desfecho mais defensável dos dois"},
		{desc: "UDEMY COURSES", lado: "expense", folha: "Educação › Cursos e material"},
		{desc: "ALURA CURSOS ONLINE", lado: "expense", folha: "Educação › Cursos e material"},
		{desc: "DUOLINGO", lado: "expense", folha: "Educação › Cursos e material"},
		{desc: "PAPELARIA KALUNGA", lado: "expense", folha: "Educação › Cursos e material"},
		{desc: "LIVRARIA CULTURA", lado: "expense", folha: "Educação › Cursos e material"},

		// --- Lazer -----------------------------------------------------------
		{desc: "NETFLIX.COM", lado: "expense", folha: "Lazer › Streaming, jogos e assinaturas"},
		{desc: "SPOTIFY AB", lado: "expense", folha: "Lazer › Streaming, jogos e assinaturas"},
		{desc: "DISNEY PLUS", lado: "expense", folha: "Lazer › Streaming, jogos e assinaturas"},
		{desc: "HBO MAX", lado: "expense", folha: "Lazer › Streaming, jogos e assinaturas"},
		{desc: "GLOBOPLAY", lado: "expense", folha: "Lazer › Streaming, jogos e assinaturas"},
		{desc: "STEAM GAMES", lado: "expense", folha: "Lazer › Streaming, jogos e assinaturas"},
		{desc: "PLAYSTATION NETWORK", lado: "expense", folha: "Lazer › Streaming, jogos e assinaturas"},
		{desc: "CINEMARK BRASIL", lado: "expense", folha: "Lazer › Cinema, shows e eventos",
			nota: "R8: «cinemark» saiu; «cinema» a cobre a 93"},
		{desc: "INGRESSO.COM", lado: "expense", folha: "Lazer › Cinema, shows e eventos"},
		{desc: "SYMPLA INGRESSOS", lado: "expense", folha: "Lazer › Cinema, shows e eventos"},
		{desc: "TEATRO MUNICIPAL", lado: "expense", folha: "Lazer › Cinema, shows e eventos"},
		{desc: "AIRBNB PAYMENTS", lado: "expense", folha: "Lazer › Viagens"},
		{desc: "BOOKING.COM", lado: "expense", folha: "Lazer › Viagens"},
		{desc: "DECOLAR.COM", lado: "expense", folha: "Lazer › Viagens",
			nota: "«viagem» e «passagem» tiram 0 contra esta linha: remover «decolar» perderia a marca, e é por isso que `colar` virou exceção em vez de entrar na lista"},
		{desc: "JOALHERIA COLAR DE PRATA", lado: "expense", folha: "Lazer › Viagens",
			nota: "FALSO POSITIVO ACEITO (91, achado N1): «decolar» vaza o sufixo `colar`. Mesma família de `ultra` e mesmo motivo para aceitar"},
		{desc: "MATERIAL ESCOLAR", lado: "expense", folha: "Educação › Escola e faculdade",
			nota: "MARGEM REGISTRADA (achado N1): `escolar` também alcança «decolar», a 80. Aqui é inócuo porque «escola» co-dispara a 96 e vence com 16 pontos de folga — mas a folga depende de «escola» continuar na semente, e é por isso que este caso existe"},
		{desc: "LATAM AIRLINES", lado: "expense", folha: "Lazer › Viagens"},
		{desc: "POUSADA DO SOL", lado: "expense", folha: "Lazer › Viagens"},
		{desc: "CRUNCHYROLL", lado: "expense", folha: nenhuma, nota: "perda aceita: streaming de cauda"},
		{desc: "NINTENDO ESHOP", lado: "expense", folha: nenhuma, nota: "perda aceita"},

		// --- Compras ---------------------------------------------------------
		{desc: "MERCADO LIVRE", lado: "expense", folha: "Compras › Compras online",
			nota: "R1 em ação: vence «supermercado» (88) com 100 porque a frase inteira casa"},
		{desc: "MERCADOLIVRE*COMPRA", lado: "expense", folha: "Compras › Compras online"},
		{desc: "AMAZON BR", lado: "expense", folha: "Compras › Compras online"},
		{desc: "AMAZON PRIME", lado: "expense", folha: "Compras › Compras online",
			nota: "FALSO POSITIVO ACEITO (100): a assinatura Prime cai em Compras, não em Lazer, porque «amazon» casa a frase inteira"},
		{desc: "AMAZONPRIMEBR", lado: "expense", folha: "Compras › Compras online",
			nota: "o que se temia perder ao remover «amazonprime» no achado N1, e que NÃO se perdeu: «amazon» alcança a grafia colada a 84. Custo da remoção: zero"},
		{desc: "PRIME CONSULTORIA", lado: "expense", folha: nenhuma,
			nota: "regressão do N1: «amazonprime» vazava o prefixo `prime` a 84 e esta linha virava Lazer › Streaming. Com a palavra fora, `prime` pôde entrar na lista de tokens de rotina — o único dos quatro modificadores de marca que coube"},
		{desc: "SHOPEE BR", lado: "expense", folha: "Compras › Compras online"},
		{desc: "MAGAZINE LUIZA SA", lado: "expense", folha: "Compras › Compras online"},
		{desc: "MAGAZINELUIZA", lado: "expense", folha: "Compras › Compras online",
			nota: "a forma colada saiu da lista (82 contra o PRIMEIRO NOME «luiza», 88 contra «magazine»); a frase «magazine luiza» cobre as duas grafias e exige os dois tokens"},
		{desc: "CASAS BAHIA", lado: "expense", folha: "Compras › Compras online"},
		{desc: "KABUM COMERCIO", lado: "expense", folha: "Compras › Eletrônicos e informática"},
		{desc: "NOTEBOOK DELL", lado: "expense", folha: "Compras › Eletrônicos e informática"},
		{desc: "IPHONE 15 PRO", lado: "expense", folha: "Compras › Eletrônicos e informática"},
		{desc: "APPLE.COM/BILL", lado: "expense", folha: "Lazer › Streaming, jogos e assinaturas",
			nota: "a cobrança recorrente da Apple é assinatura, e é o caso comum de «apple»"},
		{desc: "IPHONE APPLE STORE", lado: "expense", folha: ambigua,
			nota: "EMPATE ACEITO (100×100): «iphone» (Eletrônicos) e «apple» (assinaturas) casam exato na mesma linha. As duas ficam porque cada uma acerta sozinha (os dois casos acima); juntas, o motor se cala — que é o desfecho certo, porque a linha pode ser as duas coisas"},
		{desc: "ASSISTENCIA TECNICA CELULAR", lado: "expense", folha: "Compras › Eletrônicos e informática"},
		{desc: "TOK STOK", lado: "expense", folha: "Compras › Casa e decoração"},
		{desc: "MADEIRAMADEIRA COM", lado: "expense", folha: "Compras › Casa e decoração"},
		{desc: "MADEIRA MADEIRA", lado: "expense", folha: "Compras › Casa e decoração",
			nota: "R8: a forma com espaço saiu; «madeiramadeira» cobre as duas a 85"},
		{desc: "HAVAN LOJAS", lado: "expense", folha: "Compras › Casa e decoração"},
		{desc: "ALIEXPRESS", lado: "expense", folha: nenhuma,
			nota: "R3: fora da lista de propósito — «aliexpress» pontua 91 contra «express», que aparece em razão social"},

		// --- Serviços --------------------------------------------------------
		{desc: "CLARO MOVEL", lado: "expense", folha: "Serviços › Telefone, internet e TV"},
		{desc: "Pix automático enviado para CLARO", lado: "expense", folha: "Serviços › Telefone, internet e TV"},
		{desc: "VIVO FIBRA", lado: "expense", folha: "Serviços › Telefone, internet e TV"},
		{desc: "TIM CELULAR", lado: "expense", folha: "Serviços › Telefone, internet e TV"},
		{desc: "SKY TV", lado: "expense", folha: "Serviços › Telefone, internet e TV"},
		{desc: "TELEFONICA BRASIL", lado: "expense", folha: "Serviços › Telefone, internet e TV",
			nota: "R8: «telefônica» saiu; «telefonia» a cobre a 90"},
		{desc: "STARLINK INTERNET", lado: "expense", folha: "Serviços › Telefone, internet e TV"},
		{desc: "TARIFA MENSALIDADE PACOTE", lado: "expense", folha: "Serviços › Tarifas bancárias e juros"},
		{desc: "Estorno Tarifa", lado: "expense", folha: "Serviços › Tarifas bancárias e juros"},
		{desc: "Anuidade Diferenciada", lado: "expense", folha: "Serviços › Tarifas bancárias e juros"},
		{desc: "IOF SOBRE COMPRA INTERNACIONAL", lado: "expense", folha: "Serviços › Tarifas bancárias e juros"},
		{desc: "CESTA DE SERVICOS", lado: "expense", folha: "Serviços › Tarifas bancárias e juros"},
		{desc: "PORTO SEGURO CIA", lado: "expense", folha: "Serviços › Seguros"},
		{desc: "SEGURO DE VIDA", lado: "expense", folha: "Serviços › Seguros"},
		{desc: "MAPFRE SEGUROS", lado: "expense", folha: "Serviços › Seguros"},
		{desc: "SEGURADORA LIBERTY", lado: "expense", folha: "Serviços › Seguros"},
		{desc: "PAGSEGURO INTERNET", lado: "expense", folha: nenhuma,
			nota: "R3/R4: «seguro» e «seguros» soltos ficaram fora justamente porque tiram 90 contra «pagseguro», que é adquirente e aparece em milhares de linhas"},
		{desc: "CONTADOR ESCRITORIO", lado: "expense", folha: nenhuma,
			nota: "R3: «contador» ficou fora — pontua 89 contra «conta», presente em TODA linha de Pix do Nubank"},

		// --- Pessoal ---------------------------------------------------------
		{desc: "RENNER FILIAL 123", lado: "expense", folha: "Pessoal › Roupas e calçados"},
		{desc: "RIACHUELO SA", lado: "expense", folha: "Pessoal › Roupas e calçados"},
		{desc: "LOJA ROUPA EXEMPLO", lado: "expense", folha: "Pessoal › Roupas e calçados"},
		{desc: "CENTAURO ESPORTES", lado: "expense", folha: "Pessoal › Roupas e calçados"},
		{desc: "SHEIN BR", lado: "expense", folha: "Pessoal › Roupas e calçados"},
		{desc: "SALAO DE BELEZA", lado: "expense", folha: "Pessoal › Cabelo e beleza"},
		{desc: "BARBEARIA EXEMPLO", lado: "expense", folha: "Pessoal › Cabelo e beleza"},
		{desc: "O BOTICARIO", lado: "expense", folha: nenhuma,
			nota: "PERDA CONHECIDA, paga na revisão de 18/09/2026: «boticário» saiu porque CONTÉM «ótica» como substring e tirava 87 contra o token `otica` — «OTICA DINIZ» virava Cabelo e beleza. Não há forma de escrever a marca que escape disso (a frase «o boticário» tokeniza igual, porque «o» é palavra vazia), então a palavra cedeu. A folha fica com 11 palavras e 9 vagas livres para a pessoa recadastrar"},
		{desc: "OTICA DINIZ", lado: "expense", folha: nenhuma,
			nota: "o outro lado da mesma decisão: nenhuma folha da semente é dona de ótica, então a resposta certa é não sugerir"},
		{desc: "MANICURE E PEDICURE", lado: "expense", folha: "Pessoal › Cabelo e beleza"},
		{desc: "PETZ COMERCIO", lado: "expense", folha: "Pessoal › Pets"},
		{desc: "COBASI PET", lado: "expense", folha: "Pessoal › Pets"},
		{desc: "PET SHOP AMIGO FIEL", lado: "expense", folha: "Pessoal › Pets",
			nota: "R8: «pet shop» saiu; «pet» (exata) casa o token"},
		{desc: "RACAO PARA CAES", lado: "expense", folha: "Compras › Casa e decoração",
			nota: `FALSO POSITIVO ACEITO (87, por «decoração»). É o pior caso da família de sufixo «-ação», onde a regra 2 do motor (substring comum de 5 runas) é mais frágil: «ração», «infração», «decoração» e «operação» compartilham «racao». «ração» SAIU de Pets porque tira 89 contra «operação», que é boilerplate de extrato; «infração» SAIU de Impostos porque tira 89 contra «RACAO» e a folha já tem «detran», «multa de trânsito» e «ipva» (o preço está no caso INFRACAO DE TRANSITO, abaixo). «decoração» FICA: é a palavra central da folha dela e a única do trio com alcance próprio grande`},
		{desc: "INFRACAO DE TRANSITO", lado: "expense", folha: nenhuma,
			nota: "perda medida e aceita: «infração» saiu pelo caso acima, e «multa de trânsito» exige o token «multa»"},

		// --- Impostos --------------------------------------------------------
		{desc: "IPTU 2026 PARCELA 3", lado: "expense", folha: "Impostos › IPTU, IPVA e taxas"},
		{desc: "IPVA DETRAN SP", lado: "expense", folha: "Impostos › IPTU, IPVA e taxas"},
		{desc: "PREFEITURA MUNICIPAL", lado: "expense", folha: "Impostos › IPTU, IPVA e taxas"},
		{desc: "LICENCIAMENTO ANUAL", lado: "expense", folha: "Impostos › IPTU, IPVA e taxas"},
		{desc: "DARF NUMERADO", lado: "expense", folha: "Impostos › Imposto de renda e DARF"},
		{desc: "IMPOSTO DE RENDA PESSOA FISICA", lado: "expense", folha: "Impostos › Imposto de renda e DARF",
			nota: "R8: «imposto de renda» saiu; «imposto» casa o token a 100"},
		{desc: "Pix enviado ;Receita Federal", lado: "expense", folha: "Impostos › Imposto de renda e DARF"},
		{desc: "SIMPLES NACIONAL DAS", lado: "expense", folha: "Impostos › Imposto de renda e DARF"},

		// --- Investimentos (lado da despesa) ---------------------------------
		{desc: "APLICACAO DE CDB", lado: "expense", folha: "Investimentos › Renda fixa e Tesouro Direto"},
		{desc: "Aplicação RDB", lado: "expense", folha: "Investimentos › Renda fixa e Tesouro Direto"},
		{desc: "CDB C6 LIM.GARANT.", lado: "expense", folha: "Investimentos › Renda fixa e Tesouro Direto"},
		{desc: "COMPRA TESOURO SELIC", lado: "expense", folha: "Investimentos › Renda fixa e Tesouro Direto"},
		{desc: "LCI BANCO EXEMPLO", lado: "expense", folha: "Investimentos › Renda fixa e Tesouro Direto"},
		{desc: "APLICACAO CAIXINHA", lado: "expense", folha: "Investimentos › Renda fixa e Tesouro Direto"},
		{desc: "COMPRA DE ETF IVVB11", lado: "expense", folha: "Investimentos › Ações, fundos e previdência"},
		{desc: "FUNDO MULTIMERCADO XPTO", lado: "expense", folha: "Investimentos › Ações, fundos e previdência"},
		{desc: "PREVIDENCIA PRIVADA VGBL", lado: "expense", folha: "Investimentos › Ações, fundos e previdência"},
		{desc: "HOME BROKER B3", lado: "expense", folha: "Investimentos › Ações, fundos e previdência"},
		{desc: "XP INVESTIMENTOS CCTVM", lado: "expense", folha: "Investimentos › Corretoras e criptomoedas",
			nota: "R8: «xp investimentos» saiu; «xp» (exata) casa o token"},
		{desc: "BTG PACTUAL", lado: "expense", folha: "Investimentos › Corretoras e criptomoedas"},
		{desc: "MERCADO BITCOIN", lado: "expense", folha: "Investimentos › Corretoras e criptomoedas",
			nota: "R8: «mercado bitcoin» saiu; «bitcoin» casa a 100 e ainda vence «supermercado» (88)"},
		{desc: "COMPRA CRIPTO", lado: "expense", folha: "Investimentos › Corretoras e criptomoedas"},
		{desc: "NUINVEST CORRETORA", lado: "expense", folha: nenhuma,
			nota: "«nuinvest» SAIU: pontua 80 contra «ourinvest» e «fininvest», marcas de banco, e o token «nu» é denylist de R4"},
		{desc: "BINANCE", lado: "expense", folha: nenhuma,
			nota: "R3: fora de propósito — «binance» tira 82 contra «financeira»"},
		{desc: "ATACAREJO BOM PRECO", lado: "expense", folha: nenhuma,
			nota: "«atacadão» (72) e «natação» (75) compartilham a substring «ataca», mas NENHUMA das duas alcança o limiar. É o caso que mostra por que o limiar de 80 existe: as duas erram, e as duas se calam"},

		// --- regressões do achado A1 (revisão de segurança de 18/09/2026) -----
		//
		// A razão social SOBREVIVE inteira no Inter (Histórico;Descrição) e no
		// C6 (Título): não há documento na linha, então `sanitize.Description`
		// não tem onde cortar. Era ali que estavam TODOS os falsos positivos —
		// e o pior deles era mensal.
		{desc: "Pagamento efetuado - ADMINISTRADORA DE IMOVEIS LTDA", lado: "expense", folha: nenhuma,
			nota: "O ACHADO ALTO. Antes: Compras › Casa e decoração por «móveis», a 96 — o aluguel pago à administradora entrava em decoração TODO MÊS, com a sugestão aplicada por padrão no confirm da importação. «móveis» virou a frase «móveis planejados»"},
		{desc: "AUTOMOVEIS EXEMPLO LTDA", lado: "expense", folha: nenhuma,
			nota: "mesma família: «móveis» tirava 88 contra `automoveis`"},
		{desc: "MOVEIS PLANEJADOS SP", lado: "expense", folha: "Compras › Casa e decoração",
			nota: "o que a frase preserva: o segmento de móveis planejados, que é compra grande e recorrente"},
		{desc: "LOJA EXEMPLO MOVEIS-", lado: "expense", folha: nenhuma,
			nota: "o que a frase custa, medido: esta linha (do testdata real do C6) casava a 100 por «móveis» e agora não casa. A folha mantém «decoração», que pega «LOJA DE MOVEIS E DECORACAO»"},
		{desc: "CONTRIBUICAO SINDICAL", lado: "expense", folha: nenhuma,
			nota: "antes: Moradia › Aluguel e condomínio por «síndico», a 87 — e contribuição sindical é desconto MENSAL em folha. «síndico» saiu; o grupo já tinha «condomínio», que pega «TAXA CONDOMINIAL» a 91"},
		{desc: "SINDICATO DOS TRABALHADORES", lado: "expense", folha: nenhuma,
			nota: "mesma família: 84 contra «síndico»"},
		{desc: "PAGAMENTO SINDICO", lado: "expense", folha: nenhuma,
			nota: "o preço da retirada, medido: «condomínio» tira 0 contra o token `sindico`"},
		{desc: "COTACAO DE CAMBIO", lado: "expense", folha: nenhuma,
			nota: "antes: Saúde › Academia e esportes por «natação», a 80"},
		{desc: "ATACADO DE ALIMENTOS", lado: "expense", folha: "Alimentação › Supermercado",
			nota: "com «natação» fora, «atacadão» decide sozinha a 87 — antes eram duas donas acima do limiar (87 e 80), a um ponto de virar empate mudo que apagaria a sugestão de toda compra em atacadista. `atacado` continua FORA da lista de tokens de rotina de propósito: ele identifica o que foi comprado"},
		{desc: "NATACAO INFANTIL", lado: "expense", folha: nenhuma,
			nota: "a perda de tirar «natação», escrita: aula de natação não tem mais sugestão de fábrica"},

		// --- o que o PRÓPRIO sanitizador fabrica (achado A5) ------------------
		//
		// `sanitize.Description` reescreve seis prefixos verbosos para rótulos
		// curtos. Estes três chegam ao classificador com palavras que a semente
		// tem, e o desfecho precisa estar fixado — não deduzido.
		{desc: "Pix reembolso recebido - LOJA EXEMPLO", lado: "income", folha: "Outras receitas › Reembolsos e estornos",
			nota: "o rótulo que o sanitizador fabrica a partir de «Reembolso recebido pelo Pix»; «reembolso» casa a 100, e é o desfecho certo"},
		{desc: "Pix recebido estornado - LOJA EXEMPLO", lado: "income", folha: "Outras receitas › Reembolsos e estornos",
			nota: "«estorno»~«estornado» a 84; um recebimento estornado é de fato um estorno"},
		{desc: "Pix enviado estornado - LOJA EXEMPLO", lado: "expense", folha: nenhuma,
			nota: "do lado da DESPESA o mesmo rótulo não casa nada — «estorno» é palavra do lado da receita (ADR-029g), e é por isso que o lado do dinheiro escolhe o matcher"},

		// --- boilerplate e gente: NENHUMA sugestão (lado da despesa) ----------
		{desc: "Transferência enviada pelo Pix - JOAO DA SILVA - 123.456.789-00 - BANCO INTER S.A. (0077) Agência: 1 Conta: 123-4",
			lado: "expense", folha: nenhuma, nota: "a ameaça central: nada da semente pode casar com a linha de Pix"},
		{desc: "Transferência enviada pelo Pix - CICRANO EXEMPLO DOS SANTOS - •••.555.666-•• - BCO C6 S.A. (0336) Agência: 1 Conta: 2000003-3",
			lado: "expense", folha: nenhuma},
		{desc: "Pix enviado para Beltrano Exemplo", lado: "expense", folha: nenhuma},
		{desc: "TRANSF ENVIADA PIX", lado: "expense", folha: nenhuma},
		{desc: "Pagamento de fatura", lado: "expense", folha: nenhuma},
		{desc: "PGTO FAT CARTAO C6", lado: "expense", folha: nenhuma},
		{desc: "Pagamento de boleto efetuado", lado: "expense", folha: nenhuma},
		{desc: "Compra no débito", lado: "expense", folha: nenhuma},
		{desc: "PIX QRS MARIA DE LOURDES", lado: "expense", folha: nenhuma},
		{desc: "PIX TRANSF PEDRO OLIVEIRA", lado: "expense", folha: nenhuma},
		{desc: "TED PARA MARCOS PACHECO", lado: "expense", folha: nenhuma},
		{desc: "SAQUE BANCO24HORAS", lado: "expense", folha: nenhuma},
		{desc: "PICPAY *PAGAMENTO", lado: "expense", folha: nenhuma},
		{desc: "MERCADO PAGO *ASSINATURA", lado: "expense", folha: "Alimentação › Supermercado",
			nota: "o mesmo falso positivo aceito de «MERCADO PAGO IP LTDA»"},
		{desc: "ADMINISTRADORA EXEMPLO S/A", lado: "expense", folha: nenhuma},
		{desc: "ESCRITORIO EXEMPLO LTDA", lado: "expense", folha: nenhuma},
		{desc: "TOTTA EXEMPLO LTDA", lado: "expense", folha: nenhuma},
		{desc: "Inclusao de Pagamento", lado: "expense", folha: nenhuma},
		{desc: "Ajuste a crédito", lado: "expense", folha: nenhuma},
		{desc: "ZUMBRA Y COMERCIO", lado: "expense", folha: nenhuma,
			nota: "nome inventado: é a descrição que as fixtures de teste usam quando precisam ficar SEM categoria"},

		// --- Salário (lado da receita) ---------------------------------------
		{desc: "SALARIO MENSAL", lado: "income", folha: "Salário › Salário, 13º e férias"},
		{desc: "CREDITO FOLHA DE PAGAMENTO", lado: "income", folha: "Salário › Salário, 13º e férias",
			nota: "R8: «folha de pagamento» saiu; «folha» casa o token a 100"},
		{desc: "HOLERITE COMPETENCIA 09", lado: "income", folha: "Salário › Salário, 13º e férias"},
		{desc: "CONTRACHEQUE", lado: "income", folha: nenhuma,
			nota: "PERDA CONHECIDA (18/09/2026): «contracheque» saiu porque tirava 85 contra o token `cheque`, e `cheque` é mecanismo de transação puro («CHEQUE COMPENSADO», «CHEQUE DEVOLVIDO»). O estrago que ela causava era do lado da RECEITA, que é o pior: inflar entrada é mais perigoso que inflar saída. A folha mantém «salário», «folha» e «holerite», que são as descrições de fato usadas pelos bancos"},
		{desc: "CHEQUE COMPENSADO", lado: "income", folha: nenhuma,
			nota: "regressão do achado A1: antes ia para Salário › Salário, 13º e férias a 85"},
		{desc: "ADIANT SALARIO", lado: "income", folha: "Salário › Salário, 13º e férias",
			nota: "R8: «adiant» saiu; «adiantamento» a cobre a 85"},
		{desc: "DECIMO TERCEIRO SALARIO", lado: "income", folha: "Salário › Salário, 13º e férias",
			nota: "GANHO do enxugamento: com a lista grande esta linha empatava e não sugeria nada"},
		{desc: "PAGAMENTO DE FERIAS", lado: "income", folha: "Salário › Salário, 13º e férias"},
		{desc: "PLR PARTICIPACAO LUCROS", lado: "income", folha: "Salário › Bônus, comissões e pró-labore"},
		{desc: "COMISSAO DE VENDAS", lado: "income", folha: "Salário › Bônus, comissões e pró-labore",
			nota: "a palavra é a FRASE «comissão de vendas», não «comissão»: a família «-missão» colide com «emissão»/«emissões», que são vocabulário de extrato («EMISSAO DE BOLETO», «EMISSAO DE CDB» — esta última está no testdata do C6). Medido: «comissão» 87 contra `emissao`, «comissões» 88 contra `emissoes`, a frase 0 contra os dois. A frase não exige adjacência (o motor cai para o mínimo por token), então «COMISSAO SOBRE VENDAS» e «CRED COMISSAO VENDAS» também casam a 100"},
		{desc: "COMISSAO MENSAL", lado: "income", folha: nenhuma,
			nota: "o preço da frase, medido: «COMISSAO» sem o token «vendas» não casa mais"},
		{desc: "EMISSAO DE BOLETO", lado: "income", folha: nenhuma,
			nota: "regressão do achado A1: antes ia para Salário › Bônus, comissões e pró-labore a 87"},
		{desc: "PRO LABORE SOCIO", lado: "income", folha: "Salário › Bônus, comissões e pró-labore",
			nota: "R8: a forma com espaço saiu; «prolabore» a cobre a 90"},
		{desc: "BONUS ANUAL", lado: "income", folha: "Salário › Bônus, comissões e pró-labore"},
		{desc: "RESCISAO CONTRATUAL", lado: "income", folha: "Salário › Rescisão e FGTS"},
		{desc: "FGTS SAQUE ANIVERSARIO", lado: "income", folha: "Salário › Rescisão e FGTS"},
		{desc: "SEGURO DESEMPREGO PARCELA 2", lado: "income", folha: "Salário › Rescisão e FGTS"},
		{desc: "VERBAS RESCISORIAS", lado: "income", folha: "Salário › Rescisão e FGTS"},
		{desc: "FERIAS", lado: "income", folha: nenhuma,
			nota: "R3: «férias» solto ficou fora — tira 83 contra o sobrenome «Farias»"},

		// --- Outras receitas --------------------------------------------------
		{desc: "REEMBOLSO CONVENIO", lado: "income", folha: "Outras receitas › Reembolsos e estornos"},
		{desc: `Estorno de "IFOOD"`, lado: "income", folha: "Outras receitas › Reembolsos e estornos"},
		{desc: "DEVOLUCAO DE COMPRA", lado: "income", folha: "Outras receitas › Reembolsos e estornos"},
		{desc: "RESSARCIMENTO DESPESA", lado: "income", folha: "Outras receitas › Reembolsos e estornos"},
		{desc: "Reembolso recebido pelo Pix - LOJA EXEMPLO", lado: "income", folha: "Outras receitas › Reembolsos e estornos"},
		{desc: "CASHBACK COMPRA", lado: "income", folha: "Outras receitas › Rendimentos e cashback"},
		{desc: "DIVIDENDOS RECEBIDOS", lado: "income", folha: "Outras receitas › Rendimentos e cashback"},
		{desc: "JCP CREDITADO", lado: "income", folha: "Outras receitas › Rendimentos e cashback"},
		{desc: "PROVENTOS B3", lado: "income", folha: "Outras receitas › Rendimentos e cashback"},
		{desc: "RENDIMENTO PAGO", lado: "income", folha: nenhuma,
			nota: "«rendimento» SAIU: tira 84 contra «empreendimentos», modificador de razão social frequente em recebimento de aluguel. Perda medida e conhecida — a pessoa tem 4 vagas livres nesta folha"},
		{desc: "ALUGUEL RECEBIDO INQUILINO", lado: "income", folha: "Outras receitas › Aluguel, benefícios e renda extra"},
		{desc: "INSS BENEFICIO", lado: "income", folha: "Outras receitas › Aluguel, benefícios e renda extra"},
		{desc: "APOSENTADORIA INSS", lado: "income", folha: "Outras receitas › Aluguel, benefícios e renda extra"},
		{desc: "BOLSA FAMILIA", lado: "income", folha: "Outras receitas › Aluguel, benefícios e renda extra"},
		{desc: "FREELANCE PROJETO", lado: "income", folha: "Outras receitas › Aluguel, benefícios e renda extra"},
		{desc: "OLX PAGAMENTOS", lado: "income", folha: "Outras receitas › Aluguel, benefícios e renda extra"},
		{desc: "HONORARIOS PRESTADOS", lado: "income", folha: "Outras receitas › Aluguel, benefícios e renda extra"},
		{desc: "BENEFICIARIO FULANO DE TAL", lado: "income", folha: nenhuma,
			nota: "«benefício» SAIU: tira 86 contra «beneficiário», que é boilerplate de comprovante de Pix/TED"},

		// --- Resgates (lado da receita) --------------------------------------
		{desc: "Resgate RDB", lado: "income", folha: "Resgates › Renda fixa e Tesouro Direto"},
		{desc: "RESGATE CDB AUTOMATICO", lado: "income", folha: "Resgates › Renda fixa e Tesouro Direto"},
		{desc: "RESGATE POUPANCA", lado: "income", folha: "Resgates › Renda fixa e Tesouro Direto"},
		{desc: "VENCIMENTO LCI", lado: "income", folha: "Resgates › Renda fixa e Tesouro Direto"},
		{desc: "VENDA TESOURO SELIC", lado: "income", folha: "Resgates › Renda fixa e Tesouro Direto"},
		{desc: "RESGATE CAIXINHA", lado: "income", folha: "Resgates › Renda fixa e Tesouro Direto"},
		{desc: "VENDA DE ACOES", lado: "income", folha: "Resgates › Ações, fundos e previdência"},
		{desc: "RESGATE FUNDO MULTIMERCADO", lado: "income", folha: "Resgates › Ações, fundos e previdência"},
		{desc: "RESGATE PREVIDENCIA PRIVADA", lado: "income", folha: "Resgates › Ações, fundos e previdência",
			nota: "R8: «resgate previdência privada» saiu; «resgate previdência» casa a frase a 100"},
		{desc: "RESGATE VGBL", lado: "income", folha: "Resgates › Ações, fundos e previdência"},
		{desc: "VENDA ETF", lado: "income", folha: "Resgates › Ações, fundos e previdência"},
		{desc: "VENDA BITCOIN", lado: "income", folha: "Resgates › Criptomoedas"},
		{desc: "RESGATE CRIPTO", lado: "income", folha: "Resgates › Criptomoedas"},
		{desc: "RESGATE", lado: "income", folha: nenhuma,
			nota: "R2: «resgate» solto NÃO existe — ele empataria com toda folha de Resgates. O preço é este: «Resgate» sozinho não sugere nada"},
		{desc: "APLICACAO", lado: "income", folha: nenhuma, nota: "R2, o espelho do caso acima"},
		{desc: "FOXBIT EXCHANGE", lado: "income", folha: nenhuma, nota: "perda aceita: corretora de cripto de cauda"},

		// --- boilerplate e gente: NENHUMA sugestão (lado da receita) ----------
		{desc: "Pix recebido de Fulano de Tal Silva", lado: "income", folha: nenhuma},
		{desc: "Transferência recebida pelo Pix - BELTRANA DE SOUZA - •••.333.444-•• - BCO C6 S.A. (0336) Agência: 1 Conta: 2000002-2",
			lado: "income", folha: nenhuma},
		{desc: "Pix recebido de EMPRESA EXEMPLO LTDA", lado: "income", folha: nenhuma},
		{desc: "Pagamento recebido", lado: "income", folha: nenhuma},
		{desc: "TED RECEBIDA DE ANA PAULA", lado: "income", folha: nenhuma},
		{desc: "DEPOSITO EM DINHEIRO", lado: "income", folha: nenhuma},
		{desc: "CREDITO EM CONTA CORRENTE", lado: "income", folha: nenhuma},
		{desc: "ZUMBRA Y SERVICOS", lado: "income", folha: nenhuma},
	}
}
