# Lições de backend — correções permanentes do usuário

Correções do usuário sobre backend (Go, API, banco de dados, segurança do servidor). Ditas uma única vez, valem para sempre. Regras de manutenção e estrutura: ver `LICOES.md`. Registro somente via skill `/aprender`.

## Lições registradas

*(nenhuma lição de backend registrada ainda)*

- **[2026-09-09] A camada de persistência do backend é GORM, com AutoMigrate; SQLite é o banco de desenvolvimento.** Por quê: o usuário decidiu em 09/09/2026 usar GORM em vez de `database/sql` + sqlx, revertendo o ADR-002 original — o schema passa a ser definido pelas structs Go e evoluído por `AutoMigrate` no boot, sem goose. Como aplicar: todo acesso a dados usa `gorm.io/gorm` com os drivers oficiais dos 4 dialetos (SQLite em dev via `github.com/glebarez/sqlite`, puro Go); nunca introduzir sqlx, goose ou SQL cru; GORM fica confinado à implementação do repositório — `*gorm.DB` nunca aparece em service ou handler, que só conhecem a interface do domínio. Ver ADR-008 em `docs/ARQUITETURA.md`.

- **[2026-09-09] Cadastro exige e-mail verificado, e todo fluxo sensível de conta usa código de 6 dígitos enviado por e-mail.** Por quê: o usuário quer prova de posse do e-mail antes de a conta existir de fato, e a mesma mecânica para recuperação de senha. Como aplicar: registro cria o usuário como não verificado e só libera login após a confirmação do código; "esqueci minha senha" e demais operações sensíveis emitem código numérico de 6 dígitos gerado com `crypto/rand`, guardado **apenas como hash HMAC** (nunca em texto), de uso único, com expiração curta, limite de tentativas e rate limit; respostas nunca revelam se o e-mail existe. Detalhes normativos na seção 1.1 de `docs/SEGURANCA.md`.
