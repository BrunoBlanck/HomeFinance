package transaction_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/platform/httpserver"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rodada de QA da tarefa T5 da E2d: o que o filtro de tipo PROMETE, atacado
// pelos vetores que a spec 0004 §12.5 lista e que os testes da implementação
// não exercitavam.
//
// O que este arquivo acrescenta ao filtro_de_tipo_e2d_test.go:
//
//   - PROPRIEDADE, e não exemplo escolhido a dedo: a partição e a invariância
//     de invested/redeemed conferidas sobre cenários sorteados, percorridos
//     PÁGINA A PÁGINA com limite 1 — o exemplo fixo nunca pega a linha que o
//     cursor perde;
//   - BOLA: cursor legítimo de outra casa, e `accountId` de outra casa, nos
//     cinco recortes;
//   - cursor CRUZADO entre grupos (obtido em `expense`, reenviado em
//     `investment`), que o ADR-030(d) declara como comportamento suportado;
//   - bytes estranhos no parâmetro (`%00`, espaço, quebra de linha, homoglifo
//     cirílico) e o parâmetro REPETIDO, que a §12.5.5 manda recusar;
//   - categoria de investimento ARQUIVADA, que a §12.2 manda incluir;
//   - taxonomia violada (201 categorias marcadas) pelo caminho do filtro.

// --- propriedade: a partição ----------------------------------------------

// cenarioSorteado monta um mês inteiro aleatório, com todas as situações que a
// partição precisa separar — inclusive as três que os defeitos clássicos
// escondem: a linha SEM categoria, a perna de transferência e a categoria de
// investimento ARQUIVADA.
type cenarioSorteado struct {
	amb      *ambiente
	descrito string
}

func sortearCenario(t *testing.T, semente uint64) cenarioSorteado {
	t.Helper()

	r := rand.New(rand.NewPCG(semente, 0x5eed))
	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-2", "Poupança", account.KindChecking)

	mercado := amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	salario := amb.categoria(minhaCasa, "cat-salario", "Salário", category.KindIncome)
	cdb := amb.categoria(minhaCasa, "cat-cdb", "CDB", category.KindInvestment)
	resgate := amb.categoria(minhaCasa, "cat-resgate", "Resgates", category.KindRedemption)

	// Uma categoria de investimento ARQUIVADA: a §12.2 manda incluí-la no
	// grupo `investment` e excluí-la de `expense`. Se o serviço lesse a
	// taxonomia sem as arquivadas, o aporte antigo apareceria em "Despesas" E
	// em "Investimentos" — e a partição deixaria de fechar.
	arquivada := amb.categorias.add(category.Category{
		ID: "cat-cdb-velho", HouseholdID: minhaCasa, Name: "CDB antigo", NameNorm: "cdb antigo",
		Kind: category.KindInvestment, ArchivedAt: &agora,
	})

	// A casa vizinha, no MESMO mês e com a MESMA categoria marcada: nenhuma
	// linha dela pode entrar em recorte nenhum (BOLA).
	amb.conta(outraCasa, "acc-alheia", "Alheia", account.KindChecking)

	semear := func(casa, conta, kind string, valor int64, dia int, cat *string) {
		amb.repo.semear(transaction.Transaction{
			HouseholdID: casa, AccountID: conta, Kind: kind, AmountCents: valor,
			Description: "Linha " + strconv.Itoa(dia), CategoryID: cat,
			OccurredOn: civil.MustNew(2026, 9, dia), CompetenceMonth: "2026-09",
		})
	}

	// De 6 a 25 lançamentos, sorteados entre as sete situações.
	total := 6 + r.IntN(20)
	for i := range total {
		dia := 1 + r.IntN(28)
		valor := int64(1 + r.IntN(500_000)) // centavos, sempre > 0 (ADR-003)
		switch r.IntN(7) {
		case 0:
			semear(minhaCasa, "acc-1", transaction.KindIncome, valor, dia, ptr(salario.ID))
		case 1:
			semear(minhaCasa, "acc-1", transaction.KindIncome, valor, dia, nil)
		case 2:
			semear(minhaCasa, "acc-1", transaction.KindExpense, valor, dia, ptr(mercado.ID))
		case 3:
			semear(minhaCasa, "acc-1", transaction.KindExpense, valor, dia, nil)
		case 4:
			semear(minhaCasa, "acc-1", transaction.KindExpense, valor, dia, ptr(cdb.ID))
		case 5:
			semear(minhaCasa, "acc-1", transaction.KindIncome, valor, dia, ptr(resgate.ID))
		case 6:
			// Aporte na categoria ARQUIVADA.
			semear(minhaCasa, "acc-1", transaction.KindExpense, valor, dia, ptr(arquivada.ID))
		}
		// A cada três linhas, um par de transferência — as duas pernas.
		if i%3 == 0 {
			amb.parDeTransferencia(minhaCasa, "acc-1", "acc-2", valor, dia, fmt.Sprintf("grp-%d-%d", semente, i))
			total += 2
		}
		// E uma linha da vizinha de vez em quando, para o vazamento ter o que
		// vazar.
		if i%5 == 0 {
			semear(outraCasa, "acc-alheia", transaction.KindExpense, valor, dia, ptr(cdb.ID))
		}
	}

	return cenarioSorteado{amb: amb, descrito: fmt.Sprintf("semente=%d", semente)}
}

// percorrer pagina o recorte inteiro com limite 1 e devolve os ids na ordem em
// que a API os entregou.
//
// Limite 1 é de propósito: é o tamanho em que todo defeito de cursor aparece —
// página repetida, linha pulada e laço infinito. A guarda de 500 voltas existe
// para que um cursor que não avança falhe como TESTE e não como travamento da
// suíte.
func percorrer(t *testing.T, amb *ambiente, casa, grupo string) []string {
	t.Helper()

	var ids []string
	cursor := ""
	for voltas := 0; ; voltas++ {
		require.Less(t, voltas, 500, "o cursor não avançou: laço em %q", grupo)
		v, err := amb.svc.List(t.Context(), ator(casa), transaction.ListInput{
			Month: "2026-09", KindGroup: grupo, Limit: 1, Cursor: cursor,
		})
		require.NoError(t, err)
		for _, item := range v.Items {
			ids = append(ids, item.ID)
		}
		if v.NextCursor == nil {
			break
		}
		require.NotEmpty(t, *v.NextCursor, "cursor presente nunca é string vazia")
		cursor = *v.NextCursor
	}
	return ids
}

// A propriedade que sustenta a feature inteira: os quatro grupos PARTICIONAM a
// janela — nenhuma linha perdida, nenhuma contada duas vezes —, e isso vale
// para qualquer mês, não só para o exemplo de oito linhas da implementação.
//
// A varredura é PAGINADA com limite 1: uma partição que fechasse só na
// primeira página esconderia um defeito de cursor sob filtro.
func TestQAParticaoDoFiltroDeTipoEPropriedade(t *testing.T) {
	t.Parallel()

	for semente := uint64(1); semente <= 25; semente++ {
		t.Run(fmt.Sprintf("semente-%02d", semente), func(t *testing.T) {
			t.Parallel()
			c := sortearCenario(t, semente)

			tudo := percorrer(t, c.amb, minhaCasa, "")
			vistos := map[string]string{}
			var soma int
			for _, grupo := range []string{
				transaction.KindGroupIncome, transaction.KindGroupExpense,
				transaction.KindGroupTransfer, transaction.KindGroupInvestment,
			} {
				ids := percorrer(t, c.amb, minhaCasa, grupo)
				soma += len(ids)
				for _, id := range ids {
					if antes, repetido := vistos[id]; repetido {
						t.Fatalf("%s: lançamento %s aparece em %q E em %q (%s)",
							c.descrito, id, antes, grupo, "dupla contagem")
					}
					vistos[id] = grupo
				}
			}

			assert.Equal(t, len(tudo), soma,
				"%s: a soma dos quatro grupos tem de ser o Tudo", c.descrito)
			assert.ElementsMatch(t, tudo, chavesDe(vistos),
				"%s: os quatro grupos cobrem exatamente as linhas de Tudo", c.descrito)
		})
	}
}

func chavesDe(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A segunda propriedade: os números. `investedCents`/`redeemedCents` são
// IGUAIS nas cinco opções (ADR-030 c′), `netCents == incomeCents − expenseCents`
// nas cinco, e o `Entrou`/`Saiu` que o filtro nomeia é o mesmo de "Tudo".
//
// É a propriedade que a "correção" de propagar o kindGroup para o WHERE do
// resumo quebraria — e que um exemplo com números redondos deixaria passar.
func TestQANumerosDoResumoSaoInvariantesEPropriedade(t *testing.T) {
	t.Parallel()

	for semente := uint64(100); semente <= 124; semente++ {
		t.Run(fmt.Sprintf("semente-%03d", semente), func(t *testing.T) {
			t.Parallel()
			c := sortearCenario(t, semente)

			resumo := func(grupo string) transaction.SummaryView {
				v, err := c.amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
					Month: "2026-09", KindGroup: grupo,
				})
				require.NoError(t, err)
				return v.Summary
			}

			tudo := resumo("")
			var contagens int64
			for _, grupo := range osCincoRecortes {
				r := resumo(grupo)

				assert.Equal(t, tudo.InvestedCents, r.InvestedCents,
					"%s: investedCents mudou sob %q — ele não responde ao filtro (ADR-030 c′)", c.descrito, grupo)
				assert.Equal(t, tudo.RedeemedCents, r.RedeemedCents,
					"%s: redeemedCents mudou sob %q", c.descrito, grupo)
				assert.Equal(t, r.IncomeCents-r.ExpenseCents, r.NetCents,
					"%s: netCents deixou de ser a diferença sob %q", c.descrito, grupo)
				assert.GreaterOrEqual(t, r.IncomeCents, int64(0), "%s: receita negativa sob %q", c.descrito, grupo)
				assert.GreaterOrEqual(t, r.ExpenseCents, int64(0), "%s: despesa negativa sob %q", c.descrito, grupo)

				if grupo != "" {
					contagens += r.Count
				}
			}

			assert.Equal(t, tudo.IncomeCents, resumo(transaction.KindGroupIncome).IncomeCents,
				"%s: Entrou sob receitas é IDÊNTICO ao de Tudo (§12.3.3)", c.descrito)
			assert.Equal(t, tudo.ExpenseCents, resumo(transaction.KindGroupExpense).ExpenseCents,
				"%s: Saiu sob despesas é IDÊNTICO ao de Tudo (§12.3.3)", c.descrito)
			assert.Equal(t, tudo.Count, contagens,
				"%s: a contagem do resumo particiona junto com a lista", c.descrito)

			// Pendência: zero nos dois grupos sem categoria possível, e a soma
			// dos dois que têm fecha com o Tudo.
			assert.Zero(t, resumo(transaction.KindGroupTransfer).UncategorizedCount,
				"%s: perna de transferência não é pendência (ADR-030c)", c.descrito)
			assert.Zero(t, resumo(transaction.KindGroupInvestment).UncategorizedCount,
				"%s: aporte e resgate têm categoria por definição", c.descrito)
			assert.Equal(t, tudo.UncategorizedCount,
				resumo(transaction.KindGroupIncome).UncategorizedCount+
					resumo(transaction.KindGroupExpense).UncategorizedCount,
				"%s: a pendência de Tudo é a de receitas mais a de despesas", c.descrito)
		})
	}
}

// A categoria de investimento ARQUIVADA continua marcando (§12.2): o aporte
// feito nela sai de "Despesas" e entra em "Investimentos". Se a taxonomia
// fosse lida sem as arquivadas, a mesma linha apareceria nos DOIS grupos.
func TestQACategoriaDeInvestimentoArquivadaContinuaMarcando(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	arquivada := amb.categorias.add(category.Category{
		ID: "cat-cdb-velho", HouseholdID: minhaCasa, Name: "CDB antigo", NameNorm: "cdb antigo",
		Kind: category.KindInvestment, ArchivedAt: &agora,
	})
	aporte := amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 1_000_00, Description: "CDB antigo", CategoryID: ptr(arquivada.ID),
		OccurredOn: civil.MustNew(2026, 9, 10), CompetenceMonth: "2026-09",
	})
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 30_00, 9)

	despesas := listar(t, amb, transaction.KindGroupExpense)
	for _, item := range despesas.Items {
		assert.NotEqual(t, aporte.ID, item.ID,
			"aporte em categoria ARQUIVADA não pode voltar para Despesas")
	}
	investimentos := listar(t, amb, transaction.KindGroupInvestment)
	require.Len(t, investimentos.Items, 1)
	assert.Equal(t, aporte.ID, investimentos.Items[0].ID)
	assert.Equal(t, int64(1_000_00), investimentos.Summary.InvestedCents,
		"o dinheiro da arquivada continua sendo aporte")
}

// --- BOLA ------------------------------------------------------------------

// Cursor LEGÍTIMO da casa A reenviado pela casa B, com e sem `kindGroup`.
//
// O cursor é posição pura (ADR-030d) e não carrega casa — então ele é
// aceitável, e é exatamente por isso que o WHERE tem de ser remontado com a
// casa do TOKEN. Nenhuma linha da casa A pode sair, em nenhum dos cinco
// recortes.
func TestQACursorDeOutraCasaNaoAlcancaLinhaAlheia(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	// Casa A — a vítima. Linhas de todos os tipos.
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-2", "Poupança", account.KindChecking)
	catA := amb.categoria(minhaCasa, "cat-cdb", "CDB", category.KindInvestment)
	for dia := 1; dia <= 20; dia++ {
		amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Da casa A", 10_00, dia)
	}
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 500_00, Description: "Aporte A", CategoryID: ptr(catA.ID),
		OccurredOn: civil.MustNew(2026, 9, 25), CompetenceMonth: "2026-09",
	})
	amb.parDeTransferencia(minhaCasa, "acc-1", "acc-2", 100_00, 26, "grp-a")

	// Casa B — a atacante. Precisa existir para o serviço não recusar por
	// casa vazia, e para o teste distinguir "vazio" de "só o meu".
	amb.conta(outraCasa, "acc-b", "Conta B", account.KindChecking)
	catB := amb.categoria(outraCasa, "cat-b", "CDB B", category.KindInvestment)
	minhaLinhaB := amb.lancamento(outraCasa, "acc-b", transaction.KindExpense, "Da casa B", 7_00, 15)
	_ = catB

	idsDaCasaA := map[string]bool{}
	for id, linha := range amb.repo.linhas {
		if linha.HouseholdID == minhaCasa {
			idsDaCasaA[id] = true
		}
	}

	// O cursor legítimo da casa A, obtido pela própria casa A.
	primeira, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
		Month: "2026-09", Limit: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, primeira.NextCursor)
	cursorAlheio := *primeira.NextCursor

	for _, grupo := range osCincoRecortes {
		nome := grupo
		if nome == "" {
			nome = "tudo"
		}
		t.Run(nome, func(t *testing.T) {
			v, err := amb.svc.List(t.Context(), ator(outraCasa), transaction.ListInput{
				Month: "2026-09", KindGroup: grupo, Cursor: cursorAlheio,
			})
			require.NoError(t, err, "o cursor é posição pura: reenviado por outra casa ele não quebra")
			for _, item := range v.Items {
				assert.False(t, idsDaCasaA[item.ID],
					"BOLA: o cursor da casa A entregou o lançamento %s para a casa B", item.ID)
			}
			assert.Zero(t, v.Summary.InvestedCents, "o resumo também é da casa do token")
		})
	}

	// Sanidade: sem cursor, a casa B vê a linha dela — o teste acima não
	// passou só porque tudo veio vazio.
	semCursor, err := amb.svc.List(t.Context(), ator(outraCasa), transaction.ListInput{Month: "2026-09"})
	require.NoError(t, err)
	require.Len(t, semCursor.Items, 1)
	assert.Equal(t, minhaLinhaB.ID, semCursor.Items[0].ID)
}

// `accountId` de outra casa é 404 nos cinco recortes — nunca lista vazia.
//
// Lista vazia não vaza dado, mas também não é a resposta certa (S1): id que
// não é meu responde igual a id que não existe, e é isso que impede o
// atacante de usar o endpoint como oráculo de existência de conta.
func TestQAAccountIdDeOutraCasaResponde404NosCincoRecortes(t *testing.T) {
	t.Parallel()

	for _, grupo := range osCincoRecortes {
		nome := grupo
		if nome == "" {
			nome = "tudo"
		}
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			amb := novoHTTPAmbiente(t)
			amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
			amb.categoria(minhaCasa, "cat-cdb", "CDB", category.KindInvestment)
			amb.conta(outraCasa, "acc-alheia", "Alheia", account.KindChecking)

			alvo := "/transactions?month=2026-09&accountId=acc-alheia"
			if grupo != "" {
				alvo += "&kindGroup=" + grupo
			}
			alheia := amb.chamar(t, minhaCasa, http.MethodGet, alvo, "", amb.handler.List)
			require.Equal(t, http.StatusNotFound, alheia.Code, alheia.Body.String())

			// O corpo é IDÊNTICO ao de uma conta que não existe.
			inexistente := amb.chamar(t, minhaCasa, http.MethodGet,
				"/transactions?month=2026-09&accountId=acc-nao-existe", "", amb.handler.List)
			require.Equal(t, http.StatusNotFound, inexistente.Code)
			assert.JSONEq(t, inexistente.Body.String(), alheia.Body.String(),
				"conta alheia e conta inexistente respondem a MESMA coisa (S1)")
		})
	}
}

// Cursor CRUZADO entre grupos: obtido em `expense`, reenviado em `investment`
// e em `transfer`. O ADR-030(d) declara isso suportado — 200, nada de outra
// casa, nada de 500 e nenhuma página repetida.
func TestQACursorCruzadoEntreGruposNaoQuebraENaoVaza(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-2", "Poupança", account.KindChecking)
	cdb := amb.categoria(minhaCasa, "cat-cdb", "CDB", category.KindInvestment)
	resgate := amb.categoria(minhaCasa, "cat-resgate", "Resgates", category.KindRedemption)
	for dia := 1; dia <= 10; dia++ {
		amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 10_00, dia)
	}
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 2_000_00, Description: "CDB", CategoryID: ptr(cdb.ID),
		OccurredOn: civil.MustNew(2026, 9, 20), CompetenceMonth: "2026-09",
	})
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindIncome,
		AmountCents: 500_00, Description: "Resgate", CategoryID: ptr(resgate.ID),
		OccurredOn: civil.MustNew(2026, 9, 5), CompetenceMonth: "2026-09",
	})
	amb.parDeTransferencia(minhaCasa, "acc-1", "acc-2", 100_00, 15, "grp-1")
	amb.conta(outraCasa, "acc-alheia", "Alheia", account.KindChecking)
	alheia := amb.lancamento(outraCasa, "acc-alheia", transaction.KindExpense, "Alheia", 99_00, 20)

	emDespesas, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
		Month: "2026-09", KindGroup: transaction.KindGroupExpense, Limit: 3,
	})
	require.NoError(t, err)
	require.NotNil(t, emDespesas.NextCursor)
	cursor := *emDespesas.NextCursor

	for _, grupo := range []string{transaction.KindGroupInvestment, transaction.KindGroupTransfer, transaction.KindGroupIncome, ""} {
		nome := grupo
		if nome == "" {
			nome = "tudo"
		}
		t.Run(nome, func(t *testing.T) {
			v, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
				Month: "2026-09", KindGroup: grupo, Cursor: cursor,
			})
			require.NoError(t, err, "cursor cruzado é posição, não filtro — não pode virar erro")
			vistos := map[string]bool{}
			for _, item := range v.Items {
				assert.NotEqual(t, alheia.ID, item.ID, "cursor cruzado não pode alcançar outra casa")
				assert.False(t, vistos[item.ID], "a página repetiu o lançamento %s", item.ID)
				vistos[item.ID] = true
			}
		})
	}
}

// --- abuso do parâmetro ----------------------------------------------------

// Bytes estranhos: NUL, espaço, tabulação, quebra de linha, homoglifo
// cirílico e o valor com sufixo. Todos 400, sem eco na resposta nem no log.
//
// O homoglifo está aqui porque é o caso em que uma comparação "normalizada"
// (casefold, trim, unaccent) deixaria passar: `ехрensе` parece `expense` e não
// é. A allowlist compara BYTES, e é isso que o teste trava.
func TestQAKindGroupComBytesEstranhosERecusado(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"nul":              "\x00",
		"nul-no-fim":       "expense\x00",
		"espaco":           " ",
		"espaco-em-volta":  " expense ",
		"tabulacao":        "\texpense",
		"quebra-de-linha":  "expense\n",
		"crlf-injetado":    "expense\r\nX-Injetado: 1",
		"homoglifo":        "ехрense",
		"percent-encoding": "%65xpense",
		"sql":              "expense' OR 1=1 --",
		"array":            "expense[]",
		"json":             `{"kindGroup":"expense"}`,
		"unicode-longo":    "investment​",
	}
	for nome, valor := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			amb := novoHTTPAmbiente(t)
			amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
			amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 30_00, 9)

			alvo := "/transactions?month=2026-09&kindGroup=" + url.QueryEscape(valor)
			rec := amb.chamar(t, minhaCasa, http.MethodGet, alvo, "", amb.handler.List)

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			require.Contains(t, campos, "kindGroup")
			// O eco só é verificável quando o valor tem conteúdo próprio: um
			// espaço isolado aparece em qualquer frase em português, e afirmar
			// que a resposta "não contém espaço" seria um teste que só passa
			// por acidente.
			if strings.TrimSpace(valor) != "" {
				assert.NotContains(t, rec.Body.String(), valor, "o 400 não ecoa o valor recebido")
				assert.NotContains(t, amb.logs.String(), valor, "o valor recusado não vai para o log")
			}
			assert.Zero(t, amb.repo.listagens, "nenhum comando sai para o banco")
			assert.Empty(t, amb.repo.resumosPedidos)
		})
	}
}

// O parâmetro REPETIDO — spec 0004 §12.5.5 o lista, junto de `tudo` e do SQL
// forjado, entre os que respondem 400.
//
// Ele importa mais do que parece: com dois valores na URL, servidor e cliente
// podem discordar sobre qual vale (HTTP Parameter Pollution). Quem lê o
// PRIMEIRO e quem lê o ÚLTIMO respondem listas diferentes para a MESMA URL —
// e é assim que uma tela afirma "Despesas" mostrando outra coisa.
//
// HISTÓRICO — este teste nasceu VERMELHO (QA T5, 18/09/2026): o handler usava
// `url.Values.Get`, que devolve o PRIMEIRO valor em silêncio, e
// `?kindGroup=expense&kindGroup=income` respondia 200 filtrado por despesas.
// Corrigido no mesmo dia com `httpserver.SoleQueryValue`, e o teste passou
// SEM alteração nenhuma nele — confirmado por mutação: revertendo o handler
// para `q.Get`, quatro dos cinco subcasos voltam a falhar (o quinto,
// `invalido-depois-valido`, continua 400 pela allowlist, porque o primeiro
// valor já é inválido). É esta a razão de os cinco casos existirem.
func TestQAKindGroupRepetidoERecusado(t *testing.T) {
	t.Parallel()

	casos := []struct{ nome, query string }{
		{"dois-validos", "kindGroup=expense&kindGroup=income"},
		{"valido-depois-invalido", "kindGroup=expense&kindGroup=all"},
		{"invalido-depois-valido", "kindGroup=all&kindGroup=expense"},
		{"valido-e-vazio", "kindGroup=expense&kindGroup="},
		{"repetido-identico", "kindGroup=expense&kindGroup=expense"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			amb := novoHTTPAmbiente(t)
			amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
			amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 30_00, 9)
			amb.lancamento(minhaCasa, "acc-1", transaction.KindIncome, "Salário", 5_000_00, 5)

			rec := amb.chamar(t, minhaCasa, http.MethodGet,
				"/transactions?month=2026-09&"+c.query, "", amb.handler.List)

			require.Equal(t, http.StatusBadRequest, rec.Code,
				"chave repetida é ambígua e tem de ser recusada (spec 0004 §12.5.5); corpo: %s", rec.Body.String())
			_, campos := corpoDeErro(t, rec)
			require.Contains(t, campos, "kindGroup")
			assert.Zero(t, amb.repo.listagens)
		})
	}
}

// --- taxonomia violada -----------------------------------------------------

// 201 categorias marcadas + `kindGroup=investment` é 500 GENÉRICO (ADR-029
// j.2), nunca 4xx: não há campo que a pessoa possa corrigir neste pedido. O
// log leva a CONTAGEM e mais nada — nem id de categoria, nem centavos.
func TestQATaxonomiaVioladaNoFiltroDeInvestimentosE500Generico(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	for i := range category.MaxPerHousehold + 1 {
		amb.categoria(minhaCasa, fmt.Sprintf("cat-inv-%03d", i), fmt.Sprintf("Fundo %03d", i), category.KindInvestment)
	}
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 1_234_56, Description: "CDB", CategoryID: ptr("cat-inv-000"),
		OccurredOn: civil.MustNew(2026, 9, 10), CompetenceMonth: "2026-09",
	})

	for _, grupo := range osCincoRecortes {
		nome := grupo
		if nome == "" {
			nome = "tudo"
		}
		t.Run(nome, func(t *testing.T) {
			alvo := "/transactions?month=2026-09"
			if grupo != "" {
				alvo += "&kindGroup=" + grupo
			}
			rec := amb.chamar(t, minhaCasa, http.MethodGet, alvo, "", amb.handler.List)
			require.Equal(t, http.StatusInternalServerError, rec.Code,
				"taxonomia violada é 500, nunca 4xx (ADR-029 j.2); corpo: %s", rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeInternalError, codigo)
			assert.Empty(t, campos, "500 genérico não aponta campo")
		})
	}

	logs := amb.logs.String()
	assert.Contains(t, logs, "categorias marcadas", "o log tem a CONTAGEM, que é o diagnóstico útil")
	assert.NotContains(t, logs, "cat-inv-000", "id de categoria não entra em log")
	assert.NotContains(t, logs, "123456", "centavos não entram em log (S8)")
}

// --- paginação sob filtro --------------------------------------------------

// `limit=1` com N linhas no recorte percorre todas, na ordem publicada
// (occurred_on DESC, id DESC), sem repetir e sem pular — e o mesmo conjunto
// sai numa página só quando o limite cabe.
//
// O filtro TIRA linhas; ele não reordena e não reparticiona a página
// (critério §12.5.10).
func TestQAPaginacaoComLimite1PercorreORecorteInteiro(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(minhaCasa, "acc-2", "Poupança", account.KindChecking)
	cdb := amb.categoria(minhaCasa, "cat-cdb", "CDB", category.KindInvestment)
	for dia := 1; dia <= 7; dia++ {
		amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 10_00, dia)
		amb.lancamento(minhaCasa, "acc-1", transaction.KindIncome, "Bico", 20_00, dia)
		amb.repo.semear(transaction.Transaction{
			HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
			AmountCents: 100_00, Description: "CDB", CategoryID: ptr(cdb.ID),
			OccurredOn: civil.MustNew(2026, 9, dia), CompetenceMonth: "2026-09",
		})
		amb.parDeTransferencia(minhaCasa, "acc-1", "acc-2", 50_00, dia, fmt.Sprintf("grp-%d", dia))
	}

	for _, grupo := range osCincoRecortes {
		nome := grupo
		if nome == "" {
			nome = "tudo"
		}
		t.Run(nome, func(t *testing.T) {
			passoAPasso := percorrer(t, amb, minhaCasa, grupo)

			// Sem repetição.
			vistos := map[string]bool{}
			for _, id := range passoAPasso {
				require.False(t, vistos[id], "página repetida: %s", id)
				vistos[id] = true
			}

			// Mesmo conjunto e MESMA ORDEM da página única.
			deUmaVez, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
				Month: "2026-09", KindGroup: grupo, Limit: transaction.MaxPageSize,
			})
			require.NoError(t, err)
			esperado := make([]string, 0, len(deUmaVez.Items))
			for _, item := range deUmaVez.Items {
				esperado = append(esperado, item.ID)
			}
			assert.Equal(t, esperado, passoAPasso,
				"paginar de 1 em 1 entrega a MESMA sequência da página inteira")
			assert.Nil(t, deUmaVez.NextCursor)
		})
	}
}

// O curto-circuito de "Investimentos" conferido pela BORDA, e não só pelo
// serviço: casa sem nenhuma categoria marcada responde **200** com lista vazia
// e resumo zerado.
//
// É o estado de TODA casa no dia da entrega, e o desfecho errado aqui seria um
// 500 — o repositório recusa conjunto vazio de propósito (ErrEmptyCategoryFilter,
// ADR-029f), e sem o atalho em Go esse erro viraria "falha do servidor" na
// primeira vez que alguém clicasse na opção.
func TestQAInvestimentosSemCategoriaMarcadaResponde200PelaBorda(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 30_00, 9)

	rec := amb.chamar(t, minhaCasa, http.MethodGet,
		"/transactions?month=2026-09&kindGroup=investment", "", amb.handler.List)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	conferirContrato(t, schemasDoContrato(t), "TransactionList", rec.Body.Bytes())
	assert.JSONEq(t, `{"items":[],"nextCursor":null,"summary":{
		"incomeCents":0,"expenseCents":0,"netCents":0,"count":0,
		"uncategorizedCount":0,"investedCents":0,"redeemedCents":0}}`, rec.Body.String())
	assert.Zero(t, amb.repo.listagens, "nem a lista nem o resumo tocam o banco")
	assert.Empty(t, amb.repo.resumosPedidos)
	assert.NotContains(t, amb.logs.String(), "ERROR", "resposta vazia legítima não é erro")
}

// --- ataque à recusa de chave repetida (T5, 2ª rodada) ---------------------
//
// A recusa da §12.5.5 é de BORDA, então o ataque é de FORMA da chave, e não de
// valor: se alguma variante escrever o nome da chave de um jeito que o
// `net/url` reconheça mas o `SoleQueryValue` não conte, a repetição volta a
// passar — o mesmo defeito por outra porta.
//
// A linha que separa "bypass" de "chave desconhecida" é observável: bypass é
// a URL ambígua que ainda assim APLICA um recorte; chave desconhecida é a URL
// que não aplica nenhum e responde "Tudo", que é a URL canônica e mostra MAIS,
// nunca menos. Por isso todo caso abaixo afirma o CONJUNTO devolvido, e não só
// o status.

// duasLinhasDeTipos monta a casa mínima em que "Tudo" e "Despesas" são
// distinguíveis pela contagem: uma despesa e uma receita.
func duasLinhasDeTipos(t *testing.T) *httpAmbiente {
	t.Helper()
	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria", 30_00, 9)
	amb.lancamento(minhaCasa, "acc-1", transaction.KindIncome, "Salário", 5_000_00, 5)
	return amb
}

func kindsDe(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var corpo struct {
		Items []struct {
			Kind string `json:"kind"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &corpo), "corpo: %s", rec.Body.String())
	out := make([]string, 0, len(corpo.Items))
	for _, i := range corpo.Items {
		out = append(out, i.Kind)
	}
	return out
}

// Chave ESCRITA DE OUTRO JEITO não é o `kindGroup`: ela é uma chave
// desconhecida, o servidor a ignora e responde "Tudo".
//
// Isto NÃO é o furo do ADR-030(a). Aquele proíbe tratar um VALOR desconhecido
// de uma chave conhecida como "sem filtro" — porque quem pediu um recorte
// receberia a janela inteira. Aqui não chegou chave nenhuma: a resposta é a
// mesma de quem não pediu recorte, e a tela nunca gera estas URLs (o
// `URLSearchParams` do cliente escreve o nome exato).
func TestQAChaveEscritaDeOutroJeitoNaoEOKindGroup(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"colchetes":          "kindGroup[]=expense",
		"caixa-trocada":      "KindGroup=expense",
		"tudo-maiusculo":     "KINDGROUP=expense",
		"espaco-antes":       "%20kindGroup=expense",
		"espaco-depois":      "kindGroup%20=expense",
		"ponto":              "kindGroup.=expense",
		"sufixo":             "kindGroupX=expense",
		"nulo-no-nome":       "kindGroup%00=expense",
		"ponto-e-virgula":    "kindGroup=expense;kindGroup=income",
		"duplo-encode-do-k":  "%256bindGroup=expense",
		"underscore-no-meio": "kind_Group=expense",
	}
	for nome, query := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			amb := duasLinhasDeTipos(t)
			rec := amb.chamar(t, minhaCasa, http.MethodGet,
				"/transactions?month=2026-09&"+query, "", amb.handler.List)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.ElementsMatch(t, []string{transaction.KindExpense, transaction.KindIncome},
				kindsDe(t, rec),
				"chave desconhecida não pode aplicar recorte nenhum: a resposta é Tudo")
			// E a resposta é IDÊNTICA à da URL canônica — nem meio filtro, nem
			// um resumo de outra janela.
			canonica := amb.chamar(t, minhaCasa, http.MethodGet,
				"/transactions?month=2026-09", "", amb.handler.List)
			assert.JSONEq(t, canonica.Body.String(), rec.Body.String())
		})
	}
}

// A chave PERCENT-ENCODED **é** o `kindGroup`: o `net/url` decodifica o nome
// da chave, então `%6bindGroup` é `kindGroup` e o recorte se aplica de
// verdade.
//
// Este teste existe para o par de baixo fazer sentido: sem ele, alguém poderia
// achar que o `%6b` some no caminho e que o caso repetido é 400 por acidente.
func TestQAChavePercentEncodedEOMesmoKindGroup(t *testing.T) {
	t.Parallel()

	for nome, query := range map[string]string{
		"k-minusculo": "%6bindGroup=expense",
		"k-maiusculo": "%6BindGroup=expense",
		"varias":      "%6b%69ndGroup=expense",
	} {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			amb := duasLinhasDeTipos(t)
			rec := amb.chamar(t, minhaCasa, http.MethodGet,
				"/transactions?month=2026-09&"+query, "", amb.handler.List)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, []string{transaction.KindExpense}, kindsDe(t, rec),
				"a chave decodificada É o kindGroup, e o recorte se aplica")
		})
	}
}

// O ataque de verdade: a repetição escondida atrás de encoding da CHAVE, de
// outra chave no meio, de três ocorrências e da ordem.
//
// Se a contagem fosse feita sobre o texto cru da query em vez de sobre o mapa
// já decodificado, `kindGroup=expense&%6bindGroup=income` passaria — duas
// grafias, uma chave só depois de decodificar.
func TestQARepeticaoDisfarcadaContinuaSendo400(t *testing.T) {
	t.Parallel()

	casos := map[string]string{
		"encoding-diferente-na-2a":  "kindGroup=expense&%6bindGroup=income",
		"encoding-diferente-na-1a":  "%6BindGroup=expense&kindGroup=income",
		"as-duas-encodadas":         "%6bindGroup=expense&%6BindGroup=investment",
		"separadas-por-outra-chave": "kindGroup=expense&accountId=acc-1&kindGroup=income",
		"separadas-pelo-month":      "kindGroup=expense&month=2026-09&kindGroup=income",
		"tres-ocorrencias":          "kindGroup=expense&kindGroup=expense&kindGroup=expense",
		"quatro-com-invalido":       "kindGroup=all&kindGroup=expense&kindGroup=income&kindGroup=transfer",
		"vazia-primeiro":            "kindGroup=&kindGroup=expense",
		"duas-vazias":               "kindGroup=&kindGroup=",
		"sem-igual-e-com-valor":     "kindGroup&kindGroup=expense",
		"com-lixo-junto":            "kindGroup=expense&kindGroup=" + url.QueryEscape(lixoSQL),
	}
	for nome, query := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			amb := duasLinhasDeTipos(t)
			rec := amb.chamar(t, minhaCasa, http.MethodGet,
				"/transactions?month=2026-09&"+query, "", amb.handler.List)

			require.Equal(t, http.StatusBadRequest, rec.Code,
				"chave ambígua tem de ser 400 seja qual for a grafia; corpo: %s", rec.Body.String())
			codigo, campos := corpoDeErro(t, rec)
			assert.Equal(t, httpserver.CodeValidationFailed, codigo)
			require.Contains(t, campos, "kindGroup")

			// A mensagem aponta a AÇÃO e não ecoa valor nenhum — nem os
			// válidos, nem o forjado.
			assert.NotContains(t, rec.Body.String(), lixoSQL)
			assert.NotContains(t, amb.logs.String(), lixoSQL)
			assert.NotContains(t, amb.logs.String(), "kindGroup=")
			// E nada foi ao banco.
			assert.Zero(t, amb.repo.listagens)
			assert.Empty(t, amb.repo.resumosPedidos)
		})
	}
}

const lixoSQL = "' OR 1=1 --"

// A contraprova da correção: UMA ocorrência vazia continua sendo "Tudo", 200,
// com o corpo IDÊNTICO ao da chave ausente.
//
// É o risco específico desta classe de correção — trocar "primeiro vence" por
// "recusa" e levar junto o default. A URL canônica não pode virar erro.
func TestQAUmaOcorrenciaVaziaContinuaSendoTudo(t *testing.T) {
	t.Parallel()

	amb := duasLinhasDeTipos(t)
	ausente := amb.chamar(t, minhaCasa, http.MethodGet, "/transactions?month=2026-09", "", amb.handler.List)
	require.Equal(t, http.StatusOK, ausente.Code, ausente.Body.String())

	for nome, query := range map[string]string{
		"vazia":                "kindGroup=",
		"vazia-sem-igual":      "kindGroup",
		"vazia-entre-outras":   "kindGroup=&limit=50",
		"vazia-depois-do-mes":  "limit=50&kindGroup=",
		"vazia-com-percent-6b": "%6bindGroup=",
	} {
		t.Run(nome, func(t *testing.T) {
			rec := amb.chamar(t, minhaCasa, http.MethodGet,
				"/transactions?month=2026-09&"+query, "", amb.handler.List)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.JSONEq(t, ausente.Body.String(), rec.Body.String(),
				"uma ocorrência vazia é a URL canônica, não um quinto estado")
		})
	}
}

// A assimetria da entrega, medida em vez de deduzida: `month`, `accountId`,
// `limit` e `cursor` AINDA usam `url.Values.Get` na MESMA rota.
//
// O teste fixa o que isso significa hoje, para que a dívida do ROADMAP seja
// conferível: repetir `month` sob um `kindGroup` válido **não** cruza casa,
// **não** faz lista e resumo discordarem (os dois leem o mesmo valor) e **não**
// aplica dois recortes — vence o primeiro, em silêncio. O dano é a URL
// significar coisas diferentes para leitores diferentes, e é por isso que
// `month` é o melhor candidato à próxima adoção: é o único dos quatro que
// troca QUAL dinheiro aparece.
//
// Se um dia repetir `month` passar a devolver a janela do SEGUNDO valor, ou a
// fazer o resumo falar de um mês e a lista de outro, este teste cai.
func TestQAAssimetriaDosOutrosParametrosNaoCruzaCasaNemDivergeHoje(t *testing.T) {
	t.Parallel()

	amb := novoHTTPAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(outraCasa, "acc-alheia", "Alheia", account.KindChecking)
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Setembro", 30_00, 9)
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: "acc-1", Kind: transaction.KindExpense,
		AmountCents: 77_00, Description: "Outubro",
		OccurredOn: civil.MustNew(2026, 10, 9), CompetenceMonth: "2026-10",
	})
	amb.lancamento(outraCasa, "acc-alheia", transaction.KindExpense, "Da vizinha", 99_00, 9)

	setembro := amb.chamar(t, minhaCasa, http.MethodGet,
		"/transactions?month=2026-09&kindGroup=expense", "", amb.handler.List)
	require.Equal(t, http.StatusOK, setembro.Code)

	repetido := amb.chamar(t, minhaCasa, http.MethodGet,
		"/transactions?month=2026-09&month=2026-10&kindGroup=expense", "", amb.handler.List)
	require.Equal(t, http.StatusOK, repetido.Code, repetido.Body.String())
	assert.JSONEq(t, setembro.Body.String(), repetido.Body.String(),
		"hoje vence o PRIMEIRO month, e lista e resumo leem o mesmo valor — sem divergência interna")
	assert.NotContains(t, repetido.Body.String(), "Da vizinha", "nenhuma repetição cruza a casa")
	assert.NotContains(t, repetido.Body.String(), "Outubro")

	// `accountId` da vizinha, repetido, continua 404 — não vira lista vazia
	// nem oráculo de existência.
	alheia := amb.chamar(t, minhaCasa, http.MethodGet,
		"/transactions?month=2026-09&accountId=acc-alheia&accountId=acc-1&kindGroup=expense", "", amb.handler.List)
	assert.Equal(t, http.StatusNotFound, alheia.Code, alheia.Body.String())
}
