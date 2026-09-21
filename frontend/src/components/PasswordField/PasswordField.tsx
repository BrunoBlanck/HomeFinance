import { type ChangeEvent, useId, useState } from 'react'
import { FieldShell } from '../FieldShell/FieldShell'
import fieldStyles from '../FieldShell/FieldShell.module.css'
import { CheckIcon } from '../icons/CheckIcon'
import { EyeIcon } from '../icons/EyeIcon'
import { EyeOffIcon } from '../icons/EyeOffIcon'
import type { TextFieldProps } from '../TextField/TextField'
import styles from './PasswordField.module.css'

const MIN_LENGTH = 12

/** O `autocomplete` do campo. São dois casos que **não são o mesmo**, e trocar
 *  um pelo outro tem consequência real — por isso o tipo não aceita nada além
 *  destes três valores.
 *
 *  **`current-password` / `new-password` — credencial de sessão.** É a senha da
 *  conta: a pessoa precisa lembrar dela e digitá-la de novo. Aqui esconder o
 *  campo do gerenciador de senhas é um tiro no pé — empurra o usuário para uma
 *  senha pior, que ele consiga decorar. É o default moral deste componente.
 *
 *  **`off` — segredo de uso único, que não é credencial de ninguém.** Hoje, só
 *  a senha do arquivo da importação (o CPF do titular do cartão): digitada uma
 *  vez, mandada como `[]byte` e zerada em segundos. Com `current-password`, o
 *  Chrome, o Firefox e o Safari ofereceriam **salvar esse dado de terceiro para
 *  a origem do HomeFinance** e sincronizá-lo para o cofre do sistema ou da
 *  conta Google — fora de qualquer ciclo de vida que a aplicação controle, e
 *  anulando todo o cuidado do resto do caminho. Pior: como o login usa
 *  `current-password` na **mesma origem**, o navegador trata os dois campos
 *  como a mesma credencial e chega a oferecer a troca de uma senha pela outra,
 *  preenchendo o CPF no login depois.
 *
 *  **A regra, em uma frase:** `off` **só** em segredo efêmero de arquivo. Se o
 *  campo é uma senha do HomeFinance, é `current-password` ou `new-password` —
 *  sem exceção. Contrato: `backend/api/openapi.yaml`, campo `password` de
 *  `POST /imports`. */
type PasswordAutoComplete = 'current-password' | 'new-password' | 'off'

type PasswordFieldProps = Omit<TextFieldProps, 'type' | 'autoComplete'> & {
  /** Obrigatório e restrito — ver `PasswordAutoComplete`. `off` é exclusivo de
   *  segredo de uso único (senha de arquivo), nunca de senha de conta. */
  autoComplete: PasswordAutoComplete
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
