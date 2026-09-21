import { apiRequest, EchoMismatchError } from '@/api/client'
import type {
  AutoCategorizeRequest,
  AutoCategorizeResult,
  TransferDetectRequest,
  TransferDetectResult,
} from '@/api/types'

/** As duas rotas que a seção Reprocessar de `/ia` orquestra (spec 0010 §5).
 *  **Nenhuma rota nova**: são `POST /transfers/detect` e
 *  `POST /transactions/auto-categorize`, que já existem, já têm prévia,
 *  transação, auditoria, rate limit próprio e 409 de conflito. Nenhum tipo de
 *  payload é escrito aqui: todos derivam do contrato OpenAPI (ADR-015).
 *
 *  Declaradas nesta feature — e não importadas de `transfers/` e
 *  `transactions/` — porque import entre features é proibido no projeto. São
 *  as mesmas quatro linhas de lá, com uma diferença que aqui importa: a
 *  conferência do **eco do mês**.
 *
 *  **Por que o eco é conferido** (as três condições do `EchoMismatchError`,
 *  ADR-030): o contrato põe `month` em `required` na resposta das duas rotas;
 *  a tela AFIRMA o mês em texto visível (a linha da tabela diz "Julho de 2026 ·
 *  1 par", o `<output>` diz "Julho: 12 categorizados."); e a contagem muda de
 *  significado se o mês for outro — um "1 par" de agosto na linha de julho é o
 *  defeito que o ADR-030 nomeia. Na execução real isso vale ainda mais: o
 *  número que volta é o de linhas **gravadas**, e atribuí-lo ao mês errado
 *  seria um registro falso do que foi feito.
 *
 *  São `POST` mesmo na prévia e moram fora do cache de queries de propósito:
 *  cada chamada gasta cota do rate limit por casa (60/h, estouro 6 — ADR-036(f)),
 *  e uma `useQuery` refazendo a prévia no foco da janela queimaria a cota em
 *  silêncio. */

export type { AutoCategorizeResult, TransferDetectResult }

/** `POST /transfers/detect` — `dryRun: true` mede, `dryRun: false` converte
 *  os pares `income`/`expense` em `transfer_out`/`transfer_in` (e zera o
 *  `categoryId` das duas pernas, ADR-028). */
export async function detectarTransferencias(
  body: TransferDetectRequest,
): Promise<TransferDetectResult> {
  const resultado = await apiRequest<TransferDetectResult>('/transfers/detect', {
    method: 'POST',
    body,
  })
  if (resultado.month !== body.month) throw new EchoMismatchError()
  return resultado
}

/** `POST /transactions/auto-categorize` — `dryRun: true` mede, `dryRun: false`
 *  grava categoria nos lançamentos que estão **sem** categoria. Nunca toca em
 *  `transfer_in`/`transfer_out`, e é por isso que ela roda DEPOIS da detecção. */
export async function categorizarAutomaticamente(
  body: AutoCategorizeRequest,
): Promise<AutoCategorizeResult> {
  const resultado = await apiRequest<AutoCategorizeResult>('/transactions/auto-categorize', {
    method: 'POST',
    body,
  })
  if (resultado.month !== body.month) throw new EchoMismatchError()
  return resultado
}
