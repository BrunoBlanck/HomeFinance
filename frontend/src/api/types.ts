/** Apelidos legíveis para os tipos que vêm do contrato OpenAPI — ADR-015.
 *
 *  `schema.gen.ts` é gerado e verboso (`components['schemas']['Me']`). Este
 *  arquivo é a única porta por onde o resto do frontend fala com ele: se um dia
 *  o gerador mudar de formato, o estrago fica contido aqui.
 *
 *  Regra: **nenhum tipo de payload da API é escrito à mão em outro lugar.**
 *  Precisa de um campo novo? Ele nasce em `backend/api/openapi.yaml`, o
 *  `npm run api:gen` o traz para cá, e o `tsc` aponta quem precisa mudar. */

import type { components } from './schema.gen'

export type ApiSchemas = components['schemas']

// ---------------------------------------------------------------- erros

/** Códigos de erro da API. Derivado do enum da spec: um código novo no backend
 *  aparece aqui sozinho, e um código que o backend deixou de emitir vira erro
 *  de compilação em quem ainda o trata. */
export type ApiErrorCode = ApiSchemas['ErrorCode']

// -------------------------------------------------------------- sessão

export type SessionUser = ApiSchemas['UserSummary']
export type SessionHousehold = ApiSchemas['HouseholdSummary']

/** O que `GET /me` e `POST /auth/login` devolvem. */
export type Session = ApiSchemas['Me']

/** Papel do usuário na casa — `'owner' | 'member'`, não `string`. É a diferença
 *  entre o compilador cobrar o `default` de um switch e não cobrar. */
export type HouseholdRole = SessionHousehold['role']

// -------------------------------------------------- autenticação (entrada)

export type RegisterInput = ApiSchemas['RegisterRequest']
export type LoginInput = ApiSchemas['LoginRequest']
export type VerifyEmailInput = ApiSchemas['VerifyEmailRequest']
export type ForgotPasswordInput = ApiSchemas['EmailRequest']
export type ResetPasswordInput = ApiSchemas['ResetPasswordRequest']

/** Reenvio de código. Deliberadamente **mais estrito que a spec**, e o motivo
 *  está no ADR-014: no contrato `registrationToken` é opcional porque o servidor
 *  não recusa o pedido sem ele — ele responde o mesmo 202 e simplesmente não
 *  emite nada, para manter uma única forma de resposta neste endpoint.
 *
 *  Para o cliente, porém, chamar sem token é sempre um bug: gasta uma
 *  requisição, o usuário fica esperando um e-mail que nunca sai, e quem perdeu o
 *  token tem que refazer o cadastro. Exigir aqui transforma esse bug em erro de
 *  compilação sem mentir sobre o contrato — a intersecção preserva o vínculo com
 *  o schema gerado, então um campo novo na spec continua chegando sozinho. */
export type ResendCodeInput = ApiSchemas['ResendCodeRequest'] & { registrationToken: string }

// -------------------------------------------------- autenticação (saída)

/** Resposta 202 de `register` e de `resend-code` — idêntica para e-mail novo, já
 *  cadastrado ou inexistente. É o backend recusando-se a enumerar contas. */
export type VerificationAccepted = ApiSchemas['VerificationRequired']

/** Resposta 202 de `forgot-password`. Corpo diferente de propósito: a
 *  redefinição de senha **não** usa `registrationToken` — o código dela é
 *  amarrado à conta, não a uma tentativa de cadastro. */
export type RecoveryAccepted = ApiSchemas['ForgotPasswordAccepted']

// ---------------------------------------------------- contas (spec 0003)

export type Account = ApiSchemas['Account']
export type AccountList = ApiSchemas['AccountList']
export type CreateAccountInput = ApiSchemas['CreateAccountRequest']
export type UpdateAccountInput = ApiSchemas['UpdateAccountRequest']

/** `'cash' | 'checking' | 'savings' | 'credit_card' | 'other'`, direto do
 *  contrato. Um tipo novo no backend aparece aqui sozinho, e o `switch` que
 *  mapeia o rótulo em português deixa de ser exaustivo — erro de compilação em
 *  vez de conta sem nome de tipo na tela. */
export type AccountKind = ApiSchemas['AccountKind']

// ------------------------------------------------ categorias (spec 0003)

export type Category = ApiSchemas['Category']
export type CategoryTree = ApiSchemas['CategoryTree']
export type CreateCategoryInput = ApiSchemas['CreateCategoryRequest']
export type UpdateCategoryInput = ApiSchemas['UpdateCategoryRequest']
export type CategoryKind = ApiSchemas['CategoryKind']

// -------------------------------------------- palavras-chave (spec 0005)

/** Palavra-chave de categoria ou de conta — `string` no contrato, com as
 *  regras de forma (2–40 runas, allowlist) validadas pelo servidor. O
 *  frontend só as espelha para evitar a viagem óbvia (`lib/keywords.ts`). */
export type Keyword = ApiSchemas['Keyword']

/** Corpo do 409 `KEYWORD_TAKEN`: `fields.keyword` é a palavra recusada e
 *  `fields.ownerId` quem já a tem — sempre um recurso DA CASA do token. A tela
 *  resolve o `ownerId` pelo cache e escreve o nome; nunca mostra o id cru. */
export type KeywordConflict = ApiSchemas['KeywordConflict']

// ---------------------------------------- transferências (spec 0005)

export type TransferList = ApiSchemas['TransferList']
export type TransferItem = ApiSchemas['TransferItem']
export type TransferPair = ApiSchemas['TransferPair']
export type TransferBalance = ApiSchemas['TransferBalance']

// ------------------------ reprocessar transferências (spec 0005 §13, ADR-028)

export type TransferDetectRequest = ApiSchemas['TransferDetectRequest']
export type TransferDetectResult = ApiSchemas['TransferDetectResult']
export type TransferDetectItem = ApiSchemas['TransferDetectItem']
export type TransferDetectUnpairedItem = ApiSchemas['TransferDetectUnpairedItem']

/** `'no_mirror'` — único motivo nesta emenda. O motivo vira FRASE na tela
 *  ("sem a outra perna gravada"); um motivo novo no contrato quebra a
 *  compilação de quem escolhe a frase. */
export type TransferDetectUnpairedReason = ApiSchemas['TransferDetectUnpairedReason']

// ------------------------------------------ investimentos (spec 0006)

export type InvestmentOverview = ApiSchemas['InvestmentOverview']
export type InvestmentTotals = ApiSchemas['InvestmentTotals']
export type InvestmentSeriesPoint = ApiSchemas['InvestmentSeriesPoint']
export type InvestmentItem = ApiSchemas['InvestmentItem']

/** `'contribution' | 'redemption'` — aporte e resgate.
 *
 *  Derivado do `kind` do LANÇAMENTO, nunca da natureza da categoria
 *  (ADR-029d). Vira PALAVRA na tela (`Aporte`/`Resgate`), então um valor novo
 *  no contrato quebra a compilação de quem escolhe a palavra, em vez de
 *  imprimir `contribution` cru numa coluna. */
export type InvestmentFlow = ApiSchemas['InvestmentFlow']

// ------------------------------- detectar investimentos (spec 0006 §3.3)

export type InvestmentDetectRequest = ApiSchemas['InvestmentDetectRequest']
export type InvestmentDetectResult = ApiSchemas['InvestmentDetectResult']
export type InvestmentDetectItem = ApiSchemas['InvestmentDetectItem']
export type InvestmentDetectUnmatchedItem = ApiSchemas['InvestmentDetectUnmatchedItem']
export type InvestmentDetectAlreadyCategorizedItem =
  ApiSchemas['InvestmentDetectAlreadyCategorizedItem']

/** `'below_threshold' | 'ambiguous' | 'other_category'` — o motivo vira FRASE
 *  na tela. `Record` exaustivo do lado de quem escolhe a frase: um motivo novo
 *  no contrato é erro de compilação, não texto cru. */
export type InvestmentDetectUnmatchedReason = ApiSchemas['InvestmentDetectUnmatchedReason']

// -------------------------------------- relatório por categoria (ADR-027)

export type CategoryReport = ApiSchemas['CategoryReport']
export type CategoryReportGroup = ApiSchemas['CategoryReportGroup']
export type CategoryReportChild = ApiSchemas['CategoryReportChild']

/** Participação em pontos-base (`0..10000`, 1 bp = 0,01 %), apurada no
 *  **servidor** pelo método do maior resto (ADR-027c). O cliente só formata:
 *  é isso que faz as linhas da tabela somarem `100,00%` sem nota de
 *  arredondamento. */
export type BasisPoints = ApiSchemas['BasisPoints']

// ------------------------------- categorizar automaticamente (spec 0005)

export type AutoCategorizeRequest = ApiSchemas['AutoCategorizeRequest']
export type AutoCategorizeResult = ApiSchemas['AutoCategorizeResult']
export type AutoCategorizeItem = ApiSchemas['AutoCategorizeItem']
export type AutoCategorizeUnmatchedItem = ApiSchemas['AutoCategorizeUnmatchedItem']

/** `'below_threshold' | 'ambiguous'` — o motivo vira PALAVRA na tela
 *  ("abaixo de 80%", "empate entre categorias"); um motivo novo no contrato
 *  quebra a compilação de quem escolhe a frase. */
export type AutoCategorizeUnmatchedReason = ApiSchemas['AutoCategorizeUnmatchedReason']

// ------------------------------------------------------------ datas

/** Data civil `AAAA-MM-DD` — sem hora e sem fuso.
 *
 *  É `string` no contrato de propósito (ADR-019a): um `Date` do JavaScript é um
 *  INSTANTE, e converter "12/09/2026" em instante obriga a escolher uma hora e
 *  um fuso que ninguém informou. É dessa conversão que nasce o bug de "a data
 *  aparece um dia antes". */
export type CivilDate = ApiSchemas['CivilDate']

// ---------------------------------------------- lançamentos (spec 0004)

export type Transaction = ApiSchemas['Transaction']
export type TransactionList = ApiSchemas['TransactionList']
export type TransactionSummary = ApiSchemas['TransactionSummary']

/** Corpo de `PATCH /transactions/{id}` (spec 0005 §11): **só** `categoryId`,
 *  obrigatório e sem `null` — o atalho troca categoria, não a remove. Campo a
 *  mais é 400; o `PATCH` completo continua na E2b. */
export type UpdateTransactionCategoryInput = ApiSchemas['UpdateTransactionCategoryRequest']

/** `'income' | 'expense' | 'transfer_out' | 'transfer_in'`.
 *
 *  **O sinal do valor vem daqui**, nunca de `amountCents`, que é sempre
 *  não-negativo. É o que permite à tabela escrever `+1.600,00` e `−11,00` sem
 *  depender de cor, e é por isso que este tipo precisa ser o do contrato: um
 *  `kind` novo no backend quebra a compilação de quem decide o sinal, em vez de
 *  cair no `else` e exibir uma despesa como receita. */
export type TransactionKind = ApiSchemas['TransactionKind']

export type TransactionSource = ApiSchemas['TransactionSource']

/** Mês do app, `AAAA-MM` — sempre **competência** (ADR-023c). */
export type YearMonth = ApiSchemas['YearMonth']

// ----------------------------------------------- importação (spec 0004)

export type ImportBatch = ApiSchemas['ImportBatch']
export type ImportPreview = ApiSchemas['ImportPreview']
export type ImportRow = ApiSchemas['ImportRow']
export type ImportRowCounts = ApiSchemas['ImportRowCounts']

/** Os sete status da conciliação. A tela **não** reimplementa a tabela de
 *  defaults da §4.6: ela lê `defaultAction` e `allowedActions` de cada linha.
 *  Este tipo serve para agrupar e para escolher a PALAVRA do grupo — que é
 *  como o app marca estado, já que cor cromática está proibida aqui
 *  (docs/DESIGN.md). */
export type ImportRowStatus = ApiSchemas['ImportRowStatus']

/** `'import' | 'skip' | 'transfer'`. */
export type ImportDecisionAction = ApiSchemas['ImportDecisionAction']

export type ImportRejectReason = ApiSchemas['ImportRejectReason']
export type ImportDecision = ApiSchemas['ImportDecision']
export type ConfirmImportInput = ApiSchemas['ConfirmImportRequest']
export type StatementConfirmation = ApiSchemas['StatementConfirmation']
export type ImportResult = ApiSchemas['ImportResult']
export type ImportDocKind = ApiSchemas['ImportDocKind']

/** `'c6' | 'nubank' | 'other'` — allowlist fechada, nada de texto livre. */
export type Institution = ApiSchemas['Institution']
