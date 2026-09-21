# PLANOS — plano do sistema HomeFinance

**Última revisão:** 17/09/2026 · **Escopo:** o sistema inteiro, com foco no que ainda **não** existe (domínio financeiro).

> Este documento **pensa o sistema**. Ele não é normativo e não vira código direto: o que vale para
> implementar é a spec da entrega (`docs/specs/`), e o que vale como regra é `AGENTS.md` +
> `docs/SEGURANCA.md` + os arquivos de lições. Divergência entre este plano e um documento normativo
> se resolve a favor do normativo, e o plano é corrigido depois.

## 0. Como este documento se encaixa

| Documento | Pergunta que responde | Dono |
|---|---|---|
| **PLANOS.md** (este) | *Como o sistema deve ser, por quê, e em que ordem construir* | usuário + `arquiteto` |
| `docs/ROADMAP.md` | *Em que pé está cada fase* (status, checkboxes) | equipe |
| `docs/specs/NNNN-*.md` | *O que exatamente entra nesta entrega* (normativo) | `arquiteto` / `designer-ui` |
| `docs/ARQUITETURA.md` | *Que decisão estrutural já foi tomada e por quê* (ADRs) | `arquiteto` |
| `docs/SEGURANCA.md` | *O que é proibido e o que é obrigatório* | `revisor-seguranca` |
| `docs/BANCO-DE-DADOS.md` | *Como o schema se mantém portátil nos 4 dialetos* | `arquiteto-dados` |
| `docs/DESIGN.md` | *Como o produto se parece e se comporta* | `designer-ui` |

Regra de uso: **decisão nasce aqui como proposta**, é confirmada pelo usuário, e só então vira ADR
(se for estrutural) e spec (se for entrega). Nada deste arquivo autoriza implementação por si só.

---

## 1. O produto

### 1.1 Para quem
Uma casa — tipicamente duas pessoas adultas dividindo as mesmas contas, eventualmente com mais
membros. Não é ferramenta de contador, não é ERP, não é app de investimentos.

### 1.2 O trabalho que o app faz
1. **Registrar rápido.** Lançar uma despesa em menos de 10 segundos, do celular, sem pensar.
2. **Responder "como estamos este mês?"** em uma tela, sem montar relatório.
3. **Não deixar vencer conta.** As contas fixas do mês visíveis com pago / pendente / atrasado.
4. **Mostrar para onde o dinheiro foi.** Por categoria e por mês, com honestidade.
5. **Ser de duas pessoas.** O que um lança o outro vê, com histórico de quem fez o quê.

### 1.3 O que o produto explicitamente **não** é (v1)
Sem multimoeda, sem conversão de câmbio · ~~sem importação de OFX/extrato bancário e~~ **sem OFX** e
sem Open Finance · sem **conciliação bancária automática** · ~~sem investimentos, patrimônio ou metas
de longo prazo~~ **sem patrimônio, posição, rentabilidade ou metas de longo prazo** · sem app nativo
(web responsiva, instalável no futuro) · sem divisão de despesas entre membros ("quem deve quanto a
quem") · sem anexo de comprovante (fica no backlog, ver §14).

> **Correção de 16/09/2026 (decisão do usuário) — o histórico fica, o texto muda.** A linha original
> dizia *"sem importação de OFX/extrato bancário"*, e isso deixou de valer: a **E2 importa extrato e
> fatura em CSV e em ZIP com senha, de C6 e Nubank** (spec 0004, ADR-024). O que **continua** fora do
> v1 é o **OFX**, o **Open Finance** e a **conciliação automática** — na E2 o pareamento entre extrato
> e fatura é *proposto* na tela de revisão e confirmado pelo usuário, nunca decidido pelo sistema.

> **Correção de 17/09/2026 (decisão do usuário) — o histórico fica, o texto muda.** A linha original dizia
> *"sem investimentos, patrimônio ou metas de longo prazo"*, e a **primeira** palavra deixou de valer: a
> **E7 entrega o controle de FLUXO de investimento** — aporte e resgate marcados por **natureza de
> categoria** (`investment`/`redemption`), tela `/investimentos` com o mês, o ano até o mês e os últimos
> 12 meses, e detecção retroativa por palavra-chave (spec 0006, ADR-029). O que **continua** fora do v1
> é **patrimônio, saldo investido acumulado, posição, rentabilidade, cotação, ativo, corretora** e
> **metas de longo prazo**: a tela responde *quanto foi investido*, nunca *quanto eu tenho*, e não existe
> nenhum número acumulado além dos dois do ano corrente. A entrega não cria tipo de lançamento novo,
> conta de carteira nem coluna nova — o aporte continua sendo dinheiro que **sai** da conta (ADR-029).

### 1.4 Como saber que deu certo
Critérios de produto, não técnicos: lançar despesa em ≤ 3 toques a partir do painel · o mês corrente
carrega em ≤ 1 s com 5 anos de histórico · nenhuma conta fixa vence sem estar visível como atrasada ·
o casal usa por 3 meses seguidos sem planilha paralela.

---

## 2. Estado atual — 09/09/2026

Honestidade primeiro: o que existe é **fundação e conta de usuário**. Não existe nenhuma linha do
domínio financeiro.

### 2.1 Pronto e coberto por teste
- **Backend:** config fail-fast · GORM + AutoMigrate por `DB_DRIVER` (ADR-008) · UnitOfWork com
  transação propagada por contexto · middlewares (recover, request-id, slog, headers, CORS por
  allowlist, CSRF por `Origin`, limite de corpo, rate limit por IP e por conta) · health/ready ·
  auth completo (registro → OTP → verificação → login → refresh opaco com rotação, detecção de reúso
  e revogação de família → logout → esqueci/redefinir senha) · `registrationToken` fechando
  *pre-hijacking* (ADR-014) · mailer assíncrono (console/SMTP) · audit log de conta (escrita) ·
  janitor de expurgo · `GET /me`.
- **Domínios existentes:** `auth`, `user`, `household`, `audit`, `session`, `id`.
- **Frontend:** tokens OKLCH + `@layer` · 13 ícones próprios · componentes base (Button, TextField,
  PasswordField, CodeInput, Alert, Panel, Spinner, Skeleton, Logo, TextLink, FieldShell) · fluxo de
  autenticação inteiro com RHF + Zod · TanStack Router + Query.

### 2.2 Dívidas conhecidas que o plano precisa endereçar
| # | Dívida | Impacto se ficar | Onde resolvo | Status |
|---|---|---|---|---|
| DV1 | **ADR-011:** OpenAPI versionado mas handlers/tipos escritos à mão | o custo cresce a cada endpoint; o domínio financeiro adiciona ~35 rotas | decidir **antes da E2** (§5, D1) | ✅ **paga na E0** — gerador ligado do lado TS (ADR-015) |
| DV2 | Convites e gestão de membros ficaram fora da Fase 2 | a promessa "multiusuário por casa" não se cumpre | E7 (§9) | aberta — E7 subiu para depois da E2 (D12) |
| DV3 | Telas sem teste: criar conta, esqueci/redefinir senha, Home, menu do usuário | regressão silenciosa na área mais sensível | E0 | ✅ **paga na E0** — 105 testes no frontend |
| DV4 | Playwright previsto no `AGENTS.md`, nunca instalado | daqui pra frente toda entrega tem fluxo E2E que só o Playwright pega | E0 | ✅ **paga na E0** — 4 E2E contra a API Go real |
| DV5 | Suíte multi-banco com testcontainers não existe (só SQLite) | o requisito "qualquer SQL" é hipótese, não fato verificado | E8, com gatilho antecipado (§13, R2) | aberta |
| DV6 | Hook de gofmt/goimports em PostToolUse | ruído de formatação em revisão | E0 (barato) | ✅ **paga na E0** — hook + etapa no check + `.gitattributes` |

**DV7 — achada durante a E0, e é de processo:** o `go test -race` do `check` servia resultado de
**cache** (`ok (cached)`), então o gate podia aparecer verde numa sessão em que o detector nem
chegava a subir. Corrigido: `-count=1` obrigatório na etapa de teste, mais uma sonda que distingue
"o ThreadSanitizer não subiu neste ambiente" de "achei um data race" e imprime **PASSOU COM
RESSALVA** em vez de **TUDO PASSOU**. A causa local era o sandbox de memória do shell — em terminal
normal o detector funciona, e foi assim que a E0 foi verificada.

---

## 3. Modelo de domínio — detalhado

O modelo conceitual (`docs/ARQUITETURA.md` §Domínio) fica de pé. Aqui ele ganha campos, invariantes
e as decisões que faltavam.

### 3.1 Account — conta
Onde o dinheiro está. Carteira, conta bancária, poupança, cartão.

| Campo | Tipo | Regra |
|---|---|---|
| `id` | UUID v7 | gerado no service |
| `household_id` | ref | **sempre do token**, nunca do cliente |
| `name` | varchar(80) | obrigatório; único por casa entre as não arquivadas (normalizado) |
| `kind` | varchar(20) | `cash` · `checking` · `savings` · `credit_card` · `other` |
| `opening_balance_cents` | int64 | saldo no dia em que a conta entrou no app; pode ser negativo |
| `opening_date` | date | data civil do saldo inicial |
| `archived_at` | timestamp NULL | arquivar ≠ excluir (§4.4) |
| `deleted_at` | timestamp NULL | soft delete, só quando nunca usada |

**Invariantes.** Saldo é **derivado**, nunca coluna: `opening_balance_cents` + soma dos lançamentos
da conta (§5, D3) · `kind = credit_card` no v1 é apenas rótulo (sem fatura — §5, D6) · conta
arquivada não aparece em seletor de lançamento novo, mas continua nos relatórios e no histórico.

### 3.2 Category — categoria
| Campo | Tipo | Regra |
|---|---|---|
| `id` / `household_id` | | idem |
| `parent_id` | ref NULL | **no máximo 2 níveis** (§5, D4) |
| `name` | varchar(60) | único entre irmãos, normalizado |
| `kind` | varchar(10) | `income` · `expense` — definido no nível 1 e **herdado** pelos filhos |
| `archived_at` / `deleted_at` | | idem accounts |

**Invariantes.** Filho não muda de `kind` nem vira raiz de outra árvore · lançamento pode apontar
para grupo **ou** folha; relatório soma o filho no pai · categoria em uso não se exclui, se arquiva ·
a casa nasce com um conjunto enxuto de categorias pt-BR editáveis (§5, D5).

### 3.3 Transaction — lançamento
O coração do sistema.

| Campo | Tipo | Regra |
|---|---|---|
| `id` / `household_id` | | idem |
| `kind` | varchar(12) | `income` · `expense` · `transfer_out` · `transfer_in` |
| `account_id` | ref | obrigatório; **validado como pertencente à mesma casa** |
| `category_id` | ref NULL | obrigatório em `income`/`expense`; **proibido** em transferência |
| `amount_cents` | int64 | sempre **positivo**; o sinal vem do `kind` (§4.1) |
| `description` | varchar(140) | livre, opcional |
| `description_norm` | varchar(140) | minúsculo sem acento, gerado na aplicação — busca portátil (§6) |
| `occurred_on` | date | **data civil**, sem hora e sem fuso (§4.2) |
| `year_month` | char(7) | `YYYY-MM` derivado de `occurred_on` — agregação portátil (§6) |
| `transfer_group_id` | varchar(36) NULL | par de transferência (§5, D2) |
| `created_by` | ref user | quem lançou; aparece na UI ("lançado por Ana") |
| `created_at` / `updated_at` / `deleted_at` | | soft delete obrigatório |

**Invariantes.** Valor zero e negativo são rejeitados · teto de valor definido em §4.5 ·
`year_month` e `description_norm` são recalculados em toda escrita, nunca aceitos do cliente ·
excluir é sempre lógico · editar transferência edita o par inteiro.

### 3.4 RecurringBill — conta fixa
A **regra**, não as ocorrências.

| Campo | Tipo | Regra |
|---|---|---|
| `id` / `household_id` | | idem |
| `name` | varchar(80) | "Aluguel", "Luz" |
| `category_id` | ref | obrigatório, `kind = expense` |
| `account_id` | ref NULL | conta sugerida no pagamento |
| `amount_cents` | int64 | valor esperado; a ocorrência pode divergir |
| `due_day` | int 1..31 | dia do vencimento; mês curto usa o último dia (§4.2) |
| `starts_on` / `ends_on` | date / date NULL | janela de vigência |
| `archived_at` / `deleted_at` | | idem |

### 3.5 BillOccurrence — o mês de uma conta fixa
As ocorrências **não são materializadas**: são projetadas da regra e só existem em tabela quando
houve um fato (pagou, pulou, ajustou) — §5, D7.

| Campo | Tipo | Regra |
|---|---|---|
| `id` / `household_id` | | idem |
| `bill_id` | ref | |
| `competence_month` | char(7) | `YYYY-MM`; **único** por `(bill_id, competence_month)` — é o que torna "pagar" idempotente |
| `status` | varchar(10) | `paid` · `skipped` (pendente/atrasado **não** são gravados: derivam) |
| `amount_cents_override` | int64 NULL | a luz veio diferente neste mês |
| `due_date_override` | date NULL | prorrogou |
| `transaction_id` | ref NULL | preenchido quando `paid` |

**Derivação de status** (na leitura, no fuso da casa): existe linha `paid` → **pago** · linha
`skipped` → **pulado** · sem linha e vencimento < hoje → **atrasado** · sem linha e vencimento ≥ hoje
→ **pendente**. A ocorrência virtual é identificada na API por `billId + competenceMonth`, nunca por
um id sintético que não existe no banco.

### 3.6 Budget — orçamento
| Campo | Tipo | Regra |
|---|---|---|
| `id` / `household_id` | | idem |
| `category_id` | ref | categoria de despesa (grupo ou folha) |
| `year_month` | char(7) | único por `(household_id, category_id, year_month)` |
| `limit_cents` | int64 | > 0 |

**Invariantes.** Sem acúmulo de sobra entre meses no v1 (§5, D8) · orçamento em grupo compara com a
soma do grupo inteiro (pai + filhos) · orçamento é do mês, e "copiar do mês anterior" é uma ação
explícita, nunca automática.

### 3.7 Household — o que falta nela
Ganha `timezone` (default `America/Sao_Paulo`) e `currency` (`BRL` fixo no v1, campo existe para não
exigir mudança destrutiva depois) — §5, D9.

### 3.8 Schema v2 (delta sobre o v1 já no ar)
```
accounts          (id, household_id, name, kind, opening_balance_cents, opening_date,
                   archived_at, created_at, updated_at, deleted_at)
categories        (id, household_id, parent_id NULL, name, kind,
                   archived_at, created_at, updated_at, deleted_at)
transactions      (id, household_id, kind, account_id, category_id NULL, amount_cents,
                   description, description_norm, occurred_on, year_month,
                   transfer_group_id NULL, created_by, created_at, updated_at, deleted_at)
recurring_bills   (id, household_id, name, category_id, account_id NULL, amount_cents,
                   due_day, starts_on, ends_on NULL, archived_at, created_at, updated_at, deleted_at)
bill_occurrences  (id, household_id, bill_id, competence_month, status,
                   amount_cents_override NULL, due_date_override NULL, transaction_id NULL,
                   created_at, updated_at)      [UNIQUE(bill_id, competence_month)]
budgets           (id, household_id, category_id, year_month, limit_cents,
                   created_at, updated_at)      [UNIQUE(household_id, category_id, year_month)]
households        + timezone, currency
invitations       (E7 — id, household_id, email_norm, role, code_hash, invited_by,
                   expires_at, accepted_at, attempts, created_at)
```

**Índices planejados** (todo composto começa por `household_id` — é o isolamento):
`transactions(household_id, occurred_on DESC, id DESC)` cursor da listagem ·
`transactions(household_id, account_id, occurred_on)` extrato e saldo ·
`transactions(household_id, category_id, year_month)` relatório e orçamento ·
`transactions(household_id, transfer_group_id)` par ·
`bill_occurrences(household_id, competence_month)` projeção do mês ·
índice em toda coluna de referência (não há FK física — ADR-013).

---

## 4. Regras transversais

### 4.1 Dinheiro
`int64` em centavos em **toda** camada, incluindo JSON (`amountCents`) e formulário. Float é
proibido — inclusive em cálculo intermediário de percentual: a divisão só acontece na formatação.
Valor é sempre positivo e o significado vem do `kind`; a UI mostra o sinal. Nenhum arredondamento
existe no v1 porque nenhuma operação divide dinheiro (rateio e parcelamento entram no backlog **com**
a regra de distribuição de sobra definida — §14).

### 4.2 Datas, fuso e mês
- `occurred_on`, `due_date`, `opening_date` são **datas civis** (sem hora, sem fuso). Timestamps de
  auditoria (`created_at`) seguem UTC.
- "Hoje" e as bordas do mês são calculados **no fuso da casa**, nunca no do servidor nem no do
  navegador. Isso decide se uma conta está atrasada — é regra de negócio, não formatação.
- `due_day = 31` em fevereiro cai no último dia do mês. A regra é *clamp para o último dia*, aplicada
  em um único lugar do domínio e testada em ano bissexto.
- Mês na API e na URL é sempre `YYYY-MM`, validado; intervalo de relatório tem teto (§4.5).

### 4.3 Competência = caixa (v1)
O mês de um lançamento é o do `occurred_on`. Não existe competência separada de caixa no v1 — a
distinção só passa a importar com fatura de cartão, que está fora (§5, D6). Quando entrar, entra como
coluna nova (`competence_month` em `transactions`), o que o AutoMigrate faz sem quebrar.

### 4.4 Excluir, arquivar, restaurar
| Situação | Comportamento |
|---|---|
| Lançamento | soft delete sempre; sai de saldos e relatórios na hora; histórico preservado |
| Conta/categoria **nunca usada** | exclusão lógica permitida |
| Conta/categoria **em uso** | exclusão recusada (422 com motivo) → oferecer **arquivar** |
| Arquivado | invisível em seletores de criação, visível em histórico e relatórios |
| Restaurar | reverter arquivamento é permitido; reverter exclusão de lançamento **não** é exposto no v1 |

### 4.5 Limites e sanidade (barreira contra abuso e contra dedo errado)
Valor: `1 .. 99_999_999_999` centavos (R$ 999.999.999,99) · `occurred_on`: entre 01/01/1970 e
hoje + 10 anos · descrição: 140 caracteres · listagem: `limit` ≤ 100, default 50 · relatório:
intervalo ≤ 24 meses · categorias: ≤ 200 por casa, profundidade 2 · contas: ≤ 50 por casa ·
contas fixas: ≤ 200 por casa. Todo limite recusa com erro claro, nunca trunca em silêncio.

### 4.6 Duas pessoas ao mesmo tempo
V1 assume **último a escrever ganha**, com duas exceções onde isso causaria dano real:
1. **Pagar conta fixa** é idempotente pela chave `(bill_id, competence_month)` — dois cliques, um
   pagamento. Sem isso, duplo toque em conexão ruim gera lançamento duplicado.
2. **Transferência** grava o par dentro do UnitOfWork; meia transferência não existe.

Controle de versão otimista (coluna `version` + 409) fica no backlog e só entra se aparecer conflito
real — complexidade sem sintoma é dívida.

### 4.7 Auditoria
Toda escrita financeira gera entrada em `audit_log`: quem, quando, qual ação, qual entidade, qual id,
IP. **Sem valores monetários no audit e sem valores no log de aplicação** — o histórico do dado está
no dado (soft delete + `updated_at`), e log é superfície de vazamento.

---

## 5. Decisões propostas — precisam do seu OK

Status: `proposto` = recomendação do plano, aguardando confirmação · `confirmado` = pode virar
ADR/spec. Nenhuma decisão `proposto` deve ser implementada.

> **Rodada de confirmação de 12/09/2026:** o usuário confirmou D2–D11 como recomendado, escolheu
> em D1 a variante **só tipos TS**, e em D12 **subiu a E7 para logo depois da E2**. Todas as doze
> estão fechadas e viraram ADR-015 a ADR-021 em `docs/ARQUITETURA.md`. As perguntas do §15 estão
> respondidas no próprio §15.

### D1 — oapi-codegen: liga agora ou continua à mão? · ✅ **CONFIRMADO 12/09/2026: só os tipos TS (ADR-015)**
O ADR-011 adiou o gerador com um teste de aderência (`routes_test.go`) segurando a divergência. Isso
funcionou para 11 rotas. O domínio financeiro adiciona ~35 rotas e ~25 schemas, e o frontend vai
duplicar cada um deles em TypeScript à mão.
**Recomendação:** ligar o gerador **antes da E2**, no momento mais barato que ainda existe — tipos Go
e TS derivados da spec, handlers continuam manuais. Alternativa aceitável: gerar **somente os tipos
TS** para o frontend e manter o Go manual (metade do ganho, um décimo do risco).
**Se ficar como está:** aceitar conscientemente a duplicação e reforçar `routes_test.go` para cobrir
também os schemas, não só (método, path).

### D2 — Transferência entre contas · ✅ **CONFIRMADO 12/09/2026: par de lançamentos (ADR-016)**
| Opção | A favor | Contra |
|---|---|---|
| **A) Par** (`transfer_out` + `transfer_in`, mesmo `transfer_group_id`) | extrato e saldo por conta são soma direta e indexada; portátil e trivial | integridade do par é responsabilidade do código; editar/excluir precisa tratar os dois |
| B) Uma linha com `account_id` + `counterpart_account_id` | par atômico por natureza | toda consulta por conta vira `OR`; saldo precisa de `CASE`; índice não ajuda |

**Recomendação:** A. As consultas mais frequentes do app são "extrato da conta" e "saldo da conta", e
elas ficam triviais. O par se protege com UnitOfWork, `transfer_group_id` e a regra "editar/excluir
age no par". Transferência **nunca** entra em receita/despesa de relatório nem consome orçamento.

### D3 — Saldo da conta · ✅ **CONFIRMADO 12/09/2026: derivado (ADR-017)**
Derivar de `opening_balance_cents` + soma indexada dos lançamentos. Numa casa (ordem de 10 mil
lançamentos por ano) a soma é irrelevante em custo, e coluna materializada é a fonte clássica de
saldo errado — qualquer caminho de escrita esquecido a corrompe. Se um dia doer: snapshot mensal como
**cache**, com a soma continuando a ser a verdade.

### D4 — Profundidade de categoria · ✅ **CONFIRMADO 12/09/2026: exatamente 2 níveis (ADR-017)**
Grupo → subcategoria (Moradia → Energia). Profundidade arbitrária exigiria CTE recursiva, cujo
suporte e sintaxe variam entre os 4 dialetos — é justamente o tipo de coisa que quebra o requisito
multi-banco. Dois níveis cobrem o caso doméstico com folga e mantêm toda consulta plana.

### D5 — Categorias iniciais da casa · ✅ **CONFIRMADO 12/09/2026: semear enxuto**
Casa nova nasce com ~12 grupos pt-BR editáveis (Moradia, Alimentação, Transporte, Saúde, Educação,
Lazer, Serviços, Pessoal, Impostos, Outras despesas · Salário, Outras receitas), criados na mesma
transação em que a casa é criada (na verificação do e-mail, onde o `EnsureDefault` já roda). Casa
vazia obriga o usuário a fazer taxonomia antes de lançar o primeiro gasto — é onde se desiste do app.

> **Emenda de 18/09/2026 (decisão do usuário):** a semente deixa de ser só de grupos. Casa nova nasce
> com **15 grupos, 41 subcategorias e 440 palavras-chave** pré-preenchidas (lista e regras: spec 0003
> §5 e **ADR-033**), para a categorização automática funcionar já no primeiro extrato importado. O
> usuário escolheu a versão **enxuta** (~3 folhas por grupo) em vez da completa (66 folhas).
> "Outras despesas" continua **sem filhas e sem palavras**, como balde residual. **Casas existentes
> não são tocadas:** a semente roda uma vez, na criação da casa — o ADR-033 traz o erratum ao
> ADR-029(a), que afirmava (errado) que ela roda no auto-reparo do login.

### D6 — Cartão de crédito com fatura · ⚠️ **REVERTIDO em 16/09/2026 — entrou na E2 (ADR-023)**
> **Emenda de 16/09/2026:** a confirmação de 12/09 (*fatura fora do v1*) foi **revogada pelo usuário**.
> A fatura virou entidade na E2 — `card_statements`, `competence_month`, fechamento e vencimento —, e o
> **ADR-023 supera o ADR-019 (c) e (d)**. O parágrafo abaixo fica como registro do que se pensou em
> 12/09, não como decisão vigente. O risco **R1** (§13) se materializou por decisão, não por erosão.
Fatura de verdade traz fechamento, vencimento, competência ≠ caixa, pagamento parcial e estorno —
sozinha é do tamanho de uma fase. No v1 o cartão é uma conta como as outras (saldo negativo = dívida)
e `kind` já prevê `credit_card`. Quando entrar, entra por colunas e tabela novas, sem migração
destrutiva. **Consequência a aceitar:** quem paga tudo no cartão vê o gasto na data da compra, não na
data da fatura.

### D7 — Ocorrências de conta fixa · ✅ **CONFIRMADO 12/09/2026: projeção virtual + tabela de exceções (ADR-018)**
| Opção | A favor | Contra |
|---|---|---|
| A) Materializar 12 meses por job | ocorrência é linha real, id estável | precisa job confiável; regra alterada deixa lixo; janela sempre finita |
| **B) Projetar da regra; gravar só o fato** | sem job, sem drift, mês futuro infinito | id da ocorrência é composto; alterar a regra muda a projeção de meses não pagos |

**Recomendação:** B. Status deriva da existência de linha em `bill_occurrences` (§3.5), o pagamento é
idempotente pela chave única, e o passado **pago** é imutável porque virou transação real.
**Limitação aceita e documentada:** mudar o valor da regra muda o valor *esperado* de meses passados
ainda não pagos. Mitigação se incomodar: versionar a regra por `starts_on`/`ends_on` (edição encerra
a versão e cria outra) — decidir só se doer.

### D8 — Orçamento · ✅ **CONFIRMADO 12/09/2026: sem acúmulo, com "copiar do mês anterior" (ADR-020)**
Sobra não vira crédito no mês seguinte (*rollover*) no v1: dobra a complexidade de cálculo e é
minoria do uso doméstico. Em troca, uma ação explícita copia todos os limites do mês anterior.

### D9 — Fuso e moeda · ✅ **CONFIRMADO 12/09/2026: `timezone` na casa, `BRL` fixo (ADR-019)**
A casa define o fuso (default `America/Sao_Paulo`) porque "atrasado" depende dele. Moeda é coluna com
`BRL` fixo: nenhuma conversão, nenhum símbolo hardcoded na UI.

### D10 — Nomes na interface e nas rotas · ✅ **CONFIRMADO 12/09/2026**
"Contas" é ambíguo em português (conta bancária vs conta a pagar). Fica:

| Conceito | Nome na UI | Rota |
|---|---|---|
| Account | **Contas** | `/contas` |
| RecurringBill | **Contas fixas** | `/contas-fixas` |
| Transaction | **Lançamentos** | `/lancamentos` |
| Dashboard | **Painel** | `/` |
| Household | **Casa** | `/casa` |
| Perfil do usuário | **Perfil** | `/perfil` |

### D11 — Gráficos (Fase 5) · ✅ **CONFIRMADO 12/09/2026: SVG próprio (ADR-021)**
`docs/DESIGN.md` proíbe biblioteca de componentes; uma lib de gráficos é uma biblioteca de
componentes com outro nome, e traz estética alheia. Os gráficos do v1 são poucos e simples (barras
mensais, rosca por categoria, linha de evolução) — SVG próprio guiado pela skill `dataviz`, com os
mesmos tokens. Se aparecer necessidade de gráfico interativo complexo, reabrir a decisão com ADR.

### D12 — Onde entram convites (DV2) · ✅ **CONFIRMADO 12/09/2026: E7 sobe para logo depois da E2**
O núcleo financeiro é o que faz o app valer a pena; convite sem lançamento não serve para nada.
**Mas:** se você pretende usar com outra pessoa desde o primeiro mês de uso real, convites sobem para
logo depois da E2. É uma escolha de uso, não técnica.

---

## 6. Armadilhas de portabilidade (as que já sei que vão morder)

Cada uma tem solução escolhida **antes** de existir código.

| # | Armadilha | Por que morde | Solução adotada |
|---|---|---|---|
| P1 | Agregar por mês | `DATE_TRUNC` (PG) · `DATE_FORMAT` (MySQL) · `strftime` (SQLite) · `FORMAT` (MSSQL) — quatro sintaxes | coluna `year_month CHAR(7)` gravada pela aplicação; `GROUP BY year_month` é igual nos quatro |
| P2 | Busca por texto | `LIKE` é sensível a maiúsculas no PG, insensível no MySQL por collation, ASCII-only no SQLite, dependente de collation no MSSQL | coluna `description_norm` (minúscula, sem acento) + termo normalizado igual; `%`, `_` e `\` escapados |
| P3 | `UNIQUE` com `NULL` | MSSQL trata NULLs como iguais (só um NULL passa); PG/MySQL/SQLite não | nenhum índice único sobre coluna anulável; a unicidade de pagamento vive em `bill_occurrences`, onde as colunas são obrigatórias |
| P4 | Índice parcial / filtrado | PG e SQLite sim, MSSQL com outra sintaxe, MySQL não tem | nenhum índice parcial no caminho comum |
| P5 | Booleano | MSSQL não tem `BOOLEAN` | `SMALLINT` 0/1 (já é convenção do projeto) |
| P6 | `ORDER BY` dinâmico | concatenar é injeção | allowlist de colunas ordenáveis, mapeada de string para constante |
| P7 | Paginação por OFFSET | resultado inconsistente sob escrita concorrente e caro no fim da lista | cursor *keyset* em `(occurred_on DESC, id DESC)`, opaco (base64), validado e rejeitado se malformado |
| P8 | `RETURNING`, JSON nativo, upsert dialetal | não existe igual nos quatro | id gerado na aplicação; upsert de orçamento por SELECT+INSERT/UPDATE dentro de transação |
| P9 | Precisão de tempo | dialetos truncam `TIMESTAMP` com precisões diferentes | comparação de data sempre por data civil; nunca depender de fração de segundo em regra |

---

## 7. Superfície de API planejada

Convenções já valendo (`docs/ARQUITETURA.md` §API): `/api/v1`, plural kebab-case, JSON camelCase,
dinheiro em centavos, erro único `{error:{code,message,fields}}`, **404 para recurso de outra casa**.

### 7.1 Contas e categorias (E1)
| Método | Rota | Notas |
|---|---|---|
| GET | `/accounts` | inclui `balanceCents` calculado e `archivedAt`; `?includeArchived=true` |
| POST | `/accounts` | |
| GET · PATCH | `/accounts/{id}` | |
| POST | `/accounts/{id}/archive` · `/unarchive` | |
| DELETE | `/accounts/{id}` | 422 se houver lançamento |
| GET | `/categories` | árvore de 2 níveis; `?kind=expense` |
| POST | `/categories` | |
| GET · PATCH | `/categories/{id}` | |
| POST | `/categories/{id}/archive` · `/unarchive` | |
| DELETE | `/categories/{id}` | 422 se em uso (lançamento, conta fixa ou orçamento) |

### 7.2 Lançamentos e transferências (E2)
| Método | Rota | Notas |
|---|---|---|
| GET | `/transactions` | `?month=YYYY-MM` **ou** `?from&to` · `accountId` · `categoryId` (inclui filhos) · `kind` · `q` · `minCents/maxCents` · `limit` · `cursor` · `sort` (allowlist) → `{items, nextCursor, summary:{incomeCents, expenseCents, netCents, count}}` |
| POST | `/transactions` | 201 com o recurso |
| GET · PATCH · DELETE | `/transactions/{id}` | DELETE é lógico |
| POST | `/transfers` | corpo: `fromAccountId`, `toAccountId`, `amountCents`, `occurredOn`, `description` → cria o par |
| PATCH · DELETE | `/transfers/{groupId}` | age no par inteiro |

`summary` no mesmo payload da lista é deliberado: a tela mostra os totais do filtro e não vale uma
segunda ida ao servidor com risco de divergir do que está na tela.

### 7.2.1 Palavras-chave, categorização automática e transferências internas (E2c — spec 0005, ADR-026)
| Método | Rota | Notas |
|---|---|---|
| POST · PATCH | `/categories`, `/categories/{id}` | ganham `keywords: string[]` (≤ 20, cada 2–40 runas; PATCH com o campo presente **substitui** a lista); `Category` devolve `keywords` sempre · 409 `KEYWORD_TAKEN` com `fields.keyword` e `fields.ownerId` (sempre da mesma casa) |
| POST · PATCH | `/accounts`, `/accounts/{id}` | idem, conjunto independente do de categoria |
| POST | `/imports` · GET `/imports/{id}` · POST `/imports/{id}/confirm` | `ImportRow` ganha `suggestedCategoryId`, `matchScore`, `matchedKeyword`, `suggestedCounterpartAccountId`, `matchOccurredOn`; status `transferencia_interna` e `transferencia_ja_registrada`; ação `link`; `categoryId` tri-estado na decisão; resposta ganha `linked` |
| POST | `/transactions/auto-categorize` | `{ month, dryRun }` → prévia (`dryRun: true`) ou escrita só onde `category_id IS NULL` (`dryRun: false`, recalculada no servidor); rate limit por casa da classe de escrita pesada |
| GET | `/transfers` | `?month&accountId&counterpartAccountId&limit&cursor` → `{ items (um por par), pairs, balances, nextCursor }`; `accountId` de outra casa → 404 |

### 7.3 Contas fixas (E3)
| Método | Rota | Notas |
|---|---|---|
| GET · POST | `/recurring-bills` | |
| GET · PATCH | `/recurring-bills/{id}` | |
| POST | `/recurring-bills/{id}/archive` · `/unarchive` | |
| DELETE | `/recurring-bills/{id}` | 422 se já houver ocorrência paga |
| GET | `/bill-occurrences?month=YYYY-MM` | projeção do mês com status derivado |
| POST | `/recurring-bills/{id}/occurrences/{month}/pay` | idempotente; corpo opcional (`amountCents`, `occurredOn`, `accountId`) → cria a transação |
| DELETE | `/recurring-bills/{id}/occurrences/{month}/pay` | desfaz (remove a transação logicamente) |
| POST · DELETE | `/recurring-bills/{id}/occurrences/{month}/skip` | pular / despular |

### 7.4 Painel (E4)
| Método | Rota | Notas |
|---|---|---|
| GET | `/dashboard?month=YYYY-MM` | um pedido, uma tela: saldo total e por conta · entradas/saídas/resultado do mês · próximos vencimentos e atrasados · top categorias · comparação com o mês anterior. **A 1ª fatia (spec 0008) entrega só três números: investido no mês (líquido, com sinal), receita (sem resgates) e gasto no cartão de crédito** — os demais blocos entram depois, aditivamente, no mesmo schema |

### 7.5 Orçamentos e relatórios (E5, E6)
| Método | Rota | Notas |
|---|---|---|
| GET | `/budgets?month=YYYY-MM` | limite, gasto, restante e percentual por categoria |
| PUT | `/budgets` | upsert por `(categoryId, yearMonth)` |
| DELETE | `/budgets/{id}` | |
| POST | `/budgets/copy-from-previous` | idempotente por mês destino |
| GET | `/reports/monthly-evolution?from&to` | ≤ 24 meses |
| GET | `/reports/by-category?month&kind` | soma o grupo com os filhos |
| GET | `/reports/by-account?month` | |
| GET | `/exports/transactions.csv?...` | mesmos filtros da listagem; streaming; sanitizado (§11) |

### 7.6 Casa e membros (E7)
| Método | Rota | Notas |
|---|---|---|
| GET · PATCH | `/households/current` | renomear (owner) |
| GET | `/households/current/members` | |
| PATCH · DELETE | `/households/current/members/{userId}` | papel / remover (owner; nunca o último owner) |
| GET · POST · DELETE | `/households/current/invitations[/{id}]` | owner; código de 6 dígitos por e-mail (mesma mecânica do ADR-009) |
| POST | `/invitations/accept` | e-mail + código; resposta nunca revela existência de convite |

---

## 8. Telas e navegação

### 8.1 Casca do app (E1)
Autenticado: cabeçalho com logo, **seletor de mês** e menu do usuário; navegação lateral colapsável
no desktop, barra inferior no mobile. O mês selecionado vive na URL (`?mes=2026-09`) e é o mesmo
entre telas — trocar de mês no painel e ir para lançamentos mantém o mês.

```
/                  Painel do mês
/lancamentos       Lista densa + filtros na URL (?mes, ?conta, ?categoria, ?tipo, ?q)
/transferencias    Transferências entre as contas da casa, por mês e por par (E2c)
/contas            Contas e saldos
/contas-fixas      Contas fixas do mês + regras
/categorias        Árvore de 2 níveis
/orcamentos        Limites do mês e acompanhamento
/relatorios        Evolução, por categoria, por conta
/casa              Nome da casa, membros, convites
/perfil            Nome, e-mail, senha
```

### 8.2 O que cada tela precisa fazer
| Tela | Núcleo | Cuidados |
|---|---|---|
| **Painel** | resultado do mês, saldo por conta, vencimentos (atrasado primeiro), top categorias | nada de 6 cards decorativos; número grande só onde é o número que importa |
| **Lançamentos** | tabela densa agrupada por dia com subtotal; criar/editar em `<dialog>` nativo sem trocar de rota (deep-link por `?novo=1`) | valores tabulares alinhados à direita; teclado resolve tudo; paginação por cursor com "carregar mais" |
| **Novo lançamento** | tipo (receita/despesa/transferência) → valor → categoria → conta → data → descrição | valor recebe o foco; teclado numérico no mobile; data default hoje; lembrar a última conta usada |
| **Contas fixas** | mês corrente com pago/pendente/atrasado e ação "pagar" em um toque | "pagar" é otimista com rollback; duplo toque não paga duas vezes |
| **Contas** | saldo por conta e total; arquivar | saldo negativo com sinal explícito, não só cor |
| **Categorias** | árvore, criar/renomear/arquivar | impedir mudança de `kind` com uso; deixar claro que arquivar preserva histórico |
| **Orçamentos** | limite × gasto por categoria, barra de progresso, copiar do mês anterior | estouro comunica com número e texto, não só com vermelho |
| **Relatórios** | evolução mensal, distribuição por categoria, por conta | gráfico sempre acompanhado da tabela — número é a fonte, gráfico é o resumo |
| **Casa** | membros, papéis, convidar por e-mail com código | só owner age; texto que não vaza existência de e-mail |

### 8.3 Componentes novos por entrega (o design system cresce sob demanda)
| Entrega | Componentes |
|---|---|
| E1 | `AppShell` (nav + header) · `MonthNavigator` · `DataTable` (densa, header fixo, agrupamento) · `Dialog` (`<dialog>` nativo) · `Select` (nativo primeiro) · `EmptyState` · `Toast` (`aria-live`) · `Badge` |
| E2 | `MoneyInput` (dígitos, centavos-primeiro, pt-BR) · `MoneyText` (tabular, sinal semântico) · `DateField` (`input[type=date]` + rótulo pt-BR) · `SegmentedControl` · `Combobox` (APG, só se o `Select` não bastar) · `FilterBar` |
| E2c | `KeywordsField` (fichas: Enter/vírgula/colar, Backspace/×, contagem anunciada) · `BlocoTransferencias` na revisão · chips "adicionar *palavra* a esta categoria" · `AutoCategorizeDialog` (prévia com lista e motivos) · `TransferPairPanel` (A→B, B→A, líquido com direção em texto, saldo no fim do mês) |
| E3 | `StatusPill` (pago/pendente/atrasado) · `ConfirmDialog` |
| E5 | `ProgressMeter` (orçamento) |
| E6 | `BarChart` · `DonutChart` · `LineChart` (SVG próprio — D11) |

Regra: **nenhum componente nasce sem tela que o use**, e todo componente novo nasce com teste e com
foco/teclado resolvidos.

---

## 9. Plano de entregas

Toda entrega é **vertical** (banco → API → tela → teste → revisão de segurança) e termina com algo
que você consegue usar. Nenhuma entrega é "só backend".

### E0 — Fechar a fundação · *sem spec, é dívida conhecida* · ✅ **CONCLUÍDA em 12/09/2026**
**Objetivo:** entrar no domínio financeiro sem dívida que multiplique.
Escopo: decidir D1 (oapi-codegen) e executar · testes das telas faltantes (criar conta, esqueci,
redefinir, Home, menu) · Playwright instalado com o fluxo de login passando · hook gofmt/goimports.
**Aceite:** `check.ps1` limpo · Vitest cobrindo as 5 telas · 1 E2E verde · ROADMAP Fase 3 sem `[~]`.

**Resultado real (saída verificada, não presumida):**
- `check.ps1` completo: **TUDO PASSOU** — gofmt, build, vet, `go test -race -count=1` nos 13 pacotes,
  build sem CGo, govulncheck (0 vulnerabilidades) e gosec.
- Frontend: **105 testes** em 17 arquivos · `tsc --noEmit` limpo · `biome check` limpo ·
  `npm run build` OK · `npm audit` com 0 vulnerabilidades.
- E2E: **4 casos verdes** em Chromium contra a API Go real (`npm run e2e`).
- ROADMAP: Fases 1 e 3 sem nenhum `[~]`.
- Decisões viradas em ADR: **ADR-015** (tipos TS do OpenAPI) e **ADR-022** (Playwright no lugar do
  Vitest Browser Mode). D2–D11 fecharam nos ADR-016 a ADR-021.
- **Revisão de segurança: APROVADO**, com 5 achados próprios levantados e **todos corrigidos antes
  do fecho** — nenhum crítico ou alto:
  | # | Sev. | Achado | Correção |
  |---|---|---|---|
  | E0-1 | Média | `npm run build` passou a depender de `npx -y` (busca ao registro npm no caminho do build) | `build` voltou a ser offline; a aderência spec↔tipos virou **teste de hash SHA-256** offline (`schema-sync.test.ts`), com o `api:check` de rede reservado ao CI/entrega |
  | E0-2 | Média | `Bash(npm install *)` liberado sem prompt na allowlist — instala pacote arbitrário e roda `postinstall` de terceiro | removido da allowlist; instalação volta a exigir confirmação |
  | E0-3 | Média | `execFileSync` com `shell: true` no Windows e caminhos sem aspas | caminhos citados; comentário explicando por que o shell é inevitável (`npx` é `.cmd`, CVE-2024-27980) |
  | E0-4 | Baixa | Diretório de execução do E2E em caminho previsível de temp compartilhado, contendo o log com códigos OTP e o banco | movido para `frontend/.playwright/execucao/`, dentro do projeto e já ignorado pelo git |
  | E0-5 | Baixa | `gofmt -l "$file"` sem `--`: caminho iniciado por `-` seria lido como flag | `gofmt -l -- "$file"` e `gofmt -w -- "$file"` |
  Verificado também: o polyfill de Popover **não** entra no bundle de produção (confirmado no
  `dist/`), nenhum segredo literal em código ou teste (os do E2E são `crypto.randomBytes` por
  execução), CORS do E2E com origem exata, e `govulncheck`/`gosec`/`npm audit` limpos.

### E1 — Contas e categorias · *spec 0003* · ✅ **CONCLUÍDA em 13/09/2026**
**Objetivo:** a casa consegue descrever o próprio dinheiro.
Escopo: modelos + AutoMigrate v2 (accounts, categories) · semente de categorias (D5) · CRUD dos dois
com arquivar/desarquivar · saldo derivado (só `opening_balance` nesta entrega, ainda sem lançamento) ·
casca do app com navegação e seletor de mês · telas `/contas` e `/categorias`.
**Aceite:** criar/editar/arquivar conta e categoria pela UI · categoria em uso não se exclui ·
recurso de outra casa responde 404 em todos os endpoints · repositório testado em SQLite com caso de
isolamento · revisão de segurança APROVADO.

**Resultado real (saída verificada, não presumida):**
- `check.ps1` completo: **TUDO PASSOU** — gofmt, build, vet, `go test -race -count=1` nos
  **19 pacotes**, build sem CGo, govulncheck (0) e gosec.
- Frontend: **172 testes** em 24 arquivos · `tsc`, `biome`, `build` e `npm audit` limpos ·
  `api:check` confere spec ↔ tipos TS.
- E2E: **17 casos verdes** em Chromium contra a API Go real, incluindo o `<dialog>` e o
  `<select>` nativos, a semente de categorias e o mês na URL.
- Pacotes novos: `civil` (data civil), `textnorm` (P2 antecipada), `account`, `category`.
- **AutoMigrate verificado partindo de banco v1 POVOADO**, não só vazio — é o critério de
  aceite 6, e o caso fácil (banco vazio) não prova nada sobre produção.
- **Divergência reportada e corrigida:** a spec 0003 tinha omitido a auditoria exigida pelo
  §4.7. A revisão pegou, a implementação entrou ainda na E1 e a spec ganhou uma emenda
  datada em vez de ser reescrita como se nunca tivesse errado.
- **Revisão de segurança: APROVADO**, sem achado crítico ou alto. Verificado: nenhuma
  consulta sem `household_id`; nenhum `Raw`/`Exec`/interpolação em SQL; todo `ORDER BY` é
  constante em código; 404 (nunca 403) para recurso de outra casa em todas as rotas com
  `{id}`, com corpo byte a byte igual ao de id inexistente; DTO explícito por endpoint com
  `DisallowUnknownFields` (mandar `householdId` é 400); nenhum valor monetário em log nem em
  auditoria; nenhum `float` em caminho de dinheiro; nenhum `dangerouslySetInnerHTML`; só o
  mês trafega em query string.

### E2 — Lançamentos e transferências · *spec 0004* · **a entrega mais importante do projeto**
**Objetivo:** registrar dinheiro entrando e saindo, e ver o mês.
Escopo: `transactions` + índices · criação/edição/exclusão lógica · listagem com filtros, cursor e
`summary` · transferência como par (D2) · saldo real por conta · busca normalizada (P2) ·
`MoneyInput`/`MoneyText`/`DateField` · tela `/lancamentos` com diálogo de criação.
**Aceite:** lançar despesa em ≤ 3 interações a partir do painel · 5.000 lançamentos de fixture
listam a primeira página em ≤ 300 ms no SQLite local · transferência não aparece em receita/despesa ·
`accountId`/`categoryId` de outra casa → 404 · cursor forjado → 400 · E2E "criar, editar, excluir
lançamento" verde · revisão APROVADO.

### E2c — Palavras-chave: categorização automática e transferências internas · *spec 0005* · **planejada em 17/09/2026 (ADR-026)**
**Objetivo:** a pessoa não classifica linha por linha o mesmo "Mercado do seu José" todo mês, e a
transferência entre as próprias contas deixa de inflar receita e despesa.
Escopo: `category_keywords` + `account_keywords` (até 20 por item, únicas por casa dentro do tipo,
`KeywordsField` nos diálogos de categoria e de conta) · pacote folha `internal/textmatch`
(algoritmo determinístico da §3 da spec: exata → maior substring comum ≥ 5 runas → erro de
digitação; limiar 80; empate → sem sugestão; **sem IA, sem regex, sem dependência nova**) · cola em
`internal/classify` · importação ganha `suggestedCategoryId`/`matchScore`/`matchedKeyword` por linha e
os status `transferencia_interna` (palavra-chave de **outra** conta ativa) e
`transferencia_ja_registrada` (a outra perna já existe → ação `link`, que só grava a chave de
importação na perna existente) · confirm com `categoryId` tri-estado e "aceitar todas as
transferências sugeridas" · chips "adicionar *palavra* a esta categoria" na revisão ·
`POST /transactions/auto-categorize` com prévia (`dryRun`) e escrita só onde `category_id IS NULL` ·
`GET /transfers` + tela `/transferencias` (cada par uma vez, totais por sentido, líquido e saldo no fim
do mês somados no servidor).
**Fora:** CRUD manual de lançamento e `POST/PATCH/DELETE /transfers` (E2b) · regras por valor, data,
regex, pesos, sinônimos · recategorizar em massa o que já tem categoria · detecção de transferência
**sem** palavra-chave (conciliação por valor/data espelhados → backlog com spec própria).
**Aceite:** tabela da §3 reproduzida por teste, pontuação exata linha a linha · empate → sem sugestão
e a mesma palavra em duas categorias → 409 · conta batendo vence categoria batendo; a própria conta
do lote, conta arquivada e categoria arquivada nunca são sugeridas; `income` nunca recebe categoria
`expense` · importar A e depois B: a linha espelhada em B fica `transferencia_ja_registrada`, o
`link` grava a chave na perna existente, reimportar B cai em `duplicado_exato` e o saldo das duas
contas não muda · duas transferências iguais no mesmo dia casam com pernas distintas · `categoryId:
null` grava sem categoria apesar da sugestão; ausente grava a sugerida; de outra casa → 404 e nada
gravado · `link` fora de `transferencia_ja_registrada` → 400; a perna vem só da análise · `dryRun` não
escreve; rodar duas vezes categoriza 0 na segunda; uma auditoria por execução real, sem descrição ·
`GET /transfers` traz cada par uma vez, `pairs` fecha com `items`, `netCents == aToB − bToA`, saldo
igual ao de `GET /accounts` no mês corrente · 10.000 linhas × 1.000 palavras-chave em ≤ 2 s ·
`KeywordsField` por teclado com contagem anunciada · Playwright: cadastrar palavra → importar → linha
vem sugerida → confirmar → lançamento tem a categoria · revisão de segurança APROVADO.

> **Numeração de spec é atribuída na ESCRITA, não aqui (corrigido em 18/09/2026).** Prever o número
> na fila produziu três divergências reais: a E2d tomou o **0007** sem que o arquivo existisse, a E6a
> saiu **sem spec**, e o painel tomou o **0008** que estava reservado para a E5 — que passou a colidir
> com a E6. A partir daqui, a fila diz *a numerar*; o número nasce com o arquivo em `docs/specs/`.
> (A E6 mantém o **0009** porque o bloco da E6a já se compromete com esse número em dois pontos.)

### E3 — Contas fixas · *spec a numerar quando for escrita*
**Objetivo:** nenhuma conta vence sem aviso.
Escopo: `recurring_bills` + `bill_occurrences` · projeção virtual do mês (D7) · pagar/desfazer/pular
idempotentes · clamp de dia em mês curto · status derivado no fuso da casa · tela `/contas-fixas`.
**Aceite:** conta com `due_day=31` aparece em 28/02 · pagar duas vezes gera um lançamento ·
desfazer remove o lançamento e volta o status · testes com o processo em UTC **e** em
`America/Sao_Paulo` · revisão APROVADO.

### E4 — Painel do mês · *spec **0008** (o número 0007 nunca chegou a existir: a E2d virou emenda §12 da spec 0004)*
**Objetivo:** responder "como estamos?" em uma tela.

> **Fatiada em 18/09/2026, a pedido do usuário.** A **primeira fatia** — a faixa de resumo do mês com
> três números (**investido no mês**, líquido com sinal; **receita**, sem resgates; **gasto no cartão de
> crédito**, um número só) — está na `docs/specs/0008-painel-resumo-do-mes.md`. Saldo por conta,
> vencimentos, top categorias e comparação com o mês anterior seguem nesta E4, depois dela.
Escopo: `GET /dashboard` agregando em uma consulta por bloco · tela `/` com resultado do mês, saldos,
vencimentos e top categorias · comparação com o mês anterior.
**Aceite:** um único pedido de rede monta a tela · números idênticos aos das telas de origem (mesma
fonte, sem cálculo duplicado no front) · revisão APROVADO.

### E5 — Orçamentos · *spec a numerar quando for escrita*
Escopo: `budgets` com upsert portátil (P8) · comparação limite × gasto (grupo soma filhos) · copiar
do mês anterior · tela `/orcamentos`.
**Aceite:** orçamento de grupo reflete gasto dos filhos · copiar duas vezes não duplica · estouro
comunicado por texto e número, não só por cor · revisão APROVADO.

### E6a — Gastos por categoria · *sem spec — primeira fatia vertical da E6 (ADR-027)* · ✅ **CONCLUÍDA em 17/09/2026**
**Objetivo:** responder "para onde o dinheiro foi neste mês?" (§1.2, item 4) sem esperar a E6
inteira — e sem que o primeiro gráfico do produto nasça torto.

**Fatia, não entrega própria.** A spec 0009 ainda **não existe**: a E6 foi partida e esta fatia saiu
inteira na vertical (banco → API → tela → teste → revisão), fora da ordem da fila do §9.1. Quando a
E6 for especificada, a **spec 0009 parte do ADR-027 e trata `GET /reports/by-category` como já
entregue**; seguem sendo escopo da E6 a **evolução mensal** (`/reports/monthly-evolution`), o
relatório **por conta** (`/reports/by-account`) e a **exportação CSV** (com o S7 do §11).

**Escopo entregue.** Backend: pacote `backend/internal/report/` — serviço de **leitura pura** (sem
escrita, sem auditoria, sem UnitOfWork), com `apportion` distribuindo o percentual por **maior resto
em 128 bits** · `TransactionRepository.SumByCategory` (`GROUP BY category_id`, **uma** consulta) ·
contrato `GET /api/v1/reports/by-category?month&kind`, sob `requireAuth` e sob o limitador global.
Frontend: item **Relatórios** na casca, rota `/relatorios/categorias`, componente próprio
`DonutChart` — **o primeiro gráfico do produto**, SVG escrito para este projeto (D11 / ADR-021) —,
**tabela como fonte da verdade** (o SVG é `aria-hidden`) e tokens `--chart-1..4` / `--chart-pending`.

**Resultado real (saída verificada, não presumida):**
- `internal/report`: `go test -race -count=1 ./internal/report/...` → **ok 13.325s** (última
  execução, pelo revisor) · `gosec ./internal/report/...` → **Files: 4 · Lines: 771 · Nosec: 0 ·
  Issues: 0** · `go vet` e `gofmt` limpos.
- `gormstore`, testes do relatório: `TestRelatorioPorCategoriaBateComSummary`,
  `TestRelatorioNaoVazaEntreCasasComNomesIguais`,
  `TestRelatorioComCategoriaAdulteradaNaoVazaNomeNemId` e
  `TestRelatorioComPaiDeOutraCasaPromoveAFilha` → **PASS**.
- Volume medido: **10 000 lançamentos vivos + 200 categorias → 193 linhas, 1 consulta, 20,8 ms**.
  Sem N+1 — e a contagem de consultas é **assertada** por callback do GORM, não observada de olho.
- Frontend: `biome`, `tsc --noEmit`, `npm run build` e `npm audit` limpos · **Vitest: 645 testes
  passando**, com uma única falha — `src/api/schema-sync.test.ts` —, **alheia a esta entrega** (ver
  o último parágrafo).
- E2E Playwright: `relatorio-por-categoria.spec.ts` + `importacao.spec.ts` → **13 passed (1.2m)**;
  telas migradas para o hook de foco → **20 passed (1.2m)**.
- **Revisão de segurança: APROVADO** (`revisor-seguranca`), com três achados de severidade **baixa**
  — **B1** amplificação de log (um registro por linha anômala), **B2** incoerência de política de
  falha, **B3** `somaSegura` aceitando parcela negativa —, **os três corrigidos**; o **delta das
  correções foi revisado e APROVADO de novo**.

**Decisões que valem registro** (ADR-027 (a)–(f) em `docs/ARQUITETURA.md`; as de apresentação
ficaram na tela):
- **percentual em pontos-base inteiros, apurados no servidor** — `float` não aparece em nenhum ponto
  do caminho e o cliente só formata (§4.1);
- balde **"Sem categoria" explícito** (`categoryId: null`, nunca omitido): dinheiro sem etiqueta
  aparece, não some;
- **grupo soma as filhas** e carrega o que foi lançado direto nele (`direct*`) — é o que faz o total
  fechar;
- **teto de fatias do gráfico: 4 nomeadas + pendente + "Outras"**, medido pelo validador da skill
  `dataviz` — cinco passos monocromáticos já reprovam o piso de contraste;
- percentual com **2 casas na tabela** (a soma fecha **100,00%** exato) e **1 na legenda**.

**Correções colaterais feitas no caminho:**
- barra de navegação inferior do celular **reespecificada para 6 itens** (hífen suave, `--text-12` e
  o token `--nav-bar-h` reservando a altura) — emenda em `docs/DESIGN.md`;
- hook compartilhado **`useFocoNoTitulo`** no lugar de seis `useEffect` quase iguais — corrigiu um
  **bug real**: o `<h1>` não recebia foco ao chegar pelo menu;
- `vite.config.ts` com `testTimeout`/`hookTimeout` de 15 s — contenção de CPU na máquina, não
  defeito de teste.

**Pendências honestas — registradas como NÃO resolvidas:**
- `TransfersScreen.tsx` e `HomeScreen.tsx` ainda têm o `useEffect` de foco **inline**: a primeira é
  território da E2c em andamento, a segunda ficou fora do escopo;
- a suíte do relatório **nunca rodou contra PostgreSQL** (`eachBackend` só roda SQLite sem
  `TEST_POSTGRES_DSN`) — vale uma rodada antes da entrega final (é o gatilho de R2, §13);
- `prefers-contrast: more` e `forced-colors` da rampa `--chart-*`: validados **no papel**, sem teste
  automatizado;
- **modo escuro** e **375 px** da tela nova sem asserção automatizada;
- o E2E cobre importar → categorizar → relatório muda; falta o caminho de **exclusão** de lançamento;
- `archivedAt` de categoria filha **não exercitado no navegador**;
- rolagem horizontal a **200% de zoom** e recorte da coluna **Valor** no celular: **pré-existentes**
  do cabeçalho e do `DataTable`, medidos e deixados fora do escopo desta entrega;
- divergência **cosmética** de contrato: o `pattern` de `YearMonth` aceita `0000-01`, que o servidor
  recusa com 400 — o servidor está **mais estrito** que a spec.

**Não são desta entrega, e travam o `check.ps1` e o `api:check` — pertencem à E2c:**
`internal/importer` falha em `qa_e2c_desempenho_test.go` (teste de desempenho da E2c: 422 isolado,
500 sob carga), e `backend/api/openapi.yaml` está **dessincronizado** de
`frontend/src/api/schema.gen.ts` porque a sessão da E2c editou a spec sem rodar `npm run api:gen`.

### E6 — Relatórios e exportação · *spec 0009*
Escopo: três relatórios agregando por `year_month` (P1) · gráficos SVG próprios (D11) · CSV em
streaming sanitizado (§11) · tela `/relatorios`.
**Aceite:** intervalo > 24 meses recusado · CSV abre correto no Excel pt-BR · célula iniciada por
`=`/`+`/`-`/`@` neutralizada · export registrado em auditoria e com rate limit próprio · revisão
APROVADO.

> **Emenda de 17/09/2026:** o relatório **por categoria** já saiu na **E6a** (ADR-027). A spec 0009
> nasce partindo dele como entregue e cobre evolução mensal, por conta e exportação CSV.

### E7 — Convites e membros · *spec a numerar quando for escrita* · fecha a Fase 2 (DV2)
Escopo: `invitations` com código de 6 dígitos (mesma disciplina do ADR-009: só hash HMAC, uso único,
expiração, limite de tentativas, rate limit) · aceitar convite · listar/remover membro, trocar papel ·
tela `/casa`.
**Aceite:** só owner convida e nunca se remove o último owner · resposta não revela existência de
e-mail nem de convite · membro removido perde acesso imediatamente (sessão e família de refresh) ·
revisão APROVADO.

### E8 — Endurecimento e produção · Fase 6
Escopo: suíte testcontainers nos 4 dialetos (DV5) · workflow `auditoria-seguranca` no projeto todo ·
Playwright nos fluxos críticos · passkeys (WebAuthn) · build de produção, TLS, backup e guia de
deploy · `.env.example` final.
**Aceite:** a mesma suíte de repositório passa em Postgres, MySQL, SQLite e MSSQL · auditoria sem
achado crítico ou alto em aberto · restauração de backup testada de verdade, não descrita.

### 9.1 Dependências — ordem confirmada em 12/09/2026

D12 foi decidido a favor de **usar com duas pessoas desde o começo**, então a E7 sobe para logo
depois da E2 (ela é independente do resto do núcleo, então não atrasa E3–E6, só troca de posição
na fila):

```
E0 ──▶ E1 ──▶ E2 ──▶ E2c ──▶ E7 ──▶ E3 ──▶ E4 ──▶ E5 ──▶ E6 ──▶ E8
```

---

## 10. Definição de pronto (vale para qualquer entrega)

1. `go build ./...` · `go vet ./...` · `go test -race ./...` · `govulncheck` · `gosec` — limpos
   (via `backend/scripts/check.ps1`), com a **saída real** reportada.
2. `npx biome check` · `npm test` · `npm run build` — limpos; E2E do fluxo novo verde.
3. Código novo com teste; bug corrigido com teste de regressão.
4. Spec da entrega atualizada se a realidade divergiu do planejado (divergência se reporta, não se
   implementa em silêncio).
5. ADR escrito se houve decisão estrutural; `docs/ROADMAP.md` atualizado com status honesto.
6. `revisor-seguranca` com veredito literal **APROVADO** — achado crítico ou alto bloqueia.
7. Sem `panic` em caminho de request; erros embrulhados com contexto; mensagem ao cliente genérica.
8. AutoMigrate aplicado com sucesso partindo de banco **vazio** e de banco **já povoado**.

---

## 11. Segurança específica do domínio financeiro

O checklist de `docs/SEGURANCA.md` continua valendo inteiro. Estes são os riscos **novos** que o
domínio financeiro traz e que o `revisor-seguranca` vai cobrar em toda entrega da E1 à E7:

| # | Risco | Defesa exigida |
|---|---|---|
| S1 | **BOLA por referência cruzada** — cliente manda `accountId`/`categoryId`/`billId` válido, mas de outra casa | todo id recebido é validado como pertencente à casa do token, **na mesma transação** da escrita; falha responde 404, nunca 403 (403 confirma existência) |
| S2 | *Mass assignment* | DTO explícito por endpoint; `householdId`, `createdBy`, `yearMonth`, `descriptionNorm`, `deletedAt` **nunca** vêm do cliente |
| S3 | Valor absurdo / overflow de int64 | limites de §4.5 validados na borda; soma de agregação com teto conhecido |
| S4 | Filtro e ordenação | allowlist de campos ordenáveis e filtráveis; `limit` com teto; mês validado por formato |
| S5 | Cursor forjado | cursor opaco assinado ou estritamente validado; conteúdo inválido → 400 sem detalhe |
| S6 | DoS por consulta | intervalo de relatório ≤ 24 meses; export com rate limit próprio; nenhuma consulta sem `household_id` no filtro |
| S7 | **Injeção de fórmula em CSV** | célula iniciada por `=`, `+`, `-`, `@`, TAB ou CR recebe prefixo neutralizador; separador e decimal pt-BR |
| S8 | Vazamento em log | nenhum valor monetário, e-mail ou id sensível no `slog`; auditoria guarda ação e entidade, não valor (§4.7) |
| S9 | Duplo débito | idempotência de pagamento por `(bill_id, competence_month)`; mutação otimista no front sempre com rollback |
| S10 | Escrita parcial | transferência, pagamento e semente de categorias sempre dentro do UnitOfWork (não há FK física — ADR-013) |
| S11 | Membro removido | remoção revoga sessão e família de refresh na hora (E7) |

---

## 12. Testes — o que cada entrega precisa provar

| Camada | O que cobre | Ferramenta |
|---|---|---|
| Service (unidade) | regras, bordas de data (mês curto, bissexto, virada de mês no fuso da casa), limites, status derivado | `testing` + testify, repositório falso |
| Repositório | consulta, índice usado, e **isolamento**: dado de outra casa nunca aparece, nem em agregação | SQLite em memória |
| Handler | contrato, validação com `fields`, tradução de erro de domínio, 404 para outra casa | `httptest` |
| Abuso | ids cruzados, valor gigante e zero, cursor forjado, `limit` absurdo, mês inválido, duplo pagamento, `q` com `%`/`_`/`\` | `qa-testes` |
| Consistência | soma de saldos estável após sequência de operações aleatórias (pega bug de transferência) | teste de propriedade simples |
| Front (unidade) | máscara de dinheiro, formatação pt-BR, validação Zod, estados de tabela vazia/erro | Vitest + Testing Library |
| Front (E2E) | lançar, editar, excluir · pagar conta fixa · trocar mês · filtrar | Playwright |
| Multi-banco | a mesma suíte de repositório nos 4 dialetos | testcontainers-go (E8) |

---

## 13. Riscos do plano

| # | Risco | Probabilidade | Mitigação |
|---|---|---|---|
| R1 | **Cartão de crédito volta pela janela** e puxa competência, fatura e estorno para dentro do v1 | alta | **Materializou-se em 16/09/2026**, e pelo caminho previsto: virou **ADR-023** e escopo explícito da spec 0004, não emenda silenciosa. Fatura e competência entraram; **estorno continua fora** (crédito de fatura vira receita — §3.4 D4 da spec 0004) |
| R2 | O requisito multi-banco só é verificado na E8 e algo estrutural não passa em MSSQL/MySQL | média | §6 decide as armadilhas antes; **gatilho antecipado:** rodar a suíte em Postgres + MySQL uma vez na E2, quando o schema financeiro nasce |
| R3 | Mudança destrutiva de schema (renomear coluna) — AutoMigrate não faz | média | nome e tipo pensados agora (§3); renomear exige ADR + passo manual; preferir **coluna nova** a renomeação |
| R4 | Design system vira gargalo (E1 precisa de 8 componentes) | média | componente só nasce com tela que o use; nativo primeiro (`<dialog>`, `<select>`, `input[type=date]`) antes de reimplementar padrão APG |
| R5 | Divergência spec ↔ código cresce com ~35 rotas novas | alta se D1 ficar como está | resolver D1 na E0/E1 |
| R6 | Bug de fuso: conta "atrasada" um dia antes | média | data civil + fuso da casa em um único lugar; teste com processo em UTC e em `America/Sao_Paulo` |
| R7 | Entrega inflar e virar três (E2 é grande) | média | E2 pode ser partida em E2a (CRUD + lista) e E2b (transferência + busca) se a spec passar de ~2 dias de trabalho |

---

## 14. Backlog explícito (fora do v1, com o caminho já pensado)

| Item | Caminho previsto |
|---|---|
| **Fatura de cartão de crédito** | `accounts.closing_day/due_day` + tabela `card_statements`; `transactions.competence_month`; pagar fatura = transferência |
| **Parcelamento** | `installment_group_id` + `installment_no/total`; N lançamentos futuros; sobra da divisão vai na primeira parcela |
| **Anexo de comprovante** | armazenamento fora do banco, URL assinada de vida curta, tipo e tamanho validados, varredura — é superfície de ataque nova, entra com spec própria |
| ~~**Importar OFX/CSV de banco**~~ → **parcialmente entregue na E2 (16/09/2026)** | CSV e ZIP com senha de C6 e Nubank entraram na spec 0004, com revisão humana obrigatória antes de gravar. **Segue no backlog:** OFX, Open Finance e conciliação automática |
| **Conciliação de transferência sem palavra-chave** | pareamento só por valor e data espelhados entre duas contas — é conciliação, exige spec própria; a E2c só detecta por palavra-chave de conta (ADR-026e) |
| **Regras de categorização além de palavra-chave** | por valor, data, conta de origem ou regex; sinônimos; aprendizado sem clique — cada um reabre o ADR-026 |
| **Metas e reserva de emergência** | depende de relatório histórico consolidado |
| **Divisão entre membros** | "quem pagou / quem deve" — muda o modelo de lançamento, exige ADR |
| **Multi-casa por usuário** | `switch-household` e `hid` no token já preveem; falta UI e revisão de sessão |
| **PWA instalável e offline** | fila de lançamentos offline com idempotência por chave de cliente |
| **Passkeys** | E8 |
| **Concorrência otimista** | coluna `version` + 409, só se conflito real aparecer (§4.6) |
| **Rollover de orçamento** | reabrir D8 |

---

## 15. Perguntas abertas — ✅ **todas respondidas em 12/09/2026**

As seis perguntas abaixo foram feitas ao usuário e respondidas na abertura da execução do plano.
A resposta de cada uma está registrada logo abaixo da pergunta; as decisões correspondentes viraram
ADR-015 a ADR-021.

1. **D1 — oapi-codegen:** ligo o gerador agora (recomendado), gero **só os tipos TS** do frontend, ou
   mantenho tudo à mão e reforço o teste de aderência?
   → **Resposta: só os tipos TS.** O frontend deriva `schema.gen.ts` da spec e o Go segue manual.
   Fechado no **ADR-015**; executado na E0.
2. **D6 — cartão de crédito:** confirma que fatura fica fora do v1 (cartão como conta comum)? Se você
   paga a maior parte das despesas no cartão, isso muda o quanto o app serve já no primeiro mês.
   → **Resposta: confirmado, fatura fica fora do v1.** Cartão é conta comum, `kind = credit_card` é
   rótulo. Fechado no **ADR-019(c)**, com a consequência aceita registrada lá.
3. **D12 — convites:** o app vai ser usado por duas pessoas desde o começo? Se sim, subo a E7 para
   logo depois da E2.
   → **Resposta: sim.** A E7 subiu para logo depois da E2 (ver §9.1).
4. **D2 e D7** (transferência como par, ocorrência virtual): confirma as recomendações ou quer ver o
   desenho alternativo detalhado?
   → **Resposta: confirmadas as duas.** Fechadas nos **ADR-016** e **ADR-018**, com as alternativas
   recusadas e o porquê registrados em cada um.
5. **Ordem:** a sequência E1 → E2 → E3 → E4 faz sentido para o seu uso, ou você prefere ver o painel
   antes das contas fixas?
   → **Resposta: sequência mantida**, com a E7 inserida depois da E2 por conta de D12 (ver §9.1).
6. **E0:** pago a dívida da fundação primeiro (recomendado — entrega curta) ou vou direto para a E1 e
   resolvo as pendências no caminho?
   → **Resposta: E0 primeiro.** A dívida da fundação (DV1, DV3, DV4, DV6) é paga antes de entrar no
   domínio financeiro.
