import { type FormEventHandler, type ReactNode, useId } from 'react'
import { FormError } from '@/components/Alert/FormError'
import styles from './AuthForm.module.css'

type AuthFormProps = {
  onSubmit: FormEventHandler<HTMLFormElement>
  /** Mensagem vinda do servidor. É o único ponto que anuncia a falha. */
  error?: string | undefined
  /** Leve o foco para o erro (padrão). Telas de código desligam e focam o campo. */
  focusError?: boolean | undefined
  children: ReactNode
  primary: ReactNode
  secondary?: ReactNode
  /** Linha de status abaixo da ação secundária (resultado do reenvio). */
  note?: ReactNode
}

/** Ritmo vertical comum às cinco telas de autenticação. */
export function AuthForm({
  onSubmit,
  error,
  focusError = true,
  children,
  primary,
  secondary,
  note,
}: AuthFormProps) {
  const errorId = useId()

  return (
    <form
      className={styles.form}
      onSubmit={onSubmit}
      noValidate
      aria-describedby={error ? errorId : undefined}
    >
      {error ? (
        <div className={styles.error}>
          <FormError id={errorId} autoFocus={focusError}>
            {error}
          </FormError>
        </div>
      ) : null}
      <div className={styles.fields}>{children}</div>
      <div className={styles.primary}>{primary}</div>
      {secondary ? <div className={styles.secondary}>{secondary}</div> : null}
      {note ? <div className={styles.note}>{note}</div> : null}
    </form>
  )
}
