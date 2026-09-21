import { infiniteQueryOptions } from '@tanstack/react-query'
import { apiRequest } from '@/api/client'
import type {
  TransferBalance,
  TransferDetectItem,
  TransferDetectRequest,
  TransferDetectResult,
  TransferDetectUnpairedItem,
  TransferDetectUnpairedReason,
  TransferItem,
  TransferList,
  TransferPair,
} from '@/api/types'

/** Chamadas de transferência (spec 0005 §4.4 e §13). Nenhum tipo de payload é
 *  escrito aqui: todos derivam do contrato OpenAPI (ADR-015). */

export type {
  TransferBalance,
  TransferDetectItem,
  TransferDetectRequest,
  TransferDetectResult,
  TransferDetectUnpairedItem,
  TransferDetectUnpairedReason,
  TransferItem,
  TransferList,
  TransferPair,
}

export type FiltroDeTransferencias = {
  /** `AAAA-MM`, obrigatório no contrato — competência, como em `/lancamentos`. */
  mes: string
  /** Conta da casa. Ausente = tudo o que mudou de conta no mês. */
  contaId?: string | undefined
  /** A outra conta do par. Só vai para a query junto de `contaId`: sozinha o
   *  contrato responde 400, e `search.ts` já a descarta antes de chegar aqui. */
  contraparteId?: string | undefined
}

/** O mesmo tamanho de página de `/lancamentos`: o rótulo "Carregar mais 50"
 *  promete um número, e ele precisa ser o da requisição. */
export const POR_PAGINA = 50

export const transfersQueryKey = (filtro: FiltroDeTransferencias) =>
  [
    'transfers',
    {
      mes: filtro.mes,
      contaId: filtro.contaId ?? null,
      contraparteId: filtro.contaId ? (filtro.contraparteId ?? null) : null,
    },
  ] as const

/** Listagem paginada por cursor — o MESMO cursor de `GET /transactions`.
 *
 *  `pairs` e `balances` vêm no mesmo payload e são sobre o **mês inteiro**, não
 *  sobre a página: a tela lê sempre os da primeira página. Somar `items` no
 *  cliente para chegar ao par produziria um segundo número para a mesma coisa,
 *  e ele divergiria assim que houvesse uma segunda página. */
export function transfersInfiniteQueryOptions(filtro: FiltroDeTransferencias) {
  return infiniteQueryOptions({
    queryKey: transfersQueryKey(filtro),
    queryFn: ({ pageParam, signal }) => {
      // `URLSearchParams` e não concatenação: mês e contas vêm da URL, que é
      // editável pela pessoa. O router já os validou — isto é a segunda camada.
      const busca = new URLSearchParams({ month: filtro.mes, limit: String(POR_PAGINA) })
      if (filtro.contaId) {
        busca.set('accountId', filtro.contaId)
        if (filtro.contraparteId) busca.set('counterpartAccountId', filtro.contraparteId)
      }
      if (pageParam) busca.set('cursor', pageParam)
      return apiRequest<TransferList>(`/transfers?${busca.toString()}`, { signal })
    },
    initialPageParam: null as string | null,
    getNextPageParam: (ultima) => ultima.nextCursor,
    retry: false,
  })
}

/** Reprocessar transferências do mês (spec 0005 §13, ADR-028).
 *
 *  Junta receita e despesa já gravadas que são o mesmo Pix entre contas da
 *  casa e as converte num par `transfer_out`/`transfer_in`. **Nada é criado**:
 *  candidata sem a outra perna gravada fica como está e volta em
 *  `unpairedItems` com `reason: 'no_mirror'`.
 *
 *  `dryRun: true` é a PRÉVIA: `paired` é quantos pares seriam convertidos e as
 *  listas vêm preenchidas (teto de 500 cada; contagens completas). `dryRun:
 *  false` converte — e o servidor **recalcula** dentro de uma transação em vez
 *  de confiar na prévia, então `paired` é o número de pares realmente
 *  convertidos, e é esse que o toast mostra. Se uma linha mudou entre a leitura
 *  e o `UPDATE`, nada é gravado e a resposta é 409 `CONFLICT`: a tela pede a
 *  prévia de novo.
 *
 *  É `POST` mesmo na prévia e mora fora do cache de queries de propósito: cada
 *  chamada gasta cota do rate limit por casa (60/h, balde próprio), e uma
 *  `useQuery` refazendo a prévia no foco da janela queimaria a cota em
 *  silêncio. O corpo não leva ids — o cliente não escolhe o que converter. */
export function detectTransfers(body: TransferDetectRequest) {
  return apiRequest<TransferDetectResult>('/transfers/detect', { method: 'POST', body })
}
