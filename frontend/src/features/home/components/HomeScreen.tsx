import { useQuery } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect } from 'react'
import { validarBusca } from '@/app/search'
import { FlashAlert } from '@/components/Alert/FlashAlert'
import { Skeleton } from '@/components/Skeleton/Skeleton'
import { isUnauthenticated } from '@/lib/errors'
import { useFocoNoTitulo } from '@/lib/focus'
import { firstName, formatFullDate } from '@/lib/format'
import { FUSO_PADRAO, mesDaURL } from '@/lib/month'
import { sessionQueryOptions } from '@/lib/session'
import styles from './HomeScreen.module.css'
import { MonthSummaryBand } from './MonthSummaryBand'

/** O painel (`/`) — a primeira tela de quem entra no app (spec 0008).
 *
 *  Duas coisas e nada mais: a saudação, que diz **hoje**, e a faixa de resumo,
 *  que diz **o mês selecionado**. O `<h2>` da faixa é o que separa as duas.
 *
 *  **O resto da tela é vazio de propósito.** Saldo por conta, vencimentos e top
 *  categorias são a E4 completa e estão fora desta entrega: nenhuma moldura
 *  vazia, nenhum painel com "em breve", nenhum esqueleto permanente. A regra da
 *  casca — item que não leva a lugar nenhum não é criado — vale igual para
 *  bloco de tela.
 *
 *  **Sem seletor de mês próprio:** o mês é da casca (`?mes=` na URL), e um
 *  segundo controle para a mesma coisa seria dois lugares para discordar. */
export function HomeScreen() {
  const navigate = useNavigate()
  const flash = useRouterState({ select: (state) => state.location.state.flash })
  // Foco no <h1> na entrada da rota, igual às outras telas (docs/DESIGN.md).
  const tituloRef = useFocoNoTitulo()

  // Validada aqui, e não lida crua de `location.search`: é a fronteira entre a
  // URL (que a pessoa edita) e a query da API.
  const buscaBruta = useRouterState({ select: (estado) => estado.location.search })
  const busca = validarBusca(buscaBruta as Record<string, unknown>)

  const session = useQuery(sessionQueryOptions)
  // O mês corrente é o da CASA, nunca o do navegador (ADR-019): às 21h de 30 de
  // setembro em São Paulo, o navegador de quem está em Lisboa já virou outubro.
  const fuso = session.data?.household.timezone ?? FUSO_PADRAO
  const mes = mesDaURL(busca.mes, fuso)

  useEffect(() => {
    document.title = 'Painel · HomeFinance'
  }, [])

  // Sessão vencida não é erro de tela: é hora de voltar para o login.
  useEffect(() => {
    if (session.isError && isUnauthenticated(session.error)) {
      navigate({ to: '/entrar', replace: true })
    }
  }, [session.isError, session.error, navigate])

  return (
    <div className={styles.pagina}>
      {flash ? <FlashAlert flash={flash} /> : null}

      <section aria-busy={session.isPending || undefined}>
        <h1 className={styles.titulo} ref={tituloRef} tabIndex={-1}>
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

        {/* A data é a de HOJE, e fica: a faixa abaixo fala do mês selecionado,
            que pode ser março. Falha de sessão NÃO é reportada aqui — quem
            avisa é a casca, que é quem depende dela. A saudação simplesmente
            degrada para "Olá.". */}
        {session.data ? (
          <p className={styles.apoioDoTitulo}>{formatFullDate(new Date())}</p>
        ) : session.isPending ? (
          <p className={styles.apoioDoTitulo}>
            <Skeleton width="180px" height="0.9375rem" />
          </p>
        ) : null}
      </section>

      <MonthSummaryBand mes={mes} />
    </div>
  )
}
