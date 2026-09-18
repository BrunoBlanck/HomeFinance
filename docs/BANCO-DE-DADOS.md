# Banco de dados — HomeFinance

Requisito: o app funciona com **qualquer banco SQL**. Suportados oficialmente: **PostgreSQL (padrão de produção), MySQL/MariaDB, SQLite (dev/testes), SQL Server**.

## Estratégia de portabilidade

> **Atualizado em 09/09/2026 pelo ADR-008.** A decisão original (sem ORM, `database/sql` + sqlx, migrações goose) foi **superada por decisão do usuário**: a camada de persistência do projeto é **GORM**. Nada de sqlx e nada de goose entram no código.

1. **GORM v2** (`gorm.io/gorm`) é a única camada de acesso a dados, com os drivers oficiais dos quatro dialetos:

   | Dialeto | Driver | Observação |
   |---|---|---|
   | PostgreSQL (produção) | `gorm.io/driver/postgres` | baseado em pgx |
   | MySQL/MariaDB | `gorm.io/driver/mysql` | |
   | **SQLite (desenvolvimento e testes)** | **`github.com/glebarez/sqlite`** | baseado em `modernc.org/sqlite`, **puro Go, sem CGo** |
   | SQL Server | `gorm.io/driver/sqlserver` | |

   O driver do GORM oficial para SQLite (`gorm.io/driver/sqlite`) exige CGo; usamos o `glebarez` para manter o build puro Go e cross-compilável.

2. **Repositórios por interface.** Contratos declarados em cada pacote de domínio (ex.: `internal/transaction/repository.go`); implementação única em `internal/platform/storage/gormstore/`. A escolha do dialeto acontece uma vez, na composição (`cmd/api/main.go`), a partir de `DB_DRIVER` na config. **`*gorm.DB` nunca sai do pacote de storage** — service e handler não sabem que GORM existe.

3. **Subconjunto portátil.** Tipos e tags das structs devem funcionar nos quatro dialetos; o que não der, vive atrás de um `switch` de dialeto isolado no `gormstore`. Nada de SQL cru com interpolação de entrada — GORM parametriza por padrão, e `Raw`/`Exec` só com placeholders `?`.

4. **Evolução de schema: `AutoMigrate` no boot.** As **structs Go com tags `gorm:"..."` são a fonte de verdade do schema**. `db.AutoMigrate(&User{}, ...)` roda na subida da API e cria tabelas, colunas, índices e constraints que faltarem.

   ⚠️ **Limite conhecido e aceito:** `AutoMigrate` **nunca remove nem renomeia** coluna, e não faz alteração destrutiva de tipo. Mudança destrutiva exige passo manual explícito, registrado em ADR — não presuma que o `AutoMigrate` resolveu.

## Convenções de schema (portáteis)

Expressas como tags GORM nas structs de persistência (`gormstore`), não em SQL à mão.

| Conceito | Convenção | Motivo |
|---|---|---|
| IDs | `TEXT`/`VARCHAR(36)` com UUID v7 gerado **na aplicação** | portátil, ordenável por tempo, sem depender de autoincrement/extensões |
| Dinheiro | `BIGINT` em **centavos** | exato em todos os dialetos; float proibido |
| Data/hora | `TIMESTAMP` em **UTC** (conversão na aplicação) | evita armadilhas de timezone por dialeto |
| Booleano | `SMALLINT` 0/1 | MSSQL não tem `BOOLEAN` |
| Texto | `VARCHAR(n)` com limite explícito; `TEXT` só para conteúdo livre | validação + índices previsíveis |
| Nomes | `snake_case`, tabelas no plural | convenção única |
| Soft delete | `deleted_at TIMESTAMP NULL` em dados financeiros | histórico importa em finanças |

**Proibido no caminho comum:** triggers, stored procedures, views materializadas, JSON nativo, `RETURNING`, extensões específicas. Regra de negócio vive em Go, não no banco. **Também proibido:** hooks de callback do GORM (`BeforeSave`, `AfterFind`…) para regra de negócio — regra vive no service, onde é testável e visível.

## Schema inicial (v1)

```
households    (id, name, created_at)
users         (id, email UNIQUE, password_hash, name, created_at)
memberships   (id, household_id FK, user_id FK, role, created_at)          [UNIQUE(household_id, user_id)]
accounts      (id, household_id FK, name, type, created_at, deleted_at)
categories    (id, household_id FK, parent_id FK NULL, name, kind, created_at, deleted_at)
transactions  (id, household_id FK, account_id FK, category_id FK, kind,
               amount_cents BIGINT, description, occurred_on, created_by FK,
               created_at, deleted_at)
recurring_bills(id, household_id FK, category_id FK, name, amount_cents,
               due_day, active SMALLINT, created_at, deleted_at)
budgets       (id, household_id FK, category_id FK, year_month, limit_cents,
               created_at)                                                  [UNIQUE(household_id, category_id, year_month)]
refresh_families(id, user_id FK, revoked_at, created_at)   ← estado da SESSÃO
refresh_tokens(id, user_id FK, token_hash, family_id, expires_at, revoked_at, created_at)
verification_codes(id, user_id FK, purpose, code_hash, expires_at, consumed_at,
               attempts, created_at)     ← OTP de 6 dígitos (ADR-009)
audit_log     (id, household_id, user_id, action, entity, entity_id, ip, created_at)
```

- `users` carrega `email_verified_at TIMESTAMP NULL` — conta só é utilizável depois de verificada (ADR-009).
- `verification_codes.code_hash` guarda **só o hash HMAC** do código de 6 dígitos, nunca o código; `purpose` distingue `email_verification` de `password_reset`.
- `refresh_families` é o estado de revogação de uma **sessão inteira**, e a revogação sempre passa por ela **antes** dos tokens. Motivo: `UPDATE refresh_tokens ... WHERE family_id = ?` só alcança linhas já gravadas, então o sucessor de uma rotação em curso escapava da revogação disparada por reúso — o token roubado sobrevivia à detecção do próprio roubo. O `Refresh` consulta a família **antes de emitir** e reconfere ao gravar o sucessor; token de família revogada não vale, mesmo com `revoked_at` nulo.

- **Toda tabela de dados de usuário tem `household_id`** — base do isolamento de segurança; índice composto começando por `household_id` nos filtros frequentes (ex.: `(household_id, occurred_on)` em `transactions`).
- Índice em toda FK. `token_hash` guarda o **hash** do refresh token, nunca o token.

## Testes

- Repositórios testados contra **SQLite em memória** em todo `go test` (rápido, sem infra) — mesmo banco do ambiente de desenvolvimento.
- **testcontainers-go** roda a MESMA suíte de repositório contra Postgres, MySQL e MSSQL reais em containers (CI/pré-release) — única forma de garantir de verdade o requisito multi-banco.
- Mudança de schema só entra com `AutoMigrate` aplicado com sucesso ao menos em SQLite + Postgres, partindo de banco vazio **e** de banco já povoado.
- `go test -race ./...` sempre.

## Schema v2 (spec 0003 — contas e categorias)

```
accounts   (id, household_id, name, name_norm, kind, opening_balance_cents,
            opening_date, archived_at, created_at, updated_at, deleted_at)
categories (id, household_id, parent_id NULL, name, name_norm, kind,
            archived_at, created_at, updated_at, deleted_at)
households + timezone, currency
```

Unicidade de nome (conta e categoria) **não** é índice único: exigiria índice
parcial (P4) e, no caso da categoria, índice único sobre coluna anulável (P3).
A regra é verificada no service dentro da transação — ver D2 da spec 0003.

## Schema v3 (lançamentos, fatura de cartão e importação)

### `transactions`

| Coluna | Tipo | Regra |
|---|---|---|
| `id` | `varchar(36)` | UUID v7 gerado no service |
| `household_id` | `varchar(36)` not null | sempre do token; primeira coluna de todo índice composto |
| `kind` | `varchar(12)` not null | `income` · `expense` · `transfer_out` · `transfer_in` |
| `account_id` | `varchar(36)` not null | |
| `category_id` | `varchar(36)` NULL | lançamento importado nasce sem categoria |
| `amount_cents` | `bigint` not null | **sempre positivo**; o sinal vem do `kind` |
| `description` / `description_norm` | `varchar(140)` not null | `_norm` é minúsculo sem acento (P2) |
| `occurred_on` | `varchar(10)` not null | data civil `YYYY-MM-DD` (D3 da spec 0003) |
| `year_month` | `varchar(7)` not null | mês de **caixa**, projeção de `occurred_on` gravada pelo mapper (P1) |
| `competence_month` | `varchar(7)` not null | mês de **competência**; sempre preenchido, nunca nulo |
| `transfer_group_id` | `varchar(36)` NULL | amarra as duas pernas da transferência (ADR-016) |
| `statement_id` | `varchar(36)` NULL | fatura de cartão que contém a compra |
| `source` | `varchar(12)` not null | `manual` · `import` |
| `import_batch_id` | `varchar(36)` NULL | lote que trouxe a linha |
| `external_id` | `varchar(64)` NULL | id que o documento trouxe, quando trouxe |
| `dedup_key` | `varchar(64)` not null | hash calculado no service — **sempre preenchido** |
| `dedup_ordinal` | `int` not null default 1 | desempata repetições legítimas |
| `created_by` | `varchar(36)` not null | |
| `created_at` / `updated_at` | timestamp not null | UTC |
| `deleted_at` | timestamp NULL | soft delete (histórico financeiro importa) |

Índices (todo composto começa por `household_id`; **todos ascendentes**):

```
ux_transactions_dedup     UNIQUE (household_id, dedup_key, dedup_ordinal)
ix_transactions_occurred         (household_id, occurred_on, id)
ix_transactions_account_occurred (household_id, account_id, occurred_on)
ix_transactions_competence       (household_id, competence_month)
ix_transactions_statement        (household_id, statement_id)
ix_transactions_group            (household_id, transfer_group_id)
ix_transactions_batch            (import_batch_id)
ix_transactions_deleted_at       (deleted_at)
ix_transactions_category         (household_id, category_id)
```

**Por que a chave de deduplicação é um hash obrigatório, e não `external_id`.**
Índice único sobre coluna anulável é a armadilha **P3**: o SQL Server trata
NULLs como **iguais**, então o primeiro lançamento sem `external_id` passaria e
todos os demais seriam recusados — uma linha por casa. Um índice único
**parcial** (só onde `external_id` não é nulo) resolveria em PostgreSQL e
SQLite, mas o MySQL não tem índice parcial (**P4**) e o `AutoMigrate` não
expressa índice parcial de forma portátil. Por isso a chave é `dedup_key`,
calculado no service e sempre preenchido, com `dedup_ordinal` desempatando
repetições legítimas (duas compras idênticas no mesmo dia acontecem).

**Largura do índice único.** `36 + 64 + 4` caracteres. No MySQL/InnoDB com
`utf8mb4` (4 bytes por caractere) são `144 + 256 + 4 = 404 bytes`, bem abaixo
do teto de **3072 bytes** por chave de índice (formato `DYNAMIC`, padrão desde
o MySQL 5.7.7/8.0) — **sem prefixo de índice**. No SQL Server são 104 bytes em
`varchar` (204 se o driver usar `nvarchar`), contra 1700 bytes de teto em
índice não clusterizado. PostgreSQL e SQLite não têm limite relevante aqui.

**Ordenação descendente não entra na definição do índice.** `ORDER BY
occurred_on DESC, id DESC` é servido pelo índice ascendente: os quatro dialetos
varrem índice de trás para frente. Índice declarado `DESC` seria ruído no
MySQL < 8.0 e mais uma diferença de dialeto para manter.

**Violação de unicidade é erro TIPADO.** `gorm.Config.TranslateError` (ligado
em `storage.Open`) converte o erro nativo de cada dialeto em
`gorm.ErrDuplicatedKey` — 23505 no PostgreSQL, 1062 no MySQL, 2627/2601 no SQL
Server, `SQLITE_CONSTRAINT_UNIQUE` no SQLite. O repositório o traduz em
`transaction.ErrDuplicateDedup`, que o service transforma em "linha bloqueada"
na revisão da importação, **nunca** em 500. Comparar mensagem de erro por
string quebraria na primeira atualização de driver. Verificado em SQLite
(`TestIndiceUnicoDeDedupRecusaRepetidoComErroReconhecivel`); nos outros três a
tradução vem do driver oficial e entra na suíte testcontainers da E8.

### `card_statements`

`id` · `household_id` · `account_id` · `competence_month varchar(7)` (mês do
**vencimento**) · `closing_date` / `due_date varchar(10)` · `source varchar(12)`
· timestamps · `deleted_at`.

```
ux_card_statements UNIQUE (household_id, account_id, competence_month)
(não existe índice comum ao lado do único: teria as mesmas colunas, na mesma ordem,
 e o único já atende a toda consulta que o comum atenderia)
```

`totalCents`, `paidCents` e `status` são **derivados dos lançamentos, nunca
colunas** (disciplina do ADR-017). O upsert é SELECT + INSERT/UPDATE dentro de
transação (**P8**) e procura **inclusive a fatura excluída logicamente**: a
chave única do banco não distingue excluído, então ignorá-la deixaria aquela
competência bloqueada para sempre.

### `accounts` — colunas novas do v3

`institution varchar(20) not null default 'other'` ·
`statement_closing_day int NULL` · `statement_due_day int NULL`.

O default no nível da **coluna** é o que faz o `AutoMigrate` preencher as contas
que já existem, em vez de deixá-las com string vazia numa coluna `NOT NULL` —
mesma lição de `households.timezone`.

### `import_batches` e `import_rows`

O arquivo enviado **não é guardado**: sobrevivem o nome, o `content_sha256` e
as linhas já interpretadas. `import_rows` **não tem coluna com a linha crua** —
ela carregaria CPF, CNPJ e agência de terceiros para uma tabela nova, com outro
ciclo de vida; e `reject_reason` é um **código** curto, nunca o conteúdo da
linha.

```
import_batches: id, household_id, account_id, created_by, institution, doc_kind,
  format_id, file_name, content_sha256, row_count, imported_count,
  skipped_count, blocked_count, restored_count, rejected_count, min_date,
  max_date, suggested_competence_month NULL, suggested_closing_date NULL,
  suggested_due_date NULL, status, expires_at, committed_at NULL, timestamps
    ix_import_batches_household (household_id, created_at)
    ix_import_batches_content   (household_id, content_sha256)
    ix_import_batches_expires   (expires_at)

import_rows: id, household_id, batch_id, seq, line_no, kind, occurred_on,
  amount_cents, description, description_norm, external_id NULL, dedup_key,
  status, reject_reason NULL, match_transaction_id NULL, created_at
    ux_import_rows_seq UNIQUE (batch_id, seq)
    ix_import_rows_batch      (household_id, batch_id, seq)
```

`import_rows.household_id` é **repetido de propósito**, mesmo existindo em
`import_batches`: o filtro de isolamento entre casas nunca pode depender de um
join — quem esquece o join devolve tudo.

A transição de estado do lote é **condicional no banco**
(`UPDATE ... WHERE status = 'pending'`, devolvendo linhas afetadas). É isso, e
não uma checagem no service, que torna a confirmação idempotente: entre ler o
estado e escrever cabe a outra requisição inteira.

### Limite de parâmetros por comando (armadilha registrada no v3)

O SQL Server aceita no máximo **2100 parâmetros** por comando e o SQLite
historicamente **999** (o default só subiu para 32766 na 3.32). Por isso a
inserção em lote vai em fatias de **30 linhas** (`createBatchSize`) e a busca
por lista de ids vai em fatias de **200** (`idChunkSize`): um lote maior
funcionaria em PostgreSQL e MySQL e estouraria exatamente nos outros dois — o
tipo de defeito que só aparece em produção.

## Schema v4 (spec 0005 — palavras-chave e transferências internas)

Duas tabelas novas, seis colunas novas, nenhuma coluna removida ou renomeada
(o `AutoMigrate` faz tudo, partindo de banco vazio **e** de banco v3 povoado —
`TestMigrateLevaBancoV3PovoadoParaV4`). Decisões em ADR-026.

### `category_keywords` e `account_keywords`

| Coluna | Tipo | Regra |
|---|---|---|
| `id` | `varchar(36)` | UUID v7 gerado no service |
| `household_id` | `varchar(36)` not null | **repetido de propósito**: o isolamento nunca depende de join, e é ele que abre o índice único |
| `category_id` / `account_id` | `varchar(36)` not null | a dona; sem FK física (ADR-013) |
| `keyword` | `varchar(40)` not null | forma **exibível**, como a pessoa digitou |
| `keyword_norm` | `varchar(40)` not null | forma de comparação (`textnorm.Normalize`); é o que o índice único compara |
| `position` | `int` not null default 0 | ordem de cadastro (0..19) — UUID v7 não garante ordem no mesmo milissegundo |
| `created_at` | timestamp not null | UTC |

```
category_keywords
    ux_category_keywords_norm UNIQUE (household_id, keyword_norm)
    ix_category_keywords_cat         (household_id, category_id)
account_keywords
    ux_account_keywords_norm  UNIQUE (household_id, keyword_norm)
    ix_account_keywords_acc          (household_id, account_id)
```

**Tabela própria, e não JSON na categoria.** A unicidade "por casa" precisa de
índice único **portátil**, e JSON não é consultável igual nos quatro dialetos
(e é proibido no caminho comum). **Todas as colunas do índice único são NOT
NULL** (armadilha P3: o SQL Server trata NULLs como iguais em índice único) e
**não há índice parcial** (P4: o MySQL não tem).

**Conjuntos independentes por tipo.** O índice único é de cada tabela, então a
mesma palavra pode existir numa categoria **e** numa conta da mesma casa. A
mesma palavra em duas categorias da mesma casa é recusada pelo índice — o
repositório traduz `gorm.ErrDuplicatedKey` em `category.ErrKeywordTaken` (ou
`account.ErrKeywordTaken`), cuja mensagem **não contém a palavra**. A mesma
palavra em outra casa é legítima. Verificado nos três casos por
`TestIndiceUnicoDePalavraChaveEPorCasaEPorTipo`.

**Largura do índice único.** `36 + 40` caracteres. MySQL/InnoDB `utf8mb4` →
`144 + 160 = 304 bytes` (< 3072 do formato `DYNAMIC`); SQL Server → 76 bytes
em `varchar` (152 se o driver usar `nvarchar`), contra 1700 de teto em índice
não clusterizado; PostgreSQL e SQLite sem limite relevante. **Sem prefixo de
índice.**

**Sem `deleted_at`.** Palavra-chave é configuração, não dado financeiro: a
lista é **substituída** inteira a cada PATCH (`DELETE` + `INSERT` na mesma
transação do chamador) e apagada **fisicamente** quando a categoria/conta é
excluída — no mesmo `UnitOfWork` (ADR-013). Arquivar mantém as palavras; quem
as tira da correspondência é o classificador (`internal/classify`), em Go.

### `import_rows` — colunas novas (sugestões da análise)

`suggested_category_id varchar(36) NULL` · `match_score int NULL` ·
`matched_keyword varchar(40) NULL` (forma **exibível**, não a norm) ·
`suggested_counterpart_account_id varchar(36) NULL`.

Todas anuláveis — são palpites calculados no servidor, e "sem sugestão" é
diferente de "sugestão vazia" — e nenhuma entra em índice (P3 não se aplica).
Na linha antiga, depois da migração, as quatro nascem **NULL**.

### `import_batches` — contadores novos

`linked_count int not null default 0` · `transfer_pairs_count int not null default 0`.

O default de **coluna** é o que faz o `AutoMigrate` preencher os lotes antigos
com 0, e não NULL numa coluna NOT NULL. Eles são coluna, e não derivados dos
lançamentos (exceção consciente ao ADR-017, registrada no ADR-026g): a ação
`link` grava o `import_batch_id` do lote **vinculador** numa perna que outro
lote criou, e derivar "pares criados" de `import_batch_id` passaria a mentir
para os dois lotes. `ImportBatchFootprint` continua servindo só o
`statement_id`; os contadores entram no **mesmo `UPDATE` condicional** da
transição de estado (`UpdateBatchStatus`).

### Consultas novas e o que as torna portáteis

| Consulta | Onde | Cuidado de portabilidade |
|---|---|---|
| `UPDATE transactions SET category_id, updated_at WHERE household_id = ? AND id IN (…) AND category_id IS NULL AND kind IN (…) AND deleted_at IS NULL` | `SetCategoryWhereNull` | `IN` fatiado em 200; o `category_id IS NULL` é a regra "nunca sobrescreve" escrita onde não pode ser contornada |
| `… transfer_group_id IN (SELECT transfer_group_id FROM transactions WHERE household_id = ? AND account_id = ? …)` | `ListTransferLegs` com contraparte | subconsulta **parametrizada pelo GORM**, SQL ANSI; subconsulta sobre a própria tabela num `SELECT` é aceita pelo MySQL (a restrição dele é só em `UPDATE`/`DELETE`) |
| totais por par de contas | `TransferLegsOfMonth` + Go | `GROUP BY (min(id), max(id))` exigiria `LEAST`/`GREATEST`, que o SQL Server não tem — a soma é feita em Go sobre uma projeção de 4 colunas com teto |
| `SumByAccountUntil` | saldo no fim do mês | `occurred_on <= 'YYYY-MM-DD'`: comparação lexicográfica que é cronológica por construção (D3) |
| `LinkImport` | `UPDATE … WHERE household_id = ? AND id = ? AND account_id = ? AND deleted_at IS NULL` | `RowsAffected = 0` → `ErrNotFound`; `import_batch_id` muda sempre, então o MySQL (que só conta mudança real) nunca devolve 0 por "nada mudou" |
| `TransferLegsForLinking` | duas consultas (janela por conta + contrapartes por grupo) | nenhuma por linha; perna sem contraparte viva é descartada em Go |
| `SELECT … WHERE household_id = ? AND competence_month = ? AND transfer_group_id IS NULL AND kind IN (…) AND (statement_id IS NULL OR kind <> 'expense') AND deleted_at IS NULL ORDER BY occurred_on, id LIMIT ?` | `ListTransferCandidates` (ADR-028) | `ix_transactions_competence`; ordem constante no código (P6) para a prévia ser reproduzível nos quatro dialetos; peça o teto + 1 — o repositório não conta. A cláusula da fatura é **`OR`**, e não `NOT (… AND …)`, porque a forma é idêntica nos quatro dialetos: **despesa de fatura não é reprocessada** (converter uma compra em `transfer_out` reduziria o total cobrado, que a pessoa confere contra o banco); a **receita** de fatura continua entrando, porque é o pagamento da fatura (ADR-016) |
| `SELECT … WHERE household_id = ? AND occurred_on BETWEEN ? AND ? AND transfer_group_id IS NULL AND kind IN (…) AND (statement_id IS NULL OR kind <> 'expense') AND deleted_at IS NULL` | `IncomeExpenseInWindow` (pool de espelhos) | `ix_transactions_occurred`; a folga de ±`DedupWindowDays` é somada **no repositório**, como em `WindowForDedup`; datas comparadas como texto `YYYY-MM-DD` (D3), o que é cronológico por construção; intervalo inválido é **erro**, nunca janela vazia; a mesma exclusão de despesa de fatura vale aqui — o espelho também é convertido |
| `UPDATE transactions SET kind, transfer_group_id, category_id = NULL, updated_at WHERE household_id = ? AND id = ? AND account_id = ? AND deleted_at IS NULL AND transfer_group_id IS NULL AND kind = ?` | `ConvertToTransferPair` (duas vezes por par) | cada comando tem de afetar **exatamente 1** linha, senão `ErrTransferConversionConflict` (409); o `account_id` no WHERE reconfere no banco que as pernas são de contas **diferentes** (ADR-016), em vez de confiar só na checagem em memória; o `kind` sempre muda, então o MySQL — que só conta mudança real — nunca devolve 0 por "nada mudou"; **só dentro da transação**: são dois comandos, e é o rollback que impede meia transferência |

## Schema v4 — sem mudança: naturezas de investimento (spec 0006)

**Nenhuma tabela nova, nenhuma coluna nova, nenhum índice novo.** O schema
**não muda de versão**: a E7 é a primeira entrega do projeto em que o
`AutoMigrate` não tem o que fazer — e provar isso é parte da entrega
(`TestMigrateMantemOSchemaV4ComAsNaturezasDeInvestimento`, que parte de banco
**v4 povoado**, roda o `AutoMigrate` duas vezes e afirma que a lista de
comandos DDL executados é **vazia**, não apenas que não houve erro). Decisões
em ADR-029.

| Item | Mudança |
|---|---|
| `categories.kind` | **Nenhuma.** Continua `varchar(10) not null`: `investment` e `redemption` têm **exatamente 10 caracteres**. Só a allowlist da aplicação cresce (`category.ValidKind`) |
| `category_keywords` | Inalterada. O índice único `(household_id, keyword_norm)` já garante a unicidade entre as **quatro** naturezas, sem regra nova |
| `transactions` | Inalterada. O lançamento continua `income`/`expense` (ADR-029b); a marcação é da **categoria**, nunca do lançamento |
| Índices | Nenhum novo. As consultas do mês entram por `ix_transactions_competence (household_id, competence_month)` e filtram `category_id IN (...)` sobre o conjunto das categorias marcadas (≤ 200 por casa, `category.MaxPerHousehold`) |
| Semente | `DefaultGroups()` ganha dois grupos. Idempotente e aplicada no auto-reparo do login — casa que já existe os recebe **sem migração de dados** |

⚠️ **Risco declarado:** `categories.kind` fica **exatamente** no limite de
`varchar(10)`. Uma quinta natureza com nome mais longo exigirá **alargar a
coluna** — portátil nos quatro dialetos, mas é migração, e o `AutoMigrate`
**não** faz alteração destrutiva de tipo. O teste de migração falha alto se
alguém acrescentar uma natureza que não caiba.

### `IN ()` nunca é emitido (ADR-029f)

`IN ()` é **erro de sintaxe** em três dos quatro dialetos e se comporta como
`1=0` no outro — a diferença entre "não há nada" e "a consulta quebrou" não
pode depender do banco. Por isso:

- o conjunto de categorias marcadas sai de **uma** leitura da taxonomia (≤ 200
  linhas, **arquivadas incluídas**: arquivar não desfaz a marcação do passado);
- conjunto **vazio** é `transaction.ErrEmptyCategoryFilter` nas consultas que
  dependem dele (`SumInvestmentsByMonth`, `ListByCategories`) e na escrita
  (`SetCategoryWhereCurrentIn`), **nunca** "sem filtro". Um slice vazio
  querendo dizer "tudo" devolveria o mês inteiro na tela de investimentos e
  transformaria o `overwriteCategorized` num sobrescrevedor universal;
- quem chama faz o **curto-circuito em Go**: casa sem categoria de
  investimento responde zeros e `items: []` **sem ir ao banco**. Conferido
  contando os comandos emitidos, e não o resultado
  (`TestConsultasPorCategoriaComListaVaziaNaoTocamOBanco`): um teste que só
  olhasse o resultado vazio passaria igual se a consulta tivesse rodado — que é
  exatamente o caso em que o `IN ()` iria para o banco;
- no `Summary`, conjunto vazio faz a coluna condicional **não entrar na
  consulta**: o SQL emitido volta a ser, byte a byte, o de antes da E7
  (`TestSummarySemCategoriaDeInvestimentoEmiteOSQLDeSempre` compara as três
  formas de vazio — `nil`, slice vazio e slice só com string vazia).

### Consultas novas e o que as torna portáteis

| Consulta | Onde | Cuidado de portabilidade |
|---|---|---|
| `SELECT kind, COUNT(*), COALESCE(SUM(amount_cents),0), SUM(CASE WHEN category_id IS NULL THEN 1 ELSE 0 END), COALESCE(SUM(CASE WHEN category_id IN (…) THEN amount_cents ELSE 0 END),0) … GROUP BY kind` | `Summary` (alterado) | **UMA** consulta, como sempre. A parte marcada é mais uma **coluna agregada da mesma linha**, e a receita/despesa exibida é `total − marked_total` — subtração **dentro da linha**, entre dois agregados das MESMAS linhas, com `amount_cents` sempre positivo: `marked_total ≤ total` por construção, e `expenseCents` não tem como ficar negativo. Duas consultas com subtração entre elas divergiriam sob escrita concorrente. A expressão condicional fica na **projeção** e não no `GROUP BY` por restrição real da camada: `gorm.DB.Group` recebe `string` e **não aceita variáveis de bind**, então repetir a expressão como chave de agrupamento obrigaria a escrever os ids no texto do SQL (injeção — §3 de docs/SEGURANCA.md, recusado pelo `TestSemSQLMontadoNoGormstore`); agrupar pelo **alias** não é alternativa, porque MSSQL e PostgreSQL não agrupam por alias. A forma escolhida entrega os mesmos números por `kind` com o mesmo plano de hoje. **Ratificada em 18/09/2026 — ADR-029(j).** A desigualdade `0 ≤ marked_total ≤ total` é **verificada** no código antes de publicar, e não confiada: ela depende de `amount_cents ≥ 0`, que é invariante do caminho de escrita e não do schema — mesma disciplina do `somaSegura` do `internal/report` (ADR-029 j.1) |
| `SELECT competence_month, kind, COUNT(*), COALESCE(SUM(amount_cents),0) … WHERE household_id = ? AND deleted_at IS NULL AND kind IN (…) AND category_id IN (…) AND competence_month >= ? AND competence_month <= ? GROUP BY competence_month, kind` | `SumInvestmentsByMonth` | `ix_transactions_competence`. **Uma** consulta serve os **três** números da tela (mês, ano até o mês, série de 12): a janela do ano (`Y-01..M`) está sempre **contida** na dos 12 meses (`M-11..M`), então as ≤ 24 linhas (12 meses × 2 kinds) bastam, montadas em Go — nem 12 consultas, nem 2 que possam discordar entre si. `competence_month` é `varchar(7)` `AAAA-MM` de largura fixa: comparação **lexicográfica é cronológica**, a mesma propriedade que `SumByAccountUntil` usa em `AAAA-MM-DD` (D3), e a razão de não haver **nenhuma** função de data no SQL (quatro sintaxes diferentes — P1). Janela invertida ou vazia é **erro**, nunca zeros em silêncio. A ordenação dos meses é feita **em Go** (≤ 24 itens), o que tira do caminho a última diferença possível entre dialetos (collation) |
| `SELECT * … WHERE household_id = ? AND deleted_at IS NULL AND competence_month = ? AND kind IN (…) AND category_id IN (…) AND (occurred_on < ? OR (occurred_on = ? AND id < ?)) ORDER BY occurred_on DESC, id DESC LIMIT ?` | `ListByCategories` | O **mesmo** cursor e a **mesma** ordem de `GET /transactions`: comparação do cursor na forma **expandida** (tupla `(a,b) < (c,d)` não existe no SQL Server) e `ORDER BY` **constante no código** (P6/S4 — ordenação vinda do cliente é injeção). Peça `limit+1` para saber se há próxima página: o repositório não faz `COUNT`. O `kind IN (income, expense)` **não é redundância** com o filtro de categoria: é o que garante que esta lista mostre exatamente as linhas que `SumInvestmentsByMonth` soma — duas perguntas diferentes para o mesmo dinheiro é como se começa a ter dois números |
| `SELECT id, kind, description, description_norm, category_id … WHERE household_id = ? AND deleted_at IS NULL AND competence_month = ? AND kind IN (…) ORDER BY occurred_on ASC, id ASC LIMIT ?` | `ListIncomeExpenseOfMonth` | `ix_transactions_competence`. É `ListUncategorized` **sem** o `category_id IS NULL`, e essa ausência é o assunto do `detect` de investimentos: a linha sem categoria ele marca, a linha **com** categoria ele lista à parte e só troca com `overwriteCategorized`. Uma consulta, e não duas (uma por grupo), que leriam o mesmo índice duas vezes e poderiam discordar entre si — a prévia mostraria uma linha em nenhuma das listas, ou nas duas. `limit = teto+1` decide o 422 de `MaxAutoCategorizeRows` sem ler o mês inteiro |
| `UPDATE transactions SET category_id = ?, updated_at = ? WHERE household_id = ? AND deleted_at IS NULL AND id IN (…) AND kind IN (…) AND category_id IN (<allowlist>)` | `SetCategoryWhereCurrentIn` | A escrita do `overwriteCategorized`, a primeira do projeto autorizada a **substituir categoria já escolhida** (ADR-029h). A restrição que a torna segura mora no **`WHERE`**, não no Go: a allowlist são os ids das categorias de natureza `income`/`expense`, então a troca **nunca desfaz uma marcação de investimento** — e linha sem categoria também não é alcançada, porque `NULL` nunca está num `IN (...)`. Entre a prévia e a confirmação cabe uma requisição inteira, e é o banco que decide o que a linha ainda era. Devolve linhas afetadas; zero é resultado legítimo (idempotência), não erro |

### Orçamento de parâmetros do primeiro comando com **dois** `IN (...)`

`SetCategoryWhereCurrentIn` é o primeiro comando do projeto com duas listas no
mesmo `WHERE`. A conta, medida (não estimada) em
`TestSetCategoryWhereCurrentInCabeNoOrcamentoDeParametros`:

```
2 (SET: category_id, updated_at) + 1 (household_id) + 200 (fatia de ids)
  + 2 (kinds) + 200 (allowlist) = 405 parâmetros por comando
```

Folgado contra o piso histórico de **999** do SQLite e contra os **2100** do
SQL Server. Os alvos são fatiados em `idChunkSize` (200); a **allowlist não é
fatiada** — fatiá-la mudaria o significado do comando, porque as duas listas
estão em `AND`.

O conjunto de categorias, por outro lado, **nunca** é fatiado nas leituras:
`maxCategoryFilterIDs = 200` (o mesmo número de `category.MaxPerHousehold`) e
acima disso é `transaction.ErrTooManyCategories`. Fatiar uma **agregação**
obrigaria a somar fatias de dinheiro, e fatiar uma consulta **paginada por
cursor** quebraria a página. Se um dia o teto do domínio subir, a consulta
falha **alto** aqui, em vez de estourar dentro do driver, em dois dialetos só,
em produção.
