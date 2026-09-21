import { useQuery } from '@tanstack/react-query'
import { Link, Outlet, useNavigate, useRouterState } from '@tanstack/react-router'
import { useEffect } from 'react'
import { Alert } from '@/components/Alert/Alert'
import { Button } from '@/components/Button/Button'
import { ChartIcon } from '@/components/icons/ChartIcon'
import { CoinsIcon } from '@/components/icons/CoinsIcon'
import { HouseIcon } from '@/components/icons/HouseIcon'
import { LedgerIcon } from '@/components/icons/LedgerIcon'
import { PromptIcon } from '@/components/icons/PromptIcon'
import { TagsIcon } from '@/components/icons/TagsIcon'
import { TransfersIcon } from '@/components/icons/TransfersIcon'
import { WalletIcon } from '@/components/icons/WalletIcon'
import { Logo } from '@/components/Logo/Logo'
import { MonthNavigator } from '@/components/MonthNavigator/MonthNavigator'
import { ToastProvider } from '@/components/Toast/Toast'
import { UserMenu } from '@/components/UserMenu/UserMenu'
import { isUnauthenticated } from '@/lib/errors'
import { FUSO_PADRAO, mesCorrente, mesDaURL } from '@/lib/month'
import { sessionQueryOptions } from '@/lib/session'
import styles from './AppShell.module.css'

/** Itens de navegação.
 *
 *  A regra é dura (docs/DESIGN.md): **item que não leva a lugar nenhum não é
 *  criado**. Orçamentos e os demais relatórios entram com as suas entregas —
 *  um menu com itens desabilitados prometendo o futuro é desonesto e vira
 *  cliques frustrados.
 *
 *  Ordem: movimento (Lançamentos, Transferências, Investimentos, Relatórios)
 *  antes de cadastro (Contas, Categorias). `Relatórios` aponta para o único relatório
 *  que existe; quando a E6 trouxer os outros, o item passa a `/relatorios`
 *  com sub-navegação, sem mover esta rota (ADR-027e).
 *
 *  Cada item tem DOIS nomes, e a diferença é toda a emenda da barra inferior
 *  (docs/DESIGN.md, 17/09/2026):
 *
 *  - `label` é o nome limpo. Vai para o `aria-label`, e é ele que o leitor de
 *    tela fala e que o comando de voz casa (WCAG 2.5.3).
 *  - `rotulo` é o nome visível, com hífen suave (`\u00AD`) em cada fronteira
 *    silábica. No celular a célula tem ~49px a 320px, e é o hífen que decide
 *    ONDE a palavra quebra: sem ele o navegador partia no meio da sílaba e sem
 *    marca nenhuma ("Transf/erênci/as").
 *
 *  O hífen é escrito como escape, nunca como o caractere invisível: literal no
 *  fonte é armadilha de manutenção. Rótulo que cabe sempre não leva hífen
 *  (`Painel`, `Contas`), e divisão que deixaria menos de 3 caracteres numa
 *  linha é proibida (daí `Ca-te-go-rias`, e nunca `…ri-as`). */
const NAVEGACAO = [
  { to: '/', label: 'Painel', rotulo: 'Painel', Icone: HouseIcon },
  {
    to: '/lancamentos',
    label: 'Lançamentos',
    rotulo: 'Lan\u00ADça\u00ADmen\u00ADtos',
    Icone: LedgerIcon,
  },
  {
    to: '/transferencias',
    label: 'Transferências',
    rotulo: 'Trans\u00ADfe\u00ADrên\u00ADcias',
    Icone: TransfersIcon,
  },
  {
    to: '/investimentos',
    label: 'Investimentos',
    rotulo: 'In\u00ADves\u00ADti\u00ADmen\u00ADtos',
    Icone: CoinsIcon,
  },
  {
    to: '/relatorios/categorias',
    label: 'Relatórios',
    rotulo: 'Re\u00ADla\u00ADtó\u00ADrios',
    Icone: ChartIcon,
  },
  { to: '/contas', label: 'Contas', rotulo: 'Contas', Icone: WalletIcon },
  {
    to: '/categorias',
    label: 'Categorias',
    rotulo: 'Ca\u00ADte\u00ADgo\u00ADrias',
    Icone: TagsIcon,
  },
] as const

/** O grupo **ferramentas** da lateral — o que a pessoa usa de vez em quando,
 *  depois das seções de movimento e de cadastro, separado por um filete.
 *
 *  **Ferramenta não ocupa célula da barra inferior** (docs/DESIGN.md, spec 0010
 *  §10.6(2)). A regra nasceu medida: a célula da barra é
 *  `(viewport − 16 − (N − 1) × 2) / N`, e com N = 8 num viewport de 393px ela
 *  cai a 45,4px — abaixo dos 47px que o rótulo exige. O piso só-ícone passaria
 *  a valer em 390px de content box, e os rótulos de TODA a navegação apagariam
 *  em praticamente todo celular em pé (360, 375, 390, 393, 412 px de viewport).
 *  Um item novo não pode cobrar esse preço dos sete que já estavam lá.
 *
 *  Abaixo de 52rem estes itens somem da barra (`display: none` no `<li>`) e o
 *  destino reaparece no `UserMenu` — que por isso passou a se chamar
 *  `Menu de {nome}`, nas duas faixas: ele deixou de ser só a conta. */
const FERRAMENTAS = [{ to: '/ia', label: 'IA', rotulo: 'IA', Icone: PromptIcon }] as const

/** Casca do app autenticado: cabeçalho, navegação e o seletor de mês.
 *
 *  **O mês vive na URL** (`?mes=2026-09`) e é compartilhado entre as telas —
 *  trocar de mês no painel e ir para contas mantém o mês, e um link colado no
 *  chat abre exatamente o mês que a outra pessoa estava vendo.
 *
 *  O mês padrão é o corrente **no fuso da casa**, nunca no do navegador
 *  (ADR-019): perto da virada do mês, o fuso do navegador mostraria o mês
 *  errado para quem está viajando. */
export function AppShell() {
  const navigate = useNavigate()
  const session = useQuery(sessionQueryOptions)

  const rotaAtual = useRouterState({ select: (state) => state.location.pathname })
  const mesNaURL = useRouterState({
    select: (state) => (state.location.search as { mes?: string }).mes,
  })

  const fuso = session.data?.household.timezone ?? FUSO_PADRAO
  const mes = mesDaURL(mesNaURL, fuso)
  const corrente = mesCorrente(fuso)

  // Sessão vencida não é erro de tela: é hora de voltar para o login.
  useEffect(() => {
    if (session.isError && isUnauthenticated(session.error)) {
      navigate({ to: '/entrar', replace: true })
    }
  }, [session.isError, session.error, navigate])

  function trocarMes(novo: string) {
    // `replace` para que voltar no navegador saia da tela, e não desfaça mês a
    // mês uma navegação que a pessoa fez com as setas.
    //
    // A busca é atualizada em FUNÇÃO da anterior, e não substituída: em
    // `/lancamentos` ela carrega também `conta` e `semCategoria`, e trocar de
    // mês não pode desligar o filtro que a pessoa acabou de ligar. Substituir o
    // objeto inteiro apagaria os dois em silêncio.
    navigate({ to: rotaAtual, search: (anterior) => ({ ...anterior, mes: novo }), replace: true })
  }

  return (
    <ToastProvider>
      <div className={styles.shell}>
        <header className={styles.header}>
          <Link to="/" search={{ mes }} className={styles.brand} aria-label="HomeFinance — início">
            <Logo variant="full" size="sm" />
          </Link>

          <div className={styles.mes}>
            <MonthNavigator
              value={mes}
              onChange={trocarMes}
              {...(mes !== corrente ? { onToday: () => trocarMes(corrente) } : {})}
            />
          </div>

          {session.data ? (
            <div className={styles.conta}>
              <span className={styles.casa}>{session.data.household.name}</span>
              {/* O mês viaja para o menu porque abaixo de 52rem é ELE que
                  hospeda o destino `/ia` — e um link do app que não leva o mês
                  junto quebra o eixo do produto. */}
              <UserMenu name={session.data.user.name} email={session.data.user.email} mes={mes} />
            </div>
          ) : (
            <div className={styles.conta} />
          )}
        </header>

        <div className={styles.corpo}>
          <nav className={styles.nav} aria-label="Seções do aplicativo">
            {/* `role="list"` é OBRIGATÓRIO aqui, e não é enfeite: o reset do
                projeto só zera margem e recuo de `ul[role="list"]`, de propósito
                (quem apaga o marcador declara a lista, senão o VoiceOver perde a
                semântica). Sem ele a `<ul>` ficava com os 15px de margem e os
                40px de recuo do navegador: a barra media 114,6px em vez dos
                84,6px de `--nav-bar-h` — 30px de conteúdo escondido atrás dela —
                e a grade perdia 40px de largura, o que derrubava a célula de
                58,2px para 49px a 375px. */}
            {/* biome-ignore lint/a11y/noRedundantRoles: o papel e reposto de proposito, ver reset.css */}
            <ul className={styles.navLista} role="list">
              {NAVEGACAO.map(({ to, label, rotulo, Icone }) => (
                <li key={to}>
                  <Link
                    to={to}
                    search={{ mes }}
                    className={styles.navItem}
                    // `includeSearch: false` é o que importa aqui: sem ele, o
                    // roteador compara também o `?mes=`, e a seção em que a
                    // pessoa está deixa de ficar marcada assim que ela troca
                    // de mês. O item de menu é sobre a SEÇÃO, não sobre o mês.
                    //
                    // `exact: false` em tudo que não é a raiz: é o que mantém
                    // `Relatórios` marcado em qualquer `/relatorios/*`.
                    activeOptions={{ exact: to === '/', includeSearch: false }}
                    activeProps={{ 'data-current': 'page', 'aria-current': 'page' }}
                    // O nome acessível é o rótulo LIMPO: o hífen suave é
                    // detalhe de composição do texto visível e não pode vazar
                    // para o que é falado nem para o comando de voz.
                    aria-label={label}
                  >
                    <Icone size={20} />
                    <span className={styles.navRotulo}>{rotulo}</span>
                  </Link>
                </li>
              ))}
              {/* O grupo "ferramentas": mesmo `<ul>`, para a barra inferior
                  continuar sendo UMA grade de colunas iguais, e um `<li>`
                  marcado — é ele que leva o filete no desktop e o
                  `display: none` no celular. */}
              {FERRAMENTAS.map(({ to, label, rotulo, Icone }) => (
                <li key={to} className={styles.navFerramenta}>
                  <Link
                    to={to}
                    search={{ mes }}
                    className={styles.navItem}
                    activeOptions={{ exact: false, includeSearch: false }}
                    activeProps={{ 'data-current': 'page', 'aria-current': 'page' }}
                    aria-label={label}
                  >
                    <Icone size={20} />
                    <span className={styles.navRotulo}>{rotulo}</span>
                  </Link>
                </li>
              ))}
            </ul>
          </nav>

          <main className={styles.main}>
            {/* Falha de sessão é reportada AQUI e só aqui. A casca é quem
                depende da sessão (nome da casa, fuso do mês, menu da conta), e
                repetir o mesmo alerta em cada tela daria dois avisos para o
                mesmo problema — e dois botões de "tentar de novo" que fazem a
                mesma coisa. */}
            {session.isError && !isUnauthenticated(session.error) ? (
              <div className={styles.aviso}>
                <Alert
                  tone="error"
                  title="Não foi possível carregar sua conta."
                  action={
                    <Button onClick={() => void session.refetch()} loading={session.isFetching}>
                      Tentar de novo
                    </Button>
                  }
                >
                  Verifique sua conexão e tente de novo.
                </Alert>
              </div>
            ) : null}
            <Outlet />
          </main>
        </div>
      </div>
    </ToastProvider>
  )
}
