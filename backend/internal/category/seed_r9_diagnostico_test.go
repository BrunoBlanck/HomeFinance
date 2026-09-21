package category_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/brunorblanck/homefinance/backend/internal/textmatch"
)

// DIAGNÓSTICO da candidata a R9 — "modificador de marca vaza o pedaço comum".
//
// ⚠️ R9 NÃO É INVARIANTE. Ela foi proposta na reconferência de 18/09/2026,
// medida, e RETIRADA pelo próprio revisor: ver o bloco "por que ela não virou
// trava", abaixo. Este teste imprime o inventário mecanizável (`go test -v
// -run TestDiagnosticoR9`) e trava UMA coisa só — a CONTAGEM.
//
// POR QUE A CONTAGEM É TRAVADA, se a regra foi retirada: um checklist impresso
// tem um defeito fatal, que é ninguém ser obrigado a olhar. Com
// `assert.Equal` na contagem, quem acrescentar amanhã uma marca colada cuja
// fatia seja palavra do projeto vê o BUILD QUEBRAR e é trazido até este
// comentário, onde estão os números e o critério. É a mesma forma do achado A3
// (igualdade exata em vez de piso com folga) aplicada a um inventário — e
// custa uma linha. Trocar uma fatia por outra continua sendo um diff visível;
// acrescentar uma em silêncio, não.
//
// ⚠️ A FRONTEIRA DESTE INSTRUMENTO, que é o que impede ler "6" como inventário
// completo. `palavrasConhecidasDoProjeto` lê `tokensDeRotina` mais os tokens da
// semente, e um token que é EXCEÇÃO ACEITA nunca pode estar em
// `tokensDeRotina` — ele quebraria o teste principal, que é exatamente o motivo
// de ele ser exceção. Logo **`ultra` e `colar` JAMAIS aparecerão aqui**: dois
// dos achados que motivaram a regra são invisíveis para o instrumento que a
// mede.
//
// E quando um deles aparece, é por ACIDENTE, não por mérito da heurística —
// vale saber ler os dois casos da lista de hoje: `smart` só é "conhecido"
// porque «smart fit» virou FRASE e escreveu o token na semente (antes dessa
// troca ele seria invisível), e `mercado` só é "conhecido" porque «mercado
// livre» o contém, não por estar na lista de rotina, de onde ele está
// explicitamente excluído.
//
// Nada disso é defeito: é o limite de medir "é palavra?" sem dicionário. O
// INVENTÁRIO COMPLETO é o bloco "O QUE FICOU DELIBERADAMENTE DE FORA" em
// `seed_test.go` (que o achado A2 tornou completo); este diagnóstico é só a
// fatia mecanizável dele.
//
// A REGRA PROPOSTA: palavra-chave escrita COLADA cujo prefixo ou sufixo de ≥ 5
// runas seja palavra de uso comum vaza esse pedaço, porque a regra 2 do motor
// (maior substring comum ≥ 5 runas) alcança a palavra inteira a partir da
// fatia. Foi assim que «amazonprime» entregou `prime` (84), «smartfit»
// entregou `smart` (89), «ultragaz» entregou `ultra` (89) e «decolar» entregou
// `colar` (91) — mesma mecânica de `mercado` dentro de «minimercado».
//
// POR QUE ELA NÃO VIROU TRAVA NESTA ENTREGA — o que a medição mostrou:
//
//  1. a regra crua acusa ~1.350 fatias, e a esmagadora maioria é TRUNCAMENTO
//     que não é palavra ("upermercado", "abeleireiro", "adiantament"). O
//     discriminador de verdade é "a fatia é palavra comum?", e essa pergunta
//     não se responde sem um dicionário de português no repositório — que é
//     dependência nova, decisão de arquitetura, não de teste;
//  2. o filtro automático que parecia óbvio — "a fatia vence para OUTRA dona" —
//     é o SINAL ERRADO, e é importante dizer: ele derruba a lista para 9 casos,
//     mas perderia TODOS os quatro achados do N1. `colar` vence para Viagens,
//     que é a dona de «decolar»; `ultra` vence para Água, luz e gás, dona de
//     «ultragaz»; `mercado` vence para Supermercado, dona de «minimercado». O
//     estrago do N1 não é a fatia sugerir a folha errada — é uma DESCRIÇÃO
//     ALHEIA que contém a fatia cair naquela folha.
//
// O que sobra como automação honesta, e é o que este diagnóstico faz: marcar a
// fatia que JÁ É PALAVRA CONHECIDA do projeto — token da lista de rotina ou
// palavra-chave de outra folha. Isso pega `posto` sem dicionário (ele é uma
// palavra-chave de verdade), mas não se sustenta sozinho: não pegaria `ultra`
// nem `colar` — ver a fronteira, acima.
//
// Por isso R9 não reprova PALAVRA nenhuma: ela vale como checklist, e o que
// este arquivo trava é só a CONTAGEM do checklist, para que ele seja lido.
func TestDiagnosticoR9FatiasDePalavra(t *testing.T) {
	t.Parallel()

	conhecidas := palavrasConhecidasDoProjeto(t)

	type achado struct {
		lado     string
		palavra  string
		dona     string
		fatia    string
		contra   int
		vencedor string
		nota     int
	}
	var achados []achado
	var mudas, truncamentos int

	for _, lado := range ladosDoDinheiro {
		m := matcherDoLado(t, lado)
		vistas := map[string]struct{}{}

		for _, f := range folhasDaSemente() {
			if f.lado != lado {
				continue
			}
			for _, p := range f.keywords {
				tokens := textmatch.Tokenize(normaDaPalavra(t, p))
				// Frase já exige os dois tokens: o vazamento é problema da
				// palavra COLADA, de token único.
				if len(tokens) != 1 {
					continue
				}
				for _, fatia := range fatiasDe(tokens[0]) {
					if _, repetida := vistas[fatia]; repetida {
						continue
					}
					vistas[fatia] = struct{}{}

					if textmatch.ScoreKeyword([]string{fatia}, tokens) < textmatch.MinScore {
						continue
					}
					res, err := m.Best(fatia)
					if err != nil {
						t.Fatal(err)
					}
					if !res.Matched() {
						mudas++
						continue
					}
					_, conhecida := conhecidas[fatia]
					if !conhecida {
						truncamentos++
						continue
					}
					achados = append(achados, achado{
						lado: lado, palavra: p, dona: f.dona, fatia: fatia,
						contra:   textmatch.ScoreKeyword([]string{fatia}, tokens),
						vencedor: res.Match.OwnerID, nota: res.Match.Score,
					})
				}
			}
		}
	}

	sort.Slice(achados, func(i, j int) bool { return achados[i].fatia < achados[j].fatia })

	t.Logf("R9: %d fatias são PALAVRA CONHECIDA do projeto; "+
		"%d são truncamento sem significado; %d não sugerem nada",
		len(achados), truncamentos, mudas)
	for _, a := range achados {
		t.Logf("  %-12s ⊂ «%s» (%s) | %d contra a palavra | hoje vence %s a %d",
			a.fatia, a.palavra, a.lado, a.contra, a.vencedor, a.nota)
	}

	// A ÚNICA asserção do arquivo, e ela é sobre a CONTAGEM, não sobre o
	// conteúdo — R9 não reprova palavra nenhuma. As seis de hoje, todas
	// inofensivas porque a dona legítima vence, estão na tabela da spec 0005
	// §20.6 com a folga de cada uma: `mercado`, `smart`, `posto`, `estacio`,
	// `bilhete`, `cross`.
	assert.Equal(t, 6, len(achados),
		"acrescentar marca colada cuja fatia é palavra do projeto exige olhar esta lista e emendar o ADR-033")
}

// palavrasConhecidasDoProjeto é o único discriminador de "é palavra" que cabe
// sem dicionário: o token que o projeto já escreveu em algum lugar — na lista
// de rotina ou como palavra-chave de alguma folha.
//
// É reconhecidamente incompleto, e a incompletude é o ponto do diagnóstico: uma
// fatia só é "conhecida" depois que alguém a escreveu, então esta heurística
// nunca teria achado `ultra` ou `colar` sozinha.
func palavrasConhecidasDoProjeto(t *testing.T) map[string]struct{} {
	t.Helper()

	out := make(map[string]struct{}, len(tokensDeRotina)+512)
	for _, tok := range tokensDeRotina {
		out[tok] = struct{}{}
	}
	for _, f := range folhasDaSemente() {
		for _, p := range f.keywords {
			for _, tok := range textmatch.Tokenize(normaDaPalavra(t, p)) {
				out[tok] = struct{}{}
			}
		}
	}
	return out
}

// fatiasDe devolve todo prefixo e sufixo PRÓPRIO do token com ao menos 5 runas
// — o mínimo que a regra 2 do motor exige para casar por substring.
func fatiasDe(token string) []string {
	r := []rune(token)
	n := len(r)
	if n <= textmatch.MinFuzzyRunes {
		return nil
	}
	out := make([]string, 0, 2*(n-textmatch.MinFuzzyRunes))
	for tam := textmatch.MinFuzzyRunes; tam < n; tam++ {
		prefixo, sufixo := string(r[:tam]), string(r[n-tam:])
		out = append(out, prefixo)
		if sufixo != prefixo {
			out = append(out, sufixo)
		}
	}
	return out
}
