import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  type RouterHistory,
} from '@tanstack/react-router'
import { AccountsScreen } from '@/features/accounts/screens/AccountsScreen'
import { ConfirmEmailScreen } from '@/features/auth/screens/ConfirmEmailScreen'
import { ForgotPasswordScreen } from '@/features/auth/screens/ForgotPasswordScreen'
import { LoginScreen } from '@/features/auth/screens/LoginScreen'
import { RegisterScreen } from '@/features/auth/screens/RegisterScreen'
import { ResetPasswordScreen } from '@/features/auth/screens/ResetPasswordScreen'
import { CategoriesScreen } from '@/features/categories/screens/CategoriesScreen'
import { HomeScreen } from '@/features/home/components/HomeScreen'
import { ImportResultScreen } from '@/features/import/screens/ImportResultScreen'
import { ImportReviewScreen } from '@/features/import/screens/ImportReviewScreen'
import { ImportUploadScreen } from '@/features/import/screens/ImportUploadScreen'
import { InvestmentsScreen } from '@/features/investments/screens/InvestmentsScreen'
import { CategoryReportScreen } from '@/features/reports/screens/CategoryReportScreen'
import { TransactionsScreen } from '@/features/transactions/screens/TransactionsScreen'
import { TransfersScreen } from '@/features/transfers/screens/TransfersScreen'
import { AppShell } from './AppShell'
import { NotFoundScreen } from './NotFoundScreen'
import { validarBusca } from './search'

function RootComponent() {
  return <Outlet />
}

const rootRoute = createRootRoute({
  component: RootComponent,
  notFoundComponent: NotFoundScreen,
})

/** A busca compartilhada por todas as telas autenticadas — `mes`, `conta`,
 *  `semCategoria` e `natureza` — é validada em `./search.ts`, uma vez e num
 *  lugar só.
 *
 *  Fica lá, e não aqui, para poder ser testada sem montar o roteador: é a
 *  fronteira entre a URL (que a pessoa edita) e as queries da API, e essa
 *  fronteira merece teste próprio. */

/** Rota de LAYOUT, sem caminho próprio: ela existe só para que a casca (o
 *  cabeçalho, a navegação e o seletor de mês) seja montada uma vez e sobreviva
 *  à troca de tela. Sem isso, navegar entre painel e contas remontaria o
 *  cabeçalho e perderia o foco e o estado do menu. */
const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'app',
  component: AppShell,
  validateSearch: validarBusca,
})

/** URL é interface, então é pt-BR. Arquivos e identificadores seguem em inglês.
 *  Rotas declaradas uma a uma de propósito: é assim que o `to` de cada Link e de
 *  cada navigate continua verificado pelo compilador. */
const homeRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/',
  component: HomeScreen,
})

const accountsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/contas',
  component: AccountsScreen,
})

const categoriesRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/categorias',
  component: CategoriesScreen,
})

const transactionsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/lancamentos',
  component: TransactionsScreen,
})

/** Transferências entre as contas da casa (spec 0005 §4.4). Compartilha a
 *  busca da casca: `mes`, `conta` e, só aqui, `contraparte`. */
const transfersRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/transferencias',
  component: TransfersScreen,
})

/** Investimentos: aportes e resgates do mês, do ano e dos últimos 12 meses
 *  (spec 0006 §3.4). Compartilha a busca da casca — só `mes`: a tela não tem
 *  filtro próprio, e o eixo dela é o mês. */
const investmentsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/investimentos',
  component: InvestmentsScreen,
})

/** Relatório por categoria (ADR-027e). A URL nasce definitiva: quando a E6
 *  trouxer os demais relatórios, eles entram ao lado, sob `/relatorios/*`, sem
 *  mover esta. Compartilha a busca da casca (`mes`) e, só aqui, `natureza`. */
const categoryReportRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/relatorios/categorias',
  component: CategoryReportScreen,
})

/** A importação é **uma rota por passo**, e não um passo guardado em estado.
 *
 *  O que se ganha: recarregar a página no meio da revisão de 68 linhas não
 *  perde o trabalho, o botão "voltar" do navegador faz o que promete, e um link
 *  para a revisão continua abrindo a revisão. Um wizard em `useState` perde as
 *  três coisas no primeiro F5 — e aqui o F5 acontece justamente quando a pessoa
 *  está insegura sobre o que vai gravar.
 *
 *  O `importId` é id de recurso opaco, do mesmo tipo de `/contas/{id}`: não é
 *  dado sensível (a regra de `docs/DESIGN.md` fala de e-mail e código de
 *  verificação) e o backend responde 404 para lote de outra casa. */
const importRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/importar',
  component: ImportUploadScreen,
})

const importReviewRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/importar/$importId/revisar',
  component: ImportReviewScreen,
})

const importResultRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/importar/$importId/resultado',
  component: ImportResultScreen,
})

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/entrar',
  component: LoginScreen,
})

const registerRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/criar-conta',
  component: RegisterScreen,
})

const confirmEmailRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/confirmar-email',
  component: ConfirmEmailScreen,
})

const forgotPasswordRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/esqueci-minha-senha',
  component: ForgotPasswordScreen,
})

const resetPasswordRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/redefinir-senha',
  component: ResetPasswordScreen,
})

const routeTree = rootRoute.addChildren([
  appRoute.addChildren([
    homeRoute,
    accountsRoute,
    categoriesRoute,
    transactionsRoute,
    transfersRoute,
    investmentsRoute,
    categoryReportRoute,
    importRoute,
    importReviewRoute,
    importResultRoute,
  ]),
  loginRoute,
  registerRoute,
  confirmEmailRoute,
  forgotPasswordRoute,
  resetPasswordRoute,
])

/** Fábrica para os testes poderem montar a aplicação com histórico em memória. */
export function createAppRouter(history?: RouterHistory) {
  return createRouter({
    routeTree,
    // View Transitions do navegador — nunca o <ViewTransition> experimental do
    // React. A animação e o corte sob prefers-reduced-motion moram no CSS.
    defaultViewTransition: true,
    defaultPreload: 'intent',
    scrollRestoration: true,
    ...(history ? { history } : {}),
  })
}

export const router = createAppRouter()

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
