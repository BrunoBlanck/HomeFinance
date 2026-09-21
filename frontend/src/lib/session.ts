import { queryOptions } from '@tanstack/react-query'
import { apiRequest } from '@/api/client'
import type { Session, SessionHousehold, SessionUser } from '@/api/types'

/** Os tipos da sessão vêm do contrato OpenAPI (ADR-015) e são reexportados aqui
 *  porque este é o módulo que o resto do app conhece. Ganho concreto da
 *  derivação: `role` deixou de ser `string` e passou a ser `'owner' | 'member'`,
 *  que é o que a spec sempre disse. */
export type { Session, SessionHousehold, SessionUser }

export const sessionQueryKey = ['session'] as const

/** Estado de sessão compartilhado por toda a aplicação. Fica em `lib/` de
 *  propósito: se morasse numa feature, a Home teria de importar de `auth/`,
 *  e import entre features é proibido. */
export const sessionQueryOptions = queryOptions({
  queryKey: sessionQueryKey,
  queryFn: ({ signal }) => apiRequest<Session>('/me', { signal }),
  retry: false,
  staleTime: 30_000,
})

/** Encerrar a sessão é ciclo de vida da sessão, não de uma feature: mora aqui
 *  para que a Home possa sair sem importar nada de `features/auth`. */
export function logout() {
  return apiRequest<void>('/auth/logout', { method: 'POST' })
}
