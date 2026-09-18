import { apiRequest } from '@/api/client'
import type {
  ForgotPasswordInput,
  LoginInput,
  RecoveryAccepted,
  RegisterInput,
  ResendCodeInput,
  ResetPasswordInput,
  Session,
  VerificationAccepted,
  VerifyEmailInput,
} from '@/api/types'

/** Chamadas de autenticação. Nenhum tipo de payload é escrito aqui: todos vêm
 *  de `@/api/types`, que os deriva de `backend/api/openapi.yaml` (ADR-015).
 *  Campo que a spec não tem não compila; campo que a spec ganhou aparece
 *  sozinho. */

export type { RecoveryAccepted, VerificationAccepted }

export function register(input: RegisterInput) {
  return apiRequest<VerificationAccepted>('/auth/register', { method: 'POST', body: input })
}

export function login(input: LoginInput) {
  return apiRequest<Session>('/auth/login', { method: 'POST', body: input })
}

/** O token é obrigatório: o backend procura o código DENTRO da tentativa que
 *  ele identifica. Sem o campo a resposta é 400 com `fields.registrationToken`. */
export function verifyEmail(input: VerifyEmailInput) {
  return apiRequest<Session>('/auth/verify-email', { method: 'POST', body: input })
}

/** O token é obrigatório aqui também. O caminho "reenvio sem token" foi
 *  removido do backend por ser explorável: quem pede reenvio não apresenta
 *  credenciais, então a tentativa sucessora herdava as de OUTRA pessoa — a
 *  mesma tomada de conta que o ADR-014 fechou, por outra porta. Sem token o
 *  202 continua idêntico (grupo B da §3.12), mas nada é emitido; por isso o
 *  tipo exige o campo: quem não tem token não chama esta rota, refaz o
 *  cadastro. */
export function resendCode(input: ResendCodeInput) {
  return apiRequest<VerificationAccepted>('/auth/resend-code', { method: 'POST', body: input })
}

export function forgotPassword(input: ForgotPasswordInput) {
  return apiRequest<RecoveryAccepted>('/auth/forgot-password', { method: 'POST', body: input })
}

export function resetPassword(input: ResetPasswordInput) {
  return apiRequest<void>('/auth/reset-password', { method: 'POST', body: input })
}
