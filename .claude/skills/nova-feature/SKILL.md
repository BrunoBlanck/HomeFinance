---
name: nova-feature
description: Orquestra o ciclo completo de uma feature do HomeFinance com a equipe de IA - planejamento pelo arquiteto, implementação pelos devs, testes pelo QA e revisão de segurança obrigatória no final. Use para qualquer feature nova ou mudança relevante.
---

# Nova feature — ciclo completo com a equipe de IA

Você vai orquestrar a equipe de agentes do projeto. A feature pedida pelo usuário está nos argumentos (se não estiver clara, pergunte antes de começar).

## Etapas (nesta ordem, sem pular nenhuma)

1. **Planejar** — lance o agente `arquiteto` com a descrição da feature. Ele devolve escopo, contrato de API, modelo de dados, análise de segurança prévia, divisão de tarefas e critérios de aceite. Apresente o plano ao usuário de forma resumida antes de implementar; se a feature for grande ou ambígua, confirme o plano com o usuário.

2. **Implementar** — despache as tarefas do plano para os agentes certos:
   - Migração/modelo de dados → `arquiteto-dados` (sempre primeiro, se houver)
   - API Go → `dev-backend-go`
   - Telas novas → direção do `designer-ui` primeiro, depois `dev-frontend-react`
   - Tarefas independentes podem rodar em paralelo (backend e frontend após o contrato definido).
   Passe a cada agente o trecho relevante do plano e os critérios de aceite.

3. **Testar** — lance `qa-testes` com os critérios de aceite. Se ele reportar bugs, devolva ao dev responsável e repita até a suíte passar de verdade.

4. **Revisar segurança (OBRIGATÓRIO)** — lance `revisor-seguranca` sobre tudo que foi alterado. 
   - Veredito BLOQUEADO → devolva os achados ao dev responsável, corrija e revise de novo. Repita até APROVADO.
   - A feature só está concluída com veredito APROVADO.

5. **Encerrar** — resuma para o usuário: o que foi entregue, resultado real dos testes, veredito da revisão de segurança e pendências (se houver).

## Regras

- Nunca declare a feature concluída sem a etapa 4 aprovada — esta é a regra número um do projeto.
- Reporte resultados reais (saída de testes, veredito literal), nunca presumidos.
- Se qualquer agente propuser fugir dos padrões de `CLAUDE.md`/`docs/`, a mudança de padrão precisa passar pelo `arquiteto` e virar ADR.
