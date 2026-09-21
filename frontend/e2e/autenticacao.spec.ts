import { expect, type Page, test } from '@playwright/test'
import { esperarCodigo } from './support/caixa-de-entrada'

/** Fluxo de autenticação de ponta a ponta, contra a API Go de verdade.
 *
 *  É o E2E que a E0 exige (DV4). Ele existe porque há coisas neste caminho que
 *  nenhum teste de componente alcança: o cookie `HttpOnly` ser emitido pelo
 *  servidor e devolvido pelo navegador, o `Origin` da requisição passar pela
 *  allowlist, e o código de 6 dígitos ser um segredo que só existe porque o
 *  servidor o gerou e mandou por e-mail. */

const SENHA = 'uma frase bem longa de teste'

/** Endereço novo a cada execução: o cadastro é único por e-mail, e reaproveitar
 *  faria a segunda corrida bater num estado que a primeira deixou. */
function emailNovo(prefixo: string): string {
  const carimbo = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`
  return `${prefixo}-${carimbo}@exemplo.test`
}

async function cadastrar(page: Page, nome: string, email: string): Promise<void> {
  await page.goto('/criar-conta')
  await expect(page.getByRole('heading', { name: 'Criar conta' })).toBeVisible()

  await page.getByLabel('Nome').fill(nome)
  await page.getByLabel('E-mail').fill(email)
  await page.getByLabel('Senha', { exact: true }).fill(SENHA)
  await page.getByRole('button', { name: 'Criar conta' }).click()

  await expect(page.getByRole('heading', { name: 'Confirme seu e-mail' })).toBeVisible()
}

async function confirmar(page: Page, email: string): Promise<void> {
  const codigo = await esperarCodigo(email)
  expect(codigo).toMatch(/^\d{6}$/)
  await page.getByLabel('Código de 6 dígitos').fill(codigo)
}

test.describe('autenticação', () => {
  test('cadastro com código de 6 dígitos, saída e entrada de novo', async ({ page }) => {
    const email = emailNovo('bruno')

    await cadastrar(page, 'Bruno Blanck', email)
    await confirmar(page, email)

    // Confirmar o código já cria a sessão: não há uma segunda tela de login.
    await expect(page.getByRole('heading', { name: 'Olá, Bruno.' })).toBeVisible()
    await expect(page).toHaveURL('/')

    // O cookie de sessão precisa existir e ser inacessível ao JavaScript — é a
    // defesa que sustenta todo o resto (ADR-013).
    const cookies = await page.context().cookies()
    expect(cookies.length).toBeGreaterThan(0)
    for (const cookie of cookies) {
      expect(cookie.httpOnly, `cookie ${cookie.name} deveria ser HttpOnly`).toBe(true)
      expect(cookie.sameSite, `cookie ${cookie.name} deveria ser SameSite=Strict`).toBe('Strict')
    }
    // E nada do que o servidor emitiu pode estar legível pela página.
    const visivelAoScript = await page.evaluate(() => document.cookie)
    expect(visivelAoScript).toBe('')

    // Sair.
    await page.getByRole('button', { name: 'Menu de Bruno Blanck' }).click()
    await page.getByRole('button', { name: 'Sair' }).click()
    await expect(page.getByRole('heading', { name: 'Entrar' })).toBeVisible()
    await expect(page).toHaveURL('/entrar')

    // Entrar de novo com a mesma senha — agora sem código, porque o e-mail já
    // está verificado.
    await page.getByLabel('E-mail').fill(email)
    await page.getByLabel('Senha', { exact: true }).fill(SENHA)
    await page.getByRole('button', { name: 'Entrar' }).click()

    await expect(page.getByRole('heading', { name: 'Olá, Bruno.' })).toBeVisible()
    await expect(page).toHaveURL('/')
  })

  test('senha errada não entra e não diz se a conta existe', async ({ page }) => {
    const email = emailNovo('ana')
    await cadastrar(page, 'Ana Souza', email)
    await confirmar(page, email)
    await expect(page.getByRole('heading', { name: 'Olá, Ana.' })).toBeVisible()

    await page.getByRole('button', { name: 'Menu de Ana Souza' }).click()
    await page.getByRole('button', { name: 'Sair' }).click()
    await expect(page.getByRole('heading', { name: 'Entrar' })).toBeVisible()

    await page.getByLabel('E-mail').fill(email)
    await page.getByLabel('Senha', { exact: true }).fill('senha completamente errada')
    await page.getByRole('button', { name: 'Entrar' }).click()

    const alerta = page.getByRole('alert')
    await expect(alerta).toHaveText('E-mail ou senha incorretos.')
    await expect(page).toHaveURL('/entrar')

    // A mesma frase para um endereço que nunca existiu: é isso que impede
    // descobrir quem tem conta testando e-mails.
    await page.getByLabel('E-mail').fill(emailNovo('ninguem'))
    await page.getByLabel('Senha', { exact: true }).fill(SENHA)
    await page.getByRole('button', { name: 'Entrar' }).click()
    await expect(page.getByRole('alert')).toHaveText('E-mail ou senha incorretos.')
  })

  test('código errado é recusado sem dizer o motivo', async ({ page }) => {
    const email = emailNovo('carla')
    await cadastrar(page, 'Carla Dias', email)

    // Espera o código real chegar (para não competir com o envio) e então usa
    // um diferente dele.
    const correto = await esperarCodigo(email)
    const errado = correto === '000000' ? '111111' : '000000'

    await page.getByLabel('Código de 6 dígitos').fill(errado)

    await expect(page.getByRole('alert')).toHaveText(
      'Código inválido ou expirado. Peça um novo código se precisar.',
    )
    await expect(page.getByRole('heading', { name: 'Confirme seu e-mail' })).toBeVisible()

    // O código certo, na sequência, continua valendo.
    await page.getByLabel('Código de 6 dígitos').fill(correto)
    await expect(page.getByRole('heading', { name: 'Olá, Carla.' })).toBeVisible()
  })

  test('rota autenticada sem sessão manda para a entrada', async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { name: 'Entrar' })).toBeVisible()
    await expect(page).toHaveURL('/entrar')
  })
})
