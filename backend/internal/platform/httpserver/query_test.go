package httpserver_test

import (
	"net/url"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// SoleQueryValue é o mecanismo central contra HTTP Parameter Pollution
// (spec 0004 §12.5.5, ADR-030). Ele é testado AQUI, com a query bruta parseada
// de verdade, para que a regra não dependa de como cada rota monta o
// url.Values — é essa independência que faz a próxima rota herdar a regra em
// vez de reinventá-la.

// parse devolve a query de uma URL como o net/http a entrega ao handler.
func parse(t *testing.T, bruta string) url.Values {
	t.Helper()
	u, err := url.Parse("/x?" + bruta)
	require.NoError(t, err)
	return u.Query()
}

func TestSoleQueryValueAceitaAusenteEUmaUnicaOcorrencia(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome     string
		query    string
		chave    string
		esperado string
	}{
		{"chave ausente", "outra=1", "tipo", ""},
		{"uma ocorrência com valor", "tipo=expense", "tipo", "expense"},
		// UMA ocorrência vazia não é ambígua: ela continua significando o que
		// o parâmetro define para o valor vazio (em kindGroup, "Tudo").
		{"uma ocorrência vazia", "tipo=", "tipo", ""},
		{"query vazia", "", "tipo", ""},
		// Outra chave repetida não contamina a que está sendo lida.
		{"repetição em OUTRA chave", "outra=1&outra=2&tipo=income", "tipo", "income"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			v, err := httpserver.SoleQueryValue(parse(t, c.query), c.chave)
			require.NoError(t, err)
			assert.Equal(t, c.esperado, v)
		})
	}
}

// A regra é "a chave aparece no máximo uma vez" — sobre a AMBIGUIDADE DA URL,
// nunca sobre os valores concordarem. Por isso os valores iguais e a segunda
// ocorrência vazia também são erro.
func TestSoleQueryValueRecusaChaveRepetida(t *testing.T) {
	t.Parallel()

	casos := []struct{ nome, query string }{
		{"valores diferentes", "tipo=expense&tipo=income"},
		{"valores iguais", "tipo=expense&tipo=expense"},
		{"segunda vazia", "tipo=expense&tipo="},
		{"primeira vazia", "tipo=&tipo=expense"},
		{"as duas vazias", "tipo=&tipo="},
		{"três ocorrências", "tipo=a&tipo=b&tipo=c"},
		{"repetição separada por outra chave", "tipo=expense&outra=1&tipo=income"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			v, err := httpserver.SoleQueryValue(parse(t, c.query), "tipo")
			require.ErrorIs(t, err, httpserver.ErrRepeatedQueryParam)
			assert.Empty(t, v, "nada parcial volta de uma chave ambígua")
			// O erro não carrega o nome da chave nem nenhum dos valores: quem
			// chama sabe o campo, e o valor recusado é entrada bruta de
			// terceiro (S8).
			assert.NotContains(t, err.Error(), "tipo=")
			assert.NotContains(t, err.Error(), "expense")
		})
	}
}

// A chave é comparada BYTE A BYTE, como o net/http a entrega: `tipo` e `Tipo`
// são chaves diferentes, e a repetição de uma não é repetição da outra. Sem
// isto, "recusar repetição" viraria uma segunda opinião sobre quais chaves são
// "a mesma" — exatamente a ambiguidade que o helper existe para não ter.
func TestSoleQueryValueDistingueCaixaDaChave(t *testing.T) {
	t.Parallel()

	q := parse(t, "tipo=expense&Tipo=income&Tipo=transfer")

	v, err := httpserver.SoleQueryValue(q, "tipo")
	require.NoError(t, err)
	assert.Equal(t, "expense", v)

	_, err = httpserver.SoleQueryValue(q, "Tipo")
	require.ErrorIs(t, err, httpserver.ErrRepeatedQueryParam)
}
