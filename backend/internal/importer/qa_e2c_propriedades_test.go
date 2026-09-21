package importer_test

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/importer"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// QA da E2c (spec 0005, critério 5 e §9 do plano) — PROPRIEDADE, e não
// exemplo: "a soma dos saldos de todas as contas da casa não muda com `link`",
// sustentada em sequências ALEATÓRIAS de importar A / importar B / reimportar,
// nas duas ordens (A primeiro ou B primeiro), com transferências iguais no
// mesmo dia e uma com a data deslocada dentro da janela de ±3 dias.
//
// O invariante é independente da implementação: transferência interna é um
// par de pernas de mesmo valor, então a soma dos saldos da casa é SEMPRE
// (receitas comuns − despesas comuns). Se um `link` criasse movimento, ou um
// `transfer` criasse uma segunda perna num dos lados, a soma sairia do lugar.
// E o segundo invariante — cada transferência real vira UM par, nunca dois —
// é o que a ação `link` existe para garantir.

// transferenciaDoCenario é uma transferência real de A para B: dia em A, dia
// em que aparece no extrato de B (pode diferir dentro da janela), valor.
type transferenciaDoCenario struct {
	diaA, diaB int
	valor      string
	centavos   int64
}

// cenarioAB são os dois extratos: as transferências espelhadas e as despesas
// comuns de cada conta.
type cenarioAB struct {
	transferencias []transferenciaDoCenario
	comunsA        []linhaExtrato // despesas comuns de A (negativas)
	comunsB        []linhaExtrato // despesas comuns de B (negativas)
}

func cenarioPadraoAB() cenarioAB {
	return cenarioAB{
		transferencias: []transferenciaDoCenario{
			{5, 5, "100.00", 100_00},   // duas iguais no mesmo dia (critério 6)
			{5, 5, "100.00", 100_00},   //
			{7, 7, "100.00", 100_00},   // mesmo valor, dois dias depois: dentro da janela
			{12, 13, "250.50", 250_50}, // deslocada um dia no extrato de B
			{20, 20, "999.99", 999_99},
		},
		comunsA: []linhaExtrato{
			{3, "-11.00", "a1", "Transferência enviada pelo Pix - Padaria Exemplo"},
			{18, "-42.90", "a2", "Transferência enviada pelo Pix - Farmacia Exemplo"},
		},
		comunsB: []linhaExtrato{
			{9, "-20.00", "b1", "Transferência enviada pelo Pix - Livraria Exemplo"},
		},
	}
}

// extratoDeA monta o CSV de A: cada transferência é uma despesa que cita a
// palavra-chave de B ("itau").
func (c cenarioAB) extratoDeA() []byte {
	linhas := make([]linhaExtrato, 0, len(c.transferencias)+len(c.comunsA))
	for i, tr := range c.transferencias {
		linhas = append(linhas, linhaExtrato{tr.diaA, "-" + tr.valor, fmt.Sprintf("%02d", 10+i), "Transferência enviada pelo Pix - Itau Corrente"})
	}
	linhas = append(linhas, c.comunsA...)
	return csvExtratoAPI(linhas...)
}

// extratoDeB é o espelho: cada transferência é uma receita que cita a
// palavra-chave de A ("nubank"), na data em que B a registrou.
func (c cenarioAB) extratoDeB() []byte {
	linhas := make([]linhaExtrato, 0, len(c.transferencias)+len(c.comunsB))
	for i, tr := range c.transferencias {
		linhas = append(linhas, linhaExtrato{tr.diaB, tr.valor, fmt.Sprintf("%02d", 50+i), "Transferência recebida pelo Pix - Nubank Conta"})
	}
	linhas = append(linhas, c.comunsB...)
	return csvExtratoAPI(linhas...)
}

func (c cenarioAB) somaTransferencias() int64 {
	var s int64
	for _, tr := range c.transferencias {
		s += tr.centavos
	}
	return s
}

// centavosDe converte "-42.90" em -4290 sem float — dinheiro é int64.
func centavosDe(t *testing.T, valor string) int64 {
	t.Helper()
	negativo := strings.HasPrefix(valor, "-")
	valor = strings.TrimPrefix(valor, "-")
	partes := strings.SplitN(valor, ".", 2)
	require.Len(t, partes, 2, "valor %q precisa ter centavos", valor)
	require.Len(t, partes[1], 2)
	var reais, cents int64
	_, err := fmt.Sscanf(partes[0], "%d", &reais)
	require.NoError(t, err)
	_, err = fmt.Sscanf(partes[1], "%d", &cents)
	require.NoError(t, err)
	total := reais*100 + cents
	if negativo {
		return -total
	}
	return total
}

func (c cenarioAB) somaComuns(t *testing.T, linhas []linhaExtrato) int64 {
	t.Helper()
	var s int64
	for _, l := range linhas {
		s += centavosDe(t, l.valor)
	}
	return s
}

// confirmarAceitandoTransferencias confirma o lote com a política da tela
// "aceitar todas as transferências sugeridas": `transfer` em toda
// `transferencia_interna` (a contraparte é a sugerida) e o default nas demais
// — `link` em `transferencia_ja_registrada`, `import` em `novo`, e nada nas
// duplicatas.
func confirmarAceitandoTransferencias(t *testing.T, a *ambiente, loteID string) (importer.ResultView, int) {
	t.Helper()
	var decisoes []string
	for _, l := range revisarPelaAPI(t, a, loteID).Items {
		if l.Status == string(dedup.StatusInternalTransfer) {
			decisoes = append(decisoes, fmt.Sprintf(`{"rowId":%q,"action":"transfer"}`, l.ID))
		}
	}
	corpo := `{"decisions":[` + strings.Join(decisoes, ",") + `]}`
	return resultadoDaResposta(t, confirmarPelaAPI(t, a, loteID, corpo)), len(decisoes)
}

// fotoDaCasa é o que os invariantes olham: saldos por conta, soma das
// receitas e despesas COMUNS vivas, pares de transferência vivos.
type fotoDaCasa struct {
	saldos        map[string]int64
	comunsLiquido int64 // Σ income − Σ expense das linhas que NÃO são transferência
	comuns        int
	pernas        int
	pares         map[string]int // transfer_group_id -> pernas vivas
}

func (a *ambiente) foto(t *testing.T) fotoDaCasa {
	t.Helper()
	f := fotoDaCasa{saldos: a.saldos(t), pares: map[string]int{}}
	for _, l := range a.lancamentosDa(t, a.casa.ID, "2026-08") {
		switch l.Kind {
		case transaction.KindIncome:
			f.comunsLiquido += l.AmountCents
			f.comuns++
		case transaction.KindExpense:
			f.comunsLiquido -= l.AmountCents
			f.comuns++
		case transaction.KindTransferIn, transaction.KindTransferOut:
			f.pernas++
			require.NotNil(t, l.TransferGroupID, "perna sem grupo")
			f.pares[*l.TransferGroupID]++
		}
	}
	return f
}

// exigirInvariantes confere o que precisa valer DEPOIS DE QUALQUER passo.
func exigirInvariantes(t *testing.T, passo string, c cenarioAB, f fotoDaCasa) {
	t.Helper()
	assert.Equal(t, f.comunsLiquido, somaDeSaldos(f.saldos),
		"%s: a soma dos saldos da casa tem de ser exatamente receitas − despesas comuns (transferência não move dinheiro da casa)", passo)
	assert.Equal(t, 0, f.pernas%2, "%s: pernas de transferência vêm aos pares", passo)
	for grupo, n := range f.pares {
		assert.Equal(t, 2, n, "%s: grupo %s com %d pernas", passo, grupo, n)
	}
	assert.LessOrEqual(t, len(f.pares), len(c.transferencias),
		"%s: mais pares do que transferências reais — o link deixou uma perna virar par de novo", passo)
	assert.LessOrEqual(t, f.comuns, len(c.comunsA)+len(c.comunsB), "%s: linha comum importada duas vezes", passo)
}

// exigirEstadoFinal é o que vale quando A e B já entraram ao menos uma vez.
func exigirEstadoFinal(t *testing.T, c cenarioAB, contaA, contaB string, f fotoDaCasa) {
	t.Helper()
	assert.Len(t, f.pares, len(c.transferencias), "cada transferência real é UM par")
	assert.Equal(t, len(c.comunsA)+len(c.comunsB), f.comuns)
	assert.Equal(t, -c.somaTransferencias()+c.somaComuns(t, c.comunsA), f.saldos[contaA], "saldo de A")
	assert.Equal(t, c.somaTransferencias()+c.somaComuns(t, c.comunsB), f.saldos[contaB], "saldo de B")
}

func TestPropriedadeSomaDosSaldosNaoMudaComLinkEmSequenciasAleatorias(t *testing.T) {
	t.Parallel()

	const passos = 6
	for _, semente := range []uint64{1, 2, 3, 7, 2026} {
		t.Run(fmt.Sprintf("semente-%d", semente), func(t *testing.T) {
			t.Parallel()
			rng := rand.New(rand.NewPCG(semente, 42))

			// Sequência aleatória de {A, B} com os dois presentes: sem B não há
			// link nenhum e o teste não provaria nada.
			var seq []string
			for {
				seq = seq[:0]
				for range passos {
					if rng.IntN(2) == 0 {
						seq = append(seq, "A")
					} else {
						seq = append(seq, "B")
					}
				}
				if strings.Contains(strings.Join(seq, ""), "A") && strings.Contains(strings.Join(seq, ""), "B") {
					break
				}
			}
			t.Logf("sequência: %s", strings.Join(seq, " → "))

			a := novoAmbiente(t)
			c := cenarioPadraoAB()
			contaA := a.contaNubankComPalavra(t)
			contaB := a.contaItau(t)

			jaA, jaB := false, false
			var totalPares, totalVinculos int
			for i, op := range seq {
				passo := fmt.Sprintf("passo %d (%s)", i+1, op)
				antes := a.foto(t)

				var lote importer.BatchView
				if op == "A" {
					lote = loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, fmt.Sprintf("a-%d.csv", i), c.extratoDeA()))
				} else {
					lote = loteDaResposta(t, enviarPelaAPI(t, a, contaB.ID, fmt.Sprintf("b-%d.csv", i), c.extratoDeB()))
				}
				res, transferencias := confirmarAceitandoTransferencias(t, a, lote.ID)
				t.Logf("%s: imported=%d transfersCreated=%d linked=%d skipped=%d blocked=%d",
					passo, res.Imported, res.TransfersCreated, res.Linked, res.Skipped, res.Blocked)
				assert.Equal(t, importer.BatchStatusCommitted, res.Status, passo)
				assert.Zero(t, res.Blocked, "%s: nada bloqueado — o cenário é limpo", passo)
				assert.Equal(t, transferencias, res.TransfersCreated, "%s: toda transferencia_interna com transfer virou UM par", passo)
				totalPares += res.TransfersCreated
				totalVinculos += res.Linked

				depois := a.foto(t)
				exigirInvariantes(t, passo, c, depois)

				// O que o passo pode ter mudado: só linhas comuns novas. O
				// delta da soma dos saldos é exatamente o delta das comuns —
				// e é ZERO quando o passo só vinculou (link) ou só achou
				// duplicatas.
				assert.Equal(t, depois.comunsLiquido-antes.comunsLiquido, somaDeSaldos(depois.saldos)-somaDeSaldos(antes.saldos), passo)
				if res.Linked > 0 {
					assert.Equal(t, len(antes.pares), len(depois.pares), "%s: link não cria par", passo)
				}
				if res.Imported == 0 && res.TransfersCreated == 0 {
					assert.Equal(t, somaDeSaldos(antes.saldos), somaDeSaldos(depois.saldos), "%s: passo sem importação não move dinheiro", passo)
					assert.Equal(t, antes.saldos, depois.saldos, "%s: nem em conta nenhuma", passo)
				}

				// Reimportar o mesmo lado: tudo duplicado_exato, nada muda.
				if (op == "A" && jaA) || (op == "B" && jaB) {
					assert.Zero(t, res.Imported, "%s: reimportação não grava linha comum", passo)
					assert.Zero(t, res.TransfersCreated, "%s: reimportação não cria par", passo)
					assert.Zero(t, res.Linked, "%s: reimportação não vincula de novo — a perna já tem a chave", passo)
					assert.Equal(t, antes.saldos, depois.saldos, passo)
				}
				if op == "A" {
					jaA = true
				} else {
					jaB = true
				}
				if jaA && jaB {
					exigirEstadoFinal(t, c, contaA.ID, contaB.ID, depois)
				}
			}

			// Ao longo da sequência inteira, cada transferência real virou par
			// UMA vez (no primeiro lado a entrar) e foi vinculada UMA vez (no
			// segundo) — nunca duas, nunca zero.
			assert.Equal(t, len(c.transferencias), totalPares, "Σ transfersCreated na sequência")
			assert.Equal(t, len(c.transferencias), totalVinculos, "Σ linked na sequência")

			// Ao final, reimportar os DOIS lados de novo é 100% duplicado_exato
			// em cada um — inclusive as pernas que entraram por `link`.
			for _, lado := range []struct {
				conta   string
				extrato []byte
				linhas  int
			}{
				{contaA.ID, c.extratoDeA(), len(c.transferencias) + len(c.comunsA)},
				{contaB.ID, c.extratoDeB(), len(c.transferencias) + len(c.comunsB)},
			} {
				lote := loteDaResposta(t, enviarPelaAPI(t, a, lado.conta, "final.csv", lado.extrato))
				require.NotNil(t, lote.Counts)
				assert.Equal(t, lado.linhas, lote.Counts.DuplicateExact, "reimportação final: tudo duplicado_exato")
				assert.Zero(t, lote.Counts.InternalTransfer)
				assert.Zero(t, lote.Counts.TransferAlreadyRegistered)
			}
			exigirEstadoFinal(t, c, contaA.ID, contaB.ID, a.foto(t))
		})
	}
}

// B PRIMEIRO, explicitamente: a receita em B vira `transferencia_interna` com
// contraparte A e o `transfer` cria o par com a perna de SAÍDA em A; o extrato
// de A, importado depois, acha essa perna e vincula. É o sentido que os
// testes de exemplo dos devs não percorrem.
func TestFluxoBPrimeiroDepoisAVinculaAPernaDeSaida(t *testing.T) {
	t.Parallel()
	a := novoAmbiente(t)
	contaA := a.contaNubankComPalavra(t)
	contaB := a.contaItau(t)

	extratoB := csvExtratoAPI(linhaExtrato{13, "250.50", "51", "Transferência recebida pelo Pix - Nubank Conta"})
	loteB := loteDaResposta(t, enviarPelaAPI(t, a, contaB.ID, "b.csv", extratoB))
	require.Equal(t, 1, loteB.Counts.InternalTransfer)
	linhaB := revisarPelaAPI(t, a, loteB.ID).Items[0]
	assert.Equal(t, transaction.KindIncome, texto(linhaB.Kind))
	assert.Equal(t, contaA.ID, texto(linhaB.SuggestedCounterpartAccountID))

	resB := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteB.ID, fmt.Sprintf(`{"decisions":[{"rowId":%q,"action":"transfer"}]}`, linhaB.ID)))
	assert.Equal(t, 1, resB.TransfersCreated)
	pernasA := a.pernasDe(t, contaA.ID, "2026-08")
	require.Len(t, pernasA, 1)
	assert.Equal(t, transaction.KindTransferOut, pernasA[0].Kind, "a receita em B é uma SAÍDA em A")
	require.Len(t, a.pernasDe(t, contaB.ID, "2026-08"), 1)
	saldos := a.saldos(t)
	assert.Equal(t, int64(-250_50), saldos[contaA.ID])
	assert.Equal(t, int64(250_50), saldos[contaB.ID])

	// A, um dia antes (12/08): dentro da janela, a despesa acha a perna de
	// saída já registrada.
	extratoA := csvExtratoAPI(linhaExtrato{12, "-250.50", "01", "Transferência enviada pelo Pix - Itau Corrente"})
	loteA := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a.csv", extratoA))
	require.Equal(t, 1, loteA.Counts.TransferAlreadyRegistered)
	linhaA := revisarPelaAPI(t, a, loteA.ID).Items[0]
	assert.Equal(t, pernasA[0].ID, texto(linhaA.MatchTransactionID))
	assert.Equal(t, importer.ActionLink, linhaA.DefaultAction)

	resA := resultadoDaResposta(t, confirmarPelaAPI(t, a, loteA.ID, `{"decisions":[]}`))
	assert.Equal(t, 1, resA.Linked)
	assert.Zero(t, resA.TransfersCreated)
	assert.Equal(t, saldos, a.saldos(t), "o link não moveu um centavo em conta nenhuma")

	// A reimportação de A cai em duplicado_exato — a perna de saída ganhou a
	// chave da linha de A.
	reA := loteDaResposta(t, enviarPelaAPI(t, a, contaA.ID, "a2.csv", extratoA))
	assert.Equal(t, 1, reA.Counts.DuplicateExact)
	assert.Zero(t, reA.Counts.TransferAlreadyRegistered)
}
