import type { IconProps } from './types'

export function HouseIcon({ size = 20, title }: IconProps) {
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
      <path d="M4 10.25 12 4l8 6.25" />
      <path d="M5.75 9.5V19.5h12.5V9.5" />
      <path d="M9.75 19.5v-5.25h4.5v5.25" />
    </svg>
  )
}
