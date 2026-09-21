# Spec 0005 — Palavras-chave: categorização automática e transferências internas (entrega E2c)

**Data:** 17/09/2026 · **Fase:** 4 (núcleo financeiro) · **Entrega:** E2c (nova — entre a E2 e a E3 do
`PLANOS.md`; a E3 "contas fixas" passa a ser a spec 0006)
**ADRs relacionados:** ADR-013 (sem FK física), ADR-016 (transferência como par), ADR-017 (saldo
derivado), ADR-024 (importação em duas fases), ADR-025 (deduplicação em camadas).
**ADR proposto:** ADR-026 — classificação por palavras-chave é **determinística, sem IA, em Go**, num
pacote folha testável por tabela (o `arquiteto` registra em `docs/ARQUITETURA.md`).

> Esta spec é **normativa**. Onde ela e a documentação geral divergirem, vale a documentação geral
> (`AGENTS.md` e `docs/SEGURANCA.md`), e o desvio deve ser reportado, não implementado em silêncio.
> Paga a dívida declarada na spec 0004 §1.2: "regras de auto-categorização exigem spec própria".

---

## 1. Problema

Todo lançamento importado nasce **sem categoria** (spec 0004, D3) e toda transferência entre as contas
da casa nasce como **receita numa conta e despesa na outra**, inflando os dois relatórios. A pessoa
precisa classificar linha por linha, todo mês, o mesmo "Mercado do seu José".

## 2. Escopo

### 2.1 Entra

- **Palavras-chave em categoria e em conta.** Lista de até 20 por item, gravada em tabela própria,
  editada nos diálogos já existentes de categoria e de conta.
- **Pacote `internal/textmatch`** (folha, sem dependência do projeto além de `textnorm`): dado um texto
  e um conjunto de palavras-chave, devolve a melhor correspondência com **pontuação de 0 a 100**.
  Sem IA, sem rede, sem regex do usuário: o algoritmo da §3 e nada mais.
- **Importação — análise (fase 1):** cada linha ganha `suggestedCategoryId` + `matchScore` +
  `matchedKeyword`; linha cuja descrição bate com a palavra-chave de **outra** conta da casa vira
  `transferencia_interna` (status novo) com `suggestedCounterpartAccountId`; quando a outra perna já
  existe, vira `transferencia_ja_registrada` (status novo) apontando para ela.
- **Importação — confirmação (fase 2):** a categoria sugerida é o **default** da linha; a decisão pode
  trocá-la ou limpá-la. Transferência detectada fica **barrada até confirmar** (mesma regra do
  pagamento de fatura), com a contraparte pré-preenchida e um botão "aceitar todas as transferências
  sugeridas" na revisão.
- **Aprender na revisão:** ao escolher manualmente a categoria de uma linha sem sugestão, a tela
  oferece "adicionar *palavra* a esta categoria" (palavras da própria descrição, um clique).
- **Categorizar lançamentos sem categoria:** ação em `/lancamentos` que aplica as palavras-chave aos
  lançamentos `income`/`expense` **sem categoria** do mês, com prévia e confirmação. Nunca sobrescreve
  categoria já escolhida.
- **Transferências internas:** `GET /transfers` + tela `/transferencias` no menu — lista as
  transferências do mês, filtra por par de contas e mostra, para o par, os totais em cada sentido, o
  líquido, e o saldo de cada conta no fim do mês.

### 2.2 Fica explicitamente de fora

- **CRUD manual de lançamento** e rotas `POST/PATCH/DELETE /transfers` → E2b. Quando a criação manual
  entrar, ela sugere categoria ao digitar a descrição **usando o mesmo pacote**, sem nada novo aqui.
- **Regras por valor, data, conta de origem ou regex**, pesos por categoria, aprendizado automático sem
  clique, sinônimos embutidos ("mercado" ≈ "supermercado" só acontece pela §3, nunca por dicionário).
- **Conciliação automática** entre extratos: o pareamento da outra perna é **proposto** (§4.2.3), nunca
  aplicado em silêncio — mantém a regra da spec 0004 §1.2.
- **Recategorizar em massa** lançamentos que **já têm** categoria; edição de palavras-chave em massa;
  importar/exportar palavras-chave.
- **Detecção de transferência sem palavra-chave** (só por valor e data espelhados) → backlog: é
  conciliação, exige spec própria.

## 3. Algoritmo de correspondência (normativo — `internal/textmatch`)

Tudo opera sobre texto **normalizado** por `textnorm.Normalize` (sem acento, minúsculas, espaços
colapsados): a descrição já está em `description_norm`, e a palavra-chave em `keyword_norm`.

**Tokenização da descrição:** separa em palavras por qualquer caractere que não seja letra ou dígito;
descarta palavras de 1 rune e uma lista curta e **fechada** de palavras vazias em português (`de do da
dos das e o a os as em no na nos nas um uma por para com sem seu sua ltda me sa eireli epp`). "Mercado
do seu José" → `mercado`, `jose`. A lista vive no pacote, com teste; não é configurável.

**Pontuação de uma palavra-chave contra a descrição** — a primeira regra que casar decide:

| # | Regra | Pontuação | Exemplo (descrição ~ palavra-chave) |
|---|---|---|---|
| 1 | **Exata**: a palavra-chave (ou a frase inteira, se tiver mais de uma palavra) aparece na descrição como palavra inteira, com fronteira dos dois lados | **100** | `supermercado extra` ~ `supermercado` · `uber eats` ~ `uber` · `netflix.com` ~ `netflix` |
| 2 | **Aproximação**: a maior substring comum entre uma palavra da descrição e a palavra-chave tem **≥ 5 runas**. Pontuação = `100 − 30·(sobra da palavra maior) − 40·(sobra da palavra menor)`, onde "sobra" é a fração de runas fora da parte comum | 0–99 | `mercado do seu jose` ~ `supermercado` → **88** · `mercadinho` ~ `mercado` → **82** · `supermerc` ~ `supermercado` → 93 · `farmac` ~ `farmacia` → 93 · `hipermercado` ~ `mercado` → 88 |
| 3 | **Erro de digitação**: palavras com **≥ 6 runas** a distância de edição (Damerau-Levenshtein) **exatamente 1**. Pontuação = `100 − 100/len(maior)`, arredondado | 83–97 | `padoria` ~ `padaria` → 86 |

Palavra-chave com mais de uma palavra só casa **inteira** (regra 1) ou com **todas** as suas palavras
casando individualmente (pontuação = a menor delas). Palavra-chave com menos de 5 runas só casa pela
regra 1 — `c6`, `uber`, `pix` nunca entram em aproximação.

**Escolha:** a pontuação de uma categoria (ou conta) é a **maior** entre as suas palavras-chave; vence
a categoria de maior pontuação **desde que ≥ 80** (limiar fixo desta entrega, constante nomeada). Empate
entre categorias diferentes na pontuação máxima → **nenhuma sugestão** (ambíguo é pior que vazio em dado
financeiro). A resposta traz a palavra-chave que decidiu.

**Falsos positivos conhecidos e aceitos** (medidos no protótipo de 17/09/2026): `imposto de renda` ~
`posto` → 91; `amazonas turismo` ~ `amazon` → 93; `mercadoria` ~ `mercado` → 91. A saída é a própria
regra 1: cadastrar `imposto` na categoria certa faz o 100 vencer o 91. Nunca resolver com lista de
exceções no código. **Não casam** (medido): `padaria` ~ `farmacia` (0), `paraiba` ~ `padaria` (0),
`casamento` ~ `casa` (0 — "casa" tem 4 runas), `drogasil` ~ `drogaria` (74).

**Desempenho:** 10.000 linhas × 1.000 palavras-chave da casa em **≤ 2 s** no SQLite local. A regra 1 é
consulta em mapa; as regras 2 e 3 só rodam para pares que compartilham ao menos um trigrama, e o
resultado é memorizado por `description_norm` (extrato repete descrição).

## 4. Comportamento

### 4.1 Cadastro de palavras-chave

1. No diálogo de categoria e no de conta, um campo de "fichas" (`KeywordsField`) lista as palavras-chave;
   Enter ou vírgula adiciona, Backspace/× remove. Colar "padaria, panificadora" cria duas.
2. Validação no backend: 2–40 runas depois de normalizada; letras, dígitos, espaço e `& . - / '`;
   **única por casa dentro do seu tipo** (a mesma palavra em duas categorias é ambiguidade construída —
   409 com a categoria que já a tem). Categoria e conta são conjuntos independentes.
3. Máximo de 20 por categoria/conta; a lista enviada **substitui** a anterior (semântica de `PATCH`
   com o campo presente; ausente = não mexe).
4. Categoria/conta **arquivada** mantém as palavras mas **não participa** da correspondência; excluída
   leva as palavras junto, no mesmo `UnitOfWork` (não há FK física — ADR-013).
5. Alterar palavras-chave é auditado no evento de atualização que já existe (`category.updated` /
   `account.updated`), sem gravar as palavras no log.

### 4.2 Importação

**4.2.1 Análise** (depois da deduplicação da spec 0004 §4, por linha aproveitada):

1. Se a descrição bate (≥ 80) com palavra-chave de **outra conta ativa** da casa (nunca a conta do
   lote): se a linha é `novo`, vira `transferencia_interna`; se é `pagamento_de_fatura`, só ganha
   `suggestedCounterpartAccountId` (o status fica). Conta batendo vence categoria batendo: o dinheiro não
   saiu da casa.
2. Linha `transferencia_interna` procura a **outra perna já existente**: lançamento `transfer_*` da
   conta do lote, com contraparte igual à conta sugerida, mesmo valor, data a ±3 dias
   (`DedupWindowDays`), não excluído e ainda não apontado por outra linha deste lote. Encontrou → status
   `transferencia_ja_registrada` e `matchTransactionId` = a perna.
3. Toda linha aproveitada (inclusive as marcadas, para o caso de serem liberadas) recebe
   `suggestedCategoryId`/`matchScore`/`matchedKeyword` pela §3 contra as categorias **ativas** da
   **mesma natureza** do `kind` da linha (despesa não sugere categoria de receita). Linha
   `transferencia_*` não recebe categoria.

**4.2.2 Revisão** (`/importar/{id}/revisar`):

- Linha com sugestão mostra a categoria **já selecionada** no seletor e a pontuação como texto
  ("88% · supermercado"), nunca só cor. 100% não mostra pontuação. A pessoa troca ou limpa no seletor.
- Linha sem sugestão, ao ganhar categoria manual, mostra as palavras da descrição como fichas: clicar
  em uma chama `PATCH /categories/{id}` acrescentando a palavra às existentes (a tela lê a categoria
  antes de escrever, para não perder palavras — é o mesmo `PATCH` de substituição da §4.1.3). Sucesso
  **não** re-analisa o lote (a análise já passou); o efeito é da próxima importação em diante.
- Bloco "Transferências detectadas": `transferencia_interna` aparece **barrada** com a contraparte
  pré-selecionada e o texto "parece transferência para *Conta X* (88% · nubank)"; ações permitidas:
  `transfer` (par, ADR-016), `import` (entra como receita/despesa comum, com categoria) e `skip`.
  Botão "aceitar todas as transferências sugeridas" marca `transfer` em todas as linhas do bloco.
- `transferencia_ja_registrada` aparece com "já registrada em *dd/mm* pela conta X" e ação default
  **`link`**: não cria movimento de dinheiro — só grava na perna existente o `external_id`/`dedup_key`
  desta linha, para a reimportação cair em `duplicado_exato`. Ações permitidas: `link`, `skip`.
  `import` **não** é permitido: seria a duplicata que a spec 0004 existe para impedir.

**4.2.3 Confirmação:**

- Linha não citada em `decisions` entra com a categoria sugerida. Para entrar **sem** categoria apesar
  da sugestão, a decisão traz `"categoryId": null` explícito (tri-estado como `OptionalDay`: ausente =
  mantém sugestão; nulo = limpa; valor = usa).
- `action: "transfer"` passa a ser aceito também em `transferencia_interna`; `counterpartAccountId`
  ausente usa a sugerida; presente é validado como hoje (§7).
- `action: "link"` só em `transferencia_ja_registrada`; grava na perna existente `external_id`,
  `dedup_key` e `import_batch_id` desta linha, dentro do `UnitOfWork` do lote. Resposta ganha
  `"linked": n`.

### 4.3 Categorizar lançamentos sem categoria (`/lancamentos`)

1. Quando o mês tem lançamentos sem categoria, o aviso "N lançamentos sem categoria" (spec 0004 D3)
   ganha o botão "Categorizar automaticamente".
2. Clique → `POST /transactions/auto-categorize` com `{ "month": "2026-09", "dryRun": true }` → diálogo
   com a prévia: "12 de 18 recebem categoria" e a lista (descrição → categoria · pontuação). Linhas
   abaixo do limiar ou ambíguas aparecem em "6 continuam sem categoria" com o motivo.
3. Confirmar → mesmo pedido com `dryRun: false`; o servidor **recalcula** (não confia na prévia) e só
   escreve onde `category_id IS NULL` e `kind IN (income, expense)`. Resposta: `{ "categorized": 12,
   "unmatched": 6 }`. Auditado como `transaction.auto_categorized` com o mês e as contagens.
4. Idempotente: rodar de novo no mesmo mês categoriza 0.

### 4.4 Transferências internas (`/transferencias`)

1. Item novo no menu, ao lado de Lançamentos. Seletor de mês (o `MonthNavigator` da casca) e dois
   seletores de conta: "entre *Conta A* e *Conta B*" (opcionais; um só filtra tudo que toca A).
2. `GET /transfers?month=&accountId=&counterpartAccountId=` — cada **par** (ADR-016) vira **uma** linha:
   data, de → para, valor, descrição, origem (manual/importação).
3. Painel do par (quando os dois filtros estão preenchidos) com quatro números do servidor: A→B no mês,
   B→A no mês, **líquido** (A→B − B→A, com sinal e direção em texto) e o **saldo de cada conta no fim do
   mês**. Sem os dois filtros, o painel lista todos os pares do mês com os mesmos totais.
4. Estado vazio: "Nenhuma transferência entre as suas contas em *setembro de 2026*", com link para
   importar.

## 5. Contrato (rascunho para o `arquiteto` refinar no OpenAPI)

**Categorias e contas** — `Category` e `Account` ganham `keywords: string[]` (sempre presente, `[]`
quando vazio); `POST` e `PATCH` aceitam `keywords` (≤ 20 itens, cada 2–40 runas). Erros: 400
`VALIDATION` (`fields.keywords[i]`), 409 `KEYWORD_TAKEN` (`fields.keyword`, `fields.ownerId`).

**Importação** — `ImportRow` ganha `suggestedCategoryId`, `matchScore` (0–100 ou nulo),
`matchedKeyword`, `suggestedCounterpartAccountId` (nulos quando não há). `status` ganha
`transferencia_interna` e `transferencia_ja_registrada`; `counts` ganha as duas chaves; `action` ganha
`link`; a decisão aceita `categoryId: null`; a resposta do confirm ganha `linked`.

**Lançamentos** — `POST /transactions/auto-categorize` · corpo `{ month, dryRun }` · 200
`{ month, categorized, unmatched, items: [{ id, description, categoryId, categoryName, matchScore,
matchedKeyword }], unmatchedItems: [{ id, description, reason: "below_threshold" | "ambiguous" }] }`
(as listas só em `dryRun`; teto de 500 itens listados, contagens sempre completas). Rate limit da
classe de escrita pesada (a mesma da importação).

**Transferências** — `GET /transfers?month=YYYY-MM&accountId=&counterpartAccountId=&cursor=&pageSize=`
· 200:

```json
{ "items": [ { "groupId": "...", "occurredOn": "2026-09-05", "fromAccountId": "...", "toAccountId": "...",
               "amountCents": 150000, "description": "Transferência enviada pelo Pix", "source": "import" } ],
  "pairs": [ { "accountAId": "...", "accountBId": "...", "aToBCents": 300000, "bToACents": 50000,
               "netCents": 250000, "count": 3 } ],
  "balances": [ { "accountId": "...", "balanceAtMonthEndCents": 123456 } ],
  "nextCursor": null }
```

`month` filtra por `competence_month` como `GET /transactions`; o saldo no fim do mês é de **caixa**
(`occurred_on` ≤ último dia do mês), somado no servidor. `accountId`/`counterpartAccountId` de outra
casa → 404, como toda referência a recurso.

## 6. Dados (rascunho para o `arquiteto-dados`)

| Tabela / coluna | Tipo | Regra |
|---|---|---|
| `category_keywords` (nova) | `id` v36 pk · `household_id` v36 nn · `category_id` v36 nn · `keyword` v40 nn · `keyword_norm` v40 nn · `created_at` | `ux_category_keywords_norm (household_id, keyword_norm)` **único** · `ix_category_keywords_cat (household_id, category_id)` |
| `account_keywords` (nova) | idem, com `account_id` | `ux_account_keywords_norm (household_id, keyword_norm)` · `ix_account_keywords_acc (household_id, account_id)` |
| `import_rows.suggested_category_id` | v36 null | preenchido na análise |
| `import_rows.match_score` | int null | 0–100 |
| `import_rows.matched_keyword` | v40 null | a `keyword` (forma exibível), não a norm |
| `import_rows.suggested_counterpart_account_id` | v36 null | preenchido na análise |

Tabela própria, e não coluna JSON/texto na categoria: a unicidade por casa precisa de índice único
**portátil**, e JSON não é consultável igual nos quatro dialetos. `household_id` repetido nas duas
tabelas para o filtro de isolamento nunca depender de join. Sem FK física (ADR-013): exclusão limpa as
palavras no `UnitOfWork`. `AutoMigrate` partindo de banco v3 **povoado**, não só vazio.

## 7. Segurança (o que muda no modelo de ameaças)

- **Palavra-chave é entrada do usuário**: validada por allowlist de caracteres e tamanho, normalizada no
  servidor, nunca em log nem em auditoria (só contagens). Erro de unicidade só cita recurso da **mesma
  casa**.
- **BOLA (S1)**: `category_keywords`/`account_keywords` sempre filtrados por `household_id` do token; a
  correspondência só enxerga as palavras da casa do lote; `suggestedCategoryId` e
  `suggestedCounterpartAccountId` são **calculados no servidor** — o cliente pode trocá-los, e o valor
  trocado passa pela mesma validação de casa de hoje (404 para recurso alheio).
- **`link` é o vetor novo**: escreve numa linha existente. Só é aceito quando `matchTransactionId` foi
  gravado **pela análise** para aquela linha (nunca vindo do corpo), e a perna é reconferida por
  `household_id` + `account_id` do lote na hora do commit.
- **`auto-categorize`**: só toca `category_id IS NULL` da própria casa; `month` validado (`YYYY-MM`);
  rate limit de escrita pesada; resposta não ecoa descrições de outra casa (impossível pelo filtro, e
  o teste de isolamento prova).
- **DoS por custo**: strings ≤ 40 runas, `O(n·m)` limitado, teto de 20 palavras por item e o teto
  existente de 200 categorias/50 contas por casa; a memorização por `description_norm` limita o custo ao
  número de descrições **distintas**.
- Sem dependência nova: distância de edição e substring comum são 40 linhas de Go cada, testadas por
  tabela.

## 8. Critérios de aceite (viram os testes do `qa-testes`)

1. Tabela da §3 reproduzida por teste unitário de `textmatch` — **cada** exemplo, os que casam e os que
   não casam, com a pontuação exata.
2. Empate na pontuação máxima entre duas categorias → sem sugestão; a mesma palavra em duas categorias →
   409 na gravação.
3. Palavra-chave de conta e de categoria batendo na mesma linha → `transferencia_interna` (conta vence).
4. Palavra-chave da **própria** conta do lote nunca gera transferência; conta arquivada nunca é
   contraparte sugerida; categoria arquivada nunca é sugerida; `income` nunca recebe categoria `expense`.
5. Importar o extrato de A (cria o par A→B) e depois o de B: a linha espelhada em B fica
   `transferencia_ja_registrada`; `link` grava a chave na perna existente e **reimportar B** cai em
   `duplicado_exato`; o saldo das duas contas não muda com o `link`.
6. Duas transferências iguais no mesmo dia entre A e B: cada linha do arquivo casa com **uma** perna
   distinta; a terceira vira `transferencia_interna`.
7. Confirm com `categoryId: null` explícito grava sem categoria apesar da sugestão; decisão ausente grava
   a sugerida; `categoryId` de outra casa → 404 e nada gravado (tudo ou nada).
8. `action: "link"` em linha que não é `transferencia_ja_registrada` → 400; `link` com
   `matchTransactionId` forjado no corpo é ignorado (o corpo não tem esse campo) e a perna usada é a da
   análise; perna de outra casa (banco adulterado) → lote falha inteiro, nada parcial.
9. `auto-categorize` com `dryRun` não escreve nada (contagem de linhas idêntica antes e depois); sem
   `dryRun` escreve só em `category_id IS NULL`; rodar duas vezes categoriza 0 na segunda; auditoria tem
   um evento por execução real, sem descrição alguma.
10. `GET /transfers` traz cada par **uma vez**; `pairs` fecha com a soma de `items`; `netCents` ==
    `aToBCents − bToACents`; `balanceAtMonthEndCents` é igual ao `balanceCents` de `GET /accounts`
    quando o mês é o corrente; `accountId` de outra casa → 404 com corpo idêntico ao de id inexistente.
11. Desempenho: análise de 10.000 linhas com 1.000 palavras-chave ≤ 2 s (SQLite local, teste marcado
    como lento).
12. Frontend: `KeywordsField` aceita Enter/vírgula/colar, remove com Backspace e ×, é navegável por
    teclado e anuncia a contagem; a revisão mostra pontuação em texto; "aceitar todas" marca `transfer`
    em todas as linhas do bloco e em nenhuma outra; a tela `/transferencias` mostra o líquido com direção
    em texto, não só sinal. Playwright: cadastrar palavra-chave → importar → linha vem sugerida →
    confirmar → lançamento tem a categoria.
13. `tsc`, `biome`, `api:check`, `go vet`, `go test -race`, `govulncheck` e `gosec` limpos; revisão de
    segurança **APROVADO**.

## 9. Riscos e limites aceitos

| Risco | Mitigação |
|---|---|
| Falso positivo por contenção ("imposto" ⊃ "posto") | regra 1 vence a 2; a revisão mostra a palavra que decidiu; auto-categorize sempre tem prévia |
| Transferência não detectada quando o extrato do outro banco não cita o nome da conta | limite declarado (§2.2): sem palavra-chave não há detecção; a linha entra como hoje |
| Custo da correspondência crescer com a casa | tetos da §7 e critério 11 medido em teste |
| Palavra-chave curta genérica ("pix") casar tudo | só regra 1 abaixo de 5 runas; documentar no campo: "prefira o nome do estabelecimento" |

## 10. Emenda de 17/09/2026 — refinamentos do plano de execução (aprovados pelo usuário)

Refinamentos propostos pelo `arquiteto` ao planejar a entrega e **aprovados pelo usuário em 17/09/2026**. Onde
contradizem o texto acima, vale a emenda.

1. **Elegibilidade a `transferencia_interna`** (§4.2.1.1): linha cujo status de deduplicação *entra por
   default* (`novo` **e** `repetido_no_arquivo`), não só `novo` — sem isso o critério 6 falha em arquivo de
   chave derivada (três linhas idênticas são `novo, repetido, repetido`).
2. **Pagamento de fatura também procura a perna já existente** (§4.2.1.2 estendida): linha
   `pagamento_de_fatura` com contraparte sugerida por palavra-chave passa pelo mesmo pareamento; encontrando
   a perna, vira `transferencia_ja_registrada` (ação `link`). Fecha o caso "fatura importada antes com
   `transfer`, extrato importado depois propõe um segundo par".
3. **Validação extra de palavra-chave** (§4.1.2): precisa produzir ≥ 1 palavra útil depois da tokenização da
   §3 — só stopwords ("de", "ltda") ou só letras soltas ("c & a") é 400.
4. **`matchScore`/`matchedKeyword`** descrevem a categoria quando há `suggestedCategoryId` e a contraparte em
   linha `transferencia_*`; em `pagamento_de_fatura` a pontuação da contraparte não é exposta.
5. **Auditoria do `auto-categorize`**: o mês vai em `entity_id`; as contagens ficam no `slog` e na resposta
   (o `audit_log` não tem campo de detalhe).
6. **Colunas além da §6**: `position` nas tabelas de palavras-chave (ordem de cadastro); `linked_count` e
   `transfer_pairs_count` em `import_batches`; campo `matchOccurredOn` em `ImportRow` (para "já registrada
   em dd/mm" sem N+1).
7. **Pacote `internal/classify`** (cola que carrega palavras da casa e monta os matchers) além do
   `textmatch`, que continua folha.
8. **Rate limit próprio** do `auto-categorize`: 60/h por casa (cada uso gasta prévia + confirmação).
9. **Nomes de conta** em `TransferItem`, `TransferPair` e `TransferBalance`.
10. **Renumeração**: E3 → spec 0006, E4 → 0007, E5 → 0008, E6 → 0009, E7 → 0010.

## 11. Emenda de 17/09/2026 — atalho de categorização em `/lancamentos` (pedido do usuário)

**Pedido:** em `/lancamentos`, clicar em "Sem categoria" numa linha permite categorizar ali mesmo, com duas
saídas: **só este lançamento**, ou **este lançamento + adicionar uma palavra-chave à categoria escolhida e
reprocessar os lançamentos sem categoria do mês**.

**Comportamento:**
1. A célula "Sem categoria" de uma linha `income`/`expense` vira um controle (botão) que abre, na própria
   linha, o seletor de categoria (só categorias ativas e atribuíveis da mesma natureza do lançamento) e as
   fichas de palavras da descrição (mesma regra da §4.2.2: `tokenizar(description)`, no máximo 5, nunca só
   dígitos, nunca já presentes na categoria).
2. **Só este:** escolher a categoria e confirmar → `PATCH /transactions/{id}` com `{ "categoryId" }` →
   a linha passa a mostrar a categoria; a faixa "N sem categoria" diminui.
3. **Com palavra-chave:** escolher a categoria, clicar numa ficha e confirmar → (a) `PATCH /categories/{id}`
   acrescentando a palavra às existentes (mesma leitura-antes-de-escrever da §4.2.2); (b) `PATCH
   /transactions/{id}` neste lançamento; (c) `POST /transactions/auto-categorize` com `dryRun: false` no
   mês da tela → toast com o número real: "«mercado» adicionada a Alimentação · 7 lançamentos
   categorizados". 409 na palavra → toast com a dona e **nada mais é feito** (o lançamento não é
   categorizado; a pessoa decide). Nunca sobrescreve categoria já escolhida (regra da §4.3).
4. Escape/cancelar fecha sem gravar; foco volta ao controle da linha.

**Contrato (novo):** `PATCH /transactions/{id}` · corpo `{ "categoryId": "<uuid>" }` — **só** este campo
nesta emenda (o `PATCH` completo continua na E2b; campo desconhecido é 400) · 200 com o `Transaction`
atualizado · 404 para lançamento de outra casa (byte a byte igual ao inexistente) · 404 para categoria de
outra casa · 422 `BUSINESS_RULE` quando o lançamento é perna de transferência (`transfer_*` nunca tem
categoria) · 422 quando a natureza da categoria não combina com o `kind` ou a categoria está arquivada ·
auditoria `transaction.updated` (id do lançamento; sem valor, sem descrição).

**Fora:** editar valor/data/descrição/conta (E2b); recategorizar em massa o que já tem categoria (§2.2).
**Atualizado em 18/09/2026:** a recategorização **individual** — trocar a categoria de uma linha que já tem
uma — saiu deste *Fora* e entrou na §19, sem mudança de contrato. Massa continua fora.

**Critérios de aceite:** (a) `PATCH` com `categoryId` de outra casa → 404 e nada gravado; em perna de
transferência → 422; `income` com categoria `expense` → 422; arquivada → 422; campo além de `categoryId`
→ 400; (b) só-este não altera nenhum outro lançamento; (c) com-palavra: a palavra aparece na categoria,
este lançamento fica categorizado e o `auto-categorize` roda uma vez no mês; 409 na palavra deixa tudo
como estava; (d) teclado: abrir com Enter/Space, `<select>` nativo, Escape fecha e devolve o foco;
(e) Vitest dos dois caminhos e do 409; E2E "sem categoria → escolher → com palavra → outros do mês ganham
categoria".

## 12. Emenda de 17/09/2026 — palavras-chave em grupo com subcategorias (achado do QA)

Um grupo com subcategorias ativas **não recebe lançamento diretamente** (o seletor só oferece folhas e grupos
sem filhas), então palavra-chave nele nunca sugeriria nada. Regra explícita: `POST/PATCH` com `keywords`
não vazio em grupo que tem subcategoria ativa → 400 `VALIDATION` em `fields.keywords` ("Palavras-chave
ficam nas subcategorias"); o diálogo de categoria, para grupo com filhas, esconde o campo e mostra a nota.
Grupo sem filhas continua aceitando. Caso residual documentado: grupo com palavras que **depois** ganha uma
subcategoria mantém as palavras inertes (não sugerem) até serem movidas à mão — o diálogo do grupo passa a
mostrar a nota com a contagem ("3 palavras-chave sem efeito enquanto o grupo tiver subcategorias").

Também corrigido: `POST /imports/{id}/confirm` com `transfer` para contraparte arquivada responde 422 com
`fields.counterpartAccountId` (antes apontava `accountId`).

## 13. Emenda de 17/09/2026 — reprocessar transferências sobre lançamentos já gravados (pedido do usuário)

**Problema encontrado no uso real (banco de desenvolvimento, 17/09/2026):** os extratos da C6 e da Nubank
foram importados **antes** de as palavras-chave de conta existirem. Os Pix entre as duas contas entraram
como `expense` numa e `income` na outra; `/transferencias` fica vazia e `/lancamentos` infla receita e
despesa em R$ 8.000 no mês. Reimportar não resolve (cai em `duplicado_exato`) e **não existe nenhum
caminho para reclassificar o que já está gravado**. Além disso, a semântica da §4.2.1 ("palavra-chave de
OUTRA conta") não cobre o caso mais comum da casa — Pix entre contas próprias: "Pix enviado - BRUNO
RIBEIRO BLANCK" não diz para qual banco o dinheiro foi, e a pessoa, naturalmente, cadastrou a palavra na
conta onde o texto **aparece** — que a análise exclui.

**Decisão (ADR-028):** transferência é um **tipo de lançamento à parte** (`transfer_out`/`transfer_in`,
ADR-016) e **nunca entra em receita, despesa nem em nenhum total** — isso já vale e é reafirmado. O que
entra é uma ação de **reprocessamento** que converte pares já gravados no tipo certo.

### 13.1 Comportamento

1. Botão **"Reprocessar transferências"** na tela `/transferencias` (e chamada no estado vazio). Abre um
   diálogo com **prévia** (`dryRun: true`) e só grava ao confirmar (`dryRun: false`) — o mesmo desenho
   do "Categorizar automaticamente" (§4.3).
2. **Candidata**: lançamento vivo `income`/`expense` da casa, no mês de **competência** da tela, cuja
   descrição bate (≥ 80, §3) com palavra-chave de conta ativa da casa — **de outra conta** (semântica da
   §4.2.1: a contraparte é conhecida) **ou da própria conta** (semântica nova: "isto é transferência
   entre as minhas contas", contraparte desconhecida). Palavra de conta arquivada não participa.
3. **Espelho**: para cada candidata `T`, o servidor procura `M` — lançamento vivo `income`/`expense`
   (nunca perna de transferência), em **outra** conta ativa da casa, de **sentido oposto** (`expense` ↔
   `income`), **mesmo valor**, `occurred_on` a **±3 dias** (`DedupWindowDays`), ainda não reivindicado
   nesta execução. Regras de contraparte:
   - `T` bateu com palavra de **outra** conta `K` → `M` tem de estar em `K` (bater com palavra não é
     exigido de `M`; `T` já nomeou a conta).
   - `T` bateu com palavra da **própria** conta → `M` tem de estar em outra conta **e** ser candidata
     também (bater com palavra da própria conta de `M`, ou com palavra da conta de `T`). Sem essa
     exigência, "Pix enviado a mim mesmo" casaria com qualquer receita de mesmo valor na vizinhança — o
     falso positivo que a §2.2 recusou.
   - Entre várias `M` possíveis: prefere a que também é candidata; depois a de menor distância em dias;
     depois menor `occurred_on`; depois menor id. Determinístico. `T` é percorrida em ordem
     `(occurred_on, id)`; cada `M` só é reivindicada uma vez.
4. **Conversão (par)**: `T` e `M` viram **um par** com `transfer_group_id` novo: a de `expense` vira
   `transfer_out`, a de `income` vira `transfer_in`; `category_id` das duas vira nulo (transferência não
   tem categoria — ADR-016). **Nada mais muda**: valor, data, competência, descrição, `source`,
   `import_batch_id`, `external_id`, `dedup_key`, `statement_id` ficam como estão — a reimportação do
   mesmo arquivo continua caindo em `duplicado_exato`, e o saldo das duas contas **não muda** (−X em
   despesa vira −X em saída; +X em receita vira +X em entrada).
5. **Nunca inventa perna.** Candidata **sem espelho** fica como está e aparece na prévia em "parecem
   transferência, mas não têm par", com o motivo e a orientação ("importe o extrato da outra conta e
   reprocesse"). Criar perna sintética aqui duplicaria dinheiro na próxima importação da outra conta
   (a linha espelhada entraria como receita ao lado da perna inventada). É a diferença deliberada em
   relação ao `transfer` da importação (§4.2.3), onde a outra perna comprovadamente ainda não existe.
6. **Recálculo no servidor**: a confirmação recalcula tudo dentro de **uma** transação (nunca confia na
   prévia) e converte com `UPDATE` **condicional** — `WHERE household_id = ? AND id = ? AND
   deleted_at IS NULL AND transfer_group_id IS NULL AND kind = <esperado>` — conferindo **2 linhas
   afetadas por par**; qualquer coisa diferente desfaz a transação inteira (nada parcial). Idempotente:
   a segunda execução não encontra `income`/`expense` para os mesmos pares e responde 0.
7. Auditoria: **uma** entrada por execução real, `transaction.transfers_detected`, entidade
   `transaction_month`, id = o mês (mesma decisão da emenda §10.5). Contagens no `slog` e na resposta;
   descrição e valor nunca.
8. Rate limit próprio, por casa, 60/h (balde separado do `auto-categorize` — cada uso gasta prévia +
   confirmação).
9. Teto: `MaxTransferDetectRows = 10.000` candidatas por mês (422 acima; nunca execução parcial).

### 13.2 Contrato (o `arquiteto` refina no OpenAPI)

`POST /transfers/detect` · corpo `{ "month": "AAAA-MM", "dryRun": true|false }` (`dryRun`
**obrigatório**, como no auto-categorize) · 200:

```json
{ "month": "2026-08", "paired": 2, "unpaired": 1,
  "items": [ { "outTransactionId": "...", "inTransactionId": "...", "occurredOn": "2026-08-25",
               "fromAccountId": "...", "fromAccountName": "Bruno NuBank",
               "toAccountId": "...", "toAccountName": "Bruno C6",
               "amountCents": 300000, "description": "Pix enviado - BRUNO RIBEIRO BLANCK",
               "matchedKeyword": "Pix enviado - BRUNO RIBEIRO BLANCK", "matchScore": 100 } ],
  "unpairedItems": [ { "id": "...", "kind": "income", "accountId": "...", "accountName": "Bruno C6",
                       "occurredOn": "2026-09-09", "amountCents": 300000,
                       "description": "Pix recebido de Bruno Ribeiro Blanck",
                       "matchedKeyword": "Pix recebido de Bruno Ribeiro Blanck",
                       "reason": "no_mirror" } ] }
```

Em `dryRun`, `paired` é quantos pares **seriam** convertidos e as listas vêm preenchidas (teto de 500
cada; contagens sempre completas). Na execução real, `paired` é o número de pares **convertidos** e as
listas vêm vazias. `unpaired` conta candidatas sem espelho. `reason` é enum fechado: `no_mirror` (único
valor nesta emenda). `occurredOn` do item é o da perna de **saída**. Erros: 400 `VALIDATION`
(`month`/`dryRun`), 422 `BUSINESS_RULE` (teto), 429.

### 13.3 Dados

Sem migração: só `UPDATE` em `transactions` (`kind`, `category_id`, `transfer_group_id`,
`updated_at`). O espelho é lido por `occurred_on` na janela `[min(occurred_on das candidatas) − 3,
max(...) + 3]` da casa inteira (`ix_transactions_occurred`) — pela faixa **real** das candidatas, e não
pelas bordas do mês, porque a candidata é escolhida por competência e uma linha de fatura pode ter
`occurred_on` fora do mês (refinamento do `arquiteto`, ADR-028c). A competência de `M` pode ser outra.

### 13.4 Segurança

- BOLA: tudo pela casa do token; `T` e `M` são lidos pela casa e o `UPDATE` repete `household_id` no
  `WHERE`. O corpo **não** tem ids — o cliente não escolhe o que converter; a prévia é informativa.
- O par só nasce entre contas **ativas e distintas** da casa; `T` e `M` nunca são a mesma linha.
- Sem dependência nova; custo limitado pelos tetos (§7) e pela janela de um mês.

### 13.5 Critérios de aceite

1. Casa com `expense` na conta A e `income` na conta B, mesmo valor, mesmo dia, descrição batendo com a
   palavra da **própria** conta em cada uma → prévia mostra 1 par; confirmar converte as duas em
   `transfer_out`/`transfer_in` com o mesmo `transfer_group_id`, `category_id` nulo, e **mais nada**
   muda (`dedup_key`, `external_id`, `import_batch_id`, `statement_id`, valor, data, competência
   idênticos); saldo de A e de B idêntico antes e depois; `summary` do mês perde o valor da receita e
   da despesa; `GET /transfers` passa a listar o par.
2. Palavra de **outra** conta `K` em `T` → o espelho é procurado só em `K`; espelho em terceira conta é
   ignorado.
3. Palavra da **própria** conta em `T` e receita de mesmo valor em outra conta **sem** palavra → não
   pareia (`unpaired: 1`, `reason: no_mirror`); a mesma receita **com** palavra da própria conta → pareia.
4. Sem espelho (outra conta não importada) → `unpaired` conta a linha, nada é gravado, nenhuma perna
   sintética é criada.
5. Duas transferências iguais no mesmo dia → dois pares distintos; a terceira sem par fica `unpaired`.
6. `dryRun: true` não escreve nada (contagem e `updated_at` idênticos antes e depois) e não audita;
   `dryRun: false` audita **uma** vez; rodar de novo responde `paired: 0`.
7. (a) Candidata excluída ou convertida **entre a prévia e a confirmação** → o recálculo a ignora,
   `paired` diminui, sem erro; (b) linha alterada **entre a leitura e o `UPDATE` da mesma execução**
   (outra requisição no meio) → o `UPDATE` condicional não afeta 2 linhas → transação desfeita, resposta
   409 `CONFLICT` (código novo no conjunto fechado, mensagem genérica) e **nada** gravado.
8. Lançamento de outra casa com descrição e valor espelhados **nunca** é lido nem convertido (teste de
   isolamento pelas chamadas ao repositório e pelo resultado).
9. `month` inválido → 400; `dryRun` ausente → 400; corpo com campo desconhecido → 400.
10. Frontend: o botão abre o diálogo já pedindo a prévia; a lista mostra data, de → para, valor,
    descrição e a palavra que decidiu; "sem par" mostra o motivo em texto; confirmar mostra toast com
    o número **devolvido** e invalida `transfers`, `transactions` e `accounts`; Escape fecha e devolve
    o foco ao botão. Vitest dos dois caminhos; E2E: importar A e B → reprocessar → par aparece em
    `/transferencias` e some do total de `/lancamentos`.
11. `tsc`, `biome`, `api:check`, `go vet`, `go test -race`, `govulncheck`, `gosec` limpos; revisão de
    segurança **APROVADO**.

### 13.6 Fora desta emenda (backlog declarado)

- Ensinar a **análise da importação** a mesma semântica ("palavra da própria conta" + espelho já gravado
  em outra conta, convertendo a linha existente em vez de criar perna) — hoje a importação continua
  como na §4.2.1, e o reprocessamento fecha o mês em um clique depois.
- Linha que espelha uma perna **sintética** já existente na própria conta (ex.: fatura importada com
  `transfer` antes do extrato) — o `link` da importação cobre no momento da importação; fora dela fica
  para spec própria.
- Desfazer a conversão (voltar o par a receita/despesa).

## 13. Emenda de 17/09/2026 — grupo com subcategorias não recebe lançamento (achado do QA)

O QA das emendas §11/§12 mostrou que a invariante "grupo com subcategorias ativas não recebe lançamento
diretamente" era sustentada **só pelo seletor do frontend**: `PATCH /transactions/{id}` e
`POST /imports/{id}/confirm` gravavam `category_id` apontando para um grupo com filhas. Como a §12 recusa
palavra-chave nesse mesmo grupo justamente porque ele "não recebe lançamento", as duas regras precisam
concordar no servidor.

**Regra:** toda escrita que atribui categoria a um lançamento (`PATCH /transactions/{id}`, decisões do
confirm da importação, `defaultCategoryId`, e a categoria sugerida — que já é filtrada em `classify`)
recusa categoria que seja **grupo com ao menos uma subcategoria ativa**, com 422 em `fields.categoryId`
(no confirm o campo é `categoryId`, sem índice da decisão — o mesmo padrão que `ErrCategoryArchived` e
`ErrCategoryKindMismatch` já usam; mudar isso é mudar os três juntos). Grupo sem filhas continua válido, como hoje. Lançamentos já gravados
com categoria-grupo não são alterados nem migrados: a recusa vale só para escrita nova (a E6 os trata como
o balde do próprio grupo).

**Também corrigido:** o corpo do `PATCH /transactions/{id}` passa a recusar o nome do campo em outra caixa
(`CategoryId`) — o `encoding/json` é insensível à caixa por padrão, e o contrato declara só `categoryId`.

## 14. Emenda de 17/09/2026 — correções da revisão de segurança e do QA da §13

Achados da revisão adversarial (veredito inicial **BLOQUEADO**) e da validação do QA sobre a §13, com as
decisões tomadas. Onde contradizem a §13, vale esta emenda.

1. **`items` e `unpairedItems` são conjuntos DISJUNTOS** (achado A1, alta — prévia mentindo). A §13.1.5
   dizia que a candidata sem espelho "fica como está", mas a implementação listava a candidata no
   momento em que ela não achava espelho, **sem reivindicá-la** — e uma candidata posterior podia
   escolhê-la como espelho depois. A assimetria que dispara isso é a própria regra da §13.1.3: `T`
   recusar `U` não implica `U` recusar `T`. Efeito: a tela prometia "fica como está", a pessoa
   confirmava, e a linha era convertida com `category_id` anulado — **categoria destruída sem desfazer**
   —, além de `paired + unpaired` contar a mesma linha duas vezes. Regra explícita: a lista de "sem par"
   é montada **numa segunda passada**, depois de todos os pares fecharem, e contém apenas candidatas
   elegíveis que terminaram **fora** do conjunto de reivindicadas. Invariante permanente, com teste:
   `items` ∩ `unpairedItems` = ∅, e `candidatas_pareadas + unpaired ≤ total de candidatas`. Publicado na
   descrição de `TransferDetectResult`.
2. **Teto de trabalho do pareamento e rajada própria** (achado A2, alta — exaustão de CPU e do pool). O
   balde por `(kind, valor)` não limitava o produto **dentro** do balde: 10.000 candidatas × 20.000
   espelhos de mesmo valor custavam de 5,2 s a 19,3 s de CPU por requisição, sem `ctx.Err()` no
   pareamento, com rajada de 60 simultâneas (`burst = requests`) e segurando conexão do pool dentro da
   transação. Passa a valer: (a) `ctx.Err()` a cada 500 candidatas nas duas passadas; (b) o balde é
   indexado também por `occurred_on` (a janela é fixa em ±`DedupWindowDays`, então a busca varre 7 dias,
   não o balde inteiro), com os dias visitados na ordem do próprio desempate — o desempate publicado na
   §13.1.3 **não muda**; (c) teto duro `MaxTransferPairComparisons` para o caso patológico que o índice
   não separa (mesmo valor **e** mesmos dias), respondendo 422 como os demais tetos, nunca execução
   parcial; (d) `Rule.Burst` próprio da rota (3), mantendo a cota de 60/h. Medido depois: 0,174 s e
   0,190 s nos mesmos piores casos (30× e 101×).
3. **Despesa pertencente a uma fatura nunca é reprocessada** (achado A3 — decisão do usuário em
   17/09/2026). `SumByStatement` ignora `transfer_out` por desenho, então converter uma `expense` com
   `statement_id` **reduziria em silêncio o total cobrado na fatura** — número que a pessoa confere
   contra a cobrança do banco, e que a §13.1.4 promete não mexer. Regra: candidata exige
   `statement_id IS NULL` **quando** `kind = expense`, e a exclusão vale nos **dois** papéis (candidata e
   espelho) — o espelho também é convertido, e sem isso a compra do cartão viraria `transfer_out` pela
   porta do espelho. A `income` de fatura (o "Pagamento recebido" no cartão) **continua** candidata e
   continua virando `transfer_in`: é o pagamento da fatura, que é transferência por ADR-016. Efeito
   colateral conhecido e desejado: converter esse pagamento move o valor de `totalCents` (onde `income`
   na fatura é lido como **estorno** — spec 0004 D4) para `paidCents` (`transfer_in` = pagamento), com
   dívida líquida e saldos inalterados. É recomposição correta: a linha deixa de ser lida como estorno
   de compra e passa a ser o pagamento que quita. O número que a decisão do usuário protege — o total
   **cobrado**, que vem das compras — fica intocado porque a despesa saiu do reprocessamento.
4. **O `UPDATE` condicional confere a conta de cada perna** (achado A4). Além de casa, linha viva, `id`,
   `transfer_group_id IS NULL` e `kind` esperado, cada `UPDATE` carrega `account_id = ?`, e a conversão
   recusa as duas pernas na mesma conta antes de qualquer SQL. Defesa em profundidade: a invariante
   "contas diferentes" deixa de depender só da checagem em memória.
5. **Ordem de travamento determinística** (achado A5). Os pares são convertidos numa ordem **global**
   (por `min(id)` das duas pernas, desempate por `max`), e não na ordem das candidatas do mês: duas
   execuções simultâneas para meses diferentes cujas janelas de data se cruzam travariam as mesmas
   linhas em ordens opostas, e o deadlock do PostgreSQL sairia como 500 em vez de 409. Dentro do par, as
   duas pernas também são travadas em ordem de id (e não "saída depois entrada"), senão um par com
   `outID > inID` reintroduziria a inversão. **Não** se mapeia o deadlock nativo para 409: o GORM não o
   traduz, e detectá-lo exigiria códigos por dialeto (`40P01`/`1213`/`1205`) importando drivers
   específicos na camada de storage — com a ordem global, a causa apontada deixa de existir.
6. **Correção de redação:** o 422 desta rota usa `VALIDATION_FAILED`, não `BUSINESS_RULE` como diz a
   §13.2 — o conjunto fechado de códigos do projeto não tem `BUSINESS_RULE`, e o OpenAPI normativo já
   usa `VALIDATION_FAILED` para 422 em todo o resto. Prosa da spec corrigida, contrato inalterado.
7. **Portas e diretório do E2E são configuráveis** (`E2E_PORTA_WEB`, `E2E_PORTA_API`, `E2E_DIR_EXECUCAO`,
   com os padrões de hoje): duas execuções simultâneas do Playwright disputavam porta, banco e arquivo
   de sessão, e o teardown de uma derrubava o servidor da outra.
9. **Achados baixos aceitos, em backlog** (revisão de 17/09/2026, veredito final APROVADO):
   (a) deadlock ainda é possível no PostgreSQL quando duas execuções simultâneas da mesma casa, em meses
   diferentes, ordenam pares que se cruzam — a ordenação por par reduz muito, mas não é ordem total; sai
   como 500 em vez de 409, **sem nada gravado**. A versão provadamente livre achata todas as pernas de
   todos os pares numa única sequência de `UPDATE`s por id crescente, e exige método de repositório que
   receba N pernas; (b) quando o orçamento de comparações acaba no meio da compactação, o índice por dia
   perde a cauda daquele dia — inofensivo hoje (esgotar o orçamento aborta com 422 sem reler o índice),
   mas é armadilha se o orçamento um dia virar recuperável; (c) o 422 do orçamento pode sair na fronteira
   exata mesmo quando a busca teve sucesso gastando a última unidade. Registrado também: casa com mais de
   ~1.000 lançamentos de mesmo valor **no mesmo dia** num mês fica permanentemente em 422 nesta rota — é
   o preço do teto duro, irreal para uso doméstico, e o 422 é explícito.
10. **Nota sobre a rede de testes:** a desigualdade `pareadas + unpaired ≤ candidatas` **não** detecta
   sozinha o defeito A1 no caso geral (só no caso apertado); quem o fecha é a asserção direta de
   **disjunção** entre `items` e `unpairedItems`. Ela não pode ser removida numa limpeza futura sob o
   argumento de que a desigualdade já cobre — ela não cobre.
11. **Lacuna declarada:** os testes de repositório desta entrega rodaram **só em SQLite**; os outros três
    dialetos dependem de Docker/testcontainers, que não subiu no ambiente. O `UPDATE` condicional com
   contagem de linhas afetadas é justamente onde o MySQL diverge (conta mudança real, e por isso o
   `kind` sempre muda no `SET`) — rodar a suíte nos quatro dialetos continua pendente.

**Correção de robustez (mesma emenda):** a categoria **sugerida pela análise** que se torna inválida entre
a análise e o confirm — arquivada, excluída ou virada grupo-com-filhas — **não derruba o lote**. A sugestão
obsoleta é degradada para "sem categoria" na linha (a pessoa recategoriza depois em `/lancamentos`), e só a
categoria que o **cliente enviou** na decisão produz 422. Sem isso, arquivar uma categoria enquanto uma
revisão está aberta faz o confirm inteiro falhar com um erro que o usuário não causou. Vale também para o
`ErrCategoryArchived`, que já tinha esse modo de falha antes desta entrega.

## 14. Backlog aberto pela revisão de segurança de 17/09/2026 (achados baixos aceitos)

Registrados pelo `revisor-seguranca` no veredito da E2c; **não bloqueiam** a entrega e não têm prazo nesta spec.

1. **`link` sobrescreve a chave de deduplicação real de uma perna criada por outro lote.** Se a mesma
   transferência for importada por dois arquivos diferentes do mesmo banco (reexportação com `external_id`
   novo), a ação `link` grava a chave do segundo por cima da do primeiro, e reimportar o **primeiro** deixa
   de cair em `duplicado_exato` naquela linha. Não cruza casa, é visível e reversível na tela. Saídas:
   recusar `link` (ou degradar para `transferencia_interna`) quando a perna já tem `external_id` de outro
   lote, ou guardar os vínculos numa tabela em vez de sobrescrever a coluna.
2. **A conferência de caixa exata dos nomes de campo é cega dentro de um `json.Unmarshaler`.** Hoje é
   correto (os tri-estados e `civil.Date` embrulham escalares e listas), mas um tri-estado futuro que
   embrulhe um **objeto** perderia a conferência em silêncio. Mitigação: teste por reflexão que percorre os
   tipos de corpo do contrato e falha se algum `Unmarshaler` contiver campos com tag `json`.
3. **`POST /transfers/detect` compartilha a raiz do achado A1** (pontuação sem orçamento). A correção do
   orçamento no `textmatch` deve cobrir as duas rotas; confirmar com teste quando A1 for corrigido.
4. **Corrida tripla do `KEYWORD_TAKEN`** devolve a primeira palavra da lista em `fields.keyword`, não
   necessariamente a que colidiu — melhor esforço declarado no contrato (extrair a norma colidida exigiria
   interpretar mensagem de driver, que não é portátil entre os quatro dialetos).

## 15. Backlog da 2ª rodada de revisão (18/09/2026) — achados baixos aceitos

Da segunda rodada do `revisor-seguranca` sobre a E2c. Os altos (A3 memória) e o médio (A4 TOCTOU de
exclusão) foram corrigidos no mesmo ciclo; estes ficam registrados e **não** bloqueiam.

1. **Prazo e cancelamento viram 500, não o 422 declarado.** Só `importer/handler.go` traduz
   `context.DeadlineExceeded`; `transaction` e `investment` (E7) não mapeiam nem ele nem
   `context.Canceled`. Pior: o driver SQLite puro-Go **interrompe a escrita e devolve `interrupted (9)`,
   que não embrulha `DeadlineExceeded`** — provado de forma determinística. **Correção de 18/09/2026 a
   uma alegação desta spec:** o vazamento **não** é específico de "dentro do `tx.Do`" — a frente da E7
   mediu e ele ocorre em *toda* escrita interrompida, dentro ou fora de transação; consertar só no
   `UnitOfWork` deixaria buraco. Armadilha registrada junto: com a tabela **vazia** o SQLite nem avalia a
   condição, o `UPDATE` volta em ~1 ms sem erro, e um teste escrito assim passa sem nunca exercitar a
   interrupção — o teste precisa inserir linhas antes.

   Sem vazamento de dado (corpo fixo, `reason` sem descrição), mas o modo de falha declarado não se
   cumpre e qualquer cliente que feche a aba gera ruído no log de `ERROR`. **A raiz foi feita pela frente
   da E7** em `backend/internal/platform/storage/ctxerr.go` (callback registrado uma vez no `Open`, nos
   processadores Create/Query/Update/Delete/Row; `Raw` ficou de fora de propósito, porque a aplicação não
   passa por ele e registrar ali colidiria com o `TestSemSQLMontadoNoGormstore`). **Dívida remanescente
   desta entrega:** o ramo nos handlers de `transaction` e `importer` — `DeadlineExceeded` → o 4xx que a
   rota declara, `Canceled` → log em INFO, não ERROR.
2. **A folga de `MaxMatchWork` vale contra corpus com colisão comparável à do gerador.** Medido: 4.000
   palavras com 3 tokens comuns × 10.000 descrições = 1,98× o teto (422 legítimo); o penhasco fica em
   ~2.000 palavras-chave com vocabulário repetitivo. Não é falha (falha fechada, 422 acionável, nada
   gravado), é honestidade sobre o número. Calibrar com `Budget.Spent()` por operação em log/métrica
   antes que alguém receba o 422 legítimo.
3. **`WithWorkBudget`/`WithAnalyzeTimeout` podem afrouxar, não só apertar.** O piso de 1 impede orçamento
   zero, não orçamento infinito. Nenhum caminho de produção as usa hoje. Hardening: `min(valor, teto)`,
   para a opção só poder apertar e o invariante não depender da disciplina de quem chama.
4. **Custo adversarial residual de CPU:** ~2,5 s por requisição × 60/h × 3 rotas ≈ 6 CPU-min/hora por
   casa, com `Burst` 3. É o preço declarado de um orçamento que precisa caber no teto legítimo; só um
   teto global de requisições em voo nas rotas pesadas o reduziria mais.
5. **Categoria arquivada entre o plano e o `UPDATE`** (recorte benigno do A4): aceito com registro —
   arquivar preserva histórico por desenho, e "lançamento com categoria arquivada" é estado legítimo.
6. **`ImportUpload`/`ImportConfirm` sem `Burst`** ganharam razão técnica nova: é o maior multiplicador do
   ataque de memória do A3 (10 análises simultâneas de 10.000 linhas por casa).

## 16. Dependência entre entregas — o teto de memória do `textmatch` protege três rotas

Registrado em 18/09/2026, a pedido das duas frentes que compartilham o pacote.

`textmatch.maxMemoMatches` (teto por total de `Match` memorizados, conferido **antes** da gravação da memo)
é o que fecha o caminho de OOM do achado A3. Três rotas alcançam esse código:
`POST /transactions/auto-categorize`, `POST /transfers/detect` (E2c) e **`POST /investments/detect` (E7,
outra entrega)**.

**Condição explícita:** se esse teto for removido, afrouxado ou reordenado para depois da gravação, a E7
volta a ter caminho de OOM **sem que uma linha de `internal/investment` seja tocada** — e as duas entregas
precisam de nova revisão de segurança. Quem mexer no teto avisa a frente de Investimentos. O comentário do
próprio teto, em `internal/textmatch/matcher.go`, cita essa dependência pelo nome, para que ela seja
descoberta a partir do código e não dependa de alguém lembrar.

**Correção relacionada (18/09/2026):** a raiz do tratamento de `context.DeadlineExceeded`
(`internal/platform/storage/ctxerr.go`, feita pela frente da E7) revelou uma fragilidade no handler da
importação desta entrega: `limiteDoArquivo` classificava a falha por uma propriedade do **ambiente** (o
contexto está morto) em vez de por um fato do **domínio** (o prazo da análise estourou). Com o embrulho
novo, qualquer erro de driver com contexto morto virava 422 culpando o arquivo do usuário e **suprimia a
linha de `ERROR` no log**. Corrigido com sentinela própria (`ErrAnalyzeTimeout`) emitida por quem impõe o
prazo. Regra que fica: **não deduza a causa de uma falha a partir do `ctx`** — quem conhece o prazo emite
o erro que o nomeia.

## 17. Princípio: escolha da pessoa não degrada em silêncio; sugestão do servidor degrada

Registrado em 18/09/2026 a pedido do `revisor-seguranca`, que o extraiu de uma divergência **deliberada e
aprovada** entre duas rotas desta entrega. Vale para toda rota nova que atribua categoria (ou qualquer
referência) a um registro.

> **O que a PESSOA escolheu nunca degrada em silêncio: vira 422 (entrada inválida) ou 409 (o estado mudou
> durante a operação). O que o SERVIDOR sugeriu degrada — e o contrato publica a degradação.**

Por isso `POST /imports/{id}/confirm` **degrada para "sem categoria"** a sugestão que envelheceu entre a
análise e o commit (§13), enquanto `POST /transactions/auto-categorize` **aborta com 409** quando a
categoria do plano muda no meio (emenda §13/A4) e o `categoryId` que o **cliente** manda continua virando
**422** em qualquer das duas. Não é inconsistência: na primeira o id é palpite do servidor, e derrubar um
confirm de 400 linhas por um palpite envelhecido seria desproporcional; na segunda a pessoa apontou a
categoria, e gravar um subconjunto silencioso é pior que falhar e deixá-la decidir de novo.

O princípio está escrito aqui porque, sem ele, a terceira rota que precisar decidir isso vai redescobri-lo
— ou "uniformizar" as duas para o lado errado.

**Corolário verificado na 3ª rodada (A9): reconferência parcial é meia correção.** A reconferência que
sustenta o 409 precisa reler **tudo o que qualificava o destino quando o plano o escolheu**, não só se ele
ainda existe. Conferir "viva e sem filha ativa" e esquecer a **natureza** deixava passar `expense → income`
trocado na janela, gravando despesa em categoria de receita — estado que todas as outras portas do produto
recusam.

**Como não esquecer um qualificador** (formulação da frente da E7, 18/09/2026, que a encontrou aplicando
este corolário e achou um quinto que faltava na lista dela — o destino também estava **ativo** quando foi
escolhido, e `ErrCategoryArchived` recusa atribuição nova a categoria arquivada nas outras três portas):
a pergunta certa **não** é "reli tudo o que está na minha lista?", é **"o que qualificava este destino no
momento em que eu o escolhi?"**. A primeira depende de alguém ter mantido uma lista; a segunda se responde
olhando o domínio, e é a única que encontra o qualificador que ninguém anotou. Distinção que sobreviveu à
verificação nas duas entregas: marcação **existente** sobrevive ao arquivamento; atribuição **nova**, não.

## 18. Dívida aceita — consolidada no veredito final da E2c (18/09/2026)

Cinco rodadas de revisão de segurança: BLOQUEADO (A1 CPU sem teto, A2 `Burst`/transação) → BLOQUEADO
(A3 memória sem teto, A4 TOCTOU) → BLOQUEADO (A9 natureza não relida) → **APROVADO** com A12 e baixos →
**APROVADO** final. O que fica como dívida, com o porquê:

1. **GO-2026-5932** (`x/crypto/openpgp`, não alcançável) — §8.1 do `docs/SEGURANCA.md`, sem correção
   upstream.
2. **`WithAnalyzeTimeout` sem clamp** — dívida *declarada*, sustentada por portão automatizado
   (`importer/interruptor_do_prazo_test.go`) validado com violação real plantada.
3. **`maxMemoMatches` é a única barreira de bytes.** Não há `GOMEMLIMIT` nem teto de requisições pesadas
   em voo; o pior empilhamento por casa é da ordem de **~0,9 GB** (19 operações × ~46 MiB). Backlog: teto
   global de operações pesadas em voo.
4. **Prazo que vence *dentro* de uma consulta é 500 com `ERROR`, não 422** — decisão declarada em
   `transaction/validation.go`. O orçamento existe para limitar trabalho sobre as **linhas**; consulta
   única que sozinha estoura 15 s é infraestrutura. Revisitar só com dado de produção — e **nunca**
   voltando a perguntar ao erro.
5. **Prazo vencido + cliente ido responde 422 e não deixa linha de log** — coerente com os demais tetos
   da família, que também não logam.
6. **A invariante "só existe o `PlanTimeout` no contexto desta fase" é guardada por comentário e revisão,
   não por código.** O dia em que ela cair quebra `conferirPrazo` **e** `erroInterno` ao mesmo tempo.
   Vale uma linha em `docs/ARQUITETURA.md` amarrando as duas.
7. **Granularidade das paradas** (64 linhas / 500 pareamentos): o estouro é observado com atraso limitado
   a uma fatia de trabalho.
8. **Corrida tripla do `KEYWORD_TAKEN`** devolve a primeira palavra da lista em `fields.keyword` — melhor
   esforço declarado no contrato.
9. **`link` sobrescreve a chave de deduplicação de uma perna criada por outro lote** (§14.1) e
   **`ImportUpload`/`ImportConfirm` sem `Burst`** — este último com razão técnica extra: é o maior
   multiplicador do ataque de memória do A3.
10. **Pendências da entrega vizinha (E7), apontadas e não corrigidas aqui:** `investment/handler.go:226`
    decide a desistência **pelo erro** (espelho do defeito corrigido nesta entrega, e sem `reason` na
    linha de INFO — o registro do defeito desaparece por completo); e a cópia inline do predicado em
    `investment/detect.go:296`, cuja forma fundida deixa o eixo "excluída" sustentado por acidente do
    eixo da natureza. Fecham quando aquela frente migrar para `category.DestinoAindaQualifica`.

**Observação de processo, registrada pelo revisor:** `internal/transaction/**`, `internal/investment/**`,
`internal/report/**`, `platform/storage/ctxerr.go` e `gormstore/category_repository.go` estão
**untracked** no git. O "congelamento por hash" da entrega vizinha **não é verificável por git** neste
estado — quem depender dele precisa guardar a lista de hashes fora do git, ou comitar os caminhos.

## 19. Emenda de 18/09/2026 — recategorizar uma linha já categorizada em `/lancamentos` (pedido do usuário)

**Pedido literal:** "No menu de Lançamentos tem que ter como a qualquer momento eu manualmente alterar um
lançamento de categoria."

**Decisão.** Antecipa-se da E2b **apenas a troca de categoria** — nada do `PATCH` completo (valor, data,
descrição, conta continuam lá). O caminho já existe: o `PATCH /transactions/{id}` com `{ categoryId }` da
§11 tem contrato de **substituição**, não de preenchimento, e o servidor já responde 200 trocando a
categoria de uma linha que tinha outra. Logo: **nenhuma mudança de contrato, de schema, de filtro, de
`summary` ou de rate limit** — a emenda é de interface. Isto **revoga** a frase "recategorizar em massa o
que já tem categoria" da §11 *Fora* na parte individual: massa continua fora (§2.2), linha a linha entra
aqui.

**Comportamento** (desenho normativo em `docs/DESIGN.md`, E2c (h) §1-bis, §2, §3 e §4 "Depois de trocar"):

1. A célula de uma linha `income`/`expense` **já categorizada** deixa de ser um `<span>` e passa a ser o
   **mesmo controle** da lacuna, no outro estado fechado: o nome da categoria com o chevron, caixa
   invisível (`border: 1px solid transparent`), tinta herdada do lugar. Um componente, dois estados
   fechados, um aberto — *fechado difere, aberto é o mesmo*. O tracejado continua **exclusivo da
   pendência**. Perna de transferência continua sem controle nenhum.
2. Abre o **mesmo editor**, em modo *trocar*: legenda `Trocar categoria de {rótulo}`, `<select>` já na
   categoria atual quando ela ainda é escolhível. Não sendo (arquivada, ou grupo que ganhou
   subcategorias), abre no placeholder com uma nota que **diz qual dos dois motivos** é.
3. **Confirmar com a categoria atual não emite requisição**: o botão é `aria-disabled` com o rótulo
   `Escolha outra categoria` e o clique leva ao campo. Não existe "trocar para a mesma" — o servidor
   responderia 200 sem escrever, e um toast de troca que não trocou nada seria mentira. As fichas de
   aprender também só existem com escolha **diferente** da atual.
4. **Só este:** um `PATCH /transactions/{id}` `{ categoryId }` → toast `Categoria trocada de Transporte
   para Lazer.` (anterior não resolvível: `Categoria trocada para Lazer.`) e **o foco volta ao controle da
   própria linha**, agora com o nome novo. Não existe "próxima lacuna" no modo trocar: quem troca uma
   categoria está trabalhando nesta linha, não varrendo pendências.
5. **Com palavra-chave:** a mesma sequência (a) → (b) → (c) da §11.3, com duas notas: (c) continua
   tocando **só** os lançamentos sem categoria do mês (§4.3) — trocar uma linha nunca recategoriza outra
   que já tinha dona —, e a palavra que já é da categoria **anterior** leva 409, caso em que **nada mais é
   feito**, como na §11. No toast, a troca desta linha vem **primeiro**: `Categoria trocada de Transporte
   para Lazer · «uber» adicionada a Lazer · mais 3 lançamentos de setembro categorizados.`
6. **Sob `?tipo=`, a linha pode sair da lista** quando a categoria nova contradiz o filtro — inclusive o
   caso novo `investimentos` → categoria de despesa ou de receita. A saída é **anunciada no toast**
   (segunda frase de E2d (g), mesmo molde), e o foco vai ao controle da linha **vizinha na foto de
   antes** → `Carregar mais` → `<h1>`. Com `?semCategoria=1` não há linha categorizada na tela, então a
   recategorização não é oferecida ali.

**Fora desta emenda:** remover a categoria de uma linha (voltar a "Sem categoria"); acrescentar palavra a
uma categoria **sem** trocar a linha (isso é Categorias e a revisão da importação); recategorização em
massa (§2.2); e todos os demais campos do lançamento (E2b).

**Segurança.** Nenhum vetor novo: o vetor do ADR-029(i) — mover dinheiro entre naturezas para "sumir" com
uma despesa — apenas ganha um caminho de UI a mais, com a **mesma mitigação já em vigor**: o lançamento
continua visível em Tudo e em Investimentos, a faixa "Fora destes números" continua declarando o que o
recorte não mostra, e o servidor continua auditando `transaction.updated`. O `categoryId` enviado sai
sempre de uma opção do `<select>` alimentado pela árvore da casa; o nome da categoria entra na tela como
**texto React** (filho, atributo ou toast), nunca como HTML montado. As respostas do servidor não mudam:
404 para linha ou categoria de outra casa, 422 para transferência, natureza incompatível, arquivada ou
grupo com subcategorias.

**Critérios de aceite:**

(a) A linha categorizada tem um `<button>` com o nome da categoria, `data-atalho` e **sem** `data-lacuna`;
a lacuna tem os dois; a perna de transferência não tem controle de categoria nenhum.
(b) Em modo trocar o `<select>` abre na categoria atual, e o confirmar diz `Escolha outra categoria` com
`aria-disabled` — clicá-lo foca o campo e **não emite nenhuma requisição**.
(c) Trocar emite **um** `PATCH` com `{ categoryId }`, mostra `Categoria trocada de X para Y.` e devolve o
foco ao controle da mesma linha, já com o nome novo; nenhuma outra linha é tocada.
(d) Na saída com palavra, o 409 da palavra que já é da categoria anterior deixa tudo como estava (nada de
(b), nada de (c)).
(e) Categorizar uma **lacuna** continua levando o foco à próxima lacuna, **pulando** as linhas
categorizadas vizinhas.
(f) Sob `tipo=despesas`, `tipo=receitas` e `tipo=investimentos`, a linha que sai da lista é anunciada no
toast com a frase da natureza e o foco vai ao vizinho da foto.
(g) A gravação escreve em cache **só na lista de lançamento** — o critério é a IDENTIDADE da query, não a
presença de `pages`. `['transactions']` é prefixo de quatro formas, e as outras três ficam intactas:
`['transactions','dashboard',mês]` (painel), `['transactions','reports','by-category',…]` (relatório) e
`['transactions','investments',mês]` (a visão de `/investimentos`, que **também** é `InfiniteData` com
`pages[].items[]` e por isso passaria por qualquer guarda de forma). Corrigido em 18/09/2026, achado C1 da
revisão de segurança: com a §19 a linha marcada como aporte virou editável e **está** naquele cache, então
trocá-la por um `Transaction` apagava o `flow` — coluna "Movimento" em branco, linha de despesa listada
numa tela de aportes e `monthly.contributionsCents` ainda contando o valor, e o lixo ficava até o `gcTime`
(a invalidação é `refetchType: 'active'` e a tela está desmontada). Prova: com o painel **e** com
`/investimentos` em cache, a gravação atualiza a lista, não encosta nos dois — sem erro, com o toast de
sucesso.
(h) Categoria atual arquivada: campo no placeholder e nota `A categoria {nome} está arquivada e não pode
ser escolhida de novo.`; categoria atual que é grupo com subcategoria ativa: `O grupo {nome} tem
subcategorias e não recebe lançamento. Escolha uma delas.` — as duas frases dizem o substantivo, e a nota
está no `aria-describedby` do `<select>` (docs/DESIGN.md (h) §2, ratificado em 18/09/2026).
(i) Playwright: depois do fluxo "só este" da §11, recategorizar `MERCADO X` de Alimentação para outra
categoria, com a tela a 375 px.

---

## 20. Emenda de 18/09/2026 — semente de casa nova com palavras-chave de fábrica (ADR-033)

Casa nova passa a nascer com **15 grupos, 41 subcategorias e 440 palavras-chave** já cadastradas. A
lista e a estrutura estão na **spec 0003 §5**; a fonte da verdade é `DefaultGroups()` em
`backend/internal/category/seed.go`. Esta seção registra o que a semente muda **para esta spec**.

### 20.1 O que muda no comportamento já contratado aqui

Nada de contrato — schema, rotas e códigos de erro ficam iguais. O que muda é **quem já ocupa o
espaço** numa casa nova, e isso torna visíveis três regras que antes quase nunca disparavam:

| Situação em casa nova | Antes | Depois |
|---|---|---|
| `PATCH /categories/{grupo}` com `keywords` não vazio | 200 | **400 VALIDATION em `fields.keywords`** nos 14 grupos com filhas (§12) |
| `POST`/`PATCH /categories` com palavra da semente | 201/200 | **409 KEYWORD_TAKEN**, com `ownerId` = a folha da semente que já a tem |
| `PATCH /transactions/{id}` com `categoryId` de grupo com filhas | — | **422** (§13) |
| análise da importação e `POST /transactions/auto-categorize` | sem sugestão até a pessoa cadastrar | **sugestão de fábrica** |

A consequência de teste é declarada: a suíte E2E, que roda sobre uma casa compartilhada,
**quebra de propósito** nos pontos em que assumia grupos sem filhas e palavras livres.

### 20.2 As oito regras da lista (R1–R8) são NORMATIVAS

Estão escritas por extenso no ADR-033 (decisão d) e no doc-comment de `DefaultGroups()`. Em resumo:
sem palavra que seja subconjunto consecutivo de outra no mesmo lado do dinheiro (R1); produto de
investimento num lado só, com frase verbo+produto no outro (R2); nada que alcance o limiar contra o
vocabulário de rotina dos extratos, marcas de banco e nomes de pessoa (R3); nenhum meio de pagamento
nem marca de banco (R4); palavra com menos de 5 runas só quando o token do extrato é exatamente ela
(R5); genérico ambíguo fora (R6); teto de **16** palavras por folha semeada, contra o teto de 20 do
domínio, para o atalho "Reconhecer por «x»" da §11 nunca nascer recusado (R7); e forma redundante não
ocupa vaga (R8).

### 20.3 O teste de tokens perigosos é OBRIGATÓRIO em qualquer mudança da semente

`TestNenhumaPalavraDaSementeCasaComOVocabularioDeRotina`, em
`backend/internal/category/seed_test.go`, roda **581 tokens** de rotina — boilerplate dos parsers
`nubank`/`inter`/`c6`, marcas de banco e adquirente, nomes e sobrenomes comuns, lugares e
modificadores de razão social — contra os matchers dos **dois** lados do dinheiro, e falha se
qualquer palavra da semente alcançar `textmatch.MinScore`.

Ele existe porque a ameaça desta feature não é errar uma linha: é **categorizar todas as
transferências da casa em silêncio** e o `auto-categorize` com `dryRun: false` gravar isso. A lista
de tokens **não encolhe** para fazer uma palavra caber — quem reprova é a palavra, e o motivo fica
escrito no teste. Exemplos medidos que já custaram uma palavra: `contador` 89 contra `conta`;
`seguro` 90 contra `pagseguro`; `ração` 89 contra `operação`; `benefício` 86 contra `beneficiário`;
`magazineluiza` 82 contra o primeiro nome `luiza`.

**Empate e falso positivo se resolvem NA LISTA, nunca no motor** (ADR-026): `internal/textmatch` não
muda por causa da semente. Os casos aceitos ficam fixados em `seed_corpus_test.go`, com a pontuação
medida — ver o ADR-033 (decisão f).

### 20.4 A lista é calibrada pelo texto do PARSER, e o eixo que importa é a razão social

Emenda da revisão de segurança de 18/09/2026, normativa para qualquer mudança futura na semente.

A pergunta ao escolher um token perigoso **não** é "o que o banco escreve", e sim **"o que chega ao
classificador"** — que é a saída de `importer.Describe` = `sanitize.Description` + `textnorm`. A
diferença muda a resposta:

- `sanitize.Description` **corta** a descrição no primeiro segmento que contém documento, então numa
  linha de Pix do Nubank o nome da instituição **nunca chega ao matcher**: aquela linha enorme do
  Itaú vira `pix enviado - energia exemplo s.a.`. Marca de banco, naquele formato, é um alvo que não
  existe;
- a **razão social**, ao contrário, sobrevive inteira — no Inter ela vem no par `Histórico;Descrição`
  (`Pagamento efetuado;ADMINISTRADORA DE IMOVEIS LTDA`) e no C6 no `Título`, sem documento na linha e
  portanto sem onde cortar.

Foi por isso que a primeira versão da lista, com 532 tokens, ainda deixou passar sete falsos
positivos — **todos** em razão social. O pior era mensal: «móveis» tirava **96** contra o token
`imoveis`, e o aluguel pago à administradora entrava em "Compras › Casa e decoração" todo mês, com a
sugestão aplicada por padrão no `confirm` da importação (spec 0005 §7). A lista cresceu para **580**
nesse eixo e seis palavras da semente cederam — a medição de cada uma está no corpus e no ADR-033(g).

### 20.5 Os testes da semente usam IGUALDADE EXATA, não piso

`len(tokensDeRotina) == 581` e `len(corpusDaSemente()) == 296` são **igualdades**, não
`GreaterOrEqual`. A razão é o movimento que um piso com folga permite: acrescentar uma palavra
perigosa à semente e **podar em silêncio** os três ou quatro tokens que ela quebraria, com o build
verde. Com igualdade, **trocar** um token por outro continua sendo possível e vira um diff de duas
linhas que o revisor vê; **remover** quebra o build.

Pelo mesmo motivo, o bloco "O QUE FICOU DELIBERADAMENTE DE FORA" de `seed_test.go` é **inventário
completo**, não amostra: token perigoso que você decidir não incluir entra ali com o número e o
motivo. A maior exceção — `mercado`, que não entra porque «supermercado» sozinho já tira 88 contra
ele, e tirar «supermercado» custaria a categoria mais usada do app — estava ausente por omissão até a
revisão apontar.

### 20.6 R9 (modificador de marca) — CHECKLIST de revisão, não invariante de build

Proposta na reconferência de 18/09/2026 e **medida antes de ser adotada**: palavra-chave escrita
**colada** cujo prefixo ou sufixo de ≥ 5 runas seja palavra de uso comum **vaza esse pedaço**, porque
a regra 2 do motor alcança a palavra inteira a partir da fatia. Foi o que produziu o achado N1 —
`prime` ⊂ «amazonprime» (84), `smart` ⊂ «smartfit» (89), `ultra` ⊂ «ultragaz» (89), `colar` ⊂
«decolar» (91) — e é a mesma mecânica de `mercado` ⊂ «minimercado».

**Ela NÃO virou trava**, e a razão está medida em `seed_r9_diagnostico_test.go`:

1. a regra crua acusa **1.357 fatias**; **1.351** são truncamento que não é palavra ("upermercado",
   "abeleireiro", "adiantament"). O discriminador de verdade é *"a fatia é palavra comum?"*, e essa
   pergunta não se responde sem um dicionário de português no repositório — dependência nova, decisão
   de arquitetura, não de teste;
2. o filtro automático que parecia óbvio — *"a fatia vence para outra dona"* — é o **sinal errado**, e
   é o achado mais útil do diagnóstico: ele reduz a lista a 9 casos mas **perderia os quatro achados
   do N1**, porque `colar` vence para Viagens (dona de «decolar»), `ultra` vence para Água/luz/gás
   (dona de «ultragaz») e `mercado` vence para Supermercado (dona de «minimercado»). O estrago do N1
   não é a fatia sugerir a folha errada; é **uma descrição alheia que contém a fatia** cair ali.

O que sobra de automação honesta é marcar a fatia que **já é palavra conhecida do projeto** (token da
lista de rotina ou palavra-chave de outra folha). Com esse filtro sobram **6**, todas inofensivas hoje
porque a dona legítima vence com folga — e as duas primeiras são justamente as exceções já
documentadas:

| fatia | dentro de | contra a palavra | hoje vence | folga |
|---|---|---|---|---|
| `mercado` | «supermercado» | 88 | Supermercado, 89 | exceção documentada |
| `smart` | «smartphone» | 85 | Compras › Eletrônicos, 85 | exceção documentada |
| `posto` | «imposto» | 91 | Combustível, 100 | 9 pontos |
| `estacio` | «estacionamento» | 85 | Educação (via «estácio»), 100 | 15 pontos |
| `bilhete` | «bilheteria» | 91 | Cinema, 91 | `BILHETE UNICO` resolve certo pela frase (100) |
| `cross` | «crossfit» | 89 | Academia, 89 | `GOLDEN CROSS` resolve certo pela frase (100) |

**Nenhuma delas tem conserto grátis** — tratar qualquer uma exige remover uma palavra útil sem
substituta. Por isso R9 vale como **checklist de revisão** ao acrescentar marca colada à semente, e
**não** como invariante: ela não reprova palavra nenhuma.

**O que É travado, em uma linha:** `assert.Equal(t, 6, len(achados))`. Um checklist impresso tem um
defeito fatal — ninguém é obrigado a olhar. Com a contagem travada, quem acrescentar amanhã uma marca
colada cuja fatia seja palavra do projeto vê o build quebrar e é levado até a tabela acima. É a mesma
forma do achado A3 (igualdade exata em vez de piso) aplicada a um inventário, e custa uma linha.

⚠️ **A fronteira do instrumento, para ninguém ler "6" como inventário completo.** O discriminador
`palavrasConhecidasDoProjeto` lê `tokensDeRotina` mais os tokens da semente — e um token que é
**exceção aceita nunca pode estar em `tokensDeRotina`**, porque quebraria o teste principal, que é
exatamente o motivo de ele ser exceção. Logo **`ultra` e `colar` jamais aparecerão no diagnóstico**:
dois dos achados que motivaram a regra são invisíveis para o instrumento que a mede. E quando um
aparece é por acidente: `smart` só é "conhecido" porque «smart fit» virou frase e escreveu o token na
semente; `mercado`, porque «mercado livre» o contém — não por estar na lista de rotina, de onde ele
está explicitamente excluído. **O inventário completo é o bloco "O QUE FICOU DELIBERADAMENTE DE FORA"
de `seed_test.go`** (que o achado A2 tornou completo); o diagnóstico é só a fatia mecanizável dele.
