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

/** `'credit' | 'debit'` — o recorte de CONTAS do relatório por categoria
 *  (ADR-032): `credit` são as contas de cartão de crédito, `debit` todas as
 *  demais. Ausente = todas as contas.
 *
 *  Derivado do eco `accountGroup` da resposta (que é o mesmo enum do parâmetro
 *  de query), sem o `null` do "todas": o `null` é o jeito da RESPOSTA dizer
 *  "sem recorte"; no pedido, "sem recorte" é a ausência da chave, que é a URL
 *  canônica (o servidor aceita `accountGroup=` vazio com o mesmo significado,
 *  mas o cliente não tem por que escrever uma chave que não recorta nada).
 *  Um valor novo no contrato aparece aqui sozinho
 *  e quebra a compilação do `Record` que o traduz da palavra da URL. */
export type AccountGroup = NonNullable<CategoryReport['accountGroup']>

/** Participação em pontos-base (`0..10000`, 1 bp = 0,01 %), apurada no
 *  **servidor** pelo método do maior resto (ADR-027c). O cliente só formata:
 *  é isso que faz as linhas da tabela somarem `100,00%` sem nota de
 *  arredondamento. */
export type BasisPoints = ApiSchemas['BasisPoints']

// ------------------------------------ painel: resumo do mês (spec 0008)

/** O que `GET /dashboard?month=` devolve: os três números do mês, as contagens
 *  que os explicam e os dois contadores de estado vazio.
 *
 *  Dois campos merecem nota, e as duas notas são sobre **não recalcular nada
 *  aqui**:
 *
 *  - `investmentNetCents` é o líquido **com sinal** (aportes − resgates), e é
 *    um dos pouquíssimos campos de dinheiro do contrato sem `minimum: 0`. Ele
 *    chega pronto: subtrair no cliente criaria uma segunda fonte para o mesmo
 *    número (ADR-003, e a lição de 18/09/2026 em `LICOES-FRONTEND.md`).
 *  - `creditCardAccountCount` e `investmentCategoryCount` existem para a tela
 *    distinguir "não tem cartão cadastrado" de "tem cartão e não gastou" **sem
 *    buscar `/accounts` nem `/categories`** — a faixa se monta com um pedido de
 *    rede e só um (spec 0008, aceite 18). */
export type DashboardSummary = ApiSchemas['DashboardSummary']

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

/** `'income' | 'expense' | 'transfer' | 'investment'` — o **agrupamento** do
 *  filtro de tipo de `GET /transactions` (spec 0004 §12, emenda E2d).
 *
 *  Não é `TransactionKind`: `investment` não é um `kind` (ADR-029b), é o
 *  recorte das linhas cuja categoria tem natureza `investment`/`redemption`. A
 *  allowlist é fechada e **sensível a caixa**, e a ausência da chave é "Tudo" —
 *  o valor `all` não existe e responde 400. A tradução da palavra da URL
 *  (`?tipo=despesas`) para este valor é da tela, e mora num lugar só
 *  (`grupoDeTipo`, em `features/transactions/api/transactions.ts`). */
export type TransactionKindGroup = ApiSchemas['TransactionKindGroup']

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

/** `'c6' | 'inter' | 'nubank' | 'other'` — allowlist fechada, nada de texto livre. */
export type Institution = ApiSchemas['Institution']

// -------------------------------------------- menu IA: exportar (spec 0010)

/** O que `GET /ai/export-prompt` devolve: o texto pronto em Markdown, o eco da
 *  janela, o instante da geração e as contagens.
 *
 *  **A tela não remonta o prompt.** O contrato entrega o texto e nada além
 *  dele — nenhum lançamento, nenhum valor solto, nenhuma estrutura para o
 *  cliente concatenar. É o que faz "Copiar" e "Baixar" entregarem exatamente o
 *  que está na tela, byte a byte (aceite 49 da spec 0010): existe **um** texto,
 *  e ele veio do servidor. */
export type AiExportPrompt = ApiSchemas['AiExportPrompt']

/** As contagens ao lado do prompt. Elas existem para a pessoa ver o TAMANHO do
 *  que está prestes a colar em outro lugar **antes** de colar — não são enfeite
 *  de painel, são parte do consentimento informado (spec 0010 §8.1). */
export type AiExportStats = ApiSchemas['AiExportStats']

// ---------------------------------- menu IA: importar (spec 0010 §4, E9b)

/** O corpo que o frontend manda nas **duas** rotas (`preview` e `confirm`):
 *  o JSON da IA como veio, a janela de trabalho e, só no confirm, os `ref`
 *  das categorias desmarcadas. Os corpos são idênticos de propósito — é o que
 *  torna literal a promessa de que o confirm revalida tudo do zero. */
export type KeywordImportEnvelope = ApiSchemas['KeywordImportEnvelope']

/** O JSON da IA. A tela só garante que é JSON e objeto; quem valida campo a
 *  campo é o servidor (`additionalProperties: false` em todo nível). */
export type KeywordImportPayload = ApiSchemas['KeywordImportPayload']

/** O mesmo relatório nas duas rotas: na prévia, o que **entraria**; no
 *  confirm, o que **de fato entrou**. A tela desenha um bloco só. */
export type KeywordImportReport = ApiSchemas['KeywordImportReport']
export type KeywordImportTotals = ApiSchemas['KeywordImportTotals']
export type KeywordImportNewCategory = ApiSchemas['KeywordImportNewCategory']
export type KeywordImportItem = ApiSchemas['KeywordImportItem']
export type KeywordImportItemType = ApiSchemas['KeywordImportItemType']
export type KeywordImportSkippedKeyword = ApiSchemas['KeywordImportSkippedKeyword']
export type KeywordImportRejectedKeyword = ApiSchemas['KeywordImportRejectedKeyword']

/** Os conjuntos **fechados** de motivo (§§4.2–4.3 da spec 0010). Viram FRASE
 *  na tela por `Record` exaustivo: um motivo novo no contrato é erro de
 *  compilação em quem escolhe a frase, nunca `keyword_taken` cru na tela. */
export type KeywordImportRejectReason = ApiSchemas['KeywordImportRejectReason']
export type KeywordImportSkipReason = ApiSchemas['KeywordImportSkipReason']
export type NewCategoryOutcome = ApiSchemas['NewCategoryOutcome']

/** `grupo > folha` normalizado — o `ref` estável entre prévia e confirmação.
 *  O cliente só devolve, em `skipNewCategories`, o que o servidor mandou. */
export type CategoryPathRef = ApiSchemas['CategoryPathRef']
