# Spec 0001 — Fundação do backend e autenticação

**Data:** 09/09/2026 · **Fases:** 1 (fundação) + autenticação da 2 · **Autor:** agente `arquiteto`
**ADRs relacionados:** ADR-008 (GORM), ADR-009 (OTP), ADR-010 (toolchain), ADR-011 (OpenAPI), ADR-012 (ciclo de vida da conta), ADR-013 (cookie/CSRF/FK)

> Esta spec é **normativa**. Onde ela e a documentação geral divergirem, vale a documentação geral (`docs/SEGURANCA.md` §1.1 em especial) e o desvio deve ser reportado, não implementado em silêncio.

---

## 1. Escopo

### Entra

Módulo Go com layout por domínio (ADR-004) · config por env com fail-fast · logging `slog` com redação de campos sensíveis · conexão GORM dinâmica por `DB_DRIVER` + `AutoMigrate` no boot · middlewares (recover, request-id, log, headers de segurança, CORS por allowlist, limite de corpo, checagem de `Origin` contra CSRF, rate limit por IP e por conta) · `GET /api/v1/health` e `/health/ready` · contrato `api/openapi.yaml` versionado com teste de aderência · autenticação completa (registro → OTP → verificação → login → refresh com rotação e detecção de reúso → logout → esqueci a senha → redefinir senha) com cookies `HttpOnly` · interface `Mailer` (console em dev, SMTP em prod) com envio **assíncrono** · modelos `users`, `households`, `memberships`, `refresh_tokens`, `verification_codes`, `audit_log` · `GET /api/v1/me` · auditoria dos eventos de conta · janitor de expurgo · scripts de verificação.

### Fica explicitamente de fora

Convites, gestão de membros, troca de papel, `switch-household`, renomear casa · wiring do oapi-codegen (ADR-011) · passkeys/WebAuthn, TOTP, "sair de todos os dispositivos", troca de e-mail, exclusão de conta · suíte testcontainers multi-banco (Fase 6) · API de leitura do audit log · qualquer domínio financeiro (`account`, `category`, `transaction`, `bill`, `budget`) · frontend.

---

## 2. Árvore de arquivos

Raiz: `backend/`

```
go.mod / go.sum             go 1.26.0 — SEM pins de teto (ver §5, D13)
api/openapi.yaml            contrato OpenAPI 3.1 desta entrega (ADR-006/011)
cmd/api/
  main.go                   composição: config → logger → db → AutoMigrate → repos → services → server → shutdown
  routes.go                 tabela única de rotas ([]Route{Method,Pattern,Handler,Middlewares})
  routes_test.go            aderência: (método,path) de routes.go == de api/openapi.yaml
  janitor.go                loop horário de expurgo de códigos/refresh expirados, parado no shutdown
internal/
  session/session.go        pacote-FOLHA sem imports: Identity{UserID,HouseholdID,Role,SessionID}, NewContext, FromContext
  id/id.go                  New() string → UUID v7; type Generator func() string para injeção em teste
  id/id_test.go
  audit/                    types.go (Entry + constantes de Action), repository.go (interface), service.go, service_test.go
  household/                types.go, repository.go, service.go (EnsureDefault idempotente), service_test.go
  user/                     types.go, email.go (+_test), repository.go, service.go (+_test), handler.go (GET /me) (+_test)
  auth/
    types.go errors.go repository.go
    password.go (+_test)             Argon2id m=64MiB t=3 p=2 salt16 key32, string PHC, VerifyDummy, semáforo
    password_policy.go (+_test)      12–256 chars, difere de e-mail/nome, denylist embutida via embed.FS
    data/common-passwords.txt        denylist (uma senha por linha)
    otp.go (+_test)                  6 dígitos com crypto/rand.Int SEM viés; HMAC-SHA-256(pepper, purpose|email|code); hmac.Equal
    token.go (+_test)                JWT HS256 emissão/validação; refresh opaco crypto/rand + SHA-256
    cookies.go (+_test)              nomes __Host- quando Secure; HttpOnly/Secure/SameSite=Strict/Path=/; Set e Clear
    timing.go                        padTo(start, d) — normaliza duração de resposta dos endpoints sensíveis
    service.go                       construtor, Login, Refresh, Logout, PurgeExpired
    service_registration.go          Register, VerifyEmail, ResendCode
    service_password.go              ForgotPassword, ResetPassword
    service_test.go / service_abuse_test.go / handler.go / handler_test.go
  platform/
    config/config.go (+_test)        tags env + Load() com fail-fast e validações cruzadas de produção
    logging/logging.go (+_test)      New(cfg); ReplaceAttr redige password, code, token, cookie, dsn, secret, hash
    mailer/                          mailer.go (interface), console.go, smtp.go (net/smtp + TLS),
                                     message.go (+_test, anti CR/LF), queue.go (+_test, async + drain), templates.go
    httpserver/                      server.go, middleware.go (+_test), headers.go, cors.go (+_test), csrf.go (+_test),
                                     ratelimit.go (+_test), clientip.go (+_test), decode.go (+_test), respond.go (+_test),
                                     errorshim.go (+_test), authmw.go (+_test), health.go (+_test)
    storage/                         storage.go (Open por driver), gormlogger.go, migrate.go (+_test: vazio E povoado)
      gormstore/                     models.go, mapper.go, uow.go, db_from_context.go,
                                     {user,household,membership,refresh_token,verification_code,audit}_repository.go
                                     helpers_test.go + testes por repositório
                                     integration_sql_test.go  //go:build integration
scripts/check.ps1 / check.sh         build + vet + test -race + govulncheck + gosec
README.md                            layout real + tabela de variáveis de ambiente
```

**Regra de dependência (acíclica, verificável):**
`session`, `id` (folhas, sem imports) ← `audit`, `household` ← `user` ← `auth` ← `platform/*` ← `cmd/api`.
**`*gorm.DB` aparece exclusivamente em `internal/platform/storage/` e `.../gormstore/`.** Nenhum pacote de domínio importa `platform`.

---

## 3. Contrato da API

Base `/api/v1`. JSON `camelCase`. Erro sempre no formato único:

```json
{ "error": { "code": "VALIDATION_FAILED", "message": "Dados inválidos.", "fields": { "password": "mínimo de 12 caracteres" } } }
```

`fields` só existe em `VALIDATION_FAILED`. Códigos: `VALIDATION_FAILED`, `INVALID_CREDENTIALS`, `INVALID_CODE`, `EMAIL_NOT_VERIFIED`, `UNAUTHENTICATED`, `INVALID_SESSION`, `FORBIDDEN`, `NOT_FOUND`, `METHOD_NOT_ALLOWED`, `RATE_LIMITED`, `PAYLOAD_TOO_LARGE`, `UNSUPPORTED_MEDIA_TYPE`, `SERVICE_UNAVAILABLE`, `INTERNAL_ERROR`.

Cookies (prefixo `__Host-` quando `COOKIE_SECURE=true`):

| Cookie | Conteúdo | Atributos |
|---|---|---|
| `hf_access` | JWT HS256, TTL 15 min | `HttpOnly; Secure; SameSite=Strict; Path=/; Max-Age=900` |
| `hf_refresh` | token opaco (32 bytes de `crypto/rand`), TTL 14 dias | `HttpOnly; Secure; SameSite=Strict; Path=/; Max-Age=1209600` |

| # | Endpoint | Request | Sucesso | Erros |
|---|---|---|---|---|
| 3.1 | `GET /health` | — | **200** `{"status":"ok"}` (nunca falha) | — |
| 3.2 | `GET /health/ready` | — | **200** `{"status":"ok"}` | 503 `SERVICE_UNAVAILABLE` (sem detalhe), 429 |
| 3.3 | `POST /auth/register` | `{name,email,password}` | **202** `{"status":"verification_required","email":"…","expiresInSeconds":900}` | 400, 413, 415, 429, 500 |
| 3.4 | `POST /auth/verify-email` | `{email,code}` | **200** + cookies + corpo de `/me` (login automático) | 400, 401 `INVALID_CODE`, 429, 500 |
| 3.5 | `POST /auth/resend-code` | `{email}` | **202** (mesmo corpo de 3.3) | 400, 429, 500 |
| 3.6 | `POST /auth/login` | `{email,password}` | **200** + cookies + corpo de `/me` | 400, 401 `INVALID_CREDENTIALS`, **403 `EMAIL_NOT_VERIFIED`**, 429, 500 |
| 3.7 | `POST /auth/refresh` | — (cookie) | **200** + cookies rotacionados + corpo de `/me` | 401 `INVALID_SESSION` (sempre limpa cookies), 429, 500 |
| 3.8 | `POST /auth/logout` | — | **204** sempre (idempotente), cookies limpos, revoga a família | 429, 500 |
| 3.9 | `POST /auth/forgot-password` | `{email}` | **202** `{"status":"accepted","message":"Se este e-mail estiver cadastrado, enviamos um código de 6 dígitos.","expiresInSeconds":900}` | 400 (só e-mail inválido), 429, 500 |
| 3.10 | `POST /auth/reset-password` | `{email,code,newPassword}` | **204**, cookies limpos | 400, 401 `INVALID_CODE`, 429, 500 |
| 3.11 | `GET /me` | — (cookie) | **200** (ver abaixo) | 401 `UNAUTHENTICATED`, 500 |

`GET /me`:
```json
{
  "user":       { "id":"…","name":"Bruno Blanck","email":"…","emailVerifiedAt":"2026-09-09T14:03:11Z","createdAt":"…" },
  "household":  { "id":"…","name":"Casa de Bruno","role":"owner" },
  "households": [ { "id":"…","name":"Casa de Bruno","role":"owner" } ]
}
```
**401 também quando a membership do `hid` não existe mais** (token válido, realidade mudou).

### 3.12 Respostas que precisam ser IDÊNTICAS (status, corpo byte a byte e tempo aproximado)

| Grupo | Situações indistinguíveis entre si |
|---|---|
| **A — registro** | e-mail novo · já cadastrado e verificado · já cadastrado e não verificado → **202**, mesmo corpo |
| **B — reenvio** | e-mail existente · inexistente · dentro do cooldown de 60 s → **202**, mesmo corpo |
| **C — esqueci a senha** | existente e verificado · existente não verificado · inexistente → **202**, mesmo corpo |
| **D — validação de código** (`verify-email` e `reset-password`) | código errado · expirado · já consumido · tentativas esgotadas · e-mail sem código · e-mail inexistente · código do outro `purpose` → **401 `INVALID_CODE`**, mesma mensagem |
| **E — login** | e-mail inexistente · senha errada → **401 `INVALID_CREDENTIALS`**, mesma mensagem e **mesmo custo de Argon2id** (hash-dummy quando o usuário não existe) |
| **F — refresh** | cookie ausente · malformado · expirado · revogado · reúso detectado → **401 `INVALID_SESSION`**, mesma mensagem |

Uniformidade de tempo: A, B, C, D e E passam por `auth.padTo(start, AUTH_MIN_RESPONSE_TIME)` (padrão 300 ms) e **nunca** enviam e-mail dentro do request — a fila assíncrona remove a variância do SMTP.

> **Nota sobre o 403 `EMAIL_NOT_VERIFIED` (3.6) — decidido contra a objeção do `designer-ui`.**
> O designer pediu 401 genérico também para conta não verificada, temendo enumeração. A decisão é **manter o 403**, porque ele só é emitido **depois de a senha conferir**: quem recebe essa resposta já provou saber a senha, logo já sabe que a conta existe — não há informação nova vazada, e o grupo E acima continua íntegro. Em troca, o usuário legítimo não fica preso num login que falha sem explicar. **O frontend deve tratar 403 `EMAIL_NOT_VERIFIED` navegando para `/confirmar-email`** — o backend reemite e envia o código nesse mesmo caminho (respeitando o cooldown).

---

## 4. Modelo de dados (`gormstore/models.go`)

IDs `varchar(36)` com **UUID v7 gerado no service** (`internal/id`), nunca no repositório e nunca em hook GORM. Datas `time.Time` UTC (`NowFunc` do GORM). Zero booleanos — só timestamps anuláveis. **Zero FK física** (ADR-013), índice explícito em toda coluna de referência.

```go
type User struct {
    ID              string     `gorm:"type:varchar(36);primaryKey"`
    Email           string     `gorm:"type:varchar(254);not null;uniqueIndex:ux_users_email"`
    PasswordHash    string     `gorm:"type:varchar(255);not null"`   // PHC Argon2id
    Name            string     `gorm:"type:varchar(120);not null"`
    EmailVerifiedAt *time.Time `gorm:"index:ix_users_email_verified_at"`
    CreatedAt       time.Time  `gorm:"not null"`
    UpdatedAt       time.Time  `gorm:"not null"`
}

type Household struct {
    ID        string    `gorm:"type:varchar(36);primaryKey"`
    Name      string    `gorm:"type:varchar(120);not null"`
    CreatedAt time.Time `gorm:"not null"`
    UpdatedAt time.Time `gorm:"not null"`
}

type Membership struct {
    ID          string    `gorm:"type:varchar(36);primaryKey"`
    HouseholdID string    `gorm:"type:varchar(36);not null;uniqueIndex:ux_memberships_household_user,priority:1;index:ix_memberships_household"`
    UserID      string    `gorm:"type:varchar(36);not null;uniqueIndex:ux_memberships_household_user,priority:2;index:ix_memberships_user"`
    Role        string    `gorm:"type:varchar(20);not null"`   // owner | member
    CreatedAt   time.Time `gorm:"not null"`
    UpdatedAt   time.Time `gorm:"not null"`
}

type RefreshToken struct {
    ID         string     `gorm:"type:varchar(36);primaryKey"`
    UserID     string     `gorm:"type:varchar(36);not null;index:ix_refresh_tokens_user"`
    FamilyID   string     `gorm:"type:varchar(36);not null;index:ix_refresh_tokens_family"`
    TokenHash  string     `gorm:"type:varchar(64);not null;uniqueIndex:ux_refresh_tokens_hash"` // SHA-256 hex
    ExpiresAt  time.Time  `gorm:"not null;index:ix_refresh_tokens_expires_at"`
    RevokedAt  *time.Time
    ReplacedBy *string    `gorm:"type:varchar(36)"`   // forense da rotação
    CreatedAt  time.Time  `gorm:"not null"`
    UpdatedAt  time.Time  `gorm:"not null"`
}

type VerificationCode struct {
    ID         string     `gorm:"type:varchar(36);primaryKey"`
    Email      string     `gorm:"type:varchar(254);not null;index:ix_vcodes_email_purpose,priority:1"`
    Purpose    string     `gorm:"type:varchar(32);not null;index:ix_vcodes_email_purpose,priority:2"`
    UserID     *string    `gorm:"type:varchar(36);index:ix_vcodes_user"`
    CodeHash   string     `gorm:"type:varchar(64);not null"`   // HMAC-SHA-256 hex — NUNCA o código
    ExpiresAt  time.Time  `gorm:"not null;index:ix_vcodes_expires_at"`
    ConsumedAt *time.Time
    Attempts   int        `gorm:"not null;default:0"`
    CreatedAt  time.Time  `gorm:"not null"`
    UpdatedAt  time.Time  `gorm:"not null"`
}

type AuditLog struct {
    ID          string    `gorm:"type:varchar(36);primaryKey"`
    HouseholdID *string   `gorm:"type:varchar(36);index:ix_audit_log_household"`
    UserID      *string   `gorm:"type:varchar(36);index:ix_audit_log_user"`
    Action      string    `gorm:"type:varchar(64);not null;index:ix_audit_log_action"`
    Entity      string    `gorm:"type:varchar(64);not null"`
    EntityID    *string   `gorm:"type:varchar(36)"`
    IP          string    `gorm:"type:varchar(45);not null"`   // comporta IPv6
    CreatedAt   time.Time `gorm:"not null;index:ix_audit_log_created_at"`
}
```

Cada modelo declara `func (X) TableName() string`.

### Operações que PRECISAM ser atômicas (portáteis, sem `RETURNING`)

- **Consumir código:** `UPDATE verification_codes SET consumed_at=? WHERE id=? AND consumed_at IS NULL` → exigir `RowsAffected == 1`.
- **Contar tentativa:** `UPDATE … SET attempts = attempts + 1 WHERE id=? AND consumed_at IS NULL` e **só então** reler para comparar com o máximo (nunca read-modify-write em Go).
- **Rotacionar refresh:** `UPDATE refresh_tokens SET revoked_at=?, replaced_by=? WHERE id=? AND revoked_at IS NULL` → se `RowsAffected == 0`, outra requisição já rotacionou ⇒ tratar como **reúso** e revogar a família.
- **Registro/verificação/reset:** `UnitOfWork` envolvendo usuário + casa + membership + auditoria.

---

## 5. Decisões

- **D1** — A `household` nasce na **verificação**, não no registro. Nome: `"Casa de " + primeiro nome` (truncado em 120); nome vazio ⇒ `"Minha casa"`. Spam de registro não deixa casas órfãs.
- **D2** — `household.Service.EnsureDefault` é **idempotente** e roda na verificação *e* no login (auto-reparo), garantindo que a claim `hid` nunca fique vazia.
- **D3** — Registro com e-mail existente responde **202 idêntico**. Verificado ⇒ nada criado + e-mail "alguém tentou criar uma conta com este endereço". Não verificado ⇒ sobrescreve nome/hash pendentes e reemite código. Isso não é escalada: a conta não verificada é inutilizável e concluir o fluxo continua exigindo a caixa de entrada.
- **D4** — Login não verificado ⇒ **403 `EMAIL_NOT_VERIFIED` só depois de a senha conferir** (ver nota em §3.12). Dispara reemissão + envio.
- **D5** — `verification_codes` ancorado em **`email + purpose`**, `user_id` anulável. A consulta é sempre `WHERE email=? AND purpose=?` — mesma forma e mesmo custo exista ou não a conta, que é o que o grupo D exige. **Sem linha-isca** para e-mail inexistente (seria vetor de inflar tabela).
- **D6** — OTP: **HMAC-SHA-256 com pepper**, ligado a `purpose|email|code` (6 dígitos ≈ 20 bits: sem pepper, vazamento do banco permite força bruta offline; ligar ao e-mail e ao propósito impede replay). Refresh: **SHA-256 puro** (≥128 bits de entropia já é irreversível; evita mais um segredo obrigatório).
- **D7** — Envio de e-mail **assíncrono** (fila em memória + worker), drenada no shutdown. SMTP dentro do request seria o maior vazamento de tempo do fluxo "esqueci a senha". Fila cheia ⇒ descarta e loga; nunca bloqueia.
- **D8** — Mailer por `MAILER=console|smtp`. `Load()` **falha no boot** se: `APP_ENV=production` e (`MAILER != smtp`, ou faltar `SMTP_HOST/SMTP_PORT/SMTP_FROM`, ou `SMTP_TLS=false`, ou `COOKIE_SECURE=false`, ou `CORS_ORIGIN` vazio/`*`, ou `DB_DRIVER=sqlite`); e, em qualquer ambiente, se `JWT_SECRET` ou `OTP_PEPPER` tiverem < 32 bytes.
- **D9** — ADR-011: spec escrita e versionada agora, handlers manuais, `routes_test.go` impede divergência silenciosa.
- **D10** — CSRF por `SameSite=Strict` + checagem de `Origin`/`Sec-Fetch-Site` contra a allowlist, sem token double-submit.
- **D11** — Prefixo `__Host-` quando `COOKIE_SECURE=true`. Custo aceito: `Path=/` obrigatório (abre-se mão do escopo `/api/v1/auth` no refresh).
- **D12** — Sem FK física; integridade em Go dentro de transação (ADR-013).
- **D13 — CORRIGIDA em relação ao plano original.** O plano assumia toolchain Go 1.24 e mandava fixar `x/time v0.8.0`, `x/crypto v0.48.0`, `x/sys`, `x/text`. **Isso foi verificado e refutado:** com `GOTOOLCHAIN=auto` o Go baixa o toolchain 1.26.0 e `go build ./...` e `CGO_ENABLED=0 go build ./...` passam com as versões **mais recentes** de tudo. **Não fixe tetos de versão.** `go.mod` declara `go 1.26.0`. Ver ADR-010.
- **D14** — `internal/session` e `internal/id` são pacotes-folha, para evitar o ciclo `auth → user → auth`.
- **D15** — Argon2id com **semáforo** (`ARGON2_MAX_CONCURRENT`, padrão 4): 64 MiB por verificação sem limite torna o login um amplificador de exaustão de memória.
- **D16** — IP do cliente de `RemoteAddr`; `X-Forwarded-For` **só** se `TRUSTED_PROXY_COUNT > 0` (N-ésima entrada da direita). Confiar em XFF por padrão anula todo o rate limit por IP.
- **D17** — `/health` não toca o banco (não é amplificador de DoS); `/health/ready` faz ping e é limitado. Nenhum expõe versão, driver ou erro.
- **D18** — Casa ativa do token = membership **mais antiga**. Determinístico, sem coluna nova. Múltiplas casas entram com `switch-household` e ADR próprio.
- **D19** — `.env.example` está **bloqueado por permissão** (`.env*` no deny do `settings.json`). A tabela de variáveis vai para `backend/README.md` e o bloco pronto para colar sai no relatório final.
- **D20** — A denylist de senhas comuns (`internal/auth/data/common-passwords.txt`, embutida via `embed.FS`) **entra**: sem ela, `"123456789012"` passa na política de 12 caracteres. Stdlib pura, custo zero.

---

## 6. Dependências

**Autorizadas (ADR-005/008), já no `go.mod` e verificadas compilando:** `gorm.io/gorm` · `github.com/glebarez/sqlite` (puro Go, sem CGo) · `gorm.io/driver/{postgres,mysql,sqlserver}` · `github.com/golang-jwt/jwt/v5` · `github.com/go-playground/validator/v10` · `github.com/caarlos0/env/v11` · `golang.org/x/time` · `github.com/stretchr/testify`.

**Novas, justificadas:** `golang.org/x/crypto` (Argon2id — a stdlib não tem, e a §1 de SEGURANCA.md exige) · `github.com/google/uuid` (UUID v7 / RFC 9562 — não existe na stdlib; montar o layout de bits à mão é bug silencioso de ordenação) · `gopkg.in/yaml.v3` **só em `_test.go`** (teste de aderência spec↔rotas do ADR-011).

**Recusadas de propósito:** oapi-codegen (adiado) · qualquer lib de SMTP (`net/smtp` basta) · qualquer lib de CSRF · chi · sqlx e goose (proibidos pelo ADR-008) · lib de rate limit além de `x/time/rate` · `x/sync/semaphore` (canal com buffer resolve).

---

## 7. Configuração

`APP_ENV` · `HTTP_ADDR` · `HTTP_READ_HEADER_TIMEOUT` · `HTTP_READ_TIMEOUT` · `HTTP_WRITE_TIMEOUT` · `HTTP_IDLE_TIMEOUT` · `HTTP_SHUTDOWN_TIMEOUT` · `CORS_ORIGIN` · `TRUSTED_PROXY_COUNT` · `LOG_LEVEL` · `LOG_FORMAT` · `DB_DRIVER` · `DB_DSN` · `DB_MAX_OPEN_CONNS` · `DB_MAX_IDLE_CONNS` · `DB_CONN_MAX_LIFETIME` · `DB_AUTOMIGRATE` · `JWT_SECRET` · `JWT_ISSUER` · `JWT_AUDIENCE` · `ACCESS_TOKEN_TTL` · `REFRESH_TOKEN_TTL` · `OTP_PEPPER` · `OTP_TTL` · `OTP_MAX_ATTEMPTS` · `OTP_RESEND_INTERVAL` · `ARGON2_MEMORY_KIB` · `ARGON2_ITERATIONS` · `ARGON2_PARALLELISM` · `ARGON2_MAX_CONCURRENT` · `AUTH_MIN_RESPONSE_TIME` · `COOKIE_SECURE` · `COOKIE_DOMAIN` · `MAILER` · `MAIL_FROM` · `MAIL_FROM_NAME` · `MAIL_QUEUE_SIZE` · `SMTP_HOST` · `SMTP_PORT` · `SMTP_USERNAME` · `SMTP_PASSWORD` · `SMTP_TLS`.

**Rate limits (padrões configuráveis):** global 100/min por IP · login 10/min por IP + 5/15min por conta · register 5/h por IP · resend-code 3/h por IP + cooldown 60 s por e-mail · forgot-password 5/h por IP + 3/h por e-mail · verify-email e reset-password 10/min por IP · refresh 60/min por IP.
A chave "por conta" é o **HMAC do e-mail com o pepper** — o e-mail em claro nunca entra no mapa do limitador. Todo 429 traz `Retry-After`.

---

## 8. Critérios de aceite

**Build e ferramentas**
1. `go build ./...`, `go vet ./...`, `go test -race ./...` passam — saída real reportada.
2. `CGO_ENABLED=0 go build ./...` compila e o binário sobe contra SQLite (prova o "puro Go" do ADR-008).
3. `govulncheck ./...` e `gosec ./...` limpos (achado remanescente exige justificativa escrita).

**Banco**
4. `AutoMigrate` cria as 6 tabelas em SQLite a partir de banco vazio **e** roda de novo sobre banco povoado sem erro e sem perda.
5. Testes de repositório em SQLite em memória; com `TEST_POSTGRES_DSN` definido, a mesma suíte passa em PostgreSQL.
6. Nenhuma ocorrência de `.Raw(`, `.Exec(` ou `fmt.Sprintf` dentro de `Where/Order/Select/Table` no `gormstore`.
7. `grep -r "gorm.DB" internal/` só retorna caminhos sob `internal/platform/storage/`.

**Contrato**
8. `cmd/api/routes_test.go` passa.
9. Todo erro sai no formato único, **inclusive** 404/405 do `ServeMux` (teste do `errorshim`).

**Fluxo funcional**
10. Registrar → ler o código no log do mailer de console → verificar → receber cookies → `GET /me` com casa `"Casa de {nome}"` e papel `owner`.
11. Login antes de verificar ⇒ 403 `EMAIL_NOT_VERIFIED` + código novo; depois de verificar ⇒ 200 com cookies.
12. Refresh rotaciona os dois cookies e o refresh anterior deixa de funcionar.
13. Esqueci-a-senha completo: 202 → código do log → `reset-password` 204 → refresh antigo passa a dar 401 → login com a senha nova funciona.
14. `logout` ⇒ 204 e o refresh deixa de valer; `logout` de novo, sem cookie, continua 204.

**Segurança verificável por teste**
15. Grupos A–F do §3.12: corpo **byte a byte idêntico** e mesmo status dentro de cada grupo.
16. Nenhum código, senha, hash, DSN ou token aparece em resposta ou log — teste captura a saída do `slog` num buffer e faz assert de ausência.
17. Cookies com `HttpOnly`, `Secure`, `SameSite=Strict` e prefixo `__Host-` quando `COOKIE_SECURE=true`.
18. JWT: rejeitar `alg: none`, assinatura com outra chave, `iss`/`aud` errados, expirado, claim adulterada.
19. Reúso de refresh revogado derruba a família inteira e grava `auth.refresh_reuse_detected` no `audit_log`.
20. 5 tentativas erradas queimam o código; a 6ª, **mesmo com o código certo**, devolve 401 `INVALID_CODE`.
21. Código de `email_verification` rejeitado em `reset-password` e vice-versa.
22. Emitir código novo invalida todos os anteriores do mesmo `purpose` para aquele e-mail.
23. `Load()` falha no boot com `APP_ENV=production` + `MAILER=console`; idem com segredos curtos, `COOKIE_SECURE=false`, `CORS_ORIGIN` vazio/`*`, `DB_DRIVER=sqlite`.
24. `/me` com token válido cuja membership foi apagada ⇒ 401 + cookies limpos.
25. Campo desconhecido no JSON ⇒ 400; corpo acima do limite ⇒ 413; `Content-Type` errado ⇒ 415.
26. Origem fora da allowlist não recebe `Access-Control-Allow-Origin`; `POST` com `Origin` estranho ⇒ 403.
27. Estourar o limite do login ⇒ 429 com `Retry-After`, sem revelar se a conta existe.

---

## 9. Riscos de segurança, em ordem de gravidade (roteiro do `revisor-seguranca`)

1. **Bypass de auth por validação frouxa de JWT** — `alg` não pinado, `iss`/`aud`/`exp` não verificados, segredo fraco. Mitigação: `jwt.WithValidMethods(["HS256"])` + `WithIssuer` + `WithAudience` + `WithExpirationRequired`, segredo ≥32 bytes validado no boot.
2. **`hid` do token divorciado da realidade** — token válido dando acesso a casa da qual o usuário já não é membro. É a semente do BOLA de todas as fases seguintes. Mitigação: revalidar membership em `/me` e no `refresh`; `RequireAuth` **nunca** aceita `household_id` de corpo/query/path.
3. **Força bruta do OTP** (20 bits) — conferir incremento atômico, limite 5, expiração ≤15 min, consumo com `RowsAffected == 1`, rate limit por IP **e** por e-mail, HMAC ligado a `purpose|email`.
4. **Vazamento de existência de conta** — o mais fácil de quebrar numa refatoração. Conferir os 6 grupos por status, corpo, cabeçalho **e tempo**; conferir fila assíncrona e `padTo`.
5. **Corrida na rotação de refresh** — sem `UPDATE … WHERE revoked_at IS NULL` + checagem de `RowsAffected`, ou o roubo passa, ou o usuário legítimo é derrubado.
6. **Segredo em log** — mailer de console imprime o código de propósito; o logger do GORM imprime SQL com valores; a DSN carrega senha. Conferir redação, `gormlogger` silencioso em produção e o fail-fast do mailer.
7. **Injeção via GORM** — `Raw`/`Exec`/`Where` com string montada, `Order`/`Select`/`Table` com entrada do usuário. Nesta entrega **não deve existir nenhum**.
8. **CSRF / cookies** — flags erradas, `SameSite` relaxado, CORS com curinga ou reflexo do `Origin`, `Allow-Credentials` com origem dinâmica.
9. **Exaustão de recursos** — Argon2id sem semáforo, corpo sem `MaxBytesReader`, mapa do rate limiter sem GC, fila sem limite, `padTo` segurando goroutines.
10. **Rate limit contornável** — `X-Forwarded-For` confiável por padrão anula tudo.
11. **Escrita multi-tabela fora de transação** — sem FK física, gera casa sem membership. Conferir `Register`, `VerifyEmail`, `ResetPassword`.
12. **Header injection no SMTP** — `To`/`Subject` com CR/LF.
13. **Serialização de entidade crua** — vaza `password_hash` no dia em que um campo for adicionado. Todo handler precisa de DTO próprio.
