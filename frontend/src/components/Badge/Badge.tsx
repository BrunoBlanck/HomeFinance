import type { ReactNode } from 'react'
import styles from './Badge.module.css'

type BadgeProps = {
  /** `neutral` para rótulo de classificação (tipo de conta, natureza).
   *  Os tons cromáticos são para ESTADO, e nunca para decoração. */
  tone?: 'neutral' | 'muted' | 'warning' | 'income' | 'expense' | undefined
  children: ReactNode
}

/** Etiqueta curta.
 *
 *  Sempre TEXTO — nunca só uma bolinha colorida. Estado comunicado só por cor
 *  é invisível para daltônico e para quem imprime a tela (docs/DESIGN.md);
 *  a cor aqui é reforço, e a palavra é a informação. */
export function Badge({ tone = 'neutral', children }: BadgeProps) {
  return (
    <span className={styles.badge} data-tone={tone}>
      {children}
    </span>
  )
}
