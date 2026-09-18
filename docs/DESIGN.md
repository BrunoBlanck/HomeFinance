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

- **Dashboard do mês:** o "resumo do caderno" — saldo do mês, entradas vs saídas, próximos vencimentos, orçamentos estourando. Tabela e números, não mar de cards.
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
  categorizada (o nome em texto — recategorizar é E2b).
- **Celular (< 40rem)**: a coluna Categoria some (`hideBelow: 'sm'`) e a `.secundaria` da descrição
  passa de `Nubank · Sem categoria` para `Nubank ·` + **o mesmo botão** (a `.secundaria` vira
  `display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1)`). São duas instâncias
  no DOM, uma por faixa — exatamente uma visível em qualquer largura, o mecanismo que hoje já
  duplica o nome da conta. Ids distintos (`atalho-categoria-{id}-coluna` / `-secundaria`) e
  `data-atalho={id}` nas duas; quem devolve o foco procura `[data-atalho="{id}"]` e escolhe a
  instância com `checkVisibility()`.
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
Categorizar {rotuloDaLinha}</legend>`; `display: flex; flex-wrap: wrap; align-items: center;
gap: var(--space-2) var(--space-4); min-inline-size: 0; border: 0; padding: 0; margin: 0`.
Filhos, na ordem do DOM — que é a ordem do Tab:

1. **`Select`** `density="compact" labelHidden label="Categoria"
   placeholder="Escolha a categoria" aria-label="Categoria de {rotuloDaLinha}"` com
   `opcoesDeCategoria(arvore, linha.kind)` — só da natureza da linha, e só ativas (a árvore de
   `categoriasQueryOptions(false)` já vem sem arquivadas). `max-inline-size: 20rem` + ellipsis no
   `<select>`, como em `.decisao`. **Recebe o foco ao abrir** (`useEffect` no `editandoId`).
   Placeholder `Escolha a categoria`, e não `Sem categoria`: aqui a opção vazia não é uma decisão,
   é "ainda falta" — o imperativo continua significando "falta escolher", como em `Escolha a conta`.
2. **Fichas de aprender** — só com categoria escolhida (regra de (d)); `key={categoriaId}`, para
   trocar a categoria soltar a ficha pressionada (a palavra pode já ser da outra). Mesma anatomia
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

Léxico: a palavra sempre entre aspas angulares; o mês por extenso e em minúsculas (`setembro`);
o estado é a palavra `Sem categoria`, nunca cor; nenhum "sugerido", "inteligente" ou "automágico".
A tabela (g) continua valendo para tudo o que este atalho reaproveita (409, rótulo das fichas).
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
    visível); depois de gravar, vai para a próxima lacuna da tabela.
22. Os números do toast são os do servidor (`categorized` de (c)) — nunca a contagem das linhas
    carregadas na tela.
23. Clique fora não fecha; só uma linha aberta por vez; trocar mês ou filtro fecha.
24. A 375 px: uma única instância visível do botão (na `.secundaria`), editor empilhado, página
    sem rolagem horizontal (conferir o `colSpan` com as colunas escondidas).
25. `filter: grayscale(1)`: a lacuna (tracejado), a ficha pressionada (ícone) e a linha aberta
    (chevron virado + editor) continuam legíveis.

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
`Select density="compact" label="Natureza"` com `Despesas` / `Receitas` — **sem placeholder**
(sempre há valor); `?natureza=despesas|receitas`, ausente = despesas, validado em `search.ts` e
mapeado para `kind=expense|income` na API. À direita `.resumo` (o mesmo estilo de `.saldo`):
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
| `document.title` | `Gastos por categoria · HomeFinance` / `Receitas por categoria · HomeFinance` |
| `<h1>` | `Gastos por categoria` / `Receitas por categoria` |
| Apoio | `Para onde foi o dinheiro em setembro.` / `De onde veio o dinheiro em setembro.` |
| Filtro | `Natureza` — opções `Despesas` · `Receitas` |
| Resumo da faixa | `R$ 5.123,45 em 87 lançamentos` / `R$ 45,00 em 1 lançamento` |
| Centro da rosca | `5.123,45` + `setembro` (`sr-only` em volta: `Total de … em setembro`) |
| `<title>` da fatia | `Alimentação · R$ 2.100,00 · 41,2%` · `Sem categoria · R$ 300,00 · 5,9%` · `Outras (6 categorias) · R$ 630,00 · 12,3%` |
| Fatia dobrada | `Outras (6 categorias)` / `Outra (1 categoria)` |
| Legenda (`li`) | `Alimentação` `41,2%` |
| `<figcaption>` | `Distribuição por categoria — os valores estão na tabela abaixo.` |
| `caption` da tabela | `Gastos por categoria em setembro de 2026` / `Receitas por categoria em setembro de 2026` |
| Colunas | `Categoria` · `Lançamentos` · `Participação` · `Valor` |
| Subcategoria (`sr-only`) | `em Alimentação: ` |
| Linha direta | `Sem subcategoria` |
| Arquivada | `Alimentação (arquivada)` |
| Sem categoria | `Sem categoria` · link `Categorizar` (`aria-label` `Categorizar os lançamentos sem categoria de setembro`) |
| Rodapé | `Total` · `87` · `100,00%` · `5.123,45` |
| Secundária no celular | `12 lançamentos` / `1 lançamento` |
| Participação | tabela `41,23%` · legenda e `<title>` `41,2%` |
| Vazio | `Nenhuma despesa em setembro de 2026.` / `Nenhuma receita em setembro de 2026.` — `O relatório aparece assim que houver lançamentos no mês — registre um em Lançamentos ou importe o extrato.` — `Importar extrato` |
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
16. Copy da tabela (i), sem "poderoso", "inteligente", "visão 360°"; `Natureza` com duas opções
    e sem placeholder.
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

O `Natureza` de `/relatorios/categorias` continua com **duas** opções e isso não é um esquecimento:
investimento não é um `kind` a mais do relatório, é a tela própria (spec 0006 §4). Ninguém
"conserta" aquele filtro acrescentando duas opções.

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
10. Zero número derivado: sem líquido, sem saldo investido, sem "% do mês anterior", sem seta de
    tendência, sem projeção.
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
