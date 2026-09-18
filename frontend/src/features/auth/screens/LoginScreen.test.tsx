import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'
import { clearRegistration } from '../storage/registrationToken'

const fetchMock = vi.fn()

/** 64 hexadecimais, a forma exata do registrationToken (ADR-014). O cliente o
 *  guarda amarrado ao endereço que o pediu. */
const TOKEN = 'd4'.repeat(32)
const EMAIL = 'bruno@example.com'
const KEY = 'hf.registration'

function seedRegistration(email: string) {
  sessionStorage.setItem(KEY, JSON.stringify({ token: TOKEN, email }))
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function renderApp() {
  const router = createAppRouter(createMemoryHistory({ initialEntries: ['/entrar'] }))
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

async function submitLogin() {
  const user = userEvent.setup()
  await screen.findByRole('heading', { name: 'Entrar' })
  await user.type(screen.getByLabelText('E-mail'), 'bruno@example.com')
  await user.type(screen.getByLabelText('Senha'), 'uma frase bem longa')
  await user.click(screen.getByRole('button', { name: 'Entrar' }))
}

describe('LoginScreen', () => {
  beforeEach(() => {
    sessionStorage.clear()
    clearRegistration()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('usa a mesma frase para conta inexistente e senha errada', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(401, { error: { code: 'INVALID_CREDENTIALS', message: 'qualquer coisa' } }),
    )
    renderApp()
    await submitLogin()

    expect(await screen.findByRole('alert')).toHaveTextContent('E-mail ou senha incorretos.')
  })

  it('envia o cookie de sessão junto da requisição', async () => {
    fetchMock.mockResolvedValue(jsonResponse(401, { error: { code: 'INVALID_CREDENTIALS' } }))
    renderApp()
    await submitLogin()

    await screen.findByRole('alert')
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(init.credentials).toBe('include')
  })

  it('leva para a confirmação no 403 EMAIL_NOT_VERIFIED quando a aba ainda tem o token', async () => {
    // O 403 não devolve token; quem tem o desta aba é a própria pessoa que
    // pediu o cadastro aqui, e o código reemitido pertence à mesma tentativa.
    seedRegistration(EMAIL)
    fetchMock.mockResolvedValue(
      jsonResponse(403, { error: { code: 'EMAIL_NOT_VERIFIED', message: 'não verificado' } }),
    )
    const router = renderApp()
    await submitLogin()

    expect(await screen.findByRole('heading', { name: 'Confirme seu e-mail' })).toBeInTheDocument()
    expect(
      await screen.findByText(
        'Confirme seu e-mail para entrar. Use o código que enviamos — se não encontrar, peça um novo.',
      ),
    ).toBeInTheDocument()
    expect(screen.getByText('bruno@example.com')).toBeInTheDocument()

    // O e-mail viaja no state do roteador — nunca em query string.
    expect(router.state.location.href).toBe('/confirmar-email')
    expect(router.state.location.href).not.toContain('@')
    expect(router.state.location.href).not.toContain(TOKEN)
  })

  it('manda para o cadastro no 403 EMAIL_NOT_VERIFIED quando a aba não tem token', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(403, { error: { code: 'EMAIL_NOT_VERIFIED', message: 'não verificado' } }),
    )
    const router = renderApp()
    await submitLogin()

    expect(await screen.findByRole('heading', { name: 'Criar conta' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/criar-conta')
    expect(await screen.findByText('Confirme seu e-mail para entrar.')).toBeInTheDocument()
    // O e-mail chega preenchido pelo state — a pessoa não redigita, e nada
    // disso passa pela URL.
    expect(screen.getByLabelText('E-mail')).toHaveValue('bruno@example.com')
    expect(router.state.location.href).not.toContain('@')
  })

  // Token de OUTRO endereço é, para o servidor, o mesmo que token nenhum: a
  // tentativa é buscada pelo par (e-mail, hash do token). Mandar a pessoa para
  // a confirmação com ele produziria um beco — todo código falharia e o caminho
  // de volta ao cadastro ficaria escondido atrás do formulário.
  it('manda para o cadastro quando a aba tem token de OUTRO endereço', async () => {
    seedRegistration('vizinho@example.com')
    fetchMock.mockResolvedValue(
      jsonResponse(403, { error: { code: 'EMAIL_NOT_VERIFIED', message: 'não verificado' } }),
    )
    const router = renderApp()
    await submitLogin()

    expect(await screen.findByRole('heading', { name: 'Criar conta' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/criar-conta')
    expect(screen.getByLabelText('E-mail')).toHaveValue(EMAIL)
  })

  it('apaga o token de cadastro ao criar sessão', async () => {
    seedRegistration(EMAIL)
    fetchMock.mockResolvedValue(
      jsonResponse(200, {
        user: {
          id: '11111111-1111-4111-8111-111111111111',
          name: 'Bruno',
          email: 'bruno@example.com',
          emailVerifiedAt: '2026-09-09T12:00:00Z',
          createdAt: '2026-09-09T12:00:00Z',
        },
        household: {
          id: '22222222-2222-4222-8222-222222222222',
          name: 'Casa',
          role: 'owner',
          timezone: 'America/Sao_Paulo',
          currency: 'BRL',
        },
        households: [
          {
            id: '22222222-2222-4222-8222-222222222222',
            name: 'Casa',
            role: 'owner',
            timezone: 'America/Sao_Paulo',
            currency: 'BRL',
          },
        ],
      }),
    )
    renderApp()
    await submitLogin()

    await waitFor(() => expect(sessionStorage.getItem(KEY)).toBeNull())
  })

  it('valida os campos localmente antes de chamar a API', async () => {
    const user = userEvent.setup()
    renderApp()
    await screen.findByRole('heading', { name: 'Entrar' })

    await user.click(screen.getByRole('button', { name: 'Entrar' }))

    expect(await screen.findByText('Informe seu e-mail.')).toBeInTheDocument()
    expect(screen.getByText('Informe sua senha.')).toBeInTheDocument()
    expect(fetchMock).not.toHaveBeenCalled()
  })
})
