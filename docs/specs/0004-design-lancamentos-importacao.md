# 0004 — Direção visual: lançamentos e importação de extratos (E2)

**Status:** normativo · **Data:** 16/09/2026 · **Dono:** agente `designer-ui` ·
**Executor:** agente `dev-frontend-react`

Este documento é a **direção visual normativa da entrega E2** para as telas `/lancamentos` e
`/importar`. Ele **complementa `docs/DESIGN.md` e não o substitui**: identidade, tokens, lista de
rejeição e regras de componente continuam valendo integralmente e vencem qualquer coisa escrita
aqui em caso de conflito. Quando este documento pede algo que os tokens não cobrem, o certo é
voltar ao `designer-ui` — não inventar valor.

A seção de padrões que nasceu desta direção (marcação por palavra, léxico de conciliação, blocos de
decisão, fluxo em passos, sinal do dinheiro, faixa de pendência, entrada de arquivo) já foi gravada
no fim de `docs/DESIGN.md` e por isso **não se repete aqui** — é o "§8" ausente na numeração.

---

## Duas coisas que se perdem numa implementação apressada

### A. A armadilha do fuso na data civil

`new Date('2026-08-31')` é interpretado como **meia-noite UTC**. Formatado no fuso da casa
(`America/Sao_Paulo`, UTC−3), isso vira **30 de agosto**. O cabeçalho de dia da tela de lançamentos
mostraria o dia errado, e o agrupamento por dia jogaria a linha no grupo errado.

**Regra, sem exceção:** data civil (`AAAA-MM-DD`) nunca passa pelo construtor de `Date` com string.
Monte a partir das partes e formate em UTC.

```ts
// CERTO
const [ano, mes, dia] = civil.split('-').map(Number)
const rotulo = new Intl.DateTimeFormat('pt-BR', {
  weekday: 'long', day: 'numeric', month: 'long', timeZone: 'UTC',
}).format(new Date(Date.UTC(ano, mes - 1, dia)))

// ERRADO — desloca o dia
new Intl.DateTimeFormat('pt-BR', { /* ... */ }).format(new Date(civil))
```

Vale para: cabeçalho de dia (`/lancamentos`), coluna Data do passo 2, datas de evidência
("já importada em 05/09"), descrição dos diálogos de exclusão e o período do arquivo
("01/08 a 31/08"). **Merece teste** com o processo em UTC e em `America/Sao_Paulo`.

### B. Pendências que dependem de outras pessoas — verificar antes de começar

Estes quatro itens **bloqueiam** partes da direção. Não são notas de rodapé: são verificação de
entrada. Se um deles não estiver pronto, a parte correspondente não é implementada "por
aproximação" — o `arquiteto` decide o que fazer.

- [ ] **P1 · `Account.institution` no `openapi.yaml`.** Hoje o schema `Account` (linha ~1012) não
      tem instituição. O `Select` de conta do passo 1 depende dela para agrupar em `optgroup`
      ("Nubank › Conta corrente"). Responsável: `arquiteto` / `dev-backend-go`.
- [ ] **P2 · `uncategorizedCount` no `summary` de `GET /transactions`.** A faixa de pendência
      ("12 lançamentos de agosto estão sem categoria") é sobre o **mês inteiro**, não sobre a página
      carregada — contar no cliente daria um número errado assim que houver paginação.
      Responsável: `arquiteto` / `dev-backend-go`.
- [ ] **P3 · Descarte do arquivo enviado ao cancelar ou concluir a importação.** O rodapé do passo 1
      **promete isso ao usuário em texto**: "O arquivo fica no servidor só até você concluir ou
      cancelar esta importação." Promessa de UI que o backend não honra é mentira na interface — ou
      o backend descarta, ou o texto muda. Responsável: `dev-backend-go` / `revisor-seguranca`.
- [ ] **P4 · `validateSearch` do router aceitando `conta` e `semCategoria`.** Com **o mesmo rigor**
      já aplicado a `mes` em `frontend/src/app/router.tsx`: `conta` só passa se for UUID válido,
      `semCategoria` só passa se for exatamente `'1'`; qualquer outra coisa some da busca. A URL é
      editável pela pessoa e nada não validado pode chegar a uma query. Responsável:
      `dev-frontend-react` (é trabalho desta entrega, mas acontece antes das telas).
- [ ] **P5 · `investedCents`/`redeemedCents` do `summary` não respondem ao `kindGroup`** (emenda
      E2d, 18/09/2026). Eles seguem respondendo a `month` e `accountId` e **ignoram** o filtro de
      tipo; do contrário vão a zero justamente sob `?tipo=despesas`, e a 2ª linha da faixa
      ("Fora destes números: 2.000,00 em aportes") some — contra a exigência da spec 0006 §3.5.2 e
      contra o que o próprio schema manda. `uncategorizedCount` e `count`, ao contrário, **seguem**
      o `kindGroup`. A degradação aceita (a tela não fica bloqueada) e o texto completo da
      exigência estão em `docs/DESIGN.md` E2d (j). Responsável: `arquiteto` / `dev-backend-go`.

Premissa adicional assumida pela direção: a análise de importação é um recurso com id opaco
(`/importar/{importId}/revisar`). Id de recurso em URL é o mesmo padrão de `/contas/{id}` e **não**
é dado sensível — a regra da linha 106 de `docs/DESIGN.md` fala de e-mail e código de verificação.
Isso permite recarregar a página no meio da revisão sem perder o trabalho.

---

> **Correção de 16/09/2026 — OFX sai do texto.** A primeira versão desta direção mandava
> aceitar `.ofx` e prometia "OFX do banco" ao usuário. A spec funcional
> `0004-lancamentos-e-importacao.md` §1 diz **"OFX → fora"**, e não existe parser de OFX no
> backend: o seletor aceitaria o arquivo e a API devolveria "formato não reconhecido".
> Promessa de interface que o sistema não honra é mentira na interface — a mesma regra que
> esta direção aplica ao descarte do arquivo (P3). O limite publicado também estava errado:
> é **8 MB** (`importer.MaxUploadBytes`), não 10 MB.

## 1. Tela `/lancamentos`

### 1.1 Layout

```
página  max-inline-size: 62rem  ·  gap --space-4  (mesmo ritmo de /contas)

┌───────────────────────────────────────────────────────────────────────────────┐
│ h1 Lançamentos                        [Importar extrato]  [Novo lançamento]   │  ← h1 Fraunces --text-28/600
│ p  Tudo o que entrou e saiu em agosto.                                        │     apoio Public Sans --text-15 --ink-muted
├───────────────────────────────────────────────────────────────────────────────┤
│ ▌12 lançamentos de agosto estão sem categoria. Sem categoria eles não         │  ← Alert tone="warning"
│ ▌entram em nenhum orçamento nem relatório.                                    │     (some sozinha quando zera)
│ ▌                                    [Ver só esses 12]                        │
├───────────────────────────────────────────────────────────────────────────────┤
│ Panel padding="none"                                                          │
│ ┌───────────────────────────────────────────────────────────────────────────┐ │
│ │ Conta                              Entrou 4.250,00 · Saiu 3.918,44 ·      │ │  ← faixa de filtro+resumo
│ │ [Todas as contas          ▾]       Resultado +331,56                      │ │     bg --surface-sunken
│ ├───────────────────────────────────────────────────────────────────────────┤ │
│ │ CONTA           CATEGORIA        DESCRIÇÃO                        VALOR    │ │  ← th sticky, --text-13/600
│ ├───────────────────────────────────────────────────────────────────────────┤ │
│ │ segunda, 31 de agosto                                        −5.029,00    │ │  ← grupo: th scope="rowgroup"
│ │ Conta corrente  ·Transferência·  Pix para Cartão C6      −5.000,00    ⋯   │ │
│ │ Conta corrente  Alimentação      Padaria Exemplo            −29,00    ⋯   │ │
│ ├───────────────────────────────────────────────────────────────────────────┤ │
│ │ quinta, 27 de agosto  · 1 transferência                       −139,92     │ │
│ │ Conta corrente  ·Sem categoria·  Posto Exemplo              −139,92   ⋯   │ │
│ ├───────────────────────────────────────────────────────────────────────────┤ │
│ │ Mostrando 50 de 214 lançamentos          [Carregar mais 50]               │ │
│ └───────────────────────────────────────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────────────────┘
```

### 1.2 Hierarquia e densidade — valores fechados

| Elemento | Decisão |
|---|---|
| Linha de dado | `padding: var(--space-3) var(--space-4)` (o padrão do `DataTable`, ≈44px). **Não se cria densidade nova**: 4px de ganho não paga a divergência com `/contas`. |
| Cabeçalho de dia | `padding: var(--space-2) var(--space-4)`, `background: var(--surface-sunken)`, rótulo em `--font-ui` `--text-13`/600 `--ink`, `letter-spacing: .01em`. Não é sticky (brigaria com o `th` sticky da tabela). |
| Rótulo do dia | `Intl.DateTimeFormat('pt-BR', {weekday:'long', day:'numeric', month:'long', timeZone:'UTC'})` → "segunda, 31 de agosto". Ver a armadilha A no topo deste documento. |
| Subtotal do dia | `MoneyText tone="semantic" sign="always"`, precedido de `<span className="sr-only">Subtotal do dia </span>`. |
| Coluna Valor | `align:'end'`, `width:'min'`, `MoneyText sign="always"` → `+1.600,00` / `−11,00`. |
| Transferência | `Badge` neutro **"Transferência"** na coluna Categoria; valor com `tone="neutral"` — transferência não é receita nem despesa, e pintá-la gastaria o significado de `--income`/`--expense` (princípio 3 do `docs/DESIGN.md`). |
| Sem categoria | `Badge tone="muted"` (borda tracejada, zero cor cromática) com o texto **"Sem categoria"**. A dívida fica visível linha a linha, não só na faixa. |
| Subtotal x transferência | O subtotal **conta só receita e despesa**. Quando o dia tem transferência, o cabeçalho acrescenta `· 1 transferência` (`--ink-muted`) — é a explicação de por que as linhas visíveis não somam o subtotal. |
| Entrou / Saiu | Neutros e sem sinal: a palavra já diz a direção. **Resultado** é `tone="semantic" sign="always"` — é o único número ambíguo da barra. |
| Larguras | conta `min` · categoria `min` · descrição `auto` (trunca com ellipsis + `title`) · valor `min` · ações `min`. |
| Abaixo de 40rem | Colunas conta e categoria com `hideBelow:'sm'`; a célula de descrição ganha 2ª linha `--text-13 --ink-muted` "Conta corrente · Alimentação", escondida acima de 40rem pelo CSS da própria tela. |

### 1.3 Faixa "sem categoria" (a dívida que a importação cria)

Componente: **`Alert tone="warning"` reusado como está**, acima do `Panel`, na largura da página.
Ela é a ponte entre as duas telas desta entrega: a importação cria a dívida, a faixa cobra.

- **Ação**: `Button size="sm"` que grava `?semCategoria=1` na URL — a faixa **é** o filtro, não um
  link para outro lugar.
- **Com o filtro ativo**, a faixa vira `Alert tone="info"` (role `status`, sem interromper) com a
  saída de volta.
- **Some quando zera.** Nunca renderiza com contagem 0 e nunca é dispensável por "X": dívida
  dispensada é dívida invisível.
- Vive **fora** do `Panel` (e não dentro) porque fala do **mês inteiro**, não da página carregada da
  tabela.

Textos prontos:

```
Plural:   12 lançamentos de agosto estão sem categoria. Sem categoria eles não entram em
          nenhum orçamento nem relatório.
Ação:     Ver só esses 12

Singular: 1 lançamento de agosto está sem categoria. Sem categoria ele não entra em nenhum
          orçamento nem relatório.
Ação:     Ver esse lançamento

Filtro ativo (tone="info"):
          Mostrando só os lançamentos sem categoria de agosto.
Ação:     Mostrar todos os lançamentos
```

### 1.4 Estados

| Estado | Direção |
|---|---|
| **Carregando (1ª página)** | `DataTable loading` (esqueleto com a mesma estrutura, já implementado). A barra de filtro renderiza **funcional**; os três números do resumo viram `Skeleton width="4.5rem" height="1rem"`. A faixa de pendência **não** renderiza — não se inventa contagem. |
| **Vazio, sem filtro** | `EmptyState` · título **"Nenhum lançamento em agosto."** · descrição **"Registre o primeiro ou traga o extrato do banco — o app confere o que já existe antes de importar qualquer coisa."** · ação: `[Novo lançamento]` (primary) + `[Importar extrato]` (secondary). |
| **Vazio, filtro de conta** | Título **"Nenhum lançamento na Conta corrente em agosto."** · descrição **"Troque a conta no filtro ou volte para todas."** · ação `[Mostrar todas as contas]`. |
| **Vazio, filtro sem-categoria** | Título **"Tudo categorizado em agosto."** · descrição **"Nenhum lançamento deste mês ficou sem categoria."** · ação `[Mostrar todos os lançamentos]`. |
| **Erro na 1ª carga** | `Alert tone="error"` no lugar do `Panel`, título **"Não foi possível carregar os lançamentos."**, corpo = `messageForError`, ação `[Tentar de novo]` (mesmo padrão do `AccountsScreen`). |
| **Erro no "carregar mais"** | **Nunca troca a tabela por um erro.** As linhas já carregadas ficam; no rodapé do `Panel`, no lugar do botão: `Alert tone="error"` de uma linha — **"Não foi possível carregar mais lançamentos."** + `[Tentar de novo]`. |
| **Carregando mais** | O botão usa `loading` (mantém rótulo e largura, `aria-busy`, nunca `disabled`). Ao concluir, o foco **fica no botão** e um `<p role="status" className="sr-only">` anuncia: **"Mais 50 lançamentos carregados. 100 de 214."** |

Rodapé de paginação: à esquerda `Mostrando 50 de 214 lançamentos` (`--text-13 --ink-muted`,
tabular); à direita `Button variant="secondary"` **"Carregar mais 50"**. Quando acabou:
**"214 lançamentos — é tudo o que existe no filtro."** e nenhum botão.

### 1.5 Excluir — confirmação

`Dialog` nativo + `footer` com dois botões. Nada de `ConfirmDialog` novo: é composição, e mora em
`features/transactions/components/ExcluirLancamentoDialog.tsx`.

```
LANÇAMENTO NORMAL
title:        Excluir este lançamento?
description:  Padaria Exemplo · 12/08/2026 · R$ 11,00 · Conta corrente
corpo:        O lançamento sai da lista e dos totais do mês, e o saldo da conta é recalculado.
footer:       [Cancelar] (quiet)   [Excluir lançamento] (danger)
toast:        Lançamento excluído.

PERNA DE TRANSFERÊNCIA
title:        Excluir a transferência inteira?
description:  31/08/2026 · R$ 5.000,00 · Conta corrente → Cartão C6
corpo:        Este lançamento é uma das duas pernas de uma transferência. Excluir aqui apaga
              as duas: a saída de R$ 5.000,00 da Conta corrente e a entrada de R$ 5.000,00
              no Cartão C6. Os saldos das duas contas são recalculados.
footer:       [Cancelar] (quiet)   [Excluir as duas pernas] (danger)
toast:        Transferência excluída — as duas pernas.
```

O aviso do par está **no título e no corpo, em texto corrido** — não num ícone, não numa cor. O
rótulo do botão destrutivo repete o escopo ("as duas pernas") porque é o último texto que a pessoa
lê antes de clicar.

Erro na exclusão **não vira toast**: vira `FormError` dentro do `<dialog>`, que continua aberto com
os botões no lugar.

### 1.6 Filtro de tipo — emenda de 18/09/2026 (E2d)

A faixa ganha um segundo filtro, de **escolha única**: `Tudo · Receitas · Despesas ·
Transferências · Investimentos` (`?tipo=` na URL, `?kindGroup=` na API; "Tudo" é a **ausência** da
chave). A direção completa — controle, faixa do mês, subtotal do dia, faixa de pendência, toast,
copy e checklist — está em **`docs/DESIGN.md`, seção E2d**, que é normativa e **vence** o que este
documento diz em caso de conflito. Aqui fica só o que é desta tela e corrige o que §1.1–§1.4
escreveram em 16/09/2026.

**A faixa, com os dois filtros e o filtro de despesas ligado:**

```
┌───────────────────────────────────────────────────────────────────────────┐
│ Conta                  Tipo                                               │
│ [Todas as contas   ▾]  [Despesas        ▾]                  Saiu 3.100,00 │
│                                  Fora destes números: 2.000,00 em aportes │
├───────────────────────────────────────────────────────────────────────────┤
│ CONTA           CATEGORIA        DESCRIÇÃO                          VALOR │
├───────────────────────────────────────────────────────────────────────────┤
│ segunda, 31 de agosto                                              −29,00 │
│ Conta corrente  Alimentação      Padaria Exemplo                   −29,00 │
└───────────────────────────────────────────────────────────────────────────┘
```

Os dois `Select density="compact"` moram num `<div class="filtros">` (flex, `gap: var(--space-4)`;
`var(--space-2)` abaixo de 40 rem) que substitui o `Select` solto como primeiro filho da `.faixa`.
O resto da faixa não muda: `space-between`, filtros à esquerda, números à direita.

**Correções à tabela de §1.2** (valem só sob o filtro indicado; em Tudo nada muda):

| Item de §1.2 | Sob `tipo` |
|---|---|
| Subtotal do dia | **Não existe** em `transferencias` e em `investimentos` — o `trailing` do grupo é omitido. Transferência e investimento não mudam o patrimônio da casa; um `0,00` repetido em todo cabeçalho é resposta falsa, não total. |
| Rótulo do dia (`· 1 transferência`) | Só em **Tudo**. Sob `transferencias` não há subtotal para explicar; nos outros três não há linha de transferência. |
| Coluna Categoria | **Some** em `transferencias`: `Badge Transferência` em toda linha é o ruído de "escrever *Novo* 59 vezes" que §3.4 já recusou. |
| Coluna nova `Movimento` | Só em `investimentos`: primeira coluna, `width: 'min'`, sem `hideBelow`, palavra `Aporte`/`Resgate` em `--ink`. Derivada do `kind` (`expense` = aporte, `income` = resgate), que o servidor garante parear com a natureza (spec 0006 §7.2). |
| Coluna Valor | Em `investimentos`, **neutra e sem sinal** (E7 (b): aporte não é vermelho). Em `transferencias`, neutra **com** sinal, como hoje. Em `receitas`/`despesas`, como hoje. |
| Entrou / Saiu / Resultado | Um número só em `receitas` e em `despesas`; frase sem número em `transferencias`; `Aportes · Resgates` em `investimentos`. Tabela completa em E2d (b). |

**Acréscimos a §1.4 (estados)** — os textos exatos estão na tabela de copy de E2d (h):

| Estado | Direção |
|---|---|
| **Vazio, filtro de tipo** | `EmptyState` por opção (`Nenhuma despesa em setembro.`), ação `Mostrar todos os tipos`. |
| **Vazio, tipo + conta** | Título com os dois (`Nenhuma despesa na Nubank em setembro.`) e **dois** botões: `Mostrar todos os tipos` (primary) + `Mostrar todas as contas` (secondary). Adivinhar qual filtro a pessoa quis desfazer é pior do que oferecer os dois. |
| **Vazio, sem-categoria + tipo** | `Todas as despesas de setembro estão categorizadas.` com saída `Mostrar todas as despesas` — o botão limpa só o `semCategoria`, e o rótulo não pode prometer mais do que isso. |
| **Carregando** | `Skeleton` na quantidade do que vai aparecer: 3 em Tudo, 1 em receitas/despesas, 2 em investimentos, **nenhum** em transferências (a frase não depende de dado). |

**Precedência dos vazios**: `semCategoria` → `tipo` → `conta`.

**Busca da URL** — `?tipo=` é allowlist de quatro palavras em pt-BR (`receitas`, `despesas`,
`transferencias`, `investimentos`), com o mesmo rigor de `mes` e `conta` (P4). E **`semCategoria`
é descartado quando `tipo` é `transferencias` ou `investimentos`**, em `validarBusca` e em
`aplicarNaBusca` — a mesma mecânica que já descarta `contraparte` sem `conta`. A combinação não tem
resultado possível (transferência não tem categoria; aporte e resgate têm por definição), e uma URL
colada não pode virar lista vazia sem saída.

**Ordem de implementação desta emenda** (entra depois do item 4 de §9): (1) `?tipo=` em
`app/search.ts` com o descarte acima e teste; (2) `tipo` no filtro, na chave da query e na
`chaveDaBusca` do editor de categoria; (3) faixa do mês por opção; (4) subtotal do dia e rótulo do
dia; (5) faixa de pendência, `caption`, apoio, `document.title` e vazios; (6) colunas de
`transferencias` e `investimentos`; (7) a segunda frase do toast. Antes de (3), confirmar **P5**.

---

## 2. Tela `/importar` — três passos, três rotas

```
/importar                         passo 1 — Enviar        (largura 40rem, formulário)
/importar/{importId}/revisar      passo 2 — Revisar       (largura 76rem, tabela)
/importar/{importId}/resultado    passo 3 — Resultado     (largura 46rem)
```

`importId` inexistente ou expirado → 404 da API → a tela redireciona para `/importar` com
`Alert tone="warning"`: **"Esta análise não existe mais. Envie o arquivo de novo."**

### 2.1 Indicador de passo (nas três etapas)

`<ol aria-label="Etapas da importação">`, **à esquerda**, sob o `h1`, **não interativo** (é status,
não navegação; voltar é "Cancelar importação").

```
1 Enviar ──── 2 Revisar ──── 3 Resultado
```

- Número: caixa de 1.5rem, `border: 1px solid var(--border-strong)`, `--text-13`.
- Ativo: `background: var(--accent)`, `color: var(--on-solid)`, `aria-current="step"`.
- Concluído: borda `--accent` + `CheckIcon size={14}` no lugar do número.
- Conector: `1px solid var(--border)`.
- Nunca centralizado e nunca em "pill" arredondada — `--radius-sm`.

### 2.2 Passo 1 — Enviar

```
h1 Importar extrato ou fatura
p  O arquivo é lido, conferido contra o que já existe e só entra depois que você aprovar.

1 Enviar ──── 2 Revisar ──── 3 Resultado

┌ Panel title="Arquivo" ─────────────────────────────────────────────┐
│ Conta de destino                                                   │
│ [ Nubank › Conta corrente                                    ▾ ]   │  ← Select, optgroup = instituição
│ É a conta ou o cartão a que este arquivo pertence.                 │
│                                                                    │
│ Arquivo do extrato ou da fatura                                    │
│ ┌ 1px dashed --border-strong · bg --surface-sunken ──────────────┐ │
│ │ [Escolher arquivo]  nenhum arquivo selecionado                 │ │  ← <input type="file"> REAL e visível
│ │ ou arraste o arquivo para cá                                   │ │
│ └────────────────────────────────────────────────────────────────┘ │
│ CSV do Nubank ou o ZIP do C6, do jeito que foi baixado — sem      │
│ baixado — sem renomear nem abrir e salvar de novo.                 │
│                                                                    │
│ [ só quando .zip ou quando a API responde IMPORT_PASSWORD_REQUIRED ]│
│ Senha do arquivo                                                   │
│ [ ••••••••••••                                          [ver] ]    │  ← PasswordField existente
│ A senha do arquivo do C6 é o CPF do titular, só números...         │
│                                                                    │
│                                          [ Analisar arquivo ]      │
├────────────────────────────────────────────────────────────────────┤
│ O arquivo fica no servidor só até você concluir ou cancelar esta    │  ← footer do Panel, --text-13
│ importação.                                                        │
└────────────────────────────────────────────────────────────────────┘
```

**Regras duras atendidas:**

- O `<input type="file">` é **o elemento real, visível e rotulado** pelo `<label>` do `FieldShell`.
  O `::file-selector-button` é estilizado como botão secundário (`--control-h-sm`,
  `1px solid var(--border-strong)`, `--radius-sm`, `--font-ui --text-13`/600) — isso mantém o input
  nativo e dá a cara do sistema **sem** `<div>` clicável.
- Arrastar é **melhoria**: a zona tracejada escuta `dragover`/`drop`, e o drop grava em
  `input.files` via `DataTransfer` — o input continua sendo a fonte da verdade. Estado
  `[data-dragover="true"]`: `border-color: var(--accent)`,
  `background: color-mix(in oklch, var(--accent), transparent 92%)`.
- A senha **só aparece** quando (a) a extensão do arquivo é `.zip`, ou (b) a API respondeu
  `IMPORT_PASSWORD_REQUIRED`. No caso (b), aparece `Alert tone="info"`
  **"Este arquivo está protegido por senha."** e o foco vai para o campo de senha no mesmo efeito
  que o revela.

**Arquivo escolhido** (substitui a zona tracejada):

```
┌ 1px solid --border-strong · bg --surface ──────────────────┐
│ Nubank_2026-09-13.csv · 18 KB                   [Remover]  │
└────────────────────────────────────────────────────────────┘
```

Nome truncado com ellipsis + `title`. Tamanho em KB/MB com `Intl.NumberFormat('pt-BR')`.

**Textos prontos do passo 1:**

```
Label conta:       Conta de destino
Placeholder:       Escolha a conta
Hint conta:        É a conta ou o cartão a que este arquivo pertence.

Label arquivo:     Arquivo do extrato ou da fatura
Hint arquivo:      CSV do Nubank ou o ZIP do C6, do jeito que foi baixado —
                   sem renomear nem abrir e salvar de novo.
Arraste:           ou arraste o arquivo para cá
Remover:           Remover

Label senha:       Senha do arquivo
Hint senha:        A senha do arquivo do C6 é o CPF do titular, só números, sem pontos nem
                   traço. Ela serve só para abrir o arquivo agora e não é guardada em lugar
                   nenhum.
Alert (API):       Este arquivo está protegido por senha.

Submit:            Analisar arquivo
Enviando (status): Enviando e lendo o arquivo. Isso leva alguns segundos.
Rodapé do Panel:   O arquivo fica no servidor só até você concluir ou cancelar esta importação.
```

**Erros do passo 1 — todos em `FormError` ou no campo, nunca em toast:**

| Situação / código | Onde aparece | Texto |
|---|---|---|
| conta não escolhida | erro do `Select` | Escolha a conta de destino. |
| arquivo não escolhido | erro do `FileField` | Escolha o arquivo do extrato ou da fatura. |
| `IMPORT_PASSWORD_REQUIRED` | `Alert tone="info"` + foco no campo | Este arquivo está protegido por senha. |
| `IMPORT_PASSWORD_INVALID` | erro do `PasswordField` | Senha incorreta. No C6, é o CPF do titular, só números. |
| `IMPORT_UNSUPPORTED_FORMAT` | `FormError` | Não reconhecemos este arquivo. Ele precisa ser o CSV do Nubank ou o ZIP do C6, sem renomear. |
| `IMPORT_EMPTY_FILE` | `FormError` | O arquivo foi lido, mas não tem nenhum lançamento dentro. |
| `PAYLOAD_TOO_LARGE` | erro do `FileField` | Arquivo grande demais. O limite é 8 MB. |
| rede / 5xx | `Alert tone="error"` + `[Tentar de novo]` | Não foi possível enviar o arquivo. Verifique sua conexão e tente de novo. |

---

## 3. Passo 2 — Revisar: a solução para os 7 status

### 3.1 O diagnóstico

Sete status viram arco-íris porque tratamos "status" como **etiqueta**. Mas o usuário não precisa
saber o status: ele precisa responder **uma pergunta por linha** — *isso entra?* Os sete valores não
são sete coisas, são **três situações de trabalho**.

### 3.2 A solução: três blocos, uma pergunta por linha, zero cor de status

**Nenhuma marcação de conciliação usa cor cromática. Nenhuma.** A distinção vem de quatro portadores
que sobrevivem a monocromático, a daltonismo e a impressão em preto e branco:

1. **Em que bloco a linha está** (posição);
2. **A palavra no cabeçalho do grupo** (a explicação, escrita uma vez para o grupo inteiro);
3. **A frase de evidência na própria linha** (o "por quê" específico, com data e valor);
4. **A forma do controle de decisão** (`<select>` com palavras, checkbox, ou nenhum controle).

```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│ 1 · PRECISAM DA SUA DECISÃO · 6            aberto     controle: <select> com PALAVRAS│
│     possivel_duplicado · duplicado_excluido · pagamento_de_fatura                    │
├─────────────────────────────────────────────────────────────────────────────────────┤
│ 2 · PRONTAS PARA IMPORTAR · 59             aberto     controle: checkbox "Importar"  │
│     novo · repetido_no_arquivo                                                       │
├─────────────────────────────────────────────────────────────────────────────────────┤
│ 3 · FICAM DE FORA · 3                      <details> fechado     sem controle nenhum │
│     duplicado_exato · rejeitado                                                      │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

**Ordem:** decisão primeiro (é trabalho), prontas depois (é conferência), fora por último (é
auditoria). Quem só quer terminar rola até a barra e confirma; quem quer conferir encontra tudo no
caminho.

**O que fica colapsado:** só o bloco 3, com `<details>`/`<summary>` nativo — zero JavaScript,
teclado de graça. Justificativa: são as únicas linhas em que **não existe decisão a tomar**.
Colapsar o bloco 2 esconderia 59 linhas que a pessoa está prestes a gravar; colapsar o bloco 1
esconderia justamente o trabalho.

**A jogada central:** o status barrado não vira etiqueta — vira **o texto das opções do `<select>`
de decisão**. É lá que a pessoa lê o que vai acontecer, no momento de decidir.

| status | opções do `<select>` da linha (a 1ª é o default) |
|---|---|
| `pagamento_de_fatura` | `Não importar` · `Registrar como transferência para o Cartão C6` · `Importar como despesa mesmo assim` |
| `possivel_duplicado` | `Não importar (é a mesma)` · `Importar assim mesmo (é outra)` |
| `duplicado_excluido` | `Não importar` · `Restaurar o lançamento que eu excluí` |

O controle é um `<select>` nativo com `density="compact"`, `labelHidden` e
`aria-label="Decisão para Pagamento de fatura, 07/08, R$ 2.859,82"`.

Quando o servidor **não** sugerir a conta de destino da transferência, a opção vira
`Registrar como transferência para…` e escolhê-la revela, na mesma célula, um segundo `<select>`
compacto **"Conta de destino"**.

### 3.3 Layout do passo 2

```
h1 Revisar o que vai entrar
p  (role="status") 68 linhas lidas · Nubank · Conta corrente · 01/08 a 31/08
p  6 precisam da sua decisão · 59 prontas para importar · 3 ficam de fora.

1 Enviar ──── 2 Revisar ──── 3 Resultado

┌ Panel tone="sunken" title="Fatura do Cartão C6"  (SÓ em fatura) ───────────────────┐
│ Competência            Fechamento           Vencimento                             │
│ [setembro de 2026 ▾]   [05/09/2026]         [15/09/2026]                           │
│ Fechamento e vencimento vieram do arquivo. A competência é o mês em que esta        │
│ fatura aparece no app.                                                             │
└────────────────────────────────────────────────────────────────────────────────────┘

┌ Panel title="Precisam da sua decisão"  padding="none" ─────────────────────────────┐
│ Barramos estas linhas porque elas podem virar dinheiro contado duas vezes.          │
│ Nada aqui entra sem você mandar.                                                    │
├────────────────────────────────────────────────────────────────────────────────────┤
│ DATA    DESCRIÇÃO                          VALOR       DECISÃO          CATEGORIA   │
├────────────────────────────────────────────────────────────────────────────────────┤
│ ▌Pagamento de fatura · 1                                                            │  ← th scope="rowgroup"
│ ▌Dinheiro que sai da conta para abater a fatura do cartão. Se entrar como           │     label 15/600 + desc 13 muted
│ ▌despesa, o gasto é contado duas vezes: uma na compra, outra no pagamento.          │
│ 07/08  Pagamento de fatura              −2.859,82   [Não importar ▾]   [—       ▾]  │
│        Pagamento da fatura do Cartão C6.                                            │  ← 2ª linha: 13 muted
├────────────────────────────────────────────────────────────────────────────────────┤
│ ▌Possível duplicata · 4                                                             │
│ ▌Mesmo valor e data a até 3 dias de um lançamento que já existe, com descrição      │
│ ▌diferente.                                                                         │
│ 12/08  PADARIA EXEMPLO LTDA                −11,00   [Não importar ▾]   [—       ▾]  │
│        Possível duplicata de «Padaria» · 12/08 · mesmo valor.                        │
├────────────────────────────────────────────────────────────────────────────────────┤
│ ▌Já importada e excluída · 1                                                        │
│ ▌Este lançamento já existiu e você excluiu. Incluir aqui restaura o original,        │
│ ▌em vez de criar outro.                                                             │
│ 25/08  Resgate RDB                     +10.287,57   [Não importar ▾]   [—       ▾]  │
│        Já importada e excluída por você em 03/09.                                   │
└────────────────────────────────────────────────────────────────────────────────────┘

┌ Panel title="Prontas para importar" padding="none" ────────────────────────────────┐
│ Nenhuma colisão com o que já existe. Desmarque o que não quiser.  [Marcar todas]    │
├────────────────────────────────────────────────────────────────────────────────────┤
│ [x]  DATA    DESCRIÇÃO                                VALOR          CATEGORIA      │
│ [x]  04/08   Pix para Fulano de Tal Silva            −20,00     [Transporte  ▾]     │
│ [x]  05/08   Pix para ENERGIA EXEMPLO S.A.          −187,07     [—           ▾]     │
│ [x]  12/08   Pix para PADARIA EXEMPLO LTDA           −11,00     [—           ▾]     │
│ [x]  12/08   Pix para PADARIA EXEMPLO LTDA           −11,00     [—           ▾]     │
│              2ª ocorrência idêntica neste arquivo — as duas entram.                 │
└────────────────────────────────────────────────────────────────────────────────────┘

▸ Ficam de fora · 3 linhas                                            ← <details> fechado
   2 já importadas · 1 linha inválida. Nada aqui pode entrar.
   07/08  Pagamento recebido      +2.859,82   Já importada em 05/09 · lançamento idêntico.
   31/02  (linha 42 do arquivo)          —    Linha inválida: a data «31/02/2026» não existe.

┌ barra grudada no rodapé · bg --surface · border-block-start 1px --border · sem sombra ┐
│ Inclui 1 lançamento restaurado e 1 transferência.                                     │  ← <output aria-live="polite">
│                       [Cancelar importação]  [Importar 42 lançamentos · 6 ignorados]  │
└───────────────────────────────────────────────────────────────────────────────────────┘
```

### 3.4 Léxico de status — palavra, nunca cor

| status | palavra (no cabeçalho do grupo ou na 2ª linha) | evidência na linha, pronta para colar |
|---|---|---|
| `novo` | — (a ausência é a informação) | `<span className="sr-only">Sem pendência.</span>` |
| `repetido_no_arquivo` | **2ª ocorrência** | `2ª ocorrência idêntica neste arquivo — as duas entram.` |
| `duplicado_exato` | **Já importada** | `Já importada em 05/09 · lançamento idêntico nesta conta.` |
| `duplicado_excluido` | **Já importada e excluída** | `Já importada e excluída por você em 03/09.` |
| `possivel_duplicado` | **Possível duplicata** | `Possível duplicata de «{descrição}» · {data} · mesmo valor.` |
| `pagamento_de_fatura` | **Pagamento de fatura** | `Pagamento da fatura do {conta do cartão}.` |
| `rejeitado` | **Linha inválida** | `Linha inválida: {motivo do servidor}.` |

Para `novo`, a célula fica **visualmente vazia** de propósito — escrever "Novo" 59 vezes é ruído —
mas nunca vazia para leitor de tela: o `sr-only` diz "Sem pendência".

Textos dos cabeçalhos de grupo do bloco 1 (o "por quê" escrito uma vez para o grupo):

```
Pagamento de fatura · {n}
  Dinheiro que sai da conta para abater a fatura do cartão. Se entrar como despesa, o gasto
  é contado duas vezes: uma na compra, outra no pagamento.

Possível duplicata · {n}
  Mesmo valor e data a até 3 dias de um lançamento que já existe, com descrição diferente.

Já importada e excluída · {n}
  Este lançamento já existiu e você excluiu. Incluir aqui restaura o original, em vez de
  criar outro.
```

Títulos e apoios dos três blocos:

```
Bloco 1 — título:   Precisam da sua decisão
Bloco 1 — apoio:    Barramos estas linhas porque elas podem virar dinheiro contado duas vezes.
                    Nada aqui entra sem você mandar.

Bloco 2 — título:   Prontas para importar
Bloco 2 — apoio:    Nenhuma colisão com o que já existe. Desmarque o que não quiser.

Bloco 3 — summary:  Ficam de fora · 3 linhas
Bloco 3 — apoio:    2 já importadas · 1 linha inválida. Nada aqui pode entrar: as já importadas
                    criariam lançamento repetido, e a inválida não tem data ou valor que dê
                    para ler.
```

**Única cor cromática do passo 2:** o contador `6` no título do bloco 1, num `Badge tone="warning"`.
É reforço do que a palavra já diz, e `--warning` é justamente "algo pendente de decisão humana" — o
mesmo uso do aviso de conta em `/contas`. Todo o resto do passo 2 é `--ink`, `--ink-muted`,
`--border` e `--surface-sunken`, mais `--income`/`--expense` **nos valores** — que é dinheiro, não
status.

**Teste de aceite:** aplicar `filter: grayscale(1)` na tela inteira. Se alguma decisão ficar
ambígua, a marcação está errada.

### 3.5 Ações, seleção e contagem

- **Bloco 2**: checkbox por linha (`accent-color: var(--accent)`, 1rem, alvo efetivo de 44px pela
  altura da célula), com
  `aria-label="Importar Pix para PADARIA EXEMPLO LTDA, 12/08, 11 reais negativos"`. Checkbox mestre
  no `<th>` com `indeterminate` + `aria-label="Importar todas as 59 linhas prontas"`; no cabeçalho
  do painel, `[Marcar todas]` / `[Desmarcar todas]` como `Button size="sm" variant="quiet"`.
- **Bloco 1**: sem checkbox — `<select>` de decisão com as palavras da tabela de 3.2. No cabeçalho
  do painel, `[Decidir tudo como "não importar"]` aparece **só depois** que alguma linha foi
  mudada: é um desfazer, não um atalho para ignorar tudo.
- **Bloco 3**: **nenhum controle**, nem desabilitado. Checkbox `disabled` tem contraste ruim e some
  para parte das tecnologias assistivas; a ausência do controle, com o `<summary>` dizendo "Nada
  aqui pode entrar", comunica melhor.
- **Categoria por linha**: `Select density="compact" labelHidden`, placeholder `—`,
  `aria-label="Categoria de {descrição}"`, agrupada por `optgroup` (a árvore de 2 níveis já vem
  pronta do servidor). É **opcional** — a faixa de `/lancamentos` existe exatamente para resolver o
  resto depois.
  *Limite medido:* acima de ~150 linhas visíveis, montar as `<option>` só no primeiro
  `focus`/`pointerdown` da célula. **Não pré-otimizar antes disso.**

### 3.6 Barra de confirmação

- `position: sticky; inset-block-end: 0`, `background: var(--surface)`,
  `border-block-start: 1px solid var(--border)`, `padding: var(--space-3) var(--space-4)`.
  **Sem sombra** — não é camada flutuante, é o rodapé do documento.
- Botão primário, com o rótulo dizendo o que vai acontecer:

```
Normal:          Importar 42 lançamentos · 6 ignorados
Um item só:      Importar 1 lançamento · 6 ignorados
Nada marcado:    Nada marcado para importar        (aria-disabled="true", NUNCA disabled)
Ao clicar assim: Marque ao menos uma linha, ou cancele a importação.   (FormError acima da barra)
```

- `<output aria-live="polite">` ao lado, `--text-13 --ink-muted`, só quando houver nuance:
  **"Inclui 1 lançamento restaurado e 1 transferência."**
- `[Cancelar importação]` (`variant="quiet"`) abre `Dialog`:

```
title:  Cancelar esta importação?
corpo:  O arquivo é descartado e nada é gravado. Suas decisões desta tela se perdem.
footer: [Voltar para a revisão]   [Descartar o arquivo] (danger)
```

### 3.7 Bloco de fatura

Só quando o arquivo é fatura de cartão. `Panel tone="sunken"`, três campos em linha (empilham
abaixo de 40rem), pré-preenchidos com a sugestão do servidor:

```
Competência   — <select> com 5 meses (3 anteriores, o sugerido, 1 seguinte), rótulo "setembro de 2026"
Fechamento    — TextField type="date"
Vencimento    — TextField type="date"

Texto de apoio: Fechamento e vencimento vieram do arquivo. A competência é o mês em que esta
                fatura aparece no app.
Erro de ordem:  O vencimento não pode ser antes do fechamento.
```

---

## 4. Passo 3 — Resultado

```
h1 Importação concluída
p  Nubank · Conta corrente · extrato de 01/08 a 31/08
p  (role="status") 42 lançamentos importados, 1 restaurado, 1 transferência registrada,
                   6 ignorados por você e 3 bloqueados.

1 Enviar ──── 2 Revisar ──── 3 Resultado

┌ Panel title="O que aconteceu" ────────────────────────────┐
│ Lançamentos importados                              42    │  ← <dl>, valores tabular-nums,
│ Lançamentos restaurados                              1    │     alinhados à direita
│ Transferências registradas                           1    │
│ Linhas que você ignorou                              6    │
│ Linhas bloqueadas                                    3    │
└───────────────────────────────────────────────────────────┘

[ Ver os lançamentos de agosto ]   [ Importar outro arquivo ]

▌ Você deixou de fora o pagamento da fatura de R$ 2.859,82. Enquanto ele não for
▌ registrado como transferência da Conta corrente para o Cartão C6, a fatura do cartão
▌ continua com o valor cheio: o dinheiro já saiu da conta, mas a dívida do cartão não
▌ foi abatida.
▌                                    [Registrar a transferência]          ← Alert warning

▌ 42 lançamentos entraram sem categoria. Eles não entram em orçamento nem em relatório
▌ enquanto ficarem assim.
▌                                    [Categorizar agora]                  ← Alert info
```

**Textos prontos do passo 3:**

```
h1:                  Importação concluída
Contexto:            Nubank · Conta corrente · extrato de 01/08 a 31/08
Frase role=status:   42 lançamentos importados, 1 restaurado, 1 transferência registrada,
                     6 ignorados por você e 3 bloqueados.

Rótulos do <dl>:     Lançamentos importados
                     Lançamentos restaurados
                     Transferências registradas
                     Linhas que você ignorou
                     Linhas bloqueadas

Ações:               Ver os lançamentos de agosto        (primary)
                     Importar outro arquivo              (secondary)

AVISO DA FATURA (Alert tone="warning", só quando houver pagamento de fatura ignorado):
  Você deixou de fora o pagamento da fatura de R$ 2.859,82. Enquanto ele não for registrado
  como transferência da Conta corrente para o Cartão C6, a fatura do cartão continua com o
  valor cheio: o dinheiro já saiu da conta, mas a dívida do cartão não foi abatida.
  Ação: Registrar a transferência

AVISO DE CATEGORIA (Alert tone="info", só quando houver lançamento sem categoria):
  42 lançamentos entraram sem categoria. Eles não entram em orçamento nem em relatório
  enquanto ficarem assim.
  Ação: Categorizar agora

NADA IMPORTADO (EmptyState no lugar do Panel):
  Título:     Nada foi importado.
  Descrição:  Todas as linhas ficaram de fora. O arquivo continua com você — se foi engano,
              envie de novo e revise as decisões.
```

**Regras do passo 3:**

- Linhas do `<dl>` com valor 0 **não renderizam** — uma lista de zeros é ruído.
- `[Ver os lançamentos de agosto]` → `/lancamentos?mes=2026-08` (o **mês do arquivo**, não o mês
  corrente).
- `[Categorizar agora]` → `/lancamentos?mes=2026-08&semCategoria=1`.
- `[Registrar a transferência]` → **navega** para `/lancamentos?mes=2026-08` com
  `state: { novaTransferencia: { valorCents, data, contaOrigemId, contaDestinoId } }`; a tela de
  lançamentos abre o próprio diálogo já preenchido. É assim porque **feature não importa de
  feature** (regra do `AGENTS.md`).

---

## 5. Acessibilidade — obrigatória em cada passo

| Item | Direção |
|---|---|
| Foco na troca de passo | `h1` com `tabIndex={-1}` recebe o foco no mount (`outline: none` em `:focus`, anel só em `:focus-visible`) — regra já vigente no projeto. |
| `aria-live` por passo | Passo 1: `role="status"` no texto "Enviando e lendo o arquivo…". Passo 2: a linha "68 linhas lidas…" é `role="status"` **visível** (sem duplicata `sr-only`). Passo 3: a frase do resultado é `role="status"` **visível**. |
| Contagem viva | `<output aria-live="polite">` na barra de confirmação. `polite`, nunca `assertive`. |
| Input de arquivo | `<input type="file">` real, visível e rotulado por `<label for>`; arrastar-e-soltar só como extra. Foco: `outline: var(--focus-ring); outline-offset: var(--focus-offset)`. **Nunca** escondido atrás de `<div>` clicável. |
| Senha revelada pela API | Foco movido para o campo no mesmo efeito que o revela, mais `Alert role="status"` explicando por quê. |
| Tabela | `<table>` semântica com `<caption className="sr-only">`; grupos em `<tbody>` com `<th scope="rowgroup">`. |
| Nome de cada controle | Checkbox e `<select>` de linha recebem `aria-label` com descrição + data + valor. Nunca só "Importar". |
| Duplicata em P&B | Nenhuma marcação de status usa cor. Testar imprimindo em escala de cinza **e** com `filter: grayscale(1)` no navegador: a tela tem que continuar decidível. |
| Contraste | Zero cor nova. Texto de evidência em `--ink-muted` sobre `--surface` = 5,1:1 (AA). Nunca usar `--ink-muted` sobre `--surface-sunken` em fonte menor que 15px sem reverificar. |
| Alvo de toque | Linhas de 44px; `Button size="sm"` = 36px de altura; checkbox de 1rem dentro de célula de 44px. |
| Movimento | Só o `<dialog>` anima (já implementado, com `prefers-reduced-motion`). A transição entre passos é navegação de rota (View Transitions do navegador). Nenhuma animação em linha de tabela. |

---

## 6. Componentes: reuso, extensão, nascimento

### 6.1 Reusados sem tocar

`Panel` · `DataTable` (uso atual de `/contas`) · `Badge` · `Button` · `Dialog` · `Alert` ·
`FormError` · `Toast` · `EmptyState` · `Skeleton` · `Select` · `PasswordField` · `TextField` (com
`type="date"`) · `TextLink` · `MonthNavigator` (já na casca) · ícones `PlusIcon`, `TrashIcon`,
`PencilIcon`, `CheckIcon`, `AlertIcon`, `InfoIcon`, `ChevronDownIcon`.

### 6.2 Componente novo — **um só**

**`FileField`** — `frontend/src/components/FileField/`

```ts
type FileFieldProps = {
  label: string
  accept: string                       // ".csv,.zip"
  hint?: string | undefined
  error?: string | undefined
  file?: File | null | undefined
  onSelect: (file: File | null) => void
  ref?: Ref<HTMLInputElement> | undefined
}
```

Monta sobre `FieldShell` (label, hint, erro e slot de mensagem de graça). Internamente: zona
tracejada + `<input type="file">` nativo **visível** + linha do arquivo escolhido com `[Remover]`.
Estilo do `::file-selector-button` como botão secundário. Atributo `data-dragover` para o estado de
arraste. Nasce com teste (teclado, `onSelect`, remover, estado de erro).

### 6.3 Extensões de componente existente (4) — nenhuma inventa token

| Componente | Extensão | Por que não dá para evitar |
|---|---|---|
| `DataTable` | `groups?: readonly RowGroup<T>[]` — `{ key, label, description?, trailing?, rows }`, renderizado como **um `<tbody>` por grupo** com uma `<tr>` de cabeçalho contendo `<th scope="rowgroup" colSpan={n-1}>` + `<td>` do `trailing`. | É o agrupamento por dia (tela 1) **e** por motivo de bloqueio (tela 2) — o mesmo mecanismo serve às duas. O `PLANOS.md §8.3` já previu "DataTable (densa, header fixo, **agrupamento**)". A alternativa (uma `<table>` por dia) repetiria o cabeçalho e quebraria a navegação por coluna do leitor de tela. |
| `DataTable` | `Column.hideBelow?: 'sm'` → emite `data-hide="sm"` na `th`/`td`; o CSS do próprio componente esconde abaixo de 40rem. | Sem isso, a tabela de 5 colunas no celular só tem rolagem horizontal. |
| `Select` + `FieldShell` | `density?: 'form' \| 'compact'` (compacto: `--control-h-sm`, largura por conteúdo, slot de mensagem só quando há erro) e `labelHidden?: boolean` (label vai para `.sr-only`). | Célula de tabela não comporta label visível nem 20px de slot reservado; barra de filtro não é formulário. Sem isso, a faixa de filtro passa de 82px para 114px e cada célula de decisão fica 40px mais alta. |
| `MoneyText` + `lib/money` | `sign?: 'auto' \| 'always'` → `formatarValor(cents, { sinal: 'sempre' })` com `Intl signDisplay: 'always'`. O rótulo `sr-only` ganha "positivos", simétrico ao "negativos" que já existe. | A direção exige **sinal explícito**, e as colunas fixadas (conta/categoria/descrição/valor) não deixam espaço para uma coluna "tipo". Com `+`/`−`, a direção do dinheiro sobrevive ao preto e branco sem depender de `--income`/`--expense`. |

### 6.4 Componentes locais de feature (não são design system)

`ImportStepper` (o `<ol>` de passos), `ExcluirLancamentoDialog`, `FaturaFields`, `BlocoDecisao`,
`BlocoProntas`, `BlocoForaDetails` — todos em `features/import/components/` e
`features/transactions/components/`.

### 6.5 Tokens

**Zero token novo.** Tudo sai de `frontend/src/styles/tokens.css` como está, com as derivações
canônicas já documentadas (`color-mix(in oklch, …)`). Se algum dev precisar de uma cor que não está
lá, é sinal de que a direção foi contornada — volta para o `designer-ui`.

---

## 7. Revisão anti-"cara de IA" desta direção

| Item da lista de rejeição | O que esta direção fez |
|---|---|
| Gradiente roxo/azul/violeta | Nenhum gradiente em lugar nenhum. Superfícies sólidas, separação por borda de 1px. |
| Glassmorphism | Nenhum `backdrop-filter`. O backdrop do `<dialog>` continua sólido a 45%. |
| Cards flutuando com sombra | `Panel` com `box-shadow: none`. A barra grudada do passo 2 usa borda, não sombra. `--shadow-layer` só no `<dialog>`. |
| Emoji como ícone | Nenhum. Só o conjunto SVG do projeto, traço 1.5, `currentColor` — inclusive nas ações de linha. |
| Hero centralizado | Nada centralizado: todo título é `h1` alinhado à esquerda com uma linha de apoio; o indicador de passos é um `<ol>` à esquerda. |
| Grid de cards vazios | O resultado do passo 3 é uma `<dl>` de 5 linhas, **não** 5 cartões com números grandes. O resumo do mês é uma linha de texto na faixa de filtro, não 3 KPIs. |
| Paleta de 10 cores / roxo de destaque | Quatro cores cromáticas no total: `--accent` (ação), `--income`/`--expense` (dinheiro) e `--warning` (pendência de decisão). Status de importação: **zero cor**. |
| Cara de shadcn/MUI | `<select>` nativo, `<details>` nativo, `<dialog>` nativo, `<input type="file">` nativo e visível. Nada de combobox reimplementado, nada de badge colorida por status, nada de "pill" arredondada. |
| Tipografia sem intenção | Fraunces só em `h1`, título de painel e **valores monetários**; Public Sans em todo o resto — inclusive rótulos de status, hints e opções de decisão. |

---

## 9. Ordem de implementação

1. **Extensões do `DataTable`** (`groups`, `hideBelow`) com teste — destrava as duas telas.
2. **`MoneyText sign`** + `formatarValor(cents, { sinal })`.
3. **`FieldShell`/`Select`**: `density` + `labelHidden`.
4. **`/lancamentos`**: tabela agrupada, filtro, resumo, estados, faixa de pendência, diálogo de
   exclusão (com a variante de transferência).
5. **`FileField`** + passo 1.
6. **Passo 2**: os três blocos, os controles de decisão, a barra de confirmação.
7. **Passo 3** + o retorno do pagamento de fatura para `/lancamentos` via `state` do router.

Antes do item 4, confirmar as pendências **P1**, **P2** e **P4** do topo deste documento; antes de
publicar o passo 1, confirmar a **P3** — o rodapé daquela tela faz uma promessa ao usuário em nome
do backend.

Ao fim: `npx biome check` · `npm test` · `npm run build` limpos, E2E do fluxo novo verde, e revisão
do `revisor-seguranca` com veredito **APROVADO** (regra do `CLAUDE.md`, sem exceção).
