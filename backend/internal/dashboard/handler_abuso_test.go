package dashboard_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/dashboard"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/platform/logging"
	"github.com/brunorblanck/homefinance/backend/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Abuso de GET /dashboard — critérios 13, 14 e 16 da spec 0008 §8.
//
// O que estes testes provam, e que os de contrato (handler_test.go) não
// alcançam:
//
//   - 13: a recusa do mês é por FORMA e nada é normalizado. A prova não é só
//     "deu 400": é que o servidor não aceita uma string PARECIDA com um mês
//     válido nem devolve um mês diferente do que foi pedido;
//   - 14: sem sessão não é só 401 — é 401 SEM tocar em consulta nenhuma. A
//     prova é um dublê que FALHA o teste ao ser chamado, e não um
//     `assert.Empty` em cima de um contador, que passaria igual se o contador
//     deixasse de ser incrementado;
//   - 16: `householdId=` não é "ignorado" por não aparecer na resposta — é
//     inerte. A prova é a resposta BYTE A BYTE idêntica à do mesmo pedido sem
//     o parâmetro.

// --- dublês PROIBIDOS (critério 14) ---------------------------------------

// Os três dublês abaixo não devolvem zeros: eles FALHAM o teste. É a diferença
// entre "nenhuma consulta foi registrada" e "nenhuma consulta aconteceu" — a
// primeira sobrevive a um contador que ninguém incrementa mais.

type ledgerProibido struct{ t *testing.T }

func (l ledgerProibido) SumMonthByKindAndAccount(_ context.Context, householdID, competenceMonth string,
	_ []string,
) ([]dashboard.KindAccountTotals, error) {
	l.t.Helper()
	l.t.Errorf("a agregação do painel foi ao banco sem sessão (casa=%q, mês=%q)", householdID, competenceMonth)
	return nil, nil
}

type categoriasProibidas struct{ t *testing.T }

func (c categoriasProibidas) List(_ context.Context, householdID string, _ bool) ([]category.Category, error) {
	c.t.Helper()
	c.t.Errorf("a taxonomia foi lida sem sessão (casa=%q)", householdID)
	return nil, nil
}

type contasProibidas struct{ t *testing.T }

func (c contasProibidas) List(_ context.Context, householdID string, _ bool) ([]account.Account, error) {
	c.t.Helper()
	c.t.Errorf("as contas foram lidas sem sessão (casa=%q)", householdID)
	return nil, nil
}

// handlerProibido monta a rota sobre dublês que falham ao serem chamados.
func handlerProibido(t *testing.T) *dashboard.Handler {
	t.Helper()
	lg := logging.Discard()
	svc := dashboard.NewService(ledgerProibido{t}, categoriasProibidas{t}, contasProibidas{t}, lg)
	return dashboard.NewHandler(svc, lg)
}

// --- critério 13: a forma do mês -------------------------------------------

// Nenhuma destas strings é mês, e nenhuma é NORMALIZADA para virar um:
// espaço não é aparado, `2026-9` não ganha zero à esquerda, `2026-13` não
// vira `2027-01`. Todas são 400 em `fields.month`, com a MESMA redação de
// GET /reports/by-category, e NENHUMA consulta é emitida.
//
// A lista vai além dos quatro casos literais da spec §8.13 porque a recusa é
// por FORMA: se o validador aceitasse qualquer coisa de 7 bytes, `2026/09`
// passaria, e a tela receberia um mês que o banco nunca viu.
func TestQAMesMalformadoEh400SemNormalizarNada(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		// --- os quatro da spec §8.13 -------------------------------------
		"ausente":             "/api/v1/dashboard",
		"mes 13":              "/api/v1/dashboard?month=2026-13",
		"espaco a frente":     "/api/v1/dashboard?month=%202026-09",
		"sem zero a esquerda": "/api/v1/dashboard?month=2026-9",

		// --- vizinhos que um aparador de espaços ou um parse frouxo deixariam passar ---
		"vazio":            "/api/v1/dashboard?month=",
		"espaco atras":     "/api/v1/dashboard?month=2026-09%20",
		"mes 00":           "/api/v1/dashboard?month=2026-00",
		"mes 99":           "/api/v1/dashboard?month=2026-99",
		"separador errado": "/api/v1/dashboard?month=2026/09",
		"sublinhado":       "/api/v1/dashboard?month=2026_09",
		"com dia":          "/api/v1/dashboard?month=2026-09-01",
		"sinal no ano":     "/api/v1/dashboard?month=%2B026-09",
		"sinal no mes":     "/api/v1/dashboard?month=2026-%2B9",
		"ano nao numerico": "/api/v1/dashboard?month=abcd-09",
		"ano zero":         "/api/v1/dashboard?month=0000-01",
		// Dígitos de largura plena (U+FF10 U+FF19): parecem "09" na tela e
		// não são dígitos ASCII. Um parse por "converta o que parecer número"
		// os aceitaria.
		"unicode que parece digito": "/api/v1/dashboard?month=2026-%EF%BC%90%EF%BC%99",
		"emoji":                     "/api/v1/dashboard?month=2026-%F0%9F%99%82",
		"nulo no meio":              "/api/v1/dashboard?month=2026-%000",
		"SQL":                       "/api/v1/dashboard?month=2026-09%27%3B+DROP+TABLE+transactions%3B--",
		"HTML":                      "/api/v1/dashboard?month=%3Cscript%3Ealert(1)%3C%2Fscript%3E",
		"gigante":                   "/api/v1/dashboard?month=" + longa(4096),
	}

	for nome, alvo := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			a := novoHTTPAmbiente(t)
			a.casaComum()

			rec := a.chamar(t, minhaCasa, alvo)
			require.Equal(t, http.StatusBadRequest, rec.Code, "corpo: %s", rec.Body.String())

			var env struct {
				Error struct {
					Code   string            `json:"code"`
					Fields map[string]string `json:"fields"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), "corpo: %s", rec.Body.String())
			assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
			assert.Equal(t, "Informe o mês no formato AAAA-MM.", env.Error.Fields["month"],
				"a MESMA redação de /reports/by-category — a recusa não ensina nada sobre o servidor")

			// A recusa não devolve o que foi mandado: eco de entrada numa
			// mensagem de erro é o vetor do XSS refletido.
			assert.NotContains(t, rec.Body.String(), "script")
			assert.NotContains(t, rec.Body.String(), "DROP")

			// E, sobretudo: nada foi ao banco.
			assert.Empty(t, a.ledger.chamadas, "mês malformado não pode chegar à consulta")
			assert.Empty(t, a.cats.chamadas)
			assert.Empty(t, a.contas.chamadas)
		})
	}
}

// O outro lado da mesma moeda: o mês VÁLIDO volta exatamente como veio, e é
// exatamente ele que chega à consulta. Sem este teste, "nada é normalizado"
// seria uma afirmação só sobre as recusas.
//
// A lista atravessa a virada do ano (2026-12 → 2027-01) e as duas pontas da
// janela de sanidade de datas do projeto (1970 e 2099).
func TestQAMesValidoVoltaExatamenteComoVeio(t *testing.T) {
	t.Parallel()

	for _, m := range []string{"2026-01", "2026-09", "2026-12", "2027-01", "1970-01", "2099-12", "0001-01", "9999-12"} {
		t.Run(m, func(t *testing.T) {
			t.Parallel()
			a := novoHTTPAmbiente(t)
			a.casaComum()

			rec := a.chamar(t, minhaCasa, "/api/v1/dashboard?month="+m)
			require.Equal(t, http.StatusOK, rec.Code, "corpo: %s", rec.Body.String())

			var corpo struct {
				Month string `json:"month"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
			assert.Equal(t, m, corpo.Month, "o mês pedido volta de volta, sem normalização")

			require.Len(t, a.ledger.chamadas, 1)
			assert.Equal(t, m, a.ledger.chamadas[0].mes, "e é ele que vai ao WHERE")
		})
	}
}

// Chave `month` REPETIDA na query é URL AMBÍGUA, e ambiguidade é 400.
//
// A regra é a da emenda §12.5.5 da spec 0004 (E2d) para `kindGroup` em
// GET /transactions, herdada aqui por httpserver.SoleQueryValue: `Get`
// devolveria o PRIMEIRO valor e descartaria o resto em silêncio, e o caminho
// de uma requisição tem leitores que não são obrigados a concordar sobre qual
// ocorrência vale (proxy, WAF, log, cache) — a tela afirmaria um mês e o
// intermediário registraria outro.
//
// A recusa é da AMBIGUIDADE, não da discordância: dois valores IGUAIS e uma
// segunda ocorrência VAZIA também são 400. Perguntar "mas eles concordam?"
// seria emitir uma segunda opinião sobre uma URL que já é ambígua. E a
// resposta não ecoa nenhum dos valores nem diz quantos chegaram.
//
// Histórico: este teste nasceu documentando o comportamento anterior (200 com
// o primeiro valor, "não inventa mês") e o achado foi corrigido na mesma
// entrega, no T4 — o nome ficou, porque a garantia original continua valendo:
// o servidor nunca publica nem consulta um mês que não foi pedido.
func TestQAChaveMonthRepetidaNaoInventaMes(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"valido e depois invalido": "/api/v1/dashboard?month=2026-09&month=2026-13",
		"invalido e depois valido": "/api/v1/dashboard?month=2026-13&month=2026-09",
		"dois validos diferentes":  "/api/v1/dashboard?month=2026-09&month=2026-10",
		"dois validos iguais":      "/api/v1/dashboard?month=2026-09&month=2026-09",
		"vazio e depois valido":    "/api/v1/dashboard?month=&month=2026-09",
		"valido e depois vazio":    "/api/v1/dashboard?month=2026-09&month=",
		"tres ocorrencias":         "/api/v1/dashboard?month=2026-09&month=2026-09&month=2026-09",
		"com parametro alheio":     "/api/v1/dashboard?month=2026-09&householdId=x&month=2026-10",
	}

	for nome, alvo := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			a := novoHTTPAmbiente(t)
			a.casaComum()

			rec := a.chamar(t, minhaCasa, alvo)
			require.Equal(t, http.StatusBadRequest, rec.Code, "corpo: %s", rec.Body.String())

			var env struct {
				Error struct {
					Code   string            `json:"code"`
					Fields map[string]string `json:"fields"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), "corpo: %s", rec.Body.String())
			assert.Equal(t, httpserver.CodeValidationFailed, env.Error.Code)
			assert.Equal(t, "Informe o mês uma única vez.", env.Error.Fields["month"],
				"a redação da chave REPETIDA aponta a ação; não é a do mês malformado")

			// Nenhum dos valores volta na resposta: ambos são entrada bruta.
			assert.NotContains(t, rec.Body.String(), "2026-1")
			assert.NotContains(t, rec.Body.String(), "2026-09")

			// E nada foi ao banco — nem a taxonomia, nem as contas.
			assert.Empty(t, a.ledger.chamadas, "URL ambígua não pode chegar à consulta")
			assert.Empty(t, a.cats.chamadas)
			assert.Empty(t, a.contas.chamadas)
		})
	}
}

// O outro lado da regra: UMA ocorrência continua exatamente como era. Válida
// é 200 com o mesmo corpo de antes; ausente e vazia continuam 400 pela redação
// do mês MALFORMADO, não pela da chave repetida — a adoção do helper não pode
// ter mudado nada além do caso repetido.
func TestQAChaveMonthUnicaContinuaComoAntes(t *testing.T) {
	t.Parallel()

	t.Run("uma ocorrencia valida e 200 identico", func(t *testing.T) {
		t.Parallel()
		a := novoHTTPAmbiente(t)
		a.casaComum()
		a.ledger.rows = linhasDoMesComum()

		rec := a.chamar(t, minhaCasa, "/api/v1/dashboard?month=2026-09")
		require.Equal(t, http.StatusOK, rec.Code, "corpo: %s", rec.Body.String())

		var corpo struct {
			Month string `json:"month"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo))
		assert.Equal(t, "2026-09", corpo.Month)
		require.Len(t, a.ledger.chamadas, 1)
		assert.Equal(t, "2026-09", a.ledger.chamadas[0].mes)
	})

	for nome, alvo := range map[string]string{
		"ausente":         "/api/v1/dashboard",
		"uma vazia":       "/api/v1/dashboard?month=",
		"uma malformada":  "/api/v1/dashboard?month=2026-13",
		"outra chave dup": "/api/v1/dashboard?month=2026-13&householdId=a&householdId=b",
	} {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			a := novoHTTPAmbiente(t)
			a.casaComum()

			rec := a.chamar(t, minhaCasa, alvo)
			require.Equal(t, http.StatusBadRequest, rec.Code, "corpo: %s", rec.Body.String())

			var env struct {
				Error struct {
					Fields map[string]string `json:"fields"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env))
			assert.Equal(t, "Informe o mês no formato AAAA-MM.", env.Error.Fields["month"],
				"uma ocorrência só nunca é 'repetida' — a redação é a do formato")
			assert.Empty(t, a.ledger.chamadas)
		})
	}
}

// --- critério 14: sem sessão, nenhuma consulta -----------------------------

// 401 e NENHUMA consulta — provado por dublê que FALHA o teste ao ser
// chamado, nas três formas de "sem sessão" que chegariam ao serviço como casa
// desconhecida.
func TestQASemSessaoEh401ENenhumaConsultaEhEmitida(t *testing.T) {
	t.Parallel()

	casos := map[string]*session.Identity{
		"sem identidade no contexto": nil,
		"identidade com casa vazia":  {UserID: usuario, HouseholdID: "", Role: "owner", SessionID: "s"},
		"identidade inteira vazia":   {},
	}

	for nome, ident := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			h := handlerProibido(t)

			r := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard?month="+mes, nil)
			if ident != nil {
				r = r.WithContext(session.NewContext(r.Context(), *ident))
			}
			rec := httptest.NewRecorder()
			h.Summary(rec, r)

			require.Equal(t, http.StatusUnauthorized, rec.Code, "corpo: %s", rec.Body.String())

			var env struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &env), "corpo: %s", rec.Body.String())
			assert.Equal(t, httpserver.CodeUnauthenticated, env.Error.Code)
			assert.Equal(t, httpserver.MsgUnauthenticated, env.Error.Message)

			// 401 não vaza número nenhum: nem zerado, nem em branco.
			assert.NotContains(t, rec.Body.String(), "incomeCents")
			assert.NotContains(t, rec.Body.String(), "investmentNetCents")
		})
	}
}

// Sem sessão o mês nem chega a ser validado: 401 vem ANTES do 400. A ordem
// importa — responder 400 a quem não tem sessão diria "seu mês está errado" a
// um anônimo, o que é um oráculo de forma de parâmetro de graça.
func TestQASemSessaoOMesNemEhValidado(t *testing.T) {
	t.Parallel()
	h := handlerProibido(t)

	r := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard?month=2026-13", nil)
	rec := httptest.NewRecorder()
	h.Summary(rec, r)

	assert.Equal(t, http.StatusUnauthorized, rec.Code, "401 antes de 400")
	assert.NotContains(t, rec.Body.String(), "month")
}

// --- critério 16: `householdId=` é inerte -----------------------------------

// A resposta é BYTE A BYTE a mesma com e sem o parâmetro — que é mais forte do
// que "o id da outra casa não aparece no corpo". O ledger também recebe
// exatamente os mesmos argumentos nas duas chamadas.
func TestQAParametrosAlheiosSaoInertesByteAByte(t *testing.T) {
	t.Parallel()

	// Um por linha, e todos juntos no fim: nomes plausíveis, variações de
	// caixa e as formas que um framework poderia desembrulhar (`household_id`,
	// `household[id]`).
	sujeiras := map[string]string{
		"householdId":        "&householdId=" + outraCasa,
		"household_id":       "&household_id=" + outraCasa,
		"householdID":        "&householdID=" + outraCasa,
		"household":          "&household=" + outraCasa,
		"household aninhado": "&household%5Bid%5D=" + outraCasa,
		"accountId":          "&accountId=conta-da-outra",
		"categoryId":         "&categoryId=cat-da-outra",
		"kind":               "&kind=transfer_in",
		"includeArchived":    "&includeArchived=false",
		"limit":              "&limit=0",
		"SQL no householdId": "&householdId=%27+OR+1%3D1+--",
		"HTML no accountId":  "&accountId=%3Cimg+src%3Dx+onerror%3Dalert(1)%3E",
		"tudo junto": "&householdId=" + outraCasa + "&household_id=" + outraCasa +
			"&accountId=conta-da-outra&categoryId=cat-da-outra&kind=transfer_in&limit=0",
	}

	// A resposta de referência: o MESMO cenário, sem sujeira nenhuma.
	base := novoHTTPAmbiente(t)
	base.casaComum()
	base.conta(outraCasa, "conta-da-outra", account.KindCreditCard, false)
	base.categoria(outraCasa, "cat-da-outra", category.KindInvestment, false)
	base.ledger.rows = linhasDoMesComum()
	recBase := base.chamar(t, minhaCasa, "/api/v1/dashboard?month="+mes)
	require.Equal(t, http.StatusOK, recBase.Code)
	referencia := recBase.Body.String()

	for nome, sujeira := range sujeiras {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			a := novoHTTPAmbiente(t)
			a.casaComum()
			a.conta(outraCasa, "conta-da-outra", account.KindCreditCard, false)
			a.categoria(outraCasa, "cat-da-outra", category.KindInvestment, false)
			a.ledger.rows = linhasDoMesComum()

			rec := a.chamar(t, minhaCasa, "/api/v1/dashboard?month="+mes+sujeira)
			require.Equal(t, http.StatusOK, rec.Code, "corpo: %s", rec.Body.String())
			assert.Equal(t, referencia, rec.Body.String(),
				"o parâmetro é inerte: a resposta é byte a byte a mesma")

			// E a consulta também: mesma casa, mesmo mês, mesmo conjunto.
			require.Len(t, a.ledger.chamadas, 1)
			assert.Equal(t, minhaCasa, a.ledger.chamadas[0].casa, "a casa vem do TOKEN")
			assert.Equal(t, mes, a.ledger.chamadas[0].mes)
			assert.Equal(t, []string{"cat-aporte", "cat-resgate"}, a.ledger.chamadas[0].marcadas,
				"nenhum id da outra casa entra no IN (?)")

			// Nem o id, nem o nome da conta/categoria da outra casa aparecem.
			corpo := rec.Body.String()
			assert.NotContains(t, corpo, outraCasa)
			assert.NotContains(t, corpo, "conta-da-outra")
			assert.NotContains(t, corpo, "cat-da-outra")
			// E nada do que veio na query é refletido no corpo.
			assert.NotContains(t, corpo, "onerror")
			assert.NotContains(t, corpo, "OR 1=1")
		})
	}
}

// Um `householdId=` com a casa do PRÓPRIO token também não muda nada: o
// parâmetro não é "aceito quando bate" — ele simplesmente não existe.
func TestQAHouseholdIdDaPropriaCasaTambemEhInerte(t *testing.T) {
	t.Parallel()

	a := novoHTTPAmbiente(t)
	a.casaComum()
	a.ledger.rows = linhasDoMesComum()
	comParam := a.chamar(t, minhaCasa, "/api/v1/dashboard?month="+mes+"&householdId="+minhaCasa)
	require.Equal(t, http.StatusOK, comParam.Code)

	b := novoHTTPAmbiente(t)
	b.casaComum()
	b.ledger.rows = linhasDoMesComum()
	semParam := b.chamar(t, minhaCasa, "/api/v1/dashboard?month="+mes)
	require.Equal(t, http.StatusOK, semParam.Code)

	assert.Equal(t, semParam.Body.String(), comParam.Body.String())
}

// --- métodos e superfície --------------------------------------------------

// O handler é leitura pura: um verbo inesperado roteado até ele por engano não
// escreve nada e não muda a resposta. A rota real casa só GET
// (routes_test.go); este teste guarda o handler em si.
func TestQAHandlerNaoEscreveEmMetodoInesperado(t *testing.T) {
	t.Parallel()

	for _, metodo := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(metodo, func(t *testing.T) {
			t.Parallel()
			a := novoHTTPAmbiente(t)
			a.casaComum()
			a.ledger.rows = linhasDoMesComum()

			r := httptest.NewRequest(metodo, "/api/v1/dashboard?month="+mes, nil)
			r = r.WithContext(session.NewContext(r.Context(), session.Identity{
				UserID: usuario, HouseholdID: minhaCasa, Role: "owner", SessionID: "sess-1",
			}))
			rec := httptest.NewRecorder()
			a.handler.Summary(rec, r)

			assert.Equal(t, http.StatusOK, rec.Code, "leitura pura: o método não muda o efeito")
			assert.Len(t, a.ledger.chamadas, 1, "e continua sendo UMA consulta")
		})
	}
}

// --- apoio -----------------------------------------------------------------

// linhasDoMesComum é o mesmo mês em todos os cenários de abuso: um mês com os
// três números diferentes de zero, para uma resposta alterada ser visível.
func linhasDoMesComum() []dashboard.KindAccountTotals {
	return []dashboard.KindAccountTotals{
		{Kind: "income", AccountID: contaCorrente, Count: 4, TotalCents: 535_000,
			MarkedCount: 1, MarkedTotalCents: 35_000},
		{Kind: "expense", AccountID: cartao, Count: 3, TotalCents: 280_000,
			MarkedCount: 1, MarkedTotalCents: 200_000},
	}
}

// longa devolve uma string de `n` bytes 'a' — entrada grande sem custo de
// fixture.
func longa(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
