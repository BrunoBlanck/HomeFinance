import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { DashboardSummary } from '@/api/types'
import { createAppRouter } from '@/app/router'
import { formatarDinheiro } from '@/lib/money'

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
  households: [],
}

/** O mês do enunciado do aceite 1 da spec 0008: 3 receitas de R$ 5.000,
 *  2 despesas de cartão de R$ 800, um aporte de R$ 2.000 e um resgate de
 *  R$ 350 — líquido de R$ 1.650. */
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

function resumo(mudanca: Partial<DashboardSummary> = {}): DashboardSummary {
  return { ...RESUMO, ...mudanca }
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

type Painel = DashboardSummary | (() => Response)

/** O roteador da API do teste declara **duas** rotas: `/me` e `/dashboard`.
 *
 *  Não é economia — é a asserção do aceite 18 embutida no dublê: qualquer
 *  chamada a `/accounts` ou `/categories` para decidir estado vazio explodiria
 *  aqui, com o nome da rota que ninguém devia ter pedido. */
function rotearApi(painel: Painel = RESUMO) {
  fetchMock.mockImplementation((entrada: string) => {
    const url = new URL(String(entrada), 'https://app.invalido')
    const caminho = url.pathname.replace('/api/v1', '')
    if (caminho === '/me') return Promise.resolve(jsonResponse(200, SESSAO))
    if (caminho === '/dashboard') {
      return Promise.resolve(typeof painel === 'function' ? painel() : jsonResponse(200, painel))
    }
    throw new Error(`rota não declarada no teste: ${url.pathname}${url.search}`)
  })
}

function pedidosDoPainel(): URL[] {
  return fetchMock.mock.calls
    .map((chamada) => new URL(String(chamada[0]), 'https://app.invalido'))
    .filter((url) => url.pathname.endsWith('/dashboard'))
}

function renderPainel(caminho = '/?mes=2026-09') {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [caminho] }))
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { router, queryClient }
}

/** A linha da faixa cujo `<dt>` começa pelo rótulo. */
function linhaDe(rotulo: string): HTMLElement {
  const termo = screen.getByText(rotulo).closest('dt')
  if (!termo?.parentElement) throw new Error(`nenhuma linha com o rótulo ${rotulo}`)
  return termo.parentElement
}

/** O texto que os OLHOS veem — sem o `sr-only` do `MoneyText` e do traço. */
function visivel(elemento: Element): string {
  const copia = elemento.cloneNode(true) as HTMLElement
  for (const oculto of copia.querySelectorAll('.sr-only')) oculto.remove()
  return (copia.textContent ?? '').trim()
}

function valorDe(rotulo: string): string {
  const celula = linhaDe(rotulo).querySelector('dd')
  if (!celula) throw new Error(`a linha ${rotulo} não tem <dd>`)
  return visivel(celula)
}

/** O que o leitor de tela ouve na `<dd>` — é ali que mora o "negativos". */
function faladoEm(rotulo: string): string {
  const celula = linhaDe(rotulo).querySelector('dd .sr-only')
  return (celula?.textContent ?? '').trim()
}

/** A segunda linha do `<dt>`: a contagem e, quando há, a frase ou o link.
 *
 *  Lida peça a peça e juntada com espaço, e não por `textContent`: o respiro
 *  entre a contagem e o `·` é `gap` de flexbox, então no texto puro as duas
 *  palavras saem coladas. Quem lê a tela vê o espaço; quem lê o DOM, não. */
function apoioDe(rotulo: string): string {
  const apoio = linhaDe(rotulo).querySelector('dt > div')
  if (!apoio) return ''
  return [...apoio.children]
    .filter((peca) => !peca.classList.contains('sr-only'))
    .map((peca) => (peca.textContent ?? '').trim())
    .filter((texto) => texto !== '')
    .join(' ')
}

function faixa(): HTMLElement {
  return screen.getByRole('region', { name: /^Resumo de / })
}

/** Os rótulos, na ordem em que estão na `<dl>`. */
function rotulosEmOrdem(): string[] {
  return [...document.querySelectorAll('dl dt')].map(
    (termo) => termo.querySelector('span')?.textContent ?? '',
  )
}

async function esperarAFaixa() {
  expect(await screen.findByText('Receita do mês')).toBeInTheDocument()
  await waitFor(() => expect(faixa()).not.toHaveAttribute('aria-busy'))
}

describe('MonthSummaryBand', () => {
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

  it('monta os três números do mês, nesta ordem, com a contagem de cada um', async () => {
    rotearApi()
    renderPainel()
    await esperarAFaixa()

    // A ordem foi ratificada pelo usuário em 18/09/2026 e não é negociável.
    expect(rotulosEmOrdem()).toEqual([
      'Receita do mês',
      'Gasto no cartão de crédito',
      'Investido no mês',
    ])

    expect(valorDe('Receita do mês')).toBe(formatarDinheiro(500_000))
    expect(apoioDe('Receita do mês')).toBe('3 lançamentos')

    expect(valorDe('Gasto no cartão de crédito')).toBe(formatarDinheiro(80_000))
    expect(apoioDe('Gasto no cartão de crédito')).toBe('2 lançamentos')

    // Com sinal: o `+` é do `sign="always"`, e o número vem PRONTO do servidor.
    expect(valorDe('Investido no mês')).toBe(formatarDinheiro(165_000, { sinal: 'sempre' }))
    expect(apoioDe('Investido no mês')).toBe('2 lançamentos')

    // E o <h2> diz de que mês são os números — sem o ano, que está no seletor.
    expect(screen.getByRole('heading', { level: 2, name: 'Resumo de setembro' })).toBeVisible()
  })

  /** Aceite 18: **um** pedido de rede monta a faixa. */
  it('faz um único pedido, e nenhum a /accounts ou /categories', async () => {
    rotearApi()
    renderPainel()
    await esperarAFaixa()

    const pedidos = pedidosDoPainel()
    expect(pedidos).toHaveLength(1)
    expect(pedidos[0]?.searchParams.get('month')).toBe('2026-09')

    const caminhos = fetchMock.mock.calls.map((chamada) => String(chamada[0]))
    expect(caminhos.some((url) => url.includes('/accounts'))).toBe(false)
    expect(caminhos.some((url) => url.includes('/categories'))).toBe(false)
  })

  /** Aceite 2 + aceite 20: o negativo é dito por SINAL, por PALAVRA e pelo
   *  áudio — nunca só por cor.
   *
   *  ⚠️ O esperado é composto com `formatarDinheiro`, e nunca colado da spec: o
   *  `−` que o markdown escreve é U+2212, e o que o `Intl` emite é hífen-menos.
   *  Colar o glifo da spec faria o teste cobrar da tela uma normalização que
   *  ninguém deve escrever. */
  it('o líquido negativo aparece com sinal, com frase e com "negativos" no áudio', async () => {
    rotearApi(resumo({ investmentNetCents: -40_000, investmentCount: 2 }))
    renderPainel()
    await esperarAFaixa()

    expect(valorDe('Investido no mês')).toBe(formatarDinheiro(-40_000, { sinal: 'sempre' }))
    expect(valorDe('Investido no mês')).toContain('-')
    expect(apoioDe('Investido no mês')).toBe('2 lançamentos · os resgates superaram os aportes')
    expect(faladoEm('Investido no mês')).toBe(`${formatarDinheiro(40_000)} negativos`)
  })

  it('líquido zero COM movimento explica o empate, em vez de calar', async () => {
    rotearApi(resumo({ investmentNetCents: 0, investmentCount: 2 }))
    renderPainel()
    await esperarAFaixa()

    expect(valorDe('Investido no mês')).toBe(formatarDinheiro(0, { sinal: 'sempre' }))
    expect(apoioDe('Investido no mês')).toBe('2 lançamentos · aportes e resgates se anularam')
  })

  it('mês sem movimento mostra R$ 0,00 nas três linhas, e a faixa não some', async () => {
    rotearApi(
      resumo({
        incomeCents: 0,
        incomeCount: 0,
        creditCardExpenseCents: 0,
        creditCardExpenseCount: 0,
        investmentNetCents: 0,
        investmentCount: 0,
      }),
    )
    renderPainel()
    await esperarAFaixa()

    expect(valorDe('Receita do mês')).toBe(formatarDinheiro(0))
    expect(valorDe('Gasto no cartão de crédito')).toBe(formatarDinheiro(0))
    expect(valorDe('Investido no mês')).toBe(formatarDinheiro(0, { sinal: 'sempre' }))
    for (const rotulo of ['Receita do mês', 'Gasto no cartão de crédito', 'Investido no mês']) {
      expect(apoioDe(rotulo)).toBe('nenhum lançamento')
    }
    // Zero é um valor: nada de travessão aqui.
    expect(screen.queryByText('sem valor')).not.toBeInTheDocument()
  })

  it('casa sem cartão mostra o traço e o caminho para contas, nunca R$ 0,00', async () => {
    rotearApi(
      resumo({
        creditCardAccountCount: 0,
        creditCardExpenseCents: 0,
        creditCardExpenseCount: 0,
      }),
    )
    renderPainel()
    await esperarAFaixa()

    expect(valorDe('Gasto no cartão de crédito')).toBe('—')
    expect(faladoEm('Gasto no cartão de crédito')).toBe('sem valor')
    expect(apoioDe('Gasto no cartão de crédito')).toBe(
      'Nenhum cartão de crédito cadastrado · Ir para contas',
    )

    // Link, e não botão — e leva o mês da casca junto.
    const link = screen.getByRole('link', { name: 'Ir para contas' })
    expect(link).toHaveAttribute('href', '/contas?mes=2026-09')
  })

  /** ⚠️ A armadilha do cartão ARQUIVADO (direção do designer, §4).
   *
   *  `creditCardAccountCount` conta só cartão vivo, mas o gasto de um cartão
   *  arquivado continua contando — arquivar não apaga o passado. A condição
   *  ingênua (`count === 0 → traço`) esconderia dinheiro de verdade. */
  it('cartão arquivado com gasto no mês mostra o NÚMERO, não o traço', async () => {
    rotearApi(
      resumo({
        creditCardAccountCount: 0,
        creditCardExpenseCents: 80_000,
        creditCardExpenseCount: 2,
      }),
    )
    renderPainel()
    await esperarAFaixa()

    expect(valorDe('Gasto no cartão de crédito')).toBe(formatarDinheiro(80_000))
    expect(apoioDe('Gasto no cartão de crédito')).toBe('2 lançamentos')
    expect(screen.queryByRole('link', { name: 'Ir para contas' })).not.toBeInTheDocument()
  })

  it('casa sem categoria de investimento mostra o traço e o caminho para categorias', async () => {
    rotearApi(resumo({ investmentCategoryCount: 0, investmentNetCents: 0, investmentCount: 0 }))
    renderPainel()
    await esperarAFaixa()

    expect(valorDe('Investido no mês')).toBe('—')
    expect(faladoEm('Investido no mês')).toBe('sem valor')
    expect(apoioDe('Investido no mês')).toBe(
      'Nenhuma categoria de investimento ainda · Ir para categorias',
    )
    expect(screen.getByRole('link', { name: 'Ir para categorias' })).toHaveAttribute(
      'href',
      '/categorias?mes=2026-09',
    )
  })

  /** A mesma armadilha do outro lado: categoria de investimento ARQUIVADA
   *  continua marcando lançamento, e o contador não a conta. */
  it('investimento com contador zerado mas valor no mês mostra o NÚMERO', async () => {
    rotearApi(
      resumo({ investmentCategoryCount: 0, investmentNetCents: -40_000, investmentCount: 2 }),
    )
    renderPainel()
    await esperarAFaixa()

    expect(valorDe('Investido no mês')).toBe(formatarDinheiro(-40_000, { sinal: 'sempre' }))
    expect(screen.queryByRole('link', { name: 'Ir para categorias' })).not.toBeInTheDocument()
  })

  it('carregando não inventa zero nem contagem: esqueleto e aria-busy', async () => {
    // Promessa que não resolve no painel, mas a sessão chega: congela a faixa
    // na primeira carga.
    fetchMock.mockImplementation((entrada: string) => {
      const url = new URL(String(entrada), 'https://app.invalido')
      if (url.pathname.endsWith('/me')) return Promise.resolve(jsonResponse(200, SESSAO))
      return new Promise(() => {})
    })
    renderPainel()

    expect(await screen.findByText('Receita do mês')).toBeInTheDocument()
    expect(faixa()).toHaveAttribute('aria-busy', 'true')
    // Nenhum valor, nenhuma contagem — e nenhum travessão fingindo ausência.
    expect(valorDe('Receita do mês')).toBe('')
    expect(apoioDe('Receita do mês')).toBe('')
    expect(within(faixa()).queryByText(/lançamento/)).not.toBeInTheDocument()
    expect(screen.queryByText('sem valor')).not.toBeInTheDocument()
  })

  it('erro mostra o alerta no lugar do quadro, e "tentar de novo" recarrega', async () => {
    const user = userEvent.setup()
    let falhar = true
    rotearApi(() =>
      falhar ? jsonResponse(500, { error: { code: 'INTERNAL_ERROR' } }) : jsonResponse(200, RESUMO),
    )
    renderPainel()

    expect(await screen.findByText('Não foi possível carregar o resumo do mês.')).toBeVisible()
    // O quadro sai; o cabeçalho da tela continua.
    expect(screen.queryByText('Receita do mês')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1, name: 'Olá, Bruno.' })).toBeVisible()

    falhar = false
    await user.click(screen.getByRole('button', { name: 'Tentar de novo' }))

    await esperarAFaixa()
    expect(valorDe('Receita do mês')).toBe(formatarDinheiro(500_000))
  })

  it('sessão vencida (401) não vira alerta na faixa: quem redireciona é a casca', async () => {
    // A sessão vence para TUDO — é assim que ela vence de verdade.
    fetchMock.mockResolvedValue(jsonResponse(401, { error: { code: 'UNAUTHENTICATED' } }))
    renderPainel()

    expect(await screen.findByRole('heading', { level: 1, name: 'Entrar' })).toBeInTheDocument()
    expect(screen.queryByText('Não foi possível carregar o resumo do mês.')).not.toBeInTheDocument()
  })

  it('trocar de mês refaz o pedido sem piscar: o quadro anterior fica ocupado', async () => {
    const user = userEvent.setup()
    // Deferido criado ANTES do mock: a variável nunca é `null` no ponto de uso,
    // e o teste não depende de narrowing dentro de callback.
    let responderProximoMes!: (resposta: Response) => void
    const proximoMes = new Promise<Response>((resolver) => {
      responderProximoMes = resolver
    })

    rotearApi()
    renderPainel()
    await esperarAFaixa()

    // O mês seguinte demora: é nessa janela que se observa o "não pisca".
    fetchMock.mockImplementation((entrada: string) => {
      const url = new URL(String(entrada), 'https://app.invalido')
      if (url.pathname.endsWith('/me')) return Promise.resolve(jsonResponse(200, SESSAO))
      return proximoMes
    })

    await user.click(screen.getByRole('button', { name: /^Mês anterior/ }))

    await waitFor(() => expect(faixa()).toHaveAttribute('aria-busy', 'true'))
    // Os números do mês anterior CONTINUAM na tela — nada de esqueleto.
    expect(valorDe('Receita do mês')).toBe(formatarDinheiro(500_000))

    responderProximoMes(
      jsonResponse(200, resumo({ month: '2026-08', incomeCents: 100_000, incomeCount: 1 })),
    )
    await waitFor(() => expect(valorDe('Receita do mês')).toBe(formatarDinheiro(100_000)))
    expect(apoioDe('Receita do mês')).toBe('1 lançamento')
    expect(pedidosDoPainel().map((url) => url.searchParams.get('month'))).toEqual([
      '2026-09',
      '2026-08',
    ])
  })
})
