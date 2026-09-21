import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
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

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

/** O painel pede o resumo do mês junto com a sessão (spec 0008). O menu não se
 *  importa com estes números — mas o app montado inteiro sim, e uma faixa
 *  quebrada no cabeçalho não pode falsear um teste de menu. */
const RESUMO_DO_MES: DashboardSummary = {
  month: '2026-09',
  incomeCents: 0,
  incomeCount: 0,
  creditCardExpenseCents: 0,
  creditCardExpenseCount: 0,
  investmentNetCents: 0,
  investmentCount: 0,
  creditCardAccountCount: 1,
  investmentCategoryCount: 1,
}

/** Uma resposta NOVA a cada chamada, roteada pelo caminho.
 *
 *  `mockResolvedValue(jsonResponse(...))` devolvia o MESMO `Response` a todas
 *  as chamadas, e o corpo de um `Response` só pode ser lido uma vez. Enquanto a
 *  Home fazia um pedido só, isso não aparecia; com o `/dashboard` do painel ao
 *  lado do `/me`, quem lesse em segundo lugar recebia corpo vazio — e o teste
 *  falhava longe da causa. */
function responderApi() {
  fetchMock.mockImplementation((entrada: string) => {
    const url = new URL(String(entrada), 'https://app.invalido')
    if (url.pathname.endsWith('/dashboard')) {
      return Promise.resolve(jsonResponse(200, RESUMO_DO_MES))
    }
    return Promise.resolve(jsonResponse(200, SESSAO))
  })
}

function callsTo(path: string): unknown[][] {
  return fetchMock.mock.calls.filter((call) => String(call[0]).endsWith(path))
}

/** O menu vive no cabeçalho da Home; montar o app inteiro é o que exercita o
 *  caminho real — inclusive a navegação de saída, que depende do roteador. */
async function renderMenuAberto() {
  responderApi()
  const router = createAppRouter(createMemoryHistory({ initialEntries: ['/'] }))
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )

  const user = userEvent.setup()
  const gatilho = await screen.findByRole('button', { name: 'Menu de Bruno Blanck' })
  await user.click(gatilho)
  return { user, router, queryClient, gatilho }
}

describe('UserMenu', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    document.documentElement.removeAttribute('data-theme')
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    document.documentElement.removeAttribute('data-theme')
  })

  it('nomeia o gatilho pela pessoa, não por "menu"', async () => {
    responderApi()
    const router = createAppRouter(createMemoryHistory({ initialEntries: ['/'] }))
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    // Um botão chamado "menu" não diz nada a quem navega por lista de botões.
    expect(await screen.findByRole('button', { name: 'Menu de Bruno Blanck' })).toBeInTheDocument()
  })

  it('mostra nome e e-mail ao abrir', async () => {
    await renderMenuAberto()

    expect(screen.getByText('Bruno Blanck')).toBeVisible()
    expect(screen.getByText('bruno@example.com')).toBeVisible()
  })

  it('usa rádios nativos para a aparência, com o atual marcado', async () => {
    await renderMenuAberto()

    const claro = screen.getByRole('radio', { name: 'Claro' })
    const escuro = screen.getByRole('radio', { name: 'Escuro' })
    const sistema = screen.getByRole('radio', { name: 'Sistema' })

    // Sem preferência gravada, o padrão é seguir o sistema.
    expect(sistema).toBeChecked()
    expect(claro).not.toBeChecked()
    expect(escuro).not.toBeChecked()
  })

  it('aplica e guarda o tema escuro ao escolher', async () => {
    const { user } = await renderMenuAberto()

    await user.click(screen.getByRole('radio', { name: 'Escuro' }))

    expect(document.documentElement).toHaveAttribute('data-theme', 'dark')
    expect(localStorage.getItem('hf.theme')).toBe('dark')
  })

  it('"Sistema" devolve a decisão ao prefers-color-scheme', async () => {
    const { user } = await renderMenuAberto()

    await user.click(screen.getByRole('radio', { name: 'Claro' }))
    expect(document.documentElement).toHaveAttribute('data-theme', 'light')

    await user.click(screen.getByRole('radio', { name: 'Sistema' }))
    // O atributo some: quem decide volta a ser o navegador, não um valor fixo.
    expect(document.documentElement).not.toHaveAttribute('data-theme')
    expect(localStorage.getItem('hf.theme')).toBe('system')
  })

  it('reabre já refletindo a preferência gravada', async () => {
    localStorage.setItem('hf.theme', 'dark')
    await renderMenuAberto()

    expect(screen.getByRole('radio', { name: 'Escuro' })).toBeChecked()
  })

  it('leva o foco para a opção de aparência marcada ao abrir', async () => {
    // A Popover API dá light dismiss, ESC e devolução do foco ao gatilho; levar
    // o foco para DENTRO ao abrir continua sendo responsabilidade do componente.
    await renderMenuAberto()

    await waitFor(() => expect(screen.getByRole('radio', { name: 'Sistema' })).toHaveFocus())
  })

  it('abre já no item escolhido, não no primeiro da lista', async () => {
    localStorage.setItem('hf.theme', 'dark')
    await renderMenuAberto()

    await waitFor(() => expect(screen.getByRole('radio', { name: 'Escuro' })).toHaveFocus())
  })

  it('fecha com Escape', async () => {
    const { user } = await renderMenuAberto()
    expect(screen.getByRole('button', { name: 'Sair' })).toBeVisible()

    await user.keyboard('{Escape}')

    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Sair' })).not.toBeInTheDocument(),
    )
  })

  it('fecha ao clicar fora', async () => {
    const { user } = await renderMenuAberto()
    expect(screen.getByRole('button', { name: 'Sair' })).toBeVisible()

    await user.click(await screen.findByRole('heading', { name: 'Olá, Bruno.' }))

    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Sair' })).not.toBeInTheDocument(),
    )
  })

  it('sai chamando POST /auth/logout e volta para a tela de entrada', async () => {
    const { user, router } = await renderMenuAberto()
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }))

    await user.click(screen.getByRole('button', { name: 'Sair' }))

    await waitFor(() => expect(callsTo('/auth/logout')).toHaveLength(1))
    const [, init] = callsTo('/auth/logout')[0] as [string, RequestInit]
    expect(init.method).toBe('POST')
    expect(init.credentials).toBe('include')

    expect(await screen.findByRole('heading', { name: 'Entrar' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/entrar')
  })

  it('sai mesmo se o servidor falhar — o estado local morre de qualquer jeito', async () => {
    const { user, router } = await renderMenuAberto()
    // O logout é idempotente no backend; ficar preso numa tela autenticada
    // porque a rede caiu seria pior do que sair.
    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'))

    await user.click(screen.getByRole('button', { name: 'Sair' }))

    expect(await screen.findByRole('heading', { name: 'Entrar' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/entrar')
  })

  it('limpa o cache de queries ao sair, para a sessão não sobreviver em memória', async () => {
    const { user, queryClient } = await renderMenuAberto()
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }))

    expect(queryClient.getQueryData(['session'])).toBeDefined()
    await user.click(screen.getByRole('button', { name: 'Sair' }))

    await waitFor(() => expect(queryClient.getQueryData(['session'])).toBeUndefined())
  })

  /** O destino `/ia` mora aqui porque a barra inferior do celular fecha em SETE
   *  células (docs/DESIGN.md, spec 0010 §10.6): ferramenta não ocupa célula.
   *
   *  A fronteira de 52rem é CSS e se verifica no Playwright, em navegador de
   *  verdade. O que se afirma aqui é o que o jsdom sabe responder: o link
   *  existe, leva o mês junto e fecha o menu ao ser clicado. */
  it('oferece o destino IA, com o mês da casca junto', async () => {
    await renderMenuAberto()

    // Escopado ao PAINEL: o item da lateral também se chama `IA`, e no jsdom
    // (que não aplica a media query) os dois estão na árvore ao mesmo tempo.
    const ia = within(painelDoMenu()).getByRole('link', { name: 'IA' })
    expect(ia).toHaveAttribute('href', expect.stringContaining('/ia'))
    expect(ia.getAttribute('href')).toContain('mes=')
  })

  /** `popover="auto"` fecha por light dismiss e por ESC, mas **não** por clique
   *  DENTRO do painel. Sem o `hidePopover()` à mão, o menu ficaria aberto por
   *  cima da tela recém-aberta — tapando justamente o `<h1>` que acabou de
   *  receber o foco. */
  it('clicar em IA navega e fecha o menu', async () => {
    const { user, router } = await renderMenuAberto()

    await user.click(within(painelDoMenu()).getByRole('link', { name: 'IA' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/ia'))
    // Fechado: o painel some da árvore acessível, como nos casos de ESC e de
    // clique fora acima.
    await waitFor(() =>
      expect(screen.queryByRole('button', { name: 'Sair' })).not.toBeInTheDocument(),
    )
  })
})

/** O painel flutuante do menu — o `[popover]` que contém o botão Sair. */
function painelDoMenu(): HTMLElement {
  const painel = screen.getByRole('button', { name: 'Sair' }).closest('[popover]')
  if (!painel) throw new Error('o painel do menu não está aberto')
  return painel as HTMLElement
}
