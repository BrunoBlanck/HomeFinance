import type { IconProps } from './types'

/** Relatórios — uma pizza com o quarto superior direito fechado e o resto em
 *  arco aberto (docs/DESIGN.md, E6a (g)). As pontas do arco param 23° antes das
 *  arestas do quarto, o que deixa ~1,4 px de ar depois do traço. É pizza, e não
 *  rosca, porque a 20 px o furo não lê — e o item nomeia a seção `Relatórios`,
 *  não este gráfico. Traço 1.5, `currentColor` e a mesma massa óptica de
 *  `TransfersIcon` (4,75 a 19,25). */
export function ChartIcon({ size = 20, title }: IconProps) {
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
      <path d="M9.17 5.33A7.25 7.25 0 1 0 18.67 14.83" />
      <path d="M12 12V4.75a7.25 7.25 0 0 1 7.25 7.25z" />
    </svg>
  )
}
