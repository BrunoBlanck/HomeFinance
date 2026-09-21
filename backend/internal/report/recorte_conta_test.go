package report_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/account"
	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/report"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// O recorte crédito/débito do relatório por categoria (ADR-032), visto do
// serviço: a allowlist exata de `accountGroup`, a ordem das guardas, a
// PARTIÇÃO (`credit` + `debit` == todas, por total, por contagem e por
// categoria), o que cada lado inclui, a casa sem cartão, a conta órfã e o
// aviso agregado.

// porCategoriaID indexa os itens da resposta pelo id ("" para o balde).
func porCategoriaID(v report.CategoryReportView) map[string]report.CategoryReportGroupView {
	out := make(map[string]report.CategoryReportGroupView, len(v.Items))
	for _, it := range v.Items {
		chave := ""
		if it.CategoryID != nil {
			chave = *it.CategoryID
		}
		out[chave] = it
	}
	return out
}

// conferirParticao é o critério central do ADR-032: para o mesmo mês e
// natureza, `credit` e `debit` são partições de `todas` — por total, por
// contagem, por item de nível 1 (inclusive o balde) e por filha.
func conferirParticao(t *testing.T, todas, credito, debito report.CategoryReportView) {
	t.Helper()
	assert.Nil(t, todas.AccountGroup)
	require.NotNil(t, credito.AccountGroup)
	require.NotNil(t, debito.AccountGroup)
	assert.Equal(t, report.AccountGroupCredit, *credito.AccountGroup)
	assert.Equal(t, report.AccountGroupDebit, *debito.AccountGroup)

	assert.Equal(t, todas.TotalCents, credito.TotalCents+debito.TotalCents, "total(credit) + total(debit) == total(todas)")
	assert.Equal(t, todas.Count, credito.Count+debito.Count, "count(credit) + count(debit) == count(todas)")

	pc, pd := porCategoriaID(credito), porCategoriaID(debito)
	vistos := map[string]bool{}
	for chave, it := range porCategoriaID(todas) {
		vistos[chave] = true
		c, d := pc[chave], pd[chave]
		assert.Equal(t, it.TotalCents, c.TotalCents+d.TotalCents, "item %q: total", chave)
		assert.Equal(t, it.Count, c.Count+d.Count, "item %q: count", chave)
		assert.Equal(t, it.DirectCents, c.DirectCents+d.DirectCents, "item %q: direto", chave)
		assert.Equal(t, it.DirectCount, c.DirectCount+d.DirectCount, "item %q: contagem direta", chave)

		filhas := func(g report.CategoryReportGroupView) map[string]report.CategoryReportChildView {
			out := map[string]report.CategoryReportChildView{}
			for _, f := range g.Children {
				out[f.CategoryID] = f
			}
			return out
		}
		fc, fd := filhas(c), filhas(d)
		for _, f := range it.Children {
			assert.Equal(t, f.TotalCents, fc[f.CategoryID].TotalCents+fd[f.CategoryID].TotalCents, "filha %q: total", f.CategoryID)
			assert.Equal(t, f.Count, fc[f.CategoryID].Count+fd[f.CategoryID].Count, "filha %q: count", f.CategoryID)
		}
	}
	// Nenhum item aparece num recorte sem aparecer em "todas".
	for chave := range pc {
		assert.True(t, vistos[chave], "item %q só existe em credit", chave)
	}
	for chave := range pd {
		assert.True(t, vistos[chave], "item %q só existe em debit", chave)
	}
}

// relatorio chama o serviço para o recorte pedido, exigindo sucesso.
func (a *ambiente) relatorio(t *testing.T, kind, grupo string) report.CategoryReportView {
	t.Helper()
	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09", Kind: kind, AccountGroup: grupo})
	require.NoError(t, err)
	conferirInvariantes(t, v)
	return v
}

// --- allowlist e ordem das guardas ----------------------------------------

// Vazio é "todas as contas": `accountGroup: null` na resposta, e a lista de
// contas NÃO é carregada — o caminho padrão continua com as duas leituras.
func TestByCategoryAccountGroupVazioEhTodasSemCarregarContas(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	g := a.categoria(minhaCasa, "g", "Casa", category.KindExpense, nil, false)
	a.conta(minhaCasa, "cartao", "Cartão", account.KindCreditCard, false)
	a.ledger.rows = []report.CategoryAccountTotal{linhaConta(ptr(g.ID), "cartao", 1_000, 1)}

	v := a.relatorio(t, "", "")
	assert.Nil(t, v.AccountGroup)
	assert.Equal(t, int64(1_000), v.TotalCents)
	assert.Empty(t, a.contas.chamadas, "sem recorte, a lista de contas não é lida")
	require.Len(t, a.cats.chamadas, 1)

	// E o JSON traz a chave com null, não a omite.
	bruto, err := json.Marshal(v)
	require.NoError(t, err)
	assert.Contains(t, string(bruto), `"accountGroup":null`)
}

// `credit` e `debit` são ecoados como a CONSTANTE, e a lista de contas é
// carregada com as arquivadas e para a casa do token.
func TestByCategoryAccountGroupEcoaAConstante(t *testing.T) {
	t.Parallel()
	for _, grupo := range []string{report.AccountGroupCredit, report.AccountGroupDebit} {
		t.Run(grupo, func(t *testing.T) {
			t.Parallel()
			a := novoAmbiente(t)
			a.conta(minhaCasa, "cartao", "Cartão", account.KindCreditCard, false)
			a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)
			a.ledger.rows = []report.CategoryAccountTotal{
				linhaConta(nil, "cartao", 100, 1), linhaConta(nil, "corrente", 200, 1),
			}

			v := a.relatorio(t, "", grupo)
			require.NotNil(t, v.AccountGroup)
			assert.Equal(t, grupo, *v.AccountGroup)
			require.Len(t, a.contas.chamadas, 1)
			assert.Equal(t, chamadaContas{minhaCasa, true}, a.contas.chamadas[0], "casa do token, arquivadas incluídas")

			bruto, err := json.Marshal(v)
			require.NoError(t, err)
			assert.Contains(t, string(bruto), `"accountGroup":"`+grupo+`"`)
		})
	}
}

// Allowlist FECHADA e exata: nada é normalizado, e nenhum hostil chega ao
// ledger, às categorias ou às contas.
func TestByCategoryAccountGroupHostilEhErroSemConsultar(t *testing.T) {
	t.Parallel()

	hostis := []string{
		"CREDIT", "Credit", " credit", "credit ", "credit_card", "credit,debit", "debit;",
		"'; DROP TABLE transactions;--", "%00credit", "credit\x00", "all", "todas", "null", "0", "1",
		"crédit", "ｃredit", "credit\n", "debit\r\n", strings.Repeat("credit", 2_000),
	}
	for _, valor := range hostis {
		t.Run(fmt.Sprintf("%q", valor), func(t *testing.T) {
			t.Parallel()
			a := novoAmbiente(t)
			a.ledger.rows = []report.CategoryAccountTotal{linha(nil, 1, 1)}
			_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09", AccountGroup: valor})
			assert.ErrorIs(t, err, report.ErrInvalidAccountGroup)
			assert.Empty(t, a.ledger.chamadas, "recorte inválido não chega ao banco")
			assert.Empty(t, a.cats.chamadas)
			assert.Empty(t, a.contas.chamadas)
			// O valor recusado não viaja no erro.
			assert.NotContains(t, err.Error(), "DROP")
			assert.NotContains(t, err.Error(), "CREDIT")
		})
	}
}

// Ordem das guardas — uma falha por resposta: mês inválido vence o recorte
// inválido; natureza inválida vence o recorte inválido; e o recorte inválido
// só aparece quando mês e natureza estão certos.
func TestByCategoryOrdemDasGuardasComAccountGroup(t *testing.T) {
	t.Parallel()

	t.Run("mês inválido + accountGroup inválido → mês", func(t *testing.T) {
		t.Parallel()
		a := novoAmbiente(t)
		_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-13", AccountGroup: "CREDIT"})
		assert.ErrorIs(t, err, transaction.ErrInvalidMonth)
		assert.NotErrorIs(t, err, report.ErrInvalidAccountGroup)
		assert.Empty(t, a.ledger.chamadas)
	})

	t.Run("kind inválido + accountGroup inválido → kind", func(t *testing.T) {
		t.Parallel()
		a := novoAmbiente(t)
		_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09", Kind: "transfer_out", AccountGroup: "CREDIT"})
		assert.ErrorIs(t, err, report.ErrInvalidKind)
		assert.NotErrorIs(t, err, report.ErrInvalidAccountGroup)
		assert.Empty(t, a.ledger.chamadas)
	})

	t.Run("sem casa + accountGroup inválido → não autenticado", func(t *testing.T) {
		t.Parallel()
		a := novoAmbiente(t)
		_, err := a.svc.ByCategory(t.Context(), report.Actor{}, report.ByCategoryInput{Month: "2026-09", AccountGroup: "CREDIT"})
		assert.ErrorIs(t, err, report.ErrUnauthenticated)
	})

	t.Run("mês e kind certos + accountGroup inválido → accountGroup", func(t *testing.T) {
		t.Parallel()
		a := novoAmbiente(t)
		_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09", Kind: "income", AccountGroup: "CREDIT"})
		assert.ErrorIs(t, err, report.ErrInvalidAccountGroup)
		assert.Empty(t, a.ledger.chamadas)
	})
}

// --- o que cada lado inclui -----------------------------------------------

// `debit` é o COMPLEMENTO: cash, checking, savings e other — vivas e
// arquivadas —, e `credit` é SÓ credit_card, arquivado incluído.
func TestByCategoryCreditEhSoCartaoEDebitEhOResto(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	g := a.categoria(minhaCasa, "g", "Casa", category.KindExpense, nil, false)

	a.conta(minhaCasa, "cartao-vivo", "Cartão vivo", account.KindCreditCard, false)
	a.conta(minhaCasa, "cartao-arq", "Cartão arquivado", account.KindCreditCard, true)
	a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)
	a.conta(minhaCasa, "dinheiro", "Dinheiro", account.KindCash, false)
	a.conta(minhaCasa, "poupanca", "Poupança", account.KindSavings, true)
	a.conta(minhaCasa, "outra", "Outra", account.KindOther, false)

	a.ledger.rows = []report.CategoryAccountTotal{
		linhaConta(ptr(g.ID), "cartao-vivo", 1_000, 1),
		linhaConta(ptr(g.ID), "cartao-arq", 2_000, 2),
		linhaConta(ptr(g.ID), "corrente", 10_000, 3),
		linhaConta(ptr(g.ID), "dinheiro", 20_000, 4),
		linhaConta(ptr(g.ID), "poupanca", 30_000, 5),
		linhaConta(ptr(g.ID), "outra", 40_000, 6),
	}

	todas := a.relatorio(t, "", "")
	credito := a.relatorio(t, "", report.AccountGroupCredit)
	debito := a.relatorio(t, "", report.AccountGroupDebit)
	conferirParticao(t, todas, credito, debito)

	assert.Equal(t, int64(3_000), credito.TotalCents, "cartão vivo + cartão arquivado")
	assert.Equal(t, int64(3), credito.Count)
	assert.Equal(t, int64(100_000), debito.TotalCents, "corrente + dinheiro + poupança + outra")
	assert.Equal(t, int64(18), debito.Count)
	assert.Equal(t, int64(103_000), todas.TotalCents)
}

// Cenário fixo: cartão arquivado conta em `credit`; aporte lançado NO CARTÃO
// é descartado em `credit` E em todas (ADR-029e); despesa de cartão SEM
// categoria vai para o balde sob `credit`. A partição fecha com tudo isso.
func TestByCategoryRecorteComArquivadoAporteEBalde(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)

	casa := a.categoria(minhaCasa, "g-casa", "Casa", category.KindExpense, nil, false)
	luz := a.categoria(minhaCasa, "f-luz", "Luz", category.KindExpense, ptr(casa.ID), false)
	lazer := a.categoria(minhaCasa, "g-lazer", "Lazer", category.KindExpense, nil, false)
	cdb := a.categoria(minhaCasa, "g-cdb", "CDB", category.KindInvestment, nil, false)

	a.conta(minhaCasa, "cartao", "Cartão", account.KindCreditCard, false)
	a.conta(minhaCasa, "cartao-arq", "Cartão antigo", account.KindCreditCard, true)
	a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)

	a.ledger.rows = []report.CategoryAccountTotal{
		linhaConta(ptr(luz.ID), "cartao", 12_000, 2),
		linhaConta(ptr(luz.ID), "corrente", 8_000, 1),
		linhaConta(ptr(casa.ID), "cartao-arq", 3_000, 1), // direto, cartão arquivado
		linhaConta(ptr(lazer.ID), "corrente", 5_000, 1),
		linhaConta(nil, "cartao", 700, 3),              // balde, sob credit
		linhaConta(nil, "corrente", 300, 1),            // balde, sob debit
		linhaConta(ptr(cdb.ID), "cartao", 200_000, 1),  // aporte no cartão: descartado
		linhaConta(ptr(cdb.ID), "corrente", 50_000, 1), // aporte na corrente: descartado
	}

	todas := a.relatorio(t, "", "")
	credito := a.relatorio(t, "", report.AccountGroupCredit)
	debito := a.relatorio(t, "", report.AccountGroupDebit)
	conferirParticao(t, todas, credito, debito)

	assert.Equal(t, int64(29_000), todas.TotalCents, "sem os dois aportes")
	assert.Equal(t, int64(15_700), credito.TotalCents, "luz 12.000 + casa 3.000 + balde 700; sem o aporte")
	assert.Equal(t, int64(6), credito.Count)
	assert.Equal(t, int64(13_300), debito.TotalCents)

	pc := porCategoriaID(credito)
	_, temAporte := pc[cdb.ID]
	assert.False(t, temAporte, "aporte não aparece em credit")
	_, temAporte = porCategoriaID(todas)[cdb.ID]
	assert.False(t, temAporte, "nem em todas")
	assert.Equal(t, int64(700), pc[""].TotalCents, "a despesa de cartão sem categoria está no balde de credit")
	assert.Equal(t, int64(3), pc[""].Count)
	casaC := pc[casa.ID]
	assert.Equal(t, int64(15_000), casaC.TotalCents)
	assert.Equal(t, int64(3_000), casaC.DirectCents, "o direto do cartão arquivado")
	require.Len(t, casaC.Children, 1)
	assert.Equal(t, int64(12_000), casaC.Children[0].TotalCents)
	assert.NotContains(t, a.logs.String(), `"level":"WARN"`, "nada anômalo neste cenário")
}

// Casa SEM cartão: `credit` responde o mês vazio bem formado, com o recorte
// ecoado — e as categorias nem são carregadas, porque não há o que dobrar.
func TestByCategoryCasaSemCartaoRespondeVazioEmCredit(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	g := a.categoria(minhaCasa, "g", "Casa", category.KindExpense, nil, false)
	a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)
	a.ledger.rows = []report.CategoryAccountTotal{linhaConta(ptr(g.ID), "corrente", 1_000, 1)}

	v := a.relatorio(t, "", report.AccountGroupCredit)
	require.NotNil(t, v.AccountGroup)
	assert.Equal(t, report.AccountGroupCredit, *v.AccountGroup)
	assert.Equal(t, int64(0), v.TotalCents)
	assert.Equal(t, int64(0), v.Count)
	assert.NotNil(t, v.Items)
	assert.Empty(t, v.Items)
	assert.Empty(t, a.cats.chamadas, "partição vazia: não há o que dobrar")

	bruto, err := json.Marshal(v)
	require.NoError(t, err)
	assert.JSONEq(t, `{"month":"2026-09","kind":"expense","accountGroup":"credit","totalCents":0,"count":0,"items":[]}`, string(bruto))

	// E `debit` na mesma casa tem tudo.
	d := a.relatorio(t, "", report.AccountGroupDebit)
	assert.Equal(t, int64(1_000), d.TotalCents)
}

// Mês vazio com recorte: a resposta é o vazio bem formado com o recorte
// ecoado, e nem contas nem categorias são lidas.
func TestByCategoryMesVazioComRecorte(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	v := a.relatorio(t, "income", report.AccountGroupDebit)
	require.NotNil(t, v.AccountGroup)
	assert.Equal(t, report.AccountGroupDebit, *v.AccountGroup)
	assert.Equal(t, "income", v.Kind)
	assert.Empty(t, v.Items)
	assert.Empty(t, a.contas.chamadas)
	assert.Empty(t, a.cats.chamadas)
}

// `income` + `credit` é uma combinação aceita e CALCULADA (os estornos na
// fatura), ainda que a tela não a ofereça.
func TestByCategoryIncomeComCreditCalcula(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	estorno := a.categoria(minhaCasa, "g-estorno", "Estornos", category.KindIncome, nil, false)
	salario := a.categoria(minhaCasa, "g-salario", "Salário", category.KindIncome, nil, false)
	a.conta(minhaCasa, "cartao", "Cartão", account.KindCreditCard, false)
	a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)
	a.ledger.rows = []report.CategoryAccountTotal{
		linhaConta(ptr(estorno.ID), "cartao", 4_500, 2),
		linhaConta(ptr(salario.ID), "corrente", 500_000, 1),
	}

	v := a.relatorio(t, "income", report.AccountGroupCredit)
	assert.Equal(t, "income", v.Kind)
	assert.Equal(t, int64(4_500), v.TotalCents)
	assert.Equal(t, int64(2), v.Count)
	require.Len(t, v.Items, 1)
	assert.Equal(t, estorno.ID, *v.Items[0].CategoryID)
	require.Len(t, a.ledger.chamadas, 1)
	assert.Equal(t, chamadaLedger{minhaCasa, "2026-09", "income"}, a.ledger.chamadas[0])
}

// --- propriedade: partição -------------------------------------------------

// Linhas aleatórias (categorias em dois níveis, contas de todos os tipos,
// arquivadas, balde, aportes, valores zero): para qualquer combinação,
// `credit` + `debit` == todas em todos os níveis — sem nenhum aviso, porque
// toda conta e categoria existem na casa.
func TestByCategoryParticaoPropriedades(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(32, 2026))
	tipos := []string{account.KindCash, account.KindChecking, account.KindSavings, account.KindCreditCard, account.KindOther}
	for iter := 0; iter < 200; iter++ {
		a := novoAmbiente(t)

		nContas := 1 + rng.IntN(8)
		contas := make([]string, 0, nContas)
		for c := 0; c < nContas; c++ {
			id := fmt.Sprintf("conta-%d", c)
			a.conta(minhaCasa, id, fmt.Sprintf("Conta %d", c), tipos[rng.IntN(len(tipos))], rng.IntN(4) == 0)
			contas = append(contas, id)
		}

		var categorias []*string
		categorias = append(categorias, nil) // o balde
		nGrupos := 1 + rng.IntN(6)
		for g := 0; g < nGrupos; g++ {
			gid := fmt.Sprintf("g-%d", g)
			kind := category.KindExpense
			if rng.IntN(6) == 0 {
				kind = category.KindInvestment
			}
			a.categoria(minhaCasa, gid, fmt.Sprintf("Grupo %d", rng.IntN(4)), kind, nil, rng.IntN(5) == 0)
			categorias = append(categorias, ptr(gid))
			for f := 0; f < rng.IntN(4); f++ {
				fid := fmt.Sprintf("f-%d-%d", g, f)
				a.categoria(minhaCasa, fid, fmt.Sprintf("Filha %d", rng.IntN(4)), kind, ptr(gid), rng.IntN(5) == 0)
				categorias = append(categorias, ptr(fid))
			}
		}

		var rows []report.CategoryAccountTotal
		for _, cat := range categorias {
			for _, conta := range contas {
				if rng.IntN(3) == 0 {
					continue
				}
				valor := rng.Int64N(500_000)
				if rng.IntN(10) == 0 {
					valor = 0
				}
				rows = append(rows, linhaConta(cat, conta, valor, 1+rng.Int64N(4)))
			}
		}
		a.ledger.rows = rows

		todas := a.relatorio(t, "", "")
		credito := a.relatorio(t, "", report.AccountGroupCredit)
		debito := a.relatorio(t, "", report.AccountGroupDebit)
		conferirParticao(t, todas, credito, debito)
		if t.Failed() {
			t.Fatalf("iteração %d falhou com %d linhas, %d contas", iter, len(rows), nContas)
		}
		assert.NotContains(t, a.logs.String(), `"level":"WARN"`, "iteração %d", iter)
		// "Todas" nunca carrega contas; cada recorte carrega uma vez.
		assert.Len(t, a.contas.chamadas, 2, "iteração %d", iter)
	}
}

// --- conta órfã e conta alheia --------------------------------------------

// Conta que a linha aponta e a casa NÃO tem: sai de `credit`, entra em
// `debit` (falha ABERTA — o dinheiro aparece), com UM aviso por requisição
// levando `conta_desconhecida` e uma amostra de no máximo 5 ids — sem nome e
// sem centavos, mesmo com 50 linhas anômalas.
func TestByCategoryContaOrfaCaiEmDebitComAvisoAgregado(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	g := a.categoria(minhaCasa, "g", "Casa", category.KindExpense, nil, false)
	a.conta(minhaCasa, "cartao", "Cartão Preferido", account.KindCreditCard, false)
	a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)

	rows := []report.CategoryAccountTotal{
		linhaConta(ptr(g.ID), "cartao", 1_000, 1),
		linhaConta(ptr(g.ID), "corrente", 2_000, 1),
	}
	const orfas = 50
	for i := 0; i < orfas; i++ {
		rows = append(rows, linhaConta(ptr(g.ID), fmt.Sprintf("sumida-%02d", i), 777_000, 1))
	}
	a.ledger.rows = rows

	credito := a.relatorio(t, "", report.AccountGroupCredit)
	assert.Equal(t, int64(1_000), credito.TotalCents, "a conta órfã não é cartão")
	logCredito := a.logs.String()
	a.logs.Reset()

	debito := a.relatorio(t, "", report.AccountGroupDebit)
	assert.Equal(t, int64(2_000+orfas*777_000), debito.TotalCents, "o dinheiro da conta órfã aparece em debit")
	logDebito := a.logs.String()
	a.logs.Reset()

	todas := a.relatorio(t, "", "")
	assert.Equal(t, credito.TotalCents+debito.TotalCents, todas.TotalCents, "a partição continua fechando")
	assert.NotContains(t, a.logs.String(), `"level":"WARN"`, "sem recorte, a conta não é conferida — nada a avisar")

	for nome, log := range map[string]string{"credit": logCredito, "debit": logDebito} {
		assert.Equal(t, 1, strings.Count(log, `"level":"WARN"`), "%s: UM aviso por requisição, não %d", nome, orfas)
		assert.Contains(t, log, fmt.Sprintf(`"conta_desconhecida":%d`, orfas), nome)
		assert.Contains(t, log, fmt.Sprintf(`"anomalias":%d`, orfas), nome)
		var registro struct {
			Amostra []string `json:"amostra_conta_desconhecida"`
		}
		require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(log)), &registro), nome)
		assert.Len(t, registro.Amostra, 5, "%s: amostra de no máximo 5", nome)
		for _, id := range registro.Amostra {
			assert.True(t, strings.HasPrefix(id, "sumida-"), "%s: id %q no balde errado", nome, id)
		}
		assert.NotContains(t, log, "777000", "%s: nunca centavos", nome)
		assert.NotContains(t, log, "Preferido", "%s: nunca nome de conta", nome)
		assert.NotContains(t, log, "Corrente", nome)
		assert.NotContains(t, log, "Casa", "%s: nem nome de categoria", nome)
	}
}

// Cartão de OUTRA casa devolvido por um repositório furado NÃO conta como
// cartão: a reconferência de casa do serviço é a barreira. A linha que o
// aponta é tratada como conta órfã (cai em `debit`, com aviso).
func TestByCategoryCartaoDeOutraCasaNaoEhCartao(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.contas.semFiltroDeCasa = true
	g := a.categoria(minhaCasa, "g", "Casa", category.KindExpense, nil, false)
	a.conta(minhaCasa, "meu-cartao", "Meu cartão", account.KindCreditCard, false)
	alheio := a.conta(outraCasa, "cartao-alheio", "Cartão Da Vizinha", account.KindCreditCard, false)

	a.ledger.rows = []report.CategoryAccountTotal{
		linhaConta(ptr(g.ID), "meu-cartao", 1_000, 1),
		linhaConta(ptr(g.ID), alheio.ID, 999_999, 1), // só um banco adulterado produz isto
	}

	credito := a.relatorio(t, "", report.AccountGroupCredit)
	assert.Equal(t, int64(1_000), credito.TotalCents, "o cartão alheio não vira cartão meu")

	a.logs.Reset()
	debito := a.relatorio(t, "", report.AccountGroupDebit)
	assert.Equal(t, int64(999_999), debito.TotalCents, "falha aberta: o dinheiro do lançamento da casa aparece")
	log := a.logs.String()
	assert.Contains(t, log, `"conta_desconhecida":1`)
	assert.Contains(t, log, alheio.ID, "o aviso leva o id")
	assert.NotContains(t, log, "Vizinha", "nunca o nome")
	assert.NotContains(t, log, "999999", "nunca centavos")
}

// Conta com id VAZIO na lista (banco adulterado) é ignorada na montagem dos
// conjuntos — e uma linha com AccountID vazio é órfã, sem panic.
func TestByCategoryContaComIDVazioNaoEntraNosConjuntos(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.conta(minhaCasa, "", "Fantasma", account.KindCreditCard, false)
	a.ledger.rows = []report.CategoryAccountTotal{linhaConta(nil, "", 5_000, 1)}

	assert.NotPanics(t, func() {
		v := a.relatorio(t, "", report.AccountGroupCredit)
		assert.Equal(t, int64(0), v.TotalCents, "id vazio não é cartão")
		d := a.relatorio(t, "", report.AccountGroupDebit)
		assert.Equal(t, int64(5_000), d.TotalCents)
	})
	assert.Contains(t, a.logs.String(), `"conta_desconhecida":1`)
}

// O recorte NÃO muta a fatia que o Ledger devolveu. O dublê aqui entrega
// SEMPRE o mesmo array (o que um cache no repositório faria), e as três
// leituras seguidas têm de dar os mesmos números — filtrar no lugar embaralharia
// a fatia guardada, e como o filtro depende das contas da CASA, o estrago seria
// entre casas.
func TestByCategoryRecorteNaoMutaAsLinhasDoLedger(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.conta(minhaCasa, "cartao", "Cartão", account.KindCreditCard, false)
	a.conta(minhaCasa, "corrente", "Corrente", account.KindChecking, false)

	compartilhada := []report.CategoryAccountTotal{
		linhaConta(nil, "cartao", 1_000, 1),
		linhaConta(nil, "corrente", 2_000, 1),
		linhaConta(nil, "cartao", 300, 1),
	}
	original := append([]report.CategoryAccountTotal(nil), compartilhada...)
	a.ledger.rows = compartilhada
	a.ledger.semCopia = true

	for range 3 {
		assert.Equal(t, int64(1_300), a.relatorio(t, "", report.AccountGroupCredit).TotalCents)
		assert.Equal(t, int64(2_000), a.relatorio(t, "", report.AccountGroupDebit).TotalCents)
		assert.Equal(t, int64(3_300), a.relatorio(t, "", "").TotalCents)
	}
	assert.Equal(t, original, compartilhada, "a fatia do ledger volta intacta")
}

// --- infraestrutura ----------------------------------------------------------

// Falha ao carregar as contas sobe embrulhada, com contexto, sem virar erro de
// validação — e só acontece quando há recorte.
func TestByCategoryPropagaErroDasContas(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	a.ledger.rows = []report.CategoryAccountTotal{linha(nil, 1, 1)}
	a.contas.err = errors.New("banco fora (senha=abc)")

	_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09", AccountGroup: report.AccountGroupCredit})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "carregando contas da casa")
	assert.False(t, transaction.IsValidationError(err))
	assert.Empty(t, a.cats.chamadas, "a falha nas contas vem antes das categorias")

	// Sem recorte, o mesmo dublê quebrado não é tocado.
	v, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), v.TotalCents)
}

// Acima do teto com recorte: o teto é conferido ANTES das contas, então nem
// elas são lidas.
func TestByCategoryLinhasDemaisComRecorteNaoLeContas(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	rows := make([]report.CategoryAccountTotal, 0, tetoDeLinhas+1)
	for i := 0; i < tetoDeLinhas+1; i++ {
		rows = append(rows, linhaConta(nil, fmt.Sprintf("c-%05d", i), 1, 1))
	}
	a.ledger.rows = rows

	_, err := a.svc.ByCategory(t.Context(), ator(minhaCasa), report.ByCategoryInput{Month: "2026-09", AccountGroup: report.AccountGroupDebit})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "10051 linhas")
	assert.Empty(t, a.contas.chamadas)
	assert.Empty(t, a.cats.chamadas)
}
