import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'
import { MSG_CATEGORIA_RECUSADA } from '@/lib/errors'
import { fraseDosOutros, proximaLacuna } from './AtalhoDeCategoria'

/** O atalho de categorização vive DENTRO da tabela de `/lancamentos`: o botão
 *  numa célula, o editor numa linha de detalhe, o foco pulando de lacuna em
 *  lacuna. Testá-lo fora da rota seria testar outra coisa — por isso o
 *  harness é o da tela, com a API mockada rota a rota. */

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

type CategoriaFalsa = {
  id: string
  name: string
  kind: 'income' | 'expense' | 'investment' | 'redemption'
  parentId?: string | null
  keywords?: string[]
  children?: CategoriaFalsa[]
}

function categoria(over: CategoriaFalsa) {
  return {
    parentId: null,
    keywords: [],
    children: [],
    archivedAt: null,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...over,
  }
}

const ALIMENTACAO = categoria({
  id: 'cat-alimentacao',
  name: 'Alimentação',
  kind: 'expense',
  keywords: ['padaria'],
})

const ARVORE = {
  expense: [
    ALIMENTACAO,
    categoria({ id: 'cat-padaria', name: 'Padaria', kind: 'expense', keywords: ['mercado'] }),
    categoria({
      id: 'grp-transporte',
      name: 'Transporte',
      kind: 'expense',
      children: [
        categoria({ id: 'cat-uber', name: 'Uber', kind: 'expense', parentId: 'grp-transporte' }),
      ],
    }),
  ],
  income: [categoria({ id: 'cat-salario', name: 'Salário', kind: 'income' })],
  // As naturezas da E7 (ADR-029a). Elas não formam um seletor próprio: o de
  // despesa carrega as de investimento junto, e o de receita as de resgate.
  investment: [
    categoria({
      id: 'grp-investimentos',
      name: 'Investimentos',
      kind: 'investment',
      children: [
        categoria({
          id: 'cat-cdb',
          name: 'CDB',
          kind: 'investment',
          parentId: 'grp-investimentos',
          keywords: ['cdb'],
        }),
      ],
    }),
  ],
  redemption: [categoria({ id: 'cat-resgates', name: 'Resgates', kind: 'redemption' })],
}

type Lancamento = Record<string, unknown> & { id: string }

function lancamento(over: Record<string, unknown> = {}): Lancamento {
  return {
    id: 'tx-1',
    kind: 'expense',
    accountId: CONTA_CORRENTE,
    accountName: 'Conta corrente',
    categoryId: null,
    categoryName: null,
    amountCents: 4590,
    description: 'Mercado do seu José',
    occurredOn: '2026-09-12',
    yearMonth: '2026-09',
    competenceMonth: '2026-09',
    transferGroupId: null,
    statementId: null,
    source: 'import',
    importBatchId: null,
    createdBy: SESSAO.user.id,
    createdAt: '2026-09-12T00:00:00Z',
    updatedAt: '2026-09-12T00:00:00Z',
    ...over,
  }
}

/** Três lacunas, um lançamento já categorizado e uma transferência. */
function lancamentosPadrao(): Lancamento[] {
  return [
    lancamento(),
    lancamento({ id: 'tx-2', description: 'Posto Ipiranga', amountCents: 20_000 }),
    lancamento({ id: 'tx-3', description: 'Farmácia Popular', amountCents: 3_250 }),
    lancamento({
      id: 'tx-ok',
      description: 'Pão de Açúcar',
      categoryId: 'cat-alimentacao',
      categoryName: 'Alimentação',
    }),
    lancamento({
      id: 'tx-transf',
      kind: 'transfer_out',
      description: 'Pix para Cartão',
      amountCents: 50_000,
      transferGroupId: 'grupo-1',
    }),
  ]
}

function resumo(lancamentos: readonly Lancamento[]) {
  const semCategoria = lancamentos.filter(
    (l) => l.categoryId === null && l.kind !== 'transfer_out' && l.kind !== 'transfer_in',
  ).length
  return {
    incomeCents: 0,
    expenseCents: 0,
    netCents: 0,
    count: lancamentos.length,
    uncategorizedCount: semCategoria,
  }
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

type Chamada = { metodo: string; caminho: string; corpo: Record<string, unknown> | null }

/** Um servidor de mentira com ESTADO: o `PATCH` altera a lista que o próximo
 *  `GET` devolve, como o de verdade. É isso que permite testar "o foco vai
 *  para a próxima lacuna depois que a lista refetchou". */
function servidor(opcoes: {
  lancamentos?: Lancamento[]
  categorias?: unknown
  aoPatchCategoria?: (id: string, corpo: Record<string, unknown>) => Response
  aoPatchLancamento?: (id: string, corpo: Record<string, unknown>) => Response | null
  aoAutoCategorizar?: (corpo: Record<string, unknown>) => Response
}) {
  const lancamentos = opcoes.lancamentos ?? lancamentosPadrao()
  const chamadas: Chamada[] = []

  fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    const completa = new URL(String(url), 'https://app.invalido')
    const caminho = completa.pathname.replace('/api/v1', '')
    const corpo = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : null
    chamadas.push({ metodo, caminho, corpo })

    if (caminho === '/me') return jsonResponse(200, SESSAO)
    if (caminho === '/accounts') return jsonResponse(200, CONTAS)
    if (caminho === '/categories') return jsonResponse(200, opcoes.categorias ?? ARVORE)
    if (caminho === '/transactions' && metodo === 'GET') {
      return jsonResponse(200, {
        items: lancamentos,
        nextCursor: null,
        summary: resumo(lancamentos),
      })
    }
    if (caminho === '/transactions/auto-categorize' && metodo === 'POST') {
      if (opcoes.aoAutoCategorizar) return opcoes.aoAutoCategorizar(corpo ?? {})
      // Por padrão categoriza TODAS as outras lacunas do mês.
      let categorizados = 0
      for (const item of lancamentos) {
        if (item.categoryId === null && item.kind === 'expense') {
          item.categoryId = 'cat-alimentacao'
          item.categoryName = 'Alimentação'
          categorizados += 1
        }
      }
      return jsonResponse(200, {
        categorized: categorizados,
        unmatched: 0,
        items: [],
        unmatchedItems: [],
      })
    }
    const patchCategoria = /^\/categories\/(.+)$/.exec(caminho)
    if (patchCategoria?.[1] && metodo === 'PATCH') {
      if (opcoes.aoPatchCategoria) return opcoes.aoPatchCategoria(patchCategoria[1], corpo ?? {})
      return jsonResponse(200, { ...ALIMENTACAO, keywords: corpo?.keywords })
    }
    const patchLancamento = /^\/transactions\/(.+)$/.exec(caminho)
    if (patchLancamento?.[1] && metodo === 'PATCH') {
      const especial = opcoes.aoPatchLancamento?.(patchLancamento[1], corpo ?? {})
      if (especial) return especial
      const alvo = lancamentos.find((item) => item.id === patchLancamento[1])
      if (!alvo) return jsonResponse(404, { error: { code: 'NOT_FOUND', message: 'nao' } })
      alvo.categoryId = corpo?.categoryId
      alvo.categoryName = corpo?.categoryId === 'cat-alimentacao' ? 'Alimentação' : 'Uber'
      return jsonResponse(200, alvo)
    }
    throw new Error(`rota não declarada no teste: ${metodo} ${caminho}${completa.search}`)
  })

  return {
    chamadas,
    lancamentos,
    /** Só as chamadas que ESCREVEM, na ordem — é a sequência (a) → (b) → (c). */
    escritas: () => chamadas.filter((c) => c.metodo !== 'GET'),
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

const NOME_DA_LACUNA_1 = /^Sem categoria\. Categorizar Mercado do seu José, 12\/09\/2026/
const NOME_DA_LACUNA_2 = /^Sem categoria\. Categorizar Posto Ipiranga/
const NOME_DA_LACUNA_3 = /^Sem categoria\. Categorizar Farmácia Popular/

/** A instância VISÍVEL do botão — no jsdom não há layout, então é a primeira
 *  do DOM (a da coluna), que é a que a tela usa acima de 40rem. */
function lacuna(nome: RegExp): HTMLButtonElement {
  const [primeira] = screen.getAllByRole('button', { name: nome })
  if (!primeira) throw new Error(`lacuna não encontrada: ${nome}`)
  return primeira as HTMLButtonElement
}

async function abrirEditor(nome: RegExp = NOME_DA_LACUNA_1) {
  const user = userEvent.setup()
  await user.click(lacuna(nome))
  const editor = await screen.findByRole('group', { name: /^Categorizar / })
  return { user, editor }
}

describe('AtalhoDeCategoria — o controle da célula', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('só a linha de receita/despesa sem categoria tem a lacuna; transferência e categorizada não', async () => {
    servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const botao = lacuna(NOME_DA_LACUNA_1)
    expect(botao).toHaveTextContent('Sem categoria')
    expect(botao).toHaveAttribute('aria-expanded', 'false')
    expect(botao).not.toHaveAttribute('aria-controls')

    // Duas instâncias por linha (coluna e secundária), uma visível por faixa.
    const instancias = screen.getAllByRole('button', { name: NOME_DA_LACUNA_1 })
    expect(instancias).toHaveLength(2)
    expect(instancias[0]).toHaveAttribute('id', 'atalho-categoria-tx-1-coluna')
    expect(instancias[1]).toHaveAttribute('id', 'atalho-categoria-tx-1-secundaria')
    expect(instancias[0]).toHaveAttribute('data-atalho', 'tx-1')

    // A transferência mostra a etiqueta; a categorizada, o nome — sem botão.
    expect(screen.getByText('Transferência')).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: /Categorizar Pix para Cartão/ }),
    ).not.toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: /Categorizar Pão de Açúcar/ }),
    ).not.toBeInTheDocument()
    expect(screen.getAllByText('Alimentação').length).toBeGreaterThan(0)
  })

  it('abre o editor na própria linha, com o foco no select e o confirmar aguardando a categoria', async () => {
    servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()

    // Disclosure: expandido e apontando para o editor, que é uma linha de
    // detalhe da tabela — dentro da <table>, não uma camada.
    const botao = lacuna(NOME_DA_LACUNA_1)
    expect(botao).toHaveAttribute('aria-expanded', 'true')
    expect(botao).toHaveAttribute('aria-controls', editor.id)
    expect(editor.closest('table')).not.toBeNull()
    expect(editor.closest('tr')?.className).toMatch(/detail/)

    const select = within(editor).getByRole('combobox', {
      name: /^Categoria de Mercado do seu José/,
    })
    expect(select).toHaveFocus()
    expect(within(select).getByRole('option', { name: 'Escolha a categoria' })).toBeInTheDocument()
    // Só da natureza da linha: despesa não vê "Salário".
    expect(within(select).queryByRole('option', { name: 'Salário' })).not.toBeInTheDocument()
    expect(within(select).getByRole('option', { name: 'Uber' })).toBeInTheDocument()

    // Nunca `disabled`: o rótulo explica, e o clique leva ao select.
    const confirmar = within(editor).getByRole('button', { name: 'Escolha uma categoria' })
    expect(confirmar).toHaveAttribute('aria-disabled', 'true')
    expect(confirmar).not.toBeDisabled()
    await user.click(within(editor).getByRole('button', { name: 'Cancelar' }))
    await user.click(lacuna(NOME_DA_LACUNA_1))
    const editor2 = await screen.findByRole('group', { name: /^Categorizar / })
    await user.click(within(editor2).getByRole('button', { name: 'Escolha uma categoria' }))
    expect(within(editor2).getByRole('combobox')).toHaveFocus()
  })

  it('Escape e Cancelar fecham sem gravar e devolvem o foco ao botão da célula', async () => {
    const { escritas } = servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.keyboard('{Escape}')
    expect(editor).not.toBeInTheDocument()
    expect(lacuna(NOME_DA_LACUNA_1)).toHaveFocus()
    expect(lacuna(NOME_DA_LACUNA_1)).toHaveAttribute('aria-expanded', 'false')

    const segunda = await abrirEditor()
    await segunda.user.click(within(segunda.editor).getByRole('button', { name: 'Cancelar' }))
    expect(segunda.editor).not.toBeInTheDocument()
    expect(lacuna(NOME_DA_LACUNA_1)).toHaveFocus()
    expect(escritas()).toHaveLength(0)
  })

  it('uma linha aberta por vez; clique fora não fecha', async () => {
    servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user } = await abrirEditor(NOME_DA_LACUNA_1)
    expect(screen.getByRole('group', { name: /^Categorizar Mercado/ })).toBeInTheDocument()

    await user.click(lacuna(NOME_DA_LACUNA_2))
    expect(screen.queryByRole('group', { name: /^Categorizar Mercado/ })).not.toBeInTheDocument()
    expect(screen.getByRole('group', { name: /^Categorizar Posto/ })).toBeInTheDocument()
    expect(lacuna(NOME_DA_LACUNA_1)).toHaveAttribute('aria-expanded', 'false')
    expect(lacuna(NOME_DA_LACUNA_2)).toHaveAttribute('aria-expanded', 'true')

    // Não é popover: clicar no título não fecha.
    await user.click(screen.getByRole('heading', { name: 'Lançamentos' }))
    expect(screen.getByRole('group', { name: /^Categorizar Posto/ })).toBeInTheDocument()
  })

  it('trocar o mês fecha o editor', async () => {
    servidor({})
    const router = renderLancamentos()
    await screen.findByText('Mercado do seu José')

    await abrirEditor()
    await act(() => router.navigate({ to: '/lancamentos', search: { mes: '2026-08' } }))
    await waitFor(() => {
      expect(screen.queryByRole('group', { name: /^Categorizar / })).not.toBeInTheDocument()
    })
  })
})

describe('AtalhoDeCategoria — só este lançamento', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('PATCH só no lançamento; a célula muda, o toast confirma e o foco vai à próxima lacuna', async () => {
    const { escritas } = servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')

    // As fichas aparecem com a categoria escolhida, soltas.
    expect(within(editor).getByText('Da próxima vez, reconhecer por')).toBeInTheDocument()
    const ficha = within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' })
    expect(ficha).toHaveAttribute('aria-pressed', 'false')
    // «jose» também; «padaria» não está na descrição; nada de dígitos.
    expect(
      within(editor).getByRole('button', { name: 'Reconhecer por «jose»' }),
    ).toBeInTheDocument()

    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))

    expect(await screen.findByText('Lançamento categorizado como Alimentação.')).toBeInTheDocument()
    expect(escritas()).toEqual([
      { metodo: 'PATCH', caminho: '/transactions/tx-1', corpo: { categoryId: 'cat-alimentacao' } },
    ])
    // O editor fechou e a linha mostra a categoria.
    expect(screen.queryByRole('group', { name: /^Categorizar / })).not.toBeInTheDocument()
    expect(screen.queryAllByRole('button', { name: NOME_DA_LACUNA_1 })).toHaveLength(0)
    const linha = screen.getByText('Mercado do seu José').closest('tr')
    expect(linha).not.toBeNull()
    expect(within(linha as HTMLElement).getAllByText('Alimentação').length).toBeGreaterThan(0)

    // A próxima lacuna é a próxima tarefa.
    await waitFor(() => expect(lacuna(NOME_DA_LACUNA_2)).toHaveFocus())
    // E a faixa vem do servidor: 3 → 2.
    expect(
      await screen.findByText(/2 lançamentos de setembro estão sem categoria/),
    ).toBeInTheDocument()
  })

  // O erro SEM tratamento próprio: toast, editor aberto, foco no confirmar.
  // (O 422 que aponta `categoryId` NÃO passa por aqui — ele tem caminho
  // próprio, testado no bloco da §13 mais abaixo.)
  it('erro no PATCH: toast, editor aberto e foco no confirmar', async () => {
    servidor({
      aoPatchLancamento: () =>
        jsonResponse(500, { error: { code: 'INTERNAL_ERROR', message: 'x' } }),
    })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))

    expect(
      await screen.findByText('Algo falhou do nosso lado. Tente de novo em instantes.'),
    ).toBeInTheDocument()
    expect(editor).toBeInTheDocument()
    expect(within(editor).getByRole('button', { name: 'Categorizar' })).toHaveFocus()
  })

  it('a última lacuna resolvida leva o foco ao título quando não sobra nenhuma', async () => {
    servidor({ lancamentos: [lancamento()] })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))

    await screen.findByText('Lançamento categorizado como Alimentação.')
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Lançamentos' })).toHaveFocus())
  })
})

describe('AtalhoDeCategoria — com palavra-chave', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('a ficha é alternância: no máximo uma pressionada, e o rótulo do confirmar cita a palavra', async () => {
    servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')

    const mercado = within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' })
    const jose = within(editor).getByRole('button', { name: 'Reconhecer por «jose»' })
    const dica = within(editor).getByRole('status')
    expect(dica).toBeEmptyDOMElement()

    await user.click(mercado)
    expect(mercado).toHaveAttribute('aria-pressed', 'true')
    expect(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «mercado»' }),
    ).toBeInTheDocument()
    expect(dica).toHaveTextContent(
      '«mercado» vira palavra-chave de Alimentação — vale para os outros lançamentos sem categoria de setembro e para as próximas importações.',
    )

    // Pressionar outra solta a primeira.
    await user.click(jose)
    expect(jose).toHaveAttribute('aria-pressed', 'true')
    expect(mercado).toHaveAttribute('aria-pressed', 'false')
    expect(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «jose»' }),
    ).toBeInTheDocument()

    // Pressionar a mesma solta; a dica esvazia; o rótulo volta.
    await user.click(jose)
    expect(jose).toHaveAttribute('aria-pressed', 'false')
    expect(within(editor).getByRole('button', { name: 'Categorizar' })).toBeInTheDocument()
    expect(dica).toBeEmptyDOMElement()

    // Trocar a categoria solta a ficha pressionada.
    await user.click(mercado)
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-uber')
    expect(
      within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }),
    ).toHaveAttribute('aria-pressed', 'false')
    expect(within(editor).getByRole('button', { name: 'Categorizar' })).toBeInTheDocument()
  })

  it('grava na ordem (a) categoria com a lista inteira → (b) lançamento → (c) auto-categorize no mês da tela', async () => {
    const { escritas, lancamentos } = servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }))
    await user.click(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «mercado»' }),
    )

    // O número do toast é o que (c) devolveu — os OUTROS dois do mês.
    expect(
      await screen.findByText(
        '«mercado» adicionada a Alimentação · mais 2 lançamentos de setembro categorizados.',
      ),
    ).toBeInTheDocument()

    expect(escritas()).toEqual([
      {
        metodo: 'PATCH',
        caminho: '/categories/cat-alimentacao',
        corpo: { keywords: ['padaria', 'mercado'] },
      },
      { metodo: 'PATCH', caminho: '/transactions/tx-1', corpo: { categoryId: 'cat-alimentacao' } },
      {
        metodo: 'POST',
        caminho: '/transactions/auto-categorize',
        corpo: { month: '2026-09', dryRun: false },
      },
    ])

    // Fechou; a lista refetchada não tem mais lacuna nenhuma (o servidor de
    // mentira categorizou as outras) — o foco vai ao título.
    await waitFor(() => {
      expect(screen.queryByRole('group', { name: /^Categorizar / })).not.toBeInTheDocument()
    })
    expect(lancamentos.every((l) => l.kind !== 'expense' || l.categoryId !== null)).toBe(true)
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Lançamentos' })).toHaveFocus())
    expect(screen.queryByText(/estão sem categoria/)).not.toBeInTheDocument()
  })

  it('o foco vai para a próxima lacuna que SOBROU depois do reprocessamento', async () => {
    const { lancamentos } = servidor({
      aoAutoCategorizar: () => {
        // Categoriza só a segunda linha: a terceira continua lacuna.
        const segunda = lancamentos.find((l) => l.id === 'tx-2')
        if (segunda) {
          segunda.categoryId = 'cat-alimentacao'
          segunda.categoryName = 'Alimentação'
        }
        return jsonResponse(200, { categorized: 1, unmatched: 1, items: [], unmatchedItems: [] })
      },
    })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }))
    await user.click(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «mercado»' }),
    )

    expect(
      await screen.findByText(
        '«mercado» adicionada a Alimentação · mais 1 lançamento de setembro categorizado.',
      ),
    ).toBeInTheDocument()
    // Não a segunda (já resolvida por (c)) — a terceira.
    await waitFor(() => expect(lacuna(NOME_DA_LACUNA_3)).toHaveFocus())
    expect(screen.queryAllByRole('button', { name: NOME_DA_LACUNA_2 })).toHaveLength(0)
  })

  it('409 em (a): toast com a dona, nada mais é feito, a ficha sai e o rótulo volta', async () => {
    const { escritas } = servidor({
      aoPatchCategoria: () =>
        jsonResponse(409, {
          error: {
            code: 'KEYWORD_TAKEN',
            message: 'keyword already used',
            fields: { keyword: 'mercado', ownerId: 'cat-padaria' },
          },
        }),
    })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    const select = within(editor).getByRole('combobox')
    await user.selectOptions(select, 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }))
    await user.click(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «mercado»' }),
    )

    expect(await screen.findByText('«mercado» já está em Padaria.')).toBeInTheDocument()
    // Só (a) foi tentada.
    expect(escritas().map((c) => `${c.metodo} ${c.caminho}`)).toEqual([
      'PATCH /categories/cat-alimentacao',
    ])
    // Editor aberto, categoria mantida, ficha fora, rótulo de volta, foco no confirmar.
    expect(editor).toBeInTheDocument()
    expect(select).toHaveValue('cat-alimentacao')
    expect(
      within(editor).queryByRole('button', { name: 'Reconhecer por «mercado»' }),
    ).not.toBeInTheDocument()
    expect(
      within(editor).getByRole('button', { name: 'Reconhecer por «jose»' }),
    ).toBeInTheDocument()
    const confirmar = within(editor).getByRole('button', { name: 'Categorizar' })
    expect(confirmar).toHaveFocus()
    // A linha continua sem categoria.
    expect(lacuna(NOME_DA_LACUNA_1)).toBeInTheDocument()
  })

  it('falha em (b) depois de (a): a ficha vira texto estático e o lançamento continua sem categoria', async () => {
    const { escritas } = servidor({
      aoPatchLancamento: () =>
        jsonResponse(500, { error: { code: 'INTERNAL_ERROR', message: 'boom' } }),
    })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }))
    await user.click(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «mercado»' }),
    )

    expect(
      await screen.findByText(
        '«mercado» adicionada a Alimentação, mas o lançamento não foi categorizado. Tente de novo.',
      ),
    ).toBeInTheDocument()
    expect(escritas().map((c) => `${c.metodo} ${c.caminho}`)).toEqual([
      'PATCH /categories/cat-alimentacao',
      'PATCH /transactions/tx-1',
    ])
    expect(editor).toBeInTheDocument()
    // Texto estático com a palavra: já é da categoria.
    expect(
      within(editor).queryByRole('button', { name: 'Reconhecer por «mercado»' }),
    ).not.toBeInTheDocument()
    expect(within(editor).getByText('mercado')).toBeInTheDocument()
    expect(within(editor).getByRole('button', { name: 'Categorizar' })).toHaveFocus()
  })

  it('falha em (c) depois de (a) e (b): fecha (a linha está resolvida) e o toast manda à faixa', async () => {
    const { escritas } = servidor({
      aoAutoCategorizar: () =>
        jsonResponse(429, { error: { code: 'RATE_LIMITED', message: 'slow down' } }),
    })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }))
    await user.click(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «mercado»' }),
    )

    expect(
      await screen.findByText(
        '«mercado» adicionada e lançamento categorizado, mas os outros do mês não foram — use Categorizar automaticamente na faixa.',
      ),
    ).toBeInTheDocument()
    expect(escritas().map((c) => `${c.metodo} ${c.caminho}`)).toEqual([
      'PATCH /categories/cat-alimentacao',
      'PATCH /transactions/tx-1',
      'POST /transactions/auto-categorize',
    ])
    expect(editor).not.toBeInTheDocument()
    expect(screen.queryAllByRole('button', { name: NOME_DA_LACUNA_1 })).toHaveLength(0)
    await waitFor(() => expect(lacuna(NOME_DA_LACUNA_2)).toHaveFocus())
  })
})

describe('AtalhoDeCategoria — estados das categorias', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  // Critério de aceite 2 da spec 0006, no seletor de `/lancamentos`.
  it('o seletor de uma DESPESA traz as categorias de investimento, e nunca as de resgate', async () => {
    servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { editor } = await abrirEditor()
    const select = within(editor).getByRole('combobox')

    expect(within(select).getByRole('option', { name: 'CDB' })).toBeInTheDocument()
    // O grupo vira `optgroup`: a natureza já está no nome dele.
    expect(select.querySelector('optgroup[label="Investimentos"]')).not.toBeNull()
    // O outro lado do dinheiro fica de fora — escolhê-lo seria um 422.
    expect(within(select).queryByRole('option', { name: 'Salário' })).toBeNull()
    expect(within(select).queryByRole('option', { name: 'Resgates' })).toBeNull()
  })

  it('sem categoria do LADO: frase, caminho para categorias e só Cancelar', async () => {
    // Nem despesa nem investimento: é o que faz o lado ficar vazio de verdade.
    servidor({ categorias: { expense: [], income: ARVORE.income, investment: [], redemption: [] } })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { editor } = await abrirEditor()
    // A frase nomeia as DUAS naturezas do lado, porque o seletor oferece as
    // duas e ela só aparece quando as duas estão vazias.
    expect(
      await within(editor).findByText('Nenhuma categoria de despesa ou de investimento ainda.'),
    ).toBeInTheDocument()
    expect(within(editor).getByRole('button', { name: 'Ir para categorias' })).toBeInTheDocument()
    expect(within(editor).queryByRole('combobox')).not.toBeInTheDocument()
    expect(within(editor).queryByRole('button', { name: /Categori/ })).not.toBeInTheDocument()
    expect(within(editor).getByRole('button', { name: 'Cancelar' })).toBeInTheDocument()
  })

  it('erro ao carregar: alerta com tentar de novo no lugar do select', async () => {
    let falhar = true
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      const caminho = new URL(String(url), 'https://app.invalido').pathname.replace('/api/v1', '')
      if (caminho === '/me') return jsonResponse(200, SESSAO)
      if (caminho === '/accounts') return jsonResponse(200, CONTAS)
      if (caminho === '/categories') {
        if (falhar) return jsonResponse(500, { error: { code: 'INTERNAL_ERROR', message: 'x' } })
        return jsonResponse(200, ARVORE)
      }
      if (caminho === '/transactions' && (init?.method ?? 'GET') === 'GET') {
        const itens = [lancamento()]
        return jsonResponse(200, { items: itens, nextCursor: null, summary: resumo(itens) })
      }
      throw new Error(`rota não declarada: ${caminho}`)
    })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    expect(
      await within(editor).findByText('Não foi possível carregar as categorias.'),
    ).toBeInTheDocument()
    expect(within(editor).queryByRole('combobox')).not.toBeInTheDocument()

    falhar = false
    await user.click(within(editor).getByRole('button', { name: 'Tentar de novo' }))
    expect(await within(editor).findByRole('combobox')).toBeInTheDocument()
  })

  it('categoria com 20 palavras: sem fichas, com a frase que explica', async () => {
    const cheia = categoria({
      id: 'cat-cheia',
      name: 'Lotada',
      kind: 'expense',
      keywords: Array.from({ length: 20 }, (_, i) => `p${i}`),
    })
    servidor({ categorias: { expense: [cheia], income: [], investment: [], redemption: [] } })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-cheia')
    expect(
      within(editor).getByText(
        'Lotada já tem 20 palavras-chave. Remova uma em Categorias para incluir outra.',
      ),
    ).toBeInTheDocument()
    expect(within(editor).queryByText('Da próxima vez, reconhecer por')).not.toBeInTheDocument()
    expect(within(editor).getByRole('button', { name: 'Categorizar' })).toBeInTheDocument()
  })
})

describe('fraseDosOutros', () => {
  it('conta só os outros e concorda em número', () => {
    expect(fraseDosOutros(0, 'setembro')).toBe('nenhum outro lançamento de setembro categorizado.')
    expect(fraseDosOutros(1, 'setembro')).toBe('mais 1 lançamento de setembro categorizado.')
    expect(fraseDosOutros(7, 'setembro')).toBe('mais 7 lançamentos de setembro categorizados.')
  })
})

describe('proximaLacuna', () => {
  function lacunas(ids: readonly string[]) {
    document.body.innerHTML = ids
      .map((id) => `<button type="button" data-atalho="${id}">Sem categoria</button>`)
      .join('')
  }

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('a seguinte na ordem de antes; senão a anterior; senão qualquer uma; senão nada', () => {
    const antes = ['a', 'b', 'c', 'd']
    lacunas(['a', 'c', 'd'])
    expect(proximaLacuna('b', antes)?.dataset.atalho).toBe('c')
    lacunas(['a', 'b'])
    expect(proximaLacuna('c', antes)?.dataset.atalho).toBe('b')
    lacunas(['z'])
    expect(proximaLacuna('c', antes)?.dataset.atalho).toBe('z')
    lacunas([])
    expect(proximaLacuna('c', antes)).toBeNull()
  })

  it('pula as que o reprocessamento resolveu e nunca devolve a própria linha', () => {
    const antes = ['a', 'b', 'c']
    lacunas(['a', 'c'])
    expect(proximaLacuna('a', antes)?.dataset.atalho).toBe('c')
    lacunas(['a'])
    expect(proximaLacuna('a', antes)).toBeNull()
  })
})

// ---------------------------------------------------------------------------
// QA da §11 (17/09/2026): as lacunas que a revisão da decisão (h) apontou —
// filtro `?semCategoria=1`, troca de contexto durante a edição, teclado a
// partir da ficha, o `loading` sem `disabled`, a segunda instância do botão
// (celular) e a frase do 409 sem dona resolvível.
// ---------------------------------------------------------------------------

describe('AtalhoDeCategoria — QA da emenda §11', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('com ?semCategoria=1 a linha resolvida sai da lista e o foco vai à lacuna seguinte', async () => {
    servidor({})
    renderLancamentos('/lancamentos?mes=2026-09&semCategoria=1')
    await screen.findByText('Mercado do seu José')

    // O filtro mostra só as três lacunas: a categorizada e a transferência
    // ficam de fora.
    expect(screen.queryByText('Pão de Açúcar')).not.toBeInTheDocument()
    expect(screen.queryByText('Pix para Cartão')).not.toBeInTheDocument()

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))

    await screen.findByText('Lançamento categorizado como Alimentação.')
    // A linha resolvida SAIU da lista (o filtro a exclui) e o foco está na
    // seguinte — que tomou o lugar dela.
    await waitFor(() => expect(screen.queryByText('Mercado do seu José')).not.toBeInTheDocument())
    await waitFor(() => expect(lacuna(NOME_DA_LACUNA_2)).toHaveFocus())
  })

  it('trocar a conta ou o filtro fecha o editor sem gravar', async () => {
    const { escritas } = servidor({})
    const router = renderLancamentos()
    await screen.findByText('Mercado do seu José')

    // Com a categoria já escolhida e a ficha pressionada: nada disso vira
    // gravação ao trocar de contexto.
    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }))

    await act(() =>
      router.navigate({ to: '/lancamentos', search: { mes: '2026-09', conta: CONTA_CORRENTE } }),
    )
    await waitFor(() => {
      expect(screen.queryByRole('group', { name: /^Categorizar / })).not.toBeInTheDocument()
    })

    await abrirEditor()
    await act(() =>
      router.navigate({ to: '/lancamentos', search: { mes: '2026-09', semCategoria: 1 } }),
    )
    await waitFor(() => {
      expect(screen.queryByRole('group', { name: /^Categorizar / })).not.toBeInTheDocument()
    })

    expect(escritas()).toHaveLength(0)
    // Reabrir começa do zero: nem categoria escolhida, nem ficha pressionada.
    const reaberto = await abrirEditor()
    expect(within(reaberto.editor).getByRole('combobox')).toHaveValue('')
    expect(
      within(reaberto.editor).getByRole('button', { name: 'Escolha uma categoria' }),
    ).toBeInTheDocument()
  })

  it('Escape a partir da ficha fecha e devolve o foco ao botão da célula', async () => {
    const { escritas } = servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    const ficha = within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' })
    await user.click(ficha)
    expect(ficha).toHaveFocus()

    await user.keyboard('{Escape}')
    expect(editor).not.toBeInTheDocument()
    expect(lacuna(NOME_DA_LACUNA_1)).toHaveFocus()
    expect(escritas()).toHaveLength(0)
  })

  it('abre com Enter e com Space, como um disclosure', async () => {
    servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const user = userEvent.setup()
    lacuna(NOME_DA_LACUNA_1).focus()
    await user.keyboard('{Enter}')
    const editor = await screen.findByRole('group', { name: /^Categorizar Mercado/ })
    await user.keyboard('{Escape}')
    expect(editor).not.toBeInTheDocument()

    expect(lacuna(NOME_DA_LACUNA_1)).toHaveFocus()
    await user.keyboard(' ')
    expect(await screen.findByRole('group', { name: /^Categorizar Mercado/ })).toBeInTheDocument()
  })

  it('durante o loading nada fica disabled e o clique repetido não manda outro PATCH', async () => {
    let liberar: (() => void) | undefined
    const espera = new Promise<void>((resolve) => {
      liberar = resolve
    })
    const { escritas } = servidor({})
    // Atrasa a resposta do PATCH sem trocar o corpo: o `fetch` do teste espera
    // a promessa antes de responder.
    const original = fetchMock.getMockImplementation()
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      const metodo = init?.method ?? 'GET'
      const caminho = new URL(String(url), 'https://app.invalido').pathname.replace('/api/v1', '')
      if (caminho.startsWith('/transactions/') && metodo === 'PATCH') await espera
      return original?.(url, init)
    })

    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    const select = within(editor).getByRole('combobox')
    await user.selectOptions(select, 'cat-alimentacao')
    const confirmar = within(editor).getByRole('button', { name: 'Categorizar' })
    await user.click(confirmar)

    // Em andamento: `aria-busy`, nenhum `disabled` em lugar nenhum, e o foco
    // continua onde estava (checklist 20 do DESIGN.md).
    await waitFor(() => expect(confirmar).toHaveAttribute('aria-busy', 'true'))
    expect(confirmar).not.toBeDisabled()
    expect(select).not.toBeDisabled()
    expect(
      within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }),
    ).not.toBeDisabled()
    expect(confirmar).toHaveFocus()

    // Clique repetido enquanto grava não vira um segundo PATCH.
    await user.click(confirmar)
    await user.click(confirmar)
    liberar?.()

    await screen.findByText('Lançamento categorizado como Alimentação.')
    expect(escritas().filter((c) => c.caminho === '/transactions/tx-1')).toHaveLength(1)
  })

  it('a instância do celular abre o mesmo editor e devolve o foco à linha', async () => {
    servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const user = userEvent.setup()
    const instancias = screen.getAllByRole('button', { name: NOME_DA_LACUNA_1 })
    const secundaria = instancias[1] as HTMLButtonElement
    expect(secundaria).toHaveAttribute('id', 'atalho-categoria-tx-1-secundaria')

    await user.click(secundaria)
    const editor = await screen.findByRole('group', { name: /^Categorizar Mercado/ })
    // As duas instâncias falam do mesmo estado: o editor é um só.
    expect(screen.getAllByRole('group', { name: /^Categorizar / })).toHaveLength(1)
    for (const botao of screen.getAllByRole('button', { name: NOME_DA_LACUNA_1 })) {
      expect(botao).toHaveAttribute('aria-expanded', 'true')
      expect(botao).toHaveAttribute('aria-controls', editor.id)
    }

    await user.keyboard('{Escape}')
    // Sem layout no jsdom, `checkVisibility` aprova as duas e a devolução do
    // foco escolhe a primeira do DOM — o que importa é que o foco voltou à
    // linha, e não ao <body>.
    expect(document.activeElement).toHaveAttribute('data-atalho', 'tx-1')
  })

  it('receita só recebe categoria de receita', async () => {
    servidor({
      lancamentos: [
        lancamento({
          id: 'tx-in',
          kind: 'income',
          description: 'Salário da firma',
          amountCents: 500_000,
        }),
      ],
    })
    renderLancamentos()
    await screen.findByText('Salário da firma')

    const { editor } = await abrirEditor(/^Sem categoria\. Categorizar Salário da firma/)
    const select = within(editor).getByRole('combobox')
    expect(within(select).getByRole('option', { name: 'Salário' })).toBeInTheDocument()
    expect(within(select).queryByRole('option', { name: 'Alimentação' })).not.toBeInTheDocument()
    expect(within(select).queryByRole('option', { name: 'Uber' })).not.toBeInTheDocument()
  })

  it('409 sem dona resolvível cai na frase genérica, ainda com a palavra citada', async () => {
    servidor({
      aoPatchCategoria: () =>
        jsonResponse(409, {
          error: {
            code: 'KEYWORD_TAKEN',
            message: 'keyword already used',
            // A dona é de uma árvore que a tela não tem em cache (arquivada,
            // por exemplo): o id não resolve para nome nenhum.
            fields: { keyword: 'mercado', ownerId: 'cat-que-nao-esta-na-arvore' },
          },
        }),
    })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }))
    await user.click(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «mercado»' }),
    )

    expect(
      await screen.findByText('«mercado» já está em outra categoria desta casa.'),
    ).toBeInTheDocument()
    // O id NUNCA aparece na tela.
    expect(screen.queryByText(/cat-que-nao-esta-na-arvore/)).not.toBeInTheDocument()
    expect(lacuna(NOME_DA_LACUNA_1)).toBeInTheDocument()
  })

  it('o toast usa o número do servidor, não a contagem das linhas da tela', async () => {
    // A tela tem 3 lacunas; o servidor diz que categorizou 7 (há páginas que a
    // tela não carregou). O toast repete o servidor.
    servidor({
      aoAutoCategorizar: () =>
        jsonResponse(200, { categorized: 7, unmatched: 0, items: [], unmatchedItems: [] }),
    })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }))
    await user.click(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «mercado»' }),
    )

    expect(
      await screen.findByText(
        '«mercado» adicionada a Alimentação · mais 7 lançamentos de setembro categorizados.',
      ),
    ).toBeInTheDocument()
  })

  it('com 0 em (c) o toast diz que nenhum outro foi categorizado', async () => {
    servidor({
      lancamentos: [lancamento(), lancamento({ id: 'tx-2', description: 'Posto Ipiranga' })],
      aoAutoCategorizar: () =>
        jsonResponse(200, { categorized: 0, unmatched: 1, items: [], unmatchedItems: [] }),
    })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }))
    await user.click(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «mercado»' }),
    )

    expect(
      await screen.findByText(
        '«mercado» adicionada a Alimentação · nenhum outro lançamento de setembro categorizado.',
      ),
    ).toBeInTheDocument()
  })

  it('a lista mandada em (a) é a do cache no clique, com a palavra no fim', async () => {
    // Alimentação já tem «padaria»: o PATCH manda as duas, nunca só a nova.
    const { escritas } = servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Reconhecer por «jose»' }))
    await user.click(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «jose»' }),
    )

    await screen.findByText(/«jose» adicionada a Alimentação/)
    const patchDaCategoria = escritas().find((c) => c.caminho === '/categories/cat-alimentacao')
    expect(patchDaCategoria?.corpo).toEqual({ keywords: ['padaria', 'jose'] })
  })

  it('a linha sem palavra elegível não mostra a linha de fichas e segue pelo caminho comum', async () => {
    const { escritas } = servidor({
      lancamentos: [lancamento({ id: 'tx-num', description: '123 456' })],
    })
    renderLancamentos()
    await screen.findByText('123 456')

    const { user, editor } = await abrirEditor(/^Sem categoria\. Categorizar 123 456/)
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    expect(within(editor).queryByText('Da próxima vez, reconhecer por')).not.toBeInTheDocument()
    expect(within(editor).getByRole('status')).toBeEmptyDOMElement()

    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))
    await screen.findByText('Lançamento categorizado como Alimentação.')
    expect(escritas().map((c) => c.caminho)).toEqual(['/transactions/tx-num'])
  })
})

/** Emenda §13 da spec 0005 — grupo com subcategorias não recebe lançamento.
 *
 *  O seletor JÁ filtra esses grupos (`opcoesDeCategoria`, mesmo critério do
 *  servidor), então o 422 só aparece numa corrida: o grupo ganhou uma
 *  subcategoria enquanto o editor estava aberto, e a árvore em cache é de
 *  antes. É por isso que o teste mocka o 422 diretamente em vez de tentar
 *  escolher um grupo na lista — na lista ele não está. */
describe('AtalhoDeCategoria — 422 da emenda §13', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  function servidorQueRecusaACategoria() {
    return servidor({
      aoPatchLancamento: () =>
        jsonResponse(422, {
          error: {
            code: 'VALIDATION_FAILED',
            message: 'x',
            // A prosa do servidor chega aqui e NÃO é o que a tela exibe: a
            // frase mostrada sai de `src/lib/errors.ts`.
            fields: { categoryId: 'Este grupo tem subcategorias. Escolha uma subcategoria.' },
          },
        }),
    })
  }

  it('o editor fica aberto, a escolha é limpa e a mensagem nasce junto do seletor', async () => {
    servidorQueRecusaACategoria()
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    const select = within(editor).getByRole('combobox', { name: /^Categoria de/ })
    await user.selectOptions(select, 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))

    expect(await within(editor).findByText(MSG_CATEGORIA_RECUSADA)).toBeInTheDocument()
    // Junto do seletor, e o seletor o descreve: quem recebe o foco ouve a
    // frase sem precisar procurá-la.
    expect(select).toHaveAttribute('aria-invalid', 'true')
    expect(within(editor).getByText(MSG_CATEGORIA_RECUSADA).closest('p')?.id).toBe(
      select.getAttribute('aria-describedby'),
    )
    expect(select).toHaveFocus()

    // A escolha recusada sai do campo e o confirmar volta a cobrar uma.
    expect(select).toHaveValue('')
    expect(
      within(editor).getByRole('button', { name: 'Escolha uma categoria' }),
    ).toBeInTheDocument()

    // O editor continua aberto e nada foi anunciado como sucesso.
    expect(editor).toBeInTheDocument()
    expect(screen.queryByText(/Lançamento categorizado como/)).not.toBeInTheDocument()
    expect(
      screen.queryByText('Confira os dados informados e tente de novo.'),
    ).not.toBeInTheDocument()
  })

  it('recarrega as categorias, para o grupo recusado sumir das opções', async () => {
    const { chamadas } = servidorQueRecusaACategoria()
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    const antes = chamadas.filter((c) => c.caminho === '/categories' && c.metodo === 'GET').length
    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))

    await within(editor).findByText(MSG_CATEGORIA_RECUSADA)
    await waitFor(() =>
      expect(
        chamadas.filter((c) => c.caminho === '/categories' && c.metodo === 'GET').length,
      ).toBeGreaterThan(antes),
    )
  })

  it('na sequência com palavra, o 422 de (b) não vira "falha-b": é a mesma recusa da categoria', async () => {
    const { chamadas } = servidorQueRecusaACategoria()
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }))
    await user.click(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «mercado»' }),
    )

    expect(await within(editor).findByText(MSG_CATEGORIA_RECUSADA)).toBeInTheDocument()
    // Nada de "adicionada a X, mas o lançamento não foi categorizado": a
    // orientação daquele toast é "tente de novo", e tentar de novo na mesma
    // categoria falha de novo.
    expect(screen.queryByText(/mas o lançamento não foi categorizado/)).not.toBeInTheDocument()
    // E (c) nem chega a rodar: o mês não é reprocessado sobre uma categoria
    // que o servidor acabou de recusar.
    expect(chamadas.some((c) => c.caminho === '/transactions/auto-categorize')).toBe(false)
  })
})
