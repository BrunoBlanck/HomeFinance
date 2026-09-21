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
| Semente | `DefaultGroups()` ganha dois grupos. ⚠️ **ERRATUM de 18/09/2026:** a frase original dizia que a semente é "aplicada no auto-reparo do login" e que "casa que já existe os recebe **sem migração de dados**". **É falso.** `household.EnsureDefault` devolve cedo quando o usuário já tem casa, então a semente roda **uma vez por casa, na criação** dela — as casas anteriores a 17/09 **não** receberam "Investimentos"/"Resgates". Ver o erratum no ADR-033 e o backlog do `docs/ROADMAP.md` (backfill opt-in) |

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

## Schema v4 — sem mudança: filtro de tipo em `GET /transactions` (emenda §12 da spec 0004, E2d)

**Nenhuma tabela nova, nenhuma coluna nova, nenhum índice novo.** É a segunda
entrega seguida em que o `AutoMigrate` não tem o que fazer, e provar isso
continua sendo parte da entrega.

O parâmetro `kindGroup` (allowlist **fechada**: `income` · `expense` ·
`transfer` · `investment`; ausente = tudo) vira **um predicado a mais** no
`WHERE` da **`List`** — e **em nenhum predicado do `Summary`**, pelo motivo da
seção seguinte. Seja **M** o conjunto de ids das categorias
de natureza `investment`/`redemption` da casa (arquivadas incluídas, ≤ 200 —
`category.MaxPerHousehold`), lido **uma vez por requisição** da mesma
taxonomia que já alimenta o resumo e **nunca** vindo do cliente:

| `kindGroup` | Predicado adicional |
|---|---|
| ausente | *(nenhum)* |
| `income` | `kind = ?` + `(category_id IS NULL OR category_id NOT IN (M))` |
| `expense` | idem, com o outro kind |
| `transfer` | `kind IN (transfer_out, transfer_in)` — lista **constante no código** |
| `investment` | `kind IN (income, expense)` + `category_id IN (M)` |

O grupo escolhe **qual cláusula entra**; ele nunca vira texto de SQL. Os kinds
vão por placeholder a partir de listas constantes, e os ids de M por `IN ?` /
`NOT IN ?`, que o GORM expande em parâmetros. Grupo fora da allowlist é
`transaction.ErrUnknownKindGroup` — **falha fechada**, nunca "sem filtro":
tratar o desconhecido como ausente devolveria a janela **inteira** a quem pediu
um recorte.

### O `Summary` **não** filtra por tipo — e isso é o desenho

O `WHERE` do resumo é o **mesmo para as cinco opções**: casa + mês + conta,
byte a byte o de antes da E2d. `investedCents` e `redeemedCents` **não
descrevem a janela** — descrevem o que **saiu** de `expenseCents`/`incomeCents`
(ADR-029e), e é deles que vive a frase "Fora destes números: R$ X em aportes"
(spec 0006 §3.5.2). Filtrar o resumo por tipo apagaria essa explicação
justamente sob `?tipo=despesas`, que é onde a omissão é maior.

A distinção entre os cinco recortes migra do `WHERE` para a **projeção** — a
disciplina que o ADR-029(j) já havia ratificado. A projeção dos marcados ganha
uma **segunda** coluna condicional sobre o mesmo conjunto:

```
COALESCE(SUM(CASE WHEN category_id IN (?) THEN amount_cents ELSE 0 END), 0) AS marked_total
SUM(CASE WHEN category_id IN (?) THEN 1 ELSE 0 END)                        AS marked_cnt
```

Com `cnt`, `total`, `uncategorized`, `marked_total` e `marked_cnt` por `kind`,
os cinco resumos saem por **aritmética em Go, no serviço**, sobre a MESMA linha
agregada — `count` sob `expense` é `expense.cnt − expense.marked_cnt`, sob
`investment` é `income.marked_cnt + expense.marked_cnt`, e `invested`/`redeemed`
são **iguais nas cinco**. Cinco `WHERE`s diferentes poderiam discordar entre si
sob escrita concorrente; uma consulta só, não.

`TestSummaryEmiteOMesmoSQLParaOsCincoGruposDeTipo` compara o **comando
emitido** (não o resultado) nas cinco opções. É ele que trava o desenho contra
a "correção" que alguém tentará daqui a três meses — um teste de resultado
passaria igual se o `WHERE` tivesse mudado e o dado do cenário não
distinguisse os casos.

⚠️ **Armadilha medida, para quem for montar os números:**
`ByKind[transfer_out].Uncategorized` e `ByKind[transfer_in].Uncategorized` são
**iguais a `Count`**, e não zero — transferência nunca tem categoria
(ADR-016), então toda perna casa com `category_id IS NULL`. Somar os quatro
`kind`s produziria "pendências" que ninguém consegue resolver, porque não há
categoria para atribuir a uma perna. O número de pendências soma **apenas**
`income` e `expense`. A projeção sobe **crua** de propósito: derivar no
repositório esconderia a diferença justamente de quem precisa vê-la.

### `category_id IS NULL OR` é obrigatório nos quatro dialetos

`NULL NOT IN (…)` avalia para **NULL** — não para falso, e muito menos para
verdadeiro — e o `WHERE` só deixa passar o que é **VERDADEIRO**. É a lógica de
três valores do SQL-92, idêntica em PostgreSQL, MySQL, SQLite e SQL Server:
`IN`, `NOT IN`, `OR` e `IS NULL` com lista parametrizada são ANSI, e **nada
aqui pede variante por dialeto**.

Sem a guarda, a aba "Despesas" perderia **toda despesa sem categoria** — em
silêncio, com o total do resumo caindo junto. É o defeito mais perigoso desta
feature, e por isso ele tem prova **medida**, e não deduzida da leitura do
padrão: `TestNullNotInNaoPassaNoWhereNesteDialeto` executa as duas formas
contra o banco real e afirma a diferença (sem a guarda, 1 linha; com a guarda,
2). Roda em SQLite hoje, em PostgreSQL com `TEST_POSTGRES_DSN`, e nos outros
dois quando a suíte testcontainers da E8 entrar.

A mesma armadilha do ADR-029f vale aqui, dos dois lados:

- `income`/`expense` com **M vazio** (o caso de toda casa que não marca nada):
  a cláusula `NOT IN` **não entra na consulta**, e o SQL emitido volta a ser
  byte a byte o de antes da E2d — sem `IN ()`, que é erro de sintaxe em três
  dialetos e `1=0` no quarto
  (`TestFiltroDeTipoSemCategoriaMarcadaNaoEmiteNotIn`, comparando as três
  formas de vazio: `nil`, slice vazio e slice só com string vazia);
- `investment` com **M vazio** é `ErrEmptyCategoryFilter` e **nunca** "todas as
  categorias" — vazio significando "tudo" devolveria a janela inteira rotulada
  como investimento. Quem chama faz o curto-circuito em Go antes; o
  repositório recusa **sem emitir comando nenhum**, conferido contando os
  comandos e não o resultado
  (`TestFiltroDeTipoInvestimentoComListaVaziaNaoTocaOBanco`).

### Por que **nenhum** índice novo — medido, não intuído

`EXPLAIN QUERY PLAN` em SQLite sobre uma tabela de **9.600 lançamentos**
distribuídos como em produção (20 casas × 12 meses × 40 linhas, com `ANALYZE`
aplicado):

| Consulta | Plano |
|---|---|
| `List` **hoje** (sem `kindGroup`) | `SEARCH … USING INDEX ix_transactions_competence (household_id=? AND competence_month=?)` + temp b-tree para o `ORDER BY` |
| `List` com `kindGroup=expense` (M vazio e M=1) | **o mesmo plano**, sem diferença |
| `List` com `kindGroup=transfer` (grupo raro) | **o mesmo plano** |
| `List` com `kindGroup=investment` | **o mesmo plano** — e é, coluna por coluna, o plano do `ListByCategories` da E7, que já está em produção |
| `Summary`, **as cinco opções** | `ix_transactions_competence` + temp b-tree para o `GROUP BY` — **o mesmo comando**, logo o mesmo plano por construção |

O filtro **não muda o plano da `List`**, e no `Summary` não há o que mudar —
o comando é idêntico nas cinco opções. A `List` entra pelo
mesmo índice de sempre, e `kind`/`category_id` ficam como predicados
**residuais** sobre o conjunto que o índice já devolveu — a fatia
`(household_id, competence_month)`, que na amostra é de **40 linhas** e que o
próprio projeto declara limitada a **10.000** no pior caso
(`MaxAutoCategorizeRows`, "um mês grande").

⚠️ A medição com **uma casa só** engana: naquele dado `household_id = ?` casa
com a tabela inteira, o índice deixa de ser seletivo e o SQLite troca para
`ix_transactions_occurred` (ou `SCAN`). O número acima é o da distribuição
realista — a de uma casa dominante não descreve produção, e essa é a diferença
entre medir e achar que mediu.

O índice que ajudaria seria `(household_id, competence_month, kind)`, e ele não
se paga:

- economiza a avaliação residual de `kind` sobre ≤ 10.000 linhas — na amostra,
  13 linhas de 40. Duas comparações de `varchar(12)` por linha;
- **não ajuda em nada** o `category_id NOT IN (M)`: predicado de negação sobre
  lista não é servível por índice em **nenhum** dos quatro dialetos, então o
  custo dominante ficaria exatamente onde está;
- seria o **quinto** índice composto da tabela mais escrita do sistema (toda
  linha de toda importação o mantém) e mais um objeto para o `AutoMigrate`
  criar de forma portátil nos quatro dialetos — o oposto do que a E2d promete.

A ordenação continua `occurred_on DESC, id DESC`, **constante no código** (P6 /
S4), e o cursor continua na forma expandida — tupla `(a,b) < (c,d)` não existe
no SQL Server. O filtro tira linhas; ele não reordena nem repete página
(`TestFiltroDeTipoMantemOrdemECursor`).

### Consultas novas e o que as torna portáteis

| Consulta | Onde | Cuidado de portabilidade |
|---|---|---|
| `SELECT * … WHERE household_id = ? AND deleted_at IS NULL AND competence_month = ? AND kind = ? AND (category_id IS NULL OR category_id NOT IN (…)) AND (occurred_on < ? OR (occurred_on = ? AND id < ?)) ORDER BY occurred_on DESC, id DESC LIMIT ?` | `List` com `kindGroup` | O `category_id IS NULL OR` é **obrigatório** e ANSI: `NULL NOT IN (…)` é NULL nos quatro dialetos, e sem ele a despesa sem categoria sumiria da aba "Despesas". O GORM **envolve a cláusula em parênteses** (`AND (category_id IS NULL OR …)`) — sem isso o `OR` se espalharia pelo `WHERE` inteiro e a consulta devolveria a casa toda; é asserção de teste, não confiança. M vazio faz a cláusula **não entrar** (`IN ()` nunca é emitido — ADR-029f); em `investment`, M vazio é `ErrEmptyCategoryFilter`. Grupo fora da allowlist é `ErrUnknownKindGroup`, nunca "sem filtro". Entra por `ix_transactions_competence`: **o mesmo plano de antes da E2d** |
| `SELECT kind, COUNT(*), COALESCE(SUM(amount_cents),0), SUM(CASE WHEN category_id IS NULL …), [COALESCE(SUM(CASE WHEN category_id IN (…) …),0) AS marked_total, SUM(CASE WHEN category_id IN (…) THEN 1 ELSE 0 END) AS marked_cnt] … WHERE household_id = ? AND deleted_at IS NULL AND competence_month = ? AND account_id = ? GROUP BY kind` | `Summary` (alterado) | **Não ganha `NOT IN`: ganha uma segunda projeção condicional.** O `kindGroup` **nunca entra no `WHERE`** — o comando é idêntico nas cinco opções, e a distinção vive na projeção e na aritmética do serviço (ver a seção acima). É o comando com **duas listas de 200** do projeto, as duas no `SELECT` e as duas com o **mesmo** conjunto: dinheiro marcado e contagem marcada são perguntas diferentes sobre as **mesmas linhas**, e é por saírem da mesma varredura que não podem discordar. A expressão condicional fica na projeção, e não no `GROUP BY`, pela restrição de sempre: `gorm.DB.Group` recebe `string` e não aceita bind vars, então agrupar por ela obrigaria a escrever os ids no texto do SQL (injeção — recusado pelo `TestSemSQLMontadoNoGormstore`), e agrupar por alias não funciona em MSSQL nem em PostgreSQL. Com M vazio **nenhuma** das duas colunas entra (`IN ()` nunca é emitido). `SUM`/`COUNT`/`CASE`/`COALESCE` são ANSI; nada aqui pede variante |

### Orçamento de parâmetros — medido

Pior caso do E2d: `Summary` com `accountId` e **M = 200**.
Medido por `TestFiltroDeTipoCabeNoOrcamentoDeParametros` (mesma forma do molde
`TestSetCategoryWhereCurrentInCabeNoOrcamentoDeParametros`), contando os bind
vars reais do comando emitido:

```
200 (marked_total) + 200 (marked_cnt) + 1 (household_id)
  + 1 (competence_month) + 1 (account_id) = 403 parâmetros
```

O número é **igual nas cinco opções** de `kindGroup`, porque o comando é o
mesmo. Não há `NOT IN` no resumo.

`List` no mesmo recorte, **com conta e cursor**: **207** — o `LIMIT` vai
**inline** no texto, e não como bind var, que é mais uma razão para medir em
vez de deduzir do código. Os dois folgados contra o piso histórico de **999**
do SQLite e contra os **2100** do SQL Server.

M **não é fatiado**, pelo motivo de sempre: fatiar uma agregação obrigaria a
somar fatias de dinheiro, e fatiar uma consulta paginada por cursor quebraria a
página. Acima de `maxCategoryFilterIDs` (200) é `ErrTooManyCategories` — falha
**alta**, e não um comando que estoura dentro do driver em dois dialetos só.

### A prova de que o schema continua v4

`TestMigrateMantemOSchemaV4ComOFiltroDeTipo` parte de banco v4 **povoado**,
roda o `AutoMigrate` **duas vezes** e afirma que a lista de comandos DDL
executados é **vazia** — não apenas que não houve erro. Depois roda os
**quatro** grupos (`List` e `Summary`) contra o banco migrado e reconfere que o
`AutoMigrate` continua sem nada a fazer: a leitura não cria objeto nenhum pelas
costas.

Ele é **irmão**, e não cópia, de
`TestMigrateMantemOSchemaV4ComAsNaturezasDeInvestimento`: aquele já provaria o
"zero DDL" da E2d — lê `gormstore.Models()` em tempo de execução, então um
índice novo apareceria lá como `CREATE INDEX` —, mas não amarra a afirmação a
esta entrega nem prova que as consultas novas rodam sem nada que falte. Os dois
passam.

## Schema v4 — sem mudança: a agregação do painel (spec 0008, E4 1ª fatia)

**Nenhuma tabela nova, nenhuma coluna nova, nenhum índice novo.** É a
**terceira** entrega seguida em que o `AutoMigrate` não tem o que fazer. A
faixa de resumo do painel — receita do mês, gasto no cartão de crédito e
investido líquido — sai de **uma** consulta agregada nova
(`SumMonthByKindAndAccount`), que mora em arquivo próprio do `gormstore`
(`dashboard_queries.go`) e **não** entra na interface `transaction.Repository`:
é método do `TransactionRepository`, exposto ao pacote `dashboard` pela
interface que **ele** declara — o mesmo arranjo do `SumByCategoryAndAccount`
(ADR-027a; o método se chamava `SumByCategory` até o recorte crédito/débito do
relatório trazer a conta para a chave de agrupamento — ADR-032).
Decisões em **ADR-031**.

### A consulta

SQL **emitido** (capturado do comando real, na forma de 6 colunas):

```sql
SELECT kind AS kind, account_id AS account_id, COUNT(*) AS cnt,
       COALESCE(SUM(amount_cents), 0) AS total,
       COALESCE(SUM(CASE WHEN category_id IN (?,?) THEN amount_cents ELSE 0 END), 0) AS marked_total,
       SUM(CASE WHEN category_id IN (?,?) THEN 1 ELSE 0 END) AS marked_cnt
  FROM transactions
 WHERE household_id = ? AND deleted_at IS NULL AND competence_month = ?
   AND kind IN (?,?)
 GROUP BY kind, account_id
```

Com o conjunto de categorias marcadas **vazio** — o estado de toda casa que
ainda não marca investimento — as duas últimas colunas **não entram**, e o
comando volta à forma de **4 colunas**, com 4 parâmetros.

### A conta vai na CHAVE de agrupamento, e não na projeção

O rascunho §5 da spec previa a conta como **terceira coluna condicional**
(`SUM(CASE WHEN account_id IN (?) …)`), sobre `GROUP BY kind`. Essa forma foi
**rejeitada por dois motivos medidos** — e os dois estão escritos no código,
porque é a "simplificação" que alguém vai tentar daqui a seis meses:

1. **Perderia dinheiro em silêncio.** Excluir os marcados do cartão exigiria
   uma coluna de **interseção** (cartão ∧ marcado); a alternativa tentadora,
   `account_id IN (?) AND (category_id NOT IN (?))`, apaga **toda despesa de
   cartão sem categoria** nos quatro dialetos — `NULL NOT IN (…)` é **NULL**,
   `CASE WHEN NULL` cai no `ELSE`, e o `WHERE` só deixa passar o VERDADEIRO
   (a mesma armadilha da seção anterior, provada por
   `TestNullNotInNaoPassaNoWhereNesteDialeto`). "Sem categoria" é o estado
   **normal** logo depois de importar uma fatura: o gasto do cartão apareceria
   menor exatamente no mês em que o usuário acabou de importá-la. Com a conta
   na chave o problema **deixa de existir**: a linha sem categoria cai no
   `ELSE` da coluna marcada (correto — ela não é aporte) e continua inteira em
   `COUNT(*)` e `SUM(amount_cents)`, que é de onde sai o bruto do cartão.
   Prova **positiva** (o dinheiro aparece):
   `TestPainelContaDespesaDeCartaoSemCategoriaNoTotalBruto`.
2. **Estouraria o orçamento de parâmetros** — a conta está na seção abaixo.

O preço é uma linha por `(kind, conta)` em vez de uma por `kind`, e ele é
barato: a saída é ≤ **2 × `account.MaxPerHousehold`** (100) linhas, e o teto é
**real** porque conta com qualquer lançamento **não pode ser excluída** (o
`UsageChecker`/`ExistsByAccount` responde 422) — toda conta que aparece na
agregação é viva e da casa. Quem dobra as ≤ 100 linhas nos três números é o
**serviço**; os ids de **conta nunca entram no SQL** (a interseção com o
conjunto de cartões é feita em Go, sobre a lista de contas da própria casa).

### Consulta nova e o que a torna portátil

| Consulta | Onde | Cuidado de portabilidade |
|---|---|---|
| `SELECT kind, account_id, COUNT(*), COALESCE(SUM(amount_cents),0), [COALESCE(SUM(CASE WHEN category_id IN (…) THEN amount_cents ELSE 0 END),0) AS marked_total, SUM(CASE WHEN category_id IN (…) THEN 1 ELSE 0 END) AS marked_cnt] … WHERE household_id = ? AND deleted_at IS NULL AND competence_month = ? AND kind IN (…) GROUP BY kind, account_id` | `SumMonthByKindAndAccount` (nova) | **UMA** consulta para os **três** números da faixa: três consultas varreriam o mesmo índice três vezes e poderiam **discordar** entre si sob escrita concorrente — a receita sairia sem os resgates de uma leitura e o líquido com os resgates de outra, e a mesma tela mostraria dois dinheiros. `SUM`, `COUNT`, `CASE WHEN`, `COALESCE` e `IN` com lista parametrizada são **ANSI**, idênticos nos quatro dialetos; **nenhuma função de data** (extrair mês tem quatro sintaxes — P1), porque `competence_month` chega pronto da aplicação. **Sem `ORDER BY`**: a dobra é em Go sobre ≤ 100 linhas, o que tira do caminho a última diferença que sobraria entre dialetos (**collation**). A expressão condicional fica na **projeção** e não no `GROUP BY`, pela restrição de sempre: `gorm.DB.Group` recebe `string` e **não aceita bind vars**, então agrupar por ela obrigaria a escrever os ids no **texto** do SQL (injeção — recusado pelo `TestSemSQLMontadoNoGormstore`), e agrupar por **alias** não funciona em MSSQL nem em PostgreSQL. `kind IN (income, expense)` vem da constante `categorizableKinds`: perna de transferência **nunca é lida** (ADR-016), o que torna os critérios 6 e 7 da spec **estruturais** — pagar a fatura não mexe em número nenhum. Conjunto de marcadas **vazio** faz as duas colunas **não entrarem** (`IN ()` nunca é emitido — ADR-029f); as **três** formas de vazio (`nil`, slice vazio, slice só com string vazia) produzem a **mesma** string, byte a byte, conferido por **comando emitido** (`TestPainelEmiteAFormaDeQuatroColunasSemCategoriaMarcada`). Vazio aqui **não é erro** — diferente de `SumInvestmentsByMonth`, em que significaria "todas as categorias" |

Guardas **antes** de qualquer SQL, conferidas por **contagem de comandos** e
não por resultado (`TestPainelRecusaCasaOuMesVazioSemTocarOBanco`,
`TestPainelRecusaCategoriasDemaisSemTocarOBanco`): casa vazia ou mês vazio é
**erro** — `competence_month = ''` devolveria um mês inteiramente zerado em
silêncio, e casa vazia é a porta do BOLA —, e acima de `maxCategoryFilterIDs`
(200) é `transaction.ErrTooManyCategories`, que o serviço mapeia para **500
genérico** (ADR-029 j.2): o teto violado é o da própria taxonomia, não entrada
do usuário. O conjunto **não é fatiado**, pelo motivo de sempre — fatiar uma
agregação obrigaria a somar fatias de dinheiro.

A projeção sobe **crua**: `total − marked_total` é aritmética do **serviço**, e
só depois de ele **verificar** `0 ≤ marcado ≤ total` (ADR-029 j.1) — verificar,
nunca confiar, porque a desigualdade depende de `amount_cents ≥ 0`, que é
invariante do **caminho de escrita** e não do schema.

### Orçamento de parâmetros — medido, com a conta da forma rejeitada

Pior caso: 200 categorias marcadas (o teto real, `category.MaxPerHousehold`).
Medido por `TestPainelCabeNoOrcamentoDeParametros` (molde
`TestSetCategoryWhereCurrentInCabeNoOrcamentoDeParametros`), contando os bind
vars do comando **emitido**:

```
200 (marked_total) + 200 (marked_cnt) + 1 (household_id)
  + 1 (competence_month) + 2 (kinds)               = 404 parâmetros
```

A **forma rejeitada** (conta na projeção), pela mesma conta:

```
2 (casa + mês) + 4 × 200 (categorias, incluindo a coluna de interseção)
  + 4 × 50 (contas, account.MaxPerHousehold)       = 1002 parâmetros
```

**1002 está acima do piso histórico de 999 do SQLite**, que o projeto adota
como teto de portabilidade. Funcionaria em PostgreSQL e MySQL e estouraria
dentro do driver nos outros dois — o tipo de defeito que só aparece em
produção. A aritmética dos 1002 é **asserção do teste**, não comentário: se um
dia o teto de contas ou de categorias mudar, é o teste que avisa.

Forma de **4 colunas** (casa sem categoria de investimento): **4** parâmetros.
Ids de **conta**: **zero**, em qualquer forma. Os dois casos folgados contra os
999 do SQLite e os 2100 do SQL Server.

### Índice: o de sempre, e o plano é o **mesmo** do `Summary` — medido

`ix_transactions_competence (household_id, competence_month)`. **Nenhum índice
novo.** `EXPLAIN QUERY PLAN` em SQLite sobre a mesma distribuição realista da
seção anterior (20 casas × 12 meses × 40 linhas = 9.600 lançamentos, com
`ANALYZE` aplicado e os parâmetros **ligados a valores reais** — ver o aviso
abaixo):

| Consulta | Plano |
|---|---|
| **painel, 4 colunas** | `SEARCH transactions USING INDEX ix_transactions_competence (household_id=? AND competence_month=?)` + `USE TEMP B-TREE FOR GROUP BY` |
| **painel, 6 colunas** | **o mesmo plano**, sem diferença |
| `Summary` (E2d) | **o mesmo plano** — linha por linha |
| `List` (E2d) | `ix_transactions_competence` + `USE TEMP B-TREE FOR ORDER BY`, como já estava medido |

O painel entra pelo **mesmo índice** e sai pelo **mesmo plano** do `Summary`:
a chave de agrupamento a mais (`account_id`) entra no b-tree temporário que já
existia para o `GROUP BY`, e as colunas condicionais são avaliação **por
linha**, que nenhum índice serviria. **Não há divergência a reportar, e por
isso não há índice novo** — índice não se cria sem medição.

⚠️ **Armadilha do método, medida aqui:** rodar o `EXPLAIN QUERY PLAN` com os
parâmetros **não ligados** fez o plano da `List` trocar para
`ix_transactions_occurred` — o planejador sem valores decide diferente. O
número da tabela acima é o dos parâmetros ligados aos valores reais. Medir com
placeholder vazio não é medir.

**Volume, medido** (`dashboard_volume_test.go`, molde de
`report_volume_test.go`): 10.000 lançamentos vivos no mês, 10 contas (3
cartões), 200 categorias com 40 marcadas, mais ruído de outro mês, de outra
casa, de excluídos e das **duas** pernas de 60 transferências → **15 linhas, 1
consulta, ~21 ms** em SQLite (três execuções: 20,7 · 23,0 · 21,6 ms). A
referência da E6a (`SumByCategory`) no mesmo volume e na mesma máquina foi
**24,1 ms** — mesma ordem de grandeza, com duas colunas condicionais e uma
chave de agrupamento a mais. *(Registro do passado, mantido como foi medido:
aquele `SumByCategory` foi **superado** pelo `SumByCategoryAndAccount` da E6b
— ADR-032 —, que traz a conta para a chave de agrupamento. A medição da forma
nova está na última seção deste documento.)* O que o teste **asserta** é a contagem de
consultas (`1`) e o teto de linhas (`2 × account.MaxPerHousehold`); o tempo é
**impresso**, não assertado — tempo de máquina de CI não é critério de
correção, e a asserção de tempo existe só como rede grossa (< 5 s).

### A prova de que o schema continua v4

Não há teste de migração **novo** nesta entrega, e isso é deliberado: os dois
que já existem provam o que precisa ser provado.
`TestMigrateMantemOSchemaV4ComAsNaturezasDeInvestimento` e
`TestMigrateMantemOSchemaV4ComOFiltroDeTipo` partem de banco v4 **povoado**,
rodam o `AutoMigrate` **duas vezes** e afirmam que a lista de comandos DDL
executados é **vazia** — e os dois leem `gormstore.Models()` em **tempo de
execução**, então uma tabela, uma coluna ou um índice novo apareceria lá como
`CREATE …` e os quebraria. Esta entrega não acrescenta **nenhum** modelo,
**nenhuma** tag `gorm:"…"` e **nenhuma** entrada em `Models()`: ela só lê.

## Schema v4 — sem mudança: recorte crédito/débito do relatório (ADR-032, E6b)

**Nenhuma tabela nova, nenhuma coluna nova, nenhum índice novo.** É a **quarta**
entrega seguida em que o `AutoMigrate` não tem o que fazer (contagem do
ADR-032d). O recorte `accountGroup = credit | debit` não acrescenta **nenhum**
predicado ao SQL: a conta entra na **chave de agrupamento** e os dois recortes
viram **partições das mesmas linhas**, feitas em Go pelo serviço sobre a lista
de contas da própria casa. `SumByCategory`/`CategoryTotal` passam a se chamar
`SumByCategoryAndAccount`/`CategoryAccountTotal` — renomear é de código, não de
schema.

### A consulta

SQL **emitido** (capturado do comando real, em SQLite):

```sql
SELECT category_id AS category_id, account_id AS account_id, COUNT(*) AS cnt,
       COALESCE(SUM(amount_cents), 0) AS total
  FROM transactions
 WHERE household_id = ? AND deleted_at IS NULL
   AND competence_month = ? AND kind = ?
 GROUP BY category_id, account_id
```

**Três** variáveis de bind — e **sempre três**, nos três recortes (`todas`,
`credit`, `debit`), porque o recorte não toca o comando. Medido no comando
emitido, não deduzido do código. Sem `IN`, sem `JOIN`, sem `ORDER BY` e **sem
`LIMIT`**: `LIMIT` sobre `GROUP BY` sem `ORDER BY` é não determinístico, e o
teto da saída é **estrutural**, não de volume:

```
(category.MaxPerHousehold + 1) × account.MaxPerHousehold = (200 + 1) × 50 = 10 050 linhas
```

O `+ 1` é o balde **"Sem categoria"**: `category_id` nulo agrupa numa linha só.
O teto é **real** porque nem categoria nem conta com lançamento podem ser
excluídas (o `UsageChecker` responde 422) — toda chave que aparece na agregação
é viva e da casa.

### Índice: o de sempre, e o plano é o **mesmo** do painel — medido

`ix_transactions_competence (household_id, competence_month)`. **Nenhum índice
novo**, como o ADR-032(d) esperava — e agora **medido**, não intuído.

Método (o mesmo das seções anteriores, com o aviso do ADR-030e respeitado):
`EXPLAIN QUERY PLAN` em SQLite (`github.com/glebarez/sqlite`) sobre **9.600
lançamentos** em distribuição realista — **20 casas × 12 meses × 40 linhas**,
200 categorias e 10 contas (3 `credit_card`) por casa —, com **`ANALYZE`
aplicado** e os parâmetros **ligados aos valores reais**. A casa medida
responde por **480 de 9.600 linhas (5,0 %)**: nenhuma casa domina a tabela, que
é a condição sem a qual a medição não vale.

Saída **literal**:

| Consulta | Plano |
|---|---|
| **E6b `SumByCategoryAndAccount`** (`GROUP BY category_id, account_id`) | `SEARCH transactions USING INDEX ix_transactions_competence (household_id=? AND competence_month=?)` + `USE TEMP B-TREE FOR GROUP BY` |
| **E6a `SumByCategory`** (`GROUP BY category_id`), reconstruída | **o mesmo plano**, linha por linha |
| **E4a `SumMonthByKindAndAccount`**, 4 colunas | **o mesmo plano**, linha por linha |
| **E4a `SumMonthByKindAndAccount`**, 6 colunas (40 marcadas) | **o mesmo plano**, linha por linha |

Confirmado, portanto: a chave de agrupamento a mais (`account_id`) entra no
b-tree temporário que **já existia** para o `GROUP BY`, e a consulta nova entra
pelo mesmo índice e sai pelo mesmo plano da consulta **antiga** e da consulta
**irmã** do painel. A troca de `GROUP BY category_id` para
`GROUP BY category_id, account_id` **não muda o plano** — ela muda quantas
linhas saem (30 na amostra; ≤ 10 050 no teto).

O mesmo plano se mantém sob **25× o volume**, em forma balanceada — 20 casas ×
12 meses × **1.000** linhas = **240.000 lançamentos**, fatia de 1.000 linhas,
saída de 160 linhas, **7,07 ms** (média de 20 execuções). Não há divergência a
reportar nessa faixa, e por isso não há índice novo.

### ⚠️ A divergência existe, e é de FORMA, não de volume — medida

Empurrando a casa medida para o **pior caso declarado do projeto**
(`MaxAutoCategorizeRows` = 10.000 linhas num mês), o planejador do SQLite
**troca de índice**:

```
SEARCH transactions USING INDEX ix_transactions_category (household_id=?)
USE TEMP B-TREE FOR GROUP BY
```

Ele abandona `ix_transactions_competence` e entra por
`ix_transactions_category`, prendendo **só** `household_id`. Isso **não** é um
índice faltando, e a razão está medida:

- naquele cenário a casa tem **10.480** linhas e o mês tem **10.040** — o mês é
  **96 %** da casa. O plano escolhido toca 10.480 linhas; o plano esperado
  tocaria 10.040. **Diferença: 440 linhas.** O planejador troca exatamente
  quando os dois índices passam a ser equivalentes, e o erro é auto-limitado
  por construção;
- o `sqlite_stat1` guarda **médias por índice**, não histograma por valor: um
  único balde gigante no meio de baldes de 40 estraga a média do índice
  inteiro. É artefato da **forma** do dado, e a prova é que o plano **volta ao
  esperado** com 240.000 linhas distribuídas por igual (seção acima) — 25×
  mais volume e **nenhuma** divergência;
- é decisão do planejador do **SQLite**, o banco de **desenvolvimento**. Não
  descreve o PostgreSQL de produção, que tem estatísticas por coluna.

Custo real, em milissegundos (média de **40 execuções**, sem `-race`):

| Cenário | Plano | Tempo |
|---|---|---|
| realista (fatia de 480, saída de 30 linhas) | `ix_transactions_competence` + temp b-tree | **0,51 ms** |
| realista, com o índice candidato | candidato, **sem** temp b-tree | 0,36 ms |
| pior caso (fatia de 10.040, saída de 2.000 linhas) | `ix_transactions_category` + temp b-tree | **37,7 ms** |
| pior caso, plano esperado **forçado** (`INDEXED BY`) | `ix_transactions_competence` + temp b-tree | 30,6 ms |
| pior caso, com o índice candidato | candidato, **sem** temp b-tree | 27,4 ms |

*(Numa primeira execução, com o banco recém-escrito, os mesmos três números do
pior caso foram 69,3 · 52,2 · 53,9 ms: a ordem entre eles não muda, a escala
depende da máquina. Tempo aqui é indicativo, nunca critério de correção.)*

### O índice candidato **não se paga** — e ele é DDL

O índice que ajudaria é
`(household_id, competence_month, kind, category_id, account_id)`: serve os
três predicados e, por ordenar as duas chaves, **elimina o b-tree temporário**
(o plano sai com **uma linha só**, sem `USE TEMP B-TREE FOR GROUP BY`). Ainda
assim ele está **recusado**:

- ganha **0,15 ms** no cenário realista e **3,2 ms** no pior caso contra o
  plano esperado (30,6 → 27,4 ms) — ~10 %, dentro do ruído da máquina.
  Eliminar o b-tree temporário **não** é onde o tempo está: o custo dominante é
  ler as 10.000 linhas da fatia e materializar as 2.000 linhas de saída em Go;
- seria o **quinto** índice composto da tabela mais escrita do sistema,
  mantido por **toda linha de toda importação** — e, com **cinco** colunas, o
  mais largo de todos;
- seria **DDL** numa entrega cujo contrato é zero DDL, e exigiria emenda ao
  ADR-032.

**Veredito: nenhum índice novo.** A expectativa do ADR-032(d) está
**confirmada** na distribuição realista e sob 25× o volume; a única divergência
encontrada é de forma, auto-limitada, e o índice que a resolveria não se paga.

### Portabilidade nos quatro dialetos — conferida por leitura

| Construção | PostgreSQL · MySQL · SQLite · SQL Server |
|---|---|
| `COUNT(*)` | ANSI, idêntico. No SQL Server devolve `int` (32 bits) — irrelevante contra o teto declarado de 10.000 linhas por mês; `COUNT_BIG` só seria preciso acima de 2³¹ |
| `COALESCE(SUM(amount_cents), 0)` | ANSI nos quatro. Dentro de um `GROUP BY` o `SUM` nunca é nulo (todo grupo tem ≥ 1 linha e `amount_cents` é `NOT NULL`) — o `COALESCE` é cinto e suspensório, o mesmo de todas as agregações do projeto |
| `GROUP BY` de **duas** colunas | ANSI. A projeção **não tem nenhuma coluna fora da chave ou de um agregado**, que é o que a torna legal sob `ONLY_FULL_GROUP_BY` (padrão no MySQL desde 5.7.5), no PostgreSQL e no SQL Server |
| agrupar por **coluna**, nunca por **alias** | `Group("category_id")` resolve a **coluna**; o alias tem o mesmo nome, então não há ambiguidade em dialeto nenhum. Agrupar por alias não funciona em MSSQL nem em PostgreSQL — a restrição de sempre (ADR-029j) |
| `category_id` **nulo** numa linha só | O SQL-92 agrupa nulos juntos (`is not distinct from`) e os quatro dialetos seguem — é o balde "Sem categoria". Verificado em SQLite por `TestSumByCategoryIsolaCasasEFiltraJanela` e pelo teste de volume; nos outros três entra na suíte testcontainers da E8 |
| `deleted_at IS NULL` | ANSI |
| **nenhuma função de data** | `competence_month` é `varchar(7)` `AAAA-MM` e chega pronto da aplicação — extrair mês tem quatro sintaxes (armadilha **P1**) |
| **sem `ORDER BY`** | tira do caminho a última diferença que sobraria entre dialetos: **collation**. A dobra por categoria e a partição crédito/débito são em Go |
| identificadores | tudo `snake_case` minúsculo e nenhuma palavra reservada nos quatro (`kind`, `total`, `cnt` são seguros); a citação fica por conta do dialector do GORM |

⚠️ **Ponto de TIPO, não de sintaxe — pré-existente e ainda NÃO verificado.**
`SUM(BIGINT)` **muda de tipo por dialeto**: devolve `numeric` no PostgreSQL,
`DECIMAL` no MySQL, `bigint` no SQL Server e `INTEGER` no SQLite. O Go lê tudo
em `int64` e são os drivers que convertem. **Isto não é novo da E6b** — vale
igual para `Summary`, `SumByAccountUntil`, `SumInvestmentsByMonth` e
`SumMonthByKindAndAccount`, todas já em produção —, mas é a construção desta
consulta com maior chance de ser a primeira a quebrar fora do SQLite, e por
isso fica escrita aqui em vez de descoberta na E8.

⚠️ **Conferência multi-dialeto: PENDENTE.** Nesta medição não havia
`TEST_POSTGRES_DSN` definido, o daemon do Docker não estava no ar e
`testcontainers-go` ainda **não está no `go.mod`** — a suíte do `gormstore`
rodou **só em SQLite**. É a pendência já conhecida do projeto (suíte dos quatro
bancos, E8): a portabilidade da tabela acima é conferida **por leitura**, não
executada.

### Volume, medido

`report_volume_test.go`, no molde do `dashboard_volume_test.go`: **10.000
lançamentos vivos** no mês, **200 categorias** (o teto da taxonomia), **10
contas** (3 cartões), mais ruído de outro mês, de outra natureza, de outra casa
e de excluídos →

```
sqlite: 10000 lançamentos vivos, 200 categorias, 10 contas → 1930 linhas, 1 consulta(s), 31,0 ms
```

Três execuções sem `-race`: **33,4 · 31,0 · 34,5 ms** (24,2 ms na medição
original do `dev-backend-go`, noutra carga de máquina). **Com `-race`: 1,48 s**
— o detector de corrida custa ~45× na materialização das 1.930 linhas em Go, e
é mais uma razão para tempo **nunca** ser assertado.

O que o teste **asserta** é a contagem de consultas (**1** — qualquer número
maior é um N+1 esperando o mês cheio) e o teto de linhas
(`(category.MaxPerHousehold + 1) × account.MaxPerHousehold`); o tempo é
**impresso**. O relatório completo pelo serviço custa **2** consultas em
`todas` (agregação + categorias) e **3** nos recortes (mais a lista de contas)
— nenhuma delas cresce com o volume.

### A prova de que o schema continua v4

Não há teste de migração **novo** nesta entrega, e é deliberado — o mesmo
argumento da E4a. `gormstore.Models()` está **inalterado** (`models.go` não
aparece no diff da entrega), e os dois testes que já existem leem `Models()` em
**tempo de execução**: um índice, uma coluna ou uma tabela nova apareceria lá
como `CREATE …` e os quebraria.

Executados com `-race`, partindo de banco vazio **e** de banco v4 povoado —
saída real:

```
--- PASS: TestMigrateSemBancoDevolveErro (0.00s)
--- PASS: TestMigrateSemModelosNaoFaltaNada (0.03s)
--- PASS: TestMigrateCriaTodasAsTabelasEsperadas (1.74s)
--- PASS: TestMigrateCriaOsIndicesDoSchemaV3 (1.84s)
--- PASS: TestMigrateLevaBancoV1PovoadoParaV2 (1.94s)
--- PASS: TestMigrateLevaBancoV2PovoadoParaV3 (3.03s)
--- PASS: TestMigrateEmBancoVazioEDepoisPovoado (3.35s)
--- PASS: TestMigrateMantemOSchemaV4ComOFiltroDeTipo (3.45s)
--- PASS: TestMigrateMantemOSchemaV4ComAsNaturezasDeInvestimento (3.46s)
--- PASS: TestMigrateLevaBancoV3PovoadoParaV4 (3.73s)
PASS
ok  	github.com/brunorblanck/homefinance/backend/internal/platform/storage	5.103s
```

Os dois testes que afirmam a lista de DDL **vazia** —
`TestMigrateMantemOSchemaV4ComAsNaturezasDeInvestimento` e
`TestMigrateMantemOSchemaV4ComOFiltroDeTipo` — passam. **Zero DDL, confirmado.**

---

## Schema v4 — sem mudança: semente de casa nova com subcategorias e palavras-chave (ADR-033)

A semente de 18/09/2026 não cria tabela, coluna, índice nem migração: ela só **escreve mais linhas**
nas duas tabelas que já existem desde o schema v4 (`categories` e `category_keywords`). O teste
"zero DDL" continua sendo a prova, e continua passando.

### Volume por casa NOVA

| Tabela | Linhas | Observação |
|---|---|---|
| `categories` | **56** | 15 grupos + 41 subcategorias (`category.DefaultCategoryCount()`) |
| `category_keywords` | **440** | todas em folha; grupo com filha ativa não recebe palavra (spec 0005 §12) |

Os dois números são **derivados da tabela em código** (`backend/internal/category/seed.go`), nunca
escritos à mão no teste — a lista muda e as asserções acompanham. O teto do domínio continua 200
categorias por casa (`category.MaxPerHousehold`), então a semente ocupa **28 %** dele e sobra folga
larga para a taxonomia da pessoa.

### Orçamento de comandos — constante, uma vez por casa

Dentro da transação de `household.EnsureDefault`:

| Comando | Vezes | Por quê |
|---|---|---|
| `CountAll` | 1 | o teto da casa é conferido uma vez, com contador local depois |
| `List` (ativas) | 1 | substitui os `NameTaken` por grupo — o conjunto de nomes é montado em memória |
| `KeywordOwners` | 3 | 440 normas fatiadas por `idChunkSize` = 200 |
| `Create` (categoria) | 56 | um `INSERT` por categoria |
| `ReplaceKeywords` | 41 × 2 | `DELETE` + `CreateInBatches` por folha |

**≈ 143 comandos**, constante e independente de qualquer entrada do cliente. Nenhuma palavra do
corpo ou da URL amplifica o custo: a semente não recebe nada do cliente.

### Tempo, medido

`TestSementeDeCasaNovaNoBanco` (SQLite em arquivo, `MaxOpenConns: 1`) mede a criação da casa inteira
— `households` + `memberships` + a semente — e falha acima de **500 ms**, porque esse tempo é gasto
no caminho da **verificação do e-mail**, com a pessoa olhando a tela de cadastro.

```
semente de casa nova em sqlite: 56 categorias, 440 palavras,  19.1598ms (race=false)
semente de casa nova em sqlite: 56 categorias, 440 palavras,  23.1827ms (race=false)
semente de casa nova em sqlite: 56 categorias, 440 palavras,  24.8836ms (race=false)
semente de casa nova em sqlite: 56 categorias, 440 palavras,  18.7246ms (race=false)
semente de casa nova em sqlite: 56 categorias, 440 palavras, 253.3700ms (race=true)
```

Sob `-race` o orçamento é relaxado para **10 s** — a instrumentação multiplica o tempo de parede por
uma ordem de grandeza e o critério é sobre o binário **real**, mesmo tratamento do
`TestDesempenhoCriterio11` em `internal/textmatch` (`race_on_test.go` / `race_off_test.go`). O tempo
medido vai para o log nos dois casos, que é o que se reporta.

Como a medição **passou com folga**, a otimização prevista como plano B — um método
`InsertKeywords(ctx, hid, kws)` que gravaria a casa inteira em ~20 comandos no lugar dos 82 —
**não foi implementada**. Ela continua sendo a única otimização autorizada se o número voltar a
subir (por exemplo num dialeto remoto, onde 143 idas e voltas a 5 ms dariam ≈ 0,7 s).

### O índice único é a prova de que a lista não colide consigo mesma

`ux_category_keywords_household_norm (household_id, keyword_norm)` é **por casa**, não por
categoria. As 440 palavras entrarem sem violação é, por si só, a afirmação de que nenhuma norma se
repete entre as 41 folhas — e é por isso que o teste de integração conta as linhas gravadas em vez
de confiar só no teste de tabela em memória.

---

## Schema v4 — sem mudança: agregação por descrição do menu IA (spec 0010, E9a)

**Nenhuma tabela nova, nenhuma coluna nova, nenhum índice novo, nenhuma migração.** A fatia E9a é
**leitura pura**: uma agregação `GROUP BY description_norm, kind, account_id, category_id` sobre
`transactions`, com `household_id` vindo do token. É a sexta entrega seguida em que o `AutoMigrate`
não tem o que fazer.

O código desta seção: `backend/internal/platform/storage/gormstore/ai_queries.go`,
`backend/internal/transaction/types.go` (a projeção `DescriptionGroup`) e
`backend/internal/platform/storage/gormstore/ai_queries_test.go`.

### A medição do corpus — antes do teto, nunca depois

A spec 0010 §3.1 proíbe cravar teto no escuro, e a lição de 18/09/2026 exige o custo do corte
**medido**, nunca estimado. Medido em **21/09/2026**, em SQLite, sobre a janela máxima de **3 meses
de competência**.

**(a) Banco de desenvolvimento REAL** (`backend/homefinance.db`, 1 casa, 115 lançamentos vivos, só
**2** meses com dado — a janela de 3 tem um mês vazio):

| Medida | Valor |
|---|---|
| **M1** — `COUNT(DISTINCT description_norm)` | **78** |
| **M2** — linhas da agregação de 4 colunas | **83** |
| **razão de expansão** (M2 / M1) | **1,064** |
| descrições que aparecem uma só vez | 57 (73,1 % de M1) |
| **M3** — comprimento médio da descrição | **22,96 B** (máx. 59 B) |
| **M3** — overhead fixo da linha Markdown | **71,75 B** |
| **M3** — tabela inteira do prompt | **7.861 B = 7,68 KB** (~1.965 tokens a 4 B/token) |

**O banco de desenvolvimento é pequeno**, e o número precisa ser lido com isso na frente: 115
lançamentos vêm das amostras de `Exemplos/` (Inter, C6, Nubank — 13 a 14 linhas cada) mais lançamentos
manuais. Ele é **real**, e é a única fonte não-sintética que existe hoje, mas não responde sozinho
como a consulta se comporta numa casa de verdade depois de um ano.

**(b) Corpus SINTÉTICO — declaradamente sintético.** Vocabulário construído a partir das **formas**
reais dos extratos de `Exemplos/`: 55 % recorrentes de estabelecimento (`Ifd*Ifood Club`,
`Zaffari Bourbon Wallig`), 15 % contas de consumo (`Pagamento de boleto efetuado - EDP ESPIRITO
SANTO`) e 30 % Pix com nome de contraparte — esta última é a forma que gera descrição **quase
única**, e é ela que domina o crescimento. Cada cenário levou uma casa vizinha do mesmo tamanho e 12
meses fora da janela, com `ANALYZE` aplicado, para o planejador não ver um banco de casa única.

| Cenário (lançamentos vivos na janela de 3 meses) | M1 | M2 | razão | descrição média | tabela do prompt |
|---|---|---|---|---|---|
| **240** (casa comum, 4 contas) | 110 | 129 | **1,17** | 93,1 B | 20,9 KB |
| **750** (casa pesada, 6 contas) | 264 | 336 | **1,27** | 98,2 B | 56,1 KB |
| **3.000** (10 contas, 200 categorias) | 925 | 1.247 | **1,35** | 99,5 B | 209,3 KB |
| **9.999** (o volume dos testes do projeto) | 3.006 | 3.974 | **1,32** | 102,7 B | 679,3 KB |

A razão de expansão depende inteiramente de **quanto a descrição prediz a conta e a categoria**. Com
a correlação do mundo real (o mesmo estabelecimento cai quase sempre na mesma conta e na mesma
categoria — 85 % nesta medição) ela fica em **1,17 a 1,35**, coerente com o **1,064** do banco real.
Sorteando conta e categoria de forma independente — um mundo que não existe, medido só como teto
pessimista — ela sobe para **2,11 a 2,85**, e o cenário de 9.999 lançamentos vai a **7.278** linhas.

**M3 é calculado, não chutado:** a linha do prompt é
`| descrição | ocorrências | total R$ | tipo | contas | categoria |`, e o programa montou a linha
**real** de cada grupo e somou os bytes UTF-8. O **overhead fixo** (tudo menos a descrição) ficou
entre **72,3 e 73,8 B por linha** nos seis cenários — praticamente constante, como esperado —, então
o tamanho da tabela é `N × (73 + comprimento médio da descrição)`.

### Custo do corte, por teto candidato — medido

O teto de linhas do prompt (`maxDescriptionsInPrompt`) é decisão de produto, **não** desta seção: o
número abaixo é o insumo, e quem crava é o usuário. Corte por ocorrências decrescente, como manda a
§3.1.

| Teto | banco REAL (83 linhas) | 750 lanç. (336 linhas) | 3.000 lanç. (1.247 linhas) | 9.999 lanç. (3.974 linhas) |
|---|---|---|---|---|
| **300** | 0 cortadas | 36 linhas (4,8 % dos lançamentos) · 49,4 KB | 947 linhas (31,6 %) · 27,8 KB | 3.674 linhas (36,7 %) · 27,5 KB |
| **500** | 0 cortadas | **0 cortadas** · 56,2 KB | 747 linhas (24,9 %) · 65,0 KB | 3.474 linhas (34,7 %) · 45,4 KB |
| **800** | 0 cortadas | 0 cortadas | 447 linhas (14,9 %) · 123,8 KB | 3.174 linhas (31,7 %) · 75,2 KB |
| **1.000** | 0 cortadas | 0 cortadas | 247 linhas (8,2 %) · 164,1 KB | 2.974 linhas (29,7 %) · 101,9 KB |
| **2.000** | 0 cortadas | 0 cortadas | **0 cortadas** · 210,0 KB | 1.974 linhas (19,7 %) · 298,2 KB |
| **5.000** | 0 cortadas | 0 cortadas | 0 cortadas | **0 cortadas** · 681,9 KB |

O número em **negrito** é o primeiro teto que não corta nada naquele cenário. A leitura direta: um
teto de **500 linhas cobre inteira** a casa pesada realista (750 lançamentos na janela) e custa
**~56 KB** de tabela; a partir de 3.000 lançamentos na janela, qualquer teto abaixo de 2.000 corta —
e corta **descrições de ocorrência única**, que são 97 % das distintas naquele volume. Cortar cauda
de ocorrência 1 é barato em ocorrências e caro em variedade: no cenário de 3.000, o teto de 500
descarta 59,9 % das LINHAS mas só 24,9 % dos LANÇAMENTOS.

### `MaxDescriptionGroupRows = 5.000`: o rail do repositório, que é outra coisa

São **dois** tetos, e confundi-los seria erro:

- **`maxDescriptionsInPrompt`** (a decidir, no serviço) — quantas linhas o texto do prompt carrega.
  Corta a cauda e **declara** quantas ficaram de fora (§3.1).
- **`transaction.MaxDescriptionGroupRows = 5.000`** (aqui, no repositório) — o **rail de memória**,
  e ele é **tudo ou nada**: acima dele nada volta, e o serviço responde **422**. Nunca resposta
  parcial.

5.000 fica ~7× acima da casa pesada realista (712 linhas no pior caso descorrelacionado), ~60× acima
do banco real (83) e prende o pior caso de heap por requisição em **~2 MB** (5.001 linhas × ~400 B com
descrição no comprimento máximo de 140). O único cenário medido que o estoura é o extremo
**descorrelacionado** de 9.999 lançamentos (7.278 linhas) — que é 3.333 lançamentos por mês numa casa
doméstica, e para o qual "reduza a janela para 1 mês" é uma resposta honesta.

**Por que tudo ou nada, e não as primeiras N:** resposta parcial é o único resultado de verdade ruim
aqui. Um prompt que **parece** completo e não é faria a IA propor palavra-chave para metade da casa
sem ninguém saber — e o produto inteiro desta entrega é a pessoa confiar no texto que copia.

### Plano da consulta — `EXPLAIN QUERY PLAN` com os parâmetros LIGADOS

```sql
SELECT description_norm, MIN(description) AS sample, kind, account_id, category_id,
       COUNT(*) AS cnt, COALESCE(SUM(amount_cents), 0) AS total
  FROM transactions
 WHERE household_id = ? AND deleted_at IS NULL AND competence_month IN (?, ?, ?)
 GROUP BY description_norm, kind, account_id, category_id
 ORDER BY cnt DESC
 LIMIT ?
```

| Banco medido | Plano |
|---|---|
| desenvolvimento real (115 linhas) | `SEARCH transactions USING INDEX ix_transactions_competence (household_id=? AND competence_month=?)` + `USE TEMP B-TREE FOR GROUP BY` |
| sintético 4.500 linhas na tabela | **o mesmo plano** |
| sintético 18.000 linhas na tabela | **o mesmo plano** |
| sintético 59.994 linhas na tabela | **o mesmo plano** |
| forma final, **com** `ORDER BY cnt DESC` | o mesmo plano **+** `USE TEMP B-TREE FOR ORDER BY` |

Entra pelo **mesmo índice de sempre**, `ix_transactions_competence (household_id, competence_month)`,
e o `IN` de 3 meses vira três buscas de igualdade nele. O `GROUP BY` de quatro colunas cai no b-tree
temporário que o `Summary` e o painel já usam — **não há divergência a reportar**.

⚠️ **A armadilha do método pegou de novo, e por isso está medida aqui também.** Rodar o mesmo
`EXPLAIN QUERY PLAN` com os placeholders **vazios** muda o custo estimado que o planejador imprime
(`8|0|118` com valores reais contra `9|0|41` com placeholder vazio, no cenário de 4.500 linhas). O
índice escolhido coincidiu nos seis cenários, mas o número que o planejador reporta **não** é o
mesmo — e é dele que sai a decisão quando dois índices empatam. Vale o aviso já registrado para o
painel: **medir com placeholder vazio não é medir**.

### Veredito sobre o índice candidato: **RECUSADO** — e a recusa foi medida, não argumentada

O candidato era
`(household_id, competence_month, description_norm, kind, account_id, category_id)`. Criado de fato
no corpus sintético, com `ANALYZE` depois:

| Cenário | arquivo do banco | consulta sem o índice | com o índice | plano com o índice |
|---|---|---|---|---|
| 4.500 linhas | 3,11 → 3,62 MB (**+16,5 %**) | 2,51 ms | 2,51 ms | **inalterado** |
| 59.994 linhas | 40,75 → 47,51 MB (**+16,6 %**) | 50,96 ms | 37,27 ms | **inalterado** |

**O SQLite não escolhe o índice candidato.** O plano é *literalmente o mesmo* com e sem ele:
`ix_transactions_competence` + `USE TEMP B-TREE FOR GROUP BY`. E o motivo é estrutural, não de
estatística: a consulta precisa de `description`, de `amount_cents` e de `deleted_at`, que **não
estão** no índice — ele não cobre nada, então continua havendo ida à tabela por linha, e o planejador
prefere o índice estreito para a busca. Um índice largo que não cobre a projeção não vira
*index-only scan*, e sem isso ele não elimina o b-tree temporário, que é onde o tempo está.

Some-se a isso o que o `arquiteto` já antecipava, e que a medição confirma em vez de contradizer:

1. seria o **5º índice composto** da `transactions`, a tabela mais escrita do sistema — toda
   importação paga a manutenção dele em cada `INSERT`;
2. seria o **mais largo** deles, sobre uma coluna `varchar(140)`: **+16,5 %** no arquivo do banco,
   medido, e o custo é permanente;
3. o contrato desta entrega é **zero DDL**, e `AutoMigrate` **cria mas nunca remove** (ADR-008) —
   um índice que não serve para nada ficaria para sempre em todo banco que já subiu;
4. a diferença de tempo no cenário de 59.994 linhas (50,96 → 37,27 ms) está dentro da variação de
   máquina entre execuções do mesmo cenário (44,32 · 50,96 ms sem índice em execuções distintas), e
   **não** é reprodutível num plano que não mudou.

**Nenhum índice novo.** Índice não se cria sem medição, e esta medição diz não.

### Portabilidade nos quatro dialetos — e o achado que derrubou o plano original

O SQL gerado foi medido nos quatro dialetos com `DryRun` do GORM (não é leitura de documentação: é o
texto que o driver emitiria).

| Dialeto | Forma pedida no plano (`LIMIT` **sem** `ORDER BY`) | Veredito |
|---|---|---|
| SQLite | `… GROUP BY … LIMIT 5001` | ✅ |
| PostgreSQL | `… GROUP BY … LIMIT $5` | ✅ |
| MySQL | `… GROUP BY … LIMIT ?` | ✅ |
| **SQL Server** | `… GROUP BY … ` **`ORDER BY "id"`** ` OFFSET 0 ROW FETCH NEXT 5001 ROWS ONLY` | ❌ **quebra** |

**O achado:** T-SQL não tem `LIMIT`. O driver `gorm.io/driver/sqlserver` o traduz para
`OFFSET … FETCH NEXT`, que **exige** `ORDER BY`; quando não há um, o driver **inventa**
`ORDER BY <chave primária>` (`sqlserver@v1.6.4/sqlserver.go:77-85`). Aqui a chave primária é
`transactions.id`, que **não está no `GROUP BY` nem dentro de agregado** — erro 8127 do SQL Server.
Ou seja: `LIMIT` sobre `GROUP BY` **sem** `ORDER BY` não é portátil neste projeto, e o defeito só
apareceria no dialeto que hoje ninguém roda localmente.

**Correção adotada, e ela honra o motivo original do "sem ORDER BY".** A razão de não querer `ORDER
BY` era tirar a **collation** do caminho — e ordenar por `cnt`, que é `COUNT(*)` e portanto
**inteiro**, não tem collation nenhuma. Com `ORDER BY cnt DESC`:

| Dialeto | Forma final (`ORDER BY cnt DESC` + `LIMIT`) | Veredito |
|---|---|---|
| SQLite | `… GROUP BY … ORDER BY cnt DESC LIMIT 5001` | ✅ |
| PostgreSQL | `… GROUP BY … ORDER BY cnt DESC LIMIT $5` | ✅ |
| MySQL | `… GROUP BY … ORDER BY cnt DESC LIMIT ?` | ✅ |
| SQL Server | `… GROUP BY … ORDER BY cnt DESC OFFSET 0 ROW FETCH NEXT 5001 ROWS ONLY` | ✅ |

Ordenar por **alias da lista de seleção** é válido nos quatro (ao contrário de agrupar por alias, que
quebra em MSSQL e PostgreSQL — restrição já registrada no painel). O preço foi medido: o segundo
b-tree temporário custa **3,00 → 4,14 ms** no cenário de 4.500 linhas e **37,30 → 46,63 ms** no de
59.994. A ordenação **de verdade**, com desempate estável, continua sendo do serviço em Go: `ORDER BY
cnt DESC` sozinho não desempata, e desempate estável é o que faz o mesmo banco gerar o mesmo prompt
duas vezes.

E o `LIMIT` **não** economiza trabalho do banco — o plano é `USE TEMP B-TREE FOR GROUP BY`, e um
b-tree temporário tem de ser concluído antes da primeira linha sair. O que ele prende é o que
atravessa o driver e vira heap em Go, que é exatamente o que o rail precisa prender.

**Isto não contradiz `report_queries.go:44-47`**, que recusa `LIMIT` em `GROUP BY` sem `ORDER BY` por
não-determinismo. Ali a saída é limitada pela **estrutura** (categorias × contas) e o volume não pode
estourá-la, então `LIMIT` seria só risco. Aqui a saída é limitada pelo **volume** — o número de
descrições distintas cresce com a vida financeira da casa e não tem teto de domínio nenhum —, o
`ORDER BY cnt DESC` torna o corte determinístico **por posto**, e a semântica tudo-ou-nada garante
que o corte **nunca é entregue**.

### `IN (?, ?, ?)`, e não `BETWEEN`

`competence_month` é `varchar(7)`, não data. Uma **faixa** sobre coluna de texto
(`BETWEEN '2026-07' AND '2026-09'`) delega a decisão de "o que está entre" à **collation**, e as
quatro padrão não são a mesma coisa (a do MySQL é insensível a caixa; a do MSSQL depende da
instalação). Com a janela travada em **3 meses** (spec 0010 §2.1 e emenda §10 achado A2 — decisão do
usuário de 21/09/2026), a faixa cabe inteira em **igualdades**, e igualdade sobre `"YYYY-MM"` se
comporta igual em qualquer collation. É a mesma disciplina que o resto do projeto já segue: o mês
chega **pronto** da aplicação e nenhuma função de data entra na consulta (armadilha P1 — extrair mês
tem quatro sintaxes).

### Orçamento de parâmetros — constante, e minúsculo

| Componente | Parâmetros |
|---|---|
| `household_id = ?` | 1 |
| `competence_month IN (?, ?, ?)` | **≤ 3** (o teto da janela) |
| `LIMIT ?` | 1 (nos dialetos que o parametrizam) |
| **Total, pior caso** | **5** |

Contra o piso histórico de **999** do SQLite (o teto de portabilidade que o projeto adota) e os
**2100** do SQL Server. **Nenhum id de conta e nenhum id de categoria entra no SQL** — nem por `IN`
nem por `NOT IN` —, então nada aqui cresce com a taxonomia, com o número de contas ou com o de
lançamentos. O número é **asserção do teste de volume** (`ai_queries_test.go`), não comentário: o
espião de SQL confere `Parametros ≤ 5` em todo comando emitido.

### `MIN(description)` é AMOSTRA, não contrato

A projeção devolve `MIN(description)` só para o prompt ficar legível — qual das grafias o `MIN`
escolhe depende da **collation** do banco, e o mesmo dado pode devolver `Zaffari` num dialeto e
`ZAFFARI` noutro. **As duas respostas estão certas.** Está declarado na doc de
`transaction.DescriptionGroup.SampleDescription`, e **nenhum teste deste projeto asserta qual grafia
veio** — o teste afirma que a amostra é *uma das* grafias reais do grupo, nunca *qual*. Assertar a
grafia seria plantar um teste que passa em SQLite e quebra no primeiro MySQL. O que é estável, e o
que a lógica usa, é `description_norm`.

### A pendência da §7.1 da spec: **confirmada** — `NameTaken` NÃO muda

A spec 0010 §7.1 perguntava se o repositório precisava aprender a distinguir "nome livre" de "nome de
uma arquivada", e a §10.4 respondeu que não. **A análise da §10.4 se confirma contra o código**, e a
pendência está fechada:

1. **Mexer no `NameTaken` quebraria `Unarchive`.** `category.Service.Unarchive`
   (`category/service.go:577`) pergunta `NameTaken(casa, pai, nome, exceptID=próprio id)` sobre uma
   categoria que **está arquivada**, e a resposta que ele precisa é "há uma **ativa** ocupando este
   nome?". Se `NameTaken` passasse a contar as arquivadas, **qualquer outra irmã arquivada de mesmo
   nome** bloquearia o desarquivamento para sempre — um falso bloqueio, sem nada vivo ocupando o
   nome. `account.Service` tem a mesma chamada, no mesmo lugar (`account/service.go:478`): o estrago
   seria em dois domínios, não em um. A implementação atual
   (`gormstore/category_repository.go:149-168`) filtra `archived_at IS NULL` explicitamente, e é
   dessa semântica que os dois `Unarchive` dependem.
2. **O import lê `List(householdID, includeArchived=true)` e monta um índice em memória**, exatamente
   como `category/seed.go:443-466` já faz desde 18/09/2026 (achado A4 daquela revisão). São **no
   máximo 200 linhas** (`category.MaxPerHousehold`), lidas **uma vez, dentro da transação**, contra
   um `NameTaken` por entrada do JSON — que com 200 entradas seriam 200 idas ao banco. O índice
   `(pai, nomeNorm) → {id, kind, arquivada, temFilhaAtiva}` responde de uma vez as §§4.3(2), (4),
   (5), (6) e a §4.2(5b), que hoje precisariam de quatro consultas diferentes.
3. **Não há índice único de nome no banco para servir de rede.** `categories` tem
   `ix_categories_household_norm (household_id, name_norm)` — **não único** — e nenhum
   `uniqueIndex` sobre `(household_id, parent_id, name_norm)`. A unicidade de nome é **só** da
   aplicação, o que torna o índice em memória do item 2 não uma otimização, e sim **o** mecanismo.
   (Criar tal índice único seria DDL, e destrutivo em banco povoado que já tenha duplicata de nome
   com uma arquivada — fora do contrato desta entrega.)

### A prova de que o schema continua v4

`gormstore.Models()` está **inalterado** — `models.go` não aparece no diff da E9a —, e os dois testes
que já existem leem `Models()` em **tempo de execução**, então uma tabela, uma coluna ou um índice
novo apareceria lá como `CREATE …` e os quebraria. Executados com `-race`, partindo de banco vazio
**e** de banco v4 povoado — saída real de 21/09/2026:

```
--- PASS: TestOpenRecusaDriverDesconhecido (0.00s)
--- PASS: TestMigrateSemBancoDevolveErro (0.00s)
--- PASS: TestErroDeConexaoNaoVazaDSN (0.02s)
--- PASS: TestMigrateSemModelosNaoFaltaNada (0.16s)
--- PASS: TestMigrateCriaTodasAsTabelasEsperadas (7.22s)
--- PASS: TestMigrateCriaOsIndicesDoSchemaV3 (7.26s)
--- PASS: TestMigrateLevaBancoV1PovoadoParaV2 (8.16s)
--- PASS: TestMigrateEmBancoVazioEDepoisPovoado (10.28s)
--- PASS: TestMigrateMantemOSchemaV4ComOFiltroDeTipo (10.73s)
--- PASS: TestMigrateLevaBancoV2PovoadoParaV3 (10.92s)
--- PASS: TestMigrateMantemOSchemaV4ComAsNaturezasDeInvestimento (12.19s)
--- PASS: TestMigrateLevaBancoV3PovoadoParaV4 (13.55s)
PASS
ok  	github.com/brunorblanck/homefinance/backend/internal/platform/storage	15.107s
```

Os dois testes que afirmam a lista de DDL **vazia** passam. **Zero DDL, confirmado.**

### Volume, medido

`TestGroupByDescriptionEmVolumeEhUmaConsultaSo`: 3.000 lançamentos vivos na janela de 3 meses, 600
descrições distintas, 10 contas, 200 categorias (o teto da taxonomia), mais ruído de outra casa, de
outro mês e de excluído.

```
sqlite: 3000 lançamentos vivos, 600 descrições distintas → 750 linhas (razão de expansão 1.250), 1 consulta(s), 17.6929ms (race=false)
sqlite: 3000 lançamentos vivos, 600 descrições distintas → 750 linhas (razão de expansão 1.250), 1 consulta(s), 926.4144ms (race=true)
```

A razão de expansão de **1,250** é construída de propósito na faixa medida no corpus realista
(1,17–1,35): a conta e a categoria acompanham a descrição, com 1 em 4 descrições caindo também noutra
conta. **Sob `-race` o tempo é ~52×** — o detector instrumenta a materialização das 750 linhas em Go
—, e é mais uma razão para tempo **nunca** ser assertado. O que o teste asserta é a contagem de
consultas (**1**), o orçamento de parâmetros (**≤ 5**), o teto de linhas, a soma dos centavos grupo a
grupo e o isolamento entre duas casas povoadas com as **mesmas** descrições.
