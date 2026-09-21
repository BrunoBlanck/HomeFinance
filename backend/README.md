# Backend — API Go do HomeFinance

Módulo: `github.com/brunorblanck/homefinance/backend`.

Toolchain: **Go 1.26** — o `go.mod` declara `go 1.26.6` e o `GOTOOLCHAIN=auto` (padrão) baixa o toolchain correto automaticamente, mesmo com um `go` local mais antigo (ADR-010). Não há linha `toolchain` no `go.mod`. Consequência operacional: uma máquina nova precisa de rede na primeira build, ou de um Go ≥ 1.26.6 já instalado.

> **Por que 1.26.6 e não 1.26.0** (desvio consciente da D13 da spec 0001): com o toolchain 1.26.0 o `govulncheck` acusa **17 vulnerabilidades alcançáveis na biblioteca padrão** (`crypto/tls`, `crypto/x509`, `net/http`, `net/mail`, `net/url`, `encoding/xml`, `encoding/asn1`, `net/textproto`), corrigidas nos patches 1.26.1–1.26.6. Nenhuma está em código do projeto ou em dependência de terceiros. Subir a diretiva `go` é a única forma de zerá-las sem adicionar linha `toolchain`, e `docs/SEGURANCA.md` §8 exige `govulncheck` limpo. Verificado: com 1.26.6, `govulncheck` reporta **0 vulnerabilidades alcançáveis**.

Escopo implementado: **spec 0001 — fundação do backend e autenticação** (`docs/specs/0001-fundacao-backend-auth.md`).

---

## Como rodar

A aplicação lê a configuração **exclusivamente do ambiente do processo** — ela
não abre `.env` nem qualquer outro arquivo (`docs/SEGURANCA.md` §7). Há duas
formas de rodar:

**Com `.env` (recomendado em dev).** Copie `.env.example` para `backend/.env`,
preencha, e use o script — ele traduz arquivo → ambiente e sobe a API:

```powershell
.\scripts\dev.ps1            # Windows PowerShell
```
```bash
./scripts/dev.sh              # Linux/macOS/Git Bash
```

O script procura `backend/.env` e, se não achar, `.env` na raiz do repositório.
Ele imprime apenas os **nomes** das chaves carregadas, nunca os valores; variável
já definida no shell vence o arquivo (convenção dotenv). `-NoRun` / `NO_RUN=1`
só exporta, sem subir a API.

**Sem `.env`, exportando à mão:**

```bash
# JWT_SECRET e OTP_PEPPER precisam de >= 32 bytes e não têm valor padrão.
export JWT_SECRET="$(openssl rand -base64 48)"
export OTP_PEPPER="$(openssl rand -base64 48)"   # DIFERENTE do JWT_SECRET
go run ./cmd/api
```

Em desenvolvimento o mailer de console imprime o e-mail (com o código de 6
dígitos) no STDOUT do processo. Todas as rotas ficam sob `/api/v1` — a sonda de
liveness é `GET /api/v1/health`.

Verificação completa antes de qualquer entrega:

```bash
./scripts/check.sh          # Linux/macOS/Git Bash
.\scripts\check.ps1         # Windows PowerShell
```

Comandos individuais: `go build ./...` · `go vet ./...` · `go test -race ./...` · `CGO_ENABLED=0 go build ./...` · `govulncheck ./...` · `gosec ./...`

Suíte multi-dialeto (opcional):

```bash
TEST_POSTGRES_DSN='postgres://...' go test -race ./internal/platform/storage/gormstore/...
TEST_POSTGRES_DSN='...' TEST_MYSQL_DSN='...' TEST_SQLSERVER_DSN='...' \
  go test -tags=integration -race ./internal/platform/storage/gormstore/...
```

---

## Layout real

```
backend/
  api/openapi.yaml                 contrato OpenAPI 3.1 — fonte de verdade (ADR-006/011)
  cmd/api/
    main.go                        composição: config → logger → db → AutoMigrate →
                                   repos → services → handlers → servidor → shutdown
    routes.go                      tabela única de rotas ([]Route)
    routes_test.go                 aderência: (método, path) da tabela == da spec
    janitor.go                     expurgo horário de códigos, refresh e auditoria
  internal/
    session/                       pacote-FOLHA: Identity no context.Context
    id/                            UUID v7 (RFC 9562) + Generator injetável
    audit/                         rastro de eventos sensíveis (types, repository, service)
    household/                     casa + vínculo (types, repository, service)
    user/                          usuário (types, email, repository, service, handler /me)
    auth/
      types.go errors.go repository.go
      password.go                  Argon2id 64MiB/t=3/p=2, PHC, hash-isca, semáforo
      password_policy.go           12–256 chars + denylist embutida
      data/common-passwords.txt    denylist (embed.FS)
      otp.go                       código de 6 dígitos: crypto/rand + HMAC-SHA-256(pepper)
      token.go                     JWT HS256 + refresh opaco (32 bytes) com SHA-256
      cookies.go                   __Host- quando Secure; HttpOnly/Secure/SameSite=Strict
      timing.go                    padTo — normaliza o tempo de resposta
      service.go                   Login, Refresh, Logout, PurgeExpired
      service_registration.go      Register, VerifyEmail, ResendCode
      service_password.go          ForgotPassword, ResetPassword
      handler.go                   endpoints HTTP de /auth
    platform/
      config/                      env fail-fast (caarlos0/env) + limites de taxa
      logging/                     slog com redação de campos sensíveis
      mailer/                      interface Mailer + console + SMTP + fila assíncrona
      httpserver/                  servidor, middlewares, CORS, CSRF, rate limit,
                                   decode, respond, errorshim, authmw, health
      storage/                     conexão GORM por DB_DRIVER + AutoMigrate
        gormstore/                 modelos, mapeadores, UnitOfWork e repositórios
  scripts/check.ps1 / check.sh     build + vet + test -race + govulncheck + gosec
  scripts/dev.ps1   / dev.sh       carrega o .env no ambiente e sobe a API (só dev)
```

### Regra de dependência (verificada por teste)

```
session, id  (folhas, sem imports do projeto)
      ↑
audit, household
      ↑
    user
      ↑
    auth
      ↑
platform/*  ←  cmd/api
```

- **`*gorm.DB` existe exclusivamente sob `internal/platform/storage/`.** Verificado por `TestGormNaoVazaParaForaDoStorage`.
- Nenhum domínio importa `platform/storage`, `platform/config` ou `platform/mailer`. Verificado por `TestDominiosNaoImportamPersistenciaNemConfig`.
- **Exceção consciente:** `auth/handler.go` e `user/handler.go` importam `platform/httpserver` — o kit de borda (decodificação, envelope de erro, IP do cliente). Não há ciclo: `httpserver` nunca importa um domínio; o middleware de autenticação recebe a interface `Authenticator`. Verificado por `TestHttpserverNaoImportaDominioDeNegocio`.
- Nenhum `Raw`, `Exec` ou string montada em `Where/Order/Select/Table`. Verificado por `TestSemSQLMontadoNoGormstore`.

---

## Variáveis de ambiente

Todos os valores são lidos **exclusivamente** por `internal/platform/config`. `Load()` valida tudo e **falha no boot** se algo estiver errado — a aplicação nunca sobe com configuração insegura.

### Aplicação

| Variável | Padrão | Obrigatória | Descrição |
|---|---|---|---|
| `APP_ENV` | `development` | não | `development`, `test` ou `production`. Em produção ativa as travas da tabela final. |
| `RATE_LIMITS_PROFILE` | `default` | não | Conjunto de limites de abuso: `default` (produção), `test` (perfil frouxo da suíte automatizada, exige `APP_ENV=test`) ou `dev` (máquina de desenvolvimento: eleva **só** os tetos da importação, exige `APP_ENV=development`). **Qualquer valor diferente de `default` é recusado em produção** — o boot falha. Valor desconhecido também falha. Ver `docs/SEGURANCA.md` §5.2. |

### Servidor HTTP

| Variável | Padrão | Obrigatória | Descrição |
|---|---|---|---|
| `HTTP_ADDR` | `:8080` | não | Endereço de escuta. |
| `HTTP_READ_HEADER_TIMEOUT` | `5s` | não | Tempo máximo para o cliente enviar os cabeçalhos (defesa contra Slowloris). |
| `HTTP_READ_TIMEOUT` | `15s` | não | Tempo máximo para ler a requisição inteira. |
| `HTTP_WRITE_TIMEOUT` | `30s` | não | Tempo máximo para escrever a resposta. |
| `HTTP_IDLE_TIMEOUT` | `60s` | não | Tempo máximo de conexão keep-alive ociosa. |
| `HTTP_SHUTDOWN_TIMEOUT` | `15s` | não | Prazo do desligamento gracioso (também drena a fila de e-mail). |
| `CORS_ORIGIN` | *(vazio)* | **em produção** | Origens permitidas, separadas por vírgula. Absolutas (`https://app.exemplo.com`). **Curinga é rejeitado**: a API responde com credenciais. |
| `TRUSTED_PROXY_COUNT` | `0` | não | Quantos proxies confiáveis existem à frente. Com `0`, `X-Forwarded-For` é **ignorado** — confiar nele por padrão anularia o rate limit por IP. |

### Log

| Variável | Padrão | Obrigatória | Descrição |
|---|---|---|---|
| `LOG_LEVEL` | `info` | não | `debug`, `info`, `warn` ou `error`. |
| `LOG_FORMAT` | `json` | não | `json` ou `text`. |

### Banco de dados

| Variável | Padrão | Obrigatória | Descrição |
|---|---|---|---|
| `DB_DRIVER` | `sqlite` | não | `sqlite`, `postgres`, `mysql` ou `sqlserver`. **`sqlite` é rejeitado em produção.** |
| `DB_DSN` | `homefinance.db` | não | String de conexão. Tratada como segredo: nunca aparece em log ou em mensagem de erro. |
| `DB_MAX_OPEN_CONNS` | `25` | não | Conexões abertas no pool. |
| `DB_MAX_IDLE_CONNS` | `5` | não | Conexões ociosas mantidas (≤ `DB_MAX_OPEN_CONNS`). |
| `DB_CONN_MAX_LIFETIME` | `30m` | não | Vida máxima de uma conexão. |
| `DB_AUTOMIGRATE` | `true` | não | Roda o `AutoMigrate` no boot (ADR-008). |

### Sessão e JWT

| Variável | Padrão | Obrigatória | Descrição |
|---|---|---|---|
| `JWT_SECRET` | — | **sim** | Segredo HS256. **Mínimo de 32 bytes** em qualquer ambiente; precisa ser diferente de `OTP_PEPPER`. |
| `JWT_ISSUER` | `homefinance` | não | Claim `iss`, verificada na validação. |
| `JWT_AUDIENCE` | `homefinance-api` | não | Claim `aud`, verificada na validação. |
| `ACCESS_TOKEN_TTL` | `15m` | não | Validade do access token. Limitado a 1m–15m. |
| `REFRESH_TOKEN_TTL` | `336h` | não | Validade do refresh (14 dias). Precisa ser maior que o access. |

### Código de 6 dígitos (OTP)

| Variável | Padrão | Obrigatória | Descrição |
|---|---|---|---|
| `OTP_PEPPER` | — | **sim** | Segredo do HMAC do código. **Mínimo de 32 bytes.** Sem ele, um vazamento do banco permitiria força bruta offline dos 10⁶ valores. |
| `OTP_TTL` | `15m` | não | Validade do código. Limitado a 5m–15m. |
| `OTP_MAX_ATTEMPTS` | `5` | não | Tentativas antes de queimar o código. Limitado a 1–5. |
| `OTP_RESEND_INTERVAL` | `60s` | não | Intervalo mínimo entre emissões para o mesmo e-mail e propósito. Mínimo de 30s. |

### Hash de senha (Argon2id)

| Variável | Padrão | Obrigatória | Descrição |
|---|---|---|---|
| `ARGON2_MEMORY_KIB` | `65536` | não | Memória por hash, em KiB. **Mínimo de 65536 (64 MiB)**; valor menor é elevado. |
| `ARGON2_ITERATIONS` | `3` | não | Iterações. Mínimo de 3. |
| `ARGON2_PARALLELISM` | `2` | não | Paralelismo. Mínimo de 2. |
| `ARGON2_MAX_CONCURRENT` | `4` | não | Hashes simultâneos. Cada um reserva 64 MiB — sem limite, o login vira amplificador de exaustão de memória. |

### Endpoints sensíveis

| Variável | Padrão | Obrigatória | Descrição |
|---|---|---|---|
| `AUTH_MIN_RESPONSE_TIME` | `300ms` | não | Piso de tempo de resposta de registro, reenvio, verificação, login, recuperação e redefinição. Normaliza o tempo para que ele não revele se a conta existe. Limitado a 0–5s. |

### Cookies

| Variável | Padrão | Obrigatória | Descrição |
|---|---|---|---|
| `COOKIE_SECURE` | `false` | **`true` em produção** | Liga `Secure` e o prefixo `__Host-` nos cookies de sessão. |
| `COOKIE_DOMAIN` | *(vazio)* | não | Domínio dos cookies. **Precisa ficar vazio quando `COOKIE_SECURE=true`** — o prefixo `__Host-` proíbe o atributo `Domain`. |

### E-mail

| Variável | Padrão | Obrigatória | Descrição |
|---|---|---|---|
| `MAILER` | `console` | **`smtp` em produção** | `console` (imprime o e-mail no STDOUT, só desenvolvimento) ou `smtp`. |
| `MAIL_FROM` | `nao-responda@homefinance.local` | sim em produção | Endereço remetente. |
| `MAIL_FROM_NAME` | `HomeFinance` | não | Nome de exibição do remetente. |
| `MAIL_QUEUE_SIZE` | `256` | não | Tamanho da fila assíncrona. Fila cheia descarta com log — nunca bloqueia a requisição. |
| `SMTP_HOST` | *(vazio)* | sim com `MAILER=smtp` | Servidor SMTP. |
| `SMTP_PORT` | `587` | não | Porta. `465` usa TLS implícito; as demais usam STARTTLS. |
| `SMTP_USERNAME` | *(vazio)* | não | Usuário. A autenticação só acontece sobre canal já cifrado. |
| `SMTP_PASSWORD` | *(vazio)* | não | Senha. Tratada como segredo: nunca aparece em log. |
| `SMTP_TLS` | `true` | não | Exige canal cifrado. **Não pode ser `false` em produção.** |

### Travas de produção (`APP_ENV=production`)

O boot **falha** se qualquer uma destas condições ocorrer:

- `MAILER` diferente de `smtp` — o mailer de console imprime o código;
- `SMTP_HOST`, `SMTP_PORT` ou `MAIL_FROM` ausentes;
- `SMTP_TLS=false`;
- `COOKIE_SECURE=false`;
- `CORS_ORIGIN` vazio ou com curinga;
- `DB_DRIVER=sqlite`;
- `RATE_LIMITS_PROFILE` diferente de `default` — `test` é o perfil frouxo da suíte automatizada e `dev` o da máquina de desenvolvimento.

E, em **qualquer** ambiente: `JWT_SECRET` ou `OTP_PEPPER` com menos de 32 bytes, ou iguais entre si.

### Não configurável por ambiente (de propósito)

| Item | Valor | Por quê |
|---|---|---|
| Limite de corpo da requisição | 1 MiB | Afrouxar por env seria um botão de negação de serviço. |
| Limites de taxa, **regra a regra** | §7 da spec 0001 (ver abaixo) | São limites de segurança, não sintonia operacional: mudar um valor exige código revisado. Ficam em `config.DefaultRateLimits()`. **Não existe** variável por limite (`RATE_LIMIT_IMPORT_UPLOAD=…` não é lida por nada) — é por aí que um teto frouxo vaza para produção sem aparecer em revisão. O único interruptor é `RATE_LIMITS_PROFILE`, que escolhe um **conjunto nomeado** de limites (nunca uma regra solta) e é recusado em produção (`docs/SEGURANCA.md` §5.2). |

**Limites de taxa vigentes** — global 100/min por IP · login 10/min por IP + 5/15min por conta · register 5/h por IP · resend-code 3/h por IP + cooldown de 60s por e-mail · forgot-password 5/h por IP + 3/h por conta · verify-email e reset-password 10/min por IP · refresh 60/min por IP · health/ready 60/min por IP.

**Perfil `dev`** (desde 18/09/2026; exige `APP_ENV=development` **declarado**) — sobe **só** a cota da importação: `POST /imports` 50/h por casa, 100/h por IP e confirm 150/h, com o **estouro fixado na cota do padrão** (10, 20 e 30 de uma vez) para não mexer na concorrência instantânea por casa. Todo o resto continua nos tetos de produção.

**Por casa** (chave = HMAC do `household_id`) — `POST /imports` 10/h (+20/h por IP) · `POST /imports/{id}/confirm` 30/h · `POST /transactions/auto-categorize` 60/h · `POST /transfers/detect` 60/h com estouro 3 · `PATCH /transactions/{id}` **120/h** (desde 17/09/2026: a rota escreve e audita a cada chamada, e o balde global por IP não é teto por casa; 120 acomoda a rajada legítima de categorizar uma fatura inteira).

**Cotas de ENVIO por endereço** (`auth.MailLimiters`, aplicadas no serviço) — código de verificação disparado pelo registro **10/h por endereço** (era 3/h até 09/09/2026), mais o cooldown de 60 s; aviso "conta já existe" **1 por 24 h por endereço**. Elas seguram a MENSAGEM e nunca devolvem 429: o e-mail do cadastro vem do corpo sem prova de posse, então recusar a requisição por endereço permitiria a um terceiro trancar o cadastro de um endereço alheio (lockout por procuração). A requisição continua respondendo o 202 do grupo A. **O número 10 depende de uma relação, não é solto:** a cota por endereço tem de ficar ACIMA do teto de `register` por IP (5/h), senão um único IP drena o balde do endereço alheio e a negação de cadastro por procuração volta — invariante coberto por teste em `internal/platform/config`. Custo colateral registrado na §1.1 de `docs/SEGURANCA.md`: o orçamento de palpites de OTP por endereço passa de 15/h para 50/h (10 códigos × 5 tentativas), ainda folgado contra os 10⁶ valores. Todo 429 traz `Retry-After`. A chave "por conta" é o **HMAC do e-mail com o pepper** — o e-mail em claro nunca entra no mapa do limitador.

---

## Achados de segurança remanescentes (justificados)

`govulncheck` e `gosec` estão limpos quanto a achados acionáveis. O que sobra e por quê:

| Ferramenta | Achado | Situação |
|---|---|---|
| `govulncheck` | `GO-2026-5932` — `golang.org/x/crypto/openpgp` é um pacote não mantido | **Não alcançável.** O projeto importa apenas `golang.org/x/crypto/argon2`. O aviso é de módulo, não de símbolo, e não há versão corrigida (`Fixed in: N/A`) porque a resolução é a deprecação do pacote. |
| `gosec` | 4 anotações `#nosec` | Duas `G101` (falso positivo: constantes do contrato chamadas `…InvalidCredentials`, que são código e mensagem de erro, não credenciais) e duas `G124` em `internal/auth/cookies.go` (o atributo `Secure` vem de `COOKIE_SECURE`, que a validação de configuração proíbe ser `false` em produção; `HttpOnly` e `SameSite=Strict` são sempre aplicados). Cada anotação carrega a justificativa no código. |

## Regras da casa

- Domínios são o núcleo e não importam persistência, config nem mailer; handler → service → repository (interface); **`*gorm.DB` nunca sai do `storage`**.
- Spec-first: `api/openapi.yaml` muda **antes** do handler; `cmd/api/routes_test.go` quebra o build se a tabela de rotas divergir da spec (ADR-011).
- Segurança: checklist obrigatório em `docs/SEGURANCA.md`; BOLA é o risco nº 1 — toda query filtra por `household_id` do token; toda entrega passa pelo agente `revisor-seguranca`.
- Banco dinâmico: qualquer SQL via `DB_DRIVER`, com **GORM** (ADR-008 — sem sqlx, sem goose); schema definido pelas structs e evoluído por **`AutoMigrate` no boot**; **sem chave estrangeira física** (ADR-013) — toda escrita multi-tabela passa por `UnitOfWork`.
- Dinheiro é `int64` em centavos. Nenhum endpoint desta entrega trafega dinheiro ainda.
- Código novo nasce com teste; bug corrigido nasce com teste de regressão.
