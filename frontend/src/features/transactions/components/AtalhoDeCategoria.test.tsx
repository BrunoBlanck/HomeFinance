import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'
import { MSG_CATEGORIA_RECUSADA } from '@/lib/errors'
import { fraseDosOutros, proximaLacuna, proximoAtalho } from './AtalhoDeCategoria'

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

type GrupoDaArvore = {
  id: string
  name: string
  kind: string
  children?: readonly { id: string; name: string; kind: string }[]
}

/** Percorre a árvore (a padrão ou a que o teste passou) e devolve o nó do id. */
function noDaArvore(id: unknown, arvore: unknown = ARVORE) {
  const listas = Object.values((arvore ?? {}) as Record<string, readonly GrupoDaArvore[]>)
  for (const lista of listas) {
    for (const grupo of lista ?? []) {
      if (grupo.id === id) return grupo
      const filha = (grupo.children ?? []).find((c) => c.id === id)
      if (filha) return filha
    }
  }
  return undefined
}

/** O nome de uma categoria da árvore do teste, pelo id.
 *
 *  A árvore é PARÂMETRO desde o QA da §19: os testes que passam uma árvore
 *  própria (nomes hostis, grupo com filhas) precisam que o `PATCH` devolva o
 *  nome daquela árvore, e não `null` — é esse nome que a célula mostra depois
 *  do refetch e que o toast da troca seguinte cita. */
function nomeDaCategoria(id: unknown, arvore: unknown = ARVORE): string | null {
  return noDaArvore(id, arvore)?.name ?? null
}

/** Em qual recorte de `?tipo=` uma linha cai — a mesma partição do servidor
 *  (E2d): a natureza da CATEGORIA decide antes do lado do dinheiro. */
function grupoDaLinha(linha: Lancamento, arvore: unknown): string {
  if (linha.kind === 'transfer_out' || linha.kind === 'transfer_in') return 'transfer'
  const natureza = noDaArvore(linha.categoryId, arvore)?.kind
  if (natureza === 'investment' || natureza === 'redemption') return 'investment'
  return linha.kind === 'income' ? 'income' : 'expense'
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
  /** Honra o `kindGroup` da URL, como o servidor de verdade — é o que faz a
   *  linha SAIR da lista quando a categoria nova contradiz o filtro. Fora
   *  daqui o recorte não muda nada, e os testes de toast não precisam dele. */
  filtrarPorTipo?: boolean
  aoPatchCategoria?: (id: string, corpo: Record<string, unknown>) => Response
  aoPatchLancamento?: (id: string, corpo: Record<string, unknown>) => Response | null
  aoAutoCategorizar?: (corpo: Record<string, unknown>) => Response
}) {
  const lancamentos = opcoes.lancamentos ?? lancamentosPadrao()
  const arvore = opcoes.categorias ?? ARVORE
  const chamadas: Chamada[] = []

  fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    const completa = new URL(String(url), 'https://app.invalido')
    const caminho = completa.pathname.replace('/api/v1', '')
    const corpo = init?.body ? (JSON.parse(String(init.body)) as Record<string, unknown>) : null
    chamadas.push({ metodo, caminho, corpo })

    if (caminho === '/me') return jsonResponse(200, SESSAO)
    if (caminho === '/accounts') return jsonResponse(200, CONTAS)
    if (caminho === '/categories') return jsonResponse(200, arvore)
    if (caminho === '/transactions' && metodo === 'GET') {
      const grupo = opcoes.filtrarPorTipo ? completa.searchParams.get('kindGroup') : null
      const itens = grupo
        ? lancamentos.filter((item) => grupoDaLinha(item, arvore) === grupo)
        : lancamentos
      return jsonResponse(200, {
        items: itens,
        nextCursor: null,
        // O `summary` do contrato conta o RECORTE (spec 0004 §12.2).
        summary: resumo(itens),
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
      // O nome vem da árvore, como no servidor de verdade: é ele que a célula
      // mostra depois do refetch, e o modo trocar depende de ver o nome NOVO.
      alvo.categoryName = nomeDaCategoria(corpo?.categoryId, arvore)
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

function renderLancamentos(
  caminho = '/lancamentos?mes=2026-09',
  semear?: (cliente: QueryClient) => void,
) {
  const router = createAppRouter(createMemoryHistory({ initialEntries: [caminho] }))
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  // O cache pode já ter outras queries do app — o painel e o relatório penduram
  // as chaves deles sob o mesmo prefixo `transactions` (ADR-027).
  semear?.(queryClient)
  render(
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  )
  return { router, queryClient }
}

const NOME_DA_LACUNA_1 = /^Sem categoria\. Categorizar Mercado do seu José, 12\/09\/2026/
const NOME_DA_LACUNA_2 = /^Sem categoria\. Categorizar Posto Ipiranga/
const NOME_DA_LACUNA_3 = /^Sem categoria\. Categorizar Farmácia Popular/
/** O outro estado fechado (emenda §19): o nome da categoria abre o nome
 *  acessível, e o verbo é **trocar**. */
const NOME_DA_CATEGORIZADA = /^Alimentação\. Trocar categoria de Pão de Açúcar/

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

/** O mesmo editor, aberto pelo outro estado fechado — a legenda muda com o
 *  modo (`Trocar categoria de …`). */
async function abrirParaTrocar(nome: RegExp = NOME_DA_CATEGORIZADA) {
  const user = userEvent.setup()
  await user.click(lacuna(nome))
  const editor = await screen.findByRole('group', { name: /^Trocar categoria de / })
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

  it('a lacuna é controle e traz data-lacuna; a transferência não tem controle nenhum', async () => {
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
    expect(instancias[0]).toHaveAttribute('data-instancia', 'coluna')
    expect(instancias[1]).toHaveAttribute('data-instancia', 'secundaria')
    // `data-lacuna` SÓ na pendência: é por ele que a próxima lacuna é
    // procurada, e a linha categorizada não pode entrar nessa conta.
    expect(instancias[0]).toHaveAttribute('data-lacuna')

    // Perna de transferência não tem categoria por desenho: etiqueta, e nada
    // de controle — nem para categorizar, nem para trocar.
    expect(screen.getAllByText('Transferência').length).toBeGreaterThan(0)
    // (o botão de excluir da linha continua lá; o que não existe é controle
    // de categoria, em nenhum dos dois estados)
    expect(
      screen.queryByRole('button', {
        name: /(Categorizar|Trocar categoria de) Pix para Cartão/,
      }),
    ).not.toBeInTheDocument()
  })

  // Emenda §19: o outro estado FECHADO do mesmo controle.
  it('a linha categorizada tem o mesmo controle, com o nome da categoria e sem data-lacuna', async () => {
    servidor({})
    renderLancamentos()
    await screen.findByText('Pão de Açúcar')

    const instancias = screen.getAllByRole('button', { name: NOME_DA_CATEGORIZADA })
    expect(instancias).toHaveLength(2)
    const [coluna, secundaria] = instancias as [HTMLButtonElement, HTMLButtonElement]
    // O texto visível é o nome da categoria, e está contido no nome acessível
    // (WCAG 2.5.3), que diz de qual linha é.
    expect(coluna).toHaveTextContent('Alimentação')
    expect(coluna).toHaveAttribute('id', 'atalho-categoria-tx-ok-coluna')
    expect(secundaria).toHaveAttribute('id', 'atalho-categoria-tx-ok-secundaria')
    // Nos dois estados: `data-atalho` (por onde o foco volta à linha) e
    // `data-instancia` (o alinhamento na coluna).
    expect(coluna).toHaveAttribute('data-atalho', 'tx-ok')
    expect(coluna).toHaveAttribute('data-instancia', 'coluna')
    // Nunca na categorizada: ela não é pendência.
    expect(coluna).not.toHaveAttribute('data-lacuna')
    // Disclosure fechado, como a lacuna.
    expect(coluna).toHaveAttribute('aria-expanded', 'false')
    expect(coluna).not.toHaveAttribute('aria-controls')
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
    const { router } = renderLancamentos()
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
  /** Só lacunas: `data-lacuna` é o que a busca da próxima pendência consulta.
   *  Desde a emenda §19 `data-atalho` está TAMBÉM nas linhas categorizadas, e
   *  procurar por ele trataria a vizinha resolvida como "próximo trabalho". */
  function lacunas(ids: readonly string[]) {
    document.body.innerHTML = ids
      .map((id) => `<button type="button" data-atalho="${id}" data-lacuna>Sem categoria</button>`)
      .join('')
  }

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('ignora a linha categorizada: ela tem data-atalho, mas não é lacuna', () => {
    document.body.innerHTML = [
      '<button type="button" data-atalho="a" data-lacuna>Sem categoria</button>',
      '<button type="button" data-atalho="b">Alimentação</button>',
      '<button type="button" data-atalho="c" data-lacuna>Sem categoria</button>',
    ].join('')
    // A seguinte de "a" é "c" — "b" já tem dona e não é trabalho pendente.
    expect(proximaLacuna('a', ['a', 'c'])?.dataset.atalho).toBe('c')
    // E o vizinho do modo trocar enxerga as três, na ordem da tabela.
    expect(proximoAtalho('a', ['a', 'b', 'c'])?.dataset.atalho).toBe('b')
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

/** `proximoAtalho` — o vizinho do modo trocar (emenda §19).
 *
 *  Mesma busca da `proximaLacuna`, com duas diferenças: olha TODO controle de
 *  categoria (`data-atalho`, qualquer estado) e **para** na anterior. Pular
 *  para uma linha aleatória da tabela não é devolver o foco a quem estava
 *  trabalhando numa linha específica. */
describe('proximoAtalho', () => {
  function atalhos(ids: readonly string[]) {
    document.body.innerHTML = ids
      .map((id) => `<button type="button" data-atalho="${id}">Alimentação</button>`)
      .join('')
  }

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('a seguinte da foto; senão a anterior; nunca "qualquer uma que sobrou"', () => {
    const antes = ['a', 'b', 'c', 'd']
    atalhos(['a', 'c', 'd'])
    expect(proximoAtalho('b', antes)?.dataset.atalho).toBe('c')
    atalhos(['a', 'b'])
    expect(proximoAtalho('c', antes)?.dataset.atalho).toBe('b')
    // Uma linha que nem estava na foto NÃO serve de destino — este é o passo
    // que o modo trocar não tem.
    atalhos(['z'])
    expect(proximoAtalho('c', antes)).toBeNull()
    atalhos([])
    expect(proximoAtalho('c', antes)).toBeNull()
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
    const { router } = renderLancamentos()
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

/** A linha que some (docs/DESIGN.md, E2d (g)).
 *
 *  Com `tipo=despesas` ativo, categorizar uma linha como investimento a tira da
 *  lista na hora — a pessoa acabou de aprender, sem querer, que a natureza da
 *  categoria decide o tipo do lançamento. Sumir em silêncio é inaceitável. */
describe('AtalhoDeCategoria — a linha que sai da lista (E2d)', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('sob despesas, o toast de sucesso diz que a linha saiu e por quê', async () => {
    servidor({})
    renderLancamentos('/lancamentos?mes=2026-09&tipo=despesas')
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-cdb')
    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))

    // Continua DE SUCESSO: nada falhou, ela fez o que quis.
    expect(
      await screen.findByText(
        'Lançamento categorizado como CDB. Ele saiu da lista: é um aporte, e a lista mostra só despesas.',
      ),
    ).toBeInTheDocument()
  })

  it('sob receitas, a frase fala em resgate', async () => {
    servidor({
      lancamentos: [
        lancamento({
          id: 'tx-1',
          kind: 'income',
          description: 'Mercado do seu José',
          amountCents: 90_000,
        }),
      ],
    })
    renderLancamentos('/lancamentos?mes=2026-09&tipo=receitas')
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-resgates')
    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))

    expect(
      await screen.findByText(
        'Lançamento categorizado como Resgates. Ele saiu da lista: é um resgate, e a lista mostra só receitas.',
      ),
    ).toBeInTheDocument()
  })

  it('em Tudo o toast é o de hoje, sem acréscimo — a linha não saiu de lugar nenhum', async () => {
    servidor({})
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-cdb')
    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))

    expect(await screen.findByText('Lançamento categorizado como CDB.')).toBeInTheDocument()
    expect(screen.queryByText(/saiu da lista/)).not.toBeInTheDocument()
  })

  it('com categoria do mesmo lado do dinheiro, o filtro ativo não muda o toast', async () => {
    servidor({})
    renderLancamentos('/lancamentos?mes=2026-09&tipo=despesas')
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))

    expect(await screen.findByText('Lançamento categorizado como Alimentação.')).toBeInTheDocument()
    expect(screen.queryByText(/saiu da lista/)).not.toBeInTheDocument()
  })

  it('na saída com palavra-chave, a frase entra DEPOIS do texto de hoje', async () => {
    servidor({})
    renderLancamentos('/lancamentos?mes=2026-09&tipo=despesas')
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-cdb')
    await user.click(within(editor).getByRole('button', { name: 'Reconhecer por «mercado»' }))
    await user.click(
      within(editor).getByRole('button', { name: 'Categorizar e reconhecer por «mercado»' }),
    )

    expect(
      await screen.findByText(
        '«mercado» adicionada a CDB · mais 2 lançamentos de setembro categorizados. Este lançamento saiu da lista: é um aporte, e a lista mostra só despesas.',
      ),
    ).toBeInTheDocument()
  })
})

/** Emenda §19 da spec 0005 (18/09/2026) — trocar a categoria de uma linha que
 *  já tem uma, a qualquer momento (pedido do usuário).
 *
 *  O que é novo aqui é a linha JÁ CATEGORIZADA como ponto de partida: o mesmo
 *  controle no outro estado fechado, o mesmo editor em modo trocar, e um foco
 *  que volta para a própria linha em vez de caçar a próxima pendência. */
describe('AtalhoDeCategoria — modo trocar (emenda §19)', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('abre com a categoria atual selecionada, e confirmar com ela não emite requisição', async () => {
    const { escritas } = servidor({})
    renderLancamentos()
    await screen.findByText('Pão de Açúcar')

    const { user, editor } = await abrirParaTrocar()
    const select = within(editor).getByRole('combobox', { name: /^Categoria de Pão de Açúcar/ })
    // A pessoa vê de onde está saindo antes de escolher para onde vai.
    expect(select).toHaveValue('cat-alimentacao')
    expect(select).toHaveFocus()

    // Com a atual escolhida não há troca — e não há o que aprender: as fichas
    // só existem com escolha diferente.
    expect(within(editor).queryByText('Da próxima vez, reconhecer por')).not.toBeInTheDocument()
    const confirmar = within(editor).getByRole('button', { name: 'Escolha outra categoria' })
    expect(confirmar).toHaveAttribute('aria-disabled', 'true')
    expect(confirmar).not.toBeDisabled()

    // O clique leva ao campo, e NENHUMA requisição sai: não existe "trocar
    // para a mesma".
    await user.click(confirmar)
    expect(select).toHaveFocus()
    expect(escritas()).toHaveLength(0)

    // Escolher outra troca o verbo do rótulo e devolve as fichas.
    await user.selectOptions(select, 'cat-uber')
    expect(within(editor).getByRole('button', { name: 'Trocar categoria' })).toBeInTheDocument()
    expect(within(editor).getByText('Da próxima vez, reconhecer por')).toBeInTheDocument()
  })

  it('um PATCH só; o toast diz de onde para onde e o foco volta ao botão da MESMA linha', async () => {
    const { escritas } = servidor({})
    renderLancamentos()
    await screen.findByText('Pão de Açúcar')

    const { user, editor } = await abrirParaTrocar()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-uber')
    await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))

    expect(
      await screen.findByText('Categoria trocada de Alimentação para Uber.'),
    ).toBeInTheDocument()
    expect(escritas()).toEqual([
      { metodo: 'PATCH', caminho: '/transactions/tx-ok', corpo: { categoryId: 'cat-uber' } },
    ])
    expect(editor).not.toBeInTheDocument()

    // O foco volta para a linha que ela estava editando — nunca para a próxima
    // lacuna, que é o trabalho de outra pergunta.
    await waitFor(() => expect(document.activeElement).toHaveAttribute('data-atalho', 'tx-ok'))
    // E o botão mostra o nome novo.
    const linha = screen.getByText('Pão de Açúcar').closest('tr') as HTMLElement
    expect(
      within(linha).getAllByRole('button', { name: /^Uber\. Trocar categoria de/ }).length,
    ).toBeGreaterThan(0)
    // As lacunas continuam intocadas: trocar uma linha não mexe em nenhuma
    // outra.
    expect(screen.getAllByRole('button', { name: NOME_DA_LACUNA_1 })).toHaveLength(2)
  })

  it('com palavra-chave, a troca desta linha vem ANTES da palavra e do número do mês', async () => {
    const { escritas } = servidor({})
    renderLancamentos()
    await screen.findByText('Pão de Açúcar')

    const { user, editor } = await abrirParaTrocar()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-uber')
    const [ficha] = within(editor).getAllByRole('button', { name: /^Reconhecer por «/ })
    await user.click(ficha as HTMLElement)
    await user.click(within(editor).getByRole('button', { name: /^Trocar e reconhecer por «/ }))

    // A ordem importa: "«uber» adicionada a Uber" NÃO implica esta linha (no
    // modo categorizar, implicava), então a troca é dita primeiro.
    expect(
      await screen.findByText(
        /^Categoria trocada de Alimentação para Uber · «[^»]+» adicionada a Uber · mais 3 lançamentos de setembro categorizados\.$/,
      ),
    ).toBeInTheDocument()
    expect(escritas().map((c) => `${c.metodo} ${c.caminho}`)).toEqual([
      'PATCH /categories/cat-uber',
      'PATCH /transactions/tx-ok',
      'POST /transactions/auto-categorize',
    ])
  })

  it('Escape devolve o foco ao botão da linha e nada é gravado', async () => {
    const { escritas } = servidor({})
    renderLancamentos()
    await screen.findByText('Pão de Açúcar')

    const { user, editor } = await abrirParaTrocar()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-uber')
    await user.keyboard('{Escape}')

    expect(editor).not.toBeInTheDocument()
    expect(document.activeElement).toHaveAttribute('data-atalho', 'tx-ok')
    expect(escritas()).toHaveLength(0)
  })

  /** O cache do painel sob o mesmo prefixo (ADR-027) — regressão.
   *
   *  `['transactions']` é PREFIXO: sob ele moram o painel
   *  (`['transactions','dashboard',mes]`) e o relatório por categoria, que não
   *  são listas paginadas. Sem a guarda do `pages`, o `.map` lançava dentro do
   *  `onSuccess`, a mutação caía no `onError` e a pessoa via um toast de erro
   *  com o PATCH JÁ GRAVADO — o pior desfecho possível. */
  it('com o painel em cache, a gravação atualiza a lista e não toca no painel', async () => {
    const painel = {
      month: '2026-09',
      investmentNetCents: -25_000,
      incomeCents: 530_000,
      incomeCount: 3,
      creditCardExpenseCents: 120_000,
      creditCardExpenseCount: 7,
    }
    const { escritas } = servidor({})
    const { queryClient } = renderLancamentos('/lancamentos?mes=2026-09', (cliente) => {
      cliente.setQueryData(['transactions', 'dashboard', '2026-09'], painel)
      cliente.setQueryData(
        ['transactions', 'reports', 'by-category', { mes: '2026-09', kind: 'expense' }],
        { rows: [], totalCents: 0 },
      )
    })
    await screen.findByText('Pão de Açúcar')

    const { user, editor } = await abrirParaTrocar()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-uber')
    await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))

    // Sucesso, e não o toast de erro que o TypeError produzia.
    expect(
      await screen.findByText('Categoria trocada de Alimentação para Uber.'),
    ).toBeInTheDocument()
    expect(screen.queryByText(/Algo falhou do nosso lado/)).not.toBeInTheDocument()
    expect(escritas()).toHaveLength(1)

    // O dado do painel fica INTACTO — nenhuma tentativa de mapear `pages`.
    expect(queryClient.getQueryData(['transactions', 'dashboard', '2026-09'])).toEqual(painel)
    // E a lista recebeu a linha nova no mesmo frame.
    const linha = screen.getByText('Pão de Açúcar').closest('tr') as HTMLElement
    expect(within(linha).getAllByText('Uber').length).toBeGreaterThan(0)
  })

  /** A QUARTA forma sob o prefixo — regressão do achado C1 da revisão de
   *  segurança.
   *
   *  `['transactions','investments',mes]` (`features/investments/api`) é a
   *  única outra `InfiniteData` do prefixo: tem `pages` E tem `items`, então
   *  passa por qualquer guarda de FORMA. Enquanto só a lacuna era editável ela
   *  nunca continha a linha em edição (`InvestmentItem` exige `categoryId`);
   *  com a §19 a linha marcada como aporte virou editável e está lá.
   *
   *  Trocá-la por um `Transaction` apagaria o `flow` — a coluna "Movimento" de
   *  `/investimentos` ficaria em branco, a linha continuaria listada numa tela
   *  cuja premissa é "só aportes e resgates", e `monthly.contributionsCents`
   *  seguiria contando o valor. E o lixo FICA: a invalidação é
   *  `refetchType: 'active'` e a tela está desmontada. */
  it('com /investimentos em cache, trocar a categoria do aporte não encosta naquele cache', async () => {
    const VISAO_DE_INVESTIMENTOS = {
      pageParams: [null],
      pages: [
        {
          month: '2026-09',
          monthly: {
            contributionsCents: 200_000,
            contributionCount: 1,
            redemptionsCents: 0,
            redemptionCount: 0,
          },
          yearToDate: {
            contributionsCents: 200_000,
            contributionCount: 1,
            redemptionsCents: 0,
            redemptionCount: 0,
          },
          series: [{ month: '2026-09', contributionsCents: 200_000, redemptionsCents: 0 }],
          items: [
            {
              id: 'tx-aporte',
              occurredOn: '2026-09-12',
              flow: 'contribution',
              accountId: CONTA_CORRENTE,
              accountName: 'Conta corrente',
              categoryId: 'cat-cdb',
              categoryName: 'CDB',
              amountCents: 200_000,
              description: 'CDB Nubank',
              source: 'import',
            },
          ],
          nextCursor: null,
        },
      ],
    }
    // A foto de antes, desligada por valor: se a gravação escrever ali, a
    // comparação acusa mesmo que o objeto seja outro.
    const ANTES = JSON.parse(JSON.stringify(VISAO_DE_INVESTIMENTOS)) as unknown

    servidor({
      lancamentos: [
        lancamento({
          id: 'tx-aporte',
          description: 'CDB Nubank',
          amountCents: 200_000,
          categoryId: 'cat-cdb',
          categoryName: 'CDB',
        }),
      ],
    })
    // A chave literal, e não a fábrica de `features/investments`: import entre
    // features é proibido, e aqui o literal é o próprio objeto do teste — é a
    // forma da chave que precisa estar escrita.
    const { queryClient } = renderLancamentos('/lancamentos?mes=2026-09', (cliente) => {
      cliente.setQueryData(['transactions', 'investments', '2026-09'], VISAO_DE_INVESTIMENTOS)
    })
    await screen.findByText('CDB Nubank')

    const { user, editor } = await abrirParaTrocar(/^CDB\. Trocar categoria de CDB Nubank/)
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))

    expect(
      await screen.findByText('Categoria trocada de CDB para Alimentação.'),
    ).toBeInTheDocument()

    // Intacto: a MESMA referência (a query nem foi visitada) e o mesmo
    // conteúdo, campo a campo — inclusive o `flow`, que é o que some primeiro.
    const depois = queryClient.getQueryData(['transactions', 'investments', '2026-09'])
    expect(depois).toBe(VISAO_DE_INVESTIMENTOS)
    expect(depois).toEqual(ANTES)
    expect(JSON.stringify(depois)).toBe(JSON.stringify(ANTES))

    // E a lista de lançamentos recebeu a linha nova no mesmo frame.
    const linha = screen.getByText('CDB Nubank').closest('tr') as HTMLElement
    expect(within(linha).getAllByText('Alimentação').length).toBeGreaterThan(0)
  })

  it('sob investimentos, trocar para uma categoria de despesa tira a linha e o toast explica', async () => {
    servidor({
      lancamentos: [
        lancamento({
          id: 'tx-aporte',
          description: 'CDB Nubank',
          amountCents: 200_000,
          categoryId: 'cat-cdb',
          categoryName: 'CDB',
        }),
      ],
    })
    renderLancamentos('/lancamentos?mes=2026-09&tipo=investimentos')
    await screen.findByText('CDB Nubank')

    const { user, editor } = await abrirParaTrocar(/^CDB\. Trocar categoria de CDB Nubank/)
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))

    // Continua DE SUCESSO — nada falhou; e a linha não some em silêncio.
    expect(
      await screen.findByText(
        'Categoria trocada de CDB para Alimentação. Ele saiu da lista: é uma despesa, e a lista mostra só aportes e resgates.',
      ),
    ).toBeInTheDocument()
  })

  it('com a categoria atual arquivada, o campo abre no placeholder e a nota diz por quê', async () => {
    servidor({
      lancamentos: [
        lancamento({
          id: 'tx-arq',
          description: 'Mercado do seu José',
          // A árvore ativa não traz esta categoria: marcação existente
          // sobrevive ao arquivamento, atribuição nova não.
          categoryId: 'cat-arquivada',
          categoryName: 'Feira',
        }),
      ],
    })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { editor } = await abrirParaTrocar(/^Feira\. Trocar categoria de Mercado do seu José/)
    const campo = within(editor).getByRole('combobox')
    expect(campo).toHaveValue('')
    // O substantivo é explícito (`A categoria`) e nunca elíptico: nome injetado
    // em frase não governa concordância — com a semente de fábrica, `Salário`
    // produziria "está arquivada … escolhida" (docs/DESIGN.md (h) §2).
    const nota = await within(editor).findByText(
      'A categoria Feira está arquivada e não pode ser escolhida de novo.',
    )
    // C3: a frase existe para quem OUVE também. Quem chega ao campo pelo Tab
    // precisa ouvir por que a categoria atual não está na lista — e o `Select`
    // soma este id ao slot de mensagem dele, nunca o substitui.
    expect(nota.id).not.toBe('')
    expect(campo.getAttribute('aria-describedby')?.split(' ')).toContain(nota.id)
    expect(campo).toHaveAccessibleDescription(
      /A categoria Feira está arquivada e não pode ser escolhida de novo\./,
    )
    expect(within(editor).getByRole('button', { name: 'Escolha uma categoria' })).toHaveAttribute(
      'aria-disabled',
      'true',
    )
  })
})

// ---------------------------------------------------------------------------
// QA da emenda §19 (18/09/2026): o que o modo trocar deixou sem prova.
//
// A entrega cobriu o caminho feliz. O que falta é o que quebra primeiro: a
// nota do grupo que GANHOU filhas (a outra metade do critério (h)), o 409 da
// palavra que já é da categoria ANTERIOR — o caso mais frequente desta
// emenda —, o foco que precisa PULAR as linhas categorizadas, as saídas de
// lista sob `?tipo=` com o foco no vizinho, os erros do servidor que a UI só
// pode mostrar genéricos (404 forjado, 429) e o nome de categoria hostil.
// ---------------------------------------------------------------------------

describe('AtalhoDeCategoria — QA da emenda §19', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  /** Critério (h), a metade que faltava: a nota diz QUAL dos dois motivos é.
   *  Dizer "está arquivada" de um grupo que ganhou subcategorias mandaria a
   *  pessoa procurar em Arquivadas uma categoria que está lá, ativa. */
  it('a categoria atual virou grupo com subcategorias: a nota diz ISSO, e não "arquivada"', async () => {
    servidor({
      lancamentos: [
        lancamento({
          id: 'tx-grupo',
          description: 'Corrida do centro',
          // `grp-transporte` tem filha ativa (`cat-uber`): o seletor não o
          // oferece mais (§13), mas a marcação antiga continua de pé.
          categoryId: 'grp-transporte',
          categoryName: 'Transporte',
        }),
      ],
    })
    renderLancamentos()
    await screen.findByText('Corrida do centro')

    const { editor } = await abrirParaTrocar(/^Transporte\. Trocar categoria de Corrida do centro/)
    expect(within(editor).getByRole('combobox')).toHaveValue('')
    expect(
      await within(editor).findByText(
        'O grupo Transporte tem subcategorias e não recebe lançamento. Escolha uma delas.',
      ),
    ).toBeInTheDocument()
    // E nunca a frase do outro motivo: são dois estados diferentes do mundo.
    expect(within(editor).queryByText(/está arquivada/)).not.toBeInTheDocument()
    expect(within(editor).getByRole('button', { name: 'Escolha uma categoria' })).toHaveAttribute(
      'aria-disabled',
      'true',
    )
  })

  /** O caso frequente desta emenda: a palavra que a pessoa escolhe para a
   *  categoria NOVA é, muitas vezes, a que categorizou a linha na ANTERIOR —
   *  foi ela que trouxe a linha até aqui. O servidor responde 409, e a §19
   *  manda deixar tudo como estava. */
  it('409 com a palavra da categoria ANTERIOR: nada mais é feito e o rótulo volta a Trocar categoria', async () => {
    const { escritas } = servidor({
      aoPatchCategoria: () =>
        jsonResponse(409, {
          error: {
            code: 'KEYWORD_TAKEN',
            message: 'keyword already used',
            // A dona é a categoria de ONDE a linha está saindo.
            fields: { keyword: 'x', ownerId: 'cat-alimentacao' },
          },
        }),
    })
    renderLancamentos()
    await screen.findByText('Pão de Açúcar')

    const { user, editor } = await abrirParaTrocar()
    const select = within(editor).getByRole('combobox')
    await user.selectOptions(select, 'cat-uber')
    const [ficha] = within(editor).getAllByRole('button', { name: /^Reconhecer por «/ })
    const palavra = (ficha as HTMLElement).textContent ?? ''
    await user.click(ficha as HTMLElement)
    await user.click(within(editor).getByRole('button', { name: /^Trocar e reconhecer por «/ }))

    expect(await screen.findByText(`«${palavra}» já está em Alimentação.`)).toBeInTheDocument()
    // SÓ (a) foi tentada: nem o PATCH do lançamento, nem o auto-categorize.
    expect(escritas().map((c) => `${c.metodo} ${c.caminho}`)).toEqual([
      'PATCH /categories/cat-uber',
    ])
    // Editor aberto, escolha mantida, ficha fora, verbo de volta ao de trocar.
    expect(editor).toBeInTheDocument()
    expect(select).toHaveValue('cat-uber')
    expect(
      within(editor).queryByRole('button', { name: `Reconhecer por «${palavra}»` }),
    ).not.toBeInTheDocument()
    const confirmar = within(editor).getByRole('button', { name: 'Trocar categoria' })
    expect(confirmar).toHaveFocus()
    // E a linha continua onde estava.
    expect(screen.getAllByRole('button', { name: NOME_DA_CATEGORIZADA }).length).toBeGreaterThan(0)
  })

  /** Critério (e) no nível da TELA, com a linha categorizada NO MEIO — a
   *  regressão mais fácil de introduzir. Antes da §19 todo botão da célula era
   *  lacuna; hoje `data-atalho` está nos dois estados e só `data-lacuna`
   *  distingue. Trocar um pelo outro faz o foco parar na linha que já tem
   *  dona. */
  it('a lacuna entre linhas categorizadas: o foco PULA a categorizada e vai à próxima lacuna', async () => {
    servidor({
      lancamentos: [
        lancamento(),
        lancamento({
          id: 'tx-meio',
          description: 'Pão de Açúcar',
          categoryId: 'cat-alimentacao',
          categoryName: 'Alimentação',
        }),
        lancamento({ id: 'tx-3', description: 'Farmácia Popular', amountCents: 3_250 }),
      ],
    })
    renderLancamentos()
    await screen.findByText('Mercado do seu José')

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))

    await screen.findByText('Lançamento categorizado como Alimentação.')
    // A próxima PENDÊNCIA é `tx-3`, do outro lado da linha já categorizada.
    await waitFor(() => expect(lacuna(NOME_DA_LACUNA_3)).toHaveFocus())
    expect(document.activeElement).toHaveAttribute('data-atalho', 'tx-3')
    expect(document.activeElement).toHaveAttribute('data-lacuna')
  })

  /** Critério (f) sob `investimentos`, a natureza que a §19 acrescentou: um
   *  resgate que vira receita também sai da lista. */
  it('sob investimentos, trocar um resgate para categoria de RECEITA tira a linha e o toast fala em receita', async () => {
    servidor({
      filtrarPorTipo: true,
      lancamentos: [
        lancamento({
          id: 'tx-resgate',
          kind: 'income',
          description: 'Resgate CDB',
          amountCents: 150_000,
          categoryId: 'cat-resgates',
          categoryName: 'Resgates',
        }),
      ],
    })
    renderLancamentos('/lancamentos?mes=2026-09&tipo=investimentos')
    await screen.findByText('Resgate CDB')

    const { user, editor } = await abrirParaTrocar(/^Resgates\. Trocar categoria de Resgate CDB/)
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-salario')
    await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))

    expect(
      await screen.findByText(
        'Categoria trocada de Resgates para Salário. Ele saiu da lista: é uma receita, e a lista mostra só aportes e resgates.',
      ),
    ).toBeInTheDocument()
    // E a linha SAIU de verdade — o servidor de mentira honra o `kindGroup`.
    await waitFor(() => expect(screen.queryByText('Resgate CDB')).not.toBeInTheDocument())
  })

  /** Critério (f) sob `receitas` + o destino do foco quando a linha sai: o
   *  vizinho da FOTO de antes, nunca o <body>. */
  it('sob receitas, trocar para resgate tira a linha e o foco vai ao vizinho da foto', async () => {
    servidor({
      filtrarPorTipo: true,
      lancamentos: [
        lancamento({
          id: 'tx-salario',
          kind: 'income',
          description: 'Salário de setembro',
          amountCents: 900_000,
          categoryId: 'cat-salario',
          categoryName: 'Salário',
        }),
        lancamento({
          id: 'tx-bonus',
          kind: 'income',
          description: 'Bônus anual',
          amountCents: 300_000,
          categoryId: 'cat-salario',
          categoryName: 'Salário',
        }),
      ],
    })
    renderLancamentos('/lancamentos?mes=2026-09&tipo=receitas')
    await screen.findByText('Salário de setembro')

    const { user, editor } = await abrirParaTrocar(
      /^Salário\. Trocar categoria de Salário de setembro/,
    )
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-resgates')
    await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))

    expect(
      await screen.findByText(
        'Categoria trocada de Salário para Resgates. Ele saiu da lista: é um resgate, e a lista mostra só receitas.',
      ),
    ).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('Salário de setembro')).not.toBeInTheDocument())
    // O foco não some com a linha: vai ao vizinho que estava na foto.
    await waitFor(() => expect(document.activeElement).toHaveAttribute('data-atalho', 'tx-bonus'))
  })

  /** A mesma saída, sem vizinho nenhum: o foco cai no <h1> (não há
   *  `Carregar mais` — `nextCursor` é nulo). */
  it('sob despesas, a única linha que sai leva o foco ao título', async () => {
    servidor({
      filtrarPorTipo: true,
      lancamentos: [
        lancamento({
          id: 'tx-so',
          description: 'Aporte disfarçado',
          categoryId: 'cat-alimentacao',
          categoryName: 'Alimentação',
        }),
      ],
    })
    renderLancamentos('/lancamentos?mes=2026-09&tipo=despesas')
    await screen.findByText('Aporte disfarçado')

    const { user, editor } = await abrirParaTrocar(
      /^Alimentação\. Trocar categoria de Aporte disfarçado/,
    )
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-cdb')
    await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))

    expect(
      await screen.findByText(
        'Categoria trocada de Alimentação para CDB. Ele saiu da lista: é um aporte, e a lista mostra só despesas.',
      ),
    ).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Lançamentos' })).toHaveFocus())
  })

  /** Abuso: `categoryId` forjado de outra casa. O servidor responde 404
   *  genérico (igual ao inexistente — o teste de backend vive em
   *  `updatecategory_abuso_test.go`); o que ESTA camada tem de provar é que a
   *  tela não inventa detalhe e não fecha o editor sobre uma escrita que não
   *  aconteceu. */
  it('404 forjado no PATCH: mensagem genérica, editor aberto e a linha na categoria de antes', async () => {
    servidor({
      aoPatchLancamento: () =>
        jsonResponse(404, { error: { code: 'NOT_FOUND', message: 'not found' } }),
    })
    renderLancamentos()
    await screen.findByText('Pão de Açúcar')

    const { user, editor } = await abrirParaTrocar()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-uber')
    await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))

    expect(await screen.findByText('Não encontramos esta página.')).toBeInTheDocument()
    expect(editor).toBeInTheDocument()
    expect(within(editor).getByRole('button', { name: 'Trocar categoria' })).toHaveFocus()
    // A célula continua dizendo a verdade: a troca não aconteceu.
    expect(screen.getAllByRole('button', { name: NOME_DA_CATEGORIZADA })).toHaveLength(2)
  })

  /** Abuso: rajada de recategorização até o 429 por casa. A tela mostra o
   *  genérico do `messageForError` e deixa o editor aberto — a pessoa não
   *  perde a escolha por causa de um limite temporário. */
  it('429 na rajada: mensagem genérica de limite, editor aberto e escolha preservada', async () => {
    const { escritas } = servidor({
      aoPatchLancamento: () =>
        jsonResponse(429, { error: { code: 'RATE_LIMITED', message: 'slow down' } }),
    })
    renderLancamentos()
    await screen.findByText('Pão de Açúcar')

    const { user, editor } = await abrirParaTrocar()
    const select = within(editor).getByRole('combobox')
    await user.selectOptions(select, 'cat-uber')
    await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))

    expect(
      await screen.findByText('Muitas tentativas. Aguarde alguns minutos e tente de novo.'),
    ).toBeInTheDocument()
    expect(editor).toBeInTheDocument()
    expect(select).toHaveValue('cat-uber')
    // Uma tentativa, uma requisição: o erro não vira laço de repetição.
    expect(escritas()).toHaveLength(1)
  })

  /** XSS: o nome da categoria é dado da casa, e entra na tela em três lugares
   *  (filho, atributo e toast). Nos três é TEXTO — nunca HTML montado. */
  it('nome de categoria hostil aparece como texto literal no botão, no aria-label e no toast', async () => {
    const IMG = '<img src=x onerror="alert(1)">'
    const SCRIPT = '<script>alert(1)</script>'
    servidor({
      categorias: {
        expense: [
          categoria({ id: 'cat-img', name: IMG, kind: 'expense' }),
          categoria({ id: 'cat-script', name: SCRIPT, kind: 'expense' }),
        ],
        income: [],
        investment: [],
        redemption: [],
      },
      lancamentos: [
        lancamento({
          id: 'tx-xss',
          description: 'Compra suspeita',
          categoryId: 'cat-img',
          categoryName: IMG,
        }),
      ],
    })
    renderLancamentos()
    await screen.findByText('Compra suspeita')

    const botoes = screen.getAllByRole('button', {
      name: (nome: string) => nome.startsWith(`${IMG}. Trocar categoria de Compra suspeita,`),
    })
    expect(botoes).toHaveLength(2)
    const [botao] = botoes as [HTMLButtonElement]
    // Texto literal, e nenhum nó injetado.
    expect(botao.textContent).toContain(IMG)
    expect(botao.getAttribute('aria-label')).toContain(IMG)
    expect(botao.querySelector('img')).toBeNull()

    const user = userEvent.setup()
    await user.click(botao)
    const editor = await screen.findByRole('group', {
      name: /^Trocar categoria de Compra suspeita/,
    })
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-script')
    await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))

    expect(
      await screen.findByText(`Categoria trocada de ${IMG} para ${SCRIPT}.`),
    ).toBeInTheDocument()
    // Nada virou nó: nem script, nem imagem com handler.
    expect(document.querySelectorAll('script')).toHaveLength(0)
    expect(document.querySelectorAll('img[onerror]')).toHaveLength(0)
  })

  /** Borda: trocar duas vezes sem recarregar. O `de X` do segundo toast tem de
   *  ser a categoria NOVA — ler o `linha.categoryName` da renderização inicial
   *  faria o app dizer "de Alimentação" pela segunda vez. */
  it('trocar duas vezes seguidas: o segundo toast parte da categoria NOVA', async () => {
    servidor({})
    renderLancamentos()
    await screen.findByText('Pão de Açúcar')

    const primeira = await abrirParaTrocar()
    await primeira.user.selectOptions(within(primeira.editor).getByRole('combobox'), 'cat-uber')
    await primeira.user.click(
      within(primeira.editor).getByRole('button', { name: 'Trocar categoria' }),
    )
    await screen.findByText('Categoria trocada de Alimentação para Uber.')
    await waitFor(() => expect(document.activeElement).toHaveAttribute('data-atalho', 'tx-ok'))

    // Segunda troca, sem recarregar nada.
    const segunda = await abrirParaTrocar(/^Uber\. Trocar categoria de Pão de Açúcar/)
    // O editor reabre já na categoria ATUAL, que agora é a nova.
    expect(within(segunda.editor).getByRole('combobox')).toHaveValue('cat-uber')
    await segunda.user.selectOptions(within(segunda.editor).getByRole('combobox'), 'cat-padaria')
    await segunda.user.click(
      within(segunda.editor).getByRole('button', { name: 'Trocar categoria' }),
    )

    expect(await screen.findByText('Categoria trocada de Uber para Padaria.')).toBeInTheDocument()
    expect(screen.queryByText(/trocada de Alimentação para Padaria/)).not.toBeInTheDocument()
  })

  /** Borda do contrato: `categoryId` presente com `categoryName` nulo. A linha
   *  É categorizada — não pode virar pendência falsa —, mas não há nome para
   *  mostrar nem para citar. */
  it('categoryId com categoryName nulo não vira pendência: sem data-lacuna e com o verbo de trocar', async () => {
    servidor({
      lancamentos: [
        lancamento({
          id: 'tx-sem-nome',
          description: 'Compra sem nome',
          categoryId: 'cat-alimentacao',
          categoryName: null,
        }),
      ],
    })
    renderLancamentos()
    await screen.findByText('Compra sem nome')

    const botoes = screen.getAllByRole('button', {
      name: /^Sem categoria\. Trocar categoria de Compra sem nome/,
    })
    expect(botoes).toHaveLength(2)
    // O texto é o mesmo buraco de sempre, mas o ESTADO é "categorizada".
    expect(botoes[0]).not.toHaveAttribute('data-lacuna')
    expect(document.querySelectorAll('button[data-lacuna]')).toHaveLength(0)

    const { user, editor } = await abrirParaTrocar(
      /^Sem categoria\. Trocar categoria de Compra sem nome/,
    )
    // Modo trocar de verdade: o campo abre na categoria atual e o confirmar
    // recusa a mesma.
    expect(within(editor).getByRole('combobox')).toHaveValue('cat-alimentacao')
    expect(
      within(editor).getByRole('button', { name: 'Escolha outra categoria' }),
    ).toBeInTheDocument()

    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-uber')
    await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))
    // Sem a anterior resolvível, a frase ENCOLHE em vez de inventar um nome.
    expect(await screen.findByText('Categoria trocada para Uber.')).toBeInTheDocument()
  })

  /** Borda: outra aba trocou a categoria enquanto o editor estava aberto. O
   *  editor não pode gravar sobre premissa velha SEM SINAL — o campo intocado
   *  acompanha o dado novo, e o toast conta a verdade de agora. */
  it('a lista refetcha com a linha já em outra categoria: o editor acompanha e o toast não mente', async () => {
    const backend = servidor({})
    const { queryClient } = renderLancamentos()
    await screen.findByText('Pão de Açúcar')

    const { user, editor } = await abrirParaTrocar()
    const select = within(editor).getByRole('combobox')
    expect(select).toHaveValue('cat-alimentacao')

    // Outra aba trocou para Uber; a invalidação traz a lista nova.
    const alvo = backend.lancamentos.find((l) => l.id === 'tx-ok') as Record<string, unknown>
    alvo.categoryId = 'cat-uber'
    alvo.categoryName = 'Uber'
    await act(async () => {
      await queryClient.invalidateQueries({ queryKey: ['transactions'] })
    })

    // O SINAL: o campo intocado acompanha, e o confirmar recusa a mesma.
    await waitFor(() => expect(select).toHaveValue('cat-uber'))
    expect(
      within(editor).getByRole('button', { name: 'Escolha outra categoria' }),
    ).toBeInTheDocument()

    await user.selectOptions(select, 'cat-padaria')
    await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))
    // A premissa do toast é a de AGORA, não a de quando o editor abriu.
    expect(await screen.findByText('Categoria trocada de Uber para Padaria.')).toBeInTheDocument()
  })

  /** Borda: `?semCategoria=1`. Nenhuma linha categorizada aparece, então a
   *  recategorização não é oferecida ali — e a linha que acabou de ser
   *  categorizada não pode continuar contando como lacuna no cálculo do foco
   *  (a "lacuna fantasma"). */
  it('com ?semCategoria=1 não há controle de trocar, e a linha resolvida não vira lacuna fantasma', async () => {
    servidor({
      lancamentos: [
        lancamento(),
        lancamento({
          id: 'tx-meio',
          description: 'Pão de Açúcar',
          categoryId: 'cat-alimentacao',
          categoryName: 'Alimentação',
        }),
        lancamento({ id: 'tx-3', description: 'Farmácia Popular', amountCents: 3_250 }),
      ],
    })
    renderLancamentos('/lancamentos?mes=2026-09&semCategoria=1')
    await screen.findByText('Mercado do seu José')

    // A categorizada não está na tela, logo não há o que trocar.
    expect(screen.queryByText('Pão de Açúcar')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Trocar categoria de/ })).not.toBeInTheDocument()

    const { user, editor } = await abrirEditor()
    await user.selectOptions(within(editor).getByRole('combobox'), 'cat-alimentacao')
    await user.click(within(editor).getByRole('button', { name: 'Categorizar' }))

    await screen.findByText('Lançamento categorizado como Alimentação.')
    await waitFor(() => expect(screen.queryByText('Mercado do seu José')).not.toBeInTheDocument())
    // A resolvida sumiu da tela E da conta das lacunas — nada de fantasma.
    expect(document.querySelectorAll('button[data-lacuna="tx-1"]')).toHaveLength(0)
    await waitFor(() => expect(lacuna(NOME_DA_LACUNA_3)).toHaveFocus())
  })

  /** Borda: as DUAS instâncias do botão na mesma linha. Uma só é visível, e é
   *  para ela que o foco vai depois de gravar. No jsdom não há layout, então o
   *  teste define o `checkVisibility` que a tela consulta. */
  it('com a instância da coluna escondida, o foco depois de gravar vai para a VISÍVEL', async () => {
    const original = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'checkVisibility')
    Object.defineProperty(HTMLElement.prototype, 'checkVisibility', {
      configurable: true,
      writable: true,
      value(this: HTMLElement) {
        return this.dataset.instancia !== 'coluna'
      },
    })
    try {
      servidor({})
      renderLancamentos()
      await screen.findByText('Pão de Açúcar')

      const { user, editor } = await abrirParaTrocar()
      await user.selectOptions(within(editor).getByRole('combobox'), 'cat-uber')
      await user.click(within(editor).getByRole('button', { name: 'Trocar categoria' }))

      await screen.findByText('Categoria trocada de Alimentação para Uber.')
      await waitFor(() => expect(document.activeElement).toHaveAttribute('data-atalho', 'tx-ok'))
      // A da coluna está escondida nesta faixa: o foco tem de ser o da linha
      // secundária, e não um botão que ninguém vê.
      expect(document.activeElement).toHaveAttribute('data-instancia', 'secundaria')
    } finally {
      if (original) Object.defineProperty(HTMLElement.prototype, 'checkVisibility', original)
      else Reflect.deleteProperty(HTMLElement.prototype, 'checkVisibility')
    }
  })
})
