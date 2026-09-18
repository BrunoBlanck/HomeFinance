import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'

/** Bateria adversarial do QA sobre o diálogo de reprocessar (spec 0005 §13.5.10).
 *
 *  O teste do dev cobre os caminhos previstos. Aqui o servidor é HOSTIL: manda
 *  HTML em campo de texto, um `reason` que não existe no enum, números fora do
 *  razoável — e a pessoa é impaciente: clica duas vezes no confirmar, que gasta
 *  cota do limite por casa e converteria duas vezes se escapasse. */

const fetchMock = vi.fn()

const NUBANK = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'
const C6 = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d66'

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

function conta(id: string, name: string, institution: string) {
  return {
    id,
    name,
    kind: 'checking',
    institution,
    openingBalanceCents: 0,
    openingDate: '2026-01-01',
    balanceCents: 0,
    archivedAt: null,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
  }
}

const CONTAS = {
  items: [conta(NUBANK, 'Nubank', 'nubank'), conta(C6, 'C6', 'c6')],
  totalBalanceCents: 0,
}
const LISTA_VAZIA = { items: [], pairs: [], balances: [], nextCursor: null }

function previa(over: Record<string, unknown> = {}) {
  return {
    month: '2026-09',
    paired: 1,
    unpaired: 1,
    items: [
      {
        outTransactionId: 'out-1',
        inTransactionId: 'in-1',
        occurredOn: '2026-09-25',
        fromAccountId: NUBANK,
        fromAccountName: 'Nubank',
        toAccountId: C6,
        toAccountName: 'C6',
        amountCents: 300_000,
        description: 'Pix enviado - BRUNO',
        matchedKeyword: 'pix enviado bruno',
        matchScore: 88,
      },
    ],
    unpairedItems: [
      {
        id: 'u-1',
        kind: 'income',
        accountId: C6,
        accountName: 'C6',
        occurredOn: '2026-09-09',
        amountCents: 120_000,
        description: 'Pix recebido de Bruno',
        matchedKeyword: 'pix recebido bruno',
        reason: 'no_mirror',
      },
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

function rotearApi(respostaDoDetect: (corpo: { dryRun: boolean }) => Response) {
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    const corpo = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
    const chamada = { metodo, url: new URL(String(url), 'https://app.invalido'), corpo }
    chamadas.push(chamada)
    const caminho = chamada.url.pathname.replace('/api/v1', '')
    if (caminho === '/me') return Promise.resolve(jsonResponse(200, SESSAO))
    if (caminho === '/accounts') return Promise.resolve(jsonResponse(200, CONTAS))
    if (caminho === '/transfers' && metodo === 'GET') {
      return Promise.resolve(jsonResponse(200, LISTA_VAZIA))
    }
    if (caminho === '/transfers/detect' && metodo === 'POST') {
      return Promise.resolve(respostaDoDetect(corpo as { dryRun: boolean }))
    }
    throw new Error(`rota não declarada: ${metodo} ${chamada.url.pathname}`)
  })
}

function renderTransferencias(caminho = '/transferencias?mes=2026-09') {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [caminho] }))
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { queryClient }
}

async function abrirDialogo() {
  const botoes = await screen.findAllByRole('button', { name: 'Reprocessar transferências' })
  const primeiro = botoes[0]
  if (!primeiro) throw new Error('o botão do cabeçalho não existe')
  await userEvent.click(primeiro)
  return screen.findByRole('dialog')
}

const gravacoes = () =>
  chamadas.filter(
    (c) =>
      c.url.pathname.endsWith('/transfers/detect') &&
      (c.corpo as { dryRun: boolean }).dryRun === false,
  )

beforeEach(() => {
  sessionStorage.clear()
  chamadas.length = 0
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  vi.unstubAllGlobals()
  fetchMock.mockReset()
})

describe('DetectTransfersDialog — abuso', () => {
  it('clicar duas vezes seguidas em converter NÃO manda duas gravações', async () => {
    // A resposta da gravação segura por um tempo: é dentro dessa janela que o
    // segundo clique aconteceria. Cada gravação gasta cota do limite por casa
    // (60/h) — e a segunda ainda mostraria "Nada mudou" por cima do acerto.
    let liberar: ((r: Response) => void) | undefined
    rotearApi((corpo) => {
      if (corpo.dryRun) return jsonResponse(200, previa())
      return new Promise<Response>((resolve) => {
        liberar = resolve
      }) as unknown as Response
    })

    renderTransferencias()
    const dialogo = await abrirDialogo()
    const confirmar = await within(dialogo).findByRole('button', { name: 'Converter 1 par' })

    await userEvent.click(confirmar)
    await waitFor(() => {
      expect(confirmar).toHaveAttribute('aria-busy', 'true')
    })
    await userEvent.click(confirmar)
    await userEvent.dblClick(confirmar)

    expect(gravacoes()).toHaveLength(1)

    liberar?.(jsonResponse(200, previa({ paired: 1, items: [], unpairedItems: [] })))
    expect(await screen.findByText('1 transferência reconhecida.')).toBeInTheDocument()
    expect(gravacoes()).toHaveLength(1)
  })

  it('o botão ocupado anuncia aria-disabled e aria-busy enquanto grava', async () => {
    let liberar: ((r: Response) => void) | undefined
    rotearApi((corpo) => {
      if (corpo.dryRun) return jsonResponse(200, previa())
      return new Promise<Response>((resolve) => {
        liberar = resolve
      }) as unknown as Response
    })

    renderTransferencias()
    const dialogo = await abrirDialogo()
    const confirmar = await within(dialogo).findByRole('button', { name: 'Converter 1 par' })
    await userEvent.click(confirmar)

    await waitFor(() => {
      expect(confirmar).toHaveAttribute('aria-disabled', 'true')
      expect(confirmar).toHaveAttribute('aria-busy', 'true')
    })
    // Continua focável: em carregamento o botão nunca recebe `disabled`.
    expect(confirmar).not.toHaveAttribute('disabled')

    liberar?.(jsonResponse(200, previa({ paired: 1, items: [], unpairedItems: [] })))
    await screen.findByText('1 transferência reconhecida.')
  })

  it('HTML vindo do servidor em descrição, conta e palavra vira TEXTO, nunca marcação', async () => {
    const veneno = '<img src=x onerror="alert(1)">'
    rotearApi((corpo) =>
      corpo.dryRun
        ? jsonResponse(
            200,
            previa({
              items: [
                {
                  outTransactionId: 'out-1',
                  inTransactionId: 'in-1',
                  occurredOn: '2026-09-25',
                  fromAccountId: NUBANK,
                  fromAccountName: `<b>Nubank</b>`,
                  toAccountId: C6,
                  toAccountName: 'C6',
                  amountCents: 300_000,
                  description: veneno,
                  matchedKeyword: '<script>alert(2)</script>',
                  matchScore: 88,
                },
              ],
              unpairedItems: [],
              unpaired: 0,
            }),
          )
        : jsonResponse(200, previa({ paired: 1, items: [], unpairedItems: [] })),
    )

    renderTransferencias()
    const dialogo = await abrirDialogo()

    // O texto aparece LITERAL — e nenhum elemento novo nasceu dele.
    expect(await within(dialogo).findByText(veneno)).toBeInTheDocument()
    expect(document.querySelector('script')).toBeNull()
    expect(document.querySelector('img[src="x"]')).toBeNull()
    expect(within(dialogo).queryByText('Nubank', { selector: 'b' })).toBeNull()
  })

  it('`reason` fora do enum não é despejado cru na tela', async () => {
    rotearApi((corpo) =>
      corpo.dryRun
        ? jsonResponse(
            200,
            previa({
              paired: 0,
              items: [],
              unpaired: 1,
              unpairedItems: [
                {
                  id: 'u-1',
                  kind: 'income',
                  accountId: C6,
                  accountName: 'C6',
                  occurredOn: '2026-09-09',
                  amountCents: 120_000,
                  description: 'Pix recebido de Bruno',
                  matchedKeyword: 'pix recebido bruno',
                  reason: 'motivo_que_nao_existe',
                },
              ],
            }),
          )
        : jsonResponse(200, previa()),
    )

    renderTransferencias()
    const dialogo = await abrirDialogo()
    await within(dialogo).findByText('Pix recebido de Bruno')
    expect(within(dialogo).queryByText(/motivo_que_nao_existe/)).toBeNull()
    expect(within(dialogo).queryByText(/no_mirror/)).toBeNull()
  })

  it('a prévia é pedida UMA vez ao abrir — foco na janela não queima cota', async () => {
    rotearApi((corpo) => jsonResponse(200, previa(corpo.dryRun ? {} : { items: [] })))

    renderTransferencias()
    await abrirDialogo()
    await waitFor(() => {
      expect(chamadas.filter((c) => c.url.pathname.endsWith('/transfers/detect'))).toHaveLength(1)
    })

    window.dispatchEvent(new Event('focus'))
    window.dispatchEvent(new Event('visibilitychange'))
    await new Promise((r) => setTimeout(r, 50))

    expect(chamadas.filter((c) => c.url.pathname.endsWith('/transfers/detect'))).toHaveLength(1)
  })

  it('409 no meio: nada é dado por convertido e "Conferir de novo" refaz a PRÉVIA, não a gravação', async () => {
    rotearApi((corpo) =>
      corpo.dryRun
        ? jsonResponse(200, previa())
        : jsonResponse(409, { error: { code: 'CONFLICT', message: 'Os dados mudaram.' } }),
    )

    renderTransferencias()
    const dialogo = await abrirDialogo()
    await userEvent.click(await within(dialogo).findByRole('button', { name: 'Converter 1 par' }))

    const aviso = await within(dialogo).findByRole('alert')
    expect(aviso).toHaveTextContent('O mês mudou enquanto a prévia estava aberta.')
    const refazer = within(dialogo).getByRole('button', { name: 'Conferir de novo' })
    expect(screen.queryByText(/transferência reconhecida/)).toBeNull()
    expect(gravacoes()).toHaveLength(1)

    await userEvent.click(refazer)
    await waitFor(() => {
      const previas = chamadas.filter(
        (c) =>
          c.url.pathname.endsWith('/transfers/detect') &&
          (c.corpo as { dryRun: boolean }).dryRun === true,
      )
      expect(previas).toHaveLength(2)
    })
    expect(gravacoes()).toHaveLength(1)
  })
})
