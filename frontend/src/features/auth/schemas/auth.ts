import { z } from 'zod'

/** Um schema por operação, compartilhado entre formulário e corpo da requisição.
 *  Validação aqui é conforto de digitação — a autoridade é sempre o backend. */

const emailField = z
  .string()
  .trim()
  .min(1, 'Informe seu e-mail.')
  .pipe(z.email('Esse e-mail não parece válido.'))

const newPasswordField = z
  .string()
  .min(1, 'Informe uma senha.')
  .min(12, 'A senha precisa de pelo menos 12 caracteres.')
  .max(128, 'A senha pode ter no máximo 128 caracteres.')

const codeField = z.string().regex(/^\d{6}$/, 'Digite os 6 dígitos do código.')

/** O `registrationToken` do ADR-014: 64 hexadecimais minúsculos, exatamente o
 *  que o backend emite. Não é campo de formulário — ninguém digita isto. Serve
 *  para conferir o que volta da API e o que sai do `sessionStorage` antes de
 *  virar corpo de requisição, porque nenhum dos dois é confiável por natureza. */
export const registrationTokenSchema = z.string().regex(/^[0-9a-f]{64}$/)

/** O que o cliente guarda entre as duas telas do cadastro: o token **e** o
 *  endereço que o pediu. O par existe porque o servidor procura a tentativa por
 *  `(email, hash do token)` — sem o endereço, a interface não tem como saber se
 *  o token que ela tem governa o endereço que ela vai enviar. */
export const storedRegistrationSchema = z.object({
  token: registrationTokenSchema,
  email: z.string().trim().min(3).max(254),
})

export const loginSchema = z.object({
  email: emailField,
  password: z.string().min(1, 'Informe sua senha.'),
})

export const registerSchema = z.object({
  name: z.string().trim().min(1, 'Informe seu nome.'),
  email: emailField,
  password: newPasswordField,
})

export const verifyEmailSchema = z.object({
  email: emailField,
  code: codeField,
})

export const forgotPasswordSchema = z.object({
  email: emailField,
})

export const resetPasswordSchema = z.object({
  email: emailField,
  code: codeField,
  newPassword: newPasswordField,
})

export type LoginValues = z.infer<typeof loginSchema>
export type RegisterValues = z.infer<typeof registerSchema>
export type VerifyEmailValues = z.infer<typeof verifyEmailSchema>
export type ForgotPasswordValues = z.infer<typeof forgotPasswordSchema>
export type ResetPasswordValues = z.infer<typeof resetPasswordSchema>
