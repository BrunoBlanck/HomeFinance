import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'

const fetchMock = vi.fn()

const CONTA_CORRENTE = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'
const CARTAO = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d66'

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

const CONTAS = {
  items: [
    {
      id: CONTA_CORRENTE,
      name: 'Conta corrente',
      kind: 'checking',
      institution: 'nubank',
      openingBalanceCents: 0,
      openingDate: '2026-01-01',
      balanceCents: 0,
      archivedAt: null,
      createdAt: '2026-01-01T00:00:00Z',
      updatedAt: '2026-01-01T00:00:00Z',
    },
    {
      id: CARTAO,
      name: 'Cartão C6',
      kind: 'credit_card',
      institution: 'c6',
      openingBalanceCents: 0,
      openingDate: '2026-01-01',
      balanceCents: 0,
      archivedAt: null,
      createdAt: '2026-01-01T00:00:00Z',
      updatedAt: '2026-01-01T00:00:00Z',
    },
  ],
  totalBalanceCents: 0,
}

function lancamento(over: Record<string, unknown> = {}) {
  return {
    id: 'tx-1',
    kind: 'expense',
    accountId: CONTA_CORRENTE,
    accountName: 'Conta corrente',
    categoryId: 'cat-1',
    categoryName: 'Alimentação',
    amountCents: 2900,
    description: 'Padaria Exemplo',
    occurredOn: '2026-08-31',
    yearMonth: '2026-08',
    competenceMonth: '2026-08',
    transferGroupId: null,
    statementId: null,
    source: 'import',
    importBatchId: null,
    createdBy: SESSAO.user.id,
    createdAt: '2026-08-31T00:00:00Z',
    updatedAt: '2026-08-31T00:00:00Z',
    ...over,
  }
}

function resumo(over: Record<string, number> = {}) {
  return {
    incomeCents: 0,
    expenseCents: 2900,
    netCents: -2900,
    count: 1,
    uncategorizedCount: 0,
    // Sempre presentes no contrato desde a spec 0006 §3.5.2 — zero quando a
    // casa não tem categoria de investimento.
    investedCents: 0,
    redeemedCents: 0,
    ...over,
  }
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

/** Roteia pela URL COMPLETA (com query): a paginação por cursor e o filtro de
 *  conta só se verificam olhando a query string que a tela montou. */
function rotearApi(handler: (metodo: string, url: URL) => Response | Promise<Response>) {
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    return Promise.resolve(handler(metodo, new URL(String(url), 'https://app.invalido')))
  })
}

function padrao(lista: unknown) {
  return (metodo: string, url: URL) => {
    const caminho = url.pathname.replace('/api/v1', '')
    if (caminho === '/me') return jsonResponse(200, SESSAO)
    if (caminho === '/accounts') return jsonResponse(200, CONTAS)
    if (caminho === '/transactions' && metodo === 'GET') return jsonResponse(200, lista)
    throw new Error(`rota não declarada no teste: ${metodo} ${url.pathname}${url.search}`)
  }
}

function renderLancamentos(caminho = '/lancamentos?mes=2026-08') {
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

describe('TransactionsScreen', () => {
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

  it('agrupa por dia e escreve a data SEM deslocar o fuso', async () => {
    // A armadilha: `new Date('2026-08-31')` formatado em São Paulo vira 30/08.
    // Este teste roda com o processo em qualquer fuso e o cabeçalho tem de
    // continuar dizendo 31.
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    renderLancamentos()

    expect(
      await screen.findByRole('rowheader', { name: /segunda-feira, 31 de agosto/ }),
    ).toBeInTheDocument()
  })

  it('mostra o valor com sinal explícito, sem depender de cor', async () => {
    rotearApi(
      padrao({
        items: [
          lancamento({ id: 'tx-1', kind: 'expense', amountCents: 2900 }),
          lancamento({
            id: 'tx-2',
            kind: 'income',
            amountCents: 160_000,
            description: 'Salário',
            categoryName: 'Salário',
          }),
        ],
        nextCursor: null,
        summary: resumo({ incomeCents: 160_000, netCents: 157_100, count: 2 }),
      }),
    )
    renderLancamentos()

    expect(await screen.findByText('-29,00')).toBeInTheDocument()
    expect(screen.getByText('+1.600,00')).toBeInTheDocument()
  })

  /** A faixa do mês (spec 0006 §3.5.2, docs/DESIGN.md E7 (g)).
   *
   *  `Entrou`, `Saiu` e `Resultado` deixaram de somar os aportes e resgates.
   *  Sem explicação o mês encolheria sozinho — mentira por omissão. */
  it('explica na SEGUNDA linha da faixa o que ficou fora dos números', async () => {
    rotearApi(
      padrao({
        items: [lancamento()],
        nextCursor: null,
        summary: resumo({ investedCents: 200_000, redeemedCents: 85_000 }),
      }),
    )
    renderLancamentos()

    const fora = await screen.findByText('Fora destes números:')
    const linha = fora.parentElement
    expect(linha).not.toBeNull()
    expect(linha?.textContent).toContain('em aportes')
    expect(linha?.textContent).toContain('em resgates')
    expect(within(linha as HTMLElement).getByText('2.000,00')).toBeInTheDocument()
    expect(within(linha as HTMLElement).getByText('850,00')).toBeInTheDocument()

    // NÃO é um quarto item depois de `Resultado`: é uma linha própria,
    // subordinada, com o motivo à frente dos números.
    const numeros = screen.getByText('Resultado').closest('p')
    expect(numeros).not.toBe(linha)
    expect(numeros?.textContent).not.toContain('aportes')

    // Sem tom e sem sinal: são os mesmos números de /investimentos, e lá eles
    // são silenciosos.
    for (const valor of (linha as HTMLElement).querySelectorAll('[data-emphasis]')) {
      expect(valor.getAttribute('data-tone')).toBe('neutral')
      expect(valor.textContent).not.toMatch(/[+]/)
    }

    // Sem link: Investimentos está no menu, a dois passos.
    expect(within(linha as HTMLElement).queryByRole('link')).toBeNull()
  })

  it('com um dos dois zerado, só o que existe é citado — sem separador órfão', async () => {
    rotearApi(
      padrao({
        items: [lancamento()],
        nextCursor: null,
        summary: resumo({ investedCents: 200_000, redeemedCents: 0 }),
      }),
    )
    renderLancamentos()

    const fora = await screen.findByText('Fora destes números:')
    const linha = fora.parentElement as HTMLElement
    expect(linha.textContent).toContain('em aportes')
    expect(linha.textContent).not.toContain('em resgates')
    // O `·` só existe ENTRE dois itens: sozinho ele seria pontuação órfã.
    expect(linha.textContent).not.toContain('·')
  })

  it('sem aporte nem resgate no mês, a segunda linha não existe', async () => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    renderLancamentos()

    await screen.findByText('Resultado')
    expect(screen.queryByText('Fora destes números:')).not.toBeInTheDocument()
  })

  it('deixa a transferência fora do subtotal e explica isso no cabeçalho do dia', async () => {
    // Sem a frase "· 1 transferência", o dia parece ter erro de conta: as
    // linhas visíveis somam -5.029,00 e o subtotal mostra -29,00.
    rotearApi(
      padrao({
        items: [
          lancamento({
            id: 'tx-transf',
            kind: 'transfer_out',
            amountCents: 500_000,
            description: 'Pix para Cartão C6',
            categoryId: null,
            categoryName: null,
            transferGroupId: 'grupo-1',
          }),
          lancamento({ id: 'tx-1', amountCents: 2900 }),
        ],
        nextCursor: null,
        summary: resumo({ count: 2 }),
      }),
    )
    renderLancamentos()

    const cabecalho = await screen.findByRole('rowheader', { name: /31 de agosto/ })
    expect(cabecalho).toHaveTextContent('· 1 transferência')

    // O subtotal do dia conta só receita e despesa.
    const linhaDoGrupo = cabecalho.closest('tr')
    expect(linhaDoGrupo).not.toBeNull()
    expect(within(linhaDoGrupo as HTMLElement).getByText('-29,00')).toBeInTheDocument()

    // E a perna de transferência não é pintada como despesa.
    expect(screen.getByText('Transferência')).toBeInTheDocument()
  })

  it('a faixa de pendência filtra a própria tela pela URL', async () => {
    rotearApi(
      padrao({
        items: [lancamento({ categoryId: null, categoryName: null })],
        nextCursor: null,
        summary: resumo({ uncategorizedCount: 12 }),
      }),
    )
    const router = renderLancamentos()

    expect(
      await screen.findByText(/12 lançamentos de agosto estão sem categoria/),
    ).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Ver só esses 12' }))

    await waitFor(() => {
      expect(router.state.location.search).toMatchObject({ semCategoria: 1, mes: '2026-08' })
    })
    // Com o filtro ligado a faixa muda de tom e oferece a saída.
    expect(
      await screen.findByText('Mostrando só os lançamentos sem categoria de agosto.'),
    ).toBeInTheDocument()
  })

  it('não renderiza a faixa quando a dívida zerou', async () => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    renderLancamentos()

    await screen.findByText('Padaria Exemplo')
    expect(screen.queryByText(/sem categoria/i)).not.toBeInTheDocument()
  })

  it('a URL recusa conta que não é UUID e filtro que não é 1', async () => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    const router = renderLancamentos("/lancamentos?mes=2026-08&conta=' OR 1=1 --&semCategoria=sim")

    await screen.findByText('Padaria Exemplo')

    // O que importa: nada do lixo da URL virou requisição. `location.search`
    // guarda o PARSE cru da URL — quem valida é a tela, e é essa fronteira que
    // este teste protege.
    const urls = fetchMock.mock.calls.map((chamada) => String(chamada[0]))
    expect(urls.some((url) => url.includes('accountId'))).toBe(false)
    expect(urls.some((url) => url.includes('OR 1=1'))).toBe(false)

    // E o filtro inválido não ligou o modo "só sem categoria".
    expect(
      screen.queryByText('Mostrando só os lançamentos sem categoria de agosto.'),
    ).not.toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/lancamentos')
  })

  it('leva a conta válida da URL para a query da API', async () => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    renderLancamentos(`/lancamentos?mes=2026-08&conta=${CONTA_CORRENTE}`)

    await screen.findByText('Padaria Exemplo')
    await waitFor(() => {
      const urls = fetchMock.mock.calls.map((chamada) => String(chamada[0]))
      expect(urls.some((url) => url.includes(`accountId=${CONTA_CORRENTE}`))).toBe(true)
    })
  })

  it('carrega mais por cursor e anuncia quantas linhas chegaram', async () => {
    rotearApi((metodo, url) => {
      const caminho = url.pathname.replace('/api/v1', '')
      if (caminho === '/me') return jsonResponse(200, SESSAO)
      if (caminho === '/accounts') return jsonResponse(200, CONTAS)
      if (caminho === '/transactions') {
        const cursor = url.searchParams.get('cursor')
        if (cursor === null) {
          return jsonResponse(200, {
            items: [lancamento({ id: 'tx-1', description: 'Primeira' })],
            nextCursor: 'cursor-2',
            summary: resumo({ count: 2 }),
          })
        }
        return jsonResponse(200, {
          items: [lancamento({ id: 'tx-2', description: 'Segunda', occurredOn: '2026-08-27' })],
          nextCursor: null,
          summary: resumo({ count: 2 }),
        })
      }
      throw new Error(`rota não declarada: ${metodo} ${url.pathname}`)
    })
    renderLancamentos()

    expect(await screen.findByText('Mostrando 1 de 2 lançamentos')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /Carregar mais/ }))

    expect(await screen.findByText('Segunda')).toBeInTheDocument()
    expect(
      await screen.findByText('2 lançamentos — é tudo o que existe no filtro.'),
    ).toBeInTheDocument()
    expect(screen.getByText('Mais 1 lançamento carregado. 2 de 2.')).toBeInTheDocument()
  })

  it('a confirmação de exclusão avisa EM TEXTO que a transferência vai inteira', async () => {
    rotearApi((metodo, url) => {
      const caminho = url.pathname.replace('/api/v1', '')
      if (caminho === '/me') return jsonResponse(200, SESSAO)
      if (caminho === '/accounts') return jsonResponse(200, CONTAS)
      if (caminho === '/transactions') {
        return jsonResponse(200, {
          items: [
            lancamento({
              id: 'perna-saida',
              kind: 'transfer_out',
              amountCents: 500_000,
              description: 'Pix para Cartão C6',
              categoryId: null,
              categoryName: null,
              transferGroupId: 'grupo-1',
            }),
            lancamento({
              id: 'perna-entrada',
              kind: 'transfer_in',
              accountId: CARTAO,
              accountName: 'Cartão C6',
              amountCents: 500_000,
              description: 'Pagamento recebido',
              categoryId: null,
              categoryName: null,
              transferGroupId: 'grupo-1',
            }),
          ],
          nextCursor: null,
          summary: resumo({ count: 2 }),
        })
      }
      if (metodo === 'DELETE') return new Response(null, { status: 204 })
      throw new Error(`rota não declarada: ${metodo} ${url.pathname}`)
    })
    renderLancamentos()

    await screen.findByText('Pix para Cartão C6')
    await userEvent.click(
      screen.getByRole('button', { name: /Excluir Pix para Cartão C6, 31\/08\/2026/ }),
    )

    expect(
      await screen.findByRole('heading', { name: 'Excluir a transferência inteira?' }),
    ).toBeInTheDocument()
    // O aviso do par está em TEXTO CORRIDO, não num ícone nem numa cor.
    expect(
      screen.getByText(/Excluir aqui apaga as duas: a saída de R\$ 5\.000,00 da Conta corrente/),
    ).toBeInTheDocument()
    // E o rótulo do botão repete o escopo — é o último texto lido antes do clique.
    expect(screen.getByRole('button', { name: 'Excluir as duas pernas' })).toBeInTheDocument()
  })

  it('a confirmação de um lançamento comum não fala em pernas', async () => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    renderLancamentos()

    await screen.findByText('Padaria Exemplo')
    await userEvent.click(screen.getByRole('button', { name: /Excluir Padaria Exemplo/ }))

    expect(
      await screen.findByRole('heading', { name: 'Excluir este lançamento?' }),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Excluir lançamento' })).toBeInTheDocument()
  })

  it('o estado vazio orienta em vez de dizer "nada por aqui"', async () => {
    rotearApi(
      padrao({
        items: [],
        nextCursor: null,
        summary: resumo({ expenseCents: 0, netCents: 0, count: 0 }),
      }),
    )
    renderLancamentos()

    expect(await screen.findByText('Nenhum lançamento em agosto.')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: 'Importar extrato' }).length).toBeGreaterThan(0)
  })
})
