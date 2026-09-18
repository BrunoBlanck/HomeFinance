# Spec 0004 — Lançamentos enxutos e importação de extratos e faturas (entrega E2)

**Data:** 16/09/2026 · **Fase:** 4 (núcleo financeiro) · **Entrega:** E2 do `PLANOS.md`
**ADRs relacionados:** ADR-003 (dinheiro em centavos), ADR-008 (GORM + AutoMigrate), ADR-013 (sem FK
física, CSRF, cookies), ADR-015 (tipos TS do OpenAPI), ADR-016 (transferência como par), ADR-017
(saldo derivado, dois níveis de categoria), ADR-019 **(a) e (b)** (fuso na casa, `BRL` fixo),
**ADR-023** (fatura de cartão, competência ≠ caixa — supera o ADR-019 (c) e (d)), **ADR-024**
(importação: plugin, duas fases, ZIP com senha), **ADR-025** (deduplicação em camadas).

> Esta spec é **normativa**. Onde ela e a documentação geral divergirem, vale a documentação geral
> (`AGENTS.md` e `docs/SEGURANCA.md`), e o desvio deve ser reportado, não implementado em silêncio.

> **Decisão de execução (16/09/2026):** a entrega vai **inteira**, sem o corte E2a/E2b previsto no
> risco RE3 (§11). O corte continua descrito lá como plano de contingência, não como plano.

---

## 1. Escopo

### 1.1 Entra

**Domínio de lançamentos (enxuto).** `transactions` com índices · repositório com isolamento por casa
· `GET /transactions` filtrado por mês e conta, com cursor e `summary` · `GET /transactions/{id}` ·
`DELETE /transactions/{id}` (exclusão lógica) · **saldo derivado passa a somar os lançamentos**, o que
fecha o ADR-017 na prática · `UsageChecker` de conta e de categoria finalmente registrados, o que paga
a dívida declarada na spec 0003 §1 · criação do **par de transferência** (ADR-016) apenas como efeito
da importação, sem rotas `/transfers`.

**Fatura de cartão (conceito novo — ADR-023).** `card_statements` · `transactions.competence_month` e
`transactions.statement_id` · `accounts.statement_closing_day` e `statement_due_day` ·
`GET /card-statements` e `GET /card-statements/{id}`, somente leitura: no v1 a fatura nasce da
importação.

**Importação (o coração da entrega).** Fluxo de duas fases (enviar → revisar → confirmar) com
**staging no banco** · CSV solto e **ZIP protegido por senha** (ZipCrypto, extração em memória) ·
arquitetura de parser como plugin por **(instituição × tipo de documento)** com detecção por cabeçalho
· **quatro parsers prontos, registrados e testados** — extrato e fatura do Nubank e do C6 (§7.3) ·
**deduplicação em camadas com garantia no banco** (§4) ·
classificação de pagamento de fatura com opção de virar transferência · auditoria por lote · limites
rígidos e rate limit próprio.

**Contas (delta).** `institution` na conta, exposta em `POST`/`PATCH`/`GET /accounts`, no OpenAPI e no
seletor da UI.

**Frontend.** Tela `/lancamentos` (lista densa por mês e conta, exclusão com confirmação, "carregar
mais" por cursor) · fluxo `/importar` em três passos · componente `FileField` · marcação de duplicata
que não depende de cor.

### 1.2 Fica explicitamente de fora

- **CRUD manual de lançamento** (`POST`, `PATCH`, criação rápida, diálogo de novo lançamento) → E2b.
- **Filtros densos** (`categoryId`, `kind`, `q`, `minCents`/`maxCents`, `sort`) → E2b. Aqui o filtro é
  `month` + `accountId` + paginação, e nada mais. A busca normalizada (P2) sai junto.
- **Rotas `/transfers`** (`POST`, `PATCH`, `DELETE`) → E2b. Nesta entrega a transferência só nasce
  pela importação.
- **Edição de lançamento importado** (recategorizar em massa, renomear descrição) → E2b.
- **Regras de auto-categorização** ("todo Ifood vira Alimentação") → backlog, exige spec própria.
- **Parcelamento** (`Parcela 3/10` na fatura) → backlog do `PLANOS.md` §14. O texto da parcela é
  preservado na descrição, e nada além disso.
- **Estorno como operação** — crédito na fatura entra como receita (§3.4, D4).
- **Conciliação automática** entre extrato e fatura → o pareamento é **proposto**, nunca automático.
- **Contas fixas, orçamentos, painel, relatórios, gráficos e exportação** → E3–E6.
- **OFX, Open Finance e outros bancos** → fora. O plugin existe para que entrem depois sem tocar no
  núcleo, não para que entrem agora.
- **AES-ZIP** (`method=99`) → recusado com erro explícito e testado (§2.1).

### 1.3 Dívidas que esta entrega cria, e que precisam ficar visíveis

1. ~~**Os parsers do C6 não são registrados** enquanto o formato não chegar~~ — **RESOLVIDO
   (17/09/2026):** os dois parsers do C6 (extrato e fatura) foram implementados, registrados e
   testados a partir das amostras anonimizadas. A §7.3 registra o que foi preenchido de cada item do
   checklist. Arquivo do C6 agora é reconhecido pelo cabeçalho como qualquer outro.
2. **O teste de regressão de injeção de fórmula em CSV nasce aqui e é herdado pela E6** (§6.9). Sem
   ele, a ida e volta importar → exportar vira uma fórmula na planilha de alguém, e daqui a três
   entregas ninguém lembra de testar.

---

## 2. Achados verificados que a implementação não pode contradizer

Tudo nesta seção foi conferido nos arquivos reais em 16/09/2026 — é fato medido, não hipótese.

### 2.1 Os ZIPs do C6 — o que a stdlib faz e o que ela não faz

Saída real de um programa Go rodado contra os dois ZIPs do C6:

```
name="01M2NBB5D3KXY28NRFRYJR2DRM.csv" method=8 flags=0x0809 crc=a5fe0380 csize=731 usize=2103
  ReaderVersion=20 ExtraLen=0
  Open ok: lidos=0 err=flate: corrupt input before offset 5
  OpenRaw ok: bytes=731 err=<nil>
name="Fatura_2026-09-15.csv" method=8 flags=0x0809 crc=52e8e1c7 csize=1641 usize=7434
  ReaderVersion=20 ExtraLen=0
  Open ok: lidos=0 err=flate: corrupt input before offset 1
  OpenRaw ok: bytes=1641 err=<nil>
```

O que isso estabelece, e é **normativo**:

1. **É ZipCrypto (PKWARE tradicional), não AES.** `method=8` (deflate) + `ReaderVersion=20` + nenhum
   campo extra. AES do WinZip apareceria como `method=99`, com campo extra `0x9901` e versão 51.
2. **A stdlib não detecta a criptografia: ela entrega lixo.** `f.Open()` não olha o bit 0 do flag,
   manda os bytes cifrados direto para o inflate e devolve `flate: corrupt input`. **Quem checa
   `f.Flags&0x1` somos nós**, para responder `IMPORT_PASSWORD_REQUIRED` em vez de "arquivo
   corrompido".
3. **`f.OpenRaw()` devolve os bytes cifrados e comprimidos intactos** — é o gancho da implementação:
   decifrar, inflar com `compress/flate` e conferir o CRC32 contra `f.CRC32`, que vem do diretório
   central e está preenchido mesmo com o bit 3 ligado.
4. **Armadilha que mataria a primeira implementação:** `flags=0x0809` tem o **bit 3 (data descriptor)
   ligado**. Pela APPNOTE, nesse estado o byte de verificação do cabeçalho de 12 bytes do ZipCrypto é
   o **byte alto da hora DOS**, e não o byte alto do CRC. A implementação que compare com `CRC>>24` —
   que é o que quase todo exemplo público faz — **recusa todo arquivo do C6 mesmo com a senha certa**.
   **Decisão: ignorar o byte de verificação.** O único veredito de senha correta é o CRC32 do conteúdo
   inflado; senha errada custa um inflate limitado, e isso já está sob rate limit.

### 2.2 Os arquivos do Nubank

| Fato | Verificação |
|---|---|
| Terminador de linha | **LF nos dois** (CR = 0). O `encoding/csv` absorveria CRLF de qualquer jeito; o que importa é que o hash do arquivo seja calculado sobre o **conteúdo normalizado** (§4.1), nunca sobre os bytes crus |
| BOM | ausente nos dois. O parser remove mesmo assim: reabrir e salvar no Excel devolve o arquivo com BOM, e cabeçalho precedido de BOM não casa com assinatura nenhuma |
| Fatura | valor entre aspas em formato pt-BR, com ponto de milhar e **espaço depois do sinal**: `"- 2.859,82"` |
| Sinal | **invertido entre os dois documentos**: no extrato, negativo é saída; na fatura, **positivo é saída** |

### 2.3 Fixtures

As fixtures anonimizadas vivem em `backend/internal/importer/nubank/testdata/`
(`nubank_checking_v1.csv`, 13 linhas de dados; `nubank_card_statement_v1.csv`, 15 linhas). Elas
preservam todas as características de formato e acrescentam, de propósito, dois casos que o arquivo
real não tinha:

- **extrato** — par de linhas **idênticas em conta, data, valor e descrição** (12/08, `-11.00`,
  "PADARIA EXEMPLO LTDA") com **identificadores diferentes**: exercita a chave natural;
- **fatura** — par **completamente idêntico** (`Cafe Exemplo`, `2026-08-14`, `"11,00"`, duas vezes):
  exercita o **ordinal** da §4.3, que é o que impede compra legítima repetida de ser engolida.

O arquivo real **nunca** entra no repositório: `Exemplos/` está no `.gitignore` e nunca foi rastreado.
Fixture de parser novo nasce anonimizada — nome, valor e documento trocados —, sem exceção.

---

## 3. Modelo de dados (schema v3)

Agnóstico de dialeto. O `arquiteto-dados` valida contra a §6 do `PLANOS.md` (armadilhas P1–P9) antes
de qualquer modelo ser escrito.

### 3.1 `transactions`

| Coluna | Tipo Go / SQL | Regra |
|---|---|---|
| `id` | `string` / `varchar(36)` | UUID v7 gerado no service |
| `household_id` | `varchar(36)` not null | **sempre do token** |
| `kind` | `varchar(12)` not null | `income` · `expense` · `transfer_out` · `transfer_in` |
| `account_id` | `varchar(36)` not null | validado como da casa, **na mesma transação** |
| `category_id` | `varchar(36)` null | **nulo permitido em lançamento importado** (§3.4, D3); proibido em transferência |
| `amount_cents` | `int64` / `bigint` not null | sempre **positivo**; o sinal vem do `kind` |
| `description` | `varchar(140)` not null | já **sanitizada** (§6.8); pode ser vazia |
| `description_norm` | `varchar(140)` not null | derivada de `description` por `textnorm` |
| `occurred_on` | `varchar(10)` not null | data civil `YYYY-MM-DD` (D3 da spec 0003) |
| `year_month` | `varchar(7)` not null | **caixa**, derivado de `occurred_on` |
| `competence_month` | `varchar(7)` not null | **competência** (ADR-023): igual ao `year_month` fora de fatura, igual ao mês da fatura dentro dela |
| `transfer_group_id` | `varchar(36)` null | par de transferência (ADR-016) |
| `statement_id` | `varchar(36)` null | fatura a que a linha pertence |
| `source` | `varchar(12)` not null | `manual` · `import` |
| `import_batch_id` | `varchar(36)` null | rastreia o lote de origem — é o que substitui 10.000 linhas de auditoria (§6.10) |
| `external_id` | `varchar(64)` null | chave natural do banco (o UUID do Nubank); forense |
| `dedup_key` | `varchar(64)` not null | SHA-256 hex canônico (§4.3) |
| `dedup_ordinal` | `int` not null default 1 | ordinal da ocorrência idêntica (§4.3) |
| `created_by` | `varchar(36)` not null | quem lançou ou confirmou |
| `created_at` / `updated_at` | `timestamp` not null | UTC |
| `deleted_at` | `timestamp` null | soft delete |

**Índices** (todo composto começa por `household_id` — é o isolamento):

```
ux_transactions_dedup      UNIQUE (household_id, dedup_key, dedup_ordinal)   <- garantia anti-duplicata
ix_transactions_occurred          (household_id, occurred_on, id)            <- cursor da listagem
ix_transactions_account_occurred  (household_id, account_id, occurred_on)    <- extrato, saldo, dedup
ix_transactions_competence        (household_id, competence_month)           <- o mês do app
ix_transactions_statement         (household_id, statement_id)
ix_transactions_group             (household_id, transfer_group_id)
ix_transactions_batch             (import_batch_id)
ix_transactions_deleted_at        (deleted_at)
ix_transactions_category          (household_id, category_id)
```

Três regras de portabilidade que precisam sobreviver à revisão:

- **Índice ascendente, sempre.** Nada de `DESC` na definição: o MySQL 8 tem índice descendente, os
  outros três variam, e os quatro varrem índice ascendente de trás para frente sem custo. O
  `ORDER BY occurred_on DESC, id DESC` usa `ix_transactions_occurred` normalmente.
- **`ux_transactions_dedup` tem três colunas, todas `NOT NULL`** — foge das armadilhas **P3** (o MSSQL
  trata NULLs como iguais em índice único) e **P4** (índice parcial não existe no MySQL). §4.5.
- **Largura do índice único:** 36 + 64 + 4. Em MySQL utf8mb4 dá cerca de 404 bytes, bem abaixo do teto
  de 3072 do InnoDB — não precisa de prefixo de índice.

### 3.2 `card_statements`

| Coluna | Tipo | Regra |
|---|---|---|
| `id` / `household_id` | `varchar(36)` | idem |
| `account_id` | `varchar(36)` not null | **obrigatoriamente conta `kind = credit_card`**, validado no service |
| `competence_month` | `varchar(7)` not null | `YYYY-MM` — **mês do vencimento** (§3.4, D2) |
| `closing_date` | `varchar(10)` not null | data civil do fechamento |
| `due_date` | `varchar(10)` not null | data civil do vencimento |
| `source` | `varchar(12)` not null | `import` · `manual` (só `import` no v1) |
| `created_at` / `updated_at` / `deleted_at` | | |

```
ux_card_statements  UNIQUE (household_id, account_id, competence_month)
(não existe índice comum ao lado do único: teria as mesmas colunas, na mesma ordem,
 e o único já atende a toda consulta que o comum atenderia)
```

Todas as colunas do índice único são obrigatórias (P3). É ele que torna "importar a fatura de setembro
duas vezes" uma operação que **reutiliza** a mesma fatura em vez de criar duas.

**Derivados, nunca colunas** — mesma disciplina do ADR-017 e do ADR-018:
`totalCents` = soma das despesas menos as receitas das linhas com aquele `statement_id` ·
`paidCents` = soma dos `transfer_in` com aquele `statement_id` ·
`status` = `paga` quando `paidCents >= totalCents`; senão `vencida` quando `due_date` é anterior a
hoje **no fuso da casa**; senão `em_aberto`.

### 3.3 `accounts` — colunas novas

`institution varchar(20) not null default 'other'` (`c6` · `nubank` · `other`, allowlist em código) ·
`statement_closing_day int null` · `statement_due_day int null` (1..31, só fazem sentido em
`credit_card`; nulo significa "não configurado", e aí a importação de fatura exige que o usuário
confirme as datas na revisão).

O `AutoMigrate` adiciona coluna com default sem destruir dado (ADR-008); o teste de migração parte de
banco **v2 povoado**, não de banco vazio.

**A instituição da conta NÃO escolhe o parser.** Se escolhesse, conta marcada como `other` ficaria sem
importação, e conta marcada errado importaria com o parser errado em silêncio — inversão de sinal na
fatura inteira, que é o pior defeito possível aqui. Quem escolhe o parser é a detecção pelo cabeçalho
(§7.1); a instituição da conta é **trava de consistência**: detectei `nubank` e a conta é `c6` → 422
`IMPORT_TARGET_MISMATCH`, e nada é importado. `institution = other` não trava nada, só perde a
checagem. A trava mais forte é a outra: **fatura só entra em conta `credit_card`, e extrato só entra
em conta que não seja `credit_card`**.

### 3.4 Decisões de modelo desta spec

**D1 — `competence_month` é `NOT NULL` e sempre preenchido, inclusive fora de cartão.** A alternativa
(anulável, com "nulo significa use o `year_month`") espalha um `COALESCE` por toda consulta futura e
vira armadilha no MSSQL no dia em que entrar num índice. Preencher sempre custa uma atribuição e faz o
`GROUP BY competence_month` funcionar igual nos quatro dialetos, sem `CASE`.

**D2 — a competência da fatura é o mês do VENCIMENTO, não o do fechamento.** Uma fatura com linhas de
06/08 a 05/09 que vence em 13/09 é a "fatura de setembro" para qualquer pessoa, e é assim que o app a
chama. Pelo fechamento, a mesma fatura se chamaria "agosto" e brigaria com o extrato de setembro, onde
o pagamento dela aparece.

**D3 — lançamento importado pode nascer sem categoria; lançamento manual, não.** O `PLANOS.md` §3.3
exige `category_id` em `income`/`expense`: a regra é **mantida na criação manual** (E2b) e **relaxada
na importação**. As duas alternativas são piores — obrigar a escolher categoria em 300 linhas antes de
confirmar mata o fluxo, e semear uma categoria "A classificar" polui a taxonomia do usuário com uma
categoria falsa que depois não se apaga porque está em uso. **Consequência aceita, que a interface tem
de assumir:** existe o estado "sem categoria", ele aparece com destaque em `/lancamentos` ("N
lançamentos sem categoria"), e os relatórios da E6 precisam de um balde próprio — que precisariam de
qualquer jeito.

**D4 — crédito de fatura que não seja pagamento vira `income`.** "Ajuste a crédito" é estorno, e
modelar estorno de verdade (anular parcialmente um lançamento existente) é do tamanho de uma entrega.
No v1 ele entra como receita na conta do cartão: o **saldo fica correto** e o **relatório de receita
fica levemente inflado**. Limitação registrada, visível na revisão (a linha aparece marcada como
"crédito na fatura"), com saída pelo item "estorno" do `PLANOS.md` §14.

### 3.5 `import_batches` (o lote)

| Coluna | Tipo | Regra |
|---|---|---|
| `id` / `household_id` | `varchar(36)` | |
| `account_id` | `varchar(36)` not null | conta de destino, validada como da casa |
| `created_by` | `varchar(36)` not null | |
| `institution` / `doc_kind` / `format_id` | `varchar(20)` / `varchar(16)` / `varchar(40)` not null | parser que ganhou a detecção |
| `file_name` | `varchar(200)` not null | **sanitizado**: sem separador de caminho, sem caractere de controle |
| `content_sha256` | `varchar(64)` not null | hash do **conteúdo normalizado**, nunca dos bytes do ZIP |
| `row_count` · `imported_count` · `skipped_count` · `blocked_count` · `restored_count` · `rejected_count` | `int` not null default 0 | |
| `min_date` / `max_date` | `varchar(10)` not null | janela do arquivo |
| `suggested_competence_month` | `varchar(7)` null | só em fatura |
| `suggested_closing_date` / `suggested_due_date` | `varchar(10)` null | só em fatura |
| `status` | `varchar(12)` not null | `pending` · `committed` · `discarded` · `expired` |
| `expires_at` | `timestamp` not null | `created_at` mais 24 h |
| `committed_at` | `timestamp` null | |
| `created_at` / `updated_at` | | |

```
ix_import_batches_household  (household_id, created_at)
ix_import_batches_content    (household_id, content_sha256)   <- aviso "este arquivo já foi importado"
ix_import_batches_expires    (expires_at)                     <- janitor
```

### 3.6 `import_rows` (o staging, efêmero)

| Coluna | Tipo | Regra |
|---|---|---|
| `id` / `household_id` | `varchar(36)` | `household_id` repetido **de propósito**: o filtro de BOLA não pode depender de join |
| `batch_id` | `varchar(36)` not null | |
| `seq` | `int` not null | ordem no arquivo (1-based); é também a chave do cursor |
| `line_no` | `int` not null | linha física no CSV, para a mensagem de erro |
| `kind` | `varchar(12)` not null | |
| `occurred_on` | `varchar(10)` not null | |
| `amount_cents` | `bigint` not null | |
| `description` / `description_norm` | `varchar(140)` not null | já sanitizadas — **é exatamente o que será gravado** |
| `external_id` | `varchar(64)` null | |
| `dedup_key` | `varchar(64)` not null | |
| `status` | `varchar(24)` not null | taxonomia da §4.6 |
| `reject_reason` | `varchar(32)` null | **código**, nunca o conteúdo da linha |
| `match_transaction_id` | `varchar(36)` null | o lançamento existente que motivou a marcação |
| `created_at` | | |

```
ux_import_rows_seq  UNIQUE (batch_id, seq)
ix_import_rows_batch       (household_id, batch_id, seq)
```

**A linha crua NÃO é guardada.** Ela carregaria CPF mascarado, agência e conta de terceiros num lugar
novo, para exibir ao usuário algo **diferente** do que será gravado — o que confunde a revisão em vez
de ajudar. A revisão mostra o que vai entrar. Linha que não parseia vira `status = rejeitado`, com
`line_no` e `reject_reason`, sem conteúdo.

**Ciclo de vida:** as linhas são **apagadas fisicamente** no confirm e no discard; o lote sobrevive
como histórico, só com os contadores. O janitor varre `expires_at` vencido com `status = pending`
(marca `expired` e apaga as linhas) e apaga lotes terminais com mais de 180 dias, alinhado à retenção
de auditoria.

---

## 4. Deduplicação — a regra que esta entrega existe para cumprir

Exigência do usuário, literal: importação com critérios rigorosos para **nunca** importar linha
duplicada, de modo que um erro dele não vire problema na aplicação. A política é: **por padrão a linha
repetida é barrada; na revisão ela aparece marcada e o usuário libera caso a caso; nada entra sem a
confirmação dele.** A decisão estrutural está no **ADR-025**; o que segue é normativo para a
implementação.

São quatro camadas, e cada uma tem um papel diferente. Confundi-los é o erro clássico.

### 4.1 Camada 1 — hash do arquivo: AVISO, nunca bloqueio

`content_sha256` é calculado sobre o **conteúdo já extraído, sem BOM e com quebra de linha
normalizada para LF** — sobre os bytes do ZIP ele seria inútil, porque duas compactações do mesmo CSV
diferem em timestamp, nível de compressão e vetor da cifra.

Ele serve para **um aviso na fase 1** — "você já importou este mesmo arquivo em 13/09, com 14 linhas;
quer revisar mesmo assim?" — e **não impede** o envio. Motivo: ele é frouxo e rigoroso ao mesmo tempo.
Frouxo porque basta uma linha nova para o hash mudar, deixando passar um arquivo 99% repetido.
Rigoroso porque bloquear travaria quem reimporta de propósito para pegar as três linhas que o banco
acrescentou. **Ele economiza tempo; não é defesa.**

### 4.2 Camada 2 — chave natural: com espaço de nomes, e sem confiança cega

O `Identificador` do Nubank é um UUID por transação, estável entre downloads. É a melhor chave que
existe, e ainda assim:

- **precisa de espaço de nomes** — um id só é único dentro da instituição. A chave é
  `v1|nat|<institution>|<accountId>|<externalId>`, nunca o UUID pelado;
- **é dado controlado pelo cliente** — arquivo editado à mão pode repetir o mesmo id em linhas
  diferentes ou dar ids diferentes a linhas iguais (dedup não vê nada). Não é ameaça de segurança,
  porque o dado é do próprio usuário, mas é o motivo de a chave natural **nunca** dispensar a
  marcação fraca da §4.6. O id repetido **no mesmo arquivo** não é perda silenciosa: o identificador
  do emissor é único por transação, então a 2ª ocorrência é classificada como `duplicado_exato`
  (barrada, visível na revisão, não liberável) e só a 1ª entra — a mesma regra que a escrita aplica
  ao recusar ordinal > 1 em chave natural (§4.8);
- **`account_id` entra na chave, e isso é deliberado.** Sem ele, importar o arquivo na conta errada e
  depois tentar na conta certa ficaria bloqueado para sempre. Com ele, a segunda importação passa — e
  a colisão de `external_id` em **outra conta da mesma casa** vira marcação fraca explícita ("esta
  linha já foi importada na conta X"). O usuário vê o erro dele em vez de bater numa parede.

### 4.3 Camada 3 — chave derivada com ORDINAL: o ponto central

A chave derivada óbvia — `hash(conta, data, valor, descrição)` — **apaga compra legítima repetida, em
silêncio**. Dois cafés de R$ 11,00 no mesmo dia na mesma padaria são a mesma tupla e são dois gastos
reais. **Num app de dinheiro, sumir com um lançamento é pior do que duplicar um**: duplicata o usuário
vê e apaga; linha sumida ele descobre três meses depois conferindo o extrato. É por isso que a fixture
da fatura tem o par `Cafe Exemplo` idêntico (§2.3).

A correção é o **ordinal de ocorrência**:

- chave derivada = `v1|der|<accountId>|<kind>|<occurredOn>|<amountCents>|<descriptionNorm>`,
  SHA-256 hex, gravada em `dedup_key`;
- **a tupla não é única**: a unicidade é `(household_id, dedup_key, dedup_ordinal)`;
- o ordinal é **a enésima ocorrência daquela tupla naquela casa**. Dois cafés idênticos entram como
  `#1` e `#2`;
- reimportar o mesmo arquivo reproduz `#1` e `#2`, que colidem com os existentes: **os dois são
  barrados**.

Três detalhes de implementação, cada um capaz de quebrar a garantia sozinho:

1. **A chave é calculada sobre os valores canônicos do domínio** — `kind`, `amount_cents` positivo,
   `occurred_on` e a `description_norm` **já sanitizada e já truncada em 140** —, nunca sobre o texto
   do arquivo. Calculada sobre o texto cru, a truncagem do armazenamento faria a reimportação gerar
   chave diferente da gravada, e o dedup simplesmente não funcionaria. É um erro fácil de cometer e
   invisível em teste pequeno.
2. **O `v1` no início da string é a versão da chave.** Mudar o sanitizador de descrição muda todas as
   chaves futuras e cega o dedup contra o passado — não se bumpa isso sem ADR.
3. **Concorrência:** o ordinal é atribuído **dentro da transação do confirm**, por
   `SELECT MAX(dedup_ordinal) WHERE household_id = ? AND dedup_key = ?` mais incremento em memória
   para as linhas do próprio lote. Dois confirms simultâneos podem calcular o mesmo ordinal; o índice
   único derruba o segundo, e o serviço trata a violação como "linha bloqueada", **nunca como 500**.

**Contagem do ordinal, precisa:** `existentes(T)` é contado **no banco, incluindo linhas
soft-deleted** — o índice único conta com elas, e ignorá-las produziria colisão no INSERT. As linhas do
arquivo com a mesma tupla são numeradas em sequência a partir de `existentes(T) + 1`. A i-ésima linha
do arquivo com tupla T é **suspeita** quando `i <= existentes_vivos(T)`.

**O caso do mesmo período repartido em dois arquivos** (A cobre 01–15, B cobre 01–31):

- o hash do arquivo não ajuda em nada — é a prova de que ele não pode ser a defesa principal;
- com chave natural, resolve limpo: as linhas de 01–15 em B têm o mesmo `external_id`, ordinal 1 já
  ocupado, barradas;
- com chave derivada, resolve **se e somente se o ordinal for contado contra o BANCO, não contra o
  arquivo**. Contado por arquivo, acertaria por acidente com dois arquivos e erraria com três;
- **caso patológico que o desenho acerta:** A tem um café de R$ 11,00 em 05/08 e B tem **dois**, porque
  o segundo caiu depois do download de A. A primeira linha de B é marcada (é a mesma de A) e a segunda
  entra como ocorrência nova. **Sem ordinal, as duas seriam barradas e um gasto real sumiria.**

### 4.4 A chave é obrigatória em TODA linha, inclusive nas que não vêm de importação

| Origem | `dedup_key` | `dedup_ordinal` |
|---|---|---|
| Importado com chave natural | `SHA256("v1|nat|<inst>|<accountId>|<externalId>")` | quase sempre 1 |
| Importado sem chave natural | `SHA256("v1|der|<accountId>|<kind>|<data>|<valor>|<descNorm>")` | enésima ocorrência |
| **Manual (E2b)** | `SHA256("v1|man|" + transaction.id)` | 1 |
| **Perna de transferência criada pela importação** | `SHA256("v1|pair|" + transferGroupID + "|in")` | 1 |

O lançamento manual carrega uma chave única por construção, que nunca barra nada: ela existe só para a
coluna poder ser `NOT NULL` e para a regra ser uma só. Sem isso, voltaríamos a ter coluna anulável em
índice único, que é o problema da §4.5.

### 4.5 Camada 4 — o índice único: por que NÃO pode ser parcial

**Índice único parcial está fora, e a chave tem de ser um hash sempre preenchido.** Três motivos
independentes, cada um decisivo sozinho:

1. **O MySQL não tem índice parcial** — nem com outra sintaxe: não existe. É a armadilha **P4** do
   `PLANOS.md` §6, a mesma que fez a spec 0003 abrir mão de índice único em nome de conta.
2. **O MSSQL trata NULLs como iguais em índice único** (armadilha **P3**). Um `external_id` anulável
   com índice único deixaria passar **exatamente uma** linha sem id por casa no SQL Server: todos os
   lançamentos manuais e a fatura inteira colidiriam entre si. É catastrófico e só apareceria na E8,
   quando a suíte multi-banco rodar.
3. **O `AutoMigrate` não expressa índice parcial de forma portátil**, o que levaria a DDL por dialeto
   com `Exec` — exatamente o que o ADR-008 e o `BANCO-DE-DADOS.md` mandam evitar.

### 4.6 Taxonomia da revisão — o que o usuário vê e qual é o default

A exigência "nada entra sem confirmação" é cumprida pelo par **status + default**. O índice único é a
rede embaixo do trapézio, não o trapézio.

| Status | Significado | Default | Liberável? |
|---|---|---|---|
| `novo` | nenhuma colisão | **importar** | — |
| `repetido_no_arquivo` | é a 2ª ou enésima ocorrência idêntica dentro do próprio arquivo, e o banco não tem tantas — **só chave derivada** | **importar**, com marcação "2ª ocorrência idêntica" | — |
| `duplicado_exato` | `dedup_key` e ordinal já existem numa linha **viva**; ou o mesmo `Identificador` (chave natural) já apareceu numa linha anterior do próprio arquivo (sem `matchTransactionId`) | **barrar** | **Não.** A UI mostra link para o lançamento existente, quando existe |
| `duplicado_excluido` | a linha existente está soft-deleted | **barrar** | **Sim** — liberar **restaura** a linha existente (§4.7) |
| `possivel_duplicado` | marcação fraca: mesma conta, mesmo valor, data ±3 dias, descrição diferente; ou `external_id` já usado em outra conta da casa | **barrar** | Sim — entra como ocorrência nova |
| `pagamento_de_fatura` | classificada como pagamento ou recebimento de fatura | **barrar** | Sim — e a opção oferecida é "registrar como transferência para a conta X" |
| `rejeitado` | linha malformada (valor, data ou colunas) | **barrar** | Não |

A **marcação fraca** existe para o buraco que a chave derivada tem: **descrição que muda entre
downloads**. Banco que enriquece o nome do estabelecimento depois gera chave derivada diferente e
passaria batido; a marcação por (conta, valor, data ±3 dias) pega, marca e deixa o usuário decidir.
Ela **não** entra no índice único — heurística no banco vira bloqueio que ninguém consegue desfazer.

### 4.7 Soft delete contra índice único — o beco sem saída, e a saída

Consequência inevitável do índice: **linha excluída logicamente continua ocupando a chave**. Quem
apagar um lançamento importado e reimportar o arquivo bate num `duplicado_exato` que não consegue
liberar, porque o índice recusaria o INSERT de qualquer jeito.

**Regra:** liberar uma linha `duplicado_excluido` **restaura a linha existente** (`deleted_at` volta a
ser nulo) em vez de inserir outra. É a operação certa conceitualmente — a identidade já existe, e
"importar de novo" é desfazer a exclusão —, não briga com o índice e é auditável.

Isso abre **exceção estreita e declarada** ao `PLANOS.md` §4.4 ("reverter exclusão de lançamento não é
exposto no v1"): só pela importação, só na mesma casa, só a mesma linha, e **sem tocar em nenhum campo
financeiro** — a restauração altera `deleted_at` e `updated_at`, e nada mais. Vira item fixo de revisão
de segurança e ação de auditoria própria (`transaction.restored`).

### 4.8 Custo em consultas: N linhas não podem virar N queries

Um arquivo de 10.000 linhas não pode gerar 10.000 SELECTs. A análise carrega, **numa única consulta**,
os lançamentos existentes da conta na janela `[min_date - 3d, max_date + 3d]`, projetando apenas
`(id, occurred_on, amount_cents, kind, description_norm, external_id, dedup_key, dedup_ordinal,
deleted_at)`, e casa tudo em memória. A consulta usa `ix_transactions_account_occurred`. Se a janela
trouxer mais de **20.000** linhas, o arquivo é recusado com `IMPORT_FILE_REJECTED` e a orientação de
importar por período menor — melhor recusar com explicação do que estourar memória.

**A janela de datas não basta para a chave natural.** Ela não embute a data (§4.4), então a gêmea de uma
linha **re-datada** — o usuário corrigindo a data no CSV, o emissor re-datando uma pendente que liquidou
— cai fora da janela, a linha volta como `novo` (que entra por default) e o ordinal calculado vira 2,
que o índice único aceita: a mesma transação entra duas vezes, em silêncio. Por isso, quando o documento
tem chave natural, a análise faz **uma segunda consulta em lote, sem filtro de data**
(`WHERE household_id = ? AND dedup_key IN (...)`, fatiada em blocos de 200 pelo teto de parâmetros por
comando), projetando apenas `(id, dedup_key, dedup_ordinal, deleted_at)` e funde o resultado nos
existentes antes de classificar. Continua sendo consulta **por arquivo**, nunca por linha. A chave
derivada não precisa dela: `occurred_on` está dentro do hash, então toda gêmea dela já está na janela.

Em cima disso, `transaction.Service.CreateBatch` **recusa ordinal > 1 em linha de chave natural**
(`ErrDuplicateDedup`, que vira `blocked` na resposta). Ordinal > 1 só tem significado na chave derivada —
dois cafés iguais no mesmo dia são dois gastos reais; dois registros com o mesmo `Identificador` do
emissor são a mesma transação. A classificação diz **o mesmo**: a 2ª ocorrência da mesma chave natural
dentro do arquivo é `duplicado_exato` (barrada, não liberável), nunca `repetido_no_arquivo`. As duas
camadas têm de concordar — se a prévia liberasse a linha por default, a escrita a recusaria e desfaria o
lote inteiro, com a tela dizendo "outra importação gravou primeiro" onde não houve corrida nenhuma.
A recusa na escrita fica para a corrida real, que é o único caso em que essa mensagem é verdadeira.

**A análise da fase 1 é apenas um palpite.** Entre o envio e a confirmação, o outro morador pode ter
importado o mesmo arquivo. Por isso **o confirm recalcula tudo dentro da transação**, e o índice único
é o árbitro final: linha que virou duplicata no meio do caminho volta no resultado como `blocked`, sem
derrubar o lote.

---

## 5. Contrato HTTP

Base `/api/v1`, todas as rotas sob `RequireAuth`, envelope de erro existente, **404 para recurso de
outra casa**. **Spec-first (ADR-006):** `backend/api/openapi.yaml` é editado **antes** do handler;
`npm run api:gen` regenera `schema.gen.ts`; `routes_test.go` e `npm run api:check` seguram a aderência.

### 5.1 Lançamentos

| Método | Rota | Sucesso | Observações |
|---|---|---|---|
| GET | `/transactions` | 200 | `?month=YYYY-MM` (obrigatório) · `?accountId=` (opcional) · `?limit=` ≤ 100, default 50 · `?cursor=` |
| GET | `/transactions/{id}` | 200 | 404 se de outra casa |
| DELETE | `/transactions/{id}` | 204 | exclusão lógica; transferência **exclui o par inteiro** (ADR-016) |

`month` filtra **`competence_month`** (ADR-023): um só conceito de mês no app inteiro. Em conta que não
é cartão, competência é igual a caixa e nada muda; no cartão, é o comportamento que a pessoa espera.

Resposta:

```json
{
  "items": [{
    "id": "...", "kind": "expense", "accountId": "...", "accountName": "Nubank Conta",
    "categoryId": null, "categoryName": null,
    "amountCents": 2000, "description": "Pix enviado - Fulano de Tal Silva",
    "occurredOn": "2026-08-04", "yearMonth": "2026-08", "competenceMonth": "2026-08",
    "transferGroupId": null, "statementId": null,
    "source": "import", "importBatchId": "...",
    "createdBy": "...", "createdAt": "...", "updatedAt": "..."
  }],
  "nextCursor": "b2NjPTIwMjYtMDgtMDR8aWQ9...",
  "summary": { "incomeCents": 1279750, "expenseCents": 1127880, "netCents": 151870,
               "count": 13, "uncategorizedCount": 13 }
}
```

O `summary` vem no mesmo payload de propósito (`PLANOS.md` §7.2): a tela mostra os totais do filtro e
não vale uma segunda ida ao servidor com risco de divergir do que está na tela.
`uncategorizedCount` existe por causa da D3 (§3.4) — é o gancho da chamada "N lançamentos sem
categoria".

**Cursor:** base64url de `occurredOn|id`, **validado estritamente** (data civil por `civil.Parse` mais
forma de UUID). Qualquer coisa fora disso é **400 sem detalhe** (S5 do `PLANOS.md`). Assinatura HMAC
não é necessária: o cursor não carrega autorização, e a casa vem do token.

### 5.2 Faturas

| Método | Rota | Sucesso | Observações |
|---|---|---|---|
| GET | `/card-statements` | 200 | `?accountId=` · `?month=YYYY-MM`; devolve `totalCents`, `paidCents` e `status` derivados |
| GET | `/card-statements/{id}` | 200 | com os lançamentos da fatura paginados pelo mesmo cursor |

Sem `POST`, `PATCH` ou `DELETE`: no v1 a fatura nasce e morre com a importação. Superfície que não
existe não precisa ser revisada.

### 5.3 Importação — as três fases

| Método | Rota | Sucesso | Corpo |
|---|---|---|---|
| POST | `/imports` | 201 | **`multipart/form-data`** |
| GET | `/imports` | 200 | histórico dos últimos 50 lotes da casa |
| GET | `/imports/{id}` | 200 | preview paginado (`?limit=` ≤ 200, default 100; `?cursor=` é o `seq`) |
| POST | `/imports/{id}/confirm` | 200 | JSON com **exceções** |
| DELETE | `/imports/{id}` | 204 | descarta o lote e apaga as linhas |

**Partes do multipart** — no máximo 5, lidas por `r.MultipartReader()`; **nunca**
`ParseMultipartForm`, que grava em disco (§6.2):

| Parte | Obrigatória | Limite | Regra |
|---|---|---|---|
| `file` | sim | 8 MiB | `.csv` ou `.zip`; o tipo real é decidido pelos **magic bytes** (`PK\x03\x04`), nunca pela extensão nem pelo `Content-Type` da parte |
| `accountId` | sim | 36 B | validado como da casa → **404** se não for |
| `password` | não | 128 B | só para ZIP; `[]byte`, zerada após o uso; **nunca** gravada, logada, auditada ou ecoada |
| `format` | não | 40 B | id de parser da allowlist; só necessário quando a detecção é ambígua |

Resposta 201:

```json
{
  "id": "...", "status": "pending", "expiresAt": "2026-09-17T13:00:00Z",
  "accountId": "...", "institution": "nubank", "docKind": "card_statement",
  "formatId": "nubank.card_statement.v1", "fileName": "Nubank_2026-09-13.csv",
  "rowCount": 15, "minDate": "2026-08-06", "maxDate": "2026-09-05",
  "counts": { "novo": 12, "repetido_no_arquivo": 1, "duplicado_exato": 0,
              "duplicado_excluido": 0, "possivel_duplicado": 1,
              "pagamento_de_fatura": 1, "rejeitado": 0 },
  "statementSuggestion": { "competenceMonth": "2026-09", "closingDate": "2026-09-05", "dueDate": "2026-09-13" },
  "sameContentImportedAt": "2026-09-14T10:02:00Z",
  "encoding": "utf-8"
}
```

`statementSuggestion` é **sugestão**: vem de `accounts.statement_due_day` e `statement_closing_day`
quando configurados, e cai para uma inferência a partir da maior data do arquivo quando não. **O nome
do arquivo entra como pista apenas na sugestão exibida, nunca como regra** — nome de arquivo é entrada
do cliente, e uma fatura arquivada no mês errado estraga a competência de dezenas de linhas.

**`POST /imports/{id}/confirm`** — corpo pequeno de propósito: só as **exceções** ao default, com teto
de **2.000** decisões (com 10.000 `rowId` o corpo passaria do limite de 1 MiB).

```json
{
  "decisions": [
    { "rowId": "...", "action": "import" },
    { "rowId": "...", "action": "skip" },
    { "rowId": "...", "action": "import", "categoryId": "..." },
    { "rowId": "...", "action": "transfer", "counterpartAccountId": "...", "statementId": "..." }
  ],
  "statement": { "competenceMonth": "2026-09", "closingDate": "2026-09-05", "dueDate": "2026-09-13" },
  "defaultCategoryId": null
}
```

- `statement` é **obrigatório** quando `docKind = card_statement` e **proibido** no extrato (400).
- Linha não citada usa o default da §4.6. **Linha marcada só entra com `action: "import"` explícito** —
  é a exigência do usuário, escrita no contrato.
- `action: "transfer"` só é aceito em linha `pagamento_de_fatura`; cria o **par** (ADR-016) dentro da
  mesma transação.
- **Idempotente:** o commit faz
  `UPDATE import_batches SET status='committed' WHERE id=? AND household_id=? AND status='pending'`.
  Se afetou zero linhas e o lote já está `committed`, responde **200 com o mesmo resultado** (S9 do
  `PLANOS.md`: duplo clique em conexão ruim não importa duas vezes). Lote `discarded` ou `expired`
  responde **404**.
- **Tudo ou nada:** um `UnitOfWork` para o lote inteiro (S10). Linha bloqueada não é falha do lote — é
  linha não inserida, reportada na resposta.

Resposta 200:

```json
{ "id": "...", "status": "committed",
  "imported": 12, "restored": 1, "skipped": 2, "blocked": 0, "rejected": 0,
  "statementId": "...", "transfersCreated": 1,
  "blockedRows": [ { "rowId": "...", "reason": "duplicado_exato" } ] }
```

### 5.4 Códigos de erro novos

Entram em `internal/platform/httpserver`, na enum `ErrorCode` do OpenAPI e em `frontend/src/lib/errors.ts`.

| Código | HTTP | Quando | Por que merece código próprio |
|---|---|---|---|
| `IMPORT_PASSWORD_REQUIRED` | 422 | ZIP com bit de cifra e sem senha | a tela precisa **abrir o campo de senha**, não dizer "dados inválidos" |
| `IMPORT_PASSWORD_INVALID` | 422 | CRC não bate depois de decifrar | a tela precisa **manter o campo e pedir de novo** |
| `IMPORT_FORMAT_UNKNOWN` | 422 | nenhum parser reconheceu o cabeçalho | a tela explica quais formatos existem — **é a resposta para arquivo do C6 hoje** |
| `IMPORT_FORMAT_AMBIGUOUS` | 422 | mais de um parser casou | a tela mostra um seletor com os candidatos, que vêm em `fields.format` |
| `IMPORT_TARGET_MISMATCH` | 422 | instituição diferente da conta, ou fatura em conta que não é cartão | a tela manda **trocar a conta de destino**, não o arquivo |
| `IMPORT_FILE_REJECTED` | 422 | limites: tamanho, linhas, razão de compressão, entradas no ZIP, janela larga demais, arquivo vazio, mais de 20% de linhas rejeitadas | `fields` diz qual limite; um código só para todos os limites evita inflar a enum |

Seis códigos é o mínimo em que cada um mapeia para **uma ação diferente da interface**. Colapsar em
`VALIDATION_FAILED` obrigaria o frontend a interpretar texto em português — que é exatamente o que a
D4 da spec 0003 recusou.

Conta **arquivada** como destino é recusada com 422 `VALIDATION_FAILED` e `fields.accountId`, seguindo
o padrão já existente.

### 5.5 Onde mora a análise entre as duas fases, e por quanto tempo

**Mora no banco, em `import_batches` e `import_rows`, por 24 horas.** As alternativas e por que caem:

- **Sem estado** (o cliente devolve as linhas no confirm): o confirm vira um `POST /transactions` em
  lote com passos a mais; o cliente poderia mandar valores que o arquivo não tinha, e a frase
  "importei o meu extrato" deixa de significar alguma coisa. Ainda exigiria reanálise de duplicatas no
  servidor. Recusado.
- **Cache em memória:** morre no deploy, não funciona com duas instâncias, e transforma "confirmar"
  numa aposta. Recusado.
- **Banco:** payload de confirm pequeno, análise à prova de adulteração, fluxo retomável em outro
  dispositivo, e o mesmo `UnitOfWork` de sempre. **Escolhido.**

**24 horas** porque é dado financeiro parado numa tabela nova — quanto menos tempo, melhor —, mas menos
de um dia transformaria "vou revisar depois do jantar" em "começa tudo de novo". Descarte explícito
pelo `DELETE`, expurgo pelo janitor, e **as linhas são apagadas fisicamente no confirm**.

### 5.6 Limites e rate limit

| Limite | Valor | Por quê |
|---|---|---|
| Corpo do `POST /imports` | **8 MiB** | override **por rota** (§6.4); o global continua 1 MiB |
| Entradas no ZIP | exatamente **1**, `.csv`, sem separador de caminho | o ZIP do C6 tem uma; multiarquivo vira ambiguidade e superfície |
| Comprimido / descomprimido / razão | 4 MiB / 8 MiB / **200:1** | zip bomb (§6.1) |
| Linhas de dados | **10.000** | o `usize` real do C6 é 7 KB; 10.000 é folga de duas ordens |
| Caracteres por linha / colunas | 1.000 / 16 | linha única gigante é DoS de memória no `csv.Reader` |
| Janela de datas do arquivo | 5 anos | limita a consulta de dedup |
| Existentes carregados para dedup | 20.000 | idem |
| Decisões no confirm | 2.000 | corpo ≤ 1 MiB |
| TTL do lote pendente | **24 h** | §5.5 |
| Prazo da análise | 15 s de deadline no contexto | o `WriteTimeout` é 30 s; a análise tem de morrer antes |
| Rate limit `POST /imports` | **10/h por casa** e 20/h por IP | é a rota mais cara do sistema (inflate, parse e varredura) |
| Rate limit `confirm` | 30/h por casa | |

O limitador por casa segue o padrão do limitador por conta já existente (`newAccountLimiter`), com a
chave sendo o **HMAC do `household_id`**: id em claro não entra no mapa do limitador, igual ao e-mail
hoje.

---

## 6. Segurança

Superfície nova e séria: é o primeiro ponto do sistema que recebe **arquivo** e o primeiro que recebe
**multipart**. Esta seção é análise prévia e **não substitui** a revisão do `revisor-seguranca`
(critério de aceite 16).

### 6.1 Zip bomb

`io.CopyN(dst, inflater, maxUncompressed+1)` — **nunca** `io.Copy`, e **nunca** confiar no
`UncompressedSize64` do cabeçalho, que é declarado por quem montou o arquivo. Estourou o teto, aborta
com `IMPORT_FILE_REJECTED`. Também é checada a razão comprimido/descomprimido (200:1), a contagem de
entradas (exatamente uma), a ausência de diretório e a ausência de ZIP aninhado — a entrada tem de
terminar em `.csv`. Nota operacional: o `gosec` tem a regra **G110** para bomba de descompressão e
acusa `io.Copy` sobre um descompressor; usar `CopyN`/`LimitReader` mantém o gate limpo **sem
`#nosec`**, que é o objetivo.

### 6.2 Path traversal

Nada é escrito em disco em momento algum — extração 100% em memória. Ainda assim o nome da entrada é
validado (sem `/`, sem `\`, sem `..`, sem caractere de controle, até 255 caracteres) e o `file_name`
guardado é sanitizado, porque ele é exibido na UI e no histórico. **Proibidos explicitamente nesta
rota:** `os.CreateTemp`, `ParseMultipartForm` e `ReadForm` — os três gravam em disco, e o que gravariam
é extrato bancário.

### 6.3 Senha do ZIP

Trafega numa parte do multipart, vive como `[]byte`, é usada e **zerada**. Não vira `string` no caminho
principal, porque string é imutável e não se apaga. **Nunca** entra em `slog`, em `audit_log`, em
`import_batches`, em mensagem de erro nem em resposta. É o CPF do titular — dado pessoal, não apenas
credencial. Está verificado que o middleware de log registra só método, caminho, status, bytes, duração
e IP, sem corpo; isso continua valendo e vira item de revisão.

No frontend: campo `type="password"`, `autocomplete="off"`, **nunca** em `sessionStorage` nem em
`localStorage` (a exceção do `registrationToken` na §1 do `docs/SEGURANCA.md` é fechada, e citá-la aqui
seria violação), apagada do estado do React assim que a requisição sai, e nunca em query string.

### 6.4 Limite de corpo por rota, sem afrouxar o global

Hoje `httpserver.MaxBytes(1 MiB)` está na cadeia **global**, antes do mux. Embrulhar de novo com um
`MaxBytesReader` maior no middleware da rota **não funciona**: quem corta é o `MaxBytesReader` interno,
que já é de 1 MiB. As saídas seriam (a) subir o teto global, inaceitável porque abriria 8 MiB em todas
as rotas de autenticação, ou (b) a cadeia global consultar uma **tabela explícita de exceções por
caminho**.

**Escolhida a (b):** `httpserver.MaxBytesByPath(padrão, map[string]int64{"/api/v1/imports": 8 << 20})`,
com **comparação exata de caminho**, sem prefixo e sem curinga — prefixo faria `/api/v1/importsXYZ`
herdar o teto. A tabela é dado, revisável numa olhada, e tem teste provando que qualquer outra rota
continua em 1 MiB.

### 6.5 DoS por parsing

Tetos de linhas, colunas e caracteres por linha (§5.6) · `csv.Reader` com `FieldsPerRecord` fixado pelo
cabeçalho, de modo que uma linha com 3.000 campos morra na primeira · `LazyQuotes = false` · deadline
de 15 s no contexto da análise · **uma** consulta de dedup, não N (§4.8) · rate limit de 10/h por casa.
Senha errada custa um inflate limitado a 8 MiB, sob o mesmo rate limit.

### 6.6 BOLA — o risco nº 1 (docs/SEGURANCA.md §2, S1 do PLANOS.md)

- `import_batches` e `import_rows` têm `household_id` **próprio**, redundante de propósito: o filtro
  nunca depende de join. Toda consulta de staging é `WHERE household_id = ? AND ...`, com o id da casa
  vindo do token.
- Lote de outra casa em `GET`, `confirm` e `DELETE` responde **404**, byte a byte igual à resposta de
  lote inexistente.
- `accountId`, `categoryId`, `counterpartAccountId` e `statementId` são validados como da casa
  **dentro da transação da escrita** — não antes, para não abrir janela de TOCTOU — e falham com 404.
- `rowId` só é aceito se pertencer **àquele lote**, e o lote à casa. Decisão com `rowId` de outro lote
  é 400, nunca "ignorada em silêncio".
- O `counterpartAccountId` da transferência é o vetor mais fácil de esquecer, porque cria uma linha
  numa conta **diferente** da conta do lote. Teste dedicado é obrigatório.

### 6.7 Mass assignment (S2)

DTO explícito por endpoint e `DisallowUnknownFields`, que já é o padrão do `DecodeJSON`. O confirm
aceita **apenas** `rowId`, `action`, `categoryId`, `counterpartAccountId`, `statementId` e o bloco
`statement`. Valor, data, descrição e `kind` **vêm do staging**, nunca do cliente. `householdId`,
`dedupKey`, `dedupOrdinal`, `yearMonth`, `competenceMonth`, `descriptionNorm`, `source`, `createdBy` e
`deletedAt` não existem em nenhum DTO de entrada. No multipart, parte com nome fora da allowlist de
quatro é 400 — não se ignora.

### 6.8 Dados de terceiros na descrição — minimização

A descrição do extrato traz, por linha, nome completo do favorecido, CPF mascarado, CNPJ completo,
banco, agência e conta. **Decisão: sanitizar na importação, de forma determinística e versionada.**
Mantemos o **nome** da contraparte, que é a informação útil, e removemos os **identificadores
numéricos** — CPF mascarado, CNPJ, `Agência: N`, `Conta: N` e o código do banco entre parênteses. Uma
linha como "Transferência enviada pelo Pix - Fulano de Tal Silva - (CPF mascarado) - NU PAGAMENTOS - IP
(0260) Agência: 1 Conta: 1000001-1" vira "Pix enviado - Fulano de Tal Silva".

Ganhos: menos dado pessoal de terceiro guardado sem necessidade, descrição legível, e o texto passa a
caber nos 140 caracteres — e **descrição truncada depois do cálculo da chave é bug de dedup garantido**
(§4.3, detalhe 1).

Junto, e obrigatoriamente: remoção de caracteres de controle (`\x00`–`\x1F`, `\x7F`), BOM, zero-width e
dos **overrides de direção Unicode** (U+202A–U+202E, U+2066–U+2069) — o truque "Trojan Source", que
faria a descrição exibir um estabelecimento e guardar outro.

### 6.9 Injeção de fórmula em CSV — e por que NÃO neutralizamos na entrada

A defesa da S7 do `PLANOS.md` é na **exportação**, e continua sendo lá. Neutralizar na entrada
significaria **alterar o dado do usuário** — uma descrição que legitimamente comece com `-` ou `@` —
para proteger um programa de terceiros. Fazemos o contrário: guardamos fiel, já sem caractere de
controle (§6.8), e a E6 neutraliza `=`, `+`, `-`, `@`, TAB e CR na hora de escrever o CSV.

**O que esta entrega deve à E6, e que é dívida declarada (§1.3):** um **teste de regressão nasce agora**
com um lançamento cuja descrição começa com `=`, importado aqui e conferido na exportação da E6. Sem
ele, a ida e volta importar → exportar vira uma fórmula na planilha de alguém, e daqui a três entregas
ninguém lembra de testar.

### 6.10 Auditoria (§4.7 do PLANOS.md) — com um desvio declarado

Toda escrita financeira gera entrada, **dentro da transação**: se a entrada não couber, a escrita não
vale. Ações novas: `import.created`, `import.confirmed`, `import.discarded`, `transaction.deleted`,
`transaction.restored`, `card_statement.created`.

**O desvio, dito na cara:** a confirmação de um lote de 10.000 linhas gera **uma** entrada
(`import.confirmed`, entidade `import_batch`), e **não 10.000** entradas de `transaction.created`.
Motivo: 10.000 linhas de auditoria por importação afogam o rastro que a auditoria existe para preservar
— quem fez o quê —, e a rastreabilidade por lançamento já está no dado, em `transactions.import_batch_id`
mais `created_by` e `created_at`, que é mais preciso do que a auditoria seria. É o mesmo raciocínio que
a spec 0003 usou para não auditar a semente de categorias por ator. Escrita **avulsa** — excluir um
lançamento, restaurar um — continua gerando entrada própria. E nenhuma entrada carrega valor monetário,
nem no lote, onde a tentação seria somar o total (S8).

### 6.11 Resto do checklist

| Item | Tratamento |
|---|---|
| CSRF | `SameSite=Strict` mais `Origin`/`Sec-Fetch-Site` já cobrem multipart (é *simple request*, não tem preflight, e o `CSRFGuard` atual barra cross-site). **Sem mudança**, mas com teste E2E provando que um POST multipart de outra origem leva 403 |
| Content-Type | `/imports` exige `multipart/form-data` (415 caso contrário); todas as outras rotas continuam exigindo `application/json` |
| Encoding | BOM removido; se não for UTF-8 válido, decodifica como **Windows-1252** — e não Latin-1 puro, porque 0x80–0x9F carregam travessão, aspas curvas e o símbolo do euro, que bancos brasileiros usam — via `golang.org/x/text/encoding/charmap`. **`x/text` já é dependência direta** (o `textnorm` usa), então não há dependência nova. O encoding detectado aparece no preview |
| XSS | descrição vem sanitizada da API e é renderizada como texto; `dangerouslySetInnerHTML` continua proibido; o **nome do arquivo** também é conteúdo do usuário e passa pelas mesmas regras |
| Overflow de int64 | valor por linha na faixa da §4.5 do `PLANOS.md`; a soma do `summary` e do saldo tem teto conhecido (10.000 linhas vezes 1e11 é muito menor que 9,2e18) e teste que prova |
| Enumeração | `IMPORT_PASSWORD_INVALID` não é oráculo: o arquivo é do próprio usuário |
| `Raw`/`Exec` | zero. Todo o dedup é igualdade parametrizada, e nenhum `ORDER BY` vem do cliente |
| Segredo em código | fixtures anonimizadas; senha dos ZIPs de teste gerada no próprio teste, nunca literal no repositório |
| Dependência nova | **nenhuma** (ADR-024): ZipCrypto é escrito sobre `archive/zip`, `compress/flate` e `hash/crc32` |

---

## 7. Parsers — plugin, e o que falta para o C6

### 7.1 Estrutura e regras do registro

```
backend/internal/importer/
  types.go        RawRecord, ParsedRow, DocKind, Institution, SignConvention, StatementHint
  parser.go       interface Parser
  registry.go     registro e detecção por cabeçalho (ordem determinística)
  archive/        zipcrypto.go (decifra), open.go (limites, magic bytes, entrada única)
  csvtext/        bom.go, encoding.go, sniff.go (separador), number.go, date.go
  sanitize/       descrição, controle, direção Unicode, truncagem em 140
  dedup/          chave canônica, ordinal, marcação fraca
  nubank/         checking.go, card.go, testdata/ (anonimizado)
  c6/             PENDENTE — checking.go, card.go, testdata/
  service.go  handler.go  repository.go
```

```go
type Parser interface {
    ID() string                                  // "nubank.checking.v1"
    Institution() Institution                    // nubank | c6
    DocKind() DocKind                            // checking_statement | card_statement
    Detect(header []string, sep rune) Confidence // none | weak | exact
    Parse(ctx context.Context, r *csv.Reader, limits Limits) (ParseResult, error)
}
```

Regras do registro, todas motivadas por erro conhecido:

- **Detecção por cabeçalho normalizado** (minúsculo, sem acento, sem BOM, com trim): "todas as colunas
  esperadas presentes, nesta ordem, com extras toleradas ao final". Coluna nova no fim do arquivo do
  banco não quebra a importação; coluna faltando quebra, que é o certo.
- **Separador sniffado** entre `,`, `;` e TAB pela contagem no cabeçalho — CSV brasileiro com `;` é a
  regra, não a exceção, e o C6 tem chance alta de usar.
- **Zero parsers casaram → `IMPORT_FORMAT_UNKNOWN`. Dois ou mais → `IMPORT_FORMAT_AMBIGUOUS`** com os
  candidatos, e o cliente reenvia com `format` explícito. **Nunca escolher "o primeiro que casou"**: é
  assim que um dia a fatura entra pelo parser do extrato, com o sinal invertido do começo ao fim.
- **A convenção de sinal e o formato numérico são DECLARADOS pelo parser, nunca deduzidos.** Valor que
  não obedece à convenção declarada é **linha rejeitada**, não valor "consertado".
- **Sem float em lugar nenhum** (ADR-003): o número é partido em parte inteira e exatamente duas casas
  decimais e montado em `int64`. `strconv.ParseFloat` é proibido neste caminho, e há teste que falharia
  se alguém o usasse.
- **Erro é por linha, não por arquivo** — até 20% de linhas rejeitadas. Acima disso, o arquivo inteiro
  é recusado: 20% de lixo quer dizer parser errado, não dado ruim.

### 7.2 Os dois parsers do Nubank (prontos nesta entrega)

| | `nubank.checking.v1` | `nubank.card_statement.v1` |
|---|---|---|
| Cabeçalho | `Data,Valor,Identificador,Descrição` | `date,title,amount` |
| Data | `DD/MM/YYYY` | `YYYY-MM-DD` |
| Número | `-20.00` — decimal com ponto, sem milhar | `"- 2.859,82"` — decimal com vírgula, milhar com ponto, espaço após o sinal |
| Sinal | negativo é saída | **positivo é saída** |
| Chave natural | `Identificador` (UUID) | **não tem** — chave derivada mais ordinal |
| Classificação | "pagamento de fatura" → `pagamento_de_fatura` | "pagamento recebido" → `pagamento_de_fatura`; demais créditos → `income` marcado "crédito na fatura" |

Os padrões de classificação são uma pequena allowlist de prefixos normalizados dentro do parser, com
teste. Eles **sugerem**; quem decide é o usuário na revisão.

### 7.3 C6 — entregue (17/09/2026), e o que cada item do checklist virou

**Estado normativo: os dois parsers do C6 estão registrados** (`c6.checking.v1` e
`c6.card_statement.v1`, pacote `internal/importer/c6`), montados no `NewRegistry` do `main.go` ao lado
dos do Nubank. Arquivo do C6 agora é reconhecido pelo cabeçalho como qualquer outro; a resposta
`IMPORT_FORMAT_UNKNOWN` deixa de ser a resposta padrão para o C6. O teste-ouro (`c6/c6_test.go`) confere
as duas fixtures anonimizadas linha a linha, com o sinal esperado de cada documento, e prova que cada
uma das quatro fixtures (2 Nubank + 2 C6) casa com **exatamente um** parser.

O checklist abaixo, que era a lista de compras, fica como **registro do que foi preenchido** — para
que a próxima instituição siga o mesmo roteiro:

1. **Amostra anonimizada** dos dois CSVs em `backend/internal/importer/c6/testdata/`, com nomes,
   valores e documentos trocados. O arquivo real **nunca** entra no repositório. → **Feito:**
   `c6_checking_v1.csv` (10 linhas de dados, com preâmbulo) e `c6_card_statement_v1.csv` (10 linhas).
2. **Assinatura do cabeçalho** e **separador** (`,` ou `;`) de cada um dos dois documentos. →
   **Extrato:** `Data Lançamento,Data Contábil,Título,Descrição,Entrada(R$),Saída(R$),Saldo do Dia(R$)`,
   separador **vírgula**. **Fatura:** `Data de Compra;Nome no Cartão;Final do Cartão;Categoria;Descrição;
   Parcela;Valor (em US$);Cotação (em R$);Valor (em R$)`, separador **ponto-e-vírgula**.
3. **Formato de data** e **formato numérico**. → Os dois usam **`DD/MM/YYYY`** e **ponto decimal sem
   milhar** (`9950.00`). O extrato não usa negativo (ver item 4); a fatura escreve o crédito com `-`
   à esquerda (`-500.00`).
4. **Convenção de sinal de cada documento, separadamente** — **é o item que mais custa se vier errado.**
   → **Extrato:** modelo de **DUAS COLUNAS** — `Entrada(R$)` vira `income`, `Saída(R$)` vira `expense`,
   uma delas sempre `0.00`. **NÃO** usa `KindFromSigned`; declara a própria convenção em
   `kindFromDuasColunas` (as duas zero → `zero_amount`; as duas > 0 → `invalid_amount`). **Fatura:**
   valor com sinal, **positivo é saída** (`SignPositiveIsOutflow`), como a fatura do Nubank.
5. **Existe identificador por linha?** → **Não**, em nenhum dos dois. `ExternalID` fica nil e ambos caem
   na chave derivada com ordinal (por isso a fixture da fatura tem o par idêntico "PADARIA EXEMPLO").
6. **Preâmbulo e rodapé.** → **Extrato tem preâmbulo de 8 linhas** (título do banco, agência/conta,
   geração, período, linhas em branco) antes do cabeçalho real na 9ª linha física. Este foi o **primeiro
   exercício real** do mecanismo de pular-preâmbulo do núcleo (`OpenDocument`), coberto por teste
   dedicado; funcionou sem ajuste. A fatura não tem preâmbulo. Nenhum dos dois tem rodapé de dados.
7. **A fatura traz fechamento, vencimento ou total no arquivo?** → **Não** no corpo do CSV da amostra:
   `ParseResult.Statement` fica nil e a competência vem de `accounts.statement_closing_day /
   statement_due_day`, como no Nubank.
8. **Parcelamento** (`Parcela 3/10`): preservar no texto da descrição, e nada além disso no v1. →
   **Feito:** a coluna `Parcela` traz `2/7`, `1/12` ou `Única`; quando não é "Única", vira sufixo
   ` · <parcela>` na descrição (ponto médio, não hífen, para o sanitize não mexer). "Única" e vazio não
   acrescentam nada.
9. **Encoding** (UTF-8 ou Windows-1252) e terminador de linha. → Amostras em **UTF-8** com **CRLF**; o
   extrato ainda traz **BOM** (removido pelo `csvtext`). O núcleo lida com os três sem o parser saber.
10. **ZIP:** padrão do nome da entrada e, sobretudo, **método de cifra**. Hoje verificado como
    **ZipCrypto** (§2.1). Se algum dia vier `method=99` com campo extra `0x9901`, é AES do WinZip e cai
    no erro explícito e testado — implementável só com stdlib (`crypto/aes` mais `crypto/pbkdf2`, que
    existe desde o Go 1.24), mas **não nesta entrega**.
11. **A senha é o CPF sem pontuação.** A UI **explica o formato e não pré-preenche nada**: o app não
    tem o CPF do usuário e não vai passar a ter por causa disto.

---

## 8. Frontend

### 8.1 `/lancamentos`

Tabela densa agrupada por dia, com subtotal do dia; valor tabular alinhado à direita com `MoneyText`
(sinal explícito, **nunca só cor**); colunas de conta, categoria — com o estado "sem categoria" visível
—, descrição e valor. `MonthNavigator` já existente, com o mês na URL (`?mes=`), e o filtro de conta
também na URL (`?conta=`); "carregar mais" por cursor. Excluir abre `ConfirmDialog` e diz **em texto**
quando a exclusão afeta o par de transferência. Estados vazio, erro e carregando com `EmptyState` e
`Skeleton`. Faixa persistente "N lançamentos sem categoria" quando houver — sem ela, a D3 (§3.4) vira
dívida invisível.

### 8.2 `/importar` — três passos, um por estado na URL

1. **Enviar** — `Select` de conta (mostrando a instituição), `FileField` com `<input type="file">`
   **real e rotulado** (arrastar e soltar é melhoria, nunca o único caminho), campo de senha que **só
   aparece** quando o arquivo é `.zip` ou depois de `IMPORT_PASSWORD_REQUIRED`, e o texto explicando
   que a senha do C6 é o CPF só com números e que ela não é guardada.
2. **Revisar** — resumo por status no topo, em número e em texto; tabela densa com a marcação de
   duplicata como `Badge` **com palavra** ("possível duplicata", "já importada", "2ª ocorrência"),
   nunca só cor; ação por linha (incluir, ignorar, registrar como transferência); seletor de categoria
   opcional por linha; e, em fatura, um bloco de confirmação de competência, fechamento e vencimento
   pré-preenchido pela sugestão. O botão de confirmar diz o que vai acontecer: "Importar 12
   lançamentos · 3 ignorados".
3. **Resultado** — números reais da resposta (importados, restaurados, ignorados, bloqueados), link
   para `/lancamentos` no mês afetado e, quando houver pagamento de fatura ignorado, o aviso de que **o
   saldo do cartão não volta a zero sem registrar o pagamento como transferência**.

Acessibilidade: cada passo anuncia o resultado por `aria-live`; a marcação de duplicata é texto, não
cor; o foco é gerenciado na troca de passo; o `<input type="file">` nunca fica escondido atrás de uma
`<div>` clicável.

### 8.3 Componentes e cliente

Componente novo: **`FileField`**, e só ele. O resto reusa `DataTable`, `Dialog`, `Badge`, `Select`,
`Toast`, `EmptyState`, `MoneyText` e `Panel`. **`DateField` não nasce nesta entrega**: sem criação
manual, não há campo de data.

**Ajuste obrigatório em `frontend/src/api/client.ts`:** hoje o `send()` define
`Content-Type: application/json` sempre que há corpo. Com `FormData` isso **quebra o multipart**, porque
o navegador precisa definir o `boundary`. O cliente tem de detectar `FormData`, não mexer no
`Content-Type` e passar o corpo direto. Sem isso o upload falha com 400 e ninguém entende por quê.

Tipos: `npm run api:gen` regenera `schema.gen.ts` e `npm run api:check` quebra o build se a spec
divergir (ADR-015). **Nenhum tipo de payload é escrito à mão.**

---

## 9. Testes que esta entrega precisa provar

| Camada | Casos |
|---|---|
| Domínio (unidade) | número pt-BR e en sem float (`"- 2.859,82"` → `-285982`; `"33,70"` → `3370`; `-20.00` → `-2000`) · data nos dois formatos · sanitização de descrição (identificadores, controle, direção Unicode, truncagem em 140) · chave canônica e ordinal · convenção de sinal por parser |
| Repositório (SQLite) | **isolamento por casa em toda consulta**, inclusive nas agregadas (soma de saldo e `summary`) · índice único rejeitando `(key, ordinal)` repetido · cursor estável sob escrita concorrente |
| Parser (golden) | as duas fixtures anonimizadas produzem exatamente as linhas esperadas, **com o sinal certo em cada documento** |
| Arquivo | ZIP sem senha, com senha errada, com senha certa · duas entradas · entrada não-`.csv` · ZIP aninhado · nome com `../` · bomba 200:1 · AES recusado |
| Handler (`httptest`) | 404 para lote e ids de outra casa · 400 com `fields` · 413, 415 · multipart com parte desconhecida · confirm idempotente |
| Abuso | os casos da §10, itens 10 a 13 |
| Consistência | soma de saldos estável após sequência aleatória de importações, exclusões e restaurações (pega bug de transferência e de ordinal) |
| Front (unidade) | máscara e formatação pt-BR · marcação de duplicata legível sem cor · estados vazio, erro e carregando · três passos por teclado |
| Front (E2E) | importar CSV → revisar → confirmar → ver na lista → excluir; POST multipart de outra origem barrado |
| Multi-banco | a suíte de repositório em Postgres e MySQL, uma vez (gatilho R2 do `PLANOS.md`) |

---

## 10. Critérios de aceite

Verificáveis. Os casos de abuso estão no meio, não num apêndice.

1. **Saldo real.** Conta com saldo de abertura e lançamentos importados mostra `balanceCents` igual à
   abertura mais a soma, e o `totalBalanceCents` da lista bate com a soma das linhas exibidas.
2. **A dívida da spec 0003 é paga.** `DELETE /accounts/{id}` e `DELETE /categories/{id}` com lançamento
   associado respondem **422 `RESOURCE_IN_USE`**, com teste.
3. **Importação feliz (extrato Nubank).** Importar a fixture de 13 linhas na conta certa cria os
   lançamentos com os sinais corretos — 4 entradas como `income` e 9 saídas como `expense`, sendo que
   a linha "Pagamento de fatura" fica **ignorada por default**, resultando em 12 lançamentos —, com
   datas em `YYYY-MM-DD` e descrição sanitizada.
4. **Importação feliz (fatura Nubank).** Importar as 15 linhas cria a fatura `2026-09` com
   `closingDate` e `dueDate` confirmados: 13 despesas, 1 crédito ("Ajuste a crédito") e 1 pagamento
   ignorado por default. **O teste falha se algum sinal estiver invertido** — é o caso que mais custa
   se passar despercebido.
5. **Deduplicação — os cinco cenários, cada um com teste próprio:**
   1. reimportar o **mesmo arquivo**: 100% das linhas marcadas e **zero** importadas com o default;
   2. **compra legítima repetida** — o par `Cafe Exemplo` idêntico da fixture: as duas entram na 1ª
      importação e as duas são barradas na 2ª. **Nenhuma some, nunca**;
   3. **período repartido** — arquivo A (01–15) e depois arquivo B (01–31): só as linhas de 16–31
      entram, e as de 01–15 vêm marcadas;
   4. **descrição que mudou** entre downloads: a chave derivada não casa, mas a marcação fraca pega e a
      linha vem barrada por default;
   5. **liberação caso a caso**: linha marcada só entra com `action: "import"` explícito; sem a
      decisão, não entra.
6. **A garantia é do banco, não do código.** Um teste que tente inserir dois lançamentos com o mesmo
   `(household_id, dedup_key, dedup_ordinal)` **falha na violação do índice único**, e o serviço
   traduz isso em "linha bloqueada", nunca em 500.
7. **Restauração.** Excluir um lançamento importado, reimportar o arquivo e liberar a linha devolve o
   **mesmo** `transaction.id`, com os mesmos valores, e a auditoria registra `transaction.restored`.
8. **Idempotência do confirm.** Dois `POST /confirm` do mesmo lote, simultâneos e sequenciais, produzem
   **um** conjunto de lançamentos e a mesma resposta.
9. **ZIP.** ZIP cifrado sem senha responde `IMPORT_PASSWORD_REQUIRED` — e **não** "arquivo corrompido";
   senha errada responde `IMPORT_PASSWORD_INVALID`; senha certa importa. ZIP com duas entradas, com
   entrada que não é `.csv`, com ZIP aninhado, com nome `../x.csv` e bomba de 200:1 respondem todos
   `IMPORT_FILE_REJECTED`, e **nada é escrito em disco** (teste observando o diretório temporário).
10. **Abuso — BOLA.** `accountId`, `categoryId`, `counterpartAccountId` e `statementId` de outra casa
    respondem **404** em todas as rotas, com corpo byte a byte igual ao de id inexistente. `GET`,
    `confirm` e `DELETE` de lote de outra casa respondem 404. `rowId` de outro lote responde 400.
11. **Abuso — entrada.** Corpo com `householdId`, `dedupKey` ou `amountCents` no confirm responde 400.
    Multipart com parte fora da allowlist responde 400. `Content-Type: application/json` em `/imports`
    responde 415. Arquivo de 9 MiB responde 413. Cursor forjado responde 400 sem detalhe. `limit=10000`
    é recusado. `month=2026-13` e `month=abc` respondem 400.
12. **Abuso — conteúdo hostil.** CSV com 20.000 linhas é rejeitado; linha de 1 MB é rejeitada; 3.000
    colunas são rejeitadas; descrição com `=SOMA(A1:A9)`, com byte nulo, com U+202E e com 500
    caracteres é armazenada sanitizada e truncada, **sem** virar fórmula na exportação (o teste de
    regressão que a E6 herda — §6.9).
13. **Vazamento.** Nenhum teste encontra a senha do ZIP em `slog`, em `audit_log`, no banco ou na
    resposta. Nenhuma entrada de auditoria carrega valor monetário. Nenhum e-mail ou id sensível em log.
14. **Portabilidade.** O `AutoMigrate` do v2 **povoado** para o v3 passa, e a suíte de repositório roda
    em **SQLite, Postgres e MySQL** ao menos uma vez (gatilho R2 do `PLANOS.md`), com a saída real
    reportada.
15. **Gates.** `backend/scripts/check.ps1` completo: gofmt, build, vet, `go test -race -count=1`, build
    sem CGo, `govulncheck` e `gosec` — **sem nenhum `#nosec` novo**. Frontend com `tsc`, `biome`,
    `vitest`, `build`, `npm audit` e `api:check` limpos. E2E verde para importar CSV, revisar,
    confirmar, ver na lista e excluir.
16. **Revisão de segurança com veredito literal APROVADO.** Achado crítico ou alto bloqueia a entrega.

---

## 11. Riscos desta entrega

| # | Risco | Prob. | Mitigação |
|---|---|---|---|
| RE1 | **O formato do C6 nunca chega** e a entrega fica "metade importada" | média | A entrega é útil sem o C6: o Nubank funciona inteiro. O C6 é um arquivo de parser mais uma fixture, e entra depois sem tocar no núcleo. A §7.3 é a lista de compras |
| RE2 | **Sinal invertido escapa** num parser futuro | média | Convenção **declarada** por parser, teste-ouro obrigatório por parser, e a soma do arquivo conferida contra o extrato na revisão |
| RE3 | **A entrega infla** — ela já é maior que a E2 original | **alta** | O usuário decidiu em 16/09/2026 construir **tudo de uma vez**. O corte fica registrado como contingência, não como plano: **E2a** = lançamentos, listagem, saldo e importação de extrato; **E2b** = fatura e importação de fatura. A fronteira já está no schema, porque a fatura é tabela e colunas separadas. Se a execução passar de cerca de três dias, é aqui que se corta |
| RE4 | ZipCrypto escrito à mão com bug sutil | baixa | Vetores de teste contra os dois ZIPs reais, CRC como veredito, e o fato de o código não rodar sobre nada além de um `io.Reader` limitado |
| RE5 | Staging vira tabela grande e esquecida | baixa | TTL de 24 h, janitor, e linhas apagadas no commit — tudo com teste de relógio injetado |
| RE6 | `competence_month` semeia divergência em E4–E6 | média | O ADR-023 decide **agora** que o mês do app é competência, e as duas colunas são sempre gravadas |
| RE7 | O usuário importa na conta errada e descobre tarde | média | Trava de instituição, trava de `kind` e o resumo do passo 3 dizendo conta e período |
