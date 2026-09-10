# Design — HomeFinance

Dono deste documento: agente `designer-ui`. Toda decisão visual do projeto deriva daqui; os tokens abaixo são espelhados em `frontend/src/styles/tokens.css`.

## Identidade

Um app financeiro **doméstico**: da família, não de banco. A sensação é de um caderno de contas bem-feito — calmo, confiável, direto. Nada de "fintech disruptiva", nada de dashboard corporativo, nada de template de IA.

**Princípios:**
1. **Números são protagonistas.** Valores monetários sempre em fonte tabular, alinhados à direita, hierarquia clara entre entrou/saiu/vence.
2. **Calma.** Superfícies sólidas, contraste vindo de tipografia e espaço — não de sombras e gradientes.
3. **Honestidade.** Vermelho e verde só para significado financeiro (despesa/receita), nunca decoração.
4. **Densidade respeitosa.** Tabelas e listas compactas porém legíveis; quem controla contas quer ver o mês inteiro, não três cards gigantes.
5. **Sucesso não é verde de receita.** `--income` e `--expense` são reservados a **dinheiro**. Confirmação de sucesso do sistema usa `--accent`; erro de sistema usa `--danger` (mesmo valor de `--expense`, nome semântico distinto — o app tem um único vermelho). Nenhum estado de UI inventa cor: hover, desabilitado e fundo tingido saem sempre de `color-mix(in oklch, …)` sobre os tokens existentes.

## Lista de rejeição (reprova revisão na hora)

- Gradientes roxo/azul/violeta; glassmorphism; blobs decorativos.
- Cards brancos flutuando com sombra pesada sobre fundo cinza-claro.
- Emojis como ícone ou decoração na interface.
- Hero centralizado "título gigante + subtítulo + 2 botões".
- Qualquer componente importado ou copiado de MUI/shadcn/Ant/Chakra/Bootstrap.
- Cores, medidas ou raios hardcoded fora dos tokens.

## Tokens (fonte de verdade)

### Cor — tema claro (padrão) e escuro

| Token | Claro | Escuro | Uso |
|---|---|---|---|
| `--bg` | `#F7F5F0` | `#171512` | fundo da página (off-white quente, não cinza de template) |
| `--surface` | `#FFFFFF` | `#211E1A` | superfícies (tabelas, painéis) — separadas por borda, não sombra |
| `--border` | `#E4E0D6` | `#37322B` | bordas e divisores |
| `--ink` | `#26231E` | `#EDE9E1` | texto principal |
| `--ink-muted` | `#6E675C` | `#A39C8F` | texto secundário |
| `--accent` | `#1E5F4E` | `#4FA98D` | ação primária, links, foco (verde-escuro sóbrio) |
| `--income` | `#2E7D32` | `#66BB6A` | receitas |
| `--expense` | `#B3261E` | `#EF6E64` | despesas |
| `--warning` | `#9A6A00` | `#D9A23C` | contas a vencer |

Contraste mínimo AA (4.5:1) para texto; verificar ao criar variações.

#### Tokens derivados — obrigatórios (emenda de 09/09/2026)

A auditoria de contraste da paleta acima reprovou em três pontos, corrigidos aqui. **Estes tokens não são opcionais** — sem eles a interface falha WCAG:

| Problema medido na paleta original | Consequência |
|---|---|
| `--border` (#E4E0D6) sobre `--surface` = **1,32:1** | borda de input/checkbox **reprova** WCAG 1.4.11 (mínimo 3:1 para controle) |
| `#FFFFFF` sobre `--accent` escuro (#4FA98D) = **2,84:1** | texto de botão primário **ilegível** no tema escuro |
| `--warning` (#9A6A00) sobre `--bg` = **4,35:1** | texto de aviso **reprova AA** por pouco |

| Token | Claro | Escuro | Uso | Contraste medido |
|---|---|---|---|---|
| `--surface-sunken` | `#F1EEE7` | `#1C1916` | fundo de campo e blocos recuados ("a pauta") | ink 13,6:1 |
| `--border-strong` | `#847D71` | `#7A7266` | borda de **controle interativo** (input, checkbox, botão secundário) | 4,08:1 / 3,50:1 sobre surface |
| `--on-solid` | `#FFFFFF` | `#171512` | texto/ícone sobre qualquer preenchimento cromático sólido | 7,49:1 claro / 6,41:1 escuro sobre accent |
| `--danger` | `var(--expense)` | `var(--expense)` | **alias semântico**: erro de sistema — não é cor nova | 6,54:1 sobre surface |
| `--warning-ink` | `#7A5200` | `= --warning` | texto de aviso sobre `--bg` | 6,35:1 |

Valores autorados em OKLCH em `tokens.css` (hex acima é referência). Derivações canônicas — **o dev não inventa outras**:

```css
hover sólido        color-mix(in oklch, var(--accent), var(--ink) 18%)
active sólido       color-mix(in oklch, var(--accent), var(--ink) 28%)
hover fantasma      color-mix(in oklch, var(--accent), transparent 88%)
texto desabilitado  color-mix(in oklch, var(--ink), var(--bg) 40%)
fundo de Alert      color-mix(in oklch, var(--{tom}), var(--surface) 92%)
```

Tema por `data-theme="light|dark"` no `<html>`, com `@media (prefers-color-scheme: dark)` aplicado em `:root:not([data-theme])`; `:root { color-scheme: light dark }`. A preferência manual vive em `localStorage` (é preferência de UI, não token de sessão — permitido). **Sem `<script>` inline no `index.html`** por causa da CSP restritiva: aceita-se o flash de um frame na troca manual.

### Tipografia

- **Títulos e valores:** `"Fraunces", Georgia, serif` — serifada com personalidade, remete a livro-caixa. Valores monetários com `font-variant-numeric: tabular-nums`.
- **Texto e UI:** `"Public Sans", system-ui, sans-serif`.
- Escala: 13 / 15 (base) / 18 / 22 / 28 / 36 px. Peso: 400 texto, 600 destaque, 700 apenas em totais.
- **A escala é declarada em `rem`** (`.8125 / .9375 / 1.125 / 1.375 / 1.75 / 2.25`), e alturas de controle também (`--control-h: 2.75rem`) — px fixo ignora o zoom de fonte do sistema operacional e quebra acessibilidade.
- Fontes **self-hosted** em `frontend/public/fonts/` (woff2 variável, `font-display: swap`) — **nunca CDN do Google Fonts**: privacidade do usuário e compatibilidade com a CSP restritiva do backend.
- Fraunces com `font-variation-settings: "SOFT" 0, "WONK" 0` e `font-optical-sizing: auto` — sem a variante *wonky*, que quebraria a sobriedade.
- **Onde cada fonte entra (regra sem exceção):** Fraunces só em `<h1>`, títulos de painel, wordmark e dígitos do código de verificação. Public Sans em todo o resto — eyebrow, apoio, labels, hints, erros, botões e links. Isso impede o serif de virar enfeite editorial.

### Espaço, forma e movimento

- Espaço: escala de 4 px (`4, 8, 12, 16, 24, 32, 48, 64`).
- Raio: `6px` padrão, `10px` em painéis. Nunca fully-rounded em botões retangulares.
- Bordas de `1px solid var(--border)` separam superfícies; sombra apenas em camadas realmente flutuantes (menu, modal): `0 4px 16px rgb(0 0 0 / 0.12)`.
- Movimento: 120–180 ms, `ease-out`, apenas com propósito (feedback, entrada de camada). Respeitar `prefers-reduced-motion`. Transições entre rotas/estados com a **View Transitions API do navegador** (`document.startViewTransition` — nunca o `<ViewTransition>` experimental do React).

### CSS moderno (baseline seguro — pesquisa 09/2026)

- Tokens novos autorados em **OKLCH** (luminosidade perceptualmente uniforme); a paleta acima é a referência, e estados derivados **nunca criam cor nova**: `color-mix(in oklch, var(--accent), transparent 85%)` para hover/fundos, `color-mix(in oklch, var(--ink), var(--bg) 40%)` para desabilitado.
- Usar sem medo: **nesting nativo**, **`:has()`** (ex.: `tr:has([data-negative])` destaca a linha da despesa), **container queries** nos painéis de resumo (respondem ao container, não à viewport), **`@layer`** ordenando `reset, tokens, base, components, utilities`, `clamp()` na escala tipográfica.
- Números: `font-variant-numeric: tabular-nums` (obrigatório em tabelas, totais e inputs de valor) + `slashed-zero` em extratos densos.

## Componentes — regras

- Todo componente é escrito para este projeto, com API própria e `.module.css` ao lado consumindo só tokens.
- Estados obrigatórios desenhados: **vazio** (com orientação útil, ex.: "Nenhum lançamento em setembro — registre o primeiro"), **carregando** (skeleton discreto no lugar do conteúdo real), **erro** (o que houve + como tentar de novo).
- Foco visível desenhado (`outline` de 2px `var(--accent)` com offset), navegável por teclado, HTML semântico (`table` para tabela, `button` para botão).
- **Elementos nativos primeiro**: modais com `<dialog>` (foco, ESC e inert grátis; sempre com `aria-labelledby`); menus/dropdowns/seletores com a **Popover API** (light dismiss e foco grátis — role semântica, navegação por setas e labels são responsabilidade nossa). Combobox e tabs seguem os padrões **WAI-ARIA APG**.
- Ícones: um único conjunto, traço 1.5px, monocromático `currentColor` — SVG próprio em `frontend/src/components/icons/`.
- **Sombra é privilégio de camada flutuante.** Painéis e cartões usam `box-shadow: none` e se separam por borda de 1px; `--shadow-layer` existe apenas para `<dialog>` e popover.
- **Nenhum componente base aceita `className` ou `style` de fora** — variação nova se pede ao `designer-ui`, senão o design system vaza.
- Navegação de rota **move o foco para o `<h1>`** da tela nova (`tabIndex={-1}`, `outline: none` só em `:focus`, nunca em `:focus-visible`).
- Dados sensíveis (e-mail, código de verificação) trafegam por state do router — **nunca em query string** (seção 6 de `docs/SEGURANCA.md`).
- Botão de submit em carregamento **nunca** recebe `disabled`: usa `aria-disabled` + `aria-busy`, mantém o foco e preserva a largura do rótulo. Formulário inválido também não desabilita o submit — o usuário precisa descobrir o motivo.
- Idioma da interface: **português brasileiro**; moeda via `Intl.NumberFormat('pt-BR', { style: 'currency', currency: 'BRL' })`; datas `dd/mm/aaaa`.

## Referência de tom por tela

- **Dashboard do mês:** o "resumo do caderno" — saldo do mês, entradas vs saídas, próximos vencimentos, orçamentos estourando. Tabela e números, não mar de cards.
- **Lançamentos:** tabela densa, filtros discretos, adição rápida (a ação mais frequente do app deve custar o mínimo de cliques).
- **Contas recorrentes:** linha do tempo de vencimentos do mês com estado pago/pendente/atrasado.
- **Relatórios:** gráficos seguindo a skill `dataviz`, com a paleta deste documento.
