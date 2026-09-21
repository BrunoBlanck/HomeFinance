package category_test

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// DestinoAindaQualifica: os QUATRO eixos, um a um
// ---------------------------------------------------------------------------
//
// A regra mora aqui porque JÁ ESTEVE em duas cópias — uma em
// POST /transactions/auto-categorize, outra em POST /investments/detect — e as
// duas divergiram: o eixo do ARQUIVAMENTO entrou numa rodada de revisão em uma
// rota e só na rodada seguinte na outra. Enquanto isso durou, uma das duas
// atribuía categoria arquivada e a outra recusava, pela mesma corrida.
//
// Os testes vêm junto com a regra, e não ficam nos chamadores, pelo mesmo
// motivo: um teste por rota é um teste que a próxima rota não herda.
//
// A TABELA tem um formato deliberado: cada caso reprova por UM eixo só, e os
// outros três aprovam. É o que faz a verificação por mutação funcionar —
// removida qualquer uma das quatro checagens, existe um caso que falha por ela
// e por mais nenhuma. A primeira versão deste predicado não tinha essa
// propriedade: apagar o eixo da EXCLUSÃO não quebrava nada, porque a categoria
// excluída volta como zero value, de `Kind` vazio, e o eixo da natureza já
// reprovava o vazio. Um eixo sustentado por acidente de outro é um eixo que a
// próxima refatoração apaga sem derrubar o verde.

// Os `kind` de LANÇAMENTO aparecem como literais porque são do domínio de
// lançamentos, que este pacote não importa (é `transaction` quem importa
// `category`). São os mesmos valores de transaction.Kind*.
const (
	lancDespesa       = "expense"
	lancReceita       = "income"
	lancTransferencia = "transfer_out"
)

// destinoBom é a categoria relida que passa nos quatro eixos: encontrada, não
// arquivada, sem subcategoria ativa e de natureza que aceita DESPESA. Cada caso
// estraga UM campo a partir daqui.
func destinoBom() category.LiveState {
	return category.LiveState{ID: "cat-1", Kind: category.KindExpense}
}

func TestDestinoAindaQualificaCobreOsQuatroEixos(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome       string
		st         category.LiveState
		encontrada bool
		lado       string
		qualifica  bool
		porque     string
	}{
		{
			nome:       "tudo em ordem",
			st:         destinoBom(),
			encontrada: true,
			lado:       lancDespesa,
			qualifica:  true,
			porque:     "sem interferência, a reconferência não pode recusar nada",
		},
		{
			nome:       "EIXO 1 — EXCLUÍDA: ausente do mapa, ainda que o resto estivesse bom",
			st:         destinoBom(), // populado DE PROPÓSITO: é o que separa
			encontrada: false,        // este eixo do da natureza (zero value).
			lado:       lancDespesa,
			qualifica:  false,
			porque:     "id que não voltou de LiveStates foi excluído (ou é de outra casa)",
		},
		{
			nome:       "EIXO 1 — EXCLUÍDA: ausente e zero value (o caso real do mapa)",
			st:         category.LiveState{},
			encontrada: false,
			lado:       lancDespesa,
			qualifica:  false,
			porque:     "o mapa devolve o zero value junto com ok=false",
		},
		{
			nome: "EIXO 2 — ARQUIVADA",
			st: func() category.LiveState {
				st := destinoBom()
				st.Archived = true
				return st
			}(),
			encontrada: true,
			lado:       lancDespesa,
			qualifica:  false,
			porque:     "atribuição NOVA a categoria arquivada é recusada nas outras portas do produto",
		},
		{
			nome: "EIXO 3 — GRUPO com subcategoria ativa",
			st: func() category.LiveState {
				st := destinoBom()
				st.HasActiveChild = true
				return st
			}(),
			encontrada: true,
			lado:       lancDespesa,
			qualifica:  false,
			porque:     "grupo com filha ativa não recebe lançamento (spec 0005 §12/§13)",
		},
		{
			nome: "EIXO 4 — NATUREZA cruzou o lado do dinheiro",
			st: func() category.LiveState {
				st := destinoBom()
				st.Kind = category.KindIncome
				return st
			}(),
			encontrada: true,
			lado:       lancDespesa,
			qualifica:  false,
			porque:     "despesa em categoria de receita é o achado A9",
		},
		{
			nome: "NATUREZA mudou DENTRO do mesmo lado: continua qualificando",
			st: func() category.LiveState {
				st := destinoBom()
				st.Kind = category.KindInvestment
				return st
			}(),
			encontrada: true,
			lado:       lancDespesa,
			qualifica:  true,
			porque:     "expense → investment é permitido mesmo em uso (ADR-029c); apertar aqui inventaria regra",
		},
		{
			nome: "receita na categoria de receita",
			st: func() category.LiveState {
				st := destinoBom()
				st.Kind = category.KindIncome
				return st
			}(),
			encontrada: true,
			lado:       lancReceita,
			qualifica:  true,
			porque:     "o lado da entrada também tem de FUNCIONAR, e não só recusar",
		},
		{
			nome: "receita na categoria de resgate",
			st: func() category.LiveState {
				st := destinoBom()
				st.Kind = category.KindRedemption
				return st
			}(),
			encontrada: true,
			lado:       lancReceita,
			qualifica:  true,
			porque:     "receita aceita income E redemption (ADR-029b)",
		},
		{
			nome:       "PERNA DE TRANSFERÊNCIA não casa com natureza nenhuma",
			st:         destinoBom(),
			encontrada: true,
			lado:       lancTransferencia,
			qualifica:  false,
			porque:     "perna de transferência não tem categoria (ADR-016)",
		},
		{
			nome:       "LADO ESPERADO VAZIO reprova",
			st:         destinoBom(),
			encontrada: true,
			lado:       "",
			qualifica:  false,
			porque:     "sem saber o que se está gravando, o desfecho seguro é não gravar",
		},
		{
			nome: "NATUREZA vazia reprova, mesmo com a categoria encontrada e sadia",
			st: func() category.LiveState {
				st := destinoBom()
				st.Kind = ""
				return st
			}(),
			encontrada: true,
			lado:       lancDespesa,
			qualifica:  false,
			porque:     "natureza fora da allowlist não aceita lançamento nenhum",
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, c.qualifica,
				category.DestinoAindaQualifica(c.st, c.encontrada, c.lado), c.porque)
		})
	}
}

// A função responde a MESMA coisa que AceitaLancamento quando os três primeiros
// eixos estão em ordem — ela ACRESCENTA eixos, nunca reinterpreta o pareamento.
//
// Este teste existe para que ninguém "simplifique" o último `return` num switch
// próprio: seria a terceira cópia da regra que o ADR-029b centralizou, e a
// terceira cópia diverge como as duas primeiras.
func TestDestinoAindaQualificaDelegaOPareamentoAAceitaLancamento(t *testing.T) {
	t.Parallel()

	naturezas := []string{
		category.KindIncome, category.KindExpense,
		category.KindInvestment, category.KindRedemption,
		"", "banana",
	}
	lados := []string{lancReceita, lancDespesa, lancTransferencia, ""}

	for _, natureza := range naturezas {
		for _, lado := range lados {
			st := category.LiveState{ID: "cat-1", Kind: natureza}
			assert.Equal(t,
				category.AceitaLancamento(lado, natureza),
				category.DestinoAindaQualifica(st, true, lado),
				"lado=%q natureza=%q: o pareamento tem de vir de AceitaLancamento", lado, natureza)
		}
	}
}
