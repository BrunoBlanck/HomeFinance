package account_test

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Os três campos do schema v3 — `institution`, `statementClosingDay` e
// `statementDueDay` — existiam na coluna, na entidade e no mapper, e NÃO
// existiam no corpo da requisição nem no DTO de resposta. O efeito não era
// cosmético:
//
//   - sem caminho de escrita, toda conta ficava `other`, e a trava de
//     consistência da importação (§3.3) nunca disparava;
//   - sem os dias, a sugestão de fatura caía sempre na inferência
//     "maior data + 10 dias", ignorando o que a pessoa configurou;
//   - sem os campos na resposta, `GET /accounts` devolvia um objeto SEM três
//     campos `required` do contrato, e o tipo gerado no front prometia que eles
//     estavam lá.
//
// Estes testes cobrem as duas pontas: o que entra (allowlist, faixa, regra de
// tipo) e o que sai (o payload real contra os `required` do schema).

func inteiro(n int) *int { return &n }

// --- entrada ---------------------------------------------------------------

func TestCriarGravaInstituicaoEDiasDeFatura(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	entrada := entradaValida()
	entrada.Name = "Nubank Cartão"
	entrada.Kind = account.KindCreditCard
	entrada.Institution = account.InstitutionNubank
	entrada.StatementClosingDay = inteiro(28)
	entrada.StatementDueDay = inteiro(5)

	view, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
	require.NoError(t, err)

	assert.Equal(t, account.InstitutionNubank, view.Institution)
	require.NotNil(t, view.StatementClosingDay)
	require.NotNil(t, view.StatementDueDay)
	assert.Equal(t, 28, *view.StatementClosingDay)
	assert.Equal(t, 5, *view.StatementDueDay)

	// E o que foi de fato GRAVADO — a resposta poderia estar certa com a
	// persistência errada.
	gravada := repo.linhas[view.ID]
	assert.Equal(t, account.InstitutionNubank, gravada.Institution)
	require.NotNil(t, gravada.StatementClosingDay)
	assert.Equal(t, 28, *gravada.StatementClosingDay)
}

// Sem instituição no corpo, a conta nasce `other` — é o que o contrato diz
// ("ausente = other") e é o que mantém a coluna NOT NULL preenchida.
func TestCriarSemInstituicaoNasceOther(t *testing.T) {
	t.Parallel()

	svc := novoServico(t, novoRepo())
	view, err := svc.Create(t.Context(), ator(minhaCasa), entradaValida())
	require.NoError(t, err)
	assert.Equal(t, account.InstitutionOther, view.Institution)
	assert.Nil(t, view.StatementClosingDay)
	assert.Nil(t, view.StatementDueDay)
}

func TestInstituicaoForaDaAllowlistEhRecusada(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	entrada := entradaValida()
	entrada.Institution = "itau"

	_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
	require.ErrorIs(t, err, account.ErrInvalidInstitution)
	assert.Zero(t, repo.criadas, "nada pode ter sido criado")
}

func TestDiaDeFaturaSoExisteEmCartaoDeCredito(t *testing.T) {
	t.Parallel()

	repo := novoRepo()
	svc := novoServico(t, repo)

	entrada := entradaValida() // checking
	entrada.StatementClosingDay = inteiro(10)

	_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
	require.ErrorIs(t, err, account.ErrStatementDayNotAllowed)
	assert.Zero(t, repo.criadas)
}

func TestDiaDeFaturaForaDaFaixaEhRecusado(t *testing.T) {
	t.Parallel()

	for nome, dia := range map[string]int{"zero": 0, "negativo": -1, "trinta e dois": 32} {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			repo := novoRepo()
			svc := novoServico(t, repo)

			entrada := entradaValida()
			entrada.Kind = account.KindCreditCard
			entrada.StatementDueDay = inteiro(dia)

			_, err := svc.Create(t.Context(), ator(minhaCasa), entrada)
			require.ErrorIs(t, err, account.ErrInvalidStatementDay)
			assert.Zero(t, repo.criadas)
		})
	}
}

// O tri-estado do PATCH: ausente não mexe, `null` limpa. Sem ele, limpar o dia
// de vencimento de um cartão seria impossível pela API — e o usuário veria o
// valor antigo voltar sem entender por quê.
func TestPatchDistingueAusenteDeNuloNosDiasDeFatura(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	criar := `{"name":"Nubank Cartão","kind":"credit_card","institution":"nubank",` +
		`"statementClosingDay":28,"statementDueDay":5,` +
		`"openingBalanceCents":0,"openingDate":"2026-09-01"}`
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", criar, amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var criada account.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &criada))

	// 1. Campo AUSENTE: o outro campo muda, os dias ficam como estavam.
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+criada.ID,
		`{"name":"Nubank Cartão Roxinho"}`, amb.handler.Update, criada.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var depoisDoNome account.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &depoisDoNome))
	require.NotNil(t, depoisDoNome.StatementClosingDay, "campo ausente NÃO apaga o que estava lá")
	assert.Equal(t, 28, *depoisDoNome.StatementClosingDay)

	// 2. `null` EXPLÍCITO: volta a "não configurado".
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+criada.ID,
		`{"statementClosingDay":null,"statementDueDay":null}`, amb.handler.Update, criada.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var depoisDoNulo account.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &depoisDoNulo))
	assert.Nil(t, depoisDoNulo.StatementClosingDay, "`null` explícito limpa")
	assert.Nil(t, depoisDoNulo.StatementDueDay)
}

// A regra é conferida sobre o estado FINAL: a conta que deixa de ser cartão não
// pode ficar com um dia de vencimento pendurado, apontando para uma regra que
// não se aplica mais.
func TestTrocarOTipoSemLimparOsDiasEhRecusado(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	criar := `{"name":"Cartão","kind":"credit_card","institution":"c6",` +
		`"statementClosingDay":28,"statementDueDay":5,` +
		`"openingBalanceCents":0,"openingDate":"2026-09-01"}`
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", criar, amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var criada account.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &criada))

	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+criada.ID,
		`{"kind":"checking"}`, amb.handler.Update, criada.ID)
	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	// E com os dias limpos na MESMA edição, passa.
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+criada.ID,
		`{"kind":"checking","statementClosingDay":null,"statementDueDay":null}`,
		amb.handler.Update, criada.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// Na EDIÇÃO não existe "instituição vazia": o campo presente no corpo é uma
// escolha, e traduzir string vazia para `other` em silêncio desligaria a trava
// de consistência da importação sem ninguém pedir.
func TestPatchTrocaInstituicaoERecusaVazio(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts",
		`{"name":"Conta","kind":"checking","institution":"nubank",`+
			`"openingBalanceCents":0,"openingDate":"2026-09-01"}`,
		amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var criada account.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &criada))
	require.Equal(t, account.InstitutionNubank, criada.Institution)

	// Troca legítima.
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+criada.ID,
		`{"institution":"c6"}`, amb.handler.Update, criada.ID)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var trocada account.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &trocada))
	assert.Equal(t, account.InstitutionC6, trocada.Institution)

	// Vazio é recusa, e a instituição anterior continua valendo.
	rec = amb.chamar(t, minhaCasa, http.MethodPatch, "/accounts/"+criada.ID,
		`{"institution":""}`, amb.handler.Update, criada.ID)
	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())

	rec = amb.chamar(t, minhaCasa, http.MethodGet, "/accounts/"+criada.ID, "", amb.handler.Get, criada.ID)
	require.Equal(t, http.StatusOK, rec.Code)
	var depois account.View
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &depois))
	assert.Equal(t, account.InstitutionC6, depois.Institution, "a recusa não pode ter mexido no dado")
}

// --- borda HTTP: os códigos que o contrato publica -------------------------

func TestBordaRecusaInstituicaoEDiasComOsCodigosDoContrato(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		corpo  string
		status int
		campo  string
	}{
		"instituição fora da allowlist": {
			corpo: `{"name":"X","kind":"checking","institution":"itau",` +
				`"openingBalanceCents":0,"openingDate":"2026-09-01"}`,
			status: http.StatusUnprocessableEntity,
			campo:  "institution",
		},
		"dia em conta que não é cartão": {
			corpo: `{"name":"X","kind":"checking","statementClosingDay":10,` +
				`"openingBalanceCents":0,"openingDate":"2026-09-01"}`,
			status: http.StatusUnprocessableEntity,
			campo:  "statementClosingDay",
		},
		"dia fora da faixa": {
			corpo: `{"name":"X","kind":"credit_card","statementDueDay":40,` +
				`"openingBalanceCents":0,"openingDate":"2026-09-01"}`,
			status: http.StatusUnprocessableEntity,
			campo:  "statementDueDay",
		},
		"dia com tipo errado é corpo malformado": {
			corpo: `{"name":"X","kind":"credit_card","statementDueDay":"cinco",` +
				`"openingBalanceCents":0,"openingDate":"2026-09-01"}`,
			status: http.StatusBadRequest,
		},
	}

	for nome, caso := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			amb := novoAmbiente(t)
			rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", caso.corpo, amb.handler.Create, "")
			require.Equal(t, caso.status, rec.Code, rec.Body.String())

			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, "VALIDATION_FAILED", codigo)
			if caso.campo != "" {
				assert.Contains(t, campos, caso.campo, "o formulário precisa saber qual campo destacar")
			}
			assert.Zero(t, amb.repo.criadas, "nenhuma conta pode ter sido criada")
		})
	}
}

// --- saída: o payload REAL contra os `required` do contrato ----------------

// schemaAccountRequired lê a lista `required` do schema Account em
// api/openapi.yaml.
//
// Ler a spec em vez de repetir a lista aqui é o ponto do teste: uma lista
// copiada envelhece junto com o código que ela deveria vigiar. Foi assim que
// três campos `required` ficaram fora da resposta sem nenhum teste reclamar —
// routes_test.go compara CAMINHOS e o schema-sync do front compara TIPOS;
// ninguém comparava o payload.
func schemaAccountRequired(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile("../../api/openapi.yaml")
	require.NoError(t, err, "api/openapi.yaml precisa existir (ADR-006)")

	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Required []string `yaml:"required"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))

	esquema, ok := doc.Components.Schemas["Account"]
	require.True(t, ok, "o schema Account precisa existir na spec")
	require.NotEmpty(t, esquema.Required)
	return esquema.Required
}

// chavesDoObjeto devolve as chaves de primeiro nível de um objeto JSON.
func chavesDoObjeto(t *testing.T, bruto []byte) []string {
	t.Helper()
	var objeto map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(bruto, &objeto), "corpo: %s", string(bruto))

	out := make([]string, 0, len(objeto))
	for chave := range objeto {
		out = append(out, chave)
	}
	sort.Strings(out)
	return out
}

func TestRespostaDeContaTemTodosOsCamposRequiredDoContrato(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	criar := `{"name":"Nubank Cartão","kind":"credit_card","institution":"nubank",` +
		`"statementClosingDay":28,"statementDueDay":5,` +
		`"openingBalanceCents":0,"openingDate":"2026-09-01"}`
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", criar, amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	obrigatorios := schemaAccountRequired(t)

	t.Run("POST /accounts", func(t *testing.T) {
		chaves := chavesDoObjeto(t, rec.Body.Bytes())
		for _, campo := range obrigatorios {
			assert.Contains(t, chaves, campo, "campo `required` ausente na resposta")
		}
	})

	t.Run("GET /accounts", func(t *testing.T) {
		lista := amb.chamar(t, minhaCasa, http.MethodGet, "/accounts", "", amb.handler.List, "")
		require.Equal(t, http.StatusOK, lista.Code, lista.Body.String())

		var corpo struct {
			Items []json.RawMessage `json:"items"`
		}
		require.NoError(t, json.Unmarshal(lista.Body.Bytes(), &corpo))
		require.Len(t, corpo.Items, 1)

		chaves := chavesDoObjeto(t, corpo.Items[0])
		for _, campo := range obrigatorios {
			assert.Contains(t, chaves, campo, "campo `required` ausente na listagem")
		}
	})
}

// A contrapartida do teste acima: o que SAI não pode ter campo que o contrato
// não declara (`additionalProperties: false`). É o mesmo cuidado do
// DisallowUnknownFields, do lado da resposta — coluna nova não vaza por acidente
// (docs/SEGURANCA.md §4).
func TestRespostaDeContaNaoTemCampoForaDoContrato(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../api/openapi.yaml")
	require.NoError(t, err)

	var doc struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]yaml.Node `yaml:"properties"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &doc))
	declarados := doc.Components.Schemas["Account"].Properties
	require.NotEmpty(t, declarados)

	amb := novoAmbiente(t)
	rec := amb.chamar(t, minhaCasa, http.MethodPost, "/accounts", corpoValido, amb.handler.Create, "")
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	for _, chave := range chavesDoObjeto(t, rec.Body.Bytes()) {
		assert.Contains(t, declarados, chave, "a resposta publica um campo que o contrato não declara")
	}
}
