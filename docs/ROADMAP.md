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
- [x] Contrato inicial `backend/api/openapi.yaml` versionado — o gerador entrou **só do lado TypeScript** (ADR-015): `npm run api:gen` produz `frontend/src/api/schema.gen.ts` e `npm run api:check` quebra o build se a spec e os tipos divergirem. Handlers e tipos Go seguem manuais, com `cmd/api/routes_test.go` garantindo (método, path)
- [x] Middlewares: recover, request-id, slog, headers de segurança, CORS por allowlist, CSRF por `Origin`, limite de corpo, rate limit por IP e por conta (`x/time/rate`)
- [x] Health check — `GET /api/v1/health` (liveness, não toca o banco) e `/health/ready` (readiness com ping)
- [x] CI local: `backend/scripts/check.ps1` e `check.sh` (build · vet · test -race · build sem CGo · govulncheck · gosec)
- [x] Hook de formatação automática — `gofmt` em PostToolUse (`.claude/hooks/gofmt-backend.sh`) **e** etapa `gofmt -l .` no `check.ps1`/`check.sh`; `.gitattributes` fixa LF para o `gofmt -l` não acusar CRLF de clone novo (E0, 12/09/2026)

## Fase 2 — Autenticação e casas

- [x] Registro/login (Argon2id com semáforo de concorrência, rate limit, mensagens seguras, política de senha com denylist embutida)
- [x] **Verificação obrigatória de e-mail por código de 6 dígitos** + "esqueci minha senha" pelo mesmo mecanismo (ADR-009, §1.1 de docs/SEGURANCA.md)
- [x] **Tentativa de cadastro com `registrationToken`** — fecha o *pre-hijacking* de conta (ADR-014); o código pertence a quem o pediu, não ao endereço
- [x] Interface `Mailer` — console em dev, SMTP em produção, envio **assíncrono** com drenagem no shutdown
- [x] JWT access (`golang-jwt/v5`) + **refresh opaco** com rotação, detecção de reúso e revogação de família (cookies HttpOnly, prefixo `__Host-`)
- [~] Households e memberships (papéis owner/member) — **convites ficaram de fora** desta entrega; a casa ganhou `timezone` e `currency` na E1 (ADR-019)
- [x] Middleware de autorização por household (`hid` do token revalidado contra a membership real)
- [x] Audit log dos eventos de conta (escrita)

## Fase 3 — Fundação do frontend

- [x] Vite 8 + React 19 + TS estrito (`noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`); Biome; **TanStack Router + Query v5**; **tipos TS gerados do OpenAPI** (ADR-015) — nenhum payload da API é mais escrito à mão, e `role` deixou de ser `string` para ser `'owner' | 'member'`
- [x] `tokens.css` a partir de docs/DESIGN.md (OKLCH, `@layer`) + componentes base (Button, TextField, PasswordField, **CodeInput**, Alert, Panel, Spinner, Skeleton, Logo, 13 ícones próprios); menu de usuário com **Popover API**
- [x] Fluxo de autenticação completo (react-hook-form + Zod 4) — entrar / criar conta / **verificar código de 6 dígitos** / esqueci minha senha / redefinir senha
- [x] Guard de rotas + layout base do app + Home — testes de componente em **Vitest + jsdom**; o Browser Mode do ADR-007 foi **substituído pelo Playwright** como única camada de navegador real (ADR-022), com polyfill da Popover API para o jsdom
- [x] Playwright instalado e rodando contra a **API Go real** (`npm run e2e`) — 4 casos verdes: cadastro com código de 6 dígitos, saída e reentrada; senha errada com resposta que não enumera conta; código errado recusado; rota autenticada sem sessão. O código sai do stdout do mailer de console, sem nenhuma rota de teste no servidor
- [x] Telas sem teste cobertas: esqueci minha senha (7 casos), redefinir senha (9), Home (10) e menu de usuário (13) — criar conta já tinha. Suíte do frontend em **105 testes**

## Fase 4 — Núcleo financeiro

- [x] **Contas e categorias — entrega E1, spec 0003 (13/09/2026).** Schema v2 (`accounts`, `categories`, `households.timezone/currency`) por AutoMigrate, com teste de migração partindo de banco v1 **povoado** · CRUD completo com arquivar/desarquivar e exclusão lógica · saldo derivado (ADR-017) · árvore de exatamente 2 níveis (ADR-017b) · semente de 12 categorias pt-BR na mesma transação da criação da casa · 14 rotas novas, todas com 404 para recurso de outra casa · auditoria de toda escrita financeira, dentro da transação (§4.7) · casca do app com navegação e mês na URL no fuso da casa · telas `/contas` e `/categorias`
- [~] **Lançamentos — entrega E2, spec 0004 (em andamento desde 16/09/2026).** Nesta entrega entra o domínio **enxuto**: `transactions` com índices, listagem por mês e conta com cursor e `summary`, exclusão lógica, saldo derivado passando a somar os lançamentos (fecha o ADR-017) e os `UsageChecker` de conta e categoria que a spec 0003 deixou em aberto. **Falta para a linha fechar:** criação rápida, edição e filtros densos, que foram deliberadamente adiados para a E2b
- [~] **Importação de extratos e faturas (C6 e Nubank) — entrega E2, spec 0004 (16/09/2026).** Fluxo de duas fases (enviar → revisar → confirmar) com staging no banco por 24 h · CSV solto e ZIP com senha (ZipCrypto decifrado sem dependência nova — ADR-024) · parser como plugin por (instituição × documento) com detecção por cabeçalho · deduplicação em camadas com chave canônica, ordinal de ocorrência e índice único como árbitro (ADR-025) · conceito de **fatura de cartão** com competência separada de caixa (ADR-023, que supera o ADR-019 (c) e (d)) · telas `/lancamentos` e `/importar`. **Backend de importação completo (17/09/2026):** os quatro parsers — extrato e fatura do Nubank e do **C6** — estão implementados, registrados e testados; arquivo do C6 é reconhecido pelo cabeçalho como qualquer outro (o extrato do C6 usa o modelo de duas colunas Entrada/Saída e traz preâmbulo de 8 linhas; §7.3 da spec 0004). **Falta para a linha fechar:** só o pente-fino de ponta a ponta das telas `/lancamentos` e `/importar`
- [ ] Contas recorrentes com vencimentos do mês (pago/pendente/atrasado)
- [ ] Dashboard do mês: saldo, entradas × saídas, próximos vencimentos

## Fase 5 — Orçamentos e relatórios

- [ ] Orçamentos por categoria/mês com acompanhamento
- [~] Relatórios: evolução mensal, por categoria, por conta (skill `dataviz`) — **por categoria entregue em 17/09/2026** (entrega **E6a**, ADR-027; primeira fatia vertical da E6, sem spec própria): `GET /api/v1/reports/by-category?month&kind` servido pelo pacote de leitura pura `internal/report` (uma consulta `GROUP BY category_id`, dobra folha→grupo em Go, balde "Sem categoria" explícito e percentual em **pontos-base inteiros apurados no servidor**, nunca `float`), tela `/relatorios/categorias` com `DonutChart` SVG próprio (ADR-021) e a tabela como fonte da verdade · revisão de segurança **APROVADO**, três achados baixos corrigidos e o delta reaprovado. **Falta para a linha fechar:** evolução mensal, relatório por conta e a exportação — seguem na E6 (spec 0009, ainda não escrita), e a suíte do relatório ainda não rodou contra PostgreSQL
- [ ] Exportação (CSV)

## Fase 6 — Acabamento e produção

- [~] Playwright nos fluxos críticos — **7 arquivos de spec, 49 casos definidos** (contagem estática de 17/09/2026, incluindo o projeto `sessao.setup.ts`, que o Playwright conta como caso e roda como dependência): aos **17 casos verdes da E1** (autenticação + contas + categorias + casca) somaram-se importação, recuperação do C6, atalho de categorização e palavras-chave/transferências (E2/E2c, **em andamento**) e o **relatório por categoria** (E6a, 17/09/2026 — `relatorio-por-categoria.spec.ts`, 8 casos; última execução verde junto com `importacao.spec.ts`: **13 passed em 1,2 min**, mais **20 passed** nas telas migradas para o hook de foco). A suíte **inteira** não foi executada de ponta a ponta nesta data — o número acima é de casos escritos, não de casos verdes. Falta cobrir: criação/edição manual de lançamento (E2b), **exclusão** de lançamento refletindo no relatório, contas fixas, orçamentos, evolução mensal, relatório por conta e exportação — conforme cada entrega nasce
- [ ] Workflow `auditoria-seguranca` completo no projeto inteiro
- [ ] Suíte de integração multi-banco com testcontainers-go (Postgres, MySQL, SQLite, MSSQL)
- [ ] Passkeys (WebAuthn) via `go-webauthn/webauthn` como login preferencial
- [ ] Build de produção, TLS, backups, `.env.example` final e guia de deploy
