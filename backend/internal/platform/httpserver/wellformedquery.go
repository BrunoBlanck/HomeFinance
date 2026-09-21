package httpserver

import (
	"net/http"
	"net/url"
)

// WellFormedQuery recusa com 400 toda requisição cuja QUERY STRING não seja
// parseável — e é o que torna `r.URL.Query()` confiável no resto do projeto.
//
// # O defeito que ele fecha
//
// `r.URL.Query()` chama `url.ParseQuery` e **descarta o erro**. Desde o Go
// 1.17 esse parser não aborta: ele PULA O PAR INTEIRO que não consegue ler e
// devolve o resto. Medido nesta base:
//
//	accountGroup=credit;debit               -> accountGroup=[]       (some)
//	accountGroup=credit%                    -> accountGroup=[]       (some)
//	accountGroup=cre%zzdit                  -> accountGroup=[]       (some)
//	accountGroup=credit&accountGroup=debit% -> accountGroup=[credit] (HPP não dispara)
//	account%Group=credit                    -> accountGroup=[]       (some)
//
// A classe NÃO é "ponto e vírgula": é "query malformada é engolida em
// silêncio". Por isso uma blocklist de `;` foi DESCARTADA — ela deixaria `%zz`
// e `%` solto abertos, que produzem exatamente o mesmo efeito.
//
// O efeito é a borda receber CHAVE AUSENTE onde o cliente mandou um valor, e
// "ausente" quase sempre significa "sem filtro". Isso quebra duas promessas
// escritas do ADR-032 — (a) "valor fora da allowlist é 400 na borda, nunca
// 'sem filtro'" e (c) a guarda de HTTP Parameter Pollution — e vale para toda
// rota que lê query: `?includeArchived=true;` vira `false` (a lista sai sem as
// arquivadas com a tela afirmando o contrário) e `?accountId=<uuid>;` vira
// "todas as contas da casa".
//
// # O que ele faz, e o que deliberadamente NÃO faz
//
// Faz UMA pergunta: `url.ParseQuery(r.URL.RawQuery)` devolveu erro? Se sim,
// 400 `VALIDATION_FAILED` com a mensagem genérica e **sem `fields`** — não há
// campo a apontar, porque o que está malformado é a query INTEIRA, e nomear um
// campo seria inventar qual deles o cliente quis dizer.
//
// A resposta não ecoa a query, não ecoa o valor recebido e **não ecoa o texto
// do erro da stdlib** — esse texto carrega o escape recebido (`invalid URL
// escape "%zz"`), e devolvê-lo seria devolver a entrada do atacante pela porta
// do erro (docs/SEGURANCA.md §4). Não há log próprio: o `AccessLog` já
// registra método, caminho e status, e query NUNCA entra em log (§6 — dado
// sensível não pode trafegar em query, e o log não é o lugar de descobrir que
// trafegou).
//
// Não valida conteúdo, não normaliza e não decide o que parâmetro nenhum
// significa: isso continua sendo da borda que conhece o parâmetro. Os handlers
// seguem chamando `r.URL.Query()` como sempre — o que muda é que a
// pré-condição "a query foi parseada sem erro" passa a ser GARANTIA DA CADEIA,
// e não uma esperança de cada handler.
//
// Query vazia passa direto, sem alocar: `ParseQuery("")` é sempre válida, e a
// imensa maioria das requisições não tem query.
//
// # Posição na cadeia (cmd/api/main.go)
//
// É o ÚLTIMO da lista, ou seja, o MAIS INTERNO — envolvendo o
// `ErrorShim(newMux(routes))`. Três razões:
//
//  1. a resposta sai por dentro de `SecurityHeaders` e `CORS`, que são
//     externos, então o navegador consegue LER o 400 em vez de tomar um erro
//     de CORS opaco;
//  2. fica DEPOIS do `CSRFGuard`: origem estranha continua barrada antes de
//     qualquer validação de entrada — a ordem "quem é você" antes de "o que
//     você mandou" não se inverte por uma checagem barata;
//  3. fica DEPOIS do `RateLimit`: uma enxurrada de query malformada continua
//     gastando balde. Checagem barata não pode virar rota grátis.
func WellFormedQuery() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.RawQuery != "" {
				if _, err := url.ParseQuery(r.URL.RawQuery); err != nil {
					// Sem `fields`, sem eco e sem log — ver o doc acima.
					WriteError(w, http.StatusBadRequest, CodeValidationFailed, MsgValidationFailed)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
