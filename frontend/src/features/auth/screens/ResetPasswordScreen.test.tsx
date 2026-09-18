import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'
import { clearResendCooldown } from '../hooks/useResendCooldown'

const fetchMock = vi.fn()

const EMAIL = 'bruno@example.com'
const SENHA_NOVA = 'uma frase bem longa'

/** A mesma frase para código errado, código expirado e conta inexistente. Uma
 *  variante por caso entregaria quais e-mails existem. */
const CODIGO_INVALIDO = 'Código inválido ou expirado. Peça um novo código se precisar.'

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function bodyOf(call: unknown[] | undefined): Record<string, unknown> {
  const init = call?.[1] as RequestInit | undefined
  return JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>
}

function callsTo(path: string): unknown[][] {
  return fetchMock.mock.calls.filter((call) => String(call[0]).endsWith(path))
}

/** Chega pelo fluxo normal (veio do "esqueci minha senha"): o endereço viaja no
 *  state do roteador, e a tela nem pede o campo de e-mail. */
function renderComEmail() {
  const history = createMemoryHistory({ initialEntries: ['/redefinir-senha'] })
  const router = createAppRouter(history)
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return router
}

/** Zera a espera do reenvio de forma determinística: adianta o relógio e força
 *  o tique de 1 s do contador, em vez de esperar o relógio real. */
async function pularEsperaDoReenvio() {
  vi.setSystemTime(Date.now() + 61_000)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1100)
  })
}

describe('ResetPasswordScreen', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    clearResendCooldown()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  /** Sem o e-mail no state (link colado, aba recarregada), a tela precisa
   *  continuar utilizável — pedindo o endereço em vez de virar um beco. */
  async function preencherEEnviar(user: ReturnType<typeof userEvent.setup>, comEmail: boolean) {
    if (comEmail) {
      await user.type(screen.getByLabelText('E-mail'), EMAIL)
    }
    await user.type(screen.getByLabelText('Código de 6 dígitos'), '123456')
    await user.type(screen.getByLabelText('Nova senha'), SENHA_NOVA)
    await user.click(screen.getByRole('button', { name: 'Redefinir senha' }))
  }

  it('pede o e-mail quando a tela é aberta direto, sem vir do passo anterior', async () => {
    const user = userEvent.setup()
    renderComEmail()
    await screen.findByRole('heading', { name: 'Criar uma senha nova' })

    expect(screen.getByLabelText('E-mail')).toBeInTheDocument()
    expect(
      screen.getByText(
        'Informe o e-mail que recebeu o código, digite o código e escolha a senha nova. O código vale por 15 minutos.',
      ),
    ).toBeInTheDocument()

    fetchMock.mockResolvedValue(new Response(null, { status: 204 }))
    await preencherEEnviar(user, true)

    await waitFor(() => expect(callsTo('/auth/reset-password')).toHaveLength(1))
    expect(bodyOf(callsTo('/auth/reset-password')[0])).toEqual({
      email: EMAIL,
      code: '123456',
      newPassword: SENHA_NOVA,
    })
  })

  it('leva para o login com aviso de sucesso e sem deixar voltar para o formulário', async () => {
    const user = userEvent.setup()
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }))
    const router = renderComEmail()
    await screen.findByRole('heading', { name: 'Criar uma senha nova' })

    await preencherEEnviar(user, true)

    expect(await screen.findByRole('heading', { name: 'Entrar' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/entrar')
    expect(screen.getByText('Senha redefinida. Entre com a senha nova.')).toBeInTheDocument()
    expect(
      screen.getByText('Por segurança, encerramos as sessões abertas nos outros dispositivos.'),
    ).toBeInTheDocument()
  })

  it('usa a mesma frase para código errado e para conta inexistente', async () => {
    const user = userEvent.setup()
    fetchMock.mockResolvedValue(
      jsonResponse(400, { error: { code: 'INVALID_CODE', message: 'seja lá o que for' } }),
    )
    renderComEmail()
    await screen.findByRole('heading', { name: 'Criar uma senha nova' })

    await preencherEEnviar(user, true)

    const alerta = await screen.findByRole('alert')
    expect(alerta).toHaveTextContent(CODIGO_INVALIDO)
    // A mensagem do servidor nunca é repassada: ela poderia distinguir os casos.
    expect(alerta).not.toHaveTextContent('seja lá o que for')
  })

  it('devolve o foco ao campo de código quando o código é recusado', async () => {
    const user = userEvent.setup()
    fetchMock.mockResolvedValue(jsonResponse(400, { error: { code: 'INVALID_CODE' } }))
    renderComEmail()
    await screen.findByRole('heading', { name: 'Criar uma senha nova' })

    await preencherEEnviar(user, true)

    await screen.findByRole('alert')
    expect(screen.getByLabelText('Código de 6 dígitos')).toHaveFocus()
  })

  it('recusa código com menos de 6 dígitos sem chamar a API', async () => {
    const user = userEvent.setup()
    renderComEmail()
    await screen.findByRole('heading', { name: 'Criar uma senha nova' })

    await user.type(screen.getByLabelText('E-mail'), EMAIL)
    await user.type(screen.getByLabelText('Código de 6 dígitos'), '123')
    await user.type(screen.getByLabelText('Nova senha'), SENHA_NOVA)
    await user.click(screen.getByRole('button', { name: 'Redefinir senha' }))

    expect(await screen.findByText('Digite os 6 dígitos do código.')).toBeInTheDocument()
    expect(callsTo('/auth/reset-password')).toHaveLength(0)
  })

  it('exige senha de pelo menos 12 caracteres antes de chamar a API', async () => {
    const user = userEvent.setup()
    renderComEmail()
    await screen.findByRole('heading', { name: 'Criar uma senha nova' })

    await user.type(screen.getByLabelText('E-mail'), EMAIL)
    await user.type(screen.getByLabelText('Código de 6 dígitos'), '123456')
    await user.type(screen.getByLabelText('Nova senha'), 'curta')
    await user.click(screen.getByRole('button', { name: 'Redefinir senha' }))

    expect(
      await screen.findByText('A senha precisa de pelo menos 12 caracteres.'),
    ).toBeInTheDocument()
    expect(callsTo('/auth/reset-password')).toHaveLength(0)
  })

  it('reenvia o código pelo forgot-password e limpa o campo', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const user = userEvent.setup()
    fetchMock.mockResolvedValue(
      jsonResponse(202, { status: 'accepted', message: 'ok', expiresInSeconds: 900 }),
    )
    renderComEmail()
    await screen.findByRole('heading', { name: 'Criar uma senha nova' })

    await user.type(screen.getByLabelText('E-mail'), EMAIL)
    await user.type(screen.getByLabelText('Código de 6 dígitos'), '123456')
    await pularEsperaDoReenvio()

    await user.click(screen.getByRole('button', { name: 'Enviar outro código' }))

    expect(
      await screen.findByText('Se o e-mail estiver cadastrado, enviamos um novo código.'),
    ).toBeInTheDocument()
    expect(callsTo('/auth/forgot-password')).toHaveLength(1)
    expect(bodyOf(callsTo('/auth/forgot-password')[0])).toEqual({ email: EMAIL })
    // O código velho já não vale: deixá-lo na tela convida a reenviar o mesmo.
    expect(screen.getByLabelText('Código de 6 dígitos')).toHaveValue('')
  })

  it('explica o 429 do reenvio sem confundir com código inválido', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    const user = userEvent.setup()
    fetchMock.mockResolvedValue(jsonResponse(429, { error: { code: 'RATE_LIMITED' } }))
    renderComEmail()
    await screen.findByRole('heading', { name: 'Criar uma senha nova' })

    await user.type(screen.getByLabelText('E-mail'), EMAIL)
    await pularEsperaDoReenvio()
    await user.click(screen.getByRole('button', { name: 'Enviar outro código' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Você pediu códigos demais. Aguarde alguns minutos antes de tentar de novo.',
    )
  })

  it('não deixa o código nem a senha aparecerem na URL', async () => {
    const user = userEvent.setup()
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }))
    const router = renderComEmail()
    await screen.findByRole('heading', { name: 'Criar uma senha nova' })

    await preencherEEnviar(user, true)
    await screen.findByRole('heading', { name: 'Entrar' })

    expect(router.state.location.href).not.toContain('123456')
    expect(router.state.location.href).not.toContain(SENHA_NOVA)
    expect(router.state.location.href).not.toContain('@')
  })
})
