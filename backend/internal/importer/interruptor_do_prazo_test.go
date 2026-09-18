package importer_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// O PORTÃO DO INTERRUPTOR DE PRAZO (trava do achado A7)
// ---------------------------------------------------------------------------
//
// WithAnalyzeTimeout consegue ESTICAR o prazo da fase 1 — não tem clamp. A
// revisão de segurança aceitou isso como dívida DECLARADA, e não como descuido,
// por um motivo que está escrito na própria Option: o único chamador legítimo é
// o teste de desempenho do critério 11, que precisa de 5 minutos sob `-race`.
//
// Mas "o único chamador é um teste" é uma afirmação sobre o código de HOJE, e
// afirmação que ninguém confere apodrece. O que sustenta o prazo de produção é
// cmd/api montar o Service sem opção nenhuma; no dia em que alguém ligar esta
// Option a uma variável de ambiente "só para destravar um cliente", o prazo de
// produção deixa de existir e nenhum outro teste percebe — a fase 1 continuaria
// passando, só que sem teto.
//
// Este portão é essa conferência, no espírito de
// config.TestSemOInterruptorOAmbienteNaoMudaNenhumLimite: o interruptor de
// teste não pode vazar para o caminho de produção. A varredura de arquivo é o
// mesmo desenho do sqlsafety_test.go do gormstore — ela prende o que a revisão
// de código humana esquece.

// interruptor é o identificador vigiado. Está aqui, numa constante, para que o
// portão e a mensagem de falha nunca divirjam.
const interruptor = "WithAnalyzeTimeout"

// raízes do código de PRODUÇÃO do backend, vistas a partir deste pacote.
//
// `cmd/` entra junto e é o alvo mais importante dos dois: é lá que mora a
// montagem real do Service, e é lá que uma "configuração temporária" apareceria
// primeiro. Varrer só internal/ deixaria justamente o caminho de produção de
// fora.
const (
	raizInternal = ".."
	raizCmd      = "../../cmd"
)

// usosDoInterruptor devolve as linhas (1-based) que USAM o interruptor.
//
// Não contam como uso:
//
//   - COMENTÁRIO. A Option é documentada, citada por outros comentários e
//     explicada nesta própria trava; proibir a menção proibiria explicar a
//     regra, que é o oposto do que se quer;
//   - a própria DECLARAÇÃO `func WithAnalyzeTimeout(`. O portão veta CHAMAR o
//     interruptor fora de teste, não existir.
//
// Conta como uso qualquer outra linha de código que cite o identificador —
// inclusive `importer.WithAnalyzeTimeout(...)` de outro pacote, que é
// exatamente a forma que cmd/api usaria.
func usosDoInterruptor(conteudo string) []int {
	var fora []int
	dentroDeBloco := false
	for i, linha := range strings.Split(conteudo, "\n") {
		limpa := strings.TrimSpace(linha)

		// Comentário de bloco: o projeto usa `//`, mas um `/* */` que
		// escapasse do portão o tornaria contornável por acidente de estilo.
		if dentroDeBloco {
			if idx := strings.Index(limpa, "*/"); idx >= 0 {
				dentroDeBloco = false
				limpa = strings.TrimSpace(limpa[idx+2:])
			} else {
				continue
			}
		}
		if strings.HasPrefix(limpa, "/*") && !strings.Contains(limpa, "*/") {
			dentroDeBloco = true
			continue
		}
		if strings.HasPrefix(limpa, "//") {
			continue
		}
		if !strings.Contains(limpa, interruptor) {
			continue
		}
		if strings.HasPrefix(limpa, "func "+interruptor+"(") {
			continue
		}
		fora = append(fora, i+1)
	}
	return fora
}

// varrerProducao roda o corpo sobre todo .go de PRODUÇÃO de internal/ e cmd/.
// `_test.go` fica de fora: é o único lugar onde o interruptor é legítimo.
func varrerProducao(t *testing.T, fn func(caminho, conteudo string)) {
	t.Helper()
	for _, raiz := range []string{raizInternal, raizCmd} {
		require.NoError(t, filepath.WalkDir(raiz, func(caminho string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(caminho, ".go") || strings.HasSuffix(caminho, "_test.go") {
				return nil
			}
			bruto, err := os.ReadFile(caminho)
			if err != nil {
				return err
			}
			fn(filepath.ToSlash(caminho), string(bruto))
			return nil
		}))
	}
}

// A TRAVA: o interruptor de teste não aparece em NENHUM arquivo de produção.
//
// Falhou? A correção não é adicionar uma exceção aqui. Ou o chamador novo é um
// teste (e o arquivo tem de terminar em `_test.go`), ou o prazo precisa mesmo
// ser configurável em produção — e aí a dívida do A7 deixou de ser dívida de
// teste: a Option ganha clamp contra AnalyzeTimeout e a mudança volta para a
// revisão de segurança.
func TestInterruptorDoPrazoNaoApareceEmCodigoDeProducao(t *testing.T) {
	t.Parallel()

	var violacoes []string
	varrerProducao(t, func(caminho, conteudo string) {
		for _, linha := range usosDoInterruptor(conteudo) {
			violacoes = append(violacoes, caminho+":"+strconv.Itoa(linha))
		}
	})

	assert.Empty(t, violacoes,
		"%s é interruptor de TESTE e não pode ser chamado em código de produção (achado A7)", interruptor)
}

// O PORTÃO PRECISA ENXERGAR UMA VIOLAÇÃO DE VERDADE.
//
// Sem este controle negativo, um erro de digitação na constante, um filtro de
// comentário largo demais ou uma varredura que não acha nada deixariam o teste
// acima VERDE para sempre — um portão vazio é pior do que portão nenhum, porque
// dá a impressão de que alguém está olhando.
func TestOPortaoDoInterruptorEnxergaUmaViolacao(t *testing.T) {
	t.Parallel()

	proibidos := map[string]string{
		"chamada direta":        "func novo() { aplicar(" + interruptor + "(d)) }",
		"qualificada por outro": "svc := importer.NewService(r, importer." + interruptor + "(prazo))",
		"vinda do ambiente":     "opts = append(opts, importer." + interruptor + "(cfg.ImportAnalyzeTimeout))",
	}
	for nome, codigo := range proibidos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			assert.NotEmpty(t, usosDoInterruptor(codigo), "o portão deixou passar: %s", codigo)
		})
	}

	permitidos := map[string]string{
		"declaração":           "func " + interruptor + "(d time.Duration) Option {",
		"comentário de linha":  "// " + interruptor + " ajusta o prazo da fase 1 (teste).",
		"comentário indentado": "\t// ver " + interruptor + " para o porquê",
		"comentário de bloco":  "/*\n" + interruptor + " não pode ser usada em produção.\n*/",
	}
	for nome, codigo := range permitidos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, usosDoInterruptor(codigo), "o portão acusou o que é legítimo: %s", codigo)
		})
	}
}

// A VARREDURA PRECISA ALCANÇAR AS DUAS ÁRVORES, e ter achado a DECLARAÇÃO.
//
// Achar a declaração é a prova de que o caminho até internal/importer/service.go
// está certo: se o nome da Option mudar, ou se o arquivo se mover, a trava
// acima passaria a vigiar um identificador que não existe mais — e continuaria
// verde.
func TestVarreduraDoInterruptorAlcancaOsDoisLados(t *testing.T) {
	t.Parallel()

	vistos := map[string]bool{}
	declaracaoAchada := false
	varrerProducao(t, func(caminho, conteudo string) {
		switch {
		case strings.Contains(caminho, "/cmd/") || strings.HasPrefix(caminho, "../../cmd/"):
			vistos["cmd"] = true
		default:
			vistos["internal"] = true
		}
		if strings.Contains(conteudo, "func "+interruptor+"(") {
			declaracaoAchada = true
		}
	})

	assert.True(t, vistos["internal"], "a varredura não alcançou internal/")
	assert.True(t, vistos["cmd"], "a varredura não alcançou cmd/")
	assert.True(t, declaracaoAchada,
		"a varredura não achou a declaração de %s — a trava estaria vigiando um nome morto", interruptor)
}
