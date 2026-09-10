import { queryOptions } from '@tanstack/react-query'
import { apiRequest } from '@/api/client'

export type SessionHousehold = {
  id: string
  name: string
  role: string
}

export type SessionUser = {
  id: string
  name: string
  email: string
  emailVerifiedAt: string | null
  createdAt: string
}

export type Session = {
  user: SessionUser
  household: SessionHousehold
  households: SessionHousehold[]
}

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
