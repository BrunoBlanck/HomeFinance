package gormstore_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/brunorblanck/homefinance/backend/internal/category"
	"github.com/brunorblanck/homefinance/backend/internal/civil"
	"github.com/brunorblanck/homefinance/backend/internal/textnorm"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Este arquivo cobre a agregação por descrição do menu IA (spec 0010 §3.1
// item 8, E9a) contra banco real. O que importa provar é o WHERE — casa,
// janela de competência e deleted_at IS NULL — e o rail de tudo-ou-nada.

// janela3 é a janela máxima da spec 0010: 3 meses de competência.
var janela3 = []string{"2026-07", "2026-08", "2026-09"}

// porNorma indexa o resultado pela descrição normalizada, somando os grupos
// que se separaram por (kind, conta, categoria). É a dobra que o serviço vai
// fazer para montar UMA linha por descrição no prompt.
func porNorma(rows []transaction.DescriptionGroup) map[string]transaction.DescriptionGroup {
	out := make(map[string]transaction.DescriptionGroup, len(rows))
	for _, r := range rows {
		acc := out[r.DescriptionNorm]
		acc.DescriptionNorm = r.DescriptionNorm
		acc.Count += r.Count
		acc.TotalCents += r.TotalCents
		out[r.DescriptionNorm] = acc
	}
	return out
}

// BOLA é o risco nº 1 (docs/SEGURANCA.md §2): com DUAS casas povoadas, com as
// MESMAS descrições, nos MESMOS meses, cada uma só pode ver o seu.
//
// As descrições são iguais de propósito: se o WHERE de casa caísse, o defeito
// apareceria como uma contagem inflada — e não como uma linha estranha —, que
// é a forma que passa despercebida numa revisão de olho.
func TestGroupByDescriptionIsolaAsCasas(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		contaMinha := s.makeAccount(t, ctx, minha.ID, "Minha Conta")
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Conta Alheia")

		for range 3 {
			s.makeTransaction(t, ctx, minha.ID, contaMinha.ID, txSpec{
				Description:     "Mercado do Seu Jose",
				AmountCents:     1_000,
				OccurredOn:      civil.MustNew(2026, 8, 10),
				CompetenceMonth: "2026-08",
			})
		}
		for range 7 {
			s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
				Description:     "Mercado do Seu Jose",
				AmountCents:     9_900,
				OccurredOn:      civil.MustNew(2026, 8, 10),
				CompetenceMonth: "2026-08",
			})
		}

		minhas, err := s.transactions.GroupByDescription(ctx, minha.ID, janela3,
			transaction.MaxDescriptionGroupRows)
		require.NoError(t, err)
		require.Len(t, minhas, 1)
		assert.EqualValues(t, 3, minhas[0].Count, "só os 3 lançamentos da minha casa")
		assert.EqualValues(t, 3_000, minhas[0].TotalCents)

		alheias, err := s.transactions.GroupByDescription(ctx, alheia.ID, janela3,
			transaction.MaxDescriptionGroupRows)
		require.NoError(t, err)
		require.Len(t, alheias, 1)
		assert.EqualValues(t, 7, alheias[0].Count)
		assert.EqualValues(t, 69_300, alheias[0].TotalCents)

		// Casa inexistente é indistinguível de casa vazia — nunca vaza o
		// conteúdo alheio por outro caminho.
		nenhuma, err := s.transactions.GroupByDescription(ctx, "casa-que-nao-existe", janela3,
			transaction.MaxDescriptionGroupRows)
		require.NoError(t, err)
		assert.Empty(t, nenhuma)
	})
}

// Critério 5 da spec 0010: duas descrições que só diferem em ACENTO e CAIXA
// viram UMA linha, com a soma das ocorrências e dos centavos. Critério 10: o
// total do grupo bate com a soma dos lançamentos daquele grupo.
func TestGroupByDescriptionAgrupaPorNormaIgnorandoAcentoECaixa(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")

		grafias := []string{"MERCADO DO SEU JOSÉ", "mercado do seu jose", "Mercado do Seu José"}
		var esperadoCentavos int64
		for i, g := range grafias {
			cents := int64(1_000 * (i + 1))
			esperadoCentavos += cents
			s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
				Description:     g,
				AmountCents:     cents,
				OccurredOn:      civil.MustNew(2026, 9, 3),
				CompetenceMonth: "2026-09",
			})
		}

		rows, err := s.transactions.GroupByDescription(ctx, minha.ID, janela3,
			transaction.MaxDescriptionGroupRows)
		require.NoError(t, err)
		require.Len(t, rows, 1, "as três grafias são UMA descrição")

		assert.Equal(t, textnorm.Normalize("Mercado do Seu José"), rows[0].DescriptionNorm)
		assert.EqualValues(t, 3, rows[0].Count)
		assert.EqualValues(t, esperadoCentavos, rows[0].TotalCents)

		// A AMOSTRA é amostra, não contrato: qual das três grafias o
		// MIN(description) escolhe depende da COLLATION do dialeto, e as três
		// respostas estão certas. O teste afirma que veio UMA DELAS — nunca
		// QUAL delas. Assertar a grafia aqui seria plantar um teste que passa
		// em SQLite e quebra no primeiro MySQL.
		assert.Contains(t, grafias, rows[0].SampleDescription,
			"a amostra tem de ser uma das grafias reais do grupo")
	})
}

// A chave do agrupamento tem QUATRO colunas, e cada uma existe por uma
// pergunta do prompt: kind e categoria porque a mesma descrição pode ter sido
// classificada de formas diferentes (é isso que vira "várias" na linha), conta
// porque o prompt diz em que contas a descrição apareceu.
//
// Este teste é também a medição da RAZÃO DE EXPANSÃO em miniatura: 1 descrição
// distinta produzindo 3 linhas.
func TestGroupByDescriptionSeparaPorKindContaECategoria(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		contaA := s.makeAccount(t, ctx, minha.ID, "Conta A")
		contaB := s.makeAccount(t, ctx, minha.ID, "Conta B")
		cat := s.makeCategory(t, ctx, minha.ID, "Mercado", category.KindExpense, nil)

		const desc = "Padaria Dona Deola"
		comum := txSpec{
			Description:     desc,
			AmountCents:     500,
			OccurredOn:      civil.MustNew(2026, 7, 5),
			CompetenceMonth: "2026-07",
		}

		// (a) conta A, sem categoria
		s.makeTransaction(t, ctx, minha.ID, contaA.ID, comum)
		// (b) conta A, COM categoria
		comB := comum
		comB.CategoryID = &cat.ID
		s.makeTransaction(t, ctx, minha.ID, contaA.ID, comB)
		// (c) conta B, sem categoria
		s.makeTransaction(t, ctx, minha.ID, contaB.ID, comum)

		rows, err := s.transactions.GroupByDescription(ctx, minha.ID, janela3,
			transaction.MaxDescriptionGroupRows)
		require.NoError(t, err)
		require.Len(t, rows, 3, "uma descrição distinta, três linhas")

		dobrado := porNorma(rows)
		require.Len(t, dobrado, 1, "as três linhas são UMA descrição distinta")
		assert.EqualValues(t, 3, dobrado[textnorm.Normalize(desc)].Count)
		assert.EqualValues(t, 1_500, dobrado[textnorm.Normalize(desc)].TotalCents)

		// A linha de category_id NULL existe e é UMA só: os quatro dialetos
		// agrupam os nulos juntos, e é ela que vira o "—" do prompt.
		var nulas, comCategoria int
		for _, r := range rows {
			if r.CategoryID == nil {
				nulas++
				continue
			}
			comCategoria++
			assert.Equal(t, cat.ID, *r.CategoryID)
		}
		assert.Equal(t, 2, nulas, "duas contas sem categoria, duas linhas nulas")
		assert.Equal(t, 1, comCategoria)
	})
}

// O WHERE tem três partes, e as três precisam de prova: a janela de
// competência, o deleted_at IS NULL e a casa (que é o teste de BOLA acima).
func TestGroupByDescriptionIgnoraExcluidoEForaDaJanela(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")

		dentro := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Description:     "Dentro da Janela",
			OccurredOn:      civil.MustNew(2026, 8, 1),
			CompetenceMonth: "2026-08",
		})
		require.NotNil(t, dentro)

		s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Description:     "Fora da Janela",
			OccurredOn:      civil.MustNew(2026, 6, 1),
			CompetenceMonth: "2026-06",
		})
		excluida := s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
			Description:     "Excluida Logicamente",
			OccurredOn:      civil.MustNew(2026, 8, 2),
			CompetenceMonth: "2026-08",
		})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, excluida.ID, now()))

		rows, err := s.transactions.GroupByDescription(ctx, minha.ID, janela3,
			transaction.MaxDescriptionGroupRows)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		assert.Equal(t, textnorm.Normalize("Dentro da Janela"), rows[0].DescriptionNorm)

		// Janela de UM mês continua funcionando — o IN aceita de 1 a 3.
		umMes, err := s.transactions.GroupByDescription(ctx, minha.ID, []string{"2026-06"},
			transaction.MaxDescriptionGroupRows)
		require.NoError(t, err)
		require.Len(t, umMes, 1)
		assert.Equal(t, textnorm.Normalize("Fora da Janela"), umMes[0].DescriptionNorm)
	})
}

// As guardas recusam ANTES de ir ao banco. Casa vazia é a porta do BOLA; mês
// vazio devolveria um prompt sem movimentação em silêncio; janela e limite
// fora da faixa são o rail de memória, que não pode depender de quem chama
// lembrar dele.
func TestGroupByDescriptionRecusaEntradaInvalida(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)

		casos := []struct {
			nome  string
			casa  string
			meses []string
			limit int
		}{
			{"casa vazia", "", janela3, 10},
			{"sem mês", minha.ID, nil, 10},
			{"mês vazio na janela", minha.ID, []string{"2026-08", ""}, 10},
			{"mês só de espaços", minha.ID, []string{"   "}, 10},
			{"janela de 4 meses", minha.ID, []string{"2026-06", "2026-07", "2026-08", "2026-09"}, 10},
			{"limite zero", minha.ID, janela3, 0},
			{"limite negativo", minha.ID, janela3, -1},
			{"limite acima do teto", minha.ID, janela3, transaction.MaxDescriptionGroupRows + 1},
		}
		for _, c := range casos {
			t.Run(c.nome, func(t *testing.T) {
				rows, err := s.transactions.GroupByDescription(ctx, c.casa, c.meses, c.limit)
				require.Error(t, err)
				assert.Nil(t, rows)
			})
		}
	})
}

// TUDO OU NADA: com uma linha a mais que o limite, o repositório devolve
// ErrTooManyDescriptionGroups e NENHUMA linha.
//
// Resposta parcial é o único resultado de verdade ruim aqui — um prompt que
// parece completo e não é faria a IA propor palavra-chave para metade da casa
// sem ninguém saber. O teste prova os três pontos da fronteira: N-1 passa, N
// passa, N+1 estoura.
func TestGroupByDescriptionEhTudoOuNadaAcimaDoTeto(t *testing.T) {
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, _ := s.duasCasas(t, ctx)
		conta := s.makeAccount(t, ctx, minha.ID, "Conta")

		const grupos = 5
		for i := range grupos {
			s.makeTransaction(t, ctx, minha.ID, conta.ID, txSpec{
				Description:     fmt.Sprintf("Estabelecimento %02d", i),
				OccurredOn:      civil.MustNew(2026, 9, 2),
				CompetenceMonth: "2026-09",
			})
		}

		// Limite exatamente igual ao número de grupos: passa, inteiro.
		exato, err := s.transactions.GroupByDescription(ctx, minha.ID, janela3, grupos)
		require.NoError(t, err)
		assert.Len(t, exato, grupos)

		// Limite maior: passa igual.
		folgado, err := s.transactions.GroupByDescription(ctx, minha.ID, janela3, grupos+1)
		require.NoError(t, err)
		assert.Len(t, folgado, grupos)

		// Limite de uma linha a menos: estoura, e não devolve NADA.
		curto, err := s.transactions.GroupByDescription(ctx, minha.ID, janela3, grupos-1)
		require.ErrorIs(t, err, transaction.ErrTooManyDescriptionGroups)
		assert.Nil(t, curto, "resposta parcial é pior que erro")
	})
}

// Volume: a agregação de uma casa pesada tem de sair em UMA consulta, com um
// orçamento de parâmetros CONSTANTE — nada aqui pode crescer com o número de
// lançamentos, e o cliente não define o custo da consulta.
//
// O tempo é MEDIDO e IMPRESSO, nunca assertado: tempo de máquina de CI não é
// critério de correção (mesma regra de dashboard_volume_test.go e
// report_volume_test.go). O que o teste asserta é a contagem de consultas e o
// orçamento de parâmetros.
func TestGroupByDescriptionEmVolumeEhUmaConsultaSo(t *testing.T) {
	if testing.Short() {
		t.Skip("volume: pulado em -short")
	}
	t.Parallel()

	eachBackend(t, func(t *testing.T, s *store) {
		ctx := t.Context()
		minha, alheia := s.duasCasas(t, ctx)
		contaAlheia := s.makeAccount(t, ctx, alheia.ID, "Alheia")

		const nContas = 10
		contas := make([]string, 0, nContas)
		for i := range nContas {
			contas = append(contas, s.makeAccount(t, ctx, minha.ID, fmt.Sprintf("Conta %02d", i)).ID)
		}

		// 200 categorias: o teto da taxonomia (50 grupos × 3 filhas + 50).
		cats := make([]string, 0, category.MaxPerHousehold)
		for g := range 50 {
			pai := s.makeCategory(t, ctx, minha.ID, fmt.Sprintf("Grupo %02d", g), category.KindExpense, nil)
			cats = append(cats, pai.ID)
			for f := range 3 {
				filha := s.makeCategory(t, ctx, minha.ID,
					fmt.Sprintf("Filha %02d %d", g, f), category.KindExpense, &pai.ID)
				cats = append(cats, filha.ID)
			}
		}
		require.Len(t, cats, category.MaxPerHousehold)

		// 3.000 lançamentos vivos na janela, sobre 600 descrições distintas —
		// a forma medida em 21/09/2026 para a "casa pesada" (o corpus real do
		// banco de desenvolvimento tem 78 descrições distintas em 2 meses).
		const nVivos = 3_000
		const nDescricoes = 600
		esperado := map[string]int64{}
		lote := make([]transaction.Transaction, 0, 500)
		enviar := func() {
			if len(lote) == 0 {
				return
			}
			require.NoError(t, s.transactions.CreateBatch(ctx, minha.ID, lote))
			lote = lote[:0]
		}
		for i := range nVivos {
			desc := fmt.Sprintf("Estabelecimento Numero %04d", i%nDescricoes)
			mes := janela3[i%len(janela3)]

			cents := int64(100 + i)
			esperado[textnorm.Normalize(desc)] += cents
			// A conta e a categoria acompanham a DESCRIÇÃO, como no mundo
			// real (um estabelecimento cai quase sempre na mesma conta), com
			// 1 em 5 caindo noutro lugar — é o que produz a RAZÃO DE EXPANSÃO
			// medida em 21/09/2026 (1,06 no banco real; 1,17 a 1,35 no corpus
			// sintético correlacionado).
			descIdx, repeticao := i%nDescricoes, i/nDescricoes
			idxConta := descIdx % nContas
			idxCat := descIdx % len(cats)
			if repeticao == 0 && descIdx%4 == 0 {
				idxConta = (idxConta + 1) % nContas
				idxCat = (idxCat + 1) % len(cats)
			}
			var cat *string
			if i%4 != 0 {
				cat = &cats[idxCat]
			}
			lote = append(lote, transaction.Transaction{
				ID:              s.nextID("t"),
				HouseholdID:     minha.ID,
				Kind:            transaction.KindExpense,
				AccountID:       contas[idxConta],
				CategoryID:      cat,
				AmountCents:     cents,
				Description:     desc,
				DescriptionNorm: textnorm.Normalize(desc),
				OccurredOn:      civil.MustNew(2026, 9, 1+i%28),
				CompetenceMonth: mes,
				Source:          transaction.SourceImport,
				DedupKey:        s.nextID("dk"),
				DedupOrdinal:    1,
				CreatedBy:       s.nextID("u"),
				CreatedAt:       now(),
				UpdatedAt:       now(),
			})
			if len(lote) == cap(lote) {
				enviar()
			}
		}
		enviar()

		// Ruído que NÃO pode aparecer: outra casa, outro mês, e excluído.
		s.makeTransaction(t, ctx, alheia.ID, contaAlheia.ID, txSpec{
			Description: "Estabelecimento Numero 0000", CompetenceMonth: "2026-09",
			OccurredOn: civil.MustNew(2026, 9, 9),
		})
		s.makeTransaction(t, ctx, minha.ID, contas[0], txSpec{
			Description: "Estabelecimento Numero 0000", CompetenceMonth: "2026-05",
			OccurredOn: civil.MustNew(2026, 5, 9),
		})
		morto := s.makeTransaction(t, ctx, minha.ID, contas[0], txSpec{
			Description: "Estabelecimento Numero 0000", CompetenceMonth: "2026-09",
			OccurredOn: civil.MustNew(2026, 9, 9), AmountCents: 777_777,
		})
		require.NoError(t, s.transactions.SoftDelete(ctx, minha.ID, morto.ID, now()))

		contador := instrumentarConsultas(t, s)
		spy := s.espiarSQL(t)
		spy.ligar()

		inicio := time.Now()
		rows, err := s.transactions.GroupByDescription(ctx, minha.ID, janela3,
			transaction.MaxDescriptionGroupRows)
		decorrido := time.Since(inicio)
		require.NoError(t, err)

		assert.EqualValues(t, 1, contador.n.Load(), "a agregação é UMA consulta — mais que isso é N+1")
		assert.LessOrEqual(t, len(rows), transaction.MaxDescriptionGroupRows)
		assert.Greater(t, len(rows), nDescricoes,
			"a chave tem 4 colunas: a expansão sobre as descrições distintas é real, e é ela que o rail precisa aguentar")

		// O orçamento de parâmetros é CONSTANTE e minúsculo: casa + 3 meses +
		// limite = 5 no pior caso, contra o piso de 999 do SQLite e os 2100 do
		// SQL Server. Nem o número de lançamentos nem o de categorias nem o de
		// contas entram no SQL — nenhum id de conta ou de categoria viaja
		// ligado a esta consulta.
		for _, c := range spy.comandosEmitidos() {
			assert.LessOrEqual(t, c.Parametros, 5,
				"orçamento de parâmetros tem de ser constante: %s", c.SQL)
		}

		// A dobra por descrição reconstrói exatamente o que foi semeado: é o
		// critério 10 da spec 0010 em volume — o total de cada grupo bate com
		// a soma dos lançamentos daquele grupo, e o ruído não entrou.
		dobrado := porNorma(rows)
		assert.Len(t, dobrado, nDescricoes)
		var somaGrupos int64
		for norma, g := range dobrado {
			require.Contains(t, esperado, norma)
			assert.EqualValues(t, esperado[norma], g.TotalCents, "descrição %q", norma)
			somaGrupos += g.TotalCents
		}
		var somaSemeada int64
		for _, v := range esperado {
			somaSemeada += v
		}
		assert.EqualValues(t, somaSemeada, somaGrupos)

		// Ordem: o ORDER BY cnt DESC é do SQL, e existe porque o MSSQL exige
		// um ORDER BY para emitir o LIMIT. A ordenação de VERDADE, com
		// desempate estável, é do serviço em Go — o teste confere só que as
		// contagens não vêm crescendo.
		contagens := make([]int64, 0, len(rows))
		for _, r := range rows {
			contagens = append(contagens, r.Count)
		}
		assert.True(t, slices.IsSortedFunc(contagens, func(a, b int64) int { return int(b - a) }),
			"o SQL devolve por ocorrências decrescente")

		t.Logf("%s: %d lançamentos vivos, %d descrições distintas → %d linhas "+
			"(razão de expansão %.3f), %d consulta(s), %s (race=%t)",
			s.backendName, nVivos, nDescricoes, len(rows),
			float64(len(rows))/float64(nDescricoes), contador.n.Load(), decorrido, raceEnabled)
	})
}
