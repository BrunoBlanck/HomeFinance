package report_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Buracos do recorte crédito/débito (ADR-032) que a lista de critérios da E6b
// não previu, e que o QA foi procurar:
//
//   - a PARTICIPAÇÃO (`shareBp`) é apurada DENTRO do recorte, e não fatiada de
//     um cálculo global — inclusive `directShareBp`;
//   - categoria arquivada continua arquivada sob recorte;
//   - a casa no TETO estrutural (201 categorias × 50 contas = 10 050 linhas)
//     com metade das contas em cartão;
//   - `credit` é EXATAMENTE `credit_card` entre todos os tipos vivos de conta —
//     o dia em que um tipo novo nascer, ele cai em `debit` (o complemento), e
//     não em crédito por engano;
//   - casa sem conta NENHUMA;
//   - ponto e vírgula cru na query, que o net/url descarta antes de qualquer
//     código deste projeto ver o parâmetro.

// --- shareBp dentro do recorte ---------------------------------------------

// A participação de uma categoria MUDA conforme o recorte, porque o
// denominador é o total DAQUELE recorte. É a diferença entre "recortar e
// recalcular" e "recortar uma resposta pronta": se o serviço fatiasse um
// cálculo global, a mesma categoria apareceria com a mesma participação nos
// três quadros, e as fatias de um recorte não fechariam 100 %.
//
// O mesmo vale para `directShareBp`: o "direto" de um grupo pode ser 100 % do
// grupo num recorte (onde a filha não tem linha) e uma fração dele no outro.
func TestQARecorteApuraShareBpDentroDoRecorte(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	casa := a.categoria(minhaCasa, "g-casa", "Casa", category.KindExpense, nil, false)
	luz := a.categoria(minhaCasa, "f-luz", "Luz", category.KindExpense, ptr(casa.ID), false)
	lazer := a.categoria(minhaCasa, "g-lazer", "Lazer", category.KindExpense, nil, false)

	a.conta(minhaCasa, "cartao", "Cartão", account.KindCreditCard, false)
	a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)

	a.ledger.rows = []report.CategoryAccountTotal{
		linhaConta(ptr(luz.ID), "cartao", 6_000, 1),    // filha, só no cartão
		linhaConta(ptr(casa.ID), "corrente", 2_000, 1), // direto do grupo, só no débito
		linhaConta(ptr(lazer.ID), "cartao", 2_000, 1),
		linhaConta(ptr(lazer.ID), "corrente", 8_000, 1),
	}

	todas := a.relatorio(t, "", "")
	credito := a.relatorio(t, "", report.AccountGroupCredit)
	debito := a.relatorio(t, "", report.AccountGroupDebit)
	conferirParticao(t, todas, credito, debito)

	// Os três denominadores são diferentes, e é por isso que a mesma
	// categoria tem três participações.
	assert.Equal(t, int64(18_000), todas.TotalCents)
	assert.Equal(t, int64(8_000), credito.TotalCents)
	assert.Equal(t, int64(10_000), debito.TotalCents)

	pt, pc, pd := porCategoriaID(todas), porCategoriaID(credito), porCategoriaID(debito)

	// Casa: 8.000/18.000 no total, 6.000/8.000 no crédito, 2.000/10.000 no
	// débito. Se o recorte fatiasse um cálculo pronto, os três seriam 4444.
	assert.Equal(t, int64(4_444), pt[casa.ID].ShareBp, "8.000 de 18.000")
	assert.Equal(t, int64(7_500), pc[casa.ID].ShareBp, "6.000 de 8.000")
	assert.Equal(t, int64(2_000), pd[casa.ID].ShareBp, "2.000 de 10.000")

	// O "direto" do grupo acompanha: no crédito ele é zero (a linha direta é
	// de conta corrente) e no débito ele é o grupo inteiro.
	assert.Equal(t, int64(1_111), pt[casa.ID].DirectShareBp, "2.000 de 18.000")
	assert.Equal(t, int64(0), pc[casa.ID].DirectShareBp)
	assert.Equal(t, int64(0), pc[casa.ID].DirectCents)
	assert.Equal(t, int64(2_000), pd[casa.ID].DirectShareBp, "no débito o grupo é só o direto")
	assert.Equal(t, int64(2_000), pd[casa.ID].DirectCents)

	// E a filha some do recorte em que não tem linha — não fica com zero.
	require.Len(t, pt[casa.ID].Children, 1)
	require.Len(t, pc[casa.ID].Children, 1)
	assert.Equal(t, int64(7_500), pc[casa.ID].Children[0].ShareBp, "a filha é o grupo inteiro no crédito")
	assert.Empty(t, pd[casa.ID].Children, "no débito o grupo não tem filha com linha")

	// Σ shareBp == 10000 nos TRÊS (conferirInvariantes já o exige; aqui é
	// explícito, porque é o ponto do teste).
	for nome, v := range map[string]report.CategoryReportView{"todas": todas, "credit": credito, "debit": debito} {
		var soma int64
		for _, it := range v.Items {
			soma += it.ShareBp
		}
		assert.Equal(t, int64(report.BasisPointsTotal), soma, "as fatias de %q fecham 100%%", nome)
	}
}

// --- categoria arquivada sob recorte ---------------------------------------

// Arquivar uma categoria não muda o passado — nem sob recorte. O grupo e a
// filha arquivados aparecem nos três quadros com `archivedAt` preenchido e com
// o valor inteiro.
func TestQARecorteMantemCategoriaArquivada(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	grupo := a.categoria(minhaCasa, "g-arq", "Assinaturas", category.KindExpense, nil, true)
	filha := a.categoria(minhaCasa, "f-arq", "Streaming", category.KindExpense, ptr(grupo.ID), true)
	a.conta(minhaCasa, "cartao", "Cartão", account.KindCreditCard, false)
	a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)

	a.ledger.rows = []report.CategoryAccountTotal{
		linhaConta(ptr(filha.ID), "cartao", 4_990, 1),
		linhaConta(ptr(grupo.ID), "cartao", 1_000, 1),
		linhaConta(ptr(filha.ID), "corrente", 2_500, 1),
	}

	todas := a.relatorio(t, "", "")
	credito := a.relatorio(t, "", report.AccountGroupCredit)
	debito := a.relatorio(t, "", report.AccountGroupDebit)
	conferirParticao(t, todas, credito, debito)

	for nome, v := range map[string]report.CategoryReportView{"todas": todas, "credit": credito, "debit": debito} {
		g, ok := porCategoriaID(v)[grupo.ID]
		require.True(t, ok, "o grupo arquivado aparece em %q", nome)
		require.NotNil(t, g.ArchivedAt, "%q: archivedAt do grupo", nome)
		assert.Equal(t, "2026-08-01T10:30:00Z", *g.ArchivedAt, nome)
		require.Len(t, g.Children, 1, nome)
		require.NotNil(t, g.Children[0].ArchivedAt, "%q: archivedAt da filha", nome)
		assert.Equal(t, "2026-08-01T10:30:00Z", *g.Children[0].ArchivedAt, nome)
	}

	assert.Equal(t, int64(5_990), credito.TotalCents, "arquivada continua contando no cartão")
	assert.Equal(t, int64(2_500), debito.TotalCents)
}

// --- a casa no teto estrutural ---------------------------------------------

// 201 chaves de categoria × 50 contas = 10 050 linhas, o TETO exato — com
// metade das contas em cartão. É o pior caso que a estrutura permite, e nele
// a partição tem de continuar fechando, sem erro, sem aviso e com a lista de
// contas lida UMA vez por recorte.
func TestQARecorteNoTetoCom50Contas(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	contas := make([]string, 0, account.MaxPerHousehold)
	var cartoes int
	for c := 0; c < account.MaxPerHousehold; c++ {
		id := fmt.Sprintf("conta-%02d", c)
		kind := account.KindChecking
		if c%2 == 0 {
			kind = account.KindCreditCard
			cartoes++
		}
		a.conta(minhaCasa, id, fmt.Sprintf("Conta %02d", c), kind, c%7 == 0)
		contas = append(contas, id)
	}
	require.Equal(t, 25, cartoes)

	rows := make([]report.CategoryAccountTotal, 0, tetoDeLinhas)
	var esperadoCredito, esperadoDebito int64
	for i := 0; i < category.MaxPerHousehold; i++ {
		id := fmt.Sprintf("c-%03d", i)
		a.categoria(minhaCasa, id, "Cat "+id, category.KindExpense, nil, false)
		for c, conta := range contas {
			valor := int64(i+1) * 2
			rows = append(rows, linhaConta(ptr(id), conta, valor, 1))
			if c%2 == 0 {
				esperadoCredito += valor
			} else {
				esperadoDebito += valor
			}
		}
	}
	for c, conta := range contas { // o balde, em todas as 50 contas
		rows = append(rows, linhaConta(nil, conta, 3, 1))
		if c%2 == 0 {
			esperadoCredito += 3
		} else {
			esperadoDebito += 3
		}
	}
	require.Len(t, rows, tetoDeLinhas, "exatamente o teto")
	a.ledger.rows = rows

	todas := a.relatorio(t, "", "")
	credito := a.relatorio(t, "", report.AccountGroupCredit)
	debito := a.relatorio(t, "", report.AccountGroupDebit)
	conferirParticao(t, todas, credito, debito)

	assert.Equal(t, esperadoCredito, credito.TotalCents)
	assert.Equal(t, esperadoDebito, debito.TotalCents)
	assert.Len(t, todas.Items, category.MaxPerHousehold+1, "as 50 linhas de cada categoria viram UM item")
	assert.Len(t, credito.Items, category.MaxPerHousehold+1)
	assert.Len(t, debito.Items, category.MaxPerHousehold+1)
	assert.Equal(t, int64(tetoDeLinhas), todas.Count)
	assert.Equal(t, int64(25*(category.MaxPerHousehold+1)), credito.Count)

	assert.NotContains(t, a.logs.String(), `"level":"WARN"`, "toda conta é da casa: nada anômalo")
	assert.Len(t, a.contas.chamadas, 2, "uma leitura de contas por recorte, nenhuma sob 'todas'")
	for _, c := range a.contas.chamadas {
		assert.Equal(t, chamadaContas{minhaCasa, true}, c)
	}
}

// --- o vocabulário fechado, contra a lista viva de tipos de conta ----------

// `credit` é EXATAMENTE `credit_card`, e `debit` é o complemento — conferido
// contra `account.Kinds()`, e não contra uma lista copiada no teste. Um tipo
// de conta novo cai em `debit` (que é o desenho do ADR-032b: débito é o
// complemento, não uma segunda lista); o que este teste barra é ele aparecer
// em `credit` por engano.
func TestQACreditEhExatamenteCreditCardEntreOsTiposVivos(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	g := a.categoria(minhaCasa, "g", "Casa", category.KindExpense, nil, false)
	kinds := account.Kinds()
	require.NotEmpty(t, kinds)

	var esperadoCredito, esperadoDebito int64
	rows := make([]report.CategoryAccountTotal, 0, len(kinds))
	for i, kind := range kinds {
		id := fmt.Sprintf("conta-%s", kind)
		a.conta(minhaCasa, id, "Conta "+kind, kind, false)
		valor := int64(i+1) * 1_000
		rows = append(rows, linhaConta(ptr(g.ID), id, valor, 1))
		if kind == account.KindCreditCard {
			esperadoCredito += valor
		} else {
			esperadoDebito += valor
		}
	}
	a.ledger.rows = rows

	credito := a.relatorio(t, "", report.AccountGroupCredit)
	debito := a.relatorio(t, "", report.AccountGroupDebit)
	todas := a.relatorio(t, "", "")
	conferirParticao(t, todas, credito, debito)

	assert.Equal(t, esperadoCredito, credito.TotalCents, "só credit_card entre %v", kinds)
	assert.Equal(t, esperadoDebito, debito.TotalCents, "todo o resto, tipo novo incluído")
	assert.Equal(t, int64(1), credito.Count, "uma conta de cartão entre os tipos vivos")
	assert.Equal(t, int64(len(kinds)-1), debito.Count)
}

// --- casa sem conta nenhuma -------------------------------------------------

// Casa sem NENHUMA conta (só chega a este estado com banco adulterado — não
// há lançamento sem conta): `credit` é vazio e `debit` fica com tudo, por
// falha ABERTA, avisando uma vez. Nenhum dos dois derruba a requisição.
func TestQARecorteEmCasaSemContaNenhuma(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	g := a.categoria(minhaCasa, "g", "Casa", category.KindExpense, nil, false)
	a.ledger.rows = []report.CategoryAccountTotal{
		linhaConta(ptr(g.ID), "conta-que-sumiu", 4_200, 2),
	}

	credito := a.relatorio(t, "", report.AccountGroupCredit)
	assert.Equal(t, int64(0), credito.TotalCents)
	assert.Empty(t, credito.Items)
	require.NotNil(t, credito.AccountGroup)
	a.logs.Reset()

	debito := a.relatorio(t, "", report.AccountGroupDebit)
	assert.Equal(t, int64(4_200), debito.TotalCents, "falha aberta: o dinheiro aparece")
	assert.Equal(t, int64(2), debito.Count)
	assert.Contains(t, a.logs.String(), `"conta_desconhecida":1`)
	assert.NotContains(t, a.logs.String(), "4200", "nunca centavos")
}

// --- ponto e vírgula cru na query ------------------------------------------

// DEFESA EM PROFUNDIDADE (o achado de QA foi CORRIGIDO na cadeia, 18/09/2026).
//
// `net/url` DESCARTA o par inteiro da query que ele não consegue ler — desde o
// Go 1.17, `parseQuery` marca erro e pula o par, e `r.URL.Query()` ignora esse
// erro. Vale para `;` cru, para `%` solto e para escape percentual inválido; a
// classe é "query malformada é engolida em silêncio", não "ponto e vírgula".
// Hoje a cadeia global recusa essas requisições com 400 antes do mux
// (httpserver.WellFormedQuery), e é lá que os casos de ponta a ponta moram
// (internal/report/query_malformada_test.go).
//
// Este teste continua valendo, e chama o handler DIRETO, sem a cadeia — de
// propósito: ele mede o que sobra se a guarda da cadeia sair (por uma
// refatoração da composição, por um servidor de teste que monte a cadeia à
// mão, por um proxy que reescreva a query). Mesmo sem ela, NENHUM recorte
// escondido é aplicado e o eco NUNCA mente: a resposta devolve sempre o
// recorte que VALEU — `accountGroup: null` quando nenhum valeu —, então
// nenhum cliente mostra números de todas as contas sob o rótulo "crédito".
func TestQAPontoEVirgulaCruNaQueryNaoAplicaRecorteEscondido(t *testing.T) {
	t.Parallel()

	casos := map[string]struct {
		alvo     string
		esperado *string // o eco esperado de accountGroup
	}{
		"valor com ; cru":           {"/api/v1/reports/by-category?month=2026-09&accountGroup=credit;debit", nil},
		"; no fim do valor":         {"/api/v1/reports/by-category?month=2026-09&accountGroup=credit;", nil},
		"repetido com ; no segundo": {"/api/v1/reports/by-category?month=2026-09&accountGroup=credit&accountGroup=debit;", ptr(report.AccountGroupCredit)},
	}

	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			t.Parallel()
			a := novoHTTPAmbiente(t)
			a.conta(minhaCasa, "cartao", "Cartão", account.KindCreditCard, false)
			a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)
			a.ledger.rows = []report.CategoryAccountTotal{
				linhaConta(nil, "cartao", 1_000, 1),
				linhaConta(nil, "corrente", 2_000, 1),
			}

			rec := a.chamar(t, minhaCasa, c.alvo)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var v report.CategoryReportView
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v))

			// O eco NUNCA mente sobre o recorte que valeu.
			if c.esperado == nil {
				assert.Nil(t, v.AccountGroup, "sem recorte aplicado, o eco é null")
				assert.Equal(t, int64(3_000), v.TotalCents, "todas as contas")
				assert.Empty(t, a.contas.chamadas, "sem recorte: a lista de contas nem é lida")
			} else {
				require.NotNil(t, v.AccountGroup)
				assert.Equal(t, *c.esperado, *v.AccountGroup)
				assert.Equal(t, int64(1_000), v.TotalCents, "o recorte ecoado é o recorte aplicado")
			}
			// Em nenhum caso o `;` vira texto de consulta.
			require.Len(t, a.ledger.chamadas, 1)
			assert.Equal(t, minhaCasa, a.ledger.chamadas[0].casa)
			assert.Equal(t, "2026-09", a.ledger.chamadas[0].mes)
			assert.Equal(t, transaction.KindExpense, a.ledger.chamadas[0].kind)
			assert.NotContains(t, a.logs.String(), ";")
		})
	}
}
