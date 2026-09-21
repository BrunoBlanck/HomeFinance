import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'
import { formatarDinheiro } from '@/lib/money'
import { captionDaSerie, colunasDaSerie, maximoDaSerie, serieVazia } from './InvestmentsScreen'

const fetchMock = vi.fn()

const NUBANK = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'
const C6 = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d66'
const CDB = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8daa'

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

function categoria(id: string, name: string) {
  return {
    id,
    parentId: null,
    name,
    kind: 'investment',
    keywords: [],
    archivedAt: null,
    children: [],
  }
}

/** A casa tem o grupo "Investimentos" da semente. */
const CATEGORIAS = {
  expense: [],
  income: [],
  investment: [categoria(CDB, 'Investimentos')],
  redemption: [],
}

/** Nenhuma categoria de investimento nem de resgate: o vazio de (e.1). */
const CATEGORIAS_VAZIAS = { expense: [], income: [], investment: [], redemption: [] }

/** Doze meses terminando em setembro de 2026, com movimento em setembro e um
 *  aporte em dezembro de 2025 — a virada de ano cai entre eles. */
function serie() {
  return serieVazia('2026-09').map((ponto) => {
    if (ponto.month === '2026-09') {
      return { month: ponto.month, contributionsCents: 200_000, redemptionsCents: 85_000 }
    }
    if (ponto.month === '2025-12') {
      return { month: ponto.month, contributionsCents: 150_000, redemptionsCents: 0 }
    }
    return ponto
  })
}

function item(over: Record<string, unknown> = {}) {
  return {
    id: 'i-1',
    occurredOn: '2026-09-05',
    flow: 'contribution',
    accountId: NUBANK,
    accountName: 'Nubank',
    categoryId: CDB,
    categoryName: 'CDB',
    amountCents: 200_000,
    description: 'CDB 15 DIAS',
    source: 'import',
    ...over,
  }
}

const VISAO = {
  month: '2026-09',
  monthly: {
    contributionsCents: 200_000,
    contributionCount: 3,
    redemptionsCents: 85_000,
    redemptionCount: 1,
  },
  yearToDate: {
    contributionsCents: 1_840_000,
    contributionCount: 22,
    redemptionsCents: 320_000,
    redemptionCount: 4,
  },
  series: serie(),
  items: [
    item(),
    item({
      id: 'i-2',
      occurredOn: '2026-09-12',
      flow: 'redemption',
      accountId: C6,
      accountName: 'C6',
      amountCents: 85_000,
      description: 'RESGATE CDB',
    }),
  ],
  nextCursor: null,
}

const VAZIA = {
  month: '2026-09',
  monthly: {
    contributionsCents: 0,
    contributionCount: 0,
    redemptionsCents: 0,
    redemptionCount: 0,
  },
  yearToDate: {
    contributionsCents: 0,
    contributionCount: 0,
    redemptionsCents: 0,
    redemptionCount: 0,
  },
  series: serieVazia('2026-09'),
  items: [],
  nextCursor: null,
}

const PREVIA_VAZIA = {
  month: '2026-09',
  marked: 0,
  unmatched: 0,
  alreadyCategorized: 0,
  items: [],
  unmatchedItems: [],
  alreadyCategorizedItems: [],
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

type RespostaDeVisao = unknown | ((url: URL) => unknown)

function padrao(visao: RespostaDeVisao, categorias: unknown = CATEGORIAS) {
  return (metodo: string, url: URL) => {
    const caminho = url.pathname.replace('/api/v1', '')
    if (caminho === '/me') return jsonResponse(200, SESSAO)
    if (caminho === '/categories') return jsonResponse(200, categorias)
    if (caminho === '/investments' && metodo === 'GET') {
      const corpo = typeof visao === 'function' ? (visao as (url: URL) => unknown)(url) : visao
      return corpo instanceof Response ? corpo : jsonResponse(200, corpo)
    }
    if (caminho === '/investments/detect' && metodo === 'POST') {
      return jsonResponse(200, PREVIA_VAZIA)
    }
    throw new Error(`rota não declarada no teste: ${metodo} ${url.pathname}${url.search}`)
  }
}

function rotearApi(handler: (metodo: string, url: URL) => Response) {
  fetchMock.mockImplementation((url: string, init?: RequestInit) =>
    Promise.resolve(handler(init?.method ?? 'GET', new URL(String(url), 'https://app.invalido'))),
  )
}

function pedidosDeInvestimentos() {
  return fetchMock.mock.calls
    .map((chamada) => new URL(String(chamada[0]), 'https://app.invalido'))
    .filter((url) => url.pathname.endsWith('/investments'))
}

/** A coluna de números de um período — o <div> que segura o título e a <dl>. */
function colunaDeNumeros(titulo: string): HTMLElement {
  const cabecalho = screen.getByText(titulo)
  const coluna = cabecalho.parentElement
  if (!coluna) throw new Error(`coluna não encontrada: ${titulo}`)
  return coluna
}

function renderInvestimentos(caminho = '/investimentos?mes=2026-09') {
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

describe('InvestmentsScreen', () => {
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

  /** O critério de aceite 13 da §7 da spec: o item de menu leva à tela, e a
   *  tela mostra o aporte do mês e o número do ano. */
  it('a rota existe, o item de menu leva a ela e o <h1> recebe o foco', async () => {
    rotearApi(padrao(VISAO))
    renderInvestimentos('/?mes=2026-09')

    const nav = await screen.findByRole('navigation', { name: 'Seções do aplicativo' })
    const link = within(nav).getByRole('link', { name: 'Investimentos' })
    await userEvent.click(link)

    const titulo = await screen.findByRole('heading', { level: 1, name: 'Investimentos' })
    await waitFor(() => expect(titulo).toHaveFocus())
    expect(document.title).toBe('Investimentos · HomeFinance')
    // O mês da casca viaja com a navegação.
    expect(pedidosDeInvestimentos()[0]?.searchParams.get('month')).toBe('2026-09')
  })

  it('mostra o aporte do mês e o número do ano, com a contagem de cada um', async () => {
    rotearApi(padrao(VISAO))
    renderInvestimentos()

    // Duas colunas por PERÍODO, e cada linha diz a palavra, o número e quantos
    // lançamentos o formaram.
    // Espera o DADO, e não o título da coluna: ele aparece também enquanto
    // carrega, com esqueletos no lugar dos números.
    expect(await screen.findByText('3 lançamentos')).toBeInTheDocument()

    // Cada coluna é um PERÍODO, e é dentro dela que o número precisa estar:
    // `2.000,00` aparece também na tabela dos 12 meses e na lista do mês.
    const noMes = colunaDeNumeros('Em setembro')
    expect(within(noMes).getByText('Aportes')).toBeInTheDocument()
    expect(within(noMes).getByText('2.000,00')).toBeInTheDocument()
    expect(within(noMes).getByText('3 lançamentos')).toBeInTheDocument()
    expect(within(noMes).getByText('850,00')).toBeInTheDocument()
    expect(within(noMes).getByText('1 lançamento')).toBeInTheDocument()

    const noAno = colunaDeNumeros('No ano, até setembro')
    expect(within(noAno).getByText('18.400,00')).toBeInTheDocument()
    expect(within(noAno).getByText('22 lançamentos')).toBeInTheDocument()
    expect(within(noAno).getByText('3.200,00')).toBeInTheDocument()
    expect(within(noAno).getByText('4 lançamentos')).toBeInTheDocument()

    // A frase de apoio diz de saída que aporte é dinheiro que SAI da conta.
    expect(
      screen.getByText('O que saiu para investir e o que voltou em setembro.'),
    ).toBeInTheDocument()
  })

  it('nenhum valor da tela leva sinal + ou −, nem tom de receita/despesa', async () => {
    // Aporte não é gasto e resgate não é ganho: a tela é cromaticamente
    // silenciosa, e o sinal diria a coisa errada.
    rotearApi(padrao(VISAO))
    renderInvestimentos()

    await screen.findByText('3 lançamentos')
    // `data-emphasis` é do MoneyText, e só dele — o `data-tone` do Panel não
    // entra na conta.
    const valores = document.querySelectorAll('[data-emphasis]')
    expect(valores.length).toBeGreaterThan(10)
    for (const valor of valores) {
      expect(valor.getAttribute('data-tone')).toBe('neutral')
      expect(valor.textContent).not.toMatch(/[+]/)
    }
  })

  it('a tabela de 12 meses é a fonte da verdade, com o mês por extenso', async () => {
    rotearApi(padrao(VISAO))
    renderInvestimentos()

    await screen.findByText('3 lançamentos')
    expect(screen.getByText('Últimos 12 meses, até setembro')).toBeInTheDocument()

    const tabela = screen.getByRole('table', {
      name: 'Aportes e resgates mês a mês, de outubro de 2025 a setembro de 2026',
    })
    // Doze linhas de dado, o mês POR EXTENSO (abreviação só no eixo decorativo)
    // e mês sem movimento com `0,00`, nunca travessão.
    expect(within(tabela).getAllByRole('row')).toHaveLength(13)
    expect(within(tabela).getByText('setembro de 2026')).toBeInTheDocument()
    expect(within(tabela).getByText('outubro de 2025')).toBeInTheDocument()
    expect(within(tabela).getAllByText('0,00').length).toBeGreaterThan(0)
  })

  it('série inteiramente zerada: a seção dos 12 meses não é renderizada', async () => {
    // Nem gráfico vazio, nem doze linhas de 0,00: não há o que ilustrar.
    rotearApi(padrao(VAZIA))
    renderInvestimentos()

    await screen.findByText('Nenhum aporte ou resgate em setembro de 2026.')
    expect(screen.queryByText('Últimos 12 meses, até setembro')).not.toBeInTheDocument()
    expect(
      screen.queryByRole('table', { name: /Aportes e resgates mês a mês/ }),
    ).not.toBeInTheDocument()
  })

  it('a lista é plana, com Movimento em palavra — nunca só cor', async () => {
    rotearApi(padrao(VISAO))
    renderInvestimentos()

    const tabela = await screen.findByRole('table', {
      name: 'Aportes e resgates de setembro de 2026',
    })
    // Sem agrupamento por dia e sem subtotal: aporte e resgate não somam.
    expect(within(tabela).queryAllByRole('rowheader')).toHaveLength(0)
    expect(within(tabela).getAllByText('Aporte').length).toBeGreaterThan(0)
    expect(within(tabela).getAllByText('Resgate').length).toBeGreaterThan(0)
    expect(within(tabela).getByText('CDB 15 DIAS')).toBeInTheDocument()
    expect(within(tabela).getByText('RESGATE CDB')).toBeInTheDocument()
  })

  it('sem categoria de investimento, a tela inteira é o vazio e o botão do topo some', async () => {
    rotearApi(padrao(VAZIA, CATEGORIAS_VAZIAS))
    renderInvestimentos()

    expect(await screen.findByText('Nenhuma categoria de investimento ainda.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Ir para categorias' })).toBeInTheDocument()

    // Dois convites para o mesmo passo é ruído — e o botão SOME, nunca aparece
    // apagado (nenhum `disabled` nesta tela).
    expect(screen.queryByRole('button', { name: 'Detectar investimentos' })).not.toBeInTheDocument()
    expect(screen.queryByText('Em setembro')).not.toBeInTheDocument()
  })

  it('com categoria e sem lançamento, o vazio é da LISTA — os números ficam', async () => {
    rotearApi(padrao({ ...VAZIA, series: serie() }))
    renderInvestimentos()

    expect(
      await screen.findByText('Nenhum aporte ou resgate em setembro de 2026.'),
    ).toBeInTheDocument()
    // Os números do ano e a série dos 12 meses continuam valendo.
    expect(screen.getByText('Em setembro')).toBeInTheDocument()
    expect(screen.getByText('Últimos 12 meses, até setembro')).toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Ver os lançamentos de setembro' }),
    ).toBeInTheDocument()
  })

  it('erro da API troca o painel pelo alerta, sem número parcial', async () => {
    rotearApi((metodo, url) => {
      const caminho = url.pathname.replace('/api/v1', '')
      if (caminho === '/me') return jsonResponse(200, SESSAO)
      if (caminho === '/categories') return jsonResponse(200, CATEGORIAS)
      if (caminho === '/investments') {
        return jsonResponse(500, { error: { code: 'INTERNAL_ERROR', message: 'falhou' } })
      }
      throw new Error(`rota não declarada: ${metodo} ${caminho}`)
    })
    renderInvestimentos()

    expect(
      await screen.findByText('Não foi possível carregar os investimentos.'),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Tentar de novo' })).toBeInTheDocument()
    // Falhou, some: nenhum número é exibido como se fosse total.
    expect(screen.queryByText('Em setembro')).not.toBeInTheDocument()
  })

  /** A página é 50 — a MESMA constante de `/lancamentos` e `/transferencias`.
   *  Os números aqui são os do exemplo ratificado em docs/DESIGN.md (k): 63 no
   *  mês, 50 na primeira página, 13 no resto. */
  it('pagina por cursor de 50 em 50 e anuncia o crescimento da lista', async () => {
    const lote = (inicio: number, quantos: number) =>
      Array.from({ length: quantos }, (_, indice) =>
        item({ id: `i-${inicio + indice}`, description: `APORTE ${inicio + indice}` }),
      )
    const total63 = { ...VISAO.monthly, contributionCount: 62, redemptionCount: 1 }

    rotearApi(
      padrao((url: URL) =>
        url.searchParams.get('cursor')
          ? { ...VISAO, monthly: total63, items: lote(51, 13), nextCursor: null }
          : { ...VISAO, monthly: total63, items: lote(1, 50), nextCursor: 'c2' },
      ),
    )
    renderInvestimentos()

    // O rótulo promete o número da REQUISIÇÃO, limitado pelo que falta: 13, e
    // não 50, porque só restam 13.
    expect(await screen.findByText('Mostrando 50 de 63 lançamentos')).toBeInTheDocument()
    expect(pedidosDeInvestimentos()[0]?.searchParams.get('limit')).toBe('50')
    await userEvent.click(screen.getByRole('button', { name: 'Carregar mais 13' }))

    await waitFor(() => expect(screen.getByText('APORTE 63')).toBeInTheDocument())
    expect(pedidosDeInvestimentos()[1]?.searchParams.get('cursor')).toBe('c2')

    // Lida a última página, o rodapé para de prometer e passa a fechar a conta.
    expect(
      await screen.findByText('63 lançamentos — é tudo o que existe no mês.'),
    ).toBeInTheDocument()
    // Há mais de uma live region no documento (a da DataTable e a da
    // paginação): a afirmação é sobre o TEXTO anunciado, não sobre qual delas.
    expect(screen.getByText('Mais 13 lançamentos carregados. 63 de 63.')).toBeInTheDocument()
  })

  it('o botão do topo abre o diálogo, e a prévia sai com dryRun e sem sobrescrever', async () => {
    rotearApi(padrao(VISAO))
    renderInvestimentos()

    await screen.findByText('Em setembro')
    await userEvent.click(screen.getByRole('button', { name: 'Detectar investimentos' }))

    expect(
      await screen.findByRole('heading', { name: 'Detectar investimentos' }),
    ).toBeInTheDocument()

    const previa = fetchMock.mock.calls.find((chamada) =>
      String(chamada[0]).includes('/investments/detect'),
    )
    if (!previa) throw new Error('a prévia não foi pedida')
    expect(JSON.parse(String((previa[1] as RequestInit).body))).toEqual({
      month: '2026-09',
      dryRun: true,
      // O ausente é o seguro, e ele nunca é herdado.
      overwriteCategorized: false,
    })
  })
})

describe('cálculo da série', () => {
  it('maximoDaSerie SELECIONA o maior dos 24 inteiros', () => {
    // Seleção, nunca cálculo: o cliente não faz aritmética de dinheiro.
    expect(maximoDaSerie(serie())).toBe(200_000)
    expect(maximoDaSerie(serieVazia('2026-09'))).toBe(0)
  })

  it('colunasDaSerie põe aporte em --chart-1 e resgate em --chart-3, nesta ordem', () => {
    const colunas = colunasDaSerie(serie())
    const setembro = colunas[11]
    expect(setembro?.valores.map((valor) => valor.papel)).toEqual(['1', '3'])
    expect(setembro?.valores[0]?.cents).toBe(200_000)
    expect(setembro?.titulos).toEqual([
      `setembro de 2026 · Aportes · ${formatarDinheiro(200_000)}`,
      `setembro de 2026 · Resgates · ${formatarDinheiro(85_000)}`,
    ])
  })

  it('o eixo é o único lugar com mês abreviado, e a virada de ano cai em janeiro', () => {
    const colunas = colunasDaSerie(serie())
    expect(colunas.map((coluna) => coluna.rotulo)).toEqual([
      'out',
      'nov',
      'dez',
      'jan',
      'fev',
      'mar',
      'abr',
      'mai',
      'jun',
      'jul',
      'ago',
      'set',
    ])
    // Uma virada só, e ela é janeiro de 2026 — nunca a primeira coluna.
    const viradas = colunas.filter((coluna) => coluna.viradaDeAno)
    expect(viradas.map((coluna) => coluna.key)).toEqual(['2026-01'])
    expect(colunas[0]?.viradaDeAno).toBeUndefined()
  })

  it('a caption da tabela nomeia as duas pontas por extenso', () => {
    expect(captionDaSerie(serie())).toBe(
      'Aportes e resgates mês a mês, de outubro de 2025 a setembro de 2026',
    )
  })

  it('serieVazia deriva do mês da URL: 12 meses terminando nele', () => {
    const vazia = serieVazia('2026-01')
    expect(vazia).toHaveLength(12)
    expect(vazia[0]?.month).toBe('2025-02')
    expect(vazia[11]?.month).toBe('2026-01')
    expect(vazia.every((ponto) => ponto.contributionsCents === 0)).toBe(true)
  })
})
