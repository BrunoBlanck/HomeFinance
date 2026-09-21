/** Cliente HTTP único do HomeFinance.
 *
 *  Sessão vive em cookies `HttpOnly` emitidos pelo backend — o frontend nunca lê
 *  nem grava token, e por isso toda chamada precisa de `credentials: 'include'`.
 *  Em desenvolvimento o Vite faz proxy de `/api`, então a origem é a mesma e o
 *  `SameSite=Strict` dos cookies continua valendo.
 *
 *  Nenhuma PROSA do servidor é repassada à interface: guardamos status, código, o
 *  mapa estruturado `error.fields` (motivos como `expected_credit_card`, nunca
 *  frases prontas) e os NOMES dos campos inválidos. O texto que o usuário lê sai
 *  sempre de `src/lib/errors.ts` ou da própria tela, escolhido a partir desses
 *  motivos. */

import type { ApiErrorCode } from './types'

const BASE_URL = '/api/v1'

/** Reexportado por conveniência de quem já importava daqui. A definição vem do
 *  contrato OpenAPI (ADR-015), não desta camada. */
export type { ApiErrorCode }

export class ApiError extends Error {
  readonly status: number
  readonly code: ApiErrorCode | null
  /** Só os NOMES dos campos inválidos — é o que os formulários usam para marcar
   *  campo a campo. Continua sendo o 3º parâmetro do construtor: `fields` foi
   *  acrescentado à parte, de forma aditiva, para não mexer nestes consumidores. */
  readonly invalidFields: readonly string[]
  /** O mapa completo `campo → motivo` que o servidor mandou em `error.fields`,
   *  já filtrado para os pares cujo valor é `string`. São dados ESTRUTURADOS que
   *  o servidor já entregou (ex.: `{ reason: 'expected_credit_card' }`), não
   *  prosa para exibir: a tela lê o motivo e escolhe a própria frase em pt-BR.
   *  Renderizar sempre como texto — o React escapa. */
  readonly fields: Readonly<Record<string, string>>

  constructor(
    status: number,
    code: ApiErrorCode | null,
    invalidFields: readonly string[] = [],
    fields: Readonly<Record<string, string>> = {},
  ) {
    super(`Resposta ${status} da API`)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.invalidFields = invalidFields
    this.fields = fields
  }
}

export class NetworkError extends Error {
  constructor() {
    super('Falha de rede ao falar com a API')
    this.name = 'NetworkError'
  }
}

/** A resposta chegou 200, mas o **eco** dos parâmetros não é o do pedido: o
 *  servidor devolveu um recorte diferente daquele que a tela pediu (e que o
 *  rótulo da tela já está afirmando).
 *
 *  **Quando se confere o eco — só quando as TRÊS valem:**
 *
 *  1. a resposta **ecoa** o parâmetro (ele é `required` no schema da resposta,
 *     não apenas aceito na query);
 *  2. o **rótulo visível** da tela afirma aquele recorte (`<h1>`, subtítulo,
 *     `caption` da tabela);
 *  3. o número exibido **muda de significado** se o recorte for outro.
 *
 *  Onde as três valem, divergência é **erro da query** — nunca um estado de
 *  interface novo, nunca um aviso ao lado do número. O caminho de erro que a
 *  tela já tem substitui o quadro inteiro, e nenhum número sobrevive sob um
 *  rótulo que não é dele: é exatamente o defeito que o ADR-030 nomeia —
 *  tecnicamente verdadeiro, visualmente mentiroso. Onde alguma das três falha,
 *  **não se confere**: conferência sem rótulo a proteger é código morto.
 *
 *  Não carrega mensagem própria: `messageForError` o trata como erro
 *  desconhecido e cai na frase genérica ("Algo falhou do nosso lado. Tente de
 *  novo em instantes."), que é a verdade aqui — não há dado que o usuário possa
 *  corrigir, e inventar copy nova seria uma segunda superfície de erro para a
 *  mesma classe de problema. */
export class EchoMismatchError extends Error {
  constructor() {
    super('A resposta da API não ecoou os parâmetros do pedido')
    this.name = 'EchoMismatchError'
  }
}

export type RequestOptions = {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE'
  /** Objeto qualquer vira JSON. `FormData` vai como está — é assim que o envio
   *  de arquivo do importador de extratos trafega. */
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
  if (options.body instanceof FormData) {
    // O `Content-Type` de um multipart carrega o `boundary`, e só o navegador
    // sabe qual é — ele o gera na hora de serializar o FormData, e SÓ se o
    // header ainda não estiver preenchido. Definir "application/json" (ou até
    // "multipart/form-data" sem boundary) aqui faz o servidor receber um corpo
    // que não consegue separar em partes: 400 sem nenhuma pista do motivo.
    init.body = options.body
  } else if (options.body !== undefined) {
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
  let fields: Record<string, string> = {}
  let invalidFields: string[] = []
  try {
    const payload: unknown = await response.json()
    if (payload && typeof payload === 'object' && 'error' in payload) {
      const detail = (payload as { error: unknown }).error
      if (detail && typeof detail === 'object') {
        code = parseErrorCode((detail as { code?: unknown }).code)
        const bruto = (detail as { fields?: unknown }).fields
        if (bruto && typeof bruto === 'object') {
          // `invalidFields` guarda TODAS as chaves (o nome do campo é o que os
          // formulários marcam); `fields` guarda só os valores string, que é o
          // que a tela consegue interpretar como motivo.
          invalidFields = Object.keys(bruto)
          fields = apenasStrings(bruto as Record<string, unknown>)
        }
      }
    }
  } catch {
    // Corpo ausente ou ilegível: o status já basta para escolher a mensagem.
  }
  return new ApiError(response.status, code, invalidFields, fields)
}

/** Só os pares cujo valor é `string`. O servidor manda `fields` como
 *  `campo → motivo`; um valor que não é texto seria payload inesperado e a tela
 *  não teria o que fazer com ele. */
function apenasStrings(origem: Record<string, unknown>): Record<string, string> {
  const saida: Record<string, string> = {}
  for (const [chave, valor] of Object.entries(origem)) {
    if (typeof valor === 'string') saida[chave] = valor
  }
  return saida
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
