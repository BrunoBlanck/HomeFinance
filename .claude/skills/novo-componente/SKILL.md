---
name: novo-componente
description: Cria um componente React unico do HomeFinance seguindo o design system proprio (docs/DESIGN.md) - sem bibliotecas de componentes, com tokens, acessibilidade e testes. Use quando o pedido for criar ou redesenhar um componente ou tela do frontend.
---

# Novo componente do frontend

Os argumentos descrevem o componente/tela. Antes de escrever qualquer código:

1. **Carregue a skill `frontend-design`** — obrigatório para qualquer trabalho visual neste projeto.
2. Leia `docs/DESIGN.md` e os tokens em `frontend/src/styles/tokens.css` (quando existirem), além de componentes parecidos já criados — consistência com o sistema vem antes de criatividade nova.

## Passos

1. **Direção** — para tela nova ou componente de identidade forte, lance `designer-ui` primeiro para definir layout, hierarquia e microinterações. Para componente utilitário simples, siga direto os tokens.

2. **Implementação** — lance `dev-frontend-react` (ou siga exatamente o prompt dele):
   - Componente em `frontend/src/components/` (base) ou `frontend/src/features/<area>/` (específico), com `.module.css` ao lado usando apenas tokens (nenhuma cor/medida hardcoded).
   - TypeScript estrito, props tipadas, textos em pt-BR, moeda via `Intl.NumberFormat('pt-BR')`.
   - Estados completos: vazio, carregando, erro — desenhados, não improvisados.
   - Elementos nativos primeiro: modal = `<dialog>` (com `aria-labelledby`), menu/dropdown = Popover API, combobox/tabs conforme WAI-ARIA APG.

3. **Checklist anti-"cara de IA"** (rejeite e refaça se violar):
   - [ ] Nenhuma biblioteca de componentes instalada ou copiada
   - [ ] Nenhum gradiente roxo/azul genérico, emoji na UI ou card genérico com sombra pesada
   - [ ] Toda cor/espaçamento/raio vem de token
   - [ ] Acessível: semântica correta, foco visível, teclado funciona, contraste AA

4. **Verificar** — testes (Vitest + Testing Library) para componentes com lógica; `npm run build` com saída real. Se o app estiver rodável, valide visualmente (skill `run`/Playwright) antes de concluir.
