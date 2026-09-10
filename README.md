# HomeFinance

Aplicação de controle financeiro doméstico — receitas, despesas, contas recorrentes, orçamentos e relatórios de uma casa.

- **Backend:** Go (API REST, segurança em primeiro lugar)
- **Frontend:** React + TypeScript (design próprio, componentes escritos para este projeto)
- **Banco de dados:** dinâmico — funciona com qualquer banco SQL (PostgreSQL, MySQL, SQLite, SQL Server)

## Estado atual

O repositório está na **fase de estruturação**: a equipe de agentes de IA, as skills de trabalho e toda a documentação de arquitetura, segurança e design já estão definidas. O desenvolvimento segue as fases descritas em [`docs/ROADMAP.md`](docs/ROADMAP.md).

## Como trabalhar neste projeto (com Claude Code)

| Comando               | O que faz                                                        |
|-----------------------|------------------------------------------------------------------|
| `/aprender`           | Registra uma correção sua como regra permanente (LICOES.md) — dita uma vez, vale para sempre |
| `/especificar`        | Spec curta e versionada antes de features grandes (spec-driven)   |
| `/nova-feature`       | Ciclo completo: planejar → implementar → testar → revisar segurança |
| `/novo-endpoint`      | Cria um endpoint Go com o checklist de segurança aplicado         |
| `/novo-componente`    | Cria um componente React seguindo o design system próprio         |
| `/migracao-db`        | Cria migrações compatíveis com todos os dialetos SQL suportados   |
| `/revisao-seguranca`  | Revisão de segurança do que foi alterado (obrigatória antes de entregar) |

A documentação de referência vive em [`docs/`](docs/) e as regras do projeto em [`CLAUDE.md`](CLAUDE.md).
