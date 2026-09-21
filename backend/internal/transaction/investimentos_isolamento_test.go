package transaction_test

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/classify"
	"github.com/brunorblanck/homefinance/backend/internal/investment"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Condição bloqueante imposta pelo `arquiteto` na T5 (QA da E7).
//
// Os quatro métodos que o `arquiteto-dados` acrescentou a transaction.Repository
// (SumInvestmentsByMonth, ListByCategories, ListIncomeExpenseOfMonth e
// SetCategoryWhereCurrentIn) passaram a REGISTRAR o householdID de cada chamada
// em repoFake.casasConsultadas, e é sobre essa contagem que os testes deste
// arquivo são escritos.
//
// O motivo é preciso e não é estilo: um teste que só olhasse o resultado vazio
// passaria IGUAL se a consulta tivesse ido ao banco com o filtro errado — ou
// com filtro nenhum. "Não veio nada" e "não foi perguntado" são fatos
// diferentes, e o ADR-029(f) promete o segundo: casa sem categoria de
// investimento responde zeros SEM tocar no repositório de lançamentos, porque
// `IN ()` é erro de sintaxe em três dos quatro dialetos e `1=0` no outro.
//
// O serviço exercitado é o investment.Service de verdade, montado sobre os
// MESMOS dublês de transaction_test: é o único jeito de a contagem dizer
// respeito ao caminho que roda em produção.

// ledgerEspiao é o espião de repositório deste arquivo. Segue a FORMA que já
// existe no pacote — `repoEspiao`, em `autocategorize_orcamento_test.go`,
// escrito para o achado A2: embrulha a interface, delega tudo e registra o que
// a pergunta do teste precisa. Muda só a pergunta. Lá é "de que LADO da
// transação a chamada caiu"; aqui é "que IDS foram parar dentro do `IN (...)`".
//
// Ele COMPLEMENTA `casasConsultadas`, não o substitui: aquele campo responde
// "de quem é a consulta" e é onde moram as asserções desta entrega. O que ele
// não alcança é o conteúdo do filtro — uma categoria de outra casa ali é
// inofensiva HOJE, porque a consulta também é escopada por household_id, e é
// exatamente por isso que passaria despercebida até o dia em que uma consulta
// nova esquecesse o escopo.
type ledgerEspiao struct {
	transaction.Repository

	// filtrosDeCategoria guarda, em ordem, cada conjunto de ids que virou
	// `category_id IN (...)`.
	filtrosDeCategoria [][]string

	// allowlists guarda cada allowlist de SetCategoryWhereCurrentIn — o
	// conjunto que autoriza a substituição de uma escolha humana.
	allowlists [][]string

	// marcacoesEmVazio conta as chamadas de SetCategoryWhereNull. Ela não
	// entra em casasConsultadas (o dublê dela é anterior à E7 e vive em
	// service_test.go), e sem ela "nada foi escrito" provaria só metade.
	marcacoesEmVazio int
}

func (l *ledgerEspiao) SumInvestmentsByMonth(ctx context.Context, householdID string, categoryIDs []string, fromMonth, toMonth string) ([]transaction.InvestmentMonthTotals, error) {
	l.filtrosDeCategoria = append(l.filtrosDeCategoria, slices.Clone(categoryIDs))
	return l.Repository.SumInvestmentsByMonth(ctx, householdID, categoryIDs, fromMonth, toMonth)
}

func (l *ledgerEspiao) ListByCategories(ctx context.Context, householdID string, f transaction.CategoryListFilter) ([]transaction.Transaction, error) {
	l.filtrosDeCategoria = append(l.filtrosDeCategoria, slices.Clone(f.CategoryIDs))
	return l.Repository.ListByCategories(ctx, householdID, f)
}

func (l *ledgerEspiao) SetCategoryWhereNull(ctx context.Context, householdID string, ids []string, categoryID string, at time.Time) (int64, error) {
	l.marcacoesEmVazio++
	return l.Repository.SetCategoryWhereNull(ctx, householdID, ids, categoryID, at)
}

func (l *ledgerEspiao) SetCategoryWhereCurrentIn(ctx context.Context, householdID string, ids, currentCategoryIDs []string, categoryID string, at time.Time) (int64, error) {
	l.allowlists = append(l.allowlists, slices.Clone(currentCategoryIDs))
	return l.Repository.SetCategoryWhereCurrentIn(ctx, householdID, ids, currentCategoryIDs, categoryID, at)
}

// investimentosDe monta o investment.Service de produção sobre os dublês deste
// pacote, e devolve o espião junto. O relógio é o mesmo `agora` do resto da
// suíte; o logger é descartado para que a falha fechada não suje a saída.
func investimentosDe(t *testing.T, amb *ambiente) (*investment.Service, *ledgerEspiao) {
	t.Helper()

	espiao := &ledgerEspiao{Repository: amb.repo}
	svc := investment.NewService(
		espiao,
		amb.categorias,
		amb.contas,
		classify.NewLoader(amb.categorias, amb.contas),
		txDireto{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		investment.WithClock(func() time.Time { return agora }),
	)
	return svc, espiao
}

// atorDeInvestimento é o ator da casa informada. A casa vem do TOKEN, e é por
// isso que ela é o único lugar de onde o serviço pode tirá-la.
func atorDeInvestimento(casa string) investment.Actor {
	return investment.Actor{HouseholdID: casa, UserID: usuario, IP: "203.0.113.10"}
}

// --- curto-circuito da lista vazia (ADR-029f) --------------------------------

// Casa COM categorias, mas nenhuma das duas naturezas novas — o estado de toda
// casa no dia da entrega.
//
// A asserção que importa é a última: `casasConsultadas` VAZIO. As outras
// passariam mesmo se o serviço tivesse consultado o banco com filtro vazio, e é
// justamente esse o defeito que o ADR-029(f) existe para impedir.
func TestOverviewSemCategoriaMarcadaNaoConsultaOLedger(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	mercado := amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	salario := amb.categoria(minhaCasa, "cat-salario", "Salário", category.KindIncome)
	amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Mercado do bairro", 300_00, 6)
	amb.lancamento(minhaCasa, "acc-1", transaction.KindIncome, "Folha", 5_000_00, 5)
	require.Equal(t, category.KindExpense, mercado.Kind)
	require.Equal(t, category.KindIncome, salario.Kind)

	svc, espiao := investimentosDe(t, amb)
	amb.repo.casasConsultadas = nil

	v, err := svc.Overview(t.Context(), atorDeInvestimento(minhaCasa), investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Equal(t, investment.TotalsView{}, v.Monthly)
	assert.Equal(t, investment.TotalsView{}, v.YearToDate)
	assert.Len(t, v.Series, investment.SeriesMonths, "os 12 itens são promessa de contrato, não consequência de ter havido movimento")
	assert.NotNil(t, v.Items, "mês vazio é [], nunca null")
	assert.Empty(t, v.Items)
	assert.Nil(t, v.NextCursor)

	assert.Empty(t, amb.repo.casasConsultadas,
		"sem categoria de investimento, NENHUMA consulta de lançamento pode sair — IN () não existe em três dos quatro dialetos")
	assert.Empty(t, espiao.filtrosDeCategoria, "nenhum IN (...) foi montado")
}

// A vizinha ter categoria de investimento não pode ligar a consulta da minha
// casa — nem mesmo com uma fonte de categorias DEFEITUOSA, que devolve a
// taxonomia de todo mundo.
//
// A fonte defeituosa é de propósito: com um dublê que filtra por casa, o teste
// provaria apenas que o dublê filtra. Aqui ele mede a defesa em profundidade do
// SERVIÇO (carregarTaxonomia reconfere `c.HouseholdID`), que é a linha que
// impede um id alheio de virar filtro.
func TestCategoriaDeInvestimentoDaVizinhaNaoLigaAConsultaDaMinhaCasa(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.categorias.ignorarCasa = true
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(outraCasa, "acc-2", "Conta da vizinha", account.KindChecking)
	amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	alheia := amb.categoria(outraCasa, "cat-cdb-vizinha", "CDB", category.KindInvestment)

	dela := amb.lancamento(outraCasa, "acc-2", transaction.KindExpense, "CDB 15 DIAS", 9_999_00, 10)
	dela.CategoryID = ptr(alheia.ID)
	amb.repo.semear(dela)

	svc, espiao := investimentosDe(t, amb)
	amb.repo.casasConsultadas = nil

	v, err := svc.Overview(t.Context(), atorDeInvestimento(minhaCasa), investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Equal(t, investment.TotalsView{}, v.Monthly)
	assert.Empty(t, v.Items)
	assert.Empty(t, amb.repo.casasConsultadas,
		"a categoria marcada é da vizinha: para a minha casa o conjunto continua vazio, e conjunto vazio não vira consulta")
	assert.Empty(t, espiao.filtrosDeCategoria)
}

// --- isolamento entre casas (BOLA — docs/SEGURANCA.md §2) --------------------

// cenarioDuasCasasComAporte monta as DUAS casas com o mesmo mês, a mesma
// descrição e a mesma palavra-chave. É o desenho pedido no critério 10 da spec
// 0006 e na condição bloqueante: se o escopo escorregar, o número da vizinha
// aparece — e ele é grande o bastante para não passar despercebido.
func cenarioDuasCasasComAporte(t *testing.T) (*ambiente, category.Category, category.Category, transaction.Transaction, transaction.Transaction) {
	t.Helper()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	amb.conta(outraCasa, "acc-2", "Conta da vizinha", account.KindChecking)

	minha := amb.categoria(minhaCasa, "cat-cdb", "CDB", category.KindInvestment)
	alheia := amb.categoria(outraCasa, "cat-cdb-vizinha", "CDB", category.KindInvestment)
	amb.palavraDeCategoria(minhaCasa, minha.ID, "cdb")
	amb.palavraDeCategoria(outraCasa, alheia.ID, "cdb")

	minhaLinha := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "CDB 15 DIAS", 2_000_00, 10)
	linhaDela := amb.lancamento(outraCasa, "acc-2", transaction.KindExpense, "CDB 15 DIAS", 9_999_00, 10)
	return amb, minha, alheia, minhaLinha, linhaDela
}

// Overview com as duas casas no mesmo mês: só a casa do token chega ao
// repositório, e só os ids dela entram no `IN (...)`.
func TestOverviewSoConsultaACasaDoToken(t *testing.T) {
	t.Parallel()

	amb, minha, alheia, minhaLinha, linhaDela := cenarioDuasCasasComAporte(t)
	// As duas linhas já marcadas, para que a consulta tenha o que somar nos
	// dois lados.
	marcar(amb, minhaLinha.ID, minha.ID)
	marcar(amb, linhaDela.ID, alheia.ID)

	svc, espiao := investimentosDe(t, amb)
	amb.repo.casasConsultadas = nil

	v, err := svc.Overview(t.Context(), atorDeInvestimento(minhaCasa), investment.OverviewInput{Month: "2026-09"})
	require.NoError(t, err)

	assert.Equal(t, int64(2_000_00), v.Monthly.ContributionsCents, "o aporte da vizinha não pode entrar no meu mês")
	assert.Equal(t, int64(1), v.Monthly.ContributionCount)
	assert.Equal(t, int64(2_000_00), v.YearToDate.ContributionsCents)
	require.Len(t, v.Items, 1)
	assert.Equal(t, minhaLinha.ID, v.Items[0].ID)
	assert.Equal(t, minha.ID, v.Items[0].CategoryID)

	// A PROVA por contagem: houve consulta (o resultado não é vazio por falta
	// de pergunta) e TODA pergunta foi feita com a casa do token.
	require.NotEmpty(t, amb.repo.casasConsultadas, "o overview de uma casa COM aporte precisa consultar")
	for _, casa := range amb.repo.casasConsultadas {
		assert.Equal(t, minhaCasa, casa, "nenhuma consulta pode sair com a casa da vizinha")
	}

	require.NotEmpty(t, espiao.filtrosDeCategoria)
	for _, filtro := range espiao.filtrosDeCategoria {
		assert.Contains(t, filtro, minha.ID)
		assert.NotContains(t, filtro, alheia.ID, "id de categoria de outra casa nunca entra num IN (...)")
	}
}

// Detect com as duas casas no mesmo mês e a mesma descrição: marca 1, e a linha
// da vizinha continua sem categoria.
func TestDetectSoConsultaEEscreveNaCasaDoToken(t *testing.T) {
	t.Parallel()

	amb, minha, _, minhaLinha, linhaDela := cenarioDuasCasasComAporte(t)

	svc, espiao := investimentosDe(t, amb)
	amb.repo.casasConsultadas = nil

	v, err := svc.Detect(t.Context(), atorDeInvestimento(minhaCasa), investment.DetectInput{
		Month: "2026-09", DryRun: false,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), v.Marked, "só a linha da minha casa é candidata")
	assert.Equal(t, int64(0), v.Unmatched)

	require.NotNil(t, amb.repo.linhas[minhaLinha.ID].CategoryID)
	assert.Equal(t, minha.ID, *amb.repo.linhas[minhaLinha.ID].CategoryID)
	assert.Nil(t, amb.repo.linhas[linhaDela.ID].CategoryID,
		"a linha da vizinha, com a MESMA descrição e a MESMA palavra-chave, continua intocada")

	require.NotEmpty(t, amb.repo.casasConsultadas, "o detect leu o mês: o resultado não é vazio por falta de pergunta")
	for _, casa := range amb.repo.casasConsultadas {
		assert.Equal(t, minhaCasa, casa)
	}
	assert.Equal(t, 1, espiao.marcacoesEmVazio, "uma escrita, na casa do token")
}

// --- lista vazia não vira comando -------------------------------------------

// Execução real com `overwriteCategorized: true` e NADA a trocar: o UPDATE do
// overwrite não pode ser emitido.
//
// Importa porque ele é o único comando do projeto autorizado a substituir uma
// escolha humana, e porque ele leva DOIS `IN (...)`: emiti-lo com a lista de
// alvos vazia mandaria a allowlist inteira ao banco por nada — e uma allowlist
// que chega vazia por um caminho qualquer é o que transformaria a flag num
// sobrescrevedor universal.
func TestDetectNaoEmiteTrocaQuandoNaoHaNadaParaTrocar(t *testing.T) {
	t.Parallel()

	amb := novoAmbiente(t)
	amb.conta(minhaCasa, "acc-1", "Conta", account.KindChecking)
	cdb := amb.categoria(minhaCasa, "cat-cdb", "CDB", category.KindInvestment)
	mercado := amb.categoria(minhaCasa, "cat-mercado", "Mercado", category.KindExpense)
	amb.palavraDeCategoria(minhaCasa, cdb.ID, "cdb")
	amb.palavraDeCategoria(minhaCasa, mercado.ID, "mercado")

	// (1) já marcada como investimento: o WHERE recusaria, então não é
	//     ofertada nem trocada (caso 4 do `pontuar`);
	jaMarcada := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "CDB 15 DIAS", 2_000_00, 10)
	marcar(amb, jaMarcada.ID, cdb.ID)
	// (2) categorizada como despesa comum, mas a vencedora dela é a categoria
	//     COMUM: não é assunto desta rota;
	comum := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Mercado do bairro", 300_00, 6)
	marcar(amb, comum.ID, mercado.ID)
	// (3) sem categoria e sem nenhuma palavra que case.
	solta := amb.lancamento(minhaCasa, "acc-1", transaction.KindExpense, "Padaria da esquina", 12_00, 7)

	svc, espiao := investimentosDe(t, amb)
	amb.repo.casasConsultadas = nil

	v, err := svc.Detect(t.Context(), atorDeInvestimento(minhaCasa), investment.DetectInput{
		Month: "2026-09", DryRun: false, OverwriteCategorized: true,
	})
	require.NoError(t, err)

	assert.Equal(t, int64(0), v.Marked)
	assert.Equal(t, int64(1), v.Unmatched, "só a linha SEM categoria entra na contagem de não marcados")
	assert.Equal(t, int64(0), v.AlreadyCategorized, "com a flag ligada, este balde é sempre 0")

	assert.Empty(t, espiao.allowlists, "lista de alvos vazia não pode virar UPDATE — nem com a allowlist pronta")
	assert.Equal(t, 0, espiao.marcacoesEmVazio, "nada a marcar, nenhuma escrita")
	assert.Equal(t, []string{minhaCasa}, amb.repo.casasConsultadas,
		"UMA leitura do mês, na casa do token, e comando de escrita nenhum")

	// E nada mudou no banco.
	assert.Equal(t, cdb.ID, *amb.repo.linhas[jaMarcada.ID].CategoryID)
	assert.Equal(t, mercado.ID, *amb.repo.linhas[comum.ID].CategoryID)
	assert.Nil(t, amb.repo.linhas[solta.ID].CategoryID)
}

// marcar põe uma categoria numa linha já semeada, direto no dublê — é o estado
// inicial do cenário, não o caminho de escrita sob teste.
func marcar(amb *ambiente, transacaoID, categoriaID string) {
	linha := amb.repo.linhas[transacaoID]
	linha.CategoryID = ptr(categoriaID)
	amb.repo.linhas[transacaoID] = linha
}
