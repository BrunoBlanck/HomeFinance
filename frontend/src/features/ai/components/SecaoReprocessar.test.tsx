import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { AutoCategorizeResult, TransferDetectResult } from '@/api/types'
import type { JanelaDeTrabalho } from '../janela'
import { FRASE_JANELA_MUDOU } from '../secoes'
import { SecaoReprocessar } from './SecaoReprocessar'

const fetchMock = vi.fn()

const JANELA: JanelaDeTrabalho = {
  fromMonth: '2026-07',
  toMonth: '2026-09',
  meses: ['2026-07', '2026-08', '2026-09'],
}

const OUTRA_JANELA: JanelaDeTrabalho = {
  fromMonth: '2026-09',
  toMonth: '2026-09',
  meses: ['2026-09'],
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function deteccao(
  month: string,
  parcial: Partial<TransferDetectResult> = {},
): TransferDetectResult {
  return { month, paired: 0, unpaired: 0, items: [], unpairedItems: [], ...parcial }
}

function categorizacao(
  month: string,
  parcial: Partial<AutoCategorizeResult> = {},
): AutoCategorizeResult {
  return { month, categorized: 0, unmatched: 0, items: [], unmatchedItems: [], ...parcial }
}

/** As prévias fixas: 1 + 2 + 0 pares, 2 sem par (julho e setembro), 12 + 18 +
 *  12 categorizados, 4 + 5 + 3 sem categoria. A execução devolve números
 *  DIFERENTES da prévia (2 + 1 + 0 pares, 10 + 18 + 12 categorizados), para
 *  o teste provar que o resultado vem das respostas de execução. */
const PREVIA_DETECT: Record<string, Partial<TransferDetectResult>> = {
  '2026-07': {
    paired: 1,
    unpaired: 1,
    unpairedItems: [
      {
        id: '018f0000-0000-7000-8000-0000000000a1',
        kind: 'expense',
        accountId: '018f0000-0000-7000-8000-00000000c001',
        accountName: 'Nubank',
        occurredOn: '2026-07-31',
        amountCents: 15000,
        description: 'Pix enviado - Bruno',
        matchedKeyword: 'inter',
        reason: 'no_mirror',
      },
    ],
  },
  '2026-08': { paired: 2 },
  '2026-09': {
    paired: 0,
    unpaired: 1,
    unpairedItems: [
      {
        id: '018f0000-0000-7000-8000-0000000000a2',
        kind: 'income',
        accountId: '018f0000-0000-7000-8000-00000000c002',
        accountName: 'Inter',
        occurredOn: '2026-09-02',
        amountCents: 20000,
        description: 'Pix recebido - Bruno',
        matchedKeyword: 'nubank',
        reason: 'no_mirror',
      },
    ],
  },
}
const PREVIA_CATEGORIZE: Record<string, Partial<AutoCategorizeResult>> = {
  '2026-07': { categorized: 12, unmatched: 4 },
  '2026-08': { categorized: 18, unmatched: 5 },
  '2026-09': { categorized: 12, unmatched: 3 },
}
const REAL_DETECT: Record<string, number> = { '2026-07': 2, '2026-08': 1, '2026-09': 0 }
const REAL_CATEGORIZE: Record<string, number> = { '2026-07': 10, '2026-08': 18, '2026-09': 12 }

type Corpo = { month: string; dryRun: boolean }

type Rotas = {
  detect?: (corpo: Corpo) => Response | Promise<Response> | null
  categorize?: (corpo: Corpo) => Response | Promise<Response> | null
}

/** Roteia o `fetch` pelo caminho e pelo corpo. Cada rota devolve a prévia ou
 *  o resultado real do mês conforme `dryRun`; um `override` pode interceptar
 *  (para o 409, o 429…) devolvendo uma resposta, ou `null` para o padrão. */
function rotearApi(rotas: Rotas = {}) {
  fetchMock.mockImplementation((entrada: string, init?: RequestInit) => {
    const url = new URL(String(entrada), 'https://app.invalido')
    const corpo = JSON.parse(String(init?.body)) as Corpo
    if (url.pathname.endsWith('/transfers/detect')) {
      const especial = rotas.detect?.(corpo)
      if (especial) return Promise.resolve(especial)
      return Promise.resolve(
        jsonResponse(
          200,
          corpo.dryRun
            ? deteccao(corpo.month, PREVIA_DETECT[corpo.month])
            : deteccao(corpo.month, { paired: REAL_DETECT[corpo.month] ?? 0 }),
        ),
      )
    }
    if (url.pathname.endsWith('/transactions/auto-categorize')) {
      const especial = rotas.categorize?.(corpo)
      if (especial) return Promise.resolve(especial)
      return Promise.resolve(
        jsonResponse(
          200,
          corpo.dryRun
            ? categorizacao(corpo.month, PREVIA_CATEGORIZE[corpo.month])
            : categorizacao(corpo.month, { categorized: REAL_CATEGORIZE[corpo.month] ?? 0 }),
        ),
      )
    }
    return Promise.resolve(jsonResponse(404, { error: { code: 'NOT_FOUND', message: '' } }))
  })
}

/** Cada chamada como `rota:mês:previa|real`, na ORDEM em que saiu. */
function sequenciaDeChamadas(): string[] {
  return fetchMock.mock.calls.map((chamada) => {
    const url = new URL(String(chamada[0]), 'https://app.invalido')
    const corpo = JSON.parse(String((chamada[1] as RequestInit).body)) as Corpo
    const rota = url.pathname.endsWith('/transfers/detect') ? 'detect' : 'categorize'
    return `${rota}:${corpo.month}:${corpo.dryRun ? 'previa' : 'real'}`
  })
}

let trocarJanelaExterno: ((janela: JanelaDeTrabalho) => void) | null = null

/** A seção precisa do roteador (os links do resultado) e do QueryClient (a
 *  invalidação ao terminar). A janela vive num `useState` do host para o
 *  teste da regra de frescor trocá-la sem remontar a seção. */
async function renderSecao(janela: JanelaDeTrabalho = JANELA) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  function Host() {
    const [atual, setAtual] = useState(janela)
    trocarJanelaExterno = setAtual
    return <SecaoReprocessar janela={atual} />
  }
  const rootRoute = createRootRoute({ component: Host })
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  // O roteador monta a rota de forma assíncrona: a seção só existe depois.
  await screen.findByRole('heading', { level: 2, name: '3 · Reprocessar' })
  return {
    queryClient,
    trocarJanela: (proxima: JanelaDeTrabalho) => act(() => trocarJanelaExterno?.(proxima)),
  }
}

async function conferir(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole('button', { name: 'Conferir' }))
  await screen.findByRole('button', { name: 'Reprocessar julho, agosto e setembro' })
}

describe('SecaoReprocessar', () => {
  beforeEach(() => {
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('ociosa: o apoio, os meses, a ordem e Conferir — sem disabled, sem tabela', async () => {
    rotearApi()
    await renderSecao()

    const secao = screen.getByRole('region', { name: '3 · Reprocessar' })
    expect(secao).toHaveTextContent(
      'Palavra-chave nova não mexe sozinha no que já está gravado. Aqui ela passa a valer.',
    )
    expect(secao).toHaveTextContent('Julho, agosto e setembro.')
    expect(secao).toHaveTextContent(
      'Primeiro as transferências, em todos os meses; depois a categorização.',
    )
    expect(within(secao).getByRole('button', { name: 'Conferir' })).toBeInTheDocument()
    expect(within(secao).queryByRole('table')).not.toBeInTheDocument()
    expect(within(secao).queryByRole('status')).not.toBeInTheDocument()
    expect(document.querySelector('[disabled]')).toBeNull()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  /** Aceite 37 na tela: seis prévias, uma frase consolidada que é a soma. */
  it('Conferir: seis chamadas com dryRun, a frase consolidada bate com a soma e a tabela lista os meses', async () => {
    // A prévia de julho fica presa até o teste soltar: é o que deixa o estado
    // "conferindo" visível no meio do caminho.
    let liberar: (() => void) | null = null
    const presa = new Promise<void>((resolve) => {
      liberar = resolve
    })
    rotearApi({
      detect: (corpo) =>
        corpo.month === '2026-07'
          ? presa.then(() => jsonResponse(200, deteccao('2026-07', PREVIA_DETECT['2026-07'])))
          : null,
    })
    const user = userEvent.setup()
    await renderSecao()

    await user.click(screen.getByRole('button', { name: 'Conferir' }))
    // Dois status neste instante: a frase da seção e o "Carregando…" sr-only
    // da própria DataTable (comportamento do componente base).
    expect(screen.getByText('Conferindo julho, agosto e setembro…')).toHaveAttribute(
      'role',
      'status',
    )
    expect(screen.getByRole('button', { name: 'Conferir' })).toHaveAttribute('aria-busy', 'true')
    expect(document.querySelector('[aria-busy="true"] table')).not.toBeNull()
    ;(liberar as (() => void) | null)?.()

    await screen.findByRole('button', { name: 'Reprocessar julho, agosto e setembro' })

    const chamadas = sequenciaDeChamadas()
    expect(chamadas).toHaveLength(6)
    expect(chamadas.every((c) => c.endsWith(':previa'))).toBe(true)
    for (const mes of JANELA.meses) {
      expect(chamadas).toContain(`detect:${mes}:previa`)
      expect(chamadas).toContain(`categorize:${mes}:previa`)
    }

    expect(screen.getByRole('status')).toHaveTextContent(
      '3 pares de transferência · 42 lançamentos categorizados · 12 seguem sem categoria.',
    )

    const tabela = screen.getByRole('table', { name: 'Prévia do reprocessamento por mês' })
    const linhas = within(tabela).getAllByRole('row').slice(1)
    expect(linhas.map((linha) => linha.textContent)).toEqual([
      'Julho de 20261124',
      'Agosto de 20262185',
      'Setembro de 20260123',
    ])
    expect(within(tabela).getByRole('columnheader', { name: 'Pares' })).toBeInTheDocument()
    expect(within(tabela).getByRole('columnheader', { name: 'Sem categoria' })).toBeInTheDocument()

    // Os sem par, fechados, com a copy do diálogo de /transferencias.
    const detalhes = screen.getByText('Ver os 2 lançamentos sem par e o motivo').closest('details')
    expect(detalhes).not.toBeNull()
    expect(detalhes).not.toHaveAttribute('open')
    await user.click(screen.getByText('Ver os 2 lançamentos sem par e o motivo'))
    expect(detalhes).toHaveTextContent(
      'A descrição bate com uma palavra-chave de conta, mas a outra perna não está gravada em nenhuma conta da casa. Importe o extrato da outra conta e reprocesse.',
    )
    const semPar = within(detalhes as HTMLElement).getByRole('table')
    expect(semPar).toHaveTextContent('Pix enviado - Bruno')
    expect(semPar).toHaveTextContent('saiu de Nubank')
    expect(semPar).toHaveTextContent('entrou em Inter')
    expect(within(semPar).getAllByText('sem a outra perna gravada')).toHaveLength(2)
    // Esta tela não tem dinheiro: nenhum valor na lista.
    expect(semPar).not.toHaveTextContent('150,00')
    expect(document.querySelector('[disabled]')).toBeNull()
  })

  it('zero em tudo: a frase de "nada a reprocessar" e o botão com aria-disabled, nunca disabled', async () => {
    rotearApi({
      detect: (corpo) => jsonResponse(200, deteccao(corpo.month)),
      categorize: (corpo) => jsonResponse(200, categorizacao(corpo.month)),
    })
    const user = userEvent.setup()
    await renderSecao()

    await user.click(screen.getByRole('button', { name: 'Conferir' }))
    const botao = await screen.findByRole('button', { name: 'Nada a reprocessar' })
    expect(botao).toHaveAttribute('aria-disabled', 'true')
    expect(botao).not.toBeDisabled()
    expect(screen.getByRole('status')).toHaveTextContent(
      'Nada a reprocessar — não há par para reconhecer nem lançamento sem categoria.',
    )
    expect(screen.queryByText(/sem par e o motivo/)).not.toBeInTheDocument()

    // O clique morre no guarda: nenhuma chamada real sai.
    await user.click(botao)
    expect(sequenciaDeChamadas().filter((c) => c.endsWith(':real'))).toHaveLength(0)
    expect(document.querySelector('[disabled]')).toBeNull()
  })

  /** Aceite 38: a ORDEM das chamadas reais é transferências em todos os meses,
   *  depois categorização em todos os meses. E o resultado é o das respostas
   *  de execução — que aqui são diferentes das da prévia de propósito. */
  it('Reprocessar: transferências (todos os meses) → categorização (todos os meses), e o total real', async () => {
    rotearApi()
    const user = userEvent.setup()
    await renderSecao()
    await conferir(user)

    await user.click(screen.getByRole('button', { name: 'Reprocessar julho, agosto e setembro' }))
    await screen.findByText(
      'Reprocessado — 3 pares de transferência e 40 lançamentos categorizados.',
    )

    expect(sequenciaDeChamadas().filter((c) => c.endsWith(':real'))).toEqual([
      'detect:2026-07:real',
      'detect:2026-08:real',
      'detect:2026-09:real',
      'categorize:2026-07:real',
      'categorize:2026-08:real',
      'categorize:2026-09:real',
    ])

    // O registro do progresso: uma frase por chamada, na ordem, no <output>.
    const saida = document.querySelector('output') as HTMLElement
    expect(saida).toHaveAttribute('aria-live', 'polite')
    expect(saida.textContent).toBe(
      [
        'Julho: 2 pares.',
        'Agosto: 1 par.',
        'Setembro: 0 pares.',
        'Julho: 10 categorizados.',
        'Agosto: 18 categorizados.',
        'Setembro: 12 categorizados.',
        'Reprocessado — 3 pares de transferência e 40 lançamentos categorizados.',
      ].join(''),
    )

    // A tabela de andamento: tudo feito, uma célula por etapa.
    const andamento = screen.getByRole('table', { name: 'Andamento do reprocessamento por mês' })
    expect(within(andamento).getAllByText('feito')).toHaveLength(6)

    // O <dl> com os números da EXECUÇÃO (a prévia dizia 42 categorizados).
    const lista = document.querySelector('dl') as HTMLElement
    expect(lista).toHaveTextContent('Pares de transferência3')
    expect(lista).toHaveTextContent('Lançamentos categorizados40')

    // Os dois links, no ÚLTIMO mês da janela.
    expect(screen.getByRole('link', { name: 'Ver as transferências' })).toHaveAttribute(
      'href',
      '/transferencias?mes=2026-09',
    )
    expect(screen.getByRole('link', { name: 'Ver os lançamentos' })).toHaveAttribute(
      'href',
      '/lancamentos?mes=2026-09',
    )
    // Sem toast, sem disabled; a saída é conferir de novo.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Conferir de novo' })).toBeInTheDocument()
    expect(document.querySelector('[disabled]')).toBeNull()
  })

  it('ao terminar, invalida transferências, lançamentos e contas — nunca o prompt', async () => {
    rotearApi()
    const user = userEvent.setup()
    const { queryClient } = await renderSecao()
    const invalidar = vi.spyOn(queryClient, 'invalidateQueries')
    await conferir(user)

    await user.click(screen.getByRole('button', { name: 'Reprocessar julho, agosto e setembro' }))
    await screen.findByText(/^Reprocessado/)

    const chaves = invalidar.mock.calls.map((chamada) => JSON.stringify(chamada[0]?.queryKey))
    expect(chaves).toContain('["transfers"]')
    expect(chaves).toContain('["transactions"]')
    expect(chaves).toContain('["accounts"]')
    expect(chaves.some((c) => c.includes('"ai"'))).toBe(false)
  })

  /** Aceite 44: 409 no segundo mês da fase 2. As cinco chamadas anteriores
   *  saíram, NENHUMA depois do conflito, a tela diz o que ficou e pede prévia
   *  nova. */
  it('409 na categorização de agosto: para tudo, mostra o que ficou e pede prévia nova', async () => {
    rotearApi({
      categorize: (corpo) =>
        !corpo.dryRun && corpo.month === '2026-08'
          ? jsonResponse(409, { error: { code: 'CONFLICT', message: '' } })
          : null,
    })
    const user = userEvent.setup()
    await renderSecao()
    await conferir(user)

    await user.click(screen.getByRole('button', { name: 'Reprocessar julho, agosto e setembro' }))
    const alerta = await screen.findByRole('alert')

    // 6 da prévia + 5 da execução, e nem uma a mais.
    const reais = sequenciaDeChamadas().filter((c) => c.endsWith(':real'))
    expect(reais).toEqual([
      'detect:2026-07:real',
      'detect:2026-08:real',
      'detect:2026-09:real',
      'categorize:2026-07:real',
      'categorize:2026-08:real',
    ])
    expect(fetchMock).toHaveBeenCalledTimes(11)

    expect(alerta).toHaveTextContent('O estado mudou no meio do reprocessamento.')
    expect(alerta).toHaveTextContent(
      'As transferências de julho, agosto e setembro foram aplicadas e continuam aplicadas. A categorização de julho também. A de agosto não foi, e a de setembro não chegou a rodar. Confira de novo antes de seguir.',
    )
    expect(alerta).toHaveTextContent(
      'Na prévia nova, o que já foi aplicado volta com 0 — é a prova de que está feito.',
    )

    // A tabela fica como registro, palavra por célula.
    const andamento = screen.getByRole('table', { name: 'Andamento do reprocessamento por mês' })
    const linhas = within(andamento).getAllByRole('row').slice(1)
    expect(linhas.map((linha) => linha.textContent)).toEqual([
      'Julho de 2026feitofeito',
      'Agosto de 2026feitonão aplicado',
      'Setembro de 2026feitonão chegou a rodar',
    ])
    // O registro do progresso para na última chamada concluída.
    expect((document.querySelector('output') as HTMLElement).textContent).toBe(
      ['Julho: 2 pares.', 'Agosto: 1 par.', 'Setembro: 0 pares.', 'Julho: 10 categorizados.'].join(
        '',
      ),
    )
    // Nenhum "Reprocessar" e nenhum "Tentar de novo": a única saída é a prévia nova.
    expect(screen.queryByRole('button', { name: /^Reprocessar/ })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Tentar de novo' })).not.toBeInTheDocument()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()

    // Conferir de novo roda a prévia inteira (mais seis chamadas com dryRun).
    await user.click(within(alerta).getByRole('button', { name: 'Conferir de novo' }))
    await screen.findByRole('button', { name: 'Reprocessar julho, agosto e setembro' })
    expect(fetchMock).toHaveBeenCalledTimes(17)
    expect(
      sequenciaDeChamadas()
        .slice(11)
        .every((c) => c.endsWith(':previa')),
    ).toBe(true)
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(document.querySelector('[disabled]')).toBeNull()
  })

  it('429 na fase 1: para, diz o que ficou com a frase própria, e não oferece "tentar de novo"', async () => {
    rotearApi({
      detect: (corpo) =>
        !corpo.dryRun && corpo.month === '2026-08'
          ? jsonResponse(429, { error: { code: 'RATE_LIMITED', message: '' } })
          : null,
    })
    const user = userEvent.setup()
    await renderSecao()
    await conferir(user)

    await user.click(screen.getByRole('button', { name: 'Reprocessar julho, agosto e setembro' }))
    const alerta = await screen.findByRole('alert')

    expect(sequenciaDeChamadas().filter((c) => c.endsWith(':real'))).toEqual([
      'detect:2026-07:real',
      'detect:2026-08:real',
    ])
    expect(alerta).toHaveTextContent('O reprocessamento parou.')
    expect(alerta).toHaveTextContent(
      'As transferências de julho foram aplicadas e continuam aplicadas. As de agosto não foram aplicadas, e as de setembro não chegaram a rodar. A categorização não chegou a rodar. Confira de novo antes de seguir.',
    )
    expect(alerta).toHaveTextContent(
      'Muitas operações seguidas. Espere um minuto e confira de novo.',
    )
    expect(within(alerta).getByRole('button', { name: 'Conferir de novo' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Tentar de novo' })).not.toBeInTheDocument()
  })

  it('idempotente: execução que não muda nada diz "Nada mudou"', async () => {
    rotearApi({
      detect: (corpo) => (corpo.dryRun ? null : jsonResponse(200, deteccao(corpo.month))),
      categorize: (corpo) => (corpo.dryRun ? null : jsonResponse(200, categorizacao(corpo.month))),
    })
    const user = userEvent.setup()
    await renderSecao()
    await conferir(user)

    await user.click(screen.getByRole('button', { name: 'Reprocessar julho, agosto e setembro' }))
    await screen.findByText(
      'Nada mudou — não havia par para reconhecer nem lançamento sem categoria.',
    )
    expect(screen.queryByRole('definition')).not.toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Ver as transferências' })).toBeInTheDocument()
  })

  /** Regra de frescor: a mesma da seção 2, com a mesma frase. */
  it('trocar a janela descarta a prévia e avisa; o resultado também some', async () => {
    rotearApi()
    const user = userEvent.setup()
    const { trocarJanela } = await renderSecao()
    await conferir(user)
    const nomeDaTabela = 'Prévia do reprocessamento por mês'
    expect(screen.getByRole('table', { name: nomeDaTabela })).toBeInTheDocument()

    trocarJanela(OUTRA_JANELA)

    expect(screen.queryByRole('table', { name: nomeDaTabela })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /^Reprocessar/ })).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent(FRASE_JANELA_MUDOU)
    expect(screen.getByRole('button', { name: 'Conferir' })).toBeInTheDocument()
    expect(screen.getByRole('region', { name: '3 · Reprocessar' })).toHaveTextContent('Setembro.')

    // Conferir de novo na janela nova: só um mês, duas chamadas.
    await user.click(screen.getByRole('button', { name: 'Conferir' }))
    await screen.findByRole('button', { name: 'Reprocessar setembro' })
    expect(sequenciaDeChamadas().slice(6)).toEqual(
      expect.arrayContaining(['detect:2026-09:previa', 'categorize:2026-09:previa']),
    )
    expect(sequenciaDeChamadas()).toHaveLength(8)
    expect(screen.queryByText(FRASE_JANELA_MUDOU)).not.toBeInTheDocument()

    // E o RESULTADO também é descartado ao trocar a janela.
    await user.click(screen.getByRole('button', { name: 'Reprocessar setembro' }))
    await screen.findByText(/^Reprocessado/)
    trocarJanela(JANELA)
    expect(screen.queryByText(/^Reprocessado/)).not.toBeInTheDocument()
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent(FRASE_JANELA_MUDOU)
  })

  it('a prévia que falha: Alert com "Tentar de novo" para rede/500, sem ação para 429', async () => {
    rotearApi({
      categorize: (corpo) =>
        corpo.month === '2026-08'
          ? jsonResponse(500, { error: { code: 'INTERNAL', message: '' } })
          : null,
    })
    const user = userEvent.setup()
    await renderSecao()

    await user.click(screen.getByRole('button', { name: 'Conferir' }))
    const alerta = await screen.findByRole('alert')
    expect(alerta).toHaveTextContent('Não foi possível conferir.')
    expect(within(alerta).getByRole('button', { name: 'Tentar de novo' })).toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()

    rotearApi({
      categorize: () => jsonResponse(429, { error: { code: 'RATE_LIMITED', message: '' } }),
    })
    await user.click(within(alerta).getByRole('button', { name: 'Tentar de novo' }))
    await waitFor(() =>
      expect(screen.getByRole('alert')).toHaveTextContent(
        'Muitas operações seguidas. Espere um minuto e confira de novo.',
      ),
    )
    expect(screen.queryByRole('button', { name: 'Tentar de novo' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Conferir' })).toBeInTheDocument()
  })

  it('o título da seção é focável por script — "Ir para Reprocessar" chega aqui', async () => {
    rotearApi()
    await renderSecao()
    const titulo = screen.getByRole('heading', { level: 2, name: '3 · Reprocessar' })
    expect(titulo).toHaveAttribute('id', 'ia-secao-reprocessar')
    expect(titulo).toHaveAttribute('tabindex', '-1')
    titulo.focus()
    expect(titulo).toHaveFocus()
  })

  // ---------------------------------------------------------- QA da E9c

  /** Requisição repetida (caso de abuso do QA): dois cliques seguidos em
   *  "Reprocessar" — o duplo clique de quem acha que o primeiro não pegou —
   *  disparam UM plano, não dois. Dois planos em paralelo seriam doze
   *  chamadas de gravação sobre os mesmos meses, com a segunda detecção
   *  correndo contra a primeira (409 ou, pior, pares convertidos duas vezes na
   *  contagem da tela). O primeiro `detect` fica preso até o teste soltar: é
   *  nessa janela que o segundo clique chega. */
  it('dois cliques seguidos em Reprocessar disparam um plano só — seis chamadas reais, nunca doze', async () => {
    let liberar: (() => void) | null = null
    const presa = new Promise<void>((resolve) => {
      liberar = resolve
    })
    rotearApi({
      detect: (corpo) =>
        !corpo.dryRun && corpo.month === '2026-07'
          ? presa.then(() => jsonResponse(200, deteccao('2026-07', { paired: 2 })))
          : null,
    })
    const user = userEvent.setup()
    await renderSecao()
    await conferir(user)

    const botao = screen.getByRole('button', { name: 'Reprocessar julho, agosto e setembro' })
    await user.dblClick(botao)
    // Enquanto a primeira chamada está presa: exatamente UMA gravação saiu, o
    // botão se anuncia ocupado e o clique a mais morreu no guarda.
    expect(sequenciaDeChamadas().filter((c) => c.endsWith(':real'))).toEqual([
      'detect:2026-07:real',
    ])
    expect(botao).toHaveAttribute('aria-busy', 'true')
    await user.click(botao)
    expect(sequenciaDeChamadas().filter((c) => c.endsWith(':real'))).toHaveLength(1)
    ;(liberar as (() => void) | null)?.()

    await screen.findByText(/^Reprocessado/)
    expect(sequenciaDeChamadas().filter((c) => c.endsWith(':real'))).toEqual([
      'detect:2026-07:real',
      'detect:2026-08:real',
      'detect:2026-09:real',
      'categorize:2026-07:real',
      'categorize:2026-08:real',
      'categorize:2026-09:real',
    ])
    expect(fetchMock).toHaveBeenCalledTimes(12)
    // E o `<output>` registra cada passo UMA vez.
    expect((document.querySelector('output') as HTMLElement).textContent).toBe(
      [
        'Julho: 2 pares.',
        'Agosto: 1 par.',
        'Setembro: 0 pares.',
        'Julho: 10 categorizados.',
        'Agosto: 18 categorizados.',
        'Setembro: 12 categorizados.',
        'Reprocessado — 3 pares de transferência e 40 lançamentos categorizados.',
      ].join(''),
    )
  })

  /** Eco divergente (ADR-030): o servidor responde com OUTRO mês na execução.
   *  A tela não atribui o número ao mês errado: para no passo, com "sem
   *  resposta" (não se sabe o que foi gravado), e nenhuma chamada sai depois. */
  it('resposta de execução com o mês trocado para o plano como "sem resposta", sem chamada seguinte', async () => {
    rotearApi({
      detect: (corpo) =>
        !corpo.dryRun && corpo.month === '2026-08'
          ? jsonResponse(200, deteccao('2026-09', { paired: 5 }))
          : null,
    })
    const user = userEvent.setup()
    await renderSecao()
    await conferir(user)

    await user.click(screen.getByRole('button', { name: 'Reprocessar julho, agosto e setembro' }))
    const alerta = await screen.findByRole('alert')
    expect(sequenciaDeChamadas().filter((c) => c.endsWith(':real'))).toEqual([
      'detect:2026-07:real',
      'detect:2026-08:real',
    ])
    expect(alerta).toHaveTextContent(
      'As transferências de julho foram aplicadas e continuam aplicadas. As de agosto ficaram sem resposta, e as de setembro não chegaram a rodar. A categorização não chegou a rodar. Confira de novo antes de seguir.',
    )
    const andamento = screen.getByRole('table', { name: 'Andamento do reprocessamento por mês' })
    expect(within(andamento).getAllByText('sem resposta')).toHaveLength(1)
    // O "5 pares" de um mês trocado NUNCA entra no registro.
    expect((document.querySelector('output') as HTMLElement).textContent).toBe('Julho: 2 pares.')
  })
})
