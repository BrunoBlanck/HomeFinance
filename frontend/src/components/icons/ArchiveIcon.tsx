import type { IconProps } from './types'

export function ArchiveIcon({ size = 16, title }: IconProps) {
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
      <path d="M3.75 5.75h16.5v3.5H3.75z" />
      <path d="M5.5 9.25v8a1 1 0 0 0 1 1h11a1 1 0 0 0 1-1v-8" />
      <path d="M10 12.5h4" />
    </svg>
  )
}
