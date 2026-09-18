import { type ReactNode, useEffect, useRef } from 'react'
import { AlertIcon } from '../icons/AlertIcon'
import { CheckIcon } from '../icons/CheckIcon'
import { InfoIcon } from '../icons/InfoIcon'
import styles from './Alert.module.css'

export type AlertTone = 'error' | 'success' | 'info' | 'warning'

type AlertProps = {
  tone: AlertTone
  title?: string | undefined
  children: ReactNode
  action?: ReactNode | undefined
  /** Leva o foco para o aviso ao montar — use em resultado de submit. */
  autoFocus?: boolean | undefined
  /** A urgência do live region, quando o TOM não é quem deve decidi-la.
   *
   *  A regra (docs/DESIGN.md, E7 (l)): **resultado de uma ação já feita é
   *  `assertive`; prévia de uma ação ainda não feita é `polite`.** Um aviso que
   *  monta enquanto a pessoa ainda está no controle — o aviso da troca de
   *  natureza aparece no `onChange` do `<select>`, e no Windows a seta já troca
   *  o valor de um select fechado — não pode interromper o anúncio da opção
   *  recém-escolhida para ler a consequência. Mesmo precedente da dica do
   *  atalho de categorização (E2c (h.3), `role="status"`).
   *
   *  Sem a prop, o papel continua vindo do tom: nenhum `Alert` existente muda. */
  live?: 'assertive' | 'polite' | undefined
  id?: string | undefined
}

function iconFor(tone: AlertTone) {
  if (tone === 'success') return <CheckIcon size={20} />
  if (tone === 'info') return <InfoIcon size={20} />
  return <AlertIcon size={20} />
}

/** O papel ARIA do aviso. `role="alert"` já implica `aria-live="assertive"` e
 *  `role="status"`, `polite` — um atributo só, e não dois que podem divergir.
 *
 *  Sem `live`, o tom decide, exatamente como antes. */
function papelDoLive(tone: AlertTone, live: 'assertive' | 'polite' | undefined) {
  if (live) return live === 'assertive' ? 'alert' : 'status'
  return tone === 'error' || tone === 'warning' ? 'alert' : 'status'
}

/** Não é caixa saturada: fundo levemente tingido, filete lateral na cor do tom
 *  e texto sempre em `--ink`. Cor cheia fica para dinheiro. */
export function Alert({ tone, title, children, action, autoFocus = false, live, id }: AlertProps) {
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (autoFocus) ref.current?.focus()
  }, [autoFocus])

  return (
    <div
      ref={ref}
      id={id}
      className={styles.alert}
      data-tone={tone}
      role={papelDoLive(tone, live)}
      tabIndex={autoFocus ? -1 : undefined}
    >
      <span className={styles.icon}>{iconFor(tone)}</span>
      <div className={styles.body}>
        {title ? <strong className={styles.title}>{title}</strong> : null}
        <div className={styles.text}>{children}</div>
        {action ? <div className={styles.action}>{action}</div> : null}
      </div>
    </div>
  )
}
