package aiimport

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
	"github.com/brunorblanck/homefinance/backend/internal/transaction"
)

// errContagemInconsistente — uma linha agregada veio com contagem NEGATIVA,
// ou a soma delas não coube em int. `COUNT(*)` nunca é negativo, então só
// banco adulterado chega aqui: falha FECHADA, 500 genérico, nenhum número
// inventado no relatório.
var errContagemInconsistente = errors.New("contagem de lançamentos da janela fora da faixa representável")

// periodoAgregado é a janela de trabalho DOBRADA para a medição: as
// ocorrências de cada descrição normalizada entre os lançamentos vivos
// `income`/`expense`, e o total delas.
//
// Só `income`/`expense` de propósito (spec 0010 §4.4): transferência já É
// transferência — não vira candidata a nada —, e é por isso que ela fica de
// fora do denominador também.
type periodoAgregado struct {
	ocorrenciasPorNorm map[string]int64
	total              int
}

// dobrarPeriodo reduz as linhas de GroupByDescription (uma por descrição ×
// kind × conta × categoria) a uma contagem por descrição normalizada.
func dobrarPeriodo(linhas []transaction.DescriptionGroup) (periodoAgregado, error) {
	out := periodoAgregado{ocorrenciasPorNorm: make(map[string]int64, len(linhas))}
	var total int64
	for i := range linhas {
		l := linhas[i]
		if l.Kind != transaction.KindIncome && l.Kind != transaction.KindExpense {
			continue
		}
		if l.Count < 0 {
			return periodoAgregado{}, fmt.Errorf("%w: contagem negativa", errContagemInconsistente)
		}
		soma := out.ocorrenciasPorNorm[l.DescriptionNorm] + l.Count
		if soma < out.ocorrenciasPorNorm[l.DescriptionNorm] {
			return periodoAgregado{}, fmt.Errorf("%w: soma por descrição estourou", errContagemInconsistente)
		}
		out.ocorrenciasPorNorm[l.DescriptionNorm] = soma
		if total += l.Count; total < 0 || total > int64(maxInt) {
			return periodoAgregado{}, fmt.Errorf("%w: total da janela estourou", errContagemInconsistente)
		}
	}
	out.total = int(total)
	return out, nil
}

// medidor é a medição de impacto de UMA prévia: um Matcher com uma entrada
// por (item de conta, palavra que entraria) e o mapa de volta.
//
// Existe como tipo, e não como função só, para o teste de heap medir o que a
// requisição RETÉM enquanto o matcher está vivo — é esse número que entra na
// conta por casa de textmatch/matcher.go, e medir depois de o matcher morrer
// mediria zero.
type medidor struct {
	matcher *textmatch.Matcher

	// donos é o índice das entradas do matcher: o OwnerID de cada palavra é a
	// posição dela aqui, e é daqui que a medição volta ao item e à palavra.
	donos []donoDaMedicao
}

// donoDaMedicao aponta a palavra de volta ao relatório: o item (posição em
// plano.itens) e a posição da palavra em `added` daquele item.
type donoDaMedicao struct {
	item    int
	palavra int
}

// montarMedidor prepara a medição (spec 0010 §4.4) para TODO item de conta do
// plano: cada um ganha `impact` (zerado), e cada palavra que ENTRARIA vira uma
// entrada do matcher com dono PRÓPRIO — é assim que o resultado sai POR
// PALAVRA, que é o que a tela precisa para a genérica saltar aos olhos
// sozinha ("pagamento", 87) ao lado da legítima ("nu pagamentos", 4).
//
// UM textmatch.Budget para a operação inteira (achado A1 da revisão do
// classificador), não um por palavra: o teto tem de ser o da REQUISIÇÃO, e é
// este orçamento que entra na conta de heap por casa (28 operações no pior
// empilhamento, ADR-036 (f)). Devolve nil quando não há o que medir.
func montarMedidor(p *plano, work int64) (*medidor, error) {
	var palavras []textmatch.Keyword
	var donos []donoDaMedicao
	for i := range p.itens {
		item := &p.itens[i]
		if item.view.Type != ItemTypeAccount {
			continue
		}
		item.view.Impact = &Impact{ByKeyword: make([]KeywordImpact, 0, len(item.view.Added))}
		if item.dona == "" {
			continue
		}
		for j, kw := range item.view.Added {
			item.view.Impact.ByKeyword = append(item.view.Impact.ByKeyword, KeywordImpact{Keyword: kw})
			palavras = append(palavras, textmatch.Keyword{OwnerID: strconv.Itoa(len(donos)), Keyword: kw})
			donos = append(donos, donoDaMedicao{item: i, palavra: j})
		}
	}
	if len(palavras) == 0 {
		return nil, nil
	}
	m, err := textmatch.NewMatcher(palavras, textmatch.WithBudget(textmatch.NewBudget(work)))
	if err != nil {
		// As palavras passaram por ValidateKeyword; o Matcher só recusa o
		// que aquela validação deixaria passar. Erro embrulhado SEM a
		// palavra.
		return nil, fmt.Errorf("montando o matcher da medição de impacto: %w", err)
	}
	return &medidor{matcher: m, donos: donos}, nil
}

// medir roda o matcher sobre cada descrição da janela e preenche, em cada
// item de conta, o impacto POR PALAVRA e o TOTAL do item:
//
//   - por palavra: quantos lançamentos a palavra alcança com pontuação ≥
//     textmatch.MinScore, contados por ocorrência;
//   - do item: a UNIÃO — o lançamento alcançado por duas palavras do mesmo
//     item conta uma vez. Por isso a soma de `byKeyword` pode passar do total.
//
// `Rank` e não `Best`: a medição é "quantos ESTA palavra alcançaria",
// independente de empate com palavras de outras contas — é o estrago
// potencial. Nada é escrito. Estourar o orçamento é
// textmatch.ErrWorkBudgetExceeded, nunca uma medição parcial: quem recebe
// aborta a prévia inteira (422).
func (m *medidor) medir(p *plano, periodo periodoAgregado) error {
	if m == nil {
		return nil
	}
	porPalavra := make([]int64, len(m.donos))
	porItem := make(map[int]int64)
	alcancados := make(map[int]struct{}, 8)

	for norm, ocorrencias := range periodo.ocorrenciasPorNorm {
		rank, err := m.matcher.Rank(norm)
		if err != nil {
			return fmt.Errorf("medindo o impacto das palavras-chave de conta: %w", err)
		}
		clear(alcancados)
		for _, match := range rank {
			if match.Score < textmatch.MinScore {
				// O ranking vem ordenado por pontuação decrescente: abaixo
				// do limiar não há mais o que contar.
				break
			}
			d, err := strconv.Atoi(match.OwnerID)
			if err != nil || d < 0 || d >= len(m.donos) {
				return fmt.Errorf("%w: dono da medição fora do plano", errContagemInconsistente)
			}
			if porPalavra[d] += ocorrencias; porPalavra[d] < 0 {
				return fmt.Errorf("%w: alcance de uma palavra estourou", errContagemInconsistente)
			}
			item := m.donos[d].item
			if _, ja := alcancados[item]; ja {
				continue
			}
			alcancados[item] = struct{}{}
			if porItem[item] += ocorrencias; porItem[item] < 0 {
				return fmt.Errorf("%w: alcance de um item estourou", errContagemInconsistente)
			}
		}
	}

	for d, n := range porPalavra {
		dono := m.donos[d]
		impacto := p.itens[dono.item].view.Impact
		if impacto == nil || dono.palavra >= len(impacto.ByKeyword) || n > int64(maxInt) {
			return fmt.Errorf("%w: medição sem lugar no relatório", errContagemInconsistente)
		}
		impacto.ByKeyword[dono.palavra].TransferCandidates = int(n)
	}
	for item, n := range porItem {
		if n > int64(maxInt) {
			return fmt.Errorf("%w: alcance de um item estourou", errContagemInconsistente)
		}
		p.itens[item].view.Impact.TransferCandidates = int(n)
	}
	return nil
}

// medirImpacto é montarMedidor + medir, o caminho da prévia.
func medirImpacto(p *plano, periodo periodoAgregado, work int64) error {
	m, err := montarMedidor(p, work)
	if err != nil {
		return err
	}
	return m.medir(p, periodo)
}

// maxInt é o maior valor de `int` na plataforma — guarda das conversões
// int64 → int dos contadores publicados no DTO.
const maxInt = int(^uint(0) >> 1)
