/** O que as seções de `/ia` compartilham entre si — ids e frases que precisam
 *  ser IGUAIS em mais de uma subárvore, e por isso não moram em nenhuma delas.
 *
 *  **O id do `<h2>` da seção 3.** O relatório final da seção 2 oferece "Ir
 *  para Reprocessar", que leva o FOCO ao título da seção 3 — e foco em elemento
 *  com `tabIndex={-1}` é o que faz o navegador rolar até lá sem `scrollTo`,
 *  sem `scroll-behavior: smooth` e sem animação para quem pediu menos
 *  movimento. O id é constante (e não `useId`) porque quem o lê está noutra
 *  subárvore, sem ref para o título. */
export const ID_DO_TITULO_REPROCESSAR = 'ia-secao-reprocessar'

/** A frase da REGRA DE FRESCOR, uma só para as seções 2 e 3 (docs/DESIGN.md,
 *  spec 0010 (c)): trocar o mês da casca ou o tamanho da janela descarta o
 *  que foi medido no período anterior — a prévia do import, a prévia e o
 *  resultado do reprocessamento — e a seção diz por quê. Duas redações
 *  seriam duas regras aos olhos de quem lê a tela. */
export const FRASE_JANELA_MUDOU =
  'A janela mudou — confira de novo para medir o impacto no novo período.'
