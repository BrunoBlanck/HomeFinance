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
  id?: string | undefined
}

function iconFor(tone: AlertTone) {
  if (tone === 'success') return <CheckIcon size={20} />
  if (tone === 'info') return <InfoIcon size={20} />
  return <AlertIcon size={20} />
}

/** Não é caixa saturada: fundo levemente tingido, filete lateral na cor do tom
 *  e texto sempre em `--ink`. Cor cheia fica para dinheiro. */
export function Alert({ tone, title, children, action, autoFocus = false, id }: AlertProps) {
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
      role={tone === 'error' || tone === 'warning' ? 'alert' : 'status'}
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
