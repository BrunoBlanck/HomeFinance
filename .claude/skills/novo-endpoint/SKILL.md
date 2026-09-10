---
name: novo-endpoint
description: Cria um endpoint na API Go do HomeFinance seguindo o padrao de camadas e o checklist de seguranca do projeto - handler, service, repository, testes e revisao. Use quando o pedido for adicionar uma rota/endpoint especifica ao backend.
---

# Novo endpoint da API

Os argumentos descrevem o endpoint desejado (recurso, método, comportamento). Se faltar informação essencial (contrato de resposta, regras de negócio), pergunte antes.

## Passos

1. **Contrato primeiro** — defina e mostre: método + rota (`/api/v1/...`), request, response, códigos de erro no formato padrão do projeto (ver `docs/ARQUITETURA.md`). Confirme silenciosamente com o padrão existente — rotas semelhantes já implementadas são a referência.

2. **Implementação em camadas** — lance `dev-backend-go` (ou implemente seguindo exatamente o prompt dele em `.claude/agents/dev-backend-go.md`):
   - Atualize PRIMEIRO o contrato `backend/api/openapi.yaml` (spec-first, ADR-006) e gere os tipos com oapi-codegen
   - `internal/<dominio>/handler.go` — parse + validação de entrada + tradução de erros
   - `internal/<dominio>/service.go` — regra de negócio
   - `internal/<dominio>/repository.go` (interface) + implementação em `internal/platform/storage/<dialeto>` — SQL parametrizado e portátil
   - Registro da rota com middleware de autenticação/autorização aplicado

3. **Checklist do endpoint** (verifique item a item antes de seguir):
   - [ ] Autenticação exigida (ou justificativa explícita de rota pública)
   - [ ] Toda query filtra por `household_id` do token
   - [ ] Entrada validada: tipos, tamanhos, faixas; dinheiro em centavos int64
   - [ ] SQL 100% parametrizado (allowlist para ordenação/filtros dinâmicos)
   - [ ] Erros genéricos ao cliente; detalhes só em `slog` sem dados sensíveis
   - [ ] Testes: handler (httptest) + service + casos de abuso (ID de outra casa ⇒ 404/403)

4. **Verificar** — `go build ./...`, `go vet ./...`, `go test -race ./...` com saída real.

5. **Revisão de segurança** — lance `revisor-seguranca` sobre os arquivos criados/alterados. Só conclua com veredito APROVADO.
