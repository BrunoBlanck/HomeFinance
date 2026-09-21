import type { ImportResult } from '@/api/types'

/** Mensagem carregada de uma tela para a outra pelo state do roteador.
 *  E-mail e avisos viajam aqui, NUNCA em query string: query string vaza em
 *  log de servidor, histórico do navegador e cabeçalho Referer. */
export type Flash = {
  tone: 'info' | 'success' | 'warning' | 'error'
  title?: string | undefined
  message?: string | undefined
  detail?: string | undefined
}

/** Um pagamento de fatura que a pessoa deixou de fora na importação.
 *
 *  Viaja no state do roteador de `/importar/{id}/resultado` para
 *  `/lancamentos`. É assim, e não por import direto, porque **feature não
 *  importa de feature** (`AGENTS.md`): a tela de resultado navega e entrega os
 *  dados; a de lançamentos decide o que fazer com eles.
 *
 *  Não vai em query string: valor e data de um lançamento vazariam em log de
 *  servidor, histórico e cabeçalho `Referer` (seção 6 de `docs/SEGURANCA.md`). */
export type TransferenciaPendente = {
  /** Em CENTAVOS, não-negativo (ADR-003). */
  valorCents: number
  /** Data civil `AAAA-MM-DD`. */
  data: string
  contaOrigemId: string
  contaOrigemNome: string
}

/** O que o passo 3 da importação precisa saber e que a API não devolve.
 *
 *  Os números do lote o passo 3 busca sozinho (`GET /imports/{id}` traz o
 *  `outcome` e sobrevive a um F5). O que **só existe no momento da confirmação**
 *  viaja aqui: quantas linhas entraram sem categoria, e qual pagamento de fatura
 *  a pessoa deixou de fora — as linhas de *staging* são apagadas no commit, e
 *  depois disso não há como recalcular nenhum dos dois.
 *
 *  Sem isto, o aviso mais importante da tela ("a dívida do cartão não volta a
 *  zero enquanto o pagamento não for registrado") simplesmente não apareceria. */
export type ResumoDaImportacao = {
  resultado: ImportResult
  /** Mês (`AAAA-MM`) em que os lançamentos aparecem — competência, não caixa. */
  mes: string
  /** Quantas linhas entraram sem categoria. */
  semCategoria: number
  faturaIgnorada?: TransferenciaPendente
}

declare module '@tanstack/history' {
  interface HistoryState {
    email?: string
    flash?: Flash
    novaTransferencia?: TransferenciaPendente
    resumoDaImportacao?: ResumoDaImportacao
  }
}
