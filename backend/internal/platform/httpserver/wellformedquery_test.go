package httpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WellFormedQuery é o que torna `r.URL.Query()` confiável no projeto inteiro.
//
// A mecânica é medida AQUI, sobre um handler de mentira, para não depender de
// rota nenhuma; os casos de ponta a ponta com os parâmetros reais estão em
// internal/report/query_malformada_test.go, internal/transaction e
// internal/account, e a FIAÇÃO (posição na cadeia) em
// cmd/api/wellformedquery_wiring_test.go.

// espiao registra se o handler protegido chegou a ser chamado e com que query
// ele o foi. É por ele que "a requisição malformada não chega ao handler"
// deixa de ser inferência e vira medida.
type espiao struct {
	chamado bool
	query   url.Values
}

func (e *espiao) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.chamado = true
	e.query = r.URL.Query()
	w.WriteHeader(http.StatusOK)
}

func chamarComQuery(t *testing.T, bruta string) (*httptest.ResponseRecorder, *espiao) {
	t.Helper()

	alvo := "/api/v1/reports/by-category"
	if bruta != "" {
		alvo += "?" + bruta
	}
	// httptest.NewRequest parseia a URL; RawQuery é setada à mão para que a
	// query chegue EXATAMENTE como o cliente a escreveu, sem reescrita.
	r := httptest.NewRequest(http.MethodGet, alvo, nil)
	r.URL.RawQuery = bruta

	e := &espiao{}
	rec := httptest.NewRecorder()
	httpserver.WellFormedQuery()(e).ServeHTTP(rec, r)
	return rec, e
}

// A MEDIDA do defeito, primeiro: sem a guarda, `r.URL.Query()` devolve o par
// ilegível PULADO — e é isso que faz "o cliente mandou um valor" virar "a
// chave não existe", que quase toda borda lê como "sem filtro".
//
// Este teste não usa o middleware: ele tranca o comportamento da stdlib de que
// a correção depende. Se um Go futuro passar a abortar (ou a aceitar) esses
// casos, é aqui que se descobre.
func TestOParserDaStdlibPulaOParIlegivelEmSilencio(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		bruta    string
		esperado []string
	}{
		"ponto e vírgula cru":         {"month=2026-09&accountGroup=credit;debit", nil},
		"percent solto":               {"month=2026-09&accountGroup=credit%", nil},
		"escape percentual inválido":  {"month=2026-09&accountGroup=cre%zzdit", nil},
		"segunda ocorrência quebrada": {"month=2026-09&accountGroup=credit&accountGroup=debit%", []string{"credit"}},
		"chave quebrada":              {"month=2026-09&account%Group=credit", nil},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			r := httptest.NewRequest(http.MethodGet, "/x", nil)
			r.URL.RawQuery = c.bruta
			assert.Equal(t, c.esperado, r.URL.Query()["accountGroup"],
				"é este silêncio que a guarda da cadeia existe para acabar")
		})
	}
}

func TestQueryMalformadaEh400SemCamposESemEco(t *testing.T) {
	t.Parallel()

	brutas := map[string]string{
		"ponto e vírgula cru":         "month=2026-09&accountGroup=credit;debit",
		"percent solto no fim":        "month=2026-09&accountGroup=credit%",
		"escape percentual inválido":  "month=2026-09&accountGroup=cre%zzdit",
		"segunda ocorrência quebrada": "month=2026-09&accountGroup=credit&accountGroup=debit%",
		"chave quebrada":              "month=2026-09&account%Group=credit",
		"só o lixo":                   "%zz",
	}
	for nome, bruta := range brutas {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			rec, e := chamarComQuery(t, bruta)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.False(t, e.chamado, "a requisição malformada não chega ao handler")

			var env httpserver.ErrorEnvelope
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
			assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
			assert.Equal(t, httpserver.MsgValidationFailed, env.Error.Message)
			assert.Nil(t, env.Error.Fields,
				"sem `fields`: o que está malformado é a query inteira, e nomear um campo seria inventar qual")

			// Nada da entrada volta, e nada do texto de erro da stdlib
			// (`invalid URL escape "%zz"`, `invalid semicolon separator`)
			// atravessa — ele carrega o escape recebido.
			corpo := rec.Body.String()
			assert.NotContains(t, corpo, ";")
			assert.NotContains(t, corpo, "%zz")
			assert.NotContains(t, corpo, "credit")
			assert.NotContains(t, corpo, "invalid")
			assert.NotContains(t, corpo, "escape")
			assert.NotContains(t, corpo, "semicolon")
		})
	}
}

func TestQueryValidaOuAusentePassaIntacta(t *testing.T) {
	t.Parallel()

	t.Run("sem query", func(t *testing.T) {
		t.Parallel()
		rec, e := chamarComQuery(t, "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, e.chamado)
		assert.Empty(t, e.query)
	})

	validas := map[string]string{
		"parâmetros normais":        "month=2026-09&accountGroup=credit",
		"percent-encoding legítimo": "month=2026-09&accountGroup=credit%3Bdebit",
		"chave repetida":            "month=2026-09&accountGroup=credit&accountGroup=debit",
		"valor vazio":               "month=2026-09&accountGroup=",
		"chave sem igual":           "month=2026-09&accountGroup",
		"mais no lugar de espaço":   "q=a+b",
	}
	for nome, bruta := range validas {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			rec, e := chamarComQuery(t, bruta)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.True(t, e.chamado, "query bem formada é assunto da borda da rota, não desta guarda")
		})
	}
}

// `%3B` é o ponto e vírgula ESCAPADO, e escapado ele é query bem formada: o
// valor que chega à borda é a string literal "credit;debit". A guarda não pode
// roubar esse caso — ele pertence à allowlist da rota, que o recusa com o
// campo certo. É a diferença entre "não consigo ler o que você mandou" e "li e
// não aceito".
func TestPontoEVirgulaESCAPADOChegaInteiroAoHandler(t *testing.T) {
	t.Parallel()

	rec, e := chamarComQuery(t, "month=2026-09&accountGroup=credit%3Bdebit")
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, e.chamado)
	assert.Equal(t, "credit;debit", e.query.Get("accountGroup"))
}
