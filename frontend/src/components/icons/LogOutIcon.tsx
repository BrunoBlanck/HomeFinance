import type { IconProps } from './types'

export function LogOutIcon({ size = 20, title }: IconProps) {
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
      <path d="M14.75 4.75H6.9a1.6 1.6 0 0 0-1.65 1.6v11.3a1.6 1.6 0 0 0 1.65 1.6h7.85" />
      <path d="M18.75 12H9.5" />
      <path d="m15.75 8.75 3.5 3.25-3.5 3.25" />
    </svg>
  )
}
