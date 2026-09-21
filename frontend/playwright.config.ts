import { defineConfig, devices } from '@playwright/test'
import { API_URL, ARQUIVO_SESSAO, BASE_URL, PORTA_WEB } from './e2e/support/ambiente'

/** Testes de ponta a ponta contra a pilha real: API Go + Vite.
 *
 *  A API sobe no `globalSetup` (e não como `webServer`) porque o teste precisa
 *  ler o stdout dela — é de lá que sai o código de 6 dígitos do cadastro. O
 *  Vite fica como `webServer` normal, apontando o proxy para a API de teste. */
const SETUP = /\.setup\.ts$/
const AUTENTICACAO = /autenticacao\.spec\.ts$/

export default defineConfig({
  testDir: './e2e',
  // Sem `testMatch` global: cada projeto declara o que roda. Um filtro aqui
  // esconderia o `sessao.setup.ts` do projeto de setup.

  globalSetup: './e2e/support/global-setup.ts',
  globalTeardown: './e2e/support/global-teardown.ts',

  // O fluxo de autenticação é sequencial por natureza (um cadastro, um código,
  // uma sessão) e os testes de aplicação compartilham UMA casa, com specs em
  // `mode: 'serial'` que contam com a ordem. Paralelizar produziria
  // interferência entre specs — e, sem o perfil de limites de teste, também
  // 429 em vez de informação.
  fullyParallel: false,
  workers: 1,

  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['github'], ['list']] : [['list']],

  timeout: 60_000,
  expect: { timeout: 10_000 },

  use: {
    baseURL: BASE_URL,
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
    locale: 'pt-BR',
    timezoneId: 'America/Sao_Paulo',
  },

  projects: [
    // Um cadastro só, guardado em disco. `POST /auth/register` é limitado a 5
    // por hora por IP, e esse teto vale em PRODUÇÃO: a suíte não o afrouxa,
    // ela roda com outro perfil de limites (`RATE_LIMITS_PROFILE=test`, ligado
    // no `global-setup.ts` e recusado no boot em produção — docs/SEGURANCA.md
    // §5.2). Compartilhar a casa virou escolha de teste, não imposição — o
    // porquê está em `e2e/sessao.setup.ts`.
    {
      name: 'setup',
      testMatch: SETUP,
      use: { ...devices['Desktop Chrome'] },
    },
    // A autenticacao roda SEM sessao salva: cadastrar e entrar e o assunto
    // dela, e herdar cookies apagaria o teste.
    {
      name: 'autenticacao',
      testMatch: AUTENTICACAO,
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'aplicacao',
      testIgnore: [AUTENTICACAO, SETUP],
      dependencies: ['setup'],
      use: { ...devices['Desktop Chrome'], storageState: ARQUIVO_SESSAO },
    },
  ],

  webServer: {
    // `--strictPort` para falhar alto em vez de subir numa porta que o teste
    // não conhece e depois errar com "página não carregou".
    //
    // `--host 127.0.0.1` não é decoração: o padrão do Vite é `localhost`, que
    // no Node 17+ resolve para `::1` primeiro, e aí o servidor escuta só em
    // IPv6 enquanto o Playwright bate em `127.0.0.1` e espera até estourar o
    // prazo. Fixar IPv4 nas duas pontas (aqui e no `HTTP_ADDR` da API) elimina
    // a ambiguidade em vez de contorná-la.
    command: `npx vite --port ${PORTA_WEB} --strictPort --host 127.0.0.1`,
    url: BASE_URL,
    reuseExistingServer: false,
    timeout: 120_000,
    env: { VITE_API_PROXY_TARGET: API_URL },
  },
})
