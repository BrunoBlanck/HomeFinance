import type { IconProps } from './types'

/** Transferências — duas setas horizontais empilhadas em sentidos opostos: a de
 *  cima vai para a direita, a de baixo volta para a esquerda. É o desenho do
 *  dinheiro que só mudou de conta dentro da casa. Traço 1.5, `currentColor` e
 *  a mesma massa óptica de `LedgerIcon`. */
export function TransfersIcon({ size = 20, title }: IconProps) {
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
      <path d="M4.75 9h14.5" />
      <path d="m15.25 5.25 4 3.75-4 3.75" />
      <path d="M19.25 15h-14.5" />
      <path d="m8.75 11.25-4 3.75 4 3.75" />
    </svg>
  )
}
