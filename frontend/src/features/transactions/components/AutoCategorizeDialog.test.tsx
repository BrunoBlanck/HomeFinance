import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'
import { fraseDoToast } from './AutoCategorizeDialog'

const fetchMock = vi.fn()

const CONTA_CORRENTE = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'

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
  ],
  totalBalanceCents: 0,
}

function lancamento(over: Record<string, unknown> = {}) {
  return {
    id: 'tx-1',
    kind: 'expense',
    accountId: CONTA_CORRENTE,
    accountName: 'Conta corrente',
    categoryId: null,
    categoryName: null,
    amountCents: 2900,
    description: 'Padaria Exemplo',
    occurredOn: '2026-09-05',
    yearMonth: '2026-09',
    competenceMonth: '2026-09',
    transferGroupId: null,
    statementId: null,
    source: 'import',
    importBatchId: null,
    createdBy: SESSAO.user.id,
    createdAt: '2026-09-05T00:00:00Z',
    updatedAt: '2026-09-05T00:00:00Z',
    ...over,
  }
}

const LISTA_COM_PENDENCIA = {
  items: [lancamento()],
  nextCursor: null,
  summary: {
    incomeCents: 0,
    expenseCents: 2900,
    netCents: -2900,
    count: 1,
    uncategorizedCount: 18,
  },
}

/** Uma prévia "normal": 12 recebem, 6 continuam. */
function previa(over: Record<string, unknown> = {}) {
  return {
    month: '2026-09',
    categorized: 12,
    unmatched: 6,
    items: [
      {
        id: 'a1',
        description: 'SUPERMERCADO BOM PRECO',
        categoryId: 'cat-1',
        categoryName: 'Alimentação',
        matchScore: 88,
        matchedKeyword: 'supermercado',
      },
      {
        id: 'a2',
        description: 'NETFLIX.COM',
        categoryId: 'cat-2',
        categoryName: 'Assinaturas',
        matchScore: 100,
        matchedKeyword: 'netflix',
      },
    ],
    unmatchedItems: [
      { id: 'u1', description: 'PIX ENVIADO', reason: 'below_threshold' },
      { id: 'u2', description: 'POSTO SHELL', reason: 'ambiguous' },
    ],
    ...over,
  }
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

type Chamada = { metodo: string; url: URL; corpo: unknown }
const chamadas: Chamada[] = []

/** Roteia a API e GUARDA cada chamada com o corpo já lido — é pelo corpo que se
 *  verifica o `dryRun`, e é pelo `dryRun` que se distingue prévia de gravação. */
function rotearApi(handler: (chamada: Chamada) => Response) {
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    const corpo = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
    const chamada = { metodo, url: new URL(String(url), 'https://app.invalido'), corpo }
    chamadas.push(chamada)
    return Promise.resolve(handler(chamada))
  })
}

function padrao(
  respostaDaPrevia: (corpo: { dryRun: boolean }) => Response,
  lista: unknown = LISTA_COM_PENDENCIA,
) {
  return ({ metodo, url, corpo }: Chamada) => {
    const caminho = url.pathname.replace('/api/v1', '')
    if (caminho === '/me') return jsonResponse(200, SESSAO)
    if (caminho === '/accounts') return jsonResponse(200, CONTAS)
    if (caminho === '/categories') return jsonResponse(200, { expense: [], income: [] })
    if (caminho === '/transactions' && metodo === 'GET') return jsonResponse(200, lista)
    if (caminho === '/transactions/auto-categorize' && metodo === 'POST') {
      return respostaDaPrevia(corpo as { dryRun: boolean })
    }
    throw new Error(`rota não declarada no teste: ${metodo} ${url.pathname}${url.search}`)
  }
}

function renderLancamentos(caminho = '/lancamentos?mes=2026-09') {
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

async function abrirDialogo() {
  await userEvent.click(await screen.findByRole('button', { name: 'Categorizar automaticamente' }))
  return screen.findByRole('dialog')
}

function pedidosDeCategorizar() {
  return chamadas.filter((c) => c.url.pathname.endsWith('/transactions/auto-categorize'))
}

describe('AutoCategorizeDialog', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    chamadas.length = 0
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('abre já pedindo a prévia (dryRun: true) e mostra quem recebe e quem continua', async () => {
    rotearApi(padrao(() => jsonResponse(200, previa())))
    renderLancamentos()

    const dialogo = await abrirDialogo()

    // A prévia foi pedida com o mês da tela e SEM gravar.
    await waitFor(() => {
      expect(pedidosDeCategorizar()).toHaveLength(1)
    })
    expect(pedidosDeCategorizar()[0]?.corpo).toEqual({ month: '2026-09', dryRun: true })

    // Título, descrição com o mês por extenso e a frase-resumo com role="status".
    expect(
      within(dialogo).getByRole('heading', { name: 'Categorizar automaticamente' }),
    ).toBeInTheDocument()
    expect(
      within(dialogo).getByText(/Setembro de 2026 · aplica as palavras-chave das categorias/),
    ).toBeInTheDocument()
    const resumo = await within(dialogo).findByRole('status')
    expect(resumo).toHaveTextContent('12 de 18 recebem categoria.')

    // Os dois grupos, com a contagem COMPLETA (não a das linhas listadas).
    expect(
      within(dialogo).getByRole('rowheader', { name: /Recebem categoria · 12/ }),
    ).toBeInTheDocument()
    const semCategoria = within(dialogo).getByRole('rowheader', {
      name: /Continuam sem categoria · 6/,
    })
    expect(semCategoria).toHaveTextContent('Abaixo de 80% de semelhança o app não arrisca')

    // Proveniência: pontuação em texto; 100 nunca aparece.
    expect(within(dialogo).getByText('88% · «supermercado»')).toBeInTheDocument()
    expect(within(dialogo).getByText('«netflix»')).toBeInTheDocument()
    expect(within(dialogo).queryByText(/100%/)).not.toBeInTheDocument()
    // O prefixo para o leitor de tela existe.
    expect(within(dialogo).getAllByText('Sugerida pela palavra-chave').length).toBe(2)

    // Motivos viram PALAVRA, nunca o código do contrato.
    expect(within(dialogo).getByText('abaixo de 80%')).toBeInTheDocument()
    expect(within(dialogo).getByText('empate entre categorias')).toBeInTheDocument()
    expect(within(dialogo).queryByText('below_threshold')).not.toBeInTheDocument()

    // O rótulo do confirmar carrega o número.
    const confirmar = within(dialogo).getByRole('button', { name: 'Categorizar 12 lançamentos' })
    expect(confirmar).not.toHaveAttribute('aria-disabled')
  })

  it('confirma com dryRun: false, mostra o número REAL do servidor e recarrega a lista', async () => {
    rotearApi(
      padrao((corpo) =>
        // A prévia prometeu 12; alguém categorizou à mão no meio e o servidor
        // gravou 10. É o 10 que a pessoa precisa ler.
        corpo.dryRun
          ? jsonResponse(200, previa())
          : jsonResponse(200, previa({ categorized: 10, items: [], unmatchedItems: [] })),
      ),
    )
    renderLancamentos()

    const dialogo = await abrirDialogo()
    await userEvent.click(
      await within(dialogo).findByRole('button', { name: 'Categorizar 12 lançamentos' }),
    )

    await waitFor(() => {
      expect(pedidosDeCategorizar()).toHaveLength(2)
    })
    expect(pedidosDeCategorizar()[1]?.corpo).toEqual({ month: '2026-09', dryRun: false })

    expect(await screen.findByText('10 lançamentos categorizados.')).toBeInTheDocument()
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })

    // A lista de lançamentos foi invalidada: houve um segundo GET.
    await waitFor(() => {
      const gets = chamadas.filter(
        (c) => c.metodo === 'GET' && c.url.pathname.endsWith('/transactions'),
      )
      expect(gets.length).toBeGreaterThanOrEqual(2)
    })
  })

  it('zero na gravação vira a frase de "já tinham categoria", não "0 categorizados"', async () => {
    rotearApi(
      padrao((corpo) =>
        corpo.dryRun
          ? jsonResponse(200, previa())
          : jsonResponse(200, previa({ categorized: 0, items: [], unmatchedItems: [] })),
      ),
    )
    renderLancamentos()

    const dialogo = await abrirDialogo()
    await userEvent.click(
      await within(dialogo).findByRole('button', { name: 'Categorizar 12 lançamentos' }),
    )

    expect(
      await screen.findByText('Nada foi categorizado — os lançamentos já tinham categoria.'),
    ).toBeInTheDocument()
  })

  it('prévia vazia: estado vazio com saída para categorias, botão explicando e os motivos em <details>', async () => {
    rotearApi(
      padrao(() =>
        jsonResponse(
          200,
          previa({
            categorized: 0,
            unmatched: 18,
            items: [],
            unmatchedItems: [{ id: 'u1', description: 'PIX ENVIADO', reason: 'below_threshold' }],
          }),
        ),
      ),
    )
    const router = renderLancamentos()

    const dialogo = await abrirDialogo()
    expect(
      await within(dialogo).findByText('Nenhum lançamento receberia categoria.'),
    ).toBeInTheDocument()
    expect(
      within(dialogo).getByText(/não batem com as descrições destes 18 lançamentos/),
    ).toBeInTheDocument()

    // Nunca `disabled`: o botão continua focável e o rótulo diz o porquê.
    const confirmar = within(dialogo).getByRole('button', { name: 'Nada a categorizar' })
    expect(confirmar).toHaveAttribute('aria-disabled', 'true')
    expect(confirmar).not.toBeDisabled()
    await userEvent.click(confirmar)
    expect(pedidosDeCategorizar()).toHaveLength(1)

    // Os motivos ficam num <details> fechado — auditoria, não trabalho.
    const detalhes = within(dialogo).getByText('Ver os 18 lançamentos e o motivo')
    expect(detalhes.closest('details')).not.toHaveAttribute('open')
    await userEvent.click(detalhes)
    expect(within(dialogo).getByText('abaixo de 80%')).toBeInTheDocument()

    await userEvent.click(within(dialogo).getByRole('button', { name: 'Ir para categorias' }))
    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/categorias')
    })
    expect(router.state.location.search).toMatchObject({ mes: '2026-09' })
  })

  it('erro na prévia mostra o aviso com "Tentar de novo", que pede a prévia outra vez', async () => {
    let tentativas = 0
    rotearApi(
      padrao(() => {
        tentativas += 1
        return tentativas === 1
          ? jsonResponse(500, { error: { code: 'INTERNAL', message: 'x' } })
          : jsonResponse(200, previa())
      }),
    )
    renderLancamentos()

    const dialogo = await abrirDialogo()
    expect(
      await within(dialogo).findByText('Não foi possível conferir as palavras-chave.'),
    ).toBeInTheDocument()
    // Sem prévia não há o que confirmar — o botão explica em vez de sumir.
    expect(within(dialogo).getByRole('button', { name: 'Categorizar' })).toHaveAttribute(
      'aria-disabled',
      'true',
    )

    await userEvent.click(within(dialogo).getByRole('button', { name: 'Tentar de novo' }))
    expect(await within(dialogo).findByText('12 de 18 recebem categoria.')).toBeInTheDocument()
    expect(pedidosDeCategorizar()).toHaveLength(2)
  })

  it('erro na gravação fica DENTRO do diálogo, com o botão no lugar', async () => {
    rotearApi(
      padrao((corpo) =>
        corpo.dryRun
          ? jsonResponse(200, previa())
          : jsonResponse(429, { error: { code: 'RATE_LIMITED', message: 'x' } }),
      ),
    )
    renderLancamentos()

    const dialogo = await abrirDialogo()
    await userEvent.click(
      await within(dialogo).findByRole('button', { name: 'Categorizar 12 lançamentos' }),
    )

    expect(await within(dialogo).findByText('Não foi possível categorizar.')).toBeInTheDocument()
    expect(
      within(dialogo).getByRole('button', { name: 'Categorizar 12 lançamentos' }),
    ).toBeInTheDocument()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('lista cortada em 500 termina com "Mostrando as primeiras 500."', async () => {
    const itens = Array.from({ length: 500 }, (_, i) => ({
      id: `a${i}`,
      description: `LINHA ${i}`,
      categoryId: 'cat-1',
      categoryName: 'Alimentação',
      matchScore: 90,
      matchedKeyword: 'mercado',
    }))
    rotearApi(
      padrao(() =>
        jsonResponse(
          200,
          previa({ categorized: 700, unmatched: 0, items: itens, unmatchedItems: [] }),
        ),
      ),
    )
    renderLancamentos()

    const dialogo = await abrirDialogo()
    expect(await within(dialogo).findByText('700 de 700 recebem categoria.')).toBeInTheDocument()
    expect(within(dialogo).getByText('Mostrando as primeiras 500.')).toBeInTheDocument()
    // Grupo vazio não é desenhado.
    expect(within(dialogo).queryByText(/Continuam sem categoria/)).not.toBeInTheDocument()
    expect(
      within(dialogo).getByRole('button', { name: 'Categorizar 700 lançamentos' }),
    ).toBeInTheDocument()
  })

  it('singular: "1 de 2 recebe categoria." e "Categorizar 1 lançamento"', async () => {
    rotearApi(
      padrao(() =>
        jsonResponse(
          200,
          previa({
            categorized: 1,
            unmatched: 1,
            items: [previa().items[0]],
            unmatchedItems: [previa().unmatchedItems[0]],
          }),
        ),
      ),
    )
    renderLancamentos()

    const dialogo = await abrirDialogo()
    expect(await within(dialogo).findByText('1 de 2 recebe categoria.')).toBeInTheDocument()
    expect(
      within(dialogo).getByRole('button', { name: 'Categorizar 1 lançamento' }),
    ).toBeInTheDocument()
  })

  it('o botão também está na faixa do filtro ativo', async () => {
    rotearApi(padrao(() => jsonResponse(200, previa())))
    renderLancamentos('/lancamentos?mes=2026-09&semCategoria=1')

    expect(
      await screen.findByText('Mostrando só os lançamentos sem categoria de setembro.'),
    ).toBeInTheDocument()
    // O primário só entra quando a contagem de pendência chegou — antes disso a
    // faixa não promete uma ação sobre um número que ainda não existe.
    expect(
      await screen.findByRole('button', { name: 'Categorizar automaticamente' }),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Mostrar todos os lançamentos' })).toBeInTheDocument()
  })

  it('cancelar fecha sem gravar', async () => {
    rotearApi(padrao(() => jsonResponse(200, previa())))
    renderLancamentos()

    const dialogo = await abrirDialogo()
    await within(dialogo).findByText('12 de 18 recebem categoria.')
    await userEvent.click(within(dialogo).getByRole('button', { name: 'Cancelar' }))

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })
    expect(pedidosDeCategorizar()).toHaveLength(1)
  })
})

describe('fraseDoToast', () => {
  it('flexiona pelo número real e trata o zero como notícia, não como contagem', () => {
    expect(fraseDoToast(0)).toBe('Nada foi categorizado — os lançamentos já tinham categoria.')
    expect(fraseDoToast(1)).toBe('1 lançamento categorizado.')
    expect(fraseDoToast(12)).toBe('12 lançamentos categorizados.')
  })
})
