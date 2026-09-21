import { keepPreviousData, queryOptions } from '@tanstack/react-query'
import { apiRequest } from '@/api/client'
import type { DashboardSummary } from '@/api/types'

/** O resumo do mês do painel (spec 0008). Nenhum tipo de payload é escrito
 *  aqui: ele deriva do contrato OpenAPI (ADR-015). */

export type { DashboardSummary }

/** A chave mora sob o prefixo `transactions` **de propósito** (ADR-027).
 *
 *  Toda mutação que mexe em lançamento já invalida `['transactions']` — a
 *  importação, o atalho de categoria, a categorização automática, o
 *  reprocessamento de transferências e a exclusão. Pendurando o painel ali,
 *  estes três números se releem junto: marcar um aporte em `/lancamentos` muda
 *  o "Investido no mês" sem que aquela tela precise saber que o painel existe.
 *  Uma chave `['dashboard']` própria ficaria velha em silêncio — e o painel
 *  divergindo da tela de origem é o pior estado de um número. */
export const dashboardQueryKey = (mes: string) => ['transactions', 'dashboard', mes] as const

/** Os três números do mês, num pedido só.
 *
 *  `placeholderData: keepPreviousData` é o que faz trocar de mês nas setas da
 *  casca **não** piscar: o quadro anterior fica na tela (a 0,6 de opacidade e
 *  com `aria-busy`) enquanto o novo chega, e o `Skeleton` existe só na primeira
 *  carga. Zeros provisórios nunca aparecem — zero é um valor, e mostrá-lo antes
 *  da resposta é mentir (spec 0008 §3.2).
 *
 *  `retry: false` como no resto do app: 400 e 401 não melhoram na segunda
 *  tentativa, e o erro tem uma ação própria na tela. */
export function dashboardQueryOptions(mes: string) {
  return queryOptions({
    queryKey: dashboardQueryKey(mes),
    queryFn: ({ signal }) => {
      // `URLSearchParams` e não concatenação: o mês vem da URL, que a pessoa
      // edita. O `search.ts` já o validou — isto é a segunda camada, e custa
      // nada.
      const busca = new URLSearchParams({ month: mes })
      return apiRequest<DashboardSummary>(`/dashboard?${busca.toString()}`, { signal })
    },
    placeholderData: keepPreviousData,
    retry: false,
  })
}
