import type { ButtonHTMLAttributes, MouseEvent, ReactNode } from 'react'
import { Spinner } from '../Spinner/Spinner'
import styles from './Button.module.css'

type ButtonProps = {
  variant?: 'primary' | 'secondary' | 'quiet' | 'danger' | undefined
  size?: 'md' | 'sm' | undefined
  loading?: boolean | undefined
  iconStart?: ReactNode | undefined
  iconEnd?: ReactNode | undefined
  fullWidth?: boolean | undefined
} & Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'className' | 'style'>

export function Button({
  variant = 'secondary',
  size = 'md',
  loading = false,
  iconStart,
  iconEnd,
  fullWidth = false,
  type = 'button',
  onClick,
  children,
  ...rest
}: ButtonProps) {
  // Em carregamento o botão NUNCA recebe `disabled`: ele perderia o foco e o
  // usuário de teclado ficaria sem referência. Fica focável, anuncia-se ocupado
  // e o clique morre aqui.
  function handleClick(event: MouseEvent<HTMLButtonElement>) {
    if (loading) {
      event.preventDefault()
      event.stopPropagation()
      return
    }
    onClick?.(event)
  }

  const leading = loading ? <Spinner size="sm" /> : iconStart
  // Contrapeso invisível: com largura fixa, o rótulo não desliza ao surgir o spinner.
  const needsBalance = loading && fullWidth && !iconStart && !iconEnd

  return (
    <button
      {...rest}
      type={type}
      onClick={handleClick}
      className={styles.button}
      data-variant={variant}
      data-size={size}
      data-full-width={fullWidth || undefined}
      aria-disabled={loading ? true : rest['aria-disabled']}
      aria-busy={loading || undefined}
    >
      {leading ? <span className={styles.icon}>{leading}</span> : null}
      <span className={styles.label}>{children}</span>
      {iconEnd ? <span className={styles.icon}>{iconEnd}</span> : null}
      {needsBalance ? <span className={styles.icon} aria-hidden="true" /> : null}
    </button>
  )
}
