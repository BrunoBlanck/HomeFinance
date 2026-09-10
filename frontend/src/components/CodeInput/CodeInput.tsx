import { type Ref, useEffect, useId, useRef } from 'react'
import { FieldShell } from '../FieldShell/FieldShell'
import styles from './CodeInput.module.css'

const CODE_LENGTH = 6

type CodeInputProps = {
  label?: string | undefined
  hint?: string | undefined
  /** Erro local (ex.: código incompleto). Erro do servidor usa `invalid` + FormError. */
  error?: string | undefined
  /** Marca o campo como inválido sem repetir texto: a frase vive no FormError. */
  invalid?: boolean | undefined
  value: string
  onChange: (value: string) => void
  onComplete?: ((value: string) => void) | undefined
  busy?: boolean | undefined
  autoFocus?: boolean | undefined
  ref?: Ref<HTMLInputElement> | undefined
}

/** UM input, não seis caixas — e a decisão é de acessibilidade, não de estética:
 *
 *  1. Seis inputs viram seis campos no modo de navegação do leitor de tela; o
 *     usuário ouve "edição, em branco" seis vezes e nunca revisa "123456" como
 *     um valor único.
 *  2. `autocomplete="one-time-code"` entrega os 6 dígitos a UM campo. Com seis,
 *     o autofill é heurístico e costuma despejar tudo no primeiro.
 *  3. Colar, apagar, navegar e DESFAZER continuam nativos. Todo handler de
 *     paste/keydown que redistribui dígitos é mais uma chance de quebrar
 *     teclado físico, teclado virtual e leitor de tela.
 *  4. Sem roubo de foco entre caixas — bug clássico com Gboard e SwiftKey.
 *  5. Um `aria-invalid`, um `aria-describedby`, UM anúncio. */
export function CodeInput({
  label = 'Código de 6 dígitos',
  hint = 'Digite ou cole o código do e-mail.',
  error,
  invalid = false,
  value,
  onChange,
  onComplete,
  busy = false,
  autoFocus = false,
  ref,
}: CodeInputProps) {
  const reactId = useId()
  const id = `${reactId}-code`
  const messageId = `${reactId}-message`
  // Guarda o valor que já foi autoenviado. Zera quando o código encolhe, o que
  // resolve as três regras de uma vez: dispara ao crescer, nunca ao apagar, e
  // não repete depois de um erro (o valor continua o mesmo).
  const submittedRef = useRef<string | null>(null)

  useEffect(() => {
    if (value.length < CODE_LENGTH) {
      submittedRef.current = null
      return
    }
    if (!onComplete || busy) return
    if (submittedRef.current === value) return
    submittedRef.current = value
    onComplete(value)
  }, [value, busy, onComplete])

  const isInvalid = invalid || Boolean(error)

  return (
    <FieldShell id={id} messageId={messageId} label={label} hint={hint} error={error}>
      <input
        ref={ref}
        id={id}
        name="code"
        value={value}
        onChange={(event) => onChange(event.target.value.replace(/\D/g, '').slice(0, CODE_LENGTH))}
        className={styles.input}
        /* `type="number"` seria um desastre aqui: spinner, scroll acidental,
           perda do zero à esquerda e recusa de colagem com espaços. */
        type="text"
        inputMode="numeric"
        pattern="[0-9]*"
        autoComplete="one-time-code"
        /* DIVERGÊNCIA CONSCIENTE da spec 0002 §3, reportada ao designer.
           `maxLength={6}` é incompatível com a exigência de colar "12 34-56":
           o atributo trunca o texto colado ANTES do onChange, e sobra "12 34-"
           → "1234". O limite de 6 dígitos é garantido pelo próprio onChange
           (`slice(0, 6)`), que é a única fonte de verdade do valor. */
        enterKeyHint="done"
        autoCorrect="off"
        autoCapitalize="off"
        spellCheck={false}
        /* `readOnly` e não `disabled`: desabilitar tiraria o campo do foco e do
           cursor virtual bem no momento em que o usuário quer conferir o que digitou. */
        readOnly={busy}
        aria-busy={busy || undefined}
        aria-describedby={messageId}
        aria-invalid={isInvalid ? true : undefined}
        // biome-ignore lint/a11y/noAutofocus: é o único campo da tela e o motivo de ela existir.
        autoFocus={autoFocus}
      />
    </FieldShell>
  )
}
