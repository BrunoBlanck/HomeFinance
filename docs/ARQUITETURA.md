# Arquitetura — HomeFinance

Decisões fundamentadas na pesquisa de 09/2026 (`docs/PESQUISA.md`).

## Visão geral

```
┌─────────────────┐        HTTPS/JSON        ┌──────────────────────────────┐
│  Frontend React │ ───────────────────────▶ │  Backend Go (API REST)       │
│  (Vite 8, SPA)  │ ◀─────────────────────── │  domínios + platform         │
└─────────────────┘   contrato OpenAPI 3.1   └──────────────┬───────────────┘
                                                            │ GORM v2 (ADR-008)
                                             ┌──────────────▼───────────────┐
                                             │  Qualquer banco SQL          │
                                             │  Postgres│MySQL│SQLite│MSSQL │
                                             └──────────────────────────────┘
```

## Backend (Go 1.26)

### Layout por domínio (guia oficial go.dev/doc/modules/layout — ver ADR-004)

```
backend/
  cmd/api/main.go              ← composição: config → db → domínios → server
  api/openapi.yaml             ← contrato OpenAPI 3.1 (fonte de verdade — ADR-006)
  internal/
    household/  user/  account/  category/  transaction/  bill/  budget/
      │   Cada domínio contém: types.go (entidades e erros), service.go (regra
      │   de negócio), repository.go (INTERFACE), handler.go (HTTP), *_test.go
    auth/                      ← JWT access, refresh opaco com rotação, Argon2id,
      │                          verificação de e-mail e OTP de 6 dígitos (ADR-009)
    platform/
      config/                  ← env fail-fast (caarlos0/env); único os.Getenv
      httpserver/              ← router (ServeMux), middlewares: auth, rate limit
      │                          (x/time/rate), headers, recover, slog
      mailer/                  ← interface Mailer: console (dev) | SMTP (prod)
      storage/                 ← conexão GORM por DB_DRIVER + AutoMigrate no boot
        gormstore/             ← implementações dos repositórios (ADR-008)
```

**Regra de dependência:** os pacotes de domínio são o núcleo e não importam `platform`; `platform/storage/*` implementa as interfaces dos domínios; `cmd/api` liga tudo. Handler → service → repository (interface) — nenhuma camada pula a de baixo.

- Roteamento: `net/http.ServeMux` nativo (`mux.HandleFunc("GET /api/v1/bills/{id}", ...)`). Sem framework web.
- Injeção de dependência manual por construtores. Sem DI framework, sem globals.
- Erros embrulhados com contexto (`fmt.Errorf("saving bill: %w", err)`); erros de domínio são valores exportados que o handler traduz para HTTP.
- `context.Context` em toda a cadeia; queries sempre `...Context`.
- Validação de entrada com go-playground/validator v10 na borda.

## API — convenções

- Contrato **OpenAPI 3.1 spec-first** (`backend/api/openapi.yaml`): a spec é editada primeiro; oapi-codegen gera tipos/stubs Go e os tipos TypeScript do frontend derivam da mesma spec. Divergência entre código e spec é bug.
- Base: `/api/v1`. Recursos no plural, kebab-case: `/api/v1/recurring-bills`.
- JSON `camelCase`. Datas ISO 8601 UTC. Dinheiro em **centavos (inteiro)**: `{"amountCents": 12990}`.
- Paginação por cursor: `?limit=50&cursor=...` → `{"items": [...], "nextCursor": "..."}`.
- Erro padrão único:

```json
{ "error": { "code": "VALIDATION_FAILED", "message": "mensagem segura em pt-BR", "fields": {"amountCents": "deve ser positivo"} } }
```

- Códigos: 400 validação · 401 não autenticado · 403 sem permissão · **404 para recurso inexistente OU de outra casa** · 409 conflito · 422 regra de negócio · 429 rate limit · 500 genérico.

## Frontend (React 19.2 + Vite 8)

### Estrutura feature-based (bulletproof-react — ver ADR-007)

```
frontend/src/
  app/            ← TanStack Router (árvore de rotas), providers, config
  features/       ← auth/ dashboard/ transactions/ bills/ budgets/ reports/
      │   Cada feature: api/ (queryOptions + mutations), components/, hooks/,
      │   schemas/ (Zod 4), types/ — só as pastas necessárias
  components/     ← design system próprio (Button, Input, Table, Modal…)
    icons/        ← ícones SVG únicos do projeto
  styles/         ← tokens.css (OKLCH), reset, tipografia, @layer
  lib/  hooks/    ← utilidades compartilhadas (api client, formatação pt-BR)
```

- **Arquitetura unidirecional**: `lib/components` → `features` → `app`. **Imports entre features são proibidos** (lint) e **barrel files são proibidos** (tree-shaking).
- Rotas e **search params tipados** com TanStack Router; filtros vivem na URL (`/transacoes?mes=2026-09`) validados com Zod.
- Estado do servidor: TanStack Query v5 — `queryOptions` co-locados em `features/*/api/`, `useSuspenseQuery` + ErrorBoundary por rota, `useOptimistic`/`useMutation` para ações rápidas (ex.: marcar conta como paga).
- Formulários: react-hook-form + Zod 4 (schema compartilhado entre form, search params e contrato da API).
- Estilo: CSS Modules + tokens; CSS moderno baseline (nesting, `:has()`, container queries, `@layer`, `color-mix` em OKLCH). Transições com a View Transitions API do navegador (nunca a experimental do React), respeitando `prefers-reduced-motion`.
- Lint/format: **Biome** + typescript-eslint mínimo (regras type-aware) + react-hooks; TS `strict` + `noUncheckedIndexedAccess`.

## Domínio (modelo conceitual)

- **Household** (casa) — unidade de isolamento de dados. Todo dado pertence a uma casa.
- **User** — pertence a uma ou mais casas com papel (`owner`, `member`).
- **Account** — conta (carteira, banco, cartão) de onde sai/entra dinheiro.
- **Category** — categoria de receita/despesa, hierárquica (ex.: Casa → Energia).
- **Transaction** — lançamento (receita/despesa/transferência) em centavos.
- **RecurringBill** — conta recorrente (aluguel, luz) que gera transações previstas.
- **Budget** — orçamento por categoria/mês.

## Decisões de arquitetura (ADRs)

### ADR-001 — Roteador nativo em vez de framework web
- **Decisão:** `net/http` puro (ServeMux 1.22+); middlewares como `func(http.Handler) http.Handler`. chi só se a árvore de middlewares crescer (100% compatível).
- **Consequências:** zero dependências no caminho HTTP; menos superfície de ataque.

### ADR-002 — `database/sql` + sqlx + repositórios por interface — ❌ **SUPERADO pelo ADR-008 (09/09/2026)**
- **Contexto:** requisito de aceitar qualquer banco SQL, incluindo SQL Server — o que **eliminou sqlc** (sem suporte a MSSQL).
- **Decisão original:** sem ORM; `database/sql` com **sqlx**; migrações com goose.
- **Status:** **SUPERADO.** O usuário decidiu em 09/09/2026 adotar GORM em todo o projeto — ver **ADR-008**. sqlx e goose **não entram no código**. O que sobrevive desta decisão: repositórios continuam sendo interfaces nos domínios, com a implementação isolada em `platform/storage`.

### ADR-003 — Dinheiro como inteiro em centavos
- **Decisão:** `int64` centavos no backend e `BIGINT` no banco; `amountCents` no JSON; formatação só na borda do frontend. Float proibido.

### ADR-004 — Layout por domínio, não por camada técnica (09/2026)
- **Contexto:** golang-standards/project-layout não é oficial; o guia oficial e a prática atual favorecem pacotes por domínio (`internal/transaction`) em vez de camadas horizontais (`internal/handlers` + `internal/services`).
- **Decisão:** um pacote por domínio contendo handler, service, tipos e a interface do repositório; infraestrutura compartilhada em `internal/platform/`.
- **Consequências:** coesão alta (tudo de "transação" num lugar), features fáceis de achar; a disciplina de camadas passa a ser por arquivo/da interface, verificada em revisão.

### ADR-005 — Bibliotecas padrão de fato (09/2026)
- **Decisão:** `golang-jwt/jwt/v5` (⚠️ dgrijalva/jwt-go abandonada — proibida), go-playground/validator v10, `log/slog`, caarlos0/env, `golang.org/x/time/rate`. ~~goose v3~~ **removido pelo ADR-008** — a evolução de schema é o `AutoMigrate` do GORM.
- **Consequências:** todas mantidas ativamente, mínimo de dependências transitivas; migração aplicada no boot, sem CLI no deploy.

### ADR-006 — OpenAPI 3.1 spec-first com oapi-codegen (09/2026)
- **Contexto:** spec escrita depois do código (ou swaggo por anotações) é legado; contrato tipado ponta a ponta reduz erro humano — ganho direto de segurança de entrada.
- **Decisão:** `backend/api/openapi.yaml` é a fonte de verdade; oapi-codegen gera server stubs/tipos Go sobre stdlib; o frontend gera seus tipos TS da mesma spec.
- **Consequências:** frontend e backend nunca divergem silenciosamente; a spec é revisável no PR.

### ADR-007 — Frontend: TanStack Router + Query, RHF + Zod 4, Biome (09/2026)
- **Contexto:** para SPA client-heavy, TanStack Router entrega tipagem ponta a ponta (rotas + search params) que React Router v7 só tem em framework mode/SSR; Biome unifica lint+format com 10–25× a velocidade.
- **Decisão:** TanStack Router + Query v5; react-hook-form + Zod 4; Biome + typescript-eslint mínimo type-aware; Vitest 4 com Browser Mode para componentes.
- **Consequências:** filtros na URL tipados e validados; um schema Zod serve form, URL e contrato; toolchain rápida.

### ADR-008 — GORM como camada de persistência e AutoMigrate como evolução de schema (09/09/2026)
- **Contexto:** decisão direta do usuário, que reverte o ADR-002. O requisito multi-banco continua de pé: GORM tem drivers oficiais para os quatro dialetos, então nada se perde em portabilidade.
- **Decisão:** **GORM v2** (`gorm.io/gorm`) é a única camada de acesso a dados do projeto. Drivers: `gorm.io/driver/postgres`, `gorm.io/driver/mysql`, `gorm.io/driver/sqlserver` e **`github.com/glebarez/sqlite`** para SQLite (baseado em `modernc.org/sqlite`, **puro Go, sem CGo** — mantém o build reproduzível e cross-compilável). **SQLite é o banco de desenvolvimento.** O schema é declarado nas structs Go com tags `gorm:"..."` e evoluído por **`AutoMigrate` no boot**; goose sai do projeto.
- **Limite conhecido e aceito:** `AutoMigrate` cria tabelas, colunas, índices e constraints que faltam, mas **nunca remove nem renomeia** coluna, e não altera tipo de forma destrutiva. Qualquer mudança destrutiva futura exige migração manual explícita, registrada em ADR próprio.
- **Fronteiras que continuam valendo:** o repositório é uma **interface** declarada no pacote do domínio; GORM vive apenas na implementação em `platform/storage/gormstore`. **`*gorm.DB` nunca aparece em service ou handler.** BOLA continua sendo filtro por `household_id` em toda query, e dinheiro continua `int64` em centavos.
- **Consequências:** menos SQL à mão e schema evoluindo junto com o código; em troca, o SQL gerado passa a ser responsabilidade do ORM — a revisão de segurança precisa conferir que nenhuma query usa `Raw`/`Exec` com interpolação de entrada do usuário.

### ADR-009 — Verificação de e-mail obrigatória com código de 6 dígitos (09/09/2026)
- **Contexto:** decisão do usuário — nenhuma conta existe sem prova de posse do e-mail, e a recuperação de senha usa a mesma mecânica.
- **Decisão:** o registro cria o usuário como **não verificado**; o login só é liberado após a confirmação de um código numérico de 6 dígitos enviado por e-mail. O mesmo mecanismo serve "esqueci minha senha". O envio passa por uma **interface `Mailer`**: implementação de console/log em desenvolvimento (o código aparece no `slog`, nenhum e-mail sai da máquina) e SMTP em produção, escolhida por env — mesma estratégia do driver de banco.
- **Consequências:** um único fluxo de OTP atende cadastro e recuperação; regras normativas (geração com `crypto/rand`, guarda só do hash HMAC, uso único, expiração, limite de tentativas, resposta que não revela existência do e-mail) na seção 1.1 de `docs/SEGURANCA.md`.

### ADR-010 — Toolchain Go 1.26 resolvido pelo `GOTOOLCHAIN=auto` (09/09/2026)
- **Contexto:** a documentação declarava "Go 1.27", mas o `go` instalado na máquina é **go1.24.1**. As versões atuais de `golang.org/x/crypto`, `x/time`, `x/sys` e `x/text` declaram `go 1.26.0` nos seus `go.mod`, e desde o Go 1.21 essa diretiva é requisito duro.
- **Verificação feita (não é hipótese):** com `GOTOOLCHAIN=auto` (o padrão), o Go **baixa e usa o toolchain 1.26.0 automaticamente**. Confirmado neste ambiente: `go build ./...` e `CGO_ENABLED=0 go build ./...` passam com as versões mais recentes de todas as dependências, e `go version` dentro do módulo reporta `go1.26.0`.
- **Decisão:** `backend/go.mod` declara `go 1.26.0`. **Não** fixamos tetos de versão em `x/crypto`, `x/time`, `x/sys` ou `x/text` — a hipótese de que seria preciso rebaixá-los foi levantada no planejamento e **refutada empiricamente**. Fica registrada a única consequência operacional: uma máquina nova precisa de rede na primeira build para o Go buscar o toolchain, ou de um Go ≥1.26 já instalado.
- **Consequências:** dependências ficam em dia (menos superfície de CVE, que é o motivo de a regra das duas últimas majors existir) e o build é reproduzível. Se algum dia for necessário travar em toolchain local, basta `GOTOOLCHAIN=local` — e aí os pins voltam a ser discutidos num ADR próprio.

### ADR-011 — OpenAPI 3.1 versionado com handlers manuais nesta fundação (adiamento controlado do oapi-codegen) (09/09/2026)
- **Contexto:** o ADR-006 estabelece spec-first com oapi-codegen. Na fundação de autenticação, os handlers têm comportamento deliberadamente **não uniforme e crítico à segurança** — cookies com prefixo condicional, respostas propositalmente idênticas para não vazar existência de conta, normalização do tempo de resposta, limpeza de cookies no caminho de erro. É código para ser lido e revisado linha a linha.
- **Decisão:** o ADR-006 continua valendo quanto ao **contrato**: `backend/api/openapi.yaml` é escrito antes dos handlers e versionado como fonte de verdade. Fica **adiada apenas a geração de código**. Para o adiamento não virar dívida invisível, `cmd/api/routes_test.go` valida no `go test` que o conjunto de `(método, path)` da spec é exatamente o da tabela de rotas — divergência quebra o build.
- **Gatilho de reversão (explícito):** ao ultrapassar 25 endpoints, **ou** na primeira geração de tipos TypeScript do frontend a partir da spec, o wiring do oapi-codegen entra e este ADR é encerrado.
- **Consequências:** frontend e backend já compartilham o contrato; o custo é que a aderência de *schemas* (não só de rotas) fica por revisão humana até o gerador entrar.

### ADR-012 — Ciclo de vida da conta: casa criada na verificação e OTP ancorado no e-mail (09/09/2026) — ⚠️ **parcialmente revisto pelo ADR-014**
- **Contexto:** o ADR-009 exige verificação obrigatória por código de 6 dígitos e a mesma mecânica para recuperação de senha. Faltava definir quando a `household` nasce e o que o OTP referencia.
- **Decisão:** o registro cria **apenas** o usuário não verificado. A `household` (`"Casa de {primeiro nome}"`) e a `membership` de `owner` são criadas na **verificação do e-mail**, na mesma transação que grava `email_verified_at`. A criação é idempotente (`EnsureDefault`) e também roda no login como auto-reparo, garantindo o invariante **"todo usuário verificado tem ao menos uma casa"** — do qual depende a claim `hid` do access token. `verification_codes` é ancorado em **`email + purpose`**, com `user_id` anulável, e **nenhuma linha-isca** é criada para e-mail inexistente. ~~Registro sobre e-mail existente e **não verificado** sobrescreve nome e hash pendentes e reemite o código.~~ — **revisto pelo ADR-014 (09/09/2026):** sobrescrever credenciais pendentes é justamente o que abre o *pre-hijacking*; hoje cada cadastro abre uma **tentativa própria** e a ativação usa as credenciais da tentativa dona do código.
- **Consequências:** spam de registro não gera casas órfãs; a validação de código tem a **mesma forma de consulta** exista ou não a conta, o que sustenta as respostas idênticas exigidas pela §1.1 de `docs/SEGURANCA.md`; em troca, o fluxo de verificação passa a ser transacional e exige `UnitOfWork`.

### ADR-013 — Sessão em cookie `__Host-`, CSRF por `Origin` e ausência de FK física (09/09/2026)
- **Contexto:** três escolhas estruturais da camada de borda e de persistência que valem para todo o resto do projeto.
- **Decisão:** **(a)** access e refresh viajam em cookies `HttpOnly; Secure; SameSite=Strict; Path=/`, com prefixo `__Host-` sempre que `COOKIE_SECURE=true`, protegendo contra sobrescrita a partir de subdomínio comprometido (*session fixation*); **(b)** a defesa de CSRF é `SameSite=Strict` **mais** checagem de `Origin`/`Sec-Fetch-Site` contra a allowlist do CORS em todo método não seguro — sem token double-submit e sem dependência nova, já que o backend não serve formulário HTML; **(c)** o schema **não tem chave estrangeira física** (`DisableForeignKeyConstraintWhenMigrating: true`): a integridade é garantida em Go, dentro de transação, com índice em toda coluna de referência.
- **Por que (c), em detalhe:** o `AutoMigrate` só gera FK a partir de campos de associação, e associação em struct traz o *auto-save* do GORM (risco de upsert acidental do pai); `ON DELETE RESTRICT` não é T-SQL válido e quebraria o `AutoMigrate` no SQL Server; e o MSSQL rejeita múltiplos caminhos de cascade, o que estouraria já na Fase 4 (`transactions` → `households` e → `accounts` → `households`).
- **Consequências:** o `AutoMigrate` fica portátil nos quatro dialetos sem exceções. O preço é que linha órfã por bug de código **não é barrada pelo banco**: toda escrita multi-tabela **tem** que passar por `UnitOfWork`, e isso vira item permanente de revisão de segurança.

### ADR-014 — Tentativa de cadastro com `registrationToken`: o código pertence a quem o pediu (09/09/2026)
- **Contexto:** o modelo do ADR-012 guardava o cadastro pendente como **uma linha em `users`**, e o código de 6 dígitos ficava ancorado em `email + purpose`. Isso deixa o código que chega à caixa de entrada **desligado de quem escolheu a senha**, e abre *pre-hijacking* de conta (Sudhodanan & Paverd, USENIX Security 2022). Nenhuma política de sobrescrita fecha o buraco: com *último escreve vence*, o atacante cadastra por último e a vítima ativa a senha dele ao usar o próprio código; com *primeiro escreve vence*, o cadastro da vítima vira no-op e dá no mesmo; bloquear e-mail com cadastro pendente vira **negação permanente de cadastro** por procuração.
- **Decisão:** cada `POST /auth/register` abre a sua própria **tentativa de cadastro** (tabela `registration_attempts`), que carrega as credenciais daquele pedido, o HMAC do seu código e o SHA-256 de um **`registrationToken`** opaco de 256 bits (`crypto/rand`, guardado só como hash, devolvido no corpo do 202). `POST /auth/verify-email` passa a exigir o token junto do código, e a busca é **escopada pelo token**: código de outra tentativa não existe para a validação. A confirmação grava as credenciais **da tentativa dona do código** (`user.ActivatePending`, numa única linha de UPDATE com `email_verified_at IS NULL`) e **consome todas as outras tentativas** do endereço. A linha de `users` continua nascendo no registro, mas só para **reservar o endereço** e sustentar o 403 `EMAIL_NOT_VERIFIED` do login — ela nunca ativa conta sozinha.
- **Por que fecha o ataque:** o navegador da vítima só tem **o token dela**. O código do atacante até chega à caixa da vítima, mas é inútil na mão dela; e o atacante tem o token dele, mas nunca vê o código, que foi para a caixa alheia. Nenhum dos dois fecha o par. E, como nenhuma tentativa destrói a alheia, a defesa **não** custa negação de cadastro.
- **Recuperação:** perder o token nunca trava ninguém — e a recuperação é **refazer o cadastro**, não o reenvio. `POST /auth/register` de novo sempre funciona, nunca é bloqueado por terceiro e sempre devolve um token amarrado às credenciais informadas. `POST /auth/resend-code` **exige** o token (rotaciona o código daquela tentativa, preservando-o); **sem** o token a resposta é o mesmo 202 do grupo B, mas **nada é emitido**.
- **Revisão de 09/09/2026 (achado ALTA-1):** ~~sem o token, o reenvio agia quando o endereço tinha exatamente uma tentativa viva~~ — esse caminho foi **removido**. A inferência "existe uma única tentativa viva, logo ela é de quem está pedindo" é falsa: o pedido carrega só um e-mail, e o atacante fabrica a condição sozinho, bastando que a tentativa da vítima tenha expirado e a dele esteja viva. A sucessora emitida herdava as credenciais **dele** e o token dela ia para a **vítima**, que ativava a conta com a senha do atacante ao usar o código da própria caixa. Não há conserto dentro do caminho: quem pede reenvio **não fornece credenciais**, então qualquer código emitido para ele herda as de outra pessoa.
- **Revisão de 09/09/2026 (achado ALTA-2) — só vale o código que foi EMITIDO:** a tentativa é gravada com código mesmo quando o cooldown ou a cota seguram a mensagem (é o que preserva o token de quem pediu), e `code_issued_at` fica **NULO** nesse estado. Como `register` não tem teto por endereço — de propósito, senão vira *lockout* por procuração — cada cadastro criava um código novo e **chutável sem gastar mensagem**, e a cota de mensagens/h por endereço (10/h desde 09/09/2026) deixava de limitar quantos códigos existem para adivinhar (5 palpites × 5 cadastros/h por IP, escalando com o número de IPs, tendo como prêmio uma conta **verificada** num endereço que o atacante nunca leu). Hoje `code_issued_at IS NOT NULL` entra no `WHERE` de `LiveByToken` **e** é reconferido no serviço: código que nunca saiu por e-mail não existe para a validação. A sentinela é **NULL**, e não o zero de `time.Time`, porque o zero não é portátil — o MySQL o recusa com erro 1292 (`NO_ZERO_DATE`), o que fazia a tentativa não nascer naquele dialeto. Quem ficou sem mensagem recupera pelo reenvio **com** o token, que gasta a cota e volta a ser contabilizado por ela.
- **Revisão de 09/09/2026 (achado BAIXA-3):** a reemissão disparada pelo login não verificado passa a exigir que a senha recém-provada seja a **da tentativa** a ser rotacionada. A linha de `users` guarda o hash de quem cadastrou primeiro, que pode ser um terceiro; sem essa amarração ele rotacionava a tentativa da vítima, matando o código que ela tinha na mão e gastando a cota dela.
- **Consequências:** o contrato de `register`, `resend-code` e `verify-email` muda (campo `registrationToken`), e o frontend precisa guardar o token entre as duas telas do cadastro. `verification_codes` fica sendo usado só por `password_reset`. A regra da §1.1 "código novo invalida os anteriores" passa a valer **por tentativa** — o que preserva a defesa contra força bruta, porque sem o token nenhum código é sequer procurado. O expurgo do janitor ganha as tentativas, com uma carência de um TTL depois da expiração para que quem queimou as 5 tentativas ainda consiga pedir outro código.

*(novos ADRs entram aqui, numerados, pelo agente `arquiteto`)*
