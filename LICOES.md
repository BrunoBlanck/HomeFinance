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

- **[2026-09-18] Conteúdo pré-preenchido (semente, catálogo, lista de fábrica) nasce ENXUTO — o usuário escolhe a versão curta, não a completa.** Por quê: em 18/09/2026, diante da semente de categorias, o usuário recusou a taxonomia completa (66 subcategorias) e escolheu a enxuta (~3 por grupo, 41 folhas) — contra a recomendação do plano —, aceitando de olhos abertos o preço medido (~20 descrições do corpus deixam de ter sugestão de fábrica). Ele prefere pouca coisa útil a muita coisa exaustiva: o que sobra na tela custa atenção todo dia, e o que falta ele cadastra quando precisar. Como aplicar: ao propor qualquer conjunto de fábrica — semente de categorias, lista de opções, presets, exemplos, itens de menu —, propor a versão enxuta como padrão e apresentar a completa só como alternativa, sempre com o custo do corte MEDIDO (o que se perde, nominalmente), nunca estimado; quando o volume for dúvida legítima, perguntar antes de implementar. Registrado no ADR-033 (decisão (e)) para a semente de categorias.

- **[2026-09-18] Número de ADR e de spec é escolhido NA HORA DA ESCRITA, relendo o arquivo — nunca reservado no plano.** Por quê: em 18/09/2026 o número ADR-032 foi reservado durante o planejamento da semente de categorias e, quando a implementação chegou, outra sessão já o havia gravado com o recorte crédito/débito do relatório — o conflito custou uma renumeração em cadeia (código, plano, lição e memória). O projeto roda várias sessões em paralelo sobre os mesmos docs, então número reservado é número que envelhece. A regra já tinha sido aprendida na entrega E4a e estava só na memória; por não estar aqui, falhou de novo. Como aplicar: plano, spec e prompt de agente falam em "o próximo ADR" ou "ADR-NNN", nunca num número concreto; quem escreve o ADR relê `docs/ARQUITETURA.md` (e `docs/specs/`) no instante da escrita, toma o primeiro número livre e só então preenche as referências. O mesmo vale para numeração de spec. Em arquivo compartilhado (`docs/ARQUITETURA.md`, `docs/ROADMAP.md`, specs), acrescente em bloco novo no fim e confira o `git diff` antes de devolver: nada fora do seu bloco pode ter mudado.
