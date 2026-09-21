# Spec 0006 — Investimentos e resgates por natureza de categoria (entrega E7)

**Status:** rascunho para aprovação do usuário · **Data:** 17/09/2026 · **Origem:** pedido do usuário
(17/09/2026), com as quatro decisões de desenho respondidas na entrevista da skill `/especificar`.

Documentos que esta spec toca: `PLANOS.md` §1.3 (o v1 dizia "sem investimentos" — ver §9),
`docs/ARQUITETURA.md` (ADR novo), `docs/specs/0005` (§3, o algoritmo de palavras-chave, reaproveitado
inteiro).

---

## 1. Problema

A casa investe e resgata pelo mesmo extrato que paga o mercado: hoje um aporte de R$ 2.000 entra como
despesa comum, some dentro do relatório por categoria como se fosse gasto, e "quanto eu guardei este
mês" só se responde somando à mão.

## 2. Escopo

### 2.1 Entra

1. **Duas naturezas novas de categoria**: `investment` (aporte) e `redemption` (resgate), ao lado de
   `income` e `expense`. Mesmo cadastro, mesma árvore de exatamente dois níveis (ADR-017b), **mesmas
   palavras-chave** — a tabela `category_keywords` da spec 0005 serve as quatro naturezas sem mudança.
2. **Semente**: os grupos "Investimentos" (`investment`) e "Resgates" (`redemption`) entram em
   `DefaultGroups()`. Como a semente é idempotente e roda também como auto-reparo no login
   (`household.EnsureDefault`), as casas que já existem ganham os dois grupos sem migração de dados.
3. **Lançamento continua `income`/`expense`** — nenhum tipo novo de lançamento, nenhuma mudança na
   conta do saldo (ADR-017). O que muda é o pareamento: despesa aceita categoria `expense` **ou**
   `investment`; receita aceita `income` **ou** `redemption`.
4. **Troca de natureza dentro do mesmo lado do dinheiro** passa a ser permitida **mesmo com a
   categoria em uso** (`expense ↔ investment`, `income ↔ redemption`): é o caminho de quem já tem uma
   categoria "Investimentos" cheia de lançamentos. Cruzar o lado (`expense ↔ income`) continua
   recusado com `ErrKindLocked`.
5. **Aporte e resgate saem das agregações de receita e despesa** (decisão do usuário): relatório por
   categoria e `summary` de `GET /transactions`. O saldo da conta **não** muda — o dinheiro saiu
   mesmo.
6. **Tela `/investimentos`** com item próprio no menu: números do mês, acumulado do ano até o mês,
   série dos últimos 12 meses e a lista dos lançamentos marcados.
7. **`POST /investments/detect`** — prévia e marcação retroativa por palavra-chave sobre lançamentos
   já gravados, no molde do `/transfers/detect` da spec 0005 §13.
8. **Importação**: a sugestão de categoria passa a considerar também as categorias das naturezas
   novas, pelo mesmo motor da spec 0005 §3 — descrição "CDB 15 DIAS" numa despesa sugere a categoria
   de investimento.

### 2.2 Fica explicitamente de fora

- **Patrimônio, saldo investido acumulado, posição, rentabilidade, cotação, ativo, corretora.** A tela
  responde *quanto foi investido*, nunca *quanto eu tenho*. Não existe nenhum número acumulado além
  dos dois do ano corrente.
- Conta do tipo "investimento" e transferência para carteira (o ADR-016 fica como está): o aporte
  continua sendo dinheiro que **sai** da conta.
- Aporte programado, meta, projeção, orçamento de investimento, exportação.
- Recategorizar em massa lançamento que **já tem** categoria sem pedido explícito (ver §3.3.4).
- Natureza nova em conta fixa, orçamento ou qualquer tela ainda não entregue.

## 3. Comportamento

### 3.1 Cadastro das categorias (`/categorias`)

1. A tela ganha as seções "Investimentos" e "Resgates" ao lado de "Receitas" e "Despesas", com o mesmo
   componente de árvore, o mesmo diálogo e o mesmo `KeywordsField`.
2. Palavra-chave continua **única por casa entre todas as categorias**: cadastrar "CDB" numa categoria
   de investimento quando ela já está numa de despesa é `409 KEYWORD_TAKEN`, como hoje. A ambiguidade
   nasce barrada, sem regra nova.
3. Grupo com subcategoria ativa continua sem receber lançamento nem palavra-chave (spec 0005 §12).

### 3.2 Marcação na importação

1. Na análise, a linha de **despesa** passa a concorrer contra as categorias ativas de natureza
   `expense` **e** `investment`; a de **receita**, contra `income` **e** `redemption`. Uma só
   passagem, com o algoritmo da spec 0005 §3 inalterado (limiar 80, desempate, ambiguidade).
2. Conta batendo continua vencendo categoria batendo: linha que parece transferência interna não
   recebe categoria nenhuma, de nenhuma natureza.
3. Na revisão, a linha marcada mostra a categoria sugerida como qualquer outra — sem tratamento visual
   especial, porque a natureza já está no nome do grupo.

### 3.3 Detectar sobre o que já está gravado (`POST /investments/detect`)

1. A tela `/investimentos` tem o botão **"Detectar investimentos"**, habilitado quando existe ao menos
   uma categoria ativa de natureza `investment` ou `redemption` com palavra-chave.
2. `dryRun: true` devolve a prévia: quantos lançamentos **sem categoria** do mês de competência passam
   a ter categoria de investimento/resgate, a lista (descrição → categoria · pontuação) e a lista dos
   que continuam sem marca, com o motivo (`below_threshold` | `ambiguous`).
3. Confirmar manda o mesmo pedido com `dryRun: false`; o servidor **recalcula** (nunca confia na
   prévia) e escreve só onde `category_id IS NULL` e `kind IN (income, expense)`. Transferência nunca
   é tocada. Idempotente: rodar de novo marca 0.
4. Lançamento que **já tem** categoria e que bateria numa palavra-chave aparece na prévia em lista
   separada (`alreadyCategorizedItems`) e **não é alterado**, a menos que o pedido traga
   `overwriteCategorized: true` — a tela pede isso em confirmação própria ("trocar a categoria de N
   lançamentos que já têm uma"). Mesmo com a flag, a troca nunca desfaz uma marcação anterior: só
   substitui categoria de natureza `income`/`expense`.
5. Teto de 10.000 lançamentos candidatos por execução (`MaxAutoCategorizeRows`): acima é `422`, nunca
   execução parcial. Rate limit da classe de escrita pesada, a mesma da importação.
5.1. **Reconferência dentro da transação (emenda da revisão de segurança).** O cálculo roda fora da
   transação e a escrita dentro, então cabe uma requisição inteira entre os dois. Antes dos `UPDATE`, a
   transação relê **numa consulta** tudo o que qualifica cada categoria de destino: ser da casa, estar
   **viva**, **não arquivada**, sem subcategoria ativa, e de natureza `investment`/`redemption`
   compatível com o lado do dinheiro daquele lote. Faltando qualquer um, a operação inteira é desfeita
   com `409 CONFLICT` e nada é gravado. A categoria de **origem** é relida junto e apenas **podada** da
   allowlist quando deixa de ser comum — podar não derruba a operação, porque é indistinguível de um
   `WHERE` sem correspondência.

   **A distinção do arquivamento vale por inteiro, e é sutil:** *marcação existente* sobrevive ao
   arquivamento (categoria arquivada continua contando, e o lançamento que já aponta para ela continua
   apontando — §4.4 do `PLANOS.md`), mas *atribuição nova* a categoria arquivada é recusada, aqui como
   no `PATCH /transactions/{id}`, no lote e na importação (`ErrCategoryArchived`). Por isso a **origem**
   arquivada continua trocável e o **destino** arquivado é conflito. Sem essa recusa o `detect` seria a
   única porta do produto a gravar onde as outras três recusam.

6. Auditado com o mês como entidade e **ação distinta por escopo**:
   `transaction.investments_detected` quando a execução só preenche o que estava vazio, e
   `transaction.investments_overwritten` quando o pedido trouxe `overwriteCategorized: true`. Sem a
   distinção, "preencheu 500 vazios" e "substituiu 500 categorias escolhidas à mão" seriam a mesma
   linha de rastro, e a segunda não tem desfazer. Nunca a descrição, nunca a palavra-chave.

### 3.4 Tela `/investimentos`

1. Item novo no menu, depois de Transferências. Usa o `MonthNavigator` da casca (`mes` na URL, fuso da
   casa — ADR-019).
2. **No mês**: aportes (soma e contagem) e resgates (soma e contagem), lado a lado. Sem saldo, sem
   líquido, sem patrimônio.
   > **Emenda de 18/09/2026 — escopo desta regra.** Ela vale para **esta tela**, e continua valendo
   > por decisão explícita do usuário na mesma data. O **painel** (`/`) passa a exibir o investimento
   > do mês como UM número líquido (aportes − resgates, com sinal, podendo ser negativo), em schema
   > próprio — o `InvestmentTotals` desta rota **não** ganha líquido. Ver `LICOES-FRONTEND.md` e
   > `LICOES-BACKEND.md`.
3. **No ano**: os mesmos dois números, de 1º de janeiro até o mês selecionado, inclusive ("no ano, até
   setembro"). Ano civil da casa.
4. **Últimos 12 meses**: série terminando no mês selecionado, em barras SVG próprias (ADR-021), com a
   tabela mês a mês como fonte da verdade — o gráfico é ilustração, nunca o único caminho para o
   número (docs/DESIGN.md, acessibilidade).
5. **Lista do mês**: data, conta, descrição, categoria e valor, com aporte e resgate distinguidos por
   rótulo textual (nunca só por cor). Ordem e paginação por cursor, como `GET /transactions`.
6. **Vazio sem categoria configurada**: "Nenhuma categoria de investimento ainda", com link para
   `/categorias`. **Vazio com categoria e sem lançamento**: "Nenhum aporte ou resgate em *setembro de
   2026*", com o botão de detectar ao lado.
7. **Erro**: mês inválido na URL cai na validação da casca; falha da API mostra o `Alert` padrão com
   possibilidade de tentar de novo. Nenhum número parcial é exibido como se fosse total.

### 3.5 O que muda nas telas que já existem

1. `GET /reports/by-category` (tela `/relatorios/categorias`) passa a **descartar** os lançamentos
   cuja categoria é de natureza `investment`/`redemption`, dos totais e das linhas. O balde "Sem
   categoria" não muda.
2. `summary` de `GET /transactions`: `incomeCents`, `expenseCents` e `netCents` deixam de somar os
   marcados, e a resposta ganha `investedCents` e `redeemedCents` (sempre presentes). A faixa do mês em
   `/lancamentos` passa a dizer, quando houver, "e R$ 2.000,00 investidos" — sem isso o total do mês
   encolheria sem explicação, que é mentira por omissão.
3. A **lista** de `/lancamentos` continua mostrando os lançamentos marcados: eles existem e saíram da
   conta.
4. **Saldo de conta: inalterado.** Nenhuma consulta de saldo olha a natureza da categoria.

## 4. Contrato (rascunho para o `arquiteto` refinar no OpenAPI)

**Categorias** — o enum `kind` de `Category` ganha `investment` e `redemption` em `POST`, `PATCH` e na
resposta. `GET /categories` (`ListView`) ganha os arrays `investment` e `redemption`, sempre presentes
(`[]` quando vazios), ao lado de `income` e `expense`.

**Lançamentos** — `summary` ganha `investedCents` e `redeemedCents` (int64, sempre presentes);
`incomeCents`/`expenseCents`/`netCents` mudam de significado (§3.5.2). `400 CATEGORY_KIND_MISMATCH`
continua sendo o erro do pareamento errado.

**Relatório** — `GET /reports/by-category?month&kind` mantém o enum `income|expense` do parâmetro: o
relatório de investimento é a tela própria, não um `kind` a mais.

**Investimentos** — `GET /api/v1/investments?month=YYYY-MM&cursor=&pageSize=` · 200:

```json
{ "month": "2026-09",
  "monthly":    { "contributionsCents": 200000, "contributionCount": 3, "redemptionsCents": 85000, "redemptionCount": 1 },
  "yearToDate": { "contributionsCents": 1840000, "contributionCount": 22, "redemptionsCents": 320000, "redemptionCount": 4 },
  "series": [ { "month": "2025-10", "contributionsCents": 150000, "redemptionsCents": 0 } ],
  "items":  [ { "id": "...", "occurredOn": "2026-09-05", "flow": "contribution", "accountId": "...", "accountName": "Nubank",
                "categoryId": "...", "categoryName": "CDB", "amountCents": 200000, "description": "CDB 15 DIAS", "source": "import" } ],
  "nextCursor": null }
```

`series` tem **sempre 12 itens**, do mês selecionado para trás, com zeros nos meses sem movimento — o
cliente não escolhe o tamanho. `month` filtra por `competence_month`, como `GET /transactions`.
`monthly` e `yearToDate` são **completos**; `items` é paginado (`pageSize` ≤ 200).

**Detecção** — `POST /api/v1/investments/detect` · corpo
`{ "month": "2026-09", "dryRun": true, "overwriteCategorized": false }` · 200:

```json
{ "month": "2026-09", "marked": 5, "unmatched": 2, "alreadyCategorized": 1,
  "items": [ { "id": "...", "description": "CDB 15 DIAS", "categoryId": "...", "categoryName": "CDB",
               "flow": "contribution", "matchScore": 92, "matchedKeyword": "cdb" } ],
  "unmatchedItems": [ { "id": "...", "description": "...", "reason": "below_threshold" } ],
  "alreadyCategorizedItems": [ { "id": "...", "description": "...", "currentCategoryId": "...", "currentCategoryName": "Outros",
                                 "categoryId": "...", "categoryName": "CDB", "flow": "contribution", "matchScore": 92 } ] }
```

As três listas só vêm em `dryRun`, com teto de 500 itens cada; as contagens são sempre completas.
`422` quando o mês tem mais de 10.000 candidatos. Rate limit da classe de escrita pesada.

Toda referência a recurso de outra casa responde **404**, como em todo o resto da API.

## 5. Dados (rascunho para o `arquiteto-dados`)

**Nenhuma tabela nova e nenhuma coluna nova.** O que muda:

| Item | Mudança |
|---|---|
| `categories.kind` | Continua `varchar(10)`: `investment` e `redemption` têm exatamente 10 caracteres. Só a **allowlist da aplicação** cresce — o schema não muda de versão. |
| `category_keywords` | Inalterada. O índice único `(household_id, keyword_norm)` já garante a unicidade entre as quatro naturezas. |
| Semente | `DefaultGroups()` ganha dois grupos. Idempotente, aplicada também no auto-reparo do login. |
| Índices | Nenhum novo. A consulta do mês resolve primeiro os ids das categorias das duas naturezas (≤ 200 por casa, `MaxPerHousehold`) e filtra `category_id IN (...)` sobre `ix_transactions_competence`; a série de 12 meses é **uma** consulta agrupada por `competence_month`. |

Armadilha portátil a respeitar: com **zero** categorias de investimento, a lista de ids é vazia e o
repositório **nunca** pode emitir `IN ()` — o curto-circuito é em Go, devolvendo vazio sem consultar.
O teste de `AutoMigrate` parte de banco povoado da versão atual, como sempre.

## 6. Segurança (o que muda no modelo de ameaças)

1. **Isolamento**: toda consulta e toda escrita filtram por `household_id` do **token**; id de conta,
   categoria ou lançamento de outra casa responde 404 (BOLA — docs/SEGURANCA.md §2).
2. **Escrita pesada**: `/investments/detect` entra na mesma classe de rate limit da importação e do
   auto-categorize, com teto duro de 10.000 candidatos e recusa 422 — nunca execução parcial.
3. **Consulta limitada por construção**: a série é fixa em 12 meses e a lista é paginada; o cliente não
   consegue pedir uma varredura ilimitada do histórico.
4. **Log e auditoria sem dado sensível**: os eventos `transaction.investments_detected` e
   `transaction.investments_overwritten` guardam o mês; as contagens ficam na resposta e no log
   estruturado da borda. Descrição de lançamento e palavra-chave nunca entram em log nem em auditoria.
5. **Risco novo, declarado**: excluir aporte do relatório de despesa cria um jeito de **esconder
   gasto** — marcar uma despesa grande como investimento a faz sumir do relatório. Mitigação: o
   lançamento continua visível em `/lancamentos` e na tela de investimentos, e toda troca de categoria
   já é auditada com autor. Não há mitigação adicional nesta entrega.
6. **Sem dado novo sensível**: a feature não guarda saldo de corretora, número de conta de investimento
   nem qualquer informação além do que o lançamento já tinha.

## 7. Critérios de aceite (viram os testes do `qa-testes`)

1. Categoria de natureza `investment`/`redemption` é criada, listada, renomeada, arquivada e excluída
   como qualquer outra; terceiro nível recusado com `ErrTooDeep`; teto de 200 por casa vale igual.
2. Despesa aceita categoria `investment`; receita aceita `redemption`. Despesa com categoria
   `redemption` → 400 `CATEGORY_KIND_MISMATCH`; receita com `investment` → 400; transferência com
   qualquer uma das duas → 400.
3. Trocar a natureza de uma categoria `expense` **em uso** para `investment` funciona e não altera
   nenhum lançamento; trocar `expense` → `income` em uso continua 422 `KIND_LOCKED`.
4. Palavra-chave já usada numa categoria de despesa, cadastrada numa de investimento → 409
   `KEYWORD_TAKEN` com `fields.keyword` e `fields.ownerId`.
5. Importação: linha de despesa cuja descrição bate com palavra-chave de categoria de investimento
   recebe `suggestedCategoryId` dela; linha de despesa **nunca** recebe sugestão de `redemption`.
6. `GET /investments?month=` numa casa sem nenhuma categoria de investimento devolve zeros, `items:
   []`, `series` com 12 itens zerados e **não** executa consulta com `IN ()`.
7. Um aporte de R$ 2.000 em setembro: **não** aparece em `/reports/by-category?kind=expense`, **não**
   entra em `expenseCents`, **entra** em `investedCents`, **aparece** em `GET /investments` e o saldo
   da conta continua descontado em R$ 2.000.
8. `POST /investments/detect` com `dryRun: true` não escreve nada (conferido relendo os lançamentos);
   com `dryRun: false` marca exatamente os itens listados; a segunda execução marca 0.
9. `detect` não altera lançamento que já tem categoria quando `overwriteCategorized` é falso ou
   ausente; com `true`, troca só as de natureza `income`/`expense` e nunca desfaz uma marcação de
   investimento.
10. `detect` nunca toca lançamento excluído, de transferência, ou de outra casa (teste com duas casas e
    o mesmo mês).
11. Mês com mais de 10.000 candidatos → 422 e **nada** escrito.
12. Abuso: `month` malformado → 400; `pageSize` acima do teto → 400; cursor adulterado → 400; rajada de
    `detect` → 429 pela classe de escrita pesada.
13. Playwright: o item "Investimentos" leva à tela; depois de uma importação com "CDB" cadastrado, a
    tela mostra o aporte no mês e o número do ano; o mesmo lançamento não aparece no relatório de
    despesas.

## 8. Riscos e limites aceitos

- `categories.kind` fica **exatamente** no limite de `varchar(10)`: uma quinta natureza mais longa
  exigirá alargar a coluna (portátil nos quatro dialetos, mas é migração).
- Os números de receita e despesa **mudam de valor** para quem já usa o app com uma categoria
  "Investimentos" de natureza `expense` — no instante em que a natureza for trocada. É consequência
  direta da decisão do usuário, e a troca é explícita e auditada.
- A tela não sabe **onde** o dinheiro foi investido (qual ativo, qual corretora), só quanto e quando. É
  o escopo pedido: controle, não carteira.

## 9. Emenda necessária ao `PLANOS.md`

O §1.3 do `PLANOS.md` diz que o v1 é "sem investimentos, patrimônio ou metas de longo prazo". Esta spec
**não** revoga a linha inteira: patrimônio e metas de longo prazo continuam fora. O que passa a existir
é o **controle de fluxo** — quanto entrou e saiu de investimento por mês. O `arquiteto` registra a
correção no `PLANOS.md` §1.3 no mesmo formato da correção de 16/09/2026 (o histórico fica, o texto
muda) e abre o **ADR-029** para a decisão "natureza de categoria como marcação de investimento, sem
tipo de lançamento novo e sem conta de carteira".

## 10. Emenda de 18/09/2026 — o texto da faixa do mês (achado do QA)

A §3.5.2 desta spec escreveu a faixa do mês de `/lancamentos` como *"e R$ 2.000,00 investidos"*. A
implementação diz outra coisa:

```
Entrou 5.300,00 · Saiu 3.100,00 · Resultado +2.200,00
Fora destes números: 2.000,00 em aportes · 850,00 em resgates
```

**Vale o texto da implementação**, que é o da seção **E7 (g)** de `docs/DESIGN.md` — posterior a esta
spec e mais específica. A razão do `designer-ui` para a forma: um quarto item depois de `Resultado`
disputaria o fim da frase com o único número da faixa que leva tom e sinal, então a explicação entra
como **segunda linha subordinada, com o motivo à frente dos números**, e existe só quando
`investedCents + redeemedCents > 0`.

A **exigência** da §3.5.2 continua valendo inteira, e é ela que importa: o total do mês não pode
encolher sem explicação. A mitigação está cumprida — o QA verificou a exibição, não só o campo.
