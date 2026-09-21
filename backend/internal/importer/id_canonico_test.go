package importer_test

import (
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A importação grava o id CANÔNICO da conta — o que o banco devolveu —, nunca
// a string que o cliente mandou.
//
// O raciocínio completo do defeito está em
// `internal/transaction/id_canonico_test.go`. Em uma frase: a POSSE do id é
// conferida em SQL (`WHERE id = ?`, cuja igualdade vem da COLLATION da coluna
// — insensível a caixa no MySQL 8 e a espaço à direita no MSSQL) e a
// IDENTIDADE do mesmo id é comparada depois em GO, byte a byte (o recorte
// crédito/débito do relatório, o painel, o nome da conta na listagem). Guardar
// a string do cliente deixa uma linha que o SQL encontra e que nenhum mapa em
// Go encontra.
//
// Aqui o teste é de ponta a ponta, contra SQLite real, com o dublê
// `contasComColacaoFrouxa` fazendo o `ByID` casar como MySQL/MSSQL casariam.
// Ele cobre os DOIS registros que a importação escreve com id de conta:
// `import_batches.account_id` (fase 1) e `transactions.account_id` (fase 2).

// contaComIdDeLetras cria uma conta corrente do Nubank com um id canônico que
// contém letras hexadecimais — sem elas, "caixa trocada" não existiria como
// caso (o gerador do harness produz ids só de dígitos).
func contaComIdDeLetras(t *testing.T, a *ambiente, id string) *account.Account {
	t.Helper()
	nomeLimpo, norm, err := account.NormalizeName("Nubank Conta")
	require.NoError(t, err)

	agora := a.relogio.now()
	c := &account.Account{
		ID:          id,
		HouseholdID: a.casa.ID,
		Name:        nomeLimpo,
		NameNorm:    norm,
		Kind:        account.KindChecking,
		Institution: "nubank",
		OpeningDate: civil.MustNew(2026, 1, 1),
		CreatedAt:   agora,
		UpdatedAt:   agora,
	}
	require.NoError(t, a.repoConta.Create(t.Context(), c))
	return c
}

func TestImportacaoGravaOIdCanonicoDaContaNoLoteENosLancamentos(t *testing.T) {
	t.Parallel()

	const idCanonico = "00000000-0000-7000-bbbb-0000000000ab"

	casos := map[string]string{
		"caixa trocada (MySQL 8, utf8mb4_0900_ai_ci)": strings.ToUpper(idCanonico),
		"espaço à direita (MSSQL, padding ANSI)":      idCanonico + "   ",
	}
	for nome, pedido := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			a := novoAmbiente(t)
			conta := contaComIdDeLetras(t, a, idCanonico)
			a.contas.frouxa.Store(true)

			// Fase 1: a análise. O `ByID` do dialeto frouxo ACEITA este id —
			// é essa a premissa do defeito —, e o lote tem de nascer com o
			// id canônico mesmo assim.
			lote, err := a.svc.Analyze(t.Context(), a.ator(), importer.AnalyzeInput{
				AccountID: pedido,
				FileName:  "extrato.csv",
				Content:   fixtureExtrato(t),
			})
			require.NoError(t, err)
			assert.Equal(t, conta.ID, lote.AccountID, "import_batches.account_id é o id canônico")
			assert.NotEqual(t, pedido, lote.AccountID)

			// Fase 2: o confirm. Ele lê a conta do LOTE, então o que ele
			// grava só é canônico porque a fase 1 canonizou.
			_, err = a.svc.Confirm(t.Context(), a.ator(), lote.ID, importer.ConfirmInput{
				Decisions: []importer.Decision{},
			})
			require.NoError(t, err)

			lancamentos := a.lancamentosDa(t, a.casa.ID, "2026-08")
			require.NotEmpty(t, lancamentos, "o extrato de agosto entrou")
			for _, l := range lancamentos {
				assert.Equal(t, conta.ID, l.AccountID,
					"transactions.account_id é o id canônico — é ele que o relatório e o painel casam em Go")
			}
		})
	}
}

// A borda de POST /imports recusa o `accountId` fora da forma canônica de
// UUID, ANTES de ele virar `WHERE id = ?`.
//
// É a metade do problema que a FORMA resolve: espaço, controle, aspas,
// percent-encoding, id maior que a coluna. A outra metade — caixa trocada, que
// é forma canônica válida — só se fecha canonizando, e é o que o teste acima
// mede. A recusa é a MESMA de "não escolhi conta nenhuma": mesma mensagem,
// sem eco do valor recusado.
func TestBordaDeImportsRecusaAccountIdForaDaFormaCanonica(t *testing.T) {
	t.Parallel()

	a := novoAmbiente(t)
	conta := a.contaCorrente(t)

	malformados := map[string]string{
		"vazio":               "",
		"id curto":            "acc-1",
		"sem hífen":           strings.ReplaceAll(conta.ID, "-", ""),
		"com aspas":           "00000000-0000-7000-bbbb-00000000'ab",
		"hífen fora do lugar": "00000000-0000-7000-bbbb0-000000000a",
	}
	for nome, valor := range malformados {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			rec := enviarPelaAPI(t, a, valor, "extrato.csv", fixtureExtrato(t))
			require.Equal(t, 400, rec.Code, rec.Body.String())
			corpo := rec.Body.String()
			assert.Contains(t, corpo, `"accountId"`)
			if valor != "" {
				assert.NotContains(t, corpo, valor, "a recusa NUNCA ecoa o valor recebido")
			}
		})
	}

	// E o id canônico da própria casa continua passando.
	rec := enviarPelaAPI(t, a, conta.ID, "extrato.csv", fixtureExtrato(t))
	require.Equal(t, 201, rec.Code, rec.Body.String())
}
