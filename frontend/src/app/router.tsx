import {
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  type RouterHistory,
} from '@tanstack/react-router'
import { ConfirmEmailScreen } from '@/features/auth/screens/ConfirmEmailScreen'
import { ForgotPasswordScreen } from '@/features/auth/screens/ForgotPasswordScreen'
import { LoginScreen } from '@/features/auth/screens/LoginScreen'
import { RegisterScreen } from '@/features/auth/screens/RegisterScreen'
import { ResetPasswordScreen } from '@/features/auth/screens/ResetPasswordScreen'
import { HomeScreen } from '@/features/home/components/HomeScreen'
import { NotFoundScreen } from './NotFoundScreen'

function RootComponent() {
  return <Outlet />
}

const rootRoute = createRootRoute({
  component: RootComponent,
  notFoundComponent: NotFoundScreen,
})

/** URL é interface, então é pt-BR. Arquivos e identificadores seguem em inglês.
 *  Rotas declaradas uma a uma de propósito: é assim que o `to` de cada Link e de
 *  cada navigate continua verificado pelo compilador. */
const homeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: HomeScreen,
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
  homeRoute,
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
