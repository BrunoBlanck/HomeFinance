import type { IconProps } from './types'

export function LockIcon({ size = 20, title }: IconProps) {
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
      <rect x="4.25" y="10.25" width="15.5" height="9.5" rx="1.6" />
      <path d="M7.75 10.25V7.9a4.25 4.25 0 0 1 8.5 0v2.35" />
      <path d="M12 14v2" />
    </svg>
  )
}
