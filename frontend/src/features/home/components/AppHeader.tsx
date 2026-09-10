import { Link } from '@tanstack/react-router'
import { Logo } from '@/components/Logo/Logo'
import type { Session } from '@/lib/session'
import styles from './AppHeader.module.css'
import { UserMenu } from './UserMenu'

type AppHeaderProps = {
  session: Session | undefined
}

/** Sem barra de navegação nesta fase: "Lançamentos", "Contas" e "Relatórios"
 *  ainda não existem, e menu que não leva a lugar nenhum é desonesto. */
export function AppHeader({ session }: AppHeaderProps) {
  return (
    <header className={styles.header}>
      <Link to="/" className={styles.brand} aria-label="HomeFinance — início">
        <Logo variant="full" size="sm" />
      </Link>
      {session ? (
        <div className={styles.account}>
          <span className={styles.household}>{session.household.name}</span>
          <UserMenu name={session.user.name} email={session.user.email} />
        </div>
      ) : null}
    </header>
  )
}
