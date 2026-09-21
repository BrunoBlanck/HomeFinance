import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'

const fetchMock = vi.fn()

const EMAIL = 'bruno@example.com'

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function renderApp() {
  const router = createAppRouter(createMemoryHistory({ initialEntries: ['/esqueci-minha-senha'] }))
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

async function submit(email = EMAIL) {
  const user = userEvent.setup()
  await screen.findByRole('heading', { name: 'Esqueci minha senha' })
  await user.type(screen.getByLabelText('E-mail'), email)
  await user.click(screen.getByRole('button', { name: 'Enviar código' }))
  return user
}

const ACEITO = {
  status: 'accepted',
  message: 'Se houver conta, o código foi enviado.',
  expiresInSeconds: 900,
}

describe('ForgotPasswordScreen', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('valida o e-mail localmente antes de chamar a API', async () => {
    const user = userEvent.setup()
    renderApp()
    await screen.findByRole('heading', { name: 'Esqueci minha senha' })

    await user.click(screen.getByRole('button', { name: 'Enviar código' }))

    expect(await screen.findByText('Informe seu e-mail.')).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('recusa e-mail malformado sem gastar requisição', async () => {
    const user = userEvent.setup()
    renderApp()
    await screen.findByRole('heading', { name: 'Esqueci minha senha' })

    await user.type(screen.getByLabelText('E-mail'), 'bruno@')
    await user.click(screen.getByRole('button', { name: 'Enviar código' }))

    expect(await screen.findByText('Esse e-mail não parece válido.')).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('avança para a redefinição levando o e-mail pelo state, nunca pela URL', async () => {
    fetchMock.mockResolvedValue(jsonResponse(202, ACEITO))
    const router = renderApp()
    await submit()

    expect(await screen.findByRole('heading', { name: 'Criar uma senha nova' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/redefinir-senha')

    // O endereço é dado pessoal: não pode aparecer na URL, que vai para
    // histórico, referer e log de proxy.
    expect(router.state.location.href).not.toContain('@')
    expect(router.state.location.href).not.toContain(EMAIL)
  })

  it('a tela seguinte diz "se houver conta", sem confirmar que existe', async () => {
    fetchMock.mockResolvedValue(jsonResponse(202, ACEITO))
    renderApp()
    await submit()

    const aviso = await screen.findByText(
      `Se houver uma conta com ${EMAIL}, o código chega em instantes. Confira também a caixa de spam.`,
    )
    expect(aviso).toBeInTheDocument()
  })

  it('manda o e-mail no corpo e com o cookie de sessão', async () => {
    fetchMock.mockResolvedValue(jsonResponse(202, ACEITO))
    renderApp()
    await submit()

    await screen.findByRole('heading', { name: 'Criar uma senha nova' })
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/v1/auth/forgot-password')
    expect(init.method).toBe('POST')
    expect(init.credentials).toBe('include')
    expect(JSON.parse(String(init.body))).toEqual({ email: EMAIL })
  })

  it('mostra a mensagem de excesso de tentativas no 429 e não navega', async () => {
    fetchMock.mockResolvedValue(jsonResponse(429, { error: { code: 'RATE_LIMITED' } }))
    const router = renderApp()
    await submit()

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Muitas tentativas. Aguarde alguns minutos e tente de novo.',
    )
    expect(router.state.location.pathname).toBe('/esqueci-minha-senha')
  })

  it('trata falha de rede sem vazar detalhe do servidor', async () => {
    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'))
    renderApp()
    await submit()

    expect(await screen.findByRole('alert')).toHaveTextContent(
      'Sem conexão com o servidor. Verifique sua internet.',
    )
  })
})
