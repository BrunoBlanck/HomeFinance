---
name: migracao-db
description: Cria ou altera migracoes de banco do HomeFinance garantindo compatibilidade com todos os dialetos SQL suportados (PostgreSQL, MySQL, SQLite, SQL Server). Use para qualquer mudanca de schema, tabela nova ou alteracao de coluna.
---

# Migração de banco de dados

Os argumentos descrevem a mudança de schema. Este projeto roda em **qualquer banco SQL** — portabilidade não é opcional.

## Passos

> **A partir do ADR-008 (09/09/2026) o projeto usa GORM com `AutoMigrate`** — não existem arquivos de migração SQL nem goose. O schema é definido pelas **structs Go com tags `gorm:"..."`** em `backend/internal/platform/storage/gormstore/`.

1. Leia `docs/BANCO-DE-DADOS.md` (estratégia de portabilidade, convenções de tipos e nomes) e os modelos existentes em `backend/internal/platform/storage/gormstore/`.

2. Lance `arquiteto-dados` (ou siga exatamente o prompt dele) para desenhar a mudança:
   - Altere/crie a struct do modelo com as tags `gorm:"..."` corretas e registre-a na lista do `AutoMigrate`.
   - Apenas o subconjunto portátil de tipos definido no doc; se um dialeto exigir variante, isole-a num `switch` de dialeto dentro do `gormstore`.
   - ⚠️ **`AutoMigrate` não remove nem renomeia coluna.** Se a mudança for destrutiva (drop/rename/mudança de tipo incompatível), diga isso em voz alta e escreva o passo manual explícito — não finja que o `AutoMigrate` cobre.

3. **Checklist**:
   - [ ] Tipos portáteis (dinheiro `BIGINT` centavos, datas UTC, texto conforme doc)
   - [ ] `household_id` presente em tabela de dados de usuário, com FK e índice
   - [ ] Índices para toda FK e filtros frequentes (tags `index`/`uniqueIndex`)
   - [ ] Sem triggers, stored procedures ou features exclusivas de um banco
   - [ ] Sem hooks de callback do GORM com regra de negócio
   - [ ] Interfaces de repositório atualizadas junto com o schema

4. **Verificar** — rode o `AutoMigrate` contra SQLite no mínimo (e Postgres via testcontainers, se disponível), **partindo de banco vazio e de banco já povoado**, e reporte a saída real. Atualize a seção de schema em `docs/BANCO-DE-DADOS.md`.

5. Mudança de schema toca dados financeiros: termine lançando `revisor-seguranca` sobre os modelos e repositórios alterados.
