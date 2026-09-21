import { apiRequest } from '@/api/client'
import type { KeywordImportEnvelope, KeywordImportReport } from '@/api/types'

/** `POST /ai/keyword-import/preview` e `/confirm` (spec 0010 §4). Nenhum tipo
 *  de payload é escrito aqui: todos derivam do contrato OpenAPI (ADR-015).
 *
 *  São **mutações**, não queries, mesmo a prévia — e por dois motivos que não
 *  são estilo. O corpo é o JSON que a pessoa colou, entrada hostil por
 *  definição (§8.2): ele não entra em cache nenhum do React Query, não é
 *  revalidado em foco de janela e não é refeito sozinho. E a prévia é um
 *  RETRATO medido num período: repeti-la em silêncio trocaria o número que a
 *  pessoa está olhando ("87 de 212") por outro, sem clique. Quem quer a
 *  medição nova clica em Conferir. */

export type { KeywordImportEnvelope, KeywordImportReport }

/** Confere o JSON e devolve o relatório do que **entraria**. Nada é gravado —
 *  nem categoria, nem palavra-chave, nem auditoria. */
export function conferirImportacao(envelope: KeywordImportEnvelope) {
  return apiRequest<KeywordImportReport>('/ai/keyword-import/preview', {
    method: 'POST',
    body: envelope,
  })
}

/** Aplica o import numa única transação, **revalidando tudo do zero**: o
 *  servidor não reaproveita nada da prévia, e é por isso que o corpo é o mesmo
 *  da prévia mais `skipNewCategories`. Devolve o que **de fato** entrou. */
export function aplicarImportacao(envelope: KeywordImportEnvelope) {
  return apiRequest<KeywordImportReport>('/ai/keyword-import/confirm', {
    method: 'POST',
    body: envelope,
  })
}
