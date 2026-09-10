import { Logo } from '@/components/Logo/Logo'
import { TextLink } from '@/components/TextLink/TextLink'
import styles from './NotFoundScreen.module.css'

export function NotFoundScreen() {
  return (
    <main className={styles.wrap}>
      <Logo variant="full" size="md" />
      <h1 className={styles.title}>Não encontramos esta página.</h1>
      <p className={styles.text}>O endereço pode ter mudado de lugar.</p>
      <TextLink to="/">Voltar para o início</TextLink>
    </main>
  )
}
