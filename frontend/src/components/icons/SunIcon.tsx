import type { IconProps } from './types'

export function SunIcon({ size = 18, title }: IconProps) {
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
      <circle cx="12" cy="12" r="4.15" />
      <path d="M12 2.75v2" />
      <path d="M12 19.25v2" />
      <path d="m5.5 5.5 1.4 1.4" />
      <path d="m17.1 17.1 1.4 1.4" />
      <path d="M2.75 12h2" />
      <path d="M19.25 12h2" />
      <path d="m5.5 18.5 1.4-1.4" />
      <path d="m17.1 6.9 1.4-1.4" />
    </svg>
  )
}
