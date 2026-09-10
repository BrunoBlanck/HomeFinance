# Lições — correções permanentes do usuário (gerais e de processo)

Cada entrada é uma correção ou preferência dita **uma única vez** pelo usuário e vale para sempre a partir da data. Os arquivos de lições são importados pelo `CLAUDE.md` e carregados em **toda** sessão.

Estrutura por área:
- `LICOES.md` (este arquivo) — lições gerais, de processo e fullstack
- `LICOES-BACKEND.md` — lições de backend (Go, API, banco, segurança do servidor)
- `LICOES-FRONTEND.md` — lições de frontend (React, design, UX, acessibilidade)

Regras destes arquivos:
- Registrado pela skill `/aprender` — nunca editar de improviso, sempre pelo fluxo da skill.
- **Nunca** remover ou enfraquecer uma entrada sem ordem explícita do usuário.
- Se uma correção nova contradisser uma antiga, a antiga é atualizada (não duplicada) com nota da mudança e data.
- A lição vive aqui E nos arquivos onde é usada (agente, skill, doc) — a skill propaga.

## Lições registradas

- **[2026-09-09] Correções do usuário viram regra permanente na primeira vez.** Por quê: o usuário não quer repetir nenhuma instrução duas vezes. Como aplicar: ao receber qualquer correção, apontamento de erro ou preferência ("sempre faça X", "nunca faça Y", "não é assim"), invocar a skill `/aprender` imediatamente, sem pedir permissão, e propagar a regra aos arquivos afetados.

- **[2026-09-09] Lições são separadas por área, em arquivos próprios.** Por quê: o usuário quer o aprendizado de backend e de frontend organizados separadamente. Como aplicar: a skill `/aprender` grava lição de backend em `LICOES-BACKEND.md`, de frontend em `LICOES-FRONTEND.md`, e gerais/processo/fullstack neste arquivo; lição que afeta as duas áreas é registrada nos dois arquivos de área.
