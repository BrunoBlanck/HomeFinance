# Lições de frontend — correções permanentes do usuário

Correções do usuário sobre frontend (React, design, UX, acessibilidade). Ditas uma única vez, valem para sempre. Regras de manutenção e estrutura: ver `LICOES.md`. Registro somente via skill `/aprender`.

## Lições registradas

*(nenhuma lição de frontend registrada ainda)*

- **[2026-09-09] Todo fluxo sensível de conta tem uma tela de código de 6 dígitos.** Por quê: o usuário definiu que cadastro e recuperação de senha se confirmam com um código numérico enviado por e-mail e digitado no site. Como aplicar: cadastro e "esqueci minha senha" passam obrigatoriamente por uma etapa de verificação com um campo de 6 dígitos (`inputMode="numeric"`, `autocomplete="one-time-code"`, colar o código inteiro funciona, foco automático, erro e reenvio com contador visíveis); a tela nunca revela se o e-mail existe e nunca exibe o código. Componente base: `CodeInput` em `frontend/src/components/`.
