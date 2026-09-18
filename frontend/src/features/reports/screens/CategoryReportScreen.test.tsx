import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { CategoryReport, CategoryReportGroup } from '@/api/types'
import { createAppRouter } from '@/app/router'
import { categoryReportQueryKey } from '../api/categoryReport'

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
  totalCents: 800_000,
  count: 2,
  items: [grupoSimples('0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d09', 'Salário', 800_000, 2, 10_000)],
}

const VAZIO: CategoryReport = {
  month: '2026-09',
  kind: 'expense',
  totalCents: 0,
  count: 0,
  items: [],
}

const SO_SEM_CATEGORIA: CategoryReport = {
  month: '2026-09',
  kind: 'expense',
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

type Relatorio = CategoryReport | ((url: URL) => Response)

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
      'Alimentação20 lançamentos',
      '20',
      '41,00%',
      '2.100,00',
    ])
    // Subcategoria: recuada, com o nome do grupo só para o leitor de tela.
    const mercado = linhaPorNome(/Mercado/)
    expect(celulas(mercado)).toEqual(['Mercado12 lançamentos', '12', '29,28%', '1.500,00'])
    expect(within(mercado).getByText('em Alimentação:')).toHaveClass('sr-only')

    // "Sem subcategoria": só onde o grupo TEM filhas e há lançamento direto.
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

    // Rodapé: Total · 87 · 100,00% · 5.123,45.
    const rodape = tabela.querySelector('tfoot tr') as HTMLElement
    expect(within(rodape).getByRole('rowheader').textContent).toBe('Total')
    expect(celulas(rodape)).toEqual(['87', '100,00%', '5.123,45'])
  })

  it('categoria arquivada aparece com o sufixo, e com o valor inteiro', async () => {
    rotearApi()
    renderRelatorio()

    const transporte = await waitFor(() => linhaPorNome(/^Transporte/))
    expect(celulas(transporte)).toEqual([
      'Transporte (arquivada)8 lançamentos',
      '8',
      '15,60%',
      '800,00',
    ])
    // Sem `data-archived`: o dinheiro é real e não se apaga num relatório.
    expect(transporte).not.toHaveAttribute('data-archived')
  })

  it('"Sem categoria" tem o link Categorizar para o filtro de /lancamentos', async () => {
    rotearApi()
    renderRelatorio()

    const link = await screen.findByRole('link', {
      name: 'Categorizar os lançamentos sem categoria de setembro',
    })
    expect(link).toHaveTextContent('Categorizar')
    expect(link).toHaveAttribute('href', '/lancamentos?mes=2026-09&semCategoria=1')
    expect(celulas(linhaPorNome(/^Sem categoria/))).toEqual([
      'Sem categoria·Categorizar37 lançamentos',
      '37',
      '2,45%',
      '123,45',
    ])
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
    // Sem placeholder: sempre há valor.
    expect(
      within(seletor)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['Despesas', 'Receitas'])

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
    const chave = categoryReportQueryKey({ mes: '2026-09', kind: 'expense' })
    expect(chave[0]).toBe('transactions')
    expect(chave).toEqual([
      'transactions',
      'reports',
      'by-category',
      { mes: '2026-09', kind: 'expense' },
    ])
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
