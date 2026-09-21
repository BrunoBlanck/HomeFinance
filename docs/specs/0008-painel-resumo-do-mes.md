# Spec 0008 — Painel: o resumo do mês (primeira fatia da E4)

- **Status:** **APROVADA** pelo usuário em 18/09/2026 — imutável na essência; mudança de escopo vira revisão datada neste arquivo
- **Data:** 18/09/2026
- **Origem:** pedido do usuário em 18/09/2026, com três respostas de entrevista registradas no §7.
- **Relação com o PLANOS.md:** é a **primeira fatia vertical** da E4 (§7.4 e §9.1), não a E4 inteira.

> **Nota de numeração (atualizada em 18/09/2026).** O `PLANOS.md` §9.1 reservava "spec 0007" para a
> E4, e trabalho em curso da E2d chegou a citar esse número antes de o arquivo existir. **O 0007 não
> existe e não vai nascer:** a E2d (filtro de tipo em `GET /transactions`) é **emenda §12 da spec
> 0004**, porque o que ela faz é corrigir o §1.2 daquela spec — o parâmetro que estava adiado para a
> E2b. A E4 fica com **0008**, e a dívida de "escrever o arquivo da 0007" está **paga**: ela nunca
> teve arquivo próprio a escrever.

## 1. Problema

O painel (`/`) é a primeira tela de quem entra no app e hoje não responde nada: ele mostra um texto
de placeholder ("Seu caderno está em branco") e duas listas do estado do projeto, escritas quando não
havia lançamento no sistema. Hoje há importação, categorização, transferências e investimentos — e
nenhum número.

## 2. Escopo

Uma faixa de resumo do mês selecionado, com **três números** e nada mais:

1. **Investido no mês** — o **líquido**: aportes − resgates. Vem **com sinal** e pode ser negativo
   (mês em que se resgatou mais do que se aportou). É a decisão do usuário de 18/09/2026, registrada
   em `LICOES-FRONTEND.md` e `LICOES-BACKEND.md`.
2. **Receita do mês** — tudo que entrou, **sem os resgates de investimento**.
3. **Gasto no cartão de crédito** — as despesas lançadas em contas de tipo `credit_card`, somadas em
   **um** número (todos os cartões juntos).

O mês é o da casca (`?mes=` na URL, fuso da casa — ADR-019), e abre no mês corrente. Tudo é
**competência** (ADR-023c), como no resto do app. O conteúdo de placeholder da `HomeScreen` sai.

### 2.1 Fora de escopo (explícito)

- **Transferências internas**: não entram em nenhum dos três números, nem viram um quarto número.
  Elas já estão fora de receita e despesa por desenho (ADR-016); aqui a regra passa a ser
  **verificada por teste**, não apenas herdada. O item **Transferências** continua no menu e a tela
  `/transferencias` continua intacta — decisão do usuário nesta sessão.
- **Gasto total do mês, resultado (entrou − saiu), saldo por conta, próximos vencimentos, top
  categorias e comparação com o mês anterior**: são a E4 completa e seguem no `PLANOS.md` §7.4.
- **Abertura por cartão** (Nubank R$ X, C6 R$ Y): o usuário escolheu um número só.
- **Aportes e resgates como números de apoio** no painel: o usuário escolheu "somente o valor final".
  Quem quiser o detalhe tem a tela `/investimentos`, que não muda nesta entrega.
- **Gráfico**: nenhum. Três números.
- **Patrimônio investido acumulado**: continua não existindo em lugar nenhum do produto.

## 3. Comportamento

1. A pessoa abre `/`. A tela pede `GET /dashboard?month=<mês da casca>` — **um** pedido de rede para
   a faixa inteira (`PLANOS.md` §9.1, aceite da E4).
2. Carregando: `Skeleton` no lugar de cada número, com `aria-busy` na seção. Nunca zeros
   provisórios — zero é um valor, e mostrá-lo antes da resposta é mentir.
3. Respondido, cada número aparece formatado em pt-BR. O investimento leva **sinal explícito**
   (`−R$ 350,00` / `+R$ 2.000,00`), nunca só a cor; os outros dois são valores neutros sem sinal.
4. **Mês sem movimento nenhum**: a faixa continua existindo, com os três números em `R$ 0,00` e a
   contagem por extenso (`nenhum lançamento`). Não se esconde a faixa — sumir com ela faria a pessoa
   achar que a tela quebrou.
5. **Casa sem cartão de crédito cadastrado**: o número do cartão vira um traço e a linha diz
   `Nenhum cartão de crédito cadastrado`, com link para `/contas`. É diferente de "cartão sem gasto
   no mês", que mostra `R$ 0,00`. A resposta traz `creditCardAccountCount` justamente para a tela
   distinguir os dois casos sem adivinhar.
6. **Casa sem categoria de investimento**: mesma lógica — traço e `Nenhuma categoria de investimento
   ainda`, com link para `/categorias`, usando `investmentCategoryCount`.
7. **Erro de rede ou 500**: a faixa mostra o alerta padrão (`Alert tone="error"`) com "Tentar de
   novo", e o resto da tela continua utilizável. Falha de sessão (401) não é tratada aqui: quem
   avisa e redireciona é a casca (`AppShell`), como já acontece hoje.
8. Trocar de mês na casca refaz o pedido. O mês vive na URL, então o link é compartilhável.

## 4. Contrato (rascunho para o `arquiteto` refinar no OpenAPI)

```
GET /api/v1/dashboard?month=YYYY-MM     (requireAuth)
```

`200 OK` — schema `DashboardSummary`, `additionalProperties: false`, todos os campos `required`:

| Campo | Tipo | Faixa | Significado |
|---|---|---|---|
| `month` | string | `^\d{4}-\d{2}$` | o mês pedido, devolvido de volta |
| `incomeCents` | int64 | `minimum: 0` | receitas vivas da competência, **menos** os resgates marcados |
| `incomeCount` | int | `minimum: 0` | quantas linhas formam `incomeCents` |
| `creditCardExpenseCents` | int64 | `minimum: 0` | despesas vivas da competência em contas `credit_card`, **menos** os aportes marcados |
| `creditCardExpenseCount` | int | `minimum: 0` | quantas linhas formam o número acima |
| `investmentNetCents` | int64 | **com sinal, sem `minimum`** | aportes − resgates da competência |
| `investmentCount` | int | `minimum: 0` | aportes + resgates que formam o líquido |
| `creditCardAccountCount` | int | `minimum: 0` | contas `credit_card` **vivas** da casa (inclui arquivadas? **não** — ver §6) |
| `investmentCategoryCount` | int | `minimum: 0` | categorias de natureza `investment`/`redemption` da casa, **arquivadas incluídas** |

`investmentNetCents` é, junto com o saldo de conta, um dos pouquíssimos campos de dinheiro do
projeto **sem `minimum: 0`** — e isso é deliberado, está na lição de backend de 18/09/2026.

Erros: `401` sem sessão · `400` com `fields.month` quando o mês falta ou não é `AAAA-MM` (a mesma
mensagem de `GET /reports/by-category`) · `500` genérico, com o detalhe só no log.

> **O `InvestmentTotals` de `GET /investments` não muda.** Ele continua sem líquido (spec 0006 §2.2),
> por decisão do usuário na mesma data em que pediu o líquido aqui.

## 5. Dados (rascunho para o `arquiteto-dados`)

**Nenhuma tabela nova, nenhuma coluna nova, nenhuma migração.** O schema continua v4.

A leitura é **uma** consulta agregada sobre `transactions`, no molde do `Summary` que já existe:
`WHERE household_id = ? AND deleted_at IS NULL AND competence_month = ?`, `GROUP BY kind`, com as
parcelas marcadas saindo por `SUM(CASE WHEN category_id IN (?) THEN … END)` e a parcela do cartão por
`SUM(CASE WHEN account_id IN (?) THEN … END)` — o mesmo idioma portátil de `summaryProjecaoComMarcados`,
com os ids sempre em placeholder, nunca no texto. Índice usado:
`ix_transactions_competence (household_id, competence_month)`.

As duas listas auxiliares (categorias da casa, contas da casa) já são carregadas por outras rotas e
têm teto de domínio (`category.MaxPerHousehold` = 200, `account.MaxPerHousehold` = 50), o que mantém
os dois `IN (...)` dentro do limite de parâmetros dos quatro dialetos.

Conjunto vazio **nunca** vira consulta com `IN ()` (ADR-029f): sem categoria de investimento, a
coluna marcada simplesmente não entra na projeção; sem cartão, a do cartão também não.

> **Revisão de 18/09/2026 — a forma da consulta acima foi SUPERADA pelo ADR-031(b).** Este §5 era
> rascunho declarado para o `arquiteto-dados`, e o desenho proposto não fecha, por dois motivos
> medidos: (1) a condição do cartão como **terceira coluna condicional** exigiria excluir os marcados
> por `category_id NOT IN (?)`, e sem a guarda `category_id IS NULL OR` isso **perde toda despesa de
> cartão sem categoria** nos quatro dialetos (lógica de três valores: `NULL NOT IN (...)` é `NULL`, e
> `CASE WHEN NULL` cai no `ELSE`) — o estado normal logo depois de importar uma fatura; (2) o
> orçamento chegaria a `2 + 4·200 + 4·50 = 1002` parâmetros, **acima do piso de 999** do SQLite que o
> projeto adota como teto de portabilidade.
>
> **A forma que vale:** `GROUP BY kind, account_id`, com os ids de conta **fora do SQL** — o conjunto
> de cartões é interseccionado em Go sobre a lista de contas da própria casa. Orçamento de **404**
> parâmetros no pior caso, saída de ≤ 2 × `account.MaxPerHousehold` linhas, e só as duas colunas de
> investimento seguem condicionais. O restante deste §5 (uma consulta, sem migração, índice
> `ix_transactions_competence`, nada de `IN ()` vazio) continua valendo inteiro.

## 6. Segurança

- **BOLA (risco nº 1):** `household_id` vem do token, como em toda rota; `month` é o **único**
  parâmetro lido, e qualquer outro (`householdId=`, `accountId=`) é ignorado sem efeito. A conta de
  cartão e a categoria de investimento entram no `IN` **depois** de terem sido listadas pela casa do
  token — id de outra casa não tem como chegar ao `WHERE`.
- **Leitura pura:** sem escrita, sem `UnitOfWork`, sem auditoria (o mesmo desenho do `internal/report`,
  ADR-027). Nada a desfazer, nada a rastrear.
- **Superfície nova:** uma rota `GET`. Ela é a rota da **home**, então é a mais chamada do app — fica
  sob o limitador **global por IP**, como `/reports/by-category`, e sem limitador próprio: uma
  consulta agregada por pedido, com teto de linhas dado pelo mês.
- **Dado sensível:** nenhum campo novo; o payload é só dinheiro agregado da própria casa. Log de erro
  leva `request_id` e operação, **nunca** centavos, nome de conta ou de categoria (S8).
- **Contas arquivadas:** o gasto de um cartão **arquivado** continua contando no mês (arquivar não
  apaga o passado, `PLANOS.md` §4.4), mas `creditCardAccountCount` conta só as **vivas e não
  arquivadas** — é o número que responde "você tem um cartão cadastrado?", que é a pergunta do estado
  vazio. Os dois critérios são diferentes de propósito, e cada um tem teste.

## 7. Decisões de entrevista (18/09/2026, respondidas pelo usuário)

1. **Líquido de investimento vale só no painel.** A tela `/investimentos` mantém aportes e resgates
   separados, sem derivado.
2. **Receita não inclui resgates.** Incluí-los faria R$ 1.000 resgatados aparecerem como `+1.000` na
   receita e `−1.000` no investimento, na mesma faixa — o mesmo dinheiro contado duas vezes, e a
   receita do painel divergindo da faixa "Entrou" de `/lancamentos` (ADR-029e).
3. **Cartão é um número só**, somando todos os cartões, sem abertura por conta.
4. **Investimento aparece só como líquido**, sem os dois números de apoio.

## 8. Critérios de aceite (viram os testes do `qa-testes`)

**Números**

1. Mês com 3 receitas (R$ 5.000), 2 despesas comuns (R$ 800), 1 aporte de R$ 2.000 e 1 resgate de
   R$ 350 → `incomeCents = 500000`, `investmentNetCents = 165000`, `investmentCount = 2`.
2. Mês em que o resgate supera o aporte (aporte R$ 500, resgate R$ 900) → `investmentNetCents = -40000`.
   O número negativo chega à tela com sinal, e o teste de componente confere o texto `−R$ 400,00`.
3. Mês sem nenhum investimento → `investmentNetCents = 0` e `investmentCount = 0` (nunca ausente,
   nunca `null`).
4. `incomeCents` do painel é **idêntico** ao `summary.incomeCents` de `GET /transactions?month=` do
   mesmo mês — um teste cruzado, como o `TestRelatorioPorCategoriaBateComSummary` da E6a.
5. `investmentNetCents` é **exatamente** `monthly.contributionsCents − monthly.redemptionsCents` de
   `GET /investments?month=` do mesmo mês — teste cruzado com a outra tela.

**Transferência interna (o corte que o usuário pediu)**

6. Mês com uma transferência de R$ 1.000 da conta corrente para o cartão: nenhum dos três números
   muda em relação ao mesmo mês sem ela — inclusive `creditCardExpenseCents`, porque a perna que
   entra no cartão é `transfer_in` e não é gasto.
7. Pagar a fatura do cartão (transferência para a conta do cartão) **não** reduz nem aumenta
   `creditCardExpenseCents`.

**Cartão**

8. Duas contas `credit_card` com gasto no mês → um número, a soma das duas.
9. Despesa em conta `checking` **não** entra em `creditCardExpenseCents`.
10. Aporte lançado numa conta `credit_card` entra em `investmentNetCents` e **não** em
    `creditCardExpenseCents` (não se conta o mesmo dinheiro duas vezes na mesma faixa).
11. Casa sem nenhuma conta `credit_card` → `creditCardExpenseCents = 0` e
    `creditCardAccountCount = 0`; a tela mostra o texto de "nenhum cartão", não `R$ 0,00`.
12. Cartão **arquivado** com gasto no mês → o gasto conta; `creditCardAccountCount` não o conta.

**Abuso e bordas**

13. `GET /dashboard` sem `month` → `400` com `fields.month`; com `month=2026-13`, `month= 2026-09`
    (espaço), `month=2026-9` → `400`, sem normalizar nada.
14. Sem cookie de sessão → `401`, e nenhuma consulta é executada.
15. **BOLA:** a casa A tem gasto de cartão e investimento; o token da casa B (com conta e categoria
    de nomes idênticos) recebe `0` em tudo — nenhum centavo, nenhum id e nenhum nome da casa A
    aparece na resposta nem no log.
16. `?month=2026-09&householdId=<id da casa A>` com token da casa B → o parâmetro é ignorado, a
    resposta é a da casa B.
17. Mês com 10.000 lançamentos vivos → **uma** consulta agregada, tempo medido e registrado no
    relatório da entrega (referência: 20,8 ms da E6a no mesmo volume).
18. A tela faz **um** pedido de rede para montar a faixa (conferido no teste de componente com o
    cliente da API mockado, e no Playwright por `page.waitForRequest`).

**Design (checklist do `designer-ui` com a tela pronta)**

19. Sem card decorativo, sem gradiente, sem emoji, sem ícone grande ao lado do número.
20. Investimento negativo é comunicado por **sinal e palavra**, não só por cor — a regra de
    `docs/DESIGN.md` sobre saldo negativo vale aqui.
21. Contraste AA nos três números, incluindo o negativo, nos temas claro e escuro.

## 9. Encaminhamento

Aprovada esta spec, seguir com `/nova-feature` passando-a como entrada do `arquiteto` — que decide
se o pacote é `internal/dashboard` novo (no molde de `internal/report`) ou uma rota dentro de um
pacote existente, e registra a decisão como ADR em `docs/ARQUITETURA.md`. Revisão de segurança
obrigatória antes da entrega.
