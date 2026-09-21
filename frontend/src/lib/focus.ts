import { type RefObject, useEffect, useRef } from 'react'

/** Move o foco para o `<h1>` da tela na ENTRADA da rota — a regra de
 *  acessibilidade de `docs/DESIGN.md` ("navegação de rota move o foco para o
 *  `<h1>` da tela nova", com `tabIndex={-1}` no elemento).
 *
 *  Mora aqui, e não copiado em cada tela, porque o mesmo efeito de três linhas
 *  estava em seis arquivos — e foi numa dessas cópias que ele divergiu: a do
 *  relatório por categoria ganhou uma guarda por `document.activeElement` e
 *  virou a única tela que **não** movia o foco quando se chegava por clique no
 *  menu (o elemento ativo era o próprio `<a>` da navegação, e a guarda barrava
 *  o foco legítimo da chegada). Regra repetida é regra que diverge.
 *
 *  **Por que a dependência vazia basta, e por que não existe guarda aqui.**
 *  A guarda foi escrita sob a premissa de que o roteador remonta a tela a cada
 *  mudança da busca (`?natureza=`, `?mes=`), e de que sem ela a troca roubaria
 *  o foco do controle que a pessoa acabou de usar. A premissa é falsa, e isso
 *  foi medido: o `MatchInner` do TanStack Router só passa uma `key` ao
 *  componente da rota quando há `remountDeps`/`defaultRemountDeps`, que este
 *  projeto não declara — trocar a busca **re-renderiza** a tela, não a remonta.
 *  Na medição, o mesmo nó `<h1>` e o mesmo nó `<select>` sobreviveram à troca
 *  de natureza e à de mês, e o foco ficou onde estava. O efeito de montagem,
 *  portanto, roda uma vez por entrada na rota — que é exatamente o gatilho que
 *  a regra de design descreve.
 *
 *  Devolve a ref para o `<h1>`, que a tela ainda precisa marcar com
 *  `tabIndex={-1}` (o `<h1>` não é focável por natureza, e não pode entrar na
 *  ordem de tabulação). */
export function useFocoNoTitulo(): RefObject<HTMLHeadingElement | null> {
  const titulo = useRef<HTMLHeadingElement>(null)

  useEffect(() => {
    titulo.current?.focus()
  }, [])

  return titulo
}
