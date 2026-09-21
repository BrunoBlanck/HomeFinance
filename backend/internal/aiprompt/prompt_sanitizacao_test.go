package aiprompt_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A METADE INVISÍVEL da sanitização (achado B do QA e achado #3 da revisão de
// segurança, 21/09/2026).
//
// `TestDescricoesHostisNaoEmbaralhamAsColunas` cobre o que QUEBRA a tabela:
// barra vertical, quebra de linha, cerca de código. Este arquivo cobre o que
// NÃO quebra nada e é pior por isso — o texto continua com a forma certa e
// deixa de dizer o que está escrito.
//
// O caminho de exploração é concreto e tem dono: o pagador de um PIX escreve a
// carga no campo de mensagem, ela entra na descrição, o `<pre>` da tela NÃO
// DESENHA NADA ali — então a conferência da pessoa, que é o passo de
// consentimento da feature (ADR-036 (a)), não pode vê-la — e o texto copiado a
// leva ao modelo. O mesmo caminho existe por dentro via nome de conta, que não
// tem validação de charset nenhuma.
//
// Os números das runes estão em HEXADECIMAL, e não como literais: um arquivo
// de teste sobre caracteres invisíveis não pode ter caracteres invisíveis no
// meio do código, onde ninguém os vê ao revisar.

// runasInvisiveis é o corpus da ameaça, e ele é MAIOR do que qualquer
// categoria que o filtro use — é essa diferença que faz o teste valer.
//
// As três primeiras famílias já eram fechadas pela versão por categoria; da
// quarta em diante são as que a revisão de segurança encontrou passando.
var runasInvisiveis = []struct {
	nome string
	r    rune
}{
	// Cc — controles C0 e C1.
	{"C0 NUL U+0000", 0x0000},
	{"C0 ESC U+001B", 0x001B},
	{"C1 PAD U+0080", 0x0080},
	{"C1 NEL U+0085 (quebra de linha para vários leitores)", 0x0085},
	{"C1 APC U+009F", 0x009F},

	// Cf — formatadores invisíveis, com os overrides de Trojan Source.
	{"ALM U+061C", 0x061C},
	{"ZWSP U+200B", 0x200B},
	{"ZWNJ U+200C", 0x200C},
	{"LRM U+200E", 0x200E},
	{"RLM U+200F", 0x200F},
	{"LRE U+202A", 0x202A},
	{"RLE U+202B", 0x202B},
	{"PDF U+202C", 0x202C},
	{"LRO U+202D", 0x202D},
	{"RLO U+202E (inverte o que a pessoa lê)", 0x202E},
	{"LRI U+2066", 0x2066},
	{"RLI U+2067", 0x2067},
	{"FSI U+2068", 0x2068},
	{"PDI U+2069", 0x2069},
	{"BOM U+FEFF", 0xFEFF},

	// Zs/Zl/Zp — separadores.
	{"NBSP U+00A0", 0x00A0},
	{"EN QUAD U+2000", 0x2000},
	{"LINE SEPARATOR U+2028", 0x2028},
	{"PARAGRAPH SEPARATOR U+2029", 0x2029},
	{"IDEOGRAPHIC SPACE U+3000", 0x3000},

	// Bloco Tag — o canal de ASCII smuggling.
	{"TAG U+E0001", 0xE0001},
	{"TAG LATIN A U+E0041", 0xE0041},
	{"CANCEL TAG U+E007F", 0xE007F},

	// Mn — SELETORES DE VARIAÇÃO: o canal corrente de contrabando de bytes em
	// texto lido por LLM, e o que a versão por categoria deixava passar porque
	// `Mn` é legítimo (é dele que vem o acento de "José" em NFD).
	{"VS1 U+FE00", 0xFE00},
	{"VS16 U+FE0F", 0xFE0F},
	{"VS17 U+E0100", 0xE0100},
	{"VS256 U+E01EF", 0xE01EF},
	// Os sete `Default_Ignorable_Code_Point` de Mn que a primeira allowlist
	// deixou passar (achado 1 da passada final do QA, 21/09/2026). O CGJ é o
	// separador invisível clássico; os seletores mongóis são um canal de 2
	// bits por posição.
	{"COMBINING GRAPHEME JOINER U+034F", 0x034F},
	{"KHMER VOWEL INHERENT AQ U+17B4", 0x17B4},
	{"KHMER VOWEL INHERENT AA U+17B5", 0x17B5},
	{"MONGOLIAN FREE VARIATION SELECTOR ONE U+180B", 0x180B},
	{"MONGOLIAN FREE VARIATION SELECTOR TWO U+180C", 0x180C},
	{"MONGOLIAN FREE VARIATION SELECTOR THREE U+180D", 0x180D},
	{"MONGOLIAN FREE VARIATION SELECTOR FOUR U+180F", 0x180F},
	// Três Mn sem glifo que NÃO são Default_Ignorable (achado A4 da revisão
	// de segurança da E9b, 21/09/2026).
	{"TIFINAGH CONSONANT JOINER U+2D7F", 0x2D7F},
	{"BRAHMI NUMBER JOINER U+1107F", 0x1107F},
	{"DUPLOYAN THICK LETTER SELECTOR U+1BC9D", 0x1BC9D},

	// Me — marcas envolventes.
	{"COMBINING ENCLOSING CIRCLE U+20DD", 0x20DD},
	{"COMBINING ENCLOSING KEYCAP U+20E3", 0x20E3},

	// Co — área de uso privado: desenha o que a fonte de quem lê quiser, ou
	// nada.
	{"PRIVATE USE U+E000", 0xE000},
	{"PRIVATE USE U+F8FF", 0xF8FF},

	// Zero-width que NÃO são Cf — os que uma denylist por categoria nunca
	// pegaria.
	{"HANGUL CHOSEONG FILLER U+115F (Lo, largura zero)", 0x115F},
	{"HANGUL JUNGSEONG FILLER U+1160 (Lo, largura zero)", 0x1160},
	{"HANGUL FILLER U+3164 (Lo, largura zero)", 0x3164},
	{"HALFWIDTH HANGUL FILLER U+FFA0 (Lo, largura zero)", 0xFFA0},
	{"BRAILLE PATTERN BLANK U+2800 (So, braile em branco)", 0x2800},

	// Cn — não atribuído nesta versão do Unicode.
	{"NÃO ATRIBUÍDO U+0378", 0x0378},

	// E o resto de uma decodificação que falhou.
	{"REPLACEMENT CHARACTER U+FFFD", 0xFFFD},
}

// alfabetoLegitimo é a PROPRIEDADE INDEPENDENTE: o conjunto fechado de runes
// não-ASCII que o prompt pode conter quando os dados da casa são latinos.
//
// É por AQUI que o teste deixa de ser tautologia. A versão anterior varria
// exatamente as categorias que o filtro usava (Cc, Cf, Zl, Zp, Zs), então não
// podia falhar para uma classe que o filtro esquecesse — e o filtro esquecia
// quatro. Uma lista de CARACTERES, montada a partir do que este produto
// legitimamente escreve, não é espelho de implementação nenhuma: se o filtro
// deixar passar um seletor de variação, ele aparece aqui como
// "rune inesperada U+FE0F", venha ele de que categoria vier.
//
// Cada entrada diz de onde vem, para quem acrescentar uma ter de justificá-la.
var alfabetoLegitimo = map[rune]string{
	0x00B7: "· separador da lista de grupos (seção 7)",
	0x2014: "— travessão: o 'não tem' das tabelas e a pontuação do texto",
	0x2026: "… reticências do esqueleto de JSON da seção 9",
	// As letras acentuadas do português, que vêm do texto do produto E dos
	// dados da casa (nome de conta, de categoria, descrição).
	0x00C0: "À", 0x00C1: "Á", 0x00C2: "Â", 0x00C3: "Ã", 0x00C7: "Ç",
	0x00C9: "É", 0x00CA: "Ê", 0x00CD: "Í", 0x00D3: "Ó", 0x00D4: "Ô",
	0x00D5: "Õ", 0x00DA: "Ú", 0x00DC: "Ü",
	0x00E0: "à", 0x00E1: "á", 0x00E2: "â", 0x00E3: "ã", 0x00E7: "ç",
	0x00E9: "é", 0x00EA: "ê", 0x00ED: "í", 0x00F3: "ó", 0x00F4: "ô",
	0x00F5: "õ", 0x00FA: "ú", 0x00FC: "ü",
	// O que aparece em extrato brasileiro REAL e o filtro deixa passar de
	// propósito (achado 4 da passada final do QA, 21/09/2026). Estão aqui para
	// que uma descrição realista numa fixture nunca acuse dado legítimo — o
	// conserto natural seria alargar esta lista às pressas, que é a erosão
	// que ela existe para impedir. O contrapeso é
	// TestTextoRealDeExtratoSobreviveInteiro, do QA.
	0x00BA: "º ordinal masculino — '1º LOTE', 'Nº 1234'",
	0x00AA: "ª ordinal feminino — '2ª VIA'",
	0x201C: "“ aspa tipográfica de abertura — nome de estabelecimento vindo de sistema de PDV",
	0x201D: "” aspa tipográfica de fechamento — idem",
	0x2022: "• marcador — separador de campos em extrato de cartão",
	0x00B0: "° grau — 'N°' escrito com grau, e temperatura em nome de posto",
	0x00BD: "½ meio — 'DEVOLUÇÃO ½ VALOR'",
	0x2116: "№ sinal de número — numeração de documento",
	0x2122: "™ marca registrada — nome de estabelecimento",
	0x2192: "→ seta — 'TRANSFERÊNCIA → CONTA 1' em extrato de banco digital",
	0x20AC: "€ euro — compra internacional",
	0x00A3: "£ libra — compra internacional",
}

// casaHostil injeta o corpus inteiro nas TRÊS portas por onde texto de fora
// entra no prompt: descrição (que um estranho escolhe), nome de conta e nome
// de categoria com as palavras-chave (que um membro da casa escolhe).
func casaHostil(t *testing.T) *ambiente {
	t.Helper()
	a := novoAmbiente(t)
	a.casaComum()

	var sujeira strings.Builder
	for _, inv := range runasInvisiveis {
		sujeira.WriteRune(inv.r)
	}
	lixo := sujeira.String()

	a.ledger.linhas = []lancamento{
		{casa: minhaCasa, mes: mes1, desc: "MERCADO" + lixo + "CENTRAL",
			kind: transaction.KindExpense, conta: contaCorrente, cents: 1_00},
		// Bytes inválidos de UTF-8, que é o que um extrato mal decodificado
		// entrega: o `range` os devolve como U+FFFD.
		{casa: minhaCasa, mes: mes1, desc: "POSTO \xff\xfe SHELL",
			kind: transaction.KindExpense, conta: contaCorrente, cents: 2_00},
		// E texto REALISTA misturado ao lixo, com os símbolos que extrato
		// brasileiro traz de verdade: é o que impede a propriedade de passar
		// só porque a fixture era sintética (achado 4 do QA).
		{casa: minhaCasa, mes: mes1,
			desc: "MERCADO “DO ZÉ” Nº 1º" + lixo + "• 2ª VIA ½ № 77 ™ → 25°C €50 £30",
			kind: transaction.KindExpense, conta: contaCorrente, cents: 3_00},
	}
	a.contas.accs = append(a.contas.accs, account.Account{
		ID: "conta-hostil-inv", HouseholdID: minhaCasa,
		Name: "Banco" + lixo + "Falso", NameNorm: "banco falso", Kind: account.KindChecking,
	})
	a.contas.kws = append(a.contas.kws, account.Keyword{
		ID: "k-inv", HouseholdID: minhaCasa, AccountID: "conta-hostil-inv",
		Keyword: "pix" + lixo + "recebido", Norm: "pix recebido", Position: 0,
	})
	a.cats.cats = append(a.cats.cats, category.Category{
		ID: "cat-hostil-inv", HouseholdID: minhaCasa, ParentID: ptr(grupoAlimentacao),
		Name: "Padaria" + lixo + "Falsa", NameNorm: "padaria falsa", Kind: category.KindExpense,
	})
	a.cats.kws = append(a.cats.kws, category.Keyword{
		ID: "ck-inv", HouseholdID: minhaCasa, CategoryID: "cat-hostil-inv",
		Keyword: "pao" + lixo + "frances", Norm: "pao frances", Position: 0,
	})
	return a
}

// A PROPRIEDADE INDEPENDENTE: com o corpus inteiro entrando pelas três portas,
// o prompt continua escrito SÓ com o alfabeto legítimo deste domínio.
//
// Uma classe que o filtro esquecer aparece aqui pelo codepoint, sem que este
// teste precise saber que a classe existe.
func TestPromptSoUsaOAlfabetoLegitimoDoDominio(t *testing.T) {
	t.Parallel()
	a := casaHostil(t)

	prompt := a.exportar(t, minhaCasa, mes1, mes3).Prompt
	require.True(t, utf8.ValidString(prompt), "o prompt precisa ser UTF-8 válido")

	inesperadas := map[rune]int{}
	for _, r := range prompt {
		switch {
		case r == '\n':
		case r >= 0x20 && r <= 0x7E: // ASCII imprimível
		default:
			if _, legitima := alfabetoLegitimo[r]; !legitima {
				inesperadas[r]++
			}
		}
	}

	if len(inesperadas) > 0 {
		var achados []string
		for r, n := range inesperadas {
			achados = append(achados, fmt.Sprintf("U+%04X (%d×)", r, n))
		}
		t.Fatalf("rune inesperada no prompt: %s — ou ela é legítima e entra em "+
			"alfabetoLegitimo com justificativa, ou o filtro de `desenha` a deixou passar",
			strings.Join(achados, ", "))
	}
}

// Nenhuma rune do corpus sobrevive, uma a uma e pelas três portas — o teste
// que diz QUAL escapou quando o de cima acusa.
func TestRunasInvisiveisNaoChegamAoPrompt(t *testing.T) {
	t.Parallel()

	for _, inv := range runasInvisiveis {
		t.Run(inv.nome, func(t *testing.T) {
			t.Parallel()
			a := novoAmbiente(t)
			a.casaComum()

			// A rune entra no MEIO de um texto legítimo, que é como ela chega
			// de verdade: colada no que a pessoa vai ler.
			a.contas.accs = append(a.contas.accs, account.Account{
				ID: "conta-invisivel", HouseholdID: minhaCasa,
				Name:     "Banco" + string(inv.r) + "Falso",
				NameNorm: "banco falso", Kind: account.KindChecking,
			})
			a.ledger.linhas = []lancamento{{
				casa: minhaCasa, mes: mes1, kind: transaction.KindExpense,
				desc:  "PAGAMENTO" + string(inv.r) + "RECEBIDO",
				conta: contaCorrente, cents: 1_00,
			}}

			prompt := a.exportar(t, minhaCasa, mes1, mes3).Prompt

			assert.NotContainsf(t, prompt, string(inv.r),
				"%s sobreviveu ao prompt: o texto deixa de dizer o que a pessoa lê", inv.nome)
		})
	}
}

// A limpeza troca o invisível por ESPAÇO, e nunca gruda uma palavra na
// seguinte: "BANCO<RLO>INTER" não pode virar "BANCOINTER", que é uma descrição
// que não existe e casaria com palavra-chave que não devia.
//
// E a tabela continua com as suas 6 colunas: sanitizar não pode ser a nova
// forma de quebrá-la.
func TestInvisivelViraEspacoENaoGrudaPalavras(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.linhas = []lancamento{
		{casa: minhaCasa, mes: mes1,
			desc: "BANCO" + string(rune(0x202E)) + "INTER" + string(rune(0x00A0)) + "AGENCIA",
			kind: transaction.KindExpense, conta: contaCorrente, cents: 1_00},
	}

	view := a.exportar(t, minhaCasa, mes1, mes3)
	linha := linhaDaTabela(t, view.Prompt, "BANCO")

	assert.Contains(t, linha, "BANCO INTER AGENCIA", "cada invisível vira UM espaço")
	assert.NotContains(t, linha, "BANCOINTER", "remover sem separar inventaria uma descrição")
	assert.Equal(t, 6, colunas(linha), "a sanitização não pode quebrar a tabela")
}

// Descrição feita SÓ de invisíveis não vira célula vazia: célula vazia numa
// tabela Markdown é lida como descuido, e a IA não tem como saber que havia
// alguma coisa ali. Ela vira o travessão, que é uma afirmação.
func TestDescricaoSoDeInvisiveisViraTravessao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.linhas = []lancamento{
		{casa: minhaCasa, mes: mes1,
			desc: string(rune(0x202E)) + string(rune(0x200B)) + string(rune(0x00A0)) +
				string(rune(0x2800)) + string(rune(0xFE0F)),
			kind: transaction.KindExpense, conta: contaCorrente, cents: 1_00},
	}

	view := a.exportar(t, minhaCasa, mes1, mes3)

	linha := linhaDaTabela(t, view.Prompt, "—")
	assert.Equal(t, 6, colunas(linha))
	assert.NotContains(t, view.Prompt, "|  |", "nenhuma célula vazia")
}

// O acento COMBINANTE continua passando — é `Mn`, como os seletores de
// variação, e é por isso que a allowlist não pôde recusar `Mn` inteiro.
//
// "José" digitado em NFD (e + U+0301) tem de sobreviver: recusar acento solto
// faria o nome de uma conta perder letras conforme o teclado de quem digitou.
func TestAcentoCombinanteSobrevive(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.casaComum()
	a.ledger.linhas = []lancamento{
		{casa: minhaCasa, mes: mes1, desc: "JOSE" + string(rune(0x0301)) + " LANCHES",
			kind: transaction.KindExpense, conta: contaCorrente, cents: 1_00},
	}

	view := a.exportar(t, minhaCasa, mes1, mes3)

	assert.Contains(t, view.Prompt, "JOSE"+string(rune(0x0301)),
		"o acento combinante é Mn e DESENHA: ele não pode cair junto com os seletores de variação")
}

// O OUTRO LADO DA ALLOWLIST: o que ela NÃO pode descartar.
//
// `TestPromptSoUsaOAlfabetoLegitimoDoDominio` empurra o filtro para o lado de
// recusar; sozinho, ele é satisfeito por um filtro que jogue fora tudo o que
// não é ASCII — e um filtro assim passaria naquele teste enquanto destruía o
// dado que o prompt existe para levar.
//
// A troca de uma denylist por uma ALLOWLIST por categoria (21/09/2026) é
// exatamente a mudança que corre esse risco, e o preço dela está declarado no
// comentário de `desenha`: "o que este pacote não reconhece como tinta vira
// ESPAÇO". Quem reconhece a tinta é `unicode.IsPunct`/`IsSymbol`/`IsNumber` —
// então a pergunta que este teste responde é se a tabela do Go concorda com o
// que um extrato brasileiro de verdade escreve.
//
// As amostras são texto de extrato e de fatura, não corpus sintético: ordinal
// (`1º`, `2ª`), travessão, aspas tipográficas, marcador, grau, fração, sinal de
// número, símbolo de moeda estrangeira, seta, apóstrofo e hífen dentro de nome
// próprio. Cada uma que cair silenciosamente vira uma descrição que a IA não
// reconhece e uma palavra-chave que a pessoa não entende por que não casa.
func TestTextoRealDeExtratoSobreviveInteiro(t *testing.T) {
	t.Parallel()

	amostras := []string{
		"PIX ENVIADO MARIA JOSÉ D'ÁVILA-SANTOS",
		"COMPRA C6 BANK S/A — 1º LOTE",
		"TARIFA MENSALIDADE R$ 29,90",
		"IOF 0,38% s/ COMPRA US$ 12.00",
		"CIA BRASILEIRA DE DISTRIB. Nº 1234",
		"MERCADO “DO ZÉ” LTDA",
		"PGTO FATURA • CARTÃO",
		"POSTO 25°C ANGELONI",
		"AÇAÍ & CIA (2ª VIA)",
		"DEVOLUÇÃO ½ VALOR № 77 ™",
		"TRANSFERÊNCIA → CONTA 1",
		"COMPRA €50 £30",
	}

	a := novoAmbiente(t)
	a.casaComum()
	for i, s := range amostras {
		a.ledger.linhas = append(a.ledger.linhas, lancamento{
			casa: minhaCasa, mes: mes1, kind: transaction.KindExpense,
			desc: s, conta: contaCorrente, cents: int64(100 * (i + 1)),
		})
	}
	// O nome de conta e a palavra-chave passam pelo MESMO `celula`, e são as
	// outras duas portas por onde o dado da casa entra no texto.
	a.contas.accs = append(a.contas.accs, account.Account{
		ID: "conta-real", HouseholdID: minhaCasa,
		Name: "Itaú — Conta Corrente (1ª)", NameNorm: "itau conta corrente 1a",
		Kind: account.KindChecking,
	})
	a.contas.kws = append(a.contas.kws, account.Keyword{
		ID: "k-real", HouseholdID: minhaCasa, AccountID: "conta-real",
		Keyword: "itaú unibanco s/a", Norm: "itau unibanco sa", Position: 0,
	})

	prompt := a.exportar(t, minhaCasa, mes1, mes3).Prompt

	// Nenhuma amostra tem `|` nem `\`, então o texto entra na célula como veio
	// e a comparação é direta.
	for _, s := range amostras {
		if strings.Contains(prompt, s) {
			continue
		}
		var perdidas []string
		for _, r := range s {
			if !strings.ContainsRune(prompt, r) {
				perdidas = append(perdidas, fmt.Sprintf("U+%04X (%q)", r, r))
			}
		}
		t.Errorf("a allowlist mutilou uma descrição real: %q — runes que sumiram: %s",
			s, strings.Join(perdidas, ", "))
	}
	assert.Contains(t, prompt, "Itaú — Conta Corrente (1ª)", "nome de conta real")
	assert.Contains(t, prompt, "itaú unibanco s/a", "palavra-chave real")

	// E nenhuma delas quebrou a tabela no caminho: sanitizar não pode ser a
	// nova forma de embaralhar as colunas.
	for _, l := range linhasDeTabela(secaoDoPrompt(t, prompt, 8)) {
		assert.Equalf(t, 6, colunas(l), "linha da seção 8 fora das 6 colunas: %q", l)
	}
}
