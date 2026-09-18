import type { ReactNode } from 'react'
import styles from './EmptyState.module.css'

type EmptyStateProps = {
  title: string
  /** O que a pessoa pode FAZER agora. Estado vazio sem saída é um beco. */
  description: string
  action?: ReactNode | undefined
}

/** Estado vazio.
 *
 *  Obrigatório em toda lista (docs/DESIGN.md). E é vazio com ORIENTAÇÃO: "Nada
 *  por aqui" não ajuda ninguém — o texto diz o que a tela guarda e qual é o
 *  próximo passo.
 *
 *  Sem ilustração e sem ícone gigante: numa lista que vai encher em dois
 *  minutos, o desenho é enfeite que atrapalha na centésima vez. */
export function EmptyState({ title, description, action }: EmptyStateProps) {
  return (
    <div className={styles.empty}>
      <p className={styles.title}>{title}</p>
      <p className={styles.description}>{description}</p>
      {action ? <div className={styles.action}>{action}</div> : null}
    </div>
  )
}
