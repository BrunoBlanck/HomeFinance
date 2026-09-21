package transaction_test

import (
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/cardstatement"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// O id GRAVADO é o CANÔNICO — o que o banco devolveu —, nunca a string que o
// cliente mandou.
//
// # O defeito que estes testes trancam
//
// A POSSE de um id é conferida em SQL (`WHERE id = ?` no `ByID` do
// repositório), e a semântica dessa igualdade vem da COLLATION da coluna. As
// colunas de id são `varchar(36)` sem collation declarada e nada fixa charset
// na abertura da conexão, então:
//
//   - MySQL 8      → `utf8mb4_0900_ai_ci`: ignora CAIXA e acento;
//   - MSSQL        → `CI_AS` com padding ANSI: ignora ESPAÇO À DIREITA;
//   - SQLite/PG    → `=` binário: não ignora nada.
//
// A IDENTIDADE do mesmo id, depois, é comparada em GO, byte a byte: o recorte
// crédito/débito do relatório e o painel casam `account_id` contra um mapa das
// contas da casa, a listagem resolve o nome da conta assim, e `/transfers`
// filtra os pares e monta os saldos por chave de mapa. As duas semânticas não
// concordam — e é nessa fresta que mora o defeito: um id ACEITO pelo SQL e
// GRAVADO como veio do cliente produz uma linha que NENHUM mapa em Go
// encontra. O gasto de cartão some do quadro "Despesas no crédito", o painel
// concorda em errar, e o único sinal na tela de lançamentos é o nome da conta
// sair vazio.
//
// # Por que o dublê, e não um dialeto
//
// Em SQLite e PostgreSQL o `=` é sensível a caixa e a espaço, então o `ByID`
// responde 404 e o defeito NÃO REPRODUZ — é por isso que nenhum teste cruzado
// contra SQLite poderia flagrá-lo. MySQL/MSSQL exigiriam testcontainers, que
// não está no go.mod nem há Docker nesta máquina. A saída é reproduzir a
// COLLATION FROUXA no dublê (contasFake.colacaoFrouxa e as irmãs), que é o
// molde de dublê furado que o pacote já usa: o dublê devolve, para um id
// pedido com caixa trocada ou espaço à direita, a entidade cujo `.ID` é o
// canônico — exatamente o que MySQL e MSSQL fazem.
//
// Todo teste aqui afirma sobre o que foi GRAVADO (ou sobre o que o filtro em
// Go encontrou), nunca sobre o que foi pedido.

const (
	idContaCanonica  = "00000000-0000-7000-b000-000000000001"
	idContaCanonicaB = "00000000-0000-7000-b000-000000000002"
	idCatCanonica    = "00000000-0000-7000-c000-000000000001"
	idFaturaCanonica = "00000000-0000-7000-d000-000000000001"
	idGrupoCanonico  = "00000000-0000-7000-e000-000000000001"
)

// osDoisDialetosFrouxos são as duas grafias que passam pelo `WHERE id = ?` de
// um dialeto real sem serem o id canônico.
func osDoisDialetosFrouxos(canonico string) map[string]string {
	return map[string]string{
		"caixa trocada (MySQL 8, utf8mb4_0900_ai_ci)": strings.ToUpper(canonico),
		"espaço à direita (MSSQL, padding ANSI)":      canonico + "   ",
	}
}

func TestCreateBatchGravaOIdCanonicoDaContaENaoAStringDoCliente(t *testing.T) {
	t.Parallel()

	for nome, pedido := range osDoisDialetosFrouxos(idContaCanonica) {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			amb := novoAmbiente(t)
			amb.conta(minhaCasa, idContaCanonica, "Cartão", account.KindCreditCard)
			amb.contas.colacaoFrouxa = true

			res, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(
				linha(pedido, 11_00, 5, "cafe"),
			))
			require.NoError(t, err, "o ByID do dialeto frouxo ACEITA este id — é essa a premissa do defeito")
			require.Len(t, res.IDs, 1)

			gravada := amb.repo.linhas[res.IDs[0]]
			assert.Equal(t, idContaCanonica, gravada.AccountID,
				"o que vai para account_id é o .ID da entidade lida, nunca a string pedida")
			assert.NotEqual(t, pedido, gravada.AccountID)
		})
	}
}

// A mesma regra vale para os OUTROS dois ids que a linha carrega: a categoria
// e a fatura. São o mesmo padrão no mesmo literal — entidade carregada, string
// do cliente gravada —, e a fatura tem um agravante próprio: a conferência
// `fatura.AccountID != conta.ID` é uma comparação em Go entre um id do banco e
// (antes desta correção) um id do cliente, então a fatura CERTA parecia ser de
// outra conta e a importação inteira morria em 422.
func TestCreateBatchGravaOIdCanonicoDaCategoriaEDaFatura(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, idContaCanonica, "Cartão", account.KindCreditCard)
	amb.categoria(minhaCasa, idCatCanonica, "Mercado", category.KindExpense)
	amb.faturas.add(cardstatement.Statement{
		ID: idFaturaCanonica, HouseholdID: minhaCasa, AccountID: idContaCanonica,
		CompetenceMonth: "2026-10",
		ClosingDate:     civil.MustNew(2026, 9, 30),
		DueDate:         civil.MustNew(2026, 10, 10),
	})
	amb.contas.colacaoFrouxa = true
	amb.categorias.colacaoFrouxa = true
	amb.faturas.colacaoFrouxa = true

	l := linha(strings.ToUpper(idContaCanonica), 11_00, 5, "cafe")
	l.CategoryID = ptr(strings.ToUpper(idCatCanonica))
	l.StatementID = ptr(idFaturaCanonica + "  ")

	res, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(l))
	require.NoError(t, err, "a fatura é DESTA conta: a comparação tem de ser canônica dos dois lados")
	require.Len(t, res.IDs, 1)

	gravada := amb.repo.linhas[res.IDs[0]]
	assert.Equal(t, idContaCanonica, gravada.AccountID)
	require.NotNil(t, gravada.CategoryID)
	assert.Equal(t, idCatCanonica, *gravada.CategoryID)
	require.NotNil(t, gravada.StatementID)
	assert.Equal(t, idFaturaCanonica, *gravada.StatementID)
	// ADR-023(b): a competência veio da FATURA, o que só acontece se ela foi
	// reconhecida como sendo da conta do lançamento.
	assert.Equal(t, "2026-10", gravada.CompetenceMonth)
}

// A canonização não pode ABRIR o que a validação de forma fechava.
//
// `validarPares` recusa transferência com as duas pernas na mesma conta — mas
// roda ANTES de qualquer consulta, sobre as strings do cliente. Com a caixa
// trocada, `"<UUID>"` e `"<uuid>"` são duas strings para o Go e a MESMA conta
// para o MySQL: sem a reconferência canônica, o par passaria e gravaria
// dinheiro saindo e entrando no mesmo lugar, dobrando a linha no extrato.
func TestCreateBatchRecusaParQueSoFicaNaMesmaContaDepoisDeCanonizar(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, idContaCanonica, "Conta", account.KindChecking)
	amb.contas.colacaoFrouxa = true

	grupo := idGrupoCanonico
	perna := func(kind, conta, semente string) transaction.NewTransaction {
		return transaction.NewTransaction{
			Kind: kind, AccountID: conta, AmountCents: 10_00,
			Description: "Transferência", OccurredOn: civil.MustNew(2026, 9, 5),
			DedupKey: chave(semente), TransferGroupID: &grupo,
		}
	}

	_, err := amb.svc.CreateBatch(t.Context(), ator(minhaCasa), loteValido(
		perna(transaction.KindTransferOut, idContaCanonica, "saida"),
		perna(transaction.KindTransferIn, strings.ToUpper(idContaCanonica), "entrada"),
	))
	require.ErrorIs(t, err, transaction.ErrBrokenTransfer)
	assert.Zero(t, amb.repo.criadas, "nada pode ter sido gravado")
}

// O filtro de conta de GET /transactions desce CANÔNICO para a consulta.
//
// Com a string do cliente, o `ByID` do dialeto frouxo aceitaria o id (a rota
// não daria 404), a consulta levaria a grafia do cliente e a lista sairia
// VAZIA — uma tela que afirma "conta X, setembro" e mostra nada.
func TestListUsaOIdCanonicoDaContaNoFiltro(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, idContaCanonica, "Nubank", account.KindChecking)
	amb.repo.semear(transaction.Transaction{
		HouseholdID: minhaCasa, AccountID: idContaCanonica, Kind: transaction.KindExpense,
		AmountCents: 50_00, Description: "Padaria", OccurredOn: civil.MustNew(2026, 9, 5),
		CompetenceMonth: "2026-09",
	})
	amb.contas.colacaoFrouxa = true

	view, err := amb.svc.List(t.Context(), ator(minhaCasa), transaction.ListInput{
		Month: "2026-09", AccountID: strings.ToUpper(idContaCanonica),
	})
	require.NoError(t, err)
	require.Len(t, view.Items, 1, "com a string do cliente no WHERE a lista sairia vazia")
	assert.Equal(t, idContaCanonica, view.Items[0].AccountID)
	assert.Equal(t, "Nubank", view.Items[0].AccountName)
	assert.Equal(t, int64(50_00), view.Summary.ExpenseCents, "o resumo lê a MESMA janela da lista")
}

// `/transfers` é o caso mais agudo: o filtro de conta é aplicado em GO
// (paresDoMes compara byte a byte) e vira CHAVE DE MAPA (saldosNoFimDoMes).
// Sem canonizar, o `ByID` aceita, nenhum par casa, nenhum nome é encontrado —
// e a tela diz "nenhuma transferência neste mês" para uma conta cheia delas.
func TestListTransfersUsaOIdCanonicoNoFiltroEmGo(t *testing.T) {
	t.Parallel()

	for nome, pedido := range osDoisDialetosFrouxos(idContaCanonica) {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()

			amb := novoAmbiente(t)
			amb.conta(minhaCasa, idContaCanonica, "Nubank", account.KindChecking)
			amb.conta(minhaCasa, idContaCanonicaB, "C6", account.KindChecking)
			amb.parDeTransferencia(minhaCasa, idContaCanonica, idContaCanonicaB, 300_00, 3, idGrupoCanonico)
			amb.contas.colacaoFrouxa = true

			view, err := amb.svc.ListTransfers(t.Context(), ator(minhaCasa), transaction.TransferListInput{
				Month: "2026-09", AccountID: pedido,
			})
			require.NoError(t, err)
			require.Len(t, view.Items, 1, "a página é filtrada na consulta")
			require.Len(t, view.Pairs, 1, "os pares do mês são filtrados em GO")
			assert.Equal(t, idContaCanonica, view.Pairs[0].AccountAID)
			require.NotEmpty(t, view.Balances, "o saldo usa o id como CHAVE DE MAPA")
			assert.Equal(t, idContaCanonica, view.Balances[0].AccountID)
			assert.Equal(t, "Nubank", view.Balances[0].AccountName)
		})
	}
}

// A recusa de "conta igual à contraparte" também é reconferida sobre os ids
// canônicos: comparar as strings do cliente deixaria passar a mesma conta
// escrita de dois jeitos.
func TestListTransfersRecusaMesmaContaAindaQueEscritaDeDoisJeitos(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, idContaCanonica, "Nubank", account.KindChecking)
	amb.contas.colacaoFrouxa = true

	_, err := amb.svc.ListTransfers(t.Context(), ator(minhaCasa), transaction.TransferListInput{
		Month:                "2026-09",
		AccountID:            idContaCanonica,
		CounterpartAccountID: strings.ToUpper(idContaCanonica),
	})
	require.ErrorIs(t, err, transaction.ErrSameAccountFilter)
}
