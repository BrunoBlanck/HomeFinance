import { hashKey, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { CategoryReport, CategoryReportGroup } from '@/api/types'
import { createAppRouter } from '@/app/router'
import { categoryReportQueryKey, categoryReportQueryOptions } from '../api/categoryReport'

const fetchMock = vi.fn()

const ALIMENTACAO = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d01'
const MERCADO = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d02'
const RESTAURANTE = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d03'
const MORADIA = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d04'
const TRANSPORTE = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d05'
const LAZER = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d06'
const SAUDE = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d07'
const EDUCACAO = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d08'

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

function folha(
  categoryId: string,
  name: string,
  totalCents: number,
  count: number,
  shareBp: number,
  archivedAt: string | null = null,
) {
  return { categoryId, name, archivedAt, totalCents, count, shareBp }
}

function grupoSimples(
  categoryId: string | null,
  name: string | null,
  totalCents: number,
  count: number,
  shareBp: number,
  archivedAt: string | null = null,
): CategoryReportGroup {
  return {
    categoryId,
    name,
    archivedAt,
    totalCents,
    count,
    shareBp,
    directCents: totalCents,
    directCount: count,
    directShareBp: shareBp,
    children: [],
  }
}

/** O mês de referência dos testes.
 *
 *  Total 5.123,45 em 87 lançamentos. Σ shareBp = 10000, e Alimentação fecha:
 *  586 (direto) + 2928 (Mercado) + 586 (Restaurante) = 4100. */
const RELATORIO: CategoryReport = {
  month: '2026-09',
  kind: 'expense',
  accountGroup: null,
  totalCents: 512_345,
  count: 87,
  items: [
    {
      categoryId: ALIMENTACAO,
      name: 'Alimentação',
      archivedAt: null,
      totalCents: 210_000,
      count: 20,
      shareBp: 4100,
      directCents: 30_000,
      directCount: 3,
      directShareBp: 586,
      children: [
        folha(MERCADO, 'Mercado', 150_000, 12, 2928),
        folha(RESTAURANTE, 'Restaurante', 30_000, 5, 586),
      ],
    },
    grupoSimples(MORADIA, 'Moradia', 120_000, 10, 2340),
    grupoSimples(TRANSPORTE, 'Transporte', 80_000, 8, 1560, '2026-08-01T12:00:00Z'),
    grupoSimples(LAZER, 'Lazer', 40_000, 5, 780),
    grupoSimples(SAUDE, 'Saúde', 30_000, 4, 585),
    grupoSimples(EDUCACAO, 'Educação', 20_000, 3, 390),
    grupoSimples(null, null, 12_345, 37, 245),
  ],
}

const RECEITAS: CategoryReport = {
  month: '2026-09',
  kind: 'income',
  accountGroup: null,
  totalCents: 800_000,
  count: 2,
  items: [grupoSimples('0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d09', 'Salário', 800_000, 2, 10_000)],
}

/** Os dois recortes de conta de RELATORIO (ADR-032): crédito + débito fecham
 *  o total de todas as contas — 2.000,00 + 3.123,45 = 5.123,45 e 15 + 72 = 87.
 *  A relação é do servidor; aqui ela só existe para o teste não mentir. */
const CREDITO: CategoryReport = {
  month: '2026-09',
  kind: 'expense',
  accountGroup: 'credit',
  totalCents: 200_000,
  count: 15,
  items: [
    grupoSimples(ALIMENTACAO, 'Alimentação', 150_000, 10, 7500),
    grupoSimples(LAZER, 'Lazer', 50_000, 5, 2500),
  ],
}

const DEBITO: CategoryReport = {
  month: '2026-09',
  kind: 'expense',
  accountGroup: 'debit',
  totalCents: 312_345,
  count: 72,
  items: [
    grupoSimples(MORADIA, 'Moradia', 120_000, 10, 3841),
    grupoSimples(TRANSPORTE, 'Transporte', 80_000, 8, 2561),
    grupoSimples(ALIMENTACAO, 'Alimentação', 60_000, 10, 1921),
    grupoSimples(SAUDE, 'Saúde', 30_000, 4, 960),
    grupoSimples(EDUCACAO, 'Educação', 10_000, 3, 320),
    grupoSimples(null, null, 12_345, 37, 397),
  ],
}

const VAZIO: CategoryReport = {
  month: '2026-09',
  kind: 'expense',
  accountGroup: null,
  totalCents: 0,
  count: 0,
  items: [],
}

const SO_SEM_CATEGORIA: CategoryReport = {
  month: '2026-09',
  kind: 'expense',
  accountGroup: null,
  totalCents: 30_000,
  count: 4,
  items: [grupoSimples(null, null, 30_000, 4, 10_000)],
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

type Relatorio = CategoryReport | ((url: URL) => Response | Promise<Response>)

function rotearApi(relatorio: Relatorio = RELATORIO) {
  fetchMock.mockImplementation((entrada: string) => {
    const url = new URL(String(entrada), 'https://app.invalido')
    const caminho = url.pathname.replace('/api/v1', '')
    if (caminho === '/me') return Promise.resolve(jsonResponse(200, SESSAO))
    if (caminho === '/reports/by-category') {
      return Promise.resolve(
        typeof relatorio === 'function' ? relatorio(url) : jsonResponse(200, relatorio),
      )
    }
    if (caminho === '/accounts') {
      return Promise.resolve(jsonResponse(200, { items: [], totalBalanceCents: 0 }))
    }
    if (caminho === '/transactions') {
      return Promise.resolve(jsonResponse(200, { items: [], summary: null, nextCursor: null }))
    }
    throw new Error(`rota não declarada no teste: ${url.pathname}${url.search}`)
  })
}

/** Roteia pelo par `kind × accountGroup`, como o servidor: é o que faz um
 *  teste de troca ver o quadro do recorte certo — e não o de todas as contas
 *  servido a partir do cache por uma chave de query que ignorasse o recorte. */
function rotearPorRecorte(vazio = false) {
  rotearApi((url) => {
    const kind = url.searchParams.get('kind')
    const recorte = url.searchParams.get('accountGroup')
    if (vazio) {
      return jsonResponse(200, {
        ...VAZIO,
        kind: kind ?? 'expense',
        accountGroup: recorte,
      })
    }
    if (kind === 'income') return jsonResponse(200, RECEITAS)
    if (recorte === 'credit') return jsonResponse(200, CREDITO)
    if (recorte === 'debit') return jsonResponse(200, DEBITO)
    return jsonResponse(200, RELATORIO)
  })
}

function pedidosDoRelatorio(): URL[] {
  return fetchMock.mock.calls
    .map((chamada) => new URL(String(chamada[0]), 'https://app.invalido'))
    .filter((url) => url.pathname.endsWith('/reports/by-category'))
}

function renderRelatorio(caminho = '/relatorios/categorias?mes=2026-09') {
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

/** As células VISÍVEIS de uma linha, sem o texto que só o leitor de tela lê. */
function celulas(linha: HTMLElement): string[] {
  return within(linha)
    .getAllByRole('cell')
    .map((celula) => {
      const copia = celula.cloneNode(true) as HTMLElement
      for (const oculto of copia.querySelectorAll('.sr-only, [aria-hidden="true"] + .sr-only')) {
        oculto.remove()
      }
      return (copia.textContent ?? '').replace(/ /g, ' ').trim()
    })
}

function linhaPorNome(nome: string | RegExp): HTMLElement {
  return screen.getByRole('row', { name: nome })
}

describe('CategoryReportScreen', () => {
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

  it('a rota existe, tem título de documento, e o <h1> recebe o foco', async () => {
    rotearApi()
    renderRelatorio()

    const titulo = await screen.findByRole('heading', { level: 1, name: 'Gastos por categoria' })
    expect(document.title).toBe('Gastos por categoria · HomeFinance')
    expect(titulo).toHaveFocus()
    expect(screen.getByText('Para onde foi o dinheiro em setembro.')).toBeInTheDocument()
    // E o item de navegação leva a ela e fica marcado.
    expect(screen.getByRole('link', { name: 'Relatórios' })).toHaveAttribute('aria-current', 'page')
  })

  /** O caso que escapou (BUG-1, QA de 17/09/2026).
   *
   *  O teste acima passa por acidente: no jsdom, numa primeira renderização
   *  ninguém tem o foco e `document.activeElement` é o `<body>`. Chegando pelo
   *  caminho de verdade — clicar em **Relatórios** na navegação — quem tem o
   *  foco é o `<a>` do menu, e era exatamente aí que a guarda por
   *  `activeElement` barrava o foco legítimo da entrada na rota.
   *
   *  Por isso este caso ENTRA pela navegação, e não pela URL: é a diferença
   *  entre as duas chegadas que o defeito vivia. */
  it('chegando pelo menu — com o foco no link —, o <h1> ainda recebe o foco', async () => {
    rotearApi()
    const user = userEvent.setup()
    renderRelatorio('/?mes=2026-09')

    const link = await screen.findByRole('link', { name: 'Relatórios' })
    // O clique põe o foco no link, como faz o navegador — é esse foco que a
    // guarda por `activeElement` lia, e era por causa dele que o `<h1>` ficava
    // sem foco nenhum.
    await user.click(link)

    const titulo = await screen.findByRole('heading', { level: 1, name: 'Gastos por categoria' })
    await waitFor(() => expect(titulo).toHaveFocus())
    expect(link).not.toHaveFocus()
  })

  /** O outro lado da mesma moeda: a guarda existia para proteger o foco do
   *  `Select` na troca de natureza. Só que o roteador **não remonta** a tela
   *  numa mudança de busca (medido: o mesmo nó `<h1>` e o mesmo nó `<select>`
   *  sobrevivem à troca), então o efeito de montagem nem roda de novo e não há
   *  foco nenhum a roubar. Este caso é o que impede a guarda de voltar. */
  it('trocar a natureza NÃO tira o foco do seletor', async () => {
    fetchMock.mockImplementation((entrada: string) => {
      const url = new URL(String(entrada), 'https://app.invalido')
      const caminho = url.pathname.replace('/api/v1', '')
      if (caminho === '/me') return Promise.resolve(jsonResponse(200, SESSAO))
      if (caminho === '/reports/by-category') {
        const kind = url.searchParams.get('kind')
        return Promise.resolve(jsonResponse(200, kind === 'income' ? RECEITAS : RELATORIO))
      }
      throw new Error(`rota não declarada no teste: ${url.pathname}`)
    })
    const user = userEvent.setup()
    const { router } = renderRelatorio()

    await screen.findByRole('table', { name: /Gastos por categoria/ })
    const seletor = screen.getByLabelText('Natureza')
    seletor.focus()

    await user.selectOptions(seletor, 'receitas')

    await waitFor(() =>
      expect(router.state.location.search).toEqual({ mes: '2026-09', natureza: 'receitas' }),
    )
    await screen.findByRole('heading', { level: 1, name: 'Receitas por categoria' })
    // O foco continua onde a pessoa o deixou: no seletor que ela acabou de
    // usar. Com o teclado, perder isto é perder o controle no meio da escolha.
    expect(screen.getByLabelText('Natureza')).toHaveFocus()
    expect(screen.getByRole('heading', { level: 1 })).not.toHaveFocus()
  })

  it('a faixa traz o total e a contagem do SERVIDOR', async () => {
    rotearApi()
    renderRelatorio()

    const resumo = (await screen.findByText(/em 87 lançamentos/)).closest('p') as HTMLElement
    expect(resumo.textContent?.replace(/ /g, ' ')).toContain('R$ 5.123,45')

    const pedido = pedidosDoRelatorio().at(-1)
    expect(pedido?.searchParams.get('month')).toBe('2026-09')
    expect(pedido?.searchParams.get('kind')).toBe('expense')
  })

  it('a tabela é a fonte: uma linha por grupo e por subcategoria, com tfoot', async () => {
    rotearApi()
    renderRelatorio()

    const tabela = await screen.findByRole('table', {
      name: 'Gastos por categoria em setembro de 2026',
    })
    expect(
      within(tabela)
        .getAllByRole('columnheader')
        .map((th) => th.textContent),
    ).toEqual(['Categoria', 'Lançamentos', 'Participação', 'Valor'])

    // Grupo: contagem com as filhas, participação em DUAS casas, valor sem "R$".
    expect(celulas(linhaPorNome(/^Alimentação/))).toEqual([
      'Alimentação·Ver lançamentos20 lançamentos',
      '20',
      '41,00%',
      '2.100,00',
    ])

    // A tabela nasce MINIMIZADA: subcategoria nenhuma na tela até o clique.
    expect(screen.queryByText('Mercado')).not.toBeInTheDocument()
    expect(screen.queryByText('Sem subcategoria')).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Alimentação', expanded: false }))

    // Subcategoria: recuada, com o nome do grupo só para o leitor de tela.
    const mercado = linhaPorNome(/Mercado/)
    expect(celulas(mercado)).toEqual([
      'Mercado·Ver lançamentos12 lançamentos',
      '12',
      '29,28%',
      '1.500,00',
    ])
    expect(within(mercado).getByText('em Alimentação:')).toHaveClass('sr-only')

    // "Sem subcategoria": só onde o grupo TEM filhas e há lançamento direto.
    // Ela NÃO ganha atalho — `?categoria=` do grupo traria as filhas junto.
    expect(celulas(linhaPorNome(/Sem subcategoria/))).toEqual([
      'Sem subcategoria3 lançamentos',
      '3',
      '5,86%',
      '300,00',
    ])
    // Moradia é folha: nada de "Sem subcategoria" para ela.
    expect(screen.getAllByText('Sem subcategoria')).toHaveLength(1)

    // As linhas do grupo fecham o grupo, sem nota de arredondamento.
    expect(2928 + 586 + 586).toBe(4100)

    // Rodapé: Total · 87 · 100,00% · 5.123,45 — o mesmo aberto ou fechado.
    const rodape = tabela.querySelector('tfoot tr') as HTMLElement
    expect(within(rodape).getByRole('rowheader').textContent).toBe('Total')
    expect(celulas(rodape)).toEqual(['87', '100,00%', '5.123,45'])
  })

  it('o grupo abre e fecha, e grupo FOLHA não vira botão', async () => {
    rotearApi()
    renderRelatorio()

    const alimentacao = await screen.findByRole('button', { name: 'Alimentação' })
    expect(alimentacao).toHaveAttribute('aria-expanded', 'false')

    await userEvent.click(alimentacao)
    expect(alimentacao).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('Mercado')).toBeInTheDocument()

    await userEvent.click(alimentacao)
    expect(alimentacao).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('Mercado')).not.toBeInTheDocument()

    // Moradia é folha: um botão que não abre nada seria um botão que mente.
    expect(screen.queryByRole('button', { name: 'Moradia' })).not.toBeInTheDocument()
  })

  it('o que estava aberto não vaza para o quadro de outra natureza', async () => {
    rotearPorRecorte()
    renderRelatorio()

    await userEvent.click(await screen.findByRole('button', { name: 'Alimentação' }))
    expect(screen.getByText('Mercado')).toBeInTheDocument()

    await userEvent.selectOptions(screen.getByLabelText('Natureza'), 'despesas-credito')
    await screen.findByRole('heading', { level: 1, name: 'Gastos no crédito por categoria' })
    // O quadro do crédito tem Alimentação, e ela não herda a abertura do
    // quadro anterior — nem as subcategorias dele.
    await waitFor(() => expect(screen.queryByText('Mercado')).not.toBeInTheDocument())
  })

  it('categoria arquivada aparece com o sufixo, e com o valor inteiro', async () => {
    rotearApi()
    renderRelatorio()

    const transporte = await waitFor(() => linhaPorNome(/^Transporte/))
    expect(celulas(transporte)).toEqual([
      'Transporte (arquivada)·Ver lançamentos8 lançamentos',
      '8',
      '15,60%',
      '800,00',
    ])
    // Sem `data-archived`: o dinheiro é real e não se apaga num relatório.
    expect(transporte).not.toHaveAttribute('data-archived')
  })

  it('"Sem categoria" tem o link Categorizar com o TIPO do relatório junto', async () => {
    rotearApi()
    renderRelatorio()

    const link = await screen.findByRole('link', {
      name: 'Categorizar os lançamentos sem categoria de setembro',
    })
    expect(link).toHaveTextContent('Categorizar')
    // `tipo=despesas` é o ponto: sem ele, `/lancamentos` abre em "Tudo" e
    // mistura receita com despesa embaixo de um relatório que só fala de uma.
    expect(link).toHaveAttribute('href', '/lancamentos?mes=2026-09&tipo=despesas&semCategoria=1')
    expect(celulas(linhaPorNome(/^Sem categoria/))).toEqual([
      'Sem categoria·Categorizar37 lançamentos',
      '37',
      '2,45%',
      '123,45',
    ])
  })

  it('"Ver lançamentos" leva mês, tipo e categoria — no grupo e na subcategoria', async () => {
    rotearApi()
    renderRelatorio()

    const doGrupo = await screen.findByRole('link', {
      name: 'Ver os lançamentos de Alimentação em setembro',
    })
    expect(doGrupo).toHaveAttribute(
      'href',
      `/lancamentos?mes=2026-09&tipo=despesas&categoria=${ALIMENTACAO}`,
    )

    await userEvent.click(screen.getByRole('button', { name: 'Alimentação' }))
    expect(
      screen.getByRole('link', { name: 'Ver os lançamentos de Mercado em setembro' }),
    ).toHaveAttribute('href', `/lancamentos?mes=2026-09&tipo=despesas&categoria=${MERCADO}`)

    // "Sem subcategoria" não tem atalho: o id do grupo traria as filhas junto.
    expect(
      screen.queryByRole('link', { name: /Ver os lançamentos de Sem subcategoria/ }),
    ).not.toBeInTheDocument()
  })

  it('em receitas o atalho leva `tipo=receitas` — a natureza nunca se mistura', async () => {
    rotearPorRecorte()
    renderRelatorio('/relatorios/categorias?mes=2026-09&natureza=receitas')

    await screen.findByRole('heading', { level: 1, name: 'Receitas por categoria' })
    await screen.findByRole('link', { name: /^Ver os lançamentos de/ })
    for (const link of screen.getAllByRole('link', { name: /^Ver os lançamentos de/ })) {
      expect(link.getAttribute('href')).toContain('tipo=receitas')
    }
  })

  it('o anel tem no máximo 6 fatias, com Outras por último e a pendente inteira', async () => {
    rotearApi()
    const { container } = { container: document.body }
    renderRelatorio()

    await screen.findByRole('table', { name: /Gastos por categoria/ })

    const fatias = container.querySelectorAll('figure path[data-fill]')
    expect(fatias).toHaveLength(6)
    expect(Array.from(fatias).map((p) => p.getAttribute('data-fill'))).toEqual([
      '1',
      '2',
      '3',
      '4',
      'pendente',
      'outras',
    ])

    // A legenda repete a ordem do anel, com uma casa no percentual.
    const legenda = container.querySelector('figure ol') as HTMLElement
    expect(
      within(legenda)
        .getAllByRole('listitem')
        .map((li) => li.textContent),
    ).toEqual([
      'Alimentação41,0%',
      'Moradia23,4%',
      'Transporte15,6%',
      'Lazer7,8%',
      'Sem categoria2,5%',
      'Outras (2 categorias)9,8%',
    ])

    // A dobra é só do ANEL: Saúde e Educação continuam na tabela, com a
    // amostra hachurada.
    expect(linhaPorNome(/^Saúde/)).toBeInTheDocument()
    expect(linhaPorNome(/^Educação/)).toBeInTheDocument()
    expect(linhaPorNome(/^Saúde/).querySelector('rect[data-fill="outras"]')).toBeInTheDocument()

    // O centro traz o total do servidor, não a soma das fatias.
    expect(container.querySelector('figure [data-emphasis="hero"]')?.textContent).toContain(
      '5.123,45',
    )
  })

  it('trocar a natureza muda a URL, a query, o título e a copy', async () => {
    fetchMock.mockImplementation((entrada: string) => {
      const url = new URL(String(entrada), 'https://app.invalido')
      const caminho = url.pathname.replace('/api/v1', '')
      if (caminho === '/me') return Promise.resolve(jsonResponse(200, SESSAO))
      if (caminho === '/reports/by-category') {
        const kind = url.searchParams.get('kind')
        return Promise.resolve(jsonResponse(200, kind === 'income' ? RECEITAS : RELATORIO))
      }
      throw new Error(`rota não declarada no teste: ${url.pathname}`)
    })
    const user = userEvent.setup()
    const { router } = renderRelatorio()

    await screen.findByRole('table', { name: /Gastos por categoria/ })
    const seletor = screen.getByLabelText('Natureza')
    expect(seletor).toHaveValue('despesas')
    // Sem placeholder: sempre há valor. Quatro opções, nesta ordem — os dois
    // recortes de conta ficam entre "Despesas" e "Receitas" porque SÃO
    // despesas (ADR-032).
    expect(
      within(seletor)
        .getAllByRole('option')
        .map((o) => [o.getAttribute('value'), o.textContent]),
    ).toEqual([
      ['despesas', 'Despesas'],
      ['despesas-credito', 'Despesas no crédito'],
      ['despesas-debito', 'Despesas no débito'],
      ['receitas', 'Receitas'],
    ])

    await user.selectOptions(seletor, 'receitas')

    await waitFor(() =>
      expect(router.state.location.search).toEqual({ mes: '2026-09', natureza: 'receitas' }),
    )
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Receitas por categoria' }),
    ).toBeInTheDocument()
    expect(screen.getByText('De onde veio o dinheiro em setembro.')).toBeInTheDocument()
    expect(document.title).toBe('Receitas por categoria · HomeFinance')
    await waitFor(() =>
      expect(pedidosDoRelatorio().at(-1)?.searchParams.get('kind')).toBe('income'),
    )
    expect(
      await screen.findByRole('table', { name: 'Receitas por categoria em setembro de 2026' }),
    ).toBeInTheDocument()

    // E de volta: despesas é o padrão, e a URL canônica não escreve a chave.
    await user.selectOptions(screen.getByLabelText('Natureza'), 'despesas')
    await waitFor(() => expect(router.state.location.search).toEqual({ mes: '2026-09' }))
  })

  /** ADR-032: "Despesas no crédito" e "Despesas no débito" NÃO são naturezas —
   *  são recortes de conta da mesma natureza `expense`. A URL guarda a palavra
   *  (`?natureza=despesas-credito`) e a tela a traduz para `kind=expense` +
   *  `accountGroup=credit`; nada de `accountGroup` viaja pela URL. */
  it('Despesas no crédito: URL com a palavra, query com kind=expense e accountGroup=credit', async () => {
    rotearPorRecorte()
    const user = userEvent.setup()
    const { router } = renderRelatorio()

    await screen.findByRole('table', { name: 'Gastos por categoria em setembro de 2026' })
    await user.selectOptions(screen.getByLabelText('Natureza'), 'despesas-credito')

    await waitFor(() =>
      expect(router.state.location.search).toEqual({
        mes: '2026-09',
        natureza: 'despesas-credito',
      }),
    )
    // A copy inteira acompanha: título, apoio, `document.title`, caption.
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Gastos no crédito por categoria' }),
    ).toBeInTheDocument()
    expect(
      screen.getByText('Para onde foi o dinheiro do cartão de crédito em setembro.'),
    ).toBeInTheDocument()
    expect(document.title).toBe('Gastos no crédito por categoria · HomeFinance')
    expect(
      await screen.findByRole('table', {
        name: 'Gastos no crédito por categoria em setembro de 2026',
      }),
    ).toBeInTheDocument()

    // A requisição: `kind=expense` E `accountGroup=credit`, uma vez cada.
    const pedido = pedidosDoRelatorio().at(-1)
    expect(pedido?.searchParams.get('month')).toBe('2026-09')
    expect(pedido?.searchParams.get('kind')).toBe('expense')
    expect(pedido?.searchParams.getAll('accountGroup')).toEqual(['credit'])
    expect(pedido?.search).not.toContain('natureza')

    // E o quadro é o do RECORTE (2.000,00 em 15), não o de todas as contas.
    const resumo = (await screen.findByText(/em 15 lançamentos/)).closest('p') as HTMLElement
    // (`Intl` separa "R$" do número com NBSP; o escape explícito é para o
    // caractere não virar espaço comum numa edição e o teste passar a mentir.)
    expect(resumo.textContent?.replace(/\u00a0/g, ' ')).toContain('R$ 2.000,00')
    expect(screen.queryByRole('row', { name: /^Moradia/ })).not.toBeInTheDocument()
  })

  it('Despesas no débito: accountGroup=debit, e voltar para Despesas apaga o recorte', async () => {
    rotearPorRecorte()
    const user = userEvent.setup()
    const { router } = renderRelatorio()

    await screen.findByRole('table', { name: 'Gastos por categoria em setembro de 2026' })
    const seletor = screen.getByLabelText('Natureza')
    seletor.focus()
    await user.selectOptions(seletor, 'despesas-debito')

    await waitFor(() =>
      expect(router.state.location.search).toEqual({
        mes: '2026-09',
        natureza: 'despesas-debito',
      }),
    )
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Gastos no débito por categoria' }),
    ).toBeInTheDocument()
    expect(
      screen.getByText('Para onde foi o dinheiro que saiu direto das contas em setembro.'),
    ).toBeInTheDocument()
    expect(document.title).toBe('Gastos no débito por categoria · HomeFinance')
    expect(
      await screen.findByRole('table', {
        name: 'Gastos no débito por categoria em setembro de 2026',
      }),
    ).toBeInTheDocument()
    await waitFor(() =>
      expect(pedidosDoRelatorio().at(-1)?.searchParams.getAll('accountGroup')).toEqual(['debit']),
    )
    expect(pedidosDoRelatorio().at(-1)?.searchParams.get('kind')).toBe('expense')
    // O quadro do débito: 3.123,45 em 72, com o "Sem categoria" que só está lá.
    const resumo = (await screen.findByText(/em 72 lançamentos/)).closest('p') as HTMLElement
    expect(resumo.textContent?.replace(/\u00a0/g, ' ')).toContain('R$ 3.123,45')
    expect(linhaPorNome(/^Sem categoria/)).toBeInTheDocument()
    // Trocar recorte é trocar busca, não remontar: o foco fica no seletor.
    expect(screen.getByLabelText('Natureza')).toHaveFocus()

    // De volta a Despesas (todas as contas): a chave some da URL e o
    // `accountGroup` some da query — nunca `accountGroup=` vazio.
    await user.selectOptions(screen.getByLabelText('Natureza'), 'despesas')
    await waitFor(() => expect(router.state.location.search).toEqual({ mes: '2026-09' }))
    expect(
      await screen.findByRole('heading', { level: 1, name: 'Gastos por categoria' }),
    ).toBeInTheDocument()
    const pedido = pedidosDoRelatorio().at(-1)
    expect(pedido?.searchParams.get('kind')).toBe('expense')
    expect(pedido?.searchParams.has('accountGroup')).toBe(false)
    expect(pedido?.search).not.toContain('accountGroup')

    // E Receitas continua sem recorte: `kind=income`, sem `accountGroup`.
    await user.selectOptions(screen.getByLabelText('Natureza'), 'receitas')
    await waitFor(() =>
      expect(pedidosDoRelatorio().at(-1)?.searchParams.get('kind')).toBe('income'),
    )
    expect(pedidosDoRelatorio().at(-1)?.searchParams.has('accountGroup')).toBe(false)
    for (const url of pedidosDoRelatorio()) {
      // Em NENHUM pedido o recorte foi repetido ou mandado vazio.
      expect(url.searchParams.getAll('accountGroup').length).toBeLessThanOrEqual(1)
      expect(url.search).not.toContain('accountGroup=&')
      expect(url.search.endsWith('accountGroup=')).toBe(false)
    }
  })

  it('trocar o recorte MANTÉM o quadro: sem esqueleto, com aria-busy', async () => {
    const credito: { liberar?: (resposta: Response) => void } = {}
    rotearApi((url) => {
      if (url.searchParams.get('accountGroup') === 'credit') {
        return new Promise<Response>((resolver) => {
          credito.liberar = resolver
        })
      }
      return jsonResponse(200, RELATORIO)
    })
    const user = userEvent.setup()
    renderRelatorio()

    await screen.findByRole('table', { name: 'Gastos por categoria em setembro de 2026' })
    await user.selectOptions(screen.getByLabelText('Natureza'), 'despesas-credito')

    // A copy já é a do recorte (vem da URL), mas o quadro de todas as contas
    // continua na tela, apagado e ocupado — `keepPreviousData` vale aqui como
    // na troca de mês.
    await screen.findByRole('heading', { level: 1, name: 'Gastos no crédito por categoria' })
    await waitFor(() => expect(document.querySelector('[data-atualizando]')).toBeTruthy())
    expect(document.querySelector('[data-atualizando]')).toHaveAttribute('aria-busy', 'true')
    expect(linhaPorNome(/^Moradia/)).toBeInTheDocument()
    expect(screen.queryByText(/^Carregando/)).not.toBeInTheDocument()

    credito.liberar?.(jsonResponse(200, CREDITO))
    await waitFor(() => expect(document.querySelector('[data-atualizando]')).toBeNull())
    expect(screen.queryByRole('row', { name: /^Moradia/ })).not.toBeInTheDocument()
    expect(linhaPorNome(/^Lazer/)).toBeInTheDocument()
  })

  it('a URL com o recorte abre direto nele, com a copy do vazio por natureza', async () => {
    rotearPorRecorte(true)
    renderRelatorio('/relatorios/categorias?mes=2026-09&natureza=despesas-credito')

    expect(
      await screen.findByText('Nenhuma despesa no crédito em setembro de 2026.'),
    ).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      'Gastos no crédito por categoria',
    )
    expect(screen.getByLabelText('Natureza')).toHaveValue('despesas-credito')
    // A descrição e a saída do vazio NÃO mudam com o recorte.
    expect(
      screen.getByText(
        'O relatório aparece assim que houver lançamentos no mês — registre um em Lançamentos ou importe o extrato.',
      ),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Importar extrato' })).toBeInTheDocument()

    const user = userEvent.setup()
    await user.selectOptions(screen.getByLabelText('Natureza'), 'despesas-debito')
    expect(
      await screen.findByText('Nenhuma despesa no débito em setembro de 2026.'),
    ).toBeInTheDocument()
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      'Gastos no débito por categoria',
    )
  })

  /** `?natureza=despesas-credito&natureza=receitas` colado à mão chega ao
   *  roteador como ARRAY. A allowlist descarta o array inteiro — e é isso que
   *  garante que a tela nunca monta `accountGroup` duas vezes (400 no
   *  servidor): a tradução parte de UMA palavra, ou de nenhuma. */
  it('natureza repetida na URL cai em despesas, e o recorte nunca sai repetido', async () => {
    rotearPorRecorte()
    renderRelatorio(
      '/relatorios/categorias?mes=2026-09&natureza=despesas-credito&natureza=receitas',
    )

    await screen.findByRole('heading', { level: 1, name: 'Gastos por categoria' })
    expect(screen.getByLabelText('Natureza')).toHaveValue('despesas')
    await screen.findByRole('table', { name: 'Gastos por categoria em setembro de 2026' })

    const pedidos = pedidosDoRelatorio()
    expect(pedidos.length).toBeGreaterThan(0)
    for (const url of pedidos) {
      expect(url.searchParams.get('kind')).toBe('expense')
      expect(url.searchParams.getAll('accountGroup')).toEqual([])
      expect(url.searchParams.getAll('kind')).toHaveLength(1)
    }
  })

  it('o valor da API na URL (?natureza=credit) não vira recorte', async () => {
    rotearPorRecorte()
    renderRelatorio('/relatorios/categorias?mes=2026-09&natureza=credit')

    await screen.findByRole('heading', { level: 1, name: 'Gastos por categoria' })
    await screen.findByRole('table', { name: 'Gastos por categoria em setembro de 2026' })
    for (const url of pedidosDoRelatorio()) {
      expect(url.searchParams.has('accountGroup')).toBe(false)
    }
  })

  it('natureza inválida na URL cai em despesas, e nada dela chega à API', async () => {
    rotearApi()
    renderRelatorio('/relatorios/categorias?mes=2026-09&natureza=%27%20OR%201%3D1%20--')

    await screen.findByRole('heading', { level: 1, name: 'Gastos por categoria' })
    for (const url of pedidosDoRelatorio()) {
      expect(url.searchParams.get('kind')).toBe('expense')
      expect(url.search).not.toContain('OR')
    }
  })

  it('erro: alerta com saída, e a tabela não aparece', async () => {
    let tentativas = 0
    rotearApi(() => {
      tentativas += 1
      return tentativas === 1
        ? jsonResponse(500, { error: { code: 'INTERNAL_ERROR' } })
        : jsonResponse(200, RELATORIO)
    })
    const user = userEvent.setup()
    renderRelatorio()

    expect(await screen.findByText('Não foi possível carregar o relatório.')).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Tentar de novo' }))
    expect(await screen.findByRole('table', { name: /Gastos por categoria/ })).toBeInTheDocument()
    expect(screen.queryByText('Não foi possível carregar o relatório.')).not.toBeInTheDocument()
  })

  /** Eco divergente: a resposta veio 200, mas sob outro recorte.
   *
   *  O contrato põe `month`, `kind` e `accountGroup` em `required` na resposta
   *  exatamente para a tela não ter de adivinhar sob que rótulo está o número.
   *  Aqui ele é CONFERIDO na camada `api/`, e divergência é **erro da query** —
   *  o mesmo caminho do `isError`, que troca o `Panel` inteiro pelo alerta.
   *  Nenhum número fica sob um rótulo que não é dele (ADR-030). */
  describe('eco divergente', () => {
    const casos: { nome: string; caminho: string; resposta: CategoryReport }[] = [
      {
        nome: 'accountGroup: pedido credit, resposta null (todas as contas)',
        caminho: '/relatorios/categorias?mes=2026-09&natureza=despesas-credito',
        resposta: { ...CREDITO, accountGroup: null },
      },
      {
        nome: 'kind: pedido expense, resposta income',
        caminho: '/relatorios/categorias?mes=2026-09',
        resposta: { ...RELATORIO, kind: 'income' },
      },
      {
        nome: 'month: pedido 2026-09, resposta 2026-08',
        caminho: '/relatorios/categorias?mes=2026-09',
        resposta: { ...RELATORIO, month: '2026-08' },
      },
    ]

    for (const caso of casos) {
      it(`${caso.nome} → alerta com saída, sem tabela e sem total`, async () => {
        rotearApi(caso.resposta)
        renderRelatorio(caso.caminho)

        expect(
          await screen.findByText('Não foi possível carregar o relatório.'),
        ).toBeInTheDocument()
        // Sem copy nova: o erro é desconhecido para o `messageForError` e cai
        // na frase genérica, que é a verdade aqui.
        expect(
          screen.getByText('Algo falhou do nosso lado. Tente de novo em instantes.'),
        ).toBeInTheDocument()
        expect(screen.getByRole('button', { name: 'Tentar de novo' })).toBeInTheDocument()

        // E nada do quadro sobrevive: nem a tabela, nem o anel, nem a faixa
        // com o total e a contagem.
        expect(screen.queryByRole('table')).not.toBeInTheDocument()
        expect(document.querySelector('figure')).toBeNull()
        expect(screen.queryByText(/lançamentos$/)).not.toBeInTheDocument()
        // Erro lançado no `queryFn` também não tem segunda tentativa.
        expect(pedidosDoRelatorio()).toHaveLength(1)
      })
    }

    /** O contador acima não prova o `retry: false` sozinho: o `QueryClient`
     *  dos testes também tem `retry: false` por padrão. A opção lida da query é
     *  a que vale em produção — e ela vale para o erro lançado dentro do
     *  `queryFn` tanto quanto para o que veio do HTTP. */
    it('retry: false é da própria query, e alcança o erro lançado no queryFn', () => {
      const opcoes = categoryReportQueryOptions({
        mes: '2026-09',
        kind: 'expense',
        accountGroup: 'credit',
      })
      expect(opcoes.retry).toBe(false)
    })

    /** A regressão que mais importa: eco batendo nos três, tela normal. O caso
     *  sutil é "todas as contas" — o pedido OMITE `accountGroup` e a resposta
     *  ecoa `null`; comparar cru acusaria divergência em toda carga da tela. */
    it('eco batendo: null da resposta é o "todas as contas" do filtro, e o quadro aparece', async () => {
      rotearPorRecorte()
      const user = userEvent.setup()
      renderRelatorio()

      await screen.findByRole('table', { name: 'Gastos por categoria em setembro de 2026' })
      expect(await screen.findByText(/em 87 lançamentos/)).toBeInTheDocument()
      expect(screen.queryByText('Não foi possível carregar o relatório.')).not.toBeInTheDocument()

      // E com recorte de verdade, o eco `credit` também bate.
      await user.selectOptions(screen.getByLabelText('Natureza'), 'despesas-credito')
      await screen.findByRole('table', {
        name: 'Gastos no crédito por categoria em setembro de 2026',
      })
      expect(screen.queryByText('Não foi possível carregar o relatório.')).not.toBeInTheDocument()

      // E em receitas, onde o eco muda de `kind` e mantém o `null`.
      await user.selectOptions(screen.getByLabelText('Natureza'), 'receitas')
      await screen.findByRole('table', { name: 'Receitas por categoria em setembro de 2026' })
      expect(screen.queryByText('Não foi possível carregar o relatório.')).not.toBeInTheDocument()
    })

    /** `keepPreviousData` mantém o `data` do fetch anterior VIVO durante o erro
     *  — o que salva a tela é `isError` ser a PRIMEIRA condição do render. Isto
     *  é asserção, e não suposição: sem ela, "Moradia" e os 5.123,45 de todas
     *  as contas ficariam sob o `<h1>` "Gastos no crédito por categoria". */
    it('com quadro anterior na tela, nenhum número antigo sobra sob o rótulo novo', async () => {
      rotearApi((url) =>
        url.searchParams.get('accountGroup') === 'credit'
          ? jsonResponse(200, { ...CREDITO, accountGroup: null })
          : jsonResponse(200, RELATORIO),
      )
      const user = userEvent.setup()
      renderRelatorio()

      await screen.findByRole('table', { name: 'Gastos por categoria em setembro de 2026' })
      expect(linhaPorNome(/^Moradia/)).toBeInTheDocument()

      await user.selectOptions(screen.getByLabelText('Natureza'), 'despesas-credito')

      // O rótulo já é o do crédito (ele vem da URL, não do dado) — e é
      // justamente por isso que o quadro anterior não pode ficar.
      await screen.findByRole('heading', { level: 1, name: 'Gastos no crédito por categoria' })
      expect(await screen.findByText('Não foi possível carregar o relatório.')).toBeInTheDocument()
      expect(screen.queryByRole('table')).not.toBeInTheDocument()
      expect(screen.queryByRole('row', { name: /^Moradia/ })).not.toBeInTheDocument()
      expect(screen.queryByText(/5\.123,45/)).not.toBeInTheDocument()
      expect(screen.queryByText(/lançamentos$/)).not.toBeInTheDocument()
      expect(document.querySelector('figure')).toBeNull()
      expect(screen.getByRole('button', { name: 'Tentar de novo' })).toBeInTheDocument()
    })
  })

  it('vazio: o mês sem nada convida a importar, e não mostra resumo nem anel', async () => {
    rotearApi(VAZIO)
    renderRelatorio()

    expect(await screen.findByText('Nenhuma despesa em setembro de 2026.')).toBeInTheDocument()
    expect(
      screen.getByText(
        'O relatório aparece assim que houver lançamentos no mês — registre um em Lançamentos ou importe o extrato.',
      ),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Importar extrato' })).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(document.querySelector('figure')).toBeNull()
    expect(screen.queryByText(/lançamentos$/)).not.toBeInTheDocument()
    // O seletor de natureza continua ativo: ele não depende de dado nenhum.
    expect(screen.getByLabelText('Natureza')).toBeInTheDocument()
  })

  it('mês só com "Sem categoria" NÃO é vazio', async () => {
    rotearApi(SO_SEM_CATEGORIA)
    renderRelatorio()

    await screen.findByRole('table', { name: /Gastos por categoria/ })
    expect(screen.queryByText('Nenhuma despesa em setembro de 2026.')).not.toBeInTheDocument()
    // Anel inteiro na fatia pendente, e o link que resolve a pendência.
    const fatias = document.querySelectorAll('figure path[data-fill]')
    expect(fatias).toHaveLength(1)
    expect(fatias[0]).toHaveAttribute('data-fill', 'pendente')
    expect(
      screen.getByRole('link', { name: 'Categorizar os lançamentos sem categoria de setembro' }),
    ).toBeInTheDocument()
  })

  it('carregando: esqueleto com a estrutura real, e uma live region só', async () => {
    rotearApi()
    renderRelatorio()

    // A DataTable é quem anuncia o carregamento — uma live region só. A outra
    // `role="status"` da página é o rótulo do seletor de mês, que é da casca.
    const anuncios = await screen.findAllByRole('status')
    const doRelatorio = anuncios.filter((el) => /^Carregando/.test(el.textContent ?? ''))
    expect(doRelatorio).toHaveLength(1)
    expect(doRelatorio[0]).toHaveTextContent('Carregando gastos por categoria em setembro de 2026')

    // A figura fica só com `aria-busy`, e o esqueleto tem a estrutura real: o
    // anel já está desenhado, sem fatia nenhuma.
    expect(document.querySelector('figure')).toHaveAttribute('aria-busy', 'true')
    expect(document.querySelector('figure [role="status"]')).toBeNull()
    expect(document.querySelectorAll('figure path[data-fill]')).toHaveLength(0)
    expect(document.querySelector('figure path')).toBeTruthy()

    await screen.findByRole('table', { name: /Gastos por categoria/ })
    expect(screen.queryByText(/^Carregando/)).not.toBeInTheDocument()
    expect(document.querySelector('figure')).not.toHaveAttribute('aria-busy')
  })

  it('trocar de mês MANTÉM o quadro: sem esqueleto, com aria-busy', async () => {
    // Guardado num objeto, e não numa variável solta: o TypeScript estreita
    // para `never` o que só é atribuído dentro de um callback.
    const agosto: { liberar?: (resposta: Response) => void } = {}
    fetchMock.mockImplementation((entrada: string) => {
      const url = new URL(String(entrada), 'https://app.invalido')
      const caminho = url.pathname.replace('/api/v1', '')
      if (caminho === '/me') return Promise.resolve(jsonResponse(200, SESSAO))
      if (caminho === '/reports/by-category') {
        if (url.searchParams.get('month') === '2026-09') {
          return Promise.resolve(jsonResponse(200, RELATORIO))
        }
        return new Promise<Response>((resolver) => {
          agosto.liberar = resolver
        })
      }
      throw new Error(`rota não declarada no teste: ${url.pathname}`)
    })
    const user = userEvent.setup()
    renderRelatorio()

    await screen.findByRole('table', { name: 'Gastos por categoria em setembro de 2026' })
    await user.click(screen.getByRole('button', { name: /Mês anterior/ }))

    // Enquanto agosto não chega, o quadro de setembro continua na tela —
    // apagado e marcado como ocupado. Nenhum esqueleto, nenhum salto.
    await waitFor(() => expect(document.querySelector('[data-atualizando]')).toBeTruthy())
    expect(document.querySelector('[data-atualizando]')).toHaveAttribute('aria-busy', 'true')
    expect(linhaPorNome(/^Alimentação/)).toBeInTheDocument()
    expect(screen.queryByText(/^Carregando/)).not.toBeInTheDocument()
    expect(document.querySelectorAll('figure path[data-fill]')).toHaveLength(6)

    agosto.liberar?.(jsonResponse(200, { ...SO_SEM_CATEGORIA, month: '2026-08' }))
    await waitFor(() => expect(document.querySelector('[data-atualizando]')).toBeNull())
    expect(screen.queryByRole('row', { name: /^Alimentação/ })).not.toBeInTheDocument()
  })

  it('a chave da query mora sob ["transactions"] — toda mutação a invalida de graça', () => {
    const chave = categoryReportQueryKey({
      mes: '2026-09',
      kind: 'expense',
      accountGroup: undefined,
    })
    expect(chave[0]).toBe('transactions')
    expect(chave).toEqual([
      'transactions',
      'reports',
      'by-category',
      { mes: '2026-09', kind: 'expense', accountGroup: undefined },
    ])
  })

  /** O recorte é do SERVIDOR: fora da chave, "Despesas no crédito" seria
   *  servido do cache de "Despesas". As quatro opções da tela têm de ser quatro
   *  chaves — pelo hash que a TanStack Query usa de verdade, não por `toEqual`,
   *  que ignora `undefined`. */
  it('as quatro naturezas são quatro chaves de query distintas', () => {
    const chaves = [
      categoryReportQueryKey({ mes: '2026-09', kind: 'expense', accountGroup: undefined }),
      categoryReportQueryKey({ mes: '2026-09', kind: 'expense', accountGroup: 'credit' }),
      categoryReportQueryKey({ mes: '2026-09', kind: 'expense', accountGroup: 'debit' }),
      categoryReportQueryKey({ mes: '2026-09', kind: 'income', accountGroup: undefined }),
    ]
    expect(new Set(chaves.map((chave) => hashKey(chave))).size).toBe(4)
    // E a mesma opção é sempre a mesma chave — o `undefined` é estável no hash.
    expect(
      hashKey(categoryReportQueryKey({ mes: '2026-09', kind: 'expense', accountGroup: undefined })),
    ).toBe(hashKey(chaves[0] as readonly unknown[]))
  })

  it('o nome da categoria entra como TEXTO, nunca como marcação', async () => {
    rotearApi({
      ...VAZIO,
      totalCents: 100,
      count: 1,
      items: [grupoSimples(ALIMENTACAO, '<img src=x onerror=alert(1)>', 100, 1, 10_000)],
    })
    renderRelatorio()

    await screen.findByRole('table', { name: /Gastos por categoria/ })
    expect(document.querySelector('img')).toBeNull()
    expect(screen.getAllByText('<img src=x onerror=alert(1)>').length).toBeGreaterThan(0)
  })
})
