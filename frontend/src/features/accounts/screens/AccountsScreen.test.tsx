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

function conta(over: Partial<Record<string, unknown>> = {}) {
  return {
    id: 'acc-1',
    name: 'Conta Corrente',
    kind: 'checking',
    institution: 'other',
    statementClosingDay: null,
    statementDueDay: null,
    openingBalanceCents: 150_000,
    openingDate: '2026-01-01',
    balanceCents: 150_000,
    keywords: [],
    archivedAt: null,
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

/** Roteia por URL: o teste declara o que cada rota devolve, e o que não foi
 *  declarado explode — endpoint chamado por engano não passa despercebido. */
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

function rotearApi(rotas: Record<string, () => Response | Promise<Response>>) {
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    const caminho = String(url).replace('/api/v1', '')
    const chave = `${metodo} ${caminho}`
    const semQuery = `${metodo} ${caminho.split('?')[0]}`
    const handler = rotas[chave] ?? rotas[semQuery]
    if (!handler) throw new Error(`rota não declarada no teste: ${chave}`)
    return Promise.resolve(handler())
  })
}

function renderContas() {
  const router = createAppRouter(createMemoryHistory({ initialEntries: ['/contas'] }))
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

describe('AccountsScreen', () => {
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

  it('lista as contas com saldo e total', async () => {
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () =>
        jsonResponse(200, {
          items: [
            conta(),
            conta({ id: 'acc-2', name: 'Cartão', kind: 'credit_card', balanceCents: -80_000 }),
          ],
          totalBalanceCents: 70_000,
        }),
    })
    renderContas()

    expect(await screen.findByRole('heading', { name: 'Contas' })).toBeInTheDocument()
    expect(await screen.findByText('Conta Corrente')).toBeInTheDocument()
    expect(screen.getByText('Cartão')).toBeInTheDocument()

    // Saldo negativo aparece com SINAL, não só com cor — cor sozinha é
    // invisível para daltônico e some numa impressão.
    expect(screen.getByText('-800,00')).toBeInTheDocument()
    expect(screen.getByText('1.500,00')).toBeInTheDocument()

    // O total vem do servidor e é uma linha de rodapé de tabela.
    const rodape = screen.getByRole('row', { name: /Total/ })
    expect(within(rodape).getByText('700,00')).toBeInTheDocument()
  })

  it('põe o foco no título e define o título do documento', async () => {
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [], totalBalanceCents: 0 }),
    })
    renderContas()

    const titulo = await screen.findByRole('heading', { name: 'Contas' })
    await waitFor(() => expect(titulo).toHaveFocus())
    expect(document.title).toBe('Contas · HomeFinance')
  })

  it('estado vazio orienta o próximo passo em vez de só dizer "nada aqui"', async () => {
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [], totalBalanceCents: 0 }),
    })
    renderContas()

    expect(await screen.findByText('Nenhuma conta ainda.')).toBeInTheDocument()
    expect(screen.getByText(/Comece pela conta onde o dinheiro entra/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Criar a primeira conta' })).toBeInTheDocument()
  })

  it('cria uma conta pelo diálogo e manda centavos no corpo', async () => {
    const user = userEvent.setup()
    let criada = false
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () =>
        jsonResponse(200, {
          items: criada ? [conta({ name: 'Carteira', balanceCents: 5000 })] : [],
          totalBalanceCents: criada ? 5000 : 0,
        }),
      'POST /accounts': () => {
        criada = true
        return jsonResponse(201, conta({ name: 'Carteira' }))
      },
    })
    renderContas()

    await screen.findByRole('heading', { name: 'Contas' })
    await user.click(screen.getByRole('button', { name: 'Nova conta' }))

    expect(await screen.findByRole('heading', { name: 'Nova conta' })).toBeInTheDocument()
    await user.type(screen.getByLabelText('Nome'), 'Carteira')
    await user.click(screen.getByLabelText('Saldo de abertura'))
    await user.keyboard('5000')
    await user.click(screen.getByRole('button', { name: 'Criar conta' }))

    await waitFor(() => {
      const corpo = corpoDaChamada('POST')
      // Dinheiro é INTEIRO em centavos no corpo — nunca "50.00".
      expect(corpo.openingBalanceCents).toBe(5000)
      expect(Number.isInteger(corpo.openingBalanceCents)).toBe(true)
      expect(corpo.name).toBe('Carteira')
      expect(corpo.kind).toBe('checking')
      // Instituição tem default, mas quem decide é a tela: o corpo carrega o
      // valor escolhido em vez de deixar o servidor supor.
      expect(corpo.institution).toBe('other')
      // householdId NUNCA sai do cliente: a casa vem do token.
      expect(corpo).not.toHaveProperty('householdId')
    })

    expect(await screen.findByText('Conta criada.')).toBeInTheDocument()
  })

  it('permite saldo de abertura negativo, para cartão', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [], totalBalanceCents: 0 }),
      'POST /accounts': () => jsonResponse(201, conta()),
    })
    renderContas()

    await screen.findByRole('heading', { name: 'Contas' })
    await user.click(screen.getByRole('button', { name: 'Nova conta' }))
    await screen.findByRole('heading', { name: 'Nova conta' })

    await user.type(screen.getByLabelText('Nome'), 'Cartão')
    await user.click(screen.getByRole('button', { name: 'Valor negativo' }))
    await user.click(screen.getByLabelText('Saldo de abertura'))
    await user.keyboard('80000')
    await user.click(screen.getByRole('button', { name: 'Criar conta' }))

    await waitFor(() => {
      const corpo = corpoDaChamada('POST')
      expect(corpo.openingBalanceCents).toBe(-80000)
    })
  })

  it('mostra o erro por campo que o servidor apontou', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [conta()], totalBalanceCents: 150_000 }),
      'POST /accounts': () =>
        jsonResponse(422, {
          error: {
            code: 'VALIDATION_FAILED',
            message: 'Dados inválidos.',
            fields: { name: 'qualquer coisa do servidor' },
          },
        }),
    })
    renderContas()

    await screen.findByRole('heading', { name: 'Contas' })
    await user.click(screen.getByRole('button', { name: 'Nova conta' }))
    await screen.findByRole('heading', { name: 'Nova conta' })
    await user.type(screen.getByLabelText('Nome'), 'Conta Corrente')
    await user.click(screen.getByRole('button', { name: 'Criar conta' }))

    // A frase é NOSSA; o servidor só disse qual campo (docs/SEGURANCA.md §4).
    expect(
      await screen.findByText('Já existe uma conta com este nome, ou o nome é inválido.'),
    ).toBeInTheDocument()
    expect(screen.queryByText('qualquer coisa do servidor')).not.toBeInTheDocument()
    // O diálogo continua aberto para a pessoa corrigir.
    expect(screen.getByRole('heading', { name: 'Nova conta' })).toBeInTheDocument()
  })

  it('arquiva uma conta', async () => {
    const user = userEvent.setup()
    let arquivada = false
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () =>
        jsonResponse(200, {
          items: arquivada ? [] : [conta()],
          totalBalanceCents: arquivada ? 0 : 150_000,
        }),
      'POST /accounts/acc-1/archive': () => {
        arquivada = true
        return jsonResponse(200, conta({ archivedAt: '2026-09-12T00:00:00Z' }))
      },
    })
    renderContas()

    await screen.findByText('Conta Corrente')
    await user.click(screen.getByRole('button', { name: 'Arquivar Conta Corrente' }))

    expect(await screen.findByText('Conta arquivada.')).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByText('Conta Corrente')).not.toBeInTheDocument())
  })

  it('"mostrar arquivadas" traz de volta, com etiqueta escrita', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts?includeArchived=false': () =>
        jsonResponse(200, { items: [], totalBalanceCents: 0 }),
      'GET /accounts?includeArchived=true': () =>
        jsonResponse(200, {
          items: [conta({ archivedAt: '2026-09-12T00:00:00Z' })],
          totalBalanceCents: 150_000,
        }),
    })
    renderContas()

    await screen.findByText('Nenhuma conta ainda.')
    await user.click(screen.getByLabelText('Mostrar arquivadas'))

    expect(await screen.findByText('Conta Corrente')).toBeInTheDocument()
    // A etiqueta é TEXTO: o estado não pode depender só da opacidade.
    expect(screen.getByText('Arquivada')).toBeInTheDocument()
  })

  // O 422 RESOURCE_IN_USE não é "corrija o formulário": é um caminho
  // alternativo. Por isso vira aviso permanente com a saída, e não um toast.
  it('conta em uso não é excluída, e a tela oferece arquivar', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [conta()], totalBalanceCents: 150_000 }),
      'DELETE /accounts/acc-1': () =>
        jsonResponse(422, {
          error: { code: 'RESOURCE_IN_USE', message: 'Este item está em uso...' },
        }),
    })
    renderContas()

    await screen.findByText('Conta Corrente')
    await user.click(screen.getByRole('button', { name: 'Excluir Conta Corrente' }))

    expect(await screen.findByText('Não dá para excluir esta conta')).toBeInTheDocument()
    expect(screen.getByText(/Arquive-a/)).toBeInTheDocument()
    // A conta continua na lista.
    expect(screen.getByText('Conta Corrente')).toBeInTheDocument()
  })

  it('falha de carregamento oferece tentar de novo', async () => {
    const user = userEvent.setup()
    let falhou = false
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => {
        if (!falhou) {
          falhou = true
          return jsonResponse(500, { error: { code: 'INTERNAL_ERROR' } })
        }
        return jsonResponse(200, { items: [conta()], totalBalanceCents: 150_000 })
      },
    })
    renderContas()

    expect(await screen.findByText('Não foi possível carregar as contas.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Tentar de novo' }))
    expect(await screen.findByText('Conta Corrente')).toBeInTheDocument()
  })

  // --------------------------------------------------- instituição e fatura

  it('mostra a instituição na linha da conta, e os dias só no cartão', async () => {
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () =>
        jsonResponse(200, {
          items: [
            conta({ institution: 'nubank' }),
            conta({
              id: 'acc-2',
              name: 'Cartão',
              kind: 'credit_card',
              institution: 'c6',
              statementClosingDay: 3,
              statementDueDay: 10,
              balanceCents: -80_000,
            }),
            conta({ id: 'acc-3', name: 'Carteira', kind: 'cash', balanceCents: 0 }),
          ],
          totalBalanceCents: 70_000,
        }),
    })
    renderContas()

    await screen.findByText('Conta Corrente')
    // Dentro da TABELA: o diálogo fica montado e fechado, e as opções do
    // seletor de instituição têm os mesmos nomes.
    const tabela = within(screen.getByRole('table', { name: /Contas da casa/ }))
    expect(tabela.getByText('Nubank')).toBeInTheDocument()
    // No cartão, os dias configurados aparecem junto: é o que diz à pessoa se a
    // importação da fatura vai sugerir as datas ou adivinhá-las.
    expect(tabela.getByText('C6 · fecha dia 3 · vence dia 10')).toBeInTheDocument()
    // `other` é o default de quem não informou — escrever "Outras" em toda
    // linha seria ruído, não informação.
    expect(tabela.queryByText('Outras')).not.toBeInTheDocument()
  })

  it('cria um cartão com instituição e dias de fatura', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [], totalBalanceCents: 0 }),
      'POST /accounts': () => jsonResponse(201, conta({ name: 'Cartão C6', kind: 'credit_card' })),
    })
    renderContas()

    await screen.findByRole('heading', { name: 'Contas' })
    await user.click(screen.getByRole('button', { name: 'Nova conta' }))
    await screen.findByRole('heading', { name: 'Nova conta' })

    await user.type(screen.getByLabelText('Nome'), 'Cartão C6')
    await user.selectOptions(screen.getByLabelText('Tipo'), 'credit_card')
    await user.selectOptions(screen.getByLabelText('Instituição'), 'c6')

    // O bloco da fatura é um <fieldset>: o leitor de tela anuncia o grupo ao
    // entrar nos campos, então a condição que os fez aparecer não é só visual.
    expect(screen.getByRole('group', { name: 'Fatura do cartão' })).toBeInTheDocument()

    await user.type(screen.getByLabelText('Dia do fechamento'), '3')
    await user.type(screen.getByLabelText('Dia do vencimento'), '10')
    await user.click(screen.getByRole('button', { name: 'Criar conta' }))

    await waitFor(() => {
      const corpo = corpoDaChamada('POST')
      expect(corpo.kind).toBe('credit_card')
      expect(corpo.institution).toBe('c6')
      // Inteiros, não texto: é o que o contrato declara.
      expect(corpo.statementClosingDay).toBe(3)
      expect(corpo.statementDueDay).toBe(10)
    })
  })

  it('a instituição é uma lista fechada — não há campo livre para digitar banco', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [], totalBalanceCents: 0 }),
    })
    renderContas()

    await screen.findByRole('heading', { name: 'Contas' })
    await user.click(screen.getByRole('button', { name: 'Nova conta' }))
    await screen.findByRole('heading', { name: 'Nova conta' })

    const seletor = screen.getByLabelText('Instituição')
    expect(seletor.tagName).toBe('SELECT')
    expect(
      within(seletor)
        .getAllByRole('option')
        .map((o) => o.getAttribute('value')),
    ).toEqual(['c6', 'nubank', 'other'])
    // Default do contrato, e não uma escolha em branco que o servidor teria de
    // adivinhar.
    expect(seletor).toHaveValue('other')
  })

  it('os dias de fatura não existem fora do cartão de crédito', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [], totalBalanceCents: 0 }),
    })
    renderContas()

    await screen.findByRole('heading', { name: 'Contas' })
    await user.click(screen.getByRole('button', { name: 'Nova conta' }))
    await screen.findByRole('heading', { name: 'Nova conta' })

    // Conta corrente: os campos nem existem no DOM — nada de controle
    // desabilitado, que tem contraste ruim e some para parte das tecnologias
    // assistivas (docs/DESIGN.md).
    expect(screen.queryByLabelText('Dia do fechamento')).not.toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('Tipo'), 'credit_card')
    expect(screen.getByLabelText('Dia do fechamento')).toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('Tipo'), 'savings')
    expect(screen.queryByLabelText('Dia do fechamento')).not.toBeInTheDocument()
  })

  it('trocar de cartão para corrente apaga os dias — não só os esconde', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [], totalBalanceCents: 0 }),
      'POST /accounts': () => jsonResponse(201, conta({ name: 'Conta nova' })),
    })
    renderContas()

    await screen.findByRole('heading', { name: 'Contas' })
    await user.click(screen.getByRole('button', { name: 'Nova conta' }))
    await screen.findByRole('heading', { name: 'Nova conta' })

    await user.type(screen.getByLabelText('Nome'), 'Conta nova')
    await user.selectOptions(screen.getByLabelText('Tipo'), 'credit_card')
    await user.type(screen.getByLabelText('Dia do fechamento'), '3')
    await user.type(screen.getByLabelText('Dia do vencimento'), '10')

    await user.selectOptions(screen.getByLabelText('Tipo'), 'checking')
    expect(screen.queryByLabelText('Dia do fechamento')).not.toBeInTheDocument()

    // Voltar ao cartão traz os campos VAZIOS: o valor foi descartado, não
    // guardado atrás da condição.
    await user.selectOptions(screen.getByLabelText('Tipo'), 'credit_card')
    expect(screen.getByLabelText('Dia do fechamento')).toHaveValue('')
    expect(screen.getByLabelText('Dia do vencimento')).toHaveValue('')

    await user.selectOptions(screen.getByLabelText('Tipo'), 'checking')
    await user.click(screen.getByRole('button', { name: 'Criar conta' }))

    await waitFor(() => {
      const corpo = corpoDaChamada('POST')
      expect(corpo.kind).toBe('checking')
      // O que sumiu da tela some do corpo: um dia de fatura em conta que não é
      // cartão é 422 no servidor.
      expect(corpo).not.toHaveProperty('statementClosingDay')
      expect(corpo).not.toHaveProperty('statementDueDay')
    })
  })

  it('dia fora de 1 a 31 nem chega ao servidor', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [], totalBalanceCents: 0 }),
    })
    renderContas()

    await screen.findByRole('heading', { name: 'Contas' })
    await user.click(screen.getByRole('button', { name: 'Nova conta' }))
    await screen.findByRole('heading', { name: 'Nova conta' })

    await user.type(screen.getByLabelText('Nome'), 'Cartão')
    await user.selectOptions(screen.getByLabelText('Tipo'), 'credit_card')

    // O campo só aceita dígito, e no máximo dois: "4a5" vira "45".
    await user.type(screen.getByLabelText('Dia do fechamento'), '4a5')
    expect(screen.getByLabelText('Dia do fechamento')).toHaveValue('45')

    await user.click(screen.getByRole('button', { name: 'Criar conta' }))

    expect(await screen.findByText('O dia precisa estar entre 1 e 31.')).toBeInTheDocument()
    expect(
      fetchMock.mock.calls.some((c) => (c[1] as RequestInit | undefined)?.method === 'POST'),
    ).toBe(false)
  })

  it('editar traz instituição e dias preenchidos, e o PATCH limpa os dias ao sair do cartão', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () =>
        jsonResponse(200, {
          items: [
            conta({
              name: 'Cartão C6',
              kind: 'credit_card',
              institution: 'c6',
              statementClosingDay: 3,
              statementDueDay: 10,
              balanceCents: -80_000,
            }),
          ],
          totalBalanceCents: -80_000,
        }),
      'PATCH /accounts/acc-1': () => jsonResponse(200, conta({ name: 'Cartão C6' })),
    })
    renderContas()

    await screen.findByText('Cartão C6')
    await user.click(screen.getByRole('button', { name: 'Editar Cartão C6' }))
    await screen.findByRole('heading', { name: 'Editar conta' })

    expect(screen.getByLabelText('Instituição')).toHaveValue('c6')
    expect(screen.getByLabelText('Dia do fechamento')).toHaveValue('3')
    expect(screen.getByLabelText('Dia do vencimento')).toHaveValue('10')

    await user.selectOptions(screen.getByLabelText('Tipo'), 'checking')
    await user.click(screen.getByRole('button', { name: 'Salvar' }))

    await waitFor(() => {
      const corpo = corpoDaChamada('PATCH')
      expect(corpo.kind).toBe('checking')
      // No PATCH, campo ausente é "não mexi". Para apagar um fechamento que
      // ficaria órfão é preciso mandar `null` explícito.
      expect(corpo.statementClosingDay).toBeNull()
      expect(corpo.statementDueDay).toBeNull()
    })
  })

  it('a tabela é semântica, com cabeçalhos de coluna', async () => {
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [conta()], totalBalanceCents: 150_000 }),
    })
    renderContas()

    await screen.findByText('Conta Corrente')
    // `<table>` de verdade: é o que faz o leitor de tela anunciar "coluna
    // Saldo, linha 3" ao navegar por uma grade de números.
    expect(screen.getByRole('table', { name: /Contas da casa/ })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: 'Conta' })).toBeInTheDocument()
    expect(screen.getByRole('columnheader', { name: 'Saldo' })).toBeInTheDocument()
  })

  // Spec 0005 §4.1: o campo é o ÚLTIMO do formulário e a lista vai inteira —
  // aqui vazia, porque a pessoa não cadastrou nenhuma.
  it('o corpo da conta nova leva keywords vazia quando nada foi cadastrado', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [], totalBalanceCents: 0 }),
      'POST /accounts': () => jsonResponse(201, conta({ name: 'Carteira' })),
    })
    renderContas()

    await screen.findByRole('heading', { name: 'Contas' })
    await user.click(screen.getByRole('button', { name: 'Nova conta' }))
    await screen.findByRole('heading', { name: 'Nova conta' })

    const dialogo = screen.getByRole('dialog')
    const campos = within(dialogo).getAllByRole('textbox')
    expect(campos[campos.length - 1]).toBe(within(dialogo).getByLabelText('Palavras-chave'))
    expect(within(dialogo).getByText(/aparece nos extratos das OUTRAS contas/)).toBeInTheDocument()

    await user.type(within(dialogo).getByLabelText('Nome'), 'Carteira')
    await user.click(screen.getByRole('button', { name: 'Criar conta' }))

    await waitFor(() => {
      expect(corpoDaChamada('POST').keywords).toEqual([])
    })
  })

  it('cadastra palavras-chave na conta e as manda no corpo', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [], totalBalanceCents: 0 }),
      'POST /accounts': () =>
        jsonResponse(201, conta({ name: 'Nubank', keywords: ['nubank', 'nu pagamentos'] })),
    })
    renderContas()

    await screen.findByRole('heading', { name: 'Contas' })
    await user.click(screen.getByRole('button', { name: 'Nova conta' }))
    await screen.findByRole('heading', { name: 'Nova conta' })

    const dialogo = screen.getByRole('dialog')
    await user.type(within(dialogo).getByLabelText('Nome'), 'Nubank')
    await user.type(within(dialogo).getByLabelText('Palavras-chave'), 'nubank{Enter}nu pagamentos,')
    expect(within(dialogo).getByRole('status')).toHaveTextContent('2 de 20')
    await user.click(screen.getByRole('button', { name: 'Criar conta' }))

    await waitFor(() => {
      expect(corpoDaChamada('POST').keywords).toEqual(['nubank', 'nu pagamentos'])
    })
  })

  it('editar traz as palavras-chave da conta e o PATCH manda a lista inteira', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () =>
        jsonResponse(200, {
          items: [conta({ name: 'Nubank', keywords: ['nubank', 'nu'] })],
          totalBalanceCents: 150_000,
        }),
      'PATCH /accounts/acc-1': () => jsonResponse(200, conta({ name: 'Nubank' })),
    })
    renderContas()

    // Pelo botão da linha, não por `findByText('Nubank')`: o texto também
    // existe na <option> do select de instituição do diálogo fechado.
    await user.click(await screen.findByRole('button', { name: 'Editar Nubank' }))
    await screen.findByRole('heading', { name: 'Editar conta' })

    const dialogo = screen.getByRole('dialog')
    expect(within(dialogo).getByRole('button', { name: 'Remover nubank' })).toBeInTheDocument()
    await user.click(within(dialogo).getByRole('button', { name: 'Remover nu' }))
    await user.click(screen.getByRole('button', { name: 'Salvar' }))

    await waitFor(() => {
      expect(corpoDaChamada('PATCH').keywords).toEqual(['nubank'])
    })
  })

  // Spec 0005 §4.1.2: a mesma palavra em duas contas da casa é 409, e a tela
  // diz QUAL e em qual conta — pelo nome do cache, nunca pelo id cru.
  it('409 KEYWORD_TAKEN marca a ficha e diz em qual conta a palavra já está', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () =>
        jsonResponse(200, {
          items: [conta({ id: 'acc-nu', name: 'Nubank', keywords: ['nubank'] })],
          totalBalanceCents: 150_000,
        }),
      'POST /accounts': () =>
        jsonResponse(409, {
          error: {
            code: 'KEYWORD_TAKEN',
            message: 'keyword already used',
            fields: { keyword: 'nubank', ownerId: 'acc-nu' },
          },
        }),
    })
    renderContas()

    await screen.findByRole('button', { name: 'Editar Nubank' })
    await user.click(screen.getByRole('button', { name: 'Nova conta' }))
    await screen.findByRole('heading', { name: 'Nova conta' })

    const dialogo = screen.getByRole('dialog')
    await user.type(within(dialogo).getByLabelText('Nome'), 'Cartão Nu')
    await user.type(within(dialogo).getByLabelText('Palavras-chave'), 'NuBank{Enter}')
    await user.click(screen.getByRole('button', { name: 'Criar conta' }))

    expect(
      await within(dialogo).findByText('«NuBank» já está na conta Nubank.'),
    ).toBeInTheDocument()
    expect(within(dialogo).queryByText('acc-nu')).not.toBeInTheDocument()
    const fichas = within(within(dialogo).getByRole('list')).getAllByRole('listitem')
    expect(fichas[0]).toHaveAttribute('data-invalid', 'true')

    // Sem dona resolvível a frase é a genérica da casa — mesmo caminho, outra
    // resposta.
    await user.click(within(dialogo).getByRole('button', { name: 'Remover NuBank' }))
    expect(within(dialogo).queryByText(/já está na conta/)).not.toBeInTheDocument()
  })

  it('409 sem dona resolvível diz "outra conta desta casa"', async () => {
    const user = userEvent.setup()
    rotearApi({
      'GET /me': () => jsonResponse(200, SESSAO),
      'GET /accounts': () => jsonResponse(200, { items: [], totalBalanceCents: 0 }),
      'POST /accounts': () =>
        jsonResponse(409, {
          error: { code: 'KEYWORD_TAKEN', message: 'x', fields: { keyword: 'pix c6' } },
        }),
    })
    renderContas()

    await screen.findByRole('heading', { name: 'Contas' })
    await user.click(screen.getByRole('button', { name: 'Nova conta' }))
    await screen.findByRole('heading', { name: 'Nova conta' })

    const dialogo = screen.getByRole('dialog')
    await user.type(within(dialogo).getByLabelText('Nome'), 'C6')
    await user.type(within(dialogo).getByLabelText('Palavras-chave'), 'pix c6{Enter}')
    await user.click(screen.getByRole('button', { name: 'Criar conta' }))

    expect(
      await within(dialogo).findByText('«pix c6» já está em outra conta desta casa.'),
    ).toBeInTheDocument()
  })
})
