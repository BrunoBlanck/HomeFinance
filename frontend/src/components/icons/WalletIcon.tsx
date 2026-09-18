import type { IconProps } from './types'

export function WalletIcon({ size = 20, title }: IconProps) {
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
      <path d="M3.75 7.75A2 2 0 0 1 5.75 5.75h11a1.5 1.5 0 0 1 1.5 1.5v1.5" />
      <path d="M3.75 7.75v9a1.5 1.5 0 0 0 1.5 1.5h13.5a1.5 1.5 0 0 0 1.5-1.5v-2.25" />
      <path d="M20.25 9.25h-4a2.5 2.5 0 0 0 0 5h4z" />
    </svg>
  )
}
