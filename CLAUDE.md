# HomeFinance — fluxo de trabalho com Claude Code

@AGENTS.md
@LICOES.md
@LICOES-BACKEND.md
@LICOES-FRONTEND.md

As regras universais do projeto estão em `AGENTS.md` e as correções permanentes do usuário nos arquivos de lições (gerais, backend e frontend), todos importados acima. Este arquivo cobre apenas o fluxo com a equipe de IA.

## Correções são permanentes (regra de ouro do fluxo)

O usuário **nunca** deve precisar corrigir a mesma coisa duas vezes. Sempre que ele corrigir um comportamento, apontar um erro ou expressar uma preferência ("sempre...", "nunca...", "não é assim", "eu já disse"), invoque **imediatamente** a skill `/aprender` — sem pedir permissão — para registrar a regra no arquivo de lições da área certa (`LICOES-BACKEND.md`, `LICOES-FRONTEND.md`, ou `LICOES.md` para geral/processo), propagá-la aos agentes/skills/docs afetados e salvá-la na memória. As lições têm força de regra inegociável em toda sessão.

## Equipe de IA — quando usar cada agente

| Agente               | Papel                                                                  |
|----------------------|------------------------------------------------------------------------|
| `arquiteto`          | Planeja features, decide trade-offs, quebra o trabalho em tarefas       |
| `dev-backend-go`     | Implementa a API Go seguindo docs/SEGURANCA.md à risca                  |
| `dev-frontend-react` | Implementa UI seguindo docs/DESIGN.md (componentes próprios)            |
| `arquiteto-dados`    | Modelagem, migrações e a camada de banco agnóstica de dialeto           |
| `designer-ui`        | Direção visual, identidade própria, revisão anti-"cara de IA"           |
| `qa-testes`          | Escreve e roda testes; valida critérios de aceite                       |
| `revisor-seguranca`  | Revisão adversarial de segurança — **obrigatório antes de qualquer entrega** |

## Fluxo de trabalho obrigatório

Toda feature segue este ciclo (a skill `/nova-feature` orquestra tudo):

1. **Especificar** — feature grande ou ambígua começa com `/especificar` (spec curta versionada em `docs/specs/`).
2. **Planejar** — `arquiteto` define escopo, contratos e critérios de aceite a partir da spec.
3. **Implementar** — `dev-backend-go` / `dev-frontend-react` / `arquiteto-dados` conforme a tarefa.
4. **Testar** — `qa-testes` cobre casos felizes, bordas e casos de abuso.
5. **Revisar segurança** — `revisor-seguranca` (ou `/revisao-seguranca`). **Nenhuma entrega é concluída sem veredito APROVADO.** Achado crítico/alto bloqueia até correção.

Skills disponíveis: `/nova-feature` · `/especificar` · `/novo-endpoint` · `/novo-componente` · `/migracao-db` · `/revisao-seguranca` · workflow `auditoria-seguranca` para auditoria profunda.

## Regras do fluxo

- Ao construir qualquer tela ou componente, carregar a skill `frontend-design` antes de escrever código; validar a UI real com Playwright quando o app estiver rodável.
- Decisões estruturais novas viram ADR em docs/ARQUITETURA.md (pelo `arquiteto`).
- Reportar resultados reais (saída de testes, veredito literal), nunca presumidos.
- Documentação para consulta sob demanda: ler os docs/ relevantes à tarefa, não todos de uma vez.
