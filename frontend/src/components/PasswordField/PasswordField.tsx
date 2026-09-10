import { type ChangeEvent, useId, useState } from 'react'
import { FieldShell } from '../FieldShell/FieldShell'
import fieldStyles from '../FieldShell/FieldShell.module.css'
import { CheckIcon } from '../icons/CheckIcon'
import { EyeIcon } from '../icons/EyeIcon'
import { EyeOffIcon } from '../icons/EyeOffIcon'
import type { TextFieldProps } from '../TextField/TextField'
import styles from './PasswordField.module.css'

const MIN_LENGTH = 12

type PasswordFieldProps = Omit<TextFieldProps, 'type' | 'autoComplete'> & {
  /** O tipo impede `autoComplete="off"`: esconder o campo do gerenciador de
   *  senhas empurra o usuário para senhas piores. */
  autoComplete: 'current-password' | 'new-password'
  toggleable?: boolean | undefined
  showRequirement?: boolean | undefined
}

/** Sem campo de "confirmar senha" em nenhuma tela: existe o botão de revelar e
 *  existe recuperação por e-mail. Digitar duas vezes só produz erro de digitação. */
export function PasswordField({
  label,
  hint,
  error,
  labelAction,
  ref,
  toggleable = true,
  showRequirement = false,
  onChange,
  ...rest
}: PasswordFieldProps) {
  const reactId = useId()
  const id = `${reactId}-password`
  const messageId = `${reactId}-message`
  const requirementId = `${reactId}-requirement`
  const [revealed, setRevealed] = useState(false)
  const [meetsLength, setMeetsLength] = useState(false)

  function handleChange(event: ChangeEvent<HTMLInputElement>) {
    if (showRequirement) setMeetsLength(event.target.value.length >= MIN_LENGTH)
    onChange?.(event)
  }

  return (
    <FieldShell
      id={id}
      messageId={messageId}
      label={label}
      labelAction={labelAction}
      hint={hint}
      error={error}
      extra={
        showRequirement ? (
          <p
            id={requirementId}
            className={styles.requirement}
            data-met={meetsLength || undefined}
            aria-live="polite"
          >
            <CheckIcon size={14} />
            <span>Pelo menos {MIN_LENGTH} caracteres</span>
            <span className="sr-only">{meetsLength ? 'requisito atendido' : ''}</span>
          </p>
        ) : null
      }
    >
      <div className={styles.control}>
        <input
          {...rest}
          ref={ref}
          id={id}
          type={revealed ? 'text' : 'password'}
          maxLength={128}
          onChange={handleChange}
          className={fieldStyles.control}
          data-has-toggle={toggleable || undefined}
          aria-describedby={showRequirement ? `${messageId} ${requirementId}` : messageId}
          aria-invalid={error ? true : undefined}
        />
        {toggleable ? (
          <button
            type="button"
            className={styles.toggle}
            onClick={() => setRevealed((value) => !value)}
            aria-label={revealed ? 'Ocultar senha' : 'Mostrar senha'}
            aria-controls={id}
          >
            {revealed ? <EyeOffIcon size={20} /> : <EyeIcon size={20} />}
          </button>
        ) : null}
      </div>
    </FieldShell>
  )
}
