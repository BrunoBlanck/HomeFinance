import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'
import { fraseDoResumo, fraseDoToast } from './DetectTransfersDialog'

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

/** Uma prévia "normal": 2 pares viram transferência, 1 fica sem par. */
function previa(over: Record<string, unknown> = {}) {
  return {
    month: '2026-09',
    paired: 2,
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
        description: 'Pix enviado - BRUNO RIBEIRO BLANCK',
        matchedKeyword: 'pix enviado bruno',
        matchScore: 88,
      },
      {
        outTransactionId: 'out-2',
        inTransactionId: 'in-2',
        occurredOn: '2026-09-27',
        fromAccountId: C6,
        fromAccountName: 'C6',
        toAccountId: NUBANK,
        toAccountName: 'Nubank',
        amountCents: 50_000,
        description: 'Pix enviado - BRUNO RIBEIRO BLANCK',
        matchedKeyword: 'Pix enviado - BRUNO RIBEIRO BLANCK',
        matchScore: 100,
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
        description: 'Pix recebido de Bruno Ribeiro Blanck',
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
  lista: unknown = LISTA_VAZIA,
) {
  return ({ metodo, url, corpo }: Chamada) => {
    const caminho = url.pathname.replace('/api/v1', '')
    if (caminho === '/me') return jsonResponse(200, SESSAO)
    if (caminho === '/accounts') return jsonResponse(200, CONTAS)
    if (caminho === '/transfers' && metodo === 'GET') return jsonResponse(200, lista)
    if (caminho === '/transfers/detect' && metodo === 'POST') {
      return respostaDaPrevia(corpo as { dryRun: boolean })
    }
    throw new Error(`rota não declarada no teste: ${metodo} ${url.pathname}${url.search}`)
  }
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
  return { router, queryClient }
}

/** O botão do CABEÇALHO — o do estado vazio tem o mesmo nome e vem depois. */
async function botaoDoCabecalho() {
  const botoes = await screen.findAllByRole('button', { name: 'Reprocessar transferências' })
  const primeiro = botoes[0]
  if (!primeiro) throw new Error('o botão do cabeçalho não existe')
  return primeiro
}

async function abrirDialogo() {
  await userEvent.click(await botaoDoCabecalho())
  return screen.findByRole('dialog')
}

function pedidosDeReprocessar() {
  return chamadas.filter((c) => c.url.pathname.endsWith('/transfers/detect'))
}

describe('DetectTransfersDialog', () => {
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

  it('abre já pedindo a prévia (dryRun: true) e mostra os pares e os sem par', async () => {
    rotearApi(padrao(() => jsonResponse(200, previa())))
    renderTransferencias()

    const dialogo = await abrirDialogo()

    // A prévia foi pedida com o mês da tela e SEM gravar.
    await waitFor(() => {
      expect(pedidosDeReprocessar()).toHaveLength(1)
    })
    expect(pedidosDeReprocessar()[0]?.corpo).toEqual({ month: '2026-09', dryRun: true })

    // Título, descrição com o mês por extenso e a frase-resumo com role="status".
    expect(
      within(dialogo).getByRole('heading', { name: 'Reprocessar transferências' }),
    ).toBeInTheDocument()
    expect(
      within(dialogo).getByText(/Setembro de 2026 · junta receita e despesa/),
    ).toBeInTheDocument()
    const resumo = await within(dialogo).findByRole('status')
    expect(resumo).toHaveTextContent('2 pares viram transferência; 1 lançamento fica como está.')

    // Os dois grupos, com a contagem COMPLETA (não a das linhas listadas).
    expect(
      within(dialogo).getByRole('rowheader', { name: /Viram transferência · 2/ }),
    ).toBeInTheDocument()
    const semPar = within(dialogo).getByRole('rowheader', {
      name: /Parecem transferência, mas não têm par · 1/,
    })
    expect(semPar).toHaveTextContent('Importe o extrato da outra conta e reprocesse.')

    // Data, de → para em PALAVRAS (na coluna e na segunda linha da descrição,
    // que só o CSS mostra abaixo de 40rem), descrição e valor neutro sem sinal.
    expect(within(dialogo).getByText('25/09')).toBeInTheDocument()
    expect(within(dialogo).getAllByText('de Nubank para C6').length).toBeGreaterThan(0)
    expect(within(dialogo).getAllByText('de C6 para Nubank').length).toBeGreaterThan(0)
    expect(within(dialogo).getAllByText('Pix enviado - BRUNO RIBEIRO BLANCK')).toHaveLength(2)
    expect(within(dialogo).getByText('3.000,00')).toBeInTheDocument()
    expect(within(dialogo).queryByText('-3.000,00')).not.toBeInTheDocument()
    expect(within(dialogo).queryByText('+3.000,00')).not.toBeInTheDocument()
    // Transferência é neutra: nenhum valor da prévia leva tom de receita ou despesa.
    expect(dialogo.querySelector('[data-tone="positivo"]')).toBeNull()
    expect(dialogo.querySelector('[data-tone="negativo"]')).toBeNull()

    // Proveniência: pontuação em texto; 100 nunca aparece.
    expect(within(dialogo).getByText('88% · «pix enviado bruno»')).toBeInTheDocument()
    expect(within(dialogo).getByText('«Pix enviado - BRUNO RIBEIRO BLANCK»')).toBeInTheDocument()
    expect(within(dialogo).queryByText(/100%/)).not.toBeInTheDocument()
    expect(within(dialogo).getAllByText('Reconhecida pela palavra-chave')).toHaveLength(2)

    // O sem par: a conta com a direção em palavra (é ela que diz qual extrato
    // falta), o motivo em PALAVRA, nunca o código.
    expect(within(dialogo).getByText('Pix recebido de Bruno Ribeiro Blanck')).toBeInTheDocument()
    expect(within(dialogo).getAllByText(/entrou em\s*C6/).length).toBeGreaterThan(0)
    expect(within(dialogo).getByText('sem a outra perna gravada')).toBeInTheDocument()
    expect(within(dialogo).queryByText('no_mirror')).not.toBeInTheDocument()
    expect(within(dialogo).getByText('1.200,00')).toBeInTheDocument()

    // O rótulo do confirmar carrega o número.
    const confirmar = within(dialogo).getByRole('button', { name: 'Converter 2 pares' })
    expect(confirmar).not.toHaveAttribute('aria-disabled')
  })

  it('confirma com dryRun: false, mostra o número REAL do servidor e invalida as três leituras', async () => {
    rotearApi(
      padrao((corpo) =>
        // A prévia prometeu 2; alguém excluiu uma perna no meio e o servidor
        // converteu 1. É o 1 que a pessoa precisa ler.
        corpo.dryRun
          ? jsonResponse(200, previa())
          : jsonResponse(200, previa({ paired: 1, items: [], unpairedItems: [] })),
      ),
    )
    const { queryClient } = renderTransferencias()
    const invalidar = vi.spyOn(queryClient, 'invalidateQueries')

    const dialogo = await abrirDialogo()
    await userEvent.click(await within(dialogo).findByRole('button', { name: 'Converter 2 pares' }))

    await waitFor(() => {
      expect(pedidosDeReprocessar()).toHaveLength(2)
    })
    expect(pedidosDeReprocessar()[1]?.corpo).toEqual({ month: '2026-09', dryRun: false })

    expect(await screen.findByText('1 transferência reconhecida.')).toBeInTheDocument()
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })

    // As três invalidações do critério 10 da §13.5.
    await waitFor(() => {
      const chaves = invalidar.mock.calls.map((c) => JSON.stringify(c[0]?.queryKey))
      expect(chaves).toContain(JSON.stringify(['transfers']))
      expect(chaves).toContain(JSON.stringify(['transactions']))
      expect(chaves).toContain(JSON.stringify(['accounts']))
    })
    // E a lista de transferências da tela foi mesmo relida.
    await waitFor(() => {
      const gets = chamadas.filter(
        (c) => c.metodo === 'GET' && c.url.pathname.endsWith('/transfers'),
      )
      expect(gets.length).toBeGreaterThanOrEqual(2)
    })
  })

  it('zero na gravação vira "Nada mudou", não "0 transferências"', async () => {
    rotearApi(
      padrao((corpo) =>
        corpo.dryRun
          ? jsonResponse(200, previa())
          : jsonResponse(200, previa({ paired: 0, unpaired: 0, items: [], unpairedItems: [] })),
      ),
    )
    renderTransferencias()

    const dialogo = await abrirDialogo()
    await userEvent.click(await within(dialogo).findByRole('button', { name: 'Converter 2 pares' }))

    expect(
      await screen.findByText('Nada mudou — não havia par para reconhecer.'),
    ).toBeInTheDocument()
  })

  it('prévia vazia sem candidata: estado vazio com a dica das palavras-chave e saída para contas', async () => {
    rotearApi(
      padrao(() =>
        jsonResponse(200, previa({ paired: 0, unpaired: 0, items: [], unpairedItems: [] })),
      ),
    )
    const { router } = renderTransferencias()

    const dialogo = await abrirDialogo()
    expect(
      await within(dialogo).findByText('Nenhum par para reconhecer em setembro de 2026.'),
    ).toBeInTheDocument()
    expect(
      within(dialogo).getByText(/bate com as palavras-chave das suas contas/),
    ).toBeInTheDocument()
    expect(within(dialogo).queryByText(/Ver o lançamento sem par/)).not.toBeInTheDocument()

    // Nunca `disabled`: o botão continua focável e o rótulo diz o porquê.
    const confirmar = within(dialogo).getByRole('button', { name: 'Nada a converter' })
    expect(confirmar).toHaveAttribute('aria-disabled', 'true')
    expect(confirmar).not.toBeDisabled()
    await userEvent.click(confirmar)
    expect(pedidosDeReprocessar()).toHaveLength(1)

    await userEvent.click(within(dialogo).getByRole('button', { name: 'Ir para contas' }))
    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/contas')
    })
    expect(router.state.location.search).toMatchObject({ mes: '2026-09' })
  })

  it('prévia vazia com candidatas sem par: orienta a importar e lista os sem par em <details>', async () => {
    rotearApi(
      padrao(() =>
        jsonResponse(
          200,
          previa({ paired: 0, unpaired: 3, items: [], unpairedItems: previa().unpairedItems }),
        ),
      ),
    )
    const { router } = renderTransferencias()

    const dialogo = await abrirDialogo()
    expect(
      await within(dialogo).findByText('Nenhum par para reconhecer em setembro de 2026.'),
    ).toBeInTheDocument()
    expect(
      within(dialogo).getByText(
        '3 lançamentos parecem transferência, mas a outra perna não está gravada. Importe o extrato da outra conta e reprocesse.',
      ),
    ).toBeInTheDocument()

    // Os sem par ficam num <details> fechado — auditoria, não trabalho.
    const detalhes = within(dialogo).getByText('Ver os 3 lançamentos sem par')
    expect(detalhes.closest('details')).not.toHaveAttribute('open')
    await userEvent.click(detalhes)
    expect(within(dialogo).getByText('sem a outra perna gravada')).toBeInTheDocument()
    // A lista veio com 1 de 3: a linha de corte diz isso.
    expect(within(dialogo).getByText('Mostrando 1 de 3.')).toBeInTheDocument()

    await userEvent.click(within(dialogo).getByRole('button', { name: 'Importar extrato' }))
    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/importar')
    })
  })

  it('409 CONFLICT na gravação: aviso próprio, e "Conferir de novo" refaz a prévia', async () => {
    let gravacoes = 0
    rotearApi(
      padrao((corpo) => {
        if (corpo.dryRun) return jsonResponse(200, previa())
        gravacoes += 1
        return jsonResponse(409, { error: { code: 'CONFLICT', message: 'x' } })
      }),
    )
    renderTransferencias()

    const dialogo = await abrirDialogo()
    await userEvent.click(await within(dialogo).findByRole('button', { name: 'Converter 2 pares' }))

    const aviso = await within(dialogo).findByRole('alert')
    expect(aviso).toHaveTextContent('O mês mudou enquanto a prévia estava aberta.')
    expect(aviso).toHaveTextContent(
      'Os dados mudaram enquanto a operação rodava. Confira a prévia de novo.',
    )
    expect(gravacoes).toBe(1)
    // O diálogo fica aberto, com a prévia velha ainda visível.
    expect(screen.getByRole('dialog')).toBeInTheDocument()

    // "Conferir de novo" pede a PRÉVIA outra vez — nunca grava de novo às cegas.
    await userEvent.click(within(dialogo).getByRole('button', { name: 'Conferir de novo' }))
    await waitFor(() => {
      expect(pedidosDeReprocessar()).toHaveLength(3)
    })
    expect(pedidosDeReprocessar()[2]?.corpo).toEqual({ month: '2026-09', dryRun: true })
    expect(gravacoes).toBe(1)
    // O aviso some com a prévia nova.
    await waitFor(() => {
      expect(within(dialogo).queryByRole('alert')).not.toBeInTheDocument()
    })
    expect(
      await within(dialogo).findByText('2 pares viram transferência; 1 lançamento fica como está.'),
    ).toBeInTheDocument()
  })

  it('outro erro na gravação fica DENTRO do diálogo, com o botão no lugar e sem "Conferir de novo"', async () => {
    rotearApi(
      padrao((corpo) =>
        corpo.dryRun
          ? jsonResponse(200, previa())
          : jsonResponse(429, { error: { code: 'RATE_LIMITED', message: 'x' } }),
      ),
    )
    renderTransferencias()

    const dialogo = await abrirDialogo()
    await userEvent.click(await within(dialogo).findByRole('button', { name: 'Converter 2 pares' }))

    expect(await within(dialogo).findByText('Não foi possível converter.')).toBeInTheDocument()
    expect(
      within(dialogo).getByText('Muitas tentativas. Aguarde alguns minutos e tente de novo.'),
    ).toBeInTheDocument()
    expect(
      within(dialogo).queryByRole('button', { name: 'Conferir de novo' }),
    ).not.toBeInTheDocument()
    expect(within(dialogo).getByRole('button', { name: 'Converter 2 pares' })).toBeInTheDocument()
    expect(screen.getByRole('dialog')).toBeInTheDocument()
  })

  it('erro na prévia mostra o aviso com "Tentar de novo", que pede a prévia outra vez', async () => {
    let tentativas = 0
    rotearApi(
      padrao(() => {
        tentativas += 1
        return tentativas === 1
          ? jsonResponse(500, { error: { code: 'INTERNAL_ERROR', message: 'x' } })
          : jsonResponse(200, previa())
      }),
    )
    renderTransferencias()

    const dialogo = await abrirDialogo()
    expect(
      await within(dialogo).findByText('Não foi possível conferir as palavras-chave.'),
    ).toBeInTheDocument()
    // Sem prévia não há o que confirmar — o botão explica em vez de sumir.
    expect(within(dialogo).getByRole('button', { name: 'Converter' })).toHaveAttribute(
      'aria-disabled',
      'true',
    )

    await userEvent.click(within(dialogo).getByRole('button', { name: 'Tentar de novo' }))
    expect(
      await within(dialogo).findByText('2 pares viram transferência; 1 lançamento fica como está.'),
    ).toBeInTheDocument()
    expect(pedidosDeReprocessar()).toHaveLength(2)
  })

  it('Escape fecha sem gravar e devolve o foco ao botão que abriu', async () => {
    rotearApi(padrao(() => jsonResponse(200, previa())))
    renderTransferencias()

    const botao = await botaoDoCabecalho()
    await userEvent.click(botao)
    const dialogo = await screen.findByRole('dialog')
    await within(dialogo).findByText('2 pares viram transferência; 1 lançamento fica como está.')

    await userEvent.keyboard('{Escape}')
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })
    expect(botao).toHaveFocus()
    expect(pedidosDeReprocessar()).toHaveLength(1)
  })

  it('cancelar fecha sem gravar', async () => {
    rotearApi(padrao(() => jsonResponse(200, previa())))
    renderTransferencias()

    const dialogo = await abrirDialogo()
    await within(dialogo).findByText('2 pares viram transferência; 1 lançamento fica como está.')
    await userEvent.click(within(dialogo).getByRole('button', { name: 'Cancelar' }))

    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })
    expect(pedidosDeReprocessar()).toHaveLength(1)
  })

  it('lista cortada pelo teto do servidor termina com "Mostrando N de M."', async () => {
    // O teto real do contrato é 500; o que a tela decide é só "a lista veio
    // menor que a contagem" — 60 linhas provam isso sem pagar 500 renderizações
    // no jsdom.
    const itens = Array.from({ length: 60 }, (_, i) => ({
      outTransactionId: `out-${i}`,
      inTransactionId: `in-${i}`,
      occurredOn: '2026-09-05',
      fromAccountId: NUBANK,
      fromAccountName: 'Nubank',
      toAccountId: C6,
      toAccountName: 'C6',
      amountCents: 1000,
      description: `PIX ${i}`,
      matchedKeyword: 'pix',
      matchScore: 100,
    }))
    rotearApi(
      padrao(() =>
        jsonResponse(200, previa({ paired: 700, unpaired: 0, items: itens, unpairedItems: [] })),
      ),
    )
    renderTransferencias()

    const dialogo = await abrirDialogo()
    expect(await within(dialogo).findByText('700 pares viram transferência.')).toBeInTheDocument()
    expect(within(dialogo).getByText('Mostrando 60 de 700.')).toBeInTheDocument()
    // Grupo vazio não é desenhado.
    expect(within(dialogo).queryByText(/Parecem transferência/)).not.toBeInTheDocument()
    expect(within(dialogo).getByRole('button', { name: 'Converter 700 pares' })).toBeInTheDocument()
  })

  it('singular: "1 par vira transferência." e "Converter 1 par"', async () => {
    rotearApi(
      padrao(() =>
        jsonResponse(
          200,
          previa({ paired: 1, unpaired: 0, items: [previa().items[0]], unpairedItems: [] }),
        ),
      ),
    )
    renderTransferencias()

    const dialogo = await abrirDialogo()
    expect(await within(dialogo).findByText('1 par vira transferência.')).toBeInTheDocument()
    expect(within(dialogo).getByRole('button', { name: 'Converter 1 par' })).toBeInTheDocument()
  })
})

describe('fraseDoToast', () => {
  it('flexiona pelo número real e trata o zero como notícia, não como contagem', () => {
    expect(fraseDoToast(0)).toBe('Nada mudou — não havia par para reconhecer.')
    expect(fraseDoToast(1)).toBe('1 transferência reconhecida.')
    expect(fraseDoToast(12)).toBe('12 transferências reconhecidas.')
  })
})

describe('fraseDoResumo', () => {
  it('flexiona os dois números e omite os sem par quando não há', () => {
    expect(fraseDoResumo({ paired: 2, unpaired: 0 })).toBe('2 pares viram transferência.')
    expect(fraseDoResumo({ paired: 1, unpaired: 1 })).toBe(
      '1 par vira transferência; 1 lançamento fica como está.',
    )
    expect(fraseDoResumo({ paired: 3, unpaired: 2 })).toBe(
      '3 pares viram transferência; 2 lançamentos ficam como estão.',
    )
  })
})
