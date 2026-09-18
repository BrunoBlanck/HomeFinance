import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'
import { MSG_CATEGORIA_RECUSADA } from '@/lib/errors'

const fetchMock = vi.fn()

const LOTE = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8daa'
const CONTA_CORRENTE = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'
const CARTAO = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d66'
const NUBANK = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d77'
const ALIMENTACAO = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8c01'
const PADARIA = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8c02'

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
    {
      id: NUBANK,
      name: 'Nubank',
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

const CATEGORIAS = { income: [], expense: [] }

function categoria(over: Record<string, unknown> = {}) {
  return {
    id: ALIMENTACAO,
    name: 'Alimentação',
    kind: 'expense',
    parentId: null,
    keywords: [] as string[],
    archivedAt: null,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    children: [],
    ...over,
  }
}

/** Duas categorias de despesa, folhas: Alimentação (com «supermercado») e
 *  Padaria (com «padaria»). */
const CATEGORIAS_DE_DESPESA = {
  income: [],
  expense: [
    categoria({ keywords: ['supermercado'] }),
    categoria({ id: PADARIA, name: 'Padaria', keywords: ['padaria'] }),
  ],
}

function linha(over: Record<string, unknown> = {}) {
  return {
    id: 'row-nova',
    seq: 1,
    lineNo: 2,
    status: 'novo',
    defaultAction: 'import',
    allowedActions: ['import', 'skip'],
    kind: 'expense',
    occurredOn: '2026-08-12',
    amountCents: 1100,
    description: 'Pix para PADARIA EXEMPLO LTDA',
    externalId: null,
    rejectReason: null,
    matchTransactionId: null,
    suggestedCategoryId: null,
    matchScore: null,
    matchedKeyword: null,
    suggestedCounterpartAccountId: null,
    matchOccurredOn: null,
    ...over,
  }
}

const LOTE_BASE = {
  id: LOTE,
  status: 'pending',
  accountId: CONTA_CORRENTE,
  institution: 'nubank',
  docKind: 'checking_statement',
  formatId: 'nubank.checking.v1',
  fileName: 'Nubank_2026-09-13.csv',
  encoding: 'utf-8',
  rowCount: 4,
  minDate: '2026-08-01',
  maxDate: '2026-08-31',
  counts: {
    novo: 1,
    repetido_no_arquivo: 0,
    duplicado_exato: 1,
    duplicado_excluido: 0,
    possivel_duplicado: 1,
    pagamento_de_fatura: 1,
    transferencia_interna: 0,
    transferencia_ja_registrada: 0,
    rejeitado: 0,
  },
  outcome: null,
  statementSuggestion: null,
  sameContentImportedAt: null,
  expiresAt: '2026-09-14T00:00:00Z',
  createdAt: '2026-09-13T00:00:00Z',
  committedAt: null,
}

const LINHAS = [
  linha({
    id: 'row-fatura',
    seq: 1,
    status: 'pagamento_de_fatura',
    defaultAction: 'skip',
    allowedActions: ['skip', 'transfer', 'import'],
    amountCents: 285_982,
    occurredOn: '2026-08-07',
    description: 'Pagamento de fatura',
  }),
  linha({
    id: 'row-possivel',
    seq: 2,
    status: 'possivel_duplicado',
    defaultAction: 'skip',
    allowedActions: ['skip', 'import'],
    description: 'PADARIA EXEMPLO LTDA',
  }),
  linha({ id: 'row-nova', seq: 3 }),
  linha({
    id: 'row-dup',
    seq: 4,
    status: 'duplicado_exato',
    defaultAction: 'skip',
    allowedActions: [],
    amountCents: 285_982,
    kind: 'income',
    occurredOn: '2026-08-07',
    description: 'Pagamento recebido',
  }),
]

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

type RotasDaRevisao = Partial<{
  /** Função quando a recarga precisa devolver OUTRAS linhas que a primeira ida. */
  linhas: unknown[] | (() => unknown[])
  lote: unknown
  confirm: () => Response
  categorias: unknown
  patchCategoria: () => Response
}>

function rotearApi(over: RotasDaRevisao = {}) {
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    const alvo = new URL(String(url), 'https://app.invalido')
    const caminho = alvo.pathname.replace('/api/v1', '')

    if (caminho === '/me') return Promise.resolve(jsonResponse(200, SESSAO))
    if (caminho === '/accounts') return Promise.resolve(jsonResponse(200, CONTAS))
    if (caminho === '/categories' && metodo === 'GET') {
      return Promise.resolve(jsonResponse(200, over.categorias ?? CATEGORIAS))
    }
    if (caminho === `/imports/${LOTE}` && metodo === 'GET') {
      const itens = typeof over.linhas === 'function' ? over.linhas() : (over.linhas ?? LINHAS)
      return Promise.resolve(
        jsonResponse(200, {
          batch: over.lote ?? LOTE_BASE,
          items: itens,
          nextCursor: null,
        }),
      )
    }
    if (caminho === `/imports/${LOTE}/confirm` && metodo === 'POST') {
      if (over.confirm) return Promise.resolve(over.confirm())
      return Promise.resolve(
        jsonResponse(200, {
          id: LOTE,
          status: 'committed',
          imported: 1,
          restored: 0,
          skipped: 2,
          blocked: 0,
          rejected: 0,
          linked: 0,
          statementId: null,
          transfersCreated: 0,
          blockedRows: [],
        }),
      )
    }
    if (caminho.startsWith('/categories/') && metodo === 'PATCH') {
      if (over.patchCategoria) return Promise.resolve(over.patchCategoria())
      return Promise.resolve(jsonResponse(200, {}))
    }
    throw new Error(`rota não declarada no teste: ${metodo} ${caminho}`)
  })
}

function corpoDoConfirm(): Record<string, unknown> {
  const chamada = fetchMock.mock.calls.find((c) =>
    String(c[0]).includes(`/imports/${LOTE}/confirm`),
  )
  if (!chamada) throw new Error('o confirm não foi chamado')
  return JSON.parse(String((chamada[1] as RequestInit).body)) as Record<string, unknown>
}

function renderRevisao() {
  const router = createAppRouter(
    createMemoryHistory({ initialEntries: [`/importar/${LOTE}/revisar`] }),
  )
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

describe('ImportReviewScreen', () => {
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

  it('organiza os sete status em TRÊS blocos de trabalho', async () => {
    rotearApi()
    renderRevisao()

    expect(
      await screen.findByRole('heading', { name: 'Precisam da sua decisão' }),
    ).toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Prontas para importar' })).toBeInTheDocument()
    expect(screen.getByText(/Ficam de fora · 1 linha/)).toBeInTheDocument()

    // A palavra do grupo é a explicação, escrita uma vez para o grupo inteiro.
    expect(screen.getByRole('rowheader', { name: /Pagamento de fatura · 1/ })).toBeInTheDocument()
    expect(screen.getByRole('rowheader', { name: /Possível duplicata · 1/ })).toBeInTheDocument()
  })

  it('mantém o foco no <h1> quando o esqueleto dá lugar à revisão', async () => {
    // As duas fases renderizam árvores diferentes: o h1 do carregamento é
    // DESMONTADO quando os dados chegam. Sem refocar, o foco cai no <body> e
    // quem usa teclado recomeça do topo justo quando a tela ficou útil.
    rotearApi()
    renderRevisao()

    await screen.findByRole('heading', { name: 'Precisam da sua decisão' })
    expect(
      screen.getByRole('heading', { level: 1, name: 'Revisar o que vai entrar' }),
    ).toHaveFocus()
  })

  it('o controle de decisão é um <select> com PALAVRAS, não uma etiqueta colorida', async () => {
    rotearApi()
    renderRevisao()

    const decisao = await screen.findByRole('combobox', {
      name: /Decisão para PADARIA EXEMPLO LTDA, 12\/08/,
    })

    // A primeira opção é o default do SERVIDOR, e o texto diz a consequência.
    const opcoes = within(decisao)
      .getAllByRole('option')
      .map((o) => o.textContent)
    expect(opcoes).toEqual(['Não importar (é a mesma)', 'Importar assim mesmo (é outra)'])
  })

  it('o bloco "ficam de fora" não tem controle nenhum — nem desabilitado', async () => {
    rotearApi()
    renderRevisao()

    await screen.findByText(/Ficam de fora · 1 linha/)
    const detalhes = screen.getByText(/Ficam de fora · 1 linha/).closest('details')
    expect(detalhes).not.toBeNull()

    const escopo = within(detalhes as HTMLElement)
    expect(escopo.queryByRole('checkbox')).not.toBeInTheDocument()
    expect(escopo.queryByRole('combobox')).not.toBeInTheDocument()
    // Nem escondido atrás de `disabled`: checkbox desabilitado tem contraste
    // ruim e some para parte das tecnologias assistivas.
    expect((detalhes as HTMLElement).querySelector('input')).toBeNull()
    expect((detalhes as HTMLElement).querySelector('select')).toBeNull()
  })

  it('cada checkbox e cada select tem nome com descrição, data e valor', async () => {
    rotearApi()
    renderRevisao()

    // Nunca só "Importar": em 59 linhas, o nome precisa dizer QUAL linha.
    // `\s` e não um espaço literal: o Intl separa "R$" do número com espaço
    // NÃO quebrável (U+00A0), que é o certo — e que não casa com " ".
    expect(
      await screen.findByRole('checkbox', {
        name: /^Importar Pix para PADARIA EXEMPLO LTDA, 12\/08, R\$\s11,00 negativos$/,
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('combobox', {
        name: /^Decisão para Pagamento de fatura, 07\/08, R\$\s2\.859,82 negativos$/,
      }),
    ).toBeInTheDocument()
  })

  it('o botão de confirmar diz o que vai acontecer', async () => {
    rotearApi()
    renderRevisao()

    // Defaults do servidor: 1 nova entra, fatura e possível duplicata são
    // barradas — e a linha já importada nem conta como "ignorada por você".
    expect(
      await screen.findByRole('button', { name: 'Importar 1 lançamento · 2 ignorados' }),
    ).toBeInTheDocument()
  })

  it('manda no confirm APENAS as exceções ao default do servidor', async () => {
    rotearApi()
    renderRevisao()

    const decisao = await screen.findByRole('combobox', {
      name: /Decisão para PADARIA EXEMPLO LTDA/,
    })
    await userEvent.selectOptions(decisao, 'import')

    await userEvent.click(
      await screen.findByRole('button', { name: 'Importar 2 lançamentos · 1 ignorado' }),
    )

    await waitFor(() => {
      expect(corpoDoConfirm()).toEqual({
        // Só a linha que a pessoa liberou. A linha "nova" aceita o default e
        // não é citada; a "já importada" nunca pode ser citada.
        decisions: [{ rowId: 'row-possivel', action: 'import' }],
      })
    })
  })

  it('a transferência exige a conta de destino antes de enviar', async () => {
    rotearApi()
    renderRevisao()

    const decisao = await screen.findByRole('combobox', {
      name: /Decisão para Pagamento de fatura/,
    })
    await userEvent.selectOptions(decisao, 'transfer')

    // Escolher a transferência revela o segundo seletor na MESMA célula.
    expect(
      await screen.findByRole('combobox', {
        name: /Conta de destino da transferência de Pagamento de fatura/,
      }),
    ).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /^Importar 2 lançamentos/ }))

    // Sem destino o confirm nem sai: 400 aqui derrubaria o lote inteiro.
    expect(
      await screen.findByText(
        'Escolha a conta de destino de cada linha marcada como transferência.',
      ),
    ).toBeInTheDocument()
    expect(fetchMock.mock.calls.some((c) => String(c[0]).includes('/confirm'))).toBe(false)
  })

  it('com nada marcado usa aria-disabled e explica ao clicar — nunca disabled', async () => {
    rotearApi()
    renderRevisao()

    const marcada = await screen.findByRole('checkbox', { name: /Importar Pix para PADARIA/ })
    await userEvent.click(marcada)

    const botao = await screen.findByRole('button', { name: 'Nada marcado para importar' })
    // O botão continua focável e clicável: quem chega nele pelo teclado precisa
    // descobrir POR QUE não pode prosseguir.
    expect(botao).toHaveAttribute('aria-disabled', 'true')
    expect(botao).not.toBeDisabled()

    await userEvent.click(botao)
    expect(
      await screen.findByText('Marque ao menos uma linha, ou cancele a importação.'),
    ).toBeInTheDocument()
  })

  it('avisa a nuance de restauração na barra de confirmação', async () => {
    rotearApi({
      linhas: [
        linha({
          id: 'row-excluida',
          status: 'duplicado_excluido',
          defaultAction: 'skip',
          allowedActions: ['skip', 'import'],
          description: 'Resgate RDB',
          kind: 'income',
          amountCents: 1_028_757,
          occurredOn: '2026-08-25',
        }),
      ],
    })
    renderRevisao()

    const decisao = await screen.findByRole('combobox', { name: /Decisão para Resgate RDB/ })
    expect(
      within(decisao)
        .getAllByRole('option')
        .map((o) => o.textContent),
    ).toEqual(['Não importar', 'Restaurar o lançamento que eu excluí'])

    await userEvent.selectOptions(decisao, 'import')
    expect(await screen.findByText('Inclui 1 lançamento restaurado.')).toBeInTheDocument()
  })

  it('rollback por colisão não é "concluída": fica na revisão e recarrega as linhas', async () => {
    // Os dois moradores da casa confirmam o mesmo lote ao mesmo tempo. O índice
    // único recusa, o servidor DESFAZ a transação inteira e responde 200 com
    // `status: "pending"`, `imported: 0` e `blocked: N` — nada foi gravado, e o
    // staging continua vivo pelas 24 h do lote.
    let idasAsLinhas = 0
    rotearApi({
      linhas: () => {
        idasAsLinhas += 1
        if (idasAsLinhas === 1) return LINHAS
        // Na recarga, a linha que colidiu já não pode entrar: quem a gravou foi
        // o outro lote.
        return LINHAS.map((l) =>
          l.id === 'row-nova'
            ? { ...l, status: 'duplicado_exato', defaultAction: 'skip', allowedActions: [] }
            : l,
        )
      },
      confirm: () =>
        jsonResponse(200, {
          id: LOTE,
          status: 'pending',
          imported: 0,
          restored: 0,
          skipped: 0,
          blocked: 1,
          rejected: 0,
          linked: 0,
          statementId: null,
          transfersCreated: 0,
          blockedRows: [{ rowId: 'row-nova', reason: 'duplicado_exato' }],
        }),
    })
    const router = renderRevisao()

    await userEvent.click(
      await screen.findByRole('button', { name: 'Importar 1 lançamento · 2 ignorados' }),
    )

    expect(
      await screen.findByText(/A revisão foi recarregada: confira o que mudou e confirme de novo/),
    ).toBeInTheDocument()
    expect(screen.getByText('Outra importação gravou estas linhas primeiro')).toBeInTheDocument()

    // Regressão: a tela NÃO pode dizer que concluiu o que não concluiu, nem
    // mandar enviar o arquivo de novo — o lote desta pessoa continua pendente.
    expect(router.state.location.pathname).toBe(`/importar/${LOTE}/revisar`)
    expect(screen.queryByText('Importação concluída')).not.toBeInTheDocument()
    expect(screen.queryByText('Nada foi importado.')).not.toBeInTheDocument()

    // E a revisão foi recarregada: a linha que colidiu caiu para "ficam de fora".
    await waitFor(() => {
      expect(screen.getByText(/Ficam de fora · 2 linhas/)).toBeInTheDocument()
    })
    expect(idasAsLinhas).toBeGreaterThan(1)
  })

  /** Emenda §13 da spec 0005: grupo com subcategoria ativa não recebe
   *  lançamento. O seletor de cada linha já filtra esses grupos, então o 422 é
   *  de CORRIDA — o grupo ganhou uma filha depois que esta tela leu a árvore.
   *  O confirm é tudo-ou-nada: nada foi gravado e o lote continua vivo. */
  it('422 em fields.categoryId: erro do passo com a frase da §13, sem sair da revisão', async () => {
    rotearApi({
      confirm: () =>
        jsonResponse(422, {
          error: {
            code: 'VALIDATION_FAILED',
            message: 'x',
            // A prosa do servidor não é o que a tela mostra: a frase sai de
            // `src/lib/errors.ts`.
            fields: { categoryId: 'Este grupo tem subcategorias. Escolha uma subcategoria.' },
          },
        }),
    })
    const router = renderRevisao()

    const categoriasAte = () =>
      fetchMock.mock.calls.filter((c) => String(c[0]).includes('/categories?')).length
    await screen.findByRole('heading', { name: 'Precisam da sua decisão' })
    const antes = categoriasAte()

    await userEvent.click(
      await screen.findByRole('button', { name: 'Importar 1 lançamento · 2 ignorados' }),
    )

    expect(await screen.findByText(MSG_CATEGORIA_RECUSADA)).toBeInTheDocument()
    // NÃO cai no genérico de 422.
    expect(
      screen.queryByText('Confira os dados informados e tente de novo.'),
    ).not.toBeInTheDocument()
    // A pessoa continua na revisão, com o lote inteiro, para corrigir a linha.
    expect(router.state.location.pathname).toBe(`/importar/${LOTE}/revisar`)

    // E as categorias são recarregadas: sem isso o seletor da linha ofereceria
    // de novo o grupo que o servidor acabou de recusar.
    await waitFor(() => expect(categoriasAte()).toBeGreaterThan(antes))
  })

  it('volta para o começo quando a análise não existe mais', async () => {
    fetchMock.mockImplementation((url: string, init?: RequestInit) => {
      const metodo = init?.method ?? 'GET'
      const caminho = new URL(String(url), 'https://app.invalido').pathname.replace('/api/v1', '')
      if (caminho === '/me') return Promise.resolve(jsonResponse(200, SESSAO))
      if (caminho === '/accounts') return Promise.resolve(jsonResponse(200, CONTAS))
      if (caminho === '/categories') return Promise.resolve(jsonResponse(200, CATEGORIAS))
      if (caminho.startsWith('/imports')) {
        return Promise.resolve(jsonResponse(404, { error: { code: 'NOT_FOUND', message: 'x' } }))
      }
      throw new Error(`rota não declarada: ${metodo} ${caminho}`)
    })
    const router = renderRevisao()

    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/importar')
    })
    expect(
      await screen.findByText('Esta análise não existe mais. Envie o arquivo de novo.'),
    ).toBeInTheDocument()
  })
  describe('transferências detectadas (spec 0005)', () => {
    const INTERNA_1 = linha({
      id: 'row-int-1',
      seq: 10,
      status: 'transferencia_interna',
      defaultAction: 'skip',
      allowedActions: ['skip', 'transfer', 'import'],
      description: 'Pix enviado NUBANK',
      amountCents: 250_000,
      occurredOn: '2026-08-05',
      suggestedCounterpartAccountId: NUBANK,
      matchScore: 88,
      matchedKeyword: 'nubank',
    })
    const INTERNA_2 = linha({
      id: 'row-int-2',
      seq: 11,
      status: 'transferencia_interna',
      defaultAction: 'skip',
      allowedActions: ['skip', 'transfer', 'import'],
      description: 'Transferência para NUBANK',
      amountCents: 80_000,
      occurredOn: '2026-08-20',
      suggestedCounterpartAccountId: NUBANK,
      matchScore: 100,
      matchedKeyword: 'nubank',
    })
    const REGISTRADA = linha({
      id: 'row-reg',
      seq: 12,
      status: 'transferencia_ja_registrada',
      defaultAction: 'link',
      allowedActions: ['link', 'skip'],
      description: 'Pix recebido NUBANK',
      kind: 'income',
      amountCents: 50_000,
      occurredOn: '2026-08-09',
      suggestedCounterpartAccountId: NUBANK,
      matchTransactionId: '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8e01',
      matchOccurredOn: '2026-08-10',
    })
    const COM_TRANSFERENCIAS = [...LINHAS, INTERNA_1, INTERNA_2, REGISTRADA]

    it('é o SEGUNDO bloco e entra na linha de contagem do topo', async () => {
      rotearApi({ linhas: COM_TRANSFERENCIAS })
      renderRevisao()

      await screen.findByRole('heading', { name: 'Transferências detectadas' })
      const blocos = new Set([
        'Precisam da sua decisão',
        'Transferências detectadas',
        'Prontas para importar',
      ])
      const titulos = screen
        .getAllByRole('heading', { level: 2 })
        .map((h) => h.textContent ?? '')
        .filter((t) => blocos.has(t))
      expect(titulos).toEqual([
        'Precisam da sua decisão',
        'Transferências detectadas',
        'Prontas para importar',
      ])
      expect(
        screen.getByText(
          '2 precisam da sua decisão · 3 transferências detectadas · 1 prontas para importar · 1 ficam de fora.',
        ),
      ).toBeInTheDocument()

      // Os dois grupos, com a palavra do léxico e a explicação.
      expect(
        screen.getByRole('rowheader', { name: /Parece transferência · 2/ }),
      ).toBeInTheDocument()
      expect(
        screen.getByRole('rowheader', { name: /Já registrada como transferência · 1/ }),
      ).toBeInTheDocument()
    })

    it('a evidência fala da conta pelo NOME, com a pontuação em texto — e 100 some', async () => {
      rotearApi({ linhas: COM_TRANSFERENCIAS })
      renderRevisao()

      expect(
        await screen.findByText('Parece transferência para Nubank · 88% · «nubank»'),
      ).toBeInTheDocument()
      expect(screen.getByText('Parece transferência para Nubank · «nubank»')).toBeInTheDocument()
      expect(
        screen.getByText('Já registrada em 10/08 como transferência com Nubank.'),
      ).toBeInTheDocument()
      expect(document.body.textContent).not.toContain(NUBANK)
    })

    it('a contraparte sugerida mora no texto da opção; "outra conta…" revela o segundo select', async () => {
      rotearApi({ linhas: COM_TRANSFERENCIAS })
      renderRevisao()

      const decisao = await screen.findByRole('combobox', {
        name: /Decisão para Pix enviado NUBANK/,
      })
      expect(
        within(decisao)
          .getAllByRole('option')
          .map((o) => o.textContent),
      ).toEqual([
        'Não importar',
        'Registrar como transferência para Nubank',
        'Registrar como transferência para outra conta…',
        'Importar como despesa comum (não é transferência)',
      ])

      // Aceitar a sugerida NÃO abre segundo select.
      await userEvent.selectOptions(decisao, 'transfer')
      expect(
        screen.queryByRole('combobox', {
          name: /Conta de destino da transferência de Pix enviado/,
        }),
      ).not.toBeInTheDocument()

      // "Outra conta…" abre, sem a conta do lote entre as opções.
      await userEvent.selectOptions(decisao, 'transfer:outra')
      const destino = await screen.findByRole('combobox', {
        name: /Conta de destino da transferência de Pix enviado/,
      })
      expect(
        within(destino)
          .getAllByRole('option')
          .map((o) => o.textContent),
      ).toEqual(['Escolha a conta', 'Cartão C6', 'Nubank'])

      // Sem escolher, o confirm nem sai.
      await userEvent.click(screen.getByRole('button', { name: /^Importar 2 lançamentos/ }))
      expect(await screen.findByText('Escolha a conta de destino.')).toBeInTheDocument()
      expect(fetchMock.mock.calls.some((c) => String(c[0]).includes('/confirm'))).toBe(false)
    })

    it('"aceitar todas" marca transfer SÓ nas transferencia_interna do bloco', async () => {
      rotearApi({ linhas: COM_TRANSFERENCIAS })
      renderRevisao()

      await userEvent.click(
        await screen.findByRole('button', { name: 'Aceitar as 2 transferências sugeridas' }),
      )
      // Vira o desfazer, quiet.
      expect(screen.getByRole('button', { name: 'Desfazer o aceite de todas' })).toBeInTheDocument()

      // 1 nova + 2 transferências entram; 1 vínculo; fatura + possível ignoradas.
      expect(
        screen.getByText('Inclui 2 transferências e 1 vínculo a transferência já registrada.'),
      ).toBeInTheDocument()
      await userEvent.click(
        screen.getByRole('button', { name: 'Importar 3 lançamentos · 1 vinculado · 2 ignorados' }),
      )

      await waitFor(() => {
        expect(corpoDoConfirm()).toEqual({
          // Só as duas internas: sem counterpartAccountId (o servidor usa a
          // sugerida). A já registrada aceita o default `link` e não é citada;
          // a fatura e a possível duplicata continuam no default delas.
          decisions: [
            { rowId: 'row-int-1', action: 'transfer' },
            { rowId: 'row-int-2', action: 'transfer' },
          ],
        })
      })
    })

    it('"desfazer o aceite" volta todas a não importar', async () => {
      rotearApi({ linhas: COM_TRANSFERENCIAS })
      renderRevisao()

      await userEvent.click(
        await screen.findByRole('button', { name: 'Aceitar as 2 transferências sugeridas' }),
      )
      await userEvent.click(screen.getByRole('button', { name: 'Desfazer o aceite de todas' }))
      expect(
        screen.getByRole('button', { name: 'Aceitar as 2 transferências sugeridas' }),
      ).toBeInTheDocument()
      expect(
        screen.getByRole('button', { name: 'Importar 1 lançamento · 1 vinculado · 4 ignorados' }),
      ).toBeInTheDocument()
    })

    it('link é o default da já registrada e conta como "vinculado", não como importado', async () => {
      rotearApi({ linhas: [REGISTRADA] })
      renderRevisao()

      const decisao = await screen.findByRole('combobox', {
        name: /Decisão para Pix recebido NUBANK/,
      })
      expect(
        within(decisao)
          .getAllByRole('option')
          .map((o) => o.textContent),
      ).toEqual(['Vincular à transferência já registrada', 'Não importar'])

      // Só vínculos: o rótulo é "Vincular", nunca "Importar 0".
      await userEvent.click(screen.getByRole('button', { name: 'Vincular 1 lançamento' }))
      await waitFor(() => {
        expect(corpoDoConfirm()).toEqual({ decisions: [] })
      })
    })

    it('o botão de aceitar some quando o bloco só tem já registradas', async () => {
      rotearApi({ linhas: [REGISTRADA] })
      renderRevisao()

      await screen.findByRole('heading', { name: 'Transferências detectadas' })
      expect(screen.queryByRole('button', { name: /Aceitar/ })).not.toBeInTheDocument()
    })
  })

  describe('categoria sugerida (spec 0005 §4.2.2)', () => {
    const SUGERIDA = linha({
      id: 'row-sug',
      seq: 20,
      description: 'SUPERMERCADO BOM PRECO',
      suggestedCategoryId: ALIMENTACAO,
      matchScore: 88,
      matchedKeyword: 'supermercado',
    })

    it('vem selecionada no select, com a proveniência em texto embaixo', async () => {
      rotearApi({ linhas: [SUGERIDA], categorias: CATEGORIAS_DE_DESPESA })
      renderRevisao()

      const categoria_ = await screen.findByRole('combobox', {
        name: /Categoria de SUPERMERCADO BOM PRECO/,
      })
      expect(categoria_).toHaveValue(ALIMENTACAO)
      // Placeholder é "Sem categoria", não um travessão: limpar é uma decisão.
      expect(within(categoria_).getByRole('option', { name: 'Sem categoria' })).toBeInTheDocument()

      const origem = screen.getByText('88% · «supermercado»')
      expect(origem).toHaveTextContent('Sugerida pela palavra-chave 88% · «supermercado»')
      // Nenhuma etiqueta "sugerida": a sugestão vive no controle.
      expect(screen.queryByText(/^sugerida$/i)).not.toBeInTheDocument()

      // Aceita sem mexer: nenhuma decisão viaja.
      await userEvent.click(screen.getByRole('button', { name: 'Importar 1 lançamento' }))
      await waitFor(() => {
        expect(corpoDoConfirm()).toEqual({ decisions: [] })
      })
    })

    it('limpar a sugestão manda categoryId: null e apaga a proveniência', async () => {
      rotearApi({ linhas: [SUGERIDA], categorias: CATEGORIAS_DE_DESPESA })
      renderRevisao()

      const categoria_ = await screen.findByRole('combobox', {
        name: /Categoria de SUPERMERCADO BOM PRECO/,
      })
      await userEvent.selectOptions(categoria_, '')
      expect(screen.queryByText('88% · «supermercado»')).not.toBeInTheDocument()

      await userEvent.click(screen.getByRole('button', { name: 'Importar 1 lançamento' }))
      await waitFor(() => {
        expect(corpoDoConfirm()).toEqual({
          decisions: [{ rowId: 'row-sug', action: 'import', categoryId: null }],
        })
      })
    })

    it('trocar manda o valor; voltar à sugerida traz a proveniência e omite o campo', async () => {
      rotearApi({ linhas: [SUGERIDA], categorias: CATEGORIAS_DE_DESPESA })
      renderRevisao()

      const categoria_ = await screen.findByRole('combobox', {
        name: /Categoria de SUPERMERCADO BOM PRECO/,
      })
      await userEvent.selectOptions(categoria_, PADARIA)
      expect(screen.queryByText('88% · «supermercado»')).not.toBeInTheDocument()
      // Linha COM sugestão não ganha ficha de aprender, mesmo trocada.
      expect(screen.queryByText('Da próxima vez, reconhecer por')).not.toBeInTheDocument()

      await userEvent.selectOptions(categoria_, ALIMENTACAO)
      expect(screen.getByText('88% · «supermercado»')).toBeInTheDocument()

      await userEvent.selectOptions(categoria_, PADARIA)
      await userEvent.click(screen.getByRole('button', { name: 'Importar 1 lançamento' }))
      await waitFor(() => {
        expect(corpoDoConfirm()).toEqual({
          decisions: [{ rowId: 'row-sug', action: 'import', categoryId: PADARIA }],
        })
      })
    })
  })

  describe('fichas de aprender (spec 0005 §4.2.2)', () => {
    function corpoDoPatch(): Record<string, unknown> {
      const chamada = fetchMock.mock.calls.find(
        (c) =>
          String(c[0]).includes(`/categories/${ALIMENTACAO}`) &&
          (c[1] as RequestInit | undefined)?.method === 'PATCH',
      )
      if (!chamada) throw new Error('o PATCH não foi chamado')
      return JSON.parse(String((chamada[1] as RequestInit).body)) as Record<string, unknown>
    }

    it('aparecem só depois de escolher categoria numa linha SEM sugestão', async () => {
      rotearApi({ linhas: [linha()], categorias: CATEGORIAS_DE_DESPESA })
      renderRevisao()

      const categoria_ = await screen.findByRole('combobox', {
        name: /Categoria de Pix para PADARIA EXEMPLO LTDA/,
      })
      expect(screen.queryByText('Da próxima vez, reconhecer por')).not.toBeInTheDocument()

      await userEvent.selectOptions(categoria_, ALIMENTACAO)
      expect(screen.getByText('Da próxima vez, reconhecer por')).toBeInTheDocument()
      // "para" e "ltda" são palavras vazias e não viram ficha.
      expect(
        screen.getByRole('button', { name: 'Adicionar «pix» às palavras-chave de Alimentação' }),
      ).toBeInTheDocument()
      expect(
        screen.getByRole('button', {
          name: 'Adicionar «padaria» às palavras-chave de Alimentação',
        }),
      ).toBeInTheDocument()
      expect(
        screen.getByRole('button', {
          name: 'Adicionar «exemplo» às palavras-chave de Alimentação',
        }),
      ).toBeInTheDocument()
      expect(screen.queryByRole('button', { name: /«para»|«ltda»/ })).not.toBeInTheDocument()

      // Voltar a "Sem categoria" apaga as fichas.
      await userEvent.selectOptions(categoria_, '')
      expect(screen.queryByText('Da próxima vez, reconhecer por')).not.toBeInTheDocument()
    })

    it('clicar manda o PATCH com a lista INTEIRA, vira texto e devolve o foco ao select', async () => {
      rotearApi({
        linhas: [linha()],
        categorias: CATEGORIAS_DE_DESPESA,
        patchCategoria: () =>
          jsonResponse(200, categoria({ keywords: ['supermercado', 'exemplo'] })),
      })
      renderRevisao()

      const categoria_ = await screen.findByRole('combobox', {
        name: /Categoria de Pix para PADARIA EXEMPLO LTDA/,
      })
      await userEvent.selectOptions(categoria_, ALIMENTACAO)
      await userEvent.click(
        screen.getByRole('button', {
          name: 'Adicionar «exemplo» às palavras-chave de Alimentação',
        }),
      )

      expect(
        await screen.findByText(
          '«exemplo» adicionada a Alimentação. Vale a partir da próxima importação.',
        ),
      ).toBeInTheDocument()
      // A lista inteira lida do cache, mais a palavra — o PATCH substitui.
      expect(corpoDoPatch()).toEqual({ keywords: ['supermercado', 'exemplo'] })

      // A ficha virou texto estático; o foco foi para o select da linha.
      expect(
        screen.queryByRole('button', {
          name: 'Adicionar «exemplo» às palavras-chave de Alimentação',
        }),
      ).not.toBeInTheDocument()
      expect(screen.getByText('exemplo')).toBeInTheDocument()
      expect(categoria_).toHaveFocus()

      // Nunca re-analisa o lote: nenhuma ida nova a /imports.
      const idasAoLote = fetchMock.mock.calls.filter((c) =>
        String(c[0]).includes(`/imports/${LOTE}`),
      )
      expect(idasAoLote).toHaveLength(1)
    })

    it('409 diz QUEM já tem a palavra, pelo nome', async () => {
      rotearApi({
        linhas: [linha()],
        categorias: CATEGORIAS_DE_DESPESA,
        patchCategoria: () =>
          jsonResponse(409, {
            error: {
              code: 'KEYWORD_TAKEN',
              message: 'x',
              fields: { keyword: 'padaria', ownerId: PADARIA },
            },
          }),
      })
      renderRevisao()

      const categoria_ = await screen.findByRole('combobox', {
        name: /Categoria de Pix para PADARIA EXEMPLO LTDA/,
      })
      await userEvent.selectOptions(categoria_, ALIMENTACAO)
      await userEvent.click(
        screen.getByRole('button', {
          name: 'Adicionar «padaria» às palavras-chave de Alimentação',
        }),
      )

      expect(await screen.findByText('«padaria» já está em Padaria.')).toBeInTheDocument()
      expect(document.body.textContent).not.toContain(PADARIA)
      // A ficha continua lá: a pessoa pode tentar outra.
      expect(
        screen.getByRole('button', {
          name: 'Adicionar «padaria» às palavras-chave de Alimentação',
        }),
      ).toBeInTheDocument()
    })

    it('nenhuma ficha quando a categoria já está em 20 de 20', async () => {
      const cheia = categoria({
        keywords: Array.from({ length: 20 }, (_, i) => `palavra${i + 1}`),
      })
      rotearApi({ linhas: [linha()], categorias: { income: [], expense: [cheia] } })
      renderRevisao()

      const categoria_ = await screen.findByRole('combobox', {
        name: /Categoria de Pix para PADARIA EXEMPLO LTDA/,
      })
      await userEvent.selectOptions(categoria_, ALIMENTACAO)
      expect(screen.queryByText('Da próxima vez, reconhecer por')).not.toBeInTheDocument()
    })
  })
})
