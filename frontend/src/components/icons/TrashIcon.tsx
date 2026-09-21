import type { IconProps } from './types'

export function TrashIcon({ size = 16, title }: IconProps) {
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
      <path d="M4.75 6.75h14.5" />
      <path d="M9.5 6.75V5.5a1 1 0 0 1 1-1h3a1 1 0 0 1 1 1v1.25" />
      <path d="M6.75 6.75 7.6 18.4a1 1 0 0 0 1 .85h6.8a1 1 0 0 0 1-.85l.85-11.65" />
    </svg>
  )
}
