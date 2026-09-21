import { infiniteQueryOptions } from '@tanstack/react-query'
import { apiRequest } from '@/api/client'
import type {
  InvestmentDetectAlreadyCategorizedItem,
  InvestmentDetectItem,
  InvestmentDetectRequest,
  InvestmentDetectResult,
  InvestmentDetectUnmatchedItem,
  InvestmentDetectUnmatchedReason,
  InvestmentFlow,
  InvestmentItem,
  InvestmentOverview,
  InvestmentSeriesPoint,
  InvestmentTotals,
} from '@/api/types'

/** Chamadas de investimento (spec 0006 §3.4 e §3.3). Nenhum tipo de payload é
 *  escrito aqui: todos derivam do contrato OpenAPI (ADR-015). */

export type {
  InvestmentDetectAlreadyCategorizedItem,
  InvestmentDetectItem,
  InvestmentDetectRequest,
  InvestmentDetectResult,
  InvestmentDetectUnmatchedItem,
  InvestmentDetectUnmatchedReason,
  InvestmentFlow,
  InvestmentItem,
  InvestmentOverview,
  InvestmentSeriesPoint,
  InvestmentTotals,
}

/** O rótulo do rodapé promete um número ("Carregar mais 13"), e ele precisa ser
 *  o da requisição.
 *
 *  **Cinquenta**, a MESMA constante de `/lancamentos` e de `/transferencias`:
 *  uma lista do app tem um ritmo só de paginação, e densidade é princípio — quem
 *  abre o mês quer o mês inteiro, não um "carregar mais" inventado. Os números
 *  dos exemplos de copy de `docs/DESIGN.md` ilustram a FORMA da frase e nunca
 *  ditam esta constante. */
export const POR_PAGINA = 50

/** A chave mora sob o prefixo `transactions` DE PROPÓSITO (ADR-027).
 *
 *  Categorizar um lançamento em qualquer tela invalida `["transactions"]`, e é
 *  isso que faz os números desta tela serem relidos — um aporte marcado em
 *  `/lancamentos` aparece aqui sem ninguém pedir. Uma chave `["investments"]`
 *  própria ficaria velha em silêncio, que é o pior estado de um número. */
export const investmentsQueryKey = (mes: string) => ['transactions', 'investments', mes] as const

/** Visão do mês + ano + série de 12 meses + lista paginada por cursor.
 *
 *  `monthly`, `yearToDate` e `series` vêm no mesmo payload e são sobre o **mês
 *  inteiro**, não sobre a página: a tela lê sempre os da primeira página. Somar
 *  `items` no cliente para chegar ao total produziria um segundo número para a
 *  mesma coisa, e ele divergiria assim que houvesse uma segunda página. */
export function investmentsInfiniteQueryOptions(mes: string) {
  return infiniteQueryOptions({
    queryKey: investmentsQueryKey(mes),
    queryFn: ({ pageParam, signal }) => {
      // `URLSearchParams` e não concatenação: o mês vem da URL, que é editável
      // pela pessoa. O router já o validou — isto é a segunda camada.
      const busca = new URLSearchParams({ month: mes, limit: String(POR_PAGINA) })
      if (pageParam) busca.set('cursor', pageParam)
      return apiRequest<InvestmentOverview>(`/investments?${busca.toString()}`, { signal })
    },
    initialPageParam: null as string | null,
    getNextPageParam: (ultima) => ultima.nextCursor,
    retry: false,
  })
}

/** Detectar investimentos do mês (spec 0006 §3.3).
 *
 *  Aplica as palavras-chave das categorias de natureza `investment`/`redemption`
 *  aos lançamentos do mês. `dryRun: true` é a PRÉVIA: as três listas vêm
 *  preenchidas (teto de 500 cada; contagens completas). `dryRun: false` grava —
 *  e o servidor **recalcula** dentro de uma transação em vez de confiar na
 *  prévia, então `marked` é o número de linhas realmente afetadas, e é esse que
 *  o toast mostra.
 *
 *  `overwriteCategorized` é a primeira escrita do projeto autorizada a
 *  substituir escolha humana: **ausente é `false`, sempre**, e é a tela que
 *  precisa pedir a confirmação separada antes de mandá-lo como `true`.
 *
 *  É `POST` mesmo na prévia e mora fora do cache de queries de propósito: cada
 *  chamada gasta cota do rate limit por casa, e uma `useQuery` refazendo a
 *  prévia no foco da janela queimaria a cota em silêncio. O corpo não leva ids
 *  — o cliente não escolhe o que marcar. */
export function detectInvestments(body: InvestmentDetectRequest) {
  return apiRequest<InvestmentDetectResult>('/investments/detect', { method: 'POST', body })
}
