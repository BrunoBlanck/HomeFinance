import { Swatch } from '../Swatch/Swatch'
import styles from './BarChart.module.css'

/** Uma série dentro de uma coluna. `papel` é a posição na rampa `--chart-*`,
 *  nunca uma cor. */
export type SerieDaColuna = { papel: '1' | '3'; cents: number }

export type ColunaDoGrafico = {
  key: string
  /** Rótulo do eixo: "set". Decorativo — a tabela tem o mês por extenso. */
  rotulo: string
  /** Uma entrada por série, na ordem fixa das séries. */
  valores: readonly SerieDaColuna[]
  /** `<title>` de cada barra, na mesma ordem. */
  titulos: readonly string[]
  /** Desenha a virada de ano ANTES desta coluna. */
  viradaDeAno?: boolean | undefined
}

type BarChartProps = {
  colunas: readonly ColunaDoGrafico[]
  /** Exatamente duas: a protagonista primeiro, e ela leva `--chart-1`. */
  series: readonly { papel: '1' | '3'; nome: string }[]
  /** O maior valor da série — SELECIONADO (`Math.max` de inteiros), nunca
   *  calculado. O cliente não faz aritmética de dinheiro. */
  maximoCents: number
  legenda: string
  loading?: boolean | undefined
}

// Unidades do viewBox `0 0 480 240`. Doze colunas de 40; dentro de cada uma,
// duas barras de 13 com 3 de vão, centradas (sobra 5,5 de cada lado). O vão
// entre grupos é 11 — quase quatro vezes o vão do par, e é isso que faz o par
// ler como par.
const LARGURA = 480
const COLUNA = 40
const BARRA = 13
const PASSO = 16
const RECUO = 5.5
const BASE_Y = 236
const TOPO_Y = 4
const PLOTAGEM = 232

/** Carregando: 40 % da altura útil, igual em todas as barras. O platô achatado
 *  não se confunde com dado — e o eixo e a base continuam reais, porque derivam
 *  do mês da URL, não do servidor. */
const ALTURA_DO_PLATO = Math.round(PLOTAGEM * 0.4)

/** A altura da barra em unidades do viewBox.
 *
 *  Zero **não desenha barra** (a regra da E6a para participação nula): um traço
 *  de altura mínima ali afirmaria que houve movimento. Valor mínimo desenha 3 —
 *  o fio que diz "houve algo" sem fingir altura. */
export function alturaDaBarra(cents: number, maximoCents: number): number {
  if (cents === 0 || maximoCents <= 0) return 0
  return Math.max(3, Math.round((PLOTAGEM * cents) / maximoCents))
}

/** Barras agrupadas, duas séries (docs/DESIGN.md, E7 (c); ADR-021: SVG
 *  próprio, sem biblioteca de gráfico).
 *
 *  Quatro decisões que este componente sustenta:
 *
 *  1. **Agrupadas, nunca empilhadas e nunca divergentes.** Empilhada somaria
 *     aporte com resgate — uma soma que não existe. Divergente codificaria
 *     "positivo x negativo", que é a leitura errada. Agrupada põe as duas na
 *     mesma base e na mesma escala: comparar é olhar.
 *  2. **O SVG é ilustração.** `aria-hidden` e `focusable="false"`: a fonte dos
 *     números é a tabela ao lado, com `caption`. O `<title>` nativo por barra é
 *     cortesia para quem usa mouse — sem tooltip próprio, sem foco em barra,
 *     sem navegação por setas no desenho.
 *  3. **Sem eixo Y, sem malha, sem número sobre a barra.** O único traço de
 *     referência é a linha de base. A escala é dita **em palavras** no
 *     `<figcaption>`, e os números estão na tabela.
 *  4. **O eixo X é HTML**, não `<text>` do SVG: texto dentro do `viewBox`
 *     encolheria junto com ele (13 px viram 8,4 px a 375 px) e ignoraria o zoom
 *     de fonte do navegador. */
export function BarChart({
  colunas,
  series,
  maximoCents,
  legenda,
  loading = false,
}: BarChartProps) {
  return (
    <figure className={styles.figura} aria-busy={loading || undefined}>
      <div className={styles.plotagem}>
        {/* Ilustração (ADR-021): a fonte dos números é a tabela com `caption`
            ao lado. `<title>` por barra é cortesia para quem usa mouse. */}
        <svg
          viewBox={`0 0 ${LARGURA} 240`}
          aria-hidden="true"
          focusable="false"
          className={styles.svg}
        >
          {colunas.map((coluna, indice) =>
            coluna.viradaDeAno && indice > 0 ? (
              <line
                key={`ano-${coluna.key}`}
                className={styles.viradaDeAno}
                x1={COLUNA * indice}
                y1={TOPO_Y}
                x2={COLUNA * indice}
                y2={BASE_Y}
                vectorEffect="non-scaling-stroke"
              />
            ) : null,
          )}

          {colunas.map((coluna, indice) =>
            loading
              ? series.map((serie, posicao) => (
                  <rect
                    key={`${coluna.key}-${serie.papel}`}
                    className={styles.barraVazia}
                    x={COLUNA * indice + RECUO + posicao * PASSO}
                    y={BASE_Y - ALTURA_DO_PLATO}
                    width={BARRA}
                    height={ALTURA_DO_PLATO}
                  />
                ))
              : coluna.valores.map((valor, posicao) => {
                  const altura = alturaDaBarra(valor.cents, maximoCents)
                  // Zero não desenha: nem um traço de altura mínima, que
                  // afirmaria movimento onde não houve.
                  if (altura === 0) return null
                  return (
                    <rect
                      key={`${coluna.key}-${valor.papel}`}
                      className={styles.barra}
                      data-fill={valor.papel}
                      x={COLUNA * indice + RECUO + posicao * PASSO}
                      y={BASE_Y - altura}
                      width={BARRA}
                      height={altura}
                      vectorEffect="non-scaling-stroke"
                    >
                      <title>{coluna.titulos[posicao] ?? ''}</title>
                    </rect>
                  )
                }),
          )}

          {/* Desenhada por último: é a referência, e nenhuma barra passa por
              cima dela. */}
          <line
            className={styles.base}
            x1="0"
            y1={BASE_Y}
            x2={LARGURA}
            y2={BASE_Y}
            vectorEffect="non-scaling-stroke"
          />
        </svg>
      </div>

      {/* Doze nomes de mês sem valor nenhum são ruído no áudio, e o mês por
          extenso está em cada linha da tabela — daí o `aria-hidden`. A grade de
          12 colunas cai exatamente sobre os grupos porque a plotagem tem a
          mesma proporção do viewBox. */}
      {/* biome-ignore lint/a11y/noRedundantRoles: o papel e reposto de proposito, ver reset.css */}
      <ol className={styles.eixo} role="list" aria-hidden="true">
        {colunas.map((coluna) => (
          <li key={coluna.key}>{coluna.rotulo}</li>
        ))}
      </ol>

      {/* SEMPRE as duas séries, mesmo quando uma não tem nenhuma barra: sem a
          legenda completa, a tinta que sobrou vira ambígua. Não é
          `aria-hidden` — ela nomeia as séries em duas palavras e é o que liga a
          figura à tabela para quem enxerga. */}
      {/* biome-ignore lint/a11y/noRedundantRoles: o papel e reposto de proposito, ver reset.css */}
      <ol className={styles.legenda} role="list">
        {series.map((serie) => (
          <li key={serie.papel}>
            <Swatch papel={serie.papel} />
            <span className={styles.nome}>{serie.nome}</span>
          </li>
        ))}
      </ol>

      <figcaption className={styles.figcaption}>{legenda}</figcaption>
    </figure>
  )
}
