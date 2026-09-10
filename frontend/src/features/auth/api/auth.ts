import { apiRequest } from '@/api/client'
import type { Session } from '@/lib/session'

/** Resposta 202 de `register` e de `resend-code` — idêntica para e-mail novo, já
 *  cadastrado ou inexistente. É o backend recusando-se a enumerar contas. */
export type VerificationAccepted = {
  status: string
  email: string
  expiresInSeconds: number
  /** ADR-014: amarra o código de 6 dígitos a ESTA tentativa de cadastro. Vem
   *  sempre com 64 hexadecimais, em todos os caminhos, justamente para não
   *  revelar qual deles o servidor tomou. Guardar é obrigação do cliente. */
  registrationToken: string
}

/** Resposta 202 de `forgot-password`. Corpo diferente de propósito: a
 *  redefinição de senha **não** usa `registrationToken` — o código dela é
 *  amarrado à conta, não a uma tentativa de cadastro. */
export type RecoveryAccepted = {
  status: string
  message: string
  expiresInSeconds: number
}

export function register(input: { name: string; email: string; password: string }) {
  return apiRequest<VerificationAccepted>('/auth/register', { method: 'POST', body: input })
}

export function login(input: { email: string; password: string }) {
  return apiRequest<Session>('/auth/login', { method: 'POST', body: input })
}

/** O token é obrigatório: o backend procura o código DENTRO da tentativa que
 *  ele identifica. Sem o campo a resposta é 400 com `fields.registrationToken`. */
export function verifyEmail(input: { email: string; code: string; registrationToken: string }) {
  return apiRequest<Session>('/auth/verify-email', { method: 'POST', body: input })
}

/** O token é obrigatório aqui também. O caminho "reenvio sem token" foi
 *  removido do backend por ser explorável: quem pede reenvio não apresenta
 *  credenciais, então a tentativa sucessora herdava as de OUTRA pessoa — a
 *  mesma tomada de conta que o ADR-014 fechou, por outra porta. Sem token o
 *  202 continua idêntico (grupo B da §3.12), mas nada é emitido; por isso o
 *  tipo exige o campo: quem não tem token não chama esta rota, refaz o
 *  cadastro. */
export function resendCode(input: { email: string; registrationToken: string }) {
  return apiRequest<VerificationAccepted>('/auth/resend-code', { method: 'POST', body: input })
}

export function forgotPassword(input: { email: string }) {
  return apiRequest<RecoveryAccepted>('/auth/forgot-password', { method: 'POST', body: input })
}

export function resetPassword(input: { email: string; code: string; newPassword: string }) {
  return apiRequest<void>('/auth/reset-password', { method: 'POST', body: input })
}
