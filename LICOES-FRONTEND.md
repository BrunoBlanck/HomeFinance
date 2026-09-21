# Lições de frontend — correções permanentes do usuário

Correções do usuário sobre frontend (React, design, UX, acessibilidade). Ditas uma única vez, valem para sempre. Regras de manutenção e estrutura: ver `LICOES.md`. Registro somente via skill `/aprender`.

## Lições registradas

*(nenhuma lição de frontend registrada ainda)*

- **[2026-09-09] Todo fluxo sensível de conta tem uma tela de código de 6 dígitos.** Por quê: o usuário definiu que cadastro e recuperação de senha se confirmam com um código numérico enviado por e-mail e digitado no site. Como aplicar: cadastro e "esqueci minha senha" passam obrigatoriamente por uma etapa de verificação com um campo de 6 dígitos (`inputMode="numeric"`, `autocomplete="one-time-code"`, colar o código inteiro funciona, foco automático, erro e reenvio com contador visíveis); a tela nunca revela se o e-mail existe e nunca exibe o código. Componente base: `CodeInput` em `frontend/src/components/`.

- **[2026-09-18] No painel (home), investimento é UM número: o líquido = aportes − resgates, com sinal, podendo ser negativo.** Por quê: o usuário decidiu em 18/09/2026 que o resumo do mês do painel responde "quanto ficou investido neste mês", e não "quanto entrou e quanto saiu" — dois números lado a lado ali não respondem à pergunta que ele faz ao abrir o app. Como aplicar: o resumo do mês exibe o líquido com **sinal explícito** (inclusive negativo, quando o resgate supera o aporte), nunca só por cor, e o número vem **pronto do servidor** — subtrair no cliente criaria duas fontes para o mesmo número (§7.2 do PLANOS.md). ⚠️ A regra **não** se estende à tela `/investimentos`: ali continua valendo, por decisão do usuário na mesma data, a spec 0006 §3.4.2 e o `docs/DESIGN.md` §E7(d) — "sem saldo, sem líquido, sem patrimônio", zero número derivado —, e o schema `InvestmentTotals` do contrato segue sem líquido. As duas telas respondem a perguntas diferentes, e é essa diferença que autoriza números diferentes.
