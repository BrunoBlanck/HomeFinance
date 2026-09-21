import { queryOptions } from '@tanstack/react-query'
import { apiRequest } from '@/api/client'
import type { Account, AccountList } from '@/api/types'

/** Chamadas de conta EXCLUSIVAS da tela de contas: listar, arquivar, excluir.
 *
 *  Criar e editar (e os rótulos e o schema do formulário) moram em
 *  `@/lib/accounts` e `@/lib/account-schema`, porque o `AccountDialog` virou
 *  componente compartilhado e a importação também cria conta. Arquivar e excluir
 *  ficam aqui: só `AccountsScreen` as usa. Nenhum tipo de payload é escrito à
 *  mão — todos derivam do contrato OpenAPI (ADR-015). */

export const accountsQueryKey = (includeArchived: boolean) =>
  ['accounts', { includeArchived }] as const

export function accountsQueryOptions(includeArchived: boolean) {
  return queryOptions({
    queryKey: accountsQueryKey(includeArchived),
    queryFn: ({ signal }) =>
      apiRequest<AccountList>(`/accounts?includeArchived=${includeArchived}`, { signal }),
    retry: false,
  })
}

export function archiveAccount(id: string) {
  return apiRequest<Account>(`/accounts/${encodeURIComponent(id)}/archive`, { method: 'POST' })
}

export function unarchiveAccount(id: string) {
  return apiRequest<Account>(`/accounts/${encodeURIComponent(id)}/unarchive`, { method: 'POST' })
}

export function deleteAccount(id: string) {
  return apiRequest<void>(`/accounts/${encodeURIComponent(id)}`, { method: 'DELETE' })
}
