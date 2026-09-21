package sanitize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// bullet é o caractere de máscara do Nubank (U+2022), escrito de forma
// escapada para não depender de codificação do editor.
const bullet = "\u2022"

// TestDescriptionCasoOuro é o caso que define o contrato do pacote.
func TestDescriptionCasoOuro(t *testing.T) {
	entrada := "Transferência enviada pelo Pix - Marcelo Júnior Coutinho Zanelato - " +
		bullet + bullet + bullet + ".996.897-" + bullet + bullet +
		" - NU PAGAMENTOS - IP (0260) Agência: 1 Conta: 8703544-1"

	require.Equal(t, "Pix enviado - Marcelo Júnior Coutinho Zanelato", Description(entrada))
}

func TestDescriptionPix(t *testing.T) {
	casos := []struct {
		nome     string
		entrada  string
		esperado string
	}{
		{
			nome: "Pix recebido de pessoa física",
			entrada: "Transferência recebida pelo Pix - BELTRANA DE SOUZA - " +
				bullet + bullet + bullet + ".333.444-" + bullet + bullet +
				" - BCO C6 S.A. (0336) Agência: 1 Conta: 2000002-2",
			esperado: "Pix recebido - BELTRANA DE SOUZA",
		},
		{
			nome: "Pix enviado para empresa (CNPJ completo)",
			entrada: "Transferência enviada pelo Pix - ENERGIA EXEMPLO S.A. - 11.222.333/0001-44 " +
				"- ITAÚ UNIBANCO S.A. (0341) Agência: 912 Conta: 11554-0",
			esperado: "Pix enviado - ENERGIA EXEMPLO S.A.",
		},
		{
			nome: "reembolso recebido",
			entrada: "Reembolso recebido pelo Pix - Fulano de Tal Silva - " +
				bullet + bullet + bullet + ".111.222-" + bullet + bullet +
				" - NU PAGAMENTOS - IP (0260) Agência: 1 Conta: 1000001-1",
			esperado: "Pix reembolso recebido - Fulano de Tal Silva",
		},
		{
			nome: "reembolso enviado",
			entrada: "Reembolso enviado pelo Pix - PADARIA EXEMPLO LTDA - 22.333.444/0001-55 " +
				"- BCO DO BRASIL S.A. (0001) Agência: 21 Conta: 30001-1",
			esperado: "Pix reembolso enviado - PADARIA EXEMPLO LTDA",
		},
		{
			nome:     "estorno",
			entrada:  "Estorno de transferência enviada pelo Pix - Fulano - 11.222.333/0001-44",
			esperado: "Pix enviado estornado - Fulano",
		},
		{
			nome:     "o prefixo casa sem depender de acento nem de caixa",
			entrada:  "TRANSFERENCIA ENVIADA PELO PIX - Fulano - 11.222.333/0001-44",
			esperado: "Pix enviado - Fulano",
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			require.Equal(t, c.esperado, Description(c.entrada))
		})
	}
}

// TestDescriptionCurtasPassamIntactas: o sanitizador não pode inventar trabalho
// onde não há identificador nenhum.
func TestDescriptionCurtasPassamIntactas(t *testing.T) {
	for _, s := range []string{
		"Pagamento de fatura",
		"Resgate RDB",
		"Aplicação RDB",
		"Compra no débito - Padaria Exemplo",
		"Lanchonete Exemplo",
		"App*Entrega Exemplo",
		"Dl*Corrida Exemplo",
		"Ajuste a crédito",
		"Crédito em conta",
	} {
		t.Run(s, func(t *testing.T) {
			require.Equal(t, s, Description(s))
		})
	}
}

func TestDescriptionRemoveIdentificadores(t *testing.T) {
	casos := []struct {
		nome     string
		entrada  string
		esperado string
	}{
		{"CPF mascarado solto", "Pagamento " + bullet + bullet + bullet + ".996.897-" + bullet + bullet, "Pagamento"},
		{"CPF mascarado com asterisco", "Pagamento ***.996.897-**", "Pagamento"},
		{"CPF completo", "Pagamento 123.456.789-01", "Pagamento"},
		{"CNPJ completo", "Pagamento 11.222.333/0001-44", "Pagamento"},
		{"CPF sem pontuação", "Pagamento 12345678901", "Pagamento"},
		{"CNPJ sem pontuação", "Pagamento 11222333000144", "Pagamento"},
		{"agência e conta", "Loja Exemplo Agência: 1 Conta: 8703544-1", "Loja Exemplo"},
		{"agência sem acento e sem dois pontos", "Loja Exemplo Agencia 1234 Conta 55555-0", "Loja Exemplo"},
		{"código do banco", "Loja Exemplo (0260)", "Loja Exemplo"},
		{"tudo junto", "Loja Exemplo (0341) Agência: 912 Conta: 11554-0", "Loja Exemplo"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			require.Equal(t, c.esperado, Description(c.entrada))
		})
	}
}

// TestDescriptionNaoApagaLancamentoQueSoTemDocumento: se o único conteúdo é um
// documento, o resultado fica vazio — mas o corte por segmento não pode ser o
// responsável por isso (ele nunca corta no índice 0).
func TestDescriptionDocumentoNoPrimeiroSegmento(t *testing.T) {
	// O documento sai, mas o segmento sobrevive e a contraparte continua ali:
	// o corte por segmento nunca acontece no índice 0.
	require.Equal(t, "CPF - Fulano de Tal", Description("CPF 123.456.789-01 - Fulano de Tal"))
	require.Equal(t, "", Description("123.456.789-01"))
}

// TestDescriptionRemoveControleEBidi é a defesa contra Trojan Source: os
// overrides de direção reordenam o que o usuário LÊ sem mudar o que está
// guardado. Num app financeiro isso é uma fraude pronta.
func TestDescriptionRemoveControleEBidi(t *testing.T) {
	invisiveis := map[string]string{
		"NUL":                       "\x00",
		"BEL":                       "\x07",
		"DEL":                       "\x7F",
		"escape":                    "\x1b",
		"BOM":                       "\uFEFF",
		"zero-width space":          "\u200B",
		"zero-width non-joiner":     "\u200C",
		"zero-width joiner":         "\u200D",
		"left-to-right mark":        "\u200E",
		"right-to-left mark":        "\u200F",
		"LRE (override de direção)": "\u202A",
		"RLE (override de direção)": "\u202B",
		"PDF (override de direção)": "\u202C",
		"LRO (override de direção)": "\u202D",
		"RLO (override de direção)": "\u202E",
		"LRI (isolate)":             "\u2066",
		"RLI (isolate)":             "\u2067",
		"FSI (isolate)":             "\u2068",
		"PDI (isolate)":             "\u2069",
		"soft hyphen":               "\u00AD",
		"word joiner":               "\u2060",
	}

	for nome, ch := range invisiveis {
		t.Run(nome, func(t *testing.T) {
			got := Description("PADARIA" + ch + " EXEMPLO")
			require.Equal(t, "PADARIA EXEMPLO", got)
			require.NotContains(t, got, ch, "o caractere invisível sobreviveu")
		})
	}
}

// TestDescriptionAtaqueTrojanSourceCompleto monta o ataque de verdade: o texto
// exibido seria "MERCADO" e o guardado, outro.
func TestDescriptionAtaqueTrojanSource(t *testing.T) {
	entrada := "\u202EODACREM\u202C ESTORNO"
	got := Description(entrada)
	require.Equal(t, "ODACREM ESTORNO", got)
	for _, r := range got {
		require.False(t, r >= 0x202A && r <= 0x202E, "sobrou override de direção")
		require.False(t, r >= 0x2066 && r <= 0x2069, "sobrou isolate de direção")
	}
}

func TestDescriptionQuebrasDeLinhaViramEspaco(t *testing.T) {
	// Apagar a quebra grudaria as palavras; virar espaço preserva a leitura.
	require.Equal(t, "PADARIA EXEMPLO", Description("PADARIA\nEXEMPLO"))
	require.Equal(t, "PADARIA EXEMPLO", Description("PADARIA\r\nEXEMPLO"))
	require.Equal(t, "PADARIA EXEMPLO", Description("PADARIA\tEXEMPLO"))
}

func TestDescriptionColapsaEspacos(t *testing.T) {
	require.Equal(t, "PADARIA EXEMPLO", Description("   PADARIA     EXEMPLO   "))
	require.Equal(t, "PADARIA EXEMPLO", Description("PADARIA\u00A0EXEMPLO")) // NBSP
	require.Equal(t, "", Description("     "))
	require.Equal(t, "", Description(""))
}

// TestDescriptionTruncaEmRunas prova que o teto é de RUNAS: cortar por byte
// partiria um caractere multibyte e produziria UTF-8 inválido.
func TestDescriptionTruncaEmRunas(t *testing.T) {
	t.Run("acentuada", func(t *testing.T) {
		entrada := strings.Repeat("ç", 300)
		got := Description(entrada)
		require.Equal(t, MaxRunes, utf8.RuneCountInString(got))
		require.True(t, utf8.ValidString(got), "a truncagem não pode produzir UTF-8 inválido")
		require.Equal(t, strings.Repeat("ç", MaxRunes), got)
	})

	t.Run("emoji (4 bytes por runa)", func(t *testing.T) {
		entrada := strings.Repeat("🙂", 200)
		got := Description(entrada)
		require.Equal(t, MaxRunes, utf8.RuneCountInString(got))
		require.True(t, utf8.ValidString(got))
	})

	t.Run("exatamente no limite não é truncada", func(t *testing.T) {
		entrada := strings.Repeat("a", MaxRunes)
		require.Equal(t, entrada, Description(entrada))
	})

	t.Run("não termina em separador solto", func(t *testing.T) {
		entrada := strings.Repeat("a", MaxRunes-1) + " - resto que será cortado"
		got := Description(entrada)
		require.LessOrEqual(t, utf8.RuneCountInString(got), MaxRunes)
		require.False(t, strings.HasSuffix(got, "-"))
		require.False(t, strings.HasSuffix(got, " "))
	})
}

// TestDescriptionNaoNeutralizaCSVInjection documenta a decisão: a defesa é na
// EXPORTAÇÃO (E6). Neutralizar aqui alteraria o dado do usuário para proteger um
// programa de terceiro.
func TestDescriptionNaoNeutralizaCSVInjection(t *testing.T) {
	for _, s := range []string{
		"=SOMA(A1:A9)",
		"+CMD|'/c calc'!A0",
		"-MERCADO EXEMPLO",
		"@Padaria Exemplo",
	} {
		t.Run(s, func(t *testing.T) {
			got := Description(s)
			require.Equal(t, strings.Trim(s, " -–—,;/|"), got,
				"o texto é guardado fiel; a neutralização é na exportação (E6)")
		})
	}
}

// TestDescriptionDeterministica: a mesma entrada tem de produzir a mesma saída
// sempre — é disso que a deduplicação depende.
func TestDescriptionDeterministica(t *testing.T) {
	entrada := "Transferência enviada pelo Pix - Marcelo Júnior Coutinho Zanelato - " +
		bullet + bullet + bullet + ".996.897-" + bullet + bullet +
		" - NU PAGAMENTOS - IP (0260) Agência: 1 Conta: 8703544-1"

	primeira := Description(entrada)
	for range 500 {
		require.Equal(t, primeira, Description(entrada))
	}
}

// TestDescriptionIdempotente: sanitizar duas vezes tem de dar o mesmo
// resultado. Sem isso, um lançamento reprocessado geraria outra chave de dedup.
func TestDescriptionIdempotente(t *testing.T) {
	entradas := []string{
		"Transferência enviada pelo Pix - Fulano - " + bullet + bullet + bullet + ".111.222-" + bullet + bullet + " - NU PAGAMENTOS - IP (0260) Agência: 1 Conta: 1-1",
		"Pagamento de fatura",
		"Loja Exemplo (0260) Agência: 1 Conta: 8703544-1",
		strings.Repeat("ç", 300),
		"",
	}
	for _, e := range entradas {
		uma := Description(e)
		require.Equal(t, uma, Description(uma), "sanitizar o já sanitizado tem de ser inócuo")
	}
}

// TestDescriptionNasFixturesReais roda o sanitizador em todas as descrições dos
// arquivos anonimizados e garante que NENHUM identificador escapa.
//
// As fixtures pertencem à tarefa do adaptador Nubank — aqui elas são só lidas.
func TestDescriptionNasFixturesReais(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "nubank", "testdata", "nubank_checking_v1.csv"))
	require.NoError(t, err)

	linhas := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	require.Greater(t, len(linhas), 2)

	descricoes := 0
	for _, linha := range linhas[1:] {
		if strings.TrimSpace(linha) == "" {
			continue
		}
		// A descrição é o último campo destas fixtures.
		campos := strings.Split(linha, ",")
		bruta := campos[len(campos)-1]
		limpa := Description(bruta)
		descricoes++

		require.NotContains(t, limpa, bullet, "sobrou máscara de CPF em %q", limpa)
		require.NotContains(t, limpa, "/0001-", "sobrou CNPJ em %q", limpa)
		require.NotRegexp(t, `(?i)ag[êe]ncia\s*:?\s*[0-9]`, limpa, "sobrou agência em %q", limpa)
		require.NotRegexp(t, `(?i)conta\s*:?\s*[0-9]`, limpa, "sobrou conta em %q", limpa)
		require.NotRegexp(t, `\([0-9]{3,4}\)`, limpa, "sobrou código de banco em %q", limpa)
		require.LessOrEqual(t, utf8.RuneCountInString(limpa), MaxRunes)
	}
	require.Greater(t, descricoes, 5, "a fixture precisa ter linhas suficientes para o teste valer")
}

// TestVersaoEstavel trava a versão do contrato: subir esta constante é uma
// decisão consciente sobre o histórico já importado, nunca um efeito colateral.
func TestVersaoEstavel(t *testing.T) {
	require.Equal(t, "v1", Version)
	require.Equal(t, 140, MaxRunes)
}

// TestPacoteSemCaractereCruInvisivel impede a regressão que já derrubou o build
// deste importador: BOM cru dentro de um fonte Go só é aceito no primeiro byte
// do arquivo; em qualquer outra posição o pacote inteiro deixa de compilar.
func TestPacoteSemCaractereCruInvisivel(t *testing.T) {
	entradas, err := os.ReadDir(".")
	require.NoError(t, err)
	for _, e := range entradas {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		conteudo, err := os.ReadFile(e.Name())
		require.NoError(t, err)
		for i, r := range string(conteudo) {
			if i == 0 {
				continue
			}
			require.NotEqual(t, '\uFEFF', r, "%s tem um BOM cru no byte %d — use \\uFEFF", e.Name(), i)
		}
	}
}
