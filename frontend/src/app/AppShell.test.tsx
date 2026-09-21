import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from './router'

const fetchMock = vi.fn()

function sessao(timezone = 'America/Sao_Paulo') {
  return {
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
      timezone,
      currency: 'BRL',
    },
    households: [],
  }
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

const VAZIO = {
  accounts: { items: [], totalBalanceCents: 0 },
  categories: { expense: [], income: [] },
  relatorio: { month: '2026-03', kind: 'expense', totalCents: 0, count: 0, items: [] },
}

function rotearApi(corpoDaSessao: unknown = sessao()) {
  fetchMock.mockImplementation((url: string) => {
    const caminho = String(url).replace('/api/v1', '').split('?')[0]
    if (caminho === '/me') return Promise.resolve(jsonResponse(200, corpoDaSessao))
    if (caminho === '/accounts') return Promise.resolve(jsonResponse(200, VAZIO.accounts))
    if (caminho === '/categories') return Promise.resolve(jsonResponse(200, VAZIO.categories))
    if (caminho === '/reports/by-category') {
      return Promise.resolve(jsonResponse(200, VAZIO.relatorio))
    }
    return Promise.resolve(jsonResponse(200, {}))
  })
}

function renderApp(entrada = '/') {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [entrada] }))
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

describe('AppShell', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
  })

  it('mostra a casa, o menu da conta e as seções que já existem', async () => {
    rotearApi()
    renderApp()

    expect(await screen.findByText('Casa de Bruno')).toBeInTheDocument()
    // `Menu de {nome}`, e não `Conta de {nome}`: abaixo de 52rem este menu
    // hospeda também o destino `/ia`, então ele deixou de ser só a conta
    // (spec 0010 §10.6).
    expect(screen.getByRole('button', { name: 'Menu de Bruno Blanck' })).toBeInTheDocument()

    const nav = screen.getByRole('navigation', { name: 'Seções do aplicativo' })
    expect(nav).toBeInTheDocument()
    // Lançamentos entrou com a entrega E2 — a rota existe e o item leva a ela.
    // Transferências entrou com a E2c (spec 0005); Relatórios, com a E6a
    // (ADR-027); Investimentos, com a E7 (spec 0006), DEPOIS de Transferências
    // — movimento antes de cadastro.
    const nomes = within(nav)
      .getAllByRole('link')
      .map((link) => link.getAttribute('aria-label'))
    expect(nomes).toEqual([
      'Painel',
      'Lançamentos',
      'Transferências',
      'Investimentos',
      'Relatórios',
      'Contas',
      'Categorias',
      // `IA` é o ÚLTIMO, e é o primeiro item do grupo "ferramentas" (spec 0010
      // §10.6): o que se usa de vez em quando vem depois do que se usa todo
      // dia, separado por um filete.
      'IA',
    ])

    // Item que não leva a lugar nenhum não é criado (docs/DESIGN.md): este só
    // aparece quando a tela dele existir.
    expect(screen.queryByRole('link', { name: 'Orçamentos' })).not.toBeInTheDocument()
  })

  /** A emenda da barra inferior (docs/DESIGN.md, 17/09/2026) em forma de teste.
   *
   *  O rótulo VISÍVEL carrega hífen suave para poder quebrar em duas linhas numa
   *  célula de 49px; o nome ACESSÍVEL não pode carregar nada disso, senão o
   *  leitor de tela e o comando de voz passam a divergir do que se lê na tela
   *  (WCAG 2.5.3). Os dois lados precisam ser afirmados juntos: consertar um
   *  quebrando o outro é exatamente o erro que este caso existe para pegar. */
  it('o rótulo visível quebra por hífen suave, o nome acessível é limpo', async () => {
    rotearApi()
    renderApp()

    await screen.findByText('Casa de Bruno')
    const nav = screen.getByRole('navigation', { name: 'Seções do aplicativo' })

    // Buscar pelo nome limpo continua funcionando — é o aria-label.
    const transferencias = within(nav).getByRole('link', { name: 'Transferências' })
    expect(transferencias.textContent).toContain('Trans\u00ADfe\u00ADrên\u00ADcias')

    // O 7º item (E7): hífen suave em TODAS as fronteiras silábicas — a maior
    // sílaba mede ~26px a --text-12, bem abaixo dos 47px da célula.
    const investimentos = within(nav).getByRole('link', { name: 'Investimentos' })
    expect(investimentos.textContent).toBe('In\u00ADves\u00ADti\u00ADmen\u00ADtos')

    for (const link of within(nav).getAllByRole('link')) {
      expect(link.getAttribute('aria-label')).not.toContain('\u00AD')
    }

    // Palavra que cabe inteira na célula não leva hífen nenhum.
    expect(within(nav).getByRole('link', { name: 'Painel' }).textContent).toBe('Painel')
    expect(within(nav).getByRole('link', { name: 'Contas' }).textContent).toBe('Contas')

    // Divisão que deixaria menos de 3 caracteres numa linha é proibida.
    expect(within(nav).getByRole('link', { name: 'Categorias' }).textContent).not.toContain(
      'ri\u00ADas',
    )
  })

  /** Guarda de regressão de GEOMETRIA, não de enfeite.
   *
   *  O reset do projeto zera margem e recuo só de `ul[role="list"]` — e a
   *  navegação apaga o marcador no CSS. Sem o papel explícito, a `<ul>` volta a
   *  ter os 15px de margem e os 40px de recuo do navegador: a barra passa de
   *  84,6px para 114,6px (30px de conteúdo escondido atrás dela, porque
   *  `--nav-bar-h` continua reservando 84,6px) e a célula cai de 58,2px para
   *  49px a 375px. O Biome chama o papel de redundante; ele não é. */
  it('a lista da navegação declara role="list" (o reset depende disso)', async () => {
    rotearApi()
    renderApp()

    await screen.findByText('Casa de Bruno')
    const nav = screen.getByRole('navigation', { name: 'Seções do aplicativo' })
    expect(within(nav).getByRole('list')).toHaveAttribute('role', 'list')
  })

  it('marca a seção corrente com aria-current', async () => {
    rotearApi()
    renderApp('/contas')

    await screen.findByText('Casa de Bruno')
    expect(screen.getByRole('link', { name: 'Contas' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('link', { name: 'Painel' })).not.toHaveAttribute('aria-current')
  })

  it('Relatórios abre o relatório por categoria com o mês, e fica marcado lá', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-09-15T12:00:00Z'))
    rotearApi()
    const user = userEvent.setup()
    const router = renderApp('/contas?mes=2026-03')

    await screen.findByText('Março de 2026')
    const relatorios = screen.getByRole('link', { name: 'Relatórios' })
    expect(relatorios).toHaveAttribute('href', '/relatorios/categorias?mes=2026-03')
    expect(relatorios).not.toHaveAttribute('aria-current')

    await user.click(relatorios)

    await waitFor(() => expect(router.state.location.pathname).toBe('/relatorios/categorias'))
    // O mês sobreviveu à troca de tela, e o item é sobre a SEÇÃO: continua
    // marcado depois de trocar o mês.
    expect(router.state.location.search).toEqual({ mes: '2026-03' })
    expect(screen.getByRole('link', { name: 'Relatórios' })).toHaveAttribute('aria-current', 'page')
    await user.click(screen.getByRole('button', { name: /Próximo mês/ }))
    await waitFor(() => expect(router.state.location.search).toEqual({ mes: '2026-04' }))
    expect(screen.getByRole('link', { name: 'Relatórios' })).toHaveAttribute('aria-current', 'page')
  })

  it('respeita o mês que veio na URL', async () => {
    rotearApi()
    renderApp('/contas?mes=2026-05')

    expect(await screen.findByText('Maio de 2026')).toBeInTheDocument()
  })

  // A URL é editável pela pessoa. Mês inválido não pode quebrar a tela nem
  // chegar a lugar nenhum — cai no corrente.
  it('mês inválido na URL cai no corrente, sem quebrar', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-09-15T12:00:00Z'))
    rotearApi()

    for (const ruim of ['banana', '2026-13', '<script>alert(1)</script>']) {
      const { unmount } = renderIsolado(`/contas?mes=${encodeURIComponent(ruim)}`)
      expect(await screen.findByText('Setembro de 2026')).toBeInTheDocument()
      unmount()
    }
  })

  it('navega entre meses e grava na URL', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-09-15T12:00:00Z'))
    rotearApi()
    const user = userEvent.setup()
    const router = renderApp('/contas')

    expect(await screen.findByText('Setembro de 2026')).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Mês anterior/ }))
    expect(await screen.findByText('Agosto de 2026')).toBeInTheDocument()
    await waitFor(() => expect(router.state.location.search).toEqual({ mes: '2026-08' }))

    await user.click(screen.getByRole('button', { name: /Próximo mês/ }))
    await user.click(screen.getByRole('button', { name: /Próximo mês/ }))
    expect(await screen.findByText('Outubro de 2026')).toBeInTheDocument()
  })

  // O mês é o eixo do app: trocar de tela não pode perdê-lo.
  it('o mês sobrevive à troca de tela', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-09-15T12:00:00Z'))
    rotearApi()
    const user = userEvent.setup()
    const router = renderApp('/contas?mes=2026-03')

    expect(await screen.findByText('Março de 2026')).toBeInTheDocument()

    await user.click(screen.getByRole('link', { name: 'Categorias' }))

    await waitFor(() => expect(router.state.location.pathname).toBe('/categorias'))
    expect(router.state.location.search).toEqual({ mes: '2026-03' })
    expect(screen.getByText('Março de 2026')).toBeInTheDocument()
  })

  it('oferece voltar ao mês atual só quando não se está nele', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-09-15T12:00:00Z'))
    rotearApi()
    const user = userEvent.setup()
    renderApp('/contas')

    await screen.findByText('Setembro de 2026')
    expect(screen.queryByRole('button', { name: 'Mês atual' })).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Mês anterior/ }))
    await screen.findByText('Agosto de 2026')

    await user.click(screen.getByRole('button', { name: 'Mês atual' }))
    expect(await screen.findByText('Setembro de 2026')).toBeInTheDocument()
  })

  /** O teste que justifica o fuso vir do servidor (ADR-019).
   *
   *  01/10/2026 00:30 UTC ainda é 30 de SETEMBRO em São Paulo. Uma casa em
   *  São Paulo precisa abrir em setembro; uma casa em Lisboa, em outubro. Se o
   *  mês saísse do fuso do navegador, as duas veriam a mesma coisa — e uma
   *  delas veria o mês errado. */
  it('o mês corrente é o da CASA, não o do navegador', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-10-01T00:30:00Z'))

    rotearApi(sessao('America/Sao_Paulo'))
    const primeiro = renderIsolado('/contas')
    expect(await screen.findByText('Setembro de 2026')).toBeInTheDocument()
    primeiro.unmount()

    rotearApi(sessao('Europe/Lisbon'))
    renderIsolado('/contas')
    expect(await screen.findByText('Outubro de 2026')).toBeInTheDocument()
  })

  it('fuso desconhecido não derruba a tela', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    vi.setSystemTime(new Date('2026-09-15T12:00:00Z'))
    rotearApi(sessao('Marte/Olympus_Mons'))
    renderIsolado('/contas')

    expect(await screen.findByText('Setembro de 2026')).toBeInTheDocument()
  })

  it('sessão vencida manda para a entrada', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { error: { code: 'UNAUTHENTICATED' } }))
    const router = renderApp('/contas')

    expect(await screen.findByRole('heading', { name: 'Entrar' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/entrar')
  })

  it('falha de sessão é reportada uma única vez, com saída', async () => {
    fetchMock.mockImplementation((url: string) => {
      const caminho = String(url).replace('/api/v1', '').split('?')[0]
      if (caminho === '/me') {
        return Promise.resolve(jsonResponse(500, { error: { code: 'INTERNAL_ERROR' } }))
      }
      return Promise.resolve(jsonResponse(200, VAZIO.accounts))
    })
    renderApp('/contas')

    // Um alerta só: a casca é quem depende da sessão, e duas mensagens para o
    // mesmo problema é ruído.
    const alertas = await screen.findAllByText('Não foi possível carregar sua conta.')
    expect(alertas).toHaveLength(1)
    expect(screen.getByRole('button', { name: 'Tentar de novo' })).toBeInTheDocument()
  })
})

/** Monta a aplicação e devolve o handle de desmontagem, para os testes que
 *  precisam renderizar duas vezes no mesmo caso. */
function renderIsolado(entrada: string) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [entrada] }))
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
}
