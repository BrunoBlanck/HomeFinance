---
name: revisao-seguranca
description: Executa a revisão de segurança obrigatória do HomeFinance sobre o codigo alterado (ou sobre um alvo indicado), usando o checklist de docs/SEGURANCA.md. Obrigatoria antes de considerar qualquer entrega concluida. Para auditoria profunda de todo o projeto, use o workflow auditoria-seguranca.
---

# Revisão de segurança do HomeFinance

Determine o alvo: os argumentos podem indicar arquivos/área específica; sem argumentos, o alvo é tudo que foi alterado na sessão atual (ou `git diff` se houver repositório com mudanças).

1. Lance o agente `revisor-seguranca` informando o alvo exato (lista de arquivos/diff) e o contexto do que a mudança faz. Ele seguirá o roteiro completo de `docs/SEGURANCA.md`.

2. Receba o veredito:
   - **APROVADO** → apresente ao usuário o que foi verificado e os achados baixos/médios (se houver) como recomendações.
   - **BLOQUEADO** → apresente os achados críticos/altos com arquivo:linha e cenário de exploração. Se o usuário estava num fluxo de entrega, as correções devem ser feitas (pelo dev responsável) e a revisão repetida até aprovar.

3. Registre no resumo final: veredito literal, achados por severidade e o que ficou pendente.

Regras:
- Nunca amenize o veredito do revisor nem marque entrega como concluída com veredito BLOQUEADO.
- Se `govulncheck`/`npm audit` estiverem disponíveis para o alvo, os resultados reais fazem parte do relatório.
