import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DashboardSummary } from '@/api/types'
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

const RESUMO: DashboardSummary = {
  month: '2026-09',
  incomeCents: 500_000,
  incomeCount: 3,
  creditCardExpenseCents: 80_000,
  creditCardExpenseCount: 2,
  investmentNetCents: 165_000,
  investmentCount: 2,
  creditCardAccountCount: 1,
  investmentCategoryCount: 2,
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

/** Roteador da API do teste. `sessao` e `painel` são declarados à parte porque
 *  os casos interessantes desta tela são justamente aqueles em que um dos dois
 *  falha e o outro não. */
function rotearApi(
  sessao: () => Response,
  painel: () => Response = () => jsonResponse(200, RESUMO),
) {
  fetchMock.mockImplementation((entrada: string) => {
    const url = new URL(String(entrada), 'https://app.invalido')
    const caminho = url.pathname.replace('/api/v1', '')
    if (caminho === '/me') return Promise.resolve(sessao())
    if (caminho === '/dashboard') return Promise.resolve(painel())
    throw new Error(`rota não declarada no teste: ${url.pathname}${url.search}`)
  })
}

function comSessao() {
  rotearApi(() => jsonResponse(200, SESSAO))
}

function renderApp(caminho = '/?mes=2026-09') {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [caminho] }))
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
    comSessao()
    renderApp()

    expect(await screen.findByRole('heading', { name: 'Olá, Bruno.' })).toBeInTheDocument()

    // Pela ROTA, e não pela ordem: desde a spec 0008 a tela também pede o
    // resumo do mês, e as duas queries partem juntas.
    const chamada = fetchMock.mock.calls.find((c) => String(c[0]) === '/api/v1/me')
    expect(chamada).toBeDefined()
    const [, init] = chamada as [string, RequestInit]
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
    comSessao()
    renderApp()

    const titulo = await screen.findByRole('heading', { name: 'Olá, Bruno.' })
    await waitFor(() => expect(titulo).toHaveFocus())
  })

  /** É `Painel`, e não `Início`: é o nome do item de menu e o padrão das
   *  outras telas — a aba do navegador tem de dizer onde a pessoa está com a
   *  mesma palavra que a navegação usou. */
  it('define o título do documento', async () => {
    comSessao()
    renderApp()

    await screen.findByRole('heading', { name: 'Olá, Bruno.' })
    expect(document.title).toBe('Painel · HomeFinance')
  })

  it('mostra a casa e o menu da conta no cabeçalho', async () => {
    comSessao()
    renderApp()

    expect(await screen.findByText('Casa de Bruno')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Menu de Bruno Blanck' })).toBeInTheDocument()
  })

  it('manda para o login quando a sessão venceu (401), sem mostrar erro', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { error: { code: 'UNAUTHENTICATED' } }))
    const router = renderApp()

    expect(await screen.findByRole('heading', { name: 'Entrar' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/entrar')
    // 401 é fim de sessão, não defeito: nada de alarme vermelho na saída — nem
    // o da casca, nem o da faixa.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('oferece "tentar de novo" quando a falha não é de sessão', async () => {
    rotearApi(() => jsonResponse(500, { error: { code: 'INTERNAL_ERROR' } }))
    const router = renderApp()

    expect(await screen.findByText('Não foi possível carregar sua conta.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Tentar de novo' })).toBeInTheDocument()
    // Erro de servidor não desloga: o usuário continua onde estava.
    expect(router.state.location.pathname).toBe('/')
  })

  it('recarrega a sessão ao clicar em "tentar de novo"', async () => {
    const user = userEvent.setup()
    let falhar = true
    rotearApi(() =>
      falhar ? jsonResponse(500, { error: { code: 'INTERNAL_ERROR' } }) : jsonResponse(200, SESSAO),
    )
    renderApp()

    await screen.findByRole('button', { name: 'Tentar de novo' })
    falhar = false
    await user.click(screen.getByRole('button', { name: 'Tentar de novo' }))

    expect(await screen.findByRole('heading', { name: 'Olá, Bruno.' })).toBeInTheDocument()
  })

  /** O painel responde com NÚMEROS, e o placeholder foi apagado — não
   *  comentado, não escondido por CSS.
   *
   *  A tela acaba na faixa, e é para acabar: saldo por conta, vencimentos e top
   *  categorias são a E4 completa. Nenhuma moldura vazia prometendo o futuro. */
  it('mostra o resumo do mês, e nada de placeholder nem de "em breve"', async () => {
    comSessao()
    renderApp()

    expect(
      await screen.findByRole('heading', { level: 2, name: 'Resumo de setembro' }),
    ).toBeVisible()
    expect(screen.getByText('Receita do mês')).toBeVisible()

    expect(screen.queryByText('Seu caderno está em branco.')).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Onde o projeto está' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'Já funciona' })).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', { name: 'A seguir' })).not.toBeInTheDocument()
    expect(screen.queryByText(/em breve/i)).not.toBeInTheDocument()
  })

  it('exibe o aviso que vem do fluxo anterior pelo state do roteador', async () => {
    comSessao()
    const router = renderApp()
    await screen.findByRole('heading', { name: 'Olá, Bruno.' })

    await router.navigate({
      to: '/',
      state: { flash: { tone: 'success', message: 'Conta confirmada. Bem-vindo.' } },
    })

    expect(await screen.findByText('Conta confirmada. Bem-vindo.')).toBeInTheDocument()
  })
})
