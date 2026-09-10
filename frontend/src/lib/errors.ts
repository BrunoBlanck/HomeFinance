import { ApiError, NetworkError } from '@/api/client'

/** Mapa único de mensagens de erro. Toda tela começa por aqui e só sobrescreve
 *  o caso que precisa de redação própria (ex.: 401 do login). */
export function messageForError(error: unknown): string {
  if (error instanceof NetworkError) return 'Sem conexão com o servidor. Verifique sua internet.'

  if (error instanceof ApiError) {
    if (error.status === 400 || error.status === 422) {
      return 'Confira os dados informados e tente de novo.'
    }
    if (error.status === 401) return 'Sua sessão expirou. Entre de novo.'
    if (error.status === 403) return 'Você não tem acesso a este conteúdo.'
    if (error.status === 404) return 'Não encontramos esta página.'
    if (error.status === 429) return 'Muitas tentativas. Aguarde alguns minutos e tente de novo.'
  }

  return 'Algo falhou do nosso lado. Tente de novo em instantes.'
}

/** `true` quando a sessão acabou e a tela deve mandar o usuário para `/entrar`. */
export function isUnauthenticated(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401
}
