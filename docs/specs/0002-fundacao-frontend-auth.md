# Spec 0002 — Fundação do frontend: design system, telas de autenticação e Home

**Data:** 09/09/2026 · **Fase:** 3 · **Autor:** agente `designer-ui`
**Depende de:** Spec 0001 (contrato da API) · `docs/DESIGN.md` (emenda de 09/09/2026 já gravada)
**Emendada em 09/09/2026** — §3.1 nova e §6 revista, para acompanhar o `registrationToken` do ADR-014 (fechamento do *pre-hijacking* de conta).

> Normativa. O `dev-frontend-react` implementa isto sem inventar nada; divergência necessária se reporta, não se resolve em silêncio.

---

## 1. Estilos base

`src/styles/` com `@layer reset, tokens, base, components, utilities;`

**`tokens.css`** — valores em OKLCH (hex de referência em `docs/DESIGN.md`):

```
Claro                                   Escuro
--bg              oklch(97.0% 0.007 88.6)   oklch(19.7% 0.007 78.2)
--surface         oklch(100%  0     89.9)   oklch(23.7% 0.009 75.2)
--surface-sunken  oklch(95.0% 0.010 87.5)   oklch(21.6% 0.007 67.4)
--border          oklch(90.7% 0.014 88.7)   oklch(32.0% 0.014 76.4)
--border-strong   oklch(59.2% 0.020 81.3)   oklch(55.6% 0.021 78.1)
--ink             oklch(25.8% 0.010 80.6)   oklch(93.5% 0.012 84.6)
--ink-muted       oklch(51.7% 0.019 79.3)   oklch(69.5% 0.020 83.1)
--accent          oklch(43.9% 0.071 172.3)  oklch(67.2% 0.097 170.4)
--on-solid        oklch(100%  0     89.9)   oklch(19.7% 0.007 78.2)
--income          oklch(52.3% 0.135 144.2)  oklch(71.8% 0.142 144.9)
--expense         oklch(50.1% 0.178 28.7)   oklch(69.1% 0.161 26.6)
--warning         oklch(56.0% 0.117 77.5)   oklch(74.6% 0.132 79.3)
--warning-ink     oklch(47.1% 0.099 76.3)   oklch(74.6% 0.132 79.3)
--danger: var(--expense)
```

```
--font-display: "Fraunces", Georgia, "Times New Roman", serif
--font-ui: "Public Sans", system-ui, "Segoe UI", sans-serif
--text-13:.8125rem  --text-15:.9375rem  --text-18:1.125rem
--text-22:1.375rem  --text-28:1.75rem   --text-36:2.25rem
--space-1:4px … --space-8:64px  (4/8/12/16/24/32/48/64)
--radius-sm:6px  --radius-md:10px
--control-h:2.75rem  --control-h-sm:2.25rem
--motion-fast:120ms  --motion-base:160ms  --ease:ease-out
--shadow-layer: 0 4px 16px rgb(0 0 0 / .12)   /* só <dialog> e popover */
--focus-ring: 2px solid var(--accent)  --focus-offset: 2px
```

Derivações canônicas — **não inventar outras**: hover sólido `color-mix(in oklch, var(--accent), var(--ink) 18%)` · active sólido `… var(--ink) 28%` · hover fantasma `… transparent 88%` · texto desabilitado `color-mix(in oklch, var(--ink), var(--bg) 40%)` · fundo de Alert `color-mix(in oklch, var(--{tom}), var(--surface) 92%)`.

`:root { color-scheme: light dark }`; tema por `data-theme="light|dark"` no `<html>`, com `@media (prefers-color-scheme: dark)` em `:root:not([data-theme])`. Preferência manual em `localStorage`. **Sem `<script>` inline** no `index.html` (CSP) — aceita-se o flash de um frame.

Foco global no layer `base`:
```css
:focus-visible { outline: var(--focus-ring); outline-offset: var(--focus-offset); border-radius: inherit; }
```

**Fontes já instaladas** em `public/fonts/` (`fonts.css`, `LICENSE.txt`, 4 woff2 dos subsets latin e latin-ext). Basta importar `fonts.css`. Fraunces com `font-variation-settings: "SOFT" 0, "WONK" 0`.

---

## 2. Componentes base

`src/components/<Nome>/<Nome>.tsx` + `<Nome>.module.css`. **Sem barrel.** **Nenhum componente aceita `className` ou `style` de fora.**

### `Button`
```ts
type ButtonProps = {
  variant?: 'primary' | 'secondary' | 'quiet' | 'danger'   // default 'secondary'
  size?: 'md' | 'sm'                                        // default 'md'
  loading?: boolean; iconStart?: ReactNode; iconEnd?: ReactNode; fullWidth?: boolean
} & Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'className' | 'style'>
```
| Variante | Fundo | Texto | Borda |
|---|---|---|---|
| primary | `--accent` | `--on-solid` | — |
| secondary | `--surface` | `--ink` | 1px `--border-strong` |
| quiet | transparente | `--accent` | — |
| danger | `--danger` | `--on-solid` | — |

`min-block-size: var(--control-h)`, `padding-inline: var(--space-4)`, `gap: var(--space-2)`, `border-radius: var(--radius-sm)`, `--font-ui` `--text-15` peso 600. **Nunca** `border-radius: 999px`. Sem `transform` no active.

**Loading (regra de acessibilidade):** o submit **nunca** recebe `disabled` — usa `aria-disabled="true"` + `aria-busy="true"`, mantém o foco, e o handler faz `return` cedo. `iconStart` vira `<Spinner size="sm" aria-hidden />`; **o rótulo textual não muda** (largura estável). O resultado é anunciado pelo `Alert`. Formulário inválido **não** desabilita o submit.

Navegação usa `TextLink` (o `<Link>` do router estilizado: `--accent`, `text-decoration-thickness: 1px`, `text-underline-offset: 3px`, hover `--ink`) — nunca um Button que navega.

### `FieldShell` (interno, não exportado como UI)
```
<div class=field>
  <div class=labelRow> <label for={id}> {labelAction?} </div>
  {controle}
  <p id={msgId} class=message data-tone="hint|error"> …
</div>
```
Label `--font-ui` `--text-13` peso 600. Slot de mensagem com `min-block-size: 1.25rem` — **espaço sempre reservado**, zero salto de layout. Mostra hint **ou** erro (nunca empilha). Erro em `--danger` **com `<AlertIcon size={14} />` inline** — nunca sinalizar erro só por cor (WCAG 1.4.1). `aria-describedby` sempre; `aria-invalid` só com erro. O `<p>` **não** tem `role="alert"` (evita tagarelice a cada tecla): validação em `onSubmit`, revalidação em `onBlur`, RHF com `shouldFocusError: true`.

### `TextField`
`id` via `useId()`, `forwardRef` para o RHF. **Este é o detalhe de identidade:** fundo `--surface-sunken`, `border: 1px solid var(--border-strong)` com **`border-bottom-width: 2px`** (a linha do caderno onde se escreve), `padding-block-start: 1px` (compensação óptica), `min-block-size: var(--control-h)`, `caret-color: var(--accent)`. Foco: `border-bottom-color: var(--accent)` + anel global. Erro: todas as bordas `--danger`.
Autofill: `input:autofill { box-shadow: inset 0 0 0 100px var(--surface-sunken); -webkit-text-fill-color: var(--ink); }`.
**Não é o filled field do MUI:** label estático e permanente, sem label flutuante animado, sem underline que cresce no foco. Placeholder só quando exemplifica formato — nunca como substituto de label.

### `PasswordField`
```ts
type PasswordFieldProps = Omit<TextFieldProps,'type'> & { toggleable?: boolean; showRequirement?: boolean }
```
Botão de revelar dentro do campo (2.5rem², `type="button"`, `EyeIcon`/`EyeOffIcon` 20px). `aria-label` dinâmico **"Mostrar senha" / "Ocultar senha"** — **sem `aria-pressed`** (duplicaria o anúncio). Input com `padding-inline-end: 3rem`.
`autoComplete` **nunca** `"off"`: `current-password` no login, `new-password` em cadastro/redefinição. `maxLength={128}`.
`showRequirement`: linha `--text-13` com `CheckIcon` 14px (cinza → `--accent` quando atendido) em `aria-live="polite"`, texto "Pelo menos 12 caracteres".
**Sem campo "confirmar senha"** em nenhuma tela — existe o botão de revelar e existe recuperação por e-mail.

### `Alert` / `FormError`
```ts
type AlertProps = { tone: 'error'|'success'|'info'|'warning'; title?: string; children: ReactNode
                    action?: ReactNode; autoFocus?: boolean; id?: string }
```
`role`: error/warning → `alert`; success/info → `status`. Com `autoFocus`, container ganha `tabIndex={-1}` + `.focus()` no mount.
**Não é caixa saturada:** `background: color-mix(in oklch, var(--{tom}), var(--surface) 92%)`, `border: 1px solid var(--border)`, `border-inline-start: 3px solid var(--{tom})`, ícone 20px na cor do tom, **texto sempre `--ink`**. Sem sombra.
Tons: error → `--danger` · warning → `--warning` · success → `--accent` · info → `--border-strong`.
`FormError` = wrapper de `<Alert tone="error" autoFocus id={id}>` acima dos campos; o `<form>` referencia esse `id` em `aria-describedby`.

### `Spinner`
SVG próprio coerente com os ícones: círculo r=10 em viewBox 24, `stroke-width="1.5"`, `stroke-linecap="round"`, `stroke-dasharray="47 16"`, `animation: spin 900ms linear infinite`. Sem `label` ⇒ `aria-hidden`; com `label` ⇒ `role="status"` + `.sr-only` (default "Carregando").
`prefers-reduced-motion`: **sem rotação** — opacidade 1 → .35 → 1 em 1,4 s.

### `Skeleton`
`background: var(--surface-sunken)`, brilho por `background-position` em 1,2 s. `aria-hidden="true"` (o anúncio vem do `aria-busy` da região). Estático sob reduced-motion. **Só substitui conteúdo real**; nunca enfeita um vazio.

### `Panel`
```ts
type Panel = { as?: 'section'|'article'|'div'; title?: string; titleId?: string; subtitle?: string
               actions?: ReactNode; footer?: ReactNode; tone?: 'default'|'sunken'
               padding?: 'none'|'md'|'lg'; children: ReactNode }
```
`background: var(--surface)`, `border: 1px solid var(--border)`, `border-radius: var(--radius-md)`, **`box-shadow: none` — regra dura.** Título em `--font-display` `--text-18` peso 600. `tone="sunken"` ⇒ `--surface-sunken` sem borda. Com `as="section"` + `title`, usa `aria-labelledby`.

### `Logo`
Símbolo (casa pautada — livro-caixa doméstico), viewBox 24, `fill="none"`, `stroke="currentColor"`, `stroke-width="1.5"`, cor `--accent`:
1. telhado `M3.5 10.75 11.25 4.4a1.2 1.2 0 0 1 1.5 0l7.75 6.35`
2. corpo `M5.75 12.6v6.15a.85.85 0 0 0 .85.85h10.8a.85.85 0 0 0 .85-.85V12.6`
3. pauta `M8.75 15.4h6.5` e `M8.75 18h4` (a segunda mais curta — a linha do total)

Wordmark "HomeFinance" em `--font-display` peso 600, `letter-spacing: -0.01em`, `--ink`. Sem `title` ⇒ `aria-hidden` (o link ao redor leva `aria-label="HomeFinance — início"`).

### Ícones — `src/components/icons/`
Um `.tsx` por ícone + `types.ts` (`IconProps { size?: number; title?: string }`). Padrão único: viewBox `0 0 24 24`, `fill="none"`, `stroke="currentColor"`, `stroke-width="1.5"`, `stroke-linecap`/`linejoin="round"`, `aria-hidden="true"` e `focusable="false"` por padrão; com `title` vira `role="img"` + `<title>`.
Conjunto: `EyeIcon`, `EyeOffIcon`, `AlertIcon`, `CheckIcon`, `InfoIcon`, `MailIcon`, `LockIcon`, `ArrowLeftIcon`, `ArrowRightIcon`, `ChevronDownIcon`, `LogOutIcon`, `SunIcon`, `MoonIcon`.
**Nenhum ícone preenchido, nenhum de duas cores, nenhum emoji em lugar nenhum.**

---

## 3. `CodeInput` — um único `<input>`, não seis caixas

**A decisão é de acessibilidade, não de estética:**
1. Seis inputs = seis campos no modo de navegação do leitor de tela; o usuário ouve "edição, em branco" seis vezes e nunca revisa "123456" como um valor único.
2. `autocomplete="one-time-code"` entrega os 6 dígitos a **um** campo — com seis inputs o autofill é heurístico e costuma despejar tudo no primeiro.
3. Colar, apagar, navegar e **desfazer** são nativos. Cada handler de `paste`/`keydown` que redistribui dígitos é uma chance a mais de quebrar teclado físico, virtual e leitor de tela.
4. Sem roubo de foco entre caixas (origem clássica de bug com SR e Gboard/SwiftKey).
5. Um `aria-invalid`, um `aria-describedby`, **um** anúncio.

```ts
type CodeInputProps = {
  label?: string   // default 'Código de 6 dígitos'
  hint?: string    // default 'Digite ou cole o código do e-mail.'
  error?: string; value: string; onChange: (v: string) => void
  onComplete?: (v: string) => void; busy?: boolean; autoFocus?: boolean
}
```

**Atributos literais:** `type="text"` (**nunca** `number` — spinner, scroll acidental, perde zero à esquerda, recusa colagem com espaços) · `inputMode="numeric"` · `pattern="[0-9]*"` · `autoComplete="one-time-code"` · `maxLength={6}` · `name="code"` · `enterKeyHint="done"` · `autoCorrect="off"` · `autoCapitalize="off"` · `spellCheck={false}`.
`onChange`: `raw.replace(/\D/g, '').slice(0, 6)` — colar "123 456", "123-456" ou o trecho do e-mail funciona.

**Visual:** `--font-display`, `--text-28`, `font-variant-numeric: tabular-nums slashed-zero`, `letter-spacing: 0.5ch`, **`text-align: left`** (centralizar desalinharia da pauta com menos de 6 dígitos), `inline-size: calc(9ch + var(--space-6))`, `min-block-size: 4rem`, fundo `--surface-sunken`.
Pauta de seis casas:
```css
background-image: repeating-linear-gradient(90deg,
  var(--border-strong) 0 1ch, transparent 1ch 1.5ch);
background-size: 9ch 2px;
background-position: var(--space-3) calc(100% - var(--space-3));
background-repeat: no-repeat;
```
Erro: pauta `--danger`, **dígitos continuam `--ink`** (número é dado, não estado financeiro). Durante envio: `readOnly` + `aria-busy` — **nunca `disabled`** (tiraria o campo do foco e do cursor virtual).

**Verificação obrigatória antes de fechar o componente:** digitar `111111` e depois `000000` e conferir se cada dígito cai exatamente sobre um traço. Se a Fraunces não entregar largura tabular, trocar **só o CodeInput** para `--font-ui` com `tabular-nums`.

**Comportamento:**
- **Autoenvio:** `onComplete` ao atingir 6 dígitos **apenas quando o valor cresceu** (nunca ao apagar), nunca com requisição em voo, e **não repete após erro** até o valor mudar. Ligado em `/confirmar-email`; **desligado** em `/redefinir-senha` (ao completar, o foco vai para "Nova senha").
- **Erro:** `aria-invalid`, mensagem no `FormError`, e o campo **não é limpo** — `.focus()` + `.select()`. **Nunca exibir "restam N tentativas"**: a contagem só existe se o código existir, o que vazaria a existência da conta.
- **Reenviar:** `Button variant="quiet"` com texto fixo "Enviar outro código" e, ao lado, `<span aria-hidden="true">` com "disponível em 0:47" (`m:ss`, `tabular-nums`). Durante a espera, `aria-disabled="true"` (continua focável); se ativado, um `role="status"` diz "Ainda faltam 47 segundos para pedir outro código." Ao zerar, o mesmo `role="status"` anuncia **uma vez**: "Você já pode pedir um novo código." **O contador nunca é `aria-live`** (seriam 60 anúncios).
- Escalonamento 60 → 120 → 240 → 300 s (teto), conversando com o rate limit do backend. Instante do próximo envio em `sessionStorage` como timestamp puro, **sem o e-mail junto**.
- Após reenviar: campo limpo, foco de volta, `role="status"`: "Se o e-mail estiver cadastrado, enviamos um novo código."

---

## 3.1 `registrationToken` — a metade que fica no navegador (emenda de 09/09/2026)

O backend fechou uma tomada de conta por *pre-hijacking* que era explorável de verdade: o atacante cadastrava o e-mail da vítima com a senha **dele**, o código de 6 dígitos ia para a caixa da **vítima**, e a vítima, ao usar o código, ativava a conta **com a senha do atacante**. Funcionava nas duas ordens.

A correção (ADR-014, `docs/SEGURANCA.md` §1.1) amarra o código à **tentativa de cadastro que o pediu**: `POST /auth/register` devolve um `registrationToken` opaco de 64 hexadecimais, e `POST /auth/verify-email` **exige** esse token junto do código. O navegador da vítima só tem o token dela, então o código do atacante é inútil na mão dela; o atacante tem o token, mas nunca recebe o código. O contrato literal está em `backend/api/openapi.yaml` (schemas `VerificationRequired`, `VerifyEmailRequest`, `ResendCodeRequest`) — **é ele a fonte de verdade**, não este parágrafo.

### O que o frontend faz

| Momento | Ação |
|---|---|
| 202 de `register` | **guarda** o par `{token, email}` da resposta, substituindo qualquer anterior |
| leitura | `readRegistrationTokenFor(email)` — token de **outro** endereço vale o mesmo que token nenhum |
| `verify-email` | envia `{ email, code, registrationToken }` — sem o campo o backend responde 400 `VALIDATION_FAILED` com `fields.registrationToken` |
| `resend-code` | **exige** o token guardado; o `registrationToken` do 202 **substitui** o guardado (quem decide de que tentativa é o código novo é o servidor) |
| verificação bem-sucedida · login bem-sucedido | **apaga** o token — é capacidade de uso único e o fluxo acabou |

`login`, `refresh`, `logout`, `forgot-password`, `reset-password` e `/me` não mudaram. **A redefinição de senha não usa `registrationToken`** — o código dela é amarrado à conta, não a uma tentativa de cadastro; por isso `forgotPassword` tem tipo de resposta próprio (`RecoveryAccepted`), sem o campo.

### Onde o token vive — e por que isso não fura a §1 do `docs/SEGURANCA.md`

`sessionStorage`, em `src/features/auth/storage/registrationToken.ts`, sob a chave `hf.registration`, guardando o par **`{token, email}`**. A exceção está registrada com escopo em `docs/SEGURANCA.md` §1 — vale só para este token.

A regra que proíbe `localStorage`/`sessionStorage` é sobre **token de sessão**, e continua valendo inteira: a sessão do HomeFinance vive em cookies `HttpOnly` que o frontend nunca lê nem grava. O `registrationToken` é outra classe de segredo — sozinho não autentica ninguém e não abre dado algum, só serve junto do código que foi para a caixa de entrada, o backend o guarda apenas como hash SHA-256, e ele morre na verificação.

Por que `sessionStorage` e não outra coisa:

- **memória apenas** morre no F5, e perder o token obriga a refazer o cadastro (aqui a memória é só rede de proteção para storage bloqueado);
- **`localStorage`** sobrevive demais: fica no disco depois do fluxo e é visível a todas as abas do perfil;
- **cookie** viajaria sozinho em toda requisição, inclusive nas que nada têm a ver com cadastro — e o backend pede este valor no **corpo**;
- **query string** é proibida para dado sensível (`docs/SEGURANCA.md` §6): vaza em log de servidor, histórico e `Referer`.

**O e-mail vai junto do token, e isso é decisão de segurança.** O servidor busca a tentativa pelo par `(email, hash do token)` — um token de **outro endereço** é indistinguível de **nenhum** token. Guardar só o token faria a interface confundir "tenho **um** token" com "tenho **o** token desta tentativa", e essa confusão é explorável: com um token velho na aba e um endereço diferente no formulário, o cliente pediria reenvio para um endereço que o seu token não governa. Ler o token **pelo endereço** (`readRegistrationTokenFor`) transforma esse caso no que ele já é do lado do servidor, e a tela oferece a recuperação em vez de um beco.

O custo de privacidade é nulo na prática: o e-mail já viaja em `history.state`, que o navegador persiste em disco para restaurar sessão — **verificado no Chromium: o state, e o `email` dentro dele, sobrevivem ao F5** — e está na tela enquanto a pessoa confirma. Se um dia o e-mail sair do `history.state`, a decisão se reavalia.

Demais regras duras: só aceitar token que case `^[0-9a-f]{64}$` e registro que case o schema, na leitura **e** na escrita, descartando o resto antes de virar corpo de requisição; comparar o endereço normalizado (`trim` + minúsculas), como o backend faz; apagar o par ao concluir o fluxo. Há **fallback em memória** para navegador com storage bloqueado — sem ele, `setItem` falhando em silêncio prendia a pessoa num laço de cadastro sem fim, gastando cota de mensagem a cada volta.

### Premissa corrigida — o state do roteador **sobrevive** ao F5

A versão anterior desta spec afirmava, na §6, que o state do roteador se perdia no F5, e por isso mandava a tela de confirmação oferecer um campo de e-mail livre como recuperação. **A afirmação era falsa**, e foi essa premissa que produziu o campo por onde um token de um endereço podia ser combinado com outro endereço — o descasamento que a §3.1 agora fecha.

Verificado empiricamente no Chromium em 09/09/2026, com o app rodando em `vite dev`, via Playwright: gravou-se `history.replaceState({ ...history.state, email: 'bruno@example.com' }, '', '/confirmar-email')`, chamou-se `location.reload()`, e após o recarregamento `history.state` continha `{"__TSR_index":0,"key":"…","__TSR_key":"…","email":"bruno@example.com"}` — o state, e o `email` dentro dele, intactos. O navegador persiste o state da entrada de histórico para restauração de sessão.

Consequência normativa: **o e-mail no state do roteador é meio confiável de carregar o endereço entre as telas do cadastro, inclusive através do F5.** Não é preciso — nem admissível — um campo de e-mail de recuperação em `/confirmar-email`.

### Token perdido — a recuperação é o cadastro, não o reenvio

`resend-code` **exige** o `registrationToken`. O caminho de "reenvio sem token" existiu por pouco tempo e foi removido do backend por ser explorável: quem pede reenvio não apresenta credenciais, então a tentativa sucessora herdava as de **outra pessoa** — a mesma tomada de conta do ADR-014, por outra porta. Sem token o 202 continua idêntico (grupo B da §3.12), mas nada é emitido.

Portanto: **o botão "Enviar outro código" não é renderizado sem token**, e o tipo de `resendCode` em `features/auth/api/auth.ts` exige o campo, para que a rota não possa ser chamada sem ele. **`register` é a recuperação garantida** — sempre funciona e sempre devolve um token novo.

Na prática, sem token guardado a tela `/confirmar-email` **não renderiza formulário nenhum** — nem `CodeInput`, nem botão de reenvio — e mostra só o caminho de volta ao cadastro.

No login, o 403 `EMAIL_NOT_VERIFIED` — que **não** mudou de formato e **não** devolve token (nem poderia: entregar a metade secreta do cadastro a quem só sabe a senha reabriria o *pre-hijacking* pela porta do login) — passa a bifurcar: com o token **daquele endereço** nesta aba vai para `/confirmar-email`, porque o código reemitido pertence à **mesma** tentativa e o token continua valendo; sem token — ou com token de outro endereço — vai para `/criar-conta`, com o e-mail no state para não obrigar a redigitar.

---

## 4. Layout das telas de autenticação — "a folha do caderno"

**Recusa explícita:** nada de card branco centralizado com sombra sobre fundo cinza. Viola duas linhas da lista de rejeição e contradiz a identidade — um caderno de contas não tem um cartão pairando sobre a mesa.

**A viewport é o caderno aberto.** Coluna esquerda `--bg` (a capa), coluna direita `--surface` (a folha), separadas por 1px `--border` (a costura). Zero sombra, zero flutuação.

> **Por que tem personalidade:** o usuário escreve *na* página — margem, pauta e cabeçalho de página de livro-razão — em vez de preencher uma caixa que apareceu por cima de um fundo neutro. O mesmo vocabulário reaparece no input e no CodeInput, então a tela de login já ensina o resto do app.

**Grid ≥1024px:** `grid-template-columns: minmax(280px, 34fr) minmax(0, 66fr); min-block-size: 100dvh;`

**Capa (esquerda):** `padding: var(--space-7)`, `display: grid; align-content: space-between`.
- Topo: `<Logo variant="full" size="lg" />`
- Meio: `--font-display` `--text-22`, `max-inline-size: 20ch`, alinhado à esquerda: **"O caderno de contas da casa."** (sem centralização, sem subtítulo, sem botão)
- Rodapé `--text-13` `--ink-muted`, `max-inline-size: 34ch`: **"Feito para uma casa, não para um banco. Receitas, despesas, contas que vencem e orçamentos no mesmo lugar."** + **"Versão em desenvolvimento — Fase 3 do roadmap."**
- **Nada de amostra de extrato falsa** — não temos dados, e inventar número em tela de produto é desonesto.

**Folha (direita):** `padding-block-start: var(--space-8)`, `padding-inline: clamp(var(--space-5), 6vw, 88px)`, conteúdo alinhado ao **topo e à esquerda** (nunca `place-items: center`), `max-inline-size: 42ch`.
Margem do caderno: `::before` com `position:absolute; inset-block:0; inline-size:1px; inset-inline-start: clamp(16px, 4vw, 56px); background: var(--border)`. Some abaixo de 720px.

| Elemento | Fonte / tamanho | Espaço |
|---|---|---|
| Cabeçalho de página (seção à esquerda, etapa à direita) | `--font-ui` `--text-13` 600, `--ink-muted`, uppercase, `letter-spacing:.08em`, `tabular-nums` na etapa | `border-bottom: 1px solid var(--border)`; `padding-bottom: var(--space-2)`; `margin-bottom: var(--space-5)` |
| `<h1>` | `--font-display` `clamp(1.375rem, 1.15rem + 1.1vw, 1.75rem)` 600 | — |
| Parágrafo de apoio | `--font-ui` `--text-15` `--ink-muted`, `max-inline-size: 46ch` | `margin-top: var(--space-2)` |
| `FormError` | — | `margin-top: var(--space-5)` |
| Campos | — | `margin-top: var(--space-6)`; `gap: var(--space-5)` |
| Botão primário | `fullWidth` | `margin-top: var(--space-5)` |
| Ação secundária | `--text-15` | `margin-top: var(--space-4)` |
| Rodapé (link da tela irmã) | `--text-15` | `border-top: 1px solid var(--border)`; `margin-top: var(--space-6)`; `padding-top: var(--space-4)` |

**Fraunces vs Public Sans — sem exceção:** Fraunces só no `<h1>`, títulos de `Panel`, wordmark e dígitos do CodeInput. Public Sans em todo o resto.

**<1024px:** uma coluna. Barra superior de 56px (`--bg`, Logo `size="md"`), abaixo a folha com `border-top`, `min-block-size: calc(100dvh - 56px)`. A capa vira rodapé da folha em `--text-13` `--ink-muted`.

**Transição:** `document.startViewTransition` (**nunca** o `<ViewTransition>` experimental do React) com `view-transition-name: auth-sheet`: `opacity 0→1` + `translateY(8px→0)` em `var(--motion-base)`. Desligado sob `prefers-reduced-motion`. A capa não anima.

**Foco:** ao trocar de rota, foco no `<h1>` (`tabIndex={-1}`). `autoFocus` **só** no CodeInput (telas 3 e 5).

**Rotas** (URL é interface ⇒ pt-BR; arquivos e identificadores em inglês): `/entrar`, `/criar-conta`, `/confirmar-email`, `/esqueci-minha-senha`, `/redefinir-senha`. `document.title` = "Entrar · HomeFinance" etc. **E-mail sempre por state do router, nunca `?email=`.**

---

## 5. Home autenticada

Nada de dado financeiro nesta fase — e **nada de "R$ 0,00" gigante** nem grid de cards de KPI vazios.

**Casca:** `AppHeader` 56px, `--surface`, `border-bottom: 1px solid var(--border)`, **sem sombra**. Esquerda: `<Logo variant="full" size="sm" />` em link com `aria-label="HomeFinance — início"`. Direita: nome da casa em `--text-13` `--ink-muted` + `UserMenu`.
**Sem barra de navegação nesta fase** — "Lançamentos", "Contas", "Relatórios" ainda não existem; menu que não leva a lugar nenhum é desonesto.

`UserMenu`: botão com iniciais (32px, `--surface-sunken`) + `ChevronDownIcon`; painel via **Popover API** (`popover="auto"` + `popovertarget`), 240px, `box-shadow: var(--shadow-layer)` (aqui a sombra é legítima — camada flutuante). Conteúdo: nome + e-mail (`overflow-wrap: anywhere`); divisor; "Aparência" com `role="radiogroup"` (Claro/Escuro/Sistema); divisor; "Sair" com `LogOutIcon`. Setas com roving tabindex (a Popover API dá light-dismiss e ESC; papel semântico e teclado são nossos).

**Corpo:** `max-inline-size: 1120px; margin-inline: auto`. Grid `minmax(0,1fr) minmax(0,320px)` com `gap: var(--space-6)` a partir de 900px; empilha abaixo.

**Textos exatos:**
- `<h1>` (`--font-display` `--text-28`): **"Olá, {primeiroNome}."**
- Sob o título (`--text-15` `--ink-muted`, `tabular-nums`, `Intl.DateTimeFormat('pt-BR', { dateStyle: 'full' })` com inicial maiúscula): **"Quarta-feira, 9 de setembro de 2026"** (o valor real vem do `Intl`; o exemplo original desta spec dizia "Terça-feira" e estava **errado** — 09/09/2026 é quarta)
- **Painel principal** (`Panel padding="lg"`):
  - Título (`--font-display` `--text-22`): **"Seu caderno está em branco."**
  - Parágrafo (`--text-15` `--ink-muted`, `max-inline-size: 60ch`): **"Ainda não há nada registrado — e ainda não dá para registrar: os lançamentos entram na próxima fase do projeto. Por enquanto, sua conta já está criada e com o e-mail confirmado, que é o que garante que só você entra aqui."**
  - **Prévia da folha pautada** (`Panel tone="sunken" padding="md"`, `aria-hidden="true"`): cabeçalho `--text-13` `--ink-muted` uppercase — "Data · Descrição · Categoria · Valor" (grid `88px 1fr 140px 96px`, última coluna à direita) — e 5 linhas vazias de 32px separadas por `1px solid var(--border)`, com "—" em `--ink-muted` `tabular-nums` na última coluna. É a estrutura real do que vem, feita com os próprios tokens; como usa borda em vez de bloco preenchido, não se confunde com skeleton.
  - **Sem botão** — não há ação possível, e botão que não faz nada é o pior placeholder.
- **Painel lateral** (`Panel title="Onde o projeto está"`), duas listas de texto (não cards, não ícones decorativos):
  - **"Já funciona"** — `<ul>` com `CheckIcon` 16px `--accent`: "Criar conta com e-mail confirmado por código" · "Entrar e sair com sessão segura" · "Recuperar a senha pelo mesmo código de 6 dígitos"
  - **"A seguir"** — `<ul>` com `ArrowRightIcon` 16px `--ink-muted`: "Contas e categorias" · "Lançamentos do mês" · "Contas que vencem" · "Orçamentos e relatórios"
  - Rodapé `--text-13` `--ink-muted`: **"Este app roda no seu servidor. Nenhum dado sai daqui."**

**Estados:** *carregando* ⇒ região com `aria-busy`, `Skeleton` de 220px×1.75rem no `<h1>` e 180px×.9375rem na data (o painel principal já pode renderizar, é estático). *Erro de sessão* ⇒ `Alert tone="error"` **"Não foi possível carregar sua conta."** + "Verifique sua conexão e tente de novo." + botão "Tentar de novo". *Vindo do cadastro* ⇒ `Alert tone="success"` **"E-mail confirmado. Sua conta está pronta."**

---

## 6. Microcópia (pt-BR, literal)

Tom direto e cordial. **Proibido:** ponto de exclamação, "Ops!", "Bem-vindo de volta", emoji, "Uhul", jargão de fintech.

### `/entrar`
Eyebrow **Acesso à conta** · `<h1>` **Entrar** · Apoio **"Use o e-mail e a senha da sua conta."**
Campos: **E-mail** (`autocomplete="email"`, `inputmode="email"`) · **Senha** (`current-password`) com `labelAction` = **"Esqueci minha senha"**. Botão **Entrar**.
Erros locais: "Informe seu e-mail." · "Esse e-mail não parece válido." · "Informe sua senha."
**401** ⇒ **"E-mail ou senha incorretos."** (idêntica para conta inexistente e senha errada).
**403 `EMAIL_NOT_VERIFIED`** ⇒ bifurca pelo token guardado (§3.1):
- **com token deste endereço nesta aba** ⇒ navegar para `/confirmar-email` com o e-mail no state e `Alert tone="info"`: **"Confirme seu e-mail para entrar. Use o código que enviamos — se não encontrar, peça um novo."**
  A redação anterior desta spec dizia "Enviamos um código novo agora." e **mentia** quando o cooldown ou a cota seguravam o envio: o backend só reemite se houver exatamente uma tentativa viva e a janela permitir. A frase atual é verdadeira nos dois casos, orienta a ação certa e não revela nada além do que o próprio 403 já revela — que só chega a quem provou saber a senha.
- **sem token, ou token de outro endereço** ⇒ navegar para `/criar-conta` com o e-mail no state e `Alert tone="info"`, título **"Confirme seu e-mail para entrar."** + **"O código de 6 dígitos só vale no navegador em que o cadastro foi pedido. Preencha os dados de novo com o mesmo e-mail para receber um código novo."**
429 ⇒ "Muitas tentativas. Aguarde alguns minutos e tente de novo." · rede/5xx ⇒ "Não foi possível entrar agora. Verifique sua conexão e tente de novo."
Rodapé: **"Ainda não tem conta?"** + **"Criar conta"**.

### `/criar-conta`
Eyebrow **Nova conta** · etapa **1 de 2** · `<h1>` **Criar conta**
Apoio: **"Você vai receber um código de 6 dígitos por e-mail para confirmar o endereço."**
Campos: **Nome** (`autocomplete="name"`, hint "Como você quer ser chamado no app.") · **E-mail** · **Senha** (`new-password`, `showRequirement`, hint **"Pelo menos 12 caracteres. Prefira uma frase que só você saiba."**). Botão **Criar conta**.
Erros locais: "Informe seu nome." · "Informe seu e-mail." · "Esse e-mail não parece válido." · "Informe uma senha." · "A senha precisa de pelo menos 12 caracteres." · "A senha pode ter no máximo 128 caracteres."
**E-mail já cadastrado:** o backend responde **202 idêntico** ao caso novo; o front navega **sempre** para `/confirmar-email`. (O backend sempre envia e-mail: código, ou aviso "alguém tentou criar uma conta com este endereço".)
**No 202, guardar o `registrationToken` antes de navegar** (§3.1). Se ele vier malformado, nada é guardado e a tela seguinte já cai no caminho de recuperação — nunca se manda sujeira para a API.
Esta tela também recebe `notice` e e-mail pelo state do roteador, para o caso de chegar vinda do login sem token (§6, `/entrar`); o campo **E-mail** nasce preenchido nesse caso.
Rodapé: **"Já tem conta?"** + **"Entrar"**.

### `/confirmar-email`
Eyebrow **Nova conta** · etapa **2 de 2** · `<h1>` **Confirme seu e-mail**
São **duas aberturas**, e **esta tela não tem campo de e-mail** (correção de 09/09/2026 — ver §3.1):

| Situação | Apoio | Formulário |
|---|---|---|
| token **deste endereço** | **"Enviamos um código de 6 dígitos para {email}. Ele vale por 15 minutos."** (e-mail em peso 600 `--ink`) | só o `CodeInput` |
| sem token, ou token de **outro** endereço | **"O código de 6 dígitos só vale no navegador em que o cadastro foi pedido."** | **nenhum** |

O endereço vem **sempre** do state do roteador, que sobrevive ao F5 (verificado no navegador; a versão anterior desta spec supunha o contrário e por isso previa um campo de e-mail aqui). O campo foi removido porque uma aba que não sabe qual endereço pediu o código também não sabe se o token que ela guarda governa o endereço que a pessoa digitaria — e é exatamente esse descasamento que abria brecha. Chegar aqui por URL digitada, favorito ou histórico sem state cai no caminho de recuperação, que é o correto.

Nos dois casos sem token utilizável, a tela não renderiza `CodeInput` nem "Enviar outro código" — campo que só produziria 400 é pior que campo nenhum. No lugar, `Alert tone="info"`:
- Título: **"Refaça o cadastro para receber um código novo"**
- Texto: **"É assim que um código enviado para o seu e-mail não serve na mão de mais ninguém. Se você abriu esta tela em outra aba, em outro navegador ou em outro dispositivo, preencha o cadastro de novo com o mesmo e-mail."**
- `action`: `TextLink` **"Ir para o cadastro"** → `/criar-conta` (levando o e-mail no state quando houver)

A redação explica **por que** o código não vale aqui em vez de tratar a pessoa como culpada, e não diz nada sobre o endereço existir ou não.

Botão **Confirmar e-mail** · Secundária **"Enviar outro código"** (ambos só no caminho com token).
**Erro (uma única redação para errado / expirado / já usado / tentativas esgotadas / conta inexistente):** **"Código inválido ou expirado. Peça um novo código se precisar."**
429 no reenvio ⇒ "Você pediu códigos demais. Aguarde alguns minutos antes de tentar de novo."
Pós-reenvio (`role="status"`) ⇒ "Se o e-mail estiver cadastrado, enviamos um novo código." — e o `registrationToken` da resposta **substitui** o guardado (§3.1).
Sucesso ⇒ apaga o token guardado e navega para `/` com "E-mail confirmado. Sua conta está pronta."
Rodapé: **"Errou o e-mail?"** + **"Começar de novo"** · **"Já tem conta?"** + **"Entrar"**.

### `/esqueci-minha-senha`
Eyebrow **Recuperação de senha** · etapa **1 de 2** · `<h1>` **Esqueci minha senha**
**Apoio (redação-chave):** **"Informe seu e-mail. Se houver uma conta com ele, enviamos um código de 6 dígitos para você criar uma senha nova."**
A condição é declarada **antes** do envio, como expectativa — não depois, como desculpa. Por isso não soa evasiva.
Campo **E-mail** · Botão **Enviar código**.
Resposta (sempre 202, idêntica) ⇒ navega para `/redefinir-senha` com `Alert tone="info"`: **"Se houver uma conta com {email}, o código chega em instantes. Confira também a caixa de spam."**
Rodapé: **"Lembrou a senha?"** + **"Entrar"**.

### `/redefinir-senha`
Eyebrow **Recuperação de senha** · etapa **2 de 2** · `<h1>` **Criar uma senha nova**
Apoio: **"Digite o código enviado para {email} e escolha a senha nova. O código vale por 15 minutos."**
`CodeInput` (**autoenvio desligado**; ao completar, foco vai para a senha) · `PasswordField` **Nova senha** (`new-password`, `showRequirement`).
Botão **Redefinir senha** · Secundária **"Enviar outro código"**.
Erro do código ⇒ **"Código inválido ou expirado. Peça um novo código se precisar."** (mesma frase da tela 3 — nunca uma variante que diferencie os casos)
Sucesso ⇒ navega para `/entrar` com `Alert tone="success"` **"Senha redefinida. Entre com a senha nova."** + linha `--text-13` `--ink-muted`: **"Por segurança, encerramos as sessões abertas nos outros dispositivos."**
Rodapé: **"Voltar para entrar"**.

### Erros globais (mapa único em `src/lib/errors.ts`)
`400/422` ⇒ "Confira os dados informados e tente de novo." · `401` (sessão expirada) ⇒ "Sua sessão expirou. Entre de novo." · `403` ⇒ "Você não tem acesso a este conteúdo." · `404` ⇒ "Não encontramos esta página." · `429` ⇒ "Muitas tentativas. Aguarde alguns minutos e tente de novo." · `5xx` ⇒ "Algo falhou do nosso lado. Tente de novo em instantes." · rede ⇒ "Sem conexão com o servidor. Verifique sua internet."
**Nenhuma mensagem repassa texto cru vindo da API.**

---

## 7. Ordem de implementação

1. `src/styles/` — `tokens.css`, `reset.css`, `base.css` (`@layer`, `.sr-only`, `:focus-visible`, import de `/fonts/fonts.css`)
2. Ícones + `Logo` + `Spinner` + `Skeleton` (sem dependências)
3. `Button`, `TextLink`, `FieldShell`, `TextField`, `PasswordField`, `Alert`/`FormError`, `Panel`
4. `CodeInput` + teste Vitest: colar "12 34-56" ⇒ `123456`; `onComplete` dispara ao crescer e **não** ao apagar; não redispara após erro
4.1 `features/auth/storage/registrationToken.ts` (§3.1) + teste: só 64 hexadecimais entram e saem; nada de e-mail junto; `localStorage` intocado
5. `AuthLayout` + `AuthSheet` + as 5 telas
6. `AppHeader` + `UserMenu` (Popover API) + Home

## 8. Checklist anti-"cara de IA" (auditado pelo `designer-ui`, todos passam)

Gradiente/glassmorphism/blob **✓** (os únicos `gradient` são **funcionais, não decorativos**: a pauta do CodeInput e o brilho do `Skeleton` — nenhum é gradiente de cor de marca) · card flutuante com sombra **✓** (`Panel` tem `box-shadow: none` por regra; auth não usa card) · emoji **✓** (13 ícones SVG próprios) · hero centralizado **✓** (alinhado à esquerda e ao topo, `<h1>` ≤28px, um único botão primário) · componente de biblioteca **✓** · valor fora dos tokens **✓** · Inter por preguiça **✓** · verde/vermelho decorativo **✓** (`--income`/`--expense` só para dinheiro) · grid de cards vazios **✓** · acessibilidade **✓** (todo texto ≥4,7:1; bordas de controle ≥3,5:1; alvos de 44px; `prefers-reduced-motion` respeitado).
