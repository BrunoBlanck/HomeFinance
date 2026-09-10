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
