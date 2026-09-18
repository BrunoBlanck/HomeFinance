import { useId } from 'react'
import styles from './Swatch.module.css'

/** O papel de uma marca de gráfico — a posição na rampa `--chart-*`, nunca uma
 *  cor escolhida na tela.
 *
 *  `'1'`…`'4'` são a rampa ordinal; `'pendente'` é o balde "Sem categoria" e
 *  `'outras'` é a fatia dobrada da rosca. Duas SÉRIES que se comparam lado a
 *  lado usam `'1'` e `'3'` — nunca 1 e 2 (docs/DESIGN.md, E7 (b)). */
export type PapelDaAmostra = '1' | '2' | '3' | '4' | 'pendente' | 'outras'

/** A amostra de 12 px da legenda **e** da tabela (docs/DESIGN.md, E6a (c) e
 *  E7 (c)).
 *
 *  Um componente e um mapa só, servindo os dois gráficos do produto: a amostra
 *  ao lado do nome na legenda é a mesma que aparece na linha do grupo na
 *  tabela, e é isso que liga o desenho aos números. Cantos retos de propósito —
 *  é uma amostra impressa, não um botão.
 *
 *  Decorativa por construção: o nome está escrito ao lado, em todas as
 *  aparições. A cor nunca é o portador único. */
export function Swatch({ papel }: { papel: PapelDaAmostra }) {
  // Um id por instância: a mesma hachura aparece na legenda e em várias linhas
  // da tabela, e dois `<pattern id>` iguais no documento fazem o navegador
  // resolver o primeiro para todos — que é o bug clássico de SVG repetido.
  const id = useId().replace(/[^a-zA-Z0-9-]/g, '')
  const hachuraId = `hf-swatch-${id}`

  return (
    // Decorativa: o nome da categoria ou da série está escrito ao lado.
    <svg
      className={styles.swatch}
      width="12"
      height="12"
      viewBox="0 0 12 12"
      aria-hidden="true"
      focusable="false"
    >
      {papel === 'outras' ? (
        <defs>
          <pattern
            id={hachuraId}
            width="6"
            height="6"
            patternUnits="userSpaceOnUse"
            patternTransform="rotate(45)"
          >
            <line
              x1="0"
              y1="0"
              x2="0"
              y2="6"
              className={styles.hachura}
              vectorEffect="non-scaling-stroke"
            />
          </pattern>
        </defs>
      ) : null}
      <rect
        className={styles.swatchRect}
        width="12"
        height="12"
        data-fill={papel}
        {...(papel === 'outras' ? { fill: `url(#${hachuraId})` } : {})}
      />
    </svg>
  )
}
