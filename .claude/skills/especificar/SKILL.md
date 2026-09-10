---
name: especificar
description: Produz uma especificacao curta e versionada (docs/specs/) antes de implementar - entrevista o usuario, registra escopo, contrato e criterios de aceite. Use ANTES de /nova-feature quando a feature for grande, ambigua ou tocar dados financeiros; nao use para ajustes pequenos e bem definidos.
---

# Especificar — spec-driven development (versão leve)

Os argumentos descrevem a ideia da feature. O objetivo é transformar uma ideia vaga em uma spec curta e verificável ANTES de qualquer código — specs reduzem retrabalho e são revisáveis no PR.

## Passos

1. **Entrevistar** — faça ao usuário as perguntas mínimas que mudam o design (use AskUserQuestion quando fizer sentido): quem usa, qual problema resolve, o que fica de fora, casos de erro esperados, impacto em dados financeiros existentes. Não pergunte o que dá para decidir pelos padrões do projeto.

2. **Escrever a spec** em `docs/specs/NNN-nome-da-feature.md` (numeração sequencial), com no máximo ~1 página:
   - **Problema** — uma frase.
   - **Escopo** — o que entra; **Fora de escopo** — explícito.
   - **Comportamento** — fluxos principais em passos numerados, incluindo estados de erro/vazio.
   - **Contrato** — rotas/campos novos (rascunho para o `arquiteto` refinar no OpenAPI).
   - **Dados** — tabelas/colunas afetadas (rascunho para o `arquiteto-dados`).
   - **Segurança** — o que esta feature muda no modelo de ameaças (autorização por casa, novos dados sensíveis).
   - **Critérios de aceite** — lista verificável, incluindo casos de abuso.

3. **Validar** — apresente a spec ao usuário para ajuste/aprovação. Spec aprovada é imutável na essência; mudanças de escopo geram revisão explícita no arquivo.

4. **Encaminhar** — sugira seguir com `/nova-feature` passando a spec como entrada do `arquiteto`.

## Regras

- Uma spec por feature; arquivos de spec nunca são apagados (histórico de decisões).
- Critérios de aceite viram os testes do `qa-testes` — escreva-os testáveis.
- Não implemente nada nesta skill; ela termina na spec aprovada.
