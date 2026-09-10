import type { IconProps } from './types'

export function EyeOffIcon({ size = 20, title }: IconProps) {
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
      <path d="M9.9 5.99A9.6 9.6 0 0 1 12 5.75c5.1 0 9.25 6.25 9.25 6.25a17.4 17.4 0 0 1-3.06 3.55" />
      <path d="M6.5 7.66A17.6 17.6 0 0 0 2.75 12S6.9 18.25 12 18.25a9.3 9.3 0 0 0 3.7-.77" />
      <path d="M10.05 10.2a2.75 2.75 0 0 0 3.83 3.94" />
      <path d="m4 3.75 16 16.5" />
    </svg>
  )
}
