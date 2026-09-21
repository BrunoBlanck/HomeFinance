import { infiniteQueryOptions, queryOptions } from '@tanstack/react-query'
import { apiRequest } from '@/api/client'
import type {
  ConfirmImportInput,
  ImportBatch,
  ImportDecisionAction,
  ImportPreview,
  ImportResult,
  ImportRow,
} from '@/api/types'

/** Chamadas da importação. Nenhum tipo de payload é escrito aqui: todos derivam
 *  do contrato OpenAPI (ADR-015). */

export type { ConfirmImportInput, ImportBatch, ImportDecisionAction, ImportPreview, ImportRow }

/** Página da revisão. O teto do contrato é 200; usar o teto reduz o número de
 *  idas ao servidor num extrato grande, e a revisão precisa das linhas TODAS
 *  para montar os três blocos e contar o que vai entrar. */
export const LINHAS_POR_PAGINA = 200

type EntradaDaImportacao = {
  file: File
  accountId: string
  /** Senha do ZIP. Vive só o tempo da requisição — ver o aviso abaixo. */
  password?: string | undefined
  /** Desempate manual, só depois de `IMPORT_FORMAT_AMBIGUOUS`. */
  format?: string | undefined
}

/** Envia o arquivo e ANALISA. **Nada é gravado aqui** — o resultado vive num
 *  *staging* de 24 h, e só `confirmarImportacao` cria lançamento.
 *
 *  Duas regras que este corpo cumpre e que são fáceis de quebrar:
 *
 *  1. **`FormData` sem `Content-Type` nosso.** O cabeçalho de um multipart
 *     carrega o `boundary`, e só o navegador sabe qual é — ele o gera ao
 *     serializar, e só se o header ainda estiver vazio. Definir o tipo à mão
 *     faz o servidor receber um corpo que não consegue separar em partes: 400
 *     sem pista nenhuma. O `api/client.ts` já trata isso; o cuidado aqui é não
 *     passar `body` como objeto.
 *  2. **No máximo quatro partes, e só as que existem.** Parte com nome fora da
 *     allowlist é 400, e mandar `password` vazia num arquivo sem senha é mandar
 *     uma parte a mais sem necessidade.
 *
 *  A **senha nunca é guardada**: não vai para `sessionStorage`, não vai para
 *  `localStorage`, não vai para a query string e não entra em nenhum cache do
 *  React Query — por isso esta função é uma chamada solta, e não uma query. */
export function criarImportacao(entrada: EntradaDaImportacao) {
  const corpo = new FormData()
  corpo.append('file', entrada.file)
  corpo.append('accountId', entrada.accountId)
  if (entrada.password) corpo.append('password', entrada.password)
  if (entrada.format) corpo.append('format', entrada.format)

  return apiRequest<ImportBatch>('/imports', { method: 'POST', body: corpo })
}

export const importacaoQueryKey = (id: string) => ['imports', id] as const

/** As linhas da revisão, paginadas pelo `seq`.
 *
 *  `staleTime: Infinity` e zero refetch automático: o *staging* é imutável
 *  entre o envio e a confirmação, e uma revalidação em foco no meio de uma
 *  revisão de 68 linhas trocaria o dado embaixo das decisões que a pessoa já
 *  tomou. */
export function linhasDaImportacaoQueryOptions(id: string) {
  return infiniteQueryOptions({
    queryKey: [...importacaoQueryKey(id), 'linhas'] as const,
    queryFn: ({ pageParam, signal }) => {
      const busca = new URLSearchParams({ limit: String(LINHAS_POR_PAGINA) })
      if (pageParam !== null) busca.set('cursor', String(pageParam))
      return apiRequest<ImportPreview>(`/imports/${encodeURIComponent(id)}?${busca.toString()}`, {
        signal,
      })
    },
    initialPageParam: null as number | null,
    getNextPageParam: (ultima) => ultima.nextCursor,
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
    retry: false,
  })
}

/** Só o lote, para a tela de resultado.
 *
 *  `limit=1` porque depois do commit as linhas de *staging* já foram apagadas
 *  fisicamente: o que sobra e importa é o `outcome` do lote. Pedir 200 linhas
 *  que não existem mais seria trabalho à toa dos dois lados. */
export function loteDaImportacaoQueryOptions(id: string) {
  return queryOptions({
    queryKey: [...importacaoQueryKey(id), 'lote'] as const,
    queryFn: ({ signal }) =>
      apiRequest<ImportPreview>(`/imports/${encodeURIComponent(id)}?limit=1`, { signal }),
    retry: false,
  })
}

/** Confirma o lote e grava. Idempotente no servidor: dois cliques em conexão
 *  ruim produzem UM conjunto de lançamentos e a mesma resposta. */
export function confirmarImportacao(id: string, corpo: ConfirmImportInput) {
  return apiRequest<ImportResult>(`/imports/${encodeURIComponent(id)}/confirm`, {
    method: 'POST',
    body: corpo,
  })
}

/** Descarta o lote e apaga as linhas de *staging* fisicamente.
 *
 *  É o que honra a promessa escrita no rodapé do passo 1 ("o arquivo fica no
 *  servidor só até você concluir ou cancelar"). Cancelar sem chamar isto
 *  deixaria dado financeiro parado numa tabela por 24 h — e a interface teria
 *  mentido. */
export function descartarImportacao(id: string) {
  return apiRequest<void>(`/imports/${encodeURIComponent(id)}`, { method: 'DELETE' })
}
