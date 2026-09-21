import { ApiError, NetworkError } from '@/api/client'
import type { ApiErrorCode } from '@/api/types'

/** Os SEIS códigos de erro da importação (spec 0004 §5.4).
 *
 *  Cada um existe porque leva a uma AÇÃO DIFERENTE da tela — é essa
 *  correspondência, e nada mais, que justifica seis códigos em vez de um:
 *
 *  - `IMPORT_PASSWORD_REQUIRED` → abre o campo de senha;
 *  - `IMPORT_PASSWORD_INVALID`  → mantém o campo e pede de novo, sem perder o
 *    arquivo já escolhido;
 *  - `IMPORT_FORMAT_UNKNOWN`    → lista os formatos suportados;
 *  - `IMPORT_FORMAT_AMBIGUOUS`  → mostra o seletor com os candidatos, que vêm
 *    em `fields.format`;
 *  - `IMPORT_TARGET_MISMATCH`   → manda trocar a CONTA de destino, não o
 *    arquivo;
 *  - `IMPORT_FILE_REJECTED`     → explica qual limite estourou.
 *
 *  Colapsá-los em `VALIDATION_FAILED` obrigaria a interface a interpretar texto
 *  em português, que é exatamente o que a D4 da spec 0003 recusou. */
const IMPORT_MESSAGES: Partial<Record<ApiErrorCode, string>> = {
  IMPORT_PASSWORD_REQUIRED:
    'Este arquivo está protegido por senha. Informe a senha para continuar.',
  IMPORT_PASSWORD_INVALID: 'A senha não abriu este arquivo. Confira e tente de novo.',
  IMPORT_FORMAT_UNKNOWN: 'Ainda não sei ler este formato de arquivo.',
  IMPORT_FORMAT_AMBIGUOUS: 'Mais de um formato reconheceu este arquivo. Escolha qual usar.',
  IMPORT_TARGET_MISMATCH:
    'Este arquivo não combina com a conta escolhida. Troque a conta de destino.',
  IMPORT_FILE_REJECTED: 'Não consegui aceitar este arquivo. Tente importar um período menor.',
}

/** `true` quando o erro é da importação e a tela tem uma ação específica para
 *  ele — útil para decidir entre tratar na própria etapa ou cair no genérico. */
export function isImportError(error: unknown): boolean {
  return error instanceof ApiError && error.code !== null && error.code in IMPORT_MESSAGES
}

/** Códigos que levam a uma ação própria da tela fora da importação — e por
 *  isso têm frase própria, escolhida pelo CÓDIGO (nunca pelo texto do
 *  servidor, D4 da spec 0003):
 *
 *  - `CONFLICT` (409) → o estado mudou entre a leitura e a gravação de uma
 *    operação em lote (`POST /transfers/detect`, ADR-028d): nada foi gravado,
 *    e a saída é pedir a prévia de novo. */
const MENSAGEM_POR_CODIGO: Partial<Record<ApiErrorCode, string>> = {
  CONFLICT: 'Os dados mudaram enquanto a operação rodava. Confira a prévia de novo.',
}

/** `true` quando o 422 aponta `month`: o mês tem mais candidatos do que uma
 *  execução em lote aceita (10.000 em `POST /investments/detect`, spec 0006
 *  §3.3) e **nada foi alterado**.
 *
 *  A tela que recebe isto NÃO oferece "tentar de novo": repetir o mesmo pedido
 *  daria o mesmo 422. O que muda o resultado é reduzir o trabalho — daí a frase
 *  própria, escolhida pelo campo e nunca pelo texto do servidor (D4 da spec
 *  0003). */
export function isLoteGrandeDemais(error: unknown): boolean {
  return error instanceof ApiError && error.status === 422 && typeof error.fields.month === 'string'
}

/** `true` quando a operação em lote foi desfeita porque o mês mudou no meio
 *  (409 `CONFLICT`). A tela que recebe isto tem uma ação diferente do erro
 *  genérico: refazer a prévia, não "tentar de novo". */
export function isConflict(error: unknown): boolean {
  return error instanceof ApiError && error.code === 'CONFLICT'
}

/** Mapa único de mensagens de erro. Toda tela começa por aqui e só sobrescreve
 *  o caso que precisa de redação própria (ex.: 401 do login). */
export function messageForError(error: unknown): string {
  if (error instanceof NetworkError) return 'Sem conexão com o servidor. Verifique sua internet.'

  if (error instanceof ApiError) {
    // Os códigos da importação vêm ANTES da faixa de status: eles chegam como
    // 422, e o texto genérico de 422 ("confira os dados") não diz nada sobre o
    // que fazer com um ZIP protegido por senha.
    if (error.code !== null) {
      const daImportacao = IMPORT_MESSAGES[error.code]
      if (daImportacao !== undefined) return daImportacao
      const porCodigo = MENSAGEM_POR_CODIGO[error.code]
      if (porCodigo !== undefined) return porCodigo
    }
    // O 422 que aponta `categoryId` tem ação própria (spec 0005 §13): a
    // categoria escolhida não recebe lançamento, e "confira os dados" não
    // diria qual dado nem o que fazer com ele.
    if (isCategoriaRecusada(error)) return MSG_CATEGORIA_RECUSADA
    if (error.status === 400 || error.status === 422) {
      return 'Confira os dados informados e tente de novo.'
    }
    if (error.status === 401) return 'Sua sessão expirou. Entre de novo.'
    if (error.status === 403) return 'Você não tem acesso a este conteúdo.'
    if (error.status === 404) return 'Não encontramos esta página.'
    if (error.status === 429) return 'Muitas tentativas. Aguarde alguns minutos e tente de novo.'
  }

  return 'Algo falhou do nosso lado. Tente de novo em instantes.'
}

/** `true` quando a sessão acabou e a tela deve mandar o usuário para `/entrar`. */
export function isUnauthenticated(error: unknown): boolean {
  return error instanceof ApiError && error.status === 401
}

/** O que o 409 `KEYWORD_TAKEN` traz (spec 0005 §4.1.2): a palavra recusada e,
 *  quando o servidor conseguiu apontar, quem já a tem. `ownerId` é sempre um
 *  recurso DA CASA do token — a tela o troca pelo nome via cache e nunca
 *  mostra o id cru. */
export type KeywordTaken = {
  keyword: string
  ownerId: string | undefined
}

/** `true` quando a gravação de categoria/conta foi recusada porque a
 *  palavra-chave já pertence a OUTRA categoria (ou conta) desta casa. É um
 *  código próprio, e não um 400 de forma, porque a ação da tela é diferente:
 *  marcar a ficha e dizer "já está em X". */
export function isKeywordTaken(error: unknown): error is ApiError {
  return (
    error instanceof ApiError &&
    error.code === 'KEYWORD_TAKEN' &&
    typeof error.fields.keyword === 'string'
  )
}

/** Os campos do 409, ou `null` quando o erro é outro. */
export function keywordTakenOf(error: unknown): KeywordTaken | null {
  if (!isKeywordTaken(error)) return null
  return { keyword: error.fields.keyword ?? '', ownerId: error.fields.ownerId }
}

/** A frase do 422 que recusa a CATEGORIA escolhida para um lançamento
 *  (spec 0005 §13). Ela é NOSSA — o servidor manda uma frase parecida em
 *  `error.fields.categoryId`, e ecoá-la seria a interface exibindo prosa do
 *  servidor, que é o que a D4 da spec 0003 recusa. */
export const MSG_CATEGORIA_RECUSADA =
  'Esta categoria não pode receber este lançamento. Escolha outra na lista.'

/** `true` quando a escrita foi recusada com 422 apontando `categoryId` — a
 *  categoria existe nesta casa e o corpo está bem formado; o que não vale é
 *  pendurar o lançamento NELA (spec 0005 §13).
 *
 *  Vale para os três caminhos de escrita: `PATCH /transactions/{id}` (o atalho
 *  de `/lancamentos`), as decisões do `POST /imports/{id}/confirm` e o
 *  `defaultCategoryId` do mesmo confirm.
 *
 *  **Por que um só tratamento para as três causas do campo.** O contrato manda
 *  `fields.categoryId` também para categoria arquivada e para natureza
 *  trocada, e não existe código que separe as três — separar exigiria ler a
 *  frase em português do servidor, que é justamente o que não se faz aqui. E
 *  não precisa: a AÇÃO da tela é a mesma nas três (recarregar as opções e
 *  escolher outra categoria). Por isso a frase NÃO nomeia a causa: nomear uma
 *  das três a faria mentir nas outras duas — numa corrida de arquivamento,
 *  "este grupo tem subcategorias" seria falso, e a pessoa procuraria uma
 *  subcategoria que não existe.
 *
 *  Devolve `boolean`, e não `error is ApiError` como o `isKeywordTaken`: quem
 *  chama não lê campo nenhum do erro depois, e o predicado estreitaria o ramo
 *  negativo para `never` dentro do próprio `messageForError`. */
export function isCategoriaRecusada(error: unknown): boolean {
  return (
    error instanceof ApiError && error.status === 422 && typeof error.fields.categoryId === 'string'
  )
}
