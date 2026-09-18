import { useQuery } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect, useRef } from 'react'
import { FlashAlert } from '@/components/Alert/FlashAlert'
import { ArrowRightIcon } from '@/components/icons/ArrowRightIcon'
import { CheckIcon } from '@/components/icons/CheckIcon'
import { Panel } from '@/components/Panel/Panel'
import { Skeleton } from '@/components/Skeleton/Skeleton'
import { isUnauthenticated } from '@/lib/errors'
import { firstName, formatFullDate } from '@/lib/format'
import { sessionQueryOptions } from '@/lib/session'
import styles from './HomeScreen.module.css'
import { LedgerPreview } from './LedgerPreview'

const WORKING = [
  'Criar conta com e-mail confirmado por código',
  'Entrar e sair com sessão segura',
  'Recuperar a senha pelo mesmo código de 6 dígitos',
  'Contas e categorias da casa',
]

const NEXT = ['Lançamentos do mês', 'Contas que vencem', 'Orçamentos e relatórios']

export function HomeScreen() {
  const navigate = useNavigate()
  const flash = useRouterState({ select: (state) => state.location.state.flash })
  const headingRef = useRef<HTMLHeadingElement>(null)
  const session = useQuery(sessionQueryOptions)

  useEffect(() => {
    document.title = 'Início · HomeFinance'
  }, [])

  useEffect(() => {
    headingRef.current?.focus()
  }, [])

  // Sessão vencida não é erro de tela: é hora de voltar para o login.
  useEffect(() => {
    if (session.isError && isUnauthenticated(session.error)) {
      navigate({ to: '/entrar', replace: true })
    }
  }, [session.isError, session.error, navigate])

  return (
    <div className={styles.page}>
      <div className={styles.body}>
        {flash ? (
          <div className={styles.flash}>
            <FlashAlert flash={flash} />
          </div>
        ) : null}

        <section className={styles.greeting} aria-busy={session.isPending || undefined}>
          <h1 className={styles.title} ref={headingRef} tabIndex={-1}>
            {session.data ? (
              `Olá, ${firstName(session.data.user.name)}.`
            ) : session.isPending ? (
              <>
                <span className="sr-only">Carregando sua conta</span>
                <Skeleton width="220px" height="1.75rem" />
              </>
            ) : (
              'Olá.'
            )}
          </h1>

          {session.isPending ? <Skeleton width="180px" height="0.9375rem" /> : null}
          {session.data ? <p className={styles.date}>{formatFullDate(new Date())}</p> : null}
          {/* Falha de sessão NÃO é reportada aqui: quem avisa é a casca, que é
              quem depende da sessão. A saudação simplesmente degrada para
              "Olá." — dois avisos para o mesmo problema é ruído. */}
        </section>

        <div className={styles.grid}>
          <Panel padding="lg">
            <h2 className={styles.panelTitle}>Seu caderno está em branco.</h2>
            <p className={styles.panelText}>
              Ainda não há nada registrado — e ainda não dá para registrar: os lançamentos entram na
              próxima fase do projeto. Por enquanto, sua conta já está criada e com o e-mail
              confirmado, que é o que garante que só você entra aqui.
            </p>
            <div className={styles.preview} aria-hidden="true">
              <Panel tone="sunken" padding="md">
                <LedgerPreview />
              </Panel>
            </div>
          </Panel>

          <Panel
            as="section"
            title="Onde o projeto está"
            footer="Este app roda no seu servidor. Nenhum dado sai daqui."
          >
            <h3 className={styles.listTitle}>Já funciona</h3>
            {/* biome-ignore lint/a11y/noRedundantRoles: o reset zera marcador e recuo justamente por [role=list]; sem ele o Safari descarta a semantica de lista */}
            <ul className={styles.list} role="list">
              {WORKING.map((item) => (
                <li key={item}>
                  <CheckIcon size={16} />
                  <span>{item}</span>
                </li>
              ))}
            </ul>

            <h3 className={styles.listTitle}>A seguir</h3>
            {/* biome-ignore lint/a11y/noRedundantRoles: o reset zera marcador e recuo justamente por [role=list]; sem ele o Safari descarta a semantica de lista */}
            <ul className={styles.list} data-tone="next" role="list">
              {NEXT.map((item) => (
                <li key={item}>
                  <ArrowRightIcon size={16} />
                  <span>{item}</span>
                </li>
              ))}
            </ul>
          </Panel>
        </div>
      </div>
    </div>
  )
}
