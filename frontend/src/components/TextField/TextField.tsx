import { type InputHTMLAttributes, type ReactNode, type Ref, useId } from 'react'
import { FieldShell } from '../FieldShell/FieldShell'
import fieldStyles from '../FieldShell/FieldShell.module.css'

export type TextFieldProps = {
  label: string
  hint?: string | undefined
  error?: string | undefined
  labelAction?: ReactNode | undefined
  /** React 19: `ref` é prop normal — é por aqui que o react-hook-form registra. */
  ref?: Ref<HTMLInputElement> | undefined
} & Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'className' | 'style' | 'id' | 'aria-describedby' | 'aria-invalid'
>

/** Campo de texto do HomeFinance.
 *  Rótulo estático e permanente — sem label flutuante, sem underline que cresce
 *  no foco. O placeholder só aparece quando exemplifica formato. */
export function TextField({ label, hint, error, labelAction, ref, ...rest }: TextFieldProps) {
  const reactId = useId()
  const id = `${reactId}-field`
  const messageId = `${reactId}-message`

  return (
    <FieldShell
      id={id}
      messageId={messageId}
      label={label}
      labelAction={labelAction}
      hint={hint}
      error={error}
    >
      <input
        {...rest}
        ref={ref}
        id={id}
        className={fieldStyles.control}
        aria-describedby={messageId}
        aria-invalid={error ? true : undefined}
      />
    </FieldShell>
  )
}
