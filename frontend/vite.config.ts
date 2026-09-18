import { fileURLToPath, URL } from 'node:url'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// A API roda em outra porta em desenvolvimento; o proxy mantém o frontend na
// mesma origem da API, que é o que permite os cookies HttpOnly de sessão
// funcionarem sem CORS com credenciais (ver docs/SEGURANCA.md).
//
// O alvo acompanha o HTTP_ADDR do backend: o padrão do código Go é :8080, mas
// se você mudar a porta no .env, aponte o proxy com VITE_API_PROXY_TARGET em vez
// de editar este arquivo.
const apiTarget = process.env.VITE_API_PROXY_TARGET ?? 'http://localhost:8085'
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: apiTarget,
        changeOrigin: false,
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: true,
    // Os arquivos de e2e/ sao do Playwright: rodam em navegador de verdade,
    // contra a API Go. Se o Vitest tentasse carrega-los, ele quebraria no
    // import de @playwright/test e o erro nao explicaria por que.
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    exclude: ['node_modules/**', 'e2e/**'],
    // O prazo padrao do Vitest e 5 s, medido numa maquina com CPU sobrando.
    // Aqui os testes de tela usam `userEvent`, que avanca RELOGIO REAL entre
    // as teclas, e rodam em paralelo num jsdom por arquivo: com a maquina
    // ocupada (a suite Go, o Playwright, um build), 5 s viram pouco e a falha
    // sai como "Test timed out in 5000ms" em arquivos que nao tem nada a ver
    // um com o outro.
    //
    // Medido em 17/09/2026 pelo QA: 3 execucoes seguidas da suite completa com
    // outra suite rodando ao lado deram 2 falhas (RegisterScreen +
    // ResetPasswordScreen numa, ImportUploadScreen noutra), TODAS por prazo, e
    // a execucao seguinte com a maquina livre passou 640/640. Nao e defeito de
    // codigo nem de teste: e contencao.
    //
    // O prazo existe para pegar teste TRAVADO, nao para ser orcamento de
    // desempenho: 15 s continuam pegando um `await` que nunca resolve e param
    // de transformar contencao em vermelho. Se um caso passar a levar 15 s de
    // verdade, o problema e outro e o prazo tem de continuar falhando.
    testTimeout: 15_000,
    hookTimeout: 15_000,
  },
})
