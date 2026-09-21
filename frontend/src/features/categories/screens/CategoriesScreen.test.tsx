import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'

const fetchMock = vi.fn()

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

type CategoriaFalsa = {
  id: string
  name: string
  kind: 'income' | 'expense' | 'investment' | 'redemption'
  parentId?: string | null
  archivedAt?: string | null
  keywords?: string[]
  children?: CategoriaFalsa[]
}

function cat(over: CategoriaFalsa) {
  return {
    parentId: null,
    archivedAt: null,
    keywords: [],
    children: [],
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...over,
  }
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

/** Corpo JSON da primeira chamada com o método informado.
 *
 *  Lança quando não houve chamada, em vez de deixar o encadeamento opcional
 *  virar `undefined.body` — um teste que falha por TypeError esconde o que
 *  realmente deu errado. */
function corpoDaChamada(metodo: string): Record<string, unknown> {
  const chamada = fetchMock.mock.calls.find(
    (c) => (c[1] as RequestInit | undefined)?.method === metodo,
  )
  if (!chamada) throw new Error(`nenhuma chamada ${metodo} foi feita`)
  return JSON.parse(String((chamada[1] as RequestInit).body)) as Record<string, unknown>
}

function rotearApi(rotas: Record<string, () => Response>) {
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    const caminho = String(url).replace('/api/v1', '')
    const handler = rotas[`${metodo} ${caminho}`] ?? rotas[`${metodo} ${caminho.split('?')[0]}`]
    if (!handler) throw new Error(`rota não declarada no teste: ${metodo} ${caminho}`)
    return Promise.resolve(handler())
  })
}

function renderCategorias() {
  const router = createAppRouter(createMemoryHistory({ initialEntries: ['/categorias'] }))
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

/** O título do aviso da troca de natureza — E7 (l). Achar o aviso por ELE, e
 *  não por `getByRole`, é o que mantém o teste honesto agora que ele é
 *  `role="status"`: o `KeywordsField` também tem um `role="status"` (o contador
 *  de fichas), e um seletor ambíguo passaria a valer por acidente. */
const TITULO_DO_AVISO = 'Isto muda os totais de meses já fechados'

const ARVORE_PADRAO = {
  expense: [
    cat({
      id: 'grp-moradia',
      name: 'Moradia',
      kind: 'expense',
      children: [
        cat({ id: 'sub-energia', name: 'Energia', kind: 'expense', parentId: 'grp-moradia' }),
        cat({ id: 'sub-agua', name: 'Água', kind: 'expense', parentId: 'grp-moradia' }),
      ],
    }),
    cat({ id: 'grp-lazer', name: 'Lazer', kind: 'expense' }),
  ],
  income: [cat({ id: 'grp-salario', name: 'Salário', kind: 'income' })],
  // As duas naturezas da E7 (ADR-029a) entram na semente e ganham seção
  // própria na tela — mesma árvore, mesmo diálogo, mesmo KeywordsField.
  investment: [
    cat({
      id: 'grp-investimentos',
      name: 'Investimentos',
      kind: 'investment',
      children: [
        cat({
          id: 'sub-cdb',
          name: 'CDB',
          kind: 'investment',
          parentId: 'grp-investimentos',
          keywords: ['cdb'],
        }),
      ],
    }),
  ],
  redemption: [cat({ id: 'grp-resgates', name: 'Resgates', kind: 'redemption' })],
}

describe('CategoriesScreen', () => {
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

  it('desenha a árvore de dois níveis separada pelas quatro naturezas', async () => {
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
    })
    renderCategorias()

    // Espera o CONTEÚDO, não o <h1>: o título já está na tela durante o
    // carregamento, então esperar por ele consultaria a árvore cedo demais.
    await screen.findByText('Moradia')

    const despesas = screen.getByRole('region', { name: 'Despesas' })
    const receitas = screen.getByRole('region', { name: 'Receitas' })
    const investimentos = screen.getByRole('region', { name: 'Investimentos' })
    const resgates = screen.getByRole('region', { name: 'Resgates' })

    expect(within(despesas).getByText('Moradia')).toBeInTheDocument()
    expect(within(despesas).getByText('Lazer')).toBeInTheDocument()
    expect(within(receitas).getByText('Salário')).toBeInTheDocument()
    // O grupo da semente tem o mesmo nome da seção — o título e o grupo.
    expect(within(investimentos).getAllByText('Investimentos')).toHaveLength(2)
    expect(within(investimentos).getByText('CDB')).toBeInTheDocument()
    expect(within(resgates).getAllByText('Resgates')).toHaveLength(2)

    // As naturezas nunca se misturam.
    expect(within(receitas).queryByText('Moradia')).not.toBeInTheDocument()
    expect(within(despesas).queryByText('CDB')).not.toBeInTheDocument()
    expect(within(investimentos).queryByText('Lazer')).not.toBeInTheDocument()
  })

  // E7 (l): as duas naturezas novas se definem POR CONTRASTE com as duas que a
  // pessoa já conhece, junto de cada bloco — e não empilhadas no apoio do <h1>,
  // que continua falando de dois níveis, arquivar e excluir.
  it('cada painel ensina a natureza pelo subtítulo, em construção paralela', async () => {
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
    })
    renderCategorias()

    await screen.findByText('Moradia')

    const subtitulo = (secao: string) =>
      within(screen.getByRole('region', { name: secao })).getByText(/conta/)
    expect(subtitulo('Despesas')).toHaveTextContent('Sai da conta e é gasto.')
    expect(subtitulo('Receitas')).toHaveTextContent('Entra na conta e é ganho.')
    expect(subtitulo('Investimentos')).toHaveTextContent('Sai da conta, mas não é gasto.')
    expect(subtitulo('Resgates')).toHaveTextContent('Entra na conta, mas não é ganho.')

    // O apoio do <h1> não muda.
    expect(screen.getByText(/Dois níveis: um grupo e, dentro dele/)).toBeInTheDocument()
  })

  // Critério de aceite 1 da spec 0006: o CRUD das naturezas novas acontece
  // pela tela, sem seção de código nova.
  it('seção vazia de uma natureza nova diz o nome dela', async () => {
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () =>
        jsonResponse(200, { ...ARVORE_PADRAO, investment: [], redemption: [] }),
    })
    renderCategorias()

    await screen.findByText('Moradia')
    const investimentos = screen.getByRole('region', { name: 'Investimentos' })
    expect(
      within(investimentos).getByText('Nenhum grupo de investimento ainda.'),
    ).toBeInTheDocument()
    const resgates = screen.getByRole('region', { name: 'Resgates' })
    expect(within(resgates).getByText('Nenhum grupo de resgate ainda.')).toBeInTheDocument()
  })

  // A hierarquia precisa existir na SEMÂNTICA, não só no recuo visual: é a
  // lista aninhada que faz o leitor de tela dizer "lista de 2 itens, dentro de
  // Moradia".
  it('as subcategorias ficam numa lista aninhada dentro do grupo', async () => {
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
    })
    renderCategorias()

    await screen.findByText('Moradia')

    const itemMoradia = screen.getByText('Moradia').closest('li')
    expect(itemMoradia).not.toBeNull()
    const aninhada = within(itemMoradia as HTMLElement).getAllByRole('list')
    expect(aninhada).toHaveLength(1)
    expect(within(aninhada[0] as HTMLElement).getByText('Energia')).toBeInTheDocument()
    expect(within(aninhada[0] as HTMLElement).getByText('Água')).toBeInTheDocument()
  })

  it('cria um grupo escolhendo a natureza', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
      'POST /categories': () =>
        jsonResponse(201, cat({ id: 'novo', name: 'Pets', kind: 'expense' })),
    })
    renderCategorias()

    await screen.findByRole('heading', { name: 'Categorias' })
    await user.click(screen.getByRole('button', { name: 'Novo grupo' }))

    expect(await screen.findByRole('heading', { name: 'Novo grupo' })).toBeInTheDocument()
    await user.type(screen.getByLabelText('Nome'), 'Pets')
    await user.click(screen.getByRole('button', { name: 'Criar' }))

    await waitFor(() => {
      const corpo = corpoDaChamada('POST')
      // `keywords` vai sempre, inteira — aqui vazia: o formulário é dono da lista.
      expect(corpo).toEqual({ name: 'Pets', kind: 'expense', keywords: [] })
    })
  })

  // Critério de aceite 1 da spec 0006.
  it('o seletor oferece as quatro naturezas e cria um grupo de investimento', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
      'POST /categories': () =>
        jsonResponse(201, cat({ id: 'novo', name: 'Previdência', kind: 'investment' })),
    })
    renderCategorias()

    await screen.findByRole('heading', { name: 'Categorias' })
    await user.click(screen.getByRole('button', { name: 'Novo grupo' }))
    await screen.findByRole('heading', { name: 'Novo grupo' })

    const natureza = screen.getByLabelText('Natureza')
    expect(
      Array.from(natureza.querySelectorAll('option')).map((o) => [o.value, o.textContent]),
    ).toEqual([
      ['expense', 'Despesa'],
      ['income', 'Receita'],
      ['investment', 'Investimento'],
      ['redemption', 'Resgate'],
    ])

    await user.type(screen.getByLabelText('Nome'), 'Previdência')
    await user.selectOptions(natureza, 'investment')
    await user.click(screen.getByRole('button', { name: 'Criar' }))

    await waitFor(() => {
      expect(corpoDaChamada('POST')).toEqual({
        name: 'Previdência',
        kind: 'investment',
        keywords: [],
      })
    })
  })

  // Critério de aceite 4 da spec 0006: a palavra-chave é única por CASA, entre
  // as quatro naturezas — e o 409 cai na ficha, não num alerta genérico.
  it('palavra-chave repetida entre naturezas diferentes cai na ficha com o nome da dona', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
      'POST /categories': () =>
        jsonResponse(409, {
          error: {
            code: 'KEYWORD_TAKEN',
            message: 'keyword already used',
            // A dona é uma categoria de INVESTIMENTO: resolvê-la exige varrer
            // as quatro naturezas do cache.
            fields: { keyword: 'cdb', ownerId: 'sub-cdb' },
          },
        }),
    })
    renderCategorias()

    await screen.findByRole('heading', { name: 'Categorias' })
    await user.click(screen.getByRole('button', { name: 'Novo grupo' }))
    await screen.findByRole('heading', { name: 'Novo grupo' })

    const dialogo = screen.getByRole('dialog')
    await user.type(within(dialogo).getByLabelText('Nome'), 'Banco')
    await user.type(within(dialogo).getByLabelText('Palavras-chave'), 'cdb{Enter}')
    await user.click(screen.getByRole('button', { name: 'Criar' }))

    expect(await within(dialogo).findByText('«cdb» já está em CDB.')).toBeInTheDocument()
    expect(within(dialogo).queryByText('sub-cdb')).not.toBeInTheDocument()
    const fichas = within(within(dialogo).getByRole('list')).getAllByRole('listitem')
    expect(fichas[0]).toHaveAttribute('data-invalid', 'true')
  })

  // Spec 0005 §4.1: o campo é o ÚLTIMO do formulário e a lista vai inteira.
  it('cadastra palavras-chave no grupo novo e as manda no corpo', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
      'POST /categories': () =>
        jsonResponse(
          201,
          cat({ id: 'novo', name: 'Alimentação', kind: 'expense', keywords: ['padaria'] }),
        ),
    })
    renderCategorias()

    await screen.findByRole('heading', { name: 'Categorias' })
    await user.click(screen.getByRole('button', { name: 'Novo grupo' }))
    await screen.findByRole('heading', { name: 'Novo grupo' })

    const dialogo = screen.getByRole('dialog')
    // Último campo do formulário, com a dica da categoria.
    const campos = within(dialogo).getAllByRole('textbox')
    expect(campos[campos.length - 1]).toBe(within(dialogo).getByLabelText('Palavras-chave'))
    expect(within(dialogo).getByText(/Prefira o nome do estabelecimento/)).toBeInTheDocument()

    await user.type(within(dialogo).getByLabelText('Nome'), 'Alimentação')
    await user.type(within(dialogo).getByLabelText('Palavras-chave'), 'padaria{Enter}uber,')
    await user.click(screen.getByRole('button', { name: 'Criar' }))

    await waitFor(() => {
      expect(corpoDaChamada('POST')).toEqual({
        name: 'Alimentação',
        kind: 'expense',
        keywords: ['padaria', 'uber'],
      })
    })
  })

  it('editar traz as palavras-chave da categoria e o PATCH manda a lista inteira', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () =>
        jsonResponse(200, {
          ...ARVORE_PADRAO,
          expense: [
            ...ARVORE_PADRAO.expense,
            cat({
              id: 'grp-transporte',
              name: 'Transporte',
              kind: 'expense',
              keywords: ['uber', '99'],
            }),
          ],
        }),
      'PATCH /categories/grp-transporte': () =>
        jsonResponse(200, cat({ id: 'grp-transporte', name: 'Transporte', kind: 'expense' })),
    })
    renderCategorias()

    await screen.findByText('Transporte')
    await user.click(screen.getByRole('button', { name: 'Editar Transporte' }))
    await screen.findByRole('heading', { name: 'Editar categoria' })

    const dialogo = screen.getByRole('dialog')
    expect(within(dialogo).getByRole('button', { name: 'Remover uber' })).toBeInTheDocument()
    expect(within(dialogo).getByRole('status')).toHaveTextContent('2 de 20')

    // Remove «99» e acrescenta «taxi»: o corpo é a lista RESULTANTE, não um diff.
    await user.click(within(dialogo).getByRole('button', { name: 'Remover 99' }))
    await user.type(within(dialogo).getByLabelText('Palavras-chave'), 'taxi{Enter}')
    await user.click(screen.getByRole('button', { name: 'Salvar' }))

    await waitFor(() => {
      const corpo = corpoDaChamada('PATCH')
      expect(corpo.keywords).toEqual(['uber', 'taxi'])
    })
  })

  // Spec 0005 §4.1.2: a mesma palavra em duas categorias da casa é 409, e a
  // tela diz QUAL e ONDE — resolvendo a dona pelo cache, nunca pelo id cru.
  it('409 KEYWORD_TAKEN marca a ficha e diz em qual categoria a palavra já está', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
      'POST /categories': () =>
        jsonResponse(409, {
          error: {
            code: 'KEYWORD_TAKEN',
            message: 'keyword already used',
            fields: { keyword: 'luz', ownerId: 'sub-energia' },
          },
        }),
    })
    renderCategorias()

    await screen.findByRole('heading', { name: 'Categorias' })
    await user.click(screen.getByRole('button', { name: 'Novo grupo' }))
    await screen.findByRole('heading', { name: 'Novo grupo' })

    const dialogo = screen.getByRole('dialog')
    await user.type(within(dialogo).getByLabelText('Nome'), 'Contas da casa')
    await user.type(within(dialogo).getByLabelText('Palavras-chave'), 'agua{Enter}Luz{Enter}')
    await user.click(screen.getByRole('button', { name: 'Criar' }))

    // A frase é nossa, com a palavra como foi digitada e o NOME da dona.
    expect(await within(dialogo).findByText('«Luz» já está em Energia.')).toBeInTheDocument()
    expect(within(dialogo).queryByText('sub-energia')).not.toBeInTheDocument()
    expect(within(dialogo).queryByText('keyword already used')).not.toBeInTheDocument()

    const fichas = within(within(dialogo).getByRole('list')).getAllByRole('listitem')
    expect(fichas[0]).not.toHaveAttribute('data-invalid')
    expect(fichas[1]).toHaveAttribute('data-invalid', 'true')

    // Remover a ficha marcada limpa o erro; o diálogo continua aberto.
    await user.click(within(dialogo).getByRole('button', { name: 'Remover Luz' }))
    expect(within(dialogo).queryByText('«Luz» já está em Energia.')).not.toBeInTheDocument()
    expect(screen.getByRole('heading', { name: 'Novo grupo' })).toBeInTheDocument()
  })

  it('409 sem dona resolvível diz "outra categoria desta casa"', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
      'POST /categories': () =>
        jsonResponse(409, {
          error: { code: 'KEYWORD_TAKEN', message: 'x', fields: { keyword: 'luz' } },
        }),
    })
    renderCategorias()

    await screen.findByRole('heading', { name: 'Categorias' })
    await user.click(screen.getByRole('button', { name: 'Novo grupo' }))
    await screen.findByRole('heading', { name: 'Novo grupo' })

    const dialogo = screen.getByRole('dialog')
    await user.type(within(dialogo).getByLabelText('Nome'), 'Contas da casa')
    await user.type(within(dialogo).getByLabelText('Palavras-chave'), 'luz{Enter}')
    await user.click(screen.getByRole('button', { name: 'Criar' }))

    expect(
      await within(dialogo).findByText('«luz» já está em outra categoria desta casa.'),
    ).toBeInTheDocument()
  })

  it('400 fields.keywords[i] marca a ficha i com a frase do projeto', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
      'POST /categories': () =>
        jsonResponse(400, {
          error: {
            code: 'VALIDATION_FAILED',
            message: 'x',
            fields: { 'keywords[1]': 'invalid' },
          },
        }),
    })
    renderCategorias()

    await screen.findByRole('heading', { name: 'Categorias' })
    await user.click(screen.getByRole('button', { name: 'Novo grupo' }))
    await screen.findByRole('heading', { name: 'Novo grupo' })

    const dialogo = screen.getByRole('dialog')
    await user.type(within(dialogo).getByLabelText('Nome'), 'Pets')
    await user.type(within(dialogo).getByLabelText('Palavras-chave'), 'petz{Enter}cobasi{Enter}')
    await user.click(screen.getByRole('button', { name: 'Criar' }))

    expect(
      await within(dialogo).findByText(/«cobasi» não é uma palavra-chave válida/),
    ).toBeInTheDocument()
    const fichas = within(within(dialogo).getByRole('list')).getAllByRole('listitem')
    expect(fichas[1]).toHaveAttribute('data-invalid', 'true')
  })

  // Invariante 4 da spec 0003: a subcategoria HERDA a natureza do grupo, e o
  // cliente nem manda o campo — quem decide é o servidor.
  // Spec 0005 §12: grupo com subcategoria ATIVA não recebe lançamento, então
  // palavra-chave nele nunca sugeriria nada — o diálogo esconde o campo.
  it('grupo com subcategorias ativas esconde as palavras-chave e explica onde elas ficam', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
    })
    renderCategorias()

    await screen.findByText('Moradia')
    await user.click(screen.getByRole('button', { name: 'Editar Moradia' }))
    await screen.findByRole('heading', { name: 'Editar categoria' })

    const dialogo = screen.getByRole('dialog')
    expect(within(dialogo).queryByLabelText('Palavras-chave')).not.toBeInTheDocument()
    expect(within(dialogo).getByText('Palavras-chave ficam nas subcategorias.')).toBeInTheDocument()

    // Grupo sem filhas e subcategoria continuam com o campo.
    await user.click(within(dialogo).getByRole('button', { name: 'Cancelar' }))
    await user.click(screen.getByRole('button', { name: 'Editar Lazer' }))
    await screen.findByRole('heading', { name: 'Editar categoria' })
    expect(within(screen.getByRole('dialog')).getByLabelText('Palavras-chave')).toBeInTheDocument()
  })

  it('grupo cujas filhas estão todas arquivadas volta a aceitar palavras-chave', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () =>
        jsonResponse(200, {
          ...ARVORE_PADRAO,
          expense: [
            cat({
              id: 'grp-antigo',
              name: 'Antigo',
              kind: 'expense',
              children: [
                cat({
                  id: 'sub-velha',
                  name: 'Velha',
                  kind: 'expense',
                  parentId: 'grp-antigo',
                  archivedAt: '2026-01-02T00:00:00Z',
                }),
              ],
            }),
          ],
        }),
    })
    renderCategorias()

    await screen.findByText('Antigo')
    await user.click(screen.getByRole('button', { name: 'Editar Antigo' }))
    await screen.findByRole('heading', { name: 'Editar categoria' })

    const dialogo = screen.getByRole('dialog')
    expect(within(dialogo).getByLabelText('Palavras-chave')).toBeInTheDocument()
    expect(within(dialogo).queryByText(/ficam nas subcategorias/)).not.toBeInTheDocument()
  })

  it('caso residual: grupo com palavras que ganhou filhas mantém o campo para limpar e conta as inertes', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () =>
        jsonResponse(200, {
          ...ARVORE_PADRAO,
          expense: [
            cat({
              id: 'grp-casa',
              name: 'Casa',
              kind: 'expense',
              keywords: ['luz', 'agua'],
              children: [
                cat({ id: 'sub-gas', name: 'Gás', kind: 'expense', parentId: 'grp-casa' }),
              ],
            }),
          ],
        }),
      'PATCH /categories/grp-casa': () =>
        jsonResponse(400, {
          error: {
            code: 'VALIDATION_FAILED',
            message: 'x',
            fields: { keywords: 'Palavras-chave ficam nas subcategorias.' },
          },
        }),
    })
    renderCategorias()

    await screen.findByText('Casa')
    await user.click(screen.getByRole('button', { name: 'Editar Casa' }))
    await screen.findByRole('heading', { name: 'Editar categoria' })

    const dialogo = screen.getByRole('dialog')
    expect(within(dialogo).getByLabelText('Palavras-chave')).toBeInTheDocument()
    expect(
      within(dialogo).getByText(
        '2 palavras-chave sem efeito enquanto o grupo tiver subcategorias.',
      ),
    ).toBeInTheDocument()

    // Remover uma atualiza a contagem; a última faz a nota virar a frase base,
    // e o campo continua na tela até o diálogo fechar.
    await user.click(within(dialogo).getByRole('button', { name: 'Remover agua' }))
    expect(
      within(dialogo).getByText('1 palavra-chave sem efeito enquanto o grupo tiver subcategorias.'),
    ).toBeInTheDocument()

    // Tentar salvar com palavra: o 400 `fields.keywords` cai NO CAMPO, com a
    // frase nossa — não num alerta geral.
    await user.click(screen.getByRole('button', { name: 'Salvar' }))
    await waitFor(() => {
      expect(corpoDaChamada('PATCH').keywords).toEqual(['luz'])
    })
    expect(
      await within(dialogo).findAllByText('Palavras-chave ficam nas subcategorias.'),
    ).not.toHaveLength(0)
    expect(within(dialogo).queryByRole('alert')).not.toBeInTheDocument()
    expect(within(dialogo).getByLabelText('Palavras-chave')).toHaveAccessibleDescription(
      /ficam nas subcategorias/,
    )
  })

  it('subcategoria não escolhe receita/despesa: herda do grupo', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
      'POST /categories': () =>
        jsonResponse(
          201,
          cat({ id: 'novo', name: 'Gás', kind: 'expense', parentId: 'grp-moradia' }),
        ),
    })
    renderCategorias()

    await screen.findByText('Moradia')
    await user.click(screen.getByRole('button', { name: 'Nova subcategoria em Moradia' }))

    expect(await screen.findByRole('heading', { name: 'Nova subcategoria' })).toBeInTheDocument()
    // Sem seletor de natureza na tela...
    expect(screen.queryByLabelText('Natureza')).not.toBeInTheDocument()
    // ...e a tela EXPLICA a herança em vez de deixar a pessoa adivinhar, numa
    // frase que continua concordando com as quatro naturezas.
    expect(screen.getByText(/Vai ficar dentro de/)).toHaveTextContent(
      'Vai ficar dentro de Moradia e herda do grupo a natureza despesa.',
    )

    await user.type(screen.getByLabelText('Nome'), 'Gás')
    await user.click(screen.getByRole('button', { name: 'Criar' }))

    await waitFor(() => {
      const corpo = corpoDaChamada('POST')
      expect(corpo).toEqual({ name: 'Gás', parentId: 'grp-moradia', keywords: [] })
      expect(corpo).not.toHaveProperty('kind')
    })
  })

  // Critério de aceite 2 da spec 0003.
  it('grupo com filhas não se exclui, e a tela diz o caminho', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
      'DELETE /categories/grp-moradia': () =>
        jsonResponse(422, { error: { code: 'RESOURCE_IN_USE', message: 'em uso' } }),
    })
    renderCategorias()

    await screen.findByText('Moradia')
    await user.click(screen.getByRole('button', { name: 'Excluir Moradia' }))

    expect(await screen.findByText('Não dá para excluir esta categoria')).toBeInTheDocument()
    expect(screen.getByText(/tem subcategorias/)).toBeInTheDocument()
    expect(screen.getByText(/arquive o grupo inteiro/)).toBeInTheDocument()
    expect(screen.getByText('Moradia')).toBeInTheDocument()
  })

  it('grupo sem filhas mas em uso oferece arquivar', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
      'DELETE /categories/grp-lazer': () =>
        jsonResponse(422, { error: { code: 'RESOURCE_IN_USE', message: 'em uso' } }),
    })
    renderCategorias()

    await screen.findByText('Lazer')
    await user.click(screen.getByRole('button', { name: 'Excluir Lazer' }))

    expect(await screen.findByText(/"Lazer" está em uso/)).toBeInTheDocument()
    expect(screen.getByText(/Arquive-a/)).toBeInTheDocument()
  })

  it('arquiva um grupo', async () => {
    const user = userEvent.setup()
    let arquivado = false
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () =>
        jsonResponse(200, arquivado ? { ...ARVORE_PADRAO, expense: [] } : ARVORE_PADRAO),
      'POST /categories/grp-lazer/archive': () => {
        arquivado = true
        return jsonResponse(200, cat({ id: 'grp-lazer', name: 'Lazer', kind: 'expense' }))
      },
    })
    renderCategorias()

    await screen.findByText('Lazer')
    await user.click(screen.getByRole('button', { name: 'Arquivar Lazer' }))

    expect(await screen.findByText('Categoria arquivada.')).toBeInTheDocument()
  })

  // ADR-029c: grupo COM filhas passa a poder trocar de natureza — é o caminho
  // de quem já tem uma categoria cheia de lançamentos. A cascata é dita ANTES
  // de confirmar, porque descobrir depois pelo relatório é descobrir do pior
  // jeito.
  it('grupo com filhas troca de natureza dentro do mesmo lado, avisando da cascata', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
      'PATCH /categories/grp-moradia': () =>
        jsonResponse(200, cat({ id: 'grp-moradia', name: 'Moradia', kind: 'investment' })),
    })
    renderCategorias()

    await screen.findByText('Moradia')
    await user.click(screen.getByRole('button', { name: 'Editar Moradia' }))
    await screen.findByRole('heading', { name: 'Editar categoria' })

    const dialogo = screen.getByRole('dialog')
    const natureza = within(dialogo).getByLabelText('Natureza')
    // Sem troca escolhida, nenhum aviso: avisar do que não vai acontecer é ruído.
    expect(within(dialogo).queryByText(TITULO_DO_AVISO)).not.toBeInTheDocument()

    await user.selectOptions(natureza, 'investment')

    const aviso = within(dialogo).getByText(TITULO_DO_AVISO).closest('[role="status"]')
    // `live="polite"`: o aviso monta enquanto a pessoa ainda está no <select>,
    // e um live region assertivo interromperia o anúncio da opção escolhida.
    expect(aviso).not.toBeNull()
    expect(within(dialogo).queryByRole('alert')).not.toBeInTheDocument()
    // "continuam na lista e no saldo da conta" vem primeiro e é literal: o medo
    // real é "meus lançamentos vão sumir", e a spec garante que o saldo não muda.
    expect(aviso).toHaveTextContent(
      'Os lançamentos de Moradia continuam na lista e no saldo da conta, mas saem dos totais de despesa e passam a contar como aportes — em todos os meses, não só neste.',
    )
    expect(aviso).toHaveTextContent('As subcategorias, inclusive as arquivadas, mudam junto.')
    expect(aviso).toHaveTextContent('Dá para voltar atrás pelo mesmo caminho.')

    // Voltar à natureza original apaga o aviso: não há mais troca para avisar.
    await user.selectOptions(natureza, 'expense')
    expect(within(dialogo).queryByText(TITULO_DO_AVISO)).not.toBeInTheDocument()

    await user.selectOptions(natureza, 'investment')
    await user.click(screen.getByRole('button', { name: 'Salvar' }))

    await waitFor(() => {
      expect(corpoDaChamada('PATCH')).toEqual({
        name: 'Moradia',
        kind: 'investment',
        keywords: [],
      })
    })
  })

  it('grupo sem filhas avisa da troca sem prometer cascata que não existe', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
    })
    renderCategorias()

    await screen.findByText('Lazer')
    await user.click(screen.getByRole('button', { name: 'Editar Lazer' }))
    await screen.findByRole('heading', { name: 'Editar categoria' })

    const dialogo = screen.getByRole('dialog')
    expect(within(dialogo).getByLabelText('Natureza')).toBeInTheDocument()
    await user.selectOptions(within(dialogo).getByLabelText('Natureza'), 'investment')

    const aviso = within(dialogo).getByText(TITULO_DO_AVISO).closest('[role="status"]')
    expect(aviso).toHaveTextContent('saem dos totais de despesa e passam a contar como aportes')
    expect(aviso).not.toHaveTextContent('subcategorias')
  })

  // Cruzar o lado do dinheiro não ganha aviso: quem sabe se há lançamento
  // pendurado é o servidor. A recusa chega em `fields.kind` e cai NO CAMPO.
  it('trocar de despesa para receita numa categoria em uso mostra o 422 no campo', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
      'PATCH /categories/grp-lazer': () =>
        jsonResponse(422, {
          error: {
            code: 'VALIDATION_FAILED',
            message: 'x',
            fields: { kind: 'Não dá para mudar receita/despesa de uma categoria em uso.' },
          },
        }),
    })
    renderCategorias()

    await screen.findByText('Lazer')
    await user.click(screen.getByRole('button', { name: 'Editar Lazer' }))
    await screen.findByRole('heading', { name: 'Editar categoria' })

    const dialogo = screen.getByRole('dialog')
    await user.selectOptions(within(dialogo).getByLabelText('Natureza'), 'income')
    // Cruzar o lado não é avisado de antemão — a autoridade é o servidor.
    expect(within(dialogo).queryByText(TITULO_DO_AVISO)).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Salvar' }))

    expect(
      await within(dialogo).findByText(
        /Não dá para trocar entre receita e despesa numa categoria em uso ou com subcategorias/,
      ),
    ).toBeInTheDocument()
    // A frase ensina a troca que CONTINUA valendo, senão ensinaria o oposto
    // da regra nova.
    expect(within(dialogo).getByLabelText('Natureza')).toHaveAccessibleDescription(
      /Entre despesa e investimento, ou entre receita e resgate, a troca vale/,
    )
    expect(screen.getByRole('heading', { name: 'Editar categoria' })).toBeInTheDocument()
  })

  it('subcategoria não troca de natureza e a tela explica por quê', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
    })
    renderCategorias()

    await screen.findByText('Energia')
    await user.click(screen.getByRole('button', { name: 'Editar Energia' }))

    await screen.findByRole('heading', { name: 'Editar categoria' })
    expect(screen.queryByLabelText('Natureza')).not.toBeInTheDocument()
    // Imperativo: quem abre o diálogo da folha está procurando o campo que não
    // está lá, e precisa de instrução, não de descrição.
    expect(screen.getByText(/Subcategoria acompanha o grupo/)).toHaveTextContent(
      'Subcategoria acompanha o grupo: troque a natureza no grupo, e as filhas vão junto.',
    )
  })

  it('subcategoria arquivada não oferece criar filha dentro dela', async () => {
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
    })
    renderCategorias()

    await screen.findByText('Energia')
    // Não existe terceiro nível (ADR-017b): a folha nunca oferece "nova
    // subcategoria".
    expect(
      screen.queryByRole('button', { name: 'Nova subcategoria em Energia' }),
    ).not.toBeInTheDocument()
  })

  it('estado vazio explica a semente', async () => {
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () =>
        jsonResponse(200, { expense: [], income: [], investment: [], redemption: [] }),
    })
    renderCategorias()

    expect(await screen.findByText('Nenhuma categoria.')).toBeInTheDocument()
    expect(screen.getByText(/nasce com um conjunto em português/)).toBeInTheDocument()
  })

  it('põe o foco no título e define o título do documento', async () => {
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /categories': () => jsonResponse(200, ARVORE_PADRAO),
    })
    renderCategorias()

    const titulo = await screen.findByRole('heading', { name: 'Categorias' })
    await waitFor(() => expect(titulo).toHaveFocus())
    expect(document.title).toBe('Categorias · HomeFinance')
  })
})
