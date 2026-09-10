import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, RouterProvider } from '@tanstack/react-router'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createAppRouter } from '@/app/router'
import { clearRegistration } from '../storage/registrationToken'

const fetchMock = vi.fn()

const TOKEN_DO_CADASTRO = 'c3'.repeat(32)
const EMAIL = 'bruno@example.com'
const KEY = 'hf.registration'

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function renderApp() {
  const router = createAppRouter(createMemoryHistory({ initialEntries: ['/criar-conta'] }))
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

async function preencherCadastro() {
  const user = userEvent.setup()
  await screen.findByRole('heading', { name: 'Criar conta' })
  await user.type(screen.getByLabelText('Nome'), 'Bruno')
  await user.type(screen.getByLabelText('E-mail'), 'bruno@example.com')
  await user.type(screen.getByLabelText('Senha'), 'uma frase bem longa')
  await user.click(screen.getByRole('button', { name: 'Criar conta' }))
  return user
}

describe('RegisterScreen', () => {
  beforeEach(() => {
    sessionStorage.clear()
    localStorage.clear()
    clearRegistration()
    vi.stubGlobal('fetch', fetchMock)
    fetchMock.mockReset()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('guarda o registrationToken do 202 e o usa na confirmação', async () => {
    fetchMock.mockImplementation((url: string) =>
      Promise.resolve(
        String(url).endsWith('/auth/register')
          ? jsonResponse(202, {
              status: 'verification_required',
              email: 'bruno@example.com',
              expiresInSeconds: 900,
              registrationToken: TOKEN_DO_CADASTRO,
            })
          : jsonResponse(401, { error: { code: 'INVALID_CODE' } }),
      ),
    )
    const router = renderApp()
    const user = await preencherCadastro()

    await screen.findByRole('heading', { name: 'Confirme seu e-mail' })
    // Guardado amarrado ao endereço que o pediu: é o par que o servidor consulta.
    expect(JSON.parse(String(sessionStorage.getItem(KEY)))).toEqual({
      token: TOKEN_DO_CADASTRO,
      email: EMAIL,
    })
    // Nunca no localStorage: lá o token sobreviveria ao fim do fluxo e a todas
    // as abas do perfil.
    expect(localStorage.length).toBe(0)

    await user.type(screen.getByLabelText('Código de 6 dígitos'), '123456')
    await screen.findByRole('alert')

    const verify = fetchMock.mock.calls.find((call) =>
      String(call[0]).endsWith('/auth/verify-email'),
    ) as [string, RequestInit] | undefined
    const body = JSON.parse(String(verify?.[1]?.body)) as Record<string, unknown>
    expect(body.registrationToken).toBe(TOKEN_DO_CADASTRO)

    // Nem o e-mail nem o token viajam na URL — os dois vão por state e corpo.
    expect(router.state.location.href).toBe('/confirmar-email')
    for (const [url] of fetchMock.mock.calls as [string, RequestInit][]) {
      expect(url).not.toContain(TOKEN_DO_CADASTRO)
    }
  })

  it('cai no caminho de recuperação se o 202 vier sem token utilizável', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(202, {
        status: 'verification_required',
        email: 'bruno@example.com',
        expiresInSeconds: 900,
        registrationToken: 'token-que-nao-e-hexadecimal',
      }),
    )
    renderApp()
    await preencherCadastro()

    await screen.findByRole('heading', { name: 'Confirme seu e-mail' })
    expect(sessionStorage.getItem(KEY)).toBeNull()
    // Sem token não se pede código: a tela mostra a saída em vez de um campo
    // que só produziria 400.
    expect(screen.queryByLabelText('Código de 6 dígitos')).not.toBeInTheDocument()
    expect(screen.getByText('Refaça o cadastro para receber um código novo')).toBeInTheDocument()
  })
})
