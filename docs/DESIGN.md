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
| `--chart-1` … `--chart-4` | `#26231E` · `#504D49` · `#7D7B78` · `#AEADAB` | `#EDE9E1` · `#BBB7B0` · `#8C8881` · `#5F5B56` | **marca de gráfico** (fatia, barra, amostra) — rampa de tinta, da maior para a menor; **derivados** de `--ink`/`--surface` (E6a, 17/09/2026) | 15,65 · 8,40 · 4,22 · 2,24 (claro) / 13,71 · 8,31 · 4,71 · 2,46 (escuro); a 4ª em *relief* com legenda + tabela |
| `--chart-pending` | `var(--warning)` | `var(--warning)` | **alias semântico**: fatia "Sem categoria" (dinheiro esperando decisão) — não é cor nova | 4,73:1 / 7,26:1 sobre surface |

Valores autorados em OKLCH em `tokens.css` (hex acima é referência). Derivações canônicas — **o dev não inventa outras**:

```css
hover sólido        color-mix(in oklch, var(--accent), var(--ink) 18%)
active sólido       color-mix(in oklch, var(--accent), var(--ink) 28%)
hover fantasma      color-mix(in oklch, var(--accent), transparent 88%)
texto desabilitado  color-mix(in oklch, var(--ink), var(--bg) 40%)
fundo de Alert      color-mix(in oklch, var(--{tom}), var(--surface) 92%)
fatia de gráfico k  var(--ink) · color-mix(in oklch, var(--ink), var(--surface) 22% | 44% | 66%)  (= --chart-1..4)
idem, contraste+    18% | 36% | 54%  (@media (prefers-contrast: more))
```

Tema por `data-theme="light|dark"` no `<html>`, com `@media (prefers-color-scheme: dark)` aplicado em `:root:not([data-theme])`; `:root { color-scheme: light dark }`. A preferência manual vive em `localStorage` (é preferência de UI, não token de sessão — permitido). **Sem `<script>` inline no `index.html`** por causa da CSP restritiva: aceita-se o flash de um frame na troca manual.

### Tipografia

- **Títulos e valores:** `"Fraunces", Georgia, serif` — serifada com personalidade, remete a livro-caixa. Valores monetários com `font-variant-numeric: tabular-nums`.
- **Texto e UI:** `"Public Sans", system-ui, sans-serif`.
- Escala: 13 / 15 (base) / 18 / 22 / 28 / 36 px. Peso: 400 texto, 600 destaque, 700 apenas em totais.
- **Exceção única de escala — `--text-12` (0.75rem)**: rótulo da **barra de navegação inferior do celular**, e nada mais. Nenhum texto de conteúdo, label, hint ou erro desce abaixo de 13 px. Entrou em 17/09/2026 porque seis itens a 320 px dão 49 px de célula e "Transferências" precisa de 51 px a 13 px para caber em duas linhas (medido em Chromium com Public Sans) — ver a emenda da barra inferior no fim deste documento.
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

- **Dashboard do mês (painel, `/`):** o "resumo do caderno" — saldo do mês, entradas vs saídas, próximos vencimentos, orçamentos estourando. Tabela e números, não mar de cards. Investimento aqui é **um** número: o líquido do mês (aportes − resgates), com sinal e podendo ser negativo — ao contrário de `/investimentos`, que não publica derivado nenhum (18/09/2026, `LICOES-FRONTEND.md`).
- **Lançamentos:** tabela densa, filtros discretos, adição rápida (a ação mais frequente do app deve custar o mínimo de cliques).
- **Contas recorrentes:** linha do tempo de vencimentos do mês com estado pago/pendente/atrasado.
- **Relatórios:** gráficos seguindo a skill `dataviz`, com a paleta deste documento.

## Padrões de tela — conciliação e decisão em lote (E2, 16/09/2026)

### Marcação de estado é palavra, não cor

Nenhum estado de conciliação (duplicata, repetição, rejeição) usa cor cromática. A cor
cromática do app tem três donos e mais ninguém: `--accent` (ação), `--income`/`--expense`
(dinheiro) e `--warning` (pendência de decisão humana — antes só "conta a vencer", ampliado
em 16/09/2026 para "algo esperando decisão sua").

Um estado se comunica por quatro portadores, nesta ordem de força:

1. **posição** — em qual bloco a linha está;
2. **palavra do grupo** — o cabeçalho do `<tbody>` explica o motivo uma vez, para o grupo;
3. **frase de evidência na linha** — o "por quê" específico, com data e valor;
4. **forma do controle** — `<select>` com palavras, checkbox, ou nenhum controle.

Teste de aceite: aplicar `filter: grayscale(1)` na tela. Se alguma decisão ficar ambígua,
a marcação está errada.

### Léxico fixo de conciliação

| Situação | Palavra |
|---|---|
| sem colisão | (sem marca; `sr-only` "Sem pendência") |
| 2ª ocorrência idêntica no mesmo arquivo | **2ª ocorrência** |
| já existe no sistema | **Já importada** |
| existe, mas foi excluído | **Já importada e excluída** |
| mesmo valor, data próxima, descrição diferente | **Possível duplicata** |
| pagamento/recebimento de fatura | **Pagamento de fatura** |
| linha malformada | **Linha inválida** |

### Blocos de decisão

Tela que pede decisão em lote se organiza por **trabalho a fazer**, não por status do dado:
`Precisam da sua decisão` (aberto, controle com palavras) → `Prontas` (aberto, checkbox) →
`Ficam de fora` (`<details>` fechado, **sem nenhum controle** — não use controle desabilitado,
que tem contraste ruim e some para parte das tecnologias assistivas).

A ação de confirmar diz o que vai acontecer no próprio rótulo:
`Importar 42 lançamentos · 6 ignorados`. Com nada marcado, o botão muda de rótulo e usa
`aria-disabled`, nunca `disabled`.

### Fluxo em passos (wizard)

Uma rota por passo. O indicador de passo é um `<ol>` à esquerda, não interativo, com
`aria-current="step"`. Cada passo move o foco para o próprio `<h1>` e publica o resultado
numa frase **visível** com `role="status"` — sem duplicata `sr-only`.

### Sinal do dinheiro

Em tabela onde receita e despesa convivem na mesma coluna, o valor usa sinal explícito
(`MoneyText sign="always"`): `+1.600,00` / `−11,00`. Cor é reforço, nunca o portador único.
Perna de transferência é **neutra**: não é receita nem despesa, e subtotal de dia não a soma —
quando o dia tem transferência, o cabeçalho do dia diz `· 1 transferência`, que é a explicação
de por que as linhas não fecham com o subtotal.

### Faixa de pendência

Dívida de dado (lançamento sem categoria, conta sem saldo de abertura) aparece como
`Alert tone="warning"` acima do conteúdo, com ação que **filtra** a própria tela para os itens
pendentes. Não é dispensável por "X" e some sozinha quando a contagem zera — dívida
dispensável é dívida invisível.

### Entrada de arquivo

O `<input type="file">` é sempre real, visível e rotulado. O `::file-selector-button` recebe o
estilo de botão secundário do projeto. Arrastar-e-soltar existe só como melhoria e grava no
`input.files` via `DataTransfer` — o input continua a fonte da verdade. Nunca um `<div>`
clicável com o input escondido.

## E2c — palavras-chave, revisão com sugestões e transferências (spec 0005, 17/09/2026)

Estende a seção E2 acima sem revogar nada dela: o teste do `grayscale(1)` continua sendo o
aceite, e os quatro portadores de estado (posição, palavra do grupo, frase de evidência na
linha, forma do controle) continuam sendo os únicos. Tudo o que segue é normativo para o
`dev-frontend-react`; medidas e cores só por token.

### Léxico — acréscimos

| Situação | Palavra |
|---|---|
| descrição bate (≥ 80) com palavra-chave de OUTRA conta ativa da casa | **Parece transferência** |
| a outra perna do par já existe no sistema | **Já registrada como transferência** |
| palavra-chave citada em qualquer texto (evidência, toast, erro, dica) | sempre entre aspas angulares: «padaria» |
| pontuação da correspondência | inteiro + `%`, em `tabular-nums`; **100 nunca aparece** — ausência de pontuação significa correspondência exata |

Duas regras novas de portador:

- **Pontuação é texto, nunca cor nem barra.** `88%` é um número e vai em `tabular-nums` como
  todo número do app; não ganha tom, ícone, barra de progresso nem "gradiente de confiança".
- **Sugestão vive no controle, não numa etiqueta.** A categoria sugerida já está selecionada no
  `<select>`; a linha abaixo dele diz de onde veio. Não existe `Badge` "sugerida".

### (a) `KeywordsField` — campo de palavras-chave

**Anatomia**, de fora para dentro:

1. `FieldShell` (`density="form"`, `label="Palavras-chave"`). O slot `labelAction` recebe o
   contador `<output aria-live="polite">3 de 20</output>` — `--text-13`, `--ink-muted`,
   `tabular-nums`. É o único texto do campo que muda sozinho; por isso é ele a live region, e só ele.
2. A **caixa**: um `<div>` que reproduz `.control` de `FieldShell.module.css` — fundo
   `--surface-sunken`, `border: 1px solid var(--border-strong)`, borda de baixo 2px,
   `--radius-sm`, `min-block-size: var(--control-h)` — com `display: flex; flex-wrap: wrap;
   align-items: center; gap: var(--space-1); padding: var(--space-1) var(--space-2)`.
   Foco: `.caixa:has(input:focus-visible)` recebe `outline: var(--focus-ring); outline-offset:
   var(--focus-offset)` e a borda de baixo em `--accent`; o `<input>` interno tem `outline: none`
   (um anel só, o da caixa). `[data-invalid="true"]` na caixa → `border-color: var(--danger)`.
3. Dentro da caixa, um `<ul>` (`display: contents`) com uma **ficha** por palavra e, depois dele,
   o `<input>`: `flex: 1 1 8ch; min-inline-size: 8ch`, sem borda, sem fundo, `--text-15`,
   `--font-ui`, `caret-color: var(--accent)`, **sem placeholder** — o exemplo está na dica.
4. **Ficha** (`<li>`): `display: inline-flex; align-items: center; block-size:
   var(--control-h-sm); padding-inline-start: var(--space-2); background: var(--surface);
   border: 1px solid var(--border); border-radius: var(--radius-sm)`; texto `--text-15`,
   `--font-ui`, `--ink`, a palavra **como foi digitada**. Sem cor cromática, sem ícone à esquerda.
   A ficha (36 px) cabe na caixa de 44 px com o `padding` de 4 px dos dois lados.
5. **Botão remover**, dentro da ficha: `<button type="button" tabIndex={-1}
   aria-label="Remover padaria">` com `CloseIcon size={14}`; `inline-size` e `block-size`
   `var(--control-h-sm)` (alvo de toque de 36 px, acima do mínimo AA); `color: var(--ink-muted)`;
   `border-radius: var(--radius-sm)`; hover `color: var(--ink)` + fundo `color-mix(in oklch,
   var(--accent), transparent 88%)`. **Nunca vermelho** — remover palavra-chave não é dinheiro
   nem erro.
6. **Ficha inválida** (`data-invalid="true"`): `border-color: var(--danger)` **e** `AlertIcon
   size={14}` em `--danger` antes do texto (erro nunca só por cor). O texto continua `--ink`. A
   explicação vai na mensagem da `FieldShell`, não na ficha.

**Dica fixa** (`hint`; hint OU erro, nunca os dois — regra da `FieldShell`):

- categoria: `Prefira o nome do estabelecimento — «padaria», «uber», «netflix». Enter ou vírgula adiciona.`
- conta: `Como esta conta aparece nos extratos das OUTRAS contas — «nubank», «pix c6». É o que detecta transferências.`

**Teclado** (lista de fichas com tabindex rodante, padrão APG):

- `Enter` e `,` adicionam o que está no input; `Enter` **nunca** submete o formulário a partir
  deste campo, com ou sem texto. Colar divide por vírgula e por quebra de linha e adiciona todas.
- `Backspace` com o input vazio remove a última ficha.
- `←` com o cursor no início do input leva o foco ao × da última ficha; `←`/`→` percorrem as
  fichas; `→` na última volta ao input. `Delete`, `Backspace`, `Enter` ou `Space` num × remove a
  ficha; o foco vai para a ficha seguinte, senão a anterior, senão o input.
- `Tab` sai do campo: os × têm `tabIndex={-1}`, porque vinte fichas não podem ser vinte paradas
  de Tab dentro de um diálogo.
- O `aria-describedby` do input aponta para três ids: o contador, a mensagem da `FieldShell` e um
  `sr-only` fixo `Use as setas para percorrer as palavras e Backspace para remover.`

**Estados**:

- *vazio*: só o input; a caixa tem a altura de um `TextField`. Contador `0 de 20`.
- *duplicata* (por `normalizarPalavra`): não adiciona, o input mantém o texto, mensagem
  `«padaria» já está na lista.` — some na próxima tecla.
- *inválida ao adicionar*: não adiciona, o input mantém o texto, mensagem com o motivo (tabela
  de copy em (g)); some na próxima tecla.
- *20 de 20*: input `readOnly` (nunca `disabled`: continua focável, e o `Backspace` continua
  removendo); contador em `--ink`, peso 600; dica trocada por `Limite de 20. Remova uma palavra
  para incluir outra.`
- *erro do servidor*: 400 `fields.keywords[i]` marca a ficha `i`; 409 `KEYWORD_TAKEN` marca a
  ficha cuja forma normalizada é igual a `fields.keyword`; a mensagem da `FieldShell` diz qual e
  por quê (copy em (g)). A ficha marcada continua removível; removê-la limpa o erro.

**Posição nos diálogos**: **último campo** do formulário, nos dois — `CategoryDialog` (Nome →
Natureza → notas de herança → Palavras-chave) e `AccountDialog` (… → Data do saldo →
Palavras-chave). É o campo opcional e avançado; por último, não interrompe quem só quer dar um
nome. Aparece em todos os modos do diálogo de categoria.

### (b) Sugestão de categoria na revisão

Na célula "Categoria" de `BlocoProntas`, `BlocoDecisao` e do bloco de transferências (ação `import`):

- O `<select>` compacto vem com `suggestedCategoryId` selecionado. O placeholder da opção vazia
  passa de `—` para **`Sem categoria`**, em todos os blocos: com sugestão em jogo, escolher a opção
  vazia é uma decisão ("entrar sem categoria apesar da sugestão", `categoryId: null`), e um
  travessão não diz isso.
- **Abaixo** do select, na mesma célula (`display: flex; flex-direction: column; gap: 2px;
  align-items: start`), a **linha de proveniência**: `<span>` `--text-13`, `--ink-muted`,
  `tabular-nums`, `white-space: nowrap`, com prefixo `sr-only` `Sugerida pela palavra-chave ` e
  texto visível `88% · «supermercado»`. Com 100: apenas `«supermercado»`.
- A linha existe **enquanto** o valor do select for igual a `suggestedCategoryId`. Trocar ou
  limpar a apaga; voltar à sugerida a traz de volta. Ela descreve a proveniência do que está
  selecionado, não um histórico.
- Abaixo de 40rem a coluna some (`hideBelow: 'sm'`) — e a sugestão **vai ser gravada**. Então
  `CelulaDescricao` ganha, só nessa faixa (o mecanismo de `.secundaria` de `/lancamentos`), a
  linha `Categoria: Alimentação · sugerida` (ou `Categoria: Alimentação` quando escolhida à mão;
  nada quando sem categoria). Esconder a coluna só é honesto se o dado reaparece.

### (c) Bloco "Transferências detectadas"

**Posição**: segundo bloco — entre `Precisam da sua decisão` e `Prontas para importar`. A ordem é
um gradiente de trabalho: perguntas abertas (bloco 1) → perguntas **com resposta proposta** (este)
→ conferência (prontas) → auditoria (fora). A linha de contagem do topo ganha o quarto item:
`3 precisam da sua decisão · 5 transferências detectadas · 42 prontas para importar · 2 ficam de fora.`

**Cabeçalho** (`Panel title="Transferências detectadas" padding="none"`, slot `actions`):
`Badge tone="warning"` com a contagem de `transferencia_interna` (só elas esperam decisão;
`transferencia_ja_registrada` já resolve sozinha por `link`) e o botão
**`Button variant="secondary" size="sm"`** `Aceitar as 5 transferências sugeridas` (singular:
`Aceitar a transferência sugerida`). Secundário, e não quiet como "Marcar todas": aqui o botão
**é** o caminho principal do bloco, não um atalho redundante. Ele marca `transfer` com a
contraparte sugerida em **todas** as `transferencia_interna` deste bloco e em nenhuma outra linha.
Quando todas já estão em `transfer`, vira `quiet` `Desfazer o aceite de todas` (volta a `skip`).
Some quando o bloco não tem `transferencia_interna`.

**Apoio** (`.apoio`, como nos outros blocos): `A descrição bate com a palavra-chave de outra conta
da casa. Registrada como transferência, a linha não conta como receita nem como despesa — o
dinheiro só mudou de conta.`

**Grupos** (`DataTable groups`, as mesmas cinco colunas de `BlocoDecisao`: Data · Descrição ·
Valor · Decisão · Categoria):

- `Parece transferência · 5` — descrição do grupo: `Não entra sem você confirmar. Se não for
  transferência, importe como despesa ou receita comum.`
- `Já registrada como transferência · 2` — descrição: `A outra conta já registrou este par.
  Vincular não cria lançamento: só marca esta linha como importada, para ela não voltar como nova.`

**Frase de evidência** (`evidenciaDaLinha`, texto puro como hoje):

- `transferencia_interna`: `Parece transferência para Nubank · 88% · «nubank»`; com 100:
  `Parece transferência para Nubank · «nubank»`.
- `transferencia_ja_registrada`: `Já registrada em 05/09 como transferência com Nubank.`; sem
  `matchOccurredOn`: `Já registrada como transferência com Nubank.`
- `pagamento_de_fatura` com `suggestedCounterpartAccountId`: `Pagamento da fatura de um cartão ·
  parece o Cartão Nubank · «nubank»` (sem pontuação: o contrato não a expõe neste status).

**Célula de decisão** — a contraparte sugerida mora **no texto da opção**, não num segundo
select pré-preenchido:

- `transferencia_interna` (default `skip`, por isso primeira): `Não importar` · `Registrar como
  transferência para Nubank` · `Registrar como transferência para outra conta…` · `Importar como
  despesa comum (não é transferência)` / `Importar como receita comum (não é transferência)`,
  conforme `kind`.
- `pagamento_de_fatura` **com** sugestão: `Não importar` · `Registrar como transferência para
  Cartão Nubank` · `Registrar como transferência para outro cartão…` · `Importar como despesa mesmo
  assim`. Sem sugestão: como hoje (`Registrar como transferência para…` + segundo select).
- `transferencia_ja_registrada` (default `link`): `Vincular à transferência já registrada` ·
  `Não importar`. Sem categoria, sem contraparte.
- As duas opções "Registrar…" são a mesma ação `transfer`; a diferença é `contraparteId`
  (sugerida vs a escolher). Só a opção com reticências revela o segundo select (placeholder
  `Escolha a conta`, erro `Escolha a conta de destino.` se ficar vazio) — as reticências continuam
  significando "falta escolher", exatamente como hoje.
- A coluna Categoria segue a regra de `BlocoDecisao`: `<select>` só com ação `import`, senão `—`.

**Contagens e rótulo do confirmar**: `link` não entra em `vaoEntrar` nem em `ignorados`; vira
`vinculadas`. Rótulo: `Importar 42 lançamentos · 2 vinculados · 6 ignorados`; só vínculos:
`Vincular 2 lançamentos`; nada: `Nada marcado para importar`. Nuance: `Inclui 5 transferências e
2 vínculos a transferências já registradas.` No passo 3, linha `Vinculadas a transferências já
registradas` na `<dl>` e, na frase, `2 vinculadas a transferências que já existiam`.

### (d) Fichas de aprender

**Quando**: numa linha com ação `import`, **sem** `suggestedCategoryId`, no momento em que a
pessoa escolhe uma categoria no select. Somem quando a categoria volta a `Sem categoria`. Não
aparecem em linha com sugestão (mesmo trocada): a linha já ensina sozinha.

**Onde**: na célula **Descrição**, abaixo da descrição (e da evidência, se houver) — as palavras
são da descrição, e é a única coluna que absorve largura. A ligação com a categoria é dita no
nome acessível de cada ficha.

**Forma**: uma linha `display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1);
margin-block-start: var(--space-1)` com o rótulo `Da próxima vez, reconhecer por` (`--text-13`,
`--ink-muted`) seguido das fichas. Cada ficha é **`Button variant="quiet" size="sm"`** com
`iconStart={<PlusIcon size={14} />}`, texto = a palavra (`mercado`),
`aria-label="Adicionar «mercado» às palavras-chave de Alimentação"`. Nenhum estilo novo: é o
botão do sistema.

**Quais e quantas**: `tokenizar(description)` na ordem em que aparecem; ficam de fora tokens só
de dígitos, com mais de 40 runas ou já presentes nas palavras-chave da categoria escolhida (cache).
**Máximo 5.** Nenhuma ficha quando a categoria já tem 20 palavras.

**Ciclo**: clique → `loading` no botão (spinner no lugar do +, foco preservado) → `PATCH` com a
lista inteira lida do cache → sucesso: a ficha vira texto estático `CheckIcon size={14}` +
`mercado` (`--ink-muted`, sem controle), o foco vai ao `<select>` de categoria da linha, toast
`«mercado» adicionada a Alimentação. Vale a partir da próxima importação.`, invalida `categories`
(as outras linhas com o mesmo token perdem a ficha). 409 → toast de erro `«mercado» já está em
Padaria.`; outro erro → toast de erro genérico. Nunca re-analisa o lote.

### (e) `AutoCategorizeDialog`

**Gatilho**: na faixa de pendência de `/lancamentos` (`Alert tone="warning"`), o slot `action`
passa a ter dois botões (`display: flex; gap: var(--space-2); flex-wrap: wrap`):
`Button variant="primary" size="sm"` **`Categorizar automaticamente`** e o atual `Ver só esses 12`
(secondary sm). O mesmo botão primário entra na faixa `info` do filtro ativo. Aparece sempre que
há pendência — se a casa não tem palavras-chave, é o diálogo que ensina.

**Forma**: `Dialog` nativo, largura padrão (34rem). `title="Categorizar automaticamente"`,
`description="{Mês por extenso} · aplica as palavras-chave das categorias aos lançamentos sem
categoria. O que já tem categoria não muda."`

**Corpo**, de cima para baixo:

1. Frase-resumo visível com `role="status"` (`--text-15`, `--ink`): `12 de 18 recebem
   categoria.` (singular: `1 de 18 recebe categoria.`).
2. **Uma** `DataTable` com dois grupos e duas colunas — `Descrição` (1fr, ellipsis + `title`) e
   `Categoria` (`width: 'min'`):
   - grupo `Recebem categoria · 12`: célula Categoria = nome da categoria em `--ink` e, abaixo, a
     linha de proveniência de (b) (`88% · «supermercado»`).
   - grupo `Continuam sem categoria · 6`, descrição do grupo: `Abaixo de 80% de semelhança o app
     não arrisca; num empate entre duas categorias, também não. Uma palavra-chave mais específica
     resolve os dois casos.` Célula Categoria = o motivo em `--ink-muted`: `abaixo de 80%`
     (`below_threshold`) ou `empate entre categorias` (`ambiguous`).
   - Lista cortada em 500: última linha do grupo, em `--ink-muted`, `Mostrando as primeiras 500.`
3. Rodapé: `Cancelar` (quiet) e `Categorizar 12 lançamentos` (primary; singular `Categorizar 1
   lançamento`). Com `categorized === 0`: rótulo `Nada a categorizar` + `aria-disabled`.

**Estados**: *carregando* — abre já com título e descrição, frase `Conferindo as palavras-chave
de setembro…` (`role="status"`) e a `DataTable loading`; *erro* — `Alert tone="error"` com
`Tentar de novo`; *vazio* (`categorized === 0`) — `EmptyState title="Nenhum lançamento receberia
categoria." description="As palavras-chave cadastradas não batem com as descrições destes 18
lançamentos. Cadastre nas categorias o nome do estabelecimento como ele aparece no extrato."`
com `action` = `Button variant="secondary"` `Ir para categorias`, e abaixo um `<details>` `Ver os
18 lançamentos e o motivo` com o grupo dos sem categoria; *sucesso* — fecha, toast `12
lançamentos categorizados.` com o número **real** do servidor (0 → `Nada foi categorizado — os
lançamentos já tinham categoria.`), invalida `transactions`.

### (f) Tela `/transferencias`

**Casca**: item `Transferências` na navegação, logo depois de `Lançamentos`, ícone
`TransfersIcon`. `document.title` `Transferências · HomeFinance`. Largura `62rem`, mesmo
cabeçalho de `/lancamentos`: `<h1>` `Transferências` (foco na troca de rota) e apoio `O que mudou
de conta dentro da casa em {mês}.`

**Ícone `TransfersIcon`** (`components/icons/`, contrato de `types.ts`, traço 1.5,
`currentColor`, viewBox 24): duas setas horizontais empilhadas em sentidos opostos — a de cima
aponta para a direita, a de baixo para a esquerda. Haste superior `M4.75 9h14.5`, ponta
`m15.25 5.25 4 3.75-4 3.75`; haste inferior `M19.25 15h-14.5`, ponta `m8.75 11.25-4 3.75 4 3.75`.
Mesma massa óptica de `LedgerIcon`.

**Faixa de filtros** (a mesma `.faixa` de `/lancamentos`, dentro de `Panel padding="none"`):
dois `Select density="compact"` — `label="Conta"`, placeholder `Todas as contas` (`?conta`) e,
**só quando há conta**, `label="Outra conta"`, placeholder `Qualquer conta` (`?contraparte`), sem
a conta já escolhida entre as opções. Entre os dois, a palavra `e` em `--text-13`, `--ink-muted`,
para a faixa ler "Conta … e outra conta …". `contraparte` sem `conta` é descartado na validação
da busca (`search.ts`). À direita da faixa, com uma conta só: `Saldo da Nubank no fim de setembro`
+ `MoneyText tone="semantic"`.

**Painel do par** (só com as duas contas; abaixo da faixa, `border-block-end: 1px solid
var(--border)`, `padding: var(--space-4)`): grid de duas colunas (`grid-template-columns:
minmax(0, 1fr) minmax(0, 1fr); gap: var(--space-5)`; uma coluna abaixo de 40rem), cada uma uma
`<dl>` no estilo de `ImportResultScreen.module.css` (`dt` `--ink-muted`, `dd` = `MoneyText`,
linhas separadas por `--border`):

- Esquerda, **orientada pela conta do filtro (X)** e não pelo `A` do servidor: `Nubank → C6`
  `2.500,00` · `C6 → Nubank` `800,00` · `Líquido` `+1.700,00` (`MoneyText sign="always"
  emphasis="total"`, **neutro** — transferência não é receita nem despesa). O sinal é relativo a
  X→Y; o mapeamento a partir de `aToBCents`/`bToACents`/`netCents` é por id, e um teste garante
  que o líquido mostrado é `±netCents`.
- Direita, título `Saldo no fim de setembro` (`--text-13`, peso 600, `--ink-muted`): `Nubank`
  `4.120,00` · `C6` `−1.980,50` (`tone="semantic"`, como em `/contas` — saldo é posição).
- Abaixo do grid, a **frase de direção** (`--text-15`, `--ink`, nomes e valor em `<strong>`):
  `Nubank enviou R$ 1.700,00 a mais para C6 em setembro.` — sempre na voz de quem mandou mais;
  líquido zero: `As duas contas se equilibraram em setembro: o que foi, voltou.`
- Carregando: `Skeleton width="4.5rem"` no lugar de cada número; a frase só aparece com o dado.

**Tabela de pares** (sem as duas contas): `DataTable caption="Pares de contas com transferência
no mês"`, colunas `Contas` · `Enviado` (end) · `Recebido` (end) · `Líquido` (end) ·
`Transferências` (end, min). A célula `Contas` é um **`TextLink`** para a própria rota com
`?conta&contraparte` (navegar é trabalho de link), texto `Nubank → C6` com `ArrowRightIcon
size={14}` entre os nomes e `aria-label="Ver as transferências entre Nubank e C6"`. **A seta é
normalizada para quem mandou mais**: `Enviado` = valor no sentido da seta, `Recebido` = no
sentido contrário, `Líquido` = diferença, sempre ≥ 0 e sem sinal; empate → ordem do servidor e
`Líquido` `0,00`. Tudo neutro.

**Tabela de itens** (`DataTable caption="Transferências de {mês}"`, lista plana na ordem do
servidor): `Data` (min, `dataCurta`) · `Contas` (min, `Nubank → C6` com `ArrowRightIcon
size={14}`, `sr-only` `de Nubank para C6`) · `Descrição` (1fr, ellipsis) · `Valor` (end, min,
`MoneyText` neutro sem sinal — a direção está em `Contas`) · `Registro` (min, `hideBelow: 'sm'`,
texto `Manual` / `Importação`). Abaixo de 40rem, `Registro` reaparece como segunda linha da
descrição. Sem agrupamento por dia e sem subtotal: transferência não soma. Rodapé de paginação
igual ao de `/lancamentos`.

**Vazios**: sem filtro — `EmptyState title="Nenhuma transferência entre as suas contas em
setembro de 2026." description="Transferências aparecem aqui quando um Pix entre as suas contas
ou o pagamento de uma fatura é registrado como transferência — na importação, o app as detecta
pelas palavras-chave das contas."` com `action` = `Button variant="primary"` `Importar extrato`.
Com conta: `Nenhuma transferência envolvendo a Nubank em setembro.` + `Mostrar todas as contas`.
Com par: `Nenhuma transferência entre Nubank e C6 em setembro.` + `Mostrar todos os pares`.
Erro: `Alert tone="error" title="Não foi possível carregar as transferências."` + `Tentar de novo`.

### (g) Copy pt-BR — tabela única

| Onde | Texto |
|---|---|
| Rótulo do campo | `Palavras-chave` |
| Contador do campo | `3 de 20` (`aria-live="polite"`) |
| Dica — categoria | `Prefira o nome do estabelecimento — «padaria», «uber», «netflix». Enter ou vírgula adiciona.` |
| Dica — conta | `Como esta conta aparece nos extratos das OUTRAS contas — «nubank», «pix c6». É o que detecta transferências.` |
| Dica em 20/20 | `Limite de 20. Remova uma palavra para incluir outra.` |
| Instrução `sr-only` | `Use as setas para percorrer as palavras e Backspace para remover.` |
| Botão remover (`aria-label`) | `Remover padaria` |
| Duplicata local | `«padaria» já está na lista.` |
| Curta demais | `Use ao menos 2 letras ou números.` |
| Longa demais | `No máximo 40 caracteres.` |
| Caractere fora da lista | `Só letras, números, espaço e & . - / '` |
| Só palavras vazias / letras soltas | `Essa palavra é comum demais para reconhecer um lançamento — use o nome do estabelecimento.` |
| 400 `fields.keywords[i]` | `«xyz» não é uma palavra-chave válida: de 2 a 40 caracteres, só letras, números, espaço e & . - / '` |
| 409 categoria | `«padaria» já está em Alimentação.` · sem `ownerId` resolvível: `«padaria» já está em outra categoria desta casa.` |
| 409 conta | `«nubank» já está na conta Nubank.` · sem dona: `«nubank» já está em outra conta desta casa.` |
| Placeholder do select de categoria (revisão) | `Sem categoria` |
| Linha de proveniência | `88% · «supermercado»` · com 100: `«supermercado»` · prefixo `sr-only` `Sugerida pela palavra-chave ` |
| Secundária no celular | `Categoria: Alimentação · sugerida` / `Categoria: Alimentação` |
| Contagem do topo da revisão | `3 precisam da sua decisão · 5 transferências detectadas · 42 prontas para importar · 2 ficam de fora.` |
| Título do bloco | `Transferências detectadas` |
| Apoio do bloco | `A descrição bate com a palavra-chave de outra conta da casa. Registrada como transferência, a linha não conta como receita nem como despesa — o dinheiro só mudou de conta.` |
| Botão do bloco | `Aceitar as 5 transferências sugeridas` / `Aceitar a transferência sugerida` / `Desfazer o aceite de todas` |
| Grupo `transferencia_interna` | `Parece transferência · 5` — `Não entra sem você confirmar. Se não for transferência, importe como despesa ou receita comum.` |
| Grupo `transferencia_ja_registrada` | `Já registrada como transferência · 2` — `A outra conta já registrou este par. Vincular não cria lançamento: só marca esta linha como importada, para ela não voltar como nova.` |
| `PALAVRA_DO_STATUS` | `transferencia_interna` → `Parece transferência` · `transferencia_ja_registrada` → `Já registrada como transferência` |
| Evidência `transferencia_interna` | `Parece transferência para Nubank · 88% · «nubank»` / `Parece transferência para Nubank · «nubank»` |
| Evidência `transferencia_ja_registrada` | `Já registrada em 05/09 como transferência com Nubank.` / `Já registrada como transferência com Nubank.` |
| Evidência fatura com contraparte | `Pagamento da fatura de um cartão · parece o Cartão Nubank · «nubank»` |
| Ação `transfer` com sugestão | `Registrar como transferência para Nubank` |
| Ação `transfer` a escolher | `Registrar como transferência para outra conta…` (fatura: `…para outro cartão…`) |
| Ação `import` em `transferencia_interna` | `Importar como despesa comum (não é transferência)` / `Importar como receita comum (não é transferência)` |
| Ação `link` | `Vincular à transferência já registrada` |
| Ação `skip` | `Não importar` |
| Rótulo do confirmar | `Importar 42 lançamentos · 2 vinculados · 6 ignorados` · `Vincular 2 lançamentos` · `Nada marcado para importar` |
| Nuance | `Inclui 5 transferências e 2 vínculos a transferências já registradas.` |
| Passo 3 | linha `Vinculadas a transferências já registradas`; frase `… e 2 vinculadas a transferências que já existiam.` |
| Rótulo das fichas de aprender | `Da próxima vez, reconhecer por` |
| Ficha de aprender (`aria-label`) | `Adicionar «mercado» às palavras-chave de Alimentação` |
| Toast aprender | `«mercado» adicionada a Alimentação. Vale a partir da próxima importação.` |
| Toast aprender 409 | `«mercado» já está em Padaria.` |
| Botão na faixa | `Categorizar automaticamente` |
| Título / descrição do diálogo | `Categorizar automaticamente` / `Setembro de 2026 · aplica as palavras-chave das categorias aos lançamentos sem categoria. O que já tem categoria não muda.` |
| Resumo | `12 de 18 recebem categoria.` / `1 de 18 recebe categoria.` |
| Carregando | `Conferindo as palavras-chave de setembro…` |
| Grupos do diálogo | `Recebem categoria · 12` · `Continuam sem categoria · 6` — `Abaixo de 80% de semelhança o app não arrisca; num empate entre duas categorias, também não. Uma palavra-chave mais específica resolve os dois casos.` |
| Motivos | `below_threshold` → `abaixo de 80%` · `ambiguous` → `empate entre categorias` |
| Corte em 500 | `Mostrando as primeiras 500.` |
| Confirmar | `Categorizar 12 lançamentos` / `Categorizar 1 lançamento` / `Nada a categorizar` |
| Vazio do diálogo | `Nenhum lançamento receberia categoria.` — `As palavras-chave cadastradas não batem com as descrições destes 18 lançamentos. Cadastre nas categorias o nome do estabelecimento como ele aparece no extrato.` — `Ir para categorias` — `Ver os 18 lançamentos e o motivo` |
| Toast do auto-categorize | `12 lançamentos categorizados.` / `1 lançamento categorizado.` / `Nada foi categorizado — os lançamentos já tinham categoria.` |
| Navegação | `Transferências` |
| `<h1>` e apoio | `Transferências` / `O que mudou de conta dentro da casa em setembro.` |
| Filtros | `Conta` (`Todas as contas`) · `e` · `Outra conta` (`Qualquer conta`) |
| Saldo com uma conta | `Saldo da Nubank no fim de setembro` |
| Painel do par | `Nubank → C6` · `C6 → Nubank` · `Líquido` · `Saldo no fim de setembro` |
| Frase de direção | `Nubank enviou R$ 1.700,00 a mais para C6 em setembro.` / `As duas contas se equilibraram em setembro: o que foi, voltou.` |
| Colunas dos pares | `Contas` · `Enviado` · `Recebido` · `Líquido` · `Transferências` |
| Link do par (`aria-label`) | `Ver as transferências entre Nubank e C6` |
| Colunas dos itens | `Data` · `Contas` · `Descrição` · `Valor` · `Registro` (`Manual` / `Importação`) |
| Vazios | `Nenhuma transferência entre as suas contas em setembro de 2026.` — `Transferências aparecem aqui quando um Pix entre as suas contas ou o pagamento de uma fatura é registrado como transferência — na importação, o app as detecta pelas palavras-chave das contas.` — `Importar extrato` · `Nenhuma transferência envolvendo a Nubank em setembro.` — `Mostrar todas as contas` · `Nenhuma transferência entre Nubank e C6 em setembro.` — `Mostrar todos os pares` |
| Erro da tela | `Não foi possível carregar as transferências.` — `Tentar de novo` |

As strings do atalho de categorização em `/lancamentos` estão na tabela própria de (h).

### (h) Atalho de categorização em `/lancamentos` (emenda §11 da spec 0005, 17/09/2026)

**O que é**: a célula `Sem categoria` de uma linha `income`/`expense` vira um controle de
revelação (padrão APG *disclosure*) que abre, **na própria linha**, um editor com o `<select>` de
categoria, as fichas de aprender de (d) e **um único** botão de confirmar cujo rótulo diz o que vai
acontecer. Duas saídas: só este lançamento; ou este + palavra-chave na categoria + reprocessar os
sem categoria do mês.

**Emenda §19 da spec 0005 (18/09/2026) — trocar a categoria de uma linha já categorizada.** O
pedido: "a qualquer momento eu manualmente alterar um lançamento de categoria". A célula de uma
linha categorizada é o **mesmo controle** no **outro estado fechado** (§1-bis): o nome da categoria
com o chevron, que abre o **mesmo editor** em modo *trocar* (§2, §3), com o `<select>` já na
categoria atual. Um componente, dois estados fechados, um aberto — e o backend já substitui
(`PATCH /transactions/{id}` com `categoryId` troca, não só preenche).

**Decisão de forma: a linha se expande. Não é popover, não é diálogo.** Três razões, nesta ordem:

1. A §11.1 pede "na própria linha". Uma camada flutuante sobre a tabela não é a linha.
2. A regra "Popover API para menus, dropdowns e seletores" não cobre um mini-formulário com
   `<select>`, cinco fichas e dois botões. *Anchor positioning* não está na lista de CSS seguro
   deste documento, então ancorar a camada à célula exigiria posicionamento por JavaScript —
   reimplementação — e o *light dismiss* jogaria fora uma escolha pela metade num clique distraído.
3. É a anatomia que a pessoa já conhece da revisão da importação (T11: `<select>` compacto +
   `Da próxima vez, reconhecer por`), agora com um confirmar por linha e com a descrição — de onde
   as palavras saem — à vista, na mesma linha.

Componente da feature: `features/transactions/components/AtalhoDeCategoria.tsx` (+ `.module.css`)
— exporta o **botão da célula** e o **editor**; a tela liga os dois pelo `editandoId`.

#### 1. O controle na célula — a lacuna

Não é variante do `Button`: é o quarto portador de estado (forma do controle) — uma lacuna a
preencher, não uma ação a disparar.

- `<button type="button">` com texto visível **`Sem categoria`** e `ChevronDownIcon size={14}`
  à direita (`gap: var(--space-1)`).
- Forma: `display: inline-flex; align-items: center; min-block-size: var(--control-h-sm);
  padding-inline: var(--space-2); border: 1px dashed var(--border-strong); border-radius:
  var(--radius-sm); background: transparent; color: var(--ink-muted); font: 400 var(--text-13)
  var(--font-ui); white-space: nowrap`. Tracejado é a linguagem que o `Badge tone="muted"` já usa
  para "vazio" — mas em `--border-strong`, não `--border`: é controle e precisa de 3:1 (4,08:1).
  Peso 400 de propósito: é estado, não chamada. Cabe na altura atual da linha (36 px < 42 px).
- Hover (`@media (hover: hover)`): `color: var(--ink)` e fundo `color-mix(in oklch, var(--accent),
  transparent 88%)`. Foco: `outline: var(--focus-ring); outline-offset: var(--focus-offset)`.
  Aberto (`[aria-expanded="true"]`): `border-style: solid; color: var(--ink)`, fundo fantasma e o
  chevron em `rotate: 180deg` (`transition: rotate var(--motion-base) var(--ease)`; sem transição
  em `prefers-reduced-motion: reduce`).
- ARIA: `aria-expanded`; `aria-controls={idDoEditorDeCategoria(linha.id)}` **só quando aberto** (o
  editor não existe no DOM fechado); `aria-label="Sem categoria. Categorizar {rotuloDaLinha}"` —
  o texto visível está contido no nome (WCAG 2.5.3) e o nome diz de qual linha é: dezoito botões
  "Sem categoria" iguais não dizem nada a quem navega por lista de controles.
- Onde **não** aparece: linha de transferência (`Badge` `Transferência`, como hoje) e linha já
  categorizada — que, desde a emenda §19, tem o **outro estado fechado do mesmo controle** (§1-bis),
  não a lacuna. O tracejado é **exclusivo da pendência**: é o sinal de "dinheiro esperando decisão",
  e uma linha resolvida não o usa. Sob `?semCategoria=1` toda linha é lacuna; sob
  `?tipo=investimentos` nenhuma é (aporte e resgate são definidos pela categoria).
- Atributos de dados, nos **dois** estados: `data-atalho={id}` (é por ele que o foco **volta à
  linha**, em qualquer estado) e `data-instancia="coluna" | "secundaria"` (para o CSS de alinhamento
  do §1-bis). **Só na lacuna**: `data-lacuna` — é por ele que "a próxima lacuna" do §4 é procurada
  (`button[data-lacuna]`), nunca por `data-atalho`, que agora também está nas categorizadas.
- **Celular (< 40rem)**: a coluna Categoria some (`hideBelow: 'sm'`) e a `.secundaria` da descrição
  passa de `Nubank · Sem categoria` para `Nubank ·` + **o mesmo botão** (a `.secundaria` vira
  `display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1)`). São duas instâncias
  no DOM, uma por faixa — exatamente uma visível em qualquer largura, o mecanismo que hoje já
  duplica o nome da conta. Ids distintos (`atalho-categoria-{id}-coluna` / `-secundaria`) e
  `data-atalho={id}` nas duas; quem devolve o foco procura `[data-atalho="{id}"]` e escolhe a
  instância com `checkVisibility()`.

#### 1-bis. A linha categorizada — o outro estado fechado (emenda §19 da spec 0005, 18/09/2026)

A resposta ao pedido "alterar a categoria a qualquer momento" não é um segundo controle: é o
**mesmo** controle da célula, com **dois estados fechados e um aberto**. Fechado, a lacuna e a
linha categorizada diferem — uma é pendência, a outra é dado. Aberto, são idênticos: a pessoa está
editando a categoria da linha, e isso é uma coisa só. Regra curta: **fechado difere, aberto é o
mesmo.**

**Fechado, categorizada** (`.categorizada`): o nome da categoria com o chevron. A tinta é a do
`<span>` que estava ali — a densidade visual da tabela não muda, e a coluna continua sendo lida como
coluna de dados, não como coluna de botões.

- `<button type="button">` com texto visível **`{categoryName}`** e `ChevronDownIcon size={14}` à
  direita, no mesmo `.chevron` da lacuna (`gap: var(--space-1)`). O chevron é a linguagem que a
  lacuna e o `Select` já usam para "abre aqui", e funciona no toque.
- Forma: a **mesma caixa** da lacuna (`display: inline-flex; align-items: center; min-block-size:
  var(--control-h-sm); padding-inline: var(--space-2); border-radius: var(--radius-sm); background:
  transparent; white-space: nowrap`) com **`border: 1px solid transparent`** — a borda existe e não
  se vê; reserva a caixa para nada pular ao abrir. `font: inherit; color: inherit` (a `reset.css` já
  faz isso para `<button>`, mas fica declarado): na célula herda `--text-15` e `--ink` do `.table`,
  exatamente o que o `<span>` tinha; na `.secundaria` herda `--text-13` e `--ink-muted`, exatamente
  o que o texto tinha. Peso 400 nos dois: é dado. Pintar o nome de `--ink` na secundária, onde a
  linha inteira é `--ink-muted`, faria o nome saltar do `Nubank ·` ao lado e usaria tom como
  affordance — esse trabalho é do chevron.
- **Chevron** `color: var(--ink-muted)` em repouso, **sempre visível** — é o único portador da
  affordance e precisa existir onde não há hover. Contraste sobre `--surface`: ≈ 5,6:1 no claro,
  ≈ 6,1:1 no escuro; sobre o fantasma do hover, ≈ 5,0:1 — folga sobre o 3:1 de elemento de UI. Em
  hover e aberto, `color: inherit` (acompanha o texto, que vai a `--ink`).
- Hover (`@media (hover: hover)`): `color: var(--ink)` e fundo `color-mix(in oklch, var(--accent),
  transparent 88%)` — os mesmos da lacuna. Foco: `outline: var(--focus-ring); outline-offset:
  var(--focus-offset)`, e só isso (o anel é o sinal; o chevron não muda no foco). **O fundo diz
  "pode"; a borda diz "está aberto"** — hover não acende borda.
- **Aberto** (`[aria-expanded="true"]`): **idêntico à lacuna aberta** — `border-style: solid;
  border-color: var(--border-strong); color: var(--ink)`, fundo fantasma, chevron em `rotate:
  180deg` com a mesma transição. No CSS isso é literal: a caixa e o aberto moram numa classe base
  `.atalho`, e `.lacuna` / `.categorizada` só dizem o que difere fechado. A única diferença que
  sobra no aberto é o corpo do texto (13 px "Sem categoria" × 15 px "Alimentação"), e ela é a mesma
  diferença entre placeholder e valor num `<select>`.
- **Alinhamento na coluna**: o `padding-inline` mais a borda deslocariam o nome 9 px para a direita
  do cabeçalho `Categoria` e do texto das outras colunas — a única coluna com conteúdo recuado do
  cabeçalho, inaceitável numa tabela deste produto. Regra: **caixa visível alinha a caixa; caixa
  invisível alinha o texto.** A `Badge Transferência` e a lacuna têm caixa visível → a caixa encosta
  em `--cell-pad` e o texto fica a 9 px, como hoje. A categorizada tem caixa invisível → recua a
  caixa: `.categorizada[data-instancia="coluna"] { margin-inline-start: calc(-1 * (var(--space-2)
  + 1px)) }`. O nome fica onde o `<span>` estava; a caixa (fantasma no hover, sólida aberta) começa
  7 px depois da borda da célula. Nada muda no `DataTable`. **Sem** margem negativa na
  `.secundaria`: ali não há cabeçalho para alinhar, e o nome fica a 9 px do `·`, à mesma distância
  do `Sem categoria` da lacuna — as linhas do celular ficam iguais entre si.
- **Celular (< 40rem)**: o mecanismo do §1 — a instância `secundaria` (`Nubank ·` +
  `[Alimentação ⌄]`), com `white-space: normal; text-align: start` **só nela**: um nome de até 60
  caracteres quebra dentro do botão em vez de estourar a moldura. Custo medido e aceito: a
  `.secundaria` de toda linha categorizada passa a ter 36 px (`--control-h-sm`) em vez de ≈ 20 px —
  ≈ +16 px por linha, a altura que a linha pendente **já tinha**. Em troca as linhas do celular
  ficam uniformes e o alvo de toque é o piso do projeto, sem truque de área invisível.
- ARIA: `aria-label="{categoryName}. Trocar categoria de {rotuloDaLinha}"` (o texto visível está
  contido no nome — WCAG 2.5.3 — e o nome diz de qual linha é); `aria-expanded`; `aria-controls`
  **só aberto**, como na lacuna. Sem `title`.
- Componente: o botão da célula passa a decidir o estado por `linha.categoryId === null` (lacuna)
  ou não (categorizada) — um componente, duas classes (`styles.atalho` + `styles.lacuna` /
  `styles.categorizada`); `podeCategorizar` passa a significar "tem controle de categoria", que é
  "não é transferência".

**O que foi rejeitado, e por quê**

- *Chevron só em hover/foco + `text-decoration: underline dotted` em repouso.* Pontilhado é a
  linguagem de `<abbr>` e de dica; no toque não há hover, então o pontilhado seria a única
  affordance — e um traço interrompido em toda linha categorizada dilui o tracejado, que precisa
  continuar sendo **só** pendência. Em `grayscale(1)`, "pontilhado embaixo" e "tracejado em volta"
  viram a mesma família.
- *Chevron com opacidade menor em repouso, plena no hover.* Para ficar ≥ 3:1 sobre `--surface` no
  tema claro, `--ink-muted` não pode descer de **0,82** de opacidade — a diferença é imperceptível.
  Não compra silêncio e cria uma terceira tinta.
- *Sem chevron em repouso no desktop.* O pedido é "a qualquer momento"; um nome que só revela que é
  botão no hover não é encontrável. A preocupação real — "dezoito setas em coluna" — se resolve pela
  geometria: a coluna é `width: min` e o chevron acompanha o fim de cada nome; nomes de comprimentos
  diferentes deixam as setas desalinhadas, um sufixo do nome, não uma coluna de setas. Se a tela
  tiver dezoito linhas com o mesmo nome, é uma coluna de `Alimentação ⌄`, e isso é verdade.

**CSS normativo** (`AtalhoDeCategoria.module.css`). A `.lacuna` de hoje é reorganizada em base +
variante **sem mudar um pixel** do que ela renderiza:

```css
/* Base: a caixa e o aberto, comuns aos dois estados fechados. */
.atalho {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-block-size: var(--control-h-sm);
  padding-inline: var(--space-2);
  border: 1px solid transparent;
  border-radius: var(--radius-sm);
  background-color: transparent;
  white-space: nowrap;
  cursor: pointer;
  transition:
    background-color var(--motion-fast) var(--ease),
    border-color var(--motion-fast) var(--ease),
    color var(--motion-fast) var(--ease);

  &:focus-visible {
    outline: var(--focus-ring);
    outline-offset: var(--focus-offset);
  }

  /* Aberto: IDÊNTICO nos dois — borda sólida, tinta cheia, fantasma. */
  &[aria-expanded="true"] {
    border-style: solid;
    border-color: var(--border-strong);
    color: var(--ink);
    background-color: color-mix(in oklch, var(--accent), transparent 88%);
  }
}

/* Fechado, pendente — a lacuna de hoje. Tracejado é só dela. */
.lacuna {
  border-style: dashed;
  border-color: var(--border-strong);
  color: var(--ink-muted);
  font: 400 var(--text-13) var(--font-ui);
}

/* Fechado, categorizada — o nome na tinta do lugar; só o chevron é muted. */
.categorizada {
  font: inherit;
  color: inherit;

  & .chevron {
    color: var(--ink-muted);
  }

  &[aria-expanded="true"] .chevron {
    color: inherit;
  }

  /* Caixa invisível alinha o TEXTO: na coluna, recua padding + borda para o
     nome ficar na vertical do cabeçalho, onde o <span> estava. */
  &[data-instancia="coluna"] {
    margin-inline-start: calc(-1 * (var(--space-2) + 1px));
  }

  /* Na secundária o nome pode ter 60 caracteres: quebra dentro do botão. */
  &[data-instancia="secundaria"] {
    white-space: normal;
    text-align: start;
  }
}

/* .chevron: inalterado (caixa fixa de 14px, transição de rotate). */

.atalho[aria-expanded="true"] .chevron {
  rotate: 180deg;
}

@media (hover: hover) {
  .atalho:hover {
    color: var(--ink);
    background-color: color-mix(in oklch, var(--accent), transparent 88%);
  }

  .categorizada:hover .chevron {
    color: inherit;
  }
}

@media (prefers-reduced-motion: reduce) {
  .atalho,
  .chevron {
    transition: none;
  }
}
```

Conferência em `filter: grayscale(1)`: lacuna = caixa tracejada + texto 13 px + chevron;
categorizada = texto solto na tinta do lugar + chevron muted, sem caixa; transferência = `Badge`
(coluna) ou texto sem chevron (secundária). Três formas, nenhuma depende de cor. Aberto = caixa
sólida + fantasma + chevron virado, nos dois.

#### 2. O editor — linha de detalhe

`DataTable` ganha **uma** prop: `detail?: (row: T) => ReactNode | null`. Quando devolve conteúdo,
uma `<tr class="detail">` com **uma** `<td colSpan={columns.length}>` entra logo abaixo da linha.
É a única mudança no componente base. Em `DataTable.module.css`:

- `tbody tr:has(+ .detail) > td { border-block-end: none }` — a linha e o editor são a mesma
  linha; não há risco entre elas.
- `.detail > td { padding: var(--space-2) var(--space-4) var(--space-4); background-color:
  var(--surface) }`; em < 40rem, `padding: var(--space-2) var(--space-3) var(--space-3)`.
- `.detail` fora do hover: `tbody tr:not(.groupHead, .detail):hover td`.
- Nem barra lateral, nem fundo recuado (`--surface-sunken` é do cabeçalho de dia — o editor não
  pode parecer um), nem sombra (não é camada flutuante), nem animação de altura. A linha aparece.
- Conferir a 375 px que o `colSpan` maior que o número de colunas visíveis não cria largura
  fantasma (as colunas escondidas estão em `display: none`).

**Uma linha aberta por vez** (`editandoId` na tela): abrir outra fecha a anterior sem tocar no
foco. Trocar mês, conta ou filtro fecha (o estado zera com a busca). **Clique fora não fecha** —
não é popover; a escolha pela metade fica até `Cancelar`, `Escape` ou o confirmar.

**Anatomia** (dentro da `<td>`): `<fieldset class="editor">` com `<legend class="sr-only">
Categorizar {rotuloDaLinha}</legend>` (em modo trocar: `Trocar categoria de {rotuloDaLinha}`);
`display: flex; flex-wrap: wrap; align-items: center;
gap: var(--space-2) var(--space-4); min-inline-size: 0; border: 0; padding: 0; margin: 0`.
Filhos, na ordem do DOM — que é a ordem do Tab:

1. **`Select`** `density="compact" labelHidden label="Categoria"
   placeholder="Escolha a categoria" aria-label="Categoria de {rotuloDaLinha}"` com
   `opcoesDeCategoria(arvore, linha.kind)` — só da natureza da linha, e só ativas (a árvore de
   `categoriasQueryOptions(false)` já vem sem arquivadas). `max-inline-size: 20rem` + ellipsis no
   `<select>`, como em `.decisao`. **Recebe o foco ao abrir** (`useEffect` no `editandoId`).
   Placeholder `Escolha a categoria`, e não `Sem categoria`: aqui a opção vazia não é uma decisão,
   é "ainda falta" — o imperativo continua significando "falta escolher", como em `Escolha a conta`.
   **Modo trocar** (emenda §19): o `<select>` abre **já na categoria atual** (`value` inicial =
   `linha.categoryId`) — a pessoa vê de onde está saindo antes de escolher para onde vai, e o
   `Escape` não tem o que desfazer. Quando a atual **não está entre as opções**, o `<select>` abre
   no placeholder e, abaixo dele, `<p class="nota">` (`--text-13`, `--ink-muted` — a mesma forma da
   frase de categoria lotada; a classe `.lotada` passa a se chamar `.nota` e serve às duas) diz
   **por quê** — sem ela o editor abriria vazio sem explicação, e a pessoa concluiria que a linha
   perdeu a categoria. Enquanto as categorias carregam, a atual ainda não está nas opções: o
   confirmar diz `Escolha uma categoria` e passa a `Escolha outra categoria` quando a lista chega —
   não é defeito, é o estado real.

   **Duas causas, duas frases** (ratificado em 18/09/2026, achado B5 do QA). A nota nunca troca uma
   pela outra: mandar procurar em Arquivadas uma categoria que está lá, ativa, é pior do que não
   dizer nada.

   - **A atual foi arquivada** — marcação existente sobrevive ao arquivamento; atribuição nova,
     não. O componente sabe porque `categoriaPorId(arvore, linha.categoryId)` **não acha** (a
     árvore de `categoriasQueryOptions(false)` vem sem arquivadas):
     `A categoria Alimentação está arquivada e não pode ser escolhida de novo.`
   - **A atual é grupo com subcategoria ativa** — grupo com filha ativa não recebe lançamento
     (spec 0005 §12/§13; o `<select>` só oferece folha e grupo sem filha). O componente sabe
     porque **acha** e `children.length > 0`:
     `O grupo Transporte tem subcategorias e não recebe lançamento. Escolha uma delas.`
   - **As duas ao mesmo tempo** (grupo arquivado com filha ativa): vence a frase da arquivada — é a
     única que a árvore consegue provar, e desarquivar sozinho não devolveria a opção. Limite
     declarado, não defeito.

   Três decisões dentro dessas frases:

   - **O substantivo é explícito (`A categoria`, `O grupo`), nunca elíptico.** É a regra que (l) já
     ratificou para o nome de natureza, agora dita por inteiro: **nome injetado em frase nunca
     governa concordância**. Com o substantivo elíptico, o feminino de "arquivada" e "escolhida"
     ficava preso a uma palavra invisível, e a semente de categorias de fábrica — 8 dos 15 grupos
     com nome masculino (Transporte, Lazer, Serviços, Pessoal, Impostos, Salário, Investimentos,
     Resgates) — produziria `Salário está arquivada e não pode ser escolhida de novo`. Com o
     substantivo à vista a concordância é legítima em qualquer nome, inclusive nos que a casa
     inventar; o artigo é do substantivo, não do nome.
   - **`O grupo` na segunda frase, e não `A categoria`.** Quando essa nota aparece, aquilo é sempre
     um grupo (`children.length > 0`), e `grupo`/`subcategoria` é o par de palavras que
     `/categorias` já usa — o substantivo diz onde ir olhar. De quebra, substantivo diferente é o
     que torna as duas frases impossíveis de confundir, a olho e em teste.
   - **A segunda frase termina em instrução, e perde o `agora`.** Quem abre o editor está
     procurando `Transporte` numa lista que não o tem: precisa de instrução, não de descrição — a
     mesma decisão da frase da folha em (l). `Escolha uma delas` aponta para as opções que estão
     ali, dentro do `<optgroup>` `Transporte`. E `agora tem subcategorias` afirmava uma mudança no
     tempo que a semente desmente: 14 dos 15 grupos **nascem** com subcategorias, e a frase precisa
     ser verdadeira nos dois mundos. Duas orações, como a frase da categoria lotada, que divide a
     mesma classe `.nota`.

   **A nota tem de estar no `aria-describedby` do `<select>` — hoje não está.** Verificado em
   18/09/2026: `.nota` é um `<p>` irmão sem `id`, e `Select` remove `aria-describedby` das props
   (`Omit<…, 'aria-describedby'>`). Quem chega ao campo pelo Tab ouve "Categoria de {rótulo}, caixa
   de combinação, Escolha a categoria" e **nada** explica o vazio: a frase existe só para quem
   enxerga. Correção, sem mexer um pixel no layout — dar `id` à `<p class="nota">` e fazer o
   `Select` **somar** o `aria-describedby` recebido ao `mensagemId` que ele já monta
   (`[mensagemId, recebido].filter(Boolean).join(' ')`), em vez de omiti-lo do tipo. Passar a nota
   como `hint` do `Select` foi **rejeitado**: o `hint` mora dentro do `FieldShell`, que é item flex
   do `.editor`, e 80 caracteres ali esticariam a coluna do campo e empurrariam as fichas para a
   linha de baixo.
2. **Fichas de aprender** — só com categoria escolhida (regra de (d)) e, em modo trocar, **só com
   escolha diferente da atual**: com a atual selecionada não há troca, e "ensinar a categoria de hoje
   a reconhecer esta descrição" é outra ação, que já tem casa em Categorias e na revisão da
   importação — aqui viraria um terceiro modo com um `PATCH` que não muda nada. `key={categoriaId}`,
   para trocar a categoria soltar a ficha pressionada (a palavra pode já ser da outra). Mesma anatomia
   de (d): `<div class="fichas">` `display: flex; flex-wrap: wrap; align-items: center; gap:
   var(--space-1)`; rótulo `Da próxima vez, reconhecer por` (`--text-13`, `--ink-muted`); fichas
   `Button variant="quiet" size="sm"` com texto = a palavra; lista por
   `palavrasParaAprender(description, categoria)` (máx. 5, sem só-dígitos, sem > 40 runas, sem as
   que a categoria já tem). **Diferença única**: aqui a ficha é **alternância**, não gravação
   imediata — `aria-pressed`; `iconStart` = `PlusIcon size={14}` solta / `CheckIcon size={14}`
   pressionada; `aria-label="Reconhecer por «mercado»"` (o nome não promete "adicionar": ainda não
   gravou). **No máximo uma pressionada**: pressionar outra solta a primeira; pressionar a mesma
   solta. O estilo pressionado, canônico e único, entra em `Button.module.css`:
   `.button[data-variant="quiet"][aria-pressed="true"] { background-color: color-mix(in oklch,
   var(--accent), transparent 88%) }` — o mesmo fantasma do hover. O portador não-cromático é a
   troca do ícone (`PlusIcon` → `CheckIcon`, do conjunto do projeto) e o rótulo do confirmar, que
   cita a palavra.
   - Categoria com 20 palavras: sem fichas; no lugar da linha, `<p>` `--text-13` `--ink-muted`:
     `Alimentação já tem 20 palavras-chave. Remova uma em Categorias para incluir outra.`
   - Nenhuma palavra elegível (descrição só com dígitos, ou tudo já na categoria): a linha de
     fichas não existe e nenhuma frase a substitui — o caminho "só este" segue normal.
3. **Dica de consequência** — `<p class="dica" role="status">`, `--text-13`, `--ink-muted`,
   `flex-basis: 100%` (linha inteira). Vazia sem ficha pressionada; com «mercado» pressionada:
   `«mercado» vira palavra-chave de Alimentação — vale para os outros lançamentos sem categoria de
   setembro e para as próximas importações.` É a **única** live region do editor: quem está na
   ficha não ouve o rótulo do botão mudar; ouve isto. Soltar a ficha esvazia a região (nada é
   anunciado).
4. **Ações** — `<div class="acoes">` `display: flex; flex-wrap: wrap; gap: var(--space-2);
   margin-inline-start: auto` (na mesma linha fica à direita; ao quebrar, desce e fica à direita,
   como rodapé de diálogo): o **confirmar** `Button variant="primary" size="sm"` e `Cancelar`
   `Button variant="quiet" size="sm"`, nesta ordem.

Em < 40rem: `.editor { flex-direction: column; align-items: stretch; gap: var(--space-3) }` e
`.acoes { margin-inline-start: 0 }`. O `Select` compacto continua com largura por conteúdo; o
confirmar mais longo (`Categorizar e reconhecer por «mercado»`, ≈ 36ch a 13 px) cabe nos 343 px
úteis de um 375 px.
#### 3. As duas saídas — um botão; o rótulo diz qual

Não existem dois botões "Só este" / "Com palavra-chave", nem chave de modo. A ficha pressionada
**é** a escolha da segunda saída, e o confirmar diz o que vai acontecer no próprio rótulo (regra
de "Blocos de decisão"). Dois cliques para o caminho comum, três para o que altera outras linhas
do mês — o clique a mais é o preço certo para uma ação que não se desfaz nesta entrega.

| Estado | Rótulo do confirmar | O que faz |
|---|---|---|
| sem categoria escolhida | `Escolha uma categoria` + `aria-disabled="true"` | clique move o foco ao `<select>`; nunca `disabled` |
| categoria escolhida, nenhuma ficha pressionada | `Categorizar` | **só este**: `PATCH /transactions/{id}` `{ categoryId }` |
| categoria escolhida + «mercado» pressionada | `Categorizar e reconhecer por «mercado»` | (a) `PATCH /categories/{id}` com a lista lida do cache + a palavra → (b) `PATCH /transactions/{id}` → (c) `POST /transactions/auto-categorize` `{ month: mês da tela, dryRun: false }` |
| em andamento | o rótulo que estava, com `loading` (spinner, `aria-busy`, foco preservado) | `<select>` e fichas ficam como estão, **sem `disabled`**; cliques são ignorados enquanto `loading` |

**Modo trocar** (emenda §19): o mesmo botão, com o verbo da ação — **trocar**. O rótulo diz o que
vai acontecer com **esta** linha, e a categoria atual é uma escolha que não faz nada:

| Estado | Rótulo do confirmar | O que faz |
|---|---|---|
| sem escolha (placeholder — atual arquivada ou lista carregando) | `Escolha uma categoria` + `aria-disabled="true"` | clique move o foco ao `<select>` |
| escolha **= atual** | `Escolha outra categoria` + `aria-disabled="true"` | clique move o foco ao `<select>`; **nenhuma requisição** sai — não existe "trocar para a mesma" |
| escolha ≠ atual, nenhuma ficha | `Trocar categoria` | `PATCH /transactions/{id}` `{ categoryId }` — o servidor substitui |
| escolha ≠ atual + «uber» pressionada | `Trocar e reconhecer por «uber»` | (a) → (b) → (c) como acima; (c) continua tocando **só** os sem categoria do mês (spec §4.3) — trocar uma linha nunca recategoriza outra que já tinha dona |
| em andamento | como acima | como acima |

Por que `Trocar categoria`, e não `Trocar para Lazer`: o destino está no `<select>`, a um palmo do
botão, e repeti-lo alongaria o rótulo com palavra-chave (`Trocar para Lazer e reconhecer por
«uber»`, 41ch) sem dizer nada novo. Largura a 375 px: `Trocar e reconhecer por «uber»` (30ch a
13 px ≈ 240 px com padding) cabe ao lado de `Cancelar` (≈ 82 px) nos 343 px úteis; com «mercado»
(33ch) o `Cancelar` desce — `.acoes` embrulha, como já acontece com `Categorizar e reconhecer por
«mercado»`, que é 5ch mais longo que qualquer rótulo de trocar. Nunca "alterar", "mudar", "editar"
ou "recategorizar": do botão ao toast, o verbo é trocar.

**Teclado**: Enter/Space no botão da célula abre. `Escape` em qualquer ponto do editor (keydown no
`<fieldset>`) fecha sem gravar e devolve o foco ao botão da célula (instância visível); `Cancelar`
faz o mesmo. `Escape` com a lista nativa do `<select>` aberta é do navegador — fecha só a lista;
não interceptar.

#### 4. Depois de gravar — linha, foco, faixa, toast

- **Só este, sucesso**: o editor fecha; a célula muda no mesmo frame (o cache de `transactions`
  recebe o `Transaction` devolvido pelo `PATCH`) e a query é invalidada — o
  `summary.uncategorizedCount` da faixa vem do servidor, nunca de conta local. Toast:
  `Lançamento categorizado como Alimentação.` **O foco vai para a próxima lacuna**: o próximo
  botão `Sem categoria` visível abaixo na tabela; não havendo, o anterior; não havendo nenhum, o
  botão `Carregar mais` se existir, senão o `<h1>`. A pessoa está percorrendo a lista — a próxima
  lacuna é o próximo trabalho. Com o filtro `?semCategoria=1` ativo, a linha resolvida sai da lista
  e a seguinte toma o lugar dela: o foco já está lá.
- **Com palavra-chave, sucesso nas três chamadas**: fecha; invalida `categories` (depois de (a)) e
  `transactions` (uma vez, depois de (c)); o foco vai à próxima lacuna, calculada **depois** que a
  lista refetchou — as linhas que (c) categorizou já perderam a lacuna. Toast com o número **real**
  de (c), que conta só os outros (este já foi por (b)): `«mercado» adicionada a Alimentação · mais 7
  lançamentos de setembro categorizados.` / `· mais 1 lançamento de setembro categorizado.` /
  `· nenhum outro lançamento de setembro categorizado.`
- **409 em (a)**: toast de erro `«mercado» já está em Padaria.` (sem dona resolvível: `«mercado»
  já está em outra categoria desta casa.`); **nada mais é feito** — (b) e (c) não rodam. O editor
  fica aberto, a categoria continua selecionada, a ficha «mercado» **sai da linha** (oferecê-la de
  novo seria oferecer o mesmo 409), o rótulo volta a `Categorizar` e o foco continua no confirmar.
  A pessoa decide.
- Falha em (b) depois de (a) gravar: toast de erro `«mercado» adicionada a Alimentação, mas o
  lançamento não foi categorizado. Tente de novo.`; editor aberto; a ficha vira texto estático
  `CheckIcon size={14}` + palavra (como em (d) — a palavra já é da categoria); rótulo `Categorizar`.
- Falha em (c) depois de (a) e (b): fecha (a linha está resolvida); toast de erro `«mercado»
  adicionada e lançamento categorizado, mas os outros do mês não foram — use Categorizar
  automaticamente na faixa.`
- Outro erro na saída "só este" (400/404/422/rede): toast de erro com `messageForError`; editor
  aberto; foco no confirmar.
- **Faixa `N sem categoria`**: nada de novo — o número é o `summary` refetchado, e a faixa some
  sozinha ao zerar (regra "Faixa de pendência"). **Sem `aria-live` nela**: o toast já é a live
  region, e a regra é sem duplicata.
- **Categorias carregando**: `Select` com `placeholder="Carregando categorias…"` e sem opções;
  confirmar `Escolha uma categoria` `aria-disabled`; o foco vai ao `<select>` mesmo assim (ele
  existe e não pula). **Erro**: `Alert tone="error"` no lugar do `Select`, `Não foi possível
  carregar as categorias.` com `Button size="sm"` `Tentar de novo`. **Nenhuma categoria do lado do
  dinheiro**: no lugar do `Select`, `<p>` `--text-15` `--ink` `Nenhuma categoria de despesa ou de
  investimento ainda.` (do outro lado, `de receita ou de resgate`) + `Button variant="secondary"
  size="sm"` `Ir para categorias`; sem confirmar, só `Cancelar`. A frase nomeia as DUAS naturezas do
  lado do dinheiro (E7 (l)): o `Select` oferece as duas, e ela só aparece quando as duas
  estão vazias.

**Depois de trocar** (emenda §19). A diferença toda está no foco: quem troca uma categoria está
trabalhando **nesta** linha, não varrendo pendências. Não existe "próxima lacuna" aqui.

- **Só este, sucesso**: fecha; a célula mostra o nome novo no mesmo frame (o cache recebe o
  `Transaction` do `PATCH`) e `transactions` é invalidada — o que mudou na faixa, o servidor diz.
  Toast: `Categoria trocada de Transporte para Lazer.` (anterior não resolvível — `categoryName`
  nulo com `categoryId` presente: `Categoria trocada para Lazer.`). **O foco volta ao botão da mesma
  linha**, agora `.categorizada` com o nome novo — a instância visível, achada por
  `[data-atalho="{id}"]`. Sem realce, sem animação na célula: a pessoa acabou de fazer isso e está
  olhando; o toast confirma.
- **Com palavra-chave, sucesso nas três chamadas**: (a) → (b) → (c) como no modo categorizar; (c)
  continua tocando só os sem categoria do mês. Invalida `categories` depois de (a) e `transactions`
  uma vez depois de (c); o foco volta ao botão da mesma linha depois do refetch. Toast: `Categoria
  trocada de Transporte para Lazer · «uber» adicionada a Lazer · mais 3 lançamentos de setembro
  categorizados.` — a troca desta linha vem **primeiro** porque é o que a pessoa fez e o que a
  palavra não implica (no modo categorizar, `«mercado» adicionada a Alimentação` já implica esta
  linha; aqui não). O terceiro segmento é `fraseDosOutros` de hoje (`· mais 1 lançamento de
  setembro categorizado.` / `· nenhum outro lançamento de setembro categorizado.`).
- **A linha sai da lista** (só sob `?tipo=`; sob `?semCategoria=1` não há linha categorizada): a
  categoria nova contradiz o filtro — `despesas` → categoria de investimento, `receitas` → de
  resgate e, caso novo, **`investimentos` → categoria de despesa ou de receita**. O toast ganha a
  segunda frase de `fraseDaLinhaQueSaiu` (E2d (g)), com o sujeito `Ele` no só-este e `Este
  lançamento` na saída com palavra; a tabela de movimentos ganha as duas linhas novas: `é uma
  despesa, e a lista mostra só aportes e resgates.` / `é uma receita, e a lista mostra só aportes e
  resgates.` — o **mesmo molde** das duas frases ratificadas, sem "agora": um molde só em
  `fraseDaLinhaQueSaiu`, sem parâmetro de modo. O botão da linha não existe mais, então o foco vai ao
  **vizinho da foto**: antes de gravar, o confirmar fotografa os controles de categoria visíveis
  (`button[data-atalho]`, **qualquer** estado, na ordem da tabela); depois do refetch, o foco vai ao
  controle da linha **seguinte** na foto que ainda exista; não havendo, o da **anterior**; não
  havendo, `Carregar mais` se existir; senão o `<h1>`. Linha de transferência não está na foto (não
  tem controle) e é pulada naturalmente.
- **409 em (a)**, **falha em (b)**, **falha em (c)**, **422 da §13** e **outro erro**: como no modo
  categorizar, com o rótulo voltando a `Trocar categoria` (e não `Categorizar`) e estes textos —
  falha em (b): `«uber» adicionada a Lazer, mas o lançamento continua em Transporte. Tente de novo.`
  (anterior não resolvível: `«uber» adicionada a Lazer, mas a categoria não foi trocada. Tente de
  novo.`); falha em (c): `«uber» adicionada e categoria trocada para Lazer, mas os outros do mês não
  foram categorizados — use Categorizar automaticamente na faixa.` No 422 a escolha é limpa para o
  placeholder (não volta à atual): a mensagem junto do `<select>` pede outra, e é isso que o
  placeholder diz.
- **`Escape` e `Cancelar`**: fecham sem gravar e devolvem o foco ao botão da linha — o `<select>`
  tinha a atual, e nada mudou. Idêntico ao modo categorizar.
- **Mecânica do foco, para o dev**: `focarLacuna(id)` vira `focarAtalho(id)` e procura
  `[data-atalho="{id}"]` (qualquer estado, instância visível); `lacunasVisiveis()`,
  `idsDasLacunas()` e `proximaLacuna()` passam a consultar `button[data-lacuna]` — senão a linha
  categorizada vizinha seria tratada como "próxima lacuna" no modo categorizar. A foto do modo trocar
  é a de `button[data-atalho]`, e a busca do vizinho é seguinte → anterior → `Carregar mais` →
  `<h1>`, sem o terceiro passo "qualquer que sobrou" do modo categorizar (pular para uma linha
  aleatória não é devolver o foco).

**Código compartilhado** (regra "sem imports entre features"): `palavrasParaAprender`,
`MAX_FICHAS` e `citarPalavra` saem de `features/import/` para `lib/keywords.ts` — a revisão passa a
importar de lá; `AutoCategorizeDialog`, que hoje escreve `«${…}»` à mão, passa a usar
`citarPalavra`. `rotuloDaLinha` de lançamento já existe em `TransactionsScreen.tsx`: exportar para
o componente (ou mover para `features/transactions/lexico.ts`).
#### 5. Copy pt-BR — atalho de categorização

| Onde | Texto |
|---|---|
| Botão da célula (visível) | `Sem categoria` |
| Botão da célula (`aria-label`) | `Sem categoria. Categorizar {descrição}, {data por extenso}, {valor}` |
| Legenda do editor (`sr-only`) | `Categorizar {descrição}, {data por extenso}, {valor}` |
| Placeholder do `<select>` | `Escolha a categoria` · carregando: `Carregando categorias…` |
| `aria-label` do `<select>` | `Categoria de {descrição}, {data por extenso}, {valor}` |
| Rótulo das fichas | `Da próxima vez, reconhecer por` |
| Ficha (`aria-label`) | `Reconhecer por «mercado»` |
| Categoria lotada | `Alimentação já tem 20 palavras-chave. Remova uma em Categorias para incluir outra.` |
| Dica com ficha pressionada (`role="status"`) | `«mercado» vira palavra-chave de Alimentação — vale para os outros lançamentos sem categoria de setembro e para as próximas importações.` |
| Confirmar | `Escolha uma categoria` (`aria-disabled`) · `Categorizar` · `Categorizar e reconhecer por «mercado»` |
| Cancelar | `Cancelar` |
| Toast — só este | `Lançamento categorizado como Alimentação.` |
| Toast — com palavra | `«mercado» adicionada a Alimentação · mais 7 lançamentos de setembro categorizados.` / `· mais 1 lançamento de setembro categorizado.` / `· nenhum outro lançamento de setembro categorizado.` |
| Toast — 409 | `«mercado» já está em Padaria.` / `«mercado» já está em outra categoria desta casa.` (os mesmos de (g)) |
| Toast — falha em (b) | `«mercado» adicionada a Alimentação, mas o lançamento não foi categorizado. Tente de novo.` |
| Toast — falha em (c) | `«mercado» adicionada e lançamento categorizado, mas os outros do mês não foram — use Categorizar automaticamente na faixa.` |
| Sem categorias do lado do dinheiro | `Nenhuma categoria de despesa ou de investimento ainda.` / `Nenhuma categoria de receita ou de resgate ainda.` — `Ir para categorias` |
| Erro ao carregar categorias | `Não foi possível carregar as categorias.` — `Tentar de novo` |
| Botão da célula, categorizada (visível) — emenda §19 | `{categoryName}` — ex. `Alimentação` |
| Botão da célula, categorizada (`aria-label`) | `Alimentação. Trocar categoria de {descrição}, {data por extenso}, {valor}` |
| Legenda do editor em modo trocar (`sr-only`) | `Trocar categoria de {descrição}, {data por extenso}, {valor}` |
| Nota sob o `<select>` — a atual foi **arquivada** | `A categoria Alimentação está arquivada e não pode ser escolhida de novo.` |
| Nota sob o `<select>` — a atual é **grupo com subcategoria ativa** | `O grupo Transporte tem subcategorias e não recebe lançamento. Escolha uma delas.` |
| Confirmar — modo trocar | `Escolha uma categoria` (`aria-disabled`, placeholder) · `Escolha outra categoria` (`aria-disabled`, escolha = atual) · `Trocar categoria` · `Trocar e reconhecer por «uber»` |
| Toast — trocar, só este | `Categoria trocada de Transporte para Lazer.` / anterior não resolvível: `Categoria trocada para Lazer.` |
| Toast — trocar, com palavra | `Categoria trocada de Transporte para Lazer · «uber» adicionada a Lazer · mais 3 lançamentos de setembro categorizados.` / `· mais 1 lançamento de setembro categorizado.` / `· nenhum outro lançamento de setembro categorizado.` |
| Toast — trocar, falha em (b) | `«uber» adicionada a Lazer, mas o lançamento continua em Transporte. Tente de novo.` / `«uber» adicionada a Lazer, mas a categoria não foi trocada. Tente de novo.` |
| Toast — trocar, falha em (c) | `«uber» adicionada e categoria trocada para Lazer, mas os outros do mês não foram categorizados — use Categorizar automaticamente na faixa.` |
| Frase de saída sob `investimentos` (2ª frase do toast, E2d (g)) | `Ele saiu da lista: é uma despesa, e a lista mostra só aportes e resgates.` / `Ele saiu da lista: é uma receita, e a lista mostra só aportes e resgates.` — com palavra, o sujeito é `Este lançamento` |

Léxico: a palavra sempre entre aspas angulares; o mês por extenso e em minúsculas (`setembro`);
o estado é a palavra `Sem categoria`, nunca cor; nenhum "sugerido", "inteligente" ou "automágico".
A tabela (g) continua valendo para tudo o que este atalho reaproveita (409, rótulo das fichas).
Em modo trocar o verbo é **trocar** (`Trocar categoria`, `Categoria trocada`) do botão ao toast —
nunca "alterar", "mudar", "editar" ou "recategorizar" na interface; origem e destino sempre em
palavras (`de Transporte para Lazer`), nunca só o destino. E **nome de categoria injetado em frase
nunca governa concordância**: onde o texto pede gênero, o substantivo vem à vista (`A categoria
Alimentação…`, `O grupo Transporte…`) — a mesma regra que (l) fixou para o nome de natureza.
### Checklist anti-cara-de-IA — E2c (aplicar com a tela pronta)

1. `filter: grayscale(1)` na revisão e em `/transferencias`: toda decisão continua legível? Se
   alguma linha só se distingue por cor, reprova.
2. A pontuação é **texto** (`88%`)? Nenhuma barra, anel, ícone de "confiança" ou
   verde/amarelo/vermelho por faixa de pontuação.
3. Nenhuma `Badge` nova: nem "sugerida", nem "transferência detectada", nem "IA". O status vira
   posição, palavra do grupo, evidência e opção de `<select>`.
4. O × da ficha é `--ink-muted`, nunca vermelho; a ficha inválida tem ícone além da borda.
5. Fichas de aprender são `Button quiet sm` do sistema — nenhum chip arredondado (pill), nenhum
   fundo colorido, nenhum estilo inventado.
6. Nada de "✨", "auto-mágico", "IA sugere" ou emoji em texto, toast ou botão. O app fala de
   palavras-chave, não de inteligência.
7. O painel do par é `<dl>` com números tabulares alinhados à direita — não são "quatro cards"
   com número gigante e ícone.
8. `/transferencias` é cromaticamente silenciosa: valores neutros, líquido neutro com sinal +
   frase; só o saldo no fim do mês usa `semantic` (posição, como em `/contas`).
9. Direção do dinheiro em palavras: a frase de direção existe, e a seta nas células tem `sr-only`
   "de X para Y".
10. `<dialog>` nativo no `AutoCategorizeDialog`; `<select>` nativo em toda decisão; nenhum
    dropdown reimplementado.
11. Nenhum controle `disabled`: input `readOnly` em 20/20, botões com `aria-disabled` e rótulo
    que explica.
12. Foco: anel único na caixa do `KeywordsField`; foco volta ao select da linha após aprender;
    `<h1>` de `/transferencias` recebe o foco na rota.
13. Estados completos desenhados: vazio (com saída), carregando (skeleton com a estrutura real),
    erro (com tentar de novo) — nas três superfícies novas.
14. Nenhuma medida, cor ou raio fora dos tokens nos `.module.css` novos (`KeywordsField`,
    `BlocoTransferencias`, `TransfersScreen`, `TransferPairPanel`).
15. Copy: nenhuma frase desta seção foi "melhorada" com adjetivos ("poderoso", "inteligente",
    "sem esforço"); os textos são os da tabela (g).

Itens específicos do atalho de categorização em `/lancamentos` — (h):

16. A lacuna é `<button>` com texto `Sem categoria`, tracejado em `--border-strong`, `--ink-muted`
    peso 400 e chevron de 14 px — não é `Button quiet` verde, não é `Badge` com `onClick`, não é
    `<span>` clicável. Dezoito lacunas na tela não gritam.
17. O editor é uma `<tr>` de detalhe dentro da `<table>` — nenhuma camada flutuante, nenhum
    popover posicionado por JavaScript, nenhuma sombra, nenhum fundo recuado, nenhuma animação de
    altura. Entre a linha e o editor não há borda.
18. Um confirmar só, e o rótulo diz a saída (`Categorizar` / `Categorizar e reconhecer por
    «mercado»`); nenhum par de botões "Só este / Com palavra-chave", nenhuma chave de modo.
19. Ficha pressionada se distingue sem cor: `CheckIcon` no lugar do `PlusIcon` e o rótulo do
    confirmar cita a palavra; o fundo é o fantasma do hover, não uma cor nova.
20. Nenhum `disabled`: confirmar com `aria-disabled` e rótulo `Escolha uma categoria`; `<select>`
    e fichas continuam focáveis durante o `loading`.
21. Foco: abre no `<select>`; `Escape` e `Cancelar` devolvem ao botão da célula (a instância
    visível); depois de categorizar uma lacuna, vai para a próxima lacuna da tabela (em modo
    trocar, ver 27).
22. Os números do toast são os do servidor (`categorized` de (c)) — nunca a contagem das linhas
    carregadas na tela.
23. Clique fora não fecha; só uma linha aberta por vez; trocar mês ou filtro fecha.
24. A 375 px: uma única instância visível do botão (na `.secundaria`), editor empilhado, página
    sem rolagem horizontal (conferir o `colSpan` com as colunas escondidas).
25. `filter: grayscale(1)`: a lacuna (tracejado), a ficha pressionada (ícone) e a linha aberta
    (chevron virado + editor) continuam legíveis.

Itens do modo trocar — (h) §1-bis e emenda §19 (18/09/2026):

26. A linha categorizada **não é lacuna**: nome na tinta do lugar (`--ink` na coluna, `--ink-muted`
    na secundária), `border: 1px solid transparent`, chevron `--ink-muted` sempre visível; o
    tracejado continua só na pendência. Em `filter: grayscale(1)` os dois estados fechados se
    distinguem por **forma** (caixa tracejada × texto solto com chevron), e o aberto é o mesmo nos
    dois (borda sólida + chevron virado). Nenhum sublinhado pontilhado, nenhuma opacidade.
27. Depois de trocar, o foco volta ao botão da **mesma** linha (instância visível, por
    `[data-atalho]`); só quando a linha saiu da lista sob `?tipo=` ele vai ao vizinho da foto →
    `Carregar mais` → `<h1>`. Nunca "próxima lacuna" no modo trocar — e a busca da próxima lacuna do
    modo categorizar usa `[data-lacuna]`, não `[data-atalho]`.
28. Confirmar com a categoria atual selecionada é `aria-disabled` com rótulo `Escolha outra
    categoria`; o clique foca o `<select>` e **nenhuma requisição** sai. As fichas só existem com
    escolha diferente da atual.
29. O toast de trocar diz **de onde para onde em palavras** (`Categoria trocada de Transporte para
    Lazer.`), nunca só o destino; com palavra-chave, a troca desta linha vem antes da palavra e do
    número do mês. Sob `investimentos`, a linha que virou despesa ou receita ganha a frase de saída.
30. Alinhamento na coluna: o nome da categoria continua na vertical do cabeçalho `Categoria` e do
    texto das outras colunas (margem negativa de `--space-2` + 1 px só em
    `data-instancia="coluna"`); a caixa da `Badge Transferência` e a da lacuna continuam encostadas
    em `--cell-pad`. Na secundária, nome e `Sem categoria` ficam à mesma distância do `·`.

## E6a — relatório por categoria: `/relatorios/categorias` e a primeira rosca (ADR-027, 17/09/2026)

Estende E2 e E2c sem revogar nada: `grayscale(1)` continua sendo o aceite e os quatro portadores
de estado continuam os únicos. É o **primeiro gráfico do produto** (ADR-021: SVG próprio, sem
biblioteca), e por isso esta seção também fixa o que todo gráfico seguinte herda: cor de marca só
por `--chart-*`, texto nunca na cor da série, tabela sempre ao lado, modo escuro validado em
separado. A skill `dataviz` é o método; o *instance file* dela — as cores, a fonte, as
superfícies — é **este documento**. Normativo para o `dev-frontend-react`.

### (a) Decisões de forma

1. **Rosca, não pizza.** O furo recebe o total do mês. Números são protagonistas (princípio 1),
   e a rosca é o único gráfico em que o número cabe *dentro* do desenho em vez de virar um
   título ao lado. A pizza cheia perderia o centro e não ganharia nada: a leitura "parte do
   todo" é a mesma. A skill mantém a rosca "despriorizada" em favor da barra empilhada; aqui o
   pedido do usuário é literalmente uma pizza, e a rosca com ≤ 6 fatias é "parte-do-todo de
   relance" — o único uso que a skill admite. Comparar valores próximos é trabalho da tabela,
   não do anel — e a tabela está logo abaixo, com os "valores certinhos".
2. **Cor é tinta, não matiz.** Categoria não tem cor no modelo (ADR-027d) e cor cromática
   continua com três donos: `--accent`, `--income`/`--expense`, `--warning`. As fatias saem de
   uma **rampa ordinal de `--ink` para `--surface`**, da maior (tinta cheia) para a menor
   (tinta lavada). Isso faz ordem no anel, ordem na legenda, ordem na tabela e ordem de
   luminosidade serem **o mesmo eixo** — quatro portadores redundantes de identidade.
   Alternar passos (escuro/claro/médio) foi recusado: ganharia separação entre vizinhas e
   jogaria fora os outros três. Paleta categórica "sóbria" foi recusada duas vezes: pela regra
   da cor cromática e porque o piso de croma do validador (C ≥ 0,10) é, por definição, a
   saturação de um dashboard genérico — uma paleta discreta reprova nele *por construção*.
3. **Máximo de 4 fatias nomeadas — medido, não estimado.** Entre `--ink` e o limite de 2:1
   sobre `--surface` cabem 4 passos com ΔE ≥ 15 entre vizinhas nos dois temas (saída em (b));
   5 passos caem a ΔE 11,7 (claro) / 11,0 (escuro) e reprovam o piso *normal-vision*. Da 5ª
   categoria em diante tudo vira **`Outras (N categorias)`**: uma fatia só, em **hachura**
   (não é uma categoria, é um resto — e hachura é o papel "de-emphasis / Other" da própria
   skill), sempre a última do anel. A tabela lista **todas**; a dobra é só do anel.
4. **`Sem categoria` é fatia própria, em `--chart-pending` (= `--warning`), e nunca dobra em
   Outras.** É dinheiro esperando decisão humana — a definição de `--warning` desde E2. Ocupa a
   posição que o servidor lhe deu e **não consome passo da rampa**: as quatro nomeadas continuam
   1 → 4. Teto real do anel: 4 + `Sem categoria` + `Outras` = **6**, o limite da skill. É cor de
   *status*, não de série — ver (b).
5. **Fatia com `shareBp = 0` não é desenhada** (inclui valor zero). Uma fatia só vira o anel
   inteiro. O centro mostra `totalCents` **do servidor**. A única aritmética do cliente é somar
   inteiros (`shareBp` e `cents`) das categorias dobradas em Outras — nunca dividir centavos.
6. **Percentual: duas casas na tabela, uma na legenda e no `<title>`.** `shareBp` é exato
   (Σ = 10000 pelo maior resto, ADR-027c): com duas casas as linhas somam `100,00%` e as
   subcategorias somam o grupo, **sem nota de arredondamento**. A legenda é o relance — `41,2%`.
   `formatarParticipacao(bp, casas)` em `lib/format.ts`: `Intl.NumberFormat('pt-BR', { style:
   'percent', minimumFractionDigits: casas, maximumFractionDigits: casas }).format(bp / 10000)`
   — formatação, não cálculo.

### (b) Tokens `--chart-*` e a saída literal do validador

Tokens novos em `tokens.css`, **derivados** (mesma família de `--danger`: alias, não cor nova):

```css
--chart-1: var(--ink);
--chart-2: color-mix(in oklch, var(--ink), var(--surface) 22%);
--chart-3: color-mix(in oklch, var(--ink), var(--surface) 44%);
--chart-4: color-mix(in oklch, var(--ink), var(--surface) 66%);
--chart-pending: var(--warning);
```

Declarados **uma vez** em `:root`: como saem de `--ink`, `--surface` e `--warning`, os blocos
escuros já os recalculam sozinhos. O modo escuro **não é inversão** — é a rampa da tinta escura
sobre a superfície escura, com hex próprios, validada em separado. `@media (prefers-contrast:
more)` aperta para 18 / 36 / 54 % (toda fatia ≥ 3:1; separação cai a ΔE 13,3 / 12,5 — acima do
piso ordinal, e a ordem continua). Hachura de Outras: linhas em `--ink-muted`.

Hex resolvidos (conversão OKLCH → sRGB própria com as matrizes de Ottosson / CSS Color 4;
`color-mix(in oklch)` modelado como a CSS Color 4 manda — interpolação linear de L, C e H, com
matiz *powerless* quando C = 0. Prova de que a conversão está certa: `--ink`, `--ink-muted`,
`--warning`, `--surface` escuro etc. saem exatamente nos hex da tabela de referência acima):

| Token | Claro (sobre `--surface` `#FFFFFF`) | Escuro (sobre `--surface` `#211E1A`) |
|---|---|---|
| `--chart-1` | `#26231E` — 15,65:1 | `#EDE9E1` — 13,71:1 |
| `--chart-2` | `#504D49` — 8,40:1 | `#BBB7B0` — 8,31:1 |
| `--chart-3` | `#7D7B78` — 4,22:1 | `#8C8881` — 4,71:1 |
| `--chart-4` | `#AEADAB` — 2,24:1 (*relief*: legenda + tabela) | `#5F5B56` — 2,46:1 (idem) |
| `--chart-pending` | `#9A6A00` — 4,73:1 | `#D9A23C` — 7,26:1 |
| hachura (`--ink-muted`) | `#6E675C` — 5,59:1 | `#A39C8F` — 6,09:1 |
| contraste+ 2 / 3 / 4 | `#484541` · `#6C6A66` · `#93918F` — 9,53 · 5,40 · 3,14 | `#C4C0B9` · `#9D9992` · `#77736D` — 9,16 · 5,85 · 3,52 |

Saída literal de `scripts/validate_palette.js` da skill `dataviz`, rodado em 17/09/2026 a partir
do diretório da skill, com as superfícies do projeto:

```text
$ node scripts/validate_palette.js "#26231e,#504d49,#7d7b78,#aeadab" --ordinal --mode light --surface "#ffffff"

Palette (light, surface #ffffff, ordinal ramp): 4 slots
  [PASS] Lightness monotone     steps read light→dark
  [PASS] Adjacent ΔL            all gaps >= 0.06
  [PASS] Light-end contrast     #aeadab at 2.24:1 vs surface
  [PASS] Single hue             hue spread 9°

  → ALL CHECKS PASS  (ordinal: one hue, monotone L, visible step gaps, light end clears surface)
$ node scripts/validate_palette.js "#ede9e1,#bbb7b0,#8c8881,#5f5b56" --ordinal --mode dark --surface "#211e1a"

Palette (dark, surface #211e1a, ordinal ramp): 4 slots
  [PASS] Lightness monotone     steps read light→dark
  [PASS] Adjacent ΔL            all gaps >= 0.06
  [PASS] Light-end contrast     #5f5b56 at 2.46:1 vs surface
  [PASS] Single hue             hue spread 11°

  → ALL CHECKS PASS  (ordinal: one hue, monotone L, visible step gaps, light end clears surface)
$ node scripts/validate_palette.js "#26231e,#504d49,#7d7b78,#aeadab" --mode light --surface "#ffffff"   # categórico: só as linhas que valem para uma rampa
  [PASS] CVD separation         worst adjacent #7d7b78↔#504d49 ΔE 16.2 (deutan) · tritan 16.2
  [PASS] Normal-vision floor    worst adjacent #7d7b78↔#504d49 ΔE 16.2 (normal)
  [WARN] Contrast vs surface    below 3:1 — relief required (visible labels or table view): [["#aeadab",2.24]]
  → FAILED — fix the marked checks  (CVD in the 6–8 floor band is legal ONLY with secondary encoding: direct labels, gaps, or texture)
$ node scripts/validate_palette.js "#ede9e1,#bbb7b0,#8c8881,#5f5b56" --mode dark --surface "#211e1a"
  [PASS] CVD separation         worst adjacent #8c8881↔#bbb7b0 ΔE 15.3 (deutan) · tritan 15.3
  [PASS] Normal-vision floor    worst adjacent #8c8881↔#bbb7b0 ΔE 15.3 (normal)
  [WARN] Contrast vs surface    below 3:1 — relief required (visible labels or table view): [["#5f5b56",2.46]]
  → FAILED — fix the marked checks  (CVD in the 6–8 floor band is legal ONLY with secondary encoding: direct labels, gaps, or texture)
$ node scripts/validate_palette.js "#26231e,#44413d,#64625e,#868481,#aaa8a6" --mode light --surface "#ffffff"   # 5 passos — por que não
  [FAIL] Normal-vision floor    worst adjacent #868481↔#64625e ΔE 11.7 (normal) — below 15, hard to tell apart even with full color vision
$ node scripts/validate_palette.js "#ede9e1,#c9c5bd,#a5a19a,#838079,#635f5a" --mode dark --surface "#211e1a"
  [FAIL] Normal-vision floor    worst adjacent #838079↔#a5a19a ΔE 11.0 (normal) — below 15, hard to tell apart even with full color vision
$ node scripts/validate_palette.js "#26231e,#484541,#6c6a66,#93918f" --ordinal --mode light --surface "#ffffff"   # prefers-contrast: more
  [PASS] Light-end contrast     #93918f at 3.14:1 vs surface
  → ALL CHECKS PASS  (ordinal: one hue, monotone L, visible step gaps, light end clears surface)
$ node scripts/validate_palette.js "#ede9e1,#c4c0b9,#9d9992,#77736d" --ordinal --mode dark --surface "#211e1a"
  [PASS] Light-end contrast     #77736d at 3.52:1 vs surface
  → ALL CHECKS PASS  (ordinal: one hue, monotone L, visible step gaps, light end clears surface)
$ node scripts/validate_palette.js "#26231e,#504d49,#7d7b78,#aeadab,#9a6a00" --mode light --surface "#ffffff" --pairs all   # + Sem categoria (status)
  [PASS] CVD separation         worst all-pairs #9a6a00↔#7d7b78 ΔE 11.2 (deutan) · tritan 9.7
  [FAIL] Normal-vision floor    worst all-pairs #9a6a00↔#7d7b78 ΔE 11.4 (normal) — below 15, hard to tell apart even with full color vision
  → FAILED — fix the marked checks  (CVD in the 6–8 floor band is legal ONLY with secondary encoding: direct labels, gaps, or texture)
$ node scripts/validate_palette.js "#ede9e1,#bbb7b0,#8c8881,#5f5b56,#d9a23c" --mode dark --surface "#211e1a" --pairs all
  [PASS] CVD separation         worst all-pairs #d9a23c↔#bbb7b0 ΔE 12.4 (deutan) · tritan 10.5
  [FAIL] Normal-vision floor    worst all-pairs #d9a23c↔#bbb7b0 ΔE 12.6 (normal) — below 15, hard to tell apart even with full color vision
  → FAILED — fix the marked checks  (CVD in the 6–8 floor band is legal ONLY with secondary encoding: direct labels, gaps, or texture)
```

Como ler: o modo `--ordinal` é o que julga uma rampa de uma matiz — **passa nos dois temas**. O
modo categórico reprova banda e croma **por construção** (a skill diz: "running the categorical
validator on a sequential ramp will FAIL by design … don't fix a good ramp to satisfy it") e é
citado só pelas linhas que valem para uma rampa: piso *normal-vision* **16,2 / 15,3 ≥ 15** e
CVD **16,2 / 15,3 ≥ 8**, e a 4ª fatia em *relief* — que exige legenda ou tabela, e aqui há as
duas. A rampa de 5 está ali para mostrar por que o teto é 4. A fatia `Sem categoria` é **cor de
status**, não de série: contra a vizinha de tinta mais próxima mede ΔE 11,4 (claro) / 12,6
(escuro), abaixo do piso de série e acima do alvo CVD (11,2 / 12,4 ≥ 8) — é exatamente o par que
a skill reserva para status ("leans on the icon + label pairing and on placement; never on hue
alone"): a matiz sobrevive a protan/deutan, e o rótulo está na legenda, na tabela e no `<title>`.

Regras que ficam para todo gráfico: **`--chart-*` só entra em marca** (fatia, barra, linha,
amostra de legenda ou tabela). Texto — rótulo, valor, legenda — usa `--ink`/`--ink-muted`,
nunca a cor da série. Nenhuma matiz além de `--chart-pending` entra num gráfico sem emenda aqui.

### (c) `DonutChart` — anatomia

`frontend/src/components/DonutChart/`: `DonutChart.tsx`, `DonutChart.module.css`, `Swatch.tsx`,
`dobrar.ts` (+ `.test.tsx`/`.test.ts`). Sem `className` de fora, como todo componente base.

**Contrato**

```ts
export type PapelDaFatia = '1' | '2' | '3' | '4' | 'pendente' | 'outras'
export type Fatia = { key: string; label: string; cents: number; shareBp: number; papel: PapelDaFatia }

type DonutChartProps = {
  /** ≤ 6, já dobradas, na ordem do anel (horário, a partir do topo). */
  fatias: readonly Fatia[]
  totalCents: number
  /** O que vai sob o número: "setembro". */
  rotuloDoCentro: string
  /** Texto do <figcaption>. */
  legenda: string
  loading?: boolean | undefined
}

export const MAX_FATIAS_NOMEADAS = 4
export function dobrarParaRosca(
  grupos: readonly { key: string; label: string; cents: number; shareBp: number; pendente?: boolean }[],
): { fatias: Fatia[]; papelPorChave: ReadonlyMap<string, PapelDaFatia> }
```

`dobrarParaRosca` recebe os grupos **na ordem do servidor**, descarta `shareBp = 0`, dá `'1'`…`'4'`
às quatro primeiras não pendentes, `'pendente'` à pendente onde quer que esteja, dobra o resto
numa fatia `'outras'` (`key: 'outras'`, `label` `Outras (N categorias)` / `Outra (1 categoria)`,
somas inteiras de `cents` e `shareBp`) colocada **por último**, e devolve o papel de **cada**
grupo — os dobrados recebem `'outras'`, que é o que a tabela usa na amostra. Casos que o teste
cobre: 1 grupo (anel inteiro), 4 (sem Outras), 5 (`Outra (1 categoria)`), 6+ (`Outras (N)`),
pendente em qualquer posição (nunca dobra), `shareBp = 0` (some do anel e não conta no N).

**DOM**

```html
<figure class="figura" aria-busy={loading || undefined}>
  <div class="rosca">
    <svg viewBox="0 0 240 240" aria-hidden="true" focusable="false" class="svg">
      <defs>
        <pattern id={hachuraId} width="6" height="6" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
          <line x1="0" y1="0" x2="0" y2="6" class="hachura" vector-effect="non-scaling-stroke" />
        </pattern>
      </defs>
      <path class="fatia" data-fill="1" d="…" vector-effect="non-scaling-stroke"><title>Alimentação · R$ 2.100,00 · 41,2%</title></path>
      <path class="fatia" data-fill="pendente" d="…" vector-effect="non-scaling-stroke"><title>Sem categoria · R$ 300,00 · 5,9%</title></path>
      <path class="fatia" data-fill="outras" fill="url(#…)" d="…" vector-effect="non-scaling-stroke"><title>Outras (6 categorias) · R$ 630,00 · 12,3%</title></path>
    </svg>
    <p class="centro">
      <span class="sr-only">Total de </span>
      <MoneyText cents={totalCents} emphasis="hero" />
      <span class="centroRotulo"><span class="sr-only">em </span>setembro</span>
    </p>
  </div>
  <ol class="legenda">
    <li><Swatch papel="1" /><span class="nome">Alimentação</span><span class="parte">41,2%</span></li>
    …
    <li><Swatch papel="outras" /><span class="nome">Outras (6 categorias)</span><span class="parte">12,3%</span></li>
  </ol>
  <figcaption class="figcaption">Distribuição por categoria — os valores estão na tabela abaixo.</figcaption>
</figure>
```

**Geometria** (unidades do `viewBox`): centro (120, 120), raio externo **R = 116**, raio interno
**r = 86** — anel de 30, furo de 172, margem de 4 para o traço não cortar. Ângulos a partir do
topo, sentido horário, proporcionais a `shareBp` sobre a soma das fatias visíveis (geometria,
não dinheiro): `P(θ, ρ) = (120 + ρ·sin θ, 120 − ρ·cos θ)`; fatia de `a0` a `a1` (radianos):
`M P(a0,R) A R R 0 {a1−a0>π ? 1 : 0} 1 P(a1,R) L P(a1,r) A r r 0 {mesmo} 0 P(a0,r) Z`. Uma fatia
só: `M120 4A116 116 0 1 1 120 236A116 116 0 1 1 120 4ZM120 34A86 86 0 1 0 120 206A86 86 0 1 0
120 34Z` com `fill-rule="evenodd"` — é `<path>` e não `<circle>` para que **uma** regra de
`fill` sirva a todos os casos (num `<circle>` a cor iria no `stroke`, e `forced-colors` teria
duas regras para manter).

**CSS** (`DonutChart.module.css`, só tokens)

- `.figura`: `display: grid; grid-template-columns: auto minmax(0, 24rem); grid-template-areas:
  "rosca legenda" "figcaption figcaption"; column-gap: var(--space-6); row-gap: var(--space-4);
  align-items: center; padding: var(--space-5) var(--space-4); border-block-end: 1px solid
  var(--border)`. Abaixo de 40rem: uma coluna, áreas empilhadas `rosca` / `legenda` /
  `figcaption`, `padding: var(--space-4) var(--space-3)`. **Alinhada à esquerda** como tudo no
  app — o anel não se centraliza no celular (regra do `EmptyState`: bloco centralizado quebra o
  eixo de leitura).
- `.rosca`: `position: relative; inline-size: min(100%, 15rem); aspect-ratio: 1`. `.svg`:
  `display: block; inline-size: 100%; block-size: 100%`. 15rem escala com o zoom de fonte, como
  o número dentro.
- `.fatia`: `stroke: var(--surface); stroke-width: 2; stroke-linejoin: miter; transition: opacity
  var(--motion-fast) var(--ease)`. **O traço em cor de superfície é o gap de 2 px entre fatias**
  que a skill pede (`vector-effect="non-scaling-stroke"` o mantém em 2 px em qualquer tamanho) —
  não é borda: nunca `--border`, nunca `--ink` no traço. `[data-fill="1"] { fill: var(--chart-1) }`
  … `[data-fill="4"]`; `[data-fill="pendente"] { fill: var(--chart-pending) }`; `outras` recebe o
  `fill="url(#…)"` inline. `.hachura { stroke: var(--ink-muted); stroke-width: 1 }` — período 6,
  linha 1: cerca de 17 % de cobertura, uma textura leve, estática, a 45° (o único ângulo que a
  skill admite, com o espelho a 135°).
- Hover (`@media (hover: hover)`): `.fatia:hover { opacity: 0.82 }` — o único feedback, e é o
  que anuncia que o `<title>` vem. Com ΔL ≥ 0,16 entre passos, a fatia clareada não alcança a
  vizinha. `prefers-reduced-motion: reduce` → `transition: none`. **Nenhuma animação de
  entrada**, nenhum "desenhar o arco", nenhuma fatia deslocada, nenhum gradiente, nenhuma
  sombra, nenhum 3D.
- `.centro`: `position: absolute; inset: 0; padding: 20%; display: flex; flex-direction: column;
  align-items: center; justify-content: center; text-align: center; gap: 2px`. Dentro, o
  **`MoneyText emphasis="hero"`** — variante nova do componente base, canônica:
  `&[data-emphasis="hero"] { font-weight: 700; font-size: var(--text-22) }`, Fraunces tabular
  como todo valor do app. Cabe nos 144 px úteis do furo até `123.456,78`; a 28 px não caberia —
  é o furo que dita o corpo. `.centroRotulo`: `font: 400 var(--text-13) var(--font-ui); color:
  var(--ink-muted)`.
- `.legenda`: `list-style: none; display: flex; flex-direction: column; gap: var(--space-2)`;
  `li { display: grid; grid-template-columns: var(--space-3) minmax(0, 1fr) auto; column-gap:
  var(--space-2); align-items: center }`. `.nome`: `--text-15`, `--ink`, ellipsis; `.parte`:
  `--text-15`, `--ink-muted`, `tabular-nums`, `text-align: end`. Legenda **sempre presente** —
  com uma fatia só ela é uma linha, e é ela que diz o nome da categoria única.
- `.figcaption`: `font-size: var(--text-13); color: var(--ink-muted)`.
- **`Swatch`** (`Swatch.tsx`, `{ papel: PapelDaFatia }`): `<svg width="12" height="12"
  viewBox="0 0 12 12" aria-hidden="true" focusable="false">` com um `<rect width="12"
  height="12">` que recebe `data-fill` (mesmas regras de `.fatia`, sem traço) ou, para
  `outras`, uma `<pattern>` própria (id por `useId`). 12 px = `--space-3`; **cantos retos** — é
  uma amostra impressa, não um botão. Serve a legenda **e** a tabela: um componente, um mapa.
- **Carregando** (`loading`): o anel inteiro em `fill: var(--surface-sunken)` (a cor-base do
  `Skeleton`, sem o brilho — SVG não recebe o `background-image`), `Skeleton width="6rem"
  height="1.375rem"` no centro, três `Skeleton width="10rem" height="1rem"` na legenda,
  `aria-busy="true"` na figura e **sem** `role="status"` próprio: quem anuncia é a `DataTable
  loading` logo abaixo — uma live region só.

**`forced-colors: active`**: `.svg { forced-color-adjust: none }` e, dentro dele, cores de
sistema explícitas: `.fatia { fill: Canvas; stroke: CanvasText; stroke-width: 1; opacity: 1 }`;
`[data-fill="1"] { fill: CanvasText }` (a maior, sólida); `.hachura { stroke: CanvasText }`
(Outras continua hachurada). `Swatch` idem (`rect` com `stroke: CanvasText`, `forced-color-adjust:
none`). O anel vira desenho de contorno com as divisões visíveis e três níveis (sólido, vazio,
hachura); identidade passa a ser ordem + legenda + tabela — que é a fonte de qualquer forma.

### (d) Tela `/relatorios/categorias`

**Casca**: item **`Relatórios`** na navegação, logo depois de `Transferências` — movimento
(Lançamentos, Transferências, Relatórios) antes de cadastro (Contas, Categorias) —, ícone
`ChartIcon`, `to: '/relatorios/categorias'`. Quando a E6 trouxer os outros relatórios, o item
passa a `/relatorios` com sub-navegação, sem mover esta rota (ADR-027e). `document.title`
`Gastos por categoria · HomeFinance` / `Receitas por categoria · HomeFinance`. Largura `62rem`,
mesmo cabeçalho de `/transferencias`: `<h1>` (foco na troca de rota) **`Gastos por categoria`**
/ **`Receitas por categoria`** e apoio `Para onde foi o dinheiro em setembro.` / `De onde veio o
dinheiro em setembro.` O mês é o da casca (`?mes`), e a query do relatório fica sob o prefixo
`['transactions', …]` (ADR-027).

**Faixa** (a `.faixa` de `/transferencias`, dentro de `Panel padding="none"`): à esquerda
`Select density="compact" label="Natureza"` com `Despesas` / `Despesas no crédito` /
`Despesas no débito` / `Receitas` — **sem placeholder** (sempre há valor);
`?natureza=despesas|despesas-credito|despesas-debito|receitas`, ausente = despesas, validado em
`search.ts` por allowlist (`naturezaValida`) e traduzido pela tela — e só por ela — para o par
`{kind: expense|income, accountGroup: credit|debit|ausente}` da API (ADR-032, 18/09/2026). Os dois
recortes de conta são despesas: `Despesas` = crédito + débito. Trocar de opção é trocar de busca,
não de rota — `keepPreviousData` mantém o quadro anterior a 0,6 com `aria-busy` e o foco fica no
seletor. À direita `.resumo` (o mesmo estilo de `.saldo`):
`<MoneyText format="currency" />` + ` em 87 lançamentos` (`em 1 lançamento`). Carregando:
`Skeleton width="11rem" height="1rem"`. Vazio: sem resumo (o `EmptyState` já diz).

**Figura**: o `DonutChart` de (c), entre a faixa e a tabela; `rotuloDoCentro = nomeDoMes(mes)`;
`legenda = "Distribuição por categoria — os valores estão na tabela abaixo."`; `fatias` e
`papelPorChave` de `dobrarParaRosca(grupos)`, onde `pendente` é o grupo com `categoryId: null`.

**Refetch mantém o quadro** (anti-padrão "skeleton flash on refetch" da skill): a query usa
`placeholderData: keepPreviousData`; com dado na tela e `isFetching`, figura e tabela recebem
`aria-busy="true"` e `opacity: 0.6` (`transition: opacity var(--motion-base) var(--ease)`; sem
transição em `prefers-reduced-motion`) — sem skeleton, sem salto de altura. Skeleton só em
`isPending` (primeira carga). Trocar mês ou natureza é o caso comum desta tela, e a rosca não
pode piscar a cada seta.

### (e) Tabela

`DataTable caption="Gastos por categoria em setembro de 2026"` (ou `Receitas…`), **linhas
planas** na ordem do servidor, sem `groups` — a hierarquia é de dois níveis e a indentação já a
mostra; um `<tbody>` por grupo repetiria o nome como cabeçalho e depois como linha. Colunas:
`Categoria` (absorve largura) · `Lançamentos` (end, `width: 'min'`, `hideBelow: 'sm'`) ·
`Participação` (end, `min`) · `Valor` (end, `min`).

- **Linha de grupo**: célula Categoria = `display: flex; align-items: center; gap: var(--space-2)`
  com `Swatch papel={papelPorChave.get(id)}` + nome em **peso 600**, `--ink`. Grupos dobrados em
  Outras levam a amostra hachurada — é o mapa "estes, juntos, são a fatia hachurada".
  `Lançamentos` = contagem do grupo com os filhos, `tabular-nums`; `Participação` = `shareBp` do
  grupo com duas casas; `Valor` = `MoneyText` neutro, **plain** — o cabeçalho já diz que é
  dinheiro, e `R$` na coluna desalinharia os dígitos (mesma regra de `/transferencias`).
- **Subcategoria**: sem amostra; `padding-inline-start: calc(var(--space-3) + var(--space-2))`
  — alinha o nome com o do grupo, depois dos 12 px da amostra e 8 px de gap; prefixo `sr-only`
  `em Alimentação: `; peso 400.
- **`Sem subcategoria`**: só quando o grupo tem filhos **e** `directCount > 0`; mesmo recuo,
  `--ink-muted`, prefixo `sr-only` `em Alimentação: `; números de `direct*` — e como o servidor
  garante `grupo.shareBp = directShareBp + Σ filhos`, a coluna fecha visivelmente.
- **Arquivada**: sufixo ` (arquivada)` em `--ink-muted`, peso 400, na mesma célula. **Sem**
  `data-archived`: o valor é dinheiro real e não se apaga num relatório.
- **`Sem categoria`**: linha de grupo com `Swatch papel="pendente"`, nome em 600 e, na mesma
  célula, `·` (`.separador`, `--border-strong`, `aria-hidden`) + `TextLink to="/lancamentos"
  search={{ mes, semCategoria: 1 }}` com texto **`Categorizar`** e `aria-label="Categorizar os
  lançamentos sem categoria de setembro"`. **Não há `Alert` de pendência nesta tela**: a fatia
  em `--chart-pending`, a linha e o link *são* a faixa de pendência aqui — e a ação leva ao filtro
  de `/lancamentos`, que é a tela que resolve. Três avisos para a mesma dívida seriam ruído.
- **`<tfoot>`**: `<th scope="row">Total</th>` · contagem (a `<td>` leva `data-hide="sm"`, como a
  coluna — o rodapé é `ReactNode` cru e não herda o `hideBelow`) · `100,00%` · `MoneyText
  emphasis="total"`.
- **Celular** (< 40rem): `Lançamentos` some e reaparece como `.secundaria` sob o nome (`12
  lançamentos` / `1 lançamento`), em toda linha — o mecanismo de `/transferencias`. Esconder
  coluna só é honesto quando o dado reaparece.

### (f) Estados

- *Carregando* (`isPending`): faixa com o `Select` ativo (a natureza vem da URL, não espera
  nada) e o resumo em `Skeleton`; `DonutChart loading`; `DataTable loading`.
- *Erro*: `Alert tone="error" title="Não foi possível carregar o relatório."` com
  `messageForError` e `Button` `Tentar de novo` (`loading={isFetching}`), no lugar do painel —
  como em `/transferencias`.
- *Vazio* (`totalCents = 0` e nenhum grupo): dentro do painel, abaixo da faixa (sem resumo),
  `EmptyState title="Nenhuma despesa em setembro de 2026." description="O relatório aparece
  assim que houver lançamentos no mês — registre um em Lançamentos ou importe o extrato."`
  com `action` = `Button variant="primary"` `Importar extrato` (→ `/importar`). Receitas:
  `Nenhuma receita em setembro de 2026.`
- *Só `Sem categoria`* **não é vazio**: anel inteiro em `--chart-pending`, legenda com uma
  linha, tabela com a linha e o link `Categorizar`. É o mês que mais precisa do link.

### (g) `ChartIcon`

`components/icons/ChartIcon.tsx`, contrato de `types.ts`, traço 1.5, `currentColor`, viewBox 24,
`strokeLinecap`/`strokeLinejoin` `round` como os demais. Uma pizza com o quarto superior direito
fechado e o resto em arco aberto (as pontas do arco param 23° antes das arestas do quarto, o que
deixa ~1,4 px de ar depois do traço). É pizza, e não rosca, porque a 20 px o furo não lê — e o
item nomeia a seção `Relatórios`, não este gráfico. Massa óptica de 4,75 a 19,25, como
`TransfersIcon`:

```
<path d="M9.17 5.33A7.25 7.25 0 1 0 18.67 14.83" />
<path d="M12 12V4.75a7.25 7.25 0 0 1 7.25 7.25z" />
```

### (h) Acessibilidade

- A tabela é a fonte (`caption`); o SVG é decorativo (`aria-hidden`, `focusable="false"`), com
  `<title>` nativo por fatia para quem usa mouse. Sem tooltip próprio, sem foco em fatia — o
  caminho de teclado é a tabela, e é o mesmo dado.
- Foco no `<h1>` na troca de rota; `Select` nativo.
- Contraste: todo texto em `--ink`/`--ink-muted` sobre `--surface` (≥ 4,5:1). As fatias são
  marcas: 1–3 ≥ 3:1, a 4ª em *relief* (2,24 / 2,46) coberta por legenda e tabela;
  `prefers-contrast: more` leva todas a ≥ 3:1.
- `forced-colors` em (c). `prefers-reduced-motion`: sem transição no hover nem no refetch.
- Live regions: só a da `DataTable loading`. O centro lê `Total de R$ 5.123,45 em setembro`.
- Teste do `grayscale(1)`: a rosca continua legível (ordem + legenda + hachura); só a fatia
  pendente perde a matiz — e a decisão que ela pede está no link da tabela, não na cor.

### (i) Copy pt-BR — tabela única

| Onde | Texto |
|---|---|
| Navegação | `Relatórios` |
| `document.title` | `Gastos por categoria · HomeFinance` / `Gastos no crédito por categoria · HomeFinance` / `Gastos no débito por categoria · HomeFinance` / `Receitas por categoria · HomeFinance` |
| `<h1>` | `Gastos por categoria` / `Gastos no crédito por categoria` / `Gastos no débito por categoria` / `Receitas por categoria` |
| Apoio | `Para onde foi o dinheiro em setembro.` / `Para onde foi o dinheiro do cartão de crédito em setembro.` / `Para onde foi o dinheiro que saiu direto das contas em setembro.` / `De onde veio o dinheiro em setembro.` |
| Filtro | `Natureza` — opções `Despesas` · `Despesas no crédito` · `Despesas no débito` · `Receitas`, nesta ordem (ADR-032, 18/09/2026). Os dois recortes ficam ENTRE `Despesas` e `Receitas` porque **são** despesas; URL `?natureza=despesas-credito` / `?natureza=despesas-debito`, traduzida pela tela para `kind=expense` + `accountGroup=credit|debit` |
| Resumo da faixa | `R$ 5.123,45 em 87 lançamentos` / `R$ 45,00 em 1 lançamento` |
| Centro da rosca | `5.123,45` + `setembro` (`sr-only` em volta: `Total de … em setembro`) |
| `<title>` da fatia | `Alimentação · R$ 2.100,00 · 41,2%` · `Sem categoria · R$ 300,00 · 5,9%` · `Outras (6 categorias) · R$ 630,00 · 12,3%` |
| Fatia dobrada | `Outras (6 categorias)` / `Outra (1 categoria)` |
| Legenda (`li`) | `Alimentação` `41,2%` |
| `<figcaption>` | `Distribuição por categoria — os valores estão na tabela abaixo.` |
| `caption` da tabela | `Gastos por categoria em setembro de 2026` / `Gastos no crédito por categoria em setembro de 2026` / `Gastos no débito por categoria em setembro de 2026` / `Receitas por categoria em setembro de 2026` |
| Colunas | `Categoria` · `Lançamentos` · `Participação` · `Valor` |
| Subcategoria (`sr-only`) | `em Alimentação: ` |
| Linha direta | `Sem subcategoria` |
| Arquivada | `Alimentação (arquivada)` |
| Sem categoria | `Sem categoria` · link `Categorizar` (`aria-label` `Categorizar os lançamentos sem categoria de setembro`) |
| Rodapé | `Total` · `87` · `100,00%` · `5.123,45` |
| Secundária no celular | `12 lançamentos` / `1 lançamento` |
| Participação | tabela `41,23%` · legenda e `<title>` `41,2%` |
| Vazio | `Nenhuma despesa em setembro de 2026.` / `Nenhuma despesa no crédito em setembro de 2026.` / `Nenhuma despesa no débito em setembro de 2026.` / `Nenhuma receita em setembro de 2026.` — descrição e botão iguais nas quatro: `O relatório aparece assim que houver lançamentos no mês — registre um em Lançamentos ou importe o extrato.` — `Importar extrato` |
| Erro | `Não foi possível carregar o relatório.` — `Tentar de novo` |

Léxico: mês por extenso em minúsculas (`setembro`); `Sem categoria` e `Sem subcategoria` são
palavras, nunca travessão; nada de "gráfico interativo", "insights" ou "visão geral".

### Checklist anti-cara-de-IA — E6a (aplicar com a tela pronta)

1. A rosca é **tinta**: as fatias usam só `--chart-1..4`, `--chart-pending` e a hachura em
   `--ink-muted`. Nenhuma paleta arco-íris, nenhuma matiz "por categoria", nenhum `--accent`
   no anel.
2. `filter: grayscale(1)` na tela: o anel continua legível pela ordem, a legenda e a hachura;
   nenhuma decisão depende da fatia pendente ser ocre.
3. **Nenhuma animação de entrada**, nenhum arco que "se desenha", nenhuma fatia deslocada ou
   destacada ao passar o mouse além de `opacity: 0.82`; nenhum gradiente, sombra ou 3D.
4. Nada escrito **dentro** das fatias: sem porcentagem sobre o arco, sem rótulo com linha-guia.
   A legenda é `<ol>` com amostra + nome + `%`, sempre presente.
5. O centro é **um** `MoneyText emphasis="hero"` + `setembro` — sem ícone, sem "TOTAL GASTO"
   em caixa alta, sem seta de tendência, sem segundo número.
6. O anel tem **≤ 6 fatias** e Outras é a última, hachurada; Sem categoria nunca dobra.
7. O traço entre fatias é `--surface` de 2 px (gap), nunca `--border` ou `--ink` (borda).
8. Tabela é `<table>` com `caption` e `<tfoot>`; nenhum "card" por categoria, nenhuma barra de
   progresso por linha, nenhum percentual pintado.
9. SVG `aria-hidden`; `<title>` nativo; nenhum tooltip flutuante próprio.
10. `Swatch` de 12 px, cantos retos, o mesmo componente na legenda e na tabela.
11. `ChartIcon` traço 1.5, `currentColor`, duas `<path>`, massa óptica dos vizinhos.
12. Estados desenhados: skeleton com a estrutura real (anel em `--surface-sunken`), erro com
    `Tentar de novo`, vazio com saída; mês só com `Sem categoria` **não** é vazio; refetch
    mantém o quadro a 0,6 de opacidade, sem skeleton.
13. `forced-colors`: contorno em `CanvasText`, a maior sólida, Outras hachurada — nada some.
14. Nenhuma medida, cor ou raio fora dos tokens em `DonutChart.module.css`, `Swatch` e
    `CategoryReportScreen.module.css`; os `--chart-*` vêm de `tokens.css`, não do módulo.
15. Percentuais só **formatados** (`shareBp` do servidor); a única soma do cliente é a dobra
    inteira de Outras. Duas casas na tabela, uma na legenda.
16. Copy da tabela (i), sem "poderoso", "inteligente", "visão 360°"; `Natureza` com as quatro
    opções da tabela (duas naturezas + dois recortes de conta, ADR-032) e sem placeholder.
17. Foco no `<h1>`; `Select` nativo; a 375 px a figura empilha, a coluna `Lançamentos`
    reaparece na `.secundaria`, e a página não rola na horizontal.

## Casca — barra de navegação inferior do celular (emenda de 17/09/2026)

A E6a levou a navegação de 5 para 6 itens e o rótulo passou a quebrar em pedaços sem hífen
(`Transf/erênci/as`, `Categ/orias`). A regra abaixo fecha o assunto **para sempre**, inclusive
para o 7º item: a barra é uma grade de células iguais (`grid-auto-columns: minmax(0, 1fr)`), a
página **nunca** rola na horizontal, e o rótulo se ajusta à célula — nunca o contrário.

**Medidas (Chromium, Public Sans, medidas reais):** célula = 49 px a 320 px, 58 px a 375 px,
67 px a 430 px (barra com `padding: var(--space-2)` e `gap: 2px`). Largura mínima para caber em
**duas** linhas, com hífen suave: `Transferências` 51 px a 13 px e **47 px a 12 px**;
`Lançamentos` 45/41,5; `Categorias` 37,5/34,5; `Relatórios` 34,5/32. Daí as três decisões:

1. **`--text-12` no rótulo da barra** (só aqui). A 13 px, 320 px não cabe — e três linhas num
   item é o defeito que originou esta emenda.
2. **`padding-inline: 0` no item**: a célula inteira é o alvo de toque e todo o ar é do rótulo
   centrado. Com 4 px de cada lado sobravam 41 px e nem a sílaba cabia.
3. **Quebra controlada por hífen suave (`\u00AD`) no rótulo**, com `hyphens: manual`.
   `hyphens: auto` depende de dicionário do navegador — o Chromium do CI não tem pt-BR, e foi
   por isso que a quebra saiu sem hífen. `overflow-wrap: anywhere` é **proibido** em rótulo:
   cria oportunidade de quebra em toda letra e **vence** o hífen suave (`Transf/erênci/as`);
   a rede de segurança é `overflow-wrap: break-word`, que só age quando não há hífen possível.

**Regras do rótulo de navegação:**

- O hífen suave marca **todas as fronteiras silábicas** do rótulo, exceto a que deixaria menos
  de 3 caracteres numa linha (`Ca\u00ADte\u00ADgo\u00ADrias`, nunca `…ri\u00ADas`).
  Rótulo que cabe sempre não leva hífen nenhum (`Painel`, `Contas`).
- No código o caractere é escrito como escape `\u00AD` — literal invisível no fonte é armadilha
  de manutenção.
- O `<Link>` leva `aria-label` com o rótulo **limpo**: o nome acessível não carrega hífen suave,
  e o nome falado/consultado continua idêntico ao visível (WCAG 2.5.3).
- Altura reservada de **duas linhas** (`min-block-size: 2lh`): a barra não muda de altura
  conforme a palavra, e a primeira linha de todos os itens fica na mesma base.
- **Piso só-ícone** (`@container (max-width: 18.25rem)` na barra — 6 × 47 px + 5 × 2 px): quando
  nem a sílaba cabe (zoom 200 %, fonte grande do navegador, 280 px), o rótulo sai e ficam os
  ícones, com o nome no `aria-label`; o item ganha `padding-block: var(--space-3)` para manter
  44 px de alvo. É **piso**, não o padrão: a 320 px os rótulos continuam visíveis. A consulta é
  de **container** e em `rem` de propósito — assim ela também acerta quando a pessoa aumenta a
  fonte do navegador, o que uma media query de viewport não veria.
- Rótulo novo na navegação só entra se a sua maior sílaba couber em **47 px a `--text-12`**
  (≈ 2,9 rem). Não coube: o nome está longo demais para a barra, e é o nome que muda.

**`--nav-bar-h`** (token novo): altura real da barra, calculada, com `env(safe-area-inset-bottom)`
embutido — `4 × var(--space-2) + 20px + var(--space-1) + 2,3 × var(--text-12) + 1px` = 84,6 px a
root de 16 px; `0px` no desktop. **Todo** conteúdo fixo na base do viewport reserva por ele:
`.main` usa `padding-block-end: calc(var(--space-6) + var(--nav-bar-h))` e a região de toasts,
`inset-block-end: calc(var(--space-4) + var(--nav-bar-h))`. Número mágico (`3.5rem`) na folga da
base é **erro de revisão**: a reserva antiga era menor que a barra somada à safe area do iPhone,
e o fim da tabela ficava debaixo dela.

**Separador `·` em célula estreita** (regra geral, não só do relatório): `·` só existe entre dois
itens **na mesma linha**. Onde a célula pode quebrar (`.nomeDoGrupo` com `flex-wrap: wrap`), no
celular o bloco da ação vai para a própria linha (`flex-basis: 100%`, `row-gap: 2px`) e o
separador some (`display: none`) — `·` órfão abrindo a linha é descuido, não pontuação.

## E7 — investimentos: `/investimentos`, as barras de 12 meses e o léxico do aporte (spec 0006, 17/09/2026)

Estende E2, E2c e E6a sem revogar nada: `grayscale(1)` continua sendo o aceite, os quatro
portadores de estado continuam os únicos, e a cor cromática continua com três donos. É o
**segundo gráfico do produto** (ADR-021) e o primeiro com **duas séries** — por isso esta seção
também fixa o que toda série dupla herda. Normativo para o `dev-frontend-react`; medidas e cores
só por token.

**Nenhum token novo.** As duas séries saem da rampa `--chart-*` que a E6a já validou, os números
usam variantes de `MoneyText` que já existem e o movimento reaproveita `--motion-fast`. Uma tela
que não pede cor nova é o sinal de que a identidade está de pé.

### (a) Uma palavra só: **aporte**

`aporte` x `investimento` x `aplicação` — a tela usa **aporte** para o movimento e
**Investimentos** apenas para o nome da seção (menu, `<h1>`, grupo de categoria). Por quê:

1. **Aporte é contável, investimento não.** A tela conta eventos: "3 aportes em setembro".
   "3 investimentos" sugere três produtos — ativo, corretora, posição —, que é exatamente o que a
   spec 0006 §2.2 tira do escopo. O nome do movimento não pode prometer carteira.
2. **`aplicação` colide com o vocabulário do próprio app** ("aplicativo") e é a palavra do caixa
   eletrônico, não a da casa.
3. É a palavra da **copy já aprovada** pelo usuário na spec (§3.4.6: "Nenhum aporte ou resgate em
   setembro de 2026").

Contraparte: **resgate**, sempre. Verbo, só na voz do apoio ("o que saiu para investir e o que
voltou") — a tela não conjuga "aportar" em lugar nenhum. "Patrimônio", "rentabilidade",
"posição", "carteira", "meta" e "acumulado" são **palavras proibidas** nesta tela: nenhuma delas
descreve um dado que o contrato entrega.

| Situação | Palavra |
|---|---|
| lançamento com categoria de natureza `investment` | **Aporte** |
| lançamento com categoria de natureza `redemption` | **Resgate** |
| a coluna que os separa na tabela | **Movimento** |
| o conjunto, quando os dois cabem numa frase | **aportes e resgates** |
| a seção do app | **Investimentos** |

### (b) Como aporte e resgate se distinguem — e por que nenhum dos dois é colorido

`--income`/`--expense` são **proibidos** nesta tela. Não por escassez de cor, mas porque diriam a
coisa errada: aporte não é gasto (o dinheiro continua sendo da casa) e resgate não é ganho (é
dinheiro que já era dela voltando). Pintar o aporte de vermelho ensinaria à pessoa exatamente o
erro que a entrega existe para corrigir. `--accent` é ação e `--warning` é pendência de decisão —
nenhum dos dois é isto. Logo, **`/investimentos` é cromaticamente silenciosa**, como
`/transferencias`: todo valor é `MoneyText` neutro, **sem sinal** (`sign="auto"`) e sem tom.

Os portadores, na ordem de força:

1. **Posição** — aporte é sempre o primeiro (linha de cima da `<dl>`, barra da esquerda no grupo,
   coluna da esquerda na tabela de 12 meses). Resgate é sempre o segundo. Em toda a tela, sem
   exceção.
2. **Palavra** — o rótulo está **sempre** presente: `Aportes`/`Resgates` nos números, `Aporte`/
   `Resgate` na coluna `Movimento` da lista, `Aportes`/`Resgates` no cabeçalho da tabela de 12
   meses e na legenda do gráfico. Nunca uma bolinha, nunca uma seta sozinha, nunca só a cor.
3. **Tinta (só na marca do gráfico)** — `--chart-1` para aportes, `--chart-3` para resgates.

**Regra nova, para toda série dupla do produto: duas séries usam `--chart-1` e `--chart-3`, nunca
1 e 2.** Um passo de separação é o que basta para **vizinhas de uma rampa ordinal** (onde a ordem
já é o portador); duas séries que se comparam lado a lado, barra contra barra, precisam do dobro
da distância. Medido na tabela da E6a: 1↔2 dá ΔE 16,2 (claro) / 15,3 (escuro); 1↔3 é um salto de
dois passos, com folga sobre o piso de 15 nos dois temas, e as duas marcas continuam ≥ 3:1 sobre
`--surface` (15,65:1 e 4,22:1 claro; 13,71:1 e 4,71:1 escuro) — nenhuma precisa de *relief*.
A série protagonista recebe `--chart-1`. Em `grayscale(1)` nada muda: a rampa já é tinta.

### (c) `BarChart` — barras agrupadas, duas séries

`frontend/src/components/BarChart/`: `BarChart.tsx`, `BarChart.module.css` (+ testes). Sem
`className` de fora, como todo componente base. `Swatch` **sai** de `components/DonutChart/` para
**`components/Swatch/`** (`Swatch.tsx` + `Swatch.module.css`, com o mapa de `data-fill` e o bloco
de `forced-colors` junto), e os dois gráficos passam a importar de lá — é o mesmo movimento que
`lib/keywords.ts` fez na E2c. Uma amostra, um mapa, dois gráficos.

**Forma: agrupadas, nunca empilhadas e nunca divergentes.**

- *Empilhada* somaria aporte com resgate. Essa soma não existe: são direções opostas, e a altura
  total seria um número que ninguém pode ler.
- *Divergente* (aporte para cima, resgate para baixo de uma linha zero) codificaria "positivo x
  negativo" — a mesma mentira que a proibição de `--income`/`--expense` acabou de recusar.
- *Agrupada* põe as duas na mesma base e na mesma escala: comparar é olhar. É o padrão da skill
  `dataviz` para duas séries no tempo.

**Contrato**

```ts
export type SerieDaColuna = { papel: '1' | '3'; cents: number }
export type ColunaDoGrafico = {
  key: string
  /** Rótulo do eixo: "set". Decorativo — a tabela tem o mês por extenso. */
  rotulo: string
  /** Uma entrada por série, na ordem fixa das séries. */
  valores: readonly SerieDaColuna[]
  /** `<title>` de cada barra, na mesma ordem. */
  titulos: readonly string[]
  /** Desenha a virada de ano ANTES desta coluna. */
  viradaDeAno?: boolean | undefined
}

type BarChartProps = {
  colunas: readonly ColunaDoGrafico[]
  series: readonly { papel: '1' | '3'; nome: string }[]   // exatamente 2
  /** O maior valor da série — SELECIONADO (`Math.max` de inteiros), nunca calculado. */
  maximoCents: number
  legenda: string
  loading?: boolean | undefined
}
```

**Geometria** (unidades do `viewBox` `0 0 480 240`): 12 colunas de **40**; dentro de cada uma,
duas barras de **13** com **3** de vão, centradas (sobra 5,5 de cada lado — o vão entre grupos é
11, quase quatro vezes o vão do par, e é isso que faz o par ler como par). Barra *k* da coluna
*i*: `x = 40·i + 5,5 + k·16`. Base em `y = 236`, topo útil em `y = 4`, altura de plotagem
**232**: `altura = cents === 0 ? 0 : max(3, round(232 · cents / maximoCents))`, `y = 236 −
altura`. **Valor zero não desenha barra** (regra da E6a para `shareBp = 0`); valor mínimo
desenha 3 unidades — o fio que diz "houve algo" sem fingir altura.

- **Cantos retos.** Raio em barra é decoração e distorce o valor nas baixas.
- **Sem eixo Y, sem malha, sem rótulo sobre a barra.** O único traço de referência é a **linha de
  base** (`--border`, 1 unidade, desenhada por último, de 0 a 480). A escala é dita **em palavras**
  no `<figcaption>` ("a barra mais alta é R$ 2.400,00") e os 24 números estão na tabela ao lado.
  Números escritos sobre 24 barras seriam o oposto de "números são protagonistas": seriam ruído.
- **Virada de ano**: `<line>` de 1 unidade em `--border`, de `y=4` a `y=236`, em `x = 40·i` antes
  da primeira coluna de janeiro. Não é desenhada quando janeiro é a primeira coluna. É o único
  marcador de ano do desenho — silencioso, sem texto, na linguagem de pauta do caderno.
- **Eixo X em HTML, não em `<text>` do SVG**: `<ol class="eixo" role="list" aria-hidden="true">`
  com `grid-template-columns: repeat(12, minmax(0, 1fr))`, `--text-13`, `--ink-muted`,
  `text-align: center`. Texto dentro do `viewBox` encolheria junto com ele (13 px viram 8,4 px a
  375 px) e ignoraria o zoom de fonte. Como a plotagem ocupa 100 % da largura e as 12 colunas são
  iguais, a grade HTML cai exatamente sobre os grupos — **por isso o bloco da plotagem tem
  `aspect-ratio: 2 / 1`, idêntico ao `viewBox`**: qualquer outra proporção liga o
  `preserveAspectRatio` e desalinha os rótulos. **Abaixo de 24rem de container o eixo passa a seis
  rótulos, alternando a partir do FIM** (`li:nth-last-child(even) { visibility: hidden }` — a
  célula continua ocupando a coluna, então a grade não se desloca). Contado do fim, e não do
  começo, por uma razão só: o último rótulo é o **mês selecionado**, que é o assunto da tela e
  nunca pode ser o que some — `nth-child(even)` esconderia exatamente a 12ª coluna. O que se perde
  é o rótulo do mês mais antigo, e o período inteiro está escrito na `caption` da tabela ao lado.
  Contar do fim também sobrevive a uma série de tamanho ímpar, se um dia existir, e não deixa dois
  rótulos vizinhos na ponta (o que `:not(:last-child)` faria). Medida do limiar: rótulo de três
  letras a `--text-13` mede ~21 px, então 12 células pedem ≥ 24 px (18rem de container) só para
  não colidir e ~28 px (21rem) para ter ar; abaixo de **24rem** (384 px) seis rótulos, com
  passo de ~52 a 64 px, leem melhor do que doze espremidos. O limiar é de **container** e em
  `rem` — assim o zoom de fonte também o dispara. **Quem estabelece o container é a `.figura`; quem
  consulta é a `.eixo`, descendente dela** — elemento nenhum consulta o container que ele mesmo
  estabelece, e foi esse o defeito que a seção dos 12 meses teve antes de rodar na UI real.
- **Rótulo do eixo é o único lugar do app com mês abreviado.** `mesCurto(mes)` em `lib/month.ts`
  devolve `jan`…`dez` (minúsculas, sem ponto) e serve **só** ao eixo decorativo; na tabela, na
  `caption` e em qualquer texto lido o mês continua por extenso (`mesPorExtenso`).

**CSS** (`BarChart.module.css`, só tokens)

- `.figura`: `display: flex; flex-direction: column; gap: var(--space-3); padding: var(--space-4)`.
- `.plotagem`: `inline-size: 100%; aspect-ratio: 2 / 1`. `.svg`: `display: block; inline-size:
  100%; block-size: 100%`.
- `.barra[data-fill="1"] { fill: var(--chart-1) }`, `[data-fill="3"] { fill: var(--chart-3) }`;
  sem traço (o vão é geometria, não `stroke`), com `vector-effect="non-scaling-stroke"` para que
  o contorno de `forced-colors` saia com 1 px em qualquer tamanho.
- `.base`, `.viradaDeAno`: `stroke: var(--border); stroke-width: 1`.
- Hover (`@media (hover: hover)`): `.barra:hover { opacity: 0.82 }`, `transition: opacity
  var(--motion-fast) var(--ease)` — o mesmo e único feedback da rosca, e é o que anuncia o
  `<title>`. `prefers-reduced-motion: reduce` → `transition: none`. **Nenhuma animação de
  entrada**: barra que "cresce" do chão é o clichê do dashboard genérico, e aqui seria uma
  animação por mês, doze vezes, a cada troca de mês.
- `.legenda`: `display: flex; flex-wrap: wrap; gap: var(--space-4)`; cada `li` é `display: flex;
  align-items: center; gap: var(--space-2)`; nome em `--text-15`, `--ink`. **Sempre as duas
  séries**, mesmo quando uma não tem nenhuma barra: sem a legenda completa, a tinta que sobrou
  vira ambígua.
- `.figcaption`: `--text-13`, `--ink-muted`.
- **Carregando** (`loading`): o eixo e a base são **reais** (derivam do mês da URL, não do
  servidor) e as 24 barras saem todas a 40 % da altura em `--surface-sunken` (`.barraVazia`), sem
  `<title>`; `aria-busy="true"` na figura e **sem** `role="status"` — quem anuncia é a `DataTable`
  ao lado. O platô achatado não se confunde com dado.
- **`forced-colors: active`**: `.svg { forced-color-adjust: none }`; `.barra[data-fill] { fill:
  Canvas; stroke: CanvasText; stroke-width: 1; opacity: 1 }` e `.barra[data-fill="1"] { fill:
  CanvasText }` — sólida contra vazada, as duas séries continuam distintas sem nenhuma cor;
  `.base`, `.viradaDeAno` e `.barraVazia` em `CanvasText`.

### (d) Tela `/investimentos`

**Casca**: item **`Investimentos`** na navegação, **depois de Transferências** e antes de
Relatórios (movimento antes de cadastro, como manda a ordem atual), ícone `CoinsIcon`, rótulo
visível `In\u00ADves\u00ADti\u00ADmen\u00ADtos`. `document.title` `Investimentos · HomeFinance`.
Largura `62rem`, mesmo cabeçalho de `/transferencias`: `<h1>` **`Investimentos`** (recebe o foco
na troca de rota) e apoio **`O que saiu para investir e o que voltou em setembro.`** — a frase diz
de saída que aporte é dinheiro que **sai da conta**, que é a dúvida número um da tela.

À direita do cabeçalho, no lugar de "Reprocessar transferências": `Button variant="secondary"`
**`Detectar investimentos`**. Ele existe quando a casa tem **ao menos uma categoria ativa** de
natureza `investment` ou `redemption` (dado de `GET /categories`); sem nenhuma, a tela inteira é o
vazio de (e.1), que já tem a sua própria chamada — dois convites para o mesmo passo é ruído. Com
categoria e **sem palavra-chave**, o botão continua existindo: **é o diálogo que ensina**, como o
`AutoCategorizeDialog` da E2c (e). Nunca `disabled`.

**Uma consulta só**, no molde de `/transferencias`: `useInfiniteQuery` sob a chave
`["transactions", "investments", mes]` — sob o prefixo `transactions` de propósito (ADR-027),
para que categorizar um lançamento em qualquer tela releia estes números. `monthly`, `yearToDate`
e `series` saem de `paginas[0]`; `items` acumula. `placeholderData: keepPreviousData`: com dado
na tela e `isFetching`, painéis e tabelas recebem `aria-busy="true"` e `opacity: 0.6`
(`transition: opacity var(--motion-base) var(--ease)`, sem transição em
`prefers-reduced-motion`). Trocar de mês é a interação principal desta tela — o quadro **não**
pode piscar a cada seta.

**Um `Panel padding="none"`** com três `<section>` separadas por `1px solid var(--border)`, na
ordem em que a pergunta se responde:

```
+- cabecalho -------------------------------------------------------------------+
| Investimentos                                      [ Detectar investimentos ]  |
| O que saiu para investir e o que voltou em setembro.                           |
+--------------------------------------------------------------------------------+
+- Panel (padding=none) --------------------------------------------------------+
| Em setembro                       | No ano, ate setembro                       |
| Aportes                2.000,00   | Aportes                        18.400,00   |
| 3 lancamentos                     | 22 lancamentos                             |
| --------------------------------- | ------------------------------------------ |
| Resgates                 850,00   | Resgates                        3.200,00   |
| 1 lancamento                      | 4 lancamentos                              |
+--------------------------------------------------------------------------------+
| Ultimos 12 meses, ate setembro                                                 |
|  +- figura ----------------------------+ +- tabela (fonte da verdade) -------+ |
|  |                #          |     #   | | Mes               Aportes  Resgat.| |
|  |             #  #          |     #   | | outubro de 2025  1.500,00     0,00| |
|  |    #  #     #  # :        | #   # : | | novembro de 2025 1.500,00     0,00| |
|  |  ----------------------------------- | | ...                              | |
|  |   out nov dez jan fev mar ... ago set| | setembro de 2026 2.000,00   850,00| |
|  |   [#] Aportes    [:] Resgates        | +-----------------------------------+ |
|  |   Aportes e resgates dos ultimos 12  |                                       |
|  |   meses - a barra mais alta e R$ ... |                                       |
|  +--------------------------------------+                                      |
+--------------------------------------------------------------------------------+
| Lancamentos de setembro                                                        |
| Data   Movimento  Conta   Descricao               Categoria        Valor       |
| 05/09  Aporte     Nubank  CDB 15 DIAS             CDB           2.000,00       |
| 12/09  Resgate    Nubank  RESGATE CDB             CDB             850,00       |
| 28/09  Aporte     C6      TESOURO SELIC           Tesouro       1.000,00       |
+--------------------------------------------------------------------------------+
| Mostrando 50 de 63 lancamentos                         [ Carregar mais 13 ]    |
+--------------------------------------------------------------------------------+
```

Celular (375 px), tudo empilhado e nada escondido sem reaparecer:

```
+------------------------------------+
| Investimentos                      |
| O que saiu para investir e o que   |
| voltou em setembro.                |
| [ Detectar investimentos ]         |
+------------------------------------+
| Em setembro                        |
| Aportes                 2.000,00   |
| 3 lancamentos                      |
| ---------------------------------- |
| Resgates                  850,00   |
| 1 lancamento                       |
| No ano, ate setembro               |
| Aportes                18.400,00   |
| 22 lancamentos                     |
| ---------------------------------- |
| Resgates                3.200,00   |
| 4 lancamentos                      |
+------------------------------------+
| Ultimos 12 meses, ate setembro     |
|   #       #      |    #            |
|   #  #    #  :   | #  #  :         |
|  ----------------------------------|
|  nov   jan   mar   mai   jul   set |  (seis, alternando do fim)
|  [#] Aportes   [:] Resgates        |
|  Aportes e resgates dos ultimos... |
|  Mes               Aportes Resgat. |
|  outubro de 2025  1.500,00    0,00 |
|  ...                               |
+------------------------------------+
| Lancamentos de setembro            |
| Data   Descricao          Valor    |
| 05/09  CDB 15 DIAS     2.000,00    |
|        Aporte . Nubank . CDB       |
| 12/09  RESGATE CDB       850,00    |
|        Resgate . Nubank . CDB      |
+------------------------------------+
| Mostrando 50 de 63 lancamentos     |
| [ Carregar mais 13 ]               |
+------------------------------------+
```

**1. Os números** (`padding: var(--space-4)`; `border-block-end`) — o dispositivo do
`TransferPairPanel`, sem inventar nada: `grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
gap: var(--space-5)`, uma coluna abaixo de 40rem; cada coluna é um título
(`.tituloDaLista`: `--text-13`, peso 600, `--ink-muted`) e uma `<dl>` de linhas separadas por
`--border`. **Não são quatro cartões com número gigante e ícone.**

- Colunas por **período**, não por fluxo: `Em setembro` e `No ano, até setembro`. A comparação que
  a pessoa faz é "este mês contra o ano", e o período é o eixo que muda entre as colunas.
- `dt`: a palavra (`Aportes` / `Resgates`, `--text-15`, `--ink-muted`) e, abaixo, a contagem em
  `--text-13`, `--ink-muted` (`3 lançamentos` / `1 lançamento` / `nenhum lançamento`). O `dt` é
  `display: flex; flex-direction: column`, e `.linha { align-items: baseline }` mantém a primeira
  linha do `dt` alinhada com o número.
- `dd`: `MoneyText` **neutro, sem sinal**. No mês, `emphasis="total"` (18 px, peso 700); no ano, o
  corpo normal (15 px, peso 600 pelo CSS da lista). Dois degraus de hierarquia com variantes que já
  existem — **nenhum número hero nesta tela**.
- Cada `dt` leva um sufixo `sr-only` com o período (` em setembro` / ` no ano, até setembro`):
  quatro rótulos "Aportes"/"Resgates" soltos numa lista de definição não se distinguem no áudio.
- **Nada de derivado.** Sem líquido, sem saldo investido, sem variação contra o mês anterior, sem
  percentual, sem seta de tendência (spec 0006 §3.4.2). Quem quer saber "quanto eu tenho" está na
  tela errada, e a tela não finge o contrário.
  ⚠️ Isso vale para **esta** tela. O **painel** (`/`) mostra, desde 18/09/2026, o investimento do mês como UM número **líquido** (aportes − resgates, com sinal, podendo ser negativo) — decisão do usuário registrada em `LICOES-FRONTEND.md`. As duas telas respondem a perguntas diferentes, e é essa diferença que autoriza números diferentes.
- Janeiro selecionado faz as duas colunas mostrarem os mesmos números. É correto e fica assim —
  não é um caso a "consertar".

**2. Últimos 12 meses** (`.tituloDaSecao` `Últimos 12 meses, até setembro`) — a seção é um
container (`container-type: inline-size`) com `grid-template-columns: minmax(0, 1.4fr) minmax(0,
1fr); gap: var(--space-5)`; **abaixo de 44rem de container, uma coluna** (figura em cima, tabela
embaixo). Consulta de container e em `rem`: assim ela também acerta quando a pessoa aumenta a
fonte do navegador — o que uma media query de viewport não veria.

- À esquerda, o `BarChart` de (c). `maximoCents` = `Math.max` dos 24 inteiros da série (seleção,
  não cálculo); cada `<title>` é `setembro de 2026 · Aportes · R$ 2.000,00`.
- À direita, a **fonte da verdade**: `DataTable caption="Aportes e resgates mês a mês, de outubro
  de 2025 a setembro de 2026"`, colunas `Mês` (auto) · `Aportes` (end, min) · `Resgates` (end,
  min). Mês **por extenso** (`setembro de 2026`) — abreviação só no eixo decorativo. Valores em
  `MoneyText` neutro e `plain` (o cabeçalho já diz que é dinheiro); mês sem movimento mostra
  `0,00`, nunca travessão: zero é um valor, e a coluna tabular o faz recuar sozinho.
- **Série inteiramente zerada: a seção não é renderizada.** Nem gráfico vazio, nem 12 linhas de
  `0,00`. Não há o que ilustrar, e o vazio da lista (e.2) já diz o que fazer.

**3. Lançamentos do mês** (`.tituloDaSecao` `Lançamentos de setembro`) — `DataTable
caption="Aportes e resgates de setembro de 2026"`, lista **plana**, na ordem do servidor.

- **Sem agrupamento por dia e sem subtotal**: aporte e resgate não somam, e um cabeçalho de dia sem
  subtotal é só ruído (em `/lancamentos` o subtotal é a razão de o grupo existir).
- Colunas: `Data` (min, `dataCurta`) · `Movimento` (min, `hideBelow: "sm"`) · `Conta` (min,
  `hideBelow: "sm"`) · `Descrição` (auto, elástica, ellipsis + `title`) · `Categoria` (min,
  `hideBelow: "sm"`) · `Valor` (end, min, `MoneyText` neutro, sem sinal).
- `Movimento` é texto puro `Aporte` / `Resgate` em `--ink`. **Não é `Badge`**: dezoito etiquetas
  numa coluna em que toda linha tem valor viram poluição, e a E2c já proibiu `Badge` nova.
- Abaixo de 40rem as três colunas `hideBelow` somem e reaparecem na `.secundaria` da descrição, na
  ordem `Aporte · Nubank · CDB` — com **`Aporte` em `--ink`**, e não no `--ink-muted` do resto da
  linha: é o portador principal da distinção e não pode enfraquecer na tela estreita.
- `source` do contrato **não é exibido**: a tela responde quanto e quando; a origem do registro é
  conferência de importação e vive em `/lancamentos`.
- Rodapé de paginação idêntico ao de `/transferencias` (`Mostrando 50 de 63 lançamentos` ·
  `Carregar mais 13`), com `role="status"` `sr-only` anunciando o crescimento.
- **A página é 50**, a mesma constante de `/transferencias` e de `/lancamentos` (`POR_PAGINA`).
  Uma lista do app tem **um** ritmo de paginação, e densidade é princípio (4): quem abre o mês quer
  o mês inteiro, não um "carregar mais" inventado para dar trabalho. **Os números de qualquer
  exemplo de copy deste documento são ilustração** — `Mostrando 50 de 63 lançamentos` mostra a
  FORMA da frase, nunca dita constante; constante mora na regra, não na tabela de copy.

### (e) Estados

1. **Sem categoria de investimento** (nenhuma ativa de `investment` nem `redemption`): no lugar do
   `Panel` inteiro, um `EmptyState` — não há número, série nem lista que possam existir.
   - título `Nenhuma categoria de investimento ainda.`
   - descrição `Esta tela mostra o que sai da conta para investir e o que volta em resgates. Crie em Categorias um grupo de natureza Investimentos — CDB, Tesouro, previdência — e os lançamentos passam a aparecer aqui.`
   - ação `Button variant="primary"` `Ir para categorias` (para `/categorias`, levando o `mes`).
2. **Com categoria e sem lançamento no mês**: o vazio é **da lista**, não da tela — os números do
   ano e a série dos 12 meses continuam valendo e continuam desenhados.
   - título `Nenhum aporte ou resgate em setembro de 2026.`
   - descrição `Um lançamento aparece aqui quando recebe uma categoria de investimento ou de resgate. Se o extrato do mês já foi importado, detecte pelas palavras-chave.`
   - ações: `Button variant="primary"` `Detectar investimentos` (abre o diálogo) e
     `Button variant="quiet"` `Ver os lançamentos de setembro` (para `/lancamentos`, com o mês).
3. **Carregando** (`isPending`): números com `Skeleton width="4.5rem" height="1rem"` em cada `dd`
   e **sem** a linha de contagem (contagem inventada é pior que ausência); `BarChart loading`;
   as duas `DataTable` em `loading`.
4. **Erro da API**: no lugar do `Panel`, `Alert tone="error" title="Não foi possível carregar os
   investimentos."` com `messageForError` e `Button` `Tentar de novo` (`loading={isFetching}`).
   Erro ao **carregar mais** nunca troca a tabela por um erro: as linhas lidas ficam e o `Alert`
   fica onde estava o botão, com `Não foi possível carregar mais lançamentos.`
   **Nenhum número parcial é exibido como se fosse total** — falhou, some.
5. **Mês inválido na URL**: resolvido por `mesDaURL` na casca, como em toda tela.

### (f) `DetectInvestmentsDialog`

`features/investments/components/`. É o `DetectTransfersDialog` da E2c com outro assunto — mesma
anatomia, mesmo ciclo prévia → confirmação, mesmo princípio de que **o número do toast é o que o
servidor devolveu**, nunca o da prévia.

- `Dialog` nativo, `title="Detectar investimentos"`, `description="Setembro de 2026 · aplica as
  palavras-chave das categorias de investimento e de resgate aos lançamentos sem categoria. O que
  já tem categoria não muda."`
- Corpo: frase-resumo `role="status"` e, abaixo, **uma** `DataTable` com até três grupos — `Viram
  aporte ou resgate · 5`, `Já têm categoria · 1` e `Continuam sem categoria · 2`. Colunas
  `Descrição` (auto) · `Movimento` (min) · `Categoria` (min). A célula `Categoria` leva a linha de
  proveniência da E2c (b) abaixo do nome (`92% · «cdb»`; com 100, só a palavra); no grupo dos que
  já têm categoria a célula é `Outros → CDB`, com `ArrowRightIcon size={14}` e `sr-only`
  `de Outros para CDB` — o mesmo dispositivo de direção de `/transferencias`.
- **Os três motivos de "Continuam sem categoria"** vêm do contrato, e cada um tem a sua palavra na
  célula `Categoria`, em `--ink-muted`: `abaixo de 80%` (`below_threshold`), `empate entre
  categorias` (`ambiguous`) e **`bateu com outra categoria`** (`other_category`) — a descrição
  bateu, sim, mas a palavra-chave vencedora é de uma categoria de despesa ou de receita, e esta
  detecção não escreve categoria comum. As três são curtas e paralelas **porque a coluna é
  `width: "min"`**: medida na UI real, "a melhor palavra-chave não é de investimento" quebra em
  cinco linhas, e motivo em cinco linhas deixa de ser lido. O **porquê** de cada um é dito uma vez
  só, na descrição do grupo — regra da E2: a palavra do grupo explica o motivo para o grupo, não
  linha a linha.
- **`overwriteCategorized` é um `<label class="alternador">` com checkbox nativo** (o padrão de
  `/contas`), entre a tabela e o rodapé, presente só quando `alreadyCategorized > 0`:
  `Trocar também a categoria de 1 lançamento que já tem uma`. Não é um segundo botão nem uma chave
  de modo: a caixa marcada muda o **rótulo do confirmar**, que é quem diz o que vai acontecer — a
  regra de "Blocos de decisão" e do atalho de categorização (h).
- Rodapé: `Cancelar` (quiet) e o confirmar (primary): `Marcar 5 lançamentos` · `Marcar 1
  lançamento` · com a caixa marcada, `Marcar 5 e trocar 1` · sem nada a fazer, `Nada a marcar` com
  `aria-disabled` (nunca `disabled`).
- Sucesso: fecha, toast com o número do servidor e invalida `["transactions"]` — o prefixo cobre a
  tela de investimentos, a lista, o `summary` e o relatório de uma vez. `categories` não muda.
- **Conflito (409) — o TOCTOU da detecção.** O plano é calculado **fora** da transação (para não
  segurar conexão do pool durante o cálculo) e, nessa janela, a categoria de destino pode deixar
  de qualificar. São **cinco** os qualificadores que o servidor reconfere (contrato: resposta
  `Conflict`): ser **da casa**, estar **viva**, **não estar arquivada**, **não ter subcategoria
  ativa** (spec 0005 §12) e continuar sendo de **natureza de investimento compatível com o lado do
  dinheiro** daquele lote. Faltando qualquer um, o servidor desfaz **tudo**: nada gravado, nada
  auditado, 409 sem campos — a forma do conflito de conversão de `/transferencias` (ADR-028d), com
  a causa trocada. Na tela, `Alert tone="error"` **no lugar da tabela**, com a ação **dentro do
  próprio `Alert`** (como em `/transferencias`), e o texto de (k). Três coisas decidem esse texto:
  1. **A primeira frase é a consequência, não a causa**: `Nada foi marcado.` A dúvida de quem
     acabou de clicar em "Marcar 5 lançamentos" e viu um erro é "gravou pela metade?", e ela se
     responde em três palavras, antes de qualquer explicação. O título já disse o que houve.
  2. **A causa é AGNÓSTICA e ensina a regra**: `a detecção só marca em categoria que pode receber
     lançamento` é a regra do app, que vale amanhã de novo e cobre os cinco qualificadores de uma
     vez. A copy anterior nomeava **dois** deles ("foi excluída, ou ganhou uma subcategoria"), e
     era armadilha: quem levasse 409 por **arquivamento** ou por **troca de natureza** lia uma
     causa que não aconteceu e ia procurar no lugar errado. **Nomear um subconjunto é pior do que
     não nomear nenhum** — a pessoa confia no que a tela diz. A regra escrita assim também não
     precisa ser revisitada quando o servidor ganhar um sexto qualificador.
  3. **Nenhum nome e nenhum id de categoria.** O backend não os envia de propósito (o erro
     atravessa o log), e a tela não os inventa a partir do cache: o cache está velho — é
     exatamente essa a notícia.
  A ação é **`Conferir de novo`**, e não "Tentar de novo": repetir gravaria sobre uma prévia velha.
  É literalmente o mesmo rótulo de `/transferencias` — mesma ação, mesmo nome no app inteiro. O
  texto **não** repete "confira a prévia de novo": o botão está logo abaixo, dentro do `Alert`.
- **Toda prévia nova nasce com a caixa de sobrescrever desmarcada** — depois do 409, do erro da
  prévia, de qualquer refazer. Autorização para substituir escolha humana **não se herda de um
  pedido que não aconteceu**: a prévia nova pode trazer outra contagem, e a própria etiqueta da
  caixa (`… de 1 lançamento que já tem uma`) muda com ela. E isso não surpreende ninguém porque o
  portador do estado é o **rótulo do confirmar**, pela regra da caixa acima: sem a marca, o botão
  volta a dizer `Marcar 5 lançamentos` em vez de `Marcar 5 e trocar 1`. Quem lê o botão que vai
  clicar vê a mudança de escopo.

### (g) O que muda em `/lancamentos` — a faixa do mês (spec 0006 §3.5.2)

`Entrou`, `Saiu` e `Resultado` deixam de somar os marcados. Sem explicação, o mês encolheria
sozinho — mentira por omissão. A explicação **não** entra como um quarto item depois de
`Resultado`: ali ela seria exatamente o remendo que aparenta ser, e disputaria o fim da frase com
o único número da faixa que leva tom e sinal.

Ela entra como **segunda linha da mesma faixa**, subordinada, com o motivo à frente dos números:

```
Entrou 5.300,00 . Saiu 3.100,00 . Resultado +2.200,00
Fora destes numeros: 2.000,00 em aportes . 850,00 em resgates
```

- `.resumoFora`: mesma família do `.resumo` (`--text-13`, `--ink-muted`, `flex-wrap`,
  `gap: var(--space-2)`), com `margin-block-start: var(--space-1)`. Valores em `MoneyText` neutro,
  `plain`, **sem sinal e sem tom** — são os mesmos números da tela de investimentos, e lá eles são
  silenciosos.
- **Existe só quando `investedCents + redeemedCents > 0`.** Com um dos dois em zero, só o que
  existe é citado (`Fora destes números: 2.000,00 em aportes`).
- Sem link: `Investimentos` está no menu, a dois passos. Faixa não é lugar de atalho de navegação.
- Abaixo de 40rem os dois itens ocupam a linha inteira (`flex-basis: 100%`) e o `·` some — a regra
  do separador órfão, que já vale para o resto do app.

A **lista** de `/lancamentos` não muda: o lançamento marcado continua lá, com a sua categoria, e
continua descontado do saldo da conta. `/relatorios/categorias` simplesmente deixa de vê-lo — e a
tabela continua fechando em `100,00%`, porque o servidor recalcula o todo sem ele.

### (h) `CoinsIcon`

`components/icons/CoinsIcon.tsx`, contrato de `types.ts`, traço 1.5, `currentColor`, viewBox 24,
`strokeLinecap`/`strokeLinejoin` `round`, massa óptica **4,75 a 19,25** nos dois eixos — a mesma
de `TransfersIcon` e `ChartIcon`.

**Três moedas empilhadas, vistas de lado**: a face da moeda de cima é uma elipse inteira, as
laterais descem retas e dois arcos marcam a borda da moeda do meio e a da de baixo. É o desenho de
**dinheiro posto de lado, uma parcela de cada vez** — que é o assunto da tela: fluxo, não posição.
Recusados: seta subindo e barras crescentes (prometem rentabilidade, que a spec tira do escopo, e
são o ícone mais genérico que existe), cofrinho (é poupança, e some a 20 px), planta brotando
(metáfora de startup) e pote com nível de conteúdo (prometeria "quanto eu tenho").

```
<ellipse cx="12" cy="7.75" rx="7.25" ry="3" />
<path d="M4.75 7.75v8.5a7.25 3 0 0 0 14.5 0v-8.5" />
<path d="M4.75 12a7.25 3 0 0 0 14.5 0" />
```

### (i) Casca — o 7º item e o piso da barra inferior

A navegação vai a **sete** itens. A emenda de 17/09/2026 continua valendo inteira; só o piso muda,
agora com a fórmula explícita, para o 8º item não precisar de arqueologia:

> **piso só-ícone = N × 47 px + (N − 1) × 2 px**, escrito em `rem` e medido no **content box** da
> barra (é o que a consulta de container mede).

Com N = 7: 329 + 12 = **341 px = `@container (max-width: 21.3125rem)`** (era `18.25rem` para
N = 6). Consequências medidas: a 375 px o content box tem 359 px e a célula fica com 49,6 px —
**os rótulos continuam visíveis**; a 360 px, 47,4 px, passa raspando; a **320 px** a célula cai a
41,7 px e a barra vira **só-ícone**, com o nome no `aria-label` e `padding-block: var(--space-3)`
mantendo a altura do alvo. Largura de 41,7 px continua muito acima do mínimo de 24 px da WCAG
2.5.8, e **`--nav-bar-h` não muda** (o modo só-ícone é mais baixo, e reservar a mais é seguro).

`Investimentos` cabe: a maior sílaba (`ves`/`men`/`tos`) mede ~26 px a `--text-12`, bem abaixo dos
47 px. Hífen suave em todas as fronteiras: `In\u00ADves\u00ADti\u00ADmen\u00ADtos`.

**Aviso para a próxima entrega:** com N = 8 o piso vai a 390 px (24,375 rem) e **375 px perde os
rótulos**. Investimentos é o último item que cabe com rótulo num celular comum — o 8º exige
sub-navegação (o caminho que o ADR-027e já reservou para Relatórios), não mais uma célula.

### (j) Acessibilidade

- **A tabela é a fonte; o gráfico é ilustração** (ADR-021). O `<svg>` é `aria-hidden="true"` e
  `focusable="false"`, com `<title>` nativo por barra como cortesia para quem usa mouse — sem
  tooltip próprio, sem foco em barra, sem navegação por setas no desenho. O caminho de teclado é a
  tabela de 12 meses, que tem `caption` e os mesmos 24 números.
- O **eixo** é `aria-hidden`: doze nomes de mês sem valor nenhum são ruído no áudio, e o mês por
  extenso está em cada linha da tabela. A **legenda não é** `aria-hidden` — ela nomeia as duas
  séries em duas palavras, e é o que liga a figura à tabela para quem enxerga.
- Foco vai para o `<h1>` na troca de rota (`tabIndex={-1}`, anel só em `:focus-visible`).
- **Live regions**: só duas na tela, e nunca simultâneas — a da `DataTable loading` e o
  `<p class="sr-only" role="status">` da paginação. A faixa de números **não** é live region: ela
  muda junto com a rota e com o mês, e anunciar quatro valores a cada seta seria tagarelice.
- **Contraste**: todo texto em `--ink`/`--ink-muted` sobre `--surface` (≥ 4,5:1); as duas marcas do
  gráfico ≥ 3:1 (15,65:1 e 4,22:1 claro; 13,71:1 e 4,71:1 escuro), sem necessidade de *relief*;
  `prefers-contrast: more` aperta `--chart-3` sozinho, pelos tokens.
- `prefers-reduced-motion`: sem transição no hover da barra e sem a transição de opacidade do
  refetch. `forced-colors`: em (c).
- Alvos de toque: o único controle da tela é `Button` (altura `--control-h`); no diálogo, o
  checkbox nativo dentro de um `<label>` clicável inteiro.

### (k) Copy pt-BR — tabela única

| Onde | Texto |
|---|---|
| Navegação | `Investimentos` |
| `document.title` | `Investimentos · HomeFinance` |
| `<h1>` | `Investimentos` |
| Apoio | `O que saiu para investir e o que voltou em setembro.` |
| Ação do topo | `Detectar investimentos` |
| Títulos das colunas de números | `Em setembro` · `No ano, até setembro` |
| Linhas dos números | `Aportes` · `Resgates` (sufixo `sr-only` ` em setembro` / ` no ano, até setembro`) |
| Contagem | `3 lançamentos` / `1 lançamento` / `nenhum lançamento` |
| Título da seção do gráfico | `Últimos 12 meses, até setembro` |
| `caption` da tabela de meses | `Aportes e resgates mês a mês, de outubro de 2025 a setembro de 2026` |
| Colunas da tabela de meses | `Mês` · `Aportes` · `Resgates` |
| Célula de mês | `setembro de 2026` (por extenso; abreviação só no eixo) |
| Legenda do gráfico | `Aportes` · `Resgates` |
| `<figcaption>` | `Aportes e resgates dos últimos 12 meses — a barra mais alta é R$ 2.400,00. Os valores mês a mês estão na tabela.` |
| `<title>` da barra | `setembro de 2026 · Aportes · R$ 2.000,00` |
| Eixo do gráfico | `out` `nov` `dez` `jan` … `set` |
| Título da seção da lista | `Lançamentos de setembro` |
| `caption` da lista | `Aportes e resgates de setembro de 2026` |
| Colunas da lista | `Data` · `Movimento` · `Conta` · `Descrição` · `Categoria` · `Valor` |
| Movimento | `Aporte` · `Resgate` |
| Secundária no celular | `Aporte · Nubank · CDB` |
| Rodapé | `Mostrando 50 de 63 lançamentos` / `63 lançamentos — é tudo o que existe no mês.` · `Carregar mais 13` |
| Anúncio de paginação (`sr-only`) | `Mais 13 lançamentos carregados. 63 de 63.` |
| Vazio sem categoria | `Nenhuma categoria de investimento ainda.` — `Esta tela mostra o que sai da conta para investir e o que volta em resgates. Crie em Categorias um grupo de natureza Investimentos — CDB, Tesouro, previdência — e os lançamentos passam a aparecer aqui.` — `Ir para categorias` |
| Vazio do mês | `Nenhum aporte ou resgate em setembro de 2026.` — `Um lançamento aparece aqui quando recebe uma categoria de investimento ou de resgate. Se o extrato do mês já foi importado, detecte pelas palavras-chave.` — `Detectar investimentos` · `Ver os lançamentos de setembro` |
| Erro da tela | `Não foi possível carregar os investimentos.` — `Tentar de novo` |
| Erro ao paginar | `Não foi possível carregar mais lançamentos.` — `Tentar de novo` |
| Diálogo — título / descrição | `Detectar investimentos` / `Setembro de 2026 · aplica as palavras-chave das categorias de investimento e de resgate aos lançamentos sem categoria. O que já tem categoria não muda.` |
| Diálogo — carregando | `Conferindo as palavras-chave de setembro…` |
| Diálogo — resumo | `5 lançamentos viram aporte ou resgate.` / `1 lançamento vira aporte ou resgate.` |
| Diálogo — grupos | `Viram aporte ou resgate · 5` · `Já têm categoria · 1` — `Estes não mudam, a não ser que você peça. A categoria atual fica no lugar.` · `Continuam sem categoria · 2` — `Abaixo de 80% de semelhança o app não arrisca; num empate entre duas categorias, também não. E quando a palavra que bate é de uma categoria de despesa ou de receita, esta detecção não mexe — para essa, use Categorizar automaticamente em Lançamentos.` |
| Diálogo — motivos (os três do contrato) | `below_threshold` → `abaixo de 80%` · `ambiguous` → `empate entre categorias` · `other_category` → `bateu com outra categoria` |
| Diálogo — troca de categoria | `Outros → CDB` (`sr-only` `de Outros para CDB`) |
| Diálogo — caixa de troca | `Trocar também a categoria de 1 lançamento que já tem uma` / `de 3 lançamentos que já têm uma` |
| Diálogo — confirmar | `Marcar 5 lançamentos` · `Marcar 1 lançamento` · `Marcar 5 e trocar 1` · `Trocar 3 lançamentos` · `Nada a marcar` |
| Diálogo — corte em 500 | `Mostrando as primeiras 500.` |
| Diálogo — vazio | `Nenhum lançamento receberia categoria de investimento.` — `As palavras-chave das suas categorias de investimento e de resgate não batem com as descrições dos lançamentos sem categoria de setembro. Cadastre na categoria o nome como ele aparece no extrato — «cdb», «tesouro», «resgate cdb».` — `Ir para categorias` — `Ver os 2 lançamentos e o motivo` |
| Diálogo — toast | `5 lançamentos marcados como aporte ou resgate.` / `1 lançamento marcado como aporte ou resgate.` / `Nada mudou — nenhuma descrição bateu com as palavras-chave.` |
| Diálogo — erro da prévia | `Não foi possível conferir as palavras-chave.` — `Tentar de novo` |
| Diálogo — erro ao gravar | `Não foi possível marcar.` |
| Diálogo — teto de 10.000 | `Este mês tem lançamentos demais para detectar de uma vez.` — `O limite é 10.000 por execução e nada foi alterado. Use o atalho de categoria em Lançamentos.` |
| Diálogo — conflito (409) | `As categorias mudaram enquanto a prévia estava aberta.` — `Nada foi marcado. A detecção só marca em categoria que pode receber lançamento, e uma das categorias desta prévia deixou de poder.` — `Conferir de novo` ⟵ **proposta (18/09/2026), aguarda ratificação do `designer-ui`** |
| Diálogo — caixa de troca depois de refazer a prévia | volta **desmarcada**, sempre (ver (f)) |
| Faixa de `/lancamentos` | `Fora destes números: 2.000,00 em aportes · 850,00 em resgates` |

> **Proposta em revisão — conflito (409), achado N4 da revisão de segurança da E7 (18/09/2026).**
> O título (`As categorias mudaram enquanto a prévia estava aberta.`), a abertura (`Nada foi
> marcado.`) e o rótulo da ação (`Conferir de novo`) **não mudam** — são copy ratificada. O que
> muda é a **frase da causa**, que passa a ser agnóstica: o servidor reconfere **cinco**
> qualificadores (casa · viva · não arquivada · sem subcategoria ativa · natureza compatível com o
> lote) e o 409 não diz qual faltou, mas a copy anterior nomeava dois deles — quem levasse 409 por
> arquivamento ou por troca de natureza lia uma causa que não aconteceu. Continua valendo:
> **nenhum id e nenhum nome de categoria** (deliberado, ver (f).3), e o texto **não** repete
> "confira a prévia de novo" porque o botão está dentro do próprio `Alert`. A decisão de copy é do
> `designer-ui`; está implementada em `DetectInvestmentsDialog.tsx` e fixada em teste enquanto
> aguarda ratificação.

Léxico: mês por extenso e em minúsculas (`setembro`); palavra-chave sempre entre aspas angulares;
pontuação é texto em `tabular-nums` e 100 nunca aparece; nada de "carteira", "patrimônio",
"rentabilidade", "posição", "meta", "acumulado", "visão geral" ou "seus investimentos crescendo".

### (l) `/categorias` com as quatro naturezas (T4a, 18/09/2026)

A tela de categorias não ganha estrutura nova: é o mesmo bloco, o mesmo diálogo e o mesmo
`KeywordsField`, quatro vezes (spec 0006 §3.1). O que ela ganha é **vocabulário** — e é aqui que a
casa aprende, de uma vez, o que a E7 inteira significa. Copy ratificada; o que segue é normativo.

**Quatro painéis em 2×2, e o eixo das colunas é o lado do dinheiro.** `NATUREZAS` na ordem
`expense, income, investment, redemption` sobre `grid-template-columns: repeat(auto-fit,
minmax(min(24rem, 100%), 1fr))` cai em:

```
Despesas        Receitas
Investimentos   Resgates
```

Coluna da esquerda = o que **sai** da conta; coluna da direita = o que **entra**. Linha de cima =
o dia a dia; linha de baixo = o que a casa guarda. É leitura, não acaso: os dois eixos existem e
devem ser preservados. O `max-inline-size: 68rem` da página é o que **garante** o 2×2 — com 24rem
de mínimo, três colunas nunca cabem, e uma terceira coluna quebraria o eixo. Empilhado (uma
coluna) a ordem do DOM mantém os pares vizinhos: Despesas, Receitas, Investimentos, Resgates.

**Cada painel ganha `subtitle`** — quatro linhas curtas, em construção paralela, definindo as duas
naturezas novas **por contraste** com as duas que a pessoa já conhece. É o lugar exato da dúvida,
e ensina o modelo mental inteiro da E7 sem uma linha de prosa a mais na tela:

| Painel | `subtitle` |
|---|---|
| `Despesas` | `Sai da conta e é gasto.` |
| `Receitas` | `Entra na conta e é ganho.` |
| `Investimentos` | `Sai da conta, mas não é gasto.` |
| `Resgates` | `Entra na conta, mas não é ganho.` |

O apoio do `<h1>` **não muda** (continua falando de dois níveis, arquivar e excluir): a lição das
naturezas mora junto de cada bloco, não empilhada no cabeçalho.

**Seletor de natureza**: rótulo **`Natureza`** (era "Receita ou despesa" na E2c (a) — a linha lá
já foi corrigida), opções `Despesa · Receita · Investimento · Resgate`, **sem placeholder** (sempre
há valor, como o filtro de `/relatorios/categorias`). Só aparece em grupo — `novo-grupo` e
`editar` de grupo —, nunca em folha, que herda (ADR-017b). `hint`: `As subcategorias deste grupo
herdam esta escolha.` ao criar, `As subcategorias do grupo acompanham a troca.` ao editar.

O `Natureza` de `/relatorios/categorias` **não** ganha `Investimento` nem `Resgate`, e isso não é um
esquecimento: investimento não é um `kind` a mais do relatório, é a tela própria (spec 0006 §4).
Ninguém "conserta" aquele filtro acrescentando essas duas opções.

*Emenda de 18/09/2026 (ADR-032):* o seletor passou a ter **quatro** opções — `Despesas` ·
`Despesas no crédito` · `Despesas no débito` · `Receitas` — e a nota acima continua valendo
inteira. As duas que entraram **não são naturezas**: são recortes de conta da MESMA natureza
`expense` (cartão de crédito de um lado, todas as outras contas do outro; crédito + débito =
`Despesas`), e é exatamente isso que as autoriza onde investimento/resgate seguem proibidos. A URL
guarda a palavra (`?natureza=despesas-credito` / `?natureza=despesas-debito`) e a tela a traduz para
`{kind: expense, accountGroup: credit|debit}` — `accountGroup` nunca viaja pela URL. O rótulo
continua `Natureza` porque é como o usuário chama esse filtro, e não porque as quatro sejam
naturezas. Copy completa na tabela E6a (i).

**Aviso da troca de natureza** (ADR-029c) — `Alert tone="warning"`, **abaixo** do `Select` (a ordem
de leitura é escolha → consequência), exibido só quando a natureza escolhida difere da atual **e**
fica do mesmo lado do dinheiro:

- título: `Isto muda os totais de meses já fechados`
- corpo: `Os lançamentos de {Nome} continuam na lista e no saldo da conta, mas {efeito} — em todos
  os meses, não só neste.` + (com filhas) ` As subcategorias, inclusive as arquivadas, mudam
  junto.` + ` Dá para voltar atrás pelo mesmo caminho.`
- `{efeito}`, pela natureza de **destino**: `investment` → `saem dos totais de despesa e passam a
  contar como aportes` · `redemption` → `saem dos totais de receita e passam a contar como
  resgates` · `expense` → `voltam para os totais de despesa e deixam de contar como aportes` ·
  `income` → `voltam para os totais de receita e deixam de contar como resgates`.

Três decisões dentro dessas frases: (1) **"continuam na lista e no saldo da conta"** é literal e
vem primeiro, porque o medo real é "meus lançamentos vão sumir" — e a spec garante que o saldo não
muda; (2) **"em todos os meses, não só neste"** é o preço declarado do ADR-029c, e é o que não pode
faltar; (3) a palavra é **aporte**, nunca "investimento", conforme o léxico (a).

**Cruzar o lado do dinheiro não ganha aviso preditivo.** Quem sabe se há lançamento pendurado é o
servidor; adivinhar aqui acertaria às vezes. A recusa chega como 422 em `fields.kind` e cai no
próprio campo, com a frase que ensina a regra nova: `Não dá para trocar entre receita e despesa
numa categoria em uso ou com subcategorias. Entre despesa e investimento, ou entre receita e
resgate, a troca vale.`

**`Alert` ganha uma prop: `live?: "assertive" | "polite"`.** Hoje o componente deriva o papel do
tom (`error`/`warning` → `role="alert"`, resto → `role="status"`), e para este aviso isso está
errado: ele monta **enquanto a pessoa ainda está no `<select>`**, e no Windows a seta já troca o
valor de um select fechado — um `role="alert"` interrompe o anúncio da opção recém-escolhida para
ler a consequência. A distinção certa não é o tom, é o **momento**:

> **Resultado de uma ação já feita é `assertive`. Prévia de uma ação ainda não feita é `polite`.**

É a mesma decisão da dica de consequência do atalho de categorização (E2c (h.3), que é
`role="status"`). O padrão da prop preserva o comportamento atual de todos os `Alert` existentes;
este aviso passa `live="polite"`. Nenhum outro uso muda.

**Notas de herança e vazio do bloco** (ratificadas como o dev as escreveu, com um ajuste):

| Onde | Texto |
|---|---|
| Nova subcategoria | `Vai ficar dentro de **Moradia** e herda do grupo a natureza **despesa**.` |
| Editar folha | `Subcategoria acompanha o grupo: troque a natureza no grupo, e as filhas vão junto.` |
| Bloco sem grupos | `Nenhum grupo de despesa ainda.` / `de receita` / `de investimento` / `de resgate` |

A frase da subcategoria estava "será uma **despesa**, como o grupo" e, com quatro naturezas, saía
"será uma **investimento**". A correção do dev — dizer **"a natureza X"** em vez de concordar com
o artigo — é a certa e fica: nome de natureza nunca entra numa frase que exija gênero. O único
ajuste é na frase da folha: `a natureza se troca no grupo` virou **`troque a natureza no grupo`** —
quem abre o diálogo da folha está procurando o campo que não está lá, e o que ele precisa é de uma
instrução, não de uma descrição.

**`Select`: agrupar por trechos consecutivos, não por nome** (correção de componente base).
`agrupar()` hoje monta um `Map` por rótulo de grupo e despeja as opções **sem** grupo antes de
todos os `<optgroup>`. Com as quatro naturezas isso virou mentira visível: `opcoesDeCategoria`
oferece as duas naturezas do lado do dinheiro e emite **grupo sem subcategoria como opção solta**
— então a casa que tem o grupo "Investimentos" da semente, ainda sem filhas, vê **"Investimentos"
no topo do seletor de despesa**, acima de "Alimentação › Mercado", sem cabeçalho e sem nada que
diga que aquilo é investimento. O próprio comentário do componente promete o contrário
("preserva a ordem em que os grupos apareceram").

Correção, sem mudar a API nem o tipo `SelectOption`: percorrer `options` na ordem e **abrir um
balde novo toda vez que `group` muda em relação à opção anterior** (inclusive de indefinido para
definido e vice-versa); renderizar os baldes na ordem — balde sem `group` vira `<option>` solta
**no lugar onde está**, balde com `group` vira um `<optgroup>`. Ganhos: a ordem passa a ser
exatamente a da entrada, a opção solta para de saltar para o topo, e dois grupos de mesmo nome
**não adjacentes** produzem dois `<optgroup>` em vez de um. Testes a acrescentar: opção solta entre
dois grupos continua entre eles; dois trechos com o mesmo rótulo produzem dois `<optgroup>`.

**Limite residual, declarado:** dois grupos de mesmo nome **adjacentes** ainda se fundiriam. Hoje
isso é inalcançável, e o motivo está no backend: `NameTaken(household, parentId, norm)` **não olha
a natureza**, então dois grupos ativos de mesmo nome não coexistem em nenhuma natureza; e os dois
chamadores de `opcoesDeCategoria` usam `categoriasQueryOptions(false)`, sem arquivadas. Se algum
dia a unicidade passar a considerar `kind`, ou um seletor passar a receber a árvore com
arquivadas, o balde precisa de chave própria (`groupKey`) — e não do rótulo.

### Checklist anti-cara-de-IA — E7 (aplicar com a tela pronta)

1. `filter: grayscale(1)` na tela inteira: aporte e resgate continuam distinguíveis em **toda**
   aparição — números, gráfico, tabela de meses, lista e celular. Se alguma depender da tinta,
   reprova.
2. Nenhum valor da tela usa `--income`, `--expense` ou sinal `+/−`; nenhum usa `--accent`.
   Cromaticamente silenciosa, como `/transferencias`.
3. As barras usam **só** `--chart-1` e `--chart-3`: nenhuma matiz, nenhum gradiente, nenhuma
   sombra, nenhum 3D, nenhum canto arredondado.
4. Barras **agrupadas**: nada empilhado, nada divergente, nenhuma soma de aporte com resgate em
   lugar nenhum da tela.
5. **Nenhuma animação de entrada** no gráfico; o único feedback é `opacity: 0.82` no hover, e ele
   some em `prefers-reduced-motion`.
6. Nada escrito dentro ou sobre as barras; sem eixo Y, sem malha, sem linha-guia. A escala está em
   palavras no `<figcaption>` e os 24 números estão na tabela ao lado.
7. Os rótulos do eixo são **HTML**, em `--text-13`, alinhados por grade de 12 colunas, e o bloco da
   plotagem tem `aspect-ratio: 2 / 1`, igual ao `viewBox`; abaixo de 24rem o eixo alterna **a partir
   do fim**, e o mês selecionado — a última coluna — nunca perde o rótulo.
8. Mês zerado: **nenhuma barra** e a base contínua. Série inteiramente zerada: **a seção não
   existe** — nem moldura vazia, nem doze linhas de `0,00`.
9. Os números são uma `<dl>` de duas colunas com valores tabulares à direita — **não** são quatro
   cartões com número gigante, ícone e percentual de variação.
10. Zero número derivado **nesta tela**: sem líquido, sem saldo investido, sem "% do mês anterior",
    sem seta de tendência, sem projeção. (O painel `/` é a exceção declarada — lá o investimento do
    mês é um líquido com sinal, por decisão de 18/09/2026.)
11. A lista é `<table>` plana, sem grupo de dia e sem subtotal; `Movimento` é palavra em `--ink`,
    nunca `Badge`, nunca bolinha, nunca ícone sozinho; a página é 50, como nas outras
    listas do app.
12. Abaixo de 40rem, `Movimento`, `Conta` e `Categoria` reaparecem na `.secundaria`, com `Aporte`
    em `--ink`. Nenhum dado some, e a página não rola na horizontal a 375 px.
13. Estados desenhados: vazio sem categoria (com saída para `/categorias`), vazio do mês (com
    detectar), carregando (eixo real + platô em `--surface-sunken`), erro (com `Tentar de novo`);
    refetch mantém o quadro a 0,6 de opacidade, sem skeleton. O 409 tem ação PRÓPRIA
    (`Conferir de novo`, nunca `Tentar de novo`) e a caixa de sobrescrever volta desmarcada.
14. `forced-colors`: série 1 sólida, série 3 vazada com contorno `CanvasText`, base e virada de ano
    visíveis — as duas séries continuam distintas sem cor nenhuma.
15. Nenhum `disabled`: o confirmar do diálogo usa `aria-disabled` com rótulo que explica, e o botão
    do topo **some** quando não há categoria, em vez de aparecer apagado.
16. `CoinsIcon` com traço 1.5, `currentColor`, três elementos e massa óptica 4,75–19,25 — sem seta
    de crescimento, sem cofrinho, sem broto.
17. A faixa de `/lancamentos` ganha **segunda linha** com o motivo à frente ("Fora destes
    números:"), e não um quarto item depois de `Resultado`.
18. Copy da tabela (k), palavra por palavra: `aporte` em toda a tela, `investimento` só como nome
    da seção, nenhum adjetivo de marketing e nenhum emoji.
19. Nenhuma medida, cor ou raio fora dos tokens em `BarChart.module.css`, `Swatch.module.css`,
    `InvestmentsScreen.module.css` e `DetectInvestmentsDialog.module.css`.
20. Foco no `<h1>` na troca de rota; `<dialog>` nativo, checkbox nativo, nenhum controle
    reimplementado.

**Checklist da tela de categorias** (soma-se ao da E7):

21. Os quatro painéis caem em 2×2 com o que **sai** à esquerda; a página continua em `68rem`, que
    é o que impede uma terceira coluna.
22. Os quatro `subtitle` existem e são os da tabela — as duas naturezas novas se definem por
    contraste com as duas antigas.
23. O aviso da troca é `polite`, mora abaixo do `Select`, diz "em todos os meses" e usa a palavra
    **aporte**; cruzar o lado do dinheiro não tem aviso preditivo nenhum.
24. Nenhuma frase da tela concorda em gênero com o nome de uma natureza ("uma investimento" é o
    defeito que a T4a corrigiu).
25. Nenhum grupo sem subcategoria aparece no topo de um seletor de categoria, fora do seu lugar.

## E2d — o filtro de tipo em `/lancamentos` (spec 0004 §1.6, 18/09/2026)

Pedido do usuário: *"filtros melhores para ver somente despesas"*. Uma escolha única entre
**Tudo · Receitas · Despesas · Transferências · Investimentos**, resolvida no servidor
(`?kindGroup=` na API, `?tipo=` na URL da tela). Estende E2, E2c, E6a e E7 sem revogar nada:
`grayscale(1)` continua sendo o aceite, a cor cromática continua com três donos, e aporte e resgate
continuam fora de Despesas/Receitas — o filtro só **mostra** a partição que a faixa do mês já fazia
(spec 0006 §3.5.2).

**Nenhum token novo, nenhum componente novo, nenhuma dependência nova.** Quatro comportamentos
mudam — a faixa do mês, o subtotal do dia, a faixa de pendência e o toast do atalho de categoria —,
e é por isso que esta seção existe: sem eles o filtro seria tecnicamente verdadeiro e visualmente
mentiroso.

### (a) O controle — um `<select>` a mais na faixa, e só

`Select density="compact" label="Tipo"`, na `.faixa` do `Panel`, **à direita de `Conta`**, com
`placeholder="Tudo"` (valor vazio = a chave ausente na URL, exatamente como `Todas as contas`) e as
opções na ordem `Receitas · Despesas · Transferências · Investimentos`. **Confirmado**, com três
razões e uma recusa escrita:

1. É o mesmo par "rótulo visível + `<select>` nativo" que a faixa já tem, que
   `/relatorios/categorias` usa para `natureza` e que `/transferencias` usa duas vezes. Um filtro
   novo que parece o filtro antigo é o que faz a barra ler como **uma** barra.
2. Zero componente novo: no celular o `<select>` abre a roda do sistema; no teclado, digitar `t`
   salta para Transferências. Um *segmented control* de cinco opções custaria um componente com
   `role="radiogroup"`, tabindex rodante e navegação por setas (APG), ~340 px numa faixa que já tem
   dois blocos, e duas linhas a 375 px — **não se paga**, e ainda inventaria uma forma de controle
   que este app não tem em lugar nenhum.
3. `Tudo` como **placeholder**, e não como opção explícita, mantém a URL canônica sem a chave: um
   link colado sem `?tipo=` é o estado padrão, não um estado a mais para validar.

**Ordem das opções** — `Receitas` antes de `Despesas`, apesar de `/categorias` abrir com Despesas:
aqui a faixa logo ao lado lê `Entrou · Saiu`, e o seletor segue a ordem da frase que ele filtra.

**Faixa com dois filtros**: os dois campos vão num `<div class="filtros">` — `display: flex;
flex-wrap: wrap; align-items: flex-end; gap: var(--space-4)` (abaixo de 40 rem, `gap:
var(--space-2)`) — que passa a ser o primeiro filho da `.faixa`, no lugar do `Select` solto. A
`.faixa` continua `space-between`: filtros à esquerda, números à direita. Nenhum estilo entra no
componente de fora (regra do design system); os dois campos continuam com largura por conteúdo e
**embrulham** quando não cabem — a 375 px eles empilham, cada um com a sua largura, sem esticar.

**URL e cache** (é trabalho do `dev-frontend-react`, mas é decisão de interface):

- `BuscaDoApp` ganha `tipo?: Tipo`, com `Tipo = 'receitas' | 'despesas' | 'transferencias' |
  'investimentos'` — allowlist em pt-BR, como `natureza`. A tradução para o `kindGroup` da API é da
  tela, **nunca** da URL. Fora da lista, a chave some e a tela abre em Tudo.
- **`semCategoria` é descartado quando `tipo` é `transferencias` ou `investimentos`** — em
  `validarBusca` **e** em `aplicarNaBusca`, a mesma mecânica que já descarta `contraparte` sem
  `conta`. A combinação não tem resultado possível (transferência não tem categoria por desenho;
  aporte e resgate têm por definição), e uma URL colada não pode virar lista vazia sem saída.
- O `tipo` entra na **chave da query** de `transactions` — o filtro é do servidor, e sem ele o cache
  serviria as linhas do filtro anterior — e na `chaveDaBusca` que fecha o editor de categoria aberto
  (a regra "trocar mês, conta ou filtro fecha" já existe).

### (b) A faixa `Entrou · Saiu · Resultado` sob filtro — o ponto que justifica a tarefa

Manter os três com zeros é tecnicamente verdadeiro e **visualmente mentiroso**: com
`tipo=transferencias`, `Entrou 0,00 · Saiu 0,00 · Resultado 0,00` diz "nada aconteceu" num mês que
moveu R$ 8.000. A regra que governa as cinco opções é uma só: **a faixa mostra o número que o
filtro sabe responder e cala o que ele não sabe** — número que nunca varia é ruído, e número
repetido é pior do que número ausente.

| `tipo` | 1ª linha (`.resumo`) | 2ª linha (`.resumoFora`) |
|---|---|---|
| ausente (**Tudo**) | `Entrou 5.300,00 · Saiu 3.100,00 · Resultado +2.200,00` | `Fora destes números: 2.000,00 em aportes · 850,00 em resgates` (como hoje) |
| `receitas` | `Entrou 5.300,00` | `Fora destes números: 850,00 em resgates` |
| `despesas` | `Saiu 3.100,00` | `Fora destes números: 2.000,00 em aportes` |
| `transferencias` | `Transferência não é receita nem despesa — o dinheiro só mudou de conta dentro da casa.` | — |
| `investimentos` | `Aportes 2.000,00 · Resgates 850,00` | — |

Os porquês, um a um:

- **`receitas` / `despesas` mostram um número só.** Com o filtro, `expenseCents` é 0 sob receitas e
  `incomeCents` é 0 sob despesas: exibi-los seria escrever um zero que **nunca** muda. E `Resultado`
  seria `Entrou` (ou `−Saiu`) outra vez, com tom e sinal — o único número da faixa que carrega tom
  passaria a repetir o vizinho, como se um mês só de receitas tivesse "resultado" igual à receita.
  Detalhe verificável: `Entrou` sob `receitas` é **idêntico** ao `Entrou` de Tudo (o `incomeCents`
  do contrato já exclui resgates desde o ADR-029e) — merece teste, porque um número que mudasse ao
  filtrar denunciaria dupla contagem.
- **A 2ª linha sobrevive ao filtro, citando só o lado do dinheiro que o filtro nomeia.** Aporte é
  dinheiro que **saiu da conta** e não está em `Saiu`; resgate é dinheiro que **entrou** e não está
  em `Entrou`. Quem chega por link direto em `?tipo=despesas` nunca veria a linha de Tudo, e leria
  `Saiu 3.100,00` como tudo o que saiu. A exigência da spec 0006 §3.5.2 continua valendo inteira:
  **o total não pode encolher sem explicação**. Sob `receitas` cita-se só resgates; sob `despesas`,
  só aportes — citar o outro lado seria explicar uma omissão que não houve. A regra de zero da
  E7 (g) continua: a linha existe só quando o número citado é maior que zero. Depende de **C1**.
- **`transferencias` troca números por uma frase.** Não existe um "total movido" honesto: cada
  transferência tem **duas pernas** na lista (somar as duas conta o dinheiro duas vezes) e, com o
  filtro de conta ligado, só uma delas aparece — um número cujo significado muda conforme outro
  filtro é pior do que nenhum número. A contagem já está no rodapé (`8 lançamentos — é tudo o que
  existe no filtro`). O que a faixa precisa dizer é **por que** não há totais, e a frase é a que o
  app já usa desde a E2c ("o dinheiro só mudou de conta"). Sem link para `/transferencias`:
  Transferências está no menu, a dois passos, e faixa não é lugar de atalho de navegação (E7 (g)).
- **`investimentos` usa as palavras da E7**, não `Aportado`/`Resgatado`: a E7 (a) proíbe conjugar
  "aportar" em qualquer tela, e `Aportes`/`Resgates` são os rótulos já ratificados dos mesmos
  números em `/investimentos`. Valores `MoneyText` **neutros, `plain`, sem sinal e sem tom** — lá
  eles são silenciosos, e aqui são os mesmos números. **Os dois aparecem sempre, inclusive `0,00`**:
  nesta faixa a ausência de resgate responde a uma pergunta que a pessoa está fazendo, ao contrário
  do zero na 2ª linha de Tudo, onde seria ruído sobre um assunto que não é o dela. **Sem líquido
  aqui**: o líquido do mês existe, e é do **painel** — lição de 18/09/2026, "quanto ficou
  investido neste mês" —, vem pronto do servidor e responde a outra pergunta. Esta faixa é o
  extrato do mês: os dois números são os mesmos de `/investimentos`, e lá vale "sem saldo, sem
  líquido" (spec 0006 §3.4.2). O contrato de `/transactions` também não entrega líquido nenhum.

**Carregando**: o número de `Skeleton width="4.5rem" height="1rem"` acompanha o que vai aparecer —
três em Tudo, **um** em receitas/despesas, **dois** em investimentos e **nenhum** em
transferências, onde a frase não depende de dado nenhum e entra já na primeira renderização.

### (c) O subtotal do dia

`agruparPorDia` exclui transferência do subtotal por desenho. Com `tipo=transferencias`, todo dia
mostraria `R$ 0,00`; com `tipo=investimentos`, o subtotal do dia seria "aportes menos resgates do
dia" — e sairia com tom e sinal, pintando o aporte de vermelho, exatamente o erro que a E7 existe
para corrigir. O líquido do **mês** existe e é o número do painel (lição de 18/09/2026); o líquido
de **um dia** não responde a pergunta nenhuma.

**Regra, uma para as duas**: o subtotal do dia responde *"quanto este dia mudou o patrimônio da
casa"*. Transferência e investimento **não mudam o patrimônio** — logo, com `tipo=transferencias`
ou `tipo=investimentos` o cabeçalho do dia **não tem subtotal**: o slot `trailing` do `RowGroup` é
omitido (nada de `0,00`, nada de contagem no lugar do dinheiro, nada de `—`), e o cabeçalho fica só
com a data por extenso. Um zero repetido em doze dias não é um total: é uma resposta falsa a uma
pergunta que este filtro não faz.

Consequência limpa: **o sufixo `· 1 transferência` do rótulo do dia só existe em Tudo.** Sob
`transferencias` não há subtotal para explicar (e o sufixo viraria rótulo repetido em todo
cabeçalho); sob `receitas`, `despesas` e `investimentos` não há linha de transferência na lista.

Em `receitas` e `despesas` o subtotal **fica** — todas as linhas do dia entram nele, e ele continua
`MoneyText tone="semantic" sign="always"`, com o `sr-only "Subtotal do dia "`.

> **Dívida registrada, fora do escopo desta emenda:** em **Tudo**, o subtotal do dia ainda soma os
> aportes (eles são `expense`), enquanto a faixa do mês os exclui — a soma dos subtotais não fecha
> com `Resultado`. É anterior a esta tarefa (a E7 mexeu na faixa, não no subtotal). Quem for
> resolver decide entre tirar o aporte do subtotal do dia (explicando no cabeçalho do dia, como se
> faz com a transferência) ou assumir a diferença; o `designer-ui` escreve antes de o código mudar.

### (d) A faixa de pendência precisa nomear o filtro

Sob filtro, `uncategorizedCount` conta o **filtro** (decisão do `arquiteto`, e é o que já acontece
com o filtro de conta — duas fontes para o mesmo número divergem). Então a frase tem de nomear o
que contou: `3 lançamentos de setembro estão sem categoria`, numa tela que mostra só receitas, é
mentira sobre o mês.

| `tipo` | Frase (plural / singular) |
|---|---|
| Tudo | `12 lançamentos de setembro estão sem categoria. Eles não entram…` / `1 lançamento … está … Ele não entra…` (como hoje) |
| `receitas` | `3 receitas de setembro estão sem categoria. Elas não entram…` / `1 receita … está … Ela não entra…` |
| `despesas` | `9 despesas de setembro estão sem categoria. Elas não entram…` / `1 despesa … está … Ela não entra…` |
| `transferencias` · `investimentos` | **não existe** — a contagem é 0 por construção |

O fecho da frase é o de hoje, palavra por palavra: `… não entram em nenhum orçamento, e nos
relatórios aparecem como "Sem categoria".` (singular: `… não entra em nenhum orçamento, e nos
relatórios aparece como "Sem categoria".`). Os textos completos estão em (h).

- **Zero código novo para sumir.** Com `transferencias` e `investimentos` a contagem do filtro é
  estruturalmente 0 (transferência não tem categoria; aporte e resgate **têm**, senão não estariam
  no filtro), e a faixa já some sozinha quando zera. Nenhum caso especial — e nenhum
  `semCategoria` possível nesses dois tipos, por (a).
- **O botão preserva o tipo**: `Ver só essas 3` liga `semCategoria=1` **mantendo** `?tipo=receitas`.
  Concordância obrigatória: `esses` para lançamentos, `essas` para receitas e despesas.
- **`Categorizar automaticamente` vira `Categorizar o mês automaticamente` quando há `tipo`
  ativo.** O diálogo é do **mês** (E2c (e)) e a frase ao lado passou a ser do filtro: o rótulo
  antigo, ao lado de "3 receitas", prometeria 3 e faria 12. A regra dos blocos de decisão — *a ação
  diz no próprio rótulo o que vai acontecer* — obriga o escopo a aparecer no botão assim que a
  frase deixa de carregá-lo. Em Tudo o rótulo continua o ratificado, porque a frase já diz "de
  setembro".
- **A faixa `info` do filtro ativo** (`semCategoria=1`) nomeia o tipo do mesmo jeito, e a saída
  também: `Mostrando só as despesas sem categoria de setembro.` + `Mostrar todas as despesas` — o
  botão limpa **só** o `semCategoria`, então prometer "todos os lançamentos" seria mentira.

### (e) Casca da tela: título, apoio, `caption` e vazios

- **`<h1>` não muda: `Lançamentos`, sempre.** Ele é o nome da rota e recebe o foco na entrada;
  trocar o texto a cada filtro faria a tela mudar de identidade a cada escolha. O estado vive no
  controle e na linha de apoio.
- **Apoio** (o `<p>` sob o `<h1>`) — cada filtro empresta a frase já ratificada da tela irmã, o que
  ensina a partição sem uma linha de prosa a mais: Tudo `Tudo o que entrou e saiu em setembro.` ·
  receitas `O que entrou em setembro.` · despesas `O que saiu em setembro.` · transferências `O que
  mudou de conta dentro da casa em setembro.` (a mesma de `/transferencias`) · investimentos `O que
  saiu para investir e o que voltou em setembro.` (a mesma de `/investimentos`).
- **`document.title`** ganha o filtro à frente: `Despesas · Lançamentos · HomeFinance`. Em Tudo
  continua `Lançamentos · HomeFinance`. O sufixo `· Lançamentos` é o que o distingue de
  `Transferências · HomeFinance`, que é outra rota.
- **`caption` da `DataTable`** (é `sr-only`, e é o que diz a quem não vê a faixa o que a tabela
  contém): `Receitas de setembro, agrupadas por dia` · `Despesas de setembro, agrupadas por dia` ·
  `Transferências de setembro, agrupadas por dia` · `Aportes e resgates de setembro, agrupados por
  dia` · Tudo como hoje. Concordância no particípio.
- **Vazios** — a precedência é `semCategoria` → `tipo` → `conta`, e **cada botão diz a dimensão que
  limpa**. Com tipo e conta ativos, dois botões (primário limpa o tipo, secundário limpa a conta):
  adivinhar qual a pessoa quis desfazer é pior do que oferecer os dois. Títulos e descrições em (h).

### (f) As colunas sob filtro — `Movimento` entra, `Categoria` sai

Duas correções de honestidade, as duas derivadas de regras já escritas:

- **`tipo=transferencias`: a coluna `Categoria` não existe.** Toda linha traria a mesma
  `Badge Transferência` — o mesmo ruído de "escrever *Novo* 59 vezes" que a spec 0004 §3.4 recusou.
  Restam `Conta · Descrição · Valor · Ações`, e a `.secundaria` do celular mostra só o nome da
  conta. O valor continua **neutro com sinal** (`−5.000,00` / `+5.000,00`): aqui o sinal é a
  direção relativa à conta da linha, e é o único portador dela.
- **`tipo=investimentos`: entra a coluna `Movimento`** (primeira, `width: 'min'`, **sem**
  `hideBelow` — ela é o portador), com a palavra `Aporte` ou `Resgate` em `--ink`, nunca `Badge`,
  nunca ícone (E7). Ela é derivável sem contrato novo: dentro deste filtro, `kind === 'expense'` é
  aporte e `kind === 'income'` é resgate — o pareamento natureza↔lado do dinheiro é obrigatório no
  servidor (spec 0006 §7.2), e o comentário no código deve dizer isso, porque a derivação **só**
  vale aqui. E, com a palavra na tela, o valor passa a **neutro e sem sinal** (`MoneyText` puro):
  `--income`/`--expense` e o `−` são proibidos no **valor de linha** de aporte e resgate (E7 (b);
  o sinal do líquido do painel é outra coisa, e é do painel) — pintar o aporte de
  vermelho ensina exatamente o erro que a E7 existe para corrigir. A regra geral que isso fixa:
  **cor e sinal só onde a palavra não está.**

Em `receitas` e `despesas` as colunas são as de hoje, com `sign="always"` mantido: uma coluna toda
de `+` custa um caractere, mantém a tabela idêntica entre os filtros (sem salto de largura ao
trocar) e deixa cada linha legível fora do contexto.

### (g) A linha que some — o toast

Com `tipo=despesas` ativo, categorizar uma linha como investimento pelo atalho da célula
`Sem categoria` (E2c (h)) tira a linha da lista **na hora**. Sumir em silêncio é inaceitável: a
pessoa acabou de aprender, sem querer, que a natureza da categoria muda o tipo do lançamento.

- O toast continua **de sucesso** (nada falhou; ela fez o que quis) e ganha uma segunda frase:
  `Lançamento categorizado como CDB. Ele saiu da lista: é um aporte, e a lista mostra só despesas.`
  Sob `receitas`: `… é um resgate, e a lista mostra só receitas.`
- Na saída com palavra-chave, a segunda frase entra depois do texto de hoje:
  `«cdb» adicionada a CDB · mais 3 lançamentos de setembro categorizados. Este lançamento saiu da
  lista: é um aporte, e a lista mostra só despesas.`
- **Emenda §19 (18/09/2026), modo trocar**: sob `investimentos` toda linha é categorizada, e trocar
  um aporte para uma categoria de despesa (ou um resgate para uma de receita) também a tira da
  lista. Mesmo molde: `Categoria trocada de CDB para Transporte. Ele saiu da lista: é uma despesa,
  e a lista mostra só aportes e resgates.` / `… é uma receita, e a lista mostra só aportes e
  resgates.` Sob `despesas`/`receitas` a troca usa as duas frases já ratificadas acima. As demais
  regras (só-este × com palavra, sujeito `Ele`/`Este lançamento`) valem iguais — ver E2c (h) §4,
  "Depois de trocar".
- A frase só aparece quando a natureza da categoria escolhida **contradiz o `tipo` ativo** — em
  Tudo, e com categoria do mesmo lado, o toast é o de hoje, sem acréscimo. Com `semCategoria=1` a
  linha também sai, e isso continua **sem** frase: ali a causa é evidente (o filtro é "sem
  categoria" e ela deixou de estar), enquanto aqui a causa é uma regra que a pessoa não viu.
- **Foco**: nada muda — ele já vai para a próxima lacuna calculada depois do refetch (E2c (h) 4), e
  é justamente esse mecanismo que sobrevive a uma linha que desaparece. Merece teste. Em modo
  trocar não há próxima lacuna: o foco vai ao controle de categoria da linha vizinha na foto de
  antes (E2c (h) §4, "Depois de trocar").

### (h) Copy pt-BR — tabela única

| Onde | Texto |
|---|---|
| Rótulo do filtro | `Tipo` |
| Opção vazia (placeholder) | `Tudo` |
| Opções | `Receitas` · `Despesas` · `Transferências` · `Investimentos` |
| Apoio do `<h1>` | `Tudo o que entrou e saiu em setembro.` · `O que entrou em setembro.` · `O que saiu em setembro.` · `O que mudou de conta dentro da casa em setembro.` · `O que saiu para investir e o que voltou em setembro.` |
| `document.title` | `Lançamentos · HomeFinance` · `Receitas · Lançamentos · HomeFinance` · `Despesas · …` · `Transferências · …` · `Investimentos · …` |
| `caption` da tabela | `Lançamentos de setembro, agrupados por dia` · `Receitas de setembro, agrupadas por dia` · `Despesas de setembro, agrupadas por dia` · `Transferências de setembro, agrupadas por dia` · `Aportes e resgates de setembro, agrupados por dia` |
| Faixa — receitas | `Entrou 5.300,00` |
| Faixa — despesas | `Saiu 3.100,00` |
| Faixa — transferências | `Transferência não é receita nem despesa — o dinheiro só mudou de conta dentro da casa.` |
| Faixa — investimentos | `Aportes 2.000,00 · Resgates 850,00` |
| 2ª linha sob receitas | `Fora destes números: 850,00 em resgates` |
| 2ª linha sob despesas | `Fora destes números: 2.000,00 em aportes` |
| Coluna nova | `Movimento` — `Aporte` / `Resgate` |
| Pendência — receitas | `3 receitas de setembro estão sem categoria. Elas não entram em nenhum orçamento, e nos relatórios aparecem como "Sem categoria".` / `1 receita de setembro está sem categoria. Ela não entra em nenhum orçamento, e nos relatórios aparece como "Sem categoria".` |
| Pendência — despesas | `9 despesas de setembro estão sem categoria. Elas não entram em nenhum orçamento, e nos relatórios aparecem como "Sem categoria".` / `1 despesa de setembro está sem categoria. Ela não entra em nenhum orçamento, e nos relatórios aparece como "Sem categoria".` |
| Botão da pendência | `Ver só esses 12` / `Ver esse lançamento` · `Ver só essas 3` / `Ver essa receita` · `Ver só essas 9` / `Ver essa despesa` |
| Botão primário da pendência | `Categorizar automaticamente` (Tudo) · `Categorizar o mês automaticamente` (com `tipo`) |
| Faixa `info` do filtro ativo | `Mostrando só os lançamentos sem categoria de setembro.` · `Mostrando só as receitas sem categoria de setembro.` · `Mostrando só as despesas sem categoria de setembro.` |
| Saída da faixa `info` | `Mostrar todos os lançamentos` · `Mostrar todas as receitas` · `Mostrar todas as despesas` |
| Vazio — receitas | `Nenhuma receita em setembro.` — `O que entra por resgate de investimento está em Investimentos, e o que vem de outra conta sua é transferência.` — `Mostrar todos os tipos` |
| Vazio — despesas | `Nenhuma despesa em setembro.` — `Aporte em investimento está em Investimentos, e o que foi para outra conta sua é transferência.` — `Mostrar todos os tipos` |
| Vazio — transferências | `Nenhuma transferência em setembro.` — `Transferência é o dinheiro que muda de conta dentro da casa — na importação, o app a detecta pelas palavras-chave das contas.` — `Mostrar todos os tipos` |
| Vazio — investimentos | `Nenhum aporte ou resgate em setembro.` — `Um lançamento entra aqui quando recebe uma categoria de investimento ou de resgate.` — `Mostrar todos os tipos` |
| Vazio — tipo + conta | `Nenhuma despesa na Nubank em setembro.` — `Troque o tipo, troque a conta, ou volte para a lista inteira.` — `Mostrar todos os tipos` (primário) + `Mostrar todas as contas` (secundário) |
| Vazio — semCategoria + tipo | `Todas as receitas de setembro estão categorizadas.` / `Todas as despesas de setembro estão categorizadas.` — `Nenhuma receita deste mês ficou sem categoria.` / `Nenhuma despesa deste mês ficou sem categoria.` — `Mostrar todas as receitas` / `Mostrar todas as despesas` |
| Toast — saiu por natureza | `Lançamento categorizado como CDB. Ele saiu da lista: é um aporte, e a lista mostra só despesas.` / `… é um resgate, e a lista mostra só receitas.` |
| Toast — com palavra-chave | `«cdb» adicionada a CDB · mais 3 lançamentos de setembro categorizados. Este lançamento saiu da lista: é um aporte, e a lista mostra só despesas.` |
| Toast — saiu sob `investimentos` (modo trocar, emenda §19) | `Categoria trocada de CDB para Transporte. Ele saiu da lista: é uma despesa, e a lista mostra só aportes e resgates.` / `… é uma receita, e a lista mostra só aportes e resgates.` |

Léxico: mês por extenso e em minúsculas; `aporte`/`resgate` para o movimento e `Investimentos` só
como nome do tipo (E7 (a)); nenhum botão diz "todos os lançamentos" quando não mostra todos.

### (i) Acessibilidade

- **Trocar o filtro não tira o foco do controle.** O `<h1>` recebe o foco na **entrada da rota**,
  nunca em mudança de busca — a mesma armadilha que `CategoryReportScreen.test.tsx` já fixa para
  `natureza`. Teste equivalente obrigatório aqui, para `Tipo` **e** para `Conta`.
- **Nenhuma live region nova.** O `<select>` anuncia a própria opção; a contagem está no rodapé e o
  `caption` muda junto. Duas regiões vivas simultâneas (a da `DataTable loading` e um status de
  filtro) atropelariam uma à outra — a regra "sem duplicata" da E2c continua valendo.
- **Contraste**: zero cor nova e zero tamanho novo. A frase das transferências reusa a classe
  `.resumo` — `--text-13`/`--ink-muted` sobre `--surface-sunken` —, o par que esta faixa já usa
  desde a E2; nenhuma combinação nova é criada.
- Alvo de toque: `Select density="compact"` = `--control-h-sm` (36 px), o mesmo da faixa hoje.
- `grayscale(1)`: o filtro é palavra no controle, o tipo é palavra no apoio, no `caption` e no
  `document.title`, e o movimento é palavra na coluna. Nada depende de tinta.

### (j) Exigências de contrato — verificar antes de implementar (padrão da spec 0004 §B)

**C1 · `investedCents` e `redeemedCents` não respondem ao `kindGroup`.** Eles respondem a `month`
e a `accountId`, como hoje, e **ignoram** o filtro de tipo. Sem isso, os dois campos vão a zero
justamente sob `tipo=despesas`, que é onde a omissão é maior, e a 2ª linha da faixa desaparece
contra o que o próprio contrato manda ("a tela é **obrigada** a mostrar os dois números quando
forem maiores que zero"). É o único ponto em que o `summary` deixa de ser "do mesmo filtro", e a
descrição do schema precisa dizê-lo com todas as letras. Responsável: `arquiteto` /
`dev-backend-go`.

**Atenção: C1 contraria o que a T1 já escreveu.** Hoje `aplicarGrupoDeTipo` entra no `WHERE` do
`Summary` inteiro, com a justificativa — correta para os outros campos — de que "o resumo fala
exatamente da janela que a lista mostra". A exceção pedida vale **só** para estes dois campos, e o
motivo é que eles não descrevem a janela: eles descrevem **o que ficou de fora dela**. Um campo
cujo trabalho é nomear a omissão não pode ser silenciado pelo filtro que aumenta a omissão.

*Degradação aceita, se o `arquiteto` recusar C1* — a tela **não** fica bloqueada: sob `receitas` e
`despesas` a 2ª linha simplesmente não existe, e a linha de apoio passa a nomear o limite em
palavras, `O que saiu como despesa em setembro.` / `O que entrou como receita em setembro.`
(em vez de `O que saiu em setembro.` / `O que entrou em setembro.`). É pior — a fronteira vira
adjetivo em vez de número —, mas é honesto e custa zero. A escolha entre as duas é do `arquiteto`,
e o `dev-frontend-react` implementa a que estiver decidida quando começar.

**C2 · `uncategorizedCount` e `count` respondem ao `kindGroup`** — é o que (d) e o rodapé de
paginação assumem, e é o que a T1 já faz. Escrito aqui porque a faixa de pendência depende disso
para não mentir.

### Checklist anti-cara-de-IA — E2d (aplicar com a tela pronta)

1. O filtro é um `<select>` nativo com rótulo visível `Tipo` — não é *segmented control*, não é
   grupo de "pills", não é aba, não é chip removível, não é menu com ícones.
2. Nenhuma opção ganhou cor, bolinha, ícone ou contador dentro do `<select>`.
3. Com `tipo=transferencias` **não existe** `0,00` em lugar nenhum: nem na faixa, nem no cabeçalho
   do dia. Uma coluna de zeros repetidos reprova.
4. Com `tipo=investimentos` nenhum valor é vermelho, verde ou com sinal; `Aporte`/`Resgate` é
   palavra em `--ink`, nunca `Badge`, nunca ícone.
5. A faixa de pendência **nomeia o tipo** e concorda em gênero (`essas 3 receitas`, nunca
   `esses 3 receitas`); o botão primário diz `o mês` quando age sobre o mês.
6. Nenhum botão promete mais do que limpa (`Mostrar todas as despesas` quando só o `semCategoria`
   sai).
7. Nenhuma linha some em silêncio: o toast diz que saiu e por quê.
8. `grayscale(1)`: o estado do filtro continua legível no controle, no apoio, no `caption` e no
   `document.title`.
9. Nenhuma medida, cor ou raio novo em `TransactionsScreen.module.css` — só `.filtros`, com tokens.
10. Copy da tabela (h) palavra por palavra; nenhum "filtros avançados", "visão geral", "dashboard",
    "insights" ou emoji.

## E4a — o painel: a faixa de resumo do mês (spec 0008, 18/09/2026)

Primeira fatia da E4. O painel deixa de ser placeholder e passa a responder três perguntas do mês
selecionado — quanto entrou, quanto o cartão comeu, quanto ficou investido. Estende E2, E2c, E6a,
E7 e E2d sem revogar nada: `grayscale(1)` continua sendo o aceite, a cor cromática continua com
três donos, e aporte e resgate continuam fora de receita e despesa.

**Nenhum token novo, nenhum componente base novo, nenhuma cor cromática.** A faixa é a terceira
ocorrência de um idioma que o app já tem (a `<dl>` de `TransferPairPanel` e da `ColunaDeNumeros` de
`/investimentos`) e usa variantes de `MoneyText` que já existem. Uma tela que não pede nada novo é
o sinal de que a identidade está de pé.

Arquivos: `features/home/components/MonthSummaryBand.tsx` + `.module.css` (a faixa),
`features/home/components/HomeScreen.tsx` + `.module.css` (a coluna e o cabeçalho) e
`features/home/api/dashboard.ts` (a consulta). `LedgerPreview.*` foi **apagado** com o placeholder.

### (a) Forma: uma `<dl>` de três linhas, e não a faixa inline de `/lancamentos`

`Panel padding="none"` → `<section aria-labelledby>` → `<h2 class="tituloDaSecao">Resumo de
setembro</h2>` → `<dl class="lista">` com três `.linha` (`<dt>`/`<dd>`), separadas por
`1px solid var(--border)`.

```
+- main (casca) ------------------------------------------------------------+
|                                                                           |
|  Ola, Bruno.                          <- <h1> Fraunces --text-28 / 600     |
|  sexta-feira, 18 de setembro de 2026  <- <p> --text-15 / --ink-muted       |
|                                                                           |
|  +- Panel padding="none" - 38rem - 1px --border - --radius-md ---------+   |
|  | Resumo de setembro       <- <h2> --font-ui --text-13/600 --ink-muted|   |
|  |                                                                     |   |
|  | Receita do mes                                       R$ 5.000,00    |   |
|  | 3 lancamentos                                                       |   |
|  | ------------------- 1px solid var(--border) ----------------------- |   |
|  | Gasto no cartao de credito                             R$ 800,00    |   |
|  | 2 lancamentos                                                       |   |
|  | ------------------------------------------------------------------- |  |
|  | Investido no mes                                    +R$ 1.650,00    |   |
|  | 2 lancamentos                                                       |   |
|  +---------------------------------------------------------------------+   |
|                                                                           |
|  (nada abaixo — a tela acaba aqui, e é para acabar aqui)                   |
+---------------------------------------------------------------------------+
```

Celular (375 px): **nenhuma mudança de estrutura** — é a virtude da `<dl>`, que já é uma coluna. Só
os paddings encolhem; o rótulo longo quebra em duas linhas e o número fica ancorado na primeira.

```
+------------------------------------+
| Ola, Bruno.                        |
| sexta-feira, 18 de setembro de 2026|
| +--------------------------------+ |
| | Resumo de setembro             | |
| | Receita do mes     R$ 5.000,00 | |
| | 3 lancamentos                  | |
| | ------------------------------ | |
| | Gasto no cartao      R$ 800,00 | |  <- rotulo quebra; numero na 1a linha
| | de credito                     | |
| | 2 lancamentos                  | |
| | ------------------------------ | |
| | Investido no mes  +R$ 1.650,00 | |
| | 2 lancamentos                  | |
| +--------------------------------+ |
+------------------------------------+
```

Conta a 375 px: 375 − 2×24 (padding do `<main>`) − 2 (borda) − 2×12 (`--space-3`) = **301 px**
úteis; 216 (rótulo do cartão a `--text-15`) + 16 + 85 = 317 > 301, então a quebra é real e está
desenhada, não improvisada. Nada some e nada rola na horizontal.

Por que `<dl>` e não o `.resumo` inline de `/lancamentos`, que é a faixa vizinha:

1. **Papel diferente.** Lá a faixa é acessório de barra de ferramentas sobre uma tabela
   (`--text-13`, `--ink-muted`, dentro da `.faixa` em `--surface-sunken`). Aqui ela **é** o conteúdo
   da tela: um parágrafo de 13 px como único conteúdo de uma página seria nota de rodapé fingindo
   ser página. O léxico, os tokens e a regra de sinal continuam idênticos — muda o peso, porque
   mudou o papel.
2. **Cada número tem contagem.** Seis valores numa frase inline não se lê; em `<dl>` a contagem é a
   segunda linha do `<dt>`, exatamente como em `/investimentos`.
3. **Três ocorrências fazem um padrão do produto** — e é isso que faz o app parecer de um estúdio.

**Empilhada, nunca em três colunas.** Os três números são do mesmo período: empilhados, alinham na
mesma borda direita e a leitura vertical tabular funciona. Em três colunas haveria três bordas
direitas diferentes, nenhuma comparação possível, e a silhueta do grid de cards que a lista de
rejeição proíbe.

**Ordem: `Receita do mês` · `Gasto no cartão de crédito` · `Investido no mês`** — a gramática
`Entrou · Saiu · Resultado` da faixa vizinha: entrada, saída, e o derivado com sinal sempre por
último. **Ratificada pelo usuário em 18/09/2026.** A enumeração da spec 0008 §2 (investimento
primeiro) é ordem de **escopo**, vinda da entrevista, e nenhum parágrafo dela fixa ordem de tela:
esta é a ordem da tela, e ela não muda sem nova decisão do usuário.

**Cabeçalho da tela**: o `<h1>` continua sendo a saudação (`Olá, Bruno.`, Fraunces `--text-28`) com
a data de **hoje** como apoio — é a única nota doméstica do produto. O `<h2>` da faixa é quem nomeia
o **mês selecionado**, e é ele que impede a confusão entre as duas datas. `document.title` passa a
`Painel · HomeFinance`, alinhado ao item de menu e ao padrão das outras telas.

**Largura**: `.pagina { max-inline-size: 38rem }` — rótulo mais longo (216 px) + gap + valor mais
largo (130 px) = 370 px de mínimo; 38rem dá folga sem virar linha-guia pontilhada. Quando os demais
blocos da E4 (saldo por conta, vencimentos, top categorias) chegarem, a página vai a `62rem` e a
faixa vira o primeiro bloco da grade. **Até lá não existe moldura vazia prometendo o futuro**: a
regra da casca — *item que não leva a lugar nenhum não é criado* — vale igual para bloco de tela.
`min-block-size: 100dvh`, `max-inline-size: 1120px`, `margin-inline: auto` e fundo próprio são da
**casca**: o painel duplicava os quatro e levava padding em dobro.

**Medidas** (`MonthSummaryBand.module.css`, só tokens): `.tituloDaSecao` `padding: var(--space-4)
var(--space-4) 0`, `--font-ui`, `--text-13`, peso 600, `--ink-muted`; `.lista` `padding:
var(--space-2) var(--space-4) var(--space-4)`; `.linha` `display: flex; align-items: baseline;
justify-content: space-between; gap: var(--space-5); padding-block: var(--space-3)`; `.termo`
`display: flex; flex-direction: column; gap: 2px; color: var(--ink-muted)`; `.contagem` `--text-13`,
`--ink-muted`, `tabular-nums`; `.valor { flex: none }` (o corpo e o peso vêm do `MoneyText`). Abaixo
de 40rem só os paddings caem para `var(--space-3)` e o `gap` da linha para `var(--space-4)`.

### (b) Sinal e tom — a faixa é cromaticamente silenciosa

| Linha | `MoneyText` |
|---|---|
| `Receita do mês` | `format="currency" emphasis="total"`, neutro, sem sinal |
| `Gasto no cartão de crédito` | `format="currency" emphasis="total"`, neutro, sem sinal |
| `Investido no mês` | `format="currency" sign="always" emphasis="total"`, **neutro** |

- **Receita e cartão sem tom**: a palavra já diz a direção, e pintar os dois gastaria
  `--income`/`--expense` em informação que o texto carrega. É a regra literal de `Entrou`/`Saiu`.
- **O líquido não recebe tinta**, exatamente como o `Líquido` de `/transferencias`. `--expense`
  diria "você gastou" sobre um resgate, que é dinheiro da casa voltando; `--income` diria "você
  ganhou" sobre um aporte, que é dinheiro que continua sendo dela. É o erro que a E7 existe para
  corrigir (E7 (b)), e a E2d (f) já escreveu que "o sinal do líquido do painel é outra coisa".
- Os três com `emphasis="total"` (`--text-18`, peso 700): mesmo mês, mesmo peso na pergunta.
  **Nenhum número hero** — `hero` é o furo da rosca da E6a.
- `format="currency"` (e não o `plain` das tabelas) porque aqui não há cabeçalho de coluna dizendo
  que é dinheiro; mesmo motivo do número central da rosca em `/relatorios/categorias`. A spec 0008
  §3.3 fixa o literal `+R$ 2.000,00` / `-R$ 350,00`.
- **Nenhuma aritmética de dinheiro na tela**: `investmentNetCents` chega pronto do servidor.
  Subtrair aportes e resgates no cliente criaria uma segunda fonte para o mesmo número (ADR-003).

**Sinal E palavra** (aceite 20 da spec). O sinal é o portador visual; a palavra mora na segunda
linha do `<dt>`, colada à contagem, e só existe quando há o que explicar: `< 0` →
`2 lançamentos · os resgates superaram os aportes`; `= 0` com movimento →
`2 lançamentos · aportes e resgates se anularam`; `> 0` → só a contagem. Nenhuma delas conjuga
**aportar** (E7 (a)). A linha do zero-com-movimento existe pelo mesmo motivo que a frase de
equilíbrio de `/transferencias`: `R$ 0,00` ao lado de `2 lançamentos` é contradição silenciosa.

O glifo do menos é **o que o `Intl` emite** — hífen-menos, como `MoneyText.test.tsx` já fixa. O
`−` de qualquer documento é tipografia de markdown, e teste que o cole falha. Nenhuma normalização
de glifo fora do `MoneyText`.

### (c) Estados

Precedência: `erro` → `carregando` → `linha sem cadastro (traço)` → `mês zerado (R$ 0,00)`.
**A faixa nunca some**: mês sem movimento mantém os três números em `R$ 0,00` — sumir faria a
pessoa achar que a tela quebrou.

1. **Carregando (1ª carga)**: `Skeleton width="4.5rem" height="1rem"` em cada `<dd>` e **sem** a
   linha de contagem (contagem inventada é pior que ausência); `aria-busy` na `<section>`. Nunca
   zeros provisórios: zero é um valor, e mostrá-lo antes da resposta é mentir.
2. **Troca de mês** (refetch com dado em tela): `placeholderData: keepPreviousData`, `aria-busy` e
   `opacity: 0.6` com `transition: opacity var(--motion-base) var(--ease)` — o mesmo dispositivo de
   `/investimentos`. Trocar de mês é a interação principal do painel; o quadro não pode piscar.
3. **Mês zerado**: `R$ 0,00` e `nenhum lançamento` nas três linhas.
4. **Casa sem cartão / sem categoria de investimento**: a `<dd>` vira
   `<span aria-hidden="true">—</span><span class="sr-only">sem valor</span>` (o dispositivo de
   `CelulaData` na revisão de importação) e o slot da contagem recebe a frase mais um `TextLink`
   com o `mes`: `Nenhum cartão de crédito cadastrado · Ir para contas` · `Nenhuma categoria de
   investimento ainda · Ir para categorias`. É `TextLink` e não `Button`: navegar é trabalho de
   link. **Traço aqui não contradiz "zero é um valor, nunca travessão" (E7 (d.2))**: lá o travessão
   substituiria um zero verdadeiro; aqui ele diz que o conceito não existe nesta casa. São estados
   diferentes, e distingui-los é a razão de o contrato trazer `creditCardAccountCount` e
   `investmentCategoryCount`.
   **Condição obrigatória — o cartão arquivado:** gasto de cartão arquivado conta no mês, mas o
   contador só conta cartão vivo (spec 0008 §6). Logo o traço exige os **três** campos zerados
   (`creditCardAccountCount === 0 && creditCardExpenseCents === 0 && creditCardExpenseCount === 0`;
   o mesmo trio para investimento). Havendo qualquer valor ou qualquer contagem, **o número ganha**:
   nunca se esconde dinheiro por causa de um contador de cadastro. Tem teste próprio.
5. **Erro (rede/500)**: `Alert tone="error" title="Não foi possível carregar o resumo do mês."` com
   `messageForError` e `Tentar de novo` (`loading={isFetching}`) **no lugar do `Panel`**; o
   cabeçalho continua utilizável. 401 é da casca, como em toda tela.

**Um pedido de rede, e um só.** É proibido buscar `/accounts` ou `/categories` no painel para
decidir estado vazio. Chave de cache `["transactions", "dashboard", mes]` — sob o prefixo
`transactions` pelo motivo da ADR-027: categorizar em qualquer tela tem de reler estes números,
senão o painel diverge da tela de origem.

### (d) Acessibilidade

- O par `<dt>`/`<dd>` é o nome acessível de cada número; o valor falado sai do `sr-only` interno do
  `MoneyText` (`R$ 350,00 negativos`). A contagem mora no `<dt>`, nunca na `<dd>` — ela qualifica o
  termo; na `<dd>` viraria um segundo valor concorrendo com o dinheiro.
- **Sem sufixo `sr-only` de período nos rótulos**, ao contrário da E7 (d): lá quatro rótulos se
  repetiam entre duas colunas; aqui os três são únicos e o `<h2>` associado já nomeia o mês.
  Repeti-lo seria a tagarelice que a E7 (j) condena.
- `sr-only` entra em exatamente dois lugares: dentro do `MoneyText` e no `sem valor` do traço.
- `aria-busy` vai na `<section>` (1ª carga e refetch) e só nela. **Nenhuma live region**: o mês já é
  anunciado pelo `<output aria-live="polite">` do `MonthNavigator`, e três valores a cada seta
  atropelariam esse anúncio.
- Foco no `<h1>` na entrada da rota (`tabIndex={-1}`, anel só em `:focus-visible`); trocar de mês
  não mexe no foco. Alvos: os dois `TextLink` são inline (exceção da WCAG 2.5.8); o `Tentar de novo`
  é `Button` a `--control-h`.
- Contraste: `--ink` sobre `--surface` nos números, `--ink-muted` nos rótulos e contagens, nenhuma
  combinação nova, nenhum texto abaixo de `--text-13`. `prefers-reduced-motion`: sem a transição.

### (e) Copy pt-BR — tabela única

| Onde | Texto |
|---|---|
| `document.title` | `Painel · HomeFinance` |
| `<h1>` | `Olá, Bruno.` · sem sessão `Olá.` · carregando `Skeleton` + `sr-only` `Carregando sua conta` |
| Apoio do `<h1>` | `sexta-feira, 18 de setembro de 2026` (data de **hoje**) |
| `<h2>` da faixa | `Resumo de setembro` (sem o ano — ele está no seletor da casca) |
| Rótulos, nesta ordem | `Receita do mês` · `Gasto no cartão de crédito` · `Investido no mês` |
| Contagem | `3 lançamentos` / `1 lançamento` / `nenhum lançamento` |
| De onde sai cada contagem | a contagem acompanha **o seu** número, um campo por linha: `incomeCount`, `creditCardExpenseCount` e `investmentCount`. O contrato **não** ganhou um décimo campo, e nenhuma contagem é somada, derivada ou reaproveitada de outra linha |
| Líquido negativo | `2 lançamentos · os resgates superaram os aportes` |
| Líquido zero com movimento | `2 lançamentos · aportes e resgates se anularam` |
| Sem cartão | `—` (`sr-only` `sem valor`) — `Nenhum cartão de crédito cadastrado` · `Ir para contas` |
| Sem categoria de investimento | `—` (`sr-only` `sem valor`) — `Nenhuma categoria de investimento ainda` · `Ir para categorias` |
| Erro | `Não foi possível carregar o resumo do mês.` — `Tentar de novo` |

Léxico: mês por extenso e em minúsculas; `aportes`/`resgates` como substantivos, nunca o verbo
conjugado; `Ir para categorias` é o rótulo já ratificado na E7. Palavras proibidas nesta tela:
"visão geral", "dashboard", "insights", "seu mês em números", "patrimônio", "rentabilidade", e
qualquer exclamação ou emoji.

### Checklist anti-cara-de-IA — E4a (aplicar com a tela pronta)

1. **Um** `Panel`, três linhas separadas por `1px solid var(--border)`: nenhum cartão por número,
   nenhuma sombra, nenhum fundo tingido, nenhum ícone ao lado de valor.
2. Nenhum número acima de `--text-18`; sem `hero`, sem número centralizado.
3. `grayscale(1)` não muda nada — `--income`, `--expense` e `--warning` **não aparecem** no
   `MonthSummaryBand.module.css`.
4. O líquido negativo tem três portadores: o `-`, a frase e o `sr-only` `negativos`.
5. Exatamente três números: nenhum quarto derivado, nenhuma barra de progresso, nenhum mini-gráfico,
   nenhuma seta de tendência, nenhuma comparação com o mês anterior.
6. Sem cartão → traço; cartão sem gasto → `R$ 0,00`; cartão arquivado com gasto → o número aparece.
7. Carregando não mostra zero provisório nem contagem inventada.
8. Nenhuma moldura vazia prometendo saldo por conta, vencimentos ou top categorias.
9. `Gasto no cartão de crédito` nunca vira `Gastos`, `Despesas` ou `Gasto do mês` — o rótulo é a
   cerca do número, e encurtá-lo transformaria um parcial em total.
10. A faixa é `<dl>` de verdade, com `<h2>` real associado por `aria-labelledby`.
11. Zero medida, cor ou raio fora dos tokens em `MonthSummaryBand.module.css` e
    `HomeScreen.module.css`.
12. `LedgerPreview` e a copy de placeholder foram apagados, não escondidos.
13. Copy da tabela (e) palavra por palavra; nenhum emoji.
14. Um único pedido de rede monta a faixa; nada de `/accounts` ou `/categories` para decidir vazio.

---

## Spec 0010 — a tela /ia: exportar prompt, importar palavras-chave, reprocessar (21/09/2026)

A tela `/ia` é a fronteira do produto com o mundo de fora. **O aplicativo nunca fala com IA**: não há
cliente, chave de API nem chamada de rede para provedor nenhum (spec 0010 §2.2). O app escreve um
texto, a pessoa leva esse texto a uma IA de fora, e traz a resposta de volta em JSON. O transporte é
a pessoa — e é isso que o desenho inteiro desta tela existe para tornar óbvio.

A entrega é fatiada em três (spec 0010 §10.2). Este bloco descreve a tela inteira; a fatia **E9a**
implementa o item de menu, a janela de trabalho e a seção **Exportar**.

### (a) Casca — a barra do celular fecha em **sete** células

`IA` é o oitavo destino do produto, e **não** ganha célula na barra inferior. Regra nova, e ela vale
para tudo que vier depois: **ferramenta não ocupa célula da barra.**

A célula da barra mede `(viewport − 16 − (N − 1) × 2) / N` no content box (16px são os dois
`--space-2` de padding da barra; 2px é o `gap` da grade). O rótulo precisa de **47px** — a largura de
`Transfe-` a `--text-12`, a maior sílaba inicial da navegação. Abaixo disso a barra vira só-ícone,
por uma consulta de container cujo piso é `N × 47 + (N − 1) × 2`.

| Viewport | Célula com 7 | Célula com 8 | Piso só-ícone |
|---|---|---|---|
| 320 px | 41,7 px | 36,3 px | com 7 células: **341 px** (`21.3125rem`) |
| 360 px | 46,9 px | 40,8 px | com 8 células: **390 px** (`24.375rem`) |
| 375 px | 49,6 px | 43,1 px | |
| 390 px | 51,7 px | 45,0 px | |
| 393 px | 52,1 px | **45,4 px** | |
| 412 px | 54,9 px | 47,8 px | |
| 430 px | 57,4 px | 50,0 px | |

A leitura é direta. Com **sete** células, só o aparelho de 320px perde os rótulos. Com **oito**, o
piso sobe para 390px de content box e os rótulos de **toda** a navegação apagam em 360, 375, 390 e
393 px de viewport — praticamente todo celular em pé. Um item novo não cobra esse preço dos sete que
já estavam lá; e o rótulo perdido não seria o do item novo, seriam os dos sete existentes.

**Onde `IA` vive, então:**

- **Desktop (≥ 52rem):** último item da lateral, depois de `Categorias`, como **primeiro item do
  grupo "ferramentas"** — `border-block-start: 1px solid var(--border)` no `<li>`, com
  `margin-block-start` e `padding-block-start` de `var(--space-2)`. Um filete, e não um cabeçalho de
  grupo em caixa alta: o que separa superfícies neste projeto é 1px de borda.
- **Abaixo de 52rem:** o `<li>` recebe `display: none` (sai da grade, e a barra continua com sete
  colunas) e o destino aparece no `UserMenu` — um `<hr>` e um `<Link to="/ia" search={{ mes }}>` com
  a forma de `.item` do próprio menu, `PromptIcon size={16}` no slot `.mark` e `aria-current="page"`
  quando ativo. O clique **fecha o popover à mão** (`panelRef.current?.hidePopover()`):
  `popover="auto"` fecha por light dismiss e por ESC, **não** por clique dentro do painel, e o menu
  ficaria aberto por cima do `<h1>` que acabou de receber o foco.
- O gatilho do menu passa a se chamar **`Menu de {nome}`** (era `Conta de {nome}`) nas **duas**
  faixas. Ele deixou de ser só a conta, e um nome acessível que muda com a largura da janela seriam
  duas interfaces no mesmo botão.

### (b) `PromptIcon` — um bloco de texto entre dois colchetes

```
<path d="M9.25 4.75H5.75a1 1 0 0 0-1 1v12.5a1 1 0 0 0 1 1h3.5" />
<path d="M14.75 4.75h3.5a1 1 0 0 1 1 1v12.5a1 1 0 0 1-1 1h-3.5" />
<path d="M8.75 9.25h6.5" />  <path d="M8.75 12.25h6.5" />  <path d="M8.75 15.25h3.75" />
```

Contrato do conjunto: `viewBox 24`, traço 1.5, `currentColor`, cantos `round`, massa óptica entre
4,75 e 19,25, `IconProps` de `components/icons/types.ts`. Os colchetes são **espelhados** — eles são
a fronteira do app nos dois lados da viagem —, as três linhas formam um parágrafo com a última curta,
e **nada atravessa os colchetes**: o que sai, sai inteiro e pela mão da pessoa.

**Recusados, e o motivo importa tanto quanto o desenho:** robô, varinha mágica, faíscas/sparkles,
cérebro e balão de fala (são o clip-art de "IA", e nenhum deles diz o que a tela faz); **duas setas
opostas** (colidiria com o `TransfersIcon`, que já é o ícone de transferência interna); engrenagem e
raio (dizem "automático", e aqui **nada** é automático — nem o reprocessamento); `<>` e `{}` (dizem
"código" a quem só vai copiar texto).

### (c) A janela de trabalho — uma só, para as três seções

A URL publica `?mes=2026-09` (o que a casca já publica) **mais `?meses=3`** (novo: 1 a 3, padrão 3).
A janela é de **competência**, em meses civis, e **termina** no mês escolhido no alto da página —
nunca em datas, porque as rotas de reprocessamento só falam mês (spec 0010 §10, achado A2) e "3 meses
a partir do dia 15" não tem resposta.

`3` é o padrão e **a URL canônica não escreve a chave**, como `natureza` e `tipo` em `app/search.ts`;
valor fora de 1–3 some no portão e a tela abre na janela padrão, em vez de virar tela de erro.

A faixa é a mesma de `/lancamentos` e `/relatorios/categorias` — recuada, `--surface-sunken`, para
ler como barra de ferramentas e não como mais uma linha de dado — com um `Select density="compact"`
`label="Período"` cujas opções escrevem os meses **resolvidos**:

| Valor | Opção |
|---|---|
| 3 | `3 meses · julho a setembro` |
| 2 | `2 meses · agosto e setembro` |
| 1 | `1 mês · setembro` |

"Últimos 3 meses" obrigaria a pessoa a contar de cabeça qual é o período — e é exatamente o período
que ela está prestes a exportar para fora. À direita, em `--text-13`/`--ink-muted`:
`Vale para as três seções. A janela termina no mês escolhido no alto da página.`

**Uma função só** deriva tudo: `janelaDeTrabalho(mes, meses)` em `features/ai/janela.ts`, devolvendo
`{ fromMonth, toMonth, meses: ['2026-07', '2026-08', '2026-09'] }`, mais `rotuloDaJanela` (a frase) e
`nomeDoArquivoDoPrompt` (o nome do `.md`). **Nenhuma seção recalcula mês por conta própria** — três
derivações seriam três janelas se contradizendo no dia em que uma delas esquecesse a virada de ano.

**Regra de frescor:** trocar o mês da casca ou o tamanho da janela **descarta o resultado das outras
seções** e publica `A janela mudou — confira de novo para medir o impacto no novo período.` Na E9a
isso é a `key={fromMonth-toMonth}` da seção Exportar (que zera o "Copiado." e o `<details>` da janela
anterior); o aviso nasce com a E9b, quando existir resultado a descartar — aviso sem objeto seria
mentira.

### (d) A tela — três seções empilhadas, sempre abertas, numeradas no `<h2>`

`max-inline-size: 62rem`, coluna única, `gap: var(--space-4)`. `<h1>IA</h1>` com `tabIndex={-1}`
recebendo foco na entrada da rota (`useFocoNoTitulo`), `document.title = 'IA · HomeFinance'`. Apoio:
`O app escreve o pedido, você leva a uma IA de fora e traz a resposta de volta. Nada sai daqui
sozinho.` Depois a faixa da janela e as três seções, cada uma um `Panel as="section" padding="none"`:

1. `1 · Exportar o prompt`
2. `2 · Importar o que a IA respondeu`
3. `3 · Reprocessar`

**Não são abas, não é acordeão, não é wizard.** O fluxo **não é linear**: dá para importar sem ter
exportado nesta sessão, e dá para reprocessar sem ter importado nada. Os números dizem a ordem
**recomendada**, não uma sequência obrigatória — por isso são **texto no `<h2>`**, e nunca bolinhas
numeradas ligadas por linha. O `ImportStepper`, que marca passos de um fluxo que é mesmo sequencial,
**não entra aqui**.

`padding="none"` porque o `<pre>` e o `<summary>` sangram até a borda do painel; o `<h2>` numerado
mora no conteúdo, com o recuo dele, e a `<section>` recebe nome acessível pelo `titleId` do `Panel`.

Na E9a as seções 2 e 3 são renderizadas **sem controle nenhum** — nem botão desabilitado, nem campo
morto — com uma frase que começa por `Ainda não está no ar.` e diz o que vai acontecer ali. Elas
existem para a numeração ser verdadeira: um `1 ·` sozinho anunciaria um 2 e um 3 que a tela não
mostra. O que a regra do projeto proíbe é **prometer um caminho que não leva a lugar nenhum**; uma
frase honesta sobre o que ainda não chegou não é isso.

### (e) A seção Exportar — e o aviso como prosa

A ordem é normativa, e é esta:

1. **Apoio:** `O texto que você cola numa IA de fora — ChatGPT, Claude, a que você usar — para ela
   propor palavras-chave e categorias.`
2. **O aviso**, em **três** linhas. A primeira em `--text-15`/`--ink`, com `<strong>` em dois
   trechos: `Este texto leva **as descrições e os valores das suas movimentações** do período. O
   aplicativo não envia nada: **quem copia e cola numa IA de fora é você**.` As outras duas em
   `--text-13`/`--ink-muted`: `As descrições vão como você as vê no app — e costumam trazer o nome
   de quem pagou ou recebeu, e o que a pessoa escreveu na mensagem do Pix.` e `Não vão: saldos,
   instituição, agência e número de conta, dias de fatura, seu nome e e-mail de cadastro, nem
   identificador de lançamento.`
3. **Estatísticas numa linha**, `--text-13`/`--ink-muted`/`tabular-nums`:
   `540 linhas · 32 descrições distintas · 4 contas · 41 categorias`, com
   `· N descrições ficaram de fora (as menos frequentes)` quando `truncatedDescriptions > 0`, e
   `· nenhuma movimentação no período` (no lugar do segmento de descrições, que seria `0`) quando a
   janela não teve movimento. **Nunca** três cartões com número gigante.
   `540 linhas` é o tamanho do **texto**, contado uma vez e exibido também no `<summary>`: é a mesma
   pergunta nos dois lugares — quanto você está prestes a colar em outro lugar.
4. **Ações:** `Copiar o prompt` (primary) · `Baixar .md` (secondary) · `<output aria-live="polite">`
   com `CheckIcon size={14}` + `Copiado.` em `--accent`, sumindo após 6 s. **O rótulo do botão não
   muda ao copiar** — um botão que vira "Copiado!" some como botão justo quando a pessoa quer copiar
   de novo.
5. **`<details>` fechado** `Ver o texto (540 linhas)` → `<pre>` dentro de uma `<section>` rolável,
   `--surface-sunken`, `--ink`, `var(--font-mono)`, `max-block-size: 22rem`, `overflow: auto`,
   `tabIndex={0}` e `aria-label="Texto do prompt"` (técnica da WCAG 2.1.1 para região rolável).
   **Nunca fundo escuro, nunca cor de sintaxe** — é papel, não terminal.

**O aviso é prosa: sem caixa, sem ícone, sem `Alert tone="warning"`.** Dois motivos, e os dois são
duros. Primeiro, o banner amarelo com ícone é precisamente o objeto que as pessoas aprendem a pular —
transformá-lo em mobília é transformar o consentimento em clique automático. Segundo, `--warning`
neste produto já tem outro dono semântico (pendência de categorização), e um segundo significado para
a mesma cor desfaz o primeiro. O peso vem da **tipografia e da posição**: a primeira linha é a mais
escura da seção e está **acima de qualquer botão** (aceite 50 da spec 0010, com teste que afirma a
ordem no DOM, não só a presença na tela).

**Por que são três linhas, e não duas** (achado médio do `revisor-seguranca`, 21/09/2026, sobre a
redação original desta spec). A segunda versão dizia `Não vão no texto: saldos, nomes, e-mails…`, e
isso é **falso** exatamente para o conteúdo que domina o export. O sanitizador da importação
(`internal/importer/sanitize`) declara o oposto com todas as letras: sai o que identifica terceiro
por **documento** — CPF, CNPJ, agência, número de conta —, mas **fica o nome da contraparte**, porque
é ele que responde "quem eu paguei". O parser do Inter guarda a descrição como
`Pix enviado - Fulano de Tal` e junta a **mensagem do Pix** — texto que um terceiro escreveu — dentro
dela; e chave Pix por e-mail ou telefone é comum. Ou seja: nome de pessoa **sai** daqui, e é dado de
**terceiro**, não só da casa.

Isso pesa mais nesta tela do que pareceria, porque a mitigação **inteira** da única ameaça nova desta
feature é o consentimento informado — não há controle técnico substituto quando o transporte é a
pessoa. Um aviso que nega a maior categoria de dado pessoal que de fato sai produz consentimento
**desinformado**, que é pior do que nenhum aviso: ele compra tranquilidade com uma afirmação falsa.
Daí o desenho final: a linha 1 dá o peso e a responsabilidade, a **linha 2 diz o que realmente vai**
(a que faltava) e a linha 3 lista o que fica de fora — e é essa terceira que transforma susto em
decisão. Regra para quem escrever avisos deste tipo daqui em diante: **a lista do que não sai é
escrita a partir do código que sanitiza, nunca de memória**, e qualquer mudança no
`internal/importer/sanitize` ou num parser obriga a reler estas três linhas.

**Um texto, uma fonte.** `Copiar` usa `navigator.clipboard.writeText` do **mesmo** texto exibido, e
`Baixar` monta um `Blob` do **mesmo** texto com
`<a download="homefinance-prompt-2026-07-a-2026-09.md">` e `revokeObjectURL` em seguida — nunca uma
segunda requisição, que poderia devolver outro retrato. Falhando a cópia (contexto inseguro,
permissão negada, navegador antigo), o `<details>` **abre sozinho** e o `<output>` diz
`Não consegui copiar. Abra "Ver o texto" e copie à mão.` — e essa mensagem **não** some com o tempo,
porque pede uma ação.

**Busca do prompt:** `useQuery` com `staleTime: Infinity`, `refetchOnWindowFocus: false` e chave
**própria** `['ai','prompt',fromMonth,toMonth]` — fora do prefixo `transactions` do ADR-027, e é
deliberado: esta é a única tela do produto em que **sair do app é o comportamento esperado**, e um
refetch no retorno trocaria o texto que a pessoa acabou de copiar por outro, com outro `generatedAt`.
Carregando: botões com `aria-disabled` (**nunca** `disabled` em lugar nenhum desta tela — ele sai da
navegação por teclado e perde contraste justo quando a pessoa está esperando) e `role="status"` com
`Montando o prompt de julho a setembro…`. Erro:
`Alert tone="error" title="Não foi possível montar o prompt."` mais `Tentar de novo`.

### (f) Cor — esta tela não tem dinheiro

Nenhum centavo aparece em `/ia`. Portanto **`--income`, `--expense` e `--chart-*` não entram em lugar
nenhum** dos arquivos desta feature. Sobram exatamente dois: `--accent` (ação primária, link, anel de
foco, o "Copiado.") e `--danger` (o `Alert` de erro). Teste de aceite: `filter: grayscale(1)` na tela
inteira, e **nenhuma informação se perde**.

**Token novo:** `--font-mono: ui-monospace, "Cascadia Mono", "Segoe UI Mono", "Roboto Mono",
"Liberation Mono", monospace;` em `tokens.css`, ao lado de `--font-ui`. **Uso restrito** ao `<pre>` do
prompt e, na fatia seguinte, ao campo de colar o JSON — os dois lugares em que o texto vai inteiro
para outro programa e a monoespaçada é semântica. Em qualquer outro lugar (rótulo, número, célula de
tabela) é **erro de revisão**: dinheiro se alinha com `tabular-nums`, não trocando de família.

### (g) A seção Importar — o que a E9b concretizou (21/09/2026)

A fatia **E9b** implementa a seção `2 · Importar o que a IA respondeu` em
`features/ai/components/SecaoImportar.tsx` (campo, ações, estados, barra e relatório final) e
`BlocosDaPrevia.tsx` (os três blocos e o `<details>`), com a lógica sem JSX em `features/ai/importacao.ts`
e o componente base novo `components/TextArea/`. A ordem dentro da seção é a da direção:
campo de colar → ações → totais → bloco A → bloco B → bloco C → `<details>` → barra de confirmar.

**O que ficou exatamente como a direção pedia:**

- **`TextArea`**, gêmeo do `TextField` sobre o `FieldShell` (mesmo contrato, sem `className`/`style`),
  `<textarea>` com a mesma `.control` mais `min-block-size: 11rem; resize: vertical;
  font-family: var(--font-mono); font-size: var(--text-13)`, `spellCheck={false}`,
  `autoCapitalize="off"`, `autoCorrect="off"`. **É o segundo e último lugar de `--font-mono`.**
  Rótulo visível `Cole aqui o JSON que a IA respondeu`; dica `Só o JSON — do primeiro { ao último }.
  Se a IA escreveu texto antes ou depois, apague.`. `Conferir` (primary; vazio → `aria-disabled` e
  rótulo `Cole o JSON para conferir`, **nunca `disabled`**) e `Limpar` (quiet, só com texto ou prévia).
- **Totais** num `<p role="status">` (`--text-15`, `--ink`, `tabular-nums`) que carrega também a frase
  de carregamento (`Conferindo o que a IA respondeu…`), a de prévia vazia (`Nada deste JSON pode ser
  aplicado.`) e a da regra de frescor — um nó, uma frase por estado. Os números de `skipped`/`rejected`
  são do servidor; categorias e palavras **descontam o que foi desmarcado** (trade-off assumido: número
  parado seria mentira; é contagem, não centavo — §10.5 da spec).
- **Bloco A** (`Categorias a criar · 3`): só `outcome: created`; checkbox nativo marcado por padrão,
  `aria-label="Criar Alimentação > Padaria com 2 palavras-chave"`; `Desmarcar todas`/`Marcar todas`
  quiet sm; grupo em `--ink-muted` e folha em `--ink`; `Grupo novo · natureza: despesa` só com
  `groupIsNew`; palavras como **texto citado** `«padaria» · «panificadora»` (abaixo de 40rem a coluna
  some e elas viram terceira linha da célula); linha desmarcada em `--ink-muted` por
  `data-desmarcada`; com ≥ 5 categorias o apoio ganha a frase de "a IA costuma criar demais";
  `merged_into_existing` vai para o bloco C com a nota `já existia — as palavras vão para ela`.
- **Bloco B**: `DataTable` agrupada por conta (`RowGroup label="Nubank · 3"`, na ordem do JSON),
  **uma linha por palavra** com o número de `impact.byKeyword` (`«nu pagamentos»` · `87 de 212`,
  `tabular-nums`, `end`, `sr-only` completando "lançamentos do período"), **impacto decrescente dentro
  de cada conta**, limiar `>= 10` **e** `>= 10%` de `totals.periodTransactions` (comparado em inteiros,
  `candidatos × 10 >= universo`) **por linha** — é a palavra genérica que salta, não a conta —, e
  quando dispara: `data-atencao="true"` na linha, `box-shadow: inset 2px 0 0 var(--warning)` na primeira
  célula, `AlertIcon size={14}` em `--warning-ink`, o número em peso 600 e a frase em `--ink` citando a
  palavra: `Palavra genérica: «pagamento» casa com 87 dos 212 lançamentos do período. Para deixá-la de
  fora, apague-a do JSON e confira de novo.` **Único lugar de `--warning` na seção.** Nada desmarcável
  — a saída é a frase. O total do item (`impact.transferCandidates`, a união) **não aparece**: com uma
  palavra repete a linha, com várias é um segundo número que não aponta o problema, e a ação é sempre
  sobre uma palavra. Janela sem lançamento (`periodTransactions: 0`) escreve `sem lançamentos no
  período` no lugar de um `0 de 0` verdadeiro e mudo.
- **Bloco C** é `<dl>` (`dt` com o caminho, `dd` com as palavras citadas), no estilo de
  `ImportResultScreen.module.css`; empilha abaixo de 40rem.
- **`<details>` fechado** `Ver o que não entra · N`, dois `RowGroup` (`Já estavam lá · 6` e
  `Recusadas · 3`, cada um com o apoio da direção), colunas `Palavra` · `Item` · `Motivo`, **nenhum
  controle**. O motivo vem de três `Record` exaustivos em `importacao.ts` (`MOTIVO_DA_RECUSA`,
  `MOTIVO_DO_PULO`, `MOTIVO_DO_DESFECHO`) — motivo novo no contrato é erro de compilação. Para
  `keyword_taken`, `já está em {dono}` resolve o `ownerId` contra `/categories` e `/accounts` já
  carregados (`includeArchived=true`, só pedidos quando existe prévia); sem nome, `já está em outra
  categoria`/`outra conta` — nunca texto do JSON.
- **Barra de confirmar** grudada (`position: sticky`, borda, sem sombra), com `<output aria-live="polite">`
  (`Inclui 2 grupos novos.`, grupo contado **uma** vez mesmo com duas folhas) e o primary cujo rótulo diz a
  saída e recalcula: `Criar 3 categorias e gravar 18 palavras` · `Gravar 18 palavras` · `Criar 2
  categorias` · `Nada para aplicar` + `aria-disabled`. Confirmar manda o **mesmo `payload`** da prévia
  mais `skipNewCategories` = os `ref` das desmarcadas, vindos do relatório.
- **Estados**: vazio sem `EmptyState`; carregando com a frase no status e **uma** `DataTable loading`
  com as colunas do bloco A; JSON inválido/400/413/429 **no campo** (`TextArea error`) com o foco de
  volta ao `<textarea>`, copy por código; rede/500 em `Alert tone="error"` com `Tentar de novo`;
  409 no confirm em `Alert` (`O estado mudou desde a conferência.`) com `Conferir de novo`; sucesso
  vira o relatório do que **de fato** entrou — `<dl>` com `Categorias criadas` · `Palavras gravadas` ·
  `Já estavam lá` · `Recusadas` · `Desmarcadas por você` (zeros não renderizam), frase `role="status"`
  com os números do **confirm**, `Ir para Reprocessar` (foca o `<h2>` da seção 3, `tabIndex={-1}`,
  sem `scrollTo` nem `smooth`) e `Importar outro JSON`. **Sem toast.**
- **Regra de frescor, agora com objeto**: trocar o mês da casca ou o tamanho da janela descarta a
  prévia (o JSON colado fica) e publica `A janela mudou — confira de novo para medir o impacto no novo
  período.`. A seção **não** recebe `key` do pai (a remontagem levaria o texto junto): ela compara a
  chave da janela em que a prévia foi medida com a atual durante a renderização.
- **Cor**: `--accent`, `--warning`/`--warning-ink` (um lugar) e `--danger` (erro). Zero `--income`,
  `--expense`, `--chart-*`. Validado com `filter: grayscale(1)` em navegador real: a linha de atenção
  continua dita pelo filete (cinza), pelo ícone, pela frase e pelo peso do número.

**O que divergiu da direção, e por quê:**

1. **Bloco B: nada divergiu — mas quase.** A primeira versão do contrato media `impact` só por
   **item** (`transferCandidates` do item inteiro), e com esse contrato a tela nasceu por entrada de
   conta, numa tabela plana com coluna `Conta` — a tela não inventa uma medição por palavra que o
   servidor não fez, nem reparte o JSON em uma entrada por palavra (o confirm precisa ser o **mesmo**
   payload). No mesmo dia **o contrato se corrigiu**: `KeywordImportImpact` ganhou `byKeyword` (uma
   entrada por palavra de `added`, na mesma ordem; o total do item é a união e a soma pode passar dele),
   e o bloco voltou ao desenho original — grupo por conta, linha por palavra, atenção por linha. Fica
   registrado como lição: quando a direção e o contrato divergem, a divergência é do **contrato**, e é
   ele que muda.
2. **A tabela de copy dos motivos não existia** no bloco desta spec (a direção citava uma "§10" deste
   bloco que nunca foi escrita); as frases nasceram das §§4.2–4.3 da spec e das descrições do contrato,
   e moram em `importacao.ts`. Este bloco passa a ser a referência: `item_not_found` → `não existe nesta
   casa` · `item_archived` → `está arquivada — desarquive para receber palavras` · `name_mismatch` → `o
   nome que veio no JSON não é o deste item` · `group_has_children` → `é um grupo com subcategorias — a
   palavra vai numa subcategoria` · `invalid_keyword` → `fora do formato de palavra-chave` ·
   `keyword_taken` → `já está em {dono}` · `ambiguous_in_payload` → `aparece em dois itens no mesmo
   JSON` · `limit_exceeded` → `passaria de 20 palavras` · `already_present` → `já estava lá`; desfechos
   de categoria nova: `invalid_name` → `nome fora do formato (1 a 60 caracteres, sem >)` ·
   `kind_required` → `grupo novo sem natureza` · `invalid_kind` → `natureza fora do conjunto (despesa,
   receita, investimento, resgate)` · `kind_mismatch` → `natureza diferente da do grupo` ·
   `name_taken_archived` → `existe arquivada com este nome — desarquive em Categorias` ·
   `household_limit` → `passaria do teto de 200 categorias` · `duplicate_in_payload` → `repetida no
   mesmo JSON — a primeira vale`. Uma categoria nova recusada vira uma linha própria (`Palavra` = o
   caminho, `Item` = `categoria nova`).
3. **O 422 em `fields.toMonth`** (acrescentado ao contrato das duas rotas depois da direção: a janela
   tem descrições demais para a medição, tudo ou nada) ganhou um terceiro lugar de erro — nem no campo
   (o texto não tem culpa) nem com `Tentar de novo` (daria o mesmo 422): `Alert tone="error"` **sem
   ação**, `A janela é grande demais para conferir.` / `…para aplicar.`, com a frase `Escolha um
   período menor no alto da página e confira de novo.` — e o aviso some sozinho quando a janela muda,
   porque a ação era essa.
4. **Abaixo de 40rem** duas colunas somem e o dado reaparece na célula ao lado (o mesmo mecanismo da
   revisão da importação): no bloco B a coluna do impacto vira a linha `87 de 212 candidatos a
   transferência` sob a palavra, para a frase de atenção ter a largura da moldura; no `<details>` a
   coluna `Item` vira a linha sob a palavra. Medido a 375px: sem isso a frase de atenção virava uma
   torre de duas palavras por linha.
5. **A barra de confirmar fica acima da barra de navegação do celular** (`inset-block-end:
   var(--nav-bar-h)`, como o `Toast`), e não em `0` como a da revisão da importação — em `0` a barra
   fixa do celular a cobriria.
6. **O esqueleto de carregamento anuncia duas vezes**: o `role="status"` dos totais diz `Conferindo…`
   e a `DataTable loading` traz o próprio `Carregando categorias a criar` (`sr-only`). É o
   comportamento do componente base, mantido de propósito; trocar isso é decisão do `DataTable`, não
   desta tela.
7. **Sobre o exemplo "24 de 212 não dispara"** do pedido de testes: 24 de 212 são 11,3% — pela regra
   normativa (`>= 10` e `>= 10%`, §10.1 da spec e contrato) **dispara**, e o teste afirma isso. O caso
   "nubank legítimo que não grita" é coberto como 24 de 500 (4,8%); as fronteiras testadas são 22 de
   212 (dispara) e 21 de 212 (não), mais 10 de 212 (não, pela proporção) e 9 de 20 (não, pelo piso).
   A regra venceu o exemplo, e o exemplo estava errado por aritmética.
8. **Chaves por índice, nunca por `id`.** O contrato devolve `KeywordImportItem.id` como `""` quando o
   id do JSON não tinha forma de uuid (a entrada vem `item_not_found`), então duas entradas assim
   colidiriam numa `key` por id; os blocos B e C e o `<details>` chaveiam pela posição no relatório,
   que é a mesma do payload. E `KeywordImportRejectedKeyword.keyword` é texto livre (neutralizado e
   truncado em 40 pelo servidor — em `invalid_keyword` vem a forma bruta): entra na tela só como texto
   citado, que o React escapa.

9. **O teto de 128 KiB é conferido no cliente, em bytes UTF-8, antes de qualquer pedido** (achado B2
   do `qa-testes`, 21/09/2026). Direto na API um corpo maior é 413; pelo proxy do Vite chega **502**
   (`ECONNRESET`: o servidor responde e fecha sem drenar o corpo), e um reverse proxy em produção tende
   a fazer o mesmo — e 502 cairia em "Tentar de novo" para sempre. `lerJsonColado` mede o texto colado
   com `TextEncoder` (nunca `length`, que conta UTF-16 e mente em qualquer acento) contra o mesmo
   `MAX_BYTES_DO_JSON = 131.072` de `aiimport.MaxPayloadBytes`; e os dois envios medem o **envelope
   inteiro** (`corpoCabe`), porque no confirm os `ref` de `skipNewCategories` somam ao corpo e uma prévia
   que coube pode virar um confirm que não cabe. Estourou: o erro mora no campo, com a frase do 413, e
   nenhuma requisição sai. O 413 continua mapeado (defesa em profundidade); 502/503/504 ganharam a frase
   `O servidor recusou o envio antes de ler tudo — o JSON pode estar grande demais, ou a API está fora
   do ar.` na seção, com "Tentar de novo" (que é a ação certa quando a API só caiu).

**Validado contra a pilha real (21/09/2026)** com um E2E temporário (API Go + SQLite + Vite, casa do
projeto `setup`, JSON de verdade com duas categorias novas num grupo novo, uma folha da semente e uma
conta criada na hora): prévia `2 categorias novas · 6 palavras entram.`; bloco B `IA QA Inter · 2` com
`«pix»` e `«ia qa inter»`; desmarcar uma categoria levou o botão de `Criar 2 categorias e gravar 6
palavras` a `Criar 1 categoria e gravar 5 palavras`; o confirm devolveu `1 categoria criada e 5
palavras gravadas.` e o banco confirmou — grupo sem palavra, folha com as duas, a desmarcada
inexistente, a conta com as suas; reimportar o mesmo JSON deu `1 categoria nova · 1 palavra entra · 5
já estavam lá.`; e os três 400 do servidor (campo desconhecido, sem versão, listas vazias) caíram cada
um na sua frase, no campo. O que a validação mostrou e virou ajuste: a casa de teste não tinha
lançamento na janela e o bloco B dizia `0 de 0` — daí a frase `sem lançamentos no período`.

### (h) A seção Reprocessar — o que a E9c concretizou (21/09/2026)

A fatia **E9c** implementa a seção `3 · Reprocessar` em `features/ai/components/SecaoReprocessar.tsx`,
com a lógica sem JSX em `features/ai/reprocessamento.ts` e as duas chamadas em
`features/ai/api/reprocessamento.ts`. **Não há backend novo**: a seção orquestra `POST /transfers/detect`
e `POST /transactions/auto-categorize`, que já existiam com prévia, transação, auditoria, rate limit e
409 próprios (spec 0010 §5). As chamadas são declaradas nesta feature — import entre features é
proibido — e conferem o **eco do mês** da resposta (as três condições do `EchoMismatchError`, ADR-030,
valem: `month` é `required` na resposta, a linha da tabela afirma o mês, e "1 par" de agosto na linha de
julho seria um registro falso do que foi gravado).

**A ordem — refinamento da §5.2, e o motivo.** A §5.1 fixa *transferências → categorização*, porque
converter um par zera o `categoryId` das duas pernas (ADR-028). A §5.2 diz "mês a mês, cada mês na
ordem" — lido ao pé da letra, detectar julho, categorizar julho, detectar agosto… Mas a detecção
procura o espelho a **±3 dias**, e três dias cruzam a fronteira do mês: categorizada julho, a detecção
de agosto pode fechar um par com uma perna em 31/07 e zerar a categoria de julho **depois** de ela ter
sido reportada como gravada. O resultado final estaria certo; o relatório de julho, não. A seção roda
em **duas fases, cada uma mês a mês**: fase 1 = `detect` em todos os meses da janela; fase 2 =
`auto-categorize` em todos. É a leitura fiel ao princípio da §5.1 — nenhuma categorização acontece
antes de **toda** conversão — e elimina a borda. A frase da ordem na tela diz isso:
`Primeiro as transferências, em todos os meses; depois a categorização. Converter um par apaga a
categoria das duas pernas — categorizar antes seria trabalho perdido.` (`planoDeExecucao`, com teste
que afirma a sequência).

**O que ficou exatamente como a direção pedia:**

- **Ocioso:** apoio `Palavra-chave nova não mexe sozinha no que já está gravado. Aqui ela passa a
  valer.`; a frase dos meses (`Julho, agosto e setembro.`, por `enumerarMeses` sobre `janelaDeTrabalho` —
  nenhuma seção recalcula data); a frase da ordem; `Button variant="secondary"` **`Conferir`**.
- **Conferindo:** `role="status"` `Conferindo julho, agosto e setembro…` + `DataTable loading` com as
  colunas da prévia. Seis chamadas com `dryRun: true`, **em paralelo** (as de um mês e as das duas
  fases): `dryRun` não escreve, então nem a ordem entre fases nem entre meses importa aqui. Uma falha
  derruba a prévia inteira — um consolidado de cinco sextos mentiria.
- **Prévia pronta:** frase consolidada `3 pares de transferência · 42 lançamentos categorizados · 12
  seguem sem categoria.` — **somada no cliente**, legítimo porque são contagens de linhas e não
  centavos (ADR-036(e)); `DataTable` por mês com `Mês` · `Pares` · `Categorizados` · `Sem categoria`
  (`end`, `tabular-nums`; o mês por extenso **com o ano** — `Novembro de 2025` — porque a janela
  atravessa a virada); `<details>` fechado `Ver os N lançamentos sem par e o motivo` com a copy do
  `DetectTransfersDialog` (`sem a outra perna gravada` e a explicação, uma vez, acima da lista),
  agrupado por mês, linha de corte no teto de 500; `Button variant="primary"` **`Reprocessar julho,
  agosto e setembro`** + `Conferir de novo` (quiet). Zero em tudo: `Nada a reprocessar — não há par
  para reconhecer nem lançamento sem categoria.` e o botão `Nada a reprocessar` com `aria-disabled`
  (**nunca `disabled`**; o clique morre no guarda).
- **Executando:** a tabela vira `Mês` · `Transferências` · `Categorização`, **uma célula por etapa,
  estado por palavra** (`na fila` · `em andamento…` · `feito`), tinta e peso só como reforço.
  Progresso num `<output aria-live="polite">` que nasce **vazio** no instante em que a execução começa
  e recebe **uma frase curta por chamada concluída** (`Julho: 1 par.` / `Julho: 12 categorizados.`),
  cada uma uma ADIÇÃO — que é o que o leitor de tela anuncia — e que fica na tela como rastro.
  Estritamente sequencial, `await` a `await`, `dryRun: false`, fase 1 inteira antes da fase 2, **para
  no primeiro erro**.
- **409 no meio — para tudo.** Nenhuma chamada depois do conflito (teste conta as chamadas). `Alert
  tone="error" title="O estado mudou no meio do reprocessamento."`, corpo montado por `fraseDoQueFicou`
  mês a mês e etapa a etapa: `As transferências de julho, agosto e setembro foram aplicadas e continuam
  aplicadas. A categorização de julho também. A de agosto não foi, e a de setembro não chegou a rodar.
  Confira de novo antes de seguir.` — mais a linha `Na prévia nova, o que já foi aplicado volta com 0 —
  é a prova de que está feito.`, porque esse 0 é idempotência (§5.6), não falha. Ação única: `Conferir
  de novo`. A tabela permanece como registro: `feito` / `não aplicado` / `não chegou a rodar`. Nenhum
  `Reprocessar`, nenhum `Tentar de novo` — repetir sobre uma prévia velha seria gravar às cegas.
- **Outros erros:** mesma parada. 429 → `O reprocessamento parou.` + `Muitas operações seguidas. Espere
  um minuto e confira de novo.`; 422 em `fields.month` → `Um dos meses tem lançamentos demais para
  reprocessar de uma vez. Escolha um período menor no alto da página e confira de novo.`; rede/500 →
  a frase única do app. Na prévia, `Não foi possível conferir.` com `Tentar de novo` — **exceto** 429 e
  422, cuja ação não é repetir (esperar; encurtar a janela).
- **Sucesso:** `<dl>` do total **das respostas de execução** (nunca da prévia; zeros não renderizam),
  `TextLink` `Ver as transferências` (`/transferencias?mes=`) e `Ver os lançamentos`
  (`/lancamentos?mes=`) no **último** mês da janela, e `Conferir de novo`. A frase final
  (`Reprocessado — 3 pares de transferência e 40 lançamentos categorizados.`) entra no mesmo
  `<output>` para ser **anunciada**, mas fica `sr-only` quando o `<dl>` já diz os números — um
  resultado dito uma vez na tela. **Sem toast.** Idempotente: execução que devolve 0 e 0 diz, visível,
  `Nada mudou — não havia par para reconhecer nem lançamento sem categoria.`, sem `<dl>`.
- **Regra de frescor:** o mesmo mecanismo e a **mesma frase** da seção 2 (`FRASE_JANELA_MUDOU`, agora
  em `features/ai/secoes.ts`, importada pelas duas): comparação de chave durante a renderização, sem
  `key` no pai. Trocar o mês ou o tamanho da janela descarta a prévia e o resultado.
- **Cor:** só `--accent` e `--danger`. Zero `--income`/`--expense`/`--chart-*`. Nenhum `disabled` na
  seção. O `<h2>` tem `id="ia-secao-reprocessar"`, `tabIndex={-1}` e anel de foco só em
  `:focus-visible` — é onde "Ir para Reprocessar" chega. Nenhuma animação nova: o único movimento é
  o `Spinner` do botão, que já respeita `prefers-reduced-motion`.
- **Ao terminar** (sucesso ou parada), a seção invalida `transfers`, `transactions` e `accounts` — o que
  foi aplicado ficou aplicado. O prompt da seção 1 **não** é invalidado: ele é um retrato, e
  reescrevê-lo pelas costas de quem copia é o que a E9a proibiu.

**O que divergiu da direção, e por quê:**

1. **As ações fecham a seção.** A revisão em navegador real mostrou o `Reprocessar` acima da frase
   consolidada e da tabela, lendo como cabeçalho; ele é o **próximo passo** depois de ler os números,
   então a linha de ações (`Reprocessar…` + `Conferir de novo`, ou só `Conferir`) é o último bloco da
   seção em todos os estados. Na parada não há linha de ações: a única saída é o `Conferir de novo` do
   próprio aviso.
2. **A tabela de execução ganhou o mês na primeira coluna, e o estado nas duas seguintes** — não "a
   coluna Mês vira estado". Uma célula por etapa exige que o mês continue nomeado ao lado, senão a
   linha `feito · não aplicado` não diz de quem é. É o desenho literal de "uma célula por etapa".
3. **O `<output>` acumula, em vez de trocar a frase.** A direção dizia "uma frase curta por chamada
   concluída"; cada frase é adicionada como nó novo, então o leitor de tela anuncia exatamente uma
   por chamada (o padrão de `aria-live` sem `aria-atomic` anuncia adições), e quem enxerga fica com o
   rastro — que é o registro que a parada no meio precisa deixar. A frase final entra no mesmo
   `<output>`: nenhuma segunda live region muda ao mesmo tempo.
4. **Um estado a mais, `sem resposta`.** `não aplicado` só é verdade quando o servidor **respondeu**
   com erro (as duas rotas são uma transação; erro é rollback). Rede caída no meio não diz nada: o
   pedido pode ter chegado e sido gravado. Afirmar "não aplicado" seria chutar, então a célula e a
   frase dizem `ficou sem resposta`, e a prévia nova resolve a dúvida (o 0 de idempotência).
5. **Uma nota abaixo da tabela quando pares e categorizados são ambos > 0:** `A categorização foi
   medida antes das transferências: uma perna de par que também bateria com uma palavra-chave de
   categoria vira transferência e não é categorizada. O total real é o da execução.` As duas prévias
   são medidas sobre o mesmo estado e podem se sobrepor numa perna (aceite 39); sem a nota, "42" na
   prévia e "40" na execução pareceriam erro. Só aparece quando a sobreposição é possível.
6. **A lista dos sem par não mostra valor.** A premissa da tela (f) — `Nenhum centavo aparece em /ia`
   — continua verdadeira: data, `saiu de Nubank`/`entrou em Inter` e descrição dizem qual extrato falta
   importar, que é a única ação possível aqui; o valor mora no diálogo de `/transferencias`.
7. **Execução em curso e execução parada não são descartadas pela regra de frescor.** A primeira está
   gravando (abandonar o estado não cancela as transações no servidor); a segunda é o registro de uma
   aplicação pela metade que a pessoa precisa ler antes de seguir — sumir com ele esconderia o
   problema. Prévia e resultado concluído continuam sendo descartados, como a regra manda.
8. **`Conferir de novo` existe também na prévia e no sucesso**, e não só no 409: quem importou
   palavras depois de conferir precisa medir de novo sem trocar a janela ida e volta.
9. **A prévia-frase de "nada a reprocessar" é honesta sobre os que seguem sem categoria:** com 12
   lançamentos sem categoria que nenhuma palavra alcança, ela diz `…e nenhuma palavra-chave bate com
   os 12 lançamentos sem categoria.` em vez de negar que existem.

### Checklist anti-cara-de-IA — spec 0010 (aplicar com a tela pronta)

1. A barra inferior continua com **sete** células, e os rótulos continuam visíveis a 360, 375, 390 e
   393 px. Nenhum item novo entrou nela.
2. O ícone é o `PromptIcon` deste projeto: sem robô, sem varinha, sem faísca, sem cérebro, sem balão
   de fala, sem emoji e sem biblioteca de ícones.
3. O aviso é **prosa acima dos botões** — sem caixa amarela, sem ícone de alerta, sem `--warning`.
   E são **três** linhas: a segunda diz que o nome de quem pagou ou recebeu e a mensagem do Pix vão
   junto. Um aviso que diga "não vão nomes" é defeito de segurança, não de texto.
4. As estatísticas são **uma linha de texto**. Nenhum cartão com número gigante, nenhuma barra de
   progresso, nenhum medidor, nenhum ícone ao lado de contagem.
5. Os números das seções são **texto no `<h2>`**. Nenhuma bolinha numerada, nenhuma linha ligando
   passos, nenhum `ImportStepper`.
6. O `<pre>` é **papel**: fundo `--surface-sunken`, tinta `--ink`, zero cor de sintaxe, zero tema
   escuro de editor, zero numeração de linha na margem.
7. `grayscale(1)` não muda nada: `--income`, `--expense` e `--chart-*` não aparecem nos CSS desta
   feature.
8. `--font-mono` aparece **só** no `<pre>` do prompt e no `<textarea>` de colar o JSON (E9b) — os dois
   lugares em que o texto vai inteiro para outro programa. Em mais nenhum.
9. Nenhum `disabled` na tela inteira — o estado indisponível é `aria-disabled`, com o clique barrado
   no guarda da função.
10. Nenhuma seção promete botão que não funciona: as que ainda não existem dizem `Ainda não está no
    ar.` e não têm controle nenhum.
11. Nenhuma sombra: as três seções e a faixa se separam por borda de 1px, como todo o resto.
12. Copy em pt-BR, sentença normal, sem exclamação e sem emoji. Palavras proibidas nesta tela:
    "mágica", "inteligente", "poderoso", "insights", "com IA" como adjetivo de funcionalidade, e
    qualquer promessa de que o app "analisa" ou "entende" os lançamentos — ele monta um texto.
13. Um pedido de rede monta a seção Exportar, e ele não é refeito ao voltar para a aba.
14. Zero medida, cor ou raio fora dos tokens em `AiScreen.module.css`, `SecaoExportar.module.css`,
    `SecaoImportar.module.css`, `BlocosDaPrevia.module.css` e `TextArea.module.css`.
15. **(E9b)** As palavras-chave da prévia são **texto citado** (`«padaria» · «panificadora»`), nunca
    fichas: ficha é a linguagem de editar, e na prévia nada se edita.
16. **(E9b)** O impacto é **número como texto** (`87 de 212`); nenhuma barra de risco, anel, semáforo
    ou "score". `--warning` aparece em **um** lugar — o filete e o ícone da linha de atenção — e em
    `grayscale(1)` a linha continua dita pela frase e pelo peso do número.
17. **(E9b)** O erro do JSON mora **no campo** (`TextArea error`, foco de volta ao `<textarea>`) — nunca
    toast, nunca `Alert` no topo, nunca eco do texto colado, nunca `notes` renderizado.
18. **(E9b)** Checkbox **nativo** no bloco A, nunca `role="checkbox"`; `<details>` do que fica de fora
    **sem nenhum controle**, nem desabilitado; o `<dl>` do bloco C não é tabela.
19. **(E9b)** O sucesso é o **relatório na própria seção**, com os números do confirm — sem toast, sem
    ilustração, sem "pronto!".
