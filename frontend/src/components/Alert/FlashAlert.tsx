import type { Flash } from '@/lib/navigation'
import { Alert } from './Alert'
import styles from './Alert.module.css'

/** Renderiza o aviso que veio no state do roteador. */
export function FlashAlert({ flash }: { flash: Flash }) {
  return (
    <Alert tone={flash.tone} title={flash.title}>
      {flash.message}
      {flash.detail ? <p className={styles.detail}>{flash.detail}</p> : null}
    </Alert>
  )
}
