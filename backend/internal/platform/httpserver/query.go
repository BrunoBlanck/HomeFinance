package httpserver

import (
	"errors"
	"net/url"
)

// ErrRepeatedQueryParam — a mesma chave de query apareceu mais de uma vez.
//
// É erro de VALIDAÇÃO DE ENTRADA (docs/SEGURANCA.md §3), traduzido pelo
// handler em 400 com uma mensagem própria do parâmetro. Não é classe nova de
// erro do contrato: o enum de códigos é fechado, e parâmetro repetido é
// entrada malformada como qualquer outra.
var ErrRepeatedQueryParam = errors.New("parâmetro de query repetido")

// SoleQueryValue devolve o valor de `chave` exigindo que ela apareça NO MÁXIMO
// UMA VEZ na query.
//
// # Pré-condição garantida pela CADEIA
//
// Quem chama recebe uma `url.Values` que foi parseada SEM ERRO: o middleware
// WellFormedQuery (o mais interno da cadeia, em cmd/api/main.go) responde 400
// antes do mux quando `url.ParseQuery(r.URL.RawQuery)` falha. Sem ele,
// `r.URL.Query()` descartaria o erro e devolveria a query com o par ilegível
// PULADO — e `?x=a&x=b%` chegaria aqui como UMA ocorrência, com a guarda de
// HPP abaixo nem sendo acionada.
//
// # O que ele conserta
//
// `url.Values.Get` devolve o PRIMEIRO valor e descarta o resto em silêncio.
// Com `?x=a&x=b`, quem lê o primeiro e quem lê o último respondem coisas
// diferentes para a MESMA URL — e o caminho de uma requisição tem vários
// leitores que não são obrigados a concordar: proxy reverso, WAF, coletor de
// log, cache, o próprio cliente. É o HTTP Parameter Pollution: a tela afirma
// "Despesas" e o intermediário registra "Receitas", ou vice-versa.
//
// A recusa é da AMBIGUIDADE DA URL, e não da discordância dos valores. Por
// isso `?x=a&x=a` (valores iguais) e `?x=a&x=` (a segunda vazia) também são
// erro: perguntar "mas eles concordam?" seria emitir uma segunda opinião sobre
// uma URL que já é ambígua, e regra sem análise de caso é mais barata de
// escrever, testar e revisar. A segunda vazia é, aliás, a pior das três — quem
// ler a última ocorrência recebe "sem filtro", o recorte evapora e a tela
// mostra tudo sob o rótulo do recorte.
//
// UMA ocorrência vazia (`?x=`) NÃO é erro: ela não é ambígua, e continua
// significando o que o parâmetro define para o valor vazio.
//
// # Onde ele JÁ é adotado, e onde AINDA NÃO
//
// Adotado: `kindGroup` em GET /transactions (spec 0004 §12.5.5, ADR-030),
// `month` em GET /dashboard (spec 0008, ADR-031) e os TRÊS parâmetros de
// GET /reports/by-category — `month`, `kind` e `accountGroup` (ADR-032), cada
// um com a sua redação própria de "informe uma única vez".
//
// ⚠️ AINDA NÃO ADOTADO, e isto é DÍVIDA CONHECIDA, não descuido:
// **`accountId`**, **`limit`** e **`cursor`** continuam lidos com
// `url.Values.Get` e, portanto, continuam com a ambiguidade descrita acima —
// o mesmo valendo para as rotas de `/transfers`, `/card-statements` e
// `/investments`. A correção pendente é adotar este helper neles; ela está no
// `docs/ROADMAP.md`, Fase 6 (Acabamento), com dono (`dev-backend-go`) e
// gatilho (a próxima entrega que tocar a borda HTTP de qualquer rota de
// leitura).
//
// O mecanismo mora aqui, e não no pacote da rota, de propósito: a próxima
// rota HERDA a regra em vez de reinventá-la, e a dívida acima fica escrita no
// lugar em que quem mexe na borda tropeça nela.
//
// # O que ele NÃO faz
//
// Não valida o conteúdo, não normaliza, não corta espaço e não decide o que
// o valor significa — isso é do parâmetro, na borda que o conhece. Ele
// responde a UMA pergunta: a chave é inequívoca?
func SoleQueryValue(q url.Values, chave string) (string, error) {
	valores := q[chave]
	switch len(valores) {
	case 0:
		return "", nil
	case 1:
		return valores[0], nil
	default:
		// O erro não carrega o nome da chave nem NENHUM dos valores: quem
		// chama sabe o campo, e o valor recusado é entrada bruta de terceiro,
		// que não entra em resposta nem em log (S8).
		return "", ErrRepeatedQueryParam
	}
}
