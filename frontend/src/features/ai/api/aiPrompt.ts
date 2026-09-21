import { queryOptions } from '@tanstack/react-query'
import { apiRequest, EchoMismatchError } from '@/api/client'
import type { AiExportPrompt, AiExportStats } from '@/api/types'
import type { JanelaDeTrabalho } from '../janela'

/** `GET /ai/export-prompt` (spec 0010 §3). Nenhum tipo de payload é escrito
 *  aqui: todos derivam do contrato OpenAPI (ADR-015). */

export type { AiExportPrompt, AiExportStats }

/** Chave PRÓPRIA, fora do prefixo `transactions` — ao contrário do relatório e
 *  do painel (ADR-027).
 *
 *  O motivo é a natureza do dado: o prompt é um **retrato** de um período
 *  fechado, carimbado com `generatedAt`, feito para ser copiado para fora e
 *  colado numa IA. Ele não é um número que a tela afirma estar certo agora.
 *  Pendurá-lo em `transactions` faria categorizar um lançamento em outra aba
 *  refazer, em silêncio, o texto de 540 linhas que a pessoa está no meio de
 *  copiar — trocar o conteúdo do clipboard pelas costas de quem copia é
 *  exatamente o que esta tela não pode fazer. Quem quer o retrato novo troca a
 *  janela ou recarrega. */
export const aiPromptQueryKey = (fromMonth: string, toMonth: string) =>
  ['ai', 'prompt', fromMonth, toMonth] as const

/** O prompt da janela de trabalho.
 *
 *  `staleTime: Infinity` e `refetchOnWindowFocus: false` pelo mesmo motivo da
 *  chave: **voltar para a aba não pode reescrever o texto**. O fluxo desta tela
 *  é sair do app (colar numa IA de fora) e voltar — é a única tela do produto
 *  em que perder o foco da janela é o comportamento ESPERADO, e um refetch no
 *  retorno trocaria o texto que a pessoa acabou de copiar por outro com outro
 *  `generatedAt`.
 *
 *  `retry: false` como no resto do app: 400 (janela longa demais), 401 e um eco
 *  divergente não melhoram na segunda tentativa, e o erro tem ação própria na
 *  tela.
 *
 *  **O eco é conferido**, e as três condições do `EchoMismatchError` valem
 *  aqui: o contrato põe `fromMonth`/`toMonth` em `required` na resposta; a tela
 *  AFIRMA a janela em texto visível (o aviso de carregamento, o `<summary>` e o
 *  nome do arquivo baixado); e o conteúdo muda de significado se a janela for
 *  outra — um `.md` chamado `…2026-07-a-2026-09.md` com o texto de outro
 *  trimestre é o defeito que o ADR-030 nomeia. */
export function aiPromptQueryOptions(janela: JanelaDeTrabalho) {
  const { fromMonth, toMonth } = janela
  return queryOptions({
    queryKey: aiPromptQueryKey(fromMonth, toMonth),
    queryFn: async ({ signal }) => {
      // `URLSearchParams`, nunca concatenação: os dois meses vêm da URL, que a
      // pessoa edita. O portão de `app/search.ts` e a derivação de `janela.ts`
      // já os produziram — isto é a segunda camada, e custa nada.
      const busca = new URLSearchParams({ fromMonth, toMonth })
      const dados = await apiRequest<AiExportPrompt>(`/ai/export-prompt?${busca.toString()}`, {
        signal,
      })
      if (dados.fromMonth !== fromMonth || dados.toMonth !== toMonth) {
        throw new EchoMismatchError()
      }
      return dados
    },
    staleTime: Number.POSITIVE_INFINITY,
    refetchOnWindowFocus: false,
    retry: false,
  })
}
