import { describe, expect, it } from 'vitest'
import { ApiError, NetworkError } from '@/api/client'
import { isUnauthenticated, messageForError } from './errors'

describe('messageForError', () => {
  it('traduz cada faixa de status para a redação do projeto', () => {
    expect(messageForError(new ApiError(400, 'VALIDATION_FAILED', ['email']))).toBe(
      'Confira os dados informados e tente de novo.',
    )
    expect(messageForError(new ApiError(422, null, []))).toBe(
      'Confira os dados informados e tente de novo.',
    )
    expect(messageForError(new ApiError(401, 'UNAUTHENTICATED', []))).toBe(
      'Sua sessão expirou. Entre de novo.',
    )
    expect(messageForError(new ApiError(403, 'FORBIDDEN', []))).toBe(
      'Você não tem acesso a este conteúdo.',
    )
    expect(messageForError(new ApiError(404, 'NOT_FOUND', []))).toBe('Não encontramos esta página.')
    expect(messageForError(new ApiError(429, 'RATE_LIMITED', []))).toBe(
      'Muitas tentativas. Aguarde alguns minutos e tente de novo.',
    )
    expect(messageForError(new ApiError(500, 'INTERNAL_ERROR', []))).toBe(
      'Algo falhou do nosso lado. Tente de novo em instantes.',
    )
    expect(messageForError(new NetworkError())).toBe(
      'Sem conexão com o servidor. Verifique sua internet.',
    )
  })

  it('nunca repassa texto cru: erro desconhecido cai na mensagem genérica', () => {
    expect(messageForError(new Error('database password is hunter2'))).toBe(
      'Algo falhou do nosso lado. Tente de novo em instantes.',
    )
  })

  it('reconhece sessão expirada', () => {
    expect(isUnauthenticated(new ApiError(401, 'UNAUTHENTICATED', []))).toBe(true)
    expect(isUnauthenticated(new ApiError(403, 'FORBIDDEN', []))).toBe(false)
    expect(isUnauthenticated(new NetworkError())).toBe(false)
  })
})
