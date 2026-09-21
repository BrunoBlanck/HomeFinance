import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'

const fetchMock = vi.fn()

const LOTE = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8daa'
const CONTA_CORRENTE = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8d55'
const CPF = '12345678909'

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

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function rotearApi(respostaDoUpload: () => Response) {
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    const caminho = new URL(String(url), 'https://app.invalido').pathname.replace('/api/v1', '')
    if (caminho === '/me') return Promise.resolve(jsonResponse(200, SESSAO))
    if (caminho === '/accounts') return Promise.resolve(jsonResponse(200, CONTAS))
    if (caminho === '/imports' && metodo === 'POST') return Promise.resolve(respostaDoUpload())
    throw new Error(`rota não declarada no teste: ${metodo} ${caminho}`)
  })
}

function erroDaImportacao(codigo: string) {
  return () => jsonResponse(422, { error: { code: codigo, message: 'genérica' } })
}

/** O 422 do destino errado, agora com o MOTIVO estruturado que o backend passou
 *  a mandar em `fields` — é ele que vira caminho em vez de beco. */
function mismatch(fields: Record<string, string>) {
  return () =>
    jsonResponse(422, { error: { code: 'IMPORT_TARGET_MISMATCH', message: 'genérica', fields } })
}

const CARTAO = '0199a0f1-7c3e-7a2b-9f41-2f6f1c9a8dcc'

/** Um cartão de crédito do Nubank — a conta que a fatura procura. */
const CONTA_CARTAO = {
  id: CARTAO,
  name: 'Cartão Nubank',
  kind: 'credit_card',
  institution: 'nubank',
  statementClosingDay: null,
  statementDueDay: null,
  openingBalanceCents: 0,
  openingDate: '2026-01-01',
  balanceCents: 0,
  archivedAt: null,
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
}

/** Roteia com uma lista de contas VIVA (função) e um POST /accounts opcional —
 *  o suficiente para a recuperação de conta: a lista muda quando o cartão é
 *  criado, e o seletor tem de refletir isso. */
function rotearComContas(
  itens: () => unknown[],
  respostaDoUpload: () => Response,
  aoCriarConta?: () => Response,
) {
  fetchMock.mockImplementation((url: string, init?: RequestInit) => {
    const metodo = init?.method ?? 'GET'
    const caminho = new URL(String(url), 'https://app.invalido').pathname.replace('/api/v1', '')
    if (caminho === '/me') return Promise.resolve(jsonResponse(200, SESSAO))
    if (caminho === '/accounts' && metodo === 'GET') {
      return Promise.resolve(jsonResponse(200, { items: itens(), totalBalanceCents: 0 }))
    }
    if (caminho === '/accounts' && metodo === 'POST' && aoCriarConta) {
      return Promise.resolve(aoCriarConta())
    }
    if (caminho === '/imports' && metodo === 'POST') return Promise.resolve(respostaDoUpload())
    throw new Error(`rota não declarada no teste: ${metodo} ${caminho}`)
  })
}

function chamadaDoUpload() {
  const chamada = fetchMock.mock.calls.find(
    (c) => (c[1] as RequestInit | undefined)?.method === 'POST',
  )
  if (!chamada) throw new Error('o upload não foi chamado')
  return chamada[1] as RequestInit
}

function renderEnvio() {
  const router = createAppRouter(createMemoryHistory({ initialEntries: ['/importar'] }))
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

async function preencher(nomeDoArquivo = 'Nubank_2026-09-13.csv') {
  const user = userEvent.setup()
  // As opções chegam pela query de contas: o <select> existe antes delas.
  await screen.findByRole('option', { name: 'Conta corrente' })
  await user.selectOptions(screen.getByLabelText('Conta de destino'), CONTA_CORRENTE)
  await user.upload(
    screen.getByLabelText('Arquivo do extrato ou da fatura'),
    new File(['data,valor\n'], nomeDoArquivo, { type: 'text/csv' }),
  )
  return user
}

describe('ImportUploadScreen', () => {
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

  it('não pede senha para um CSV comum', async () => {
    rotearApi(() => jsonResponse(201, { id: LOTE }))
    renderEnvio()

    await preencher()
    expect(screen.queryByLabelText('Senha do arquivo')).not.toBeInTheDocument()
  })

  it('pede senha assim que o arquivo é um ZIP', async () => {
    rotearApi(() => jsonResponse(201, { id: LOTE }))
    renderEnvio()

    await preencher('Extrato_C6.zip')
    expect(await screen.findByLabelText('Senha do arquivo')).toBeInTheDocument()
    // O texto explica o que é a senha e promete que ela não fica guardada.
    expect(screen.getByText(/é o CPF do titular, só números/)).toBeInTheDocument()
  })

  it('a senha do arquivo NUNCA é oferecida ao cofre de senhas do navegador', async () => {
    rotearApi(() => jsonResponse(201, { id: LOTE }))
    renderEnvio()

    await preencher('Extrato_C6.zip')
    const campo = await screen.findByLabelText('Senha do arquivo')

    // Regressão de segurança, e o valor é exato de propósito.
    //
    // Com `current-password`, Chrome, Firefox e Safari ofereceriam SALVAR esta
    // senha como credencial da origem do HomeFinance e a sincronizariam para o
    // cofre do sistema ou da conta Google. O que seria salvo é o **CPF do
    // titular do cartão** — dado pessoal de um terceiro —, passando a viver
    // fora de qualquer ciclo de vida que a aplicação controle, o que anula todo
    // o cuidado do resto do caminho (a senha é `[]byte`, zerada em todos os
    // ramos, e não toca log, auditoria, banco nem resposta). Pior: o campo do
    // login usa `current-password` na MESMA origem, e o navegador pode
    // sobrescrever a senha da conta por este CPF e preenchê-la sozinho depois.
    //
    // O contrato já manda `autocomplete="off"` — campo `password` de
    // `POST /imports`, em `backend/api/openapi.yaml`.
    expect(campo).toHaveAttribute('autocomplete', 'off')
    expect(campo.getAttribute('autocomplete')).not.toBe('current-password')

    // E vale para todo campo de senha desta tela, não só o que tem este rótulo.
    for (const senha of Array.from(document.querySelectorAll('input[type="password"]'))) {
      expect(senha.getAttribute('autocomplete')).toBe('off')
    }
  })

  it('revela o campo e leva o FOCO até ele quando a API pede senha', async () => {
    rotearApi(erroDaImportacao('IMPORT_PASSWORD_REQUIRED'))
    renderEnvio()

    const user = await preencher()
    await user.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    const campo = await screen.findByLabelText('Senha do arquivo')
    expect(campo).toHaveFocus()
    // E um aviso explica POR QUE o campo apareceu — não "dados inválidos".
    expect(screen.getByText('Este arquivo está protegido por senha.')).toBeInTheDocument()
  })

  it('a senha some do estado assim que a requisição parte e nunca é guardada', async () => {
    rotearApi(erroDaImportacao('IMPORT_PASSWORD_INVALID'))
    renderEnvio()

    const user = await preencher('Extrato_C6.zip')
    const campo = await screen.findByLabelText<HTMLInputElement>('Senha do arquivo')
    await user.type(campo, CPF)
    await user.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    // A senha é o CPF do titular: ela vive o tempo da requisição e some.
    await waitFor(() => {
      expect(screen.getByLabelText<HTMLInputElement>('Senha do arquivo').value).toBe('')
    })

    expect(sessionStorage.length).toBe(0)
    expect(localStorage.length).toBe(0)
    expect(JSON.stringify(sessionStorage)).not.toContain(CPF)
    expect(JSON.stringify(localStorage)).not.toContain(CPF)
    // E nunca em query string — ela vaza em log, histórico e Referer.
    for (const chamada of fetchMock.mock.calls) {
      expect(String(chamada[0])).not.toContain(CPF)
    }

    // O arquivo escolhido NÃO se perde: só a senha é pedida de novo.
    expect(screen.getByText('Extrato_C6.zip')).toBeInTheDocument()
    expect(
      screen.getByText('Senha incorreta. No C6, é o CPF do titular, só números.'),
    ).toBeInTheDocument()
  })

  it('envia multipart sem Content-Type nosso — o boundary é do navegador', async () => {
    rotearApi(() => jsonResponse(201, { id: LOTE }))
    renderEnvio()

    const user = await preencher()
    await user.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    await waitFor(() => {
      const init = chamadaDoUpload()
      expect(init.body).toBeInstanceOf(FormData)
      // Definir o tipo à mão apaga o boundary e o servidor não consegue separar
      // as partes: 400 sem nenhuma pista do motivo.
      const headers = init.headers as Record<string, string>
      expect(headers['Content-Type']).toBeUndefined()
    })
  })

  it('manda só as partes que existem — a allowlist do contrato tem quatro', async () => {
    rotearApi(() => jsonResponse(201, { id: LOTE }))
    renderEnvio()

    const user = await preencher()
    await user.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    await waitFor(() => {
      const corpo = chamadaDoUpload().body as FormData
      expect([...corpo.keys()].sort()).toEqual(['accountId', 'file'])
    })
  })

  it('segue para a revisão quando a análise dá certo', async () => {
    rotearApi(() => jsonResponse(201, { id: LOTE }))
    const router = renderEnvio()

    const user = await preencher()
    await user.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    await waitFor(() => {
      expect(router.state.location.pathname).toBe(`/importar/${LOTE}/revisar`)
    })
  })

  it('manda trocar a CONTA, não o arquivo, quando o destino não bate', async () => {
    rotearApi(erroDaImportacao('IMPORT_TARGET_MISMATCH'))
    renderEnvio()

    const user = await preencher()
    await user.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    // O arquivo está certo; o destino é que não é dele. O erro fica no campo
    // da conta, que é o que a pessoa precisa mexer.
    expect(
      await screen.findByText(
        'Este arquivo não é desta conta. Escolha a conta a que ele pertence.',
      ),
    ).toBeInTheDocument()
  })

  it('cobra a conta e o arquivo antes de gastar uma requisição', async () => {
    rotearApi(() => jsonResponse(201, { id: LOTE }))
    renderEnvio()

    await screen.findByLabelText('Conta de destino')
    await userEvent.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    expect(await screen.findByText('Escolha a conta de destino.')).toBeInTheDocument()
    expect(screen.getByText('Escolha o arquivo do extrato ou da fatura.')).toBeInTheDocument()
    expect(fetchMock.mock.calls.some((c) => (c[1] as RequestInit)?.method === 'POST')).toBe(false)
  })

  // ---------------------------------- recuperação: fatura na conta errada

  it('fatura na conta errada oferece criar a conta de cartão, já preenchida pela instituição', async () => {
    rotearApi(
      mismatch({
        reason: 'expected_credit_card',
        detectedInstitution: 'nubank',
        detectedDocKind: 'card_statement',
      }),
    )
    renderEnvio()

    const user = await preencher()
    await user.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    // Explica em pt-BR para onde a fatura vai — não o beco "não é desta conta".
    expect(await screen.findByText('Isto parece uma fatura de cartão.')).toBeInTheDocument()
    // O arquivo escolhido sobrevive ao erro: só a conta é que estava errada.
    expect(screen.getByText('Nubank_2026-09-13.csv')).toBeInTheDocument()

    // O botão de criar já nomeia a instituição detectada.
    await user.click(screen.getByRole('button', { name: 'Criar conta de cartão do Nubank' }))

    // O diálogo abre já preenchido: tipo cartão, instituição Nubank, nome sugerido.
    expect(await screen.findByRole('heading', { name: 'Nova conta' })).toBeInTheDocument()
    expect(screen.getByLabelText('Nome')).toHaveValue('Cartão Nubank')
    expect(screen.getByLabelText('Tipo')).toHaveValue('credit_card')
    expect(screen.getByLabelText('Instituição')).toHaveValue('nubank')
  })

  it('criar a conta de cartão pré-seleciona a nova conta no seletor, sem perder o arquivo', async () => {
    let criado = false
    rotearComContas(
      () => (criado ? [CONTAS.items[0], CONTA_CARTAO] : CONTAS.items),
      mismatch({
        reason: 'expected_credit_card',
        detectedInstitution: 'nubank',
        detectedDocKind: 'card_statement',
      }),
      () => {
        criado = true
        return jsonResponse(201, CONTA_CARTAO)
      },
    )
    renderEnvio()

    const user = await preencher()
    await user.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    await user.click(await screen.findByRole('button', { name: 'Criar conta de cartão do Nubank' }))
    await screen.findByRole('heading', { name: 'Nova conta' })
    // O nome já vem preenchido; basta confirmar.
    await user.click(screen.getByRole('button', { name: 'Criar conta' }))

    // A conta nova entra no seletor (a query de contas foi invalidada) e já fica
    // pré-selecionada como destino — o passo seguinte é só reenviar.
    await waitFor(() => {
      expect(screen.getByLabelText('Conta de destino')).toHaveValue(CARTAO)
    })
    // E o arquivo continua lá — a senha do ZIP é repedida, o arquivo não.
    expect(screen.getByText('Nubank_2026-09-13.csv')).toBeInTheDocument()
  })

  it('quando já existe um cartão, oferece escolhê-lo — e a escolha vira o destino', async () => {
    rotearComContas(
      () => [CONTAS.items[0], CONTA_CARTAO],
      mismatch({
        reason: 'expected_credit_card',
        detectedInstitution: 'nubank',
        detectedDocKind: 'card_statement',
      }),
    )
    renderEnvio()

    const user = await preencher()
    await user.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    // O aviso traz um seletor só com as contas de cartão.
    const escolher = await screen.findByLabelText('Conta de cartão')
    await user.selectOptions(escolher, CARTAO)

    // Escolher no aviso é escolher o destino: os dois seletores compartilham o
    // estado, então reenviar já vai na conta certa.
    expect(screen.getByLabelText('Conta de destino')).toHaveValue(CARTAO)
  })

  it('extrato num cartão guia a escolher ou criar uma conta corrente', async () => {
    rotearApi(
      mismatch({
        reason: 'expected_bank_account',
        detectedInstitution: 'c6',
        detectedDocKind: 'checking_statement',
      }),
    )
    renderEnvio()

    const user = await preencher()
    await user.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    expect(await screen.findByText('Isto parece um extrato de conta.')).toBeInTheDocument()
    expect(screen.getByText(/Extratos vão para uma conta corrente/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Criar conta do C6' })).toBeInTheDocument()
  })

  it('arquivo de um banco e conta de outro pede a conta certa, sem oferecer criar', async () => {
    rotearApi(
      mismatch({
        reason: 'wrong_institution',
        detectedInstitution: 'c6',
        accountInstitution: 'nubank',
        detectedDocKind: 'card_statement',
      }),
    )
    renderEnvio()

    const user = await preencher()
    await user.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    const aviso = await screen.findByRole('alert')
    expect(
      within(aviso).getByText('O arquivo e a conta são de instituições diferentes.'),
    ).toBeInTheDocument()
    expect(
      within(aviso).getByText(
        'Este arquivo é do C6, e a conta que você escolheu é do Nubank. Escolha a conta do C6.',
      ),
    ).toBeInTheDocument()
    // Aqui não se cria conta — é escolha da conta certa, não falta de uma.
    expect(within(aviso).queryByRole('button')).not.toBeInTheDocument()
  })

  it('sem motivo legível (unknown_document), mantém a mensagem genérica e nenhum aviso', async () => {
    rotearApi(mismatch({ reason: 'unknown_document', detectedDocKind: 'card_statement' }))
    renderEnvio()

    const user = await preencher()
    await user.click(screen.getByRole('button', { name: 'Analisar arquivo' }))

    expect(
      await screen.findByText(
        'Este arquivo não é desta conta. Escolha a conta a que ele pertence.',
      ),
    ).toBeInTheDocument()
    // Sem caminho para montar, cai no beco antigo — e nenhum bloco de recuperação.
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
})
