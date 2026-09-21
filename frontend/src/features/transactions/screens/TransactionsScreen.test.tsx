import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'

const fetchMock = vi.fn()

const CONTA_CORRENTE = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'
const CARTAO = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d66'

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
  ],
  totalBalanceCents: 0,
}

function lancamento(over: Record<string, unknown> = {}) {
  return {
    id: 'tx-1',
    kind: 'expense',
    accountId: CONTA_CORRENTE,
    accountName: 'Conta corrente',
    categoryId: 'cat-1',
    categoryName: 'Alimentação',
    amountCents: 2900,
    description: 'Padaria Exemplo',
    occurredOn: '2026-08-31',
    yearMonth: '2026-08',
    competenceMonth: '2026-08',
    transferGroupId: null,
    statementId: null,
    source: 'import',
    importBatchId: null,
    createdBy: SESSAO.user.id,
    createdAt: '2026-08-31T00:00:00Z',
    updatedAt: '2026-08-31T00:00:00Z',
    ...over,
  }
}

function resumo(over: Record<string, number> = {}) {
  return {
    incomeCents: 0,
    expenseCents: 2900,
    netCents: -2900,
    count: 1,
    uncategorizedCount: 0,
    // Sempre presentes no contrato desde a spec 0006 §3.5.2 — zero quando a
    // casa não tem categoria de investimento.
    investedCents: 0,
    redeemedCents: 0,
    ...over,
  }
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

/** Roteia pela URL COMPLETA (com query): a paginação por cursor e o filtro de
 *  conta só se verificam olhando a query string que a tela montou. */
function rotearApi(handler: (metodo: string, url: URL) => Response | Promise<Response>) {
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    return Promise.resolve(handler(metodo, new URL(String(url), 'https://app.invalido')))
  })
}

function padrao(lista: unknown) {
  return (metodo: string, url: URL) => {
    const caminho = url.pathname.replace('/api/v1', '')
    if (caminho === '/me') return jsonResponse(200, SESSAO)
    if (caminho === '/accounts') return jsonResponse(200, CONTAS)
    if (caminho === '/transactions' && metodo === 'GET') return jsonResponse(200, lista)
    throw new Error(`rota não declarada no teste: ${metodo} ${url.pathname}${url.search}`)
  }
}

function renderLancamentos(caminho = '/lancamentos?mes=2026-08') {
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

describe('TransactionsScreen', () => {
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

  it('agrupa por dia e escreve a data SEM deslocar o fuso', async () => {
    // A armadilha: `new Date('2026-08-31')` formatado em São Paulo vira 30/08.
    // Este teste roda com o processo em qualquer fuso e o cabeçalho tem de
    // continuar dizendo 31.
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    renderLancamentos()

    expect(
      await screen.findByRole('rowheader', { name: /segunda-feira, 31 de agosto/ }),
    ).toBeInTheDocument()
  })

  it('mostra o valor com sinal explícito, sem depender de cor', async () => {
    rotearApi(
      padrao({
        items: [
          lancamento({ id: 'tx-1', kind: 'expense', amountCents: 2900 }),
          lancamento({
            id: 'tx-2',
            kind: 'income',
            amountCents: 160_000,
            description: 'Salário',
            categoryName: 'Salário',
          }),
        ],
        nextCursor: null,
        summary: resumo({ incomeCents: 160_000, netCents: 157_100, count: 2 }),
      }),
    )
    renderLancamentos()

    expect(await screen.findByText('-29,00')).toBeInTheDocument()
    expect(screen.getByText('+1.600,00')).toBeInTheDocument()
  })

  /** A faixa do mês (spec 0006 §3.5.2, docs/DESIGN.md E7 (g)).
   *
   *  `Entrou`, `Saiu` e `Resultado` deixaram de somar os aportes e resgates.
   *  Sem explicação o mês encolheria sozinho — mentira por omissão. */
  it('explica na SEGUNDA linha da faixa o que ficou fora dos números', async () => {
    rotearApi(
      padrao({
        items: [lancamento()],
        nextCursor: null,
        summary: resumo({ investedCents: 200_000, redeemedCents: 85_000 }),
      }),
    )
    renderLancamentos()

    const fora = await screen.findByText('Fora destes números:')
    const linha = fora.parentElement
    expect(linha).not.toBeNull()
    expect(linha?.textContent).toContain('em aportes')
    expect(linha?.textContent).toContain('em resgates')
    expect(within(linha as HTMLElement).getByText('2.000,00')).toBeInTheDocument()
    expect(within(linha as HTMLElement).getByText('850,00')).toBeInTheDocument()

    // NÃO é um quarto item depois de `Resultado`: é uma linha própria,
    // subordinada, com o motivo à frente dos números.
    const numeros = screen.getByText('Resultado').closest('p')
    expect(numeros).not.toBe(linha)
    expect(numeros?.textContent).not.toContain('aportes')

    // Sem tom e sem sinal: são os mesmos números de /investimentos, e lá eles
    // são silenciosos.
    for (const valor of (linha as HTMLElement).querySelectorAll('[data-emphasis]')) {
      expect(valor.getAttribute('data-tone')).toBe('neutral')
      expect(valor.textContent).not.toMatch(/[+]/)
    }

    // Sem link: Investimentos está no menu, a dois passos.
    expect(within(linha as HTMLElement).queryByRole('link')).toBeNull()
  })

  it('com um dos dois zerado, só o que existe é citado — sem separador órfão', async () => {
    rotearApi(
      padrao({
        items: [lancamento()],
        nextCursor: null,
        summary: resumo({ investedCents: 200_000, redeemedCents: 0 }),
      }),
    )
    renderLancamentos()

    const fora = await screen.findByText('Fora destes números:')
    const linha = fora.parentElement as HTMLElement
    expect(linha.textContent).toContain('em aportes')
    expect(linha.textContent).not.toContain('em resgates')
    // O `·` só existe ENTRE dois itens: sozinho ele seria pontuação órfã.
    expect(linha.textContent).not.toContain('·')
  })

  it('sem aporte nem resgate no mês, a segunda linha não existe', async () => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    renderLancamentos()

    await screen.findByText('Resultado')
    expect(screen.queryByText('Fora destes números:')).not.toBeInTheDocument()
  })

  it('deixa a transferência fora do subtotal e explica isso no cabeçalho do dia', async () => {
    // Sem a frase "· 1 transferência", o dia parece ter erro de conta: as
    // linhas visíveis somam -5.029,00 e o subtotal mostra -29,00.
    rotearApi(
      padrao({
        items: [
          lancamento({
            id: 'tx-transf',
            kind: 'transfer_out',
            amountCents: 500_000,
            description: 'Pix para Cartão C6',
            categoryId: null,
            categoryName: null,
            transferGroupId: 'grupo-1',
          }),
          lancamento({ id: 'tx-1', amountCents: 2900 }),
        ],
        nextCursor: null,
        summary: resumo({ count: 2 }),
      }),
    )
    renderLancamentos()

    const cabecalho = await screen.findByRole('rowheader', { name: /31 de agosto/ })
    expect(cabecalho).toHaveTextContent('· 1 transferência')

    // O subtotal do dia conta só receita e despesa.
    const linhaDoGrupo = cabecalho.closest('tr')
    expect(linhaDoGrupo).not.toBeNull()
    expect(within(linhaDoGrupo as HTMLElement).getByText('-29,00')).toBeInTheDocument()

    // E a perna de transferência não é pintada como despesa.
    expect(screen.getByText('Transferência')).toBeInTheDocument()
  })

  it('a faixa de pendência filtra a própria tela pela URL', async () => {
    rotearApi(
      padrao({
        items: [lancamento({ categoryId: null, categoryName: null })],
        nextCursor: null,
        summary: resumo({ uncategorizedCount: 12 }),
      }),
    )
    const router = renderLancamentos()

    expect(
      await screen.findByText(/12 lançamentos de agosto estão sem categoria/),
    ).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Ver só esses 12' }))

    await waitFor(() => {
      expect(router.state.location.search).toMatchObject({ semCategoria: 1, mes: '2026-08' })
    })
    // Com o filtro ligado a faixa muda de tom e oferece a saída.
    expect(
      await screen.findByText('Mostrando só os lançamentos sem categoria de agosto.'),
    ).toBeInTheDocument()
  })

  it('não renderiza a faixa quando a dívida zerou', async () => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    renderLancamentos()

    await screen.findByText('Padaria Exemplo')
    expect(screen.queryByText(/sem categoria/i)).not.toBeInTheDocument()
  })

  it('a URL recusa conta que não é UUID e filtro que não é 1', async () => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    const router = renderLancamentos("/lancamentos?mes=2026-08&conta=' OR 1=1 --&semCategoria=sim")

    await screen.findByText('Padaria Exemplo')

    // O que importa: nada do lixo da URL virou requisição. `location.search`
    // guarda o PARSE cru da URL — quem valida é a tela, e é essa fronteira que
    // este teste protege.
    const urls = fetchMock.mock.calls.map((chamada) => String(chamada[0]))
    expect(urls.some((url) => url.includes('accountId'))).toBe(false)
    expect(urls.some((url) => url.includes('OR 1=1'))).toBe(false)

    // E o filtro inválido não ligou o modo "só sem categoria".
    expect(
      screen.queryByText('Mostrando só os lançamentos sem categoria de agosto.'),
    ).not.toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/lancamentos')
  })

  it('leva a conta válida da URL para a query da API', async () => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    renderLancamentos(`/lancamentos?mes=2026-08&conta=${CONTA_CORRENTE}`)

    await screen.findByText('Padaria Exemplo')
    await waitFor(() => {
      const urls = fetchMock.mock.calls.map((chamada) => String(chamada[0]))
      expect(urls.some((url) => url.includes(`accountId=${CONTA_CORRENTE}`))).toBe(true)
    })
  })

  it('carrega mais por cursor e anuncia quantas linhas chegaram', async () => {
    rotearApi((metodo, url) => {
      const caminho = url.pathname.replace('/api/v1', '')
      if (caminho === '/me') return jsonResponse(200, SESSAO)
      if (caminho === '/accounts') return jsonResponse(200, CONTAS)
      if (caminho === '/transactions') {
        const cursor = url.searchParams.get('cursor')
        if (cursor === null) {
          return jsonResponse(200, {
            items: [lancamento({ id: 'tx-1', description: 'Primeira' })],
            nextCursor: 'cursor-2',
            summary: resumo({ count: 2 }),
          })
        }
        return jsonResponse(200, {
          items: [lancamento({ id: 'tx-2', description: 'Segunda', occurredOn: '2026-08-27' })],
          nextCursor: null,
          summary: resumo({ count: 2 }),
        })
      }
      throw new Error(`rota não declarada: ${metodo} ${url.pathname}`)
    })
    renderLancamentos()

    expect(await screen.findByText('Mostrando 1 de 2 lançamentos')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: /Carregar mais/ }))

    expect(await screen.findByText('Segunda')).toBeInTheDocument()
    expect(
      await screen.findByText('2 lançamentos — é tudo o que existe no filtro.'),
    ).toBeInTheDocument()
    expect(screen.getByText('Mais 1 lançamento carregado. 2 de 2.')).toBeInTheDocument()
  })

  it('a confirmação de exclusão avisa EM TEXTO que a transferência vai inteira', async () => {
    rotearApi((metodo, url) => {
      const caminho = url.pathname.replace('/api/v1', '')
      if (caminho === '/me') return jsonResponse(200, SESSAO)
      if (caminho === '/accounts') return jsonResponse(200, CONTAS)
      if (caminho === '/transactions') {
        return jsonResponse(200, {
          items: [
            lancamento({
              id: 'perna-saida',
              kind: 'transfer_out',
              amountCents: 500_000,
              description: 'Pix para Cartão C6',
              categoryId: null,
              categoryName: null,
              transferGroupId: 'grupo-1',
            }),
            lancamento({
              id: 'perna-entrada',
              kind: 'transfer_in',
              accountId: CARTAO,
              accountName: 'Cartão C6',
              amountCents: 500_000,
              description: 'Pagamento recebido',
              categoryId: null,
              categoryName: null,
              transferGroupId: 'grupo-1',
            }),
          ],
          nextCursor: null,
          summary: resumo({ count: 2 }),
        })
      }
      if (metodo === 'DELETE') return new Response(null, { status: 204 })
      throw new Error(`rota não declarada: ${metodo} ${url.pathname}`)
    })
    renderLancamentos()

    await screen.findByText('Pix para Cartão C6')
    await userEvent.click(
      screen.getByRole('button', { name: /Excluir Pix para Cartão C6, 31\/08\/2026/ }),
    )

    expect(
      await screen.findByRole('heading', { name: 'Excluir a transferência inteira?' }),
    ).toBeInTheDocument()
    // O aviso do par está em TEXTO CORRIDO, não num ícone nem numa cor.
    expect(
      screen.getByText(/Excluir aqui apaga as duas: a saída de R\$ 5\.000,00 da Conta corrente/),
    ).toBeInTheDocument()
    // E o rótulo do botão repete o escopo — é o último texto lido antes do clique.
    expect(screen.getByRole('button', { name: 'Excluir as duas pernas' })).toBeInTheDocument()
  })

  it('a confirmação de um lançamento comum não fala em pernas', async () => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    renderLancamentos()

    await screen.findByText('Padaria Exemplo')
    await userEvent.click(screen.getByRole('button', { name: /Excluir Padaria Exemplo/ }))

    expect(
      await screen.findByRole('heading', { name: 'Excluir este lançamento?' }),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Excluir lançamento' })).toBeInTheDocument()
  })

  it('o estado vazio orienta em vez de dizer "nada por aqui"', async () => {
    rotearApi(
      padrao({
        items: [],
        nextCursor: null,
        summary: resumo({ expenseCents: 0, netCents: 0, count: 0 }),
      }),
    )
    renderLancamentos()

    expect(await screen.findByText('Nenhum lançamento em agosto.')).toBeInTheDocument()
    expect(screen.getAllByRole('button', { name: 'Importar extrato' }).length).toBeGreaterThan(0)
  })
})

// ------------------------------------------------ o filtro de tipo (E2d)

/** Só as requisições de LISTA, na ordem. É sobre a contagem delas que se prova
 *  que uma URL colada não dispara uma varredura do mês. */
function listas(): string[] {
  return fetchMock.mock.calls
    .map((chamada) => String(chamada[0]))
    .filter((url) => url.includes('/transactions?'))
}

function kindGroupDa(url: string): string | null {
  return new URL(url, 'https://app.invalido').searchParams.get('kindGroup')
}

function transferencia(over: Record<string, unknown> = {}) {
  return lancamento({
    id: 'tx-transf',
    kind: 'transfer_out',
    amountCents: 500_000,
    description: 'Pix para Cartão C6',
    categoryId: null,
    categoryName: null,
    transferGroupId: 'grupo-1',
    ...over,
  })
}

describe('TransactionsScreen — filtro de tipo (E2d)', () => {
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

  /** O erro silencioso que esta tarefa mais teme: mandar a palavra da URL
   *  (`kindGroup=despesas`) faria o servidor IGNORAR o parâmetro desconhecido e
   *  devolver a janela inteira, enquanto o seletor continuaria afirmando
   *  "Despesas". Nada estoura — a tela só mente. */
  it.each([
    ['', null],
    ['receitas', 'income'],
    ['despesas', 'expense'],
    ['transferencias', 'transfer'],
    ['investimentos', 'investment'],
  ])('manda o kindGroup do contrato com ?tipo=%s', async (tipo, esperado) => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    renderLancamentos(`/lancamentos?mes=2026-08${tipo ? `&tipo=${tipo}` : ''}`)

    await screen.findByText('Padaria Exemplo')
    const [primeira] = listas()
    expect(primeira).toBeDefined()
    expect(kindGroupDa(String(primeira))).toBe(esperado)
    // E `all` não existe no contrato: Tudo é a AUSÊNCIA da chave.
    expect(listas().some((url) => url.includes('kindGroup=all'))).toBe(false)
  })

  it.each(["' OR 1=1 --", 'expense', 'tudo', 'Despesas', '<script>alert(1)</script>'])(
    'a URL com ?tipo=%s abre em Tudo e nada chega à API',
    async (lixo) => {
      rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
      renderLancamentos(`/lancamentos?mes=2026-08&tipo=${lixo}`)

      await screen.findByText('Padaria Exemplo')
      // A fronteira que este teste protege: `location.search` guarda o PARSE
      // cru da URL, e quem valida é a tela.
      expect(listas().some((url) => url.includes('kindGroup'))).toBe(false)
      expect(listas().some((url) => url.includes('OR 1=1'))).toBe(false)
      expect(listas().some((url) => url.includes('script'))).toBe(false)
      // E a tela está mesmo em Tudo, não num meio-termo.
      expect(screen.getByText('Tudo o que entrou e saiu em agosto.')).toBeInTheDocument()
      expect(document.title).toBe('Lançamentos · HomeFinance')
      expect(screen.getByLabelText('Tipo')).toHaveValue('')
    },
  )

  /** A tempestade de requisições. `?tipo=transferencias&semCategoria=1` é uma
   *  combinação sem resultado possível: sem o descarte, o laço que procura
   *  lacunas nas próximas páginas varreria o mês inteiro, 50 linhas por
   *  requisição, atrás de uma linha que não pode existir. */
  it('?tipo=transferencias&semCategoria=1 abre sem o filtro de pendência e não varre o mês', async () => {
    rotearApi(
      padrao({
        items: [transferencia()],
        // Há página seguinte: é ela que o laço buscaria, uma atrás da outra.
        nextCursor: 'cursor-2',
        summary: resumo({ count: 8, uncategorizedCount: 0 }),
      }),
    )
    const router = renderLancamentos('/lancamentos?mes=2026-08&tipo=transferencias&semCategoria=1')

    await screen.findByText('Pix para Cartão C6')
    await screen.findByRole('button', { name: /Carregar mais/ })

    // Uma requisição de lista, e só.
    expect(listas()).toHaveLength(1)
    expect(kindGroupDa(String(listas()[0]))).toBe('transfer')
    // O filtro de pendência não ligou.
    expect(screen.queryByText(/Mostrando só/)).not.toBeInTheDocument()
    // O roteador NÃO reescreve a URL de quem chegou: `location.search` guarda o
    // parse cru, e o `semCategoria=1` continua escrito ali — exatamente como o
    // `?conta=' OR 1=1 --` do caso acima (medido em 18/09/2026). Quem descarta
    // é o portão, e é por isso que ele é o único lugar que decide: a chave está
    // na URL e não filtra nada. A primeira navegação que a tela fizer a apaga,
    // porque `aplicarNaBusca` aplica o mesmo descarte.
    expect(router.state.location.search).toMatchObject({ tipo: 'transferencias' })
    expect(router.state.location.pathname).toBe('/lancamentos')
  })

  /** O outro lado da mesma guarda: o filtro de pendência é legítimo, mas o mês
   *  não tem nenhuma. Sem `uncategorizedCount > 0`, a tela pediria página após
   *  página até o fim do mês. */
  it('?semCategoria=1 num mês sem pendência faz no máximo 1 requisição de lista', async () => {
    rotearApi(
      padrao({
        items: [lancamento()],
        nextCursor: 'cursor-2',
        summary: resumo({ count: 60, uncategorizedCount: 0 }),
      }),
    )
    renderLancamentos('/lancamentos?mes=2026-08&semCategoria=1')

    expect(await screen.findByText('Tudo categorizado em agosto.')).toBeInTheDocument()
    expect(listas()).toHaveLength(1)
    // E não ficou dizendo que procura: não há o que procurar.
    expect(screen.queryByText(/Procurando lançamentos sem categoria/)).not.toBeInTheDocument()
  })

  it('trocar o filtro descarta o cursor e recomeça a lista', async () => {
    rotearApi((metodo, url) => {
      const caminho = url.pathname.replace('/api/v1', '')
      if (caminho === '/me') return jsonResponse(200, SESSAO)
      if (caminho === '/accounts') return jsonResponse(200, CONTAS)
      if (caminho === '/transactions') {
        if (url.searchParams.get('kindGroup') === 'expense') {
          return jsonResponse(200, {
            items: [lancamento({ id: 'tx-d', description: 'Só despesas' })],
            nextCursor: null,
            summary: resumo({ count: 1 }),
          })
        }
        if (url.searchParams.get('cursor') === null) {
          return jsonResponse(200, {
            items: [lancamento({ id: 'tx-1', description: 'Primeira' })],
            nextCursor: 'cursor-2',
            summary: resumo({ count: 2 }),
          })
        }
        return jsonResponse(200, {
          items: [lancamento({ id: 'tx-2', description: 'Segunda', occurredOn: '2026-08-27' })],
          nextCursor: null,
          summary: resumo({ count: 2 }),
        })
      }
      throw new Error(`rota não declarada: ${metodo} ${url.pathname}`)
    })
    renderLancamentos()

    await userEvent.click(await screen.findByRole('button', { name: /Carregar mais/ }))
    await screen.findByText('Segunda')

    await userEvent.selectOptions(screen.getByLabelText('Tipo'), 'despesas')
    expect(await screen.findByText('Só despesas')).toBeInTheDocument()

    // O cursor é POSIÇÃO, não filtro: a lista do recorte novo começa do zero.
    const doFiltro = listas().filter((url) => url.includes('kindGroup=expense'))
    expect(doFiltro).toHaveLength(1)
    expect(String(doFiltro[0])).not.toContain('cursor=')
    expect(screen.queryByText('Segunda')).not.toBeInTheDocument()
    expect(screen.queryByText('Primeira')).not.toBeInTheDocument()
  })

  it('o rodapé e o botão de carregar mais usam o count do filtro', async () => {
    rotearApi(
      padrao({ items: [lancamento()], nextCursor: 'cursor-2', summary: resumo({ count: 9 }) }),
    )
    renderLancamentos('/lancamentos?mes=2026-08&tipo=despesas')

    expect(await screen.findByText('Mostrando 1 de 9 lançamentos')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Carregar mais 8' })).toBeInTheDocument()
  })

  /** A armadilha que `CategoryReportScreen.test.tsx` já fixou para `natureza`:
   *  trocar a busca não pode roubar o foco do controle que a pessoa acabou de
   *  usar. Com o teclado, perder isto é perder o controle no meio da escolha. */
  it('trocar o tipo NÃO tira o foco do seletor', async () => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    const router = renderLancamentos()

    await screen.findByText('Padaria Exemplo')
    const seletor = screen.getByLabelText('Tipo')
    seletor.focus()

    await userEvent.selectOptions(seletor, 'despesas')

    await waitFor(() => expect(router.state.location.search).toMatchObject({ tipo: 'despesas' }))
    expect(screen.getByLabelText('Tipo')).toHaveFocus()
    expect(screen.getByRole('heading', { level: 1 })).not.toHaveFocus()
  })

  it('trocar a conta NÃO tira o foco do seletor', async () => {
    rotearApi(padrao({ items: [lancamento()], nextCursor: null, summary: resumo() }))
    const router = renderLancamentos()

    await screen.findByText('Padaria Exemplo')
    const seletor = screen.getByLabelText('Conta')
    seletor.focus()

    await userEvent.selectOptions(seletor, CONTA_CORRENTE)

    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({ conta: CONTA_CORRENTE }),
    )
    expect(screen.getByLabelText('Conta')).toHaveFocus()
  })

  /** O ponto que justifica a tarefa: manter os três números com zeros é
   *  tecnicamente verdadeiro e visualmente mentiroso — `Entrou 0,00 · Saiu 0,00`
   *  diz "nada aconteceu" num mês que moveu R$ 5.000. */
  it('em transferências a faixa é uma frase, e não existe 0,00 em lugar nenhum', async () => {
    rotearApi(
      padrao({
        items: [transferencia()],
        nextCursor: null,
        summary: resumo({
          expenseCents: 0,
          netCents: 0,
          count: 2,
          investedCents: 200_000,
          redeemedCents: 85_000,
        }),
      }),
    )
    renderLancamentos('/lancamentos?mes=2026-08&tipo=transferencias')

    // A frase não depende de dado nenhum: ela entra já na PRIMEIRA
    // renderização, antes de a lista chegar (é por isso que ela não tem
    // skeleton). Por isso o teste espera a linha, e não a frase, para só então
    // olhar a tabela.
    expect(
      await screen.findByText(
        'Transferência não é receita nem despesa — o dinheiro só mudou de conta dentro da casa.',
      ),
    ).toBeInTheDocument()
    await screen.findByText('Pix para Cartão C6')
    expect(screen.queryByText('Entrou')).not.toBeInTheDocument()
    expect(screen.queryByText('Saiu')).not.toBeInTheDocument()
    expect(screen.queryByText('Resultado')).not.toBeInTheDocument()
    // A 2ª linha some junto: aqui não há total do qual algo tenha saído.
    expect(screen.queryByText('Fora destes números:')).not.toBeInTheDocument()
    // Nenhum zero repetido, nem na faixa nem no cabeçalho do dia.
    expect(screen.queryAllByText('0,00')).toHaveLength(0)

    // O cabeçalho do dia fica só com a data — sem subtotal e sem o sufixo, que
    // viraria rótulo repetido em todo cabeçalho.
    const cabecalho = screen.getByRole('rowheader', { name: /31 de agosto/ })
    expect(cabecalho).toHaveTextContent('segunda-feira, 31 de agosto')
    expect(cabecalho).not.toHaveTextContent('transferência')
    expect(cabecalho).not.toHaveTextContent('Subtotal')

    // E a coluna Categoria não existe: a mesma etiqueta em toda linha é ruído.
    expect(screen.queryByRole('columnheader', { name: 'Categoria' })).not.toBeInTheDocument()
    expect(screen.queryByText('Transferência')).not.toBeInTheDocument()
    expect(document.title).toBe('Transferências · Lançamentos · HomeFinance')
    expect(
      screen.getByRole('table', { name: 'Transferências de agosto, agrupadas por dia' }),
    ).toBeInTheDocument()
  })

  it('em investimentos a faixa traz Aportes e Resgates, e a linha ganha a palavra Movimento', async () => {
    rotearApi(
      padrao({
        items: [
          lancamento({
            id: 'tx-aporte',
            kind: 'expense',
            amountCents: 200_000,
            description: 'CDB Nubank',
            categoryId: 'cat-cdb',
            categoryName: 'CDB',
          }),
          lancamento({
            id: 'tx-resgate',
            kind: 'income',
            amountCents: 85_000,
            description: 'Resgate CDB',
            categoryId: 'cat-resgates',
            categoryName: 'Resgates',
          }),
        ],
        nextCursor: null,
        summary: resumo({
          expenseCents: 0,
          netCents: 0,
          count: 2,
          investedCents: 200_000,
          redeemedCents: 85_000,
        }),
      }),
    )
    renderLancamentos('/lancamentos?mes=2026-08&tipo=investimentos')

    const faixa = (await screen.findByText('Aportes')).closest('p') as HTMLElement
    expect(within(faixa).getByText('2.000,00')).toBeInTheDocument()
    expect(within(faixa).getByText('Resgates')).toBeInTheDocument()
    expect(within(faixa).getByText('850,00')).toBeInTheDocument()
    // Sem líquido aqui: o líquido do mês é do painel de /investimentos.
    expect(screen.queryByText('Resultado')).not.toBeInTheDocument()
    expect(screen.queryByText('Fora destes números:')).not.toBeInTheDocument()

    // A palavra é a portadora — e o valor passa a neutro e SEM sinal: pintar o
    // aporte de vermelho ensinaria que poupar é prejuízo.
    const linha = screen.getByText('CDB Nubank').closest('tr') as HTMLElement
    expect(within(linha).getByText('Aporte')).toBeInTheDocument()
    expect(within(linha).getByText('2.000,00')).toBeInTheDocument()
    expect(within(linha).queryByText('-2.000,00')).not.toBeInTheDocument()
    for (const valor of linha.querySelectorAll('[data-emphasis]')) {
      expect(valor.getAttribute('data-tone')).toBe('neutral')
    }
    expect(
      within(screen.getByText('Resgate CDB').closest('tr') as HTMLElement).getByText('Resgate'),
    ).toBeInTheDocument()

    // E o dia não tem subtotal: investimento não muda o patrimônio da casa.
    expect(screen.getByRole('rowheader', { name: /31 de agosto/ })).not.toHaveTextContent(
      'Subtotal',
    )
  })

  it('em investimentos os dois números aparecem sempre, inclusive zerados', async () => {
    rotearApi(
      padrao({
        items: [
          lancamento({
            id: 'tx-aporte',
            kind: 'expense',
            amountCents: 200_000,
            description: 'CDB Nubank',
            categoryId: 'cat-cdb',
            categoryName: 'CDB',
          }),
        ],
        nextCursor: null,
        summary: resumo({
          expenseCents: 0,
          netCents: 0,
          count: 1,
          investedCents: 200_000,
          redeemedCents: 0,
        }),
      }),
    )
    renderLancamentos('/lancamentos?mes=2026-08&tipo=investimentos')

    // Aqui a ausência de resgate RESPONDE à pergunta que a pessoa está fazendo
    // — ao contrário do zero na 2ª linha de Tudo, que seria ruído.
    const faixa = (await screen.findByText('Resgates')).closest('p') as HTMLElement
    expect(within(faixa).getByText('0,00')).toBeInTheDocument()
  })

  it('em receitas a faixa mostra só Entrou, e a 2ª linha cita só os resgates', async () => {
    rotearApi(
      padrao({
        items: [
          lancamento({
            id: 'tx-salario',
            kind: 'income',
            amountCents: 530_000,
            description: 'Salário',
            categoryName: 'Salário',
          }),
        ],
        nextCursor: null,
        summary: resumo({
          incomeCents: 530_000,
          expenseCents: 0,
          netCents: 530_000,
          count: 1,
          investedCents: 200_000,
          redeemedCents: 85_000,
        }),
      }),
    )
    renderLancamentos('/lancamentos?mes=2026-08&tipo=receitas')

    const faixa = (await screen.findByText('Entrou')).closest('p') as HTMLElement
    expect(within(faixa).getByText('5.300,00')).toBeInTheDocument()
    // Um zero que nunca muda é ruído; `Resultado` seria `Entrou` outra vez.
    expect(screen.queryByText('Saiu')).not.toBeInTheDocument()
    expect(screen.queryByText('Resultado')).not.toBeInTheDocument()

    // A 2ª linha SOBREVIVE, citando só o lado do dinheiro que este filtro nomeia:
    // resgate é dinheiro que entrou e não está em `Entrou`.
    const fora = screen.getByText('Fora destes números:').parentElement as HTMLElement
    expect(fora.textContent).toContain('em resgates')
    expect(fora.textContent).not.toContain('em aportes')
    expect(screen.getByText('O que entrou em agosto.')).toBeInTheDocument()
    expect(document.title).toBe('Receitas · Lançamentos · HomeFinance')
  })

  it('em despesas a faixa mostra só Saiu, e a 2ª linha cita só os aportes', async () => {
    rotearApi(
      padrao({
        items: [lancamento()],
        nextCursor: null,
        summary: resumo({
          incomeCents: 0,
          expenseCents: 310_000,
          netCents: -310_000,
          investedCents: 200_000,
          redeemedCents: 85_000,
        }),
      }),
    )
    renderLancamentos('/lancamentos?mes=2026-08&tipo=despesas')

    const faixa = (await screen.findByText('Saiu')).closest('p') as HTMLElement
    expect(within(faixa).getByText('3.100,00')).toBeInTheDocument()
    expect(screen.queryByText('Entrou')).not.toBeInTheDocument()
    expect(screen.queryByText('Resultado')).not.toBeInTheDocument()

    const fora = screen.getByText('Fora destes números:').parentElement as HTMLElement
    expect(fora.textContent).toContain('em aportes')
    expect(fora.textContent).not.toContain('em resgates')

    // Em despesas o subtotal do dia FICA: todas as linhas do dia entram nele.
    expect(screen.getByRole('rowheader', { name: /31 de agosto/ })).toHaveTextContent('Subtotal')
    expect(screen.getByText('O que saiu em agosto.')).toBeInTheDocument()
    expect(document.title).toBe('Despesas · Lançamentos · HomeFinance')
    expect(
      screen.getByRole('table', { name: 'Despesas de agosto, agrupadas por dia' }),
    ).toBeInTheDocument()
  })

  it('a faixa de pendência nomeia o tipo, concorda em gênero e preserva o ?tipo=', async () => {
    rotearApi(
      padrao({
        items: [
          lancamento({
            id: 'tx-freela',
            kind: 'income',
            amountCents: 90_000,
            description: 'Freela',
            categoryId: null,
            categoryName: null,
          }),
        ],
        nextCursor: null,
        summary: resumo({
          incomeCents: 530_000,
          expenseCents: 0,
          netCents: 530_000,
          count: 3,
          uncategorizedCount: 3,
        }),
      }),
    )
    const router = renderLancamentos('/lancamentos?mes=2026-08&tipo=receitas')

    expect(
      await screen.findByText(
        '3 receitas de agosto estão sem categoria. Elas não entram em nenhum orçamento, e nos relatórios aparecem como "Sem categoria".',
      ),
    ).toBeInTheDocument()
    // O diálogo é do MÊS: ao lado de "3 receitas", o rótulo antigo prometeria 3
    // e faria 12.
    expect(
      screen.getByRole('button', { name: 'Categorizar o mês automaticamente' }),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Categorizar automaticamente' }),
    ).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Ver só essas 3' }))

    await waitFor(() =>
      expect(router.state.location.search).toMatchObject({
        mes: '2026-08',
        tipo: 'receitas',
        semCategoria: 1,
      }),
    )
    expect(
      await screen.findByText('Mostrando só as receitas sem categoria de agosto.'),
    ).toBeInTheDocument()
    // O botão limpa SÓ o semCategoria — prometer "todos os lançamentos" seria
    // prometer o que o clique não faz.
    expect(screen.getByRole('button', { name: 'Mostrar todas as receitas' })).toBeInTheDocument()
  })

  it('a pendência no singular concorda: "1 receita" e "Ver essa receita"', async () => {
    rotearApi(
      padrao({
        items: [
          lancamento({
            id: 'tx-freela',
            kind: 'income',
            amountCents: 90_000,
            description: 'Freela',
            categoryId: null,
            categoryName: null,
          }),
        ],
        nextCursor: null,
        summary: resumo({
          incomeCents: 90_000,
          expenseCents: 0,
          netCents: 90_000,
          count: 1,
          uncategorizedCount: 1,
        }),
      }),
    )
    renderLancamentos('/lancamentos?mes=2026-08&tipo=receitas')

    expect(
      await screen.findByText(
        '1 receita de agosto está sem categoria. Ela não entra em nenhum orçamento, e nos relatórios aparece como "Sem categoria".',
      ),
    ).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Ver essa receita' })).toBeInTheDocument()
  })

  it('o vazio do tipo orienta e o botão diz a dimensão que limpa', async () => {
    rotearApi(
      padrao({
        items: [],
        nextCursor: null,
        summary: resumo({ expenseCents: 0, netCents: 0, count: 0 }),
      }),
    )
    const router = renderLancamentos('/lancamentos?mes=2026-08&tipo=despesas')

    expect(await screen.findByText('Nenhuma despesa em agosto.')).toBeInTheDocument()
    expect(
      screen.getByText(
        'Aporte em investimento está em Investimentos, e o que foi para outra conta sua é transferência.',
      ),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'Mostrar todas as contas' }),
    ).not.toBeInTheDocument()

    await userEvent.click(screen.getByRole('button', { name: 'Mostrar todos os tipos' }))
    await waitFor(() => expect(router.state.location.search).toEqual({ mes: '2026-08' }))
  })

  it('com tipo e conta, o vazio oferece os dois botões — um por dimensão', async () => {
    rotearApi(
      padrao({
        items: [],
        nextCursor: null,
        summary: resumo({ expenseCents: 0, netCents: 0, count: 0 }),
      }),
    )
    renderLancamentos(`/lancamentos?mes=2026-08&tipo=despesas&conta=${CONTA_CORRENTE}`)

    expect(
      await screen.findByText('Nenhuma despesa na Conta corrente em agosto.'),
    ).toBeInTheDocument()
    expect(
      screen.getByText('Troque o tipo, troque a conta, ou volte para a lista inteira.'),
    ).toBeInTheDocument()
    // Adivinhar qual filtro a pessoa quis desfazer é pior do que oferecer os dois.
    expect(screen.getByRole('button', { name: 'Mostrar todos os tipos' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Mostrar todas as contas' })).toBeInTheDocument()
  })

  it('o vazio de semCategoria com tipo não promete mais do que limpa', async () => {
    rotearApi(
      padrao({
        items: [lancamento()],
        nextCursor: null,
        summary: resumo({ count: 9, uncategorizedCount: 0 }),
      }),
    )
    renderLancamentos('/lancamentos?mes=2026-08&tipo=despesas&semCategoria=1')

    expect(
      await screen.findByText('Todas as despesas de agosto estão categorizadas.'),
    ).toBeInTheDocument()
    expect(screen.getByText('Nenhuma despesa deste mês ficou sem categoria.')).toBeInTheDocument()
    // Duas saídas, a mesma promessa: a da faixa `info` e a do vazio. Nenhuma
    // das duas diz "todos os lançamentos" — o clique limpa só o `semCategoria`,
    // e a tela continua mostrando só despesas.
    expect(screen.getAllByRole('button', { name: 'Mostrar todas as despesas' })).toHaveLength(2)
    expect(
      screen.queryByRole('button', { name: 'Mostrar todos os lançamentos' }),
    ).not.toBeInTheDocument()
  })
  // --- rodada de QA da T5 (E2d) --------------------------------------------

  /** A outra metade da amplificação. `?tipo=receitas&semCategoria=1` é uma
   *  combinação LEGÍTIMA — `validarBusca` só descarta o `semCategoria` sob
   *  transferências e investimentos —, então aqui o portão não protege nada: a
   *  única guarda é `uncategorizedCount > 0`, e ela agora lê a pendência DO
   *  RECORTE, que é um número diferente do de "Tudo".
   *
   *  Sem ela, uma URL colada varreria o mês inteiro, 50 linhas por requisição,
   *  atrás de uma receita sem categoria que o servidor já disse não existir. */
  it('?tipo=receitas&semCategoria=1 num mês sem pendência faz no máximo 1 requisição de lista', async () => {
    rotearApi(
      padrao({
        items: [lancamento({ id: 'tx-r', kind: 'income', description: 'Salário' })],
        // Há página seguinte: é ela que o laço buscaria, uma atrás da outra.
        nextCursor: 'cursor-2',
        summary: resumo({ count: 60, incomeCents: 500_000, uncategorizedCount: 0 }),
      }),
    )
    renderLancamentos('/lancamentos?mes=2026-08&tipo=receitas&semCategoria=1')

    expect(
      await screen.findByText('Todas as receitas de agosto estão categorizadas.'),
    ).toBeInTheDocument()
    expect(listas()).toHaveLength(1)
    expect(kindGroupDa(String(listas()[0]))).toBe('income')
    expect(screen.queryByText(/Procurando lançamentos sem categoria/)).not.toBeInTheDocument()
  })

  /** O `kindGroup` tem de viajar JUNTO do `cursor`. Se a segunda página o
   *  perdesse, ela viria da janela inteira — e a tela emendaria linhas de
   *  outros tipos embaixo das do filtro, sem nada estourar. */
  it('a segunda página leva o kindGroup junto do cursor', async () => {
    rotearApi((metodo, url) => {
      const caminho = url.pathname.replace('/api/v1', '')
      if (caminho === '/me') return jsonResponse(200, SESSAO)
      if (caminho === '/accounts') return jsonResponse(200, CONTAS)
      if (caminho === '/transactions') {
        if (url.searchParams.get('cursor')) {
          return jsonResponse(200, {
            items: [lancamento({ id: 'tx-d2', description: 'Segunda despesa' })],
            nextCursor: null,
            summary: resumo({ count: 2 }),
          })
        }
        return jsonResponse(200, {
          items: [lancamento({ id: 'tx-d1', description: 'Primeira despesa' })],
          nextCursor: 'cursor-2',
          summary: resumo({ count: 2 }),
        })
      }
      throw new Error(`rota não declarada: ${metodo} ${url.pathname}`)
    })
    renderLancamentos('/lancamentos?mes=2026-08&tipo=despesas')

    await userEvent.click(await screen.findByRole('button', { name: /Carregar mais/ }))
    await screen.findByText('Segunda despesa')

    const comCursor = listas().filter((url) => url.includes('cursor='))
    expect(comCursor).toHaveLength(1)
    expect(kindGroupDa(String(comCursor[0]))).toBe('expense')
    // E nenhuma requisição mandou a palavra da URL no lugar do valor da API.
    expect(listas().some((url) => url.includes('tipo='))).toBe(false)
    expect(listas().some((url) => url.includes('kindGroup=despesas'))).toBe(false)
  })
})
