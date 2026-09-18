import { expect, type Page } from '@playwright/test'
import { esperarCodigo } from './caixa-de-entrada'

/** Cria uma conta de usuário NOVA e deixa a página autenticada.
 *
 *  Passa pelo fluxo real (cadastro → código de 6 dígitos → sessão) em vez de
 *  injetar um cookie: o cookie é `HttpOnly` e emitido pelo servidor, então
 *  forjá-lo exigiria conhecer o segredo de assinatura — e um atalho desses
 *  faria o E2E deixar de testar justamente a parte que mais importa. */
export const SENHA_PADRAO = 'uma frase bem longa de teste'

export type ContaDeTeste = {
  email: string
  nome: string
  primeiroNome: string
}

/** Endereço novo a cada chamada: o cadastro é único por e-mail, e reaproveitar
 *  faria a segunda execução bater num estado que a primeira deixou. */
export function emailNovo(prefixo: string): string {
  const carimbo = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`
  return `${prefixo}-${carimbo}@exemplo.test`
}

export async function entrarComContaNova(page: Page, nome = 'Bruno Blanck'): Promise<ContaDeTeste> {
  const email = emailNovo(nome.split(' ')[0]?.toLowerCase() ?? 'pessoa')
  const primeiroNome = nome.split(' ')[0] ?? nome

  await page.goto('/criar-conta')
  await expect(page.getByRole('heading', { name: 'Criar conta' })).toBeVisible()

  await page.getByLabel('Nome').fill(nome)
  await page.getByLabel('E-mail').fill(email)
  await page.getByLabel('Senha', { exact: true }).fill(SENHA_PADRAO)
  await page.getByRole('button', { name: 'Criar conta' }).click()

  await expect(page.getByRole('heading', { name: 'Confirme seu e-mail' })).toBeVisible()

  const codigo = await esperarCodigo(email)
  await page.getByLabel('Código de 6 dígitos').fill(codigo)

  await expect(page.getByRole('heading', { name: `Olá, ${primeiroNome}.` })).toBeVisible()

  return { email, nome, primeiroNome }
}
