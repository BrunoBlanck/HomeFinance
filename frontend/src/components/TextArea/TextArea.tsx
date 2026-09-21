import { type ReactNode, type Ref, type TextareaHTMLAttributes, useId } from 'react'
import { FieldShell } from '../FieldShell/FieldShell'
import fieldStyles from '../FieldShell/FieldShell.module.css'
import styles from './TextArea.module.css'

export type TextAreaProps = {
  label: string
  hint?: string | undefined
  error?: string | undefined
  labelAction?: ReactNode | undefined
  /** React 19: `ref` é prop normal — é por aqui que a tela devolve o foco ao
   *  campo quando a conferência falha. */
  ref?: Ref<HTMLTextAreaElement> | undefined
} & Omit<
  TextareaHTMLAttributes<HTMLTextAreaElement>,
  'className' | 'style' | 'id' | 'aria-describedby' | 'aria-invalid'
>

/** Campo de texto longo do HomeFinance — irmão gêmeo do `TextField`, sobre o
 *  mesmo `FieldShell`: rótulo estático e visível, dica OU erro no slot de
 *  mensagem, `aria-describedby` e `aria-invalid` cuidados aqui.
 *
 *  **É o segundo e último lugar em que `--font-mono` aparece** (o primeiro é
 *  o `<pre>` do prompt de `/ia`). Aqui a monoespaçada é semântica: o que se
 *  cola neste campo é um texto que vai INTEIRO para outro programa, e chaves,
 *  colchetes e aspas precisam ser distinguíveis de relance — é assim que a
 *  pessoa vê que colou "texto antes do primeiro {". Em qualquer outro campo é
 *  erro de revisão.
 *
 *  `spellCheck={false}`, `autoCapitalize="off"` e `autoCorrect="off"` vêm
 *  fixos: um corretor que troca `"kind"` por `"kindly"` ou põe maiúscula no
 *  começo de uma chave produz um JSON que o servidor recusa sem que a pessoa
 *  tenha digitado nada. Sem `resize: none`: a caixa cresce na vertical, porque
 *  quem cola 300 linhas quer ver o fim delas. */
export function TextArea({ label, hint, error, labelAction, ref, ...rest }: TextAreaProps) {
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
      <textarea
        {...rest}
        ref={ref}
        id={id}
        className={`${fieldStyles.control} ${styles.textarea}`}
        spellCheck={false}
        autoCapitalize="off"
        autoCorrect="off"
        aria-describedby={messageId}
        aria-invalid={error ? true : undefined}
      />
    </FieldShell>
  )
}
