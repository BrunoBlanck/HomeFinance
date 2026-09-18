import type { IconProps } from './types'

export function TagsIcon({ size = 20, title }: IconProps) {
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
      <path d="M4.75 4.75h5.1a1.5 1.5 0 0 1 1.06.44l7.15 7.15a1.5 1.5 0 0 1 0 2.12l-4.6 4.6a1.5 1.5 0 0 1-2.12 0l-7.15-7.15a1.5 1.5 0 0 1-.44-1.06z" />
      <circle cx="8.25" cy="8.25" r="1.15" />
    </svg>
  )
}
