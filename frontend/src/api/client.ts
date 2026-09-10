/** Cliente HTTP único do HomeFinance.
 *
 *  Sessão vive em cookies `HttpOnly` emitidos pelo backend — o frontend nunca lê
 *  nem grava token, e por isso toda chamada precisa de `credentials: 'include'`.
 *  Em desenvolvimento o Vite faz proxy de `/api`, então a origem é a mesma e o
 *  `SameSite=Strict` dos cookies continua valendo.
 *
 *  Nenhuma mensagem do servidor é repassada à interface: guardamos status, código
 *  e os NOMES dos campos inválidos; o texto sai sempre de `src/lib/errors.ts`. */

const BASE_URL = '/api/v1'

export type ApiErrorCode =
  | 'VALIDATION_FAILED'
  | 'INVALID_CREDENTIALS'
  | 'INVALID_CODE'
  | 'EMAIL_NOT_VERIFIED'
  | 'UNAUTHENTICATED'
  | 'INVALID_SESSION'
  | 'FORBIDDEN'
  | 'NOT_FOUND'
  | 'METHOD_NOT_ALLOWED'
  | 'RATE_LIMITED'
  | 'PAYLOAD_TOO_LARGE'
  | 'UNSUPPORTED_MEDIA_TYPE'
  | 'SERVICE_UNAVAILABLE'
  | 'INTERNAL_ERROR'

export class ApiError extends Error {
  readonly status: number
  readonly code: ApiErrorCode | null
  readonly invalidFields: readonly string[]

  constructor(status: number, code: ApiErrorCode | null, invalidFields: readonly string[]) {
    super(`Resposta ${status} da API`)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.invalidFields = invalidFields
  }
}

export class NetworkError extends Error {
  constructor() {
    super('Falha de rede ao falar com a API')
    this.name = 'NetworkError'
  }
}

export type RequestOptions = {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  body?: unknown
  signal?: AbortSignal
}

/** Rotas de autenticação nunca disparam refresh: o 401 delas é a resposta, não
 *  uma sessão vencida. Sem isso, um login errado viraria um laço de refresh. */
const SKIP_REFRESH = new Set([
  '/auth/login',
  '/auth/refresh',
  '/auth/logout',
  '/auth/register',
  '/auth/verify-email',
  '/auth/resend-code',
  '/auth/forgot-password',
  '/auth/reset-password',
])

let refreshInFlight: Promise<boolean> | null = null

async function send(path: string, options: RequestOptions): Promise<Response> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const init: RequestInit = {
    method: options.method ?? 'GET',
    credentials: 'include',
    headers,
  }
  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(options.body)
  }
  if (options.signal) init.signal = options.signal

  try {
    return await fetch(`${BASE_URL}${path}`, init)
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error
    throw new NetworkError()
  }
}

/** Uma renovação por vez: dez queries que expiram juntas não podem rodar a
 *  rotação de refresh dez vezes — o backend trataria isso como reúso de token. */
async function refreshSession(): Promise<boolean> {
  if (!refreshInFlight) {
    refreshInFlight = (async () => {
      try {
        const response = await send('/auth/refresh', { method: 'POST' })
        return response.ok
      } catch {
        return false
      }
    })()
  }
  const attempt = refreshInFlight
  try {
    return await attempt
  } finally {
    if (refreshInFlight === attempt) refreshInFlight = null
  }
}

function parseErrorCode(value: unknown): ApiErrorCode | null {
  return typeof value === 'string' ? (value as ApiErrorCode) : null
}

async function toApiError(response: Response): Promise<ApiError> {
  let code: ApiErrorCode | null = null
  let invalidFields: string[] = []
  try {
    const payload: unknown = await response.json()
    if (payload && typeof payload === 'object' && 'error' in payload) {
      const detail = (payload as { error: unknown }).error
      if (detail && typeof detail === 'object') {
        code = parseErrorCode((detail as { code?: unknown }).code)
        const fields = (detail as { fields?: unknown }).fields
        if (fields && typeof fields === 'object') invalidFields = Object.keys(fields)
      }
    }
  } catch {
    // Corpo ausente ou ilegível: o status já basta para escolher a mensagem.
  }
  return new ApiError(response.status, code, invalidFields)
}

async function readBody<T>(response: Response): Promise<T> {
  if (response.status === 204) return undefined as T
  const text = await response.text()
  if (!text) return undefined as T
  return JSON.parse(text) as T
}

export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  let response = await send(path, options)

  if (response.status === 401 && !SKIP_REFRESH.has(path)) {
    const renewed = await refreshSession()
    if (renewed) response = await send(path, options)
  }

  if (!response.ok) throw await toApiError(response)
  return readBody<T>(response)
}
