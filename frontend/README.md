# Frontend — App React do HomeFinance

Ainda não inicializado — será criado na **Fase 3** do [roadmap](../docs/ROADMAP.md) com **Vite 8 + React 19 + TypeScript estrito** (`noUncheckedIndexedAccess`).

## Layout planejado — feature-based (ADR-007, ver docs/ARQUITETURA.md)

```
frontend/src/
  app/            ← TanStack Router (árvore de rotas), providers, config
  features/       ← auth/ dashboard/ transactions/ bills/ budgets/ reports/
  │    cada feature: api/ (queryOptions), components/, hooks/, schemas/ (Zod 4)
  components/     ← design system próprio (Button, Input, Table, Modal…)
    icons/        ← ícones SVG únicos do projeto (traço 1.5px, currentColor)
  styles/         ← tokens.css (OKLCH, @layer), reset, tipografia
  lib/  hooks/    ← api client (tipos gerados do OpenAPI), formatação pt-BR
```

## Regras da casa

- **Nenhuma biblioteca de componentes** (MUI, shadcn, Ant, Chakra, Bootstrap) e nenhum Tailwind. Todo componente é único deste projeto.
- Arquitetura unidirecional `lib/components → features → app`; **imports entre features e barrel files são proibidos**.
- Rotas e search params tipados (TanStack Router + Zod); estado do servidor com TanStack Query v5 (`queryOptions` co-locados); formulários com react-hook-form + Zod 4.
- Cadastro e recuperação de senha passam **obrigatoriamente** por uma etapa de **código de 6 dígitos** enviado por e-mail (ADR-009) — componente `CodeInput`, com `autocomplete="one-time-code"`, colar o código inteiro, reenvio com contador; a tela nunca revela se o e-mail existe.
- Identidade visual: `docs/DESIGN.md` é a fonte de verdade; lista de rejeição anti-"cara de IA" reprova revisão. Antes de trabalho visual: skill `frontend-design`; telas novas passam pelo agente `designer-ui`.
- Acessibilidade nativa primeiro: `<dialog>` para modais, Popover API para menus, WAI-ARIA APG no resto.
- Lint/format: **Biome**; testes com Vitest 4 (+ Browser Mode) e Playwright E2E.
- Segurança: nada de `dangerouslySetInnerHTML` com dado de usuário; tokens nunca em `localStorage` (cookies HttpOnly — ver `docs/SEGURANCA.md`).
