import type { ReactNode } from 'react'
import { AlertIcon } from '../icons/AlertIcon'
import styles from './FieldShell.module.css'

type FieldShellProps = {
  id: string
  messageId: string
  label: string
  /** Ação no canto do rótulo — ex.: "Esqueci minha senha". */
  labelAction?: ReactNode | undefined
  hint?: string | undefined
  error?: string | undefined
  children: ReactNode
  /** Conteúdo que vem DEPOIS da mensagem — hoje só a linha de requisito de senha. */
  extra?: ReactNode
}

/** Esqueleto interno de campo. Não é exportado como UI: quem monta campo é o
 *  TextField, o PasswordField ou o CodeInput.
 *
 *  O slot de mensagem existe sempre, com altura reservada — validar não pode
 *  empurrar o resto do formulário para baixo. E mostra hint OU erro, nunca os
 *  dois: dois textos concorrentes sob o mesmo campo é ruído. */
export function FieldShell({
  id,
  messageId,
  label,
  labelAction,
  hint,
  error,
  children,
  extra,
}: FieldShellProps) {
  return (
    <div className={styles.field}>
      <div className={styles.labelRow}>
        <label className={styles.label} htmlFor={id}>
          {label}
        </label>
        {labelAction}
      </div>
      {children}
      {/* Sem role="alert": a validação acontece no submit e no blur, e um live
          region aqui tagarelaria a cada tecla. O anúncio é do FormError. */}
      <p className={styles.message} id={messageId} data-tone={error ? 'error' : 'hint'}>
        {error ? (
          <>
            <AlertIcon size={14} />
            <span>{error}</span>
          </>
        ) : (
          hint
        )}
      </p>
      {extra}
    </div>
  )
}
