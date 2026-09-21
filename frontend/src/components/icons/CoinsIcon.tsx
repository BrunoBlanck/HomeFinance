import type { IconProps } from './types'

/** Investimentos — três moedas empilhadas, vistas de lado: a face da de cima é
 *  uma elipse inteira, as laterais descem retas e dois arcos marcam a borda da
 *  do meio e a de baixo.
 *
 *  É o desenho de **dinheiro posto de lado, uma parcela de cada vez** — que é o
 *  assunto da tela: fluxo, não posição. Seta subindo e barras crescentes
 *  prometeriam rentabilidade, que a spec 0006 tira do escopo; cofrinho é
 *  poupança e some a 20 px; pote com nível de conteúdo prometeria "quanto eu
 *  tenho". Traço 1.5, `currentColor` e a mesma massa óptica (4,75–19,25) de
 *  `TransfersIcon` e `ChartIcon`. */
export function CoinsIcon({ size = 20, title }: IconProps) {
  return (
    // biome-ignore lint/a11y/noSvgWithoutTitle: sem `title` o icone e decorativo e sai com aria-hidden; o contrato esta em icons/types.ts
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      role={title ? 'img' : undefined}
      aria-hidden={title ? undefined : true}
      focusable="false"
    >
      {title ? <title>{title}</title> : null}
      <ellipse cx="12" cy="7.75" rx="7.25" ry="3" />
      <path d="M4.75 7.75v8.5a7.25 3 0 0 0 14.5 0v-8.5" />
      <path d="M4.75 12a7.25 3 0 0 0 14.5 0" />
    </svg>
  )
}
