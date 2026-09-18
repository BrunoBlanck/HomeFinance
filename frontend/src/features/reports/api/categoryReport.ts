import { keepPreviousData, queryOptions } from '@tanstack/react-query'
import { apiRequest } from '@/api/client'
import type {
  CategoryKind,
  CategoryReport,
  CategoryReportChild,
  CategoryReportGroup,
} from '@/api/types'

/** Relatório por categoria (ADR-027). Nenhum tipo de payload é escrito aqui:
 *  todos derivam do contrato OpenAPI (ADR-015). */

export type { CategoryReport, CategoryReportChild, CategoryReportGroup }

export type FiltroDoRelatorio = {
  /** `AAAA-MM` — competência, como no resto do app (ADR-023c). */
  mes: string
  /** `expense` (padrão) ou `income`. Traduzido da `?natureza=` da URL. */
  kind: CategoryKind
}

/** A chave mora sob o prefixo `['transactions', …]` **de propósito** (ADR-027).
 *
 *  Toda mutação que mexe em lançamento já invalida `['transactions']` — a
 *  importação, o atalho de categoria, a categorização automática, o
 *  reprocessamento de transferências e a exclusão. Pendurando o relatório
 *  ali, ele se atualiza junto, sem que nenhuma dessas features precise saber
 *  que este relatório existe. Import entre features é proibido (AGENTS.md), e
 *  a alternativa — um prefixo próprio invalidado em seis lugares — viraria
 *  dívida no primeiro esquecimento. */
export const categoryReportQueryKey = (filtro: FiltroDoRelatorio) =>
  ['transactions', 'reports', 'by-category', { mes: filtro.mes, kind: filtro.kind }] as const

/** A query da tela `/relatorios/categorias`.
 *
 *  `keepPreviousData` é o que faz trocar de mês ou de natureza **não** piscar:
 *  com dado na tela, o quadro anterior fica visível (a 0,6 de opacidade e com
 *  `aria-busy`) enquanto o novo chega. Skeleton só na primeira carga
 *  (`isPending`) — é o anti-padrão "skeleton flash on refetch", e esta tela é
 *  justamente a que se navega com as setas do mês.
 *
 *  `retry: false` como no resto do app: 400 e 401 não melhoram na segunda
 *  tentativa, e o erro tem uma ação própria na tela. */
export function categoryReportQueryOptions(filtro: FiltroDoRelatorio) {
  return queryOptions({
    queryKey: categoryReportQueryKey(filtro),
    queryFn: ({ signal }) => {
      // `URLSearchParams`, nunca concatenação: mês e natureza vêm da URL, que
      // a pessoa edita. O `search.ts` já os validou por allowlist — isto é a
      // segunda camada, e ela custa nada.
      const busca = new URLSearchParams({ month: filtro.mes, kind: filtro.kind })
      return apiRequest<CategoryReport>(`/reports/by-category?${busca.toString()}`, { signal })
    },
    placeholderData: keepPreviousData,
    retry: false,
  })
}
