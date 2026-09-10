# Roadmap — HomeFinance

Fases de execução. Cada entrega de cada fase passa pelo ciclo obrigatório: planejar → implementar → testar → **revisão de segurança aprovada**.

Legenda: `[x]` concluído · `[~]` parcial (o que falta está dito na própria linha) · `[ ]` não iniciado.

## Fase 0 — Estruturação ✅ (concluída em 09/2026)

- [x] Equipe de agentes de IA (`.claude/agents/`)
- [x] Skills de trabalho (`.claude/skills/`)
- [x] Workflow de auditoria de segurança (`.claude/workflows/`)
- [x] Documentação: arquitetura, segurança, design, banco de dados
- [x] Regras do projeto (`CLAUDE.md` + `AGENTS.md`), git, permissões e hooks
- [x] Pesquisa de modernização aplicada — stack e ADRs atualizados (docs/PESQUISA.md, 09/2026)

## Fase 1 — Fundação do backend

- [x] `go mod init` (toolchain Go 1.26 via `GOTOOLCHAIN=auto`) + esqueleto **por domínio** conforme docs/ARQUITETURA.md
- [x] Config por env com fail-fast (caarlos0/env em `internal/platform/config`) — 20 validações cruzadas de produção
- [x] Conexão a banco dinâmica por `DB_DRIVER` (**GORM**, ADR-008) + **AutoMigrate** no boot (schema v1) — SQLite em dev
- [~] Contrato inicial `backend/api/openapi.yaml` versionado — **oapi-codegen adiado pelo ADR-011**, com `cmd/api/routes_test.go` impedindo divergência silenciosa entre spec e rotas
- [x] Middlewares: recover, request-id, slog, headers de segurança, CORS por allowlist, CSRF por `Origin`, limite de corpo, rate limit por IP e por conta (`x/time/rate`)
- [x] Health check — `GET /api/v1/health` (liveness, não toca o banco) e `/health/ready` (readiness com ping)
- [x] CI local: `backend/scripts/check.ps1` e `check.sh` (build · vet · test -race · build sem CGo · govulncheck · gosec)
- [ ] Hook de formatação automática (gofmt/goimports em PostToolUse)

## Fase 2 — Autenticação e casas

- [x] Registro/login (Argon2id com semáforo de concorrência, rate limit, mensagens seguras, política de senha com denylist embutida)
- [x] **Verificação obrigatória de e-mail por código de 6 dígitos** + "esqueci minha senha" pelo mesmo mecanismo (ADR-009, §1.1 de docs/SEGURANCA.md)
- [x] **Tentativa de cadastro com `registrationToken`** — fecha o *pre-hijacking* de conta (ADR-014); o código pertence a quem o pediu, não ao endereço
- [x] Interface `Mailer` — console em dev, SMTP em produção, envio **assíncrono** com drenagem no shutdown
- [x] JWT access (`golang-jwt/v5`) + **refresh opaco** com rotação, detecção de reúso e revogação de família (cookies HttpOnly, prefixo `__Host-`)
- [~] Households e memberships (papéis owner/member) — **convites ficaram de fora** desta entrega
- [x] Middleware de autorização por household (`hid` do token revalidado contra a membership real)
- [x] Audit log dos eventos de conta (escrita)

## Fase 3 — Fundação do frontend

- [x] Vite 8 + React 19 + TS estrito (`noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`); Biome; **TanStack Router + Query v5** — tipos TS gerados do OpenAPI **adiados junto com o ADR-011**
- [x] `tokens.css` a partir de docs/DESIGN.md (OKLCH, `@layer`) + componentes base (Button, TextField, PasswordField, **CodeInput**, Alert, Panel, Spinner, Skeleton, Logo, 13 ícones próprios); menu de usuário com **Popover API**
- [x] Fluxo de autenticação completo (react-hook-form + Zod 4) — entrar / criar conta / **verificar código de 6 dígitos** / esqueci minha senha / redefinir senha
- [~] Guard de rotas + layout base do app + Home — testes de componente com **Vitest em jsdom**, não Browser Mode
- [ ] Playwright E2E nos fluxos críticos (previsto no AGENTS.md, ainda não instalado)
- [ ] Telas sem teste: criar conta, esqueci minha senha, redefinir senha, Home e menu de usuário

## Fase 4 — Núcleo financeiro

- [ ] Contas (accounts) e categorias hierárquicas — CRUD
- [ ] Lançamentos: criação rápida, listagem densa com filtros, edição, exclusão lógica
- [ ] Contas recorrentes com vencimentos do mês (pago/pendente/atrasado)
- [ ] Dashboard do mês: saldo, entradas × saídas, próximos vencimentos

## Fase 5 — Orçamentos e relatórios

- [ ] Orçamentos por categoria/mês com acompanhamento
- [ ] Relatórios: evolução mensal, por categoria, por conta (skill `dataviz`)
- [ ] Exportação (CSV)

## Fase 6 — Acabamento e produção

- [ ] Playwright nos fluxos críticos
- [ ] Workflow `auditoria-seguranca` completo no projeto inteiro
- [ ] Suíte de integração multi-banco com testcontainers-go (Postgres, MySQL, SQLite, MSSQL)
- [ ] Passkeys (WebAuthn) via `go-webauthn/webauthn` como login preferencial
- [ ] Build de produção, TLS, backups, `.env.example` final e guia de deploy
