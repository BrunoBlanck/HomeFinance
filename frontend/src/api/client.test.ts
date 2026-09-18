import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, apiRequest, NetworkError } from './client'

const fetchMock = vi.fn()

/** O que o cliente realmente mandou ao navegador na chamada de índice `i`. */
function chamada(i = 0): { url: string; init: RequestInit } {
  const args = fetchMock.mock.calls[i]
  if (!args) throw new Error(`fetch não foi chamado ${i + 1} vez(es)`)
  return { url: String(args[0]), init: (args[1] ?? {}) as RequestInit }
}

function headers(i = 0): Headers {
  return new Headers(chamada(i).init.headers)
}

function jsonResponse(status: number, body: unknown) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

beforeEach(() => {
  fetchMock.mockReset()
  vi.stubGlobal('fetch', fetchMock)
})

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('apiRequest — corpo JSON', () => {
  it('serializa o objeto e declara o Content-Type', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, { id: 'a-1' }))

    await apiRequest('/accounts', { method: 'POST', body: { name: 'Conta corrente' } })

    expect(headers().get('Content-Type')).toBe('application/json')
    expect(chamada().init.body).toBe('{"name":"Conta corrente"}')
  })

  it('sem corpo, não inventa Content-Type nenhum', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, []))

    await apiRequest('/accounts')

    expect(headers().has('Content-Type')).toBe(false)
    expect(chamada().init.body).toBeUndefined()
  })

  it('manda o cookie de sessão junto — a sessão vive em HttpOnly, não em token lido', async () => {
    fetchMock.mockResolvedValue(jsonResponse(200, []))

    await apiRequest('/accounts')

    expect(chamada().init.credentials).toBe('include')
  })
})

/** O `Content-Type` de um multipart carrega o `boundary`, e o navegador só o
 *  gera se o header estiver vazio. Preenchê-lo faz o servidor receber um corpo
 *  que não consegue separar em partes — 400 sem pista nenhuma de causa. É o
 *  caminho do envio de extrato, então está coberto por teste. */
describe('apiRequest — corpo FormData (envio de arquivo)', () => {
  function formulario(): FormData {
    const form = new FormData()
    form.append('accountId', '11111111-1111-4111-8111-111111111111')
    form.append('file', new File(['data,valor\n'], 'extrato.csv', { type: 'text/csv' }))
    return form
  }

  it('não define Content-Type, para o navegador pôr o boundary', async () => {
    fetchMock.mockResolvedValue(jsonResponse(201, { importId: 'imp-1' }))

    await apiRequest('/imports', { method: 'POST', body: formulario() })

    expect(headers().has('Content-Type')).toBe(false)
  })

  it('passa o FormData direto, sem passar por JSON.stringify', async () => {
    fetchMock.mockResolvedValue(jsonResponse(201, { importId: 'imp-1' }))

    const form = formulario()
    await apiRequest('/imports', { method: 'POST', body: form })

    // `JSON.stringify(new FormData())` devolveria "{}" — o arquivo sumiria e o
    // erro só apareceria do lado do servidor.
    expect(chamada().init.body).toBe(form)
    expect(chamada().init.method).toBe('POST')
  })

  it('reenvia o mesmo FormData depois de renovar a sessão', async () => {
    fetchMock
      .mockResolvedValueOnce(jsonResponse(401, { error: { code: 'UNAUTHORIZED' } }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(jsonResponse(201, { importId: 'imp-1' }))

    const form = formulario()
    await apiRequest('/imports', { method: 'POST', body: form })

    expect(fetchMock).toHaveBeenCalledTimes(3)
    expect(chamada(1).url).toContain('/auth/refresh')
    // A repetição não pode virar JSON nem perder o arquivo.
    expect(chamada(2).init.body).toBe(form)
    expect(headers(2).has('Content-Type')).toBe(false)
  })
})

describe('apiRequest — erros', () => {
  it('guarda status, código e os NOMES dos campos, nunca o texto do servidor', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(422, {
        error: {
          code: 'VALIDATION_FAILED',
          message: 'o e-mail bruno@example.com já existe',
          fields: { email: 'duplicado' },
        },
      }),
    )

    const erro = await apiRequest('/accounts', { method: 'POST', body: {} }).catch(
      (e: unknown) => e,
    )

    expect(erro).toBeInstanceOf(ApiError)
    const api = erro as ApiError
    expect(api.status).toBe(422)
    expect(api.code).toBe('VALIDATION_FAILED')
    expect(api.invalidFields).toEqual(['email'])
    // O texto do servidor não pode chegar à interface (src/lib/errors.ts é a
    // fonte das mensagens); nem por acaso, no message da exceção.
    expect(api.message).not.toContain('bruno@example.com')
  })

  it('preserva os VALORES de fields, não só os nomes — a tela lê o motivo', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(422, {
        error: {
          code: 'IMPORT_TARGET_MISMATCH',
          message: 'genérica',
          fields: {
            reason: 'expected_credit_card',
            detectedInstitution: 'nubank',
            detectedDocKind: 'card_statement',
          },
        },
      }),
    )

    const erro = (await apiRequest('/imports', { method: 'POST', body: {} }).catch(
      (e: unknown) => e,
    )) as ApiError

    // O mapa completo chega à tela — é o que transforma "não é desta conta" num
    // caminho para criar/escolher a conta de cartão.
    expect(erro.fields).toEqual({
      reason: 'expected_credit_card',
      detectedInstitution: 'nubank',
      detectedDocKind: 'card_statement',
    })
    // E `invalidFields` continua sendo só os nomes: os formulários que marcam
    // campo a campo não podem regredir.
    expect(erro.invalidFields).toEqual(['reason', 'detectedInstitution', 'detectedDocKind'])
  })

  it('em fields, guarda só os valores string — o resto não vira texto na tela', async () => {
    fetchMock.mockResolvedValue(
      jsonResponse(422, {
        error: {
          code: 'VALIDATION_FAILED',
          fields: { email: 'duplicado', detalhe: { aninhado: true }, tentativas: 3 },
        },
      }),
    )

    const erro = (await apiRequest('/x', { method: 'POST', body: {} }).catch(
      (e: unknown) => e,
    )) as ApiError

    expect(erro.fields).toEqual({ email: 'duplicado' })
    // O nome do campo ainda aparece para quem marca campo inválido, mesmo quando
    // o valor não é string.
    expect(erro.invalidFields).toEqual(['email', 'detalhe', 'tentativas'])
  })

  it('falha de rede vira NetworkError, não vaza o erro do fetch', async () => {
    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'))

    await expect(apiRequest('/accounts')).rejects.toBeInstanceOf(NetworkError)
  })
})
