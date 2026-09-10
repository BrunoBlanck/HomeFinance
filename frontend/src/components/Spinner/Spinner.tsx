import styles from './Spinner.module.css'

type SpinnerProps = {
  size?: 'sm' | 'md' | undefined
  /** Ausente ⇒ decorativo. Presente ⇒ `role="status"` com texto só para leitor de tela. */
  label?: string | undefined
}

export function Spinner({ size = 'md', label }: SpinnerProps) {
  const text = label === undefined ? null : label || 'Carregando'

  return (
    <span
      className={styles.wrap}
      data-size={size}
      role={text ? 'status' : undefined}
      aria-hidden={text ? undefined : true}
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        className={styles.spinner}
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeDasharray="47 16"
        focusable="false"
        aria-hidden="true"
      >
        <circle cx="12" cy="12" r="10" />
      </svg>
      {text ? <span className="sr-only">{text}</span> : null}
    </span>
  )
}
