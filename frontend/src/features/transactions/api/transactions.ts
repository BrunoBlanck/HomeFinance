import { infiniteQueryOptions } from '@tanstack/react-query'
import { apiRequest } from '@/api/client'
import type {
  AutoCategorizeRequest,
  AutoCategorizeResult,
  Transaction,
  TransactionKind,
  TransactionList,
  TransactionSummary,
  UpdateTransactionCategoryInput,
} from '@/api/types'

/** Chamadas de lançamento. Nenhum tipo de payload é escrito aqui: todos derivam
 *  do contrato OpenAPI (ADR-015). */

export type { Transaction, TransactionKind, TransactionList, TransactionSummary }

export type FiltroDeLancamentos = {
  /** `AAAA-MM`, obrigatório no contrato — não existe "o mês atual por padrão". */
  mes: string
  /** Conta da casa. Ausente = todas. */
  contaId?: string | undefined
}

/** Quantas linhas por página. É o default do contrato, escrito aqui porque o
 *  rótulo do botão o promete ("Carregar mais 50") e um número solto na tela que
 *  não bate com o da requisição é uma mentira pequena que ninguém depura. */
export const POR_PAGINA = 50

export const transactionsQueryKey = (filtro: FiltroDeLancamentos) =>
  ['transactions', { mes: filtro.mes, contaId: filtro.contaId ?? null }] as const

/** Listagem paginada por cursor.
 *
 *  O `summary` vem no MESMO payload da lista (decisão do contrato) e é sobre o
 *  **filtro inteiro**, não sobre a página carregada — por isso a tela lê sempre
 *  o da primeira página. Recontar centavos no cliente criaria uma segunda fonte
 *  para o mesmo número, e duas fontes divergem. */
export function transactionsInfiniteQueryOptions(filtro: FiltroDeLancamentos) {
  return infiniteQueryOptions({
    queryKey: transactionsQueryKey(filtro),
    queryFn: ({ pageParam, signal }) => {
      // `URLSearchParams` e não concatenação: o mês e a conta vêm da URL, que é
      // editável pela pessoa. O router já os valida antes de chegarem aqui —
      // isto é a segunda camada, e ela custa nada.
      const busca = new URLSearchParams({ month: filtro.mes, limit: String(POR_PAGINA) })
      if (filtro.contaId) busca.set('accountId', filtro.contaId)
      if (pageParam) busca.set('cursor', pageParam)
      return apiRequest<TransactionList>(`/transactions?${busca.toString()}`, { signal })
    },
    initialPageParam: null as string | null,
    getNextPageParam: (ultima) => ultima.nextCursor,
    retry: false,
  })
}

/** Exclusão lógica. Em transferência o backend apaga o **par inteiro**
 *  (ADR-016) — a tela precisa ter dito isso em texto antes de chamar. */
export function deleteTransaction(id: string) {
  return apiRequest<void>(`/transactions/${encodeURIComponent(id)}`, { method: 'DELETE' })
}

/** Categoriza UM lançamento (spec 0005 §11) — o atalho da célula
 *  "Sem categoria" em `/lancamentos`.
 *
 *  Devolve o `Transaction` atualizado, e é ele que entra no cache para a
 *  célula mudar no mesmo frame. O servidor recusa perna de transferência
 *  (422 `fields.id`), categoria arquivada ou de natureza trocada (422
 *  `fields.categoryId`) e categoria de outra casa (404, igual ao inexistente). */
export function updateTransactionCategory(id: string, input: UpdateTransactionCategoryInput) {
  return apiRequest<Transaction>(`/transactions/${encodeURIComponent(id)}`, {
    method: 'PATCH',
    body: input,
  })
}

/** O sinal do dinheiro e a neutralidade da transferência vivem em `lib/kind.ts`
 *  porque a revisão da importação aplica exatamente as mesmas regras — e uma
 *  segunda cópia delas acabaria divergindo. Reexportados aqui para a tela desta
 *  feature não precisar conhecer dois módulos. */
export { ehTransferencia, valorComSinal } from '@/lib/kind'

/** Categorização automática do mês (spec 0005 §4.3).
 *
 *  `dryRun: true` é a PRÉVIA: devolve quantos receberiam categoria e quais,
 *  sem gravar nada. `dryRun: false` grava — e o servidor **recalcula** em vez
 *  de confiar na prévia, então o número que volta é o de linhas realmente
 *  afetadas, e é esse que o toast mostra.
 *
 *  É `POST` mesmo na prévia e mora fora do cache de queries de propósito: cada
 *  chamada gasta uma cota do rate limit por casa (60/h), e uma `useQuery`
 *  refazendo a prévia no foco da janela queimaria a cota em silêncio. */
export function autoCategorize(body: AutoCategorizeRequest) {
  return apiRequest<AutoCategorizeResult>('/transactions/auto-categorize', {
    method: 'POST',
    body,
  })
}
