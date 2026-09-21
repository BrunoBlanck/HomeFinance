package importer_test

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/csvtext"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Casos de ABUSO da importação, exercitados pela borda HTTP (critérios de
// aceite 10 a 13 da spec 0004).
//
// A régua destes testes é hostil de propósito: eles não perguntam "funciona?",
// perguntam "o que um atacante tentaria, e o que acontece?". Falha de
// autorização aqui é defeito CRÍTICO, e o corpo do 404 tem de ser idêntico byte
// a byte ao de um id que não existe — um 404 "diferente" para recurso alheio é
// um oráculo de existência, e enumerar ids alheios é o primeiro passo.

// --- BOLA ------------------------------------------------------------------

const idInexistente = "00000000-0000-7000-dead-000000000001"

// TestBOLAContaDeOutraCasaNoEnvioEhIdenticaAContaInexistente cobre o
// `accountId` da fase 1.
func TestBOLAContaDeOutraCasaNoEnvioEhIdenticaAContaInexistente(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	// A conta É real: só pertence à outra casa. Id inventado também dá 404,
	// mas provar isso não prova isolamento nenhum.
	alheia := a.conta(t, a.alheia.ID, "Conta Alheia", account.KindChecking, "nubank")

	comAlheia := enviarPelaAPI(t, a, alheia.ID, "e.csv", fixtureExtrato(t))
	comFantasma := enviarPelaAPI(t, a, idInexistente, "e.csv", fixtureExtrato(t))

	require.Equal(t, http.StatusNotFound, comAlheia.Code)
	require.Equal(t, http.StatusNotFound, comFantasma.Code)
	assert.Equal(t, comFantasma.Body.Bytes(), comAlheia.Body.Bytes(),
		"byte a byte: a resposta não pode dizer que a conta alheia existe")

	// E nada foi gravado em lugar nenhum.
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
	assert.Empty(t, a.lancamentosDa(t, a.alheia.ID, "2026-08"))
}

// TestBOLACategoriaDeOutraCasaNoConfirmEhIdenticaACategoriaInexistente cobre os
// DOIS caminhos de categoria do confirm: a da linha e o padrão do lote.
func TestBOLACategoriaDeOutraCasaNoConfirmEhIdenticaACategoriaInexistente(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	// Categoria REAL, criada na outra casa pelo serviço de verdade.
	alheia, err := a.categoriaSvc.Create(t.Context(), category.Actor{
		HouseholdID: a.alheia.ID, UserID: a.outroUsuario.ID,
	}, category.CreateInput{Name: "Alimentação", Kind: category.KindExpense})
	require.NoError(t, err)

	t.Run("categoria da linha", func(t *testing.T) {
		lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "e.csv", fixtureExtrato(t)))
		linha := revisarPelaAPI(t, a, lote.ID).Items[0]

		corpo := func(catID string) string {
			return fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"import","categoryId":%q}]}`,
				linha.ID, catID)
		}
		comAlheia := confirmarPelaAPI(t, a, lote.ID, corpo(alheia.ID))
		comFantasma := confirmarPelaAPI(t, a, lote.ID, corpo(idInexistente))

		require.Equal(t, http.StatusNotFound, comAlheia.Code, comAlheia.Body.String())
		assert.Equal(t, comFantasma.Body.Bytes(), comAlheia.Body.Bytes())
		assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), "tudo ou nada: nada entrou")
	})

	t.Run("categoria padrão do lote", func(t *testing.T) {
		lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "e2.csv", fixtureExtrato(t)))

		comAlheia := confirmarPelaAPI(t, a, lote.ID,
			fmt.Sprintf(`{"decisions":[],"defaultCategoryId":%q}`, alheia.ID))
		comFantasma := confirmarPelaAPI(t, a, lote.ID,
			fmt.Sprintf(`{"decisions":[],"defaultCategoryId":%q}`, idInexistente))

		require.Equal(t, http.StatusNotFound, comAlheia.Code, comAlheia.Body.String())
		assert.Equal(t, comFantasma.Body.Bytes(), comAlheia.Body.Bytes())
		assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
	})

	// E a categoria alheia continua intacta na casa dela.
	viva, err := a.categoriaSvc.List(t.Context(), category.Actor{
		HouseholdID: a.alheia.ID, UserID: a.outroUsuario.ID,
	}, category.ListInput{})
	require.NoError(t, err)
	assert.NotEmpty(t, viva.Expense)
}

// TestBOLAContrapartidaDeOutraCasaEhIdenticaAContaInexistente é o vetor mais
// fácil de esquecer: a transferência cria uma linha numa conta DIFERENTE da
// conta do lote — é a única escrita desta entrega que sai da conta de destino.
func TestBOLAContrapartidaDeOutraCasaEhIdenticaAContaInexistente(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	cartaoAlheio := a.conta(t, a.alheia.ID, "Cartão Alheio", account.KindCreditCard, "nubank")

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "e.csv", fixtureExtrato(t)))
	pagamento := linhaComDescricao(t, revisarPelaAPI(t, a, lote.ID).Items, "Pagamento de fatura")

	corpo := func(contrapartida string) string {
		return fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}]}`,
			pagamento.ID, contrapartida)
	}
	comAlheia := confirmarPelaAPI(t, a, lote.ID, corpo(cartaoAlheio.ID))
	comFantasma := confirmarPelaAPI(t, a, lote.ID, corpo(idInexistente))

	require.Equal(t, http.StatusNotFound, comAlheia.Code, comAlheia.Body.String())
	assert.Equal(t, comFantasma.Body.Bytes(), comAlheia.Body.Bytes())

	// A prova que importa: NENHUMA linha nasceu na casa alheia.
	assert.Empty(t, a.lancamentosDa(t, a.alheia.ID, "2026-08"),
		"escrever na conta de outra casa seria o pior defeito possível aqui")
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"), "e o lote inteiro volta atrás")
}

// TestBOLAFaturaDeOutraCasaEhIdenticaAFaturaInexistente cobre o `statementId`
// da decisão de transferência.
func TestBOLAFaturaDeOutraCasaEhIdenticaAFaturaInexistente(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	cartao := a.contaCartao(t)

	// Fatura REAL da outra casa, gravada pelo repositório de verdade.
	cartaoAlheio := a.conta(t, a.alheia.ID, "Cartão Alheio", account.KindCreditCard, "nubank")
	faturaAlheia := &cardstatement.Statement{
		ID:              a.proximoID("00000000-0000-7000-c100"),
		HouseholdID:     a.alheia.ID,
		AccountID:       cartaoAlheio.ID,
		CompetenceMonth: "2026-09",
		ClosingDate:     civil.MustNew(2026, 8, 31),
		DueDate:         civil.MustNew(2026, 9, 10),
		Source:          cardstatement.SourceImport,
		CreatedAt:       a.relogio.now(),
		UpdatedAt:       a.relogio.now(),
	}
	require.NoError(t, a.repoFatura.Upsert(t.Context(), faturaAlheia))

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "e.csv", fixtureExtrato(t)))
	pagamento := linhaComDescricao(t, revisarPelaAPI(t, a, lote.ID).Items, "Pagamento de fatura")

	corpo := func(faturaID string) string {
		return fmt.Sprintf(
			`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q,"statementId":%q}]}`,
			pagamento.ID, cartao.ID, faturaID)
	}
	comAlheia := confirmarPelaAPI(t, a, lote.ID, corpo(faturaAlheia.ID))
	comFantasma := confirmarPelaAPI(t, a, lote.ID, corpo(idInexistente))

	require.Equal(t, http.StatusNotFound, comAlheia.Code, comAlheia.Body.String())
	assert.Equal(t, comFantasma.Body.Bytes(), comAlheia.Body.Bytes())
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
	assert.Empty(t, a.lancamentosDa(t, a.alheia.ID, "2026-09"))
}

// TestBOLALinhaDeOutroLoteResponde400ENuncaEmSilencio: `rowId` de outro lote é
// 400. Ignorar em silêncio faria o cliente acreditar que a decisão valeu.
func TestBOLALinhaDeOutroLoteResponde400ENuncaEmSilencio(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	loteA := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "A.csv", fixtureExtrato(t)))
	loteB := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "B.csv", fixtureExtrato(t)))
	linhaDeA := revisarPelaAPI(t, a, loteA.ID).Items[0]

	casos := map[string]string{
		"linha de outro lote da mesma casa": linhaDeA.ID,
		"linha que não existe":              idInexistente,
		"linha vazia":                       "",
	}
	for nome, rowID := range casos {
		t.Run(nome, func(t *testing.T) {
			rec := confirmarPelaAPI(t, a, loteB.ID,
				fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"skip"}]}`, rowID))
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "VALIDATION_FAILED")
		})
	}

	// O lote A continua pendente e íntegro: nenhuma das tentativas o tocou.
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, loteA.ID).Batch.Status)
}

// --- mass assignment (§6.7) ------------------------------------------------

// TestMassAssignmentNoConfirmEhRecusadoCampoACampo enumera os campos que o
// cliente tentaria injetar. NENHUM deles existe em DTO de entrada; mandá-los é
// 400, jamais "ignorado".
func TestMassAssignmentNoConfirmEhRecusadoCampoACampo(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "e.csv", fixtureExtrato(t)))
	linha := revisarPelaAPI(t, a, lote.ID).Items[0]

	naRaiz := map[string]string{
		"householdId": `"householdId":"casa-alheia"`,
		"accountId":   `"accountId":"outra-conta"`,
		"status":      `"status":"committed"`,
		"imported":    `"imported":999`,
	}
	for nome, campo := range naRaiz {
		t.Run("raiz/"+nome, func(t *testing.T) {
			rec := confirmarPelaAPI(t, a, lote.ID, `{"decisions":[],`+campo+`}`)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}

	naDecisao := map[string]string{
		"amountCents":     `"amountCents":1`,
		"dedupKey":        `"dedupKey":"aaaa"`,
		"dedupOrdinal":    `"dedupOrdinal":9`,
		"occurredOn":      `"occurredOn":"2026-01-01"`,
		"description":     `"description":"forjada"`,
		"descriptionNorm": `"descriptionNorm":"forjada"`,
		"kind":            `"kind":"income"`,
		"yearMonth":       `"yearMonth":"2026-01"`,
		"competenceMonth": `"competenceMonth":"2026-01"`,
		"source":          `"source":"manual"`,
		"createdBy":       `"createdBy":"outro-usuario"`,
		"deletedAt":       `"deletedAt":null`,
		"externalId":      `"externalId":"forjado"`,
		"householdId":     `"householdId":"casa-alheia"`,
	}
	for nome, campo := range naDecisao {
		t.Run("decisao/"+nome, func(t *testing.T) {
			corpo := fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"import",%s}]}`, linha.ID, campo)
			rec := confirmarPelaAPI(t, a, lote.ID, corpo)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}

	noStatement := map[string]string{
		"id":        `"statement":{"id":"forjado","competenceMonth":"2026-09","closingDate":"2026-08-31","dueDate":"2026-09-10"}`,
		"accountId": `"statement":{"accountId":"outra","competenceMonth":"2026-09","closingDate":"2026-08-31","dueDate":"2026-09-10"}`,
		"source":    `"statement":{"source":"manual","competenceMonth":"2026-09","closingDate":"2026-08-31","dueDate":"2026-09-10"}`,
	}
	for nome, campo := range noStatement {
		t.Run("statement/"+nome, func(t *testing.T) {
			rec := confirmarPelaAPI(t, a, lote.ID, `{"decisions":[],`+campo+`}`)
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}

	// Depois de todas as tentativas, o lote continua pendente e o banco vazio.
	assert.Equal(t, importer.BatchStatusPending, revisarPelaAPI(t, a, lote.ID).Batch.Status)
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
}

// TestMassAssignmentNoMultipartEhRecusadoParteAParte: parte fora da allowlist
// de quatro é 400.
func TestMassAssignmentNoMultipartEhRecusadoParteAParte(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	foraDaAllowlist := []string{
		"householdId", "dedupKey", "dedupOrdinal", "amountCents",
		"institution", "docKind", "formatId", "rowCount", "status",
		"contentSha256", "createdBy", "expiresAt",
	}
	for _, nome := range foraDaAllowlist {
		t.Run(nome, func(t *testing.T) {
			rec := enviarPelaAPI(t, a, conta.ID, "e.csv", fixtureExtrato(t),
				parte{nome: nome, conteudo: []byte("valor forjado")})
			assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "VALIDATION_FAILED")
		})
	}
}

// --- entrada (critério 11) -------------------------------------------------

// TestEntradaHostilNaBordaDaImportacao junta os casos de forma do pedido.
func TestEntradaHostilNaBordaDaImportacao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "e.csv", fixtureExtrato(t)))

	t.Run("content-type json em /imports é 415", func(t *testing.T) {
		r := requisicao(t, http.MethodPost, "/api/v1/imports",
			strings.NewReader(`{"accountId":"x"}`), identidade(a.casa.ID, a.usuario.ID))
		r.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		a.handler(t).Create(rec, r)
		assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
	})

	t.Run("content-type ausente é 415", func(t *testing.T) {
		r := requisicao(t, http.MethodPost, "/api/v1/imports",
			strings.NewReader("qualquer coisa"), identidade(a.casa.ID, a.usuario.ID))
		rec := httptest.NewRecorder()
		a.handler(t).Create(rec, r)
		assert.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
	})

	// multipart/form-data SEM o parâmetro `boundary` responde 400, e não 415.
	// É defensável e está registrado aqui de propósito: o tipo de mídia É o
	// suportado (por isso não é 415), e o que falha é a FORMA do corpo, que é
	// 400. O que não pode acontecer — e é o que o teste vigia — é 500 ou, pior,
	// o servidor tentar adivinhar um boundary.
	t.Run("multipart sem boundary é 400", func(t *testing.T) {
		r := requisicao(t, http.MethodPost, "/api/v1/imports",
			strings.NewReader("qualquer coisa"), identidade(a.casa.ID, a.usuario.ID))
		r.Header.Set("Content-Type", "multipart/form-data")
		rec := httptest.NewRecorder()
		a.handler(t).Create(rec, r)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "VALIDATION_FAILED")
	})

	t.Run("arquivo de 9 MiB é 413", func(t *testing.T) {
		// 9 MiB: um mebibyte acima do teto declarado da rota. É o número do
		// critério de aceite 11, e não uma aproximação dele.
		gigante := bytes.Repeat([]byte("a"), 9<<20)
		require.Greater(t, len(gigante), importer.MaxUploadBytes)

		rec := enviarPelaAPI(t, a, conta.ID, "grande.csv", gigante)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "PAYLOAD_TOO_LARGE")
	})

	t.Run("cursor forjado na revisão é 400 sem detalhe", func(t *testing.T) {
		forjados := []string{
			"1 OR 1=1",
			`"; DROP TABLE import_rows--`,
			"'; DELETE FROM transactions; --",
			"-1",
			"0",
			"1e9",
			"999999999999999999999",
			strings.Repeat("9", 5_000),
		}
		for _, forjado := range forjados {
			r := requisicao(t, http.MethodGet,
				"/api/v1/imports/"+lote.ID+"?cursor="+url.QueryEscape(forjado), nil,
				identidade(a.casa.ID, a.usuario.ID))
			r.SetPathValue("id", lote.ID)
			rec := httptest.NewRecorder()
			a.handler(t).Get(rec, r)

			require.Equal(t, http.StatusBadRequest, rec.Code, "cursor %q", forjado)
			corpo := rec.Body.String()
			assert.NotContains(t, corpo, forjado, "o 400 não ecoa o cursor forjado (S5)")
			assert.NotContains(t, strings.ToLower(corpo), "sql")
			assert.NotContains(t, strings.ToLower(corpo), "import_rows")
		}
	})

	t.Run("limit fora da faixa é recusado", func(t *testing.T) {
		for _, limite := range []string{"10000", "201", "0", "-1", "abc", "1.5"} {
			r := requisicao(t, http.MethodGet,
				"/api/v1/imports/"+lote.ID+"?limit="+limite, nil,
				identidade(a.casa.ID, a.usuario.ID))
			r.SetPathValue("id", lote.ID)
			rec := httptest.NewRecorder()
			a.handler(t).Get(rec, r)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "limit=%s", limite)
		}
	})

	t.Run("month inválido em /transactions é 400", func(t *testing.T) {
		for _, mes := range []string{"2026-13", "abc", "2026-00", "2026-8", "202608", "2026-08-01", ""} {
			r := requisicao(t, http.MethodGet, "/api/v1/transactions?month="+mes, nil,
				identidade(a.casa.ID, a.usuario.ID))
			rec := httptest.NewRecorder()
			a.handlerLancamento().List(rec, r)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "month=%s", mes)
		}
	})
}

// --- conteúdo hostil (critério 12) ----------------------------------------

// TestConteudoHostilEhRecusadoPelaBorda: os três tetos do parser, medidos pela
// resposta HTTP e não pelo erro interno.
func TestConteudoHostilEhRecusadoPelaBorda(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	cabecalho := "Data,Valor,Identificador,Descrição\n"

	t.Run("20.000 linhas", func(t *testing.T) {
		var b bytes.Buffer
		b.WriteString(cabecalho)
		for i := range 20_000 {
			fmt.Fprintf(&b, "04/08/2026,-1.00,11111111-1111-4111-8111-%012d,Compra Exemplo\n", i)
		}
		require.Greater(t, 20_000, csvtext.MaxRows)

		rec := enviarPelaAPI(t, a, conta.ID, "muitas.csv", b.Bytes())
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "IMPORT_FILE_REJECTED")
		assert.Contains(t, rec.Body.String(), `"rows"`, "o campo diz QUAL limite estourou")
	})

	t.Run("linha de 1 MB", func(t *testing.T) {
		linha := "04/08/2026,-1.00,11111111-1111-4111-8111-111111111101," +
			strings.Repeat("x", 1<<20) + "\n"
		rec := enviarPelaAPI(t, a, conta.ID, "linhona.csv", []byte(cabecalho+linha))
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "IMPORT_FILE_REJECTED")
	})

	// 3.000 colunas são recusadas com IMPORT_FORMAT_UNKNOWN, e não com
	// IMPORT_FILE_REJECTED. O caminho é este: o teto de 16 colunas é conferido
	// DENTRO da detecção de cabeçalho (registry.headerOf), então a linha larga
	// simplesmente não é aceita como cabeçalho, nenhum parser casa, e o
	// veredito é "não sei ler este formato".
	//
	// O que importa para a segurança está garantido — o arquivo é recusado, sem
	// varrer 3.000 colunas e sem gravar nada. O que muda é a MENSAGEM da tela.
	// Fica documentado aqui de propósito: se alguém mudar o código para
	// IMPORT_FILE_REJECTED, que seja por decisão, e não por acidente.
	t.Run("3.000 colunas", func(t *testing.T) {
		largo := strings.Repeat("c,", 3_000) + "ultima\n" + strings.Repeat("v,", 3_000) + "fim\n"
		rec := enviarPelaAPI(t, a, conta.ID, "largo.csv", []byte(largo))
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "IMPORT_FORMAT_UNKNOWN")
	})

	// A variante que fecha o buraco que a de cima deixa: cabeçalho RECONHECIDO
	// (quatro colunas do Nubank) e uma linha de dados com 3.000 campos. Aqui o
	// parser já está escolhido, e quem precisa segurar é o `csv.Reader` com o
	// FieldsPerRecord fixado pelo cabeçalho.
	t.Run("cabeçalho válido com linha de 3.000 campos", func(t *testing.T) {
		linhaLarga := strings.Repeat("v,", 3_000) + "fim\n"
		rec := enviarPelaAPI(t, a, conta.ID, "larga.csv", []byte(cabecalho+linhaLarga))
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "IMPORT_FILE_REJECTED")
	})

	// Nada de nada entrou por nenhum dos três caminhos.
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
}

// TestDescricaoHostilEhArmazenadaSanitizadaETruncada é o outro lado do critério
// 12: o que NÃO é rejeitado tem de ser guardado limpo.
//
// A fórmula de planilha é guardada FIEL de propósito (§6.9): neutralizar na
// entrada alteraria o dado do usuário para proteger um programa de terceiros. A
// defesa é na exportação, e a E6 herda o teste de regressão que nasce aqui.
func TestDescricaoHostilEhArmazenadaSanitizadaETruncada(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	const (
		formula = "=SOMA(A1:A9)"
		// U+202E (RIGHT-TO-LEFT OVERRIDE) é o truque "Trojan Source": ele faria
		// a descrição EXIBIR um estabelecimento e GUARDAR outro.
		trojan = "Loja Exemplo ‮gpj.exe"
		// 500 caracteres: bem acima dos 140 da coluna.
		longa = "Estabelecimento Exemplo Com Nome Absurdamente Longo "
	)
	descricaoLonga := strings.Repeat(longa, 10)
	require.Greater(t, len(descricaoLonga), 400)

	conteudo := csvExtratoAPI(
		linhaExtrato{dia: 4, valor: "-10.00", idSufixo: "01", descricao: formula},
		linhaExtrato{dia: 5, valor: "-11.00", idSufixo: "02", descricao: trojan},
		linhaExtrato{dia: 6, valor: "-12.00", idSufixo: "03", descricao: descricaoLonga},
	)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "hostil.csv", conteudo))
	_ = resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`))

	lista := listarPelaAPI(t, a, "2026-08")
	require.Len(t, lista.Items, 3)

	porValor := map[int64]string{}
	for _, i := range lista.Items {
		porValor[i.AmountCents] = i.Description
	}

	// 1) A fórmula é guardada FIEL — a defesa é na exportação (§6.9). Este é o
	//    teste de regressão que a E6 herda: quando a exportação existir, ela
	//    precisa neutralizar ESTA linha.
	assert.Equal(t, formula, porValor[1000],
		"a descrição do usuário não é alterada na entrada; quem neutraliza é a exportação (E6)")

	// 2) O override de direção Unicode SAI.
	guardada := porValor[1100]
	assert.NotContains(t, guardada, "‮", "U+202E não pode sobreviver ao armazenamento")
	for _, r := range []rune{'‪', '‫', '‬', '‭', '⁦', '⁧', '⁨', '⁩'} {
		assert.NotContains(t, guardada, string(r))
	}
	assert.Contains(t, guardada, "Loja Exemplo")

	// 3) Truncagem em 140 RUNAS, e nunca no meio de uma.
	truncada := porValor[1200]
	assert.LessOrEqual(t, len([]rune(truncada)), 140)
	assert.NotEmpty(t, truncada)
	assert.True(t, strings.HasPrefix(descricaoLonga, truncada[:20]))

	// 4) Nenhuma descrição guardada tem caractere de controle.
	for _, i := range lista.Items {
		for _, r := range i.Description {
			assert.False(t, r < 0x20 || r == 0x7f,
				"caractere de controle %U sobreviveu em %q", r, i.Description)
		}
	}
}

// TestArquivoComByteNuloEhRecusadoInteiro documenta o comportamento REAL do
// byte nulo, que diverge da letra do critério 12.
//
// O critério diz "descrição com byte nulo é armazenada sanitizada"; a
// implementação recusa o ARQUIVO INTEIRO com IMPORT_FILE_REJECTED, porque byte
// nulo no conteúdo quer dizer "isto não é um CSV de texto" (csvtext.Decode).
// A divergência é para o lado SEGURO — recusar em vez de limpar e seguir —, mas
// é divergência, e fica registrada aqui em vez de virar surpresa depois.
func TestArquivoComByteNuloEhRecusadoInteiro(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	conteudo := csvExtratoAPI(
		linhaExtrato{dia: 4, valor: "-10.00", idSufixo: "01", descricao: "Loja\x00Exemplo"},
	)
	rec := enviarPelaAPI(t, a, conta.ID, "nulo.csv", conteudo)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "IMPORT_FILE_REJECTED")
	assert.Empty(t, a.lancamentosDa(t, a.casa.ID, "2026-08"))
}

// --- ZIP hostil (critério 9) ----------------------------------------------

// O lado do ENCRYPT do ZipCrypto existe só no teste — produção nunca cifra nada
// com ZipCrypto, que é uma cifra quebrada. Esta é uma segunda implementação,
// independente da que vive em archive/: se as duas concordarem, o algoritmo
// está certo; se discordarem, uma delas está errada, e é isso que se quer saber.
type chavesDeTeste struct{ k0, k1, k2 uint32 }

func novasChavesDeTeste(senha []byte) *chavesDeTeste {
	k := &chavesDeTeste{k0: 0x12345678, k1: 0x23456789, k2: 0x34567890}
	for _, b := range senha {
		k.avancar(b)
	}
	return k
}

func (k *chavesDeTeste) avancar(claro byte) {
	k.k0 = crc32.IEEETable[(k.k0^uint32(claro))&0xff] ^ (k.k0 >> 8)
	k.k1 = (k.k1+(k.k0&0xff))*134775813 + 1
	k.k2 = crc32.IEEETable[(k.k2^(k.k1>>24))&0xff] ^ (k.k2 >> 8)
}

func (k *chavesDeTeste) fluxo() byte {
	t := (k.k2 & 0xffff) | 2
	return byte(((t * (t ^ 1)) >> 8) & 0xff)
}

func cifrarZipCrypto(senha, claro []byte) []byte {
	k := novasChavesDeTeste(senha)
	out := make([]byte, len(claro))
	for i, c := range claro {
		out[i] = c ^ k.fluxo()
		k.avancar(c)
	}
	return out
}

// entradaZip descreve a entrada a fabricar.
type entradaZip struct {
	nome     string
	conteudo []byte
	senha    []byte // nil = sem cifra
}

// montarZip escreve um ZIP em memória, com os mesmos flags do arquivo real do
// banco: bit 3 (data descriptor) mais bit 11 (nome em UTF-8).
func montarZip(t *testing.T, entradas ...entradaZip) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	for _, e := range entradas {
		var comprimido bytes.Buffer
		fw, err := flate.NewWriter(&comprimido, flate.BestCompression)
		require.NoError(t, err)
		_, err = fw.Write(e.conteudo)
		require.NoError(t, err)
		require.NoError(t, fw.Close())

		carga := comprimido.Bytes()
		flags := uint16(0x0008 | 0x0800)
		if e.senha != nil {
			cabecalho := make([]byte, 12)
			_, err := rand.Read(cabecalho)
			require.NoError(t, err)
			carga = cifrarZipCrypto(e.senha, append(cabecalho, carga...))
			flags |= 0x0001
		}

		fh := &zip.FileHeader{
			Name:               e.nome,
			Method:             zip.Deflate,
			Flags:              flags,
			CRC32:              crc32.ChecksumIEEE(e.conteudo),
			CompressedSize64:   uint64(len(carga)),
			UncompressedSize64: uint64(len(e.conteudo)),
		}
		w, err := zw.CreateRaw(fh)
		require.NoError(t, err)
		_, err = w.Write(carga)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// senhaAleatoria gera a senha do teste. Nenhuma senha literal entra no
// repositório: mesmo uma "senha de teste" vira, com o tempo, a senha que
// alguém copiou para algum lugar de verdade.
func senhaAleatoria(t *testing.T) []byte {
	t.Helper()
	raw := make([]byte, 16)
	_, err := rand.Read(raw)
	require.NoError(t, err)
	const imprimiveis = '~' - '!' + 1
	for i := range raw {
		raw[i] = byte('!') + raw[i]%imprimiveis
	}
	return raw
}

// TestZipPeloHTTPRespondeOCodigoCertoParaCadaCaso é o critério 9 medido na
// borda: o que importa é o CÓDIGO que chega à tela, porque cada um leva a uma
// ação diferente da interface.
func TestZipPeloHTTPRespondeOCodigoCertoParaCadaCaso(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	senha := senhaAleatoria(t)

	cifrado := montarZip(t, entradaZip{nome: "extrato.csv", conteudo: fixtureExtrato(t), senha: senha})

	t.Run("cifrado sem senha é IMPORT_PASSWORD_REQUIRED", func(t *testing.T) {
		rec := enviarPelaAPI(t, a, conta.ID, "extrato.zip", cifrado)
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "IMPORT_PASSWORD_REQUIRED")
		// A tela precisa ABRIR o campo de senha; "arquivo corrompido" a faria
		// mandar o usuário procurar outro arquivo.
		assert.NotContains(t, rec.Body.String(), "IMPORT_FILE_REJECTED")
		assert.NotContains(t, strings.ToLower(rec.Body.String()), "corromp")
	})

	t.Run("senha errada é IMPORT_PASSWORD_INVALID", func(t *testing.T) {
		outra := senhaAleatoria(t)
		rec := enviarPelaAPI(t, a, conta.ID, "extrato.zip", cifrado,
			parte{nome: importer.PartPassword, conteudo: outra})
		require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
		assert.Contains(t, rec.Body.String(), "IMPORT_PASSWORD_INVALID")
	})

	t.Run("senha certa importa", func(t *testing.T) {
		rec := enviarPelaAPI(t, a, conta.ID, "extrato.zip", cifrado,
			parte{nome: importer.PartPassword, conteudo: append([]byte(nil), senha...)})
		lote := loteDaResposta(t, rec)
		assert.Equal(t, 13, lote.RowCount)
		assert.Equal(t, "nubank.checking.v1", lote.FormatID)
	})

	rejeitados := map[string][]byte{
		"duas entradas": montarZip(t,
			entradaZip{nome: "a.csv", conteudo: fixtureExtrato(t)},
			entradaZip{nome: "b.csv", conteudo: fixtureExtrato(t)}),
		"entrada que não é csv": montarZip(t,
			entradaZip{nome: "extrato.txt", conteudo: fixtureExtrato(t)}),
		"zip aninhado": montarZip(t,
			entradaZip{nome: "dentro.csv", conteudo: montarZip(t,
				entradaZip{nome: "extrato.csv", conteudo: fixtureExtrato(t)})}),
		"nome com travessia": montarZip(t,
			entradaZip{nome: "../x.csv", conteudo: fixtureExtrato(t)}),
		"nome com barra invertida": montarZip(t,
			entradaZip{nome: `..\x.csv`, conteudo: fixtureExtrato(t)}),
		"nome absoluto": montarZip(t,
			entradaZip{nome: "/etc/x.csv", conteudo: fixtureExtrato(t)}),
		"zip vazio":        montarZip(t),
		"bomba de 200:1":   bombaDeCompressao(t),
		"zip que não abre": append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0xAB}, 512)...),
	}
	for nome, conteudo := range rejeitados {
		t.Run(nome, func(t *testing.T) {
			rec := enviarPelaAPI(t, a, conta.ID, "hostil.zip", conteudo)
			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), "IMPORT_FILE_REJECTED")
		})
	}
}

// bombaDeCompressao monta um ZIP cuja razão descomprimido:comprimido passa de
// 200:1 e cujo volume absoluto passa do piso de 64 KiB.
func bombaDeCompressao(t *testing.T) []byte {
	t.Helper()
	// 4 MiB de um byte só comprimem para alguns KiB — razão muito acima de 200.
	return montarZip(t, entradaZip{nome: "bomba.csv", conteudo: bytes.Repeat([]byte("A"), 4<<20)})
}

// TestZipHostilNaoEscreveNadaEmDisco observa o diretório temporário do processo
// durante a bateria inteira de ZIPs hostis.
//
// Não é paralelo: mexe em variável de ambiente do processo.
func TestZipHostilNaoEscreveNadaEmDisco(t *testing.T) {
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	temporario := t.TempDir()
	t.Setenv("TMPDIR", temporario)
	t.Setenv("TMP", temporario)
	t.Setenv("TEMP", temporario)
	require.Equal(t, temporario, filepath.Clean(os.TempDir()))

	senha := senhaAleatoria(t)
	hostis := [][]byte{
		montarZip(t, entradaZip{nome: "extrato.csv", conteudo: fixtureExtrato(t), senha: senha}),
		montarZip(t, entradaZip{nome: "../x.csv", conteudo: fixtureExtrato(t)}),
		montarZip(t,
			entradaZip{nome: "a.csv", conteudo: fixtureExtrato(t)},
			entradaZip{nome: "b.csv", conteudo: fixtureExtrato(t)}),
		bombaDeCompressao(t),
		montarZip(t, entradaZip{nome: "extrato.csv", conteudo: fixtureExtrato(t)}),
	}
	for _, conteudo := range hostis {
		rec := enviarPelaAPI(t, a, conta.ID, "hostil.zip", conteudo)
		require.NotEqual(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	}

	sobraram, err := os.ReadDir(temporario)
	require.NoError(t, err)
	assert.Empty(t, sobraram,
		"extração é 100%% em memória: o que seria gravado aqui é extrato bancário")
}

// --- vazamento (critério 13) ----------------------------------------------

// TestSenhaUsadaDeVerdadeNaoVazaEmLugarNenhum é a versão forte do critério 13.
//
// O teste que já existia usa a senha num CSV solto, onde ela é IGNORADA. Este
// usa a senha num ZIP de verdade, no caminho em que ela é derivada em chaves,
// aplicada ao fluxo e conferida contra o CRC — e confere os quatro lugares: a
// resposta, o log, a auditoria e o banco.
func TestSenhaUsadaDeVerdadeNaoVazaEmLugarNenhum(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	senha := senhaAleatoria(t)
	textoDaSenha := string(senha)
	cifrado := montarZip(t, entradaZip{nome: "extrato.csv", conteudo: fixtureExtrato(t), senha: senha})

	var log bytes.Buffer
	handler := importer.NewHandler(a.svc, slog.New(slog.NewJSONHandler(&log, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})), 0)

	enviarComSenha := func(t *testing.T, usar []byte) *httptest.ResponseRecorder {
		t.Helper()
		corpo, tipo := montarMultipart(t,
			parte{nome: importer.PartFile, arquivo: "extrato.zip", conteudo: cifrado},
			parte{nome: importer.PartAccountID, conteudo: []byte(conta.ID)},
			parte{nome: importer.PartPassword, conteudo: usar},
		)
		r := requisicao(t, http.MethodPost, "/api/v1/imports", corpo, identidade(a.casa.ID, a.usuario.ID))
		r.Header.Set("Content-Type", tipo)
		rec := httptest.NewRecorder()
		handler.Create(rec, r)
		return rec
	}

	// Caminho feliz E caminho de erro: a senha errada é a que mais tenta vazar,
	// porque ela passa por uma mensagem de erro.
	certo := enviarComSenha(t, append([]byte(nil), senha...))
	require.Equal(t, http.StatusCreated, certo.Code, certo.Body.String())
	errado := enviarComSenha(t, senhaAleatoria(t))
	require.Equal(t, http.StatusUnprocessableEntity, errado.Code)

	assert.NotContains(t, certo.Body.String(), textoDaSenha, "a senha não volta na resposta")
	assert.NotContains(t, errado.Body.String(), textoDaSenha, "nem na resposta de erro")
	assert.NotContains(t, log.String(), textoDaSenha, "a senha não entra em log")

	for _, e := range a.auditoria.entradas {
		junto := e.Action + e.Entity + e.EntityID + e.UserID + e.HouseholdID + e.IP
		assert.NotContains(t, junto, textoDaSenha, "a senha não entra em auditoria")
	}

	// E não está em coluna nenhuma do lote gravado.
	var view importer.BatchView
	require.NoError(t, json.Unmarshal(certo.Body.Bytes(), &view))
	lote, err := a.repoImport.BatchByID(t.Context(), a.casa.ID, view.ID)
	require.NoError(t, err)
	serializado, err := json.Marshal(lote)
	require.NoError(t, err)
	assert.NotContains(t, string(serializado), textoDaSenha, "a senha não entra no banco")

	// Nem nas linhas de staging.
	linhas := revisarPelaAPI(t, a, view.ID)
	linhasJSON, err := json.Marshal(linhas)
	require.NoError(t, err)
	assert.NotContains(t, string(linhasJSON), textoDaSenha)
}

// TestAuditoriaDaImportacaoNuncaCarregaValorMonetario é a segunda metade do
// critério 13 (S8).
func TestAuditoriaDaImportacaoNuncaCarregaValorMonetario(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)
	cartao := a.contaCartao(t)

	// Um ciclo com TODAS as ações auditáveis: criar, confirmar com
	// transferência, excluir, reimportar, restaurar e descartar.
	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "e.csv", fixtureExtrato(t)))
	pagamento := linhaComDescricao(t, revisarPelaAPI(t, a, lote.ID).Items, "Pagamento de fatura")
	_ = resultadoDaResposta(t, confirmarPelaAPI(t, a, lote.ID, fmt.Sprintf(
		`{"decisions":[{"rowId":%q,"action":"transfer","counterpartAccountId":%q}]}`,
		pagamento.ID, cartao.ID)))

	descartado := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "d.csv", fixtureExtrato(t)))
	r := requisicao(t, http.MethodDelete, "/api/v1/imports/"+descartado.ID, nil,
		identidade(a.casa.ID, a.usuario.ID))
	r.SetPathValue("id", descartado.ID)
	rec := httptest.NewRecorder()
	a.handler(t).Delete(rec, r)
	require.Equal(t, http.StatusNoContent, rec.Code)

	require.NotEmpty(t, a.auditoria.entradas)

	// Todos os valores que existem de verdade no arquivo, em centavos e em
	// reais. Nenhum deles pode aparecer em NENHUM campo de auditoria.
	proibidos := []string{
		"2000", "18707", "160000", "85000", "1100", "1028757", "300000",
		"13992", "2900", "500000", "285982",
		"20.00", "187.07", "1600.00", "850.00", "2859.82", "11.00",
		"amount", "cents", "valor", "total",
	}
	for _, e := range a.auditoria.entradas {
		junto := strings.ToLower(e.Action + "|" + e.Entity + "|" + e.EntityID + "|" +
			e.UserID + "|" + e.HouseholdID + "|" + e.IP)
		for _, proibido := range proibidos {
			assert.NotContains(t, junto, strings.ToLower(proibido),
				"auditoria não carrega valor monetário (S8): ação %q", e.Action)
		}
	}

	// E as ações registradas são exatamente as do vocabulário da spec.
	vocabulario := map[string]bool{
		"import.created": true, "import.confirmed": true, "import.discarded": true,
		"transaction.deleted": true, "transaction.restored": true,
		"card_statement.created": true,
	}
	for _, acao := range a.auditoria.acoes() {
		assert.True(t, vocabulario[acao], "ação de auditoria fora do vocabulário: %q", acao)
	}
	assert.Contains(t, a.auditoria.acoes(), "import.discarded")
}

// TestNenhumaRespostaDaImportacaoExpoeAChaveDeDeduplicacao: a chave é o
// MECANISMO da garantia. Expô-la daria ao cliente material para tentar fabricar
// colisões — e colisão fabricada apaga lançamento alheio dentro da própria casa.
func TestNenhumaRespostaDaImportacaoExpoeAChaveDeDeduplicacao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	lote := loteDaResposta(t, enviarPelaAPI(t, a, conta.ID, "e.csv", fixtureExtrato(t)))

	revisaoRec := requisicao(t, http.MethodGet, "/api/v1/imports/"+lote.ID+"?limit=200", nil,
		identidade(a.casa.ID, a.usuario.ID))
	revisaoRec.SetPathValue("id", lote.ID)
	rec := httptest.NewRecorder()
	a.handler(t).Get(rec, revisaoRec)
	require.Equal(t, http.StatusOK, rec.Code)

	confirmRec := confirmarPelaAPI(t, a, lote.ID, `{"decisions":[]}`)
	listaRec := listarPelaAPI(t, a, "2026-08")
	listaJSON, err := json.Marshal(listaRec)
	require.NoError(t, err)

	corpos := map[string]string{
		"revisão":  rec.Body.String(),
		"confirm":  confirmRec.Body.String(),
		"listagem": string(listaJSON),
	}
	proibidos := []string{"dedupKey", "dedup_key", "dedupOrdinal", "dedup_ordinal",
		"householdId", "household_id", "descriptionNorm", "description_norm", "contentSha256"}
	for onde, corpo := range corpos {
		for _, campo := range proibidos {
			assert.NotContains(t, corpo, campo, "%s não pode expor %s", onde, campo)
		}
	}
}

// --- taxonomia: nada entra sem decisão ------------------------------------

// TestTodoStatusBarradoTemDefaultSkipNaBorda percorre a taxonomia inteira pela
// RESPOSTA HTTP, e não pela tabela interna.
//
// É a trava contra o defeito mais caro desta entrega: um status novo que nasça
// com default `import` importaria linha marcada em silêncio, e o único sinal
// seria um lançamento a mais que ninguém procura.
func TestTodoStatusBarradoTemDefaultSkipNaBorda(t *testing.T) {
	t.Parallel()

	barrados := []dedup.Status{
		dedup.StatusDuplicateExact,
		dedup.StatusDuplicateDeleted,
		dedup.StatusPossibleDuplicate,
		dedup.StatusCardPayment,
		dedup.StatusInternalTransfer,
		dedup.StatusRejected,
	}
	for _, s := range barrados {
		assert.Equal(t, importer.ActionSkip, importer.DefaultActionFor(s),
			"status %q não pode entrar por default", s)
	}

	// `transferencia_ja_registrada` tem default `link` (spec 0005), que NÃO
	// grava lançamento: nenhuma linha barrada entra por default continua
	// verdade — o que muda é que a perna já registrada ganha a chave.
	assert.Equal(t, importer.ActionLink, importer.DefaultActionFor(dedup.StatusTransferAlreadyRegistered))
	assert.NotContains(t, importer.AllowedActionsFor(dedup.StatusTransferAlreadyRegistered), importer.ActionImport)

	entram := []dedup.Status{dedup.StatusNew, dedup.StatusRepeatedInFile}
	for _, s := range entram {
		assert.Equal(t, importer.ActionImport, importer.DefaultActionFor(s),
			"status %q é linha legítima e precisa entrar", s)
	}
}

// --- fiação do io (paranoia barata) ---------------------------------------

// TestNenhumaLeituraSemTetoNoCaminhoDoUpload é a trava de CÓDIGO do caminho que
// recebe arquivo.
//
// Duas regras, cada uma com um defeito real por trás:
//
//  1. `io.Copy` sobre um descompressor é bomba de descompressão (regra G110 do
//     gosec). O caminho inteiro usa `io.CopyN` ou `io.LimitReader`.
//  2. `io.ReadAll` só é aceitável envolvendo um leitor JÁ limitado. Um
//     `io.ReadAll(parte)` cru leria os 8 MiB — ou o que o cliente quisesse —
//     direto para a memória do processo.
//
// A varredura é sobre o CÓDIGO reimpresso da árvore sintática, sem comentários:
// sem isso, o próprio comentário que explica a proibição faria o teste falhar, e
// o jeito de "consertar" seria apagar a explicação.
func TestNenhumaLeituraSemTetoNoCaminhoDoUpload(t *testing.T) {
	t.Parallel()

	arquivos, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, arquivos)

	for _, caminho := range arquivos {
		if strings.HasSuffix(caminho, "_test.go") {
			continue
		}
		codigo := semComentarios(t, caminho)

		assert.NotContains(t, codigo, "io.Copy(",
			"%s: use io.CopyN ou io.LimitReader — io.Copy sobre descompressor é bomba (G110)", caminho)

		// Toda ocorrência de io.ReadAll precisa abrir imediatamente com um
		// leitor limitado.
		for resto := codigo; ; {
			i := strings.Index(resto, "io.ReadAll(")
			if i < 0 {
				break
			}
			resto = resto[i+len("io.ReadAll("):]
			assert.True(t, strings.HasPrefix(resto, "io.LimitReader("),
				"%s: io.ReadAll sem io.LimitReader no caminho do upload", caminho)
		}
	}

	// A varredura só vale se o pacote de fato usar o leitor limitado.
	fonte, err := os.ReadFile("multipart.go")
	require.NoError(t, err)
	assert.Contains(t, string(fonte), "io.LimitReader", "o teto por parte tem de existir")
}
