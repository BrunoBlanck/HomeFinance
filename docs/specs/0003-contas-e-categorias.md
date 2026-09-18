# Spec 0003 — Contas e categorias (entrega E1)

**Data:** 12/09/2026 · **Fase:** 4 (núcleo financeiro) · **Entrega:** E1 do `PLANOS.md`
**ADRs relacionados:** ADR-013 (sem FK física, CSRF, cookies), ADR-015 (tipos TS do OpenAPI),
ADR-017 (saldo derivado, categoria com 2 níveis), ADR-019 (fuso na casa, `BRL` fixo)

> Esta spec é **normativa**. Onde ela e a documentação geral divergirem, vale a documentação geral
> (`AGENTS.md` e `docs/SEGURANCA.md`), e o desvio deve ser reportado, não implementado em silêncio.

---

## 1. Escopo

### Entra

Schema v2 parcial: `accounts`, `categories` e as colunas novas de `households` (`timezone`,
`currency`) · semente de categorias pt-BR na criação da casa · CRUD completo de conta e de categoria
com arquivar/desarquivar · saldo derivado da conta (nesta entrega, só o saldo de abertura — não há
lançamento ainda) · casca do app autenticado (navegação + seletor de mês na URL) · telas `/contas` e
`/categorias` · componentes `AppShell`, `MonthNavigator`, `DataTable`, `Dialog`, `Select`,
`EmptyState`, `Toast`, `Badge`.

### Fica explicitamente de fora

`transactions`, `recurring_bills`, `bill_occurrences`, `budgets` e qualquer relatório · transferência
· `MoneyInput` (entra na E2 junto com o primeiro campo de valor editável pelo usuário — o saldo de
abertura usa o mesmo componente, então ele **entra aqui**, ver §6) · convites e gestão de membros
(E7) · gráficos (E6) · exportação (E6).

### Emenda de 13/09/2026 — auditoria (divergência corrigida)

A primeira versão desta spec **omitiu a auditoria**, e a revisão de segurança da
entrega pegou: o §4.7 do `PLANOS.md` é explícito — *"toda escrita financeira gera
entrada em `audit_log`: quem, quando, qual ação, qual entidade, qual id, IP"* — e conta e
categoria são o domínio financeiro. A omissão foi corrigida ainda dentro da E1, e fica
registrada aqui em vez de sumir: a spec é normativa, e uma regra do plano que ela deixou de
lado é um erro da spec, não uma decisão.

O que passou a valer:

- **Toda escrita** de conta e de categoria (criar, editar, arquivar, desarquivar, excluir)
  grava uma entrada. Leitura não grava.
- A gravação acontece **dentro da transação** da escrita: se a auditoria não couber, a
  escrita não vale. É diferente dos eventos de autenticação, que usam `TryRecord` — lá uma
  auditoria indisponível não pode virar negação de login; aqui, dinheiro se movendo sem
  rastro é justamente o que a auditoria existe para impedir.
- A entrada guarda **ação, entidade, id, usuário, casa e IP**. Nunca valor (S8): não existe
  campo livre onde um centavo possa entrar, e há teste que impede alguém de somar um.
- A **semente de categorias não é auditada por ator**: ela roda dentro da criação da casa,
  que já tem a sua entrada (`household.created`). Doze linhas de `category.created` sem
  ninguém que as pediu esconderiam o que uma pessoa de fato fez.
- Os serviços passaram a receber um `Actor{HouseholdID, UserID, IP}` no lugar de um
  `householdID` solto — casa e usuário do **token**, IP da borda HTTP.

### Dependência não resolvida nesta entrega, e como fica

`DELETE /accounts/{id}` e `DELETE /categories/{id}` devem recusar com **422** quando o recurso está
em uso. Em E1 as tabelas que os usariam ainda não existem, com **uma exceção real e testável**:
categoria-grupo com filhas. O desenho é uma interface `UsageChecker` por domínio, com a
implementação de E1 respondendo "em uso" apenas para o caso de filhas; E2 e E3 registram os seus
verificadores no mesmo ponto. **O teste que prova o 422 de lançamento nasce na E2, não aqui** — e
isso está dito para não parecer esquecimento.

---

## 2. Modelo de dados

Vale o `PLANOS.md` §3.1, §3.2, §3.7 e §3.8. Aqui só o que é normativo para a implementação.

### 2.1 `accounts`

| Coluna | Tipo Go / SQL | Regra |
|---|---|---|
| `id` | `string` / `varchar(36)` | UUID v7 gerado **no service** (`internal/id`) |
| `household_id` | `varchar(36)` `not null` | **sempre do token**; índice em toda consulta |
| `name` | `varchar(80)` `not null` | 1..80 runas após `TrimSpace`; obrigatório |
| `name_norm` | `varchar(80)` `not null` | minúsculo sem acento; **gerado na aplicação** |
| `kind` | `varchar(20)` `not null` | `cash` · `checking` · `savings` · `credit_card` · `other` |
| `opening_balance_cents` | `int64` / `bigint` `not null` | pode ser negativo; faixa de §4.5 do plano |
| `opening_date` | `varchar(10)` `not null` | data civil `YYYY-MM-DD` — ver D3 |
| `archived_at` | `*time.Time` | arquivar ≠ excluir |
| `created_at` / `updated_at` | `time.Time` `not null` | UTC |
| `deleted_at` | `*time.Time` | soft delete |

**Unicidade do nome.** "Único por casa entre as não arquivadas" (plano §3.1) **não** vira índice
único no banco: exigiria índice parcial (`WHERE archived_at IS NULL AND deleted_at IS NULL`), que é
a armadilha **P4** — Postgres e SQLite têm, MySQL não tem, MSSQL tem com outra sintaxe. A regra é
verificada **no service, dentro da transação**, por consulta em `name_norm` filtrando
`household_id`, `archived_at IS NULL` e `deleted_at IS NULL`. Índice **não único**
`(household_id, name_norm)` sustenta a consulta.

Índices: `ix_accounts_household (household_id)` · `ix_accounts_household_norm (household_id, name_norm)`
· `ix_accounts_household_archived (household_id, archived_at)`.

### 2.2 `categories`

| Coluna | Tipo | Regra |
|---|---|---|
| `id` / `household_id` | | idem accounts |
| `parent_id` | `*string` / `varchar(36)` | nulo = grupo (nível 1); preenchido = folha (nível 2). **Nunca** um terceiro nível |
| `name` | `varchar(60)` `not null` | 1..60 runas após `TrimSpace` |
| `name_norm` | `varchar(60)` `not null` | idem accounts |
| `kind` | `varchar(10)` `not null` | `income` · `expense`; na folha é **cópia** do pai, nunca do cliente |
| `archived_at` / `created_at` / `updated_at` / `deleted_at` | | idem accounts |

**Unicidade do nome entre irmãos:** mesma decisão de `accounts` — verificação no service, não índice
único (o escopo inclui `parent_id`, que é **anulável**, e índice único sobre coluna anulável é a
armadilha **P3**: o MSSQL trata NULLs como iguais e só deixaria passar um grupo por casa).

Índices: `ix_categories_household (household_id)` ·
`ix_categories_household_parent (household_id, parent_id)` ·
`ix_categories_household_norm (household_id, name_norm)`.

### 2.3 `households` — colunas novas

`timezone varchar(64) not null default 'America/Sao_Paulo'` · `currency varchar(3) not null default 'BRL'`.
O `AutoMigrate` adiciona coluna com default sem destruir dado (ADR-008). O `timezone` é validado
contra `time.LoadLocation` na escrita; `currency` é `BRL` fixo no v1 e **não** é editável pela API.

---

## 3. Invariantes de domínio

1. **Toda** consulta e **toda** escrita filtram por `household_id` vindo do token. Recurso de outra
   casa responde **404**, nunca 403 (403 confirma existência) — `docs/SEGURANCA.md` §2 e S1 do plano.
2. `kind` de conta e de categoria vêm de conjunto fechado, validado por allowlist em código.
3. Categoria tem **exatamente 2 níveis**: `parent_id` só pode apontar para uma categoria da mesma
   casa **cujo `parent_id` seja nulo**. Apontar para uma folha é 422.
4. Folha **herda** o `kind` do pai. `kind` enviado pelo cliente na criação de folha é **ignorado**
   (não é erro — o contrato marca o campo como opcional e o servidor manda).
5. `kind` de um grupo **não muda** se ele tiver filhas ou estiver em uso.
6. Categoria **não vira** filha de outra depois de criada, e filha **não vira** grupo: `parent_id` é
   imutável no `PATCH`. Mudar a árvore com dado dependente é uma operação de outra ordem, e o v1 não
   a expõe.
7. `name_norm` e `kind` de folha são **sempre** recalculados no servidor; nunca aceitos do cliente
   (S2 do plano — *mass assignment*).
8. Arquivada: some de seletor de criação, continua em histórico e relatório. Desarquivar exige que o
   nome ainda esteja livre entre as ativas — senão 422.
9. Excluir é **lógico** e só é permitido quando o recurso nunca foi usado.
10. Limites da §4.5 do plano: ≤ 50 contas e ≤ 200 categorias por casa, contando as **não excluídas**
    (arquivada ocupa vaga — ela ainda existe). Estouro é 422 com motivo, nunca truncamento.
11. Saldo é **derivado** (ADR-017): nesta entrega `balanceCents == openingBalanceCents`. A soma dos
    lançamentos entra na E2 **no mesmo lugar**, sem mudar o contrato.

---

## 4. Contrato HTTP

Base `/api/v1`, todas as rotas sob `RequireAuth`. Convenções e envelope de erro: os já existentes.

| Método | Rota | Sucesso | Observações |
|---|---|---|---|
| GET | `/accounts` | 200 | `?includeArchived=true` (default `false`); devolve `balanceCents` e `totalBalanceCents` |
| POST | `/accounts` | 201 | corpo: `name`, `kind`, `openingBalanceCents`, `openingDate` |
| GET | `/accounts/{id}` | 200 | 404 se de outra casa |
| PATCH | `/accounts/{id}` | 200 | campos opcionais: `name`, `kind`, `openingBalanceCents`, `openingDate` |
| POST | `/accounts/{id}/archive` | 200 | idempotente: arquivar arquivada devolve 200 |
| POST | `/accounts/{id}/unarchive` | 200 | 422 se o nome colidir com uma ativa |
| DELETE | `/accounts/{id}` | 204 | 422 `RESOURCE_IN_USE` se em uso |
| GET | `/categories` | 200 | árvore de 2 níveis; `?kind=income\|expense`, `?includeArchived=true` |
| POST | `/categories` | 201 | corpo: `name`, `kind` (grupo) **ou** `name`, `parentId` (folha) |
| GET | `/categories/{id}` | 200 | |
| PATCH | `/categories/{id}` | 200 | `name` e, só em grupo sem filhas nem uso, `kind` |
| POST | `/categories/{id}/archive` | 200 | arquivar grupo arquiva as filhas junto, na mesma transação |
| POST | `/categories/{id}/unarchive` | 200 | desarquivar folha exige o pai ativo (422 se arquivado) |
| DELETE | `/categories/{id}` | 204 | 422 se tiver filhas ou estiver em uso |

**Código de erro novo:** `RESOURCE_IN_USE` (HTTP 422), com mensagem genérica
`"Este item está em uso e não pode ser excluído."`. É o único código novo desta entrega, e entra na
lista fechada de `httpserver`.

**`fields` em 422 de limite:** `{"name":"..."}` para colisão, `{"limit":"..."}` para teto de
quantidade. As mensagens dentro de `fields` são genéricas e não revelam dado de outra casa.

---

## 5. Semente de categorias (D5)

Casa nova nasce com 12 grupos pt-BR, criados **na mesma transação** que cria a casa
(`household.EnsureDefault`, que já roda na verificação do e-mail — ADR-012), atendendo S10 do plano.

Despesa: Moradia · Alimentação · Transporte · Saúde · Educação · Lazer · Serviços · Pessoal ·
Impostos · Outras despesas.
Receita: Salário · Outras receitas.

Todas **editáveis e arquiváveis** — não há categoria "de sistema". A semente é idempotente: rodar
duas vezes não duplica (a checagem é por `name_norm` dentro da casa).

---

## 6. Frontend

### 6.1 Casca (`AppShell`)

Cabeçalho com logo, **seletor de mês** e menu do usuário; navegação lateral colapsável no desktop e
barra inferior no mobile. O mês vive na URL como `?mes=YYYY-MM` e é **compartilhado entre telas**.

O mês default é o mês corrente **no fuso da casa** (`households.timezone`), nunca no do navegador —
é a mesma regra que a E3 usará para decidir "atrasado", e ela nasce aqui, num único lugar
(`lib/mesCorrente.ts`), com teste rodando o processo em UTC e em `America/Sao_Paulo`.

Rotas desta entrega: `/` (Painel, ainda o conteúdo atual), `/contas`, `/categorias`. As demais do
plano §8.1 entram com as suas entregas — **item de menu que não leva a lugar nenhum não é criado**.

### 6.2 Telas

- **`/contas`** — tabela densa com nome, tipo, saldo e estado; total no rodapé. Saldo negativo com
  **sinal explícito**, não só cor. Criar/editar em `<dialog>` nativo. Alternar "mostrar arquivadas".
- **`/categorias`** — árvore de 2 níveis agrupada por `kind`, criar/renomear/arquivar. Deixa claro
  que arquivar preserva histórico e que excluir só é possível quando nunca houve uso.

### 6.3 Componentes novos

`AppShell` · `MonthNavigator` · `DataTable` · `Dialog` (`<dialog>` nativo) · `Select` (nativo
primeiro) · `EmptyState` · `Toast` (`aria-live`) · `Badge` · `MoneyInput` e `MoneyText`
(antecipados da E2: o saldo de abertura é um campo de dinheiro editável, e não há como fazer esta
tela sem eles).

Todo componente nasce com teste e com foco/teclado resolvidos.

---

## 7. Decisões desta spec

**D1 — `name_norm` como coluna, não `LOWER()` na consulta.** `LOWER()` depende de collation e não
remove acento; "Alimentação" e "alimentacao" precisam colidir nos quatro dialetos. A normalização
(minúscula + remoção de diacríticos por `unicode/norm` NFD) é feita em Go e gravada. É a mesma
solução da armadilha **P2** do plano, antecipada porque a unicidade de nome já precisa dela.

**D2 — unicidade por consulta no service, não por índice único.** Justificada em §2.1 e §2.2 (P3 e
P4). **Consequência aceita e registrada:** sem índice único, duas requisições simultâneas de criação
com o mesmo nome podem passar as duas. O dano é um nome duplicado — cosmético, corrigível pelo
usuário, e sem efeito em dinheiro. Trocar isso por um índice que não é portátil nos quatro dialetos
seria pagar caro por um problema barato. Se um dia incomodar, a saída é índice único **total**
(incluindo arquivadas) mais renomeação automática no arquivamento — decisão para quando doer.

**D3 — data civil como `varchar(10)`, não `DATE`.** `opening_date` é data civil sem hora e sem fuso
(§4.2 do plano). Os quatro dialetos têm `DATE`, mas os drivers Go o devolvem como `time.Time` com
fuso implícito, e é exatamente aí que nasce o bug de "um dia antes" (**R6** do plano). Guardar o
texto `YYYY-MM-DD` torna a comparação e a ordenação lexicográficas — corretas para o formato ISO — e
elimina a conversão de fuso de todo o caminho. Um tipo `civil.Date` em Go encapsula validação,
comparação e serialização, e nenhuma camada manipula a string crua.

**D4 — `RESOURCE_IN_USE` como código próprio, e não `VALIDATION_FAILED`.** O frontend precisa
distinguir "corrija o formulário" de "não dá para excluir, mas dá para arquivar" — são duas
interfaces diferentes. Um código próprio evita o front ter que interpretar mensagem de texto.

---

## 8. Testes que esta entrega precisa provar

| Camada | Casos |
|---|---|
| Domínio (unidade) | normalização de nome (acento, caixa, espaço) · allowlist de `kind` · herança de `kind` na folha · recusa de 3º nível · limites 50/200 · faixa de valor · validação de `YYYY-MM-DD` incluindo bissexto e `2026-02-30` |
| Repositório (SQLite) | **isolamento**: conta/categoria de outra casa nunca aparece em listagem, busca por id, contagem nem verificação de nome duplicado |
| Handler (`httptest`) | 404 para id de outra casa em **todas** as rotas com `{id}` · 400 com `fields` · 422 `RESOURCE_IN_USE` · corpo com campo desconhecido recusado · `householdId` no corpo é ignorado (mass assignment) |
| Abuso | id inexistente e malformado · `kind` inválido · nome com 1000 caracteres · valor `9_999_999_999_999` · `openingDate` `"0000-00-00"` · `parentId` apontando para folha · `parentId` de outra casa · arquivar duas vezes |
| Frontend (unidade) | máscara de dinheiro · sinal de saldo negativo · árvore de 2 níveis · estados vazio/erro/carregando · mês na URL e mês corrente no fuso da casa (processo em UTC e em SP) |
| E2E | criar conta → aparece na lista com saldo → arquivar → some da lista → mostrar arquivadas → aparece · criar grupo e subcategoria · trocar de mês e o mês persistir entre telas |

---

## 9. Critérios de aceite

1. Criar, editar e arquivar conta e categoria pela UI, com o resultado visível sem recarregar.
2. Categoria com filhas não se exclui (422 `RESOURCE_IN_USE`), e a UI oferece arquivar.
3. **Recurso de outra casa responde 404 em todos os endpoints com `{id}`**, com teste que prova.
4. Repositório testado em SQLite com caso de isolamento por casa em toda consulta.
5. Casa nova nasce com as 12 categorias, na mesma transação da criação da casa.
6. `AutoMigrate` aplica o schema v2 partindo de banco **vazio** e de banco **já povoado** com o v1.
7. `backend/scripts/check.ps1` limpo; frontend com `tsc`, `biome`, `vitest` e `build` limpos.
8. `npm run api:check` confere: a spec e os tipos TS não divergem.
9. E2E verde para o fluxo de conta.
10. Revisão de segurança com veredito **APROVADO**.
11. Toda escrita de conta e de categoria gera entrada em `audit_log`, na mesma transação —
    e nenhuma entrada carrega valor monetário.
