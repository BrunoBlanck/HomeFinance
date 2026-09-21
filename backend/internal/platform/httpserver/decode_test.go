package httpserver_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type payload struct {
	Nome  string `json:"nome"`
	Idade int    `json:"idade"`
}

func postJSON(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestDecodeJSONFeliz(t *testing.T) {
	t.Parallel()

	got, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), postJSON(`{"nome":"bruno","idade":40}`))
	require.NoError(t, err)
	assert.Equal(t, payload{Nome: "bruno", Idade: 40}, got)
}

// Critério de aceite 25 da spec 0001.
func TestDecodeJSONRejeitaCampoDesconhecido(t *testing.T) {
	t.Parallel()

	_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), postJSON(`{"nome":"b","admin":true}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, httpserver.ErrMalformedBody)

	rec := httptest.NewRecorder()
	httpserver.WriteDecodeError(rec, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDecodeJSONRejeitaContentTypeErrado(t *testing.T) {
	t.Parallel()

	casos := []string{"", "text/plain", "application/x-www-form-urlencoded", "lixo/(("}
	for _, ct := range casos {
		r := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader(`{}`))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), r)
		require.Error(t, err, "content-type %q deveria ser rejeitado", ct)
		assert.ErrorIs(t, err, httpserver.ErrUnsupportedMediaType)

		rec := httptest.NewRecorder()
		httpserver.WriteDecodeError(rec, err)
		assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
	}
}

func TestDecodeJSONAceitaCharsetNoContentType(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader(`{"nome":"x"}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), r)
	assert.NoError(t, err)
}

func TestDecodeJSONRejeitaCorpoVazio(t *testing.T) {
	t.Parallel()

	_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), postJSON(``))
	require.Error(t, err)
	assert.ErrorIs(t, err, httpserver.ErrMalformedBody)
}

func TestDecodeJSONRejeitaMaisDeUmValor(t *testing.T) {
	t.Parallel()

	_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), postJSON(`{"nome":"a"}{"nome":"b"}`))
	require.Error(t, err)
	assert.ErrorIs(t, err, httpserver.ErrMalformedBody)
}

// Critério de aceite 25: corpo acima do limite vira 413.
func TestDecodeJSONRespeitaMaxBytes(t *testing.T) {
	t.Parallel()

	grande := `{"nome":"` + strings.Repeat("a", 5000) + `"}`
	var capturado error
	h := httpserver.Chain(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, capturado = httpserver.DecodeJSON[payload](w, r)
			httpserver.WriteDecodeError(w, capturado)
		}),
		httpserver.MaxBytes(512),
	)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, postJSON(grande))

	require.Error(t, capturado)
	assert.ErrorIs(t, capturado, httpserver.ErrPayloadTooLarge)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	assert.Contains(t, rec.Body.String(), httpserver.CodePayloadTooLarge)
}

func TestDecodeJSONNaoEcoaDetalheInterno(t *testing.T) {
	t.Parallel()

	_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), postJSON(`{"idade":"texto"}`))
	require.Error(t, err)

	rec := httptest.NewRecorder()
	httpserver.WriteDecodeError(rec, err)
	body, _ := io.ReadAll(rec.Body)
	assert.NotContains(t, string(body), "json:")
	assert.NotContains(t, string(body), "payload")
	assert.Contains(t, string(body), httpserver.MsgValidationFailed)
}

// --- nome do campo na caixa exata (spec 0005 §13) ---------------------------

// aninhado exercita o que o DisallowUnknownFields sozinho não alcança: objeto
// dentro de lista, ponteiro para struct, tipo com UnmarshalJSON próprio e
// campo sem tag.
type aninhado struct {
	Itens    []item          `json:"itens"`
	Extra    *item           `json:"extra"`
	Livre    map[string]item `json:"livre"`
	Proprio  proprio         `json:"proprio"`
	SemTag   string
	Ignorado string `json:"-"`
}

type item struct {
	RowID      string  `json:"rowId"`
	CategoryID *string `json:"categoryId"`
}

// proprio decide sozinho como se decodificar: a conferência de nomes para
// nele, como pára em civil.Date e nos tri-estados de PATCH.
type proprio struct {
	Bruto string
}

func (p *proprio) UnmarshalJSON(b []byte) error {
	p.Bruto = string(b)
	return nil
}

// O achado do QA: `{"Nome":…}` casava com `nome` porque o encoding/json ignora
// a caixa. Agora é corpo malformado — 400 genérico, sem ecoar o nome recebido.
func TestDecodeJSONRejeitaNomeDeCampoEmOutraCaixa(t *testing.T) {
	t.Parallel()

	for _, corpo := range []string{
		`{"Nome":"b"}`,
		`{"NOME":"b"}`,
		`{"nOmE":"b"}`,
		`{"nome":"b","Idade":40}`,
	} {
		_, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(), postJSON(corpo))
		require.Error(t, err, "corpo %s deveria ser rejeitado", corpo)
		assert.ErrorIs(t, err, httpserver.ErrMalformedBody)

		rec := httptest.NewRecorder()
		httpserver.WriteDecodeError(rec, err)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.NotContains(t, rec.Body.String(), "Nome", "a resposta não ecoa o nome recebido")
	}
}

// A conferência é recursiva: o mesmo furo existia no objeto aninhado e no item
// de lista — que é onde moram as decisões do confirm da importação.
func TestDecodeJSONRejeitaNomeEmOutraCaixaEmCampoAninhado(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"dentro da lista":    `{"itens":[{"RowId":"a"}]}`,
		"no objeto opcional": `{"extra":{"rowId":"a","CategoryId":"c"}}`,
		"no valor do mapa":   `{"livre":{"qualquer":{"RowId":"a"}}}`,
		"campo sem tag":      `{"semtag":"x"}`,
	}
	for nome, corpo := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			_, err := httpserver.DecodeJSON[aninhado](httptest.NewRecorder(), postJSON(corpo))
			require.Error(t, err)
			assert.ErrorIs(t, err, httpserver.ErrMalformedBody)
		})
	}
}

// E o que é legítimo continua entrando: nome exato em todos os níveis, chave
// de MAPA livre (é dado, não nome de campo), campo sem tag pelo nome Go, e o
// tipo que decodifica a si mesmo recebendo o objeto inteiro sem conferência.
func TestDecodeJSONAceitaOContratoInteiroNaCaixaExata(t *testing.T) {
	t.Parallel()

	corpo := `{"itens":[{"rowId":"a","categoryId":"c"}],` +
		`"extra":{"rowId":"b","categoryId":null},` +
		`"livre":{"Qualquer Chave":{"rowId":"d"}},` +
		`"proprio":{"QualquerCoisa":1},` +
		`"SemTag":"x"}`

	got, err := httpserver.DecodeJSON[aninhado](httptest.NewRecorder(), postJSON(corpo))
	require.NoError(t, err)
	require.Len(t, got.Itens, 1)
	assert.Equal(t, "a", got.Itens[0].RowID)
	require.NotNil(t, got.Extra)
	assert.Equal(t, "b", got.Extra.RowID)
	assert.Contains(t, got.Livre, "Qualquer Chave")
	assert.Equal(t, `{"QualquerCoisa":1}`, got.Proprio.Bruto)
	assert.Equal(t, "x", got.SemTag)
}

// `json:"-"` não é nome de campo: mandar "Ignorado" ou "-" continua sendo
// corpo malformado, e nada disso chega ao destino.
func TestDecodeJSONNaoAceitaCampoMarcadoComoIgnorado(t *testing.T) {
	t.Parallel()

	for _, corpo := range []string{`{"Ignorado":"x"}`, `{"-":"x"}`, `{"ignorado":"x"}`} {
		_, err := httpserver.DecodeJSON[aninhado](httptest.NewRecorder(), postJSON(corpo))
		require.Error(t, err, "corpo %s deveria ser rejeitado", corpo)
		assert.ErrorIs(t, err, httpserver.ErrMalformedBody)
	}
}

// Chave REPETIDA não é atalho para escapar da conferência (achado M2 da
// revisão de segurança de 17/09/2026).
//
// O encoding/json processa TODAS as ocorrências da mesma chave e deixa cada uma
// escrever no destino — só a última "vence" o que as duas disputam. Uma
// conferência feita sobre `map[string]json.RawMessage` só veria a última, e a
// primeira entraria com o nome em qualquer caixa.
func TestDecodeJSONConfereTodasAsOcorrenciasDaChaveRepetida(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"lista com nome torto na PRIMEIRA ocorrência": `{"itens":[{"RowId":"x"}],"itens":[]}`,
		"objeto com nome torto na primeira":           `{"extra":{"CategoryId":"x"},"extra":{"rowId":"b"}}`,
		"chave repetida dentro do mapa":               `{"livre":{"a":{"RowId":"x"},"a":{"rowId":"b"}}}`,
		"nome torto na ÚLTIMA ocorrência":             `{"itens":[],"itens":[{"RowId":"x"}]}`,
	}
	for nome, corpo := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			_, err := httpserver.DecodeJSON[aninhado](httptest.NewRecorder(), postJSON(corpo))
			require.Error(t, err, "corpo %s deveria ser rejeitado", corpo)
			assert.ErrorIs(t, err, httpserver.ErrMalformedBody)
		})
	}
}

// E a repetição com os nomes CERTOS continua valendo o que sempre valeu: a
// última ocorrência vence, como no encoding/json. A conferência de nomes não
// inventa uma regra nova sobre repetição — quem decide isso é o contrato, e
// mudá-lo seria outra decisão.
func TestDecodeJSONMantemUltimaOcorrenciaQuandoOsNomesEstaoCertos(t *testing.T) {
	t.Parallel()

	got, err := httpserver.DecodeJSON[payload](httptest.NewRecorder(),
		postJSON(`{"nome":"primeiro","nome":"ultimo","idade":1,"idade":2}`))
	require.NoError(t, err)
	assert.Equal(t, payload{Nome: "ultimo", Idade: 2}, got)
}

// Teto de TOKENS da varredura: um corpo com dezenas de milhares de objetos
// minúsculos é 400 ANTES de ser decodificado (achados M1 e A1). O corpo
// legítimo mais largo do contrato — as 2.000 decisões do confirm — passa com
// folga.
func TestDecodeJSONRecusaCorpoComEstruturaDemais(t *testing.T) {
	t.Parallel()

	var abusivo strings.Builder
	abusivo.WriteString(`{"itens":[`)
	for i := 0; abusivo.Len() < 1<<20; i++ {
		if i > 0 {
			abusivo.WriteByte(',')
		}
		abusivo.WriteString(`{"rowId":"a"}`)
	}
	abusivo.WriteString(`]}`)

	_, err := httpserver.DecodeJSON[aninhado](httptest.NewRecorder(), postJSON(abusivo.String()))
	require.Error(t, err)
	assert.ErrorIs(t, err, httpserver.ErrMalformedBody)
	rec := httptest.NewRecorder()
	httpserver.WriteDecodeError(rec, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var legitimo strings.Builder
	legitimo.WriteString(`{"itens":[`)
	for i := range 2000 {
		if i > 0 {
			legitimo.WriteByte(',')
		}
		legitimo.WriteString(`{"rowId":"a","categoryId":"c"}`)
	}
	legitimo.WriteString(`]}`)

	got, err := httpserver.DecodeJSON[aninhado](httptest.NewRecorder(), postJSON(legitimo.String()))
	require.NoError(t, err, "o maior corpo legítimo do contrato passa")
	assert.Len(t, got.Itens, 2000)
}

// Corpo que NÃO tem nome nenhum para conferir não pode custar por token
// (achado A1 da revisão de segurança de 17/09/2026): `{"nome":[1,1,1,…]}` num
// campo de texto era percorrido token a token, com uma alocação por token, em
// rotas PÚBLICAS de autenticação. A varredura pula esse valor com o scanner do
// encoding/json, e o custo dela deixa de acompanhar o tamanho do corpo.
//
// O teto é absoluto e folgado de propósito: o que ele precisa provar é a
// ordem de grandeza (dezenas, não centenas de milhares), não um número exato,
// que mudaria com a versão do Go.
func TestDecodeJSONNaoPagaPorTokenAoPularValorQueNaoTemNome(t *testing.T) {
	// Sem t.Parallel(): testing.AllocsPerRun não pode rodar em teste paralelo.

	var corpo strings.Builder
	corpo.WriteString(`{"nome":[`)
	for i := range 50_000 {
		if i > 0 {
			corpo.WriteByte(',')
		}
		corpo.WriteByte('1')
	}
	corpo.WriteString(`]}`)
	texto := corpo.String()

	alocacoes := testing.AllocsPerRun(1, func() {
		// O corpo é recusado pelo TIPO do campo (lista onde cabe texto), que é
		// a resposta de sempre; o que este teste mede é o CUSTO até lá.
		_, _ = httpserver.DecodeJSON[payload](httptest.NewRecorder(), postJSON(texto))
	})
	assert.Less(t, alocacoes, 1_000.0, "pular um valor sem nomes não pode alocar por token")
}

// A outra metade do A1: o corpo cujos objetos não têm chave NENHUMA passava por
// baixo de um teto que só contasse nomes. O teto conta tokens, então este corpo
// é 400 — e não "passa sem conferir".
func TestDecodeJSONRecusaListaGiganteDeItensSemChave(t *testing.T) {
	t.Parallel()

	for nome, item := range map[string]string{
		"objetos vazios": `{}`,
		"escalares":      `1`,
	} {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			var corpo strings.Builder
			corpo.WriteString(`{"itens":[`)
			for i := 0; corpo.Len() < 1<<20; i++ {
				if i > 0 {
					corpo.WriteByte(',')
				}
				corpo.WriteString(item)
			}
			corpo.WriteString(`]}`)

			_, err := httpserver.DecodeJSON[aninhado](httptest.NewRecorder(), postJSON(corpo.String()))
			require.Error(t, err)
			assert.ErrorIs(t, err, httpserver.ErrMalformedBody)
		})
	}
}
