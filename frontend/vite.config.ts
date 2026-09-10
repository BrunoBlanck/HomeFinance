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
  },
})
