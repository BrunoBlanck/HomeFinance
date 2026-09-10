// Este import vem PRIMEIRO de propósito (o Biome preserva imports de efeito
// colateral no topo, e o teste em src/styles/layers.test.ts vigia isso): é ele
// que registra `@layer reset, tokens, base, components, utilities`.
// Se qualquer *.module.css for avaliado antes, o navegador cria a camada
// `components` primeiro, `reset` e `base` passam a vencer os componentes, e o
// `font: inherit` do reset apaga a tipografia de todo campo — verificado no
// navegador em 09/09/2026.
import '@/styles/index.css'

import { RouterProvider } from '@tanstack/react-router'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { AppProviders } from '@/app/providers'
import { router } from '@/app/router'
import { applyThemePreference, readThemePreference } from '@/lib/theme'

// A CSP do backend proíbe <script> inline no index.html, então o tema é aplicado
// aqui — o custo é um único frame de flash na troca manual, e ele é aceito.
applyThemePreference(readThemePreference())

const rootElement = document.getElementById('root')
if (!rootElement) throw new Error('Elemento #root não encontrado')

createRoot(rootElement).render(
  <StrictMode>
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>
  </StrictMode>,
)
