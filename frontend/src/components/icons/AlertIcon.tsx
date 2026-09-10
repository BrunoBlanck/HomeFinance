import type { IconProps } from './types'

export function AlertIcon({ size = 20, title }: IconProps) {
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
      <path d="M12 4.4 2.9 20.1a.8.8 0 0 0 .7 1.2h16.8a.8.8 0 0 0 .7-1.2Z" />
      <path d="M12 10v4.4" />
      <path d="M12 17.6h.01" />
    </svg>
  )
}
