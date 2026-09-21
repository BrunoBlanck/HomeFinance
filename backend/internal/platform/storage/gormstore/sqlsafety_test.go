package gormstore_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// internalRoot é a raiz de internal/ vista a partir deste pacote.
const internalRoot = "../../../../internal"

// cmdRoot é a raiz de cmd/, vizinha de internal/.
const cmdRoot = "../../../../cmd"

// varrerCodigo roda o corpo sobre TODAS as árvores de código de produção do
// backend — internal/ e cmd/.
//
// `cmd/` entra junto, e a omissão anterior não era inofensiva: os dois portões
// deste arquivo — "nada de SQL montado" e "GORM não vaza" — sustentam decisões
// tomadas em outro lugar. A omissão do processador Raw em
// internal/platform/storage/ctxerr.go, por exemplo, apoia-se em "os métodos de
// SQL cru são proibidos no código de produção"; se cmd/api pudesse usá-los sem
// o portão notar, aquela decisão passaria a ser falsa em silêncio. cmd/ está
// limpo hoje — é justamente quando vale prender.
func varrerCodigo(t *testing.T, fn func(path, content string)) {
	t.Helper()
	for _, raiz := range []string{internalRoot, cmdRoot} {
		walkGoFiles(t, raiz, fn)
	}
}

// modulePath é o caminho do módulo, para casar importações reais (com aspas)
// e não menções em comentário.
const modulePath = "github.com/brunorblanck/homefinance/backend"

func importPath(pkg string) string {
	return `"` + modulePath + "/internal/" + pkg + `"`
}

func walkGoFiles(t *testing.T, root string, fn func(path, content string)) {
	t.Helper()

	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fn(filepath.ToSlash(path), string(raw))
		return nil
	}))
}

// O portão precisa ALCANÇAR cmd/, e este teste é o que prova.
//
// Sem ele, tirar cmd/ da varredura não quebraria nada — os dois portões abaixo
// passam de qualquer jeito enquanto cmd/ estiver limpo, e a regressão só
// apareceria no dia em que alguém escrevesse SQL cru lá. Um portão cuja
// cobertura ninguém verifica é um portão que encolhe em silêncio.
func TestVarreduraAlcancaInternalECmd(t *testing.T) {
	t.Parallel()

	vistos := map[string]bool{}
	varrerCodigo(t, func(path, _ string) {
		switch {
		case strings.Contains(path, "/internal/"):
			vistos["internal"] = true
		case strings.Contains(path, "/cmd/"):
			vistos["cmd"] = true
		}
	})

	assert.True(t, vistos["internal"], "a varredura tem de alcançar internal/")
	assert.True(t, vistos["cmd"], "a varredura tem de alcançar cmd/: é o que sustenta a omissão do processador Raw em storage/ctxerr.go")
}

// Critério de aceite 7 da spec 0001 e fronteira do ADR-008: *gorm.DB só pode
// aparecer sob internal/platform/storage/. Este teste é o grep automatizado —
// se alguém vazar GORM para um service ou handler, o build quebra.
func TestGormNaoVazaParaForaDoStorage(t *testing.T) {
	t.Parallel()

	var violacoes []string
	varrerCodigo(t, func(path, content string) {
		if strings.Contains(path, "/platform/storage/") {
			return
		}
		if strings.Contains(content, "gorm.io/gorm") || strings.Contains(content, "gorm.DB") {
			violacoes = append(violacoes, path)
		}
	})

	assert.Empty(t, violacoes, "GORM só pode existir em internal/platform/storage/")
}

// Critério de aceite 6: nada de Raw/Exec, e nada de string montada em
// Where/Order/Select/Table (docs/SEGURANCA.md §3 — com GORM esses pontos
// continuam sendo injeção).
//
// CUIDADO — outro controle DEPENDE deste. `internal/platform/storage/ctxerr.go`
// deixa o processador `Raw` de fora do embrulho de erro de contexto, e a
// omissão só é segura porque este teste garante que não existe caminho de
// requisição por `Raw`/`Exec` em `internal/`. Afrouxar a expressão abaixo, ou
// abrir exceção para um arquivo, reabre lá um buraco que não aparece aqui: o
// erro do driver volta como `interrupted (9)` e para de ser reconhecido como
// prazo estourado. Mudou este teste? Leia o comentário do `Raw` em ctxerr.go
// antes.
func TestSemSQLMontadoNoGormstore(t *testing.T) {
	t.Parallel()

	proibidos := []*regexp.Regexp{
		regexp.MustCompile(`\.Raw\(`),
		regexp.MustCompile(`\.Exec\(`),
		// fmt.Sprintf dentro de Where/Order/Select/Table/Joins/Having.
		regexp.MustCompile(`\.(Where|Order|Select|Table|Joins|Having|Group)\([^)\n]*fmt\.Sprintf`),
		// Concatenação de string dentro dos mesmos construtores.
		regexp.MustCompile(`\.(Where|Order|Select|Table|Joins|Having|Group)\("[^"]*"\s*\+`),
		// Interpolação com variável antes do fechamento.
		regexp.MustCompile(`\.(Where|Order|Select|Table|Joins|Having|Group)\([a-zA-Z_][a-zA-Z0-9_.]*\s*\+`),
	}

	var violacoes []string
	varrerCodigo(t, func(path, content string) {
		if strings.HasSuffix(path, "_test.go") {
			return
		}
		for _, linha := range strings.Split(content, "\n") {
			for _, re := range proibidos {
				if re.MatchString(linha) {
					violacoes = append(violacoes, path+": "+strings.TrimSpace(linha))
				}
			}
		}
	})

	assert.Empty(t, violacoes, "SQL montado por concatenação é injeção, mesmo com GORM")
}

// Regra de dependência da §2 da spec 0001: os pacotes de domínio não importam
// internal/platform/storage nem internal/platform/config.
//
// Exceção consciente e registrada: os handlers HTTP dos domínios (auth e
// user) importam internal/platform/httpserver, que é o kit de borda
// (decodificação, envelope de erro, cliente IP). Não há ciclo: httpserver
// nunca importa um domínio — o middleware de autenticação recebe a interface
// Authenticator.
func TestDominiosNaoImportamPersistenciaNemConfig(t *testing.T) {
	t.Parallel()

	dominios := []string{"audit", "auth", "household", "user", "session", "id"}
	proibidos := []string{
		importPath("platform/storage"),
		importPath("platform/config"),
		importPath("platform/mailer"),
	}

	var violacoes []string
	for _, dom := range dominios {
		walkGoFiles(t, filepath.Join(internalRoot, dom), func(path, content string) {
			if strings.HasSuffix(path, "_test.go") {
				return
			}
			for _, p := range proibidos {
				if strings.Contains(content, p) {
					violacoes = append(violacoes, path+" importa "+p)
				}
			}
		})
	}

	assert.Empty(t, violacoes)
}

// O pacote httpserver não pode importar domínio: é o que quebra o ciclo
// auth -> httpserver -> auth.
func TestHttpserverNaoImportaDominioDeNegocio(t *testing.T) {
	t.Parallel()

	proibidos := []string{
		importPath("auth"),
		importPath("user"),
		importPath("household"),
		importPath("audit"),
	}

	var violacoes []string
	walkGoFiles(t, filepath.Join(internalRoot, "platform", "httpserver"), func(path, content string) {
		if strings.HasSuffix(path, "_test.go") {
			return
		}
		for _, p := range proibidos {
			if strings.Contains(content, p) {
				violacoes = append(violacoes, path+" importa "+p)
			}
		}
	})

	assert.Empty(t, violacoes)
}

// docs/SEGURANCA.md: math/rand é proibido em qualquer caminho de segurança.
func TestSemMathRandNoProjeto(t *testing.T) {
	t.Parallel()

	var violacoes []string
	walkGoFiles(t, internalRoot, func(path, content string) {
		if strings.HasSuffix(path, "_test.go") {
			return
		}
		if strings.Contains(content, `"math/rand"`) || strings.Contains(content, `"math/rand/v2"`) {
			violacoes = append(violacoes, path)
		}
	})

	assert.Empty(t, violacoes, "use crypto/rand")
}

// ADR-005/ADR-008: bibliotecas proibidas não podem reaparecer.
func TestSemDependenciasProibidas(t *testing.T) {
	t.Parallel()

	proibidas := []string{
		`"github.com/dgrijalva/jwt-go`, // abandonada
		`"github.com/jmoiron/sqlx`,     // superada pelo ADR-008
		`"github.com/pressly/goose`,    // superada pelo ADR-008
		`"github.com/go-chi/chi`,       // ADR-001: roteador nativo
	}

	var violacoes []string
	walkGoFiles(t, internalRoot, func(path, content string) {
		if strings.HasSuffix(path, "_test.go") {
			return
		}
		for _, p := range proibidas {
			if strings.Contains(content, p) {
				violacoes = append(violacoes, path+" usa "+p)
			}
		}
	})

	assert.Empty(t, violacoes)
}
