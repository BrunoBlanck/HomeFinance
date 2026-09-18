import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'

const fetchMock = vi.fn()

const SESSAO = {
  user: {
    id: '11111111-1111-4111-8111-111111111111',
    name: 'Bruno Blanck',
    email: 'bruno@example.com',
    emailVerifiedAt: '2026-09-09T12:00:00Z',
    createdAt: '2026-09-09T12:00:00Z',
  },
  household: {
    id: '22222222-2222-4222-8222-222222222222',
    name: 'Casa de Bruno',
    role: 'owner',
    timezone: 'America/Sao_Paulo',
    currency: 'BRL',
  },
  households: [
    {
      id: '22222222-2222-4222-8222-222222222222',
      name: 'Casa de Bruno',
      role: 'owner',
      timezone: 'America/Sao_Paulo',
      currency: 'BRL',
    },
  ],
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function renderApp() {
  const router = createAppRouter(createMemoryHistory({ initialEntries: ['/'] }))
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

describe('HomeScreen', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    document.title = ''
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('busca a sessão em /me com o cookie e saúda pelo primeiro nome', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, SESSAO))
    renderApp()

    expect(await screen.findByRole('heading', { name: 'Olá, Bruno.' })).toBeInTheDocument()

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/v1/me')
    expect(init.credentials).toBe('include')
  })

  it('anuncia o carregamento enquanto a sessão não chega', async () => {
    // Promessa que não resolve: congela a tela no estado pendente.
    fetchMock.mockReturnValue(new Promise(() => {}))
    renderApp()

    expect(await screen.findByText('Carregando sua conta')).toBeInTheDocument()
    // `aria-busy` na seção é o que faz o leitor de tela esperar em vez de ler
    // um conteúdo que ainda vai mudar.
    expect(screen.getByText('Carregando sua conta').closest('section')).toHaveAttribute(
      'aria-busy',
      'true',
    )
  })

  it('põe o foco no título ao abrir, para o teclado começar no conteúdo', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, SESSAO))
    renderApp()

    const titulo = await screen.findByRole('heading', { name: 'Olá, Bruno.' })
    await waitFor(() => expect(titulo).toHaveFocus())
  })

  it('define o título do documento', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, SESSAO))
    renderApp()

    await screen.findByRole('heading', { name: 'Olá, Bruno.' })
    expect(document.title).toBe('Início · HomeFinance')
  })

  it('mostra a casa e o menu da conta no cabeçalho', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, SESSAO))
    renderApp()

    expect(await screen.findByText('Casa de Bruno')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Conta de Bruno Blanck' })).toBeInTheDocument()
  })

  it('manda para o login quando a sessão venceu (401), sem mostrar erro', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { error: { code: 'UNAUTHENTICATED' } }))
    const router = renderApp()

    expect(await screen.findByRole('heading', { name: 'Entrar' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/entrar')
    // 401 é fim de sessão, não defeito: nada de alarme vermelho na saída.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('oferece "tentar de novo" quando a falha não é de sessão', async () => {
    fetchMock.mockResolvedValue(jsonResponse(500, { error: { code: 'INTERNAL_ERROR' } }))
    const router = renderApp()

    expect(await screen.findByText('Não foi possível carregar sua conta.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Tentar de novo' })).toBeInTheDocument()
    // Erro de servidor não desloga: o usuário continua onde estava.
    expect(router.state.location.pathname).toBe('/')
  })

  it('recarrega a sessão ao clicar em "tentar de novo"', async () => {
    const user = userEvent.setup()
    fetchMock.mockResolvedValueOnce(jsonResponse(500, { error: { code: 'INTERNAL_ERROR' } }))
    renderApp()

    await screen.findByRole('button', { name: 'Tentar de novo' })
    fetchMock.mockResolvedValue(jsonResponse(200, SESSAO))
    await user.click(screen.getByRole('button', { name: 'Tentar de novo' }))

    expect(await screen.findByRole('heading', { name: 'Olá, Bruno.' })).toBeInTheDocument()
  })

  it('é honesta sobre o que ainda não existe em vez de fingir dados', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, SESSAO))
    renderApp()

    await screen.findByRole('heading', { name: 'Olá, Bruno.' })
    expect(screen.getByText('Seu caderno está em branco.')).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Já funciona' })).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'A seguir' })).toBeInTheDocument()
    expect(screen.getByText('Lançamentos do mês')).toBeInTheDocument()
  })

  it('exibe o aviso que vem do fluxo anterior pelo state do roteador', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, SESSAO))
    const history = createMemoryHistory({ initialEntries: ['/'] })
    const router = createAppRouter(history)
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    })
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )
    await screen.findByRole('heading', { name: 'Olá, Bruno.' })

    await router.navigate({
      to: '/',
      state: { flash: { tone: 'success', message: 'Conta confirmada. Bem-vindo.' } },
    })

    expect(await screen.findByText('Conta confirmada. Bem-vindo.')).toBeInTheDocument()
  })
})
