import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'
import { clearRegistration } from '../storage/registrationToken'

const fetchMock = vi.fn()

/** Dois tokens bem formados e visivelmente diferentes: o guardado e o que o
 *  reenvio devolve. 64 hexadecimais, como o backend emite (ADR-014). */
const TOKEN_GUARDADO = 'a1'.repeat(32)
const TOKEN_DO_REENVIO = 'b2'.repeat(32)

const EMAIL = 'bruno@example.com'
const OUTRO_EMAIL = 'vizinho@example.com'
const KEY = 'hf.registration'

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function verificationAccepted(token: string) {
  return jsonResponse(202, {
    status: 'verification_required',
    email: EMAIL,
    expiresInSeconds: 900,
    registrationToken: token,
  })
}

/** Simula a aba que veio do cadastro: o par (token, endereço) sobreviveu. */
function seedRegistration(token: string, email: string) {
  sessionStorage.setItem(KEY, JSON.stringify({ token, email }))
}

/** Liga o relógio falso ANTES de montar. `shouldAdvanceTime` mantém os timers
 *  correndo sozinhos, que é do que o userEvent precisa para digitar e clicar. */
function comRelogioControlado() {
  vi.useFakeTimers({ shouldAdvanceTime: true })
}

/** Zera a espera do reenvio de forma determinística: adianta o relógio para
 *  depois do prazo (gravado em milissegundos absolutos) e **força** o tique de
 *  1 s do contador, em vez de esperar o relógio real. Esperar de verdade
 *  deixava o teste refém da carga da máquina. Chamar sempre depois da
 *  montagem: é ela que grava o primeiro degrau. */
async function pularEsperaDoReenvio() {
  vi.setSystemTime(Date.now() + 61_000)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1100)
  })
}

function bodyOf(call: unknown[] | undefined): Record<string, unknown> {
  const init = call?.[1] as RequestInit | undefined
  return JSON.parse(String(init?.body ?? '{}')) as Record<string, unknown>
}

function callsTo(path: string): unknown[][] {
  return fetchMock.mock.calls.filter((call) => String(call[0]).endsWith(path))
}

function renderApp(entry = '/confirmar-email') {
  const history = createMemoryHistory({ initialEntries: [entry] })
  const router = createAppRouter(history)
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

/** A tela só é alcançada com o endereço no state do roteador — é assim que o
 *  cadastro e o login chegam nela. `initialEntries` não carrega state, então a
 *  navegação é feita pelo próprio roteador. */
async function irParaConfirmacao(email: string) {
  const router = renderApp('/entrar')
  await screen.findByRole('heading', { name: 'Entrar' })
  await act(async () => {
    await router.navigate({ to: '/confirmar-email', state: { email } })
  })
  await screen.findByRole('heading', { name: 'Confirme seu e-mail' })
  return router
}

describe('ConfirmEmailScreen', () => {
  beforeEach(() => {
    sessionStorage.clear()
    clearRegistration()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
  })

  it('envia sozinho ao completar os 6 dígitos e uma única vez', async () => {
    seedRegistration(TOKEN_GUARDADO, EMAIL)
    fetchMock.mockResolvedValue(jsonResponse(401, { error: { code: 'INVALID_CODE' } }))
    await irParaConfirmacao(EMAIL)

    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Código de 6 dígitos'), '123456')

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Código inválido ou expirado. Peça um novo código se precisar.')
    expect(callsTo('/auth/verify-email')).toHaveLength(1)
  })

  it('não conta tentativas para o usuário e preserva o que ele digitou', async () => {
    seedRegistration(TOKEN_GUARDADO, EMAIL)
    fetchMock.mockResolvedValue(
      jsonResponse(401, { error: { code: 'INVALID_CODE', message: 'restam 2 tentativas' } }),
    )
    await irParaConfirmacao(EMAIL)

    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Código de 6 dígitos'), '123456')

    await screen.findByRole('alert')
    expect(screen.queryByText(/tentativa/i)).not.toBeInTheDocument()
    expect(screen.getByLabelText('Código de 6 dígitos')).toHaveValue('123456')
    expect(screen.getByLabelText('Código de 6 dígitos')).toHaveFocus()
  })

  // É a defesa contra o pre-hijacking: sem esta metade, o código que chega na
  // caixa da vítima confirmaria a tentativa de quem quer que o tenha pedido.
  it('manda o registrationToken guardado junto do código', async () => {
    seedRegistration(TOKEN_GUARDADO, EMAIL)
    fetchMock.mockResolvedValue(jsonResponse(401, { error: { code: 'INVALID_CODE' } }))
    await irParaConfirmacao(EMAIL)

    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Código de 6 dígitos'), '123456')
    await screen.findByRole('alert')

    expect(bodyOf(callsTo('/auth/verify-email')[0])).toEqual({
      email: EMAIL,
      code: '123456',
      registrationToken: TOKEN_GUARDADO,
    })
  })

  // Para o servidor, token de outro endereço é indistinguível de token nenhum:
  // a tentativa é buscada pelo par (e-mail, hash do token). A tela tem de
  // concordar, senão oferece um formulário em que nada jamais funcionaria.
  it('token de OUTRO endereço vale o mesmo que token nenhum', async () => {
    seedRegistration(TOKEN_GUARDADO, OUTRO_EMAIL)
    await irParaConfirmacao(EMAIL)

    expect(screen.queryByLabelText('Código de 6 dígitos')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Enviar outro código' })).not.toBeInTheDocument()
    expect(screen.getByText('Refaça o cadastro para receber um código novo')).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })

  // A tela não tem campo de e-mail: uma aba que não sabe qual endereço pediu o
  // código também não sabe se o token dela governa o endereço que a pessoa
  // digitaria. Sem o campo, o descasamento é inalcançável.
  it('não oferece campo de e-mail em nenhuma das aberturas', async () => {
    seedRegistration(TOKEN_GUARDADO, EMAIL)
    await irParaConfirmacao(EMAIL)
    expect(screen.queryByLabelText('E-mail')).not.toBeInTheDocument()

    clearRegistration()
    sessionStorage.clear()
    renderApp('/confirmar-email')
    await screen.findAllByRole('heading', { name: 'Confirme seu e-mail' })
    expect(screen.queryByLabelText('E-mail')).not.toBeInTheDocument()
  })

  it('substitui o token guardado pelo que volta do reenvio', async () => {
    comRelogioControlado()
    seedRegistration(TOKEN_GUARDADO, EMAIL)
    fetchMock.mockImplementation((url: string) =>
      Promise.resolve(
        String(url).endsWith('/auth/resend-code')
          ? verificationAccepted(TOKEN_DO_REENVIO)
          : jsonResponse(401, { error: { code: 'INVALID_CODE' } }),
      ),
    )
    await irParaConfirmacao(EMAIL)

    const user = userEvent.setup()
    await pularEsperaDoReenvio()
    await waitFor(() => expect(screen.queryByText(/disponível em/)).not.toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Enviar outro código' }))
    await screen.findByText('Se o e-mail estiver cadastrado, enviamos um novo código.')

    // O reenvio levou o token antigo, com o endereço dele...
    expect(bodyOf(callsTo('/auth/resend-code')[0])).toEqual({
      email: EMAIL,
      registrationToken: TOKEN_GUARDADO,
    })
    // ...e o que voltou passou a ser o guardado, no storage e na requisição
    // seguinte, ainda amarrado ao mesmo endereço.
    expect(JSON.parse(String(sessionStorage.getItem(KEY)))).toEqual({
      token: TOKEN_DO_REENVIO,
      email: EMAIL,
    })

    await user.type(screen.getByLabelText('Código de 6 dígitos'), '654321')
    await screen.findByRole('alert')
    expect(bodyOf(callsTo('/auth/verify-email')[0])?.registrationToken).toBe(TOKEN_DO_REENVIO)
  })

  it('apaga o par guardado quando a verificação dá certo', async () => {
    seedRegistration(TOKEN_GUARDADO, EMAIL)
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        user: {
          id: '11111111-1111-4111-8111-111111111111',
          name: 'Bruno',
          email: EMAIL,
          emailVerifiedAt: '2026-09-09T12:00:00Z',
          createdAt: '2026-09-09T12:00:00Z',
        },
        household: { id: '22222222-2222-4222-8222-222222222222', name: 'Casa', role: 'owner' },
        households: [{ id: '22222222-2222-4222-8222-222222222222', name: 'Casa', role: 'owner' }],
      }),
    )
    await irParaConfirmacao(EMAIL)

    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Código de 6 dígitos'), '123456')

    await waitFor(() => expect(sessionStorage.getItem(KEY)).toBeNull())
  })

  it('sem token guardado, não pede código nenhum e manda refazer o cadastro', async () => {
    const router = renderApp('/confirmar-email')
    await screen.findByRole('heading', { name: 'Confirme seu e-mail' })

    expect(screen.queryByLabelText('Código de 6 dígitos')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Enviar outro código' })).not.toBeInTheDocument()
    expect(screen.getByText('Refaça o cadastro para receber um código novo')).toBeInTheDocument()

    const user = userEvent.setup()
    await user.click(screen.getByRole('link', { name: 'Ir para o cadastro' }))

    await screen.findByRole('heading', { name: 'Criar conta' })
    expect(router.state.location.pathname).toBe('/criar-conta')
    // Nada foi pedido à API: `resend-code` sem token não emite nada, então
    // oferecê-lo aqui seria empurrar a pessoa para um beco.
    expect(fetchMock).not.toHaveBeenCalled()
  })

  // Trava de regressão: o caminho "reenvio sem token" foi removido do backend
  // por ser explorável (a tentativa sucessora herdava credenciais de outra
  // pessoa). Nenhuma requisição pode sair sem o campo. O estado sem token está
  // coberto no teste acima, que prova que a API nem chega a ser chamada.
  it('nunca chama resend-code sem registrationToken', async () => {
    comRelogioControlado()
    seedRegistration(TOKEN_GUARDADO, EMAIL)
    fetchMock.mockImplementation((url: string) =>
      Promise.resolve(
        String(url).endsWith('/auth/resend-code')
          ? verificationAccepted(TOKEN_DO_REENVIO)
          : jsonResponse(401, { error: { code: 'INVALID_CODE' } }),
      ),
    )
    await irParaConfirmacao(EMAIL)

    const user = userEvent.setup()
    await pularEsperaDoReenvio()
    await waitFor(() => expect(screen.queryByText(/disponível em/)).not.toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Enviar outro código' }))
    await screen.findByText('Se o e-mail estiver cadastrado, enviamos um novo código.')

    const reenvios = callsTo('/auth/resend-code')
    expect(reenvios.length).toBeGreaterThan(0)
    for (const call of reenvios) {
      const body = bodyOf(call)
      expect(body.registrationToken).toMatch(/^[0-9a-f]{64}$/)
      expect(body.email).toBe(EMAIL)
    }
  })

  it('não revela se o endereço existe no caminho sem token', async () => {
    renderApp('/confirmar-email')
    await screen.findByRole('heading', { name: 'Confirme seu e-mail' })

    const texto = document.body.textContent ?? ''
    expect(texto).not.toMatch(/não existe|inexistente|não encontrad|já cadastrad/i)
    expect(texto).not.toContain('!')
  })

  it('nunca coloca o token em URL nenhuma', async () => {
    comRelogioControlado()
    seedRegistration(TOKEN_GUARDADO, EMAIL)
    fetchMock.mockImplementation((url: string) =>
      Promise.resolve(
        String(url).endsWith('/auth/resend-code')
          ? verificationAccepted(TOKEN_DO_REENVIO)
          : jsonResponse(401, { error: { code: 'INVALID_CODE' } }),
      ),
    )
    const router = await irParaConfirmacao(EMAIL)

    const user = userEvent.setup()
    await pularEsperaDoReenvio()
    await waitFor(() => expect(screen.queryByText(/disponível em/)).not.toBeInTheDocument())
    await user.click(screen.getByRole('button', { name: 'Enviar outro código' }))
    await screen.findByText('Se o e-mail estiver cadastrado, enviamos um novo código.')
    await user.type(screen.getByLabelText('Código de 6 dígitos'), '123456')
    await screen.findByRole('alert')

    // O token vai no CORPO. Query string vaza em log de servidor, histórico do
    // navegador e cabeçalho Referer (docs/SEGURANCA.md §6).
    for (const [url] of fetchMock.mock.calls as [string, RequestInit][]) {
      expect(url).not.toContain(TOKEN_GUARDADO)
      expect(url).not.toContain(TOKEN_DO_REENVIO)
    }
    for (const alvo of [
      router.state.location.href,
      router.state.location.searchStr,
      window.location.href,
      document.location.hash,
      ...[...document.querySelectorAll('a')].map((a) => a.getAttribute('href') ?? ''),
    ]) {
      expect(alvo).not.toContain(TOKEN_GUARDADO)
      expect(alvo).not.toContain(TOKEN_DO_REENVIO)
    }
    // E nem em campo escondido de formulário, que viraria URL num submit nativo.
    expect(document.querySelector('input[type="hidden"]')).toBeNull()
  })
})
