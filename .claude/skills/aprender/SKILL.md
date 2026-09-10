---
name: aprender
description: Registra PERMANENTEMENTE uma correcao ou preferencia do usuario para que nunca precise ser repetida - grava em LICOES.md, propaga aos agentes/skills/docs afetados e salva na memoria. Invoque IMEDIATAMENTE (sem pedir permissao) sempre que o usuario corrigir um comportamento, apontar um erro ou expressar preferencia ("sempre faca X", "nunca faca Y", "nao era assim", "eu ja disse"). Nao use para pedidos normais de tarefa.
---

# Aprender — correção vira regra permanente

O usuário corrigiu algo ou expressou uma preferência (nos argumentos ou na conversa recente). A partir de agora esse erro nunca mais pode se repetir. Siga os passos, todos.

## Passos

1. **Extrair a regra geral** — generalize a correção específica para a regra reutilizável por trás dela. Ex.: "esse botão ficou roxo" → regra: "nunca usar roxo; cores só dos tokens". Se a intenção for ambígua a ponto de gerar regras opostas, confirme com o usuário (AskUserQuestion) antes de gravar.

2. **Checar conflito** — procure a regra em `LICOES.md`, `AGENTS.md`, `CLAUDE.md` e `docs/`:
   - Já existe igual → a regra falhou em ser aplicada; reforce-a no lugar onde deveria ter agido (agente/skill) em vez de duplicar.
   - Contradiz regra existente → a correção NOVA vence, mas confirme com o usuário mostrando as duas ("você definiu X antes; a partir de agora vale Y?"). Depois atualize a regra antiga em TODOS os lugares — nunca deixe as duas coexistindo.

3. **Registrar no arquivo da área certa** — lição de backend (Go, API, banco, segurança do servidor) → `LICOES-BACKEND.md`; lição de frontend (React, design, UX, acessibilidade) → `LICOES-FRONTEND.md`; geral/processo/fullstack → `LICOES.md` (se afetar as duas áreas, registre nos dois arquivos de área). Formato da entrada:
   `- **[AAAA-MM-DD] Regra em uma frase imperativa.** Por quê: motivo dito/inferido. Como aplicar: onde e como a regra age.`

4. **Propagar** — a lição precisa viver onde é usada, não só na lista:
   - Afeta um agente → edite o prompt do agente em `.claude/agents/`.
   - Afeta uma skill → edite a skill.
   - Afeta decisão de arquitetura/design/segurança → atualize o doc correspondente (mudança estrutural vira ADR em docs/ARQUITETURA.md).
   - Precisa ser GARANTIDO (não só lembrado) → proponha hook/permissão no `.claude/settings.json` (carregue a skill `update-config`).

5. **Salvar na memória persistente** — grave/atualize um arquivo de memória (type: feedback) com a regra, para valer também fora deste repositório quando fizer sentido.

6. **Confirmar** — mostre ao usuário o texto exato registrado e a lista de arquivos alterados, em uma linha cada.

## Regras

- Nunca registre a instância específica sem a regra geral — é a regra que evita a repetição.
- Nunca apague lição sem ordem explícita; contradições se resolvem por atualização datada.
- Este fluxo roda inteiro na hora da correção — nunca "depois".
