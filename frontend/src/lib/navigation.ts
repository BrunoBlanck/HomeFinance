/** Mensagem carregada de uma tela para a outra pelo state do roteador.
 *  E-mail e avisos viajam aqui, NUNCA em query string: query string vaza em
 *  log de servidor, histórico do navegador e cabeçalho Referer. */
export type Flash = {
  tone: 'info' | 'success' | 'warning' | 'error'
  title?: string | undefined
  message?: string | undefined
  detail?: string | undefined
}

declare module '@tanstack/history' {
  interface HistoryState {
    email?: string
    flash?: Flash
  }
}
