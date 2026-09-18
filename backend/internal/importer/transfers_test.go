package importer

import (
	"testing"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/importer/dedup"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tabela do pareamento com a perna já existente (spec 0005 §4.2.1.2 + emenda
// §10.2). Função PURA: nada de banco, só o que o repositório teria carregado.

const (
	contaB = "conta-b"
	contaC = "conta-c"
)

func dia(d int) civil.Date { return civil.MustNew(2026, 9, d) }

func linhaArquivo(seq int, kind string, d, cents int64) ParsedRow {
	return ParsedRow{Seq: seq, Kind: kind, OccurredOn: dia(int(d)), AmountCents: cents}
}

func veredito(seq int, s dedup.Status) dedup.RowResult {
	return dedup.RowResult{Seq: seq, Status: s}
}

func sugerindo(contraparte string) sugestaoDaLinha {
	if contraparte == "" {
		return sugestaoDaLinha{}
	}
	return sugestaoDaLinha{CounterpartID: &contraparte}
}

func perna(id, kind string, d int, cents int64, contraparte string) transaction.TransferLeg {
	return transaction.TransferLeg{
		ID: id, Kind: kind, OccurredOn: dia(d), AmountCents: cents,
		TransferGroupID: "g-" + id, CounterpartAccountID: contraparte,
	}
}

func TestPairExistingLegsTabela(t *testing.T) {
	t.Parallel()

	casos := []struct {
		nome      string
		rows      []ParsedRow
		results   []dedup.RowResult
		sugestoes []sugestaoDaLinha
		pernas    []transaction.TransferLeg
		esperado  map[int]string
	}{
		{
			nome:      "despesa casa com transfer_out da mesma contraparte, valor e dia",
			rows:      []ParsedRow{linhaArquivo(1, transaction.KindExpense, 5, 150000)},
			results:   []dedup.RowResult{veredito(1, dedup.StatusInternalTransfer)},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB)},
			pernas:    []transaction.TransferLeg{perna("p1", transaction.KindTransferOut, 5, 150000, contaB)},
			esperado:  map[int]string{1: "p1"},
		},
		{
			nome:      "receita casa com transfer_in, nunca com transfer_out (direcao)",
			rows:      []ParsedRow{linhaArquivo(1, transaction.KindIncome, 5, 150000)},
			results:   []dedup.RowResult{veredito(1, dedup.StatusInternalTransfer)},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB)},
			pernas: []transaction.TransferLeg{
				perna("saida", transaction.KindTransferOut, 5, 150000, contaB),
				perna("entrada", transaction.KindTransferIn, 5, 150000, contaB),
			},
			esperado: map[int]string{1: "entrada"},
		},
		{
			nome:      "contraparte diferente da sugerida nao casa",
			rows:      []ParsedRow{linhaArquivo(1, transaction.KindExpense, 5, 150000)},
			results:   []dedup.RowResult{veredito(1, dedup.StatusInternalTransfer)},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB)},
			pernas:    []transaction.TransferLeg{perna("p1", transaction.KindTransferOut, 5, 150000, contaC)},
			esperado:  map[int]string{},
		},
		{
			nome:      "valor diferente nao casa",
			rows:      []ParsedRow{linhaArquivo(1, transaction.KindExpense, 5, 150000)},
			results:   []dedup.RowResult{veredito(1, dedup.StatusInternalTransfer)},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB)},
			pernas:    []transaction.TransferLeg{perna("p1", transaction.KindTransferOut, 5, 150001, contaB)},
			esperado:  map[int]string{},
		},
		{
			nome:      "ate DedupWindowDays de distancia casa; um dia a mais nao",
			rows:      []ParsedRow{linhaArquivo(1, transaction.KindExpense, 10, 150000), linhaArquivo(2, transaction.KindExpense, 20, 150000)},
			results:   []dedup.RowResult{veredito(1, dedup.StatusInternalTransfer), veredito(2, dedup.StatusInternalTransfer)},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB), sugerindo(contaB)},
			pernas: []transaction.TransferLeg{
				perna("tres-dias", transaction.KindTransferOut, 10+transaction.DedupWindowDays, 150000, contaB),
				perna("quatro-dias", transaction.KindTransferOut, 20+transaction.DedupWindowDays+1, 150000, contaB),
			},
			esperado: map[int]string{1: "tres-dias"},
		},
		{
			nome:      "escolhe a perna de menor distancia em dias",
			rows:      []ParsedRow{linhaArquivo(1, transaction.KindExpense, 10, 150000)},
			results:   []dedup.RowResult{veredito(1, dedup.StatusInternalTransfer)},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB)},
			pernas: []transaction.TransferLeg{
				perna("longe", transaction.KindTransferOut, 12, 150000, contaB),
				perna("perto", transaction.KindTransferOut, 11, 150000, contaB),
			},
			esperado: map[int]string{1: "perto"},
		},
		{
			nome:      "empate na distancia: menor OccurredOn, depois menor ID",
			rows:      []ParsedRow{linhaArquivo(1, transaction.KindExpense, 10, 150000)},
			results:   []dedup.RowResult{veredito(1, dedup.StatusInternalTransfer)},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB)},
			pernas: []transaction.TransferLeg{
				perna("z-depois", transaction.KindTransferOut, 11, 150000, contaB),
				perna("b-antes", transaction.KindTransferOut, 9, 150000, contaB),
				perna("a-antes", transaction.KindTransferOut, 9, 150000, contaB),
			},
			esperado: map[int]string{1: "a-antes"},
		},
		{
			// Critério 6 da spec: duas transferências iguais no mesmo dia casam
			// com pernas DISTINTAS; a terceira, sem perna sobrando, não casa.
			nome: "duas iguais no mesmo dia reivindicam pernas distintas; a terceira fica",
			rows: []ParsedRow{
				linhaArquivo(1, transaction.KindExpense, 5, 150000),
				linhaArquivo(2, transaction.KindExpense, 5, 150000),
				linhaArquivo(3, transaction.KindExpense, 5, 150000),
			},
			results: []dedup.RowResult{
				veredito(1, dedup.StatusInternalTransfer),
				veredito(2, dedup.StatusInternalTransfer),
				veredito(3, dedup.StatusInternalTransfer),
			},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB), sugerindo(contaB), sugerindo(contaB)},
			pernas: []transaction.TransferLeg{
				perna("p1", transaction.KindTransferOut, 5, 150000, contaB),
				perna("p2", transaction.KindTransferOut, 5, 150000, contaB),
			},
			esperado: map[int]string{1: "p1", 2: "p2"},
		},
		{
			// Emenda §10.2: pagamento de fatura com contraparte sugerida
			// também procura a perna.
			nome:      "pagamento de fatura com contraparte sugerida participa",
			rows:      []ParsedRow{linhaArquivo(1, transaction.KindExpense, 7, 285982)},
			results:   []dedup.RowResult{veredito(1, dedup.StatusCardPayment)},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB)},
			pernas:    []transaction.TransferLeg{perna("fatura", transaction.KindTransferOut, 7, 285982, contaB)},
			esperado:  map[int]string{1: "fatura"},
		},
		{
			nome:      "pagamento de fatura SEM contraparte sugerida nao participa",
			rows:      []ParsedRow{linhaArquivo(1, transaction.KindExpense, 7, 285982)},
			results:   []dedup.RowResult{veredito(1, dedup.StatusCardPayment)},
			sugestoes: []sugestaoDaLinha{sugerindo("")},
			pernas:    []transaction.TransferLeg{perna("fatura", transaction.KindTransferOut, 7, 285982, contaB)},
			esperado:  map[int]string{},
		},
		{
			nome:      "linha nova, duplicada ou possivel duplicata nunca participa",
			rows:      []ParsedRow{linhaArquivo(1, transaction.KindExpense, 5, 150000), linhaArquivo(2, transaction.KindExpense, 5, 150000), linhaArquivo(3, transaction.KindExpense, 5, 150000)},
			results:   []dedup.RowResult{veredito(1, dedup.StatusNew), veredito(2, dedup.StatusDuplicateExact), veredito(3, dedup.StatusPossibleDuplicate)},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB), sugerindo(contaB), sugerindo(contaB)},
			pernas:    []transaction.TransferLeg{perna("p1", transaction.KindTransferOut, 5, 150000, contaB)},
			esperado:  map[int]string{},
		},
		{
			// A ordem de reivindicação é a do ARQUIVO (Seq), não a do slice.
			nome: "reivindica em ordem de Seq mesmo com o slice fora de ordem",
			rows: []ParsedRow{
				linhaArquivo(2, transaction.KindExpense, 6, 150000),
				linhaArquivo(1, transaction.KindExpense, 5, 150000),
			},
			results:   []dedup.RowResult{veredito(2, dedup.StatusInternalTransfer), veredito(1, dedup.StatusInternalTransfer)},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB), sugerindo(contaB)},
			pernas:    []transaction.TransferLeg{perna("unica", transaction.KindTransferOut, 5, 150000, contaB)},
			esperado:  map[int]string{1: "unica"},
		},
		{
			nome:      "sem pernas: nada casa e nada explode",
			rows:      []ParsedRow{linhaArquivo(1, transaction.KindExpense, 5, 150000)},
			results:   []dedup.RowResult{veredito(1, dedup.StatusInternalTransfer)},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB)},
			pernas:    nil,
			esperado:  map[int]string{},
		},
		{
			nome:      "comprimentos desalinhados: defesa devolve vazio",
			rows:      []ParsedRow{linhaArquivo(1, transaction.KindExpense, 5, 150000)},
			results:   []dedup.RowResult{},
			sugestoes: []sugestaoDaLinha{sugerindo(contaB)},
			pernas:    []transaction.TransferLeg{perna("p1", transaction.KindTransferOut, 5, 150000, contaB)},
			esperado:  map[int]string{},
		},
	}

	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			got := pairExistingLegs(c.rows, c.results, c.sugestoes, c.pernas)
			assert.Equal(t, c.esperado, got)
		})
	}
}

func TestPairExistingLegsNaoAlteraAsEntradas(t *testing.T) {
	t.Parallel()
	rows := []ParsedRow{linhaArquivo(1, transaction.KindExpense, 5, 150000)}
	results := []dedup.RowResult{veredito(1, dedup.StatusInternalTransfer)}
	sugestoes := []sugestaoDaLinha{sugerindo(contaB)}
	pernas := []transaction.TransferLeg{perna("p1", transaction.KindTransferOut, 5, 150000, contaB)}

	got := pairExistingLegs(rows, results, sugestoes, pernas)
	require.Equal(t, map[int]string{1: "p1"}, got)

	// Pura: chamar de novo com as mesmas entradas dá o mesmo resultado — a
	// reivindicação vive dentro da chamada, não nas pernas.
	assert.Equal(t, got, pairExistingLegs(rows, results, sugestoes, pernas))
	assert.Equal(t, dedup.StatusInternalTransfer, results[0].Status)
}

func TestCategoriaDaLinhaSegueAPrecedenciaDaSpec(t *testing.T) {
	t.Parallel()
	valor, sugerida, padrao := "cat-decisao", "cat-sugerida", "cat-padrao"

	// recusaTudo é o conferente da spec 0005 §13 respondendo "esta sugestão
	// não vale mais" — arquivada, excluída, virada grupo ou de outra casa dão
	// todas nele.
	recusaTudo := func(string, string) bool { return false }
	soASugerida := func(id, _ string) bool { return id == sugerida }
	// aceitaTudo é o conferente carregado e satisfeito. Ele é o que o confirm
	// tem sempre que alguma linha traz sugestão; `valida == nil` é a AUSÊNCIA
	// de conferente, e passou a descartar a sugestão (achado B4).
	aceitaTudo := func(string, string) bool { return true }

	casos := []struct {
		nome     string
		decisao  Decision
		citada   bool
		sugerida *string
		padrao   *string
		valida   categoriaAtribuivel
		esperado *string
	}{
		{"decisao com valor vence tudo", Decision{CategoryID: OptionalCategory{Set: true, ID: &valor}}, true, &sugerida, &padrao, aceitaTudo, &valor},
		{"decisao nula vence sugestao e padrao", Decision{CategoryID: OptionalCategory{Set: true}}, true, &sugerida, &padrao, aceitaTudo, nil},
		{"decisao ausente usa a sugestao", Decision{Action: ActionImport}, true, &sugerida, &padrao, aceitaTudo, &sugerida},
		{"linha nao citada usa a sugestao", Decision{}, false, &sugerida, &padrao, aceitaTudo, &sugerida},
		{"sem sugestao usa o padrao", Decision{}, false, nil, &padrao, nil, &padrao},
		{"sem nada fica sem categoria", Decision{}, false, nil, nil, nil, nil},
		{"decisao nula em linha nao citada nao conta", Decision{CategoryID: OptionalCategory{Set: true}}, false, &sugerida, nil, aceitaTudo, &sugerida},

		// Achado B4: SEM conferente não há como afirmar que a sugestão ainda
		// vale, e o lado seguro é "sem categoria" — nunca aceitá-la de olhos
		// fechados, nem cair no padrão do cliente.
		{"sem conferente descarta a sugestao", Decision{}, false, &sugerida, nil, nil, nil},
		{"sem conferente NAO cai no padrao", Decision{}, false, &sugerida, &padrao, nil, nil},
		{"sem conferente nao mexe na decisao", Decision{CategoryID: OptionalCategory{Set: true, ID: &valor}}, true, &sugerida, &padrao, nil, &valor},

		// §13 "Correção de robustez": a sugestão obsoleta some da linha.
		{"sugestao obsoleta vira sem categoria", Decision{}, false, &sugerida, nil, recusaTudo, nil},
		{"sugestao obsoleta NAO cai no padrao", Decision{}, false, &sugerida, &padrao, recusaTudo, nil},
		{"sugestao valida continua valendo", Decision{}, false, &sugerida, &padrao, soASugerida, &sugerida},
		{"categoria da DECISAO nao e degradada", Decision{CategoryID: OptionalCategory{Set: true, ID: &valor}}, true, &sugerida, &padrao, recusaTudo, &valor},
		{"padrao do CLIENTE nao e degradado", Decision{}, false, nil, &padrao, recusaTudo, &padrao},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			t.Parallel()
			got := categoriaDaLinha(c.decisao, c.citada, c.sugerida, c.padrao,
				transaction.KindExpense, c.valida)
			if c.esperado == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *c.esperado, *got)
		})
	}
}

// A natureza da sugestão é conferida junto: uma categoria de despesa numa
// linha de receita é degradada, e não mandada para o 422 do serviço de
// lançamentos. Categoria de transferência não existe — perna nunca chega aqui.
func TestCategoriaCombinaComALinha(t *testing.T) {
	t.Parallel()

	assert.True(t, categoriaCombinaComALinha(transaction.KindIncome, category.KindIncome))
	assert.True(t, categoriaCombinaComALinha(transaction.KindExpense, category.KindExpense))
	assert.False(t, categoriaCombinaComALinha(transaction.KindIncome, category.KindExpense))
	assert.False(t, categoriaCombinaComALinha(transaction.KindExpense, category.KindIncome))
	assert.False(t, categoriaCombinaComALinha(transaction.KindTransferIn, category.KindIncome))
	assert.False(t, categoriaCombinaComALinha(transaction.KindTransferOut, category.KindExpense))
	assert.False(t, categoriaCombinaComALinha("", ""))
}
