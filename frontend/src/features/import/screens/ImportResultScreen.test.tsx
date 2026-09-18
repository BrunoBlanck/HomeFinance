import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'
import type { ResumoDaImportacao } from '@/lib/navigation'

const fetchMock = vi.fn()

const LOTE = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8daa'
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

const LOTE_CONFIRMADO = {
  id: LOTE,
  status: 'committed',
  accountId: CONTA_CORRENTE,
  institution: 'nubank',
  docKind: 'checking_statement',
  formatId: 'nubank.checking.v1',
  fileName: 'Nubank_2026-09-13.csv',
  encoding: 'utf-8',
  rowCount: 52,
  minDate: '2026-08-01',
  maxDate: '2026-08-31',
  counts: null,
  outcome: { imported: 42, restored: 1, skipped: 6, blocked: 3, rejected: 0, linked: 2 },
  statementSuggestion: null,
  sameContentImportedAt: null,
  expiresAt: '2026-09-14T00:00:00Z',
  createdAt: '2026-09-13T00:00:00Z',
  committedAt: '2026-09-13T01:00:00Z',
}

const RESUMO: ResumoDaImportacao = {
  resultado: {
    id: LOTE,
    status: 'committed',
    imported: 42,
    restored: 1,
    skipped: 6,
    blocked: 3,
    rejected: 0,
    linked: 2,
    statementId: null,
    transfersCreated: 1,
    blockedRows: [],
  },
  mes: '2026-08',
  semCategoria: 42,
  faturaIgnorada: {
    valorCents: 285_982,
    data: '2026-08-07',
    contaOrigemId: CONTA_CORRENTE,
    contaOrigemNome: 'Conta corrente',
  },
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function rotearApi() {
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    const caminho = new URL(String(url), 'https://app.invalido').pathname.replace('/api/v1', '')
    if (caminho === '/me') return Promise.resolve(jsonResponse(200, SESSAO))
    if (caminho === '/accounts') return Promise.resolve(jsonResponse(200, CONTAS))
    if (caminho === `/imports/${LOTE}`) {
      return Promise.resolve(
        jsonResponse(200, { batch: LOTE_CONFIRMADO, items: [], nextCursor: null }),
      )
    }
    if (caminho === '/transactions') {
      return Promise.resolve(
        jsonResponse(200, {
          items: [],
          nextCursor: null,
          summary: {
            incomeCents: 0,
            expenseCents: 0,
            netCents: 0,
            count: 0,
            uncategorizedCount: 0,
          },
        }),
      )
    }
    throw new Error(`rota não declarada no teste: ${metodo} ${caminho}`)
  })
}

async function renderResultado(comResumo = true) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: ['/'] }))
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  await router.navigate({
    to: '/importar/$importId/resultado',
    params: { importId: LOTE },
    ...(comResumo ? { state: { resumoDaImportacao: RESUMO } } : {}),
  })
  return router
}

describe('ImportResultScreen', () => {
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

  it('mostra os números reais da resposta, sem linha zerada', async () => {
    rotearApi()
    await renderResultado()

    expect(await screen.findByRole('heading', { name: 'Importação concluída' })).toBeInTheDocument()
    expect(screen.getByText('Lançamentos importados')).toBeInTheDocument()
    expect(screen.getByText('Transferências registradas')).toBeInTheDocument()
    // `rejected` é 0: uma lista de zeros é ruído, então a linha não renderiza.
    expect(screen.queryByText('Linhas rejeitadas')).not.toBeInTheDocument()

    expect(
      screen.getByText(
        '42 lançamentos importados, 1 restaurado, 1 transferência registrada, 2 vinculadas a transferências que já existiam, 6 ignorados por você e 3 bloqueados.',
      ),
    ).toBeInTheDocument()
  })

  it('conta as linhas vinculadas a transferências já registradas', async () => {
    // Vincular não cria lançamento: é uma linha própria na <dl> e uma parte
    // própria na frase — nem "importada", nem "ignorada".
    rotearApi()
    await renderResultado()

    expect(await screen.findByRole('heading', { name: 'Importação concluída' })).toBeInTheDocument()
    const rotulo = screen.getByText('Vinculadas a transferências já registradas')
    expect(rotulo.nextElementSibling).toHaveTextContent('2')
  })

  it('avisa que a dívida do cartão não volta a zero sem a transferência', async () => {
    // Este é o texto que existe para a pessoa NÃO descobrir sozinha, meses
    // depois, que a dívida do cartão só cresce.
    rotearApi()
    await renderResultado()

    expect(
      await screen.findByText(/Você deixou de fora o pagamento da fatura de/),
    ).toBeInTheDocument()
    expect(screen.getByText(/a fatura do cartão continua com o valor cheio/)).toBeInTheDocument()
    expect(
      screen.getByText(/o dinheiro já saiu da conta, mas a dívida do cartão não foi abatida/),
    ).toBeInTheDocument()
  })

  it('"Registrar a transferência" NAVEGA e entrega os dados pelo state', async () => {
    // Feature não importa de feature (AGENTS.md): a tela de resultado navega e
    // entrega; a de lançamentos decide o que fazer com o que chegou.
    rotearApi()
    const router = await renderResultado()

    await screen.findByText(/Você deixou de fora o pagamento da fatura de/)
    await userEvent.click(screen.getByRole('button', { name: 'Registrar a transferência' }))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/lancamentos')
    })
    expect(router.state.location.search).toMatchObject({ mes: '2026-08' })

    // O aviso continua do outro lado, com valor, data e conta.
    expect(await screen.findByText(/ficou de fora da importação/)).toBeInTheDocument()
    // O título do Alert é um <strong>, não um heading — a hierarquia de
    // cabeçalhos da página pertence ao conteúdo, não aos avisos.
    expect(screen.getByText('Falta registrar o pagamento da fatura')).toBeInTheDocument()

    // Valor e data de lançamento NUNCA em query string.
    expect(JSON.stringify(router.state.location.search)).not.toContain('285982')
    expect(JSON.stringify(router.state.location.search)).not.toContain('2026-08-07')
  })

  it('"Categorizar agora" leva ao mês do ARQUIVO já filtrado', async () => {
    rotearApi()
    const router = await renderResultado()

    await screen.findByText(
      /42 lançamentos entraram sem categoria\. Eles não entram em orçamento nem em relatório/,
    )
    await userEvent.click(screen.getByRole('button', { name: 'Categorizar agora' }))

    await waitFor(() => {
      expect(router.state.location.search).toMatchObject({ mes: '2026-08', semCategoria: 1 })
    })
  })

  it('sobrevive a um F5: sem state, os números vêm do lote', async () => {
    rotearApi()
    await renderResultado(false)

    expect(await screen.findByRole('heading', { name: 'Importação concluída' })).toBeInTheDocument()
    expect(await screen.findByText('Lançamentos importados')).toBeInTheDocument()
    // Sem o state não há como saber o que foi deixado de fora: as linhas de
    // staging já foram apagadas no commit. Melhor calar do que inventar.
    expect(screen.queryByText(/Você deixou de fora o pagamento da fatura/)).not.toBeInTheDocument()
  })
})
