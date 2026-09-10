import { type ReactNode, useEffect, useRef } from 'react'
import { Logo } from '@/components/Logo/Logo'
import styles from './AuthLayout.module.css'

type AuthLayoutProps = {
  /** Vira "{documentTitle} · HomeFinance" na aba. */
  documentTitle: string
  eyebrow: string
  step?: string | undefined
  title: string
  intro?: ReactNode | undefined
  /** Aviso acima do formulário (ex.: o info que vem do login não verificado). */
  notice?: ReactNode | undefined
  children: ReactNode
  footer?: ReactNode | undefined
}

/** A viewport é o caderno aberto: capa à esquerda, folha à direita, uma costura
 *  de 1px entre as duas. Nada de cartão branco flutuando sobre fundo cinza —
 *  um caderno de contas não tem um cartão pairando sobre a mesa. */
export function AuthLayout({
  documentTitle,
  eyebrow,
  step,
  title,
  intro,
  notice,
  children,
  footer,
}: AuthLayoutProps) {
  const headingRef = useRef<HTMLHeadingElement>(null)
  const sheetRef = useRef<HTMLElement>(null)

  useEffect(() => {
    document.title = `${documentTitle} · HomeFinance`
  }, [documentTitle])

  // O foco vai para o <h1> da tela nova — a menos que a própria tela já tenha
  // colocado o foco em algum lugar melhor (o CodeInput das telas 3 e 5).
  useEffect(() => {
    const active = document.activeElement
    if (active && active !== document.body && sheetRef.current?.contains(active)) return
    headingRef.current?.focus()
  }, [])

  return (
    <div className={styles.shell}>
      <div className={styles.coverTop}>
        <span className={styles.logoLarge}>
          <Logo variant="full" size="lg" />
        </span>
        <span className={styles.logoSmall}>
          <Logo variant="full" size="md" />
        </span>
      </div>

      <p className={styles.tagline}>O caderno de contas da casa.</p>

      <div className={styles.coverNote}>
        <p>
          Feito para uma casa, não para um banco. Receitas, despesas, contas que vencem e orçamentos
          no mesmo lugar.
        </p>
        <p>Versão em desenvolvimento — Fase 3 do roadmap.</p>
      </div>

      <main className={styles.sheet} ref={sheetRef}>
        <div className={styles.sheetInner}>
          <div className={styles.pageHead}>
            <span>{eyebrow}</span>
            {step ? <span className={styles.step}>{step}</span> : null}
          </div>
          <h1 className={styles.title} ref={headingRef} tabIndex={-1}>
            {title}
          </h1>
          {intro ? <p className={styles.intro}>{intro}</p> : null}
          {notice ? <div className={styles.notice}>{notice}</div> : null}
          {children}
          {footer ? <div className={styles.footer}>{footer}</div> : null}
        </div>
      </main>
    </div>
  )
}
