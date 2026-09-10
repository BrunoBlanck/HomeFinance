# HomeFinance — Guia do projeto

Regras universais, válidas para qualquer ferramenta ou pessoa. O fluxo específico do Claude Code (equipe de agentes, skills) está em `CLAUDE.md`.

## Projeto

Aplicação de controle financeiro doméstico: receitas, despesas, contas recorrentes, categorias, orçamentos e relatórios. Multiusuário por casa (*household*). Dados financeiros sensíveis — **segurança é requisito de primeira classe**.

## Idioma

Comunicação e documentação em **português brasileiro**. Código, identificadores, nomes de arquivos e mensagens de commit em **inglês**; comentários e textos da interface em português.

## Stack (decisões de set/2026 — fontes em docs/PESQUISA.md)

| Camada | Tecnologia |
|---|---|
| Backend | **Go 1.26** (`go.mod` declara `go 1.26.0`; o `GOTOOLCHAIN=auto` baixa o toolchain — o `go` local pode ser mais antigo), roteador `net/http.ServeMux` nativo, `log/slog`, config por env com `caarlos0/env` |
| Banco | **GORM v2** (`gorm.io/gorm`) com repositórios por interface — **qualquer SQL** (PostgreSQL padrão; MySQL, SQLite, MSSQL). **SQLite em dev** (`glebarez/sqlite`, puro Go). Evolução de schema por **AutoMigrate** a partir das structs — ADR-008 (supera o ADR-002: sem sqlx, sem goose) |
| Auth | `golang-jwt/jwt/v5` (access 10–15 min) + **refresh opaco** com rotação e invalidação de família; senhas Argon2id; **verificação obrigatória de e-mail e recuperação de senha por código de 6 dígitos (OTP)**; passkeys (WebAuthn) planejado |
| Contrato | **OpenAPI 3.1 spec-first** com oapi-codegen (spec versionada gera tipos e stubs) |
| Frontend | React 19.2 + Vite 8 + TypeScript estrito (`noUncheckedIndexedAccess`), **TanStack Router** + TanStack Query v5, CSS Modules + design tokens (OKLCH), react-hook-form + Zod 4, lint/format com **Biome** |
| Testes | Go: `testing` + testify + httptest + testcontainers-go (suíte nos 4 bancos), `-race` sempre · Front: Vitest 4 (+ Browser Mode) + Testing Library + Playwright E2E |

## Comandos

Backend (`backend/`): `go build ./...` · `go vet ./...` · `go test -race ./...` · `govulncheck ./...` · `gosec ./...`
Frontend (`frontend/`): `npm run dev` · `npm run build` · `npm test` · `npx biome check --write .`

## Estrutura do repositório

```
AGENTS.md            ← este arquivo (regras universais)
CLAUDE.md            ← fluxo com a equipe de IA do Claude Code
.claude/             ← agentes, skills, workflows, settings
docs/                ← ARQUITETURA, SEGURANCA, DESIGN, BANCO-DE-DADOS, ROADMAP, PESQUISA
backend/             ← API Go (layout por domínio — ver docs/ARQUITETURA.md)
frontend/            ← app React (feature-based — ver docs/ARQUITETURA.md)
```

## Regras inegociáveis

### Segurança (checklist completo em docs/SEGURANCA.md)
- **Nunca** concatenar entrada do usuário em SQL — sempre parametrização (inclusive `ORDER BY`/`LIMIT`, via allowlist).
- **Nunca** segredos em código ou arquivos versionados — só env (`.env` está no `.gitignore` e bloqueado para edição).
- **BOLA é o risco nº 1** (OWASP API Top 10): toda operação filtra por `household_id` vindo do token, nunca do cliente; recurso de outra casa responde 404.
- Toda entrada externa é validada no backend; erros ao cliente são genéricos; logs sem dados sensíveis.
- Dinheiro é `int64` em **centavos** — float é proibido em qualquer camada.
- Dependência nova exige justificativa + `govulncheck`/`npm audit` limpos.

### Design (identidade completa em docs/DESIGN.md)
- **Proibido** bibliotecas de componentes (MUI, shadcn, Ant, Chakra, Bootstrap) e Tailwind. Todo componente é escrito para este projeto.
- Proibido o visual genérico de IA: gradiente roxo/azul, glassmorphism, emojis na UI, grids de cards vazios, Inter por preguiça. Cor cromática é reservada para estado financeiro.
- Acessibilidade obrigatória: elementos nativos primeiro (`<dialog>`, Popover API), padrões WAI-ARIA APG no resto, contraste AA, foco visível.

### Qualidade
- Código novo nasce com teste; bug corrigido nasce com teste de regressão.
- Nenhum `panic` em caminho de request; erros embrulhados com contexto.
- Schema portátil entre os 4 dialetos (GORM/AutoMigrate); variantes por dialeto só nos lugares designados.
- Conta só existe com e-mail verificado; código de 6 dígitos é de uso único, guardado só como hash, com expiração e limite de tentativas.
- Frontend: sem imports entre features, sem barrel files.

## Documentos de referência

`docs/ARQUITETURA.md` (camadas, contratos, ADRs) · `docs/SEGURANCA.md` (modelo de ameaças + checklist) · `docs/DESIGN.md` (identidade e tokens) · `docs/BANCO-DE-DADOS.md` (estratégia multi-SQL) · `docs/ROADMAP.md` (fases) · `docs/PESQUISA.md` (pesquisa de 09/2026 com fontes)
