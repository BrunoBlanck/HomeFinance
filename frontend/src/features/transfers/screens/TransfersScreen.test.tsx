import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { TransferPair } from '@/api/types'
import { createAppRouter } from '@/app/router'
import { normalizarPar } from './TransfersScreen'

const fetchMock = vi.fn()

const NUBANK = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'
const C6 = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d66'
const CARTEIRA = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d77'

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
  items: [
    conta(NUBANK, 'Nubank', 'nubank'),
    conta(C6, 'C6', 'c6'),
    conta(CARTEIRA, 'Carteira', 'other'),
  ],
  totalBalanceCents: 0,
}

/** A = Nubank (menor id). A → B 800, B → A 2.500: a C6 mandou mais. */
const PAR_NUBANK_C6: TransferPair = {
  accountAId: NUBANK,
  accountAName: 'Nubank',
  accountBId: C6,
  accountBName: 'C6',
  aToBCents: 80_000,
  bToACents: 250_000,
  netCents: -170_000,
  count: 4,
}

const SALDOS = [
  { accountId: NUBANK, accountName: 'Nubank', balanceAtMonthEndCents: 412_000 },
  { accountId: C6, accountName: 'C6', balanceAtMonthEndCents: -198_050 },
]

function item(over: Record<string, unknown> = {}) {
  return {
    groupId: 'g-1',
    occurredOn: '2026-09-05',
    competenceMonth: '2026-09',
    fromAccountId: NUBANK,
    fromAccountName: 'Nubank',
    toAccountId: C6,
    toAccountName: 'C6',
    amountCents: 150_000,
    description: 'Transferência enviada pelo Pix',
    source: 'import',
    ...over,
  }
}

const LISTA = {
  items: [
    item(),
    item({
      groupId: 'g-2',
      occurredOn: '2026-09-12',
      amountCents: 100_000,
      source: 'manual',
      fromAccountId: C6,
      fromAccountName: 'C6',
      toAccountId: NUBANK,
      toAccountName: 'Nubank',
      description: 'Devolução',
    }),
  ],
  pairs: [PAR_NUBANK_C6],
  balances: SALDOS,
  nextCursor: null,
}

const VAZIA = { items: [], pairs: [], balances: [], nextCursor: null }

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

/** A prévia do reprocessamento, vazia: o que interessa a esta tela é que o
 *  botão abre o diálogo e o pedido sai com `dryRun: true` — o diálogo em si é
 *  testado em `DetectTransfersDialog.test.tsx`. */
const PREVIA_VAZIA = { month: '2026-09', paired: 0, unpaired: 0, items: [], unpairedItems: [] }

type Chamada = { metodo: string; url: URL; corpo: unknown }
const chamadas: Chamada[] = []

function rotearApi(handler: (metodo: string, url: URL) => Response) {
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    const corpo = typeof init?.body === 'string' ? JSON.parse(init.body) : undefined
    chamadas.push({ metodo, url: new URL(String(url), 'https://app.invalido'), corpo })
    return Promise.resolve(handler(metodo, new URL(String(url), 'https://app.invalido')))
  })
}

/** A lista pode ser um objeto ou uma função da URL — é assim que o teste de
 *  paginação responde uma coisa para cada cursor. */
type RespostaDeLista = unknown | ((url: URL) => unknown)

function padrao(lista: RespostaDeLista) {
  return (metodo: string, url: URL) => {
    const caminho = url.pathname.replace('/api/v1', '')
    if (caminho === '/me') return jsonResponse(200, SESSAO)
    if (caminho === '/accounts') return jsonResponse(200, CONTAS)
    if (caminho === '/transfers' && metodo === 'GET') {
      const corpo = typeof lista === 'function' ? (lista as (url: URL) => unknown)(url) : lista
      return jsonResponse(200, corpo)
    }
    if (caminho === '/transfers/detect' && metodo === 'POST') {
      return jsonResponse(200, PREVIA_VAZIA)
    }
    throw new Error(`rota não declarada no teste: ${metodo} ${url.pathname}${url.search}`)
  }
}

function pedidosDeTransferencias() {
  return fetchMock.mock.calls
    .map((chamada) => new URL(String(chamada[0]), 'https://app.invalido'))
    .filter((url) => url.pathname.endsWith('/transfers'))
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
  return router
}

describe('TransfersScreen', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    document.title = ''
    chamadas.length = 0
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('a rota existe, tem título de documento, e o <h1> recebe o foco', async () => {
    rotearApi(padrao(LISTA))
    renderTransferencias()

    const titulo = await screen.findByRole('heading', { level: 1, name: 'Transferências' })
    expect(document.title).toBe('Transferências · HomeFinance')
    expect(titulo).toHaveFocus()
    expect(screen.getByText('O que mudou de conta dentro da casa em setembro.')).toBeInTheDocument()
    // E o item de navegação leva a ela.
    expect(screen.getByRole('link', { name: 'Transferências' })).toHaveAttribute(
      'aria-current',
      'page',
    )
  })

  it('sem filtro: tabela de pares com a seta para quem mandou MAIS, e a lista de itens', async () => {
    rotearApi(padrao(LISTA))
    renderTransferencias()

    // O servidor mandou Nubank (A) → C6 (B) com líquido negativo; a linha
    // aponta a seta para a C6, que foi quem mandou mais.
    const link = await screen.findByRole('link', {
      name: 'Ver as transferências entre C6 e Nubank',
    })
    expect(link).toHaveAttribute(
      'href',
      `/transferencias?mes=2026-09&conta=${C6}&contraparte=${NUBANK}`,
    )
    const linhaDoPar = link.closest('tr') as HTMLElement
    const celulas = within(linhaDoPar).getAllByRole('cell')
    expect(celulas[1]).toHaveTextContent('2.500,00') // enviado, no sentido da seta
    expect(celulas[2]).toHaveTextContent('800,00') // recebido
    expect(celulas[3]).toHaveTextContent('1.700,00') // líquido, sem sinal
    expect(celulas[3]).not.toHaveTextContent('-')
    expect(celulas[4]).toHaveTextContent('4')

    // Itens: data curta, "de X para Y" em texto para o leitor de tela, valor
    // neutro sem sinal e a origem em palavra.
    expect(screen.getByText('Transferência enviada pelo Pix')).toBeInTheDocument()
    expect(screen.getByText('05/09')).toBeInTheDocument()
    // Duas vezes cada: na coluna Contas e na segunda linha da descrição, que
    // só o CSS mostra abaixo de 40rem.
    expect(screen.getAllByText('de Nubank para C6').length).toBeGreaterThan(0)
    expect(screen.getAllByText('de C6 para Nubank').length).toBeGreaterThan(0)
    expect(screen.getByText('1.500,00')).toBeInTheDocument()
    expect(screen.getAllByText('Importação').length).toBeGreaterThan(0)
    expect(screen.getAllByText('Manual').length).toBeGreaterThan(0)
    expect(screen.queryByText('+1.500,00')).not.toBeInTheDocument()

    // Tudo neutro: nenhum valor da tela leva tom de receita ou despesa.
    expect(document.querySelector('[data-tone="positivo"]')).toBeNull()
    expect(document.querySelector('[data-tone="negativo"]')).toBeNull()

    // Sem filtro não há painel do par nem "Outra conta".
    expect(screen.queryByText('Saldo no fim de setembro')).not.toBeInTheDocument()
    expect(screen.queryByLabelText('Outra conta')).not.toBeInTheDocument()
    expect(
      screen.getByText('2 transferências — é tudo o que existe no filtro.'),
    ).toBeInTheDocument()
  })

  it('com as duas contas: painel orientado pela conta do filtro, líquido = ±netCents e frase', async () => {
    rotearApi(padrao(LISTA))
    renderTransferencias(`/transferencias?mes=2026-09&conta=${NUBANK}&contraparte=${C6}`)

    // O painel aparece já em carregamento (com esqueletos); os números só
    // podem ser lidos quando a frase de direção existe — ela só nasce com o dado.
    await screen.findByText(/enviou/)

    // A query levou as duas contas.
    await waitFor(() => {
      const ultima = pedidosDeTransferencias().at(-1)
      expect(ultima?.searchParams.get('accountId')).toBe(NUBANK)
      expect(ultima?.searchParams.get('counterpartAccountId')).toBe(C6)
    })

    const termos = screen.getAllByRole('term').map((el) => el.textContent)
    expect(termos).toEqual(['Nubank → C6', 'C6 → Nubank', 'Líquido', 'Nubank', 'C6'])
    const definicoes = screen.getAllByRole('definition')
    expect(definicoes[0]).toHaveTextContent('800,00')
    expect(definicoes[1]).toHaveTextContent('2.500,00')
    // Filtro pela Nubank = A do servidor: o líquido mostrado é netCents (−1.700).
    expect(definicoes[2]).toHaveTextContent('-1.700,00')
    expect(screen.getByText(/enviou/).textContent).toMatch(
      /^C6 enviou R\$\s1\.700,00 a mais para Nubank em setembro\.$/,
    )
    // Saldo é posição: só ele é semântico.
    expect(definicoes[3]?.querySelector('[data-tone]')).toHaveAttribute('data-tone', 'positivo')
    expect(definicoes[4]?.querySelector('[data-tone]')).toHaveAttribute('data-tone', 'negativo')

    // Os dois seletores, o "e" entre eles, e a conta escolhida fora da segunda lista.
    expect(screen.getByLabelText('Conta')).toHaveValue(NUBANK)
    const outra = screen.getByLabelText('Outra conta')
    expect(outra).toHaveValue(C6)
    const opcoes = within(outra)
      .getAllByRole('option')
      .map((o) => o.textContent)
    expect(opcoes).toEqual(['Qualquer conta', 'C6', 'Carteira'])

    // Com o par, a tabela de pares dá lugar ao painel.
    expect(
      screen.queryByRole('link', { name: /Ver as transferências entre/ }),
    ).not.toBeInTheDocument()
  })

  it('com as duas contas invertidas, o líquido mostrado é −netCents', async () => {
    rotearApi(padrao(LISTA))
    renderTransferencias(`/transferencias?mes=2026-09&conta=${C6}&contraparte=${NUBANK}`)

    await screen.findByText(/enviou/)
    const definicoes = screen.getAllByRole('definition')
    expect(definicoes[2]).toHaveTextContent('+1.700,00')
    expect(screen.getByText(/enviou/).textContent).toMatch(
      /^C6 enviou R\$\s1\.700,00 a mais para Nubank em setembro\.$/,
    )
  })

  it('com uma conta só: saldo dela à direita da faixa e os pares que a envolvem', async () => {
    rotearApi(padrao(LISTA))
    renderTransferencias(`/transferencias?mes=2026-09&conta=${NUBANK}`)

    expect(await screen.findByText('Saldo da Nubank no fim de setembro')).toBeInTheDocument()
    expect(screen.getByText('4.120,00')).toBeInTheDocument()
    expect(screen.getByLabelText('Outra conta')).toHaveValue('')
    expect(
      screen.getByRole('link', { name: 'Ver as transferências entre C6 e Nubank' }),
    ).toBeInTheDocument()

    const ultima = pedidosDeTransferencias().at(-1)
    expect(ultima?.searchParams.get('accountId')).toBe(NUBANK)
    expect(ultima?.searchParams.has('counterpartAccountId')).toBe(false)
  })

  it('a URL recusa contraparte sem conta e conta que não é UUID', async () => {
    rotearApi(padrao(LISTA))
    renderTransferencias(`/transferencias?mes=2026-09&contraparte=${C6}`)
    await screen.findByText('Transferência enviada pelo Pix')

    for (const url of pedidosDeTransferencias()) {
      expect(url.searchParams.has('accountId')).toBe(false)
      expect(url.searchParams.has('counterpartAccountId')).toBe(false)
    }
    expect(screen.queryByLabelText('Outra conta')).not.toBeInTheDocument()
  })

  it("`?conta=' OR 1=1 --` não vira requisição", async () => {
    rotearApi(padrao(LISTA))
    renderTransferencias(`/transferencias?mes=2026-09&conta=' OR 1=1 --&contraparte=${C6}`)
    await screen.findByText('Transferência enviada pelo Pix')

    const urls = fetchMock.mock.calls.map((chamada) => String(chamada[0]))
    expect(urls.some((url) => url.includes('accountId'))).toBe(false)
    expect(urls.some((url) => url.includes('OR 1=1'))).toBe(false)
  })

  it('contraparte igual à conta é descartada antes de virar 400', async () => {
    rotearApi(padrao(LISTA))
    renderTransferencias(`/transferencias?mes=2026-09&conta=${NUBANK}&contraparte=${NUBANK}`)
    await screen.findByText('Saldo da Nubank no fim de setembro')

    const ultima = pedidosDeTransferencias().at(-1)
    expect(ultima?.searchParams.has('counterpartAccountId')).toBe(false)
  })

  it('escolher a conta e a outra conta escreve as duas na URL', async () => {
    rotearApi(padrao(LISTA))
    const router = renderTransferencias()

    await screen.findByText('Transferência enviada pelo Pix')
    await userEvent.selectOptions(screen.getByLabelText('Conta'), NUBANK)
    await waitFor(() => {
      expect(router.state.location.search).toMatchObject({ conta: NUBANK, mes: '2026-09' })
    })
    await userEvent.selectOptions(await screen.findByLabelText('Outra conta'), C6)
    await waitFor(() => {
      expect(router.state.location.search).toMatchObject({ conta: NUBANK, contraparte: C6 })
    })

    // Voltar para "Todas as contas" apaga as duas.
    await userEvent.selectOptions(screen.getByLabelText('Conta'), '')
    await waitFor(() => {
      expect(router.state.location.search).toEqual({ mes: '2026-09' })
    })
  })

  it('vazio sem filtro orienta para a importação e para o reprocessamento', async () => {
    rotearApi(padrao(VAZIA))
    renderTransferencias()

    expect(
      await screen.findByText('Nenhuma transferência entre as suas contas em setembro de 2026.'),
    ).toBeInTheDocument()
    expect(screen.getByText(/o app as detecta pelas palavras-chave das contas/)).toBeInTheDocument()
    expect(
      screen.getByText(/Se os extratos já foram importados, reprocesse para reconhecer os pares/),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Importar extrato' })).toBeInTheDocument()
    // Dois botões "Reprocessar transferências": o do cabeçalho e o do vazio.
    expect(screen.getAllByRole('button', { name: 'Reprocessar transferências' })).toHaveLength(2)
    // Sem pares, a tabela de pares não é desenhada.
    expect(screen.queryByText('Pares de contas')).not.toBeInTheDocument()
  })

  it('o botão do cabeçalho abre o diálogo de reprocessar, que já pede a prévia do mês', async () => {
    rotearApi(padrao(LISTA))
    renderTransferencias()
    await screen.findByText('Transferência enviada pelo Pix')

    // Com dados, só existe o botão do cabeçalho — e o diálogo ainda não está
    // no DOM: ele só nasce na primeira abertura.
    const botoes = screen.getAllByRole('button', { name: 'Reprocessar transferências' })
    expect(botoes).toHaveLength(1)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()

    await userEvent.click(botoes[0] as HTMLElement)
    const dialogo = await screen.findByRole('dialog')
    expect(
      within(dialogo).getByRole('heading', { name: 'Reprocessar transferências' }),
    ).toBeInTheDocument()
    await waitFor(() => {
      const previas = chamadas.filter((c) => c.url.pathname.endsWith('/transfers/detect'))
      expect(previas).toHaveLength(1)
      expect(previas[0]?.corpo).toEqual({ month: '2026-09', dryRun: true })
    })
  })

  it('o botão do estado vazio também abre o diálogo', async () => {
    rotearApi(padrao(VAZIA))
    renderTransferencias()
    await screen.findByText('Nenhuma transferência entre as suas contas em setembro de 2026.')

    const doVazio = screen.getAllByRole('button', { name: 'Reprocessar transferências' })[1]
    await userEvent.click(doVazio as HTMLElement)
    const dialogo = await screen.findByRole('dialog')
    expect(
      within(dialogo).getByRole('heading', { name: 'Reprocessar transferências' }),
    ).toBeInTheDocument()
    // Prévia vazia: o diálogo diz que não há par e explica o que fazer.
    expect(
      await within(dialogo).findByText('Nenhum par para reconhecer em setembro de 2026.'),
    ).toBeInTheDocument()
  })

  it('vazio com uma conta e com o par oferecem a saída certa', async () => {
    rotearApi(padrao({ ...VAZIA, balances: [SALDOS[0]] }))
    const router = renderTransferencias(`/transferencias?mes=2026-09&conta=${NUBANK}`)

    expect(
      await screen.findByText('Nenhuma transferência envolvendo a Nubank em setembro.'),
    ).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Mostrar todas as contas' }))
    await waitFor(() => {
      expect(router.state.location.search).toEqual({ mes: '2026-09' })
    })
  })

  it('vazio com o par: sem painel do par, e "Mostrar todos os pares" limpa os dois filtros', async () => {
    rotearApi(padrao({ ...VAZIA, balances: SALDOS }))
    const router = renderTransferencias(
      `/transferencias?mes=2026-09&conta=${NUBANK}&contraparte=${C6}`,
    )

    expect(
      await screen.findByText('Nenhuma transferência entre Nubank e C6 em setembro.'),
    ).toBeInTheDocument()
    expect(screen.queryByText(/equilibraram/)).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Mostrar todos os pares' }))
    await waitFor(() => {
      expect(router.state.location.search).toEqual({ mes: '2026-09' })
    })
  })

  it('erro mostra o aviso da tela com "Tentar de novo"', async () => {
    let tentativas = 0
    rotearApi((_metodo, url) => {
      const caminho = url.pathname.replace('/api/v1', '')
      if (caminho === '/me') return jsonResponse(200, SESSAO)
      if (caminho === '/accounts') return jsonResponse(200, CONTAS)
      tentativas += 1
      return tentativas === 1
        ? jsonResponse(500, { error: { code: 'INTERNAL', message: 'x' } })
        : jsonResponse(200, LISTA)
    })
    renderTransferencias()

    expect(
      await screen.findByText('Não foi possível carregar as transferências.'),
    ).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Tentar de novo' }))
    expect(await screen.findByText('Transferência enviada pelo Pix')).toBeInTheDocument()
  })

  it('carrega mais por cursor e anuncia quantas linhas chegaram', async () => {
    rotearApi(
      padrao((url: URL) => {
        const cursor = url.searchParams.get('cursor')
        if (cursor === null) {
          return { ...LISTA, items: [item()], nextCursor: 'cursor-2', pairs: [PAR_NUBANK_C6] }
        }
        return {
          ...LISTA,
          items: [item({ groupId: 'g-9', description: 'Segunda' })],
          nextCursor: null,
        }
      }),
    )
    renderTransferencias()

    expect(await screen.findByText('Mostrando 1 de 4 transferências')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Carregar mais 3' }))

    expect(await screen.findByText('Segunda')).toBeInTheDocument()
    expect(screen.getByText('Mais 1 transferência carregada. 2 de 4.')).toBeInTheDocument()
    expect(pedidosDeTransferencias().at(-1)?.searchParams.get('cursor')).toBe('cursor-2')
  })
})

describe('normalizarPar', () => {
  it('aponta a seta para quem mandou mais e deixa o líquido sem sinal', () => {
    const normalizado = normalizarPar(PAR_NUBANK_C6)
    expect(normalizado.de.nome).toBe('C6')
    expect(normalizado.para.nome).toBe('Nubank')
    expect(normalizado.enviadoCents).toBe(250_000)
    expect(normalizado.recebidoCents).toBe(80_000)
    expect(normalizado.liquidoCents).toBe(170_000)
  })

  it('mantém a ordem do servidor quando A mandou mais ou no empate', () => {
    const aMandouMais = normalizarPar({
      ...PAR_NUBANK_C6,
      aToBCents: 300,
      bToACents: 100,
      netCents: 200,
    })
    expect(aMandouMais.de.nome).toBe('Nubank')
    expect(aMandouMais.liquidoCents).toBe(200)

    const empate = normalizarPar({ ...PAR_NUBANK_C6, aToBCents: 100, bToACents: 100, netCents: 0 })
    expect(empate.de.nome).toBe('Nubank')
    expect(empate.liquidoCents).toBe(0)
    expect(Object.is(empate.liquidoCents, -0)).toBe(false)
  })
})
