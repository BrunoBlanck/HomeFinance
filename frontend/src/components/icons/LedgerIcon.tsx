import type { IconProps } from './types'

/** Lançamentos — o livro-caixa aberto: uma folha pautada com a linha de valor à
 *  direita. Traço 1.5 e `currentColor`, como todo o conjunto. */
export function LedgerIcon({ size = 20, title }: IconProps) {
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
      <path d="M5.75 3.75h9.5l4 4v12.5a1 1 0 0 1-1 1H5.75a1 1 0 0 1-1-1V4.75a1 1 0 0 1 1-1Z" />
      <path d="M15.25 3.75v4h4" />
      <path d="M8.25 12.25h4" />
      <path d="M8.25 15.75h7" />
    </svg>
  )
}
